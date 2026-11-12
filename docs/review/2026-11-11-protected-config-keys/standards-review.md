<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 0.5 评审（Standards/Governance 轴，第 4 轮）

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md`
- 候选四元组：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `c5b77adcdfeb8be6c051c5a968419f96253be3e2`；subject `docs/design/2026-11-11-protected-config-keys-design.md`；revision `0.5`（上轮候选 `4a67c13c` / 0.4，本轴上轮 PASS）
- 修订版本：Draft 0.5
- 评审者：Standards/Governance 轴独立子 agent（未参与本设计起草或修订）
- 评审轴：Standards/Governance；第 4 轮（上轮全 PASS 含 `CORE_CORRECTIVE` 后的纠正确认轮，同时承接 Spec 轴 F16 的 blocking closure）；`REPORT_ONLY`
- 日期：2026-11-11（容器时间；候选提交时间 2026-11-11 18:46 -0800）
- 排除项：用户已选的 D3–D10 本身不评，只评其落地是否最小、可追溯、内部成立；Spec/Executability 轴细节只在影响治理判定时提及。

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（全文，含 review-contract、review 模板 1.2.0、review-candidate-loop、planning-core-rigorous）、`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（全文，含 design-contract、design 模板 1.2.0）；suga `AGENTS.md`（全文）。
- 上轮本轴报告：`git show 0fe0f5f8:docs/review/2026-11-11-protected-config-keys/standards-review.md`（第 3 轮，S13–S17 与 S10、S11 残留）；Spec 轴第 3 轮 F16、F19 段落（只读取因果链与建议）。
- 候选：设计 0.5 全文（`git show c5b77adc:<subject>`，238 行）与 `git diff 4a67c13c..c5b77adc -- <subject>`；0.2、0.3 的清单与 D4/D6（`git show 50950889:`、`a0845767:`），用于核对 D6 的用户选择追溯。
- 代码事实核对（HEAD = 候选 `c5b77adc`，工作树干净）：`internal/config/settings_registry.go` 全部静态与动态描述符（88-357 行，逐键对照“不保护”分组与 protected 表）、`:371-375`、`internal/config/bootstrap_lock.go:12-21`、`internal/config/yaml_edit.go:129-159`、`internal/config/load.go:33-75`、`internal/config/providers.go:214-235`、`internal/config/network_proxy.go:1-40`、`internal/wiring/runtime_build.go:88-92`、`internal/wiring/localenv.go:186-199`、`internal/chat/chat.go:49`、`internal/personal/process/process.go:650-680`、`internal/hooks/hooks.go:84-100,160-262,350-400`、`docs/usage/gofer-personal-agent.md:120-128,212`；gookit/config v2.2.9 `read.go:149-189`（`GetValue` 的键查找顺序）；全仓库 `gkconfig.*("…")`/`stringList("…")` 读取的键名与注册表 `Pattern` 的差集；`wiring.Open/OpenCLI` 全部调用点；`git tag --contains f5753984`。
- 方法：先逐项核对 S13–S17 与 S10、S11 残留；再对 0.5 的修订做 scope necessity → internal validity 核对；最后按“运行时实际读取、且符合设计 protected 定义的键”做同类穷尽（不再只看 `Writable:true`）。

## 验收标准

design-contract 必需内容与质量检查；SR1103、SR1301、SR1403、SR1405、SR1410；GR108、GR109、GR110；SR2004；review-contract 的 severity/disposition、`CORE_BLOCKING` 五类与 necessity proof、新机制 scope necessity、同类穷尽（含“上轮未穷尽”标注）。

## 上轮 S13–S17 及 S10、S11 残留关闭核对

