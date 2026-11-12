<!-- template_id: plan; template_version: 1.3.1 -->
# suga chat hooks 多命令 实施计划

> 状态：Draft 0.2 / 待人工计划批准；issue `suga-5xmf`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-12 | Claude | 初稿：按已批准设计 0.6 拆为 T1–T9 |
| 0.2 | 2026-11-12 | Claude | 处理第 1 轮两轴评审（按类自查：打断与 `Done` 的消费者、确定性同步点、反向验证预期、CLI 输出、验证要求落任务、文件归属、假进程、文档落点、授权与执行者）：打断不读 `Done`，`finished` 态一律发给最后执行的条目，`Done` 写一次后 close，`Interrupt` 等内部完成信号，T6 加经 `recordHooks` 包装的打断用例；增加打断侧同步点与三种窗口的用例编排；T2 改反向验证预期并负责 `config get/set` 的 YAML 输出；设计验证要求逐条落任务；`newPipeline` 签名与文件归属闭合；规定假进程合同；T1 补决定性代码位置与分支②后的去向；T8 按 hyj7 合入后的文档落点；补真实环境验证、派活约束、执行者待定、授权、retrospective 与 gofer plan |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

第 1 轮评审处理（`docs/review/2026-11-12-multi-command-hooks-plan/`）：

| 评审项 | 处理 |
|---|---|
| HPF1（阻断） | T5：`Interrupt` 不读 `Done`；`finished` 态一律以最后执行的条目为目标；`Done` 只写一次后 close；`Interrupt` 等流水线内部完成信号，reason 由内部逐条结果计算。T6 加经 `recordHooks` 包装的打断用例（running/finished 各一），“不重复交回、不挂起”的断言移到 T6。核对：不改变设计 §4 语义——设计中“结果已到但未被消费”的“消费”指 chat 读到结果，chat 读到后即置 `waiting=nil`、不再调用 `Interrupt`，所以 `finished` 态被打断时结果必然未被 chat 消费，与“一律发给最后执行者”等价 |
| HPF2（阻断） | T5 增加打断侧钩子点 `pointInterruptFrozen`；全部钩子点不持锁调用、`pointBeforeDone` 在状态置 `finished` 之后；写出 running 子态、between、finished、HF21、HF29 五个用例的编排（谁阻塞、谁放行、以什么信号），不用 sleep |
| HPF3（阻断） | T2 非法值用例断言错误文本含 `hooks.commands[0].command`；反向验证预期改为该断言失败 |
| HPF4（阻断） | T2 文件加 `internal/cli/config.go`：`writeConfigValue` 对 `hooks.commands` 以 goccy flow 风格输出 YAML（仿 `PreferredTool` 分支），覆盖 `config get` 与 `config set` 回显 |
| HPF5、HPS1 | T3 加 `stop_background`+`stop_args` 无 `stop_wait_sec` 与只配 `stop_args` 的兼容用例；T6 加后台 R1 正例（任务通知、`takeBlocked()` 为真）；T5 加合并 Notice 断言；T7 加 SessionStart/UserPromptSubmit 的 `entry`/`program` 与“trace 无 Interrupt”断言；写明去掉 R0 的反向验证在 T5/T6 单元层承担（ptye2e 场景不触发 R0） |
| HPF6、HPS7 | T3 文件加 `pipeline.go`（骨架），`newPipeline` 签名写定，`compatWaiting` 在 T3 的 `New` 中计算并经参数传入；T4 临时 `StartStop` 提交信息带 `checkpoint`；T1 分支②改为记入 issue 评论供 T8 写文档 |
| HPF7 | 规定假进程合同：`internal/hooks` 的 `TestPipelineHookProcess`、`internal/chat` 的 `TestMultiHookProcess`、ptye2e 的 `TestMultiHookHelper`，按 argv 选行为、可非零退出、启动即写 `start` 行作为“已启动”信号 |
| HPF8、HPS6 | T1 补 gofer `internal/commands/hook.go:158,284-299`、`internal/httpapi/session_handler.go:249-265`、`run.go:167,393-395,594,642`；旧版本改查 `config get`/`config list`/chat 启动；分支②获确认后继续 T2；T1 可在 hyj7 合入前执行，Gate 2 相应改写 |
| HPF9 | T8 落点按 hyj7 合入后的文件：README “Protected settings” exec 清单与 `:119` 常用开关、usage“安全相关配置”执行类与 hooks 条目、`config.md` 的 `hooks.*` 表、CHANGELOG；grep 验证覆盖两处执行类清单；前置判定改为 `git merge-base --is-ancestor feat/protected-config-keys main` |
| HPF10 | between 态被冻结时 `Done` 结果 `Err=context.Canceled` |
| HPF11 | `ReadHookEntries` 写全接受的容器与数字类型及归一化顺序（同 `ReadMCPServerSettings`） |
| HPF12、HPS4 | 执行者改为在计划 Gate 由你指定（hyj7 的 oc-go ds4.1 属对 hyj7 的单次授权）；按 SR1432 建议 T5 用推理更强的执行者；任务书补暂存核对、禁 `stash`/`reset`/`checkout -- <他人路径>`、提交节奏 |
| HPS2 | 可追溯表补 D8、HF12、HF19、HF20 与 T9 的合并、真实环境验证、IDEV-STD 消息 |
| HPS3 | T9 加主机真实环境验证（真实 gofer hook + `idevstd script continuation_hook --agent suga`，隔离 `SUGA_HOME`）；续跑 block 的真实路径标 `UNVERIFIED_REAL_ENV` 并给复现命令 |
| HPS5 | 授权与 Gate 2 写明：当前执行请求覆盖建 worktree 分支 `feat/multi-command-hooks` 与合并到本地 main，不含 push |
| HPS8 | Capability decisions 补 CAP-05 多条目编排 |
| HPS9 | Windows 跑 `go test ./...`；容器只跑 `make smoke-chat`（已依赖 smoke-go） |
| HPS10 | 前置补建 gofer plan 与 T1–T9 todo；完成 Gate 补 retrospective 判定 |
| HPS11 | T9 写定 IDEV-STD 消息要点与发送时机理由 |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：落实设计 0.6——`hooks.commands` 新条目（`command` + `timeout_ms`）与 `hooks.command` 兼容条目共存；新条目的 Stop block 开启下一回合；只配 `hooks.command` 时行为、能否启动与现状一致（AC1–AC4）。
- scope freeze：设计 0.6 的 §1–§6、架构与“验证要求”；评审 HS10、HS11 的计划落实；设计 §5 要求的 `config get` YAML 输出（`internal/cli/config.go` 一处）。不含未选方案 A 的条目字段、项目级 hook、HS6 的 glob 文案修正、发版与推送。
- 扩围策略：`DEFER_OR_REQUEST`；review budget：最多 8 轮；停止条件：无开放 `CORE_BLOCKING`。
- 暴露度（SR1409）：高（已发布 `v0.55.0`；持久命令执行、gofer Stop 等待链路）；两轴独立评审与 ledger，评审目录 `docs/review/2026-11-12-multi-command-hooks-plan/`。

