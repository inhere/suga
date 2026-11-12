<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护 实施计划 评审（Standards/Governance 轴，第 1 轮 discovery）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `109ec8c21158ff9238203ebba2ac7e9cd12a767d`；subject path `docs/plans/2026-11-12-protected-config-keys-plan.md`
- 修订版本: Draft 0.1
- 评审者: 独立评审子 agent（Standards/Governance 轴，与 Spec/Executability 轴隔离；REPORT_ONLY）
- 日期: 2026-11-12
- 排除项: 不评审已批准设计 0.8 的决策本身；不改仓库、不提交；不做 Spec/Executability 轴的全面可执行性评审（仅记录在核对规范时直接撞到的执行性缺陷）。

## 输入与方法

- 规范：IDEV-STD semantic payload `tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md`（plan-contract、plan-closure、planning-core-rigorous、plan 模板 1.3.1、SR1403/SR1407/SR1409、GR101–GR110、SR2001–SR2004）；review-contract 与 review 模板 1.2.0（经 delta 链 `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/delta-IW44zeUgd7-QD1S0dii4kw.md` 取得，hash 与 active 列表一致）；SR1301 取自 `rules-security.md`（sha256:6c81e07…）；SR1402.1/SR1433 文本；仓库 `AGENTS.md`。
- 被评对象：`git show 109ec8c:<plan>` 全文、`git show 109ec8c:docs/design/2026-11-11-protected-config-keys-design.md`（Draft 0.8）全文。
- 核实代码事实（只读）：`internal/config/yaml_edit.go`、`internal/cli/config.go`、`internal/cli/acp.go`、`pkg/sugasdk/session.go`、`internal/wiring/runtime.go`、`internal/personal/runner.go`、`internal/personal/store/types.go`、`internal/i18n/`、`Makefile`、`.github/workflows/go.yml`、`test/ptye2e/*`、全部 `config set/unset` 测试调用、gkconfig 字面键读取盘点、当前 HEAD 与并行设计 `b11cd717`。

## 验收标准

- plan-contract 必需内容逐项；plan-closure（task 五要素、Capability Discovery 三表 grammar、唯一 `host_or_non_offline_action`、workspace baseline）。
- 设计 D1–D10 与“验证要求”逐条落到任务与验证；新增/变更接口写出签名、返回、错误与文案归属。
- 规则：SR1301、SR1403、SR1407、SR1409、GR104、GR107–GR110、SR2002–SR2004；AGENTS.md 验证要求（ptye2e、反向验证、Windows、ACP 真实验证）。

### 必需内容核对总表

| 项 | 结论 |
|---|---|
| 模板首行 / 状态 `Draft 0.1` / 修订记录 | 通过 |
| 思考声明（RIGOROUS、核心目标、scope freeze、DEFER_OR_REQUEST、budget、停止条件） | 通过 |
| 暴露度 SR1409（高，`v0.55.0` tag 已核实存在） | 通过 |
| 目标与完成定义、范围/排除/授权 | 通过（授权中的 host 动作描述不完整，见 PS8） |
| 唯一 `host_or_non_offline_action=REQUIRED` | 结构通过；适用范围描述不完整（PS8） |
| 输入与批准证据（设计 0.8、批准原话、评审目录） | 通过 |
| Capability Discovery 三表 grammar（CAP-01..04、NONE sentinel） | 结构通过；CAP-03 与 T2 不一致（PS14） |
| workspace baseline | 不完整（PS10） |
| 前置与 fail-closed | 通过（测试环境隔离缺 `SUGA_SESSION_ID`，PS12） |
| 波次与依赖 | 通过 |
| 每任务 文件/动作/验证(含运行目录)/完成标准/依赖 | 结构齐全；验证命令缺陷见 PS1、PS9、PS15 |
| 回滚、人工 Gate | 通过 |
| 可追溯性 | 不完整（PS5） |
| 完成 Gate 与 ≤8 行完成总结 | 通过 |

### 规则核对

