<!-- template_id: design; template_version: 1.1.1 -->
# 历史折叠按上下文体积触发设计

> 状态：Draft 0.3 / 已批准，可实施（“0.3 修订”优先于后文）
> 日期：2026-10-21
> 关联：issue `suag-ldi7`；`docs/design/2026-10-12-history-folding-design.md`；`internal/personal/fold.go`、`internal/personal/context.go:398-409`、`internal/personal/compact.go:53-80`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-21 | 监督（Claude） | 用户 2026-10-21 开始第二十六批，ldi7 先出设计。 |
| 0.2 | 2026-10-21 | 监督（Claude） | 用户：“加进去，ldi7 两点一起按推荐”。新增第二点：折叠边界之前，本地可重复执行的只读工具（工具元数据 ReadOnly、非 External、Prefetchable）的较大结果换成一行说明；Q1–Q4 按推荐批准。 |
| 0.3 | 2026-10-21 | 监督（Claude） | 用户批准调整方向：真实对比显示“边界前全折叠旧工具结果”让模型反复重读，总输入反而更高；改为只在结果已被后来的调用取代（重复读取）或已过时（文件被修改）时才折叠，其余旧结果原样保留。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 0.3 修订（优先于后文）

0.2 的第 2 点（折叠边界之前所有可重读工具的大结果）在真实对比中被证明是净亏损，改为“只折叠已过时或重复的结果”。按体积触发（`context.fold_at`、开启后本段保持、every/keep）与推理/长参数折叠**保持不变**。

真实对比数据（2026-10-21，同一探索任务、`command-code/deepseek-v4.1-flash`、`context_window: 96000`，隔离 SUGA_HOME 与最小配置）：

| 指标 | A：`fold_at: 0` | B：`fold_at: 0.5`（0.2 全折叠） |
|---|---|---|
| model calls / tool calls | 13 / 23（file_read 11、file_grep 11、file_list 1） | 81（撞 80 步上限收尾）/ 132（file_read 109、file_symbols 11、file_grep 10、file_list 2） |
| 总输入 token | 688.3k | 2.47M |
| 未命中输入 | 67.8k（fresh 67.7k + rebuilt 0） | 324.6k（fresh 222.9k + rebuilt 100.9k，fold ×12） |
| 每请求平均输入 | 52.9k | 30.5k（−42%） |
| 工具结果折叠累计 | 0 | 112 条（`tool_chars` 570k → 14.3k） |
| 耗时 | 114.7s | 376.4s |

结论：折叠把单次请求体积降了 42%，但被折掉的旧结果仍是模型需要的信息，它大量重读（`file_read` 11 → 109，轨迹里 `compact.go ×10`、`context.go ×8`），步数与总输入都翻了几倍。因此：

1. **只在折叠边界之前折叠**，且结果必须**已被后来的调用取代或已过时**；其余旧结果原样保留。
   - **重复**：后面以相同的工具和参数调用过——`file_read` 为同一路径、且后面的 `offset`/`limit` 行范围（缺省 offset 1、limit 2000）覆盖它；`file_grep`/`file_find`/`file_list`/`file_symbols`/`file_refs` 为参数完全相同（JSON 值逐项相等）。说明写 `[已折叠：后面第 N 步重新读取了同一内容]`。
   - **过时**：`file_read` 读的文件后来被 `file_edit`/`file_write`/`file_remove` 用同一路径修改过。说明写 `[已折叠：<path> 在后面第 N 步被修改，此处是修改前的内容]`。其他工具写下的文件（`shell_exec` 等）无法从调用参数判断，不算过时。
   - 判定按时间顺序取**第一个**使结果失效的步骤：修改之后的重新读取返回的是新内容，不再代表被折掉的文本。
2. **N 的口径**：请求视图里的步数，与此前折叠边界一样按 assistant 消息计数（1 起）；后面被当作目标的读取/修改本身保留原文。
3. **不折叠的对象不变**：错误结果、工具元数据不是 `ReadOnly && !External && Prefetchable` 的工具、文本 ≤ 1000 字符的结果；存储不变，只影响请求视图；前缀仍按批变化。
4. **只看请求视图**：工具名、调用参数与顺序，不读文件、不查存储（判定与真实文件系统状态可能不一致时，以视图为准）。
5. **可观察与测试**：`fold` 事件的 `tool_results_folded`、`tool_chars_before/after` 不变（现在只统计真正被取代的结果）；测试覆盖重复/覆盖范围/编辑致旧/边界之后/元数据与错误/说明文字，反向验证“改回边界前全折叠”时“未被取代的旧结果保留”失败。

## 0.2 修订（优先于后文）

用户 2026-10-21 批准两点一起做：

1. **按体积触发**（原方案，Q1–Q4 按推荐）：`context.fold_at` 默认 0.5（相对压缩阈值），开启后本段保持，开启时默认 every 10 / keep 6，显式 `fold_every` 单独设置时行为不变。
2. **折叠可重复执行的只读工具的旧结果**（新增）：
   - 范围：折叠边界之前（与推理、长参数同一个边界）的 tool 结果，且该工具的元数据 `ReadOnly && !External && Prefetchable`（当前为 file_read、file_grep、file_find、file_list、file_symbols、file_refs）；按元数据判定，不写死工具名，以后新增的同类工具自动适用。
   - 只折叠较大的结果：结果文本超过 1000 字符（与长参数同一个阈值 `foldArgChars`）；小结果保持原样。
   - 换成一行说明：`[已折叠的 <工具名> 结果：原 <N> 字符；参数见上方调用；文件可能已变化，需要时重新调用读取]`。对应的 assistant 工具调用保留工具名和短参数（path、行号范围、pattern 等），模型知道当时读了什么。
   - 不折叠：`web_fetch`、`web_search`、ledger 搜索等 External 工具（重新获取可能得到不同内容）；`shell_exec` 等非只读工具（不能重放）；错误结果（保留原文，便于理解后续步骤）。这些工具较大的结果已由输出治理转存成摘录加 ref（> 8000 字节），留在上下文里的部分本来就小。
   - 存储不变；只影响请求视图；前缀仍按批变化（与推理折叠同一边界）。
3. **可观察**：fold 事件 attrs 增加 `tool_results_folded`（条数）与 `tool_chars_before/after`；`suga inspect trace --analyze` 的折叠归因照常。
4. **测试补充**：只折叠满足元数据条件的工具、阈值以下不折叠、External/非只读/错误结果不折叠、说明文字含工具名与原长度、调用参数保留；真实验证里比较折叠前后的请求体积（file_read 为主的探索任务）。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：折叠只在会话真的变长时才开始，短任务不再为折叠付出前缀重建；长会话在压缩之前先折叠，推迟压缩。
- allowed_scope：`internal/personal/fold.go`、`internal/personal/context.go` 的折叠接入、`internal/config/fold.go` 与配置注册、trace 事件、测试与文档。
- non_goals：改变压缩；改变工具结果截断与转存；折叠 External 或非只读工具的结果。
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
| Q1 | 默认 `fold_at = 0.5`（相对压缩阈值）开启？ | 已定：是 |
| Q2 | 开启后本段保持，不因体积回落关闭？ | 已定：是 |
| Q3 | 按体积开启时默认 `every = 10`、`keep = 6`？ | 已定：是 |
| Q4 | 实施前独立审核？ | 已定：否（实施后用真实长会话对比验证） |

## 结论与人工计划 Gate

用相对压缩阈值的体积比例触发折叠、开启后本段保持，默认开启 `fold_at = 0.5`；用户已于 2026-10-21 批准（含 0.2 新增的可重读工具结果折叠），派出实施。
