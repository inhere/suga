<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `3618827e`）
- 修订版本: Draft 0.2
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告
- 轮次: Round 2（confirmation）。读 `bae17865..3618827e` 的计划 diff，并因 0.2 重排了任务与接口，实际全量重读计划
- 日期: 2026-10-31
- 结论: **BLOCKED**（S11，1 项 CORE_BLOCKING）
- 排除项: 未运行测试与基准；fork seq 映射、分页补足、嵌套 cut 的逐行推演归可执行轴

## 输入与方法

- 规范：`idevstd load --delivery semantic --accept-delta --task plan --task review --agent claude --consumer-mode --thinking-mode rigorous`，BOUND、FULL、18 项资源，payload sha256 `a7be77b2…` 已核验。payload 不含 plan 合同，另从 `IDEV_STD_RELEASES_ROOT` 下的 release 只读查阅 `plan-contract.md`。
- 读取：计划 0.2 全文；设计 0.2（名词、事实、总体方案、代码组织、关键流程、决策表）；第 1 轮规范轴报告；仓库 `AGENTS.md`。
- 结构校验：`idevstd script validate_document --kind plan <plan>`，结果 `ok: true, errors: []`。
- 代码核对：对“预期符号”逐条 `sed -n`，并 grep 跨任务接口涉及的现有类型（见下表）。

| 引用 | 结果 |
|---|---|
| `context.go:111/355`、`segment_handoff.go:97/112`、`handoff.go:124`、`lastreply.go:23` | 一致 |
| `session_card.go:99/126/129/194`（194 在 `fillCardPlan` 内，函数起于 193） | 一致 |
| `undo.go:109` UndoCore、`:190` checkpointsLocked、`:347` ListTurns；`undo_runner.go:8` | 一致 |
| `tasks.go:18` TaskNotices；`tasks.Manager.List`（`pkg/agentcore/tasks/tasks.go:559`）；实现方只有 `*tasks.Manager`（`internal/wiring/runtime.go:91`、`internal/chat/chat.go:134`）和测试 fake | 一致；给接口加 `List` 不用改 wiring |
| `ledger.go:100-135` 作用域、`:124` SearchEntriesPage、`:249` readMessages | 一致（设计写 `:262`，计划已更正为 `:249`） |
| `store/entries.go:24,291,321,358`、`fts_snapshot.go:27`、`events.go:40`、`turns.go:30,61,91`、`sessions.go:45` | 一致 |
| `pkg/sugasdk/engine.go:296` 用零值以外的字段只传 `Query/Limit` | 一致 |
| `chat_undo.go:107`（SelectUndoCheckpoints）、`:170`（Undo）；`steering.go:209`；`chat_usage.go:103-115` | 一致 |
| `prefix.go:366` clearRequest、`segment_handoff.go:52` metaInjected、`undo_notice.go:22` `suga.undo.pending`、`ledger_test.go:59` spill 夹具、`../xhen/internal/link/anchor.go` | 均存在 |
| `BenchmarkSessionCard`（`internal/personal/session_card_test.go`）、`make smoke-chat`/`smoke-go`（Makefile:135/178）、`fakellm_test.go` 的 `ptye2e` build tag | 均存在，验证命令可执行 |

## 验收标准

plan 合同（思考声明、修订记录、Capability Discovery 三表、`host_or_non_offline_action`、前置检查与 fail-closed、波次依赖、任务五要素与运行目录、跨任务签名、workspace baseline、人工 Gate、exact 可追溯、完成 Gate）；评审合同（同类穷尽、五类 CORE_BLOCKING 与 necessity proof）；SR1301；GR104、GR107–GR110、SR2002–SR2004；AGENTS.md 的验证要求。

## 第 1 轮发现关闭状态