- SR1301：鉴权（protected 键 CLI 写入只接受会话外终端）——已核对，T2 落实；注入防护——已核对，不拼接命令，不适用新增风险；秘密不进日志/输出/仓库——已核对，快照只存摘要、提示只含键名、trace attrs 不含值（T3/T4 写明）；入参校验——已核对，沿用 `writableConfigKey`；资源归属——不适用。未发现 SECURITY 违反。
- SR1403：无新 Module，快照放 `internal/config` 新文件，符合“既有 owner 扩展”。通过。
- SR1407：任务内含包级/全量检查，见 PS9。
- SR1409：高暴露、两轴 + ledger，声明有 tag 证据。通过。
- GR104 / AGENTS 1000 行：见 PS13。
- GR107：不改 `cmd/suga/main.go`。通过。
- GR108：wiring 只调比较函数；但计划把进程缓存与会话去重放在 wiring/Runtime 内，见 PS2 建议。
- GR109：写后回调与快照写失败记 Warn 已写；chat 推进 `AdvanceProtected` 的错误处理未写，见 PS6。
- GR110：T2 经 root 真实分发。通过（快照断言缺失见 PS3）。
- SR2002 / AGENTS 测试写法：计划未提断言库与 `t.Run` 约定，见 PS16。
- SR2003/SR2004：suga 用 capp，沿用现有 `newConfigCommand` 结构；布局符合参照。CAP-03 包级可变变量见 PS14。
- AGENTS 验证：反向验证 T1–T5 均有（通过）；`make smoke-chat`/`smoke-go` 在 T7（通过）；ptye2e 命令缺 build tag（PS1）；Windows 主机 job 有（PS1、PS3e）；ACP 真实验证缺（PS11）。

## 发现

### PS1 [BLOCKER] ptye2e 验证命令缺 `-tags ptye2e`，场景测试与 Windows ConPTY 验证会零用例空过

- disposition: CORE_BLOCKING [BUG]
- 证据: `test/ptye2e/*.go` 全部带 `//go:build ptye2e`（如 `chat_observe_test.go:1`）；`Makefile:169-170` `smoke-go` 用 `go test -tags ptye2e …`。计划同类全部出现点：T5 验证（计划第 149 行）`go test ./test/ptye2e -run 'TestChatProtectedConfig' -count=1`；T7 动作（第 164 行）Windows `go test ./test/ptye2e -run TestChatProtectedConfig`。`make smoke-go` 本身（第 164 行前半）不受影响。
- 影响: 两条命令在无 tag 时编译不到任何测试文件，返回成功而不执行；T5 的反向验证（“去掉横幅确认用例失败”）同样不会失败，会被误记为通过。设计“验证要求”中 chat 横幅 ptye2e 与“主机 Windows ConPTY”两项因此没有证据。necessity proof：这两项是设计列明的验收（设计第 230-231 行），按计划原文执行必然得到空证据 → 结果错误。
- 建议: 两处改为 `go test -tags ptye2e -run 'TestChatProtectedConfig' -count=1 ./test/ptye2e/`；完成标准加“输出含 `--- PASS: TestChatProtectedConfig`/`ok` 且非 `no tests to run`”；反向验证同样检查用例确实运行。

### PS2 [HIGH] trace 去重机制写为“runtime 内”，与 ACP 每个 prompt 新建 runtime 冲突，验收无法达成

- disposition: CORE_BLOCKING [BUG]
- 证据: 计划 T4 第 139 行“按会话 ID 在 runtime 内去重，使 ACP 同一会话多个 prompt 只记一条”；`internal/cli/acp.go:84` 每个 prompt 回调里调用 `wiring.Open` 新建 runtime；设计第 171 行“ACP 每个 prompt 新建 runtime，按会话 ID 去重”。
- 影响: 按计划字面实现（Runtime 实例字段去重），ACP 同一会话每个 prompt 都记一条 trace，T4 验收“ACP 同会话两次 prompt 只有一条 trace”失败或被实施者自行改机制（实现不唯一）。necessity proof：计划规定的机制与其自身验收在现有代码结构下互相矛盾。
- 建议: 写定去重为进程级、按（规范化配置路径, session ID）的集合，与比较结果缓存同 owner（建议放 `internal/config` 或 personal 包级，带 `ResetProtectedCheckForTest` 一并清空），wiring 只传递（GR108）；写明 runtime 关闭后不清除。

### PS3 [MEDIUM] 设计“验证要求”有若干条未落到任务验证（同类穷尽）

- disposition: CORE_CORRECTIVE
- 证据（设计第 227-231 行 对照 计划 T2/T3/T5/T7）:
  a) GR110 项“注入‘是终端’时 protected 键可写且快照只更新该键、无快照时不建”——T2 用例（第 112 行）只断言可写，无快照断言；T3 用例在 `internal/config` 直接调用，不经 CLI 分发；且 T2 先于 T3 合入，T2 时回调尚不存在，之后无任务补 CLI 层快照断言。
  b) “`hooks.pre_tool_use.*` 改动被报出”——T3 列表无此项（T2 只测不可写）。
  c) “只有注释的文件按全部未设置”——T3 只列“空文件”。
  d) “整文件模式（当前或快照一侧）下人工 set 不改快照且下次仍提示”——T3 只列“整文件模式人工 set 不改快照”，缺“快照侧为 file 模式”与“下次仍提示”。
  e) “主机 Windows：ConPTY 下真实二进制…管道输入被拒”——T5/T7 只有终端内 set 成功，无管道输入被拒。
