<!-- template_id: plan; template_version: 1.3.1 -->
# suga 后台分身（fork） 实施计划

> 状态：Draft 0.3 / 已批准实施（2026-11-26）；issue `suga-hao6`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-26 | Claude | 初稿：按已批准设计 0.4 拆为 T1–T9 |
| 0.2 | 2026-11-26 | Claude | 处理第 1 轮两轴计划评审（`docs/review/2026-11-26-background-fork-plan/`），见下表；kill 按用户 Q3 选 A 沿用 6gcy 行为 |
| 0.3 | 2026-11-26 | Claude | 处理第 2 轮计划评审（PF10–PF14、PS6，用户同意第 3 轮：“hao6 Q6 选 A”），见第 2 轮处理表；设计引用改为 0.5 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

第 1 轮处理：

| 发现 | 处理 |
|---|---|
| PF1（阻断） | T1 写定 `ExpandToolCalls` 只分配 lead 文字、reasoning、元数据与批次 meta；`persistToolTranscript` 的 ToolCall 仍取 finalCall；分身尾部用模型原调用 |
| PF2 | T1 加只经 `Run` 入口的 characterization 用例，先在基线 `00c890f7` 回放通过 |
| PF3（阻断） | T6 补 `chat.Options.StartFork`、`wiring.Runtime` 暴露、`cli/chat.go` 传参；agents 未启用时提示；删去“净增 ≤3 行” |
| PF4、PS1（阻断，kill） | 用户 2026-11-26：“hao6 Q3 选 A”。T4 不改 killed 分支；T4 单测与 T7 ③ 断言无通知、output 为“已终止”、`stop=cancelled`；设计关键流程 3 已同步修正 |
| PF5、PS1（阻断，观测） | T2 写 `fork_reuse` 与 `fork_reuse_note` attr；T3 写会话 meta `fork_base`；各一条单测 |
| PF6、PS2 | 三处文案指定任务与 i18n 键（见“i18n 键”） |
| PS3 | T5 补 `compact.go:838`；i18n catalog 文件进清单，模型可见文案放现有 `[model]` 段、占位符 `{name}`，`TestCatalogParity` 进验证 |
| PF7 | 改为可选接口 `native.ForkSeeder`，T4 不动 wiring 也能编译；每个任务验证加 `go build ./...` |
| PF8 | T1 的 tracker 记 turn id 与结束方式；`:bg` 配置了 host hook 时无论 prefix 还是重建都拒绝（你的决定；0.2 曾误扩到模型触发，见第 2 轮 PF11） |
| PF9 | 用例名改为实际存在的并纳入 `TestRunningTurnPutsRewindAndForkBack`；T3 反向验证②父策略放行 `file_write`；T5 补超阈值；kill 补单测 |
| PS4 | 新增“与设计代码组织的差异”；T8 CHANGELOG 覆盖 `pkg/agentcore` 公开 API 变化 |
| PS5 | 人工 Gate 列出仪式成本数字 |

第 2 轮处理：

| 发现 | 处理 |
|---|---|
| PF11（阻断） | 删去 T1 `ForkSeedForCall` 的 host hook 拒绝：模型触发一律用当步请求，ACP 的 TurnGate 照常启动；T5 `:bg` 表不变。0.2 中该条为监督者决定，有误，按 PF11 撤回 |
| PF10（阻断） | T2 加 `observe.go`：recorder 从 `Config.ForkSeed` 取值，只在该会话第一个无 purpose 的 model 事件写 `fork_reuse`/`fork_reuse_note`；不走 `ModelEvent.Metadata` |
| PF12 | `ForkUnavailableError` 带 `hint`，由 Runner 填译文，`Hint()` 返回译文 |
| PF13 | T4 定义 `native.ForkLimitError{Running, Limit}`，`StartFork` 用 `Running()`/`Limits()` 预检后返回；native 与 `cmd.bg.full` 都取它 |
| PF14 | characterization 用保留原始消息的 storage 包装，不用 `newRuntimeTestStorage` 读落库消息；回放命令先 `mkdir -p`、放 WP 级 `tmp/`、在容器或主机 pwsh 7 解包 |
| PF5 附注 | `observeTurnEnd` 在 `runner.go` 的 `runtime.Run` 返回之后、`r.finish` 之前调用；`completed = runErr == nil && coreResult.Status == RunCompleted` |
| PS6 | 设计升 0.5（kill 不通知，沿用 6gcy；附 Q3 原文）；本计划改引 0.5 |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：落实设计 0.5——模型（`agent` 的 `type: fork`）或 `:bg` 派出只读分身，分身以主会话基准请求为前缀起步、在后台运行、只回交结论。
- scope freeze：设计 0.5 的总体方案、D1–D5、F1–F10 与“验证要求”。不含设计非目标与 deferred（`recall` 进白名单）。
- 扩围策略：`DEFER_OR_REQUEST`；review budget：按 SR1409 Full 档两轴评审，轮次按 `planning-core-rigorous`；停止条件：无开放 `CORE_BLOCKING`。
- 暴露度：Full（依据见设计“安全、数据、运维与回滚”第 1 条）。

## 目标与完成定义

