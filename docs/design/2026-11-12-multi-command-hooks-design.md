<!-- template_id: design; template_version: 1.2.0 -->
# suga chat hooks 多命令 设计

> 状态：Draft 0.2 / 待人工计划批准；issue `suga-5xmf`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-12 | Claude | 初稿：多个 hook 命令共存的配置形状、各事件执行与合并规则、Stop 长等待与 Interrupt；核心选择列为 Q1–Q7 待确认 |
| 0.2 | 2026-11-12 | Claude | 处理第 1 轮两轴评审（`tmp/review/suga-hooks/r1-standards.md`、`r1-spec.md`）：新校验只作用于 `hooks.commands` 条目，兼容条目不变（HF1）；Q1 增最小选项 M 并改为推荐，逐项对照验收，A 的追加规则单列，写明 A 与安全配置键设计 F33 的冲突与替代声明（HS1、HS2、HF8）；条目解析放 `internal/config`（HS3、HF7）；写定 Stop 合并结果字段、打断的冻结顺序、Interrupt 失败日志与不进 trace、打断来源全集（HF2、HF3、HF9、HS4）；锁定随 `suga-hyj7` 同版或其后发布（HS5）；补 gofer 影响与验证项、修正引用（HF4–HF6、HF10–HF12） |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

第 1 轮评审的处理：

| 评审项 | 处理 |
|---|---|
| HF1（阻断） | 全部新校验只作用于 `hooks.commands` 条目；`hooks.command` 及其配套键的接受范围与行为不变（含 `stop_background` 下只配 `stop_args` 的 7 天上限）；验证补“现有配置照常启动”用例（§2、§5、验证要求） |
| HS1、HS2、HF8 | Q1 增最小选项 M（条目只有 `command`+`timeout_ms`，不能等待、不收 Pre/PostToolUse），逐项列出各选项字段对应的验收；推荐改为 M；Q1-A 的追加机制单列（§7），并写明 A+Q6-A 会替代安全配置键设计 F33，附替代声明 |
| HS3、HF7 | 条目解析与校验放 `internal/config`（`ReadHookEntries`，沿用 Reader 分发），`internal/hooks` 消费；`internal/config` 不依赖 `internal/hooks` |
| HS4 | D1 去掉 GR108 误引，改为说明装配层只构造；Interrupt 失败沿用 `Call` 的 Warn 日志（GR109） |
| HS5 | 锁定随 `suga-hyj7` 同版或其后发布 |
| HF2 | §4 写定合并 Stop 结果的 `Decision`/`Reason`/`Err`/`Notice` 及各消费点含义 |
| HF3 | §4 写定打断顺序：冻结流水线 → 向正在运行的条目发 Interrupt → 取消；两条目之间不发 |
| HF4 | 选 M 时 Pre/PostToolUse 路径不变；选 A 时 `chat_turn.go` 的 PostToolUse 也改传原始名，接口与受影响测试列于 §7 |
| HF5 | 选 M 时不存在（无 `events`、无条目级 glob）；选 A 时定为“显式 `events` 不含该事件却配了 glob → 启动报错”（§7） |
| HF6 | 选 M 时无跨键约束；选 A 时 `hooks.*` 相关描述符挂同一 Reader（§7） |
| HF9 | Interrupt 不进 trace（同现状）；ptye2e 经假 hook 的落盘记录观察；打断来源按代码列全（§4） |
| HF10 | 补 gofer Stop 被推迟的职责与 catch-up 兜底（安全、数据、运维与回滚） |
| HF11 | 验证要求补 chat 层逐条 trace、阶段切换打断、跨键 set（A）、后台 Stop 被新 Stop 杀掉、合并 Err、`stopWait` 夹具 |
| HF12 | 修正前台 block 处理行号为 `chat.go:782-790`；程序名用 `filepath.Base`；条目错误按 `hooks.commands[i].<字段>` 报告，顺带修正现有 `pre_tool_use` glob 错误的键名 |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：`suga chat` 能同时运行多个 hook 命令（典型组合：gofer 的 `gofer hook generic --agent suga` 负责会话上报与 Stop 长等待，IDEV-STD 的 `idevstd script continuation_hook --agent suga` 负责续跑），新命令的 Stop block 能生效；只配 `hooks.command` 的现有用户行为与能否启动都不变。
- 验收（当前已证实的需要）：AC1 新命令与 gofer 的 `hooks.command` 共存；AC2 新命令 Stop 的 block 开启下一回合；AC3 只配 `hooks.command` 时行为不变、现有配置照常启动；AC4 新命令可单独设超时（IDEV-STD 需要 10 秒，`idev-standards-public/docs/design/2026-11-12-continuation-hooks-split-plans-1-3-0-design.md:92`）。
- scope freeze：`internal/hooks` 的多命令执行与合并、`internal/config` 的条目解析与注册表、chat 对 hook 结果的消费点（Stop 等待、后台 Stop、trace 记录）、文档。不改事件 JSON 与 stdout 回答格式；不做项目级 hook；不做 shell 解析；不给 ACP、`suga run`、SDK 增加 hooks。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget：最多 8 轮（`planning-core-rigorous`），本版为第 1 轮之后的修订。
- 暴露度（SR1409）：高（已发布 `v0.55.0`；涉及持久命令执行、执行前拒绝门、gofer 的 Stop 等待链路）。Delivery Track：Full。两轴独立评审与 ledger，评审目录 `docs/review/2026-11-12-multi-command-hooks/`（第 1 轮报告暂存 `tmp/review/suga-hooks/`）。
- 停止条件：无开放的 `CORE_BLOCKING`，且 Q1–Q7 已由你选定。

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
- **等待型条目**：Stop 上限长于其普通超时的条目。兼容条目在设了 `stop_wait_sec`，或 `stop_background` 下未设 `stop_wait_sec`（7 天上限）时为等待型（`hooks.go:247-255`）。选 Q1-M 时只有兼容条目可能是等待型。
- **即时条目**：其余条目；Stop 在其普通超时内结束。
- **合并结果**：一个事件所有条目回答合成的单个 `Response`/`Result`，chat 只消费它。

