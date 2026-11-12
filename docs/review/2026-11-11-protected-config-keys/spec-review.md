<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 R4 评审：Spec/Executability 轴

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md` 的可执行性、是否有歧义、事实是否准确
- 候选四元组：git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `c5b77adcdfeb8be6c051c5a968419f96253be3e2`；subject 为上述设计；revision Draft 0.5（上轮 `4a67c13c` / 0.4）
- 修订版本：Draft 0.5
- 评审者：spec 轴独立子 agent，第 4 轮（blocking closure），REPORT_ONLY
- 日期：2026-11-11
- 排除项：用户已选定的 D3–D10 本身不评；Standards/Governance 轴另行评审；不改仓库、不提交

## 输入与方法

- 规范：payload `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（review-contract、review-candidate-loop、rules-*）与 delta `tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`；仓库 `AGENTS.md`。
- 上轮本轴报告 `docs/review/2026-11-11-protected-config-keys/spec-review.md`（第 3 轮，F16–F24）；设计 0.5 全文（`git show c5b77adc:<subject>`，下文“第 N 行”均指 0.5）与 `git diff 4a67c13c..c5b77adc -- <subject>`。HEAD 即 `c5b77adc`，工作树干净，代码行号按工作树。
- 读过的代码：
  - `internal/config/yaml_edit.go:25-40,75-84,102-354`（锁路径、`editConfigFile`、`parseConfigDocument`、`configYAMLPath`、`yamlKey`）；`bootstrap_lock.go:12-35`、`bootstrap_unix.go:10-16`、`bootstrap_windows.go:11-19`、`bootstrap.go:60-110`；
  - `internal/config/load.go:1-80`、`settings_registry.go:300-400`（动态描述符、`modelIdentifierPattern`、`MatchConfigKey`）、`providers.go:200-260`；
  - `internal/cli/config.go:150-195`（`writableConfigKey`、`activeConfigPath`，`WriteConfigValue`/`UnsetConfigValue` 的唯一调用方）；
  - `internal/chat/chat.go:49,186-190,279,543`、`chat_input.go:200-240`、`layout.go:77`；
  - `internal/wiring/runtime_build.go:89-92,187-192`、`runtime.go:648,813`、`context.go:314`；`internal/hooks/hooks.go:209`；
  - 引用核对：`docs/usage/gofer-personal-agent.md:124-125`、`settings_registry.go:227,250`、`internal/personal/store/types.go:57,144`。
- 实测（一次性探针，放在 `tmp/review/suga-config-keys/probe`，用 suga 的 go.sum 离线构建，跑完已删除，仓库无改动），gookit/config v2.2.9 + goccy/go-yaml v1.19.2：
  1. `hooks: {<<: {command: [evil]}}`（**内联合并键，无锚点/别名**）：gkconfig `Get("hooks.command")` = `[evil]`，生效；goccy AST 能解析，`ast.FilterFile(ast.MergeKeyType)` 计数 1，不触发 `parseConfigDocument` 的锚点/别名拒绝。
  2. `tools: {"approval_rules.enabled": false}`（**中间层**字面量点号键）：`Get("tools.approval_rules.enabled")` = nil，**不生效**。
  3. `providers.x.models.gpt-4.1.base_url`：gkconfig 读 `Get("providers")` 得到 `map[x:map[models:map[gpt-4.1:…]]]`，带点模型名正常生效。
  4. 两文档文件（`---` 分隔）：gkconfig 取第一文档且生效；AST `len(Docs)=2`，`parseConfigDocument` 会拒绝（yaml_edit.go:203）。
  5. 重复顶层键：gkconfig 加载报错，suga 无法启动（不构成漏报路径）。
- 方法：逐项核对 F16–F24；按任务要求把 §3 的比较、快照、锁、推进、入口表与验证要求逐条落到代码挂点；同类穷尽；对全部 `Writable:true` 静态键做了一次与设计文本的机械对照（全部已归组）。

