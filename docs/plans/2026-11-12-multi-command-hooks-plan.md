<!-- template_id: plan; template_version: 1.3.1 -->
# suga chat hooks 多命令 实施计划

> 状态：Draft 0.1 / 待人工计划批准；issue `suga-5xmf`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-12 | Claude | 初稿：按已批准设计 0.6 拆为 T1–T9 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：落实设计 0.6——`hooks.commands` 新条目（`command` + `timeout_ms`）与 `hooks.command` 兼容条目共存；新条目的 Stop block 开启下一回合；只配 `hooks.command` 时行为、能否启动与现状一致（AC1–AC4）。
- scope freeze：设计 0.6 的 §1–§6、架构与“验证要求”；评审 HS10、HS11 的计划落实。不含未选方案 A 的条目字段、项目级 hook、HS6 的 glob 文案修正、发版与推送。
- 扩围策略：`DEFER_OR_REQUEST`；review budget：最多 8 轮；停止条件：无开放 `CORE_BLOCKING`。
- 暴露度（SR1409）：高（已发布 `v0.55.0`；持久命令执行、gofer Stop 等待链路）；两轴独立评审与 ledger，评审目录 `docs/review/2026-11-12-multi-command-hooks-plan/`。

## 目标与完成定义

完成定义（全部满足）：
1. `hooks.commands` 可在 `config.yaml` 中配置，也可由人在终端经 `suga config set` 写入（protected、`Reader:"hooks"` 校验，非法值退出码 2）；分类表含 `hooks.commands: exec`。
2. 只配 `hooks.command` 时 `hooks.New` 返回现有单条目实现，现有测试除 `hooks_test.go:164`、`:293` 两条零值断言外不改即通过。
3. 有新条目时：SessionStart/UserPromptSubmit/SessionEnd 按编号顺序发给全部条目；PreToolUse/PostToolUse 只发兼容条目；Stop 两阶段与 R0–R3、打断三态与交回规则按设计 §4 实现。
4. trace 每条运行记一条 `kind=hook`，带 `entry`/`program`；只配兼容条目时其余字段与 status 不变。
5. 设计“验证要求”全部有对应测试且通过，含反向验证、ptye2e（Linux 与 Windows ConPTY）、GR110 真实分发。
6. gofer 无 `--wait` 时的核对结论已记录（HS11）；README、`docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/config.md`、CHANGELOG「未发布」已更新。

## 范围、排除项与授权

- 范围：suga 仓库 `internal/config`（新 `hooks_entries.go`、注册表一行、分类表测试一行）、`internal/hooks`（`hooks.go`、新 `pipeline.go` 与测试）、`internal/chat`（`hooks_record.go`、`hooks_gate.go` 与测试）、`internal/cli`（测试）、`test/ptye2e`（新场景）、文档与 CHANGELOG。gofer 仓库只读核对。
- 排除：未选方案 A 的字段；项目级 hook（D13）；HS6 的 glob 错误文案（deferred）；`inspect env` 展示条目；发版、推送、安装、通知 IDEV-STD 以外的使用方。
- 授权：本计划批准只授权进入实施请求；实施需你另行指示。本地提交为默认授权；push、tag、发版、安装需另行授权。
- `host_or_non_offline_action=REQUIRED`：主机上建 worktree（`tmp/xhen-p1/wt-repo.ps1`）、合并到本地 main（`tmp/xhen-p1/host-merge.ps1`）、执行者 `gofer job -a suga` 在主机运行并调用模型 provider（联网、产生费用）、主机 Windows 测试；`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1` 会联网下载模块。均不推送、不发布。

## 输入与批准证据

- 设计：`docs/design/2026-11-12-multi-command-hooks-design.md` Draft 0.6，提交 `da204262` 记录批准。
- 批准原话（2026-11-12）：“批准 suga 多命令 hook 设计 0.6（接受未复核修订），开始写计划”。
- 设计评审：`docs/review/2026-11-12-multi-command-hooks/`（4 轮，ledger PASS）；HS10、HS11 在本计划落实（T1、T5）。
- 前置计划：`docs/plans/2026-11-12-protected-config-keys-plan.md` 0.4（`suga-hyj7`，已批准，实施中）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 新条目解析与 `config set` 校验 | `internal/config/settings_registry.go` Reader 分发（`dispatchConfigReader`）、`mcp.go` `ReadMCPServerSettings` 模式、`hooks.go` `stringList` | 是 | 原位扩展（新 Reader `"hooks"`） | DIRECT_REUSE | none | 无 |
| CAP-02 | 单条命令执行、超时、环境、Interrupt | `internal/hooks/hooks.go` `commandClient.call`、`stopWait`、`hookEnv`、`Option` | 是 | 原位扩展（每条目一个 `commandClient`，加 `Runs`） | DIRECT_REUSE | none | 无 |
| CAP-03 | trace 记录、后台 Stop、PreToolUse 门 | `internal/chat/hooks_record.go` `recordHookRun`、`stop_background.go`、`hooks_gate.go` | 是 | 原位扩展（按 `Runs` 逐条记录） | DIRECT_REUSE | none | 无 |
| CAP-04 | 伪终端端到端与假 hook | `test/ptye2e` `startChatOpts`、`chat_observe_test.go` `TestHookHelper` 模式、假模型 | 是 | 新场景文件 | DIRECT_REUSE | none | 无 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| NONE | none | none |

