<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 R6 评审：Spec/Executability 轴

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md` 第 5 轮纠正项（F31–F35、S21、S22）是否已正确修订且可执行；0.7 修订是否引入新的 CORE_BLOCKING 或内部矛盾
- 候选四元组：git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `6ea0178466b92313c7ef3dc2d7b8649a56100902`；subject 为上述设计；revision Draft 0.7（上轮 `1832e465` / 0.6，两轴均 PASS）
- 修订版本：Draft 0.7
- 评审者：Spec/Executability 轴独立子 agent，第 6 轮（纠正确认轮），REPORT_ONLY
- 日期：2026-11-11
- 排除项：不做全量重评；D3–D10 用户选择本身不评；Standards/Governance 轴另行评审；不改仓库、不提交

## 输入与方法

- 规范：payload `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（review-contract、review-candidate-loop、planning-core-rigorous、rules-*）与 delta `tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（design-contract、design 模板），均完整读取。
- 上轮报告：`git show 983807dd:docs/review/2026-11-11-protected-config-keys/spec-review.md`（F31–F35）与 `standards-review.md`（S21、S22）。
- 增量：`git diff 1832e465..6ea01784 -- <subject>`；依赖章节按 0.7 重读 §1 分类表与“不保护”分组、§2、§3 全节、架构、关键流程、风险与验证、D6（下文“第 N 行”指 0.7）。
- 读过的代码（工作树与候选一致）：
  - `internal/config/load.go:33-67`（`Load`：`.env` → `LoadFiles(SUGA_CONFIG)` 或 `LoadExists(SUGA_HOME/config.yaml)` → `validateLoadedSettings`）；
  - `internal/config/config_candidate.go:26-53`（`ValidateConfigCandidate` 用 `LoadData`，不经 `Load`；`Reload` = `Reset` + `Load`）；`internal/cli/config.go:153-200`（`writableConfigKey`、`editConfig` 写后 `Reload`）；
  - `internal/config/yaml_edit.go:102-244`（`configLockPath` 会 `MkdirAll(SUGA_HOME/run/locks)`；`editConfigFile`；`parseConfigDocument` 拒绝多文档、空文档、锚点/别名）；`internal/config/bootstrap_lock.go:12-35`（`O_CREATE` 建锁文件）；
  - `cmd/suga/main.go:30-60,106-123`（只读 inspect 路径调用 `Load`，注释约定“nothing is written under SUGA_HOME”）；`internal/cli/rules_inspect_test.go:174-189`；`pkg/sugasdk/engine.go:150-160`；
  - `internal/config/settings_registry.go:37-47,118-122,225-279,313-314,361-380`；`settings_registry_source.go:190-201`（`configFileName` 依赖 `LoadedFiles()`）；`internal/config/settings_registry_test.go:40-90`；
  - `internal/wiring/runtime.go:29-70,235-247,380-392` 与全部 `wiring.Open*` 调用方；
  - 依赖源码 `gookit/config v2.2.9`：`load.go:29-57,293-320,497-570`（`loadFile` 读字节后走与 `LoadSources` 相同的 `parseSourceCode`，并记 `loadedFiles`）、`read.go:299,628`（`ParseEnv` 在读取时展开）。
- 方法：逐项核对上轮 7 项；对 0.7 新增文字（快照 JSON、set 例外、trace attrs、空文件、`load.go` 改动、`wiring.Options`、不可写描述符）逐条推演可实现性与对现有流程的影响；同类穷尽。

## 验收标准

- 设计核心目标、§3 比较/推进规则、§2 人工 set 规则、验证要求（第 222–231 行）。
- review-contract：CORE_BLOCKING 只限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 且需 necessity proof；同类穷尽，再现标“上轮未穷尽”。
- SR1301；GR108–GR110。

## 上轮项关闭核对

| 上轮 | 结果 | 设计位置 | 证据 / 剩余 |
|---|---|---|---|
| F31 | **已关闭** | 第 133、239 行 | “不保护”新增“其他秘密键：`serpapi_key`、`providers.<id>.api_key/headers`”；D6 改为“覆盖全部注册表键（含不可写与秘密键）”。非 `Writable:true` 的 9 个注册表键现在都有归属。 |
| F32 | **主体关闭；一处上轮未穷尽，见 F37** | 第 79、146、157、164、174、195、220、228 行 | 名词、数据行、快照 JSON、trace attrs、空文件均已写定。剩余：单键更新对 `mode`/`file` 字段的处理、“整文件模式”指哪一侧仍未写，加上 0.7 新增的“两种摘要每次都写”，可读出一种会吸收未展示改动的实现。 |
| F33 | **已关闭** | 第 47、119、228、240 行 | 改为只标 protected、不可写（选项②）。核对代码：`writableConfigKey`（`cli/config.go:162`）对非 Writable 返回 “read-only”，在 protected 检查之前，不新增 CLI 能力。 |
| F34 / S21 | **已关闭** | 第 215 行 | 风险补 `.env` 中 `SUGA_HOOKS=off`（含执行前拒绝门）与未配 `network.proxy` 时的 `HTTP(S)_PROXY`。与 S21 的同类穷尽结论一致。 |
| F35 | **已关闭（附 F36）** | 第 183、184 行 | 架构补 `load.go` 与 `wiring.Options`。`Options` 改法可唯一实现；`load.go` 的“持配置锁”与现有只读路径冲突，见 F36。 |
| S22 | **已关闭** | 第 79、130、220、239 行 | D6、`network.proxy.sites` 理由（“只选择哪些主机走已配置的代理，代理地址本身受比较保护”）、名词与数据行都已同步。第 38、42、247 行是历史记录，按原话保留。 |

## 任务指定项复核

- **快照 JSON 模型**（第 164 行）：`{"version":1,"mode":"keys|file","keys":{…},"file":"…"}` 可唯一序列化。整文件模式下逐键摘要也算得出：取值定义为 goccy 解码后、未展开 `${VAR}` 的值（第 155 行），锚点/别名/合并键在解码时已展开，多文档按 gkconfig 同样只取首文档。这些逐键摘要在 `mode=file` 时不参与比较（任一侧为整文件即按整文件比，第 157 行），写入无害。“快照缺整文件摘要视为有变化”与“每次都写”并存，只覆盖损坏/手工改写，不矛盾。
- **整文件模式下人工 set 不改快照**（第 146、157、195 行）：可达性有限。`editConfigFile` 经 `parseConfigDocument` 已拒绝多文档、锚点、别名（`yaml_edit.go:203-208`），因此真能写成功的整文件触发只剩顶层点号键（合并键取决于 `yamlKey` 能否解析 `<<`）。规则本身无漏报，但单键更新对 `mode`/`file` 两个字段的处理没写，见 F37。
- **trace attrs**（第 174 行）：整文件模式 `{"whole_file":true}` 已定；逐键模式“只含键名列表”没给字段名，计划可自取（如 `keys`），不影响唯一性，并入 F37 建议。
- **空文件处理**（第 164 行）：可实现。goccy `Unmarshal` 对空内容或只有注释的内容返回空映射，gkconfig 正常加载；比较侧不能复用 `parseConfigDocument`（它对 `Body == nil` 报错），0.7 写明“按全部未设置”已足够消除歧义。现有行为不变：空文件上 `config set` 仍由 `parseConfigDocument` 拒绝，不在本设计范围。
- **`load.go` 持锁读字节经 `LoadSources` 加载**（第 183 行）：
  - 解析等价：`loadFile` 读出字节后调用的 `parseSourceCode` 与 `LoadSources` 是同一个函数（`gookit/config load.go:307-308,518`）；`ParseEnv` 在读取时生效（`read.go:299,628`），不受加载方式影响；`validateLoadedSettings`、`.env` 处理顺序不变。
  - `LoadedFiles()` 不再记录：唯一使用方 `configFileName`（`settings_registry_source.go:191-200`）的回退值是原始 `SUGA_CONFIG` 或 `SUGA_HOME/config.yaml`，与 `LoadFiles/LoadExists` 记下的原始路径一致；无 `ReloadFiles`/`WatchFiles` 调用方。兼容。
  - 需保持的两点（计划写明即可，不立 finding）：默认路径文件不存在时按空处理（现 `LoadExists`），`SUGA_CONFIG` 不存在时报错（现 `LoadFiles`）；格式按扩展名取（现 `loadFile` 的行为），不要写死 yaml。
  - `Reload` 只在 `editConfigFile` 释放锁之后调用（`cli/config.go:197`），`ValidateConfigCandidate` 不经 `Load`，所以锁不可重入不会造成死锁。
  - 持锁带来的副作用见 F36。
- **`wiring.Options` 新字段**（第 172、184 行）：`OpenCLI(ctx, Options)` 与 `Open(ctx, Options)` 同用一个 `Options`；`OpenCLI` 调用方只有 `run.go:187` 与 `compact.go:60`。表格里的“装配参数”与架构里的“`Options` 新增字段”是同一件事，不矛盾。
- **`hooks.pre_tool_use.*` 不可写描述符**：运行时读取不受影响（`hooks.go` 按字面键直接读，描述符无 `Reader`，不进 `validateLoadedSettings`），与 `hooks.post_tool_use.*` 同型（`settings_registry.go:278-279` 同为 StringList/String、无 Reader）。对 config 命令的影响：`config get`、`config list` 现在会列出并解析这两个键（原先是未知键），`config set/unset` 的报错从“未知键”变为“read-only”，退出码同为 2。现有测试 `settings_registry_test.go` 不枚举全部描述符，不受影响。行为变化很小，属于正常的注册表补全。

## 发现

### F36 [MEDIUM] `Load` 持配置锁会在只读 inspect 路径下创建 `SUGA_HOME`，锁失败时所有命令启动失败

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`（0.7 新增文字引入；不阻断核心验收，见下）。
- 证据：
  - 第 183 行：“`internal/config/load.go`：持配置锁读取文件字节……”。
  - `configLockPath` 无条件 `os.MkdirAll(SUGA_HOME/run/locks, 0700)`（`yaml_edit.go:121`），`acquireBootstrapLock` 以 `O_CREATE` 建锁文件（`bootstrap_lock.go:13`）。
  - `cmd/suga/main.go:33-40`：只读 inspect（`inspect manifest/info/rules …`）调用 `appconfig.Load()`，注释约定 “nothing is written under SUGA_HOME”；`rules_inspect_test.go:174-189` 断言命令运行后 `SUGA_HOME` 不存在（该测试不经 `main`，所以不会报出回归）。
  - 现状 `Load` 不写任何文件；持锁后，锁目录或锁文件无法创建时 `Load` 返回错误，所有命令启动失败，设计没有写失败处理。
  - 锁对比较没有一致性收益：`Load` 读字节与 wiring 里读快照比较是两次独立加锁（第 165 行“各自取锁”），两者之间仍可能插入一次人工 set，结果只是多报一次。防止读到写了一半的文件已经由 `atomicWriteConfig`（临时文件加改名）保证。
