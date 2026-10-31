<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `abdb5bd9`）
- 修订版本: Draft 0.6
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告（spec-review.md）
- 轮次: Round 6（变更范围评审）。读 `git diff 0d6737d7..abdb5bd9 -- <plan>`，以及受影响的章节：修订记录、输入与批准证据（评审轮次、设计偏差清单）、跨任务接口（错误值归属、并发判定）、前置检查（运行目录与环境、fail-closed）、T2 文件清单、T4、T5（步骤 7–8 与验证）、可追溯性
- 日期: 2026-10-31
- 结论: **PASS**（没有开放的 CORE_BLOCKING）。S28 为 CORE_CORRECTIVE，交人工批准前一行级修正即可
- 排除项: 未运行测试与基准；没有逐行推演通知队列在多进程下的时序（归可执行轴）

## 输入与方法

- 规范：`idevstd load --delivery semantic --accept-delta --workspace <suga> --task plan --task review --agent claude --consumer-mode --thinking-mode rigorous`。结果 BOUND、FULL、18 项资源，payload sha256 `a7be77b2…` 已核验，与上一轮相同。
- 读取：0.5→0.6 的计划 diff 和上面列出的章节；第 5 轮规范轴报告；设计 0.2 的流程 3 通知（236-241 行）、流程 3 后台任务（151-152 行）、决策表 R5 与 R8（267、270 行）。
- 结构校验：工作区的计划与 `abdb5bd9` 一致。`idevstd script validate_document --kind plan` 结果为 `ok: true, errors: []`，共 615 行，不需要拆分。
- 代码核对：

| 引用 | 结果 |
|---|---|
| `undo_notice.go:31-41` pendingUndoNotice | 现无 `Kind` 字段，新增 `kind,omitempty` 不与已有键冲突；旧二进制读到 rewind 项会忽略 `kind`、按普通通知送达，降级可用 |
| `undo_notice.go:160-178` injectUndoNotice 放回 | 两处失败都调用 `appendUndoNotice` 放回队尾，计划“按 Kind 分支”落点正确 |
| `undo_notice.go:180-185` event_id 回填 | 只在注入成功时回填，被移除的旧 rewind 项不会回填，见 S29 |
| `undo_restore.go:62-67`、`undo_options.go:45` `:undo` 的 Turns | 有快照目录的行都计 1（不论是否全部跳过）；入队条件 `result.Turns > 0`（`undo_restore.go:105`、`undo_options.go:83`）。与 S28 相关 |
| `undo_notice_test.go:117` `TestUndoNoticeQueueKeepsOrder` | 存在 |

## 验收标准

- plan 合同：修订记录、scope freeze 与 fail-closed 一致、波次依赖与文件不相交、任务五要素、跨任务签名的归属、可追溯、设计偏差的登记、人工 Gate。
- 评审合同：同类穷尽、五类 CORE_BLOCKING 与 necessity proof、新增机制的 scope necessity、按删除优先的顺序关闭。
- 其他规则：SR1103、SR1401、SR1403、SR1406、SR1410、SR1301；GR104、GR109。

## 第 5 轮发现关闭状态

| ID | 状态 | 依据 |
|---|---|---|
| S25 `ErrRewindBusy` 归属与可追溯表 | **已关闭** | 跨任务接口分两行声明，`ErrRewindBusy` 注明 T4、`ErrRewindStale` 注明 T5（158-159 行）；可追溯表“S18、F28 取锁”改为“跨任务接口、T4、T6”，新增 S25 行（591、597 行）。T4 文件清单未点名声明文件，见 S30 |
| S26 偏差清单、注入失败放回、连续 rewind 顺序 | **已关闭** | 偏差清单改为“undo 通知保留、rewind 通知插入队首、分轮送达”，并注明设计原为丢弃（71 行），批准时对用户可见；rewind 项注入失败放回队首、undo 项仍放队尾（425 行）；连续 rewind 选定“只送最新”（424 行）；三项都有对应用例（436-437 行）。新机制的小缺口见 S29 |
| S27① 容器内 git 一条的位置 | **部分关闭** | 已从“文件拆分理由”下移出，但放进了 fail-closed 列表（220 行），而不是“运行目录与环境”（212-214 行）。见 S30 |
| S27② 批准证据缺 round 4 | **已关闭** | 补了 round 4（`f009fd02`）与 round 5（`67fd3f0c`）（66-67 行） |
| S27③ T2 文件清单缺 `runner.go` | **已关闭** | T2 清单补 `runner.go`（`:444` Ledger 绑定处注入 `maskedRanges`）（302 行）。T5 的“`runner.go`（如需透传，F36）”未改（397 行），见 S30 |

## 发现

### S28 [LOW] files scope 的 Turns 定义与 `:undo` 不同，而计划同时要求“计数与入队条件对齐 `:undo`”（CORE_CORRECTIVE）

- 证据：
  - T5 步骤 7 定义“Turns 为至少有一项 restore 或 delete 的轮数”，同时写“是否入队沿用 `:undo` 的条件”（418-419 行）；验证要求“files scope 有不可恢复项：通知含‘无法恢复’提示，计数与 `:undo` 一致”（438 行）。
  - `:undo` 的 Turns 是“有快照目录的行数”，全部跳过或不可恢复的行也计 1（`undo_restore.go:62-67`）；入队条件是 `Turns > 0`（`undo_restore.go:105`）。
  - 因此两种写法在“只有冲突或不可恢复、没有 restore/delete”时结果不同：按计划的定义 Turns=0、不入队、没有“无法恢复”提示；按 `:undo` 的定义会入队并提示。rewind 的不可恢复项还包括快照已清理的轮（T4），这类轮在 `:undo` 中根本不计。实施者拿到的是两条互相矛盾的指令，438 行的用例能否通过取决于夹具是否恰好带一项 restore。
