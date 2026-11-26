<!-- template_id: review; template_version: 1.2.0 -->
# suga 记忆可信开关实施计划：Spec/Executability 评审（第 3 轮，闭合确认）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `6f8e39e7`；subject `docs/plans/2026-11-26-trusted-memory-backend-plan.md`；document revision Draft 0.3（上一轮 `428f7d2c` / Draft 0.2）
- 修订版本: Draft 0.3
- 评审者: Spec/Executability 轴的独立评审子 agent（Claude），未参与计划编写；REPORT_ONLY
- 日期: 2026-11-26
- 轮次依据: 用户原话“7s4v Q7 选 A”同意开第 3 轮
- 排除项: Standards/Governance 轴；设计决策；T4 真实复现、MF6 修订、新增阻断以外的范围

## 输入与方法

- 读取：review-contract payload（`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`）中的评审合同与轮次规则；第 2 轮两份报告 `docs/review/2026-11-26-trusted-memory-backend-plan/{spec,standards}-review.md`；`git diff 428f7d2c..6f8e39e7 -- <subject>`。
- 对照代码：`internal/hooks/hooks.go`（`New`、`newCompatClient`、`PreTool`、`call`）、`internal/chat/hooks_gate.go`（`toolGate.Rewrite`）、`internal/chat/chat.go:230-237`、`internal/cli/chat.go:22,154`、`internal/cli/run.go:275,294,309-316`、`internal/wiring/runtime.go:186-190,300-336`、`internal/wiring/runtime_methods.go:86-90`、`internal/wiring/localenv.go:187-193`、`internal/wiring/approval_policy.go:64-89`、`internal/wiring/approval_mode.go:43-53`、`internal/wiring/origin_guard.go:120,155-158`、`internal/personal/observe.go:317,419`、`pkg/agentcore/tools/native/code_exec.go:493-501,572-633`、`internal/wiring/memory.go:222-240`、`internal/wiring/chat_approver_test.go:57-61`、`scripts/acpprobe/acp_probe.py`；用户文档 `docs/usage/gofer-personal-agent.md:250`、`README.md:120`。
- 方法：读代码，按 T4 原文推演 `suga run` 的装配路径与 trace 断言；未运行测试。

## 验收标准

- T4 真实复现能照做，结果唯一：hook 在 `suga run` 下生效、非零退出即拒绝、trace 有可断言的 hook 事件；`--allow-write --max-steps 6` 能暴露 `shell_exec`；两组断言能从 `inspect trace --json` 取到；凭据注入安全。
- MF6 修订正确。
- 没有新的 CORE_BLOCKING 或矛盾；CORE_BLOCKING 只限五类，并附 necessity proof。

## 发现

### MF8 [BLOCKER] `suga run` 不装配 PreToolUse hook，T4 的“只放行一条 echo”不成立；hook 事件断言必定失败，登记组实际上不受限

- disposition: CORE_BLOCKING
- 类别: [BUG]（T4 真实复现按原文不可执行，结果恒为失败）+ [SECURITY]/[AUTHORITY]（人工 Gate 第 4 条披露的执行边界与代码不符）
- 证据:
  - PreToolUse gate 只在 chat 里安装：`sugahooks.New` 的唯一前端调用点是 `internal/cli/chat.go:22`，gate 经 `chat.go:235-236` `opts.SetCallRewriter(newToolGate(...))` 装进审批层，`SetCallRewriter` 只由 `internal/cli/chat.go:154` 传入。`internal/cli/run.go` 中没有 hooks 的构造，也没有调用 `SetCallRewriter`。`OpenCLI` 把 agentcore 的 `opts.Hooks` 置为 nil（`runtime.go:325`）。`localenv.go:193` 的 `hooks.New()` 只用于环境报告。用户文档也把它写成 “chat hooks”（`docs/usage/gofer-personal-agent.md:250`、`README.md:120`）。
  - 所以在 `suga run` 下，`hooks.command` 与 `hooks.pre_tool_use.tools` 都不生效：`allow-echo.ps1` 从不运行，也不会产生“非零退出即拒绝”；`recordHookRun`（`hooks_gate.go:63`）不会被调用，trace 里没有 PreToolUse 的 `hook` 事件。
  - 计划自己写了“trace 须有 PreToolUse 的 `hook` 事件，否则判失败”。照做的话两组都必定判失败，完成定义 5 中“主机 gofer 后端真实复现”无法达成；`UNVERIFIED_REAL_ENV` 的两条出口都不覆盖这种情况。
  - 这条断言只能事后发现问题。登记组的 `run --allow-write` 先跑完：run 的 native 默认是 Allow（`approval_policy.go:84-86`；`--approve ask` 不改 run 的 native 默认，`approval_mode.go:43-53`），登记组又不经过 originGuard。真实模型在 `max-steps 6` 内发出的任何 `shell_exec` 都会无人确认直接执行，cwd 在 `tmp/suga-7s4v/ws`，没有沙箱。
  - 凭据方面：MS7 的做法把 provider 引用的密钥变量设进 suga 进程环境；`shellEnv` 只删 `SUGA_HOME/SUGA_CONFIG/SUGA_MODEL` 和会话变量（`code_exec.go:572,578,497`），密钥变量会原样传给 `shell_exec` 子进程。hook 不生效时，模型发出的 `env` / `$env:XXX` 一类命令能读到密钥。trace 与输出只做模式匹配脱敏（`code_exec.go:529,535`，`observe.go:419`），不保证能覆盖。第 2 轮报告写的“`shell_exec` 子进程环境会经 `dropEnv(..., exportedEnv)` 过滤”不准确：`exportedEnv` 只含会话变量。
  - 人工 Gate 第 4 条与 `host_or_non_offline_action` 向用户披露的是“其余 shell 被 hook 拒绝”，这个边界在 run 下不存在；用户会按错误的边界批准计划。
