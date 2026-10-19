<!-- template_id: design; template_version: 1.1.1 -->
# assistant 大字段移到会话文件设计（推理内容与工具调用参数）

> 状态：Draft 0.1 / 待用户审批
> 日期：2026-10-20
> 关联：`docs/design/2026-10-19-tool-output-files-design.md`（tool 正文移到会话文件，v0.36.0 已发布）、`docs/design/2026-10-19-data-governance-design.md`、issue `suga-zy9f`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-20 | 监督（Claude） | 用户 2026-10-20：“先做 1 和 2，3 和 4 你出设计”。3 = 旧会话的推理内容（Reasoning 35MB + Responses 加密推理 Metadata 43MB），4 = 大的工具调用参数（30MB，file_write/file_edit 全文为主）。两者都在 assistant 条目里，用同一套机制处理：把 assistant 条目的大字段随整条 JSON 移到会话文件，库里留含完整正文 Content 的小桩；同时让上下文组装只取回当前分段的正文。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：assistant 条目里的推理内容与大工具调用参数不再占数据库；发给模型的请求、恢复会话、导出与 trace 读到的内容与现在逐字节相同；全文检索结果不变。
- allowed_scope：`internal/personal/store`（桩形状扩展到 assistant、取回、维护命令）、`internal/personal/context.go`（只取回当前分段）、`internal/cli` 的 `data tool-bodies`（或改名的命令）、测试与文档。
- non_goals：丢弃推理内容（保留为备选，不做）；改变 provider 请求；改变 FTS 的检索内容；压缩会话文件。
- expansion_policy：DEFER_OR_REQUEST。需要新增迁移以外的启动期工作、或改变读取接口语义时回到 Gate。
- review budget：中；机制复用 v0.36 的 tool-bodies，新增风险集中在“FTS 不能读到桩”与“只取回当前分段”两点，实施前做一次独立审核。
- 停止条件：用户批准前不改代码。

## 背景与目标

只读统计用户真实库（2026-10-20，326MB）：assistant 条目 46,677 条共 114MB，其中 `Metadata` 43MB（主要是 Responses 接口的加密推理 `suga.responses_reasoning`，续聊时原样发回）、`Reasoning` 35MB（模型思考文本）、`ToolCall.Arguments` 30MB（file_write 10、file_edit 9.8、shell 3）、`Content` 只有 2.6MB。

这些字段只在“把历史发回模型”时需要；而上下文组装发回的只是当前分段（压缩摘要 + `first_kept_seq` 之后的条目，`internal/personal/context.go:74-90`），更早的条目只用于导出、trace、回看。

另一个现状问题：`context.go:128` 每一轮都 `ListEntries` 整个会话的全部条目，v0.36 起这意味着每轮都要从 `tools.jsonl` 取回全部 tool 正文，即使它们在压缩点之前、不会被发送。

目标：

1. assistant 条目超过阈值时，随整条 JSON 移到会话文件，库里留小桩；
2. 上下文组装只取回 `first_kept_seq` 之后（当前分段）的正文，之前的条目只取桩；
3. 读到的内容逐字节相同，FTS 检索结果不变；
4. 历史数据可迁移、可还原、可校验，备份照常包含。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| tool-bodies 机制：写事务内追加 `sessions/<sid>/tools.jsonl` 并改桩，`body_ref` 列识别桩，`ListEntries`/`LatestEntry` 透明取回，`--move/--undo/--check/--status`，备份始终包含 | v0.36.0，`internal/personal/store/toolbody*.go` | 直接扩展到 assistant 条目，不另造一套。 |
| FTS 正文表达式读 `json_extract(json,'$.Content')`（以及 tool 的 `ToolResult.Content`），assistant 只索引 `Content` | `internal/personal/store/fts.go:54-56` | assistant 桩必须保留**完整** `Content`（总共只有 2.6MB），FTS 回填/维护读到桩时结果不变。 |
| `ListEntryRefs` 用 `json_extract` 取 `ToolCall.CallID` | `internal/personal/store/entries.go:120` | 桩保留 `ToolCall{CallID, Name}`。 |
| 上下文 = 摘要 + `first_kept_seq` 之后的条目 | `internal/personal/context.go:74-90`、`:128` | 取回范围可以按 seq 限定。 |
| P2 去重只针对 tool 条目，且跳过桩 | `internal/personal/store/tool_dedupe.go` | 不受影响。 |
| 前缀缓存哈希基于发送的消息编码 | `internal/personal/prefix.go:96` | 只要当前分段的消息逐字节相同，缓存不受影响。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

### 1. assistant 桩

- 移出条件：`type='assistant'` 且条目 JSON 超过阈值（沿用 `store.tool_body_inline_bytes`，默认 1,024 字节；配置名可改为 `store.body_inline_bytes` 并兼容旧名，见 Q2）。
- 桩保留：`Role`、**完整 `Content`**、`ToolCall{CallID, Name}`（不带 Arguments）；不带 `Reasoning`、`Metadata`。`body_ref` 列与 tool 桩相同。
- 正文仍写进同一个会话文件（`tools.jsonl`，名字保留以兼容；见 Q3），记录格式与校验不变（原始字节、seq、sha256）。
- 新写入在 `AppendEntries` 写事务内移出（与 tool 相同的顺序与回滚）；FTS 行在改桩**之前**由原 JSON 写入，内容与现在相同。