- 不判阻断的理由（necessity proof 不成立）：core_acceptance——启动比较与人工 set 拒绝——不依赖 `Load` 持锁；deletion_alternative = PASS（`Load` 不持锁读字节，核心验收不变）；影响落在既有只读 inspect 的“不写 home”约定，不在本设计核心验收里。
- 建议：第 183 行改为“`Load` 自己读取文件字节（不持锁，写入方已是临时文件加改名），经 `LoadSources` 加载并保留”；第 165 行的“启动时的读”相应限定为“读快照与比较”。若坚持持锁，须写明：只读 inspect 路径不加锁，锁失败时退回无锁读取并记 Warn，不让 `Load` 因此失败。

### F37 [LOW] 上轮未穷尽（F32 同类）：单键更新对快照 `mode`/`file` 字段的处理与“整文件模式”所指一侧仍未写

- 级别与处置：LOW，`CORE_CORRECTIVE`（F32 未穷尽的部分；沿用原处置）。
- 证据：
  - 第 146、157、195 行“整文件模式下不改快照”仍未说明指当前文件、快照 `mode=file`，还是两者任一（F32 原建议已点出）。
  - 第 164 行新增“逐键与整文件摘要每次都写”。如果把它理解为单键更新时也按写入后的内容重算 `file`，再把“整文件模式”理解为只看快照一侧，就能推出一条漏报路径：快照 `mode=keys`；模型直接在文件里加顶层点号键 `"tools.approval": auto`（当前文件因此进入整文件模式）；之后人在终端 set 任一 protected 键，快照一侧是 keys，于是按单键更新并重算 `file`；下次启动当前文件为整文件模式，按整文件比较结果相等，模型的改动不再提示。
  - 第 146 行“不重算其他键，不会吸收此前未展示的改动”已从原则上排除这种实现，所以不构成实现不唯一的 BUG；但第 228 行用例“人工 set 不吸收其他键的改动”没有要求覆盖这一组合，计划作者可能只测逐键模式。
  - 同类穷尽：凡是描述单键更新的位置（第 146、157、195 行）以及验证列表第 228 行。trace 逐键模式下 attrs 的字段名未定（第 174 行），属同一处的小缺口。