| ID | 状态 | 依据 |
|---|---|---|
| S1 依赖与波次 | 已关闭 | T2←T1、T3←T2、T5←T3+T4、T6←T1+T2+T3、T8←T0+T7（计划 166-170 行）；前置拒绝只有一个 owner（T3 `rewindPreconditions`，279 行），T5、T6 复用；失败处理表移到依赖 T4 的 T5（340 行）。各波次的并行任务文件不相交（W0 T0/T1，W1 T2/T4，W3 T5/T6） |
| S2 跨任务签名 | 部分关闭 | 新增“跨任务接口”一节（91-124 行），给出了 store、Runner、预览/执行两段式和 `ErrRewindStale`。剩余缺口见 S12 |
| S3 sugasdk 默认行为 | 已关闭 | `ExcludeSession` 零值不排除，sugasdk 与 CLI 用零值（104、204-207 行）；T1 加零值一致用例；`engine.go:296` 列入预期符号 |
| S4 可追溯 | 已关闭 | 表中有 R9、R10 两行（468-469 行）；recall 有动作与用例（244、259 行）；`session_card.go:194` 已点名（239 行） |
| S5 文件清单 | 基本关闭 | T5 已列 `model.ini`，预期符号已加 `ListEvents`。领域层文件拆分（design 的 `rewind.go` 拆成 `rewind_target.go`、`rewind_notice.go`、`store/rewind_store.go`，并新增 `fork_files.go`）仍没写理由（上轮未穷尽），并入 S16 作 ADVISORY |
| S6 Capability 覆盖 | 已关闭 | 已补 CAP-05–09。NM-01 的分类问题见 S15（ADVISORY） |
| S7 反向验证 | 已关闭 | T3 的跨会话归属（288 行）、T5 的 undo pending 合并（351 行）、T8 每个场景一次（434-439 行） |
| S8 运行目录 | 已关闭 | 152-154 行写了 worktree 根目录与 gofer `--cwd` 形态；完成 Gate 第 2 条 `--cwd inhere-tools/suga` |
| S9 外部动作 Gate | 已关闭 | 49 行与 454 行都是“发版 v0.54.0、push、主机安装” |
| S10 文字与基线 | 基本关闭 | 只涉及单仓库；baseline 写了 Git root 与 branch；评审纠正项造成的偏差已在 58-61 行注明。T2 仍用整包 `go test ./internal/personal/`，属 ADVISORY，不再跟踪 |

## 发现

### S11 [HIGH] ApplyRewind 的会话部分不按 scope 区分，files 模式会把对话一起回退（CORE_BLOCKING · [BUG]）

- 证据（同类全部出现点）：
  - 设计：回退记录只在“会话部分（scope 包含 conversation）”追加（design 100-103 行）；fold、`loaded_tools`、前缀缓存的重置写在“rewind 会话部分”（design §3）；流程 3 写明“只回文件：沿用现有 undo 文案”。
  - 计划 T5 `ApplyRewind`（327-338 行）：第 2 步“按 scope 调用 applyRewindFiles”，第 3 步“追加 `rewind` entry”，第 4 步 meta 重置与 `clearRequest`，第 5 步 `TakeNotices` 丢弃、合并 undo pending、注入会话通知。第 3–5 步都没有 scope 条件。
  - 计划 T2 `maskedRanges`（233 行）：“由 `RewindRecords` 构造区间并集”，不按 `RewindRecord.Scope`（97 行）过滤。
  - `RewindPlan.DropTasks`（112 行）与 T5 预览（325 行）也不分 scope。
- 影响：照计划字面实施，`:rewind N files` 会写一条 rewind 记录，而 T2 会把它算进屏蔽区间，于是对话也回到了第 N 轮，同时丢弃任务通知、重置 fold，并注入“会话已回退”的通知。这违反完成定义第 2 条和设计流程 3。T5 的用例“三种 scope 的结果”可能按同一误读编写，测不出这个问题。另一种实施方式（只在 conversation/both 时写记录）也说得通，所以实施不唯一。
- necessity proof：
  - core_acceptance：完成定义第 2 条（files 只恢复快照），以及设计流程 3 对“只回文件”的通知要求。
  - necessary_causal_chain：T5 第 3 步无条件写记录 → T2 用全部记录构造区间 → files scope 屏蔽了对话 → 第 2 条验收的结果错误。
  - minimal_owner：T5（第 3–5 步与预览加 scope 条件）；也可以改为 T2 按 Scope 过滤，两种方式只能选一种写死。
  - deletion_alternative：PASS（只需加一句条件，不新增机制）。
  - scope_effect：不新增 Module、Interface、Schema、state 或 lifecycle。
- 建议：
  - 在 T5 写明：“第 3–5 步与 DropTasks 只在 scope 为 conversation 或 both 时执行；files 只执行第 2 步，然后按现有 undo 文案入队通知（`enqueueUndoNotice`）”。
  - 在 T2 写明区间只来自 conversation/both 的记录（如果第 3 步仍写记录用于审计，这一条就是必需的）。
  - T5 增加反向验证：“files 模式下写入 rewind 记录，conversation 未变用例失败”。

