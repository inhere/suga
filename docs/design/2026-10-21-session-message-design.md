<!-- template_id: design; template_version: 1.1.1 -->
# 会话之间互发消息设计（不依赖 gofer 的 session_message 工具）

> 状态：Draft 0.1 / 已批准（用户 2026-10-21：按推荐），可实施
> 日期：2026-10-21
> 关联：issue `suga-hko7`；`suga-wy40`（短名称与 `suga send`）；`docs/design/2026-10-20-send-image-design.md`；`internal/personal/process/process.go`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-21 | 监督（Claude） | 用户 2026-10-20：“可以带上 hko7——配置里可以关闭这个工具”；此前用户强调不能只依赖 gofer。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：模型在一个 suga 会话里可以列出并给同一台机器、同一 SUGA_HOME 下正在运行的其它 suga 会话发消息，不需要 gofer；可以在配置里关闭；两个会话不会互相回复到停不下来。
- allowed_scope：`pkg/agentcore/tools/native`（新工具，经宿主注入投递函数，保持分层）、`internal/wiring`（注入与配置）、`internal/personal/process`（请求的来源字段与跳数）、`internal/chat`（接收端显示与跳数上限）、测试与文档。
- non_goals：跨机器、跨 SUGA_HOME；通过 gofer 投递（模型已可用 shell 调 `gofer session mail`，本工具不包装它）；给 `run` 会话加进程登记。
- expansion_policy：DEFER_OR_REQUEST。
- review budget：低–中。
- 停止条件：用户批准前不改代码。

## 背景与目标

`suga send --session <短名称|ID> "文本"` 已能把消息投递给正在运行的 chat 会话（进程记录在 `$SUGA_HOME/run`，`process.Request{type, session_id, text, from, mode, wait}`）。模型自己没有对应的工具；有 gofer 时可以用 shell 调 `gofer session mail/say`，没有 gofer 时会话之间就无法通信。

两个约束：

1. **只有 chat 登记进程**（`process.Start` 只在 `internal/chat/session_name.go` 调用），`run`/ACP 会话收不到消息。
2. **投递给空闲的 chat 会立刻开始一个回合**（`internal/chat/steer_send.go:61`）。两个会话都能用工具发消息时，A 发给 B → B 开始回合并回信 → A 开始回合并回信……会一直循环。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| 投递协议与进程记录、短名称解析 | `internal/personal/process/process.go`、`internal/cli/send.go` | 工具复用同一套解析与投递，不另造协议。 |
| 空闲 chat 收到消息立即开始回合 | `internal/chat/steer_send.go:61` | 必须有自动回合的跳数上限。 |
| 只有 chat 登记进程 | `process.Start` 调用方 | 列表与投递只覆盖 chat 会话。 |
| 外部内容是数据不是指令（本批 suga-eygi） | `internal/wiring/prompt.go` | 收到的会话消息要标明来源，模型不把它当用户指令。 |
| `pkg/agentcore` 不得依赖 `internal/*` | `layering_test.go` | 工具在 native，投递函数由 wiring 注入。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

1. **工具 `session_message`**（一个工具两个动作，减少工具数）：
   - `action: "list"`：列出本机同一 SUGA_HOME 下正在运行的 chat 会话（短名称、完整会话 ID、工作目录、状态 idle/busy、标题），不含自己。
   - `action: "send", to: <短名称|会话 ID>, text: <消息>, mode?: "follow_up"|"steer"`：投递一条消息。`to` 不能是自己。结果写明“已投递（对方空闲，已开始处理）/已排队（对方正忙）/对方不存在或已退出”。
   - 只读模式下可用（不改文件）；不需要人工审批。
2. **配置**：`tools.session_message.enabled`（默认 true）；关闭时工具不出现在工具表里。环境变量 `SUGA_SESSION_MESSAGE=off` 同样关闭（便于 gofer job 等场景统一关）。
3. **来源标记**：请求的 `from` 写成 `session:<发送方完整会话 ID>`，另加 `from_name`（短名称）；接收端把消息作为用户消息放进上下文时包一层标记：`[来自会话 <短名称>（<会话 ID>）的消息] …`，chat 界面同样显示来源。配合 suga-eygi 的提示词，模型把它当同伴的请求或报告，不当用户指令或批准。
4. **防止互相回复停不下来**：请求带 `hops`（跳数）字段。
   - 用户在终端输入或 `suga send`（命令行）发出的消息跳数为 0。
   - 一个回合由跳数为 h 的会话消息启动时，该回合里 `session_message send` 发出的消息跳数为 h+1。
   - 接收端收到跳数 ≥ 3（常量，可配置 `tools.session_message.max_hops`）的消息时**不自动开始回合**：只排队并在 chat 界面提示“来自会话 X 的消息已排队（连续自动回复已达上限）”，等用户下一次输入时一起处理。
   - 单回合内 `send` 最多 5 次（超过时工具返回错误说明上限）。
