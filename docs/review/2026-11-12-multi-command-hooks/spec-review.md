<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 2 轮 Spec/Executability 评审

## 评审目标与范围

- 目标: `inhere-tools/suga` 仓库（Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`）`docs/design/2026-11-12-multi-command-hooks-design.md`，candidate commit `096c9e93`（上轮 `b11cd717` / 0.1），issue `suga-5xmf`
- 修订版本: Draft 0.3
- 评审者: 独立 Spec/Executability 轴评审 agent（Claude，REPORT_ONLY，未改仓库、未提交）
- 日期: 2026-11-12
- 排除项: Q1–Q7 的选择本身（用户已定：Q1 选 M，Q2–Q7 按推荐）；Standards/Governance 轴；gofer 服务端 `sessionrelay` 状态机；IDEV-STD 续跑 hook 的实现

## 输入与方法

- 规范：`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/payload.md`；review-contract 与 review 模板取自 `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`。
- 上轮本轴报告 `docs/review/2026-11-12-multi-command-hooks/spec-review.md`（HF1–HF12）。
- 设计 0.3 全文（260 行，`git show 096c9e93:<subject>`）与 `git diff b11cd717..096c9e93 -- <subject>`；按 review-contract 第 2 轮读 diff 与依赖章节，因 §3/§4/架构/验证要求均被重写，实际按全文读。
- 代码（工作树 HEAD `8b870a79`，`internal/` 下与候选相比无改动）：`internal/hooks/hooks.go` 全文、`hooks_test.go:342-372`；`internal/chat/{hooks_record.go,stop_background.go,hooks_gate.go}` 全文，`chat.go:485-545,552-566,726-858`，`chat_turn.go:240-252,290-302,394-433`；`internal/config/{config_candidate.go,mcp.go}`、`settings_registry.go:36-47,192,268-279,475-492,678-735`；`internal/wiring/localenv.go:184-199`；`internal/cli/chat.go:21,121`；`pkg/agentcore/tasks/tasks.go:493,509-523,658-670,684-710,825-838`。
- `go list -deps ./internal/config`：只依赖 `internal/i18n`，不经 `internal/hooks`，`internal/hooks → internal/config` 无环。
- 方法：逐项核对上轮发现的关闭证据；把 0.3 的每条规则（事件集合、两阶段、合并字段、打断、后台、Pre/Post 委托、Reader、trace、gofer 影响、验证）映射到现有消费点，检查结果是否唯一、是否与 D2/AC3 一致；每类发现做同类穷尽。

## 验收标准

- review-contract（四级 severity + disposition；`CORE_BLOCKING` 限五类并附 necessity proof；同类穷尽）。
- 设计核心目标与 AC1–AC4（`:37-38`），特别是 AC3/D2“只配 `hooks.command` 时行为、超时、等待、Interrupt、trace 与现状一致”（`:53`、`:230`）。
- 规则实现唯一、可由计划直接拆解；验证要求能证明所述行为。

## 上轮发现关闭情况

| 上轮 | 状态 | 证据 |
|---|---|---|
| HF1（阻断） | 关闭 | `:123` 新校验只作用于新条目、兼容条目不增加检查；`:218` 补“`stop_background`+`stop_args` 无 `stop_wait_sec`、只配 `stop_args`”启动用例；与 `hooks.go:222-255` 一致 |
| HF2 | 关闭（新文本引出 HF13） | `:146-154` 写定 `Decision`/`Reason`/`Err`/`Notice`；但未覆盖“流水线被其 ctx 取消”的情况，见 HF13 |
| HF3 | 关闭（新规则引出 HF15） | `:156-160` 冻结→Interrupt→取消，冻结与启动下一条同锁判定；“两条目之间不发”对兼容单条目改变了现状，见 HF15 |
| HF4 | 关闭 | M 下 `PreToolName`/`PostToolName` 签名不变（`:185`）；`PostToolUse` 经通用 `Call` 的路由需写明，见 HF18 |
| HF5 | 关闭 | M 下无 `events`、无条目级 glob |
| HF6 | 关闭 | M 下新条目有效性不依赖其他 `hooks.*`（`:167`）；默认超时取 `hooks.timeout_ms`，`<=0` 回落 3000（`hooks.go:242-245`），不构成校验约束 |
| HF7 | 关闭 | `:182` 定为 `internal/config/hooks_entries.go` 的 `ReadHookEntries() ([]HookEntry, error)` + `dispatchConfigReader` `case "hooks"`；与 `ReadMCPServerSettings`（`mcp.go:17`）同模式；`ValidateConfigCandidate` 先把候选载入 gkconfig 再分发（`config_candidate.go:31-44`），无参 Reader 可用 |
| HF8 | 关闭 | M 下新条目无 PreToolUse 字段（`:168`），与 F33 无冲突 |
| HF9 | 关闭 | `:83`、`:213` Interrupt 不进 trace；`:224` ptye2e 用假 hook 落盘记录；`:84` 打断来源与 `chat.go:748-812,823-833`、`followup.go:119-123`、`chat_turn.go:429-431`、`chat.go:532-533` 逐一相符 |
| HF10 | 关闭 | `:211` 列出被推迟的 gofer Stop 职责与 catch-up；一处措辞见 HF20 |
| HF11 | 部分关闭 | `:218-225` 补齐上轮①–⑥（①中 PreToolUse 多条目在 M 下不再适用）；HF13/HF14/HF15 引出的新验证项未覆盖 |
| HF12 | 关闭 | `:81` 行号改为 `chat.go:782-790`；`:209`、`:225` `filepath.Base`；`:182`、`:184` 条目错误路径与 `pre_tool_use` glob 文案（现有测试只断言 `post_tool_use` 用例，`hooks_test.go:139-140`，不受影响） |

## 核对结论摘要（非发现项）

- **条目接收的事件集合**（`:117`）：新条目收 SessionStart/UserPromptSubmit/Stop/SessionEnd 与被打断时的 Interrupt，不收 Pre/PostToolUse，与 D7 一致且可实现。
- **两阶段中兼容条目 0 的位置**：即时型在阶段一最前、等待型在阶段二（`:141-143`）。`stop_background` 下兼容条目必为等待型（`hooks.go:249-255`），故后台流水线总是“新条目阶段一 → 兼容条目阶段二”。分类边界见 HF17，`stop_args` 见 HF16。
- **前台续跑（AC2）**：阶段一 block 后 `Done` 交出 `Decision=block`、reason 非空、`Err=nil`，`chat.go:782-791` 开启注入回合并置 `stopHookActive`（`chat.go:559-563`）。成立。
- **`stop_hook_active` 全局**（`:144`）：与 `chat_turn.go:251` 的 `s.stopHookActive || s.bgStop.takeBlocked()` 一致。
- **锁与冻结**：同锁判定可消除上轮 HF3 竞态；交回路径与 `recordingWait.Interrupt` 的 `reply == ""` 防重（`hooks_record.go:117-121`）配合不会重复投递 reason。
- **`ReadHookEntries` / `Reader:"hooks"`**：`ConfigTypeYAML` 解析路径现成（`settings_registry.go:487-491`），整体替换写入可行；`Protected` 字段当前不存在（`settings_registry.go:41-47`），依赖 `suga-hyj7` 先合入或同版，设计已锁定发布顺序（`:170-173`）。
- **装配层**：`localenv.go:193` 的 `hooks.New()` 与 `Enabled = client != nil` 在只配 `hooks.commands` 时自然为启用，不需改 `localenv.go`（`:188`）；`inspect env` 的原始 `hooks` 映射已会带出 `commands`（`localenv.go:188-191`，已脱敏），与“展示条目 deferred”不冲突。
- **gofer `--wait` 链路**：上轮判断仍成立；阶段一只在等待前追加至多各条目超时之和（典型 10 秒），不改 gofer 的 `stop_wait_sec` 上限。

## 发现

### HF13 [HIGH] 合并规则“阶段一有 block 则 `Err=nil`”未排除流水线被取消，后台 Stop 被新 Stop 杀掉后仍会投递过期的续跑通知

- disposition: `CORE_BLOCKING` / `[BUG]`
- 证据:
  - 设计 `:150-151`：阶段一有 block → `Decision=block`、`Err=nil`，并声明“前台、后台、交回路径、trace 用同一规则”（`:146`）；`:162`：后台“新 Stop 杀掉旧任务时直接取消（同现状……）”。
  - 后台消费 `stop_background.go:67-79`：`Err != nil && ctx.Err() != nil` 才视为被杀并返回；`Err == nil` 时只要 `Decision=="block"` 且 reason 非空就 `b.blocked.Store(true)` 并 `r.Notify(...)`。现状单条目被杀时 `Err` 为取消/超时错误（`hooks.go:315-327,359-365`），所以不会通知。
  - 杀任务只取消 ctx 并最多等 5 秒（`tasks.go:658-667,825-837`）；`reporter.Notify` 不检查任务状态（`tasks.go:509-523`），`TakeNotices` 也不过滤已杀任务（`tasks.go:686-710`）。
  - 触发组合：`stop_background: true`，阶段一至少两条即时条目（M 下即两条及以上 `hooks.commands`，兼容条目在后台必为等待型）；A 已 block，B 仍在运行时下一回合结束触发新 Stop（后台模式不受输入打断，新回合可在 B 的超时内结束）。按 `:150-151`，被取消的旧流水线合并结果为 `block`+`Err=nil`。
- 影响: 旧回合的续跑 reason 作为任务通知送进会话、唤醒 chat 或进入正在运行的回合，并把 `stop_hook_active` 置真（`stop_background.go:77`、`chat_turn.go:251`）——模型收到基于过期状态的“继续”指令；与 `:162` 的“同现状”承诺相反。同一规则下，阶段一无 block 而被杀时合并 `Err=nil`，任务以 0 结束并打印 `decision: none`（现状为 `-1`），属次要差异。
- necessity proof: 按设计 `:150-151` 字面实现，上述合法配置下必然产生错误的用户可见结果（过期续跑通知）；`:146` 明确后台使用同一规则，计划阶段若自行加“取消时 `Err` 非空”就是改写设计已写定的合并语义，故须在设计中修正。属 BUG（结果错误）。
- 同类穷尽: 依赖合并 `Err` 判定“被取消”的消费点：①后台 `stop_background.go:67-69`（受影响）；②前台 `chat.go:782-788`——被打断时 `stopDone` 已置 nil（`chat.go:739-744,773-777`），不读 Done，不受影响；③交回路径 `recordingWait.Interrupt`（`hooks_record.go:119`）——其 reason 本来就要交回（设计 `:160`），不受影响；④chat 退出 `chat.go:532-533` 走 Interrupt，不受影响；⑤trace（`hooks_record.go:88-96`）——若按逐条记录则各条自带错误，不受影响，若记合并结果则被杀流水线记为 `ok`。
- 建议: 在 `:151` 增加首要规则：“流水线的 ctx 已取消（后台被新 Stop 杀掉或 chat 退出）时，`Err` 取 ctx 错误，不论阶段一是否有 block”；前台打断的 reason 只经 `Interrupt` 返回值交回（`:160`），不受此影响。验证要求 `:221` 的“阶段一运行中被新 Stop 杀掉时不启动阶段二”补“且已得的 block 不产生任务通知、不置 `stop_hook_active`”。

### HF14 [MEDIUM] 逐条 trace 的数据来源与字段只对 Stop 写定；`Call` 返回 `Response`、PreToolUse 自记 trace，且兼容条目失败的 status 会变化

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:185`“`Result` 增加逐条运行记录”、`:187`“`hooks_record.go` 按逐条运行记录逐条写 trace；`hooks_gate.go` 只消费合并结果，逻辑不变”、`:213`“每个条目每次运行记一条……只有兼容条目时同样带这两个字段，其余字段不变”。代码：①`Client.Call` 返回 `Response` 而非 `Result`（`hooks.go:79`），`recordingHooks.Call` 只能拿到 `Response`（`hooks_record.go:35-39`），SessionStart/UserPromptSubmit/PostToolUse/SessionEnd 都走这条路；②PreToolUse 的 trace 由 `hooks_gate.go:55-58` 直接调 `recordHookRun`，`ToolGate.PreTool` 返回 `(Response, error)`，无 `entry`/`program` 来源；③现状 `Call` 失败在 trace 中记为 `status=ok`（`hooks_record.go:38` 不带 `Err`），逐条记录若带“该条错误”，只配兼容条目时 SessionStart 等失败会变成 `error`/`cancelled`。
- 影响: 三处都有多种实现（逐条记录挂在 `Response` 上、改 `Call` 签名、或 Call 事件不逐条记；PreToolUse 带不带 `entry`/`program`；失败 status 变不变），其中一些违反 `:213`/D2 的“其余字段不变”。
- 同类穷尽: 所有写 `kind=hook` 的点：`hooks_record.go:38`（Call）、`:54`（StartStop）、`hooks_gate.go:58`（PreToolUse）；无其他 `recordHookRun` 调用。
- 建议: 写定：逐条运行记录同时挂在 `Response`（或给 `Call` 路径同等载体）上；PreToolUse 是否带 `entry=0`/`program`（带则 `hooks_gate.go` 要改，不能写“逻辑不变”）；普通事件逐条记录的失败是否进 status（若进，在 D2 中列为新增行为，并在 chat 层验证中加只配兼容条目时的 trace 断言）。