完成定义（全部满足）：
1. 模型调用 `agent`（`type: fork`）或 chat 输入 `:bg <任务>` 时，分身作为 `Kind:"agent"` 后台任务启动，主会话继续；`:tasks`、状态行可见；kill 后无通知、output 为“已终止”、`stop=cancelled`。
2. 分身首个请求的 System/Tools/Model 与基准请求相同、Messages 以基准 Messages 为前缀（`fakeLLM.Requests()` 逐字节断言）；分身首个 model 事件带 `fork_reuse`，子会话 meta 带 `fork_base`。
3. 分身只执行 explore 白名单；一切拒绝返回 F9 普通错误，不触发每回合拒绝上限；上下文到阈值以 `stop=context` 收尾。
4. 通知按 D4（仅 `type: fork`）；普通后台 agent 通知不变；满额直接拒绝。
5. 设计“验证要求”全部落到测试并通过（单测、GR110、ptye2e Linux 与 Windows ConPTY、反向验证）；真实 provider 的命中率已记录（不设门槛），或按 `UNVERIFIED_REAL_ENV` 披露。

## 范围、排除项与授权

- 范围：suga 仓库 `pkg/agentcore`（`runtime.go`、新 `expand.go`）、`pkg/agentcore/tools/native/agent.go`、`internal/personal`（新 `subagent_fork.go`、`subagent_fork_seed.go`，`prefix.go`、`context.go`、`runner.go`、`subagent.go`、`segment_handoff_reuse.go`、`compact.go` 一处调用）、`internal/wiring`（`subagent.go`、新 `subagent_fork.go`、`runtime.go`）、`internal/chat`（新 `chat_fork_bg.go`、`chat.go`、`steering.go`、`chat_usage.go`）、`internal/cli/chat.go`、`internal/i18n/catalog/{zh,en}/{model,cmd}.ini`、测试、`test/ptye2e` 新场景、README、`skills/suga-guide/references/`、CHANGELOG。
- 排除：分身写文件、shell、上网、`recall`；续聊分身；ACP 回合外推送；gofer 契约；kill 通知；发版、推送、安装。
- 授权：本计划批准只授权进入实施请求；实施需你另行指示。当前执行请求将覆盖：建 worktree 分支 `feat/background-fork`、在其上本地提交、合并到本地 main；不含 push、tag、发版、安装。
- `host_or_non_offline_action=REQUIRED`：主机上建 worktree `.worktrees/fork`（`tmp/xhen-p1/wt-repo.ps1`）、合并到本地 main（`tmp/xhen-p1/host-merge.ps1`）；执行者 `gofer job -a suga` 在主机运行并调用模型 provider（联网、产生费用）；主机 Windows 测试；T9 真实 provider 调用；`staticcheck@2026.2.1` 联网下载模块。均不推送、不发布。

## 输入与批准证据

- 设计：`docs/design/2026-11-26-background-fork-design.md` Draft 0.5（0.4 批准，提交 `a8a2bc83`；0.5 仅含 Q3 修正）。
- 批准原话（2026-11-26）：“批准 suga 后台分身设计 0.4（接受未复核修订），开始写计划”；Q3：“hao6 Q3 选 A”。
- 设计评审：`docs/review/2026-11-26-background-fork/`（两轮两轴 PASS，ledger `f1201c30`）。计划评审：`docs/review/2026-11-26-background-fork-plan/`（第 1 轮）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 后台运行、通知、kill、用量与污染传递 | `native.AgentTool.startBackground`、`tasks.Manager`、`personal.RunChild`、`wiring.subAgents` | 是 | 原位扩展（`type: fork` 分支） | DIRECT_REUSE | none | 无 |
| CAP-02 | 主会话请求前缀与重建 | `prefixTracker.lastRequest`、`requestBaseBetweenTurns`、`prepareHandoffReuse` 的回退判定 | 是 | 既有 owner 扩展（tracker 加当步回复、已执行结果、turn 结束方式；重建加“跳过指纹比对”参数） | OWNER_EXTENSION | tracker 不存回复与工具结果（设计 BF2） | 与交接包沿用共用重建，不复制 |
| CAP-03 | 逐调用 assistant 展开 | `pkg/agentcore/runtime.go` `persistToolTranscript` 与 `leadOutput` | 否（未导出） | 既有 owner 扩展（导出同一分配规则，runtime 改为调用它） | OWNER_EXTENSION | 分身须与落库同规则展开，复制实现会漂移 | runtime 与分身共用一份 |
| CAP-04 | 只读执行与拒绝 | `subAgentTools` 白名单、`tools.NewApproval`、`guardExternalOrigin` | 是 | 薄包装（Approval 外层把拒绝转为普通错误） | OWNER_EXTENSION | 现有拒绝是 `ErrDenied`，会触发每回合上限（BF1、BF14） | 只在分身装配中使用 |
| CAP-05 | 伪终端与分发测试 | `test/ptye2e` `startChatOpts`、`fakeLLM.Requests()`、`chat_agent_test.go`；`internal/cli/subagent_test.go` `TestRunWithSubAgent` | 是 | 新场景文件 | DIRECT_REUSE | none | 无 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| NONE | none | none |

与设计“代码组织”的差异（PS4）：
- `pkg/agentcore` 新增导出 `ExpandToolCalls`（新文件 `expand.go`）：分身尾部须与落库同一分配规则，否则签名 thinking 元数据的位置会漂移（CAP-03）；属公开 API 新增，进 CHANGELOG。
- `native` 新增可选接口 `ForkSeeder`，不改 `SubAgents`：外部实现方不受影响，T4 提交可单独编译（PF7）。
- `:bg` 经 `chat.Options.StartFork` 调 native 的启动路径，而非“只调 personal 层”：满额、Kind、通知必须与模型触发走同一份 `startBackground` 逻辑；chat 本就依赖 native，依赖方向不变。

i18n 键（模型可见文案放现有 `[model]` 段，chat 文案放 `[cmd]` 段，占位符 `{name}`，中英两份，`TestCatalogParity` 须过）：

