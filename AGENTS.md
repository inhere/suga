# AGENTS for AI

- 请优先使用中文回复和响应，除非明确要求英文回复
- 多阶段任务，需要在每个阶段完成后提交 Git；进度用 `gofer plan` / `gofer issue` 跟踪

## 当前项目入口

- 个人 Agent 的构建与命令以 `README.md` 为准；Gofer CLI/ACP 接入见 `docs/usage/gofer-personal-agent.md`。
- 设计与实施依据：内置工具与 skills 见 `docs/design/2026-10-10-dev-tools-and-skills-design.md` 与 `docs/plans/2026-10-10-dev-tools-and-skills-plan.md`；配置分层与 chat 见 `docs/design/2026-10-09-config-layering-chat-design.md` 与对应计划（2026-10-08 的 core-capabilities 与 2026-10-06 的 personal-agent-redesign 设计/计划保留为历史）；配置格式见 `docs/usage/llm-config-v0.md`；旧控制面代码见 `v0.1.0` tag。
- 分层：`pkg/agentcore`（内核 + llm/tools/native/mcp/skills 能力层）不得依赖 `internal/*`，`pkg/agentcore/layering_test.go` 守住；依据见 `docs/design/2026-10-10-agentcore-layering-design.md` 与对应计划，删除与迁移记录见 `docs/archive/removed-2026-10.md`。
- 状态、记忆与可扩展性（记忆、历史检索、输出治理、manifest、评测）：设计 `docs/design/2026-10-10-state-memory-extensibility-design.md`，计划 `docs/plans/2026-10-11-state-memory-extensibility-plan.md`；用法见 `README.md` 的“Memory”一节。
- 会话分段与上下文投影（稳定系统提示词、段上下文消息、分段交接、空闲整理、用量上报、`pkg/agentcore/schedule`）：设计 `docs/design/2026-10-11-segments-projection-design.md`，计划 `docs/plans/2026-10-11-segments-projection-plan.md`；用法见 `README.md` 的“Sessions”一节。
- 任务与进度用 `gofer issue`（数据在 `.gofer/tracker`）和 `gofer plan` 跟踪，经验用 `gofer memory`；阶段结果以 issue/plan 状态和实际验证证据为准。

## 验证

- 单元测试：`go test -race ./...`；Windows 行为经 gofer exec job 在主机跑 `go test ./...`。
- 终端交互改动（chat 输入、提示符、确认交互、Ctrl-C、渲染）必须再跑伪终端端到端冒烟：`make smoke-chat`（`scripts/ptysmoke/`，说明见其 README）；新增交互行为时在 `chat_approval.py` 补对应检查或新建场景脚本。
- 只有交互式 chat 才会出现的行为（人工审批、hook、空闲整理、记忆询问，以及它们写进 trace 的事件）不要留给人工验证：用伪终端冒烟脚本 + `fakellm.py` 自动跑，结束后用 `suga inspect trace --json` 检查记录（参考 `chat_observe.py`），并加进 `make smoke-chat`。新场景写完要做一次反向验证（临时去掉被测逻辑，确认对应检查会失败）。
- 涉及 provider 协议的改动，至少用一个真实 provider 跑一轮带工具调用的对话；做不到时在交付说明中写明未经真实模型验证。
- ACP 行为的真实模型验证用 `scripts/acpprobe/acp_probe.py`（最小 ACP 客户端：隔离 SUGA_HOME、读真实 config.yaml 与 .env 但不复制文件、自动允许权限、多个 prompt 之间可等待，结束后列出 trace 里的任务与工具事件）；Windows 行为经 gofer exec job 在主机上跑。

## 项目编码规范

- 关键的方法或逻辑点需要添加注释说明
- 单个go文件最大行数不能操作1000行，超过时需要按情况进行拆分

### git worktree 开发规范

- 使用 git worktree 进行开发时，在 `.worktrees` 目录下创建分支

### 单元测试编写

- 使用 `github.com/gookit/goutil/x/assert` 断言结果
- 同一个方法的多个用例使用 `t.Run()` 包裹

require 断言结果的写法：

```go
Require(t, assert.Eq(t, 1, res.ID))

// Standard assertion
assert.Eq(t, expected, actual)
```
