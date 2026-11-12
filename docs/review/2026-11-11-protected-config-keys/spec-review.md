<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 R3 评审：Spec/Executability 轴

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md` 的可执行性、是否有歧义、事实是否准确
- 候选四元组：git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `4a67c13c65546f3292a6408245ce39e1caf9be84`；subject 为上述设计；revision Draft 0.4（上轮 `a0845767` / 0.3）
- 修订版本：Draft 0.4
- 评审者：spec 轴独立子 agent，第 3 轮（blocking closure），REPORT_ONLY
- 日期：2026-11-11
- 排除项：用户已选定的 D3–D10 本身不评；Standards/Governance 轴另行评审；不改仓库、不提交

## 输入与方法

- 规范：payload `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（review-contract、review-candidate-loop、rules-*）与 delta `.../bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`；仓库 `AGENTS.md`。
- 上轮本轴报告 `docs/review/2026-11-11-protected-config-keys/spec-review.md`（F8–F15）；设计 0.4 全文（`git show 4a67c13c:<subject>`，下文“第 N 行”均指该版本）与 `git diff a0845767..4a67c13c -- <subject>`。HEAD 即 `4a67c13c`，工作树干净，代码行号按工作树。
- 读过的代码：
  - `internal/config/settings_registry.go:88-357`（全部静态与 4 张动态描述符表）；
  - `internal/config/load.go`（全文）、`yaml_edit.go:1-320`（编辑、锁路径、AST 解析）、`bootstrap_lock.go:12-35`、`providers.go:205-225,600-615`、`rules.go:19`、`mcp.go:18`、`tools_disabled.go:16`；
  - `internal/hooks/hooks.go:212-222`、`internal/wiring/chat_approver.go:111`、`capabilities.go:323`、`runtime.go:150-260,370-400`、`runtime_build.go:89-123`、`sessionenv.go:15-36`；
  - `internal/cli/config.go:15-25,150-195,278-286`、`root.go:66-71`、`cwdcheck.go:205-216`、`chat.go:55-110`、`acp.go:60-100`；
  - `internal/chat/chat.go:186-300,543`、`render.go:735-750`；`internal/personal/observe.go:85-120`；`internal/personal/backup/backup.go:428-436`；
  - `pkg/agentcore/tools/native/code_exec.go:560-566`、`pkg/agentcore/internal/proctree/proctree_unix.go:22-27`；
  - `test/ptye2e/chat_smoke_test.go:424-448`（测试环境变量处理）；`docs/usage/gofer-personal-agent.md:123-126`；`xhen/README.md:149-153`；
  - gookit/config `v2.2.9` 的 `read.go:149-190`（`GetValue`）与 `yaml/yaml.go`（goccy 解码）。
- 实测：在临时包里用 `gkconfig.LoadFiles` 加载一个含字面量点号顶层键与 YAML 合并键的 config.yaml，确认二者都生效（见 F17）；探针已删除，仓库无改动。
- 方法：逐项核对 F8–F15；把 §3 与入口表的每一步落到代码挂点；对照注册表逐键检查分类；同类穷尽。

## 验收标准

- 核心目标见设计第 34 行；用户可见结果第 51–54 行。
- review-contract：只有 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 可标 CORE_BLOCKING，须给 necessity proof；同类问题穷尽，后续轮次再现标“上轮未穷尽”。
- SR1301；GR108–GR110；AGENTS.md 验证规范（GR110、ptye2e、反向验证、Windows 主机测试）。

## F8–F15 关闭核对

