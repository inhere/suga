# suga 安全计划 0.1 评审：Spec/Executability 轴，第 1 轮

## 候选四元组

- git_root：`/d/work/inhere/my-tools-dev/inhere-tools/suga`
- candidate_commit：`962b3974f134574aaab20203718509187826b07a`（HEAD，提交说明 `docs(plans): safety plan draft 0.1`）
- subject：`docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md`
- revision：0.1

评审者：spec 轴独立子 agent（REPORT_ONLY，只读，没有改仓库、没有提交）。
基准：已批准设计 `docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md` 0.5。

## 实际读取范围

- 计划全文、设计 0.5 全文；`gofer issue show suga-flei`。
- `pkg/agentcore/tools/rules/rules.go`（`Policy.Decide` :148-170，`ClampAllow`/`IsProtectedPath` :258-305）。全仓 grep 了 `ClampAllow(`、`.Decide(` 的生产调用处与测试调用处。
- `internal/wiring/approval_policy.go:50-215`、`chat_approver.go:100-300`、`origin_guard.go:1-130`、`danger_guard.go:25-110`、`memory_runtime.go:55-150`、`runtime.go:250-330, 370-390, 440-450, 575-600, 660-690, 815-850`、`runtime_build.go:60-110`、`capabilities.go:295-330, 452-495`，以及 `nativeEnv(` 的全部调用处。
- `internal/acpserver/policy.go:20-330`（`PolicyDecision`、`sessionPolicy`、`permissionHook.Decide/decide`），grep 了 acpserver 测试里的 Policy 字面量。
- `internal/cli/run.go:240-300`、`internal/cli/acp.go:1-110`、`internal/output/streamjson.go:15-40`、`internal/chat/layout.go:76-120`、`internal/i18n/catalog/*/status.ini`（只 grep）。
- `internal/personal/runner.go:330-350, 407-440, 485-575`、`compact.go:455-500`、`observe.go:320-355`，以及 `Injected:` 的全部写入点。
- `pkg/agentcore/tools/approval.go:185-280`、`pkg/agentcore/api/provider.go`（grep）、`pkg/agentcore/redact`（grep）。
- `pkg/sugasdk/session.go:80-120`、`types.go:36-90`。
- `pkg/agentcore/tools/native`：`resolveRead`/`readFileInRoot` 的全部调用处、`file.go:80-200`、`file_guard.go`、`find.go`、`grep.go`、`refs.go`、`refs_context.go`、`file_symbols.go`、`walk.go`（grep），`catalog.go` 的 `Env`。
- `internal/config/settings_registry.go`（:76、:90-106、:205-276、:523）、`providers.go:150-170`。
- `scripts/acpprobe/acp_probe.py`（参数与启动行）、`scripts/eval/` 目录（确认没有独立 go.mod）。
- 没读：设计评审目录的各轮正文、`docs/design` 里的前序设计、ptye2e 源码。

## 结论

**FAIL**：有 2 项 CORE_BLOCKING（F1、F2，都属 SECURITY），另有 10 项 CORE_CORRECTIVE、1 项 DEFERRED_ENHANCEMENT。

## 发现

### F1 [HIGH][CORE_BLOCKING][SECURITY] `trust` 怎么落到策略上没写明；“非保护 ask → allow”的事后转换会放过保护路径和 `--read-only` 的 MCP 边界

- 事实：
  - `ClampAllow` 只在 `action == Allow` 时降级（rules.go:261-263）。
  - ACP 的缺省动作固定是原生 Ask（approval_policy.go:72-74，`compileACPApprovalPolicy` 没有 auto 参数）。
  - chat 在 `tools.approval: ask` 时原生动作也是 Ask。
  - 于是在这些策略下，对 `.git/...` 写入的缺省 Ask 根本没被降级，计划接口定义的 `protected`（“命中保护路径而被降级时为 true”）是 false。
  - `approvalPolicy.Decide` 在 `--read-only` 下对 MCP 返回 `(Deny, hit==nil)`（:190-194），外观和“MCP 默认 Deny”一样。
