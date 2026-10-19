<!-- template_id: design; template_version: 1.1.1 -->
# tool 结果正文移到会话文件设计

> 状态：Draft 0.3 / 已批准，按独立审核修订后可实施（“0.3 修订”优先于后文）
> 日期：2026-10-19
> 关联：数据治理设计 `docs/design/2026-10-19-data-governance-design.md`（“B 路线”的收窄版本）、issue `suga-zy9f`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-19 | 监督（Claude） | 用户 2026-10-19：“tool 输出基本不是那么重要，搜索主要看 AI 和用户的消息，这部分能否移到 jsonl 文件，需要时才去看”，并要求先出设计。只把 tool 结果正文移出数据库，读取在 store 层透明取回；用户/AI 消息、元数据、events、FTS 仍在库里。 |
| 0.2 | 2026-10-19 | 监督（Claude） | 用户批准（“B 的设计按推荐来”）：Q1 阈值 1,024 字节、Q2 命令 `suga data tool-bodies`、Q3 新写入立即生效、Q4 校验失败报错、Q5 不压缩、Q6 实施前独立审核。 |
| 0.3 | 2026-10-19 | 监督（Claude） | 按独立审核（独立子 agent，结论“修改后可实施”；codex 审核两次因模型连接中断未产出）修订：--move 改在写事务内；restore 必须安装 tools.jsonl、备份拷贝按记录长度并边拷边算 hash；桩不带 P2 标记且 P2 两个改写跳过桩；用 entries.body_ref 列识别桩；原始字节写文件；失败截断与目录 fsync；--remove-files 竞态；Store 需要知道 sessions 目录；A、P2 为硬前置（均已合并）。 |
| 0.4 | 2026-10-20 | 监督（Claude） | 用户 2026-10-20 批准“桩瘦身”：真实库 3.3 万个桩平均 1.5KB（设计预期一两百字节），因为桩保留了完整 `ToolCall`（含参数）、`Reasoning`，且预览存了两份。0.4 起桩只留 `Role`、`ToolCall{CallID,Name}`、`ToolResult{CallID,Name,IsError}` 与一份 `Content` 前 120 字预览；读取路径始终取回文件里的完整 JSON，桩只供 SQL 取 CallID。新增 `suga data tool-bodies --slim` 改写旧桩。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 0.3 修订：独立审核后的定稿要点（本节优先于后文）

前置：A（tool 不进 FTS，`a0647875`）与 P2（写存储去重，`4947dcab`）已合入 main。A 是硬前置：否则 FTS 回填/维护会读到桩里的预览。

### 写入与迁移都在写事务内（阻断 B1、S6）

- 新写入：保持 0.2 的“插入行 → 追加并 Sync → 改桩 → 提交”。同一事务里同一会话的多条 tool 只打开文件一次、Sync 一次。
- `--move` 也必须在写事务内：每批 `BEGIN IMMEDIATE` → 读这一批行 → 按会话追加并 Sync → 改桩 → 写断点 → 提交；批次小（≤100 行或 ≤8MB），控制持锁时间。这样“写前偏移”在锁内计算，和在线写入不会交错；同一行不会被追加两次（已是桩的行跳过）。
- 网络盘不支持（SQLite WAL 本来就要求本地文件系统），在文档里写明。

### 文件格式与校验（S3、S4）

- 每行记录：`{"seq":N,"sha256":"…","json":` + **条目 JSON 的原始字节** + `}\n`。`BodyRef.off/len` 指向原始字节那一段，`sha256` 对这段原始字节计算；取回时 `ReadAt` 直接得到原始字节，逐字节相同不依赖再编码。
- 追加写失败：`Truncate` 回写前长度，再回滚事务。Linux 上首次创建文件后 fsync 所在目录。

### 桩的识别：新增列，而不是嗅探 JSON（建议采纳）

- 迁移：`ALTER TABLE entries ADD COLUMN body_ref TEXT`（O(1)，不是全表工作），桩行 `body_ref` 非空（存 `off,len,sha256`），`json` 存短桩（不含 `BodyRef` 键）。
- 读取路径只看 `body_ref` 列；`--status`、GC 估算、`--check` 都可直接用 SQL；`Types` 不含 tool 的读取零文件 IO。

### 与 P2 的交互（阻断 B4）

- 桩不带 `ResultSameAsContent` 标记。
- P2 的 `dedupe-tool-results` 与 `--undo` 都跳过 `body_ref` 非空的行（补测试）。
- 回滚顺序：先 `suga data tool-bodies --undo`，再 `suga data dedupe-tool-results --undo`，再回退版本（CHANGELOG 写明）。

### 备份与恢复（阻断 B2、B3）

