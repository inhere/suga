<!-- template_id: review; template_version: 1.2.0 -->
# suga 会话 rewind 与 fork 实施计划 规范轴评审

## 评审目标与范围

- 目标: `docs/plans/2026-10-31-session-rewind-fork-plan.md`（git root `inhere-tools/suga`，candidate commit `bae17865`）
- 修订版本: Draft 0.1
- 评审者: 规范与治理轴（Standards/Governance）独立评审子 agent，只读；未读本计划的可执行轴报告
- 轮次: Round 1（discovery，全量读）
- 日期: 2026-10-31
- 排除项: 未运行测试与基准；fork 映射、分页补足、嵌套 cut 的逐行推演归可执行轴

## 输入与方法

- 规范：`idevstd load --delivery semantic --task plan --task review --consumer-mode --thinking-mode rigorous`，BOUND、FULL、18 项资源，payload sha256 已核验（`a7be77b2…`）。payload 未含 plan 合同与 plan 模板，另从已绑定 runtime 1.2.0 只读查阅 `plan-contract.md` 与 `templates/plan.md`（template 1.3.0）作对照。
- 读取：计划全文；设计 0.2 全文；设计评审 `standards-review.md`、`spec-review.md`（第 2 轮遗留项）；仓库 `AGENTS.md`。
- 结构校验：`idevstd script validate_document --kind plan <plan>` → `ok: true, errors: []`。
- 代码核对：对计划“预期符号”与各任务引用逐条 `sed`/`grep`（结果见“验收标准”下的核对表）。

## 验收标准

plan 合同（思考声明、修订记录、Capability Discovery 三表、`host_or_non_offline_action`、前置/fail-closed、波次依赖、任务五要素与运行目录、跨任务签名、workspace baseline、人工 Gate、exact 可追溯、完成 Gate）；评审合同与 SR1301 安全清单；GR104/GR107–GR110、SR2002–SR2004；AGENTS.md 验证要求。

代码位置核对结果：

| 引用 | 结果 |
|---|---|
| `context.go:111` loadHistory、`segment_handoff.go:97/112`、`handoff.go:124`、`lastreply.go:23`、`session_card.go:99` | 一致 |
| `store/entries.go:24,291,321,358`、`fts_snapshot.go:27`、`turns.go:30,61,91`、`sessions.go:45` | 一致（AppendEntries、LatestEntry、RecentEntries、CountEntriesByType、SearchEntriesPage、CreateTurn、ListTurns、CountTurns、CreateSession） |
| `undo.go:207-227,347,363-366`、`gate.go:115-140`、`snapshot_retention.go:620-628`、`observe.go:311` | 一致（gate 范围落在 Acquire/TryAcquire 内） |
| `runner_sessions.go:181` writeStartSegment、`segment_clear.go:100`、`compact.go:176`、`replay.go:233`、`admit.go:165`、`steer.go:418` | 一致 |
| `ledger.go:100-135,262`、`chat/steering.go:205-212`、`chat/chat_usage.go:103-115` | 一致 |
| `restoreItem`(undo_restore.go:143)、`contentConflict`(:346)、`UndoWithOptions`(undo_options.go:20)、`TakeNotices`(tasks.go:686)、`Manager.Running`(:766)、`clearRequest`(prefix.go:366)、`SetCheckpointState`、`OnFilesRestored`、`HydrateEntries`、`DeleteSession`、`DeleteSessionFiles`、`idx_entries_type`、`metaInjected`、`chat.undo_confirm`、`BenchmarkSessionCard`、`make smoke-chat/smoke-go`、`undo_notice.go`、`fakellm_test.go` | 均存在 |
| Baseline：HEAD `d3782cdc` + 一个 tracker 提交 | 属实（`cbab7594`），之后是计划自身提交 `bae17865`；工作区干净 |

## 发现

### S1 [MEDIUM] 波次依赖与任务依赖不一致，存在隐藏依赖（CORE_CORRECTIVE）

