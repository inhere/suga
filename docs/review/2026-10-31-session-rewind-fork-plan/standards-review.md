<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `19f6f589`）
- 修订版本: Draft 0.3
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告（spec-review.md）
- 轮次: Round 3（blocking closure）。读 `3618827e..19f6f589` 的计划 diff，以及受影响的章节：跨任务接口、前置检查、T2、T3、T5、T6、T7、T8、可追溯性
- 日期: 2026-10-31
- 结论: **PASS**（没有开放的 CORE_BLOCKING；S17–S20 为 CORE_CORRECTIVE，建议在计划批准前修正）
- 排除项: 未运行测试与基准；fork seq 映射、分页补足、嵌套 cut 的逐行推演归可执行轴

## 输入与方法

- 规范：`idevstd load --delivery semantic --accept-delta --task plan --task review --agent claude --consumer-mode --thinking-mode rigorous`。结果 BOUND、FULL、18 项资源，payload sha256 `a7be77b2…` 已核验。plan 合同不在 payload 内，沿用上一轮从 release 只读查阅的结论。
- 读取：计划 0.3 全文；`3618827e..19f6f589` 的计划 diff；设计 0.2（范围、非目标、读者表、R2/R9/R11、流程 3）；第 2 轮规范轴报告；仓库 `AGENTS.md`。
- 结构校验：对 `19f6f589` 的计划运行 `idevstd script validate_document --kind plan`，结果 `ok: true, errors: []`。工作区中的计划与 candidate 一致。
- 代码核对：用 grep 和 sed 核对本轮新增的引用。

| 新增引用 | 结果 |
|---|---|
| `session_card.go:117` LatestSummary（`RecentEntries` handoff/compaction） | 一致 |
| `pkg/sugasdk/session.go:464` | 一致。调用 `Runner.SessionCard`，但对外的方法名是 `Session.Card`，见 S21 |
| `undo_notice.go:22` undoNoticeQueueKey、`:182` UpdateEventEntrySeq、`:199` `notice.EventID = event.ID` | 一致（计划写 `:181`，实际回填调用在 182 行，差 1 行，可忽略） |
| `gate.go:133` TryAcquire | 一致 |
| `chat_commands.go:381` `case isUndoCommand` | 一致 |
| `undo.go:207-227` withSession（阻塞 `Gate.Acquire` 加跨进程锁）；`UndoGate` 接口只有 `Acquire`（`undo.go:102-104`） | 与 S18 相关 |
| `undo_restore.go:143` `restoreItem(..., item CheckpointItem, ...)`；`CheckpointItem.Meta SnapshotMeta`（`undo.go:32-41`） | 与 S19 相关 |
| `tasks.Manager.TakeNotices` 返回 `[]tasks.Info`（`pkg/agentcore/tasks/tasks.go:686`），与 `RewindResult.DroppedTasks` 的类型一致 | 一致 |
| sugasdk 调用的 Runner 方法：`Run`、`RunWithContract`、`Steer`、`SessionCard`、`Compact`（`pkg/sugasdk/session.go:300,314,448,464,477`） | 与 S17 相关 |

## 验收标准

plan 合同（思考声明、修订记录、Capability Discovery 三表、`host_or_non_offline_action`、前置检查与 fail-closed、波次依赖、任务五要素、跨任务签名、baseline、人工 Gate、可追溯、完成 Gate）；评审合同（同类穷尽、五类 CORE_BLOCKING 与 necessity proof、新增机制的 scope necessity）；SR1301；SR1403/SR1405/SR1410；GR104、GR107–GR110、SR2002–SR2004。

## 第 2 轮发现关闭状态

