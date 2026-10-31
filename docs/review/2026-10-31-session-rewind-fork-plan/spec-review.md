<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审（Round 3）

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，计划 commit `19f6f589`）
- 修订版本：Draft 0.3
- 依据：设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准）；本目录第 2 轮可执行轴报告（commit `71a0b184`，F15–F24）；`git diff 3618827e 19f6f589 -- docs/plans/2026-10-31-session-rewind-fork-plan.md`
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，未读 standards-review.md
- 轮次：**Round 3**
- 日期：2026-10-31
- **结论：PASS**（无开放 CORE_BLOCKING）。新增 CORE_CORRECTIVE F25–F28 都会在实施时撞上“改接口形状要回到计划修订”或违背设计验收，建议在 0.4 中写入后再交用户批准；修订只需逐条核对 diff，不需要再开一轮

## 输入与方法

在 HEAD `19f6f589` 上静态读码，没有运行测试，也没有运行 `EXPLAIN QUERY PLAN`。读过的代码：

- 锁与 undo：`gate.go`（Acquire、TryAcquire、TryAcquirePreemptible）、`compact.go:775`、`undo.go`（UndoGate、UndoCore、checkpointsLocked、withSession、annotateRoots、snapshotRootStatus）、`undo_runner.go`、`undo_restore.go`（Undo、restoreItem）、`undo_notice.go`、`runner.go:600-650`。
- 读者：`session_card.go:99-210`、`ledger.go`（Ledger、Search、Fetch、readMessages）。
- store：`entries.go`（ListEntries、LatestEntry、RecentEntries、CountEntriesByType、LastEntrySeq）、`types.go:207` EntryFilter、`turns.go:91` CountTurns、`migrations/20261008-230000-create-core-store.sql`、`migrations/20261017-120000-add-query-indexes.sql`。
- meta：grep `PatchSessionMeta`、`.Meta[...] =`、`Meta*` 常量（internal 与 pkg），`store/session_origin.go`、`adddir.MetaKey`、`runner_sessions.go:155-180`。
- 任务通知：`tasks.go:18` TaskNotices、`pkg/agentcore/tasks/tasks.go` 的 List/TakeNotices/ReturnNotices/Running、TaskNotices 全部实现方。

## 第 2 轮发现关闭状态