## 目标与完成定义

完成定义（全部满足）：
1. `hooks.commands` 可在 `config.yaml` 中配置，也可由人在终端经 `suga config set` 写入（protected、`Reader:"hooks"` 校验，非法值退出码 2）；`config get` 以 YAML 输出；分类表含 `hooks.commands: exec`。
2. 只配 `hooks.command` 时 `hooks.New` 返回现有单条目实现，现有测试除 `hooks_test.go:164`、`:293` 两条零值断言外不改即通过；`stop_background`+`stop_args` 无 `stop_wait_sec` 等现有配置照常启动。
3. 有新条目时：SessionStart/UserPromptSubmit/SessionEnd 按编号顺序发给全部条目；PreToolUse/PostToolUse 只发兼容条目；Stop 两阶段与 R0–R3、打断三态与交回规则按设计 §4 实现，经 chat 的 `recordHooks` 包装后行为不变。
4. trace 每条运行记一条 `kind=hook`，带 `entry`/`program`；只配兼容条目时其余字段与 status 不变；Interrupt 不进 trace。
5. 设计“验证要求”全部有对应测试且通过，含反向验证、ptye2e（Linux 与 Windows ConPTY）、GR110 真实分发；主机真实环境验证已做或按 `UNVERIFIED_REAL_ENV` 披露。
6. gofer 无 `--wait` 时的核对结论已记录（HS11）；文档已更新；给 IDEV-STD 的配置形状消息已准备（发送走外部动作 Gate）。

## 范围、排除项与授权

- 范围：suga 仓库 `internal/config`（新 `hooks_entries.go`、注册表一行与一个 Reader case、分类表测试一行）、`internal/cli/config.go`（`writeConfigValue` 一处 YAML 分支）与 `internal/cli` 测试、`internal/hooks`（`hooks.go`、新 `pipeline.go` 与测试）、`internal/chat`（`hooks_record.go`、`hooks_gate.go` 与测试）、`test/ptye2e`（新场景）、README、`docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/config.md`、CHANGELOG。gofer 仓库只读核对。
- 排除：未选方案 A 的字段；项目级 hook（D13）；HS6 的 glob 错误文案（deferred）；`inspect env` 展示条目；发版、推送、安装。
- 授权：本计划批准只授权进入实施请求；实施需你另行指示。当前执行请求覆盖：建 worktree 分支 `feat/multi-command-hooks`、在其上本地提交、合并到本地 main；不含 push、tag、发版、安装、向其他会话发消息。
- `host_or_non_offline_action=REQUIRED`：主机上建 worktree（`tmp/xhen-p1/wt-repo.ps1`）、合并到本地 main（`tmp/xhen-p1/host-merge.ps1`）；执行者 `gofer job -a suga` 在主机运行并调用模型 provider（联网、产生费用）；主机 Windows 测试；T9 真实环境验证会调用真实模型、在本机 gofer server 上产生一个临时 hook 会话记录；`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1` 会联网下载模块。均不推送、不发布。

## 输入与批准证据

- 设计：`docs/design/2026-11-12-multi-command-hooks-design.md` Draft 0.6，提交 `da204262` 记录批准。
- 批准原话（2026-11-12）：“批准 suga 多命令 hook 设计 0.6（接受未复核修订），开始写计划”。
- 设计评审：`docs/review/2026-11-12-multi-command-hooks/`（4 轮，ledger PASS）；HS10、HS11 在本计划落实（T5、T1）。
- 前置计划：`docs/plans/2026-11-12-protected-config-keys-plan.md` 0.4（`suga-hyj7`，已批准，实施中）。
- 计划评审：`docs/review/2026-11-12-multi-command-hooks-plan/`（第 1 轮）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 新条目解析与 `config set` 校验、`config get` 输出 | `internal/config/settings_registry.go` Reader 分发（`dispatchConfigReader`）、`mcp.go` `ReadMCPServerSettings`、`internal/cli/config.go` `writeConfigValue` 的 `PreferredTool` YAML 分支 | 是 | 原位扩展（Reader `"hooks"`、一处 YAML 分支） | DIRECT_REUSE | none | 无 |
| CAP-02 | 单条命令执行、超时、环境、Interrupt | `internal/hooks/hooks.go` `commandClient.call`、`stopWait`、`hookEnv`、`Option` | 是 | 原位扩展（每条目一个 `commandClient`，加 `Runs`） | DIRECT_REUSE | none | 无 |
| CAP-03 | trace 记录、后台 Stop、PreToolUse 门 | `internal/chat/hooks_record.go` `recordHookRun`、`stop_background.go`、`hooks_gate.go` | 是 | 原位扩展（按 `Runs` 逐条记录） | DIRECT_REUSE | none | 无 |
| CAP-04 | 伪终端端到端与假 hook | `test/ptye2e` `startChatOpts`、`chat_observe_test.go` `TestHookHelper` 模式、假模型 | 是 | 新场景文件 | DIRECT_REUSE | none | 无 |
| CAP-05 | 多条目编排与合并（分流、顺序、两阶段 Stop、冻结与打断） | `stopWait`、`stop_background.go` 任务、`commandClient.call` | 否（均为单条目） | 既有 owner 扩展（`internal/hooks` 新文件 `pipeline.go`，设计 D1） | OWNER_EXTENSION | 现有原语没有多条目顺序执行、两阶段 Stop 与冻结状态机 | 单条目路径保留不改，无重复实现 |

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

