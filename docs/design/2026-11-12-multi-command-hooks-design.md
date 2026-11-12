<!-- template_id: design; template_version: 1.2.0 -->
# suga chat hooks 多命令 设计

> 状态：Draft 0.1 / 待人工计划批准；issue `suga-5xmf`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-12 | Claude | 初稿：多个 hook 命令共存的配置形状、各事件执行与合并规则、Stop 长等待与 Interrupt；核心选择列为 Q1–Q7 待确认 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：`suga chat` 能同时运行多个 hook 命令（典型组合：gofer 的 `gofer hook generic --agent suga` 负责会话上报与 Stop 长等待，IDEV-STD 的 `idevstd script continuation_hook --agent suga` 负责续跑），每个事件的执行顺序、超时、失败影响与决策合并都有确定规则；只配 `hooks.command` 的现有用户行为不变。
- scope freeze：`internal/hooks` 的多命令执行与合并、配置键与注册表、chat 对 hook 结果的消费点（Stop 等待、后台 Stop、PreToolUse 门、trace 记录）、文档。不改事件 JSON 形状与 stdout 回答格式；不做项目级 hook 配置；不做 shell 解析；不给 ACP、`suga run`、SDK 增加 hooks。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget：最多 8 轮（`planning-core-rigorous`）。
- 暴露度（SR1409）：高（已发布 `v0.55.0`；涉及持久命令执行、执行前拒绝门、gofer 的 Stop 等待链路）。Delivery Track：Full（新增配置 schema、改变 hook 执行合同）。两轴独立评审与 ledger，评审目录 `docs/review/2026-11-12-multi-command-hooks/`。
- 停止条件：无开放的 `CORE_BLOCKING`，且 Q1–Q7 已由你选定。

## 背景与目标

- 现状：`hooks.command` 是唯一的全局命令，每个事件运行一次，事件 JSON 一行写入 stdin（`internal/hooks/hooks.go:1-10`、`:208-265`）。配套键 `hooks.stop_args`、`hooks.stop_wait_sec`、`hooks.stop_background`、`hooks.timeout_ms`、`hooks.pre_tool_use.*`、`hooks.post_tool_use.*` 都只服务这一条命令。
- gofer 用这条命令做会话上报，并用 Stop `--wait <秒>` 长时间等待 web 回复（`docs/usage/gofer-personal-agent.md:390-402`）；等待期间你在终端输入时，suga 发 Interrupt 并结束等待（`hooks.go:407-422`）。
- IDEV-STD 1.3.0 新增续跑 Stop hook `idevstd script continuation_hook --agent suga`：只处理 `hook_event_name=="Stop"`，需要继续时退出码 0 并输出 `{"decision":"block","reason":…}`，否则无输出；不需要 exit 2 语义。其设计 Q2 记录“suga 只有一条全局命令，已被其他工具占用时无法并存”，安装命令待本设计定稿后按新配置形状输出（`idev-standards-public/docs/design/2026-11-12-continuation-hooks-split-plans-1-3-0-design.md:41`、`:92`、`:143`）。
- 用户 2026-11-12 原话：“hooks 并行出设计”（与 `suga-hyj7` 安全配置键计划并行）。

用户可见结果：
- 你在 `config.yaml` 里保留原有的 gofer `hooks.command` 配置，再加一条 IDEV-STD 续跑命令，两者同时生效：回合结束时先问续跑命令，它要求继续就直接开始下一回合；不继续时 gofer 照常等待 web 回复。
- 只配 `hooks.command` 时，事件、超时、等待、Interrupt、trace 与现在完全一致。
- 配置写错（如两条命令都要长等待）时 `suga chat` 启动报错并指出第几条。

## 名词

