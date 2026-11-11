<!-- template_id: design; template_version: 1.2.0 -->
# suga 安全配置键保护 设计

> 状态：Draft 0.3 / 待人工计划批准；issue `suga-hyj7`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-11 | Claude | 初稿：安全相关配置键只允许人修改；列出可选防护档位 Q1–Q3 待定 |
| 0.2 | 2026-11-11 | Claude | 记录用户选择（2026-11-11：“Q1 选 A，Q2、Q3 按推荐”），转为决策 D3–D5；方案只保留 A 档 |
| 0.3 | 2026-11-11 | Claude | 处理第 1 轮两轴评审（`docs/review/2026-11-11-protected-config-keys/`）与用户选择（2026-11-11：“Q4–Q6 按推荐”）：清单补端点类、`workspace` 等（D6）；如实写明判定只挡直接调用（D7）；加回启动时变化提示（D8）；显式分类表、验证要求、兼容说明、拒绝细节、替代声明 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

0.3 对第 1 轮评审的处理（按类自查：清单完整性、判定强度与误拒、其他写入口、验证与兼容）：

| 评审项 | 处理 |
|---|---|
| S1、F1（阻断） | 清单补端点类（`providers.<id>.base_url/type`、`providers.<id>.models.<name>.base_url/type`）、`workspace`、`models.judge`、`memory.*.backend`；`Protected` 同时加到动态描述符（D6） |
| S2、F2 | 风险改写为“挡直接调用、挡不住刻意绕过”（`</dev/tty`、`script`、pty、`env -u`、SDK `NoSessionEnv`），由启动提示兜底（D7、D8） |
| F3、S5 | 兼容说明列出误拒场景（chat `!`、hook 脚本、gofer exec、Git Bash/mintty、管道输入）与处理方式；写明对 xhen、gofer 的影响 |
| F4 | 防漏标测试改为显式分类表，覆盖全部可写键（含动态键） |
| F5 | 写明拒绝的退出码 2、i18n 提示、stdin 来源、检查放在 set/unset 共用路径；同步改动的注释与测试 |
| F6 | 待定键逐一分类（见清单与“不列入”） |
| S3 | 声明替代 2026-11-10 安全计划中“新键不可写”的决定，列出需同步的注释、文档、测试 |
| S4 | 增“验证要求” |
| S6、F7 | `suga init --force`、`suga data restore` 列为非目标，由启动提示兜底 |
| S7 | 架构写明分层参照与规范编号 |
| Q6 | 用户选择加回原 C 档：启动时变化提示（D8） |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：模型（含被外部内容注入的模型）不能用 `suga config set/unset` 放宽之后会话的安全检查、扩大读写范围、引入外部可执行程序或改变模型请求的去向；用其他方式改动这些配置时，下次启动能被你看到。
- scope freeze：`suga config set/unset` 的限制 + 启动时变化提示；不做沙箱、不做 shell 命令解析、不阻止直接编辑文件。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget：最多 8 轮。
- 暴露度（SR1409）：高（已发布 `v0.55.0`；涉及持久命令执行、API key 去向与安全检查开关）。两轴独立评审与 ledger，评审目录 `docs/review/2026-11-11-protected-config-keys/`。
- 停止条件：无开放的 `CORE_BLOCKING`。

## 背景与目标

- suga 的配置只有一层：SUGA_HOME（或 `SUGA_CONFIG`）的 `config.yaml` 加环境变量，进程启动时读取（`internal/config/load.go:33-67`）。
- 写 `config.yaml` 的 CLI 路径是 `suga config set/unset`（`internal/cli/config.go:61-110`），只拒绝秘密键与 `Writable:false` 的键（`:153-169`）；ACP 的 `set_config_option` 只切换会话模型。
- 模型在 run（如 gofer 可写任务，原生工具默认放行）或 trust 档下可以执行 `suga config set …`，修改在之后每个会话生效：
  - `hooks.command`、`memory.*.command`、`mcp.servers` 可成为持久命令执行；
  - `providers.<id>.base_url` 可把请求连同 API key 发往他处；
  - `workspace: /` 使整个文件系统成为工作区；
  - `tools.ask_on_external_origin` 等可关闭防护。
- 用文件工具改 `config.yaml` 受保护路径约束；`shell_exec` 直接改文件、`suga init --force`、`suga data restore` 不受约束。

