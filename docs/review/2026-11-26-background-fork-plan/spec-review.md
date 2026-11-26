<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）实施计划 Spec/Executability 评审（第 1 轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `00c890f724f8983b531eaa120c8708e11c7c2238`；subject `docs/plans/2026-11-26-background-fork-plan.md`；document revision Draft 0.1
- 修订版本: Draft 0.1（实施已批准设计 `docs/design/2026-11-26-background-fork-design.md` Draft 0.4）
- 评审者: 独立评审子 agent（Spec/Executability 轴），未参与计划编写
- 日期: 2026-11-26
- 排除项: Standards/Governance 轴（另一评审者负责）；设计本身的决策；REPORT_ONLY，不修改仓库

## 输入与方法

- 全文读取：review/plan 规范 payload（`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`、`tmp/idev-std/d/L1pSw4v7EitW46DpcPDbLQ/payload.md`）、`AGENTS.md`、计划全文、设计全文。
- 对照 candidate 的代码核对：`pkg/agentcore/runtime.go`（executeToolCalls / executeSingleTool / persistToolTranscript / AfterModel 时点）、`pkg/agentcore/tool_validation.go`、`pkg/agentcore/call_expander.go`、`pkg/agentcore/runtime_tools_test.go`、`pkg/agentcore/tools/native/agent.go`、`internal/wiring/subagent.go`、`internal/wiring/runtime.go`、`internal/personal/{runner.go,prefix.go,subagent.go,segment_handoff_reuse.go,compact.go}`、`internal/chat/{chat.go,steering.go}`、`internal/cli/chat.go`、`Makefile`，以及各 `-run` 正则对应的现有测试名。
- 方法：逐任务核对文件/符号/签名是否存在与可落地；依赖顺序与共用文件；`-run` 是否命中；反向验证能否编译且会失败；设计“验证要求”到任务的覆盖。

## 验收标准

- plan-contract：任务精确文件、动作、验证、完成标准；跨任务调用的签名；“验收声称行为等价时须含基线回放”；可追溯性。
- review-contract：五类 CORE_BLOCKING + necessity proof；同类穷尽。
- 用户要求：治理要轻，只报影响核心验收或安全的问题。

## 发现

### PF1 [HIGH] `ExpandToolCalls(res)` 无法复现落库记录，"persistToolTranscript 改为取用它、行为不变"实现不唯一

- disposition: CORE_BLOCKING [BUG]
- 证据: 计划 T1 动作第 1 条（plan:97）。代码：AfterModel 拿到的 `GenerateResult` 是 `PrepareToolCalls` 强转参数/置 `ArgumentsError` 之前的克隆（`runtime.go:426-430` 先 AfterModel，`runtime.go:498` 才 prepare）；落库的 `ToolCall` 是 `finalCall`，经过 `tool_call` 代理展开为真实调用（`runtime.go:560-566`）和 BeforeTool/AfterTool hook 改写（`runtime.go:577-579,603-615`）；`persistToolTranscript` 是逐调用调用的（`runtime.go:630`），而计划签名是整步 `res` 输入。
- 影响: 照计划，"`persistToolTranscript` 取用 `ExpandToolCalls(res)` 的输出"有两种读法：①只取 Content/Reasoning/Metadata，`ToolCall` 仍用 finalCall（行为不变）；②直接落库展开结果（存的是未展开的 `tool_call` 代理、未强转的参数、丢失 hook 改写）。②会改变主会话历史和此后每次主循环请求的内容。T1 的验证 `-run 'TestExpandToolCalls|TestRuntime'` 里没有一个现有用例断言"落库的 ToolCall 是展开/改写后的调用"，②能通过 T1 验收。
- necessity proof:
  - core_acceptance: 计划 T1 完成标准"persistToolTranscript 行为不变"；完成定义 5（现有测试全绿、无回归）。
  - necessary_causal_chain: 执行者按签名 `ExpandToolCalls(res)` 在 `executeToolCalls` 里展开后直接落库 → 走 `tool_call` 代理的延迟工具调用以代理名和原始参数存进历史，hook 改写的参数丢失 → 下一回合起主会话请求里的历史与实际执行不一致，模型看到的是没执行过的调用形态（用户可见的会话结果变化），T1 focused 测试仍通过。
  - minimal_owner: 计划 T1 动作第 1 条。
  - deletion_alternative: FAIL（分身尾部需要按同一规则展开，删除抽出就是 CAP-03 所说的复制漂移）。
  - scope_effect: 不新增 Module/Interface/Schema/state。
