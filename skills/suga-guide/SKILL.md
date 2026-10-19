---
name: suga-guide
description: suga 的安装、配置与使用向导——首次配置（SUGA_HOME、config.yaml、.env 密钥、验证、常见报错）、完整配置项参考、provider 与模型细节、常用任务（写权限与审批、记忆、MCP、hooks、子 agent、后台任务、skills）、接入 gofer。用户问 suga 怎么配/怎么用、要改 suga 的 config.yaml 或排查启动报错时读它。
---

# suga 配置与使用向导

给模型看的操作向导：用户问 suga 怎么安装、怎么配置、怎么接入 gofer，或要改 suga 的 `config.yaml`、排查配置与启动报错时，按本文件做。

## 铁律（先读）

- suga **只读** `$SUGA_HOME/config.yaml` 和 `$SUGA_HOME/.env`，`SUGA_HOME` 默认 `~/.suga`。工作目录里的 `config.yaml` / `.env` 属于用户的项目，suga 从不读取——要改配置就改 `$SUGA_HOME` 下的那两个文件，不要在项目目录里放一份。
- 密钥只写在 `$SUGA_HOME/.env`（或进程环境变量）里，配置文件用 `${VAR}` 引用。**不要把密钥值读出来贴进对话**：不要 `file_read` `.env` 后回显内容，也不要让用户把密钥发给你；只说变量名。密钥由用户自己填时，让他在自己终端里填（例如 `file_write` 写出变量名+占位符，或告诉他用编辑器补上）。
- 改配置前先确认真实情况，别凭印象：`suga inspect manifest`（生效的模型、思考档位、工具、skills、记忆后端与选择原因、输出上限）、`suga run --help`、`suga inspect manifest --json`。本文件里的键名与命令都与当前代码一致；仍拿不准时读 `references/config.md`。
- 写配置**只增改需要的那几个键**，不要整份重写：用户可能有本文件没列出的配置与注释。
- 值写错时 suga 是启动失败（不是悄悄用默认值），所以让用户看到真实报错，别猜一个值蒙过去。

## 首次配置向导（按顺序做）

### 1. 确定 `SUGA_HOME` 并初始化

- 默认 `~/.suga`。要换位置，先在启动 suga 的环境里设 `SUGA_HOME`；配置、会话库、记忆、释放的 skills 全跟着走。
- 运行 `suga init`。它会按需创建 home、带注释的 `config.yaml` 和只含说明注释的 `.env`；已有文件永不覆盖。`suga init --force` 只备份并替换 home 的 `config.yaml`，`.env` 在任何情况下都不改。
- `version`、`--version` 和帮助命令是纯查询，不会创建 home 或文件。首次运行 `chat`、`run`、`acp`、`config`、`models` 等需配置的命令也会执行同一初始化门控。

### 2. 检查路径并编辑配置

```bash
suga config path
suga config get model
suga models
```

- 在 `$SUGA_HOME/.env` 中自己填写 provider 密钥和私有地址；自动生成的文件只有注释，suga 不会替你写入任何值。配置里的 `${VAR}` 引用 `.env` 或进程环境。
- 用 `suga config set <key> <value>`、`unset <key>` 修改已支持的非密钥键；`get`/`list` 显示生效值、来源并遮罩敏感值，`path` 显示 home、配置、`.env` 和当前来源。
- `SUGA_CONFIG` 指向外部配置时，`config` 命令写该文件，`init --force` 拒绝覆盖它；缺失的显式文件不会被另建 home 配置。Windows 路径按当前 shell 的原生分隔符显示。
- 需要手工配置时，参考仓库的 [最小上手模板](../../config/onboarding.yaml) 和 [完整字段样例](../../config/config.yaml)，不要把密钥提交到版本库。

### 3. 写 `$SUGA_HOME/config.yaml`（无二进制时的手工备选）

如果无法运行 `suga init`，可以手工创建最小配置：

```yaml
model: deepseek/deepseek-chat
```

完整字段和 provider 选项见 `references/config.md` 与 `references/providers.md`；正常情况下优先让 `suga init` 生成模板，避免复制过时的完整样例。

