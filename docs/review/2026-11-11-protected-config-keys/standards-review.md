<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 0.4 评审（Standards/Governance 轴，第 3 轮 blocking closure）

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md`
- 候选四元组：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `4a67c13c65546f3292a6408245ce39e1caf9be84`；subject `docs/design/2026-11-11-protected-config-keys-design.md`；revision `0.4`（上轮候选 `a0845767` / 0.3）
- 修订版本：Draft 0.4
- 评审者：Standards/Governance 轴独立子 agent（未参与本设计起草或修订，未参与第 1、2 轮）
- 评审轴：Standards/Governance；第 3 轮 blocking closure；`REPORT_ONLY`
- 日期：2026-11-11（容器时间；候选提交时间 2026-11-11 18:33 -0800）
- 排除项：用户已选的 Q1–Q8 本身（含 D9“只在 chat 横幅展示后推进、人工 set 只更新该键”与 D10“按原始值比较”）不评；只评其落地是否最小、可追溯、内部成立。Spec/Executability 轴的细节只在影响治理判定时提及。

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（全文 760 行，含 review-contract、review 模板 1.2.0、review-candidate-loop）、`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（全文 201 行，含 design-contract、design 模板 1.2.0）；suga `AGENTS.md`（全文）。
- 上轮本轴报告（`git show 4a67c13c:docs/review/2026-11-11-protected-config-keys/standards-review.md`，S8–S12 及 S2/S5/S7 残留，全文）。
- 候选：设计 0.4 全文（`git show 4a67c13c:<subject>`，212 行）与 `git diff a0845767..4a67c13c -- <subject>`；按 diff 涉及面，0.4 几乎每节都有改动，故全文复核。
- 代码事实核对（HEAD = 候选 `4a67c13c`，工作树干净）：`internal/config/settings_registry.go:88-357`（全部静态与动态描述符，逐键对照分类原则）、`internal/config/yaml_edit.go:102-127`、`internal/config/run.go`、`internal/personal/deadline.go:72-91`、`internal/config/network_proxy.go`、`internal/personal/observe.go:100-108`、`internal/personal/store/types.go:57-144`（trace kind 常量与说明）、`internal/personal/process/process.go:650-680`（`run/*.json` 扫描）、`internal/personal/backup/backup.go:432`、`internal/cli/cwdcheck.go:210-215`、`internal/cli/root.go:63-75`、`internal/cli/config.go:14-25`、`internal/cli/backup_restore.go:85`、`internal/cli/gc_maintenance.go:34`、`internal/cli/data_gc.go:212-219`、`internal/cli/compact.go:60`、`internal/cli/run.go:187`、`internal/cli/acp.go:84`、`pkg/sugasdk/session.go:98`、`internal/chat/chat.go:543`、`internal/chat/layout.go:76-99`、`pkg/agentcore/tools/native/local_command.go:124`；CLI 测试中所有 `config set/unset` 调用（`internal/cli/*_test.go`）与 `scripts/`、`test/` 中的调用；`docs/usage/gofer-personal-agent.md:120-128`；`git tag -l 'v0.5[5-9]*'`；`git log -3 --date=iso`。
- 方法：先逐项核对 S8 关闭证据与 S9–S12、S2/S5/S7 残留；再对 0.4 新增内容按 design-contract 与指定规则核对；设计中出现的文件:行号与键名逐一对照代码；同类问题一次列全。

## 验收标准

design-contract 必需内容与质量检查；SR1103、SR1108、SR1301、SR1403、SR1405、SR1409、SR1410；GR107、GR108、GR109、GR110；SR2003、SR2004；AGENTS.md“验证”要求；review-contract 的 severity/disposition、CORE_BLOCKING 五类与 necessity proof、新机制 scope necessity、同类穷尽。

## 上轮 S8–S12 及残留关闭核对

