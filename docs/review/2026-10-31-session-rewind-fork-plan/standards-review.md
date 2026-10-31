<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `234a9a51`）
- 修订版本: Draft 0.7
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告（spec-review.md）
- 轮次: Round 7（变更范围评审）。读 `git diff abdb5bd9..234a9a51 -- <plan>`，以及直接依赖的章节：修订记录、批准证据、跨任务接口（错误值）、前置检查（运行目录与环境、fail-closed）、T4 文件清单与 planRewindFiles、T5（文件清单、步骤 7–8、验证）、可追溯性
- 日期: 2026-10-31
- 结论: **PASS**（没有开放的 CORE_BLOCKING）。S31、S32 为 CORE_CORRECTIVE，交人工批准前一行级修正即可
- 排除项: 未运行测试；未推演通知队列的多进程时序（归可执行轴）

## 输入与方法

- 规范：`idevstd load --delivery semantic --accept-delta --workspace <suga> --task plan --task review --agent claude --consumer-mode --thinking-mode rigorous`。结果 BOUND、FULL、18 项资源，payload sha256 `a7be77b2…` 已核验，与上一轮相同。
- 读取：0.6→0.7 计划 diff 与上列章节；第 6 轮规范轴报告；设计 0.2 流程 3 通知（236-241 行）。
- 结构校验：工作区计划与 `234a9a51` 一致；`idevstd script validate_document --kind plan` 为 `ok: true, errors: []`，622 行，不需拆分。round 6 批准证据 `8b0e8bf7` 为有效 commit。
- 代码核对：

| 引用 | 结果 |
|---|---|
| `undo_restore.go:62-67`、`undo_options.go:42-45` | `:undo` 的 Turns = 所选行中 `HasSnapshots()`（`len(Items)>0`）的行数；所选行已排除 undone（`selectUndoRows`、`undo_options.go:115`）。计划“目标范围内有快照目录的轮数”与 T4“已 undone 的轮跳过”合起来等价 |
| `undo_restore.go:105` 入队条件 `Turns > 0` | 与计划步骤 7 一致 |
| `undo_notice.go:31-41` pendingUndoNotice | 只有 `Text`、各项计数与 `Files`；没有跳过或不可恢复的文件清单字段。见 S31 |
| `undo_notice.go:60-86` undoNoticeText | 只列 `Files`（恢复与删除的路径），跳过与不可恢复只给计数。见 S32 |

## 验收标准

- plan 合同：修订记录、scope freeze 与 fail-closed 一致、任务五要素、跨任务签名归属、可追溯、新增机制的 scope necessity、人工 Gate。
- 评审合同：同类穷尽、五类 CORE_BLOCKING 与 necessity proof、按删除优先的顺序关闭。
- 其他规则：SR1103、SR1403、SR1406、SR1410、SR1301；GR104、GR109。

## 第 6 轮发现关闭状态

| ID | 状态 | 依据 |
|---|---|---|
| S28 Turns 与入队条件定义不唯一 | **已关闭** | 步骤 7 写定 Turns 与 `:undo` 同义（有快照目录的轮数，引用 `undo_restore.go:62-67`），Turns>0 调 `enqueueUndoNotice`，Turns=0 不入队、改写一条 rewind 会话事件（421-423 行）；新增“全部冲突”“全部无快照”两条用例（443-444 行）与可追溯行（607 行）。“只回文件”的单事件规则同步改写（416 行）。原建议的“不可恢复用例夹具”未写明，见 S33 |
| S29 被取代 rewind 项的后果 | **已关闭，但引入新机制** | 写明被移除项 `entry_seq` 不回填、事件保留（428 行）。同时选了“合并前一次的文件事实到新通知”，而非上一轮倾向的“只写明后果”，该机制落点未写，见 S31 |
| S30① 容器内 git 一条的位置 | **已关闭** | 移入“运行目录与环境”（217 行），属位置调整 |
| S30② T5 `runner.go`（如需透传） | **已关闭** | 从 T5 清单删去（399 行）。`cfg.Tasks` 由 Runner 方法直接访问，T5 不需改 `runner.go` |
| S30③ `ErrRewindBusy` 声明文件 | **已关闭** | T4 清单写明 `rewind_files.go` 声明（372 行），与跨任务接口“T4 声明”（160 行）一致 |
| S30④ 修订记录 F41 重复 | 未改，排版，不追究 | — |

## 发现

### S31 [LOW] 被取代 rewind 项“文件事实并入新通知”没有数据来源与合并规则（CORE_CORRECTIVE）

- 证据：
  - 428 行“被移除项的文件事实（跳过、不可恢复清单）并入新通知”。
  - 队列项 `pendingUndoNotice`（`undo_notice.go:31-41`）只保存渲染好的 `Text` 与计数，`Files` 只含恢复与删除路径；跳过与不可恢复的清单不在结构化字段中。计划在 0.6/0.7 只声明新增 `Kind` 一个字段（427 行）。
  - 因此实施者只能三选一：解析旧 `Text`、新增未声明的队列字段、或把旧 `Text` 原样拼进新通知。三者行为不同，且都没有去重规则：例如第一次 both 跳过了 X，第二次 both 恢复了 X，合并后同一通知既说 X 被跳过又说 X 已恢复。
  - 验证列表 440 行“连续两次含会话的 rewind：只送达后一次的通知”没有覆盖合并内容，合并行为没有可观察验收。
