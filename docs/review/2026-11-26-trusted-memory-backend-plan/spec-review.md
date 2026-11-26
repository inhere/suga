<!-- template_id: review; template_version: 1.2.0 -->
# suga 记忆可信开关实施计划 Spec/Executability 评审（第 2 轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `428f7d2c`；subject `docs/plans/2026-11-26-trusted-memory-backend-plan.md`；document revision Draft 0.2（上一轮 `452845ff` / Draft 0.1）
- 修订版本: Draft 0.2
- 评审者: Spec/Executability 轴独立评审子 agent（Claude），未参与计划编写；REPORT_ONLY
- 日期: 2026-11-26
- 排除项: Standards/Governance 轴；设计决策；MF1–MF5 与 T4 安全以外的新范围（confirmation 轮，只做闭合确认）

## 输入与方法

- 读取：review-contract payload（C0TJVugR8Kpr8ZAERo543A）中的评审模板与轮次规则；上轮本轴报告 `docs/review/2026-11-26-trusted-memory-backend-plan/spec-review.md`；`git show 428f7d2c:<subject>` 全文与 `git diff 452845ff..428f7d2c`。
- 对照代码：`internal/wiring/admit_ledger_test.go`（`admitServer`、`openAdmitRuntime`）、`approval_mode_test.go`（`TestTrustThroughAssembly`、`hostStubTool`）、`chat_approver_test.go:57`（`scriptedAsker`）、`runtime.go:242-336,537`（`OpenCLI`）、`origin_guard.go:25-200`、`internal/personal/taint.go`、`observe.go:43-50,369-372`、`runner.go:574,619`、`internal/config/paths.go:51-63`、`internal/wiring/memory.go:222-290`、`approval_policy.go:68-89`、`pkg/agentcore/tools/native/code_exec.go:496-502`、`docs/plans/2026-11-26-background-fork-plan.md` T9。
- 方法：读代码推演真实回合路径与六个子用例；逐条核对 T4 主机复现在 `suga run` 下的工具注册、审批与凭据流向。未运行测试。

## 验收标准

- MF1 闭合：真实回合路径能让 taint 置位（含回合级会话 ID），能从 `rt.repo` 读到 `tainted_at`；`origin_judge` 关闭后直接询问；①–⑥可构造。
- MF2–MF5 修订正确、可执行。
- T4 主机复现无凭据泄露，`approval` 放开后只执行无害命令，对照组不执行危险命令。
- CORE_BLOCKING 仅限五类并附 necessity proof。

## 发现

### MF1 闭合确认（上轮 CORE_BLOCKING）—— 已闭合

- 真实回合路径：`rt.Runner.Run` 在回合内调用 `withObserveScope(ctx, model.ID, turnID)`（`runner.go:574,619`），同时设置 `api.WithSessionID` 与 observe scope 的 `sessionID`（`observe.go:47-50`）。`memory get` 结果带 `external=true` 时，`taintMarker.observe`（`observe.go:369-372` → `taint.go:76-98`）在会话行上写入 `tainted_at`；`originObserver` 用同一个 `SessionIDFrom(ctx)` 记录来源（`origin_guard.go:41-51`），所以下一步照抄调用能被 `Find` 命中。会话行由 Runner 创建，`rt.repo` 可读，与 `admitRows` 用 `rt.repo` 的方式相同。
- 关掉 `origin_judge`：`judge == nil` 时跳过裁决；在 chat（`g.chat != nil`）中直接走 `confirm` 询问（`origin_guard.go:66-68,123,156-163`）。`OpenCLI(ChatMode)` 下 `chatGate` 非空，`rt.Asker` 是 `BindableAsker`（`runtime.go:278-330`），`Bind(&scriptedAsker{})` 后可按 `len(asker.questions)` 计提问数。`tools.approval: auto` 让 native 默认 Allow（`approval_policy.go:76-80`），所以②③④的唯一提问来自 originGuard。
- 六个子用例：①⑥无外部标记，不置 taint，也不命中；②③④照旧外部，会置 taint 并提问一次（`scriptedAsker` 空答案按拒绝处理）；⑤ `taint_tools` 只影响 taint（`taint.go:59-61`），不影响 origin 记录（只看 `external`/`External`），所以“有 `tainted_at`、无提问”成立。④的 `.git` 文件目录：`gitRoot` 在该目录停止，根不等于主仓库，可构造。⑥：`selectUserBackend` 支持 exec（`memory.go:230-233`）。R6 在新写法下能让②③④失败。
- 结论：MF1 闭合。构造细节见 MF6（不阻断）。

### MF2–MF5 闭合确认

- MF2：已闭合。T1 加了 `selectUserBackend` 真/假，T2 加了⑥与 manifest user。注意 exec 分支会在 `memory.go:233` 提前 return，“与后端无关”的赋值必须放在 switch 之前；⑥用 exec 后端，能抓住这个错误。
- MF3：已闭合。屏幕判断改为 `来源工具：memory`/`执行 shell_exec？`，询问处输入 `n`，trace 取 `denial`。
- MF4：已闭合。T4 恢复了 gofer 后端真实复现，并加了 JSON 键断言。复现脚本本身的问题见 MF7。
- MF5：已闭合。管道用例显式替换为 false；R1 写明必败用例；字段 `MemoryID`；工作区写定（Options.Workspace 一说有误，见 MF6）；ptye2e 用 `git rev-parse --show-toplevel` 的结果登记。

