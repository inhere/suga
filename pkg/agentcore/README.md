# agentcore

`agentcore` 是 suga 的 Agent 内核与能力层：单 agent 的模型/工具循环、可恢复的 run 状态与 transcript，以及可直接组装的 provider、工具、技能与 MCP 接入。它不依赖 suga 的应用层（`internal/*`），可以单独嵌入；当前为 `v0`，接口仍可能调整。

它负责：

- 准备或恢复 run state，构建 prompt 与上下文
- 调用模型 provider，执行 tool calls
- 持久化 snapshot 与 append-only transcript
- 暴露 hooks、policies 与稳定的错误分类

它不负责：会话产品逻辑（会话、压缩、交接）、配置读取、终端与 ACP 接入、多 agent 编排——这些属于宿主（suga 的 `internal/*`）。

## 分层

```text
应用层     suga internal/*: config, wiring, personal(+store), chat, acpserver, cli, hooks, output
              │ 只向下依赖
宿主侧测试 agentcore/testing（内核一致性检查）、agentcore/mock（假 provider 与假工具）、agentcore/testutil
           （内核之上的宿主侧测试套件：testing 依赖根包，mock 依赖根包与 api）
              │ 依赖内核与能力层
能力层     agentcore/llm          OpenAI 兼容与 Claude provider、fallback 池
           agentcore/schedule     进程内定时：After/Every/Idle，只依赖标准库
           agentcore/tasks        后台任务：起命令或函数、读输出、等待、结束（按会话限额，结束通知交宿主）
           agentcore/tools        注册表、审批层、延迟加载（visibility）、输出治理（Output/Spiller）、工具元数据
           agentcore/tools/native 内置工具：文件读写/搜索、shell、web、update_plan、tool_search、skill、memory、session_search、recall
           agentcore/tools/mcp    stdio MCP 客户端，工具以 MCP kind 注册
           agentcore/skills       SKILL.md 解析、多根目录加载与检索
           agentcore/memory       长期记忆：Store 接口、Governed 治理层、Router 范围路由；memory/files、memory/execstore 两个内置后端
           agentcore/redact       密钥脱敏（Secrets）与不可信文本清理（Clean）
           agentcore/extproc      外部进程 exec 协议 v1 与去凭据环境（exec 记忆后端、MCP 子进程共用）
           agentcore/storage/files 文件存储
              │
内核       agentcore (根包) + agentcore/api：Runtime、契约类型（含 api.History）、hooks、policies、内存默认存储
              │
共享底座   agentcore/internal/*：runid（run id 生成）、utils（元数据克隆）、proctree（进程树回收）；只能被 agentcore 内部引用
              │
示例       agentcore/examples/*：可运行的嵌入示例（见「示例与文档」），与其它包一样受 layering_test 白名单约束
```

依赖只能自上而下。`layering_test.go` 守住边界：每个包允许依赖哪些 agentcore 包在测试里有一张白名单表（表外依赖、未登记的新包都失败，改表前先想清楚方向）；并且 `pkg/agentcore` 下任何包（含测试）都不依赖 `pkg/agentcore` 之外的 `github.com/inhere/suga/...` 包。根包不依赖能力层：不传 `Config.Tools` 时用私有空注册表，不传 `Config.Storage` 时用内存存储。

## Quick Start

最小可运行组合是一个 provider：

```go
runtime := agentcore.New(agentcore.Config{
    Provider: mock.NewProvider(mock.Final("hello")),
})

result, err := runtime.Run(ctx, agentcore.RunInput{
    AgentID:     "example-agent",
    Instruction: "say hello",
})
```

不传 `Storage` 时 runtime 使用进程内的 `agentcore.NewMemoryStorage()`：默认不持久化，进程退出即丢失；跨进程或跨 Runtime 实例续跑需显式传入 `files.NewStorage(dir)` 或自己的存储实现。

## 只读 Agent

[examples/recommended](./examples/recommended/main.go) 是推荐的组装方式：真实 provider（`llm`）+ 只读内置工具 + 技能 + 可选 MCP + 审批层。核心几步：

