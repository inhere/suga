# R1 评审：Spec/Executability 轴

- 候选四元组：git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `5095088921667f6c7f9f109c48510e5b395eacad`；subject `docs/design/2026-11-11-protected-config-keys-design.md`；revision 0.2
- 评审者：spec 轴独立子 agent（第 1 轮，全量读，REPORT_ONLY）
- issue：suga-hyj7（已读）
- 用户已选 D3–D5，本报告不评选择本身，只评落地可行性与事实准确性
- 实际读取范围：设计全文（提交内版本）；`internal/config/load.go`、`settings_registry.go`（第 36–360 行，含 provider 动态描述符、MatchConfigKey）、`settings_registry_source.go`、`paths.go`、`rules.go`、`log.go`；`internal/cli/config.go`（全文）、`root.go`、`cwdcheck.go`、`init.go`、`backup_restore.go`；`internal/personal/backup/backup.go`（config.yaml 部分）；`internal/wiring/sessionenv.go`、`runtime.go`（OpenCLI）、`capabilities.go`（readRoots）、`approval_policy.go`（Workspace/ClampAllow 引用）、`memory.go`（片段）；`pkg/agentcore/tools/native/code_exec.go`（shell_exec 启动、环境变量）、`local_command.go`、`pkg/agentcore/internal/proctree/proctree_unix.go`；`internal/chat/local_cmd.go`；`internal/acpserver/config_options.go`（grep）；`hooks.go`（grep）

## 结论：不通过（有 1 个 CORE_BLOCKING）

## 事实核对

| 设计中的说法 | 结果 | 证据 |
|---|---|---|
| 配置只有一层：SUGA_HOME/SUGA_CONFIG 的 config.yaml，加环境变量 | 属实（另外还读 SUGA_HOME/.env） | load.go:33-64 |
| set/unset 只拒绝秘密键和 Writable:false 的键 | 属实；set 和 unset 都先调 `writableConfigKey` | config.go:67、:99、:154-169 |
| 只有一个写入口 | 属实，在 `suga config` 范围内是唯一写入口；`WriteConfigValue/UnsetConfigValue` 只在 config.go:191/193 被调用；ACP set_config_option 只切换会话模型 | grep |
| 注册表写法 | 键是**精确匹配**的（MatchConfigKey 第 371-375 行），没有通配；设计里的 `tools.approval_rules.*`、`tools.shell_limits.*`、`tools.session_message.*`、`tools.read_deny*` 只是简写，分别对应 4/4/3/2 个具体键；`mcp.servers` 是单个 YAML 键，没有子键 | settings_registry.go:203-207,238-241,252-253,287-290,192 |
| 列出的键都存在且 Writable:true | 属实（origin_judge、read_deny 两组例外，现为不可写，D5 处理） | settings_registry.go:200-290 |
| SUGA_SESSION_ID 导出位置 | 属实；shell_exec、code_exec、watch、chat 的 `!` 本地命令、hook 都带它；嵌入 SDK 时设 `NoSessionEnv` 则全都不带 | code_exec.go:285,481-485,563；local_command.go:124；sessionenv.go:11-20,56-58；pkg/sugasdk/types.go:78-83 |
| shell_exec 子进程 stdin 不是终端 | 字面属实（stdin 为 nil，即空设备），但**不能当作防线**，见 F2 | code_exec.go:287；proctree_unix.go:25 只设了 `Setpgid`，没有 `Setsid`，子进程仍保留控制终端 |
| `term.IsTerminal` 用法 | 属实（`isTerminalInput`）；但 `newConfigCommand` 目前拿不到 stdin，需要从 root.go:69 传进去 | cwdcheck.go:212-214；root.go:69 |

## 发现

### F1 [High / SECURITY / CORE_BLOCKING] 清单漏了同一入口下会直接破坏核心目标的可写键

- 漏掉的键（同类已穷尽）：
  - `workspace`（settings_registry.go:114）：它是文件工具和审批策略的根目录（paths.go:51-62 → runtime.go:243、capabilities.go:308、approval_policy.go:147/220）。执行 `suga config set workspace /` 后，之后所有 chat/run 会话都把整个文件系统当作工作区，范围比已列入的 `tools.read_roots` 还大。
  - `providers.<id>.base_url`、`providers.<id>.type`、`providers.<id>.models.<name>.base_url`（settings_registry.go:311-312,331，在 `providerKeyDescriptors` 和模型描述符的 map 里）：可以把已有 provider 指向攻击者的端点，或者新建一个 provider。后果有两个：一是请求带着 API key 发给对方；二是之后会话的模型回复由对方控制，在 run 档下原生工具默认放行，这和 `hooks.command` 一样是持久的命令执行。