| 上轮 | 结果 | 设计位置 | 证据 / 剩余 |
|---|---|---|---|
| F8（CORE_BLOCKING） | **已关闭** | 第 116、131、153 行，D9 第 196 行；测试第 182 行“人工 set 不吸收其他键的改动” | 写定“只更新被改键、不重算其他键”，并有对应测试。实现层的锁与取值来源见 F18（CORRECTIVE，不影响 F8 关闭） |
| F9 | 已关闭 | 入口表第 124–131 行；trace 第 133 行；D9 | 只有 chat 推进，自动入口不再消费提示；trace 改到回合内（observe.go:107-108 的丢弃问题已规避）。挂点细节见 F21 |
| F10 | 部分关闭 | 第 122–123、84 行 | 路径、按配置路径区分、持锁、原子写、损坏全部提示、不在 `data restore` 范围内都已写明（backup.go:432 不含 `run/`）。上轮并入 F10 的两项未处理：新增/删除 provider 条目（升级为 F16）、值的规范化（F22） |
| F11 | 已关闭 | 第 98 行 | `tools.disabled` 列入放宽类 |
| F12 | 部分关闭 | 第 93、103–107 行 | 有了分类原则与不保护分组；上轮点名的部分键仍未归组，且按第 93 行原则判不出结果，见 F20；`model` 与 `compact_model` 的同类不一致见 F19 |
| F13 | 已关闭 | 第 113–114、175、179–184 行 | “非空”、可注入终端判定、受影响测试（逐条 grep 核对，其余 set/unset 测试用的都是非 protected 键）、ACP/SDK/run/SUGA_CONFIG/并发用例都已补；ptye2e 的 `testEnvLanguage` 已剔除全部 `SUGA_*`（chat_smoke_test.go:435），正向用例在 agent 内也能跑。CLI 拒绝的反向验证仍未写明，并入 F21 |
| F14 | 部分关闭 | 第 79、84、86、174 行 | `local_command.go`、`backup.go` 已改对；但 `gofer-personal-agent.md` 现在两处都写 `:125`，origin_judge 实际在 `:124`，见 F23 |
| F15 | 已关闭 | 第 117 行 | 空值、`env -u`、Windows `< CON`/`CONIN$`、`NoSessionEnv` 都已列入 |

## 发现

### F16 [HIGH] 比较时取哪一组键没有写定；“只看快照里已有的键”这种实现漏报新增的 provider，改变请求去向而不提示

- 级别与处置：HIGH，`CORE_BLOCKING`，类别 `[BUG]`（实现不唯一，其中一种让核心验收失败）。上轮在 F10 的 SR1301 补充里提过“新增或删除 provider/model 条目算作变化”，当时只按误报处理、定为 CORRECTIVE；0.4 未采纳。本轮补上漏报方向的因果链，因此升级。
- 证据：
  - 第 122 行：“对每个 protected 键在 `config.yaml` 中的原始取值（…缺省视为‘未设置’）计算 SHA-256 摘要，与快照比较”。“缺省视为未设置”只说了 config.yaml 一侧，没说**快照里没有的键**怎么算。
  - 动态描述符的具体键名随 `providers` 下的条目变化（settings_registry.go:310-357；运行时按 `gkconfig.Get("providers")` 的整张表读入，providers.go:214）。新增一个 provider，就会多出快照里从没有过的 `providers.<new>.base_url`、`providers.<new>.type`。
  - 有两个现成理由会让实现者选择“快照里没有的键跳过”：
    1. 0.4 新增“人工 set 只更新该键”（第 116、153 行），但没写没有快照时 set 怎么办；如果 set 建了一个只含一个键的快照，“缺失即变化”会让下次启动报出全部已配置的 protected 键，跳过缺失键看起来就是正确做法；
    2. 以后的版本新增 protected 键时，旧快照里没有这些键，跳过缺失键能避免升级后的误报。
- 必要性证明：
  - core_acceptance：第 34 行“改变模型请求的去向……用其他方式改动这些配置时，你下次打开 chat 能看到”；第 54 行。
  - necessary_causal_chain：
    1. 模型用 shell_exec 在 `config.yaml` 里加一个 provider：`providers.x: {type: openai, base_url: <外部地址>, api_key: ${现有的 key 变量}}`，同一次编辑里改 `model: x/…`（`model` 不是 protected 键，用 `config set` 也行）；
    2. 下次启动时，当前 protected 键里有 `providers.x.base_url`、`providers.x.type`，快照里没有；
    3. 按“快照里没有就跳过”实现，改动列表为空，横幅不提示；
    4. 之后的请求连同 API key 发往外部地址。
    5. 第 182 行列出的快照用例都只改已有的键，两种实现都能通过，测试发现不了这个问题。
  - minimal_owner：设计 §3 第 122 行加一句：“比较取快照与当前两侧键名的并集，任一侧缺失按‘未设置’计；新增或删除 provider/model 条目会报出对应键”。第 123 行或第 131 行加一句：“没有快照时，set/unset 不建快照（由下次启动按首次运行建）”。第 182 行加两条用例：新增 provider 的 `base_url` 被报出；删除 provider 被报出。
  - deletion_alternative：FAIL。把端点类动态键移出清单就违背第 34 行的“请求去向”；删掉启动提示属于 D8/D9 的用户选择，不在本轴范围内。
  - scope_effect：不新增 Module、Interface、Schema 或 lifecycle，只把已有比较步骤的语义写确定。升级后新增 protected 键会提示一次，属于可接受的误报，在“风险”一节写一句即可。

