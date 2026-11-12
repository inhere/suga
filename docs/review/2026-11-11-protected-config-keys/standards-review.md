<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 0.6 评审（Standards/Governance 轴，第 5 轮）

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md`
- 候选四元组：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `1832e4652d8a9104284910cf0f428f1da27c0cb5`；subject `docs/design/2026-11-11-protected-config-keys-design.md`；revision `0.6`（上轮候选 `c5b77adc` / 0.5，本轴上轮 BLOCKED：S18）
- 修订版本：Draft 0.6
- 评审者：Standards/Governance 轴独立子 agent（未参与本设计起草或修订）
- 评审轴：Standards/Governance；第 5 轮（blocking closure）；`REPORT_ONLY`
- 日期：2026-11-11（容器时间）
- 排除项：用户已选的 D3–D10 本身不评；Spec/Executability 细节只在影响治理判定时提及。

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（全文：review-contract、review 模板 1.2.0、review-candidate-loop、planning-core-rigorous、SR/GR 条目）、`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（全文：design-contract、design 模板 1.2.0）；suga `AGENTS.md`（全文）。
- 上轮本轴报告 `docs/review/2026-11-11-protected-config-keys/standards-review.md`（第 4 轮，S18–S20、S16 残留）；Spec 轴报告 F25–F30 段落（只读因果链，用于判断 0.6 整文件回退触发条件的来由）。
- 候选：设计 0.6 全文（`git show 1832e465:<subject>`，250 行）与 `git diff c5b77adc..1832e465 -- <subject>`。
- 代码全量盘点（HEAD = 候选 `1832e465`）：
  - 全部非测试文件中的 `gkconfig.*` 调用（54 个文件，164 处读取），含 `Get/String/Strings/Bool/Int/Data`；
  - 以变量或拼接键名读取的辅助函数及其全部调用点：`hooks.stringList`、`config.chatSwitch`、`readLogNumber`、`tasks.positive`、`fold/compact_knobs/memory_runtime` 的 `read`、`cli_mcp.mcpAllowlist`、`runtime_build.toolNameList`、`readBudget*`、`resolveModelRef`、`agents.go` 的 map 键、`wiring/memory.go` 的 `key+"command"/"args"/"timeout_sec"`、`origin_judge.go`/`read_deny.go` 的键常量、`run.RunDeadlineEnvKey`；
  - 整段读入的映射：`providers`（`providers.go:214-235` 子键、`readModelConfigs` 378-393 行）、`tools.approval_rules`、`tools.snapshots`、`tools.spill`、`gc.retention`、`retry`、`timeouts`（各自的严格子键白名单）；
  - 注册表全部 `Pattern`（199 个）与上述读取键求差集；
  - 其他配置来源：`load.go:33-75`（只加载一个 config.yaml）、全部 `os.Getenv/LookupEnv("…")`、`config.yaml` 字面出现处、`wiring.Open/OpenCLI` 全部调用点、`pkg/agentcore/llm/compat.go`（`providers.<id>.compat` 是否改变去向）。
- 方法：先核对 S16、S18、S19、S20；再做 S18 同类穷尽（运行时读取且符合 110 行分类原则、但不在比较范围的键）；最后对 0.6 修订做 scope necessity → internal validity 核对。

## 验收标准

design-contract 必需内容与质量检查；SR1103、SR1301、SR1403、SR1410；GR108、GR109、GR110；SR2004；review-contract 的 severity/disposition、`CORE_BLOCKING` 五类与 necessity proof、新机制 scope necessity、同类穷尽。

## 上轮 S16、S18、S19、S20 关闭核对

