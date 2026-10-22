# suga 完整配置参考

本文是 `$SUGA_HOME/config.yaml` 的完整键表（默认值、取值、作用范围），外加环境变量、目录布局与常用命令。键名与默认值取自当前代码；样例文件另有仓库里的 `config/config.yaml`（带注释的字段样例）。

## 文件位置与优先级

- suga **只加载** `$SUGA_HOME/config.yaml` 与 `$SUGA_HOME/.env`，`SUGA_HOME` 默认 `~/.suga`。当前工作目录的 `config.yaml` / `.env` 不会被读取。
- `SUGA_CONFIG` 指定唯一的配置文件（此时不读 `$SUGA_HOME/config.yaml`）。
- `.env` 里的值会进入进程环境，供配置里的 `${VAR}` 展开；`SUGA_HOME`、`SUGA_CONFIG` 例外，只认进程环境里最初的值（避免 `.env` 改写数据根）。
- 主模型优先级：`--model` > `SUGA_MODEL` > `model`。
- 值类型写错通常是**启动报错**，而不是回退默认值（如 `tools.allow_write`、`tools.approval`、`context.fold_keep`、`memory.write`、`retry.*`、`agents.*`、`tasks.*`）。

## 模型与 provider

| 键 | 默认 | 含义 |
|---|---|---|
| `model` | 无（必填，或用 SUGA_MODEL/--model） | 主模型，`<provider>/<model>` |
| `compact_model` | 不设（用当前回合的模型） | 生成分段摘要（自动压缩、`:compact`、空闲整理）的模型，引用规则同 `model`，裸 id 用主模型的 provider |
| `fallback_models` | 不设 | 主模型失败后按顺序尝试的模型列表（会先重试 `retry` 次） |
| `providers.<id>` | 内置预设 openai / anthropic / deepseek | provider 定义，逐字段覆盖同名预设；其它 id 必须写 `type` |
| `providers.<id>.type` | 预设自带；其它 id 必填 | `openai`（`/chat/completions`）、`openai-responses`（Responses API `/responses`）、`anthropic`（`/v1/messages`） |
| `providers.<id>.compat` | 按 base_url/模型 id 自动匹配 | 网关适配名列表，自动匹配不上时在此追加 |
| `providers.<id>.base_url` | 预设自带 | 地址，支持 `${VAR}` |
| `providers.<id>.api_key` | 预设读对应环境变量 | 密钥，支持 `${VAR}` |
| `providers.<id>.headers` | `User-Agent: suga` | 附加请求头，值支持 `${VAR}` 与 `{{session_id}}` |
| `providers.<id>.prompt_cache` | `false` | 仅 `anthropic` 类型：加 `cache_control` 断点（system 变块数组），部分网关不接受 |
| `providers.<id>.context_window` | 按模型名推断 | 该 provider 所有模型的默认上下文窗口 |
| `providers.<id>.thinking` | 不设（不发送） | 思考档位（provider 级默认） |
| `providers.<id>.models.<model>.context_window` | 按模型名推断 | 该模型的上下文窗口（优先级：模型级 > provider 级 > 名字推断） |
| `providers.<id>.models.<model>.model_id` | 不设（发送键名） | 请求里实际发送的模型 id，便于用短名引用长 id |
| `providers.<id>.models.<model>.type`、`base_url` | 继承 provider | 该模型自己的协议与地址（同一网关的 Claude 模型可单独走 Anthropic 端点） |
| `providers.<id>.models.<model>.price` | 不设 | `{input, output, cache_read, cache_write}`，每百万 token 的美元价；provider 不返回成本时 `suga inspect stats` 用它算 |
| `providers.<id>.models.<model>.thinking` | 不设 | 该模型的思考档位 |

思考档位取值：`off`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`auto`。优先级 `--thinking`（只作用于主模型）> 模型级 > provider 级；不设或 `auto` 不发送。细节与网关示例见 `references/providers.md`。

## 重试与上下文

| 键 | 默认 | 含义 |
|---|---|---|
| `retry.max_attempts` | `6` | 一次模型请求的总尝试次数（含首次）；`1` 关闭重试 |
| `retry.base_delay` | `1s` | 退避起始等待（每次翻倍，±20% 抖动） |
| `retry.max_delay` | `60s` | 退避等待上限（服务端 `Retry-After` 同样受此限制） |
| `retry.reset_after` | `10m` | 连续这么久没有失败就把等待档位回到 `base_delay` |
| `retry.max_elapsed` | `3m` | 一次请求**全部重试的总时长上限**：到点不再重试、直接失败（配了 `fallback_models` 则交给下一个模型）；`off` 不限制 |
| `context.fold_every` | `0`（关） | 每 N 步前移一次历史折叠点（如 `10`） |
| `context.fold_keep` | `5`（≥1） | 最近多少步保持完整 |
| `context.fold_at` | `0`（关；0..1，建议 `0.5`） | 折叠加前估算体积超过压缩阈值的这个比例时，本段开始折叠（之后本段保持）；`0` 时只按 `context.fold_every` 从第一步折叠 |
| `context.idle_after` | `15m`（`0` 关） | 仅 chat：空闲这么久且上下文达到阈值比例时在后台开启新段 |
| `context.idle_min_ratio` | `0.65` | 空闲整理要求的上下文占比（相对阈值 `窗口-预留`；0..1） |

- 重试覆盖所有 provider，触发条件：网络错误、HTTP 408/429/5xx、响应流中断。
- 历史折叠只改发给模型的请求（去掉较早步骤的思考内容、把超过 1000 字符的字符串参数换成说明），存储的历史完整；主模型为 `anthropic` 类型时不生效（签名绑定请求前缀）。

## 请求超时

