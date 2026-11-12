<!-- template_id: design; template_version: 1.2.0 -->
# suga chat hooks 多命令 设计

> 状态：Draft 0.4 / 待人工计划批准；issue `suga-5xmf`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-12 | Claude | 初稿：多个 hook 命令共存的配置形状、各事件执行与合并规则、Stop 长等待与 Interrupt；核心选择列为 Q1–Q7 待确认 |
| 0.2 | 2026-11-12 | Claude | 处理第 1 轮两轴评审（`docs/review/2026-11-12-multi-command-hooks/`）：新校验只作用于 `hooks.commands` 条目，兼容条目不变（HF1）；Q1 增最小选项 M 并改为推荐，逐项对照验收，A 的追加规则单列，写明 A 与安全配置键设计 F33 的冲突与替代声明（HS1、HS2、HF8）；条目解析放 `internal/config`（HS3、HF7）；写定 Stop 合并结果字段、打断的冻结顺序、Interrupt 失败日志与不进 trace、打断来源全集（HF2、HF3、HF9、HS4）；锁定随 `suga-hyj7` 同版或其后发布（HS5）；补 gofer 影响与验证项、修正引用（HF4–HF6、HF10–HF12） |
| 0.3 | 2026-11-12 | Claude | 记录用户选择（2026-11-12：“hooks Q1 选 M，Q2–Q7 按推荐”），Q1–Q7 转为决策 D7–D13：新条目只有 `command`+`timeout_ms`，等待与 Pre/PostToolUse 过滤只来自兼容条目；删去未选方案 A 的追加规则、F33 替代声明与只属于 A 的验证项；写明与 `suga-hyj7` 分类表的衔接 |
| 0.4 | 2026-11-12 | Claude | 处理第 2 轮两轴评审：合并规则加首要规则——流水线 ctx 已取消时 `Err` 取 ctx 错误、不出 block，后台被杀不发过期续跑通知（HF13）；只配兼容条目时仍走现有单条目实现，合同不变；流水线中兼容条目的 Stop 总附加 `stop_args`、等待型只按键判定、流水线已结束时打断仍向最后执行的条目发 Interrupt（HF15–HF17）；`Call` 按事件名分流、`hooks.New` 任一键非空即启用（HF18）；写定逐条 trace 的载体与挂点，普通事件失败的 trace status 与现状一致（HF14）；阶段一 Notice 随最终结果显示的取舍、gofer 描述更正（HF19、HF20）；Stop 单条失败记 Warn、取消与超时记 Debug（HS4）；删去顺带修改 glob 错误文案（HS6） |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

第 1 轮评审的处理：

| 评审项 | 处理 |
|---|---|
| HF1（阻断） | 全部新校验只作用于 `hooks.commands` 条目；`hooks.command` 及其配套键的接受范围与行为不变（含 `stop_background` 下只配 `stop_args` 的 7 天上限）；验证补“现有配置照常启动”用例（§2、§5、验证要求） |
| HS1、HS2、HF8 | Q1 增最小选项 M（条目只有 `command`+`timeout_ms`，不能等待、不收 Pre/PostToolUse），逐项列出各选项字段对应的验收；推荐改为 M。0.3 用户选 M（D7），与 F33 无冲突，A 的追加规则与替代声明删去 |
| HS3、HF7 | 条目解析与校验放 `internal/config`（`ReadHookEntries`，沿用 Reader 分发），`internal/hooks` 消费；`internal/config` 不依赖 `internal/hooks` |
| HS4 | D1 去掉 GR108 误引，改为说明装配层只构造；Interrupt 失败沿用 `Call` 的 Warn 日志（GR109） |
| HS5 | 锁定随 `suga-hyj7` 同版或其后发布 |
| HF2 | §4 写定合并 Stop 结果的 `Decision`/`Reason`/`Err`/`Notice` 及各消费点含义 |
| HF3 | §4 写定打断顺序：冻结流水线 → 向正在运行的条目发 Interrupt → 取消；两条目之间不发 |
| HF4 | 选 M（D7）后 Pre/PostToolUse 路径不变，`PreToolName`/`PostToolName` 签名不改 |
| HF5 | 选 M 后不存在（无 `events`、无条目级 glob） |
| HF6 | 选 M 后无跨键约束，兼容键维持无 Reader |
| HF9 | Interrupt 不进 trace（同现状）；ptye2e 经假 hook 的落盘记录观察；打断来源按代码列全（§4） |
| HF10 | 补 gofer Stop 被推迟的职责与 catch-up 兜底（安全、数据、运维与回滚） |
| HF11 | 验证要求补 chat 层逐条 trace、阶段切换打断、后台 Stop 被新 Stop 杀掉、合并 Err、`stopWait` 夹具（0.2 中只属于 A 的跨键 set、PreToolUse 多条目项在 0.3 删去） |
| HF12 | 修正前台 block 处理行号为 `chat.go:782-790`；程序名用 `filepath.Base`；条目错误按 `hooks.commands[i].<字段>` 报告（0.4 起不再顺带修正现有 `pre_tool_use` glob 错误文案，见 HS6） |

第 2 轮评审的处理（报告 `docs/review/2026-11-12-multi-command-hooks/standards-review.md`、`spec-review.md`）：

