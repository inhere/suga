# Agentcore API Guide

这份文档面向要扩展 `agentcore` 的开发者，重点解释公共接口各自负责什么，以及这些接口之间的边界关系。

## 1. Runtime 入口

最主要的构造入口是：

```go
runtime := agentcore.New(agentcore.Config{...})
```

`Config` 当前主要包含：

- `Provider`
- `Storage`
- `Memory`
- `Tools`
- `PromptBuilder`
- `ContextBuilder`
- `ResumePolicy`
- `ToolExecutionPolicy`
- `ErrorPolicy`
- `Hooks`
- `MaxSteps`
- `MaxToolCallsPerStep`

其中：

- `Provider` 是必需的
- 其他组件如果没有显式提供，runtime 会尽量补默认实现

如果你需要调整恢复策略，而不是把这类逻辑塞进 hook，可以提供一个自定义 `ResumePolicy`：

```go
type ResumePolicy interface {
    AllowResume(ctx context.Context, input RunInput, state *RunState) error
    ShouldAppendInstruction(input RunInput, state *RunState) bool
    MergeMetadata(stateMetadata, inputMetadata map[string]any) map[string]any
}
```

当前内置默认实现是 `agentcore.DefaultResumePolicy`，它保留现有 v0 语义：

- terminal state 不允许 resume
- `ResumeMode` 决定是否追加新的 user input
- input metadata 覆盖同名 persisted metadata

如果你需要调整 tool loop 主路径策略，而不是把这些语义塞进 hook，也可以提供自定义 `ToolExecutionPolicy`：

```go
type ToolExecutionPolicy interface {
    ValidateToolCalls(ctx context.Context, state *RunState, calls []ToolCall) error
    HandleToolError(ctx context.Context, state *RunState, call ToolCall, err error) ToolErrorDecision
}

type ToolErrorDecision struct {
    Result *ToolResult
    Fail   error
}
```

当前内置默认实现是 `agentcore.DefaultToolExecutionPolicy`，它保留现有 v0 语义：

- 使用 `MaxToolCallsPerStep` 约束单步 tool call 数量
- tool 执行错误默认转成 `tool` transcript message，再继续下一轮模型调用

如果你需要把某些 recoverable runtime error 正式提升成“yield 给 host”或“返回替代结果”，也可以提供自定义 `ErrorPolicy`：

```go
type ErrorPolicy interface {
    HandleRecoverable(ctx context.Context, state *RunState, event *ErrorEvent) ErrorDecision
}

type ErrorDecision struct {
    Result *RunResult
    Fail   error
}
```

当前内置默认实现是 `agentcore.DefaultErrorPolicy`，它保留现有 v0 语义：

- 不主动覆盖 recoverable error 的默认处理
- 仍允许现有 `OnError` hook 做 yield / short-circuit
- `persist_tool_output` 这类 non-recoverable authority 风险路径仍不走这条策略

## 2. RunInput 与 RunResult

`RunInput` 是每次执行或恢复一次 run 的公共入口：

```go
type RunInput struct {
	RunID       string
	AgentID     string
	Instruction string
	Metadata    map[string]any
	MaxSteps    int
	ResumeMode  ResumeMode
}
```

建议这样理解：

- `RunID`
  同一次 run 的稳定标识；resume 时应该复用
- `AgentID`
  当前 agent 的稳定标识
- `Instruction`
  本次新增输入；是否会被追加到 transcript，取决于 `ResumeMode`
- `Metadata`
  附加元数据
- `MaxSteps`
  覆盖默认 step 上限
- `ResumeMode`
  定义恢复语义

`RunResult` 是这次调用返回给 embedding host 的出口：

- `RunCompleted`
- `RunYielded`
- `RunFailed`

但真正用于恢复和 replay 的事实层仍然在 `Storage` 里。

## 3. Provider

`Provider` 负责把 runtime 已经准备好的模型请求，转换成一次稳定生成结果。

```go
type Provider interface {
	Name() string
	Generate(ctx context.Context, req *GenerateRequest) (*GenerateResult, error)
}
```

适合自己实现 `Provider` 的场景：

- 接 OpenAI / Anthropic / Gemini / Ollama
- 包一层现有内部 LLM SDK
- 在 provider 层做路由、审计、限流或重试

`GenerateResult` 里的几个字段，推荐按 authority 边界这样理解：

