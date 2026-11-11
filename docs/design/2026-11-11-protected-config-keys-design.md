<!-- template_id: design; template_version: 1.2.0 -->
# suga 安全配置键保护 设计

> 状态：Draft 0.2 / 待人工计划批准；issue `suga-hyj7`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-11 | Claude | 初稿：安全相关配置键只允许人修改；列出可选防护档位 Q1–Q3 待定 |
| 0.2 | 2026-11-11 | Claude | 记录用户选择（2026-11-11：“Q1 选 A，Q2、Q3 按推荐”），转为决策 D3–D5；方案只保留 A 档 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：模型（含被外部内容注入的模型）不能通过修改 suga 自己的配置，放宽之后会话的安全检查或获得持久的命令执行。
- scope freeze：只处理 `suga config set/unset` 与 SUGA_HOME 配置文件这两个入口；不做沙箱、不做 shell 命令解析。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget：最多 8 轮。
- 暴露度（SR1409）：高（已发布；涉及持久命令执行与安全检查开关）。两轴独立评审与 ledger，评审目录 `docs/review/2026-11-11-protected-config-keys/`。
- 停止条件：无开放的 `CORE_BLOCKING`。

## 背景与目标

- suga 的配置只有一层：SUGA_HOME（或 `SUGA_CONFIG` 指定）的 `config.yaml` 加环境变量，进程启动时读取；没有项目级配置层（`internal/config/load.go:33-67`）。
- `suga config set/unset` 写这个文件，只拒绝秘密键与注册表中 `Writable:false` 的键（`internal/cli/config.go:153-169`）。v0.55.0 起 `tools.origin_judge.*`、`tools.read_deny*` 已不可写，其余安全相关键仍可写（见“已确认事实”）。
- 模型在 run（如 gofer 可写任务，原生工具默认放行）或 trust 档下可以执行 `suga config set …`，修改会在之后的每个会话生效。最坏情况：`hooks.command`、`memory.*.command`、`mcp.servers` 被写成任意命令，成为持久命令执行；`tools.ask_on_external_origin`、`memory.write_tainted` 等被关掉，防护失效。
- 用文件工具改 `config.yaml` 已受保护路径约束（SUGA_HOME 写入询问或拒绝）；用 `shell_exec` 直接改文件不受约束。

用户可见结果：
- 你在终端里 `suga config set tools.read_roots …` 照常可用。
- 模型在会话里执行同样的命令会被拒绝，提示“安全相关配置只能由你在终端里修改”。

## 名词

- **安全配置键**：放宽检查、扩大读写或执行范围、引入外部可执行程序的配置键（清单见方案）。
- **会话内调用**：由 suga 会话的 `shell_exec` 启动的命令；suga 给这些子进程设置 `SUGA_SESSION_ID`。

## 范围与非目标

范围：安全配置键清单；`suga config set/unset` 对这些键的限制；文档。

非目标：
- 不阻止 `shell_exec` 直接编辑 `config.yaml`（无法可靠识别，见“风险”）；
- 不做配置签名或启动时完整性校验（列为可选档 Q1-C）；
- 不改非安全键的可写性。

## 已确认事实与规范

- **可写的安全相关键**（`internal/config/settings_registry.go` 中 `Writable: true`）：
  - 执行类：`hooks.command`、`hooks.stop_args`、`hooks.post_tool_use.tools`、`hooks.post_tool_use.name`、`memory.user.command`、`memory.user.args`、`memory.project.command`、`memory.project.args`、`mcp.servers`、`tools.shell`、`tools.interpreters`；
  - 放宽类：`tools.allow_write`、`tools.approval`、`tools.auto_allow`、`tools.read_only_allow`、`tools.read_roots`、`tools.dependency_roots`、`tools.standards_roots`、`tools.skill_roots`、`tools.ask_on_external_origin`、`tools.protect_changed_files`、`tools.approval_rules.*`、`chat.ask_outside_dirs`、`memory.write`、`memory.write_tainted`、`memory.taint_tools`、`memory.taint_on_spill`、`tools.shell_limits.*`、`tools.web_fetch.allow_private`、`tools.session_message.*`。
