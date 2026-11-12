<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 4 轮（纠正确认轮）Standards/Governance 评审

## 评审目标与范围

- 目标: `docs/design/2026-11-12-multi-command-hooks-design.md`（issue `suga-5xmf`）
- 候选身份: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `c099198b7fdd17d3a3bbfbc6e787ab80c60f0f90`；subject path `docs/design/2026-11-12-multi-command-hooks-design.md`；document revision Draft 0.5（工作区 HEAD 即 `c099198b`，无未提交改动）
- 修订版本: Draft 0.5（上轮候选 `46272dc5` / 0.4，两轴 PASS）
- 评审者: 独立 Standards/Governance 轴评审者（子 Agent，未参与撰写），REPORT_ONLY，未改仓库、未提交
- 日期: 2026-11-12
- 轴: Standards/Governance；第 4 轮，唯一一轮纠正确认轮（planning-core-rigorous：第 3 轮全 PASS 且含 `CORE_CORRECTIVE`、candidate 已变化）
- 排除项: 全量重评；Spec/Executability 轴（R0 与临界区状态机的正确性、竞态推演）；用户选择（“hooks Q1 选 M，Q2–Q7 按推荐”）本身；gofer `hookrelay` 无 `--wait` 路径的实际行为

## 输入与方法

- 规范：`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/payload.md`（SR1103、SR1403/SR1403.1、SR1405、SR1408、SR1410、SR1401、GR104、GR107–GR110、SR2004、planning-core-rigorous 轮次规则）；review-contract 与 review 模板取自 `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`。
- 上轮两轴报告：`docs/review/2026-11-12-multi-command-hooks/standards-review.md`（HS9）、`spec-review.md`（HF21–HF25 及“剩余风险”中的 gofer 即时型条目推论）。
- 差异：`git diff 46272dc5..c099198b -- <subject>`（+30/−11，集中在修订记录、第 3 轮处理表、§4 R0 与打断、架构 `hooks.New`、gofer 影响、验证要求、结论）；同区间 `review-ledger.json` 的 diff（核对 HS9）。依赖章节：§2（只配兼容条目走现有实现，`:150-152`）、§4 开头适用范围（`:176`）、决策 D2/D5/D9（`:296-303`）、用户选项记录（`:310-318`）。行号均指 `c099198b` 版本。
- 事实抽查：gofer `internal/hookrelay/run.go:246,265-282,594-640`（只为确认“收 Stop、不收 Interrupt”的处理确实尚未核实，不评其正确性）。

## 验收标准

- 0.5 修订（HF21–HF25、gofer 影响补充、“有意收紧”）能否追溯到 AC1–AC4（`:66`）、已决用户选择 D7–D13 或已批准的验证要求；是否引入无来源机制（测试钩子、临界区状态机）。
- 是否改变已决用户选择或 D2 兼容承诺；是否需要按 SR1401/SR1408 回到 Human Gate。
- SR1403/SR1410 最小充分；GR109（日志/错误不吞）、GR110（真实分发）不退化。
- 修订记录（SR1103）、第 3 轮处理表完整；HS9（ledger 指针）处理。

### 上轮发现关闭情况

| ID | 结果 | 依据 |
|---|---|---|
| HS9（ledger 第 1 轮 receipt 指针被改写） | 关闭 | `git diff 46272dc5..c099198b -- docs/review/2026-11-12-multi-command-hooks/review-ledger.json`：第 1 轮 Standards receipt 恢复为 `4edfb889…`/`46df14c2…`，Spec receipt 恢复为 `4edfb889…`/`d5baa3ee…`，与上轮建议值一致；第 3 轮 receipt 以追加方式写入（`0cf46aa6`），未改前两轮。设计 `:53` 写明“HS9 属 ledger 元数据，设计不改”，符合 SR1103 不因元数据递增版本 |

### 0.5 修订的可追溯性核对