| 键 | 默认 | 含义 |
|---|---|---|
| `timeouts.request` | `120s` | 单次**非流式**模型请求的总超时（连接、TLS、响应头与读正文）；`off`/`0` 不限制 |
| `timeouts.response_header` | `60s` | **流式**请求等待响应头的时间；`off`/`0` 不限制 |
| `timeouts.stream_idle` | 普通 `120s` / 思考模型 `300s` | **流式正文**两次收到数据（文本、工具参数、心跳都算）之间的最长静默；超时取消本次请求并按可重试错误重新请求（`retry` 事件写明 `stream idle for Ns`）；`off`/`0` 关闭 |
| `providers.<id>.timeouts.request` | 继承 `timeouts.request` | 该 provider 的请求总超时，覆盖全局 |
| `providers.<id>.timeouts.response_header` | 继承 `timeouts.response_header` | 该 provider 的响应头等待，覆盖全局 |
| `providers.<id>.timeouts.stream_idle` | 继承 `timeouts.stream_idle` | 该 provider 的流空闲上限，覆盖全局 |

超时后由 `retry` 决定是否重试：一次请求最多 `retry.max_attempts` 次尝试，每次都可能等满上面的超时，所以一次卡住的时长约为“超时 × 尝试次数 + 退避”。默认值下（响应头 60s、`reset_after` 10m）连续超时会正常退避（1s→2s→4s→8s→16s），最坏约 6×60s + 31s ≈ 6.5 分钟。注意两个值的相对大小：如果某个超时大于 `retry.reset_after`，退避档位会在每次尝试前被重置（表现为每次重试只等约 `base_delay`）。

`retry.max_elapsed` 是这套组合的总闸：不管尝试次数用没用完，一次请求的重试总耗时到点就结束（默认 3 分钟），进程随即收尾退出、释放会话锁，而不是继续持有几分钟。provider 持续连不上、网关一直 5xx 时最明显；配了 `fallback_models` 时由下一个模型接手。

流式正文没有整体超时（长回答可以流很久），但 `timeouts.stream_idle` 守着“两次收到数据之间”的静默：超过就取消这次 HTTP 请求，按可重试错误重新流式请求（已流出的文本作废、重来）。普通请求默认 `120s`，请求里带思考参数（`thinking` / `reasoning_effort` / `reasoning`）时默认 `300s`，因为这些请求在首个可见增量前可能只想不说。

## 其它顶层键

| 键 | 默认 | 含义 |
|---|---|---|
| `workspace` | `""`（当前工作目录） | 文件与 shell 工具的工作区根 |
| `rules.file` | `AGENTS.md` | 每级目录读取的项目规则文件名（只接受文件名，不能带路径），如 `CLAUDE.md`；`$SUGA_HOME/AGENTS.md` 始终读取 |
| `max_steps` | `240` | 一次任务（一轮，不是整个会话）的最大 ReAct 循环步数；可被环境变量 `SUGA_MAX_STEPS` 与 `run --max-steps N` 覆盖（子 agent 用 `agents.max_steps`），到顶时要求模型收尾并在答复开头写明是步数上限 |
| `serpapi_key` | `""` | 配置后注册 `web_search`（支持 `${VAR}`） |
| `run.deadline_env` | `""`（关闭） | 环境变量名，值是本次运行的截止时间（RFC3339）。gofer 时限 job 注入 `GOFER_JOB_DEADLINE`，配成它即可；每次请求模型前在消息末尾临时附当前时间、截止时间与剩余时间（剩余 <10% 或 <5 分钟提示尽快收尾，已过截止说明超时）。不写库、不进稳定前缀；变量缺失或解析失败不报错只记调试日志。`run`、`chat`、ACP 及子 agent 通用 |
| `transcripts` | 开 | 会话记录（transcript）写到 `$SUGA_HOME/transcripts`；`false` 或 `"off"` 关闭（键值接受布尔或字符串） |

## `chat.*`（仅 chat 的终端显示）

| 键 | 默认 | 含义 |
|---|---|---|
| `chat.agent_progress` | `summary` | 子 agent 进度显示：`summary` 在等待状态行按 description 汇总调用次数、结束时显示耗时；`full` 显示每次工具调用；`off` 不显示进度（agent 工具行与结果仍显示） |
| `chat.spinner` | `true` | 等待模型输出时显示“思考中… Ns”，正文/工具调用/审批问题之前清除；`chat.steering` 开启时回合中不显示（输入行常驻，避免与它争同一行；工具调用行、回答与输入行本身仍能看出在干活） |
| `chat.steering` | `true` | 回合进行中输入行保持可用：提示符为 `↳ `，回车把内容插进当前回合（模型下一次请求前送达；模型已答完则作为下一回合），Esc 或一次 Ctrl-C 只中止本轮（未送达的插话回到输入行），3 秒内两次 Ctrl-C 退出 chat。按键以字节到达，不依赖控制台的 Ctrl-C 事件，因此在 ConPTY（Zed、Windows Terminal）里同样有效；仅在输入是终端时生效，关闭后回合中恢复 cooked 模式，`Ctrl-C` 是 SIGINT、一次即退出 |
| `chat.highlight_input` | `true` | 提示符提交的那行（含粘贴的多行）与外部投递的消息（`suga send`、Stop hook 回复、任务通知）整行加低饱和背景，与模型回答区分；两者背景色不同 |
| `chat.notify` | `auto` | 长回合结束或审批等待时通知：`auto`（Windows Terminal 用 OSC 9，否则 BEL）、`bell`、`osc9`、`off` |
| `chat.notify_after` | `60s` | 回合达到此时长后结束时通知 |
| `chat.terminal_title` | `true` | 把终端窗口/标签页标题设为 `suga · <短名称> · <会话标题>`（会话标题即续接时已有的那条，新会话取第一条消息，截到 30 字）：启动时先保存终端原有标题（`ESC[22;0t`），退出时置空并恢复（`ESC[23;0t`，置空是不支持标题栈的终端的兜底）。标题里的控制字符（含 ESC/BEL/C1）会被剔除，避免注入转义序列 |
| `chat.history` | `true` | 本地 chat 输入历史保存在 `$SUGA_HOME/history.jsonl`；只记录键盘输入并先脱敏（`!` 本地命令也记录，投递消息不记录） |
| `chat.history_size` | `1000` | 输入历史最多保存的条数 |
| `chat.diff` | `true` | 文件改动预览；`false` 关闭（`:diff` / `:diff N` 查看完整改动） |
| `chat.diff_lines` | `20` | 改动预览最多显示的行数（超出时提示用 `:diff` 查看全部） |
| `chat.code_highlight` | `true` | 代码块按围栏语言逐行着色（注译、字符串、数字、关键字四类，16 色 ANSI）；`false`、未知语言、非终端或 `NO_COLOR` 时按原文显示 |
| `chat.code_max_lines` | `40` | 单个代码块最多显示的行数，超出部分折叠并提示 `… 还有 N 行（:code 查看全部）`；`0` 不折叠 |
| `chat.theme` | `auto` | chat 里 Markdown 与代码着色用的调色板：`auto` 按环境变量 `COLORFGBG` 最后一段的背景色判断（`7` 或 `15` 即浅色背景 → light，其余或未设置 → dark），`dark`/`light` 直接指定；环境变量 `SUGA_THEME`（dark/light）优先于配置，配置值非法时启动报错 |
| `chat.ask_outside_dirs` | `true` | 文件工具越出工作区、只读根与附加目录时询问 `[r] 只读 [w] 读写 [n] 拒绝`，答应后把目录写进会话；关闭时越界调用直接拒绝 |
| `chat.win32_input_mode` | `auto` | Windows Terminal 下原生的 `Ctrl+Enter` 换行：`auto` 只在 `WT_SESSION` 存在时请求 win32-input-mode（`ESC[?9001h`），`on` 总是请求，`off` 关闭（仍可用 `Ctrl+J`/`Alt+Enter` 换行）。退出行编辑、Ctrl-C、退出 chat 与 `!` 本地命令前后都恢复 `ESC[?9001l`；终端不认识该序列时按键仍按原字节解析 |