- workspace baseline（2026-11-12，计划写作时）：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；branch `main`；HEAD `1b747836`（计划 0.1）；`git status --short` 干净；`suga-hyj7` 在主机 worktree `.worktrees/hyj7`（分支 `feat/protected-config-keys`，tip `5d0685ff`，T1–T6 已提交，T7 未做）实施中，未合入 main。实施开始时重新记录 HEAD 与 status。
- **前置条件（fail-closed，T2 起）**：`git merge-base --is-ancestor feat/protected-config-keys main` 为真（hyj7 分支最终 tip 已合入，含 T6 文档），issue `suga-hyj7` 已关闭，main 上存在 `ConfigKey.Protected` 与 `internal/config/settings_protected_test.go` 的 `TestProtectedClassification`。不满足时不建 worktree、不开始 T2。T1 不受此限。
- 进度跟踪：执行开始时 `gofer plan create --title "suga 多命令 hooks 实施" --project my-tools-dev --desc "仓库 inhere-tools/suga；计划 docs/plans/2026-11-12-multi-command-hooks-plan.md"`，再 `gofer plan add-todo <plan-id> "T1 …" --cwd inhere-tools/suga --after prev …` 建 T1–T9 todo；每个执行者 job 挂到对应 todo。
- worktree：前置条件满足后在主机建 `gofer job run -p my-tools-dev -a exec --runner local --sync -- pwsh -File tmp/xhen-p1/wt-repo.ps1 -Repo inhere-tools/suga -Name hooks5x -Branch feat/multi-command-hooks`（`.worktrees/hooks5x`，基于当时的 main）。容器内不 `git worktree add/prune`；对 worktree 的 git 命令只用单条前缀 `GIT_DIR`/`GIT_WORK_TREE`，不 export，不与测试同一命令。
- 执行者：T2–T8 由 `gofer job -a suga` 在 `.worktrees/hooks5x` 顺序实施，每任务一个 job、≤45 分钟；模型在计划 Gate 由你指定（hyj7 用的 `opencode-go/deepseek-v4.1-flash` 是对 hyj7 的单次授权，不继承）。按 SR1432，T5（并发状态机）建议用推理更强的执行者。执行者变化不改任务合同。我审查（先看 `git diff --stat` 删除行）并复核反向验证；T1、T9 由我执行。
- 任务书约束（每个 job 都写）：只改本任务列出的文件；不删注释、不重排无关代码；不用 web_fetch；可改 go.mod/go.sum（本计划预期不改）；遵守 GR107–GR110 与分层；只暂存本任务文件，提交前核对 `git diff --cached --name-only`；禁用 `git stash`、`git reset`、`git checkout -- <他人路径>`；每完成一块（实现、测试、反向验证说明）即提交，首个提交不晚于开始后 20 分钟；`--sync`，不起空等后台任务；不在 export 了 `GIT_DIR`/`GIT_WORK_TREE` 的命令里跑测试。
- 测试环境：执行者 job 在 Windows 主机运行，无 cgo，任务内 focused 测试不带 `-race`；`-race`、Linux ptye2e、staticcheck 由我在容器里于 T9 跑。所有测试用临时 `SUGA_HOME`，`t.Setenv("SUGA_SESSION_ID", "")`、`t.Setenv("SUGA_HOOKS", "")`，不读写真实 `~/.suga`。
- 测试写法：`github.com/gookit/goutil/x/assert`，同一方法多用例用 `t.Run`（SR2002、AGENTS.md）。
- **测试钩子（HS10）**：`internal/hooks` 包内未导出的函数变量 `var pipelineTestHook func(point pipelinePoint, entry int)`，生产代码中为 nil、调用前判空；只在 `_test.go` 中赋值并 `t.Cleanup` 复原；不导出、不读配置或环境变量、不进 `pkg/*`。
- **假进程合同**（HPF7）：各包测试二进制自带假 hook，命令为 `<test binary> -test.run=^<Name>$ -- <label> <mode>`，由环境 `<包前缀>_HOOK_PROCESS=1` 启用、`<包前缀>_HOOK_LOG=<文件>` 指定日志；启动即向日志追加 `<label> <event> start`，结束前追加 `<label> <event> end`；`mode` 只作用于 Stop：`block`（输出 `{"decision":"block","reason":"<label>-reason"}`）、`fail`（exit 1）、`wait`（睡到被杀，最长 30s）、`silent`（无输出）、`slow`（睡 1s 后无输出）、`ifail`（Stop 同 `wait`，收到 Interrupt 时 exit 1）；SessionStart/UserPromptSubmit 输出 `{"hookSpecificOutput":{"additionalContext":"<label>-ctx"}}` 并在 stderr 写 `notice-<label>`；Interrupt 记日志后立即 exit 0；`timeout` 模式在全部事件上睡 1s（用于超时用例）。名称：`internal/hooks` 的 `TestPipelineHookProcess`、`internal/chat` 的 `TestMultiHookProcess`、ptye2e 的 `TestMultiHookHelper`。“已启动”同步点为轮询日志出现 `start` 行（期限 10s），不用固定 sleep。
- fail-closed：任一任务的 focused 测试或反向验证不符合预期即停止，按实施发现四类记录；遇到其他人的改动（Ownership Conflict）停止。T1 走分支②时停下请求确认。
- 依赖：不新增第三方依赖。

## 波次与依赖

| 波次 | 任务 | 说明 |
|---|---|---|
| W0 | T1 | gofer 无 `--wait` 核对、旧版本对未知键的处理（只读，我执行，可在 hyj7 合入前） |
| W1 | T2 | 条目解析、注册表、分类表、CLI 输出与分发 |
| W2 | T3 → T4 → T5 | `Runs` 与 `New`；流水线普通事件；流水线 Stop 与打断 |
| W3 | T6 | chat 记录、包装下的打断、后台 Stop |
| W4 | T7、T8 | ptye2e；文档 |
| W5 | T9 | 全量、`-race`、staticcheck、smoke、主机 Windows、真实环境、合并（我执行） |

## 任务

### T1 gofer 无 `--wait` 时的核对与旧版本未知键（HS11）

- 文件: 无改动。只读 gofer 仓库 `inhere-tools/gofer`：`internal/hookrelay/run.go`（未设 `--wait` 时 `Options.Wait` 回落 `:167,393-395`；Stop 打开 turn `:594,642`；Interrupt `:246-282`）、`internal/httpapi/session_handler.go:249-265`（服务端 `WaitReason`）、`internal/commands/hook.go:158,284-299`（只有 SIGINT/SIGTERM/SIGHUP 触发 `ReportInterrupt`；suga 以 `exec.CommandContext` 结束进程，Unix 为 SIGKILL、Windows 为 TerminateProcess）。只读 suga `git show v0.55.0:internal/cli/config.go`、`v0.55.0:internal/config/settings_registry.go`（`MatchConfigKey`）与 `v0.55.0:internal/hooks/hooks.go`。
- 动作:
  - 判定：未设 `--wait` 的 `gofer hook generic --agent suga` 在 Stop 时是否打开等待 web 回复的 turn；在流水线中“收 Stop、不收 Interrupt”（以及被 suga 超时杀掉）时该 turn 如何结算。
  - 分支①：不打开 turn → 结论写进 issue `suga-5xmf` 评论，继续。
  - 分支②：会打开 turn → 把这一用户可见影响写进 issue 评论（供 T8 写入文档），并**停下请求你确认**；不得在计划内改 D9 的 Interrupt 去向。获你确认后继续 T2。
  - 旧版本：v0.55.0 遇到 `hooks.commands` 时 `suga config get hooks.commands`（预期 `unknown configuration key`）、`suga config list`（只列注册键）与 chat 启动（`hooks.New` 不读该键）的表现，结论写进同一评论，供 T8 回滚说明。
