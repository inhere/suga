<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护实施计划 第 1 轮 Spec/Executability 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `109ec8c21158ff9238203ebba2ac7e9cd12a767d`；subject `docs/plans/2026-11-12-protected-config-keys-plan.md`
- 修订版本: Draft 0.1
- 评审者: 独立评审子 agent（Spec/Executability 轴，第 1 轮 discovery，REPORT_ONLY）
- 日期: 2026-11-12
- 排除项: 设计 D3–D10 的选择本身；Standards/Governance 轴（另一评审者负责）；不改仓库、不提交

## 输入与方法

- 规范：idev-std payload `SD95t2uhRN3Z9UqnPguVJg`（plan-contract、planning-core-rigorous、plan-closure、plan 模板、SR14xx、GR107–GR110、SR2001–SR2004）；review-contract 与 review 模板取自 base payload `OfGu6IpGnHz92hejmPjb9g`（delta `bXLJzNcvvw_ZrjUevizE3A` 相对它移除了 review-contract，因此改读 base 中的正文）。
- 读取：仓库 `AGENTS.md`；计划全文；设计 `docs/design/2026-11-11-protected-config-keys-design.md` Draft 0.8 全文。
- 对照代码（HEAD = candidate）：`internal/cli/{config,root,cwdcheck,run,chat,compact,acp}.go`、`internal/cli/{config,config_origin_judge,config_read_deny,approval_flags,i18n_cli,preferred_tools}_test.go`、`internal/config/{settings_registry,yaml_edit,load,bootstrap_lock,bootstrap_unix,bootstrap_windows,config_candidate,settings_registry_source}.go` 与测试名、`internal/wiring/runtime.go`、`internal/chat/{chat,layout,render}.go`、`internal/personal/{runner,observe,turncontext,subagent}.go`、`internal/personal/store/types.go`、`pkg/sugasdk/{session,engine}.go`、`cmd/suga/main.go`、`test/ptye2e/*`、`Makefile`、`scripts/ptysmoke/chat_onboarding.py`、`internal/i18n/catalog/*`、gookit/config v2.2.9 `load.go`。
- 实际执行（只读）：`go test ./test/ptye2e -run 'TestChatProtectedConfig' -count=1`（证明 PF1）；用 grep 盘点字面配置键读取与注册表的差集。

## 验收标准

- plan-contract：每个任务的文件/符号/动作/验证/完成标准能被只读计划与设计的实施者唯一、正确执行；跨模块函数写出签名、返回与错误处理；验证命令写明运行目录，且真能证明完成标准；反向验证可编译。
- plan-closure：任务可追溯到设计核心目标与“验证要求”。
- review-contract：`CORE_BLOCKING` 只限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY，并给 necessity proof；同类穷尽。

## 发现

### PF1 [BLOCKER] ptye2e 验证命令不带 `-tags ptye2e`，无法执行；主机 Windows 的 ConPTY 验收证明不了

- disposition: `CORE_BLOCKING` [BUG]
- 证据:
  - `test/ptye2e/*.go` 全部以 `//go:build ptye2e` 开头；`Makefile:170` 的 `smoke-go` 是 `go test -tags ptye2e -count=1 -timeout 30m ./test/ptye2e/`。
  - T5 的验证命令 `go test ./test/ptye2e -run 'TestChatProtectedConfig' -count=1`（计划 :149）在 candidate 上实际执行得到 `build constraints exclude all Go files ... FAIL [setup failed]`。如果新文件不带 tag，它会成为唯一被编译的文件，但用不到 `buildSuga`、`testEnv`、`traceEvents` 等 helper，结果是编译失败。
  - T7（计划 :164）的 Windows 命令 `go test ./...` 不带 tag，会静默跳过 `test/ptye2e`；`go test ./test/ptye2e -run TestChatProtectedConfig` 同样缺 tag。
  - 设计“验证要求”最后一项（设计 :231）要求 ConPTY 下真实二进制在终端里 set protected 键成功、管道输入被拒。T5 只有“终端里 set 成功”一个用例，没有“真实二进制 + 管道 stdin 被拒”。
