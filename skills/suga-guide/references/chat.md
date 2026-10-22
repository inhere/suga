# chat 交互参考

`suga chat`（或直接 `suga`）的本地命令、按键、插话（steering）、`!` 本地命令、模型切换、续接预览与投递。相关配置键（`chat.*`）的默认值见 `references/config.md`。

## 步数上限（`max_steps`）

- 一轮最多用 `max_steps` 步（默认 240，按轮计，不是整个会话），到顶时模型被要求直接基于已获得的信息作答。
- `run --max-steps N`（N 至少 1）为本次调用覆盖该设置；环境变量 `SUGA_MAX_STEPS` 在两者之间生效。
- 子 agent 用它们自己的 `agents.max_steps`。

## 本地命令（行首 `:`）

| 命令 | 作用 |
|---|---|
| `:session` | 显示会话 ID |
| `:compact` | 开启新段（见 `references/sessions.md` 的“分段”） |
| `:quit` / `:exit` | 退出 |
| `:diff` / `:diff N` | 查看最近一次 / 第 N 次完整文件改动；`chat.diff: false` 关闭预览，`chat.diff_lines` 调整预览行数 |
| `:code` / `:code N` | 展开上一条回答里被折叠的代码块 / 第 N 个代码块（按出现顺序，`chat.code_max_lines` 控制折叠） |
| `:add-dir <绝对目录>[:ro\|:rw]` | 为本会话追加授权目录 |
| `:dirs` | 列出工作区与全部附加目录（权限，以及来源：启动参数、环境变量、会话中追加、越界授权、早先会话沿用） |
| `:rm-dir <路径>` | 撤销其中一个附加目录 |
| `:image` | 列出待发送的图片（见下“附图”） |
| `:image <路径>` | 把图片附到**下一条**用户消息（可多次、顺序保留） |
| `:image clear` | 清空待发送的图片 |
| `:model` / `:model <provider>/<model>` | 查看 / 切换主模型（见下） |
| `:help` | 列出本地命令和按键 |
| `:usage` | 显示本进程与会话的 token、缓存和成本累计 |
| `:tasks` | 列出后台任务 |

- 目录改动写入会话，因此本会话后续回合与续接都生效。
- 这些命令只在本地键盘输入时生效：`suga send` 或 job runner 投递的文本一律是普通消息，投递的 `:add-dir`（或 `:quit`）不会作用于会话。

## 切换模型（`:model`）

- `:model` 查看当前 provider/model、思考级别、上下文窗口与 `max_output_tokens`，以及配置中 `providers.<id>.models` 的模型键。
- `:model <provider>/<model>` 从下一回合切换主模型，Tab 可补全模型名。
- 切换保留 `fallback_models`，写入会话元数据并在 trace 记录 `model_switch`；`:model` 只认本地键盘输入。
- 续接会话时沿用该选择，除非显式传 `--model`。

## 附图（`:image` 与 Alt+V）

- `:image <路径>` 把图片附到**下一条**消息：路径规则同文件工具（工作区、只读依赖根、附加目录），按 `view_image` 同一套校验与缩放（PNG/JPEG/GIF/WebP，长边缩到 `images.max_side`）；可多次附，顺序保留。
- `Alt+V` 直读系统剪贴板图片：有图与 `:image` 同流程附队（输入行上方显示 `[已附图 N：宽×高 格式]`），没图提示“剪贴板里没有图片”，模型不看图就拒绝。
- 当前模型不支持看图（`vision` 未开启）时两条路都拒绝并提示用 `:model` 切换；已发图会落盘到 `sessions/<id>/images/`（数据库只存引用），历史里早于 fold 边界的旧图会换成文字说明，需要时让模型 `view_image <Ref>`。
- 命令行一次性附图：`suga run --image <路径>`（可重复、顺序保留）走同一套校验与缩放，相对路径按 run 的工作目录解析；模型不看图或图片读不了就在回合开始前报错退出（提示用 `--model` 或配置 `vision`）。`suga send` 仍只投递文本。

## 输入历史与补全

- 支持 Up/Down 历史、Ctrl+R 增量反查（Enter 采用、Ctrl+G/Esc 取消），以及行首 `:` 命令和 `:add-dir`/`:rm-dir` 目录的 Tab 补全。
- `!` 本地命令也会记录到同一历史文件；投递消息不会记录。
- 历史文件：`chat.history`、`chat.history_size`（默认开启、最多 1000 条），保存在 `$SUGA_HOME/history.jsonl`；只记录键盘输入并先脱敏。

