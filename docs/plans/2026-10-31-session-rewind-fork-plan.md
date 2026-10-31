<!-- template_id: plan; template_version: 1.3.0 -->
# suga 会话 rewind 与 fork 实施计划

> 状态：Draft 0.1 / 待人工计划批准

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-31 | Claude | 初稿：按已批准设计 0.2 拆为 W0 基础（测试设施、store）、W1 投影与选轮、W2 rewind/文件/fork、W3 命令与验收；收口两轴第 2 轮遗留的 CORE_CORRECTIVE（S-N1、S-N2、F11、F-N1–N5） |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 计划工作声明

- 思考模式：`RIGOROUS`（SR1408）。
- 核心目标：实现设计 `docs/design/2026-10-31-session-rewind-fork-design.md` 0.2，即 `:rewind N [files|conversation|both]` 与 `:fork N`，并满足 suga-4tnd 的验收：三种 scope、fork 后原会话不变、有 e2e 与单测。
- Scope freeze：只改 suga 仓库的 `internal/personal/**`、`internal/personal/store/**`、`internal/chat/**`、`internal/i18n/catalog/**`、`test/ptye2e/**`，以及 CHANGELOG、README 中的 chat 说明。不改 CLI 子命令、ACP、sugasdk 公开 API、wiring、DDL。
- 暴露度：会话数据是用户资产，不属低暴露（SR1409）。计划评审采用两轴独立评审，严谨模式最多 8 轮；无开放 `CORE_BLOCKING` 即停止。
- 停止条件：两轴都 PASS 后交用户批准；实施中出现 Semantic Amendment 或 Ownership Conflict 即停。

## 目标与完成定义

1. `:rewind N conversation`：下一轮发给模型的请求里，不再包含第 N 轮及之后被屏蔽的任何内容，回退早于 compaction 或 clear 时也一样；交接包与 handoff 中不出现被回退的提问或 plan。
2. `:rewind N files`：从最新一轮逆序到第 N 轮，恢复每一轮的快照。快照已被清理的轮列为不可恢复；冲突（含级联冲突）跳过并列出。
3. `:rewind N`（both）：同时满足 1 和 2，并按设计流程 3 注入通知。
4. `:fork N`：生成新会话，可以续聊，其投影历史等于源会话第 N 轮之前的可见历史；源会话的 entries、turns、meta 不变。
5. 序号只追加的承诺不变。旧条目保留，trace 照常显示。当前会话以及其他会话的模型侧检索（`session_search`/recall）都排除被屏蔽的条目（R11，见 T3）。
6. 两轴第 2 轮遗留的纠正项全部落到下列任务：S-N1 → T3；S-N2 → T3；F11 → T0、T9；F-N1 → T1、T7；F-N2 → T6；F-N3 → T3；F-N4 → T5；F-N5 → T1、T3。
7. 完成 Gate 全部通过。

## 范围、排除项与授权

- 范围：见计划工作声明的 scope freeze。
- 排除：
  - summarize-from-here、redo；
  - CLI、ACP、sugasdk API；
  - trace 与 transcript 中的“已回退”标注；
  - 把第 N 轮提问回填到输入行；
  - 延长快照保留期。
  以上均依据设计 R9。
- 授权：
  - 计划批准只授权在 `.worktrees/` 下按任务实施并本地提交（SR1211）；
  - 合入 main 由主会话审核后执行；
  - 发版 v0.54.0、push、安装都走外部动作 Gate。
- `host_or_non_offline_action=REQUIRED`（Windows 主机经 gofer exec job 跑 `go test` 与 Go ptye2e；只用工作区 `tmp/` 下的临时 SUGA_HOME）

## 输入与批准证据

