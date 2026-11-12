<!-- template_id: plan; template_version: 1.3.1 -->
# suga 安全配置键保护 实施计划

> 状态：Draft 0.1 / 待人工计划批准；issue `suga-hyj7`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-12 | Claude | 初稿：按已批准设计 0.8 拆为 T1–T7 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：落实设计 0.8——模型不能经 `suga config set/unset` 修改 protected 键；其他方式改动 protected 键时，你下次打开 chat 能看到改动的键名。
- scope freeze：设计 0.8 的 §1–§3 与验证要求；不含多命令 hook（`suga-5xmf`，另出设计）、发版与推送。
- 扩围策略：`DEFER_OR_REQUEST`；review budget：最多 8 轮；停止条件：无开放 `CORE_BLOCKING`。
- 暴露度（SR1409）：高（已发布 `v0.55.0`）；两轴独立评审与 ledger，评审目录 `docs/review/2026-11-12-protected-config-keys-plan/`。

## 目标与完成定义

完成定义（全部满足）：
1. 注册表每个键都在显式分类表中，protected 标记与表一致；`hooks.pre_tool_use.tools/name` 有不可写描述符；`tools.origin_judge.*`、`tools.read_deny*` 为可写 + protected。
2. `suga config set/unset` 改 protected 键：`SUGA_SESSION_ID` 非空或 stdin 非终端时退出码 2、配置文件不变、i18n 提示；终端里照常写入。
3. 启动比较：chat 横幅列出改动键名并推进快照；run 在 stderr 一行；ACP、SDK 每个会话记一条 `kind=config` trace；compact 不比较。设计 §3 的键集、整文件模式、规范化、快照模型、锁与路径规则全部实现。
4. 设计“验证要求”全部有对应测试且通过，含反向验证与主机 Windows。
5. README、`docs/usage/gofer-personal-agent.md`、CHANGELOG「未发布」已更新。

## 范围、排除项与授权

- 范围：suga 仓库 `internal/config`、`internal/cli`、`internal/wiring`、`internal/chat`、`internal/personal`（trace 事件与回合开始处）、`internal/personal/store/types.go`、`internal/i18n/catalog`、`pkg/sugasdk`（只在需要暴露比较结果时）、`test/ptye2e`、文档与 CHANGELOG。
- 排除：多命令 hook；`.env` 与环境变量的比较（D10）；发版、推送、安装、通知 gofer。
- 授权：本计划批准只授权进入实施请求；实施需你另行指示。本地提交为默认授权；push、tag、发版、安装需另行授权。
- `host_or_non_offline_action=REQUIRED`（T7 经 gofer exec job 在主机跑 Windows 测试；只写 `tmp/`，不改业务工作区）。

## 输入与批准证据

- 设计：`docs/design/2026-11-11-protected-config-keys-design.md` Draft 0.8，提交 `d416ed9b` 记录批准。
- 批准原话（2026-11-12）：“批准 suga 安全配置键设计 0.8（接受未复核修订与评审补入的键），开始写计划；hooks 并行出设计”。
- 设计评审：`docs/review/2026-11-11-protected-config-keys/`（6 轮，ledger PASS）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 键分类与 protected 标记 | `internal/config/settings_registry.go` ConfigKey | 是 | 原位加字段 | DIRECT_REUSE | none | 无 |
| CAP-02 | 配置 YAML 逐键读取、锁、原子写 | `internal/config/yaml_edit.go`（goccy AST、`configLockPath`、`editConfigFile`）、`bootstrap_lock.go` | 是 | 原位扩展（写后回调、路径规范化） | DIRECT_REUSE | none | 锁文件名变化见风险 |
| CAP-03 | 终端判定 | `internal/cli/cwdcheck.go` `isTerminalInput` | 是 | 注入为函数参数 | DIRECT_REUSE | none | 无 |
| CAP-04 | 横幅、stderr、trace 输出 | `internal/chat` `render.Banner`、`internal/personal/observe.go` recorder、`store` kind 常量 | 是 | 原位扩展 | DIRECT_REUSE | none | 无 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| NONE | none | none |

（快照逻辑放 `internal/config` 新文件 `protected_snapshot.go`，属既有 owner 扩展，不是新 Module。）

