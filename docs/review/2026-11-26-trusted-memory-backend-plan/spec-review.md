<!-- template_id: review; template_version: 1.2.0 -->
# suga 记忆可信开关实施计划 Spec/Executability 评审（第 1 轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `452845ff`；subject `docs/plans/2026-11-26-trusted-memory-backend-plan.md`；document revision Draft 0.1
- 修订版本: Draft 0.1（依据设计 `docs/design/2026-11-26-trusted-memory-backend-design.md` 0.4，已批准）
- 评审者: Spec/Executability 轴独立评审子 agent（Claude），未参与计划编写；REPORT_ONLY
- 日期: 2026-11-26
- 排除项: Standards/Governance 轴（代码组织、GR107–GR110 结构合规、ledger 与仪式成本）；设计本身的决策；实施代码

## 输入与方法

- 完整读取：review-contract payload（C0TJVugR8Kpr8ZAERo543A）、plan-contract payload（L1pSw4v7EitW46DpcPDbLQ）、`AGENTS.md`、计划（`git show 452845ff:<subject>`）与设计全文。
- 对照代码：`internal/wiring/memory.go`、`memory_runtime.go`、`main_test.go`、`origin_guard.go`、`origin_guard_test.go`、`admit_ledger_test.go`、`taint_config_test.go`、`manifest.go`、`runtime.go`（623-630、885）；`internal/personal/taint.go`、`observe.go`、`runner.go:465-479`；`internal/config/settings_registry.go`、`settings_protected_test.go`、`protected_snapshot.go`/`_test.go`；`internal/cli/config.go`、`config_test.go`、`config_protected_test.go`、`manifest.go`、`manifest_test.go`；`pkg/agentcore/tools/native/memory.go`/`_test.go`；`pkg/agentcore/memory/types.go`、`execstore/execstore.go`、`extproc/env.go`、`call.go`；`test/ptye2e/fakellm_test.go`、`fakellm_origin_test.go`、`chat_origin_judge_test.go`、`smoke_helpers_test.go`、`chat_smoke_test.go`；`internal/i18n/catalog/zh/host.ini`；`Makefile` smoke-go；主机脚本 `tmp/xhen-p1/wt-repo.ps1`、`host-merge.ps1` 参数。
- 方法：逐任务核对文件/符号/签名存在性、`-run` 命中、构建标签、R1–R8 可编译与失败路径、链路子用例能否由现有夹具构造、设计“验证要求”到任务的落点。未运行测试。

## 验收标准

- plan-contract：任务含精确文件、动作、验证、完成标准、依赖；验证可执行且唯一。
- review-contract：CORE_BLOCKING 仅限五类并附 necessity proof；计划评审中验证缺口只在某项核心验收完全没有验证手段时才阻断。
- 核心验收：计划“完成定义”1–5 与设计“验证要求”。

## 发现

### MF1 [HIGH] T2 链路测试按所写方式拿不到 `tainted_at`，“不污染”与 D5 例外无法验证

- disposition: CORE_BLOCKING `[BUG]`
- 证据:
  - 计划 T2 动作末条与验证：`TestTrustedMemoryChain` “经 `OpenCLI` …执行 `memory get` 后执行照抄正文…的 execute 调用（同 `TestExternalOriginGuardThroughAssembly` 的 stub 写法）”，五个子用例断言 `tainted_at` 有/无。
  - stub 写法是 `rt.tools.ExecuteTool(ctx, …)`（`internal/wiring/origin_guard_test.go:65-69`），`rt.tools` 是 Runner 包装前的 registry（`internal/wiring/runtime.go:885`）。
  - 置污染只在 Runner 内部加的 observe 层里发生（`internal/personal/runner.go:465-479` → `observe.go:367-372`），且 `taintMarker.observe` 要求 `observeScopeFrom(ctx).sessionID` 非空（`taint.go:80-84`），这个值只由 `withObserveScope` 在回合内设置；`api.WithSessionID` 不够。会话行也要先在 store 中存在，`PatchSessionMetaIfAbsent` 才能写入。
  - 现有 wiring 测试没有一处经真实路径观察 `tainted_at`（`TestTaintConfigLists` 只测配置解析）；T3 的 ptye2e 也不断言 `tainted_at`。
- 同类全部出现点: T2 子用例①（“无 `tainted_at`”会空过）、②③④（“有 `tainted_at`”必失败）、⑤（唯一区别断言必失败）；完成定义 1 “会话无 `tainted_at`”；可追溯性表“方案 6”“方案 7、D3、D5”两行。
- necessity proof:
  - core_acceptance: 完成定义 1（不污染会话）与 D5 例外（`memory.taint_tools: [memory]` 仍污染）。
  - necessary_causal_chain: 照计划用 stub 写法实施 → 子用例②③④⑤的 `tainted_at` 断言必然失败，T2 无法达到完成标准；执行者若改为删掉或放宽这些断言，①会空过 → 可信记忆仍把会话标为已污染的实现缺陷（例如 Trusted 只关掉了 ExternalOrigin）测不出来 → 用户读完可信记忆再写记忆时仍被要求确认，D5 的用户可见结果错误却显示全绿。
  - minimal_owner: T2 的 `internal/wiring/memory_trusted_test.go`（改测试驱动方式，不改产品代码）。
  - deletion_alternative: FAIL（删掉 `tainted_at` 断言后，D5 及其例外再没有任何验证手段）。
  - scope_effect: 无新增 Module、Interface、Schema、state、lifecycle、authority 或 external action。