- **hook 条目（entry）**：一条 hook 命令及其专属设置（argv、订阅的事件、超时、Stop 参数、工具过滤）。
- **兼容条目**：由 `hooks.command` 及其旧配套键组成的那一条，编号 0。
- **等待型条目**：Stop 的超时长于其普通超时的条目（设了 `stop_wait_sec`，或兼容条目在 `stop_background` 下无 `stop_wait_sec` 时的 7 天上限，见 `hooks.go:247-255`）。gofer 的 `--wait` 条目即为此类。
- **即时条目**：其余条目；Stop 在其 `timeout_ms` 内结束。IDEV-STD 续跑条目属于此类。
- **合并结果**：一个事件所有条目回答合成的单个 `Response`，chat 只消费它。

## 范围与非目标

范围：
- 新配置键（Q1）、与 `hooks.command` 的共存规则（Q5）；
- 每个事件的多条目执行方式与合并规则（Q2–Q4）；
- Stop 前台等待、Interrupt、`stop_background` 在多条目下的语义；
- 注册表描述符、protected 标记、`suga config set/get`、`inspect env` 展示；
- trace 记录；文档（README、`docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/config.md`）。

非目标：
- 项目级（仓库内）hook 配置（Q7）；
- 事件并发执行之外的调度（如按条目设置后台）、条目级 `stop_background`（deferred）；
- 改变事件 JSON 字段、stdout 回答格式、exit 2 语义（不引入）；
- ACP、`suga run`、SDK（含 xhen）的 hooks：现在没有，本设计也不加（`pkg/sugasdk/doc.go:94`）。

受影响项目与接口：
- suga：新增配置键（只增不改）；trace `hook` 事件 attrs 新增字段（只增）；`internal/hooks` 内部接口调整（`Client`、`ToolGate` 只在本仓库内使用）。
- gofer：现有配置无需改动；行为变化只在你另加了 IDEV-STD 等条目时出现（见“对其他项目的影响”）。gofer 仓库内未见写入 suga hooks 配置的代码。
- IDEV-STD：`idevstd hooks install --agent suga` 按 Q1 选定的形状输出 `suga config set` 命令，并去掉“冲突则不给命令”的分支。
- xhen：无（SDK 不提供 chat hooks）。

## 已确认事实与规范

- **调用点**：`hooks.New` 只在 `internal/cli/chat.go:21`（chat）与 `internal/wiring/localenv.go:193`（`inspect env` 展示）被调用；`internal/hooks` 只被 `internal/chat`、`internal/cli`、`internal/wiring` 导入。
- **事件全集（以代码为准）**：SessionStart（`internal/chat/chat.go:492`）、UserPromptSubmit（`chat_turn.go:235`）、PreToolUse（`hooks_gate.go:51`）、PostToolUse（`chat_turn.go:295`）、Stop（`chat_turn.go:395`）、SessionEnd（`chat.go:539`）、Interrupt（`hooks.go:407-414`，只在前台 Stop 等待被打断时发）。
- **各事件对回答的使用**：SessionStart、UserPromptSubmit 取 `additionalContext` 进下一次请求并显示 stderr notice（`chat.go:494-496`、`chat_turn.go:240-242`）；PostToolUse 只显示 notice（`chat_turn.go:302`）；SessionEnd、Interrupt 回答被忽略；PreToolUse 的 deny 拒绝调用、`updatedInput` 改写参数、失败一律拒绝（fail closed，`hooks_gate.go:43-80`）；Stop 的 block+reason 开启新回合（前台等待 `chat.go:555-563`，后台任务转为通知 `stop_background.go:62-82`）。
- **失败处理**：非 PreToolUse 事件失败只记日志（`hooks.go:302-308`）；Stop 超时与取消不记为错误（`hooks.go:316-326`、`hooks_record.go:88-96`）。
- **Interrupt 合同**：`Wait.Interrupt` 先发 Interrupt（1 秒超时），再取消等待，并交回已收到但未消费的 block reason（`hooks.go:407-422`、`hooks_record.go:112-128`）。
- **后台 Stop**：整个 Stop 调用作为一个 `hook` 类任务运行，新 Stop 杀掉旧的，block 转为任务通知（`stop_background.go:16-86`）。
- **trace**：每次调用记一条 `kind=hook` 事件，attrs 含 decision、reason/附加上下文长度、notice、失败（`hooks_record.go:64-100`）；PreToolUse 另带 `rewritten_args`（`hooks_gate.go:57-58`）。
- **注册表**：`hooks.*` 现有 7 个描述符（`internal/config/settings_registry.go:273-279`），无 `hooks.pre_tool_use.*`；`ConfigTypeYAML` 已用于可写的 `mcp.servers`（`:192`，带 `Reader` 校验）。
- **安全配置键设计**（`docs/design/2026-11-11-protected-config-keys-design.md`，Draft 0.8 已批准进入计划）：执行类 protected 含 `hooks.command`、`hooks.stop_args`、`hooks.pre_tool_use.*`、`hooks.post_tool_use.*`；`hooks.timeout_ms`、`stop_wait_sec`、`stop_background` 不保护；启动比较覆盖全部标 `Protected` 的注册表键；显式分类表测试要求每个注册表键归类。
- **规范**：SR1301、SR1403、SR1405、GR108、GR109、GR110、SR2004；验证要求见 `AGENTS.md`“验证”节（ptye2e、`suga inspect trace --json`、反向验证、Windows）。

