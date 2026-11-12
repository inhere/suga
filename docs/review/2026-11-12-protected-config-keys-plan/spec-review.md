<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护实施计划 第 2 轮 Spec/Executability 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `f897738008b449672ece282f4a841fe3e93a8443`；subject `docs/plans/2026-11-12-protected-config-keys-plan.md`
- 修订版本: Draft 0.2（上轮 `109ec8c2` / Draft 0.1）
- 评审者: 独立评审子 agent（Spec/Executability 轴，第 2 轮 confirmation，REPORT_ONLY）
- 日期: 2026-11-12
- 排除项: 设计 0.8 的选择本身；Standards/Governance 轴；不改仓库、不提交

## 输入与方法

- 规范：payload `SD95t2uhRN3Z9UqnPguVJg`（plan-contract、planning-core-rigorous、plan-closure、plan 模板、SR14xx、GR107–GR110、SR2001–SR2004）；review-contract 与 review 模板取自 payload `3Z3cOanwUM2ILAXHbxiTlQ`。
- 读取：`AGENTS.md`；上轮本轴报告 `docs/review/2026-11-12-protected-config-keys-plan/spec-review.md`（PF1–PF12）；计划 0.2 全文，以及 `git diff 109ec8c2..f8977380`；设计 0.8 中 §1–§3、架构与验证要求。
- 对照代码（HEAD = candidate，工作树干净）：
  - `internal/cli/{config,root,run,chat,i18n,cwdcheck,acp,compact}.go`，以及 `config_test.go`、`approval_flags_test.go`、`config_read_deny_test.go`、`config_origin_judge_test.go`；
  - `internal/config/{load,yaml_edit,config_candidate,settings_registry,settings_registry_source}.go`；
  - `internal/wiring/{runtime,subagent}.go`；`internal/personal/{runner,turncontext}.go`、`store/types.go`；
  - `internal/chat/{chat,render,layout}.go`；`internal/i18n/{i18n.go,catalog/*}`；`pkg/sugasdk/session.go`、`engine.go`；`cmd/suga/main.go`；
  - `test/ptye2e/{smoke_helpers_test,chat_smoke_test}.go`；`scripts/ptysmoke/chat_onboarding.py`；`scripts/acpprobe/acp_probe.py`；
  - 依赖源码：gookit/gopty v0.1.2 `ptytest`、goutil v0.8.6 `cflag/capp/app.go`、gookit/config v2.2.9 `load.go`；主机脚本 `tmp/xhen-p1/{wt-repo,host-merge}.ps1`。
- 方法：逐条核对 PF1–PF12 的关闭情况。对 0.2 新写的签名、调用点和验证命令，逐项与代码比对。验证命令的 `-run` 正则与现有测试名比对，反向验证逐条判断能否编译。本轮未执行 `go test`。

## 验收标准

- plan-contract / plan-closure：每个任务的文件、符号、动作、验证和完成标准能被实施者唯一、正确地执行；跨模块函数写出签名与错误处理；验证命令能真实证明完成标准。
- review-contract：`CORE_BLOCKING` 只限五类，并给 necessity proof；同类穷尽。

## 上轮发现关闭情况