- `restore` 必须安装 `sessions/*/tools.jsonl`（`internal/personal/backup/backup.go:387` 的 `replaceData` 目前只装 db、config、memory、projects）。
- 不变量：备份先做 DB 快照，后采集文件（`backup.go:107` 与 `:145` 起），所以文件是快照的超集，快照里的桩都有效。写进设计与代码注释。
- 归档拷贝按 manifest 记录的 Size 用 `LimitReader`，边拷边算 hash，避免两步之间文件被追加导致 hash 不符（`addExternal` `:471` 与 `writeArchive` `:178`）。
- `treeBytes(sessions)`（`:162`）不把 `tools.jsonl` 算作“省略的 spill”。
- 恢复后自动跑一次 `--check`，报告结果。

### `--undo --remove-files` 的竞态（S1）

- 用库内持久开关（例如 meta 表里一行）控制“新写入是否移出”，在写事务里读取；`--undo` 先关开关再还原。
- 删除某会话文件前，在写锁内确认该会话已没有 `body_ref` 行。
- `--check` 只报告孤儿记录，不清理；清理需要重写文件（改变偏移），必须在写锁内连同桩一起更新，本版不做。

### Store 需要知道 sessions 目录（S2）

- `store.Open` 增加可选参数（会话文件根目录）；未传时从库路径推导 `<db 所在目录>/../sessions`，并保证 restore 的 staging 库与 `inspect trace` 的 `OpenReadOnly` 下也成立。
- `sessionDir` 不接受的会话 id（`internal/cli/data_gc.go:136` 的校验）不移出。

### 与代码事实的更正（S5）

- 删除会话目录在 `internal/cli/data_gc.go:173-184`，不在 store 的 `gc*.go`；`handoff.go:92`、`runner.go:483` 调 `DeleteSession` 不删目录，会留下孤儿文件——实施时让这两处也清理 `tools.jsonl`（或交给 `--check` 报告）。
- 实际读取点约 15 处，不是 20 处。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：tool 结果正文（数据库里最大的一块）不再存进 SQLite，改存每个会话一个追加式文件；所有读取方读到的消息与现在逐字节相同；可迁移、可还原、可修复。
- allowed_scope：`internal/personal/store`（写入、读取取回、维护命令的 store 部分）、`internal/cli` 的 `data` 子命令、`internal/personal/backup`（备份必须带上这些文件）、GC 的会话目录删除确认、测试与文档。
- non_goals：不移动 user/assistant/compaction/handoff 正文（它们是检索与回放的主体，且体积小）；不改 FTS（A 已让 tool 不进索引）；不改 events；不做压缩（gzip 会破坏随机偏移，留到后续）；不改 provider 请求与前缀缓存；不做启动期迁移。
- expansion_policy：DEFER_OR_REQUEST。若需要改变消息结构、读取接口签名的语义，或需要启动期全表工作，停止并回到 Gate。
- review budget：中到大；涉及写入一致性、所有读取路径与迁移，实施前需一次独立审核。
- 停止条件：用户批准前不改代码。

## 背景与目标

只读实测（2026-10-19）：tool 消息 44,411 条，entries JSON 305.5MB，其中 124MB 是 `Content` 与 `ToolResult.Content` 的重复（P2 正在去掉）；去重后 tool 正文仍约 180MB，是数据库里最大的一块。A（全文索引不再收录 tool 输出）完成后，tool 正文在数据库里只剩“存着、偶尔读”的用途：恢复会话时发给模型、`recall`、`export`、`trace`。

目标：