用户可见结果：
- 你在终端里 `suga config set tools.read_roots …` 照常可用。
- 模型在会话里直接执行同样的命令被拒绝，退出码 2，提示“安全相关配置只能由你在 suga 之外的终端里修改，或直接编辑 config.yaml”。
- 安全相关配置被其他方式改过（包括模型直接改文件）时，下次启动在 chat 横幅、stream-json `session` 行与 trace 中提示改动的键名（不含取值）。

## 名词

- **protected 键**：放宽检查、扩大读写或执行范围、引入外部可执行程序、改变模型请求去向的配置键（清单见方案）。
- **会话内调用**：由 suga 会话启动的子进程（`shell_exec`、chat `!` 本地命令、hook 脚本）；suga 给它们导出 `SUGA_SESSION_ID`。
- **配置快照**：上次启动时 protected 键取值的逐键摘要，存于 SUGA_HOME 的状态目录。

## 范围与非目标

范围：protected 清单与显式分类表；`suga config set/unset` 对 protected 键的限制；启动时变化提示；文档。

非目标：
- 不阻止 `shell_exec` 直接编辑 `config.yaml`、`suga init --force` 重置、`suga data restore` 用备份覆盖（三者由启动提示兜底）；
- 不做配置签名、沙箱；
- 不改非 protected 键的可写性。

受影响项目：
- xhen：文档让你在终端里执行 `SUGA_HOME=… suga config set tools.read_roots …`（`xhen/README.md:151`），终端里照常可用，Git Bash/mintty 下可能被误拒（改为直接编辑文件）；xhen 进程内嵌 SDK，启动提示进 trace。
- gofer：仓库内未见 `suga config set` 调用；gofer exec 任务里的脚本改 protected 键会被拒；stream-json `session` 行新增字段（非空才出现，旧解析器忽略）。

## 已确认事实与规范

- **注册表**：`ConfigKey` 精确匹配（`internal/config/settings_registry.go:371-375`）；`providers.<id>.*`、`providers.<id>.models.<name>.*` 是动态描述符（`:311-331`，含可写的 `base_url`、`type`）；`mcp.servers` 是单个 YAML 键。
- **会话内调用标记**：`shell_exec` 子进程导出 `SUGA_SESSION_ID`、`SUGA_SESSION_NAME`、`SUGA_TURN_ID`（`pkg/agentcore/tools/native/code_exec.go:563`，`internal/wiring/sessionenv.go:17`）；chat `!` 本地命令同样带会话变量且 stdin 为 nil（`internal/chat/local_command.go:122-127`）；SDK 宿主可用 `NoSessionEnv` 不导出。
- **子进程终端**：`shell_exec` 只设 `Setpgid`（`proctree_unix.go:25`），子进程仍可经 `</dev/tty`、`script`、pty 取得终端。
- **TTY 判定**：`internal/cli/cwdcheck.go:214` 已用 `term.IsTerminal`；命令的 stdin 来自 `internal/cli/root.go:69`。
- **退出码**：配置输入错误为 `processExitError{code: 2}`（`internal/cli/config.go:282-284`）。
- **其他写入口**：`suga init --force` 重置配置；`suga data restore` 用归档中的 `config.yaml` 替换（`internal/cli/backup.go:152,432`）。
- **前序决定**：`docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md`（F14/S12）把 `tools.origin_judge.*`、`tools.read_deny*` 设为不可写（注册表注释 `:227`、`:250`，`docs/usage/gofer-personal-agent.md:124`，测试 `internal/cli/config_origin_judge_test.go`、`config_read_deny_test.go`）。
- **输出位置**：chat 横幅 `internal/chat/layout.go`（`BannerInfo`）；stream-json `internal/output/streamjson.go`；trace 事件 `internal/personal/observe.go`；文案 `internal/i18n/catalog`。
- **规范**：SR1301、SR1403、GR107、GR108、GR109、GR110、SR2004。

## 总体方案

### 1 清单与显式分类表（D4、D6）

注册表 `ConfigKey` 增字段 `Protected bool`，静态与动态描述符都可设置：