### F17 [MEDIUM] “原始取值”的读取方式和 gkconfig 实际生效的值对不上：字面量点号顶层键、YAML 合并键都能生效却读不到；解析失败时的行为也没写

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`。
- 证据：
  - 运行时读取 protected 键靠 `gkconfig.Get("<带点号的键>")`，例如 `hooks.go:212`、`mcp.go:18`、`rules.go:19`、`tools_disabled.go:16`、`chat_approver.go:111`、`capabilities.go:323`。gookit/config `GetValue` 先查顶层字面量键 `c.data[key]`（read.go:163），查不到才按点号逐级找。
  - 实测：config.yaml 同时有 `tools: {approval: ask}` 和顶层 `"tools.approval": auto` 时，生效的是 `auto`；用 `&h`/`<<: *h` 合并进来的 `hooks.command` 也会生效（goccy 解码支持合并键）。
  - 设计第 122 行的“`config.yaml` 中的原始取值”，自然实现是复用 `configYAMLPath` 按段查找（yaml_edit.go:246）。这样读到的是 `tools.approval: ask`，看不到生效的 `auto`；合并进来的 `hooks.command` 则被当成“未设置”。
  - 现有 AST 解析 `parseConfigDocument` 遇到锚点或别名直接报错（yaml_edit.go:206），遇到多文档也报错（:203），而 gkconfig 能正常加载这类文件。设计只写了快照损坏怎么办，没写 **config.yaml 读不成原始值**时怎么办；如果实现成“跳过比较”，只要在文件里加一个锚点，整个提示就失效了。
- 为什么不是 CORE_BLOCKING：这几种写法都是刻意掩盖，与第 169 行已接受的“改写或删除快照”同属一类，攻击者已经可以更简单地删掉快照。只改写正常 YAML 的情况能被检测到，第 34 行核心验收在非刻意场景下成立。
- 建议（任选其一并写进 §3）：
  1. 读取时出现顶层带点号的 protected 键、锚点/别名/合并键或解析失败，一律视为“全部 protected 键已改动”，提示中注明原因（与“快照无法读取”同一处理）；
  2. 或者在第 167–172 行的风险清单里写明这几种写法与删除快照同级、不被检测。
  - 同时写明“原始值”取自哪个解析器（建议 goccy AST，与 `config set` 同一套），以及上面这些情况的测试用例。

### F18 [MEDIUM] 推进和单键更新用的是哪一份值、在哪把锁里做，没有写定；有两种实现会吸收比较之后才发生的改动，还有一种会死锁

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`。
- 证据：
  - **chat 推进的取值来源。** 入口表第 128 行写“横幅显示后整体推进”，没说推进的是启动时比较所用的那份摘要，还是推进时重新读文件算出来的。
    - 比较发生在装配阶段；横幅在 `chat.Run` 内部、会话建立之后打印（chat.go:543）。中间要启动 MCP server，最长可等 `mcp.startup_timeout_sec`（默认 30s，mcp.go:172）。
    - 如果推进时重读，这段时间里别的 suga 会话（例如并发的 gofer job）对 protected 键的修改，会在没有展示的情况下被写进快照。
  - **人工 set 的取值来源与锁。** 第 123 行“读改写持该配置文件的配置锁”，而 `editConfigFile` 写配置时已经持有同一把锁（yaml_edit.go:138）。`acquireBootstrapLock` 是不可重入的文件锁（bootstrap_lock.go:12-21）：
    - 如果在 `editConfigFile` 的锁里再取一次锁，就会死锁；
    - 如果在释放之后重读文件来算摘要，就会把这一瞬间别人对同一个键的修改记成“你设置的值”。
  - **比较与生效值读的不是同一次文件。** `Load` 不持锁读一次（load.go:52-60），比较时持锁再读一次。这只会造成多报或少报一个“正在生效的值”，属于观测误差，写一句即可。
- 建议：在 §3 写明三点。
  1. chat 推进写入的是启动比较时算出的摘要，在锁内按键合并，不重读文件；
  2. set/unset 的单键摘要用本次写入的值（`editConfigFile` 渲染出的内容）计算，在同一个临界区内完成，不重新取锁、不重读；如果确实要在释放后更新快照，就用写入时的值；
  3. 第 182 行加用例：比较之后、横幅之前修改另一个 protected 键，下次启动仍然报出。

