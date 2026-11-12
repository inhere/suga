<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令设计 第 4 轮 Spec/Executability 评审（纠正确认轮）

## 评审目标与范围

- 目标: `inhere-tools/suga` 仓库（Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`）`docs/design/2026-11-12-multi-command-hooks-design.md`，candidate commit `c099198b7fdd17d3a3bbfbc6e787ab80c60f0f90`（上轮 `46272dc5e8bceb9729badfc7202b9f327c7518a1` / Draft 0.4，两轴 PASS），issue `suga-5xmf`
- 修订版本: Draft 0.5
- 评审者: 独立 Spec/Executability 轴评审 agent（Claude，REPORT_ONLY，未改仓库、未提交）
- 日期: 2026-11-12
- 排除项: 全量重审；Q1–Q7/D7–D13 的选择本身；Standards/Governance 轴；gofer `internal/hookrelay` 无 `--wait` 路径的实际行为（设计已留到计划阶段核对）；IDEV-STD 续跑 hook 实现

## 输入与方法

- 规范：review-contract 与 review 模板取自 `tmp/idev-std/d/8Yz1eltnU9GuyZy8zAtfgA/payload.md`；轮次规则（全 PASS 且含 `CORE_CORRECTIVE` 后的单个纠正确认轮）取自 `tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md`。
- 上轮本轴报告 `docs/review/2026-11-12-multi-command-hooks/spec-review.md`（HF21–HF25，全部 `CORE_CORRECTIVE`）。
- `git diff 46272dc5..c099198b -- <subject>`（+30/−11，集中在修订记录、第 3 轮处理表、§3 trace 载体、§4 R0 与打断、架构 `hooks.New`、gofer 影响、验证要求、结论）。
- 代码（工作树 HEAD = 候选，`internal/`、`pkg/` 未改）：`internal/chat/stop_background.go` 全文；`pkg/agentcore/tasks/tasks.go:302,417,457,658-667,777-799,826-840`；`internal/cli/run.go:180-195`；`internal/hooks/hooks.go:51-61,140-275,296-330,395-453`；`internal/chat/hooks_record.go:105-130`；`internal/wiring/sessionenv.go:30-60,100-110`、`runtime.go:38-40,481`；`pkg/sugasdk/session.go:102`、`types.go:78-83`；`internal/cli/chat.go:21`、`internal/wiring/localenv.go:185-198`；`internal/hooks/hooks_test.go:150-166,280-296`；全仓 `hooks.New` 与 `NoSessionEnv` 调用点。
- 方法：逐项核对 HF21–HF25 修订的事实与可实现性；把修订后的新规则与设计中未改动的承诺（D2、验证“现有断言不改”、§4 第 3/4 步）交叉比对，找修订引入的矛盾；核对新增 gofer 影响段与 §4 阶段顺序是否一致。

## 验收标准

- review-contract：四级 severity + disposition；`CORE_BLOCKING` 限 BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY 并附 necessity proof；同类穷尽；无阻断才 PASS。
- 纠正确认轮只判定：HF21–HF25 是否正确关闭、修订是否引入新的 `CORE_BLOCKING` 或内部矛盾。

## 上轮发现关闭情况

| 上轮 | 状态 | 证据 |
|---|---|---|
| HF21 | 关闭（残留 HF29） | `:189` R0 条件、`:197` 同类穷尽列出三个取消入口；行号核实：`tasks.go:302`（`WithCancel(context.Background())`）、`:417`（`AfterFunc(... end(TimedOut))`）、`:658` `Kill`、`:777` `KillSession`、`:788` `Close`、`:836` `end` 中 `t.cancel()`；`run.go:193` `defer taskManager.Close()`；`stop_background.go:67-69` 为 `ctx.Err() != nil → -1, nil`。`:198` “有意收紧”写明；验证 `:285`、`:289` 补齐。收紧的判定时点见 HF29 |
| HF22 | 关闭（残留 HF27） | `:240` opts 应用到每个条目各自的 `commandClient`；`Option` 为 `func(*commandClient)`（`hooks.go:150`），新条目复用 `commandClient` 作执行原语即可直接应用，可实现。`NoSessionEnv` 的表述不准，见 HF27 |
| HF23 | 关闭 | `:240` “`hooks.command` 为空时不构造兼容条目、不解析其配套键”，与现状 `hooks.go:216-218` 的短路等价；验证 `:282` 可直接用 `gkconfig.Set("hooks.stop_args", <非列表>)` 构造 |
| HF24 | 关闭，但引入矛盾 HF26 | `:169` 成功/失败/超时/取消都填 `Runs`；与“现有 `hooks_test.go` 断言不改”冲突，见 HF26 |
| HF25 | 关闭（残留 HF28） | `:208-212` 三态 + 同锁状态转换 + “启动前再取锁检查冻结”，三态互斥且覆盖上轮列出的 ①–④ 全部状态；测试钩子注入点（`:285`）在同包未导出变量上可实现。“运行中（进程已退出）”状态下第 4 步交回值未写，见 HF28 |
| gofer 影响新增段 | 准确 | `:274` 与 §4 `:179`（兼容即时条目排阶段一最前）、HF17 按键判定（未设 `stop_wait_sec` 且未开 `stop_background` 才是即时型）、HF25 三态一致：即时兼容条目只在打断恰落在它运行时收 Interrupt，全部结束时发给最后执行的新条目；后台模式不走 Interrupt。gofer 侧行为留计划核对，表述为待核而非断言，合适 |

## 发现

### HF26 [MEDIUM] HF24“失败也填 `Runs`”与“现有 `hooks_test.go` 断言不改”/D2 直接矛盾

- disposition: `CORE_CORRECTIVE`
- 证据: 设计 `:169` 单条目实现在失败路径也填一条 `EntryRun`；`:281` 兼容验证“现有 `hooks_test.go`、`hooks_pretool_test.go` 断言不改”，`:296` D2“现有测试断言不改”。现有 `hooks_test.go:164` `assert.Eq(t, Response{}, h.Call(..., SessionStart))`（命令不存在的失败路径）与 `:293` `assert.Eq(t, Response{}, h.Call(..., UserPromptSubmit))`（超时路径）断言失败时返回整体零值；按 `:169` 返回值将含 `Runs`（且含耗时，非确定），两条断言必然失败。
- 影响: 计划无法同时满足 HF24 与“断言不改”：要么改这两条断言（违反 `:281`/D2 字面），要么失败时不填 `Runs`（回到 HF24 的缺口）。运行时行为唯一（`:169` 明确），用户可见结果不受影响。
- necessity proof（为何不是 `CORE_BLOCKING`）: 冲突只在验证措辞，不导致结果错误、数据丢失或越权；任一解法下运行时行为相同；一句话即可消除。
- 同类穷尽: 全仓断言整体 `Response` 相等且经过 `commandClient` 的只有 `hooks_test.go:164`、`:293`（`grep "assert.Eq(t, Response"`）；`internal/chat` 测试里的 `Response{...}` 都是桩构造的输入，不经单条目实现，不受影响；`Response` 已含 map 字段，不存在 `==` 比较点。
- 建议: `:281`/D2 补例外“`hooks_test.go:164`、`:293` 改为逐字段断言回答为空，并断言 `Runs` 恰一条、`entry=0`、错误非空（HF24）；其余断言不改”。

### HF27 [LOW] `NoSessionEnv` 的描述与验证不准：它不经过 `suga chat`，且 `SUGA_SESSION_ID` 有 fallback

- disposition: `CORE_CORRECTIVE`（上轮 HF22 建议措辞带入，本轮修订照写；按“上轮未穷尽”记）
- 证据: `:240` “`NoSessionEnv` 同样作用于全部条目”，`:283` “`NoSessionEnv` 时全部条目都不导出”。`NoSessionEnv` 只由 `pkg/sugasdk/session.go:102` 传入 `wiring.Options`；`hooks.New` 的调用点只有 `internal/cli/chat.go:21`（传 `runtime.SessionEnv`）与 `wiring/localenv.go:193`（仅判断是否启用），SDK 路径不构造 hooks。另外 `hookEnv`（`hooks.go:429-436`）在 `extra` 返回空时仍以事件 `session_id` 补 `SUGA_SESSION_ID`。
- 影响: `:283` 的“全部条目都不导出”在 chat 中不可达，且即使用空 `WithExtraEnv` 模拟也会导出 `SUGA_SESSION_ID`；照字面写测试会失败，或诱导实现去掉 `hookEnv` 的 fallback，改变兼容条目现状（违反 D2）。
- 同类穷尽: 设计中提到 `NoSessionEnv` 的只有 `:58`（处理表）、`:240`、`:283` 三处。
- 建议: 删去 `NoSessionEnv` 两处表述，改为“各条目的环境与兼容条目一致：`WithExtraEnv` 提供的变量加 `hookEnv` 的 `SUGA_SESSION_ID` fallback（同现状）”；验证只保留新条目收到 `SUGA_SESSION_NAME`/`SUGA_TURN_ID`。

### HF28 [LOW] “运行中（进程已退出、结束临界区未执行）”状态下，第 4 步交回值是否包含该条的 block 未写

- disposition: `CORE_CORRECTIVE`
- 证据: `:209` 新增该状态归“运行中”→ 发 Interrupt 给它；`:213` 第 3 步取消；`:214` 第 4 步只交回“已完成条目中 R1 意义下的 block reason，或等待条目已到但未消费的 block 回复”。处于该状态的条目按状态机尚未标记完成，是否算“已完成/已到”有两种读法。现状单条目 `stopWait.Interrupt`（`hooks.go:405-420`）在取消后 `<-w.done` 等结果，`Err==nil && block` 即交回。
- 影响: 若读为“未完成”，等待型兼容条目（gofer `--wait`）恰在用户输入瞬间拿到 web 回复时，该回复被丢弃，而单条目现状会交回；窗口很窄（且取消后 `exec` 可能报错，现状同样非确定），故为 LOW。
- 同类穷尽: 打断时有已产生但未合并结果的状态只有“运行中”的这一子态；“全部结束”与“两条目之间”的结果都已在临界区内落定，交回值明确。
- 建议: 第 4 步补“取消后等运行中的条目结束；其结果 `Err==nil` 且为 block 时一并交回（同 `stopWait.Interrupt` 现状）”，验证“运行中（进程已退出）”用例断言该 reason 被交回且不重复投递。

### HF29 [LOW] R0 的判定时点未写：“被杀瞬间恰好完成也丢弃”只在合并前取消时成立

- disposition: `CORE_CORRECTIVE`
- 证据: `:189` R0 条件“流水线 ctx 已取消”、`:198` “R0 只看 ctx，此时也丢弃”；`:213` 第 3 步“`Done` 结果按 R0”。流水线把合并结果写入 `Done` 之后才被取消时（结果已在缓冲通道里），`stop_background.go:63-79` 读到 `Err==nil` 的 R1 仍会 `Notify`、置 `blocked`——与现状单条目同一竞态窗口；`:213` 对“全部结束未消费”状态也不应改写为 R0（上轮 HF13 核对表即按“保持已产生的 R1/R2/R3”通过）。
- 影响: 设计文字暗示收紧对任何时点都成立，计划可能为“合并后被杀”写出无法稳定通过的测试，或在 `stop_background` 加 ctx 复查（与架构 `:243`“只消费合并结果，逻辑不变”冲突）。结果对既定窗口是唯一的，残余窗口与现状一致，不阻断。
- 同类穷尽: 读合并结果的点：前台 `Done`（打断后不再读）、后台 `run`、`stopWait/recordingWait.Interrupt` 补取；仅后台 `run` 存在合并后取消仍消费的窗口。
- 建议: `:189`/`:198` 写明“R0 在最后一条的结束临界区（判定全部结束、生成合并结果时）检查 ctx；结果写入 `Done` 后的取消不改写结果，残余窗口同现状”；`:213` 改为“运行中被打断时 `Done` 按 R0；全部结束未消费时保持已产生结果”；`:285` 的“恰好正常完成”用例经测试钩子在条目结束与合并之间取消。

## 覆盖缺口与限制

- 静态阅读，未运行代码或测试；状态机与竞态由代码推演。
- 按纠正确认轮只读 diff 与依赖章节；§1、§2（除引用）、§5、§6、D1–D13 未变，沿用上轮结论。
- 未读 gofer `internal/hookrelay` 无 `--wait` 路径；设计已把该核对列为计划阶段事项。
- Standards/Governance 轴不在本轴范围。

## 结论

PASS：HF21–HF25 均已按建议修订并可实现，新增 gofer 影响段与 §4 阶段顺序、HF17 按键判定、HF25 三态一致。本轮无 `CORE_BLOCKING`。修订引入一处验证措辞矛盾（HF26，MEDIUM）与三处精确化缺口（HF27–HF29，LOW），全部 `CORE_CORRECTIVE`，运行时结果唯一，不触发新轮次。

## 剩余风险与后续

- 本轮为唯一纠正确认轮，之后不再评审：HF26–HF29 若由作者修订，按“未复核修订”在计划 Gate 披露；建议至少在计划的验证任务中直接落实 HF26（两条断言的例外）与 HF27（删去 `NoSessionEnv` 断言），否则计划 T 级验收会自相矛盾。
- 修订时按类自查：①所有断言整体 `Response` 相等的测试（HF26）；②所有提到会话环境变量开关的位置（HF27）；③打断与取消后每个读结果点的交回值与判定时点（HF28、HF29）。
- 即时兼容条目收 Stop 不收 Interrupt 对 gofer web 侧的影响仍待计划阶段核对 `internal/hookrelay`（设计 `:274` 已登记）。