### S12 [MEDIUM] 跨任务接口仍有未定义的形状与归属（CORE_CORRECTIVE，S2 残留）

- 证据：
  - `RewindResult`（117 行）与 `RewindFileItem`（111 行）没有字段定义。T7 要打印结果，T4 的 `planRewindFiles`/`applyRewindFiles`（297-298 行）没写返回类型，也没写这些类型由 T4 还是 T5 定义。
  - `Hidden(seq)`（233 行）由 T2 提供，但 `maskedRanges` 返回 `store.SeqRanges`。Go 不能在 personal 包里给 store 的类型定义方法，所以 `Hidden` 要么是 T1 的 `SeqRanges` 方法，要么是 T2 的独立函数，计划没写定。
  - 接口里同时有 `UndoCore.RewindPlan` 和 `Runner.RewindPlan`（115-116 行），但 UndoCore 没有 Tasks 和回合状态（`undo.go:109-116` 只有 Repository、Snapshots、Gate、HeldLock）。所以 DropTasks、前置拒绝和目标解析（T3 函数需要 Runner 的屏蔽集合）放在哪一层，计划没有说明。
  - `ForkInput` 的字段只出现在 T1 正文（208 行），接口一节里没有。
- 建议：
  - 在接口一节补齐 `RewindResult`、`RewindFileItem`、`ForkInput`，以及 T4 两个方法的签名；
  - 写定 `Hidden` 归 T1 的 `SeqRanges`；
  - 写明 UndoCore 只负责文件部分，Runner 负责组装目标、任务与 Stamp。如果 UndoCore.RewindPlan 没有独立调用方，就删掉它。

### S13 [MEDIUM] 用 `Manager.List` 预览“将丢弃的待注入通知”，取到的集合不对（CORE_CORRECTIVE）

- 证据：
  - CAP-05 与 T5 的预览（73、325 行）用 `List` 收集将丢弃的通知。但 `Manager.List` 返回会话的全部任务（`pkg/agentcore/tasks/tasks.go:559-574`），而待注入通知在 `s.notices` 中，只有 `TakeNotices` 能读出（`:686-690`），而且它会同时清空队列。已经注入过的已结束任务，也会被 `List` 当成“将丢弃”列出。
  - 待注入通知还包括带 text 的进度通知（`:699` `taken[i].text`），`List` 表达不了这类通知。
  - 在 `pkg/agentcore/tasks` 增加只读 peek 超出 scope freeze（20 行没有列 `pkg/agentcore/**`）。
- 影响：预览列出的被丢弃项与实际结果不一致（设计 R8、流程 1 第 2 步要求列出）。
- 建议：二选一写定：
  - 预览只列“已结束的任务（可能有待注入通知）”，准确清单以 `ApplyRewind` 的 `TakeNotices` 结果为准，在结果里列出（T5 的“被丢弃并列出”用例已覆盖）；
  - 或者把 `pkg/agentcore/tasks` 的只读 peek 作为显式扩围提交 Human Gate。

  CAP-05 的 proven_gap 按选定方案改写。

### S14 [LOW] R11 的收窄与设计一致，但 deferred 项写错了依据（ADVISORY）

- 核对：设计 R11（design 273 行）与 §4（156 行）是“当前会话的 session_search/recall 排除被屏蔽条目”，目的是不让模型检索回自己回退掉的内容。计划写的是“任何作用域都排除当前会话的屏蔽条目，其他会话不排除”（32、243-244 行），覆盖了设计的字面要求。workspace 与 all 作用域也排除当前会话，这比只管 current 作用域更严，与设计目的一致。recall 只在 ref 属于当前会话时跳过，与此一致。**此项一致。**
- 问题：45 行把“其他会话屏蔽区间的排除”列在“排除（设计 R9）”下，494 行写“设计 R9、R11”。但设计 R9（271 行）和非目标（53 行）都没有这一项，它是计划依 R11 字面做的 deferred。
- 建议：改成“依 R11 字面只覆盖当前会话；其他会话列为计划级 deferred”，不归到 R9。

### S15 [LOW] NM-01 不是新 Module，删除处置写成了将来复用（ADVISORY）

