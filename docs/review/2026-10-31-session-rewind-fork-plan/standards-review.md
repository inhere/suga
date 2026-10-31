<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `ff0586ba`）
- 修订版本: Draft 0.8
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告（spec-review.md）
- 轮次: Round 8（变更范围评审，严谨模式最后一轮）。读 `git diff 234a9a51..ff0586ba -- <plan>`，以及直接依赖的章节：修订记录、批准证据、T5 步骤 7–8 与验证列表
- 日期: 2026-10-31
- 结论: **PASS**（没有开放的 CORE_BLOCKING；第 7 轮 S31、S32、S33 均已关闭；新发现 S34、S35 为 ADVISORY）
- 排除项: 未运行测试；未推演通知队列的多进程时序（归可执行轴）

## 输入与方法

- 规范：`idevstd load --delivery semantic --accept-delta --workspace <suga> --task plan --task review --agent claude --consumer-mode --thinking-mode rigorous`。结果 BOUND、FULL、18 项资源，payload sha256 `a7be77b2…` 已核验，与上一轮相同。
- 读取：0.7→0.8 计划 diff 与上列章节；第 7 轮规范轴报告；设计 0.2 流程 3 相关段落。
- 结构校验：工作区计划与 `ff0586ba` 一致；`idevstd script validate_document --kind plan` 为 `ok: true, errors: []`，624 行，不需拆分。round 7 批准证据 `351c4d4b` 为有效 commit。
- 代码核对：

| 引用 | 结果 |
|---|---|
| `undo.go:449` classifySnapshot | `meta.Kind == purged` 或 `Unrecoverable != ""` 归为 unrecoverable；该 meta 位于快照目录内，所在行 `HasSnapshots()` 为真，Turns≥1，计数与 `:undo`（`undo_options.go:60-61`）一致 |
| `undo_notice.go:60-86` undoNoticeText | 跳过只输出“跳过 N 个冲突”；全部冲突时 Files 为空，文案为“跳过 N 个冲突”，与 0.8 用例期望一致 |
| `undo_restore.go:105` | Turns>0 入队，全部冲突时 Turns>0，与用例“写 EventUndo 并入队”一致 |
| `undo_notice.go:31-41` pendingUndoNotice | 0.8 前置拼接只用已有 `Text` 字段，不需新增字段，与计划“不新增字段”一致 |

## 验收标准

- plan 合同：修订记录、scope freeze、任务五要素、可追溯、新增机制的 scope necessity、人工 Gate。
- 评审合同：同类穷尽、五类 CORE_BLOCKING 与 necessity proof、删除优先。
- 其他规则：SR1103、SR1403、SR1406、SR1410、SR1301；GR109。

## 第 7 轮发现关闭状态

| ID | 状态 | 依据 |
|---|---|---|
| S31 被取代 rewind 项的文件事实合并无来源与规则 | **已关闭** | 改为旧项 `Text` 原样前置拼接（空一行），结构字段只取新项、不合并、不新增字段（430 行）；用例改为断言单条注入依次含两次文本（442 行）。数据来源唯一（已有 `Text`），无去重需求，实现唯一，有可观察验收。未取上轮倾向的“删去”，但所选做法不新增字段或逻辑分支，规模可接受。残留措辞见 S34、增长边界见 S35 |
| S32 全部冲突用例期望跳过清单 | **已关闭** | 445 行改为“通知含跳过计数（沿用 `:undo` 现有文案，不改文案）”，与 `undoNoticeText` 一致 |
| S33 不可恢复用例夹具 | **已关闭** | 444 行写明夹具为“快照 meta 标为 purged（`undo.go:449`）”。该 meta 在快照目录内，Turns≥1、入队，“计数与 `:undo` 一致”成立 |

## 发现

### S34 [LOW] 括注“只送最新状态”与前置拼接旧文本的新语义不一致（ADVISORY，可在实施任务书中处理）

- 证据：430 行同一句中，括注“连续两次 rewind 时只送最新状态”紧接“被移除项的正文 `Text` 原样放在新通知正文之前……两次操作的事实按先后顺序在同一条注入中送达”。
- 影响：读者可能理解为仍只送新项文本。后半句与 442 行用例已唯一确定做法，不导致实现错误。
- 同类穷尽：同类“只送最新/只送后一次”措辞在 0.8 中只剩 430 行此处（442 行旧用例措辞已改）。
- 建议：括注改为“连续两次 rewind 时合为一条注入”。属措辞，不递增版本。

### S35 [LOW] 多次连续 rewind 时前置拼接文本逐次累积，计划未写边界（ADVISORY，可在实施任务书中处理）

- 证据：430 行只描述“两次”；第三次 rewind 会把已含前两次文本的旧项整体前置，注入正文随中间无回合的连续 rewind 次数线性增长。
- 影响：交互场景下连续多次 rewind 不发消息较少见，每段文本短，不影响正确性或核心验收；不新增机制即可处理。
- 建议：任务书中写明“接受累积，不设上限”或“只保留最近一次被取代项的文本”二选一；倾向前者（最小实现，SR1403），并可在 442 行用例旁注明三次时的期望。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，不新增入口）。
- 注入防护：已核对。本轮变更只涉及队列 JSON 中已有 `Text` 字段的字符串拼接，不拼接查询或命令。
- 秘密不进日志或输出：已核对。拼接的是既有通知文案，不含会话正文或密钥。
- 入参校验：已核对。无新增外部入参。
- 资源归属：已核对。队列仍按当前会话 sid 读写。

### 已核对且无问题

- 首行模板 1.3.0，状态 `Draft 0.8`。0.8 改变被取代通知的处理语义，递增版本符合 SR1103；S32、S33 属澄清，一并记入，摘要以语义变化为主，可接受。
- 批准证据追加 round 7 `351c4d4b`，有效。
- 被移除项 `entry_seq` 不回填、事件保留的表述保留不变；前置拼接后注入失败仍整体放回队首，与既有放回逻辑兼容。

## 覆盖缺口与限制

- 只读了 diff 与依赖章节；其余章节沿用第 7 轮结论。
- 设计 0.2 第 152 行“队列中的 undo 通知同样丢弃，由本次 rewind 的通知取代”与计划（S22 起改为保留 undo 通知、rewind 插队首）的偏差已在前几轮记入计划偏差清单，本轮未变，不重复追究。

## 结论

**PASS**（规范与治理轴，Round 8，严谨模式最后一轮）。

- 第 7 轮 S31、S32、S33 全部关闭。
- 新发现 S34、S35 均为 ADVISORY，可在实施任务书中处理，不需要新候选或新评审轮次。

## 剩余风险与后续

- 人工计划批准可针对 Draft 0.8（`ff0586ba`）进行。
- 实施任务书中补一句处理 S34（措辞）与 S35（累积边界，倾向接受不设上限）。
- 开工时按计划重新采集 baseline。
