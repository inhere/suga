<!-- template_id: plan; template_version: 1.3.1 -->
# suga 安全配置键保护 实施计划

> 状态：Draft 0.4 / 已批准（2026-11-12）；issue `suga-hyj7`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-12 | Claude | 初稿：按已批准设计 0.8 拆为 T1–T7 |
| 0.2 | 2026-11-12 | Claude | 处理第 1 轮两轴评审（按类自查：验证命令能否真跑、去重与缓存作用域、接口合同、验证要求落任务、环境与主机动作）：ptye2e 命令加 `-tags ptye2e` 并补真实二进制与 Windows 用例；进程级缓存与会话去重；写全签名、推进条件、`LoadedConfigBytes` 合同、文案键；T2 测试基线隔离；新代码放新文件；补字面键盘点、host 动作与 acpprobe |
| 0.3 | 2026-11-12 | Claude | 处理第 2 轮两轴评审纠正项（按类自查：包归属、真实验证能否触发、文案调用、空情况、ptye2e 写法）：缓存、去重与推进判断移到 `internal/config`，wiring 只接线；acpprobe 改为验证首次运行零条并披露去重未经真实验证；文案按 `cliText` 与节前缀；回调无变化时 `ok=false`；ptye2e 用 `ToolsExtra` 制造改动并先跑一回合；补锁路径与 `Reload` 语义 |
| 0.4 | 2026-11-12 | Claude | 未复核修订（第 3 轮纠正确认轮之后，处理 PS24–PS27、PF21–PF24）：`ProtectedState` 自带锁并补 `-race` 并发用例；acpprobe 改为主机 gofer job 并写全构建与检查命令；修正 T3 回归测试名；分隔符走 catalog 键、文案函数放 config 供 chat 与 run 共用、`Path` 为原始绝对路径；trace attrs 由 personal 组装；T4 文件清单同步 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

第 1 轮评审处理（`docs/review/2026-11-12-protected-config-keys-plan/`）：

| 评审项 | 处理 |
|---|---|
| PS1、PF1（阻断） | ptye2e 命令一律 `-tags ptye2e`；T5 用直接构造的 `gopty.Spec` 跑真实二进制（终端 set 成功、管道输入被拒）；T7 Windows 显式跑带 tag 的用例 |
| PS2、PF2（阻断） | 比较结果与会话去重放进 `internal/wiring` 的进程级缓存（键为规范化配置路径）；写定交给 personal 的回调签名与记录点 |
| PS3、PF8 | 设计验证要求逐条落到 T2/T3/T4/T5，CLI 分发层的快照断言放 T3 之后的 T3b |
| PS4 | 字面键盘点作为 T1 的第一步并记录结果 |
| PS5 | 可追溯表补 D2、D9、替代声明 |
| PS6、PF3、PF4、PF5 | 写全签名、推进条件、`LoadedConfigBytes` 合同、文件定位、`EventConfig` 命名 |
| PS7 | 文案键、`{name}` 占位、zh/en 与整文件/快照不可读文案 |
| PS8 | host 动作补 worktree 建立与合并；staticcheck 固定 2026.2.1 |
| PS9 | 任务内只跑 focused 命令；包级与全量放 T7 |
| PS10、PF12 | baseline 写明 HEAD；登记并行的多命令 hook 设计与落地顺序 |
| PS11 | T7 加 acpprobe 真实验证 |
| PS12、PF6 | 测试统一清空 `SUGA_SESSION_ID`；`setupConfigCLI` 默认注入“是终端”；负向测试改用非 protected 键或注入终端 |
| PS13 | 新代码放新文件 |
| PS14、PS15、PF9、PF10 | 统一注入方式；修正测试名；动态描述符四张表；锁名两侧同一规范化；T6 grep 补英文表述 |
| PF7 | `scripts/ptysmoke/chat_onboarding.py` 改用非 protected 键 |
| PF11 | 子 agent 会话不记 trace |
| PS16 | 任务书写明测试写法约定（DEFERRED，直接纳入） |
| 第 2 轮 PS17、PF15 | acpprobe 每次清空 SUGA_HOME，改为验证首次运行 `kind=config` 恰为 0 条；有变化时的去重由 T4 两次 `Open` 用例证明，真实环境标 `UNVERIFIED_REAL_ENV`；写全命令参数 |
| 第 2 轮 PS18 | host 声明补 acpprobe 用真实凭据调用 provider |
| 第 2 轮 PS19 | 进程级缓存、会话去重、推进条件移到 `internal/config/protected_state.go`；wiring 只调用与接线（GR108） |
| 第 2 轮 PS20、PS21 | 范围补 `pkg/sugasdk`（测试）；可追溯表补文档与脚本行、写测试名 |
| 第 2 轮 PS22、PF13、PF16 | 文案用 `cliText(tr, "cli.config.protected_refused", …)` 与 `status.protected.*`；`{keys}` 用“、”/“, ”连接；补英文；`{path}` 来自 `LoadedConfigBytes`；横幅数据经 `rendererOptions`；单元测试断言 `err.Error()` |
| 第 2 轮 PF14 | 回调在无变化或比较失败时返回 `ok=false`；失败结果按“无变化”缓存；补“无变化零条”用例 |
| 第 2 轮 PF17 | 回调为 `personal.Config.ProtectedNotice`；子 runner 在 `subagent.go:189` 单独构造，不设回调 |
| 第 2 轮 PS23、PF18、PF19 | 新增 `TestProtectedPathNormalize`；cli 回归跑 `TestConfigCommands|TestConfigPath`；`Reload`（= Reset + Load）刷新 `LoadedConfigBytes`；补 `ResolveSugaHome` 出错分支 |
| 第 2 轮 PF20 | ptye2e 用 `ToolsExtra` 制造改动、第二次 chat 先跑一回合；终端用例自写 `config.yaml` 与空 `.env`；T6 grep 补 unset、不排除 ptye2e |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：落实设计 0.8——模型不能经 `suga config set/unset` 修改 protected 键；其他方式改动 protected 键时，你下次打开 chat 能看到改动的键名。
- scope freeze：设计 0.8 的 §1–§3 与验证要求；不含多命令 hook（`suga-5xmf`）、发版与推送。
- 扩围策略：`DEFER_OR_REQUEST`；review budget：最多 8 轮；停止条件：无开放 `CORE_BLOCKING`。
- 暴露度（SR1409）：高（已发布 `v0.55.0`）；两轴独立评审与 ledger，评审目录 `docs/review/2026-11-12-protected-config-keys-plan/`。

