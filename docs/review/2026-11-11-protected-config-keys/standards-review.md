<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 0.3 评审（Standards/Governance 轴，第 2 轮 confirmation）

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md`
- 候选四元组：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `a08457674817632068a03ef69fcbb8e502ac5bbb`；subject `docs/design/2026-11-11-protected-config-keys-design.md`；revision `0.3`（上轮候选 `50950889` / 0.2）
- 修订版本：Draft 0.3
- 评审者：Standards/Governance 轴独立子 agent（未参与本设计起草或修订，也未参与第 1 轮）
- 评审轴：Standards/Governance；第 2 轮 confirmation；`REPORT_ONLY`
- 日期：2026-11-11
- 排除项：用户已选的 Q1–Q6 本身（含 D8“加回启动时变化提示”这一选择）不评；只评 D8 是否最小、是否有未追溯的机制、内部是否成立。Spec/Executability 轴的细节只在影响核心验收或治理判定时提及。

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（全文 760 行，含 review-contract、review 模板 1.2.0、review-candidate-loop）、`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（全文 201 行，含 design-contract 与 design 模板 1.2.0）；suga `AGENTS.md`（全文）。
- 上轮本轴报告 `docs/review/2026-11-11-protected-config-keys/standards-review.md`（S1–S7，全文）。
- 候选：设计 0.3 全文（`git show a0845767:<subject>`，183 行）与 `git diff 50950889..a0845767 -- <subject>`；补读 0.2 第 45–75 行作对照。
- 代码事实核对（HEAD `520fd9a7`，相对候选只多了评审台账提交，代码未变）：`internal/config/settings_registry.go`（全部静态与动态描述符，93–357、362–379 行）、`internal/config/load.go:30-63`、`internal/config/rules.go:10-35`、`internal/config/paths.go`、`internal/config/providers.go:600-610`、`internal/cli/config.go:21,61-111,154-180,282-284`、`internal/cli/root.go:60-76`、`internal/cli/cwdcheck.go:207-215`、`internal/cli/init.go`、`internal/cli/backup_restore.go:51`、`internal/personal/backup/backup.go:152,432`、`internal/chat/local_cmd.go:79-100`、`pkg/agentcore/tools/native/local_command.go:116-127`、`pkg/agentcore/tools/native/code_exec.go:563`、`internal/wiring/sessionenv.go`、`internal/hooks/hooks.go:354-355`、`pkg/agentcore/internal/proctree/proctree_unix.go:25`、`pkg/agentcore/tools/rules/rules.go:267-292`、`internal/output/streamjson.go:35`、`internal/chat/layout.go:77`、trace 事件 kind 全量检索；`docs/usage/gofer-personal-agent.md:124`；`xhen/README.md:151`；安全设计 `docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md:157`；`git tag -l v0.55*`。
- 方法：先逐项核对 S1–S7 关闭证据；再对 0.3 新增内容按 design-contract 与所列规则核对；所有设计中的文件:行号与键名逐一对照代码；同类问题一次列全。

## 验收标准

design-contract 必需内容与质量检查；SR1103、SR1108、SR1301、SR1403、SR1405、SR1408、SR1409、SR1410；GR107、GR108、GR109、GR110；SR2004；AGENTS.md“验证”要求；review-contract 的 severity/disposition、CORE_BLOCKING 五类与 necessity proof、新机制 scope necessity、同类穷尽。

## 上轮 S1–S7 关闭核对