## 前置检查与 fail-closed 条件

- workspace baseline（2026-11-12）：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；branch `main`；HEAD 以计划批准时为准（本稿写于 `d416ed9b` 之后）；`git status --short` 干净；本地领先 origin 的提交为 nf95/xrxl/xsvv 与文档、tracker，未推送。
- 实施在主机建的 worktree：`tmp/xhen-p1/wt-repo.ps1 -Repo inhere-tools/suga -Name hyj7 -Branch feat/protected-config-keys`（`.worktrees/hyj7`）。容器内不 `git worktree add/prune`；对 worktree 的 git 命令只用单条前缀 `GIT_DIR`/`GIT_WORK_TREE`，不 export，不与测试同一命令。
- 测试隔离：所有测试与 ptye2e 用临时 `SUGA_HOME`；不读写真实 `~/.suga`、`~/.xhen`。
- fail-closed：任一任务的 focused 测试或反向验证不符合预期即停止，按实施发现四类记录；遇到其他人的改动（Ownership Conflict）停止。
- 依赖：不新增第三方依赖；goccy YAML 已在 go.mod（v1.19.2，含 `MergeKeyType`）；`gkconfig.LoadSources` 已存在。

## 波次与依赖

| 波次 | 任务 | 说明 |
|---|---|---|
| W1 | T1 | 注册表与分类表（其余任务依赖 `Protected`） |
| W2 | T2、T3 | CLI 拒绝；快照模块与 load/edit 改动（T3 的写后回调接入 T2 的 set/unset，T2 先完成拒绝部分） |
| W3 | T4 | 装配与各入口输出 |
| W4 | T5、T6 | ptye2e 场景；文档 |
| W5 | T7 | 全量 race、staticcheck、smoke、主机 Windows |

执行者：T1–T6 由 sonnet 子 agent 在 `.worktrees/hyj7` 顺序实施（每个任务一个提交）；我审查、反向验证复核并做 T7 与合并。

## 任务

### T1 注册表 `Protected` 与显式分类表

- 文件: `internal/config/settings_registry.go`；新测试 `internal/config/settings_protected_test.go`；同步注释与 `docs/usage/gofer-personal-agent.md:124-125`。
- 动作:
  - `ConfigKey` 增 `Protected bool`；按设计 §1 表给静态与动态描述符（`providerKeyDescriptors`、`providerModelKeyDescriptors`）标记。
  - 新增描述符 `hooks.pre_tool_use.tools`（StringList）、`hooks.pre_tool_use.name`（String），`Writable:false, Protected:true`。
  - `network.proxy.url`、`tools.web_fetch.proxy` 加 `Protected:true`（仍 `Sensitive`、不可写）。
  - `tools.origin_judge.enabled/timeout`、`tools.read_deny`、`tools.read_deny_defaults` 改 `Writable:true, Protected:true`，更新 `:227`、`:250` 处注释。
  - 新增导出函数 `func ProtectedConfigKeys() []ConfigKey`（静态 + 动态描述符中 `Protected` 的全部，供 T3 使用）。
  - 测试：显式表 `map[pattern]group`，group ∈ {`exec`,`relax`,`exec_limit`,`endpoint`,`judge`,`other_protected`,`cost`,`retention`,`model_choice`,`runtime`,`tool_pref`,`log`,`display`,`provider_tuning`,`secret`}；遍历全部静态与动态描述符：未在表中 → 失败；表中 protected 组与 `Protected` 字段不一致 → 失败。
- 验证: 在 worktree 根运行 `go test ./internal/config -run 'TestProtected|TestSettingsRegistry' -count=1`；反向验证：临时去掉 `tools.disabled` 的 `Protected`（可编译），确认分类表测试失败且输出非 `[build failed]`，恢复。
- 完成标准: 测试通过；反向验证记录在提交说明；`go vet ./internal/config` 通过。
- 依赖: 无。

### T2 `suga config set/unset` 拒绝 protected 键