### 4. 密钥写进 `$SUGA_HOME/.env`

```dotenv
BAILIAN_API_KEY=<密钥>
BAILIAN_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
```

- `config.yaml` 里的 `${VAR}` 从进程环境或 `$SUGA_HOME/.env`（只读这两个地方）展开。
- 参看「铁律」：不在对话里回显密钥值；可以让用户自己粘进文件。
- 也可以不用 `.env`，改为在启动 suga 的进程环境里设这些变量（Gofer server 场景常用）。

### 5. 验证

```bash
suga run --prompt "只回答 ok 两个字"                   # 单轮，只读
suga inspect manifest                            # 生效的模型、工具、skills、记忆后端
```

第一条能正常回话即配置完成。想确认发出去的模型 id、思考档位，用 `suga inspect manifest`（主模型一行显示实际发送的 id）。

### 6. 常见报错怎么处理

| 报错 / 现象 | 原因与处理 |
|---|---|
| `model is not configured; set \`model: <provider>/<model>\`` | `model` 没写、为空，或被空串 `SUGA_MODEL` 覆盖。补上 `model: <provider>/<model>`。 |
| `SUGA_PROVIDER is no longer supported` | 旧配置残留：删掉 `SUGA_PROVIDER`，改用 `SUGA_MODEL=<provider>/<model>` 或 `model:`。 |
| 启动报 `llm` 相关错误 | 旧的 `llm:` 段已移除，存在就启动失败；改成 `model:` + `providers:`（对照 `references/providers.md` 的「从旧配置迁移」）。 |
| provider 未定义 / 模型报 unknown provider | `model` 的前缀写错，或 `providers:` 下没有这个 id（注意缩进、大小写）。 |
| 401 / 403 / `invalid api key` | `.env` 里没有该变量、变量名与 `${VAR}` 不一致，或密钥确实无效。检查变量名拼写，再看密钥类别是否与该 provider 匹配。 |
| 404 / `model not found` | `base_url` 多写或漏写版本段（`/v1`、`/chat/completions`）；模型 id 与上游不一致时用 `providers.<id>.models.<model>.model_id` 指定请求里发的 id。 |
| 连接超时 / 连不上 | `base_url` 不可达或需要代理。注意 `tools.web_fetch.proxy` 只管 `web_fetch`，不影响模型请求。 |
| 上下文被过早分段，或模型报 token 超限 | 上下文窗口判断不对：写 `providers.<id>.models.<model>.context_window`（或 `providers.<id>.context_window`）。 |
| `tools.allow_write must be a boolean` / `tools.approval must be "ask" or "auto"` / `context.fold_keep must be ...` | 值类型不对，suga 选择启动失败而不是猜。按报错里的键名改正。 |
| chat 里写文件/执行命令被拒 | chat 需要 `tools.allow_write: true`；`run` 需要 `--allow-write`；ACP 需要会话 mode 为 `default`；`--read-only` 永远优先。 |

## 常用任务怎么做

### 切换模型与思考档位

- 临时：`suga run --model <provider>/<model> --prompt ...`；不经过配置文件的默认：`SUGA_MODEL=<provider>/<model>`。
- 永久：改 `model:`。便宜模型生成摘要：`compact_model:`；主模型失败时依次尝试：`fallback_models:`。
- 思考档位 `off|minimal|low|medium|high|xhigh|max|auto`：命令行 `--thinking`（只作用于主模型）> `providers.<id>.models.<model>.thinking` > `providers.<id>.thinking`；都不设或 `auto` 时不发送参数，用 provider 默认。`openai` 类型发 `reasoning_effort`，`anthropic` 类型走 extended thinking。细节见 `references/providers.md`。
- 交互式 `suga chat` 中，输入 `:model` 查看当前 provider/model、thinking、上下文窗口、`max_output_tokens` 与全部已配置模型；输入 `:model <provider>/<model>` 从下一回合切换主模型，模型键支持 Tab 补全。切换保留 `fallback_models`，写入会话元数据并记录 `model_switch` trace；`--resume` 会沿用该模型，除非命令行显式传入 `--model`。只认本地键盘输入。