| 评审项 | 处理 |
|---|---|
| HF13（阻断） | §4 合并规则加首要规则 R0：流水线 ctx 已取消（后台被新 Stop 杀掉、chat 退出、前台打断）时 `Err` 取 ctx 错误、`Decision`/`Reason` 为空；后台不发通知、不置 `stop_hook_active`；前台打断已得的 reason 只经 `Interrupt` 返回值交回。同类穷尽列于 §4；验证补“后台 A 已 block、B 在跑时被新 Stop 杀掉，不发过期续跑通知” |
| HF14 | 架构写定：逐条运行记录 `Runs` 挂在 `Response` 上（`Call`、`PreTool`、Stop 的 `Result.Response` 同一载体）；`recordingHooks` 与 `hooks_gate.go` 按 `Runs` 写 trace（PreToolUse 带 `entry=0`/`program`）；普通事件逐条记录的 status 与现状一致（失败仍记 `ok`，只记 Warn 日志） |
| HF15 | 没有 `hooks.commands` 时 `hooks.New` 返回现有单条目实现（`commandClient`/`stopWait`），合同不变；有新条目时，流水线已全部结束（结果未消费）被打断仍向最后执行的条目发 Interrupt，只有两条目之间不发 |
| HF16 | 兼容条目的 Stop 总附加 `stop_args`（同现状），与所在阶段无关 |
| HF17 | 等待型只按键判定（`stop_wait_sec > 0` 或 `stop_background`），删去按时长比较的定义 |
| HF18 | 流水线 `Call` 按事件名分流：PostToolUse 只发兼容条目，无兼容条目时不发；`hooks.New` 在兼容条目或新条目任一非空时返回 client |
| HF19 | 接受：阶段一 Notice 随最终结果显示，打断时与现状一样不显示；用户文档提示新条目不要依赖 stderr 提示，trace 逐条记 `notice` |
| HF20 | gofer 影响改为“阶段一被打断时 gofer 不打开等待 turn、本回合最后回复不上报，web 保持运行中直到下一次 UserPromptSubmit” |
| HS4（遗留） | Stop 单条失败记 Warn，取消与超时记 Debug（同现状 `StartStop`） |
| HS6 | 删去“顺带修正 `pre_tool_use` glob 错误文案”，列为 deferred（可另开 Quick issue） |
| HS7、HS8 | 引用纠正：发布顺序锁定改引 D6；第 1 轮评审来源改指 `docs/review/2026-11-12-multi-command-hooks/` |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：`suga chat` 能同时运行多个 hook 命令（典型组合：gofer 的 `gofer hook generic --agent suga` 负责会话上报与 Stop 长等待，IDEV-STD 的 `idevstd script continuation_hook --agent suga` 负责续跑），新命令的 Stop block 能生效；只配 `hooks.command` 的现有用户行为与能否启动都不变。
- 验收（当前已证实的需要）：AC1 新命令与 gofer 的 `hooks.command` 共存；AC2 新命令 Stop 的 block 开启下一回合；AC3 只配 `hooks.command` 时行为不变、现有配置照常启动；AC4 新命令可单独设超时（IDEV-STD 需要 10 秒，`idev-standards-public/docs/design/2026-11-12-continuation-hooks-split-plans-1-3-0-design.md:92`）。
- scope freeze：`internal/hooks` 的多命令执行与合并、`internal/config` 的条目解析与注册表、chat 对 hook 结果的消费点（Stop 等待、后台 Stop、trace 记录）、文档。不改事件 JSON 与 stdout 回答格式；不做项目级 hook；不做 shell 解析；不给 ACP、`suga run`、SDK 增加 hooks。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget：最多 8 轮（`planning-core-rigorous`），本版为第 2 轮之后的修订。
- 暴露度（SR1409）：高（已发布 `v0.55.0`；涉及持久命令执行、执行前拒绝门、gofer 的 Stop 等待链路）。Delivery Track：Full。两轴独立评审与 ledger，评审目录 `docs/review/2026-11-12-multi-command-hooks/`。
- 停止条件：无开放的 `CORE_BLOCKING`（Q1–Q7 已由用户选定，见 D7–D13）。

## 背景与目标

- 现状：`hooks.command` 是唯一的全局命令，每个事件运行一次，事件 JSON 一行写入 stdin（`internal/hooks/hooks.go:1-10`、`:208-265`）。配套键 `hooks.stop_args`、`hooks.stop_wait_sec`、`hooks.stop_background`、`hooks.timeout_ms`、`hooks.pre_tool_use.*`、`hooks.post_tool_use.*` 都只服务这一条命令。
- gofer 用这条命令做会话上报，并用 Stop `--wait <秒>` 长时间等待 web 回复（`docs/usage/gofer-personal-agent.md:390-402`）；等待期间被打断时，suga 先发 Interrupt 再结束等待（`hooks.go:407-422`）。
- IDEV-STD 1.3.0 新增续跑 Stop hook：只处理 `hook_event_name=="Stop"`（其余事件无输出退出 0），需要继续时退出码 0 并输出 `{"decision":"block","reason":…}`；不需要 exit 2 语义。其设计 Q2 记录“suga 只有一条全局命令，已被其他工具占用时无法并存”，安装命令待本设计定稿后按新配置形状输出（同上文件 `:41`、`:75`、`:92`、`:143`）。
- 用户 2026-11-12 原话：“hooks 并行出设计”（与 `suga-hyj7` 安全配置键计划并行）。

