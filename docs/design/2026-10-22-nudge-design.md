# 收工检查（agents.nudge）设计

日期：2026-10-22
状态：已实施
参考：qybaihe/mu 的 `turn.completion` / `turn.continue`（只借规则部分，不调模型）

## 问题

模型常在“看起来做完了”时停下：改了文件却没跑验证、计划里还有未完成项、或者答完一句“接下来我去跑测试”就结束回合。调用方（gofer job、CI 封装）只能看到回合正常结束，事后才发现交付不完整。

本功能不引入额外的模型调用，只用规则检查回合留下的证据，并在各宿主上按各自的语义处理。

## 规则（`internal/personal/nudge.go`）

`CheckNudge(calls, plan, finalText, completed)` 是纯函数，输入是回合内按顺序的工具调用（名称、是否成功、目标路径）、会话最新计划、最后一条助手消息的文本、以及“该消息是否无工具调用”。

- **N1 未验证或未完成**
  - 本回合最后一次**成功**的写入类工具（`file_edit` / `file_write` / `file_remove`）之后没有任何 `shell_exec` 调用（失败的 `shell_exec` 也算跑过：尝试过即可，结果由模型判断）；或
  - 会话计划（`update_plan` 最新一条 `plan` 条目）里还有 `pending` / `in_progress` 的项。
  - 两个条件都命中时合并为一条 finding，证据里同时列出文件与未完成项。
- **N2 说了要做却停下**
  - 回合以纯文本结束（最后一条助手消息无工具调用），且其**最后一段**（最多末尾 300 字符）命中“承诺继续”的正则表（`nudgePromisePatterns`：`接下来我(会|将|来|去|先|再)`、`让我(先|再)?(运行|执行|跑|检查|…)`、`下一步(我)?(将|会|来|要)`、`I'll/will now run|check|fix…`、`Next, I'll…`、`Let me run|check|fix…`）；
  - **以问句结尾、征求同意的不算**（`?`/`？`/`吗`/`呢` 结尾）：是否继续由人决定；
  - 只搜最后一段：中间段落出现承诺句、末尾是总结时不命中。

`Runner.NudgeFindings` 从 store 取本回合的 `tool` 事件与会话最新 `plan` 条目，读配置 `agents.nudge`（`on`/`off`，默认 `on`，`off` 时什么都不做），并跳过失败 / yield / 取消 / 收尾（`WrapUp`）的回合。

## 宿主处理

- **run**（`internal/cli/nudge.go`）：命中时推进一条 `[收工检查]` 注入回合，逐条列出规则与证据，要求“继续完成，或说明为什么不需要继续 / 为什么停下，不要重复已完成的工作”。N2 每次 `run` 最多推回 2 次、N1 最多 1 次；用了 `--verify` 时跳过 N1（verify 已覆盖交付检查）。文本模式在 stderr 打一行 `nudge: N2 结尾说…`（stdout 仍只有答复），stream-json 新增 `{"type":"nudge","rule":…,"action":"pushed","evidence":…}` 行。
- **chat**（`internal/chat/chat_nudge.go`）：只提示不推回，回合结束后显示一行灰色提示 `⚠ 最后修改 a.txt 之后没有运行 shell_exec；结尾说接下来我去跑测试`，绝不自动开回合。
- **ACP**（`internal/acpserver/server.go`）：只写 trace，不推回不提示；事件在 runtime 关闭（库仍打开）之前写。
- **trace**：`nudge` 事件（`store.EventNudge`），name `turn`，attrs `rule`（N1/N2）、`action`（`pushed` / `hinted` / `recorded`）、`evidence`（证据摘要）。

## 配置

`agents.nudge`：`on` / `off`，默认 `on`（`internal/config/agents.go` 的 `ReadNudgeSetting`），已注册进配置表（`settings_registry.go`，可 `suga config set agents.nudge off`）。

## 测试

- `internal/personal/nudge_test.go`：N1 各条件（改后跑过命令不命中、改后没跑命中、命令必须在最后一次写之后、失败的命令也算、失败写入不算、计划未完成命中、两项合并）、N2 正则表正反例（中英文承诺句命中、问句不命中、中间承诺 + 末尾总结不命中、无工具调用才算、只搜末尾 300 字符）。
- `internal/cli/nudge_test.go`：推回次数上限（N2 两次、N1 一次）、`--verify` 跳过 N1、收尾回合结束循环；端到端（httptest 假 provider）第一轮“接下来我去跑测试”被推回、第二轮完成，stream-json 与 trace 有 `nudge`；推回 2 次后不再推；`agents.nudge off` 不推回。
- `scripts/ptysmoke/chat_nudge.py`（Linux 容器）：fakellm `--mode nudge` 改文件后直接收尾，断言出现灰色提示行、假模型只收到本回合的两次请求（没有自动开新回合）、trace 里 N1/N2 都是 `action=hinted`；已加入 `make smoke-chat`。
