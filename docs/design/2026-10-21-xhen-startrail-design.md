# xhen（星痕 / Startrail）：常驻个人助手 初版设计

> 状态：草案 0.1（2026-10-21），待用户审阅。
> 关联：suag-frgt（以人物定义的永久会话构想）；借鉴整理 `local/analysis/2026-10-21-jev-nomi-borrowing.md`、qybaihe/mu 分析（9812b048）。

## 1. 定义

xhen 是一个常驻在你电脑上的个人助手。LLM 是它的大脑，但大脑不记事；xhen 是它的身体和记忆，工具是它的手脚。它认识你，记得你们一起做过的每件事，能同时照看多件事，也会自己去发现和学会使用新工具。

- **大脑 = LLM**：负责思考与决策，不记事；每次由 xhen 把“现在需要知道的东西”喂给它。
- **身体与存储 = xhen**：身份与性格、对你的了解、长期记忆、主题（正在进行与做完的事）、时间感（日程、心跳、提醒）。
- **手脚 = 工具**：终端、文件、浏览器、电脑操作、外部服务；xhen 主动发现本机工具，学会后记成技能。

与 suga 的关系：**suga 是引擎**，在一个干净上下文里把一件事做好；**xhen 是产品**，决定“你是谁、记得什么、手上有哪些事、什么时候主动做”。xhen 的每个主题背后是一个 suga 会话。suga 保持开发者 coding agent 定位，默认行为不因 xhen 改变。

名称：命令与代码用 `xhen`，对外英文名 **Startrail**（星轨：长曝光下星星留下的轨迹——每刻很小，连起来是完整的轨迹）。

## 2. 架构选择：进程内 SDK，而不是 ACP

用户 2026-10-21 提问：ACP 会不会有限制？用 `pkg/sugasdk` 直接调用会不会更灵活？结论：**会，选 SDK**。

| 需要的能力 | ACP（`suga acp`） | `pkg/sugasdk`（进程内） |
|---|---|---|
| 建会话、发消息、流式事件、取消、续接 | 有 | 有 |
| 给会话注入 xhen 自己的工具（切主题、提醒、浏览器…） | 只能把 xhen 做成 MCP server 再传给 session/new，绕一层进程 | 直接注册 Go 工具 |
| 注入人物与主题上下文（每段的上下文消息） | 只有系统提示附加，且要走自定义扩展 | 直接传入 |
| 读会话内部状态：交接摘要、计划、用量、trace | 协议里没有，要加 suga 私有扩展方法 | 直接调用 |
| 记忆读写与整理 | 只能让模型调 memory 工具 | 直接调用 |
| 工具调用前后的 hook（审计、风险闸门、loop_guard 等） | 没有 | `agentcore.Hook` 直接挂 |
| 后台循环在会话外做事（整理、心跳） | 只能发 prompt | 直接调用 |
| 资源 | 每个 suga acp 进程一套 provider/MCP | 一个 daemon 进程共享 |

ACP 是“编辑器 ↔ agent”的协议，面向外部客户端；xhen 需要的大多是**引擎内部的钩子与状态**，用 ACP 每一项都要加私有扩展，最后等于在协议上重造一个 SDK。

代价与对策：

- **耦合**：xhen 跟随 suga 的 Go API 版本。对策：只经 `pkg/sugasdk` 这一层，SDK 面向用例、小而稳定；同仓库期间改接口一次提交改两边。
- **崩溃隔离**：一个会话 panic 可能带倒 daemon。对策：每个回合在 SDK 边界 recover，记录并把该主题标记为出错，daemon 继续。
- **并发**：daemon 长期持有会话，同时你可能在另一个终端用 `suga chat`。suga 的 store 是 SQLite WAL，已支持多进程；同一会话被两个进程同时推进由现有会话锁（`suga ps` 那套）拒绝。

ACP 不丢：以后 xhen 自己可以**对外提供 ACP**（让编辑器或 gofer 把 xhen 当一个 agent 接入），那是渠道层的事。

## 3. 代码组织与边界

```
cmd/xhen/            入口（daemon、终端客户端、管理命令）
pkg/sugasdk/         suga 引擎对外的进程内 API（本仓库内包装 internal/*）
pkg/xhen/            xhen 全部产品代码，只依赖 pkg/sugasdk、pkg/agentcore 与第三方库
```