| 键 | 任务 | 中文原文 |
|---|---|---|
| `model.fork.directive` | T1 | 你是主会话派出的分身，带着上面的全部对话。只读：不能写文件、执行命令或上网。任务：{task}。最终回答就是交回主会话的报告，不超过 4000 字符，结论附依据的文件:行号。 |
| `model.fork.not_run` | T1 | 主会话在派出分身后才执行此调用，分身中未执行 |
| `model.fork.wrapup_context` | T2 | 已到分身的上下文上限。不要再调用工具，直接给出报告；报告首行写明“因上下文上限收尾”。 |
| `model.fork.denied` | T3 | 分身只读：{name} 在分身中不可用，不要重试；需要改动或命令时写进报告由主会话执行。分身读不到主会话的 recall ref，需要时重读文件。 |
| `model.fork.too_large` | T1 | 主会话上下文已接近上限，分身无法带全部上下文启动；改用 agent 的 explore 类型并在 prompt 里写清背景 |
| `cmd.bg.usage` / `cmd.bg.started` / `cmd.bg.unavailable` / `cmd.bg.full` / `cmd.bg.disabled` | T6 | 用法：:bg <任务> / 分身 {id} 已在后台启动：{desc} / 无法启动分身：{reason} / 后台任务已满（{running}/{limit}），用 :tasks 查看或 :tasks -k <ID> 终止后重试 / 分身不可用：agents 未启用或没有后台任务 |

`pkg/agentcore/tools/native` 不依赖 i18n，其文案沿用该文件现有写法（英文硬编码）：启动行 `[background fork task %s started: %s]`、满额 `agent: %d background tasks are running (limit %d); task wait or task kill one before starting a fork`。

## 前置检查与 fail-closed 条件

- workspace baseline（2026-11-26，0.1 写作时）：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；branch `main`；HEAD `a8a2bc83`；他人改动 `docs/design/2026-11-26-trusted-memory-backend-design.md` 与 `.gofer/tracker/issues.jsonl`（保留不动，不暂存）。expected symbols：`AgentTool.startBackground`、`RunChild`、`subAgents.build`、`prefixTracker.observeRequest/lastRequest`、`requestBaseBetweenTurns`、`persistToolTranscript`、`Runner.turnHooks`、`denialLimitHook`、`chat.Options`。实施开始时重新记录 HEAD 与 status。
- 进度跟踪：执行开始时 `gofer plan create --title "suga 后台分身实施" --project my-tools-dev --desc "仓库 inhere-tools/suga；计划 docs/plans/2026-11-26-background-fork-plan.md"`，再 `gofer plan add-todo <plan-id> "T1 …" --cwd inhere-tools/suga --after prev …` 建 T1–T9 todo；每个执行者 job 挂到对应 todo。
- worktree：主机 `gofer job run -p my-tools-dev -a exec --runner local --sync -- pwsh -File tmp/xhen-p1/wt-repo.ps1 -Repo inhere-tools/suga -Name fork -Branch feat/background-fork`（基于当时的 main）。容器内不 `git worktree add/prune`；对 worktree 的 git 命令只用单条前缀 `GIT_DIR`/`GIT_WORK_TREE`，不 export，不与测试同一命令。
- 执行者：T1–T8 由 `gofer job -a suga` 在 `.worktrees/fork` 顺序实施，每任务一个 job、≤45 分钟（`--timeout 5400`）；模型由你在计划 Gate 指定。核心任务（建议推理更强的模型）：**T1**（展开合同与 F10 尾部）、**T2**（forkHeadHook 与分身上下文模式）、**T3**（拒绝转换与晚绑定）、**T5**（F7 基准判定）；T4、T6、T7、T8 可用 `opencode-go/deepseek-v4.1-flash`。我审查（先看 `git diff --stat` 删除行）并复核反向验证；T9 由我执行。
- 任务书约束（每个 job 都写）：只改本任务列出的文件；不删注释、不重排无关代码；不用 web_fetch；可改 go.mod/go.sum（预期不改）；遵守 GR104、GR107–GR110 与 `pkg/agentcore` 不依赖 `internal/*`；只暂存本任务文件，提交前核对 `git diff --cached --name-only`；禁用 `git stash`、`git reset`、`git checkout -- <他人路径>`；首个提交不晚于开始后 20 分钟；`--sync`，不起空等后台任务；不在 export 了 `GIT_DIR`/`GIT_WORK_TREE` 的命令里跑测试。
- 测试环境：执行者在 Windows 主机，无 cgo，任务内 focused 测试不带 `-race`；每个任务验证先跑 worktree 根 `go build ./...`；`-race`、Linux ptye2e、staticcheck 在 T9 由我在容器里跑。测试用临时 `SUGA_HOME`；断言用 `github.com/gookit/goutil/x/assert`，多用例 `t.Run`（SR2002）。
- 反向验证写法：临时改动必须可编译，确认输出含对应断言失败且不含 `[build failed]`，恢复后写进提交说明。
- fail-closed：`go build ./...`、focused 测试或反向验证不符合预期即停止，按实施发现四类记录；遇到他人改动（Ownership Conflict）停止。
- 依赖：不新增第三方依赖。

## 波次与依赖

| 波次 | 任务 | 说明 |
|---|---|---|
| W1 | T1 → T2 | 展开、当步记录与种子；请求头、分身上下文模式与 `fork_reuse` |
| W2 | T4 → T3 | native 工具、可选接口与通知；wiring 装配、拒绝转换与 `fork_base` |
| W3 | T5 → T6 | 回合外种子；`:bg` 与接线 |
| W4 | T7、T8 | 分发与 ptye2e；文档 |
| W5 | T9 | 全量、主机 Windows、真实 provider、合并（我执行） |