| ID | 状态 | 证据 |
|---|---|---|
| S1（CORE_BLOCKING） | 关闭 | 端点类 `providers.<id>.base_url/type`、模型级 `base_url/type`、`models.judge` 进分类表（设计 97 行、D6 173 行），对应代码 `settings_registry.go:311-312,330-331,106`；`workspace`、`log.file`、`memory.*.backend` 进表（95-96 行；代码 114、297、263、267 行）；`Protected` 同时加到动态描述符（91 行）；按键名模式的防漏标测试改为显式分类表覆盖全部可写键（101-102 行）。分类理由的残留问题另列 S9（不属阻断）。 |
| S2 | 部分关闭 | 刻意绕过（`</dev/tty`、`script`、pty、`env -u`、`NoSessionEnv`）已如实写明（109、151 行，D7 174 行）；D5 相对 v0.55.0 的放宽已写明（110 行）。残留：154 行“外部来源裁决……是另一道防线”仍未写明 `trust` 档不检查（安全设计 157 行“档位 `trust` → 不检查”），上轮建议未落实。disposition 沿用 `CORE_CORRECTIVE`。 |
| S3 | 关闭 | 83 行列出前序计划 F14/S12 与注释 `:227`、`:250`（代码 226-229、249-251 行核实）、usage `:124`（核实）、两个测试文件（存在）；156 行替代声明含同步清单。 |
| S4 | 关闭 | 160-164 行“验证要求”：GR110 真实分发、分类表反向验证、ptye2e + `inspect trace --json`、Windows 主机。Git Bash/mintty 由 D7 接受为误拒（155、174 行），不再需要“可用”验证。 |
| S5 | 关闭（附注） | 受影响项目 71-73 行；退出码 2（53、107 行，代码 `config.go:282-284`）；unset 同样适用（106 行）。附注：72 行“（改为直接编辑文件）”未写明 xhen README 是否改、由谁改，157 行文档清单未含 xhen；属 S5 原建议的未完成小项，不另立 finding。 |
| S6 | 关闭（依赖 S8） | `init --force`、`data restore` 列入非目标，由启动提示兜底（49、67、151 行）。兜底是否成立取决于 D8，见 S8（含快照位置与 restore 集合的关系）。 |
| S7（结构项） | 部分关闭 | 123 行改为引用 SR2004 与 suga 现有分层，成立。残留：126 行仍写“命令层只做入口判断，GR107”；GR107 是“`main.go` 薄、命令放 `internal/commands`”，与此句无关（85 行规范列表同）。按合同结构项不得延后，沿用 `CORE_CORRECTIVE`。 |

## 0.3 新增内容治理核对摘要

- design-contract：思考声明（32-38）、修订记录（8-12，0.3 为语义变化，递增正确，SR1103 成立）、背景与用户可见结果（40-54）、名词（56-60，新增“配置快照”）、范围/非目标/受影响项目（62-73）、事实与规则 ID（75-85）、方案（87-119）、架构与代码组织（121-128）、流程图（130-145）、安全/数据/回滚（147-164）、决策（166-175）、待确认（177-179）、Gate（181-183）均在。作者修订摘要写明了按类自查范围（16 行），合乎 review-contract。
- SR1108 / SR1408：核心目标与 scope freeze 因 Q6 扩为“set/unset 限制 + 启动时变化提示”（34-35 行），有用户原话追溯（12、179 行），不是静默扩围。但 Q4–Q6 的选项与“推荐”内容没有写进任何已提交产物，D6 也没列全 0.3 新增的键，见 S10。
- SR1409：暴露度高（`v0.55.0` 已发布，已用 `git tag` 核实），两轴独立 + ledger 相称。
- SR1301：鉴权——已核对，CLI 判定如实定位为提高门槛（D7），本轴无新增阻断；注入防护——已核对，不拼接命令，成立；秘密——已核对，快照存摘要、提示只含键名、秘密键仍不可写（149 行），成立。计划阶段应写明摘要的输入是原始配置文本还是 `${NAME}` 展开后的值（`load.go:47` 启用 `ParseEnv`；`mcp.servers` 等值可能含展开后的令牌），见 S8(e)；入参校验——已核对，沿用现有校验；资源归属——不适用。
- SR1403 / SR1405 / SR1410（D8 最小性）：快照 + chat 横幅 + trace 事件直接追溯到核心目标后半句，必需。stream-json 字段与新 trace kind 是未声明的 Interface 增量，其中 stream-json 字段没有消费方，必要性未证明，见 S11。“人工 set 刷新快照”可删除且删除后核心验收仍成立（只是人会看到一次自己的改动），并且它会吞掉未提示的改动，见 S8(b)。
- GR107：残留误用，见 S7。GR108：suga 的 `internal/wiring` 已承载审批策略等逻辑（`approval_policy.go`、`origin_guard.go`），设计让 wiring 调 config 的比较函数并把结果交给各入口（127 行），比较与快照读写放在 `internal/config`（125 行），与现有分层一致；`wiring.Open`/`OpenCLI`（`runtime.go:235,380`）是 chat/run/acp/SDK 的共同入口，挂一处即可覆盖，成立。GR109：快照写失败记 Warn 并写明理由（119 行），成立；人工 set 后刷新快照失败如何处理未写（见 S8(b)）。GR110：161 行经 `internal/cli` 真实分发，成立。SR2004：123 行写明参照 suga 自身分层，成立。GR104：`internal/cli/config.go` 291 行、`settings_registry.go` 737 行，加字段不会超限。
- 新机制 scope necessity：`Protected` 字段、显式分类表测试——必需；启动时变化提示（快照、横幅、trace）——必需（用户选定，核心目标直接依赖）；stream-json 字段——未证明（S11）；人工 set 刷新快照——非必需（S8(b)）。

