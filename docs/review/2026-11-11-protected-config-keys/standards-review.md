<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 0.7 评审（Standards/Governance 轴，第 6 轮：纠正确认轮）

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md`
- 候选四元组：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `6ea0178466b92313c7ef3dc2d7b8649a56100902`；subject 同上；revision `0.7`（上轮候选 `1832e465` / 0.6，两轴均 PASS 附 `CORE_CORRECTIVE`）
- 修订版本：Draft 0.7
- 评审者：Standards/Governance 轴独立子 agent（未参与本设计起草或修订）
- 评审轴：Standards/Governance；第 6 轮，PASS 后唯一一轮纠正确认轮；`REPORT_ONLY`
- 日期：2026-11-11（容器时间）
- 排除项：不做全量重评；D3–D10 本身不评；未变章节沿用第 5 轮结论；Spec/Executability 细节只在核对 F31–F35 是否落实时涉及。

## 输入与方法

- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（全文，含 review-contract、review-candidate-loop、planning-core-rigorous、review 模板 1.2.0、SR/GR 条目）；`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（全文，design-contract、design 模板 1.2.0）。
- 上轮报告：`git show 6ea01784:docs/review/2026-11-11-protected-config-keys/standards-review.md`（S21、S22）与 `spec-review.md`（F31–F35）。
- 候选：`git diff 1832e465..6ea01784 -- <subject>`；0.7 全文中与修订相关的段落（第 16–50、80、94、115–135、143–162、174–184、191–198、208–228、238–247 行，下文行号均指 0.7）；全文 grep `可写|pre_tool_use|whole_file|整文件|键名列表|serpapi|秘密` 做一致性扫描。
- 代码（只读）：`internal/config/settings_registry.go`（非 `Writable:true` 描述符：`:118,121,230,231,243,252,253,313,314`）、`internal/config/yaml_edit.go:102-128`（`configLockPath`）、`internal/config/load.go:30-75`；`Load` 调用方 `cmd/suga/main.go:38,52`、`pkg/sugasdk/engine.go:157`。
- 方法：逐项核对 S21、S22、F31–F35；对 0.7 新增文字做 scope necessity → internal validity；按“0.7 新增表述与其他段落是否一致”做同类穷尽。

## 验收标准

design-contract 必需内容与质量检查；SR1103；SR1301；SR1403/SR1410；GR108–GR110；review-contract 的 severity/disposition、`CORE_BLOCKING` 五类与 necessity proof、同类穷尽；planning-core-rigorous 纠正确认轮规则。

## S21、S22、F31–F35 修订核对

| ID | 状态 | 证据 |
|---|---|---|
| S21 | 关闭 | 第 215 行补“`.env` 中 `SUGA_HOOKS=off`（关闭全部 hook，含执行前拒绝门）”；第 222 行 README/usage 写明限制（泛述，可覆盖）。 |
| S22 | 关闭 | D6（第 239 行）改为“覆盖全部注册表键（含不可写与秘密键）”；`network.proxy.sites` 理由（第 130 行）改为“代理地址本身受比较保护”；名词（第 80 行）与数据（第 223 行）补整文件摘要。第 38、42、247 行为历史处理记录/选项原话，按上轮意见保留。 |
| F31 | 关闭（附 S23 一处） | 第 133 行新增“其他秘密键”组，`serpapi_key` 有归属；与注册表非可写键核对：5 个秘密键中两个代理地址在 protected 表，其余 3 个在该组，无遗漏。但 `providers.<id>.api_key/headers` 同时落入第 135 行“provider 调参”组，见 S23。 |
| F32 | 主体关闭（残留见 S24） | 快照格式写定（第 161 行，含 `mode` 字段）；第 146、195 行补“整文件模式下不改快照”；trace attrs 写定 `{"whole_file":true}`（第 174 行）；空文件按全部未设置（第 161 行）。残留：set 路径“整文件模式”指哪一侧未写；整文件模式下逐键摘要“每次都写”的取值来源未写，见 S24。 |
| F33 | 关闭 | 采用方案②：第 119 行“新增不可写描述符，只参与比较”；第 240 行 Gate 项改为“只参与比较（不可写）”；第 228 行验证补“不可经 config set 写”；修订记录第 16、47 行。全文扫描：除第 42 行第 4 轮历史行外，无“可写 `pre_tool_use`”残留；第 115 行“不可写键也可标 Protected”、第 143 行“检查放在可写键判定之后”、第 191 行“只读键拒绝（现状）”与此一致（不可写键在现有只读判定处即被拒，不进 protected 分支）。第 89 行“不改非 protected 键的可写性”不受影响。新增 CLI 能力已消除，接口清单（第 94 行）无需补 set 项。 |
| F34 | 关闭 | 第 215 行补 `SUGA_HOOKS=off` 与未配 `network.proxy` 时的 `HTTP(S)_PROXY`。 |
| F35 | 关闭 | 第 183 行架构补 `internal/config/load.go`（持锁读字节，经 `LoadSources` 加载并保留供比较）；第 184 行补 `wiring.Options` 新字段；第 216 行把锁文件名变化的升级过渡列入风险。`Load` 的三个调用方都在进程启动处且读后即释放锁，与 `editConfigFile` 取锁顺序不交叠，不引入锁重入。 |

## 0.7 修订治理核对

