<!-- template_id: plan; template_version: 1.3.0 -->
# suga 会话 rewind 与 fork 实施计划

> 状态：Draft 0.8 / 待人工计划批准

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-31 | Claude | 初稿：按已批准设计 0.2 拆为 W0–W3；收口设计两轴第 2 轮遗留的 CORE_CORRECTIVE（S-N1、S-N2、F11、F-N1–N5） |
| 0.2 | 2026-10-31 | Claude | 收口计划评审第 1 轮：规格轴 F1–F9 与规范轴 S1–S8。主要变化：<br>• 新增“跨任务接口”一节，rewind 改为预览加执行两段式，入口挂在 UndoCore 适配层；<br>• 重排任务与依赖，消除并行任务改同一文件；<br>• fork 的 seq 映射在事务前算好，CreateForkedSession 复用 FTS 与正文外置写入；<br>• R11 按设计原文收窄为“任何作用域下都排除当前会话的屏蔽条目”，sugasdk 与 CLI 默认不排除；<br>• 补 recall、SegmentCount、后台任务判定、checkpointsLocked；<br>• 补 R9、R10 追溯、反向验证、运行目录 |
| 0.3 | 2026-10-31 | Claude | 收口计划评审第 2 轮：规范轴 S11–S16 与规格轴 F15–F24。主要变化：<br>• 会话部分只在 scope 含 conversation 时执行（S11）；<br>• ApplyRewind 在锁内重算回退计划并与预览比较，去掉 Stamp（F15）；<br>• 任务通知改为执行后报告实际丢弃项，预览只做一般提示（S13、F16）；<br>• 补全接口字段与 T4 签名（S12）；<br>• session card 的 LatestSummary 接入屏蔽，并在 CHANGELOG 写明 sugasdk 会话卡片的变化（F17）；<br>• recall 用区间跳过的窗口读法（F18）；<br>• 屏蔽区间按会话缓存，Skipping 查询按可见区间分段走主键（F19）；<br>• gate 改用 TryAcquire 拒绝；meta 白名单默认拒绝；undo 通知合并方式按 `undo_notice.go` 写明（F20–F23）；<br>• baseline、分发位置、NM-01 归类（S14–S16、F24） |
| 0.4 | 2026-10-31 | Claude | 收口计划评审第 3 轮（两轴 PASS）的 CORE_CORRECTIVE：S17–S20、F25–F28，并处理 S21、F29–F31。<br>• sugasdk 行为变化如实写明，fail-closed 改为“无回退记录的会话上行为变化”（S17）；<br>• 新增 `UndoCore.withSessionUnlessBusy`：gate 用新增的 `AcquireUnlessBusy`，回合占用时返回忙，空闲整理占用时抢占后等待；预览不加锁（S18、F28）；<br>• 文件部分改用内部按项结构，携带 `restoreItem` 所需的 Meta 与 RootStatus（S19、F25）；<br>• 去掉屏蔽区间缓存（S20、F29）；<br>• undo 通知注入改为每回合取空队列（F26）；<br>• recall 窗口读法带 types，续读 ref 取多读一条的 seq（F27）；<br>• meta 白名单按真实键名；files scope 只写一条事件；`cfg.Tasks` 判空（F30、F31） |
| 0.5 | 2026-10-31 | Claude | 收口计划评审第 4 轮（两轴 PASS）的 CORE_CORRECTIVE：S22–S23、F32–F34，并处理 S24、F35–F38。<br>• 撤回 undo 通知取空队列的例外，改为 rewind 通知插入队首，没有回退记录的会话行为不变（S22）；<br>• `AcquireUnlessBusy`、`withSessionUnlessBusy` 移到 T4，T6 依赖 T4（S23、F34）；<br>• fork 的 origin 按设计取 chat（F32）；<br>• recall 只在 `last.Seq+1` 落在区间内时才换续读取法（F33）；<br>• title 后缀放 `personal.ini`（F34）；<br>• T4 正文与签名对齐（S24、F35）；补桩、`runner.go`、`UndoResult` 计数映射（F36–F38）；<br>• CHANGELOG 用 `Session.Card` 并补 `Run`/`Compact`（S17 残留） |
| 0.6 | 2026-10-31 | Claude | 收口计划评审第 5 轮（两轴 PASS）的 CORE_CORRECTIVE：S25–S26、F39–F40，并处理 S27、F41。<br>• 错误值 `ErrRewindBusy` 归 T4、`ErrRewindStale` 归 T5（S25）；<br>• undo 队列项加 `kind`（omitempty）：新 rewind 通知取代未送达的旧 rewind 通知；rewind 通知注入失败时放回队首；undo 项的取放不变（S26、F39、F41）；<br>• files scope 的计数与入队条件对齐 `:undo`（F40）；<br>• 偏差清单、批准证据、T2 文件清单、列表结构（S27、F41） |
| 0.7 | 2026-10-31 | Claude | 收口计划评审第 6 轮（两轴 PASS）的 CORE_CORRECTIVE：S28/F42（同一问题），并处理 S29–S30、F43。<br>• files scope 的 Turns 与入队条件逐字沿用 `:undo`；Turns=0 时改写一条 rewind 会话事件，保证一次操作一条事件；<br>• 被取代的旧 rewind 通知的文件事实并入新通知；skip 结果的 Action 与 `UndoResult.Files` 写明；<br>• 列表位置与文件清单残留 |
| 0.8 | 2026-10-31 | Claude | 收口计划评审第 7 轮（两轴 PASS）的 CORE_CORRECTIVE：S31/F44、S32，并处理 S33。<br>• 被取代的旧 rewind 通知改为正文原样前置拼接，不再合并清单（S31、F44）；<br>• 全部冲突用例只断言跳过计数，不改 undo 文案（S32）；<br>• 写明不可恢复夹具（S33） |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 计划工作声明

- 思考模式：`RIGOROUS`（SR1408）。
- 核心目标：实现设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2，即 `:rewind N [files|conversation|both]` 与 `:fork N`，并满足 suga-4tnd 的验收：三种 scope、fork 后原会话不变、有 e2e 与单测。
- Scope freeze：
  - 只改 suga 仓库（Git root `inhere-tools/suga`，分支 `main`）的 `internal/personal/**`、`internal/personal/store/**`、`internal/chat/**`、`internal/i18n/catalog/**`、`test/ptye2e/**`，以及 CHANGELOG、README 中的 chat 说明。
  - 不改 CLI 子命令、ACP、sugasdk 公开 API、wiring、DDL。
  - sugasdk 的公开 API 不变。但在**有回退记录的会话**上，经 sugasdk 调用的 `Run`、`Compact`、`Session.Card` 会反映回退后的可见状态（`pkg/sugasdk/session.go:464` 等调用 personal 的同一批读者）。这是设计 R2 的直接结果，T8 在 CHANGELOG 中写明。
  - sugasdk 的 `Entries` 与搜索保持原样；没有回退记录的会话，所有行为不变。
- 暴露度：会话数据是用户资产，不属低暴露（SR1409）。计划评审采用两轴独立评审，严谨模式最多 8 轮；无开放 `CORE_BLOCKING` 即停止。
- 停止条件：两轴都 PASS 后交用户批准；实施中出现 Semantic Amendment 或 Ownership Conflict 即停。

## 目标与完成定义