## 总体方案

### 1 配置形状（Q1 推荐 A）

新键 `hooks.commands`：条目列表，每个条目自带设置。

```yaml
hooks:
  # 兼容条目（编号 0），旧配置原样有效
  command: [gofer, hook, generic, --agent, suga]
  stop_args: [--wait, "7140"]
  stop_wait_sec: 7140
  post_tool_use: {tools: ["*shell*", "*exec*"], name: shell}
  # 新增条目（编号 1…）
  commands:
    - command: [idevstd, script, continuation_hook, --agent, suga]
      events: [Stop]
      timeout_ms: 10000
```

条目字段：

| 字段 | 默认 | 说明 |
|---|---|---|
| `command` | 必填 | argv 列表，直接执行（不经 shell），程序名非空 |
| `events` | 全部普通事件 | 订阅的事件名，取值限于上节事件全集；未知名启动报错 |
| `timeout_ms` | `hooks.timeout_ms` | 本条目普通事件超时 |
| `stop_args` | `[]` | 只追加到本条目的 Stop；与 `stop_wait_sec` 同时配 |
| `stop_wait_sec` | 0 | >0 时本条目为等待型，Stop 上限 `stop_wait_sec+2` 秒 |
| `pre_tool_use.tools` / `.name` | 空 | 本条目的 PreToolUse 工具 glob 与改名；空则不收 PreToolUse |
| `post_tool_use.tools` / `.name` | 空 | 同上，PostToolUse |

- `events` 省略时订阅 SessionStart、UserPromptSubmit、Stop、SessionEnd、Interrupt；PreToolUse/PostToolUse 仍只由工具 glob 决定（与现状一致），列在 `events` 里但没有 glob 时启动报错，避免“以为订阅了却收不到”。
- Interrupt 只发给 Stop 被打断的那个条目（见 §3），条目的 `events` 不含 Interrupt 时不发。
- `hooks.stop_background` 保持全局开关；`SUGA_HOOKS=off` 关闭全部条目。

### 2 兼容条目与共存（Q5 推荐 A）

- `hooks.command` 存在时构成兼容条目 0，旧配套键（`stop_args`、`stop_wait_sec`、`pre_tool_use.*`、`post_tool_use.*`、`timeout_ms`）只属于它；`hooks.commands` 的条目依次编号 1…，不继承这些键（`timeout_ms` 例外：作为条目默认超时）。兼容条目订阅全部事件，与现在一致，Stop 上限计算原样保留（含 `stop_background` 下的 7 天上限）。
- 两个键可同时存在：这正是 IDEV-STD 安装的场景——只写 `hooks.commands`，不碰你已有的 gofer 配置。
- 两者都未设或为空时 hooks 不启用（同现状）。