### 2. 上下文组装只取回当前分段

- `store.EntryFilter` 增加 `HydrateFromSeq int64`：seq 小于它的带桩条目返回桩（不读文件），大于等于的取回正文。0 表示全部取回（默认，保持现有调用方行为）。
- `context.go` 先读最新的压缩记录得到 `first_kept_seq`，再以它调用 `ListEntries`；更早条目只用于判断回合状态等元数据，不发送。
- 交接（segment_handoff）、压缩生成摘要时读取的是需要被总结的那一段：它们的调用方显式传 0（全部取回）或该段起点，保证摘要内容不变。
- 效果：长会话每轮的文件 IO 只与当前分段大小有关；对已发布的 tool 正文同样受益。

### 3. 维护命令

- `suga data tool-bodies` 的 `--move/--undo/--check/--status/--slim` 扩展到 assistant 条目（`--status` 分 tool / assistant 两行）；或改名为 `suga data bodies` 并保留旧名为别名（见 Q2）。
- 历史迁移分批在写事务内，断点可续、幂等；`--undo` 逐字节还原 assistant 条目。
- 完成后提示 `suga data vacuum`。

### 4. 不采用的方案

- **直接丢弃旧会话的推理内容**：最省空间，但续聊时 provider 会丢掉之前的推理上下文（Responses 的加密推理、Claude 的 thinking 签名），且不可逆。移到文件同样把数据库变小，又不丢数据，所以不采用；如果以后会话文件也嫌大，再单独设计“按保留期清理会话文件里的推理”。

## 关键流程

见“assistant 桩”“上下文组装只取回当前分段”“维护命令”。

## 架构

- `internal/personal/store/toolbody.go`：桩生成按条目类型分支（tool / assistant），读取路径不变。
- `internal/personal/store/entries.go`：`EntryFilter.HydrateFromSeq`。
- `internal/personal/context.go`：按 `first_kept_seq` 限定取回。
- `internal/personal/store/toolbody_maint.go`、`internal/cli/data_toolbody.go`：维护命令扩展。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 复用 tool-bodies 的整条移出机制，而不是逐字段外置 | 读取、校验、备份、还原都已经有了；逐字段外置要改 api 编码。 |
| D2 | assistant 桩保留完整 Content | FTS 只索引 Content，且总量只有 2.6MB。 |
| D3 | 上下文组装只取回当前分段 | 压缩点之前的条目不会被发送，读文件是纯浪费；对 tool 正文同样受益。 |
| D4 | 不丢弃推理内容 | 移到文件不丢数据，可还原。 |

## 安全、数据、运维与回滚

- 回滚：`suga data tool-bodies --undo`（扩展后同时还原 assistant 条目），再回退版本；顺序与 v0.36 相同。
- 预计效果（以用户库为例）：assistant 条目 114MB → 约 8MB（Content 2.6MB + 桩字段），配合桩瘦身（另一任务），数据库预计降到 150MB 以下；会话文件增加约 105MB。

## 测试与验证

- 新写入：assistant 条目超过阈值时移出、桩字段（完整 Content、ToolCall 只有 CallID/Name）、FTS 行内容与移出前一致、回滚。
- 取回：`ListEntries` 默认逐字节相同；`HydrateFromSeq` 下界之前返回桩、之后返回正文。
- 上下文：同一会话在移出前后、压缩前后，组装出的请求逐字节相同（前缀哈希不变）；长会话每轮读文件字节数只与当前分段相关（可观测）。
- 交接与压缩摘要：移出前后生成的摘要输入相同。
- 维护命令：assistant 的 `--move/--undo/--check/--status`、断点续跑、幂等。
- FTS：`shrink-fts`、回填测试在 assistant 桩存在时结果不变。
- 真实库副本：`--status`、`--move`、`vacuum` 的耗时与前后大小，抽查导出、trace、恢复一个会话继续对话（真实模型，一轮）。
- 反向验证：桩里去掉 Content 后 FTS 用例失败；去掉 `HydrateFromSeq` 判断后“压缩点前不读文件”用例失败。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 采用“整条移出 + 桩保留完整 Content”而不是丢弃推理内容？ | 是 |
| Q2 | 命令与配置名：沿用 `suga data tool-bodies` 与 `store.tool_body_inline_bytes`，还是改名 `suga data bodies` / `store.body_inline_bytes`（旧名保留为别名）？ | 改名并保留旧名别名（名字更准确） |
| Q3 | 会话文件名：沿用 `tools.jsonl` 还是改名 `bodies.jsonl`？ | 沿用 `tools.jsonl`（已发布、备份与检查都认它；改名需迁移，收益小） |
| Q4 | 上下文组装只取回当前分段（同时惠及 tool 正文）？ | 是 |
| Q5 | 阈值沿用 1,024 字节？ | 是 |
| Q6 | 实施前独立审核？ | 是 |

## 结论与人工计划 Gate

用 v0.36 已有的整条移出机制处理 assistant 条目的推理内容与大工具调用参数，桩保留完整 Content 以保证检索不变，并让上下文组装只取回当前分段。用户批准 Q1–Q6 后先做独立审核，再派出实施；批准前不改代码。