1. `:rewind N conversation`：下一轮发给模型的请求里，不再包含第 N 轮及之后被屏蔽的任何内容，回退早于 compaction 或 clear 时也一样；交接包与 handoff 中不出现被回退的提问或 plan。
2. `:rewind N files`：从最新一轮逆序到第 N 轮，恢复每一轮的快照。快照已被清理的轮列为不可恢复；冲突（含级联冲突）跳过并列出。执行前先展示回退计划，用户确认后才执行。
3. `:rewind N`（both）：同时满足 1 和 2，并按设计流程 3 注入通知。
4. `:fork N`：生成新会话，可以续聊，其投影历史等于源会话第 N 轮之前的可见历史；源会话的 entries、turns、meta 不变。
5. 序号只追加的承诺不变。旧条目保留，trace 照常显示。
6. R11：模型侧的 `session_search` 与 recall 在任何作用域下都不返回**当前会话**的被屏蔽条目。其他会话的条目照常返回，这是本计划依据设计 R11 字面范围（“当前会话”）作出的决定。sugasdk 与 CLI 的检索行为不变。
7. 设计评审遗留项与计划评审第 1 轮的发现都落到具体任务，见可追溯性。
8. 完成 Gate 全部通过。

## 范围、排除项与授权

- 范围：见计划工作声明的 scope freeze。
- 排除（设计 R9）：
  - summarize-from-here、redo；
  - CLI、ACP、sugasdk API；
  - trace 与 transcript 中的“已回退”标注；
  - 把第 N 轮提问回填到输入行；
  - 延长快照保留期。
- 排除（本计划依据设计 R11 字面范围的决定）：跨会话检索时排除其他会话的屏蔽区间。
- 授权：
  - 计划批准只授权在 `inhere-tools/suga/.worktrees/` 下按任务实施并本地提交（SR1211）；
  - 合入 main 由主会话审核后执行；
  - 发版 v0.54.0、push、主机安装都走外部动作 Gate。
- `host_or_non_offline_action=REQUIRED`（Windows 主机经 gofer exec job 跑 `go test` 与 Go ptye2e；只用工作区 `tmp/` 下的临时 SUGA_HOME）

## 输入与批准证据

- 设计：`docs/design/2026-10-31-session-rewind-fork-design.md` 0.2，用户 2026-10-31 批准，原话：“批准 rewind 设计 0.2（含 R11、R12）”。
- 设计评审：`docs/review/2026-10-31-session-rewind-fork/`（rounds 1–2，PASS，`cff054ce`）。
- 计划评审：`docs/review/2026-10-31-session-rewind-fork-plan/`。
  - round 1：`a59b27d0`；
  - round 2：`71a0b184`；
  - round 3：`a3e9eb93`，两轴 PASS，CORE_CORRECTIVE 在 0.4 收口；
  - round 4：`f009fd02`，评审 0.4，两轴 PASS，CORE_CORRECTIVE 在 0.5 收口；
  - round 5：`67fd3f0c`，评审 0.5，两轴 PASS，CORE_CORRECTIVE 在 0.6 收口；
  - round 6：`8b0e8bf7`，评审 0.6，两轴 PASS，CORE_CORRECTIVE 在 0.7 收口；
  - round 7：`351c4d4b`，评审 0.7，两轴 PASS，CORE_CORRECTIVE 在 0.8 收口。
- issue：suga-4tnd。gofer plan `plan-20261031-211227-3fa910d5`（suga 0.54）。
- 本计划与设计正文的偏差都来自评审纠正项，偏差处标注了依据：
  - T6 的 fork 写入方式（F-N1、计划 F2）；
  - T5 的通知处理：设计写“undo 通知丢弃并列出”，本计划改为 undo 通知保留、rewind 通知插入队首、分轮送达（F-N4、S22、S26）；
  - rewind 入口挂在 UndoCore 适配层（计划 F1）；
  - recall 续读 ref 在落入屏蔽区间时改取下一条可见条目（计划 F18、F33）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 文件按轮恢复 | `undo_restore.go` restoreItem/contentConflict、`undo_options.go` UndoWithOptions、`undo.go:190` checkpointsLocked | 部分：UndoWithOptions 自取 gate，不可重入 | 在 UndoCore 上新增不加锁的选择与执行，复用 checkpointsLocked 与 restoreItem | OWNER_EXTENSION | gate 不可重入（`gate.go:115-140`） | 与 undo 共用同一套恢复逻辑 |
| CAP-02 | 屏蔽区间读取 | `store/entries.go` LatestEntry、RecentEntries、CountEntriesByType；`fts_snapshot.go` SearchEntriesPage | 否：没有跳过区间的读取，FTS 不能排除区间 | 在 store 中新增区间感知的读取方法与检索选项 | OWNER_EXTENSION | 计划 F5、F6 | 方法归 store 所有 |
| CAP-03 | fork 原子写入 | CreateSession、CreateTurn、AppendEntries（各自独立事务） | 否：没有跨调用事务 | 新增 `CreateForkedSession`，内部复用 AppendEntries 的 FTS 与正文外置写入 | OWNER_EXTENSION | F-N1、计划 F3 | 抽出 AppendEntries 的事务内部分共用，不复制 |
| CAP-04 | 后台任务通知丢弃 | `pkg/agentcore/tasks` TakeNotices；`personal/tasks.go` TaskNotices | 是 | 直接调用 | DIRECT_REUSE | none | 无 |
| CAP-05 | 运行中后台任务判定 | `tasks.Manager.List`；personal `TaskNotices` 接口 | 部分：Manager 有 List，接口没有 | 在 `TaskNotices` 接口加 `List(sessionID) []tasks.Info`，Manager 已实现 | OWNER_EXTENSION | 计划 F8 | 只扩接口，无新实现 |
| CAP-06 | 回退计划预览 | `chat_undo.go:107,170` 两段式（先选、再执行） | 部分：结构可借鉴 | 新增 `RewindPlan` 与 `ApplyRewind`，结构对齐 undo | OWNER_EXTENSION | 计划 F1、S2 | 与 undo 并列，不改 undo |
| CAP-07 | 写调用事件读取 | `store/events.go:40` ListEvents（`observe.go` 写入的 tool 事件） | 是 | 直接调用 | DIRECT_REUSE | none | 无 |
| CAP-08 | fork 附件复制与失败清理 | 图片与 spill 路径（`ledger_test.go:59` 的 spill 夹具）；`DeleteSession`、`DeleteSessionFiles` | 清理部分直接复用 | 在 personal 包内新增复制函数（文件 `fork_files.go`） | OWNER_EXTENSION | 仓库内没有复制会话附件的实现 | 只被 fork 使用，随 fork 生命周期 |
| CAP-09 | 用户轮索引 | `store.ListTurns`、entry 的 `suga.injected` 元数据 | 部分 | 新增过滤函数 | OWNER_EXTENSION | 没有“用户轮”概念的现成函数 | 只有一份 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 新增独立的 rewind store 包 | CAP-02 | 现有 `internal/personal/store` 已是会话数据的唯一 owner，删除该候选后验收不受影响 |

## 跨任务接口

以下签名是任务之间的约定。实施时只能补充参数注释，改形状要回到计划修订。

