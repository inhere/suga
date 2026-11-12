<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 3 轮 Standards/Governance 评审

## 评审目标与范围

- 目标: `docs/design/2026-11-12-multi-command-hooks-design.md`（issue `suga-5xmf`）
- 候选身份: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `46272dc5`；subject path `docs/design/2026-11-12-multi-command-hooks-design.md`；document revision Draft 0.4（工作区 HEAD 即 `46272dc5`，无未提交改动）
- 修订版本: Draft 0.4（上轮候选 `096c9e93` / 0.3，本轴上轮 PASS）
- 评审者: 独立 Standards/Governance 轴评审者（子 Agent，未参与撰写），REPORT_ONLY，未改仓库、未提交
- 日期: 2026-11-12
- 轴: Standards/Governance；第 3 轮
- 排除项: Spec/Executability 轴（R0–R3 时序、冻结锁、`PreTool` 出错时 `Runs` 是否填充等可执行性）；用户选择（“hooks Q1 选 M，Q2–Q7 按推荐”）本身；`suga-hyj7` 计划本身

## 输入与方法

- 规范：`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/payload.md`（SR1403/SR1403.1、SR1405/SR1405.1、SR1410、GR104、GR107–GR110、SR2004 等）；`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md` 中的 review-contract、review-candidate-loop 与 review 模板。
- 仓库：`AGENTS.md`；上轮本轴报告 `git show 46272dc5:docs/review/2026-11-12-multi-command-hooks/standards-review.md`（HS6–HS8、HS4 遗留）；`git diff 096c9e93..46272dc5 -- <subject>`，并通读 0.4 全文（`git show 46272dc5:<subject>`，下文行号均指该版本）；同区间内 `review-ledger.json` 与 `docs/plans/2026-11-12-protected-config-keys-plan.md` 的 diff。
- 事实抽查：`internal/chat/hooks_record.go:35-40,86-98,115-122`、`internal/chat/stop_background.go:62-72`、`internal/chat/hooks_gate.go:40-80`、`internal/hooks/hooks.go:232-256,334-340`、`internal/wiring/localenv.go:185-198`（`Enabled = client != nil`）；`internal/hooks` 的非测试导入方只有 `internal/chat`、`internal/cli`、`internal/wiring`；现有 chat 测试对 hook trace 只做按字段断言（`hooks_gate_test.go:96-97`、`stop_background_test.go:69`），新增 attrs 不破坏；`hooks.go` 523 行（GR104 有余量）。

## 验收标准

- 上轮 HS4 遗留、HS6–HS8 是否关闭。
- 0.4 新增/修改机制能否追溯到 AC1–AC4（行 55）或已批准的验证要求；SR1403/SR1405/SR1410、GR108–GR110、SR2004。
- 顺带的事实更新是否属于元数据（不需递增版本、不扩范围）。
- 同类穷尽。

### 上轮发现关闭情况

| ID | 结果 | 依据 |
|---|---|---|
| HS4（遗留） | 关闭 | 行 154 Stop 行“失败记 Warn，取消与超时记 Debug（同现状 `StartStop`）”，D5（行 280）同；验证要求行 267 补对应断言。R1 单元格（行 179）仍写“只记日志”，但级别由行 154 唯一确定，不再不唯一 |
| HS6 | 关闭 | 顺带修改删去；行 230 列为 deferred（`hooks.go:234-240` 描述与代码一致），行 34 HF12 处理同步改写；行 139“与现状完全相同”字面成立；全文无其他“顺带” |
| HS7 | 关闭 | 行 213 改为“（D6、HS5）” |
| HS8 | 关闭 | 行 11 改指 `docs/review/2026-11-12-multi-command-hooks/`；全文已无 `tmp/` 引用。同目录按轴固定报告、每轮覆盖、旧轮靠 Git 历史，符合 review-candidate-loop（但见 HS9：ledger 侧的旧轮指针被改写） |

### 0.4 修订的可追溯性核对