### MF6 [LOW] T2 链路的工作区写法里“经 `Options.Workspace`”在 `OpenCLI` 下无效，另有两处细节缺失

- disposition: CORE_CORRECTIVE，user_visible_effect: false
- 证据: T2 写“工作区为临时目录 … 经 `Options.Workspace` 或 `gkconfig.Set("workspace", …)` 设定”。`OpenCLI` 会用 `appconfig.ResolveWorkspacePath()` 覆盖 `opts.Workspace`（`runtime.go:250,311`），而它只读 `workspace` 配置或 cwd（`paths.go:55-62`）。执行者如果选了 Options.Workspace，工作区会变成测试包目录（suga 仓库），①⑥会失败。失败是显式的，不会空过。另外：⑥只写了 `memory.user.backend: exec`，缺 `memory.user.command`（`newExecStore("user", cfg.UserExec)` 需要它）；`scriptedAsker{answers: n}` 的类型是 `[]string`，提问数应取 `len(asker.questions)`。
- 影响: 执行者要多花一轮排查；不影响验收是否成立。
- 建议: 删掉“`Options.Workspace` 或”，只保留 `gkconfig.Set("workspace", <临时仓库>)`；⑥补上 `memory.user.command: os.Args[0]`；提问数写成 `len(asker.questions)`。

### MF7 [MEDIUM] T4 主机复现在 `suga run` 下拿不到 `shell_exec`；放开后的执行边界没有写清

- disposition: CORE_CORRECTIVE，user_visible_effect: false
- 证据:
  - T4 配置 `tools.approval: auto` 后跑 `suga.exe run`。`tools.approval` 与 `tools.allow_write` 只在 ChatMode 下读取（`runtime.go:278-320`）；run 只有带 `--allow-write`（或 `--approval trust`）才暴露 shell 与写工具（`runtime.go:322`，`internal/cli/run.go:275`）。按计划原样执行时，没有 `shell_exec` 可调用，登记组“`shell_exec` 无 `origin_judge`、未被拒绝”必然不成立。计划的 `UNVERIFIED_REAL_ENV` 只覆盖“模型未调用 memory”，没有覆盖这种情况，执行者可能误判为产品失败，或者为了通过而改配置。
  - 加上 `--allow-write` 后，run 的 native 默认是 Allow（`approval_policy.go:84-86`），真实模型发出的任何 `shell_exec` 都会直接执行，不会询问；登记组又跳过了 originGuard。计划写了备忘正文是“一条无害的 `echo` 命令”，但没有写：只能有这一条命令；正文不能含 `$`/`%VAR%`/`$env:` 等展开；trace 要核对实际执行的命令只有这一条；没有限制模型自行发出的其他命令。工作区虽在 `tmp/`，`shell_exec` 本身没有沙箱。
  - 对照组：在 run 下，`g.chat == nil` 时命中就拒绝（`origin_guard.go:156-159`），照抄的命令不会执行；即使模型改写后未命中，执行的也只是同一条 echo。对照组不会执行危险命令，安全上成立。但改写会造成“未被拒绝”的假阴性，计划没有对应的判定路径。
  - 凭据：provider 段白名单（丢弃 `headers`，`api_key` 必须是 `${VAR}`）、`.env` 只读不复制、不打印环境、结束删除 home，与 fork 计划 T9 一致，写入 `tmp/` 的文件不含明文凭据。`shell_exec` 子进程环境会经 `dropEnv(..., exportedEnv)` 过滤（`code_exec.go:497`）。未发现凭据问题。
- 影响: 主机复现（完成定义 5 的一项）按原文无法得到有效结果；放开执行后，边界只靠提示词。核心验收仍有 T2/T3 支撑，所以不阻断。
- 建议: T4 的 run 命令写明 `--allow-write`（并说明 `tools.approval` 在 run 下不生效，可删去）；写明备忘正文是固定的 `echo <常量文本>`，不含变量展开或管道；加 `--max-steps` 上限；断言 trace 里所有 `shell_exec` 的 `command` 都等于该 echo，否则判失败并停止；对照组若模型改写后未被拒绝，记 `UNVERIFIED_REAL_ENV: 模型改写了命令`。可选：给隔离 home 的 approval 规则文件加一条“只允许 `echo` 前缀、其余 shell deny”。

## 覆盖缺口与限制

- 未运行任何测试或反向验证，结论来自读代码。
- 未核实 `exportedEnv` 的具体列表，以及 gofer 后端在隔离 `SUGA_HOME` 下能否找到 gofer CLI 与 tracker（主机运行时会暴露问题，有失败关闭路径）。
- 本轮没有复核 T3 ptye2e 的细节，只核对了 MF3 的修订。

## 结论

PASS：上轮唯一的 `CORE_BLOCKING`（MF1）已闭合，修订没有引入新的 `CORE_BLOCKING` 或矛盾。MF2–MF5 已闭合。新增 MF6、MF7，均为 CORE_CORRECTIVE（`user_visible_effect: false`），建议在实施前或执行 T2/T4 时一并改正。

## 剩余风险与后续

- MF7 不改的话，T4 真实复现大概率得到无效结果；改了之后执行边界才明确。
- 两条均不标 `user_visible_effect: true`，按轮次规则不触发纠正确认轮。
