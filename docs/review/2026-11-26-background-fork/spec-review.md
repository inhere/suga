<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）设计 评审（Spec/Executability 轴 · 第 1 轮）

## 评审目标与范围

- 目标: 设计 `docs/design/2026-11-26-background-fork-design.md` 是否正确、可实现、无歧义（对照代码）
- 修订版本: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga` · candidate `dd7b5fd8` · subject `docs/design/2026-11-26-background-fork-design.md` · Draft 0.2
- 评审者: Spec/Executability 轴独立评审者（Claude 子 agent，未参与该设计的撰写或修订）
- 日期: 2026-11-26
- 排除项: 用户已选定的 D1–D5 本身；暴露度定档与 Standards/Governance 轴内容；相邻架构

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（含 review-contract、review-candidate-loop、planning-core-rigorous），suga `AGENTS.md`。
- 设计全文（182 行，第 1 轮全量读）。
- 代码（工作树干净，HEAD = dd7b5fd8；与设计所引 `bc0e9ca5` 之间的 Go 改动仅涉及 killed-agent 通知与 hooks glob，不影响结论）：
  `pkg/agentcore/tools/native/agent.go`、`internal/personal/subagent.go`、`internal/wiring/subagent.go`、`internal/wiring/runtime.go:535-560`、
  `internal/personal/prefix.go:340-420`、`internal/personal/segment_handoff_reuse.go`（全文）、`internal/personal/runner.go:780-836`（turnHooks）、
  `pkg/agentcore/runtime.go:330-640`（BeforeModel、工具执行与落库）、`pkg/agentcore/tools/approval.go:136-176`、`internal/personal/denial.go`、
  `internal/personal/observe.go:300-330`、`internal/personal/context.go:495-525`（estimate）、`internal/personal/compact.go:69-86`、
  `pkg/agentcore/tasks/tasks.go`（Start/Notify）、`internal/personal/tasks.go`、`internal/wiring/tasks.go`、
  `internal/chat/chat.go:682`、`internal/chat/steering.go:195-225`、`internal/chat/render_agents.go`、`internal/personal/trace.go:395-500`、
  `internal/personal/analyze.go:240-252`、`test/ptye2e/fakellm_test.go`（请求日志）。
- 方法：按任务书九个检查点逐条把设计陈述落到代码路径，判断能否照做、是否只有一种结果；只记影响核心验收或安全的问题，同类一次列全。

## 验收标准

- 设计“用户可见结果”四条与“验证要求”六条。
- review-contract：`CORE_BLOCKING` 只限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 五类并附 necessity proof；其余为 `CORE_CORRECTIVE`（改变用户可见结果或外部接口标 `user_visible_effect: true`）或 `DEFERRED_ENHANCEMENT`。
- SR1301 安全核对清单（见文末逐项）。

## 发现

### BF1 [HIGH] 拒绝路径会触发每回合 3 次拒绝上限，分身的报告被替换成“回合结束，等待授权”

- disposition: `CORE_BLOCKING` · 类别 `BUG`
- 证据:
  - 设计“总体方案”4：模型看到基准请求里的全部工具，执行层只放 explore 白名单，其余调用返回“分身只读……”；F4 要求复用子 agent 路径。
  - 子 agent 现有拒绝走 `approval.SetDisabled(subAgentDisabledHints(registry))`（`internal/wiring/subagent.go:180,264-272`），返回 `tools.Denied(...)`（`pkg/agentcore/tools/approval.go:139-141`）。
  - 每个 `ErrDenied` 都计入当回合拒绝数（`internal/personal/observe.go:316-319`）。`DenialLimit = 3`（`internal/personal/denial.go:16`）。到 3 次时 `denialLimitHook` 在下一次 BeforeModel 用固定文本 `本回合已有 %d 次工具调用被拒绝……回合结束，等待授权或新的指示。` 短路回合（`denial.go:99-111`）。
  - `RunChild` 把短路后的 `res.Output` 当报告、`Stop=final`（`internal/personal/subagent.go:100-105`）。
  - 普通 explore 子 agent 声明的工具只有白名单，很少碰到这个上限。分身声明的是主会话全部工具，主会话上下文又通常正在改文件、跑命令，所以模型去调写工具或 shell 是常见情况，不是边角情况。
- 同类全部出现点（基准 Tools 里有、分身执行表里没有的名字，两条拒绝路径的行为不一致）:
  1. 在 `native.Catalog()` 中、不在白名单的工具（`file_write`/`file_edit` 类、`shell_exec`、`web_fetch`、`web_search`、`recall`、`session_search` 等）会被 `Denied`，计入上限。提示文本被包进模板 `工具 %s 在本会话未启用：%s。不要重试，请告诉用户开启方法。`，与设计要的“写进报告”相矛盾。
  2. 不在 Catalog 的工具（`agent`、`task`、`update_plan`、`memory`、`skill`、`tool_search` 以及 deferred 的 `tool_call` 代理、MCP 工具）走 not-found 普通错误 `...; available tools: <白名单>`（`approval.go:142`），不计入上限，但措辞不是设计给出的。设计点名“`agent`、`task`、`tool_search` 一律拒绝”，却没有说走哪条路径。
- 影响: 分身调了 3 次写工具或 shell 后，主会话收到的通知是 `agent "<desc>" finished (stop=final): 本回合已有 3 次工具调用被拒绝……等待授权`，没有结论。这违反核心结果“只回交结论”。
- necessity proof:
  - core_acceptance: 背景目标 2 与用户可见结果 2 要求分身结束时主会话收到带结论的通知。
  - necessary_causal_chain: 照文档复用子 agent 的拒绝提示 → 分身模型按主会话上下文调用 `file_edit`/`shell_exec` 三次 → `turnDenials` 达到 3 → `denialLimitHook` 短路 → `RunChild` 把短路文本当作 final 报告 → 主会话通知里只有“等待授权”，分身的调查结论丢失。
  - minimal_owner: `internal/wiring/subagent.go` 的分身装配分支（拒绝路径与提示表）。
  - deletion_alternative: FAIL。为了沿用前缀不能删掉工具声明（F2/H5），拒绝路径无法绕开。
  - scope_effect: 无新增 Module/Interface/Schema/state/lifecycle/authority/external action。
- 建议: 设计写明分身的拒绝规则，二选一：
  - 不计入拒绝上限：例如返回普通错误结果，或给分身单独的 denial 计数豁免。
  - 设分身专用上限，到达时走收尾（`stop=` 写明原因），而不是短路成 final。
  - 无论选哪种，都要对基准 Tools 中每个不在白名单的名字（Catalog 与非 Catalog、`tool_call` 代理、MCP）统一返回设计给出的“分身只读，需要改动请写进报告”文本，提示表按基准 Tools 生成，而不是按 Catalog 生成。
  - 验证中加一条：分身连续 ≥3 次调写工具后作答，断言报告是作答内容、`stop=final`。

### BF2 [HIGH] 模型触发的尾部拿不到“本条 assistant 消息”，组装方式不唯一

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据:
  - 设计“关键流程 1.2”写的是“工具取基准请求与本条 assistant 消息”。可是 `AgentTool.Execute` 只拿到 `api.ToolCall`（`agent.go:118`）。tracker 只保存请求，不保存模型回复（`prefix.go:343-350`）。
  - 运行时逐个执行调用、逐个落库：`persistToolTranscript` 在每个调用执行之后才写该调用的 assistant 消息（`runtime.go:518-533,622-640`），所以执行分身调用时，它自己和之后的调用都还没落库。
  - suga 的 assistant 表示是“每个调用一条 assistant 消息”：文字、`Reasoning` 和 provider 元数据（签名 thinking 块）只放在第一条上，并行批次带 `ToolBatchMetaKey`（`runtime.go:487-495,518-523,636-640`）。
- 影响: 实现者要自己补来源，可能是新增 AfterModel 观察，也可能只按 ToolCall 拼。只拼 ToolCall 会丢掉 thinking 签名或批次元数据，带签名 thinking 的 provider 可能直接拒绝分身首个请求。模型触发路径没有回退（设计称“基准请求此时必然存在”），结果就是分身 `failed`。
- 同类: 同一回复里排在分身之前的调用已经真实执行并有结果，设计却统一写“（未在分身中执行）”，分身会据此误判主会话状态（例如认为前面的 `file_edit` 没做）。排在之后的调用才是真的未执行。
- 建议: 写明尾部来源，例如 personal 层挂 AfterModel hook，保存当步 `GenerateResult`，再用与 `persistToolTranscript` 相同的规则展开成逐调用的 assistant 消息（lead 文字、reasoning、meta、batch）。排在分身之前的调用填真实结果，之后的调用填占位。验证中加入带 reasoning/meta 的回复与并行批次用例。

### BF3 [HIGH] 分身沿用子 Runner 的上下文管线，会在阈值处自行压缩、截断或段断点，而不是 `stop=context` 收尾

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据:
  - F6 要求“不做段断点或压缩，超过阈值即收尾（`stop=context`）”，但没有给出机制。
  - 子 Runner 的 `contextBuilder.estimate` 用 provider 上报的 `lastInputTokens` 加视图增量（`context.go:497-505`）。对分身来说，上报值是改写后的整段主会话上下文，所以从第 2 步起就按真实大小与阈值 `window-reserve`（`compact.go:69-86`）比较。超限时现有路径是 shrink 截断工具结果，或者段断点与交接，最终可能 `ErrContextOverflow`（`context.go:533` 起）。
  - 段断点还会 `clearRequest`，并按子会话视图重建；hook 又会把基准前缀加回去，请求并不会变小。
  - `RunChild` 没有 `context` 这个 stop 值（`subagent.go:22-28`）。
- 影响: 主会话越长越需要分身，分身越接近阈值，结果就越可能是失败、截断后的错误判断或额外的交接调用，而不是带原因的收尾报告。
- 建议: 写明三点：
  1. 分身 Runner 关闭压缩与段断点，具体用哪个配置项或分支。
  2. 阈值按 hook 改写后的请求计算，并说明阈值取值。
  3. 首个请求已超阈值时怎么办（直接拒绝启动并提示，或者只允许一步收尾）。
  另外把 `stop=context` 加进 `ChildResult.Stop` 与 `AgentReport.Stop` 的取值说明。验证中加阈值收尾用例。

### BF4 [MEDIUM] BeforeModel 固定请求头的 hook 规格不完整：位置、子会话自身首条消息、瞬时消息

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: false`
- 证据:
  - hook 顺序（`runner.go:806-835`）是 prefixHook → deadlineHook（追加瞬时截止提示）→ `cfg.Hooks` → limitHook → … → handoffRequestHook。
  - 子 Runner 的消息由 `RunChild` 的 `Prompt` 起头（`subagent.go:95`）。运行时还会在构建后追加 wrap-up 指令。
  - 设计只写了“Messages 为分身请求加分身新增消息”。
