# 工具与权限参考

内置工具清单、写权限与审批、优先工具（`tools.preferred`）、附加目录与只读根、危险删除拦截、后台与监听任务、大输出外置、MCP 与子 agent。相关键（`tools.*`、`tasks.*`、`agents.*`、`mcp.*`）的默认值见 `references/config.md`；面向 Gofer 接入方的完整语义另见仓库 `docs/usage/gofer-personal-agent.md` 的“内置工具与相关配置”。

## 工具清单

| 工具 | 类型 | 作用 |
|---|---|---|
| `file_read` | 读 | 带行号分段读取（`offset`/`limit`，默认 2000 行）；`paths` 一次读最多 10 个文件；二进制文件只报告不输出 |
| `file_list` | 读 | 列目录，`depth`（≤ 5）可递归 |
| `file_grep` | 读 | RE2 正则搜索，支持 `glob`、`ignore_case`、`output: content\|files` |
| `file_find` | 读 | 支持 `**` 的 glob 查找，按修改时间从新到旧 |
| `web_fetch` | 读 | 抓取网页正文（保留标题、链接、代码块）；拒绝内网和本机地址 |
| `web_search` | 读 | SerpAPI 搜索（需 `serpapi_key`） |
| `skill` | 读 | 加载 skill、读取其文件，或检索 skills（`query`；`"*"` 列出全部名字） |
| `session_search` | 读 | 全文检索过往消息、工具结果与摘要（`scope`：workspace、current、all，`kind`：user、assistant、tool、compaction、handoff）；带 `ref=out:...` 时在那份已保存的输出内检索 |
| `recall` | 读 | 按 ref 读取命中附近内容：`<session>/<seq>` 起的消息，或已保存输出 `out:<session>:<hex>` 的带行号文本 |
| `agent` | 读 | 把一件独立的任务交给子 agent：它在自己的上下文里大量读取，只把报告带回来（`description`、`prompt`、`type`：`explore` 或 `research`、`background`）；见下文“子 agent” |
| `suga_manifest` | 读 | 以 JSON 返回本实例的能力清单（与 `suga manifest` 相同） |
| `memory` | 状态 | 对长期记忆 `add`、`replace`、`remove`、`get`、`search`（`scope` 为 project 或 user），条目可选 `applies_when` 适用条件，见 `references/sessions.md` 的“记忆” |
| `file_write` | 写 | 整文件写入 |
| `file_edit` | 写 | 替换一处唯一的字符串（`replace_all` 替换全部），或用 `edits` 一次替换多处（全部成功或全部不改）；保留 CRLF |
| `file_remove` | 写 | 删除文件，或带 `recursive` 删除目录（细节见下） |
| `shell_exec` | 写 | 执行命令（Windows 用 PowerShell，其他系统用 `sh`），带超时；返回输出与退出码。`run` 与 chat 里传 `background: true` 会把它作为后台任务启动，立即返回任务 id |
| `task` | 状态 | 本会话的后台任务：`list`、`output`（末尾若干行，可带 `offset`/`limit`、`grep`）、`wait`（可带 `timeout`）、`kill`（整个进程树） |

- `file_remove`：移入 `$SUGA_HOME/trash/` 保留 7 天（`origin.json` 记录原路径），单次最多 1000 个文件。被 `.gitignore` 忽略的目录（以及 `node_modules`、`vendor`、`dist`），如被忽略的 `tmp/` 下的测试仓库，视为可再生：不遍历、不计数、直接永久删除，并在结果中列出；被忽略的单个文件（如 `.env`）仍进回收区。拒绝工作区根、工作区自己的 `.git`、用户主目录、通配符与工作区外路径。
- 忽略规则：`file_grep`、`file_find` 与递归的 `file_list` 跳过 `.git`、`node_modules`、`vendor`、`dist` 和 `.gitignore` 匹配的路径（各级目录的 `.gitignore`，支持 `!` 与 `**`）；结果末尾一行注明跳过了什么，传 `no_ignore: true` 可包含它们（`.git` 除外）。
- 参数校验：工具调用先按工具自己声明的 JSON schema 校验（类型、枚举、范围、长度与项数、`additionalProperties: false` 下的未知字段），一次报出全部问题让模型一次改对；被拒的调用不执行、该响应的其它调用照常执行。schema 未声明的字段，以及 `anyOf`/`oneOf`/`allOf`/`$ref` 这类未建模的写法一律放行——MCP 等第三方 schema 写得不严不会被拒。可选字段的空占位（`""`、`[]`、`{}`）视为未提供。
- 并发读取：同一次回复里有多个 `file_read`、`file_list`、`file_grep`、`file_find` 调用时并发执行（`tools.parallel_reads`，默认开）；每个调用仍按顺序经过审批、污染追踪与 trace 记录，只有读取本身重叠。

