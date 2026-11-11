# 评审报告：suga 可信模式、外部来源裁决与敏感路径禁读 设计（R1 / Spec 轴）

## 1. 评审目标与范围

- 候选四元组：
  - Git root：`/d/work/inhere/my-tools-dev/inhere-tools/suga`
  - candidate commit：`113fa5f5990057684d170a840d5c4b0ddc846290`（`docs(design): record Q1-Q4 decisions, draft 0.2`；工作区干净，HEAD 即该提交）
  - subject：`docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md`
  - revision：Draft 0.2
- 评审者：Spec/Executability 轴独立子 agent，第 1 轮全量读，REPORT_ONLY（未修改仓库、未提交）。
- 实际读取范围：
  - 设计全文（209 行）；issue `suga-kkh4`、`suga-f5la`、`suga-4t8i`。
  - 代码：`internal/wiring/{runtime.go(240-300,370-400,540-620), approval_policy.go(全), chat_approver.go(1-260,470-540), origin_guard.go(全), danger_guard.go(全), subagent.go(100-270), memory_runtime.go(100-175), runtime_build.go(60-110), capabilities.go(452-492), native_tools.go(31-51), manifest.go(590-600)}`；`internal/acpserver/{policy.go(90-330), config_options.go(全), server.go(80-117 grep)}`；`internal/cli/{acp.go(grep), run.go(240-280)}`；`pkg/sugasdk/{session.go(60-140), types.go(41-80)}`；`internal/personal/{runner.go(455-480), compact.go(455-520), observe.go(30-70)}`；`internal/config/settings_registry.go(205,518-530)`；`pkg/agentcore/tools/{origin.go(20-200), approval.go(90-205), output.go(140-160), rules/rules.go(255-275)}`；`pkg/agentcore/tools/native/{readroots.go(全), file.go(208-240), walk.go(95-120), remove.go(140-155), grep.go/find.go/refs.go(grep)}`；`pkg/agentcore/api/*.go`（ctx 键 grep）。
- 未读：`internal/hooks/hooks.go` 仅 grep；`internal/output/streamjson.go` 仅 1-80 与 175-195；xhen 仓库未读；`docs/design` 前序设计未读。

## 2. 输入与方法

- 逐条核对设计“已确认事实”中的文件:行引用；对 A/B/C 的每个落点，沿代码追到数据来源与调用者，判断“按设计能否写出唯一实施计划、能否满足用户可见结果与 D1–D10”。
- 用户已选定的 Q1–Q4（D7–D10）不作为评审对象，仅评其可落地性。
- 同类问题穷尽列出出现点。

## 3. 验收标准

- 设计引用的代码事实属实；
- A 的 6 项放开与硬限制列表能在现有审批链上无歧义实现，四入口语义自洽；
- B 的裁决输入在 originGuard 处可得，provider/用量/回退有确定的装配路径，安全上不留旁路；
- C 的禁读能覆盖声明的工具与路径形态，SDK/配置/子 agent 都生效；
- 无开放 CORE_BLOCKING 为 PASS。

## 4. 发现

### 事实核对（概览）

大部分引用属实：`runtime.go:591` 包装顺序、`approval_policy.go:60-81` 默认动作、`152-171` run 的 ask→Denied、`policy.go:117,183`、`config_options.go:31`、`run.go:249-270`、`runtime_build.go:76`、`compact.go:464/494`、`readroots.go:95-127`、`output.go:150-156`、`streamjson.go:181-192`、`approval.go:194-200`、`danger_guard.go:46-71`、`remove.go:143-151`、`rules.go:261-305`、`origin_guard.go:93-99`、`memory_runtime.go:132-143`、`chat_approver.go:483`。不准确处见 F15。

---

### F1 — HIGH — CORE_BLOCKING（SECURITY）：裁决缓存键只含“工具 + 命中片段”，一次 allow 可被同片段的不同调用复用

