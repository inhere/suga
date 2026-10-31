<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审（Round 7）

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，计划 commit `234a9a51`）
- 修订版本：Draft 0.7
- 评审范围：只评审变更部分，即 `git diff abdb5bd9 234a9a51 -- docs/plans/2026-10-31-session-rewind-fork-plan.md` 的改动，以及这些改动直接依赖的段落：跨任务接口中的 `RewindFileItem`/`RewindResult`、T4 文件清单、T5 文件清单与第 7–8 步、T5 验证、追溯表、批准证据
- 依据：
  - 设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准），流程 1 第 5 步与流程 3（通知）；
  - 本目录第 6 轮可执行轴报告：F42 为 CORE_CORRECTIVE，F43 为 ADVISORY。
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，没有读 standards-review.md
- 轮次：**Round 7**
- 日期：2026-10-31
- **结论：PASS**（没有开放的 CORE_BLOCKING）。新增一项 CORE_CORRECTIVE F44（只改 T5 第 8 步一条子项），建议写入后由主会话核对 diff，不需要再开一轮评审

## 输入与方法

在 `234a9a51` 上静态读码，没有运行测试。读过的代码：

- `internal/personal/undo_restore.go:20-115`（`UndoResult`、`restoreOutcome`、`Undo` 的计数与入队）、`:143-240`（`restoreItem` 各返回分支）；
- `internal/personal/undo.go:75-76`（`HasSnapshots` 即 `len(Items) > 0`）；
- `internal/personal/undo_notice.go:25-202` 全部队列与通知代码；
- `internal/personal/store/types.go:88,115`（`EventSession`、`EventUndo` 常量）。

## 第 6 轮发现关闭状态

| 项 | 级别 | 0.7 落点 | 状态 | 说明 |
|---|---|---|---|---|
| F42 files scope 的 Turns 与入队条件 | CORE_CORRECTIVE | T5 第 7 步、验证、追溯表 | **CLOSED** | 见下方核对 1 |
| F43-1 被取代 rewind 通知的文件事实 | ADVISORY | T5 第 8 步 | **PARTIAL → F44** | 已写“并入新通知”，但只列了跳过与不可恢复，漏了 F43 场景的核心事实（已恢复/已删除），且缺载体与文案规则，详见 F44 |
| F43-2 skip 的 `RewindFileItem.Action` | ADVISORY | T5 第 7 步 | **CLOSED** | 映射为 `conflict`，Reason 取跳过原因。`restoreOutcome.reason`（`undo_restore.go:34`）在每个 skip 分支都有赋值，可行 |
| F43-3 `UndoResult.Files` 映射 | ADVISORY | T5 第 7 步 | **CLOSED** | “Files 为实际恢复与删除的路径”，与 `undo_restore.go:72-74` 一致 |
| F41/F33 附带小项、F21、F24 | ADVISORY | — | 不变 | 不在本轮 diff 范围内 |
| 文件清单残留（`runner.go` 移出 T5、`ErrRewindBusy` 写入 T4 清单） | — | T4、T5 | 已核对 | T5 不再列 `runner.go`，与 T2/T5 文件交集说明一致；`ErrRewindBusy` 归属与跨任务接口一致 |

### 核对 1：Turns 定义与入队条件（F42）

- **Turns 定义一致。** 计划写“目标范围内有快照目录的轮数（`undo_restore.go:62-67`）”。代码中 `Undo` 对每个 `row.HasSnapshots()` 的选中轮执行 `result.Turns++`，与项结果无关；`HasSnapshots` 即 `len(c.Items) > 0`（`undo.go:76`）。实施时以 `HasSnapshots` 为准即可，行号引用正确。
- **入队条件一致。** “Turns>0 时调用与 `:undo` 相同的 `enqueueUndoNotice`（`undo_restore.go:105`）”：`enqueueUndoNotice`（`undo_notice.go:189-202`）先 `AppendEvent(EventUndo)`，再 `appendUndoNotice` 入队尾，与计划描述一致。全部冲突时 Turns>0，通知文案走 `undoNoticeText` 的“跳过 N 个冲突”分支（`undo_notice.go:68-70`），与新增验证用例一致。
- **预判 unrecoverable 的项。** 计划规定 unrecoverable 项不送进 `restoreItem`（F35）。只要该轮 `Items` 非空，Turns 仍按 `HasSnapshots` 计数，与 `:undo` 一致（`:undo` 下这些项由 `restoreItem` 返回 unrecoverable，同样计入 Turns 与 Unrecoverable）。
- **Turns=0 只写一条 session 事件：可行。** `store.EventSession = "session"`（`types.go:88`）已存在，与 conversation scope 的 rewind 事件同 kind 同 name，由 attrs 的 scope 区分。Turns=0 时 `:undo` 不写事件、不入队；rewind 改写一条 session 事件、不入队，磁盘确实无变化（没有项进入 `restoreItem`），所以“不入队通知”与 `:undo` 的通知方式一致，“一次操作一条事件”也成立。这是只在使用 `:rewind` 时才出现的新行为，不触发“没有回退记录的会话上行为变化”的 fail-closed。
- 三种情形（conversation 写 session 事件、files Turns>0 写 EventUndo、files Turns=0 写 session 事件）互斥且各恰好一条，F31 成立。