5. **兼容**：旧版本接收端忽略 `hops`/`from_name`（文字照常送达，但不会受跳数限制保护）——工具在 `list` 结果里标出对方不支持跳数限制（进程记录 `features` 无 `hops`），`send` 给这类会话时在结果里提示风险但仍投递。

## 关键流程

见“总体方案”1、4。

## 架构

- `pkg/agentcore/tools/native/session_message.go`：工具定义与参数校验；通过 `native.Env.SessionMessenger`（接口：List、Send）调用宿主。
- `internal/wiring/session_message.go`：实现接口（复用 `internal/personal/process` 的记录读取、短名称解析与投递），按配置决定是否注册工具；当前会话 ID 与当前回合的跳数从 ctx 取。
- `internal/personal/process`：`Request.Hops`、`Request.FromName`；`Record.Features` 增加 `hops`。
- `internal/chat`：接收端按跳数决定是否自动开始回合，显示来源。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 一个工具两个动作 | 工具表少一项；列出和发送总是一起用。 |
| D2 | 默认开启、可在配置关闭 | 用户 2026-10-20 要求可关闭；同机会话协作是主要用途。 |
| D3 | 跳数上限而不是完全禁止自动回合 | 保留“对方空闲时立即处理”的便利，同时防止无限往返。 |
| D4 | 不包装 gofer | 目标是不依赖 gofer；有 gofer 时模型仍可直接用 `gofer session mail`。 |

## 安全、数据、运维与回滚

- 只能投递给同一 SUGA_HOME 下自己登记的进程；消息只含文本。
- 回滚：`tools.session_message.enabled: false`。

## 测试与验证

- 单测：list 不含自己、字段完整；send 解析短名称与完整 ID、不能发给自己、对方不存在时报错；配置与环境变量关闭后工具不在工具表；单回合 5 次上限；跳数传递（用户消息 0、由会话消息启动的回合 +1）；接收端跳数 ≥ 上限时不自动开回合而排队并提示；来源标记进入用户消息；旧接收端（无 features）提示风险。
- 伪终端冒烟：两个 chat 会话（假模型），A 的模型调用 `session_message send` 给 B → B 自动开始回合并回信 → … → 第 3 跳后 B 只排队、界面出现提示；断言回合数有上限。
- 反向验证：去掉跳数检查后冒烟里的回合数超过上限（脚本用超时兜底）。
- 真实验证：两个 chat（ds flash），A 让模型问 B 的工作目录里有什么文件，B 回答，A 收到并结束。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 一个工具 `session_message`（list/send 两个动作）？ | 已定：是 |
| Q2 | 默认开启，配置 `tools.session_message.enabled` 与 `SUGA_SESSION_MESSAGE=off` 可关闭？ | 已定：是 |
| Q3 | 连续自动回合跳数上限 3、单回合发送上限 5？ | 已定：是 |
| Q4 | 只覆盖 chat 会话（run/ACP 不登记进程，本期不改）？ | 已定：是 |

## 结论与人工计划 Gate

用 `session_message` 工具复用 send 的投递通道，加来源标记与跳数上限防止循环，可在配置关闭；用户已于 2026-10-21 按推荐批准 Q1–Q4，派出实施。

## 现状核实（2026-10-22）

> 只核实、不改行为（issue `suga-vvw2`）。行号是核对时的快照。

### 一、对方忙时消息怎么排队

- 工具的投递走与 `suga send` 同一条会话 socket：工具 → `internal/wiring/session_message.go` 的 `sessionMessenger.Send`（构造 `process.Request`，`sessionMessageRequest`）→ `personalprocess.Send`（`internal/personal/process/process.go`）→ 接收端 chat 的 `chatState.deliverMessage`（`internal/chat/steer_send.go:31`）。
- 会话消息**永远不插入**正在运行的回合：`deliverMessage` 对 `from` 以 `session:` 开头的 steer 请求强制改成 `follow_up`（`steer_send.go:51-53`），工具自身也固定 `mode = "follow_up"`（`pkg/agentcore/tools/native/session_message.go:163-166`，Schema 里没有 mode 参数）。
- 实际排队处是 `chatInputSource.DeliverImages`（`internal/chat/chat_input.go:262`）写入 `deliveries` 通道（容量 8，`chat_input.go:198`；`process.MaxQueue = 8` 只是同名常量）。`Response.Queued` 在入队时按 `s.busy || len(s.deliveries) > 0` 计算（`chat_input.go:281`），所以“已排队”= 进了对方**内存**队列，不是回合已跑。
- 队列满时返回 `process.ErrQueueFull`，工具报“对方会话 X 的投递队列已满，稍后再试”（`internal/wiring/session_message.go:117-118`），并归还本回合的发送配额（`native/session_message.go:170-174`、`220-230`），不自动重试。
- 队列不落盘：chat 退出时 `DiscardQueued` 把还在队列里的消息全部丢弃（`chat_input.go:292-306`），退出提示写 `Warning: N queued message(s) were discarded on exit and not run.`（`internal/chat/chat.go:441`）。进程崩溃同理（没有持久化）。