```go
// store（T1）
type RewindRecord struct { Seq, CutSeq int64; TargetTurn, Scope string }
func (s *Store) RewindRecords(ctx, sid string) ([]RewindRecord, error)
type SeqRange struct{ Lo, Hi int64 } // 左开右开区间 (CutSeq, Seq)
type SeqRanges []SeqRange             // 已排序、已合并
func (r SeqRanges) Hidden(seq int64) bool
func (s *Store) RecentEntriesSkipping(ctx, sid string, types []string, skip SeqRanges, limit int) ([]Entry, error)
func (s *Store) LatestEntrySkipping(ctx, sid, typ string, skip SeqRanges) (*Entry, error)
func (s *Store) EntriesFromSkipping(ctx, sid string, types []string, fromSeq int64, skip SeqRanges, limit int) ([]Entry, error) // recall 窗口，正序
func (s *Store) CountEntriesByTypeSkipping(ctx, sid, typ string, skip SeqRanges) (int, error)
func (s *Store) CountTurnsSkipping(ctx, sid string, skip SeqRanges) (int, error)
// SearchEntriesPage 增加选项 ExcludeSession{SessionID string; Ranges SeqRanges}；零值不排除（sugasdk、CLI 默认零值）
func (s *Store) CreateForkedSession(ctx, in ForkInput) error   // 一个写事务

// personal：文件部分（T4，UndoCore 上；方法本身不加锁：预览时无锁只读，执行时由 withSessionUnlessBusy 持有 gate 与跨进程锁）
type RewindFileItem struct {
    TurnID string
    Path   string          // 快照条目路径；不可恢复项为 attrs["target"] 原值
    Action RewindFileAction // restore | delete | conflict | unrecoverable
    Reason string          // conflict / unrecoverable 的原因（cleared、no_snapshot、snapshots_off、root_untrusted…）
}
type RewindFileAction string // restore | delete | conflict | unrecoverable
// 内部按项结构：携带 restoreItem 所需的快照条目与根状态（S19、F25）
type rewindFileStep struct {
    View RewindFileItem
    Item CheckpointItem      // 含 SnapshotMeta 与 RootStatus；非 trusted 映射为 unrecoverable/root_untrusted
    // Action 为 unrecoverable 的项没有快照，不送进 restoreItem（F35）
}
func (u *UndoCore) planRewindFiles(ctx, sid, toTurnID string) ([]rewindFileStep, error)
func (u *UndoCore) applyRewindFiles(ctx, sid string, steps []rewindFileStep) ([]RewindFileItem, error) // 返回实际结果
func (u *UndoCore) withSessionUnlessBusy(ctx, sid string, fn func() error) error // gate 用 AcquireUnlessBusy，再取跨进程锁

// personal：Runner 入口（T3、T5、T6）
type RewindScope string // files | conversation | both
type RewindPlan struct {
    SessionID, TargetTurn string; Scope RewindScope; CutSeq int64
    Files      []RewindFileItem // scope 含 files 时填写
    LatestSeq  int64            // 预览时会话的最新 seq
}
type RewindResult struct {
    Files        []RewindFileItem // 实际恢复、删除、跳过、不可恢复
    Conversation bool             // 是否写入了 rewind 记录
    DroppedTasks []tasks.Info     // 实际取出并丢弃的任务通知
    Notice       string           // 注入给模型的通知正文（为空表示未注入）
}
type ForkResult struct { SessionID string; Turns, Entries int }
// store.ForkInput{Session store.Session; Turns []store.Turn; Entries []store.NewEntry（Seq 与 CreatedAt 已填）}
func (r *Runner) RewindPlan(ctx, snapshots *SessionSnapshots, sessionsRoot, sid, target string, scope RewindScope) (RewindPlan, error)
func (r *Runner) ApplyRewind(ctx, snapshots *SessionSnapshots, sessionsRoot string, plan RewindPlan) (RewindResult, error)
func (r *Runner) Fork(ctx, snapshots *SessionSnapshots, sessionsRoot, sid, target string) (ForkResult, error)
var ErrRewindBusy error  // T4 声明，withSessionUnlessBusy 返回
var ErrRewindStale error // T5 声明
```

- 分工：
  - `maskedRanges`（T2）返回 `store.SeqRanges`，`Hidden` 由 T1 的类型提供。
  - Runner 入口负责目标解析、前置拒绝、任务与 meta；UndoCore 只负责文件。不设 `UndoCore.RewindPlan`（计划 F20）。
- 适配层：Runner 方法与现有 `Runner.Undo` 一样，通过 `r.UndoCore(snapshots, sessionsRoot)` 拿到 UndoCore。
- 恢复：文件恢复走 UndoCore 的 `checkpointsLocked`，以带上 RootStatus（计划 F7）。
- 并发判定（计划 F21）：
  - 新增 gate 方法 `AcquireUnlessBusy(ctx)`，并加入 `UndoGate` 接口（`undo.go:102`），Runner 侧由 `gateForUndo` 提供：
    - 回合持有（非可抢占）时，立即返回 `ErrRewindBusy`；
    - 空闲整理以可抢占方式持有（`compact.go:775`）时，与 `Acquire` 一样触发抢占并等待释放；
    - 空闲时直接取得。
  - `withSessionUnlessBusy` 与 `withSession`（`undo.go:207`）结构相同，只是 gate 换用 `AcquireUnlessBusy`；跨进程锁照旧，`ErrSessionBusy` 映射为 `ErrRewindBusy`。不在已持 gate 的情况下再调用 `withSession`，避免自锁（S18）。
  - 预览（`RewindPlan`）不取 gate 与锁，只读；正确性由执行时的锁内重算保证。
  - ApplyRewind 与 Fork 的全部步骤都在 `withSessionUnlessBusy` 的 fn 内执行；`ErrRewindBusy` 只由它产生。
- 过期判定（计划 F15）：`ApplyRewind` 在锁内重新计算回退计划，包括目标解析、`planRewindFiles` 与 LatestSeq，再与预览逐项比较。任何不同（含 cwd、add_dirs 或快照目录变化导致的条目变化）都返回 `ErrRewindStale`，不执行。

## 前置检查与 fail-closed 条件

- **Workspace baseline**：
  - suga Git root `inhere-tools/suga`，分支 `main`，HEAD `66a3cbc8`（B4 合入之后），工作区干净。
  - 实施开工时以当时的 main HEAD 为准，记入 progress。
  - 设计 0.2 已批准。
  - chat 命令分发在 `chat_commands.go:381`，本计划只在其中新增分支。
- **预期符号**（每个任务开工时 grep 复核）：
  - `context.go:111` loadHistory、`:355` takeTaskNotices；
  - `segment_handoff.go:97` latestPlan、`:112` recentRequests；
  - `handoff.go:124`；
  - `lastreply.go:23` LastReply；
  - `session_card.go:99` SessionCard、`:117` LatestSummary、`:126` SegmentCount、`:129` Turns、`:194` fillCardPlan；
  - `pkg/sugasdk/session.go:464`（session card 调用方）；
  - `undo_notice.go:22` undoNoticeQueueKey、`:181,199` event_id 回填；
  - `gate.go:105` Acquire、`:133` TryAcquire、`:174` TryAcquirePreemptible；`compact.go:775` 空闲整理取 gate；
  - `undo.go:102` UndoGate、`:207` withSession；`undo_notice.go:119` takeUndoNotice；
  - `chat_commands.go:381` 命令分发；
  - `ledger.go:100-135` 检索作用域、`:249` readMessages（recall）；
  - `store/entries.go:24,291,321,358`；
  - `store/fts_snapshot.go:27` SearchEntriesPage；
  - `store/events.go:40` ListEvents；
  - `store/turns.go:30,61,91`；
  - `store/sessions.go:45`；
  - `undo.go:109` UndoCore、`:190` checkpointsLocked、`:347`；
  - `undo_runner.go:8` Runner.UndoCore；
  - `undo_restore.go`、`gate.go:115-140`；
  - `tasks.go:18` TaskNotices；
  - `snapshot_retention.go:620-628`；
  - `observe.go:311-433`（工具事件的 `attrs["target"]`、`attrs["snapshot"]` 与 Status）；
  - `runner_sessions.go:181` writeStartSegment；
  - `chat/chat_undo.go:107,170`；
  - `chat/steering.go:205-212`；
  - `chat/chat_usage.go:103-115`；
  - `pkg/sugasdk/engine.go:296`（SearchEntriesPage 调用方，须保持零值选项）。
