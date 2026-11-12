<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 2 轮 Standards/Governance 评审

## 评审目标与范围

- 目标: `docs/design/2026-11-12-multi-command-hooks-design.md`（issue `suga-5xmf`）
- 候选身份: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `096c9e93fa8364aa448e957679e3c6526279cd20`；subject path `docs/design/2026-11-12-multi-command-hooks-design.md`；document revision Draft 0.3（HEAD `341237b3` 未改该文件）
- 修订版本: Draft 0.3（上轮候选 `b11cd71747bb9d3ff5edadf4c2f7962752e5c66d` / 0.1；0.2 `1dae6a7a` 为未评审中间版，本轮按 0.1→0.3 diff 覆盖）
- 评审者: 独立 Standards/Governance 轴评审者（子 Agent，未参与撰写），REPORT_ONLY
- 日期: 2026-11-12
- 轴: Standards/Governance；第 2 轮 confirmation
- 排除项: Spec/Executability 轴；用户 2026-11-12 的选择（“hooks Q1 选 M，Q2–Q7 按推荐”）本身；`suga-hyj7` 计划自身的正确性

## 输入与方法

- 规范：`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/payload.md`（design-contract、design 模板、SR1103/SR1108/SR1301/SR1403/SR1405/SR1405.1/SR1406/SR1409/SR1410、GR107–GR110、SR2004）；`tmp/idev-std/d/8Yz1eltnU9GuyZy8zAtfgA/payload.md` 的 review-contract 与 review 模板。
- 仓库：`AGENTS.md`；上轮本轴报告 `docs/review/2026-11-12-multi-command-hooks/standards-review.md`（HS1–HS5）；设计 0.3 全文（`git show 096c9e93:<subject>`）与 `git diff b11cd717..096c9e93 -- <subject>`（+164/−156）；已批准设计 `docs/design/2026-11-11-protected-config-keys-design.md`（0.8，§1 表行 116–140、F33 行 48）；计划 `docs/plans/2026-11-12-protected-config-keys-plan.md`（0.4，行 51、58、105、126–137、147）。
- 事实核对：`internal/hooks/hooks.go:208-265,302-330`；`go list -deps ./internal/config`（不含 `internal/hooks`，新依赖 `internal/hooks → internal/config` 无环）；`internal/config/settings_registry.go:273-279,678`（`dispatchConfigReader`）；`internal/chat/chat.go:780-792`；`internal/cli/chat.go:21`、`internal/wiring/localenv.go:193`；`internal/cli/config.go:160-168`；`internal/hooks/hooks_test.go:139`；`ConfigKey` 目前无 `Protected` 字段（由 `suga-hyj7` T1 新增）；`tmp/review/suga-hooks/` 存在 `r1-*.md`，同内容已提交到 `docs/review/2026-11-12-multi-command-hooks/`（commit `4edfb889`）。

## 验收标准

- 上轮 HS1–HS5 是否关闭。
- design-contract 必需内容；SR1103、SR1301、SR1403/SR1405/SR1410（scope necessity）、GR107–GR110、SR2004；与 protected-config-keys 0.8 / 计划 0.4 的一致性。
- 每个新增机制或改动能否追溯到核心目标（AC1–AC4，行 38）。

### 上轮发现关闭情况

| ID | 结果 | 依据 |
|---|---|---|
| HS1 | 关闭 | Q1 增 M 并被选定（D7，行 235）；新条目只有 `command`+`timeout_ms`（行 112-117），逐项对应 AC1–AC4；`events`、条目级等待、Pre/Post 过滤、`inspect env` 展示均列为非目标/deferred（行 69），引 SR1405.1 |
| HS2 | 关闭 | 新条目不含 PreToolUse 过滤器，行 168 明确与 F33 无冲突；与 0.8 的唯一差异（条目内 `timeout_ms` 随整块受保护）在行 171 披露并给理由 |
| HS3 | 关闭 | 解析与校验定在 `internal/config/hooks_entries.go`（`ReadHookEntries`，`dispatchConfigReader` 增 `case "hooks"`），依赖方向 `internal/hooks → internal/config` 写明（行 182-184）；`go list -deps` 证实无环 |
| HS4 | 关闭（同类有一处上轮未穷尽，见下） | D1 去掉 GR108 误引（行 229）；装配层只构造（行 188）；Interrupt 失败沿用 `Call` 的 Warn（行 136，GR109）。上轮未穷尽：Stop 单条失败写“记日志”（行 135、151，0.1 行 128 即已存在），未写级别 |
| HS5 | 关闭 | D6 与 §5 锁定随 `suga-hyj7` 同版或其后发布（行 173、234）；SR1301 鉴权结论因此无条件成立（行 209） |