| ID | 状态 | 证据 |
|---|---|---|
| S18（CORE_BLOCKING） | 关闭 | 110 行：不可写或秘密键也可标 `Protected`，只用于启动比较；114 行执行类加入 `hooks.pre_tool_use.tools`、`hooks.pre_tool_use.name`（新增描述符）；117 行端点类加入 `network.proxy.url`、`tools.web_fetch.proxy`（仍不可写，只参与比较）；147 行比较范围改为“全部标 `Protected` 的注册表键，不论是否可写、是否秘密”；133 行分类表测试改为覆盖全部注册表键（含不可写与秘密键）；219 行验证项补“代理地址改动只报键名”“`hooks.pre_tool_use.*` 改动被报出”；231 行把这批新增键列为 Gate 上请你确认。上轮因果链（比较集合 ⊂ 可写键）已断开。 |
| S16 残留 | 关闭 | 164 行：`wiring.OpenCLI` 增装配参数关闭比较，compact 传入。代码中 `OpenCLI` 只有 `compact.go:60` 与 `run.go:187` 两个调用点，`acp.go:84` 与 `sugasdk/session.go:98` 走 `wiring.Open`，开关落点唯一；装配参数只是连线，符合 GR108。 |
| S19 | 关闭 | 151 行：快照同存逐键与整文件摘要；任一侧整文件模式按整文件比较；快照缺整文件摘要视为有变化（关闭上轮 (c)）；整文件模式下人工 set/unset 不改快照（关闭 (b)）；156 行推进写启动时摘要（关闭 (a)）。150 行把“中间层点号键”移出触发条件，带点模型名仍逐键。锚点、别名、合并键、多文档仍是触发条件：Spec F27 的探针说明比较器按 AST（`parseConfigDocument`）逐键读，AST 读法下这些写法会与 gkconfig 生效值不一致，必要性成立；它们沿用已有的整文件回退路径，没有新增机制。验证项（219 行）已覆盖各触发写法与整文件模式下的 set。 |
| S20 | 关闭 | 118 行把 `models.judge` 单列为“裁决”，注明“依据 Q4 用户选择；同组其他模型选择键不保护，见风险”；不再挂在端点类下。 |

## S18 同类穷尽：字面与拼接键读取全量盘点

结论：config.yaml 中运行时读取、且符合 110 行分类原则的键，0.6 的 protected 表（114-119 行）已全部覆盖；没有发现新的遗漏键。

- 读取键与注册表差集（非测试代码）：只剩 `hooks.pre_tool_use.tools/name`（0.6 已补描述符）、`llm`（`providers.go:603`，旧段落，出现即启动报错，不生效）、`hooks`（`localenv.go:188`，只用于脱敏显示）以及 `providers`、`tools.approval_rules`、`tools.snapshots`、`tools.spill`、`gc.retention`、`retry`、`timeouts` 这些整段映射。其余差集名（`gofer.*`、`file_*`、`web_*` 等）只出现在测试中。
- 整段映射的子键：`tools.approval_rules`、`tools.snapshots`、`tools.spill`、`gc.retention` 都有严格白名单，未知子键启动报错（如 `approval_rules.go:53-57`、`snapshots.go:66`、`spill.go:83`），白名单与注册表一致。`providers.<id>` 实际读取 `type/base_url/api_key/headers/models/context_window/max_output_tokens/thinking/thinking_style/prompt_cache_retention/prompt_cache/stream_required/vision/compat/retry/timeouts`；模型级读取 `model_id/type/base_url/context_window/max_output_tokens/thinking/thinking_style/stream_required/vision/price`。其中改变去向的只有两级 `base_url`、`type`，已列入保护范围。`compat` 只选请求字段适配器（`compat.go:13-41`），不改 URL；`api_key`、`headers` 是不可写秘密键，不改变去向，按“provider 调参”不保护成立。
- 拼接键名：`wiring/memory.go:93-121` 的 `memory.{user,project}.{command,args,timeout_sec}`，前两项已保护；`cli_mcp.go:22-26` 的 `tools.auto_allow`、`tools.read_only_allow`，`manifest.go:706-709` 的 `memory.taint_tools`、`memory.taint_on_spill`，`memory_runtime.go:59-63` 的 `memory.write`、`memory.write_tainted`，`hooks.go:212-230` 的四个列表键，全部已保护。
- 注册表中不保护的键逐组对照 110 行原则：费用与步数、留存、模型选择、运行参数、偏好、日志、显示、provider 调参各组都不扩大执行或读写范围，也不改变去向或关闭这类检查。`hooks.timeout_ms` 超时按拒绝处理（上轮已核），`tools.snapshots.on_error` 只涉及可恢复性，`skills.always_load` 只在已有 skill 中选择（见限制）。
- 配置文件来源唯一：`Load` 只加载 `SUGA_CONFIG` 或 `SUGA_HOME/config.yaml` 之一（`load.go:51-60`），没有第二份合并的配置文件。
- 配置之外的同类面：`.env` 环境变量中有一项符合分类原则但风险清单没列，见 S21。用户级审批规则文件（`ResolveUserApprovalRulesPath`，`wiring/rules.go:77`）不是 config.yaml 键，与 hook 脚本文件同属配置之外，见限制。

