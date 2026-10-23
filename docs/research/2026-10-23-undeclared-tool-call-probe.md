# 探针：模型能否调用未声明工具（native 延迟加载 Q9 / 评审 F1）

> 日期：2026-10-23　关联：`docs/plans/2026-10-23-native-deferred-tools-plan.md` T1、`docs/design/2026-10-23-native-deferred-tools-design.md` Q9、`docs/review/2026-10-23-native-deferred-tools-design-review.md` F1
> 结论：**两项中只有第②项成立，Q9 选择「常驻代理工具 `tool_call`」**。

## 方法

- 探针程序 `tmp/probe/main.go`（不入库）：用 `internal/config` 的 `Load`/`ReadProviders`/`ResolveModel`/`BuildProviderWithProxy` 直接构造 provider，绕开工具层。
- 隔离 home：把用户 `~/.suga/config.yaml` 与 `.env` **复制**到 worktree 的 `tmp/probe-home/`，以 `SUGA_HOME` 指向它；真实 `~/.suga` 只读。跑完删除该目录与探针程序。
- provider（用户已有配置，`command-code`）：
  - `command-code/claude-haiku-5-5`（model `claude-haiku-5-5`）
  - `command-code/deepseek-v4.1-flash`（model `deepseek/deepseek-v4.1-flash`）
  - `command-code/glm-5.3-flash`（model `z-ai/glm-5.3-flash`）
- 每项每 provider 3 次，`MaxTokens=512`。
- ① 工具 schema **只出现在消息文本里**（`req.Tools` 为空）：文本给出 `save_note(title: string, body: string): 保存一条笔记` 并明确要求「请调用 save_note」。
- ② 历史里含**未声明工具**的 `tool_use`/`tool_calls`：`user → assistant(tool_use save_note) → tool(tool_result) → user "只回复 ok"`，`req.Tools` 为空，看 API 是否接受。

## 结果

| provider | ① 是否发出原生调用（3 次） | ② 历史能否接受（3 次） |
|---|---|---|
| claude-haiku-5-5 | 0/3（三次都是文本里的 JSON 片段） | 3/3 ok |
| deepseek-v4.1-flash | 0/3（`<tool_call>` 文本 / 说明文字 / DSML 标记文本） | 3/3 ok |
| glm-5.3-flash | 0/3（明确回答「tools 字段里没有该工具，无法调用」） | 3/3 ok |

原始响应片段（去密钥，`finish=final calls=[]` 表示 `GenerateResult.ToolCalls` 为空）：

```
## command-code/claude-haiku-5-5 (model=claude-haiku-5-5)
- item1#1 ok(3450ms) finish=final calls=[] text="```json {\"name\": \"save_note\", \"arguments\": {\"title\": \"probe\", \"body\": \"hello\"}} ```"
- item1#2 ok(2916ms) finish=final calls=[] text="```json {\"name\": \"save_note\", \"arguments\": {\"title\": \"probe\", \"body\": \"hello\"}} ```"
- item1#3 ok(2328ms) finish=final calls=[] text="```json {\"name\": \"save_note\", \"arguments\": {\"title\": \"probe\", \"body\": \"hello\"}} ```"
- item2#1 ok(1530ms) finish=final calls=[] text="ok"
- item2#2 ok(1127ms) finish=final calls=[] text="ok"
- item2#3 ok(1079ms) finish=final calls=[] text="ok"

## command-code/deepseek-v4.1-flash (model=deepseek/deepseek-v4.1-flash)
- item1#1 ok(2281ms) finish=final calls=[] text="<tool_call> {\"name\": \"save_note\", \"arguments\": {\"title\": \"probe\", \"body\": \"hello\"}} </tool_call>"
- item1#2 ok(2186ms) finish=final calls=[] text="调用 `save_note`，参数为 `title=\"probe\"`、`body=\"hello\"`。"
- item1#3 ok(1994ms) finish=final calls=[] text="<｜｜DSML｜｜ calls> <｜｜DSML｜｜ invoke name=\"save_note\"> ..."
- item2#1 ok(1649ms) finish=final calls=[] text="ok"
- item2#2 ok(1765ms) finish=final calls=[] text="ok"
- item2#3 ok(1643ms) finish=final calls=[] text="ok"

## command-code/glm-5.3-flash (model=z-ai/glm-5.3-flash)
- item1#1 ok(7657ms) finish=final calls=[] text="抱歉，我当前的工具列表中并没有实际注册的 save_note 函数——它只是以文本形式描述，并非真正可调用的工具。..."
- item1#2 ok(5751ms) finish=final calls=[] text="我的实际可用工具列表中并未注册 `save_note`（请求的 tools 字段中没有该函数的定义）。..."
- item1#3 ok(7308ms) finish=final calls=[] text="我无法执行这个调用。虽然你在说明中描述了 `save_note(title, body)` 工具，但本次会话中并没有实际挂载任何可调用的工具，..."
- item2#1 ok(3861ms) finish=final calls=[] text="ok"
- item2#2 ok(4392ms) finish=final calls=[] text="ok"
- item2#3 ok(3951ms) finish=final calls=[] text="ok"
```

## 结论与对实施的影响

1. **不能依赖模型调用未声明工具**：三个 provider 9 次尝试，`ToolCalls` 全为空。deepseek 把调用写成文本标记，glm 直接拒绝——都和评审 F1 的预判一致。因此设计结论 5 的「直接调用」分支不成立。
2. **未声明工具的历史可接受**：三个 provider 均接受含未声明工具 `tool_use` 的历史（②3/3），说明段内走过的调用记录在换段/入表之前不会让后续请求报错——代理工具的调用记录可以安全留在历史里。
3. **Q9 选择：常驻代理工具 `tool_call {name, arguments}`**（设计与计划都无法成立时的默认分支）。它意味着：段内调用一律走 `tool_call`，执行层展开成真实工具调用，审批、只读模式、PreToolUse hook（`tools.CallRewriter`）、taint、trace 按真实工具名走；到段边界再把已加载工具原生入表。
4. 附带观察：`deepseek` 与 `glm` 会把「文本里给了 schema、tools 里没有」理解为「工具不存在」，**所以清单文案不能只写名字，必须让模型明确知道要用 `tool_call` 调**——已在 T3/T4 的实现里写进段上下文清单与 `tool_search` 结果文本。