## 范围与非目标

范围：新配置键 `hooks.commands`（Q1）及与 `hooks.command` 的共存（Q5）；SessionStart、UserPromptSubmit、Stop、SessionEnd、Interrupt 的多条目执行与合并（Q2–Q4）；Stop 前台等待、打断、`stop_background` 的多条目语义；注册表描述符与 protected、`suga config set/get`；trace；文档（README、`docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/config.md`）。

非目标：项目级 hook（Q7）；`inspect env` 展示条目（deferred，SR1405）；条目级 `stop_background`；事件 JSON、回答格式与 exit 2 语义的变化；ACP、`suga run`、SDK（含 xhen）的 hooks（`pkg/sugasdk/doc.go:94`）。选 Q1-M 时另不做：条目级事件订阅、条目级 Stop 等待、新条目收 PreToolUse/PostToolUse（均 deferred，等出现第二个已证实用例再议，SR1405.1）。

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

以下按推荐的 Q1-M 写；选 Q1-A 时的追加规则见 §7，其余选项的差异写在“待确认事项”。

### 1 配置形状（Q1 推荐 M）

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

### 2 兼容条目与共存（Q5 推荐 A）

- `hooks.command` 存在时构成兼容条目 0，解析、校验、超时（含 `stop_background` 下的 7 天上限）、订阅的事件与现状完全相同；配套键只属于它（`hooks.timeout_ms` 另作新条目的默认超时）。
- **新校验只作用于新条目**（HF1）：程序名非空、`timeout_ms` 为正整数、条目只含已知字段（未知字段报错，防止误以为可配 `stop_args` 等）。兼容条目不增加任何检查，现有配置不会因升级而启动失败。
- 两个键可同时存在，这正是 IDEV-STD 安装的场景：只写 `hooks.commands`，不碰已有配置。两者都未设或为空时 hooks 不启用。

### 3 各事件的执行与合并（Q2 推荐 A：顺序执行）

订阅某事件的条目按编号顺序逐个执行，每条用自己的超时，不共享总预算。

