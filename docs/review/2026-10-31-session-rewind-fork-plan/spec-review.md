<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，commit `bae17865`）
- 修订版本：Draft 0.1
- 依据：设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准）；设计评审 `docs/review/2026-10-31-session-rewind-fork/spec-review.md` 第 2 轮遗留项（F11、N1–N5）
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，未读 standards-review.md
- 轮次：**Round 1**
- 日期：2026-10-31
- **结论：BLOCKED**（2 项 CORE_BLOCKING）

## 输入与方法

静态读码（HEAD `bae17865`），逐个核对计划“预期符号”与任务动作：`context.go` loadHistory、`segment_handoff.go`、`handoff.go`、`lastreply.go`、`session_card.go`（含 BenchmarkSessionCard）、`ledger.go`、`undo.go`/`undo_restore.go`/`undo_options.go`/`undo_runner.go`/`undo_notice.go`、`gate.go`、`segment_clear.go`、`snapshot_retention.go`、`observe.go`、`runner_sessions.go`、`tasks.go`、`store/entries.go`、`store/fts.go`、`store/fts_snapshot.go`、`store/fts_cursor.go`、`store/turns.go`、`store/sessions.go`、`store/tx.go`、`store/migrations/*`、`test/ptye2e/fakellm_test.go`、`internal/chat/chat_undo.go`、`steering.go`。另 grep 了 `internal/`、`pkg/` 中全部 entries 读取调用（ListEntries/RecentEntries/LatestEntry/CountEntriesByType/SearchEntriesPage/SearchEntries/EntriesAround/HasSearchableEntries/LastEntryAt/ListEntryRefs）。未运行测试。

## 验收标准

- A1 每个任务动作在当前代码上可落地（签名、锁、事务能力相符）；
- A2 设计“必须经过屏蔽的读者”被完整覆盖，且有对应测试；
- A3 fork 的 turn/seq 映射、附件、taint 与原子性可落地；
- A4 测试与反向验证能证明 suga-4tnd 验收；
- A5 任务依赖与文件归属无冲突；性能约束可守住。

## 发现

### F1 [CORE_BLOCKING] 设计流程 1 的“先出回退计划、确认、再执行”没有归属任务；T4 与 T6 的编排依赖缺失

- 证据：
  - 设计“关键流程 1”第 2–3 步要求先生成并打印回退计划（屏蔽轮数、丢弃通知、恢复/删除/冲突/级联冲突/不可恢复文件），确认后才执行。计划只有执行接口 `Runner.Rewind(ctx, sid, target, scope)`（T4），没有任何任务产出“只读预览”接口；T8 只写“确认交互复用 chat.undo_confirm”。
  - 现有 undo 是两段式：`chat_undo.go:107` `SelectUndoCheckpoints`（取锁→出计划→放锁）→ 确认 → `chat_undo.go:170` `Undo`（再取锁执行）。rewind 也需要同样的 plan/execute 两段（并说明两段之间状态变化的处理，如执行时重新计算并在差异时报告）。
  - T4 的验证要覆盖“四种失败分支”，其中“文件部分返回 error”与“出现 Failures/Skipped/Unrecoverable”都依赖 T6 的 `rewindFiles`，但波次表写 T4 只依赖 T2、T3，且与 T6 并行。Rewind 对 files/both scope 的编排无处落地。
  - `Runner.Rewind` 签名缺少快照存储：`restoreItem` 是 `*UndoCore` 方法（`undo_restore.go:143`），UndoCore 由 `r.UndoCore(snapshots, sessionsRoot)` 构造（`undo_runner.go:8`），snapshots 由 chat 按配置创建（`chat_undo.go:85`）。Runner 自身没有 snapshots。
- 建议：新增（或在 T4 内明确）`Runner.RewindPlan(ctx, snapshots, sessionsRoot, sid, target, scope)` 只读预览与 `Runner.Rewind(ctx, snapshots, sessionsRoot, sid, target, scope)` 执行两个接口，T6 同时提供“选择/预览”与“执行”两个不加锁函数；把 T4 依赖改为 T2、T3、T6（或把编排与失败分支测试移到 T6 之后的独立任务）；T8 依赖预览接口。

### F2 [CORE_BLOCKING] fork 的 seq 映射与 first_kept_seq 重写在事务之外；rewind 记录与 meta 复制规则缺失

