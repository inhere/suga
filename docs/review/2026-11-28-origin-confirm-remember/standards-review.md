<!-- template_id: review; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 设计 Standards/Governance 轴评审（第 2 轮，闭合确认）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`，candidate commit `0c2f444c`（上轮 `146ac624`），subject `docs/design/2026-11-28-origin-confirm-remember-design.md`
- 修订版本: Draft 0.2
- 评审者: 独立评审子 agent（Claude Opus 5.5），Standards/Governance 轴；未参与本设计起草与 0.2 修订，未写本轮 Spec 轴报告
- 日期: 2026-11-28
- 排除项: Q1–Q3 选哪一项（交用户）；Spec/Executability 轴；实施代码；只报影响核心验收或安全的问题。本轮范围限于：OS1–OS4 闭合；新增接口的必要性与分层；残余风险/SR1301 披露与修订记录、处理表

## 输入与方法

- 全量读取 review 合同 payload（`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`，含 SR1301、review-contract、review-candidate-loop、review 模板、GR107–GR110）；design 合同 payload（`tmp/idev-std/d/LysoVXM1D86b1PtHtpG8jg/payload.md`）按 SR1409、“代码组织”要求检索相关段。
- 上轮本轴报告 `docs/review/2026-11-28-origin-confirm-remember/standards-review.md`（OS1–OS4）。
- `git diff 146ac624..0c2f444c -- <subject>`，并通读 0.2 全文（128 行），因 OS1 涉及方案 2/3、名词、流程、安全、验证多个章节。
- 核对代码：`pkg/agentcore/tools/origin.go:107-177`（`OriginHit`、`find`、`originSnippet`，窗口按每个字节偏移滚动计算）、`internal/wiring/origin_guard.go:104-215`（`Decide`、`confirm`，问句只显示 `hit.Tool` 与 `hit.Snippet`）、`pkg/agentcore/tools/approval.go:228-282`（`OriginJudge` 字段与 `ReportOriginJudge` 先例）、`pkg/agentcore/layering_test.go:23-44`。
- `git ls-tree 0c2f444c docs/review/2026-11-28-origin-confirm-remember/` 核对评审产物。

## 验收标准

- review-contract：OS1–OS4 逐项闭合；同类穷尽（同类再现标“上轮未穷尽”，沿用原 disposition）。
- review-candidate-loop：新增机制先答 scope necessity；Full subject 固定报告路径与 ledger，进行中 ledger 每轮随提交。
- SR1301 安全核对清单（放宽类改动）；SR1103 修订记录；GR108 与 `pkg/agentcore` 分层守卫。

### OS1–OS4 闭合

| 上轮 | 状态 | 依据 |
|---|---|---|
| OS1 记入范围超出所示片段 | 部分关闭；同类未穷尽，见 OS5 | 方案 3、D7、关键流程改为只 `Allow` 问句所示那一个命中段，不重跑 find（设计 66、90、111 行）；“另一段”被一并记入已关闭，验证与反向验证补了 P+X 用例（121、122、124 行）。但“命中段”定义（39 行）不按已允许状态切分，问句片段仍从段首起 48 字节，已允许前缀与新复制段相邻时用户看到的仍是旧前缀，新段随答 a 记入 |
| OS2 评审目录与 ledger | 设计侧关闭；ledger 未落盘，见 OS7 | 第 8 行已写目录与三个文件名 |
| OS3 残余与 SR1301 | 关闭 | 残余 1–4（97–100 行）逐条对应上轮 1–3 与原有尾部残余；第 60 行引用 SR1301；裁决默认开启且可关闭写入事实（52 行）；auto 下问句只显示一处来源写入事实（56 行） |
| OS4 代码组织 | 关闭 | 74–78 行列出 `origin.go`、`approval.go`、`observe.go`、`origin_guard.go`、i18n 各自改动与签名，参照 `ReportOriginJudge` |

### 新增接口 scope necessity 与分层

| 接口 | 必要性 | 评审 |
|---|---|---|
| `findAll`（不导出） | 是：D3 逐窗口判定须看全部段 | 不导出，不扩大包外接口；`Find`/`FindIgnoringURLs` 签名不变 |
| `Allow(session, hit)`（导出） | 是：wiring 答 a 后记入，且 wiring 不碰哈希（GR108） | `hit.windows` 不导出，包外无法伪造窗口，只能回传 `Find` 结果；方向 internal→pkg |
| `OriginHit.Remembered`、不导出 `windows` | 是：`Decide` 分支依据与记入对象 | 加切片字段后 `OriginHit` 不可 `==` 比较；已 grep，非测试代码无比较用法 |
| `ApprovalReport.OriginRemembered` + `ReportOriginRemembered` | 是：用户可见目标“trace 能看出是哪次放行”，AGENTS 要求 chat 行为用 trace 验证 | 与现有 `OriginJudge`/`ReportOriginJudge`（`approval.go:232,282`）同形 |
| 其他 | 无新增配置键、状态持久化或外部动作 | 已核对 |

分层：已核对。新增项都在 `pkg/agentcore/tools`（守卫测试 `layering_test.go:40` 允许 `.`→`tools`，`tools` 不引 `internal/*` 业务包）；`internal/wiring`、`internal/personal` 只调用导出项，方向正确；wiring 只做“问不问 + 回传 hit”，未内联哈希逻辑（GR108）。

### SR1301（本轮变更面）

| 项 | 结果 |
|---|---|
| 鉴权（授权） | 已核对：独立的另一段已不随答 a 记入；相邻段仍会，违反，见 OS5 |
| 注入防护 | 已核对：本项放宽的就是提示注入防护，残余 1–4 已披露；记入范围与问句展示不一致的残余未披露，见 OS6；无新增拼接 |
| 秘密不进日志/输出/仓库 | 已核对：attr 值为常量 `added`/`hit`；问句片段沿用 `redact.Secrets` + `displaySafe`（`origin_guard.go:119`）；不持久化 |
| 入参校验 | 已核对：只有 `ReplyAllowAlways` 记入，`d`/空/未知为拒绝且不记入（122 行） |
| 资源归属校验 | 已核对：`Allow` 带会话参数，集合挂在 `originSession`；子 agent 独立实例且 chat 为 nil |

### 修订记录与处理表

已核对：0.2 行存在，摘要只写语义变化（SR1103）；处理表覆盖 OS1–OS4 与 OF1–OF4，无遗漏；结论节写明第 1 轮 BLOCKED 与待复核状态（128 行）。

## 发现

### OS5 [HIGH] 已允许前缀与新复制段相邻时，问句仍显示旧前缀，答 a 把新段一并记入（OS1 上轮未穷尽）

- disposition: `CORE_BLOCKING` / `[SECURITY]`（SR1301 授权；同类再现，沿用 OS1 disposition，由我上轮建议①“首个未允许命中所在的连续段”未穷尽而来）
- 证据: 名词“命中段”= 相邻偏移都命中且来源工具相同的一串窗口（设计 39 行），不按已允许状态切分；方案 2“`Find` 返回首个未全部已允许的段”（65 行）；问句片段沿用 `hit.Snippet`，即段首起 48 字节（53 行；`origin.go:152,165`；`origin_guard.go:119,193-194`）；方案 3 记入“`hit` 自带的窗口”即整段（66、76 行）。窗口按每个字节偏移计算（`origin.go:147`），同一页面里相邻的 P 与 X 被模型连续复制时构成一个段。验证要求只断言“P + 新段”返回未允许命中（121 行），没约束片段起点与记入范围。缓解句“只记入用户看到的那一段”（95 行）在此场景不成立。
- 影响: 用户界面上看到的是已经允许过的那段旧前缀，新复制的段 X 不显示，答 a 后 X 也进了已允许集合；之后只带 X 的调用在裁决 ask/error/关闭时、auto 模式下无人确认直接执行。D3 想防的“已允许前缀 + 新复制段”对相邻段失效。
- necessity proof:
  - core_acceptance: 用户可见结果“答 a 后……只带已允许的片段不再弹询问”与缓解“只记入用户看到的那一段；同一调用里任何未允许段仍询问”（34、95 行）。
  - necessary_causal_chain: 页面含 `P`（如 44 字节以上的 sshc 前缀）后紧跟 `X`（恶意命令）→ 先前模型只复制 P，用户答 a，P 的窗口记入 → 之后模型把 P+X 连续复制进一条 `shell_exec` → 照文档 P+X 是一个段，未全部已允许，询问时片段显示段首 48 字节，与上次的 P 几乎一样 → 用户按“本会话允许该片段”答 a → 照方案 3 整段窗口（含 X）记入 → 后续一条只含 X 的命令（`tools.approval: auto`、裁决 ask）不弹任何询问直接执行，用户从未看见 X。
  - minimal_owner: 本设计名词“命中段”定义与方案 2/3（实现落在 `pkg/agentcore/tools/origin.go` 的 `findAll`）。
  - deletion_alternative: PASS：不新增机制，把“命中段”按已允许状态再切分即可（连续命中、同一来源工具、且已允许状态相同的窗口才算一段）；这样 P+X 拆成已允许的 P 与未允许的 X，`Find` 返回 X，片段从 X 的首个窗口起显示，`Allow` 只记入 X。
  - scope_effect: 无新增 Module/Interface/Schema/state/lifecycle/authority/external action。
- 建议: 在名词 39 行加“且已允许状态相同”，方案 2 写明片段从返回段的首个窗口起；单元验证补“P 已允许后 P+X 连续复制：返回段的 `Snippet` 不以 P 开头且包含 X 的开头、`Allow` 后只带 P 的调用仍 `Remembered`、只带 X 的调用在 `Allow` 前未允许”；反向验证补“段不按已允许状态切分”使该用例失败。

### OS6 [MEDIUM] 一段长于 48 字节时整段记入，但用户只看到 48 字节；缓解句写成“只记入用户看到的那一段”

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`（只改披露，不改行为）
- 证据: 问句片段固定 48 字节（`origin.go:165`），`Allow` 记入整段窗口（设计 76 行）；`approval: auto` 下问句不带调用全文（设计 56 行）。缓解 95 行“只记入用户看到的那一段”；残余 1–4（97–100 行）没有这一条。同类出现点仅此两处。
- 影响: 模型复制一段 2 KB 的外部脚本，用户只看到开头 48 字节答 a，之后只带脚本中任一 40 字节以上片段的调用都不再询问。这是 OS5 修正后仍保留的设计取舍（为让 44 字节以上的前缀可用），可以接受，但 Gate 上用户看到的说法偏乐观。
- 建议: 缓解句改为“只记入问句所示片段所在的那一段（段长可超过显示的 48 字节）”；残余里加一条：段长于显示部分时，答 a 也允许了未显示的部分。

### OS7 [LOW] 第 1 轮 BLOCKED 后 ledger 未随报告提交

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据: 设计第 8 行声明 `review-ledger.json`；`git ls-tree 0c2f444c docs/review/2026-11-28-origin-confirm-remember/` 只有 `spec-review.md`、`standards-review.md`；review-candidate-loop：进行中的 ledger（`IN_REVIEW`/`BLOCKED`，可只有 1 轮）每轮随提交。
- 影响: 第 1 轮 BLOCKED 状态与 OS1/OF1 处置只在报告里，没有机器可核对的 round progress；本轮结论落盘时缺前一轮 receipt。不影响设计内容。
- 建议: 主 agent 在提交本轮报告时一并补第 1、2 轮 ledger（第 1 轮 receipt 的 `source_report_commit_oid` 指向 `586f2166`），并在 ledger 里披露上轮两轴报告由同一子 agent 写出的 reviewer isolation 缺口。

## 覆盖缺口与限制

- 增量复核：读了 diff 与全文；未变章节（背景、范围、D1–D2、D4–D6）沿用上轮结论。
- 只读核对代码，未运行测试；`findAll` 的段切分实现细节只按设计文字判断。
- 第 1 轮两轴报告由同一子 agent 写出，reviewer isolation 不满足，本轮未能消除，只建议在 ledger 披露（OS7）。
- 未报不影响核心验收或安全的措辞、格式问题。

## 结论

BLOCKED：OS5 为开放 `CORE_BLOCKING [SECURITY]`（OS1 同类未穷尽）。OS2 设计侧、OS3、OS4 已关闭；新增接口都能追溯到核心目标，分层成立。OS6、OS7 为 `CORE_CORRECTIVE`，不单独阻断。

## 剩余风险与后续

- 本轮已是设计声明的第 2 轮上限（第 8 行停止条件），OS5 开放即交用户决定：批准第 3 轮，或接受 OS5 的一行定义修正作为“未复核修订”披露后进入 Gate。OS5 改动面很小（名词定义、方案 2 一句、1 条单元用例、1 条反向验证），不新增机制。
- Q2 选 B 时，OS5 的相邻段问题同样存在（段只按状态切分即可，与来源工具是否入键无关）；残余 1 的扩大已在 116 行写明。