## 优先工具（`tools.preferred`）

声明本机已有的强工具：确实可用时 Agent 在环境说明里看到并优先使用，不可用时提示词里完全不提，Agent 照常用内置工具。配置可以手写 YAML，也可以用 `suga config set`：

```yaml
tools:
  preferred:
    - name: rg
      kind: command
      when: 精确搜索文本或正则，优先于反复读取文件
    - name: codebase-memory-mcp
      kind: mcp
      when: 需要跨文件查符号、调用关系时
```

```text
suga config set tools.preferred '[{name: rg, kind: command, when: "精确搜索文本或正则"}]'
```

- 三项都必填：`name`（可执行文件名，不带参数和 shell 语法；`mcp` 时是 `mcp.servers` 里的 server `id`）、`kind`（`command` 或 `mcp`）、`when`（一句适用场景）。列表顺序即展示顺序；`config set` 整列表替换，不支持 `tools.preferred[0]` 这样的元素级路径。名字重复、空值、未知字段、非法 `kind` 都会让命令失败（退出码 2）且原配置文件不变。
- **探测方式**：`command` 按当前 PATH 查找（Windows 交给 Go 的 `PATHEXT`，`.exe`、`.cmd` 都认），只查 PATH 不执行命令，也不产生审批请求；`mcp` 以会话启动时该 server 启动并 `tools/list` 成功为准。`suga manifest` 不启动 MCP，`mcp` 项只报是否已在 `mcp.servers` 中配置。
- **只列能用项**：环境说明里只出现可用项，形如 ``- Preferred tool: rg (command) — <when>. Use when applicable; usage unclear, first check `--help`; record learned usage in memory.``；不可用项不写进提示词。另外 `command` 项只在本会话注册了 `shell_exec` 时可写会话里列出：只读会话（`run` 无 `--allow-write`、chat 未开 `tools.allow_write`、ACP `read_only`）列出连上的 `mcp` 项、不列 `command` 项，因为那里的模型执行不了命令。列表只进段上下文，不改稳定系统提示，所以新装命令在下一个 segment 才出现，`command` 在新 segment 重新查 PATH，MCP 状态在会话启动时固定。
- **MCP 常驻**：`kind: mcp` 的 server 注册出来的工具不会被 `tools.deferred: auto|always` 放进 `tool_search` 延迟集合，直接出现在工具列表里；其他 MCP 照旧。
- **优先不等于放行**：`preferred` 只影响提示词与可见性。MCP 调用仍按 `tools.auto_allow` / `tools.read_only_allow` 判定：不在放行名单时 chat 与 `run` 直接拒绝（不询问），ACP 走客户端权限请求；`command` 仍经过 `shell_exec` 的审批，可用审批规则放行，例如 `command: ["rg *"]`。
- **观测**：`suga manifest --json` 的 `preferred_tools` 列出每个声明的 `kind`、`when`、`present` 与原因（`found on PATH; read-only sessions do not list it`、`not found on PATH`、`server not configured`、`configured; connection confirmed at session start`、`connected at runtime`、`not connected at runtime`）——命令项找不到就是 `not found on PATH`，找到了也仍写 `present=true`，原因里注明了只读会话不列出；`suga trace <session> --json` 的 `turn_context.preferred_tools` 是本回合环境说明实际列出的名字（按声明顺序，只读会话里没有 `command` 项）。
- **语义检索提供者**：例如把 `xerj`（`command`，在已建索引的项目上做语义检索）或某个代码图谱 MCP 声明为优先项，让 Agent 先试它、再用内置工具兜底。不可用时（命令不在 PATH、MCP 没配置或没连上）Agent 照常用 `file_grep`、`file_find`、`session_search` 等内置工具，不会因为优先项缺失而失败。MCP 返回属外部内容：读过之后会话按现有污点规则被标记，之后的记忆写入受 `memory.write_tainted` 约束。