### F19 [MEDIUM] 外部来源裁决模型的回退链最终落到 `model`，与把 `compact_model` 列为 protected 的理由同类，但 `model` 被归为不保护（上轮未穷尽）

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`（同类穷尽）。
- 证据：
  - 第 100 行把 `models.compact`、`compact_model` 列为端点类，理由是“外部来源裁决未配 `models.judge` 时回退到压缩模型”。
  - 代码里的回退链是 models.judge → models.compact → **主模型**（runtime_build.go:89-92、123）。
  - 第 106 行却把 `model` 归为不保护，理由是“只在已配置的 provider 之间选择”。这条理由对 `models.judge`、`models.compact` 同样成立，两组的划分自相矛盾。
- 为什么不是 CORE_BLOCKING：可选模型都在你自己配置的 provider 里，不改变请求去向；裁决模型变弱只削弱一道纵深防线。
- 建议：二选一。
  - 把 `model` 也列为 protected，并注明“在终端里照常可改”；
  - 或者改写第 100 行的理由，例如“`models.judge`/`compact` 只用于裁决与后台工作，改了你在会话里看不出来；`model` 改了会显示在每次 chat 横幅与 run 的 manifest 里”，再按这条理由决定 `compact_model` 是否保留。

### F20 [LOW] 按第 93 行的原则，仍有一批可写键归不了组（上轮 F12 未关闭部分）

- 级别与处置：LOW，`CORE_CORRECTIVE`（同类穷尽）。
- 证据：对照注册表的全部 `Writable:true` 静态键（settings_registry.go:88-299）与 4 张动态表（:310-357），下列键在设计里没有点名，第 107 行“其余……等”这个兜底分组也判不出它们属于哪一组，有的还会按第 93 行的“检查与上限”原则被归为 protected：
  - 外部程序的超时：`hooks.timeout_ms`、`hooks.stop_wait_sec`、`hooks.stop_background`、`memory.user.timeout_sec`、`memory.project.timeout_sec`、`mcp.call_timeout_sec`、`mcp.startup_timeout_sec`。字面上属于“执行上限”，与 `tools.shell_timeout_max` 同形；
  - 检查开关：`tools.loop_guard`；
  - 审计与留存：`transcripts`（关闭会话转录），与第 105 行的“留存与可恢复性”同类但没有列入；
  - 网络路径：`network.proxy.sites`（让指定主机走代理，与“请求去向”相关）；
  - 其他：`run.deadline_env`、`tools.shell_background_after`、`tasks.wake_on_exit`、`log.level`、`log.stderr`。
  - 其余 `chat.*`、`context.*`、`images.*`、`retry.*`、`timeouts.*`、`tools.spill.*`、`tools.deferred*`、输出字节类、`language`、`store.body_inline_bytes`、`skills.list_max`、`tools.web_fetch.user_agent`、provider 调参与 `price.*`，归入第 107 行没有歧义。
- 建议：在第 103–107 行的分组里逐个点名上面这些键。例如外部程序超时写明“程序本身由 protected 键决定，超时只影响已允许程序的运行时长”；`transcripts` 并入留存组。这样计划阶段的分类表就不用猜。

### F21 [LOW] 入口挂点还有几处没写明（F9 的剩余细节）

- 级别与处置：LOW，`CORE_CORRECTIVE`。
- 证据：
  - **chat 由谁推进。** 横幅在 `chat.Run` 内部打印（chat.go:543），cli 层不知道横幅什么时候打出来。另外已有的 `ContextNotices` 通道是在横幅**之前**打到 stderr 的（chat.go:279-281）。设计要写明：变化提示是走 `BannerInfo` 还是复用 `ContextNotices`；推进经由 `chat.Options` 新增的回调完成，回调实现放在 internal/config，chat 包不直接写快照。
  - **ACP 的“每会话首个回合”。** ACP 每个 prompt 都调一次 `wiring.Open` 新建 runtime（acp.go:84）。挂在 runtime 上的“首个回合记一条”实际会变成每个 prompt 记一条。要么写明按会话去重（例如进程内记住已经记过的 session），要么接受每个 prompt 都记一条。
  - **compact。** `suga session compact` 也走 `wiring.OpenCLI`（compact.go:60），入口表没有列出它是否比较、是否打印 stderr。
  - **进程内缓存。** 第 133 行没写缓存按什么区分（建议按配置文件绝对路径），也没写测试时怎么重置（参照 `ResetLoadedStateForTest`，load.go:27）。SDK 宿主在同一进程里换 SUGA_HOME 时会用到。
  - **CLI 拒绝的反向验证。** 第 181、183 行只要求分类表测试和 chat 横幅做反向验证；CLI 拒绝的 GR110 测试也应做一次（上轮 F13 已提）。
- 建议：在入口表与第 133 行补上以上几点，各一句即可。

### F22 [LOW] 列表、映射和整块 YAML 值算摘要之前怎么规范化，没有写（上轮 F10 未关闭部分）

- 级别与处置：LOW，`CORE_CORRECTIVE`。
- 证据：
  - `mcp.servers` 是整块 YAML（settings_registry.go:194）。`hooks.command` 等是列表，`tools.approval_rules.project_trust` 是映射。
  - 第 122 行没说摘要是对节点的原文（含注释、flow/block 风格、CRLF）算，还是对解码后重新编码的规范形式算。按原文算，你手工改个注释或换个缩进，就会收到“如果不是你改的”的提示；`config set` 改写节点风格之后也一样。
- 建议：写明“对未展开的取值做规范编码（忽略注释、风格、换行符，映射键排序）后再算摘要”。

### F23 [LOW] 引用行号仍有一处不对（上轮 F14 未关闭部分）

- 级别与处置：LOW，`CORE_CORRECTIVE`。
- 证据：第 86、174 行用 `docs/usage/gofer-personal-agent.md:125` 同时指 origin_judge 和 read_deny。实际 origin_judge 那条在 `:124`，read_deny 在 `:125`。
- 建议：改为 `:124-125`。

### F24 [LOW] 风险清单还缺两项同类情形

- 级别与处置：LOW，`CORE_CORRECTIVE`（同类穷尽）。
- 证据：
  - 模型可以在 pty 里自己起一个 `suga chat`（`script -c "suga chat"`），横幅打给它看，快照随之推进，你下次打开 chat 就不再提示。这和第 117 行的“刻意绕过”、第 169 行的“改写快照”是同一类，但风险清单没有写。
  - 快照文件名按配置文件路径的 sha256 区分（第 123 行）。Windows 上路径大小写不同、相对路径和绝对路径不同，都会得到另一份快照，而新快照按“首次运行”处理，不提示。配置锁用的是 `filepath.Abs` 之后的路径（yaml_edit.go:130），设计应写明快照同样先取绝对路径再计算。大小写问题写进风险即可。
- 建议：第 167–172 行补一句；第 123 行写明“路径先取绝对路径”。

### SR1301 核对

- 鉴权：已核对。CLI 判定只挡直接调用，已如实写明（第 117、194 行）；启动提示只在 chat 推进，自动入口无法消费提示（对模型自起 chat 的情况见 F24）。
- 注入防护：已核对。键名精确匹配（settings_registry.go:371-375），不拼接命令。
- 秘密：已核对。快照只存摘要，trace attrs 只含键名；动态键名里只有 provider id 和模型名，不含秘密。`mcp.servers` 内联的 env 值以摘要形式进入 `SUGA_HOME/run/`，模型本来就能直接读 config.yaml，没有新增暴露面。
- 入参校验：已核对，沿用现有校验。
- 资源归属：不适用。

## 覆盖缺口与限制

- 没有运行项目测试和 ptye2e；只用一次性探针验证了 gookit/config 的字面量点号键与合并键行为（F17），探针已删除。
- Windows 上 ConPTY 下 `term.IsTerminal` 为 true，是按 x/term 实现推断的，没有在主机上实际跑。
- 本轮是增量复核：读了 0.3→0.4 的 diff，以及 §1–§3、架构、流程、风险、验证、决策各节；分类表做了全量对照，其余未变章节沿用上轮结论。

## 结论

**BLOCKED。**
- 上轮的 CORE_BLOCKING F8 已关闭。
- 本轮新增 1 个 CORE_BLOCKING：F16，`[BUG]`。比较的键集合没有写定，“跳过快照里没有的键”这种实现会漏报新增的 provider，请求连同 API key 发往外部而不提示，违反第 34 行。补一句“取并集、缺失按未设置”，再写明“无快照时 set 不建快照”，加两条用例即可关闭。
- F17–F24 都是 CORE_CORRECTIVE，应在计划批准前修正。

## 剩余风险与后续

- 关闭 F16 后进入第 4 轮，只复核 §3 与验证要求这两处 changed/dependent scope。
- D7、D9、D10 已接受的风险不变：刻意绕过 CLI 判定；改写或删除快照；`${VAR}` 经 `.env` 间接改值；只用 ACP/run/SDK、从不打开 chat 的用户，提示会一直出现在 stderr 与 trace 里，直到人工 set 或删除快照。最后这一条建议在文档里写明消除提示的方法。