- 影响: 完成定义第 4 条“验证要求全部有对应测试”无法从计划核实；漏项可能在实施中被省略。
- 建议: T4 或 T3 之后加 CLI 分发层用例覆盖 a；T3 用例清单补 b、c、d；T5 增“`echo x | suga config set tools.read_roots …` 退出码 2”并由 T7 在 Windows 跑。

### PS4 [MEDIUM] 设计要求“计划阶段盘点全部字面配置键读取”未在计划中执行或记录

- disposition: CORE_CORRECTIVE
- 证据: 设计第 138 行与第 43 行（S18 处理）“计划阶段盘点全部字面键读取，未登记的补描述符”；计划无此盘点结果，也无对应任务。评审者只读抽查 `gkconfig.(Get|String|Bool|…)("…")` 字面读取 93 个，未在 `settings_registry.go` 出现的仅：`hooks.pre_tool_use.name`（计划已补）与父级整块读取 `hooks`（`internal/wiring/localenv.go:188`，只用于展示）、`llm`（`internal/config/providers.go:603`，旧配置拒绝）、`tools.approval_rules`、`tools.snapshots`、`tools.spill`；非 gkconfig 读取路径未抽查。
- 影响: 分类表测试只覆盖已注册键，未登记的字面读取会绕过 D1 的“防漏标”。
- 建议: 在“前置检查”或 T1 写入盘点方法与结论（含上述父级读取的处理理由），并让 T1 的完成标准包含“盘点结论已记录”。

### PS5 [MEDIUM] 可追溯性不完整：D2 未映射，D9/替代声明映射不全

- disposition: CORE_CORRECTIVE
- 证据: 计划第 183-190 行表格无 D2；D9（chat 推进、run 不推进、人工 set 只更新该键）涉及 T2/T3/T4，只映射到 T3；“替代声明”的测试改写在 T2，只映射 T1、T6；“盘点字面键”与“文档”的设计条目未映射。
- 影响: 不满足 plan-contract“exact 可追溯性”；无法据表确认每条决策有验证。
- 建议: 逐条 D1–D10 + 验证要求子项 → 任务 → 具体测试名/命令，一行一项。

### PS6 [MEDIUM] 新增/变更接口的签名、返回、错误处理未写全

- disposition: CORE_CORRECTIVE
- 证据（同类穷尽）:
  - `updateProtectedKeyLocked(path, key, newBytes)`（第 126 行）无参数类型与返回；unset 时传什么未定；调用时点“写入成功、仍持锁”未区分 `atomicWriteConfig` 之后与读回校验之后——`yaml_edit.go:160-167` 读回校验失败会回滚，若回调在其前，快照会记录已被回滚的值。
  - `LoadedConfigBytes() (path, data, ok)`（第 127 行）：默认文件不存在时 ok/data 取值、path 是否已规范化未写。
  - `Runtime.ProtectedDigest()` 返回类型未写；`ResetProtectedCheckForTest()` 所在包未写。
  - `OnBannerShown` → `AdvanceProtected` 失败的处理（GR109 要求 Warn 并注明理由）未写；run 输出 stderr 写失败处理未写。
  - trace 常量名 `KindConfig`（第 134 行）与现有约定 `EventXxx`（`internal/personal/store/types.go:56-79`，如 `EventTurnContext`）不一致。
- 影响: 子 agent 实施时自行选择，易出现回滚不一致或吞错（GR109）。
- 建议: 补齐上述签名与错误处理；回调明确放在读回校验成功之后、返回之前；常量改为 `EventConfig = "config"`。

### PS7 [MEDIUM] 文案归属与格式不完整