- **会话内调用标记**：suga 给 `shell_exec` 子进程导出 `SUGA_SESSION_ID`、`SUGA_SESSION_NAME`、`SUGA_TURN_ID`（`pkg/agentcore/tools/native/code_exec.go:563`，`internal/wiring/sessionenv.go:17`）。
- **TTY 判定**：`internal/cli/cwdcheck.go:214` 已有 `term.IsTerminal` 用法。
- **规范**：SR1301、SR1403、GR107、GR110。

## 总体方案

1. **清单**：注册表 `ConfigKey` 增字段 `Protected bool`，上文“已确认事实”中的键标记为 protected（含 `tools.origin_judge.*`、`tools.read_deny*`，它们改回 `Writable:true` + `Protected:true`，统一语义）。新增注册表测试：凡键名命中执行类与放宽类模式而未标 protected 即失败，防止以后新增的键漏标。
2. **`suga config set/unset` 对 protected 键的限制**（D3）：
   - 拒绝会话内调用：环境中有 `SUGA_SESSION_ID`，或标准输入不是终端时拒绝，错误写明“安全相关配置只能由你在终端里修改（或直接编辑 config.yaml）”。人在终端里照常使用。
3. **文档**：README 与 `docs/usage/gofer-personal-agent.md` 写明 protected 键清单与修改方式；明确“模型用 shell 直接改文件”不在防护内，可信模式下尤其如此。

## 架构

- `internal/config/settings_registry.go`：`ConfigKey.Protected` 与清单；
- `internal/cli/config.go`：`writableConfigKey` 之后增 `protectedKeyAllowed`（读环境变量与 TTY，判定放在 cli 层，GR107：命令层只做入口判断，清单在 config 包）；

## 关键流程

```text
suga config set <key> <value>
  → MatchConfigKey → 秘密键/只读键拒绝（现状）
  → key.Protected ?
       ├─ 否 → 写入（现状）
       └─ 是 → SUGA_SESSION_ID 存在 或 stdin 非终端 → 拒绝并说明
                 否则 → 写入
```

## 安全、数据、运维与回滚

- **SR1301**：鉴权——protected 键的修改只接受人在终端中的调用；注入防护——不涉及命令拼接；秘密——不变；入参校验——沿用现有校验；资源归属——不适用。
- **风险与限制**：
  - 绕过 1：模型在命令里去掉 `SUGA_SESSION_ID`（如 `env -u`）——仍因 `shell_exec` 子进程的标准输入不是终端而拒绝。
  - 绕过 2：模型用 `shell_exec` 直接编辑 `config.yaml`——本设计不防；启动时变化提示（原 C 档）列为后续，完全防护需要沙箱或配置签名，超出范围。
  - 外部来源裁决（v0.55.0）会对照抄外部内容的此类命令裁决，是另一道防线。
- **兼容**：人在终端里的用法不变；脚本或 CI 用 `suga config set` 改 protected 键会被拒，需改为直接编辑文件或设环境变量。
- **回滚**：revert；无数据变更。

## 决策

- **D1** protected 键由注册表字段统一标记，配测试防漏标。
- **D2** 限制只加在 `suga config set/unset` 入口；直接编辑文件不在防护内，文档写明。
- **D3**（Q1）采用 A：环境中有 `SUGA_SESSION_ID` 或标准输入不是终端时，拒绝修改 protected 键；不做启动时变化提示。
- **D4**（Q2）“已确认事实”中的执行类与放宽类全部列为 protected，含 `tools.session_message.*`、`tools.shell_limits.*`、`tools.web_fetch.allow_private`。
- **D5**（Q3）`tools.origin_judge.*`、`tools.read_deny*` 改为 `Writable:true` + `Protected:true`，与其他安全键统一。

## 待确认事项

无。Q1–Q3 已由用户选定（2026-11-11：“Q1 选 A，Q2、Q3 按推荐”），见 D3–D5。

## 结论与人工计划 Gate

本设计经两轴评审并由你批准后才写计划；批准不授权实施。