上表中的开关键（`chat.markdown`、`chat.spinner`、`chat.steering`、`chat.highlight_input`、`chat.terminal_title`、`chat.history`、`chat.diff`、`chat.ask_outside_dirs`、`chat.code_highlight`）都接受布尔或字符串（`"off"` / `"false"` 关闭）。`chat.agent_progress` 不是开关，只接受 `summary`、`full`、`off`；`chat.notify` 接受 `auto`、`bell`、`osc9`、`off`，`chat.win32_input_mode` 接受 `auto`、`on`、`off`。`chat.markdown`、`chat.spinner`、`chat.highlight_input`、`chat.terminal_title`、`chat.code_highlight` 只在彩色终端生效：非终端、重定向输出或 `NO_COLOR` 时按原文显示，不加任何转义。`chat.steering` 只要求输入是终端。

## `tools.*`

| 键 | 默认 | 含义 / 作用范围 |
|---|---|---|
| `tools.allow_write` | `false` | **仅 chat**：启用写文件与命令工具。`run` 需 `--allow-write`；ACP 看会话 mode；`--read-only` 优先 |
| `tools.approval` | `ask` | **仅 chat**：`ask` 时每个非只读、非 MCP 工具调用前在终端询问；`auto` 不问。无终端时需确认的工具被拒 |
| `tools.approval_rules.enabled` | `true` | 启用统一规则判定；`false` 回退到旧审批路径 |
| `tools.approval_rules.always_prefix_tokens` | `2`（≥1） | chat 的 `a`/`always` 为安全 shell 命令保存的前缀 token 数 |
| `tools.approval_rules.persist_ask` | `true` | chat 的 `a`/`d` 后是否询问写回用户级/项目级规则文件 |
| `tools.approval_rules.project_trust` | `{}` | 绝对 workspace 路径到规则文件 SHA-256；未信任项目 allow 会被忽略 |
| `tools.deferred` | `auto` | MCP 工具 schema 延迟加载：`auto`（schema 超过上下文窗口约 10% 时延迟）、`always`、`never`；被延迟的工具用 `tool_search` 取回 |
| `tools.preferred` | `[]` | 优先工具声明列表：每项 `name`、`kind`（`command` 的可执行文件名，或 `mcp` 的 `mcp.servers` id）、`when`（适用场景）。可用项写进环境说明，缺失项不列出；`command` 项只在本会话注册了 `shell_exec` 的可写会话里列出；`mcp` 项不被延迟加载，但不自动放行（见 references/tools.md 的“优先工具”） |
| `tools.snapshots.enabled` | `true` | 是否为 `file_write`、`file_edit`、`file_remove` 保存写前快照；关闭只停止新快照，不删除已有快照或命令 |
| `tools.snapshots.max_file_bytes` | `16777216`（16 MiB） | 单次调用保留的写前内容上限；超过时照常写入但标记不可恢复 |
| `tools.snapshots.max_session_bytes` | `268435456`（256 MiB） | 一个会话的快照内容上限 |
| `tools.snapshots.max_global_bytes` | `1073741824`（1 GiB） | `$SUGA_HOME/sessions` 下快照的全局内容上限 |
| `tools.snapshots.keep_turns` | `20` | 保留的已完成 checkpoint 回合数；open/incomplete 回合不因数量清理 |
| `tools.snapshots.keep_days` | `7` | 已完成 checkpoint 的最长保留天数 |
| `tools.snapshots.on_error` | `deny` | `deny` 在快照存储、清理或状态不确定时在文件 I/O 前拒绝；`continue` 允许写入但标记不可恢复 |
| `tools.max_output_bytes` | `8000` | 单次工具结果内联进会话的上限；超出时脱敏原文存 `$SUGA_HOME/sessions/<id>/outputs/`，会话只留 ref 与头尾各 2000 字节 |
| `tools.max_spill_bytes` | `1048576`（1 MiB） | 单次工具（含 MCP）原始输出上限 |
| `tools.spill.max_bytes` | `1073741824`（1 GiB） | `$SUGA_HOME/sessions/**/outputs` 的逻辑字节配额（`.gz` 按压缩前字节计）；超额时新的 spill 返回错误并只保留头尾，**不会**自动删旧输出；`0` 不设配额 |
| `tools.spill.max_age` | `90d` | 可淘汰年龄（只作报告/gc 候选，不在写入路径删文件） |
| `tools.spill.compress_after` | `7d` | `suga data compress-spill` 压缩 mtime 超过此时长且会话不活跃的 `.txt`（先写 `.txt.gz.tmp`、解压校验后 rename、最后删 `.txt`；ref 不变） |
| `tools.spill.compression` | `gzip` | `off` 关闭压缩；读取端始终兼容 `.txt`/`.txt.gz` |
| `tools.spill.on_quota` | `refuse` | 超配额时拒写新输出（仅支持 `refuse`） |
| `tools.spill.keep_days` | `0` | 额外的按天淘汰规则；`0` 只用 `max_age` |
| `tools.parallel_reads` | `true` | 同一次回复里的多个本地读调用（file_read/list/grep/find）并发执行 |
| `tools.read_page_bytes` | `32768` | `file_read` 每页字节数（它自己分页，不受 `max_output_bytes` 限制） |
| `tools.minimize_output` | `true` | shell 结果进上下文前整理（去色、折叠重复行、按输出规则裁剪测试/构建/git/grep 输出），原文留 ref |
| `tools.loop_guard` | `true` | 循环调用检测：键是“工具 + 规范化参数 + 结果摘要（是否出错与结果正文的哈希）”，结果变了不算重复。一个回合内连续 3 次（R1）或累计 4 次（R2）同一个键，或同一条 `shell_exec` 命令失败 3 次（R3；中间有编辑也计数，而成功的 `file_edit`/`file_write`/`file_remove` 会把 R1/R2 计数清零，所以改完重跑不会误报）时，在该次结果末尾追加提醒（换方法或停下说明原因），trace 记 `loop_guard` 事件（`rule` 为 `R1`/`R2`/`R3`）、chat 显示一行灰色提示；同一组每回合最多提醒 2 次（`update_plan`/`session_message`/`agent` 不计入） |
| `agents.nudge` | `on` | 收工检查（作用于主 agent 的回合）：`on`/`off`。回合正常结束后按规则的种类判断是否提前收工——`unverified_write`：改动后没有验证（本回合最后一次成功的 `file_edit`/`file_write`/`file_remove` 之后，没有 check 工具的命令命中规则 `check_command` 的调用）；`plan_open`：会话计划还有 pending/in_progress 项；`promise`：结尾承诺继续却停下（最后一条消息无工具调用，末尾约 300 字符命中承诺句式，以问句结尾不算）。规则能用 `nudge-rules.yaml` 调整（见下方「收工检查规则」）。`run` 注入 `[收工检查]` 回合推回（按种类限额：promise 最多 2 次，`unverified_write` 与 `plan_open` 合计最多 1 次，`--verify` 时这两种跳过），`chat` 只显示一行灰色提示，ACP 只写 trace（`nudge` 事件带规则名与 kind）。回合出错/yield/取消/收尾时不检查 |
| `tools.shell` | `""`（Windows：pwsh 优先，否则 powershell；其它：sh） | `shell_exec` 使用的 shell（命令名或路径） |
| `tools.interpreters` | 自动探测 | 启动时探测 PATH 上的脚本解释器（默认候选 `python3`、`python`、`node`）：每进程探测一次并缓存（`--version`，2 秒超时），命中的写进环境段（可写模式）并提示优先写脚本执行。`false`（或 `off`）关闭；也可给名称或绝对路径列表，如 `[python, C:\Python312\python.exe]`（绝对路径按你的选择只校验 `--version`）。Windows 上自动探测会跳过 `%LOCALAPPDATA%\Microsoft\WindowsApps` 下的应用商店别名（运行只会跳商店或不出输出版本），商店包自身的安装目录（`C:\Program Files\WindowsApps\...`）不受影响 |
| `tools.shell_timeout_max` | `600` | `shell_exec` 单次 `timeout` 的上限秒数（单次默认 120） |
| `tools.shell_background_after` | `120s` | 前台 `shell_exec` 超过此时长后转为后台任务并保留已输出；`0` 关闭转后台；`tools.shell_timeout_max` 仍是硬上限 |
| `tools.shell_limits.memory` | `4GiB` | 一次 `shell_exec` 与它整棵进程树的内存上限（`2GiB`、`512MiB` 或字节数）；`0`/`off` 不限。Windows 为 Job Object 的单进程与整树提交上限，Unix 为 `RLIMIT_DATA` |
| `tools.shell_limits.processes` | `256` | 进程树同时可有的进程数（Windows `ActiveProcessLimit`）；`0` 不限。Unix 为 `RLIMIT_NPROC`，按用户计数，suga 取「当前用户进程数 + 该值」，无 `/proc` 的 Unix 不生效 |
| `tools.shell_limits.cpu_time` | 不限 | 单进程 CPU 时间上限（如 `10m` 或秒数，Windows `PerProcessUserTimeLimit`、Unix `RLIMIT_CPU`）；`0` 不限。Unix 上被它杀死时通常没有输出痕迹，结果里可能只有退出码（见 `references/tools.md`） |
| `tools.shell_limits.off` | `false` | `true` 时以上三项全部关闭，命令不受资源限制（详见 `references/tools.md` 的资源限制一节；这不是沙箱） |
| `tools.dependency_roots` | `true` | 自动识别工作区外的只读目录（Go 模块缓存、`GOROOT/src`、Rust crate 源码） |
| `tools.standards_roots` | `true` | 自动识别 idev-std 发行目录（`IDEV_STD_RELEASES_ROOT` / `INHERE_DEV_STANDARDS_ROOT`）与绑定工作空间的 `tmp/idev-std` 加载产物目录；`false` 关闭 |
| `tools.read_roots` | `[]` | 额外只读目录列表（绝对路径，支持 `~`）。只影响 file_read/list/grep/find。只想对某个会话生效时用 `--add-dir <目录>` 或 `SUGA_ADD_DIRS`（`:rw` 可写） |
| `tools.web_fetch.allow_private` | `false` | 允许 `web_fetch` 访问私网/本机地址 |
| `tools.web_fetch.proxy` | `""` | `web_fetch` 的代理（`http://`、`https://`、`socks5://`）；只影响 web_fetch，不影响模型请求 |
| `tools.web_fetch.user_agent` | `""` | `web_fetch` 发出的 User-Agent；空 = `suga-web-fetch/1.0`，可改成浏览器 UA，例如 `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36` |
| `tools.auto_allow` | `[]` | MCP 放行名单（普通运行），条目 `server/toolglob`（裸 `toolglob` 也接受，会匹配任意 server） |
| `tools.read_only_allow` | `[]` | `--read-only` 时**唯一**使用的放行名单，条目必须写成 `server/toolglob` |