| 事件 | 执行 | 单条失败或超时 | 合并 |
|---|---|---|---|
| SessionStart / UserPromptSubmit | 顺序，全部执行 | 记 Warn（沿用 `Call`），跳过该条 | `additionalContext` 按顺序以空行连接；notice 按顺序换行连接 |
| SessionEnd | 顺序，全部执行 | 同上 | 忽略回答 |
| PreToolUse / PostToolUse | 只有兼容条目，路径与现状相同 | 同现状 | 同现状 |
| Stop | 两阶段（§4） | 记日志，视为无决定 | §4 |
| Interrupt | 只发给 Stop 被打断时正在运行的条目 | 记 Warn（沿用 `Call`，GR109），不影响取消 | 无 |

### 4 Stop：两阶段、合并结果与打断（Q3 推荐 A，Q4 推荐 A）

**执行**：
1. 阶段一：即时条目按编号顺序执行（兼容条目为即时型时排在最前），各自超时；全部执行，不因前一个 block 短路（Q4）。
2. 阶段一有任一 block（`Err==nil`、`Decision=="block"`、reason 非空）→ 不启动等待型条目，返回合并结果。
3. 否则阶段二：启动等待型条目（只可能是兼容条目，附加 `stop_args`）；没有等待型条目则立即结束。
4. `stop_hook_active` 仍是全局的：上一次合并结果为 block（前台回复或后台通知）时为真，对所有条目相同。

**合并结果**（HF2；前台、后台、交回路径、trace 用同一规则）：

| 字段 | 规则 |
|---|---|
| `Decision`/`Reason` | 阶段一有 block：`block`，reason 为各 block 的 reason 按顺序以空行连接；否则取阶段二等待条目的回答；无等待条目时为空 |
| `Err` | 阶段一有 block：`nil`（阶段一其他条的失败只记日志与逐条 trace）；否则取等待条目的错误（与现状相同）；无等待条目时 `nil` |
| `Notice` | 已执行条目的 notice 按执行顺序换行连接 |

由此：前台（`chat.go:782-790`）在阶段一 block 加另一条失败时仍开启下一回合；后台任务只在“无 block 且等待条目失败”时以错误结束（与现状一致）。

**打断**（HF3；来源见“已确认事实”，全部经 `Wait.Interrupt`）：
1. 先冻结流水线：之后不再启动任何条目（与“启动下一条”在同一把锁内判定，避免阶段切换竞态）；
2. 若此刻有条目在运行，向它发 Interrupt（1 秒超时，失败记 Warn）；两条目之间（无条目在运行）不发；
3. 再取消正在运行的条目；
4. 返回值：按上表规则从已完成条目得出的 block reason（含阶段一已得的 reason、等待条目已到但未消费的回复），交给现有 `lostReply` 路径（`chat_turn.go:244-247`）。

**后台 Stop**（`stop_background: true`）：整个流水线作为一个 `hook` 任务运行，顺序、短路与合并规则相同；不被输入打断，不发 Interrupt；新 Stop 杀掉旧任务时直接取消（同现状，不发 Interrupt）；合并后的 block 转为一条任务通知。

### 5 注册表、`suga config` 与 protected（Q6 推荐 A）

- 新描述符 `hooks.commands`：`ConfigTypeYAML`、`Writable:true`、`Protected:true`、`Reader:"hooks"`。`config set` 写入时经 `ReadHookEntries` 做与启动相同的条目校验，非法值退出码 2、文件不变；`suga config get hooks.commands` 输出 YAML；整体替换写入，不提供追加子命令（SR1403）。
- 选 M 时没有跨键约束（新条目的有效性不依赖其他 `hooks.*` 键），兼容键维持无 Reader。
- 归入安全配置键设计显式分类表的“执行类”；启动比较按整块值求摘要，条目内 `timeout_ms` 的变化也会被报出（可接受：属于执行配置）。选 M 时新条目不含 PreToolUse 过滤器，与 F33 无冲突。
- **发布顺序锁定**（HS5）：本特性随 `suga-hyj7` 同版或其后发布，`hooks.commands` 自首个版本起即为 protected；不单独先发。

### 6 不纳入项目级 hook 配置（Q7 推荐 A）

