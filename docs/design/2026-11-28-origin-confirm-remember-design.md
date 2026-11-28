<!-- template_id: design; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 设计

> 状态：Draft 0.3 / 待人工计划批准；issue `suga-ilko`

- 思考模式：RIGOROUS；核心目标：chat 里对外部来源询问答一次“本会话允许该片段”，之后带同一片段的其他调用不再问这一问。
- scope freeze：`originGuard` 询问选项、会话内已允许集合、审批事件 attr、问句文案；其余 deferred。
- 暴露度：Full（放宽外部来源防护，属安全边界，SR1409）；review budget：两轴评审封顶两轮，报告与 ledger 在 `docs/review/2026-11-28-origin-confirm-remember/`（`standards-review.md`、`spec-review.md`、`review-ledger.json`）；停止条件：无开放 `CORE_BLOCKING` 或到达两轮交用户决定。

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-28 | Claude | 初稿 |
| 0.2 | 2026-11-28 | Claude | 第 1 轮两轴评审处理（见下表）：答 a 只记入问句所示那一段；判定逐窗口；写明接口签名与残余风险 |
| 0.3 | 2026-11-28 | Claude | 第 2 轮处理（见第 2 轮表）：问句片段从段内首个未允许窗口起，答 a 只记所示片段覆盖的窗口；用户 2026-11-28“ilko Q1–Q4 按推荐”：Q1–Q3 转为 D6、D8、D9，Q4 定为按此修法改后开第 3 轮只确认这一处 |

第 1 轮处理：

| 发现 | 处理 |
|---|---|
| OS1=OF1 | 答 a 只记入问句所示命中段（hit 自带窗口，不重跑 find）；本次调用本身已获允许（同 y），其他段不进入记住集合（方案 3）；验证补“P+X 答 a 后只带 X 仍询问” |
| OF2 | 名词改为逐窗口判定 |
| OF3、OS4 | 架构加“代码组织”，写明 `OriginHit` 窗口、`findAll`、`Allow`、`ApprovalReport` 字段 |
| OS3 | 安全节补同工具不同页面、裁决 ask/error 加 auto 无人确认、会话内重新抓取三条残余；规范补 SR1301 |
| OS2 | 补评审目录与 ledger |
| OF4 | 验证写明内层审批模式、裁决设置、执行断言、淘汰预期、裁决 ask 跳过用例与反向验证 |

第 2 轮处理：

| 发现 | 处理 |
|---|---|
| OS5、OF5 | 名词加“所示片段”；方案 2/3、D3、D7 改为片段从段内首个未允许窗口起、只记所示片段覆盖的窗口；未答过 a 时与现状一致；验证补两条、反向验证补“改回整段记入” |
| OS6 | 安全节缓解句改为“只记入所示片段覆盖的窗口”，与定义一致 |
| OS7 | ledger 由协调方建立，本文不改 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 背景与目标

模型把外部结果里的一段命令前缀（实例：`sshc run <host> -- 'pct exec <id> -- bash -lc`）拼到多条不同命令前，每条都触发外部来源询问，只能逐条答 y。`suga-7s4v` 已让登记为可信的记忆后端不再触发；本项覆盖其余来源：网页、MCP、未登记可信的记忆后端等。

用户可见结果：外部来源询问多一个选项 `[a]本会话允许该片段`；答 a 后，本会话后续调用若只带已允许的片段，不再弹外部来源询问，但审批规则照常：deny 规则仍拒绝，普通 ask 仍询问，裁决 deny 仍拒绝；run 与 ACP 行为不变；trace 能看出是哪次放行。

## 名词

- **外部来源询问**：`originGuard.Decide` 命中后由 `confirm` 发出的 chat 问题。
- **命中段**：参数某个字符串里相邻偏移都命中、且来源工具相同的一串 40 字节窗口（`OriginWindow`），即一段逐字复制文本。
- **所示片段**：从命中段内首个未允许窗口起按 `originSnippet` 截取的至多 48 字节；“覆盖的窗口”指整个落在这段字节内的该段窗口。从未答过 a 时起点就是段首，与现状 `Snippet` 相同。
- **已允许集合**：(会话, 来源工具) → 窗口哈希。判定逐窗口：命中段的每个窗口都在其来源工具下已允许，该段才算已允许。