## 代码事实核对

- 分类表与 D4–D6 中的每个具体键都在注册表中存在：执行类 13 个（`settings_registry.go:192,232,247,263-269,273,275,278-279`）；放宽类具体键均存在；端点类 5 个（`:106,311-312,330-331`）。
- 带 `*` 的项实际展开为：
  - `tools.approval_rules.*` = `enabled`、`always_prefix_tokens`、`persist_ask`、`project_trust`（`:287-290`）；
  - `tools.shell_limits.*` = `memory`、`processes`、`cpu_time`、`off`（`:238-241`）；
  - `tools.session_message.*` = `enabled`、`in_gofer_jobs`、`max_hops`（`:203,206,207`）；
  - `tools.origin_judge.*` = `enabled`、`timeout`（`:230-231`，当前无 `Writable`）；
  - `tasks.max_*` = `max_running`、`max_duration`、`max_output_bytes`（`:180-182`）；
  - `tools.read_deny*` = `read_deny`、`read_deny_defaults`（`:252-253`，当前无 `Writable`）。
- 引用的行号核实成立的：`load.go:33-67`、`config.go:61-110`、`:153-169`（函数起于 154）、`:282-284`、`settings_registry.go:371-375`、`:227`、`:250`、`code_exec.go:563`、`sessionenv.go:17`、`cwdcheck.go:214`、`streamjson.go:35`（`type: session` 行）、`layout.go:77`（`BannerInfo`）、usage `:124`、`xhen/README.md:151`。
- 引用不准的见 S12。

## 发现

### S8 [HIGH] 启动提示的快照更新规则会吞掉提示，主要威胁路径下“下次启动能被你看到”不成立

- disposition：`CORE_BLOCKING` [BUG]
- 证据：核心目标“用其他方式改动这些配置时，下次启动能被你看到”（34 行）；用户可见结果（54 行）；“提示后更新快照（每处改动只提示一次）”（118 行，D8 175 行）；run 只在 stream-json 加字段，而 gofer“旧解析器忽略”（73、116 行）；ACP、SDK 只写 trace（117 行）；威胁场景本身就是 gofer 可写任务（44 行）。
- 影响：模型在 gofer run 任务里用 `shell_exec` 改了 `config.yaml`（非目标，靠 D8 兜底，67 行）。下一次启动多半是另一个 gofer run 任务：提示进了没人读的 stream-json 字段和 trace，随后快照被更新；你之后开 chat 横幅什么也不显示。S6 的兜底（`init --force`、`data restore`）也因此落空。
- 同类穷尽（快照身份与更新时机，候选中的全部出现点）：
  - (a) 非交互入口（run/ACP/SDK）先启动就把提示消耗掉（118 行）；
  - (b) “人工 set 后刷新快照”（108、138 行）如果按全部 protected 键重算，会把之前未提示的改动一并吸收；刷新失败的处理（GR109）也没写；
  - (c) gofer 并发启动多个 suga 时，快照的读、比较、写没有说明互斥或原子写，可能重复提示或丢失；
  - (d) 快照位置只写“SUGA_HOME 状态目录”（60、114、158 行），代码里没有这个目录（`internal/config/paths.go` 只有 `db/`、`sessions/` 等）；没写明是否落在 `data restore` 的恢复集合之外（`internal/personal/backup/backup.go:432` 恢复 `db/sessions.db`、`config.yaml`、`memory/user`、`projects`），落在集合内则 restore 会连快照一起换掉；`SUGA_CONFIG` 指向不同文件时共用一个快照，会来回误报；
  - (e) 摘要的输入是原始值还是 `${NAME}` 展开后的值没写（`load.go:47` `ParseEnv`）：用原始值，则改 SUGA_HOME `.env` 换掉端点不会提示；用展开值，则摘要中含秘密派生内容（SR1301）。