- 建议: 合同写成"`ExpandToolCalls` 只负责 lead 文字、`Reasoning`、元数据只在第一条、批次 meta 每条都有的分配规则；返回消息里的 `ToolCall` 是模型回复中的原调用；`persistToolTranscript` 取第 i 条的 Content/Reasoning/Metadata，`ToolCall` 保持 finalCall"，或改为逐调用的 helper（如 `ExpandToolCall(lead, call, i, batch) Message`）。同时写明分身尾部用的是模型原调用（与主会话落库形态的差异可接受，或写明要在 AfterTool 里拿到 finalCall 覆盖）。

### PF2 [HIGH] T1 声称行为等价，但没有基线 characterization 与回放

- disposition: CORE_CORRECTIVE（`user_visible_effect: false`）
- 证据: plan:101 只以"现有 runtime 用例不改即过"作为等价证据。现有 `pkg/agentcore` 测试里只有 `TestRuntime_Run_PrefetchesMultiCallReplies`（`runtime_tools_test.go:527-539`）断言批次 meta；没有用例断言"lead 文字、Reasoning、provider 元数据只在第一条 assistant 工具调用消息上"，也没有用例断言落库 ToolCall 是代理展开、参数强转后的调用（`grep ExpandCall --include=*_test.go` 只在 wiring 与 tools 的可见性测试中出现，且不检查落库消息）。plan-contract 要求等价声明须有只经基线已有入口调用、在基线 commit 上回放通过的 characterization 用例。
- 影响: PF1 的回归和"元数据重复到每条"这类回归都检测不到；签名 thinking 元数据重复到每条会让 Claude 类 provider 的后续请求出错。
- 建议: T1 加一个只经 `New(Config{...}).Run(...)` 入口的 characterization 用例（如 `TestRuntime_Run_ToolTranscriptLeadOnFirstCall`）：三调用并行回复带 Content/Reasoning/Metadata，第二个调用走 `CallExpander` 代理、第三个参数需强转；断言落库 assistant 消息的 lead 只在第一条、批次 meta 每条都有、ToolCall 是展开/强转后的调用。先在基线上回放（`git archive 00c890f7 | tar -x -C tmp/<dir>` → 拷入用例 → `go test ./pkg/agentcore -run <名> -count=1` 通过），再在改动后通过；验证命令与回放结果写进 T1 的提交说明。

### PF3 [HIGH] T6 `:bg` 拿不到 `AgentTool.StartFork`，列出的文件里没有可行的接线路径

- disposition: CORE_BLOCKING [BUG]
- 证据: plan:155 要求 `runForkBg` 调 `native.AgentTool.StartFork`。`AgentTool` 只是 `internal/wiring/runtime.go:551` 里的局部变量，注册进 registry 后没有导出；`chat.Run(ctx, runner *personal.Runner, opts Options, ...)`（`chat.go:220`）与 `chat.Options`（`chat.go:135-195`）都没有对应字段；`internal/cli/chat.go:105-151` 组装 Options。T6 的文件只有 `chat_fork_bg.go`、`chat.go`（分发一行，完成标准"净增不超过 3 行"）、`steering.go`、`chat_usage.go` 与 i18n；任务书又要求"只改本任务列出的文件"，并且 fail-closed。
- 影响: 执行者要么停下，要么自行加 Options 字段、wiring Runtime 导出与 cli 传参（越出文件清单，且加字段加注释超过"净增 3 行"），要么在 chat 里按名字从 registry 找工具再类型断言。`agents.enabled=false` 时 `AgentTool` 不存在，`:bg` 该提示什么也没有写。
- necessity proof:
  - core_acceptance: 完成定义 1（chat 输入 `:bg <任务>` 时分身作为后台任务启动）；D1。
  - necessary_causal_chain: 照计划执行 T6 → 在允许的文件内无法拿到 `StartFork` → 执行者按 fail-closed 停止，或越界自行接线 → `:bg` 不可用，或接线方式各异（例如另建一个不挂 tasks 的 AgentTool，导致满额/kill 不共用上限）→ 完成定义 1 的 `:bg` 验收失败。
  - minimal_owner: 计划 T6 的文件与动作。
  - deletion_alternative: FAIL（D1 已批准 `:bg`）。
  - scope_effect: 新增一个包内部的 Options 字段与 wiring 访问器（内部接口，不改外部 Interface）。
