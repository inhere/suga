<!-- template_id: plan; template_version: 1.2.0 -->
# 会话消息可靠投递（一期：内存语义）实施计划

> 状态：Approved 0.1（2026-10-22 用户：“批准消息投递一期计划, 开始实施”）/ 已实施（36cfa5f3，suga-verify 通过）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-22 | Claude | 初稿：消息 ID 去重、合并投递、失败屏障、处理回执与 `--wait-processed`、退出回执 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

设计工作声明：思考模式 `RIGOROUS`；核心目标是实现已批准设计 0.2 的**一期**（内存语义，不改存储）；scope freeze 为设计“一期”一节与 D1–D4；评审预算：低暴露，一轮合并轴评审（实施后代码审核由监督完成）；停止条件：用户批准本计划，或要求修订。

## 目标与完成定义

目标：会话之间的消息不会被处理两次、处理失败对双方可见，发送方能等到“已处理 / 失败 / 待用户处理”。

完成定义：
- 发送方能为消息指定 ID；同一接收方进程内重复 ID 不再入队；工具结果返回消息 ID。
- 接收方空闲时把排队消息（≤10 条）合并成一个投递回合；每条无条件带来源标签；跳数取最大值，达上限的照旧进 `sessionHeld`。
- 投递回合 completed → 回执“已处理”；failed/cancelled → 回执“失败”，剩余排队消息转“待用户处理”，不自动重试。
- `suga send --wait` 语义不变；新增 `--wait-processed`、`--id`；`session_message` 返回消息 ID 并支持 `status` 查询。
- chat 退出时排队消息回执“未处理（接收方退出）”。
- 单测与伪终端冒烟覆盖以上行为，含反向验证；`suga-verify` workflow 全通过。

## 范围、排除项与授权

范围：suga 仓库 `internal/personal/process`（协议字段）、`internal/chat`（取件、合并、回执、屏障、退出）、`internal/cli/send.go`、`internal/wiring/session_message.go` 与对应工具 schema、文档与 CHANGELOG、`scripts/ptysmoke` 冒烟。

排除：二期持久收件箱与离线入库；ACP 会话收件；`hasSourceTag` 冒充问题（suga-lq5t 另做）；taint 规则变更；gofer 侧任何改动；发版。

授权边界：按功能点本地提交（SR1211）；不 push、不发版（另需授权）。

- `host_or_non_offline_action=NOT_APPLICABLE`

## 输入与批准证据

- 设计：`docs/design/2026-10-22-session-message-delivery-design.md` 0.2，用户 2026-10-22 原话“批准消息设计”。
- 评审：`docs/review/2026-10-22-session-message-delivery-design-review.md`（0.1 BLOCKED，0.2 已处理 F1–F9）。
- 现状核实：`docs/design/2026-10-21-session-message-design.md` 末尾“现状核实（2026-10-22）”。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 消息 ID、处理回执、等待处理完成 | `personalprocess.Request/Response`、`chatInputEvent.settle`、`suga send --wait` | 是 | 原位扩展协议字段与 settle 时机 | DIRECT_REUSE | none | 无新模块；旧客户端忽略新字段 |
| CAP-02 | 合并投递与失败屏障 | `chat.go` 取件分支、`sessionHeld`、`holdSessionMessage` | 是 | 原位扩展 | DIRECT_REUSE | none | 无 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| NONE | none | none |

## 前置检查与 fail-closed 条件

- Workspace baseline（2026-10-22）：Git root `inhere-tools/suga`；branch `main`；HEAD `17690f2c`；`git status --short` 干净（只有本计划与设计状态的未提交文档改动，随本计划提交）。
- 实施在 gofer worktree（`.worktrees/<name>`，分支 `feat/session-message-delivery`）中进行，合并用 `gofer job worktree merge`。
- 依赖：第三十二批的 `invocation_id`（`tools.InvocationID(ctx)`）已在 main。
- fail-closed：协议新字段破坏旧版本收发（新发旧收或旧发新收）的任何测试失败即停；`suga send` 退出码或 `--wait` 现有语义的任何变化即停（Semantic Amendment）。

## 波次与依赖

- 波次 1：T1（协议与 CLI/工具入口）。
- 波次 2：T2（合并与去重）、T3（回执与失败屏障），依赖 T1；二者同在 `internal/chat`，顺序执行。
- 波次 3：T4（退出回执与状态查询），依赖 T3。
- 波次 4：T5（冒烟与文档），依赖 T2–T4；末尾跑 `suga-verify`。

## 任务

### T1 协议与入口：消息 ID、回执状态、`--id`、`--wait-processed`

- 文件: `internal/personal/process/process.go`（`Request.ID string`、`Request.WaitProcessed bool`；`Response.MessageID string`、`Response.State string`，取值 `queued|steered|processed|failed|held|discarded`）、`internal/cli/send.go`（`--id`、`--wait-processed`；不传 `--id` 时生成并在输出打印）、`internal/wiring/session_message.go` 与工具 schema（消息 ID 取 `tools.InvocationID(ctx)`，结果返回 `message_id`）。
- 动作: 加字段与参数；旧字段与现有分支不变；新字段为空时行为逐字节同现状。
- 验证: `go test -count=1 ./internal/personal/process/ ./internal/cli/ -run 'Send|Request'`（运行目录：仓库根）；新增用例：旧客户端请求（无新字段）得到与现状相同的响应。
- 完成标准: 协议往返测试通过；`suga send` 退出码表不变（测试守护）。
- 依赖: 无。