- 设计：“同一回合内对‘同一工具 + 同一命中片段’的裁决结果缓存”（设计 L119）。
- 代码事实：`OriginHit.Snippet` 只是第一个命中窗口起点的前 48 字节（`pkg/agentcore/tools/origin.go:108-113,155,170-181`），与调用其余参数无关。
- 后果：模型先发一个良性调用（如 `shell_exec echo "<片段S>"`）得到 `allow`，同回合再发 `shell_exec` 把同一片段 S 嵌入真正执行外部指令的命令，缓存命中直接 allow，裁决模型不再看到第二个调用。
- necessity proof：
  - core_acceptance：用户可见结果“照抄网页里的指令去执行时，裁决模型给出拒绝或询问”（L35）及 D3“出错按现状处理，不放宽”。
  - necessary_causal_chain：call1(tool=T, snippet=S, 良性) → judge allow → cache[(T,S)]=allow → call2(tool=T, snippet=S, 恶意参数) → 命中缓存 → 内层审批器（run/auto 原生默认 Allow）放行 → 执行。
  - minimal_owner：`internal/originjudge` 的缓存键定义（设计 B 节 L119）。
  - deletion_alternative：PASS（删除缓存即消除旁路，代价是重复调用多一次裁决；或键改为“工具 + 规范化参数全文摘要”）。
  - scope_effect：收窄，不扩范围。

### F2 — HIGH — CORE_BLOCKING（SECURITY/AUTHORITY）：`ClampAllow` 在 chat/ACP 输出的是 `Ask`，与“规则/默认 ask”不可区分；trust 的“ask→放行”会绕过 D8 声明的硬限制

- 设计：A 第 1 项“审批规则中命中 ask 的调用 → 放行”，关键流程“ask → trust 放行”（L168）；同时 D8/L100 把 `ClampAllow` 列为 trust 不放开的硬限制。
- 代码事实：`ClampAllow` 对受保护路径在 `ModeChat`/`ModeACP` 返回 `Ask`，仅 run 返回 `Deny`（`pkg/agentcore/tools/rules/rules.go:261-271`）；`approvalPolicy.Decide` 在无规则命中时返回 `(ClampAllow(...), nil)`（`internal/wiring/approval_policy.go:198-201`），调用方拿到的是 `(Ask, hit=nil)`，与“默认动作 Ask”完全相同；chat 的 trust 以 auto 为基（原生默认 Allow），所以 chat/ACP 下凡是 `file_write/file_edit/file_remove` 指向 `.git`、`.suga`、SUGA_HOME、规则文件的调用，恰好就是经 ClampAllow 变成 Ask 的那一类。
- 设计未规定 trust 下这类 Ask 如何处理（继续问人？拒绝？），也未要求 Decide 区分 Ask 的来源。按 L168 字面实现即放行，违反 D8；而 run 下同一调用是 Deny，四入口语义也不一致。
- necessity proof：
  - core_acceptance：D8“`ClampAllow` 对 `.git`/`.suga`/SUGA_HOME/规则文件的降级等硬限制不动”；D1“四入口一致”。
  - necessary_causal_chain：chat `--approve trust` → `file_write .suga/approval-rules...`（或 `.git/hooks/pre-commit`）→ Decide 返回 (Ask,nil) → trust 分支把 ask 放行 → 写入控制面文件（模型可改写自己的审批规则）。
  - minimal_owner：设计 A 节第 1 项与关键流程；实现落在 `approvalPolicy.Decide`（需暴露“clamp 产生的 Ask”）。
  - deletion_alternative：FAIL（ClampAllow 是用户选定 D8 的硬限制，不能删；只能在设计中写明 trust 下 clamp-Ask 的处理，如“仍询问（chat/ACP）/拒绝（无人可问）”）。
  - scope_effect：无扩张，仅澄清 A 内既有决策。

### F3 — HIGH — CORE_CORRECTIVE：裁决输入“用户本回合原始请求、会话目标”在 originGuard 处取不到，设计未给来源；“会话目标”在代码中不存在

- originGuard 只拿到 `(ctx, call, tool)`（`origin_guard.go:76`）；ctx 中可取的只有 session id、turn id（`pkg/agentcore/api/session.go:20-37`，`internal/personal/observe.go:46-51`），以及 approval report、purpose 等，无回合 prompt。
- 全仓库 `internal/personal`、`pkg/agentcore` 无“会话目标/goal”概念（grep 仅 `internal/chat/followup.go`、`subagent.go`、codeintel 规则有 goal 字样）。
- 需在设计中定：由 `personal.Runner` 在回合开始把用户输入放进 ctx（新增 `api.WithTurnPrompt` 一类键，属 pkg/agentcore，需注意分层）或由 judge 按 session/turn id 查 store；非人工输入（`--injected`、verify 修复轮、后台任务通知唤醒的回合、子 agent 的任务描述由父模型撰写）时“原始请求”取什么；“会话目标”删除或给出定义。

