<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 1 轮 Spec/Executability 评审

## 评审目标与范围

- 目标: `inhere-tools/suga` 仓库（Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`）`docs/design/2026-11-12-multi-command-hooks-design.md`，candidate commit `b11cd717`，issue `suga-5xmf`
- 修订版本: Draft 0.1
- 评审者: 独立 Spec/Executability 轴评审 agent（Claude，REPORT_ONLY，未改仓库、未提交）
- 日期: 2026-11-12
- 排除项: Q1–Q7 选项本身的取舍（只指出推荐项在代码上的不可行或隐藏代价）；Standards/Governance 轴（另一轴负责）；gofer 服务端内部状态机的完整审计

## 输入与方法

- 规范：`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/payload.md` 全文；review-contract 与 review 模板取自 `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`。
- 仓库 `AGENTS.md`；设计全文（252 行）。
- 代码：`internal/hooks/` 全部（`hooks.go` 及 4 个测试文件）；调用方 `internal/chat/{chat.go,chat_turn.go,hooks_gate.go,hooks_record.go,stop_background.go,followup.go}`、`internal/cli/{chat.go,config.go}`、`internal/wiring/localenv.go`、`internal/config/{settings_registry.go,config_candidate.go}`；`internal/personal` 只有注释引用 hooks（`analyze.go:119`），无调用。
- gofer 侧：`docs/usage/gofer-personal-agent.md:206-214,374-411`；gofer 仓库 `internal/hookrelay/run.go`（事件分发 `:205-262`、Stop `:570-680`、catch-up `:933-937`、harness 判定 `:1082-1084`）、`payload.go`（`stop_hook_active` 只解析不使用）。
- 依赖方向：`go list -deps ./internal/hooks`（只依赖 `pkg/agentcore/api`）与 `./internal/config`（不依赖 `internal/hooks`）。
- 方法：逐事件把设计规则映射到现有消费点，检查合并结果在每个消费点的含义是否唯一；对每类发现做同类穷尽。

## 验收标准

- review-contract（四级 severity + disposition；`CORE_BLOCKING` 限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 并附 necessity proof；同类穷尽）。
- 设计自身的核心目标与 D2：只配 `hooks.command` 时行为、超时、等待、Interrupt、trace 与现状一致（设计 `:16`、`:31`、`:213`）。
- 规则实现唯一、可由计划直接拆解；验证要求能证明所述行为（AGENTS.md“验证”节）。

## 核对结论摘要（非发现项）

- **事件全集**：与代码一致。chat 只构造 SessionStart（`chat.go:492`）、UserPromptSubmit（`chat_turn.go:235`）、PostToolUse（`chat_turn.go:295`）、Stop（`chat_turn.go:395`）、SessionEnd（`chat.go:539`）、PreToolUse（`hooks_gate.go:51`）、Interrupt（`hooks.go:408`）；无其他 `Event{Name:…}` 构造点。
- **PreToolUse fail closed 与 updatedInput 串联**：可实现。现有 `UpdatedInput` 是整体替换参数（`hooks_gate.go:72-77`），链式传递语义唯一；`GateOf` 在录制包装前取门（`chat.go:227`），多条目 client 实现 `ToolGate` 即可。
- **SessionStart/UserPromptSubmit 合并**：`additionalContext` 空行连接与 `appendContext` 现有拼法一致（`chat.go:848-856`）；`termtext.Clean` 保留换行（`termtext.go:54`），多条 notice 用换行拼接可行。
- **gofer `--wait` 链路**：设计提到的“续跑 block 时 gofer 收不到 Stop、web 显示运行中”**成立**——gofer 的 Stop 是携带 `LastMessage`/`IdleSec`/`ClearProgress` 的心跳并在此打开 relay turn（`run.go:570-583,664`），被短路时这些都不发生；但 suga 侧没有因此失效的契约：gofer 不使用 `stop_hook_active`，下一次注入回合的 UserPromptSubmit 带 `injected=true`（`chat_turn.go:239`），gofer 视为 harness 输入、保持 relay、并经 catch-up 补投 job 通知（`run.go:212-226,935-937`）。结论：风险成立且可接受，不需要新的设计层机制，但描述需补全（见 HF10）。打断引起的 gofer 侧遗留 turn 风险见 HF3。
- **Reader 落点的 import 方向**：两个方向都无环（`internal/hooks` 不依赖 `internal/config`，反之亦然），无内部分层守卫测试约束；需要的是设计给出确定落点（HF7）。

## 发现

### HF1 [HIGH] `stop_args 需配 stop_wait_sec` 校验未限定范围，会让现有合法的兼容配置启动失败

- disposition: `CORE_BLOCKING` / `[BUG]`
- 证据: 设计 `:195` 把“`stop_args` 需配 `stop_wait_sec`”列为“启动与 `config set` 同一套校验”，未限定只作用于 `hooks.commands` 条目；表格 `:103` 同样只说“与 `stop_wait_sec` 同时配”。现状 `hooks.New` 不做这项检查（`hooks.go:222-255`），且文档明确允许 `stop_background: true` 下只配 `stop_args` 不配 `stop_wait_sec`（`docs/usage/gofer-personal-agent.md:213`、`:402`“不设该值时由 `tasks.max_duration` 限制”）；只配 `stop_args` 的旧配置当前也能启动（只是按 `timeout_ms` 结束）。
- 影响: 按 `:195` 实现会让这些现有用户升级后 `suga chat` 启动报错，直接违反核心目标 `:16`“只配 `hooks.command` 的现有用户行为不变”与 D2 `:213`；按 D2 实现则 `:195` 不成立——实现不唯一，两种读法结果相反。
- necessity proof: 存在仓库内文档化的合法配置（`stop_background: true` + `stop_args` 无 `stop_wait_sec`），在 `:195` 规则下必然被拒；这是用户可见的结果错误，且无法由计划阶段在不改设计语义的前提下自行裁决（需要在 D2 与 `:195` 之间二选一）。
- 同类穷尽: 设计中所有“校验”清单出现点：`:32`（启动报错指出第几条）、`:101`（未知事件名）、`:108`（events 列 Pre/Post 但无 glob）、`:135`（等待型条目多于一个）、`:150`（reader 与启动同一校验）、`:195`（清单）。其中只有 `:195` 的 `stop_args` 项会触达兼容条目；“至多一个等待型条目”对兼容条目只在同时存在 `hooks.commands` 时才可能触发，不影响只配 `hooks.command` 的用户。
- 建议: `:195`、`:103` 写明该项只校验 `hooks.commands` 条目；兼容条目保持现有接受范围（含 `stop_background` 下无 `stop_wait_sec` 的 7 天上限）。

### HF2 [MEDIUM] Stop 合并结果的 `Err`、`Notice`、`Decision` 未定义，而每个消费点都依赖它们

- disposition: `CORE_CORRECTIVE`
- 证据: 设计表格 `:128` 只写“合并见 Q3、Q4”，`:146` 只定义 reason 合并。消费点：前台 `chat.go:782-790` 以 `result.Err != nil || Decision != "block" || Reason == ""` 判无决定，并打印 `result.Response.Notice`；后台 `stop_background.go:65-81` `Err != nil` 时任务以错误结束、打印 Notice 与 decision；交回路径 `hooks.go:418` 与 `hooks_record.go:119` 都要求 `Err == nil` 才交回 block reason；trace `hooks_record.go:88-96` 按 Err 定 status。
- 影响: 若计划按“合并 Err = 各条错误之 join”实现，阶段一 A 条 block、B 条超时时，前台与交回路径都会丢掉 A 的 block（续跑失效），后台任务被记为失败；若按“有 block 则 Err=nil”实现则正常。设计 `:128`“单条失败视为无决定”只能推出部分规则。Notice 在阶段一多条、或阶段一 + 阶段二时如何拼接也未写。
- 建议: 在 §3 写定 Stop 合并：有合并 block 时 `Err=nil`；无 block 时 `Err` 取阶段二等待条目的错误（与现状一致），无等待条目时 `Err=nil`（阶段一失败只记日志）；`Notice` 按执行顺序换行拼接；`Decision` 有 block 为 `block`，否则取等待条目的回答。交回路径（`Interrupt` 返回值）沿用同一判定。

### HF3 [MEDIUM] “只给正在运行的条目发 Interrupt”在阶段切换时有竞态，可能让 gofer 打开的 turn 被杀而收不到 Interrupt

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:139`、`:181`。现有合同是先发 Interrupt（1 秒）再取消（`hooks.go:407-415`，测试 `hooks_test.go:342-361` 守住顺序）。多条目流水线中，Interrupt 发给阶段一条目的同时，若该条目恰好结束且阶段二尚未被冻结，等待条目（gofer）可能随后启动并在 `OpenSessionTurn` 后被取消（gofer `run.go:664`），却没有收到 Interrupt——而 gofer 正是靠 Interrupt 心跳结算 open turn（`run.go:246-248,265-282`）。
- 影响: web 侧可能短暂残留“等你回复”的 turn（gofer 服务端在下一次 UserPromptSubmit 时也会结算，故不是持久数据问题）；同时“未启动的条目不收 Interrupt”的断言在该竞态下不稳定。
- 建议: 写明 Interrupt 的原子顺序：①先冻结流水线（之后不再启动任何条目）；②若此刻有条目在跑，向它发 Interrupt（受其 `events` 约束）；③再取消；④两条目之间（无条目在跑）时不发 Interrupt。单元测试覆盖“阶段一结束瞬间打断”。