- necessity proof: 完成定义 4（计划 :27）要求“设计验证要求全部有对应测试且通过，含……主机 Windows”。按计划原文执行，T5 的命令会失败，T7 的 Windows 全量会“全绿”但没有跑任何 ConPTY 用例。实施者照计划执行时，要么卡在不可执行的命令上，要么在验收没有证据的情况下宣称完成。这属于“不可执行或结果错误”。
- 影响: 主机 Windows 这一核心验收没有可执行的证明路径。
- 建议: T5、T7 的 ptye2e 命令统一改为 `go test -tags ptye2e -count=1 -run 'TestChatProtectedConfig' ./test/ptye2e/`，注明运行目录为 worktree 根，Windows 用同一条命令；新文件首行写 `//go:build ptye2e`。T5 增加“真实二进制、`exec.Command` 管道 stdin 执行 `config set tools.read_roots …` → 退出码 2、文件字节不变”的用例。另外写明 `startChatOpts` 固定在参数前加 `chat`（`smoke_helpers_test.go:85`），终端里的 `config set` 需要直接构造 `gopty.Spec{Command: binary, Args: []string{"config","set",…}, Env: testEnv(home)}`。`testEnv` 会剥掉全部 `SUGA_*`，因此 `SUGA_SESSION_ID` 为空；pty 的 stdin 让 `isTerminalInput` 为真。

### PF2 [BLOCKER] trace 去重写成“在 runtime 内”，而 ACP 每个 prompt 都新建 runtime，验收用例必然失败；wiring 到 personal 的接口也没有写

- disposition: `CORE_BLOCKING` [BUG]
- 证据:
  - 计划 T4（:139）写“按会话 ID 在 runtime 内去重，使 ACP 同一会话多个 prompt 只记一条”。
  - `internal/cli/acp.go:67-87`：`acpserver.Serve` 的工厂对每个 prompt 调用一次 `wiring.Open` 并新建 Runtime；设计 :171 也写明“ACP 每个 prompt 新建 runtime”。
  - `pkg/sugasdk/session.go:98`：每个 SDK 会话也各自 `wiring.Open`。
  - `internal/personal/runner.go:667` 一带（`recordTurnContext`）是回合开始的记录点，但 `personal.Runner` 没有任何途径拿到比较结果。计划没有给出 `personal` 的配置字段、回调签名，也没有写去重集合放在哪里。
- necessity proof: 按计划原文，去重集合挂在 Runtime 上。ACP 第二个 prompt 拿到的是新 Runtime，集合为空，会再记一条，T4 的验收用例“ACP 同会话两次 prompt 只有一条 trace”必然失败。设计只说“按会话 ID 去重”和“进程内按配置路径缓存比较结果”，没有指定去重放在哪一层，所以实施者无法从计划和设计唯一推出正确做法。
- 影响: 结果错误（重复 trace），或实施者自行决定跨模块接口。
- 建议: 写定去重与比较结果放在同一个进程级缓存条目里，键为规范化配置路径，条目含 `change`、`digest`、`path` 和 `recorded map[sessionID]struct{}`，用 mutex 保护。写定 wiring 交给 personal 的接口签名，例如 `personal.Config.ConfigNotice func(sessionID string) (attrs map[string]any, ok bool)`：首次对某个会话调用时返回 attrs 并标记已记录，之后返回 false。写定调用点：`Runner.Run` 中 `withObserveScope` 之后、`recordTurnContext` 之前，事件经 `r.rec.record` 写入。同时写明子 agent 会话（`internal/personal/subagent.go:95` 的 `child.Run`）是否记录（见 PF11）。

### PF3 [HIGH] chat 推进快照的前提与错误处理没有写定，一种合理读法会吸收未展示的改动

