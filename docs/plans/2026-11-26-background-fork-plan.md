<!-- template_id: plan; template_version: 1.3.1 -->
# suga 后台分身（fork） 实施计划

> 状态：Draft 0.1 / 待人工计划批准；issue `suga-hao6`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-26 | Claude | 初稿：按已批准设计 0.4 拆为 T1–T9 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：落实设计 0.4——模型（`agent` 的 `type: fork`）或 `:bg` 派出只读分身，分身以主会话基准请求为前缀起步、在后台运行、只回交结论。
- scope freeze：设计 0.4 的总体方案、D1–D5、F1–F10 与“验证要求”。不含设计非目标与 deferred（`recall` 进白名单）。
- 扩围策略：`DEFER_OR_REQUEST`；review budget：按 SR1409 Full 档两轴评审，轮次按 `planning-core-rigorous`；停止条件：无开放 `CORE_BLOCKING`。
- 暴露度：Full（依据见设计“安全、数据、运维与回滚”第 1 条）。

## 目标与完成定义

完成定义（全部满足）：
1. 模型调用 `agent`（`type: fork`）或 chat 输入 `:bg <任务>` 时，分身作为 `Kind:"agent"` 后台任务启动，主会话继续；`:tasks`、状态行可见，可 kill。
2. 分身首个请求的 System/Tools/Model 与基准请求相同、Messages 以基准 Messages 为前缀（`fakeLLM.Requests()` 逐字节断言）；`fork_reuse` 记在分身首个 model 事件。
3. 分身只执行 explore 白名单；一切拒绝返回 F9 普通错误，不触发每回合拒绝上限；上下文到阈值以 `stop=context` 收尾。
4. 通知按 D4（仅 `type: fork`）；普通后台 agent 通知不变；满额直接拒绝。
5. 设计“验证要求”全部落到测试并通过（单测、GR110、ptye2e Linux 与 Windows ConPTY、反向验证）；真实 provider 的命中率已记录（不设门槛），或按 `UNVERIFIED_REAL_ENV` 披露。

## 范围、排除项与授权

- 范围：suga 仓库 `pkg/agentcore`（导出逐调用展开）、`pkg/agentcore/tools/native/agent.go`、`internal/personal`（新 `subagent_fork.go`、`subagent_fork_seed.go`、`context.go` 分身分支、`runner.go` turnHooks 一项、`subagent.go` stop 常量）、`internal/wiring`（`subagent.go`、新 `subagent_fork.go`、`runtime.go` 绑定一处）、`internal/chat`（新 `chat_fork_bg.go`、`chat.go` 分发一行、`steering.go` 放回名单一项、usage 文案）、`internal/cli` 测试、`test/ptye2e` 新场景、README、`skills/suga-guide/references/`、CHANGELOG。
- 排除：分身写文件、shell、上网、`recall`；续聊分身；ACP 回合外推送；gofer 契约；发版、推送、安装。
- 授权：本计划批准只授权进入实施请求；实施需你另行指示。当前执行请求将覆盖：建 worktree 分支 `feat/background-fork`、在其上本地提交、合并到本地 main；不含 push、tag、发版、安装。
- `host_or_non_offline_action=REQUIRED`：主机上建 worktree `.worktrees/fork`（`tmp/xhen-p1/wt-repo.ps1`）、合并到本地 main（`tmp/xhen-p1/host-merge.ps1`）；执行者 `gofer job -a suga` 在主机运行并调用模型 provider（联网、产生费用）；主机 Windows 测试；T9 真实 provider 调用；`staticcheck@2026.2.1` 联网下载模块。均不推送、不发布。

## 输入与批准证据

