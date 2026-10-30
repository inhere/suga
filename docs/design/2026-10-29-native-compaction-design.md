<!-- template_id: design; template_version: 1.1.1 -->
# 原生压缩端点（`/responses/compact`）接入 设计

> 状态：Draft 0.1 / 待人工计划批准

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-29 | suga | 初稿 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 背景与目标

suga 现在把「压缩」（`:compact`、上下文阈值自动分段、空闲整理）实现为**一次完整的摘要模型请求**：把待丢弃的消息渲染成包，请模型产出 7 段式 handoff 包，写入段记录（`internal/personal/compact.go:385-389`）。实测（会话 `sg_c26ak287eqpm`）一次空闲压缩花 142s、3 次请求。而 Codex / Responses 后端本身提供**原生压缩端点**：把消息序列交给上游，直接拿回压缩后的消息序列，不用再请模型写摘要；Codex CLI 自己走的就是这条路。

用户可见目标：

1. 压缩更快、更省（不再付一次完整摘要请求的 token）。
2. 压缩不再把主对话的上游 prompt 缓存冲掉（现状已用独立缓存键缓解，原生压缩可进一步减少一次请求）。
3. 行为可回退：任何端点不支持、失败或产物无法解释时，回到现在的本地摘要，不让一次压缩失败。

非目标（本次不做）：替换 `--from` 交接包；改动折叠（fold）与段边界语义；接入 Codex turn-state 接力（`X-Codex-Turn-State`）；为 Anthropic / openai(chat) 类型实现原生压缩；历史会话迁移。

## 名词

- **段 / 段记录**：一次压缩开启的新上下文区间；落盘为一条 `store.EntryCompaction` 记录（`compactionRecord`）。
- **摘要包**：现在压缩的产物，7 段式 markdown（当前任务 / 坐标 / 采纳与否决 / 验证到哪 / 进展与阻塞 / 下一步 / 引用）。
- **原生压缩**：`POST {base_url}/responses/compact`，请求 `{model, input[], instructions, prompt_cache_key}`，响应 `{output[], instructions}`（axonhub 实现见 `llm/transformer/openai/responses/compact_inbound.go:34-87`、`compact_outbound.go:17-33`）。
- **回落**：本地摘要路径，即当前实现。

## 范围与非目标

范围：`openai-responses` 类型的压缩路径（chat 的 `:compact`、阈值分段、空闲整理）；配套配置项、trace/`suga inspect` 可见性、测试与文档。

非目标：见上节；本次也不改 SDK/ACP 的对外类型（沿用 `CompactResult`）。

## 已确认事实与规范

- **F1** 原生压缩的请求需要一个 `model`；`input` 是**有序消息数组**，不能被折叠成纯文本；`instructions` 与 `prompt_cache_key` 原样透传（`tmp/axonhub/llm/transformer/openai/responses/compact_inbound.go:53-75`、`compact_outbound.go:26-33`）。
- **F2** 响应体的 `output` 是压缩后的 item 列表，axonhub 把它转回消息（`compact_outbound.go:75-115`），即产物是**消息序列**而非摘要文本。
- **F3** 路由：上游暴露 `/responses/compact`，Codex 渠道还有 `/codex/responses/compact`（`tmp/axonhub/internal/server/routes.go:210`、`internal/server/codex_routes_test.go:181`）。
- **F4** suga 现状：压缩在 `internal/personal/compact.go`（`breakSegment` 提交一条段记录、`compactionRecord{Summary, FirstKeptSeq, TokensBefore, TokensAfter, Reason, Context, RecentRequests, Refs}`），对外结果 `CompactResult{TokensBefore, TokensAfter, FirstKeptSeq, Segment}`；段摘要作为下一段的段头进入上下文（`b.summary`）。
- **F5** 摘要请求已使用独立的上游缓存键 `<会话>-compact`（提交 `3bc75d65`），接入原生压缩不必再改缓存键逻辑。
- **F6** 适用规范：本设计遵循 SR1103 / SR1104 / SR1107；治理暴露度按 SR1409 为低（本项目未投产、无生产数据、可回滚）；实施范围确认按 SR1204，改动将超过 3 个文件，需在实施前单独确认。
- **F7** 公开声明（SR1108）：`thinking_mode=RIGOROUS`；核心目标＝压缩路径改用原生端点且可回退；`allowed_scope`＝`openai-responses` 压缩路径 + 配置 + 可见性 + 测试；`non_goals` 同上；`expansion_policy=DEFER_OR_REQUEST`；review budget＝1 轮初稿 + 最多 1 轮修订；停止条件＝出现改变用户结果/授权/接口/数据的选择即停止并请人拍板。

## 总体方案

**推荐方案 A：原生优先 + 本地回落。**

1. 触发压缩时先判断该 provider 是否支持原生压缩（见 D2），支持就走 `POST /responses/compact`，把待压缩段的消息（含工具调用与结果）按现有顺序序列化成 `input`，带上段的 `instructions`（系统提示）与缓存键。
2. 成功且产物可解释 → 写段记录，新段的段头是**原生产物消息序列**；`Summary` 留空，新增字段标记来源与消息。
3. 任何失败（4xx/5xx/超时/item 类型不认识/产物为空）→ 走现有本地摘要，trace 与日志记 `native_compact=fallback` 及原因，不静默。

