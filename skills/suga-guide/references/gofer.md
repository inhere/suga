# 接入 gofer

suga 提供两个 Gofer agent：`suga`（一次性 CLI job）与 `suga-acp`（常驻 ACP 会话，支持逐次审批）。两者共用同一份个人会话库（`$SUGA_HOME/db/sessions.db`，启动 Suga 的那个用户的 `SUGA_HOME`）。Gofer job ID 是执行记录，suga session ID 是对话记录。

## 准备二进制

1. 在 suga 仓库 `make install`（装到 `$(go env GOPATH)/bin`，Windows 上若 chat 正在运行该二进制会先改名让位）。
2. 确认该目录在**终端**和 **Gofer server 进程**的 PATH 里。
3. 新终端执行 `suga version`，输出形如 `suga 0.18.0 (abc1234)`——Gofer 的 `detect` 就用它探测版本。

## Gofer 侧 agent 配置（`agents.yaml`）

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

- 用 `gofer config validate server` 检查文件，按当前 Gofer 服务的配置管理流程 reload，再用 `gofer agent list --runner server` 确认两项可用。
- 想在 Gofer 里看到工具调用/计划：把 `tool_call`、`tool_result`、`plan` 加进 `ndjson_keep`。
- `--allow-write` 加进 `args` / `session_resume` 就得到可写 CLI job（run 没有逐次确认，只适合可信的本机任务）；需要逐次审批用 `suga-acp`。

## 模型与密钥

- 模型地址与认证由**启动 suga 的那个用户**的 `$SUGA_HOME/config.yaml` / `.env` 决定（数组示例见 `config/config.yaml`，字段含义见 `references/config.md` 与 `references/providers.md`）。
- 主模型优先级 `--model` > `SUGA_MODEL` > 配置 `model`；这些选项**不接收密钥**。
- 不要把密钥写进 agent argv、job `--env` 或任务文本：Gofer 会持久化 job 请求。密钥放 `$SUGA_HOME/.env` 或 Gofer server 进程可用的稳定环境变量里。
- `SUGA_HOME` 替换 `~/.suga` 作为配置与数据根；会话库位置只由它决定。

## 只读与放行

- `--read-only` 映射为 ACP `read_only` mode / CLI 的只读运行：写文件、命令与 MCP 工具不加入可执行集合。
- 只读 job 需要的 MCP 工具必须在 suga 配置里列进 `tools.read_only_allow`（条目必须写成 `server/toolglob`）；`tools.auto_allow` 不会扩大它。**不配置则所有 MCP 调用被拒绝**，这是只读 job 里 MCP 不生效的常见原因。

```yaml
tools:
  auto_allow: ["gofer/gofer_*_list", "gofer/gofer_*_get", "gofer/gofer_work_report"]
  read_only_allow:
    - gofer/gofer_*_list
    - gofer/gofer_*_get
    - gofer/gofer_work_report
    - gofer/gofer_session_list
```

- job 环境里一般设 `SUGA_HOOKS=off`（job 内不再启用 chat hooks），并用 `SUGA_APPEND_SYSTEM_PROMPT` 或 `--append-system-prompt` 提供该 job 需要的汇报提示（如“可用 `gofer work report` / `gofer issue` 汇报”）。命令行参数优先；不传或传空串都沿用会话已保存的追加提示，用 `--clear-append-system-prompt` 清空。

## 常用命令

```powershell
# 一次性只读 job
gofer job run --runner server --project <项目> --cwd <相对路径> --agent suga --read-only --prompt "检查当前项目"
gofer job show <job-id>                 # 读 session_id
gofer job logs <job-id> --stream stdout --tail -n 20
gofer job resume <job-id> --prompt "继续上个问题"

# 常驻 ACP 会话（逐次审批）
gofer job run --runner server --project <项目> --agent suga-acp --session --read-only --prompt "检查当前项目"
gofer job say <job-id> "继续分析"
gofer job end <job-id>
gofer job resume <job-id> --prompt "从上次会话继续"

# 审批
gofer job interactions <job-id>
gofer job answer <job-id> <interaction-id> reject|allow
```

- CLI job 的 `stdout` 是 Gofer 提取的最终答复；`stderr` 保留原始 NDJSON 便于排查。
- `suga send`（agent 配置里的 `deliver_command` + `deliver_stdin: true`）把文本送进正在运行的 chat：忙时最多排队 8 条，空闲时进入下一轮；chat 退出时终端会提示丢弃条数。加 `--steer` 则插进正在进行的回合（默认开着 `chat.steering` 时模型在下一次请求前看到；空闲/回合收尾/关了开关时仍排队，收件箱满（8 条）返回 `queue_full`），再加 `--wait` 就等插话送达或作为自己的回合开始（0），被退回（回合被取消/失败、chat 先退出）时退出码 3。