### 3 各事件的执行与合并（Q2 推荐 A：顺序执行）

所有事件在订阅它的条目间**按编号顺序逐个执行**，每个条目用自己的超时（不共享总预算）。

| 事件 | 执行 | 单条失败或超时 | 合并 |
|---|---|---|---|
| SessionStart / UserPromptSubmit | 顺序，全部执行 | 记 Warn，跳过该条，其余照常 | `additionalContext` 按顺序以空行连接；notice 逐条显示 |
| PostToolUse | 顺序，各条按自己的 glob 与改名构造事件 | 同上 | notice 逐条显示 |
| SessionEnd | 顺序，全部执行 | 同上 | 忽略回答 |
| PreToolUse | 顺序，按自己的 glob 选择；前一条的 `updatedInput` 作为后一条的 `tool_input` | **整体拒绝**（fail closed），后续条目不再调用 | 任一 deny 即拒绝并停止（reason 取该条）；改写链式叠加，最终参数再走审批 |
| Stop | 两阶段，见下 | 记日志，视为无决定（同现状） | 见 Q3、Q4 |
| Interrupt | 只发给 Stop 被打断的条目 | 忽略 | 无 |

**Stop（Q3 推荐 A：先即时、后等待，block 短路）**：
1. 阶段一：即时条目按顺序执行，各自 `timeout_ms`。
2. 阶段一有任一 block（带非空 reason）→ 合并结果为 block（多个 reason 合并见 Q4），**不启动**等待型条目；chat 照常开启下一回合，下一次 Stop 带 `stop_hook_active=true`。
3. 阶段一无 block → 阶段二启动唯一的等待型条目（附加其 `stop_args`），其结果即 Stop 结果；没有等待型条目则 Stop 立即以“无决定”结束。
4. 等待型条目最多一个，多于一个时启动报错（Q3 选项 B/C 可放开）。
5. `stop_hook_active` 仍是全局的：上一次合并结果为 block 时为真，对所有条目相同。

**打断**（前台等待时你输入、`suga send` 投递、回合内插入消息、chat 退出）：
- 正在运行的那个条目（阶段一的某条或阶段二的等待条目）收到 Interrupt（1 秒超时）后被取消；尚未启动的条目不再运行，也不收 Interrupt。
- 已完成条目中已得到的 block reason 作为“未消费回复”交回（沿用现有 `lostReply` 路径，`chat_turn.go:244-247`），不丢失。

**后台 Stop**（`hooks.stop_background: true`）：整个两阶段流水线作为一个 `hook` 任务运行，顺序与短路规则相同；输入不打断，不发 Interrupt；新 Stop 杀掉旧任务（同现状）；合并后的 block 转为一条任务通知。

### 4 多个 block 的合并（Q4 推荐 A）

阶段一全部即时条目都执行（不因第一个 block 短路，保证每个条目都收到 Stop），多个 block 的 reason 按条目顺序以空行连接成一次输入，trace 中每条单独记录。

### 5 注册表、`suga config` 与 protected（Q6 推荐 A）

- 新描述符 `hooks.commands`：`ConfigTypeYAML`、`Writable:true`、`Protected:true`，带 `Reader` 做与启动相同的校验（`config set` 写入非法条目时拒绝，退出码 2）；`suga config get hooks.commands` 输出 YAML。整体替换写入，不提供追加子命令（SR1403）。
- 归入安全配置键设计显式分类表的“执行类”；启动比较按该键整块值的规范形式求摘要，条目内的超时变化也会被报出（可接受：属于执行配置的一部分）。
- 依赖顺序：若本特性先于 `suga-hyj7` 落地，描述符先只加 `Writable:true`，由 `suga-hyj7` 计划把它列入分类表；后落地则直接标 `Protected`。两种顺序都由分类表测试兜底（未分类键测试失败）。
- 顺带补 `hooks.pre_tool_use.tools/name` 描述符一事已由 `suga-hyj7` 承担，本设计不重复。

