<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审（Round 4）

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，计划 commit `6e03cbff`）
- 修订版本：Draft 0.4
- 评审范围：变更范围评审，`git diff 19f6f589 6e03cbff -- docs/plans/2026-10-31-session-rewind-fork-plan.md` 的改动，以及这些改动直接依赖的段落（跨任务接口、T4、T5、T6、fail-closed、追溯表）
- 依据：设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准）；本目录第 3 轮可执行轴报告（commit `a3e9eb93`，F25–F28 CORE_CORRECTIVE、F29–F31 ADVISORY）
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，未读 standards-review.md
- 轮次：**Round 4**
- 日期：2026-10-31
- **结论：PASS**（无开放 CORE_BLOCKING）。新增 CORE_CORRECTIVE F32–F34，都是一两句话的修订，建议在 0.5 写入后交用户批准；主会话逐条核对 diff 即可，不需要再开一轮

## 输入与方法

在 HEAD `6e03cbff` 上静态读码，没有运行测试。读过的代码：

- 锁：`gate.go`（gateState、preemptLocked、takeLocked、Acquire、TryAcquire、AcquirePreemptible、TryAcquirePreemptible）、`compact.go:765-790` ConsolidateIdle；`undo.go:100-226`（UndoGate、UndoCore、checkpointsLocked、withSession）、`undo_runner.go`（gateForUndo、undoGate）；`undo_core_test.go:17-60` 测试桩；`internal/cli/undo.go:111-132`（NewUndoCore，不设 Gate）。
- 文件恢复：`undo_restore.go:40-180`（Undo、selectUndoRows、restoreItem）、`undo.go:32` CheckpointItem、`snapshot_retention.go:118` SetCheckpointState。
- 通知：`undo_notice.go` 全文、`runner.go:640-651`、`store/events.go:106` UpdateEventEntrySeq、`undo_notice_test.go:117-136`、`test/ptye2e/chat_undo_test.go`、`test/ptye2e/fakellm_test.go:146-225`。
- recall：`ledger.go:45-135,249-290`、`ledger_test.go:238-260`、`pkg/agentcore/tools/native/ledger.go:82`。
- meta：`adddir.go:25`、`store/session_origin.go`、`personal/session_origin.go`、`cli/sessions.go:31,62,176`、`pkg/sugasdk/engine.go:329-346`、`handoff.go:16`、`runner_sessions.go:171`、`internal/i18n/catalog/zh/`。

## 第 3 轮发现关闭状态

| 项 | 级别 | 0.4 落点 | 状态 | 说明 |
|---|---|---|---|---|
| F25 文件项缺 Meta | CORE_CORRECTIVE | 跨任务接口 `rewindFileStep`、T5 第 2 步 | **CLOSED**（细节见 F35） | `rewindFileStep.Item` 是完整的 `CheckpointItem`（含 Meta、RootStatus、Reason），满足 `restoreItem(ctx, session, sid, turnID, item, force)`（`undo_restore.go:143`）；TurnID 取自 `View.TurnID`，session 在 fn 内 `GetSession` 即可。非 trusted 映射已写明 |
| F26 undo 队列一回合一条 | CORE_CORRECTIVE | T5 第 8 步、fail-closed 例外、T8 CHANGELOG | **CLOSED**（细节见 F36） | 改为每回合取空、各写一条 entry、各自回填 event_id；连续 `:undo` 的时机变化登记为唯一例外并写入 CHANGELOG。可行性核对见下文 |
| F27 recall 类型过滤、续读矛盾 | CORE_CORRECTIVE | 接口 `EntriesFromSkipping` 带 types、T2 recall | **PARTIAL → F33** | types 已加，矛盾已消除（统一“按不存在处理”）。但续读 ref 的新取法在无回退记录的会话上也改变了模型可见文本，撞上 0.4 自己新加的 fail-closed 条款 |
| F28 TryAcquire 被空闲整理拒绝、自锁 | CORE_CORRECTIVE | 跨任务接口“并发判定”、T5、T6 | **CLOSED**（细节见 F37） | `AcquireUnlessBusy` 可在 gateState 上实现；`withSessionUnlessBusy` 内的步骤不再经过 `withSession`，无自锁。可行性核对见下文 |
| F29 缓存键代价 | ADVISORY | T2 | CLOSED | 去掉缓存，每个读者调一次 `RewindRecords`，由基准把关；跨实例用例保留 |
| F30 meta 白名单键名 | ADVISORY | T6 | **PARTIAL → F32、F34** | `add_dirs`、cwd 列已写实。但 origin 选了新值 `fork`，与设计相悖；title 后缀的 i18n 文件与 T5 并行冲突 |
| F31 files scope 事件写法、Tasks 判空 | ADVISORY | T5 第 6–7 步、T3 | CLOSED（细节见 F38） | files scope 只经 `enqueueUndoNotice` 写一条 EventUndo；`cfg.Tasks` 为 nil 视为无任务 |
| F21 遗留（chat 空闲取通知的互斥依据） | ADVISORY | — | 仍 OPEN | 不在本轮 diff 范围，状态不变 |
| F24 遗留（TurnID 为空、fork e2e 基准等） | ADVISORY | — | 仍 PARTIAL | 不在本轮 diff 范围，状态不变 |