- **SR1103**：0.7 含语义变化（`hooks.pre_tool_use.*` 可写性、快照数据模型、分组），递增正确；摘要（第 16 行）写明按类自查范围（快照数据模型、未归组键、`.env` 风险、改动点清单），满足 review-contract“作者修订摘要写明按类自查的范围”。
- **scope necessity**：0.7 没有新增机制。`mode` 字段是关闭 F32 歧义所需的最小数据项；`load.go` 改动是 0.6 已决定的“同一份字节”约束的落点（F35），不是新机制；`pre_tool_use` 改不可写是收窄。
- **授权**：`pre_tool_use` 从 0.6 的“可写+protected”收窄为“只参与比较”，0.6 未获批准，D6 Gate 项已同步（第 240 行），授权边界披露完整。
- **SR1301**：鉴权——已核对，判定不变；注入防护——已核对，不适用新增文字；秘密——已核对，快照格式只含摘要、trace 整文件模式只有布尔标记，不含值；入参校验——已核对，`pre_tool_use` 不可写后不再经 CLI 接收入参；资源归属——不适用。
- **GR108**：`wiring.Options` 只增关闭比较的字段，判断仍在 config（第 184 行），成立。GR109、GR110、SR2004 相关段落未变，沿用上轮结论。
- **代码组织**：架构节（第 179–185 行）已覆盖本轮补入的 `load.go`、`wiring.Options`，无结构违规。

## 发现

### S23 [LOW] 0.7 新增表述与其他段落有两处不一致

- disposition：`CORE_CORRECTIVE`
- 证据（同类穷尽：0.7 新增或改写的表述与设计其他段落冲突的全部出现点）：
  1. 第 174 行 trace 改为“attrs 只含键名列表，整文件模式下为 `{"whole_file":true}`”，但第 94 行“新增接口（只增不改）”仍写“attrs 只含键名列表”。新增接口的声明处与实现处不一致，计划作者按第 94 行写接口文档或测试会漏掉 `whole_file`。
  2. 第 133 行“其他秘密键”组列入 `providers.<id>.api_key`、`providers.<id>.headers`，第 135 行“provider 调参：`providers.<id>` 与模型级下除 `base_url`、`type` 外的键”同样包含这两个键。第 139 行分类表要求每个键给出“所属组”，两组重叠时归属不唯一。
- 其余扫描结果：第 42 行（第 4 轮历史行“与 post 同为可写+protected”）、第 38 行、第 247 行是历史记录或选项原话，第 47 行已注明第 5 轮变更，不算冲突；第 148 行“与其他 protected 键一致”指 D5 的两组键，不受 `pre_tool_use` 可写性影响。
- 影响：只影响新增接口说明与分类表测试的唯一性，不影响核心验收。
- 建议：第 94 行改为“attrs 为键名列表，整文件模式下为 `{"whole_file":true}`”；第 135 行改为“除 `base_url`、`type` 及秘密键外的键”，或把 `api_key/headers` 从第 133 行移出，二选一。

### S24 [LOW] F32 两处残留未写定

- disposition：`CORE_CORRECTIVE`（F32 同类，上轮已识别，本轮部分采纳；不另立 disposition）
- 证据：
  1. 第 146、157、195 行“整文件模式下（人工 set/unset）不改快照”仍未写明“整文件模式”指当前文件、快照 `mode`，还是任一侧。F32 建议为“当前文件或快照处于整文件模式时不改快照”。
  2. 第 161 行“逐键与整文件摘要每次都写”：整文件模式由“无法可靠逐键读取”触发（第 156 行），此时逐键摘要取自哪种读法未写。按第 157 行，任一侧为整文件模式时只比较整文件摘要，所以 `mode=file` 快照里的逐键摘要不会被比较使用。
- 影响：按 Spec 轴第 5 轮的推演，两种理解都不会漏报，只是实现不唯一、可能写出永远用不到的数据。
- 建议：第 146/157 行写“当前文件或快照 `mode` 为 `file` 时不改快照”；第 161 行写“`mode=file` 时 `keys` 可为空（不参与比较）”。

## 覆盖缺口与限制

- 只读 diff 与依赖段落，未重新全量盘点配置键（沿用第 5 轮盘点结论）；未运行代码或测试。
- `Load` 持锁读取的失败处理（取锁超时或失败时 `Load` 是否报错）设计未写，属于计划阶段的实现细节，未立 finding；`configLockPath` 在建目录失败时已有回退路径（`yaml_edit.go:121-124`）。
- Spec/Executability 轴对 F31–F35 的可执行性另行结论，本报告对 F32 的判断只涉及表述完整性。

## 结论

`PASS`：S21、S22 已关闭；F31、F33、F34、F35 已关闭，F32 主体已关闭。0.7 把 `hooks.pre_tool_use.*` 改为不可写、只参与比较后，第 115、119、143、191、228、240 行表述一致，Gate 项已同步。没有引入新机制，也没有新增 `CORE_BLOCKING`。本轮新增 S23、S24，均为 `LOW` / `CORE_CORRECTIVE`。

## 剩余风险与后续

- 本轮是唯一一轮纠正确认轮，不再开新轮。S23、S24 若在 Gate 前修订，属“未复核修订”，Gate 上须按 planning-core-rigorous 披露修订摘要；也可留给计划阶段写定（建议至少改第 94 行接口声明）。
- Gate 上需你确认的评审补入键见第 240 行，其中 `hooks.pre_tool_use.tools/name`、`network.proxy.url`、`tools.web_fetch.proxy` 只参与比较、不可写。
- 已知并已接受的限制不变（第 210–218 行），含 `.env` 的 `SUGA_APPEND_SYSTEM_PROMPT`、`SUGA_HOOKS=off`、`HTTP(S)_PROXY`，以及锁文件名变化的短暂过渡期。