- 计划 T2 动作 2 只写了“规则与默认动作的非保护 ask → allow；MCP 默认 Deny/Ask → allow”，没说 `trust` 是先把默认动作按 auto 编译（原生 Allow，ClampAllow 才会触发），还是在结果上事后转换。也没说 chat 在 `--approve trust` 下 `chatAuto` 取什么值、ACP 的 auto/trust 怎么编译默认动作。
- necessity proof：
  - core_acceptance：设计硬限制“保护路径 ask（chat/ACP 询问）”“`--read-only` 优先”，以及计划目标 2“硬限制全部保持”。
  - necessary_causal_chain：按计划字面做事后转换，会出现下面两条：
    1. `suga acp --approve trust`：ACP 默认 Ask，对 `.git/hooks/x` 的 Ask 是 protected=false，被转为 allow，写入不再询问。
    2. `suga run --read-only --yolo`：只读 MCP 的 Deny（hit==nil）被当作“MCP 默认 Deny”放行。
  - minimal_owner：计划 T2 动作 2 加一句。`trust`（以及 ACP auto）在 `defaultApprovalDefaults`/`compileApprovalPolicy` 层把原生与 MCP 默认动作编译为 Allow，并保持 ClampAllow 与 read-only 判定在其后执行。被放行的 ask 只限规则命中的、未降级的 Ask。同时写明 chat trust 时 `chatAuto=true`。
  - deletion_alternative：不改的话，实施者只能自行选择机制，两种做法的安全结果不同。
  - scope_effect：不新增机制，只确定已批准设计“trust 在 auto 之上”的落点。
- 还要补的测试：ACP ask 缺省加 trust 写 `.git` 仍询问；`--read-only --yolo` 下 MCP 仍拒绝。

### F2 [HIGH][CORE_BLOCKING][SECURITY] dangerGuard 在 `trust` 下“放行”会绕过内层审批器

- 事实：现有 chat 路径里，人输入 yes 后 `dangerGuard.confirm` 直接 `return nil`（danger_guard.go:75-100），不再调用 `g.inner.Decide`。
- 计划 T2 只写“dangerGuard 非 hard 目标在 trust 下放行”。照现有写法，`trust` 下的危险删除会跳过：
  - 规则显式 deny（设计硬限制）；
  - SDK 宿主 Approver（设计矩阵：SDK 由宿主决定）。
- necessity proof：
  - core_acceptance：硬限制“规则中的显式 deny”，以及计划 T2 测试“SDK：trust 下宿主 Approver 仍被调用”。
  - necessary_causal_chain：`trust` 加 `shell_exec rm -rf ./build`（非 hard），守卫 return nil，用户的 deny 规则或宿主 Approver 都不再被调用。
  - minimal_owner：T2 写明“trust 下跳过本守卫的确认，交给 `g.inner.Decide`”（与 originGuard 的“跳过”同义），并补两条用例：trust 加显式 deny 规则命中危险删除时拒绝；SDK trust 下危险删除仍调用宿主 Approver。
  - deletion_alternative：没有。
  - scope_effect：只是措辞加测试。

### F3 [MEDIUM][CORE_CORRECTIVE] T0 的落点与顺序跑不通

1. 修 ACP 需要的保护信息拿不到：
   - `permissionHook` 只拿到 `PolicyDecision` 返回的三个字符串（policy.go:27），没有 workspace/SUGA_HOME/规则文件，没法在 acpserver 里调 `IsProtectedPath`。
   - 能判定的地方在 wiring 的 `ACPPolicy` 闭包（runtime.go:829-836）。
   - 所以 T0 必须扩 `PolicyDecision` 的返回值，但计划把这项放在 T2。T0 的 owner 也缺 `internal/wiring/runtime.go`、`internal/acpserver/server.go`，以及 4 个用到 Policy 字面量的 acpserver 测试（approved_note、mcp_annotations、policy_table、server）。
   - flei 的场景是 ACP 默认 Ask，本来就没降级，因此只能直接用 `IsProtectedPath`，T1 的“降级标记”修不了它。
2. chat 不受 flei 影响：chat 的会话规则以 `SourceSession` 追加进 `Policy.Rules`，走 `rules.Policy.Decide` 时 winner 已经被 ClampAllow 处理（rules.go:162-164、chat_approver.go:142,241）。计划的反向验证“去掉新增检查后 chat 用例失败”做不到，chat 只能写成特征测试。
3. T0 不再改 `rules.go`（会话规则并不在那里）。T0/T1 文件相交的说法因此不成立。
- 建议：
  - T0 owner 改为 `runtime.go:829`（闭包返回 protected=`IsProtectedPath(...)`）、`acpserver/policy.go`（`decide` 在 protected 时忽略会话 allow）、`server.go` 与相关测试；
  - `PolicyDecision` 的扩展从 T2 移到 T0；
  - chat 只保留特征测试。

### F4 [MEDIUM][CORE_CORRECTIVE] T1 的“protected 只传递”没有列出签名波及面

- 当前调用处：
  - `rules.Policy.Decide` 的生产调用只有 approval_policy.go:189，另有测试 command_test/rules_test/path_test/protection_test。
  - wiring 的 `approvalPolicy.Decide` 是真正被调用的那一层：approval_policy.go:160、chat_approver.go:142,277、cli_mcp.go:82、runtime.go:830，测试有 approval_policy_test、subagent_policy_t8_test。
