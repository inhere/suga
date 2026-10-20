# 会话、记忆与 skills 参考

会话的续接与交接、换目录检查、分段与压缩、中断回合、历史折叠、空闲整理、计划、提示词结构，以及长期记忆和 skills 的加载规则。chat 的按键与插话见 `references/chat.md`；trace / stats 等观测见 `references/observability.md`。

所有数据都在 `$SUGA_HOME/db/sessions.db`（单个 SQLite 库）。

## 会话

### 续接与交接

- `--session-id` 创建或复用指定 ID 的会话；`--resume` 要求会话已存在，不存在时以退出码 4 退出。
- `run --from <session>` 新建一个会话，继承来源会话的摘要、计划和已加载的工具。

### 换目录续接

- 会话记录着创建时的工作目录，换到别的目录续接不再静默：`chat --resume`（以及 `--session-id` 打开已存在的会话）在动手之前比较两者。
- 有终端时打印两个目录并询问：
  1. 切到会话原目录续接（进程切过去，工作区、shell 与会话短名都随之）；
  2. 在当前目录继续（把会话记录的工作目录改到这里，trace 里出现一条 `session_cwd_changed`）；
  3. 取消（不改动任何数据）。
- `run --resume` 与 stdin 是管道（脚本、job runner）的 chat 直接失败退出，错误里给出两个目录与两种做法：先 `cd <会话原目录>`，或加 `--allow-cwd-change` 在当前目录继续（同样会更新记录）。
- 比较时两边都做符号链接解析，Windows 下不区分大小写；没有记录目录的旧会话不拦截（下一回合补写）。

### 分段（压缩）

- 会话是一串段。每段以一条段起始记录开头（原因：开始、阈值、`:compact`、空闲、`--from`），记录里有上下文消息。
- 第一段之后还有交接包：面向当前任务的摘要、最近的用户请求原文和仍相关的 ref。摘要按固定小节输出（当前任务 / 坐标（文件 / 符号 / 分支 / 版本）/ 采纳与否决 / 验证到哪 / 进展与阻塞 / 下一步 / 与当前任务相关的引用），便于按小节定位，也便于全文检索。
- 上下文接近模型窗口或执行 `:compact` 时开启新段，原样保留的只有最后一组完整的工具调用/结果。
- 旧段仍在库里，用 `session_search` 和 `recall` 取回；交接摘要本身也进全文检索（分段交接与 `--from` 交接都算），`session_search` 传 `kind: handoff` 只看交接，它给的 ref 交给 `recall` 读原文。
- 摘要可用更便宜的 `compact_model` 生成（见 `references/config.md`）。

### 中断回合

- 以 `failed` / `cancelled` 结束的回合已经把效果落到了工作区（写文件、跑命令、提交），因此它的记录不再被整体丢弃：下一回合回放它到“最后一个完整的工具调用/结果”为止，并附一条小结，写明它执行过哪些工具、为何停下。
- 只截掉悬空尾部（没有结果的工具调用及之后的内容），小结会说明截掉了多少条。
- 这类回合的压缩与上下文更新记录仍然不生效（下一回合会重算），插话则退回输入行、不随回放。

### 历史折叠

- 每次请求都会重发此前的对话，其中大部分是模型自己的思考内容和整文件写入。
- 设置 `context.fold_every`（如 10；默认 0，关闭）与 `context.fold_keep`（默认 5）开启后，最近 5 步之前的步骤发送时去掉思考内容，超过 1000 字符的字符串参数（整文件写入、长编辑）换成一句说明，提示需要时重读文件。
- 折叠点每 10 步才前移一次，期间缓存前缀保持有效。存储的会话不变；折叠点前移时 `suga inspect trace` 显示一条 `fold`。
- T9 对比中每次请求的上下文减半、prompt 总量下降 59%（见仓库 `docs/research/2026-10-11-suag-vs-omp-loop-analysis.md`）。
- 默认关闭：P5 复测中每次折叠后重建缓存前缀约占未命中输入的三分之一。

### 空闲整理（chat）

- 超过 `context.idle_after`（默认 15m）没有输入，且上下文已达阈值的 `context.idle_min_ratio`（默认 0.65）时，chat 在后台开启新段。
- 回合进行中不会触发，开始输入即取消。
- 它打印的行以 `✦` 开头，与其他提示相区分。

### 计划

- `update_plan` 工具记录模型的计划。stream-json 中输出为 `plan` 事件，ACP 中转为计划更新。

### 系统提示词与段上下文

- 系统提示词只含稳定内容（身份与工作约定），同一模式下跨会话、跨工作区字节相同。
- 环境（系统、shell、工作区）、项目规则（每级一个文件，`rules.file`，默认 `AGENTS.md`，另有 `~/.suga/AGENTS.md`）、`--append-system-prompt`、记忆索引、skills 列表和延迟工具列表放在每段开头的上下文消息里。
- 段内发生变化（如规则文件被改）时，追加一条“上下文更新”消息，不改写已有内容。
- `--no-project-rules` 跳过项目规则。

### 投递与 hooks

- `suga send` 把文本送进正在运行的 chat（短名称、`--steer`、`--wait` 与退出码见 `references/chat.md` 的“投递”）。
- `hooks.command` 以 stdin 上的 JSON 接收 chat 的生命周期事件（键与事件列表见 `references/config.md` 的 `hooks.*`）。

