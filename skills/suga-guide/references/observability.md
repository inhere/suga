# 观测参考：trace、stats、stream-json 与日志

会话时间线、循环分析、用量与成本统计、`run --output-format stream-json` 的用量事件，以及进程日志。面向 Gofer 的用量上报另见 `references/gofer.md`。

## 命令

```text
suga inspect trace <session> [--turn <id>] [--json]                                          # 时间线：模型调用、工具、拒绝、计划
suga sessions [关键词] [-n 10] [--since 7d] [--until 2026-10-01] [--cwd .] [--agents] [--json]  # 会话列表，最新在前；关键词匹配标题、id、目录、模型或对话内容；--agents 连子会话一起列出
suga inspect trace <session> --analyze [--json]                                              # 循环分析：步数、并行调用、重复读、重复命令、首次通过后的测试、子 agent
suga inspect trace <session> --web [--addr 127.0.0.1:0]                                      # 本机网页：时间线（后台任务横条、任务通知标注、子 agent 行及其子会话 trace 命令）、详情、上下文组成、分析、本机环境（Ctrl-C 结束）
suga inspect stats [--session <id>] [--since 7d] [--json] # 调用次数、耗时、token、缓存命中
suga inspect export <session> [--redact] [-o <file>]      # 以 jsonl 导出会话内容与事件
suga data gc --older-than 30d [--apply]                   # 列出（默认）或删除长期未活动的会话
suga data gc --retention [--apply]                        # 按来源套用保留期（chat 180 天 / run+ACP 30 天，默认只报告）
suga data usage [--json] [--top N]                        # 只读：库文件/空闲页/WAL、entries 与 events 的字节构成、全文索引、会话目录
suga ps                                                   # 正在运行的 chat 进程（含短名称）
suga inspect manifest [--json] [--model <p/m>]            # 模型、工具、MCP、skills、记忆后端、输出上限
```

- `trace --web` 以只读方式打开会话库：不创建文件、不跑迁移，库不存在或未初始化时直接报错；页面上的“自动刷新”开关默认关闭，打开后只拉取新增步骤（回合进行中每 5 秒，空闲时每 15 秒检查新回合）。

## `suga inspect trace` 时间线里的各类行

- **工具行**：退出码（shell）、进入上下文的字节数、转存标记与脱敏后的参数摘要；编辑的增删行数（`+新增/-删除`）；问过人时显示 `approval=user(wait=3.2s)`。
- **`agent` 行**（子 agent 调用）：它运行的会话、类型、步数（连带子 agent 自己的工具调用数）、token 与成本、结束原因，如 `sub=<会话> type=explore steps=12 calls=5 in=85k(cache 60k) out=3k cost=$0.0123 stop=final`。后台子 agent 的 task 行显示 `agent steps=N calls=M`。
- **模型行**：这次回复发出了几个工具调用（`calls=N`）、被输出上限截断时的 `truncated`、请求重试时的 `retries=N(reason, waited Xs)`、命中缓存的输入 token（`cached=N`），以及请求没有延续上一次时的原因和重新发送的早先内容量（`prefix=fold(rebuilt=12k)`，原因有 fold、segment、system、tools、rewrite）。
- **`task` 行**（后台任务结束）：任务的种类；watch 带 `runs=N changes=M` 或最近一次状态，Stop hook 显示它的摘要，agent 任务显示子 agent 跑的步数。
- **`context` 行**（每回合一行）：该回合第一次请求由哪些部分组成（系统提示词、工具及其数量与 MCP 占比、环境、规则、记忆索引、skills、延迟工具、交接摘要、历史、hook 注入、本轮输入的字符数）以及估算 token 与窗口。
- **`hook` 行**（每次 hook 运行）：事件、耗时、是否 block、注入的上下文。
- **回合行**：输入长度及是否为注入而非人工输入。
- **`turn_context` 行**（每回合）：模型、模式、工具集哈希、规则与记忆索引哈希、是否已污染。
- **分段行**：`segment=#N reason=... tokens=前->后`。
- **`schedule` 事件**：chat 空闲整理：fired、done、cancelled、skipped。
- **`session recovered` 行**：被死进程留在 `running` 的会话被本次运行恢复为 `open`（`turn=` 是那回合未结束的 id，已记为 `cancelled`）；`chat --resume` 与 `run --resume` 靠它才能开始新回合。
- **`local_command` 行**：一次在提示符处、回合之外执行的 `!`/`!!` 命令（`exit=`、输出的 `bytes=`、结果会随下一条消息进入模型时的 `model=next-message`、被终止时的 `cancelled`/`timeout`），命令本身放在行尾的方括号里。
- 其它：`model_switch`（`:model` 切换）、`fold`（折叠点前移）、`session_cwd_changed`（换目录续接）、`danger=…`（危险删除拦截原因）。