### F4 — HIGH — CORE_CORRECTIVE：“命中的外部片段前后各扩展 512 字节”无法实现——ExternalOrigin 只存滚动哈希，不存原文

- `ExternalOrigin.Record` 只保存窗口哈希（`origin.go:63-105`，`originSource{id, tool, hashes}`），`find` 返回的 `Snippet` 是参数中的 48 字节（`origin.go:170-181`）。外部结果原文在 guard 处不可得，无法向裁决模型展示“外部内容中这段话前后说了什么”。
- 需在设计中二选一并写明代价：(a) ExternalOrigin 额外保存规范化原文（每结果上限 128 KB，`originMaxResultBytes`，会话内存上限需重算）并返回命中偏移；(b) 只扩展调用参数一侧的上下文（则“外部片段”名不副实，裁决依据变弱）。这决定 pkg/agentcore/tools 是否改动，影响计划拆分。

### F5 — HIGH — CORE_CORRECTIVE：judge provider 的回退链、用量记录与 `purpose` 都在 `personal.Runner` 内部，wiring 在构建审批链时还没有这些对象

- `scenarioProvider` 的回退（场景→compact→主模型）是 `Runner` 方法（`compact.go:489-503`）；被观测（记用量、价格、model 事件）的 provider 由 `personal.New` 内 `rec.observeProvider` 包装（`runner.go:461-480`）；`withPurpose/purposeKey` 未导出（`observe.go:56-66`）。
- 审批链在 `runtime.go:591` 构建并作为 `Tools` 传给 `personal.New`，即 originGuard 构建早于 Runner 与被观测 provider。
- 设计 L116-118 要求“回退顺序一致”“用量按 purpose=origin_judge 记入”，L145 却说“wiring 只装配：构建 models.judge provider 注入 originGuard”。若 wiring 自建未观测 provider，用量/trace/预算都不记；若要记，需延迟绑定（类似 `resident` 的函数变量）或 personal 导出 `ScenarioProvider(name)`/`WithPurpose`。另外预算（`--budget-*`、估算器）是否计入裁决调用未说明。须在设计中定唯一装配路径。

### F6 — HIGH — CORE_CORRECTIVE：A 第 5 项“`chat.ask_outside_dirs` → 放行”语义自相矛盾

- `askOutsideDir` 的“放行”实际是把目录加入会话 add-dir（`chat_approver.go:483-512`，`addDirs.add(..., Writable)`），之后工具才能访问；若只是跳过提问，工具会因路径越界自行拒绝（注释 L494-496），即“放行”无效果。
- 设计同时写“读写范围本身仍按 read roots / add-dir / `--allow-write`”。两者冲突：要么 trust 自动授权目录（须定 ro 还是 rw——rw 等于放开工作区外写入），要么 trust 下仍问/仍拒（则不是“放行”）。须明确，并说明 run/ACP/SDK 下该项不存在。

### F7 — MEDIUM — CORE_CORRECTIVE（AUTHORITY）：配置 `tools.approval: trust` 的作用入口未定；ACP 初始档位与上限关系未定

- 现状 `tools.approval` 只被 chat 读取（`runtime.go:262-268`；`toolsApprovalAuto` 仅 chat 与 manifest 调用），且同类的 `tools.allow_write` 明确只作用于 chat，理由是 run 常由共享该配置的 job runner 启动（`runtime.go:289-292`）。
- 设计 L90“配置 `tools.approval` 取值扩为 ask|auto|trust，命令行优先于配置”与 L91“run 缺省 auto、ACP 缺省 ask”冲突：若配置作用于 run/ACP，共享 SUGA_HOME 中为 chat 写的 `trust` 会让所有 gofer job 与 ACP 会话进入 trust，并绕过 D6 的“启动参数上限”。
- ACP：`suga acp --approve trust` 时会话初始值是 trust 还是 ask（仅上限为 trust）？gofer 不发 `set_config_option` 时能否得到 trust 取决于此。另需 `acpserver.OpenRuntime` 签名增加档位参数（`server.go:83` 现只有 `readOnly bool`），架构节未列。

### F8 — MEDIUM — CORE_CORRECTIVE：trust 与 `--allow-write`/`tools.allow_write` 的关系未定

