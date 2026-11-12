<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令 实施计划 评审（第 1 轮 · Spec/Executability 轴）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `1b7478369019c5590a760bd3739af00944a531b2`；subject path `docs/plans/2026-11-12-multi-command-hooks-plan.md`；document revision Draft 0.1（实施已批准设计 `docs/design/2026-11-12-multi-command-hooks-design.md` 0.6）
- 修订版本: Draft 0.1
- 评审者: 独立评审子 agent（Spec/Executability 轴），REPORT_ONLY，未改仓库、未提交
- 日期: 2026-11-12
- 排除项: 设计选择本身（D1–D13、R0–R3 规则内容）；Standards/Governance 轴（模板、validator、结构规则）；gofer 仓库只读

## 输入与方法

完整读取：semantic payload `tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md`（含 plan-contract、planning-core-rigorous、plan-closure）；review-contract 与 review 模板（`tmp/idev-std/d/8Yz1eltnU9GuyZy8zAtfgA/payload.md`）；suga `AGENTS.md`；计划全文与设计全文。

对照代码（main @ `1b747836`）：`internal/hooks/` 全部（`hooks.go`、`hooks_test.go`、`hooks_pretool_test.go`、`hooks_env_test.go`、`main_test.go`）；`internal/chat/hooks_record.go`、`hooks_gate.go`、`stop_background.go`、`chat.go:215-235,725-835`、`hooks_record_test.go`、`stop_background_test.go`、`chat_hooks_config_test.go`；`internal/cli/chat.go`、`internal/cli/config.go`；`internal/wiring/localenv.go:184-198`；`internal/config/settings_registry.go`（注册表、`ValidateConfigKey`、`dispatchConfigReader`）、`settings_registry_readers.go`、`config_candidate.go`、`mcp.go`；`test/ptye2e/chat_observe_test.go`、`smoke_helpers_test.go`、`Makefile`；`pkg/agentcore/tasks/tasks.go`；`v0.55.0:internal/cli/config.go`。hyj7 分支 `feat/protected-config-keys`（tip `5d0685ff`）：`settings_protected_test.go`、`settings_registry.go`（`ConfigKey.Protected`）、`internal/cli/config.go`（`protectedWriteAllowed`、`formatConfigValue`）、`config_test.go:setupConfigCLI`、`config_protected_test.go`、README/CHANGELOG/`docs/usage/gofer-personal-agent.md` 差异。gofer：`internal/hookrelay/run.go`、`internal/commands/hook.go`、`internal/httpapi/session_handler.go`（只读）。

方法：逐任务核对文件/符号/行号/签名是否存在；`-run` 正则对现有与新增测试名；反向验证在当前代码下的实际结果；T5 状态机在 chat 真实包装链（`recordingHooks`/`recordingWait`）下的行为；测试钩子能否无 sleep 确定性复现三种打断窗口；设计“验证要求”逐条落位。用一个临时 Go 片段验证了 `fmt.Sprint` 对 YAML 列表的输出格式（已删除）。

## 验收标准

- plan-contract / plan-closure：每任务有精确文件、具体动作、可执行验证与可观察完成标准，可被只读计划与设计的实施者唯一、正确执行；被其他任务调用的函数写出签名；反向验证可编译且预期正确。
- 设计 0.6 “验证要求”全部追溯到任务与验证。
- review-contract：`CORE_BLOCKING` 仅限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 并附 necessity proof；同类穷尽。

## 发现

### HPF1 [BLOCKER] T5 打断与结果交回依赖读取 `Done` 通道，与 chat 的 `recordingWait` 转发协程冲突（CORE_BLOCKING · BUG）