- 影响（同类一次列全）:
  1. 子会话首条 user（RunChild 的 Prompt）是否丢掉没有写。模型触发时任务指令已经在尾部的工具结果里，不丢就重复一遍；`:bg` 时又要它作为 user 指令。
  2. deadline 提示与 wrap-up 指令要保留在新增消息里的哪个位置没有写。
  3. hook 放进 `cfg.Hooks` 时，排在 prefixHook 之后。子会话 trace 的 `prefix_reused/rebuilt`（`observe.go:180`）记的是改写前的请求，`inspect --analyze` 的复用率对分身没有意义。
  4. handoffRequestHook 在分身里观察到的是改写后的请求，而子会话的段断点又会用到它（与 BF3 相关）。
  5. 基准请求指针在主会话与分身之间共享，拼接必须复制，不能在原切片上 append（`prefix.go:340-342` 承诺“never mutated”）。
  6. `GenerateRequest.Model` 取基准值还是分身 Runner 的值没有写（与 BF6 相关）。
- 建议: 在设计里给出 hook 的输入、输出定义与挂载位置（例如排在 prefixHook 之前，或者给分身单独的统计口径），并写明子会话首条消息与瞬时消息的处理。

### BF5 [MEDIUM] `:bg` 的基准请求在多种情况下不存在或不可用，回退的“按持久化历史正常组装”有歧义

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据: `lastRequest` 只在本进程发过请求后才有值，`compact.go:413`、`rewind.go:254`、`segment_clear.go:129` 会清掉它。取消或失败的回合不会清（只有这三处调用 `clearRequest`）。`requestBaseBetweenTurns` 自己不检查 host hook，这项检查在 `prepareHandoffReuse` 里（`segment_handoff_reuse.go:100-105,206-227`）。
- 情况（一次列全）:
  1. `chat --resume` 后还没发过请求：没有基准，需要重建。
  2. `:compact`、段断点、`:rewind`、段清理之后：已被清除，需要重建。
  3. 上一回合被 Ctrl-C 取消或失败：`lastRequest` 仍是该回合最后一次请求，但持久化历史不重放它，设计说的尾部“上一轮最终回复”并不存在，分身会看到主会话已丢弃的内容。
  4. `:model` 切换之后：基准是旧模型的请求，缓存不共享（另见 BF6）。
  5. 配置了 host hook：重建结果不可信，交接包对此给的是 `host_hook` 回退，设计 F7 没提。
  6. 回合进行中：设计已经规定放回输入行，没有问题。
  回退的“按持久化历史正常组装”没有说 System/Tools 用哪套：主会话的 `requestBase(RunState{})`，还是分身 Runner 的子 agent 提示词与白名单工具。两种结果的上下文和拒绝行为不同。