- **运行目录与环境**：
  - 容器内命令都在任务 worktree 根目录 `inhere-tools/suga/.worktrees/<任务>` 下执行；Go 1.26，`GOFLAGS=-buildvcs=false`。
  - 主机命令写法：`gofer job run -p my-tools-dev -a exec --runner local --sync --cwd inhere-tools/suga/.worktrees/<任务> -- go test ...`。
  - 容器内 git 只对单条命令前缀 `GIT_DIR/GIT_WORK_TREE`；合并在主机执行，并以 `is_ancestor=0` 为收尾前提。
- **fail-closed**：
  - 需要改 DDL、sugasdk 公开 API、CLI、ACP，或在**没有回退记录的会话**上改变任何行为（含 sugasdk），或改变设计以外的用户可见行为：属 Semantic Amendment，停止。
  - `BenchmarkSessionCard`（含新增的带屏蔽区间变体）比 T1 记录的基线回退超过 20%（`-benchtime=2s`）：停止。
  - 区间排除的检索查询，`EXPLAIN QUERY PLAN` 出现对 entries 的全表扫描：停止。
  - 尾部大屏蔽区间基准（见 T1）的耗时与无屏蔽基准相比超过 2 倍：停止。

**文件拆分理由**：新文件按职责拆分（store 区间读取、fork 写入、屏蔽集合、目标解析、文件回退、执行、通知、fork、附件复制），使每个新文件不超过 400 行（GR104）、每个 go 文件不超过 1000 行（AGENTS.md）。

## 波次与依赖

| 波次 | 任务 | 依赖 | 并行说明 |
|---|---|---|---|
| W0 | T0 测试设施；T1 store 方法 | 无 | 文件不相交 |
| W1 | T2 屏蔽区间集合与读者；T4 文件回退与取锁 | T2 依赖 T1；T4 无 | T4 只按 turn_id 处理 checkpoint，不需要屏蔽集合；文件不相交 |
| W2 | T3 用户轮、目标解析、前置拒绝 | T2 | — |
| W3 | T5 回退预览与执行；T6 fork | T5 依赖 T3、T4；T6 依赖 T1、T2、T3、T4 | 前置拒绝在 T3、取锁在 T4，二者都复用；T5 用 `model.ini`，T6 用 `personal.ini`，文件不相交 |
| W4 | T7 chat 命令 | T5、T6 | — |
| W5 | T8 e2e、文档与收尾 | T0、T7 | — |

- 每个任务一个 worktree 分支，由 sonnet 子 agent 实施。
- 主会话复核（SR1433）：逐条核对、反向验证、grep 调用点、`-count=2`、注释未删、`git diff --stat` 无无关删除、新文件不超过 400 行（GR104）。
- 涉及 chat 的任务（T7、T8），由主会话自己跑 `make smoke-chat` 与 `make smoke-go`，**以冒烟通过为合入前提**。合入命令用 `&&` 串接：冒烟或测试失败时合入不执行。

## 任务

### T0 测试设施：可观察请求与按内容写入

- 文件：`test/ptye2e/fakellm_test.go`；`internal/personal` 中供单测使用的测试辅助（`*_test.go`）
- 动作：
  - fakeLLM 记录每次请求的完整消息列表，包括 role、内容，以及 compaction/handoff 包内的原文。
  - 提供按提问内容触发的写入脚本：用户提问含 `write vK` 时，对指定文件写入 `vK`。不按请求次数触发，避免 compaction 或 handoff 额外请求打乱顺序（计划 F13）。
  - 单测侧提供夹具：构造带多轮、compaction、clear、steer、undo 通知、interrupted 轮的会话。
- 验证：
  - `go vet -tags ptye2e ./test/ptye2e/`；
  - `go test -tags ptye2e -count=1 ./test/ptye2e/`（现有用例不受影响）；
  - `go test -count=1 ./internal/personal/`。
- 完成标准：新增能力有自测；现有用例通过。
- 依赖：无

### T1 store：rewind 类型、区间感知读取、检索选项、fork 原子写入

- 文件：
  - `internal/personal/store/types.go`、`entries.go`、`fts_snapshot.go`、`turns.go`；
  - 新文件 `rewind_store.go`、`fork_store.go`；
  - 对应测试。
- 动作（签名见“跨任务接口”）：
  - `EntryRewind = "rewind"`。
  - `RewindRecords`：rewind 条目在全库中极少，查询方式以 `EXPLAIN QUERY PLAN` 与基准为准选定，结果记入 progress（计划 F5、F19）。备选两种：
    - `idx_entries_type` 上 `type='rewind'` 再按会话过滤；
    - 主键范围扫描。
  - `SeqRanges` 及 `Hidden`：区间排序合并后二分查找。
  - `RecentEntriesSkipping`、`LatestEntrySkipping`、`EntriesFromSkipping`：把 skip 的补集拆成若干可见段，每段一条 `seq` 范围查询，按方向依次查，取够 limit 即停（计划 F19）。
    - 不在 SQL 里用 `NOT (seq…)` 逐行过滤：那样遇到尾部大区间时要逐行走过被屏蔽的条目；
    - 也不在 Go 里先读后补。
  - `CountEntriesByTypeSkipping`、`CountTurnsSkipping`：供 session card 使用（计划 F6）。
    - turn 被屏蔽的判定：该轮的全部 entry 都在区间内。
    - 没有 entry 的轮按原样计数。
  - `SearchEntriesPage` 新增 `ExcludeSession` 选项：
    - 只排除指定会话在区间内的行；
    - 零值行为与现在逐字一致；
    - 选项进入分页 fingerprint，cursor 不跨选项复用（计划 F5）。
  - `CreateForkedSession(ForkInput{Session, Turns, Entries})`：
    - 一个写事务内写入 session、turns、entries；
    - entries 的 seq 由调用方按 1..n 预先给定，事务内校验连续；
    - 写入复用 AppendEntries 的事务内部分（抽出共用函数），包括 FTS 插入、大正文外置文件、原样保留 `created_at`（计划 F3）；
    - 任何一步失败整体回滚，外置正文文件一并丢弃。
- 验证：
  - 在 worktree 根目录运行 `go test -race -count=1 ./internal/personal/store/`；
  - 新用例覆盖：
    - 区间边界（cut 与 rewind 记录自身）；
    - 区间排除后的多页分页、cursor 连续、换选项后 cursor 失效；
    - 零值选项结果与改动前一致（用固定夹具对比）；
    - fork 写入中途注入失败后无残留（行与外置文件）；
    - fork 条目能被 FTS 检索到。
  - `EXPLAIN QUERY PLAN` 断言：Skipping 方法与区间检索走主键或 FTS，不全表扫描。
  - 反向验证：
    - 去掉区间排除，分页用例失败；
    - 去掉 fork 的 FTS 写入，检索用例失败。
  - 基准：
    - `go test -run '^$' -bench BenchmarkSessionCard -benchtime=2s ./internal/personal/`，作为基线记入 progress；
    - 新增 `BenchmarkRecentEntriesSkipping`：会话 2 万条，屏蔽最后 1 万条，耗时与无屏蔽相比不超过 2 倍。