| 类别 | protected 键 |
|---|---|
| 执行类 | `hooks.command`、`hooks.stop_args`、`hooks.post_tool_use.tools`、`hooks.post_tool_use.name`、`memory.user.command`、`memory.user.args`、`memory.user.backend`、`memory.project.command`、`memory.project.args`、`memory.project.backend`、`mcp.servers`、`tools.shell`、`tools.interpreters` |
| 放宽类 | `tools.allow_write`、`tools.approval`、`tools.auto_allow`、`tools.read_only_allow`、`tools.read_roots`、`tools.dependency_roots`、`tools.standards_roots`、`tools.skill_roots`、`tools.ask_on_external_origin`、`tools.protect_changed_files`、`tools.approval_rules.*`、`chat.ask_outside_dirs`、`memory.write`、`memory.write_tainted`、`memory.taint_tools`、`memory.taint_on_spill`、`tools.shell_limits.*`、`tools.shell_timeout_max`、`tools.task_timeout_max`、`tasks.max_*`、`tools.web_fetch.allow_private`、`tools.session_message.*`、`tools.origin_judge.*`、`tools.read_deny`、`tools.read_deny_defaults`、`workspace`、`log.file` |
| 端点类 | `providers.<id>.base_url`、`providers.<id>.type`、`providers.<id>.models.<name>.base_url`、`providers.<id>.models.<name>.type`、`models.judge` |

不列入（理由）：`rules.file`（只接受不带路径的文件名）、`tools.preferred`、`skills.always_load`、`agents.*`、`tools.disabled`（只能关闭工具）、`model`/`compact_model`/`fallback_models` 等模型引用（只在已配置的 provider 之间选择；`models.judge` 例外，因它决定外部来源裁决由谁做）、其余显示与性能类键。

- 计划阶段把表中带 `*` 的项展开为注册表中的具体键名，并核对每个键存在。
- 测试：一张覆盖全部 `Writable:true` 键（含动态描述符）的显式分类表（protected / 不保护 + 理由）；注册表出现未分类的可写键，或分类与 `Protected` 字段不一致，测试失败。

### 2 `suga config set/unset` 的限制（D3、D7）

- 检查放在 set 与 unset 共用的可写键判定之后：键为 protected，且环境中有 `SUGA_SESSION_ID` 或 stdin 不是终端，则拒绝；stdin 取命令实际的输入（root 注入的那个），便于测试。
- 拒绝：退出码 2（与现有输入错误一致），提示走 i18n：“安全相关配置 <键> 只能由你在 suga 之外的终端里修改，或直接编辑 <config.yaml 路径>”。
- 人在终端里修改 protected 键成功后同时刷新配置快照（见 3），自己的修改不触发下次提示。
- 强度如实定位：只挡“模型直接调用 `suga config set`”；刻意绕过（`env -u SUGA_SESSION_ID … </dev/tty`、`script`、pty、SDK `NoSessionEnv`）与直接改文件同属一类，由 3 兜底。
- D5 使 `tools.origin_judge.*`、`tools.read_deny*` 从 v0.55.0 的“任何调用都不可写”变为“人在终端可写”，与其他 protected 键一致（替代声明见“安全”一节）。

### 3 启动时变化提示（D8）

- 启动时计算 protected 键当前取值的逐键摘要（SHA-256，不保存原值），与 SUGA_HOME 状态目录中的快照比较。有差异时：
  - chat：横幅显示“安全相关配置自上次运行以来有变化：<键名列表>；如果不是你改的，请检查 config.yaml”；
  - run：stream-json `session` 行新增 `protected_config_changed: [键名]`；
  - ACP、SDK 与上述入口：都写一条 trace 事件（`kind=session`、`name=protected_config_changed`，attrs 只含键名）。
- 提示后更新快照（每处改动只提示一次）；首次运行（无快照）只建快照不提示。
- 快照写入失败只记 Warn，不影响会话（GR109 例外理由：提示是辅助观测），下次启动再比较。

## 架构

代码组织参照 suga 现有分层（命令在 `internal/cli`、配置在 `internal/config`、装配在 `internal/wiring`，SR2004）：
- `internal/config/settings_registry.go`：`ConfigKey.Protected` 与静态、动态描述符标记；
- `internal/config` 新文件（如 `protected_snapshot.go`）：protected 键摘要计算、快照读写与比较；
- `internal/cli/config.go`：set/unset 共用的 protected 检查（命令层只做入口判断，GR107）；人工修改后刷新快照；
- `internal/wiring`：启动装配时调用 config 的比较函数，把结果交给各入口（GR108）；输出在 chat 横幅、stream-json、trace（位置见“已确认事实”）；
- `internal/i18n/catalog`：拒绝与提示文案。

## 关键流程

