<!-- template_id: design; template_version: 1.2.0 -->
# suga 安全配置键保护 设计

> 状态：Draft 0.4 / 待人工计划批准；issue `suga-hyj7`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-11 | Claude | 初稿：安全相关配置键只允许人修改；列出可选防护档位 Q1–Q3 待定 |
| 0.2 | 2026-11-11 | Claude | 记录用户选择（2026-11-11：“Q1 选 A，Q2、Q3 按推荐”），转为决策 D3–D5；方案只保留 A 档 |
| 0.3 | 2026-11-11 | Claude | 处理第 1 轮两轴评审（`docs/review/2026-11-11-protected-config-keys/`）与用户选择（2026-11-11：“Q4–Q6 按推荐”）：清单补端点类、`workspace` 等（D6）；如实写明判定只挡直接调用（D7）；加回启动时变化提示（D8）；显式分类表、验证要求、兼容说明、拒绝细节、替代声明 |
| 0.4 | 2026-11-11 | Claude | 处理第 2 轮两轴评审与用户选择（2026-11-12：“Q7、Q8 按推荐”）：快照只在 chat 横幅展示后推进、人工 set 只更新该键（D9）；按 config.yaml 原始值比较（D10）；去掉 stream-json 字段，run 改为 stderr 一行；补 `tools.disabled`、`rules.file`、压缩模型；写明分类原则、快照位置与并发、判定细节、受影响测试与引用 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

各轮评审的处理（详见 `docs/review/2026-11-11-protected-config-keys/`）：

| 轮次 | 评审项 | 处理 |
|---|---|---|
| 1 | S1、F1（阻断） | 清单补端点类、`workspace` 等；动态描述符可标 `Protected`（D6） |
| 1 | S2、F2、F3、S5 | 强度如实定位与误拒场景（D7）；受影响项目 |
| 1 | S3、S4、S6、S7、F4–F7 | 替代声明、验证要求、非目标、显式分类表、拒绝细节 |
| 2 | S8、F8（阻断） | 快照只在 chat 横幅展示后推进；人工 set/unset 只更新被改键的摘要（D9） |
| 2 | F9、S11 | 写明各入口的比较与送达方式；去掉 stream-json 字段（无消费方）；trace 新事件声明为新增接口 |
| 2 | F10 | 快照放 `SUGA_HOME/run/`（不在 `data restore` 范围内），按配置文件路径区分，持配置锁、原子写，损坏不静默重建；按原始值比较（D10） |
| 2 | S9、F11、F12 | 补 `tools.disabled`、`rules.file`、`models.compact`/`compact_model`；写明分类原则与“不保护”各组理由 |
| 2 | S10 | 记录 Q4–Q8 的选项；D6 列全新增键 |
| 2 | F13 | 判定为 `SUGA_SESSION_ID` 非空；终端判定可注入；列出受影响测试；补验证项 |
| 2 | S12、F14、F15 | 修正引用；补 Windows 与空值绕过；修正 GR107 引用 |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：模型（含被外部内容注入的模型）不能用 `suga config set/unset` 放宽之后会话的安全检查、扩大读写或执行范围、引入外部可执行程序或改变模型请求的去向；用其他方式改动这些配置时，你下次打开 chat 能看到。
- scope freeze：`suga config set/unset` 的限制 + 启动时变化提示；不做沙箱、不做 shell 命令解析、不阻止直接编辑文件。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget：最多 8 轮。
- 暴露度（SR1409）：高（已发布 `v0.55.0`；涉及持久命令执行、API key 去向与安全检查开关）。两轴独立评审与 ledger，评审目录 `docs/review/2026-11-11-protected-config-keys/`。
- 停止条件：无开放的 `CORE_BLOCKING`。

## 背景与目标