- 必要性：F40 本身是为了对齐 `:undo`，只需让定义唯一，不新增机制。
- 影响：通知是否注入、EventUndo 的 `turns` 属性取值不唯一；不影响没有回退记录的会话，不涉及数据安全，chat 侧结果仍列出不可恢复项，所以不是 blocker。
- 建议：二选一写定一句：
  - Turns 按 `:undo` 取“有快照目录且被处理的轮数”，入队条件 `Turns > 0`；或
  - 入队条件改为“Restored、Deleted、Skipped、Unrecoverable 任一大于 0”，并删去“沿用 `:undo` 的条件”。
  - 同时在 438 行写明夹具（例如“一轮可恢复加一轮已清理”或“只有已清理的轮”），使用例只有一种期望。

### S29 [LOW] 被新 rewind 取代的旧 rewind 项：event_id 不回填、跳过与不可恢复清单丢失，计划没有说明（ADVISORY）

- 证据：
  - 424 行“入队新的 rewind 通知时，先移除队列中尚未送达的 rewind 项”。被移除的项带有 `EventID`，回填只在注入成功时发生（`undo_notice.go:180-185`），所以前一次 rewind 的 session 事件 `entry_seq` 永远是 0。414 行“队列中各项沿用现有的 event_id 回填”在这条路径上不成立。
  - 设计流程 3 要求“两者都回，但有不可恢复或跳过的文件：列出这些文件”（设计 240 行）。如果第一次是 both 且有跳过项，第二次是只回会话，第一次列出的文件不会送达模型。第二次通知已提示“磁盘没有回退，请先重新读取”，模型仍会重读文件，所以不导致错误结果。
- 必要性：“只送最新”与设计“rewind 通知取代队列中的通知”同向，机制本身成立；这里只要求写明后果。
- 建议：在 424 行补半句“被取代项的事件 `entry_seq` 保持 0，前一次的跳过与不可恢复清单只在 chat 结果中出现”，或者在新通知中合并前一次未送达的跳过清单（二选一，倾向前者，不新增逻辑）。

### S30 [LOW] 文字与小处（ADVISORY，S27 残留）

- “容器内 git 只对单条命令前缀……”现在挂在 fail-closed 列表下（220 行）。它是运行环境约定，不是停止条件，应移到“运行目录与环境”（212-214 行）。
- T5 文件清单仍写“`runner.go`（如需透传，F36）”（397 行），没有写定改动点。T2 已补 `runner.go`；T5 若确实无需改动应删去，否则写明改什么（例如“透传 `cfg.Tasks`”）。
- T4 文件清单（370 行）没有点名 `ErrRewindBusy` 声明在哪个文件。按 `withSessionUnlessBusy` 所在位置应为 `undo.go`，建议在清单的 `undo.go` 括注中补上。
- 修订记录 0.6 中 F41 同时出现在 S26 与 S27 两条（15 行），属排版，可不改。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，不新增入口）。
- 注入防护：已核对。`Kind` 只是会话 meta 中 JSON 字段，按值分支，不拼接查询或命令。
- 秘密不进日志或输出：已核对。通知正文与事件 attrs 未新增内容。
- 入参校验：已核对。`Kind` 为非 `rewind` 的值一律按 undo 项处理即可，计划写明 undo 项“为空”，旧数据兼容。
- 资源归属：已核对。队列按当前会话 sid 读写（`undo_notice.go:106-131`），不跨会话。

### 已核对且无问题

- 首行模板 1.3.0，状态 `Draft 0.6`。0.6 含语义变化（新增队列字段与取代规则、files scope 计数），递增版本符合 SR1103；修订记录只写语义变化。validator 通过。
- 新增机制的 scope necessity：`Kind` 是在不改变 undo 项放回位置（没有回退记录的会话行为不变，scope freeze 第 25 行）的前提下，让 rewind 项放回队首的最小手段；删除替代方案（所有项一律放回队首）会改变既有 undo 行为，违反 fail-closed，所以不成立。“只送最新”复用同一字段，不另加状态。
- `Kind` 写在会话 meta 的 JSON 队列里，不改 DDL、sugasdk API、CLI、ACP，在 scope freeze 内；`internal/personal/**` 在允许范围。
- 波次与文件不相交不变：`undo_notice.go` 只在 T5；T4 与 T2 在 W1 文件不相交。
- 可追溯表新增 S25、S26/F39/F41、F40 三行，都指向任务与用例。

## 覆盖缺口与限制

- 只读了 diff 和受影响的章节。未变化的章节（T0、T1、T3、T6–T8、Capability Discovery、回滚）沿用第 5 轮的结论。
- 治理观察：第 5 轮没有 CORE_BLOCKING，本轮属于额外确认。S28–S30 都不是 CORE_BLOCKING，按 SR1406 修正后不应再开第 7 轮，由主会话核对修正 diff 即可。

## 结论

**PASS**（规范与治理轴，Round 6）。

- 第 5 轮的 S25、S26 已关闭；S27 的②③已关闭，①与 T5 `runner.go` 残留并入 S30。
- 新发现：S28 为 CORE_CORRECTIVE（files scope 的 Turns 与入队条件定义不唯一）；S29、S30 为 ADVISORY。

## 剩余风险与后续

- 交人工批准前修正 S28。只是把定义写成唯一，如果选择第一种写法（按 `:undo` 的 Turns）可视为对 F40 的澄清；选择第二种写法属于语义变化，按 SR1103 递增版本，不需要新开评审轮次。
- S29、S30 可随 S28 一并处理；S30 中纯位置调整不递增版本。
- 开工时按计划重新采集 baseline。