### 可行性核对（本轮重点项）

**`AcquireUnlessBusy`（F28）**——可实现，不需要改 gateState 结构：

- 区分持有者的依据已有：`takeLocked(st, false)` 时 `st.preempt == nil`，可抢占持有时 `st.preempt != nil`（`gate.go:82-88`）。
- 实现与 `Acquire`（`gate.go:105-130`）同一循环：`!st.held` 取得；`st.preempt == nil` 返回 `ErrRewindBusy`；否则 `preemptLocked`、`waiters++`、等 `freed` 或 ctx、`waiters--`、`dropLocked`，回到循环开头重读。被抢占释放后若普通 `Acquire` 先拿到 gate，下一轮循环看到非可抢占持有，返回忙，语义正确。
- 被抢占的整理若已过提交点（`compact.go:765-770` 注释：从写入起取消不会撤销），会留下一条 compaction 记录，LatestSeq 变化，ApplyRewind 返回 `ErrRewindStale`；T7 已规定 stale 时提示重新执行，可接受。
- 接口：`UndoGate`（`undo.go:102`）的实现方只有 `undoGate`（`undo_runner.go:51`，需加一个函数字段）和测试桩 `undoTestGate`（`undo_core_test.go:17`，需补方法，否则 `core.Gate = gate` 编译失败，见 F37）。CLI 的 `openUndoCore`（`internal/cli/undo.go:132`）用 `NewUndoCore`，Gate 为 nil，`withSessionUnlessBusy` 照 `withSession` 的写法跳过 gate 即可，不受影响；CLI 也不调用 rewind。
- 自锁：fn 内的步骤是 `resolveTarget`、`planRewindFiles`（只调 `checkpointsLocked`，无锁）、`applyRewindFiles`（只调 `restoreItem` 与 `SetCheckpointState`，无锁）、`enqueueUndoNotice`（无锁）、`TakeNotices`、`PatchSessionMeta`、`prefix.clearRequest`，都不经过 `withSession`/`Gate.Acquire`。chat 持有进程锁，`HeldLock` 为真，跨进程锁不重取。无自锁。

**undo 通知取空队列（F26）**——可行：

- `takeUndoNotice`（`undo_notice.go:119`）改为一次取出全部并清空 meta；`injectUndoNotice`（`:157`）对每项编码，可用一次 `AppendEntries` 写多条（返回顺序对应），失败时整批 `appendUndoNotice` 放回；逐项 `UpdateEventEntrySeq`（`store/events.go:109` 对任意事件通用，rewind 的 session 事件也适用）。
- `runner.go:646` 要从追加一个 ctxItem 改为追加多个。
- 测试影响：`TestUndoNoticeQueueKeepsOrder`（`undo_notice_test.go:117`）逐次调用 `takeUndoNotice`，要改写；ptye2e `chat_undo_test.go:173-186` 有连续两次 `:undo`（冲突跳过后 `:undo force`）中间不发消息，改后“附加”回合会同时收到两条通知。现有断言只检查通知出现过（`:220`）和 EventUndo 存在（`:231`），fakeLLM 取最后一条 user 消息选脚本（`fakellm_test.go:209-216`），注入的通知在真实用户消息之前，不影响脚本选择。两者都不会失败，但前者必须改。

## 新发现

### F32 [CORE_CORRECTIVE] fork 的 origin 取 `fork`，与已批准设计“origin 为 chat”相悖，且未登记为偏差

- 证据：
  - 设计 0.2 第 129 行：“title 加上‘（分叉）’后缀，origin 为 chat。”计划 T6 改为“origin 取值 `fork`”，而“输入与批准证据”的偏差清单（只列 T6 写入方式、T5 通知合并、UndoCore 适配层）没有这一项。第 3 轮 F30 的建议也是沿用 `store.OriginChat`。
  - 用户可见影响：`suga sessions --origin chat`（`cli/sessions.go:176-177`）不再列出分叉会话，`--origin` 帮助文本只列 chat、run、acp、agent（`:62`）；sugasdk `ListSessions` 的 Origin 过滤与 `SessionInfo.Origin`（`pkg/sugasdk/engine.go:338-346`）出现未知值；session card 的 Origin（`session_card.go:109`）同样。gc 保留期按默认分支视为交互会话（`store/session_origin.go:49-55`），这一点无害。
  - 计划自己的 fail-closed 把“改变设计以外的用户可见行为”列为 Semantic Amendment，实施方照计划写会直接触发停止。
