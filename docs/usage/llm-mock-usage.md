# 测试用的假模型与假工具

单元测试用 `pkg/agentcore/mock`：脚本化的 Provider 与 Tool，不访问网络、结果确定。端到端（真实二进制 + 伪终端）测试用 `test/ptye2e/fakellm_test.go` 的假模型，它是一个 OpenAI 兼容的 SSE 假服务（用法与场景写法见同目录的 `smoke_helpers_test.go`、`vtscreen_test.go`）。

## Provider

按顺序返回脚本步骤，步骤用完后再调用会报错：

```go
import (
	"errors"

	"github.com/inhere/suga/pkg/agentcore/mock"
)

p := mock.NewProvider(
	mock.ToolCall("c1", "file_read", map[string]any{"path": "go.mod"}), // 第 1 次：调用工具
	mock.Final("done"),                                                  // 第 2 次：最终回答
)

// 失败与命名：多个 provider 组成回退池时用名字区分
primary := mock.NewProvider(mock.Error(errors.New("boom"))).WithName("primary")
backup := mock.NewProvider(mock.Final("fallback answer")).WithName("backup")
```

`mock.Stream(step, events...)` 在返回结果前先发出流式事件。Provider 会深拷贝脚本与结果，测试改动返回值不会影响后续步骤。

## Tool

```go
echo := mock.NewTool("echo") // 默认把参数原样作为结果返回
custom := mock.NewTool("lookup",
	mock.WithDescription("look something up"),
	mock.WithHandler(func(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
		return &api.ToolResult{Content: "42"}, nil
	}),
)
```

注册到 `pkg/agentcore/tools` 的注册表后交给 `agentcore.New(agentcore.Config{Provider: p, Tools: reg})` 运行。

旧版 llm 包内的 `MockProvider`（含手动步进模式 `WaitForCall`）已删除，原文档存于 [archive](../archive/agentcore-2026-10/llm-mock-usage.md)。
