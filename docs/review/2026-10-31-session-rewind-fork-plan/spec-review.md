<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审（Round 8）

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，计划 commit `ff0586ba`）
- 修订版本：Draft 0.8
- 评审范围：只评审变更部分，即 `git diff 234a9a51 ff0586ba -- docs/plans/2026-10-31-session-rewind-fork-plan.md`，以及其直接依赖的段落：T5 第 7–8 步、T5 验证、修订记录与批准证据
- 依据：
  - 设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准），流程 3（通知）；
  - 本目录第 7 轮可执行轴报告：F44 为 CORE_CORRECTIVE。
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，没有读 standards-review.md
- 轮次：**Round 8**（严谨模式最后一轮）
- 日期：2026-10-31
- **结论：PASS**（没有开放的 CORE_BLOCKING，也没有新增 CORE_CORRECTIVE）。新发现 F45、F46 都是 ADVISORY，可在实施任务书中处理

## 输入与方法

在 `ff0586ba` 上静态读码，没有运行测试。读过的代码：

- `internal/personal/undo_notice.go:31-202`（`pendingUndoNotice`、`undoNoticeMessage`、`undoNoticeText`、队列取放、`injectUndoNotice`、`enqueueUndoNotice`）；
- `internal/personal/undo.go:435-460`（`classifySnapshot`）；
- `internal/personal/undo_restore.go:76-90,170-188`（计数分支、`restoreItem` 的不可恢复分支）；
- 设计 0.2 流程 3。

## 第 7 轮发现关闭状态

| 项 | 级别 | 0.8 落点 | 状态 | 说明 |
|---|---|---|---|---|
| F44 被取代 rewind 通知的文件事实 | CORE_CORRECTIVE | T5 第 8 步、T5 验证 | **CLOSED** | 改为“被移除项的 `Text` 原样前置拼接（空一行），结构字段只取新项、不新增字段”。不再需要承载清单的新字段，也不需要合并文案，F44 的载体缺失与文案缺失两点随之消失。可行性见核对 1 |
| F43 其余子项、F41/F33 附带小项、F21、F24 | ADVISORY | — | 不变 | 不在本轮 diff 范围内 |
| 上一轮“剩余风险”中的 checkpoint 状态与 `Failures` 收集 | 提示 | — | 不变 | 不在本轮 diff 范围内，实施时对齐 `:undo` |

### 核对 1：前置拼接的可行性（F44）

- **载体可行。** `pendingUndoNotice.Text` 是预渲染的完整字符串（`undo_notice.go:32`），注入时直接 `undoNoticeMessage(notice.Text)`（`undo_notice.go:162`），生成一条 `EntryUser`（`:167-168`）。把旧项 `Text + "\n\n" + 新 Text` 写进新项的 `Text`，下一轮仍然只有一条注入，内容依次是两次操作，不需要改 `undoNoticeMessage`、取法或回填逻辑。
- **结构字段只取新项可行。** `undoEventAttrs` 只在 `enqueueUndoNotice` 入队前用于写事件（`undo_notice.go:91-104,192-195`），队列项的计数字段在注入时不再使用；所以旧项的计数不并入，不影响注入正文，只影响新项事件的 attrs，符合“一次操作一条事件”。
- **回填正确。** 合并后的项只带新项的 `EventID`，注入后回填新事件（`:181-185`）；旧事件的 `entry_seq` 保持 0，计划已写明（S29）。
- **多次连续 rewind 可行。** rewind 项总在队首且入队时先移除旧 rewind 项，队列里最多一个 rewind 项；第三次 rewind 前置的是已拼接过的正文，按时间顺序累积，语义一致。
- **注入失败放回队首** 放回的是合并后的整项，不会丢失第一次的事实。
- **第一次的已恢复事实能否送达：** 取决于 both 通知正文是否写出已恢复事实，见 F45。

### 核对 2：不可恢复夹具（S33）

`classifySnapshot`（`undo.go:448-450`）对 `meta.Unrecoverable != ""` 或 `meta.Kind == native.SnapshotKindPurged` 返回 `CheckpointActionUnrecoverable`；`:undo` 一侧 `restoreItem` 的 `switch meta.Kind` 对 purged 落到 default 分支，同样返回 unrecoverable（`undo_restore.go:182-186`），计数在 `undo_restore.go:82-83` 累加。夹具只需把快照 meta 的 `Kind` 写为 purged，`:rewind files` 的预判与 `:undo` 的计数同口径，“计数与 `:undo` 一致”可断言。可行。

### 核对 3：全部冲突用例（S32）

全部冲突时 `Restored=Deleted=0`、`Files` 为空、`Skipped>0`，`undoNoticeText` 只输出“跳过 N 个冲突”（`undo_notice.go:68-70`），不进入“没有文件被恢复”分支。用例只断言跳过计数、沿用现有文案，可行，也不改 `:undo` 行为。

## 新发现

### F45 [ADVISORY] both 通知在“全部恢复成功”时的正文没有写明要列出已恢复事实，而新用例依赖它

- 证据：
  - 设计流程 3 只给了三种文案：只回会话、只回文件、“两者都回，但有不可恢复或跳过的文件：列出这些文件”。both 且全部恢复成功的正文没有规定。
  - 计划 T5 第 8 步只写“新增通知文案进 `model.ini`”，没有写 both 正文是否包含已恢复/已删除计数或路径。
  - T5 新验证用例（计划 `:442`）要求“第一次为 both 且恢复了文件……第一次的已恢复事实可见”。若实施者按设计字面只在有跳过/不可恢复时列文件，both 全成功的正文可能只有会话部分，用例无法通过；而 only-conversation 文案“磁盘文件没有回退”显然不适用于 both。
- 判定：用例已把要求钉死（both 正文须含已恢复事实），实施者可以从计划唯一推出做法，不会实施错误，故不是 CORE。
- 建议（可在实施任务书中处理）：写明 both 正文 = “会话已回到第 N 轮” + 文件结果（已恢复/已删除计数与路径，沿用 `undoNoticeText` 的截断规则 `undoNoticeTextFiles`，`undo_notice.go:25,77-86`），有跳过或不可恢复时另列这些文件；不得出现“磁盘文件没有回退”。

### F46 [ADVISORY] 拼接后两段正文之间没有先后标识

- 证据：第一次 both 的正文说文件已回退，紧接着第二次 conversation 的正文说“磁盘文件没有回退”。计划只写“空一行、按先后顺序”。模型可能把后一句理解为“此前的回退也没有改动磁盘”。
- 判定：计划已选定“原样拼接”且用例可断言，不影响实施正确性。
- 建议（可在实施任务书中处理）：只回会话文案可改为“本次只回退了会话，磁盘文件未再改动”，或在拼接时不加额外字段、仅靠文案措辞区分“本次”。若不改，记为已接受取舍即可。

## 结论

**PASS**（可执行轴，Round 8，最后一轮）。

- 第 7 轮 F44 关闭：前置拼接只用现有 `Text` 字段与现有注入路径，可行，且第一次 rewind 的正文会在同一条注入里送达。
- S33 夹具（purged）与 S32 全部冲突用例均可按现有代码实现。
- 新发现 F45、F46 均为 ADVISORY，可在实施任务书中处理，无需再开评审轮次。

## 剩余风险与后续

- 第 6–7 轮记录的风险不变：抢占空闲整理后第一次执行可能以 stale 结束；undo 通知会迟到送达；分段查询代价要在 T1 实测。
- `applyRewindFiles` 的 checkpoint 状态（undone/partial）与 `Failures` 收集需在实施时对齐 `:undo`（`undo_restore.go:95-101`）。
