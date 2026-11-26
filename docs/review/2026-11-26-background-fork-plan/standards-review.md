<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）实施计划 评审（第 3 轮 Standards/Governance 闭合确认）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `1a8331ed`；subject `docs/plans/2026-11-26-background-fork-plan.md`（连带设计 `docs/design/2026-11-26-background-fork-design.md`）；上轮 candidate `806af9b2`
- 修订版本: 计划 Draft 0.3；设计 Draft 0.5
- 评审者: Standards/Governance 轴独立评审者（Claude 子 agent），计划评审第 3 轮（闭合确认，用户“hao6 Q6 选 A”授权），REPORT_ONLY
- 日期: 2026-11-26
- 排除项: Spec/Executability 轴的逐任务技术正确性（PF10–PF14 的实现细节、并发与 TOCTOU 等）；未变章节沿用前两轮结论；不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（SR1103、SR1406、SR1408、SR1409、GR104、GR108、planning-core-rigorous 轮次规则、review 模板 1.2.0）。
- 上轮本轴报告 `docs/review/2026-11-26-background-fork-plan/standards-review.md`（PS6）。
- `git diff 806af9b2..1a8331ed -- docs/design docs/plans`；`git show 1a8331ed:` 计划全文（290 行）与设计全文（230 行）。
- 代码事实（工作区，静态核对）：`internal/personal/observe.go:76` `recorder`、`:122` `attemptObserver`、`:169` purpose attr；`internal/personal/runner.go:211` `Config.Translator`、`:467` `New` 中建 recorder；`pkg/agentcore/tasks/tasks.go:245` `Limits()`、`:778` `Running(sessionID)`；`internal/wiring/runtime.go` 964 行、`subagent.go` 303 行。
- `idevstd script validate_document --kind plan`（对 `1a8331ed` 版本）：`ok: true`，`errors: []`，输出 `tmp/review/suga-fork-plan/r3-validate-plan.out`。
- `ls docs/review/2026-11-26-background-fork-plan/`：只有 `spec-review.md`、`standards-review.md`，无 `review-ledger.json`。
- `git grep Q6`：本特性文档内只有计划修订表与 Gate 的“hao6 Q6 选 A”，无 Q6 题面。

## 验收标准

1. PS6 闭合：

| 要求 | 证据（`1a8331ed`） | 结论 |
|---|---|---|
| 设计升 0.5 | 设计第 4 行状态 `Draft 0.5 / 已批准进入计划（0.4 批准；0.5 仅含 Q3 修正）`；修订表 0.5 行写明 kill 改动、PF4/PS1/PS6 来源、“用户直接决定，未另行评审” | 关闭；语义变化随版本递增，符合 SR1103 |
| Q3 原文 | 修订表下方一行写出 Q3 题面与 A/B 含义及用户原话 | 关闭 |
| 批准记录 | 批准记录保留 0.4 原话，追加“0.4 批准；0.5 仅含 Q3 修正（用户直接决定）”，版本号与批准内容不再错位 | 关闭 |
| 计划引用 | 计划核心目标、scope freeze、“输入与批准证据”均改为设计 0.5，并注明 0.4 批准提交 `a8a2bc83` | 关闭 |

2. 修订是否超出设计或破坏分层：

- observe.go 的 recorder 写 `fork_reuse`：设计关键流程 6 与 F4 要求“分身首个 model 事件带 attr `fork_reuse`”，没有规定写入位置。recorder 是 trace attrs 的唯一 owner（`observe.go:169` 已按同样方式写 `purpose`），每个分身 Runner 有自己的 recorder（`runner.go:467`），“该会话第一个无 purpose 的 model 事件”与设计“首个 model 事件”一致（compact 调用不计）。改动留在 personal 包内，不经 `ModelEvent.Metadata` 透传，也没有新增跨层通道。不超出设计，分层不变。
- `native.ForkLimitError{Running, Limit}`：设计 D5 与关键流程 2/3 规定满额直接拒绝，模型与 `:bg` 各给一种提示。这个类型只是把已有的满额判定结构化，方便 `:bg` 用 `errors.As` 取数字填 `cmd.bg.full`。定义在 native（`StartFork` 的 owner），chat 本就依赖 native，依赖方向不变；`Running`/`Limits` 是现有 API。属于设计行为的实现，不是新机制。
- `ForkUnavailableError.hint`：由 Runner 用现有 `Config.Translator` 填译文，做法与 deadline/budget 的提示相同（`runner.go:496`），chat 只取 `Hint()`，不碰键名。不超出设计，没有第二份文案逻辑。
- `subagent_fork.go`：设计“代码组织”第 137 行原文就是“接近 GR104 上限时拆到 `subagent_fork.go`”；计划 T3/T6 把 fork 连线放进 `wiring/subagent_fork.go`，`runtime.go` 只留绑定一行和赋值一行。处理了上轮“剩余风险”里 964 行的问题，符合 GR104/GR108。
- PF11 撤回 `ForkSeedForCall` 的 host hook 拒绝：设计 F7 原文“模型触发总用当步请求（`prefix`）”，host hook 拒绝只针对 `:bg` 的重建路径。0.3 回到了设计，不算扩围。
- 代码位置小差异（非 finding）：设计第 136 行把 tracker 与 `ForkSeed` 放在 `personal/subagent_fork.go`，计划拆到 `subagent_fork_seed.go`，`fork_reuse` 写在 `observe.go`。两者同包，不涉及依赖方向，“与设计代码组织的差异”一节可以不列。

