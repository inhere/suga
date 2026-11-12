<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护 实施计划 评审（Standards/Governance 轴，第 2 轮 confirmation）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `f897738008b449672ece282f4a841fe3e93a8443`；subject path `docs/plans/2026-11-12-protected-config-keys-plan.md`（上轮 candidate `109ec8c2` / Draft 0.1）
- 修订版本: Draft 0.2
- 评审者: 独立评审子 agent（Standards/Governance 轴，与 Spec/Executability 轴隔离；REPORT_ONLY，不改仓库、不提交）
- 日期: 2026-11-12
- 排除项: 已批准设计 0.8 的决策本身；Spec/Executability 轴的全面执行性评审（只记录核对规范时直接撞到的执行性缺陷）；未变章节沿用第 1 轮结论。

## 输入与方法

- 规范：`tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md` 全文（plan-contract、plan-closure、planning-core-rigorous、plan 模板 1.3.1、SR1403/SR1407/SR1409、GR101–GR110、SR2001–SR2004）；review-contract 与 review 模板 1.2.0（`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`）；仓库 `AGENTS.md`。
- 上轮本轴报告 `docs/review/2026-11-12-protected-config-keys-plan/standards-review.md`（PS1–PS16）。
- 被评对象：`git show f8977380:<plan>` 全文；`git diff 109ec8c2..f8977380 -- <plan>`；依赖章节对照 `git show f8977380:docs/design/2026-11-11-protected-config-keys-design.md`（§3、架构、验证要求、D1–D10）。
- 只读核实：`scripts/acpprobe/acp_probe.py`（参数与 `--home` 行为）、`tmp/xhen-p1/{wt-repo,host-merge}.ps1` 参数、`internal/cli/{config.go,i18n.go}`、`internal/i18n/catalog/zh/{cli,status}.ini`、`internal/i18n/i18n.go`、`test/ptye2e/chat_smoke_test.go`（`testEnvLanguage`）、`internal/wiring` 文件列表、相关文件行数、测试名存在性、`internal/cli` 测试有无 `t.Parallel`；`idevstd script validate_document --kind plan` 对 0.2 输出 `ok: true`。

## 验收标准

- plan-contract 必需内容；plan-closure（task 五要素、Capability Discovery 三表、唯一 `host_or_non_offline_action`、baseline）；可追溯性 exact。
- SR1301、SR1403、SR1407、SR1409、GR104、GR107–GR110、SR2002–SR2004；设计“代码组织”（review-contract 要求本轴核对，结构违规为 `CORE_CORRECTIVE`、批准前修正）；AGENTS.md 验证要求。

### 上轮发现处理核对

| 上轮 | 0.2 处理 | 结论 |
|---|---|---|
| PS1（阻断） | T5 验证 `go test -tags ptye2e ./test/ptye2e -run 'TestChatProtectedConfig\|TestConfigSetProtectedInTerminal' -count=1 -v`，要求两个 `--- PASS`；T7 Windows 同命令带 tag；ptye2e 子进程环境由 `testEnvLanguage` 剔除 `SUGA_*`（`chat_smoke_test.go:428-436`） | 关闭 |
| PS2（阻断） | T4 进程级缓存 `map[规范化配置路径]*protectedState` + `recorded map[string]bool`，ACP 每 prompt 新 `Open` 共用同进程缓存 | 关闭（机制所在包另见 PS19） |
| PS3 | a→T3b；b、c、d→T3 用例清单；e→T5 `TestConfigSetProtectedInTerminal` 管道被拒 + T7 Windows | 关闭 |
| PS4 | T1 第一步盘点，未登记 protected 键按 Semantic Amendment 停止 | 关闭 |
| PS5 | 补 D2、D9、替代声明 | 部分关闭，上轮未穷尽（见 PS5 续） |
| PS6 | `updateProtectedKeyLocked(path, key string, data []byte) error` 在读回校验后调用；`LoadedConfigBytes` 合同；`ProtectedChange`/`ProtectedDigest` 类型；`ResetProtectedCheckForTest` 在 wiring；`OnBannerShown` 失败 Warn；`EventConfig` | 关闭 |
| PS7 | 键名、`{name}` 占位、zh/en（cli）与 zh（status） | 部分关闭，上轮未穷尽（见 PS7 续） |
| PS8 | 列出主机 worktree、合并、Windows、staticcheck 固定 2026.2.1 | 关闭（0.2 新增 acpprobe 的同类遗漏见 PS18） |
| PS9 | 任务内只跑 focused；包级/全量在 T7 | 关闭 |
| PS10 | baseline HEAD `97e26d11`、status 干净；登记 `suga-5xmf` 与落地顺序 | 关闭 |
| PS11 | T7 加 acpprobe 或 `UNVERIFIED_REAL_ENV` | 形式关闭，实际验证为空（见 PS17） |
| PS12 | 测试统一 `t.Setenv("SUGA_SESSION_ID","")`；`setupConfigCLI` 注入“是终端” | 关闭 |
| PS13 | 新文件放新逻辑；T4 完成标准限三文件净增 ≤20 行 | 关闭 |
| PS14 | CAP-03 与 T2 统一为包级可替换变量；`internal/cli` 测试无 `t.Parallel`，无竞态 | 关闭 |
| PS15 | T1 测试名改正；T6 grep 写运行目录与英文表述 | 关闭（0.2 新增测试名同类见 PS15 续） |
| PS16 | 前置检查写明 SR2002 断言库与 `t.Run` | 关闭 |