### HF15 [MEDIUM] “只给正在运行的条目发 Interrupt”改变了只配兼容条目时的现有 Interrupt 合同

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:136`、`:158`“两条目之间（无条目在运行）不发”。现状 `stopWait.Interrupt` 无条件先发 Interrupt 再取消（`hooks.go:407-415`），即使命令已结束、结果已在 `done` 中——`hooks_test.go:342-361` 正是以预先填好 `done` 的 wait 断言 Interrupt 被发出。会出现“已结束未消费”的路径：`nextChatInput` 每轮先 select 输入/唤醒/投递（`chat.go:737-768`），gofer 回复与输入同时到达时走打断；回合内插入消息接续（`chat_turn.go:429-431`）；chat 退出（`chat.go:532-533`）。
- 影响: 按 `:158` 实现时，只配 gofer 的用户在上述窗口内不再收到 Interrupt，与 D2/AC3“Interrupt 与现在一致”不符；而 `:186` 又说 `stopWait` 保留为原语、其测试不改，读者无法判断兼容单条目走 `stopWait` 原语（保持现状）还是走流水线（改变现状）——实现不唯一。影响限于竞态窗口，gofer 侧多一次或少一次结算心跳，故非阻断。
- 同类穷尽: 打断时“无条目在运行”的三种状态：①阶段一与阶段二之间（`:158` 已覆盖，应不发）；②流水线已全部结束但结果未被消费（本项）；③阶段一 block 后结束、阶段二未启动（同②）。
- 建议: 写明“流水线已结束时，向最后执行的条目发 Interrupt（保持单条目现状）；只有处于两条目之间时不发”，或明确接受该差异并从 D2 中列出；验证补“只配兼容条目、结果已到未消费时打断仍发 Interrupt”。

### HF16 [MEDIUM] `stop_args` 只在阶段二出现，即时型兼容条目在阶段一是否附加 `stop_args` 未写

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:143`“兼容条目为等待型时启动它（附加 `stop_args`）”、`:197`“兼容条目为等待型时带 stop_args 运行”；`:141` 阶段一的兼容条目未提 `stop_args`。现状 Stop 总是附加 `stop_args`（`hooks.go:336-339`），文档化的“只配 `stop_args`、不设 `stop_wait_sec`”配置（设计 `:88`、`:218`）正是即时型兼容条目带 `stop_args`。
- 影响: 读作“只有等待型才附加”时，该类现有配置的 Stop 参数被去掉，违反 AC3；`:122`“行为与现状完全相同”可推出正确读法，但两处文字互相矛盾，计划易照流程图实现。
- 同类穷尽: 提及 `stop_args` 的规范性文字：`:46`、`:88`、`:103`、`:122`、`:123`、`:143`、`:197`、`:218`；只有 `:143`、`:197` 把它绑定到等待型。
- 建议: `:141`、`:143`、`:197` 改为“兼容条目的 Stop 总附加 `stop_args`（同现状），与它在哪个阶段无关”。