suga 只有一层配置（`SUGA_HOME/config.yaml`），没有读取仓库内配置的机制；项目级 hook 会让仓库内容（含克隆的第三方仓库、模型写入的文件）引入在每个事件上自动执行的程序，绕开 protected 键与启动提示两道防线。续跑命令是你个人工具链的一部分，放全局配置即可满足需求；需要按仓库区分时，命令自身可读事件的 `cwd` 判断。

### 7 选 Q1-A 时的追加规则（推荐 M 时不做）

Q1-A 让新条目另带 `events`、`stop_args`、`stop_wait_sec`、`pre_tool_use.tools/.name`、`post_tool_use.tools/.name`。这些字段目前没有对应验收（AC1–AC4），选 A 需同时接受：
- **事件订阅与 glob**（HF5）：`events` 省略时订阅全部普通事件；PreToolUse/PostToolUse 需配对应 glob 才收；`events` 显式给出且不含 PreToolUse/PostToolUse 却配了对应 glob → 启动报错；`events` 列出它们却无 glob → 启动报错。
- **PreToolUse 多条目**：顺序执行，任一失败或 deny 即拒绝并停止（fail closed）；前一条的 `updatedInput` 作为后一条的 `tool_input`，最终参数再走审批；逐条 trace（`hooks_gate.go` 现只记一条）。
- **接口变化**（HF4）：`PreToolName`/`PostToolName` 改为只判定“是否有条目订阅”，事件中的工具名由各条目过滤器决定；`hooks_gate.go:43-58` 与 `chat_turn.go:291-297` 改传原始工具名。受影响测试：`chat_hooks_test.go`、`hooks_gate_test.go`、`hooks_record_test.go`、`stop_background_test.go`、`chat_hooks_config_test.go`、`followup_test.go`。
- **等待型新条目**：至多一个等待型条目（含兼容条目）；`stop_args` 需配 `stop_wait_sec`（只校验新条目）。
- **跨键校验**（HF6）：是否已有等待型条目取决于 `hooks.command`、`hooks.stop_wait_sec`、`hooks.stop_background`，这些描述符与 `hooks.timeout_ms` 一并挂 `Reader:"hooks"`，使 `config set` 任一相关键时都跑完整校验。
- **与安全配置键设计的冲突**（HS2、HF8）：选 A 且 Q6-A 时，PreToolUse 过滤器经 `hooks.commands` 获得人在终端可写的 CLI 入口。**替代声明**：替代 `docs/design/2026-11-11-protected-config-keys-design.md` 0.8 F33 中“`hooks.pre_tool_use.*` 不新增 CLI 能力”的取舍——`hooks.pre_tool_use.*` 本身仍不可写，但新条目的 `pre_tool_use` 字段可经 `suga config set hooks.commands` 由人在终端写入；`suga-hyj7` 计划的分类表与该设计“兼容与误拒”一节需同步注明。若不接受此替代，可让 Reader 拒绝含 `pre_tool_use` 的条目，或选 Q6-B。

## 架构

代码组织沿用 suga 现有分层（SR2004；参照本仓库 `internal/config` 的 `ReadMCPServerSettings` 与 Reader 分发、`internal/hooks`、`internal/chat`）：
- `internal/config` 新文件 `hooks_entries.go`：`ReadHookEntries() ([]HookEntry, error)` 读 `hooks.commands`、校验并返回解析后的新条目（argv、超时），错误指明 `hooks.commands[i].<字段>`；`dispatchConfigReader` 增 `case "hooks"`。基础包 `internal/config` 不依赖功能包 `internal/hooks`。
- `internal/hooks`：
  - `hooks.New` 保留兼容条目的现有解析，再调用 `config.ReadHookEntries` 追加新条目（新增依赖方向 `internal/hooks → internal/config`，无环）；顺带把 `pre_tool_use.tools` 的 glob 错误按实际键名报告（只改错误文案）。
  - 新文件 `pipeline.go`：多条目 `Client`——顺序执行与合并、两阶段 Stop、冻结与打断；`Result` 增加逐条运行记录（编号、程序名、耗时、该条回答与错误）。
  - 现有 `commandClient.call` 与 `stopWait` 保留为单条目执行原语，现有直接构造 `stopWait` 的测试不改。
