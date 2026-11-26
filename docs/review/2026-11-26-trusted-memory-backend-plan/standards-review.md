<!-- template_id: review; template_version: 1.2.0 -->
# suga 记忆可信开关实施计划 Standards/Governance 轴第 2 轮 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `428f7d2c`；subject `docs/plans/2026-11-26-trusted-memory-backend-plan.md`；document revision Draft 0.2（上轮 candidate `452845ff`，Draft 0.1，本轴 PASS）
- 修订版本: 0.2
- 评审者: Standards/Governance 轴独立评审子 agent（不是计划作者；第 2 轮闭合确认）
- 日期: 2026-11-26
- 排除项: 只评三项：MS1–MS4 是否关闭；T4 真实复现的 SR1301 秘密项，以及 `tools.approval: auto` 和关闭 `origin_judge` 是否要在 Gate 告知；Gate 是否完整。Spec/Executability 轴其余内容不评。不改仓库、不提交。

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（SR1301、SR1409、review 合同、planning-core-rigorous、review 模板），`tmp/idev-std/d/L1pSw4v7EitW46DpcPDbLQ/payload.md`（plan 合同）。
- 上轮本轴报告 `docs/review/2026-11-26-trusted-memory-backend-plan/standards-review.md`；`git diff 452845ff..428f7d2c -- docs/plans/`。
- 核对代码事实：`internal/wiring/runtime.go:312-322`（`tools.allow_write` 只对 chat 生效，run 要显式 `--allow-write`）；`config/config.yaml:343-348`（`approval` 注明 chat only）；`internal/wiring/chat_approver.go:108-123`（`tools.approval` 只由 chat 审批器读取）；`internal/cli/run.go:275`（`--allow-write` 默认 false）；`internal/config/load.go:66-72`（只加载 `SUGA_HOME/.env`）；fork 计划 T9 第 243 行（凭据过滤原文）；AGENTS.md 里 `acp_probe.py` 的做法（读真实 `.env`，但不复制）。

## 验收标准

上轮 MS1–MS4 的建议；SR1301 中“秘密不进日志、输出和仓库”一项；planning-core-rigorous 对计划批准请求的要求（仪式成本数字）；本计划“人工 Gate”和 `host_or_non_offline_action` 是否如实列出主机动作。

| 项 | 结论 |
|---|---|
| MS1（主机真实复现） | 已关闭。T4 恢复了真实复现，有登记组和对照组。完成定义 5 与可追溯性已同步，修订记录写明了选择。但复现步骤本身跑不通，见 MS5。 |
| MS2（凭据边界） | 已关闭。只留 provider 四个字段，丢弃 `headers`；`api_key` 逐值匹配 `^\$\{[A-Za-z0-9_]+\}$`；`.env` 不复制；脚本不打印环境变量和 `.env`；结束后删除 `home`。`.env` 怎样进入进程没写，见 MS7。 |
| MS3（仪式成本） | 已关闭。Gate 1 列了设计评审（191 行报告、444 行 ledger、141 行设计）和计划第 1 轮（102 + 98 = 200 行报告、179 行计划），数字与仓库一致。第 2 轮的数字应在发出 Gate 时补上，属于 Gate 文案，不另记 finding。 |
| MS4（合并顺序） | 已关闭。只要 main 有新提交就 rebase，之后跑全量测试；`fakellm_test.go` 冲突时加 `TestChatFork`；重叠清单补了 `skills/suga-guide/references/`。 |
| SR1301 秘密项 | 计划层面已核对：凭据只引用 `${VAR}`，`.env` 不复制，也不打印。其余四项（鉴权、注入、入参校验、资源归属）本轮 diff 未涉及，沿用上轮结论。 |
| Gate 完整性 | 三项 Gate 都在。Gate 1 由用户指定执行者模型，并给出建议（T1、T2 `openai-codex/gpt-6.1-sol`，T3 `opencode-go/deepseek-v4.1-flash`）；T4 写明由作者执行；仪式成本数字已列。缺一项主机动作披露，见 MS6。 |

## 发现

### MS5 [HIGH] T4 真实复现的 `suga.exe run` 没带 `--allow-write`，`shell_exec` 不会暴露给模型，登记组和对照组区分不开