- 建议：第 157 行写定“当前文件或快照 `mode=file` 任一成立即为整文件模式，此时人工 set/unset 不改快照；否则单键更新只改 `keys[<键>]`，不改 `mode` 与 `file`（过期的 `file` 只会多报）”；第 164 行“每次都写”限定为“整体推进与首建时”；第 228 行用例补“当前文件含顶层点号键而快照为逐键模式时，人工 set 不改快照、下次仍提示”；第 174 行写明逐键模式 attrs 形如 `{"keys":[…]}`。

### SR1301 核对

- 鉴权：已核对。`hooks.pre_tool_use.*` 改为不可写后，会话内与终端都无法经 CLI 修改，只能直接编辑文件，由启动比较兜底。
- 注入防护：已核对。键名精确匹配；快照文件名用哈希；无命令拼接。
- 秘密：已核对。`serpapi_key` 等归入“其他秘密键”、不比较；代理地址只存摘要；trace 在整文件模式下只记 `whole_file`，不含值。
- 入参校验：已核对，沿用 `writableConfigKey` 与候选校验，0.7 无改动。
- 资源归属：不适用。

## 覆盖缺口与限制

- 没有运行测试或写探针；`gookit/config`、goccy 行为依据源码阅读。`yamlKey` 对合并键 `<<` 是否报错未追到底，只影响“整文件模式下人工 set”的可达范围，不影响结论。
- Windows 下改名与并发读的共享模式（`FILE_SHARE_DELETE`）未核实；现状 `Load` 本来就不持锁读，0.7 删除持锁不会比现状更差。
- 增量复核：只读了 0.6→0.7 diff 和依赖章节；§1 分类原则、§2 判定条件、各入口挂点沿用上轮结论。
- 轮次说明：上轮本轴报告把第 5 轮称为“唯一纠正确认轮”。按 ledger，第 4 轮有 S18 阻断，第 5 轮实为 blocking closure 末轮（全 PASS 含 CORE_CORRECTIVE），本轮才是合同允许的唯一纠正确认轮。轮次归属由 Standards 轴与 ledger 认定，本轴不判定。