- 必要性证明：
  - core_acceptance：设计的核心目标是“模型不能通过修改 suga 配置放宽安全检查，或获得持久的命令执行”，入口限定为 `config set/unset`。
  - necessary_causal_chain：模型执行 `suga config set workspace /`（或改 base_url）→ 这些键不是 protected，照常写入 → 下次启动时生效 → 写入范围扩大，或者模型被攻击者控制。设计选定的那个入口上，目标没有达到。
  - minimal_owner：`internal/config/settings_registry.go` 的清单。另外“架构”一节要写明 `Protected` 字段也要放进 provider/model 动态描述符（它们不在 `configKeyDescriptors` 里）。
  - deletion_alternative：不补这些键，就得把核心目标改成不包括这两类，这和“放宽读写范围”的定义自相矛盾。
  - scope_effect：只增加清单条目，不引入新机制，不扩大范围。

### F2 [Medium / CORE_CORRECTIVE] “绕过 1 仍会被拒绝”这句不成立

- Unix 下 `env -u SUGA_SESSION_ID suga config set hooks.command x </dev/tty` 就能同时绕过两项检查：chat 在终端里运行时，shell_exec 子进程只调了 setpgid，仍有控制终端。
- 在 gofer/ACP 这类没有终端的场景，`script -qc '…' /dev/null`、`unbuffer`、Python 的 `pty.spawn` 也能做到。
- 嵌入 SDK 时（NoSessionEnv），只剩 stdin 一项检查。
- 改法：
  - 在“风险”一节如实写明：这项检查挡的是直接调用，挡不住刻意绕过，和“直接改文件”属于同一类已接受的风险；
  - 外部来源裁决仍是另一道防线。
  - 可选加强（DEFERRED）：同时要求 stdout/stderr 也是 TTY。

### F3 [Medium / CORE_CORRECTIVE] 人在 chat 里用 `!` 执行会被误拒，设计没有写

- `!` 本地命令和 shell_exec 用的是同一套环境（会带 SUGA_SESSION_ID），stdin 也是 nil（local_command.go:122-127）。
- 所以人在 chat 里输入 `!suga config set tools.read_roots …` 会被拒绝，这和“人在终端里照常可用”的说法冲突。
- 改法：在兼容一节写明这种情况，并在提示里建议退出 chat 后再执行，或直接编辑文件。
- 同样会被拒的还有：hook 脚本、gofer `-a exec` 任务、Windows 的 mintty/Git Bash（在那里 `term.IsTerminal` 返回 false）、`echo y |` 管道。这些都属于“脚本被拒”，应当在文档里列出来。

### F4 [Medium / CORE_CORRECTIVE] “防漏标测试”写不成确定的判定

- “键名命中执行类与放宽类模式”没有定义具体模式。按名字匹配也抓不到 `workspace`、`base_url` 这类键，F1 就是证据。
- 改为确定的判定：在测试里放一张显式的分类表，覆盖全部 `Writable:true` 的键，包括 provider/model 动态描述符；每个键标为 protected，或者标为不需要保护并写上理由。
  - 注册表里出现表里没有的键 → 失败；
  - 分类和 `Protected` 字段不一致 → 失败。
- 同时把设计里的简写（F 表格中的 `.*`）展开成具体键名。

### F5 [Low / CORE_CORRECTIVE] 拒绝行为写得不够具体

- 没有写退出码。现有输入类错误用的是 `configInputError` 返回 2（config.go:282-284），需要指定沿用 2 还是用新的码。
- 提示文字要按现有方式走 i18n key（cli.config.*），不要硬编码中文。
- 应该写明 stdin 从 root.go 传给 `newConfigCommand`，测试用注入的 reader。
- 建议把检查直接放进 `writableConfigKey`（或者 set 和 unset 共用的一个函数），保证两条路径一致。
- D5 会让 origin_judge/read_deny 现有的“read-only”测试和注释（settings_registry.go:226-229,249-251）随之变化，计划里要列出来。

### F6 [Low / CORE_CORRECTIVE] 待定键：逐一写明分类和理由

- 建议标为 protected：
  - `models.judge`：它决定外部来源裁决用哪个模型，设计把这项裁决当作另一道防线；没设置时会回落到 `models.compact`，再回落到主模型；
  - `tools.disabled`：unset 后，用户禁用的工具就恢复了；
  - `tools.shell_timeout_max`、`tools.task_timeout_max`、`tasks.max_*`：和 `shell_limits` 是同一类资源上限；
  - `log.file`：可以向任意路径追加内容；
  - `memory.*.backend`：可以把已配置的 exec 命令启用。
- 可以不保护：`rules.file`（只接受不带路径的文件名，rules.go:30-32）、`tools.preferred`、`skills.always_load`、`agents.*`、`network.proxy.sites`。
- 设计里已列入的键，没有发现误列的无害键。

### F7 [Low / OUT_OF_SCOPE，记入非目标] 其他会改写 config.yaml 的 CLI

- `suga data restore` 会用归档里的 config.yaml 整个替换现有文件（backup.go:152-156,432）。归档只做内部 sha256 自洽校验，所以模型可以自己构造归档。
- `suga init --force` 会把配置重置成模板。
- 这两条和“直接改文件”属于同一类，设计的范围冻结排除了它们。建议在“非目标/风险”里点名，在文档里写清楚。

## 判定

存在 CORE_BLOCKING（F1），本轮不通过。只要补齐 F1，再修正 F2–F5，可以进入下一轮。
