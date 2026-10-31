<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `0d6737d7`）
- 修订版本: Draft 0.5
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告（spec-review.md）
- 轮次: Round 5（变更范围评审）。读 `git diff 6e03cbff..0d6737d7 -- <plan>`，以及受影响的章节：修订记录、输入与批准证据（设计偏差清单）、跨任务接口（文件部分与并发判定）、fail-closed、波次与依赖、T2（recall）、T3 验证、T4、T5（文件与通知）、T6、T8 CHANGELOG、可追溯性
- 日期: 2026-10-31
- 结论: **PASS**（没有开放的 CORE_BLOCKING）。S25、S26 为 CORE_CORRECTIVE，交人工批准前按一行级修正处理即可
- 排除项: 未运行测试与基准；没有逐行推演 recall 续读与 Ledger 注入的时序（归可执行轴）

## 输入与方法

- 规范：`idevstd load --delivery semantic --accept-delta --workspace <suga> --task plan --task review --agent claude --consumer-mode --thinking-mode rigorous`。结果 BOUND、FULL、18 项资源，payload sha256 `a7be77b2…` 已核验，与上一轮相同。
- 读取：0.4→0.5 的计划 diff 和上面列出的章节；第 4 轮规范轴报告；设计 0.2 的流程 1（fork 建会话，129 行）、流程 3 后台任务（151-152 行）、决策表 R5 与 R8（266、269 行）；设计评审 ledger 中的 R2-F-N4。
- 结构校验：工作区的计划与 `0d6737d7` 一致。`idevstd script validate_document --kind plan` 结果为 `ok: true, errors: []`，共 596 行，不需要拆分。
- 代码核对：

| 引用 | 结果 |
|---|---|
| `undo_notice.go:106-117` appendUndoNotice | 追加到**队尾**。`injectUndoNotice` 写入失败时用它放回（`:169-178`）。与 S26 相关 |
| `undo_notice.go:119-131` takeUndoNotice | 每次取 `notices[0]`，与计划所述相符 |
| `undo_core_test.go:17` `undoTestGate` | 存在，T4 补方法的位置正确 |
| `internal/i18n/catalog/{zh,en}/personal.ini` | 存在，T6 的 title 后缀落点可用 |
| `ledger.go:74` `bind`；`runner.go:444` `cfg.Ledger.bind(...)` | Ledger 由 Runner 在 personal 内绑定，“Runner 注入 maskedRanges”可以不改 wiring。T2 的文件清单没有 `runner.go`，见 S27 |
| `wiring/runtime.go:531`、`wiring/manifest.go:609` NewLedger | 只构造，不需要改动，scope freeze 成立 |

## 验收标准

- plan 合同：修订记录、scope freeze 与 fail-closed 一致、波次依赖与文件不相交、任务五要素、跨任务签名的归属、可追溯、设计偏差的登记、人工 Gate。
- 评审合同：同类穷尽、五类 CORE_BLOCKING 与 necessity proof、新增机制的 scope necessity、按删除优先的顺序关闭。
- 其他规则：SR1401、SR1403、SR1406、SR1410、SR1411、SR1301；GR104、GR109。

## 第 4 轮发现关闭状态

| ID | 状态 | 依据 |
|---|---|---|
| S17 残留（T8 CHANGELOG 名称与内容） | **已关闭** | T8 的 CHANGELOG 改为 `Session.Card`，并补了 `Run` 与 `Compact`，限定在“有回退记录的会话”上（511 行），与 scope freeze 第 24 行一致 |
| S18 残留（T3“三种前置拒绝”） | **已关闭** | 改为“两种前置拒绝”，并写明回合进行中由 T4 的取锁负责（359 行） |
| S21① 文件拆分理由的位置 | **未关闭（上轮未穷尽）** | “文件拆分理由”已提成独立段落（217 行），但“容器内 git……”一条仍然缩进挂在它下面（218 行），没有回到“运行目录与环境”中。并入 S27 |
| S21② title 后缀落点 | **已关闭** | 定为 `personal.ini`，已加入 T6 文件清单（437、451 行）；W3 写明 T5 用 `model.ini`、T6 用 `personal.ini`（227 行） |
| S22 取空队列例外 | **已关闭（按方案 A）** | 删除了 fail-closed 中的例外与 CHANGELOG 中的 F26 条目，改为 rewind 通知插入队首，`takeUndoNotice` 不改（413-414 行）。scope freeze 第 25 行“没有回退记录的会话，所有行为不变”现在成立。验证改为“下一轮 rewind，再下一轮 undo”，并保持 `TestUndoNoticeQueueKeepsOrder` 通过（422 行）。新机制留下两处缺口，见 S26 |
| S23 取锁归属与 T6 依赖 | **基本关闭** | `AcquireUnlessBusy`、`withSessionUnlessBusy`、`gateForUndo` 与测试桩都移到 T4（366-369 行）；T5 的文件清单删去 `gate.go` 与 `undo.go`（393 行）；T6 依赖 T4（227、475 行）。W1 中 T2 与 T4 的文件不相交，已核对。残留（上轮未穷尽）：`ErrRewindBusy` 的声明归属与可追溯表行，见 S25 |
| S24 文字与小处 | **已关闭** | 删去 `rewindFileStep.Root`，改为注明 `Item` 自带 RootStatus（130-131 行）；T4 正文改为 `steps`（371 行）；接口注明预览无锁、执行由 `withSessionUnlessBusy` 持锁（119 行） |