用户可见结果：
- 你保留原有的 gofer `hooks.command` 配置，再加一条续跑命令，两者同时生效：回合结束时先问续跑命令，它要求继续就直接开始下一回合；不继续时 gofer 照常等待 web 回复。
- 只配 `hooks.command` 时，事件、超时、等待、Interrupt、trace 与现在一致（trace 只多两个 attrs），现有配置照常启动。
- 新条目写错时 `suga chat` 启动报错并指出 `hooks.commands[i]` 的哪个字段；`suga config set` 写入非法值时拒绝。

## 名词

- **hook 条目（entry）**：一条 hook 命令及其设置。
- **兼容条目**：由 `hooks.command` 及其配套键组成的那一条，编号 0，解析与行为与现状相同。
- **新条目**：`hooks.commands` 中的条目，编号 1…。
- **等待型条目**：只按键判定——兼容条目在 `hooks.stop_wait_sec > 0` 或 `hooks.stop_background` 为真时为等待型（`hooks.go:247-255`）；新条目按 D7 永远不是等待型。
- **即时条目**：其余条目（全部新条目，以及不满足上述条件的兼容条目）。
- **合并结果**：一个事件所有条目回答合成的单个 `Response`/`Result`，chat 只消费它。

## 范围与非目标

范围：新配置键 `hooks.commands`（D7）及与 `hooks.command` 的共存（D11）；SessionStart、UserPromptSubmit、Stop、SessionEnd、Interrupt 的多条目执行与合并（D8–D10）；Stop 前台等待、打断、`stop_background` 的多条目语义；注册表描述符与 protected、`suga config set/get`；trace；文档（README、`docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/config.md`）。

非目标：项目级 hook（D13）；`inspect env` 展示条目（deferred，SR1405）；条目级 `stop_background`；事件 JSON、回答格式与 exit 2 语义的变化；ACP、`suga run`、SDK（含 xhen）的 hooks（`pkg/sugasdk/doc.go:94`）。按 D7 另不做：条目级事件订阅、条目级 Stop 等待、新条目收 PreToolUse/PostToolUse（均 deferred，等出现第二个已证实用例再议，SR1405.1）。

受影响项目与接口：
- suga：新增配置键（只增）；trace `hook` 事件 attrs 新增 `entry`、`program`（只增）；`internal/hooks` 内部类型调整（只在本仓库使用）。
- gofer：现有配置无需改动；只在另加了续跑等条目时行为有变（见“安全、数据、运维与回滚”）。gofer 仓库内未见写入 suga hooks 配置的代码。
- IDEV-STD：`idevstd hooks install --agent suga` 改为输出 `suga config set hooks.commands '<YAML>'`，去掉“冲突则不给命令”的分支，也不再需要改全局 `hooks.timeout_ms`。
- xhen：无。

## 已确认事实与规范

- **调用点**：`hooks.New` 只在 `internal/cli/chat.go:21` 与 `internal/wiring/localenv.go:193` 被调用；`internal/hooks` 只被 `internal/chat`、`internal/cli`、`internal/wiring` 导入。`internal/hooks` 只依赖 `pkg/agentcore/api`，`internal/config` 不依赖 `internal/hooks`，两包互不依赖。
- **事件全集**：SessionStart（`internal/chat/chat.go:492`）、UserPromptSubmit（`chat_turn.go:235`）、PreToolUse（`hooks_gate.go:51`）、PostToolUse（`chat_turn.go:295`）、Stop（`chat_turn.go:395`）、SessionEnd（`chat.go:539`）、Interrupt（`hooks.go:407-414`，只在前台 Stop 等待被打断时发）。
- **各事件对回答的使用**：SessionStart、UserPromptSubmit 取 `additionalContext` 并显示 stderr notice（`chat.go:494-496`、`chat_turn.go:240-242`）；PostToolUse 只显示 notice（`chat_turn.go:302`）；SessionEnd、Interrupt 回答被忽略；PreToolUse 的 deny 拒绝调用、`updatedInput` 改写参数、失败一律拒绝（`hooks_gate.go:43-80`）；Stop 前台由 `chat.go:782-790` 消费（`Err != nil`、非 block 或 reason 为空即“无决定”，并打印 Notice），后台由 `stop_background.go:62-82` 消费（`Err != nil` 时任务以错误结束）；交回路径要求 `Err == nil` 才交回 block reason（`hooks.go:418`、`hooks_record.go:119`）。
- **失败处理**：非 PreToolUse 事件失败记 Warn（`hooks.go:302-308`，Interrupt 也经此路径）；Stop 超时与取消不记为错误（`hooks.go:316-326`、`hooks_record.go:88-96`）。
- **Interrupt 合同**：先发 Interrupt（1 秒超时），再取消等待，并交回已收到但未消费的 block reason（`hooks.go:407-422`、`hooks_record.go:112-128`）；顺序由 `hooks_test.go:342-361` 守住，该测试直接构造 `stopWait`。Interrupt 调用不进 trace（`stopWait.call` 是未包装的 `Call`）。
- **打断来源**：前台等待期间——输入活动（`chat.go:823-828`）、Ctrl-C/EOF（`:830-833`）、任务唤醒（`:748-759`、`:792-803`）、`suga send` 投递（`:760-768`、`:804-812`）、Tab 排队消息（`followup.go:119-123`）、回合内插入消息使回合结束后直接接续（`chat_turn.go:429-431`）、chat 退出（`chat.go:532-533`）。全部经 `Wait.Interrupt`。
- **后台 Stop**：整个 Stop 作为一个 `hook` 任务运行，新 Stop 杀掉旧的，block 转为任务通知（`stop_background.go:16-86`）。
- **trace**：每次调用记一条 `kind=hook` 事件（`hooks_record.go:64-100`）；PreToolUse 另带 `rewritten_args`（`hooks_gate.go:57-58`）。
- **注册表**：`hooks.*` 现有 7 个描述符（`internal/config/settings_registry.go:273-279`，无 Reader）；`ConfigTypeYAML` + Reader 的先例是 `mcp.servers`（`:192`，`ReadMCPServerSettings`）；Reader 分发是 `internal/config` 内的 switch（`settings_registry.go:678-735`）；`config set` 只按被改键的 Reader 校验（`config_candidate.go:41`、`internal/cli/config.go:165`）。
- **兼容配置的现有接受范围**：`hooks.New` 不要求 `stop_args` 配 `stop_wait_sec`（`hooks.go:222-255`），文档允许 `stop_background: true` 下只配 `stop_args`（`docs/usage/gofer-personal-agent.md:213`、`:402`）。
- **安全配置键设计**（`docs/design/2026-11-11-protected-config-keys-design.md`，Draft 0.8 已批准进入计划）：执行类 protected 含 `hooks.command`、`hooks.stop_args`、`hooks.pre_tool_use.*`、`hooks.post_tool_use.*`；`hooks.pre_tool_use.*` 只标 protected、不可写、不新增 CLI 能力（F33，该文 `:48`、`:120`）；`hooks.timeout_ms`、`stop_wait_sec`、`stop_background` 不保护；显式分类表要求每个注册表键归类。
- **gofer Stop 的职责**：除打开等待 web 回复的 turn，还上报本回合 `LastMessage`/`IdleSec`/`ClearProgress`、接管通知、信件与 watched job 的送达（gofer `internal/hookrelay/run.go:570-664`）；靠 Interrupt 结算打开的 turn（`:246-248`、`:265-282`）；UserPromptSubmit 经 catch-up 补投 job 通知（`:212-226`、`:933-937`）；不使用 `stop_hook_active`。
- **规范**：SR1301、SR1403、SR1405、SR1405.1、SR1410、GR109、GR110、SR2004；验证要求见 `AGENTS.md`“验证”节。