| 项 | 级别 | 0.3 落点 | 状态 | 说明 |
|---|---|---|---|---|
| F15 锁内重算、Stamp 覆盖 | CORE_CORRECTIVE | 跨任务接口“过期判定”、T5 第 1–2 步 | **CLOSED（遗留转 F25）** | 去掉 Stamp，锁内重算目标解析、`planRewindFiles` 与 LatestSeq，逐项比较；三种过期用例与反向验证已列。核对三种过期：①追加 entry → LatestSeq 变化，可检测；②删快照目录 → `ListCheckpoints` 按目录派生（`undo.go:63-64` 注释），该轮转为“有 ok 写事件无快照”的 cleared 不可恢复项，可检测；③改 cwd → `annotateRoots`（`undo.go:229`）把非 trusted 根的项改为 Skip 并带原因，前提是 `RewindFileItem` 把它映射为 `unrecoverable/root_untrusted`（Action 枚举里没有 skip，要写明映射），可检测。但 `RewindFileItem` 缺 restoreItem 所需字段，见 F25 |
| F16 待注入通知来源 | CORE_CORRECTIVE | T5 RewindPlan、T7 | CLOSED | 采用第 2 轮建议 (b) 的变体：预览只给一般提示，执行后用 `TakeNotices` 的实际返回填 `DroppedTasks`；不扩 agentcore。运行中判定用接口新增的 `List`（`*tasks.Manager.List` 已存在，`tasks.go:559`）；需同步的实现只有 `tasks_test.go:29` 的 fakeNotices |
| F17 card LatestSummary、sugasdk 说明 | CORE_CORRECTIVE | T2、计划工作声明、T8 CHANGELOG | CLOSED | 四个字段齐全；`RecentEntriesSkipping` 有了调用方；scope freeze 与 CHANGELOG 已写明 sugasdk SessionCard 的变化 |
| F18 recall store 读法与续读 ref | CORE_CORRECTIVE | 接口 `EntriesFromSkipping`、T2 | **PARTIAL → F27** | 有了 SQL 侧分段读法；但签名缺类型过滤，续读规则与“ref 在区间内按不存在处理”互相矛盾 |
| F19 区间读取代价 | CORE_CORRECTIVE | T1、T2、fail-closed | CLOSED（细节见 F29） | Skipping 按可见段走主键并限 LIMIT；`RewindRecords` 两种备选按 EXPLAIN 与基准定；尾部 1 万行屏蔽基准、2 倍 Gate 已列。缓存键的读取代价见 F29 |
| F20 UndoCore.RewindPlan 职责 | ADVISORY | 跨任务接口“分工” | CLOSED | 明确不设 `UndoCore.RewindPlan` |
| F21 回合进行中判定、通知互斥理由 | ADVISORY | 接口“并发判定”、T3、T5 | **PARTIAL → F28** | 改为 `TryAcquire` 失败即 `ErrRewindBusy`。但 gate 的另一类持有者（空闲整理的可抢占持有）也会让 TryAcquire 失败，见 F28。T5 第 6 步的互斥理由仍只引 `context.go:355`，没补 chat 空闲时 gate 外取通知（`chat.go:349,747,791`）与 `:rewind` 同在输入主循环的依据（ADVISORY 遗留） |
| F22 meta 白名单默认拒绝 | ADVISORY | T6 | CLOSED（细节见 F30） | 默认拒绝、注释列白名单、测试逐项断言、progress 列出未复制键 |
| F23 undo 通知合并与 event_id | ADVISORY | T5 第 8 步 | **PARTIAL → F26** | 错误引用已删，不改回填逻辑。但“实施时先核实是否合成一条”现在就能回答：不是，每回合只取一条，rewind 通知会被延后，见 F26 |
| F24 测试判据补充 | ADVISORY | T8、前置检查 | PARTIAL | baseline 已改为 `66a3cbc8`；三种 scope 判据已写。仍未落实：rewind entry 的 `TurnID` 为空；T8 fork e2e 的对照基准与段上下文处理；fork 后大正文仍为 stub 且可 Hydrate 的用例；card LatestSummary 与 `ExcludeSession` 的反向验证；T2 依赖列补 T0。均为 ADVISORY 遗留 |

## 新发现

### F25 [CORE_CORRECTIVE] `RewindFileItem` 不足以调用 `restoreItem`，`applyRewindFiles` 按冻结签名无法实现

- 证据：
  - `restoreItem(ctx, session, sessionID, turnID, item CheckpointItem, force)`（`undo_restore.go:143`）需要 `CheckpointItem.Meta`（SnapshotMeta：Root、Rel、RealRel、Kind、Status、Tool）和 `RootStatus` 才能恢复；恢复顺序还依赖同一轮内 Items 的逆序（`undo_restore.go:68-70` 注释），状态写回需要按轮调用 `SetCheckpointState`。
  - 接口里的 `RewindFileItem` 只有 `TurnID、Path、Action、Reason`，`applyRewindFiles(ctx, sid, items []RewindFileItem)` 拿不到 Meta，只能在函数内再调一次 `checkpointsLocked` 按 path 反查。这就等于再算一遍计划，而且同一轮写同一 path 两次时按 path 反查有歧义。
  - 计划规定“改形状要回到计划修订”，所以 T4 开工就会触发修订。
- 建议（二选一，写进跨任务接口）：
  - `RewindFileItem` 增加不导出或 `json:"-"` 的 `item CheckpointItem` 字段（或 `Meta SnapshotMeta` 与 `RootStatus`），比较时只比较 TurnID、Path、Action、Reason；或
  - `planRewindFiles` 返回 `[]rewindTurn{TurnID; State; Items []CheckpointItem}`（内部类型），`RewindPlan.Files` 由它展平生成；`applyRewindFiles` 接收内部类型。
  - 同时写明非 trusted 根映射为 `Action=unrecoverable, Reason=root_untrusted`（或新增 `skip`），否则“改 cwd”过期用例的比较依据不确定。

### F26 [CORE_CORRECTIVE] undo 队列每回合只注入一条；队列非空时 rewind 通知会被推迟，违背 R5“下一轮注入”