- `internal/chat`：`hooks_record.go` 按逐条运行记录逐条写 trace；`chat.go`、`chat_turn.go`、`stop_background.go`、`hooks_gate.go` 只消费合并结果，选 M 时逻辑不变。
- 装配层（`internal/cli/chat.go`、`internal/wiring/localenv.go`）只构造 client，不含合并逻辑；`localenv.go` 不改。
- 不改 `pkg/*`。

## 关键流程

```text
回合结束 → Stop 合并调用
  阶段一：即时条目按编号顺序执行，各自超时（全部执行）
     ├─ 有 block → Decision=block，reason 空行连接，Err=nil，跳过阶段二
     └─ 无 block → 阶段二：等待型条目（兼容条目，若是）带 stop_args 运行
                      └─ Decision/Reason/Err 取它的结果
  前台打断：冻结（不再启动条目）→ 正在运行的条目收 Interrupt → 取消
           → 已得 block reason 交回 lostReply
  后台：整个流水线为一个 hook 任务；block → 任务通知；新 Stop 直接取消旧任务

SessionStart / UserPromptSubmit / SessionEnd
  订阅条目按编号顺序执行 → 失败记 Warn 跳过 → additionalContext、notice 按顺序连接
```

## 安全、数据、运维与回滚

- **SR1301**：鉴权——`hooks.commands` 为 protected，且随 `suga-hyj7` 同版或其后发布，模型在会话内不能经 `config set` 写入；注入防护——argv 直接执行，不经 shell；秘密——trace 只记 `filepath.Base(argv[0])` 与条目编号，不记参数；入参校验——启动与 `config set` 同一 `ReadHookEntries` 校验；资源归属——不适用。
- **执行门不变弱**：选 M 时 PreToolUse 路径不变；选 A 时任一条目失败即拒绝。
- **对 gofer 的影响**（HF10）：只配 gofer 时无变化。加了续跑条目且它要求继续时，该回合 gofer 不收 Stop，因此以下各项推迟到续跑结束后的那次 Stop：打开等待 web 回复的 turn、上报本回合最后回复与空闲时长、清进度、接管通知、信件与 watched job 的 Stop 送达；中间回合的最后回复不会出现在 web，web 侧会话保持“运行中”（IDEV-STD 以状态不前进与最多 8 次为界）。job 通知经下一次 UserPromptSubmit（带 `injected=true`）的 catch-up 补投，不丢。阶段一被打断时 gofer 该回合既无 Stop 也无 Interrupt，其 turn 由下一次 UserPromptSubmit 结算。
- **对 IDEV-STD 的影响**：安装命令改为 `suga config set hooks.commands '<YAML>'`；条目自带超时；已有 `hooks.commands` 时整体替换写入，需提示你合并。
- **trace**：每个条目每次运行记一条 `kind=hook` 事件，attrs 新增 `entry`、`program`；只有兼容条目时同样带这两个字段，其余字段不变。Interrupt 不进 trace（同现状）；打断时未启动的条目不记。
- **运维**：顺序执行使事件延迟为各条目耗时之和；SessionEnd 在退出时最多等待各条目超时之和；文档建议条目控制在少数几条。
- **数据**：无存储结构变化。
- **回滚**：revert；删掉 `hooks.commands` 即回到单命令行为。旧版本运行时不读取 `hooks.commands`，只剩兼容条目生效（计划阶段核对旧版本 `config validate` 对该键的提示）。
- **验证要求**（计划中落实）：
  - 兼容（HF1）：只配 `hooks.command` 的现有配置照常启动，含 `stop_background: true` + `stop_args` 无 `stop_wait_sec`、只配 `stop_args`；现有 `hooks_test.go`、`hooks_pretool_test.go` 断言不改。
  - 单元（`internal/config`）：`ReadHookEntries` 全部校验错误与字段路径；未知字段报错。
  - 单元（`internal/hooks`，假 hook 进程）：普通事件顺序、各自超时、单条失败不影响其他条、连接规则；Stop 阶段一 block 跳过等待条目、多 block 合并、阶段一 block 加另一条失败时 `Err=nil` 且前台续跑（HF2）、无 block 时等待条目运行且 `Err` 取其错误；打断：等待条目收 Interrupt、未启动条目不收、阶段一结束瞬间打断不启动等待条目且无条目收 Interrupt（HF3）、已得 reason 交回；Interrupt 失败记 Warn。
  - 后台 Stop：block 变为任务通知、输入不打断、阶段一运行中被新 Stop 杀掉时不启动阶段二。
  - GR110：经 `internal/cli` 真实命令分发——`suga config set hooks.commands` 合法值写入、非法值退出码 2 且文件不变、`config get` 输出；protected 拒绝随 `suga-hyj7` 一起验证。选 A 时另测：已有等待型新条目时 `config set hooks.stop_wait_sec` 被拒（HF6）。
  - chat 层：逐条 trace（`entry`/`program`）；选 A 时另测 PreToolUse 多条目逐条 trace 与审批看到最终改写参数。
  - ptye2e（`test/ptye2e`，参照 `chat_observe_test.go` 的 `TestHookHelper` 假 hook）：配兼容条目（模拟等待型 gofer）与一条新条目（模拟续跑，第一次 block、之后不输出）；假 hook 把收到的事件名与自身标签追加到测试临时目录的记录文件。断言：续跑回合开始且该回合等待条目未收 Stop；下一次 Stop 等待条目运行；等待期间输入后记录文件中 Interrupt 只出现在等待条目；`suga inspect trace --json` 中每条 `hook` 事件的 `entry`/`program`/decision 正确。反向验证：临时去掉阶段一短路、临时把 Interrupt 发给全部条目，确认对应检查失败（确认失败来自断言而非编译错误）。
  - Windows：经 gofer exec job 在主机跑 `go test ./internal/config/... ./internal/hooks/... ./internal/chat/...` 与 ptye2e ConPTY 场景（含被打断条目的进程终止、`filepath.Base` 对反斜杠路径）。