- `Content`
  最终 assistant completion 的正文
- `Artifacts`
  这次 completion 产出的附属结果；runtime 会把它们追加到 `RunState.Artifacts`
- `Metadata`
  这次 completion 自身的事实元数据；runtime 会把它写到最终 assistant message 的 `Message.Metadata`

也就是说：

- `RunState.Metadata` 更适合 run-level / host-level 状态
- `GenerateResult.Metadata` 更适合 completion-level 事实

不要默认把 provider 返回的 final metadata 直接并入 `RunState.Metadata`，否则容易把一次 completion 的局部事实污染成整个 run 的长期状态。

## 4. Storage

`Storage` 是 authority layer，负责保存：

- 最新 run snapshot
- append-only transcript

```go
type Storage interface {
	LoadRun(ctx context.Context, runID string) (*RunState, error)
	LoadMessages(ctx context.Context, runID string) ([]Message, error)
	SaveRun(ctx context.Context, state *RunState) error
	AppendMessages(ctx context.Context, runID string, msgs ...Message) error
}
```

不传 `Config.Storage` 时 runtime 使用进程内的 `NewMemoryStorage()`（同时实现可选的 `ArtifactStorage`），进程退出即丢失；`storage/files` 提供文件实现。缺失的 run 应返回包装 `os.ErrNotExist` 的 `*StorageError`，runtime 据此把它当作新 run；非法 runID 返回 `*StorageError`。

建议：

- `LoadMessages` 保持追加顺序
- `AppendMessages` 保持 append 语义，不要做覆盖
- 即使底层用同一张表或同一份文件，也不要把 snapshot 和 transcript 混成一个公共对象

如果你需要比最小 `Storage` 更多的能力，推荐沿 optional capability 增长，而不是直接把主接口做大。

下一阶段最值得优先考虑的一类可选能力是 artifact persistence / replay：

```go
type ArtifactStorage interface {
	SaveArtifacts(ctx context.Context, runID string, artifacts []Artifact) error
	LoadArtifacts(ctx context.Context, runID string) ([]Artifact, error)
}
```

推荐理解方式：

- `Storage`
  继续承接最小 authority contract
- `ArtifactStorage`
  承接 artifact 这类 additive capability

这样做的目的不是把 artifact 从 authority 层剥离出去，而是：

- 不让主 `Storage` 接口过早膨胀
- 让 runtime 能在支持该 capability 时做 artifact replay / lookup
- 保持 files / db / remote store 等不同实现的增长路径一致

当前更推荐的边界是：

- artifact persistence / replay 更接近 `Storage` optional capability
- artifact-to-context 是否注入模型上下文，仍属于 `Memory` / `ContextBuilder` 决策

当前第一方 `files` 实现已经可以作为这个 capability 的最小示范：

- runtime 持久化 `RunState` 时，会在检测到 `ArtifactStorage` 时同步保存 artifacts
- runtime 恢复 run 时，会优先从该 capability 重新载入 artifacts
- capability 缺失时，runtime 仍保持当前最小语义，不要求所有 `Storage` 实现都支持 artifact replay

如果 host / context layer 想直接拿到同一套 authority 视图，而不是自己分别调用 `LoadRun`、`LoadMessages` 和 `LoadArtifacts` 再手动拼装，当前也可以直接复用：

```go
state, err := agentcore.LoadRunAuthority(ctx, store, runID)
```

这个 helper 的语义是：

- 以 snapshot 作为 run-level 基础
- 以 append-only messages 作为 authoritative transcript
- 在存在 `ArtifactStorage` 时，用 capability 层重建 artifacts

如果你希望显式区分这三部分，而不是直接拿一个已经 hydrate 完成的 `RunState`，也可以使用：

```go
bundle, err := agentcore.LoadAuthorityBundle(ctx, store, runID)
state := bundle.HydratedState()
```

推荐理解：

- `LoadAuthorityBundle`
  适合 host / debug / custom context 侧按部分消费 authority 数据
- `LoadRunAuthority`
  适合想直接复用官方 hydrate 规则、拿一份完整 `RunState` 的场景

当前 `AuthorityBundle` 也开始提供少量只读 helper，帮助 host 避免重复写样板查询逻辑，例如：

- `LastAssistantMessage()`
- `ToolMessages()`
- `FindArtifactByURI(uri)`