- 证据：
  - `takeUndoNotice`（`undo_notice.go:119-139`）只取 `notices[0]`，其余写回队列；`injectUndoNotice`（`:157`）每回合调用一次（`runner.go:646`），返回单个 ctxItem。所以现有注入不会合成一条，而是一回合一条。
  - T5 第 8 步把 rewind 通知追加到队尾并保留已有 undo 通知。用户先 `:undo` 再 `:rewind N conversation`、中间不发消息（很常见），下一轮只会注入那条 undo 通知；“磁盘未回退”的通知要等下下轮。这违背设计 R5（“强制在下一轮注入”）与 T8 conversation 判据。
  - 计划写“实施时先核实……不是的话，在 progress 中写明实际行为，并保证各项都送达”：只保证“送达”不够，还要保证下一轮送达；而要做到这一点必须改 `undo_notice.go` 与 `runner.go`，二者都不在 T5 文件清单里。
  - 另：设计 0.2 第 3 节原文是“队列中的 undo 通知同样丢弃，由本次 rewind 的通知取代”；计划依据设计评审 N4 改为保留，已在“输入与批准证据”声明为偏差，这一点可以接受。
- 建议：
  - 把 `injectUndoNotice` 改为一回合取空整个队列，每项各写一条 user entry，并各自按 `EventID` 回填 `entry_seq`（回填逻辑不变，只是循环）；`runner.go:646` 处追加多个 ctxItem。这样同时满足 N4（cut 之前的 undo 事实不丢）、R5（下一轮注入）、F23（回填不变）。
  - 这会改变连续多次 `:undo` 的送达时机（从逐轮改为下一轮一起送达），在 T5 与 CHANGELOG 中写明；或者只在队列含 rewind 项时一次取空，保持纯 undo 的旧行为。
  - T5 文件清单加 `undo_notice.go`、`runner.go`；用例改为“队列已有 undo 通知时，下一轮请求同时含 undo 与 rewind 通知，各自 event 的 entry_seq 正确”。

### F27 [CORE_CORRECTIVE] `EntriesFromSkipping` 缺类型过滤；续读规则与“ref 在区间内按不存在处理”矛盾

- 证据：
  - `readMessages`（`ledger.go:248-290`）用 `ListEntries(SinceSeq: seq-1, Types: ledgerMessageTypes, Limit: offset+limit+1)`，类型白名单只有 user、assistant、tool、compaction、handoff（`ledger.go:53`）。接口里的 `EntriesFromSkipping(ctx, sid, fromSeq, skip, limit)` 没有 types（也没有 NoHydrate），按冻结签名读出的窗口会混入 `rewind`、`context_update`、plan 等条目：limit 被占用，`rewind` 还会被渲染成 “unreadable entry”。
  - 现有 ref 语义是“从该 seq 起（含）向后读”，并不要求 ref 那一条存在；续读 ref 就是一个普通 ref（`recall(ref=…)`），由模型原样传回。readMessages 无法分辨它是模型自造的还是系统给的续读位置。所以 T2 的两条规则——“ref 指向被屏蔽条目按不存在处理”和“续读位置落在区间内自动跳到区间后”——对同一个输入给出相反结果。典型触发：窗口最后一条正好是 cut 条目，续读 ref = cut+1 恰在区间内。
- 建议：
  - 签名改为 `EntriesFromSkipping(ctx, sid string, fromSeq int64, types []string, skip SeqRanges, limit int)`（NoHydrate 按 recall 现状不需要）。
  - 续读 ref 直接取多读的那一条（第 limit+1 条）的 seq，它必然可见，不再用 `last.Seq+1`；于是“系统给出的 ref 永不落在区间内”，可以统一规定“ref 落在区间内按不存在处理”（或统一规定为从区间后继续）——二者选一，不要同时写。
  - 用例保留“窗口末尾正好是 cut”“续读跨过区间”“模型给出区间内的 ref”三项。
  - 附带（ADVISORY）：`Ledger` 只持有 `*store.Store`（`ledger.go:58-65`），没有 Runner 引用，拿不到 T2 放在 Runner 上的屏蔽缓存。写明 recall 与 `session_search` 的屏蔽区间来源：直接调 `RewindRecords` 构造，或在 `New` 中像 `bind` 一样注入取区间的函数。另外只在 `sid == 当前会话` 时屏蔽（R11 字面范围），T2 已有此意，建议在 readMessages 处写明判断位置。