## 目标与完成定义

完成定义（全部满足）：
1. 注册表每个键都在显式分类表中，protected 标记与表一致；`hooks.pre_tool_use.tools/name` 有不可写描述符；`tools.origin_judge.*`、`tools.read_deny*` 为可写 + protected。
2. `suga config set/unset` 改 protected 键：`SUGA_SESSION_ID` 非空或 stdin 非终端时退出码 2、配置文件不变、i18n 提示；终端里照常写入并只更新快照中该键。
3. 启动比较：chat 横幅列出改动键名并推进快照；run 在 stderr 一行；ACP、SDK 每个顶层会话记一条 `kind=config` trace；compact 不比较。设计 §3 的键集、整文件模式、规范化、快照模型、锁与路径规则全部实现。
4. 设计“验证要求”全部有对应测试且通过，含反向验证、真实二进制 ptye2e（Linux 与 Windows ConPTY）与 acpprobe。
5. README、`docs/usage/gofer-personal-agent.md`、CHANGELOG「未发布」已更新。

## 范围、排除项与授权

- 范围：suga 仓库 `internal/config`、`internal/cli`、`internal/wiring`、`internal/chat`、`internal/personal`、`internal/personal/store/types.go`、`internal/i18n/catalog/{zh,en}`、`pkg/sugasdk`（只加测试）、`test/ptye2e`、`scripts/ptysmoke/chat_onboarding.py`、文档与 CHANGELOG。
- 排除：多命令 hook；`.env` 与环境变量比较（D10）；发版、推送、安装、通知 gofer。
- 授权：本计划批准只授权进入实施请求；实施需你另行指示。本地提交为默认授权；push、tag、发版、安装需另行授权。
- `host_or_non_offline_action=REQUIRED`：主机上建 worktree（`tmp/xhen-p1/wt-repo.ps1`）、合并到本地 main（`tmp/xhen-p1/host-merge.ps1`）、Windows 测试；`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1` 会联网下载模块；T7 的 acpprobe 用真实 `config.yaml`/`.env` 中的凭据调用模型 provider（联网、产生少量费用）。均不推送、不发布。

## 输入与批准证据

- 设计：`docs/design/2026-11-11-protected-config-keys-design.md` Draft 0.8，提交 `d416ed9b` 记录批准。
- 批准原话（2026-11-12）：“批准 suga 安全配置键设计 0.8（接受未复核修订与评审补入的键），开始写计划；hooks 并行出设计”。
- 设计评审：`docs/review/2026-11-11-protected-config-keys/`（6 轮，ledger PASS）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 键分类与 protected 标记 | `internal/config/settings_registry.go` ConfigKey 与四张动态描述符表 | 是 | 原位加字段 | DIRECT_REUSE | none | 无 |
| CAP-02 | 配置 YAML 逐键读取、锁、原子写 | `internal/config/yaml_edit.go`（goccy AST、`configLockPath`、`editConfigFile`）、`bootstrap_lock.go` | 是 | 原位扩展（写后回调、路径规范化） | DIRECT_REUSE | none | 锁文件名变化见风险 |
| CAP-03 | 终端判定 | `internal/cli/cwdcheck.go` `isTerminalInput` | 是 | 包级可替换变量（测试替换） | DIRECT_REUSE | none | 无 |
| CAP-04 | 横幅、stderr、trace 输出 | `internal/chat/render.go` 横幅、`internal/personal/observe.go` recorder、`store` Event 常量 | 是 | 原位扩展 | DIRECT_REUSE | none | 无 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| NONE | none | none |