- 证据: 计划 `:149` “再写入 `Done`（缓冲 1）”（未写 close）；`:150` 打断目标“`finished` 且结果未被 `Done` 读出 → `last`”、“等待 `Done` 结果”、“`finished` 未消费时取该结果中 block 的 reason”。chat 中 `hooks` 总被 `recordHooks` 包装（`internal/chat/chat.go:227-230`），`recordingHooks.StartStop` 起协程立即 `<-inner.Done()` 并转存（`internal/chat/hooks_record.go:44-58`）；现有测试明确依赖“the forwarder took it”（`hooks_record_test.go:66-68`）。现有单条目实现靠“写一次后 `close(done)`”（`hooks.go:327-328`）让 `stopWait.Interrupt` 的 `<-w.done` 在值被转发协程取走后仍立即返回，再由 `recordingWait.Interrupt` 补取（`hooks_record.go:112-125`）。chat “消费”指读 `recordingWait.out`（`chat.go:783-791`），pipeline 自身看不到。
- 影响: 必要性证明——按计划字面实现：(a) 在真实 chat 路径中，流水线全部结束后转发协程已读走 `Done`，pipeline 判定为“已读出”，打断时不向最后执行的条目发 Interrupt，违反设计 §4 打断第 2 条（HF15，“与单条目现状一致”）；(b) `Interrupt` 若以 `<-done` 等待且未 close，转发协程先取走值时 `Interrupt` 永久阻塞，chat 卡死在输入/投递/退出路径。T5 单元测试直接调 pipeline、无包装，两种错误都会“通过”；T5 `:152` 的“`recordingWait` 不重复取”在 `internal/hooks` 包内无法构造（`recordingWait` 在 `internal/chat`），T6 也未列 pipeline+包装下的打断用例；T7 只覆盖 `running` 态，(b) 只能概率性暴露。结果错误且实现不唯一。
- 建议: T5 写定：`finished` 态（无论通道是否被读）一律以 `last` 为目标；`Done` 只写一次后 `close`；`Interrupt` 等待内部完成信号（不从 `Done` 取值），reason 从内部记录的逐条结果计算。T6 增加经 `recordHooks` 包装的打断用例（running / finished 各一，断言 Interrupt 去向、reason 只交回一次、不挂起），并把 `:152` 中“`recordingWait` 不重复取”的断言移到 T6。同类穷尽：计划 `:149`、`:150`（三处）、`:152`（`finished 未消费`、`recordingWait 不重复取` 两处）。

### HPF2 [HIGH] T5 测试钩子缺少打断侧同步点，“运行中子态”与“两条目之间”无法不靠 sleep 确定性复现（CORE_BLOCKING · BUG）

- 证据: 计划 `:151` 只有 `pointEntryExited`、`pointBeforeStartNext`、`pointBeforeDone` 三个点，都在流水线协程内；`:150` `Interrupt` 取锁冻结后会等待该条结束/`Done`。测试若在 `pointEntryExited` 或 `pointBeforeStartNext` 处阻塞流水线协程，再在另一协程调 `Interrupt`，必须在 `Interrupt` 已冻结之后才放行，否则 `Interrupt` 永等；而 `between` 态不发任何 Interrupt 进程，测试没有可观察信号判断冻结已发生。`:151` 也未写各点是否在持锁时调用（`pointBeforeDone` 相对“状态置 `finished`”的先后未定）。
- 影响: 必要性证明——放行早于冻结时，`running` 子态用例实际走到 `finished` 路径（目标同为该条、reason 同样交回一次），断言照样通过，HF28 代码路径未被证明；`between` 用例放行过早则下一条已启动，用例随机失败。实施者只能用 sleep 或自行发明内部轮询，验证不唯一；设计验证要求“经测试钩子在临界区之间注入打断”（设计 `:298`）无法按计划唯一落地。
- 建议: 增加打断侧钩子点（如 `pointInterruptFrozen`：取锁冻结并选定目标、出锁后调用），测试在该点收到信号后再放行流水线协程；写明每个点“不持锁调用”及 `pointBeforeDone` 位于“状态置 `finished`”之后；给出三种窗口的用例编排（谁阻塞、谁放行、以什么信号）。同类穷尽：`running` 子态（`:152` HF28 用例）、`between`（`:152` HF3/HF25 用例）；`finished` 与 HF29 两个用例只需钩子+父 ctx 取消，可确定性复现，不在本项内。

### HPF3 [HIGH] T2 反向验证的预期与代码不符，按 fail-closed 会在 T2 停下（CORE_BLOCKING · BUG）

- 证据: 计划 `:115` “临时去掉 `case "hooks"`，确认非法值用例失败”。去掉后 `dispatchConfigReader("hooks")` 走 `default` 返回 `unknown config reader "hooks"`（`internal/config/settings_registry.go:732-733`），经 `ValidateConfigKey`→`ValidateConfigCandidate` 包成 `ConfigValidationError`，退出码 2、文件不写（`config_candidate.go:17-19,41-43`）。计划对非法值用例只要求“退出码 2、文件字节不变”，该用例照样通过；失败的是合法值用例。计划 `:79` 规定反向验证不符合预期即停止。
- 影响: 必要性证明——照计划执行，反向验证结果与写定预期相反，实施者要么按 fail-closed 停在 T2，要么自行改预期/断言，执行不唯一。
- 建议: 非法值用例同时断言错误文本含 `hooks.commands[0].command`；或把反向验证改为“去掉 `ReadHookEntries` 的某项检查”；或把预期改为“合法值用例失败”。同类穷尽：计划内其余反向验证（T3 `:127`、T4 `:139`、T5 `:152` ①②③、T6 `:163`、T7 `:173`）按当前代码推演预期成立，仅本处不成立。