## 范围与非目标

范围（受影响项目：仅 suga）：`pkg/agentcore/tools/origin.go`、`internal/wiring/origin_guard.go`、`pkg/agentcore/tools/approval.go` 与 `internal/personal/observe.go`（attr）、`internal/i18n/catalog/{zh,en}/host.ini`（问句）。

非目标：不持久化；不改裁决、初筛窗口与审批规则；不加“本会话总是拒绝该片段”；不提供查看或撤销命令（Q3）；不改 run、ACP、子 agent。

## 已确认事实与规范

- `internal/wiring/origin_guard.go:115-120`：`Find` 命中后取 `hit.Snippet` 组成询问理由；`:156-158`：无 chat 时直接拒绝；`:160-165`：chat 答 y 后以 `originConfirmedKey` 调内层审批器，内层普通 ask 不再重复问。
- `internal/wiring/origin_guard.go:209-215`：只有 `ReplyAllow` 放行，其余一律拒绝；不记忆。
- `internal/wiring/origin_guard.go:123-154`：裁决在询问之前；allow 交内层，deny 拒绝，ask 才问人；`:71-74` 写明裁决不缓存。裁决默认开启（`internal/config/origin_judge.go:27`），可关闭。
- `pkg/agentcore/tools/origin.go:130-158`：`find` 只返回第一处命中；`Snippet` 为参数中命中位置起 48 字节（`:152,165`），含复制段之后的非复制字节。上例前缀为 44 字节，不同命令的 `Snippet` 末尾不同。
- `pkg/agentcore/tools/origin.go:52-54,100`：`index` 把窗口哈希映射到最新持有它的来源；来源只记工具名（`origin_guard.go:49` 传 `call.Name`），不区分页面。
- `internal/wiring/runtime.go:632`：`ExternalOrigin` 每个运行时在内存新建，不随会话持久化；`internal/wiring/subagent.go:199-200`：子 agent 有独立实例且 chat 为 nil。
- `internal/wiring/chat_approver.go:296`：`approval: auto` 下内层不问，`previewAsk` 不并入调用详情，问句只显示一处来源与片段。
- `pkg/chatui/approval.go:21-25`：`a`/`always` 解析为 `ReplyAllowAlways`；`:116-118`：普通审批问句已用“[a]本会话总是允许…”。`internal/wiring/chat_approver.go:230-245`：普通审批答 a 时追加内存会话规则，按工具与目标匹配，表达不了片段，且位于 `originGuard` 内层，不可直接复用。
- `internal/i18n/catalog/zh/host.ini:18`、`en/host.ini:19`：现问句“输入 y 允许，其它任意输入拒绝”。
- `internal/personal/observe.go:379,387`：审批事件已有 `approval_answered`、`origin_judge` 等 attr。
- 规范：SR1301（安全核对）、GR108（装配只装配）、SR1409。

## 总体方案

1. 问句改为 `执行 {tool}？[y]是 [N]否 [a]本会话允许该片段：`（en 同义）；解析沿用 `chatreply.ParseApprovalReply`，`ReplyAllowAlways` 即“允许该片段”，`ReplyDenyAlways` 与其他输入仍为拒绝。
2. `find` 收集参数中全部命中段；`Find` 返回首个未全部已允许的段，`Snippet` 为其所示片段；全部段都已允许时返回首段并标 `Remembered`。
3. 答 a：本次调用本身已获允许（同 y，`originConfirmedKey` 照旧），并只把所示片段覆盖的窗口（`hit` 自带）记入已允许集合；段内超出所示片段的部分与同一调用里的其他段都不记入，之后带这些部分的调用照常询问。不在答 a 后重跑 find。
4. 后续调用在裁决 allow/ask/error 之后、无 chat 判断之后、问人之前检查：`hit.Remembered` 则不问，以不带 `originConfirmedKey` 的 ctx 调内层审批器，规则与普通 ask 照常。
5. trace：审批事件 attr `origin_remembered`：答 a 的那次为 `added`，被跳过询问的为 `hit`；`danger` attr 照旧记录。