### HF4 [MEDIUM] 按条目改名后 PostToolUse 事件须由 hooks 构造，架构里“chat_turn 逻辑不变”与之矛盾

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:166` 把 `PostToolName/PreToolName` 改为“是否有条目订阅”，名由各条目决定；`:168` 只说 `hooks_gate.go` 改为传原始工具名，`chat_turn.go`“只消费合并结果，逻辑不变”。但 `chat_turn.go:291-297` 用 `PostToolName` 返回的改名结果填 `postTool.ToolName` 后整体 `Call`，与 `hooks_gate.go:175-181` 是同一模式。
- 同类穷尽: 依赖“返回改名后的工具名”的点：`chat_turn.go:291-296`、`hooks_gate.go:175-181`、`hooks.go:81-83,93-95`（接口注释）、`hooks.go:291-293`；以及实现假 `Client`/`ToolGate` 的 chat 测试（`chat_hooks_test.go`、`hooks_gate_test.go`、`hooks_record_test.go`、`stop_background_test.go`、`chat_hooks_config_test.go`、`followup_test.go`）。
- 建议: 架构写明 `Client`/`ToolGate` 的新签名（如 `PostToolName(tool) bool` 或保留签名但只作判定），`chat_turn.go` PostToolUse 改传原始名；把这些测试列入受影响清单。

### HF5 [MEDIUM] `events` 与工具 glob 的关系只写了一个方向

- disposition: `CORE_CORRECTIVE`
- 证据: `:108` 写“PreToolUse/PostToolUse 仍只由工具 glob 决定”且“列在 `events` 里但没有 glob 时启动报错”，未写反方向：`events: [Stop]` 但配了 `pre_tool_use.tools` 的条目是否收 PreToolUse。按“只由 glob 决定”推得“收”，但同一句又把 `events` 当作订阅声明，读者易得出相反结论；PreToolUse 是执行前拒绝门，两种读法的门开关相反。
- 建议: 明确二选一并写进表格：要么“Pre/Post 只看 glob，`events` 中列出与否不影响”，要么“有 glob 而 `events` 显式给出但不含该事件时启动报错”。推荐后者（与 `:108` 的防误配理由对称）。

### HF6 [MEDIUM] “启动与 `config set` 同一套校验”只对 `hooks.commands` 成立，跨键约束可经其他 `hooks.*` 键绕过

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:150`、`:195`。“至多一个等待型条目”涉及兼容条目，而兼容条目是否等待型由 `hooks.stop_wait_sec` / `hooks.stop_background` 决定（`hooks.go:249-255`）。这些键是简单类型、无 Reader（`settings_registry.go:273-279`），`config set` 只按被改键的 Reader 校验（`config_candidate.go:41`、`cli/config.go:165`）。
- 影响: 已有一个等待型 `hooks.commands` 条目时，`suga config set hooks.stop_wait_sec 7140` 或 `hooks.stop_background true` 会成功写入，之后 `suga chat` 启动报错——与 `:150` 的承诺不符。
- 同类穷尽: 参与跨键校验的键：`hooks.command`（是否存在兼容条目）、`hooks.stop_wait_sec`、`hooks.stop_background`、`hooks.stop_args`（若 HF1 后仍对兼容条目有约束）、`hooks.timeout_ms`（条目默认超时，仅取值合法性）。
- 建议: 给上述 `hooks.*` 描述符统一挂同一个 Reader（如 `"hooks"`），或在设计中如实写明“跨键约束只在启动时检查”。