1. tool 正文超过阈值的部分存到 `sessions/<sid>/tools.jsonl`，数据库只留小桩（元数据 + 短预览 + 文件位置）；
2. `ListEntries`/`LatestEntry` 返回的条目 JSON 与现在**逐字节相同**（在 store 层取回），上层 20 个读取点不改；
3. 写入崩溃安全、多进程安全；
4. 历史数据可分批迁移、可 `--undo` 还原、可 `--check` 校验；
5. 备份/恢复、GC 删会话把文件一起处理。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| 写入在 `AppendEntries` 的一个写事务里完成，`seq` 在事务内由 `MAX(seq)+1` 算出 | `internal/personal/store/entries.go:15-60` | 文件记录要带 seq，只能在插入行之后、提交之前写；写事务持有数据库写锁，可兼作“同一会话文件追加”的串行化（同一台机器）。 |
| store 层读取正文的入口只有 `ListEntries`、`LatestEntry`；`ListEntryRefs` 用 SQL 从 JSON 取 `ToolCall.CallID`/`ToolResult.CallID` | `entries.go:80-160`、`:120` | 取回只需在这两个入口做；桩里必须保留 `ToolResult.CallID`、`Name`、`IsError`，`ListEntryRefs` 不变。 |
| 其它包没有直接用 SQL 读 entries 正文；`gc_retention.go:146` 用 `length(json)` 估算可回收字节 | grep 结果 | 估算改为“行长 + 文件里对应记录长度”或接受低估，在报告里注明。 |
| P2 引入 `store.EncodeMessage` 与 `api.Message.UnmarshalJSON`（去重标记还原） | 数据治理设计 0.4 | 文件里存的就是 `EncodeMessage` 的输出；取回后的字节交给现有解码，P2 的还原照常生效。 |
| 三个 provider 都只读 `ToolResult.Content` | `pkg/agentcore/llm/claude.go:231`、`openai.go:162`、`responses.go:180` | 旧版本读到“桩”会把工具结果发成空，回退前必须 `--undo`（与 P2 相同的约束）。 |
| 会话目录在 GC 删除会话时整体删除；P3 的备份默认不含 spill | `internal/personal/store/gc*.go`、`internal/personal/backup` | `tools.jsonl` 是正文的唯一来源，备份必须**始终**包含，不能跟 spill 一样默认排除。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

### 存储形态

- **文件**：`$SUGA_HOME/sessions/<sid>/tools.jsonl`，追加写，一行一条：`{"seq":<n>,"sha256":"<hex>","json":<条目原 JSON>}`。
- **数据库桩**：只对 `type='tool'` 且条目 JSON 超过阈值（默认 1,024 字节，配置 `store.tool_body_inline_bytes`）的条目移出；桩是合法的消息 JSON，只保留 SQL 与人工识别用得上的字段（0.4）：`Role`、`ToolCall{CallID,Name}`、`ToolResult{CallID,Name,IsError}` 和 `Content` 前 120 字预览；文件位置在 `entries.body_ref` 列（`off,len,sha256`），不在 JSON 里。
- 预览让桩在“文件丢失”这种异常下仍可读（看得出是什么工具、结果开头是什么），但读取路径**不会**把桩当正文用（见“取回”）。

### 写入（崩溃与并发）

在 `AppendEntries` 的同一个写事务内：

1. 插入行（正文先按原样写入，与现在相同），得到 `seq`；
2. 若该条目要移出：以追加模式打开 `tools.jsonl`，写入一行记录，`Sync`，记下写入前的偏移与长度，关闭；
3. `UPDATE entries SET json = <桩> WHERE session_id=? AND seq=?`；
4. 提交。

- 第 2 步失败：整个事务回滚，返回错误（不出现“库里是桩、文件里没有”）。
- 第 2 步成功、提交前崩溃：文件里多了一条没有对应行的记录（孤儿），无害；`--check` 会报告并可清理。之后同一 seq 被重新分配时，新行指向新的偏移，读取按偏移 + seq + sha256 校验，孤儿记录不会被误读。
- 多进程：写事务（`BEGIN IMMEDIATE`）持有数据库写锁，同一库的两个进程不会同时追加；不依赖文件锁。
- Windows：追加模式打开、写完即关，不长期持有句柄；`Sync` 用 `FlushFileBuffers`（Go 的 `File.Sync`）。

### 取回（读取路径不变）

- `ListEntries`、`LatestEntry` 扫描结果时，对带 `BodyRef` 的行：按会话打开一次 `tools.jsonl`，`ReadAt(off, len)`，校验记录的 `seq` 与 `sha256`，把条目 JSON 替换为记录里的原 JSON。
- 校验失败或文件缺失：返回带会话、seq 的明确错误（`ErrToolBodyMissing`），**不**静默用预览顶替；`suga data tool-bodies --check` 给出修复路径（从备份恢复或 `--undo` 不可行时的说明）。
- 性能：恢复会话按 seq 顺序读，偏移单调递增，近似顺序读；每次调用每个会话只开一次文件。

### 迁移、还原、校验（显式维护命令）

`suga data tool-bodies [--status | --move | --undo | --check] [--batch N]`（命令名待确认，见 Q2）：

- `--move`：按 `(session_id, seq)` 游标分批 500 行：先把这批要移出的正文追加到各自会话文件并 `Sync`，再在一个短写事务里把这批行改成桩；断点可续；不进启动迁移。
- `--undo`：分批把桩还原为文件里的原 JSON（覆盖所有带 `BodyRef` 的行，含新写入的），完成后可删除文件（`--undo --remove-files`）。
- `--check`：逐行校验 `BodyRef` 指向的记录存在且 seq/sha256 一致；报告孤儿记录与缺失记录。
- `--status`：待移出的行数与字节、已移出的行数、文件总大小。
- `--slim`（0.4）：按 `(session_id, seq)` 游标分批把旧形状的桩改写成新形状（只改库内行，不动会话文件与 `body_ref`），断点可续、重跑幂等；`--status` 同时报告仍是旧形状的桩数量与预计回收字节。
- 完成后提示 `suga data vacuum`。