- 设计：`docs/design/2026-11-26-background-fork-design.md` Draft 0.4；批准记录提交 `a8a2bc83`。
- 批准原话（2026-11-26）：“批准 suga 后台分身设计 0.4（接受未复核修订），开始写计划”。
- 设计评审：`docs/review/2026-11-26-background-fork/`（两轮两轴 PASS，ledger `f1201c30`）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 后台运行、通知、kill、用量与污染传递 | `native.AgentTool.startBackground`、`tasks.Manager`、`personal.RunChild`、`wiring.subAgents` | 是 | 原位扩展（`type: fork` 分支） | DIRECT_REUSE | none | 无 |
| CAP-02 | 主会话请求前缀与重建 | `prefixTracker.lastRequest`、`requestBaseBetweenTurns`、`prepareHandoffReuse` 的回退判定 | 是 | 既有 owner 扩展（tracker 加当步回复与已执行结果；重建加“跳过指纹比对”参数） | OWNER_EXTENSION | tracker 不存回复与工具结果（设计 BF2） | 与交接包沿用共用重建，不复制 |
| CAP-03 | 逐调用 assistant 展开 | `pkg/agentcore/runtime.go` `persistToolTranscript` 与 `leadOutput` | 否（未导出） | 既有 owner 扩展（导出同一展开函数，runtime 改为调用它） | OWNER_EXTENSION | 分身须与落库同规则展开，复制实现会漂移 | runtime 与分身共用一份 |
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

（新文件都在既有包内，属 owner 扩展；`internal/chat/chat.go` 已 935 行，命令实现放新文件，`chat.go` 只加分发一行。）

## 前置检查与 fail-closed 条件

- workspace baseline（2026-11-26，计划写作时）：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；branch `main`；HEAD `a8a2bc83`；`git status --short` 仅 ` M docs/design/2026-11-26-trusted-memory-backend-design.md`（他人改动，保留不动，不暂存）。expected symbols：`AgentTool.startBackground`、`RunChild`、`subAgents.build`、`prefixTracker.observeRequest/lastRequest`、`requestBaseBetweenTurns`、`persistToolTranscript`、`Runner.turnHooks`、`denialLimitHook`。实施开始时重新记录 HEAD 与 status。
- 进度跟踪：执行开始时 `gofer plan create --title "suga 后台分身实施" --project my-tools-dev --desc "仓库 inhere-tools/suga；计划 docs/plans/2026-11-26-background-fork-plan.md"`，再 `gofer plan add-todo <plan-id> "T1 …" --cwd inhere-tools/suga --after prev …` 建 T1–T9 todo；每个执行者 job 挂到对应 todo。
- worktree：主机 `gofer job run -p my-tools-dev -a exec --runner local --sync -- pwsh -File tmp/xhen-p1/wt-repo.ps1 -Repo inhere-tools/suga -Name fork -Branch feat/background-fork`（基于当时的 main）。容器内不 `git worktree add/prune`；对 worktree 的 git 命令只用单条前缀 `GIT_DIR`/`GIT_WORK_TREE`，不 export，不与测试同一命令。
- 执行者：T1–T8 由 `gofer job -a suga` 在 `.worktrees/fork` 顺序实施，每任务一个 job、≤45 分钟（`--timeout 5400`）；模型由你在计划 Gate 指定。核心任务（建议用推理更强的模型）：**T1**（F10 尾部拼装）、**T2**（forkHeadHook 与分身上下文模式）、**T3**（拒绝转换与晚绑定）、**T5**（F7 基准判定）；其余 T4、T6、T7、T8 可用 `opencode-go/deepseek-v4.1-flash`。我审查（先看 `git diff --stat` 删除行）并复核反向验证；T9 由我执行。
- 任务书约束（每个 job 都写）：只改本任务列出的文件；不删注释、不重排无关代码；不用 web_fetch；可改 go.mod/go.sum（预期不改）；遵守 GR104、GR107–GR110 与 `pkg/agentcore` 不依赖 `internal/*`；只暂存本任务文件，提交前核对 `git diff --cached --name-only`；禁用 `git stash`、`git reset`、`git checkout -- <他人路径>`；首个提交不晚于开始后 20 分钟；`--sync`，不起空等后台任务；不在 export 了 `GIT_DIR`/`GIT_WORK_TREE` 的命令里跑测试。
- 测试环境：执行者在 Windows 主机，无 cgo，任务内 focused 测试不带 `-race`；`-race`、Linux ptye2e、staticcheck 在 T9 由我在容器里跑。测试用临时 `SUGA_HOME`，不读写真实 `~/.suga`；断言用 `github.com/gookit/goutil/x/assert`，多用例 `t.Run`（SR2002）。
- 反向验证写法：临时改动必须可编译，确认输出含对应断言失败且不含 `[build failed]`，恢复后写进提交说明。
- fail-closed：focused 测试或反向验证不符合预期即停止，按实施发现四类记录；遇到他人改动（Ownership Conflict）停止。
- 依赖：不新增第三方依赖。