- disposition: `CORE_CORRECTIVE`
- 证据: T4（:137）写“横幅后有变化时显示一行……随后调用 `OnBannerShown`，由 `internal/cli` 调 `AdvanceProtected(path, digest)`”，可以理解成无论有无变化都调用。T3（:123）规定锁获取失败时“调用方记 Warn 并视为无变化”。另外，`LoadedConfigBytes` 返回 `ok=false` 或 `SkipProtectedCheck` 时，Runtime 上的 digest 是零值。设计 :169 的入口表只在“有改动时”推进，:163 写锁失败时“跳过比较”。
- 影响: 如果无条件推进，比较失败或被跳过时会用启动摘要（甚至零值摘要）覆盖快照，把从未展示过的改动吸收掉，违反核心目标“你下次打开 chat 能看到”。设计能消解这个歧义，所以不定为阻断；但计划应当写成一句不会读错的话。
- 建议: 写明只有比较成功且 `!change.Empty()` 时，`OnBannerShown` 才推进；比较出错、跳过或 `ok=false` 时不推进。`AdvanceProtected` 返回错误时用 `slog.Warn` 记录，不影响 chat（GR109 例外，理由同设计 :176）。`OnBannerShown` 每个进程最多调用一次。

### PF4 [HIGH] `LoadedConfigBytes` 的合同不完整：路径在什么时刻取绝对路径、`ok=false` 时怎么处理、与 `Reload` 和 `LoadedFiles` 的关系都没写

- disposition: `CORE_CORRECTIVE`
- 证据:
  - `cmd/suga/main.go:52` 在命令分发前调用 `Load`。`internal/cli/run.go:163` 的 `guardResumeCwd` 可能把进程切到会话目录（chat 的第一选择），之后才在 :187 调 `OpenCLI`。计划 T3（:125）的 `protectedSnapshotPath` 在比较时才做 `filepath.Abs`。如果 `SUGA_CONFIG` 是相对路径（设计 :229 要求覆盖这个用例），Load 读的是原 cwd 下的文件，快照和锁却算在新 cwd 下：会找到错误的快照文件，按首次运行处理而不提示，chat 还会推进错误的文件。
  - wiring 有大量测试直接调用 `Open`/`OpenCLI` 而不调用 `Load`（如 `internal/wiring/allow_write_test.go`、`approval_mode_test.go`、`capabilities_test.go` 等）。计划没有写 `ok=false` 时 Open 应当跳过比较，而不是报错。
  - 默认 `config.yaml` 不存在时（SDK 的 `ensureConfigHome`，`pkg/sugasdk/engine.go:157`，不走 EnsureHome），返回值 `ok`/`data` 的语义没有写。
  - `Reload`（`internal/config/config_candidate.go:50`）会再调用 `Load` 并覆盖字节。目前只有 `internal/cli/config.go:197` 调用它，计划应写明这是预期行为。
  - 改用 `gkconfig.LoadSources` 后，`gkconfig.Default().LoadedFiles()` 会变空（gookit/config `load.go:497-525` 只有 `loadFile` 会追加），`internal/config/settings_registry_source.go:191` 的 `configFileName()` 会退回到环境变量或家目录的拼接。现有输出（`config get` 的 `source = config: <路径>`）基本不变，但计划没有提到这个依赖。
- 影响: 相对 `SUGA_CONFIG` 加跨目录续接时漏报；实施者需要自行决定多处语义。
- 建议: 签名改为 `func LoadedConfigBytes() (absPath string, data []byte, ok bool)`。`absPath` 在 `Load` 内、读文件时就用 `filepath.Abs` 求出。默认文件不存在时返回 `ok=true, data=nil`，按“全部未设置”处理。只有 Load 从未运行时才返回 `ok=false`，此时 Open 跳过比较。写明 `Reload` 会刷新这份字节。`configFileName()` 改用 `LoadedConfigBytes` 的路径（或在计划中写明接受回退），并在 T3 的验证里加一条 `config get` 的 source 断言（`TestConfigCommandsUseTheRealCappPath` 已覆盖默认 home，`SUGA_CONFIG` 场景需要补）。