## 写权限与审批

- **默认只读**：写工具只在 `run --allow-write`、chat 设置 `tools.allow_write: true`、或 ACP 会话处于 `default` 模式时才有；`--read-only` 永远优先。模型调用了未开启的工具时，会被告知开启方法，并计入拒绝次数。
- **chat 先询问**：`tools.approval: ask` 时，每次写文件或执行命令前都会询问，并显示完整命令、路径或改动前后的文本。审批规则会先决定是否放行或拒绝；需要询问时可答 `y`（一次）、`a`/`always`（按范围创建本会话 allow）、`d`/`deny`（按范围创建本会话 deny），其它任何回答都是拒绝。启用追问后，`a`/`d` 可再用 `u` 或 `p` 写回规则。一轮里被拒 3 次就结束这一轮。

## 审批规则（`tools.approval_rules`）

设置 `tools.approval_rules` 启用统一审批策略。规则正文放在独立文件中，这样 suga 可以在用户确认后追加规则，而不会重写主配置：

```yaml
tools:
  approval_rules:
    enabled: true
    always_prefix_tokens: 2
    persist_ask: true
    project_trust:
      /绝对路径/到/项目: <从 suga manifest 获取的 sha256>
```

Suga 读取 `$SUGA_HOME/approval-rules.yaml`（用户级）和 `<workspace>/.suga/approval-rules.yaml`（项目级）。文件只包含一个 `rules` 列表：

```yaml
rules:
  - tool: shell_exec
    command: ["go test *", "go build *"]
    action: allow
  - tool: file_write
    path: ["docs/**", "README*.md"]
    action: allow
  - tool: file_write
    path: ["**/.env", "**/*.pem"]
    action: deny
  - tool: "gofer/gofer_*_delete"
    action: deny
```

`tool` 是原生工具名 glob；MCP 规则必须写成 `server/toolglob`。`command` 只适用于原生 `shell_exec`；`path` 只适用于明确的 `file_write`、`file_edit` 或 `file_remove`，且不能和 `command` 同时出现。规则可用 `modes: [chat, run, acp]` 限定入口；省略时适用于所有入口。未知字段、动作、工具/path 组合以及 YAML anchor/alias 都会导致启动失败。单个文件上限 64 KiB、最多 256 条规则。

匹配采用 fail-closed：所有命中中取最强动作（`deny > ask > allow`），不是首次命中。allow 命令必须按实际配置的 shell（`sh`/`bash`、PowerShell 或 `cmd`）通过保守的 ASCII、引号和 token 检查；只有末尾独立的 `*` 或 `**` 才能扩展 token。控制符、展开、包装器和未知 shell 不会产生 allow。allow 路径同时检查 root 下的词法路径和 `EvalSymlinks` 后的真实目标；符号链接、硬链接、特殊文件、解析失败，以及 Windows 短名、ADS、尾部点/空格名称都不会产生 allow。deny 在 allow 安全检查失败时仍可用于收紧策略。

项目规则有意采用不对称信任：项目 `deny` 和 `ask` 可以收紧策略，但项目 `allow` 必须等精确的绝对 workspace 路径和当前规则文件 SHA-256 出现在 `project_trust` 后才生效。`suga manifest --json` 会显示当前 hash 及 allow 被忽略的原因；确认文件内容后再把 hash 复制进配置。文件变化或重载失败时保留上一份有效快照，并在诊断中报告。

各入口默认动作保持明确：

| 入口 | 默认动作 | 重要边界 |
|---|---|---|
| chat | `tools.approval`：`ask`，或 `auto` → `allow` | 可询问；规则也能把只读/agent-state 工具改为 `ask` 或 `deny` |
| `run` | `allow` | 无终端时 `ask` 按拒绝处理；`--read-only` 与工具可用性优先 |
| ACP default | `ask` | `read_only` 拒绝非只读工具；不读取 CLI 的 legacy MCP 放行名单 |
| 子 agent | 其只读工具集合默认 `allow` | 创建时取得 Policy 快照；无终端时 `ask` 按拒绝处理 |
| chat/run MCP | `deny` | `tools.auto_allow` / `tools.read_only_allow` 仅在这里编译成 `legacy` allow 规则 |