- 验证: 评论中给出代码位置（文件:行）与结论；分支选择明确。
- 完成标准: issue 评论已写；分支①继续，分支②已停下请求确认。
- 依赖: 无（可在 hyj7 合入前执行）。

### T2 条目解析、注册表、CLI 输出与分发（D4、D7、D12）

- 文件: 新 `internal/config/hooks_entries.go`、`internal/config/hooks_entries_test.go`；`internal/config/settings_registry.go`（一个描述符、`dispatchConfigReader` 一个 case）；`internal/config/settings_protected_test.go`（分类表一行）；`internal/cli/config.go`（`writeConfigValue` 一处分支）；新 `internal/cli/config_hooks_test.go`。
- 动作（签名为合同）:
  - `type HookEntry struct { Command []string; TimeoutMS int }`（`TimeoutMS` 为 0 表示未设，由 `internal/hooks` 取 `hooks.timeout_ms` 默认）。
  - `func ReadHookEntries() ([]HookEntry, error)`，归一化顺序：①`raw := gkconfig.Get("hooks.commands")`；nil → `nil, nil`。②容器：`[]any` 直接用；`[]map[string]any` 转为 `[]any`（同 `ReadMCPServerSettings`）；空 → `nil, nil`；其他类型 → `hooks.commands must be a list of entries`。③每项（YAML 下标 i，从 0 起）须为 `map[string]any`，否则 `hooks.commands[i]: must be a mapping`；键只允许 `command`、`timeout_ms`，其他键 → `hooks.commands[i]: unknown field "<k>" (only command and timeout_ms are supported)`（多个未知键按字典序报第一个）。④`command`：接受 `[]string` 或元素全为 string 的 `[]any`；缺失、其他类型、为空、首项去空白后为空 → `hooks.commands[i].command: must be a non-empty list of strings with a program name`。⑤`timeout_ms`：缺失为 0；接受 `int`、`int64`、`uint64`（≤ `math.MaxInt`）、整值 `float64`；≤0、非整数或其他类型 → `hooks.commands[i].timeout_ms: must be a positive integer`。错误文案为英文，与现有 `hooks.*` 启动错误一致，不走 i18n。返回顺序即配置顺序。
  - 注册表：`{Pattern: "hooks.commands", Type: ConfigTypeYAML, Writable: true, Protected: true, Reader: "hooks"}`；`dispatchConfigReader` 增 `case "hooks": _, err = ReadHookEntries()`。兼容键维持无 Reader。
  - 分类表 `settings_protected_test.go` 的 `exec` 组加 `"hooks.commands": "exec"`。
  - `internal/cli/config.go`：`writeConfigValue` 在 `value.Key == "hooks.commands"` 时以 `goccyyaml.MarshalWithOptions(value.Value, goccyyaml.Flow(true))` 输出（去首尾空白，单行），失败时回落现有 `formatConfigValue`；`config get` 与 `config set` 成功回显都经此函数。其他键输出不变。
- 验证: worktree 根运行 `go test ./internal/config -run 'TestReadHookEntries|TestProtectedClassification' -count=1`；`TestReadHookEntries` 用 `t.Run` 覆盖：未设、空列表、合法两条（含省略 `timeout_ms`）、经 `gkconfig.Set` 写入的 `[]map[string]any` 与 YAML 加载得到的 `[]any`、`[]string` 与 `[]any` 两种 `command`、`timeout_ms` 为 int/uint64/整值 float64、非列表、项非映射、未知字段 `stop_args`、`command` 缺失/空/首项空白/非字符串、`timeout_ms` 为 0/负数/1.5/字符串，断言 `err.Error()` 含 `hooks.commands[<i>].<字段>`。worktree 根运行 `go test ./internal/cli -run 'TestConfigHooksCommands|TestConfigCommands' -count=1`：经 root 命令真实分发（GR110），在“终端为真、`SUGA_SESSION_ID` 为空”条件下（沿用 hyj7 的 `setupConfigCLI` 注入）`config set hooks.commands '[{command: [x], timeout_ms: 10000}]'` 写入成功且回显行为 `hooks.commands = [{command: [x], timeout_ms: 10000}]`、`config get hooks.commands` 同一行（若 goccy flow 的空格与此不同，按实际输出固定并记为 Operational Discovery）；`config set hooks.commands '[{command: []}]'` 退出码 2、文件字节不变、`err.Error()` 含 `hooks.commands[0].command`；`SUGA_SESSION_ID=x` 下 set 被 protected 拒绝；`config get mcp.servers` 输出不变。反向验证：临时去掉 `case "hooks"`（可编译），确认非法值用例的 `hooks.commands[0].command` 断言失败（此时报 `unknown config reader`）且输出不含 `[build failed]`，恢复。
- 完成标准: 上述测试通过；反向验证写在提交说明。
- 依赖: T1（分支①，或分支②已获确认）；前置条件 hyj7 已合入。

### T3 `Runs` 载体、`hooks.New` 与流水线骨架（D2、HF1、HF14、HF18、HF22–HF24、HF26）

- 文件: `internal/hooks/hooks.go`；新 `internal/hooks/pipeline.go`（仅骨架）；新 `internal/hooks/hooks_runs_test.go`；`internal/hooks/hooks_test.go`（仅 `:164`、`:293` 两条断言）。
- 动作（签名为合同）:
  - `type EntryRun struct { Entry int; Program string; Duration time.Duration; Response Response; Err error }`（`Response` 内不再嵌套 `Runs`）；`Response` 增字段 `Runs []EntryRun`。
  - `commandClient` 增字段 `entry int`（兼容条目 0，新条目为 YAML 下标 + 1）与 `program string`（`filepath.Base(argv[0])`）；`call` 在**每个返回路径**（成功、marshal 失败、超时、取消、非零退出、回答不可解析）都设 `resp.Runs = []EntryRun{{Entry, Program, Duration, Response: <该条回答，失败为空>, Err}}`，其他字段不变。
  - `New(opts ...Option)`：`SUGA_HOOKS=off` 同现状；`hooks.command` 为空时不构造兼容条目、不解析 `stop_args`、`post_tool_use.*`、`pre_tool_use.*`；非空时保持现有解析顺序与错误；随后 `appconfig.ReadHookEntries()`，出错原样返回；两者都空 → `nil, nil`；无新条目 → 返回兼容 `*commandClient`（现有实现）；有新条目 → 返回 `newPipeline(compat, entries, compatWaiting)`。`compatWaiting` 在 `New` 中计算：兼容条目存在且（`hooks.stop_wait_sec > 0` 或 `StopBackground()`）。每个新条目 `commandClient`：`argv=Command`、`timeout=TimeoutMS` 或兼容的 `hooks.timeout_ms` 解析结果（≤0 回落 3000）、`stopTimeout=timeout`、无 `stopArgs` 与工具过滤；`opts` 对兼容条目与每个新条目各应用一次。
  - `pipeline.go` 骨架：`type pipeline struct { compat *commandClient; entries []*commandClient; compatWaiting bool }`；`func newPipeline(compat *commandClient, entries []*commandClient, compatWaiting bool) *pipeline`；方法先全部委托兼容条目（无兼容条目时返回空值），使编译通过，由 T4/T5 替换。
  - `hooks_test.go:164`、`:293`：改为断言 `Decision`、`Reason`、`AdditionalContext`、`Notice` 为空、`UpdatedInput` 为 nil，`len(Runs)==1`、`Runs[0].Entry==0`、`Runs[0].Err != nil`；不改其他断言。