- 设计：`docs/design/2026-10-31-session-rewind-fork-design.md` 0.2，用户 2026-10-31 批准，原话：“批准 rewind 设计 0.2（含 R11、R12）”。
- 评审：`docs/review/2026-10-31-session-rewind-fork/`，包括 `standards-review.md`、`spec-review.md`、`review-ledger.json`（rounds 1–2，PASS，`cff054ce`）。
- issue：suga-4tnd。计划 todo 见 gofer plan `plan-20261031-211227-3fa910d5`（suga 0.54）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 文件按轮恢复 | `undo_restore.go` restoreItem/contentConflict、`undo_options.go` UndoWithOptions | 部分：UndoWithOptions 自取 gate，不可重入 | 新增不加锁的选择与执行函数，复用 restoreItem | OWNER_EXTENSION | gate 不可重入（`gate.go:115-140`） | 与 undo 共用 restoreItem，无第二份恢复逻辑 |
| CAP-02 | 屏蔽区间读取 | `store/entries.go` LatestEntry、RecentEntries、CountEntriesByType、SearchEntriesPage | 否：没有“从某 seq 往前”的游标，FTS 不能排除区间 | 在既有 store 中新增游标与区间排除方法 | OWNER_EXTENSION | F-N5 | 方法归 store 所有，不复制 |
| CAP-03 | fork 原子写入 | CreateSession、CreateTurn、AppendEntries（各自独立）、私有 beginWriteTx | 否：没有跨调用事务 | 在 store 中新增 `CreateForkedSession`，一个事务内写 session、turns、entries | OWNER_EXTENSION | F-N1 | 由 store 统一负责 |
| CAP-04 | 后台任务通知丢弃 | `pkg/agentcore/tasks/tasks.go` TakeNotices | 是 | 直接调用 | DIRECT_REUSE | none | 无 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 新增独立的 rewind store 包 | CAP-02 | 现有 `internal/personal/store` 已是会话数据的唯一 owner，删除该候选后验收不受影响 |

## 前置检查与 fail-closed 条件

- **Workspace baseline**：suga main HEAD 为 `d3782cdc`（之后有一个只改 tracker 的提交），工作区干净。设计 0.2 已批准。B1–B3 与 S50A1 已合入，与本计划的文件交集在 `internal/chat/render.go`、`chat.go`，本计划只在其中新增命令分支。
- **预期符号**：
  - `context.go:111` loadHistory；
  - `segment_handoff.go:97` latestPlan、`:112` recentRequests；
  - `handoff.go:124`；
  - `lastreply.go:23` LastReply；
  - `session_card.go:99` SessionCard；
  - `ledger.go:100-135,262` 检索作用域；
  - `store/entries.go:24,291,321,358`；
  - `store/fts_snapshot.go:27` SearchEntriesPage；
  - `store/turns.go:30,61,91`；
  - `store/sessions.go:45`；
  - `undo_restore.go`、`undo.go:207-227,347,363-366`、`gate.go:115-140`；
  - `snapshot_retention.go:620-628`；
  - `observe.go:311-433`（工具事件的 `attrs["target"]` 与 Status）；
  - `runner_sessions.go:181` writeStartSegment；
  - `chat/steering.go:205-212`；
  - `chat/chat_usage.go:103-115`。
- **环境**：容器 Go 1.26，`GOFLAGS=-buildvcs=false`；Windows 主机经 gofer exec job 运行。
- **fail-closed**：
  - 需要改 DDL、sugasdk、CLI、ACP，或改变设计以外的用户可见行为：属 Semantic Amendment，停止。
  - 任何改动导致 `BenchmarkSessionCard` 回退超过 20%（按 `-benchtime=2s` 对比基线）：停止。
  - 两仓库都只在 `.worktrees/` 下实施；容器内 git 只对单条命令前缀 `GIT_DIR/GIT_WORK_TREE`；合并在主机执行，并以 `is_ancestor=0` 为收尾前提。

## 波次与依赖

| 波次 | 任务 | 执行方式 |
|---|---|---|
| W0 | T0 测试设施；T1 store 方法 | 两项并行，文件不相交 |
| W1 | T2 用户轮索引与 cut；T3 屏蔽区间集合与读者接入 | T3 依赖 T1；T2 与 T3 可并行 |
| W2 | T4 回退记录与会话部分；T6 文件回退；T7 fork | 都依赖 T2、T3；T4 与 T6 文件不相交；T7 依赖 T1 |
| W2 | T5 meta 与通知 | 依赖 T4 |
| W3 | T8 chat 命令；T9 e2e、CHANGELOG 与收尾 | T8 依赖 T4–T7；T9 依赖 T8 |