## 波次与依赖

| 波次 | 任务 | 说明 |
|---|---|---|
| W1 | T1 → T2 | 种子（模型触发）；请求头与分身上下文模式 |
| W2 | T4 → T3 | native 工具与通知（先做，定义 `AgentFork`、`Seed`、`StartFork`）；wiring 装配 |
| W3 | T5 → T6 | 回合外种子；`:bg` |
| W4 | T7、T8 | 分发与 ptye2e；文档 |
| W5 | T9 | 全量、主机 Windows、真实 provider、合并（我执行） |

## 任务

### T1 当步记录、逐调用展开与模型触发的种子（F1、F3、F10）【核心】

- 文件: `pkg/agentcore/runtime.go`（抽出展开）、新 `pkg/agentcore/expand.go`；`internal/personal/prefix.go`（tracker 字段）；新 `internal/personal/subagent_fork_seed.go`；`internal/personal/runner.go`（turnHooks 加一项）；新测试 `pkg/agentcore/expand_test.go`、`internal/personal/subagent_fork_seed_test.go`。
- 动作（签名为合同）:
  - `func ExpandToolCalls(res *GenerateResult) []Message`：按 `persistToolTranscript` 的现有规则把一步回复展开为逐调用 assistant 消息（lead 文字、`Reasoning`、元数据只在第一条，并行批次带 `ToolBatchMetaKey`）；`persistToolTranscript` 改为取用它，行为不变。
  - tracker：`observeStep(sessionID string, res *agentcore.GenerateResult)`（AfterModel，清空已执行结果）、`observeToolResult(sessionID, callID string, res agentcore.ToolResult)`（AfterTool），`lastStep(sessionID) (res *agentcore.GenerateResult, done map[string]agentcore.ToolResult)`；`clearRequest` 一并清掉。由新 `forkStepHook`（AfterModel/AfterTool）挂进 `turnHooks`。
  - `type ForkSeed struct { Request *agentcore.GenerateRequest; Model, ProviderName, BaseTurn, Source, Reuse, ReuseNote string }`（`Source` 为 `call|bg`，`Reuse` 为 `prefix|rebuilt`）；`type ForkUnavailableError struct{ Reason string }`（`Error()` 为 `fork unavailable: <reason>`，原因常量 `host_hook`、`prompt_state`、`no_request`、`context`，沿用 `handoffFallback*` 命名）。
  - `func (r *Runner) ForkSeedForCall(ctx context.Context, sessionID, callID, directive string) (ForkSeed, error)`：不取会话 gate；基准 = `lastRequest`；尾部 = `ExpandToolCalls(当步回复)`，每条之后接结果：排在 `callID` 前的填 `done` 中的真实结果，`callID` 填 `directive`，之后的填 i18n 文案 `personal.fork.not_run`（“主会话在派出分身后才执行此调用，分身中未执行”）；新切片，不 append 到共享切片；Model/ProviderName 取 Runner 当前值；估算（`tokensOfMessages` 加工具表）超过 `thresholdOf(window, reserve)` 返回 `context`。
- 验证: worktree 根 `go test ./pkg/agentcore -run 'TestExpandToolCalls|TestRuntime' -count=1`（现有 runtime 用例不改即过，作展开行为不变的证据）；`go test ./internal/personal -run 'TestForkSeedForCall' -count=1`，`t.Run` 覆盖：带 reasoning 与签名元数据的回复（元数据只在第一条）、三调用并行批次中分身居中（前填真实结果、后填占位）、基准切片调用前后长度与内容不变、超阈值返回 `context`。反向验证：临时让前面的调用也填占位，确认“真实结果”断言失败。
- 完成标准: 用例通过；`persistToolTranscript` 相关现有测试未改。
- 依赖: 无。

### T2 forkHeadHook 与分身上下文模式（F2、F6、BF12、BF13）【核心】