### HPF4 [HIGH] `config get hooks.commands` 不输出 YAML，T2 断言必然失败，且无任务负责设计要求的输出格式（CORE_BLOCKING · MISSING_FUNCTION）

- 证据: 设计 §5（`:235`）“`suga config get hooks.commands` 输出 YAML”；计划 `:115` 断言输出含 `timeout_ms: 10000`。`ReadConfigValue` 对该键走 `effectiveConfigValue` 的 `default` 返回原始值（`settings_registry_readers.go` `default: lookupRawConfig`），`formatConfigValue` 对 `[]any` 落到 `fmt.Sprint`（`internal/cli/config.go:260-280`；hyj7 版 `:292-312` 相同），实测得 `[map[command:[x] timeout_ms:10000]]`（无空格）。计划范围只允许改 `internal/cli` 测试（`:33`），T2 文件不含 `internal/cli/config.go`。
- 影响: 必要性证明——T2 验证照写不能通过；满足设计需改 `internal/cli/config.go`（超出 T2 文件与计划范围），否则只能改断言而放弃设计要求，两条路结果不同，属验收要求的功能缺失且实现不唯一。
- 建议: T2 文件与范围加入 `internal/cli/config.go`，仿 `[]appconfig.PreferredTool` 的 YAML 分支（`config.go:273-277`）为 `hooks.commands` 输出 YAML，断言写成具体输出（同时覆盖 `config set` 成功后的回显，它也走 `writeConfigValue`）。同类穷尽：`config get`（`:115`）与 `config set` 成功回显（`runConfigSet` 末尾 `writeConfigValue`）两处输出。

### HPF5 [HIGH] 设计“验证要求”有三项未落到任务（CORE_CORRECTIVE）

- 证据: (1) 设计 `:294` 兼容项要求“`stop_background: true` + `stop_args` 无 `stop_wait_sec`”的现有配置照常启动且 `hooks.New` 返回现有单条目实现；`internal` 下除 `settings_registry_test.go:44` 外无任何测试设 `hooks.stop_background`，T3 `:127` 未新增；可追溯性 `:216` 只写“现有测试”。(2) 设计 `:299` 后台 Stop 正例“R1 block 变为任务通知”：T6 `:163` 只列被杀/超时/Close 三个反例，现有 `TestBackgroundStop` 用桩不经 pipeline。(3) 设计 `:303` ptye2e 反向验证含“临时去掉 R0”：T7 `:173` 只列两项且未说明移到何处（T7 场景中 R0 被去掉也无可观察差异，实际由 T6 `:163` 承担）。
- 影响: AC3 与后台通知路径缺少直接证据；可追溯性表把未覆盖项标为已覆盖。
- 建议: T3 `TestNewHookEntries` 增加 stop_background+stop_args 用例（断言非 nil、`*commandClient`、`stopTimeout==backgroundStopTimeout`）；T6 增加两条新条目 A block 时任务通知 `Stop hook asks to continue: <reason>` 与 `takeBlocked()` 为真；T7 写明 R0 反向验证由 T6 承担及理由。

### HPF6 [MEDIUM] T3/T4/T5 对 `hooks.go`、`pipeline.go` 的文件归属与 `newPipeline` 签名不闭合（CORE_CORRECTIVE）

- 证据: T3 `:125` 要求返回 `newPipeline(compat, entries)` 并“先放只含构造与委托的骨架，使编译通过”，但 T3 文件（`:121`）只有 `hooks.go` 与测试，`pipeline.go` 到 T4 `:133` 才“新建”；`newPipeline` 参数类型（`[]HookEntry` 还是 `[]*commandClient`）未写，而 T3 标“签名为合同”。T5 `:147` 要求 `compatWaiting` “在 `New` 中算好”，T5 文件（`:145`）不含 `hooks.go`；T4 `:135` 的 `pipeline` 结构无该字段。T4 `:138` 的临时 `StartStop` “T5 完成前不合入”，与“每任务一个 job、一个或多个提交”（`:75`）的提交粒度关系未写。
- 影响: 顺序单执行者可自行化解，但任务书按“只改本任务列出的文件”（`:75`）下发时会互相越界。
- 建议: T3 文件加 `internal/hooks/pipeline.go`（骨架），写出 `func newPipeline(compat *commandClient, entries []*commandClient, compatWaiting bool) *pipeline`（或等价）；T5 文件加 `hooks.go`，或改为在 `newPipeline` 参数中传入；写明 T4 的临时 `StartStop` 可提交到 worktree 分支、合并前由 T5 替换。