| ID | 状态 | 证据 |
|---|---|---|
| S10 残留 | 关闭 | D6 主句（218 行）与 Q4 推荐记录（226 行）逐项一致：`providers.<id>.base_url/type`、模型级 `base_url`、`models.judge`、`workspace`、`memory.*.backend`。评审补入键单列（219 行）：模型级 `type`、`tools.shell_timeout_max`、`tools.task_timeout_max`、`tasks.max_*`、`log.file`、`tools.disabled`、`rules.file`，并写明“需你在 Gate 上确认”。我把 0.5 protected 表（108-112 行）逐键回溯到 0.2 的 Q2 清单（`50950889` 49-51 行）、Q3（D5）、Q4、219 行：无未追溯键。D4（216 行）已说明 `tools.shell_limits.*` 0.4 起归执行上限类，与表一致。`models.compact`/`compact_model` 是 0.4 评审补入、从未经用户选定，0.5 移除属缩小范围，不需 Gate 授权。 |
| S11 残留 | 关闭 | 已确认事实 94 行与架构 166 行写明 kind 常量位置 `internal/personal/store/types.go`。 |
| S13 | 关闭 | 全文已无 SR2003（只在 39 行处理表中出现）；规范列表 98 行、129 行、164 行改为“沿用现有 capp 命令结构”。 |
| S14 | 关闭（附注） | 推进值（145 行）：写入启动时算出的摘要，不重读；单键更新（131、144、176 行）：在 `editConfigFile` 已持有的锁内、用本次写入的值；锁不可重入的事实成立（`bootstrap_lock.go:12-21` 每次新开文件描述符加锁，`yaml_edit.go:138` 已持锁）。调用点（150、166 行）：`internal/chat` 经 `chat.Options` 回调触发（`chat.go:49` `Options` 存在），wiring 只连线，符合 GR108。附注：建议的验证项“启动与横幅之间改动的键下次仍提示”未写入 207 行；145 行行为可测，计划阶段补一条用例即可。 |
| S15 | 关闭 | 143 行快照放 `run/protected-config/`（0700，文件 0600）；`personalprocess.List` 跳过子目录（`process.go:661` `entry.IsDir()`），不再依赖进程记录判定细节。 |
| S16 | 部分关闭 | 入口表 153 行加了“`suga compact` 等维护命令不参与比较”。残留：代码组织 165 行仍写“`internal/wiring` 装配时调用 config 的比较函数”，而 `compact.go:60` 与 `run.go:187`（chat/run）走同一个 `wiring.OpenCLI`；如何让 compact 不比较没写（`wiring.Options` 开关，还是比较移到 cli 入口）。不影响核心验收；disposition 沿用 `CORE_CORRECTIVE`（LOW）。 |
| S17 | 关闭 | 0.4 行日期改为 2026-11-12（13 行，纯元数据未升版，符合 SR1103）；0.5 摘要写明按类自查范围（14 行）。 |

## 0.5 修订治理核对

- **design-contract**：思考与范围声明（41-47）、修订记录（8-16）、背景与用户可见结果（49-63）、名词（65-69）、范围/非目标/受影响项目与接口（71-83，新目录已列为新增接口）、事实与规则 ID（85-98）、方案（100-157）、架构与代码组织（159-166）、流程（168-185）、安全/数据/运维/回滚/验证（187-209）、决策与选项记录（211-230）、待确认（232-234）、Gate（236-238）均在。
- **SR1103**：0.5 含语义变化（比较键集、整文件回退、推进取值、清单移除压缩模型、快照目录），递增正确；0.4 日期纠正未升版，正确。0.5 摘要行（14 行）没提快照改为子目录与权限、compact 不参与、ACP 按会话去重，但 39 行之前的处理表（37-38 行）写了；两处合起来完整，不另立 finding。
- **SR1301**：鉴权——已核对，CLI 判定定位不变；注入防护——已核对，不拼接命令；秘密——已核对：快照 0600、只存摘要，整文件摘要对含内联秘密的文件也只存 SHA-256，提示只含键名；入参校验——已核对，沿用现有校验；资源归属——不适用。比较集合的安全覆盖缺口见 S18（不是 SR1301 清单项违规，按 MISSING_FUNCTION 记）。
- **SR1403 / SR1405 / SR1410（新机制 scope necessity → internal validity）**：
  - 键名并集——必需（Spec F16 的因果链：新增 provider 的 `base_url` 漏报），最小；
  - 规范形式摘要——必需（摘要要确定，注释与缩进不能算改动），最小；
  - 整文件摘要回退——必需性部分成立：gookit/config `GetValue` 先按完整键名查顶层（`read.go:163`），所以顶层 `"tools.approval": auto` 在运行时生效，逐键走 YAML 树会漏报。中间层字面量点号键运行时不生效（`read.go:179` 起按分隔符整体切分），触发条件比已证明的需要宽；同时整文件模式下的快照语义没写全，见 S19；
  - 推进取值与锁区间——是对已有机制的澄清，没有新机制；
  - `chat.Options` 回调——最小挂点，GR108 成立；
  - 进程内缓存按配置路径、测试重置函数——有先例（`load.go:27` `ResetLoadedStateForTest`），不算新机制；
  - ACP 按会话去重——修正 F21 的重复事件，代价小；
  - 快照子目录与权限——S15 的修正；
  - `models.compact`/`compact_model` 改为不保护——缩小范围，符合 SR1403；分类原则的一致性见 S20；
  - 未发现无法追溯到核心目标的新机制。
