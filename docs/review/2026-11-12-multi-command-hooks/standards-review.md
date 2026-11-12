<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 1 轮 Standards/Governance 评审

## 评审目标与范围

- 目标: `docs/design/2026-11-12-multi-command-hooks-design.md`（issue `suga-5xmf`）
- 候选身份: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `b11cd71747bb9d3ff5edadf4c2f7962752e5c66d`；subject path `docs/design/2026-11-12-multi-command-hooks-design.md`；document revision Draft 0.1（HEAD 中该文件与候选一致）
- 修订版本: Draft 0.1
- 评审者: 独立 Standards/Governance 轴评审者（子 Agent，未参与本设计撰写），REPORT_ONLY
- 日期: 2026-11-12
- 轴: Standards/Governance；第 1 轮 discovery（全量读）
- 排除项: Spec/Executability 轴（流程正确性、并发与时序细节、测试可执行性）；用户尚未作出的 Q1–Q7 选择本身；IDEV-STD 仓库的实现

## 输入与方法

- 规范：`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/payload.md`（design-contract、design 模板、SR11xx/SR14xx、SR1301、GR101–GR110、规则包 SR2001–SR2004）全文；`tmp/idev-std/d/OfGu6IpGnHz92hejmPjb9g/payload.md` 的 review-contract、review-candidate-loop、review 模板、SR1409/SR1410.1/SR1433 等章节。
- 仓库：`AGENTS.md`；设计全文（`git show b11cd717:<subject>`）；已批准设计 `docs/design/2026-11-11-protected-config-keys-design.md`（Draft 0.8，批准记录 commit `d416ed9b`）；`gofer issue show suga-5xmf`。
- 事实核对：`internal/hooks/hooks.go`（全文）、`internal/chat/hooks_record.go`、`hooks_gate.go`、`stop_background.go`、`chat.go`/`chat_turn.go` 相关行、`internal/config/settings_registry.go:180-200,265-285,595-740`、`pkg/sugasdk/doc.go:94`、`internal/cli/chat.go:21`、`internal/wiring/localenv.go:193`；`git tag`（存在 `v0.55.0`）；IDEV-STD 设计 `idev-standards-public/docs/design/2026-11-12-continuation-hooks-split-plans-1-3-0-design.md:41,75,92,143`；分层守卫测试位置（仅 `pkg/agentcore/layering_test.go` 等，`internal/*` 间无守卫）。
- 设计引用的代码行号逐一抽查，均与现状一致（`hooks.go:247-255/302-308/316-326/407-422`、`settings_registry.go:192/273-279`、`chat_turn.go:235/244-247/295/302/395`、`hooks_record.go:64-100/88-96/112-128` 等）。

## 验收标准

- design-contract 必需内容（声明、修订记录、背景/目标、名词、范围/非目标/受影响项目、事实与规则 ID、方案与架构、流程、安全/数据/运维/回滚、决策与未决问题、结论与 Gate）。
- SR1103、SR1108、SR1301、SR1403、SR1405（含 SR1405.1）、SR1409、SR1410（含 SR1410.1）、GR107–GR110、SR2004。
- 每个新增机制先答 scope necessity（核心需求：多个命令共存、Stop block 能生效；只配 `hooks.command` 行为不变）。
- 与已批准 protected-config-keys 设计 0.8 的一致性。
- Q1–Q7 是否列全需用户拍板的核心选择，选项与推荐理由是否充分。

### 逐项核对结果

| 条目 | 结果 |
|---|---|
| design-contract 必需内容 | 齐全：声明（行 14-20）、修订记录（行 6-12）、背景与用户可见结果（22-32）、名词（34-40）、范围/非目标/受影响项目（42-61）、事实与规则 ID（63-74）、方案（76-157）、架构/代码组织（159-171）、流程（173-191）、安全数据运维回滚（193-208）、决策与待确认（210-248）、结论与 Gate（250-252） |
| SR1103 | 通过：0.1 行含版本、日期、作者、摘要 |
| SR1108 | 通过：RIGOROUS、核心目标、scope freeze、DEFER_OR_REQUEST、预算 8 轮、停止条件均公开声明；但 scope freeze 本身含超出核心需要的机制，见 HS1 |
| SR1301 | 逐项：鉴权——已核对（protected，依赖 `suga-hyj7`，见 HS5）；注入防护——已核对（argv 直接执行，不经 shell）；秘密——已核对（trace 只记 argv[0] 基名与编号）；入参校验——已核对（启动与 `config set` 同一校验）；资源归属——不适用。无 SECURITY 违反 |
| SR1403 / SR1405 / SR1405.1 / SR1410 | 不通过（纠正级）：多项条目字段与跨条目机制没有当前验收需要、没有两个已验证用例，也未证明更小方案不足，见 HS1 |
| SR1409 | 通过：判高暴露度并引用可核实事实（tag `v0.55.0` 存在；Stop 等待链路与执行前拒绝门），声明两轴独立评审与 ledger 目录 |
| GR107 | 不适用（不新增 CLI 入口） |
| GR108 | 引用不准确（D1 用于包职责而非装配层）；装配层 `localenv.go` 只做展示，未见违规，见 HS4 |
| GR109 | 列入规范但无落点说明（Interrupt“忽略”与现有 Warn 日志的关系未写），见 HS4 |
| GR110 | 通过：验证要求含 `suga config set/get hooks.commands` 经真实命令分发 |
| SR2004 | 通过：不新增 CLI，代码组织沿用本仓库分层并给出参照；但校验函数的包归属未定，见 HS3 |
| 与 protected-config-keys 0.8 一致性 | 一处未披露的决定反转，见 HS2；超时类键在条目内随整块受保护已披露（行 151），可接受 |
| Q1–Q7 完整性 | 缺“最小充分形状”选项，见 HS1；其余各问选项与理由基本充分 |

