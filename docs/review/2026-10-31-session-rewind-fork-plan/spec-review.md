<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审（Round 2）

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，计划 commit `3618827e`）
- 修订版本：Draft 0.2
- 依据：设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准）；本目录第 1 轮可执行轴报告（commit `a59b27d0`，F1–F14）
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，未读 standards-review.md
- 轮次：**Round 2**（confirmation）
- 日期：2026-10-31
- **结论：PASS**（无开放 CORE_BLOCKING；F15–F19 为 CORE_CORRECTIVE，建议在 0.3 或对应任务书中落实后再交用户批准）

## 输入与方法

在 HEAD `3618827e` 上静态读码，没有运行测试，也没有运行 `EXPLAIN QUERY PLAN`。读过的代码：

- undo 与锁：`undo.go`（UndoCore、checkpointsLocked、withSession、ListCheckpoints）、`undo_runner.go`、`undo_restore.go`（Undo、restoreItem）、`undo_options.go`、`undo_notice.go`、`gate.go`。
- 读者：`context.go`（loadHistory、BuildMessages）、`segment_clear.go`、`segment.go`（segmentMeta、resolveSegment）、`segment_handoff.go`、`handoff.go`、`lastreply.go`、`session_card.go`、`ledger.go`（Search、readMessages）、`tasks.go`、`tasks_test.go`。
- store：`entries.go`、`turns.go`、`sessions.go`、`fts.go`、`fts_snapshot.go`、`fts_cursor.go`、`toolbody.go`、`tx.go`、`migrations/*`。
- 其他：`pkg/agentcore/tasks/tasks.go`（List、TakeNotices、Running、HasNotices）、`internal/chat/chat.go:340-360,730-800`、`chat_undo.go`、`internal/wiring/runtime.go:680-686`、`pkg/sugasdk/session.go:464`、`xhen/internal/link/anchor.go`。
- 另 grep 了 session meta 键的定义，以及 TaskNotices 的全部实现与 `RecentEntries`/`LatestEntry`/`CountEntriesByType` 的全部调用方。

## 第 1 轮发现关闭状态

| 项 | 级别 | 0.2 落点 | 状态 | 说明 |
|---|---|---|---|---|
| F1 预览/确认/执行两段式 | CORE_BLOCKING | 跨任务接口、T4、T5、T7 | **CLOSED** | `RewindPlan`/`ApplyRewind` 两段式，参数含 snapshots 与 sessionsRoot，T5 依赖 T3、T4，T7 先预览再确认。遗留：执行时用的是预览结果还是重新计算的结果，以及 Stamp 覆盖面，转 F15 |
| F2 fork seq 映射与单事务 | CORE_BLOCKING | T1、T6 | **CLOSED** | 映射在事务前算好；clear 的 K 映射到它自身的新 seq；rewind 记录不复制；meta 白名单含 model、provider、loaded_tools、`:rw`。核对：K 映射到“不小于 K 的第一条已复制条目”时总能取到值，因为 compaction 记录自身 seq ≥ K 且会被复制，不需要兜底。白名单默认拒绝其余键的细节见 F22 |
| F3 复用 AppendEntries 事务内部分 | CORE_CORRECTIVE | T1 | CLOSED | 抽出共用函数，保留 `created_at`，验证 FTS 能检索、失败无残留。“大正文 fork 后仍为 stub 且可 Hydrate”没有列为用例（ADVISORY，见 F24） |
| F4 recall readMessages | CORE_CORRECTIVE | T2 | **PARTIAL** | 已覆盖跳过屏蔽条目、ref 在区间内的情况；缺 store 读法与续读 ref 规则，见 F18 |
| F5 跨会话排除机制、索引 | CORE_CORRECTIVE | T1、T2 | CLOSED | R11 收窄为“排除当前会话”，与设计 R11 原文一致；`ExcludeSession` 进入 fingerprint；有 EXPLAIN Gate。遗留的性能问题见 F19 |
| F6 card 计数方法与基准 | CORE_CORRECTIVE | T1、T2 | **PARTIAL** | 已新增 `CountEntriesByTypeSkipping`、`CountTurnsSkipping`；基准变体只写了“3 个屏蔽区间”，没有要求大区间，见 F19；LatestSummary 漏改，见 F17 |
| F7 checkpointsLocked 取行 | CORE_CORRECTIVE | T4 | CLOSED | 有 RootStatus 反向验证 |
| F8 后台任务判定与竞争 | CORE_CORRECTIVE | T3、T5 | **PARTIAL** | 运行中任务的判定可行（用 `List` 按 Status 过滤）；“预览列出将丢弃的通知”用 List 做不到，见 F16；互斥理由引用不完整，见 F21 |
| F9 屏蔽集合归属与依赖 | CORE_CORRECTIVE | 波次、T2、T3 | CLOSED | masked 归 T2，T3 依赖 T2，前置拒绝放在 T3 由 T5、T6 共用 |
| F10 不受影响读者清单 | ADVISORY | T2 | CLOSED | 已补全；但 SessionCard 的唯一调用方是 sugasdk，见 F17 |
| F11 LastReply 误报 | ADVISORY | T2 | CLOSED | — |
| F12 清理检测细节 | ADVISORY | T4 | CLOSED | — |
| F13 测试判据与夹具 | ADVISORY | T0、T6、T8 | PARTIAL | T0 按内容写入、T6 的对照基准已落实；T8 fork e2e 的基准和 rewind entry 的 TurnID 仍未写，见 F24 |
| F14 文件归属与规模 | ADVISORY | T3、T4、T5 | CLOSED | 拆成 `rewind_target.go`、`rewind_files.go`、`rewind.go`；T5 已列 `model.ini` |