- **GR108**：wiring 只连线；chat 推进由 `internal/chat` 触发（150、166 行），成立。compact 排除的落点见 S16 残留。
- **GR109**：快照写失败只记 Warn 并写明理由（157 行）；set 路径的单键更新在同一临界区内，配置写成功而快照写失败时同样按 157 行处理，成立。
- **GR110**：205 行经 `internal/cli` 真实分发，含空 `SUGA_SESSION_ID`、注入终端判定、无快照不建、拒绝分支反向验证，成立。
- **SR2004**：161 行参照 suga 自身分层，成立。
- **“不保护”分组完整性**：逐键对照注册表全部 `Writable:true` 静态键与动态描述符，114-122 行各组加 108-112 行 protected 表覆盖全部可写键，没有遗漏或重复（`context.handoff_reuse_prefix` 归 `context.*`，`tools.deferred_native/_apply` 归 `tools.deferred*`，`providers.<id>.models.<name>.model_id/price.*` 归 provider 调参）。`hooks.timeout_ms` 归不保护成立：PreToolUse 超时按拒绝处理（`hooks.go:86-91`、`:361-364`）。但分组只覆盖了可写键，覆盖范围本身的缺口见 S18。

## 发现

### S18 [HIGH] 运行时读取、符合 protected 定义但不可经 config set 写入的键，被排除在启动比较之外（上轮未穷尽：S1/S9 同类，清单完整性）

- disposition：`CORE_BLOCKING`，类别 `[MISSING_FUNCTION]`
- 证据：
  - 设计把 protected 定义为“放宽检查、扩大读写或执行范围、引入外部可执行程序或指令、改变模型请求去向的配置键”（67 行，原则 104 行），核心目标第二句是“用其他方式改动这些配置时，你下次打开 chat 能看到”（43 行）。但标记手段只有注册表字段（D1，213 行），分类表测试只覆盖 `Writable:true` 键（124 行），比较集合只取 protected 键（137-138 行）。
  - `hooks.pre_tool_use.tools`、`hooks.pre_tool_use.name` 不在注册表中（`settings_registry.go` 无此 Pattern），运行时由 `internal/hooks/hooks.go:230`、`:259` 读取。PreToolUse 是执行前的拒绝门（`hooks.go:86-91`，fail closed；usage `gofer-personal-agent.md:212`），从 v0.45.0 起已发布（`git tag --contains f5753984`）。把它改成 `[]` 或收窄 glob，门就对相应工具关闭。这正是 104 行的“关闭约束这些能力的检查”。
  - `network.proxy.url`、`tools.web_fetch.proxy` 是 Sensitive、不可写（`settings_registry.go:121`、`:243`），运行时决定模型客户端与 web_fetch 请求经哪个代理（`network_proxy.go:20-24`，`wiring/capabilities.go:480`）。这符合“改变请求去向”。设计 118 行说 `network.proxy.sites` 不保护的理由是“代理地址是秘密键不可写”，只考虑了 config set，没考虑直接改文件。
  - 设计已有同类先例：`tools.origin_judge.*`、`tools.read_deny*` 原本不可写，D5 把它们改为 `Writable:true`+`Protected:true`，从而进入比较集合（133、217 行）。上面四个键没有被同样处理，也没有列为限制。