### PF5 [MEDIUM] T4 的文件与符号定位有误或缺漏

- disposition: `CORE_CORRECTIVE`
- 证据（同类穷尽）:
  1. chat 的 `chat.Options` 在 `internal/cli/chat.go:89`（`runChat`）构造，不在 `run.go`；`run.go:187` 只负责 `OpenCLI`。计划 :134 把“chat 的回调”放在 `run.go`，文件清单里没有 `internal/cli/chat.go`。
  2. 横幅由 `internal/chat/render.go:740` 的 `Renderer.Banner()` 从 renderer opts 组装 `BannerInfo`，再在 `chat.go:543` 调用。计划只列了 `layout.go`（`BannerInfo`）和 `chat.go`，漏了 `render.go`。
  3. 计划写“`Open`/`OpenCLI` 在 `Load` 之后比较”，但 `OpenCLI` 内部会调用 `Open`（`runtime.go:319`），两处都做就会重复比较。wiring 也不调用 `Load`，Load 在 `main` 和 `sugasdk/engine.go` 里。
  4. `AdvanceProtected(path, digest)` 需要的 `path` 从哪里来没有写；Runtime 只暴露了 `ProtectedChange()` 和 `ProtectedDigest()`。
  5. 新常量写成 `store.KindConfig`，现有约定是 `EventModel`、`EventTurnContext`、`EventSchedule` 等（`internal/personal/store/types.go:56-70`）。
- 建议: T4 的文件清单补上 `internal/cli/chat.go`、`internal/chat/render.go`；写明比较只在 `Open` 里做一次；Runtime 增加 `ProtectedConfigPath()`，或把三者合成一个结构返回；常量命名为 `EventConfig = "config"`。

### PF6 [MEDIUM] T2 让现有负向测试变成空过，测试的密封性也没有交代

- disposition: `CORE_CORRECTIVE`
- 证据:
  - 计划把检查放在 `writableConfigKey` 之后、`ParseConfigValue` 之前，测试都通过 `strings.NewReader("")` 传 stdin，属于非终端。
  - `approval_flags_test.go:130-135` 的 `TestConfigRefusesTrustTier` 只断言 `assert.Error`，改动后会因为 protected 拒绝而通过，“trust 档不可写”就不再被测试。
  - `config_test.go:192-206` 的 “invalid yaml list” 子测试断言退出码 2 且错误含 `tools.auto_allow`，而新文案里含键名，所以同样会空过。
  - 计划 :113 只泛泛写“改为会话内拒绝、终端可写”，没有要求这些负向用例注入“是终端”以保留原来的断言。
  - `setupConfigCLI`（`config_test.go:306`）没有清空 `SUGA_SESSION_ID`。如果在 suga 会话里跑测试（例如 `gofer job -a suga`），会继承非空值，“终端可写”用例会失败。
  - `newConfigLeaf` 和 `configLeafHandler` 的签名是 `func(io.Writer, *capp.Cmd) error`（`config.go:36-47`），计划没有写 stdin 怎样传到 `runConfigSet`/`runConfigUnset`。
- 建议: `setupConfigCLI` 里加 `t.Setenv("SUGA_SESSION_ID", "")`，并默认注入 `configStdinIsTerminal = func(io.Reader) bool { return true }`（用 `t.Cleanup` 还原），只有拒绝用例改成 false。在 T2 中点名 `TestConfigRefusesTrustTier` 和 invalid yaml list 子测试必须在“是终端”下断言原来的错误文案。写明 handler 改为 `func(stdin io.Reader, out io.Writer, c *capp.Cmd) error`，或用闭包捕获 stdin。