- 计划没说 wiring 的 `approvalPolicy.Decide` 是否改签名，标记“传递”到哪里也没说。T1 owner 不含 approval_policy.go、chat_approver.go、cli_mcp.go、runtime.go（动作 1 却要改 approval_policy.go）。
- 建议：写明 wiring Decide 的新签名（或增加一个只读的 protected 查询），并列出全部调用处与归属任务。

### F5 [MEDIUM][CORE_CORRECTIVE] “本会话最近用户请求”保存在哪里没写

- `Runner` 没有按会话保存的状态（runner.go:407-439）。注入回合来自 nudge、verify、contract、`run --injected`、跨会话消息等，可能出现在新进程里，比如 `suga run --injected` 续接会话。
- 计划没说是放进 Runner 的内存 map（要锁，进程间会丢），还是从 store 取最后一个非注入回合的输入。
- 另外，设计说没有时写“未知”，计划 T1 写 `""`，T5 也没说空值怎么渲染。
- 建议：确定保存来源、并发保护和跨进程行为；写明空值在 Judge 中渲染为“未知”，并补一条用例：新进程中的注入回合。

### F6 [MEDIUM][CORE_CORRECTIVE] ACP/SDK 的档位传递与区分方式没写

1. `permissionHook` 没有档位字段。计划写“permissionHook 按档位处理 ask”，却没说档位经什么到达 acpserver（`Serve`/`Runtime` 新字段？），还是全部在 wiring 的 `ACPPolicy` 里转换。
2. ACP `--approve auto` 要求“原生放行，MCP 照旧询问”，需要改 `compileACPApprovalPolicy`。计划没提。
3. 第 5 项（`memory.write_tainted`）要求 chat/run/acp 放行而 SDK 不变。但 ACP 和 SDK 都直接调 `wiring.Open` 并传入 `MemoryConfirm`（acp.go:79、sugasdk/session.go:93）。计划没给 `Open` 区分 SDK 的依据，比如 Options 增来源字段，或由 SDK 不设置 mode 的 memory 分支。
- 建议：在“跨任务接口”里固定这三点。

### F7 [MEDIUM][CORE_CORRECTIVE] chat “状态行”没有对应的代码位置，i18n 也不在范围内

- chat 没有常驻状态行，档位类信息只出现在启动 banner：`internal/chat/layout.go` 的 `BannerInfo`/`BuildBanner`，文案取自 `internal/i18n/catalog/{en,zh}/status.ini`。spinner 的 `statusLine()` 是另一回事。
- 计划写“internal/chat（状态行）”，没有给符号，scope freeze 里也没有 i18n catalog。
- 建议：写明在 `BannerInfo` 加字段（谁来填）、新增 catalog 键，并把 `internal/i18n/catalog` 列入范围。

### F8 [LOW][CORE_CORRECTIVE] 参数库写错，acp 的参数校验也要改

- suga CLI 用的是 `github.com/gookit/goutil/cflag/capp`（run.go:13-14、acp.go:9），不是 gcli。CAP-03 和共同约束都写了“gcli”。
- `suga acp` 目前没有任何 flag，`RemainArgs` 非空就报错（acp.go:20-22）。要新增 flag 并把档位传进 `RunPersonalACP`，计划只写了“acp 增同两参数”。
- 建议：改为 capp 写法，并写明 `RunPersonalACP` 的签名变化。

### F9 [MEDIUM][CORE_CORRECTIVE] Judge 的调用方式没写，只支持流式的网关上 B 会一直走出错路径

- personal 里的 `generateQuietly` 在 provider 支持流式时用 `Stream`，并另设 prompt-cache scope（compact.go:464-479）。注释写明：有的网关只接受 stream=true，普通 Generate 每次都会失败。
- `internal/originjudge` 不能依赖 personal，计划也没说 Judge 要做同样的流式优先处理。在这类网关上，每次裁决都会出错，回退到 v0.54.8 行为，设计目标 2 在这些环境下落空。
- 建议：T5 动作 1 写明“有 `api.StreamingProvider` 时走 Stream 并丢弃增量，设置独立的 prompt-cache scope”，并补一条用例：只支持流式的假 provider。

### F10 [MEDIUM][CORE_CORRECTIVE] T6 怎么把禁读列表送进 native 环境没写全

- `nativeEnv(workspace, dirs)` 没有 Options 参数，SDK 每个会话的 `ReadDeny` 进不来。调用处有：capabilities.go:452、runtime.go:508/549/846、manifest.go:598、subagent.go:236（子 agent），另有 native_tools.go:46 的 `native.Env{}`。
- `subagent.go` 在 T2 的 owner 里，不在 T6。
- 接口 `ReadDenied(path string) bool  // 由 env 中的已展开列表判定` 是包级函数，要么依赖全局状态（违反“不新增包级可变变量”），要么签名写错。
- 波次一栏“T6 后合入并 rebase”含义不清。
- 建议：
  - `native.Env` 增 `ReadDeny` 字段（已展开的匹配器），判定改为方法或带列表参数；
  - 列出 nativeEnv 的全部调用处，并说明 `Options.ReadDeny` 怎么到达；
  - 把 `subagent.go` 并入 T6 owner，或写明 T2/T6 的先后。