- 建议: T6 文件加 `internal/chat/chat.go`（`Options` 加一个字段，如 `StartFork func(ctx context.Context, req native.AgentRequest) (tasks.Info, error)`，nil 时 `:bg` 打印"分身不可用：agents 未启用或无后台任务"）、`internal/wiring/runtime.go`（Runtime 暴露该函数）、`internal/cli/chat.go`（传参）；把"净增不超过 3 行"改为"命令实现不放进 chat.go"。

### PF4 [HIGH] kill 后的"分身被终止"通知没有任务负责，且与现有代码有意不发通知相反

- disposition: CORE_BLOCKING [BUG]
- 证据: 设计"kill 与失败"写 task output 写最后一段回答、通知写"分身被终止"；计划 T7 ③（plan:166）断言"`:tasks -k t1` 后通知含'分身被终止'"。现有 `startBackground` 的 killed 分支（`agent.go:200-206`）只写"已终止"和 footer，**有意不调 `Notify`**（注释说明：kill 后的通知会唤醒没人要的回合、打断正在输入的行）。T4 的动作只改通知的完成分支，没有提 killed 分支；T7 的文件里没有 `agent.go`。
- 影响: T7 ③ 照写必失败；执行者要么在 T7 越界改 `agent.go` 给 fork 加 kill 通知（重新引入被删除的"kill 后唤醒回合"行为），要么改断言。两种做法的用户可见结果不同（kill 后 chat 空闲时是否被唤醒、是否注入回合）。
- necessity proof:
  - core_acceptance: 完成定义 1（可 kill）；可追溯性行"kill 与通知 → T7 ③"。
  - necessary_causal_chain: 照计划实施 T4（不动 killed 分支）→ T7 ③ 断言通知含"分身被终止"→ 实际没有任何通知 → ptye2e 失败、fail-closed 停止；或执行者自行加 Notify → kill 分身后 chat 被唤醒并注入一个回合（用户可见的行为倒退）。
  - minimal_owner: 计划 T4 动作 + T7 ③ 断言（kill 时的通知语义要先定下来；与设计有冲突，SR1413 需在计划里写明）。
  - deletion_alternative: PASS（把 T7 ③ 改为断言 task output 为"已终止"、无通知、`stop=cancelled`，沿用现有行为，即可关闭；设计中的"通知写分身被终止"需要在计划里写明是按现有代码修正的偏离并在 Gate 披露）。
  - scope_effect: 无新增。
- 建议: 二选一写进计划：①沿用现有行为（推荐，删除优先）：T7 ③ 断言无通知、task output 含"已终止"、子会话 `stop=cancelled`，并在计划中注明对设计"通知写分身被终止"的偏离及依据 `agent.go:200-206`；②给 fork 加 kill 通知：T4 负责 killed 分支，写明文案、是否唤醒，并由用户确认。

### PF5 [HIGH] `fork_reuse` 与 `fork_base` 没有任务负责写入