```go
reg := tools.NewRegistry()
for _, tool := range native.BuildTools(native.Env{Workspace: workspace}, false) { // false: 只读工具
    reg.Register(tool)
}
reg.Register(native.NewSkillTool(skills.LoadRoots(roots), 0))

approval := tools.NewApproval(reg, tools.ApproverFunc(func(ctx context.Context, call agentcore.ToolCall, tool agentcore.Tool) error {
    if tools.MetaOf(tool).ReadOnly {
        return nil
    }
    return tools.Denied(call.Name + " needs approval") // 真实宿主在这里询问用户
}))

runtime := agentcore.New(agentcore.Config{
    Provider: llm.NewOpenAIProvider(llm.OpenAIConfig{APIKey: key}),
    Tools:    approval,
})
```

写工具（`file_write`、`file_edit`、`shell_exec`）只在 `BuildTools(env, true)` 时注册，且必须经过 `tools.NewApproval` 的 Approver 才会执行；没有 Approver 时审批层拒绝一切调用。

## 工具

- `tools.Registry`：`Register`（同名 panic）、`TryRegister`（先到先得）、`MustRegister`（覆盖）、`Get`、`List`。
- 工具实现 `agentcore.Tool`（即 `api.Tool`）。可选接口：
  - `tools.Kinded`（`Kind()`），缺省 native；读取用 `tools.KindOf(tool)`。
  - `tools.Describer`（`ToolMeta()`），声明中性类别（read/edit/execute/search/fetch/think）与只读标记；读取用 `tools.MetaOf(tool)`。
- MCP 工具：`tools.IsMCP(tool)`、`tools.MCPServerOf(tool)`。`MetaOf` 对 MCP 工具一律返回“其他、非只读”，同名冒充内置工具也不会走只读快速路径。
- 每次工具调用都有一个调用 ID：runtime 在 `BeforeTool` 之前放进 ctx，钩子与工具执行里用 `tools.InvocationID(ctx)` 读到的都是同一个值，同一回合内不同调用各不相同（`<run_id>/<call_id>`）。宿主工具可以拿它当自己副作用账本的幂等键。
- 分层顺序：observe（宿主）→ `tools.Output`（输出治理）→ `tools.Visibility`（延迟加载 MCP schema）→ `tools.Approval`（审批）→ `tools.Registry`。
- `tools.Output`：结果不超过 `InlineBytes` 原样通过；更大的结果交给宿主实现的 `tools.Spiller` 存下全文，上下文换成 ref 行加头尾片段（未配置 Spiller 时只截断）。错误结果不改写；`Meta.Rereadable` 的工具（自带分页，如 `file_read`、`recall`）不经外置。`Meta.External` 标记结果含外部内容（如 `web_fetch`、MCP 工具），宿主据此做污染标记；`Meta.AgentState` 标记写的是 agent 自身状态而非工作区（如 `memory`）。
- `native.Env` 的 `InlineBytes`（内联阈值）与 `RawBytes`（单次原始输出上限）取代原 `MaxOutputBytes`。
- 宿主把类别映射到自己的协议，例如 suga 的 ACP 服务把 `CategoryEdit` 映射为 `acp.ToolKindEdit`；ACP 概念不进入 agentcore。

## llm：Claude 缓存与段上下文

- `llm.ClaudeConfig.PromptCache`（默认关）：开启后在两处加 `cache_control` 断点（至多 2 个，Anthropic 上限 4 个），`system` 改为块数组；关闭时 `system` 仍是字符串，网关不接受 `cache_control` 时保持关闭。
- 断点位置：带 `llm.SegmentContextMetaKey`（`Message.Metadata["suga.segment_context"] = true`）的消息的最后一个块，以及请求的最后一个块。宿主构建段上下文消息时要设置同一个 key，稳定前缀（system + tools + 段上下文）才会被缓存。

## schedule：进程内定时

`schedule.New(opts...)` 返回 `Scheduler`，任务按名字注册，`Func` 收到的 ctx 在任务取消或调度器关闭时取消：

| 方法 | 作用 |
|---|---|
| `After(name, d, fn)` | d 后触发一次，然后注销 |
| `Every(name, d, fn)` | 每隔 d 触发 |
| `Idle(name, d, fn)` | 最后一次 `Touch` 之后 d 触发一次，再等下一次 `Touch` |
| `Touch()` | 有活动：重置所有 Idle 计时 |
| `Close()` | 取消全部任务并等待运行中的任务结束 |

