# 在 Gofer 中运行 Suga

Suga 提供两个 Gofer agent：`suga` 执行一次性 CLI 任务，`suga-acp` 提供常驻 ACP 会话。两者共用个人会话数据，默认位于启动 Suga 的用户主目录 `~/.suga/db/sessions.db`（单个 SQLite 库，WAL）。Gofer job ID 是执行记录，Suga session ID 是对话记录。

## 准备二进制与配置

在 Suga 仓库执行 `make install`。它将二进制安装到 `$(go env GOPATH)/bin`；先确认该目录位于终端和 Gofer server 进程的 PATH 中，再在新终端执行 `suga run --help`。主机 Gofer 配置的 `agents` 下增加以下两项：

```yaml
agents:
  suga:
    type: cli-agent
    command: suga
    args: [run, --output-format, stream-json, --append-system-prompt, "{{system_prompt}}", --prompt, "{{prompt}}"]
    interactive_args: [chat]
    session_inject: [--session-id, "{{session_id}}"]
    session_resume: [run, --output-format, stream-json, --resume, "{{session_id}}", --prompt, "{{prompt}}"]
    session_resume_interactive: [chat, --resume, "{{session_id}}"]
    read_only_args: [--read-only]
    output_format: ndjson
    ndjson_keep: [session, turn_start, turn_end, result, error, input_required]
    ndjson_stdout: final_text
    ndjson_stdout_path: result
    transcript_dialect: generic
    inject_process: [suga]
    session_family: suga
    deliver_command: [send, --session, "{{session_id}}"]
    deliver_stdin: true
    detect:
      command: suga
      args: [version]
  suga-acp:
    type: acp-agent
    command: suga
    args: [acp]
    session_family: suga
    acp:
      modes: {read_only: read_only}
      permission_policy: ask
      load_session: true
    detect:
      command: suga
      args: [version]
```

用 `gofer config validate server` 检查文件，再按当前 Gofer 服务的配置管理流程 reload，并用 `gofer agent list --runner server` 确认两项可用。`detect` 使用 `suga version`，其输出为单行 `suga <version> (<commit>)`。

Suga 创建会话时从外层到内层读取项目规则，每一级只读一个文件：文件名由 `rules.file` 决定（默认 `AGENTS.md`，可设为 `CLAUDE.md`），并在最前加入 `~/.suga/AGENTS.md`。展开后的真实文件路径会去重，同一个文件（包括经 `@` 引入的）只读取一次。规则里的独占行 `@相对文件` 展开一层；规则和 `--append-system-prompt` 在会话创建时保存，续接时只在规则文件变化后向下一轮补充更新消息。`SUGA_APPEND_SYSTEM_PROMPT` 可提供默认追加提示，命令行参数优先；不传或传空串都沿用会话已保存的追加提示（Gofer 模板在 job 没有系统提示时会渲染出空串），用 `--clear-append-system-prompt` 清空；`--no-project-rules` 可在调试时跳过项目文件。Suga 不再因 `GOFER_JOB_ID` 自动追加任何提示；Gofer job 需要的汇报提示（`gofer work report` / `gofer issue`）由 Gofer 经 `SUGA_APPEND_SYSTEM_PROMPT` 或 `--append-system-prompt` 提供（见「升级迁移」）。

## 本机 chat 会话送话

`suga chat` 会在 `SUGA_HOME/run` 登记当前进程并监听仅本机用户可访问的 socket，同时给会话取一个短名称 `<工作目录名>-<两位随机后缀>`（目录名转小写、只留 `[a-z0-9-]`、截到 20 字符，如 `suga-k7`）：横幅、`:session`、退出摘要（在续接命令前）、`suga ps`（含 `--json` 的 `name` 字段）都会显示。名称在首次登记时写入会话元数据（`short_name`），`--resume` / `--session-id` 续接沿用；若该名称当前被另一个活着的会话进程占用，本进程临时另取一个并在终端说明，存储的名称不变。可用 `suga ps --json` 查看活跃会话，用 `suga send --session <会话 ID|ID 前缀|短名称> <文本>` 或 `suga send --session <会话 ID|ID 前缀|短名称> --text <文本>` 送话：匹配顺序是完整 ID > 短名称（忽略大小写）> ID 前缀，多个位置参数用单个空格拼接，两种文本写法不能同时给出（同时给出时报错并以退出码 1 结束，不发送）；都不给时从 stdin 读取（上限 1 MiB）。文本以 `-` 开头时用 `--` 分隔，例如 `suga send --session suga-k7 -- -hello`。没有匹配进程时退出码为 3，多个匹配项为 2，stdin 超限为 4，成功送达或排队为 0，其他错误为 1。加 `--wait` 时，排队的消息要等到真正开始一轮才返回 0；若 chat 在此之前退出，消息被丢弃并返回 3。不加时排队即返回，chat 退出时会在其终端提示丢弃的条数。`run` 与 ACP 不登记会话 socket，因此不会生成短名称。

`suga send --session <会话 ID|ID 前缀|短名称> --image <路径> 看看这张` 向运行中的会话投递图片（`--image` 可重复，最多 16 张）：发送端先用 `run --image` 同一套规则读取、校验、缩放（相对路径按**发送端**的工作目录解析），把图片写入**目标会话**的 `sessions/<sid>/images/`（内容寻址，同一张图不重写），请求里只带 `{ref, sha256, media_type, width, height}` 引用；接收端逐张校验文件存在且 sha256 一致，任一不符就整条拒绝（响应 `image_rejected`），通过的图片作为只带引用的图片部件随这条消息走：不带 `--steer` 时进下一回合的用户消息，`--steer` 时进当前回合。新旧版本混跑时不会静默丢图：进程记录新增能力声明 `features`（本版本写 `["images"]`），目标记录里没有 `images` 时发送端直接报错退出（退出码 5，错误里写明会话与 PID，并提示“请在新版本里重新打开该会话”），**连文字也不投递**。当前模型不支持看图时按第一期规则降级为文字说明，响应里带 `warning`，发送端打到 stderr、退出码仍为 0。不带 `--image` 时请求与旧版本逐字节一致。

`--steer` 把文本插进**正在进行的**回合，而不是等下一轮：默认开着 `chat.steering` 且该会话正在跑回合时，模型在下一次请求前看到它，通常紧跟在刚看到的那次工具结果之后（与在 `↳ ` 提示符处键入的一句话走同一条路，消息带 `[来自 <source>] ` 标注、来源记为 `send:<source>`，trace 里是 `steer_delivered`）；会话空闲、回合正在结束或 `chat.steering: false` 时按普通投递排队，响应为 `queued`，客户端打印“未插入当前回合，已排队”；正在进行回合的插话收件箱满（每回合 8 条）时既不插入也不排队，响应 `queue_full`（退出码 1）。

`--steer --wait` 的完成点：插话被某次模型请求带上（`steer_delivered`）→ 返回 0；没被取到而该回合正常结束、插话作为紧接着的下一回合运行 → 该回合开始时返回 0；没被取到而回合被取消或失败（插话退回输入行、不自动运行）→ 返回 3（响应 `discarded`）。若插话已送达、之后该回合才被取消，`--wait` 已经返回 0，之后的退回只记 trace（`steer_returned`，已知限制）。插话与普通投递同权限，文本永远不被当作命令（chat 按来源判定），旧客户端不带 `mode` 字段时行为不变（follow-up）。

Gofer agent 可配置 `deliver_command: [send, --session, "{{session_id}}"]` 和 `deliver_stdin: true`，由 Gofer 在会话所在 worker 上调用 Suga；若要让投递插进正在进行的回合（gofer 的“插话”），在 `deliver_command` 里加上 `--steer`（再加 `--wait` 时，退出码 3 表示插话未送达、已退回）。Suga chat 忙时最多排队 8 条；空闲时送入下一轮。`send` 的 `--source <label>`（默认 `cli`）决定 chat 中的显示前缀 `[来自 <label>] `；文本首行已带 `[...] ` 标签时原样保留。送话作为注入输入记录。`UserPromptSubmit` 事件对这类输入（含 Stop block 注入的回复和 `[gofer job 完成]` 通知）带 `"injected": true`；终端手敲的输入不带该字段，其它事件也不带。`run` 和 ACP 续接同一会话时使用会话锁，避免与仍运行的 chat 同时写入；chat 退出后进程登记、socket 和锁会清理。

`interactive_args: [chat]` 允许 Gofer 在原进程退出后以交互终端启动 `suga chat --resume`；`session_resume_interactive` 给出续接参数。本机会话 socket `SUGA_HOME/run/<pid>.sock` 有路径长度上限：Windows 100 个 UTF-16 code units，Linux 107 字节，macOS 等 103 字节。超长时 chat 照常运行，但打印一行提示且不注册会话进程，该会话收不到 `suga send` 与 Gofer 投递；需要投递时把 `SUGA_HOME` 配置得更短。