同类全部出现点：
- T2 写“依赖：无（…T1 未合入前先用接口桩）”，但 W1 位于 W0 之后，T1 必已合入，“接口桩”自相矛盾；且 T2 要“排除已被屏蔽的轮”，需要 T3 的 `masked.Hidden`（`visible.go`），而波次表写“T2 与 T3 可并行”。并行时 T2 只能自建一份区间判断，与 T3 重复实现。
- T4 要实现“设计流程 1 第 4 步的失败处理表”各分支，其中“文件部分返回 error”“文件已恢复但写 entry 失败”都需要 T6 的 `rewindFiles`；但 T4 与 T6 同波并行，T4 未依赖 T6。
- T7 “前置拒绝条件同 rewind”，复用 T4 的前置检查，但 T7 不依赖 T4，存在复制前置检查的风险。
- T9 的三种 scope 用例依赖 T0 的按轮写入能力，但 T9 依赖只写 T8（经 W0 时序间接满足）。
- 建议：T2 依赖改为 T1、T3（或把 masked 集合构造前移到 T1/T2 并由 T3 消费，二选一写死）；T4 依赖加 T6，或把“编排文件部分 + 失败表”移到依赖 T4、T6 的任务；前置检查函数指定唯一 owner（T4）并让 T7 依赖它；T9 依赖写 T0、T8。

### S2 [MEDIUM] 跨任务调用的函数缺签名、返回格式与错误归属（CORE_CORRECTIVE）

- 证据：plan 合同要求“任务新增或改变会被其他任务调用的函数…写出签名、返回格式、错误类型与文案归属”。计划中被他任务消费的接口：
  - T1 `RecentEntriesBefore`/`LatestEntryBefore` 只有参数名无类型与返回；`SearchEntriesPage` “支持传入屏蔽区间”未给 `EntrySearch` 新字段形状，也未说明 workspace/all 作用域下多会话区间如何传入（逐会话 map 还是 SQL 子查询）；`CreateForkedSession` 的“seq 映射”返回类型未定。
  - T3 `masked` 的构造函数与 `Hidden` 签名、谁负责读 `RewindRecords`。
  - T4 `Runner.Rewind(ctx, sid, target, scope)` 无返回类型；T5/T6 产出的“回退计划”（被丢弃通知、恢复/跳过/不可恢复清单）由谁组装、以什么结构交给 T8 打印，未定义。
  - 设计流程 1 是“校验 → 生成并打印回退计划 → 确认 → 执行”。现有 `:undo` 用 `ListCheckpoints` + `BuildUndoPlan`（`internal/chat/chat_undo.go:150,236`）先预览再执行；计划没有预览/dry-run API，也没写 Rewind 在等待用户确认时是否持锁。T8 无法唯一实现确认交互。
  - T7 `Runner.Fork` 返回值（新会话 ID）未写，而 T8 要打印它。
- 建议：在 T1/T3/T4/T7 各补一行签名；新增“回退计划预览”API（如 `Runner.RewindPlan` 只读、执行时重新校验），并写明锁只在执行段持有。

### S3 [MEDIUM] sugasdk 也调用 `SearchEntriesPage`，计划未声明默认行为不变（CORE_CORRECTIVE）

- 证据：`pkg/sugasdk/engine.go:296` 直接调用 `repo.SearchEntriesPage`。计划 scope freeze 排除 sugasdk，R11 规定“FTS 数据保留”，但 T1 只写“支持传入屏蔽区间”。若实施成默认排除，sugasdk 搜索结果会悄然变化，越过 R9/R11 的范围。
- 建议：T1 写明排除为显式开关（零值 = 不排除），只有 `ledger.go` 打开；T1 验证加“不开开关时结果与基线一致”用例。

### S4 [MEDIUM] 可追溯表未 exact-cover 设计决策与读者清单（CORE_CORRECTIVE）