（新文件 `internal/config/protected_snapshot.go`、`internal/config/protected_state.go`、`internal/wiring/protected.go`、`internal/personal/protected_notice.go`、`internal/chat/protected_banner.go` 都在既有包内，属 owner 扩展，不是新 Module；放新文件是因为 `runtime.go` 951 行、`runner.go` 938 行、`chat.go` 899 行接近 1000 行上限。）

## 前置检查与 fail-closed 条件

- workspace baseline（2026-11-12）：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；branch `main`；计划 0.2 写于 HEAD `97e26d11`；`git status --short` 干净；本地领先 origin（nf95/xrxl/xsvv 与设计、评审、tracker 提交），未推送。实施开始时重新记录 HEAD 与 status。
- 并行工作：多命令 hook 设计 `docs/design/2026-11-12-multi-command-hooks-design.md`（`suga-5xmf`，提交 `b11cd717`，设计评审中）会新增 `hooks.commands`（protected），与本计划共用 `settings_registry.go` 与 T1 的分类表测试。落地顺序：本计划先合入；`suga-5xmf` 实施时在分类表中加一行并标 `Protected`。若顺序反转，由后合入者补分类表一行。
- 实施在主机建的 worktree：`gofer job run -p my-tools-dev -a exec --runner local --sync -- pwsh -File tmp/xhen-p1/wt-repo.ps1 -Repo inhere-tools/suga -Name hyj7 -Branch feat/protected-config-keys`（`.worktrees/hyj7`）。容器内不 `git worktree add/prune`；对 worktree 的 git 命令只用单条前缀 `GIT_DIR`/`GIT_WORK_TREE`，不 export，不与测试同一命令。
- 测试隔离：所有测试与 ptye2e 用临时 `SUGA_HOME`，并 `t.Setenv("SUGA_SESSION_ID", "")`（在 suga 会话里跑测试时宿主会带这个变量）；不读写真实 `~/.suga`、`~/.xhen`。
- 测试写法：`github.com/gookit/goutil/x/assert`，同一方法多用例用 `t.Run`（SR2002、AGENTS.md）。
- fail-closed：任一任务的 focused 测试或反向验证不符合预期即停止，按实施发现四类记录；遇到其他人的改动（Ownership Conflict）停止。
- 依赖：不新增第三方依赖；goccy YAML v1.19.2（含 `MergeKeyType`）；`gkconfig.LoadSources` 已存在。

## 波次与依赖

| 波次 | 任务 | 说明 |
|---|---|---|
| W1 | T1 | 字面键盘点、注册表与分类表 |
| W2 | T2 → T3 → T3b | CLI 拒绝；快照模块与 load/edit 改动；CLI 层快照断言 |
| W3 | T4 | 装配与各入口输出 |
| W4 | T5、T6 | ptye2e；文档与 ptysmoke 脚本 |
| W5 | T7 | 全量、staticcheck、smoke、acpprobe、主机 Windows、合并 |

执行者：T1–T6 由 sonnet 子 agent 在 `.worktrees/hyj7` 顺序实施（每个任务一个提交）；我审查、复核反向验证，执行 T7 与合并。

## 任务

### T1 字面键盘点、注册表 `Protected` 与显式分类表

- 文件: `internal/config/settings_registry.go`；新测试 `internal/config/settings_protected_test.go`；`docs/usage/gofer-personal-agent.md:124-125` 的旧表述留给 T6。
- 动作:
  - 盘点：`grep -rn 'gkconfig\.' --include=*.go internal pkg | grep -v _test` 与拼接键名的读取器（`stringList`、`chatSwitch`、`mcpAllowlist`、`toolNameList`、`memory.go` 的 `key+"command"` 等），列出未登记的键；结果写进提交说明。已知未登记：`hooks.pre_tool_use.tools/name`。若发现其他符合设计 protected 原则的键，停止并按 Semantic Amendment 处理。
  - `ConfigKey` 增 `Protected bool`；按设计 §1 表给静态描述符与四张动态表（`providerKeyDescriptors`、`providerModelKeyDescriptors`、`providerModelNestedDescriptors`、`providerNestedDescriptors`）标记。
  - 新增描述符 `hooks.pre_tool_use.tools`（StringList）、`hooks.pre_tool_use.name`（String），`Writable:false, Protected:true`。
  - `network.proxy.url`、`tools.web_fetch.proxy` 加 `Protected:true`（仍 `Sensitive`、不可写）。
  - `tools.origin_judge.enabled/timeout`、`tools.read_deny`、`tools.read_deny_defaults` 改 `Writable:true, Protected:true`，更新 `:227`、`:250` 处注释。
  - 导出 `func ProtectedConfigKeys() []ConfigKey`：返回静态与动态描述符中 `Protected` 的全部（动态的 Pattern 含 `<id>`/`<name>`），按 Pattern 排序。
  - 测试 `TestProtectedClassification`：显式表 `map[string]string{pattern: group}`，group ∈ {`exec`,`relax`,`exec_limit`,`endpoint`,`judge`,`other_protected`,`cost`,`retention`,`model_choice`,`runtime`,`tool_pref`,`log`,`display`,`provider_tuning`,`secret`}；遍历全部静态与四张动态表：未在表中 → 失败；protected 组（前六个）与 `Protected` 字段不一致 → 失败。
