<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 可执行轴评审（Round 6）

## 评审目标与范围

- 目标：`docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，计划 commit `abdb5bd9`）
- 修订版本：Draft 0.6
- 评审范围：只评审变更部分，即 `git diff 0d6737d7 abdb5bd9 -- docs/plans/2026-10-31-session-rewind-fork-plan.md` 的改动，以及这些改动直接依赖的段落：跨任务接口中的错误值与 `RewindFileItem`/`RewindResult`、T2 文件清单、T5 第 7–8 步与验证、偏差清单、追溯表
- 依据：
  - 设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2（已批准），第 236–242 行（通知）；
  - 本目录第 5 轮可执行轴报告：F39、F40 为 CORE_CORRECTIVE，F41 为 ADVISORY。
- 评审者：可执行轴（Spec/Executability）独立评审子 agent，只读；与规范轴隔离，没有读 standards-review.md
- 轮次：**Round 6**
- 日期：2026-10-31
- **结论：PASS**（没有开放的 CORE_BLOCKING）。新增一项 CORE_CORRECTIVE F42，只需改一行定义；建议写入后由主会话核对 diff，不需要再开一轮评审

## 输入与方法

在 `abdb5bd9` 上静态读码，没有运行测试。读过的代码如下：

- `internal/personal/undo_notice.go` 全文：
  - `pendingUndoNotice`（`:31-41`）；
  - `appendUndoNotice`（`:106-117`）、`takeUndoNotice`（`:119-140`）、`decodeUndoNotices`（`:142-155`）；
  - `injectUndoNotice`（`:157-187`）、`enqueueUndoNotice`（`:189-202`）；
  - `undoNoticeText`（`:60-89`）。
- `internal/personal/undo_restore.go:20-30`（`UndoResult`），以及 `:46-115`（`Undo` 的计数与入队）。
- `internal/personal/undo.go:76`（`HasSnapshots`）。
- `internal/personal/runner.go:438-448`（Ledger 绑定）。
- 在包内检索队列键与结构字面量的使用处：只有 `undo_notice_test.go:82,122` 和 `undo_options_test.go:56`。

## 第 5 轮发现关闭状态

| 项 | 级别 | 0.6 落点 | 状态 | 说明 |
|---|---|---|---|---|
| F39 连续 rewind 时旧通知过期 | CORE_CORRECTIVE | T5 第 8 步、验证、追溯表 | **CLOSED** | 新增 `Kind`（omitempty）字段；入队时移除未送达的 rewind 项。可行性核对见下文。被取代通知中的文件事实会丢失，这一残留点见 F43 第 1 条（ADVISORY） |
| F40 files scope 的计数映射 | CORE_CORRECTIVE | T5 第 7 步、验证 | **PARTIAL → F42** | 已关闭的部分：unrecoverable 计入 Unrecoverable，通知按 `undoNoticeText:71-73` 出现“N 个文件无法恢复”；`restoreItem` 的跳过计入 Skipped。新引入的问题：Turns 的定义与 `:undo` 不同，和“沿用 `:undo` 的入队条件”组合后，行为与 `:undo` 不一致，详见 F42 |
| F41 注入失败放回队尾 | ADVISORY | T5 第 8 步 | CLOSED | `:170`、`:176` 两处放回逻辑按 Kind 分支：rewind 项放回队首，undo 项仍用 `appendUndoNotice` 放回队尾 |
| F41 recall 的区间来源 | ADVISORY | T2 文件清单 | CLOSED | 已写明在 `runner.go:444` 的 Ledger 绑定处注入 `maskedRanges`。已核对：`cfg.Ledger.bind(cfg.Repository)` 在 `runner.go:443-445`，行号正确；`runner.go` 不在同波的 T4 文件清单中，没有冲突 |
| F41 只对当前会话跳过、F33 附带的 limit 与错误文本 | ADVISORY | — | 仍 OPEN | 0.6 没有处理，属实施细节 |
| S25 错误值归属（规范轴，影响编译面） | — | 跨任务接口 | 已核对 | `ErrRewindBusy` 由 T4 声明（`withSessionUnlessBusy` 返回它），`ErrRewindStale` 由 T5 声明。依赖方向是 T5 依赖 T4，编译顺序成立 |
| F21、F24 遗留 | ADVISORY | — | 不变 | 不在本轮 diff 范围内 |

### 可行性核对：`Kind` 字段与队列操作

- **编解码兼容旧数据：成立。**
  - 队列以 JSON 形式保存在 session meta 中，读取时经过 `decodeUndoNotices` 的 Marshal 与 Unmarshal（`:146-151`）。
  - 旧数据没有 `kind` 键，解码后 `Kind` 为空串，按 undo 项处理。
  - undo 项的 `Kind` 为空，因为加了 `omitempty`，编码结果逐字节不变。
  - `undo_notice_test.go:122` 使用具名字段的结构字面量，新增字段后照常编译。
- **入队时移除未送达的 rewind 项：可行。**
  - 新增一个与 `appendUndoNotice` 对称的函数：解码队列 → 过滤掉 `Kind=="rewind"` 的项 → 把新项插到队首 → 调用 `PatchSessionMeta`。
  - 这个函数只在 rewind 时调用，并且在 `withSessionUnlessBusy` 中执行，与 `takeUndoNotice` 互斥。原因是 `takeUndoNotice` 只在持有 gate 的回合中被调用。
  - 被移除那一项对应的 session 事件不会再回填 `entry_seq`，这是可接受的。
- **注入失败时 rewind 项放回队首：可行。**
  - 失败发生在回合持有 gate 期间，rewind 此时会返回忙，所以在取出与放回之间，队列不会被 rewind 改动。
  - 放回时按 `notice.Kind` 分支即可；`takeUndoNotice` 不需要改。
- **undo 项的行为确实不变。**
  - `appendUndoNotice`、`takeUndoNotice`、`enqueueUndoNotice` 都不改。
  - undo 项的放回仍走队尾。
  - 没有回退记录的会话，队列里不会出现 rewind 项，所以新的过滤与插入路径不会执行。
  - `TestUndoNoticeQueueKeepsOrder` 不受影响。

## 新发现

### F42 [CORE_CORRECTIVE] files scope 的 Turns 定义与 `:undo` 不同，导致只有冲突的回退既不写事件也不入队

- 证据：
  - T5 第 7 步写的是“Turns 为至少有一项 restore 或 delete 的轮数”，同时写“是否入队沿用 `:undo` 的条件”。
  - `:undo` 的实际做法：
    - 每个有快照的选中轮都计入 Turns：先 `result.Turns++`，再处理该轮的各项，与各项结果无关（`undo_restore.go:62-66`，`HasSnapshots` 见 `undo.go:76`）；
    - 入队条件是 `result.Turns > 0`（`undo_restore.go:105`）。
  - 照 0.6 的定义，`:rewind tN files` 的各项如果全部是 conflict，或者全部被 `restoreItem` 跳过，Turns 为 0，于是不写 EventUndo，也不入队通知。
  - 同样的快照交给 `:undo` 处理时，Turns 为 1，会写一条 EventUndo，并通知“跳过 N 个冲突”（`undo_notice.go:68-70`）。
  - 这有三处矛盾：
    1. 与第 8 步“与 `:undo` 的通知方式一致”矛盾；
    2. 与验证项“计数与 `:undo` 一致”矛盾；
    3. 与 F31 的“一次操作一条事件”矛盾，这种情形下事件数为零。
  - 另外，“沿用 `:undo` 的条件”没有指明代码位置，实施方需要自己反推出 `Turns > 0`。
- 建议（改 T5 第 7 步的一行）：
  - 改为“Turns 为有快照、并且其中有项送进 `restoreItem` 的轮数，与 `Undo` 的 `HasSnapshots` 计数相同（`undo_restore.go:62-66`）；入队条件为 `Turns > 0`（`undo_restore.go:105`）”。
  - 预先判定为 unrecoverable、没有快照的轮不计入 Turns。`:undo` 同样跳过这类轮（`:63`），两者行为一致。
  - 验证中增加一个用例：“files scope 全部冲突时写一条 EventUndo，通知中含‘跳过 N 个冲突’”。

### F43 [ADVISORY] 其余小项

- **被取代的 rewind 通知中的文件事实。**
  - 场景：第一次 `:rewind t5 both` 已经回退了文件，还没发消息，又执行 `:rewind t3 conversation`。
  - 后一次的通知是“磁盘文件没有回退”，前一次已回退 t5 之后文件改动这一事实随着旧通知被移除而丢失。
  - 设计中只回会话的文案要求“先重新读取相关文件”，可以兜住这个问题，所以不升为 CORE。
  - 建议在 T5 中写明：被移除的旧 rewind 通知如果带有文件结果，就把文件统计并入新通知；也可以把这一点明确记为已接受的取舍。
- **skip 结果对应的 `RewindFileItem.Action`。**
  - 第 7 步写了“`restoreItem` 返回的跳过→Skipped”，但 `RewindFileAction` 只有 restore、delete、conflict、unrecoverable 四个值（跨任务接口），跳过项应该显示成哪个动作没有写明。
  - 建议写“映射为 conflict，并带上原 reason”。
- **`UndoResult.Files` 的映射。**
  - 映射清单中没有 `Files`。`:undo` 只把 restore 和 delete 的路径放进 `Files`（`undo_restore.go:72-74`），通知文案按 `Files` 列出文件（`undo_notice.go:77-86`）。
  - 建议补一句“Files 取 restore 与 delete 项的路径”，与 `Undo` 相同。

## 并行与依赖核对

- T2 新增 `runner.go`，T4 的文件清单不含 `runner.go`，同处 W1 的两个任务仍然没有文件交集。
- W3 中，T5 也会改 `runner.go`，但 T5 依赖 T2（经 T3）和 T4，执行时串行，不冲突。
- T6 不改 `runner.go`。
- 错误值的归属与依赖方向一致：T5 依赖 T4。

## 结论

**PASS**（可执行轴，Round 6）。

- 第 5 轮发现的处理结果：
  - F39 关闭；
  - F41 中的放回队首与 recall 区间来源两项关闭；
  - F40 部分关闭，剩余问题转为 F42。
- 新增 CORE_CORRECTIVE F42：files scope 的 Turns 改按 `Undo` 的 `HasSnapshots` 计数，入队条件写明是 `Turns > 0`（`undo_restore.go:105`）。
- 新增 ADVISORY F43。
- F42 的修订只影响有回退记录的 files scope 路径，不改跨任务接口的形状。建议写入后由主会话核对 diff，然后交用户批准计划。

## 剩余风险与后续

- 第 5 轮记录的风险不变：
  - 抢占空闲整理后，第一次执行可能以 stale 结束，需要重跑；
  - undo 通知会迟到送达；
  - 分段查询的代价目前只来自读码，要在 T1 实测。
