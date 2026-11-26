<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）实施计划 评审（第 2 轮 Standards/Governance 闭合确认）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `806af9b2`；subject `docs/plans/2026-11-26-background-fork-plan.md`；上轮 candidate `00c890f7`；连带核对设计 `docs/design/2026-11-26-background-fork-design.md` 在批准后的一行修正
- 修订版本: Draft 0.2
- 评审者: Standards/Governance 轴独立评审者（Claude 子 agent），计划评审第 2 轮（闭合确认），REPORT_ONLY
- 日期: 2026-11-26
- 排除项: Spec/Executability 轴的逐任务技术正确性；未变章节沿用第 1 轮结论；不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`、`tmp/idev-std/d/L1pSw4v7EitW46DpcPDbLQ/payload.md`（SR1103、SR1406、SR1408、SR1409、GR104、GR108、review-contract、planning-core-rigorous、plan-contract 含修订判定表与 Gate、review 模板）。
- 上轮本轴报告 `docs/review/2026-11-26-background-fork-plan/standards-review.md`（PS1–PS5）。
- `git diff 00c890f7..806af9b2 -- <plan> <design>`；`git show 806af9b2:<plan>` 全文（277 行）。
- 代码事实（均在 `806af9b2` 上）：`internal/chat/chat.go:21,23` 已 import `pkg/agentcore/tasks` 与 `tools/native`；`pkg/agentcore/layering_test.go:84` `TestLayering` 存在；`internal/personal/compact.go:838` 为 `requestBaseBetweenTurns` 唯一调用方；`internal/i18n/catalog/zh/{model,cmd}.ini` 分别只有 `[model]`、`[cmd]` 段，占位符为 `{name}` 形式；`native/agent.go:195-199` killed 分支写“已终止”且不通知；`internal/wiring/runtime.go` 964 行。
- 设计全文 grep：除关键流程 3 外不再有“分身被终止”等旧 kill 文案；验证要求第 215 行“kill 后 `stop=cancelled`”与新行为一致。
- 行数核对：设计评审报告 75 + 98 行；计划第 1 轮报告 123 + 134 行；计划 0.1 为 231 行、0.2 为 277 行；设计 227 → 229 行。
- `idevstd script validate_document --kind plan`（对 `806af9b2` 版本）：`ok: true`，`errors: []`，输出 `tmp/review/suga-fork-plan/r2-validate-plan.out`。`--companion` 对设计 ledger 未输出 `ceremony` 字段，仪式成本只能按文件行数人工核对。

## 验收标准

PS1–PS5 闭合核对：

| ID | 上轮 disposition | 本轮处理证据 | 结论 |
|---|---|---|---|
| PS1 `fork_reuse` | CORE_BLOCKING | T2 动作（第 145 行）首个 model 事件写 `fork_reuse` / `fork_reuse_note`，验证（第 147 行）断言“只有首个 model 事件带”；可追溯性第 262 行 | 关闭 |
| PS1 kill | CORE_BLOCKING | 用户“hao6 Q3 选 A”，不改 killed 分支（第 158 行）；T4 单测断言无通知、“已终止”、`stop=cancelled`（第 160 行）；T7 ③ 同断言（第 211 行）；完成定义 1、排除项“kill 通知”、可追溯性第 265 行、设计流程 3 同步改。现有代码 `agent.go:195-199` 即此行为，断言可达 | 关闭（取舍由用户定，符合 SR1401） |
| PS2 | CORE_CORRECTIVE（user_visible） | `model.fork.too_large` + `Hint()`（T1，第 134 行）并在 T4 工具错误与 T6 `:bg` 追加；`model.fork.directive` 含 4000 字符与文件:行号（第 94 行，T1 验证“指令文本”）；`fork_base` 由 T3 写入子会话 meta 并断言 | 关闭 |
| PS3 | CORE_CORRECTIVE | T5 文件含 `compact.go:838`；T4 改为可选接口，`SubAgents` 不变，完成标准“`internal/wiring` 未改仍能编译”；T6 文件补 `wiring/runtime.go`、`cli/chat.go`，删去“净增 ≤3 行”；i18n catalog 进 T1/T2/T3/T6 文件清单，段与 `{name}` 占位写明，`TestCatalogParity` 进验证；每任务验证首项 `go build ./...` | 关闭 |
| PS4 | CORE_CORRECTIVE | 新增“与设计代码组织的差异”三条（第 85–88 行）；T8 CHANGELOG 列出五项公开 API（第 219 行）并有 grep 验证 | 关闭（理由评估见下） |
| PS5 | CORE_CORRECTIVE | 人工 Gate 1 列出仪式成本（第 246 行），数字与文件行数一致 | 关闭 |

“与设计代码组织的差异”三条理由：

- `ExpandToolCalls` 导出到 `pkg/agentcore`：成立。分配规则的唯一 owner 是 `persistToolTranscript` 所在的 `pkg/agentcore`，导出后 runtime 与分身共用一份（CAP-03），依赖方向为 `internal/personal → pkg/agentcore`，不违反“`pkg/agentcore` 不依赖 internal”；T1 有基线回放的 characterization 与反向验证兜住落库行为不变；公开 API 新增已进 CHANGELOG。
- 可选接口 `native.ForkSeeder`：成立。`SubAgents` 不变，外部实现方不受影响；种子以 `any` 穿过 `native`，`pkg/agentcore` 不需要知道 `personal.ForkSeed`，分层守卫 `TestLayering` 进了 T4 验证。实现落在 `internal/wiring` 的 `subAgents`，经晚绑定的 `forkHost` 调 `Runner.ForkSeedForCall`，种子计算本体在 personal，wiring 只连线，符合 GR108。
- `:bg` 经 `Options.StartFork`：成立。chat 只依赖注入的函数字段与 `tasks.Info`、`personal.ForkUnavailableError`，依赖方向仍是 chat → personal / pkg；满额、Kind、通知走同一份 `AgentTool.StartFork`，避免第二份后台启动逻辑（SR1403）。`wiring.Runtime.StartFork` 只把 `ForkSeedBetweenTurns` 与 `AgentTool.StartFork` 串起来，属于连线，符合 GR108；chat 侧命令实现限定在 `chat_fork_bg.go`。

设计批准后改一行（第 3 项）见 PS6。

计划 Gate（第 4 项）：

- 仪式成本：有轮数、报告行数、被评文档行数，数字已核对。批准请求发出时需补上本轮（第 2 轮）报告行数，属请求消息内容，不记 finding。
- 执行者由用户指定：Gate 1 写明“同时指定 T1–T8 执行者模型”，前置检查第 108 行写明“模型由你在计划 Gate 指定”，T4/T6/T7/T8 的模型只是建议。符合。
- 核心任务标注：T1、T2、T3、T5 标题带【核心】，与前置检查第 108 行、Gate 1 一致。符合。
- 最小 Gate 集合：计划批准、当前执行请求、外部动作 Gate（push/tag/发版/安装逐项）齐全；T9 的真实 provider 调用与 `staticcheck` 下载已在 `host_or_non_offline_action=REQUIRED` 中列出并由执行请求覆盖。符合 plan-contract Gate 一节。

## 发现

### PS6 [LOW] 设计批准后的语义修正沿用 0.4，且 Q3 的选项原文未入库

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据:
  - 设计第 15 行修订说明：“0.4 批准后按用户 Q3 选 A 修正……不改版本号”；第 157 行把 kill 从“通知写‘分身被终止’、output 写最后一段回答”改为“不发通知、output 写‘已终止’”。这是用户可见行为（流程）的变化。SR1103 规定只有语义变化才新增版本，纯 identity/provenance 纠正才沿用原版本；plan-contract 修订判定表同理。此处方向反了：语义变了，版本没变。
  - 结果是同一个“Draft 0.4”对应两份内容：批准记录 `a8a2bc83` 指向旧 kill 行为，`806af9b2` 是新行为。计划第 57 行与 scope freeze 第 35 行引用“设计 0.4 + 批准后修正”，靠文字补救才能读清。
  - 授权依据“hao6 Q3 选 A”只有选项字母。Q3 的题面与 A/B 各自含义在仓库、计划评审报告与 issue `suga-hao6` 中都查不到（已 grep），“A = 沿用 6gcy 行为”目前只有作者转述。
- 影响: 不影响核心验收：kill 行为在计划完成定义、T4、T7、可追溯性与设计中已一致，且与现有代码相同；用户确实就这一项作了决定，不构成越权。影响的是追溯：日后按版本号对照批准记录会得到错误的 kill 行为，且无法从仓库核对用户选的到底是什么。
- 建议: 二选一，不需要再开评审轮（SR1406：已获用户直接决定的单项修正）。①设计升为 0.5，修订表加一行“按用户 Q3 选 A 修正关键流程 3 kill 行为（不通知、‘已终止’、`stop=cancelled`）；用户直接决定，未另行评审”，计划第 57 行改引 0.5（plan 的 identity 纠正，不升 plan 版本）；②保留 0.4 时，至少把 Q3 题面与 A 选项原文写进设计第 15 行或 `suga-hao6` 评论，并在计划批准请求里说明“0.4 内容在批准后改过一行”，请用户一并确认。两种做法都要补 Q3 原文。

## 覆盖缺口与限制

- 只读 `00c890f7..806af9b2` 的计划与设计 diff 及其依赖章节；未变章节（SR1301 核对、GR104/GR107/GR109/GR110/SR2002）沿用第 1 轮结论。
- 代码事实只做静态核对，未编译、未跑测试。
- `validate_document --companion` 未输出 `ceremony`，仪式成本按文件行数人工核对；设计评审“2 轮”的第 1 轮报告已被第 2 轮覆盖，173 行只计最后一轮报告，轮数以 ledger 为准（ledger schema 2.1.0，status `PASS`）。
- Q3 选项含义无法在仓库内核实，见 PS6。

## 结论

PASS：PS1（CORE_BLOCKING）已关闭，PS2–PS5 已关闭，本轮没有开放的 `CORE_BLOCKING`。新增 PS6 为 `CORE_CORRECTIVE`（`user_visible_effect: false`），按 2.2.0 规则不触发纠正确认轮，可在计划批准前直接修订并在 Gate 披露。

## 剩余风险与后续

- `internal/wiring/runtime.go` 现为 964 行，T3 绑定与 T6 `Runtime.StartFork` 都落在此文件，接近 GR104 上限；实施审查时核对行数，超 1000 行就把 fork 连线移到 `wiring/subagent_fork.go`。
- 计划评审目录下还没有 `review-ledger.json`；Full 档按 SR1409 需要 ledger，作者在本轮收口时补建，并把两轴第 2 轮报告行数补进批准请求的仪式成本。
- `wiring.Runtime.StartFork` 只做串联；`:bg` 的 description 由谁生成，计划没写，归 Spec 轴确认，不要在 wiring 里加文案或截断逻辑。