模型地址与认证由启动 Suga 的用户配置。例如在该用户的 `~/.suga/config.yaml` 中设置：

```yaml
model: bailian/glm-5
providers:
  bailian:
    type: openai
    api_key: "${BAILIAN_API_KEY}"
    base_url: "${BAILIAN_BASE_URL}"
```

将 `BAILIAN_API_KEY` 和兼容 OpenAI API 的 `BAILIAN_BASE_URL` 放在同一用户的 `~/.suga/.env`，例如：

```dotenv
BAILIAN_API_KEY=<你的密钥>
BAILIAN_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
```

也可将这两个变量设为 Gofer server 进程可用的环境变量。仓库的 `config/config.yaml` 是字段样例；Suga 只加载 `~/.suga/config.yaml` 与 `~/.suga/.env`（或 `SUGA_HOME` 下的同名文件），不读取当前工作目录的配置和 `.env`。`SUGA_HOME` 替换 `~/.suga` 作为数据与配置根，`SUGA_CONFIG` 指定唯一配置文件。模型优先级是 `--model` > `SUGA_MODEL` > 配置 `model`（`<provider>/<model>`）；这些选项不会接收密钥。单轮步数上限同理由 `run --max-steps` > `SUGA_MAX_STEPS` > 配置 `max_steps`（默认 240）决定。OpenAI、Anthropic、DeepSeek 是内置预设，换用时改 `model` 并提供对应环境变量即可；完整字段见 [LLM 配置](llm-config-v0.md)。不要把真实密钥写入 agent argv、job `--env` 或任务文本：Gofer 会持久化 job 请求。会话库位置只由 `SUGA_HOME` 决定（`$SUGA_HOME/db/sessions.db`），`database_url` 与 `agentcore.run_root` 已移除。旧版本的库（`suag.db`、`agentcore/` 目录）不读取、不迁移、也不删除，仍留在磁盘上：旧会话 ID 无法用 `--resume`/`--session-id` 续接，Gofer 中登记的旧 Suag 会话（`session_family: suag`）无法再被唤醒或接管，需要新建会话。

## 批处理 CLI

`suga run --output-format stream-json` 的最终 `result` 事件在能确定本回合最后一次模型请求和模型窗口时还会带 `context`：

```json
{"context":{"used":550,"window":1000,"share":55}}
```

`context.used` 是最后一次模型请求的输入 token 数（包含缓存 token），`context.window` 是模型上下文窗口；`context.share` 是 `used * 100 / window` 向下取整的整数百分比，因此超过窗口时可能大于 `100`。这与 ACP 的 `usage_update` 中 `used`/`size` 口径一致，`share` 只是便于客户端直接显示的补充字段。窗口未知或本回合没有模型调用时省略整个 `context` 对象；其它 `usage` 与 `session_usage` 字段的累计口径不变。

CLI 可在 agent 的 `args` 中加入 `--mcp-config <file>` 接入本机 stdio MCP 服务；可重复指定多个 JSON 文件。文件格式为 Claude 的 `mcpServers`，例如 `{"mcpServers":{"gofer":{"command":"gofer","args":["mcp"]}}}`。Suga 的 YAML 配置也可在 `mcp.servers` 下列出默认服务。MCP 子进程继承启动环境（`GOFER_*` 等保留），但会剔除凭据变量：名称以 `_API_KEY`、`_AUTH_TOKEN` 结尾的变量及 suga 自己读取的 LLM 密钥（`ANTHROPIC_API_KEY`、`OPENAI_API_KEY`、`DEEPSEEK_API_KEY`），再叠加各服务的 `env`；服务 `env` 里显式配置的同名变量照常传入。MCP 工具名保持原名；若与内置工具或更早配置的 server 的工具重名，后者被丢弃并记录警告，不会覆盖。需要让 Gofer job 调用其工具时，须在可执行路径和运行环境中提供 `gofer`；不要把密钥放在参数或文档中。

MCP 工具默认一律不放行，由 YAML 中两个 glob 列表控制，条目形如 `server/工具glob`：`tools.auto_allow` 用于普通运行；`--read-only` 只认 `tools.read_only_allow`（条目必须带服务名，`auto_allow` 不会扩大它）。两者都为空时所有 MCP 调用被拒绝。Gofer 只读 job 需要的典型配置见「升级迁移」。其余 MCP 工具在 CLI 中被拒绝，即使带 `--allow-write`；拒绝信息作为工具错误交给模型，回合继续。拒绝语义见下文“工具拒绝语义”。需要逐次审批 MCP 写工具时使用 ACP。

### 内置工具与相关配置

内置工具来自同一份清单，run、chat、ACP 共用：

- 读工具（只读模式也有，chat 不询问）：`file_read`（按行分段，`offset`/`limit`，默认 2000 行，带行号；`paths` 一次读最多 10 个文件，`offset`/`limit` 对每个文件生效，某个文件出错只在它的位置报错）、`file_list`（`depth` ≤ 5、`max_entries` 500；递归时不进入被忽略的目录）、`file_grep`（RE2 正则，`path`/`glob`/`ignore_case`/`output: content|files`/`max_results` 默认 100 上限 1000）、`file_find`（`**` glob，按修改时间新到旧，默认 200 条；`file_grep`/`file_find`/`file_list` 跳过 `.git`、`node_modules`、`vendor`、`dist` 与各级 `.gitignore` 匹配，支持 `!` 取反与 `**`，结果末尾注明跳过了哪些，`no_ignore: true` 可包含）、`web_fetch`（http(s)，`url` 或 `urls`（1–5 个，并发，按输入顺序分段，单段失败不影响其他）二选一，20 秒、下载上限 5MB、最多 5 次跳转，HTML 转为保留标题/链接/代码块的文本，每个 URL 默认返回 20000 字节）、`web_search`（配置 `serpapi_key` 时注册）、`skill`（见下）。`update_plan` 各模式都有。
- 写工具（`--allow-write` / `tools.allow_write` / ACP `default` mode）：`file_write`（整文件写）、`file_edit`（按唯一字符串替换，`replace_all` 替换全部，`edits` 一次多处且全成或全不改，保留 CRLF）、`file_remove`（移入 `$SUGA_HOME/trash/` 保留 7 天，单次最多 1000 个文件；被 `.gitignore` 忽略的目录与 `node_modules`/`vendor`/`dist` 不遍历、直接永久删除并在结果中列出，被忽略的单个文件如 `.env` 仍进回收区；拒绝工作区根、自身 `.git`、主目录、通配符与工作区外路径）、`shell_exec`。
- 工作区外的目录：`run` 与 `chat` 可用可重复的 `--add-dir <绝对目录>[:ro|:rw]`（默认 `:ro`）给本次会话追加目录，`SUGA_ADD_DIRS` 按系统的路径列表分隔符分隔（Windows 为 `;`），适合由 Gofer 按 job 注入。`:ro` 目录加入本会话的只读根（与 `tools.read_roots`、自动识别的依赖根合并去重），`:rw` 目录同时允许 `file_write`/`file_edit`/`file_remove` 写入，审批、危险删除拦截以及拒绝工作区根/自身 `.git`/主目录的规则与工作区相同；`shell_exec` 的 cwd 仍是工作区，需要执行命令时先确认工作目录。目录记入会话元数据 `add_dirs`，`--resume`/`--session-id` 续接沿用，同路径重给时以本次的权限为准；`--read-only` 下它们只以只读生效（会提示，会话里保留你要求的权限）。环境段会列出每个附加目录及其权限，`turn_context` 事件带 `add_dirs`（`path:ro`/`path:rw`，即本次实际生效的权限）便于核对。只接受已存在的绝对目录（会清理 `..`），工作区与附加目录之外的路径仍被拒绝；ACP 的 `session/new` 带 `additionalDirectories` 目前被拒绝。交互式 chat 里还可以用 `:add-dir <绝对目录>[:ro|:rw]`、`:dirs`、`:rm-dir <路径>` 在对话中途改这些目录（同样写入会话元数据、下一次工具调用即生效），而文件工具越界时 chat 会先问一次 `授权访问 <目录>？[r] 只读 [w] 读写 [n] 拒绝`（`chat.ask_outside_dirs`，默认开）。这两条都只认本地键盘：Gofer/`suga send` 投递的文本即使是 `:add-dir` 也只是普通消息，不会改授权（需要按 job 授权时用 `SUGA_ADD_DIRS` 或 `--add-dir` 注入）。
- 文件类工具都经 `os.OpenRoot` 限制在工作区内，不跟随指向外部的 symlink；`file_list`/`file_grep`/`file_find` 不进入 `.git`、`node_modules`、`vendor`、`dist` 与根 `.gitignore` 的简单模式（不支持 `!` 与子目录 `.gitignore`），`file_grep` 跳过二进制与超过 1MB 的文件；搜索超过 10 秒返回已有结果并注明不完整。
- `web_fetch` 在拨号时检查实际连接的 IP，拒绝回环、私网、链路本地（含云元数据地址）、CGNAT 与组播地址，跳转与 DNS 重绑定同样受限；不读取环境代理（`HTTPS_PROXY` 等）。需要代理才能访问的站点，设 `tools.web_fetch.proxy`（`http://`、`https://` 或 `socks5://` 地址，如 `http://127.0.0.1:7890`）：此时连接只发往代理，改为在请求前与每次跳转时解析目标域名并校验其 IP，解析失败也拒绝（代理端若对同一域名解析出不同地址则无法察觉，保护弱于直连）；格式错误时启动报错。`tools.web_fetch.allow_private: true` 可放开内网限制。请求头 User-Agent 由 `tools.web_fetch.user_agent` 决定（空 = `suga-web-fetch/1.0`，可改成浏览器 UA）。
- skills：启动时扫描一次 `<工作区>/.agents/skills`、`<工作区>/skills`、`~/.agents/skills` 下的 `SKILL.md`（frontmatter 含 `name`、`description`，可选 `allowed-tools` 仅展示），同名时工作区优先，无效文件记警告后跳过。段上下文消息按数量呈现 skills：总数不超过 `skills.list_max`（缺省 15）时全部列出“名字 — 描述”（描述截到 160 字符）；超过时只列工作区内的 skills（带描述），其余只列名字（名字合计上限 3000 字节，超出的只给数量），模型可直接按名字加载，或用 `skill` 工具的 `query`（关键词，名字命中优先，中文按两字切分；`*` 按工作区/用户分组列出全部名字）检索后再按名字加载。模型用 `skill`（`name`）取正文与目录文件列表，`file` 参数只能读该 skill 目录内的文件。需要每次都生效的 skill 可预加载：在 SKILL.md frontmatter 写 `metadata: {always_load: true}`（或顶层 `always_load: true`），或在 `~/.suga/config.yaml` 设 `skills.always_load: [名字]`；其正文（每个上限 8000 字节）放进段上下文消息。

