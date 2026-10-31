<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `6e03cbff`）
- 修订版本: Draft 0.4
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告（spec-review.md）
- 轮次: Round 4（变更范围评审）。读 `git diff 19f6f589..6e03cbff -- <plan>`，以及受影响的章节：计划工作声明、输入与批准证据、跨任务接口、前置检查与 fail-closed、波次与依赖、T3、T4、T5、T6、T8、可追溯性
- 日期: 2026-10-31
- 结论: **PASS**（没有开放的 CORE_BLOCKING）。附条件：S22 必须在交人工批准前处理；如果保留“取空队列”方案，批准请求里必须单独点明这项行为变化
- 排除项: 未运行测试与基准；没有逐行推演取空队列的失败回退、gate 抢占的时序（归可执行轴）

## 输入与方法

- 规范：`idevstd load --delivery semantic --accept-delta --workspace <suga> --task plan --task review --agent claude --consumer-mode --thinking-mode rigorous`。结果 BOUND、FULL、18 项资源，payload sha256 `a7be77b2…` 已核验，与上一轮一致。
- 读取：0.3→0.4 的计划 diff 和上面列出的章节；第 3 轮规范轴报告；设计 0.2 中的流程 3、R5、R8 与“锁与恢复函数”（design 85、151-152、236-240、267、270 行）；设计评审 ledger 中的 R2-F-N4。
- 结构校验：对工作区的计划（与 `6e03cbff` 一致，`git status` 干净）运行 `idevstd script validate_document --kind plan`，结果为 `ok: true, errors: []`。
- 代码核对：用 grep 和 sed 核对本轮新增的引用。

| 新增引用 | 结果 |
|---|---|
| `gate.go:105` Acquire（先抢占可抢占的持有者，再等待）、`:133` TryAcquire、`:174` TryAcquirePreemptible | 一致 |
| `compact.go:775` ConsolidateIdle 用 `TryAcquirePreemptible` | 一致 |
| `undo.go:102` UndoGate 只有 `Acquire`、`:207` withSession（gate 加跨进程锁，被占时返回 `ErrSessionBusy`） | 一致 |
| `undo_runner.go:42` gateForUndo | 一致，在 T5 的文件清单内 |
| `undo_notice.go:119` takeUndoNotice 每次只取队首一项；`runner.go:646` 每回合只调用一次 `injectUndoNotice` | 一致，与 F26 所述的现状相符 |
| `SnapshotRootStatus`（`undo.go:91`）；`CheckpointItem` 已含 `RootStatus` 字段（`undo.go:35`） | 类型存在。`rewindFileStep.Root` 与 `Item.RootStatus` 重复，见 S24 |
| `enqueueUndoNotice` 的调用方：`undo_restore.go:106`、`undo_options.go:84`；`internal/cli/undo.go` 与 `internal/chat/chat_undo.go` 都走 UndoCore | 与 S22 相关：CLI 的 `suga undo` 也写入同一队列 |
| `pkg/sugasdk/session.go:460-466` 对外方法是 `Session.Card` | 与 S17 的残留相关 |

## 验收标准

- plan 合同：思考声明、修订记录、scope freeze 与 fail-closed、波次依赖与文件不相交、任务五要素、跨任务签名、可追溯、人工 Gate。
- 评审合同：同类穷尽、五类 CORE_BLOCKING 与 necessity proof、新增机制的 scope necessity、按删除优先的顺序关闭。
- 其他规则：SR1401、SR1403、SR1405、SR1408、SR1410、SR1411、SR1301；GR104、GR109。

## 第 3 轮发现关闭状态