## 总体方案

按用户选定的 Q1-M 与 Q2–Q7 推荐项（D7–D13）。未选方案 A 的追加规则（条目级事件订阅、条目级 Stop 等待、新条目的 Pre/PostToolUse 过滤、跨键校验）不实施。

### 1 配置形状（D7）

```yaml
hooks:
  # 兼容条目（编号 0），旧配置原样有效，等待与工具过滤只在这里
  command: [gofer, hook, generic, --agent, suga]
  stop_args: [--wait, "7140"]
  stop_wait_sec: 7140
  post_tool_use: {tools: ["*shell*", "*exec*"], name: shell}
  # 新条目（编号 1…）
  commands:
    - command: [idevstd, script, continuation_hook, --agent, suga]
      timeout_ms: 10000
```

| 新条目字段 | 默认 | 说明 |
|---|---|---|
| `command` | 必填 | argv 列表，直接执行（不经 shell），程序名非空 |
| `timeout_ms` | `hooks.timeout_ms`（再缺省 3000） | 本条目全部事件的超时，含 Stop；正整数 |

- 新条目收 SessionStart、UserPromptSubmit、Stop、SessionEnd；Stop 被打断时收 Interrupt（§4）；不收 PreToolUse/PostToolUse；不能等待（`stop_background` 下也按自己的 `timeout_ms`）。
- `hooks.stop_background` 仍是全局开关；`SUGA_HOOKS=off` 关闭全部条目。

### 2 兼容条目与共存（D11）

- `hooks.command` 存在时构成兼容条目 0，解析、校验、超时（含 `stop_background` 下的 7 天上限）、订阅的事件与现状完全相同；配套键只属于它（`hooks.timeout_ms` 另作新条目的默认超时）。等待型条目与 PreToolUse/PostToolUse 过滤只来自兼容条目。
- **只配兼容条目时走现有实现**（HF15）：没有 `hooks.commands`（或为空）时，`hooks.New` 返回现有单条目实现（`commandClient` 与 `stopWait`），Stop、Interrupt（无条件先发再取消）、`stop_args` 附加、超时与日志合同都不变，只在回答上附带一条运行记录供 trace 取 `entry`/`program`（§3）。有新条目时才走流水线。
- **启用条件**（HF18）：`hooks.command` 或 `hooks.commands` 任一非空即返回 client；两者都未设或为空时返回 nil（hooks 不启用），`SUGA_HOOKS=off` 同现状。
- **新校验只作用于新条目**（HF1）：程序名非空、`timeout_ms` 为正整数、条目只含 `command` 与 `timeout_ms`（未知字段报错，防止误以为可配 `stop_args` 等）。兼容条目不增加任何检查，现有配置不会因升级而启动失败。
- 两个键可同时存在，这正是 IDEV-STD 安装的场景：只写 `hooks.commands`，不碰已有配置。

### 3 各事件的执行与合并（D8）