### 0.2 治理轴核对

| 项 | 结论 |
|---|---|
| 模板首行、`Draft 0.2`、修订记录（含按类自查范围） | 通过；validator `ok: true` |
| 思考声明（RIGOROUS、核心目标、scope freeze、DEFER_OR_REQUEST、budget、停止条件） | 通过 |
| 目标/完成定义、范围/排除/授权 | 通过；范围列表漏 `pkg/sugasdk`（PS20） |
| 唯一 `host_or_non_offline_action=REQUIRED`（计 1 处） | 结构通过；适用描述漏 acpprobe 真实 provider 调用（PS18） |
| 输入与批准证据（设计 0.8、原话、评审目录） | 通过 |
| Capability Discovery 三表、CAP-01..04 唯一、NONE sentinel；新文件属 owner 扩展 | 通过（SR1403） |
| workspace baseline、并行工作、fail-closed、测试隔离 | 通过 |
| 波次与依赖（W1–W5，T3b 插入） | 通过 |
| 任务五要素与运行目录 | 通过；T7 acpprobe 见 PS17 |
| 回滚、人工 Gate（计划批准 / 执行请求 / 外部动作） | 通过 |
| 可追溯性 | 不完整（PS5 续） |
| 完成 Gate、≤8 行完成总结 | 通过 |
| 超出设计的任务 | 未发现。`configFileName()` 改动、`scripts/ptysmoke/chat_onboarding.py` 改键、T3b、子 agent 会话不记 trace 均可追溯到设计 §3/架构/“受影响测试与 scripts grep”或完成定义；子 agent 不记 trace 属计划内澄清（设计写“每个会话”），建议在批准 Gate 一句点明 |

### 规则核对

- SR1301：鉴权、注入、秘密（快照只存摘要、提示与 attrs 只含键名）、入参校验与第 1 轮一致，0.2 未引入新风险。通过。
- SR1403：无新 Module，四个新文件均在既有包。通过。
- SR1407：任务内 focused，全量与 staticcheck/smoke 在 T7 各一次。通过。
- SR1409：高暴露（`v0.55.0` 已发布），两轴 + ledger。通过。
- GR104 / AGENTS 1000 行：`runtime.go` 951、`runner.go` 938、`chat.go` 899 只加挂点且净增 ≤20 行；`settings_registry.go` 737、`render.go` 795 余量足够。通过。
- GR107：不改 `cmd/suga/main.go`。通过。
- GR108 / 设计“代码组织”：不符，见 PS19。
- GR109：`updateProtectedKeyLocked` 失败 Warn 并写理由；`AdvanceProtectedSnapshot` 失败 Warn；`CompareProtected` 出错 Warn 且不推进。通过。
- GR110：T2/T3b 经 root 真实分发，T5 真实二进制。通过。
- SR2002：前置写明。通过。SR2003/SR2004：沿用 capp 现有命令结构，无新 CLI。通过。
- AGENTS 验证：反向验证 T1–T5 均有且 T1 要求检查非 `[build failed]`；`make smoke-chat`/`smoke-go` 在 T7；Windows 主机 job 在 T7；ACP 真实验证见 PS17。

## 发现

### PS17 [HIGH] acpprobe 真实验证按计划写法必然得到 0 条 trace，“至多一条”恒成立，证明不了去重；命令也缺必需参数