- suga 的配置只有一层：SUGA_HOME（或 `SUGA_CONFIG`）的 `config.yaml` 加 `SUGA_HOME/.env` 与进程环境变量，进程启动时读取（`internal/config/load.go:33-67`）。
- 写 `config.yaml` 的 CLI 路径是 `suga config set/unset`（`internal/cli/config.go:61-110`），只拒绝秘密键与 `Writable:false` 的键（`:153-169`）；ACP 的 `set_config_option` 只切换会话模型。
- 模型在 run（如 gofer 可写任务，原生工具默认放行）或 trust 档下可以执行 `suga config set …`，修改在之后每个会话生效：
  - `hooks.command`、`memory.*.command`、`mcp.servers` 可成为持久命令执行；
  - `providers.<id>.base_url` 可把请求连同 API key 发往他处；
  - `workspace: /` 使整个文件系统成为工作区；`rules.file` 可指向模型写好的文件，成为持久注入的指令；
  - `tools.ask_on_external_origin`、`tools.disabled` 等可关闭防护或重新启用你禁用的工具。
- 用文件工具改 `config.yaml` 受保护路径约束；`shell_exec` 直接改文件、`suga init --force`、`suga data restore` 不受约束。

用户可见结果：
- 你在终端里 `suga config set tools.read_roots …` 照常可用。
- 模型在会话里直接执行同样的命令被拒绝，退出码 2，提示“安全相关配置只能由你在 suga 之外的终端里修改，或直接编辑 config.yaml”。
- 安全相关配置被其他方式改过（包括模型直接改文件）时，你下次打开 chat，横幅列出改动的键名（不含取值）；在此之前每次 run 在 stderr 提示一行、每个会话的 trace 记一条。

## 名词

- **protected 键**：放宽检查、扩大读写或执行范围、引入外部可执行程序或指令、改变模型请求去向的配置键（清单见方案）。
- **会话内调用**：由 suga 会话启动的子进程（`shell_exec`、chat `!` 本地命令、hook 脚本）；suga 给它们导出非空的 `SUGA_SESSION_ID`。
- **配置快照**：每个 protected 键在 `config.yaml` 中原始取值的摘要，代表“你已经看到过的状态”。

## 范围与非目标

范围：protected 清单与显式分类表；`suga config set/unset` 对 protected 键的限制；启动时变化提示；文档。

非目标：
- 不阻止 `shell_exec` 直接编辑 `config.yaml`、`suga init --force` 重置、`suga data restore` 用备份覆盖（三者由启动提示兜底）；
- 不比较 `${VAR}` 引用经 `.env` 或环境变量展开后的值（D10）；
- 不做配置签名、沙箱；不改非 protected 键的可写性。

受影响项目与接口：
- xhen：README 让你在终端里执行 `SUGA_HOME=… suga config set tools.read_roots …`（`xhen/README.md:151`），终端里照常可用，不需改文档；Git Bash/mintty 下会被拒（改为直接编辑文件），由 xhen 侧在跟进 suga 版本时补一句说明。xhen 内嵌 SDK，提示进 trace。
- gofer：仓库内未见 `suga config set` 调用；gofer exec 任务里的脚本改 protected 键会被拒；suga run 的 stderr 多一行提示（进 job 日志）。
- 新增接口（只增不改）：trace 事件 `kind=config`、`name=protected_config_changed`（attrs 只含键名列表）；SUGA_HOME 下新文件 `run/protected-config*.json`。

## 已确认事实与规范