| 修订 | 追溯 | 结果 |
|---|---|---|
| R0 取消来源改为后台任务 ctx 三入口（`:186`、`:197`） | HF21；R0 是 0.4 已评审的 HF13 修正，0.5 只修正列举与机制描述，规则本身不变 | 事实纠正，不新增机制 |
| “有意收紧”（`:198`） | HF21 要求写明；作用域受 §4 开头“以下只适用于存在新条目的流水线；只配兼容条目时见 §2”（`:176`）约束，只配兼容条目仍走现有单条目实现（`:151`、D2 `:296`），所以 AC3/D2 不受影响 | 通过。方向是在“已被新 Stop/上限/退出取代的回合”中少投递一条过期续跑，不改变 AC2（正常回合 block 开启下一回合）；不触及 D7–D13 任一选择，无需 SR1401 停顿 |
| `hooks.New` opts 作用于每个条目（`:240`） | HF22；新条目与兼容条目同一会话环境是 AC1“共存”的最小一致形态；`NoSessionEnv` 沿用现有开关 | 通过；不新增 Option、不改 `Option` 类型 |
| `hooks.command` 为空时不解析配套键（`:240`） | HF23；直接落实 AC3 与 `:153`“现有配置不会因升级而启动失败” | 通过；是对现状的保留，不是新增检查 |
| 成功/失败/超时/取消都填 `Runs`（`:169`） | HF24；0.3/0.4 已批准的逐条 trace（`entry`/`program`）在失败路径的完整化 | 通过；不新增字段 |
| 临界区状态机（`:208-212`） | HF25；0.4 已有“冻结与启动下一条在同一把锁内判定”（步骤 1），0.5 只把同一把锁的状态转换写定，使 D9“只给一个条目发 Interrupt”与单条目现状（AC3 下 Interrupt 合同）唯一可实现 | 通过 SR1410：没有新增锁、通道或额外 goroutine，是已有锁的规格化，不是“重机制” |
| 测试钩子（`:285`“经测试钩子在临界区之间注入打断”） | HF25 的三种状态各一例验证；不在临界区间注入就无法确定性地覆盖“进程已退出但结束临界区未执行”等窗口 | 可追溯（D9 + HF25 验证要求）。形式未约束，见 HS10 |
| gofer 影响补即时型兼容条目推论（`:274`） | 上轮 spec-review“剩余风险”；是 D9 的推论记录，不新增 suga 机制 | 通过；把核对留到计划阶段的停止条件未写，见 HS11 |
| 验证补项（`:282-283`、`:285`、`:288-289`） | HF21–HF24 对应断言；GR110 行（`:287`）未改 | 通过 |

### 逐项核对

| 条目 | 结果 |
|---|---|
| SR1103 / 版本 | 0.5 有语义变化（R0 适用说明、`hooks.New` 构造规则、`Runs` 填充规则、打断状态机），递增合理；修订行（`:14`）含版本、日期、作者、摘要，并列出 gofer 影响补充；第 3 轮处理表（`:53-61`）逐项覆盖 HF21–HF25 并交代 HS9；结论（`:325`）同步为 0.5 与“只做一轮纠正确认” |
| 已决用户选择 | 未改变：D7–D13 正文与用户选项记录（`:300-318`）在区间内无 diff；“待确认事项”仍为无 |
| SR1403 / SR1403.1 / SR1405 / SR1410 | 通过。0.5 没有引入无法追溯到 AC1–AC4、D7–D13 或已批准验证要求的机制；未扩大范围（未新增 Option、配置键、事件、CLI） |
| SR1408 / SR1401 | 通过。纠正未改变用户结果、授权、Interface、Schema、安全或数据、lifecycle；“有意收紧”只作用于新功能路径的竞态窗口 |
| GR104 / GR107 / GR108 | 不受本轮影响（代码组织 `:236-244` 只改 `hooks.New` 一条说明，分层与依赖方向不变） |
| GR109 | 通过。R0 丢弃 block 是语义规则且写明理由（`:197-198`）；Stop/Interrupt 日志级别（D5）未变；`Runs` 失败路径填充使错误多一处留存，不吞错误 |
| GR110 | 通过。`:287` 经 `internal/cli` 真实分发的验证要求未变 |
| SR1301 | 本轮 diff 未触及鉴权、注入、秘密、入参校验面：opts 只传会话环境变量（与兼容条目现状相同），trace 仍只记 `filepath.Base(argv[0])`；不适用新增核对 |

## 发现

### HS10 [LOW] 测试钩子的形式未约束