| ID | 状态 | 依据 |
|---|---|---|
| PF1（阻断） | 关闭 | T5、T7 的 ptye2e 命令都带 `-tags ptye2e`，T5 新文件首行是 `//go:build ptye2e`。`-run 'TestChatProtectedConfig\|TestConfigSetProtectedInTerminal'` 能匹配计划里两个用例名。T5 增加了“终端 set 成功、非终端被拒”的真实二进制用例；T7 在 Windows 上跑同一条带 tag 的命令。Windows 可行性已核实：`sugaRun`（`smoke_helpers_test.go:196`）不设 Stdin，子进程拿到的是 NUL，`isTerminalInput` 判为假；pty 用 `ptytest.StartSpec` 启动，用 `WaitExit` 取退出码（gopty `ptytest.go:89,227`）。`testEnv` 会剥掉 `SUGA_*`（`chat_smoke_test.go:435`）。 |
| PF2（阻断） | 关闭 | 比较结果与按会话去重放进 `internal/wiring` 的进程级缓存（键为配置路径）。回调签名和调用点（`runner.go:669` 的 `recordTurnContext` 之前，此时 `withObserveScope` 已在 :568/:613 设置）都已写定。ACP 每个 prompt 都 `wiring.Open`（`acp.go:84`），缓存在进程级，所以只记一条。 |
| PF3 | 关闭 | `AdvanceProtectedSnapshot` 只在比较成功且变化非空时写入；chat 遇到错误记 Warn。 |
| PF4 | 基本关闭 | 签名、Load 阶段取绝对路径、`ok` 语义、`configFileName` 都已写明。残留问题见 PF18、PF19：`Reload` 的合同与代码结构矛盾；`SUGA_CONFIG` 下 source 输出没有对应验证。 |
| PF5 | 基本关闭 | 文件清单补了 `chat.go`、`render.go`；比较只在 `Open` 中做；常量为 `EventConfig`；推进不再需要外部传入 path。展示用的 `{path}` 从哪里来仍没写，见 PF16（同类，上轮未穷尽）。 |
| PF6 | 关闭 | 写定了 handler 的 stdin 签名；`setupConfigCLI` 注入“是终端”并清空 `SUGA_SESSION_ID`；负向测试点了名。核实：`TestConfigRefusesTrustTier`、read_deny、origin_judge 三个测试和 `TestConfigSetAllowsSimpleRuntimeKeys` 都经 `setupConfigCLI`。 |
| PF7 | 关闭 | T6 改 `chat_onboarding.py`，改用的 `chat.markdown` 可写（`settings_registry.go:151`）。 |
| PF8 | 关闭 | 新增 T3b；T3 的用例补齐了 hooks、只有注释、快照 `mode=file` 时“下次仍报”；字面键盘点放进 T1。 |
| PF9 | 基本关闭 | 比较两侧都用 `normalizeConfigPath`。`ResolveSugaHome` 出错这一分支没有写，见 PF19。 |
| PF10 | 基本关闭 | 修正了 T1 的 `-run`，覆盖四张动态表，T6 grep 补了英文表述。i18n 调用方式与键前缀仍有误，见 PF13；T3 出现新的空匹配 `-run` 片段，见 PF18（同类）。 |
| PF11 | 关闭 | 子 agent 不记录。已核实子 runner 由 `wiring/subagent.go:189` 另行调用 `personal.New` 构造，不共用 Runner，所以计划里“若共用”的假设分支用不上（见 PF17）。 |
| PF12 | 关闭 | baseline 写明 HEAD `97e26d11` 与工作树干净，并登记了并行的 hooks 设计；当前 `git status --short` 为空，与之一致。 |

## 发现

### PF13 [MEDIUM] i18n 调用写法和键名与代码不符；`TestConfigProtected` 断言“stderr 含键名”在单元测试里拿不到

- disposition: `CORE_CORRECTIVE`
- 证据（同类穷尽：计划中所有文案键和错误输出断言）:
  1. T2（计划 :136）写的是 `i18n.T("config.protected_refused", map{…})`。`internal/i18n` 没有包级 `T`，只有 `(*Catalog).T`（`i18n.go:158`）。CLI 里的惯例是 `cliText(tr, "cli.config.written", …)`（`config.go:83`、`cli/i18n.go:39`）。
  2. catalog 的键要带 ini 节名前缀：`cli.ini` 的 `[cli]` 节下 `config.written` 要用 `cli.config.written` 查；`status.ini` 的 `[status]` 节下 `banner.session` 要用 `status.banner.session` 查（`layout.go:112`）。计划 T2 写的键是 `config.protected_refused`，T4 写的是 `protected.changed`、`protected.changed_file`、`protected.unreadable`，都没有写出查找时用的完整键（`cli.…`、`status.…`）。键找不到时只记一条 warn，并原样返回键名。
  3. 文案里的 `{keys}` 占位是列表，计划没有写拼接方式（例如用“、”或“, ”连接）。直接传 `[]string` 会渲染成 `[a b]`。
  4. T2 验证（:139）要求 `TestConfigProtected` 断言“stderr 含键名”。但 `capp.App.RunWithArgs` 只返回错误，不打印（goutil `cflag/capp/app.go:154-166`）；打印发生在 `cmd/suga/main.go:70-72`。现有测试都断言 `err.Error()`（例如 `config_read_deny_test.go:31`）。按原文写，正确的实现反而会让这条断言失败。
- 影响: 实施者会在测试失败后自行修正，但计划给出的写法本身不对。
- 建议: T2 写成 `configInputError(cliText(tr, "cli.config.protected_refused", map[string]any{"key": key, "path": path}))`；T4 写出完整键 `status.protected.changed` 等，并规定 `{keys}` 用 `strings.Join(keys, ", ")`。单元测试改为断言 `err.Error()` 含键名；只有 ptye2e 的真实二进制用例断言 stderr。

### PF14 [MEDIUM] `ProtectedNotice` 在没有变化或比较失败时的返回值没有写；失败结果是否缓存没有写