3. Gate 完整性：

- 仪式成本：Gate 1 列出设计评审 2 轮（173 行，设计 227 行）、计划第 1 轮（257 行，0.1 为 231 行）、第 2 轮（77 + 100 = 177 行，0.2 为 277 行）以及第 3 轮授权原话。已逐个核对文件行数，数字一致。还缺计划 0.3 的行数（290 行）和本轮报告行数，这两项属于批准请求消息的内容，不记 finding。
- 执行者由用户指定：Gate 1 写“同时指定 T1–T8 执行者模型”，前置检查第 121 行写“模型由你在计划 Gate 指定”，其余模型只是建议。符合。
- 核心任务：T1、T2、T3、T5 标题带【核心】，与第 121 行、Gate 1 一致。符合。
- 第 3 轮授权：第 2 轮 Spec 轴有开放的 `CORE_BLOCKING`（PF10、PF11），按 planning-core-rigorous 规则开第 3 轮需要用户批准。批准原话已写进计划修订表 0.3 行和 Gate 1。ledger 的记录见 PS7。

## 发现

### PS7 [LOW] 计划评审仍无 review-ledger，第 3 轮授权没有记进 `continuation_approvals`

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据: 计划第 50 行自定 review budget 为“SR1409 Full 档两轴评审”，SR1409 规定 Full 档要有 ledger。planning-core-rigorous 规定第 k 轮（k≥3）的批准原话要写进 ledger 的 `continuation_approvals: [{round, evidence}]`，缺了会报 `REVIEW_CONTINUATION_APPROVAL_MISSING`。`docs/review/2026-11-26-background-fork-plan/` 下至今没有 `review-ledger.json`，上轮“剩余风险与后续”已经提过。另外，“hao6 Q6 选 A”和 PS6 之前的 Q3 一样只有选项字母，仓库里查不到 Q6 的题面。
- 影响: 不影响核心验收和安全。授权确实存在，原话已写在计划修订表和 Gate 里，这一轮没有越权。影响的是机器核验：计划评审得不到可校验的 PASS 记录，第 3 轮按规则会缺授权。
- 建议: 本轮收口时补建计划评审 ledger，覆盖两轴三轮的 verdict 与 candidate（`00c890f7`、`806af9b2`、`1a8331ed`）。写入 `continuation_approvals: [{round: 3, evidence: "hao6 Q6 选 A"（附 Q6 题面：第 2 轮仍有 PF10/PF11，A = 再评一轮）}]`，再用 `validate_document --companion` 校验。这是元数据补录，不升计划版本，也不需要再开一轮（SR1406）。

## 覆盖缺口与限制

- 只核对了 `806af9b2..1a8331ed` 的计划与设计 diff 及相关代码位置。PF10–PF14 的技术正确性归 Spec 轴，比如 `attemptObserver` 的“首个”标记是否需要并发保护、`StartFork` 预检与 tasks 管理器自身满额判定之间的竞态。
- 代码事实只做了静态核对，没有编译，也没有跑测试。
- 没有 ledger，轮次与授权只能按计划文本人工核对，见 PS7。

## 结论

PASS：PS6 已关闭；本次修订（recorder 写 `fork_reuse`、`ForkLimitError`、`ForkUnavailableError.hint`、`wiring/subagent_fork.go`、撤回模型触发的 host hook 拒绝）都在设计 0.5 范围内，没有引入新机制，也没有分层问题。Gate 写明了仪式成本、第 3 轮授权、由用户指定执行者，并标出了核心任务 T1/T2/T3/T5。本轴没有开放的 `CORE_BLOCKING`。新增 PS7 为 `CORE_CORRECTIVE`（`user_visible_effect: false`），补录 ledger 即可，不触发纠正确认轮。

## 剩余风险与后续

- 批准请求要补上计划 0.3 的行数（290 行）和第 3 轮两轴报告的行数。
- 实施审查时核对 `runtime.go` 是否只增了两行，fork 连线和文案是否都在 `wiring/subagent_fork.go` 和 `chat_fork_bg.go` 里。
