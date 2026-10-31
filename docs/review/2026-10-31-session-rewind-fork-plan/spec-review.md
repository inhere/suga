<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审（Round 5）

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，计划 commit `0d6737d7`）
- 修订版本：Draft 0.5
- 评审范围：变更范围评审，`git diff 6e03cbff 0d6737d7 -- docs/plans/2026-10-31-session-rewind-fork-plan.md` 的改动，以及这些改动直接依赖的段落（跨任务接口、波次表、T2 recall、T3 验证、T4、T5 第 7–8 步与验证、T6、T8 CHANGELOG、追溯表）
- 依据：设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准）；本目录第 4 轮可执行轴报告（commit `6e03cbff`，F32–F34 CORE_CORRECTIVE、F35–F38 ADVISORY）
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，未读 standards-review.md
- 轮次：**Round 5**
- 日期：2026-10-31
- **结论：PASS**（无开放 CORE_BLOCKING）。新增 CORE_CORRECTIVE F39、F40，都是一两句话的修订，建议写入后由主会话核对 diff，不需要再开一轮

## 输入与方法

在 HEAD `0d6737d7` 上静态读码，没有运行测试。读过的代码：

- 通知队列：`undo_notice.go` 全文（`pendingUndoNotice`、`appendUndoNotice`、`takeUndoNotice`、`injectUndoNotice`、`enqueueUndoNotice`）、`runner.go:643-650`、`store/events.go:109-134` UpdateEventEntrySeq。
- recall：`ledger.go:51-135,247-290`、`runner.go:443-444`（Ledger 绑定）。
- 文件恢复与计数：`undo_restore.go:20-30`（UndoResult）、`:55-112`（Undo 计数）、`:143-187`（restoreItem 各返回分支）。
- 取锁：`gate.go:54-174`、`undo_core_test.go:17-26,56`、`undo_runner.go:13`（`Gate` 赋值点只有这两处）。
- 其他：`store/session_origin.go:10-13`（`OriginChat`）、`internal/i18n/catalog/{zh,en}/` 目录（`personal.ini`、`model.ini` 均存在）。
- 设计：0.2 第 143–152 行（meta 与进程内状态）、第 236–242 行（通知）、R5/R8；设计评审台账 R2-F-N4。

## 第 4 轮发现关闭状态

| 项 | 级别 | 0.5 落点 | 状态 | 说明 |
|---|---|---|---|---|
| F32 origin 取 `fork` | CORE_CORRECTIVE | T6 | **CLOSED** | 改为 `store.OriginChat`（`store/session_origin.go:13`），与设计第 129 行一致 |
| F33 续读 ref 在无回退会话上变化 | CORE_CORRECTIVE | T2 recall、T2 验证、偏差清单 | **CLOSED** | 只在 `last.Seq+1` 落入区间时改取多读那一条的 seq，其余照旧。核对：`readMessages` 已多读一条（`ledger.go:264`），`EntriesFromSkipping` 跳过区间后多读的那条必在区间之后；`last.Seq+1` 不在区间、但其后另有区间时，下一次从 `last.Seq+1` 跳区间读，结果同样正确。无回退会话不进入新分支，ref 逐字不变。附带 ADVISORY 见 F41 |
| F34 title 后缀文件与并行冲突 | CORE_CORRECTIVE | T6 文件清单、波次表 | **CLOSED** | `personal.ini` 存在（zh、en），`[personal]` 段属 personal 包；T5 用 `model.ini`，T6 用 `personal.ini`，W3 文件不相交 |
| F34 附带：`withSessionUnlessBusy` 归属 | CORE_CORRECTIVE | T4、波次表 | **CLOSED** | 移入 T4（W1，无依赖），T5、T6 都依赖 T4。T4 改 `gate.go`、`undo.go`、`undo_runner.go`、`undo_core_test.go`，与同波的 T2（`visible.go`、`context.go`、`segment_*.go`、`handoff.go`、`lastreply.go`、`session_card.go`、`ledger.go`）不相交；T5 也改 `undo_runner.go`，但它在 W3、依赖 T4，串行无冲突。`UndoGate` 的实现方只有 `undoGate` 与 `undoTestGate`（`Gate` 赋值点仅 `undo_runner.go:13`、`undo_core_test.go:56`），桩已列入。T3 前置拒绝只定义函数、由 T5/T6 在锁内调用，T3 不依赖 T4 成立 |
| F35 `rewindFileStep` 冗余字段等 | ADVISORY | 跨任务接口、T4 | CLOSED | 删去 `Root`，以 `Item.RootStatus` 为准；unrecoverable 项不送 `restoreItem`；`SetCheckpointState` 只对有快照目录的轮调用；T4 正文与“预览无锁只读、执行在 `withSessionUnlessBusy` 内”对齐。“轮内逆序调用”未写明，属实施细节，`Undo` 有现成写法（`undo_restore.go:68-70`） |
| F36 取空队列文件清单 | ADVISORY | T5 | CLOSED（前提已变） | 取空队列已撤回；`runner.go` 列为“如需透传”；`TestUndoNoticeQueueKeepsOrder` 保持通过列入验证 |
| F37 `UndoGate` 编译面 | ADVISORY | T4 | CLOSED | 测试桩补方法列入 T4；`undoSessionIDKey` 由“结构相同”隐含 |
| F38 files scope 的 UndoResult 映射 | ADVISORY | T5 第 7 步 | **PARTIAL → F40** | 写了映射，但 unrecoverable 的处理与 `:undo` 不一致，`restoreItem` 的 skip 结果、Turns 未写 |
| F21 遗留（chat 空闲取通知的互斥依据） | ADVISORY | — | 仍 OPEN | 不在本轮 diff 范围 |
| F24 遗留（TurnID 为空、fork e2e 基准等） | ADVISORY | — | 仍 PARTIAL | 不在本轮 diff 范围 |