- disposition: CORE_CORRECTIVE
- 证据: 计划 T7（:205）“`python scripts/acpprobe/acp_probe.py` … 跑同一会话两个 prompt，… 确认至多一条 `kind=config`”；完成定义 4（:50）把 acpprobe 列为验收的一部分。`scripts/acpprobe/acp_probe.py:241` `--home` “isolated SUGA_HOME (reset at start)”、`:263` `shutil.rmtree(home…)`、`:272` `SUGA_CONFIG=<config-from>/config.yaml`：每次运行快照目录被清空 → 首次 `Open` 时快照不存在 → 只建快照、变化为空（设计 §3“首次运行只建快照不提示”）→ 同一进程第二个 prompt 复用进程级缓存，仍为空。因此 trace 条数恒为 0，“至多一条”无论去重是否正确都通过。另 `:239-242` `--bin`、`--cwd`、`--config-from` 为 `required=True`，计划命令未给，也未写在容器还是主机运行。
- 影响: T7 会把一次未触达被测路径的运行记为“ACP 真实验证通过”，完成报告误称已做 real-env 验证（SR1402.1 / AGENTS“ACP 行为的真实模型验证”）。不判为 `CORE_BLOCKING` 的理由：设计验证要求中的 ACP 去重证据（“同一会话多个 prompt 只记一条”）由 T4 进程内测试（两次 `Open`）承担且能失败；acpprobe 是 AGENTS 层面的真实环境补充，修正是局部改写一条验证。
- 建议: 二选一写定：①acpprobe 只证明“真实 provider 下带工具调用的 ACP 回合在新功能下正常、首次运行不误报”，期望值写成“恰为 0 条 `kind=config`”，同时在交付说明写 `UNVERIFIED_REAL_ENV: ACP 有变化时的去重（原因：acpprobe 每次重置 SUGA_HOME；替代证据：T4 两次 Open 用例）`；②若要真实覆盖去重，需能预置快照的入口（如给 acpprobe 加保留 home 的选项），这会扩大到 `scripts/acpprobe/`，按 DEFER_OR_REQUEST 处理。两种都补全 `--bin <worktree 构建产物> --cwd tmp/… --config-from ~/.suga --prompt … --prompt …` 与运行位置。

### PS18 [MEDIUM] `host_or_non_offline_action` 适用描述漏列 acpprobe 的真实 provider 调用（PS8 同类，0.2 新增出现点）

- disposition: CORE_CORRECTIVE
- 证据: 计划 :58 只列主机 worktree、合并、Windows 测试与 staticcheck 下载；T7（:205）新增 acpprobe“读真实 config.yaml/.env”，`acp_probe.py:12` 把真实 `.env` 读入服务端环境并对真实模型发请求（联网、使用真实凭据、产生费用）。同类全部出现点：仅此一处（`make smoke-chat`、`smoke-go` 用假模型，`go test` 离线）。
- 影响: 审批 Gate 上看不到这项联网/凭据使用动作。
- 建议: 在 :58 加一项“acpprobe 以真实凭据调用模型 provider（读取不复制 `.env`，不改真实配置）”，并写明运行位置。

### PS19 [MEDIUM] 比较缓存、会话去重与推进判定放在 `internal/wiring`，与设计“代码组织”和 GR108 不符

- disposition: CORE_CORRECTIVE
- 证据: 设计“架构”节：“`internal/wiring`：装配时调用 config 的比较函数，把键名列表交给各入口，自身不含判断逻辑（GR108）”。计划 T4（:171-173）把以下逻辑放进新 `internal/wiring/protected.go`：进程级缓存与 `ResetProtectedCheckForTest`、`ok=false`/出错时“标记不可推进”、`AdvanceProtectedSnapshot` 的“仅在比较成功且变化非空时写入”、按（配置路径, session ID）“只返回一次 `ok=true`”的去重集合与 attrs 组装。同类全部出现点：仅 T4 这一处（T3 的 `internal/config` 与 T2 的 `internal/cli` 改动符合设计归属）。说明：`internal/wiring` 现有 `origin_guard.go`、`danger_guard.go` 等策略文件，包内惯例并不纯装配，但本设计显式约束了这一项。
- 影响: 结构条目偏离已批准设计的代码组织；按 review-contract 属批准前必须修正的结构问题。
- 建议: 缓存、去重与“可否推进”判定放 `internal/config`（与 `CompareProtected` 同 owner，例如 `func CheckProtected(path string, data []byte) ProtectedResult`、`func ClaimProtectedNotice(path, sessionID string) bool`、`func ResetProtectedCheckForTest()`），wiring 只调用并把结果/回调接到 chat、run、Runner；或在计划中写明偏离设计的理由并请批准时确认。

### PS20 [LOW] 范围列表漏 `pkg/sugasdk`