订阅某事件的条目按编号顺序逐个执行，每条用自己的超时，不共享总预算。流水线的 `Call` 按事件名分流（HF18）：PostToolUse 只发兼容条目（无兼容条目时 `PostToolName` 为 false，不发）；SessionStart、UserPromptSubmit、SessionEnd 发给全部条目。PreToolUse 经 `PreTool` 只委托兼容条目。

| 事件 | 执行 | 单条失败或超时 | 合并 |
|---|---|---|---|
| SessionStart / UserPromptSubmit | 顺序，全部执行 | 记 Warn（沿用 `Call`），跳过该条 | `additionalContext` 按顺序以空行连接；notice 按顺序换行连接 |
| SessionEnd | 顺序，全部执行 | 同上 | 忽略回答 |
| PreToolUse / PostToolUse | 只有兼容条目，路径与签名与现状相同 | 同现状 | 同现状 |
| Stop | 两阶段（§4） | 失败记 Warn，取消与超时记 Debug（同现状 `StartStop`，HS4），视为无决定 | §4 |
| Interrupt | 按 §4 打断规则发给一个条目 | 记 Warn（沿用 `Call`，GR109），不影响取消 | 无 |

**逐条 trace 的载体与挂点**（HF14）：
- `Response` 增加 `Runs []EntryRun`（编号、`filepath.Base(argv[0])`、耗时、该条回答、该条错误）；`Call`、`PreTool` 的返回值与 Stop 的 `Result.Response` 用同一载体，单条目实现也填一条（编号 0）。
- `recordingHooks.Call` 与 Stop 记录：`Runs` 非空时每条写一个 `kind=hook` 事件（attrs 用该条回答，另加 `entry`、`program`），为空时按现状写一条。
- 普通事件（`Call` 路径）逐条记录的 status 与现状一致：不因该条失败改为 `error`/`cancelled`（现状 `hooks_record.go:38` 不带 `Err`），失败只记 Warn 日志；Stop 逐条记录按该条错误定 status，规则同现状（`hooks_record.go:88-96`）。因此只配兼容条目时 trace 只多 `entry`/`program` 两个 attrs（D2）。
- PreToolUse：`hooks_gate.go` 从 `response.Runs` 取 `entry=0`/`program` 加入 attrs（`hooks_gate.go` 只此一处改动；拒绝与改写逻辑不变）。

### 4 Stop：两阶段、合并结果与打断（D9、D10）

以下只适用于存在新条目的流水线；只配兼容条目时见 §2（现有实现）。

**执行**：
1. 阶段一：即时条目按编号顺序执行（兼容条目为即时型时排在最前），各自超时；全部执行，不因前一个 block 短路（D10）。
2. 阶段一有任一 block（该条 `Err==nil`、`Decision=="block"`、reason 非空）→ 不启动等待型条目，返回合并结果。
3. 否则阶段二：兼容条目为等待型时启动它；否则立即结束。
4. 兼容条目的 Stop **总附加 `stop_args`**（同现状 `hooks.go:336-339`），与它在阶段一还是阶段二无关（HF16）。
5. `stop_hook_active` 仍是全局的：上一次合并结果为 block（前台回复或后台通知）时为真，对所有条目相同。

**合并结果**（HF2、HF13；前台、后台、交回路径用同一规则，按顺序取第一条适用的规则）：

| 规则 | 条件 | `Decision`/`Reason` | `Err` |
|---|---|---|---|
| R0（首要） | 流水线 ctx 已取消（后台被新 Stop 杀掉、chat 退出取消父 ctx、前台打断后的取消） | 空 | ctx 错误（`errors.Is` 可判 `context.Canceled`） |
| R1 | 阶段一有 block | `block`，各 block 的 reason 按顺序以空行连接 | `nil`（阶段一其他条的失败只记日志与逐条 trace） |
| R2 | 阶段二已运行 | 取等待条目的回答 | 取等待条目的错误（含其自身上限 `ErrTimedOut`，同现状） |
| R3 | 其余（无等待条目） | 空 | `nil` |

`Notice`：已执行条目的 notice 按执行顺序换行连接（各规则相同）。

R0 的同类穷尽（HF13）：
- 后台被新 Stop 杀掉（`stop_background.go:67-69`）：`Err` 非空且 ctx 已取消 → 任务以 `-1` 结束，不发通知、不置 `blocked`/`stop_hook_active`（与现状单条目被杀一致）；阶段一已得的 block 丢弃，因为它属于已被新 Stop 取代的回合。
- chat 退出：前台经 `Interrupt`（`chat.go:532-533`）交回 reason 并告警“未投递”，同现状；后台任务随父 ctx 取消按上一条处理。
- 前台打断：打断后 chat 不再读 `Done`（`chat.go:739-744`、`:773-777`），已得的 reason 只经 `Interrupt` 返回值交回（见下），`recordingWait.Interrupt` 只在 `Err==nil` 时补取，R0 使其不会重复投递。
- 单条超时（新条目 `timeout_ms`、兼容条目自身上限）不是流水线取消：只影响该条（阶段一视为无决定；阶段二按 R2，同现状）。
- trace 逐条记录各条自己的错误，不受合并规则影响。

由此：前台（`chat.go:782-790`）在阶段一 block 加另一条失败时仍开启下一回合；后台任务只在被杀或“无 block 且等待条目失败”时以错误结束（与现状一致）。