- 文件: `internal/cli/config.go`、`internal/cli/root.go`（只改 `newConfigCommand` 的参数传递）、`internal/i18n/catalog/{zh,en}` 文案；测试 `internal/cli/config_protected_test.go`；更新 `config_origin_judge_test.go`、`config_read_deny_test.go`、`config_test.go:132-160`、`:175-204`、`approval_flags_test.go:133`，并 grep `scripts/` 与 `test/` 中对 protected 键的 `config set/unset` 调用同步修改。
- 动作:
  - `newConfigCommand(stdin io.Reader, stdout, stderr io.Writer)`；root 传入 stdin（沿用现有 capp 命令结构）。
  - 包级可替换变量 `var configStdinIsTerminal = isTerminalInput`（测试替换）。
  - 新函数 `func protectedWriteAllowed(key appconfig.ConfigKey, stdin io.Reader) error`：`key.Protected` 且（`strings.TrimSpace(os.Getenv("SUGA_SESSION_ID")) != ""` 或 `!configStdinIsTerminal(stdin)`）→ `configInputError(<i18n 文案>)`（退出码 2）；set 与 unset 在 `writableConfigKey` 之后调用。
  - 文案（zh）：“安全相关配置 %s 只能由你在 suga 之外的终端里修改，或直接编辑 %s”；en 对应。
- 验证: 在 worktree 根运行 `go test ./internal/cli -run 'TestConfigProtected|TestConfigSet|TestConfigCommands|TestConfigRefuses|TestRunApproveFlags' -count=1`。GR110 用例经 root 命令真实分发：会话内（`SUGA_SESSION_ID=x`）与非终端的 set/unset protected 键 → 退出码 2、文件字节不变；`SUGA_SESSION_ID` 为空字符串且注入“是终端” → 可写；非 protected 键在非终端下照常；`hooks.pre_tool_use.tools` set → 只读拒绝（退出码 2）。反向验证：临时让 `protectedWriteAllowed` 返回 nil，确认拒绝用例失败，恢复。
- 完成标准: 上述测试通过；受影响旧测试改为“会话内拒绝、终端可写”；反向验证记录。
- 依赖: T1。

### T3 快照模块、读取同源与单键更新

- 文件: 新 `internal/config/protected_snapshot.go` 与 `protected_snapshot_test.go`；`internal/config/load.go`；`internal/config/yaml_edit.go`（`configLockPath` 路径规范化、`editConfigFile` 写后回调）。
- 动作（签名为合同，内部可调整）:
  - `type ProtectedDigest struct { Mode string /* "keys"|"file" */; Keys map[string]string; File string }`
  - `func DigestProtected(data []byte) (ProtectedDigest, error)`：goccy 解析；顶层字面量点号键、锚点/别名、合并键（含内联 `<<: {…}`）、多文档任一出现 → `Mode="file"`；逐键摘要取未展开 `${VAR}` 的解析后值，整块值转 JSON（映射键排序、列表原序），键缺失不入 map；动态键从 `providers` 映射枚举 `<id>`、`models.<name>`；空文件或只有注释 → 空 map；`File` 恒为整文件 SHA-256。
  - `type ProtectedChange struct { Keys []string; WholeFile bool; SnapshotUnreadable bool }`；`func (c ProtectedChange) Empty() bool`。
  - `func CompareProtected(configPath string, cur ProtectedDigest) (ProtectedChange, error)`：持锁读快照；不存在 → 写入全部字段并返回空变化；损坏 → `SnapshotUnreadable=true`、`Keys`=全部当前与快照键名；任一侧 `file` 模式 → 比较 `File`（快照缺 `File` 视为变化）；否则按键名并集比较，缺失一侧按“未设置”。锁获取失败 → 返回错误，调用方记 Warn 并视为无变化。
  - `func AdvanceProtected(configPath string, cur ProtectedDigest) error`：持锁写入 `cur` 全部字段（临时文件 + 改名，目录 0700、文件 0600）。
  - 快照路径 `func protectedSnapshotPath(configPath string) string`：`SUGA_HOME/run/protected-config/default.json` 或 `<sha256(规范化路径)>.json`；规范化 = `filepath.Abs` + `Clean`，Windows 再 `strings.ToLower`；`configLockPath` 使用同一规范化路径。
  - `editConfigFile` 在写入成功、仍持锁时调用 `updateProtectedKeyLocked(path, key, newBytes)`：仅当键为 protected、快照存在、当前文件与快照均非 `file` 模式时改 `keys[key]`（unset 则删除该项），不改 `mode`/`file`；失败记 Warn（GR109 例外）。
  - `load.go`：读取配置文件字节一次（不取锁），经 `gkconfig.LoadSources` 加载；保留默认文件不存在按空、`SUGA_CONFIG` 不存在报错、按扩展名取格式的现有语义；新增 `func LoadedConfigBytes() (path string, data []byte, ok bool)`，`ResetLoadedStateForTest` 一并清空。
