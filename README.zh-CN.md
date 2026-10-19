# Suga

[![Unit-Tests](https://github.com/inhere/suga/actions/workflows/go.yml/badge.svg)](https://github.com/inhere/suga/actions/workflows/go.yml)

[English](README.md) | 简体中文

Suga 是用 Go 写的个人编码 Agent。同一个持久会话引擎提供两种入口：终端 CLI（交互式 `chat` 和单轮 `run`）和 ACP stdio 服务（`acp`）。
[Gofer](https://github.com/inhere/gofer) 可以拉起这两种入口，并在其上提供作业、worker、日志和交互式审批。

> 详细文档都在内置 skill [suga-guide](skills/suga-guide/SKILL.md) 的 `references/` 里；suga 首次运行会把它释放到 `~/.suga/skills/suga-guide/`，在 chat 里直接问“怎么配置 suga”即可。

## 功能

- **开发工具**：分段读文件、列目录、内容/文件名搜索、精确字符串修改、shell 命令（Windows 用 PowerShell）、网页抓取与搜索。
- **默认安全**：不开启就是只读；chat 写文件、执行命令前先询问；文件工具限制在工作区内；网页抓取拒绝内网地址。
- **持久会话**：单个 SQLite 库，支持续接、自动压缩、计划、会话交接，以及用于 trace 和统计的事件记录。
- **子 agent 与后台任务**：只读子 agent 只带回报告；长命令、监听任务在后台运行，结束时通知模型。
- **Skills**：读取标准 `SKILL.md`，按需列出或检索；内置配置与使用向导 `suga-guide`。
- **MCP**：接入 stdio MCP 服务，放行名单控制，工具多时延迟加载。
- **持久记忆**：跨会话的用户与项目记忆，后端可换（files、gofer 或外部命令）。
- **历史检索与大输出外置**：全文检索过往会话；超大工具结果存到会话目录，上下文只留头尾与 ref。

## 安装

- **推荐**：通过 [eget](https://github.com/inherelab/eget) 安装：`eget install inhere/suga`
- Go 安装：`go install github.com/inhere/suga/cmd/suga@latest`（私有访问需 `GOPRIVATE=github.com/inhere/*`）
- 从 [GitHub Releases](https://github.com/inhere/suga/releases) 下载对应平台归档（推送 `v*` 标签自动发布 Linux、macOS、Windows 预编译包），把 `suga` 放进 `PATH`。
- 源码构建（Go 1.26+）：`git clone https://github.com/inhere/suga.git && cd suga && make install`
  - 构建（装了 `upx` 时顺带压缩）并安装到 `$(go env GOPATH)/bin`；Windows 上旧二进制被打开的 chat 占用时，会先把它改名挪开再安装。
- 确认安装目录在终端（以及 Gofer server）的 `PATH` 中，然后运行 `suga version` 检查。

## 快速开始

详细说明：[references/config.md](skills/suga-guide/references/config.md)（上手与配置命令）

```bash
suga init                                         # 创建 ~/.suga、带注释的 config.yaml 与只含注释的 .env（已有文件不覆盖）
# 在 ~/.suga/.env 填密钥，例如 DEEPSEEK_API_KEY=...（内置 provider：openai / anthropic / deepseek）
suga config set model deepseek/deepseek-chat      # 选模型（<provider>/<model>）；密钥不能用 config set 写
suga config set tools.allow_write true            # 允许 chat 写文件、执行命令（执行前会询问）
suga models                                       # 离线查看可用模型；suga config get <key> 看生效值与来源
suga chat                                         # 在当前目录开启交互式对话
```

- 单轮：`suga run --prompt "概括一下这个项目"`（只读）、`suga run --allow-write --prompt "修好失败的测试"`、`suga run --resume <session-id> --prompt "继续"`；`--max-steps` 限制本轮步数（默认 240）。
- 完整字段样例见 [config/config.yaml](config/config.yaml)；`suga inspect manifest` 显示完整的生效能力。

## Chat

详细说明：[references/chat.md](skills/suga-guide/references/chat.md)

- **本地命令**：`:session`、`:compact`、`:quit`/`:exit`、`:diff [N]`、`:model [p/m]`、`:add-dir`/`:dirs`/`:rm-dir`、`:help`、`:usage`、`:tasks`；只认本地键盘输入。
- **输入**：Up/Down 历史、Ctrl+R 反查、`:` 命令与目录 Tab 补全；多行输入用 `Ctrl+Enter`/`Ctrl+J`（Windows）或 `Alt+Enter`。
- **插话（steering）**：模型干活时提示符变成 `↳ `，回车把这句插进当前回合；`Esc` 只中止本轮，3 秒内两次 `Ctrl-C` 才退出。
- **`!` 本地命令**：`!cmd` 直接在本地 shell 执行、不叫模型；`!!cmd` 把结果附在下一条消息里给模型。
- **续接预览**：续接时先显示上一次模型回复；被强杀留下的 `running` 会话会自动恢复。
- **投递**：`suga send --session <id|短名称> <文本>` 把文本送进正在运行的 chat，`--steer` 插进当前回合，`--wait` 等待送达。

## 配置

详细说明：[references/config.md](skills/suga-guide/references/config.md)（全部键与默认值）、[references/providers.md](skills/suga-guide/references/providers.md)（provider 与模型）

- 只读取 `$SUGA_HOME/config.yaml` 和 `$SUGA_HOME/.env`（默认 `~/.suga`），工作目录下的配置不会被读取；`SUGA_CONFIG` 可指定唯一配置文件。
- `suga init` 可安全重复执行；`suga init --force` 只备份并替换 home 配置，绝不写入 `.env` 值，也不会绕过显式 `SUGA_CONFIG` 保护。
- `suga config get|set|unset|list|path` 显示生效值与来源、保留 YAML 注释，并拒绝把密钥写入 YAML；密钥放进 `.env`，配置用 `${VAR}` 引用。
- `suga models [--json]` 只离线查看本地 provider 和模型引用，不替代 `suga inspect manifest` 或 chat 会话级的 `:model` 切换。
- `model` 为 `<provider>/<model>`，优先级 `--model` > `SUGA_MODEL` > 配置；`fallback_models` 依次备用，`compact_model` 生成摘要。
- `providers.<id>`：`type`（`openai`、`openai-responses`、`anthropic`）、`base_url`、`api_key`、`headers`，以及模型级 `context_window`、`max_output_tokens`、`model_id`、`price`、`thinking`。
- 常用开关：`tools.allow_write`、`tools.approval`、`mcp.servers`、`memory.*`、`hooks.*`、`chat.*`、`skills.*`、`workspace`。
- 所有数据都在 `$SUGA_HOME/db/sessions.db`。

## 工具与权限

详细说明：[references/tools.md](skills/suga-guide/references/tools.md)

- **工具**：`file_read`/`file_list`/`file_grep`/`file_find`、`web_fetch`/`web_search`、`skill`、`session_search`/`recall`、`agent`、`memory`、`file_write`/`file_edit`/`file_remove`、`shell_exec`、`task`。
- **默认只读**：写工具需 `run --allow-write`、chat 的 `tools.allow_write: true` 或 ACP `default` 模式；`--read-only` 永远优先。
- **审批**：`tools.approval: ask` 时写/shell 前询问：`y` 一次，`a`/`d` 对同一范围（命令前缀、目录、路径）本会话允许或拒绝，可选写回规则文件；递归删除根目录、工作区等危险命令总要手动输入 `yes`。
- **回滚改动**：`file_write`/`file_edit`/`file_remove` 改文件前自动快照；chat 里 `:checkpoints` 查看、`:undo [N] [force]` 回滚，CLI 用 `suga undo <session>`；`shell_exec` 的改动只列出、不回滚（`tools.snapshots.*`）。
- **审批规则**：`tools.approval_rules` 从 `$SUGA_HOME/approval-rules.yaml` 与 `<workspace>/.suga/approval-rules.yaml` 读取 `allow`/`ask`/`deny` 规则（工具、命令与路径 glob）；取最强命中，项目级 `allow` 需 `project_trust`，规则不会放开受保护路径（`$SUGA_HOME`、`.suga/`、`.git/`）。
- **目录**：`--add-dir <绝对目录>[:ro|:rw]` / `SUGA_ADD_DIRS` 追加授权目录；chat 中越界访问会询问 `[r]/[w]/[n]`。
- **只读根**：自动识别依赖源码缓存与 idev-std 目录（`tools.dependency_roots`、`tools.standards_roots`），也可用 `tools.read_roots` 显式配置。
- **后台与监听任务**：`shell_exec` 带 `background: true` 后台运行；加 `every` / `watch_paths` 变成监听，结果变化时通知模型。
- **大输出**：超过 `tools.max_output_bytes` 的结果外置为 ref；shell 测试/构建输出先按输出规则精简（可在 `output-rules.yaml` 定制）。
- **MCP**：`--mcp-config` 或 `mcp.servers` 接入，默认全部拒绝，需在 `tools.auto_allow` / `tools.read_only_allow` 放行。
- **优先工具**：`tools.preferred` 声明本机已有的命令（如 `rg`、`xerj`）与 MCP 服务；可用项写进环境说明（`kind: command` 只在注册了 `shell_exec` 的可写会话里列出，只读会话不列），`suga inspect manifest` 对每项显示 `present` 与原因（包括缺失项，命令项的原因注明只读会话不列出）。优先只是排序提示：不会放行 MCP 调用、也不跳 `shell_exec` 的审批，缺失的项只是不进提示词。

## 子 agent

详细说明：[references/tools.md#子-agent](skills/suga-guide/references/tools.md#子-agent)

- `agent` 工具把独立任务交给只读子 agent，只把最终报告带回父上下文。
- `explore`（默认，工作区只读工具）与 `research`（再加 `web_fetch`、`web_search`）两种类型。
- `background: true` 作为后台任务运行，用 `task wait` 取报告，可并行派多个。
- 上限 `agents.max_steps`（30）、`agents.max_tokens`（400000）、`agents.timeout`（10m）；`agents.model` 可换便宜模型。
- 每次调用是一个子会话：`suga sessions --agents` 列出，用量计入父会话。

## Skills

详细说明：[references/sessions.md#skills](skills/suga-guide/references/sessions.md#skills)

- 按顺序扫描 `<工作区>/.agents/skills`、`<工作区>/skills`、`~/.suga/skills`、`~/.agents/skills`，同名先到者优先。
- 内置 `suga-guide` 首次运行释放，升级时只刷新未被你改过的文件。
- 数量超过 `skills.list_max` 时只列名字，模型可按名加载或 `skill(query=…)` 检索；`skills.always_load` 让正文常驻提示词。

## 记忆

详细说明：[references/sessions.md#记忆](skills/suga-guide/references/sessions.md#记忆)

- 模型通过 `memory` 工具写入跨会话记忆：`user`、`feedback`、`project`、`reference` 四类。
- 用户记忆在 `$SUGA_HOME/memory/user`；项目记忆自动选后端（仓库根 `MEMORY.md`、`.gofer/tracker` 或 `$SUGA_HOME/projects/<key>/memory`）。
- 记忆索引在每段开头冻结，本会话写入的内容下一段才可见。
- files 后端的 `memory search` 用可从 Markdown 重建的 FTS5 索引按相关度排序，条目可带可选的 `applies_when` 适用条件。
- 读过外部内容的会话写记忆需要确认（`memory.write_tainted`）。

## 会话

详细说明：[references/sessions.md](skills/suga-guide/references/sessions.md)

- `--session-id` 创建或复用，`--resume` 要求已存在（不存在退出码 4），`run --from` 继承摘要与计划开新会话。
- 换目录续接会先询问（切回原目录 / 在当前目录继续 / 取消）；非终端用 `--allow-cwd-change`。
- 上下文接近窗口或 `:compact` 时开启新段，带交接摘要（固定小节）；旧段可用 `session_search` / `recall` 取回，`session_search` 传 `kind: handoff` 可直接搜到摘要本身。
- 中断的回合回放到最后一个完整的工具调用，并附小结。
- 可选历史折叠（`context.fold_every`）；chat 空闲时后台整理（`context.idle_after`）。
- 系统提示词只含稳定内容；环境、规则（`AGENTS.md`）、记忆索引、skills 列表放在每段的上下文消息里。

## 配合 Gofer 使用

详细说明：[references/gofer.md](skills/suga-guide/references/gofer.md)、[docs/usage/gofer-personal-agent.md](docs/usage/gofer-personal-agent.md)

- Gofer 拉起 `suga run`（CLI 作业），或 `suga acp`（常驻会话，通过 `permission_policy` 逐次审批）。
- agent 配置、只读与可写两种 run、stream-json 事件和拒绝语义见上面两份文档。

## 观测

详细说明：[references/observability.md](skills/suga-guide/references/observability.md)

```text
suga inspect trace <session> [--turn <id>] [--json]            # 时间线：模型调用、工具、拒绝、计划
suga inspect trace <session> --analyze | --web                 # 循环分析 / 本机网页
suga sessions [关键词] [--since 7d] [--agents]                    # 会话列表
suga inspect stats [--session <id>] [--json]                   # 调用次数、耗时、token、缓存、成本
suga inspect export <session> | suga data gc --older-than 30d  # 导出 / 清理
suga data gc --retention [--apply]                             # 按来源清理：chat 180 天 / run+ACP 30 天
suga ps | suga inspect manifest                                # 运行中的 chat / 能力清单
```

- `run --output-format stream-json` 每次模型调用后输出累计 `usage`，重试输出 `retry`，上限收尾时 `result` 带 `wrap_up`。
- `trace --web` 只以只读方式打开会话库（库不存在或未初始化时报错，不创建、不迁移）；“自动刷新”默认关闭，打开后只拉取新增：回合进行中每 5 秒，空闲时每 15 秒检查新回合。
- 诊断日志只写 `$SUGA_HOME/logs/suga.log`；`SUGA_DEBUG=1` 同时写 stderr。

## 开发

```bash
go test -race ./...     # 单元测试
make smoke-chat         # 伪终端端到端 chat 测试，含 chat_memory 与 chat_idle（Linux/macOS，需 Python 3）
go run ./scripts/eval --fake --bin tmp/smoke/suga   # 任务评测，见 scripts/eval/README.md
make build-all          # 交叉编译到 dist/
```

- 评测支持多轮任务（`task.yaml` 的 `prompts`，同一会话依次发送），并报告 `first_call_uncached`（表格 FIRST 列：首个主循环模型调用的未缓存输入）；`--probe-prefix` 跑三组前缀缓存探针（同工作区、跨工作区、系统提示词带随机 nonce 的对照组）；`--agent omp` 用 omp 跑同样的任务做对比。
- `cmd/suga` 程序入口；`internal/cli` 每个命令一个文件；`internal/wiring` 组装 provider、工具、skills、提示词与 runner。
- `internal/personal`（回合、分段、交接、事件；`store` 为 SQLite 存储）；`internal/chat` 终端对话；`internal/acpserver` ACP 服务。
- `pkg/agentcore`：应用层之下可复用的各层，不依赖 `internal/*`（[README](pkg/agentcore/README.md)）：内核（模型/工具循环、run 状态、transcript），以及 `llm`（各 provider）、`schedule`（进程内 After/Every/Idle 定时）、`tools`（注册表、审批、延迟加载）、`tools/native`（内置工具）、`tools/mcp`（MCP 客户端）、`skills`、`memory`（存储接口、治理、路由；后端 `memory/files`、`memory/execstore`）、`redact`（密钥与隐形文本清理）和 `extproc`（外部命令的 exec 协议 v1 与凭据剥离）。
- 版本变更见 [CHANGELOG.md](CHANGELOG.md)；设计与计划文档在 [docs/](docs/README.md)。CI 在 Ubuntu 和 Windows 上用 Go 1.26 和 stable 跑测试；旧的控制面（team、cron、orchestrator）保留在 `v0.1.0` 标签。