- 写工具只有在 `AllowWrite` 时注册（`runtime.go:289-299`），否则调用被 `SetDisabled` 以 ErrDenied 拒绝并计入 DenialLimit（`approval.go:110-114`）。
- 用户可见结果“`suga run --yolo …` 不再因需要确认而拒绝”（L34）在不带 `--allow-write` 时不成立。须写明：trust 不蕴含 allow-write（文档提示 gofer 同时传），或 trust 蕴含 allow-write（则属于新增授权，需说明）。chat 同理（`tools.allow_write`）。

### F9 — MEDIUM — CORE_CORRECTIVE：SDK `ApprovalMode` 的逐项语义不完整

- SDK 由宿主 Approver 决定一切（`session.go:93-99`，`types.go:66-67`），外层只有 dangerGuard/originGuard（`runtime.go:591`，chat 为 nil 故走“无人可问”分支）。因此：`ask` 与 `auto` 在 SDK 下无差别；6 项中第 1、2、5 项在 SDK 下不存在；第 6 项“memory.write_tainted ask→放行”与宿主自己的 `MemoryConfirm`（`types.go:68-70`）冲突——trust 时是否跳过宿主确认，与 L92“宿主自己的策略不被绕过”矛盾。
- 需给出“入口 × 6 项”的适用矩阵，并说明 SDK 子 agent 用 ACP 编译策略（`runtime.go:378-385` 的 `compileACPApprovalPolicy` 快照经 `subagent.go:121-139`）时 trust 的继承方式。

### F10 — MEDIUM — CORE_CORRECTIVE（SECURITY）：禁读匹配的对象与路径规范化未定义（符号链接、大小写、绝对/相对）