## 验收标准

- 核心目标见第 43 行；用户可见结果第 60–63 行；§3 自述性质第 137–145 行、第 131 行。
- review-contract：只有 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 可标 CORE_BLOCKING，须给 necessity proof；同类穷尽，后续轮次再现标“上轮未穷尽”。
- SR1301；GR108–GR110；AGENTS.md 验证规范。

## F16–F24 关闭核对

| 上轮 | 结果 | 设计位置 | 证据 / 剩余 |
|---|---|---|---|
| F16（CORE_BLOCKING） | **已关闭** | 第 138、131、176、205、207 行 | 键集写定为“快照键 ∪ 当前 protected 键（含动态 provider 键），缺失一侧按未设置”；无快照时 set/unset 不建快照；用例含“新增与删除 provider 被报出”。可实现性：当前键可从 goccy AST 的 `providers` 映射逐 `<id>`、逐 `models.<name>` 枚举 `base_url`/`type`（与运行时 `Get("providers")` 整表读入同源，providers.go:214-220）；键名作为不透明字符串存放，带点模型名不需拆分。升级新增键不误报（第 142 行）。 |
| F17 | 部分关闭 | 第 139–140 行 | 取值来源与回退已写；但回退触发条件把“中间层字面量点号键”也算进去，与合法的带点模型名冲突，见 F25；内联合并键、多文档、解析失败的处理与测试未写全，见 F27 |
| F18 | 部分关闭 | 第 131、144–145 行 | 推进写启动摘要、单键更新在 `editConfigFile` 临界区内且不重复取锁，已写。可行性：`WriteConfigValue`/`UnsetConfigValue` 的唯一调用方是 `internal/cli/config.go:191-193`，经 `ConfigWriteOptions` 增一个写后回调即可在锁内完成；锁为阻塞 flock/LockFileEx、不可重入，启动比较、chat 推进、set 三处各取一次，无嵌套；`home.lock` 只在 bootstrap 中使用，与配置锁无交叉持有，无死锁。剩余：整文件模式下的单键更新未定义（F26）；上轮第 3 点“比较与生效值读同一份字节”与“比较后横幅前改动”的用例未落地（F29） |
| F19 | 已关闭 | 第 117、196 行 | 改为不保护并把裁决回退写入风险，与 runtime_build.go:89-92 一致 |
| F20 | 已关闭 | 第 114–122 行 | 机械对照：全部 `Writable:true` 静态键均已点名或被 `{…}`/`*` 组覆盖 |
| F21 | 已关闭 | 第 150–156、166、205、208 行 | 回调经 `chat.Options`、横幅走 `BannerInfo`；ACP 按会话去重；compact 不参与；缓存按路径并可重置；CLI 反向验证 |
| F22 | 已关闭（附 LOW） | 第 139 行 | 规范化为排序 JSON；“排序”的对象未写清，并入 F27 |
| F23 | 已关闭 | 第 97、199 行 | `gofer-personal-agent.md:124-125` 核对无误 |
| F24 | 已关闭（附 LOW） | 第 143、195 行 | 路径规范化与风险已写；与锁路径的规范化不一致见 F28；风险表述偏窄见 F30 |

## 发现

### F25 [HIGH] 整文件回退把“中间层字面量点号键”列为触发条件，与合法的带点模型名冲突：多数真实配置会常驻整文件模式

