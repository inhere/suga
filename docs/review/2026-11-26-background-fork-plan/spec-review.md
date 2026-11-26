<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）实施计划 Spec/Executability 评审（第 3 轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `1a8331ed`；subject `docs/plans/2026-11-26-background-fork-plan.md`；document revision Draft 0.3（设计引用 Draft 0.5）
- 修订版本: Draft 0.3（上一轮评审 `806af9b2` / Draft 0.2）
- 评审者: 独立评审子 agent（Spec/Executability 轴），未参与计划编写与修订
- 日期: 2026-11-26
- 排除项: Standards/Governance 轴；设计决策本身；PF10–PF14 以外的章节（第 3 轮为闭合确认轮，用户原话“hao6 Q6 选 A”）；REPORT_ONLY，不修改仓库、不提交

## 输入与方法

- 读取：review-contract 与 review 模板（`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`）、上一轮本轴报告（`1a8331ed:docs/review/2026-11-26-background-fork-plan/spec-review.md`，PF10–PF14）、`git diff 806af9b2..1a8331ed -- docs/plans docs/design`。
- 对照 candidate 源码静态核对：`internal/personal/observe.go`（recorder、`attemptObserver`、purpose 分支）、`internal/personal/runner.go:455-490`（recorder 在 `personal.New` 中构造、各 provider 包装）、`runner.go:733-760`（唯一 `runtime.Run` 调用点与 `r.finish`）、`runner_sessions.go:66`（`:model` 重新包装沿用同一 recorder）、`internal/wiring/subagent.go:185-215`（子 Runner 配置，不传 `Hooks`）、`pkg/agentcore/tasks/tasks.go:245,261-284,778`（`Limits`/`Start`/`Running`）、`pkg/agentcore/tools/native/agent.go:69-76`（`AgentTool.tasks`）、`pkg/agentcore/runtime.go`（`RunCompleted`）、设计 F7、验证要求与用户可见差异中所有 host hook / ACP 文字。
- 未实跑测试（本轮修订只改文字合同，PF14 的 characterization 已在第 2 轮实跑）。

## 验收标准

- 上轮 CORE_BLOCKING（PF10、PF11）是否闭合；PF12–PF14 与 PF8 附注（`observeTurnEnd` 调用点、`completed` 判定）是否正确可执行。
- 修订是否引入新的 CORE_BLOCKING 或计划/设计/可追溯性之间的矛盾。
- review-contract：五类 CORE_BLOCKING + necessity proof；只报影响核心验收或安全的问题。

## 发现

### 上轮发现闭合情况

