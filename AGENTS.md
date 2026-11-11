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

## 三项目分工（xhen / gofer / suga）

按“谁替你做事、谁承载流程、谁执行”切分，主链 xhen → gofer → suga（用户 2026-11-02 定，2026-11-11 修订）。

- xhen：在 super-agent 之上、替你做事的产品——替代你的一部分工作与能力。意图与人设、原话约束、记忆、委托语义（边界、报告、账本、授权依据）、替你决策（决策台账、打扰策略）、个人日程与提醒、与你本人对话的通道（终端、桌面通知）。
- gofer：super-agent 式工作流的周边能力，xhen 可用之前由人直接使用，让人能轻松管理、跟进更多的事。来源接入（入站事件、轮询外部状态）、准入（确定性规则、权限上限、只记录模式）、工单与跟进（工作项 / plan / todo 订阅外部线程，后续消息接回原工作，跟到 PR 合并等结束条件）、执行编排（job、执行槽位与排队、跨机器 worker、定时、worktree 与交付）、回报（出站通知、回复到原线程）、注意力面（「等我」、今天、收件箱）、运行健康与审计、控制台。需要模型判断的环节（如是否接活）由 gofer 派 suga job 完成，gofer 不内置模型。
- suga：执行引擎。会话与回合、工具执行与审批回调（审批策略由宿主决定）、skills、子 agent、trace。
- 三条连接：
  - xhen → suga：进程内 SDK（`pkg/sugasdk` 等 suga 公开包），xhen 自己思考与对话的大脑（主题会话、轻量委托）；
  - xhen → gofer：提交 job / 工单并回收结果（HTTP/MCP），用于需要真正干活、耗时、换机器、要跟进或要看日志的工作；契约待定；
  - gofer → suga：agent 接入（CLI/ACP，`gofer job -a suga`）。
- 新功能先按上面归属落到对应项目；跨项目需要时扩展连接上的契约，不在另一侧复制一套。
- suga 可以独立使用：终端 chat 与 ACP（由 gofer、IDE 等拉起）都不依赖 xhen；xhen 经公开包嵌入、gofer 经 agent 接入都只是它的使用方。分工用于加新功能时考虑三个项目如何配合，不删减现有功能。

## 验证

- 单元测试：`go test -race ./...`；Windows 行为经 gofer exec job 在主机跑 `go test ./...`。
- 终端交互改动（chat 输入、提示符、确认交互、Ctrl-C、渲染）必须再跑伪终端端到端冒烟：`make smoke-chat`（`scripts/ptysmoke/`，说明见其 README）；新增交互行为时在 `chat_approval.py` 补对应检查或新建场景脚本。冒烟正逐批迁到 Go（`test/ptye2e`，`github.com/gookit/gopty/ptytest`，Linux 与 Windows ConPTY 同一套，假模型 `test/ptye2e/fakellm_test.go`），入口 `make smoke-go`；已迁移的场景以 Go 测试为准，新场景优先写成 Go 测试。
- 只有交互式 chat 才会出现的行为（人工审批、hook、空闲整理、记忆询问，以及它们写进 trace 的事件）不要留给人工验证：用伪终端冒烟脚本 + `fakellm.py` 自动跑，结束后用 `suga inspect trace --json` 检查记录（参考 `test/ptye2e/chat_observe_test.go`），并加进 `make smoke-chat`。新场景写完要做一次反向验证（临时去掉被测逻辑，确认对应检查会失败）。
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