- R9（延后项）与 R10（排进 0.54）未出现在可追溯表；R9 只在“排除”段引用，R10 只体现在发版 Gate，表中应各有一行（R9 → 无任务 + 排除段 / 完成 Gate 第 6 条；R10 → 外部动作 Gate v0.54.0）。
- R11 在设计中是 `session_search` **与 recall**（设计读者表 `ledger.go:124,262`）。T3 动作只写“检索”，验证只列“检索排除三种作用域”；recall（`ledger.go:262` 按 ref 的 `ListEntries(SinceSeq…)`，可指向任何会话）没有动作与用例。按 ref 召回被屏蔽 seq 是 R11 的直接泄漏路径。
- `session_card.go:194` 另有一处 `LatestEntry(EntryPlan)`（LatestSummary/plan），T3 只笼统写“session card 用游标补足”，未点名。
- 建议：可追溯表补 R9、R10 两行；T3 动作与验证点名 recall（被屏蔽 seq 的 ref 返回空或拒绝）与 `session_card.go:194`。

### S5 [LOW] 任务文件清单不完整（CORE_CORRECTIVE）

- T5 要把三种通知文案加入 zh/en i18n，模型侧文案在 `internal/i18n/catalog/*/model.ini`（`model_texts.go` 注释），但 T5 文件清单只有 `rewind.go`、`undo_notice.go`，catalog 仅出现在 T8。
- T4 新增 `rewind_records.go`，设计“代码组织”把回退记录放在 `rewind.go`；属可接受细化，但应在计划中注明偏离理由（SR2004 对偏离要求写理由，此处为同包拆分，影响小）。
- T6 清理检测需读 events（`store/events.go:40 ListEvents`），T3/T4/T6 均未把它列入预期符号。
- 建议：T5 文件加 `internal/i18n/catalog/{zh,en}/model.ini`；T4 注一句拆分理由；预期符号补 `ListEvents`、`ListCheckpoints`、`session_card.go:194`。

### S6 [LOW] Capability decisions 未覆盖全部所需能力（CORE_CORRECTIVE）

- 证据：只有 CAP-01–04。以下能力在任务中出现但无 CAP 行，无法看出“复用 → 扩展 → 新建”的检索结论：fork 附件复制与 `out:<会话>:` 改写（全仓 `grep 'func.*[Cc]opy.*(Image|Spill|Session|Body)'` 无现成实现，即新增能力）、回退预览（可复用 `ListCheckpoints`/`BuildUndoPlan` 模式）、清理检测读 events（复用 `ListEvents`）、失败清理（复用 `DeleteSession`+`DeleteSessionFiles`）、用户轮索引（复用 `ListTurns` + `metaInjected`）。
- 建议：补 CAP-05..09，各写 decision（DIRECT_REUSE / OWNER_EXTENSION）与 proven_gap；附件复制写明归属 `internal/personal/fork.go` 而非 store。

### S7 [LOW] 反向验证覆盖不足（CORE_CORRECTIVE）

- T2 的 SR1301 归属校验（跨会话 `turn_id`、已屏蔽轮）无反向验证；T5 的 F-N4（undo pending 合并而非丢弃）无反向验证。这两项分别是安全校验与评审遗留纠正项，缺反向验证则无法证明用例真能拦住回归。
- ADVISORY 部分：T4 失败分支、T8 命令无反向验证；T9 五个 e2e 场景只有一个反向验证（AGENTS.md：新场景写完做一次反向验证）。
- 建议：T2 加“去掉归属校验，跨会话用例失败”；T5 加“改回丢弃 pending，合并用例失败”；T9 至少对 fork（源会话不变）与回合中放回各补一个反向验证。

### S8 [LOW] 验证命令未写运行目录；主机命令未给 gofer 提交形态（CORE_CORRECTIVE）

- 证据：plan 合同要求“命令写明运行目录（相对 Git root）”。全文未写；实施在 `.worktrees/<branch>` 下，主机 Windows 用例（T9、完成 Gate 第 2 条）未给 `--cwd` 形态。
- 建议：在“前置检查”加一句“所有命令在任务 worktree 根运行；主机命令以 `gofer job run -p my-tools-dev -a exec --runner local --cwd inhere-tools/suga/.worktrees/<branch> --sync -- …` 提交”。

### S9 [LOW] 外部动作 Gate 前后不一致（ADVISORY）

