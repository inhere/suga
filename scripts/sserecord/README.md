# sserecord — 真实 provider 的 SSE 夹具录制

把真实网关的 **原始 SSE 响应体** 录下来，作为 `pkg/agentcore/llm/testdata/sse/` 的
回放测试夹具（`sse_replay_test.go` 用 httptest 原样回放并断言解析结果）。
每个模型录 3 个场景：`text`（纯文本回答）、`tool1`（第一步：模型发起工具调用）、
`tool2`（第二步：带工具结果再要最终回答）。每个场景产出两个文件：

- `<provider>-<model>-<case>.sse` — 原始响应体（逐字节保存）
- `<provider>-<model>-<case>.json` — 对应的请求体

安全约定（录前须知）：

- 只落 **响应体与请求体**，不落任何 HTTP 头 —— Authorization / x-api-key /
  anthropic-version 等头不会进入夹具。
- 密钥只从 `$SUGA_HOME/config.yaml + .env` 读入内存用于发请求，**不写盘**。
- 对话内容全部是固定测试文本（`list_files` 假工具、固定目录名），无用户数据。

## 用法

```sh
go run ./scripts/sserecord \
  --models command-code/deepseek-v4.1-flash,command-code/glm-5.3-flash,command-code/claude-haiku-5-5,openai-codex/gpt-6-luna
```

- `--config-from`：读配置的 SUGA_HOME（默认 `$SUGA_HOME`，再退到 `~/.suga`），
  只读不写。
- `--out`：夹具目录（默认 `pkg/agentcore/llm/testdata/sse`）。
- `--models`：要录的 `<provider>/<model>` 列表（config.yaml 里的 short key），
  省略则报错退出。
- 需要真实网络与有效密钥；不需要依赖产品代码路径。

## 已知行为

- `command-code/claude-haiku-5-5`（Anthropic messages 流）：budget 型 thinking
  （`{"type":"enabled"}`）被该网关/模型拒绝，录制器自动改发 adaptive 型并落到
  `high` effort，同时用"先在 thinking 块里想清楚再回答/调工具"的提示词把模型
  逼出 thinking 块；仍失败时会打印 `retrying without thinking` 并录到无 thinking
  的流。
- 单个夹具要求 <64KB；目前最大 ~16KB。若某响应超限，缩短提示词里的思考要求后重录。