| ID | 状态 | 证据 |
|---|---|---|
| S8（CORE_BLOCKING [BUG]） | 关闭 | (a) run/ACP/SDK 只提示不推进，只有 chat 横幅展示后推进（设计 126-130、159-160 行，D9 196 行）；(b) 人工 set/unset 只更新被改键，不重算其他键（116、131、153 行），写失败按 GR109 记 Warn（134 行）；(c) 读改写持配置锁、临时文件加改名（123 行），验证项含并发启动（182 行）；(d) 快照位置 `SUGA_HOME/run/protected-config*.json`（123、177 行），`data restore` 只替换 `db/sessions.db`、`config.yaml`、`memory/user`、`projects`（设计 84 行，代码 `backup.go:432` 核实，不含 `run/`），`SUGA_CONFIG` 按路径摘要分文件（123 行，与 `yaml_edit.go:102-127` 命名法一致）；(e) 由用户选定的 D10 处理：按原始值比较，限制写入非目标与风险（68、170、197 行），摘要不含展开后的秘密。核心目标改为“你下次打开 chat 能看到”（34 行），有 D9/Q7 用户选择追溯（13、196、203、208 行），不是静默缩窄。主要威胁路径（gofer run 先启动）下 chat 仍会提示，验证项“变化后 run 不推进而 chat 推进”（182 行）直接覆盖。残留细节（推进取值的时点）见 S14，不构成阻断。 |
| S9 | 关闭（附注） | 分类原则写明（93 行）；`tools.disabled`、`rules.file`、`models.compact`、`compact_model` 列入（97-100 行）；“不保护”分组与理由（103-107 行），`gc.retention.*` 的 trace 风险列为已知（105、171 行）。附注：我按 93 行原则逐键核对注册表全部 190 个 `Writable:true` 静态键与动态键，“等”字未覆盖的键（`run.deadline_env`、`network.proxy.sites`、`transcripts`、`tools.loop_guard`、`hooks.timeout_ms/stop_wait_sec/stop_background`、`memory.*.timeout_sec`、`mcp.*_timeout_sec`、`tasks.wake_on_exit`、`tools.shell_background_after`、`log.level/stderr/max_size/keep/compress`、`tools.spill.*` 等）均不满足原则：`run.deadline_env` 只按 RFC3339 解析且失败只记 Debug（`deadline.go:79-89`），`network.proxy.sites` 只把主机导向不可写的 `network.proxy.url`（`settings_registry.go:120-122`）。未发现按原则应列入而遗漏的键；逐键归属由计划阶段的显式分类表测试（109 行）落定。 |
| S10 | 部分关闭 | Q4–Q8 的选项与推荐已记录（199-204 行），D6 已列全 0.3、0.4 新增键（193 行）。残留：记录的 Q4 推荐只含“`providers.<id>.base_url/type`、模型级 `base_url`、`workspace`、`models.judge`、`memory.*.backend`”（200 行），而 D6 标注为“（Q4）”却还含模型级 `type`、`models.compact`/`compact_model`、执行上限类、`log.file`、`tools.disabled`、`rules.file`（193 行）；这些键来自评审补入，没有用户选择证据。另外 D4 写“放宽类全部……含 `tools.shell_limits.*`”（191 行），而 0.4 的表把 `tools.shell_limits.*` 移到了新的“执行上限类”（99 行），D4 文字与表不一致。不阻断：设计仍为 Draft，人工计划 Gate 会就整份清单批准，清单在文档中明示，没有越过 Gate。disposition 沿用 `CORE_CORRECTIVE`。 |
| S11 | 部分关闭 | stream-json 字段已删除（73、129 行，修订记录 13 行），run 改为 stderr 一行，消费方是 gofer job 日志（73 行），成立；trace 新事件已声明为新增接口（74 行）。更正上轮事实：现有 trace kind 中有 `session`（`internal/personal/store/types.go:88-93` `EventSession`，说明为“会话自身生命周期”），上轮 S11 说“没有 `session`”不准；0.4 改用新 kind `config` 与 `EventSession` 的语义不冲突，选择成立。残留：trace kind 的权威说明是 `store/types.go` 中带注释的常量（57-144 行，每个 kind 一条），架构（143 行）只写了 `observe.go`，文档清单（176 行）也没有 trace 事件说明；应在架构中加“`store.EventConfig` 常量与注释”。disposition 沿用 `CORE_CORRECTIVE`（LOW）。 |
| S12 | 关闭 | 78 行 `:310-357` 核实；79 行 `pkg/agentcore/tools/native/local_command.go`（`:124` `cmd.Env = shellEnv(...)`）核实；80 行 proctree 全路径；81 行 config 命令不接收 stdin（`root.go:69` `newConfigCommand(stdout, stderr)`、`config.go:21`）如实写为待改，并进入架构（141 行）与方案（114 行）；84 行 `backup.go:432` 核实。0.4 新引用 `yaml_edit.go:102-127`、`observe.go:107-108`、`cwdcheck.go:212-215`、usage `:125`、`config_test.go:132-160` 与 `:175-204`、`approval_flags_test.go:133` 均核实成立。 |
| S2 残留 | 关闭 | 172 行写明 trust 档下外部来源检查被放开，只剩 CLI 判定与启动提示。 |
| S5 残留 | 关闭（附注） | 72 行写明 xhen README 终端用法不需改，Git Bash/mintty 的说明由 xhen 侧跟进时补。附注：这是跨项目后续动作，建议在 xhen 侧登记 issue，避免只存在于本设计。 |
| S7 残留 | 关闭 | GR107 已从规范列表（87 行）与架构（141 行）移除。但 0.4 新引入的 SR2003 引用同属“结构条目误引”一类，见 S13。 |