- disposition: CORE_BLOCKING [MISSING_FUNCTION]
- 证据: 完成定义 2"`fork_reuse` 记在分身首个 model 事件"（plan:25）；设计"观测"要求会话 meta `fork_base`（turn、尾部来源）。T1 只在 `ForkSeed` 上定义 `BaseTurn/Source/Reuse/ReuseNote` 字段；T2 的 `forkHeadHook` 动作只改 System/Tools/Model/Messages/ToolChoice 与收尾；T3 只装配；没有任何任务写 model 事件 attr `fork_reuse`（含 `ReuseNote` 原因）或会话 meta `fork_base`。只有 T7 在 ptye2e 里断言 `fork_reuse=prefix`。
- 影响: 照 T1–T6 实施后，T7 的 trace 断言必失败；`fork_base` 没有实现也没有测试。
- necessity proof:
  - core_acceptance: 完成定义 2。
  - necessary_causal_chain: T2/T3 执行者按列出的动作实现 → 分身首个 model 事件没有 `fork_reuse` attr → T7 `inspect trace --json` 断言失败 → 完成定义 2 不满足。
  - minimal_owner: T2（`forkHeadHook` 在首个 `BeforeModel`/`AfterModel` 写 attr，或 personal 在分身模式下写入）与 T3/RunChild（建子会话时写 meta `fork_base`）。
  - deletion_alternative: FAIL（完成定义要求）。
  - scope_effect: 只新增 meta 键与 attr（设计已批准）。
- 建议: T2 动作写明谁、在哪一步写 `fork_reuse`（及原因 attr 名），T2 单测断言；T3（或 T2 的 RunChild 改动）写明 `fork_base` 的键名与值格式（`<turn>|call/bg`），加一条单测断言。

### PF6 [MEDIUM] 设计规定的若干用户可见文案/指令没有落到任务（同类全列）

- disposition: CORE_CORRECTIVE（`user_visible_effect: true`）
- 证据与各出现点:
  1. 种子超阈值不启动时"提示改用 `agent explore`"（设计 F6 与"用户可见差异"第 2 条）：T1 只返回 `fork unavailable: context`，T4 透传为 `agent: fork unavailable: <reason>`，T6 打印原因，均未写该提示。
  2. 分身指令要求"报告不超过 4000 字符"（设计总体方案 5）和"列出依据的文件:行号"（设计安全第 4 条）：T1 的 `directive` 由谁拼、i18n 键、包含哪些要求都没写；分身沿用主会话 System，`subagent.go:278-284` 的子 agent 提示词对分身不生效。
  3. 模型触发的返回文本 `[background fork task t2 started: <description>]`（设计流程 1 第 2 步）：T4 没有改现有 `[background agent task %s started` 文案（`agent.go:221`），T7 只说"屏幕启动行"。
- 影响: 用户看到的拒绝提示、报告长度与依据格式、启动行与设计不一致，且实现不唯一。
- 建议: 在 T1/T4/T6 写明三处文案的 i18n 键与原文，并在对应单测中断言。

### PF7 [MEDIUM] T4 给 `SubAgents` 接口加方法，T3 之前 `internal/wiring` 编译不过

- disposition: CORE_CORRECTIVE（`user_visible_effect: false`）
- 证据: `internal/wiring/subagent.go:66` 有 `var _ native.SubAgents = (*subAgents)(nil)`；T4 只改 `native` 包，T4 的验证只跑 native 与 `TestLayering`，看不到 wiring 的编译失败；计划"回滚与恢复"把各任务提交作为恢复点。
- 影响: T4 的提交是一个 `go build ./...` 不通过的恢复点；T4 执行者也可能为了让构建通过越界改 wiring。
- 建议: T4 文件加 `internal/wiring/subagent.go`，只加一个返回 `fork unavailable: no_request` 的 `ForkSeed` 桩（T3 再替换），T4 验证加 `go build ./...`；或改用可选接口（`native` 中 `type ForkSeeder interface{ ForkSeed(...) }`，类型断言不到时报 `agent: fork needs ...`）。

### PF8 [MEDIUM] T5 的基准判定缺数据来源，host hook 情形有歧义

- disposition: CORE_CORRECTIVE（`user_visible_effect: true`）
- 证据: T5（plan:145）的 `prefix` 条件"上一回合正常完成、模型未变且 `lastRequest` 属于该回合"：`prefixTracker` 按会话只存最后一次请求（`prefix.go:343-372`），没有 turn id，也不知道回合是否正常结束（设计已确认取消/失败的回合不清 `lastRequest`）。T5 文件不含 `prefix.go`/`runner.go`（`prefix.go` 归 T1），也没说用 T1 的 `lastStep`（如 FinishReason==final）判定。另：T5 写"配置了 host hook → `host_hook`"，但没说是只在重建分支拒绝，还是在 `prefix` 情形也拒绝；设计 F7 读起来是前者，设计"用户可见差异"第 1 条读起来是后者。T5/T6 的 host_hook 用例没写前置状态（正常回合后还是重建场景）。
- 影响: "取消回合 → rebuilt"的实现方式不唯一（可能需要改 T1 的文件）；配置了 host hook 的用户在正常回合后输入 `:bg`，有的实现会启动、有的会拒绝。
- 建议: T5 写明判定数据（例如 T1 的 `observeStep` 同时记 turn id，`lastStep` 的结果 FinishReason 为 final 且 turn id 等于最后完成的回合时才算 `prefix`）并把需要改的 `prefix.go` 归属写清（T1 预留字段或 T5 共享）；host hook 写成一张"情形 → 结果"表，用例写明前置状态。