### 6 不纳入项目级 hook 配置（Q7 推荐 A）

suga 只有一层配置（`SUGA_HOME/config.yaml`），没有读取仓库内配置的机制；项目级 hook 会让仓库内容（含克隆的第三方仓库、模型写入的文件）引入在每个事件上自动执行的程序，绕开 protected 键与启动提示两道防线。IDEV-STD 续跑命令是你个人工具链的一部分，放全局配置即可满足需求。需要按仓库区分时，条目命令自身可读 `cwd` 字段自行判断。

## 架构

代码组织沿用 suga 现有分层（SR2004；参照本仓库 `internal/hooks`、`internal/chat`、`internal/config`）：
- `internal/hooks`：
  - `hooks.go` 拆出条目解析（新文件如 `entries.go`：读 `hooks.command` 兼容条目与 `hooks.commands`，校验并编号）；
  - 新文件如 `multi.go`：多条目 `Client` 实现——顺序执行、合并、两阶段 Stop 与 Interrupt；单条目仍走同一路径（一条的合并即原样）；
  - `Response`/`Result` 增加每条目运行记录（编号、程序名、耗时、状态、该条回答），供记录层逐条写 trace；
  - `PostToolName`/`PreToolName` 改为“是否有条目订阅该工具”，事件中的工具名由各条目自己的过滤器决定；
  - 现有 `commandClient.call` 保留为单条目执行原语。
- `internal/chat`：`hooks_record.go` 改为按每条目运行记录逐条写 trace；`hooks_gate.go` 构造事件时传原始工具名；`stop_background.go`、`chat.go`、`chat_turn.go` 只消费合并结果，逻辑不变。
- `internal/config/settings_registry.go`：`hooks.commands` 描述符与 `Reader`；校验函数放 `internal/hooks`，经注册表的 reader 分发调用（不在 config 包复制解析，计划阶段核对 import 方向，必要时把纯解析放到无依赖的小包）。
- `internal/wiring/localenv.go`：`inspect env` 展示条目数与各条目程序名。
- 不改 `pkg/*`。

## 关键流程

```text
回合结束 → Stop 合并调用
  阶段一：即时条目 i=1..n（订阅 Stop）顺序执行，各自 timeout_ms
     ├─ 有 block → 合并 reason（Q4）→ 结果 block，跳过阶段二
     └─ 无 block → 阶段二：等待型条目（若有）带 stop_args 运行，上限 stop_wait_sec+2
                      └─ 结果即 Stop 结果
  前台：提示符处等待合并结果；你输入 → 正在运行的条目收 Interrupt 并取消，
        已得 block reason 交回为未消费回复
  后台：整个流水线为一个 hook 任务；block → 任务通知

PreToolUse（工具 X）
  条目 i 订阅 X？→ 以当前参数（含前面条目的改写）调用
     ├─ 失败/超时 → 拒绝调用（fail closed），停止
     ├─ deny → 拒绝调用，停止
     └─ updatedInput → 替换参数，继续下一条
  全部通过 → 最终参数进入审批
```

## 安全、数据、运维与回滚