- necessity proof：
  - core_acceptance：34 行核心目标后半句；54 行用户可见结果。
  - necessary_causal_chain：不修 → 第一次非交互启动消耗提示并更新快照 → 人可见的入口（chat 横幅）不再提示 → 主要威胁路径（44 行）下改动对人不可见 → 核心验收不成立。
  - minimal_owner：本设计第 3 节（114-119 行）与 D8 的快照更新规则。
  - deletion_alternative：删除 D8 = FAIL（核心目标后半句随之消失，需用户改范围）；收窄为 target-locked corrective = PASS，例如快照只记录“已在 chat 横幅展示过”的状态，或保留待提示键名直到 chat 展示；删掉“人工 set 刷新”或改为只刷新所改的键；写明快照路径（不在 restore 集合内）、按配置路径区分、原子写。
  - scope_effect：不新增 Module/Interface/Schema；快照多一个“待提示”语义，属本设计已有 state 的细化。它改变了 D8 已记录的“提示一次、人工 set 刷新”细节，按 SR1408/SR1401 须请用户确认。
- 建议：按上面的收窄方案改第 3 节和 D8，并在验证要求里补 (a)、(b)、(c) 的测试（例如“run 先启动后 chat 仍提示”）。

### S9 [MEDIUM] 分类判据不一致、个别不列入理由不实

- disposition：`CORE_CORRECTIVE`
- 证据与同类穷尽：
  - `rules.file` 的理由“只接受不带路径的文件名”（99 行）答非所问：S1 指出的问题是换规则文件名能绕开 `ClampAllow` 对规则文件的保护（`pkg/agentcore/tools/rules/rules.go:267-292` 按当前配置的规则文件名保护），模型先写一个未受保护的文件再改 `rules.file` 指向它。真正的理由应是：这与 `shell_exec` 直接改 `AGENTS.md` 同级（`ClampAllow` 只管文件工具，280-283 行），属 D2 已接受的范围。要么改写理由，要么列入。
  - `models.judge` 的例外理由是“它决定外部来源裁决由谁做”（99 行），但未配置 `models.judge` 时裁决用 `models.compact`、再用主模型（`settings_registry.go:104-105`；usage `:124`）。按同一理由，`models.compact`/`compact_model`/`model` 在缺省配置下同样决定裁决者，判据前后不一。端点去向已由 `base_url/type` 守住，建议统一判据：要么只保护端点、删掉 `models.judge` 例外，要么写明这几个键为何不同。
  - 资源上限类：放宽类收了 `tools.shell_limits.*`、`tools.shell_timeout_max`、`tools.task_timeout_max`、`tasks.max_*`（96 行），但同属上限的 `budget.tokens`/`budget.cost_usd`（`:108-109`）、`max_steps`（`:107`）、`agents.max_steps/max_tokens/timeout`（`:186-188`，被 `agents.*` 整体排除）、`hooks.timeout_ms`、`mcp.call_timeout_sec`、`tools.max_output_bytes` 没有列入，也没有给理由。
  - 兜底“其余显示与性能类键”覆盖不了这些不属显示或性能的可写键：`gc.retention.*`（`:291-292`，缩短保留期会删掉 D8 依赖的 trace 证据）、`transcripts`、`hooks.stop_wait_sec`/`hooks.stop_background`、`network.proxy.sites`、`run.deadline_env`、`tools.snapshots.*`、`tools.loop_guard`、`providers.<id>.compat`、`providers.<id>.models.<name>.model_id`。
- 影响：计划阶段的显式分类表要逐键写理由（102 行），判据不一会使表的取舍无据可依；理由不实会误导后来的维护者。
- 建议：在第 1 节写一句判据（例如“改变权限、执行、去向或安全检查”；资源与成本上限归哪一边），按判据重写上述键的归属或理由；如有键移入 protected，属扩大 D4/D6 清单，须请用户确认。

### S10 [MEDIUM] Q4–Q6 的选项没有留痕，D6 没有列全 0.3 新增的键

- disposition：`CORE_CORRECTIVE`
- 证据：12、179 行引用用户原话“Q4–Q6 按推荐”，但 Q4–Q6 的选项与推荐内容不在任何已提交产物中（`git log --all -S'Q4' -- docs/` 只命中本次提交与其他设计；0.1、0.2 只有 Q1–Q3）。0.3 在放宽类新增了 `tools.shell_timeout_max`、`tools.task_timeout_max`、`tasks.max_*`、`log.file`（diff 中 86 行对比 0.2 的 62 行），D6（173 行）没有列出这几个键，它们只能经 D4“放宽类全部”带入，而用户为 D4 选的是 0.2 的清单；修订摘要写“`workspace` 等”（12 行）。
- 影响：无法从文档核对“按推荐”批准的范围与 D6–D8 是否一致；SR1108“不得静默扩大范围”的证据不完整。不阻断，因为人工计划 Gate 会就整份文档批准。
- 建议：在 D6–D8 各附一句 Q4–Q6 的推荐内容，D6 列全 0.3 新增的键（含上述 4 项），修订摘要去掉“等”。