### HF7 [LOW] 校验 Reader 的落点需在设计中定死

- disposition: `CORE_CORRECTIVE`
- 证据: `:169` 写“校验函数放 `internal/hooks`，经注册表的 reader 分发调用……计划阶段核对 import 方向，必要时把纯解析放到无依赖的小包”。实际：Reader 分发是 `internal/config` 内的 switch，全部调用 `config.ReadXxx`（`settings_registry.go:680-735`，如 `ReadMCPServerSettings`）；`go list -deps` 显示两包互不依赖，两种方向都无环。
- 影响: 留到计划会产生三种可行实现（config→hooks、hooks 调 config 的解析、新小包），属可在设计阶段消除的不唯一。
- 建议: 定为在 `internal/config` 增加 `ReadHookEntries()`（与 `ReadMCPServerSettings` 同模式、读 gkconfig、返回解析后的条目与校验错误），`hooks.New` 消费它；避免基础包 `internal/config` 反向依赖功能包。

### HF8 [MEDIUM] Q6 推荐 A 的隐藏代价：经 `hooks.commands` 可写入 PreToolUse 门配置，与安全配置键设计对 `hooks.pre_tool_use.*` 的处理不一致

- disposition: `CORE_CORRECTIVE`
- 证据: 本设计条目字段含 `pre_tool_use.tools/.name`（`:105`），Q6 A 让 `hooks.commands` 可写（`:150`、`:243`）。安全配置键设计 0.7 有意把 `hooks.pre_tool_use.*` 定为“只标 protected、不可写，不新增 CLI 能力”（`2026-11-11-protected-config-keys-design.md:16,48,120`；评审 F33 `docs/review/2026-11-11-protected-config-keys/spec-review.md:40`）。设计 `:153` 只说“补描述符一事由 `suga-hyj7` 承担”，未提及这一差异。
- 影响: 选 Q6 A 后，执行前拒绝门配置事实上获得 CLI 写入口（人在终端可写），推翻了已批准设计的取舍而无记录。（`:152` 的“先 Writable 后 Protected”窗口不是新暴露面：`hooks.command` 当前已可写且未保护。）
- 建议: Q6 说明中写明这一代价与对已批准设计的影响，并给出处理：接受并在两份设计间同步说明，或让 `config set hooks.commands` 拒绝含 `pre_tool_use` 的条目。

