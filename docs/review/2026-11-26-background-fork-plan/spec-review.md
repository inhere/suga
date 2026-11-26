<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）实施计划 Spec/Executability 评审（第 2 轮，闭合确认）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `806af9b2`（Draft 0.2）；上轮 `00c890f7`（Draft 0.1）；subject `docs/plans/2026-11-26-background-fork-plan.md`
- 修订版本: Draft 0.2
- 评审者: 独立评审子 agent（Spec/Executability 轴），未参与计划编写
- 日期: 2026-11-26
- 排除项: Standards/Governance 轴；设计关键流程 3（kill 不通知，用户 Q3 选 A）；REPORT_ONLY，不改仓库、不提交

## 输入与方法

- 读取：review/plan 规范 payload、上轮本轴报告（PF1–PF9）、`git diff 00c890f7..806af9b2 -- docs/plans`、计划 0.2 全文。
- 对照代码（`00c890f7..806af9b2` 只改 docs，工作区代码即基线）：`pkg/agentcore/runtime.go:221,375-431,497-657`、`tool_validation.go`、`api/provider.go:149-152`、`api/hooks.go:126-133`、`llm/observe.go`、`tools/native/agent.go:25-58,118-140,177-221`、`tasks/tasks.go:245,261-284,778`、`internal/personal/{runner.go:572-760,781-835,prefix.go:340-372,observe.go:120-200,subagent.go:20-135,segment_handoff_reuse.go:85-125,compact.go:400-415,825-845}`、`internal/wiring/runtime.go:159-200,318-326,545-560,725-811`、`internal/cli/{acp.go:68-90,chat.go:138,run.go:175-217}`、`internal/acpserver/server.go:74-83`、`internal/i18n/{i18n_test.go:134-165,catalog/zh/model.ini,catalog/zh/cmd.ini}`。
- 实跑基线回放：`git archive 00c890f7 | tar -x -C tmp/review/suga-fork-plan/base`，写入按 T1 描述的 characterization 用例，`go test ./pkg/agentcore -run TestRuntime_Run_ToolTranscriptLeadOnFirstCall -count=1`。用例存档 `tmp/review/suga-fork-plan/r2-characterization_test.go.txt`（基线目录已删）。

## 验收标准

- 上轮 CORE_BLOCKING（PF1、PF3、PF4、PF5）是否闭合；PF2、PF6–PF9 修订是否正确可执行；修订是否引入新的 CORE_BLOCKING 或矛盾。
- review-contract：五类 CORE_BLOCKING + necessity proof；只报影响核心验收或安全的问题。

## 发现

### 上轮发现闭合情况