- 验证: 在 worktree 根运行 `go test ./internal/config -run 'TestProtectedSnapshot|TestDigestProtected|TestLoad|TestWriteConfig|TestUnsetConfig' -count=1 -race`。用例覆盖设计验证要求“快照”一项全部情形（首建、无变化、变化、人工 set 不吸收、新增/删除 provider、注释缩进不报、顶层点号键/锚点/内联合并键/多文档走整文件且带点模型名仍逐键、整文件模式人工 set 不改快照、损坏全部提示、`SUGA_CONFIG` 分别建快照含相对路径与 Windows 大小写（大小写用例只在 windows 构建标签下）、空文件、秘密键改动只出键名、并发 `CompareProtected`/`AdvanceProtected` 多 goroutine 不损坏文件）。反向验证：临时把并集改为只取快照键，确认“新增 provider”用例失败，恢复。
- 完成标准: 测试通过且 `-race` 干净；`go test ./internal/config/... ./internal/cli/...` 全绿；反向验证记录。
- 依赖: T1；与 T2 的 set/unset 共用 `editConfigFile`，T2 先合入。

### T4 装配与各入口输出

- 文件: `internal/wiring/runtime.go`（`Options`、`Open`/`OpenCLI`）；`internal/cli/compact.go`（传关闭字段）；`internal/cli/run.go`（run 的 stderr 行、chat 的回调）；`internal/chat/chat.go`（`Options` 新回调与横幅文本）；`internal/chat/layout.go`（`BannerInfo` 字段）；`internal/personal/runner.go` 回合开始处与 `observe.go`；`internal/personal/store/types.go`（`KindConfig = "config"`）；`internal/cli/acp.go`、`pkg/sugasdk/session.go`（只在需要传递比较结果时）；i18n 文案；测试同目录。
- 动作:
  - `wiring.Options.SkipProtectedCheck bool`；`Open`/`OpenCLI` 在 `Load` 之后、非 Skip 时：`DigestProtected(LoadedConfigBytes())` → `CompareProtected`，结果按配置路径做进程内缓存（`sync.Once` 风格，`ResetProtectedCheckForTest()` 重置），挂到 `Runtime.ProtectedChange() appconfig.ProtectedChange` 与 `Runtime.ProtectedDigest()`。
  - chat：`chat.Options.ProtectedChange appconfig.ProtectedChange` 与 `OnBannerShown func()`；横幅后有变化时显示一行（设计 §3 文案；`WholeFile`/`SnapshotUnreadable` 用对应文案），随后调用 `OnBannerShown`，由 `internal/cli` 调 `AdvanceProtected(path, digest)`。
  - run：有变化时在开始执行前向 stderr 写一行同样文案，不推进。
  - trace：personal 回合开始（`runner.go:670` 附近）若该会话尚未记录且变化非空，记 `store.Event{Kind: store.KindConfig, Name: "protected_config_changed", Attrs: {"keys":[...]}或{"whole_file":true}}`；按会话 ID 在 runtime 内去重，使 ACP 同一会话多个 prompt 只记一条。
  - compact：传 `SkipProtectedCheck: true`。
- 验证: 在 worktree 根运行 `go test ./internal/wiring ./internal/chat ./internal/personal/... ./internal/cli ./pkg/sugasdk -run 'Protected' -count=1`。用例：run 的 stderr 行且快照不推进；chat 回调推进写入启动摘要（比较后、推进前改动另一个键，下次仍报出）；ACP 同会话两次 prompt 只有一条 trace；SDK 会话有 trace；compact 不建快照。反向验证：临时去掉 trace 记录，确认 ACP/SDK 用例失败，恢复。
- 完成标准: 测试通过；`go test -race ./internal/... ./pkg/...` 全绿。
- 依赖: T3。