### HF17 [LOW] “等待型条目”的两句定义在边界配置下结论相反

- disposition: `CORE_CORRECTIVE`
- 证据: `:61`“Stop 上限长于其普通超时的条目”与“设了 `stop_wait_sec`……时为等待型”。`stop_wait_sec=1`、`timeout_ms=5000` 时 Stop 上限为 3 秒（`hooks.go:249-250`）短于普通超时：按前句为即时型（阶段一最前），按后句为等待型（阶段二，可被续跑 block 跳过）。
- 影响: 决定兼容条目的阶段与是否被跳过，实现不唯一（配置罕见，故 LOW）。
- 建议: 只保留按键判定：“兼容条目在 `stop_wait_sec > 0` 或 `stop_background` 为真时为等待型”，删去按时长比较的句子。

### HF18 [LOW] PostToolUse 经通用 `Call` 发出，流水线 `Call` 需按事件名只委托兼容条目；`hooks.New` 的提前返回需改写

- disposition: `CORE_CORRECTIVE`
- 证据: ①PostToolUse 由 `chat_turn.go:302` 的 `s.hooks.Call` 发出，不经专用方法；设计 `:185` 只写 `PreToolName`/`PostToolName`/`PreTool` 委托兼容条目，`:134` 表格写“只有兼容条目”，未说明流水线 `Call` 按 `event.Name` 分流。②`hooks.New` 在 `hooks.command` 为空时直接 `return nil, nil`（`hooks.go:216-218`），设计 `:184`“保留兼容条目的现有解析，再……追加新条目”需要先去掉这一返回才能满足 `:124`“只配 `hooks.commands` 也启用”。
- 影响: 前者若照 `:185` 实现，`PostToolUse` 会发给新条目（续跑 hook 对其无输出退出，功能无害，但违反 D7 且增加每次工具调用的延迟）；后者是可推出的改动，写明可防遗漏。
- 建议: 架构写明“流水线 `Call` 对 `PostToolUse` 只调兼容条目、无兼容条目时不发”；`hooks.New` 的启用条件改为“兼容条目或新条目任一非空”，验证补“只配 `hooks.commands` 时 `New` 返回非 nil、`PostToolName` 为 false”。