### 开写权限与审批

- **chat**：`tools.allow_write: true` 启用写文件与命令工具（默认 false）。默认 `tools.approval: ask`，每个写/shell 调用前在终端显示完整命令/路径并询问（`y` 允许一次、`a` 本次进程不再问、其他拒绝）；`auto` 不询问。
- **run**：必须显式 `--allow-write`（不读 `tools.allow_write`，适合任务型 job）；没有逐次确认。
- **ACP**：由会话 mode 决定（`read_only` / `default`），确认走客户端的 `session/request_permission`（gofer 侧是 `permission_policy`）。
- `--read-only` 永远优先。想收紧：给用户建议只读模式，而不是关掉审批。
- 危险递归删除（`rm -r`、`Remove-Item -Recurse` 等指向 `/`、`~`、盘根或工作区及其父目录）在任何模式下都会被拦下，chat 只有手敲 `yes` 才执行。

### 检查点与回滚

- 默认情况下，`file_write`、`file_edit`、`file_remove` 在写入前把原内容保存到 `$SUGA_HOME/sessions/<session-id>/snapshots/`。快照不进入模型上下文、transcript 或 export；`shell_exec` 的改动不在回滚范围内，`file_remove` 的 7 天回收区语义不变。
- chat 提示符使用 `:checkpoints` 查看清单，使用 `:undo`、`:undo N` 回滚最近的文件改动，`force` 只覆盖内容冲突；默认会确认，可用 `chat.undo_confirm: false` 关闭。只认本地键盘输入，投递消息不会触发这些命令。
- CLI 使用 `suga undo <session> --list [--json]`，或 `suga undo <session> [--turn <id>|-n <count>] [--force] [--yes] [--json]`。session 支持唯一前缀；非终端破坏性调用必须 `--yes`。该命令不需要 provider，可在没有模型配置时列出或回滚。
- 默认上限为单次 16 MiB、会话 256 MiB、全局 1 GiB，保留 20 个已完成回合或 7 天。`on_error: deny` 在快照不可靠时拒绝文件 I/O；`continue` 会继续但标为不可恢复。`shell_exec`、cwd/可写附加目录变更、冲突与 `--force` 的安全边界见 README 的回滚章节。
- 回滚后下一次显式回合会收到 `suga.undo` 事实通知；用 `suga inspect trace <session> --json` 检查 undo 事件和写工具事件的 `attrs.snapshot`。没有 redo，也没有 ACP 内置 undo 命令，但 ACP/run 的文件写操作照常快照。

### chat 输入历史与快捷键

- chat 默认把本地键入（包括 `!` 命令）写入 `$SUGA_HOME/history.jsonl`，每行含时间、工作区和脱敏后的文本；`chat.history: false` 可关闭，`chat.history_size` 默认 1000。投递消息不会写入。
- `Up`/`Down` 浏览历史，当前工作区优先；`Ctrl+R` 增量反查，重复按键继续向前，Enter 采用、Ctrl+G 取消。行首 `:` 命令以及 `:add-dir` / `:rm-dir` 后的目录支持 Tab 补全。

### chat 里直接执行本地命令（`!` / `!!`）

- 在提示符处输入 `!<命令>`：chat 直接在本地 shell 里执行（与模型的 `shell_exec` 同一个 shell、工作目录与环境），输出实时打印在终端，不调用模型、不开始回合、不进会话上下文；非零退出码、超时（`tools.shell_timeout_max`）或被你终止会各提示一行。
- `!!<命令>`：同样执行，并把“命令 + 退出码 + 输出（按工具输出那套规则精简、截断）”附在下一条消息之前发给模型；多个 `!!` 按顺序累积，chat 退出时未发送的丢弃。
- 执行中 `Ctrl-C` / `Esc` 只终止该命令（连同进程树），chat 留在提示符；回合进行中在 `↳ ` 输入行输入的 `!…` 不执行，该行退回输入行。
- 只认本地键盘：`suga send`/gofer 投递、`--steer`、Stop hook 回复里以 `!` 开头的文本一律按普通消息。由本人键入，因此不走审批、不做危险命令拦截，`--read-only` 会话同样可用（该模式只限制模型的工具）。每次执行在 trace 里记一条 `local_command`。