（新文件 `internal/config/hooks_entries.go`、`internal/hooks/pipeline.go` 都在既有包内，属 owner 扩展；`hooks.go` 已 523 行，流水线放新文件避免逼近 1000 行上限。）

## 前置检查与 fail-closed 条件

- workspace baseline（2026-11-12，计划写作时）：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；branch `main`；HEAD `da204262`；`git status --short` 干净；`suga-hyj7` 在主机 worktree `.worktrees/hyj7`（分支 `feat/protected-config-keys`，HEAD `22482244`，T1–T5 已提交）实施中，未合入 main。实施开始时重新记录 HEAD 与 status。
- **前置条件（fail-closed）**：`suga-hyj7` 已合入本地 main（`git merge-base --is-ancestor 22482244 main` 或其最终合并提交为真，且 `internal/config/settings_protected_test.go` 与 `ConfigKey.Protected` 存在于 main）。不满足时不建 worktree、不开始 T2。
- worktree：hyj7 合入后，在主机建 `gofer job run -p my-tools-dev -a exec --runner local --sync -- pwsh -File tmp/xhen-p1/wt-repo.ps1 -Repo inhere-tools/suga -Name hooks5x -Branch feat/multi-command-hooks`（`.worktrees/hooks5x`，基于当时的 main）。容器内不 `git worktree add/prune`；对 worktree 的 git 命令只用单条前缀 `GIT_DIR`/`GIT_WORK_TREE`，不 export，不与测试同一命令。
- 执行者：T2–T8 由 `gofer job -a suga --model opencode-go/deepseek-v4.1-flash` 在 `.worktrees/hooks5x` 顺序实施（每任务一个 job、≤45 分钟、一个或多个提交），我审查（先看 `git diff --stat` 删除行）与复核反向验证；T1、T9 由我执行。任务书写明：只改本任务列出的文件、不删注释不重排无关代码、不用 web_fetch、可改 go.mod/go.sum（本计划预期不改）、遵守 GR107–GR110 与分层；`--sync`，不起空等后台任务。
- 测试环境：执行者 job 在 Windows 主机运行，无 cgo，任务内 focused 测试不带 `-race`；`-race`、Linux ptye2e、staticcheck 由我在容器里于 T9 跑。所有测试用临时 `SUGA_HOME`，`t.Setenv("SUGA_SESSION_ID", "")`、`t.Setenv("SUGA_HOOKS", "")`（`internal/hooks/main_test.go` 已默认关 hooks 的用法沿用），不读写真实 `~/.suga`；不在 export 了 `GIT_DIR`/`GIT_WORK_TREE` 的命令里跑测试。
- 测试写法：`github.com/gookit/goutil/x/assert`，同一方法多用例用 `t.Run`（SR2002、AGENTS.md）。
- **测试钩子（HS10）**：`internal/hooks` 包内未导出的函数变量 `var pipelineTestHook func(point pipelinePoint, entry int)`，生产代码中为 nil、调用前判空；只在 `_test.go` 中赋值并 `t.Cleanup` 复原；不导出、不读配置或环境变量、不进 `pkg/*`。审查时核对。
- fail-closed：任一任务的 focused 测试或反向验证不符合预期即停止，按实施发现四类记录；遇到其他人的改动（Ownership Conflict）停止。T1 若走分支②，停在 T1 请求确认。
- 依赖：不新增第三方依赖。

## 波次与依赖