`shell_exec` 在 Windows 用 PowerShell（优先 `pwsh`，否则 `powershell`），输出固定 UTF-8，返回原生命令的退出码；其他系统用 `sh -c`。每次调用可传 `timeout`（秒，默认 120），前台运行超过 `tools.shell_background_after`（默认 120s）且启用了任务管理时会转为后台任务，工具结果保留交接前的输出并给出任务 id；设为 0 关闭。`tools.shell_timeout_max` 仍是硬上限，超过后整组进程（Unix 进程组 / Windows job object）被终止；后台残留进程占住输出时最多再等 2 秒。命令以 `GIT_TERMINAL_PROMPT=0`、`GIT_PAGER=cat`、`PAGER=cat` 运行，没有标准输入；suga 自身的 `SUGA_HOME`、`SUGA_CONFIG`、`SUGA_MODEL` 不传给命令（在 shell 里再调用 suga 时用默认的 `~/.suga`，需要时在命令里显式设置）。输出去掉 ANSI 颜色码。

后台任务（`shell_exec` 传 `background: true`，写模式才有）在两种 agent 下生命周期不同：`suga`（run）在回答前若仍有任务在跑，会提醒模型一次先 `task wait` 或 `task kill`，进程退出时终止剩下的任务并写到 stderr；`suga-acp` 的任务属于 `suga acp` 进程，跨 prompt 继续运行，结束提示进入该会话的下一个 prompt，`session/cancel` 只取消当前回合，连接关闭（Gofer 结束会话）时终止仍在运行的任务，trace 里记为 killed。所以一次性的 ACP 作业里，需要结果的任务要在本回合内 `task wait`。

配置项（`~/.suga/config.yaml`，均有缺省值）：

- `tools.parallel_reads`（缺省 `true`）：同一次回复里的多个本地读取调用（`file_read`、`file_list`、`file_grep`、`file_find`）并发执行；审批、污染追踪与 trace 仍按调用顺序进行。
- `tools.max_output_bytes`（缺省 8000）：单次工具结果完整写入会话的上限；`file_read` 不受它限制，按 `tools.read_page_bytes`（缺省 32 KiB，`paths` 一次最多四页）按行分页并提示续读的 `offset`；其他工具超出时原文（脱敏后）存入 `~/.suga/sessions/<id>/outputs/`，会话只保留 ref 与头尾各 2000 字节。
- `tools.max_spill_bytes`（缺省 1048576）：单次工具（含 MCP）原始输出上限，超出部分按 UTF-8 边界保留头尾截断。
- `tools.shell`：显式指定 shell（命令名或路径，如 `bash`）；不自动探测。
- `tools.shell_timeout_max`（缺省 600）：`shell_exec` 的 `timeout` 上限（秒）。
- `tools.shell_background_after`（缺省 120s）：前台 `shell_exec` 超过此时长后转为后台任务；设为 `0` 关闭，硬上限仍由 `tools.shell_timeout_max` 控制。
- `tools.dependency_roots`（缺省 `true`）、`tools.standards_roots`（缺省 `true`）与 `tools.read_roots`（目录列表，支持 `~`，须为已存在的绝对路径）：工作区外的只读目录。`file_read`、`file_list`、`file_grep`、`file_find` 可用绝对路径读取其中文件，写工具与 `shell_exec` 的工作目录不受影响。自动识别的依赖根只取源码目录：Go 模块缓存（`GOMODCACHE`，或 `go env -w` 写的值，或 `GOPATH/pkg/mod`）、Go 标准库（`GOROOT/src`，未设置时按 PATH 中 `go` 的位置推断）、Rust crate 源码（`CARGO_HOME/registry/src`），不会放开整个 `~/.cargo`、`~/.m2` 这类含凭据的目录；Java 依赖多为 jar，需要时把解压后的源码目录写进 `tools.read_roots`。开启 `tools.standards_roots` 后，`IDEV_STD_RELEASES_ROOT` 或兼容的 `INHERE_DEV_STANDARDS_ROOT` 若指向已存在目录就加入 idev-std 发行根；再从工作区向上查找带 `standards.json` 的工作空间根，把存在的 `<工作空间根>/tmp/idev-std` 加入加载产物根。识别结果列在提示词的环境一节，grep/find 在这些目录里输出绝对路径；重复路径会合并。开启 `tools.skill_roots`（缺省 `true`）后，工作区外的 skill 根（`SUGA_HOME/skills`、`~/.agents/skills`，目录存在才收录；工作区内的 skill 根本就可达，不重复加入）也加入只读根路，模型读 skill 的 `references/` 等文件不再触发目录授权，写工具与 shell 的审批不变。
- `mcp.call_timeout_sec`（缺省 120）：单次 MCP 工具调用的超时。
- `mcp.startup_timeout_sec`（缺省 30）：单个 MCP server 的 initialize + `tools/list` 发现超时；每个 server 独立计时，一个慢 server 不拉长其他 server 的启动。
- `mcp.servers[].url` / `transport` / `headers`：Streamable HTTP server。`url` 与 `command` 互斥，`transport` 取 `stdio` 或 `streamable_http`（不写时按字段形状推断，旧 `sse` 明确报错）；`headers` 只在 HTTP 下有效，值里的 `${VAR}` 从进程环境或 `$SUGA_HOME/.env` 展开，日志、manifest 与诊断不回显值。HTTP 与 stdio 的 MCP 工具在权限、`tools.deferred` 与 preferred 上语义相同；同名工具仍按注册顺序 first-wins，冲突可在运行时 manifest 的 `tool_conflicts`（server/tool/kept_by）中查到。

另有三组工具，run、chat、ACP 均有：

- `session_search`（全文检索过往消息、工具结果与摘要）与 `recall`（按 ref 读命中附近的消息，或读已外置输出的带行号文本）：只读，但结果算外部内容，用过即把会话标为已污染。
- `suga_manifest`：以 JSON 返回能力清单，等同在主机上跑 `suga inspect manifest --json`（模型、工具及元数据、MCP、skills、记忆后端及选择原因、输出上限）。
- `memory`：长期记忆，见下节。

### 写操作快照与回滚

可写的 `suga run`、chat 和 ACP 共享同一套写操作快照配置；`file_write`、`file_edit`、`file_remove` 在实际 I/O 前保存写前状态，快照位于 `$SUGA_HOME/sessions/<session-id>/snapshots/`。默认单次 16 MiB、会话 256 MiB、全局 1 GiB，保留 20 个已完成回合或 7 天。默认 `tools.snapshots.on_error: deny` 时快照不可写、清理或状态不确定会在写盘前拒绝；只有显式 `continue` 才允许写入并标明不可恢复，因此 Gofer 派发的 `suga run` 也遵守 fail-closed。