## 任务

### T1 展开合同、当步记录与模型触发的种子（F1、F3、F6、F10、PF1、PF2、PF8）【核心】

- 文件: `pkg/agentcore/runtime.go`、新 `pkg/agentcore/expand.go`；`internal/personal/prefix.go`；新 `internal/personal/subagent_fork_seed.go`；`internal/personal/runner.go`（turnHooks 一项、`runtime.Run` 返回后一处）；`internal/i18n/catalog/{zh,en}/model.ini`（`model.fork.directive`、`not_run`、`too_large`）；测试 `pkg/agentcore/expand_test.go`、`pkg/agentcore/runtime_transcript_test.go`、`internal/personal/subagent_fork_seed_test.go`。
- 动作（签名为合同）:
  - `func ExpandToolCalls(res *GenerateResult) []Message`：每个调用一条 assistant 消息，只分配 lead 文字、`Reasoning`、provider 元数据（只在第一条）与 `ToolBatchMetaKey`（多调用时每条都有）；`ToolCall` 是模型回复中的原调用。`persistToolTranscript` 取第 i 条的 Content/Reasoning/Metadata，`ToolCall` 仍取 finalCall（代理展开、强转、hook 改写之后）；分身尾部用原调用（与落库形态的差异可接受：分身不重放主会话历史）。
  - characterization（PF2）：`TestRuntime_Run_ToolTranscriptLeadOnFirstCall`，只经 `New(Config{...}).Run(...)`；三调用并行回复带 Content/Reasoning/Metadata，第二个走 `CallExpander` 代理、第三个参数需强转；断言落库 assistant 消息 lead 只在第一条、批次 meta 每条都有、ToolCall 是展开与强转后的调用。用例自带只追加原始消息的 storage 包装读落库消息（`newRuntimeTestStorage` 的 `cloneMessages` 会丢 Reasoning，不改 `runtime_doubles_test.go`；参考 `<WP>/tmp/review/suga-fork-plan/r2-characterization_test.go.txt`）。先回放（容器内或主机 pwsh 7，不用 PowerShell 5 管道）：WP 根 `mkdir -p tmp/suga-hao6/base && git -C inhere-tools/suga archive 00c890f7 | tar -x -C tmp/suga-hao6/base`，拷入该用例，在 `tmp/suga-hao6/base` 运行 `go test ./pkg/agentcore -run TestRuntime_Run_ToolTranscriptLeadOnFirstCall -count=1` 通过，再改代码。
  - tracker：`observeStep(sessionID, turnID string, res *agentcore.GenerateResult)`（AfterModel，清空已执行结果）、`observeToolResult(sessionID, callID string, res agentcore.ToolResult)`（AfterTool）、`observeTurnEnd(sessionID, turnID string, completed bool)`（回合结束时，`completed` 为正常结束）；`lastStep(sessionID) (step forkStep, ok bool)`，`forkStep{Result; Done map[string]agentcore.ToolResult; TurnID string; Completed bool}`；`clearRequest` 一并清掉。由 `forkStepHook` 挂进 `turnHooks`；`observeTurnEnd` 在 `runner.go` 的 `runtime.Run` 返回之后、`r.finish` 之前调用，`completed = runErr == nil && coreResult.Status == agentcore.RunCompleted`。
  - `type ForkSeed struct { Request *agentcore.GenerateRequest; Model, ProviderName, BaseTurn, Source, Reuse, ReuseNote string }`（`Source` 为 `call|bg`，`Reuse` 为 `prefix|rebuilt`）；`type ForkUnavailableError struct{ Reason, hint string }`，`Error()` 为 `fork unavailable: <reason>`，原因 `host_hook`、`prompt_state`、`no_request`、`context`；`context` 时由 Runner 用 `r.cfg.Translator` 把 `model.fork.too_large` 译好填入 `hint`，`Hint()` 返回译文（不暴露键名）。
  - `func (r *Runner) ForkSeedForCall(ctx context.Context, sessionID, callID, task string) (ForkSeed, error)`：不取会话 gate；不判 host hook（模型触发一律用当步请求，ACP 的 TurnGate 照常启动）；基准 = `lastRequest`；尾部 = `ExpandToolCalls(当步回复)` 逐条接结果：排在 `callID` 前的填真实结果，`callID` 填 `model.fork.directive`（`{task}`），之后的填 `model.fork.not_run`；新切片；Model/ProviderName 取 Runner 当前值；估算超过 `thresholdOf(window, reserve)` → `context`。
- 验证: worktree 根 `go build ./...`；`go test ./pkg/agentcore -run 'TestExpandToolCalls|TestRuntime' -count=1`；`go test ./internal/personal -run 'TestForkSeedForCall' -count=1`（带 reasoning 与签名元数据只在第一条、三调用批次中分身居中前真后占位、基准切片前后不变、超阈值 `context` 且 `Hint()` 等于译文、配置 host hook（或 ACP TurnGate）时仍启动且 `Reuse=prefix`、`observeTurnEnd` 记录取消回合为 `Completed=false`）；`go test ./internal/i18n -run TestCatalogParity -count=1`。反向验证：①临时让 `persistToolTranscript` 落库 `ExpandToolCalls` 的 ToolCall，确认 characterization 用例失败；②临时让前面的调用也填占位，确认“真实结果”断言失败。
- 完成标准: 基线回放与改动后均通过；两项反向验证与回放命令写进提交说明。
- 依赖: 无。

### T2 forkHeadHook、分身上下文模式与 `fork_reuse`（F2、F6、BF12、BF13、PF5）【核心】