## 决策

- **D1** 多条目执行与合并全部在 `internal/hooks` 内完成，chat 只消费合并结果与逐条运行记录；装配层只构造 client。
- **D2** 只配 `hooks.command` 时行为、超时、接受范围与 trace 字段（除新增 attrs）与现状一致；新校验只作用于 `hooks.commands` 条目；现有测试断言不改。
- **D3** 不引入 exit 2 语义、不改事件与回答格式。
- **D4** 条目解析与校验放 `internal/config`（`ReadHookEntries`），`internal/hooks` 消费。
- **D5** Stop 合并结果字段与打断顺序按 §4；Interrupt 失败记 Warn，不进 trace。
- **D6** 随 `suga-hyj7` 同版或其后发布。
- 以下 Q1–Q7 待你选定后转为决策。

## 待确认事项

**Q1 配置形状**（各选项字段对应的验收：AC1 共存、AC2 Stop block 生效、AC3 兼容不变、AC4 单独超时）
- M（推荐）新条目只有 `command` + `timeout_ms`；收全部普通事件，不收 PreToolUse/PostToolUse，不能等待；等待与 Pre/Post 过滤只来自兼容条目。对应：`command`→AC1、AC2；`timeout_ms`→AC4；兼容条目原样→AC3；没有无验收的字段。理由：SR1403/SR1410 下最小充分；续跑命令自己过滤事件，唯一额外需要是 10 秒超时；与 F33 无冲突，HF4–HF6 与 Q3-B 的前提随之消失。代价：以后要第二个等待来源或第二个 PreToolUse 门时需再扩展（字段可向 A 兼容追加）。
- A 新条目另带 `events`、`stop_args`、`stop_wait_sec`、`pre_tool_use`、`post_tool_use`（追加规则见 §7）。对应：同 M 的 AC1–AC4；其余字段目前无对应验收。代价：条目订阅、PreToolUse 改写链、等待型选择、跨键校验都要实现与测试；与 Q6-A 同选时替代安全配置键设计 F33（见 §7 替代声明）。
- B Claude/Codex 的按事件分组 `hooks.<Event>[].hooks[].command`。对应：AC1、AC2、AC4 可满足；AC3 需另写兼容映射。代价：gofer 命令要在各事件下重复书写；事件名键与 `hooks.timeout_ms` 等现有键混在同一映射；`stop_args/stop_wait_sec` 无对应位置；Claude 的 `command` 是经 shell 的字符串，与 suga 的 argv 不一致；PreToolUse 由 `matcher` 选工具，同样与 F33 冲突。
- C `hooks.commands` 只是 argv 列表，共享全局超时。对应：AC1、AC2、AC3；AC4 只能靠调大全局 `hooks.timeout_ms`（同时放宽 gofer 各事件的失败判定）。

