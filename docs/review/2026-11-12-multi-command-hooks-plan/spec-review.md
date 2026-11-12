<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令 实施计划 评审（第 2 轮 · Spec/Executability 轴）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `3ef5f64239958d36404022be9381688565c46105`；subject path `docs/plans/2026-11-12-multi-command-hooks-plan.md`；document revision Draft 0.2（上轮 `1b7478369019c5590a760bd3739af00944a531b2` / 0.1；实施已批准设计 `docs/design/2026-11-12-multi-command-hooks-design.md` 0.6）
- 修订版本: Draft 0.2
- 评审者: 独立评审子 agent（Spec/Executability 轴），REPORT_ONLY，未改仓库、未提交
- 日期: 2026-11-12
- 排除项: 设计选择本身；Standards/Governance 轴；gofer 仓库只读；“hyj7 已合入”这一前置事实（调用方告知已成立，合并提交 `7905a0f4`）

## 输入与方法

读取：上轮本轴报告 `docs/review/2026-11-12-multi-command-hooks-plan/spec-review.md`（HPF1–HPF12）；计划 0.2 全文；设计 §1–§4 与“验证要求”。

对照代码（当前 main，含 hyj7 合并 `7905a0f4`）：`internal/hooks/hooks.go`（`New`、`commandClient.Call/StartStop/call`、`stopWait.Interrupt`）、`hooks_test.go:155-165,285-295`；`internal/chat/hooks_record.go`（`recordingHooks.StartStop` 转发协程、`recordingWait.Interrupt`）、`chat.go` 全部 `Interrupt` 调用点与 `waiting` 置 nil 逻辑（`nextChatInput`、`processChatInputEvent`、主循环、退出路径）、`followup.go:116-125`、`chat_turn.go:427-432`、`stop_background.go:38-86`、`stop_background_test.go`；`pkg/agentcore/tasks/tasks.go`（`reporter.Notify`、`Kill`、`markSeen`）；`internal/config/config_candidate.go`、`settings_registry.go` 的 `dispatchConfigReader`、`settings_protected_test.go`；`internal/cli/config.go` 的 `writeConfigValue`/`formatConfigValue`；README 与 `docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/config.md` 的 hyj7 合入后落点。

实测：临时 Go 片段（`tmp/review/suga-hooks-plan/probe`，已删除）以仓库锁定的 goccy v1.19.2 调 `MarshalWithOptions(v, Flow(true))`：`[]any{map[string]any{"command":[]any{"x"},"timeout_ms":10000}}` 与 `[]map[string]any` 两种输入都得 `"[{command: [x], timeout_ms: 10000}]\n"`。执行 `git merge-base --is-ancestor feat/protected-config-keys main` 得 `fatal: Not a valid object name`、rc=128。

## 验收标准

- plan-contract / plan-closure：任务可被只读计划与设计的实施者唯一、正确执行；验证与反向验证可执行且预期成立。
- HPF1–HPF4 关闭判据：上轮必要性证明所述失败在 0.2 文本下不再成立；HPF5–HPF11 有对应处理。
- review-contract：`CORE_BLOCKING` 仅限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 并附 necessity proof；同类穷尽。

## 上轮项复核

| 项 | 结论 | 依据 |
|---|---|---|
| HPF1 | 关闭 | 见下 |
| HPF2 | 关闭 | 见下 |
| HPF3 | 关闭 | 去掉 `case "hooks"` 后 `dispatchConfigReader` 返回 `unknown config reader "hooks"`，经 `ConfigValidationError.Error()` 原样透出，不含 `hooks.commands[0].command`，新断言必然失败；可编译 |
| HPF4 | 关闭 | 实测 goccy flow 输出与计划写定的 `hooks.commands = [{command: [x], timeout_ms: 10000}]` 逐字一致；`ValidateConfigCandidate` 与 `Load` 均经 goccy 解码，值为 `[]any`/`map[string]any`/`uint64`，同样输出；`config get`（`:60`）、`config set` 回显（`:98`）都经 `writeConfigValue`。附带：`config list`（`:138`）也经此函数，该键在 list 中同样变为 YAML，属一致性改善，计划“其他键不变”仍成立 |
| HPF5 | 关闭 | T3 两个兼容用例、T6 后台正例、T7 写明 R0 反向验证由 T5/T6 承担；已核 `reporter.Notify` 的通知不被 `Kill→markSeen` 删除（只删 `text==""`），故 T6 “去掉 R0 → HF13 用例失败”预期成立 |
| HPF6 | 关闭 | T3 文件含 `pipeline.go` 骨架，`newPipeline(compat, entries, compatWaiting)` 写定；`compatWaiting` 与设计 HF17“`stop_wait_sec>0` 或 `stop_background`”一致 |
| HPF7 | 关闭（文字小瑕疵见 HPF16） | 假进程合同已写定标签、模式、日志与同步点 |
| HPF8 | 关闭 | T1 文件与旧版本核对项已改；分支②去向已写 |
| HPF9 | 关闭 | 合入后 README `:101` *exec* 清单、usage `:140` “- 执行类”、`config.md` `hooks.*` 表存在，T8 的 grep 能命中对应行 |
| HPF10 | 关闭 | 冻结路径 `Err=context.Canceled` 写定 |
| HPF11 | 关闭 | 容器、数字类型与顺序写全，覆盖 goccy 的 `uint64`/`int64` |
| HPF12 | 关闭（移交 Gate 1） | 执行者改为计划 Gate 指定 |