### S11 [MEDIUM] D8 新增的对外接口未声明，stream-json 字段必要性未证明

- disposition：`CORE_CORRECTIVE`
- 证据：116 行 stream-json `session` 行新增 `protected_config_changed`，而唯一已知消费方 gofer 会忽略它（73 行）；117 行 trace 事件 `kind=session`、`name=protected_config_changed`，代码中现有 trace kind 里没有 `session`（全量检索 `Kind: "…"`，只有 `run_started`、`approval`、`hook`、`command` 等），即这是新增的 trace kind；两处都没在“受影响项目”或“文档”（157 行）中写明属于接口变化，也没写进 trace 事件说明。
- 影响：SR1403/SR1405/SR1410 要求新机制直接追溯到验收。stream-json 字段没有消费方，对“被你看到”没有贡献；新 trace kind 改变了 `suga inspect trace --json` 的输出面，却没有文档合同。
- 建议：stream-json 字段要么删掉（run 由 trace 与 S8 的 chat 补报覆盖），要么在受影响项目中登记 gofer 侧的展示为后续 issue（跨项目，deferred）；trace 改用已有 kind，或明确声明新 kind 并列入文档更新清单。

### S12 [LOW] 已确认事实中的路径与注入关系引用不准

- disposition：`CORE_CORRECTIVE`
- 证据（同类全部出现点）：
  - 78 行 `internal/chat/local_command.go:122-127` 不存在；行号对应的是 `pkg/agentcore/tools/native/local_command.go:122-127`（`cmd.Env = shellEnv(...)`，未设 Stdin），chat 入口在 `internal/chat/local_cmd.go:93`；
  - 82 行 `internal/cli/backup.go:152,432` 不存在；实际是 `internal/personal/backup/backup.go:152,432`，命令在 `internal/cli/backup_restore.go:51`；
  - 79 行 `proctree_unix.go:25` 缺目录，实际为 `pkg/agentcore/internal/proctree/proctree_unix.go:25`；
  - 80 行“命令的 stdin 来自 `internal/cli/root.go:69`”、106 行“stdin 取……root 注入的那个”：`root.go:69` 是 `newConfigCommand(stdout, stderr)`，config 命令目前拿不到 stdin（`config.go:21`），需要改签名；
  - 77 行动态描述符“`:311-331`”只覆盖到模型级 `base_url`，实际范围为 `:310-357`（provider 级 311-325、模型级 329-345、provider retry/timeouts 349-356），分类表要覆盖全部动态键时应引用完整范围。
- 影响：计划阶段按错的路径找 owner；stdin 注入是需要做的改动，却被写成现状。
- 建议：按上列更正；把“config 命令接收 root 的 stdin”写进架构的 `internal/cli` 条目。

## 覆盖缺口与限制

- 未运行代码或测试；S8 的 gofer 消费情况以设计 73 行自述为准，未逐行读 gofer 的 stream-json 解析器。
- 未逐行追 `gc.retention.*`、`hooks.stop_background` 等的安全含义，只指出缺判据（S9）。
- Spec/Executability 轴的细节（快照格式、比较算法、i18n 文案）未深评。
- HEAD 与候选之间只多了评审台账提交，代码事实按 HEAD 核对，与候选时点一致。

## 结论

`BLOCKED`：S8 为开放的 `CORE_BLOCKING` [BUG]（启动提示的快照更新规则在主要威胁路径下吞掉提示）。S2、S7 残留与 S9–S12 为 `CORE_CORRECTIVE`，计划批准前应修正（S7 属结构项，按合同不得延后）。上轮 S1、S3、S4、S5、S6 已关闭。

## 剩余风险与后续

- S8 的修正会改变 D8 已记录的“提示一次、人工 set 刷新”细节，须请用户确认后出 0.4；下一轮按 `git diff a0845767..<新候选>` 复核 S8 与依赖章节（第 3 节、关键流程、验证要求、D8）。
- 即使修正后，防护仍是“提高门槛 + 事后可见”：刻意绕过与同时改写或删除快照都挡不住，README/usage 需如实写明。
- S9 若导致清单扩大（如 `budget.*`、`gc.retention.*`），属扩大用户所选清单，同样须用户确认。