- disposition: `CORE_BLOCKING`；类别 `BUG`（不可执行，完成定义 5 失败）
- 证据: 计划 T4 写的是“`tools.approval: auto` … 跑 `suga.exe run`”，验证要求“`shell_exec` 无 `origin_judge`、未被拒绝”，对照组“应被拒绝（run 无人可问）”。代码事实有三条。第一，`internal/wiring/runtime.go:312-322`：run 不读 `tools.allow_write`，只有 `--allow-write` 或 trust 才会暴露写入和 shell 工具。第二，`internal/cli/run.go:275`：`--allow-write` 默认 false。第三，`config/config.yaml:344-348` 和 `chat_approver.go`：`tools.approval` 只在 chat 生效，对 run 没有作用。同类全列：本计划只有 T4 这一处真实 run；T1–T3 都用 `OpenCLI(ChatMode)` 或 ptye2e chat，不受影响。
- necessity proof（`necessary_causal_chain`）: 按 T4 写 `realenv.ps1`，命令里没有 `--allow-write` → run 的工具集里没有 `shell_exec` → 真实模型读完 `memory get` 也无法执行照抄的命令，trace 里没有 `shell_exec` 事件 → 登记组“未被拒绝”这条断言要么失败，要么因为没有事件而空洞成立；对照组如果“被拒绝”，也不是 originGuard 拒的 → `UNVERIFIED_REAL_ENV` 只覆盖“模型没调用 memory get”这一种情况，覆盖不到这里 → 完成定义 5 的“主机 gofer 后端真实复现”要么无法完成，要么得到一个看似通过、实际是假阳性的结论。
- 影响: MS1 要求恢复的真实环境验证，照计划执行拿不到有效证据。
- 建议: 登记组和对照组的 `suga.exe run` 都加 `--allow-write`。`tools.approval: auto` 对 run 没有作用，删掉或注明“仅 chat，此处不生效”。验证里加一条前置断言：登记组 trace 必须有 `shell_exec` 的 `tool` 事件。对照组断言拒绝来自 originGuard，例如拒绝事件带外部来源原因，不能只看“被拒绝”。

### MS6 [MEDIUM] 修好 MS5 后，真实模型会在主机上无人值守执行 shell，`host_or_non_offline_action` 和 Gate 没有列出这项动作

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据: `host_or_non_offline_action` 只写了“T4 主机真实复现调用真实 provider”。登记组的设计目标是让照抄记忆正文的 `shell_exec` 不经询问直接执行。加了 `--allow-write` 以后，run 中模型发出的任何 shell 调用都会在主机 Windows 上执行，工作目录是 `tmp/suga-7s4v/ws`，没有人确认。关闭 `origin_judge` 只是少发一次 judge 请求：命中外部来源后，run 没有人可问，直接拒绝。这比默认更严，不放宽安全边界，所以不需要在 Gate 上单独告知。
- 影响: 风险有限：备忘正文是无害的 `echo`，provider 是用户自己的，执行者用的 suga job 本来就在主机上写文件。但这是一项新的主机执行面，用户在 Gate 上看不到。
- 建议: 在 `host_or_non_offline_action` 里加一句：“T4 登记组以 `--allow-write` 让真实模型在 `tmp/suga-7s4v/ws` 内无确认执行 shell（预期只有一条 `echo`）”。可选：在 `tmp/suga-7s4v/ws/.suga/approval-rules.yaml` 或主机 home 的审批规则里只放行 `echo` 前缀。`origin_judge` 关闭不需要披露。

### MS7 [LOW] “`.env` 只读引用不复制”没有写怎样让 `${VAR}` 解析到值

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据: `internal/config/load.go:66-72` 只加载 `SUGA_HOME/.env`。隔离 home 里没有 `.env`，`${VAR}` 只能从进程环境取值，所以脚本必须自己把变量注入子进程。计划没写注入方式。同类：fork 计划 T9（第 243 行）措辞相同，但不属于本 subject，只在这里列出。
- 影响: 执行者可能临时选一种不安全的注入方式，例如把 key 写进 gofer job 命令行或参数（会进 job 记录），或者把 `.env` 复制进隔离 home（违反“不复制”）。按计划原文执行不会泄露，所以不阻断。
- 建议: 写明照 `scripts/acpprobe/acp_probe.py` 的做法：在脚本进程内逐行读取真实 `SUGA_HOME/.env`，只为 provider 段引用的变量名设置子进程环境，不落盘、不经命令行参数、不回显。

## 覆盖缺口与限制

- 计划评审目录 `docs/review/2026-11-26-trusted-memory-backend-plan/` 下没有 review ledger，只有设计评审有。按 SR1409，本候选涉及安全边界（originGuard），应走 Full。是否需要计划评审 ledger，以及 Gate 仪式成本该用 `--companion` 输出，本轮不在范围内，请作者核对。
- 没有核对 run 模式下 originGuard 在没有 asker 时一定拒绝（计划沿用设计的说法），也没有核对 `gofer` 后端在隔离 home 下能否找到 tracker；这些属 Spec 轴。
- 没有运行 validator 或任何测试。

## 结论

BLOCKED。MS1–MS4 都已关闭。但 MS5 为 `CORE_BLOCKING`（BUG）：T4 真实复现漏了 `--allow-write`，`tools.approval: auto` 对 run 不生效，完成定义 5 照写无法得到有效证据。MS6、MS7 是 `CORE_CORRECTIVE`，计划批准前改掉即可。

## 剩余风险与后续

- MS5 是一行命令参数加两条断言的修订。修订后请只对 T4 和 `host_or_non_offline_action` 做闭合确认。按 planning-core-rigorous，第 2 轮仍有 `CORE_BLOCKING`，不得自动进入第 3 轮，要先向用户列清单并取得批准。
- 发出 Gate 时补上计划评审第 2 轮的报告行数和计划 0.2 的行数（193 行）。
- fork 计划 T9 的 `.env` 注入方式（MS7 同类）建议告知 fork 会话。