- 建议：改为 `origin = store.OriginChat`（与设计一致）。分叉来源已由 `forked_from`、`forked_at_turn` 记录，不需要另造 origin 值。

### F33 [CORE_CORRECTIVE] 续读 ref 改为“多读那一条的 seq”，在无回退记录的会话上也改变模型可见文本，撞上 0.4 新加的 fail-closed 条款

- 证据：
  - 现状 `readMessages`（`ledger.go:264,280-281`）已经多读一条（`Limit: offset+limit+1`），续读提示是 `entries[len-1].Seq+1`。0.4 改为取多读那一条的 seq。两者只在“窗口末条之后紧跟非 ledger 类型条目（plan、context_update、rewind 等）”时不同，例如末条 seq 40、41–44 是 plan/context_update、下一条消息是 45：旧提示 `s/41`，新提示 `s/45`。读出的窗口相同，但提示文本变了。
  - 0.4 新加的 fail-closed：“在**没有回退记录的会话**上改变任何行为（含 sugasdk）……属 Semantic Amendment，停止”；唯一预先登记的例外是 undo 通知取空。续读 ref 的变化不在例外里，实施方照写会触发停止，或悄悄违反该条。现有用例 `ledger_test.go:259` 恰好末条后紧跟消息，不会暴露差异。
- 建议（二选一，写进 T2）：
  - 只在 `last.Seq+1` 落在屏蔽区间内时才改用多读那一条的 seq，其余照旧 `last.Seq+1`；用例加“无回退记录时续读 ref 文本不变（末条后紧跟 plan 条目）”；或
  - 把这一变化登记为 fail-closed 的第二个例外，并写进 CHANGELOG。
- 附带（ADVISORY，第 3 轮 F27 附带项仍未处理）：
  - `Ledger` 只持有 `*store.Store`（`ledger.go:58-65`），当前会话来自 `observeScopeFrom(ctx)`（`:101`），拿不到 Runner 上的 `maskedRanges`。去掉缓存后最简单的写法是把“由 `RewindRecords` 构造 SeqRanges”放在 store（T1）或包级函数，Ledger 与 `maskedRanges` 共用。写明位置。
  - 写明 `EntriesFromSkipping` 的 limit 实参仍是 `offset+limit+1`，offset 按可见条目计数；“按不存在处理”给出具体错误文本（建议复用 `no message at or after %s`）。

### F34 [CORE_CORRECTIVE] fork title 后缀的 i18n 文件与 T5 并行冲突，且不在 T6 文件清单

- 证据：
  - T6 写“进 `chat.ini` 或 `model.ini` 中与 title 同处”。T5 与 T6 同在 W3 并行，T5 文件清单已含 `internal/i18n/catalog/{zh,en}/model.ini`；`chat.ini` 属 T7（W4），且 `[chat]` 段由 chat 包使用，personal 去读它跨了层。波次表“文件不相交”的说法因此不成立，T6 文件清单也没有任何 catalog 文件。
  - 现有 `internal/i18n/catalog/zh/personal.ini` 的 `[personal]` 段正是 personal 包面向用户的文案（如 `session.recovered`），title 后缀属于这一类。
- 建议：后缀放 `personal.ini`（zh、en 各一），把 `internal/i18n/catalog/{zh,en}/personal.ini` 列入 T6 文件清单；去掉“或”。

### F35 [ADVISORY] `rewindFileStep` 的冗余字段与非快照项处理

- `Root SnapshotRootStatus` 与 `Item.RootStatus`（`undo.go:35`，`annotateRoots` 已填）重复，两处可能不一致。建议删去 `Root`，或注明以 `Item.RootStatus` 为准。
- 清理检测、快照关闭产生的 unrecoverable 项来自事件，没有快照条目，`Item` 为零值。零值进 `restoreItem` 会因 RootStatus 为空按 skip 处理，结果从 unrecoverable 变成 skip。写明 `applyRewindFiles` 对 `View.Action ∈ {unrecoverable}` 的项不调用 `restoreItem`，原样计入结果。
- `SetCheckpointState`（`snapshot_retention.go:136-141`）要求快照目录存在。写明只对有快照目录的轮设置状态（与 `Undo` 的 `HasSnapshots` 判断一致，`undo_restore.go:64`），并按 `View.TurnID` 分组、轮内逆序调用（`undo_restore.go:68-70`）。
- T4 正文仍写“都不加锁，由调用方持有 gate 与锁”，与 0.4 的“预览不取 gate 与锁”矛盾。改为“`applyRewindFiles` 要求调用方持有；`planRewindFiles` 可在无锁时只读调用（预览）”。