| 波次 | 任务 | 说明 |
|---|---|---|
| W0 | T1 | gofer 无 `--wait` 核对、旧版本对未知键的处理（只读，我执行） |
| W1 | T2 | 条目解析、注册表、分类表、CLI 分发 |
| W2 | T3 → T4 → T5 | `Runs` 与 `New`；流水线普通事件；流水线 Stop 与打断 |
| W3 | T6 | chat 记录与后台 Stop |
| W4 | T7、T8 | ptye2e；文档 |
| W5 | T9 | 全量、`-race`、staticcheck、smoke、主机 Windows、合并（我执行） |

## 任务

### T1 gofer 无 `--wait` 时的核对与旧版本未知键（HS11）

- 文件: 无改动；只读 gofer 仓库 `inhere-tools/gofer/internal/hookrelay/run.go`（Stop 处理约 `:570-680`、Interrupt `:246-282`）及服务端打开 turn 的条件（`WaitReason` 相关）；只读 suga `git show v0.55.0:internal/config/...` 中 `config validate`/未知键处理。
- 动作:
  - 判定：未设 `--wait`（`hooks.stop_args` 不含等待参数）的 `gofer hook generic --agent suga` 在 Stop 时是否打开等待 web 回复的 turn，以及“收 Stop、不收 Interrupt”时该 turn 如何结算。
  - 分支①：不打开 turn → 结论写进 issue `suga-5xmf` 评论，继续 T2。
  - 分支②：会打开 turn → 只把这一用户可见影响写进 T8 的用户文档条目，并**停下请求你确认**；不得在计划内改 D9 的 Interrupt 去向（如给即时兼容条目也发 Interrupt）。
  - 旧版本：核对 v0.55.0 的 `suga config validate`/`config get` 遇到 `hooks.commands` 时的表现（报错、警告或忽略），结论写进同一评论，供 T8 回滚说明使用。
- 验证: 评论中给出代码位置（文件:行）与结论；分支选择明确。
- 完成标准: issue 评论已写；分支①继续，分支②已停下请求确认。
- 依赖: 无（可在 hyj7 合入前执行）。

### T2 条目解析、注册表与 CLI 分发（D4、D7、D12）

- 文件: 新 `internal/config/hooks_entries.go`、`internal/config/hooks_entries_test.go`；`internal/config/settings_registry.go`（一个描述符、`dispatchConfigReader` 一个 case）；`internal/config/settings_protected_test.go`（分类表一行）；新 `internal/cli/config_hooks_test.go`。
- 动作（签名为合同）:
  - `type HookEntry struct { Command []string; TimeoutMS int }`（`TimeoutMS` 为 0 表示未设，由 `internal/hooks` 取 `hooks.timeout_ms` 默认）。
  - `func ReadHookEntries() ([]HookEntry, error)`：读 `gkconfig.Get("hooks.commands")`；nil 或空列表 → `nil, nil`；不是列表 → `hooks.commands must be a list of entries`；第 i 项（YAML 下标，从 0 起）须为映射，只允许键 `command`、`timeout_ms`，其他键 → `hooks.commands[i]: unknown field "<k>" (only command and timeout_ms are supported)`；`command` 缺失、不是字符串列表、为空或首项去空白后为空 → `hooks.commands[i].command: …`；`timeout_ms` 存在时须为正整数（接受 YAML int 与可无损转换的数字），否则 `hooks.commands[i].timeout_ms: must be a positive integer`。错误文案为英文，与现有 `hooks.*` 启动错误一致，不走 i18n。返回顺序即配置顺序。
  - 注册表：`{Pattern: "hooks.commands", Type: ConfigTypeYAML, Writable: true, Protected: true, Reader: "hooks"}`；`dispatchConfigReader` 增 `case "hooks": _, err = ReadHookEntries()`。兼容键 `hooks.command` 等维持无 Reader。
  - 分类表 `settings_protected_test.go` 的 `exec` 组加 `"hooks.commands": "exec"`。
- 验证: worktree 根运行 `go test ./internal/config -run 'TestReadHookEntries|TestProtectedClassification' -count=1`；`TestReadHookEntries` 用 `t.Run` 覆盖：未设、空列表、合法两条（含省略 `timeout_ms`）、非列表、项非映射、未知字段 `stop_args`、`command` 缺失/空/首项空白/非字符串、`timeout_ms` 为 0/负数/字符串，断言 `err.Error()` 含 `hooks.commands[<i>].<字段>`。worktree 根运行 `go test ./internal/cli -run 'TestConfigHooksCommands' -count=1`：经 root 命令真实分发（GR110），在“终端为真、`SUGA_SESSION_ID` 为空”条件下（沿用 hyj7 的 `setupConfigCLI` 注入）`config set hooks.commands '[{command: [x], timeout_ms: 10000}]'` 写入成功、`config get hooks.commands` 输出含 `timeout_ms: 10000`；`config set hooks.commands '[{command: []}]'` 退出码 2、文件字节不变；`SUGA_SESSION_ID=x` 下 set 被 protected 拒绝。反向验证：临时去掉 `case "hooks"`（可编译），确认非法值用例失败且输出不含 `[build failed]`，恢复。
- 完成标准: 上述测试通过；反向验证写在提交说明。
- 依赖: T1 分支①；前置条件 hyj7 已合入。