### HF19 [LOW] 阶段一条目的 Notice 显示时机未写：会被推迟到等待结束，打断时丢失

- disposition: `CORE_CORRECTIVE`
- 证据: `:152` Notice 并入合并结果；前台只在读到 `Done` 时打印 Notice（`chat.go:782-785`），打断后不再读 `Done`（`chat.go:739-744`），`Interrupt` 只交回 reason（`hooks.go:417-421`）。
- 影响: 新条目在阶段一写到 stderr 的提示，要等 gofer 等待（最长约 2 小时）结束才显示，用户输入打断则不显示。现状单条目也是“结束时显示、打断时丢”，但多条目下延迟来自另一条命令，用户无法预期。
- 建议: 一句写明取舍：接受（并在用户文档提示新条目勿依赖 stderr 提示），或阶段一结束且进入阶段二时先打印阶段一 Notice。

### HF20 [LOW] gofer 影响中“其 turn 由下一次 UserPromptSubmit 结算”措辞不准

- disposition: `CORE_CORRECTIVE`
- 证据: `:211`。gofer 在 Stop 时才打开等待 web 回复的 turn（gofer `run.go:664`，上轮核对）；阶段一被打断时 gofer 未收到 Stop，没有新开的 turn 需要结算，遗留的只是该回合的“运行中”状态与未上报的最后回复。
- 建议: 改为“阶段一被打断时 gofer 该回合既无 Stop 也无 Interrupt：不打开等待 turn，本回合最后回复不上报，web 保持运行中直到下一次 UserPromptSubmit”。