- 验证: worktree 根运行 `go test ./internal/config -run 'TestProtectedClassification' -count=1`；反向验证：临时去掉 `tools.disabled` 的 `Protected`（可编译），确认失败且输出不含 `[build failed]`，恢复。
- 完成标准: 测试通过；盘点结果与反向验证写在提交说明。
- 依赖: 无。

### T2 `suga config set/unset` 拒绝 protected 键

- 文件: `internal/cli/config.go`、`internal/cli/root.go`（`newConfigCommand` 调用处）、`internal/i18n/catalog/{zh,en}/cli.ini`；新测试 `internal/cli/config_protected_test.go`；修改 `config_test.go`（`setupConfigCLI`）、`config_origin_judge_test.go`、`config_read_deny_test.go`、`approval_flags_test.go`。
- 动作:
  - `newConfigCommand(stdin io.Reader, stdout, stderr io.Writer)`；`configLeafHandler` 签名改为 `func(in io.Reader, out io.Writer, c *capp.Cmd) error`，`newConfigLeaf` 透传 stdin；root 传入 stdin。
  - 包级变量 `var configStdinIsTerminal = isTerminalInput`（只供测试替换）。
  - `func protectedWriteAllowed(key appconfig.ConfigKey, path string, stdin io.Reader) error`：`key.Protected` 且（`strings.TrimSpace(os.Getenv("SUGA_SESSION_ID")) != ""` 或 `!configStdinIsTerminal(stdin)`）→ `configInputError(cliText(tr, "cli.config.protected_refused", map[string]any{"key": key.Pattern, "path": path}))`（退出码 2；`tr` 沿用 config 命令现有的 translator 获取方式）；set 与 unset 在 `writableConfigKey` 之后、写入之前调用。
  - 文案键（`cli.ini` 的 `[cli]` 节）`config.protected_refused`：zh “安全相关配置 {key} 只能由你在 suga 之外的终端里修改，或直接编辑 {path}”；en “{key} is a protected setting: change it from a terminal outside suga, or edit {path} directly”。
  - 测试基线：`setupConfigCLI` 设 `SUGA_SESSION_ID=""` 并把 `configStdinIsTerminal` 替换为恒真（`t.Cleanup` 恢复）；原负向测试（`TestConfigRefusesTrustTier`、invalid yaml list 子测试）在“是终端”下仍测原来的拒绝原因。`TestConfigSetRefusesOriginJudgeKeys`、`TestConfigSetRefusesReadDenyKeys` 改为“会话内拒绝、终端可写”。
- 验证: worktree 根运行 `go test ./internal/cli -run 'TestConfigProtected|TestConfigSet|TestConfigCommands|TestConfigRefuses|TestRunApproveFlags' -count=1`。`TestConfigProtected` 经 root 命令真实分发（GR110）：`SUGA_SESSION_ID=x` 下 set/unset protected 键 → 退出码 2、文件字节不变、`err.Error()` 含键名；终端判定为假 → 同上；`SUGA_SESSION_ID=""` 且终端为真 → 写入成功；非 protected 键在终端为假时照常写入；`hooks.pre_tool_use.tools` set → 只读拒绝、退出码 2。反向验证：临时让 `protectedWriteAllowed` 返回 nil，确认拒绝用例失败，恢复。
- 完成标准: 上述测试通过；反向验证写在提交说明。
- 依赖: T1。

### T3 快照模块、读取同源与单键更新