### T3 `Runs` 载体与 `hooks.New`（D2、HF14、HF18、HF22–HF24、HF26）

- 文件: `internal/hooks/hooks.go`；新 `internal/hooks/hooks_runs_test.go`；`internal/hooks/hooks_test.go`（仅 `:164`、`:293` 两条断言）。
- 动作（签名为合同）:
  - `type EntryRun struct { Entry int; Program string; Duration time.Duration; Response Response; Err error }`（`Response` 内不再嵌套 `Runs`）；`Response` 增字段 `Runs []EntryRun`。
  - `commandClient` 增字段 `entry int`（兼容条目 0，新条目为 YAML 下标 + 1）与 `program string`（`filepath.Base(argv[0])`）；`call` 在**每个返回路径**（成功、marshal 失败、超时、取消、非零退出、回答不可解析）都设 `resp.Runs = []EntryRun{{Entry, Program, Duration, Response: <该条回答，失败为空>, Err}}`，返回值的其他字段不变。
  - `New(opts ...Option)`：`SUGA_HOOKS=off` 同现状；`hooks.command` 为空时不构造兼容条目、不解析 `stop_args`、`post_tool_use.*`、`pre_tool_use.*`（保持现有解析顺序与错误，只是不再提前 `return nil, nil`）；随后 `appconfig.ReadHookEntries()`，出错原样返回；两者都空 → `nil, nil`；无新条目 → 返回兼容 `*commandClient`（现有实现）；有新条目 → 返回 `newPipeline(compat, entries)`（T4 实现；本任务先放只含构造与委托的骨架，使编译通过）。每个新条目 `commandClient`：`argv=Command`、`timeout=TimeoutMS` 或兼容的 `hooks.timeout_ms` 解析结果（≤0 回落 3000）、`stopTimeout=timeout`、无 `stopArgs` 与工具过滤；`opts` 对兼容条目与每个新条目各应用一次。
  - `hooks_test.go:164`、`:293`：改为断言 `Decision`、`Reason`、`AdditionalContext`、`Notice` 为空、`UpdatedInput` 为 nil，`len(Runs)==1`、`Runs[0].Entry==0`、`Runs[0].Err != nil`；不改其他断言。
- 验证: worktree 根运行 `go test ./internal/hooks -run 'TestHookRuns|TestNewHookEntries|TestHookFailuresAreBestEffort|TestHookTimeouts|TestNewIsOffUnlessConfigured|TestNewRejectsMalformedConfig|TestHookCommandSessionEnv|TestStopInterrupt|TestPreToolUseGate' -count=1`。新增：`TestHookRuns`（成功、非零退出、超时、取消、回答不可解析各填一条 `Runs`，`Program` 为 argv[0] 基名，Windows 反斜杠路径用例放 `_windows_test.go`）；`TestNewHookEntries`（只配 `hooks.commands` 时非 nil 且 `PostToolName("shell_exec")` 为 false；未设 `hooks.command` 而 `hooks.stop_args` 为非法类型、只配 `hooks.commands` 时 `New` 不报错；两者都空为 nil；`WithExtraEnv` 提供 `SUGA_SESSION_NAME`/`SUGA_TURN_ID` 时新条目命令收到它们，事件 `session_id` 补 `SUGA_SESSION_ID`，复用 `hooks_env_test.go` 的假进程）。反向验证：临时去掉超时路径的 `Runs` 填充，确认 `TestHookTimeouts` 改写后的断言失败，恢复。
- 完成标准: 上述测试通过；现有 `internal/hooks` 测试除两条断言外未改；反向验证记录。
- 依赖: T2。

### T4 流水线：普通事件与分流（D1、D8、HF18）

