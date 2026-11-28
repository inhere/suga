<!-- template_id: review; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 设计 Spec/Executability 轴评审（第 2 轮，闭合确认）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`，candidate commit `0c2f444c`，subject `docs/design/2026-11-28-origin-confirm-remember-design.md`
- 修订版本: Draft 0.2（上轮 candidate `146ac624`，Draft 0.1）
- 评审者: 独立评审子 agent（Claude Opus 5.5），未参与本设计起草与 0.2 修订
- 日期: 2026-11-28
- 排除项: Q1–Q3 选哪一项；Standards 轴内容；按要求只报影响核心验收或安全的问题；未变章节沿用第 1 轮结论

## 输入与方法

- 读取 review 合同（payload 中“评审报告合同”“Review Candidate Loop”“严谨规划核心合同”）、上轮 `docs/review/2026-11-28-origin-confirm-remember/spec-review.md`（OF1–OF4）。
- `git diff 146ac624..0c2f444c -- <subject>` 全量，以及被修订的名词、总体方案、架构/代码组织、关键流程、安全、决策、验证要求各节。
- 对照 `0c2f444c` 下代码：`pkg/agentcore/tools/origin.go`（`Record`、`find`、`originWindowHashes`、`originSnippet`、`visitStrings`）、`internal/wiring/origin_guard.go`（`Decide`、`confirm`）、`pkg/agentcore/tools/approval.go:206-316`、`internal/personal/observe.go:340-400`、`test/ptye2e/fakellm_origin_test.go`、`test/ptye2e/chat_origin_judge_test.go`、`pkg/agentcore/tools/origin_test.go`（现有 `Find` 调用方）。
- 用 P+X、同页连续复制、前缀+不同尾部几个实例手推窗口与分段。

## 验收标准

设计“用户可见结果”与“验证要求”两节；review 合同对 `CORE_BLOCKING` 五类与 necessity proof 的要求；上轮 OF1–OF4 的建议。

### 上轮发现闭合核对

| 上轮 | 结果 |
|---|---|
| OF1（CORE_BLOCKING/SECURITY） | 已闭合。方案 3、D7、代码组织写明答 a 只 `Allow(session, hit)`，`hit.windows` 只含问句所示命中段，不重跑 find。按“命中段”定义（同一字符串内相邻偏移都命中且来源工具相同），P+X 中 P 的最后一个整窗在偏移 len(P)-40，跨边界的 39 个窗口不在任何来源里（除非页面 A 里 P 后正好接着 X 的开头），所以 P 与 X 一定分成两段；即使跨边界窗口落在 A 里，也会在工具从 A 换到 B 时断开。答 a 只记 P，之后只带 X 的调用 `Find` 返回 X 段且 `Remembered=false`，裁决 ask/error/关闭时进入询问。保证成立。可测：单元“对 P 段 Allow 后只带 X 仍返回未允许命中”、guard“P+X 答 a 后只带 X 仍 Ask”、反向验证“改为记入本次全部段”三处都写了，用真实 `ExternalOrigin` 对两个来源各 `Record` 一次即可构造 |
| OF2 逐窗口判定 | 已修正。名词节改为“(会话, 来源工具) → 窗口哈希，逐窗口判定”，与方案 2、D3 一致。核心场景：两条命令的前缀段若比页面多/少几个跨边界窗口，较短那条的窗口是较长那条的子集，答 a 记入较长的后较短的判为已允许；反之较长的多出新复制字节，重问，符合 D3 |
| OF3 签名 | 已修正。`OriginHit` 增 `Remembered bool` 与不导出 `windows`；`findAll(session, args, ignoreURLs) []OriginHit`；`Allow(session, hit)`。`Find`/`FindIgnoringURLs` 签名不变，现有调用方只有 `origin_guard.go:111-115` 与 `origin_test.go`，测试只读 `hit.Tool`/`hit.Snippet` 与 ok，不比较整个结构体，加不导出字段不会弄坏它们。wiring 包拿到的 `hit` 值复制时带着不导出字段，`Allow` 可在 tools 包内读取；`find` 与 `Allow` 分两次加锁，但记入的窗口取自 `hit`，中间的 `Record` 不影响记入内容 |
| OF3 ApprovalReport | 已修正。`OriginRemembered string` + `ReportOriginRemembered`，`observe.go` 在工具事件 attr 写 `origin_remembered`，与现有 `OriginJudge` 一样挂在同一事件上（`observe.go:384-391`），端到端从 trace 工具事件读取可行 |
| OF4 验证 | 已修正。guard 用例分了内层 auto / 默认 ask / deny 规则与假裁决；端到端写了 `approval: auto`、fake judge 固定 `ask`（fakellm 的 `JudgeReply` 已支持，`fakellm_test.go:113-118`）、第二条输出标记与 trace 成功工具事件断言；淘汰预期写明（淘汰后 `Find` 为 false；同工具重新 `Record` 仍 `Remembered`，换工具则不是），与 `Record` 的淘汰逻辑（`origin.go:86-104`，已允许集合另存、不随淘汰清除）一致；补了裁决 ask 跳过用例与“跳过分支挪到无 chat 判断之前”等反向验证 |

### 新问题核对：`Find` 返回首个未允许段，在从未答 a 时是否改变行为

不改变。从未 `Allow` 时没有段是已允许的，“首个未允许段”就是首段；首段的起点就是旧 `find` 在 `visitStrings` 顺序（map 键排序、列表顺序）下的第一个命中偏移，来源工具同为 `s.names[s.index[h]]`，`Snippet` 仍是 `originSnippet(norm[起点:])`，`ok` 相同，`Remembered=false`。差别只在不再提前停止遍历，没有副作用；`originWindowHashes` 本来就对整串算一遍，额外开销只是多走后面的字符串。所以 `ReportDanger` 理由、裁决输入（`Snippet`/`Source`）与问句在未答 a 时与现状一致。设计没有逐字写“段的 `Snippet` 取段起点”，但“签名不变、取首段”只有这一种读法，不单列发现（并入 OF5 建议）。

## 发现

### OF5 [MEDIUM] 问句只显示命中段开头 48 字节，记入的却是整段；“只记入用户看到的那一段”说得过满

- disposition: `CORE_CORRECTIVE`，`user_visible_effect: true`
- 证据: 设计“安全”节缓解第 1 条“只记入用户看到的那一段”；方案 3“问句所示那一个命中段”；问句片段为 `originSnippet(norm[i:])`，即段起点起 48 字节（`origin.go:152,165-177`，`origin_guard.go:119,193-194`）；`approval: auto` 下 `previewAsk` 不并入调用详情（`chat_approver.go:296`），用户看不到段的其余部分。同类出现点：(1) 同页连续复制一长段（P 后紧跟同页的 Y），段为 P+Y，问句只显示约 P 的部分；(2) 已允许 P 之后，调用带“P+同页连续的 Y”，该段部分已允许、整体未允许，`Find` 返回它，问句片段从段起点（已允许的 P）开始，新出现的 Y 不在显示的 48 字节里。
- 影响: 两种情况下都仍会询问，核心性质“带未允许复制字节的调用仍询问”成立，未答 a 时现状的 y 也是只看 48 字节就放行整条调用，所以不构成阻断。但答 a 会把用户没看到的 Y 也记入，之后只带 Y（≥40 字节）的调用不再问；情况 (2) 里用户看到的是自己刚允许过的熟悉前缀，更容易随手答 a。Gate 的残余清单没有列这一点，安全节的说法与实际不符。
- 建议: 二选一写进设计：① 问句片段从段内首个未允许窗口起取 48 字节（未答 a 时首个未允许窗口就是段起点，行为不变），并在残余里写明“记入整段，段可能长于所示片段”；② 或把记入范围收窄为“所示片段覆盖的窗口”。同时把安全节第 1 条改为准确说法，并写明段的 `Snippet` 取哪个起点。验证补一条：已 `Allow` P 后，“P+同页 Y”的问句片段包含 Y 的开头（选 ①）。

## 覆盖缺口与限制

- 只读核对，未运行测试；分段与命中为手推。
- 端到端要“两条不同命令”，现 `fakellm` origin 模式只有 web_fetch + 一条 shell_exec（`fakellm_origin_test.go:9-16`），需给夹具加第二步与第二条命令字段；属测试夹具扩展，做法唯一，不列为发现。
- 段被不显著窗口（长空白或标点串，hash 为 0）或“同一窗口最新来源换了工具”切开时，同一段复制会分成几段，答 a 一次只记一段，下次还会再问一次；只影响询问次数，不影响安全，未列为发现。
- 两轴独立性：本报告由独立子 agent 只写 Spec 轴；Standards 轴另评。

## 结论

PASS：上轮 OF1（CORE_BLOCKING [SECURITY]）已闭合且可测，OF2–OF4 修正正确；本轮未发现新的 `CORE_BLOCKING` 或文档内矛盾。新增 OF5 为 `CORE_CORRECTIVE`（user_visible_effect: true）。

## 剩余风险与后续

- OF5 带 `user_visible_effect: true`：按 2.2.0 规则，若两轴本轮均无 `CORE_BLOCKING`，修订 OF5 后可做一轮纠正确认轮；不做则按“未复核修订”在 Gate 披露。
- 实施时注意：`findAll` 要按段计算 `Remembered`；记入的窗口取自 `hit`，不要在 guard 里为记入再查一次。