- 文件: 新 `internal/config/protected_snapshot.go`、`protected_snapshot_test.go`；`internal/config/load.go`；`internal/config/yaml_edit.go`；`internal/config/settings_registry_source.go`（`configFileName`）。
- 动作（签名为合同）:
  - `type ProtectedDigest struct { Mode string /* "keys"|"file" */; Keys map[string]string; File string }`
  - `func DigestProtected(data []byte) (ProtectedDigest, error)`：goccy 解析；顶层字面量点号键、锚点/别名、合并键（含内联 `<<: {…}`）、多文档任一出现 → `Mode="file"`；否则 `Mode="keys"`。逐键摘要取未展开 `${VAR}` 的解析后值，整块值转 JSON（映射键排序、列表原序）后 SHA-256；键缺失不入 map；动态键从 `providers` 映射枚举具体 `<id>`、`models.<name>`；空文件或只有注释 → 空 map；`File` 恒为整文件字节的 SHA-256。解析失败返回错误。
  - `type ProtectedChange struct { Keys []string; WholeFile bool; SnapshotUnreadable bool }`；`func (c ProtectedChange) Empty() bool`。
  - `func CompareProtected(configPath string, cur ProtectedDigest) (ProtectedChange, error)`：持该配置的锁读快照。不存在 → 写入 `cur` 全部字段并返回空变化；损坏 → `SnapshotUnreadable=true`、`Keys`=当前键名；`cur.Mode=="file"` 或快照 `mode=="file"` → 比较 `File`（快照缺 `file` 视为变化），变化时 `WholeFile=true`；否则按键名并集比较，缺失一侧按“未设置”，`Keys` 排序。锁获取或快照 I/O 失败 → 返回错误。
  - `func AdvanceProtected(configPath string, cur ProtectedDigest) error`：持锁写入 `cur` 全部字段（`{"version":1,"mode":…,"keys":…,"file":…}`；临时文件 + 改名；目录 0700、文件 0600）。
  - 路径：`func normalizeConfigPath(p string) string` = `filepath.Abs` + `Clean`，Windows 再 `strings.ToLower`；`protectedSnapshotPath(configPath)` = `SUGA_HOME/run/protected-config/default.json`（规范化后等于规范化的 `SUGA_HOME/config.yaml` 时）或 `<sha256(规范化路径)>.json`；`configLockPath` 两侧都用 `normalizeConfigPath` 比较与求哈希（默认仍为 `config.lock`）。`configLockPath` 的三个回退分支（`ResolveSugaHome` 出错、无 `SUGA_HOME`/`SUGA_CONFIG` 的直接库调用、建目录失败）下，快照路径放在配置文件同目录的 `run/protected-config/`，建目录失败则返回错误。
  - 单键更新：`editConfigFile` 在写入并通过读回校验之后、释放锁之前调用 `func updateProtectedKeyLocked(path, key string, data []byte) error`（`data` 为写入后的文件字节）：仅当键为 protected、快照存在、`DigestProtected(data).Mode=="keys"` 且快照 `mode=="keys"` 时改 `keys[key]`（新值缺失则删除该项），不改 `mode`/`file`；返回错误时由 `editConfigFile` 记 Warn（GR109 例外：快照是辅助观测）。校验失败回滚时不调用。
  - `load.go`：在 `Load` 中求配置文件绝对路径（处理相对 `SUGA_CONFIG`，在任何切换目录之前），读取字节一次（不取锁），经 `gkconfig.LoadSources` 加载；保留默认文件不存在按空、`SUGA_CONFIG` 不存在报错、按扩展名取格式。新增 `func LoadedConfigBytes() (path string, data []byte, ok bool)`：`Load` 成功后 `ok=true`，`path` 为绝对路径，默认文件不存在时 `data` 为空切片；未调用 `Load` 时 `ok=false`。`Reload`（= Reset + Load）会重新读取并刷新它。`ResetLoadedStateForTest` 一并清空。`configFileName()` 改为优先用 `LoadedConfigBytes` 的 path（`LoadedFiles()` 在 `LoadSources` 下为空）。
- 验证: worktree 根运行 `go test ./internal/config -run 'TestProtectedSnapshot|TestDigestProtected|TestProtectedPathNormalize|TestLoad|TestWriteConfig|TestUnsetConfig' -count=1 -race`，以及 `go test ./internal/cli -run 'TestConfigCommands|TestConfigSetAllowsSimpleRuntimeKeys' -count=1`（`config_test.go:163` 的锁名仍为 `config.lock`、`config get` 来源不变）。新增 `TestProtectedPathNormalize`：相对路径、`..`、默认配置得 `default.json`、锁名与快照名同源。`TestDigestProtected`/`TestProtectedSnapshot` 覆盖：首建、无变化、变化、单键更新不吸收其他键、新增与删除 provider、注释与缩进不报、顶层点号键/锚点/内联合并键/多文档走整文件且带点模型名（`gpt-4.1`）仍逐键、只有注释与空文件为空 map、快照 `mode=file` 时单键更新不改快照且下次仍报、损坏 → `SnapshotUnreadable`、`SUGA_CONFIG` 相对路径与不同文件分别建快照、秘密键改动只出键名、`hooks.pre_tool_use.tools` 改动被报出、多 goroutine 并发 Compare/Advance 后文件可解析；Windows 大小写用例放 `_windows_test.go`。反向验证：临时把并集改为只取快照键，确认“新增 provider”用例失败，恢复。
- 完成标准: 测试通过且 `-race` 干净；反向验证写在提交说明。
- 依赖: T2。

### T3b CLI 分发层的快照断言