## 发现

### HS1 [HIGH] 推荐形状引入多项无法追溯到核心需求的机制，Q1 缺最小充分选项

- disposition: `CORE_CORRECTIVE`（不属五类：不采纳也不会让结果错误、不安全或不可执行，只是范围超出最小充分）
- 证据:
  - 核心需求（设计行 16、30；issue `suga-5xmf`）：新增命令与 gofer 的 `hooks.command` 共存、按序收事件、Stop block 生效；IDEV-STD 续跑命令只处理 Stop，非 Stop 事件自行过滤后无输出退出 0（IDEV-STD 设计行 75），唯一已证实的额外需要是独立超时 10 秒（同文行 92）。
  - 推荐 A 为每条新条目引入、但当前任何验收都不需要的机制（同类穷尽）：
    1. `events` 订阅字段及“列在 events 中但无 glob 时启动报错”规则（行 101、108）；续跑命令已自行过滤事件。
    2. 条目级 `stop_args`、`stop_wait_sec`（行 103-104）及由此产生的“至多一个等待型条目”校验（行 135、195）、在新条目中挑选等待型条目的分支；已证实的等待来源只有兼容条目（gofer）。
    3. 条目级 `pre_tool_use.tools/.name`（行 105）及跨条目 PreToolUse 改写链（行 127、185-190、D3）、`PreToolName` 语义改为“任一条目订阅”（行 166）；续跑命令不需要 PreToolUse。
    4. 条目级 `post_tool_use.tools/.name`（行 106）及 `PostToolName` 语义变化（行 166）；同上。
    5. `inspect env` 展示条目数与程序名（行 48、170）；非验收所需。
  - Q1 的 C（行 223）被否决的理由是“不能单独设超时”“无法订阅部分事件”；前者可用只加 `timeout_ms` 的形状解决，后者不是已证实需要。Q1 没有给出“`command` + `timeout_ms`、新条目只做即时条目、不收 PreToolUse/PostToolUse”这一最小充分选项，用户无法选它。
  - 规则：SR1403（不做→复用→最小实现）、SR1405/SR1405.1（无两个已验证用例不抽象）、SR1410/SR1410.1（重机制须列出局部修复为何不足）、planning-core-rigorous“scope freeze 后新增机制须直接追溯到核心验收”。
- 影响: 计划与实施面扩大（条目解析、校验、两类过滤器、改写链、等待型选择、展示），评审与测试负担随之扩大；且第 3 项引出 HS2 的规范冲突。若改为最小形状，Q3 的 B/C（多个等待来源）也随之失去前提。
- 建议: 在 Q1 增加最小充分选项（如 A0：条目只有 `command` 与 `timeout_ms`；新条目收全部普通事件、不收 PreToolUse/PostToolUse、不能等待；等待只来自兼容条目），并把推荐理由改为按 SR1403/SR1410.1 比较；若仍推荐 A，逐项写明每个字段对应的当前验收与“局部方案为何不足”，否则把第 1–5 项列为 deferred。

### HS2 [HIGH] 推荐方案使 PreToolUse 过滤器可经 `config set` 写入，反转已批准设计的决定且未披露

- disposition: `CORE_CORRECTIVE`（Gate 前须修正或显式列为决策；不判 AUTHORITY：本设计本身仍须经用户 Gate，属于披露缺口而非越权）
- 证据:
  - 已批准 protected-config-keys 0.8：`hooks.pre_tool_use.tools/name` 为“新增不可写描述符，只参与比较”（该文行 120），第 5 轮 F33 决定“只标 protected、不可写，不新增 CLI 能力”（行 48、16）。
  - 本设计 `hooks.commands` 条目含 `pre_tool_use.tools/.name`（行 105），描述符 `Writable:true`（行 150），即执行前拒绝门的过滤器经 `suga config set hooks.commands` 可写。
  - 本设计对 0.8 的摘要（行 73）未提及 `hooks.pre_tool_use.*` 不可写；Q6 的 B 引用了该处理（行 244），但 A 未说明它会反转 F33。
  - 同类穷尽（与 0.8 的其他差异）：条目内 `timeout_ms`、`stop_wait_sec` 随整块成为受保护比较对象，而 0.8 将 `hooks.timeout_ms`、`hooks.stop_wait_sec` 列为“不保护”（0.8 行 131）——本设计行 151 已披露并给理由，可接受；`hooks.stop_background` 保持全局、不入条目，无冲突。未发现其他差异。