### F36 [ADVISORY] 取空队列的文件清单与既有测试

- T5 文件清单加 `runner.go`（`:646` 追加多个 ctxItem）。scope freeze 已覆盖 `internal/personal/**`，只是清单遗漏。
- T5 验证写明改写 `TestUndoNoticeQueueKeepsOrder`（`undo_notice_test.go:117`）为“一次取出全部且保序”，并加“AppendEntries 失败时整批放回、顺序不变”的用例。
- ptye2e `chat_undo_test.go` 不需要改。可选加强：在连续两次 `:undo` 后的“附加”回合断言请求中有两条回滚通知，作为取空行为的端到端证据。

### F37 [ADVISORY] `UndoGate` 扩接口的编译面

- `undoTestGate`（`undo_core_test.go:17-26`）要补 `AcquireUnlessBusy`，否则测试包编译失败。把它列入 T5 的测试改动。
- 另一种写法是新增可选接口（如 `undoBusyGate`），在 `withSessionUnlessBusy` 里类型断言，Gate 未实现时退回 `Acquire`。这样不动测试桩，但退回时“回合中不等待”就不成立了。计划现写法（扩 `UndoGate`）更直接，保留即可，只需列出测试桩。
- `withSessionUnlessBusy` 需与 `withSession` 一样把 sid 放进 `undoSessionIDKey` 的 ctx 值，`gateForUndo` 才能取到会话（`undo_runner.go:43-45,61-64`）。“结构相同”已隐含这一点，可以在注释里点明。

### F38 [ADVISORY] files scope 的 UndoResult 映射

- files scope 复用 `enqueueUndoNotice(ctx, UndoResult)`（`undo_notice.go:189`），但 `applyRewindFiles` 返回 `[]RewindFileItem`。写明映射：restore→Restored、delete→Deleted、conflict→Skipped、unrecoverable→Unrecoverable，Turns 为有快照且实际处理的轮数，Files 为 restore/delete 项的路径，Forced=false；与 `Undo` 一致，Turns 为 0 时不入队（`undo_restore.go:103`）。
- both scope 只写 session 事件、不写 EventUndo：`suga trace` 里按 `kind=undo` 找文件回滚的使用方看不到这次回滚的文件。session 事件 attrs 已含文件统计，可接受；建议在 T8 CHANGELOG 中带一句。

## 并行与依赖核对

- W3：T5（`rewind.go`、`rewind_notice.go`、`undo_runner.go`、`undo_notice.go`、`gate.go`、`undo.go`、`model.ini`，按 F36 加 `runner.go`）与 T6（`fork.go`、`fork_files.go`，按 F34 加 `personal.ini`）在 F34 修正后不相交。
- T6 依赖 `withSessionUnlessBusy`，它在 T5 的文件里（`undo.go`），而 T6 只依赖 T1、T2、T3。两个任务并行时 T6 拿不到这个方法。建议二选一：把 `AcquireUnlessBusy`、`withSessionUnlessBusy` 挪进 T3（W2，单独一个任务，`gate.go`、`undo.go`、`undo_runner.go` 的适配字段），T5、T6 共用；或者 T6 依赖加 T5，W3 改为串行。**这一项影响波次可执行性**，但改法明确，并入 F34 一起修订（归为 CORE_CORRECTIVE 的一部分）。

## 结论

**PASS**（可执行轴，Round 4）。

- 第 3 轮 CORE_CORRECTIVE：F25、F26、F28 关闭；F27 部分关闭，转 F33。ADVISORY：F29、F31 关闭，F30 部分关闭，转 F32、F34。
- 新增 CORE_CORRECTIVE：
  - F32：origin 改回 `chat`；
  - F33：续读 ref 仅在落入区间时改用下一条，或登记为例外；
  - F34：title 后缀放 `personal.ini` 并列入 T6；同时把 `withSessionUnlessBusy`/`AcquireUnlessBusy` 挪进 T3，或让 T6 依赖 T5。
- ADVISORY：F35–F38，及 F21、F24 遗留。
- 以上修订都不涉及接口形状以外的设计变化，建议写入 0.5 后由主会话逐条核对 diff，交用户批准。

## 剩余风险与后续

- `AcquireUnlessBusy` 抢占空闲整理后，若整理已过提交点，第一次 `:rewind` 会以 stale 结束，需要用户重跑；属可接受的体验代价，T7 已有提示。
- 取空队列后，同一回合注入多条连续的 user 角色消息。现状已存在“通知 + 用户消息”两条连续 user，未见 provider 兼容问题，但多条时建议在 T8 e2e 中覆盖一次。
- 分段主键查询与 `RewindRecords` 的代价仍只来自读码，要在 T1 实测。