- 证据：CAP-08 与 NM-01（76、83 行）把 `internal/personal/fork_files.go` 记为 `MINIMAL_NEW_MODULE`。它是现有包里、只服务 fork 的一个文件，实质是 fork owner 的扩展，拆出来只为满足 GR104 的行数上限。NM-01 的 `deletion_or_merge_handling` 写的是“将来 CLI fork 复用同一函数”，这是对未来复用的推测（SR1405 target-locked），没有写删除或合并时怎么处理。替代方案的说法“放入 ledger 会混进检索 owner”也没对准真正的备选（放进 `fork.go`）。
- 建议：CAP-08 改为 `OWNER_EXTENSION`（owner 为 fork），New module candidates 写 `NONE` sentinel；如果保留 NM-01，删除处置改为“随 fork 一并删除”。

### S16 [LOW] baseline 与若干文字细节（ADVISORY）

- baseline（128 行）写的是 HEAD `a59b27d0`。当前 main 是 `66a3cbc8`，其中包含 `3618827e` 之后合入的 B4（`pkg/chatui` 文档与示例，不在本计划范围内）。实施开工时要按 plan closure 重新采集 baseline。
- 128 行说本计划在 `render.go`、`chat.go` 中“新增命令分支”，但命令分发实际在 `chat_commands.go:381`，T7 的文件清单也不含 `chat.go`/`render.go`。应删掉这句或改成 `chat_commands.go`。
- S5 残留：领域层文件按职责拆分，偏离了设计“代码组织”，需补一句理由（同包拆分，GR104 / 单一职责）。
- T6 的 title 后缀“（分叉）”没写是否走 i18n；如果走 i18n，T6 要在文件清单里加 catalog 文件，并说明与同波次的 T5 改动的 `model.ini` 不冲突。
- fail-closed（156 行）写“改 sugasdk 行为即停止”，而 T8 的 CHANGELOG 承认 `sugasdk.Entries` 会返回新的 `rewind` 类型。建议在 fail-closed 中注明这是设计内的预期，避免实施者误停。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，没有新增入口）。
- 注入防护：已核对。T1 用 `NOT (seq > ? AND seq < ?)` 绑定参数（200 行）；`ExcludeSession` 进入 fingerprint。
- 秘密不进日志或输出：已核对。事件 attrs 只含目标轮、scope 和文件统计。
- 入参校验：已核对。T3 `resolveTarget` 校验序号或 turn_id。
- 资源归属：已核对。T3 校验 turn_id 属于本会话，有反向验证（288 行）；recall 对 ref 落在区间内的条目按“不存在”处理（244 行）。

### 已核对且无问题

- 首行模板 1.3.0、状态 `Draft 0.2`；修订记录只因语义变化递增；RIGOROUS 声明、scope freeze、暴露度（非低暴露，两轴）、预算与停止条件齐全；validator 通过。
- `host_or_non_offline_action=REQUIRED` 恰好一处（50 行）。
- 授权 Gate 一致：计划批准只授权 worktree 实施与本地提交；合入由主会话执行；外部动作（发版、push、安装）单独 Gate；冒烟以 `&&` 串接作为合入前提。
- 可追溯表覆盖 R1–R12 全部要求，以及设计评审与计划第 1 轮的各项。
- 结构条目：不新增入口或 CLI，GR107、GR110、SR2003 不适用；wiring 不改（GR108）。`TaskNotices` 加 `List` 只影响 `*tasks.Manager` 与测试 fake，不越过“不改 wiring”。GR109 在 T5 中点名；GR104 由每个新文件 400 行的上限约束；SR2002 由 AGENTS.md 约束。

## 覆盖缺口与限制

- 未运行测试与基准。
- 未推演 fork 的 seq 与 `first_kept_seq` 映射、分页补足、嵌套 rewind 的正确性（归可执行轴）。
- plan 合同取自 releases root 下的 release 副本，不在本次 semantic payload 内。
- 未核实“B1–B4 与 S50A1 已合入”的说法，只确认 B4 在 `3618827e` 之后合入。

## 结论

**BLOCKED**（规范与治理轴，Round 2）。

- 开放 CORE_BLOCKING：S11 [BUG]，ApplyRewind 与屏蔽区间不按 scope 区分。
- S12、S13 为 CORE_CORRECTIVE，须在计划批准前修正。
- S14–S16 为 ADVISORY。
- 第 1 轮 S1、S3、S4、S6–S9 已关闭；S2 的残留并入 S12；S5、S10 基本关闭。

## 剩余风险与后续

- S11 修正只需加一条 scope 条件，再加一个反向验证；第 3 轮只读修订 diff 与 T2、T5 两节。
- S13 如果选择扩围（tasks peek），须先过 Human Gate。
- 开工前重新采集 baseline（当前 main 为 `66a3cbc8`）。
