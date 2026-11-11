# 第 4 轮 Standards/Governance 评审（blocking closure）

## 候选四元组

- Git root：/d/work/inhere/my-tools-dev/inhere-tools/suga
- commit：d51491a15f7420fd99d2da97e699a1b0e45ce4dc（计划改动在 5c7bc582，d51491a1 只加评审台账）
- subject：docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md
- revision：0.4（上轮候选 466e0dc8 / 0.3）

## 评审者身份

独立评审子 agent（Claude Opus 5.5），轴：Standards/Governance；只读、REPORT_ONLY，未改文件、未提交。

## 实际读取范围

- `git diff 466e0dc8..d51491a1 -- <subject>` 全量（31 行）；
- 0.4 受影响章节：修订表与 0.4 处理表（:13、:53-61）、scope freeze（:71-74）、波次表（:249-255）、T1 Owner/动作（:261-267）、T2 Owner 与动作 8/10（:280-292）、T5/T6 Owner（:306、:318）、可追溯性表（:371）；
- 上轮本轴报告 docs/review/.../standards-review.md（466e0dc8 版）S14、S15 段；
- 代码事实核对（只为确认引用存在与包归属）：`internal/personal/segment_handoff.go:52,154`、`segment.go:357,431`、`context.go:43`、`turncontext.go:50-62`、`internal/wiring/runtime.go:715-725`、`internal/acpserver/i18n.go:1-25`、`server.go:117,144`；`runner.go`(933 行)、`runtime.go`(917 行)行数；
- 规范 payload/delta 沿用前轮已加载内容，本轮未重读；未运行测试。

## 上轮处置核对表

| ID | 上轮建议 | 0.4 处理 | 核对结论 |
|---|---|---|---|
| S14 [LOW] | T5 依赖补 T6，或相交列登记与 T6 同改 `runtime_build.go` | 波次表 :252 依赖改“T2、T6 合入”，相交列写明“与 T2 同改 `origin_guard.go`，与 T6 同改 `runtime_build.go`、`settings_registry.go`”；处理表 :60 | 已关闭 |
| S15 [INFO] | 可追溯性通知改为 G1 | :371 改为“T8、G1 / 文档（T8）与发版时的通知（G1）” | 已关闭，与授权 :95、G1 Gate 一致 |

## 修订引入项核对

- 越权：无。本轮未新增任何授权；推送/tag/安装/通知仍归 G1。
- 扩围：新增 owner 仅 `internal/personal/turncontext.go`、`runner.go`（`Config` 档位字段），服务于已批准的“回合开始事件记录 approval_mode”（F24），在 suga 仓库内；ACP 改用 `ServeOption`（`WithApprovalMode`）只是改传参方式，`server.go` 原已在 T2 owner 与 freeze 中，且符合 `acpserver/i18n.go:8-14` 既有写法，未引入新机制。
- turn_prompt 过滤：所引 `isContextUpdate`、`isSegmentContext`、`isInjectedItem`、`metaInjected` 均在 `internal/personal` 包内，与 `turn_prompt.go` 同包，复用既有判定，无跨包/分层问题。
- 三者同步：
  - scope freeze :74 已加 `turncontext.go` 与“`runner.go` 调用点与 `Config` 档位字段”；`runtime.go` 原已在 :72；
  - T2 Owner :280 已加 `turncontext.go`、`runner.go`；
  - `runner.go` 由 T1（一处调用，波 1）与 T2（Config 字段，波 3）共改，波次已串行，无冲突；`runtime.go` 由 T1/T2/T6 共改，波 3 已登记 T2 先、T6 rebase；
  - 波次表 T5 行已同步。结论：scope freeze、Owner、波次表一致。
- 文件大小约束：`runner.go` 933 行、`runtime.go` 917 行；本轮新增的只是一个 Config 字段与一处赋值，未违反 :227“新逻辑放新文件”的约束。
- 历史记录：0.2 处理表 :44（F16 行）仍写 `Options`/`ServeOption`，被 :59 取代，属历史记录，不另立项。

## 发现

均无 CORE_BLOCKING。

### S16 [INFO] `turn_prompt` 过滤条件与 `segment_handoff.go:154` 是两份手抄列表 — DEFERRED_ENHANCEMENT（可选）

- 证据：T1 动作 3（:265）要求 `turn_prompt.go` “按 `segment_handoff.go:154` 的过滤”再加 `metaInjected`；`segment_handoff.go:154` 本身只有三项判定。两处各自列条件，日后新增一类注入消息时容易只改一处。
- 建议（可选）：实施时在 `internal/personal` 内抽一个“是否真实用户消息”的同包判定供两处共用，或至少在两处互相加注释指向。不影响批准与执行。

## 结论

PASS：无 CORE_BLOCKING。S14、S15 已关闭；0.4 修订（turn_prompt 过滤、approval_mode 经 personal.Config、ACP ServeOption、波次依赖、scope/owner 同步）未引入越权、扩围、结构或一致性问题。仅 S16 一条 INFO。