### HPF7 [MEDIUM] 各任务所需的按条目区分行为的假 hook 进程未规定（CORE_CORRECTIVE）

- 证据: `internal/hooks` 的 `TestFakeHookProcess` 以全局环境变量控制 sleep/stdout，且总是 `os.Exit(0)`（`hooks_test.go:27-49`）；`internal/chat` 的同名假进程只记日志、无输出、无 sleep（`chat_hooks_config_test.go:26-41`）。T4 `:139`（条目 1 睡 1s 被截断、条目 2 照常返回）、T5 `:152`（A block + B 非零退出；等待条目长睡）、T6 `:163`（A 已 block、B 运行中时再发 Stop）都需要按 argv 区分行为、非零退出码，以及“B 已启动”的同步信号；计划只写“复用……模式”。
- 影响: 实施者需自行设计；若用 sleep 判断“B 运行中”会引入不稳定。
- 建议: 规定新假进程（如 `pipeline_test.go` 的 `TestPipelineHookProcess`，chat 侧 `TestMultiHookProcess`）：按 argv 标签选 block/fail(exit 1)/sleep/silent，启动即写日志；T6 以轮询日志出现 B 的 Stop 行（带期限）作为“运行中”同步点，不用固定 sleep。

### HPF8 [MEDIUM] T1 能在只读前提下完成，但引用了不存在的命令、漏列决定性代码位置，且分支②之后的去向未写（CORE_CORRECTIVE）

- 证据: `:97`/`:102` 要核对 v0.55.0 的 `suga config validate`，该版本 `config` 只有 get/set/unset/list/path（`v0.55.0:internal/cli/config.go:27-31`）；旧版本对 `hooks.commands` 的表现应查 `config get`（`MatchConfigKey` 报 `unknown configuration key`，`v0.55.0:internal/config/settings_registry.go:379`）、`config list`（只收注册键）与 chat 启动（`hooks.New` 不读该键）。gofer 侧决定性代码不在 `:97` 列表：未设 `--wait` 时 `Options.Wait` 回落 540s（`hookrelay/run.go:167,393-395`），是否打开 turn 由服务端 `WaitReason` 决定（`run.go:594,642`；`internal/httpapi/session_handler.go:249-265`），Stop 被杀时只有 SIGINT/SIGTERM/SIGHUP 会触发 `ReportInterrupt`（`internal/commands/hook.go:158,284-299`），而 suga 用 `exec.CommandContext` 超时结束进程（Unix 为 SIGKILL、Windows 为 TerminateProcess）。T2 依赖写“T1 分支①”（`:117`），分支②获确认后能否继续 T2 未写。
- 影响: 实施者按原文找不到 `config validate`；只看 `run.go` 难以下结论。按上述代码初读，未设 `--wait` 的 gofer 在 relay 等待时仍会打开 turn，分支②的概率较高（仅为证据提示，不代替 T1 结论）。
- 建议: T1 文件列表补 `internal/commands/hook.go`、`internal/httpapi/session_handler.go`，把 `config validate` 改为 `config get`/`config list`/chat 启动；T2 依赖改为“T1 分支①，或分支②已获确认”。

### HPF9 [MEDIUM] T8 文档目标与 hyj7 合入后的文档不对齐（CORE_CORRECTIVE）

- 证据: `:179` 写 “`README.md`（hooks 一节）”，README 无 hooks 一节（仅 `:117` 列 `hooks.*`）；hyj7 合入后 README 新增 “Protected settings” 执行类清单，`docs/usage/gofer-personal-agent.md` 新增“安全相关配置”执行类清单（hyj7 版 `:140`），CHANGELOG「未发布」也列分组——三处都应加入 `hooks.commands`，T8 未提，且 `:181` 的 grep 验证在漏改清单时照样通过。`:179` 的行号 `:212-213`、`:390-402` 在 hyj7 合入后约移到 `:229-230`、`:405-419`。前置条件 `:73` 用 `22482244`（hyj7 T5）判定合入，而这些文档来自 hyj7 tip `5d0685ff`（T6）。
- 影响: protected 清单与实际注册表不一致；实施者对 README 落点无唯一选择。
- 建议: T8 明确落点：README `:117` 附近与 “Protected settings” exec 列表、gofer-personal-agent.md 的 hooks 条目与“安全相关配置”执行类、`skills/suga-guide/references/config.md` `hooks.*` 表（hyj7 版 `:259-271`）；验证增加对两处执行类清单含 `hooks.commands` 的 grep；前置条件改为“hyj7 最终合并提交（含 T6 文档）已在 main”，行号改为合入后重新定位。