**HPF1 详细核对**。(a) 消费者等价：chat 的全部 `Interrupt` 调用点（`chat.go:542,769,776,813,820,839,845`、`followup.go:121`、`chat_turn.go:430`）都以 `waiting != nil` 为前提；`nextChatInput` 从 `stopDone` 读到结果后立即返回 `stopDone:true`，主循环（`chat.go` `if interrupted … else if state.waiting != nil && stopDone`）置 `waiting=nil`，之后不再 Interrupt；`stop_background.go` 只读 `Done` 不 Interrupt。所以 pipeline 处于 `finished` 而 `Interrupt` 被调用时，chat 必未读到该结果，“一律发给 `last`”与设计 §4 打断 2“全部结束、结果未被消费 → 最后执行的条目”等价。(b) 不挂起：`done` 缓冲 1、只写一次再 close，`Interrupt` 只等 `finished`（在 `done` 写入之后 close），转发协程是否取走值与 `Interrupt` 无关。(c) 不重复：`finished` 态 pipeline `Interrupt` 返回非空 reason，`recordingWait.Interrupt`（`hooks_record.go:112-125`）只在 `reply==""` 时补取，不重复；`running`/冻结态 `done` 为 R0（`Err!=nil`），补取条件 `r.Err==nil` 不成立，不重复。(d) 返回值规则“全部已完成条目中 `Err==nil` 的 block reason”对 R1（阶段一全部 block）、R2（阶段一无 block，只剩等待条目）与设计 §4 打断 4 一致。

**HPF2 详细核对**。四个钩子点全部出锁调用，`pointBeforeDone` 在状态置 `finished` 之后。五个用例：running 子态——流水线协程阻塞于 `pointEntryExited`（状态仍 `running(A)`），`Interrupt` 冻结并以 target=A 调 `pointInterruptFrozen` 后测试才放行，冻结必先于结束临界区，结果确定为 R0 且返回 A 的 reason；between——阻塞于 `pointBeforeStartNext`（状态 `between`），放行信号是 `pointInterruptFrozen(-1)`，冻结必先于再取锁，B 确定不启动；finished——先读 `done` 再 Interrupt，无竞态；HF21/HF29——在 `pointEntryExited`/`pointBeforeDone` 内同步取消父 ctx（子 ctx 的取消在 `cancel()` 返回前完成），判定时点确定。均不需 sleep。

## 发现

### HPF13 [MEDIUM] T5 状态机未写初始状态、阶段二“下一条”判定与冻结拦截后的收尾（CORE_CORRECTIVE）

- 证据: 计划 T5 状态只有 `running(i)`/`between`/`finished`，转换只写“条目返回后”与“有下一条时”；第一条启动前的状态未写。chat 在 `StartStop` 返回后立即可能 Interrupt（输入活动、投递、Tab 排队、回合内插入都会调）。“有下一条”是否包含阶段二等待条目、何时包含（阶段一无 block 且 `compatWaiting`）只在 `StartStop` 一句里，状态机段未写。冻结拦截路径“按上一条生成 R0 结果并结束”未写“结束”是否同完成路径一样置 `finished`、调 `pointBeforeDone`、写 `done` 后 close、再 close `finished`。
- 影响: 初始状态若实现为零值 `running(0)`，打断会向尚未启动的条目发 Interrupt（对 gofer 是一个没有 Stop 的 Interrupt），若实现为 `between` 则不发——两种实现结果不同，五个用例都不覆盖此窗口。拦截路径漏 close `finished` 时 `Interrupt` 永等，T5 between 用例会以超时失败暴露，不会静默漏过，故不升级为阻断。
- 建议: 写定：`StartStop` 起协程前状态为 `between`（下一条为第一条），第一条与后续条目同样“取锁查 `frozen` → 置 `running`”；“有下一条”= 阶段一尚有条目，或阶段一结束、无 block 且 `compatWaiting`；拦截路径与完成路径共用同一收尾（置 `finished`、出锁调 `pointBeforeDone`、写 `done`、close `done`、close `finished`）。可选加一例：`StartStop` 后在第一条 `start` 前打断（钩子在第一条的 `pointBeforeStartNext` 阻塞）。同类穷尽：初始态、阶段一→阶段二、冻结拦截收尾三处；其余转换计划已写定。