- 验证: worktree 根运行 `go test ./internal/hooks -run 'TestHookRuns|TestNewHookEntries|TestHookFailuresAreBestEffort|TestHookTimeouts|TestNewIsOffUnlessConfigured|TestNewRejectsMalformedConfig|TestHookCommandSessionEnv|TestStopInterrupt|TestPreToolUseGate|TestCancelledStopIsNotReportedAsAFailure' -count=1`。新增：`TestHookRuns`（成功、非零退出、超时、取消、回答不可解析各填一条 `Runs`，`Program` 为 argv[0] 基名；Windows 反斜杠路径用例放 `hooks_runs_windows_test.go`）；`TestNewHookEntries`：只配 `hooks.command`+`stop_args`+`stop_background: true`（无 `stop_wait_sec`）时 `New` 成功、返回 `*commandClient` 且 `stopTimeout==backgroundStopTimeout`；只配 `hooks.command`+`stop_args`（无 `stop_wait_sec`）时成功且 `stopTimeout` 等于 `timeout`；只配 `hooks.commands` 时为 `*pipeline` 且 `PostToolName("shell_exec")` 为 false；未设 `hooks.command` 而 `hooks.stop_args` 为非法类型、只配 `hooks.commands` 时 `New` 不报错；两者都空为 nil；`compatWaiting` 在 `stop_wait_sec=1` 时为真；`WithExtraEnv` 提供 `SUGA_SESSION_NAME`/`SUGA_TURN_ID` 时新条目命令收到它们，事件 `session_id` 补 `SUGA_SESSION_ID`（复用 `hooks_env_test.go` 的 `TestFakeHookEnvProcess`）。反向验证：临时去掉超时路径的 `Runs` 填充，确认 `:293` 改写后的断言失败，恢复。
- 完成标准: 上述测试通过；现有 `internal/hooks` 测试除两条断言外未改；反向验证记录。
- 依赖: T2。

### T4 流水线：普通事件与分流（D1、D8、HF18）

- 文件: `internal/hooks/pipeline.go`；新 `internal/hooks/pipeline_test.go`（含假进程 `TestPipelineHookProcess`）。
- 动作:
  - `Call(ctx, ev)`：`ev.Name=="PostToolUse"` → 有兼容条目则 `compat.Call`，否则返回空 `Response`；其余事件（SessionStart、UserPromptSubmit、SessionEnd 及未知名）→ 兼容条目（若有）在前、新条目按编号依次 `call`，失败记 Warn（文案同现有 `Call`：`"hook command failed"`，另带 `entry`）并跳过；合并：`AdditionalContext` 去空白后非空的按顺序以 `"\n\n"` 连接，`Notice` 非空的以 `"\n"` 连接，`Runs` 按执行顺序拼接；`Decision`/`Reason`/`UpdatedInput` 置空。
  - `PostToolName`/`PreToolName`/`PreTool`：兼容条目存在时委托；无兼容条目时两个 `Name` 方法返回 `("", false)`，因此 chat 不会发出这两个事件（`PreTool` 被直接调用时返回空回答与 nil）。
  - `StartStop` 暂为“全部条目按编号依次、无等待条目”的最简实现，提交信息带 `checkpoint` 字样，T5 替换；合并前必须完成 T5。
- 验证: worktree 根运行 `go test ./internal/hooks -run 'TestPipelineEvents' -count=1`：兼容条目 + 两条新条目时 SessionStart 日志顺序为 0、1、2；条目 1 `timeout` 模式且 `timeout_ms=200` 被截断（日志无 `end`）、条目 2 照常返回；条目 1 `fail` 不影响条目 2；`additionalContext` 为 `"<l0>-ctx\n\n<l2>-ctx"` 形式、notice 以换行连接；PostToolUse 只到兼容条目，无兼容条目时不启动任何进程；新条目不收 PreToolUse。反向验证：临时让 PostToolUse 也发给新条目，确认用例失败，恢复。
- 完成标准: 用例通过；反向验证记录。
- 依赖: T3。

### T5 流水线：Stop 两阶段、R0–R3 与打断（D5、D9、D10、HF2、HF3、HF13、HF15–HF17、HF19、HF21、HF25、HF28、HF29）