## 0.6 修订治理核对

- **design-contract**：思考与范围声明（47-53）、修订记录（8-15）、背景与用户可见结果（55-69）、名词（71-75）、范围/非目标/受影响项目与接口（77-89）、事实与规则 ID（91-104）、方案（106-168）、架构与代码组织（170-177）、流程（179-196）、安全/数据/运维/回滚/验证（198-221）、决策与选项记录（223-242）、待确认（244-246）、Gate（248-250）都在。
- **SR1103**：0.6 有语义变化（比较范围、新描述符、整文件模式规则、路径规范化、compact 开关），递增正确；摘要（15 行）写明按类自查范围。0.4–0.6 日期 2026-11-12 与容器日期不同，属作者时区，上轮已接受，不另立。
- **SR1301**：鉴权——已核对，CLI 判定不变；注入防护——已核对，不拼接命令；秘密——已核对：秘密键（代理地址）只存 SHA-256 摘要、提示只含键名。按 D10 摘要取的是 config.yaml 原始值：值为 `${VAR}` 时摘要不含秘密；值为字面量时，同一秘密已明文存在于同属 `SUGA_HOME` 的 config.yaml 中，快照（0600）不额外泄露；入参校验——已核对，沿用现有校验；资源归属——不适用。
- **SR1403 / SR1410（新机制 scope necessity → internal validity）**：
  - 不可写、秘密键可标 `Protected`、只参与比较——必需（S18 因果链），最小（不改可写性）；
  - `hooks.pre_tool_use.*` 新描述符——描述符本身必需；设为 `Writable:true` 不是比较所必需，但与 `post_tool_use.*` 和 D5 一致，并已在 231 行列为 Gate 上请你确认，授权边界已披露，不立 finding；
  - 快照同存两种摘要——关闭 S19/F26 的实现歧义，复用已有整文件路径，最小；
  - 锁与快照路径同一规范化——关闭 F28，只改路径计算方式，没有新机制；
  - “比较与 `Load` 读取同一份字节”——关闭 F29，是取值来源的约束，没有新状态；
  - `OpenCLI` 装配参数——见 S16，最小落点；
  - 131 行“计划阶段盘点全部字面键读取”——本轮已代为完成（见上节），未发现需要补描述符的其他键；计划阶段保留这一步只作回归核对。
  - 未发现无法追溯到核心目标的新机制。
- **GR108**：wiring 只连线（164、176 行）；推进由 `internal/chat` 经回调触发（161、177 行），成立。
- **GR109**：快照写失败记 Warn 并写理由（168 行），未变，成立。
- **GR110**：217 行经 `internal/cli` 真实分发并反向验证，未变，成立。
- **SR2004**：172 行参照 suga 自身分层，成立。

## 发现

### S21 [LOW] 风险清单的 `.env` 同类未穷尽：`SUGA_HOOKS=off` 写进 `SUGA_HOME/.env` 会关闭全部 hook，包括 PreToolUse 拒绝门

- disposition：`CORE_CORRECTIVE`
- 证据：`hooks.go:209` 在 `SUGA_HOOKS=off` 时返回 nil client（hook 全部关闭，包括 0.6 列为执行前拒绝门的 `hooks.pre_tool_use.*`）；`load.go:46` 把 `SUGA_HOME/.env` 载入进程环境。设计 207 行只列了 `.env` 中的 `SUGA_APPEND_SYSTEM_PROMPT`（Spec F30 的处理），没列这一项。按 110 行原则，它属于“关闭约束这些能力的检查”。
- 同类穷尽：全部非测试 `Getenv/LookupEnv` 名中，`SUGA_HOME/.env` 能设置、且符合 110 行原则的只有 `SUGA_APPEND_SYSTEM_PROMPT`（已列）和 `SUGA_HOOKS`（未列）。`SUGA_MODEL` 属模型选择组，`SUGA_MAX_STEPS`、`SUGA_MAX_OUTPUT_TOKENS` 属费用组，`SUGA_LANGUAGE`、`SUGA_THEME`、`SUGA_DEBUG` 属显示与日志；`SUGA_HOME`、`SUGA_CONFIG` 在 `.env` 中设置无效（`load.go:45-47`）；`OPENAI_BASE_URL`、`AGENTCORE_MCP_COMMAND` 只在 `pkg/agentcore/examples` 中读取。
- 不判阻断的理由：D10 和 83 行非目标已由你选定“不比较 `.env`”，核心目标针对 config.yaml 中的配置键；这一项只是如实披露已接受风险的完整性，不改变核心验收。
- 建议：207 行加一句“`.env` 中 `SUGA_HOOKS=off` 会关闭全部 hook（含 PreToolUse 拒绝门），同属 `.env` 不比较的限制”；README/usage 的限制说明同步。