| ID | 状态 | 依据 |
|---|---|---|
| S11 ApplyRewind 不分 scope（CORE_BLOCKING） | **已关闭** | T5 第 3 步写明 4–6 步只在 scope 含 conversation 时执行（381 行）。files scope 不写 rewind 记录，所以 T2 的区间只来自 conversation/both 的记录，`maskedRanges` 不需要按 Scope 过滤。第 8 步的通知分为只回文件和含会话两种（390-391 行）。`RewindPlan.DropTasks` 已删除。新增了用例“files scope 不写记录、不改 meta、不丢通知、历史不变”和反向验证“去掉 scope 条件，用例失败”（397、406 行）。T8 给出了三种 scope 的判据（479-481 行） |
| S12 接口形状与归属 | 基本关闭 | 已补 `RewindFileItem`、`RewindResult`、`ForkResult`、T4 的两个签名、`SeqRange` 与 `SeqRanges.Hidden`。分工写明 Runner 负责目标、前置、任务与 meta，UndoCore 只负责文件，删掉了 `UndoCore.RewindPlan`（101-144 行）。残留：`ForkInput` 与 `RewindFileAction` 仍没有类型定义，只在 T1 正文和注释中出现（110、116、250 行），属上轮未穷尽，ADVISORY。`RewindFileItem` 的新形状引出 S19 |
| S13 任务通知预览 | **已关闭** | 选了方案一：预览只给一般提示，执行结果列出实际丢弃项（377、387、461-462 行），不扩 agentcore |
| S14 R11 deferred 的依据 | **已关闭** | 排除项分为“设计 R9”和“本计划依据设计 R11 字面范围的决定”两条（41-47 行），完成定义第 6 条同步（34 行） |
| S15 NM-01 归类 | **已关闭** | CAP-08 改为 `OWNER_EXTENSION`，New module candidates 为 `NONE` sentinel（78、85 行） |
| S16 baseline 与文字细节 | 部分关闭 | 已关闭：baseline 改为 `66a3cbc8`，开工时重新采集（155-156 行）；分发位置改为 `chat_commands.go:381`（158 行）；补了文件拆分理由（194 行）。**未关闭（上轮未穷尽）**：① fail-closed 仍写“改 sugasdk 行为即停止”（190 行），与 0.3 新写的 sugasdk 行为变化说明矛盾，见 S17；② T6 的 title 后缀“（分叉）”仍没写是否走 i18n（428 行） |

## 发现

### S17 [MEDIUM] scope freeze 中 sugasdk 行为变化的说明与设计相容，但措辞过窄，并与 fail-closed 冲突（CORE_CORRECTIVE）

- 相容性核对：设计 R2（design 264 行）要求“凡进入模型请求、交接包或用户可见当前状态的读者”都经过屏蔽，读者表把 session card 列在其中（design 76 行）。R9（design 271 行）与非目标（design 50 行）延后的是 sugasdk 的**新增 API 与暴露**，不是现有读者的行为。所以“SessionCard 随之反映回退后状态”与 R2、R9 相容，计划做这个说明是对的。
- 问题（同类全部出现点）：
  - 23 行写“sugasdk 行为**只有一处**随之变化：SessionCard”。但 sugasdk 的 `Session.Run`/`RunWithContract`（`session.go:300,314`）走 `loadHistory`，并让模型使用 `session_search`/recall。`Session.Compact`（`session.go:477`）走 `recentRequests`/`latestPlan`/`buildHandoff`。在 chat 中回退过的会话上，这些路径的结果也会按 R2/R11 变化。这是设计本身要的结果，但“只有一处”不成立。
  - 190 行 fail-closed 仍写“需要改……sugasdk 行为……属 Semantic Amendment，停止”，没有给 23 行列出的变化留出例外。T2 的实施者照字面会在改 `session_card.go`/`context.go` 时触发停止条件（S16 第 5 点上轮未穷尽）。
  - T8 的 CHANGELOG 条目（488 行）写 “sugasdk `SessionCard`”，对外的方法名其实是 `Session.Card`（`session.go:460`），返回 `Card`。
- 影响：治理条款自相矛盾，实施者可能误停；CHANGELOG 对外说明不准确。不影响结果的正确性。
- 建议：
  - 23 行改为“sugasdk 现有的读路径（`Session.Card`、`Run`、`Compact`）在回退过的会话上按设计 R2/R11 反映可见状态；`Entries` 与 `engine.go:296` 的搜索保持原样；不新增 sugasdk API”；
  - 190 行改为“需要改 DDL、CLI、ACP、sugasdk 公开 API，或改变 scope freeze 所列之外的 sugasdk/用户可见行为……停止”；
  - T8 的 CHANGELOG 用 `Session.Card` 的名字，并补一句 `Run`/`Compact` 在回退过的会话上的变化。

### S18 [MEDIUM] gate 与跨进程锁的取法、预览是否加锁、Busy 的归属写得不一致（CORE_CORRECTIVE）

