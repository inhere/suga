<!-- template_id: review; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 实施计划 第 1 轮 Spec/Executability 评审

## 评审目标与范围

- 目标: suga 仓库 `/d/work/inhere/my-tools-dev/inhere-tools/suga`，`docs/plans/2026-11-28-origin-confirm-remember-plan.md`，candidate commit `6bf852a6`
- 修订版本: Draft 0.1（实现已批准设计 `docs/design/2026-11-28-origin-confirm-remember-design.md` 0.4）
- 评审者: 独立 reviewer（Spec/Executability 轴），REPORT_ONLY，未修改仓库
- 日期: 2026-11-28
- 排除项: Standards/Governance 轴（另一 reviewer）；执行者模型选择；设计本身的取舍（已批准）

## 输入与方法

- 全文读取：review-contract 与 plan-contract payload、`AGENTS.md`、计划与设计全文。
- 对照代码核对：`pkg/agentcore/tools/origin.go`、`origin_test.go`、`approval.go`；`internal/wiring/origin_guard.go`、`origin_judge_test.go`、`origin_guard_test.go`、`chat_approver.go`、`chat_approver_test.go`、`admit_ledger_test.go`、`memory_trusted_test.go`；`internal/personal/observe.go`、`observe_origin_judge_test.go`；`pkg/chatui/approval.go`；`internal/chat/ask.go`、`render.go`；`test/ptye2e/fakellm_test.go`、`fakellm_origin_test.go`、`chat_origin_judge_test.go`、`chat_trusted_memory_test.go`、`smoke_helpers_test.go`；i18n `host.ini`（zh/en）；Makefile `smoke-go`。
- 方法：逐任务核对文件/符号/签名、`-run` 名与 build tag；对 R1–R9 逐项推演“变异能否编译、是否让目标断言失败”；推演 `approval: auto` + 裁决 ask 下两次调用的完整路径；对照设计“验证要求”逐条找落点；grep 旧问句文案与片段相关断言评估 D10 影响。

## 验收标准

- 只读计划与设计即可唯一、正确执行；核心验收（计划完成定义 1–4）每项有可执行验证手段。
- 分级按 review-contract：`CORE_BLOCKING` 仅限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 且附 necessity proof；其余 `CORE_CORRECTIVE`（标 `user_visible_effect`）或 `DEFERRED_ENHANCEMENT`；同类穷尽。

## 已核实无问题的项（节选）

- 符号与签名：`ExternalOrigin`、`originSession`、`OriginHit`、`find`、`visitStrings`、`originWindowHashes`、`originSnippet`/`originSnippetBytes`、`normalizeOrigin`、`originGuard.Decide/confirm`、`originConfirmedKey`、`ApprovalReport`、`ReportOriginJudge` 均存在且与计划描述一致；`Allow` 在 `confirm` 持有 `a.mu` 时取 `o.mu`，无锁序问题。
- `-run`：`TestExternalOrigin`（tools 包）、`TestLayering`（`pkg/agentcore/layering_test.go:84`）、`TestOriginRemember|TestOriginGuardJudge|TestExternalOriginGuard|TestSubAgentExternalOriginGuard`、`TestObserveWritesOriginJudgeAttrs` 均能匹配；ptye2e 命令带 `-tags ptye2e`，新文件首行 `//go:build ptye2e`；`make smoke-go` 跑整个 `./test/ptye2e/`，新场景自动纳入。
- `approval: auto` + 裁决固定 ask：第一条 `Decide` → 裁决 ask → `g.chat != nil` → 未 Remembered → `confirm` 问人（`previewAsk` 在 auto 下返回 false，问句只有来源/片段/裁决理由）→ 答 a → 内层带 `originConfirmedKey`，策略 auto 放行；第二条 → 裁决 ask（`origin_judge=ask`）→ Remembered → 内层不带 key，auto 放行。确实“第一条问、第二条执行”。
- 屏幕文案：`internal/chat/render.go:663` 的 `ApprovalPrompt` 原样输出 `q.Prompt`，故新 zh 问句 `执行 shell_exec？[y]是 [N]否 [a]本会话允许该片段：` 会出现在屏幕上，`Expect("执行 shell_exec？")` 与 `本会话允许该片段` 均可匹配；ptye2e 场景 zh UI 与 `TestChatOriginJudge` 一致。
- 页面/命令共享段 ` ORIGIN_REMEMBER_PREFIX_alpha_bravo_charlie_delta `（50 字节，11 个窗口，满足 40～200 字节）。
- `TestOriginRememberTurn` 可用现有夹具搭建：`memoryScriptServer` 的写法、`hostPageTool`（只读 External，内容 `fetchedPage`）、`hostStubTool`、`setProviders`、`OpenCLI(Options{ChatMode: true, ExtraTools: ...})`、`rt.Asker.Bind(&scriptedAsker{...})`、`rt.Runner.Run` 均存在（`memory_trusted_test.go:29-115`）。answers `["a"]` 下去掉跳过分支（R8）时第二问拿到空答复被拒，stub 只跑 1 次，测试确实失败。
- R2–R9 推演：R3（键去工具）使③换工具与⑧失败；R4（Snippet 文本作键）因新片段不含非复制尾部会让①仍过，但②（P 的子串）失败——加②是必要且有效的；R5（上界 `start+cut-39`）使⑥失败；R6（跳过裁决）使⑤失败；R7（挪到 chat==nil 前）使⑥失败；R8/R9（去掉分支）使①②、真实回合与 ptye2e ①失败。均可编译。R2 的前提见 IF2(c)。
- D10 影响：grep 全仓测试，无断言依赖旧 48 字节片段、`…` 结尾、`逐字复制自` 理由全文或旧问句“输入 y 允许/Type y to allow”；现有 `Details[1]` 包含 `curl -sS https://example.invalid/`（`origin_guard_test.go:78`）、`installer --force`、`rm -rf`（`origin_test.go:31,89`）、裁决提示词不含密钥（`origin_judge_test.go`）在新片段定义下仍成立。计划未列具体受影响用例可接受（预期为零）。