- 建议: 定义“可用基准”。例如上一回合正常完成，且 `printRequest(lastRequest)` 与持久化的 `cache_prefix_print` 一致才用它，否则按 `requestBaseBetweenTurns` 重建，并带上 `host_hook`、`prompt_state`、`prefix_changed` 判定。写明最终回退的请求头来源，列出 `fork_reuse=fallback:<reason>` 的全部原因常量（可复用 `handoffFallback*` 的命名）。验证中加入取消回合后 `:bg`、`--resume` 后 `:bg` 两个用例。

### BF6 [MEDIUM] wiring 拿不到父 Runner 的 tracker 和当前模型，`:model` 切换后分身会用错模型

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据:
  - `subAgents` 在 `personal.New` 之前构造并注册到工具表（`internal/wiring/runtime.go:550-560`），只持有启动时的 `mainModel: opts.Model`（`subagent.go:37,148-151`），没有父 Runner 引用。
  - chat `:model` 走 `Runner.SwitchModel`（`runner_sessions.go:43`），不会更新 `subAgents`。
- 影响: F3 “用主会话本次使用的模型与 provider 配置”无法照做。切换模型后派分身，基准 System/Tools/Messages 会发给旧模型，缓存不命中，thinking 等配置也可能不一致。取基准请求同样需要一条从 wiring 通到父 Runner `rec.prefix` 的链路。
- 建议: 写明晚绑定方式（同 `memoryHost` 在 `personal.New` 之后绑定的做法），模型取父 Runner 当前的 provider、model（或基准 `req.Model` 对应的配置），并让 personal 暴露“取分身基准”的方法，wiring 不直接碰 tracker。