### 审批规则文件

规则正文不放进 `config.yaml`，而是分别从 `$SUGA_HOME/approval-rules.yaml` 和 `<workspace>/.suga/approval-rules.yaml` 读取；`tools.approval_rules.*`（`enabled`、`always_prefix_tokens`、`persist_ask`、`project_trust`）是它的设置项，见本文的 `tools.*` 表。条目的完整字段表（`tool`/`command`/`path`/`action`/`modes`）、匹配与 fail-closed 语义、项目 allow 的信任门槛与完整示例见 `references/rules.md` 的审批一节——不要在这里重复维护一份。chat 的 `a`/`d` 创建会话规则时用 `u` 写用户级、`p` 写项目级 deny；ACP 的 `allow_always`/`reject_always` 只保存在当前进程状态。写完用 `suga inspect rules approval` 与 `suga inspect manifest` 的 Approval rules 一节（路径、生效条数、hash/信任与忽略原因）核对；`suga inspect trace <session> --json` 显示 `approval_rule_persist`。`enabled: false` 可回退旧路径。

输出规则：内置规则按语言/工具分文件（`suga inspect rules output --builtin` 可逐份打印原文）→ `$SUGA_HOME/output-rules.yaml` → `<workspace>/.suga/output-rules.yaml`（同名替换、新名追加、`enabled: false` 关闭）。能力概览：丢行、折叠噪声、按正则改写（`replace`）、按捕获键分组合并（`group_by`）、保留首尾 N 行（`head`/`tail`），可用 `min_bytes`/`min_saving` 给自己设更低的处理门槛（默认 <600 字节不动、省不到 20% 放弃）；整理结果短于原文且达到节省门槛才交给模型。**完整字段表（含 `match_output`、`block`、`count`/`sum`、`fold`、`summary`）与执行顺序见 `references/rules.md` 的输出整理一节**；改完用 `suga inspect rules output` 或 `suga inspect manifest` 的 `Output rules` 一节核对（生效规则与来源层，被跳过的文件列在 `output_rule_skips`）。