## 结论

**PASS（Spec/Executability 轴，本轮无 CORE_BLOCKING）。**
- F31、F33、F34/S21、S22 已关闭；F35 已关闭，`load.go` 持锁的副作用另立 F36；F32 主体关闭，未穷尽部分见 F37。
- 0.7 的修订都可唯一实现，与现有 `Load`、`wiring`、config 命令流程兼容。`LoadSources` 与 `LoadFiles` 的解析路径相同，`LoadedFiles` 的唯一使用方回退值一致；不可写描述符不影响运行时读取，只让 `config get/list` 认识这两个键。
- 新增 F36（MEDIUM）、F37（LOW），均为 `CORE_CORRECTIVE`，没有开放的 `CORE_BLOCKING`，也没有新的内部矛盾。

## 剩余风险与后续

- 本轮是唯一纠正确认轮，之后不再开评审轮。F36、F37 若在计划前修订，属“未复核修订”，Gate 上需按 planning-core-rigorous 披露；也可以在计划中落实（F36 写进 `load.go` 任务，F37 写进快照任务与用例）。
- 计划需注意：`Load` 改为自己读字节后，保留“默认文件不存在按空处理、`SUGA_CONFIG` 不存在报错、格式按扩展名取”三点现有语义。
- 已接受风险不变：刻意绕过 CLI 判定、改写或删除快照、`echo /exit | suga chat` 推进快照、`.env` 直接或间接改变行为（含 `SUGA_HOOKS`、`HTTP(S)_PROXY`）、锁文件名变化的升级过渡。