- **SR1301**：鉴权——新键为 protected，模型在会话内不能经 `config set` 写入（依赖 `suga-hyj7`）；注入防护——argv 直接执行，不经 shell；秘密——trace 只记程序名（argv[0] 基名）与条目编号，不记参数；入参校验——启动与 `config set` 同一套校验（程序名非空、事件名合法、glob 可解析、至多一个等待型条目、`stop_args` 需配 `stop_wait_sec`）；资源归属——不适用。
- **执行门不变弱**：PreToolUse 任一条目失败即拒绝，条目越多只会更严；改写链的最终参数仍经审批。
- **对 gofer 的影响**：只配 gofer 时无变化。加了续跑条目且它要求继续时，本回合 gofer 不收 Stop、不进入等待，web 侧会话保持“运行中”直到续跑停止（IDEV-STD 以状态不前进与最多 8 次为界）后的那次 Stop；这与“agent 仍在工作”一致。gofer 收到的 UserPromptSubmit 会带 `injected=true`。
- **对 IDEV-STD 的影响**：安装命令改为 `suga config set hooks.commands '<YAML>'`；条目超时自带，不再需要改全局 `hooks.timeout_ms`；已有 `hooks.commands` 时需与你确认合并（整体替换写入）。
- **trace**：每个条目每次运行记一条 `kind=hook` 事件（name 为事件名），attrs 新增 `entry`（编号）与 `program`（argv[0] 基名）；只有兼容条目时同样带这两个字段，其余字段不变（只增不改）。打断时未启动的条目不记。
- **运维**：顺序执行使事件延迟为各条目耗时之和；SessionEnd 在退出时最多等待各条目超时之和。条目数无上限，文档建议控制在少数几条。
- **数据**：无存储结构变化。
- **回滚**：revert；删掉 `hooks.commands` 即回到单命令行为。旧版本运行时不读取 `hooks.commands`，只剩兼容条目生效（计划阶段核对旧版本 `config validate` 对该键的提示）。
- **验证要求**（计划中落实）：
  - 单元（`internal/hooks`，用假 hook 进程）：条目解析与全部校验错误；每个事件的顺序、各自超时、单条失败不影响其他条；PreToolUse deny/失败短路与改写链；Stop 阶段一 block 跳过等待条目、多 block 合并、阶段一无 block 时等待条目运行、等待条目被打断收 Interrupt 而未启动条目不收、已得 reason 作为未消费回复交回；只有兼容条目时与现有测试结果一致（现有 `hooks_test.go`、`hooks_pretool_test.go` 不改断言）。
  - GR110：经 `internal/cli` 真实命令分发——`suga config set hooks.commands` 合法值写入、非法值退出码 2 且文件不变、`config get` 输出；protected 拒绝随 `suga-hyj7` 一起验证。
  - ptye2e（`test/ptye2e`，参照 `chat_observe_test.go`）：配两条命令（一条模拟等待型 gofer、一条模拟续跑并 block 一次），断言续跑回合开始且等待条目未被调用，下一次 Stop 等待条目运行；再在等待期间输入，断言 Interrupt 只到等待条目；结束后 `suga inspect trace --json` 检查每条 `hook` 事件的 `entry`/`program`/decision。反向验证：临时去掉阶段一短路、临时把 Interrupt 发给全部条目，确认对应检查失败（确认失败来自断言而非编译错误）。
  - 后台 Stop：`stop_background` 下同一组合，block 变为任务通知、输入不打断。
  - Windows：经 gofer exec job 在主机跑 `go test ./internal/hooks/... ./internal/chat/...` 与 ptye2e ConPTY 场景（含被打断条目的进程终止）。

## 决策

- **D1** 多条目执行与合并全部在 `internal/hooks` 内完成，chat 只消费合并结果与逐条运行记录（GR108 精神：消费方不含合并逻辑）。
- **D2** 只配 `hooks.command` 时行为、超时、trace 字段（除新增 attrs）与现状一致；现有测试断言不改。
- **D3** PreToolUse 多条目下保持 fail closed：任一条失败或 deny 即拒绝。
- **D4** 不引入 exit 2 语义、不改事件与回答格式。
- 以下 Q1–Q7 待你选定后转为决策。

## 待确认事项