- 文件: 新 `internal/hooks/pipeline.go`、`internal/hooks/pipeline_test.go`。
- 动作:
  - `type pipeline struct { compat *commandClient /* 可为 nil */; entries []*commandClient }`，实现 `Client` 与 `ToolGate`。
  - `Call(ctx, ev)`：`ev.Name=="PostToolUse"` → 有兼容条目则 `compat.Call`，否则返回空 `Response`；其余事件（SessionStart、UserPromptSubmit、SessionEnd 及未知名）→ 兼容条目（若有）在前、新条目按编号依次 `call`，失败记 Warn（文案同现有 `Call`：`"hook command failed"`，带 `entry`）并跳过；合并：`AdditionalContext` 去空白后非空的按顺序以 `"\n\n"` 连接，`Notice` 非空的以 `"\n"` 连接，`Runs` 按执行顺序拼接；`Decision`/`Reason`/`UpdatedInput` 置空（这些事件的回答现状不使用）。
  - `PostToolName`/`PreToolName`/`PreTool`：兼容条目存在时委托；无兼容条目时两个 `Name` 方法返回 `("", false)`，因此 chat 不会发出这两个事件（`GateOf` 仍返回 gate，但 `PreToolName` 为 false 即不调用 `PreTool`；`PreTool` 被直接调用时返回空回答与 nil）。
  - `StartStop` 留给 T5（本任务先委托到“无等待条目、全部即时”的最简顺序实现并标 TODO 在 T5 替换，T5 完成前不合入）。
- 验证: worktree 根运行 `go test ./internal/hooks -run 'TestPipelineEvents' -count=1`：两条新条目 + 兼容条目时 SessionStart 顺序为 0、1、2（假进程把事件名与标签写入临时记录文件）；各自超时（条目 1 `timeout_ms=200` 且睡 1s 被截断，条目 2 照常返回）；单条失败不影响其他条；`additionalContext` 与 notice 连接；PostToolUse 只到兼容条目，无兼容条目时不运行任何进程；新条目不收 PreToolUse。反向验证：临时让 PostToolUse 也发给新条目，确认用例失败，恢复。
- 完成标准: 用例通过；反向验证记录。
- 依赖: T3。

### T5 流水线：Stop 两阶段、R0–R3 与打断（D5、D9、D10、HF2、HF3、HF13、HF15–HF17、HF21、HF25、HF28、HF29）

- 文件: `internal/hooks/pipeline.go`；新 `internal/hooks/pipeline_stop_test.go`。
- 动作:
  - 等待型判定只按键：兼容条目 `hooks.stop_wait_sec > 0` 或 `StopBackground()` 为真（在 `New` 中算好存为 `compatWaiting bool`）。
  - `StartStop(ctx, ev)`：建流水线 ctx（`context.WithCancel(ctx)`）与 `pipelineWait`；协程中阶段一按编号执行即时条目（兼容条目即时型时最前，兼容条目调用总附加 `stop_args`）；全部执行；阶段一无 block 且 `compatWaiting` → 阶段二运行兼容条目（`stop=true` 路径，其 `stopTimeout` 同现状）。单条失败记 Warn，取消与 `ErrTimedOut` 记 Debug（同现有 `StartStop`）。
  - 状态机（一把 `sync.Mutex`）：状态 `running(i)` / `between` / `finished`，`frozen bool`，`last int`。条目进程返回后进入结束临界区：记录该条结果、`last=i`，判定下一步——无后续 → 在**同一临界区**内检查流水线 ctx 并生成合并结果（R0：ctx 已取消 → `Err=ctx.Err()`、无决定；R1：阶段一有 block → `block`、reason 以 `"\n\n"` 连接、`Err=nil`；R2：取等待条目结果；R3：空），状态置 `finished`，再写入 `Done`（缓冲 1）；有下一条 → 状态置 `between`，出锁后启动前再取锁：`frozen` 则停止（不运行、不写额外结果，`Done` 以 R0 结束），否则置 `running(i+1)` 再启动进程。`Notice` 为已执行条目按顺序以 `"\n"` 连接；`Runs` 按执行顺序拼接。
  - `Interrupt(ctx)`：取锁置 `frozen=true`，按状态取目标：`running(i)` → i；`finished` 且结果未被 `Done` 读出 → `last`；`between` → 无。出锁后对目标发 Interrupt 事件（`Name="Interrupt"`、清 `LastAssistant` 与 `StopHookActive`，1 秒超时，经 `commandClient.Call`，失败记 Warn）；再取消流水线 ctx；等待 `Done` 结果（`running` 时等该条结束）；返回值：已完成条目中 `Err==nil` 且为 block 的 reason，加上 `running` 条目结束后其 `Err==nil` 且为 block 的 reason，按执行顺序以 `"\n\n"` 连接，每个 reason 只出现一次；`finished` 未消费时取该结果中 block 的 reason。
  - 测试钩子（HS10）：`type pipelinePoint int`，常量 `pointEntryExited`（进程返回后、进入结束临界区前）、`pointBeforeStartNext`（判定有下一条、启动前取锁前）、`pointBeforeDone`（生成合并结果后、写入 `Done` 前）；`var pipelineTestHook func(point pipelinePoint, entry int)`，生产为 nil。