### HF9 [LOW] Interrupt 的 trace 归属与触发来源描述不完整

- disposition: `CORE_CORRECTIVE`
- 证据: 现状 Interrupt 调用不进 trace（`stopWait.call` 是未包装的 `commandClient.Call`，`hooks.go:313,413`；`recordingWait.Interrupt` 只转发，`hooks_record.go:112-125`）。设计 `:199`“每个条目每次运行记一条”未说是否包含 Interrupt；`:206` 的 ptye2e 要“断言 Interrupt 只到等待条目”，但未说用什么观察（trace 还是假 hook 的落盘记录）。`:138` 列的打断来源缺少任务唤醒（`chat.go:748,792`）、Tab 排队消息（`followup.go:119-123`）、等待中的 Ctrl-C/EOF（`chat.go:830-833`）。
- 建议: 写明 Interrupt 是否记 trace（若记，属于 D2 之外的新增行为需列出）；ptye2e 的观察手段写定；打断来源按代码列全（行为由 `Wait.Interrupt` 统一，补全只为测试覆盖与读者理解）。

### HF10 [LOW] 对 gofer 影响的描述只写了“运行中”，漏了被短路的其他 Stop 职责

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:197`。gofer Stop 还承担：上报本回合 `LastMessage`/`IdleSec`/`ClearProgress`（`run.go:581-583`）、接管通知 `Notice`（`run.go:590-593`）、信件与 watched job 的 Stop 送达（`run.go:596-660`）。续跑 block 期间这些都推迟到续跑结束后的那次 Stop；中间回合的最后回复不会出现在 web。job 通知有 UserPromptSubmit catch-up 兜底（generic 方言，`run.go:933-937`）。另外阶段一被打断时，gofer 该回合既无 Stop 也无 Interrupt。
- 影响: 不构成错误（风险判断见“核对结论摘要”），但读者与 gofer 维护方无法据 `:197` 预判 web 上缺失的中间回复与延迟的接管提示。
- 建议: `:197` 补一句列出上述被推迟的项，并说明 job 通知经 catch-up 不丢。

### HF11 [LOW] 验证要求有缺口

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:203-208`。
- 缺口: ①chat 层 PreToolUse 多条目：逐条 trace（`hooks_gate.go:56-58` 现只记一条）与审批看到最终改写参数，仅列了 `internal/hooks` 单元；②HF3 的阶段切换打断；③HF6 的跨键 `config set`；④后台 Stop 在阶段一被新 Stop 杀掉；⑤HF2 的合并 Err 规则（阶段一 block + 另一条失败时仍交回/续跑）；⑥`hooks_test.go:342-372` 直接构造 `stopWait{call,event,cancel,done}`，`:204`“现有测试不改断言”需说明夹具可调整或保留 `stopWait` 作为单条目原语。
- 建议: 在验证要求中逐项补上。