| 发现 | 结论 | 依据 |
|---|---|---|
| PF1 | 闭合 | T1 合同写定 `ExpandToolCalls` 只分配 Content/Reasoning/Metadata/批次 meta，`persistToolTranscript` 的 ToolCall 仍取 finalCall（`runtime.go:602-624`）。附注：`prepareToolCalls` 原位改 `res.ToolCalls`（`runtime.go:498`、`tool_validation.go:36-46`），tracker 若直接存 AfterModel 的指针，分身尾部拿到的是强转后的参数，不影响验收。`ValidateGenerateResult` 拒绝空 CallID（`api/provider.go:149-152`），`step-%d` 回退不影响 `ExpandToolCalls(res)` 的签名 |
| PF2 | 闭合（有一处执行陷阱，见 PF14） | 用例名能被 `-run 'TestExpandToolCalls\|TestRuntime'` 命中；实跑：按描述写的用例在基线上通过（`--- PASS`）；反向验证①会因第二条的 `tool_call` 代理名失败 |
| PF3 | 闭合 | `agentTool` 是 `wiring/runtime.go:551` 的局部变量，同函数可捕获后组装 `Runtime.StartFork`；`cli/chat.go:138` 组装 Options，可传入；nil → `cmd.bg.disabled` 已写 |
| PF4 | 闭合 | killed 分支 `agent.go:200-206` 写“已终止”、不 `Notify`；`Spec.NoEndNotice: true`（`agent.go:180`）；kill 取消 ctx → `RunChild` 走 `ctx.Err()` 分支 `Stop=cancelled`（`subagent.go:106-108`）→ label `stop=cancelled`。T4 单测、T7 ③ 与完成定义 1 一致 |
| PF5 | 部分闭合 | `fork_base`：T3 给 `ChildRequest` 加 `Meta` 并合入 `NewSessionWithMeta`（`subagent.go:79-83`），可执行，已闭合。`fork_reuse`：写入机制在代码中不存在，见 PF10 |
| PF6 | 闭合 | 三处文案都指定了键与任务；native 文案沿用英文硬编码 |
| PF7 | 闭合 | 可选接口 `ForkSeeder`、`SubAgents` 不变，`wiring/subagent.go:66` 的断言不受影响，T4 可单独编译 |
| PF8 | 闭合（附注） | tracker 加 turnID/Completed，数据源明确；`turnHooks` 已有 `result.TurnID`（`runner.go:781-790`）。附注：`observeTurnEnd` 只能放在 `runner.go` 里 `runtime.Run` 返回后、`r.finish` 之前（`runner.go:748-758`），T1 文件栏只写了“turnHooks 一项”；`completed` 的判定（如 `runErr == nil && coreResult.Status == RunCompleted`）没写，不影响核心验收 |
| PF9 | 闭合 | `TestChatCommands` 命中 `TestChatCommandsText`（`chat_commands_test.go:22`）；`TestRunningTurnPutsRewindAndForkBack`、`TestRunTurnRefusesLocalCommand`、`TestCatalogParity`、`TestLayering`、`TestSubAgent*`、`TestHandoffReuse*`、`TestAgentTool` 都存在；新名 `TestForkHeadHook`、`TestForkContextMode`、`TestForkRunner`、`TestForkSeed*`、`TestForkBg` 与现有 `TestFork*`（`:fork N`）不冲突 |
| i18n | 一致 | `[model]` 段已有命名占位符先例（`model.ini:32-34` 的 `{type}{size}{ref}`）；`TestCatalogParity` 逐键比对中英占位符（`i18n_test.go:160-163`），`{task}`、`{name}`、`{id}`、`{desc}`、`{reason}`、`{running}`、`{limit}` 只要两份一致就能通过 |

### PF10 [HIGH] `fork_reuse` 写进 `ModelEvent.Metadata`，到不了 trace；负责的文件不在 T2 清单内

- disposition: CORE_BLOCKING [BUG]
- 证据: 计划 T2 动作（plan:145）写“首次调用时在 `event.Metadata` 写 `fork_reuse`……（observe 把 model 事件 metadata 中这两个键拷进 trace attrs）”。代码：`callModel` 的 `ModelEvent.Metadata` 只是 `state.Metadata` 的克隆，BeforeModel 之后只把 `event.Request` 发给 provider，Metadata 被丢弃（`runtime.go:375-402`）；trace 里的 model 事件只由 `recorder.attemptObserver` 用 `ctx` 与 `llm.Attempt` 生成（`observe.go:122-200`），`llm.Attempt` 也不带请求或 hook 元数据（`llm/observe.go:12-120`）；personal 里没有任何地方读 `ModelEvent.Metadata` 写 trace（`grep event.Metadata internal/personal` 无结果）。hook 也不能改变 provider 调用所用的 ctx。T2 文件清单没有 `observe.go`。
- 影响: 照 T2 字面实现，trace 中没有 `fork_reuse` / `fork_reuse_note`；T2 自己的断言“trace 中只有首个 model 事件带 `fork_reuse`”必失败，任务书要求 fail-closed 和“只改本任务列出的文件”，执行者只能停下或越界改 `observe.go`，改法各不相同（recorder 读 `Config.ForkSeed`、ctx 值、prefix tracker 附带字段等）。
- necessity proof:
  - core_acceptance: 完成定义 2（分身首个 model 事件带 `fork_reuse`）；T7 ptye2e 断言 `fork_reuse=prefix`。
  - necessary_causal_chain: T2 执行者按计划在 `forkHeadHook.BeforeModel` 写 `event.Metadata["fork_reuse"]` → 运行时丢弃该 map → `attemptObserver` 生成的 model 事件没有该 attr → T2 的 trace 断言失败并 fail-closed 停止；即使越界补救，T7 `inspect trace --json` 的 `fork_reuse=prefix` 能否通过取决于临场方案 → 完成定义 2 不满足或不唯一。
  - minimal_owner: 计划 T2 的文件与动作。
  - deletion_alternative: FAIL（完成定义 2 要求）。
  - scope_effect: 不新增 Module/Interface/Schema；只把 `internal/personal/observe.go` 列入 T2 文件。