| ID | 状态 | 依据 |
|---|---|---|
| S17 sugasdk 行为说明与 fail-closed 冲突 | **部分关闭** | 已关闭两项：① scope freeze 改为“有回退记录的会话上 `Run`、`Compact`、`Session.Card` 反映可见状态”（24-25 行）；② fail-closed 改为“公开 API”以及“没有回退记录的会话上的任何行为”（210 行）。**未关闭（上轮未穷尽）**：T8 的 CHANGELOG 条目仍写“sugasdk `SessionCard`”，没有改用对外名 `Session.Card`，也没有补充 `Run`/`Compact` 的变化（508 行）。24 行承诺“T8 在 CHANGELOG 中写明”这三项，T8 正文只覆盖了其中一项。维持 CORE_CORRECTIVE（LOW） |
| S18 gate/锁的取法与 Busy 的归属 | **基本关闭** | 新增 `AcquireUnlessBusy` 和 `withSessionUnlessBusy`，写明抢占语义、`ErrSessionBusy`→`ErrRewindBusy` 的映射，以及“`ErrRewindBusy` 只由它产生”（161-168 行）。预览不加锁已写定。T3 的前置拒绝收为两项（349-351 行）。残留（上轮未穷尽）：T3 的验证仍写“三种前置拒绝”（356 行），应改为两种。新机制引出 S23 的依赖问题 |
| S19 `RewindFileItem` 缺 Meta | **已关闭** | 新增内部类型 `rewindFileStep{View, Item CheckpointItem, Root}`，`planRewindFiles` 和 `applyRewindFiles` 都改为传 step（124-131 行）。剩下的文字问题见 S24 |
| S20 屏蔽区间缓存 | **已关闭** | 删除了缓存，T5 第 4 步的失效逻辑和缓存用例随之删除，并写明“基准不达标时停止，回到计划修订，不在实施中临时加缓存”（298-299 行）。保留的跨实例用例（329 行）在没有缓存时只是一条回归测试，没有问题 |
| S21 文字与元数据细节 | **部分关闭** | 已关闭：计划评审 round 1–3 补齐（60-63 行）；补了 `ForkInput` 与 `RewindFileAction` 的定义（125、149 行）；files scope 只写一条事件（405 行）；`TryAcquirePreemptible` 的情况改为可抢占（S18 已覆盖）。**未关闭（上轮未穷尽）**：① “文件拆分理由”仍插在 fail-closed 列表与“容器内 git……”之间，git 那一条仍是它的子项（215-216 行）；② title 的 i18n 后缀写成“进 `chat.ini` 或 `model.ini`”，没有定下来，T6 的文件清单也没有 catalog 文件（446、433 行），并入 S23 |

## 发现

### S22 [MEDIUM] “唯一预先登记的例外”（取空 undo 通知队列）的必要性没有证明，影响面写窄了，并与 scope freeze 冲突（CORE_CORRECTIVE）

- 证据：
  - fail-closed 登记了例外：T5 把 `takeUndoNotice` 改为每回合取空队列，“连续多次 `:undo` 的通知”从每轮一条变为下一轮全部送达（211 行；T5 410 行；T8 CHANGELOG 507 行）。
  - scope freeze 同时写着“没有回退记录的会话，所有行为不变”（25 行），没有提到这个例外。两处自相矛盾。
  - 影响面比写的大。`enqueueUndoNotice` 在 UndoCore 中（`undo_restore.go:106`、`undo_options.go:84`），chat 的 `:undo` 和 CLI 的 `suga undo`（`internal/cli/undo.go`）都写同一个队列。注入发生在 `Runner.Run`（`runner.go:646`），所以 chat、`suga send`、sugasdk `Run`、ACP 的下一轮都会改变。scope freeze 写的是“不改 CLI……sugasdk 行为”，而例外实际碰到了 CLI 与 sugasdk 在无回退会话上的可见行为。例外的文字只提了 `:undo`。
  - 这项变化不在已批准设计 0.2 中。设计流程 3 与 R8（design 151-152、270 行）原本是“丢弃队列中的 undo 通知”。R2-F-N4 推翻了这一点，建议是“合并两段通知”。“输入与批准证据”中的设计偏差清单（64-67 行）列了 F-N4 的“合并”，但没有列 F26 的“取空队列”，而取空队列改变的是 rewind 以外的既有功能。
- 必要性（Review Candidate Loop：先删除，再复用，再收窄）：F26 要解决的是：先 `:undo` 再 `:rewind conversation` 时，R5 要求的“磁盘未回退”通知排在 undo 通知之后，会晚一轮送达。有不改变无回退会话行为的做法，计划没有说明为什么不选：
  - 把 rewind 通知放到队首，或在注入时优先取 rewind 项。R5 和 F-N4 都满足，现有队列格式与逐轮送达的行为不变。
  - 只有队列中有 rewind 项时才取空。
  - 按 R2-F-N4 的原建议，在入队时把 rewind 通知与已排队的 undo 通知合并为一项。