- 文件: 新 `internal/personal/subagent_fork.go`；`internal/personal/context.go`；`internal/personal/observe.go`；`internal/personal/subagent.go`（`StopContext = "context"`）；`internal/personal/runner.go`（`Config.ForkSeed *ForkSeed`）；`internal/i18n/catalog/{zh,en}/model.ini`（`model.fork.wrapup_context`）；新 `internal/personal/subagent_fork_test.go`。
- 动作:
  - `Config.ForkSeed` 非 nil 即分身模式：`contextBuilder` 不渲染段上下文消息、hookContext、ephemeral，不折叠、不段断点、不 shrink。
  - `type forkHeadHook struct{ agentcore.NopHook; seed *ForkSeed; prompt string; maxInput int; first bool }`，挂在分身 Runner hook 链首位（prefixHook 之前）。`BeforeModel`：System、Tools、Model 取种子；Messages = 新切片（种子 Messages + 本次请求去掉内容等于 `prompt` 的首条 user 消息后的其余消息）；`ToolChoice` 为空保持为空，非空（收尾重试）原样保留。`fork_reuse`：recorder 在 `personal.New` 时从 `Config.ForkSeed` 取 `Reuse/ReuseNote`，`attemptObserver` 只在该会话第一个无 purpose 的 model 事件写 `fork_reuse`（及非空的 `fork_reuse_note`），之后不再写。`AfterModel`：上次调用上报的输入 token ≥ `maxInput` 时 `ctrl.RequestWrapUp(StopContext, model.fork.wrapup_context)`。
  - `RunChild` 识别 `res.WrapUp == StopContext`，`Stop=context`。
- 验证: worktree 根 `go build ./...`；`go test ./internal/personal -run 'TestForkHeadHook|TestForkContextMode' -count=1`（录请求的假 provider）：首个请求 System/Tools/Model 等于种子、Messages 以种子 Messages 为前缀且不含 Prompt 与子会话段上下文；第二步只在末尾追加；收尾重试保留 `ToolChoiceNone`；上报输入达阈值时 `Stop=context` 且有报告；`inspect` 读到的 trace 中只有首个无 purpose 的 model 事件带 `fork_reuse`，`rebuilt` 时带 `fork_reuse_note`，非分身会话无该 attr；前缀统计 attr 记改写后的请求；`go test ./internal/i18n -run TestCatalogParity -count=1`。反向验证：临时把 hook 挂到 prefixHook 之后，确认“前缀统计”断言失败。
- 完成标准: 用例通过。
- 依赖: T1。

### T4 native：`type: fork`、可选接口与通知（D2、D4、D5、PF4、PF6、PF7）

- 文件: `pkg/agentcore/tools/native/agent.go`；新 `pkg/agentcore/tools/native/agent_fork_test.go`。
- 动作:
  - 常量 `AgentFork = "fork"`；schema `type` enum 加 `fork`，描述补一句“fork 带着本对话全部上下文，在后台运行，只读”；`fork` 时忽略 `background`，总走后台；未挂 tasks 时报错 `agent: fork needs background tasks`。
  - 可选接口 `type ForkSeeder interface{ ForkSeed(ctx context.Context, req AgentRequest) (any, error) }`；`SubAgents` 不变；`agents` 未实现 `ForkSeeder` 时报 `agent: fork is not available`。`AgentRequest` 增 `Seed any`；种子错误作为工具错误结果返回 `agent: <err>`，错误实现 `Hint() string` 时追加提示。
  - `func (t *AgentTool) StartFork(ctx context.Context, req AgentRequest) (tasks.Info, error)`：fork 的后台启动（`Execute` 与 `:bg` 共用）；先用 `tasks.Running(sid)`/`Limits()` 预检，满额返回 `*ForkLimitError{Running, Limit int}`（`Error()` 为上文英文满额文案）。返回给模型的启动行 `[background fork task %s started: %s]`。
  - 完成分支（仅 fork）：`utf8.RuneCountInString(strings.TrimSpace(report)) <= 4000`（不含 footer）时通知 = 全文 + footer，否则前 12 行 + `[report cut; task output reads it all]` + footer；task label `agent_type=fork`；`Kind:"agent"`、Summary `agent: <description>` 不变。普通 agent 分支与 killed 分支（`r.Killed()`：output 写“已终止”、不通知）均不改。
  - `Stop` 注释加 `context`。
- 验证: worktree 根 `go build ./...`；`go test ./pkg/agentcore/tools/native -run 'TestAgentTool' -count=1`（假 `SubAgents`+`ForkSeeder`）：3999/4000/4001 rune（含多字节字符）的通知分界；普通后台 agent 12 行通知不变；启动行与满额文案；`Kind`/Summary/label；kill 后无通知、output 为“已终止”、label `stop=cancelled`；未实现 `ForkSeeder` 时的错误；`go test ./pkg/agentcore -run TestLayering -count=1`。
- 完成标准: 用例通过；`internal/wiring` 未改仍能编译。
- 依赖: T2。

### T3 wiring：晚绑定、分身装配、拒绝转换与 `fork_base`（F3、F9、BF14、PF5）【核心】