- 完成标准：用例与反向验证通过；基线已记录。
- 依赖：无

### T2 屏蔽区间集合与读者接入

- 文件：`internal/personal/visible.go`（新）、`context.go`、`segment_history.go`、`segment_handoff.go`、`handoff.go`、`lastreply.go`、`session_card.go`、`ledger.go`、`runner.go`（Ledger 绑定处 `:444` 注入 `maskedRanges`）及测试
- 动作：
  - `maskedRanges(ctx, sid) (store.SeqRanges, error)`：由 `RewindRecords` 构造区间并集。
    - 不做缓存（S20、F29）：每个读者调用一次 `RewindRecords`，代价由 T1 选定的查询方式与 T2 的基准把关；基准不达标时按 fail-closed 停止并回到计划修订，不在实施中临时加缓存。
  - 接入设计 0.2 读者表中的全部读者：
    - `loadHistory`：Types 白名单加入 `EntryRewind`，扫描时把它当作区间记录；
    - `SegmentHistory`、`latestPlan`、`recentRequests`、`buildHandoff`；
    - `LastReply`：跳过被屏蔽的轮。回退后最新可见轮如果是正常结束的，不得误报“上一回合未正常结束”（计划 F11）。
  - session card：
    - LatestSummary（`:117`）改用 `RecentEntriesSkipping`（计划 F17）；
    - `fillCardPlan`（`:194`）改用 `LatestEntrySkipping`；
    - SegmentCount 改用 `CountEntriesByTypeSkipping`；
    - Turns 改用 `CountTurnsSkipping`（F-N3）。
  - 检索（R11，计划 S3、F4）：
    - `session_search` 在任何作用域下都传入 `ExcludeSession{当前会话, 当前会话区间}`；
    - recall 的 `readMessages`（`ledger.go:249`）改用 `EntriesFromSkipping`，types 传原有的 `ledgerMessageTypes`，跳过当前会话区间内的条目（计划 F18、F27）。
      - 续读 ref 仍按现有取法（上一窗口最后一条 seq+1）；只有它落在屏蔽区间内时，才改为区间之后第一条可见条目的 seq（读窗口时多读一条取得）。没有回退记录的会话上，ref 与现在逐字相同（F33）；
      - Ledger 通过 Runner 注入的 `maskedRanges` 取得当前会话区间；
      - ref 指向被屏蔽条目时，一律按“不存在”处理。
  - 在 `visible.go` 的注释与 progress 中写明“不受影响的读者”及理由（S-N1、计划 F10）：
    - 按单轮读取：`compact.go:176`、`replay.go:233`、`admit.go:165`、`steer.go:418`；
    - `:checkpoints`/`:undo`（`undo.go:347` ListTurns）按磁盘事实计数；
    - `segment_clear.go:100` 的 LastEntrySeq 不读内容；
    - sugasdk `Entries` 与 `engine.go:296` 搜索、CLI 的检索与 trace：按 R9 保持原样。
- 验证：
  - `go test -race -count=1 ./internal/personal/`，新用例：
    - 回退早于 compaction 与早于 clear；
    - 嵌套 rewind；
    - rewind 后再 compaction，交接包不含被回退的提问与 plan；
    - interrupted 轮、context_update；
    - LastReply 不误报；
    - session card 四个字段（LatestSummary、plan、SegmentCount、Turns）；
    - 检索：当前会话、workspace、all 三种作用域都不返回当前会话的屏蔽条目，其他会话照常返回；
    - recall：ref 在区间内、窗口跨越区间、上一窗口末尾紧挨区间时续读 ref 指向区间后第一条可见条目；没有回退记录的会话，续读 ref 与改动前逐字相同；
    - 另一个 Store 实例写入 rewind 记录后，本 Runner 的读者立即看到新区间。
  - 反向验证：
    - 去掉 `recentRequests` 的屏蔽，交接包用例失败；
    - 去掉 loadHistory 的屏蔽，上下文用例失败；
    - 去掉 readMessages 的屏蔽，recall 用例失败。
  - 基准：`BenchmarkSessionCard` 及新增的带 3 个屏蔽区间的变体，都与 T1 基线对比，回退不超过 20%。
- 完成标准：用例、反向验证与基准都通过。
- 依赖：T1

### T3 用户轮、目标解析与前置拒绝

- 文件：`internal/personal/rewind_target.go`（新）、`tasks.go`（只扩接口）及测试
- 动作：
  - `userTurns(ctx, sid)`：按 `ListTurns` 顺序，排除以下轮：
    - 请求 user entry 带 `suga.injected` 元数据的；
    - 没有 entry 的；
    - 已被屏蔽的（用 T2 的集合）。
  - `Hops>0` 的轮与 `suga send` 发起的轮：实施前核实它们是否带 Injected 标记，结果写入 progress。没有标记的按用户轮处理。
  - `resolveTarget(sid, arg)`：参数可以是序号或 `turn_id`；`turn_id` 须属于该会话、不在屏蔽区间内、且是用户轮（SR1301）。
  - `cutFor(turn)`：取该轮首条 entry 的 seq 减 1。
  - 前置拒绝 `rewindPreconditions(ctx, sid)`，rewind 与 fork 共用，在 `withSessionUnlessBusy` 的 fn 内调用（回合进行中已由 gate 返回 `ErrRewindBusy`）：
    - 会话已归档；
    - 有运行中的后台任务：`TaskNotices` 接口加 `List(sessionID) []tasks.Info`，判断其中 Status 为 running（计划 F8）；`cfg.Tasks` 为 nil 时视为没有任务（F31）。
- 验证：
  - `go test -race -count=1 -run 'UserTurn|Cut|ResolveTarget|RewindPrecond' ./internal/personal/`，覆盖：
    - injected、nudge、空轮、steer；
    - 跨会话 `turn_id`、已屏蔽轮；
    - 两种前置拒绝（会话已归档、有运行中的后台任务；回合进行中由 T4 的取锁负责）。
  - 反向验证：去掉 turn_id 的会话归属校验，跨会话用例失败（计划 S7）。
- 完成标准：用例与反向验证通过。
- 依赖：T2

### T4 文件回退、清理检测与取锁

- 文件：`internal/personal/rewind_files.go`（新，声明 `ErrRewindBusy`）、`gate.go`（`AcquireUnlessBusy`）、`undo.go`（`UndoGate` 接口与 `withSessionUnlessBusy`）、`undo_runner.go`（`gateForUndo` 适配）、测试桩 `undoTestGate` 补方法（F37）及测试
- 动作：
  - 取锁（S23）：按跨任务接口实现 `AcquireUnlessBusy` 与 `withSessionUnlessBusy`；用例覆盖回合持有返回忙、空闲整理被抢占、空闲直接取得，以及 CLI 侧 Gate 为 nil。
  - UndoCore 上新增两个文件方法（签名见跨任务接口），方法本身不加锁：预览时无锁只读，执行时调用方在 `withSessionUnlessBusy` 内调用：
    - `planRewindFiles(ctx, sid, toTurnID)`：用 `checkpointsLocked` 取行（带 RootStatus，计划 F7），按时间逆序列出从最新一轮到 toTurn 的恢复、删除、冲突（含级联）与不可恢复项。被屏蔽的轮也计入；已 undone 的轮跳过；partial 的轮重跑。
    - `applyRewindFiles(ctx, sid, steps)`：只接受调用方在锁内刚算出的 steps，对非 unrecoverable 项循环调用 `restoreItem`；只对快照目录存在的轮调用 `SetCheckpointState`（F35）；返回实际结果。`OnFilesRestored` 由 Runner 适配层调用。
  - 清理检测（F-N2）：
    - 目标区间内的每一轮，用 ListEvents 查 `Kind=tool`、`Status=ok`、`Name ∈ {file_write, file_edit, file_remove}` 的事件；有这种事件但没有快照目录，就判定为快照已清理。
    - 列出 `attrs["target"]` 的原始值（数组逐项列出），注明可能是相对 cwd 的路径或目录。
    - 有 `attrs["snapshot"]` 时，用它区分“已清理”与“未建快照”（计划 F12）。
  - 快照功能整体关闭时，所有写调用都列为不可恢复。