- 文件: `internal/hooks/pipeline.go`；新 `internal/hooks/pipeline_stop_test.go`。
- 动作:
  - `StartStop(ctx, ev)`：建流水线 ctx（`context.WithCancel(ctx)`）、`pipelineWait`（含 `done chan Result`（缓冲 1）、内部完成信号 `finished chan struct{}`、逐条结果切片）；协程中阶段一按编号执行即时条目（兼容条目即时型时最前，兼容条目调用总附加 `stop_args`）；全部执行；阶段一无 block 且 `compatWaiting` → 阶段二运行兼容条目（`stop=true` 路径）。单条失败记 Warn，取消与 `ErrTimedOut` 记 Debug（同现有 `StartStop`）。
  - 状态机（一把 `sync.Mutex`）：状态 `running(i)` / `between` / `finished`，`frozen bool`，`last int`。条目进程返回后先调用钩子 `pointEntryExited`（不持锁），再进入结束临界区：记录该条结果、`last=i`，判定下一步——
    - 无后续条目或 `frozen`：在**同一临界区**内生成合并结果：`frozen` 或流水线 ctx 已取消 → R0（`Err` 为 ctx 错误，ctx 尚未取消时为 `context.Canceled`，无决定）；否则 R1（阶段一有 block → `block`、reason 以 `"\n\n"` 连接、`Err=nil`）/R2（取等待条目结果）/R3（空）；`Notice` 为已执行条目按顺序以 `"\n"` 连接；`Runs` 按执行顺序拼接；状态置 `finished`。出锁后调用 `pointBeforeDone`（不持锁），然后向 `done` 写一次并 `close(done)`，再 `close(finished)`。
    - 有下一条：状态置 `between`，出锁后调用 `pointBeforeStartNext`（不持锁），再取锁：`frozen` → 按上一条生成 R0 结果（`Err=context.Canceled`，HPF10）并结束；否则置 `running(i+1)`、出锁后启动进程。
  - `Interrupt(ctx)`：取锁置 `frozen=true`，按状态选目标：`running(i)` → i；`finished` → `last`（不论 `done` 是否已被读出，HPF1）；`between` → 无。出锁后调用 `pointInterruptFrozen(target)`（不持锁；无目标时 entry 为 -1）；对目标发 Interrupt 事件（`Name="Interrupt"`、清 `LastAssistant` 与 `StopHookActive`，1 秒超时，经 `commandClient.Call`，失败记 Warn）；再取消流水线 ctx；等待 `finished` 信号（不从 `done` 取值）；返回值由内部逐条结果计算：所有已完成条目中 `Err==nil` 且 `Decision=="block"`、reason 非空的 reason 按执行顺序以 `"\n\n"` 连接（含 `running` 子态的那条），每条只计一次。
  - 测试钩子（HS10）：`type pipelinePoint int`，常量 `pointEntryExited`、`pointBeforeStartNext`、`pointBeforeDone`、`pointInterruptFrozen`；`var pipelineTestHook func(point pipelinePoint, entry int)`，生产为 nil，全部在出锁后调用。
- 验证: worktree 根运行 `go test ./internal/hooks -run 'TestPipelineStop|TestPipelineInterrupt' -count=1`。
  - `TestPipelineStop`：R1 跳过等待条目（等待条目日志无 Stop）；两条新条目都 `block` 时 reason 以空行连接；A `block` + B `fail` → `Err=nil` 且为 block；阶段一无 block → 兼容等待条目运行，`Err` 取其错误（`wait` 模式加 `stop_wait_sec=1` 得 `ErrTimedOut`）；即时型兼容条目在阶段一且附加 `stop_args`（HF16）；`stop_wait_sec=1`、`timeout_ms=5000` 的兼容条目归阶段二（HF17）；合并 `Notice` 为已执行条目 notice 按执行顺序换行连接（HF19）；`done` 读一次后通道已关闭；R0：父 ctx 取消时 `errors.Is(err, context.Canceled)` 且无 block；HF21/HF29（编排）：钩子在 `pointEntryExited`（A，已输出 block）处同步取消父 ctx 后返回 → 结果为 R0；钩子在 `pointBeforeDone` 处取消父 ctx 后返回 → 结果保持 R1；Stop 失败记 Warn、取消与超时记 Debug（`slog` 测试 handler 断言级别）。
  - `TestPipelineInterrupt`（编排，均不用 sleep）：
    - running（等待条目运行中）：轮询日志出现 `gofer Stop start` 后调用 `Interrupt` → 只有等待条目日志出现 `Interrupt`，返回空；
    - running 子态（HF28）：钩子在 `pointEntryExited`（A，已输出 block）处阻塞在通道 `release` 上；另一协程调用 `Interrupt`；钩子在 `pointInterruptFrozen` 收到 target=A 后关闭 `release`；断言 A 日志出现 `Interrupt`、返回值为 A 的 reason、`done` 结果为 R0（`Err` 非空）、`Interrupt` 在期限内返回；
    - between（HF3、HF25）：钩子在 `pointBeforeStartNext`（A 之后、B 之前）阻塞；调用 `Interrupt`；钩子在 `pointInterruptFrozen` 收到 entry=-1 后放行；断言无条目日志出现 `Interrupt`、B 与等待条目无 `start`、`done` 结果 `errors.Is(err, context.Canceled)`；
    - finished（HF15）：先读走 `done`（模拟转发协程），再调用 `Interrupt` → `last` 日志出现 `Interrupt`，返回值为 R1 的 reason，`Interrupt` 不挂起；
    - Interrupt 进程失败（等待条目用 `ifail` 模式）记 Warn，取消照常完成。
  - 反向验证：①临时去掉阶段一 block 跳过阶段二；②临时把 Interrupt 发给全部条目；③临时去掉 R0。各确认对应用例失败且输出不含 `[build failed]`，恢复。
- 完成标准: 用例通过；三项反向验证记录在提交说明；`grep -n pipelineTestHook internal/hooks/*.go` 只有声明、判空调用与测试赋值。
- 依赖: T4。

### T6 chat：逐条 trace、包装下的打断与后台 Stop（HF13、HF14、HF21、HF24、HPF1）

- 文件: `internal/chat/hooks_record.go`、`internal/chat/hooks_gate.go`；测试 `internal/chat/hooks_record_test.go`、`hooks_gate_test.go`、新 `internal/chat/hooks_multi_test.go`（含假进程 `TestMultiHookProcess`）。
- 动作:
  - `recordingHooks.Call`：`resp.Runs` 非空时每条调用 `recordHookRun(…, ev, Result{Response: run.Response}, run.Duration, map[string]any{"entry": run.Entry, "program": run.Program})`（不带 `Err`，status 同现状）；为空时按现状写一条。
  - Stop 记录（`StartStop` 的转发协程）：`Runs` 非空时每条以 `Result{Response: run.Response, Err: run.Err}` 记录并加两个 attrs；为空时按现状。`recordingWait` 的转发与 `Interrupt` 补取逻辑不改。
  - `hooks_gate.go`：`extra` 中在 `response.Runs` 非空时加 `entry`、`program`（取 `Runs[0]`）；拒绝与改写逻辑不改。