### 逐项核对结果

| 条目 | 结果 |
|---|---|
| design-contract 必需内容 | 齐全：声明（35-42）、修订记录（6-14）、背景与用户可见结果（44-54）、名词（56-63）、范围/非目标/受影响项目（65-75）、事实与规则 ID（77-91）、方案（93-177）、架构/代码组织（179-189）、流程（191-205）、安全数据运维回滚（207-225）、决策（227-252）、待确认（254-256）、结论与 Gate（258-260，“批准只授权进入计划，不授权实施”） |
| SR1103 | 通过：0.2、0.3 两行均含版本、日期、作者、摘要；0.3 为语义变化（记录用户决策、删去 A），递增合理；用户原话留痕（行 243） |
| SR1301 | 鉴权——已核对（protected + 发布顺序锁定；与 0.8“只挡会话内/非终端的 config set”强度一致，行 209 措辞“会话内不能经 config set 写入”未夸大）；注入防护——已核对（argv 直接执行）；秘密——已核对（trace 只记 `filepath.Base(argv[0])` 与编号）；入参校验——已核对（启动与 `config set` 同一 `ReadHookEntries`，未知字段报错）；资源归属——不适用。无 SECURITY 违反 |
| SR1403 / SR1405 / SR1405.1 / SR1410 | 通过（一处小的范围外改动，见 HS6）。其余新增项均可追溯：逐条 trace 与 `entry`/`program` attrs 对应 AGENTS.md“验证”节要求的 trace 检查及多条目可观察性，并已在用户可见结果披露（行 53）；条目未知字段报错对应 AC3/误配防护；`Result` 逐条记录服务于 trace |
| SR1409 | 通过：高暴露度，引用 tag `v0.55.0` 与执行门/等待链路，两轴独立评审与 ledger |
| GR107 | 不适用（不新增 CLI 入口） |
| GR108 | 通过：装配层 `internal/cli/chat.go`、`internal/wiring/localenv.go` 只构造 client，`hooks.New` 签名不变、`localenv.go` 不改（行 188） |
| GR109 | 基本通过：普通事件与 Interrupt 失败明确记 Warn；Stop 失败级别未写（HS4 上轮未穷尽） |
| GR110 | 通过：`suga config set/get hooks.commands` 经 `internal/cli` 真实分发，含非法值退出码 2 且文件不变（行 222） |
| SR2004 | 通过：不新增 CLI；代码组织参照本仓库 `ReadMCPServerSettings` + Reader 分发与现有分层，每个新文件包归属明确 |
| 与 protected-config-keys 0.8 / 计划 0.4 一致性 | 通过：`Protected:true` 用的正是计划 T1 新增的 `ConfigKey.Protected` 字段（计划行 131）；“执行类”对应计划分类表组 `exec`（计划行 136）；“后合入者补分类表一行”与计划行 105 一致，且 `TestProtectedClassification` 对未分类键失败，兜底成立；`hooks.pre_tool_use.*` 仍由 `suga-hyj7` 承担，不重复。条目 `timeout_ms` 受保护与 0.8“`hooks.timeout_ms` 不保护”的差异已披露 |
| 无法追溯的机制 | 无新增机制；仅 HS6 一处顺带改动 |

## 发现

### HS6 [LOW] “顺带修正 `pre_tool_use` glob 错误键名”不在核心验收内，且与 §2“兼容条目完全相同”字面冲突

- disposition: `CORE_CORRECTIVE`（不属五类：不改变结果正确性、安全或数据；属 scope freeze 披露一致性）
- 证据: 行 184“顺带把 `pre_tool_use.tools` 的 glob 错误按实际键名报告（只改错误文案）”、行 33（HF12 处理）；现状 `hooks.go:236-243` 对 pre 与 post 两类 glob 共用 `hooks.post_tool_use.tools` 文案。行 39 scope freeze 与 AC1–AC4（行 38）均未包含兼容条目的错误文案；行 122 写“兼容条目 0……解析、校验……与现状完全相同”，D2（行 230）写“现有测试断言不改”（`hooks_test.go:139` 只断言 post 文案，故断言不受影响）。同类穷尽：全文“顺带”只此一处；其余改动均已追溯（见逐项核对）。
- 影响: 小；但违反 SR1403.1“不顺带”与 SR1108“不得静默扩大范围”，且兼容条目“完全相同”的承诺在字面上不再成立，计划阶段可能被据此质疑。
- 建议: 二选一：(a) 删去该顺带项，列为 deferred（可另开 Quick issue）；(b) 保留时把它写进 scope freeze（行 39），理由写“新条目错误按 `hooks.commands[i].<字段>` 报告，同函数内统一键名报告方式”，并把行 122 改为“除 pre glob 错误文案外完全相同”。