### HPF14 [MEDIUM] 设计“两条目之间 → 已得 reason 交回且不重复投递”未落到断言（CORE_CORRECTIVE）

- 证据: 设计“验证要求”单元（`internal/hooks`）项：“两条目之间 → 无条目收 Interrupt、等待条目不启动（HF3）、已得 reason 交回且不重复投递”。计划 T5 between 用例未规定 A 的模式（`block`），只断言无 Interrupt、B 与等待条目无 `start`、`done` 为 Canceled，不断言 `Interrupt` 返回值；T6 包装下的打断只有 running（等待条目）与 finished 两例。
- 影响: between 态 reason 交回这一设计验证项无直接证据；可追溯表“打断三态、HF3、HF25”标为覆盖。
- 建议: T5 between 用例写定 A 为 `block`，断言 `Interrupt` 返回 `A-reason`、`done` 结果 `Err!=nil`（据此 `recordingWait` 不会补取，即不重复）。HF28（running 子态）用例已断言返回 A 的 reason 与 `done` 为 R0，同理成立，无需新增。

### HPF15 [LOW] 前置条件命令引用已删除分支，照字面执行即 fail-closed（CORE_CORRECTIVE）

- 证据: 计划“前置条件”写 `git merge-base --is-ancestor feat/protected-config-keys main` 为真；hyj7 合并（`7905a0f4`）后该分支已删除，实测 `fatal: Not a valid object name feat/protected-config-keys`、rc=128。
- 影响: 前置事实本身已成立（不作为发现）；但按计划“不满足时不建 worktree、不开始 T2”，照字面执行会停在 T2 前。同一条目的另两个判据（issue 关闭、`ConfigKey.Protected` 与 `TestProtectedClassification` 存在）可替代，影响有限。
- 建议: 改为 `git merge-base --is-ancestor 7905a0f4 main`（或记为“已满足：合并提交 `7905a0f4`”），并顺带把“workspace baseline”中 hyj7 实施中的描述更新。同类穷尽：计划中引用 `feat/protected-config-keys`/`.worktrees/hyj7` 的只有 baseline 与前置条件两处。

### HPF16 [LOW] T6 两个用例的同步方式与假进程合同个别措辞不唯一（CORE_CORRECTIVE）

- 证据: (1) T6 finished 用例“等 `recordingWait.Done()` 可读但不读”——Go 没有不接收的可读判断，可行办法是测试内对 `*recordingWait` 断言后轮询 `len(w.out)==1`，计划未写。(2) T6 HF13 用例中第二个 Stop 跑同一配置，A 再次 block、B `wait`，B 若用默认 `hooks.timeout_ms`（3000）超时后第二个流水线按 R1 发出同文 `Stop hook asks to continue: A-reason`，“无该通知”的断言只有在 3s 内做完才成立；计划未写 B 的 `timeout_ms` 与按任务 ID 区分通知。(3) 假进程合同写“`mode` 只作用于 Stop”，随后 `timeout` 作用于全部事件、`ifail` 作用于 Interrupt，自相矛盾。
- 影响: 实施者各自选择；(2) 在慢机器（Windows 主机）上可能偶发失败。
- 建议: (1) 写明轮询 `len(w.(*recordingWait).out)==1`（期限 10s）；(2) 写明 HF13 中 B 的 `timeout_ms` 不小于 30000，`b.start` 返回（`Kill` 已等旧任务结束）后立即断言，`t.Cleanup` 调 `taskManager.Close()`，或按第一个任务 ID 过滤通知；(3) 改为“`mode` 决定 Stop 行为；`timeout` 与 `ifail` 另作用于全部事件 / Interrupt”。

## 覆盖缺口与限制

- 未运行仓库测试或构建；结论来自代码阅读，外加一个 goccy 输出片段与一次 `git merge-base`。
- 返回值规则在“`finished` 且结果为 R0（前台父 ctx 被取消）”时会交回已完成条目的 block reason（现状单条目在同情形返回空）；这与设计 §4 打断 4“已完成条目中 R1 意义下的 block reason”一致，只影响 chat 退出时的“未投递”告警文本，不列为发现。
- 未评 Standards/Governance 轴与设计选择本身。

## 结论

PASS。上轮阻断项 HPF1–HPF4 全部关闭，无开放 `CORE_BLOCKING`。新增 HPF13、HPF14（MEDIUM）与 HPF15、HPF16（LOW）均为 `CORE_CORRECTIVE`，建议在计划批准前一并修订（均不改设计语义）。

## 剩余风险与后续

- HPF13 的初始状态写定后，T5 用例集可选加“第一条启动前打断”一例。
- 修订仅涉及 T5/T6 文字与前置条件，按计划内修订处理，无需再走完整一轮评审；是否复核由 ledger 决定。
