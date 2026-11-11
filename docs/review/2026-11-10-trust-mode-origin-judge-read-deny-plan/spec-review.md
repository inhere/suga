# suga 安全计划 0.2 评审：Spec/Executability 轴，第 2 轮（confirmation）

## 候选四元组

- git_root：`/d/work/inhere/my-tools-dev/inhere-tools/suga`
- candidate_commit：`57643ac42fd44468cce0acf0cd5e410edc11df8d`（HEAD；计划正文改动在 f6f79d1e，57643ac4 只加评审 ledger）
- subject：`docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md`
- revision：0.2（上轮候选 962b3974 / 0.1）

评审者：spec 轴独立子 agent（REPORT_ONLY，只读；没有改仓库、没有提交）。基准：已批准设计 0.5。

## 实际读取范围

- 计划 0.2 全文（`git show 57643ac4:<subject>`）、上轮 `spec-review.md`；设计 0.5 的 A 表/硬限制（:70-152）与 C 节（:160-191）。
- 代码核对：
  - `internal/wiring/approval_policy.go:40-215`（`defaultApprovalDefaults`、`compileApprovalPolicy`、`compileACPApprovalPolicy`、`policyApprover`、`approvalPolicy.Decide`）；
  - `pkg/agentcore/tools/rules/rules.go:148-170, 255-305`；全仓 grep `ClampAllow(`、`approvalrules.Policy{`、`.Decide(`；
  - `internal/wiring/danger_guard.go` 全文；`chat_approver.go:128-200, 268-290`；`cli_mcp.go:40-100`；`runtime.go:225-330, 365-395, 800-860`（含 `ACPPolicy` 字段 :157）；`memory_runtime.go:125-150`；
  - `internal/acpserver/policy.go:15-60, 100-330`、`server.go`（`TurnGate`、`Serve`/`ServeOption`、`:310` permissionHook 构造）、`memory_confirm.go`；acpserver 测试中的 `PolicyDecision` 字面量（grep）；
  - `internal/cli/acp.go:1-110`、`run.go:240-295`、`config.go:140-180`（Writable 检查）；
  - `internal/chat/layout.go:76-96`、`render.go:730-745`、grep `WriteTools:`（`cli/chat.go:93`、`chat/chat.go:415`）；
  - `internal/personal/segment_handoff.go:140-215`、`rewind_target.go:110-140`、grep `Injected`；`compact.go:455-535`；`observe.go:320-355`；行数 runner.go 933、runtime.go 917；
  - `pkg/agentcore/api/provider.go:1-60`、`llm/claude.go:190-200`、`llm/openai.go:128-136`；
  - `internal/wiring/capabilities.go:440-495` 与 grep `nativeEnv(`/`nativeEnvWithSnapshot(`/`native.Env{`/`BuildTools(`；
  - `pkg/agentcore/tools/native`：文件存在性、grep `resolveRead(` 全部调用处、`file_guard.go:115-160`、其他直接读文件处；
  - `internal/config/settings_registry.go`（grep Writable、`tools.ask_on_external_origin`）；`scripts/acpprobe/acp_probe.py`（参数与 :272）。
- 没读：standards-review.md 正文、review-ledger.json、ptye2e 源码、设计评审目录。

## 结论

**PASS**：无 CORE_BLOCKING。F1、F2 已关闭；上轮 F3–F13 大体落地，余下 4 项 CORE_CORRECTIVE（其中 3 项为上轮同类未穷尽）、3 项 LOW 修正、1 项 DEFERRED。

## 上轮处置核对表