规则不会绕过工具注册、`--read-only`、文件/shell 沙箱、危险删除守卫或受保护的控制面路径：`$SUGA_HOME`、workspace 下的 `.suga/` 与 `.git/`，以及两个 approval-rules 文件。命中这些路径的 allow 会被钳制为 chat/ACP 的 ask，或无交互 run/子 agent 的 deny。因此 `run` 拒绝写入这些路径是有意行为。

chat 中，`y`/`yes` 只允许本次调用。`a`/`always` 为本会话创建规则：安全 shell 命令前缀（`always_prefix_tokens`，默认 2）、写/编辑目录、精确的 `file_remove` 路径或其他工具名。不安全 shell 命令不提供 `[a]`。`d`/`deny` 用相同范围创建本会话 deny；其它答案（包括回车）拒绝本次调用并继续回合。`persist_ask` 为 true 时，`a`/`d` 后会追问写回：`u` 写用户级规则，`p` 只把 deny 写项目级，`N`/回车只保留本会话。写回使用加锁、原子读改写，并记录 `approval_rule_persist` 事件；写失败也不会丢失会话规则。

ACP 提供 `allow`、`allow_always`、`reject`、`reject_always` 四个选项。两个 `always` 选项创建进程内会话规则：后续同范围调用跳过或直接失败权限请求，但仍发送 `tool_call` 通知；不会写规则文件。同一进程内 `session/load` 保留它们，换进程后需要重新询问。

用 `suga manifest --json` 查看规则文件路径、生效条数、项目 hash/信任诊断、被忽略规则和 `tools.approval` 默认动作；用 `suga trace <session> --json` 查看工具/拒绝事件的 `approval_rule`、`approval_scope` 以及写回事件 `approval_rule_persist`。设置 `tools.approval_rules.enabled: false` 可回到旧审批路径。

## 回滚文件工具改动（快照与 undo）

默认开启时，`file_write`、`file_edit`、`file_remove` 会在实际改动文件前保存原内容快照。快照是本机私有数据，位于 `$SUGA_HOME/sessions/<session-id>/snapshots/`，不会进入模型上下文、transcript 或 `suga export`。只覆盖文件工具：`shell_exec`（包括后台任务）的改动只会被列出，不会自动回滚。`file_remove` 仍按原语义保留 7 天回收区；被忽略目录直接永久删除时，仍不可恢复。

在 chat 提示符输入 `:checkpoints` 查看最近写操作，清单会区分恢复、删除、跳过、无法恢复，并列出未纳入快照的 shell 命令。输入 `:undo` 回滚最近一个有文件改动的回合，或输入 `:undo N` 回滚最近 N 个这样的回合；追加 `force` 才会覆盖内容冲突。默认 `chat.undo_confirm: true`，会先询问，除 `y`/`yes` 外都取消。这些命令只认本地键盘：投递文本、hook、任务通知和插话仍是普通消息；回合进行中输入的 `:undo` / `:checkpoints` 会退回输入框，等回合结束后再执行。

不依赖 provider 的 CLI 也可以执行：

```text
suga undo <session> --list [--json]
suga undo <session> [--turn <turn-id> | -n <count>] [--force] [--yes] [--json]
```

`<session>` 支持完整 ID 或唯一前缀。非终端执行破坏性操作必须带 `--yes`；`--force` 只覆盖内容冲突，不绕过 root、路径、文件类型、链接或保护路径校验。回滚按调用逆序执行，可删除写操作新建的文件，可恢复符合条件的 `file_remove` 回收区条目，并在回滚前建立独立快照。会话 cwd 变化或可写附加目录被移除后，旧快照会被标记为不可恢复，不会写入不可信 root。只读会话可以回滚自己可信的快照，但不能扩大允许的 root。没有 redo，也没有 ACP 内置 undo 命令；ACP 与 `run` 的文件写操作仍会创建快照。

默认上限为每次调用 16 MiB、每会话 256 MiB、全局 1 GiB；保留最近 20 个已完成回合或 7 天。`tools.snapshots.enabled: false` 只停止新快照，已有快照仍可由命令读取。默认 `on_error: deny`：快照存储、清理或状态检查不确定时，在文件 I/O 前拒绝写入；显式设为 `continue` 才会继续，但会标记为不可恢复。超过 `max_file_bytes` 的文件照常写入，并明确提示不可回滚。`suga gc` 会随会话数据清理快照，并报告快照字节数。回滚后，下一次显式回合会收到 `suga.undo` 事实通知（不是新的用户请求）；可用 `suga trace <session> --json` 查看 undo 事件以及各写工具事件的 `attrs.snapshot`。Git 工作区中，快照边界之外的改动请用 `git status --short` 和 `git diff` 核对。