### 二、多条消息是否合并成一个回合

- **不合并**：每条投递是 `deliveries` 里的一个元素，chat 主循环一次取一条，每条各起一个回合（`chat.go:672-679` 与 `715-722`；`nextChatInput` 每次只返回一个 `chatInput`）。忙时连发 5 条就产生 5 个相继的回合，没有 whip 那样的“bounded digest”。
- 唯一的合并发生在**跳数已达上限**的消息上：`shouldHoldSessionMessage` 为真时 `holdSessionMessage` 把它们累积在 `chatState.sessionHeld`（`internal/chat/session_message.go:22-26`、`46-54`），下一次任意回合开始时由 `takeHeldSessionMessages` 一次性拼到该回合输入的前面（`internal/chat/chat_turn.go:184-190`），N 条被按住的消息只跑一次、且带的是“用户自己那一回合”的跳数 0。

### 三、“送达”的判定点

- **工具发送（不带 `--wait`）**：`sessionMessageRequest` 不设 `Request.Wait`（`internal/wiring/session_message.go:155-162`），`personalprocess.Send` 收到第一条 ack 就返回（`process.go:442-443`、`566-568`）。因此“送达”= 对方进程把消息收进内存队列（或 steer 插入正在运行的回合，会话消息不会走这条），**不是**对方回合提交，更不是回合成功。
- **`suga send --wait`（CLI）**：第二条响应在消息**被取走、开始一个回合**时发出（`chat.go:678`、`721` 的 `delivery.settle(nil)`），也就是“送达模型/开始跑”，不是回合提交；回合结束后成功还是失败，发送方收不到任何信号。chat 在消息还没跑就退出时回 `discarded`（`chat_input.go:292-306`、`process.go:449-459`、`575-577`，CLI 退出码 3）。
- 结论：没有任何“回合提交才算送达”的语义；发送方最多知道“进了对方队列/开始跑了”，投递结果与回合结果解耦。

### 四、失败如何处理

- 发送端是一次性的：对方不在线（`ErrNotOnline`）、选择器歧义（`ErrAmbiguous`）、队列满（`ErrQueueFull`）、旧版本不支持图片（`ErrImagesUnsupported`）都直接作为工具错误返回给模型（`native/session_message.go:167-174`），不重试、不持久化、不记账；旧版本接收端（`FeatureHops` 缺失）仍投递，只在结果里提示“会直接开始对方回合”（`native/session_message.go:179-181`）。
- 接收端没有重试屏障：一个回合失败或被取消，队列里剩下的消息在下一轮循环照样各起一个回合（`chat.go` 的主循环不读上一回合结果来暂停取用）。
- 没有消息版本/投递回执/去重：同一条消息重发就是两条（模型侧只能靠文本自己判断）。

### 五、与 whip 持久邮箱语义的对比（只读参考 `tmp/ext/whip/docs/concurrency.md:88-118`）

| 语义点 | suga 现状 | whip |
|---|---|---|
| 存储 | 内存 channel（8 条），退出/崩溃即丢 | SQLite `agent_messages` 为准，重启保留未领用输入 |
| 忙时多条 | 每条一个回合 | 10 条合成一个 digest 回合（摘录，不含正文） |
| 送达判定 | 入队即 ack；`--wait` 时“开始回合”即结算 | 展示它们的回合**成功提交**后才 `delivered` |
| 失败后屏障 | 无，排队消息继续起回合 | 有：失败/取消/中断后 pending 等显式输入，成功显式回合再恢复自动投递 |
| 修订/幂等 | 无 | 每条消息有 revision，旧回合不能确认新版本；命令身份去重 |

是否值得改（只建议，不实施）：

1. **失败后重试屏障**（建议先做）：对方回合失败后，队列里的会话消息仍会立刻各起回合，容易变成失败风暴/循环。可在 chat 主循环记“上一回合失败/取消”，失败后把会话消息转为 held（等用户输入），并让发送端得到明确“未处理”信号。改动集中在 `internal/chat`，不动协议。
2. **忙时多条合并为一个回合**（建议第二）：减少互相回复的回合数与配额消耗，需要先定摘要格式与来源标记是否保留全文。
3. **持久化邮箱**（成本最高，最后评估）：跨重启不丢消息，需要新的 entries 类型、GC 与迁移，和 `suga data` 的生命周期一起设计。
4. **回合级回执**（需要协议扩展）：现在是“开始回合即结算”，要做到“回合提交才算送达”，得把 settle 点从 `chat.go` 的取用处移到 `chat_turn.go` 的回合结束处，并把回合结果编码进第二次响应。

### 六、与设计文档的偏差（顺带记录）

- 设计“总体方案”1 里的 `mode?: "follow_up"|"steer"` 已实现为不使用：工具 Schema 没有 `mode`，投递固定 `follow_up`（`native/session_message.go:94-109`、`163-166`）。
- 设计里的 `list` 字段含“标题”，实际列的是短名称、会话 ID、状态、工作目录与是否支持跳数上限（`native/session_message.go:140-148`）。