- disposition: `DEFERRED_ENHANCEMENT`（不属五类；可追溯到 HF25 验证要求，形式交计划写定即可，不需要再改设计）
- 证据: `:285`“打断三种状态各一例（HF25，经测试钩子在临界区之间注入打断）”；设计其他位置（架构 `:236-244`、`pipeline.go` 说明）未出现该钩子。同类穷尽：全文“测试钩子”只此一处；其他验证均经假 hook 进程或真实分发，不需要生产代码中的注入点。
- 影响: 计划若照字面实现，可能做成导出 API、配置键或环境变量开关，等于在生产路径增加一个无验收来源的可控入口，偏离 SR1403/SR1405。
- 建议: 计划任务写定：钩子为 `internal/hooks` 包内未导出的函数变量（生产为 nil，只在 `_test.go` 中赋值），不导出、不读配置或环境变量、不进 `pkg/*`；审查时核对。

### HS11 [LOW] gofer 即时型兼容条目“收 Stop、不收 Interrupt”的核对留到计划阶段，但未写核对失败时的处理

- disposition: `DEFERRED_ENHANCEMENT`（不属五类：属 D9 已选方案的推论，设计已如实记录；核对动作本身归计划）
- 证据: `:274`“gofer 侧在无 `--wait` 时对‘收 Stop、不收 Interrupt’的处理留到计划阶段核对 gofer `internal/hookrelay`”。gofer `internal/hookrelay/run.go:246,265-282` 由 Interrupt 结算打开的 turn，`:594-640` 是否在无 `--wait` 时打开 turn 取决于服务端 `WaitReason`，本轮未判定。同类穷尽：设计中“留到计划阶段核对”另有 `:290` 旧版本 `config validate` 提示一处，属回滚说明、无用户结果影响，不在此类。
- 影响: 若核对发现无 `--wait` 的 gofer 在 Stop 时也会打开 turn，则配了新条目后 web 侧会多出靠下一次 UserPromptSubmit 结算的 turn——这是用户可见结果变化，按 SR1401 应回到用户，而不是在计划里静默加机制（如给即时兼容条目也发 Interrupt，会改变 D9“只给一个条目”）。
- 建议: 计划把该核对列为首个任务并写明分支：①gofer 无 `--wait` 不打开 turn → 结论记录即可；②会打开 turn → 只在用户文档写明并停止请求确认，不得在计划内自行改 D9 的 Interrupt 去向。

## 覆盖缺口与限制

- 只读 diff 与依赖章节；未变章节（§1、§3 表格、§5、§6、D1/D3/D4/D6–D13）沿用上轮结论。
- 未评临界区状态机与 R0 在各路径下的正确性（Spec/Executability 轴）。
- 未运行代码或测试；gofer `hookrelay` 只读到足以确认“尚未核实”，未判定其无 `--wait` 行为。

## 结论

PASS（Standards/Governance 轴第 4 轮，纠正确认轮）。HS9 已在 ledger 中恢复、设计按元数据处理；0.5 对 HF21–HF25 的修订都能追溯到 AC1–AC4、D9 与已批准的逐条 trace/验证要求：临界区状态机是 0.4 已有冻结锁的规格化，不是新机制；测试钩子来自 HF25 的验证要求；“有意收紧”只作用于存在新条目的流水线，D2/AC3 不受影响，未改变任何已决用户选择。SR1403/SR1410、GR109/GR110 无退化；修订记录与第 3 轮处理表完整。无 `CORE_BLOCKING`，无 `CORE_CORRECTIVE`；HS10、HS11 为 LOW `DEFERRED_ENHANCEMENT`，交计划落实，不构成“未复核修订”。

## 剩余风险与后续

- 计划阶段落实 HS10（测试钩子形式）与 HS11（gofer 无 `--wait` 核对及其停止条件）。
- 上轮后续仍有效：计划任务禁止在 `pipeline.go` 重写子进程执行，复用 `commandClient.call`/`stopWait`；按 D6 在 `TestProtectedClassification` 补 `hooks.commands: exec`；GR110 分发用例在“终端为真、`SUGA_SESSION_ID` 为空”条件下写入。
- 本轮为唯一一轮纠正确认轮；之后若再修订设计语义，须按“未复核修订”向用户披露。