## 附加目录（`--add-dir`）

- `run` 与 `chat` 支持可重复参数 `--add-dir <绝对目录>[:ro|:rw]`（默认 `:ro`）；环境变量 `SUGA_ADD_DIRS` 可再加（各项用系统的路径列表分隔符分隔，Windows 是 `;`，适合 job runner 透传）。
- `:ro` 目录加入本会话的只读根（与 `tools.read_roots` 并列），适合 `cwd` 落在某个子仓、却要读项目根或兄弟模块文档的场景。
- `:rw` 目录同时允许 `file_write`、`file_edit`、`file_remove` 写入，审批、危险删除拦截、拒绝工作区根/自带 `.git`/主目录等规则与工作区完全一致；`shell_exec` 的 cwd 仍是工作区。
- 目录写入会话元数据（`add_dirs`），`--resume` / `--session-id` 续接沿用，同路径重给时以本次的权限为准。
- `--read-only` 下它们只以只读生效（给出提示，会话里保留你要求的权限；会话中途也只能授权只读目录）。
- 环境段会列出每个附加目录及其权限，模型因此直接用文件工具读取，不必绕 shell；会话中途追加的目录从下一次工具调用起就能用，下一回合的上下文更新把它告诉模型（环境段在段上下文里，不放在稳定系统提示，避免前缀缓存失效）。
- 只接受已存在的绝对目录，会清理 `..`；工作区与附加目录之外的路径照旧被拒绝。
- ACP 暂不支持附加目录（`session/new` 带 `additionalDirectories` 会被拒绝）。

## 会话中途要目录

- chat 里用 `:add-dir` / `:rm-dir` / `:dirs` 改、查附加目录（见 `references/chat.md`）。
- 不给命令时模型也能问：文件工具要访问工作区、只读根与附加目录之外的路径时，chat 先问一行 `授权访问 <目录>？[r] 只读 [w] 读写 [n] 拒绝`（`chat.ask_outside_dirs`，默认开）。问的是工具真正解析出的那个目录（跟随符号链接），不是模型嘴里的路径。
- 答 `r` 或 `w` 就把该目录写进会话（来源 `ask`）并让这次调用照常跑；答 `w` 的写工具随后仍会照常确认一次。`--read-only` 会话不提供 `[w]`。
- 关掉这个开关、没有终端、或 `run`/ACP 下，越界调用照旧直接拒绝。

## 只读根

- 只读文件工具（`file_read`、`file_list`、`file_grep`、`file_find`）可以读取工作区外指定的目录，但不会改变写工具或 `shell_exec` 的工作区。
- `tools.dependency_roots`（默认 `true`）自动识别依赖源码缓存（Go 模块缓存、`GOROOT/src`、Rust crate 源码）。
- `tools.standards_roots`（默认 `true`）从 `IDEV_STD_RELEASES_ROOT`（或兼容的 `INHERE_DEV_STANDARDS_ROOT`）识别 idev-std 发行目录，并从工作区向上查找工作空间根，把其 `<工作空间根>/tmp/idev-std` 加为加载产物目录。工作空间根通过 `standards.json` 判断；产物目录只有存在时才加入。设 `false` 关闭 idev-std 自动只读根。
- 仍可用 `tools.read_roots` 显式配置目录，重复路径会合并。
- 识别结果会列在提示词环境段；这些目录只读，写工具与 shell cwd 不受影响。

## 危险删除拦截

- 递归删除的 `shell_exec` 命令（`rm -r`、`Remove-Item -Recurse`、`rd /s`，以及 `sh -c`、`cmd /c`、`pwsh -Command` 里的同类命令）在审批前就会被拦下，条件是目标为：`/`、`~` 或 `$HOME`、盘符根、工作区或其上级目录，或路径以变量开头、变量为空时会落到根目录（`"$X/"`、`"$dir\*"`）。对这些目录用通配符（`rm ~/*`）时没有 `-r` 也算。
- chat 每次都询问，`tools.approval: auto` 或之前回答过的 `a` 也不例外，只有手动输入 `yes` 才执行；`run` 和 ACP 直接拒绝，并提示模型改用 `file_remove`。
- trace 里记下原因（`danger=…`）。
- 这能挡住常见的误操作，挡不住刻意构造的命令：它不是安全边界。