这类 helper 的设计目标是：

- 只服务 authority 读取
- 返回 clone 结果，避免外部修改底层 authority 视图
- 不把它演进成大而全查询框架

## 5. Memory

`Memory` 是上下文派生层，不是 authority transcript。

```go
type Memory interface {
	Hydrate(ctx context.Context, state *RunState) error
	ObserveMessages(ctx context.Context, state *RunState, messages ...Message) error
	BuildContext(ctx context.Context, state *RunState) ([]Message, error)
}
```

推荐职责拆分：

- `Hydrate`
  在 resume 或重新加载 state 后，把 transcript 同步到 memory 派生层
- `ObserveMessages`
  在 runtime 提交新消息后，增量更新 memory
- `BuildContext`
  输出最终给模型的上下文消息

如果你需要 summary、recent window、recall 或 context compaction，这一层最适合承载这些能力。

如果你只是想先用一个默认实现，而不是从零写自己的 `Memory`，当前可以直接看：

- `memory/`

它现在提供一个第一方 `WorkingMemory`，包含：

- summary slot
- recent transcript window
- 可配置的 system/tool 消息过滤

## 6. ToolRegistry

`ToolRegistry` 暴露可用 tool schema，并执行 provider 请求的 tool call。

```go
type ToolRegistry interface {
	ListTools(ctx context.Context) ([]ToolSchema, error)
	ExecuteTool(ctx context.Context, call ToolCall) (*ToolResult, error)
}
```

建议：

- `ListTools` 输出保持稳定顺序
- `ToolResult` 尽量补齐 `CallID` / `Name`
- 工具失败直接返回原始错误，让 runtime 包装成 `ToolError`；交给 `ToolExecutionPolicy` 转成结果的失败会带 `ToolResult.IsError = true`

