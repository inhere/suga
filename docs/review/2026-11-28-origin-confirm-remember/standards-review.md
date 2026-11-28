<!-- template_id: review; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 设计 Standards/Governance 轴评审（第 1 轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`，candidate commit `146ac624`，subject `docs/design/2026-11-28-origin-confirm-remember-design.md`
- 修订版本: Draft 0.1
- 评审者: 独立评审子 agent（Claude Opus 5.5），未参与本设计起草
- 日期: 2026-11-28
- 排除项: Q1–Q3 选哪一项（交用户）；Spec/Executability 轴内容（另见 `r1-spec.md`）；实施代码；轻治理要求下只报影响核心验收或安全的问题

## 输入与方法

- 全量读取：review 合同 payload（`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`）、design 合同 payload（`tmp/idev-std/d/LysoVXM1D86b1PtHtpG8jg/payload.md`）、`AGENTS.md`、设计全文（104 行）。
- 核对代码：`internal/wiring/origin_guard.go`、`pkg/agentcore/tools/origin.go`、`internal/wiring/chat_approver.go`（`Decide`、`previewAsk`）、`pkg/chatui/approval.go`、`internal/chat/approval_reply.go`、`internal/wiring/subagent.go:196-200`、`internal/wiring/runtime.go:625-640`、`pkg/agentcore/tools/approval.go:190-316`、`internal/personal/observe.go:360-400`、`internal/config/origin_judge.go`。
- 对照：已合并设计 `docs/design/2026-11-26-trusted-memory-backend-design.md`（同属 Full、同类放宽，含第 1 轮 TS1 处理）。

## 验收标准

- design-contract 必需内容（思考模式/核心目标/scope freeze/budget/停止条件、修订记录、背景、名词、范围与受影响项目、事实与规则 ID、方案与架构、流程、安全数据运维回滚、决策与未决、结论与 Gate）。
- SR1409 暴露度定档与 Full 档要求（两轴独立评审、ledger、固定报告路径）。
- SR1301 安全核对清单（放宽类改动逐项）。
- 分层：`pkg/agentcore` 不依赖 `internal/*`（`AGENTS.md`、`pkg/agentcore/layering_test.go`）；GR108；“代码组织”（review 合同：结构违规记 `CORE_CORRECTIVE`，计划批准前须修正）。
- 每个新增机制先答 scope necessity（review-candidate-loop）。

### design-contract 必需内容核对

| 项 | 结果 |
|---|---|
| 公开声明（RIGOROUS、核心目标、scope freeze、budget、停止条件） | 已核对，第 6–8 行 |
| 修订记录 | 已核对 |
| 背景与用户可见目标 | 已核对，第 18–22 行 |
| 名词 | 已核对；“已允许片段”的键定义与正文不一致，见 Spec 轴 |
| 范围、非目标、受影响项目 | 已核对，仅 suga |
| 已确认事实与规则 ID | 已核对；抽查行号与代码一致（`origin_guard.go:115-120/156-158/160-165/209-215/71-74`，`origin.go:130-158/165`，`subagent.go:199-200`，`runtime.go:632`，`approval.go:21-25/116-118`，`host.ini`）。缺 SR1301 引用，见 OS3 |
| 总体方案与架构 | 已核对；无“代码组织”与跨层接口，见 OS4 |
| 关键流程 | 已核对，含图 |
| 安全、数据、运维与回滚 | 已核对；残余风险不全，见 OS3 |
| 决策与未决 | 已核对，D1–D7、Q1–Q3 |
| 结论与人工计划 Gate | 已核对 |

### SR1301 逐项

| 项 | 结果 |
|---|---|
| 鉴权（认证与授权） | 已核对：违反，见 OS1（记住范围超出用户所见与所同意） |
| 注入防护 | 已核对：本项放宽的就是提示注入防护；SQL/命令参数化不适用（不新增拼接）；放宽后的残余见 OS3 |
| 秘密不进日志/输出/仓库 | 已核对：新 attr 值为常量 `added`/`hit`；问句片段沿用 `redact.Secrets` + `displaySafe`（`origin_guard.go:119`）；不持久化 |
| 入参校验 | 已核对：回复经 `ParseApprovalReply`，空/未知/`d` 均为拒绝（fail closed），且设计写明 `d` 不记入 |
| 资源归属校验 | 已核对：键含会话；`ExternalOrigin` 按 runtime 新建（`runtime.go:632`），子 agent 独立实例且 chat 为 nil（`subagent.go:199-200`），不会跨会话或跨子 agent 复用 |

### 新增机制 scope necessity

| 机制 | 必要性 |
|---|---|
| `[a]` 选项与会话内已允许集合 | 是，即核心目标 |
| `find` 收集全部命中 | 是，D3 防“已允许前缀 + 新复制段” |
| 键含来源工具 | 是（安全缓解），取舍交 Q2 |
| `OriginRemembered` / `origin_remembered` attr | 是，用户可见目标写明“trace 能看出是哪次放行”，且 AGENTS.md 要求 chat 行为用 trace 验证 |
| 无其他不可追溯的机制 | 已核对 |

### 分层

已核对：集合与匹配在 `pkg/agentcore/tools`，attr 字段在 `pkg/agentcore/tools/approval.go`，读取在 `internal/personal/observe.go`，问句在 `internal/i18n`，选择逻辑在 `internal/wiring/origin_guard.go`；无 `pkg/agentcore → internal` 新依赖。回复解析经 `internal/chat` 别名到 `pkg/chatui`（`internal/chat/approval_reply.go:19`），方向正确。

## 发现

### OS1 [HIGH] 答 a 记入的范围超出问句展示且用户同意的“该片段”

- disposition: `CORE_BLOCKING` / `[SECURITY]`（SR1301 授权）
- 证据: 设计总体方案 3“把本次全部命中窗口……记入已允许集合”、关键流程“a → 放行并记入”；问句只展示一处来源与一段 48 字节片段（`origin_guard.go:119,193-194`，`origin.go:152,165`）；`tools.approval: auto` 下 `previewAsk` 返回 false（`chat_approver.go:296`），问句不带调用全文；选项文案为单数“允许该片段”。
- 影响: 用户只看见首个命中片段就把同一调用里其他未展示的复制段（可能来自另一来源工具）一并授权。D3 防的“已允许前缀 + 新复制段”在第一次答 a 时就失效，因为前缀和新复制段一起进了集合。
- necessity proof:
  - core_acceptance: 用户可见目标“答 a 后……只带这段已允许的片段，不再弹询问”与安全缓解“同一调用里出现任何未允许的复制段仍询问”。
  - necessary_causal_chain: 页面含前缀 P 和恶意段 X → 模型第一条命令为 P + X（都是逐字复制）→ auto 模式下问句只显示 P 起首 48 字节 → 用户按“允许该片段”答 a → P、X 的窗口全部记入 → 后续含 X 的调用（判断模型无裁决或裁决为 ask/error 时）不再询问，auto 下直接执行，用户从未看到 X 也没同意过 X。
  - minimal_owner: 本设计总体方案 3 与关键流程（实现落在 `pkg/agentcore/tools/origin.go` 的记入接口）。
  - deletion_alternative: PASS：删掉“记入全部命中”，改为只记入问句展示的那一段连续复制段（或问句列出全部未允许段后再记入）即可关闭，不新增机制。
  - scope_effect: 无新增 Module/Interface/Schema/state/lifecycle/authority/external action。
- 建议: 二选一写进设计：①只记入问句所示那段连续命中窗口（首个未允许命中所在的连续段），其余未允许段下次照常询问；②问句逐段列出全部未允许复制段（各自来源与片段），答 a 记入所列各段。补对应单元测试与反向验证。

### OS2 [MEDIUM] Full 档缺评审目录、ledger 与固定报告路径

- disposition: `CORE_CORRECTIVE`
- 证据: 设计第 8 行定为 Full（安全边界，SR1409）且“两轴评审封顶两轮”，但没写 `docs/review/<subject>/` 下的 `review-ledger.json`、`standards-review.md`、`spec-review.md`；review-candidate-loop 要求 Full subject 用固定路径与 ledger。同类先例：`2026-11-26-trusted-memory-backend-design.md` 第 8 行已写明，第 1 轮 TS1 即为此项。
- 影响: 本轮报告在 `tmp/review/` 只能作为原始输出；没有 ledger 时下游 PASS 状态不可核对。
- 建议: 第 8 行补评审目录（如 `docs/review/2026-11-28-origin-confirm-remember/`）与三个文件名，照 7s4v 写法。

### OS3 [MEDIUM] 安全节残余风险披露不全，且未引用 SR1301

- disposition: `CORE_CORRECTIVE`
- 证据: 设计第 76 行。同类遗漏全部如下：
  1. “键含来源工具，换来源重新询问”中的“来源”是工具名（`origin.go:49-50,100`，`Record` 只收 `call.Name`）：同一 `web_fetch` 抓的另一个攻击者页面若含同一 40 字节窗口，不会重问。正文用“如另一网页工具”，但风险句“攻击者控制的页面在后续结果中重复同一段文本”正是这种情况，没被缓解，也没写成残余。
  2. 残余句“只剩审批规则与裁决”：裁决只在 deny 时拦，裁决 ask/error 时按流程直接跳过询问（关键流程第 68–70 行）；裁决默认开启（`internal/config/origin_judge.go:27`）但可关闭；`tools.approval: auto` 下内层不问，已允许片段的调用不经任何人工确认就执行。这三点都没写。
  3. D1 用“重新读取同一来源后若沿用旧允许，等于静默放宽一次新的读取”否定持久化，但会话内同样如此：来源被淘汰或重新抓取后，已允许窗口仍然生效。
  4. 第 47 行规范列表未引用 SR1301，安全节没有逐项核对。
- 影响: 用户在 Gate 上看到的安全取舍偏乐观（auto 模式与同工具不同页面两种最常见的情况都没提）。不改变实现，但影响用户对 Q2 的判断。
- 建议: 在安全节残余风险里补上 1–3，第 47 行加 SR1301。

### OS4 [LOW] 架构节缺“代码组织”与 tools↔wiring 接口

- disposition: `CORE_CORRECTIVE`（review 合同：结构项计划批准前须修正）
- 证据: 设计第 59 行只写“片段匹配与集合放在 `pkg/agentcore/tools`……wiring 不碰哈希”；模板要求涉及代码时写“代码组织”。现在 `OriginHit` 只有 `Tool`、`Snippet`（`origin.go:108-113`），wiring 要在答 a 后“记入本次全部命中窗口”又不碰哈希，需要一个新的导出接口（如 `OriginHit` 带不导出的窗口集合 + `ExternalOrigin.Allow(session, hit)`），设计没写名字和签名。
- 影响: 计划阶段需要自己发明跨层接口；有可能在 wiring 里重新跑一次 find，结果和问句展示的不一致（与 OS1 相关）。
- 建议: 架构节加“代码组织”小段：列出 `tools` 新增导出项（字段/方法签名）、`originGuard` 调用点，以及 i18n、approval、observe 各自的改动，参照现有 `ReportOriginJudge` 写法。

## 覆盖缺口与限制

- 两轴由同一评审子 agent 按调用方要求分别写出，轴间 reviewer isolation 不满足 review-candidate-loop 对 Full subject 的“两轴 reviewer 相互独立”要求；若写入 ledger，需披露或另派独立 reviewer。
- 只读核对代码，未运行测试；未检查 `test/ptye2e` 现有 origin 场景的细节。
- 按轻治理要求，未报不影响核心验收或安全的措辞、格式问题。

## 结论

BLOCKED：OS1 为开放 `CORE_BLOCKING [SECURITY]`。OS2–OS4 为 `CORE_CORRECTIVE`，不单独阻断，计划批准前修正。

## 剩余风险与后续

- 修订 OS1 后需对变更章节（总体方案 3、关键流程、安全节、验证要求）做第 2 轮复核。
- Q1–Q3 的选项都可实施；Q2-B 会让 OS3 第 1 条的残余扩大到跨工具，用户选择时应看到这一点。