## 发现

### IF1 [MEDIUM] 设计反向验证“记入本次全部段”缺失，计划 R1 实为设计的“改回整段记入”

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: 设计“验证要求·反向验证”列 9 项，第 1 项“改为记入本次全部段（P+X 用例失败）”、第 8 项“改回整段记入（相邻 P+X 与长段用例失败）”。计划 T1 的 R1 写“`Allow` 记入整段全部窗口（改回整段记入），⑤⑥失败”，即设计第 8 项；R2–R9 依次对应设计第 2、3、7、9、4、5、6 项与端到端拆分。设计第 1 项（同一调用里两个来源的 P、X 段，答 a 时把 X 也记入）没有任何 R 项；计划完成定义 4 却写“9 项反向验证”、可追溯性表“方案 3、D7 … + R1、R5”。
- 影响: 守住“同调用其他段不记入”（完成定义 2、安全缓解首条）的 guard ⑦ / T1 ④ 用例是否真能抓住该变异未被证明；若 T1 ④ 的 hit 是对只含 P 的参数 `Find` 得到的，该变异根本不经过 ④。用例本身存在（guard ⑦），故不是“完全无验证手段”，不构成阻断。
- 建议: 增加 R10（或把 R 序号对齐设计）：在 `find`/`findAll` 里把同一调用全部段的窗口放进返回 hit 的 `windows`，目标 guard ⑦ 失败；并写明 T1 ④ 的 hit 须取自同时含 P 与 X 的参数，使该变异下 ④ 也失败。

### IF2 [MEDIUM] T1 用例构造前提不全：否定断言可能空过、反向验证可能不失败（同类穷尽）

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据与同类全部出现点:
  - (a) ⑤ 长度：计划统一写“P、X 各为 ≥40 字节”。按计划定义推算：P 长 Lp 且已允许时，所示片段起点为 Lp-39，覆盖窗口起点至 Lp+121，“所示片段之外剩余部分（≥40 字节）”要求 X 至少约 162 字节。X 取 40～161 字节时剩余部分不存在或不足 40 字节。
  - (b) 否定断言空过：③④⑤⑥⑦⑧ 的“非 `Remembered`”若参数里根本没有命中（`Find` 返回 ok=false），`Remembered` 天然为 false，断言恒过；与 (a) 叠加时，R1 下 ⑤ 不会失败。计划未要求同时断言 `ok==true`。
  - (c) ⑦ 的键序：`visitStrings` 按 map 键排序遍历（`origin.go:188-193`）。若放 X 的参数键排在 P 之前，R2（只看首个段）下首段就是 X，⑦ 照样通过，R2 不失败。
  - (d) ⑥ 文本：长于 200 字节的段若用 `strings.Repeat` 等周期文本，后面窗口与已覆盖窗口哈希相同，正确实现下“起点 i+cut-39 的窗口非 Remembered”反而失败；另外计划沿用设计的 `i` 但未定义（应为该段起点 = start）。
- 影响: 执行者按字面构造用例，可能得到恒过的否定断言或与计划不符的反向验证结果，削弱 D3/D7/D10 边界（计划自评“最大风险”）的验证；计划要求反向验证必须失败，多数情况下会在执行中暴露，故不阻断。
- 建议: T1 写明 ⑤ 的 X ≥ 200 字节（或给出“X 长度 ≥ 所示片段覆盖终点后再 40 字节”）；所有“非 Remembered”断言同时 `assert.True(t, ok)`；⑦ 中 P 所在参数键字典序在前（如 `a`/`b`）；⑥ 用非周期文本（如带递增序号），`i` 写成段起点。