- 验证: worktree 根运行 `go test ./internal/hooks -run 'TestPipelineStop|TestPipelineInterrupt' -count=1`。`TestPipelineStop`：R1 跳过等待条目（等待条目记录文件中无 Stop）；两条新条目都 block 时 reason 以空行连接；阶段一 A block + B 非零退出 → `Err=nil` 且为 block；阶段一无 block → 兼容等待条目运行，`Err` 取其错误（含 `ErrTimedOut`）；即时型兼容条目在阶段一且附加 `stop_args`（HF16）；`stop_wait_sec=1`、`timeout_ms=5000` 的兼容条目归阶段二（HF17）；R0：父 ctx 取消时 `errors.Is(err, context.Canceled)` 且无 block；`pointEntryExited` 处取消、命令已正常输出 block → R0 丢弃（HF21、HF29）；`pointBeforeDone` 之后取消 → 结果保持 R1（HF29）；Stop 失败记 Warn、取消与超时记 Debug（用 `slog` 测试 handler 断言级别）。`TestPipelineInterrupt`：`running`（等待条目运行中）→ 只有它收 Interrupt；`running` 子态（`pointEntryExited` 阻塞期间打断，该条已输出 block）→ 该条收 Interrupt 且 reason 交回一次（HF28）；`finished` 未消费 → `last` 收 Interrupt（HF15）；`between`（`pointBeforeStartNext` 阻塞期间打断）→ 无条目收 Interrupt、等待条目不启动（HF3、HF25）；阶段一已得 block 经返回值交回且 `Done` 为 R0（`recordingWait` 不重复取）；Interrupt 进程失败记 Warn。反向验证：①临时去掉阶段一 block 跳过阶段二；②临时把 Interrupt 发给全部条目；③临时去掉 R0。各确认对应用例失败且输出不含 `[build failed]`，恢复。
- 完成标准: 用例通过；三项反向验证记录在提交说明；`pipelineTestHook` 只在 `_test.go` 赋值（`grep -n pipelineTestHook internal/hooks/*.go` 只有声明、判空调用与测试）。
- 依赖: T4。

### T6 chat：逐条 trace 与后台 Stop（HF13、HF14、HF21、HF24）

- 文件: `internal/chat/hooks_record.go`、`internal/chat/hooks_gate.go`；测试 `internal/chat/hooks_record_test.go`、`hooks_gate_test.go`、新 `internal/chat/stop_background_multi_test.go`。
- 动作:
  - `recordingHooks.Call`：`resp.Runs` 非空时每条调用 `recordHookRun(…, ev, Result{Response: run.Response}, run.Duration, map[string]any{"entry": run.Entry, "program": run.Program})`（不带 `Err`，status 同现状）；为空时按现状写一条。
  - Stop 记录（`StartStop` 的转发协程）：`Runs` 非空时每条以 `Result{Response: run.Response, Err: run.Err}` 记录并加两个 attrs；为空时按现状。`recordingWait` 的转发与 `Interrupt` 补取逻辑不改。
  - `hooks_gate.go`：`extra` 中在 `response.Runs` 非空时加 `entry`、`program`（取 `Runs[0]`）；拒绝与改写逻辑不改。
- 验证: worktree 根运行 `go test ./internal/chat -run 'TestRecordingHooks|TestToolGate|TestBackgroundStop|TestMultiHookBackgroundStop|TestPersonalChatRunsConfiguredHookCommand|TestPersonalChatPostToolUseFollowsConfig|TestPersonalChatBlockStartsInjectedTurnAndMarksStopActive|TestPersonalChatKeepsWebReplyReceivedDuringInterrupt|TestPersonalChatSurvivesFailingHookCommand' -count=1`。新增断言：只配兼容条目时每个 hook 事件带 `entry=0`/`program`，SessionStart 失败仍记 `ok`；PreToolUse 失败的记录带 `entry=0`/`program` 且 status 同现状；两条新条目时 Stop 记两条。`TestMultiHookBackgroundStop`（`hooks.New` + 两条新条目的假进程，复用 `chat_hooks_config_test.go` 的 `TestFakeHookProcess` 模式）：`stop_background` 下 A 已 block、B 运行中时以新 Stop 杀掉 → 不发 `Stop hook asks to continue` 通知、`takeBlocked()` 为 false、任务以 `-1` 结束、等待条目未运行（HF13）；`tasks.max_duration` 设很短时同样不通知；`taskManager.Close()` 时同样不通知（HF21）。反向验证：临时去掉 R0（T5 中），确认 `TestMultiHookBackgroundStop` 失败，恢复。
- 完成标准: 用例通过；`hooks_record.go` 与 `hooks_gate.go` 净增各不超过 25 行；反向验证记录。
- 依赖: T5。