### PF7 [MEDIUM] `scripts/ptysmoke/chat_onboarding.py` 会被拒，T7 却写“确认未受影响”

- disposition: `CORE_CORRECTIVE`
- 证据: `chat_onboarding.py:199-206` 用 `subprocess.run(..., capture_output=True)` 执行 `config set/unset tools.allow_write`（protected 键）。stdin 继承自父进程，没有终端时（CI、gofer job、容器后台）会被拒，退出码 2；在交互终端里跑又会通过，结果取决于环境。这个脚本属于 `make smoke-chat`（`Makefile:146`）。T2 只泛泛要求 grep `scripts/`，T7（:164）却写“grep `scripts/` 中 `suga config set` 调用并确认未受影响”，两处互相矛盾。
- 建议: 在 T2 的文件清单里点名 `scripts/ptysmoke/chat_onboarding.py`，改成非 protected 键（例如 `tools.minimize_output`），或者显式传 `stdin=subprocess.DEVNULL` 并断言被拒。T7 改成“确认已同步修改”。

### PF8 [MEDIUM] 设计“验证要求”与 §1 有几项没有落进任务

- disposition: `CORE_CORRECTIVE`
- 证据（逐项对照设计 :227-231 与 :138）:
  1. GR110 中“注入是终端时 protected 键可写、快照只更新该键、无快照时不建”要求经 CLI 真实分发验证。T2 在 T3 之前完成，测不到快照；T3 的用例在 `internal/config`，不经 CLI 分发。没有任务覆盖这一项。
  2. “`hooks.pre_tool_use.*` 改动被报出”：T3 的用例清单没有（T2 只测了不可写）。
  3. “只有注释的文件按全部未设置”：T3 只写了“空文件”。
  4. “整文件模式（当前或快照一侧）下人工 set 不改快照，且下次仍提示”：T3 只写了“整文件模式人工 set 不改快照”，没有区分快照一侧为 `mode=file` 的情形，也没有“下次仍提示”的断言。
  5. 设计 :138 要求“计划阶段先盘点代码中全部字面配置键读取，未登记的补描述符”，计划里没有盘点结果，也没有对应任务。本评审用 grep 粗查，`gkconfig.X("…")` 字面读取中未登记的叶子只有 `hooks.pre_tool_use.{tools,name}`（计划已补）；`gkconfig.Get("hooks")`（`internal/wiring/localenv.go:188`）等整树读取没有逐一核对。
- 建议: 在 T4（或 T3 之后新增一个小任务）里加一条 CLI 分发用例：先建快照，注入“是终端”后 set 某个 protected 键，断言快照只有 `keys[<键>]` 变化；没有快照时 set 不建快照。T3 补上第 2–4 项。在“前置检查”里记录键盘点的结论和所用命令。

### PF9 [LOW] 路径规范化在 `configLockPath` 现有分支中的落法没有写

- disposition: `CORE_CORRECTIVE`
- 证据: `yaml_edit.go:102-127` 用 `filepath.Clean(path) != filepath.Clean(homeConfig)` 决定锁名是 `config.lock` 还是 hash。Windows 上如果只把一侧转小写，默认配置的锁会变成 hash 名，`TestConfigSetAllowsSimpleRuntimeKeys`（`config_test.go:158-159` 断言 `run/locks/config.lock`）在 Windows 上会失败。另外还有两个分支：`ResolveSugaHome` 出错时锁放在 `path + ".lock"`；未设 `SUGA_HOME`/`SUGA_CONFIG` 的库调用方锁放在文件旁的 `run/locks`。快照路径在这两种情形下怎么算，计划没有写。
- 建议: 写明比较两侧都用同一个 `normalizeConfigPath`；快照目录在这两个分支里与锁目录同源（同一个 `run/` 根），或者明确此时跳过快照。

### PF10 [LOW] 验证命令与文案的小处

