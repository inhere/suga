# suga 安全计划 0.3 评审：Spec/Executability 轴，第 3 轮（纠正确认轮）

## 候选四元组

- git_root：`/d/work/inhere/my-tools-dev/inhere-tools/suga`
- candidate_commit：`466e0dc831090cd27fb18a8f5ed0b232534230d1`（计划正文改动在 df11ca5d，466e0dc8 只加评审 ledger）
- subject：`docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md`
- revision：0.3（上轮候选 57643ac4 / 0.2）

评审者：spec 轴独立子 agent（REPORT_ONLY，只读；没有改仓库、没有提交；只写本报告）。

## 实际读取范围

- `git diff 57643ac4..466e0dc8 -- <subject>` 全部；计划 0.3 的跨任务接口、Scope freeze、波次表、T0/T1/T2/T5/T6/T8、人工 Gate；上轮 `spec-review.md`（466e0dc8 版，F15–F22）。
- 代码核对：
  - `internal/acpserver/server.go:144`（`Serve(..., opts ...ServeOption)`）、`:295-320`（permissionHook 构造）、`i18n.go:8-12`（`ServeOption`/`WithTranslator`）；grep `type Options` 于 acpserver：不存在；
  - `internal/wiring/cli_mcp.go:55-90`、`runtime.go:240-290`（run 用 `newPolicyApprover(gate.policy)`、chat 重设 Defaults）、`newCLIMCPGate(` 全部调用处；
  - `nativeEnv(`/`nativeEnvWithSnapshot(`/`native.Env{` 全部调用处（capabilities.go:102/452/456、runtime_build.go:161、runtime.go:508/549/846、subagent.go:236、manifest.go:598、native_tools.go:46）；
  - `pkg/agentcore/tools/native/readroots.go:85-135` 与 `resolveRead(` 全部调用处；
  - `internal/personal/context.go:30-50`（`isInjectedItem`/`injectedItemKeys`）、`segment_handoff.go:44-56, 145-170, 370-400`（`metaInjected`、`injectedStorage`）、`store/entries.go:213`（`ListEntries`）、`segment.go:357/431`；
  - `internal/personal/runner.go:43`（Config）、`:655-690`、`turncontext.go:40-75`（EventTurnContext attrs）、`runner_finish.go:199`、`observe.go:303`；`internal/wiring/runtime.go:720`（personal.Config.Mode）。
- 没读：standards-review.md、review-ledger.json、设计全文（只依据计划与上轮报告引用）。

## 结论

**FAIL**：1 项 CORE_BLOCKING（F23，0.3 修订引入）。F15–F20、F22 关闭；F21 一半关闭（turn_prompt 部分改错见 F23，approval_mode 部分见 F24）。另有 3 项 LOW CORE_CORRECTIVE。

## 上轮处置核对表

| 项 | 判定 | 依据 |
|---|---|---|
| F15 | 关闭 | `PolicyDecision` 分出 `clamped`（trust 判定，与 chat/run 的 protected 同义）与 `pathProtected`（只供 always 会话规则）。ACP trust 下默认编为 Allow，ClampAllow 会触发，`.git` 默认动作得到 clamped=true 仍询问；两入口语义一致。T0 先恒 false、T2 填实无害 |
| F16 | 关闭（附 LOW，见 F25） | run：`newCLIMCPGate` 增档位、cli_mcp.go 入 T2；run 实际审批器为 `newPolicyApprover(gate.policy)`（runtime.go:257），trust 默认 Allow 可达。ACP：server.go 入 T2 owner，:310 构造处带入可行；但写的 `acpserver.Options` 不存在 |
| F17 | 关闭（附 LOW，见 F26） | 调用点清单与 grep 一致（含 capabilities.go:102、runtime_build.go:161）；补了经 `wiring.Open` 的主会话用例 |
| F18 | 关闭 | cli/chat.go、chat/chat.go、render.go 已入 scope 与 T2 owner |
| F19 | 关闭 | 接口改为断言 `api.StreamingProvider`，否则 Generate |
| F20 | 关闭 | refs_context.go 入 T6；“只对文件、目录照常解析、遍历逐项判定”可实施（resolveRead 本身不 stat，需在判定处 stat，属实现细节）；补了 `file_list ~/.ssh` 用例 |
| F21 | 部分 | turn_prompt：指定的过滤器错误，见 F23；approval_mode：写入事件与取值通道仍不唯一且出 owner，见 F24；`approval=trust` 走 ApprovalReport→observe.go:303 可行 |
| F22 | 关闭 | Gate 差异说明与剩余工作已写明 `tools.ask_on_external_origin` 仍可写 |

## 发现

### F23 [HIGH][CORE_BLOCKING][SECURITY] turn_prompt 只用 `isInjectedItem` 过滤，注入回合的正文会被当成“用户请求”