## 多行输入

- Windows Terminal（1.25 及以上）下 chat 进入行编辑前请求 win32-input-mode（`ESC[?9001h`，配置 `chat.win32_input_mode: auto|on|off`，`auto` 只在 `WT_SESSION` 存在时开启）：此后终端把每个按键作为记录送达，`Ctrl+Enter`、`Shift+Enter`、`Alt+Enter` 都在输入里插入换行，不依赖 `settings.json`；退出行编辑、Ctrl-C、退出 chat 与 `!` 本地命令前后都恢复（`ESC[?9001l`）。终端不认识该序列时按键仍按原字节解析，行为与下面相同。
- 其它情况（非 Windows Terminal、旧版终端、`chat.win32_input_mode: off`）：Windows 上 `Ctrl+J` 在输入里插入换行（Windows Terminal 1.24 把 `Ctrl+Enter` 也发成裸 LF，用 `tmp/keyprobe` 实测确认）；Unix 等终端上用 `Alt+Enter`（ESC+Enter），因为那里裸 LF 就是 Enter。
- 哪个键能到程序由终端决定：Windows Terminal 把 `Alt+Enter` 留给全屏（要用就在 `settings.json` 里重绑或 `unbound`），`Shift+Enter` 通常与 Enter 同码——在 Windows Terminal 里加一行 `{"keys":"shift+enter","command":{"action":"sendInput","input":"\u001b\r"}}` 也能用。
- `Enter` 把整段作为一条消息提交。
- 输入里含换行时，`Up`/`Down` 在行内移动光标（保持行内文本列、目标行较短则落在行尾）；在第一行再按 `Up`（或最后一行再按 `Down`）照常切历史，`Down` 回来时整段输入恢复。

## 提示符处的 Ctrl-C / Ctrl-D

- 输入行上有文字时 Ctrl+C 只清空这一行（不提示、也不计数）；多行输入整段擦掉、光标回到空行起点。
- 行空时按一次要求再按一次确认，3 秒内再按一次才退出。
- Ctrl+D 直接退出。
- 退出时会显示 token 用量和续接本会话的命令。

## 回合中插话与中止（`chat.steering`，默认开）

- 模型干活时输入行仍然可用：提示符变成 `↳ `，回车把这句插进**正在运行**的当前回合，模型在下一次请求前看到它（通常紧跟在你刚看到的那次工具调用结果之后），本轮不中断；若模型已经答完，这句成为下一回合。
- 按 `Esc`（或输入行为空时的一次 `Ctrl-C`）只中止本轮——chat 继续运行、留在提示符，本轮没能送达的插话回到输入行等你回车；3 秒内连按两次 `Ctrl-C` 才退出 chat。
- 输入行上有文字时 `Ctrl-C` 只清空该行（参考 Claude Code）：既不中止本轮，也不计入两次退出的计数；行空时两个含义都不变。
- `Esc`（回合中中止本轮）与审批提问处（取消问题）、本地命令执行中（终止命令）的 `Ctrl-C` 都不变。
- 按键以字节到达，因此不依赖控制台自己的 Ctrl-C 事件（ConPTY：Zed、Windows Terminal）——以前那条路上回合中按 `Ctrl-C` 没有反应（suga-nrf2）。
- 撤回插话：输入行为空时按 `Up` 把还在等待送达的插话取回来：输出区那几条改为“（已撤回）”，文本按原顺序一行一条回到输入行（光标在末尾），改完再回车重新插话；回合结束的状态行只计已送达的插话。已被某次请求带走的撤不回，`suga send --steer` 来的也不动（它的发送方还在等结果）。
- 回合中按 `Tab` 把输入行**排到本轮结束后**（不插进当前回合，与 `suga send` 不带 `--steer` 的排队语义一致）：输入行上方显示 `[排队] <内容>（本轮结束后发送）`，本轮结束后它作为下一条用户消息自动发送，并显示“（已发送）”；可以连续排多条，按顺序一轮一条。`:queue` 列出排队的消息，`:queue clear` 清空。回合中 `Ctrl-C` 中止本轮时排队的消息保留，照常在回合结束后发送。
- `chat.steering: false` 时回合中保持旧行为：输入回到 cooked 模式，`Ctrl-C` 是 SIGINT，中断即直接退出整个 chat。

## `!` 本地命令