- 文件: `internal/cli/config_protected_test.go`。
- 动作: 在 T2 的分发用例上补：先由 `CompareProtected` 建快照，终端下 `config set` 一个 protected 键后，快照只有该键的摘要变化；无快照时 `config set` 不建快照；整文件模式（顶层点号键）下 `config set` 不改快照。
- 验证: worktree 根运行 `go test ./internal/cli -run 'TestConfigProtected' -count=1`；反向验证：临时去掉 `editConfigFile` 中对 `updateProtectedKeyLocked` 的调用，确认“只更新该键”用例失败，恢复。
- 完成标准: 用例通过；反向验证记录。
- 依赖: T3。

### T4 装配与各入口输出

- 文件: 新 `internal/config/protected_state.go`（与测试）；新 `internal/wiring/protected.go`（与测试）；`internal/wiring/runtime.go`（只加 `Options` 字段与一处调用）；`internal/cli/compact.go`；`internal/cli/chat.go`（chat Options 构造处，约 `:89`）；`internal/cli/run.go`（run 的 stderr 行）；新 `internal/chat/protected_banner.go`；`internal/chat/chat.go`（`Options` 字段、`:412` 映射到 `rendererOptions`、横幅后回调一处）；`internal/chat/render.go`（`rendererOptions` 新字段与横幅组装处，约 `:740`）；`internal/i18n/catalog/{zh,en}/status.ini`（含 `protected.separator`）；新 `internal/personal/protected_notice.go`；`internal/personal/runner.go`（`recordTurnContext` 之前一处调用）；`internal/personal/store/types.go`（`EventConfig = "config"`）；`internal/i18n/catalog/{zh,en}/status.ini`；测试同目录与 `pkg/sugasdk`、`internal/cli` 的 ACP 测试。
- 动作:
  - `internal/config/protected_state.go`（判断逻辑归 config）：进程级缓存 `map[规范化配置路径]*ProtectedState`（`sync.Mutex`）；`type ProtectedState struct{ mu sync.Mutex; Change ProtectedChange; Digest ProtectedDigest; Path string /* 原始绝对路径，用于展示与快照；缓存键另用规范化路径 */; ok bool; recorded map[string]bool }`；`NoticeFor` 与 `Advance` 都在 `mu` 内完成（ACP 每个请求一个 goroutine，同一进程多个会话会并发调用）；`func CheckProtectedOnce() *ProtectedState`：`LoadedConfigBytes()` 的 `ok=false`、`DigestProtected` 或 `CompareProtected` 出错 → 记 Warn，缓存为“无变化、不可推进”；`func (s *ProtectedState) Advance() error`：仅 `ok` 且变化非空时写入启动摘要，否则返回 nil；`func (s *ProtectedState) NoticeFor(sessionID string) (ProtectedChange, bool)`：无变化、不可用或该会话已返回过 → `ok=false`；否则返回变化并记入 `recorded`（检查与标记在同一锁内）；`func ProtectedChangeText(tr i18n.Translator, c ProtectedChange, path string) string`：按变化种类选文案键，`{keys}` 用 catalog 键 `status.protected.separator`（zh “、”，en “, ”，参照 `approval.render.agent.separator`）连接，供 chat 横幅与 run stderr 共用；`func ResetProtectedStateForTest()`。
  - `internal/wiring/protected.go`：只接线——`Open` 中非 Skip 时调 `CheckProtectedOnce()`，在 `runtime.go:722` 构造 `personal.Config` 处设置 `ProtectedNotice`（Skip 时不设），因此 `CheckProtectedOnce` 在其之前调用；把 state 挂到 `Runtime`（`Runtime.ProtectedState() *appconfig.ProtectedState`）。
  - `wiring.Options.SkipProtectedCheck bool`；比较只在 `Open` 中做（`OpenCLI` 经 `Open`），Skip 时不做。`runtime.go` 的改动：`Options` 字段、`Open` 中一处调用、`Runtime` 一个字段、`:722` 一行回调设置；其余放 `protected.go`。
  - personal：`personal.Config` 新字段 `ProtectedNotice func(sessionID string) (keys []string, wholeFile, unreadable bool, ok bool)`（由 wiring 包装 `state.NoticeFor`）；`protected_notice.go` 组装 attrs：`{"keys":[…]}`、`{"whole_file":true}` 或 `{"keys":[…],"snapshot_unreadable":true}`；`ok=false` 时不记事件。`runner.go` 在 `recordTurnContext` 之前调用 `r.recordProtectedNotice(ctx, sessionID)`（新文件），记 `store.Event{Kind: store.EventConfig, Name: "protected_config_changed", Attrs: attrs}`。子 agent 会话不记：子 runner 在 `subagent.go:189` 单独构造，不设该回调。
  - chat：`internal/cli/chat.go` 把 `state.Change`、`state.Path` 与 `OnBannerShown: func(){ if err := state.Advance(); err != nil { log Warn } }` 放进 `chat.Options`；`chat.go` 经 `rendererOptions`（约 `:412`）传给 renderer，`rendererOptions` 新增字段，`render.go` 横幅组装处在变化非空时追加一行（`protected_banner.go` 调 `appconfig.ProtectedChangeText`）；`chat.go` 在 `render.Banner()` 之后调用 `OnBannerShown`。
  - run：`run.go` 在开始执行前，变化非空时向 stderr 写 `appconfig.ProtectedChangeText(...)` 一行，不推进。
  - compact：`compact.go` 传 `SkipProtectedCheck: true`。
  - 文案键（`status.ini` 的 `[status]` 节，按该节现有调用方式取）：`protected.changed` zh “安全相关配置自上次确认以来有变化：{keys}；如果不是你改的，请检查 {path}”，en “Protected settings changed since you last confirmed: {keys}. If you did not change them, check {path}”；`protected.changed_file` zh “配置文件有变化（含无法逐键比较的写法），请检查 {path}”，en “The config file changed (it uses forms that cannot be compared key by key); check {path}”；`protected.unreadable` zh “安全相关配置快照无法读取，以下键视为已改动：{keys}；请检查 {path}”，en “The protected settings snapshot could not be read; treating these as changed: {keys}. Check {path}”。`{keys}` 由调用方拼接：zh 用“、”，en 用“, ”；`{path}` 为 `state.Path`。