- disposition: CORE_CORRECTIVE
- 证据: 范围（:55）列出的目录不含 `pkg/sugasdk`；T4 文件（:169）“测试同目录与 `pkg/sugasdk`… 的 ACP 测试”，验证命令（:178）含 `./pkg/sugasdk`。
- 影响: 范围声明与任务文件不一致，实施时触及该目录会被误判为扩围。
- 建议: 范围补 `pkg/sugasdk`（仅测试）。

### PS5（续）[LOW] 可追溯性仍不 exact——上轮未穷尽

- disposition: CORE_CORRECTIVE（沿用 PS5，不新增 disposition）
- 证据: 可追溯表（:226-235）。0.1 的“替代声明、文档 → T1、T6”行在 0.2 被改成只含“替代声明”，设计“文档”条目与完成定义 5（README、gofer-personal-agent、CHANGELOG）不再有映射；设计“受影响的现有测试”与“scripts 调用 grep”（对应 T2 测试改写、T6 `chat_onboarding.py`）未映射；“验证要求”只列 Windows 一行，验证列仍是类别描述而非测试名/命令。
- 影响: 不满足 plan-contract“从决策/需求到每个任务及其验证的 exact 可追溯性”。
- 建议: 补“文档 → T6 → 两条 grep + CHANGELOG 检查”“受影响测试与 scripts → T2、T6 → 对应测试名/grep”两行；验证列写具体测试名（如 `TestProtectedClassification`、`TestConfigProtected`、`TestDigestProtected`、`TestChatProtectedConfigBanner`）。

### PS7（续）[LOW] 文案获取方式与 status 英文文本仍未写定——上轮未穷尽

- disposition: CORE_CORRECTIVE（沿用 PS7）
- 证据: T2（:136）写 `i18n.T("config.protected_refused", …)`；仓库无包级 `i18n.T`（只有 `Catalog.T`，`internal/i18n/i18n.go:158`），`internal/cli` 现有写法是 `cliText(tr, "cli.config.written", …)`（`internal/cli/config.go:83`，`internal/cli/i18n.go:39`），键带区段前缀 `cli.`。T4（:177）三条 status 文案只给 zh，en 写“对应”；`{keys}` 的连接方式未写。
- 影响: 实施者需自行决定键前缀、取文案入口与英文文本，`chat_i18n` 英文冒烟可能出现缺键。
- 建议: 写成 `cliText(cliTranslator(), "cli.config.protected_refused", map[string]any{"key":…, "path":…})`；status 键写全 `status.protected.*` 的 zh/en 全文与 `{keys}` 连接符。

### PS15（续）[LOW] T3 验证正则引用的 `TestConfigLockPath` 现不存在，也未声明为新增用例——上轮未穷尽

- disposition: CORE_CORRECTIVE（沿用 PS15）
- 证据: T3 验证（:155）`-run '…|TestConfigLockPath'`；`internal/config`、`internal/cli` 中无 `func TestConfigLockPath`；T3 用例清单未列锁路径规范化用例（设计 §3“配置锁路径也用同一规范化后的路径（三处一致）”）。
- 影响: 锁路径规范化可能无专门用例，正则匹配不到也不报错。
- 建议: 在 T3 用例清单加 `TestConfigLockPath`（相对 `SUGA_CONFIG` 与绝对路径得到同一锁；Windows 大小写），或从正则删去。

## 覆盖缺口与限制

- 未运行测试；acpprobe 结论基于脚本源码（`--home` 重置）与设计“首次运行只建快照”。
- 只读复核 changed/dependent 章节，未变章节沿用第 1 轮结论。
- `internal/cli/chat.go` 目前无日志器，`OnBannerShown` 的 “log Warn” 取哪个 logger 属执行细节，交 Spec 轴。
- 报告写在 `tmp/review/suga-config-keys-plan/`，归档到 `docs/review/2026-11-12-protected-config-keys-plan/` 由调用方处理。

## 结论

PASS（本轴）。PS1、PS2 两个阻断已关闭；PS3、PS4、PS6、PS8–PS14、PS16 已处理；本轮无 `CORE_BLOCKING`。开放 `CORE_CORRECTIVE`：PS17、PS18、PS19、PS20 及上轮未穷尽的 PS5（续）、PS7（续）、PS15（续），计划批准前应修正（PS19 为结构项，按 review-contract 必须修正或写明偏离理由）。按 planning-core-rigorous，修订后可做唯一一轮纠正确认轮（只看 changed scope）。

## 剩余风险与后续

- 修订后复核范围：T4 归属（PS19）、T7 acpprobe 与授权段（PS17、PS18）、范围列表、可追溯表、T2/T4 文案、T3 用例清单。
- `suga-5xmf` 与本计划共用 `settings_registry.go` 与分类表，落地顺序已写定，实施前再核对。