### PF9 [LOW] 验证命令与反向验证的小缺口（同类全列）

- disposition: CORE_CORRECTIVE（`user_visible_effect: false`）
- 证据与各出现点:
  1. T6 `-run '...|TestSteering'`：`internal/chat` 中没有 `TestSteering*`（steering 测试名为 `TestRunTurn*`、`TestSteerReturns` 等）；现有"回合中放回"用例是 `TestRunningTurnPutsRewindAndForkBack`（`chat_rewind_test.go:153`），没被纳入。
  2. T3 反向验证②"去掉白名单（注册全部工具）"：若测试的父策略对 `file_write` 是 ask，失败关闭得到的 `ErrDenied` 会被 `forkDenyTools` 转成同一段 F9 文案，文件也不会创建，反向验证不会失败。用例须写明父策略放行 `file_write`，使白名单成为唯一屏障。
  3. T5 动作写"超阈值同 T1"，但 `TestForkSeedBetweenTurns` 没有超阈值子用例（设计"阈值：种子超阈值时不启动"对 `:bg` 路径无验证）。
  4. 设计"回交：kill 后 `stop=cancelled`"没有单测（只在 T7 ③，见 PF4）。
- 已核对无问题: ptye2e 命令均带 `-tags ptye2e`，新文件首行 `//go:build ptye2e`，`make smoke-go` 跑整个 `./test/ptye2e/`，新场景会自动纳入；`TestRuntime`、`TestSubAgent`、`TestHandoffReuse`（含 `...BetweenTurnsRefusesChangedPrefix`）、`TestAgentTool`、`TestLayering`、`TestChatCommandsText` 均能命中现有用例；新用例名与现有 `TestFork*`（`:fork N`）不冲突；T1/T2/T3①/T5/T7 的反向验证可编译且会使对应断言失败。
- 建议: 修正上述 4 处命令/用例。

## 覆盖缺口与限制

- 只读核对，未运行任何测试或基线回放；判断基于 candidate `00c890f7` 的源码。
- 未核对 i18n 目录结构、`chat_usage.go` 内容与 T8 文档文件定位（不影响核心验收）。
- 安全核对（SR1301）：鉴权/授权——分身白名单 + `forkDenyTools` + 父策略快照已核对，`tool_call` 代理在分身 registry 中不展开而被按名拒绝，未发现越权路径（PF9-2 只影响反向验证有效性）；注入防护——不适用（无 SQL/命令拼接新增）；秘密——T9 真实 provider 脚本对 `api_key` 做 `${VAR}` 形态校验、不复制 `.env`、不打印环境，已核对；入参校验——`:bg` 空参与 `type` 枚举已覆盖；资源归属——不适用（单用户本地会话）。

## 结论

BLOCKED。开放 CORE_BLOCKING：PF1（展开函数签名与落库不等价、实现不唯一）、PF3（`:bg` 无接线路径）、PF4（kill 通知无负责任务且与现有行为相反）、PF5（`fork_reuse`/`fork_base` 无实现任务）。

## 剩余风险与后续

- 修订后请在下一轮只复核 PF1、PF3、PF4、PF5 的改动及其依赖章节（T1、T2、T3、T4、T6、T7）。
- PF2 属 plan-contract 必含项（基线回放），建议随 PF1 一并补齐；PF6、PF8 标 `user_visible_effect: true`。
- PF4 涉及设计文字与现有代码冲突，若选方案②（给 fork 加 kill 通知）需用户确认。