### HPF10 [LOW] `between` 态冻结路径的 `Err` 未定（CORE_CORRECTIVE）

- 证据: `:149` “`frozen` 则停止……`Done` 以 R0 结束”，R0 定义为“ctx 已取消 → `Err=ctx.Err()`”；`:150` `Interrupt` 先冻结、发 Interrupt、再取消 ctx，流水线协程可能在冻结后、取消前取锁，此时 `ctx.Err()` 为 nil。
- 影响: 合并结果可能成为 `Err=nil` 的空结果（R3 形状），与“以 R0 结束”和设计 R0 的 `errors.Is(..., context.Canceled)` 不一致。
- 建议: 写定冻结路径产出 `Err=context.Canceled`（或先等 ctx 取消再生成结果）。

### HPF11 [LOW] `ReadHookEntries` 的输入归一化未写全（CORE_CORRECTIVE）

- 证据: `:112` 只写“不是列表 → 错误”“接受 YAML int 与可无损转换的数字”。`gkconfig.Get` 可能给出 `[]any` 或 `[]map[string]any`（测试用 `gkconfig.Set` 时，先例 `cli_mcp_test.go:101`；`ReadMCPServerSettings` 两种都接受，`mcp.go:17-30`），数字可能是 int/uint64/float64，`command` 可能是 `[]string` 或 `[]any`。plan-contract 要求写出输入归一化顺序。
- 影响: 测试与实现各自选择类型时可能互不匹配。
- 建议: 写明接受的容器与数字类型，顺序同 `ReadMCPServerSettings`。

### HPF12 [LOW] 执行者模型选择与工作区既有约定可能冲突（OUT_OF_SCOPE）

- 证据: `:75` 指定 `opencode-go/deepseek-v4.1-flash`；调用方会话的工作区记忆写有“suga 模型选择：10-28 起优先 codex gpt-6.1-terra / gpt-6-sol；oc-go 只作补充”。
- 影响: 不影响计划可执行性；属调用方的派发约定。
- 建议: 由计划作者按 SR1413 核对，不在本轴处置。

## 覆盖缺口与限制

- 未运行任何测试或构建；结论来自代码阅读，外加一个临时 `fmt.Sprint` 片段。
- hyj7 只读分支 tip `5d0685ff`；它合入 main 时若再变动，T2/T8 的行号与文件需按合入结果复核。
- gofer 服务端 `WaitReason` 的计算细节只看到字段定义与使用点，未追到全部判定分支（属 T1 的工作）。
- 未评 Standards/Governance 轴（模板/validator/结构规则）及设计选择本身。
- 同类穷尽已在各条列出；对 `-run` 正则逐一核对：T3/T6 引用的现有测试名全部存在且能匹配（`TestStopInterrupt` 匹配两条现有用例），T2 的 `TestProtectedClassification` 在 hyj7 合入后存在，T7/T9 的 ptye2e 命令带 `-tags ptye2e`。`internal/hooks → internal/config`（包名 `axconfig`，惯用别名 `appconfig`）无循环、无分层守卫冲突；`Response` 加切片字段后，仓库内无 `==` 比较或整值 `assert.Eq`，只有计划已列的 `hooks_test.go:164`、`:293` 两处。

## 结论

BLOCKED。开放 `CORE_BLOCKING`：HPF1（BUG）、HPF2（BUG）、HPF3（BUG）、HPF4（MISSING_FUNCTION）。HPF5–HPF11 为 `CORE_CORRECTIVE`，HPF12 为 `OUT_OF_SCOPE`。

## 剩余风险与后续

- 修订后第 2 轮按 `git diff 1b747836..<新 candidate> -- docs/plans/2026-11-12-multi-command-hooks-plan.md` 复核 T2、T5、T6、T7 与可追溯性表。
- HPF1 的修正会改变 T5 的状态机描述与 T6 的验证范围，属于计划内修订，不改设计（设计 §4 已要求 finished 态发给最后执行的条目）。
- HPF4 需要扩大 T2 的文件范围（`internal/cli/config.go`），属于计划语义变化，需随修订重新提交计划批准。