## 新发现

### F15 [CORE_CORRECTIVE] ApplyRewind 执行的是预览结果而不是锁内重算的结果；Stamp 覆盖不全

- 证据：
  - T4 的签名是 `applyRewindFiles(ctx, plan)`；T5 执行第 2 步“按 scope 调用 applyRewindFiles”，执行前只“重算 Stamp”。按这个写法，执行用的是预览阶段（锁外）算出的 `CheckpointItem`，包括其中的 `RootStatus`。
  - 现有 undo 不是这样做的：`UndoCore.Undo`（`undo_restore.go:53` 起）在 `withSession` 内重新调用 `checkpointsLocked` 再执行；chat 预览用的 rows（`chat_undo.go:144`）只用来展示（`:174` BuildUndoResult）。
  - `restoreItem` 只在 `RootStatus == SnapshotRootTrusted` 时写盘（`undo_restore.go:147`）。RootStatus 由 `annotateRoots` 根据会话 cwd、`add_dirs` 与事件计算（`undo.go:229` 起）。Stamp 只包含“最新 seq 与各 checkpoint 状态”，不包含 cwd、`add_dirs`、快照目录是否还在（保留策略的 `removeCheckpoint`），也不包含待注入的任务通知和 `suga.undo.pending` 队列。预览之后发生的这些变化都检测不到，可能写入已不受信任的根目录（SR1301），也可能丢弃预览里没有列出的通知。
- 建议：
  - `ApplyRewind` 在 gate 与跨进程锁内重新执行 `planRewindFiles`，并重新收集通知与 undo 队列，得到新计划；只执行新计划。
  - Stamp 定义为“新计划中确定性内容的摘要”：每项的 turn、path、action、RootStatus、Reason、checkpoint state，加上最新 seq、通知 ID 列表、undo 队列长度。与预览不一致时返回 `ErrRewindStale`。
  - 把 `applyRewindFiles` 的入参写成“锁内重算的计划”。在 T5 加一个用例：预览后修改 `add_dirs`（或删掉快照目录），执行必须返回 stale，或者跳过该项。

### F16 [CORE_CORRECTIVE] 用 `TaskNotices.List` 无法得到“将丢弃的已完成任务通知”

- 证据：`Manager.List`（`pkg/agentcore/tasks/tasks.go:559-574`）返回会话的全部任务快照，其中也有通知已经送达的任务。待注入状态只存在于 `s.notices` 和 `task.seen` 中，`Info` 没有对应字段（`:137-162`）。对外只有 `TakeNotices`（会取出）和 `HasNotices`（只返回 bool，`:752`）。所以 T5 写的“用 List 收集将丢弃的已完成任务通知（只读，不取出）”无法实现：列出全部已结束任务会多报，只列 running 又是另一回事。scope freeze 不包含 `pkg/agentcore/**`，不能直接给 Manager 增加 peek 方法。
- 建议：二选一，写进计划。
  - (a) 把 `pkg/agentcore/tasks` 加入 scope，新增只读的 `PendingNotices(sessionID) []Info`，`TaskNotices` 接口改为加这个方法，而不是加 `List`。running 的判定仍可以用现有的 `Running`。
  - (b) 预览只显示“有 N 条待注入通知将被丢弃”（新增 `HasNotices` 也要改接口），执行后报告 `TakeNotices` 实际丢弃的条目，并说明这偏离了设计流程 1 第 2 步。