### 可行性核对：rewind 通知插入队首（S22）

- 可行，且不改变无回退会话的行为：
  - 队列是 meta 中的 JSON 数组（`undo_notice.go:22,111-116`），插入队首只需新增一个与 `appendUndoNotice` 对称的写法（decode → `append([]pendingUndoNotice{n}, notices...)` → `PatchSessionMeta`），只有 rewind 调用；`appendUndoNotice`、`takeUndoNotice`、`injectUndoNotice` 不改。
  - `takeUndoNotice` 取 `notices[0]`（`:131`），rewind 通知在队首即在下一轮送达。
  - `event_id` 回填对任意事件通用：`UpdateEventEntrySeq` 只读写 `attrs_json` 的 `entry_seq`（`store/events.go:109-134`），rewind 的 session 事件同样适用；入队时把 session 事件 id 填进 `EventID` 即可。
  - 注入消息带 `metaUndo` 标记（`:44`），handoff/compaction 按事实消息保留，与设计“沿用 undo 队列”一致。
- 两处未覆盖的情形见 F39（连续两次 rewind）与 F41（注入失败放回队尾）。

## 新发现

### F39 [CORE_CORRECTIVE] 连续两次含会话的 rewind 时，被取代的旧 rewind 通知在之后送达，模型最后看到的是过期状态

- 证据：
  - T5 第 8 步：含会话时“本次通知插入队列首位，已在队列中的 undo 通知保留不删，在之后的回合依次送达”。队列不区分 undo 与 rewind 通知（`pendingUndoNotice` 无类型字段，`undo_notice.go:31-41`）。
  - 场景：`:rewind t5`（只回会话）后不发消息，再 `:rewind t3`。队列变为 `[rewind→t3, rewind→t5]`。下一轮送达“会话已回到第 3 轮”，再下一轮送达“会话已回到第 5 轮；磁盘文件没有回退”。第二条是过期事实，且因为最晚出现，模型会把它当作最新状态。
  - 0.4 的取空队列按入队顺序同轮送达（旧在前、新在后），这一问题是 0.5 改为队首插入后新出现的。F-N4（设计评审 R2-F-N4）保留的是“cut 之前 undo 的文件事实”，不涵盖已被取代的 rewind 通知；设计第 152 行的原意也是“由本次 rewind 的通知取代”。
- 建议（写进 T5 第 8 步与验证）：
  - `pendingUndoNotice` 增加一个 omitempty 的类型字段（如 `Kind: "rewind"`），只有 rewind 通知写入，无回退会话的队列编码不变；
  - 插入新的 rewind 通知时，移除队列中尚未送达的旧 rewind 通知；若被移除的是只回会话的通知，把“磁盘文件没有回退”并入新通知文案（新通知本身含 files 时除外）；被移除通知的 session 事件不回填 entry_seq，可接受；
  - 验证加“连续两次 `:rewind` 不发消息，只送达最后一次的通知”。

### F40 [CORE_CORRECTIVE] files scope 的 UndoResult 映射与 `:undo` 不一致，且 `restoreItem` 的 skip 结果与 Turns 未定

- 证据：
  - T5 第 7 步：“restore→Restored、delete→Deleted、conflict→Skipped，unrecoverable 只计入 rewind 结果”。但 `Undo` 按 `restoreItem` 的实际结果计数，`CheckpointActionUnrecoverable` 计入 `Unrecoverable`（`undo_restore.go:82-83`，来源如未知快照类型 `:183-186`），其余（受保护路径、快照未完成、工具身份无效、根不受信）都计入 `Skipped`（`:85-86`、`:147-169`）。
  - 照 0.5 的写法，同一个快照条目经 `:undo` 会在通知里出现“N 个文件无法恢复”，经 `:rewind files` 则不出现；文案由 `undoNoticeText` 按计数生成（`:71-73`），模型因此不知道有文件没恢复。这违背 T5 第 8 步“与 `:undo` 的通知方式一致”和设计第 241 行“只回文件：沿用现有 undo 文案”。
  - `RewindFileAction` 只有 restore/delete/conflict/unrecoverable 四种，`restoreItem` 返回的 `CheckpointActionSkip`（受保护路径等）落到哪一种没有写。
  - `Turns` 未写。`Undo` 只在 `Turns > 0` 时入队（`:105`），而 F31 要求 files scope“一次操作一条事件”。若全部是无快照的 unrecoverable 项，Turns 为 0，既不写 EventUndo 也不注入通知，与 F31 和 R4 的“列出这些文件”不符。