## 架构

分层与现状一致：哈希与集合只在 `pkg/agentcore/tools`，wiring 只决定问不问并把 `hit` 交回记入（GR108）；不新增配置键。

代码组织（参照现有 `ReportOriginJudge` 写法）：

- `pkg/agentcore/tools/origin.go`：`OriginHit` 增 `Remembered bool` 与不导出的 `windows []originWindow`（`originWindow{hash uint64; tool string}`，只含所示片段覆盖的窗口）；新增 `func (o *ExternalOrigin) findAll(session string, args map[string]any, ignoreURLs bool) []OriginHit`（每段一个，按出现顺序），`Find`/`FindIgnoringURLs` 签名不变、经它取首个未允许段；新增 `func (o *ExternalOrigin) Allow(session string, hit OriginHit)`，把 `hit.windows` 记入 `originSession` 的已允许集合。
- `pkg/agentcore/tools/approval.go`：`ApprovalReport` 增 `OriginRemembered string`（`added`/`hit`），新增 `ReportOriginRemembered(ctx, value string)`；`internal/personal/observe.go` 写 attr。
- `internal/wiring/origin_guard.go`：`Decide` 加 `Remembered` 分支；`confirm` 对 `ReplyAllowAlways` 调 `g.origin.Allow(session, hit)`。i18n 两份问句。

## 关键流程

```text
write/exec 调用 → Find（全部段，逐窗口判定已允许）
  ├─ 无命中 → 内层审批器
  └─ 有命中 → 裁决（若启用）
        ├─ allow → 内层；deny → 拒绝（已允许也拒绝）
        └─ ask / error / 无裁决
              ├─ 无 chat（run、ACP、子 agent）→ 拒绝（不变）
              ├─ hit.Remembered → 内层（不带 originConfirmedKey），attr origin_remembered=hit
              └─ 否则问人：y → 放行；a → 放行并 Allow(所示片段)，attr=added；其他 → 拒绝
```

## 安全、数据、运维与回滚

- 缓解：只在会话内存且须用户主动答 a；只记入问句所示片段覆盖的窗口，未显示的部分不记入；键含来源工具；同一调用里任何未允许段仍询问；裁决 deny、规则 deny、普通 ask 均不被跳过；trace 记 `added`/`hit`。
- 残余（Gate 须知）：
  1. 来源是工具名：同一个 `web_fetch` 抓到的另一个页面若含同一窗口，不会重问（D8 已定来源工具入键，不扩大到跨工具）。
  2. 裁决返回 ask 或 error（或已关闭裁决）时跳过询问；`tools.approval: auto` 下内层也不问，已允许片段的调用会无人确认直接执行。
  3. 会话内来源被淘汰后重新抓取，旧允许仍生效（同工具时）；这与 D1 否定持久化的理由相同，本设计只在会话内接受它，因为允许来自同一会话里用户刚给的回答。
  4. 已允许前缀后接模型自写的恶意尾部不被外部来源防护拦（尾部不是复制段，现状同样），只剩审批规则与裁决。
- 数据：无持久化、无迁移。回滚：revert 代码即可，无残留；`tools.ask_on_external_origin: false` 行为不变。

## 决策

