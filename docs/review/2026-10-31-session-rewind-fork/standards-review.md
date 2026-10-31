<!-- template_id: review; template_version: 1.1.0 -->
# suga 会话 rewind 与 fork 设计 规范轴评审

## 评审目标与范围

- 目标: `docs/design/2026-10-31-session-rewind-fork-design.md`（git root `inhere-tools/suga`，candidate commit `0ae0a20c`）
- 修订版本: 0.1
- 评审者: 规范轴独立评审子 agent（只读，与可执行轴隔离）；axis = Standards/Governance；round 1
- 日期: 2026-10-31
- 排除项: 实现能否落地与算法正确性（归可执行轴）；未运行测试

## 输入与方法

- `idevstd load --task review`（BOUND，FULL，payload hash VERIFIED）；补读 1.2.0 design-contract 与模板 1.2.0。
- 设计全文、`gofer issue show suga-4tnd`、0ae0a20c diff；抽查 `pkg/sugasdk/doc.go`、`types.go`、`internal/personal/{context,segment_handoff,handoff,undo_options,undo_restore,snapshot_retention,taint,segment_clear,runner_sessions,gate,lastreply,compact}.go`、`store/{entries,toolbody,events,types}.go`、`internal/chat/steering.go`；全仓 grep `ListEntries`/`LatestEntry` 非测试调用点；xhen 对 sugasdk 的使用。
- `validate_document --kind design` ok。

## 验收标准

review-contract、design-contract 1.2.0、模板“代码组织”；SR1101–SR1108、SR1301、SR1403–SR1410、GR104、GR107–GR110、SR2002–SR2004；issue ①② 与验收。

## 发现

### S1 [HIGH] 读者清单不全，被屏蔽内容经未列出的读者回到模型上下文（CORE_BLOCKING · BUG）

- 证据: 只列了 loadHistory、latestPlan、session card、SegmentHistory；漏 `segment_handoff.go:112-124 recentRequests`（交接包用户原话）、`handoff.go:124 buildHandoff` 的 `LatestEntry(EntryPlan)`、`lastreply.go:28 LastReply`、`ledger.go:262`；按 turn 读取的 `compact.go:176`、`replay.go:233`、`admit.go:165`、`steer.go:418` 未说明。
- 影响: rewind 后的 compaction/handoff 把被回退的原话与 plan 写进新交接包，R2“不泄漏”不成立。
- 建议: 读者规则改为“凡进入模型上下文或用户可见当前状态的 entry 读者”，同类穷尽逐个标注。

### S2 [MEDIUM] session_search/Entries 可见只作事实陈述未作决策（CORE_CORRECTIVE）
### S3 [MEDIUM] trace“已回退”标记超出范围与代码组织（CORE_CORRECTIVE）
### S4 [MEDIUM] 多步写入失败语义不完整（GR109）（CORE_CORRECTIVE）
### S5 [MEDIUM] SR1301 入参校验与资源归属未写；fork 继承 `:rw` 授予未说明（CORE_CORRECTIVE）
### S6 [LOW] taint 继承判据单位不一致（tainted_at 是时间，cut 是 seq）（CORE_CORRECTIVE）
### S7 [LOW] 代码组织缺装配项（GR108、SR2004）（CORE_CORRECTIVE）
### S8 [LOW] 工作声明与待确认事项有过时表述，review budget 未写（CORE_CORRECTIVE）
### S9 [LOW] 部分事实引用不准（store 路径、undo_options 行号、heldLocks 位置等）（CORE_CORRECTIVE）
### S10 [LOW] 回滚到旧版本的“两条分支拼接”未披露（CORE_CORRECTIVE）
### S11 [LOW] 回填第 N 轮提问到输入行不在 issue ①② 内（OUT_OF_SCOPE）

## 覆盖缺口与限制

按 turn 读取的读者只看调用形态；fold/loaded_tools 重算与 seq 映射归可执行轴；未运行测试；design-contract 为补充读取。

## 结论

BLOCKED（S1）。S2–S10 CORE_CORRECTIVE（S7 为结构类，计划批准前必须修正）；S11 OUT_OF_SCOPE。

## 剩余风险与后续

修订 S1 时按“所有 entry 读者”同类穷尽并写明自查范围；修订后进入第 2 轮 confirmation，读 `0ae0a20c..<新 candidate>` diff 与总体方案、已确认事实、安全各节；与可执行轴核对 S1 处置一致。