- **分层测试**（照 `pkg/agentcore/layering_test.go`）：`pkg/xhen/...` 不得 import `internal/*`；`pkg/sugasdk` 是唯一可以 import `internal/*` 的 pkg。
- 拆仓时机：SDK 接口稳定、或 xhen 发版节奏与 suga 明显不同。拆时 `pkg/xhen` + `cmd/xhen` 搬走，依赖 suga 模块的 `pkg/sugasdk`，几乎只是挪目录。

### 3.1 `pkg/sugasdk` 初版接口（用例驱动，只放 xhen 第一阶段要用的）

```go
type Engine struct{ ... }                       // 一个进程一个；共享配置、provider 缓存、store
func Open(ctx, EngineOptions) (*Engine, error)  // SUGA_HOME、日志、默认模型
func (e *Engine) Close() error

type SessionOptions struct {
    Workspace   string            // 该主题的工作目录（可为 xhen 的主题目录）
    Model       string
    AllowWrite  bool
    ExtraTools  []tools.Tool      // xhen 注入的工具（新增 wiring 扩展点）
    Hooks       []agentcore.Hook
    Context     func() string     // 每段上下文消息里追加的内容：人物 + 主题索引 + 原话约束
    Approver    tools.Approver    // 交给 xhen 决定（终端问你 / 后台默认拒绝写）
}
func (e *Engine) OpenSession(ctx, id string, SessionOptions) (*Session, error)   // 新建或续接
func (s *Session) Send(ctx, Prompt, emit func(Event)) (Result, error)          // 一个回合
func (s *Session) Steer(text string) / Cancel()
func (s *Session) Card(ctx) (Card, error)       // 主题卡片：最近交接摘要、计划未完成项、用量、最后活跃
func (s *Session) Compact(ctx) error            // 主动换段（切走主题前收尾）

func (e *Engine) Memory() MemoryAPI             // search / add / list（复用 suga 记忆）
func (e *Engine) Search(ctx, query) ([]Hit, error) // 跨会话 session_search
func (e *Engine) Sessions(ctx, filter) ([]SessionInfo, error)
```

需要在 suga 内部补的扩展点（对 suga 默认行为无影响）：`wiring.Options.ExtraTools`、段上下文追加回调、会话卡片读取（交接摘要 + 计划 + 用量的只读组合）。

## 4. 核心概念

### 4.1 人物（Identity）

`$XHEN_HOME/identity/`：
- `SOUL.md` 名字、性格、说话方式、价值与边界（你写，xhen 只能提议修改，你确认才落盘）
- `USER.md` 对你的了解：称呼、偏好、习惯、长期约束（xhen 可追加，带来源与日期；冲突时提示你）

两者压缩后放进每个主题会话的段上下文（有大小上限，超了在 manifest 报 OVER，同 suga 常驻上下文规则）。

### 4.2 主题（Topic）

- 一个主题 = 一个 suga 会话 + 一张卡片：标题、状态（进行中/等待/搁置/完成）、摘要、未完成项、原话约束、工作目录、最后活跃。
- **主题索引**（所有进行中与最近完成的主题，每条一行）放进 xhen 的“前台会话”上下文；你说“回到 X 那件事”就能定位。
- 路由（第一阶段只做显式）：`/new 标题`、`/to <主题>`、`/topics`、`/done`。不带命令的消息发给**当前主题**。自动判断“这条消息属于哪个主题/是否新主题”放到第二阶段，且先 shadow（只记录判断与你的实际选择，统计准确率后再启用）。
- 切走时：对旧主题会话做一次主动交接（`Compact`），卡片更新；切回时：会话续接，交接摘要 + 卡片进上下文。
- **原话约束**：你在某主题里说的“不要…/必须…/以后都…”类句子按原话进卡片，每段上下文都带；跨主题的长期约束进 `USER.md`。

### 4.3 记忆（Memory）

复用 suga 记忆（user/project 两层、带摘要的索引、FTS 检索）。xhen 增加：
- 来源标注（哪个主题、哪天）；
- 后台整理：重复/冲突候选（即 suga-649p `memory lint` 的结果）→ 生成待你确认的清单，不自动改；
- 退役：长期从未被用到、或被你否定的条目标记退役（借鉴 mu“召回多次从未照做即退役”）。

### 4.4 两个循环