ACP 没有内置 undo 命令，但它的文件写操作照常创建快照；可在同一用户的 Suga 环境中用 `suga session undo <session>` 列出或回滚。`shell_exec`（含后台任务）不自动回滚，`file_remove` 的回收区仍只保留 7 天。快照不进模型上下文、transcript 或 export；回滚后的 `suga.undo` 通知在下一次显式回合投递，`suga inspect trace <session> --json` 可查看 undo 事件和工具事件的 `attrs.snapshot`。关闭 `tools.snapshots.enabled` 只停止新快照，不删除已有数据或命令面。

### 子 agent（`agent` 工具）

`agent` 工具把一件独立任务交给只读子 agent：`type` 为 `explore`（缺省，工作区只读工具）或 `research`（再加 `web_fetch`、`web_search`），它只把最终报告带回来，`description` 就是它的名字。完整说明（两种类型、后台运行、上限与 `agents.*` 配置、用量与污染口径、子会话的列表/搜索/gc）见 [README 的“子 agent”一节](../../README.zh-CN.md#子-agent)。这里只列接入方会看到的部分：

- **只读会话也可用**：子 agent 自己只读（不能写文件、执行命令、询问用户，也不能再派子 agent），所以 `agent` 工具在 `read_only` mode、chat 默认模式与 `run` 缺省模式都注册，且不触发审批。只读模式下注册的是 `task`（`list`/`output`/`wait`/`kill`），用于 `background: true` 的子 agent；`shell_exec` 的 `background` 仍只在写模式出现。
- **ACP 标题**：每次子 agent 调用是一条普通 `tool_call`，标题为 `agent: <description>`（`toolCallId` 与状态序列与其它工具一致），并按只读工具放行；子 agent 内部的工具调用不发给客户端（第一期），只在 trace 里可见。
- **用量**：子 agent 的模型调用计入调用它的那次 run——prompt 进行中发出的 `usage_update`（顶层扁平键与 `_meta.run`）已包含子 agent 的 token 与成本；`_meta.session` 与顶层 `cost.amount` 是会话累计，等于父会话加它的全部子会话。后台子 agent 结束时会话若没有进行中的 run，用量暂存到下一个 run 一起上报（不在已结束的 run 之后补发）。
- **观测**：父会话里有一条 `agent` 工具事件（attrs 含 `child_session_id`、`agent_type`、`steps`、`tool_calls`、`input_tokens`、`cache_read_tokens`、`output_tokens`、`cost_usd`、`stop`），后台子 agent 结束时是一条 `kind=agent` 的 task 事件；子会话自己的事件用 `suga inspect trace <子会话 id>` 查看，`suga session list --agents` 会列出子会话。

### 输出治理

工具结果超过 `tools.max_output_bytes`（缺省 8000）时，脱敏后的原文存到 `$SUGA_HOME/sessions/<会话 id>/outputs/`，会话里只有头尾各 2000 字节与 ref（`out:<session>:<hex>`），模型用 `recall` 分页读、用 `session_search`（带 `ref`）在其中检索；`file_read` 自己分页，不外置；出错结果不外置。输出外置不影响 stream-json 与 ACP 的事件格式。`tools.max_spill_bytes`（缺省 1 MiB）是单次原始输出的上限。`tools.spill.max_bytes`（缺省 1 GiB）是全部 spill 文件的逻辑字节配额（`.gz` 按压缩前字节计）：超额时新的 spill 返回可诊断错误、结果只留头尾，**不会**自动删旧输出；`suga data compress-spill` 默认只报告文件数/逻辑字节/占用与是否超额并试算压缩后字节，加 `--apply` 把 mtime 超过 `tools.spill.compress_after`（缺省 7d）且会话不活跃的 `.txt` 压成 `.txt.gz`（`--apply` 需无其他 suga 进程，复用 `gc --vacuum` 的维护窗口）；`out:<session>:<hex>` ref 不变，`recall`/`session_search(ref)`/`export --with-outputs` 透明读两种形式。`tools.minimize_output`（缺省 `true`）：shell 结果进入上下文前先整理——去掉颜色码与进度重绘、折叠连续相同的行、测试与构建输出按输出规则裁剪（全部通过时只留各包结果和一行汇总、有失败时只留失败用例及其信息）；原文保存为 ref 并在结果中注明，`suga inspect trace` 的工具行显示 `tidied(raw=…)`。内置规则覆盖 go test、cargo test、jest/vitest/mocha 与 npm/pnpm/yarn/bun test、pytest、Maven、Gradle、git log/diff/status、rg/grep 的命中以及 flutter test/dart test、flutter analyze/dart analyze、flutter build、flutter pub get，规则还能按正则改写整理结果（`replace`）、按捕获的键分组（`group_by`）、保留首尾行并给自己设更低的门槛（`min_bytes`/`min_saving`），字段、执行顺序与覆盖方式见 `pkg/agentcore/tools/native/outrules/README.md`；用户级 `$SUGA_HOME/output-rules.yaml`、项目级 `<workspace>/.suga/output-rules.yaml` 依次覆盖（同名替换、新名追加、`enabled: false` 关闭），文件有错时启动警告并跳过整个文件。命令本身用管道过滤了输出（引号外的 `|`，不含 `||`）时不套用规则。`suga inspect stats --json` 的 `tools` 项给出 `raw_bytes`、`inline_bytes`、`spilled`；`suga inspect trace` 的 `turn_context` 行记录每回合的模型、模式、工具集哈希与是否已污染。

### 记忆

- 范围与后端：用户记忆在 `$SUGA_HOME/memory/user`；项目记忆后端的选择顺序为 `memory.project.backend` > 仓库根 `MEMORY.md`（files，条目在 `.suga/memory/<id>.md`）> 仓库根有 `.gofer/tracker` 时选 gofer > `$SUGA_HOME/projects/<key>/memory`（`<key>` = 仓库目录名 + 完整路径短哈希，如 `suga-5cb0fa32`；旧版本按整条路径生成的目录会在首次使用时自动改名过来）。`memory.user.backend` / `memory.project.backend` 可选 `files`、`exec`（项目另可选 `gofer`），`exec` 需配 `memory.<scope>.command`（可选 `args`、`timeout_sec`）。完整字段见 `config/config.yaml`。
- 仓库根有 `.gofer/tracker` 且没有 `MEMORY.md` 时，项目记忆自动选 gofer 后端：每条项目记忆写成带 `suga` 标签的 gofer memory（key 形如 `suga-<kind>-<id>`，来源 `suga:<session>`），**会进入该仓库所有 agent 的开场上下文**，所以应简短且对各 agent 都有用；suga 只管理 `suga` 标签条目，不读写、不删除其他 gofer memory。不想写进 gofer 时设 `memory.project.backend: files`。
- 索引在每段开始时冻结进段上下文消息，本段内写入下一段可见，开启新段时刷新。
- 污染与确认：会话读过外部内容后（`web_fetch`、`web_search`、MCP 工具、`session_search`、`recall`，以及 `memory.taint_tools`、`memory.taint_on_spill`（缺省 `[shell_exec]`）列出的工具）写记忆需要确认，由 `memory.write_tainted`（`ask` 缺省 / `deny` / `allow`）决定。ACP 下确认走 `session/request_permission`（标题“memory: 会话读过外部内容，确认写入记忆”，可用 `gofer job interactions` / `answer` 处理）；`suga run` 没有交互，直接拒绝；chat 在终端询问。`--read-only` 与 ACP `read_only` mode 下 `memory` 只允许 `get`/`search`；`run` 默认模式可写记忆（它是 suga 自身状态，不是工作区写入）。`memory.write: off` 让工具整体只读。

系统提示词只含稳定内容（身份、使用约定），同一模式下逐字节不变，利于提供方做前缀缓存。环境（系统与 shell、工作区、只读或可写及开启写工具的方法）、项目规则、记忆索引、skills 列表在每段开头的上下文消息里；段内规则变化以“上下文更新”消息追加，并随会话回放。

Gofer 中两种 `suga run` 用法：

- 只读（模板缺省）：模型可读、列、搜索文件，抓取网页，使用 skills；要改文件或跑命令时会说明需要 `--allow-write`。
- 可写：在 `suga` agent 的 `args` / `session_resume` 中加入 `--allow-write`，模型可写文件、执行 shell（run 没有逐次确认，适合可信的本机任务）。需要逐次审批时用 `suga-acp`：审批请求的等待时长与超时处理由 Gofer 的 `permission_policy` 决定。

### chat 的写权限与终端确认

仅作用于 `suga chat`（`run` 与 ACP 行为不变）：

- `tools.allow_write`（布尔，缺省 `false`）：为 `true` 时 chat 启用写文件与命令工具，等同 `--allow-write`。`run` 仍需 `--allow-write`；`--read-only` 优先；ACP 不读取该项；非布尔值使 chat 启动失败。
- `tools.approval`（`ask` 缺省 / `auto`）：`ask` 时，只读名单（`file_read`、`file_list`、`file_grep`、`file_find`、`web_fetch`、`web_search`、`skill`、`update_plan`、`tool_search`）之外的所有非 MCP 工具（目前是 `file_write`、`file_edit`、`file_remove`、`shell_exec`；`file_remove` 的询问显示路径、进回收区的文件数与字节数，以及将直接删除的被忽略目录）执行前在终端询问；`file_edit` 的询问显示路径与原文/改为各前 20 行执行前在终端询问，显示完整命令 / 路径与字节数 / 查询（已脱敏、不截断）。回答 `y`/`yes` 允许一次，`a`/`always` 在本次 chat 进程内放行后续同名工具，其他（含回车）拒绝，回合继续。询问中 Ctrl-C / Ctrl-D 只取消本回合并显示“已取消”，不退出 chat；回合中提前敲的输入不会被当作回答。提示符处按一次 Ctrl-C 只清掉已输入内容并提示，3 秒内再按一次才退出 chat；Ctrl-D 直接退出。`auto` 不询问。
- 标准输入不是终端时，需确认的工具一律拒绝，并提示设置 `tools.approval: auto`。
- 同一回合 3 次拒绝结束回合；回合末显示“本轮被拒绝的工具：…”。
- MCP 工具不询问，仍按 `tools.auto_allow` / `tools.read_only_allow`。

```powershell
gofer job run --runner server --project my-tools-dev --cwd inhere-tools/suga --agent suga --read-only --prompt "检查当前项目"
gofer job show <job-id>
gofer job logs <job-id> --stream stdout --tail -n 20
gofer job resume <job-id> --prompt "继续上个问题"
```

首个 CLI job 的 `stdout` 是 Gofer 提取的最终答复；`session_id` 可从 `job show` 读取。Gofer 0.118.0 的 `job resume` 已验证继承源 job 的环境，并将最终答复投影到 `stdout`；原始 NDJSON 保留在 `stderr` 供排查。真实凭据仍应放在 Suga 私有配置或稳定的进程环境中。历史作业若使用已移除的 `suag-cli` agent 键，不能直接用当前的 `suga` 键续接。

### 会话交接、观测与 deferred 工具

- Suga 的进程日志写在 worker 的 `$SUGA_HOME/logs/suga-<YYYYMMDD>.log`（文件名带日期，按大小切分，`log.keep` 份保留），默认不写 stderr（job 的 `stderr` 里只有面向人的提示与 Gofer 自己的事件行）；要在 job 日志里同时看到 suga 的详细日志，给该 agent 加 `env: {SUGA_DEBUG: "1"}`（或配 `log.level: debug` + `log.stderr: always`）。
- `--from <session>`：`suga run --from <旧会话> --prompt ...` 新建会话并继承来源的摘要、计划与已加载工具（来源只读，不能与 `--resume` 同用）。可同时传 `--session-id` 指定新会话 ID；交接失败（如生成摘要失败）时不会创建会话，同一 ID 可直接重试。Gofer 目前没有对应的模板变量（Gofer 侧计划以 `--from-session` 参数支持），在此之前需要在 job 的 agent args 中手工写入 `--from <session-id>`。
- 会话不存在：`run`/`chat` 的 `--resume` 目标或 `--from` 来源不存在时，退出码为 4，stderr 输出 `session not found: <id>`；被 Ctrl-C / SIGTERM 中断时退出码为 130，stderr 输出 `已中断`；其他失败为 1。旧版本的会话 ID 在新库中都属于这种情况，可据此改用 `--from` 或新建会话。
- `suga session list [关键词] [-n 10] [--since 7d|日期] [--until …] [--cwd 路径|.] [--json]`：列出会话，按最近活动排序，默认 10 条（`-n 0` 全部）；关键词全部命中才列出，匹配标题、id、目录、模型，或对话内容（全文索引，显示命中片段）；`--cwd` 按目录前缀过滤（不区分大小写，`.` 为当前目录）。表格含更新时间、id、模型、suga 版本、标题、目录。
- `suga session compact <session> [--model <ref>] [--json]`：立即给会话开新段（chat `:compact` 的命令行形式，`--json` 输出换段前后的 token 估算与新段序号）；不传 `--model` 时用会话已选的模型写摘要，脚本可以在两轮 `suga run` 之间调用，用于长会话的强制压缩。
- `suga inspect trace <session> --web [--addr 127.0.0.1:0]`：在本机起一个只绑回环地址的网页，按时间线查看会话（瀑布图、按类型/错误/慢节点筛选与搜索、点开看完整参数与结果、思考与正文、审批与前缀对比；每回合上下文组成的堆叠条与各部分原文；每次调用的缓存命中；`--analyze` 结果），地址带 `#ev=<事件 id>` 或 `#tab=context` 可直接定位；页面以只读方式打开会话库（库不存在或未初始化时报错，不创建、不迁移），头部带“自动刷新”开关（默认关闭），打开后只拉取新增：回合进行中每 5 秒，空闲时每 15 秒检查新回合，页面手动刷新仍是最新。
- `suga inspect trace <session> [--turn id] [--json] [--analyze]`（`--analyze` 输出循环分析：步数、并行调用、重复读、写后回读、重复命令、首次通过后的测试次数、阶段拆分（探索 / 首次写入到转绿 / 转绿后）、单处编辑步、未命中缓存输入的来源拆分等）、`suga inspect stats [--session id] [--since 7d] [--json]`、`suga inspect export <session> [--redact] [-o file]` 读取同一个会话库，在运行 Suga 的主机上执行（Gofer worker 所在用户的 `SUGA_HOME`）；`export --redact` 去掉工具参数，适合外发。
- `run` 的 stream-json 另有两种新增行：`tool_call`（`name`、`args` 摘要）与 `tool_result`（`name`、`is_error`、脱敏并截断的 `summary`）；已有行不变。未配置 `ndjson_keep` 的调用方会收到它们，上面的示例 `ndjson_keep` 不含它们，需要在 Gofer 中显示工具调用时再加入。`suga inspect trace` 会显示 plan 行（文本 `plan ✔/▶/○ 步骤`，`--json` 为 `{"kind":"plan",...}`）。
- `run` 的 stream-json 在 `plan` 事件上输出完整计划；`ndjson_keep` 示例未保留它，需要在 Gofer 中显示计划时把 `plan` 加入 `ndjson_keep`。
- `tools.deferred`（`auto`/`always`/`never`，默认 `auto`）在 `~/.suga/config.yaml` 中配置。MCP 工具多时被延迟加载，模型通过内置 `tool_search` 取回；Gofer 不需要额外配置。
- 历史折叠（默认关闭）：`context.fold_every`（默认 0 即关闭，设为如 10 开启）与 `context.fold_keep`（默认 5）。最近 5 步之前的步骤在请求中去掉思考内容，超过 1000 字符的字符串参数换成说明；折叠点每 10 步前移一次。只影响请求，存储不变；`suga inspect trace` 显示 `fold` 行。
- `compact_model` 指定分段交接摘要（自动、`:compact`、空闲整理）使用的模型；会话按段组织，旧段可用 `session_search`/`recall` 取回；`providers.<id>.models.<model>.context_window` 覆盖上下文窗口。

### run 的 stream-json 输出与 chat hooks

- `run --output-format stream-json` 先输出 `session` 事件，再输出过程事件，最后输出一行 `result`。`result`、`input_required`、`error` 都带 `usage`（本回合）和 `session_usage`（会话累计，持久化在会话元数据中）。两者均含 `input_tokens`（不含缓存）、`output_tokens`、可选的 `cache_read_tokens`/`cache_write_tokens`，以及 `total_tokens`（输入、输出与缓存之和）。
- 因上限收尾的回合（目前只有主 agent 的步数上限）在 `result` 行带 `"wrap_up":"max_steps"`，正常答复不带该字段（为空省略）；`--output-format text` 时同样的原因打到 stderr（如“本轮因步数上限（100 步）收尾，以上答复只基于此前已获得的信息”），stdout 仍只有答复正文。收尾指令同时要求模型在答复开头如实写明真实原因（不得写成超时），Gofer 侧判断短答复时可直接认 `wrap_up`。
- 回合因 provider 错误失败时，`error` 行另带 `error_kind`：`provider_transient`（网络错误、HTTP 408/429/5xx、响应头超时等重试用尽，或流被切断/截断，错误里含 `stream truncated`，例如响应被网关切断、最后一行没有换行）、`provider_rejected`（provider 用 4xx 拒了这次请求：400/401/403/404/422）或 `provider_bad_response`（响应体或流事件不是合法 JSON、答复内容缺失、API 自身报错）；其他失败（取消、工具/存储错误）不带该字段；`context_length_exceeded`（OpenAI code）与 Anthropic 的 `prompt is too long` 也归入 `provider_rejected`，错误会提示“上下文超长，可 :compact 或开新会话”，不重试且不换同一 provider 的模型；若有其它 provider，可能换到更大上下文窗口的 provider。`suga inspect trace --json` 的 turn 事件 attrs 里同样有 `error_kind`。模型在中间步失败与结尾失败一样都带（错误文本里能看到具体状态码）。`provider_quota` 且 provider 给出了额度恢复时间时，`error` 行另带 `reset_at`（RFC3339）。Gofer 据此决定是否让同一会话续跑（gofer v0.168.0 起只有 `provider_transient` 自动续接），例如 `provider_transient` 时按“上一回合因模型请求失败中断，请从中断处继续”再跑一回合；`provider_rejected` 里 400/422 一般要改请求（模型不支持某个参数、提示词太长），401/403/404 则需要改配置（密钥、模型名），重启一回合通常无用。
- 每次模型调用结束后，`run` 还会输出一行 `usage`：`{"type":"usage","usage":{input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,total_tokens,cost_usd,tool_calls}}`，为**本次 run 的累计值**（run 口径，含 compact 调用，`input_tokens` 不含缓存；`tool_calls` 为本次 run 的工具调用次数，含被拒的）。`turn_end` 与 `result` 带同名 `usage`，`result` 的是最终值；`usage` 逐行单调递增，因此调用方可以在运行中途读取。Gofer 已读取该行并显示 `tools N`，`--max-tokens` 在运行中途就能生效，不必等到 `result`。
- chat hooks：`hooks.command`（argv 列表，不经 shell）在 SessionStart、UserPromptSubmit、PreToolUse、PostToolUse、Stop、SessionEnd、Interrupt 事件时被调用，事件以一行 JSON 写入其 stdin。未设 `hooks.command` 时不启用，`SUGA_HOOKS=off` 可整体关闭。`hooks.timeout_ms` 限制普通事件；`hooks.stop_args` 只追加到 Stop 调用，且必须同时设 `hooks.stop_wait_sec`（Stop 最多等 `stop_wait_sec+2` 秒，否则按 `timeout_ms`）。PostToolUse 只对匹配 `hooks.post_tool_use.tools` glob 的工具发送，设了 `hooks.post_tool_use.name` 时改用该名称。PreToolUse 只对匹配 `hooks.pre_tool_use.tools`（以及 `.name`）的工具在**执行前**发送：回答里的 `hookSpecificOutput.permissionDecision: deny` 拒绝该调用，`updatedInput` 改写参数（改写后重走审批），命令超时/失败/不可解析或宿主 hook panic 一律按拒绝执行（fail closed），`allow` 不绕过审批模式。
- 后台 Stop hook：`hooks.stop_background: true`（默认 false，仅 chat）把 Stop 作为 `hook` 类后台任务运行，不再在提示符处等待：用户输入、投递与任务唤醒都不会中断它，耗时的 Stop（如跑全量测试）因此能跑完。hook 返回 block 加 reason 时产生通知 `Stop hook asks to continue: <reason>`：空闲的 chat 被唤醒并开始注入回合，回合进行中则在下一次模型请求前带上（带 `stop_hook_active=true`，语义同同步等待）。新的 Stop 到来时先终止还在跑的上一个；没有空闲任务槽位时退回原来的等待。超时：设了 `hooks.stop_wait_sec` 时仍用 `stop_wait_sec+2` 秒，否则不再套用 `hooks.timeout_ms`，而是由任务管理器的 `tasks.max_duration`（默认 2h）限制。
- `suga run --injected` 把本轮输入标记为非人工输入，transcript 和 hook 事件中都会体现。
- `suga run --max-steps N`（N 至少 1）覆盖本轮的步数上限，优先于环境变量 `SUGA_MAX_STEPS`，再优先于配置 `max_steps`（默认 240）；子 agent 的 `agents.max_steps` 不受影响。要在 Gofer 里限制 CLI job 的单轮步数，在该 agent 的 args 中加 `--max-steps`，或给 worker 进程设 `SUGA_MAX_STEPS`。

### Gofer job 的回合/会话预算

预算默认关闭；只有 `budget.tokens` 或 `budget.cost_usd` 配置为非零值时才启用。run 和 chat 共用下面三个 flag，优先级都是 **flag > 环境变量 > Suga 配置**：

| 目的 | flag | 环境变量 | 取值 |
|---|---|---|---|
| token 上限 | `--budget-tokens N` | `SUGA_BUDGET_TOKENS` | 大于等于 0 的整数 |
| 成本上限 | `--budget-cost-usd USD` | `SUGA_BUDGET_COST_USD` | 大于等于 0 的数字，单位美元 |
| 计量范围 | `--budget-scope turn\|session` | `SUGA_BUDGET_SCOPE` | `turn` 或 `session`，不区分大小写 |

`scope` 默认是 `turn`。`turn` 只限制一次 `suga run` 回合（chat 中则是当前回合）；`session` 还会把该会话历史回合及子 agent 的用量累计进去。预算不是进程级或跨会话配额，也不改变 `agents.max_tokens`。只设置其中一个上限也可以；两个上限同时设置时任一超限都会触发收尾。flag 也可直接放进 Gofer agent 的 `args`，例如 `args: [run, --budget-tokens, "200000", --prompt, "{{prompt}}"]`。

需要持久配置时，在 Suga 的 `SUGA_HOME/config.yaml`（通常是 `~/.suga/config.yaml`）中写：

```yaml
budget:
  tokens: 200000
  cost_usd: 2.00
  scope: turn
  # estimate: auto       # auto（默认）、on 或 off；没有对应 CLI flag
  # estimate_factor: 1.0 # 大于 1 可显式取更保守的估算
  # notice_ratio: 0.8    # chat 达到该比例时每回合提醒一次
```

#### 在 Gofer job 中注入

Gofer 不需要为预算增加协议字段；将环境变量注入 `suga` agent，worker 启动的 `suga run` 或 `suga chat` 会自动读取。例如 Gofer 配置可以写成：

```yaml
agents:
  suga:
    type: cli-agent
    command: suga
    env:
      SUGA_BUDGET_TOKENS: "200000"
      SUGA_BUDGET_COST_USD: "2.00"
      SUGA_BUDGET_SCOPE: turn
    args: [run, --output-format, stream-json, --prompt, "{{prompt}}"]
```

也可以只给特定 job 的进程环境设置同名变量；不要把它们误写成 `SUGA_MAX_TOKENS`，后者不是 Suga 的预算变量。使用 `--budget-*` 时，flag 只覆盖对应的环境变量和配置项；没有设置 flag 的其它预算字段仍按各自的环境变量/配置解析。

#### 用量口径、估算与成本告警

预算 token 的消耗是：

```text
max(input_tokens - cache_read_tokens, 0) + output_tokens
```

缓存读不计入消耗；缓存写仍包含在 provider 的 `input_tokens` 中，因此按该口径计入。压缩、交接等内部模型请求以及子 agent 默认也计入。provider 返回的 `cost_usd` 优先；否则按 `providers.<id>.models.<model>.price.*` 价格表计算，价格单位是美元/百万 token：

```yaml
providers:
  bailian:
    models:
      glm-5:
        price:
          input: 1.0
          output: 3.0
          cache_read: 0.2
          cache_write: 1.0
```

provider 既不返回成本、价格表也没有对应模型时，Suga 会告警，**成本预算不会生效**，不会猜价格。provider 缺少 token 用量时，默认 `budget.estimate: auto` 会使用本地字符/rune 估算，并在 usage、trace 和输出中标记 `estimated`；设为 `off` 时会告警且该次预算用量按 0 计。估算是预算用的近似值，不是未标记的计费真值。

#### Suga 软收尾与 Gofer 硬熔断

Suga 的预算是软阈值：普通回合在一次模型请求完成、观察到用量超限后，通过已有 wrap-up 机制请求模型最后汇报，回合以 `completed` 和预算原因结束；不会在模型请求中途硬杀进程。`session` 已经用尽时，下一回合在首个模型请求前短路，不调用模型，但用户消息仍保留在会话中。文本模式会在 stderr 说明原因，stream-json/trace 也会带 `wrap_up` 和预算快照；stdout 的答复正文不变。

一次普通回合的超出量不能保证只多一次模型请求。运维核对最坏上界时，还要考虑触发超限的请求、收尾请求、收尾前的 compact/segment handoff、空回复重试（最多 2 次）、忽略收尾时的无工具重试、已报告用量的 provider 重试，以及同时运行的子 agent 自身限制；后台子 agent 不会因父回合收尾被取消。预算因此不是硬上限。

Gofer 的 `--max-tokens`/`--max-cost` 是进程外的硬熔断，可能在 Suga 软收尾前直接终止 job，终止时不保证有完整答复或 trace 收尾事件。运维应根据历史缓存命中比例，把 `SUGA_BUDGET_TOKENS`/`SUGA_BUDGET_COST_USD`（或对应 flag）设在 Gofer 硬上限之前，并持续复测；Gofer 的硬上限优先于 Suga 的软收尾。默认给 Gofer job 使用 `scope: turn`，需要限制整个会话生命周期时才选择 `session`。

### Gofer job 的截止时间提示（run.deadline_env）

Gofer 给有时限的 job 注入环境变量 `GOFER_JOB_DEADLINE`（RFC3339，如 `2026-10-19T23:22:11+08:00`）。把配置键 `run.deadline_env` 设为该变量名（子 agent 也会沿用），每次请求模型前就在消息列表末尾临时附一条时间提示：当前时间与剩余约多少分钟；剩余不足 10%（相对本次运行观察到的最早请求时刻）或不足 5 分钟时改为“即将到达时限：请尽快提交已完成的工作并汇报未完成项”；已过截止时间则说明已超时。该提示只进本次请求（不写库、不进稳定系统提示前缀，前缀缓存不受影响）。配置为空（默认）时不附任何提示；变量不存在或无法按 RFC3339 解析时不报错，只在调试日志记一行。`run`、`chat`、ACP 都读进程环境，主机配置示例：

```yaml
run:
  deadline_env: GOFER_JOB_DEADLINE
```

## ACP 常驻与审批

```powershell
gofer job run --runner server --project my-tools-dev --cwd inhere-tools/suga --agent suga-acp --session --read-only --prompt "检查当前项目"
gofer job say <job-id> "继续分析"
gofer job end <job-id>
gofer job resume <job-id> --prompt "从上次会话继续"
```

`--read-only` 映射为 ACP `read_only` mode，写文件、命令与 MCP 工具不会加入可执行工具集合。默认 mode 中，写入/执行及所有 MCP 工具（含与内置只读工具同名者）在执行前调用 Gofer `session/request_permission`；可用 `gofer job interactions <job-id>` 查看待答复请求，再用 `gofer job answer <job-id> <interaction-id> reject|allow` 处理。拒绝时工具不执行；允许仅作用于当前一次请求。

### 工具拒绝语义

审批在工具注册表的包装层完成（从外到内：observe → visibility → approval → 工具），CLI 与 ACP 共用同一套语义：

- 被拒绝的调用（CLI 未授权的 MCP 工具；ACP 用户选择 reject、`session/request_permission` 请求失败、`read_only` mode 下调用写入/执行类工具）不执行，拒绝信息作为工具错误交给模型，回合继续。每次拒绝在 `suga inspect trace`/`stats` 中记为一条 denial 事件。
- 同一回合累计被拒 3 次后（`read_only` 的拒绝同样计数），下一次模型调用前结束回合，最终答复说明拒绝次数与工具名。计数按回合重置。
- 审批被取消（ACP 返回 `cancelled` 结果或 `session/cancel`）时中止回合，不把取消当作工具结果交给模型。
- 调用清单中但本次未启用的内置工具（只读模式下的写工具、缺 `serpapi_key` 的 `web_search`）按拒绝处理并计数，拒绝信息按入口给出开启方法（run：`--allow-write`；chat：`tools.allow_write: true`；`--read-only`：去掉该参数；ACP：会话 mode 改为 `default`）。完全未知的工具名返回“not found”及可用工具列表，不计入拒绝。
- 尚未加载的 deferred MCP 工具被直接调用时，由 visibility 层返回“尚未加载”并自动加载，不会触发审批请求。

对外契约：

- stream-json：本回合只要发生过拒绝，回合结束时都会在 `result` 之前发出一行 `input_required`，并带 `denied_tools`（去重后的被拒工具名数组）；`result` 的 `status` 仍为 `COMPLETED`，进程正常退出。旧版仅在首次拒绝时发出且不带 `denied_tools`，并会在模型重复请求被拒工具时直接结束回合；该短路已移除，改为上面的 3 次上限。
- ACP（语义变化）：拒绝后模型若正常完成，`stopReason` 由原来的 `refusal` 改为 `end_turn`；只有累计 3 次拒绝时返回 `refusal`（结束前发送说明文本）；审批取消返回 `cancelled`。被拒工具的 `tool_call` 状态为 `failed`（不再跟 `completed`）；允许路径的状态序列不变（`pending` → `in_progress` → `completed`）；未经审批就失败的调用（如未加载的 deferred 工具）以 `failed` 结束。Gofer 的 ACP 客户端两种 stopReason 都能处理。
- ACP `session/load` 的 `cwd` 必须与会话创建时的目录一致（两边都做符号链接解析，Windows 下不区分大小写）：不一致时返回 JSON-RPC `-32602`（invalid params），**message 里写明两个目录**，会话不加载、也不注册到本次连接（不会被后续 prompt 用到）。`session/new` 不变；旧会话没有记录目录时不拦截。`load_session: true` 的客户端（Gofer）若确实换了工作目录，应新建会话而不是 load 旧会话。
- ACP 的 prompt 因模型请求失败结束时，返回的 JSON-RPC 错误（`-32603`）`data` 为 `{"error": <文本>, "error_kind": <取值>}`（额度耗尽且 provider 给出恢复时间时另带 `reset_at`），取值与 CLI 的 `error_kind` 相同（`provider_transient` / `provider_rejected` / `provider_bad_response` / `provider_quota`）；不是模型失败的失败（存储、hook、取消、审批）仍是 `{"error": <文本>}`，形状不变、不带该字段。Gofer 据此决定续跑还是报错（v0.168.0 起只有 `provider_transient` 自动续接）。

2026-10-07 的 Gofer 实际作业已验证 CLI/ACP 续聊、ACP `job say/end/resume`、只读无写入及审批允许/拒绝；0.118.0 另以当前 `suga` agent 验证了 CLI 续接的环境继承与输出投影。模型响应由本机回环模拟服务提供；最终配置不含测试模型地址或测试 HOME，真实模型凭据路径须在运行环境中单独准备和验证。

### ACP 会话覆盖（list / resume / close / set_config_option / load 回放）

`initialize` 声明 `sessionCapabilities.list/resume/close`（`load_session` 仍由顶层 loadSession 能力声明）：

- `session/list`：按 `updated_at` 倒序，每页 50 条，`nextCursor` 为上一页最后一条的 `(updated_at, id)` 编码；只列普通会话，排除 archived 与子 agent。目录规则与 CLI `suga session list` 一致（设计 2026-10-20「会话列表默认当前目录」）：请求带 `cwd` 按它过滤（同目录，Windows 不区分大小写），不带时用 suga 进程的工作目录；没有记录目录的旧会话只在 `_meta` 带 `suga/all: true` 的请求里出现。与 CLI 一致，宿主会话（`origin=xhen`）默认不返回，数量在响应 `_meta.suga/hiddenXhen` 里；客户端需要时用请求 `_meta` 的 `suga/xhen: true` 显式包含（相当于 `suga session list --origin xhen`；默认视图为空时 `suga/hint` 里也会点名）。响应 `_meta` 始终带 `suga/otherDirs`（其它目录的会话数）；默认视图结果为空且 N>0 时另带 `suga/hint`，文本与 CLI 的 stderr 提示相同（“当前目录 <dir> 没有会话…或用 suga session list --all 列出全部”）。列表只含标题与目录，不含会话内容。
- `session/load`：按协议把当前分段以 `session/update` 通知回放给客户端——压缩摘要一条 agent 思考（“之前的对话已压缩：…”）、用户消息 `user_message_chunk`、助手正文 `agent_message_chunk`、工具调用与结果 `tool_call`/`tool_call_update`（completed/failed，结果只带前 2KB 文本）；回放总量上限 2MB，超过时只回放最近部分并在开头一条思考说明。回放的边界与采用规则复用上下文组装的 `loadHistory`（`personal.SegmentHistory`），客户端看到的内容与下一回合模型的一致。
- `session/resume`：与 load 相同的检查（目录一致、没有进行中的回合），不回放任何通知；响应带 `modes` 与 `configOptions`。`chat --resume` 的目录询问与 chdir 逻辑不受影响（v0.25.0）。
- `session/close`：有进行中的回合先取消并等待结束（同 `session/cancel`），之后释放该会话的进程内状态（always 规则不持久化）；关闭后的 prompt 返回 invalid params。会话本身不删除（数据治理走 `suga data gc`）。
- `session/set_config_option`：new/load/resume 的响应都带两个 select——`model`（category model，选项为配置里全部模型，当前值为会话模型）与 `mode`（category mode，default/read_only，与 `session/set_mode` 同源）。设置 model 走与 chat `:model` 相同的 `SwitchModel`（下一回合生效；回合进行中返回 invalid params），设置 mode 即 `session/set_mode` 语义；两种都返回最新的全部选项。
- acpprobe 覆盖：`--scenario lifecycle --model <provider/model> --switch-to <provider/model>`（真实模型验证 new → 带工具调用的对话 → close → close 后拒绝 → list → resume → load 回放 → 切换模型 → 再对话，并从 trace 核对 `model_switch`）。

### ACP 图片输入

Suga 的 ACP 服务端支持图片内容块（第二期）：

- `initialize`：配置里有任一支持看图的模型（`suga models` 显示 vision 的模型，含主模型、compact、fallback 与网关下其它模型）时声明 `promptCapabilities.image = true`；没有则不声明，客户端（gofer）继续走“附图路径 + 让模型 `file_read`”的方案。
- `session/prompt`：image 内容块（`data` 为 base64，或 `uri` 指向 `data:` URI 或本地文件路径）按顺序与文本块一起处理：图片走与 `view_image` 相同的校验与缩放（PNG/JPEG/GIF/WebP、20MB/40MP 上限、长边 1568），落盘到 `sessions/<id>/images/`，消息文本里留 “[图片 N]” 占位；远程 `http(s)://` URI 拒绝，坏 base64、不支持的格式、超限的图片写原因进文本（“[图片 N 不可用：<原因>]”），不让整轮失败——只有没有任何文本块且没有可用图片时才拒绝这条 prompt（invalid params）。
- 当前模型不支持看图时：图片仍照常落盘，请求里换成文字说明，并在回复前以一次 agent 思考通知告知客户端（“附带的 N 张图片已转为文字说明”）。会话中途 `:model` 类切换由下一次 prompt 重新判断。
- acpprobe（`scripts/acpprobe/acp_probe.py`）可用 `--prompt-with-image "<文本>|<PNG 路径>"` 发一个带图片块的 prompt，开始时打印 initialize 的实际声明。

### CLI 图片输入（`run --image`）

- `suga run --image <路径>`（可重复，顺序保留）把图片直接附到本条用户消息，不必先让模型调 `view_image`（读图入口是 `file_read`）：校验、缩放与落盘与 chat `:image` 相同（PNG/JPEG/GIF/WebP，长边 `images.max_side`，落盘到 `sessions/<id>/images/`，数据库只存引用），相对路径按 run 的工作目录解析。
- 当前模型不支持看图、或任何一张图读取失败（不存在、格式不支持、超限）时，run 在开始回合前报错退出（exit 1）：错误写明第几张与路径/原因，并提示用 `--model` 指定支持看图的模型或为该模型配置 `vision`，不会静默丢图。
- `--output stream-json` 的首行 session 事件在有图时多一个 `images` 字段（张数）；无 `--image` 时该行与原来逐字节一致。
- gofer 的命令行形态可按路径传图：`suga run --model <provider>/<model> --image <绝对路径> -p "描述这张图"`（图片在发送端读取、按 chat `:image` 的规则校验后随本条消息发出）。`suga send --image` 尚未支持：投递协议（`internal/personal/process`）只携带文本，需要附图时用 run 或 ACP 图片块。
- `suga send` 的退出码表与 `--wait` 语义对 gofer 是稳定契约（0 已投递/已排队，1 队列满，2 选择器歧义，3 未在线或消息被丢弃，4 文本过大，5 接收方不支持附图）：会话消息可靠投递（一期）只在这些之外新增 `--id` 与 `--wait-processed`，不带新参数时输出与退出码逐字节不变。带 `--wait-processed` 时，发送方拿到的是消息的终态：`processed` 退出 0，`held`（失败的回合把它转给用户下一回合）退出 0，`failed`/`discarded` 退出 3；状态行写 `状态：<state>`，另有 `id: <消息 ID>` 行。

### ACP 用量上报

每次模型调用结束后以及每轮结束（含被取消的回合）都发送 `usage_update`：`_meta` 顶层的扁平键 `input_tokens`/`output_tokens`/`cache_read_tokens`/`cache_write_tokens`/`total_tokens`/`cost_usd`/`tool_calls` 为**本次 run 的累计值**（run 口径，Gofer 只读这些扁平键，所以预算按 run 计）；另附嵌套对象 `_meta.run`（同一 run 值）和 `_meta.session`（会话累计，含本次 run；无 `tool_calls`）；顶层 `cost.amount` 仍为**会话累计值**；顶层 `used` 是最近一次模型请求的上下文 token 数，`size` 是模型上下文窗口。窗口取 `providers.<id>.models.<model>.context_window`（可选配置），否则按实际生效的模型（`--model`/`SUGA_MODEL` 覆盖后的值）名取保守默认（claude 200k、gpt-4.1 1M、gpt-5 400k、gpt-4o/deepseek 128k），未知模型为 128k。会话累计（`_meta.session`）保存在会话 metadata（`sessions.metadata_json`），`session/load` 或重启后从已保存值继续累计。`input_tokens` 不含缓存，缓存另报 `cache_read_tokens`/`cache_write_tokens`，`total_tokens` = input + output + cache_read + cache_write（与 Gofer 口径一致）。

## 升级迁移

本批改动（providers/model 配置、hooks 通用化、来源前缀）带来下列不兼容变化；按两侧分别处理。

### 用户 suga 配置（`~/.suga/config.yaml`）

- `llm:` 段改写为 `model: <provider>/<model>` 加 `providers:`，旧写法启动即报错；对照见 [LLM 配置](llm-config-v0.md#从旧配置迁移)。
- 补 `tools.read_only_allow`。只读 job（`--read-only`）不再有内置放行表，不配置则所有 MCP 工具被拒绝：

  ```yaml
  tools:
    read_only_allow:
      - gofer/gofer_*_list
      - gofer/gofer_*_get
      - gofer/gofer_work_report
      - gofer/gofer_session_list
  ```

- 补 `hooks.command` 等。原先 `gofer.hooks` 的 auto 模式（PATH 中有 `gofer` 即启用）已取消，不配置就不启用 hooks。等价于原行为的配置：

  ```yaml
  hooks:
    command: [gofer, hook, generic, --agent, suga]
    stop_args: [--wait, "7140"]
    stop_wait_sec: 7140
    post_tool_use:
      tools: ["*shell*", "*exec*"]
      name: shell
  ```

  只配 `stop_args` 不配 `stop_wait_sec` 会让 Stop 在 `timeout_ms`（默认 3000）后被结束，两者必须同时配置。`post_tool_use.tools` 不配置则不发送 PostToolUse。耗时的 Stop（如跑全量测试）可再加 `hooks.stop_background: true` 让它作为后台任务运行，不被输入与唤醒中断：此时 `stop_wait_sec` 只决定 hook 自身的超时（`stop_wait_sec+2` 秒），不设该值时由 `tasks.max_duration`（默认 2h）限制。

- provider 名变化：trace/stats 中的 provider 名是配置里的 id（原 `claude` 变为 `anthropic` 或自定义 id）。

### Gofer 侧（由 Gofer 会话在 Gofer 仓库实施）

- job 的 `cwd` 是子仓、但需要读项目根、兄弟模块或 `../../docs` 时，给 agent 的 `args` 追加 `--add-dir <绝对目录>`（需要写时用 `--add-dir <绝对目录>:rw`），或在 job 环境里设 `SUGA_ADD_DIRS`（路径列表分隔符分隔）；不需要把目录内容复制进工作区，也不必依赖依赖根探测。目录会记入会话，resume 时继续生效。
- job 环境设 `SUGA_HOOKS=off`，替代 suga 原先对 `GOFER_JOB_ID` 的判断：job 内的 suga 不应再启用 hooks。
- job 环境设 `SUGA_APPEND_SYSTEM_PROMPT`，携带原“运行在 Gofer job 中，可用 `gofer work report` / `gofer issue` 汇报”的提示。chat、resume 与 ACP 路径不带 `--append-system-prompt`。
- 经 `run` 续接投递 web 回复时加 `--injected`，suga 不再根据文本前缀（gofer v0.185 起为 `[来自 web 回复，<用户>]`，旧版为 `[gofer web 回复]`） 推断注入。
- socket 投递（`suga send`）若要保留“[来自 web 回复，<用户>]”这类前缀，需由 Gofer 自己加在文本前；suga 不再特判，`--source gofer`（或来源为 gofer）时显示为 `[来自 gofer] `。
- agent 配置中删除 `--provider` 用法（若有）；改用 `--model <provider>/<model>`。