**Q1 配置形状**
- A（推荐）`hooks.commands` 条目列表，每条自带 `command/events/timeout_ms/stop_args/stop_wait_sec/pre_tool_use/post_tool_use`。理由：沿用 suga 现有键名与 argv 语义，gofer 那条无需拆到各事件下重复书写；一条 `config set` 即可写入；Stop 等待等 suga 特有语义有地方放。
- B Claude/Codex 的按事件分组 `hooks.<Event>[].hooks[].command`（`matcher` 选工具）。好处是与 Claude/Codex 文档同形，IDEV-STD 可复用写法；代价是 gofer 命令要在 7 个事件下各写一遍，事件名键与 `hooks.timeout_ms` 等现有键混在同一映射里，`stop_args/stop_wait_sec` 无对应位置，且 Claude 的 `command` 是经 shell 的字符串，与 suga 的 argv 不一致。
- C 最小：`hooks.commands` 只是 argv 列表，共享全局超时，新增命令都不能等待、不收 `stop_args`。改动最小，但不能给续跑命令单独设超时（IDEV-STD 需要 10 秒），也无法让新命令订阅部分事件。

**Q2 同一事件多条目的执行方式**
- A（推荐）按编号顺序逐个执行，各自超时。理由：结果确定，PreToolUse 改写可以链式传递，续跑与等待的先后可控；条目少时延迟可接受。
- B 并发执行（Claude 的做法），合并全部结果。延迟最低，但 PreToolUse 多条改写冲突需另定规则，Stop 的“先续跑后等待”要另加编排。

**Q3 Stop 长等待与多条目**
- A（推荐）先即时、后等待：即时条目顺序执行，有 block 就不启动等待条目；等待条目最多一个；打断只给正在运行的条目发 Interrupt。理由：续跑优先于等待人回复，避免 gofer 在 agent 仍要继续时向 web 报“等你回复”；单个等待条目让“谁的等待生效”没有歧义。
- B 全部条目（含多个等待条目）并发，谁先 block 用谁，其余条目收 Interrupt 后取消。支持多个等待来源，但 gofer 每回合都会先进入等待再被撤销，web 侧状态来回跳。
- C 全部按配置顺序执行，block 即短路，不区分等待型。规则最简单，但等待条目若排在前面，续跑条目要等它超时（可长达 2 小时）才运行。

**Q4 多个 Stop block 的合并**
- A（推荐）阶段一全部执行，reason 按顺序以空行连接为一次输入。理由：每个即时条目都收到 Stop（有的条目借 Stop 记录状态），且不丢任何一方的继续理由。
- B 第一个 block 短路，后面的即时条目不调用。少一次调用，但后面的条目这回合收不到 Stop。

**Q5 `hooks.command` 与 `hooks.commands` 同时出现**
- A（推荐）共存：`hooks.command` 为条目 0，旧配套键只属于它，`hooks.commands` 依次追加。理由：IDEV-STD 等外部安装只写新键，不改你已有配置；旧配置零迁移。
- B 互斥：两者同时出现时启动报错，要求迁移到 `hooks.commands`。语义更统一，但每次加一条都要整体重写，含 gofer 那条。

**Q6 `hooks.commands` 是否可经 `suga config set` 写入**
- A（推荐）可写 + protected（人在终端可写，会话内被拒），带校验 reader。理由：IDEV-STD 安装按惯例输出 `config set` 命令；protected 保证模型不能写。
- B 不可写、只参与启动比较（同安全配置键设计对 `hooks.pre_tool_use.*` 的处理），只能直接编辑 `config.yaml`。少一个 CLI 写入口，但 IDEV-STD 需改为提示编辑文件。

**Q7 项目级 hook 配置**
- A（推荐）不纳入，理由见总体方案 §6。
- B 纳入（仓库内文件声明条目）。需要另设信任机制（如首次运行确认、按仓库指纹记忆），超出本次范围，若选择需要单独设计。

## 结论与人工计划 Gate

本设计为 Draft 0.1，Q1–Q7 待你选定；之后按高暴露度做两轴独立评审，评审无开放 `CORE_BLOCKING` 并经你批准后才写计划。批准只授权进入计划，不授权实施。定稿后把配置形状回复 IDEV-STD 维护会话（my-tools-dev-08）。