- disposition: CORE_CORRECTIVE
- 证据: T2 文案用 `%s` 占位（第 111 行），而 catalog 用 `{name}` 具名占位（`internal/i18n/catalog/en/cli.ini:3`，`i18n.go:158` `T(key, map)`）；未给 catalog 键名与所在 ini 文件、en 文本；T4 横幅/stderr 文案只写“设计 §3 文案”，`WholeFile`、`SnapshotUnreadable` 写“用对应文案”，设计只给了 zh 句子、无“快照无法读取”完整句与 en 文本；现有 `configInputError` 消息为英文非 i18n，拒绝文案如何经 `cliText` 获取未写。
- 影响: 文案归属不唯一，`chat_i18n` 冒烟（en）可能缺键告警。
- 建议: 列出键名（如 `cli.config.protected_refused`、`cli.protected.changed`、`.whole_file`、`.snapshot_unreadable`）、文件、zh/en 全文与占位名；写明经 `cliText(cliTranslator(), …)` 取文案。

### PS8 [MEDIUM] `host_or_non_offline_action` 的适用描述漏列主机与联网动作

- disposition: CORE_CORRECTIVE
- 证据: 第 35 行只写“T7 经 gofer exec job 在主机跑 Windows 测试；只写 `tmp/`，不改业务工作区”。但计划另含：主机 `tmp/xhen-p1/wt-repo.ps1` 建 worktree（第 71 行，改仓库 worktree 登记）；T7 完成标准用主机 `host-merge.ps1` 合并到 main（第 166 行，改业务工作区）；T7 `go run honnef.co/go/tools/cmd/staticcheck@latest`（第 164 行，联网下载且未固定版本；CI 固定 staticcheck 2026.2.1 并注明新 Go 导出数据不兼容，`.github/workflows/go.yml:36-41`）。
- 影响: 声明与实际动作不一致，Gate 审批时无法逐项看到出机器/主机动作；`@latest` 结果不可复现。
- 建议: 在授权段逐项列出：主机建 worktree、主机 Windows 测试、主机本地合并 main（本地提交默认授权，非推送）、staticcheck 下载；staticcheck 固定 `@2026.2.1`（与 CI 一致）或改用已安装二进制。

### PS9 [LOW] 任务内含包级/全量检查，违反 SR1407 与 plan-contract“任务写 focused 命令”

- disposition: CORE_CORRECTIVE
- 证据: T3 完成标准 `go test ./internal/config/... ./internal/cli/...`（第 129 行）；T4 完成标准 `go test -race ./internal/... ./pkg/...`（第 142 行），与 T7 `go test -race ./...` 重复。
- 影响: 增加执行成本，且与合同“包级/全量放波次末或完成 Gate 各一次”冲突。
- 建议: 移到 W2/W3 波次末各一次或只保留 T7。

### PS10 [MEDIUM] workspace baseline 缺具体 HEAD，且未登记并行设计对同一 owner 文件的影响

- disposition: CORE_CORRECTIVE
- 证据: 第 70 行“HEAD 以计划批准时为准”；plan-closure 要求记录 HEAD。当前 HEAD 已是 `b11cd717 docs(design): multi-command hooks design 0.1`（在计划提交之后）。该设计第 148-153 行规定新描述符 `hooks.commands`（`Protected:true`），并写明“若本特性先于 suga-hyj7 落地…由 suga-hyj7 计划把它列入分类表”，两者共享 `internal/config/settings_registry.go` 与分类表测试。
- 影响: 基线不可核对；并行特性的落地顺序未写入前置/fail-closed，可能在 T1 触发 Ownership Conflict 或分类表遗漏 `hooks.commands`。
- 建议: 基线写明具体 HEAD、`git status --short` 结果与 expected symbols；前置中加“`suga-5xmf` 若先合入，T1 分类表纳入 `hooks.commands`（执行类）；若命中其未合入改动按 Ownership Conflict 停止”。

### PS11 [MEDIUM] ACP / run 真实边界验证或 `UNVERIFIED_REAL_ENV` 披露缺失

- disposition: CORE_CORRECTIVE
- 证据: AGENTS.md 验证节“ACP 行为的真实模型验证用 `scripts/acpprobe/acp_probe.py`”；SR1402.1 与模板注释（真实边界以 mock 替代时另列 real-env 验证或披露）。计划 T4 的 ACP/SDK trace 用例为进程内测试，T7 无 acpprobe 一步，也无披露。
- 影响: ACP 同会话去重（PS2 的风险点）没有真实客户端证据。
- 建议: T7 加一次 `python3 -I scripts/acpprobe/acp_probe.py`（两个 prompt，检查 trace 只有一条 `kind=config`），或在完成报告写 `UNVERIFIED_REAL_ENV: ACP（复现：…）`。

### PS12 [MEDIUM] 测试隔离未覆盖 `SUGA_SESSION_ID`