| 发现 | 结论 | 依据 |
|---|---|---|
| PF10 | 闭合 | T2 文件已含 `internal/personal/observe.go`，删去 `event.Metadata` 说法。可执行性：recorder 在 `personal.New` 内构造（`runner.go:467-469`），此时 `cfg` 可读，T3 在 `RunAgent` 构造子 Runner 时即传入 `Config.ForkSeed`（每个分身各自 `store.Open` + `personal.New`，`wiring/subagent.go:185-189`），recorder 可在构造时取 `Reuse/ReuseNote`。`attemptObserver` 已按 `purposeFrom(ctx)` 分支（`observe.go:169-186`），无 purpose 即主循环调用；压缩调用带 `purposeCompact`（`compact.go:640,659`），子 Runner 不配 Idle/Handoff provider，“第一个无 purpose 的 model 事件”就是分身首个主循环请求；`:model` 重新包装沿用同一 recorder（`runner_sessions.go:66`），首次标记不会重置。写入的 attr 进 `store.Event.Attrs` 落库，`inspect trace --json` 与 T2 单测（读 repo 事件）都能看到，T2/T7 的 `fork_reuse` 断言可通过。可追溯性 F4 行 T2 → `TestForkContextMode` attr 一致 |
| PF11 | 闭合 | T1 `ForkSeedForCall` 改为“不判 host hook（模型触发一律用当步请求，ACP 的 TurnGate 照常启动）”，T1 验证改为“配置 host hook（或 ACP TurnGate）时仍启动且 `Reuse=prefix`”。分身子 Runner 本身不带 `Hooks`（`wiring/subagent.go:189-215`），ACP gate 不会进入分身。`:bg`：T5 表第 1 行“配置了 host hook（任何情形）→ `host_hook`”、T5 验证“正常回合后与 `:compact` 后配置 host hook 均为 `host_hook`”、T6 `TestForkBg` host_hook 文案不变。一致性：处理表 PF8 行注明只针对 `:bg`、PF11 行撤回；可追溯性 F7 行改为“模型触发不判 host hook；`:bg` 遇 host hook 拒绝”；设计 F7（design:189）“模型触发总用当步请求（`prefix`）”、关键流程 1“chat / run / ACP 相同”、验证要求 design:215 与用户可见差异 design:224 均只在 `:bg` 语境下拒绝。三者一致，无矛盾 |
| PF12 | 闭合 | `ForkUnavailableError{Reason, hint string}` 只在 personal 内由 Runner 构造，`r.cfg.Translator` 已存在（`runner.go:795` 有先例）；`Hint()` 返回译文。native 按 `Hint() string` 接口追加，chat 经 `errors.As` 取 `Hint()`，两条路径看到同一译文 |
| PF13 | 闭合 | `AgentTool` 持有 `tasks *tasks.Manager`（`agent.go:69,76`），`Running(sid) []Info`（`tasks.go:778`）与 `Limits().MaxRunning`（`tasks.go:245`）可用于预检，与 `Start` 的按会话计数口径一致（`tasks.go:274-284`）。`*ForkLimitError{Running, Limit}` 由 native 返回、chat 用 `errors.As` 取字段填 `cmd.bg.full` |
| PF14 | 闭合 | T1 写明 characterization 自带保留原始消息的 storage 包装、不改 `runtime_doubles_test.go`，参考文件 `tmp/review/suga-fork-plan/r2-characterization_test.go.txt` 存在；回放命令先 `mkdir -p`，路径为 WP 级 `tmp/suga-hao6/base`，`00c890f7` 是可解析的 commit，WP 根与 `tmp/` 无 `go.work`，解包目录作为独立 module 运行不受干扰；已注明不用 PowerShell 5 管道 |
| PF8 附注 | 正确 | `runner.go:748` 是 personal 内唯一的 `runtime.Run` 调用点，其后 `appendTurnOutput` 与 `r.finish`（`runner.go:756-759`），计划写的“`runtime.Run` 返回之后、`r.finish` 之前”有唯一位置，T1 文件栏已补“`runtime.Run` 返回后一处”。`completed = runErr == nil && coreResult.Status == agentcore.RunCompleted`：`RunCompleted` 存在（`runtime.go:794,813`），`&&` 短路保证 `runErr != nil` 时不解引用；取消回合 `runErr` 非空 → `Completed=false`，与 T5“取消或失败回合 → `rebuilt`”一致 |

### 新发现

无。本轮修订未引入新的 CORE_BLOCKING 或 CORE_CORRECTIVE（编号 PF15 起未使用）。

## 覆盖缺口与限制

- 只做静态核对，未运行测试；PF10 的“首个无 purpose 事件”按当前子 Runner 配置（无 Idle/Handoff provider）成立，若实施时给分身加了不带 purpose 的辅助调用，需要同步复核。
- 只复核 PF10–PF14 及其依赖文字（T1、T2、T4 满额、T5 表、T6、完成定义 1/2、可追溯性 F4/F7 行、设计 F7 与验证要求）；未重审其余章节与 T8 文档内容。
- 安全（SR1301）：本轮修订不改只读白名单、`forkDenyTools` 与父策略快照合同；PF11 删除的是一条拒绝判定，分身子 Runner 不继承 host hook 也不扩大工具面，未发现新的越权路径。

## 结论

PASS。PF10、PF11 两个 CORE_BLOCKING 已闭合，PF12–PF14 与 `observeTurnEnd` 调用点 / `completed` 判定正确可执行，无开放 CORE_BLOCKING，无新发现。

## 剩余风险与后续

- 以下两点不影响核心验收，留给实施时注意（不作为发现）：
  - T4 先用 `Running`/`Limits` 预检、再调 `Manager.Start`，两者之间有竞争窗口；并发派分身恰好撞满时会退回 `Start` 的未分类英文错误，仍会拒绝（D5 不受影响），只是 `:bg` 显示 `cmd.bg.unavailable` 而不是 `cmd.bg.full`。
  - T8 文档“拒绝启动的情形（host hook、…）”写的时候要注明 host hook 只影响 `:bg`，别写成模型触发也会拒绝。