## 0.4 新增内容治理核对摘要

- design-contract：思考声明（32-38）、修订记录（8-13）、背景与用户可见结果（40-54）、名词（56-60）、范围/非目标/受影响项目与接口（62-74）、事实与规则 ID（76-87）、方案（89-134）、架构与代码组织（136-143）、流程（145-162）、安全/数据/运维/回滚/验证（164-184）、决策与选项记录（186-204）、待确认（206-208）、Gate（210-212）均在。作者修订摘要（17-30 行）没有写“按类自查的范围”（0.3 有），见 S17。
- SR1103：0.4 含语义变化（D9、D10、核心目标措辞、入口表），递增正确；日期元数据不一致见 S17（不需升版）。
- SR1108 / SR1408：核心目标与 scope freeze 的变化（34-35 行）都追溯到 Q7/Q8 用户选择；D6 中评审补入的键归属见 S10 残留。
- SR1409：高暴露（`git tag` 核实 `v0.55.0` 存在；涉及持久命令执行与 API key 去向），两轴独立 + ledger 相称。
- SR1301：鉴权——已核对，CLI 判定如实定位为提高门槛（117 行），本轮无新增阻断；注入防护——已核对，不拼接命令，成立；秘密——已核对：快照只存原始值摘要（D10 后不再接触展开后的秘密）、stderr 与 trace 只含键名（74、128-133 行）、秘密键仍不可写（166 行），成立；快照文件权限未写，见 S15（不构成违规）；入参校验——已核对，沿用现有校验；资源归属——不适用。
- SR1403 / SR1405 / SR1410（新机制最小性与 scope necessity）：
  - D9 chat 展示后推进——必需（S8 关闭所依赖，用户选定）；
  - 人工 set 只更新被改键——非核心验收所必需（删掉后只多一次对自己改动的提示），但无害、已由用户在 D9 中选定，不另立 finding；
  - 损坏不静默重建——必需（否则删改快照可静默吸收）；
  - trace `kind=config`——必需（ACP/SDK 唯一可见面），已声明为新接口；
  - run stderr 一行——必需且最小（替代无消费方的 stream-json 字段）；
  - 比较结果进程内缓存（133 行）——优化，非必需；配置本就只在进程启动时读一次，缓存不引入陈旧语义，不另立 finding；
  - 未发现无法追溯到核心目标的新机制。
- GR107：已不再误引。GR108：wiring 只调用 config 的比较函数并转交键名（142 行），成立；但 chat 横幅后由谁调用“整体推进”没有写进代码组织，见 S14。GR109：快照写失败记 Warn 并写明理由（134 行），对 set 路径同样适用，成立。GR110：经 `internal/cli` 真实分发，含空 `SUGA_SESSION_ID`、注入终端判定（180 行），成立（`RunWithArgs` 已是现有 CLI 测试的分发方式）。SR2003：误引，见 S13。SR2004：参照 suga 自身分层（138 行），成立。GR104：新增文件独立，不会使现有文件超限。
- 入口表（126-131 行）：chat、run、ACP、SDK、`config set/unset` 与代码入口（`run.go:187`、`acp.go:84`、`pkg/sugasdk/session.go:98`）一致；遗漏 `suga compact`（`compact.go:60` 同样经 `wiring.OpenCLI`），见 S16。