- 每个任务一个 worktree 分支，由 sonnet 子 agent 实施。
- 主会话复核（SR1433）：逐条核对、反向验证、grep 调用点、`-count=2`、注释未删、`git diff --stat` 无无关删除。
- 涉及 chat 的任务，由主会话自己跑 `make smoke-chat`，并**以冒烟通过为合入前提**。合入命令用 `&&` 串接：冒烟或测试失败时合入不执行。

## 任务

### T0 测试设施：可观察请求与按轮写入

- 文件：`test/ptye2e/fakellm_test.go`、`internal/personal` 中供单测使用的测试辅助（`*_test.go`）
- 动作：
  - fakeLLM 记录每次请求的完整消息列表，包括 role、内容，以及 compaction/handoff 包内的原文。
  - 提供“按轮脚本”：第 k 次请求对指定文件写入内容 `vk`，用于验证文件回到第 N 轮。
  - 单测侧提供构造带多轮、compaction、clear、steer、undo 通知、interrupted 轮的会话夹具。
- 验证：`go vet -tags ptye2e ./test/ptye2e/`、`go test -tags ptye2e -count=1 ./test/ptye2e/`（现有用例不受影响）、`go test -count=1 ./internal/personal/`。
- 完成标准：新增能力有自测；现有冒烟与单测通过。
- 依赖：无

### T1 store：rewind 类型、区间读取、游标、FTS 排除、fork 原子写入

- 文件：`internal/personal/store/types.go`、`entries.go`、`fts_snapshot.go`、`turns.go`、`sessions.go`（或新文件 `fork_store.go`）及测试
- 动作：
  - `EntryRewind = "rewind"`。
  - `RewindRecords(ctx, sid) ([]RewindRecord, error)`：只查 `type='rewind'`，走 `idx_entries_type` 索引。
  - 游标方法 `RecentEntriesBefore(ctx, sid, types, beforeSeq, limit)` 与 `LatestEntryBefore(ctx, sid, typ, beforeSeq)`，用于跳过被屏蔽条目后补足数量（F-N5）。
  - `SearchEntriesPage` 支持传入屏蔽区间，在 SQL 中排除 `seq` 落在区间内的行，分页与 cursor 保持正确（F-N5）。
  - `CreateForkedSession(ctx, ForkInput{Session, Turns, Entries})`：在一个写事务内创建 session、turns、entries，seq 由事务分配并返回“源 seq → 新 seq”映射；失败时整体回滚（F-N1）。
- 验证：
  - `go test -race -count=1 ./internal/personal/store/`；
  - 新用例覆盖：游标边界、区间排除后的分页（多页、cursor 连续）、fork 写入中途失败后无残留；
  - `go test -run '^$' -bench BenchmarkSessionCard -benchtime=2s ./internal/personal/` 记录基线。
- 完成标准：用例通过；分页正确性有反向验证（去掉区间排除，用例失败）。
- 依赖：无

### T2 用户轮索引、cut 与校验

- 文件：`internal/personal/rewind.go`（新）及测试
- 动作：
  - `userTurns(ctx, sid) ([]userTurn, error)`：按 `ListTurns` 顺序，排除请求 user entry 带 `suga.injected` 元数据的轮、没有 entry 的轮、已被屏蔽的轮。
  - `Hops>0` 的轮与 `suga send` 发起的轮：实施前核实它们是否带 Injected 标记，结果写入 progress。没有标记的按用户轮处理。
  - `resolveTarget(sid, arg)`：参数可以是序号或 `turn_id`；`turn_id` 须属于该会话、不在屏蔽区间内、且是用户轮（SR1301）。
  - `cutFor(turn)`：取该轮首条 entry 的 seq 减 1。
- 验证：`go test -race -count=1 -run 'UserTurn|Cut|ResolveTarget' ./internal/personal/`，覆盖 injected、nudge、空轮、steer、跨会话 `turn_id`、已屏蔽轮。
- 完成标准：用例通过。
- 依赖：无（使用 T1 的 RewindRecords 时以 T1 合入为准；T1 未合入前先用接口桩）

### T3 屏蔽区间集合与读者接入

