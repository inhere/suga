<!-- template_id: design; template_version: 1.1.1 -->
# 历史折叠按上下文体积触发设计

> 状态：Draft 0.1 / 待用户审批
> 日期：2026-10-21
> 关联：issue `suag-ldi7`；`docs/design/2026-10-12-history-folding-design.md`；`internal/personal/fold.go`、`internal/personal/context.go:398-409`、`internal/personal/compact.go:53-80`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-21 | 监督（Claude） | 用户 2026-10-21 开始第二十六批，ldi7 先出设计。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：折叠只在会话真的变长时才开始，短任务不再为折叠付出前缀重建；长会话在压缩之前先折叠，推迟压缩。
- allowed_scope：`internal/personal/fold.go`、`internal/personal/context.go` 的折叠接入、`internal/config/fold.go` 与配置注册、trace 事件、测试与文档。
- non_goals：改变折叠的内容（仍只去掉早期步骤的推理与长参数）；改变压缩；改变工具结果截断。
- expansion_policy：DEFER_OR_REQUEST。
- review budget：低–中。
- 停止条件：用户批准前不改代码。

## 背景与目标

折叠（`context.fold_every` / `context.fold_keep`）把请求视图里较早的 assistant 步骤去掉推理内容和超过 1000 字符的字符串参数，存储的历史不变。边界每 `fold_every` 步才移动一次，前缀缓存一批只失效一次。

自 488d6d5 起默认关闭（`fold_every` 默认 0）。issue 里记录的复测：折叠后重建前缀约占未命中输入的 35%，短任务（17 步）也会折叠一次、付出约 35k token 的重建。所以“每 N 步折叠”对短任务是净亏损，对长会话才有收益。

压缩阈值是 `窗口 - min(16000, 窗口×20%)`（`compact.go:53-80`）。目标是：请求体积低于压缩阈值的一定比例时完全不折叠；超过后才开启，并且一旦开启就在本段内保持，避免反复开关导致前缀来回变化。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| 折叠只作用于请求视图，存储不变 | `fold.go:16-24` | 可以随时开关，不涉及数据迁移。 |
| 边界按批移动，减少前缀失效 | `foldBoundary` | 开启后仍按 every/keep 移动。 |
| 折叠事件记录 `fold_boundary` 到会话 meta，分段时重置 | `fold.go` `metaFoldBoundary`、`segmentMeta` | “本段已开启”的状态放在同一处，分段时一起重置。 |
| Anthropic 主模型不折叠（thinking 签名绑定前缀） | issue 描述、llm 层 | 新触发条件不改变这条。 |
| 压缩阈值随窗口变化 | `compact.go:74-80` | 触发比例相对压缩阈值，而不是固定 token 数，适配不同窗口。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

1. **新配置 `context.fold_at`**（0–1 之间的比例，默认 0.5；0 = 不按体积触发）：本次请求在折叠前的估算体积超过 `fold_at × 压缩阈值` 时，本段开启折叠。
2. **开启后保持到本段结束**：记在会话 meta（与 `fold_boundary` 同处，键名如 `fold_active`），分段（压缩/交接）时重置。不因折叠后体积回落而关闭，避免来回切换导致前缀反复失效。
3. **开启后的批量参数**：用 `fold_every`/`fold_keep` 的配置值；`fold_every` 为 0 时使用按体积触发时的默认值 `every = 10`、`keep = 6`（保留最近 6 步完整）。
4. **兼容现有配置**：显式设置 `fold_every > 0` 且 `fold_at = 0` 时，行为与现在完全一样（一直按步数折叠）；两者都设时，体积达到后才开始按 `fold_every` 折叠。
5. **可观察**：开启时写一条 trace 事件（`fold`，attrs 带 `trigger: size`、请求体积、阈值），`suga inspect trace --analyze` 的“rebuilt (fold …)”照常归因；`suga config` 显示 `context.fold_at` 来源。
6. **Anthropic 主模型**：照旧不折叠。

## 关键流程

见“总体方案”1–3：组装请求 → 估算体积 → 未开启且超过 `fold_at × 阈值` 则标记本段开启并记事件 → 已开启则按 every/keep 折叠。

## 架构

- `internal/config/fold.go`：`FoldSettings.At`，读取与校验（0 ≤ at < 1）。
- `internal/personal/context.go`：折叠接入处判断是否开启；`fold.go` 增加按体积开启的判定与 meta 读写。
- `internal/personal/segment*.go`：分段时重置 `fold_active`（与 `fold_boundary` 一起）。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 触发比例相对压缩阈值 | 窗口从 32k 到 1M 都适用；与压缩使用同一套估算。 |
| D2 | 开启后本段保持 | 避免折叠让体积回落后又关闭，前缀来回失效。 |
| D3 | 默认 `fold_at = 0.5` | 短任务不折叠（不付出重建）；长会话在压缩前就开始减负。 |
| D4 | 显式 `fold_every` 单独设置时行为不变 | 已有配置的用户不受影响。 |

## 安全、数据、运维与回滚

- 只影响请求视图，存储不变；回滚：`context.fold_at: 0`。
- 风险：长会话在 50% 处开始折叠会让前缀重建一次，换来后续请求变小、压缩推迟；用真实长会话的 trace 验证净收益。

## 测试与验证

- 单测：低于阈值不折叠；越过阈值后开启并记录事件；开启后体积回落仍保持；分段后重置；`fold_every` 单独设置时与现状一致；`fold_at = 0` 关闭；Anthropic 主模型不折叠；开启后边界仍按批移动（前缀每批只变一次）。
- 反向验证：去掉“开启后保持”后“回落仍保持”用例失败。
- 真实验证：同一个长任务（例如在 suga 仓库里做一次 30 步以上的探索与修改）分别用 `fold_at: 0` 与 `0.5` 跑，比较总输入 token、缓存命中、压缩次数（`suga inspect stats` / `trace --analyze`）；短任务（<15 步）确认 `0.5` 下没有折叠事件。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 默认 `fold_at = 0.5`（相对压缩阈值）开启？ | 是 |
| Q2 | 开启后本段保持，不因体积回落关闭？ | 是 |
| Q3 | 按体积开启时默认 `every = 10`、`keep = 6`？ | 是 |
| Q4 | 实施前独立审核？ | 否（实施后用真实长会话对比验证） |

## 结论与人工计划 Gate

用相对压缩阈值的体积比例触发折叠、开启后本段保持，默认开启 `fold_at = 0.5`；用户批准 Q1–Q4 后派出实施，批准前不改代码。