### HF12 [LOW] 小的事实与表述偏差

- disposition: `CORE_CORRECTIVE`
- 证据: ①`:67` 引“前台等待 `chat.go:555-563`”，前台 block 实际处理在 `chat.go:782-790`；②`:199` “argv[0] 基名”未指明用 `filepath.Base`（Windows 下 `path.Base` 不切反斜杠）；③现有 `hooks.go:234-240` 对 `pre_tool_use.tools` 的 glob 错误也报成 `hooks.post_tool_use.tools`，新条目校验要“指出第几条”时应一并按实际键名/条目报错。
- 建议: 顺带修正。

## 覆盖缺口与限制

- 未运行代码或测试；结论基于静态阅读。
- gofer 服务端对“缺 Stop、连续 injected UserPromptSubmit”的会话状态只读了 hookrelay 客户端侧，未审计 `sessionrelay` 服务端状态机；“web 显示运行中”的判断依据是 Stop 心跳负责清进度与上报最后回复。
- IDEV-STD 续跑 hook 的实现未读（只采信本设计 `:26` 的描述，特别是它不依赖 `stop_hook_active`）。
- Standards/Governance 轴（代码组织、GR107–GR110 结构核对）不在本轴范围。

## 结论

BLOCKED：HF1（`CORE_BLOCKING`/BUG）——`stop_args` 需配 `stop_wait_sec` 的校验未限定到新条目，与 D2 冲突，按字面实现会让文档化的现有配置启动失败。其余 HF2–HF12 为 `CORE_CORRECTIVE`。

## 剩余风险与后续

- 修订时按类自查：①所有校验规则对兼容条目的作用范围（HF1、HF6）；②合并结果每个字段在每个消费点的含义（HF2）；③打断与阶段切换的时序（HF3、HF9）；④接口签名变化波及的调用方与测试（HF4、HF11）。
- 第 2 轮 confirmation 读 `git diff b11cd717..<新 candidate> -- docs/design/2026-11-12-multi-command-hooks-design.md` 及依赖章节。
- Q6 若选 A，需与安全配置键设计（`suga-hyj7`）同步处理 HF8。