- 解析是纯词法的：`ResolveSandboxedPath`/`resolveRooted` 不解符号链接（`file.go:208-230`，`readroots.go:115-129`）；工作区内 `cfg -> .env` 或 `k -> ~/.ssh/id_rsa`（若在 read root 内）按 `**/.env` 词法匹配会漏。
- Windows：`foldPathCase` 仅用于根判定（`pathresolve.go:14`），`globMatch` 用 `path.Match` 区分大小写（`walk.go:95-120`），`.ENV`、`C:\Users\X\.SSH` 会漏。
- 匹配对象：`~`、`$SUGA_HOME` 展开后是绝对模式，`**/.env` 是相对模式；walk 回调给出的是相对 base（工作区或 read root）的 rel（`grep.go:123`、`find.go:76`），须写明统一用“清理后的绝对路径（斜杠形式）”匹配，以及 xhen 全盘 read root（base 为 `/` 或 `C:\`）时的行为。
- 内置列表中的“除外”（`.env.example` 等）在 glob 语法中无法表达，须定义例外机制（内置白名单或 `!` 否定），并说明用户配置能否写例外。

### F11 — MEDIUM — CORE_CORRECTIVE：C 的工具覆盖清单与实际工具不一致，遗漏其余接受路径的读工具

- 工具名实为 `file_grep`、`file_find`（`catalog.go:128,131`），设计写作 `grep`、`find`。
- 调 `resolveRead` 的读工具还有：`file_symbols`（`file_symbols.go:112`）、`file_refs`（`refs.go:130`，输出匹配行文本）、`view_image`（`view_image.go:187`）、`file_read` 的图片分支（`file_read_image.go:78`）、`file_read` 批量项（`file_read.go:234`）；`file_edit` 结果会回显编辑处上下文行（`edit_report.go`）。用户自定义 `read_deny`（如 `secrets/**/*.go`）可经 `file_refs`/`file_symbols` 读出内容。
- 须逐个列出纳入/不纳入及理由；判定放 `readroots.go` 只覆盖起点路径，`file_list`/`file_grep`/`file_find` 需在各自 walk 回调逐项判定（设计“与 resolveRead 同处”易被误读为只在入口判一次）。

### F12 — LOW — CORE_CORRECTIVE：SDK `ReadDeny` 与 `$SUGA_HOME` 展开的装配路径未列全

- `nativeEnv` 只读 gkconfig，签名不含 Options（`capabilities.go:452-492`）；调用点有 `capabilities.go:102`、`runtime_build.go:161`、`runtime.go:508,549,846`、`subagent.go:236`、`manifest.go:598`。SDK 的 `ReadDeny` 要经 `wiring.Options` 到达每个构建读工具的点，含子 agent，否则 SDK 会话的子 agent 只有配置层的禁读。
- `$SUGA_HOME` 展开须在 internal/config（`ResolveSugaHome`）完成后以绝对模式传入 native，native 不得自行解析（分层）。设计只写了 `capabilities.go`。

### F13 — LOW — CORE_CORRECTIVE：用户可见结果与安全节对“读 .env”的表述矛盾

- L34“任何模式下……不会读 `.env`、`~/.ssh`”；L184“`trust` 档下可通过 shell 读取”。且 auto 档（run 缺省）下原生 `shell_exec` 默认 Allow（`approval_policy.go:62`），`cat .env` 同样可读。L34 应限定为“读类工具”。

### F14 — LOW — CORE_CORRECTIVE：B 的若干边界未写

- 子 agent：`subagent.go:176` 也构建 originGuard，设计未说是否注入 judge（子 agent 只有读工具与宿主 extraTools，originGuard 只对非只读工具生效，实际仅影响 extraTools）。
- ACP：ACP 能经 `RequestPermission` 询问（`policy.go:240-256`），但 `chatApproverOf(permissionHook)` 为 nil，`ask` 仍走“其他入口拒绝”；应明确这是有意保留。
- `tools.ask_on_external_origin: false` 时整个 guard 不装（`origin_guard.go:69-73`），与 `tools.origin_judge.enabled` 的关系应写明。
- 裁决期间回合被取消（ctx canceled）应中止回合而非按“出错”回退。

### F15 — LOW — CORE_CORRECTIVE：事实引用与架构文件清单的小误差

- `chat_approver.go:33` 是类型定义，`newChatApprover` 在 `:82`。
- “子 agent 强制 run”（L62）：`childPolicy` 保留父快照的 Mode（chat/ACP），只在为空时设 run（`subagent.go:121-139`）；“无人可问即拒”来自 `newPolicyApprover`，不是 Mode。trust 继承需落在快照上。
- 架构节漏列：`pkg/agentcore/tools/approval.go`（`approval=trust` 需新来源常量，L194-200）、`internal/acpserver/server.go`（OpenRuntime 签名）、`internal/wiring/memory_runtime.go`（`memoryTaintedConfirm`）、`internal/config/settings_registry.go:522`（枚举校验）、`chat_approver.go:110-122`/`manifest.go:594`（`toolsApprovalAuto` 只认两值）。

### F16 — LOW — DEFERRED_ENHANCEMENT：内置禁读列表可补常见凭据

- 未含 `~/.git-credentials`、`~/.config/gh/hosts.yml`、`~/.kube/config`、`~/.npmrc`/`~/.pypirc` 等。scope necessity：issue 只点名 `.env`、`~/.ssh`，列表扩充不影响本设计成立，可后续补。

## 5. 覆盖缺口与限制

- 未读 xhen 仓库，xhen 如何设置 read_roots（推断为 SUGA_HOME 配置）未核实。
- `internal/hooks/hooks.go` 仅 grep 确认“ask/allow 视为无异议”，未细读。
- issue `suga-kkh4` 提到的“只读委托门”在 suga 代码中未找到对应机制，设计也未提及，未判定归属（可能在 xhen/gofer 侧）。
- 未运行任何测试；判定均为静态阅读。

## 6. 结论

**BLOCKED**。开放 CORE_BLOCKING 2 项：F1（裁决缓存旁路）、F2（ClampAllow 的 Ask 在 trust 下的处理未定，字面实现违反 D8）。另有 HIGH CORE_CORRECTIVE 4 项（F3–F6），它们使 B 的输入/装配和 A 第 5 项无法写出唯一实施计划，建议与 blocker 同轮修订。

## 7. 剩余风险与后续

- 修 F1：缓存键改为“工具 + 规范化参数摘要”，或删除缓存。
- 修 F2：在 A 中写明 trust 下 clamp-Ask 的处理（建议 chat/ACP 仍询问、无人可问时拒绝），并要求 Decide 区分 Ask 来源。
- F3–F6：补数据来源（turn prompt 的 ctx 键或 store 查询）、ExternalOrigin 是否存原文、judge provider 的延迟绑定与 purpose 导出、ask_outside_dirs 在 trust 下的确定行为。
- F7–F11 在 0.3 一并澄清；F12–F15 可在计划中落实；F16 后续。
- 修订后建议第 2 轮仅复核 F1–F11 相关段落与新增内容。