被否方案：

- **B 只用原生缩短输入、仍本地生成包**：省不了那次摘要请求，收益接近零，且增加一条状态分支。
- **C 不接入**：保留现状（可以，但要继续付每次压缩的完整请求）。

## 架构

- `pkg/agentcore/llm`：`ResponsesProvider` 增加 `Compact(ctx, req)`（路径 `/responses/compact`），请求体复用现有的 model/instructions/input 渲染与 header 处理；`Compat` 增加一个适配名（如 `no_native_compact`）用于自动排除不支持的网关；新增 `ErrCompactUnsupported` 供上层判定回落。
- `internal/personal`：`breakSegment` 在选择路径处分支；`compactionRecord` 增 `SegmentKind`（`package|native`）、`Messages`（规范化后的消息）与 `Version`；`loadHistory`/`compose` 渲染段头时按 `SegmentKind` 取包或消息；`CompactResult` 增加 `Native bool`（对外兼容，默认 false）。
- `internal/config`：`providers.<id>.native_compact`（`auto|on|off`，默认 `auto`），注册表与校验同本仓既有 provider 键的模式；`build_provider.go` 传给 `llm.ResponsesConfig`。
- 可见性：trace 的 compaction 事件增 `native_compact`、`native_messages`、`fallback_reason`；`suga inspect export`/`:compact` 提示区分「原生」与「本地摘要」；会话标题/统计不新增字段。

## 关键流程

```
压缩触发（阈值 / :compact / 空闲整理）
  ├─ 读段视图（现有 compose / prepareImages）
  ├─ provider 支持且开关允许？
  │    ├─ 是 → POST /responses/compact（prompt_cache_key = <会话>-compact）
  │    │        ├─ 2xx 且产物可解释 → 写段记录（SegmentKind=native, Messages）
  │    │        └─ 失败/不可解释 → 记 fallback_reason → 本地摘要
  │    └─ 否 → 本地摘要（现状）
  └─ 段记录写入即提交点（现有语义），新段上下文 = 段头 + 快照
```

## 安全、数据、运维与回滚

- 数据：段记录新增可选字段，旧记录仍按 `Summary` 读（向后兼容）；旧二进制读到 `native` 段时只看到空摘要，因此**新字段写入前需确认读取侧兼容**（见 D4）。
- 安全：压缩请求携带的就是即将被压缩的段消息，与现有摘要请求同一外发面，不新增数据类别；不写新日志字段时不含秘密。
- 运维：失败可见；原生路径耗时与请求数进 trace，可与本地摘要对比。
- 回滚：`native_compact: off`（或全局 `auto` 下不做任何事）即回到现状；已写入的 `native` 段继续被新版本读取，因此回滚需保证读取侧兼容（D4 的前置条件）。

## 决策

- **D1 产物表示**：建议「消息序列 + `Summary` 留空」。理由：F2 说明上游返回的就是消息，硬转成 7 段包会丢工具结果原文，还要再付一次模型请求。
- **D2 开关默认**：建议 `auto`＝仅当 `Compat` 判定该网关支持原生压缩（Codex 渠道）时启用；其它 Responses 网关保持本地摘要，避免把 `:compact` 变成一次 4xx。
- **D3 回落条件**：HTTP 404/400/5xx、超时、未知 item 类型、产物为空、产物消息数不少于输入（没有压缩效果）——最后一类也回落，避免写入无收益的段。
- **D4 读取兼容**：新字段写入前，旧读取者必须能安全读取；建议 vet 一次「旧二进制读新段记录只看到空摘要」的风险，必要时把 `SegmentKind` 作为写入闸门（默认仍写包）。

## 待确认事项

- **Q1 数据模型**：接受「段摘要 = 消息序列」这一变化吗？它影响 `suga inspect` 展示、handoff 复用（`HandoffReusePrefix`）与折叠指纹的计算；不接受则只能选方案 C 或方案 B。
- **Q2 启用范围**：只对 Codex 渠道启用（`auto`），还是允许手写 `on` 强制任何 Responses 网关尝试？
- **Q3 空闲整理**：空闲整理也走原生压缩吗？它更在意「短」而不是「保原文」，可能更适合本地摘要包。
- **Q4 两档保留**：是否同时保留「摘要包（高压缩率）」与「原生消息（保原文）」两档，由原因决定（阈值/`:compact` 用原生，空闲整理用包）？

## 结论与人工计划 Gate

接入原生压缩的收益真实（省一次完整摘要请求），但代价是把段摘要的数据模型从「7 段包」改成「消息序列」，属于 DPI-flow 的 Full 改动。本设计建议方案 A + D1~D4，**Draft 0.1，等待人工批准**：批准后才编写实施计划（plan），计划再经人工确认后才实施。Q1~Q4 请在批准前给结论。