- **注册表**：`ConfigKey` 精确匹配（`internal/config/settings_registry.go:371-375`）；`providers.<id>.*`、`providers.<id>.models.<name>.*` 是动态描述符（`:310-357`，含可写的 `base_url`、`type`）；`mcp.servers` 是单个 YAML 键。
- **会话内调用标记**：`shell_exec` 子进程导出 `SUGA_SESSION_ID`、`SUGA_SESSION_NAME`、`SUGA_TURN_ID`（`pkg/agentcore/tools/native/code_exec.go:563`，`internal/wiring/sessionenv.go:17`）；chat `!` 本地命令同样带会话变量且 stdin 为 nil（`pkg/agentcore/tools/native/local_command.go`）；SDK 宿主可用 `NoSessionEnv` 不导出。
- **子进程终端**：`shell_exec` 只设 `Setpgid`（`pkg/agentcore/internal/proctree/proctree_unix.go:25`），子进程仍可取得终端（Unix `</dev/tty`、`script`、pty；Windows `< CON`、`CONIN$`）。
- **TTY 判定**：`isTerminalInput` 要求 stdin 为 `*os.File`（`internal/cli/cwdcheck.go:212-215`）；config 命令目前不接收 stdin（`internal/cli/root.go:69`、`internal/cli/config.go:21`），需改为由 root 传入。
- **退出码**：配置输入错误为 `processExitError{code: 2}`（`internal/cli/config.go:282-284`）。
- **配置锁**：`configLockPath` 在 `SUGA_HOME/run/locks/` 下按配置路径的 `sha256` 命名（`internal/config/yaml_edit.go:102-127`）。
- **其他写入口**：`suga init --force` 重置配置；`suga data restore` 替换 `db/sessions.db`、`config.yaml`、`memory/user`、`projects`（`internal/personal/backup/backup.go:432`），不含 `run/`。
- **trace**：事件没有 session ID 时丢弃（`internal/personal/observe.go:107-108`）。
- **前序决定**：`docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md`（F14/S12）把 `tools.origin_judge.*`、`tools.read_deny*` 设为不可写（注册表注释 `:227`、`:250`，`docs/usage/gofer-personal-agent.md:125`，测试 `internal/cli/config_origin_judge_test.go`、`config_read_deny_test.go`）。
- **规范**：SR1301、SR1403、GR108、GR109、GR110、SR2003、SR2004。

## 总体方案

### 1 清单与显式分类表（D4、D6）

注册表 `ConfigKey` 增字段 `Protected bool`，静态与动态描述符都可设置。分类原则：键能扩大模型可执行、可读写的范围，引入外部程序或指令，改变请求去向，或关闭约束这些能力的检查与上限，即为 protected。

| 类别 | protected 键 |
|---|---|
| 执行类 | `hooks.command`、`hooks.stop_args`、`hooks.post_tool_use.tools`、`hooks.post_tool_use.name`、`memory.user.command`、`memory.user.args`、`memory.user.backend`、`memory.project.command`、`memory.project.args`、`memory.project.backend`、`mcp.servers`、`tools.shell`、`tools.interpreters`、`rules.file` |
| 放宽类 | `tools.allow_write`、`tools.approval`、`tools.auto_allow`、`tools.read_only_allow`、`tools.disabled`、`tools.read_roots`、`tools.dependency_roots`、`tools.standards_roots`、`tools.skill_roots`、`tools.ask_on_external_origin`、`tools.protect_changed_files`、`tools.approval_rules.{enabled,always_prefix_tokens,persist_ask,project_trust}`、`chat.ask_outside_dirs`、`memory.write`、`memory.write_tainted`、`memory.taint_tools`、`memory.taint_on_spill`、`tools.web_fetch.allow_private`、`tools.session_message.{enabled,in_gofer_jobs,max_hops}`、`tools.origin_judge.{enabled,timeout}`、`tools.read_deny`、`tools.read_deny_defaults`、`workspace` |
| 执行上限类 | `tools.shell_limits.{memory,processes,cpu_time,off}`、`tools.shell_timeout_max`、`tools.task_timeout_max`、`tasks.{max_running,max_duration,max_output_bytes}` |
| 端点类 | `providers.<id>.base_url`、`providers.<id>.type`、`providers.<id>.models.<name>.base_url`、`providers.<id>.models.<name>.type`、`models.judge`、`models.compact`、`compact_model`（外部来源裁决未配 `models.judge` 时回退到压缩模型） |
| 其他 | `log.file`（可把日志写到任意路径） |