**打断**（HF3、HF15；来源见“已确认事实”，全部经 `Wait.Interrupt`）：
1. 先冻结流水线：之后不再启动任何条目（与“启动下一条”在同一把锁内判定，避免阶段切换竞态）；
2. 发 Interrupt（1 秒超时，失败记 Warn）给一个条目：有条目在运行时发给它；流水线已全部结束（结果已到但未被消费）时发给最后执行的条目，与单条目现状一致；处于两条目之间（前一条已结束、下一条未启动）时不发；
3. 再取消正在运行的条目（流水线 ctx 取消，`Done` 结果按 R0）；
4. 返回值：已完成条目中 R1 意义下的 block reason，或等待条目已到但未消费的 block 回复，交给现有 `lostReply` 路径（`chat_turn.go:244-247`）。

**Notice 的显示时机**（HF19）：接受阶段一 Notice 并入最终结果、随 `Done` 一起显示；打断时与现状一样不显示。代价是阶段一提示要等阶段二（等待型 gofer）结束才出现。用户文档写明新条目不要依赖 stderr 提示传达重要信息；trace 逐条记 `notice`。

**后台 Stop**（`stop_background: true`）：整个流水线作为一个 `hook` 任务运行，顺序、短路与合并规则相同；不被输入打断，不发 Interrupt；新 Stop 杀掉旧任务时直接取消（同现状，不发 Interrupt），结果按 R0；R1 的 block 转为一条任务通知。

### 5 注册表、`suga config` 与 protected（D12）

- 新描述符 `hooks.commands`：`ConfigTypeYAML`、`Writable:true`、`Protected:true`、`Reader:"hooks"`。`config set` 写入时经 `ReadHookEntries` 做与启动相同的条目校验（只校验 `command` 与 `timeout_ms`），非法值退出码 2、文件不变；`suga config get hooks.commands` 输出 YAML；整体替换写入，不提供追加子命令（SR1403）。
- 没有跨键约束（新条目的有效性不依赖其他 `hooks.*` 键），兼容键维持无 Reader。
- 新条目不含 PreToolUse 过滤器，与安全配置键设计 F33（`hooks.pre_tool_use.*` 不新增 CLI 能力）无冲突，无需替代声明。

**与 `suga-hyj7`（安全配置键，计划 0.4 已批准）的衔接**：
- `hooks.commands` 在其显式分类表中归“执行类”、`Protected:true`；启动比较按整块值求摘要，条目内 `timeout_ms` 的变化也会被报出（可接受：属于执行配置）。
- 两者谁后合入，谁在分类表补 `hooks.commands` 一行（`suga-hyj7` 后合入时由其计划补；本特性后合入时由本计划补）；分类表测试兜底，漏补即失败。
- 发布顺序锁定（D6、HS5）：本特性随 `suga-hyj7` 同版或其后发布，`hooks.commands` 自首个版本起即为 protected；不单独先发。

### 6 不纳入项目级 hook 配置（D13）

suga 只有一层配置（`SUGA_HOME/config.yaml`），没有读取仓库内配置的机制；项目级 hook 会让仓库内容（含克隆的第三方仓库、模型写入的文件）引入在每个事件上自动执行的程序，绕开 protected 键与启动提示两道防线。续跑命令是你个人工具链的一部分，放全局配置即可满足需求；需要按仓库区分时，命令自身可读事件的 `cwd` 判断。

## 架构

代码组织沿用 suga 现有分层（SR2004；参照本仓库 `internal/config` 的 `ReadMCPServerSettings` 与 Reader 分发、`internal/hooks`、`internal/chat`）：
- `internal/config` 新文件 `hooks_entries.go`：`ReadHookEntries() ([]HookEntry, error)` 读 `hooks.commands`、校验并返回解析后的新条目（argv、超时），错误指明 `hooks.commands[i].<字段>`；`dispatchConfigReader` 增 `case "hooks"`。基础包 `internal/config` 不依赖功能包 `internal/hooks`。
- `internal/hooks`：
  - `hooks.New`：保留兼容条目的现有解析；去掉“`hooks.command` 为空即返回 nil”的提前返回，改为调用 `config.ReadHookEntries` 后按 §2 启用条件决定；无新条目时返回现有 `commandClient`，有新条目时返回流水线（新增依赖方向 `internal/hooks → internal/config`，无环）。
  - 新文件 `pipeline.go`：多条目 `Client`——按事件名分流、顺序执行与合并、两阶段 Stop、R0–R3、冻结与打断；`PreToolName`/`PostToolName`/`PreTool` 只委托兼容条目，签名不变。
  - `Response` 增加 `Runs`（§3）；现有 `commandClient.call` 与 `stopWait` 保留为单条目执行原语，现有直接构造 `stopWait` 的测试不改。
- `internal/chat`：`hooks_record.go` 按 `Runs` 逐条写 trace（§3）；`hooks_gate.go` 只加 `entry`/`program` attrs；`chat.go`、`chat_turn.go`、`stop_background.go` 只消费合并结果，逻辑不变（R0 使后台被杀路径沿用现有 `ctx.Err() != nil` 分支）。
- 装配层（`internal/cli/chat.go`、`internal/wiring/localenv.go`）只构造 client，不含合并逻辑；`localenv.go` 不改（只配 `hooks.commands` 时 `Enabled` 自然为真）。
- 不改 `pkg/*`。
- deferred（HS6）：现有 `pre_tool_use.tools` 的 glob 错误文案误报为 `hooks.post_tool_use.tools`（`hooks.go:234-240`），不在本次范围，可另开 Quick issue。