- “授权”段写发版 v0.54.0、push、**安装**走外部动作 Gate；“人工 Gate”第 3 条只列发版、push。按合同应是一个覆盖全部出机器动作的 Gate 并逐项列目标。
- 建议：Gate 第 3 条补“安装”（及如有的内网上传），两处一致。

### S10 [LOW] 文字与基线细节（ADVISORY）

- fail-closed 写“两仓库都只在 `.worktrees/` 下实施”，本计划只涉及 suga 一个仓库。
- workspace baseline 未显式写 Git root 与 branch（`inhere-tools/suga`、`main`），也未列 preserved dirty（当前为空，写“无”即可）。
- T0、T1、T3、T8 的任务验证用整包 `go test ./internal/personal/` 等；合同建议任务级写 focused `-run`，整包放波次末/完成 Gate（完成 Gate 已有整包）。
- T5 采纳 F-N4（合并 undo pending）、T1/T7 采纳 F-N1（`CreateForkedSession`），与设计 0.2 正文（§3“同样丢弃”、§2“CreateTurn + 一次 AppendEntries”）字面不同；计划已引用评审编号，建议各加半句“依设计评审第 2 轮纠正项，替代设计正文 §x 的写法”，避免实施者按设计正文回退。

### SR1301 安全核对

- 鉴权：不适用（本地 chat，无新增入口）。
- 注入防护：T1 在 SQL 中按区间排除 seq，计划未写参数化要求——建议 T1 动作注明区间以绑定参数传入（并入 S2 修订即可，ADVISORY）。
- 秘密不进日志/输出：不适用（事件 attrs 只含轮、scope、文件统计）。已核对。
- 入参校验：T2 `resolveTarget` 已覆盖序号/turn_id。已核对。
- 资源归属：T2 校验 turn_id 属于该会话。已核对（反向验证见 S7）。

### 已核对且无问题

- 模板 1.3.0 首行、状态 `Draft 0.1`、修订记录、计划工作声明（RIGOROUS、核心目标、scope freeze、暴露度、预算、停止条件）齐全；validator 通过。
- Capability Discovery 三表 grammar、`NONE` sentinel、Rejected new tools 删除测试成立；CAP-01（gate 不可重入 → 不加锁新函数复用 restoreItem）、CAP-03（无跨调用事务 → store 内新增）决策合理。
- `host_or_non_offline_action=REQUIRED` 恰一处，理由明确（主机 go test / ptye2e，tmp SUGA_HOME）。
- 授权边界：计划批准只授权 worktree 实施与本地提交；合入、发版、push 另行 Gate；`make smoke-chat` 作为合入前提并用 `&&` 串接，fail-closed。
- 设计评审遗留项 S-N1、S-N2、F11、F-N1–F-N5 均已落到任务（目标第 6 条映射与任务正文一致；spec 评审提到的 `Hops>0` 是否用户轮也落到 T2）。
- 结构条目：无新入口/CLI，GR107/GR110/SR2003 不适用；装配不变（GR108）；GR109 在 T4 点名；GR104 由设计限 400 行；SR2002 由 AGENTS.md 约束。真实环境披露（完成 Gate 第 4 条 `UNVERIFIED_REAL_ENV`）符合要求。

## 覆盖缺口与限制

未运行任何测试或基准；未推演 SQL 分页与 fork seq 映射正确性；plan 合同与模板从 runtime 1.2.0 目录只读查阅（不在本次 semantic payload 内）；未核对“B1–B3 与 S50A1 已合入”的说法。

## 结论

PASS（规范与治理轴，Round 1）。无 CORE_BLOCKING；S1–S8 为 CORE_CORRECTIVE，按评审合同须在计划批准前修正；S9、S10 为 ADVISORY。

## 剩余风险与后续

- S1、S2 不修正时，W1/W2 并行实施会出现重复的区间判断与前置检查，T8 确认交互实现不唯一。
- S3 不修正时有越过 sugasdk 范围的风险。
- 修订后第 2 轮只需读 `bae17865..<新 candidate>` 的 diff 与依赖章节。