### 配套改动

- **备份**：`suga data backup` 始终包含 `sessions/*/tools.jsonl`（与 `--include-spill` 无关）；manifest 记一类 `tool_bodies`。restore 校验与其它文件相同。
- **GC**：删除会话时会话目录整体删除（现状），确认 `tools.jsonl` 在目录内；保留期报告的可回收字节加上文件大小。
- **export / trace / recall**：经 store 取回后与现在相同，无需改动；测试覆盖。
- **配置**：`store.tool_body_inline_bytes`（默认 1024；0 = 不移出，相当于关闭本功能）。

## 关键流程

见“写入”“取回”“迁移、还原、校验”。

## 架构

- 新文件 `internal/personal/store/toolbody.go`：桩的生成与识别、文件追加、`ReadAt` 取回与校验。
- `entries.go`：`AppendEntries` 写入步骤 2–3；`ListEntries`/`LatestEntry` 调用取回。
- `internal/personal/store/toolbody_maint.go`：`--move/--undo/--check/--status` 的分批实现（复用 tx.go 与 P1 的断点表做法）。
- `internal/cli`：`data tool-bodies` 子命令。
- `internal/personal/backup`：新增文件类别。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 只移 tool 正文，且只移超过阈值的 | tool 正文是最大的一块、读取频率低；小条目留在库里避免大量小文件读。 |
| D2 | 在 store 层透明取回，返回逐字节相同的 JSON | 20 个读取点不改；回放给模型的请求不变。 |
| D3 | 文件写入放在写事务内、提交前，失败即回滚 | 不会出现“桩指向不存在的正文”；孤儿记录无害且可检测。 |
| D4 | 读取校验失败报错，不用预览顶替 | 静默顶替会让模型看到被截断的历史却不自知。 |
| D5 | 不做压缩 | gzip 破坏随机偏移；文件本身可在备份时压缩。 |
| D6 | 备份始终包含 tool 正文文件 | 它们是唯一正文来源。 |

## 安全、数据、运维与回滚

- 回滚：`suga data tool-bodies --undo` 后再回退版本（CHANGELOG 写明）；旧版本读到桩时工具结果为空（provider 只读 `ToolResult.Content`）。
- 文件权限沿用会话目录（0700 / 受保护 DACL）。
- 预计大小（以 P2 + A 完成为前提）：数据库约 250–300MB（VACUUM 后），`tools.jsonl` 合计约 180MB。

## 测试与验证

- 写入：阈值上下、事务内文件写失败回滚、提交前崩溃留下孤儿并被 `--check` 报告、两个连接并发追加同一会话（`-race`）。
- 取回：`ListEntries`/`LatestEntry` 对含桩的会话返回与移出前逐字节相同的 JSON；文件缺失与 sha256 不符返回 `ErrToolBodyMissing`。
- 读取路径回归：恢复会话（LoadMessages）、交接、压缩、recall、session_search（只回到条目时）、export、trace 在移出前后结果一致。
- 维护命令：分批、中断续跑、`--undo` 后与原 JSON 逐字节相同、`--check`。
- 备份/恢复：包含 `tools.jsonl`，恢复后可读。
- 真实库副本：`--status`、`--move`、`vacuum` 的耗时与前后大小，抽查读取，`--undo` 还原，副本用完删除。
- 反向验证：去掉取回后回归用例失败；去掉 sha256 校验后“记录被篡改”用例失败；去掉事务内回滚后“文件写失败”用例失败。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 移出阈值 | 1,024 字节（条目 JSON）；配置可调，0 关闭 |
| Q2 | 维护命令名 | `suga data tool-bodies --move/--undo/--check/--status` |
| Q3 | 新写入是否默认立即移出 | 是（阈值 > 0 即生效）；历史数据由用户自己执行 `--move` |
| Q4 | 读取校验失败时 | 报错，不用预览顶替 |
| Q5 | 文件是否压缩 | 本版不压缩 |
| Q6 | 实施前是否独立审核 | 是（涉及写入一致性与所有读取路径） |

## 结论与人工计划 Gate

只移 tool 正文、store 层透明取回、事务内写文件、显式迁移与还原。用户批准 Q1–Q6 后，先做一次独立审核，再派出实施；批准前不改代码。依赖：P2（`EncodeMessage`/`UnmarshalJSON`）与 A（tool 不进 FTS）先合并。