## 关键流程

```text
hooks.New
  hooks.commands 为空 → hooks.command 非空 ? 现有单条目实现 : nil
  hooks.commands 非空 → 流水线（含兼容条目 0，若有）

回合结束 → Stop（流水线）
  阶段一：即时条目按编号顺序执行，各自超时（全部执行；兼容条目若在此附加 stop_args）
     ├─ 有 block → R1：Decision=block，reason 空行连接，Err=nil，跳过阶段二
     └─ 无 block → 阶段二：兼容条目为等待型时带 stop_args 运行 → R2
  任何时刻流水线 ctx 被取消 → R0：Err=ctx 错误，无 block
  前台打断：冻结 → Interrupt 给正在运行的条目（全部结束时给最后执行的条目；两条之间不发）
           → 取消 → 已得 block reason 交回 lostReply
  后台：整个流水线为一个 hook 任务；R1 → 任务通知；被新 Stop 杀掉 → R0，不通知

SessionStart / UserPromptSubmit / SessionEnd
  全部条目按编号顺序执行 → 失败记 Warn 跳过 → additionalContext、notice 按顺序连接
PostToolUse / PreToolUse → 只兼容条目
```

## 安全、数据、运维与回滚

- **SR1301**：鉴权——`hooks.commands` 为 protected，且随 `suga-hyj7` 同版或其后发布，模型在会话内不能经 `config set` 写入；注入防护——argv 直接执行，不经 shell；秘密——trace 只记 `filepath.Base(argv[0])` 与条目编号，不记参数；入参校验——启动与 `config set` 同一 `ReadHookEntries` 校验；资源归属——不适用。
- **执行门不变弱**：PreToolUse 路径不变，仍只由兼容条目决定。
- **对 gofer 的影响**（HF10、HF20）：只配 gofer 时无变化（走现有实现）。加了续跑条目且它要求继续时，该回合 gofer 不收 Stop，因此以下各项推迟到续跑结束后的那次 Stop：打开等待 web 回复的 turn、上报本回合最后回复与空闲时长、清进度、接管通知、信件与 watched job 的 Stop 送达；中间回合的最后回复不会出现在 web，web 侧会话保持“运行中”（IDEV-STD 以状态不前进与最多 8 次为界）。job 通知经下一次 UserPromptSubmit（带 `injected=true`）的 catch-up 补投，不丢。阶段一被打断时 gofer 该回合既无 Stop 也无 Interrupt：不打开等待 turn，本回合最后回复不上报，web 保持运行中直到下一次 UserPromptSubmit。
- **对 IDEV-STD 的影响**：安装命令改为 `suga config set hooks.commands '<YAML>'`（条目只有 `command` 与 `timeout_ms`）；已有 `hooks.commands` 时整体替换写入，需提示你合并；续跑提示不要依赖 stderr notice（HF19）。
- **trace**：每个条目每次运行记一条 `kind=hook` 事件，attrs 新增 `entry`、`program`；只配兼容条目时同样带这两个字段，其余字段与 status 不变（§3）。Interrupt 不进 trace（同现状）；打断时未启动的条目不记。
- **运维**：顺序执行使事件延迟为各条目耗时之和；SessionEnd 在退出时最多等待各条目超时之和；文档建议条目控制在少数几条。
- **数据**：无存储结构变化。
- **回滚**：revert；删掉 `hooks.commands` 即回到单命令行为（现有实现）。旧版本运行时不读取 `hooks.commands`，只剩兼容条目生效（计划阶段核对旧版本 `config validate` 对该键的提示）。
- **验证要求**（计划中落实）：
  - 兼容（HF1、HF15）：只配 `hooks.command` 的现有配置照常启动，含 `stop_background: true` + `stop_args` 无 `stop_wait_sec`、只配 `stop_args`；`hooks.New` 返回现有单条目实现；现有 `hooks_test.go`、`hooks_pretool_test.go` 断言不改；只配兼容条目时 trace 只多 `entry`/`program`，`Call` 失败仍记 `ok`（HF14）。
  - 启用（HF18）：只配 `hooks.commands` 时 `New` 返回非 nil、`PostToolName` 为 false、新条目不收 PostToolUse/PreToolUse。
  - 单元（`internal/config`）：`ReadHookEntries` 全部校验错误与字段路径；未知字段（如 `stop_args`）报错。
  - 单元（`internal/hooks`，假 hook 进程）：普通事件顺序、各自超时、单条失败不影响其他条、连接规则；即时型兼容条目在阶段一附加 `stop_args`（HF16）；`stop_wait_sec=1`、`timeout_ms=5000` 的兼容条目归阶段二（HF17）；Stop R1 跳过等待条目、多 block 合并、阶段一 block 加另一条失败时 `Err=nil` 且前台续跑（HF2）、R2 取等待条目错误；R0：流水线 ctx 取消时 `Err` 为取消错误且无 block（HF13）；打断：等待条目收 Interrupt、未启动条目不收、阶段一结束瞬间打断不启动等待条目且无条目收 Interrupt（HF3）、流水线已全部结束未消费时打断向最后执行的条目发 Interrupt（HF15）、已得 reason 交回且不重复投递；Stop 失败记 Warn、取消与超时记 Debug；Interrupt 失败记 Warn。
  - 后台 Stop：R1 block 变为任务通知、输入不打断；**`stop_background` 下条目 A 已 block、条目 B 仍在运行时被新 Stop 杀掉：不发过期续跑通知、不置 `stop_hook_active`、任务以 `-1` 结束、不启动阶段二**（HF13）。
  - GR110：经 `internal/cli` 真实命令分发——`suga config set hooks.commands` 合法值写入、非法值退出码 2 且文件不变、`config get` 输出；protected 拒绝随 `suga-hyj7` 一起验证。
  - chat 层：逐条 trace（`entry`/`program`/status）；PreToolUse 记录带 `entry=0`/`program`。
  - ptye2e（`test/ptye2e`，参照 `chat_observe_test.go` 的 `TestHookHelper` 假 hook）：配兼容条目（模拟等待型 gofer）与一条新条目（模拟续跑，第一次 block、之后不输出）；假 hook 把收到的事件名与自身标签追加到测试临时目录的记录文件。断言：续跑回合开始且该回合等待条目未收 Stop；下一次 Stop 等待条目运行；等待期间输入后记录文件中 Interrupt 只出现在等待条目；`suga inspect trace --json` 中每条 `hook` 事件的 `entry`/`program`/decision 正确。反向验证：临时去掉阶段一短路、临时把 Interrupt 发给全部条目、临时去掉 R0，确认对应检查失败（确认失败来自断言而非编译错误）。
  - Windows：经 gofer exec job 在主机跑 `go test ./internal/config/... ./internal/hooks/... ./internal/chat/...` 与 ptye2e ConPTY 场景（含被打断条目的进程终止、`filepath.Base` 对反斜杠路径）。