- 同类穷尽：我取全仓库 `gkconfig.*("…")`/`stringList("…")` 读到的键名与注册表 Pattern 求差集，再加上注册表里不可写的键。符合 protected 定义的只有 `hooks.pre_tool_use.tools`、`hooks.pre_tool_use.name`、`network.proxy.url`、`tools.web_fetch.proxy`。其余不可写键 `serpapi_key`、`providers.<id>.api_key`、`providers.<id>.headers` 不改变请求去向，也不放宽检查；差集中的其他名字是 map 段或测试数据。
- 影响：模型在 run（原生工具默认放行）或 trust 档下用 `shell_exec` 改 `config.yaml`，清空 `hooks.pre_tool_use.tools` 或设置代理，之后的会话里拒绝门失效或请求改道，而 chat 横幅、run stderr、trace 都不提示。设计承诺用启动提示兜住直接改文件（76、132 行），这几个键却兜不住。
- necessity proof：
  - core_acceptance：43 行核心目标第二句，以及 63 行用户可见结果“安全相关配置被其他方式改过时，你下次打开 chat，横幅列出改动的键名”。
  - necessary_causal_chain：不采纳 → 比较集合 = 注册表中 Protected 键 ⊂ 可写键 → `hooks.pre_tool_use.tools` / 代理地址不在集合内 → 直接改文件后摘要不变 → 不提示 → 一个符合 67 行定义的安全相关配置被改动而你看不到。这条链只依赖 D1 与 124、137 行的写法，不依赖实现者的选择。
  - minimal_owner：设计 §1 分类表与 D1/124 行。给这四个键加注册表描述符（`Protected:true`；`hooks.pre_tool_use.*` 可与 `hooks.post_tool_use.*` 对齐为 `Writable:true`，代理地址保持 Sensitive、不可写），并把分类表测试的覆盖范围从“全部 `Writable:true` 键”改为“全部注册表键”。
  - deletion_alternative：`FAIL`。不加入比较集合就关不掉；唯一替代是把这些键写进非目标与风险并请你在 Gate 上确认接受，这会改变核心验收，需要你决定。
  - scope_effect：不新增 Module、Interface、Schema、state、lifecycle、authority 或 external action。只增两三个注册表描述符；若 `hooks.pre_tool_use.*` 设为可写，`suga config set` 多两个可写的 protected 键，属于现有接口内的扩展。
- 建议：按 minimal_owner 修改，在 §1 执行类或放宽类中列出 `hooks.pre_tool_use.{tools,name}`，在端点类中列出 `network.proxy.url`、`tools.web_fetch.proxy`（注明不可写、只参与比较）；把 118 行 `network.proxy.sites` 的理由改为“代理地址本身受比较保护”；验证项补“改 `hooks.pre_tool_use.tools` 或代理地址后 chat 提示”。代理地址可能内嵌凭据，摘要按原始值（多为 `${VAR}`）求，文件已是 0600，可接受；如果担心低熵凭据被离线猜测，可只对这两个键用带随机盐的摘要（盐存在同一快照文件中）。

### S19 [MEDIUM] 整文件摘要回退：触发条件比已证明的需要宽，且整文件模式下的快照推进、单键更新与模式切换没写

- disposition：`CORE_CORRECTIVE`
- 证据：140 行在“锚点、别名、合并键，或顶层/中间层的字面量点号键”时改按整文件摘要比较。
  - 必需性：顶层字面量点号键成立（gookit/config `read.go:163` 先查完整键名）；中间层字面量点号键运行时不生效（`read.go:179-189` 按分隔符整体切分后逐层查），不构成绕过。锚点、别名、合并键是否需要回退，取决于比较器用 AST 还是解码后的值，设计没写。
  - 内部成立性：(a) 整文件模式下 chat 推进写什么（145 行只说“启动时的摘要”）；(b) 人工 set 的“只更新该键”（131 行）在整文件模式下无法执行，是不更新（下次误报你自己的改动），还是重算整文件摘要（会吸收此前未展示的改动，违反 131 行“不重算其他键”）；(c) 文件从逐键模式切到整文件模式时，快照里还没有整文件摘要，是按“有变化”、按损坏（141 行），还是按首次运行（143 行，只建不提示）处理。按首次运行处理会静默吸收切换时一起改动的 protected 键。