- 三个注册方法返回 `Handle`：`Cancel`（注销，运行中的调用看到 ctx 取消）、`Reset`、`Pause`、`Resume`。同名再次注册会替换旧任务。
- 同一任务不会并发：上一次还在运行时到点的触发记为 `skipped`。
- `WithClock(Clock)` 注入假时钟（测试用）；`WithObserver(func(Event))` 接收事件（scheduled、fired、skipped、cancelled、paused、resumed、done、failed）。观察者在调度器锁外调用，可以重入调度器（如在事件里 `Touch` 或 `Close`）；极短的任务可能先收到 `done` 再收到 `fired`，需要顺序时按 `Event.At` 排序。
- suga 的 chat 用它做空闲整理（`Idle` 任务 + 回合期间 `Pause`）。

## 记忆

`memory` 包提供长期记忆的抽象，不含任何产品策略：

- `memory.Store`：`Index`、`Get`、`Write`（add/replace/remove）、`Search` 四个方法；条目分 user、feedback、project、reference 四类，范围为 user 或 project(key)。
- `memory.Governed`：包在任意 `Store` 外的治理层，负责清理读写内容（`Policy.Sanitize`，宿主传 `redact.Clean`）、已污染会话写入前的确认（`Policy.Confirm`）、条目预算、来源与墓碑（`ProvenanceStore`）和事件回调。
- `memory.Router`：按范围把请求分给不同后端。
- 后端：`memory/files`（`MEMORY.md` 索引 + 一条一文件，`DirLayout` 与仓库内的 `RepoLayout`）、`memory/execstore`（经 `extproc` 调外部命令）。gofer 后端依赖 gofer CLI，属于应用层适配器，不在 agentcore 内。
- 工具 `native.NewMemoryTool(store, opts)` 把 `Store` 暴露给模型；`native.NewSessionSearchTool` / `NewRecallTool` 经窄接口 `native.LedgerSearcher` 检索历史，由宿主实现。

## 核心契约

- `Provider` 连接 LLM，返回 final answer 或 tool calls；实现 `Stream` 时转发增量文本。
- `Storage` 保存 run snapshot 与 append-only transcript，是恢复和审计的事实来源；可选 `ArtifactStorage`。
- `ToolRegistry` 暴露 tool schema 并执行工具调用。
- `PromptBuilder` 生成 system prompt；`ContextBuilder` 组装发给模型的消息（未提供时走 `History.BuildContext`，默认 `History` 原样使用完整 transcript）。`api.History`（`Config.History`）是上下文工程层，原名 `api.Memory`，因“记忆”改指长期记忆而改名。
- `Hook` 拦截生命周期节点；`ResumePolicy`、`ToolExecutionPolicy`、`ErrorPolicy` 定制恢复、工具错误与可恢复错误的处理。
- `ToolResult.IsError` 标记失败或被拒绝的工具调用，provider 据此告诉模型（如 Claude `tool_result.is_error`）。
- `api.Usage.Add` 累加用量。

读取某次 run 的权威视图可用 `agentcore.LoadRunAuthority(ctx, store, runID)` 与 `agentcore.LoadAuthorityBundle(ctx, store, runID)`。

## Resume

恢复基于稳定 `RunID` 与显式 `ResumeMode`：

- `ResumeFromState`：只从已有 state / transcript 继续，即使传了 `Instruction` 也不追加 user message。
- `ResumeWithInput`：恢复时追加一条新的 user input。
- `ResumeAuto`：有非空 `Instruction` 时追加，否则直接继续。

```go
first, err := runtime.Run(ctx, agentcore.RunInput{RunID: "task-123", AgentID: "agent-1", Instruction: "analyze this repo"})
if err != nil || first.Status != agentcore.RunYielded {
    return err
}
resumed, err := runtime.Run(ctx, agentcore.RunInput{RunID: "task-123", ResumeMode: agentcore.ResumeFromState})
```

终态（completed / failed）的 run 不能再恢复。跨 Runtime 实例恢复需要两边使用同一个持久化存储。

## Transcript

一次工具交互持久化为 `assistant(tool_call)` 与 `tool(result)` 两条消息，而不只是最终输出。这保证 resume 时 provider 看到完整上下文，replay 不丢失模型的工具决策，不同 provider 围绕同一个 `Message` 契约工作。

## 示例与文档

- 示例：[examples/README.md](./examples/README.md)（全部参与 `go vet`，用 mock 离线运行；`recommended` 设置 `OPENAI_API_KEY` 后使用真实模型）
- 文档：[docs/README.md](./docs/README.md)
- 一致性检查：[testing/README.md](./testing/README.md)
- 2026-10 分层重构中删除或归档的包与文档：[删除记录](../../docs/archive/removed-2026-10.md)