收工检查规则：内置规则（`promise-zh`、`promise-en`：回答末尾承诺继续的句式；`unverified-write`：改动后没有验证；`plan-open`：会话计划还有未完成项）→ `$SUGA_HOME/nudge-rules.yaml` → `<workspace>/.suga/nudge-rules.yaml`（同名整条替换、新名追加、`enabled: false` 关闭；配置不写 kind 时沿用同名内置规则的种类）。种类固定三种（`promise` / `unverified_write` / `plan_open`），配置只能调参数、加句式、开关，不能写任意逻辑；**字段表（`patterns`/`write_tools`/`check_tools`/`check_command`）与完整示例见 `references/rules.md` 的收工检查一节**。坏正则、未知 kind、新规则缺 kind、解析失败会带文件路径与规则名报错，整个文件被跳过——改完用 `suga inspect rules nudge` 核对（生效规则按名字、种类、来源层列出，坏文件出现在 skipped）。

## `store.*`

| 配置项 | 默认 | 说明 |
|---|---|---|
| `store.body_inline_bytes` | `1024` | tool 结果正文超过此字节数时移出数据库，写进 `$SUGA_HOME/sessions/<id>/bodies.jsonl`（v0.40 前改过名的库仍是 `tools.jsonl`），库里只留元数据、短预览与文件偏移，读取时透明取回；`0` 表示全部留在库里。旧名 `store.tool_body_inline_bytes` 在 v0.41.0 移除：加载配置时警告一次并忽略该键（不报错，旧配置文件照常启动）。回退到旧版本前先执行 `suga data bodies --undo` |

## `gc.*`
| 配置项 | 默认 | 说明 |
|---|---|---|
| `gc.retention.interactive` | `180d` | `suga data gc --retention` 对交互会话的保留期：chat 创建、被 chat 接续过（`used_interactively`）以及没有 `meta.origin` 的历史会话。`0` 表示该类不清理 |
| `gc.retention.noninteractive` | `30d` | 同上，适用于非交互入口（`meta.origin` 为 `run`、`acp`、`agent`）创建的会话。`0` 表示该类不清理 |

时长接受天数（`180d`、`30d`）、Go 时长（`4320h`）或秒数。`suga data gc --retention` 默认只报告（每类的会话数、条目数、预计字节），`--apply` 才删除；活跃会话、run 登记与持锁会话始终跳过，删除本身不 VACUUM（要回收磁盘空间再跑 `suga data vacuum`）。`meta.origin` 为 `xhen` 的会话（宿主应用 xhen 创建的，它通过 `wiring.Options.SessionOrigin` 声明）不参与按期清理：生命周期由宿主决定，报告单列“由宿主管理，未清理 N 个”，`suga data gc --older-than` 显式清理仍会删除。

## `mcp.*`

| 键 | 默认 | 含义 |
|---|---|---|
| `mcp.servers` | `[]` | 默认 MCP 服务列表；每项 `id`、`command`、`args`、`env` |
| `mcp.call_timeout_sec` | `120` | 单次 MCP 工具调用超时（秒） |

命令行也可用 `--mcp-config <Claude 风格 JSON>` 临时接入（可重复）。MCP 子进程继承启动环境，但 `*_API_KEY` / `*_AUTH_TOKEN` 与 suga 读取的 LLM 密钥变量被剔除。

## `skills.*`

| 键 | 默认 | 含义 |
|---|---|---|
| `skills.list_max` | `15` | 带描述列出的 skill 数量上限；超过后只列工作区 skills（带描述），其余只列名字（名字合计上限 3000 字节） |
| `skills.always_load` | `[]` | 正文每次进提示词的 skill 名列表（等价于 SKILL.md 的 `metadata: {always_load: true}`），每个正文上限 8000 字节 |

扫描根目录顺序：`<工作区>/.agents/skills`、`<工作区>/skills` → `$SUGA_HOME/skills` → `~/.agents/skills`；同名先到者胜。

## `agents.*`（子 agent）