### T5 ptye2e chat 场景

- 文件: 新 `test/ptye2e/chat_protected_config_test.go`（参照 `chat_observe_test.go`、`chat_origin_judge_test.go`）。
- 动作: 用临时 SUGA_HOME 与假模型：首次 chat 建快照、无提示；直接改 `config.yaml` 的 `tools.read_roots` 后再开 chat，断言横幅出现键名，`suga inspect trace --json` 有 `kind=config` 事件；第三次 chat 无提示。另一用例：在伪终端里执行 `suga config set tools.read_roots …` 成功（终端判定为真）。
- 验证: 在 worktree 根运行 `make smoke-go` 中对应用例（`go test ./test/ptye2e -run 'TestChatProtectedConfig' -count=1`）；反向验证：临时不调用横幅显示，确认用例失败，恢复。
- 完成标准: 用例通过；反向验证记录。
- 依赖: T4。

### T6 文档

- 文件: `README.md`（配置一节）、`docs/usage/gofer-personal-agent.md`、`CHANGELOG.md`「未发布」。
- 动作: 写明 protected 键清单与分组、修改方式（外部终端或直接编辑文件）、会被拒的场景、启动提示与限制（设计“风险与限制”中用户需要知道的部分）、`tools.origin_judge.*`/`tools.read_deny*` 变为终端可写。
- 验证: 人工核对与设计 §1–§3 一致；`grep -n "只能写在配置文件里" docs/usage/gofer-personal-agent.md` 无旧表述残留。
- 完成标准: 文档与实现一致。
- 依赖: T4。

### T7 全量验证（我执行）

- 文件: 无代码改动；主机脚本放 `tmp/suga-hyj7/`。
- 动作: worktree 根运行 `go test -race ./...`、`go run honnef.co/go/tools/cmd/staticcheck@latest ./...`、`make smoke-go`、`make smoke-chat`；grep `scripts/` 中 `suga config set` 调用并确认未受影响；主机 gofer exec job 在 worktree 跑 Windows `go test ./...` 与 `go test ./test/ptye2e -run TestChatProtectedConfig`（ConPTY）。
- 验证: 上述命令全部通过，结果记录在 issue 评论。
- 完成标准: 全绿；合并按主机 `tmp/xhen-p1/host-merge.ps1`，以 `is_ancestor=0` 为收尾前提。
- 依赖: T5、T6。

## 回滚与恢复

- 每个任务一个提交，恢复点为各任务提交；合并前在 worktree 分支上，主分支不受影响。
- 合并后回滚：revert 合并提交；用户侧删除 `SUGA_HOME/run/protected-config/` 即回到“首次运行”。
- 锁文件名变化：升级过渡期新旧版本同时写同一 `SUGA_CONFIG` 配置可能拿到不同锁，接受（设计风险）。

## 人工 Gate

1. 计划批准（本稿评审后）。
2. 当前执行请求：你指示开始实施。
3. 外部动作 Gate：push、tag、发版、安装、通知使用方，均需你另行逐项批准；本计划不含。

## 可追溯性

| 设计条目 | 任务 | 验证 |
|---|---|---|
| D1、D4、D5、D6（§1 清单与分类表） | T1 | 分类表测试 + 反向验证 |
| D3、D7（§2 CLI 拒绝） | T2 | GR110 分发测试 + 反向验证 |
| D8、D9、D10（§3 比较、快照、锁、路径） | T3 | 快照单元测试 + 并发 + 反向验证 |
| §3 各入口、trace、compact | T4、T5 | 入口测试、ptye2e + trace 断言 + 反向验证 |
| 替代声明、文档 | T1、T6 | 文档核对 |
| 验证要求（Windows） | T7 | 主机 Windows 测试 |

## 完成 Gate 与剩余工作

- 完成条件：T1–T7 完成标准全部满足、合并到 main（本地），issue `suga-hyj7` 评论附验证结果后关闭；plan todo 标 done。
- 进入 `COMPLETE` 时写 ≤8 行完成总结（主旨、状态、自主决策、提交数与区间、耗时、剩余项）。
- 剩余：发版与推送待你授权；多命令 hook（`suga-5xmf`）按其设计另行计划，可与本项同一版本发布。
