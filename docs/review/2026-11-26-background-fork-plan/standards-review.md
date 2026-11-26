<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）实施计划 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `00c890f7`；subject `docs/plans/2026-11-26-background-fork-plan.md`；实施对象为已批准设计 `docs/design/2026-11-26-background-fork-design.md` 0.4
- 修订版本: Draft 0.1
- 评审者: Standards/Governance 轴独立评审者（Claude 子 agent），计划评审第 1 轮，REPORT_ONLY
- 日期: 2026-11-26
- 排除项: Spec/Executability 轴的逐任务技术正确性（另一轴负责）；不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`、`tmp/idev-std/d/L1pSw4v7EitW46DpcPDbLQ/payload.md`（plan-contract、plan-closure、planning-core-rigorous、plan 模板、review 合同与模板），全文读取。
- suga `AGENTS.md`、计划全文（231 行）、设计全文（227 行），全文读取。
- 在 candidate 上用 `git show 00c890f7:<path>` 核对计划引用的代码事实：各待改文件行数、`internal/wiring/subagent.go:66` 的接口断言、`requestBaseBetweenTurns` 调用方、`startBackground` 的 kill 分支、i18n catalog 布局与 `TestCatalogParity`、chat 与 AgentTool 的装配位置、设计评审 ledger 状态。
- 运行 `idevstd script validate_document --kind plan <plan>`：`ok: true`，`errors: []`（输出 `tmp/review/suga-fork-plan/validate-plan.out`）。
- 按用户要求只报影响核心验收或安全的问题，同类一次列全。

## 验收标准

plan-contract 必需项核对：

| 项 | 结果 |
|---|---|
| 思考模式、核心目标、scope freeze、budget、停止条件 | 有（第 14–19 行） |
| Capability Discovery 三表 | 有，列名与模板一致；New module / Rejected 用 `NONE` sentinel；validator 通过 |
| 唯一 host 声明 | 恰一个 `host_or_non_offline_action=REQUIRED`（第 35 行），列出了主机动作 |
| workspace baseline | 有 Git root、branch、HEAD、status、expected symbols；HEAD `a8a2bc83` 与所列 dirty 路径已过时（`d5dc67ce` 已提交该文件），计划写明实施开始时重新记录，属 identity 元数据，不记 finding |
| 前置检查与 fail-closed | 有 |
| 波次 | 有（W1–W5，与任务依赖一致） |
| 每任务文件/动作/验证/完成标准/依赖 | 五项齐全；文件清单漏项见 PS3 |
| 回滚与恢复 | 有；T4 提交点不可构建，见 PS3 |
| 人工 Gate（含执行者由用户指定） | 有：计划批准时由用户指定 T1–T8 执行者模型；执行请求；外部动作 Gate |
| 可追溯性 | 有表；两项验收没有实施任务承载，见 PS1 |
| 完成 Gate 与剩余工作 | 有，含 ≤8 行完成总结与 retrospective |
| 计划批准请求列出仪式成本数字 | 缺，见 PS5 |

规则逐项：

- SR1301：鉴权/授权已核对（执行白名单、父策略快照、ask 失败关闭转普通错误，只转换 `ErrDenied`，未碰取消路径）；注入防护已核对（分身不执行 shell，T9 脚本不拼接外部输入）；秘密已核对（T9 只复制 `${VAR}` 形式的 `api_key`、不打印环境与 `.env`、结束删除隔离 home）；入参校验已核对（`:bg` 空参、`type: fork` 未挂 tasks 报错）；资源归属已核对（种子按 ctx 中的会话 ID 取本会话 tracker）。无违反。
- SR1403：复用 `startBackground`/`RunChild`/`subAgents`，`skipPrint` 参数有依据（`prefix_changed` 判定确在 `requestBaseBetweenTurns` 内，`segment_handoff_reuse.go:225-229`）。无多余机制。
- SR1407：任务验证为 focused 命令，全量、`-race`、staticcheck 集中在 T9。符合。
- SR1409：Full 档有设计“安全、数据、运维与回滚”第 1 条的可核实事实（公开仓库、release workflow、对外安装方式）；两轴独立评审。符合。计划体量与改动相称。
- GR104：`runner.go` 947 行、`wiring/runtime.go` 964 行、`chat.go` 935 行，计划只加少量行、主体放新文件，且限定 `chat.go` 净增 ≤3 行。符合；实施时注意 `wiring/runtime.go` 还要承接 PS3 的注入点。
- GR107：不改 `cmd/`。不适用。
- GR108：`forkHost` 绑定只在 `runtime.go` 一处；`forkDenyTools` 放 wiring 与现有 `subAgentDisabledHints`（`wiring/subagent.go:264`）同一 owner，且为设计“代码组织”所列。符合。
- GR109：任务书写明遵守；新增写入路径只有内存 tracker 与 tasks 通知。符合。
- GR110：T7 `TestRunWithFork` 经 `app.Run`。符合。
- SR2002：前置检查写明 goutil assert 与 `t.Run`。符合。
- SR2004：不新增 CLI。不适用。
- 设计范围：无超出设计范围的任务；与设计“代码组织”的结构差异见 PS4。

## 发现

### PS1 [HIGH] 两项验收断言没有任何实施任务承载

- disposition: `CORE_BLOCKING` [MISSING_FUNCTION]
- 证据:
  - `fork_reuse`：完成定义 2（计划第 25 行）要求“`fork_reuse` 记在分身首个 model 事件”，T7（第 166 行）断言首个 model 事件 `fork_reuse=prefix`。T1–T6 的动作里只有 `ForkSeed.Reuse` 字段（第 99 行），没有任务负责把它写成 model 事件 attr；T2 只验证 `prefix_*` 统计（第 112 行）。
  - kill：设计流程 3（设计第 155 行）要求 kill 后 `stop=cancelled`、task output 写最后一段回答、通知写“分身被终止”；设计验证要求含“kill 后 `stop=cancelled`”（第 213 行）；T7 ③ 断言“`:tasks -k t1` 后通知含‘分身被终止’”（第 166 行）。T4 的动作只改正常结束的通知分界（第 134 行），没改 kill 分支。现有 kill 分支有意不发通知、只写“已终止”（`pkg/agentcore/tools/native/agent.go` `startBackground` 中 `r.Killed()` 分支，注释说明通知会唤醒没人要的回合），所以不改就没有这条通知。
- 影响: 照计划实施完 T1–T6 后，T7 的两条断言必然失败。T7 只允许改测试文件，执行者要么按 fail-closed 停下，要么越出文件清单临时改 `agent.go`，自行决定 kill 是否发通知、是否唤醒空闲 chat，用户看到的行为因执行者而异。
- necessity proof:
  - core_acceptance: 完成定义 2；设计验证要求“kill 后 `stop=cancelled`”与 ptye2e `:tasks -k` 终止场景。
  - necessary_causal_chain: 计划没有任务写 `fork_reuse` attr、没有任务改 fork 的 kill 分支 → T7 断言 `fork_reuse=prefix` 和“分身被终止”失败 → T7 完成标准无法满足，完成定义 2 和 kill 验收失败。
  - minimal_owner: T2（或 T3）的动作加“分身首个 model 事件写 attr `fork_reuse=<Seed.Reuse>`”及 focused 断言；T4 的动作加 fork 分支的 kill 处理（通知文案、是否唤醒、task output 内容）及 `stop=cancelled` 单测。
  - deletion_alternative: `fork_reuse` 为 FAIL（完成定义直接要求）。kill 通知为 UNKNOWN：设计要求发通知，现有代码有意不发；若作者认为应保留“kill 不发通知”，那就是改设计的用户可见行为，要按 SR1401 回设计 Gate，不能在计划里自行删掉。
  - scope_effect: 不新增 Module/Interface/Schema/lifecycle，只在既有任务里加动作与断言行。
- 建议: 按 minimal_owner 补两个任务的动作和验证，并在可追溯性表把“kill 与通知”的实施任务从只有 T7 改为 T4 + T7。kill 通知与现有“不唤醒”注释冲突，要在 T4 写明取舍（例如发通知但不唤醒空闲回合）；若这条取舍偏离设计，先请用户确认。

### PS2 [MEDIUM] 设计写明的三项用户可见行为没有任务承载（与 PS1 同类，未被断言）

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: true`
- 证据:
  - 设计 F6 与 Gate“用户可见差异”（第 185、222 行）：种子超阈值时拒绝启动并“提示改用 `agent explore`”。计划 T1 的 `ForkUnavailableError` 只产出 `fork unavailable: context`（第 99 行），T4 只透传为 `agent: fork unavailable: <reason>`（第 133 行），没有这条提示。
  - 设计总体方案 5 与安全第 4 条（第 112、167 行）：分身指令要求报告不超过 4000 字符、列出依据的文件:行号。T1 的 `directive` 由调用方传入，但没有任务规定由谁、用什么文案组装这段指令（T3、T4、T6 都没写）。
  - 设计观测 6（第 113 行）：分身会话 meta `fork_base`（基准 turn、尾部来源 `call|bg`）。`ForkSeed` 有 `BaseTurn`/`Source` 字段，但没有任务把它写进会话 meta。