- 建议: T2 文件加 `internal/personal/observe.go`，动作改为：分身 Runner 的 recorder 在 `personal.New` 时从 `Config.ForkSeed` 取 `Reuse/ReuseNote`，`attemptObserver` 在该会话第一个无 `purpose` 的 model 事件上写 `fork_reuse`（及非空的 `fork_reuse_note`），之后不再写；删掉“写 `event.Metadata`”的说法。T2 单测断言不变。

### PF11 [HIGH] T1 让模型触发的分身在配置 host hook 时拒绝，ACP 每个回合都带 gate hook，ACP 中模型触发的分身永远启动失败

- disposition: CORE_BLOCKING [BUG]
- 证据: 0.2 的 T1（plan:135）新增“`ForkSeedForCall` ……配置了 host hook → `host_hook`”（0.1 的 T1 没有这条，只在 T5 有）。设计 F7（design:188）写“模型触发总用当步请求（`prefix`）”，只有 `:bg` 重建时 host hook 才拒绝；设计关键流程 1 标题“模型触发（chat / run / ACP 相同）”、D1“`run` 与 ACP 只经模型触发”。代码：host hook 的判定就是 `len(r.cfg.Hooks) > 0`（`segment_handoff_reuse.go:101-103` 的先例）；chat/run 经 `wiring/runtime.go:325` 把 `opts.Hooks` 置 nil，生产里只有 ACP 会设置它——ACP 每个回合的 `TurnGate` 本身是一个 `agentcore.Hook`（`acpserver/server.go:74-83`），`cli/acp.go:76-85` 在回合中把 `[]agentcore.Hook{gate}` 传给 `wiring.Open`（`runtime.go:811` → `personal.Config.Hooks`）。PF8 的处理行写“配置了 host hook 时无论 prefix 还是重建都拒绝（你的决定）”，针对的是 `:bg`（T5 表格）。
- 影响: 照 T1 实施后，ACP（gofer 驱动 suga 的方式）里模型每次调用 `agent{type: fork}` 都得到 `agent: fork unavailable: host_hook`；而 chat/run 永远不会触发 host_hook，所以计划里所有 host_hook 用例都只是人为注入 `NopHook` 的单测，没有任何验证会暴露 ACP 中的这个结果。`prefixHook` 排在 `cfg.Hooks` 之前（`runner.go:806-812`），`lastRequest` 是 host hook 改写前的请求，模型触发路径不依赖重建，本来不需要这条拒绝。
- necessity proof:
  - core_acceptance: 完成定义 1（模型调用 `agent{type: fork}` 时分身作为后台任务启动）与设计 D1/关键流程 1 的“ACP 相同”。
  - necessary_causal_chain: 照 T1 在 `ForkSeedForCall` 开头判 `len(r.cfg.Hooks) > 0` → ACP 回合中 `cfg.Hooks = [gate]` → 每次模型派分身都返回 `fork unavailable: host_hook` 工具错误 → ACP 用户（gofer 会话）完全用不了分身，而 T1–T9 的测试全部通过。
  - minimal_owner: 计划 T1 动作第 4 条（与 T5 表格第 1 行的适用范围）。
  - deletion_alternative: PASS（删除 T1 中这一条，模型触发按设计 F7 总用当步请求；T5 的 `:bg` 表格不变；T1 验证中的“host hook 时 `host_hook`”改为“配置 hook 时仍为 `prefix`”）。
  - scope_effect: 无新增，删除一条判定。
- 建议: 按删除方案处理。如果“你的决定”确实也要覆盖模型触发路径，就需要在计划里明确写“ACP 中模型触发的分身一律拒绝”，同步修改完成定义 1、可追溯性 D1 行和设计流程 1 的“ACP 相同”，并请用户在知道 ACP gate 也算 host hook 的前提下确认。

### PF12 [MEDIUM] `ForkUnavailableError.Hint()` 返回键名还是译文没写清，native 无法翻译

- disposition: CORE_CORRECTIVE（`user_visible_effect: true`）
- 证据: T1“`Hint()` 对 `context` 返回 `model.fork.too_large`”；T4“错误实现 `Hint() string` 时追加提示”（native 不依赖 i18n，plan:101）；T6“`context` 时加 `Hint()`”。`ForkUnavailableError struct{ Reason string }` 不带 translator。
- 影响: 有的实现返回键名，模型就会在工具结果里看到字面量 `model.fork.too_large`；有的会在 Runner 里译好再放进错误。模型和 `:bg` 看到的提示因实现而异。
- 建议: 写明 `ForkUnavailableError` 带一个 `hint string` 字段，由 Runner 用 `r.cfg.Translator.T("model.fork.too_large")` 填好，`Hint()` 返回译文；T1 单测断言 `Hint()` 等于译文。