- 内部有效性（只在保留取空队列时适用）：现有 `injectUndoNotice` 在写入失败时把这一项放回队列（`undo_notice.go:166-176`）。改成多项之后，“第 k 项写入失败”时前面已写入的项与剩下的项如何处理（顺序、重复、丢失），计划没有写，这与 GR109 相关。验证中也没有“只连续 `:undo`、不 rewind”的用例，也没有“改回只取一项则用例失败”的反向验证。
- 为什么不是 CORE_BLOCKING：变化已在计划正文中登记并写进 CHANGELOG，没有绕过 Gate。它不丢数据，也可以回滚。所以只按 SR1410（默认最小方案）和 scope necessity 记为 CORE_CORRECTIVE。
- 是否需要用户在批准时明确知悉：**需要**，如果保留取空队列的话。依据 SR1401 和 SR1411：这项变化改变了既有功能（`:undo`、`suga undo`）在设计范围之外的用户可见结果。仅把它放在 fail-closed 的子项里，不能认定为用户在批准时已经知道。
- 建议（二选一，优先 A）：
  - A：改用“rewind 通知优先注入”（队首插入，或注入时优先取 rewind 项）。这样删除例外，scope freeze 第 25 行就成立，CHANGELOG 中的 F26 条目也删除。T5 的用例保留“先 `:undo` 再 `:rewind`，下一轮 R5 通知必定送达”，另加“只连续 `:undo` 时行为与现在一致”。
  - B：保留取空队列。需要做五件事：
    1. 第 25 行改为“除 fail-closed 登记的例外外，所有行为不变”；
    2. 例外的文字补全影响面：chat `:undo`、CLI `suga undo`，以及之后经 chat、`suga send`、sugasdk、ACP 的下一轮；
    3. 把 F26 加入设计偏差清单（64-67 行）；
    4. T5 写明多项写入中途失败时的处理，并补“只连续 `:undo`”的用例和“改回只取一项则失败”的反向验证；
    5. 主会话在批准请求中用一句话单独点明这项变化。

### S23 [MEDIUM] `withSessionUnlessBusy` 的归属不唯一，T6 的依赖缺失，W3“文件不相交”不成立（CORE_CORRECTIVE）

- 证据（同类全部出现点）：
  - 接口一节把 `withSessionUnlessBusy` 写在“personal：文件部分（T4）”下（131 行）。但 T4 的文件清单只有 `rewind_files.go`（363 行）。`gate.go`、`undo.go`、`undo_runner.go`（AcquireUnlessBusy、gateForUndo）则列在 T5 的文件清单中（389 行）。归 T4 还是 T5，两处说法不同。
  - T6 的 `Runner.Fork` 要在 `withSessionUnlessBusy` 内执行（435 行），但 T6 只依赖 T1、T2、T3，并在 W3 与 T5 并行（225、471 行）。T6 的 worktree 中没有这个方法，实施者只能等 T5，或者自己在 `gate.go`/`undo.go` 中再实现一份。后一种做法与 T5 改同一批文件，合入时冲突，也有重复实现的风险。
  - T6 的 title 后缀定为“进 `chat.ini` 或 `model.ini`”（446 行），没有定下来，T6 的文件清单也没有 catalog 文件。如果选 `model.ini`，就与 T5 的 `model.ini` 改动冲突（S21 残留，上轮未穷尽）。
- 影响：波次表与任务正文不能同时照字面执行。编译或合入时会立即暴露，结果不会错，所以不是 blocker。
- 建议：
  - 把 `AcquireUnlessBusy`、`withSessionUnlessBusy`，以及 `gate.go`、`undo.go`、`undo_runner.go` 中 gateForUndo 的改动移到 T4。T4 在 W1，没有依赖，与 T2 的文件不相交。然后给 T6 加上对 T4 的依赖，T5 的文件清单相应删去 `gate.go` 与 `undo.go`。也可以保留在 T5，让 T6 依赖 T5，把 T6 移到 W4。
  - title 后缀定一个文件，例如 `chat.ini`。把它加入 T6 的文件清单，并在 W3 的并行说明中写明与 T5 的 `model.ini` 不冲突。