### T2 合并投递与按 ID 去重

- 文件: `internal/chat/chat.go`（两处 `case delivery := <-source.deliveries`）、`internal/chat/chat_input.go`（`DeliverImages` 入队时按 ID 去重，进程内 map）、`internal/chat/session_message.go`。
- 动作: 取到一条后非阻塞再取至多 9 条，合并为一个 `chatInput`：每条以 `[来自 <短名>]` 开头（无条件）；`hops` 取最大值；达上限的消息照旧 `holdSessionMessage`；记录本回合包含的消息 ID 列表供 T3 回执。
- 验证: `go test -count=1 ./internal/chat/ -run 'Delivery|SessionMessage|Merge'`；用例：3 条排队合并成一个回合；重复 ID 只入队一次；跳数取最大；正文以 `[x] ` 开头的消息仍被加标签。
- 完成标准: 用例通过，反向验证（去掉合并 → 合并用例失败）。
- 依赖: T1。

### T3 处理回执与失败屏障

- 文件: `internal/chat/chat_turn.go`（回合结束处）、`internal/chat/chat_input.go`（`settle` 改为两段：取件时 `taken`，回合结束时 `processed|failed`）。
- 动作: 投递回合 completed → 对本批消息回执 `processed`；failed/cancelled → 回执 `failed`，并把仍在 `deliveries` 中的排队消息移入 `sessionHeld` 并回执 `held`；`held` 消息在用户下一回合 completed 后回执 `processed`。不自动重试。`--wait` 仍在取件时返回（现状）；`--wait-processed` 等到终态。
- 验证: `go test -count=1 ./internal/chat/ -run 'Barrier|Receipt'`；用例：成功回执、失败回执、取消回执、屏障转 held、held 在下一回合后 processed、`--wait` 与 `--wait-processed` 的返回时机。
- 完成标准: 用例通过；反向验证（失败后照常继续投递 → 屏障用例失败）。
- 依赖: T2。

### T4 退出回执与状态查询

- 文件: `internal/chat/chat_input.go`（`DiscardQueued` 对排队消息回执 `discarded`）、`internal/wiring/session_message.go`（`action=status`，按 ID 经 socket 查询接收方进程内状态表）、process 协议的状态查询请求类型。
- 动作: 接收方维护进程内 `id → state` 表（上限如 1000 条，按时间淘汰）；`status` 查询返回状态或“未知（接收方已重启或不在线）”。
- 验证: `go test -count=1 ./internal/chat/ ./internal/wiring/ -run 'Discard|Status'`。
- 完成标准: 退出时发送方收到 `discarded`；`status` 查询各状态正确。
- 依赖: T3。

### T5 伪终端冒烟与文档

- 文件: `scripts/ptysmoke/chat_session_message.py`（或新增 `chat_delivery.py`，加入 `make smoke-chat`）、`skills/suga-guide/references/*.md`（会话消息与 `suga send` 说明）、`docs/usage/gofer-personal-agent.md`（gofer 依赖的退出码与 `--wait` 不变的说明）、`CHANGELOG.md`（`## [未发布]`）。
- 动作: 冒烟覆盖两个 chat 会话间：合并投递、失败屏障（假模型让投递回合失败）、`--wait-processed` 返回 processed/failed、退出回执；脚本遵守公共附录（清空工作目录、子会话结果计入）。
- 验证: 容器内 `python3 -I scripts/ptysmoke/<脚本>.py --bin tmp/smoke/suga`；反向验证一项；最后 `gofer wf run --template suga-verify -p my-tools-dev --var commit=<sha>` 全通过（不与容器内其他冒烟并发）。
- 完成标准: 冒烟全过、workflow 全过。
- 依赖: T2–T4。

## 回滚与恢复

- 每个任务独立提交；回滚即 revert 对应提交（无存储与迁移改动）。
- 恢复点：每波次结束的提交；中断后按 progress 的最后通过验证继续。
- 新字段对旧版本透明（JSON 未知字段忽略），新旧混用不需要协调回滚。

## 人工 Gate

1. 本计划批准（当前）。
2. 当前执行请求（批准后由用户发起或明确授权）。
3. 发版与推送另行授权（不在本计划内）。

## 可追溯性

| 设计决策 / 完成定义 | 任务 | 验证 |
|---|---|---|
| D2 至多处理一次、失败可见 | T3 | Barrier/Receipt 用例、冒烟失败屏障 |
| D3 跳数取最大、来源标签无条件 | T2 | Merge 用例 |
| D4 退出码与 `--wait` 不变，新增 `--wait-processed`、`--id` | T1、T3 | 协议往返与退出码守护测试 |
| 消息 ID 与去重 | T1、T2 | 去重用例 |
| 退出可见、状态查询 | T4 | Discard/Status 用例 |
| 端到端 | T5 | 冒烟与 suga-verify |

## 完成 Gate 与剩余工作

完成 Gate：T1–T5 完成标准全部满足、`suga-verify` 全通过、监督审核通过并合入 main。完成时在 progress（gofer plan 备注）写 ≤8 行完成总结。剩余工作：二期（持久收件箱、离线入库）待用户决定；suga-lq5t 来源标签冒充另做。