- 验证: worktree 根运行 `go test ./internal/chat -run 'TestRecordingHooks|TestToolGate|TestBackgroundStop|TestMultiHook|TestPersonalChatRunsConfiguredHookCommand|TestPersonalChatPostToolUseFollowsConfig|TestPersonalChatBlockStartsInjectedTurnAndMarksStopActive|TestPersonalChatKeepsWebReplyReceivedDuringInterrupt|TestPersonalChatSurvivesFailingHookCommand' -count=1`。新增 `TestMultiHook*`（`hooks.New` + `TestMultiHookProcess`）：
  - trace：只配兼容条目时每个 hook 事件带 `entry=0`/`program`，SessionStart 失败仍记 `ok`；PreToolUse 失败的记录带 `entry=0`/`program` 且 status 同现状；两条新条目时 Stop 记两条；Interrupt 不产生 hook 事件。
  - 包装下的打断（HPF1，`recordHooks(hooks.New(...), recorder)`）：running——等待条目 `start` 后调用 `recordingWait.Interrupt`，Interrupt 只到等待条目，在期限内返回；finished——转发协程已取走结果（等 `recordingWait.Done()` 可读但不读）后调用 `Interrupt`，最后执行的条目收 Interrupt，返回的 reason 恰好一次（不与 `out` 中的结果重复），不挂起。
  - 后台 Stop（`backgroundStop` + `tasks.Manager`）：正例——两条新条目、A `block`：产生一条 `Stop hook asks to continue: A-reason` 通知、`takeBlocked()` 为真；HF13——A `block`、B `wait`（日志出现 `B Stop start` 后）以新 Stop 杀掉：无该通知、`takeBlocked()` 为 false、任务以 `-1` 结束、兼容等待条目未运行；HF21——`tasks.max_duration` 设 200ms 且 B `wait`：无通知；`taskManager.Close()`：无通知。
  - 反向验证：临时去掉 T5 的 R0，确认 HF13 用例失败（ptye2e 场景不触发 R0，R0 的反向验证只在本任务与 T5 承担），恢复。
- 完成标准: 用例通过；`hooks_record.go` 与 `hooks_gate.go` 净增各不超过 25 行；反向验证记录。
- 依赖: T5。

### T7 ptye2e 真实二进制场景

- 文件: 新 `test/ptye2e/chat_multi_hooks_test.go`（首行 `//go:build ptye2e`，参照 `chat_observe_test.go` 与 `smoke_helpers_test.go` 的 `startChatOpts`）。
- 动作:
  - 假 hook `TestMultiHookHelper`（按“假进程合同”，标签 `cont`/`gofer`）：`cont` 第一次 Stop `block`（reason `continue-now`，用日志所在目录的状态文件记次数），之后 `silent`；`gofer` 的 Stop 为 `wait`。
  - `TestChatMultiHooks`：`Extra` 写 `hooks:` 下 `command:`（gofer）、`stop_wait_sec: 30`、`commands:` 一条（cont，`timeout_ms: 5000`）；发一行输入 → 等屏幕出现 `continue-now`（续跑回合）→ 轮询日志出现 `gofer Stop start` → 输入一行打断 → 退出。
  - 断言：日志中第一次 Stop 只有 `cont Stop`、无 `gofer Stop`；第二次有 `gofer Stop`；`Interrupt` 只出现为 `gofer Interrupt`；`suga inspect trace --json` 中 SessionStart、UserPromptSubmit、Stop 的 `kind=hook` 事件都带 `entry`/`program`（entry 0 与 1 各有），`entry=1` 的第一条 Stop `decision=block`；trace 中没有 `name=Interrupt` 的 hook 事件。
- 验证: worktree 根运行 `go test -tags ptye2e ./test/ptye2e -run 'TestChatMultiHooks' -count=1 -v`，输出中为 `--- PASS`（不是 `no tests to run`）。反向验证：临时去掉阶段一 block 跳过阶段二、临时把 Interrupt 发给全部条目，确认对应断言失败（确认失败来自断言而非编译错误），恢复。去掉 R0 的反向验证由 T5/T6 承担（本场景不触发 R0）。
- 完成标准: 用例在执行者的 Windows ConPTY 下通过；反向验证记录（Linux 由 T9 跑）。
- 依赖: T6。

### T8 文档

- 文件（落点按 hyj7 合入后的 main 重新定位行号）: `README.md`（“Protected settings” 段的 *exec* 清单、`Common switches` 一行附近补 hooks 说明）、`docs/usage/gofer-personal-agent.md`（“安全相关配置（protected 键）”的执行类清单、chat hooks 条目、`hooks.command` 等价配置一节）、`skills/suga-guide/references/config.md`（`hooks.*` 表加 `hooks.commands` 一行）、`CHANGELOG.md`「未发布」。
- 动作: 写明 `hooks.commands` 形状（只有 `command` 与 `timeout_ms`）、与 `hooks.command` 共存、新条目收哪些事件、Stop 先即时后等待、续跑 block 时 gofer 本回合不收 Stop 的影响（HF20）、新条目不要依赖 stderr 提示（HF19）、protected 与在终端 `suga config set` 的写法、回滚方式（含 T1 对旧版本的结论）；两处执行类清单补 `hooks.commands`；T1 走分支②时按 issue 评论补写该影响。
- 验证: worktree 根运行 `grep -n "hooks.commands" README.md docs/usage/gofer-personal-agent.md skills/suga-guide/references/config.md CHANGELOG.md`，四个文件都有结果；`grep -n "\*exec\*" README.md` 所在行与 `grep -n "^- 执行类" docs/usage/gofer-personal-agent.md` 所在行都含 `hooks.commands`；`git diff -U0 main -- CHANGELOG.md` 的改动只在「未发布」内。
- 完成标准: grep 结果符合。
- 依赖: T6（与 T7 可并行审查，顺序提交）。

### T9 全量验证、真实环境与合并（我执行）

- 文件: 无代码改动；主机脚本放 `tmp/suga-5xmf/`。
- 动作:
  - 容器内 worktree 根：`go test -race ./...`、`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...`、`make smoke-chat`（已依赖 `smoke-go`，含 Linux 上的 `TestChatMultiHooks`）。
  - 主机 Windows（gofer exec job，只写 `tmp/`）：在 worktree 跑 `go test ./...` 与 `go test -tags ptye2e ./test/ptye2e -run 'TestChatMultiHooks|TestChatObserve' -count=1 -v`。
  - 真实环境（主机 gofer exec job，脚本 `tmp/suga-5xmf/realenv.ps1`）：在 worktree 根 `go build -o <WP>/tmp/suga-5xmf/suga.exe ./cmd/suga`；以隔离 `SUGA_HOME=<WP>/tmp/suga-5xmf/home`（`config.yaml` 只含从真实配置复制的 provider 引用与 `hooks.command: [gofer, hook, generic, --agent, suga]`、`hooks.commands: [{command: [idevstd, script, continuation_hook, --agent, suga], timeout_ms: 10000}]`，`.env` 只读引用真实凭据，不复制文件）在 `<WP>/tmp/suga-5xmf/ws` 运行一回合（`"说一句话" | suga.exe chat`，结束后 `/exit`）；随后 `suga.exe inspect trace <id> --json` 确认 SessionStart、UserPromptSubmit、Stop 各有 `entry` 0 与 1 的 hook 事件、无 error 状态，gofer 侧会话列表出现该会话。续跑 block 的真实路径需要 SUPMODE 状态文件，本次不构造：完成说明写 `UNVERIFIED_REAL_ENV: 真实 continuation_hook 的 block 路径（复现：在 ws 下按 IDEV-STD 续跑合同放置状态文件后重跑同一脚本）`；gofer 无 `--wait`/有 `--wait` 的真实差异同样以 T1 结论为准并披露。
  - 合并：主机 `tmp/xhen-p1/host-merge.ps1 -Repo inhere-tools/suga -Branch feat/multi-command-hooks -MsgFile …`，以 `is_ancestor=0` 为收尾前提；CHANGELOG 已发布小节逐字未变。
  - IDEV-STD 消息草稿写进 issue 评论，要点：条目形状只有 `command` 与 `timeout_ms`（示例 YAML）；`suga config set hooks.commands` 整体替换，已有条目需合并后写入；`hooks.commands` 是 protected 键，必须由人在 suga 之外的终端执行，agent 会话或管道 stdin 会被拒（退出码 2），所以安装命令应打印给用户而不是代执行；需要同时包含 `suga-hyj7` 与本特性的 suga 版本（尚未发布，版本号待发版确定）。发送时机：合并到 main 之后、发版前准备草稿，发版后由你确认发送——形状虽在设计 0.6 已定，但只有 T9 全绿后才确认可用，版本号也要发版后才能给出。