### F28 [CORE_CORRECTIVE] `TryAcquire` 也会被空闲整理的可抢占持有拒绝，chat 空闲一段时间后 `:rewind`/`:fork` 可能一直报“回合进行中”

- 证据：
  - `TryAcquire`（`gate.go:133-141`）只看 `st.held`，不区分持有者是否可抢占；空闲整理用 `TryAcquirePreemptible`（`compact.go:775`）以可抢占方式持有 gate。普通回合用 `Acquire`，会先请可抢占持有者让出（`gate.go:104-130`）。
  - 结果：用户空闲后输入 `:rewind`，恰逢空闲整理在跑（含一次模型调用），会得到 `ErrRewindBusy`，提示“等回合结束”，而此时并没有回合；用户随后发普通消息反而能抢占。这是常见路径上的错误提示与不一致行为。
  - 另外 `UndoCore.Gate` 是只有 `Acquire` 的 `UndoGate` 接口（`undo.go:102-105`），`r.UndoCore(...)` 会装上它（`undo_runner.go:13`）。Runner 先 TryAcquire 再调用任何走 `withSession` 的 UndoCore 方法会自锁。计划写“跨进程锁沿用 undo 的取法”，但现有取法 `withSession` 把 gate 与跨进程锁捆在一起。
- 建议：
  - gate 新增 `TryAcquireOrPreempt(ctx)`：未持有则取；持有者可抢占则请其让出并等待（受 ctx 限时）；不可抢占则失败返回 `ErrRewindBusy`。放进 T3 文件清单（`gate.go`），T5、T6 共用；T3 加用例“空闲整理持有时 rewind 可抢占成功”。
  - 跨进程锁写明取法：用 `Gate=nil` 的 UndoCore 副本调用 `withSession`（此时只取进程锁，并复用 chat 的 `HeldLock`），或抽出只取进程锁的 helper；并注明不得在已持 gate 时调用 `Checkpoints/Select/Undo`。
  - `RewindPlan`（预览）同样调用 `planRewindFiles`，而后者的约定是“调用方持有 gate 与跨进程锁”。写明预览也要取 gate 与锁（同一取法），或者说明预览在 chat 已持进程锁、只读的前提下可以不取 gate。

### F29 [ADVISORY] 屏蔽缓存的“键”本身要查库，代价约等于 `RewindRecords`

- 证据：T2 把缓存键定为“会话最新的 rewind 记录 seq”，并要求跨进程写入后“按键比较发现变化”。取这个键需要 `SELECT MAX(seq) FROM entries WHERE session_id=? AND type='rewind'`：走主键时要扫该会话全部行（与第 2 轮 F19 指出的代价相同），走 `idx_entries_type` 时要扫全库 rewind 行（很少）。无论哪种，取键的代价和直接 `RewindRecords` 一样，缓存省不下查询，只省解析。迁移注释（`20261017-120000-add-query-indexes.sql:8-9`）也说明 `session_id + type` 的查询规划器会选主键，所以要走类型索引必须 `INDEXED BY`。
- 建议：缓存记录“上次读到的会话最新 seq”（`LastEntrySeq`，主键一次读）。下次先取 `LastEntrySeq`，不变则直接用缓存；变了只查 `session_id=? AND seq>? AND type='rewind'`（主键范围，只覆盖新增行），把新记录并入区间。rewind 只追加，区间只增不减，增量合并是正确的。或者 T1 实测后若 `RewindRecords` 走类型索引已足够便宜，就直接去掉缓存，减少一处失效逻辑。

### F30 [ADVISORY] fork meta 白名单的键名与取值写实