## 记忆

跨会话共享的长期记忆，由模型通过 `memory` 工具写入。

- **四类条目**：`user`（用户是谁、偏好）、`feedback`（纠正与确认过的做法）、`project`（代码里看不出的目标、决定、进展）、`reference`（外部资源在哪）。不记：能从代码或 git 推出的内容、规则文件已有的内容、本次会话的临时细节、密钥。流程类内容应写成 skill。
- **用户记忆**：在 `$SUGA_HOME/memory/user`（files）。
- **项目记忆后端选择顺序**：`memory.project.backend` 的配置；仓库根有 `MEMORY.md`（files，仓库模式）；仓库根有 `.gofer/tracker`（gofer）；否则用 `$SUGA_HOME/projects/<key>/memory` 下的 files，`<key>` 为仓库目录名加完整路径的短哈希（如 `suga-5cb0fa32`，也显示为作用域 `project:<key>`）。
- **仓库模式**：索引就是 `MEMORY.md`，每条记忆放在 `.suga/memory/<id>.md`，可随项目提交；根目录的 `MEMORY.md` 不会由 suga 创建。
- **索引行带摘要**：索引行为 `- [标题](路径) — 类型：一句话摘要`；摘要是可空的元数据，为空时与旧格式逐字节相同，旧格式行照常解析。摘要折成一行，超过 120 字截断加 `…`。`memory` 工具 add/replace 的 `summary` 参数写它（add 必填，未给时用正文首句并提示，replace 留空则保留原摘要）；exec 后端用协议里的 `summary` 字段（旧脚本不返回时为空）；gofer 后端只有一个摘要槽（`--summary`，已用于标题），没有独立字段，索引行不带摘要。
- **补摘要**：`suga memory summaries --missing` 列出缺摘要的条目，`--fill` 用正文首句预览，`--fill --apply` 才写入；只改 `MEMORY.md` 的索引行，不动条目正文与标题（`--scope user|project` 限定范围）。
- **gofer 后端**：每条项目记忆是带 `suga` 标签的 gofer memory，只管理这些条目。
- **exec 后端**：各范围也可用 `exec`（外部命令），键见 `references/config.md` 的 `memory.*`。
- `suga inspect manifest` 会显示选了哪个后端及原因。
- **冻结的索引**：记忆索引（id、类型、标题与一句话摘要）放在段上下文消息里，本段内不变；本会话写入的内容下一段才可见，开启新段时刷新。正文用 `memory get` 读取。
- **检索**：`memory search` 对 files 后端按相关度排序（用会话库里的 FTS5 派生索引，标题、正文与 `applies_when` 都参与；1–2 个字的词照常命中）。Markdown 仍是唯一真源：索引在搜索前按条目增量同步，可随时重建——手改或删除条目文件、删掉整个会话库后，下一次搜索自动反映/重建；索引未绑定、同步或查询失败时不报错，回退到原来的子串搜索。gofer 后端按标题与正文做多词 AND 的子串匹配（最新在前）；exec 后端由外部命令决定。
- **适用条件**：条目可选 `applies_when`，写明它适用的版本、环境等条件（数组，每条单行、至多 200 字，最多 10 条）。`memory get` 会显示，参与搜索，但不进索引行（不占索引预算）；旧条目与旧版本互相兼容。
- **污染与确认**：会话一旦读过外部内容（`web_fetch`、`web_search`、MCP 工具、`session_search`、`recall`，以及 `memory.taint_tools` / `memory.taint_on_spill` 中的工具），就永久标记为已污染，此后写记忆需要确认（`memory.write_tainted`）：chat 在终端询问（`y`、`n`，`a` 表示本会话不再问），ACP 发权限请求，`run` 直接拒绝；`deny` 永不写入，`allow` 不询问。
- `--read-only`（及 ACP `read_only`）下该工具只读。普通 `run` 可以写记忆：它是 suga 自身的状态，不属于工作区写入。

## Skills

- **扫描顺序**：启动时按顺序扫描 `<工作区>/.agents/skills`、`<工作区>/skills`、`<SUGA_HOME>/skills`（默认 `~/.suga/skills`）与 `~/.agents/skills`；同名时先扫到的优先。
- **内置 suga-guide**：`<SUGA_HOME>/skills/suga-guide` 是内置的配置与使用向导：首次运行会写到这里，升级后只要目录里每个文件仍是 suga 写入的那份就会刷新。
- **内置标记**：它旁边的 `.suga-builtin.json` 记录 suga 写了什么（版本与每个文件的哈希）：你改过、加过文件的目录，或没有这个标记的目录（你自己的同名 skill）永远不被动。
- **列出规则**：skills 数量不超过 `skills.list_max` 时全部列在系统提示词里；超过时只列工作区 skills 的说明，其余只列名字（名字总计最多 3000 字节，再多只给数量）。模型可以按名字加载任意一个，或用 `skill(query=…)` 检索。
- **常驻正文**：要让某个 skill 的正文每次都放进提示词，可在其 frontmatter 写 `metadata: {always_load: true}`，或把名字加进 `skills.always_load`。