### T7 ptye2e 真实二进制场景

- 文件: 新 `test/ptye2e/chat_multi_hooks_test.go`（首行 `//go:build ptye2e`，参照 `chat_observe_test.go` 的 `TestHookHelper` 与 `smoke_helpers_test.go` 的 `startChatOpts`）。
- 动作:
  - 假 hook `TestMultiHookHelper`（只在环境 `PTYE2E_MULTI_HOOK=1` 时运行）：读 stdin 一行事件，把 `<标签> <hook_event_name>` 追加到 `PTYE2E_MULTI_HOOK_LOG` 指定文件；标签由命令行参数给出（`-test.run=^TestMultiHookHelper$ -- cont|gofer`）。`cont`：第一次 Stop 输出 `{"decision":"block","reason":"continue-now"}`（用同目录状态文件记次数），之后无输出。`gofer`：Stop 时睡到被杀（最长 30s），Interrupt 立即退出。
  - `TestChatMultiHooks`：`Extra` 写 `hooks:` 下 `command:`（gofer 标签）、`stop_wait_sec: 30`、`commands:` 一条（cont 标签，`timeout_ms: 5000`）；发一行输入 → 等续跑回合（屏幕出现 `continue-now`）→ 第二次 Stop 进入等待 → 输入一行打断 → 退出。断言记录文件：第一次 Stop 只有 `cont Stop`，无 `gofer Stop`；第二次有 `gofer Stop`；`Interrupt` 只出现为 `gofer Interrupt`；`cont` 无 Interrupt；`suga inspect trace --json` 中 `kind=hook`、`name=Stop` 的事件带 `entry`/`program`，`entry=1` 的第一条 `decision=block`。
- 验证: worktree 根运行 `go test -tags ptye2e ./test/ptye2e -run 'TestChatMultiHooks' -count=1 -v`，输出中为 `--- PASS`（不是 `no tests to run`）。反向验证：临时去掉阶段一 block 跳过阶段二、临时把 Interrupt 发给全部条目，确认对应断言失败（确认失败来自断言而非编译错误），恢复。
- 完成标准: 用例在执行者的 Windows ConPTY 下通过；反向验证记录（Linux 由 T9 跑）。
- 依赖: T6。

### T8 文档

- 文件: `README.md`（hooks 一节）、`docs/usage/gofer-personal-agent.md`（`:212-213` 与 `:390-402` 附近）、`skills/suga-guide/references/config.md`（`hooks.*` 表）、`CHANGELOG.md`「未发布」。
- 动作: 写明 `hooks.commands` 形状（只有 `command` 与 `timeout_ms`）、与 `hooks.command` 共存、新条目收哪些事件、Stop 先即时后等待、续跑 block 时 gofer 本回合不收 Stop 的影响、新条目不要依赖 stderr 提示、protected 与 `suga config set` 写法、回滚方式（含 T1 对旧版本的结论）；T1 走分支②时补写该影响。
- 验证: worktree 根运行 `grep -n "hooks.commands" README.md docs/usage/gofer-personal-agent.md skills/suga-guide/references/config.md CHANGELOG.md`，四个文件都有结果；CHANGELOG 新条目在「未发布」下、已发布小节未变（`git diff -U0 CHANGELOG.md` 只在「未发布」内）。
- 完成标准: grep 结果符合。
- 依赖: T6（与 T7 可并行审查，顺序提交）。

### T9 全量验证与合并（我执行）