- 证据：
  - 147-149 行：rewind 与 fork 用 `TryAcquire` 取 gate，“跨进程锁沿用 undo 的取法”。但 undo 的取法 `UndoCore.withSession`（`undo.go:207-227`）把阻塞的 `Gate.Acquire` 与跨进程锁绑在一起，`UndoGate` 接口也只有 `Acquire`（`undo.go:102-104`）。gate 不可重入（CAP-01，`gate.go:115-140`），所以 `TryAcquire` 之后再调用 `withSession` 会自锁。要只取跨进程锁，需要从 `withSession` 中抽出锁的部分，计划没有写这一步，也没有写跨进程锁被占时返回现有的 `ErrSessionBusy`（`undo.go:220`）还是 `ErrRewindBusy`。
  - T4 的方法要求“调用方持有 gate 与锁”（112、347 行）。T5 的 `RewindPlan` 标为“预览，只读”（373 行），并调用 `planRewindFiles`，但没有写预览是否取 gate 与锁。对照的 undo 预览 `Select` 是在 `withSession` 内调用 `checkpointsLocked` 的（`undo.go:150-187`）。
  - T3 `rewindPreconditions` 写“调用方已用 `TryAcquire` 取得 gate”，同时又把“回合进行中：`TryAcquire` 失败即返回 `ErrRewindBusy`”列为它的一项拒绝（330-331 行）。已经持有 gate 的函数观察不到 `TryAcquire` 失败，所以 Busy 的判定是在 T3 还是在 T5/T6 的调用方，写得不唯一。T3 的验证“三种前置拒绝”也因此不清楚测的是哪三种。
- 影响：实施方式不唯一。照字面实施会自锁，或者预览在没有锁的情况下读快照。T5 的锁内重算加比较能兜住数据安全，测试也能发现自锁，所以不构成 blocker。
- 建议：在跨任务接口中写定：
  - Runner 的 `withRewindSession(sid, fn)` 先 `r.gate(sid).TryAcquire()`，失败返回 `ErrRewindBusy`；再取跨进程锁，这部分从 `withSession` 抽出共用的 `withLock`，被占时返回 `ErrSessionBusy`（或映射为 `ErrRewindBusy`，二选一写死）；
  - `RewindPlan`、`ApplyRewind`、`Fork` 都在这个函数内执行；
  - T3 的 `rewindPreconditions` 只判定归档与运行中任务两项，Busy 的判定归调用方，T3 的验证相应改为两项。

### S19 [MEDIUM] 冻结的 `RewindFileItem` 形状不含 `restoreItem` 所需的快照元数据（CORE_CORRECTIVE）

- 证据：
  - 95 行规定“改形状要回到计划修订”。
  - `RewindFileItem` 只有 `TurnID`、`Path`、`Action`、`Reason`（113-118 行）。`applyRewindFiles(ctx, sid, items []RewindFileItem)` 要“循环调用 `restoreItem`”（349 行），但 `restoreItem` 的入参是 `CheckpointItem`，需要其中的 `Meta SnapshotMeta`（快照文件、hash 等）和 `RootStatus`（`undo_restore.go:143`、`undo.go:32-41`）。
  - 只凭 `Path` 无法恢复。实施者要么给类型加字段（这违反冻结），要么在 `applyRewindFiles` 内重跑 `checkpointsLocked`，再按 TurnID 加 Path 匹配（计划没写，而且与“只接受调用方在锁内刚算出的条目”的意图重复）。
- 影响：T4 的动作与接口不能同时照字面执行，实施方式不唯一。
- 建议：`RewindFileItem` 增加一个不导出的 `item CheckpointItem` 字段（也可以导出为 `Item`），注明预览与比较只用导出字段，`applyRewindFiles` 用 `item` 调用 `restoreItem`。也可以写明比较的键是 `(TurnID, Path, Action, Reason)`。

### S20 [LOW] T2 新增的屏蔽区间缓存没有证明必要性（CORE_CORRECTIVE）

- 证据：
  - T2 的 `maskedRanges` 在 0.3 中新增“按会话缓存在 Runner 中，键为最新 rewind 记录 seq，本进程写入时失效，其他进程写入时按键比较后重建”（278-280 行）。T2 还为它加了一个跨 Store 实例的用例（310 行），T5 第 4 步要负责让它失效（382 行）。
  - 按键比较需要每次读都查一次“最新 rewind 记录 seq”。T1 已经说明 rewind 条目“在全库中极少”，并要求按 EXPLAIN 和基准选定 `RewindRecords` 的查询方式（236-238 行）。所以缓存省掉的只是对极少几条记录的区间合并，而省不掉查询本身。计划没有说明为什么需要缓存。
  - 缓存是 Runner 上新的共享状态，计划也没有写它的并发保护。这与 `-race` 的要求相关。
- 依据：SR1403、SR1410（默认最小方案，重机制须证明局部修复不足）；Review Candidate Loop 的 scope necessity（必要性未证明时，支持删除新增机制）。
- 建议：删除缓存，T5 第 4 步与 T2 的缓存用例随之删除。如果担心性能，改成条件：“T2 基准超过 20% 时，才允许加入按会话缓存（带 mutex），并记入 progress”。

### S21 [LOW] 文字与元数据细节（ADVISORY）