- 事实：
  - 0.3 把 T1 动作 3 改为“用 `store.ListEntries` 读会话条目、以 `isInjectedItem` 排除注入项，取最近一条用户消息”（0.2 原为“最近一条非注入用户消息”）。
  - `isInjectedItem` 只看 `injectedItemKeys = {metaTaskNotice, metaSteer, metaTurnInterrupted, metaUndo}`（context.go:39-49）。
  - `RunRequest.Injected` 回合（Stop hook 阻断理由、socket 投递、`suga run --injected`）的用户消息由 `injectedStorage` 打的是 `metaInjected = "suga.injected"`（segment_handoff.go:48-52、:384-397），`isInjectedItem` 不识别。
  - 现成的正确写法在 segment_handoff.go:154-162：`isContextUpdate || isSegmentContext || isInjectedItem` 加 `metaInjected` 单独判断。上轮 F21 建议点名的正是这四项，0.3 只采纳了一项。
- necessity proof：
  - core_acceptance：设计 B——用户请求是裁决的可信部分，注入回合正文进不可信块；计划目标 2 与 T5 动作 1 依赖 `TurnPromptFrom` 的值。
  - necessary_causal_chain：会话序列“用户回合 A → 注入回合 1（metaInjected）→ 注入回合 2”。按计划字面实现，注入回合 2 的 `TurnPromptFrom` 返回注入回合 1 的正文；若其中含外部注入内容，Judge 在“可信用户请求”里看到攻击者文本，与工具调用一致即 allow，B 在其目标场景失效。context update / segment context 消息（role=user）也可能被取作请求。T1 现有测试（用户回合、单个注入回合、跨进程）在当前回合消息尚未持久化时都能通过，发现不了。
  - minimal_owner：T1 动作 3 一句话改为“与 `segment_handoff.go:154` 相同的判定：排除 `isContextUpdate`、`isSegmentContext`、`isInjectedItem` 与 `Metadata[metaInjected]`”；T1 测试加“连续两个注入回合时取到的是最近的人工请求”与“只有注入回合时为 \"\"”。
  - deletion_alternative：不改则实施者按字面实现即得错误结果；删去具体函数名回到 0.2 的泛述又会重开 F21。
  - scope_effect：只改措辞与 2 条用例，不增机制，文件仍在 T1 owner（turn_prompt.go）。

### F24 [LOW][CORE_CORRECTIVE] `approval_mode` 的“回合开始事件”没有唯一落点，取值通道与文件不在 owner

- personal 没有名为“回合开始”的持久事件：`run_started` 只走 Emit 不入库（runner.go:665）；入库的是 `EventTurnContext`（turncontext.go:57，已有 `mode` attr，取自 `r.cfg.Mode`）与回合结束的 `EventTurn`（runner_finish.go:199）。
- “值来自 `Options.ApprovalMode`”是 wiring 的 Options，personal 拿不到；可行通道是 `personal.Config` 增字段（runner.go:43）并在 runtime.go:720 处赋值。turncontext.go 与 runner.go 的 Config 都不在 T2 owner/scope（personal 只列 runner.go 调用点、turn_prompt.go、observe.go）。
- 建议：写明“`EventTurnContext` attrs 增 `approval_mode`，经 `personal.Config.ApprovalMode`（runtime.go:720 赋值）”，并把 turncontext.go、runner.go（Config 字段）加入 T2 owner 与 scope。ptye2e 的 `inspect trace --json` 断言可据此成立。

### F25 [LOW][CORE_CORRECTIVE] `acpserver.Options` 不存在

- acpserver 只有 `ServeOption func(*Agent)`（i18n.go:8）与 `Serve(..., opts ...ServeOption)`（server.go:144）。T2 owner、动作 8 写“`Options` 增档位 / 经 `acpserver.Options`”。建议改为“新增 `ServeOption`（如 `WithApprovalMode`）写入 `Agent` 字段，server.go:310 构造 permissionHook 时带入”，避免实施者另造 Options 类型。

### F26 [LOW][CORE_CORRECTIVE] T5 与 T6 的文件相交未在波次表写明顺序

- 0.3 把 `runtime_build.go` 加入 T6 owner，而 T5 也改 `runtime_build.go`（judge provider）；两者还同改 `settings_registry.go`。T5 只依赖 T2，T6 在 T2 后 rebase 合入，二者可能并行。波次表未写 T5/T6 先后。建议写“T6 先合入，T5 rebase 后合入”或反之。

## 其他已核实

- 修订未越权：G1 时发 gofer 说明属已声明的网络动作，仍在 G1 授权之后；Gate 差异说明新增“新键不可写”与设计差异披露一致。
- `clamped`/`pathProtected` 名称在接口、T0、T2 中一致。
