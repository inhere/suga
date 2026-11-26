<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）设计 评审（Spec/Executability 轴 · 第 2 轮 blocking closure）

## 评审目标与范围

- 目标: 确认第 1 轮 `CORE_BLOCKING` BF1 已经闭合，BF2–BF9 的修订正确、可实现，本次修订没有引入新的 `CORE_BLOCKING` 或矛盾
- 修订版本: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga` · candidate `58dbf9df` · subject `docs/design/2026-11-26-background-fork-design.md` · Draft 0.3（上轮 `dd7b5fd8` / Draft 0.2）
- 评审者: Spec/Executability 轴独立评审者（Claude 子 agent），没有参与该设计的撰写或修订
- 日期: 2026-11-26
- 排除项: D1–D5 本身；Standards/Governance 轴（暴露度、代码组织、GR 结构）；未改动的章节沿用上轮结论；相邻架构

## 输入与方法

- 规范: `tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md` 中的 review-contract 与 review 模板。
- 上轮报告: `docs/review/2026-11-26-background-fork/spec-review.md`（BF1–BF10）。
- 设计改动: `git diff dd7b5fd8..58dbf9df -- docs/design/...`；同时读了候选全文（208 行）。
- 代码（工作树 HEAD = `58dbf9df`）:
  - `internal/personal/observe.go:300-325`、`denial.go:99-125`、`pkg/agentcore/tools/approval.go:136-176`：拒绝计数与普通错误路径；
  - `pkg/agentcore/runtime.go:134-260,370-430,486-640`、`runtime_wrapup.go`：步数上限、收尾、收尾重试、AfterModel/AfterTool、逐调用执行；
  - `internal/personal/subagent.go:1-140`：RunChild 与 Stop；
  - `internal/personal/runner.go:780-836,910`：turnHooks 顺序与 maxStepsWrapUp；
  - `internal/config/agents.go:28,59`：`agents.max_steps` 默认 30、最小 1；
  - `internal/personal/context.go:250-480`：contextBuilder 的组装、折叠、段断点与 shrink；
  - `internal/personal/segment.go:339-354`、`runner.go:60-80`、`internal/wiring/subagent.go:140-275`：子 Runner 的段上下文消息与装配；
  - `internal/personal/segment_handoff_reuse.go:82-330`、`compact.go:825-845`、`prefix.go:38-70,329-391`：重建与 `cache_prefix_print`；
  - `internal/wiring/memory_runtime.go:70-92`、`internal/personal/runner_sessions.go:43-60`：晚绑定先例与 SwitchModel。
- 方法: 逐条把修订后的规则落到代码路径上，判断能否照做、结果是否唯一。只记录影响核心验收或安全的问题，同类问题一次列全。

## 验收标准

- 设计中“用户可见结果”的四条，以及“验证要求”。
- review-contract：`CORE_BLOCKING` 只限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 五类，并附 necessity proof；其余发现记 `CORE_CORRECTIVE`（改变用户可见结果的标 `user_visible_effect: true`）或 `DEFERRED_ENHANCEMENT`。

## 上轮发现闭合核对

| 上轮 | 结论 | 依据 |
|---|---|---|
| BF1（阻断） | **已闭合** | 只有 `errors.Is(err, tools.ErrDenied)` 才计入 `turnDenials`（`observe.go:316-319`）。其他错误由 `abortingToolPolicy` 交给 `DefaultToolExecutionPolicy`，变成 `IsError` 工具结果（`denial.go:116-125`、`runtime.go:584-592`），不会触发 `denialLimitHook`。F9 要求的普通错误可以在 wiring 中包在 Approval 外层实现，提示表在每次运行时按种子 Tools 生成（子 Runner 每次都会新建，`subagent.go:140-220`）。`tool_call` 代理在子 registry 中找不到，同样落到普通错误。收尾路径：`agents.max_steps` 最小为 1，默认 30（`config/agents.go:28,59`），`maxStepsWrapUp` 对任何正数上限都会给出收尾指令（`runner.go:910-915`）。到达上限时运行时先做一步收尾（`runtime.go:141-147`）。模型仍然调工具时，`finishIgnoredWrapUp` 依次用伴随文本、重试或固定通知加最后正文作答（`runtime_wrapup.go:89-131`）。RunChild 取 `res.WrapUp` 得到 `stop=max_steps` 并带报告（`subagent.go:99-105`）。主会话最终收到的是带结论或最后正文的报告，不是“等待授权”。同类遗留见 BF14（LOW）。 |
| BF2 | 已处理 | AfterModel 的 `event.Result` 包含当步完整的 `GenerateResult`（`runtime.go:426-430`）。工具在同一批次里逐个顺序执行，每个调用执行完都会跑 AfterTool（`runtime.go:517-528,601`）。执行分身调用时，排在它前面的调用已经有 AfterTool 结果，所以“前面填真实结果、后面填占位”可以照做。 |
| BF3 | 已处理 | 折叠在 `compose`、段断点与 shrink 在 `BuildMessages`（`context.go:359-415,465-476`），都可以按分身模式分支关闭。`estimate` 用上次实测输入加视图增量，与 F6 的口径一致。`RequestWrapUp` 是 hook 的 `Control` 方法，计划里要落到一个 BeforeModel/AfterModel hook（已有 `subagent.go:219` 的 limitHook 先例），不影响可实现性。 |
| BF4 | 基本已处理 | hook 放在首位、复制而不是 append、wrap-up 与 deadline 提示的位置都已写明，与实际顺序（usage → budget → denialLimit → prefixHook → deadlineHook → cfg.Hooks → …，`runner.go:807-835`）一致。“首条”的事实有误，见 BF12；收尾重试的 ToolChoice 见 BF13。 |
| BF5 | 部分处理 | 表格已经给出。但 `prefix_changed` 的规则与“`:compact`/`:rewind`/段清理后重建”自相矛盾，见 BF11。 |
| BF6 | 已处理 | `memoryHost.bind` 是现成的先例（`memory_runtime.go:70-92`）。子 Runner 每次按传入的 model 调用 `loadRuntimeSettings`（`subagent.go:148-152`），把种子模型传进去，窗口、输出上限、价格与 provider 都会随之一致。 |
| BF7 | 已处理 | 保持 `Kind:"agent"`，摘要用 `agent: ` 前缀，现有 5 处消费方都会自动覆盖分身。 |
| BF8 | 已处理 | 满额时的两种提示、`:bg` 的分发与放回输入行、带会话的 ctx、空参数提示、D4 的计法都已写明。 |
| BF9 | 已处理 | 前缀断言改成用 `fakeLLM.Requests()`，`fork_reuse` 只记首个 model 事件，每项都补了用例。真实模型实测改为并列记录、不设门槛，这条验收现在可以判定。 |

## 发现

### BF11 [MEDIUM] F7：`prefix_changed` 判为“不启动”，与“`:compact`/`:rewind`/段清理后按重建处理”矛盾

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据:
  - F7 说 `:compact`、段断点、`:rewind`、段清理之后“一律按 `requestBaseBetweenTurns` 重建（`rebuilt`）”，又说“`prefix_changed` 时重建不可信，分身不启动”。
  - `requestBaseBetweenTurns` 拿重建结果与会话 meta `cache_prefix_print` 比对（`segment_handoff_reuse.go:206-226`、`rebuildDiff` `:273-295`）。这个 meta 只在回合结束时写入（`prefix.go:70,376-391`，全仓库只有这一处写入），`:compact` 不会更新它（`compact.go:825-845`）。
  - `:compact`、`:rewind`、段清理之后，持久化视图换成了“段上下文消息（交接包）+ 保留部分”，与上一回合的指纹在 `msg:<n>` 或 `length` 上必然不同，所以总会返回 `prefix_changed`。
- 同类全部出现点: `:compact` 后、`:rewind` 后、段清理后，这三种 `:bg` 都会落到 `prefix_changed`。回合内的段断点之后同一回合还会再发请求，tracker 会重新取到值，所以不受影响。`--resume`、取消或失败回合之后，视图仍以指纹为前缀，也不受影响。
- 影响: 照文档实施有两种结果。一种是三种情形下 `:bg` 一律拒绝启动，提示 `prefix_changed`，与表格写的 `rebuilt` 不符。另一种是实现者跳过指纹比对，按重建结果启动。验证中“`:compact` 后的 `fork_reuse` 或拒绝原因”也因此判不出该断言哪个。
- 不定为阻断的理由: 只影响这三条命令之后、下一个回合之前的窄窗口，用户再发一轮就能恢复，不阻断“派出分身并收到结论”这一核心验收；而且严重度不到 BLOCKER/HIGH。
- 建议: 写定一种。推荐：对分身来说，`prefix_changed` 只表示缓存不会命中，不表示内容不可信（重建内容就是持久化视图），所以照常以重建结果启动，记 `fork_reuse=rebuilt`，原因附在 attr 上。真正不可信、要拒绝的只有 `host_hook` 与 `prompt_state`。如果坚持拒绝，就把这三种情形从“重建”一行移到“不启动”一行，并写清提示文本。验证中这三种情形的期望值要相应写死。

### BF12 [MEDIUM] F2：分身子会话视图的首条不是 RunChild 的 Prompt，而是子 Runner 的段上下文消息

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: false`
- 证据: `compose` 先输出 `segmentContextMessage`（`context.go:432-434`）。子 Runner 配置了 `EnvironmentSource`（子 agent 环境）与 `RuleSource`（`wiring/subagent.go:202-209`），这两项都会进入段上下文（`runner.go:65-75`、`segment.go:339-354`），所以子会话视图依次是“段上下文消息 → Prompt → …”。另外 `hookContext` 与 `ephemeral` 也可能出现在 Prompt 之前（`context.go:438-446`）。
- 影响: “去掉首条”按字面实现会删掉段上下文消息，Prompt 留下来，与尾部里的任务指令重复。按身份删 Prompt 的话，基准请求的尾部之后会多出一条子 agent 环境与规则消息，与主会话的段上下文重复。两种做法都不破坏前缀，缓存仍然命中，但请求内容不唯一，前缀单测的期望也写不死。
- 建议: 写明按身份去掉 RunChild 的 Prompt，并规定子会话的段上下文消息、hookContext、ephemeral 的去留。推荐：分身模式下这些都不渲染，因为基准里已经有主会话的段上下文；只读约束改由尾部的任务指令或 F9 的提示说明。