### HS7 [LOW] 发布顺序锁定的决策引用错为 D13

- disposition: `CORE_CORRECTIVE`（纯引用纠正，按 SR1103/SR1406 不递增版本、不启动新轮次）
- 证据: 行 173“发布顺序锁定（D13、HS5）”；发布顺序决策是 D6（行 234），D13 是“不纳入项目级 hook”（行 241）。同类穷尽：全文 D 编号引用（行 21、27、42、61、67、69、95、97、120、126、138、141、164、173、175）逐一核对，仅行 173 错。
- 影响: 读者按引用会找到无关决策。
- 建议: 改为“（D6、HS5）”。

### HS8 [LOW] 第 1 轮评审来源指向未纳入版本控制的 `tmp/` 路径

- disposition: `CORE_CORRECTIVE`（provenance 元数据纠正，不递增版本、不启动新轮次）
- 证据: 行 11（0.2 摘要“`tmp/review/suga-hooks/r1-standards.md`、`r1-spec.md`”）、行 41（“第 1 轮报告暂存 `tmp/review/suga-hooks/`”）；同内容报告已于 `4edfb889` 提交到 `docs/review/2026-11-12-multi-command-hooks/standards-review.md`、`spec-review.md`，ledger 见 `c51c3872`。同类穷尽：全文 `tmp/` 引用只有这两处。
- 影响: 受版本控制的设计引用工作区临时路径，`tmp/` 清理后来源不可追溯；与行 41 自述的评审目录不一致。
- 建议: 两处改指 `docs/review/2026-11-12-multi-command-hooks/`（第 2 轮报告同样落该目录）。

### HS4（上轮未穷尽）[LOW] Stop 单条失败未写日志级别

- disposition: 沿用 HS4 的 `CORE_CORRECTIVE`，不新增 disposition
- 证据: 行 135“记日志，视为无决定”、行 151“阶段一其他条的失败只记日志与逐条 trace”；现状 `hooks.go:316-326` 对失败记 Warn、对取消与 `ErrTimedOut` 记 Debug。续跑条目的 Stop 失败会让本应开启的下一回合丢失，是用户可感知的结果（GR109 落点）。
- 影响: 计划阶段日志级别不唯一；若按 Debug 实现，续跑失败对用户不可见。
- 建议: 行 135、151 写明“失败记 Warn，取消与超时记 Debug（同现状 `StartStop`）”。

## 覆盖缺口与限制

- 未评 Spec/Executability（两阶段 Stop 冻结锁、`lostReply` 交回、后台任务杀除时序、ptye2e 可执行性）。
- 0.2 中间版未单独评审；本轮以 `b11cd717..096c9e93` diff 加全文复读覆盖其全部改动。
- 未运行测试；`gofer internal/hookrelay` 行号（行 90）与 IDEV-STD 设计行号只按文本存在性抽查，未逐行核对。
- 行 170 写计划 0.4“待批”：候选提交时属实，其后 `341237b3` 已记录批准；属候选之后的事实变化，不作发现。

## 结论

PASS（Standards/Governance 轴第 2 轮 confirmation）。HS1–HS5 全部关闭；无开放 `CORE_BLOCKING`；新增 HS6–HS8 及 HS4 上轮未穷尽项均为 LOW `CORE_CORRECTIVE`，未引入无法追溯到核心目标的机制。

## 剩余风险与后续

- HS7、HS8 为纯元数据纠正，按 SR1103/SR1406 沿用 0.3、不需要纠正确认轮；HS6、HS4 残留为小的语义澄清，可在 0.3.x 计划前修订中一并处理，是否另做唯一一轮纠正确认轮由作者按 planning-core-rigorous 判断（本轴认为改动量不需要）。
- 计划阶段：`suga-hyj7` 先合入时，本计划须在 `TestProtectedClassification` 表中加 `hooks.commands: exec`，GR110 分发用例需在“终端为真、`SUGA_SESSION_ID` 为空”条件下写入（计划 0.4 行 147 的拒绝条件）。
- `internal/hooks` 新依赖 `internal/config` 会传递引入较重的 `pkg/agentcore/tools/*` 依赖树；无环、非违规，计划可留意编译与测试耗时。