- 必要性（scope necessity）：上一轮已说明不合并也不会导致错误结果（第二次通知已要求模型重读文件）。按删除优先，合并不是核心验收所需；按 SR1403/SR1410 应选不新增逻辑的做法。
- 影响：实现不唯一，可能出现自相矛盾的通知；不涉及数据安全，不阻断核心验收，故不是 CORE_BLOCKING。
- 建议（倾向前者）：
  - 删去“并入新通知”，改为“前一次的跳过与不可恢复清单只在 chat 结果中出现”；或
  - 若保留，写明数据来源（例如 rewind 项新增 `skipped_files`/`unrecoverable_files`，omitempty）、去重规则（以新一次结果为准，新结果已处理的路径从旧清单剔除），并在 440 行用例中断言合并内容。保留属语义变化，按 SR1103 递增版本。

### S32 [LOW] “全部冲突”用例要求“通知跳过清单”，但指定的 `enqueueUndoNotice` 文案只给跳过计数（CORE_CORRECTIVE）

- 证据：
  - 443 行用例“files scope 全部冲突：Turns>0，写 EventUndo 并通知跳过清单”。
  - 步骤 7 要求调用与 `:undo` 相同的 `enqueueUndoNotice`（422 行），`Files` 为实际恢复与删除的路径（420 行）；设计流程 3 规定只回文件“沿用现有 undo 文案”（设计 240 行）。
  - `undoNoticeText`（`undo_notice.go:68-86`）对跳过只输出“跳过 N 个冲突”，只列 `Files`。全部冲突时 `Files` 为空，通知中不会有跳过清单。
- 影响：用例期望与指定机制矛盾。实施者若为通过用例去改 `undoNoticeText` 或往 `Files` 塞跳过路径，会改变 `:undo` 在没有回退记录的会话上的通知与 EventUndo attrs，触发 fail-closed（219 行）。用例措辞修正即可消除，不新增机制。
- 同类穷尽：同类“期望列出跳过/不可恢复清单”的出现点为 428 行（S31）与 443 行（本项）；420 行“N 个文件无法恢复”只要求计数，与现有文案一致，无问题。
- 建议：443 行改为“通知含‘跳过 N 个冲突’（与 `:undo` 文案一致）”。属澄清，不递增版本。

### S33 [LOW] 不可恢复用例夹具仍未写明（ADVISORY，S28 建议残留）

- 442 行“files scope 有不可恢复项：通知含‘无法恢复’提示，计数与 `:undo` 一致”。按 0.7 的 Turns 定义，只含已清理轮（无快照目录）时 Turns=0、不入队，用例会失败；需要至少一轮有快照目录。另外 rewind 的 Unrecoverable 计入已清理轮的项，而 `:undo` 根本不处理这类轮，“计数与 `:undo` 一致”只对有快照目录的轮成立。
- 建议：写明夹具为“一轮可恢复加一轮已清理”，期望 Turns=1、Unrecoverable 为已清理轮的写调用数，并去掉或限定“计数与 `:undo` 一致”。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，不新增入口）。
- 注入防护：已核对。本轮变更只涉及会话事件与队列 JSON，不拼接查询或命令。
- 秘密不进日志或输出：已核对。新增的 rewind 会话事件 attrs 为目标轮、scope、文件统计，不含正文。
- 入参校验：已核对。无新增外部入参。
- 资源归属：已核对。队列与事件仍按当前会话 sid 读写。

### 已核对且无问题

- 首行模板 1.3.0，状态 `Draft 0.7`。0.7 含语义变化（Turns=0 的事件、合并规则、skip 的 Action 映射），递增版本符合 SR1103；位置调整与清单修正一并记入，摘要以语义变化为主，可接受。
- 一次操作一条事件：files scope 下 Turns>0 只有 EventUndo，Turns=0 只有 rewind 会话事件；含会话 scope 只有 rewind 会话事件。与 F31 一致。“files scope 不写 rewind 记录”（437 行）指 meta 屏蔽记录，与 Turns=0 写会话事件不冲突。
- skip 映射：`RewindFileItem.Action` 记为 `conflict` 并带 Reason，与跨任务接口的 `RewindFileItem`（125 行起）一致。
- 波次与文件不相交不变：T5 去掉 `runner.go` 后，`runner.go` 只在 T2。

## 覆盖缺口与限制

- 只读了 diff 与依赖章节；其余章节沿用第 6 轮结论。
- 治理观察：第 5、6 轮均无 CORE_BLOCKING，本轮属额外确认。S31–S33 都不是 CORE_BLOCKING，按 SR1406 修正后不应再开第 8 轮，由主会话核对修正 diff 即可。S31 若选“删去合并”，同时回收了一个无必要性证明的新增机制，符合删除优先。

## 结论

**PASS**（规范与治理轴，Round 7）。

- 第 6 轮 S28、S30 已关闭；S29 已关闭但所选方案引入未落地的合并机制（S31）。
- 新发现：S31、S32 为 CORE_CORRECTIVE；S33 为 ADVISORY。

## 剩余风险与后续

- 交人工批准前修正 S31（倾向删去合并）与 S32（改用例措辞）；S33 可一并处理。
- 删去合并属对 0.7 语义的回退，按 SR1103 可递增为 0.8，但不需新开评审轮次；S32、S33 属澄清。
- 开工时按计划重新采集 baseline。