- 文件：`internal/personal/visible.go`（新）、`context.go`、`segment_history.go`、`segment_handoff.go`、`handoff.go`、`lastreply.go`、`session_card.go`、`ledger.go` 及测试
- 动作：
  - `masked` 集合：由 `RewindRecords` 构造，提供 `Hidden(seq)`；语义为所有回退记录区间的并集。
  - 接入设计 0.2 中“必须经过屏蔽的读者”表里的全部读者：
    - `loadHistory`：Types 白名单加入 `EntryRewind`，扫描时把它当作区间记录；
    - `SegmentHistory`、`latestPlan`、`recentRequests`、`buildHandoff` 中的 plan 读取与 recentRequests、`LastReply`；
    - session card 用 T1 的游标补足；
    - 检索：当前会话以及 workspace、all 作用域下的其他会话，统一排除各自的屏蔽区间（S-N2，R11 的统一规则：模型侧检索在任何作用域下都不返回被屏蔽条目）。
  - session card 的 `Turns` 改为减去被屏蔽的轮，凭区间内条目的 turn_id 判定（F-N3）。
  - 在代码注释与 progress 中写明“不受影响的读者”及理由（S-N1）：
    - 按单轮读取：`compact.go:176`、`replay.go:233`、`admit.go:165`、`steer.go:418`；
    - `:checkpoints`/`:undo`（`undo.go:347` ListTurns）按磁盘事实计数，被屏蔽轮的文件改动仍可撤销；
    - `segment_clear.go:100` 的 LastEntrySeq 不读内容。
- 验证：
  - `go test -race -count=1 ./internal/personal/`，新用例：
    - 回退早于 compaction 与早于 clear；
    - 嵌套 rewind；
    - rewind 后再 compaction，交接包中不含被回退的提问与 plan；
    - interrupted 轮、context_update；
    - LastReply；
    - session card 的 Turns；
    - 检索排除，覆盖当前会话、workspace、all 三种作用域。
  - 反向验证：去掉 `recentRequests` 的屏蔽，交接包用例失败；去掉 loadHistory 的屏蔽，上下文用例失败。
  - `BenchmarkSessionCard` 与 T1 记录的基线对比，回退不超过 20%。
- 完成标准：用例、反向验证与基准测试都通过。
- 依赖：T1

### T4 回退记录与会话部分

- 文件：`internal/personal/rewind.go`、`rewind_records.go`（新）及测试
- 动作：
  - `Runner.Rewind(ctx, sid, target, scope)`：持有 gate 与跨进程文件锁。
  - 前置拒绝：回合进行中、有运行中的后台任务（`tasks.Manager.Running`）、会话已归档。
  - 追加 `rewind` entry `{cut_seq, target_turn, scope, files}`。
  - 写 `session` 事件，name 为 `rewind`，attrs 含目标轮、scope、文件统计。
  - 按设计流程 1 第 4 步的失败处理表，实现各分支的报告与 Warn 日志（GR109）。
- 验证：`go test -race -count=1 -run Rewind ./internal/personal/`，覆盖三种前置拒绝、四种失败分支（注入错误）、事件属性。
- 完成标准：用例通过。
- 依赖：T2、T3

### T5 meta、进程内状态与通知

- 文件：`internal/personal/rewind.go`、`undo_notice.go`（只扩展）及测试
- 动作：
  - 重置 `fold_boundary=0`、`fold_active=false`；`loaded_tools` 保留不动（R12）；调用 `r.rec.prefix.clearRequest`。
  - 已完成但未注入的后台任务通知用 `TakeNotices` 取出后丢弃，并列入回退计划。
  - 待注入的 `suga.undo.pending` 不直接丢弃，与本次 rewind 的通知合并成一段注入，保留“哪些文件已被撤销”的事实（F-N4）。
  - 设计流程 3 的三种通知文案，zh 与 en 都要加进 i18n。
- 验证：`go test -race -count=1 -run 'Rewind.*(Meta|Notice)' ./internal/personal/`，覆盖 fold 两个字段、前缀缓存被清、任务通知被丢弃并列出、undo pending 被合并。
- 完成标准：用例通过。
- 依赖：T4

### T6 文件回退与清理检测