- 文件: `internal/wiring/subagent.go`；新 `internal/wiring/subagent_fork.go`；`internal/wiring/runtime.go`（`personal.New` 之后绑定一行；该文件已 964 行，fork 连线放 `subagent_fork.go`）；`internal/personal/subagent.go`（`ChildRequest.Meta` 合入子会话 meta）；`internal/i18n/catalog/{zh,en}/model.ini`（`model.fork.denied`）；新 `internal/wiring/subagent_fork_test.go`。
- 动作:
  - `type forkHost struct{ mu sync.Mutex; runner *personal.Runner }`，`bind(r *personal.Runner)`，同 `memoryHost` 在 `personal.New` 之后绑定；`subAgents` 增字段 `fork *forkHost` 并实现 `native.ForkSeeder`（经 `Runner.ForkSeedForCall`）。
  - `RunAgent` 在 `req.Type == native.AgentFork` 时：以种子的 Model 调 `loadRuntimeSettings`，注册 explore 白名单，`personal.Config.ForkSeed` 设为种子；`ChildRequest.Meta = {"fork_base": "<BaseTurn>|<Source>"}`。
  - `type forkDenyTools struct{ inner agentcore.ToolExecutor; allow map[string]bool }`：包在 Approval 外层；名字不在白名单 → `IsError` 结果，内容 `model.fork.denied`（`{name}`）；白名单内调用返回 `tools.ErrDenied`（ask 规则无终端时失败关闭）同样转为 `IsError` 结果。不调用 `SetDisabled`。
- 验证: worktree 根 `go build ./...`；`go test ./internal/wiring -run 'TestForkRunner|TestSubAgent' -count=1`：连续 3 次 `file_write` 后作答——每次结果为 F9 文案、临时目录目标文件未创建、报告为作答内容、`Stop=final`；对读路径配 ask 规则后连续 3 次 `file_read` 同样 `Stop=final`；`agent`、`tool_call`、`mcp__x__y` 文案一致；`:model` 切换后分身请求 Model 为新模型；子会话 meta `fork_base` 为 `<turn>|call`；现有 `TestSubAgent*` 不改即过；`go test ./internal/i18n -run TestCatalogParity -count=1`。反向验证：①临时把拒绝改回 `ErrDenied`，确认“3 次写调用”失败；②父策略放行 `file_write`（使白名单成为唯一屏障）后临时注册全部工具，确认“F9 文案、文件未创建”断言失败。
- 完成标准: 用例通过；两项反向验证记录。
- 依赖: T4。

### T5 回合外种子（F7、BF11、PF8）【核心】

- 文件: `internal/personal/subagent_fork_seed.go`；`internal/personal/segment_handoff_reuse.go`；`internal/personal/compact.go`（`:838` 调用方传 `false`）；`internal/personal/subagent_fork_seed_test.go`。
- 动作:
  - `func (r *Runner) ForkSeedBetweenTurns(ctx context.Context, sessionID, task string) (ForkSeed, error)`，判定：

    | 情形 | 结果 |
    |---|---|
    | 配置了 host hook（任何情形） | `host_hook`，不启动 |
    | `lastStep` 存在、`Completed`、TurnID 为最后完成的回合、模型未变 | `prefix`，尾部 = 该步最终回复 + user `model.fork.directive` |
    | `--resume` 后、`:compact`、段断点、`:rewind`、段清理、取消或失败回合、`:model` 切换 | `requestBaseBetweenTurns(..., skipPrint=true)` 重建 → `rebuilt`，`ReuseNote` 记原因 |
    | 重建返回 `prompt_state` | `prompt_state`，不启动 |
    | 会话没有任何完成的回合 | `no_request`，不启动 |
    | 种子超阈值 | `context`，不启动 |

  - `requestBaseBetweenTurns` 增参数 `skipPrint bool`；交接包调用方（`compact.go:838`）传 `false`，行为不变。
- 验证: worktree 根 `go build ./...`；`go test ./internal/personal -run 'TestForkSeedBetweenTurns|TestHandoffReuse' -count=1`：正常回合后 `prefix`；`--resume`（新 Runner 同库）、取消回合、`:compact`、`:rewind`、`:model` 切换后均 `rebuilt`；正常回合后与 `:compact` 后配置 host hook 均为 `host_hook`；超阈值 `context`；现有 `TestHandoffReuse*` 不改即过。反向验证：临时把 `skipPrint` 固定为 false，确认 `:compact` 用例失败。
- 完成标准: 用例通过。
- 依赖: T3。

### T6 chat：`:bg` 与接线（D1、PF3）

- 文件: 新 `internal/chat/chat_fork_bg.go`、`internal/chat/chat_fork_bg_test.go`；`internal/chat/chat.go`（`Options` 一字段、分发一行）；`internal/chat/steering.go`（放回名单一项）；`internal/chat/chat_usage.go`；`internal/wiring/runtime.go`（`Runtime.StartFork`）；`internal/cli/chat.go`（传参）；`internal/i18n/catalog/{zh,en}/cmd.ini`（`cmd.bg.*`）。
- 动作:
  - `chat.Options.StartFork func(ctx context.Context, task string) (tasks.Info, error)`；`wiring.Runtime.StartFork` 由 runtime 组装（`runtime.go` 已 964 行，fork 连线放 `subagent_fork.go`，`runtime.go` 只留一行赋值）：`Runner.ForkSeedBetweenTurns` → `AgentTool.StartFork`（agents 未启用或无 tasks 时字段为 nil）；`internal/cli/chat.go` 传入。
  - `func (s *chatState) runForkBg(task string)`：空参数 `cmd.bg.usage`；`StartFork` 为 nil → `cmd.bg.disabled`；以 `api.WithSessionID(s.ctx, s.sessionID)` 调用；成功 `cmd.bg.started`；`ForkUnavailableError` → `cmd.bg.unavailable`（`context` 时加 `Hint()` 的译文）；`errors.As(err, *native.ForkLimitError)` → `cmd.bg.full`（`{running}`、`{limit}` 取其字段）。任务文本即分身 prompt，description 由 `runForkBg` 取任务文本前 6 个词（同 `firstWords` 的规则，在 chat 内实现），wiring 不加文案。命令实现只在 `chat_fork_bg.go`。
  - `chat.go` 分发 `:bg`；`steering.go` 回合中输入 `:bg` 放回输入行；`chat_usage.go` 加用法。