- disposition: `CORE_CORRECTIVE`
- 证据（同类穷尽）:
  1. T1 的 `-run 'TestProtected|TestSettingsRegistry'`：`TestSettingsRegistry` 匹配不到任何现有测试（`settings_registry_test.go` 里的是 `TestReferenceConfigLeavesAreRegistered`、`TestDocumentedSimpleKeysRemainWritable`、`TestMatchConfigKey`）。
  2. T1 只点名了 `providerKeyDescriptors` 和 `providerModelKeyDescriptors`，但动态描述符共有四张表，还有 `providerModelNestedDescriptors` 和 `providerNestedDescriptors`（`settings_registry.go:341-357`）。分类表测试必须遍历四张表；另外 `KnownConfigKeys()` 只返回静态表（:361）。
  3. T2 的文案写成 `%s`，而 catalog 是 ini 格式，用 `{name}` 占位（`internal/i18n/catalog/zh/cli.ini:3`），调用方式为 `cliText(tr, key, map[string]any{...})`。
  4. T6 只 grep `docs/usage/gofer-personal-agent.md` 中的“只能写在配置文件里”。`README.md:133-134` 的英文表述 “file-only settings” 和 “(both file-only)” 同样需要更新，但不在检查范围内。
- 建议: 按上面逐条修正；T1 的命令改为 `-run 'TestProtected|TestMatchConfigKey|TestDocumentedSimpleKeysRemainWritable'`。

### PF11 [LOW] trace 的“会话”是否包括子 agent 会话没有写

- disposition: `CORE_CORRECTIVE`
- 证据: `internal/personal/subagent.go:95` 的 `child.Run` 会为子会话走同一个 `Runner.Run` 回合路径。如果 notice 回调注入到 child runner，每个子 agent 会话都会各记一条。
- 建议: 写明只在顶层会话记录，child runner 不注入该回调；或者明确子会话也记录，并在测试里固定。

### PF12 [LOW] workspace baseline 与当前状态不符

- disposition: `CORE_CORRECTIVE`
- 证据: 计划 :70 写“`git status --short` 干净”“HEAD 以计划批准时为准”。candidate 时实际有 `?? docs/design/2026-11-12-multi-command-hooks-design.md`（并行的 hooks 设计会话），HEAD 为 `109ec8c2`。plan-contract 要求 baseline 写明 HEAD 和 dirty/untracked 的归属。
- 建议: 写明 HEAD，把该 untracked 文件列为“保留的无关路径（另一会话）”。实施在 worktree 里进行，不受它影响。

## 覆盖缺口与限制

- 没有运行任何会修改仓库的命令；除 PF1 的一条 `go test` 外，没有预跑其他验证命令。
- 没有核对 goccy AST 能否区分顶层字面量点号键、锚点、内联合并键、多文档（计划 :74 声称 v1.19.2 支持 `MergeKeyType`，未验证）。
- 没有核对 `chat` 在非终端 stdin（`echo /exit | suga chat`）下是否同样调用 `render.Banner()`（设计把这一点列为风险，本轮不评）。
- 字面键盘点只用 grep 粗查了 `gkconfig.X("literal")` 形式，没有覆盖 `MapStruct` 和整树读取。
- Standards/Governance 轴（Capability Discovery、SR1409、GR107–GR110 结构）不在本轴范围。

## 结论

BLOCKED。开放的 `CORE_BLOCKING` 为 PF1（ptye2e/Windows 验证命令不可执行）和 PF2（trace 去重作用域错误，跨模块接口缺失）。另有 `CORE_CORRECTIVE` PF3–PF12，其中 PF3、PF4 为 HIGH，建议与阻断项一起修订。

## 剩余风险与后续

- 修订后的第 2 轮只需复核 T2–T5、T7 及“前置检查”的变化部分；PF3、PF4 修订时要同步更新 T3 的签名合同。
- 即使 PF1 修好，Windows ConPTY 用例仍依赖主机 gofer exec job，结果要以 job 日志为证据。