### IF3 [LOW] ptye2e 用 `TWO_DONE` 证明执行会匹配到调用行，且“不再出现问句”的检查时点未定

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: T3 ① 写“取新 mark 后 `Expect("TWO_DONE", …)` 且新 mark 后不再出现 `执行 shell_exec？`”。`TWO_DONE` 同时出现在命令文本 `echo ORIGIN_REMEMBER_PREFIX_… TWO_DONE` 里，chat 的调用行（`$ shell_exec …`，见 `internal/chat/tool_summary_test.go` 的格式）在执行前或被拒时也会显示，故它不能证明“输出标记出现在屏幕上”（设计验证要求原文）。若在匹配到调用行后立即检查“无问句”，问句可能尚未渲染，检查会误过。现有做法是用结果行 `⎿ ok:` + 输出（`chat_trusted_memory_test.go:37-38` 的 `ranBody`）。
- 影响: 屏幕层断言偏弱；trace 中“两个 `shell_exec` tool 事件”（被拒为 `denial` 类）仍能证明第二条执行，核心验收有手段，不阻断。
- 建议: 屏幕断言改为结果行正则 `⎿ ok:` + `ORIGIN_REMEMBER_PREFIX_…TWO_DONE`（同 `ranBody` 写法，注意 screenText 去空白）；“新 mark 后无 `执行 shell_exec？`”放在 `exitAfterTurn` 之后检查。

### IF4 [LOW] `origin_remembered` attr 的写入位置有歧义，且无用例覆盖“裁决关闭时”

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: T2 写“`observe.go` 在 `origin_judge` 旁写 `e.Attrs["origin_remembered"]`”。现有 `origin_judge` 系列 attr 都在 `if approval.OriginJudge != "" { … }` 块内（`internal/personal/observe.go:383-393`）。若写进块内，`tools.origin_judge.enabled: false` 的会话里 `added`/`hit` 不会进 trace。计划的 observe 用例在已有 `judged` 工具上追加断言（同时有 OriginJudge），`TestOriginRememberTurn` 不查 trace，ptye2e 裁决固定 ask——三者都抓不到这一错误。
- 影响: 完成定义 3 在裁决关闭时可能不满足且无测试发现。
- 建议: 写明 attr 写在 `OriginJudge` 块之外、独立判断非空；observe 用例加一个只调 `ReportOriginRemembered` 不调 `ReportOriginJudge` 的工具，断言 attr 存在。

### IF5 [LOW] 全部已允许时 `Snippet` 的终点未定义

- disposition: CORE_CORRECTIVE；user_visible_effect: true
- 证据: T1 只定义“起点为段内首个未允许窗口…终点为其后连续未允许窗口中最后一个的末尾”，并说“全部已允许时取段首”，未说此时终点（不存在未允许窗口）。该 `Snippet` 会进入 `ReportDanger` 理由（trace `danger` attr）与裁决输入（`origin_guard.go:119-129`）。
- 影响: 不同实现给出不同的 trace 理由与裁决输入文本（可见但轻微），不影响问不问、拒不拒。
- 建议: 写明全部已允许时片段取段首起至段尾、按同一 200 字节与 UTF-8 规则截断（与从未答 a 时一致）。

### IF6 [LOW] 夹具指引不精确

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: 任务书要求“照 `memoryScriptServer` 写脚本服务器”，范围说明列出 `admit_ledger_test.go`，但 `memoryScriptServer` 在 `internal/wiring/memory_trusted_test.go:29-60`；真实回合还需 `rt.Asker.Bind(&scriptedAsker{...})` 才能让 answers 生效（同文件 `:108-109`），计划未提。guard harness“照 `newJudgeHarness`”，而该 harness 的内层是计数 `ApproverFunc`、chat 另设（`origin_judge_test.go:59-81`）；①③要求“内层为 chat approver”，需显式 `inner = chat`。
- 影响: 低能力执行者可能找错夹具或写出 answers 不生效的回合测试（会以失败暴露）。
- 建议: 补文件路径与 `Bind` 一句；写明 ①③ 的 `originGuard{inner: chat, chat: chat}`，④ 用返回 `tools.Denied` 的 `ApproverFunc` 作内层。

## 覆盖缺口与限制

- 未实际运行任何测试或反向验证，结论来自代码阅读与推演；UTF-8 截断、ConPTY 渲染等细节以实施时运行为准。
- 未核对 Standards/Governance 轴内容（GR 结构条目、仪式成本、授权表述）。
- 未核对 `web_fetch` 对 `text/plain` 页面的具体包装格式，依据是同模式的 `TestChatOriginJudge` 已通过。

## 结论

PASS。无 `CORE_BLOCKING`：核心验收（问句与 a 选项、只带已允许片段时跳过询问并执行、未允许部分与裁决 deny/内层 deny/无 chat 照常、trace attr）每项都有可执行验证；`approval: auto` + 裁决 ask 下“第一条问、第二条执行”的路径与 ptye2e 文案已对照代码核实；D10 不破坏现有断言。IF1–IF6 为 `CORE_CORRECTIVE`，建议在计划批准前一并修入（其中仅 IF5 标 `user_visible_effect: true`，影响轻微）。

## 剩余风险与后续

- IF1、IF2 关系到“记入范围不超出用户所见”这一安全核心的验证强度，建议优先修订并在实施时由 T4 复核抽查 R1/R10/R2。
- 实施时 T4 应确认 ptye2e 在 Windows ConPTY 下结果行匹配（IF3 改法）。