| 修订 | 追溯 | 结果 |
|---|---|---|
| R0 首要规则（行 178、185-190） | HF13（BUG 类阻断）；AC2 的正确性（不发过期续跑） | 必需 |
| 只配兼容条目时保留现有单条目实现（行 140、224、236、D2） | AC3；SR1403 的“复用”优先，比让流水线模拟单条目更小 | 通过，且降低风险；流水线复用 `commandClient.call`/`stopWait` 为原语（行 226），无重复实现 |
| `stop_args` 总附加、等待型按键判定（行 171、78） | AC3 行为对齐现状；HF17 是删减定义 | 通过 |
| 流水线全部结束时打断仍发给最后执行的条目（行 196） | 与单条目现状合同一致；gofer 依赖 Interrupt 结算 turn（行 107） | 通过 |
| `Call` 按事件名分流、任一键非空即启用（行 141、147） | D7（新条目不收 Post/Pre）；只配 `hooks.commands` 时不能静默失效（AC1/AC2 前提） | 通过；`localenv.go` 不改的说法与代码一致 |
| `Response.Runs []EntryRun`（行 158） | 0.3 已批准的逐条 trace 的载体（AGENTS.md“验证”节要求用 trace 检查 hook 行为）；字段（编号、程序名、耗时、回答、错误）均为逐条记录所需的最小集合 | 通过；内部类型，只在本仓库使用（行 89），不改 `pkg/*`（行 229） |
| 单条目实现也填一条 Run（行 140、158） | D2 承诺兼容条目 trace 带 `entry`/`program`（0.3 行 213 已有） | 通过 |
| PreToolUse 记录带 `entry=0`/`program`，改 `hooks_gate.go` 一处（行 161、227、270） | 0.3 已承诺“每条 hook 事件”带两 attrs；0.4 只是写定挂点，避免 trace schema 出现“缺省即 0”的特例 | 通过，属落实而非新增 |
| 普通事件逐条 status 与现状一致（行 160） | AC3 | 通过；与 `hooks_record.go:38` 不传 `Err` 的现状一致 |
| Notice 显示时机取舍（行 200）、gofer 影响更正（行 257） | HF19、HF20：记录现有后果与文档提示，不新增机制 | 通过 |
| 计划 0.4“已批准”（行 210）、Draft 0.8“已批准进入计划”（行 106） | 事实更新，依据 `341237b3` 记录的批准原话 | 元数据，不扩范围 |
| 0.2 修订行来源路径改写（行 11）、HS7 引用改正 | provenance 纠正（HS8、HS7） | 元数据；与 0.4 的语义修订同版合并，版本递增由语义修订决定，合理 |

### 逐项核对

| 条目 | 结果 |
|---|---|
| SR1103 / 版本 | 0.4 有语义变化（R0、启用条件、单条目实现保留等），递增合理；修订行含版本、日期、作者、摘要；HS7/HS8 元数据纠正在第 2 轮处理表（行 50）留痕 |
| SR1403 / SR1403.1 / SR1405 / SR1405.1 / SR1410 | 通过。0.4 未引入无法追溯到 AC1–AC4 或已批准验证要求的机制；HS6 改为 deferred；`inspect env`、条目级等待/过滤仍 deferred（行 86） |
| GR104 | 通过：新逻辑放新文件 `pipeline.go`，`hooks.go` 现 523 行 |
| GR107 | 不适用（不新增 CLI 入口） |
| GR108 | 通过：装配层只构造 client，`localenv.go` 不改（行 228），与 `localenv.go:193-197` 一致 |
| GR109 | 通过：普通事件、Stop、Interrupt 失败的日志级别均已写定（行 151、154、155、D5）；R0 丢弃阶段一 block 是语义规则而非吞写入错误，并有理由（行 186） |
| GR110 | 通过：行 269 经 `internal/cli` 真实分发验证 `config set/get hooks.commands` |
| SR2004 | 通过：不新增 CLI；代码组织参照 `ReadMCPServerSettings` + Reader 分发与现有分层，依赖方向 `internal/hooks → internal/config` 无环（行 222-229） |
| 与 protected-config-keys 衔接 | 通过：行 210-213 与计划 0.4 已批准状态一致 |