| 键 | 默认 | 含义 |
|---|---|---|
| `agents.enabled` | `true` | 是否注册 `agent` 工具 |
| `agents.model` | 不设（会话自身的模型） | 子 agent 使用的模型，写法同 `model`（裸 id 用主模型 provider） |
| `agents.max_steps` | `30` | 步数上限，达到后要求收尾（不受 `SUGA_MAX_STEPS` / `run --max-steps` 影响） |
| `agents.max_tokens` | `400000` | 未命中缓存的输入 + 输出上限，达到后要求收尾 |
| `agents.timeout` | `10m` | 软时间上限（之后 60 秒宽限） |

## `tasks.*`（后台任务）

| 键 | 默认 | 含义 |
|---|---|---|
| `tasks.max_running` | `4` | 每会话同时运行的任务数 |
| `tasks.max_duration` | `2h` | 单个任务最长运行时间 |
| `tasks.max_output_bytes` | `67108864`（64 MiB） | 任务输出文件上限 |
| `tasks.wake_on_exit` | `true` | chat 等待输入时任务结束/监听结果变化的通知会唤醒并开始一轮 |

## `memory.*`

| 键 | 默认 | 含义 |
|---|---|---|
| `memory.write` | `auto` | `auto` 允许 memory 工具写入；`off` 让工具整体只读 |
| `memory.write_tainted` | `ask` | 读过外部内容的会话写记忆时：`ask`（chat 询问 / ACP 发权限请求 / run 拒绝）、`deny`、`allow` |
| `memory.taint_tools` | `[]` | 额外把会话标记为已污染的工具（每次结果都算） |
| `memory.taint_on_spill` | `[shell_exec]` | 仅当输出被外置成文件时才污染会话的工具；显式空列表可关闭 |
| `memory.user.backend` | `files` | `files`（`$SUGA_HOME/memory/user`）或 `exec` |
| `memory.project.backend` | 自动（见下） | `files`、`gofer` 或 `exec` |
| `memory.<scope>.command` | 无（`exec` 时必填） | 外部命令 |
| `memory.<scope>.args` | `[]` | 外部命令参数 |
| `memory.<scope>.timeout_sec` | 不设 | 外部命令超时秒数 |

`memory.project.backend` 不设时的选择顺序：仓库根有 `MEMORY.md` → `files`（仓库模式，条目在 `.suga/memory/<id>.md`，可随项目提交）；否则仓库根有 `.gofer/tracker` → `gofer`；否则 `$SUGA_HOME/projects/<key>/memory` 的 `files`（`<key>` = 仓库目录名 + 路径短哈希）。`suga inspect manifest` 显示结果与原因。

## `hooks.*`（仅 chat）

| 键 | 默认 | 含义 |
|---|---|---|
| `hooks.command` | 不设（不启用） | hook 命令，argv 列表（不经 shell）；事件 JSON 写在 stdin 上 |
| `hooks.timeout_ms` | `3000` | 普通事件的超时 |
| `hooks.stop_args` | `[]` | 只追加到 Stop 事件 argv；**必须**与 `stop_wait_sec` 同时配，否则 Stop 按 `timeout_ms` 结束 |
| `hooks.stop_wait_sec` | 不设 | 设置了它时 Stop 最多等 `stop_wait_sec+2` 秒 |
| `hooks.stop_background` | `false` | 把 Stop 作为后台任务运行（打字、投递、任务唤醒都不打断它）；此时超时由 `tasks.max_duration` 限制，除非设了 `stop_wait_sec` |
| `hooks.post_tool_use.tools` | `[]`（不发） | 只对这些 glob 匹配的工具发送 PostToolUse |
| `hooks.post_tool_use.name` | 不设 | 设置了就把 PostToolUse 事件里的工具名改成它 |
| `hooks.pre_tool_use.tools` | `[]`（不发） | 只对这些 glob 匹配的工具在**执行前**发送 PreToolUse（可拒绝或改写参数） |
| `hooks.pre_tool_use.name` | 不设 | 设置了就把 PreToolUse 事件里的工具名改成它 |

事件：SessionStart、UserPromptSubmit、PreToolUse、PostToolUse、Stop、SessionEnd、Interrupt。`SUGA_HOOKS=off` 整体关闭。

## `log.*`（进程日志）

| 键 | 默认 | 含义 |
|---|---|---|
| `log.level` | `info` | 最低记录级别：`debug`、`info`、`warn`、`error`；`debug` 在 `log.stderr: auto` 下也把记录同时写到 stderr |
| `log.file` | `$SUGA_HOME/logs/suga.log` | 日志路径；实际写入的是在扩展名前加日期的文件，如 `suga-20261020.log`（同一天的所有进程追加同一文件）。`off` 关闭文件日志 |
| `log.stderr` | `auto` | `auto` 仅在 debug 时也写 stderr（即 `SUGA_DEBUG` 非空或 `log.level: debug`）；`always` 总是写；`never` 不写（日志文件打不开时仍回退到 stderr，避免静默） |
| `log.max_size` | `10485760`（10MB） | 单个日志文件达到该字节数后切分并重新打开当前文件 |
| `log.keep` | `5` | 保留的切分文件份数（按数量清理，30 秒内改动的文件不动）；`0` 不清理 |
| `log.compress` | `false` | 把已切分的旧文件 gzip 压缩（`*.gz`） |

- 轮转用 `github.com/gookit/rotatefile`，模式为按大小 + `ModeCreate`：切分时把当前文件改名为 `<名>_<序号>.log` 再重新打开 `<名>.log`，文件名带日期让同一天的各进程自然写同一个文件、跨天自然换文件。多进程（chat 与 gofer 拉起的多个 run/ACP）同时追加是常态：一条记录一次写入，行不会互相穿插；某个进程切分时，其他进程仍持有旧文件的句柄，它们的行会继续落进切分出来的文件（不丢），写入失败时本进程会重建 writer 重试一次。Windows 只允许在所有句柄都允许删除时改名（Go 的 `os.OpenFile` 不允许），所以多个 suga 进程同时持有日志时，部分切分会被跳过（文件继续增长直到有一次改名成功），行不会丢；没进程长期占用时（常见的单个 chat）切分正常。
- 坏值（如 `log.level: loud`、`log.max_size: 10MB`）在启动时直接报错并指出键名。
- 日志记录位于 `$SUGA_HOME/logs/`，不进入会话库；运行一个回合时的记录带 `session_id`、`turn_id` 属性，便于与 `suga trace` 对齐。