- `!git status`：直接走本地 shell 执行（与模型的 `shell_exec` 同一个 shell、工作目录与环境，不询问）并实时显示输出，完全不叫模型。
- `!!<命令>`：同样执行，并把结果（命令、退出码、输出；输出按工具结果那套规则精简并截断）附在下一条消息的上下文里给模型。多个 `!!` 按顺序累积；chat 退出时未发送的丢弃。
- 非零退出码、超时（`tools.shell_timeout_max`）和被你终止会在单独一行上提示。
- 执行中按 Ctrl-C 或 Esc 只终止该命令（连同进程树），chat 留在提示符。
- 在运行中回合的 `↳ ` 输入行里输入 `!` 命令不会执行，那行退回输入行。
- `--read-only` 会话同样可以执行：该模式只限制模型的工具，不限制你自己的 shell。
- 每次执行在 trace 里记一条 `local_command`。

## 续接预览与崩溃恢复

- 续接已有会话（`--resume <id>`，或 `--session-id <id>` 指向已存在的会话）时，第一个提示符之前先显示该会话最后一次有正文的模型回复：淡色“上一次回复：”作区分，过长只显示末尾 40 行并给出 `suga inspect trace` 命令；上一回合被中断或失败时加一句“上一回合未正常结束”。只看不写，不调用模型、不产生会话条目。
- 进程被强杀（kill -9、崩溃）后会话会停在 `running`；续接后的第一个提示符输入时先把它恢复为 `open`（显示“上一回合异常中断，已恢复会话”，被中断的回合记为 `cancelled`）再正常开始新回合。有活进程在跑该会话时行为不变，仍然拒绝。
- 换目录续接的检查见 `references/sessions.md`。

## 投递（`suga send`）

- `suga send --session <s> <文本>`（或 `--text <文本>`，二选一）把文本送进正在运行的 chat。
- `<s>` 可用会话 ID、ID 前缀或短名称：每个 chat 注册时取一个 `<工作目录名>-<两位随机后缀>` 的名字（目录名转小写、只留 `[a-z0-9-]`，如 `suga-k7`），横幅与 `suga ps` 都显示它；名字存在会话元数据里，续接沿用（被另一个活进程占用时临时换一个并在终端说明）。
- 匹配优先级：完整 ID > 短名称 > ID 前缀；匹配到多个进程时退出码 2。
- `--steer` 把文本插进**正在进行的**回合（默认开着 `chat.steering`）：模型在下一次请求前看到它，与在 `↳ ` 提示符处键入的一句话走同一条路。会话空闲、`chat.steering: false` 或回合正在结束时，文本按普通投递等下一回合，客户端打印“未插入当前回合，已排队”；正在进行回合的插话收件箱满（8 条）时返回 `queue_full`。
- `--wait` 在插话送达模型或作为自己的回合开始运行时返回 0；插话被退回（它所在回合被取消/失败，或 chat 先退出）时退出码 3。
- 两个参数都不给时：文本等自己的回合。
- `--id <消息 ID>` 给消息起名，`--wait-processed` 等它的终态（不传 ID 时自动生成并打印，形如 `id: <ID>`）：同一接收方进程内重复 ID 不再入队（“至多处理一次”），回执写 `状态：processed`（回合完成）、`状态：held`（失败的回合把手里的消息转给用户下一回合，不会自动重试）或 `状态：failed`（回合失败/取消，退出码 3；接收方退出时丢弃也是退出码 3）。不带这两个参数时输出与退出码与之前逐字节一致。
- 接收方空闲时，排队中的消息（至多 10 条）合并成**一个**回合：每条照旧带自己的来源标签，回合按其中最大的跳数计，附图一起带上。投递回合失败时，还没取走的消息转入 `sessionHeld`，等用户下一次输入时一起处理（终端提示“已排队”）。
- `session_message` 的 `action=status`（参数 `to` + `id`）可以事后查一条消息的状态（`queued`/`steered`/`processed`/`failed`/`held`/`discarded`）；接收方重启或换进程后返回“未知”。
- 任何模式下投递的文本都只是普通消息，永远不会被当成命令（`:add-dir`、`:quit`、`!cmd` …）。

## 子 agent 进度（`chat.agent_progress`）

