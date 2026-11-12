<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 R5 评审：Spec/Executability 轴

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md` 的可执行性、是否有歧义、事实是否准确
- 候选四元组：git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `1832e4652d8a9104284910cf0f428f1da27c0cb5`；subject 为上述设计；revision Draft 0.6（上轮 `c5b77adc` / 0.5，本轴上轮 PASS）
- 修订版本：Draft 0.6
- 评审者：spec 轴独立子 agent，第 5 轮（纠正确认轮），REPORT_ONLY
- 日期：2026-11-11
- 排除项：用户已选定的 D3–D10 本身不评；Standards/Governance 轴另行评审；不改仓库、不提交

## 输入与方法

- 规范：payload `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（review-contract、review-candidate-loop、rules-*）与 delta `tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（design-contract、design 模板）；仓库 `AGENTS.md`。
- 上轮本轴报告 `docs/review/2026-11-11-protected-config-keys/spec-review.md`（第 4 轮，F25–F30）；设计 0.6 全文（`git show 1832e465:<subject>`，下文“第 N 行”均指 0.6）与 `git diff c5b77adc..1832e465 -- <subject>`。工作树干净，代码行号按工作树（与候选一致）。
- 读过的代码：
  - `internal/config/settings_registry.go`（全部 `Pattern` 行，对非 `Writable:true` 键机械列举；`:118-122`、`:230-253`、`:273-279`、`:312-314`）；
  - `internal/config/load.go:33-67`（`Load` 不持锁、经 `gkconfig.LoadFiles/LoadExists` 自行读文件；`.env` 只保护 `SUGA_HOME`/`SUGA_CONFIG`）；
  - `internal/config/network_proxy.go:20-56`（`network.proxy.url` 原始值保留 `${VAR}`，`expandNetworkProxyURL` 展开）；`internal/wiring/capabilities.go:480`、`network_proxy.go:43`（`tools.web_fetch.proxy` 经 `gkconfig.String` 读取）；`pkg/agentcore/llm/provider_client.go:17`（未配 `network.proxy` 时保留 `http.ProxyFromEnvironment`）；
  - `internal/hooks/hooks.go:195-290`（`SUGA_HOOKS=off` 在 `:209`；`hooks.pre_tool_use.tools` 在 `:230` 经 `stringList` 读、`.name` 在 `:259` 经 `gkconfig.String` 读）；`internal/wiring/localenv.go:186-200`；
  - `internal/config/yaml_edit.go:102-240`（`configLockPath`、`editConfigFile`、`parseConfigDocument`、`validateMappingKeys`）；
  - `internal/wiring/runtime.go:29-70,235-260,380-400`（`Options`、`OpenCLI`、`Open`）；`internal/cli/compact.go:50-62`、`run.go:187`、`acp.go:84`、`pkg/sugasdk/session.go:98`、`engine.go:157`、`cmd/suga/main.go:38,52`（全部 `wiring.Open*` 与 `appconfig.Load` 调用方）；
  - 依赖源码：`gookit/config v2.2.9` 的 `yaml/yaml.go`（驱动即 `goccy/go-yaml` 的 `Unmarshal`）、`read.go:150-175`（只在顶层查字面量整键）、`load.go:293`（`LoadSources` 存在）；`goccy/go-yaml v1.19.2` 的 `ast/ast.go:42,951`（`MergeKeyType` 存在）。
- 机械盘点：用 grep 收集代码中 `gkconfig.*("<字面键>")` 与 `stringList("<键>")` 读取，与注册表 `Pattern` 求差；配置键类的未登记项只有 `hooks.pre_tool_use.tools/name`（0.6 已补），其余差集为整块读取（`tools.approval_rules`、`hooks` 等，其子键均已登记）或测试/非配置字符串。
- 方法：逐项核对 F25–F30；按任务要求复核 0.6 新增内容（秘密与不可写键的取值、`hooks.pre_tool_use.*` 描述符、整文件模式触发检测、两种摘要并存与模式切换、compact 装配参数、锁与快照路径规范化）；同类穷尽。

## 验收标准

- 核心目标第 49 行；用户可见结果第 66–69 行；§3 第 145–168 行；§2 第 140 行。
- review-contract：只有 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 可标 CORE_BLOCKING，须给 necessity proof；同类穷尽，后续轮次再现标“上轮未穷尽”。
- SR1301；GR108–GR110；AGENTS.md 验证规范。

## F25–F30 关闭核对

| 上轮 | 结果 | 设计位置 | 证据 / 剩余 |
|---|---|---|---|
| F25 | **已关闭** | 第 150、219 行 | 整文件触发改为只看顶层字面量点号键，并写明“中间层带点的键名（如模型名 `gpt-4.1`）照常逐键比较”；用例“带点模型名仍逐键”。与 gkconfig 只在顶层查字面量整键一致（`gookit/config read.go:163`）。 |
| F26 | **已关闭（附 LOW）** | 第 151、219 行 | 快照同存逐键与整文件摘要；任一侧整文件模式即按整文件比较、缺整文件摘要视为有变化；整文件模式下人工 set 不改快照；用例已补。推演：逐键模式下人工 set 只改该键、整文件摘要因此过期，但过期摘要只会让后续整文件比较多报，不会漏报（要相等就得把文件回退到不含整文件写法的旧内容，此时按逐键比较仍报出被 set 的键）。剩余：快照格式未写模式字段、相关段落未同步，见 F32。 |
| F27 | **已关闭（附 LOW）** | 第 149、150、219 行 | “映射键排序、列表保持原顺序”；触发条件含内联 `<<` 与多文档；用例已补。可实现性：顶层点号键遍历根映射键即可；锚点/别名用 `ast.FilterFile(ast.AnchorType/AliasType)`（`parseConfigDocument` 已有）；合并键用 `ast.MergeKeyType`（v1.19.2 存在）；多文档看 `len(file.Docs)`。gkconfig 的 YAML 驱动就是 goccy `Unmarshal`，与 AST 同一解析器，不存在“AST 解析失败而 gkconfig 成功”的分歧；重复键、根非映射时 `Load` 本身失败。剩余：空文档/只有注释的文件未说明（`parseConfigDocument` 会拒绝，但应按全部未设置），并入 F32。 |
| F28 | **已关闭** | 第 154 行 | “配置锁路径也用同一规范化后的路径（三处一致）”。需改 `configLockPath`（`yaml_edit.go:109-110` 现为对原始路径求哈希），见剩余风险。 |
| F29 | **已关闭（附 LOW）** | 第 149、219 行 | “比较与 `Load` 读取同一份文件字节”；用例“启动后横幅前的改动下次仍提示”。可实现：`gkconfig.LoadSources(yaml, bytes)` 存在（`gookit/config load.go:293`），`Load` 改为自己读字节再交给 gkconfig 并保留字节。剩余：这要求改 `load.go`，架构节未列，见 F35。 |
| F30 | **已关闭；同类上轮未穷尽** | 第 206、207 行 | `echo /exit \| suga chat` 与 `.env` 里的 `SUGA_APPEND_SYSTEM_PROMPT` 已写入风险。同类还有 `SUGA_HOOKS=off`、`HTTP(S)_PROXY`，见 F34。 |

## 0.6 新增内容复核

- **秘密/不可写键的取值（`network.proxy.url`、`tools.web_fetch.proxy`）**：两者都是注册表静态键，运行时只从 gkconfig 读取（`network_proxy.go:29` 用 `gkconfig.Get` 拿原始值再自行展开 `${VAR}`；`capabilities.go:480` 用 `gkconfig.String`）。按 D10 应取 `config.yaml` 中未展开的原始字符串，`${PROXY_URL}` 这种写法只比较引用本身；环境变量不会映射到这两个键（`Load` 只开 `ParseEnv`，即 `${VAR}` 展开）。第 147、149 行的规定足以唯一实现。人改这两个键只能直接编辑文件，下次 chat 会把自己的改动列出来一次，可接受。唯一的旁路是未配置 `network.proxy` 时 `HTTP(S)_PROXY` 环境变量生效，见 F34。
- **`hooks.pre_tool_use.*` 描述符**：`hooks.go:230,259` 是直接按字面键读取，加描述符对运行时读取无影响（无 `Reader`，不进入 `validateLoadedSettings`）。对 config set 的影响：这两个键从“未知键被拒”变为“终端可写”，是新增的 CLI 能力，见 F33。
- **整文件触发检测**：可用现有 goccy AST 可靠实现，见 F27 行。
- **两种摘要并存、模式切换、整文件模式下 set 不改快照**：无漏报路径（见 F26 行推演）；格式与段落未同步见 F32。
- **compact 装配参数**：`wiring.OpenCLI` 的调用方只有 `run.go:187`（chat/run）与 `compact.go:60`；ACP 与 SDK 走 `wiring.Open`。在 `Options` 增字段、compact 传入即可，“等维护命令”实际只有 compact 一个打开 runtime 的命令（memory 命令走 `OpenMemoryStore`，不比较）。可唯一实现。
- **锁与快照路径同一规范化**：可唯一实现；默认配置判定沿用 `configLockPath` 的“路径等于 `SUGA_HOME/config.yaml`”规则即与第 154 行“`SUGA_CONFIG` 指向其他文件时”一致。

## 发现

### F31 [LOW] 分类表扩到全部注册表键后，`serpapi_key` 不在任何分组；D6 仍写“覆盖全部可写键”

- 级别与处置：LOW，`CORE_CORRECTIVE`（与上轮已关闭的 F20 同类，因 0.6 扩大测试范围而重新出现）。
- 证据：
  - 第 133 行测试改为“覆盖全部注册表键（含动态描述符、不可写与秘密键）……出现未分类的键，测试失败”。
  - 非 `Writable:true` 的注册表键共 9 个（`settings_registry.go:118,121,230,231,243,252,253,313,314`）。其中 `network.proxy.url`、`tools.web_fetch.proxy`、`tools.origin_judge.*`、`tools.read_deny*` 在 protected 表中，`providers.<id>.api_key/headers` 被“provider 调参”组覆盖，只有 `serpapi_key`（`:118`）在第 121–129 行的“不保护”分组里找不到。
  - 第 230 行 D6 仍为“显式分类表覆盖全部可写键”，与第 133 行不一致。
- 影响：按第 133 行写测试时，`serpapi_key` 的归属要由计划作者临时决定；不影响核心目标（它是凭据，不改变请求去向）。
- 建议：“不保护”加一组“凭据：`serpapi_key`、`providers.<id>.api_key/headers`（秘密键，本就不可写）”，或把 `serpapi_key` 放入现有某组；第 230 行改为“全部注册表键”。

### F32 [LOW] 整文件模式加入后，快照数据模型与相关段落未同步

- 级别与处置：LOW，`CORE_CORRECTIVE`（同类穷尽：凡是描述快照内容或人工 set 更新快照的位置）。
- 证据：
  - 第 151 行要求判断“快照一侧处于整文件模式”，这需要快照记录当时的模式（整文件摘要始终存在，不能用“有无整文件摘要”推断模式）；但第 214 行数据仍写“（键名 → 摘要）”，没有整文件摘要与模式字段。整文件模式下逐键摘要可能算不出（多文档、别名），存什么也未写。
  - 第 140 行（§2）与第 187 行（关键流程）仍写人工 set“只把该键的摘要更新到快照”，没有带上第 151 行“整文件模式下人工 set/unset 不改快照”的例外；这里的“整文件模式”指当前文件还是快照一侧也没写。推演表明两种理解都不会漏报（快照一侧为整文件模式时，下次一定按整文件比较），所以只是表述问题。
  - 第 167 行 trace “attrs 只含键名列表”是新增接口（第 89 行）；整文件模式下没有键名，attrs 填什么（空列表、哨兵值或另设标记）未定，实现不唯一。
  - 空文档或只有注释的 `config.yaml`：gkconfig 加载为空，`parseConfigDocument` 会拒绝（`yaml_edit.go:203`，`Body == nil`）。设计未说明按“全部未设置”逐键比较，实现者若复用 `parseConfigDocument` 会把它当错误处理。
- 建议：第 214 行改为“模式、整文件摘要、逐键摘要（整文件模式下可为空）”；第 140、187 行补“当前文件或快照处于整文件模式时不改快照”；第 167 行写明整文件模式下 trace attrs 的取值（如 `keys: []` 加 `whole_file: true`）；第 150 行补“空文档按全部未设置”。

### F33 [LOW] `hooks.pre_tool_use.tools/name` 设为可写，是未列入“新增接口”的 CLI 新能力

- 级别与处置：LOW，`CORE_CORRECTIVE`。
- 证据：
  - 第 41、114、231 行：新增描述符“与 post 同为可写+protected”。现状这两个键不在注册表，`suga config set hooks.pre_tool_use.tools …` 会被当作未知键拒绝；加描述符后，人在终端里可以 set/unset。
  - 第 89 行“新增接口（只增不改）”只列了 trace 事件与 `run/protected-config/` 目录。
  - Scope necessity：核心目标里的“启动时比较”只需要 `Protected:true`，不需要 `Writable:true`（第 110 行已允许不可写键标 Protected）；“可写”不是核心目标所必需。它也没有风险，因为 protected 键在会话内仍被拒。
- 影响：用户可见的 CLI 行为多了一项，但设计的接口清单与 GR110 验证要求里都没有它；计划作者可能漏测。
- 建议：二选一。①保持可写：第 89 行加“`suga config set/unset` 新接受 `hooks.pre_tool_use.tools/name`”，第 217 行 GR110 用例覆盖一次；②只标 Protected、不设 Writable，与“只参与比较”的秘密键一样。D6 第 231 行已请你在 Gate 上确认，按所选写明即可。

### F34 [LOW] 上轮未穷尽（F30 同类）：`.env` 直接设置的 `SUGA_HOOKS=off`、`HTTP(S)_PROXY` 同样能绕开比较

- 级别与处置：LOW，`CORE_CORRECTIVE`（上轮未穷尽，沿用 F30 处置；只要求如实记录已接受的风险，不改 D10）。
- 证据：
  - `Load` 把 `SUGA_HOME/.env` 载入进程环境，只还原 `SUGA_HOME`、`SUGA_CONFIG`（`load.go:44-46`）。
  - `SUGA_HOOKS=off` 让 `hooks.New` 直接返回 nil（`hooks.go:209`），关闭全部 hook，包括 0.6 新列为 protected 的 `hooks.pre_tool_use.*` 执行前拒绝门（第 114 行）。
  - 未配置 `network.proxy` 时，模型客户端保留 `http.ProxyFromEnvironment`（`provider_client.go:17`），`.env` 里的 `HTTPS_PROXY` 会改变模型请求的出口，与第 117 行端点类同类。
  - 第 207 行只写了 `SUGA_APPEND_SYSTEM_PROMPT`。代码中其他直接读取的 `SUGA_*` 变量（`SUGA_MODEL`、`SUGA_PROVIDER`、`SUGA_MAX_STEPS` 等）只影响模型选择或步数，属于“不保护”组的同类，不需要列。
- 建议：第 207 行改为“`SUGA_HOME/.env` 中直接写入的环境变量——`SUGA_APPEND_SYSTEM_PROMPT`（持久注入指令）、`SUGA_HOOKS=off`（关闭全部 hook，含执行前拒绝门）、未配置 `network.proxy` 时的 `HTTP(S)_PROXY`（改变请求出口）——不在比较范围（D10）”。

### F35 [LOW] “比较与 `Load` 读取同一份字节”需要改 `Load`，架构节未列

- 级别与处置：LOW，`CORE_CORRECTIVE`（F29 落地后的挂点缺口）。
- 证据：
  - 第 149 行要求比较与 `Load` 读同一份字节；第 155 行要求启动时的读取持配置锁。现状 `Load` 不持锁，并由 `gkconfig.LoadFiles/LoadExists` 自己读文件（`load.go:49-60`），比较拿不到这份字节。
  - 可行做法是 `Load` 持锁读出字节、交给 `gkconfig.LoadSources`（`gookit/config load.go:293`）并保留下来供比较使用；`Load` 的调用方是 `cmd/suga/main.go:38,52` 与 `pkg/sugasdk/engine.go:157`，SDK 路径同样适用。
  - 第 172–177 行架构节只列了 `settings_registry.go`、新文件 `protected_snapshot.go`、`cli/config.go`、`wiring`、输出位置；没有 `load.go`，也没有 `wiring.Options` 的新字段（第 164 行表格里有）。
- 建议：架构节补一条“`internal/config/load.go`：持配置锁读取字节，经 `LoadSources` 加载并保留给比较使用；`internal/wiring/runtime.go` 的 `Options` 增关闭比较的字段（compact 传入）”。

### SR1301 核对

- 鉴权：已核对。CLI 判定只挡直接调用（第 141、202 行如实写明）；新增可写的 `hooks.pre_tool_use.*` 同受 protected 判定约束（F33 只涉及接口清单）。
- 注入防护：已核对。键名精确匹配，不拼接命令；快照文件名用哈希。
- 秘密：已核对。秘密键（代理地址）只存 SHA-256 摘要，提示与 trace 只含键名；快照目录 0700、文件 0600，与存放明文的 `config.yaml` 同一信任域；`run/` 不进 `data restore`/备份。
- 入参校验：已核对，沿用现有校验；`hooks.pre_tool_use.tools` 的 glob 校验在 `hooks.New`（`hooks.go:234-241`），与 post 相同。
- 资源归属：不适用。

## 覆盖缺口与限制

- 没有运行项目测试与 ptye2e，也没有写探针；goccy/gkconfig 行为依据源码阅读与上轮探针结论。
- Windows 下锁与大小写行为按代码推断，未在主机上跑。
- 增量复核：读了 0.5→0.6 diff 与全文；§2、§3、架构、关键流程、风险、验证、决策各节均按 0.6 重新对照。§1 分类原则沿用上轮结论，只对非可写键做了机械对照。
- 字面键盘点用 grep 完成，经结构体映射或整块读取间接使用的子键未逐一追踪（第 131 行已把完整盘点交给计划阶段）。

## 结论

**PASS（Spec/Executability 轴，本轮无 CORE_BLOCKING）。**
- F25、F28 已关闭；F26、F27、F29 主体已关闭，剩余并入 F32、F35；F30 已关闭，同类遗漏见 F34。
- 0.6 新增内容可唯一实现：秘密键按 D10 取原始字符串即可；`hooks.pre_tool_use.*` 描述符不影响现有读取；整文件触发四类均可用现有 goccy AST 检测；模式切换与“整文件模式下 set 不改快照”无漏报路径；compact 的装配参数挂点明确；锁与快照路径规范化可统一。
- 本轮新增 F31–F35，均为 LOW / `CORE_CORRECTIVE`。按 review-candidate-loop，本轮是 PASS 后的唯一纠正确认轮，不再开新轮；这些修正若在计划前做，属“未复核修订”，Gate 上需按规则披露。

## 剩余风险与后续

- 修改 `configLockPath` 的哈希输入后，`SUGA_CONFIG` 场景的锁文件名会变；升级过渡期新旧版本同时写同一配置时互不互斥。持续时间短、后果是丢失更新，可在计划里注明。
- 已接受的风险不变：刻意绕过 CLI 判定；改写或删除快照；`echo /exit | suga chat` 推进快照；`.env`/环境变量间接或直接改变行为（含 F34 补充项）；只用 ACP/run/SDK 的用户提示会一直出现，直到打开 chat。
- 计划阶段：按第 131 行做完整字面键盘点，新发现的键按第 110 行原则归类，并在 Gate 上请你确认。