不传 `Config.Tools` 时 runtime 使用私有空注册表（不列出工具，调用一律返回未注册的 `*ToolError`）。现成实现在 `agentcore/tools`：`tools.NewRegistry()`、审批层 `tools.NewApproval`、延迟加载 `tools.NewVisibility`，以及工具元数据 `tools.MetaOf` / `tools.KindOf` / `tools.IsMCP`；每次调用的 ID 用 `tools.InvocationID(ctx)` 读取（钩子与工具内一致，会话内唯一），见 [README](../README.md#工具)。

## 7. ToolExecutionPolicy

`ToolExecutionPolicy` 负责 tool loop 中两类最容易继续扩散进 hook 的主路径语义：

- 一批 tool calls 当前是否允许执行
- 某次 tool 执行失败后，runtime 应该继续还是直接失败

推荐边界是：

- `ToolRegistry`
  负责“有哪些工具”和“如何执行工具”
- `ToolExecutionPolicy`
  负责“当前能不能执行这些工具”和“执行失败后 runtime 如何处理”

默认 `agentcore.DefaultToolExecutionPolicy` 会：

- 在 `ValidateToolCalls(...)` 中沿用 `MaxToolCallsPerStep`
- 在 `HandleToolError(...)` 中把错误转成 `ToolResult`，让 runtime 持久化 `assistant(tool_call)` 与 `tool(result)`，再继续下一轮

如果你的 host 需要更严格或更宽松的策略，例如：

- 某些场景允许更多并发 tool calls
- 某些工具错误应该直接 fail run
- 某些工具错误需要被映射成稳定的业务结果

优先扩展这个 policy，而不是把 runtime 主路径语义散落到 `BeforeTool` / `AfterTool` hooks 中。

## 8. PromptBuilder 与 ContextBuilder

`PromptBuilder` 负责生成 system prompt：

```go
type PromptBuilder interface {
	BuildSystemPrompt(ctx context.Context, state *RunState) (string, error)
}
```

`ContextBuilder` 负责生成 provider messages：

```go
type ContextBuilder interface {
	BuildMessages(ctx context.Context, state *RunState) ([]Message, error)
}
```

可以把它们和 `Memory` 的关系理解成：

- `Memory`
  负责上下文派生内容
- `ContextBuilder`
  负责这些内容如何被组织成最终 messages

简单场景下，只有 `Memory` 就够了。
只有需要更强的消息排布控制时，才需要自定义 `ContextBuilder`。

## 9. Hook

`Hook` 是运行时生命周期扩展点。

当前主要节点：

- `BeforeRun`
- `BeforePromptBuild`
- `BeforeContextBuild`
- `BeforeModel`
- `AfterModel`
- `BeforeTool`
- `AfterTool`
- `AfterRun`
- `OnError`

它最适合做：

- 观测
- 审计
- 注入 metadata
- 轻量控制
- 局部修改 prompt / context / tool call / tool result

如果大量核心业务逻辑都只能藏在 hook 里，通常意味着核心 contract 需要升级。

## 10. Streaming

`agentcore` 当前已经支持一个最小 streaming contract。

基础事件模型是：

```go
type StreamEventKind string

const (
	StreamEventTextDelta   StreamEventKind = "text_delta"
	StreamEventMessageStop StreamEventKind = "message_stop"
)

type StreamEvent struct {
	Kind     StreamEventKind
	Delta    string
	Metadata map[string]any
}
```

host 侧通过 `RunInput.Stream` 接收事件：

```go
type StreamFunc func(ctx context.Context, event StreamEvent) error
```

provider 侧可以选择实现额外能力：

```go
type StreamingProvider interface {
	Provider
	Stream(ctx context.Context, req *GenerateRequest, emit StreamFunc) (*GenerateResult, error)
}
```

当前规则是：

- 只有当 provider 实现 `StreamingProvider` 且 caller 提供 `RunInput.Stream` 时，runtime 才走 streaming 路径
- 否则继续沿用现有 `Provider.Generate(...)`
- streaming delta 默认不直接进入 authority transcript
- 最终 `GenerateResult` 仍然是 authoritative completion

## 11. ResumeMode

`ResumeMode` 定义恢复时是否追加新的 user input：

- `ResumeFromState`
  只从已持久化状态继续
- `ResumeWithInput`
  先追加一条新的 user message 再继续
- `ResumeAuto`
  有输入就追加，没有输入就直接继续

推荐恢复流程理解为：

1. 用稳定 `RunID` 找回同一次 run
2. 加载 snapshot 与 transcript
3. 根据 `ResumeMode` 决定是否追加新的 user message
4. 再进入 runtime loop

如果你只是想调整“能不能恢复”“要不要追加输入”“metadata 怎么合并”，优先扩展 `ResumePolicy`，而不是把这些主路径语义分散到 hook 中。

一个最小例子：

```go
type permissiveResumePolicy struct {
    agentcore.DefaultResumePolicy
}

func (p permissiveResumePolicy) AllowResume(ctx context.Context, input agentcore.RunInput, state *agentcore.RunState) error {
    return nil
}

func (p permissiveResumePolicy) ShouldAppendInstruction(input agentcore.RunInput, state *agentcore.RunState) bool {
    return false
}

runtime := agentcore.New(agentcore.Config{
    Provider:     provider,
    Storage:      storage,
    ResumePolicy: permissiveResumePolicy{},
})
```

## 12. 错误包装

runtime 会对错误做稳定分类：

- `ProviderError`
- `ToolError`
- `HookError`
- `StorageError`
- `MemoryError`

推荐在 embedding host 中围绕这些错误类型做统一策略，而不是依赖字符串匹配。

需要特别注意两点运行语义：

- provider 失败后，即使后续观察型 hook 继续上报错误，返回值仍应保留原始 provider root cause
- transcript append / 持久化失败属于更高优先级问题，因为它影响 authority transcript 的可信度

如果你的 host 需要把部分 recoverable error 变成正式产品语义，例如：

- provider 暂时失败时先 yield，等待人工或外部重试
- 某类 builder error 返回一个稳定的 fallback `RunResult`
- 某些 recoverable error 必须保持 terminal，不能再被 hook 恢复

优先考虑扩展 `ErrorPolicy`，而不是继续把这些主路径语义全部压到 `OnError` hook 中。

## 13. 最小扩展路径

推荐按这个顺序扩展：

1. `mock + files`
2. 自定义 `Tool`
3. 自定义 `Hook`
4. 跑通 `yield -> resume`
5. 接入 `Memory`
6. 最后接真实 `Provider` 和自定义 `Storage`

如果你不只是想看单个接口，而是想直接从当前更推荐的组合起步，再继续看：

- [examples/recommended](../examples/recommended/main.go)
- [host-integration.md](./host-integration.md)
