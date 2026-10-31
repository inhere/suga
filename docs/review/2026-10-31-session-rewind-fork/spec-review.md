<!-- template_id: review; template_version: 1.1.0 -->
# suga 会话 rewind 与 fork 设计 可执行轴评审

## 评审目标与范围

- 目标: `docs/design/2026-10-31-session-rewind-fork-design.md`（git root `inhere-tools/suga`，candidate commit `0ae0a20c`）
- 修订版本: 0.1
- 评审者: 可执行轴独立评审子 agent（只读，与规范轴隔离）；axis = Spec/Executability；round 1
- 日期: 2026-10-31
- 排除项: 已确认的取舍 1–4；未运行测试或原型

## 输入与方法

读 `internal/personal` 的 context、segment、segment_clear、segment_handoff、segment_history、session_card、handoff、lastreply、admit、compact、steer、replay、ledger、nudge、undo_options、undo_restore、undo、undo_runner、undo_notice、snapshot_retention、fold、taint、gate、runner、runner_sessions、spill；store 的 types、entries、turns、image_files 与核心迁移；`pkg/agentcore/tools/visibility.go`、`pkg/sugasdk/{engine,types,request}.go`、`internal/chat/{steering,chat_undo}.go`、`test/ptye2e/fakellm_test.go`；多轮 grep entry 读取点与 seq 引用。

## 验收标准

A1 三种 scope 唯一实现且回退后模型上下文无被回退内容；A2 fork 新会话可续聊且等于源会话第 N 轮前可见历史、源不变；A3 R1–R8 有数据来源与实现路径；A4 e2e/单测可覆盖；A5 旧版本回滚披露准确。

## 发现

### F1 [HIGH] fork 未处理 turns 表，分叉会话历史全部丢失（CORE_BLOCKING · MISSING_FUNCTION/BUG）

- 证据: loadHistory 只采纳 `done[e.TurnID]`（`context.go:206-208`），`done` 来自 `ListTurns`；`turns.id` 为全局主键（核心迁移 :15-23）；设计未提 turns 与 entry 的 turn_id。
- 建议: 为复制的轮生成新 turn_id 并写 turns 行，按映射改写 entry TurnID，写明 `last_turn_id`；单测 fork 后 loadHistory 条目数等于源 cut 前可见条目数。

### F2 [HIGH] 漏列会把被回退内容送给模型的读者（CORE_BLOCKING · BUG）

- 证据: `recentRequests`（`segment_handoff.go:111-160`）进入交接包与 compaction 的 `RecentRequests`；`buildHandoff`（`handoff.go:100-128`）的 recentRequests、独立 `LatestEntry(EntryPlan)`、`loadedToolNames`。
- 建议: 读者定义扩为“进入模型请求或交接包的 entries 读取”，逐个列出（含 `nudge.go:221`；说明 `turnSteers`、`markInterruptedToolCalls` 为何不受影响）；单测 rewind 后 compaction 的交接包不含被回退提问。

### F3 [HIGH] R4“快照被清理列为不可恢复”无数据来源（CORE_BLOCKING · MISSING_FUNCTION/DATA）

- 证据: `removeCheckpoint` 整体删除目录不留墓碑（`snapshot_retention.go:620-628`）；`ListCheckpoints` 对无快照轮直接跳过（`undo.go:363-366`）。
- 建议: 以 events 中 `Kind=tool`、`Name∈{file_write,file_edit,file_remove}`（`observe.go:311`，`analyze.go:207` 同类用法）判断“有写调用但无快照”即已清理，路径取自事件属性；快照整体关闭时单独说明。

### F4 [MEDIUM] visibleEntries 形态与语义要定死（限量查询、Types 过滤、区间并集）（CORE_CORRECTIVE）
### F5 [MEDIUM] 其他读者遗漏或需取舍（LastReply、last_turn_id、session card 口径、session_search/recall、sugasdk.Entries 返回新类型）（CORE_CORRECTIVE）
### F6 [MEDIUM] cut_seq 与用户轮序号定义（第 N 轮首条 entry seq−1；用户轮以 `suga.injected` 元数据判定）（CORE_CORRECTIVE）
### F7 [MEDIUM] 文件回退实现路径（gate 不可重入→不加锁的选择执行函数、OnFilesRestored、undone/partial 处理、级联冲突、失败边界）（CORE_CORRECTIVE）
### F8 [MEDIUM] meta 与进程内状态重置（loaded_tools 不重算、fold 两字段、prefix 缓存、待注入任务通知、undo.pending）（CORE_CORRECTIVE）
### F9 [MEDIUM] fork 其余细节（cwd 与重复 start 段记录、first_kept_seq 映射规则、图片与 spill 与 Refs、单事务与失败清理、taint 用 created_at 比较、回合中行为统一）（CORE_CORRECTIVE）
### F10 [LOW] 旧版本下的 compaction 会把屏蔽区间永久写进摘要（CORE_CORRECTIVE）
### F11 [LOW] 可测性：fakeLLM 需记录完整消息列表与按轮写入不同内容，点名单测（CORE_CORRECTIVE）

已核实正确：屏蔽后状态机（segmentSeq、firstKept、summary、updates、context_update、interrupted 轮、steer 与 undo 通知随轮屏蔽）。

延后与范围外：D1 transcripts 无回退标记；D2 sugasdk RequestID 去重可能重放被屏蔽轮输出；D3 fork 条目进 FTS 造成跨会话重复命中；O1 redo、summarize、CLI、ACP。

## 覆盖缺口与限制

静态读码；compact summarize 输入只确认经 loadHistory；recovery、cachebucket、memory 只 grep；未核查 xhen 侧；`Hops>0` 的轮是否带 Injected 未确认。

## 结论

BLOCKED（F1、F2、F3）。F4–F11 CORE_CORRECTIVE。三项阻断可在设计层修补，不推翻 R1/R2。

## 剩余风险与后续

修订 0.2 补齐 fork 的 turn 映射、完整读者清单、快照清理检测来源、屏蔽区间 API、文件回退失败语义与不可重入锁下的函数划分；修订后对 F1–F3 复审。实施风险：session card 性能约束、进程内 Visibility 与通知队列一致性、F10 回滚泄漏。