- 无论选哪种，`tasks_test.go:29` 的 `fakeNotices` 都要补上新方法。实现方只有它和 `*tasks.Manager`（`wiring/runtime.go:91,680-686`、`cli/chat.go:121`、`cli/acp.go:75`）。

### F17 [CORE_CORRECTIVE] session card 的 LatestSummary 没有接入屏蔽；`RecentEntriesSkipping` 没有调用方；card 的唯一调用方是 sugasdk

- 证据：
  - 设计读者表要求 session card 的 `RecentEntries`、`LatestEntry`、`CountEntriesByType` 都经过屏蔽，涉及 Turns、OpenSteps、LatestSummary、SegmentCount 四个字段。T2 只改了 fillCardPlan、SegmentCount、Turns 三项，`session_card.go:117` 的 `RecentEntries(handoff, compaction)`（即 LatestSummary）没有改。
  - 全仓只有这一处调用 `RecentEntries`。这说明 T1 新增的 `RecentEntriesSkipping` 本来就是给它用的，但 T2 漏写了。回退到 compaction 之前以后，card 仍会显示被屏蔽段的摘要。
  - `Runner.SessionCard` 在仓库内的唯一调用方是 `pkg/sugasdk/session.go:464`。card 改为屏蔽后，sugasdk 的可见行为也会变。这与计划目标 6 和 scope freeze 中“sugasdk 行为不变”的说法冲突；T8 的 CHANGELOG 只提到 `Entries` 返回新类型。
- 建议：T2 的 card 改为四个字段，其中 LatestSummary 用 `RecentEntriesSkipping`；用例与反向验证覆盖“回退早于带摘要的 compaction”。在范围说明和 CHANGELOG 中写明：sugasdk 的 SessionCard 按设计 R2 反映 rewind；“sugasdk 不变”只限 `Entries` 与检索。

### F18 [CORE_CORRECTIVE] recall 的屏蔽缺 store 读法，续读 ref 可能落进屏蔽区间

- 证据：
  - `readMessages`（`ledger.go:249` 起）用 `ListEntries(SinceSeq: seq-1, Types, Limit: offset+limit+1)` 读取窗口，再用 `len(entries) > limit` 判断是否还有后续，并给出续读 ref：最后一条的 seq+1。
  - 在 Go 侧读完后再过滤，窗口会变短，“还有后续”的判断也会出错。“跨任务接口”里没有带 skip 的 ListEntries（`EntryFilter` 没有区间字段），按计划规定，改形状要回到计划修订。
  - 如果窗口刚好结束在 cut 处，续读 ref = last.Seq+1 会落进屏蔽区间。按 T2 的规则“ref 在区间内按不存在处理”，模型照着提示续读会直接报错。第 1 轮 F4 已要求续读 ref 不能落在屏蔽区间，0.2 删掉了这一条。
- 建议：
  - T1 给 `EntryFilter` 加 `Skip SeqRanges`，在 SQL 中用与 `RecentEntriesSkipping` 相同的谓词，并写进跨任务接口；或者明确允许 Go 侧循环补足，并写明上限。
  - 续读 ref 取“下一条可见条目的 seq”。
  - 区分两种 ref：模型传入的 ref 落在区间内，按不存在处理；系统给出的续读 ref 永远不会落在区间内。
  - 增加两个用例：“窗口末尾正好是 cut”和“续读跨过区间”。

### F19 [CORE_CORRECTIVE] 区间读取的代价与屏蔽区间大小、会话长度成正比，EXPLAIN Gate 与基准变体都检测不到

- 证据：
  - entries 是 rowid 表，主键是 `(session_id, seq)`，type 不在主键索引里（`migrations/20261008-230000-create-core-store.sql:26-35`）。T1 规定 `RewindRecords` 走“主键扫描并过滤 type”，因此每次调用都要回表读取该会话的每一行。T2 让每个读者先调用它：loadHistory 每轮一次，card 一次，每次检索一次，每次 recall 一次。BenchmarkSessionCard 的会话有 2 万行，card 原本已有一次同样代价的扫描（`CountEntriesByType`），现在还要加上 `RewindRecords` 和 `CountEntriesByTypeSkipping`。
  - `RecentEntriesSkipping`、`LatestEntrySkipping` 用 `NOT (seq > ? AND seq < ?)` 加 `ORDER BY seq DESC LIMIT`。SQL 能写，EXPLAIN 结果仍然是 `SEARCH entries USING PRIMARY KEY (session_id=?)`，所以能通过“无全表扫描”Gate。但回退通常屏蔽的是会话尾部，倒序扫描要先逐行走完整个屏蔽区间，才能遇到可见行。
  - 基准变体只写了“3 个屏蔽区间”，没有规定区间大小，测不到这种情况。第 1 轮 F6 要求的是“1 个大屏蔽区间”。