- 194-195 行：“文件拆分理由”插在 fail-closed 列表中间，原属 fail-closed 的“容器内 git 只对单条命令前缀……”一条变成了它的子项。应把拆分理由移到 fail-closed 列表之后，或改为平级。
- 58 行“计划评审”只写 round 1（`a59b27d0`），没有补 round 2（`71a0b184`）。35 行完成定义第 7 条仍写“计划评审第 1 轮”，可追溯表其实已覆盖第 2 轮。这属元数据，不需要递增版本。
- S12 残留：`ForkInput{Session, Turns, Entries}` 与 `RewindFileAction` 应在接口一节给出类型定义。
- S16 残留：T6 的 title 后缀“（分叉）”是否走 i18n。如果走，要在 T6 的文件清单中加 catalog 文件，并说明与同波次 T5 的 `model.ini` 改动不冲突。
- files scope 下，T5 第 7 步写 `session/rewind` 事件，第 8 步复用的 `enqueueUndoNotice` 又会写一条 `EventUndo`（`undo_notice.go:191-199`），同一次操作会产生两条事件。建议写明这是预期行为，或者 files scope 只保留其中一条。
- 用 `TryAcquire` 判定“回合进行中”时，后台整理用 `TryAcquirePreemptible` 持有 gate（`gate.go:174`）的情况也会让 rewind 返回 Busy，而 `Acquire` 在这种情况下会先让对方让出。建议在 T3 或 T5 中注明这一点是否可以接受（这一条主要归可执行轴，这里只作提示）。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，不新增入口）。
- 注入防护：已核对。Skipping 查询改为按可见段做 seq 范围查询，仍然绑定参数（240 行）。
- 秘密不进日志或输出：已核对。事件 attrs 只有目标轮、scope、文件统计；`RewindResult.Notice` 是给模型的通知正文，不含秘密。
- 入参校验：已核对。T3 的 `resolveTarget` 不变。
- 资源归属：已核对。turn_id 的会话归属校验与反向验证保留（328、339 行）；recall 中 ref 落在区间内时按“不存在”处理，续读跳过区间（293-294 行）；缓存按会话分键。

### 已核对且无问题

- 首行模板 1.3.0，状态 `Draft 0.3`；修订记录 0.3 只描述语义变化；RIGOROUS 声明、暴露度（两轴）、停止条件不变；validator 通过。
- `host_or_non_offline_action=REQUIRED` 恰好一处（52 行）。
- scope freeze 的路径没有扩大：0.3 新增的改动仍在 `internal/personal/**`、`internal/chat/**` 内，`pkg/agentcore/**` 与 `pkg/sugasdk/**` 都不改。
- 依赖与并行：T5 第 4 步要使 T2 的缓存失效，T5 经 T3 传递依赖 T2，依赖成立。W3 的 T5（`rewind.go`、`rewind_notice.go`、`undo_runner.go`、`model.ini`）与 T6（`fork.go`、`fork_files.go`）文件不相交。W1 的 T4 只新增 `rewind_files.go`，与 T2 不相交。
- 授权 Gate 不变：计划批准只授权 worktree 实施与本地提交；合入由主会话审核；发版 v0.54.0、push、主机安装单独走外部动作 Gate；冒烟用 `&&` 串接作为合入前提。
- 可追溯表新增了 S11–S16 与 F15–F24 的行，每项都指向任务或章节以及验证。
- 结构条目：不新增入口与 CLI，wiring 不改；GR109 在 T5 第 9 步点名；GR104 由每个新文件 400 行的上限和拆分理由约束；SR2002 由 AGENTS.md 约束。

## 覆盖缺口与限制

- 未运行测试与基准；没有验证可见段分页、续读跳区间、锁内重算比较的正确性（归可执行轴）。
- 只读了 diff 和受影响的章节；未变化的章节沿用第 2 轮的结论。
- plan 合同沿用第 2 轮从 release 只读查阅的版本，本轮没有重读。

## 结论

**PASS**（规范与治理轴，Round 3）。

- 上一轮唯一的 CORE_BLOCKING S11 已关闭，S13–S15 已关闭，S12、S16 基本关闭，残留并入 S17 与 S21。
- 本轮新发现都不是 CORE_BLOCKING：
  - S17–S20 为 CORE_CORRECTIVE，都只是改写措辞或删减内容，不新增 Module、Interface、Schema 或授权，建议在交人工计划批准前一并修正；
  - S21 为 ADVISORY。

## 剩余风险与后续

- S17–S20 的修正属于语义变化，修正后计划应递增为 0.4。修正只会澄清或删减内容，不需要新的 blocking 轮次（第 3 轮以后只为 CORE_BLOCKING 开轮）。批准时由主会话核对修正 diff 即可。
- S20 如果选择保留缓存，应写明并发保护和必要性依据（基准结果）。
- 开工时按计划重新采集 baseline（当前 main 已在 `19f6f589` 之后）。
