<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 设计 可执行轴评审

## 评审目标与范围

- 目标: `docs/design/2026-10-31-session-rewind-fork-design.md`（git root `inhere-tools/suga`，candidate commit `b7b33526`）
- 修订版本: 0.2
- 评审者: 可执行轴独立评审子 agent（只读，与规范轴隔离）；axis = Spec/Executability；round 2（confirmation）
- 日期: 2026-10-31
- 排除项: 未运行测试或原型
- 第 1 轮报告见本文件 git 历史（提交 `b71dd04e`）

## 输入与方法

读 `git diff 0ae0a20c..b7b33526` 与 0.2 全文；回到代码核实：`store/turns.go` CreateTurn 与 ListTurns、`store/sessions.go:151-161` UpdateSessionFields、`idx_entries_type`、`store/tx.go`、`observe.go:311-433` 工具事件属性、`store/gc.go:222,304`、`pkg/agentcore/tasks/tasks.go:686` TakeNotices、`ledger.go:100-135`、`xhen/internal/link/anchor.go:41`。

## 验收标准

A1–A5（同第 1 轮），以及修订不引入新的可执行问题。

## 发现

### 第 1 轮闭合情况

F1 CLOSED（新 turn_id、CreateTurn 复制状态与时间、映射 TurnID、last_turn_id 可写）。F2 CLOSED（读者清单完整，未发现其他进入模型或交接包的读取）。F3 CLOSED（写工具事件必记、只随会话删除、`attrs["target"]` 含路径；细节见 N2）。F4 CLOSED（区间集合、并集语义、Types 白名单；游标见 N5）。F5 CLOSED（细节见 N3）。F6 CLOSED。F7 CLOSED。F8 CLOSED（meta 重置失败无害；TakeNotices 可用）。F9 CLOSED（细节见 N1）。F10 CLOSED。F11 OPEN（CORE_CORRECTIVE）：正文未写 fakeLLM 记录完整消息、按轮写入不同内容与点名单测，需在实施计划补齐。

### N1 [MEDIUM] fork“单个事务”用现有 API 无法实现（CORE_CORRECTIVE）

- 证据: CreateSession、CreateTurn、AppendEntries 各自独立，`beginWriteTx` 私有；附件复制不在 DB 事务中；`insertSession` 必写 start 段记录（`runner_sessions.go:181`）。
- 建议: 新增 store 方法一次写入 session+turns+entries，或改为“失败即 DeleteSession+DeleteSessionFiles”保证；fork 不经 insertSession。

### N2 [MEDIUM] 快照已清理检测须只统计 Status=ok 的写事件；target 为原始路径，file_remove 可为目录（CORE_CORRECTIVE）
### N3 [LOW] session card 的 Turns 来自 CountTurns（turns 表），屏蔽区间不影响，需写明口径或扣减（CORE_CORRECTIVE）
### N4 [LOW] 只回会话时丢弃 `suga.undo.pending` 会丢失 cut 之前 undo 的事实，应合并两段通知；引用改为 `xhen/internal/link/anchor.go`（CORE_CORRECTIVE）
### N5 [MEDIUM] 限量查询需新增 store 游标方法（如 RecentEntriesBefore）与 FTS 排除方式，并保持 BenchmarkSessionCard（CORE_CORRECTIVE）

## 覆盖缺口与限制

静态读码；未逐行核对 SearchEntriesPage SQL；`Hops>0` 是否为用户轮未确认；只核 xhen anchor 路径。

## 结论

PASS（可执行轴，round 2）。F1–F3 闭合，无新增 CORE_BLOCKING；F11 与 N1–N5 为 CORE_CORRECTIVE，在实施计划落实。

## 剩余风险与后续

实施风险：session card 补足后的性能、FTS 排除后的分页正确性、fork 失败清理路径（需专门单测）；R11、R12 需用户批准时确认。