## 审批与拒绝语义

- 审批在工具注册表的包装层完成（外到内：observe → visibility → approval → 工具），CLI 与 ACP 共用。
- ACP 默认 mode 下写/执行类工具与所有 MCP 工具在执行前调用 `session/request_permission`；拒绝时工具不执行，拒绝信息作为工具错误交给模型，回合继续；每次拒绝记一条 denial 事件，同一回合累计 3 次拒绝后结束回合。
- CLI 未授权的 MCP 工具、只读 mode 下调用的写工具都按拒绝处理，并给出开启方法（run：`--allow-write`；chat：`tools.allow_write: true`；ACP：把会话 mode 改成 `default`）。
- stream-json：本回合发生过拒绝时，`result` 之前会有一行 `input_required` 带 `denied_tools`（去重后的工具名）；`result` 的 `status` 仍是 `COMPLETED`。
- ACP：拒绝后模型正常完成时 `stopReason` 为 `end_turn`，累计 3 次拒绝才返回 `refusal`，审批被取消返回 `cancelled`；被拒工具的 `tool_call` 状态是 `failed`。

## 用量与观测

- stream-json 每次模型调用后输出一行 `usage`（本次 run 的累计值：`input_tokens`（不含缓存）、`output_tokens`、`cache_read_tokens`、`cache_write_tokens`、`total_tokens`、`cost_usd`、`tool_calls`）；`turn_end` 与 `result` 带同名 `usage`，`result` 是最终值。
- `result` 行在因上限收尾时带 `wrap_up`（步数上限为 `"max_steps"`），正常答复不带该字段；`--output-format text` 时该原因打到 stderr，stdout 仍只有答复正文。单轮步数上限可用 `run --max-steps N`（N 至少 1，需自己加进 agent args）或环境变量 `SUGA_MAX_STEPS` 覆盖配置 `max_steps`（默认 240）；子 agent 用 `agents.max_steps`。
- ACP 每次模型调用后与每轮结束都发 `usage_update`：`_meta` 顶层扁平键是**本次 run** 的累计（Gofer 的预算按此计），另附 `_meta.run` 与 `_meta.session`（会话累计）；顶层 `cost.amount` 是会话累计；`used`/`size` 是最近一次请求的上下文与窗口。
- 观测命令在**运行 suga 的那台主机**上执行（Gofer worker 用户的 `SUGA_HOME`）：

```text
suga inspect trace <session> [--turn <id>] [--json] [--analyze]    # 时间线、循环分析
suga inspect trace <session> --web [--addr 127.0.0.1:0]            # 本机网页时间线
suga inspect stats [--session <id>] [--since 7d] [--json]          # 用量与成本
suga session list [关键词] [--agents] [--json]                      # 会话列表
suga inspect export <session> [--redact] [-o <file>]               # jsonl 导出
suga inspect manifest --json                                       # 能力清单
```

## 会话交接与兼容

- `suga run --from <旧会话> --prompt ...`：新建会话并继承来源的摘要、计划与已加载工具（来源只读）。Gofer 侧目前没有对应模板变量，需要在 job 的 agent args 里手工写 `--from <session-id>`。
- 会话不存在（`--resume` 目标或 `--from` 来源）：退出码 4，stderr `session not found: <id>`；被 Ctrl-C/SIGTERM 中断：退出码 130。
- 换目录续接：会话原目录与当前工作目录不一致时，`run --resume`（含 Gofer job 的 `session_resume`）退出码 1，stderr 给出两个目录与两种做法：先 `cd <会话原目录>`，或给 args 加 `--allow-cwd-change` 在当前目录继续（同时把会话的 `sessions.cwd` 改到当前目录，并在 trace 写一条 `session_cwd_changed`）；`chat --resume`（`session_resume_interactive`）有终端时改为三选一询问，自动流程建议用 `--allow-cwd-change`。旧会话没有记录目录时不拦。
- 旧版本的库（`suag.db`、`agentcore/` 目录）不读取、不迁移、不删除：旧会话 ID 无法 `--resume`，Gofer 里登记的旧 `session_family: suag` 会话也不能再被唤醒，需要新建会话。
- 历史作业若用已移除的 `suag-cli` agent 键，不能直接用当前的 `suga` 键续接。

仓库 `docs/usage/gofer-personal-agent.md` 是同一主题的完整文档（含事件线格式、内部细节与迁移清单），本文只取接入与使用需要的部分。