- 级别与处置：HIGH，`CORE_CORRECTIVE`。
- 证据：
  - 第 140 行：“顶层/中间层的字面量点号键（如 `"tools.approval": auto`）等无法逐键读取的写法时，改为对整个文件求一个摘要”。
  - 模型名允许点号：`modelIdentifierPattern = ^[A-Za-z0-9_@:/.-]+$`（settings_registry.go:306）；`configYAMLPath` 专门为带点模型名生成 `providers/<id>/models/<name>` 段（yaml_edit.go:254-266）；用法文档举例 `gpt-4.1`（docs/usage/llm-config-v0.md:38）。`providers.x.models.gpt-4.1:` 这类中间层带点键是正常写法，人用 `suga config set providers.x.models.gpt-4.1.base_url …` 也会写出它。
  - 实测：中间层带点键对 gkconfig **不生效**（探针 2）；gkconfig 只在顶层查字面量键（gookit/config read.go:163）。所以只有顶层带点键会造成“AST 逐键读到的值 ≠ 生效值”，中间层不需要回退。
  - 按第 140 行字面实现，配置里只要有一个带点模型名就进入整文件模式，后果：
    1. 横幅只能显示“配置文件有变化”，不再列键名，与第 63 行用户可见结果不符；
    2. 任何改动（包括你自己 `config set model …`、改注释、改缩进）都会触发提示，与第 139 行“注释与缩进变化不算改动”矛盾，提示失去区分度；
    3. 人工 set 的单键更新在整文件模式下没有定义（见 F26），这个歧义从边缘情形变成常见情形。
  - 第 207 行的用例“注释与缩进变化不报”用不带点的样例也能通过，测试发现不了。
- 为什么不是 CORE_BLOCKING：整文件模式只会多报，不会漏报，第 43 行“你下次打开 chat 能看到”仍成立。
- 建议：第 140 行改为只检测**顶层**带点键（运行时唯一会读到的位置；注册表的顶层键都不含点号），并写明中间层带点键（含 `providers.<id>.models.<name>`）按普通键名处理；第 207 行加一条用例：含 `gpt-4.1` 这类模型名的配置仍走逐键比较、只报被改键。