| 项 | 判定 | 依据 |
|---|---|---|
| F1 | 关闭 | `approvalPolicy.Decide`：Policy.Decide → read-only MCP 判定（:190-194，返回 Deny 而非 Ask）→ 无命中时 ClampAllow（:201）。trust 把原生/MCP 默认编为 Allow 后，保护路径默认动作被降为 Ask/Deny 且带标记，read-only MCP 仍 Deny，不会落入“(Ask, protected=false)”放行分支。ACP `compileACPApprovalPolicy` 加档位参数即可。遗留语义问题见 F15 |
| F2 | 关闭 | `runtime.go:591` 链为 `guardExternalOrigin(guardDangerous(opts.Approver))`，inner 即 chatApprover / policyApprover / permissionHook / SDK 宿主 Approver；trust 下交 `g.inner.Decide` 可保留显式 deny 与宿主决定 |
| F3 | 关闭 | T0 移到 T1 后；扩 `PolicyDecision`、闭包内 `IsProtectedPath`；chat 只做特征用例 |
| F4 | 关闭 | 5 处调用点 grep 核实齐全（approval_policy.go:160、chat_approver.go:142/277、cli_mcp.go:82、runtime.go:830）；`ClampAllow`/`Policy{}` 生产调用只有 rules.go:163、approval_policy.go:189/201 |
| F5 | 关闭（附 LOW，见 F21） | transcript 来源可行：`repo.ListEntries(..EntryUser)` 加 `isInjectedItem`/`metaInjected` 过滤已有现成写法（segment_handoff.go:154-162） |
| F6 | 部分（见 F16，上轮未穷尽） | 记忆按入口包装可行（chat runtime.go:283、run 为 nil、acp.go:67）；但 ACP 档位进 permissionHook 的落点与 run 的 MCP 默认编译缺 owner |
| F7 | 部分（见 F18，上轮未穷尽） | BannerInfo 与 i18n 已入范围，但填值链路的文件未入 |
| F8 | 关闭 | capp 写法核实；`RunPersonalACP` 只有 acp.go:23 与 acp_test.go:50 调用 |
| F9 | 关闭（附 LOW，见 F19） | 写明流式，但接口写法不可编译 |
| F10 | 部分（见 F17，上轮未穷尽） | 漏了 `nativeEnvWithSnapshot` 的两个主装配调用点 |
| F11 | 基本关闭（附 LOW，见 F20） | file_symbols 目录、refs 上下文已列；`refs_context.go` 未入范围 |
| F12 | 关闭 | acp_probe 增额外参数、脚本入范围；（真实模型写 `.git` 的不稳定性仍在，可接受） |
| F13 | 部分（见 F21） | 用例已补；`approval_mode` attrs 的写入事件与来源仍未定 |
| F14 | 已处理（附 DEFERRED，见 F22） | `internal/cli/config.go:162` 对 `Writable:false` 拒绝 set/unset，机制存在 |

## 发现

### F15 [MEDIUM][CORE_CORRECTIVE] 同名 `protected` 两种语义，ACP 与 chat/run 的 trust 放行范围不一致

- 事实：T1 定义 `protected` 为“因保护路径被降级时为 true”（ClampAllow 只降级 Allow，rules.go:261）；T0 的 `PolicyDecision.protected` 定义为 `IsProtectedPath`（“与是否降级无关”）。T2 动作 3 的 trust 放行在 ACP 只能用后者，在 chat/run 用前者。
- 结果：用户规则 `file_write .git/** ask` 在 trust 下，chat/run 得到 `(Ask, protected=false)` 被放行写入 `.git`；ACP 得到 protected=true 仍询问。前者与设计 A 表 #1、术语定义（:76）一致，所以不是计划越界，但两入口行为不同、实施者面对同名字段会混用。
- 建议（不扩范围）：跨任务接口给 ACP 字段改名（如 `protectedPath`）并写明“ACP trust 放行用降级标记还是路径判定”二选一；若选路径判定，在 Gate 差异说明里注明 ACP 比设计更严。另把“显式 ask 规则指向保护路径在 trust 下放行”作为设计层事项提示用户（是否改为按 `IsProtectedPath` 判定由设计决定，不在本轮扩大）。

### F16 [MEDIUM][CORE_CORRECTIVE] ACP 档位到达 permissionHook 与 run 的 MCP 默认编译缺落点（上轮未穷尽，F6 同类）

- permissionHook 在 acpserver 内部构造（server.go:310），acp.go 的闭包拿到的已是构造好的 gate；T2 动作 8 写“传入 permissionHook 构造”，但 T2 owner 没有 `internal/acpserver/server.go`。可行做法：新增 `ServeOption`（Serve 已有 `opts ...ServeOption`）并把 server.go 列入 T2 owner；或明确 trust 的 ask→allow 与记忆放行都在 acp.go/wiring 侧完成（记忆包装在 acp.go 即可，不必改 memory_confirm.go）。二选一写死。
- run：`newCLIMCPGate(readOnly)` → `compileCLIApprovalPolicy(..., ModeRun, false, readOnly)` 没有档位入口，run trust 的 MCP 默认仍是 Deny（非 Ask，trust 放行分支救不回来）。T2 owner 不含 `cli_mcp.go`。写明：在 OpenCLI 中像 chat（runtime.go:266）一样重设 `gate.policy.Defaults`，或把 cli_mcp.go 加入 T2 owner。