## 发现

### S25 [LOW] `ErrRewindBusy` 的声明没有归属，T4 无法独立编译；可追溯表仍把取锁指向 T5（CORE_CORRECTIVE，S23 同类，上轮未穷尽）

- 证据：
  - `ErrRewindBusy` 和 `ErrRewindStale` 一起写在跨任务接口的 Runner 段（155 行），没有标明由哪个任务声明。按原来的分工，它们由 T5 的 `rewind.go` 声明。
  - 0.5 把 `withSessionUnlessBusy` 移到 T4（W1），并要求它把 `ErrSessionBusy` 映射为 `ErrRewindBusy`，“`ErrRewindBusy` 只由它产生”（168、170 行）。T4 的用例还要断言“回合持有返回忙”（368 行）。T4 没有依赖，在 W1 实施，这时 T5 的 `rewind.go` 还不存在。实施者只能自己声明一份，到 W3 时 T5 再声明就会重复定义。
  - 可追溯表“计划 S18、F28 取锁”一行仍写“跨任务接口、T5、T6”（575 行），与新增的 S23 行（579 行，T4）不一致。
- 影响：编译时会立即暴露，结果不会错，所以不是 blocker。但这与 S23 是同一类问题（同一符号的归属不唯一）。
- 建议：在 155 行注明 `ErrRewindBusy` 由 T4 在 `undo.go`（或 `rewind_files.go`）中声明，`ErrRewindStale` 仍由 T5 声明；575 行改为“跨任务接口、T4”。

### S26 [LOW] 队首插入的 rewind 通知：设计偏差清单没有更新；写入失败时放回队尾，会失去队首位置（CORE_CORRECTIVE）

- 证据：
  - 偏差清单仍写“T5 的 undo 通知合并（F-N4）”（67 行）。0.5 已经不合并，改成 rewind 通知插入队首、undo 通知保留并在之后的回合送达（413 行）。这既不同于设计 0.2 的流程 3 和 R8（151-152、269 行：undo 通知“丢弃”，由 rewind 通知取代），也不同于 R2-F-N4 的建议（“合并两段通知”）。偏差清单记录的做法与计划正文不一致。
  - 内部有效性：`injectUndoNotice` 写入失败时，用 `appendUndoNotice` 把这一项放回**队尾**（`undo_notice.go:115、169-178`）。计划写的是“取法不改”“不改回填逻辑”（413-414 行），没有处理放回位置。如果 rewind 通知在注入时写入失败，它会排到已有的 undo 通知之后，要再等若干轮才送达。计划所说的“rewind 通知在队首，因此总在下一轮送达（R5）”在这条失败路径上不成立。验证中也没有这个用例。
  - 连续两次 `:rewind conversation`（中间不发消息）时，后一次的通知插在前一次之前，送达顺序变成先新后旧，计划没有说明这是有意的。