### 长期记忆（memory）

- 范围与后端：`memory.user.backend`（`files` / `exec`）、`memory.project.backend`（`files` / `gofer` / `exec`）。`exec` 必须同时配 `memory.<scope>.command`（可选 `.args`、`.timeout_sec`）。
- 不设 `memory.project.backend` 时的自动选择顺序：仓库根有 `MEMORY.md` → `files`（仓库模式，条目在 `.suga/memory/`）；否则仓库根有 `.gofer/tracker` → `gofer`；否则 `$SUGA_HOME/projects/<key>/memory` 的 `files`。`suga inspect manifest` 显示选了哪个与原因。
- 写入开关：`memory.write: off` 让 memory 工具只读；`memory.write_tainted: ask|deny|allow` 决定读过外部内容（网页、MCP、会话检索等）的会话写记忆时如何处理。放宽前先跟用户说明影响。

### MCP 服务

```yaml
mcp:
  servers:
    - id: filesystem
      command: npx
      args: [-y, "@modelcontextprotocol/server-filesystem", ./workspace]
      env:
        NODE_ENV: development
  # call_timeout_sec: 120
```

- 一次性的 Claude 风格配置：`suga run --mcp-config <file.json>`（文件形如 `{"mcpServers":{"gofer":{"command":"gofer","args":["mcp"]}}}`）。
- 放行名单：`tools.auto_allow`（普通运行）、`tools.read_only_allow`（`--read-only` 时只认它，条目必须写成 `server/toolglob`）。**两个都为空时所有 MCP 调用都被拒绝**，这是最常见的“MCP 工具不生效”原因。
- MCP 子进程会继承启动环境，但 `*_API_KEY` / `*_AUTH_TOKEN` 及 suga 自己读的 LLM 密钥变量被剔除；需要时在各 server 的 `env` 里显式给。
- 工具很多时 `tools.deferred`（默认 `auto`）延迟加载 schema，模型用 `tool_search` 取回。

### hooks（仅 chat）

- 不设 `hooks.command` 就不启用 hooks；`SUGA_HOOKS=off` 整体关闭（Gofer job 环境里就是这么设的）。

```yaml
hooks:
  command: [gofer, hook, generic, --agent, suga]
  timeout_ms: 3000
  # stop_args: [--wait, "7140"]     # 只追加到 Stop；必须与 stop_wait_sec 同时配
  # stop_wait_sec: 7140
  # stop_background: true           # Stop 作为后台任务跑，不被打字打断
  # post_tool_use: {tools: ["*shell*", "*exec*"], name: shell}
```

- 事件在 stdin 上一行一个 JSON；`hooks.post_tool_use.tools` 不配则不发送 PostToolUse。

### 子 agent 与后台任务

- 子 agent（`agent` 工具）：`agents.enabled: false` 不注册；`agents.model` 让子 agent 跑更便宜的模型（写法同 `model`，裸 id 用主模型 provider）；上限 `agents.max_steps`(30)、`agents.max_tokens`(400000)、`agents.timeout`(10m)。子 agent 只读、不能再派子 agent。
- 后台任务：`shell_exec` 传 `background: true`（写模式）或 `agent` 传 `background: true`（任何模式）；`tasks.max_running`(4)、`tasks.max_duration`(2h)、`tasks.max_output_bytes`(64MiB)、`tasks.wake_on_exit`(true，chat 里任务结束会唤醒空闲提示符)。
- 前台 `shell_exec` 默认运行超过 `tools.shell_background_after`（默认 `120s`）会转成后台任务，已产生的输出会随工具结果返回，并明确给出任务 id；任务完成后通过现有通知机制唤醒模型。设置为 `0` 可关闭，`tools.shell_timeout_max` 仍会终止超过硬上限的任务。