- 默认 `summary`：不逐条打印子 agent 的工具调用；等待状态行按 description 显示运行中的数量与各自调用次数，子 agent 结束时在结果旁显示调用总数与耗时。多个并行子 agent 按名字分别计数。
- `full` 保留逐条的 `  · <工具> <参数摘要>` 行（参数同样是按工具定制的摘要，未登记的工具仍是 JSON）；`off` 隐藏逐条进度和汇总，但 agent 工具本身的调用行与结果行不变。
- 前台子 agent 的汇总跟随 agent 工具结果；`background: true` 的子 agent 在后台任务完成通知之前显示汇总。终端状态行沿用已有 spinner/steering 行，过长内容会按终端宽度截断。
## 启动提示与工具调用行

- 启动提示只列每轮常用的命令（`:session`、`:compact`、`:diff`、`:quit`/`:exit`、`:help` 与换行键）；`:add-dir`/`:dirs`/`:rm-dir`、`:checkpoints`、`:undo` 照样可用、照样补全，完整列表看 `:help`。
- 工具调用的第一行按工具显示内容而不是参数 JSON：`file_read` 显示路径（多个用 `, ` 分隔，带 offset/limit 时附 `:起-止`），`shell_exec` 显示命令本身（多行只显首行并加 `…`，有 timeout/background/every/watch_paths 时附简短说明），`file_write` 路径加字节/行数，`file_edit` 路径加处数，`file_remove`/`view_image` 路径，`file_list` 目录与 depth，`file_find` 的 pattern 加目录，`file_grep` 的 `"pattern"` 加目录/glob，`file_symbols`/`file_refs` 的符号（带路径），`web_fetch` 的 URL，`web_search`/`tool_search`/`session_search` 的查询词，`recall` 的 ref，`skill` 的技能名，`memory` 的动作加标题，`agent` 的子 agent 类型加任务（background 时注明），`session_message` 的 `list` 或 `→ <对方> "<文本首行>"`。
- 工作区内的路径显示为相对路径（工作区外、只读依赖根下的保持原样）；没有登记摘要的工具（如 `update_plan`、MCP 工具）、参数无法解析或摘要为空时仍显示原来的参数 JSON。
- 摘要与 JSON 用同一宽度截断（`callArgsLimit`），敏感值同样经 `redact` 处理；子 agent 的进度行（`chat.agent_progress: full`）用同一套摘要。只是显示：`run`、stream-json、ACP、trace 与转录里仍是完整的参数 JSON。

## 收工提示（`agents.nudge`）

- 回合正常结束后，suga 按两条规则看这个回合留下的证据：N1 改了文件但之后没跑过 `shell_exec`，或会话计划还有未完成项；N2 答复末尾在说“接下来我去…/让我先跑…/I'll check…”却没有工具调用（以问句结尾不算）。
- chat **只提示不推回**：命中时在答复下面显示一行灰色提示（`⚠ 最后修改 a.txt 之后没有运行 shell_exec；结尾说接下来我去跑测试`），不会自动开新回合，要不要继续由你决定；同一提示也写进 trace 的 `nudge` 事件（`action=hinted`）。
- `run` 会注入一回合把模型推回（N2 最多 2 次、N1 最多 1 次，`--verify` 时跳过 N1），`agents.nudge off` 全部关闭。

## 通知与终端显示

长回合结束或审批等待时的通知（`chat.notify`、`chat.notify_after`）、markdown 渲染、spinner、输入高亮与终端标题等显示开关见 `references/config.md` 的 `chat.*` 一节。

## 代码块与表格

- chat 的 Markdown 配色跟随 `chat.theme`（`references/config.md` 有完整说明）：dark 用默认调色板（标题粗体青 `1;36`、行内代码亮黄 `93`），light 是浅色终端背景用的一套（标题粗体蓝 `1;34`、行内代码品红 `35`、代码关键字蓝 `34`，引用与代码围栏的浅灰 `90` 改用普通前景色）。
- 代码块按围栏语言着色（`chat.code_highlight`，默认开）：注译、字符串、数字、关键字四类用 16 色 ANSI，其余保持终端本色；支持 go/python/javascript・typescript/shell/json/yaml/sql/rust/java/c・cpp 及其常见别名。流式下逐行着色，块注译跨行延续，行内字符串与行注译不跨行；未知语言、非终端或 `NO_COLOR` 不着色。
- 一个代码块超过 `chat.code_max_lines`（默认 40，`0` 不折叠）行后不再输出，结束时提示 `… 还有 N 行（:code 查看全部）`；`:code` 展开最近一个被折叠的块，`:code N` 展开上一条回答的第 N 个块。
- GFM 表格（表头行 + `|---|` 分隔行）按显示宽度对齐（中文算 2 列），表头加粗、分隔线用 `─`；比终端宽的表格原样输出，不截断。