- 证据：
  - T7 写“调用 `CreateForkedSession` 在一个事务内写入；按返回的 seq 映射，重写 compaction 的 `first_kept_seq`”。按此顺序，重写发生在事务提交之后，需要再一次 UPDATE，破坏设计 R6 / F-N1 的“单事务完成”；重写失败时 fork 会话的 `first_kept_seq` 仍指向源会话的 seq，loadHistory（`context.go:179-185,193`）按错误边界截取历史。此外 clear 记录的 `FirstKeptSeq = lastSeq+1` 等于其自身 seq（`segment_clear.go:104-106`），指向的是“记录之后”的条目，必须先知道全部映射再写记录。
  - 新会话的 seq 从 1 连续分配（`entries.go:63-66` `COALESCE(MAX(seq),0)+1`），映射可以在写入前确定性算出（第 i 条 → i），因此可以在事务内一次写对。
  - 计划没有说明源会话中**未被屏蔽**的 `rewind` 记录如何处理。若原样复制，其 `cut_seq` 仍是源会话 seq 空间，会在 fork 中屏蔽错误区间；因为被屏蔽的条目本就不复制，rewind 记录应当不复制（或必须重映射）。
  - meta 复制只列了 cwd、`:rw`、title、origin、forked_from、forked_at_turn。`insertSession`（`runner_sessions.go:154-170`）会写 segmentMeta、`model`、`provider`；handoff 会继承 `loaded_tools`（`handoff.go:105,154`）。fork 历史中含有已加载的延迟工具调用，若不复制 `loaded_tools`，续聊请求的工具集与历史不一致。
- 建议：T1 的 `CreateForkedSession` 在事务内按顺序插入，first_kept_seq 映射在 Go 侧预先计算（“不小于 K 的第一条已复制条目”的新序号；没有时取“最后新 seq+1”），写入前完成 JSON 改写，事务内断言分配到的 seq 与预算一致；T7 明确“不复制 rewind 记录”；列出 fork 的 meta 复制白名单（至少 `model`、`provider`、`loaded_tools`、`add_dirs`、segment 缓存键，fold 两键置零），并为 clear 记录（K 等于自身 seq）与 K 落在已屏蔽区间两种情况加单测。

### F3 [CORE_CORRECTIVE] `CreateForkedSession` 未要求复用 AppendEntries 的正文外置、FTS 写入与时间保留

- 证据：AppendEntries 在同一事务内执行 `ftsInsertEntrySQL`（`entries.go:71-73`），大正文移到会话 body 文件并换成 stub（`entries.go:76-125`）。FTS 没有插入触发器（`migrations/20261010-234359-...sql:23` 只有删除会话触发器）。T7 先 `HydrateEntries` 取全文，若新方法直接 INSERT，fork 的条目不可检索，大正文全部内联进 DB。AppendEntries 把 `created_at` 统一设为 now（`entries.go:64`），按原样复用会改变 fork 条目的时间（影响 `Since/Until` 检索、card 的 `SummaryAt`、trace）。
- 建议：T1 把 AppendEntries 的循环体抽成 `appendEntriesTx(tx, sid, entries)` 供两者共用（含 body writer 的回滚），`NewEntry` 增加可选 `CreatedAt`；验证中加入“fork 后 session_search 能命中复制条目”“大正文 fork 后为 stub 且 Hydrate 可读”。

### F4 [CORE_CORRECTIVE] 设计读者表中的 recall（`ledger.go:262` readMessages）没有落到 T3 动作与测试

- 证据：设计表“模型工具 session_search/recall 读当前会话 | `ledger.go:124,262`”。`:262` 是 `readMessages`：`ListEntries(SinceSeq: seq-1, Limit: offset+limit+1)` 按 ref 向后读窗口，可跨入被屏蔽区间，且可读任意会话 id。T3 只写“检索……统一排除”，T1 只改 `SearchEntriesPage`，测试也只列检索三种作用域。
- 建议：T3 明确 readMessages 跳过被屏蔽条目，并在跳过后补足 offset/limit（用 T1 游标或按区间跳读）；“more messages follow”提示的 ref 不得落在屏蔽区间；ref 本身被屏蔽时按“不存在”报告。加入当前会话与其他会话两种 recall 用例。`HasSearchableEntries`（`fts.go:289`）口径可保持不变，但需写明。

### F5 [CORE_CORRECTIVE] 跨会话检索排除的机制未定；`RewindRecords` 走 `idx_entries_type` 的说法与现有查询计划记录不符

- 证据：
  - T1 写 `SearchEntriesPage` “支持传入屏蔽区间”；但 workspace/all 作用域要排除**各个**会话的区间，调用方事先无法知道命中哪些会话。合理做法是在 SQL 内用相关子查询，例如 `NOT EXISTS (SELECT 1 FROM entries r WHERE r.session_id=f.session_id AND r.type='rewind' AND f.seq > json_extract(r.json,'$.cut_seq') AND f.seq < r.seq)`。计划没有锁定方案。
  - `idx_entries_type` 只建在 `entries(type)` 上（`migrations/20261017-120000-add-query-indexes.sql:25`），同一迁移注释写明“LatestEntry (session_id + type) keeps using the (session_id, seq) primary key”。`RewindRecords(sid)` 按 `session_id=? AND type='rewind'` 查询时，规划器很可能对该会话做主键范围扫描（BenchmarkSessionCard 会话有 2 万行）。计划禁止改 DDL，不能加复合索引。
  - 新增过滤字段若不写入 `fingerprintEntrySearch`（`fts_cursor.go:132-156`），快照游标会跨过滤条件复用。