- 文件: 新 `internal/personal/subagent_fork.go`；`internal/personal/context.go`（分身分支）；`internal/personal/subagent.go`（`StopContext = "context"`）；`internal/personal/runner.go`（`Config` 一字段）；新 `internal/personal/subagent_fork_test.go`。
- 动作:
  - `Config.ForkSeed *ForkSeed`（非 nil 即分身模式）。分身模式下 `contextBuilder` 不渲染段上下文消息、hookContext、ephemeral，不折叠、不段断点、不 shrink。
  - `type forkHeadHook struct{ agentcore.NopHook; seed *ForkSeed; prompt string; maxInput int }`，挂在分身 Runner hook 链首位（prefixHook 之前）。`BeforeModel`：System、Tools、Model 取种子；Messages = 新切片（种子 Messages + 本次请求中去掉内容等于 `prompt` 的首条 user 消息后的其余消息，顺序不变）；`ToolChoice` 为空时保持为空，非空（收尾重试）原样保留。`AfterModel`：上次调用上报的输入 token ≥ `maxInput`（= 主会话阈值）时 `ctrl.RequestWrapUp(StopContext, <i18n 收尾指令，首行写明因上下文上限收尾>)`。
  - `RunChild` 识别 `res.WrapUp == StopContext`，`Stop=context`。
- 验证: worktree 根 `go test ./internal/personal -run 'TestForkHeadHook|TestForkContextMode' -count=1`（录请求的假 provider）：首个请求 System/Tools/Model 等于种子、Messages 以种子 Messages 为前缀且不含 RunChild 的 Prompt、不含子会话段上下文；第二步只在末尾追加；收尾重试请求保留 `ToolChoiceNone`；上报输入达阈值时 `Stop=context` 且有报告；前缀统计 attr 记的是改写后的请求。反向验证：临时把 hook 挂到 prefixHook 之后，确认“前缀统计”断言失败。
- 完成标准: 用例通过。
- 依赖: T1。

### T3 wiring：晚绑定、分身装配与拒绝转换（F3、F9、BF14）【核心】

- 文件: `internal/wiring/subagent.go`；新 `internal/wiring/subagent_fork.go`；`internal/wiring/runtime.go`（`personal.New` 之后绑定一处）；新 `internal/wiring/subagent_fork_test.go`。
- 动作:
  - `type forkHost struct{ mu sync.Mutex; runner *personal.Runner }`，`bind(r *personal.Runner)`，同 `memoryHost` 在 `personal.New` 之后绑定；`subAgents` 增字段 `fork *forkHost`。
  - `subAgents.RunAgent` 在 `req.Type == native.AgentFork` 时：种子由调用方传入（`AgentRequest.Seed any`，见 T4），`build` 以种子的 Model 调 `loadRuntimeSettings`，注册 explore 白名单，`personal.Config.ForkSeed` 设为种子，`forkHeadHook` 由 personal 在分身模式下自动挂首位。
  - `type forkDenyTools struct{ inner agentcore.ToolExecutor; allow map[string]bool }`：包在 Approval 外层；名字不在白名单 → 返回 `IsError` 工具结果，内容为 i18n `wiring.fork.denied`（“分身只读：%s 在分身中不可用，不要重试；需要改动或命令时写进报告由主会话执行。分身读不到主会话的 recall ref，需要时重读文件”）；白名单内调用若返回 `tools.ErrDenied`（ask 规则在无终端时失败关闭）同样转为 `IsError` 结果，不向上返回 Go error。不调用 `SetDisabled`。
  - 种子 Tools 中有、白名单没有的名字（含 `agent`、`task`、`tool_search`、`tool_call`、MCP、memory）全部走上述分支。
- 验证: worktree 根 `go test ./internal/wiring -run 'TestForkRunner|TestSubAgent' -count=1`：连续 3 次 `file_write` 后作答——每次结果为 F9 文案、临时目录目标文件未创建、报告为作答内容、`Stop=final`；对读路径配置 ask 规则后连续 3 次 `file_read` 同样 `Stop=final`；`agent`、`tool_call`、`mcp__x__y` 名字的文案一致；`:model` 切换后（绑定的 Runner 换模型）分身请求的 Model 为新模型；现有 `TestSubAgent*` 不改即过。反向验证：①临时把拒绝改回 `ErrDenied`，确认“3 次写调用”用例失败；②临时去掉白名单（注册全部工具），确认“F9 文案、文件未创建”断言失败。
- 完成标准: 用例通过；两项反向验证记录。
- 依赖: T4。

### T4 native：`type: fork`、通知与满额提示（D2、D4、D5）