- 验证: worktree 根运行 `go test -race ./internal/config ./internal/wiring ./internal/chat ./internal/personal ./internal/cli ./pkg/sugasdk -run 'Protected' -count=1`。用例：无变化时零条 `kind=config` trace 且横幅无提示行；`-race` 下多 goroutine 并发对同一会话与不同会话调用 `NoticeFor`，同一会话只返回一次；run 的 stderr 行且快照不推进；chat 回调推进写入启动摘要（比较后、推进前改动另一个键，下次仍报出）；比较失败或 `ok=false` 时不推进；ACP 同一会话两次 prompt（两次 `Open`）只有一条 `kind=config` trace；SDK 会话有一条；子 agent 会话没有；compact 不建快照；未调用 `Load` 的 wiring 测试不受影响。反向验证：临时去掉 `recordProtectedNotice` 调用，确认 ACP/SDK 用例失败，恢复。
- 完成标准: 测试通过；`runtime.go`、`runner.go`、`chat.go` 净增各不超过 20 行。
- 依赖: T3b。

### T5 ptye2e 真实二进制场景

- 文件: 新 `test/ptye2e/chat_protected_config_test.go`（首行 `//go:build ptye2e`，参照 `chat_observe_test.go`、`chat_history_test.go` 的 `gopty.Spec` 用法）。
- 动作:
  - `TestChatProtectedConfigBanner`：临时 SUGA_HOME + 假模型；首次 chat 建快照、无提示；第二次 `startChatOpts` 以 `ToolsExtra` 加入 `read_roots`（`startChatOpts` 每次重写 `config.yaml`，这次重写即“其他方式的改动”），断言横幅含 `tools.read_roots`，并跑一个回合后退出；`suga inspect trace --json` 有一条 `kind=config` 事件；第三次 chat（同样的 `ToolsExtra`）无提示。
  - `TestConfigSetProtectedInTerminal`：先自写 `config.yaml` 与空 `.env`，再直接构造 `gopty.Spec` 运行 `suga config set tools.read_roots …`（不经 `startChatOpts`），断言成功；同一命令以管道 stdin（`exec.Command`，非 pty）运行，断言退出码 2 与提示。
- 验证: worktree 根运行 `go test -tags ptye2e ./test/ptye2e -run 'TestChatProtectedConfig|TestConfigSetProtectedInTerminal' -count=1 -v`，输出中两个用例为 `--- PASS`（不是 `no tests to run`）。反向验证：临时不追加横幅行，确认 `TestChatProtectedConfigBanner` 失败，恢复。
- 完成标准: 用例通过；反向验证记录。
- 依赖: T4。

### T6 文档与 ptysmoke 脚本

- 文件: `README.md`（配置一节，含 `:133-134` 英文 file-only 表述）、`docs/usage/gofer-personal-agent.md`（含 `:124-125`）、`CHANGELOG.md`「未发布」、`scripts/ptysmoke/chat_onboarding.py:199-206`。
- 动作: 写明 protected 键分组、修改方式、会被拒的场景、启动提示与限制；`tools.origin_judge.*`/`tools.read_deny*` 改为终端可写；`chat_onboarding.py` 的 set/unset 改用非 protected 键（如 `chat.markdown`）并同步断言。
- 验证: worktree 根运行 `grep -n "只能写在配置文件里\|file-only\|only in the config file" README.md docs/usage/gofer-personal-agent.md` 无旧表述；`grep -rn 'config", "set"\|config", "unset"\|config set\|config unset' scripts/ test/` 列出的调用均不涉及 protected 键，或在伪终端中执行。
- 完成标准: 两条 grep 结果符合；CHANGELOG 新条目在「未发布」下。
- 依赖: T4。

### T7 全量验证与合并（我执行）