## 覆盖缺口与限制

- 静态阅读，未运行代码或测试。
- HF13 中“过期通知会被送进会话”基于 `tasks.go` 的 Notify/TakeNotices 不过滤已杀任务的静态推断，未实测时序。
- IDEV-STD 续跑 hook 的 stderr 行为与“最多 8 次”上界（`:211`）未核实。
- 候选之后的提交 `341237b3` 已记录 `suga-hyj7` 计划 0.4 获批，设计 `:170` 的“计划 0.4 待批”在候选时点成立，不作发现。
- Standards/Governance 轴不在本轴范围。

## 结论

BLOCKED：HF13（`CORE_BLOCKING`/BUG）——合并规则在流水线被取消时仍给出 `block`+`Err=nil`，`stop_background` 下被新 Stop 杀掉的旧流水线会投递过期续跑通知并置 `stop_hook_active`，与 `:162`“同现状”相反。上轮 HF1 已关闭；HF14–HF20 为 `CORE_CORRECTIVE`。

## 剩余风险与后续

- 修订时按类自查：①合并结果在“取消/被杀/打断/正常结束”四种结束方式下每个字段的值（HF13）；②trace 的每个写入点的数据来源与只配兼容条目时的字段差异（HF14）；③兼容单条目经流水线后与 `stopWait`/`call` 现状的逐项差异——Interrupt、`stop_args`、阶段归属（HF15–HF17）；④经通用 `Call` 发出的事件按名分流（HF18）。
- 第 3 轮读 `git diff 096c9e93..<新 candidate> -- docs/design/2026-11-12-multi-command-hooks-design.md` 与 §4、架构、验证要求。
