<!-- template_id: review; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 设计 Spec/Executability 轴评审（第 1 轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`，candidate commit `146ac624`，subject `docs/design/2026-11-28-origin-confirm-remember-design.md`
- 修订版本: Draft 0.1
- 评审者: 独立评审子 agent（Claude Opus 5.5），未参与本设计起草
- 日期: 2026-11-28
- 排除项: Q1–Q3 选哪一项（只判断各选项能否实施）；Standards 轴内容（另见 `r1-standards.md`）；轻治理要求下只报影响核心验收或安全的问题

## 输入与方法

- 全量读取 review 合同、design 合同、`AGENTS.md`、设计全文。
- 逐条对照代码：`pkg/agentcore/tools/origin.go`（`Record`、`find`、`originWindowHashes`、`originSnippet`、`visitStrings`）、`internal/wiring/origin_guard.go`（`Decide`、`confirm`）、`internal/wiring/chat_approver.go:108-250,280-306`、`pkg/chatui/approval.go`、`internal/chat/approval_reply.go`、`pkg/agentcore/tools/approval.go:190-316`、`internal/personal/observe.go:360-400`、`internal/wiring/subagent.go:196-200`、`internal/wiring/runtime.go:625-640`、`internal/config/origin_judge.go`、`test/ptye2e/chat_origin_judge_test.go:44`、`test/ptye2e/fakellm_test.go:113-120,523-526`。
- 用设计里的实例（44 字节前缀 + 不同尾部）手推窗口与命中。

## 验收标准

设计第 22 行的用户可见结果与第 95–100 行验证要求；review 合同对 `CORE_BLOCKING` 五类的定义（含“实现不唯一且导致不同用户可见结果”）。

### 要点逐项核对

| 要点 | 结果 |
|---|---|
| 40 字节窗口哈希作键 | 可行。`originWindowHashes` 按字节偏移给滚动哈希，与位置无关；44 字节前缀有 5 个完全落在前缀内的窗口，内容不变则哈希不变，与尾部无关；若页面上前缀后面紧跟的字节（如 ` '`）在两条命令里也相同，跨边界的窗口也会稳定命中，两条命令仍判为同一组。D2 不用 `hit.Snippet` 的理由成立（`origin.go:152,165`）。键定义的写法见 OF2 |
| `find` 改为收集全部命中 | 可行：去掉 `origin.go:153` 的 `return false`，`visitStrings` 走完；每个窗口的来源工具取 `s.names[s.index[h]]`（index 指向最新来源）。返回值和记入接口没写，见 OF3 |
| 与裁决的交互 | 可行且无歧义：裁决仍在询问前（`origin_guard.go:123-154`），allow 交内层，deny 拒绝，ask/error 进入“已允许则跳过”分支；跳过时 `ReportDanger` 与 `ReportOriginJudge` 照旧记录。裁决默认开启（`internal/config/origin_judge.go:27`），已允许片段每次调用仍要花一次裁决调用，与 D4 一致 |
| 回复选项解析 | 可行：`a`/`always` 已解析为 `ReplyAllowAlways`（`pkg/chatui/approval.go:25-26`），`confirm` 现在把它当拒绝（`origin_guard.go:210`），只改这一处分支即可；`d`、空、未知仍拒绝 |
| trace attr | 可行：仿照 `OriginJudge`，在 `ApprovalReport` 加字段 + `ReportOriginRemembered`，`observe.go:387` 附近写 `origin_remembered`；`pkg/agentcore` 不依赖 internal |
| “全部命中已允许 → 跳过”无歧义 | 按总体方案 2 / D3 的逐窗口判定无歧义（每个非零命中窗口在 (会话, 该窗口当前来源工具) 下已允许）；名词节的“窗口哈希集合”写法与之冲突，见 OF2 |
| 验证能否证明行为 | 部分不能：审批模式与裁决状态没写，有一条单元断言预期不清，见 OF4 |

### Q1–Q3 能否实施（不判对错）

- Q1-A：能，无解析改动。Q1-B：能，但不能把 `s` 加进共享的 `ParseApprovalReply`（普通审批也用它），要在 guard 里本地解析或加新的回复常量。
- Q2-A：能，即上表做法。Q2-B：能，键去掉工具名，显示仍用 `names`。
- Q3-A：能。Q3-B：能，但要新增 chat 命令并让命令层拿到 runtime 的 `ExternalOrigin`，属新增 Interface。

## 发现

### OF1 [HIGH] 答 a 记入“本次全部命中窗口”，超出问句展示的那段

- disposition: `CORE_BLOCKING` / `[SECURITY]`
- 证据: 设计第 53 行“把本次全部命中窗口……记入”、第 71 行；问句只带首个命中的来源与 48 字节片段（`origin_guard.go:119,160,193-194`；`origin.go:152,165`）；`tools.approval: auto` 下 `previewAsk` 返回 false（`chat_approver.go:296`），问句没有调用全文；第 51 行选项文案为“允许该片段”。
- 影响: 一次调用里若有多段复制文本（可能来自不同来源工具），只有第一段展示给用户，答 a 却把所有段都记成已允许。
- necessity proof:
  - core_acceptance: 第 22 行“只带这段已允许的片段，不再弹询问”；第 76 行“同一调用里出现任何未允许的复制段仍询问”；D3。
  - necessary_causal_chain: 页面 A 含前缀 P，MCP 结果 B 含命令段 X → 模型第一条命令是 P + X → `find` 首个未允许命中为 P，问句显示“来源 web_fetch，片段 P…” → 用户答 a → 按第 53 行，P（web_fetch）和 X（MCP 工具）窗口全部记入 → 之后模型调用只带 X 的命令，在裁决 ask/error/关闭且 `approval: auto` 时直接执行，用户从未看到 X 也没同意过 X。照文档实施，D3 防的“已允许前缀 + 新复制段”在第一次答 a 时就放过了。
  - minimal_owner: 设计总体方案 3 与关键流程；实现在 `origin.go` 的记入接口。
  - deletion_alternative: PASS：把“全部命中”收窄为“问句所示那一段连续命中窗口”即可关闭，不新增机制；或者问句列出全部未允许段再记入。
  - scope_effect: 无新增 Module/Interface/Schema/state/lifecycle/authority/external action（OF3 的记入接口本来就要新增）。