### BF7 [MEDIUM] 任务 `Kind=fork` 会漏掉现有所有按 `Kind=="agent"` 判断的分支

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据（同类全部出现点）:
  - `internal/chat/render_agents.go:62`：状态行运行中 agent 计数。
  - `internal/chat/render_agents.go:226`：后台 agent 完成摘要，同时要求 Summary 以 `agent: ` 开头。
  - `internal/personal/analyze.go:244`（`taskKindAgent`，`:851`）：`--analyze` 的 agent 用量与 BackgroundMs。
  - `internal/personal/trace.go:435`（`agentEvent`）。
  - `internal/personal/trace.go:467`（`taskTraceParts`，显示 steps/calls/exit）。
- 影响: 设计的“观测”一节说任务 `Kind=fork`，F4 又说观测复用后台 agent 路径。照前者实施，分身不进状态行、没有完成摘要、`--analyze` 不计分身用量、trace 的任务行不显示步数，与“分身用量计入主会话累计；trace 能从主会话跳到分身会话”的可见结果冲突。
- 建议: 任务保持 `Kind=agent`，用 `agent_type=fork`（task label 与会话 meta）区分；或者在设计里列出上述 5 处一并改。前者改动更小。

### BF8 [LOW] `:bg` 与并发上限的边界没写明

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: true`
- 证据与同类:
  1. `tasks.Manager.Start` 达到 `MaxRunning` 时直接报错，不排队（`tasks.go:281-284`）。设计 D5 只写了“共用”，没写拒绝。模型触发时，这个错误经工具错误结果回给模型；`:bg` 时要在 chat 打印。两处提示都应写明。
  2. `:bg` 要加进 `chat.go:682` 的命令分发和 `steering.go:209` 的“回合中放回输入行”名单，否则回合中输入的 `:bg ...` 会被当成 steering 消息送给模型。设计写了“同 `:rewind`”，计划需要落到这两处。
  3. `:bg` 不在回合里，`tasks.Start` 取会话用的是 `api.SessionIDFrom(ctx)`（`tasks.go:238-240,262`），调用方必须构造带会话 id 的 ctx。
  4. `:bg` 不带参数时的提示没写。
  5. D4 的实现位置应是 `native/agent.go` 的 `startBackground`：按 `req.Type==fork` 分支，绕过 `agentNoticeLines`。4000 按 rune 计还是 byte 计、是否包含 footer，需要写明。通知原样进入 `TaskNoticeText`，没有其他截断（`internal/personal/tasks.go:66-88`）。现有子 agent 提示词“约 4000 字符”不约束分身，因为分身用的是主会话 System。
  6. 没有发现 `:bg` 与现有 `:` 命令冲突（已检索 chat 包全部 `:` 命令）。
- 建议: 在“关键流程 2”与 D4、D5 中各补一句。

### BF9 [MEDIUM] 验证要求证明不了 BF1–BF7 的行为，且有一条断言 trace 做不到

- disposition: `CORE_CORRECTIVE` · `user_visible_effect: false`
- 证据:
  - ptye2e 要求“结束后 `suga inspect trace --json` 断言：……分身首个请求以主会话最后一次请求的消息为前缀”。trace 的 model 事件只有 `prefix_*` 统计和 token 数，没有消息正文（`observe.go:180-187`），能逐字节比对前缀的是假模型的请求日志 `fakeLLM.Requests()`（`test/ptye2e/fakellm_test.go:187-191`）。
  - `fork_reuse` 只记首个 model 事件还是分身全部 model 事件，没有写明，断言因此有歧义。
- 缺口（一次列全）:
  - 拒绝上限（BF1）；
  - reasoning/meta 与并行批次尾部（BF2）；
  - 阈值收尾 `stop=context`（BF3）；
  - 取消回合后与 `--resume` 后的 `:bg`（BF5）；
  - `:model` 切换后派分身（BF6）；
  - 状态行、`--analyze` 与 trace 任务行对分身的显示（BF7）；
  - 达到 `max_running` 时的拒绝（BF8）。
- 已足够的部分: “录请求的假 provider + 逐字节前缀”能证明缓存可命中的前提。反向验证的两条设计合理。真实 provider 实测（`cache_read/prompt`）能证明网关侧命中，但目标“不低于段内复用水平”需要给出具体基线数字或引用 suga-jh7b 的数值，否则“达不到时写明原因”使这条验收不可判定。
- 建议: 把前缀断言改为基于假模型请求日志，trace 只断言 `agent_type=fork`、`fork_reuse` 和 `task_notice`。按上列补用例，并给真实模型实测写明基线。

### BF10 [LOW] 分身读不到主会话上下文里引用的 spill/recall 内容

- disposition: `DEFERRED_ENHANCEMENT`
- 证据: explore 白名单不含 `recall`，理由是“fresh sub-agent session has nothing of its own to recall”（`internal/wiring/subagent.go:68-74`）。分身继承的上下文里可能有主会话 spill 出去的 ref，这个前提对分身不成立。
- 影响: 分身看到“完整内容见 ref”时取不到，只能重新读文件。不影响核心验收。D3 由用户选定，不在本轮修改。
- 建议: 计划阶段在拒绝提示里说明这一点；实测有需要时再评估把 `recall` 加进分身的白名单（它会标记污染，需要一并考虑）。

## 覆盖缺口与限制

- 未运行任何代码或测试；结论来自静态阅读。
- 未逐个核对各 provider 对“assistant 消息缺少 thinking 签名”的实际拒绝行为（BF2 按 `runtime.go` 注释所述的签名 thinking 元数据推断），所以 BF2 定为 CORE_CORRECTIVE，没有定为阻断。
- ACP：每个 prompt 一个 runtime，tracker 随之新建（`internal/wiring/tasks.go:17-19` 的注释）。模型触发在回合内取基准，不受影响。ACP 没有 `:bg`。多会话按 session id 分键（`prefix.go:343-350`），没有发现串会话问题。
- 主会话正在回合中时模型触发：执行分身调用时 tracker 里就是产生该回复的请求，主回合随后的新请求会覆盖 tracker，但分身已经取走快照（需按 BF4-5 复制），没有竞争问题。
- 暴露度与 Full 定档、代码组织与 GR 结构条目属 Standards/Governance 轴，本轮未评。

### SR1301 安全核对

- 鉴权（认证与授权）：已核对。执行层沿用白名单、父策略快照、询问失败关闭、外部来源守卫，可执行面没有扩大。拒绝路径的正确性见 BF1。
- 注入防护：不适用（不新增 SQL 或命令拼接，分身不执行 shell）。
- 秘密不进日志、输出、仓库：已核对。只向主会话已在用的 provider 发请求，分身会话只落新增 entries，没有新增落盘的秘密。
- 入参校验：已核对。`agent` 的 `prompt` 必填沿用现有校验，`:bg` 空参数见 BF8-4。
- 资源归属校验：已核对。任务按会话登记，kill 只作用于本会话的任务（`tasks.go` 按 sid 分组）。

## 结论

BLOCKED。BF1（`CORE_BLOCKING`/BUG）还开着：照文档复用子 agent 的拒绝路径，分身调到第 3 次写工具时会被每回合拒绝上限短路，主会话收到的“报告”是“等待授权”文本，没有结论。其余发现不阻断，但 BF2、BF3、BF5、BF6、BF7、BF8 会改变用户可见结果（`user_visible_effect: true`），建议在本次修订中一并处理。

## 剩余风险与后续

- 修订 BF1 后做一轮闭合确认，只看拒绝规则与对应验证（changed/dependent scope）。
- 计划阶段要把 BF9 列出的用例写成可观察的验收，并给真实模型实测补上基线数字。
- 风险：分身首个请求未命中缓存时按全价计费（回退、缓存过期或 BF6 的模型错配）。BF6 修正后，主要剩回退和过期两种情况，由 `fork_reuse` 统计观察。