## 边界

- 文件工具经 `os.OpenRoot` 访问，不会顺着 symlink 跑出工作区。
- `shell_exec` 超时会终止整个进程树。
- `web_fetch` 检查实际连接的 IP，跳转也一样受限；所有工具结果都有上限。

## 大输出外置与输出整理

- 工具结果超过 `tools.max_output_bytes` 时，脱敏后的原文存到 `$SUGA_HOME/sessions/<id>/outputs/`，上下文只留头尾（各 2000 字节）和 ref（形如 `out:<session>:<hex>`）；用 `recall` 读取，用 `session_search` 检索。
- 在此之前，shell 结果会先整理（`tools.minimize_output`，默认开）：去掉颜色码与进度重绘，折叠连续相同的行，测试与构建输出按输出规则裁剪——全部通过时只留各包结果和一行汇总，有失败时只留失败用例及其信息与日志；原文保存为 ref，结果中注明。
- 内置规则覆盖 `go test`、`cargo test`、jest/vitest/mocha 与 `npm|pnpm|yarn|bun test`、pytest、Maven、Gradle、`git log`/`git diff`/`git status` 与 `rg`/`grep` 的命中。
- 一条规则可以丢行、折叠重复噪声、改写整理结果（`replace`）、按捕获的键分组（`group_by`）、保留首尾 N 行，并给自己设更低的门槛（字段与格式见仓库 `pkg/agentcore/tools/native/rules/`）。
- `$SUGA_HOME/output-rules.yaml` 与其后的 `<workspace>/.suga/output-rules.yaml` 可按名字替换规则、新增规则，或用 `enabled: false` 关掉某条。
- 命令本身用管道过滤了输出（`| grep`、`| Select-String`、`| tail` 等）时不套用这些规则，保留它筛出的内容。
- `file_read` 自己分页（每个文件一页最多 `tools.read_page_bytes`，默认 32 KiB；`paths` 一次最多四页），不会被外置。

## 后台任务

- 前台命令超过 `tools.shell_background_after` 后会转为后台任务：工具结果保留已输出并给出任务 id，完成时沿用任务通知；设为 `0` 关闭。硬上限仍是 `tools.shell_timeout_max`。
- 输出逐行脱敏写入会话的 outputs（`recall` 也能读它的 ref）。
- 任务在模型看到之前就结束时，会留下一条提示，在下一次模型请求前加入（退出码、耗时、大小、末尾几行）；正等待输入的 chat 会被它唤醒并开始一轮（`tasks.wake_on_exit`，默认 true，运行中的任务发来通知时同样唤醒）。
- `run` 在回答后仍有任务在跑时提醒模型一次，退出时终止它们并在 stderr 中列出名字。
- ACP 下任务属于 `suga acp` 进程：跨 prompt 继续运行，结束提示进入该会话的下一个 prompt；`session/cancel` 不影响它们，连接关闭时终止。
- 上限：`tasks.max_running`（每会话 4）、`tasks.max_duration`（2h）、`tasks.max_output_bytes`（64 MiB）。
- trace 里每个任务记一条 `task` 事件（带种类，watch 还带运行与变化次数），`--analyze` 按种类统计，包括 watch 任务数与后台 Stop hook 次数。
- 设计见仓库 `docs/design/2026-10-13-background-tasks-design.md`。

## 监听任务（watch）

- `shell_exec` 传 `background: true` 并带 `every`（秒，至少 10）和/或 `watch_paths`（相对工作区的 glob，`**` 跨目录，不含 `/` 的模式匹配任意深度的同名文件）时命令变成监听。
- 启动时先跑一次，之后每个周期到点、以及被监听文件变化（每 2 秒轮询，变化停止后的下一次轮询才触发）时再跑。
- 运行之间不会重叠，每次运行受调用的 `timeout` 限制（运行自身写入被监听的文件不算变化）。
- 输出文件里每轮以 `=== run N (触发原因) HH:MM:SS ===` 开头、`=== exit X ===` 结尾。
- 只有退出码或输出（忽略耗时与时间戳）与上一次不同才通知模型，基线运行只在失败时通知，所以模型可以改代码、再从下一轮通知得知测试是否转绿。
- 对监听调用 `task wait` 会在下一轮运行结束时返回，并带上这一轮的报告；`task list` 显示运行次数与最近一次退出码，`task kill` 停止监听。