- 建议（写进 T5 第 7 步）：
  - 对送进 `restoreItem` 的项，按其返回结果计数，与 `Undo` 逐分支相同：restore→Restored、delete→Deleted、unrecoverable→Unrecoverable、其余→Skipped；`RewindFileItem.Action` 中 skip 映射为 conflict 并带原 reason（或新增 `skip` 动作，属接口形状变化需同时改跨任务接口）。
  - 预先判定为 unrecoverable、未送 `restoreItem` 的项（cleared、no_snapshot、snapshots_off、root_untrusted）同样计入 `Unrecoverable`，让 undo 文案提示“N 个文件无法恢复”；路径由 rewind 结果列出。
  - `Turns` 为有快照且实际处理的轮数；files scope 下只要存在任一文件项（含 unrecoverable）就写 EventUndo 并入队，不沿用 `Turns > 0` 的门槛。

### F41 [ADVISORY] 其余小项

- **注入失败放回队尾**：`injectUndoNotice` 在 `AppendEntries` 失败时用 `appendUndoNotice` 放回队尾（`undo_notice.go:170,176`）。rewind 通知因此可能排到旧 undo 通知之后，R5“下一轮送达”在失败重试路径上不成立。建议放回时，带 rewind 类型字段（F39）的通知放回队首；undo 通知照旧，无回退会话不变。
- **recall 的区间来源**（F33 附带项）：T2 写“Ledger 通过 Runner 注入的 `maskedRanges` 取得当前会话区间”。Ledger 只在 `runner.go:443-444` 由 `bind(repo)` 绑定，T2 文件清单没有 `runner.go`；而 `maskedRanges` 只依赖 `RewindRecords`，Ledger 可用自己的 `repo` 直接调同一函数，不必注入。建议写明二者之一并补清单（`runner.go` 不在 W1 的 T4 中，不产生冲突）。
- **只对当前会话跳过**：recall 可读任意会话（`readMessages` 的 sessionID 由 ref 给出），写明只有 `sessionID == observeScopeFrom(ctx).sessionID` 时才传区间，其他会话传空区间，以免实施方对被读会话自身的 rewind 记录也做屏蔽（设计只要求当前会话）。
- **F33 附带未处理项**：`EntriesFromSkipping` 的 limit 实参仍为 `offset+limit+1`、offset 按可见条目计数；“按不存在处理”的错误文本（建议复用 `no message at or after %s`，`ledger.go:276`）。

## 并行与依赖核对

- W0：T0、T1 不变。
- W1：T2 与 T4 文件不相交（见上表 F34 附带行）。
- W3：T5（`rewind.go`、`rewind_notice.go`、`undo_runner.go`、`undo_notice.go`、`runner.go`、`model.ini`）与 T6（`fork.go`、`fork_files.go`、`personal.ini`）不相交；二者都只调用 T4 的 `withSessionUnlessBusy`，不改它。
- 依赖表与正文一致：T5 依赖 T3、T4；T6 依赖 T1、T2、T3、T4。

## 结论

**PASS**（可执行轴，Round 5）。

- 第 4 轮 CORE_CORRECTIVE F32、F33、F34（含取锁归属）全部关闭；ADVISORY F35–F37 关闭，F38 部分关闭转 F40；F21、F24 遗留不变。
- 新增 CORE_CORRECTIVE：
  - F39：连续 rewind 时移除尚未送达的旧 rewind 通知（加 omitempty 类型字段）；
  - F40：files scope 计数按 `restoreItem` 结果与 `:undo` 一致，预判 unrecoverable 计入 Unrecoverable，写明 skip 映射与入队门槛。
- ADVISORY：F41。
- 两项修订都不改变无回退会话的行为，也不改跨任务接口形状（F40 若选新增 `skip` 动作则需同步接口），建议写入 0.6 后由主会话逐条核对 diff，交用户批准。

## 剩余风险与后续

- `AcquireUnlessBusy` 抢占已过提交点的空闲整理时，第一次 `:rewind` 以 stale 结束，需要重跑（第 4 轮已记录，可接受）。
- 保留的旧 undo 通知在 rewind 之后的回合才送达，模型看到的是“迟到”的文件回滚事实；这是 F-N4 的既定取舍，建议 T8 e2e 覆盖一次“undo 后 rewind”两轮送达的请求内容。
- 分段主键查询与 `RewindRecords` 的代价仍只来自读码，要在 T1 实测。