- disposition: `CORE_CORRECTIVE`
- 证据: T4（计划 :173）只写了“对同一 session ID 只返回一次 `ok=true`”和 attrs 的三种形态，没有写 `change.Empty()` 或 `protectedCheck` 出错时返回 `ok=false`。T4 的用例只测了“有变化时一条”，没有“无变化时零条”的断言。按字面实现，每个会话都可能记一条空的 `protected_config_changed`，现有用例都发现不了。设计入口表（设计 §3）只在“有改动时”写 trace，因此不定为阻断。另外，`protectedCheck` 失败（例如锁超时）后，ACP 后续的 prompt 是沿用失败结果还是重新比较，计划也没有写。
- 建议: 写明 `change.Empty()` 或比较失败时 `ProtectedNotice` 返回 `ok=false`、不写事件；写明失败结果是否缓存（建议缓存，与“进程内比较一次”一致）。T4 补一个用例：无变化的会话没有 `kind=config` 事件。

### PF15 [MEDIUM] T7 的 acpprobe 验证必然空过，证明不了“至多一条”

- disposition: `CORE_CORRECTIVE`
- 证据: `scripts/acpprobe/acp_probe.py:262-263` 启动时会 `shutil.rmtree(home)`，`--home` 的帮助文字（:241）也写着 “reset at start”；:272 让 `SUGA_CONFIG` 指向真实配置。因此每次运行快照都不存在，`CompareProtected` 首建后返回空变化，trace 里一定没有 `kind=config`，“确认至多一条”恒为真。T7 的命令也没有给出必填参数 `--bin`、`--cwd`、`--config-from`，以及两个 `--prompt`。去重的实际证明在 T4 的单元测试，所以不阻断；但计划把它列为完成定义 4 的 real-env 证据，这个证据是空的。
- 建议: 二选一。（a）把 T7 的 acpprobe 验收改成“真实 provider 下两次 prompt 跑通，首次运行 0 条 `kind=config`（首建快照）”，并写明去重由 T4 与 T5 证明；（b）如果要真实证明去重，需要能预置快照（脚本加“不重置 home”选项，这属于扩大文件范围），或者不用脚本，用同一个 home 手工跑两次 ACP。无论选哪个，都写全命令参数。

### PF16 [LOW] 横幅和 run stderr 文案里的 `{path}` 从哪里来没有写；横幅数据经 `rendererOptions` 传递也没有写（PF5 同类，上轮未穷尽）

- disposition: `CORE_CORRECTIVE`
- 证据: `status.ini` 的三条文案都含 `{path}`（计划 :177）。`Runtime` 只新增了 `ProtectedChange()` 和 `AdvanceProtectedSnapshot()`（:172），`ProtectedChange` 结构里没有路径（:149）。横幅由 `Renderer.Banner()` 从 `r.opts`（`rendererOptions`，`render.go:43`、:740-747）组装，`rendererOptions` 在 `chat.go:412` 由 `chat.Options` 映射而来。计划只写了 `chat.Options` 字段和 `render.go` 的追加行，没有写 `rendererOptions` 字段和 :412 的映射。
- 建议: 二选一：`ProtectedChange` 增加 `Path string`；或者 `Runtime` 增加 `ProtectedConfigPath()`。同时写明 `rendererOptions` 加一个对应字段，在 `chat.go:412` 映射。

### PF17 [LOW] 回调写成“`Runner` 新字段”，但 Runner 由 `personal.New(personal.Config{…})` 构造；`runtime.go` 改动范围写少了

- disposition: `CORE_CORRECTIVE`
- 证据: `Runner` 只有未导出字段和 `cfg Config`（`runner.go:410-411`），wiring 在 `runtime.go:722` 与 `subagent.go:189` 各自调用 `personal.New(personal.Config{…})`。计划（:173）写“`Runner` 新字段……由 wiring 设置”，并保留“若共用 Runner”的假设分支。T4 的文件说明写 `runtime.go`“只加 Options 字段与一处调用”，但实际还需要 Runtime 结构字段（保存 `*protectedState`），以及 :722 处 Config 的一行。
- 建议: 写定为 `personal.Config.ProtectedNotice`，在 `runtime.go:722` 设置，`subagent.go:189` 不设置，删掉“若共用”分支。`runtime.go` 的改动范围按实际列出，净增 ≤20 行的约束保留。

### PF18 [LOW] T3 的 `-run` 含空匹配片段，而受 T3 影响的 CLI 回归用例不在 T3 的 focused 范围内（PF10.1 同类）

- disposition: `CORE_CORRECTIVE`
- 证据: T3 验证（:155）中的 `TestConfigLockPath` 不匹配任何现有测试。锁名的回归断言 `run/locks/config.lock` 在 `internal/cli/config_test.go:158-159`（`TestConfigSetAllowsSimpleRuntimeKeys`）。`configFileName` 改用 `LoadedConfigBytes` 会影响 `config get` 的 `source` 输出（`TestConfigCommandsUseTheRealCappPath`），而 T3 只跑 `./internal/config`。PF4 建议补的 `SUGA_CONFIG` 场景 source 断言也没有加。
- 建议: 删除 `TestConfigLockPath`。T3 验证追加 `go test ./internal/cli -run 'TestConfigSetAllowsSimpleRuntimeKeys|TestConfigCommands' -count=1`，并补一条相对 `SUGA_CONFIG` 下 `config get` 的 source 断言（绝对路径）。