- **前台**：你与 xhen 的对话（终端客户端连到 daemon）。
- **后台**（daemon 定时）：心跳检查待办与到期提醒、整理记忆与主题卡片、对“等待中”的主题做跟进检查。**默认只读**：可以读文件、查状态、整理自己的存储；任何写外部世界的动作（改文件、发消息、操作浏览器）只生成提议，等你在前台确认。每项后台行为可单独开关，新行为先 shadow。

### 4.5 身体（Tools）

- 第一阶段：suga 已有的终端、文件、web、记忆、计划工具 + xhen 工具（主题管理、提醒、查看/编辑档案提议）。
- 第二阶段：浏览器与电脑操作经 MCP 接入（如 Playwright MCP、computer-use 类 MCP）；**工具发现**：扫描 PATH 与已配置 MCP，列出“可能有用但没用过”的工具，试用（只读/帮助命令）后写成技能条目，你确认后纳入常用。

### 4.6 入口与常驻

- `xhen daemon`：后台进程，持有 Engine、所有主题会话、调度器；本机 socket（Unix 域 socket / Windows 命名管道或 127.0.0.1 + token）对客户端提供 API。开机自启由用户自行配置（文档给出 Windows 计划任务 / systemd 示例），不自动注册。
- `xhen`（无参数）：终端客户端，连上 daemon 进入对话（复用 suga chat 的渲染与输入组件，经 SDK 暴露或共享包）；daemon 没运行时提示启动或 `--foreground` 临时起一个。
- `xhen topics|identity|memory|status`：管理命令。
- 以后：gofer 渠道、Web、IM；以及对外提供 ACP。

## 5. 数据布局

```
$XHEN_HOME (默认 ~/.xhen)
  identity/SOUL.md, USER.md
  topics.db            主题卡片、路由记录、后台任务与提醒（SQLite）
  topics/<id>/         主题默认工作目录（未指定工作区时）
  logs/
suga 数据仍在 SUGA_HOME（会话、记忆、trace），xhen 用独立 SUGA_HOME 子目录或共用由配置决定（见待定 3）
```

## 6. 分阶段

- **第一阶段（MVP）**：sugasdk 初版 + 3 个 suga 扩展点；daemon + 终端客户端；人物档案；显式主题管理与索引、切换时交接；原话约束；后台心跳（提醒、记忆整理提议）；trace 可查。
- **第二阶段**：自动主题路由（shadow 起步）、浏览器/电脑操作、工具发现与技能沉淀、外部渠道。
- **首版不做**：多用户、云端托管、修改自身代码。

## 7. 评测

长会话评测（与 suga 共用 `scripts/eval`，新增多轮场景类型）：
- 跨压缩：前面给出约束，若干轮与一次换段后，检查是否仍遵守；
- 切主题再切回：A 做到一半 → B → 回 A，检查 A 的进度与约束是否接上；
- 后台提醒：到期时间点是否触发、是否误触发。
真实模型跑，结果记 `model-log`。

## 8. 风险

- 常驻进程长期持有 provider 连接与 MCP 子进程：需健康检查与重连；内存泄漏用长时间测试观察。
- 人物与用户档案常驻上下文会挤占预算：严格上限 + manifest 报告。
- 后台循环的成本：心跳本身不调模型，只有发现需要处理的事才调；每日预算上限（复用 turn budget）。
- 隐私：所有数据在本机；外发只经你配置的 provider 与工具。

## 9. 待定（请用户决定）

1. **SDK 方案**：同意进程内 `pkg/sugasdk`、`pkg/xhen` 只依赖它（分层测试守护）？
2. **终端客户端的渲染与输入**：直接复用 suga chat 的组件（需要把 `internal/chat` 的一部分抽成可复用包）还是 xhen 先做简单行界面？建议复用，抽出时不改 suga chat 行为。
3. **suga 数据是否与 suga 本身共用 SUGA_HOME**：共用的话 `suga sessions` 能看到 xhen 的主题会话、记忆互通；分开则互不干扰。建议共用记忆与配置、会话加来源标记（`origin=xhen`），默认 `suga sessions` 不列出。
4. **daemon 与客户端的通信**：本机 socket 自定义 JSON-RPC，还是直接用 ACP（客户端当 ACP client）？建议后者：客户端协议用 ACP 现成的会话/流式语义，xhen 的主题命令走扩展方法；这样以后编辑器也能直接接 xhen。
5. 第一阶段范围是否合适。