- 文件: `pkg/agentcore/tools/native/agent.go`；`pkg/agentcore/tools/native/agent_test.go`（或新 `agent_fork_test.go`）。
- 动作:
  - 常量 `AgentFork = "fork"`；schema `type` enum 加 `fork`，描述补一句“fork 带着本对话全部上下文，在后台运行，只读”；`fork` 时忽略 `background`，总走 `startBackground`；未挂 tasks 时报错 `agent: fork needs background tasks`。
  - `SubAgents` 接口增 `ForkSeed(ctx context.Context, req AgentRequest) (any, error)`（wiring 经 forkHost 调 `Runner.ForkSeedForCall`）；`AgentRequest` 增 `Seed any`；种子错误作为工具错误结果返回（`agent: fork unavailable: <reason>`）。
  - `startBackground` 的 `type: fork` 分支：`utf8.RuneCountInString(strings.TrimSpace(report)) <= 4000`（不含 footer）时通知 = 全文 + footer，否则前 12 行 + `[report cut; task output reads it all]` + footer；task label `agent_type=fork`；`Kind:"agent"`、Summary `agent: <description>` 不变。普通 agent 分支不改。
  - 抽出 `func (t *AgentTool) StartFork(ctx context.Context, req AgentRequest) (tasks.Info, error)`（`startBackground` 的 fork 分支调用它，T6 的 `:bg` 复用）。
  - `Stop` 注释加 `context`。`tasks.Start` 满额错误时工具结果为“已有 N 个后台任务在运行（上限 N），先 task wait 或 task kill 再派分身”。
- 验证: worktree 根 `go test ./pkg/agentcore/tools/native -run 'TestAgentTool' -count=1`：fork 报告 3999/4000/4001 rune（含多字节字符）的通知分界；普通后台 agent 12 行通知不变；满额文案；`Kind`/Summary/label；`go test ./pkg/agentcore -run TestLayering -count=1`。
- 完成标准: 用例通过（wiring 侧 `ForkSeed` 在 T3 实现，本任务用假 `SubAgents`）。
- 依赖: T2。

### T5 回合外种子（F7、BF11）【核心】

- 文件: `internal/personal/subagent_fork_seed.go`；`internal/personal/segment_handoff_reuse.go`（重建加参数）；`internal/personal/subagent_fork_seed_test.go`。
- 动作:
  - `func (r *Runner) ForkSeedBetweenTurns(ctx context.Context, sessionID, directive string) (ForkSeed, error)`：上一回合正常完成、模型未变且 `lastRequest` 属于该回合 → `prefix`，尾部 = 上一轮最终回复 + user `directive`；否则（`--resume` 后、`:compact`、段断点、`:rewind`、段清理、取消或失败回合、`:model` 切换）经 `requestBaseBetweenTurns(..., skipPrint=true)` 重建 → `rebuilt`，`ReuseNote` 记原因；配置了 host hook → `host_hook`、`prompt_state` → `prompt_state`、无任何回合 → `no_request`，返回 `ForkUnavailableError`；超阈值同 T1。
  - `requestBaseBetweenTurns` 增参数 `skipPrint bool`（交接包调用方传 false，行为不变）。
- 验证: worktree 根 `go test ./internal/personal -run 'TestForkSeedBetweenTurns|TestHandoffReuse' -count=1`：正常回合后 `prefix`；`--resume`（新 Runner 同库）、取消回合、`:compact`、`:rewind`、`:model` 切换后均为 `rebuilt`；host hook 时 `host_hook`；现有 `TestHandoffReuse*` 不改即过。反向验证：临时把 `skipPrint` 固定为 false，确认 `:compact` 用例失败。
- 完成标准: 用例通过。
- 依赖: T3。

### T6 chat：`:bg`（D1、BF8）

- 文件: 新 `internal/chat/chat_fork_bg.go`、`internal/chat/chat_fork_bg_test.go`；`internal/chat/chat.go`（分发一行）；`internal/chat/steering.go`（放回名单一项）；`internal/chat/chat_usage.go` 与 i18n 目录（用法与提示文案）。
- 动作:
  - `func (s *chatState) runForkBg(task string)`：空参数打印“用法：:bg <任务>”；以 `api.WithSessionID(s.ctx, s.sessionID)` 调 `ForkSeedBetweenTurns`，再经 agent 工具同一启动路径（`native.AgentTool` 的导出方法 `StartFork(ctx, req AgentRequest) (tasks.Info, error)`，T4 的 `startBackground` 抽出复用）；打印启动行；`ForkUnavailableError` 打印原因；满额打印“后台任务已满（N/N），用 :tasks 查看或 :tasks -k <ID> 终止后重试”。
  - `chat.go` 分发 `:bg`；`steering.go` 回合中输入 `:bg` 放回输入行。