- `:rw` 不是 meta 键。真实键是 `add_dirs`（`internal/adddir/adddir.go:25`），条目带 ro/rw；设计第 172 行说“继承 `:rw` 附加目录”。写明复制整个 `add_dirs`，还是只复制 writable 项（文件回退的根信任 `snapshotRootStatus` 只认 writable，`undo.go:266-271`）。
- cwd 是 sessions 表列（`Session.Cwd`），不是 meta；白名单里写成“Session.Cwd（列）”更准确。
- `origin` 要写明取值：沿用 `store.OriginChat`。新造一个值不会破坏 `InteractiveSession`（默认按交互保留），但会让其他按 origin 枚举的地方多一个未知值。
- 已知未复制键的清单再补上：`run_mode`（`turncontext.go:30`）、`used_interactively`、`suga_version`、`kind`、`parent_session_id`、`sdk_req:*`（`pkg/sugasdk/request.go:51`）。默认拒绝本已覆盖，补上是为了 progress 与测试断言不漏。
- `tainted_at`/`tainted_by` 是条件复制，建议在白名单注释中单列，避免实施方以为它们被默认拒绝。

### F31 [ADVISORY] 只回文件时的事件与通知写法

- T5 第 3 步写“以下 4–6 步只在 scope 含 conversation 时执行”，那么第 7（session 事件）与第 8 步（注入通知）对 files scope 同样执行。files scope 若“沿用 `:undo` 的通知方式”而调用 `enqueueUndoNotice`（`undo_notice.go:189`），它会另写一条 `EventUndo` 事件，加上第 7 步的 `session/rewind` 事件就是两条。写明 files scope 是复用 `enqueueUndoNotice`（接受两条事件，或跳过第 7 步）还是由 rewind 自己 `appendUndoNotice` 并把 `EventID` 指向 rewind 事件；同时写明从 `RewindFileItem` 构造 `UndoResult` 的计数映射。
- `cfg.Tasks` 可能为 nil（未接后台任务的 Runner），前置拒绝与第 6 步要判空。

## 并行与依赖核对

- W0：T0（`test/ptye2e/fakellm_test.go` 与 personal 测试辅助）与 T1（`store/**`）不相交。
- W1：T2（`visible.go`、context/segment_history/segment_handoff/handoff/lastreply/session_card/ledger）与 T4（`rewind_files.go`）不相交。F25 的修正只动接口类型与 `rewind_files.go`，不影响 T2。
- W2：T3 单独；F28 建议把 `gate.go` 放进 T3，仍是串行波次，无冲突。
- W3：T5（`rewind.go`、`rewind_notice.go`、`undo_runner.go`、`model.ini`，按 F26 再加 `undo_notice.go`、`runner.go`）与 T6（`fork.go`、`fork_files.go`）仍不相交。遗留：T6 的标题后缀“（分叉）”若走 i18n 要改 catalog，与 T5 的 `model.ini` 可能同文件；建议写死或放 `chat.ini` 并列入 T6 清单（第 2 轮已提）。
- 测试文件：各任务“及测试”未给文件名，建议规定新建 `<任务主文件>_test.go`，避免并行任务往同一个既有测试文件追加夹具。

## 结论

**PASS**（可执行轴，Round 3）。

- 第 2 轮 CORE_CORRECTIVE 中 F15、F16、F17、F19 已关闭；F18 部分关闭，转 F27。
- 新增 CORE_CORRECTIVE：F25（文件项缺 Meta，apply 无法按冻结签名实现）、F26（undo 队列一回合一条，rewind 通知被推迟）、F27（recall 读法缺类型过滤、续读规则矛盾）、F28（TryAcquire 被空闲整理拒绝、gate/进程锁取法与预览持锁）。都不需要改设计，修正点明确；其中 F25、F27 是接口形状问题，按计划自身规则必须在实施前修订，建议在 0.4 写入后交用户批准。
- ADVISORY：F29–F31，及 F21、F24 遗留项。

## 剩余风险与后续

- 分段主键查询、`RewindRecords` 的规划器选择、`CountTurnsSkipping`（每轮 NOT EXISTS 子查询走 `idx_entries_turn`）的代价只来自读码，要在 T1 实测。
- F26 选择“一回合取空队列”会改变纯 `:undo` 连续多次时的送达时机，需要在 CHANGELOG 中说明。
- Windows ConPTY 下 e2e 的稳定性仍是实施风险。
- 0.4 若只纳入以上纠正项，主会话按本报告逐条核对 diff 即可。
