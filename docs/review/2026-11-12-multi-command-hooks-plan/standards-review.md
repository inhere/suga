<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令 实施计划 第 1 轮 Standards/Governance 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `1b7478369019c5590a760bd3739af00944a531b2`；subject path `docs/plans/2026-11-12-multi-command-hooks-plan.md`
- 修订版本: Draft 0.1
- 评审者: 独立 Standards/Governance 轴评审者（Claude 子 agent，未参与本计划撰写，未读 Spec/Executability 轴报告）；模式 `REPORT_ONLY`，第 1 轮 discovery，全量读
- 日期: 2026-11-12
- 排除项: Spec/Executability 轴的逻辑正确性（状态机语义、竞态、测试能否真跑通）只在影响治理判断时引用；不评审设计本身（设计 0.6 已批准）；不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md`（router、文档、AI 协作、Go 规则、inhere 规则包 SR2001–SR2004、doc-workflow、plan-contract、planning-core-rigorous、plan-closure、plan 模板）全文；`tmp/idev-std/d/8Yz1eltnU9GuyZy8zAtfgA/payload.md` 中 review-contract、review-candidate-loop、review 模板、SR1201–SR1211.1、SR1301、SR1402.1/SR1412.1/SR1433 段落。
- 被评对象：`git show 1b747836:docs/plans/2026-11-12-multi-command-hooks-plan.md` 全文（237 行）。
- 依据：`git show 1b747836:docs/design/2026-11-12-multi-command-hooks-design.md` 全文（0.6，批准记录 `:341`）；`AGENTS.md` 全文；参照 `docs/plans/2026-11-12-protected-config-keys-plan.md` 0.4（已批准）。
- 事实核对（只读）：`internal/hooks/hooks.go`（523 行、`New` 在 `:208-218` 提前 `return nil, nil`）；`hooks_test.go:164`（`TestHookFailuresAreBestEffort`）与 `:293`（`TestHookTimeouts`）；计划各 `-run` 名在 main 或 `feat/protected-config-keys` 中是否存在；`feat/protected-config-keys` 的 `ConfigKey.Protected`、`TestProtectedClassification`、`setupConfigCLI` 终端注入、分类表 `hooks.*` 行；`Makefile:135`、`:169-170`；`tmp/xhen-p1/{wt-repo,host-merge}.ps1` 存在；`grep -rln stop_background --include=*_test.go internal test`。

## 验收标准

- plan-contract 必需内容；plan-closure（Capability Discovery 三表、唯一 `host_or_non_offline_action`、baseline、任务 owner/动作/依赖/验证/完成标准、可追溯性）；plan 模板 1.3.1。
- SR1107/SR1137 Gate、SR1204、SR1211/SR1211.1、SR1301、SR1402.1、SR1403/SR1403.1、SR1405/SR1405.1、SR1407、SR1409、SR1412、SR1432、SR1433；GR104、GR107–GR110；SR2002、SR2004；AGENTS.md“验证”与“项目编码规范”。
- review-contract：五类 `CORE_BLOCKING` + necessity proof；同类穷尽；结构违规按 `CORE_CORRECTIVE`。

### 逐项核对结果（无发现者）

| 项 | 结论 | 证据 |
|---|---|---|
| 思考模式/核心目标/scope freeze/budget/停止条件 | 符合（`RIGOROUS`，8 轮，`DEFER_OR_REQUEST`） | 计划 `:14-19` |
| 修订记录、状态 `Draft 0.1`、模板首行 | 符合 | `:1`、`:4`、`:6-12` |
| 目标/完成定义/范围/排除/授权 | 基本符合（授权缺口见 HPS5） | `:21-36` |
| 输入与批准证据 | 符合：设计 0.6、批准提交 `da204262`、用户原话与设计 `:341` 一致 | `:38-43` |
| Capability Discovery 三表 grammar | 结构符合（固定 heading、精确列名、`CAP-0x` 唯一、`NONE` sentinel）；内容缺一行见 HPS8 | `:45-68` |
| `host_or_non_offline_action` 恰一个 | 符合（`grep -c` = 1，值 `REQUIRED`，列全主机/联网动作） | `:36` |
| workspace baseline | 符合（root、branch、HEAD `da204262`、status 干净、hyj7 在途归属、实施时重记）；当时事实属实（hyj7 T6 `5d0685ff` 提交于 17:21 +0800，晚于计划提交 17:20 +0800） | `:72` |
| 波次 | 符合，依赖链 T1→T2→…→T9 无环 | `:82-91` |
| 运行目录与 `-run` 名 | 符合：均写“worktree 根”；现有用例名在 main 全部存在，`TestProtectedClassification` 存在于 hyj7 分支（受前置条件保护），新增用例名由各任务创建 | `:115`、`:127`、`:139`、`:152`、`:163`、`:173` |
| 任务内 focused、全量放末尾一次（SR1407） | 符合（`-race ./...`、staticcheck、smoke 只在 T9） | `:189-190` |
| 回滚与恢复点 | 符合 | `:197-201` |
| 人工 Gate 最小集合 | 符合：计划批准、当前执行请求、T1 分支②停、统一外部动作 Gate（逐项列 push/tag/发版/安装/IDEV-STD 消息） | `:203-208` |
| T9 向 IDEV-STD 会话发消息 | 可追溯到设计 `:339`“定稿后把配置形状回复 IDEV-STD 维护会话”，不超出设计；已列入外部动作 Gate（`:208`）与剩余工作（`:237`），计划内只准备内容。内容与追溯缺口见 HPS2、HPS11 | `:192`、`:208` |
| 超出设计的任务 | 未发现。T1（HS11）、`pipelineTestHook`（HS10）均为设计明确留给计划的项（设计 `:72-73`） | — |
| GR104 | 符合：`hooks.go` 523 行，流水线放新文件；chat 两文件净增 ≤25 行 | `:68`、`:164` |
| GR107 | 不适用（不改 `cmd/*`） | — |
| GR108 | 符合：`internal/cli/chat.go`、`internal/wiring/localenv.go` 不改，合并逻辑全在 `internal/hooks`（D1）；新依赖 `internal/hooks → internal/config` 无环（`internal/config` 无 `internal/hooks` 引用） | 设计 `:251-258`；计划 `:33` |
| GR109 | 符合：Stop/普通事件/Interrupt 失败记 Warn，取消/超时记 Debug；`case "hooks": _, err = ReadHookEntries()` 丢的是返回值不是错误，与 `case "mcp"`（`settings_registry.go:715-716`）同式 | `:113`、`:136`、`:148`、`:150` |
| GR110 | 符合：`TestConfigHooksCommands` 经 root 命令分发，含合法/非法/protected 拒绝 | `:115` |
| SR2002 / AGENTS 测试写法 | 符合 | `:77` |
| SR2004 / 设计“代码组织” | 符合：文件清单与设计“架构”一节一致（`hooks_entries.go`、`pipeline.go`、`hooks_record.go`、`hooks_gate.go`），`chat.go`/`chat_turn.go`/`stop_background.go`/`pkg/*` 不改 | 设计 `:248-259`；计划 `:33` |
| SR1409 暴露度 | 符合：高暴露（已发布 v0.55.0、命令执行、执行前拒绝门链路），两轴 + ledger | `:19` |
| SR1403/SR1405 | 符合：排除 A 方案字段、项目级 hook、HS6、`inspect env`；无新 Module、无新依赖 | `:17`、`:34`、`:80` |

### SR1301 安全核对（计划层）

| 面 | 结论 |
|---|---|
| 鉴权 | 已核对：`hooks.commands` `Protected:true`；protected 生效依赖 hyj7，计划以“hyj7 已合入”作 fail-closed 前置（`:73`），T2 验证 `SUGA_SESSION_ID=x` 下被拒（`:115`）；发版顺序 D6 列为剩余项（`:237`）。前置判定式的精度见 HPS6 |
| 注入防护 | 已核对：argv 直接执行，复用 `commandClient`，不经 shell（CAP-02） |
| 秘密不进日志/输出/仓库 | 已核对：trace 新增 attrs 只有 `entry`、`program`（`filepath.Base(argv[0])`），不记参数（`:124`、`:160`）；测试用临时 `SUGA_HOME`，不读写真实 `~/.suga`（`:76`） |
| 入参校验 | 已核对：启动与 `config set` 共用 `ReadHookEntries`，错误路径精确到 `hooks.commands[i].<字段>`，非法值退出码 2 且文件不变（`:112`、`:115`） |
| 资源归属 | 不适用（无多用户资源） |

无 SECURITY 类违反。

## 发现

按严重级别排序。全部为 `CORE_CORRECTIVE`，无 `CORE_BLOCKING`。

### HPS1 [MEDIUM] 设计“验证要求”未全部落到任务（CORE_CORRECTIVE）

- 证据: 完成定义 5 要求“设计‘验证要求’全部有对应测试”（计划 `:28`）。逐条对照设计 `:293-304`，同类全部缺口：
  1. 兼容（HF1）：“`stop_background: true` + `stop_args` 无 `stop_wait_sec`、只配 `stop_args` 的现有配置照常启动”（设计 `:294`）。T3 只回放现有 `internal/hooks` 测试（`:127`），而 `grep -rln stop_background --include=*_test.go internal test` 只命中 `internal/config/settings_registry_test.go`，现有 hooks/chat 测试没有此用例；T3 恰好要改写 `New` 去掉 `hooks.go:216-218` 的提前返回，这是回归风险最集中的点。
  2. 后台 Stop 正例：“R1 block 变为任务通知、输入不打断”（设计 `:299`）。T6 的 `TestMultiHookBackgroundStop`（`:163`）只有被杀/超时/Close 三个负例，没有多条目 R1 → 任务通知的正例；`TestBackgroundStop` 是现有单条目用例。
  3. Stop 合并 `Notice` 按执行顺序换行连接（设计 `:206`、HF19）：T5 动作写了（`:149`），T5 验证（`:152`）无对应断言。
  4. ptye2e：设计要求“`suga inspect trace --json` 中每条 `hook` 事件的 `entry`/`program`/decision 正确”（`:303`），T7 只断言 `name=Stop` 的事件（`:172`）。
  5. 设计“Interrupt 不进 trace；打断时未启动的条目不记”（`:289`）：无任何任务断言。
  6. 设计 ptye2e 反向验证三项含“临时去掉 R0”（`:303`）；T7 只做两项（`:173`），R0 反向验证在 T5/T6 单元层（`:152`、`:163`）。映射本身合理（ptye2e 场景不触发 R0），但计划未写明这一替代。
- 影响: 计划与自身完成定义 5 不一致；第 1 项使 AC3“现有配置照常启动”在 `New` 重构后缺回归保护。不判 `CORE_BLOCKING`：功能本身已在任务动作中，缺的是断言；不采纳不会使计划不可执行或结果必然错误，属验证完整性（necessity proof 不成立）。
- 建议: T3 新增 `TestNewHookEntries` 子用例（或 `TestNewCompatAcceptance`）：只配 `hooks.command`+`stop_args`+`stop_background: true`、只配 `stop_args` 无 `stop_wait_sec` 时 `New` 成功且返回 `*commandClient`；T6 补“两条新条目、A block → 一条任务通知、`takeBlocked()` 为真”；T5 补 Notice 连接断言；T7 补 SessionStart/UserPromptSubmit 事件 `entry`/`program` 与“trace 中无 Interrupt”断言；T7 验证处写明 R0 反向验证由 T5/T6 承担。

### HPS2 [MEDIUM] 可追溯表缺项（CORE_CORRECTIVE）

- 证据: plan-contract 要求“决策/需求到每个任务及其验证的 exact 可追溯性”。可追溯表（`:212-231`）同类缺口：
  - D8（顺序执行、各自超时，设计 `:315`）未出现，只能从“AC4（单独超时）”行间接推出；
  - HF12（`filepath.Base`、Windows 反斜杠，设计 `:36`、`:304`）未列，虽 T3 有 `_windows_test.go`（`:127`）；
  - HF19（Notice 显示时机、文档提示不依赖 stderr，设计 `:229`）、HF20（gofer 影响描述，设计 `:49`、`:286`）未列，T8 动作已覆盖（`:180`）；
  - T9 的合并与 IDEV-STD 消息无对应行（来源是设计 `:339` 结论节），完成定义（`:23-29`）也未提及该消息。
- 影响: 评审与验收无法逐条核对上述决策落在哪个任务、用什么证据；违反“无法追溯的任务不得进入范围”的形式要求（T9 消息实际可追溯，只是未登记）。
- 建议: 表中补 D8、HF12、HF19、HF20 行；补“设计结论：配置形状回复 IDEV-STD（外部动作 Gate）| T9 | issue 评论中的消息草稿”一行。

### HPS3 [MEDIUM] 真实边界验证缺失或未披露（SR1402.1）（CORE_CORRECTIVE）

- 证据: 本特性改的是外部进程交互路径；全部验证用假 hook 进程（T3–T7 的 `TestFakeHookProcess`/`TestMultiHookHelper`）。核心场景 AC1 是“gofer 的 `gofer hook generic --agent suga` 与 IDEV-STD `idevstd script continuation_hook --agent suga` 共存”（设计 `:77-78`），计划没有任何任务用真实 gofer hook 或真实 continuation_hook 跑一次，也没有 `UNVERIFIED_REAL_ENV: <面>（复现：<命令>）` 披露。计划模板任务节注释明确要求“另列 real-env 验证或 UNVERIFIED_REAL_ENV 披露（SR1402.1）”。参照计划 hyj7 0.4 对 acpprobe 做了真实验证并对未覆盖面标注（hyj7 计划 `:217`）。
- 影响: 完成报告可能把假进程 ptye2e 当成端到端证明（review-contract 禁止）；T1 分支判断（gofer 无 `--wait`）也只有读代码结论。
- 建议: T9 加一次主机 gofer exec job：worktree 构建的 `suga.exe` + 隔离 `SUGA_HOME`，`hooks.command` 指向真实 `gofer hook generic --agent suga`（不带 `--wait` 或短 `--wait`）、`hooks.commands` 指向真实 `idevstd script continuation_hook --agent suga`，跑一回合并查 trace；做不到则在 T9 完成标准写明 `UNVERIFIED_REAL_ENV` 行及复现命令。若需真实模型，已在 host 声明范围内（`:36`）。

### HPS4 [MEDIUM] 派活约束未写全，执行者选择沿用他计划的单次授权（SR1433、SR1211.1、SR1412、SR1432）（CORE_CORRECTIVE）

- 证据: 计划 `:75` 的任务书条款列了“只改列出文件、不删注释、不用 web_fetch、可改 go.mod/go.sum、GR107–GR110、`--sync`”，未列 SR1433 要求随派活携带的 SR1211/SR1211.1 约束：只暂存本任务 exact owner 文件、提交前核对 `git diff --cached --name-only`、共享工作区禁 `git stash`/`git reset`/`git checkout -- <他人路径>`；也未写“首个提交时限/每完成一块即提交”。执行者固定为 `--model opencode-go/deepseek-v4.1-flash`；该选择在 hyj7 中来自用户对 hyj7 的单次原话（hyj7 计划 `:232`“实施执行可以用suga oc-go ds4.1”），本计划未给出本计划的用户依据。T5 是带锁状态机与三种打断子态的并发实现，SR1432 要求按任务性质选执行者。
- 影响: 执行者提交越界或吸收他人改动时缺少明文约束；执行者选择可能与用户当前偏好不一致（授权不跨计划继承，SR1412）。
- 建议: 任务书条款补 SR1211/SR1211.1 三条与提交节奏；执行者写为“默认 `-a suga` + 模型在当前执行请求中由你确认”，或注明 T5 可改派更强模型/子 agent，执行者变化不改任务合同（同 hyj7 的写法）。

### HPS5 [LOW] 分支创建与本地合并未写入授权边界（SR1211.1）（CORE_CORRECTIVE）

- 证据: SR1211.1：默认本地提交授权“不授权创建/切换分支”。计划 `:35` 只写“本地提交为默认授权；push、tag、发版、安装需另行授权”；建分支 `feat/multi-command-hooks`（`:74`）与合并到本地 main（`:191`）只出现在 host 声明（`:36`）与任务动作中，人工 Gate（`:205-208`）未说明由哪一 Gate 覆盖。
- 影响: 授权来源隐含，执行时可能被要求另行确认或被视为越权。
- 建议: 在“授权”或 Gate 2 写明“当前执行请求覆盖建 worktree 分支 `feat/multi-command-hooks` 与合并到本地 main；不含 push”。

### HPS6 [LOW] hyj7 前置条件判定式不精确，且与 Gate 2 / T1 依赖表述冲突（CORE_CORRECTIVE）

- 证据: `:73` 以固定中间提交 `22482244`（hyj7 T5）判定“已合入”，并附“或其最终合并提交为真”而无判定命令；hyj7 分支当前已到 `5d0685ff`（T6），还有 T7 未做。Gate 2（`:206`）写“你指示开始实施（且 hyj7 已合入 main）”，而 T1 依赖写“可在 hyj7 合入前执行”（`:105`）。
- 影响: 判定可被不完整的 hyj7 状态满足（例如只合到 T5 的中间状态），fail-closed 不精确；T1 能否在 hyj7 合入前开始的授权口径不一。
- 建议: 判定改为“issue `suga-hyj7` 已关闭且 `git merge-base --is-ancestor <hyj7 合并提交> main` 为真，`ConfigKey.Protected` 与 `TestProtectedClassification` 在 main 存在”；Gate 2 写明“执行请求可先只覆盖 T1；T2 起另需 hyj7 已合入”。

### HPS7 [LOW] 任务文件清单与动作不一致（CORE_CORRECTIVE）

- 证据（同类穷尽）:
  - T3 动作要求先放 `newPipeline` 骨架“使编译通过”（`:125`），文件清单只有 `hooks.go` 与测试（`:121`），骨架应在的 `pipeline.go` 列在 T4（`:133`）；
  - T1“文件: 无改动”（`:97`），分支②却要“把这一用户可见影响写进 T8 的用户文档条目”（`:101`）；
  - T4 的 `StartStop` 临时实现标 TODO、“T5 完成前不合入”（`:138`），未说明该提交按 SR1211.1 如实命名为 checkpoint。
- 影响: 执行者按文件清单约束时会自相矛盾（T3 不能新建 `pipeline.go` 又要编译通过）。
- 建议: T3 文件补 `internal/hooks/pipeline.go`（仅骨架）；T1 分支②改为“把影响记入 issue 评论，供 T8 写入文档”；T4 注明提交信息带 `checkpoint`/`wip` 字样。

### HPS8 [LOW] Capability decisions 缺多条目编排能力行（CORE_CORRECTIVE）

- 证据: 本计划最大的新增代码是 `pipeline.go`（事件分流、顺序合并、两阶段 Stop、R0–R3、冻结与打断），但 Capability decisions（`:51-54`）没有对应 required capability；CAP-02 写作“每条目一个 `commandClient`，加 `Runs`”，只覆盖单条执行原语。`:68` 的括注只说明文件位置。
- 影响: 复用搜索（现有 `stopWait`、`stop_background` 任务、`commandClient.call`）与“为何需要新编排代码”的 gap 未在表中证明。不判 `CORE_BLOCKING`（AUTHORITY/能力复用未证明）：编排的 owner、位置与形态已由已批准设计 D1、架构节（设计 `:254`）确定，补一行不改变复用决策、owner 或生命周期，necessity proof 的因果链不成立；按 plan-closure 属可局部修正。
- 建议: 加 `CAP-05 | 多条目编排与合并 | stopWait、stop_background 任务、commandClient.call | 否（均为单条目） | 既有 owner 扩展（internal/hooks 新文件 pipeline.go） | OWNER_EXTENSION | 现有原语无多条目顺序/两阶段/冻结 | 单条目路径保留不改，无重复`。

### HPS9 [LOW] 与 AGENTS.md 验证要求的偏差与重复执行（CORE_CORRECTIVE）

- 证据: AGENTS.md“验证”要求 Windows 行为经 gofer exec job 跑 `go test ./...`；T9 只跑 `./internal/config/... ./internal/hooks/... ./internal/chat/... ./internal/cli/...`（`:190`），未说明理由。T9 同时跑 `make smoke-go` 与 `make smoke-chat`（`:189`），而 `smoke-chat: smoke-go`（`Makefile:135`），smoke-go 会跑两遍（SR1407）。
- 影响: 偏离项目验证合同而无记录；重复 30 分钟级 ptye2e。
- 建议: Windows 改为 `go test ./...`（或写明只跑子集的理由并接受风险）；容器内只跑 `make smoke-chat`（已含 smoke-go）。

### HPS10 [LOW] 完成 Gate 缺 retrospective 判定与 gofer plan/todo 建立步骤（CORE_CORRECTIVE）

- 证据: plan-contract/plan-closure 要求 Full 计划进入 `COMPLETE` 时除 ≤8 行总结外做一次性 retrospective 判定与提示（总结“不替代 retrospective 提示”）；计划 `:236` 只写总结。`:235` 要求“plan todo 标 done”，但前置检查未写建 gofer plan 与 T1–T9 todo（AGENTS.md：进度用 `gofer plan`/`gofer issue` 跟踪）。
- 影响: 完成 Gate 少一步；todo 无创建点。
- 建议: `:236` 补“并按 plan-closure 做一次 retrospective 判定/提示”；前置检查补“执行开始时 `gofer plan create` + `add-todo` T1–T9（`--cwd inhere-tools/suga`），job 挂到 plan”。

### HPS11 [LOW] T9 IDEV-STD 消息内容要点未写定（CORE_CORRECTIVE）

- 证据: T9 只写“把配置形状回复 IDEV-STD 维护会话的消息内容写进 issue 评论”（`:192`）。`hooks.commands` 为 protected（D12），`SUGA_SESSION_ID` 非空或 stdin 非终端时 `config set` 被拒（T2 验证 `:115`）；设计 `:114`、`:288` 说 IDEV-STD 改为输出 `suga config set hooks.commands '<YAML>'`，若该命令由 agent 会话代执行会被拒。另，设计 `:339` 写“定稿后”回复，计划推迟到合并后，未说明理由。
- 影响: 外部消息内容不完整可能使对方实现一个在 agent 会话中必然失败的安装路径。
- 建议: T9 写明消息至少含：条目形状（只 `command`+`timeout_ms`）、整体替换语义、protected 需人在终端执行、需随 hyj7 同版或其后的 suga 版本；并说明为何合并后发送（或改为设计定稿后即可准备、发版后发送）。

## 覆盖缺口与限制

- 未执行任何测试或 validator（`idevstd script validate_document`）；结构结论来自人工对照合同。
- 未读 gofer `internal/hookrelay/run.go`，T1 的判断依据未复核。
- Spec/Executability 轴问题（状态机竞态是否闭合、测试钩子点能否复现三种子态、`config get` 对 `ConfigTypeYAML` 的输出格式）未展开，留给另一轴。
- 本报告写在未跟踪的 `tmp/review/suga-hooks-plan/`；正式 `docs/review/2026-11-12-multi-command-hooks-plan/standards-review.md` 与 ledger 由主 Agent 落盘。

## 结论

PASS（Standards/Governance 轴，第 1 轮）：无 `CORE_BLOCKING`。HPS1–HPS11 均为 `CORE_CORRECTIVE`，未发现 GR107–GR110 或规则包结构违规（无“计划批准前必须修正”的结构项）；建议在第 2 轮 confirmation 前一并修订，尤其 HPS1、HPS3、HPS4。

## 剩余风险与后续

- hyj7 未合入前本计划不可进入 T2；hyj7 若返工改变 `ConfigKey.Protected`/分类表合同，按 Semantic Amendment 处理（计划 `:201` 已写）。
- 设计 0.6 为未复核修订（设计 `:339-341`），其 HF26–HF29 内容只经本计划评审间接检查。
- 发版顺序 D6 与 IDEV-STD 消息发送在外部动作 Gate 之后，需在完成报告中保持为剩余项。