- 验证：
  - `go test -race -count=1 -run 'RewindFiles|Cleared' ./internal/personal/`，覆盖：
    - 多轮逆序恢复到第 N 轮；
    - 冲突与级联冲突；
    - undone 与 partial；
    - RootStatus 非 trusted 的轮；
    - 清理检测只统计 ok 事件；
    - 快照关闭。
  - 反向验证：
    - 清理检测改为计入 error 事件，用例失败；
    - 改为不经 checkpointsLocked 取行，RootStatus 用例失败。
- 完成标准：用例与反向验证通过。
- 依赖：无

### T5 回退预览与执行

- 文件：`internal/personal/rewind.go`（新）、`rewind_notice.go`（新）、`undo_runner.go`（只加 Rewind 适配方法）、`undo_notice.go`（队首插入）、`internal/i18n/catalog/{zh,en}/model.ini` 及测试
- 动作：
  - `RewindPlan`（预览，只读）：
    - 解析目标（T3）；
    - scope 含 files 时调用 T4 的 `planRewindFiles`；
    - 记录 LatestSeq。
    - 不预列任务通知：`pkg/agentcore/tasks` 没有只读取得待注入通知的方法，本计划不扩 agentcore（计划 S13、F16）。chat 预览给出一般提示“已结束但未送达的后台任务通知将被丢弃”，实际丢弃项在执行结果中列出。
  - `ApplyRewind`（执行），全部步骤在 `withSessionUnlessBusy` 内：
    1. 重跑前置拒绝；在锁内重算回退计划并与预览逐项比较，不同则返回 `ErrRewindStale`（计划 F15）。
    2. scope 含 files 时，用锁内重算的 `rewindFileStep` 调用 `applyRewindFiles`。
    3. **以下 4–6 步只在 scope 含 conversation 时执行**（设计“会话部分（scope 包含 conversation）”，计划 S11）：
    4. 追加 `rewind` entry `{cut_seq, target_turn, scope, files}`。
    5. 处理 meta：
       - 重置 `fold_boundary=0`、`fold_active=false`；
       - `loaded_tools` 保留（R12）；
       - 调用 `r.rec.prefix.clearRequest`。
    6. 任务通知（`cfg.Tasks` 非 nil 时）：`TakeNotices` 取出已结束的任务通知后丢弃，并写入 `RewindResult.DroppedTasks`。gate 内与 `takeTaskNotices`（`context.go:355`，只在持 gate 的回合中运行）互斥，没有竞争（计划 F8）。
    7. 事件：scope 含 conversation 时写一条 `session` 事件，name 为 `rewind`，attrs 含目标轮、scope、文件统计。只回文件时按下方 Turns 规则：Turns>0 只沿用 `:undo` 的 EventUndo 与通知记录；Turns=0 只写一条 `session` 事件。两种情况都保证一次操作一条事件（F31）。EventUndo 与 undo 通知所需的 `UndoResult` 由结果映射（F38、F40）：
       - restore→Restored、delete→Deleted；
       - conflict 与 `restoreItem` 返回的跳过→Skipped，`RewindFileItem.Action` 记为 `conflict`，Reason 取跳过原因；
       - unrecoverable→Unrecoverable，通知照 `:undo` 提示“N 个文件无法恢复”；
       - Files 为实际恢复与删除的路径；
       - Turns 与 `:undo` 定义相同：目标范围内有快照目录的轮数（`undo_restore.go:62-67`）；
       - Turns>0 时调用与 `:undo` 相同的 `enqueueUndoNotice`（写 EventUndo 并入队，`undo_restore.go:105`），全部冲突时也照此通知；
       - Turns=0（全部无快照）时磁盘没有变化，不入队通知，改写一条 `session` 事件（name 为 `rewind`），保证一次操作一条事件（S28、F42）。
    8. 注入通知，按设计流程 3：
       - **只回文件**：沿用现有 undo 文案与 `suga.undo.pending` 队列（`undo_notice.go:22`），与 `:undo` 的通知方式一致。
       - **含会话**：本次通知**插入队列首位**，已在队列中的 undo 通知保留不删，在之后的回合依次送达（F-N4、S22）。队列中各项沿用现有的 `event_id` 回填（`undo_notice.go:182,199`），不改回填逻辑（计划 F23）。
       - 队列项 `pendingUndoNotice` 加字段 `Kind`（`json:"kind,omitempty"`，rewind 通知为 `rewind`，undo 通知为空，旧数据兼容）（F39、S26）：
         - 入队新的 rewind 通知时，先移除队列中尚未送达的 rewind 项（连续两次 rewind 时只送最新状态）；被移除项的正文 `Text` 原样放在新通知正文之前（中间空一行），两次操作的事实按先后顺序在同一条注入中送达；计数、路径等结构字段只取新项，不合并，也不新增字段（S31、F44）；被移除项的事件 `entry_seq` 不再回填，事件本身保留（S29）；
         - rewind 项注入失败时放回**队首**（`undo_notice.go:169-178` 的放回逻辑按 Kind 分支）；undo 项仍放回队尾，行为不变；
         - 没有回退记录的会话里队列不出现 rewind 项，行为与现在相同。
       - 现有 `takeUndoNotice`（`undo_notice.go:119`）每回合取队首一项，取法不改。rewind 通知在队首，因此总在下一轮送达（R5）；没有回退记录的会话，队列行为与现在完全相同（S22）。
       - 新增通知文案进 `model.ini`，zh 与 en 各一份。
    9. 按设计流程 1 第 4 步的失败处理表，实现各分支的报告与 Warn 日志（GR109）。
- 验证：
  - `go test -race -count=1 -run 'Rewind' ./internal/personal/`，覆盖：
    - 三种 scope 的结果；**files scope 不写 rewind 记录、不改 meta、不丢任务通知，下一轮请求历史不变**；
    - 过期：预览后追加 entry、预览后删除快照目录、预览后改 cwd，三种都返回 `ErrRewindStale`；
    - 回合进行中返回 `ErrRewindBusy`，不等待；空闲整理持有 gate 时被抢占，rewind 成功；
    - 先 `:undo` 再 `:rewind`（中间不发消息）：下一轮送达 rewind 通知，再下一轮送达 undo 通知，event_id 各自回填；`TestUndoNoticeQueueKeepsOrder` 保持通过；
    - 连续两次含会话的 rewind（第一次为 both 且恢复了文件）：下一轮只有一条注入，正文依次含第一次与第二次的通知文本，第一次的已恢复事实可见；
    - rewind 通知注入失败：放回队首，下一轮仍先送达；
    - files scope 有不可恢复项（夹具：快照 meta 标为 purged，见 `undo.go:449`）：通知含“无法恢复”提示，计数与 `:undo` 一致；
    - files scope 全部冲突：Turns>0，写 EventUndo 并入队，通知含跳过计数（沿用 `:undo` 现有文案，不改文案）；
    - files scope 全部无快照：Turns=0，只写一条 rewind 会话事件，不入队；
    - 四种失败分支（注入错误）；
    - fold 两个字段、前缀缓存被清；
    - 任务通知被丢弃并列在结果中；
    - 队列中已有的 undo 通知保留，event_id 回填正确；
    - 事件属性。
  - 反向验证：
    - 去掉 scope 条件，files scope 用例失败（计划 S11）；
    - 改成直接丢弃 undo pending，保留用例失败（计划 S7）；
    - 去掉锁内重算，过期用例失败。