## `suga inspect trace --analyze`

从同一组事件得出：

- 步数与并行步数、各工具次数与失败。
- 读了多次的文件和写完又回读的次数、每个文件的编辑次数、重复执行的相同命令。
- 测试运行次数及首次通过之后又跑了几次（输出报告了失败的不算通过，不论退出码）。
- 各阶段的模型调用数（首次写入前的探索、首次写入到测试首次通过、通过之后）。
- 编辑步数及其中只改一处的步数；退出码为 0 但有 stderr 输出的 shell 次数。
- 未命中缓存的输入按来源拆分：上次请求之后新增的内容、前缀断开后重新发送的早先内容（按原因）、前缀本可命中但 provider 没有命中的部分（按请求大小估算）。
- 转存/折叠/压缩/模型重试次数、最慢的几次模型调用。
- 本会话子 agent 的开销（调用次数、步数、工具调用数、token、成本与耗时）：`agent_calls`、`agent_steps`、`agent_tool_calls`、`agent_input_tokens`、`agent_cache_read_tokens`、`agent_output_tokens`、`agent_cost_usd`、`agent_ms`；`agent_stops` 记录各结束原因的分布。
- 有后台任务时还会补充：任务运行期间循环用于模型与工具调用的时间（`background_overlap_ms`，即省下的等待，只计后台命令，不含监听与 Stop hook；重叠的前台调用只算一次）、`task wait` 的次数与阻塞时长，以及其中在任务启动后立刻 wait 的次数（`immediate_waits`，把后台当前台用）。
- 按种类补充：watch 任务数及其运行与变化次数（`watch_tasks`、`watch_runs`、`watch_changes`），以及后台 Stop hook 的运行次数（`stop_hooks`）。

## `suga inspect stats`

- 显示各模型成本（provider 返回，或按配置的 `price` 计算）。
- 加 `--json` 还有工具输出字节（`raw_bytes`、`inline_bytes`、`spilled`）。
- `--session` 的会话累计 = 父会话加它的全部子会话。

## stream-json 用量、重试与收尾

- `run --output-format stream-json` 在每次模型调用后输出一行 `usage`，为本次 run 的累计值：`input_tokens`（不含缓存）、`output_tokens`、`cache_read_tokens`、`cache_write_tokens`、`total_tokens`、`cost_usd`、`tool_calls`。`turn_end` 与 `result` 带同名 `usage`，调用方可在运行中途做预算控制。
- 模型请求因瞬时故障重试时会输出一行 `retry`（`reason`、`attempt`、`wait_ms`、`restart`）；`restart: true` 表示这条消息此前输出的 `message_delta` 文本作废，会从头重新流式输出。chat 把这种重试显示为一行 `↻` 提示，`run` 打到 stderr，ACP 作为 thought 块下发。
- ACP 在同一时机发 `usage_update`（说明见仓库 `docs/usage/gofer-personal-agent.md` 的“ACP 用量上报”）。
- 因上限收尾而非正常作答的回合会在最后的 `result` 行上带 `wrap_up`（到步数上限时为 `"max_steps"`，正常答复不带该字段）；文本模式把该原因打到 stderr，短答复因此不会被当成完成，也不会被当成超时。
- `result` 还能带 `context`（`used`、`window`、`share`），文本模式的收尾行则显示 `ctx N%`：两者都是本回合最后一次模型请求的输入 token 占模型窗口的整数百分比（含缓存 token，超窗口时可能大于 100）；窗口未知或本回合没有模型调用时省略 `context`、收尾行也不显示。

## 进程日志

- 进程诊断日志只写 `$SUGA_HOME/logs/suga.log`（0600、各次运行都追加、不轮转），不再出现在终端：需要让人看到的信息由运行本身打印（会话 id、模型、重试、收尾原因），而 Gofer 把 stdout 当协议读，自由文本的日志行只会让终端或 job 日志变吵。
- `SUGA_DEBUG=1` 才会打开 debug 记录并往 stderr 再写一份（在 Gofer 的 agent 里就是 `env: {SUGA_DEBUG: "1"}`）。
- 日志文件打不开时无论如何都回退到 stderr。
