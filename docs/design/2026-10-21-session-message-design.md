<!-- template_id: design; template_version: 1.1.1 -->
# 会话之间互发消息设计（不依赖 gofer 的 session_message 工具）

> 状态：Draft 0.1 / 待用户审批
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
| Q1 | 一个工具 `session_message`（list/send 两个动作）？ | 是 |
| Q2 | 默认开启，配置 `tools.session_message.enabled` 与 `SUGA_SESSION_MESSAGE=off` 可关闭？ | 是 |
| Q3 | 连续自动回合跳数上限 3、单回合发送上限 5？ | 是 |
| Q4 | 只覆盖 chat 会话（run/ACP 不登记进程，本期不改）？ | 是 |

## 结论与人工计划 Gate

用 `session_message` 工具复用 send 的投递通道，加来源标记与跳数上限防止循环，可在配置关闭；用户批准 Q1–Q4 后派出实施，批准前不改代码。