### F11 [MEDIUM][CORE_CORRECTIVE] 遍历类工具没有逐一覆盖，有一个文件名不存在

- `file_symbols` 有目录遍历模式（file_symbols.go:131,162-220 经 `root.ReadFile`），`file_refs` 的上下文行由 refs_context.go:53 另读文件。设计 0.3（S4）要求“遍历类工具的逐文件判定”，计划 T6 只列了 `file_symbols` 单文件一例，`file_symbols` 遍历跳过与 refs 上下文读取都没有覆盖。
- 计划写的 `file_find.go` 不存在，实际是 `find.go`。
- 建议：按 `walkWorkspace` 的调用方（grep、find、refs、file_symbols）逐一列出判定点和测试，并更正文件名。

### F12 [MEDIUM][CORE_CORRECTIVE] T7 的脚本不在范围内，acp_probe 也需要改

- `acp_probe.py` 写死了 `[binary, "acp"]`（:272），传不了 `--approve trust`，得新增参数，比如 `--acp-arg`。
- `scripts/acpprobe`、`scripts/eval/originjudge` 都不在 scope freeze 的代码列表里。T7 自称“只加测试与评测脚本”，却要改已有脚本。
- 可行性已确认：scripts/eval 属于根模块（没有独立 go.mod），可以 import `internal/originjudge`。
- 建议：把两处脚本列入范围，写明 acp_probe 新增的参数。“保护路径仍请求”依赖真实模型愿意写 `.git`，结果不一定稳定，应给出确定的 prompt，或允许用单元测试代替这一项。

### F13 [LOW][CORE_CORRECTIVE] 部分验收没有对应测试

- 缺的用例：
  - `approval=trust` 与 `approval_mode` 的事件 attrs 断言；
  - `--read-only` 加 `--yolo` 时以只读为准并提示；
  - judge 未配置时回退 compact（只测了回退主模型）；
  - chat trust banner 的 ptye2e 反向验证（AGENTS.md 要求新场景做反向验证）。
- “回合事件 attrs 写 approval_mode”没说写在哪个事件，档位怎么进 `personal.Config` 也没说（runner/observe 不知道档位）。
- 建议：在 T2/T5 的测试清单里补上，并指定事件与字段来源。

### F14 [LOW][DEFERRED_ENHANCEMENT] 新配置键的 `Writable` 没定

- 计划没写 `tools.read_deny*`、`tools.origin_judge.*`、`models.judge` 在设置注册表里的 `Writable` 取值。
- 现有 `tools.ask_on_external_origin` 是 Writable（settings_registry.go:222），也就是说模型可以经 shell 执行 `suga config set` 关掉 B/C（ACP 每个 prompt 重新 Open，下一个 prompt 就生效）。
- 这属于设计层面的问题，设计里没有处理。本轮不扩大范围，只建议计划写明取值并记为后续事项。

## 第 4 项（波次与文件相交）小结

- T0/T1 都改 `rules.go` 的说法不成立，T0 实际不改 rules.go（见 F3）。
- 真正的相交：
  - T0 与 T2 都改 `acpserver/policy.go`、`runtime.go`；
  - T2 与 T6 都改 `subagent.go`、`sugasdk/types.go`、设置注册表；
  - T2 与 T5 都改 `origin_guard.go`、`observe.go`、设置注册表。
- 顺序上 T0→T1→{T2,T6}→T5 可行，但计划要写清 T2/T6 谁先合入（F10）。

## 已核实无误的锚点

- `rules.go:162-164`、`:261-305`；`approval_policy.go:60-81,152-171,198-202`；`runtime.go:446,580-594`；`origin_guard.go`（chat 询问、其他入口拒绝）；`streamjson.go:25`（`SessionLine` 只在 run.go:398 调用）；`runner.go:342,495`。
- `api.Provider` 存在（pkg/agentcore/api/provider.go:6），`redact.Secrets` 可用；`ApprovalReport` 加 observe.go:331-349 足够承载裁决 attrs。
- compact provider 在 :662 才构建，晚于审批链（:591），所以计划写的“延迟取值”是必要的。
- `tools.approval` 的枚举已经只接受 ask/auto（settings_registry.go:523），T2 只需补特征测试。
- `file_write` 的结果目前就不含旧内容（file.go:123-133），T6 是特征测试。