- **D1 作用域**：只存进程内存、随会话结束消失，不写 session meta。理由：`ExternalOrigin` 本身就在内存（`runtime.go:632`），resume 后防护不会对旧片段触发，持久化的允许无可匹配；重新读取后沿用旧允许等于静默放宽一次新读取，跨重启不接受（会话内同样存在，见残余 3）。
- **D2 “同一片段”**：键用命中段的窗口哈希，不用 `hit.Snippet`（含非复制尾部，不同命令不同），也不用来源全文（会放行该页任意片段）。
- **D3 逐窗口判定**：跳过询问须所有命中段的每个窗口都已允许；只看首个命中会让“已允许前缀 + 新复制段”蒙混；所示片段从首个未允许窗口起，已允许前缀后接的新段会显示新段本身。
- **D4 裁决**：已允许不跳过裁决，也不覆盖裁决 deny；只替代“问人”这一步（与 `origin_guard.go:71-74` 一致）。
- **D5 无询问入口**：run、ACP、子 agent 仍拒绝，不变。
- **D6 选项与解析**：复用 chat 已有的 `a` = “本会话总是允许…”写法与 `ParseApprovalReply`；不复用 `chatApprover` 会话规则（Q1 选 A）。
- **D7 记入范围**：答 a 只记入所示片段覆盖的窗口，不给工具装会话规则；后续调用内层普通 ask 照常询问。代价：复制段长于 48 字节时，未显示部分下次会再问。
- **D8 来源工具入键**：换来源工具重问（Q2 选 A）。
- **D9 不做查看与撤销**：重开 chat 即清空（Q3 选 A）。

## 待确认事项

无。

选项记录（用户 2026-11-28“ilko Q1–Q4 按推荐”）：Q1 选项字母 A 复用 `a` / B 新增 `s`，已决 A（D6）；Q2 来源工具 A 入键 / B 不入键，已决 A（D8）；Q3 查看与撤销 A 不做 / B 加 `/origin`，已决 A（D9）；Q4 第 2 轮 OS5 A 按本版修法改后开第 3 轮只确认这一处，已决 A。

## 验证要求

- 单元（`ExternalOrigin`）：`Allow` 后同会话同来源、同一段配不同尾部判为 `Remembered`；另一会话、另一来源工具、另一段均不判；P+X 中对 P 段 `Allow` 后，只带 X 的调用仍返回未允许命中；P 已允许后调用带同页相邻的 P+X，`Snippet` 从 X 起，`Allow` 后只带 X 中所示片段之外剩余部分的调用仍未允许；长于所示片段的一段只记所示部分；来源被淘汰后其窗口不再命中（`Find` 为 false）；同一工具重新 `Record` 同一文本后仍为 `Remembered`，换工具 `Record` 则不是。
- guard（假 asker、假裁决）：内层为 auto 时，答 a 后第二条不同命令不再 Ask 且放行，attr `hit`；裁决固定 ask 且全部已允许时同样不 Ask；内层为默认 ask 时第二条只出现普通审批问句；内层 deny 规则仍拒绝；裁决 deny 仍拒绝；chat 为 nil 仍拒绝；P+X 答 a 后只带 X 仍 Ask；答 d 或空为拒绝且不记入。
- 端到端：`test/ptye2e` 在 `fakellm` origin 模式上加场景，配 `tools.approval: auto`、fake judge 固定回 `ask`；页面含 40 字节以上前缀，模型依次跑两条带该前缀的不同命令；第一条询问，答 a；第二条屏幕不出现外部来源询问且其输出标记出现在屏幕上，`suga inspect trace --json` 中第二条为成功的工具事件，两次审批事件 `origin_remembered` 分别为 `added`、`hit`，均有 `origin_judge=ask`。
- 反向验证（确认能编译，失败不是 build failed）：改为记入本次全部段（P+X 用例失败）；只看首个命中；键去掉来源工具；已允许时跳过裁决（裁决 deny 用例失败）；跳过分支挪到无 chat 判断之前（chat 为 nil 用例失败）；去掉跳过分支（裁决 ask 用例与端到端失败）；改用 `hit.Snippet` 作键；改回整段记入（相邻 P+X 与长段用例失败）；分别确认对应测试失败。

## 结论与人工计划 Gate

评审状态：第 2 轮 Spec PASS、Standards BLOCKED（OS5），0.3 已按 Q4 修法处理，待第 3 轮只确认这一处；Q1–Q3 已决。批准后才进入实施计划，批准不授权实施。