不保护（按组）：
- 费用与步数（`budget.*`、`max_steps`、`agents.max_steps`、`agents.max_tokens`、`agents.timeout`）：只影响花费和轮数，不扩大权限；
- 留存与可恢复性（`gc.retention.*`、`tools.snapshots.*`）：影响保留多久、能否回滚，不扩大权限；缩短留存可能提前清掉 trace，列为已知风险；
- 模型选择（`model`、`fallback_models`、`agents.model`、`models.*` 中除 `judge`、`compact` 外的键）：只在已配置的 provider 之间选择；
- 其余工具偏好、显示、性能与 provider 调参键（`tools.preferred`、`skills.always_load`、`agents.enabled`、`agents.nudge`、`providers.<id>` 下除 `base_url`、`type` 外的键等）。

测试：一张覆盖全部 `Writable:true` 键（含动态描述符）的显式分类表（protected / 不保护 + 所属组）；注册表出现未分类的可写键，或分类与 `Protected` 字段不一致，测试失败。

### 2 `suga config set/unset` 的限制（D3、D7）

- 检查放在 set 与 unset 共用的可写键判定之后（`internal/cli/config.go`）：键为 protected，且 `SUGA_SESSION_ID` 非空或 stdin 不是终端，则拒绝。
- config 命令改为接收 root 的 stdin；终端判定经可注入的函数完成（默认 `isTerminalInput`），测试可替换。
- 拒绝：退出码 2（与现有输入错误一致），配置文件不变；提示走 i18n：“安全相关配置 <键> 只能由你在 suga 之外的终端里修改，或直接编辑 <config.yaml 路径>”。
- 人在终端里修改 protected 键成功后，只把该键的摘要更新到快照（见 3，D9），不重算其他键，不会吸收此前未展示的改动。
- 强度如实定位：只挡“模型直接调用 `suga config set`”；刻意绕过（`SUGA_SESSION_ID=` 置空或 `env -u`，再配合 Unix `</dev/tty`、`script`、pty 或 Windows `< CON`/`CONIN$`；SDK `NoSessionEnv`）与直接改文件同属一类，由 3 兜底。
- D5 使 `tools.origin_judge.*`、`tools.read_deny*` 从 v0.55.0 的“任何调用都不可写”变为“人在终端可写”，与其他 protected 键一致。

### 3 启动时变化提示（D8、D9、D10）

- **比较**：读配置后，对每个 protected 键在 `config.yaml` 中的原始取值（未展开 `${VAR}`；缺省视为“未设置”）计算 SHA-256 摘要，与快照比较，得到改动的键名列表。快照无法读取或损坏时视为全部 protected 键已改动，提示中注明“快照无法读取”，不静默重建。
- **快照**：`SUGA_HOME/run/protected-config.json`；`SUGA_CONFIG` 指向其他文件时为 `run/protected-config-<sha256(路径)>.json`（与配置锁同一命名法）。读改写持该配置文件的配置锁，写入用临时文件加改名。首次运行（文件不存在）只建快照不提示。
- **各入口**：

| 入口 | 有改动时 | 推进快照 |
|---|---|---|
| chat | 横幅显示“安全相关配置自上次确认以来有变化：<键名>；如果不是你改的，请检查 <config.yaml 路径>”；并写 trace | 横幅显示后整体推进 |
| run | stderr 一行同样内容；写 trace | 否 |
| ACP、SDK（含 xhen） | 写 trace | 否 |
| `suga config set/unset`（人在终端） | — | 只更新被改键 |

- **trace**：在每个会话首个回合内记一条 `kind=config`、`name=protected_config_changed`、attrs 只含键名列表（回合内才有 session ID，避免被丢弃）；比较结果在进程内缓存，同一进程多个会话不重复读快照。
- 快照写入失败只记 Warn，不影响会话（GR109 的 best-effort 例外，理由：提示是辅助观测），下次启动仍会提示。