### PF19 [LOW] `Reload` 的合同与代码结构矛盾；快照路径规则漏了 `configLockPath` 的一个分支（PF4、PF9 残留）

- disposition: `CORE_CORRECTIVE`
- 证据:
  1. 计划（:154）写“`Reload` 不改变它”，但 `Reload` 就是 `gkconfig.Reset()` 加 `Load()`（`config_candidate.go:50-53`）。如果 `Load` 记录字节，`Reload` 必然刷新它。要满足合同，需要拆分 Load 或者在 Reload 中保存再恢复，计划没有写机制。`Reload` 只在 `config set/unset` 写入后调用（`cli/config.go:197`），之后该进程不再比较，所以两种语义的结果一样。
  2. `configLockPath` 有三个回退分支：`ResolveSugaHome` 出错时用 `path + ".lock"`（`yaml_edit.go:103-105`）；无 env 且非默认文件时放在文件旁（:116-118）；建目录失败时（:121-124）。计划 :152 只写了后两个。
- 建议: 改写为“`Reload` 会刷新字节，路径不变，不影响比较”（最简单），或者写明保存与恢复的机制。补一句 `ResolveSugaHome` 出错时快照放在哪里（或跳过比较，并返回错误交给调用方记 Warn）。

### PF20 [LOW] T5、T6 的写法细节

- disposition: `CORE_CORRECTIVE`
- 证据（同类穷尽：ptye2e 与 grep 验证的可执行细节）:
  1. `startChatOpts` 每次调用都会重写 `config.yaml`（`smoke_helpers_test.go:44-63`）。T5 写“直接改 `config.yaml` 的 `tools.read_roots`，再开 chat”：如果改完文件再用 `startChatOpts` 打开，改动会被覆盖。应改为第二、三次调用都通过 `ToolsExtra` 带上同一个 `read_roots`。
  2. trace 事件在回合内才记录（回调在 `Runner.Run` 中）。T5 第二次 chat 需要发一条 prompt，由 `exitAfterTurn` 拿到 session id 后再调用 `traceEvents`。计划没有写“发一个回合”。
  3. `TestConfigSetProtectedInTerminal` 需要自己写 `config.yaml` 和空 `.env`，避免出现首次启动提示（参照 `startChatOpts` :60-63）；非终端用例可以直接用 `sugaRun`；pty 用例用 `ptytest.StartSpec` 加 `WaitExit`。
  4. T6 的 grep 只匹配 `config", "set"` 和 `config set`，漏了 `"config", "unset"`（`chat_onboarding.py:206`），而且 `grep -v _test.go` 会把整个 `test/ptye2e` 排除。
- 建议: 按以上各条补进 T5、T6 的动作和验证；T6 的 grep 改为 `grep -rnE '"config", "(set|unset)"|config (set|unset)' scripts/ test/`，并逐条核对命中的键。

## 覆盖缺口与限制

- 本轮没有运行任何 `go test` 或 ptye2e。Windows 可行性的结论来自源码推断（Go 的 `exec.Cmd` 在 Stdin 为 nil 时使用 NUL；ConPTY 子进程的标准句柄是控制台句柄），没有实际执行。
- 没有验证 `staticcheck@2026.2.1` 版本是否存在（属 Standards 轴 PS8）。
- 没有核对 goccy AST 对内联合并键、多文档的识别能力（延续上轮限制）。
- 未变更的章节沿用上轮结论。

## 结论

PASS。PF1、PF2 两个阻断项已关闭；PF3–PF12 已处理，其中 PF4、PF5、PF9、PF10 有少量残留，已并入本轮发现。本轮没有新的 `CORE_BLOCKING`。新增 `CORE_CORRECTIVE` PF13–PF20：MEDIUM 三项（PF13–PF15），LOW 五项（PF16–PF20）。按 planning-core-rigorous，本轮 PASS 但含 `CORE_CORRECTIVE`，修订后可以增加唯一一轮纠正确认轮；不修订的话，应在 Gate 上披露。

## 剩余风险与后续

- PF13、PF14 都会被测试或设计兜住，但按原文实施会多一轮返工；建议与 PF15 一起在 0.3 里修订。
- PF15 不修的话，完成定义 4 中 “acpprobe” 这一项的证据为空，交付说明应如实写明。
- ptye2e 的 Windows 结果仍要以主机 gofer job 日志为证。