- 验证: worktree 根 `go test ./internal/chat -run 'TestForkBg|TestChatCommands|TestSteering' -count=1`：空参提示、启动行、host_hook 拒绝文案、满额文案、回合中放回。
- 完成标准: 用例通过；`chat.go` 净增不超过 3 行。
- 依赖: T5、T4（`StartFork`）。

### T7 GR110 分发与 ptye2e

- 文件: `internal/cli/subagent_test.go`（加 `TestRunWithFork`）；新 `test/ptye2e/chat_fork_test.go`（首行 `//go:build ptye2e`）；`test/ptye2e/fakellm_test.go`（分身模式识别：按种子消息末尾的任务指令识别分身请求）。
- 动作:
  - `TestRunWithFork`：经 `app.Run([]string{"run", ...})`，假模型调用 `agent{type: fork}` 再 `task wait`；断言输出含分身报告，`inspect trace --json` 有 `agent_type=fork` 子会话。
  - `TestChatFork` 子用例：①模型派分身 → 屏幕启动行、状态行计数 → `:tasks` 列出 → 空闲时通知唤醒注入回合；②`:bg 查一下 X` 启动、`:bg` 空参提示、回合中输入 `:bg` 放回输入行；③`:tasks -k t1` 后通知含“分身被终止”。前缀用 `fakeLLM.Requests()` 断言分身首个请求以主会话最后一次请求的消息为前缀；结束后 `suga inspect trace --json` 断言分身子会话 `agent_type=fork`、首个 model 事件 `fork_reuse=prefix`、任务行有步数、主会话有 `task_notice`。
- 验证: worktree 根 `go test ./internal/cli -run TestRunWithFork -count=1`；`go test -tags ptye2e ./test/ptye2e -run 'TestChatFork' -count=1 -v`，输出为 `--- PASS`（不是 `no tests to run`）。反向验证：临时让分身按普通子 agent 方式组装请求（不设 `ForkSeed`），确认前缀断言失败。
- 完成标准: 用例在 Windows ConPTY 下通过；反向验证记录（Linux 由 T9 跑）。
- 依赖: T6。

### T8 文档

- 文件: `README.md`（中英“Sub-agents”与 chat 命令清单）、`skills/suga-guide/references/`（agent 工具与 chat 命令所在文件）、`CHANGELOG.md`「未发布」。
- 动作: 写明 `type: fork` 与 `:bg`、只读与拒绝提示、回交规则（≤4000 rune 全文）、共用上限、`fork_reuse` 含义、拒绝启动的三种情形（host hook、`prompt_state`、超上下文阈值）、与 `:fork N` 的区别。
- 验证: worktree 根 `grep -n "type: fork\|:bg" README.md CHANGELOG.md`、`grep -rn ":bg" skills/suga-guide/references/` 均有结果；`git diff -U0 main -- CHANGELOG.md` 改动只在「未发布」内。
- 完成标准: grep 结果符合。
- 依赖: T7。

### T9 全量验证、真实 provider 记录与合并（我执行）

