<!-- template_id: design; template_version: 1.1.1 -->
# ACP 覆盖补齐设计（session/list、resume、close、set_config_option，load 回放历史）

> 状态：Draft 0.1 / 待用户审批
> 日期：2026-10-20
> 关联：issue `suga-1ojw`；`internal/acpserver/server.go`；acp-go-sdk v0.13.5

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-20 | 监督（Claude） | 用户 2026-10-20 “继续下一批”，ACP 补齐先出设计。核对代码后发现 `session/load` 未按协议回放历史，一并纳入。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：ACP 客户端（gofer 工作台、Zed 等）能列出 suga 会话、无回放地恢复、显式关闭、在会话里切换模型与模式；`session/load` 按协议回放历史。
- allowed_scope：`internal/acpserver`、`internal/personal`（只读接口：会话列表、历史条目到消息的投影）、`scripts/acpprobe`、测试与文档。
- non_goals：session/fork、session/delete（删除走 `suga data gc`）、认证、跨进程共享“总是允许”规则。
- expansion_policy：DEFER_OR_REQUEST。
- review budget：低–中；单包改动，协议字段由 SDK 生成类型约束。
- 停止条件：用户批准前不改代码。

## 背景与目标

`internal/acpserver/server.go:560-570` 的 `CloseSession`、`ListSessions`、`ResumeSession`、`SetSessionConfigOption` 都返回 MethodNotFound；`Initialize` 只声明 `LoadSession`。`LoadSession`（`:176-215`）只登记会话状态，不发送任何 `session/update`，而协议要求 load 把历史以通知形式回放给客户端；所以客户端重新打开一个会话时看到的是空白。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| SDK 已有 `ListSessions/ResumeSession/CloseSession/SetSessionConfigOption` 接口与 `SessionCapabilities{List, Resume, Close}` | acp-go-sdk v0.13.5 `types_gen.go:4420`、`agent_gen.go:317-449` | 只需实现并在 initialize 声明。 |
| `ListSessionsRequest{Cursor, Cwd}` → `{Sessions []SessionInfo{SessionId, Cwd, Title, UpdatedAt}, NextCursor}` | `types_gen.go` | 分页与按 cwd 过滤。 |
| `ResumeSessionResponse` / `NewSessionResponse` 可带 `ConfigOptions`（select/boolean，类别 mode/model/thought_level） | `types_gen.go:4684-4686` | 模型切换用 select 类型、类别 model。 |
| 会话列表：`store.ListSessions` | `internal/personal/store/sessions.go:82` | 有 Title、Cwd、UpdatedAt。 |
| 模型切换：`Runner.SwitchModel` | `internal/personal/runner.go:408` | chat `:model` 已在用。 |
| 会话状态只在进程内（`sessionState`，含 always 规则） | `server.go:71-82` | close 释放它；不持久化 always。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

1. **initialize**：`SessionCapabilities{List, Resume, Close}` 声明为支持。
2. **session/list**：按 `updated_at` 倒序，`cwd` 给出时只列同一目录（`personal.SameDir`）的会话；每页 50 条，`cursor` 为上一页最后一条的 `(updated_at, id)` 编码；只列 ACP、CLI、chat 来源的普通会话，排除 archived 与子 agent 会话。
3. **session/resume**：与 load 相同的检查（目录一致、没有进行中的回合），**不回放历史**；返回 modes 与 configOptions。
4. **session/load 回放**：登记后，把当前分段（压缩摘要 + `first_kept_seq` 之后）的条目按顺序转成 `session/update`：用户消息 → `user_message_chunk`；assistant 正文 → `agent_message_chunk`；工具调用与结果 → `tool_call`（status completed/failed，标题为工具名与参数摘要，结果只给前 2KB 文本）；压缩摘要 → 一条 `agent_thought_chunk`“（之前的对话已压缩：…）”。读取用 `NoHydrate` 列出、只取回当前分段（与上下文组装相同的边界，复用 `loadHistory` 的采用规则，不重复实现）。回放大小上限 2MB，超过时只回放最近部分并在开头说明。
5. **session/close**：若有进行中的回合先取消并等待结束（复用 `Cancel`），然后释放 `sessionState`；会话本身不删除。关闭后的 prompt 返回 invalid params。
6. **session/set_config_option**：在 new/load/resume 的响应里给出两个选项：`model`（select，类别 model，选项为配置里的全部模型，当前值为会话模型）与 `mode`（select，类别 mode，default/read_only，与现有 `SetSessionMode` 同源）。设置 model 时调用 `SwitchModel`（下一回合生效，回合进行中拒绝）；返回最新的全部选项。切到不支持看图的模型时，`promptCapabilities` 不变（initialize 已发出），图片按第一期规则降级。

## 关键流程

见“总体方案”1–6。

## 架构

- `internal/acpserver/session_list.go`（list/close/resume）、`internal/acpserver/replay.go`（load 回放）、`internal/acpserver/config_options.go`（set_config_option）；`server.go` 只保留分派。
- `internal/personal`：导出一个只读的“当前分段条目”接口供回放使用（复用 `loadHistory` 的边界计算）。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | load 回放只回放当前分段 | 与模型看到的内容一致；长会话不会发送几十 MB 通知。 |
| D2 | 模型切换走 configOptions 而不是 SDK 不稳定的 set_model | configOptions 是协议的稳定方式，客户端统一渲染。 |
| D3 | close 不删除会话 | 删除是数据治理范围（`suga data gc`），close 只释放进程内状态。 |

## 安全、数据、运维与回滚

- 只读接口；list 不暴露其他目录的会话内容（只有标题与目录）；回放文本限制 2MB。
- 回滚：去掉 initialize 声明即退回现状（方法仍可保留）。

## 测试与验证

- 单测：list 分页/cwd 过滤/排除 archived；resume 不发通知、目录不符拒绝；load 回放的顺序与类型（用户、assistant、工具、摘要）、超限截断说明、压缩点之前不回放；close 取消进行中的回合、关闭后 prompt 被拒；set_config_option 切模型下一回合生效、回合中拒绝、未知值报错。
- 反向验证：去掉回放后“load 后收到历史通知”用例失败；去掉 cwd 过滤后 list 用例失败。
- 真实验证：`scripts/acpprobe/acp_probe.py` 扩展 list/load/resume/close/set_config_option；用 gofer 工作台重新打开一个已有 suga 会话，确认历史可见（gofer 侧由其开发会话确认）。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | load 回放只回放当前分段（压缩点之后），而不是全部历史？ | 是 |
| Q2 | list 默认只列同目录？（协议 cwd 可选：不传时列全部） | 按协议：传 cwd 才过滤 |
| Q3 | configOptions 暴露 model 与 mode 两项，暂不暴露 thinking 级别？ | 是（thinking 级别以后再加） |
| Q4 | 实施前独立审核？ | 否（单包、协议类型约束；实施后审核） |

## 结论与人工计划 Gate

补齐四个方法并修正 load 回放；用户批准 Q1–Q4 后派出实施，批准前不改代码。