- 建议：T1 明确 SQL 内相关子查询方案（`'rewind'` 不在 FTS 索引类型内，不会自命中）；`RewindRecords` 与子查询用 `INDEXED BY idx_entries_type` 或经 `EXPLAIN QUERY PLAN` 验证后写进测试（参照 `indexes_test.go`）；指出排除逻辑是常开的，不是调用方可选参数（sugasdk `engine.go:296` 也会经过，需在 CHANGELOG 写明行为）。

### F6 [CORE_CORRECTIVE] session card 的 SegmentCount 与 Turns 缺少 store 方法；基准未覆盖有屏蔽区间的会话

- 证据：`session_card.go:126` `CountEntriesByType(compaction)`、`:129` `CountTurns`。T1 只新增 `RecentEntriesBefore`/`LatestEntryBefore`，没有“按区间排除的计数”方法。T3 写“Turns 凭区间内条目的 turn_id 判定”，同样没有对应的 store 查询。现有基准 `session_card_test.go:279` 不含 rewind 记录，所以“回退不超过 20%”只测到无屏蔽路径；补足若按页逐步读，被屏蔽区间很大时成本与区间大小成正比。
- 建议：T1 增加 `CountEntriesByTypeExcluding(sid, typ, ranges)` 与 `CountMaskedTurns(sid, ranges)`（区间内 `COUNT(DISTINCT turn_id)`，turn_id 非空，主键范围扫描）；游标补足在遇到被屏蔽的 seq 时直接跳到该区间的 `cut_seq`，不要逐页读；基准增加“含 1 个大屏蔽区间”的变体并纳入 20% Gate。

### F7 [CORE_CORRECTIVE] `rewindFiles` 只写“直接循环调用 restoreItem”，缺少取得带根校验的检查点行

- 证据：`restoreItem` 对 `RootStatus != SnapshotRootTrusted` 的项一律跳过（`undo_restore.go:147-151`）。RootStatus 由 `checkpointsLocked` → `annotateRoots` 填充（`undo.go:190-205,229`）。如果自行从 `LoadTurn` 组装 item，就会全部被跳过。
- 建议：T6 写明复用不加锁的 `u.checkpointsLocked(ctx, sid, 0)` 取行，再按“最新→目标轮”筛选；把 T6 的入口定义为 `*UndoCore` 方法（与 F1 的签名一并确定）。

### F8 [CORE_CORRECTIVE] “有运行中的后台任务”在 Runner 内无法判断

- 证据：`Runner.cfg.Tasks` 的类型是 `TaskNotices` 接口，只有 `TakeNotices`/`ReturnNotices`（`tasks.go:18-24`、`runner.go:89`），没有 `Running`。`tasks.Manager.Running` 在 `pkg/agentcore/tasks/tasks.go:766`，只有 chat 持有 Manager（`chat.go:350`）。另外 chat 的 `taskNotice` 回调也会 `TakeNotices`，与 T5 的“取出后丢弃”存在竞争。
- 建议：T4 选择一种方式并写进计划：扩展 `TaskNotices` 接口加 `Running(sid)`（wiring 不需改，Manager 已实现），或由 T8 在 chat 层拒绝；同时说明 T5 丢弃通知与 chat taskWake 的先后顺序（在持 gate 期间取）。

### F9 [CORE_CORRECTIVE] T2 与 T3 标为可并行，但 T2 需要 T3 的屏蔽集合

- 证据：T2 的 `userTurns` 要“排除已被屏蔽的轮”，`resolveTarget` 要校验“不在屏蔽区间内”，这正是 T3 `visible.go` 的 `masked.Hidden`。T2 依赖写“无（T1 未合入前先用接口桩）”，只考虑了 T1。两个 worktree 并行时容易各自实现一份区间判定（违反 CAP-02“不复制”）。另外 T7 的“前置拒绝条件同 rewind”要复用 T4 的检查函数，而 T7 不依赖 T4。
- 建议：把 `masked` 类型（仅构造与 Hidden，不含读者接入）前移到 T1 或 T2，T3 只做读者接入；或者把 T2 改为依赖 T3。前置拒绝抽成一个函数，放在 T2（选轮）中，供 T4 与 T7 共用。

### F10 [ADVISORY] 不受影响读者清单（S-N1）可补全