### F26 [MEDIUM] 整文件模式下快照存什么、两种模式之间怎么切换、人工 set 的“只更新该键”怎么做，都没有写定

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`。
- 证据：
  - 第 202 行快照内容只写“键名 → 摘要”；第 140 行的整文件摘要存在哪里、逐键摘要是否同时保留，没有写。
  - **模式切换**：上次推进时是逐键模式、这次变成整文件模式（或反过来）时，快照里没有当前模式需要的那份摘要。第 138 行“缺失按未设置”是针对键说的，整文件摘要不是键；第 143 行“首次运行只建不提示”只针对快照文件不存在。实现者可能把“缺整文件摘要”当成首次运行，静默补上。
  - **人工 set**：顶层带点键和内联合并键都能通过 `parseConfigDocument`（探针 1；yaml_edit.go:205 只拒绝锚点/别名），所以整文件模式下 `suga config set` 照常成功。此时第 131 行的两句话互相冲突：“用本次写入的值计算”在整文件模式下就是重算整文件摘要；“不会吸收此前未展示的改动”则要求不能这么做。
  - 漏报链（只在实现者按前一句做时出现）：模型写入顶层 `"hooks.command": [evil]`（生效，探针同类）→ 进入整文件模式 → 你在终端 `suga config set tools.read_roots …`（第 61 行的正常操作）→ 快照整文件摘要按写后文件重算 → 下次 chat 不提示。
- 为什么不是 CORE_BLOCKING：第 131 行已把“不吸收此前未展示的改动”写成必须满足的性质，照着这条实现不会选择重算整文件摘要；这里是表述歧义，不是唯一实现就出错。考虑到 F25 会让整文件模式变常见，应在计划前写清。
- 建议：§3 补三句。
  1. 快照始终同时存整文件摘要和（能逐键读取时的）逐键摘要；比较按当前模式取对应的那一份，缺失按“已改动”计；
  2. 整文件模式下人工 set/unset 不改快照（下次 chat 照常提示一次）；
  3. 第 207 行加用例：整文件模式下人工 set 后，下次启动仍提示；从逐键模式切到整文件模式时提示。

### F27 [LOW] 整文件回退的触发条件需写全并配测试；“排序后的 JSON”中排序的对象没写清

- 级别与处置：LOW，`CORE_CORRECTIVE`（同类穷尽：凡是 AST 逐键读取与 gkconfig 生效值可能不一致的写法）。
- 证据：
  - **内联合并键**：`<<: {command: [evil]}` 没有锚点/别名，`parseConfigDocument` 不拒绝；按 AST 逐键读 `hooks.command` 是“未设置”，gkconfig 生效值是 `[evil]`（探针 1）。第 140 行列了“合并键”，但第 207 行用例只有“锚点或字面量点号键”。如果实现只靠 `parseConfigDocument` 报错来触发回退，就会漏掉这一种。检测方法很直接：`ast.FilterFile(ast.MergeKeyType, file)`。
  - **多文档、其他解析失败**：多文档文件 gkconfig 取第一文档生效（探针 4），`parseConfigDocument` 会拒绝（yaml_edit.go:203）。第 140 行只用了“等”，没写解析失败一律回退整文件。如果实现成“解析失败就跳过比较”，比较就失效了。
  - **“排序”**：第 139 行“序列化为排序后的 JSON”。如果把列表元素也排序，`hooks.command`、`memory.*.args` 里参数重新排列就不会被报出，而参数顺序会改变实际执行的命令。
- 建议：第 140 行写明触发条件为“锚点、别名、合并键（含内联 `<<`）、顶层带点键，以及 `parseConfigDocument` 拒绝的任何情况（多文档等）”；第 139 行写明“映射键排序，列表保持原顺序”；第 207 行补内联合并键与多文档两条用例。

### F28 [LOW] 快照路径与配置锁路径的规范化不一致

- 级别与处置：LOW，`CORE_CORRECTIVE`。
- 证据：
  - 第 143 行：快照文件名用“绝对路径 + `filepath.Clean` + Windows 小写”后的哈希。
  - 配置锁用 `sha256([]byte(path))`，对路径没有做 Clean，也没有转小写（yaml_edit.go:109-110）。`editConfigFile` 传入的是 `filepath.Abs` 之后的路径（yaml_edit.go:130），而 `Load` 直接用原始的 `os.Getenv("SUGA_CONFIG")`（load.go:51-52）。
  - 第 144 行“启动时的读、比较与 chat 推进各自取该配置文件的配置锁”，没写取锁时用哪种路径形式。如果启动时用原始路径取锁，就会和 set 拿到不同的锁文件；在 Windows 上，大小写不同的两个进程也会拿到不同的锁，却写同一份快照。
  - 后果只会是丢失更新，结果是多报，不会吸收改动，所以定为 LOW。
- 建议：第 144 行写明三处都用 `configLockPath(filepath.Abs(path))`，与 `editConfigFile` 取同一把锁；或者让锁名与快照名共用同一个规范化函数。

### F29 [LOW] F18 的剩余部分：比较读的文件与 gkconfig 生效的不是同一份字节；“比较后、横幅前改动”的用例没有写进验证要求

- 级别与处置：LOW，`CORE_CORRECTIVE`（上轮 F18 第 3 点与建议用例未处理）。
- 证据：
  - `Load` 不持锁读一次文件并生效（load.go:51-60）；按第 144 行，比较时持锁再读一次。两次读之间如果文件被改回快照状态，本会话实际用的是被改过的配置，却不会提示。窗口很小，需要并发进程刻意配合。
  - 上轮 F18 建议的用例“比较之后、横幅之前修改另一个 protected 键，下次启动仍然报出”，第 207 行没有写进去。第 145 行的性质因此没有测试守住。
- 建议：第 145 行或风险清单写一句“比较读取与生效读取之间的改动，下次启动才会报出”（或者让比较复用 `Load` 读到的字节）；第 207 行补上述用例。

### F30 [LOW] 风险清单同类缺口：chat 不需要终端；`.env` 可以直接注入持久指令

- 级别与处置：LOW，`CORE_CORRECTIVE`（同类穷尽，只是如实记录已接受的风险，不改变 D9/D10 的选择）。
- 证据：
  - **chat 不需要终端。** 第 195 行写的是“模型可在伪终端里自起 `suga chat`”。但 chat 支持非终端输入：stdin 不是终端时不进入 raw 模式，照常逐行读（chat_input.go:226 只在终端时设 makeRaw）。所以 `shell_exec` 里一句 `echo /exit | suga chat` 就能打出横幅并推进快照，不需要 pty。危害与第 192 行“删除快照”同级，只是现在的表述低估了门槛。
  - **`.env` 注入持久指令。** `SUGA_APPEND_SYSTEM_PROMPT` 从进程环境读取（runtime_build.go:187-192），每个会话都会用到（runtime.go:648,813；context.go:314）；`Load` 会把 `SUGA_HOME/.env` 载入进程环境（load.go:46）。在 `.env` 里写这一行，就是一条持久注入的指令，与 `rules.file`（第 108 行，列为 protected，理由是持久注入的指令）同类。但第 193 行的风险只提到“`${VAR}` 引用经 `.env` 间接改值”，没有提这种直接方式。
- 建议：
  - 第 195 行改为“模型可直接（管道或伪终端）自起 `suga chat`……”。如果愿意，推进时可以复用第 128 行的判定（`SUGA_SESSION_ID` 非空或 stdin 非终端时不推进）。这是一个可选的低成本收紧，不要求本轮采纳。
  - 第 193 行补“`.env` 中直接设置的 `SUGA_APPEND_SYSTEM_PROMPT` 等环境变量不被比较（与 `rules.file` 同类的持久指令注入）”。

### SR1301 核对

- 鉴权：已核对。CLI 判定只挡直接调用，已如实写明（第 132、191 行）；推进门槛的表述见 F30。
- 注入防护：已核对。键名精确匹配，不拼接命令；快照文件名用哈希，不拼接用户输入的路径。
- 秘密：已核对。快照只存摘要，提示与 trace 只含键名；整文件摘要也只是哈希；`run/protected-config/` 目录 0700、文件 0600。
- 入参校验：已核对，沿用现有校验。
- 资源归属：不适用。

## 覆盖缺口与限制

- 没有运行项目测试和 ptye2e。gookit/config 与 goccy 的行为只用一次性探针验证（见“输入与方法”），探针已删除。
- Windows 上锁与大小写的行为是读代码推断的，没有在主机上实际跑。
- 本轮是增量复核：读了 0.4→0.5 的 diff，以及 §3、架构、关键流程、风险、验证、决策各节；分类表做了机械对照。§1 分类原则与 §2 判定沿用上轮结论。

## 结论

**PASS（Spec/Executability 轴，本轮无开放 CORE_BLOCKING）。**
- 上轮的 CORE_BLOCKING F16 已关闭：键集取并集，动态 provider 键可以从 AST 枚举，无快照时 set 不建快照，用例覆盖新增和删除 provider。
- F19–F21、F23 已关闭；F17、F18、F22、F24 的剩余部分转为本轮 finding。
- 本轮新增 F25–F30，都是 `CORE_CORRECTIVE`，应在计划批准前修正。其中 F25（HIGH）与 F26（MEDIUM）优先：F25 让常见配置常驻整文件模式；F26 在整文件模式下要依靠第 131 行的“不吸收”性质，才能排除漏报那种实现。
- 按 review-candidate-loop，本轮全 PASS 且含 CORE_CORRECTIVE，修订后可做唯一一轮纠正确认轮（candidate 须变化）。

## 剩余风险与后续

- 修订 F25–F30 后，纠正确认轮只需复核 §3 第 138–145 行、第 131 行、风险清单与第 207 行。
- 已接受的风险不变：刻意绕过 CLI 判定；改写或删除快照；`.env`/环境变量间接改值；只用 ACP/run/SDK 的用户提示会一直出现，直到打开 chat。