### S24 [LOW] 文字与小处不一致（ADVISORY）

- `rewindFileStep.Root SnapshotRootStatus` 与 `Item.RootStatus` 重复，因为 `CheckpointItem` 本来就带 `RootStatus`（`undo.go:35`）。建议删去 `Root`，或注明它就是 `Item.RootStatus` 的副本（SR1403）。
- T4 正文仍写 `applyRewindFiles(ctx, sid, items)`、“只接受调用方……条目”（367 行），应改为 `steps` 与 `rewindFileStep`，与接口一致。
- 跨任务接口写“T4……调用方持有 gate 与跨进程锁”，T4 的单测要直接调用这两个不加锁的方法，可以注明测试中不取锁。
- S17 与 S18 的残留（T8 的 CHANGELOG 名称、T3 的“三种”）见关闭状态表。修正都是一行措辞，与 S22、S23 一起改即可。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，不新增入口）。
- 注入防护：已核对。`EntriesFromSkipping` 增加 types 参数，仍然是参数化的 seq 范围查询。
- 秘密不进日志或输出：已核对。取空队列只搬运已有的通知正文，事件 attrs 不变。
- 入参校验：已核对。`resolveTarget` 不变。
- 资源归属：已核对。`withSessionUnlessBusy` 按 sid 取 gate 与锁。recall 的续读 ref 改为多读一条并取它的 seq，总是指向可见条目，“ref 在区间内按不存在处理”保留（312-313 行）。

### 已核对且无问题

- 首行模板 1.3.0，状态 `Draft 0.4`。0.4 的修订记录只描述语义变化，并逐项对应到 S17–S21、F25–F31。validator 通过。
- scope freeze 的路径没有扩大。新增的 `gate.go`、`undo.go`、`undo_notice.go` 仍在 `internal/personal/**` 内，不改 `pkg/**`。
- S20 按删除优先关闭，并加了“不在实施中临时加缓存”的 fail-closed 约束，符合 SR1410。
- `AcquireUnlessBusy` 是现有 gate 的方法扩展（OWNER_EXTENSION），没有新建 Module。空闲整理被抢占沿用 `Acquire` 的既有语义，并有用例（T5 418 行）。
- 授权 Gate 不变：计划批准只授权 worktree 实施与本地提交；发版、push、主机安装单独走外部动作 Gate。
- 可追溯表新增了 S17–S21 与 F25–F31 的行，每行都指向任务和验证。缺口只有两处：S22 的设计偏差清单，以及 S17 行所说的 T8 CHANGELOG 内容与 T8 正文不一致，这两处已在上文列出。

## 覆盖缺口与限制

- 只读了 diff 和受影响的章节。未变化的章节（T0、T1、T7、Capability Discovery、回滚）沿用第 3 轮的结论。
- 未运行测试与基准，也没有验证 `AcquireUnlessBusy` 如何从 gate 状态中区分“回合持有”与“可抢占持有”（归可执行轴）。

## 结论

**PASS**（规范与治理轴，Round 4）。

- S18–S20 已关闭或基本关闭。S17、S21 部分关闭，残留都是一行措辞。
- 新发现都不是 CORE_BLOCKING：
  - S22、S23 为 CORE_CORRECTIVE，都只是收窄或调整依赖，不新增 Module、Interface、Schema 或授权；
  - S24 为 ADVISORY。

## 剩余风险与后续

- S22 优先选 A：删除例外，回到“无回退会话行为不变”。如果选 B，主会话在批准请求中要单独用一句话点明：“`:undo` 与 `suga undo` 的连续通知改为下一轮全部送达”。不能只靠 fail-closed 的子项让用户知悉。
- S22、S23 的修正属于语义变化，修正后计划应递增为 0.5。修正只是收窄内容或调整依赖，不需要新开 blocking 轮次（第 3 轮以后只为 CORE_BLOCKING 开轮），由主会话核对修正 diff 即可。
- 开工时按计划重新采集 baseline。