### skills 与规则文件

- 加载顺序：`<工作区>/.agents/skills`、`<工作区>/skills` > `$SUGA_HOME/skills` > `~/.agents/skills`，同名先到者胜。
- `$SUGA_HOME/skills/suga-guide` 就是本 skill：suga 首次运行时释放。目录里的 `.suga-builtin.json` 记录 suga 写了哪些文件及其哈希——用户改过其中任何文件后，升级不再覆盖该目录；没有这个标记的同名目录（用户自己的 skill）永远不动。
- 自己写一个 skill：目录 + `SKILL.md`，frontmatter 至少 `name`、`description`（描述会截断到 160 字节列进提示词，写清“什么时候用它”），正文写给模型看的步骤。
- `skills.list_max`(15) 是带描述列出的数量上限，超过后只列工作区 skills（带描述），其余只列名字；`skills.always_load: [名字]` 或 frontmatter `metadata: {always_load: true}` 让正文每次进提示词（每个上限 8000 字节）。
- 项目规则文件：`rules.file`（默认 `AGENTS.md`，可改 `CLAUDE.md`），每级目录一个，从 Git 根读到 cwd，外加 `$SUGA_HOME/AGENTS.md`；`--no-project-rules` 可跳过（调试用）。

### 接入 gofer

- 在 Gofer 的 `agents` 下加 `suga`（cli-agent，跑一次性 job）与 `suga-acp`（acp-agent，常驻会话 + 逐次审批）；两种 agent 都用启动它的用户的 `$SUGA_HOME` 配置与密钥。
- 只读 job 需要在 suga 配置里补 `tools.read_only_allow`，否则 MCP 工具全被拒；job 环境里设 `SUGA_HOOKS=off`。
- 完整的 `agents.yaml` 示例、送话、观测与升级迁移见 `references/gofer.md`。

## 参考文档（按需读一个）

用 skill 工具读本 skill 目录里的文件：`skill(name="suga-guide", file="references/config.md")`。

- `references/config.md`：完整配置项参考（全部键、默认值、取值、作用范围）、环境变量、目录布局、命令与常用 flag。
- `references/providers.md`：provider 与模型细节：类型、兼容适配、地址拼接、`model_id`、上下文窗口、价格、思考档位、`prompt_cache`、模型引用解析、网关示例、从旧配置迁移。
- `references/gofer.md`：接入 gofer：`agents.yaml` 配置、只读/可写、审批与拒绝语义、chat 送话、观测命令、升级迁移要点。
- `references/chat.md`：chat 交互：`:` 本地命令（`:diff`、`:model`、`:add-dir`/`:dirs`、`:help`/`:usage`/`:tasks`）、输入历史与多行输入、插话（steering）与 Esc/Ctrl-C 语义、`!`/`!!` 本地命令、续接预览与崩溃恢复、`suga send` 投递。用户问 chat 里某个按键或命令怎么用时读。
- `references/tools.md`：工具与权限：工具清单、写权限与审批、`--add-dir` 与越界授权、只读根（依赖/idev-std）、危险删除拦截、大输出外置与输出规则、后台/监听任务、后台 Stop hook、MCP、子 agent。用户问某个工具能做什么、为什么被拒、怎么放行时读。
- `references/sessions.md`：会话、记忆与 skills：续接与交接、换目录续接、分段压缩、中断回合、历史折叠、空闲整理、提示词结构、记忆后端与污染确认、skills 扫描与加载。用户问会话/记忆/skill 行为时读。
- `references/observability.md`：观测：`suga inspect trace`（各类行与 `--analyze` 字段）、`sessions`/`stats`/`export`/`gc`、stream-json 的 `usage`/`retry`/`wrap_up`、进程日志与 `SUGA_DEBUG`。排查一次运行、看用量成本时读。

只有在需要某个具体键的默认值或取值范围、或要回答“能不能配 X”时再读 `references/config.md`；回答“怎么装、怎么改、报错怎么办”通常上面的向导就够了。