## 发现

### HS9 [LOW] 第 2 轮 ledger 改写了第 1 轮 receipt 的报告指针（候选区间内的评审产物，非 subject 正文）

- disposition: `CORE_CORRECTIVE`（不属五类：Git 历史仍保留第 1 轮原文，不影响设计正确性、安全或授权；属评审留痕一致性）
- 证据: `git diff 096c9e93..46272dc5 -- docs/review/2026-11-12-multi-command-hooks/review-ledger.json`（来自 `3dbf6e5e`）把第 1 轮 Standards receipt 的 `source_report_commit_oid`/`source_report_blob_oid` 从 `4edfb889`/`46df14c2…` 改为 `f784d0eb`/`2d5bb35a…`，第 1 轮 Spec receipt 从 `4edfb889`/`d5baa3ee…` 改为 `f784d0eb`/`da1be8d2…`；后者正是第 2 轮报告的 blob（`git rev-parse f784d0eb:<path>`），与 `c51c3872` 中的原值不同。同类穷尽：ledger 中第 1 轮只有这两个 receipt 被改；顶层 `candidate` 更新为最新候选属正常；设计正文的来源引用（行 11、36）按轴固定路径，符合 review-candidate-loop，不在此类。
- 影响: review-candidate-loop 规定 receipt 的 `source_report_commit_oid` 指向“该轮提交”、旧轮原文由 Git 历史保留；review-contract 要求“旧报告保持指向旧 candidate，不回填或联锁更新”。现在第 1 轮 receipt 指向第 2 轮的报告内容，按 ledger 追溯第 1 轮结论会读到错的报告。
- 建议: 下次更新 ledger 时把这两个字段恢复为 `4edfb88989bae04f96ad2ade310aa3dac0fd8cb6` 及原 blob（`46df14c2a7958003a03bc1ad25b4be46f91df992`、`d5baa3ee68721e4e978b5827cf04d75c3f121708`）；第 3 轮写 ledger 时只追加本轮 receipt，不改前两轮。若改写是 ledger 校验工具强制的，记录工具名与报错，作为工具缺陷另开 issue。纯元数据纠正，不递增设计版本、不触发新轮次。

## 覆盖缺口与限制

- 未评 Spec/Executability：R0 与冻结锁的竞态、`PreTool` 返回错误时 `response.Runs` 是否仍有编号 0 的记录（行 161 依赖它取 attrs）、ptye2e 可执行性，留给另一轴。
- 未运行测试；行号只抽查了“输入与方法”所列位置，gofer `internal/hookrelay` 与 IDEV-STD 设计的行号未逐行核对。
- 未独立核实 ledger 校验工具是否强制 HS9 那样的改写。

## 结论

PASS（Standards/Governance 轴第 3 轮）。HS4 遗留、HS6、HS7、HS8 全部关闭；0.4 的修订（R0、单条目实现保留、`Response.Runs`、trace 挂点、启用条件与事件分流等）都能追溯到 AC1–AC4、HF13 阻断修正或 0.3 已批准的逐条 trace 验证要求，没有引入无来源的机制；事实更新（计划已批准、来源路径、D6 引用）属元数据。无开放 `CORE_BLOCKING`；新增 HS9 为 LOW `CORE_CORRECTIVE`，针对评审 ledger 而非设计正文。

## 剩余风险与后续

- HS9 在第 3 轮写 ledger 时一并恢复即可，不需要纠正确认轮。
- 计划阶段：`internal/hooks` 会多出一个流水线实现，与单条目实现共用 `commandClient.call`/`stopWait` 原语；计划任务里应写明禁止在 `pipeline.go` 重写子进程执行，防止出现重复实现（G021–G024 的审查点）。
- 上轮后续项仍然有效：`suga-hyj7` 已在实施（`8b870a79`），本计划须按行 212 在 `TestProtectedClassification` 表中补 `hooks.commands: exec`；GR110 分发用例要在“终端为真、`SUGA_SESSION_ID` 为空”的条件下写入。