- necessary_causal_chain: 照 T4 原文实施 → `suga run --allow-write --max-steps 6` 不加载 hook → 登记组里真实模型发出的所有 `shell_exec` 都无人确认执行（环境里带 provider 密钥）→ 事后检查 trace 没有 PreToolUse `hook` 事件，两组都判失败 → 完成定义 5 的主机真实复现无法通过；用户按 Gate 4 批准的“只放行一条 echo”的边界实际没有生效。
- 已核对、确认无问题的部分（hook 本身）：`call` 在非零退出、超时或输出无法解析时返回 error（`hooks.go:441-466`），`Rewrite` 收到 error 就拒绝（`hooks_gate.go:64-66`），说明 hook 机制本身是失败关闭的；事件 JSON 经 stdin 传入，`tool_input` 带有 `command` 参数，所以在 chat 下用 `pwsh -File` 读 stdin 可以取到该值。这些只在 chat 前端成立。
- 建议（任选其一，由作者决定）:
  - A：登记组不让真实模型执行 shell。断言只看“`memory get` 结果无 `external`、会话无 `tainted_at`、照抄的调用没有被 originGuard 标记”。例如只给只读工具，或照抄目标改为非执行类工具；对照组保留 run 下 originGuard 的拒绝（这部分照常成立，见下）。
  - B：如果坚持做真实执行，就换一个确实存在的硬边界：用 chat 前端（PTY 驱动，hook 在 chat 下生效），或者先在代码里给 run 装配 PreToolUse gate。后者属于范围外的接口变化，需要你另行批准。
  - 不论选哪条，provider 密钥都不能进入 `shell_exec` 能读到的环境，或者登记组根本不暴露 shell。同时更正 Gate 第 4 条与 `host_or_non_offline_action` 的披露。

### T4 其余核对项（无新发现）

- `--allow-write`（`run.go:275`）与 `--max-steps`（`run.go:294`，须 ≥1，`run.go:364`）都存在。`--allow-write` 会让 `opts.AllowWrite` 为真（`runtime.go:316`），从而暴露 `shell_exec`。删掉 `tools.approval` 是正确的。
- 对照组：run 下 `g.chat == nil` 时命中即拒绝（`origin_guard.go:155-158`），拒绝原因是 `参数里有逐字复制自 memory 结果的文本：…`（`origin_guard.go:120`），会作为 `denial` 事件的 `Error` 写入 trace（`observe.go:317`）。原因含“逐字复制自 memory 结果”的断言可以从 `inspect trace --json` 取到。前提是 MF8 去掉 hook 事件这条前置断言，或者换成有效的边界。
- 登记组的 `shell_exec` `tool` 事件带 `command` 属性（`observe.go:419`，截断 500 字符，并做脱敏）。固定 echo 不触发脱敏，所以“逐字等于该 echo”可以断言。
- 凭据读取方式与 `acp_probe.py:33-43,266-271` 一致（进程内读 `.env`，不落盘、不经命令行参数）；本身没有问题，风险只在 MF8 所述的 shell 子进程继承。

### MF6 闭合确认 —— 已闭合

- 工作区只用 `gkconfig.Set("workspace", …)`，并注明 `OpenCLI` 会覆盖 `Options.Workspace`，与 `runtime.go:311` 一致。
- ⑥补了 `memory.user.command: os.Args[0]`，与 `newExecStore("user", cfg.UserExec)`（`memory.go:232`）的要求一致。
- `asker := &scriptedAsker{answers: []string{"n"}}`，提问数取 `len(asker.questions)`，与 `chat_approver_test.go:57-61` 的类型一致。
- `selectUserBackend` 的赋值放在 switch 之前，与 `memory.go:230-233` exec 分支提前 return 的情况相符。
- “每个子用例独立一次回合”比原来的“按回合计”更明确，没有矛盾。

## 覆盖缺口与限制

- 没有运行测试，也没有实际跑 `suga run` 去确认 trace 里缺少 hook 事件，结论来自读代码（装配点已穷尽检索：`hooks.New(`、`SetCallRewriter`、`GateOf(` 的全部非测试调用点）。
- 没有复核 T1–T3 中与 MF6 无关的部分，也没有复核 Standards 轴的 MS5–MS7（只核对了它们在 T4 文本中对 Spec 轴的影响）。

## 结论

BLOCKED：MF8（CORE_BLOCKING，[BUG]+[SECURITY]/[AUTHORITY]）。T4 用来限定执行的 PreToolUse hook 在 `suga run` 下不装配，计划要求的 hook 事件断言必定失败，登记组的 shell 执行实际上不受限，Gate 披露的边界也与代码不符。MF6 已闭合。

## 剩余风险与后续

- 按轮次规则，确认轮仍有阻断项，需要把 MF8 交用户决定（继续下一轮需你批准，或按 scope-reset 处理），决定记入计划修订记录并在 Gate 披露。
- 不论选哪种修法，T4 里都不应同时出现“真实模型可以无人确认执行 shell”和“provider 密钥在 shell 子进程环境里”。