- 必要性：队首插入是 S22 方案 A 选定的最小机制，必要性成立。上面的缺口都可以在 T5 内用一两行收窄解决，不需要新增机制。
- 影响：失败路径下 R5 的送达延后，不丢通知，也不涉及没有回退记录的会话。所以不是 blocker。
- 建议：
  - 67 行改为“T5 的 undo 通知保留，rewind 通知插入队首、分轮送达（设计 R8 与流程 3 原为丢弃；F-N4、计划 S22）”；
  - T5 写明：rewind 项写入失败时放回**队首**；undo 项沿用现有的放回队尾，这样没有回退记录的会话行为不变。补一个“rewind 通知写入失败，下一轮仍先送达”的用例；
  - 连续 rewind 的顺序，二选一写明：插到已有 rewind 项之后（保持先旧后新），或者接受先新后旧。

### S27 [LOW] 文字与小处（ADVISORY）

- S21① 残留：218 行“容器内 git……”仍挂在“文件拆分理由”段落下，应移回“运行目录与环境”列表中。
- “输入与批准证据”的计划评审记录只到 round 3（61-64 行），缺 round 4（`f009fd02`，两轴 PASS，CORE_CORRECTIVE 在 0.5 收口）。这是元数据纠正，不递增版本（SR1103）。
- T2 写了“Ledger 通过 Runner 注入的 `maskedRanges`”（315 行），而绑定点在 `runner.go:444`，T2 的文件清单（298 行）没有列 `runner.go`。T5 的“`runner.go`（如需透传）”（393 行）则没有写定改什么。expected paths 不是封闭白名单，T2 与 T4 在 W1 也不冲突，所以只建议在 T2 的文件清单中补上 `runner.go`（Ledger 绑定），并把 T5 的“如需”改为具体的改动点或删去。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，不新增入口）。
- 注入防护：已核对。recall 续读只改取 seq 的来源，仍走参数化的 seq 查询。
- 秘密不进日志或输出：已核对。通知插入队首只改变顺序，正文和事件 attrs 不变。
- 入参校验：已核对。`resolveTarget` 不变。
- 资源归属：已核对。Ledger 的屏蔽区间按当前会话的 sid 取得；fork 的 origin 改为 `store.OriginChat`，与设计 129 行一致（F32）。

### 已核对且无问题

- 首行模板 1.3.0，状态 `Draft 0.5`。0.5 的修订记录只描述语义变化，逐项对应 S22–S24、F32–F38 与 S17 残留。validator 通过。
- scope freeze 与 fail-closed 一致：例外删除后，fail-closed 的“没有回退记录的会话上改变任何行为即停止”（212 行）与 scope freeze 第 25 行之间不再有冲突。新增的 F33 写明“没有回退记录的会话，续读 ref 与现在逐字相同”，并有对应用例（315、331 行）。
- 偏差清单新增了 recall 续读 ref 一项（70 行）。
- 波次：W1 中 T2 与 T4 的文件不相交；W3 中 T5 与 T6 的文件不相交（i18n 分别用 `model.ini` 与 `personal.ini`，`undo_runner.go` 只由 T4、T5 先后修改，两者不并行）。
- 没有新增 Module、Interface、Schema 或授权；授权 Gate 不变。
- 可追溯表新增 S22、S23、F32、F33 四行，都指向任务和验证。

## 覆盖缺口与限制

- 只读了 diff 和受影响的章节。未变化的章节（T0、T1、T7、Capability Discovery、回滚）沿用第 4 轮的结论。
- 治理观察：按 Review Candidate Loop，第 3 轮以后只为开放的 CORE_BLOCKING 开轮。第 4 轮没有 CORE_BLOCKING，本轮属于额外的确认。S25–S27 都不是 CORE_BLOCKING，修正后不应再开第 6 轮，由主会话核对修正 diff 即可（SR1406）。

## 结论

**PASS**（规范与治理轴，Round 5）。

- 第 4 轮的 S17、S18 残留，S21②、S22、S24 已关闭；S23 基本关闭。S21① 仍有残留，并入 S27。
- 新发现：
  - S25、S26 为 CORE_CORRECTIVE，都是归属或失败路径的一两行收窄，不改变范围；
  - S27 为 ADVISORY。

## 剩余风险与后续

- 交人工批准前修正 S25、S26。S26 修正后，偏差清单与 T5 正文会写明“undo 通知保留、rewind 通知队首、分轮送达”，这项设计偏差在批准时对用户可见。
- S25、S26 修正属于语义变化，计划递增为 0.6 即可，不需要新开评审轮次。S27 中的纯元数据部分不递增版本。
- 开工时按计划重新采集 baseline。