## 架构

代码组织参照 suga 现有分层（命令在 `internal/cli`、配置在 `internal/config`、装配在 `internal/wiring`，SR2004）：
- `internal/config/settings_registry.go`：`ConfigKey.Protected` 与静态、动态描述符标记；
- `internal/config` 新文件（如 `protected_snapshot.go`）：原始值摘要、快照路径、比较、整体推进与单键更新（持配置锁）；
- `internal/cli/config.go`：set/unset 共用的 protected 检查与单键更新；config 命令接收 stdin（SR2003 的命令结构不变）；
- `internal/wiring`：装配时调用 config 的比较函数，把键名列表交给各入口，自身不含判断逻辑（GR108）；
- 输出：chat 横幅 `internal/chat/layout.go`（`BannerInfo`）、run 的 stderr、trace `internal/personal/observe.go`；文案 `internal/i18n/catalog`。

## 关键流程

```text
suga config set|unset <key> …
  → MatchConfigKey → 秘密键 / 只读键拒绝（现状）
  → Protected ?
       ├─ 否 → 写入
       └─ 是 → SUGA_SESSION_ID 非空 或 stdin 非终端 → 拒绝（退出码 2，i18n 提示）
                 否则 → 写入 → 快照中只更新该键

suga 启动（chat / run / acp / SDK）
  → 读配置 → protected 键原始值摘要 → 持锁读快照比较
       ├─ 无快照 → 建快照
       ├─ 相同 → 无事
       └─ 不同 → chat：横幅显示 → 整体推进快照
                 run：stderr 一行；ACP/SDK：无
                 各入口：会话首个回合记 trace
```

## 安全、数据、运维与回滚

- **SR1301**：鉴权——protected 键的 CLI 修改只接受 suga 之外的终端调用；注入防护——不拼接命令；秘密——快照只存摘要、提示只含键名，`api_key` 等秘密键仍不可经 config set 写入；入参校验——沿用现有校验；资源归属——不适用。
- **风险与限制**：
  - 刻意绕过 CLI 判定、直接改文件、`init --force`、`data restore` 都不被阻止，只在你下次打开 chat 时提示；
  - 快照可被同时改写或删除（删除后按首次运行处理），从而掩盖改动，与直接改文件同级；
  - 经 `.env` 或环境变量间接改变 `${VAR}` 引用的值不被比较（D10）；
  - 缩短 `gc.retention.*` 可能提前清掉 trace；
  - trust 档下外部来源检查被放开，这时只剩 CLI 判定与启动提示；其他档位下外部来源裁决对照抄外部内容的此类命令裁决，是另一道防线。
- **兼容与误拒**：以下人的操作改 protected 键会被拒，提示改在 suga 之外的终端执行或直接编辑文件：chat 里 `!` 执行、hook 脚本、gofer exec 任务里的脚本、Git Bash/mintty（stdin 为管道）、`echo … | suga config set`。非 protected 键不受影响。
- **替代声明**：替代 `docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md` F14/S12 的“`tools.origin_judge.*`、`tools.read_deny*` 不可写”，改为 protected；同步注册表注释（`:227`、`:250`）、`docs/usage/gofer-personal-agent.md:125`。
- **受影响的现有测试**（改为“会话内拒绝、终端可写”或改用非 protected 键）：`internal/cli/config_origin_judge_test.go`、`config_read_deny_test.go`、`config_test.go:132-160` 与 `:175-204`、`approval_flags_test.go:133`；计划阶段再 grep 一遍 set/unset protected 键的测试与 `scripts/` 调用。
- **文档**：README 与 `docs/usage/gofer-personal-agent.md` 写明 protected 键、修改方式、启动提示与限制。
- **数据**：新文件 `SUGA_HOME/run/protected-config*.json`（键名 → 摘要）；无表结构变化。
- **回滚**：revert；删除快照文件即恢复“首次运行”。
- **验证要求**（计划中落实）：
  - GR110：经 `internal/cli` 真实命令分发——会话内（`SUGA_SESSION_ID` 非空）与非终端的 set/unset protected 键被拒、退出码 2、配置文件未变；`SUGA_SESSION_ID` 为空字符串时按无标记处理；非 protected 键照常；注入“是终端”判定时 protected 键可写且快照只更新该键；
  - 显式分类表测试（含动态描述符），并做反向验证；
  - 快照：首建、无变化、变化后 run 不推进而 chat 推进、人工 set 不吸收其他键的改动、损坏时全部提示、`SUGA_CONFIG` 分别建快照、并发启动不损坏文件；
  - chat 横幅用 `test/ptye2e` + `suga inspect trace --json` 断言并反向验证；ACP、SDK 的 trace 事件测试；run 的 stderr 行；
  - 主机 Windows：ptye2e ConPTY 下真实二进制在终端里 set protected 键成功、管道输入被拒。