## 新发现

### F44 [CORE_CORRECTIVE] 被取代 rewind 通知的“文件事实并入”漏掉已恢复/已删除，且队列项没有承载清单的字段

- 证据：
  - T5 第 8 步（计划 `:428`）：“被移除项的文件事实（跳过、不可恢复清单）并入新通知”。
  - F43 原场景：`:rewind t5 both` 已回退文件、尚未发消息，又执行 `:rewind t3 conversation`。旧通知被移除后，模型从未得知 t5 之后的文件已被回退；新通知按设计流程 3 的“只回会话”文案说“磁盘文件没有回退”。0.7 只并入跳过与不可恢复清单，**没有并入已恢复/已删除的事实**，所以新通知仍对模型陈述一个错误事实，F43 想修的正是这一点。计划既然选择“并入”而不是“记为已接受取舍”，就应把关键事实并全。
  - 载体缺失：队列项 `pendingUndoNotice`（`undo_notice.go:31-41`）只有计数和 `Files`（仅 restore/delete 路径，`undo_restore.go:72-74`），`Text` 是预渲染好的字符串；没有跳过、不可恢复的路径清单。计划只声明新增 `Kind` 字段，实施方要么从 `Text` 反解析（不可行），要么自行加字段（计划未写，属于接口缺口）。
  - 文案规则缺失：新通知为“只回会话”而并入了旧的文件事实时，设计流程 3 的三种文案都不直接适用（“只回会话”文案说磁盘没有回退，与并入的事实矛盾）。计划没有写选用哪种文案、或如何拼接。
- 建议（改 T5 第 8 步该子项）：
  1. 并入范围改为“被移除项的全部文件结果：已恢复、已删除、跳过、不可恢复”；
  2. 写明 rewind 队列项需携带结构化的文件结果（例如在 `pendingUndoNotice` 上新增 `omitempty` 字段保存跳过与不可恢复路径；restore/delete 沿用 `Files` 与计数），合并后用这些字段重新渲染 `Text`，不从旧 `Text` 解析。undo 项不设这些字段，编码不变；
  3. 写明合并后的文案：本次为“只回会话”但并入了文件结果时，不使用“磁盘文件没有回退”，改为说明“此前一次回退已改动磁盘文件”并列出文件（新增一条 `model.ini` 文案，zh/en 各一份）；
  4. 验证增加一个用例：`both` 回退后不发消息再做 `conversation` 回退，下一轮通知含前一次已恢复的文件，且不含“磁盘文件没有回退”。
- 影响：只涉及有回退记录的会话的 rewind 队列项，不改跨任务接口，也不改 undo 项的编码与取放。

## 并行与依赖核对

- T5 文件清单去掉 `runner.go` 后，W3 中 T5 与 T6 仍无文件交集（T6 改 `fork.go`、`fork_files.go`、`personal.ini`）。
- T4 文件清单写明声明 `ErrRewindBusy`，T5 声明 `ErrRewindStale`，依赖方向 T5→T4 不变。
- “容器内 git 只前缀 `GIT_DIR/GIT_WORK_TREE`”从 fail-closed 列表移到“运行目录与环境”，只是归类调整，语义不变。

## 结论

**PASS**（可执行轴，Round 7）。

- 第 6 轮发现：F42 关闭；F43 的 skip 映射与 `Files` 映射关闭；F43 的“文件事实并入”部分关闭，剩余转为 F44。
- 新增 CORE_CORRECTIVE F44：并入范围补上已恢复/已删除，写明队列项的结构化字段与合并后文案。修订局限于 T5 第 8 步一条子项和一个验证用例，建议写入后由主会话核对 diff，然后交用户批准计划。

## 剩余风险与后续

- 第 6 轮记录的风险不变：抢占空闲整理后第一次执行可能以 stale 结束；undo 通知会迟到送达；分段查询代价要在 T1 实测。
- 计划未写 `applyRewindFiles` 是否像 `Undo`（`undo_restore.go:95-101`）那样按轮设置 checkpoint 状态（undone/partial），以及 `restoreItem` 返回错误时是否收集到 `Failures` 而不中断；不在本轮 diff 范围内，仅提示实施时对齐 `:undo`。