- 影响: 照计划实施，模型和用户看不到“改用 `agent explore`”的提示；分身报告长度与引用格式不受约束，超过 4000 rune 时通知被截断的情况会更多；trace 里没有 `fork_base`。核心验收不因此失败，但与已批准设计的用户可见结果不一致。
- 建议: 在 T1（提示文案与 `fork_base` 的来源字段）、T3 或 T4（指令组装与 meta 写入，选一个 owner）补动作，各加一条 focused 断言。

### PS3 [MEDIUM] 任务文件清单漏列必改文件，T4 提交点不可构建

- disposition: `CORE_CORRECTIVE`
- 证据（全部出现点）:
  - T5：`requestBaseBetweenTurns` 加参数，但唯一的现有调用方 `internal/personal/compact.go:838` 不在 T5 文件清单（第 143 行）。不改它 `internal/personal` 无法编译。
  - T4：`native.SubAgents` 接口加 `ForkSeed` 方法（第 133 行），而 `internal/wiring/subagent.go:66` 有 `var _ native.SubAgents = (*subAgents)(nil)`。T4 提交后到 T3 完成之前 `internal/wiring` 无法编译，与“恢复点为各任务提交”（第 195 行）不符。
  - T6：`:bg` 要调 `native.AgentTool.StartFork`，但 AgentTool 实例只是 `internal/wiring/runtime.go:551` 的局部变量，chat 由 `internal/cli/chat.go:105` 以 `chat.Options{...}` 构造，只拿到 `Tasks`。注入点（`chat.Options` 新字段、`internal/cli/chat.go`、wiring Runtime 暴露 AgentTool）不在 T6 文件清单，`chat.go` 加 Options 字段也会突破“净增 ≤3 行”。
  - i18n：T1 `personal.fork.not_run`、T2“i18n 收尾指令”、T3 `wiring.fork.denied` 都要求 i18n 文案，但 `internal/i18n/catalog/{zh,en}/*.ini` 不在这三个任务的文件清单；catalog 没有 `[wiring]` 段，`TestCatalogParity` 要求中英键集与占位符一致，而 T3 文案写的是 `%s`，catalog 占位符用 `{name}`。