### BF13 [LOW] F2“不发 `ToolChoice`”与收尾重试的 `ToolChoiceNone` 冲突，没有写明怎么处理

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: false`
- 证据: 模型在收尾步仍然调工具且没有正文时，运行时会带 `ToolChoice = ToolChoiceNone` 再发一次（`runtime_wrapup.go:107-127`），这次请求同样经过 BeforeModel hook（`runtime.go:382`）。F2 的 hook 写的是“不发 `ToolChoice`”。
- 影响: hook 清掉 ToolChoice 时，重试大概率仍然调工具，最终落到固定通知加最后正文（`wrapUpFallbackNotice`）。保留 ToolChoice 时，按 H5 网关会丢掉工具定义，这次缓存不命中，但拿到正文的概率更高。两种做法都有报告，只是 F9 所依赖的 max_steps 收尾的报告质量不同。
- 建议: 写明 F2 的“不发 ToolChoice”只适用于常规步，收尾重试保留运行时设定的值（只有这一次请求不命中缓存，可以接受）。

### BF14 [LOW] 白名单工具在父策略快照下“询问失败关闭”仍返回 `ErrDenied`，分身仍可能被拒绝上限短路（BF1 的同类遗留）

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据: 子 Runner 的审批是 `guardExternalOrigin(newPolicyApprover(policy) …)`，ask 类规则在没有终端时失败关闭（`wiring/subagent.go:168-180`），返回的是 `Denied`，会计入上限（`approval.go:159-163`、`observe.go:316-319`）。F9 只覆盖“不在白名单的名字”。
- 影响: 用户如果给读类工具配置了 ask 规则（例如某些路径），分身按主会话上下文去读这些路径 3 次，仍然会得到“等待授权”的报告。普通 explore 子 agent 也有这个问题，但分身继承了主会话上下文，触发的概率更高。
- 建议: 一句话关闭：分身 Runner 不挂 `denialLimitHook`（反复尝试交给 max_steps 收尾），或者把 F9 的规则扩展到“分身中的一切拒绝都返回普通错误”。“连续 3 次写调用”用例可以顺带覆盖一个 ask 规则下的读调用。

## 覆盖缺口与限制

- 没有运行任何代码或测试，结论来自静态阅读。
- 未改动的章节（安全、数据、回滚、D1–D5 本身）沿用上轮结论，本轮没有重新评审。
- F10 的细节没有展开：AfterTool 记录的结果要不要包含排在后面的 hook（loopGuard）追加的说明，`tool_call` 代理用展开前还是展开后的调用。两者都只影响尾部内容的保真度，不影响前缀与核心验收，留给计划阶段决定。
- model 触发时 `ForkSeed` 读取当前模型，不应获取会话 gate（主回合正持有它，`runner_sessions.go:50`）。这属于实现注意事项，没有单列为发现。

## 结论

PASS。上轮唯一的 `CORE_BLOCKING` BF1 已经闭合：分身的拒绝改为普通错误，不再计入每回合拒绝上限，反复尝试由 `agents.max_steps`（最小 1，默认 30）收尾兜住，收尾时仍会给出带原因的报告，对应的反向验证也已写入。BF2–BF9 的修订可以对照代码照做。本轮没有新的 `CORE_BLOCKING`。新发现 BF11–BF14 都是 `CORE_CORRECTIVE`，其中 BF11 与 BF14 带 `user_visible_effect: true`，建议在进入计划前一并写定。

## 剩余风险与后续

- BF11 与 BF14 会改变用户可见结果。按合同，可以在一轮纠正确认中只看这两处，也可以由作者修订后直接带入计划，由用户决定。
- BF12 与 BF13 写进计划的任务书，前缀单测与收尾用例的期望值据此写死。
- 风险没有变化：分身首个请求未命中缓存（重建或缓存过期）时按全价计费，由 `fork_reuse` 与真实模型记录观察。