- 完成标准：用例与反向验证通过。
- 依赖：T3、T4

### T6 fork

- 文件：`internal/personal/fork.go`（新）、`fork_files.go`（新）、`internal/i18n/catalog/{zh,en}/personal.ini`（title 后缀）及测试
- 动作：
  - `Runner.Fork`，在源会话的 `withSessionUnlessBusy` 内执行，先跑 T3 的前置拒绝。
  - 收集与映射：
    - 收集源会话 cut 之前的可见轮与条目，`HydrateEntries` 取回正文；
    - 丢弃 `rewind` 记录本身（新会话不需要屏蔽，原样复制会让 cut_seq 指错，计划 F2）。
  - **在事务前算好全部映射**（计划 F2）：
    - 新 seq 为 1..n；
    - compaction 的 `first_kept_seq` 映射为“不小于它的第一条已复制条目”的新 seq；
    - clear 记录的 K 映射为它自身的新 seq；
    - 每轮生成新 turn_id；
    - 写好的 payload 一次交给 `CreateForkedSession`。
  - meta 复制规则**默认拒绝**（计划 F22）：只复制白名单中的键，白名单列在 `fork.go` 注释中，测试逐项断言。
    - 复制：cwd（sessions 表列）、`add_dirs`（`:rw` 的 meta 键）、model 与 provider、`loaded_tools`（与 R12 一致）；
    - 改写：title 加 i18n 后缀（zh“（分叉）”，en “(fork)”，放 `personal.ini`）；
    - origin 取 `store.OriginChat`（设计 0.2 第 129 行，F32）；新增 `forked_from`、`forked_at_turn`；
    - 其余一律不复制。实施时 grep 全部 meta 键，在 progress 中列出未复制的键及理由。已知未复制的有：fold 字段、segment 与前缀缓存键、pin、子会话关系、`suga.undo.pending`、`short_name`。
  - 附件：
    - `fork_files.go` 复制图片与 spill 文件，并改写正文中的 `out:<源会话>:` 前缀；
    - compaction 的 Refs 保留不改（R6）；
    - 附件在事务前复制到新会话目录；事务或复制失败时执行 `DeleteSession` 加 `DeleteSessionFiles`。
  - 继承 taint：`tainted_at` 不晚于 cut 条目的 `created_at` 时，复制 `tainted_at` 与 `tainted_by`。
  - 写 `session` 事件，name 为 `fork`。
  - 不调用 `insertSession`/`writeStartSegment`。
- 验证：
  - `go test -race -count=1 -run Fork ./internal/personal/`，覆盖：
    - 新会话 loadHistory 的投影消息，等于源会话**在 fork 前**于同一 cut 处 rewind conversation 后的投影消息（逐条比较 role 与内容；计划 F13 的对照基准）；
    - interrupted 轮的状态被保留；
    - compaction 早于 cut 与跨越 cut；
    - 图片与 spill 可读；
    - 源会话 entries、turns、meta 前后的哈希不变；
    - meta 白名单；
    - taint 的继承与不继承；
    - 事务失败与附件复制失败后无残留。
  - 反向验证：
    - 不写入 turns，loadHistory 用例失败；
    - 不映射 `first_kept_seq`，compaction 用例失败。
- 完成标准：用例与反向验证通过。
- 依赖：T1、T2、T3、T4

### T7 chat 命令

- 文件：`internal/chat/chat_rewind.go`（新）、`chat_usage.go`、`chat_commands.go`、`steering.go`、`internal/i18n/catalog/{zh,en}/chat.ini`
- 动作：
  - 注册 `:rewind`（无参数时列出用户轮）与 `:fork N`。
  - 加入 `steering.go` 的回合中放回判断，与 `:undo` 一致。
  - `:rewind` 流程与 `:undo` 对齐（`chat_undo.go:107,170`）：
    - 先调用 `RewindPlan` 展示计划：文件清单，以及 scope 含 conversation 时关于任务通知会被丢弃的一般提示；
    - 用 `chat.undo_confirm` 确认后调用 `ApplyRewind`，再展示 `RewindResult`，包括实际丢弃的任务通知；
    - 无 TTY 时取消；
    - `ErrRewindStale` 时提示重新执行；`ErrRewindBusy` 时提示等回合结束。
  - fork 完成后打印新会话 ID 与 `suga chat --resume <id>`，当前 chat 不切换（R7）。
  - 所有文案 zh 与 en 各一份。
- 验证：
  - `go test -race -count=1 ./internal/chat/`；
  - 主会话跑 `make smoke-chat` 与 `make smoke-go`。
- 完成标准：单测与冒烟通过。
- 依赖：T5、T6

### T8 e2e、文档与收尾

- 文件：`test/ptye2e/chat_rewind_test.go`（新）、CHANGELOG、README 的 chat 命令说明
- 动作：
  - Go ptye2e 用例，按设计“验收”逐项：
    - **三种 scope**：用 T0 的按内容写入跑 3 轮（文件依次为 v1、v2、v3），`:rewind 2` 分别执行 files、conversation、both，判据（计划 F24）：
      - files：文件为 v1；下一轮请求仍含第 2、3 轮提问；
      - conversation：文件仍为 v3；下一轮请求不含第 2、3 轮提问，含“磁盘未回退”通知；
      - both：文件为 v1；请求不含第 2、3 轮提问；
    - **回退早于 `:compact`**：断言交接包与请求里都不含被回退的提问；
    - **快照已清理**：缩短保留期（只在测试 home 中设置），断言列出不可恢复项；
    - **fork**：fork 出新会话后 `--resume` 续聊，新会话请求等于源会话第 N 轮之前的可见历史，源会话不变；
    - **回合中输入**：回合进行中输入 `:rewind` 被放回。
  - CHANGELOG 写明：
    - `sugasdk.Entries` 会返回新类型 `rewind`；
    - sugasdk 在有回退记录的会话上：`Session.Card` 的 LatestSummary、plan、SegmentCount、Turns 都不计被屏蔽部分；`Run` 与 `Compact` 使用回退后的可见历史（计划 F17、S17）；
    - 回滚到旧版本的语义：两条分支会拼接成一条上下文，旧版本下的 compaction 会把屏蔽区间永久写进摘要，建议回滚前先 fork。
  - README 补充命令说明。