- 建议: T2 改为经真实回合驱动：照 `admit_ledger_test.go` 的 `admitServer`/`openAdmitRuntime` 起 httptest 假模型（OpenCLI ChatMode 加 `rt.Asker.Bind`，或 Open 加 Approver），脚本依次返回 `memory get` → 照抄正文的 `host_exec` → 结束文本，再用 `rt.Runner.Run(...)`；按 `res.SessionID` 从 `rt.repo` 读会话 meta，断言 `tainted_at`。假模型还要应答 origin judge 请求（或在该测试里关掉 `tools.origin_judge.enabled`，避免请求真实 provider）。同时把“无提问/提问一次”的断言写成按回合计数。

### MF2 [MEDIUM] `memory.user.trusted` 在 wiring 层没有任何验证

- disposition: CORE_CORRECTIVE，user_visible_effect: true
- 证据: 完成定义 1 写明“`memory.user.trusted: true` 对 user 作用域同理”；T1 只对 `selectProjectBackend` 的四种 `Trusted` 写了用例，没有覆盖 `selectUserBackend` 置 `choice.Trusted = cfg.UserTrusted`；T2 的 `newMemoryRuntime` 回调里 `s.Kind == memory.ScopeUser → setup.User.Trusted` 分支没有用例；链路①–⑤和 `TestLoadManifest` 都只覆盖 project（manifest 只断言“user 未设 false”）。native 用例只验证回调为真时去掉标记，不验证 wiring 的映射。
- 影响: user 分支映射写错（例如读成 `setup.Project.Trusted`）时测试全绿，用户设了 `memory.user.trusted: true` 却仍弹询问。
- 建议: T1 在 `memory_test.go` 加 `selectUserBackend` 的 `Trusted` 真/假用例；T2 在链路里加 user 子用例（`memory.user.backend: exec` 加 `memory.user.trusted: true`，`memory get scope=user` → 无提问），或在 `TestLoadManifest` 加 user `trusted=true`。

### MF3 [MEDIUM] T3 屏幕断言用的文本不是 chat 实际显示的询问

- disposition: CORE_CORRECTIVE
- 证据: chat 的询问来自 `host.origin.source`，显示为“⚠ 参数里有逐字复制自外部来源的文本，来源工具：memory”（`internal/i18n/catalog/zh/host.ini:15`，`origin_guard.go:188-190`）。“参数里有逐字复制自 memory 结果的文本”只是 `reason`，用在 trace 的 danger 字段和拒绝错误里（`origin_guard.go:120-121,204,214`）。
- 同类全部出现点: T3 子用例①（不出现该文本：恒成立，空过）；②（出现该询问：在询问阶段不会出现）；R8 “子用例①失败（出现询问）”。设计“验证要求·端到端”也是同一写法，属上游文本，按代码修正即可。②还没写在询问处输入什么。
- 影响: ②照写会失败，①的屏幕检查不起作用。真正有效的检查只剩 `origin_judge` 属性。不阻断：trace 属性仍可作为核心验证手段。
- 建议: 照 `TestChatOriginJudge`，用 `执行 shell_exec？` 或 `来源工具：memory` 判断是否询问；②在询问处输入 `n`，trace 取 `denial` 事件；①取 `tool` 事件，断言 `origin_judge == nil`。

### MF4 [LOW] 设计的两条验证要求在计划里被降级或漏掉

- disposition: CORE_CORRECTIVE
- 证据:
  - 设计“端到端”写明“另在主机 chat 配 gofer 后端复现一次原场景”。T4 改为只看 `suga manifest` 显示 `trusted=true`，“不做真实模型调用”，但没有把这当作对已批准设计的偏离记进人工 Gate。gofer 后端路径也没有其他测试覆盖（所有用例都用 exec 后端）。
  - 设计“manifest：JSON 与文本输出各断言一次”。T2 只在 `TestLoadManifest` 断言结构体字段，在 `TestManifestCommand` 断言文本 `trusted=`，没有断言 JSON 键 `trusted`。
- 建议: T4 要么恢复主机 gofer 后端复现（可用真实 chat 加人工一次，或说明为何不可行），要么在“人工 Gate”中列出该偏离，请用户批准。T2 加一次 JSON 断言，例如对 `json.Marshal` 后的 manifest 断言含 `"trusted":true`。

### MF5 [LOW] 验证写法的小歧义（一次列全）