grep 结果中还有 `compact.go:424` segmentOrdinal（只用于 trace 序号）、`store/agentstore.go:125`（按 run 读取）、`pkg/sugasdk/request.go:182`（按 turn 读取）、`engine.go:370` EntriesAround 与 `:296` 检索（sugasdk，属范围外）、`traceweb.go`、`cli/export.go`、`cli/trace.go`、`cli/sessions.go:130`（CLI 检索，非模型侧）。建议一并写进注释与 progress，并写明 sugasdk 与 CLI 检索在本期是否排除。

### F11 [ADVISORY] LastReply 的“上一回合未正常结束”判断在回退后可能误报

`turnUnfinished` 依据 `Session.LastTurnID` 的状态（`lastreply.go` 注释第二条信号）。回退到 N 之后，last_turn_id 仍指向被屏蔽的轮；如果那一轮失败或被取消，resume 时会误报。建议在 T3 中让 LastReply 忽略被屏蔽的 last turn，或在 T4 中说明这是可接受的行为。

### F12 [ADVISORY] 清理检测细节

`attrs["target"]` 是字符串数组（`observe.go:419-434`），从 DB 读出后是 `[]any`。工具事件还带有 `attrs["snapshot"]`（`observe.go:350-351`），可以区分“快照未拍（超限、关闭）”与“拍过后被清理”。建议 T6 用它细化不可恢复原因，避免把超过 MaxFileBytes 的调用误报为“已清理”。

### F13 [ADVISORY] 测试判据与夹具

- T0 的“第 k 次请求写入 vk”：一轮至少两次请求（工具调用、最终回复），按请求序号写入很脆弱。建议按 user 消息里的标记（如“第2轮”）选择写入内容，与 `editCalls` 的现有风格一致（`fakellm_test.go:146-172`）。`fakeRequest.Messages` 已有全部消息文本（`:76-84`），扩展为带 role 的列表即可。
- T7 中“fork 后 loadHistory 条目数等于源会话 cut 之前的可见条目数”需要可计算的判据：建议在源会话的副本上执行 conversation rewind 后取 loadHistory 作为基准，比较消息内容，不只比较条目数。T9 中 fork 的“请求等于第 N 轮之前的可见历史”，可以用 fakeLLM 记录的源会话第 N 轮请求（去掉最后一条 user 消息）作为基准，并说明 segment context 中随时间变化的部分如何处理。
- `rewind` entry 的 TurnID 应当明确为空（不属于任何轮），否则会影响 Turns 计数与 cut 判定。

### F14 [ADVISORY] 文件归属与规模

T5 会新增模型侧通知文案（`model_texts.go` 走 `catalog/*/model.ini`），但它的文件清单没有列出 `internal/i18n/catalog/**`，T8 也会改同一组 ini（T8 依赖 T5，属顺序修改，不冲突）。`rewind.go` 由 T2、T4、T5 依次修改，要注意 GR104 规定的 400 行上限，可以预先把用户轮索引拆到 `rewind_turns.go`。

## 设计遗留项落实情况

| 遗留项 | 计划落点 | 本轮判断 |
|---|---|---|
| F11 可测性 | T0、T9 | 基本落实，判据见 F13 |
| N1 fork 原子性 | T1、T7 | 未闭合，见 F2、F3 |
| N2 清理检测只计 ok | T6 | 落实，细化见 F12 |
| N3 Turns 口径 | T3 | 缺 store 方法，见 F6 |
| N4 undo 通知合并 | T5 | 落实 |
| N5 游标、FTS 排除、基准 | T1、T3 | 部分落实，见 F5、F6 |
| S-N1 不受影响读者 | T3 | 落实，可补全，见 F10 |
| S-N2 跨作用域检索 | T3 | 机制未定、recall 漏项，见 F4、F5 |

## 覆盖缺口与限制

只做了静态读码，未运行 `EXPLAIN QUERY PLAN` 与基准测试，F5 中的查询计划判断依据是迁移注释，需要实测确认。未核实 `Hops>0` 与 `suga send` 轮是否带 injected 标记（计划已安排在实施前核实）。没有逐行检查 fork 中图片引用的具体格式。

## 结论

**BLOCKED**（可执行轴，Round 1）。

- CORE_BLOCKING：F1（预览、确认、执行三步没有接口归属，T4 与 T6 的编排依赖缺失）、F2（fork 的 seq 重写在事务外，rewind 记录与 meta 的复制规则缺失）。
- CORE_CORRECTIVE：F3–F9。
- ADVISORY：F10–F14。

其余部分可以落地：loadHistory 先屏蔽再走状态机、读者主体覆盖、undo 复用路径、清理检测、fakeLLM 扩展、e2e 场景。

## 剩余风险与后续

修订后进入第 2 轮确认，只读 diff 与受影响的任务。实施中的主要风险有三项：带屏蔽区间的 session card 与检索性能（F5、F6 实测）、fork 失败清理路径（包括 body 文件），以及 Windows ConPTY 下 e2e 的稳定性。