**Q2 同一事件多条目的执行方式**
- A（推荐）按编号顺序逐个执行，各自超时。理由：结果确定，续跑与等待的先后可控；条目少时延迟可接受。选 Q1-A 时还使 PreToolUse 改写链有唯一结果。
- B 并发执行（Claude 的做法），合并全部结果。延迟最低；Stop 的“先续跑后等待”要另加编排；选 Q1-A 时 PreToolUse 多条改写冲突需另定规则。

**Q3 Stop 长等待与多条目**
- A（推荐）先即时、后等待：即时条目顺序执行，有 block 就不启动等待条目；打断按 §4 冻结后只给正在运行的条目发 Interrupt。理由：续跑优先于等待人回复，避免 gofer 在 agent 仍要继续时向 web 报“等你回复”。
- B 全部条目并发，谁先 block 用谁，其余收 Interrupt 后取消。gofer 每回合都会先进入等待再被撤销，web 侧状态来回跳；“多个等待来源”只在 Q1-A 下才存在。
- C 全部按编号顺序执行，block 即短路，不区分等待型。规则最简单，但兼容条目为等待型时排在最前，续跑条目要等它超时（可长达 2 小时）才运行。

**Q4 多个 Stop block 的合并**
- A（推荐）阶段一全部执行，reason 按顺序以空行连接为一次输入。理由：每个即时条目都收到 Stop，不丢任何一方的继续理由。
- B 第一个 block 短路，后面的即时条目不调用。少一次调用，但后面的条目这回合收不到 Stop。

**Q5 `hooks.command` 与 `hooks.commands` 同时出现**
- A（推荐）共存：`hooks.command` 为条目 0，配套键只属于它，`hooks.commands` 依次追加。理由：IDEV-STD 等外部安装只写新键，不改已有配置；旧配置零迁移。选 Q1-M 时只能选 A（等待与 Pre/Post 过滤只在兼容条目）。
- B 互斥：同时出现时启动报错，要求迁移到 `hooks.commands`。只在 Q1-A 下可行；每次加一条都要整体重写，含 gofer 那条。

**Q6 `hooks.commands` 是否可经 `suga config set` 写入**
- A（推荐）可写 + protected（人在终端可写，会话内被拒），带 `Reader:"hooks"` 校验。理由：IDEV-STD 安装按惯例输出 `config set` 命令；protected 且锁定发布顺序保证模型不能写。注意：与 Q1-A 同选时替代安全配置键设计 F33（PreToolUse 过滤器获得 CLI 写入口，见 §7）；与 Q1-M 同选时无此冲突。
- B 不可写、只参与启动比较（同 F33 对 `hooks.pre_tool_use.*` 的处理），只能直接编辑 `config.yaml`。少一个 CLI 写入口，但 IDEV-STD 需改为提示编辑文件。

**Q7 项目级 hook 配置**
- A（推荐）不纳入，理由见 §6。
- B 纳入（仓库内文件声明条目）。需要另设信任机制（如首次运行确认、按仓库指纹记忆），超出本次范围，若选择需单独设计。

## 结论与人工计划 Gate

本设计为 Draft 0.2，已处理第 1 轮两轴评审；Q1–Q7 待你选定。之后进行第 2 轮 confirmation 评审，无开放 `CORE_BLOCKING` 并经你批准后才写计划；批准只授权进入计划，不授权实施。定稿后把配置形状回复 IDEV-STD 维护会话（my-tools-dev-08）。