- 文件: 无代码改动；主机脚本放 `tmp/suga-hao6/`。
- 动作:
  - 容器内 worktree 根：`go test -race ./...`、`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...`、`make smoke-go`（含 Linux 上的 `TestChatFork`）。
  - 主机 Windows（gofer exec job，只写 `tmp/`）：worktree 根 `go test ./...` 与 `go test -tags ptye2e ./test/ptye2e -run 'TestChatFork|TestChatAgent' -count=1 -v`。
  - 真实 provider（主机 gofer exec job，脚本 `tmp/suga-hao6/realenv.ps1`）：`go build -o <WP>/tmp/suga-hao6/suga.exe ./cmd/suga`；隔离 `SUGA_HOME=<WP>/tmp/suga-hao6/home`，`config.yaml` 只含从真实配置复制的 provider 段（丢弃 `headers`，只留 `type`、`base_url`、`models`、`api_key`；`api_key` 每值去引号后须匹配 `^\$\{[A-Za-z0-9_]+\}$`，逐值通过才继续），`.env` 只读引用不复制；在 `<WP>/tmp/suga-hao6/ws` 跑 `suga.exe run`，提示要求先读两个文件、再用 `agent` 的 `type: fork` 派一个子任务并 `task wait`；从 `inspect trace --json` 取分身首个 model 事件的 `cache_read / prompt` 与主会话 steady 桶命中率，并列记入 issue 评论（不设门槛）。模型未调用分身时写 `UNVERIFIED_REAL_ENV: 真实模型未触发 fork（复现：同一脚本重跑）`。脚本不打印环境与 `.env`；结束删除 `home`、`ws` 与 `suga.exe`。
  - 提交 tracker：合并前核对之前，在主目录只暂存 `.gofer/tracker/` 下的文件（核对 `git diff --cached --name-only`）并提交 `chore(tracker): suga-hao6 verification notes`。
  - 合并前核对（fail-closed）：主机 job `git -C inhere-tools/suga branch --show-current` 须为 `main`、`status --short` 除计划写作时已存在的他人路径外为空；否则按 Ownership Conflict 停止。记录合并前 `rev-parse HEAD` 作恢复点。
  - 合并：主机 `tmp/xhen-p1/host-merge.ps1 -Repo inhere-tools/suga -Branch feat/background-fork -MsgFile …`，再以主机 job `git -C inhere-tools/suga merge-base --is-ancestor feat/background-fork main` 确认退出码 0；CHANGELOG 已发布小节逐字未变。
- 验证: 以上命令全部通过；Windows 输出含 `--- PASS: TestChatFork`；真实 provider 记录或 `UNVERIFIED_REAL_ENV` 行写在 issue 评论。
- 完成标准: 全绿并合并到本地 main。
- 依赖: T8。

## 回滚与恢复

- 每个任务至少一个提交，恢复点为各任务提交；合并前改动只在 worktree 分支上。
- 合并后回滚：revert 合并提交（恢复点为 T9 记录的合并前 main HEAD）；旧版本把分身会话当普通子 agent 会话，未知 meta 与 attr 被忽略。
- T1 抽出展开后若现有 runtime 用例失败：按 Corrective 在 T1 内修正，不得改现有断言。

## 人工 Gate

1. 计划批准（评审后），同时指定 T1–T8 执行者模型（核心 T1、T2、T3、T5）。
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
| F1、F10（种子与尾部） | T1 | `TestForkSeedForCall` + 反向验证 |
| F2、BF12、BF13（请求头） | T2、T7 | `TestForkHeadHook`、`fakeLLM.Requests()` 前缀 + 反向验证 |
| F3（晚绑定、模型） | T1、T3 | `TestForkRunner` `:model` 用例 |
| F4（复用 Kind 与观测） | T4、T7 | `TestAgentTool` Kind/label、ptye2e 状态行与 trace |
| F6（`stop=context`、超阈值不启动） | T1、T2 | `TestForkSeedForCall` 超阈值、`TestForkContextMode` |
| F7、BF11（基准判定） | T5、T6 | `TestForkSeedBetweenTurns` + 反向验证、`TestForkBg` host_hook |
| kill 与通知 | T7 | `TestChatFork` ③ |
| GR110 | T7 | `TestRunWithFork` |
| 验证要求（`-race`、Linux ptye2e、Windows、staticcheck） | T9 | 全量命令与主机 job |
| 真实 provider 命中率记录 | T9 | issue 评论 |
| 文档 | T8 | grep |
| 合并到本地 main | T9 | `merge-base --is-ancestor` 退出码 0 |

## 完成 Gate 与剩余工作

- 完成条件：T1–T9 完成标准全部满足、合并到本地 main，issue `suga-hao6` 评论附验证结果后关闭；gofer plan 的 todo 全部 done，plan 状态置 done。
- 进入 `COMPLETE` 时写 ≤8 行完成总结（主旨、状态、自主决策、提交数与区间、耗时、剩余项），并按 plan-closure 做一次 retrospective 判定与提示（仅首次 `COMPLETE` 写入时提示一次）。
- 剩余：发版与推送待你授权；deferred：分身白名单加 `recall`（设计 BF10）。