- 不判阻断的理由：(c) 的自然实现（没有存过的摘要与当前摘要不同 → 提示）会提示，(b) 受 131 行“不重算其他键”的原则约束，按设计原文实现不会吸收。缺少从“不采纳”到核心验收失败的必要因果链，但实现不唯一的空间应在计划前收掉。
- 建议：按删除优先，把触发条件收窄为“运行时实际生效、而逐键读取看不到的写法”（顶层字面量点号键；比较器若用解码值，则锚点、别名、合并键不需要回退）；如果保留回退，写明快照记录模式与整文件摘要，切换模式按“有变化”提示，整文件模式下人工 set 不更新快照（留到下次 chat 展示），chat 推进写入启动时的整文件摘要。验证项补“逐键 → 整文件切换并同时改 protected 键时提示”。

### S20 [LOW] `models.judge` 仍为 protected，但它的理由与分类原则、“模型选择”组的理由矛盾

- disposition：`CORE_CORRECTIVE`
- 证据：117 行“模型选择”组不保护的理由是“只在已配置的 provider 之间选择”，这条理由对 `models.judge` 同样成立。按 104 行原则，裁决模型的选择既不扩大能力也不关闭检查。`models.judge` 仍列在“端点类”（111 行），但它不是端点。保留它的实际依据是 Q4 用户选择（218、226 行），表里没有写明。
- 同类穷尽：protected 表 108-112 行逐键对照 104 行原则，只有 `models.judge` 不能由原则推出；其余键（含 `log.file`“扩大写范围”、`tools.origin_judge.timeout`“检查参数”、`tasks.max_*`“上限”）都能推出。
- 影响：分类表测试的维护者以后加新的场景模型键时，没有可循的规则；Spec F19 指出的不一致只解决了一半。
- 建议：在表中注明“`models.judge`：Q4 选定，裁决模型不随主模型漂移”，或把它移到单独的“用户选定”一行；104 行原则补一句“以及用户点名的键”。

## 覆盖缺口与限制

- 未运行代码或测试；锁与并发判断基于代码阅读（`bootstrap_lock.go`、`yaml_edit.go`）。
- S18 的差集基于对 `gkconfig.<Getter>("字面键")` 与 `stringList("字面键")` 的 grep；用拼接或变量键名读取的配置可能漏掉。`providers` 段按整张表读入（`providers.go:214`），其中未注册子键（`type`/`base_url` 等之外）只按注册表核对。
- `skills.always_load` 只按“只选择已在 skill 根中的 skill”判断归属，没有追踪模型能否向 skill 根写入新 skill 来持久注入指令；若可以，它与 `rules.file` 同类，建议 Spec 轴或计划阶段复核。
- Spec/Executability 细节（快照 JSON 结构、i18n 文案、ACP 去向表）未深评。

## 结论

`BLOCKED`：S18（`CORE_BLOCKING` [MISSING_FUNCTION]）未关闭。`hooks.pre_tool_use.{tools,name}` 与两个代理地址符合设计自己的 protected 定义，却不在启动比较集合中，直接改文件时核心目标第二句不成立。上轮 S10、S11、S13、S15、S17 已关闭，S14 关闭（附注），S16 部分关闭（`CORE_CORRECTIVE` 残留）；新增 S19、S20 为 `CORE_CORRECTIVE`。0.5 的其他修订（键名并集、规范形式、锁与推进、移除压缩模型、补全不保护分组、D6 拆分、快照子目录）在治理轴上成立，没有引入无法追溯到核心目标的新机制。

## 剩余风险与后续

- S18 关闭路径有二，二选一：(1) 把四个键加入注册表并标 Protected（`hooks.pre_tool_use.*` 可写，代理地址不可写、只参与比较），分类表测试改为覆盖全部注册表键；(2) 写入非目标与风险，并请你在 Gate 上确认接受这一缩小。只有 (2) 需要你决定。
- 按 planning-core-rigorous，本轮出现 `CORE_BLOCKING`，回到 blocking closure 规则；下一轮只需复核 S18 的 changed/dependent scope 与 S16、S19、S20 的修订。
- 已知且已接受的限制不变：刻意绕过 CLI 判定、同时改写或删除快照、在伪终端自起 chat、经 `.env` 改 `${VAR}`、hook 脚本文件本身被改写（配置之外），都挡不住；README/usage 需如实写明（201 行已列）。