- 影响: 任务书规定“只改本任务列出的文件”（第 75 行），执行者会在编译失败或 parity 测试失败时停下，或自行越界，多出返工。T4 的提交不能作为恢复点。
- 建议: 把上述文件补进对应任务；T4 同时在 `wiring/subagent.go` 加返回 `ForkUnavailableError{no_request}` 的占位实现，或把接口方法移到 T3，保证每个任务提交可构建；i18n 写明键所在 ini 段与 `{name}` 占位，或明确模型可见文案照 `denial.go` 现例直接写在代码里。

### PS4 [LOW] 与设计“代码组织”的结构差异未登记理由

- disposition: `CORE_CORRECTIVE`（review 合同：结构差异在计划批准前修正）
- 证据:
  - 公开包 `pkg/agentcore` 新增导出 `ExpandToolCalls`、新文件 `expand.go`（T1，第 95–97 行），设计“代码组织”（第 130–136 行）没有列 `pkg/agentcore`。理由只写在 CAP-03。
  - 公开接口 `native.SubAgents` 加方法（T4），对外部实现方是破坏性变更；工作区内没有其他实现方（xhen 未实现该接口，已核对）。
  - 设计写 `:bg`“只调 personal 层”，计划 T6 让 chat 直接调 `native.AgentTool.StartFork`。chat 本来就 import native，依赖方向不变，但与设计文字不一致。
- 影响: 不影响核心验收。公开包 API 变化不在 CHANGELOG 任务（T8）的写作范围里，使用方看不到。
- 建议: 计划加一小段“与设计代码组织的差异”，三行各写理由；T8 的 CHANGELOG 动作加上 `pkg/agentcore` 的 API 变化（新导出函数与接口新方法）。

### PS5 [LOW] 计划批准请求没有列出仪式成本数字

- disposition: `CORE_CORRECTIVE`
- 证据: planning-core-rigorous 要求“计划批准请求须列出上述仪式成本数字”；计划“人工 Gate”（第 199–203 行）没有轮数、报告行数、被评文档行数。可用数字：设计评审 2 轮两轴（ledger schema 2.1.0，`PASS`），报告 75 + 98 = 173 行，设计 227 行；计划 231 行，本轮计划评审报告待合计。
- 影响: 用户批准时看不到治理成本，与“治理要轻”的诉求直接相关，不影响功能。
- 建议: 在计划 Gate 或批准请求消息里补一行仪式成本（设计与计划评审各自的轮数、报告行数、被评文档行数）。

## 覆盖缺口与限制

- 本轴不评审逐任务算法正确性（如 F10 尾部展开细节、`forkHeadHook` 去 Prompt 的匹配方式），归 Spec/Executability 轴。
- 代码事实只在 candidate `00c890f7` 上静态核对，没有编译或运行测试。
- T9 主机脚本 `tmp/xhen-p1/wt-repo.ps1`、`host-merge.ps1` 未读，只核对了计划对它们的使用约束。

## 结论

BLOCKED：PS1 为开放 `CORE_BLOCKING`（两项验收断言没有实施任务承载）。PS2–PS5 为 `CORE_CORRECTIVE`，其中 PS2 标 `user_visible_effect: true`，建议与 PS1 同一次修订处理。

## 剩余风险与后续

- PS1 的 kill 通知与现有“kill 不发通知”的设计理由冲突；若作者选择偏离设计，要先按 SR1401 请用户确认。
- 修订后只需复核 T1–T8 中改动的任务块、可追溯性表与人工 Gate 一节。
- 实施时注意 `internal/wiring/runtime.go`（964 行）同时承接 T3 绑定与 PS3 的注入，接近 GR104 上限。