```text
suga config set|unset <key> …
  → MatchConfigKey → 秘密键 / 只读键拒绝（现状）
  → Protected ?
       ├─ 否 → 写入
       └─ 是 → SUGA_SESSION_ID 存在 或 stdin 非终端 → 拒绝（退出码 2，i18n 提示）
                 否则 → 写入 → 刷新快照

suga 启动（chat / run / acp / SDK）
  → 读配置 → 计算 protected 键摘要 → 与快照比较
       ├─ 无快照 → 建快照
       ├─ 相同 → 无事
       └─ 不同 → 按入口提示改动的键名 → 更新快照
```

## 安全、数据、运维与回滚

- **SR1301**：鉴权——protected 键的 CLI 修改只接受 suga 之外的终端调用；注入防护——不拼接命令；秘密——快照只存摘要、提示只含键名，`api_key` 等秘密键仍不可经 config set 写入；入参校验——沿用现有校验；资源归属——不适用。
- **风险与限制**：
  - 刻意绕过 CLI 判定（伪终端、去掉环境变量、SDK `NoSessionEnv`）与直接改文件、`init --force`、`data restore` 都不被阻止，只在下次启动提示；
  - 启动提示依赖快照，快照被同时改写可掩盖变化（与直接改文件同级风险）；
  - 本会话内的改动只影响之后的会话，下次启动即提示；
  - 外部来源裁决（v0.55.0）对照抄外部内容的此类命令裁决，是另一道防线。
- **兼容与误拒**：以下人的操作改 protected 键会被拒，提示改在 suga 之外的终端执行或直接编辑文件：chat 里 `!` 执行、hook 脚本、gofer exec 任务里的脚本、Git Bash/mintty（stdin 为管道）、`echo … | suga config set`。非 protected 键不受影响。
- **替代声明**：替代 `docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md` F14/S12 的“`tools.origin_judge.*`、`tools.read_deny*` 不可写”，改为 protected；同步注册表注释（`:227`、`:250`）、`docs/usage/gofer-personal-agent.md:124`、测试 `config_origin_judge_test.go`、`config_read_deny_test.go`（从“只读”改为“会话内拒绝、终端可写”）。
- **文档**：README 与 `docs/usage/gofer-personal-agent.md` 写明 protected 键、修改方式、启动提示与限制。
- **数据**：快照文件（键名 → 摘要）位于 SUGA_HOME 状态目录；无表结构变化。
- **回滚**：revert；删除快照文件即恢复“首次运行”。
- **验证要求**（计划中落实）：
  - GR110：经 `internal/cli` 真实命令分发的测试——会话内（设 `SUGA_SESSION_ID`）与非 TTY 的 set/unset protected 键被拒、退出码 2、配置文件未变；非 protected 键照常；终端（伪终端）下 protected 键可写；
  - 显式分类表测试（含动态描述符），并做反向验证；
  - 启动提示：快照首建、无变化、变化后提示一次、人工 set 后不提示；chat 横幅用 `test/ptye2e` + `suga inspect trace --json` 断言并反向验证；stream-json 字段测试；
  - 主机 Windows 测试（终端判定，Windows Terminal 下人工 set 可用）。

## 决策

- **D1** protected 键由注册表字段统一标记，显式分类表测试防漏标。
- **D2** CLI 限制只加在 `suga config set/unset`；其他改动方式由启动提示兜底。
- **D3**（Q1）判定：环境中有 `SUGA_SESSION_ID` 或 stdin 不是终端时，拒绝修改 protected 键。
- **D4**（Q2）执行类与放宽类全部列为 protected，含 `tools.session_message.*`、`tools.shell_limits.*`、`tools.web_fetch.allow_private`。
- **D5**（Q3）`tools.origin_judge.*`、`tools.read_deny*` 改为 `Writable:true` + `Protected:true`，替代前序“不可写”决定。
- **D6**（Q4）清单增端点类（`providers.<id>.base_url/type`、模型级 `base_url/type`、`models.judge`）、`workspace`、`memory.*.backend`；显式分类表覆盖全部可写键（含动态键）。
- **D7**（Q5）CLI 判定如实定位为“挡直接调用、挡不住刻意绕过”；接受“兼容与误拒”中列出的误拒，提示改用外部终端或直接编辑文件。
- **D8**（Q6）加回启动时变化提示：逐键摘要快照，变化只报键名、提示一次，人工 set 刷新快照，不阻断。

## 待确认事项

无。Q1–Q3（2026-11-11：“Q1 选 A，Q2、Q3 按推荐”）与 Q4–Q6（2026-11-11：“Q4–Q6 按推荐”）已由用户选定，见 D3–D8。

## 结论与人工计划 Gate

本设计经两轴评审并由你批准后才写计划；批准不授权实施。
