<!-- template_id: review; template_version: 1.2.0 -->
# suga 记忆可信开关实施计划 Standards/Governance 轴第 3 轮 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `6f8e39e7`；subject `docs/plans/2026-11-26-trusted-memory-backend-plan.md`；document revision Draft 0.3。上轮 candidate 为 `428f7d2c`（Draft 0.2，本轴 BLOCKED：MS5）
- 修订版本: 0.3
- 评审者: Standards/Governance 轴独立评审子 agent。不是计划作者；第 3 轮闭合确认，用户已同意开这一轮（“7s4v Q7 选 A”）
- 日期: 2026-11-26
- 排除项: 只评三项：MS5–MS7 是否关闭；用 PreToolUse hook 代替审批规则文件，算不算合理偏离；Gate 是否完整。Spec/Executability 轴其余内容不评。只报影响核心验收或安全的问题。不改仓库、不提交。

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`，看了 review 合同的五类 `CORE_BLOCKING` 与 necessity proof、Review Candidate Loop 的第 3 轮及以后规则、计划批准请求的仪式成本要求、review 模板。
- 上轮本轴报告：`docs/review/2026-11-26-trusted-memory-backend-plan/standards-review.md`（`316d82f9`，MS5–MS7）。
- `git diff 428f7d2c..6f8e39e7 -- docs/plans/`。
- 核对的代码事实：
  - `internal/cli/chat.go:22`、`:154`：hook 客户端 `sugahooks.New` 和 `SetCallRewriter` 只在 `runChat` 里装配。
  - `internal/chat/chat.go:231-237`：PreToolUse gate 只在 chat 循环里装到审批层，hook 的 trace 事件也由 gate 自己记录。
  - `internal/cli/run.go`：没有任何 hooks 引用。
  - `internal/wiring/runtime.go:188-190`：注释写明“chat hands it the PreToolUse hook”。
  - `internal/wiring/localenv.go:185-198`：只用于 `inspect env` 展示。
  - `docs/design/2026-11-12-multi-command-hooks-design.md:79`、`:109`：明文“不给 ACP、`suga run`、SDK 增加 hooks”。
  - `docs/usage/gofer-personal-agent.md:250`：称作“chat hooks”。
  - `pkg/sugasdk/doc.go:94`。
  - `internal/wiring/origin_guard.go:120`：拒绝原因格式为“参数里有逐字复制自 %s 结果的文本”。
  - `internal/cli/run.go:294`：有 `--max-steps`。
  - 行数：计划 0.2 为 193 行，0.3 为 204 行；第 2 轮报告 spec 72 行、standards 69 行。

## 验收标准

- 上轮 MS5–MS7 的建议。
- SR1301 安全清单。
- `host_or_non_offline_action` 和人工 Gate 要如实披露主机动作。
- 偏离监督者建议时写明理由，且不超出 scope freeze。
- 计划批准请求要列出仪式成本数字，第 3 轮要有授权引用。

| 项 | 结论 |
|---|---|
| MS5（`--allow-write`） | 原问题已修：两组都用 `run --allow-write --max-steps 6`，删了对 run 无效的 `tools.approval`；登记组前置断言 trace 里有 `shell_exec` 的 `tool` 事件；对照组断言拒绝原因含“逐字复制自 memory 结果”，与 `origin_guard.go:120` 的格式一致；模型改写命令时记 `UNVERIFIED_REAL_ENV`。不过同一修订新加了一个前提：要求 trace 有 PreToolUse 的 `hook` 事件，这个前提在 run 下不成立，见 MS8。 |
| MS6（主机动作披露） | 文字已补：`host_or_non_offline_action` 和 Gate 4 都写了“无人确认执行一条固定 `echo`、步数上限 6”。但“其余 shell 被 hook 拒绝”这句不成立，披露与实际行为不符，见 MS8。 |
| MS7（`.env` 注入） | 已关闭。照 `acp_probe.py` 的做法，在脚本进程内读 `.env`，只注入 provider 段引用的变量；不落盘、不经命令行参数、不回显。 |
| 偏离（用 hook 代替规则文件） | 理由写了：修订记录 0.3 一行，T4 括注说明规则文件做不到“只放行一条”（deny 恒为最强、文件没有默认项、run 的 native 默认恒为 allow）。范围上也合规：hook 脚本只在 `tmp/suga-7s4v/` 里，配置写在隔离 home，不改产品代码，结束后删除。按 scope freeze 看，这是验证脚本内部的手段，偏离本身合理。但这个手段在 `suga run` 里不生效，见 MS8。规则文件那三条理由本轮没有逐条核对代码。 |
| Gate 完整性 | 仪式成本：第 2 轮“69 + 72 = 141 行，计划 0.2 为 193 行”与仓库一致，第 3 轮授权原话已引用。主机动作：echo、无人确认、步数上限都已列出。“hook 拒绝其余 shell”不实，见 MS8。 |

## 发现

### MS8 [HIGH] PreToolUse hook 只在 `suga chat` 装配，`suga run` 不发送也不执行；T4 的“只放行一条 echo”限制不存在，Gate 4 的安全披露不实，登记组也必然失败

- disposition: `CORE_BLOCKING`；类别 `BUG`（核心验收照写不可执行）。它同时带 `AUTHORITY` 的一面：用户依据一条不成立的安全限制批准无人确认的主机 shell 执行。
- 证据:
  - hook 装配只在 chat 里：
    - `internal/cli/chat.go:22` 调 `sugahooks.New`，`:154` 传 `SetCallRewriter`。
    - `internal/chat/chat.go:235-236` 只在 chat 循环里把 gate 装到审批层。
    - `internal/cli/run.go` 没有任何 hooks 引用。
  - 设计文档也是这样定的：`docs/design/2026-11-12-multi-command-hooks-design.md:79` 写明“不给 ACP、`suga run`、SDK 增加 hooks”；用法文档 `gofer-personal-agent.md:250` 称作“chat hooks”。
  - 计划 T4 依赖的写法有三处：
    - 两组都跑 `suga.exe run --allow-write --max-steps 6`，靠 `hooks.command` 加 `hooks.pre_tool_use.tools: [shell_exec]` 来做到“其余 shell 被拒绝”。
    - “trace 须有 PreToolUse 的 `hook` 事件，否则判失败（hook 未生效即失败关闭）”。
    - Gate 4 和 `host_or_non_offline_action` 写着“其余 shell 被 hook 拒绝”。
  - 同类穷尽：本计划只有 T4 一处用 hook。T1–T3 走 `OpenCLI(ChatMode)` 或 ptye2e chat，不受影响。
- necessity proof:
  - `core_acceptance`: 完成定义 5 和 T4 验证“真实复现登记组未拒绝、对照组被拒绝，或写 `UNVERIFIED_REAL_ENV`”；人工 Gate 4 的安全披露。
  - `necessary_causal_chain`: 照计划写 `realenv.ps1` → `suga.exe run` 不加载 hooks，PreToolUse 从不发送 → 模型的每次 `shell_exec` 都在主机 `tmp/suga-7s4v/ws` 里无人确认执行。登记组 trusted，originGuard 不拦。对照组只拦逐字复制的那一条。最多 6 步，任意命令都会执行。“只等于该 echo”的检查是跑完后看 trace，命令已经执行过了 → trace 里没有 `hook` 事件，按计划判失败。这个结果是确定的，不取决于模型怎么回答，也不属于计划允许写 `UNVERIFIED_REAL_ENV` 的两种情况 → 完成定义 5 无法完成。用户在 Gate 4 上依据“其余 shell 被 hook 拒绝”批准的限制，实际不存在。
  - `minimal_owner`: 计划 T4 的“主机真实复现”一项、`host_or_non_offline_action` 和人工 Gate 第 4 条。只改计划文本，不改产品。
  - `deletion_alternative`: `PASS`。删掉 hook 机制和“须有 hook 事件”这条断言，再把披露改成如实描述，就能关闭阻断，不需要新机制。
  - `scope_effect`: 无新增 Module、Interface、Schema、state、lifecycle 或 authority。外部动作的披露范围要按实际改大。
- 影响: 照计划执行会出现两个结果。第一，真实复现每次都判失败，完成定义 5 拿不到证据。第二，执行前用户以为 shell 只限一条 echo，实际是 6 步之内任意命令都无人确认执行。工作目录在 `tmp/`，provider 是用户自己的，备忘正文是常量 echo，实际风险有限；但 Gate 上的安全说法是错的。
- 建议（按删除优先，作者核实后选一种）:
  1. 删掉 hook。删去 `hooks.*` 配置、`allow-echo.ps1` 和“须有 `hook` 事件”这条断言。保留“所有 `shell_exec` 的 `command` 逐字等于该 echo，否则判失败”，并注明这是事后检查。Gate 4 和 `host_or_non_offline_action` 如实改为：“T4 两组以 `--allow-write --max-steps 6` 让真实模型在 `tmp/suga-7s4v/ws` 内无人确认执行 shell，命令不受限制（预期只有一条 `echo`），不经 hook 或规则限制”。由用户在 Gate 上决定是否接受。
  2. 如果一定要在执行前限制命令，改走 chat：hook 在 chat 里生效。这需要作者先核实非 TTY 或 ptye2e 驱动下的真实 provider chat 可行，而且对照组在 asker 未绑定时一律拒绝，要重新确认“拒绝来自 originGuard”的断言还成立。这种做法改动更大，不建议在本轮引入。
  3. 不建议改回审批规则文件：计划给出的三条理由说明它也做不到“只放行一条”。

## 覆盖缺口与限制

- 规则文件做不到“只放行一条”的三条理由（`internal/config/approval_rules.go`、`internal/wiring/approval_policy.go`）没有逐条核对代码。这不影响 MS8 的结论。
- 没有核对 `--max-steps 6` 能否容纳“memory get → shell_exec → 结束”这个序列；没有核对 trace 里 `tool` 和 `denial` 事件的字段名，也没有核对 originGuard 拒绝时 `hit.Tool` 恰好是 `memory`。这些属于 Spec 轴。
- 计划评审目录仍然没有 review ledger。第 3 轮授权应写入 `continuation_approvals`（`round = 3`），这个问题沿用上轮“覆盖缺口”的提示，本轮不另记 finding。
- 没有运行 validator 或任何测试。

## 结论

BLOCKED。MS7 已关闭。MS5 原来的问题（缺 `--allow-write`、`tools.approval` 无效、断言不足）已经修好，MS6 的披露文字也补上了。但为 MS6 新加的 PreToolUse hook 在 `suga run` 里不生效，于是出现 MS8（`CORE_BLOCKING`，`BUG`，兼 `AUTHORITY`）：Gate 4 的“其余 shell 被 hook 拒绝”不成立，“须有 hook 事件”让登记组必然判失败。偏离本身有理由，也在验证脚本范围内，但它的前提不成立。

## 剩余风险与后续

- 按 planning-core-rigorous，第 3 轮结束时仍有开放的 `CORE_BLOCKING`，不得自动进入第 4 轮。作者先向用户列出 MS8 的一句话场景、再评一轮能防住什么、仪式成本，由用户三选一：再评一轮、scope-reset 或接受风险。MS8 的修法 1 只是删掉一项机制、改一处披露文字；如果用户选再评一轮，只需对 T4 和 Gate 第 4 条做闭合确认。
- 发出 Gate 时补上第 3 轮的报告行数，以及计划 0.3 的行数（204 行），修订后则用新版本的行数。
- 修法 1 下的披露要写明“命令不受限制，只做事后检查”。用户可能因此要求换成 chat 方案，或者去掉真实执行。这需要用户在 Gate 上决定，不属于评审范围。