## 决策

- **D1** protected 键由注册表字段统一标记，显式分类表测试防漏标。
- **D2** CLI 限制只加在 `suga config set/unset`；其他改动方式由启动提示兜底。
- **D3**（Q1）判定：`SUGA_SESSION_ID` 非空或 stdin 不是终端时，拒绝修改 protected 键。
- **D4**（Q2）执行类与放宽类全部列为 protected，含 `tools.session_message.*`、`tools.shell_limits.*`、`tools.web_fetch.allow_private`。
- **D5**（Q3）`tools.origin_judge.*`、`tools.read_deny*` 改为 `Writable:true` + `Protected:true`，替代前序“不可写”决定。
- **D6**（Q4）清单增端点类（`providers.<id>.base_url/type`、模型级 `base_url/type`、`models.judge`，及其回退的 `models.compact`/`compact_model`）、`workspace`、`memory.*.backend`、执行上限类、`log.file`、`tools.disabled`、`rules.file`；显式分类表覆盖全部可写键。
- **D7**（Q5）CLI 判定如实定位为“挡直接调用、挡不住刻意绕过”；接受“兼容与误拒”中列出的误拒。
- **D8**（Q6）启动时变化提示：逐键摘要快照，只报键名，不阻断。
- **D9**（Q7）快照只在 chat 横幅展示后整体推进；run、ACP、SDK 只提示不推进；人工 set/unset 只更新被改键。
- **D10**（Q8）按 `config.yaml` 原始值比较，不比较 `${VAR}` 展开值，限制写入文档。

用户选项记录（均选推荐项）：
- Q4 清单：推荐补端点类（`providers.<id>.base_url/type`、模型级 `base_url`）、`workspace`、`models.judge`、`memory.*.backend`，防漏标测试改为覆盖全部可写键的显式分类表；未提出其他选项。
- Q5 强度：推荐如实写明只挡直接调用、挡不住刻意绕过，接受 chat `!`、gofer exec 脚本、Git Bash/mintty 的误拒；未提出其他选项。
- Q6 兜底：推荐加回原 C 档（启动时检测受保护键变化并提示，不阻止启动）；另一选项为维持 0.2 不做。
- Q7 快照推进：A（推荐）chat 横幅展示后推进；B 新增确认命令后推进；C 去掉启动提示。
- Q8 比较依据：A（推荐）原始值；B 另外比较 `SUGA_HOME/.env` 中被引用变量的摘要。

## 待确认事项

无。Q1–Q3（2026-11-11：“Q1 选 A，Q2、Q3 按推荐”）、Q4–Q6（2026-11-11：“Q4–Q6 按推荐”）、Q7–Q8（2026-11-12：“Q7、Q8 按推荐”）已由用户选定，见 D3–D10。

## 结论与人工计划 Gate

本设计经两轴评审并由你批准后才写计划；批准不授权实施。