- 验证: 以上命令全部通过，Windows 输出含 `--- PASS: TestChatMultiHooks`；真实环境 trace 检查结果与 `UNVERIFIED_REAL_ENV` 行记录在 issue 评论。
- 完成标准: 全绿并合并到本地 main；真实环境验证已记录。
- 依赖: T7、T8。

## 回滚与恢复

- 每个任务至少一个提交，恢复点为各任务提交；合并前在 worktree 分支上，主分支不受影响。
- 合并后回滚：revert 合并提交；用户侧删掉 `hooks.commands` 即回到单命令行为。
- hyj7 若合入后被回滚：本分支依赖 `ConfigKey.Protected` 与分类表，停在 T2 前，按 Semantic Amendment 处理。

## 人工 Gate

1. 计划批准（本稿评审后），同时指定 T2–T8 执行者模型。
2. 当前执行请求：你指示开始实施。执行请求可先只覆盖 T1；T2 起另需前置条件（hyj7 已合入 main）满足。该请求覆盖建 worktree 分支 `feat/multi-command-hooks` 与合并到本地 main，不含 push。
3. T1 分支②：gofer 无 `--wait` 会打开 turn 时，停下请求确认，确认后继续 T2。
4. 外部动作 Gate：push、tag、发版、安装、向 IDEV-STD 会话发送配置形状，均需你另行逐项批准；本计划只准备消息草稿。

## 可追溯性

| 设计条目 | 任务 | 验证 |
|---|---|---|
| AC1、D7、D11（新条目与兼容条目共存） | T2、T3、T4、T7、T9 | `TestReadHookEntries`、`TestNewHookEntries`、`TestPipelineEvents`、`TestChatMultiHooks`、真实环境 trace |
| AC2、D9、D10（Stop 两阶段、block 合并） | T5、T6、T7 | `TestPipelineStop`、`TestMultiHook*` 后台正例、`TestChatMultiHooks` + 反向验证 |
| AC3、D2、HF1、HF15、HF23、HF26（兼容不变） | T3、T6 | 现有 `internal/hooks` 与 chat hook 测试、`TestNewHookEntries` 兼容用例 |
| AC4（单独超时） | T2、T4 | `TestReadHookEntries`、`TestPipelineEvents` 各自超时 |
| D8（顺序执行、各自超时） | T4、T5 | `TestPipelineEvents` 顺序与超时、`TestPipelineStop` |
| D1、D4（包归属） | T2、T3、T4 | 文件清单与审查 |
| D3（格式不变） | T4、T5 | 现有回答解析测试不改 |
| D5、R0–R3、HF2、HF13、HF21、HF29 | T5、T6 | `TestPipelineStop`、`TestMultiHook*` 后台用例 |
| 打断三态、HF3、HF25、HF28、HPF1 | T5、T6、T7 | `TestPipelineInterrupt`、`TestMultiHook*` 包装下的打断、`TestChatMultiHooks` |
| HF16、HF17（`stop_args`、等待型判定） | T3、T5 | `TestNewHookEntries`、`TestPipelineStop` |
| HF18（分流与启用） | T3、T4 | `TestNewHookEntries`、`TestPipelineEvents` |
| HF14、HF24（逐条 trace、Interrupt 不进 trace） | T3、T6、T7 | `TestHookRuns`、`TestRecordingHooks`、`TestToolGate`、`TestMultiHook*`、ptye2e trace 断言 |
| HF12（`filepath.Base`、Windows 反斜杠） | T3、T9 | `hooks_runs_windows_test.go`、主机 Windows `go test ./...` |
| HF19（Notice 时机、文档提示） | T5、T8 | `TestPipelineStop` Notice 断言、T8 文档 |
| HF20（gofer 影响描述） | T1、T8 | issue 评论、T8 文档 |
| HF22、HF27（环境） | T3 | `TestNewHookEntries` |
| D6、D12（protected、Reader、分类表、`config get` YAML、发布顺序） | 前置条件、T2 | `TestProtectedClassification`、`TestConfigHooksCommands` |
| D13（不纳入项目级） | 排除项 | — |
| HS10（测试钩子形式） | T5 | grep 与审查 |
| HS11（gofer 无 `--wait`） | T1、T8 | issue 评论、文档 |
| 文档、回滚说明 | T1、T8 | T8 grep |
| 验证要求（`-race`、Linux ptye2e、Windows） | T9 | 全量命令与主机 job |
| 真实边界（SR1402.1） | T9 | 真实环境 trace、`UNVERIFIED_REAL_ENV` 行 |
| 设计结论：配置形状回复 IDEV-STD（外部动作 Gate） | T9 | issue 评论中的消息草稿 |
| 合并到本地 main | T9 | `host-merge.ps1` 输出 `is_ancestor=0` |

## 完成 Gate 与剩余工作

- 完成条件：T1–T9 完成标准全部满足、合并到本地 main，issue `suga-5xmf` 评论附验证结果后关闭；gofer plan 的 todo 全部标 done，plan 状态置 done。
- 进入 `COMPLETE` 时写 ≤8 行完成总结（主旨、状态、自主决策、提交数与区间、耗时、剩余项），并按 plan-closure 做一次 retrospective 判定与提示（仅首次 `COMPLETE` 写入时提示一次）。
- 剩余：发版与推送待你授权，且须与 `suga-hyj7` 同版或其后（D6）；向 IDEV-STD 会话发送配置形状待你确认；HS6 glob 文案另开 Quick issue。