- 验证: worktree 根 `go build ./...`；`go test ./internal/chat -run 'TestForkBg|TestChatCommands|TestRunningTurnPutsRewindAndForkBack|TestRunTurnRefusesLocalCommand' -count=1`：空参、disabled、启动行、host_hook 文案、满额文案、回合中放回；`go test ./internal/i18n -run TestCatalogParity -count=1`。
- 完成标准: 用例通过。
- 依赖: T5。

### T7 GR110 分发与 ptye2e

- 文件: `internal/cli/subagent_test.go`（`TestRunWithFork`）；新 `test/ptye2e/chat_fork_test.go`（首行 `//go:build ptye2e`）；`test/ptye2e/fakellm_test.go`（按分身指令识别分身请求）。
- 动作:
  - `TestRunWithFork`：经 `app.Run([]string{"run", ...})`，假模型调 `agent{type: fork}` 再 `task wait`；断言输出含分身报告、`inspect trace --json` 有 `agent_type=fork` 子会话。
  - `TestChatFork` 子用例：①模型派分身 → 启动行、状态行计数 → `:tasks` 列出 → 空闲时通知唤醒注入回合；②`:bg 查一下 X` 启动、`:bg` 空参提示、回合中输入 `:bg` 放回输入行；③`:tasks -k t1` 后无通知、`task output` 为“已终止”、子会话 `stop=cancelled`。前缀用 `fakeLLM.Requests()` 断言分身首个请求以主会话最后一次请求的消息为前缀；`suga inspect trace --json` 断言 `agent_type=fork`、首个 model 事件 `fork_reuse=prefix`、子会话 meta `fork_base`、任务行有步数、主会话有 `task_notice`。
- 验证: worktree 根 `go build ./...`；`go test ./internal/cli -run TestRunWithFork -count=1`；`go test -tags ptye2e ./test/ptye2e -run 'TestChatFork' -count=1 -v`，输出为 `--- PASS`。反向验证：临时不设 `ForkSeed`（按普通子 agent 组装），确认前缀断言失败。
- 完成标准: 用例在 Windows ConPTY 下通过；反向验证记录（Linux 由 T9 跑）。
- 依赖: T6。

### T8 文档

- 文件: `README.md`（中英“Sub-agents”与 chat 命令清单）、`skills/suga-guide/references/`（agent 工具与 chat 命令所在文件）、`CHANGELOG.md`「未发布」。
- 动作: 写明 `type: fork` 与 `:bg`、只读与拒绝提示、回交规则（≤4000 rune 全文）、共用上限、kill 不通知、`fork_reuse` 含义、拒绝启动的情形（host hook、`prompt_state`、超上下文阈值）、与 `:fork N` 的区别；CHANGELOG 另列公开 API：`agentcore.ExpandToolCalls`、`native.ForkSeeder`、`native.AgentFork`、`AgentTool.StartFork`、`AgentRequest.Seed`。
- 验证: worktree 根 `grep -n "type: fork\|:bg" README.md CHANGELOG.md`、`grep -n "ExpandToolCalls\|ForkSeeder" CHANGELOG.md`、`grep -rn ":bg" skills/suga-guide/references/` 均有结果；`git diff -U0 main -- CHANGELOG.md` 改动只在「未发布」内。
- 完成标准: grep 结果符合。
- 依赖: T7。

### T9 全量验证、真实 provider 记录与合并（我执行）

- 文件: 无代码改动；主机脚本放 `tmp/suga-hao6/`。
- 动作:
  - 容器内 worktree 根：`go test -race ./...`、`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...`、`make smoke-go`（含 Linux 上的 `TestChatFork`）。
  - 主机 Windows（gofer exec job，只写 `tmp/`）：worktree 根 `go test ./...` 与 `go test -tags ptye2e ./test/ptye2e -run 'TestChatFork|TestChatAgent' -count=1 -v`。
  - 真实 provider（主机 gofer exec job，脚本 `tmp/suga-hao6/realenv.ps1`）：`go build -o <WP>/tmp/suga-hao6/suga.exe ./cmd/suga`；隔离 `SUGA_HOME=<WP>/tmp/suga-hao6/home`，`config.yaml` 只含从真实配置复制的 provider 段（丢弃 `headers`，只留 `type`、`base_url`、`models`、`api_key`；`api_key` 每值去引号后须匹配 `^\$\{[A-Za-z0-9_]+\}$`，逐值通过才继续），`.env` 只读引用不复制；在 `<WP>/tmp/suga-hao6/ws` 跑 `suga.exe run`，提示要求先读两个文件、再用 `agent` 的 `type: fork` 派一个子任务并 `task wait`；从 `inspect trace --json` 取分身首个 model 事件的 `cache_read / prompt` 与主会话 steady 桶命中率，并列记入 issue 评论（不设门槛）。模型未调用分身时写 `UNVERIFIED_REAL_ENV: 真实模型未触发 fork（复现：同一脚本重跑）`。脚本不打印环境与 `.env`；结束删除 `home`、`ws` 与 `suga.exe`。
  - 提交 tracker：合并前核对之前，在主目录只暂存 `.gofer/tracker/` 下的文件（核对 `git diff --cached --name-only`）并提交 `chore(tracker): suga-hao6 verification notes`。
  - 合并前核对（fail-closed）：主机 job `git -C inhere-tools/suga branch --show-current` 须为 `main`、`status --short` 为空（他人改动未清时按 Ownership Conflict 停止并询问）。记录合并前 `rev-parse HEAD` 作恢复点。
  - 合并：主机 `tmp/xhen-p1/host-merge.ps1 -Repo inhere-tools/suga -Branch feat/background-fork -MsgFile …`，再以主机 job `git -C inhere-tools/suga merge-base --is-ancestor feat/background-fork main` 确认退出码 0；CHANGELOG 已发布小节逐字未变。