## 决策

- **D1** 多条目执行与合并全部在 `internal/hooks` 内完成，chat 只消费合并结果与逐条运行记录；装配层只构造 client。
- **D2** 只配 `hooks.command` 时走现有单条目实现，行为、超时、接受范围、Interrupt 与 trace 字段和 status（除新增 `entry`/`program` attrs）与现状一致；新校验只作用于 `hooks.commands` 条目；现有测试断言不改。
- **D3** 不引入 exit 2 语义、不改事件与回答格式。
- **D4** 条目解析与校验放 `internal/config`（`ReadHookEntries`），`internal/hooks` 消费。
- **D5** Stop 合并结果按 §4 的 R0–R3（ctx 已取消时首要按 R0，不出 block）；打断顺序按 §4；Stop 单条失败记 Warn、取消与超时记 Debug；Interrupt 失败记 Warn，不进 trace。
- **D6** 随 `suga-hyj7` 同版或其后发布；后合入者在其分类表补 `hooks.commands` 一行。
- **D7**（Q1 选 M）新条目 `hooks.commands[]` 只有 `command` + `timeout_ms`，收全部普通事件、不收 PreToolUse/PostToolUse、不能等待；等待与 Pre/Post 过滤只来自兼容条目。
- **D8**（Q2 选 A）同一事件的条目按编号顺序逐个执行，各自超时。
- **D9**（Q3 选 A）Stop 先即时、后等待：阶段一有 block 就不启动等待条目（只可能是兼容条目 0）；打断时冻结后按 §4 只给一个条目发 Interrupt。
- **D10**（Q4 选 A）阶段一全部执行，多个 block 的 reason 按顺序以空行连接。
- **D11**（Q5 选 A）`hooks.command` 与 `hooks.commands` 共存，前者为条目 0，配套键只属于它。
- **D12**（Q6 选 A）`hooks.commands` 可写 + protected，`Reader:"hooks"` 只校验 `command` 与 `timeout_ms`。
- **D13**（Q7 选 A）不纳入项目级 hook 配置，理由见 §6。

D7–D13 依据用户 2026-11-12 原话：“hooks Q1 选 M，Q2–Q7 按推荐”。

用户选项记录：
- Q1：选 M（最小形状，只对应 AC1–AC4）；未选 A（新条目另带 `events`、`stop_args`、`stop_wait_sec`、Pre/Post 过滤，无对应验收且与 F33 冲突）、B（按事件分组，与现有键和 argv 语义不一致）、C（只列 argv，不能单独设超时）。
- Q2：选 A 顺序执行；未选 B 并发（Stop 先后需另编排）。
- Q3：选 A 先即时后等待；未选 B 全部并发（gofer 每回合先等待再被撤销）、C 全部顺序短路（等待条目在前时续跑要等它超时）。
- Q4：选 A 全部执行、reason 连接；未选 B 第一个 block 短路（后面条目收不到 Stop）。
- Q5：选 A 共存；未选 B 互斥（每加一条都要整体重写）。
- Q6：选 A 可写 + protected；未选 B 不可写只参与比较（IDEV-STD 只能提示编辑文件）。
- Q7：选 A 不纳入；未选 B 纳入（需另设信任机制）。

## 待确认事项

无。Q1–Q7 已由用户选定（2026-11-12：“hooks Q1 选 M，Q2–Q7 按推荐”），见 D7–D13。

## 结论与人工计划 Gate

本设计为 Draft 0.4：已处理第 2 轮两轴评审（规范轴 PASS；可执行轴 HF13 阻断已在 §4 修正）。之后进行第 3 轮评审，无开放 `CORE_BLOCKING` 并经你批准后才写计划；批准只授权进入计划，不授权实施。定稿后把配置形状回复 IDEV-STD 维护会话（my-tools-dev-08）。