## 上手、初始化与配置命令

### `suga init`

首次配置建议先运行：

```bash
suga init
```

它按需创建 `$SUGA_HOME`（默认 `~/.suga`）、0600 的注释型 `config.yaml` 和只含说明注释的 0600 `.env`。模板中没有任何 `NAME=value` 赋值；用户自行把密钥和私有地址写入 `.env`，在 YAML 中用 `${NAME}` 引用。已有 `config.yaml` 或 `.env` 都不覆盖，`.env` 也不备份。

`--force` 只针对 home 下的 `config.yaml`：先创建带 UTC 时间戳的 `.bak.<timestamp>` 备份，再写入 onboarding 模板；它仍不会改动 `.env`。设置 `SUGA_CONFIG` 后，配置命令以该外部文件为源，`init --force` 会拒绝覆盖（退出码 2），缺失的显式配置文件也不会静默改写为 home 配置。Windows 下路径使用平台原生分隔符。

`version`、`--version`、`-V` 及帮助命令不初始化、不创建目录或文件。`chat`、`run`、`acp`、`config`、`models` 和其它需要配置的命令会在加载配置前执行同一个安全初始化门控；首次提示写 stderr，因此不会污染 `run --output-format stream-json` 或 ACP 的协议 stdout。

### `suga config`

配置命令只维护 YAML 配置源，不修改环境变量：

```text
suga config get <key>       # 生效值与来源；敏感值遮罩
suga config set <key> <value>
suga config unset <key>
suga config list             # 稳定排序的键、值和来源
suga config path             # home/config/.env/source
```

- `get` 遵守默认值、配置文件和环境变量的现有优先级；`SUGA_MODEL` 覆盖 `model` 时会显示 `env:SUGA_MODEL` 来源。
- `set`/`unset` 只接受 registry 中的可写键，使用现有 typed reader 做类型与语义校验；拒绝 `api_key`、token、password、secret 等密钥键写入 YAML。请改 `.env`，不要把密钥放命令行或配置文件。
- 字符串列表键（如 `tools.auto_allow`、`fallback_models`、`tools.read_roots`）的值有两种写法，写进文件的结果相同（块式 YAML 列表）：
  - 逗号分隔：`suga config set tools.auto_allow 'a/b, c/*'`（每项两边空格会被去掉，空项报错）；
  - YAML 行内列表：`suga config set tools.auto_allow '["a/b", "c/*"]'`，适合直接从 `config.yaml` 复制列表；每项必须是字符串（`[1]`、`[[a/b]]`、`[""]` 都报错），带空格的项要加引号。
  值以 `[` 开头时按 YAML 列表解析，解析失败或含非字符串项会报错退出 2，不写文件。
- YAML AST 写回只改目标节点，保留注释、键顺序、行内注释和原有换行；每次 `set`/`unset` 只保留最近的 `<config>.bak`，失败时原文件保持不变。
- `SUGA_CONFIG` 存在时 `get`/`set`/`unset`/`list` 使用显式文件；`path` 总是显示 home、实际 config、`.env` 与 `source`。配置输入/类型/策略错误退出 2，读取或写盘错误退出 1。

### `suga models`

`models` 是完全离线的本地模型投影：

```bash
suga models
suga models --json
```

它只读取本地配置，列出内置 provider 命名空间、已配置模型以及当前/compact/fallback 角色，并输出与 chat `:model` 共享的 `switchable` 范围。它不构造 provider、不启动 MCP 或 hooks、不请求远端模型目录；`suga inspect manifest` 仍负责完整能力诊断，chat 的 `:model` 仍是会话级切换且不写配置。

### 命令退出码与手工备选

成功为 0；初始化、配置读写的普通文件系统失败为 1；初始化保护、未知键、只读/密钥键、类型或参数错误为 2。无法运行二进制时，可按仓库的 [onboarding.yaml](../../../config/onboarding.yaml) 手工创建最小配置，再按 [providers.md](providers.md) 设置 provider；不要提交 `.env` 或真实密钥。

## 环境变量

| 变量 | 含义 |
|---|---|
| `SUGA_HOME` | 数据与配置根，默认 `~/.suga`（会话库 `$SUGA_HOME/db/sessions.db` 等都在其下） |
| `IDEV_STD_RELEASES_ROOT` | idev-std 的发行目录；指向已存在目录时，在 `tools.standards_roots` 开启时作为只读根 |
| `INHERE_DEV_STANDARDS_ROOT` | `IDEV_STD_RELEASES_ROOT` 的兼容环境变量，作用相同 |
| `SUGA_CONFIG` | 指定唯一的配置文件路径（替代 `$SUGA_HOME/config.yaml`） |
| `SUGA_MODEL` | 主模型，`<provider>/<model>`；被 `--model` 覆盖，覆盖配置 `model` |
| `SUGA_HOOKS` | `off` 时整体关闭 chat hooks |
| `SUGA_APPEND_SYSTEM_PROMPT` | 默认的追加系统提示（命令行的 `--append-system-prompt` 优先；非空时也覆盖会话已保存的那份） |
| `SUGA_DEBUG` | 非空时打开 debug 日志并**同时写 stderr**（含 `DEBUG MODE ENABLED` 行与 `log_file` 路径）；默认按 `log.*` 配置只写 `$SUGA_HOME/logs/suga-<日期>.log`，终端保持干净 |
| `SUGA_PROVIDER` | **已移除**：仍有值时启动报错，改用 `SUGA_MODEL=<provider>/<model>` |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `DEEPSEEK_API_KEY` | 三个内置预设的默认密钥变量 |
| `{VAR}` | 配置里 `api_key`、`base_url`、`headers` 的值支持 `${VAR}`，来自进程环境或 `$SUGA_HOME/.env` |

密钥不要写进命令行参数（会被进程列表、job 请求记录持久化）；放进 `.env` 或进程环境。

### suga 导出给子进程的变量