- 验证: 以上命令全部通过；Windows 输出含 `--- PASS: TestChatFork`；真实 provider 记录或 `UNVERIFIED_REAL_ENV` 行写在 issue 评论。
- 完成标准: 全绿并合并到本地 main。
- 依赖: T8。

## 回滚与恢复

- 每个任务至少一个提交且 `go build ./...` 通过，恢复点为各任务提交；合并前改动只在 worktree 分支上。
- 合并后回滚：revert 合并提交（恢复点为 T9 记录的合并前 main HEAD）；旧版本把分身会话当普通子 agent 会话，未知 meta 与 attr 被忽略。
- T1 characterization 在基线回放不通过：用例写错，修用例；改动后不通过：按 Corrective 在 T1 内修实现，不得改用例断言。

## 人工 Gate

1. 计划批准（评审后），同时指定 T1–T8 执行者模型（核心 T1、T2、T3、T5）。仪式成本：设计评审 2 轮两轴（报告 75 + 98 = 173 行，设计 227 行）；计划评审第 1 轮两轴（报告 257 行，计划 0.1 为 231 行）、第 2 轮两轴（报告 77 + 100 = 177 行，计划 0.2 为 277 行）、第 3 轮获用户同意（“hao6 Q6 选 A”）。
2. 当前执行请求：你指示开始实施；覆盖建 worktree 分支 `feat/background-fork` 与合并到本地 main，不含 push。
3. 外部动作 Gate：push、tag、发版、安装均需你另行逐项批准。

## 可追溯性

| 设计条目 | 任务 | 验证 |
|---|---|---|
| D1（模型与 `:bg`） | T4、T6、T7 | `TestAgentTool`、`TestForkBg`、`TestChatFork` ①② |
| D2（`type: fork`） | T4 | `TestAgentTool` |
| D3、F9、BF1、BF14（只读与拒绝） | T3 | `TestForkRunner` 3 次写、ask 读、名字文案 + 两项反向验证 |
| D4（回交，仅 fork） | T4 | `TestAgentTool` rune 分界、普通 agent 不变 |
| D5（共用上限、满额拒绝） | T4、T6 | `TestAgentTool` 满额、`TestForkBg` 满额 |
| F1、F10（种子与尾部）、展开不变 | T1 | `TestForkSeedForCall`、characterization 基线回放 + 反向验证 |
| F2、BF12、BF13（请求头） | T2、T7 | `TestForkHeadHook`、`fakeLLM.Requests()` 前缀 + 反向验证 |
| F3（晚绑定、模型） | T1、T3 | `TestForkRunner` `:model` 用例 |
| F4（复用 Kind 与观测）、`fork_reuse`、`fork_base` | T2、T3、T4、T7 | `TestForkContextMode` attr、`TestForkRunner` meta、`TestAgentTool` Kind/label、ptye2e trace |
| F6（`stop=context`、超阈值不启动与提示） | T1、T2、T5、T6 | `TestForkSeedForCall`、`TestForkContextMode`、`TestForkSeedBetweenTurns` 超阈值、`TestForkBg` |
| F7、BF11（模型触发不判 host hook；`:bg` 遇 host hook 拒绝） | T1、T5、T6 | `TestForkSeedForCall` host hook 仍 `prefix`、`TestForkSeedBetweenTurns` 表 + 反向验证、`TestForkBg` |
| kill（Q3 选 A：不通知、“已终止”、`stop=cancelled`） | T4、T7 | `TestAgentTool` kill、`TestChatFork` ③ |
| 分身指令（≤4000 字符、文件:行号） | T1 | `TestForkSeedForCall` 指令文本 |
| GR110 | T7 | `TestRunWithFork` |
| 验证要求（`-race`、Linux ptye2e、Windows、staticcheck） | T9 | 全量命令与主机 job |
| 真实 provider 命中率记录 | T9 | issue 评论 |
| 文档与公开 API 变化 | T8 | grep |
| 合并到本地 main | T9 | `merge-base --is-ancestor` 退出码 0 |

## 完成 Gate 与剩余工作

- 完成条件：T1–T9 完成标准全部满足、合并到本地 main，issue `suga-hao6` 评论附验证结果后关闭；gofer plan 的 todo 全部 done，plan 状态置 done。
- 进入 `COMPLETE` 时写 ≤8 行完成总结（主旨、状态、自主决策、提交数与区间、耗时、剩余项），并按 plan-closure 做一次 retrospective 判定与提示（仅首次 `COMPLETE` 写入时提示一次）。
- 剩余：发版与推送待你授权；deferred：分身白名单加 `recall`（设计 BF10）。

批准记录：2026-11-26 用户原话“批准 suga 后台分身计划 0.3，按建议分配模型开始实施”。执行者：核心 T1/T2/T3/T5 用 `openai-codex/gpt-6.1-sol`，T4/T6/T7/T8 用 `opencode-go/deepseek-v4.1-flash`，T9 由监督会话执行。批准授权本地实施与合并到本地 main；push、发版仍需另行授权。