- 文件：`internal/personal/rewind_files.go`（新）及测试
- 动作：
  - 不加锁的 `rewindFiles(sid, fromNewest, toTurn)`：
    - 按时间逆序处理，被屏蔽的轮也要处理；跳过已 undone 的轮，partial 的轮重跑；
    - 直接循环调用 `restoreItem`；每轮调用 `SetCheckpointState`，结束后调用 `OnFilesRestored`；
    - 输出恢复、删除、跳过（冲突，含级联冲突）与不可恢复的清单。
  - 清理检测：目标区间内的每一轮，如果有 `Kind=tool`、`Status=ok`、`Name ∈ {file_write, file_edit, file_remove}` 的事件，但没有快照目录，就判定为快照已清理，取 `attrs["target"]` 原样列出，并注明它可能是相对 cwd 的路径或目录（F-N2）。
  - 快照功能整体关闭时，所有写调用都列为不可恢复。
- 验证：
  - `go test -race -count=1 -run 'RewindFiles|Cleared' ./internal/personal/`，覆盖：
    - 多轮逆序恢复到第 N 轮；
    - 冲突与级联冲突；
    - undone 与 partial；
    - 清理检测（只统计 ok 事件，error 事件不计入）；
    - 快照关闭。
  - 反向验证：把清理检测改为计入 error 事件，用例失败。
- 完成标准：用例与反向验证通过。
- 依赖：T2、T3

### T7 fork

- 文件：`internal/personal/fork.go`（新）及测试
- 动作：
  - `Runner.Fork(ctx, sid, target)`：
    - 收集源会话 cut 之前可见的轮与条目，`HydrateEntries` 取回正文；
    - 为每轮生成新 turn_id，组装 `ForkInput` 后调用 T1 的 `CreateForkedSession`，在一个事务内写入；
    - 按返回的 seq 映射，重写 compaction 的 `first_kept_seq`，取“不小于它的第一条已复制条目”；
    - 设置 `last_turn_id`。
  - cwd、`:rw`、title（加“（分叉）”）、origin、`forked_from`、`forked_at_turn` 写进 meta。
  - 不调用 `insertSession`/`writeStartSegment`。
  - 复制图片与 spill 文件，并改写正文中的 `out:<源会话>:` 前缀；compaction 的 Refs 保留不改。
  - 继承 taint：`tainted_at` 不晚于 cut 条目的 `created_at` 时，复制 `tainted_at` 与 `tainted_by`。
  - 附件复制失败时执行 `DeleteSession` 加 `DeleteSessionFiles`。
  - 写 `session` 事件，name 为 `fork`。
  - 前置拒绝条件同 rewind。
- 验证：
  - `go test -race -count=1 -run Fork ./internal/personal/`，覆盖：
    - fork 后 loadHistory 的条目数等于源会话 cut 之前的可见条目数；
    - interrupted 轮的状态被保留；
    - 图片与 spill 可读；
    - 源会话 entries、turns、meta 前后的哈希不变；
    - taint 的继承与不继承；
    - 附件复制失败后无残留。
  - 反向验证：不写入 turns，loadHistory 用例失败。
- 完成标准：用例与反向验证通过。
- 依赖：T1、T2、T3

### T8 chat 命令

- 文件：`internal/chat/chat_rewind.go`（新）、`chat_usage.go`、`chat_commands.go`、`steering.go`、`internal/i18n/catalog/{zh,en}/*.ini`
- 动作：
  - 注册 `:rewind`（无参数时列出用户轮）与 `:fork N`。
  - 加入 `steering.go` 的回合中放回判断，与 `:undo` 一致。
  - 确认交互复用 `chat.undo_confirm`；无 TTY 时取消。
  - fork 完成后打印新会话 ID 与 `suga chat --resume <id>`，当前 chat 不切换（R7）。
  - 所有文案 zh 与 en 各一份。
- 验证：`go test -race -count=1 ./internal/chat/`；主会话跑 `make smoke-chat`。
- 完成标准：单测通过；冒烟通过。
- 依赖：T4、T5、T6、T7

### T9 e2e、文档与收尾

