<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 3 轮 Spec/Executability 评审（blocking closure）

## 评审目标与范围

- 目标: `inhere-tools/suga` 仓库（Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`）`docs/design/2026-11-12-multi-command-hooks-design.md`，candidate commit `46272dc5e8bceb9729badfc7202b9f327c7518a1`（上轮 `096c9e93fa8364aa448e957679e3c6526279cd20` / Draft 0.3），issue `suga-5xmf`
- 修订版本: Draft 0.4
- 评审者: 独立 Spec/Executability 轴评审 agent（Claude，REPORT_ONLY，未改仓库、未提交）
- 日期: 2026-11-12
- 排除项: Q1–Q7 的选择本身（用户已定：Q1 选 M，Q2–Q7 按推荐）；Standards/Governance 轴；gofer 服务端状态机；IDEV-STD 续跑 hook 的实现

## 输入与方法

- 规范：`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/payload.md`（SR1406/SR1408/SR1409、planning-core-rigorous 轮次规则）；review-contract 与 review 模板取自 `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`；`AGENTS.md`。
- 上轮本轴报告 `docs/review/2026-11-12-multi-command-hooks/spec-review.md`（HF13–HF20，HF13 阻断）。
- 设计 0.4 全文（307 行）与 `git diff 096c9e93..46272dc5 -- <subject>`（+102/−55，集中在 §2–§4、架构、关键流程、安全/验证、D2/D5/D9）。
- 代码（工作树 HEAD = 候选 `46272dc5`，`internal/`、`pkg/` 未改）：`internal/hooks/hooks.go:51-110,150-157,208-430`；`internal/chat/hooks_record.go` 全文、`stop_background.go` 全文、`hooks_gate.go:41-80`、`chat.go:226-227,499,520-545,726-858`、`chat_turn.go:230-260,385-440`；`pkg/agentcore/tasks/tasks.go:295-330,400-418,490-525,650-720,770-845`；`internal/cli/chat.go:15-40,121`、`internal/cli/run.go:170-215`；`internal/wiring/sessionenv.go:25-45`；gofer `internal/hookrelay/run.go:240-285,590-670`（只为核对 Interrupt 的用途）。
- 方法：①逐项核对 HF13–HF20 的关闭证据；②HF13：把 R0–R3 对照所有能使 Stop 流水线结束的路径（后台被新 Stop 杀、任务时长上限、chat 退出、前台各打断来源、回合内接续、正常结束未消费）与所有读合并结果的消费点（前台 `Done`、后台 `run`、`stopWait.Interrupt`/`recordingWait.Interrupt` 补取、trace）做交叉表；③0.4 新增内容（`Response.Runs []EntryRun`、`hooks.New` 启用条件、单条目实现保持不变、流水线结束未消费时的 Interrupt、按事件分流）逐条映射到现有代码，检查是否唯一可实现；④每类发现做同类穷尽。

## 验收标准

- review-contract：四级 severity + disposition；`CORE_BLOCKING` 限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 并附 necessity proof；同类穷尽；无阻断才 PASS。
- 设计核心目标与 AC1–AC4（`:54-55`），特别是 AC3/D2“只配 `hooks.command` 时行为、超时、等待、Interrupt、trace 与现状一致”（`:70`、`:277`）。
- 规则实现唯一、可由计划直接拆解；验证要求能证明所述行为。

## 上轮发现关闭情况

| 上轮 | 状态 | 证据 |
|---|---|---|
| HF13（阻断） | 关闭 | `:174-181` R0 为首要规则：流水线 ctx 已取消时 `Decision`/`Reason` 为空、`Err` 取 ctx 错误。交叉核对见下“HF13 关闭核对”；残留的列举与机制描述偏差见 HF21（LOW，不影响结果） |
| HF14 | 关闭（残留 HF24） | `:157-161` 写定 `Runs` 挂在 `Response` 上，`Call`/`PreTool`/Stop 同一载体；`recordingHooks` 按 `Runs` 逐条写、为空按现状；`Call` 路径 status 仍为 `ok`（与 `hooks_record.go:35-39` 一致）；PreToolUse 从 `response.Runs` 取 `entry=0`/`program`。失败路径是否填 `Runs` 未写，见 HF24 |
| HF15 | 关闭 | `:140` 无 `hooks.commands` 时 `hooks.New` 返回现有 `commandClient`/`stopWait`，Interrupt 无条件先发再取消（`hooks.go:407-415`）保持；`:196` 流水线已全部结束未消费时发给最后执行的条目；`:264`、`:267` 补验证。边界见 HF25 |
| HF16 | 关闭 | `:171` 兼容条目 Stop 总附加 `stop_args`，与阶段无关（同 `hooks.go:336-339`）；`:267` 补“即时型兼容条目在阶段一附加 `stop_args`” |
| HF17 | 关闭 | `:78` 只按键判定（`stop_wait_sec > 0` 或 `stop_background`）；`:267` 补边界用例 |
| HF18 | 关闭 | `:147` 流水线 `Call` 按事件名分流，PostToolUse 只发兼容条目；`:141`、`:224` `hooks.New` 去掉提前返回、任一非空即启用；`:265` 补验证。`GateOf`（`hooks.go:99-101`、`chat.go:227`）对流水线返回非 nil gate，`PreToolName` 委托兼容条目、无兼容条目为 false，`toolGate.Rewrite` 直接放行（`hooks_gate.go:46-49`），可实现 |
| HF19 | 关闭 | `:200` 明确接受取舍并要求用户文档提示、trace 逐条记 `notice` |
| HF20 | 关闭 | `:257` 改为“不打开等待 turn，本回合最后回复不上报，web 保持运行中直到下一次 UserPromptSubmit” |

## HF13 关闭核对（非发现项）

结束路径 × 消费点（按 0.4 规则推演）：

| 结束路径 | 流水线 ctx | 合并结果 | 消费点与结果 | 与现状 |
|---|---|---|---|---|
| 后台被新 Stop 杀（`stop_background.go:169-172` → `tasks.go:658-667` → `t.end(Killed)` → `t.cancel()`，`tasks.go:836`） | 已取消 | R0：`Err=context.Canceled` | `stop_background.go:195-197` → `-1, nil`，不 `Notify`、不置 `blocked` | 一致；上轮的过期通知不再产生 |
| 后台到达 `tasks.max_duration`（`tasks.go:417` `t.end(TimedOut)` → cancel） | 已取消 | R0 | 同上 `-1, nil` | 一致（单条目时 `call` 返回 `timeoutError`，同走该分支）；设计未列出，见 HF21 |
| chat 退出时的后台任务（`run.go:193` `taskManager.Close` → `KillSession` → `Kill`） | 已取消 | R0 | 同上 | 一致；设计写成“随父 ctx 取消”，机制不准（函数任务 ctx 来自 `context.Background()`，`tasks.go:302`），见 HF21 |
| 后台无任务槽回落前台（`chat_turn.go:400-403`） | — | 按前台规则 | — | 一致 |
| 前台各打断来源（`chat.go:739-744,748-768,792-812,823-833`、`chat_turn.go:429-431`、`chat.go:532-533`） | 打断第 3 步取消 | 运行中被打断：R0；已结束未消费：保持已产生的 R1/R2/R3 | `stopDone` 置 nil 不再读 `Done`；reason 只经 `Interrupt` 返回值进 `lostReply`；`recordingWait.Interrupt`（`hooks_record.go:112-125`）只在 `Err==nil && reply==""` 时补取——运行中被打断为 R0 不补取，已结束未消费时流水线已返回 reason、`reply!=""` 不补取 | 不重复投递；单条目路径不变 |
| 正常结束、前台读到 `Done`（`chat.go:782-791`） | 未取消 | R1/R2/R3 | R1 开启注入回合并置 `stopHookActive`（`chat.go:559-563`） | AC2 成立 |
| 正常结束、后台 | 未取消 | R1/R2/R3 | R1 → `Notify` + `blocked`；R2 失败 → 任务错误 | 一致 |

trace：`Runs` 逐条记录各条自己的错误（`:160`、`:190`），R0 下已完成条目仍按其自身结果记，未启动条目不记（`:259`），与 `recordHookRun` 的 status 规则（`hooks_record.go:88-97`）相容。

结论：R0–R3 按顺序取首条适用规则，结果在上述全部路径下唯一，HF13 的错误结果（被杀后投递过期续跑通知、置 `stop_hook_active`）不再出现。HF13 关闭。

## 0.4 新增内容核对（非发现项）

- `Response.Runs []EntryRun`：`EntryRun` 含“该条回答”（`Response`）时为切片间接递归，Go 合法；`recordHookRun` 现按 `Result.Response` 取 attrs（`hooks_record.go:68-100`），逐条调用即可，无需改签名。Stop 单条目由 `StartStop` 协程在得到 `(response, err)` 后填一条即可（`hooks.go:296-312`）。
- 启用条件：`hooks.New` 去掉 `hooks.go:216-218` 的提前返回后，`localenv.go` 与 `cli/chat.go:21` 只看是否 nil，无需改动，成立。兼容键解析顺序见 HF23。
- 单条目实现保持不变：与现有测试（`hooks_test.go:87,150,342-372`）和 D2 一致；`stopWait.Interrupt` 的 `w.call` 为未包装 `Call`，Interrupt 不进 trace 不变。
- 流水线结束未消费时的 Interrupt：在流水线里“最后执行的条目”只可能是等待条目（阶段二）或最后一条新条目；兼容条目为即时型时排在阶段一最前，不会是最后执行者——即时兼容条目不收 Interrupt 是 D9“只给一个条目”的推论，属用户已选范围内，不作发现（见“剩余风险”）。
- 按事件分流：chat 只发 SessionStart/UserPromptSubmit/PostToolUse/Stop/SessionEnd（PreToolUse 走 `PreTool`、Interrupt 走 `Wait`），`:147` 覆盖经 `Call` 的全部事件名，结果唯一。

## 发现

### HF21 [LOW] R0 同类穷尽漏列 `tasks.max_duration`，“chat 退出取消父 ctx”对后台任务机制不准；“被杀时与现状一致”有一处竞态差异未说明

- disposition: `CORE_CORRECTIVE`
- 证据:
  - 设计 `:178` R0 条件列“后台被新 Stop 杀掉、chat 退出取消父 ctx、前台打断后的取消”；`:186-187` 同类穷尽只列新 Stop、chat 退出、前台打断。
  - 函数任务的 ctx 来自 `context.WithCancel(context.Background())`（`tasks.go:302`），不继承 chat ctx；取消来源为 `Kill`（`tasks.go:658-667`）、时长上限 `t.end(TimedOut)`（`tasks.go:417`）、`Close`/`KillSession`（`tasks.go:777-799`，chat 退出时由 `run.go:193` 调用），三者都走 `t.cancel()`（`tasks.go:836`）。`stop_background` 下兼容条目自身上限为 7 天（`hooks.go:251-254`），实际由 `tasks.max_duration`（默认 2 小时，`tasks.go:56`）截断。
  - 竞态：现状单条目在被杀瞬间刚好正常退出时 `cmd.Output()` 成功、`Err==nil`（`hooks.go:358-366`），`stop_background.go:195-206` 仍会 `Notify`；按 R0（只看 ctx）则丢弃。`:186` 写“与现状单条目被杀一致”。
- 影响: 规则 R0 本身覆盖这些来源（都使 ctx 取消、`errors.Is(err, context.Canceled)` 成立），结果唯一，不影响实现；但同类穷尽列表不完整、机制描述会误导计划把“chat 退出”写成父 ctx 传递的测试。竞态差异属更保守的方向，可接受但应写明。
- 同类穷尽: 函数任务取消入口全集：`Kill`、`TimedOut` 定时器、`Close`→`KillSession`→`Kill`；其余 `t.cancel()` 调用点只有 `tasks.go:457`（`runFunc` 在 `run` 返回后释放 ctx），不是运行中的取消来源。
- 建议: `:178`、`:186-187` 改为“后台任务 ctx 被取消（新 Stop `Kill`、`tasks.max_duration` 到期、chat 退出时 `taskManager.Close`）”；补一句“被杀瞬间恰好完成的 block 也丢弃（现状单条目在此竞态下会投递），属有意收紧”。

### HF22 [MEDIUM] `hooks.New` 的 `opts`（`WithExtraEnv` 会话变量）是否作用于新条目未写，实现不唯一

- disposition: `CORE_CORRECTIVE`
- 证据: `Option` 类型为 `func(*commandClient)`（`hooks.go:150`），`WithExtraEnv` 只设 `commandClient.extraEnv`（`:155-157`），`New` 只对兼容条目的 client 应用（`:261-263`）；`cli/chat.go:21` 传入 `WithExtraEnv(runtime.SessionEnv)`，提供 `SUGA_SESSION_NAME`/`SUGA_TURN_ID` 等（`hooks.go:152-154`、`wiring/sessionenv.go:25-45`，含 `NoSessionEnv` 时整体不导出的开关）。设计架构 `:222-226` 只写“保留兼容条目的现有解析”“有新条目时返回流水线”，未提 opts 与新条目的环境；§1/`:63` 只描述 stdin 事件 JSON。
- 影响: 计划可能只把 opts 应用到兼容条目（沿用现有 `New` 尾部），新条目缺 `SUGA_SESSION_NAME`/`SUGA_TURN_ID`（`SUGA_SESSION_ID` 仍由 `hookEnv` 的 fallback 补上）；或因 `Option` 绑死 `*commandClient` 而改 `Option` 类型。两种实现对新条目可见环境不同。续跑 hook 读 stdin JSON，AC1–AC4 不受影响，故非阻断。
- 同类穷尽: `hooks.Option` 现只有 `WithExtraEnv` 一个（`hooks.go:150-157`）；`New` 的调用点两个（`cli/chat.go:21` 传 opts，`wiring/localenv.go:193` 不传）。
- 建议: 架构写定“opts 应用到每个条目（兼容条目与各新条目各自的 `commandClient`），新条目与兼容条目收到相同的会话环境变量；`NoSessionEnv` 同样作用于全部条目”，验证补一条新条目收到 `SUGA_SESSION_NAME` 的单元断言。

### HF23 [LOW] 去掉提前返回后，`hooks.command` 为空时是否仍解析兼容配套键未写，可能让现有配置启动失败

- disposition: `CORE_CORRECTIVE`
- 证据: 现状 `hooks.command` 为空即 `return nil, nil`（`hooks.go:216-218`），之后才解析 `stop_args`、`post_tool_use.tools`、`pre_tool_use.tools` 并可能报错（`:222-240`）。设计 `:224`“保留兼容条目的现有解析；去掉……提前返回，改为调用 `config.ReadHookEntries` 后按 §2 启用条件决定”；`:142` 承诺“兼容条目不增加任何检查，现有配置不会因升级而启动失败”。
- 影响: 按 `:224` 字面去掉提前返回、顺序执行现有解析，则“未设 `hooks.command` 但留有格式错误的 `hooks.stop_args`/glob”的配置（现状忽略、正常启动）升级后启动报错，违反 AC3 与 `:142`。`:142` 可推出正确读法，但 `:224` 的写法易照字面实现。
- 同类穷尽: 提前返回之后的报错点：`hooks.go:222-224`（`stop_args`）、`:226-228`（`post_tool_use.tools`）、`:230-232`（`pre_tool_use.tools`）、`:234-240`（glob 校验）、`:219-221`（程序名空，仅在 argv 非空时）。
- 建议: `:224` 改为“`hooks.command` 为空时不构造兼容条目、不解析其配套键（同现状）；随后读 `ReadHookEntries`，两者都空返回 nil”。验证 `:264` 补“未设 `hooks.command`、`hooks.stop_args` 为非法类型、只配 `hooks.commands` 时照常启动”。

### HF24 [LOW] 单条目与流水线在失败路径是否填 `Runs` 未写；PreToolUse 失败与 `Call` 失败的 trace 可能缺 `entry`/`program`

- disposition: `CORE_CORRECTIVE`（上轮 HF14 同类，按“上轮未穷尽”记，不新增阻断）
- 证据: `commandClient.call` 的失败路径都返回零值 `Response{}`（`hooks.go:350,364,366,380`）；`hooks_gate.go:55-58` 在 `PreTool` 返回错误时同样用该 `Response` 记 trace；`recordingHooks.Call` 拿到的也是零值（`hooks_record.go:37-38`）。设计 `:158`“单条目实现也填一条（编号 0）”、`:159`“`Runs` 为空时按现状写一条”、`:161` PreToolUse 从 `response.Runs` 取 `entry=0`/`program`，均未说失败时是否填。
- 影响: 若只在成功时填，失败的 PreToolUse/普通事件记录不带 `entry`/`program`，与 `:259`“只配兼容条目时同样带这两个字段”及 `:270` 的 chat 层断言不一致；若失败也填，则 `EntryRun` 的“该条错误”是 `Call` 路径唯一保留错误的地方。两种实现都可能。
- 同类穷尽: 写 `kind=hook` 的三处（`hooks_record.go:38`、`:54`、`hooks_gate.go:58`）的失败输入都来自上述零值返回点；Stop 单条目由 `StartStop` 协程（`hooks.go:296-312`）同时拿到 `response, err`，可在此统一填。
- 建议: `:158` 补“无论成功、失败、超时或取消都填该条 `EntryRun`（失败时回答为空、错误非空）”；`:270` 补“PreToolUse 失败与 SessionStart 失败的 trace 记录仍带 `entry=0`/`program`，status 规则不变”。

### HF25 [LOW] “两条目之间”与“流水线已全部结束”的边界未定义：最后一条结束后、下一步判定之前的打断归哪类

- disposition: `CORE_CORRECTIVE`
- 证据: `:195` 冻结与“启动下一条”同锁判定；`:196` 有条目运行→发给它，全部结束→发给最后执行的条目，两条目之间→不发。最后一条即时条目刚结束、流水线尚未判定“R1 结束 / 无等待条目结束 / 启动阶段二”的瞬间，三种状态都可自洽地套用。`:267` 的两条验证（“阶段一结束瞬间打断……无条目收 Interrupt”与“全部结束未消费……发给最后执行的条目”）只在存在等待条目时互不冲突。
- 影响: 无等待条目（只配新条目，或兼容条目为即时型）或 R1 时，该竞态窗口内最后一条新条目是否收 Interrupt 取决于实现；新条目（续跑 hook）对 Interrupt 无输出，功能影响很小，但测试期望不唯一。
- 同类穷尽: 打断时无条目在运行的状态：①阶段一内两条之间（不发，明确）；②最后一条结束、尚未判定（本项）；③已判定结束、结果在 `Done` 中（发给最后执行者，明确）；④阶段一结束、已判定进入阶段二但等待条目未启动（不发，明确，`:267`）。
- 建议: 写定“条目结束与下一步判定在同一临界区内完成；判定结果为‘无后续条目’即视为全部结束（发给最后执行的条目），判定为启动下一条而被冻结拦下即视为两条目之间（不发）”。

## 覆盖缺口与限制

- 静态阅读，未运行代码或测试；`R0` 各路径的结果由代码推演得出，未实测时序。
- gofer 侧只读了 `hookrelay/run.go` 的 Interrupt 与 Stop 打开 turn 片段，未核实即时型 gofer（无 `--wait`）在阶段一后不收 Interrupt 对 web 侧的影响。
- 未读 IDEV-STD 续跑 hook 实现，HF22 对其不依赖环境变量的判断来自设计背景（`:65`）。
- 按 review-contract 第 3 轮读 diff 与依赖章节；§1、§5、§6、D1/D3/D4/D6–D13 未变，沿用上轮结论。
- Standards/Governance 轴不在本轴范围。

## 结论

PASS：HF13（上轮唯一 `CORE_BLOCKING`）已关闭——R0 作为首要规则使后台被杀、任务时长上限、chat 退出、前台打断各路径的合并结果唯一，不再投递过期续跑通知或置 `stop_hook_active`，交回路径不重复投递。HF14–HF20 关闭。本轮无 `CORE_BLOCKING`；新增 HF21–HF25 均为 `CORE_CORRECTIVE`（1 项 MEDIUM、4 项 LOW），建议计划批准前修订。

## 剩余风险与后续

- 按 planning-core-rigorous，blocking closure 末轮全 PASS 且含 `CORE_CORRECTIVE`：若作者修订 HF21–HF25，可做唯一一轮纠正确认轮（candidate 须变化）；不做则这些修订按“未复核修订”披露。
- 修订时按类自查：①所有使 ctx 取消的入口（HF21）；②`hooks.New` 对每个条目应用的构造输入——opts、兼容键解析前提（HF22、HF23）；③所有 `Runs` 生成点在成功/失败/超时/取消下的内容（HF24）；④打断时“无条目运行”的状态全集及其 Interrupt 去向（HF25）。
- 即时型兼容条目（如未设 `stop_wait_sec` 的 gofer）在流水线中收 Stop 但不收 Interrupt，属 D9 推论；若 gofer 在无 `--wait` 时也会打开 turn，web 侧可能多一个靠下一次 UserPromptSubmit 结算的 turn。建议计划阶段核对 gofer `hookrelay` 无 `--wait` 路径，必要时在用户文档写明。
