# 第 3 轮 Standards/Governance 评审（纠正确认轮）

## 候选四元组

- Git root：/d/work/inhere/my-tools-dev/inhere-tools/suga
- commit：466e0dc831090cd27fb18a8f5ed0b232534230d1
- subject：docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md
- revision：0.3（上轮候选 57643ac4 / 0.2）

## 评审者身份

独立评审子 agent（Claude Opus 5.5），轴：Standards/Governance；只读、REPORT_ONLY。

## 实际读取范围

- `git diff 57643ac4..466e0dc8 -- <subject>` 全量；
- 0.3 计划：修订表（:1-50）、计划工作声明/目标/授权（:52-100）、波次与依赖（:234-246）、T0/T2/T5/T6/T8 Owner 与动作、人工 Gate（:345-350）、可追溯性与剩余工作（:355-365）；
- 上轮本轴报告 standards-review.md 中 S12、S13 段及剩余风险；
- 设计 0.5 按 grep 核对 :256（发版时说明）；
- 确认新增 owner 文件在仓库中存在：internal/cli/chat.go、internal/chat/chat.go、internal/chat/render.go、internal/wiring/cli_mcp.go、internal/wiring/runtime_build.go、pkg/agentcore/tools/native/refs_context.go。
- 规范 payload/delta 沿用前两轮已加载内容，本轮未重读；未运行测试，未复核代码行号（属 Spec 轴）。

## 上轮处置核对表

| ID | 上轮建议 | 0.3 处理 | 核对结论 |
|---|---|---|---|
| S12 | Gate 披露“新键不可写为计划新增”；理由改准为只防模型关闭裁决与禁读；`tools.ask_on_external_origin` 仍可写记入剩余工作；不得扩到既有键 | Gate :347-349 增差异说明（计划新增、理由收窄、既有键仍可写）；修订表 :49；剩余工作 :365 新增“既有安全开关可被模型关闭（需另立设计）”；未改既有键可写性 | 已关闭。残余：0.2 处理表 :29（F14 行）仍写“防止模型关闭防护”，属历史记录且被 :49 取代，不另立项 |
| S13 | gofer 说明随 G1 发出 | 授权 :95“G1 发版时…不早于新版本安装”、:100；T8 :335-336 改为起草草稿；G1 :350“安装后发出” | 已关闭。授权、T8、G1 三处一致；残余见 S15 |

## 修订引入项核对

- 越权：无。新增授权仅为说明发送时点后移；推送/tag/安装仍归 G1。
- 扩围：新增 owner 文件（`cli/chat.go`、`chat/chat.go`、`chat/render.go`、`acpserver/server.go`、`wiring/cli_mcp.go`、`runtime_build.go`、`refs_context.go`）均为 F15–F21 所需的取值链或调用点，在 suga 仓库内、服务于已批准的 A/C，合理；scope freeze :63-64 已同步加入 `chat.go`、`render.go`、`cli/chat.go`、`refs_context.go`；`server.go`、`cli_mcp.go`、`runtime_build.go` 原已在 freeze 中。scope freeze 与 Owner 同步。
- 授权表述：一致（见 S13）。
- 结构：见 S14（波次相交核对未随 Owner 同步）。

## 发现

均无 CORE_BLOCKING。

### S14 [LOW] 波次表“文件相交核对”未随新增 Owner 同步 — CORE_CORRECTIVE

- 证据：T6 Owner :295 新增 `runtime_build.go:161`；T5 Owner :307 也改 `runtime_build.go`；波次表 :241 T5 依赖仅“T2 合入”，相交列只列 `origin_guard.go`、`settings_registry.go`；:240 波 3 的 T2/T6 共同文件未提 `runtime_build.go`（T2 不改，正确），但 T5 与 T6 在 `runtime_build.go` 上的相交未登记。
- 影响：若按“依赖”列执行，T5 可在 T6 未合入时开工，两者同改 `runtime_build.go`，属 Ownership Conflict 风险；波次顺序（4 在 3 后）在多数读法下已隐含规避，故为 LOW。
- 建议：:241 依赖改为“T2、T6 合入”或在相交列补“与 T6 同改 `runtime_build.go`，T6 先合入”。

### S15 [INFO] 可追溯性表“兼容与使用方说明 → T8 / 文档与通知”与新时点不符 — DEFERRED_ENHANCEMENT（可选）

- 证据：:360；通知已移到 G1（:95、:350），T8 只起草（:336）。
- 建议：改为“T8、G1 / 文档；G1 发出说明”。不影响执行。

## 结论

PASS：无 CORE_BLOCKING。S12、S13 已正确处理；修订未引入越权、扩围或授权不一致；scope freeze 与 Owner 同步。S14 为 LOW 纠正项（建议批准前顺手修正），S15 为 INFO。