- 验证：
  - Linux：`go test -tags ptye2e -count=1 -run TestChatRewind ./test/ptye2e/`；
  - Windows：`gofer job run ... --cwd inhere-tools/suga/.worktrees/<任务> -- go test -tags ptye2e -count=1 -run TestChatRewind ./test/ptye2e/`；
  - 反向验证，每个场景各做一次（计划 S7）：
    - 三种 scope：去掉 loadHistory 的屏蔽，conversation 失败；跳过 applyRewindFiles，files 失败；
    - compaction：去掉 recentRequests 的屏蔽，交接包断言失败；
    - 清理：关闭清理检测，不可恢复断言失败；
    - fork：Fork 不复制 turns，续聊断言失败；
    - 回合中：去掉放回判断，用例失败。
- 完成标准：用例与反向验证通过，Windows 日志已确认；文档已更新。
- 依赖：T0、T7

## 回滚与恢复

- 每个任务独立分支、独立合并提交，可用 `git revert -m 1 <merge>` 回退。
- 只追加 `rewind` entry，不改 DDL。回滚代码后，旧数据中的 `rewind` 记录会被旧二进制忽略，语义见 T8 的 CHANGELOG 说明。
- 恢复点：每次合入后的 main HEAD 记录到对应的 gofer todo；中断时按 SR1407 写 handoff。
- 测试只用工作区 `tmp/` 下的临时 SUGA_HOME。

## 人工 Gate

1. 本计划批准（用户原话记入状态行）。
2. 当前执行请求（可与批准同句给出）。
3. 外部动作 Gate：发版 v0.54.0、push、主机安装。全部完成后单独请求，逐项列出目标。

## 可追溯性

| 设计 0.2 要求 / 评审项 | 任务 | 验证 |
|---|---|---|
| R1 追加回退记录与屏蔽区间 | T1、T2、T5 | 区间边界、嵌套 rewind |
| R2 读者经过屏蔽 | T2 | 回退早于 compaction 或 clear、交接包、LastReply、session card；反向验证 |
| R3 选轮与校验 | T3 | 用户轮、turn_id 校验；反向验证 |
| R4 部分恢复与清理检测，执行前确认 | T4、T5、T7 | 清理检测（只计 ok 事件）、快照关闭、预览与确认 |
| R5 只回会话时的通知 | T5 | 通知用例 |
| R6 fork | T1、T6 | turns 与 seq 映射、附件、原会话哈希不变、失败无残留 |
| R7 不切换会话 | T7 | 命令输出 |
| R8 前置拒绝与通知丢弃 | T3、T5 | 拒绝与 TakeNotices 用例 |
| R9 范围冻结 | 范围、排除项；T1 零值选项；T2 不受影响读者 | sugasdk 零值结果不变用例；scope freeze 复核 |
| R10 排进 0.54 | gofer plan `plan-20261031-211227-3fa910d5` | 发版 Gate |
| R11 当前会话检索排除 | T1、T2 | 三种作用域、recall 用例 |
| R12 loaded_tools 保留，fold 重置 | T5、T6 | meta 用例、fork 白名单 |
| 设计评审 S-N1、S-N2 | T2 | 不受影响读者清单；检索作用域用例 |
| 设计评审 F11 | T0、T8 | 完整消息断言 |
| 设计评审 F-N1–F-N5 | T1、T6；T4；T2；T5；T1、T2 | 对应用例 |
| 计划 F1、S2 预览与接口 | 跨任务接口、T5、T7 | 锁内重算过期、确认流程 |
| 计划 F2、F3 fork 事务 | T1、T6 | 失败无残留、FTS、compaction 映射 |
| 计划 F4、S4 recall | T2 | recall 用例 |
| 计划 F5、F6、S3 检索与 card | T1、T2 | EXPLAIN、零值一致、基准变体 |
| 计划 F7、F8 | T4、T3、T5 | RootStatus、运行中任务 |
| 计划 F9、S1 依赖 | 波次与依赖 | — |
| 计划 S5–S8、F10–F14 | 前置检查、各任务文件清单、反向验证、运行目录 | — |
| 计划 S11 files scope 不动会话 | T5 | files scope 用例与反向验证 |
| 计划 S12 接口字段 | 跨任务接口 | — |
| 计划 S13、F16 任务通知 | T5、T7 | 结果列出丢弃项 |
| 计划 S14–S16、F24 | 范围与排除项、Capability、前置检查、T8 判据 | — |
| 计划 F15 锁内重算 | 跨任务接口、T5 | 三种过期用例与反向验证 |
| 计划 F17 card LatestSummary | T2、T8 | card 四字段用例、CHANGELOG |
| 计划 F18 recall 续读 | T1、T2 | 续读 ref 指向可见条目的用例 |
| 计划 F19 区间读取性能 | T1、T2 | 尾部大区间基准 |
| 计划 F20–F23 | 跨任务接口、T3、T5、T6 | Busy、meta 默认拒绝、event_id 回填用例 |
| 计划 S17 sugasdk 行为 | 计划工作声明、fail-closed、T8 | 无回退记录会话的零变化用例（T1 零值选项、T2 全部既有用例） |
| 计划 S18、F28 取锁 | 跨任务接口、T4、T6 | Busy 与抢占用例 |
| 计划 S19、F25 文件按项结构 | 跨任务接口、T4 | RootStatus 用例 |
| 计划 S20、F29 去缓存 | T2 | 跨实例可见用例、基准 |
| 计划 F26、S22 通知顺序 | T5 | undo 后 rewind 的通知顺序用例；既有队列测试不改 |
| 计划 S23、F34 取锁归属与并行 | T4、波次表 | Busy、抢占、Gate 为 nil 用例 |
| 计划 F32 origin | T6 | meta 白名单用例 |
| 计划 F33 recall 续读 | T2 | 无回退会话 ref 不变用例 |
| 计划 S25 错误值归属 | 跨任务接口、T4、T5 | 编译 |
| 计划 S26、F39、F41 rewind 通知 | T5 | 连续 rewind、注入失败放回队首用例 |
| 计划 F40 files 计数 | T5 | 不可恢复提示用例 |
| 计划 S28、F42 Turns 与入队 | T5 | 全部冲突、全部无快照用例 |
| 计划 F27 recall | T1、T2 | 续读 ref 用例 |
| 计划 S21、F30、F31 | 跨任务接口、T3、T5、T6 | meta 白名单、单事件、Tasks 为 nil |

## 完成 Gate 与剩余工作

1. suga main（Git root 下运行）：`go vet ./...`、`go test -race -count=1 ./...`、`go test -race -count=2 ./internal/personal/... ./internal/chat/`、`make smoke-chat`、`make smoke-go`。
2. Windows 主机：`gofer job run -p my-tools-dev -a exec --runner local --sync --cwd inhere-tools/suga -- go test -count=1 ./...`，以及同样方式运行 `go test -tags ptye2e -count=1 ./test/ptye2e/`。
3. `BenchmarkSessionCard` 及带屏蔽区间的变体，与基线相比回退不超过 20%。
4. 真实环境（SR1402.1）：用真实模型跑一次 `:rewind 2 conversation`，检查 trace 中下一轮请求不含被回退的内容。做不到时记为 `UNVERIFIED_REAL_ENV: 真实 provider 的 rewind 后请求（复现：真实配置下三轮对话后 :rewind 2 conversation 再发一句，suga inspect trace --json 查看请求）`。
5. 收尾：关闭 suga-4tnd；在 gofer plan 中标记 done；本计划状态改为“已完成”，写不超过 8 行的完成总结。
6. 剩余：
   - summarize-from-here、redo；
   - CLI、ACP、sugasdk；
   - trace 标注；
   - 跨会话检索排除其他会话的屏蔽区间（设计 R9、R11）。