- 建议：
  - `RewindRecords` 改用 `INDEXED BY idx_entries_type`（`'rewind'` 类型极少，代价约等于全库 rewind 行数），或者先用 EXPLAIN 与基准比较两种做法再定。计划不要事先把它锁定在主键扫描。
  - Skipping 方法按可见区间由新到旧分段查询，每段都是主键范围加 `LIMIT 剩余数量`，以免逐行走过屏蔽区间。
  - 基准变体规定为“尾部 1 个约 1 万行的屏蔽区间，加 2 个小区间”，纳入 20% Gate。

### F20 [ADVISORY] `UndoCore.RewindPlan` 的职责与 Runner 版本重叠，也拿不到 Runner 依赖

`UndoCore` 只有 Repository、Snapshots、SessionsRoot、LockRoot、Gate、HeldLock 这些字段（`undo.go:109-116`），没有 `cfg.Tasks`，也无法判断是否在回合中。所以计划要求它产出的 `DropTasks` 和前置拒绝，只能在 `Runner.RewindPlan` 中完成。建议把接口中的 `UndoCore.RewindPlan` 删掉，或者改名为只负责文件部分的 `planRewindFiles`（T4 已经有这个方法）。同时写明 `userTurns`、`resolveTarget`、`rewindPreconditions` 的接收者是 Runner。

### F21 [ADVISORY] 前置拒绝“回合进行中”的机制，以及通知互斥的论断

- `sessionGate.Acquire` 遇到占用会**等待**，不会拒绝；遇到可抢占的持有者（空闲整理）会请它让出（`gate.go:104-130`）。Runner 层的“回合进行中”要明确用哪种判定：`TryAcquire` 失败时返回 `ErrSessionBusy`，或者 `Status == SessionRunning`（`runner.go:531`）。后者要说明进程崩溃后残留的 running 状态怎么处理。也可以写明：在 chat 中由 steering 放回，Runner 层只等待。
- T5 写“gate 内与 `takeTaskNotices`（`context.go:355`）互斥，没有竞争”，这个结论成立，但理由只讲了一半。chat 空闲时还会在 gate 外取通知：`chat.go:349` 的 `taskNotice` 回调在 `chat.go:747,791` 被调用。这条路径与 `:rewind` 不冲突，是因为二者都在 chat 输入主循环中顺序执行，确认提问期间也不会处理 taskWake。建议在 T5 注释中写明这个依据。

### F22 [ADVISORY] fork 的 meta 白名单应明确默认拒绝，并逐项列出真实的键

grep 到的会话 meta 键中，计划没有逐项给出处理方式的有：

- `run_mode`（`turncontext.go:30`，每轮会按 cfg 重写）；
- `short_name`（`metaShortName`，名字应唯一，不应复制）；
- `suga.undo.pending`（`undo_notice.go:22`，源会话的待注入 undo 事实不应带进 fork）；
- `MetaUsedInteractively`；
- `cache_prefix_print`（`prefix.go:42`，属于“前缀缓存键”）；
- `personal_rules_*` 与 `memory_index_*`（`segment.go:232-246`，只是缓存，以段记录为准，不复制没有问题）。

建议在 `fork.go` 注释中写明“除白名单外一律不复制”，并在测试中断言 `suga.undo.pending`、`short_name`、pin、子会话键都不存在。

### F23 [ADVISORY] undo 待注入通知的合并方式引用有误；合并后 event_id 回填会丢失

- T5 写的“见 `xhen/internal/link/anchor.go` 的合并方式”：这个文件只处理锚点与 entry 探测，没有任何 undo 或通知合并逻辑，引用应删除。
- 现有队列每轮只取第一条（`takeUndoNotice`，`undo_notice.go:119-139`）。每条通知带一个 `EventID`，注入时用它回填对应 undo 事件的 `entry_seq`（`:181-184`）。把多条通知合并成一条后，只能保留一个 EventID。建议给 `pendingUndoNotice` 加上 `EventIDs`，或者明确接受只回填一个，并写进 T5 用例。

### F24 [ADVISORY] 测试判据补充