- 文件：`test/ptye2e/chat_rewind_test.go`（新）、CHANGELOG、README 的 chat 命令说明
- 动作：
  - Go ptye2e 用例：
    - **三种 scope**：用 T0 的按轮写入跑 3 轮，`:rewind 2` 分别执行 files、conversation、both，断言磁盘内容与下一轮请求的完整消息；
    - **回退早于 `:compact`**：断言交接包与请求里都不含被回退的提问；
    - **快照已清理**：缩短保留期（只在测试 home 中设置），断言列出不可恢复项；
    - **fork**：fork 出新会话后 `--resume` 续聊，新会话请求等于源会话第 N 轮之前的可见历史，源会话不变；
    - **回合中输入**：回合进行中输入 `:rewind` 被放回。
  - CHANGELOG 写明：
    - `sugasdk.Entries` 会返回新类型 `rewind`；
    - 回滚到旧版本的语义：两条分支会拼接成一条上下文，旧版本下的 compaction 会把屏蔽区间永久写进摘要，建议回滚前先 fork。
  - README 补充命令说明。
- 验证：
  - `go test -tags ptye2e -count=1 -run TestChatRewind ./test/ptye2e/`（Linux 与 Windows）；
  - 反向验证：去掉 loadHistory 的屏蔽，conversation scope 用例失败。
- 完成标准：用例通过，Windows 也跑过（看日志确认），文档已更新。
- 依赖：T8

## 回滚与恢复

- 每个任务独立分支、独立合并提交，可用 `git revert -m 1 <merge>` 回退。
- 只追加 `rewind` entry，不改 DDL。回滚代码后，旧数据中的 `rewind` 记录会被旧二进制忽略，语义见 T9 的 CHANGELOG 说明。
- 恢复点：每次合入后的 main HEAD 记录到对应的 gofer todo；中断时按 SR1407 写 handoff。
- 测试只用工作区 `tmp/` 下的临时 SUGA_HOME。

## 人工 Gate

1. 本计划批准（用户原话记入状态行）。
2. 当前执行请求（可与批准同句给出）。
3. 外部动作 Gate：发版 v0.54.0、push。全部完成后单独请求，逐项列出目标。

## 可追溯性

| 设计 0.2 要求 / 验收 | 任务 | 验证 |
|---|---|---|
| R1、R2 屏蔽区间与读者 | T1、T3 | 回退早于 compaction 或 clear、嵌套、rewind 后 compaction 的交接包用例；反向验证 |
| R3 选轮与校验 | T2 | 用户轮、turn_id 校验用例 |
| R4 部分恢复与清理检测 | T6 | 清理检测（只计 ok 事件）、快照关闭用例 |
| R5 通知 | T5 | 通知与合并用例 |
| R6 fork | T1、T7 | turns 映射、seq 映射、附件、原会话哈希不变、失败无残留 |
| R7 不切换会话 | T8 | 命令输出 |
| R8 前置拒绝与通知丢弃 | T4、T5 | 拒绝与 TakeNotices 用例 |
| R11 检索排除 | T1、T3 | 三种作用域的检索用例 |
| R12 loaded_tools 保留，fold 重置 | T5 | meta 用例 |
| 验收：三种 scope、fork、e2e | T9 | Go ptye2e（Linux 与 Windows） |
| 两轴遗留项 S-N1、S-N2、F11、F-N1–N5 | 见目标与完成定义第 6 条 | 对应任务的用例 |

## 完成 Gate 与剩余工作

1. suga main：`go vet ./...`、`go test -race -count=1 ./...`、`go test -race -count=2 ./internal/personal/... ./internal/chat/`、`make smoke-chat`、`make smoke-go`。
2. Windows 主机：`go test -count=1 ./...` 与 `go test -tags ptye2e -count=1 ./test/ptye2e/`。
3. `BenchmarkSessionCard` 与基线相比回退不超过 20%。
4. 真实环境（SR1402.1）：用真实模型跑一次 `:rewind 2 conversation`，检查 trace 中下一轮请求不含被回退的内容。做不到时记为 `UNVERIFIED_REAL_ENV: 真实 provider 的 rewind 后请求（复现：真实配置下三轮对话后 :rewind 2 conversation 再发一句，suga inspect trace --json 查看请求）`。
5. 收尾：关闭 suga-4tnd；在 gofer plan 中标记 done；本计划状态改为“已完成”，写不超过 8 行的完成总结。
6. 剩余：summarize-from-here、redo、CLI、ACP、sugasdk、trace 标注（设计 R9）。