- disposition: CORE_CORRECTIVE
- 证据与建议:
  - GR110 “默认管道 stdin”：`setupConfigCLI` 会把 `configStdinIsTerminal` 强制替换为 true（`internal/cli/config_test.go:315-317`），所以管道用例必须像 `config_protected_test.go:99` 那样显式替换为 false，否则会写入成功。应在 T1 验证中写明。
  - R1 “父目录与子目录用例失败”：`strings.HasPrefix(root, item)` 只会让父目录用例失败，子目录用例仍通过；参数顺序反过来时则相反。应改为“至少父目录用例失败”，或写明参数顺序。
  - `memorigin` 第 0 步需要 fixture id，但 `fakeLLM` 没有对应字段。T3 写的是“一个分支与 Mode 注释一行”，应补上“新增一个字段”（仍在 `fakellm_test.go` 内）。
  - 链路测试的工作区：`OpenCLI` 的工作区来自 `workspace` 配置或 cwd（`internal/config/paths.go:51-62`），应写明用 `gkconfig.Set("workspace", <临时仓库>)`。worktree 子用例④写明在主仓库外或内建一个只含 `.git` 文件的目录即可（`gitRoot` 在 `.git` 文件处停止，`memory.go:379-390`）。

## 覆盖缺口与限制

已核对并成立的项：
- 符号与签名：`loadMemoryConfig`、`MemoryConfig`、`MemoryBackendChoice`、`selectProjectBackend`/`selectUserBackend`/`selectExplicitProjectBackend`、`gitRoot`、`newMemoryRuntime`、`MemoryToolOptions`、`MemoryTool.external`、`ManifestMemory`、`protectedClassification`、`protectedWriteAllowed`、`configStdinIsTerminal`、`ConfigTypeStringList` 都存在。`memory.Scope` 是可比较的 struct，`ScopeUser` 是 Kind 常量，T2 回调写法成立。
- `-run` 名都能命中现有用例（`TestLoadMemoryConfig`、`TestSelectProjectBackend`、`TestMemoryExecBackend`、`TestProtectedClassification`、`TestProtectedSnapshot`、`TestConfigProtected`、`TestMemoryToolExternalReads`、`TestLoadManifest`、`TestExternalOriginGuardThroughAssembly`、`TestTaintConfigLists`、`TestManifestCommand`、`TestLayering`、`TestChatOriginJudge`、`TestChatMemory`）。新用例名由计划指定。ptye2e 命令带 `-tags ptye2e`，新文件首行带构建标签；`make smoke-go` 跑整个包，新场景自动纳入。
- 任务顺序 T1→T2→T3→T4 成立（T2 依赖 T1 新增的 `Trusted` 与配置字段）。
- R2、R3：`TestProtectedClassification` 的 “classification matches the Protected field” 与 “every registry key is classified” 会失败；R2 同时让 GR110 会话内拒绝失败。R4、R5、R7 可编译且对应断言会失败；R6 会让②③④失败（在 MF1 修正后）。R8 会让①的 `origin_judge` 断言失败。
- 链路子用例构造：第三方仓库（列表只含另一个根）、父目录、worktree（`.git` 文件）都能用临时目录构造。假 exec 后端走 wiring 的 TestMain 分支可行：`memory.project.command: os.Args[0]`，`extproc` 只剔除凭据类环境变量（`extproc/env.go:34-45`），`SUGA_TEST_MEMEXEC` 能传给子进程。
- ptye2e：`buildSuga` 的 `go build` 模式可以直接复用到 `./test/ptye2e/testdata/fakememexec`（显式路径可构建 testdata 下的包）。`MemoryExtra`、`startChatOpts`、`traceEvents`、`findEvent`、`JudgeReply` 都存在。
- GR110 替换 `configStdinIsTerminal`：可行（包级变量，已有用例这样替换），细节见 MF5。
- 启动比较：`TestProtectedSnapshot` 的已有子用例 “a changed key is reported by name” 用的就是列表键（`tools.read_roots`），新子用例照抄即可。
- SR1301：鉴权（受保护键与会话内拒绝，复用现有机制）已核对；注入防护：不适用（fakememexec 只用于测试）；秘密：不适用；入参校验（`loadMemoryTrust`）已核对；资源归属：不适用。

限制：未运行任何测试或反向验证，结论来自读代码。Windows 下 `t.TempDir` 短路径与 `os.Getwd` 是否一致（影响 ptye2e ①的登记匹配）没有核实，留作实施风险。

## 结论

BLOCKED：MF1 是开放的 `CORE_BLOCKING`（BUG）。T2 链路测试按所写的 stub 方式拿不到 `tainted_at`，“不污染”与 D5 例外的核心验收没有可执行的验证手段。MF2–MF5 为 CORE_CORRECTIVE，其中 MF2 标 `user_visible_effect: true`。

## 剩余风险与后续

- 修订 T2 的链路测试驱动方式（MF1）后，第 2 轮只需复核 T2，以及可追溯性表中相关的行。
- MF2–MF5 建议在同一修订中一起改。
- Windows 路径形式（短名、大小写）在 ptye2e 中的匹配风险，由 T3/T4 的主机 Windows 运行暴露。