- 写明 `rewind` entry 的 `TurnID` 为空（第 1 轮 F13 遗留）。
- T8 中 fork e2e 的判据“新会话请求等于第 N 轮之前的可见历史”，仍未写明对照基准，也没有说明段上下文中随时间变化的部分（环境、日期）如何处理。建议沿用 T6 的做法：以源会话第 N 轮请求去掉最后一条 user 消息作为基准，按 role 与内容逐条比较，忽略段上下文消息。
- T1 或 T6 补一个用例：大正文 fork 后在 DB 中为 stub，且 Hydrate 后内容完整（第 1 轮 F3）。
- 补充反向验证：去掉 card LatestSummary 的屏蔽，以及不传 `ExcludeSession`。
- T2 依赖 T0 的多轮夹具，但依赖列只写了 T1。波次是顺序执行的，实际不受影响，建议在依赖列中写明。
- 前置检查中的 baseline HEAD 仍写 `a59b27d0`，应更新为计划批准时的 HEAD。

## 并行与依赖核对

- W1 的 T2 与 T4：文件不相交，T2 改 visible.go 与读者文件，T4 只改新文件 `rewind_files.go`。“T4 不需要屏蔽集合”成立：文件回退本来就要处理被屏蔽的轮；`ListCheckpoints` 按 ListTurns 从新到旧遍历（`undo.go:347-376`）；清理检测要补上“没有快照也没有 shell 记录”的轮，这些轮会被 `ListCheckpoints` 跳过（`:365`），只能按 ListTurns 与 ListEvents 单独遍历。T4 的写法“用 ListEvents 查……”可以做到，建议在注释中点明这一点。
- W3 的 T5 与 T6：文件不相交，T5 改 rewind.go、rewind_notice.go、undo_runner.go、model.ini，T6 改 fork.go、fork_files.go。二者通过 T3 的 `rewindPreconditions` 和 `withSession` 共享逻辑，不需要同时修改同一个文件。注意：T6 的标题后缀“（分叉）”如果走 i18n，要改 catalog，这不在 T6 的文件清单内，要么写死，要么列入清单。
- `CreateForkedSession` 预先给出 seq 与现有分配方式不冲突：新会话在同一事务内用 `COALESCE(MAX(seq),0)+1`（`entries.go:63-66`）得到的就是 1..n，共用函数返回 seq 后与预先给出的值比对即可。共用函数还要支持按条目设置 `CreatedAt`（`NewEntry` 加字段）；事务开头的 `UPDATE sessions` 需要先在同一事务内插入 session 行。`openToolBodyWriter` 会自己建目录（`toolbody.go:276-284`），失败回滚后留下的空文件由 `DeleteSessionFiles` 清理。
- `TaskNotices` 加方法后，需要同步的实现只有 `*tasks.Manager` 和 `tasks_test.go` 的 `fakeNotices`。wiring 不用改，因为它传的是 `*tasks.Manager`。
- LastReply：在 `ListEntries(assistant, NoHydrate)` 后按 Hidden 过滤；`turnUnfinished` 要忽略落在屏蔽区间内的 `LastTurnID`。可以实现。`subagent.go:157` 也调用它，但子会话不会被 rewind，不受影响。
- `SearchEntriesPage` 的 `ExcludeSession`：在 SQL 中加 `NOT (f.session_id = ? AND (区间谓词))` 即可，选项写入 `fingerprintEntrySearch`（`fts_cursor.go:132-156`）后，rewind 前后的游标不会混用。可以实现。

## 结论

**PASS**（可执行轴，Round 2）。

- 第 1 轮的两项 CORE_BLOCKING（F1、F2）已真正关闭：预览加执行两段式，接口有归属；fork 的映射在事务前算好，在单个事务内写入。
- 新增 CORE_CORRECTIVE F15–F19：执行用锁内重算的计划并扩大 Stamp 覆盖面；DropTasks 的数据来源；card 的 LatestSummary 与 sugasdk 说明；recall 的续读 ref 与 store 读法；区间读取的性能与基准变体。这些都不需要改设计，建议在 0.3 中逐条写入，或在对应任务书中写明后再交用户批准。
- ADVISORY F20–F24。

## 剩余风险与后续

- F19 的查询计划与基准判断只来自读码，要在 T1 实测。
- fork 的附件与 spill 改写没有逐行核对格式。
- Windows ConPTY 下 e2e 的稳定性仍是实施风险。
- 修订 0.3 后，如果只是纳入以上纠正项，不需要再开一轮。主会话按本报告逐条核对 diff 即可。