## 代码事实核对

- 分类表与 D6 中每个具体键在注册表中存在；`*`/`{}` 展开项与注册表一致（`tools.approval_rules.*` 287-290 行附近、`tools.shell_limits.*`、`tools.session_message.*`、`tools.origin_judge.*`、`tasks.max_*`、`tools.read_deny*`）。
- 快照放在 `SUGA_HOME/run/`：`run/` 同时是会话进程记录目录，`personalprocess.List` 扫描其中所有 `*.json`（`process.go:650-668`），`protected-config.json` 因 PID≤0 / 文件名不匹配会被跳过、不会被删除；`backup_restore.go:85`、`gc_maintenance.go:34`、`data_gc.go:212-219` 只把 `run/` 当锁与进程记录目录使用，没有清空 `run/` 的路径。结论：位置可用，见 S15 的附注。
- 配置锁目录 `run/locks` 以 `0o700` 创建（`yaml_edit.go:121`）；`editConfigFile` 在写入时持锁（`yaml_edit.go:129` 起）。
- chat 横幅输出点 `internal/chat/chat.go:543` `render.Banner()`；`BannerInfo` 在 `layout.go:77`。
- suga 的 CLI 库为 `github.com/gookit/goutil/cflag/capp`（`internal/cli/config.go:14`），不是 gookit/gcli。

## 发现

### S13 [MEDIUM] 新引用的 SR2003 不适用于 suga 的 CLI 库

- disposition：`CORE_CORRECTIVE`（结构项，按合同计划批准前修正，不得延后）
- 证据：87 行规范列表新增 SR2003；141 行“config 命令接收 stdin（SR2003 的命令结构不变）”。SR2003 的适用对象是 gookit/gcli 命令；suga 的 config 命令用 `github.com/gookit/goutil/cflag/capp`（`internal/cli/config.go:14,21-22`）。
- 同类穷尽（规则引用适用性，候选全部出现点）：SR2003（87、141 行）为唯一误引；SR1301（166 行）、SR1403、GR108（142 行）、GR109（134 行）、GR110（180 行）、SR2004（138 行，作为“参照 suga 现有分层”的引用成立）均适用。
- 影响：计划阶段可能按 gcli 的位置参数/选项约定审查一个 capp 命令；与上轮 S7（GR107 误引）同类。
- 建议：删去 SR2003，改写为“沿用 `internal/cli` 现有 capp 命令结构，签名改为 `newConfigCommand(stdin, stdout, stderr)`”。

### S14 [MEDIUM] 快照推进与单键更新的取值时点、调用点未写明

- disposition：`CORE_CORRECTIVE`（S8 同类：快照身份与更新时机；由 D9 新机制带来的细节）
- 证据：“横幅显示后整体推进”（128 行）、流程“持锁读快照比较 → chat：横幅显示 → 整体推进快照”（156-159 行）未说明推进写入的是“展示时计算的摘要”还是推进时重新读取配置得到的摘要；“写入 → 快照中只更新该键”（153 行）未说明单键摘要是否在 `editConfigFile` 持有的同一配置锁区间内、按刚写入的值计算；代码组织（140-143 行）写了 config 提供“整体推进”，但没写由谁在横幅之后调用（应为 `internal/chat`，横幅输出点 `chat.go:543`）。
- 同类穷尽：(a) chat 推进：比较与推进之间另一个 gofer run 改了配置，若推进时重算则吸收未展示的改动；(b) set 单键更新：写入释放锁后再取锁更新快照，其间同键被改则吸收；(c) 推进的调用点不在代码组织中，存在被放进 wiring（违反 GR108）或放在横幅之前的实现歧义。
- 不判阻断的理由：(a)(b) 窗口为毫秒级，且攻击者删除或改写快照的能力已作为已接受风险（169 行），不改变核心验收的成立条件；缺少从“不采纳”到核心验收失败的必要因果链。
- 建议：写明“推进只写入本次横幅展示时计算的摘要；单键更新在写配置的同一锁区间内按写入值计算”；代码组织补“`internal/chat`：横幅显示后调用 config 的推进函数”；验证项补一条“比较后、推进前改动的键在下次 chat 仍提示”。

### S15 [LOW] 快照文件与进程记录共用 `run/` 目录、文件权限未写