- disposition: CORE_CORRECTIVE
- 证据: 第 72 行测试隔离只写临时 `SUGA_HOME`；新判定读取 `SUGA_SESSION_ID`，而 suga 会话内 `shell_exec` 会导出它（设计第 100 行）。本工作区常由 `gofer job -a suga` 在 suga 会话中跑 `go test`；受影响旧测试（`config_test.go:132-160`、`:175-204` 等 15 处 protected 键调用，`config_origin_judge_test.go`、`config_read_deny_test.go`、`approval_flags_test.go:133`）改为“终端可写”后会随宿主环境变化；ptye2e 子进程也继承环境。
- 影响: 测试结果依赖执行环境，可能在 suga 内跑时误失败或误通过。
- 建议: 前置中规定所有“可写”用例 `t.Setenv("SUGA_SESSION_ID", "")`，ptye2e 启动子进程时显式清除；拒绝用例显式设置。

### PS13 [MEDIUM] 改动落点接近 GR104/AGENTS 1000 行上限，未规划拆分

- disposition: CORE_CORRECTIVE
- 证据: 当前行数 `internal/wiring/runtime.go` 951、`internal/personal/runner.go` 938、`internal/chat/chat.go` 899；T4 在这三个文件内加 Options 字段、比较调用、缓存、trace 记录与横幅逻辑（第 134-140 行），未指定新文件。
- 影响: 易超过 1000 行（结构规则，review-contract 要求计划批准前修正）。
- 建议: T4 写明新逻辑放新文件（如 `internal/wiring/protected.go`、`internal/personal/protected_trace.go`），在原文件只留挂点。

### PS14 [LOW] CAP-03 与 T2 的终端判定注入方式不一致

- disposition: CORE_CORRECTIVE
- 证据: CAP-03（第 51 行）“注入为函数参数”；T2（第 109 行）用包级可替换变量 `var configStdinIsTerminal = isTerminalInput`。SR2003 精神是不用包级可变全局。
- 影响: 能力决定与任务实现两说，包级变量与并行测试冲突（`t.Parallel` 下竞态）。
- 建议: 二选一写定；推荐经 `newConfigCommand` 参数或命令结构体字段注入，测试从 `newCLI` 构造处替换。

### PS15 [LOW] 若干验证命令/标准不可观察

- disposition: CORE_CORRECTIVE
- 证据: T1 `-run 'TestProtected|TestSettingsRegistry'` 中 `TestSettingsRegistry` 在 `internal/config` 不存在（现有为 `TestNetworkProxyRegistry` 等）；T6 验证“人工核对与设计一致”无可观察标准，grep 未写运行目录；T7 “grep `scripts/` 中 `suga config set` 调用并确认未受影响”——实际只有 `scripts/ptysmoke/chat_i18n_en.py:139` 写 `language`（非 protected），可直接写成结论。
- 影响: 小幅降低可执行性。
- 建议: 改正正则；T6 列出需出现的关键句/键清单作为检查点并写运行目录。

### PS16 [LOW] 计划未要求测试写法约定

- disposition: DEFERRED_ENHANCEMENT
- 证据: SR2002 与 AGENTS.md“单元测试编写”（`github.com/gookit/goutil/x/assert`、同方法多用例 `t.Run`）；计划任务未提及，而实施交由子 agent。
- 影响: 子 agent 可能用其他断言库，审查返工。
- 建议: 在“执行者”说明或任务书中引用 SR2002 一句。

## 覆盖缺口与限制

- 字面键盘点只抽查 gkconfig 直接读取，未覆盖经 `Read*` 辅助函数或结构体解码的读取。
- 未运行任何测试；ptye2e 构建 tag 结论基于源码与 Makefile。
- 本报告写在 `tmp/review/suga-config-keys-plan/`，计划声明的评审目录为 `docs/review/2026-11-12-protected-config-keys-plan/`，归档由调用方处理。
- Spec/Executability 轴另行评审；本轴对执行性问题只记录了核对规范时直接撞到的项（PS1、PS2、PS6）。

## 结论

BLOCKED。开放 CORE_BLOCKING：PS1（BUG，ptye2e/Windows 验证命令缺 build tag，证据为空）、PS2（BUG，ACP trace 去重机制与 runtime 生命周期矛盾）。其余 PS3–PS15 为 CORE_CORRECTIVE，计划批准前应修正；PS16 为 DEFERRED_ENHANCEMENT。

## 剩余风险与后续

- 修订后第 2 轮只需复核 changed/dependent scope：T3–T5、T7 验证命令、可追溯表、baseline、授权段。
- 与 `suga-5xmf`（多命令 hook）的落地顺序是持续风险，需在实施前确认。