- 文件: 无代码改动；主机脚本放 `tmp/suga-hyj7/`。
- 动作:
  - worktree 根：`go test -race ./...`、`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...`、`make smoke-go`、`make smoke-chat`。
  - acpprobe（主机 gofer exec job，真实凭据在主机；脚本 `tmp/suga-hyj7/acpprobe.ps1`，路径相对 WP 根）：在 worktree 根 `go build -o <WP>/tmp/suga-hyj7/suga.exe ./cmd/suga`；`python scripts/acpprobe/acp_probe.py --bin <WP>/tmp/suga-hyj7/suga.exe --cwd <WP>/tmp/suga-hyj7/ws --home <WP>/tmp/suga-hyj7/acp-home --config-from $HOME/.suga --prompt "说一句话" --prompt "再说一句"`（脚本每次清空并重建 `--home`，只读真实 `config.yaml`/`.env` 不复制）；随后以 `SUGA_HOME=<WP>/tmp/suga-hyj7/acp-home` 在 `<WP>/tmp/suga-hyj7/ws` 目录下运行 `suga.exe session list --json` 取会话 id，再 `suga.exe inspect trace <id> --json`，确认 `kind=config` 恰为 0 条（首次运行只建快照）、`run/protected-config/default.json` 已建、两个 prompt 正常完成。有变化时的去重在真实 ACP 环境未验证，交付说明标 `UNVERIFIED_REAL_ENV`，由 T4 的两次 `Open` 与并发用例证明。
  - 主机 Windows（gofer exec job，只写 `tmp/`）：在 worktree 跑 `go test ./...` 与 `go test -tags ptye2e ./test/ptye2e -run 'TestChatProtectedConfig|TestConfigSetProtectedInTerminal' -count=1 -v`。
  - 合并：主机 `tmp/xhen-p1/host-merge.ps1 -Repo inhere-tools/suga -Branch feat/protected-config-keys -MsgFile …`，以 `is_ancestor=0` 为收尾前提；CHANGELOG 已发布小节逐字未变。
- 验证: 以上命令全部通过，Windows 输出含两个 `--- PASS`；结果记录在 issue 评论。
- 完成标准: 全绿并合并到本地 main。
- 依赖: T5、T6。

## 回滚与恢复

- 每个任务一个提交，恢复点为各任务提交；合并前在 worktree 分支上，主分支不受影响。
- 合并后回滚：revert 合并提交；用户侧删除 `SUGA_HOME/run/protected-config/` 即回到“首次运行”。
- 锁文件名变化：升级过渡期新旧版本同时写同一 `SUGA_CONFIG` 配置可能拿到不同锁，接受（设计风险）。

## 人工 Gate

1. 计划批准（本稿评审后）。已批准：2026-11-12 用户原话“批准 suga 安全配置键计划 0.4（接受未复核修订），开始实施；实施执行可以用suga oc-go ds4.1”。该原话同时是当前执行请求；执行者由 sonnet 子 agent 改为 `gofer job -a suga --model opencode-go/deepseek-v4.1-flash`（执行者变化不改任务合同），我审查验收。
2. 当前执行请求：你指示开始实施。
3. 外部动作 Gate：push、tag、发版、安装、通知使用方，均需你另行逐项批准；本计划不含。

## 可追溯性

| 设计条目 | 任务 | 验证 |
|---|---|---|
| D1、D4、D5、D6（§1 清单、分类表、字面键盘点） | T1 | `TestProtectedClassification` + 反向验证 |
| D2（CLI 限制只在 set/unset） | T2 | 分发测试 |
| D3、D7（§2 判定与拒绝） | T2、T5 | `TestConfigProtected`、`TestConfigSetProtectedInTerminal` + 反向验证 |
| D8、D10（§3 比较、整文件模式、规范化） | T3 | `TestDigestProtected`、`TestProtectedSnapshot`、`TestProtectedPathNormalize` + 反向验证 |
| D9（推进与单键更新） | T3、T3b、T4 | 单键更新、CLI 层断言、chat 推进与不推进用例 |
| §3 各入口、trace、compact | T4、T5、T7 | T4 的 `Protected` 用例、`TestChatProtectedConfigBanner`、acpprobe（首次零条） |
| 替代声明（origin_judge、read_deny 可写） | T1、T2、T6 | `TestConfigSetRefusesOriginJudgeKeys`、`TestConfigSetRefusesReadDenyKeys` 改写、T6 grep |
| 文档与使用方脚本（README、usage、CHANGELOG、ptysmoke） | T6 | T6 两条 grep、`make smoke-chat` |
| 验证要求（Windows） | T7 | 主机 Windows 测试 |

## 完成 Gate 与剩余工作

- 完成条件：T1–T7 完成标准全部满足、合并到本地 main，issue `suga-hyj7` 评论附验证结果后关闭；plan todo 标 done。
- 进入 `COMPLETE` 时写 ≤8 行完成总结（主旨、状态、自主决策、提交数与区间、耗时、剩余项）。
- 剩余：发版与推送待你授权；多命令 hook（`suga-5xmf`）按其设计另行计划，可与本项同一版本发布。