- 建议: 在第 53 行写明记入的是哪段，并定义“段”：首个未允许命中起的连续命中窗口（相邻偏移都命中且来源工具相同）。另选方案：问句逐段列出。单元加“P + X 答 a 后单独带 X 仍询问”，反向验证加“改回记入全部”。

### OF2 [MEDIUM] 名词节把键写成“窗口哈希集合”，与逐窗口判定冲突

- disposition: `CORE_CORRECTIVE`，`user_visible_effect: true`
- 证据: 第 28 行“键为 (会话, 来源工具, 窗口哈希集合)”；第 52 行“返回首个未被允许的命中”、D3“所有命中窗口都已允许”是逐窗口成员判定。
- 影响: 照名词节做集合相等匹配时，若页面给的是完整示例命令，第一条命令复制的窗口比第二条多，第二条的命中集合是第一条的真子集，集合不等，于是再问一次。核心场景失败，用户看到的行为不同。正文已经足够明确，所以不列为阻断。
- 建议: 第 28 行改为“已允许集合：(会话, 来源工具) → 窗口哈希；判定逐窗口”。

### OF3 [MEDIUM] `find` 返回值与记入接口没写

- disposition: `CORE_CORRECTIVE`
- 证据: 现 `OriginHit{Tool, Snippet}`（`origin.go:108-113`）；第 52 行只说“带 `Remembered` 标记的命中”；第 53 行要记入窗口，第 59 行又要求 wiring 不碰哈希。
- 影响: 实施时要自己发明接口。若在 guard 里答 a 后再调一次 find 来取窗口，可能拿到问句展示后才 `Record` 进来的窗口（同一会话多个工具结果先后到达），记入的内容与展示的不一致，会加重 OF1。
- 建议: 写明签名，例如 `OriginHit` 加 `Remembered bool` 和不导出的 `windows []originWindow{hash, tool}`（只含 OF1 定义的那一段），新增 `func (o *ExternalOrigin) Allow(session string, hit OriginHit)`；`Find` 与 `FindIgnoringURLs` 都走同一 `find`，行为一致。

### OF4 [MEDIUM] 验证要求没写审批模式和裁决状态，有一条预期不清

- disposition: `CORE_CORRECTIVE`
- 证据（同类全部）:
  1. guard 测试（第 98 行）要求“答 a 后第二条不同命令不再 Ask”，同一行又要求“内层普通 ask 仍 Ask”。默认 `tools.approval` 为 ask（`chat_approver.go:108-121`），内层对 shell_exec 会问，“不再 Ask”只有在 auto 或 allow 规则下才成立；没写各用例用哪种内层配置。
  2. 端到端（第 99 行）没写 `approval: auto`（现有 origin 场景都这样配，`chat_origin_judge_test.go:44`），也没写裁决怎么处理。裁决默认开启，fakellm 会回答裁决请求（`fakellm_test.go:113-116`）：若回 allow，第一条就不会问，场景不成立；要么设成 ask，要么关闭裁决。另外只断言“屏幕不出现外部来源询问”，第二条也可能是被拒才没出现询问，还需要断言第二条命令确实执行了（结果出现在屏幕上或 trace 里的成功事件）。
  3. 单元“被淘汰来源的窗口不影响已允许判定的正确性”（第 97 行）没有可观察的预期：淘汰后窗口不再命中（不问），还是重新 `Record` 后沿用旧允许（不问）或重问？要写明结果。
  4. 没有覆盖“裁决 ask + 全部已允许 → 跳过询问”，这是 D4 新增的分支；反向验证也没有“跳过分支提到无 chat 判断之前”这一项（关键流程第 69 行的先后关系）。
- 影响: 照写的测试可能在错误实现下也通过（第 2 条），或者两条测试互相矛盾（第 1 条），核心结果“第二条不再问、照常执行”没被证明。
- 建议: 逐条写明内层配置（auto / ask / deny 规则）与裁决设置（关闭，或 fakellm 回 ask）；端到端加执行结果断言；写明淘汰场景的预期；补上第 4 条的用例与反向验证；加入 OF1 的测试。

## 覆盖缺口与限制

- 两轴由同一评审子 agent 按调用方要求分别写出，轴间 reviewer isolation 不满足 Full subject 的独立性要求；写入 ledger 时需披露。
- 只读核对，未运行测试；窗口命中是对实例手推的，没有跑代码。
- 没有核对 chat 进程内切换会话（如在同一进程里恢复旧会话）时集合是否保留；如有该能力，D1 的“随会话结束消失”需补一句，影响较小，未列为发现。

## 结论

BLOCKED：OF1 为开放 `CORE_BLOCKING [SECURITY]`。OF2（user_visible_effect: true）、OF3、OF4 为 `CORE_CORRECTIVE`。

## 剩余风险与后续

- 修订 OF1 后，第 2 轮复核总体方案 2–3、名词、关键流程、验证要求。
- 实施时在 `origin.go` 记入路径上注意并发：`find` 与 `Allow` 分两次加锁，中间可能有 `Record`；按 OF3 让 `hit` 自带窗口即可避免。