## 后台 Stop hook

- 配置 `hooks.stop_background: true`（仅 chat）后，Stop hook 作为 `hook` 类任务运行，不再在提示符处等待：用户输入、投递、任务唤醒都不会中断它。
- hook 返回 block 加 reason 时产生通知 `Stop hook asks to continue: <reason>`：空闲的 chat 被唤醒并开始注入回合，回合进行中则在下一次模型请求前带上。
- 新的 Stop 到来时终止正在跑的那个。
- 设了 `hooks.stop_wait_sec` 时 hook 超时为它加 2 秒，否则由 `tasks.max_duration` 限制；没有空闲任务槽位时退回原来的等待。
- `hooks.command` 以 stdin 上的 JSON 接收 chat 的生命周期事件；其它 hooks 键见 `references/config.md`。

## MCP

- 用 `--mcp-config <file>`（Claude 风格的 `mcpServers` JSON）或配置里的 `mcp.servers` 接入。
- MCP 调用默认一律拒绝，需在 `tools.auto_allow`（`--read-only` 时用 `tools.read_only_allow`）中放行，条目形如 `server/工具glob`；两者都为空时所有 MCP 调用被拒绝。
- 工具很多时会延迟加载，模型通过 `tool_search` 取用。

## 子 agent

`agent` 工具把一件独立的任务交给子 agent：它是一个在自己的会话里运行的模型，只有只读工具，最终回答就是带回给主 agent 的报告。要大量阅读的任务——追调用链、比对多个实现、查清一个依赖的用法——因此只花掉父上下文一份报告，而不是每个读过的文件。

- **两种类型**：`explore`（默认）拿到工作区只读工具（`file_read`、`file_list`、`file_grep`、`file_find`）；`research` 再加 `web_fetch` 与 `web_search`。子 agent 不能写文件、不能执行命令、不能询问用户，也不能再派子 agent，并且看不到本会话的任何内容。调用里的 `description` 就是它在 chat、ACP 与 trace 里的名字。
- **后台**：传 `background: true` 时它作为任务（种类 `agent`）运行：调用立刻返回任务 id，`task wait` 等它结束，报告就是该任务的输出（结束时还有一条带报告开头的通知）。可以一次派几个并行探索（与 shell 任务共用 `tasks.max_running` 槽位）。只读会话也有 `task` 工具，因为它只用到 `list`/`output`/`wait`/`kill`（`shell_exec` 仍只在写模式后台运行）。
- **上限**：`agents.max_steps`（30）、`agents.max_tokens`（400000，按未命中缓存的输入加输出累计，长上下文命中缓存不会很快耗尽它）、`agents.timeout`（10m，软截止，之后 60 秒宽限）。到任一个上限时子 agent 被要求收尾，基于已有信息给出报告。
- **报告尾行**：`[sub-agent <会话> · N steps · in … / out … · <耗时> · stop=<原因>]`，原因有 `final`、`max_steps`、`max_tokens`、`timeout`、`cancelled`、`failed`。
- **模型与开关**：`agents.model` 可让子 agent 跑在更便宜的模型上；`agents.enabled: false` 则不注册该工具。
- **用量与污染**：子 agent 的模型调用计入调用它的会话当前进行中的 run（stream-json 与 ACP 的 `usage_update` 都含它，ACP 在 prompt 进行中上报），也计入会话累计——会话累计 = 父会话加它的全部子会话（`suga stats --session`）。子 agent 读过的外部内容会和其它外部读取一样把父会话标记为已污染：`research` 类型的子 agent（或读到了引用网页内容的文件）会让之后的写记忆需要确认。
- **子会话**：每次调用都在一个自己的会话里运行——`kind: agent`，带 `parent_session_id`、`agent_type` 以及发起它的回合与工具调用，标题就是 description。`suga sessions` 与 `session_search` 默认不列出子会话；`suga sessions --agents` 会列出并标明各自的父会话。`suga trace <会话>` 可以照常查看某一个子会话，`suga gc` 随父会话一起删除它。