会话跑的子进程（`shell_exec` 及其后台/ watch 任务、chat 的 `!cmd`/`!!cmd` 本地命令，以及 chat hooks 的 `hooks.command`）环境里带上当前会话信息；每次执行时取值，所以 `:model` 切换、子 agent 的会话、ACP 同进程多会话都拿当前那一个。

| 变量 | 含义 |
|---|---|
| `SUGA_SESSION_ID` | 当前会话 id（与 `suga sessions`、`suga inspect trace` 用的相同；子 agent 内部是子 agent 自己的会话 id） |
| `SUGA_SESSION_NAME` | 会话短名称（`$SUGA_HOME/run/` 里登记的名字，如 `suga-k7`）；会话没有短名称时不设置（run/ACP 的会话通常没有） |
| `SUGA_TURN_ID` | 当前回合 id（工具调用所在回合）；回合外（如 `SessionStart` hook、chat 本地命令）不设置 |

- 父进程里已有的同名变量会被覆盖；父进程里没有的（例如会话无短名称）不会被继承，避免嵌套 suga 时读到外层会话的值。
- `GOFER_SESSION_ID` 不由 suga 设置或修改：它属于 gofer，原样透传。
- chat hooks 另外在 stdin 的 JSON 里带 `session_id`（格式不变）。

## 目录布局（`$SUGA_HOME` 下）

| 路径 | 内容 |
|---|---|
| `config.yaml`、`.env` | 配置与密钥 |
| `db/sessions.db` | 全部会话（单个 SQLite 库，WAL） |
| `sessions/<session-id>/outputs/` | 外置的大工具输出（`recall` 用 `out:<session>:<hex>` 读） |
| `sessions/<session-id>/tools.jsonl` | 移出库的 tool 结果正文（每行 `seq` + sha256 + 原始 JSON；库里只留桩，读取时透明取回；备份始终包含，删会话时一并删除） |
| `sessions/<session-id>/snapshots/` | 文件工具的敏感写前快照；按会话、回合和调用保存，不进入上下文、transcript 或 export |
| `memory/user/` | 用户级记忆（files 后端） |
| `projects/<key>/memory/` | 项目记忆（files 后端，非仓库模式） |
| `trash/` | `file_remove` 的回收区（保留 7 天，`origin.json` 记录来源） |
| `run/` | chat 进程登记与锁（`suga ps`、`suga send` 用；进程记录里的 `name` 是会话短名称 `<目录名>-xy`，如 `suga-k7`） |
| `skills/` | 释放的内置 skill 与用户自己的 skill |
| `transcripts/` | chat 会话记录（`transcripts: false` 关闭） |
| `logs/suga-<YYYYMMDD>.log` | 进程日志（0600、追加）：文件名带日期，按 `log.max_size` 切分，切分文件按 `log.keep` 保留；默认只写文件，debug（`SUGA_DEBUG` 或 `log.level: debug`）时同时写 stderr，`log.file: off` 关闭 |
| `AGENTS.md` | 用户级项目规则（每级规则文件之外额外读取） |
| `output-rules.yaml` | 用户级输出整理规则 |
| `nudge-rules.yaml` | 用户级收工检查规则 |

## 命令与常用 flag

```text
suga                      # 等价 suga chat（交互式对话）
suga chat [--resume <id>|--session-id <id>] [--model <p/m>] [--thinking <level>] [--add-dir <目录>[:ro|:rw]]
                          # 换目录续接时：有终端就问三选一（切到原目录 / 当前目录 / 取消）；
                          # 非终端默认失败，--allow-cwd-change 直接在当前目录继续
suga run --prompt "..."   # 单轮；[--allow-write] [--read-only] [--model] [--thinking]
                          # [--session-id <id>] [--resume <id>] [--from <id>]
                          # [--output-format text|stream-json] [--mcp-config <file>]
                          # [--append-system-prompt <text>] [--clear-append-system-prompt]
                          # [--no-project-rules] [--injected] [--max-steps <N>]
                          # [--verify <命令>] [--verify-rounds <N, 1-5>] [--verify-timeout <10m>]
                          # [--add-dir <绝对目录>[:ro|:rw]]（可重复；也可用 SUGA_ADD_DIRS）
                          # [--allow-cwd-change]（会话原目录与当前目录不一致时在当前目录继续）
suga acp                                      # 以 ACP stdio 服务（供 gofer 等客户端接入）
suga send --session <id|短名称|前缀> --text <文本>   # 给正在运行的 chat 送话（--wait 等真正开始）
suga ps                                       # 正在运行的 chat 进程（第二列是短名称）
suga sessions [关键词] [-n 10] [--since 7d] [--cwd .] [--agents] [--origin <name>] [--json]
suga inspect trace <session> [--turn <id>] [--json] [--analyze] [--web]
suga undo <session> --list [--json]
suga undo <session> [--turn <id>|-n <count>] [--force] [--yes] [--json]
suga inspect stats [--session <id>] [--since 7d] [--json]
suga inspect export <session> [--redact] [-o <file>]
suga data gc --older-than 30d [--apply]
suga data gc --retention [--apply]
suga data bodies [--status|--move|--undo|--check|--remove-files]
suga data vacuum
suga data usage [--json] [--top N]
suga inspect manifest [--json] [--model <p/m>] [--thinking <level>]
suga version
```

chat 里的命令：`:session`（会话 ID）、`:compact`（开启新段）、`:quit` / `:exit`（退出）。退出码：会话不存在 `4`、被中断 `130`、其它 `1`。

`suga undo` 不依赖 provider，session 参数接受完整 ID 或唯一前缀。`--list` 只读；破坏性操作在非终端 stdin 下必须 `--yes`，`--force` 只覆盖内容冲突。退出码为：`0` 完整恢复或幂等无操作，`3` 无快照/部分恢复/不可恢复文件，`4` 会话不存在，`5` 会话忙，`1` 参数、取消或其它错误。`--json` 输出 checkpoint 清单或回滚结果。chat 的 `:checkpoints` 和 `:undo [N] [force]` 只认本地提示符输入；shell_exec 只列出不回滚，cwd 或可写附加目录变化会使旧快照不可恢复。