### F17 [MEDIUM][CORE_CORRECTIVE] nativeEnv 调用点漏了主会话装配（上轮未穷尽，F10 同类）

- 主会话工具表由 `capabilities.go:102` 的 `nativeEnvWithSnapshot(workspace, dirs, snapshotter)` 构建，另有 `runtime_build.go:161` 同样调用；计划只列 `nativeEnv(workspace, dirs, readDeny)` 及其调用点（“452/456”中 456 是 nativeEnv 内部对 nativeEnvWithSnapshot 的调用）。只给 nativeEnv 加参数时主会话没有禁读，C 失效；native 层单测发现不了。`runtime_build.go` 也不在 T6 owner（属 T5）。
- 建议：接口改为 `nativeEnvWithSnapshot` 一并加参数，列出 capabilities.go:102、runtime_build.go:161；T6 owner 加 runtime_build.go 并写明与 T5 的先后；T6 测试加一条经 `wiring.Open` 的主会话 `file_read .env` 被拒（反向验证覆盖该装配）。

### F18 [LOW][CORE_CORRECTIVE] chat banner 的填值链路不在范围（上轮未穷尽，F7 同类）

- BannerInfo 只在 `internal/chat/render.go:740` 填，数据经 `internal/cli/chat.go:93` → `internal/chat/chat.go:415`（Options → RenderOptions）。scope/owner 只有 layout.go。建议把三处补入 T2 owner。

### F19 [LOW][CORE_CORRECTIVE] `Judge` 的“用 p.Stream”按接口写法不可编译

- `api.Provider` 只有 `Name/Generate`，`Stream` 在 `api.StreamingProvider`（provider.go:35-38）。写明“断言 StreamingProvider 则 Stream 丢弃增量，否则 Generate”；另外 llm 各 provider 在 `stream_required` 时 Generate 已转 Stream（claude.go:190-192、openai.go:128-130），可作补充依据。只支持流式的假 provider 用例保留。

### F20 [LOW][CORE_CORRECTIVE] 禁读的落点细节

- `refs_context.go:53` 另读文件，计划在 T6 说“上下文读取同样判定”，但 scope freeze 的 native 文件列表没有 refs_context.go。
- 判定放进 `resolveRead` 时，`file_list.go:77` 对目录参数也走 resolveRead；`~/.ssh/**` 若按目录本身匹配，`file_list ~/.ssh` 会报错，与设计“遍历照常列出并标注”冲突。写明判定只对文件目标（或 file_list 的目录参数不做禁读）并加一例。

### F21 [LOW][CORE_CORRECTIVE] 两处来源仍未写明（上轮未穷尽，F13/F5 子点）

- `approval_mode` attrs 写在哪类事件、值从哪来（ApprovalReport 增字段由审批器写入，还是进 `personal.Config` 由 observe 写入；后者要动 runner.go，不在 T2 owner）。二选一。
- turn_prompt 建议点名复用 `repo.ListEntries(EntryUser)` 加 `isInjectedItem`/`metaInjected`/`isContextUpdate`/`isSegmentContext` 过滤（segment_handoff.go:154），避免另写一套判定。

### F22 [LOW][DEFERRED_ENHANCEMENT] “防止模型关闭防护”的理由只做到一半

- `tools.ask_on_external_origin` 仍 `Writable: true`（settings_registry.go:222），它关掉的是整个 B；`models.judge` 也可写。scope necessity：设计没要求，本轮不扩大。建议把修订表 F14 的理由改为“新增键不可写”，总开关可写列入剩余工作。

## 其他已核实无误

- acpserver 中 `PolicyDecision` 字面量 6 处（mcp_annotations_test:134、approved_note_test:86、policy_table_test:191、server_test:99/728/760）与 `Runtime.ACPPolicy` 字段（runtime.go:157）由 T0 的“各自测试/runtime.go”覆盖。
- `suga acp` 用 capp，`RemainArgs` 校验（acp.go:20）可改为声明 flag。
- `acp_probe.py` 当前确无额外启动参数（:272 写死）。
- Env 定义在 `catalog.go`（计划写“或 Env 定义所在文件”，可接受）；`file_guard.go:136` 经 resolveRead 只取 stamp，加禁读不削弱改动保护。