- disposition：`CORE_CORRECTIVE`
- 证据：123、177 行快照为 `SUGA_HOME/run/protected-config*.json`；`run/*.json` 是会话进程记录，`personalprocess.List` 逐个读取并按 `<pid>.json` 判定（`process.go:650-668`），当前代码会跳过该文件而不删除，但这是依赖现有判定细节的隐含约定；快照文件模式未写，同目录的锁目录用 `0o700`（`yaml_edit.go:121`）。
- 影响：日后 `List` 的清理条件放宽（例如删除无法解析的记录）会误删快照，触发“首次运行”语义而静默吸收改动；摘要虽不含秘密，原始值可能含写死的令牌（例如 `mcp.servers` 内联值），文件应只对本人可读。
- 建议：写明快照放 `run/` 下的子目录（如 `run/state/`）或在设计中注明与进程记录共存的约定；写明文件模式 `0600`、目录 `0700`。

### S16 [LOW] 入口表遗漏 `suga compact`

- disposition：`CORE_CORRECTIVE`
- 证据：入口表（126-131 行）与流程（155 行）列 chat/run/acp/SDK；`internal/cli/compact.go:60` 同样经 `wiring.OpenCLI` 打开运行时。
- 同类穷尽（经 `wiring.Open`/`OpenCLI` 的入口）：`run.go:187`、`compact.go:60`、`acp.go:84`、`pkg/sugasdk/session.go:98`，以及 chat 路径；只有 compact 未列。
- 影响：比较挂在 wiring 时 compact 也会执行比较，是否写 stderr、是否写 trace 无规定；不推进快照，故不影响核心验收。
- 建议：入口表加一行“其他入口（如 `suga compact`）：不提示、不推进”或与 run 同等处理。

### S17 [LOW] 修订记录元数据：日期不一致、未写按类自查范围

- disposition：`CORE_CORRECTIVE`（纯元数据，按 SR1103 修正时不递增版本、不开新轮）
- 证据：0.4 行日期 `2026-11-11`（13 行），同一行与 208 行引用的用户原话日期为 `2026-11-12`；候选提交时间 `2026-11-11 18:33 -0800`（即 UTC+8 的 11-12）。review-contract 要求“作者修订摘要写明按类自查的范围”，0.3 写了（“按类自查：清单完整性、判定强度与误拒、其他写入口、验证与兼容”），0.4 的处理表（17-30 行）没有。
- 建议：统一用一个时区的日期；在处理表前补一句本轮按类自查的范围（如“快照身份与更新时机、入口覆盖、分类判据、接口声明、引用”）。

## 覆盖缺口与限制

- 未运行代码或测试；并发与锁区间的判断基于代码阅读（`yaml_edit.go`、`process.go`）。
- gofer 对 suga run stderr 的处理以设计 73 行自述为准，未读 gofer 代码。
- Spec/Executability 轴的细节（快照 JSON 格式、i18n 文案、ACP 多会话缓存的实现）未深评。
- S9 附注中的逐键核对只按 93 行原则判断“是否扩大能力”，未逐键追运行时全部用法。

## 结论

`PASS`（本轴无开放 `CORE_BLOCKING`）：上轮 S8 已关闭，S9、S12、S2/S5/S7 残留已关闭；S10、S11 部分关闭，残留为 `CORE_CORRECTIVE`；本轮新增 S13–S17 均为 `CORE_CORRECTIVE`，其中 S13 为结构项，计划批准前必须修正。

## 剩余风险与后续

- S10 残留：D6 中评审补入的键（模型级 `type`、`models.compact`/`compact_model`、执行上限类、`log.file`、`tools.disabled`、`rules.file`）应在人工计划 Gate 中点名请用户确认，或在 D6 中与 Q4 推荐分开标注；D4 文字与表的类别对齐。
- 若作者按 S13–S17 及残留修订，属含 `CORE_CORRECTIVE` 的全 PASS 之后的纠正，可按 planning-core-rigorous 走唯一一轮纠正确认轮（candidate 须变化）；不做确认轮时这些修订按“未复核修订”在 Gate 披露。
- 防护本质仍是“提高门槛 + 下次打开 chat 可见”：刻意绕过、同时删改快照、经 `.env` 改 `${VAR}` 都挡不住，README/usage 需如实写明（176 行已列）。