### S22 [LOW] 0.6 扩大比较范围、增加整文件摘要后，有几处旧表述没跟着改

- disposition：`CORE_CORRECTIVE`
- 证据（同类穷尽，全部出现点）：
  - 230 行 D6：“显式分类表覆盖全部可写键”，与 133 行“覆盖全部注册表键（含不可写与秘密键）”冲突。（238 行 Q4 选项记录是历史原话，保留即可。）
  - 125 行 `network.proxy.sites` 不保护的理由仍是“代理地址是秘密键不可写”，0.6 起实际理由是“代理地址本身受比较保护”（117 行）。
  - 75 行名词“配置快照：每个 protected 键……原始取值的摘要”与 214 行“数据：键名 → 摘要”，没有包含 151 行的整文件摘要。
- 影响：计划作者按 D6 写分类表测试时，可能只覆盖可写键，S18 的防漏标测试就失效了。其余几处只影响读者理解。
- 建议：D6 加一句“0.6 起分类表覆盖全部注册表键（S18）”；125 行理由改为“只选择走哪个代理，代理地址本身受保护”；75、214 行补“及整文件摘要”。

## 覆盖缺口与限制

- 未运行代码或测试；盘点基于静态 grep 与阅读。动态读取只追到 `internal/` 与 `pkg/` 中的 `gkconfig` 调用，以及以 key 参数转发的辅助函数；通过 `gkconfig.Data()` 整树读取的只有 `cli/config.go:215`（列键）与 `settings_registry_source.go`（按注册表键显示来源），都不影响运行时行为。
- `OUT_OF_SCOPE` 观察（不立 finding）：用户级与项目级审批规则文件（`wiring/rules.go:72-118`）可增加 allow 规则，`SUGA_HOME/skills` 下的 skill 文件可写入指令。它们与 hook 脚本文件一样在 config.yaml 之外，不在 scope freeze 内。README/usage 的限制说明可一并提及，是否纳入由你决定。
- `skills.always_load` 沿用上轮判断（只在已有 skill 中选择；skill 列表本身已把描述注入提示词，always_load 的增量很小）。
- Spec/Executability 细节（“同一份字节”的实现方式、快照 JSON 结构、i18n 文案）未深评。

## 结论

`PASS`（附 `CORE_CORRECTIVE`）：S18 已关闭，比较范围覆盖全部标 Protected 的注册表键，四个遗漏键已补。全量盘点字面与拼接键读取后，没有发现其他符合分类原则却不在比较范围内的 config.yaml 键。S16、S19、S20 关闭。新增 S21、S22 均为 `LOW` / `CORE_CORRECTIVE`，没有开放的 `CORE_BLOCKING`。0.6 的修订都能追溯到上轮发现，没有引入无法追溯到核心目标的新机制。

## 剩余风险与后续

- 按 planning-core-rigorous，blocking closure 末轮全 PASS 但含 `CORE_CORRECTIVE`：可修订 S21、S22 后做唯一一轮纠正确认轮（candidate 须变化）；不做则 Gate 上按“未复核修订”披露。S22 中 D6 那一处建议在计划批准前改掉，以免计划作者照 D6 写测试。
- Gate 上需要你确认的仍是 231 行列出的评审补入键，其中包括 0.6 新增的可写 `hooks.pre_tool_use.*`，以及只参与比较的两个代理地址。
- 已知且已接受的限制不变：刻意绕过 CLI 判定、改写或删除快照、`echo /exit | suga chat` 推进快照、`.env`（含 S21）、配置之外的脚本、规则与 skill 文件。