- 影响: 用户按推荐批准时可能未意识到推翻了刚批准的 F33；`suga-hyj7` 计划（当前评审中）的分类表与 CLI 能力边界会和本设计不一致。
- 建议: 二选一并写进决策或 Q6：(a) 采纳 HS1 最小形状，新条目不带 `pre_tool_use`，冲突消失；(b) 保留时在 Q6 明确写“选 A 即把 PreToolUse 过滤器变为人在终端可写，替代 protected-config-keys 0.8 F33”，并加替代声明（参照 0.8 行 221 的写法）。

### HS3 [MEDIUM] 代码组织中校验函数的包归属与依赖方向未定

- disposition: `CORE_CORRECTIVE`（review-contract：结构问题计划批准前必须修正）
- 证据: 行 169“校验函数放 `internal/hooks`，经注册表的 reader 分发调用……计划阶段核对 import 方向，必要时把纯解析放到无依赖的小包”。现状：reader 分发在 `internal/config/settings_registry.go:700-740`（`dispatchConfigReader` 只调用 `internal/config` 内函数）；`internal/config` 目前只依赖 `internal/i18n`；`internal/*` 之间没有分层守卫测试（仅 `pkg/agentcore/layering_test.go`）。按行 169，`internal/config` 将依赖执行包 `internal/hooks`（无循环，但方向新增），或另建未命名的小包。
- 影响: 包归属与新依赖方向留到计划阶段决定，设计的“代码组织”不唯一；SR2004 要求偏离在设计中写理由。
- 建议: 在设计中定下一种：如条目解析与校验（纯函数，只依赖 gkconfig）放 `internal/config`（如 `hooks_entries.go`），`internal/hooks` 消费解析结果；或明确接受 `internal/config → internal/hooks` 并说明理由。

### HS4 [LOW] 规则引用不准确或无落点（GR108、GR109）

- disposition: `CORE_CORRECTIVE`
- 证据: D1（行 212）以“GR108 精神”说明合并逻辑留在 `internal/hooks`，而 GR108 约束的是装配层不内联业务逻辑，本处是领域包与消费方的分工；“规范”列出 GR109（行 74），但全文无落点——表中 Interrupt 单条失败“忽略”（行 129）未说明是否沿用现有 `Call` 的 Warn 日志（`hooks.go:302-308`），Interrupt 会改变 gofer 侧等待状态（发消息类调用）。
- 影响: 规则引用与实际约束错位，计划阶段可能据此误判。
- 建议: D1 去掉 GR108 引用或改为说明装配层（`localenv.go`、`cli/chat.go`）只构造不合并；Interrupt 失败写为“记 Warn（沿用 `Call`），不影响取消”，以落实 GR109。

### HS5 [LOW] SR1301“鉴权”结论依赖落地顺序，未锁定

- disposition: `CORE_CORRECTIVE`
- 证据: 行 152 允许本特性先于 `suga-hyj7` 落地并以 `Writable:true`、未 protected 发布；行 195 的鉴权结论“新键为 protected”只在后落地时成立。现状 `hooks.command` 同样可写且未保护（`settings_registry.go:273`），所以不是相对现状的新风险类，但在先落地的版本里多了一个模型可写的执行配置入口。
- 影响: SR1301 逐项结论有条件成立，计划实现路径有两支。
- 建议: 锁定为“随 `suga-hyj7` 同版或其后发布”，或在 SR1301 项中写明先落地版本的暴露与接受理由。

## 覆盖缺口与限制

- 未评 Spec/Executability：两阶段 Stop 在前台打断、后台任务被杀、Q4 A 下阶段一中途打断的时序与 `recordingWait` 交接细节，由另一轴负责。
- 未运行任何测试或工具；IDEV-STD 侧需求只按其设计文本核对，未核对其实现。
- 未核对 `docs/usage/gofer-personal-agent.md:390-402` 引文全文（行号存在性未逐行验证）。
- `suga-hyj7` 计划正在评审（commit `97e26d11` 为其第 1 轮 ledger，BLOCKED），HS2/HS5 的最终一致性取决于该计划修订后的分类表。

## 结论

PASS（Standards/Governance 轴第 1 轮）。无开放 `CORE_BLOCKING`；HS1–HS5 均为 `CORE_CORRECTIVE`，其中 HS1、HS2、HS3 应在用户就 Q1–Q7 拍板与计划批准前修正（review-contract：结构问题计划批准前必须修正）。

## 剩余风险与后续

- 作者修订时按类自查：①所有条目字段与跨条目机制的 necessity（HS1）；②与 protected-config-keys 0.8 的每处差异是否已披露（HS2）；③代码组织中每个新文件的包归属（HS3）。
- 若采纳 HS1 最小形状，Q3 的 B/C、PreToolUse 改写链与“至多一个等待型条目”校验可整体删除，Spec 轴相应范围缩小。
- 第 2 轮 confirmation 读 `git diff b11cd717..<新候选> -- <subject>` 及依赖章节。