### PF13 [MEDIUM] 满额拒绝：`tasks.Manager.Start` 的错误不带类型，`:bg` 的 `cmd.bg.full`（`{running}/{limit}`）与 native 的满额文案都没写数据来源

- disposition: CORE_CORRECTIVE（`user_visible_effect: true`）
- 证据: `Manager.Start` 满额返回的是不带类型的 `fmt.Errorf("%d background tasks are already running (the limit)…")`（`tasks.go:281-284`）；T4 要求 `StartFork` 返回另一段带 `limit %d` 的文案；T6 要求 chat 识别满额并填 `{running}`、`{limit}`，但 `Options.StartFork` 只返回 `error`。`Manager.Running(sid)`（`tasks.go:778`）与 `Limits()`（`tasks.go:245`）可以用来预检。
- 影响: chat 只能解析英文字符串，或者退回到 `cmd.bg.unavailable` 显示英文原文；`TestForkBg` 的“满额文案”断言能否通过、显示什么都由执行者临场决定。满额时都会拒绝（D5 不受影响）。
- 建议: T4 定义 `native.ForkLimitError{Running, Limit int}`（或在 `StartFork` 里用 `Running`/`Limits` 预检后返回它），`Error()` 为上文的英文文案；T6 用 `errors.As` 取数填 `cmd.bg.full`。

### PF14 [LOW] characterization 用例直接用 `newRuntimeTestStorage` 会在基线上失败（Reasoning 被测试替身丢掉）

- disposition: CORE_CORRECTIVE（`user_visible_effect: false`）
- 证据: 实跑：第一版用例经 `newRuntimeTestStorage` 读落库消息，基线失败，`Reasoning:""`。原因是替身的 `cloneMessages` 不复制 `Reasoning`（`runtime_doubles_test.go:453-469`），运行时本身是正确的（`runtime.go:635`）。改用包一层只追加原始消息的 storage 后，基线 `--- PASS`（用例见 `tmp/review/suga-fork-plan/r2-characterization_test.go.txt`）。另外，回放命令 `tar -x -C ../../tmp/suga-hao6/base` 需要目录先存在；它解析到 `inhere-tools/suga/tmp/`（已在 `.gitignore`），与 T9 的 WP 级 `tmp/suga-hao6/` 不是同一处；执行者在 Windows 上，PowerShell 5 的管道会损坏二进制 tar 流。
- 影响: 执行者会先遇到一次“基线回放不通过”。计划写了“修用例”的回退，但如果把替身的 `cloneMessages` 改成复制 Reasoning，就改动了共享替身，范围比预期大。
- 建议: T1 写明 characterization 用例自带一个保留原始消息的 storage 包装（或断言 `RunResult`/state 中的消息），不改 `runtime_doubles_test.go`；回放命令写成先 `mkdir`，并说明用 `cmd /c` 或 Git Bash 执行管道。

## 覆盖缺口与限制

- 只实跑了 T1 characterization 的基线回放，其他判断都来自基线源码的静态核对。
- 没有核对 `requestBaseBetweenTurns` 为 `ForkSeedBetweenTurns` 构造 contextBuilder 的细节（`compact.go:825-840` 的现有构造可以复用），也没有核对 T8 文档。
- 安全（SR1301）：只读白名单与 `forkDenyTools` 合同相比 0.1 没有变化，没有发现新的越权路径；T9 真实 provider 脚本对秘密的处理没有变化。

## 结论

BLOCKED。仍开放的 CORE_BLOCKING：PF10（`fork_reuse` 的写入机制在代码中不存在，T2 不可执行）、PF11（T1 新增的 host_hook 拒绝让 ACP 中模型触发的分身全部失败，与设计 F7/流程 1 矛盾）。PF1、PF3、PF4 闭合，PF5 的 `fork_base` 部分闭合（`fork_reuse` 转入 PF10）。

## 剩余风险与后续

- PF10、PF11 都是局部修订（T2 加 `observe.go` 并改写机制；T1 删一条判定）。下一轮只复核 T1、T2 两处及对应的完成定义/可追溯性行。
- PF12、PF13 标 `user_visible_effect: true`，建议随这次修订一并写清；PF14 是执行提示。