- 文件: 无代码改动；主机脚本放 `tmp/suga-5xmf/`。
- 动作:
  - 容器内 worktree 根：`go test -race ./...`、`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...`、`make smoke-go`（含 Linux 上的 `TestChatMultiHooks`）、`make smoke-chat`。
  - 主机 Windows（gofer exec job，只写 `tmp/`）：在 worktree 跑 `go test ./internal/config/... ./internal/hooks/... ./internal/chat/... ./internal/cli/...` 与 `go test -tags ptye2e ./test/ptye2e -run 'TestChatMultiHooks|TestChatObserve' -count=1 -v`。
  - 合并：主机 `tmp/xhen-p1/host-merge.ps1 -Repo inhere-tools/suga -Branch feat/multi-command-hooks -MsgFile …`，以 `is_ancestor=0` 为收尾前提；CHANGELOG 已发布小节逐字未变。
  - 把配置形状回复 IDEV-STD 维护会话（my-tools-dev-08）的消息内容写进 issue 评论，发送需你确认（外部动作 Gate）。
- 验证: 以上命令全部通过，Windows 输出含 `--- PASS: TestChatMultiHooks`；结果记录在 issue 评论。
- 完成标准: 全绿并合并到本地 main。
- 依赖: T7、T8。

## 回滚与恢复

- 每个任务至少一个提交，恢复点为各任务提交；合并前在 worktree 分支上，主分支不受影响。
- 合并后回滚：revert 合并提交；用户侧删掉 `hooks.commands` 即回到单命令行为。
- hyj7 若合入后被回滚：本分支依赖 `ConfigKey.Protected` 与分类表，停在 T2 前，按 Semantic Amendment 处理。

## 人工 Gate

1. 计划批准（本稿评审后）。
2. 当前执行请求：你指示开始实施（且 hyj7 已合入 main）。
3. T1 分支②：gofer 无 `--wait` 会打开 turn 时，停下请求确认。
4. 外部动作 Gate：push、tag、发版、安装、向 IDEV-STD 会话发送配置形状，均需你另行逐项批准；本计划只准备消息内容。

## 可追溯性

| 设计条目 | 任务 | 验证 |
|---|---|---|
| AC1、D7、D11（新条目与兼容条目共存） | T2、T3、T4、T7 | `TestReadHookEntries`、`TestNewHookEntries`、`TestPipelineEvents`、`TestChatMultiHooks` |
| AC2、D9、D10（Stop 两阶段、block 合并） | T5、T6、T7 | `TestPipelineStop`、`TestMultiHookBackgroundStop`、`TestChatMultiHooks` + 反向验证 |
| AC3、D2、HF1、HF15、HF23、HF26（兼容不变） | T3、T6 | 现有 `internal/hooks` 与 chat hook 测试、`TestNewHookEntries` |
| AC4（单独超时） | T2、T4 | `TestReadHookEntries`、`TestPipelineEvents` 各自超时 |
| D1、D4（包归属） | T2、T3、T4 | 文件清单与审查 |
| D3（格式不变） | T4、T5 | 现有回答解析测试不改 |
| D5、R0–R3、HF2、HF13、HF21、HF29 | T5、T6 | `TestPipelineStop`、`TestMultiHookBackgroundStop` |
| 打断三态、HF3、HF25、HF28 | T5、T7 | `TestPipelineInterrupt`、`TestChatMultiHooks` |
| HF16、HF17（`stop_args`、等待型判定） | T5 | `TestPipelineStop` |
| HF18（分流与启用） | T3、T4 | `TestNewHookEntries`、`TestPipelineEvents` |
| HF14、HF24（逐条 trace） | T3、T6、T7 | `TestHookRuns`、`TestRecordingHooks`、`TestToolGate`、trace 断言 |
| HF22、HF27（环境） | T3 | `TestNewHookEntries` |
| D6、D12（protected、Reader、分类表、发布顺序） | 前置条件、T2 | `TestProtectedClassification`、`TestConfigHooksCommands` |
| D13（不纳入项目级） | 排除项 | — |
| HS10（测试钩子形式） | T5 | grep 与审查 |
| HS11（gofer 无 `--wait`） | T1、T8 | issue 评论、文档 |
| 文档、回滚说明 | T1、T8 | T8 grep |
| 验证要求（`-race`、Linux ptye2e、Windows） | T9 | 全量命令与主机 job |

## 完成 Gate 与剩余工作

- 完成条件：T1–T9 完成标准全部满足、合并到本地 main，issue `suga-5xmf` 评论附验证结果后关闭；plan todo 标 done。
- 进入 `COMPLETE` 时写 ≤8 行完成总结（主旨、状态、自主决策、提交数与区间、耗时、剩余项）。
- 剩余：发版与推送待你授权，且须与 `suga-hyj7` 同版或其后（D6）；向 IDEV-STD 会话发送配置形状待你确认；HS6 glob 文案另开 Quick issue。
