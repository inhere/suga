<!-- template_id: design; template_version: 1.1.1 -->
# 数据治理设计：备份恢复、GC 与输出配额

> 状态：Draft 0.3 / 已批准方向，按独立审核修订（P1 改为原地分批截断；本节“0.3 修订”优先于 0.2 与 0.1）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.0 | 2026-10-19 | suga（gofer job） | 建立数据治理问题域、现状盘点和 A/B 路线比较。 |
| 0.1 | 2026-10-19 | suga（gofer job） | 针对约 1.09GB 会话库，设计在线备份/恢复、保守 GC、spill 配额与压缩，并比较 DB 内瘦身和按会话 JSONL 真相源两条路线。 |
| 0.2 | 2026-10-19 | 监督（Claude） | 用 `entries_fts` 按 role 的实测更正 0.1 的待核对项：FTS 约 500MB 里 95% 来自 **tool 输出**（42,735 行、120MB 文本），不是 user/assistant；据此把“FTS 只索引 tool 输出开头”定为第一阶段（预计回收约 350MB，改动最小、风险最低），tool 内联阈值为第二阶段，backup/GC/spill 为第三阶段，B 路线暂缓并给出重启条件；新增按会话来源（gofer job）设保留期的问题。 |
| 0.3 | 2026-10-19 | 监督（Claude） | 用户批准方向（P1→P2→P3、B 暂缓、非交互 30 天/交互 180 天、Q1–Q8 按倾向）。按独立审核（监督派出的独立子 agent；codex 审核因模型容量不足中断后重试中）修订：P1 由“影子表+换表”改为“FTS 原地按 rowid 分批截断 + merge”（避开触发器改名与并发写丢失）；截断改为头 512 + 尾 512 字符；改漂移测试契约；GC 会话删除的 O(会话数×FTS 行数) 问题与自动 VACUUM 纳入 P3；P2 改为先评估 tool 消息双份存储去重；会话来源用新写入的 meta.origin，历史一律按交互。 |

### 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：在不阻塞正常会话、不误删用户记忆、可回滚的前提下，控制 SQLite、FTS 派生数据和会话输出的长期增长；同时给出从 DB 内容日志迁移为按会话文件日志的可实施路线。
- allowed_scope：`internal/personal/store`、`internal/personal` 的 spill/ledger、`internal/cli` 的 `backup`/`restore`/`gc`/`export`、`internal/config` 的治理配置、相应文档与隔离测试。
- non_goals：本批不改代码、不直接改线上用户库、不删除现有 FTS、不改变 provider 协议、不把 `.env` 密钥纳入备份、不把 codex 的 1.38GB 投影缓存模式照搬进 suga。
- expansion_policy：若要改变会话写入语义、恢复时覆盖正在运行实例、或把配置/密钥备份格式改成跨产品兼容协议，先回到用户确认 Gate。
- review budget：中。优先交付可逆的治理命令；文件为真相源是后续分阶段迁移，不作为本批隐式实现。
- stop condition：用户批准本设计和计划前，不添加命令、配置、迁移或数据重写代码。

## 0.3 修订：独立审核后的定稿要点（本节优先于后文）

独立审核结论“修改后可实施”。已核对的事实与据此的修改：

### P1 改为原地分批截断（替代 0.2 的影子表 + 换表）

- **原因**：触发器 `entries_fts_session_delete`（`migrations/20261010-234359-add-ledger-fts-memory-meta.sql:23-26`）正文写死表名 `entries_fts`，新版 SQLite 默认 `legacy_alter_table=OFF`，换表改名会让触发器跟着指向旧表，DROP 后删会话就报错；DROP 约 500MB 的 FTS 表还会长时间占写锁；影子表重建期间并发写入的新条目和被删会话会漏/留孤儿。
- **做法**：
  1. 新写入：`ftsTextExpr` 的 tool 分支改为“trim 之后取头 512 + 尾 512 字符”（中间用 ` … ` 连接；长度 ≤1024 字符时原样）。SQLite 的 `substr`/`length` 对 TEXT 按字符计，不会切坏 UTF-8；CJK 输出最多约 3KB。
  2. 历史数据：显式维护命令按 **FTS 自己的 rowid** 分批（每批 2,000 行，短事务）执行 `UPDATE entries_fts SET text = <同一截断表达式> WHERE rowid > ? AND rowid <= ? AND role = 'tool' AND length(text) > 1024`；断点只记 FTS rowid（不用 entries 的隐式 rowid，VACUUM 会重编号）；结束后按批 `INSERT INTO entries_fts(entries_fts, rank) VALUES('merge', N)` 回收段空间。与并发写入共存（新写入本来就已截断），不需要换表、不动触发器。
  3. 命令带 `--status`（已处理/剩余/预计回收）与 `--abort`；可 Ctrl-C，重跑从断点续。
  4. **不新增回填迁移**；把 `TestFTSExpressionsMatchMigration` 的契约改为“维护命令与 `AppendEntries` 共用 `ftsSourceSelect`/同一截断表达式”，旧迁移文件不动（`fts.go:25-28` 的注释同步改写）。
- **检索影响如实写进 `session_search` 结果说明**：tool 输出只索引开头与结尾各 512 字符；1KB–8KB 的输出（平均约 2.8KB，占多数）没有 `out:` 引用（spill 阈值 8000 字节，`pkg/agentcore/tools/output.go:19`），截断后其中段不可检索——这是 P1 的明确代价。命令参数从未进 FTS（只索引结果），0.2 里“开头是命令”的说法删除。
- 回收量以截断前后的长度直方图实测为准（审核估计索引降到约 150MB 量级，而非 0.2 写的约 100MB）。

### VACUUM（P1 之后由用户显式执行）

执行前检查剩余空间（WAL 模式下峰值可能接近库大小的 2 倍）、持有 `run/maintenance.lock` 且无其他 suga 进程（chat/ACP/run）在用这个库，否则拒绝；结束后 `PRAGMA wal_checkpoint(TRUNCATE)`。Windows 上失败（句柄占用、杀毒）时保持原库不动并提示重试。

### P3 新增：GC 删会话本身会卡

- 现状：`gc.go:85-93` 在一个事务里删掉所有过期会话，每删一个会话触发器都会对 FTS 的 UNINDEXED `session_id` 全表扫一遍（O(会话数 × FTS 行数)），事务结束后还**自动 VACUUM**。按 30 天保留期一次删几百个会话会重演 v0.31.0 式卡死。
- 修改：会话分小批删除；FTS 行改为一次扫描收集 rowid 后按 rowid 删除（批量删会话时临时绕过逐会话触发器，或改触发器为按 rowid 集合）；`gc --apply` 不再自动 VACUUM，改为显式 `--vacuum`。

### P2 重新定义

- 0.2 的“超 4KB 只留头尾 + spill”等于把 `DefaultInlineOutputBytes` 从 8000 调到 4096，会改变模型可见上下文，不只是存储问题，暂不做。
- 先评估收益更大、对模型无影响的一项：tool 消息 JSON 里 `Content` 与 `ToolResult.Content` 存了同一份结果（`fts.go:40-45` 注释、`api/types.go`），新写入只存一份、读取时补齐；历史数据是否改写另议（改写会让恢复的会话回放不同字节、使 provider 缓存失效）。另：`spill.go` 写文件后未 fsync，P2/P3 动 spill 前补上。

### 会话来源（保留期 30/180 天）

现有数据无法区分来源（`meta_json` 只有 kind/parent、provider/model、版本、segment；id 形如 `sess_日期_…`）。新会话在入口（chat / run / ACP）写 `meta.origin`；**历史会话一律按交互处理（180 天）**；被 chat 接着用过的非交互会话按交互处理。

## 0.2 修订：更正实测与新的优先级（本节优先于后文 0.1 的路线排序）

### 实测更正（2026-10-19，只读统计用户主机 `~/.suga/db/sessions.db`）

`SELECT role, COUNT(*), SUM(LENGTH(text)) FROM entries_fts GROUP BY role`：

| role | 行数 | 文本 |
|---|---:|---:|
| tool | **42,735** | **120.6MB** |
| user | 975 | 2.5MB |
| assistant | 2,314 | 2.3MB（43,516 条 assistant 里只有带正文的进索引） |
| compaction | 3 | 0.03MB |

即代码里的 `ftsIndexedTypes` 与库一致：**tool 输出是 FTS 的绝对主体**。trigram 索引（387MB）与内容副本（约 125MB）合计约 500MB，其中约 96% 来自 tool 输出全文。0.1 中“FTS 只索引 user/assistant”的表述（来自监督早先的口径）是错的，以本表为准。

### 新的阶段顺序

| 阶段 | 内容 | 预计回收（本机库） | 风险 |
|---|---|---|---|
| **P1 FTS 瘦身（先做）** | tool 输出只把**开头 1KB**（命令/首行结果，按 UTF-8 边界截断）写入 FTS 文本；user/assistant/compaction/handoff 不变。新写入立即生效；历史数据用显式维护命令分批重建 FTS（影子表 + 每批 2,000 行 + 断点，可中断），**不进启动迁移**；重建后提示用户执行 `VACUUM` 才能真正缩小文件。 | 约 **350MB**（索引 387MB→约 100MB、内容副本 125MB→约 45MB 的量级，以 benchmark 为准） | 低：派生表可重建；`session_search` 对 tool 输出深处的文本不再命中（可用 `recall` 的 `out:` ref 或 `file_grep` 补）。 |
| P2 tool 内联阈值 | 新写入的 tool 结果超过 4KB 时 DB 只留头尾预览 + spill 引用；历史数据按需显式迁移（先写 spill 并 fsync 再改 DB） | 约 100–180MB | 中：涉及读路径与 spill 可靠性 |
| P3 backup/restore、GC 扩展、spill 配额与 gzip | 即 0.1 的 §备份格式、§GC、§Spill | 视 TTL 而定 | 中：删除类操作，默认 dry-run |
| B 文件为真相 | **暂缓** | — | 高 |

P1+P2 后本机库预计从 1.09GB 降到约 0.55–0.65GB（VACUUM 后），之后的增长速度约为现在的一半。B 路线（会话正文改为 JSONL）复杂度高（写入顺序、跨进程锁、所有读路径、1GB 迁移），在 P1/P2 之后收益不足以覆盖风险，暂缓；重启条件：P1/P2 后库仍 >1GB 且增长主要来自正文，或出现多进程同时写同一会话的真实需求。

### 增长来源与保留期

日增量峰值来自 gofer 派给 suga 的实施/评测 job（它们使用真实 `~/.suga`）。`sessions` 表没有来源列，但 gofer 拉起的会话可从 `meta_json`/会话 id 形态或 ACP/CLI 入口区分（实施前核对）。新增问题 Q9：是否给“非交互（gofer job / `suga run`）会话”单独的、更短的保留期（例如 30 天），交互 chat 会话保留更久（例如 180 天）。

## 背景与目标

### 现有实现和用户实测

当前个人会话库是 `$SUGA_HOME/db/sessions.db`，SQLite WAL，`internal/personal/store` 在打开时按文件名顺序执行迁移。`sessions`、`turns`、`entries`、`events` 是主表；`entries_fts` 和 `memory_meta` 是派生/治理侧表。`entries_fts` 通过 `(session_id, seq)` 定位条目，不能依赖 SQLite 隐藏 rowid；会话删除触发 FTS 删除。

现有命令：

- `suga export <session> [--with-outputs]` 从 DB 读取 entries/events，并从 `sessions/<id>/outputs/*.txt` 读取外置输出。
- `suga gc --older-than 30d [--apply]` 默认 dry-run，删除不活跃会话、其 entries/turns 和关联外置目录，之后 `VACUUM`；当前只处理 snapshots 和会话目录，不处理 memory 文件、`memory_meta`、WAL 或独立的旧 events TTL。
- spill 文件由 `internal/personal/spill.go` 写为 `sessions/<sid>/outputs/<16hex>.txt`，内容已做 secret redaction；`recall`/`session_search(ref=out:...)` 和 export 直接读 `.txt`，还没有配额、gzip 或压缩文件读取。
- memory files 后端以 `MEMORY.md` 为索引、每条记忆一个 Markdown 文件；用户记忆在 `$SUGA_HOME/memory/user`，项目记忆可以在仓库 `.suga/memory/` 或 `$SUGA_HOME/projects/<key>/memory`。SQLite 的 `memory_meta` 保存 provenance、fingerprint 和 tombstone，但不是记忆正文的真相源。

2026-10-19 的只读实测库为 **1.09GB**：4096 字节页 × 267,540 页，freelist 183 页。构成如下：

| 部分 | 实测大小 | 设计含义 |
|---|---:|---|
| `entries_fts_data`（trigram） | 387MB | FTS 索引本体是最大单项，不能用逐行 `NOT EXISTS` 清理；当前代码的 `ftsIndexedTypes` 还包含 `tool`，与统计口径需在实施前核对。 |
| `entries_fts_content` | 约 113MB | 非 contentless FTS 重复保存正文；适合改为 contentless/外部内容或缩小索引角色。 |
| entries 的 tool 结果 | 292MB | 42,000 条左右；大结果即使 spill，DB 仍保留部分内联正文。 |
| entries 的 assistant 等 | 约 113MB | 保留在 DB 内的正文和回放路径。 |
| events | 26MB | 适合按会话活跃时间分批 TTL；不应为报表每次全部拉入 Go 内存。 |
| 其他 B-tree/碎片 | 约 150MB | `VACUUM` 只能在明确维护窗口回收，不能随启动迁移执行。 |
| spill 文件 | 约 72MB | 可压缩；默认不因瞬时超额自动删除新输出。 |

日增量为 2026-10-14 55MB、10-16 145MB、10-17 94MB、10-18 44MB，峰值主要来自 Gofer 派发的实施/评测 job，而非 chat。治理必须把 FTS 重复内容和 tool 结果作为第一目标。

### 目标

1. `suga backup` 生成可校验的跨 Windows/Unix 归档：在线一致的 DB、memory 文件和不含密钥的配置；spill 是否纳入由显式策略决定。
2. `suga restore` 在实例停止、当前数据已自动备份、归档校验通过后才替换；失败可回到自动备份。
3. `suga gc` 扩展为 dry-run 优先的报告式治理：会话、事件 TTL、孤儿 `memory_meta`、明显的 memory 文件 orphan、WAL checkpoint、spill 年龄/配额和压缩均可见且按批执行。
4. spill 在目录达到上限时可预测：压缩旧文件、透明读取、按时间淘汰；不因 GC 短暂失败而破坏新会话。
5. 对 A（DB 内瘦身）与 B（按会话追加式 JSONL 为真相、DB 只做索引）做成本/风险比较，并推荐可中断分阶段路线。

## 范围

### 包含

- `backup`/`restore` 命令、归档 manifest、SHA-256 校验、SQLite 在线快照、配置脱敏、memory/spill 选择和恢复安全锁。
- GC 的候选发现、dry-run 字节估算、active session 保护、事件 TTL、派生 FTS 处理、memory/meta orphan、WAL checkpoint/truncate 和 spill 管理。
- 配置名称、默认值、CLI 覆盖关系、Windows/Unix 文件语义。
- 大库 benchmark、迁移分批、崩溃注入、反向验证和回滚步骤。

### 不包含

- 本文不授权直接修改 `entries` JSON、删除真实 `~/.suga` 数据或改变已有 migration 文件。
- 不默认清理所有未列在 `MEMORY.md` 的 Markdown；可疑文件先报告/进回收区，不能把手工文件当 orphan 直接删除。
- 不压缩仍可能写入的 task output，不在活跃 session 上做目录重命名。
- 不引入服务端、云备份、增量块存储或新的 provider/ACP 协议。

## 已确认事实

### 数据路径和读写路径

| 数据 | 当前路径/入口 | 约束 |
|---|---|---|
| DB | `$SUGA_HOME/db/sessions.db`，`store.Open` 设置 WAL、busy timeout 15s、`synchronous=NORMAL` | `Open` 会迁移；`OpenReadOnly` 不迁移不写入。 |
| memory | `$SUGA_HOME/memory/user`、项目 `.suga/memory` 或 `$SUGA_HOME/projects/<key>/memory` | Markdown/`MEMORY.md` 是 files 后端真相源；`memory_meta` 可重建/清理但 tombstone 有治理用途。 |
| spill | `$SUGA_HOME/sessions/<sid>/outputs/<hex>.txt` | ref 是 `out:<sid>:<hex>`，文件名不能包含模型给出的 call id；内容已脱敏。 |
| snapshots | `$SUGA_HOME/sessions/<sid>/snapshots` | 有现成的按 turn/day/global 上限和 `suga undo`，GC 应复用其 cleanup，不与 spill 混算。 |
| config | `$SUGA_HOME/config.yaml`，密钥通常在 `$SUGA_HOME/.env` | 备份 config 可含 `${VAR}` 引用，但永远不打包 `.env` 或展开后的 secret。 |
| active marker | `$SUGA_HOME/run`、`run/locks/<sid>.lock` | 现有 GC 已用进程登记和 session lock 判断活跃；backup/restore 也必须复用同一套锁。 |

### 当前 FTS 与迁移风险

`entries_fts` 是 `tokenize='trigram'` 的非 contentless FTS5。新 entry 在 `AppendEntries` 同事务写入 FTS，迁移回填历史数据。**严禁**新增逐 entry `NOT EXISTS` 对 FTS 虚表做存在性检查：FTS 的 `session_id/seq` 是 UNINDEXED，按行反查可能导致全表扫描，v0.31.0 已在大库上出现启动卡死。任何 FTS 改造应使用一次性 set-based rebuild 或显式 rowid/批次标记，且不得在普通 `Open` 上同步做 1GB 全表工作。

代码中的 `ftsIndexedTypes` 当前列出 `user, assistant, tool, compaction, handoff`；实测摘要写成“只索引 user/assistant/compaction/handoff”。这是统计口径与代码事实的待核对项，实施前必须用 `SELECT role, COUNT(*)` 和文件版本确认，不能据此直接承诺 tool FTS 的回收量。

## 总体方案

### 架构

- **数据 owner**：`store` 继续拥有 SQLite schema、事务和派生表约束；`personal/backup` 拥有归档格式与恢复 staging；`personal/spill` 拥有 ref、路径安全、`.txt`/`.gz` reader 和配额；CLI 只编排 flags、锁和报告。
- **维护 seam**：backup、GC、spill maintenance、FTS/tool shrink 和 JSONL migration 都是显式命令/任务，不进入 `store.Open` 的启动迁移。每个任务有 checkpoint、短批次、取消和旧数据保留期。
- **索引关系**：`sessions/turns/events` 是统计与状态；`entries_fts`、FTS shadow table、`memory_meta` 和 JSONL offset index 都是可重建投影。memory Markdown、尚未完成迁移的 entries JSON 和已 fsync 的 transcript 才是正文真相。

### 治理原则

1. **默认报告，不默认破坏。** `gc` 和压缩先 dry-run；新 spill 超额默认拒写/保留短结果，不在写入路径静默淘汰旧数据。
2. **派生数据可删，真相源先保全。** FTS、`memory_meta` 中无用 tombstone、WAL 和压缩缓存可以重建/恢复；memory Markdown、DB entries 和 spill 正文在删除前必须进入可验证的备份或回收区。
3. **在线快照不等于安全恢复。** backup 可以在线读；restore 必须维护窗口、进程锁、自动备份和 staged validation。
4. **大库动作不放在启动迁移。** 任何 FTS rebuild、tool 正文外移、JSONL 导出和 VACUUM 都是显式、可中断、可续的 maintenance job。
5. **所有删除先按 session 维度判活跃。** 运行登记、session lock、`status=running` 或当前 turn 未结束均保护；不能只看 `updated_at`。

## 关键流程

### 备份恢复、GC 与 spill 的运行顺序

### 备份格式（推荐）

默认命令：

```text
suga backup --output <file>.suga-backup.zip [--include-spill]
suga restore <file>.suga-backup.zip [--dry-run] [--no-auto-backup]
```

归档为标准 ZIP，便于 Windows/Unix 互通；每个文件条目使用相对 POSIX 路径，不能跟随 symlink 越出 SUGA_HOME。归档根包含：

```text
manifest.json                 # 格式版本、创建时间、来源版本、文件清单、sha256、spill 策略
 db/sessions.db               # VACUUM INTO 的一致快照，不带 -wal/-shm
 config.yaml                  # 原文，保留 ${ENV} 引用，不含 .env
 memory/user/**               # 用户 files memory（不存在则空目录/清单）
 memory/projects/**            # 实际使用的 files memory，路径映射写入 manifest
 sessions/<sid>/outputs/**    # 仅 --include-spill；也记录省略原因/总字节
```

配置文件、memory 和 spill 复制前逐文件读入 ZIP 并计算 hash；DB 用 SQLite 在线一致性备份：首选 modernc SQLite 可用的 backup API；若当前依赖不暴露该 API，使用临时同盘文件 `VACUUM INTO`，完成后 fsync/关闭再加入归档。`VACUUM INTO` 需要额外约 1.1GB 空间，遇到 busy 重试并报告，不复制 live `-wal/-shm`。backup 期间允许普通读写，但 DB snapshot 与文件目录不是同一时刻，manifest 记录开始/结束时间；需要严格跨目录一致时加 `--quiesce`，取得全局维护锁后短暂阻止新 turn。

推荐默认 **不纳入 spill**：spill 可能含大量敏感工具输出，且可以从 DB 的头尾/ref 发现“已省略”；需要完整离线恢复时显式 `--include-spill`。用户选择后，manifest 必须写 `spill: included|excluded`、文件数、总字节和遗漏说明；`config.yaml` 中不保存一次性命令选择。

restore 流程：

1. 读取 ZIP central directory，拒绝绝对路径、`..`、重复路径、超大解压比例和不认识的 manifest version；先校验所有 hash，再写 staging 目录。
2. 取得 `$SUGA_HOME/run/maintenance.lock`，检查 `run` 进程和 session locks；发现 chat/ACP/runner 活跃就失败并说明如何停止，绝不覆盖正在运行实例。Unix 也不能只依赖 rename（旧进程可能继续写旧 inode）；Windows 更不能删除被打开的 DB。
3. 除非 `--no-auto-backup`（仅用于恢复灾难场景且需显式确认），先运行一次完整 backup 到 `$SUGA_HOME/backups/pre-restore-<timestamp>.zip`，至少包含当前 DB、config、memory 和现有 spill。
4. staging 中先用 `OpenReadOnly`/SQLite integrity check 验证 DB，检查 `schema_migrations`、manifest 路径和 memory layout；再以同盘 rename 替换 DB/config/memory。Windows rename 失败时保持原数据不动。
5. spill 缺失时保留 DB 中的 ref，但 recall/export 返回“backup excluded”；不得伪造空正文。成功后 checkpoint，释放 lock，并打印恢复清单和自动备份位置。

## GC、事件 TTL 与派生表

### 命令和默认策略

保持现有 `--older-than` 语义，扩展为：

```text
suga gc --older-than 30d [--events-older-than 90d] [--apply]
        [--compress-spill] [--vacuum] [--checkpoint]
```

- 无 `--apply` 只产生报告；`--checkpoint` 仅允许 WAL checkpoint，不删除主表数据。
- `--older-than` 没有默认值，避免升级后首次运行误删会话。
- `--events-older-than` 缺省不启用；显式配置/flag 才裁剪事件。建议默认保留 180d，审批后才落配置。
- 活跃 session、`run` 登记、持有 lock、`status=running`、未结束 turn 的 session 一律跳过会话删除和 event TTL；GC 取得 lock 后再次复核，竞态则报告 `busy`。
- session 删除沿用 `sessions` cascade 和现有 FTS session trigger；不单独逐条清 entries_fts。FTS 只在删除后做按表统计/`optimize`（可选），不会每次对 FTS 做逐行存在性查询。

### 候选和报告

dry-run 报告至少包含：会话数/DB entries 数/事件数、memory meta orphan 数、memory 文件 orphan 数、spill 文件数与可压缩字节、待删字节、WAL 当前大小、checkpoint 结果和被保护 session 列表。大小分为“可确定”（`stat`、`PRAGMA page_count`、文件大小）与“估算”（压缩后、VACUUM 后），不把估算冒充已回收。

事件 TTL 按**会话最后活动**而非单个 event 时间判断：候选是 `sessions.updated_at < now - session_ttl` 且 `events.started_at < now - events_ttl`；这样一个旧事件不会导致仍在使用的 session 被裁剪。已删除 session 的 orphan events 单独按 `orphan_event_ttl` 处理，默认 30d；无 session 的事件不能被 session join 掩盖。q3jy 的 `events(kind)`、`events(turn_id)`、`events(session_id, started_at)` 组合索引和 SQL 聚合应先落地，GC 使用索引游标按 `id`/时间每批 1,000–5,000 行删除，提交后检查 context cancellation。

memory 规则：

- `memory_meta` 的 live row 若对应 scope 中不存在 entry，可报告为 orphan；files 后端以 Markdown 为真相，不能因为 meta 缺失删除文件。
- tombstone 只在 `deleted_at < now - memory_tombstone_ttl`（建议 180d）且没有仍需审计的 provenance 时删除；fingerprint index 删除应随批处理一起完成。
- entry file 未被 `MEMORY.md` 管理索引列出时，先按 `memory_orphan_grace=30d` 报告；只对合法 id、位于受控 layout、mtime 超龄且不是手工未知文件的候选做 `--apply`，优先移动到 `$SUGA_HOME/trash/`，不直接 unlink。恢复期内可从 trash 取回。
- `MEMORY.md` 中列出的 entry 即使没有 meta 也不能由 GC 删除；下次 memory 写入/索引同步补 provenance。

WAL/FTS/空间：

- `--checkpoint` 先执行 `PRAGMA wal_checkpoint(PASSIVE)`；无活跃 writer 且用户明确 `--apply --checkpoint` 时才尝试 `TRUNCATE`。busy 时保留 WAL 并报告，不循环等待。
- 主库删除在短事务中完成；`VACUUM` 改为显式 `--vacuum`，因为 1.09GB 上需要长时间独占写锁和近似同等临时空间。失败不影响已提交的分批删除。
- FTS 派生行随 session trigger 删除；tool 正文迁移或 FTS 重建不在 GC 中隐式执行。必要时单独 maintenance 命令/计划执行，避免把 GC 变成无法预测的启动操作。

## Spill 配额、压缩与透明读取

### 配置草案

配置 namespace 建议为 `tools.spill`，现有 `tools.max_output_bytes`/`tools.max_spill_bytes` 保持含义不变：

| 配置 | 默认 | 语义 |
|---|---:|---|
| `tools.spill.max_bytes` | `1GiB` | `$SUGA_HOME/sessions/**/outputs` 的逻辑字节上限；0 表示不设新配额但仍可手工 GC。 |
| `tools.spill.max_age` | `90d` | dry-run/GC 识别的可淘汰年龄；不在写入路径自动删。 |
| `tools.spill.compress_after` | `7d` | mtime 超龄且不活跃的 `.txt` 可 gzip。 |
| `tools.spill.compression` | `gzip` | `off` 或 `gzip`；gzip 使用标准库，读取无需外部程序。 |
| `tools.spill.on_quota` | `refuse` | 超配额时新 spill 返回可诊断错误，output layer 保留头尾；不自动删旧输出。 |
| `tools.spill.keep_days` | `0` | 0 表示不额外按天删除，仅 `max_age`/显式 `gc --apply` 生效。 |

配额按未压缩逻辑大小和物理压缩大小同时报告，防止压缩后“看似未超额”而恢复需要重新展开。推荐的写流程是：在 session lock 下写 `.tmp`、flush+close（Unix fsync；Windows FlushFileBuffers 尽可能执行）、rename 成 `.txt`；压缩时写同目录 `.gz.tmp`，校验原文 hash，关闭后 rename，并在最后删除 `.txt`。压缩不改变 `out:<sid>:<hex>` ref；manifest/读取器按 stem 查找 `.txt`，找不到再查 `.txt.gz`。

`Ledger.readOutputLines`、`session_search(ref=...)`、`export --with-outputs` 必须共用一个 `openOutput` 深模块：验证 ref 和 session root 后，透明识别 `.txt`/`.txt.gz`，禁止 symlink 越界；`.gz` 用 `gzip.NewReader` 流式读，不一次把 1GiB 全载入内存。`recall` 仍按行 offset/limit，搜索可按流扫描。正在写的 task output 只有 `.txt`，mtime 在 `compress_after` 内或持有 lock 时不压缩。

按时间淘汰应以文件 mtime/manifest 写入时间为主，不能用 DB event 时间推断；一个 session 被 GC 删除时先确认 lock，再删除 outputs 目录并在报告中计数。超过配额时，默认 `refuse` 保证新数据不悄悄牺牲旧数据；用户确认后 `gc --apply --compress-spill` 才按 oldest-first 删除超出部分，删除前报告每个 ref，失败留下剩余文件。

## 两条会话存储路线

### A：只在 DB 内瘦身（短期推荐）

保持 `entries` 作为正文真相源，分三步：

1. FTS 改为 contentless 或外部内容：索引只保留可搜索文本/映射列，命中后从 entries 读取正文生成 snippet；评估 `unicode61 + CJK 二元组` 或仅索引近 N 天，仍保留 compaction/handoff 和用户/assistant，tool 只保留短 preview 或不入 FTS。
2. 将 tool 结果 spill 阈值从 8KiB 降至 2–4KiB；超过阈值的历史 JSON 做一次可续迁移，DB 只留头尾 preview + ref，完整正文先写入受配额治理的 spill。新写入沿用 output layer 的 hash/ref，迁移前必须确保原文已成功落文件。
3. 对旧 entries 做分批正文清理、FTS rebuild，最后在维护窗口显式 VACUUM；不改变 entry seq、turn、event ref。

以当前实测量级估算（不是承诺）：FTS content 副本直接可回收约 **113MB**；缩小 tokenizer/角色后 FTS data 可能再回收 **100–250MB**，但 CJK 命中率和索引大小必须 benchmark。tool 结果改为 4KiB 左右头尾预览，对 42,000 条、292MB 原文预计回收 **100–180MB**；若只处理 >10KiB 的 140MB，保守回收约 70–120MB。合计现实目标约 **300–550MB**，需要额外 spill 空间和一次迁移。

A 的优点是改动集中、旧 DB 仍可回滚、所有 `ListEntries`/`LoadMessages`/`export` 路径基本不变；缺点是 DB 仍把正文和索引耦合，FTS 仍会重复部分文本，spill 失败会让迁移无法安全瘦身。A 是第一阶段推荐路线。

### B：按会话追加式 JSONL 为真相，DB 只存元数据和索引

目标布局：

```text
sessions/<sid>/ledger.jsonl[.gz]  # 每条 entry 一个带 seq/type/turn/time/body 的记录
sessions/<sid>/outputs/...        # tool 大正文旁路文件
DB sessions/turns/events          # 元数据、事件、状态
DB entries                        # seq/type/turn_id/offset/length/preview/hash，逐步去掉 json
FTS                             # contentless/外部索引，可从 ledger 重建
```

写入顺序和崩溃恢复：持有每 session 的 OS lock；先分配 seq 并追加完整 JSONL 记录，`fsync` 文件，再在 `BEGIN IMMEDIATE` 中写 entry 索引和更新 session，commit 后才向调用者报告成功。记录含 schema、seq、byte offset/length、CRC/hash；启动/显式 repair 扫描尾部 partial，截断不完整尾行；发现文件中 durable 但 DB 无索引的完整记录时按 seq 幂等补索引。DB 有索引但文件缺记录是 fail-closed（不能凭 preview 伪造正文），从最近 backup 恢复。跨进程不能只靠 SQLite busy timeout：复用 `run/locks/<sid>.lock`，Windows 用独占 handle/重试，Unix 用 flock/lockfile；不能允许两个进程同时 append 同一 session。

所有读路径的变化必须列为迁移验收清单：

- resume/`LoadMessages`：通过 DB offset 读取 JSONL；旧 session 在没有文件时回退 DB `json`。
- `--from`、compaction、handoff、segment replay：写文件记录，DB 只写定位索引；handoff/compaction 仍按 entry type。
- `session_search`/FTS：搜索文件重建的 contentless index，命中后按 offset 读；`recall` 直接读 JSONL 的消息窗口。
- `export`/`trace`：统一 transcript reader，输出格式不变；`--with-outputs` 继续走透明 spill reader。
- backup/restore/GC：文件和 DB 是一个 session 单位复制/删除；先文件后 index 删除，恢复按 manifest 校验。
- 压缩：只能在 session idle 且有索引 offset 重写表后做；压缩后 offset 需更新，或改为 block index，不能简单 gzip 后继续使用旧 offset。

B 的优点是大正文不再挤占 SQLite、DB/FTS 是可重建投影，符合 codex 的“文件真相”方向但避免其 1.38GB thread_items 投影；缺点是读路径、并发锁、恢复和迁移复杂，JSONL 压缩会破坏随机 offset，Windows rename/打开句柄更敏感。

当前 1.09GB 库的迁移不能在 `Open` 中做。先为新 session 采用文件写入；旧 session 按 500–1,000 entries 一批导出，持久化 `(session_id, next_seq, source_hash, target_offset)` checkpoint，可中断续跑。顺序读约 1.1GB：NVMe 粗估 1–5 分钟，普通 SSD 3–10 分钟，HDD/杀毒扫描可能 10–30 分钟；这是 I/O 粗估，必须用本机 benchmark 校准。文件写完成并 fsync 后才把 DB `json` 标成 migrated；失败保留旧 DB 正文。FTS rebuild 另行执行，按 1,000 行批次或整表重建，不逐行查 FTS。

### 推荐路线

推荐 **A 先行、B 分阶段评估**：先用备份/GC/spill 治理止血，再做 contentless FTS 和 tool threshold，最后在新 session 先写 JSONL、旧数据按需迁移。B 的设计保留为第二阶段架构，不把一次大迁移作为版本升级的启动副作用。用户若希望长期保留超大 job，会话文件路线的收益更大；若优先稳定和低回滚成本，A 足够把当前库压到约 0.55–0.8GB。

## 配置、迁移和 q3jy 关系

### 配置冻结候选

| key | 默认/状态 | 生效范围 |
|---|---|---|
| `backup.include_spill` | `false` | 仅作为交互提示默认；CLI `--include-spill` 优先，不把 secret 写入归档。 |
| `gc.events_ttl` | `off`（建议审批后 180d） | 事件仅在 session 不活跃且不持锁时按批删除。 |
| `gc.orphan_event_ttl` | `30d` | 删除已不存在 session 的旧 events。 |
| `gc.memory_tombstone_ttl` | `180d` | 清理旧 tombstone/meta orphan。 |
| `gc.memory_orphan_grace` | `30d` | 可疑 Markdown 先报告/回收区。 |
| `gc.vacuum` | `false` | 不在常规 GC 做长时间独占 VACUUM。 |
| `gc.checkpoint` | `passive` | 可选 `passive`/`truncate`，truncate 需维护锁。 |
| `tools.spill.*` | 见上表 | 配额、压缩和透明读取。 |

### 大库耗时和分批表

| 动作 | 1.09GB 库粗估 | 分批/中断策略 | 是否允许启动迁移 |
|---|---|---|---|
| `VACUUM INTO` backup | NVMe 1–5 分钟；慢盘 10–20 分钟，另需约 1.1GB 空闲 | 临时文件完成后才入归档；失败删除临时文件，源库不动 | 不允许阻塞 `Open`，仅 backup 命令 |
| FTS 重建/换 tokenizer | 约 500MB FTS、4.5 万级文本：SSD 1–5 分钟，慢盘 5–20 分钟 | 建 shadow table，1,000–5,000 行批次；manifest/checkpoint；原表最后切换 | 不允许 |
| tool 正文外移 | 292MB、约 4.2 万条：SSD 1–3 分钟，慢盘 5–15 分钟 | 每 500 条：spill fsync → DB batch；可重跑 hash，失败不删原文 | 不允许 |
| events TTL | 26MB | 每 1,000–5,000 行、短事务；按 q3jy 索引游标 | 可由显式 GC 执行 |
| memory/meta orphan | memory 通常 ≤数百条；meta 用索引分页 | 每 scope 扫描；文件 orphan grace；不全库 `NOT EXISTS` | 可由显式 GC 执行 |
| JSONL 旧数据迁移 | 读写约 1.09GB：1–5/3–10/10–30 分钟（NVMe/SSD/HDD 粗档） | 500–1,000 entries checkpoint，旧 DB 保留至校验通过 | 不允许 |

以上是容量级估算，不是验收数字；计划要求用 20,000+ entries 和可选 1GB fixture 实测，并记录设备、SQLite 版本、批大小、p50/p95。

### 与 `suga-q3jy` 的关系

q3jy 正在实施 `entries(type)`、`events(kind)`、`events(turn_id)` 等索引、SQL 聚合报表和 `BEGIN IMMEDIATE` + 忙重试。数据治理依赖这些工作：

- event TTL 的候选扫描和 dry-run 计数必须使用 q3jy 索引/聚合，不能回到把 26MB events 全部拉进 Go 内存。
- GC/backup/restore 的写事务采用同一有限重试和 busy 错误分类；maintenance lock 只解决 suga 进程协调，不能替代 SQLite 忙重试。
- 任何新 migration 与 q3jy 串行落地；不修改已在真实 1GB 库执行过的 migration 文件。新增 migration 不做 FTS 逐行存在性查询，且必须有 20,000+ entries 的耗时测试。
- 若 q3jy 尚未合并，治理计划可先实现只读报告接口，但不得假装报表已经 SQL 聚合；待 q3jy 的索引提交后再启用 apply。

## 安全、兼容和回滚

- 归档不包含 `.env`、环境变量展开值、provider headers、MCP secrets；ZIP 路径和 symlink 必须拒绝越界。
- backup/restore/GC 需要 `$SUGA_HOME/run/maintenance.lock`；恢复不能覆盖 live DB，Unix/Windows 都以进程/lock 二次检查为准。
- ZIP 先 hash 校验、后 staging、再自动备份和替换；任何校验/解压/rename 失败保持当前数据不动。
- 旧版本看到新 `.txt.gz` 时若不支持透明读取，只能报告 spill unavailable，不得把 gzip 字节当文本；因此压缩功能应与读取代码同一版本发布，或保留 `.txt` 直到兼容窗口结束。
- A 的回滚是恢复 pre-restore backup 或保留的旧 `entries.json`；FTS/data 都是可重建派生表，不能依赖 DOWN migration 抹掉真实正文。
- B 的回滚是停止新文件 writer，仍从旧 DB `json` 读取；未完成批次只删除新文件和 checkpoint，不改旧列。只有全量校验后才允许清理旧正文。
- GC 删除前只移动 memory orphan/spill 到受保护 trash 或先要求 backup；已提交的 DB 删除只能通过 restore 回滚，dry-run 必须明确显示这一点。

## 决策

| ID | 决策 | 理由 |
|---|---|---|
| D1 | backup 使用 ZIP + manifest/hash，DB 用在线 snapshot，默认不含 spill | 跨平台、可审计、不会复制 live WAL，也不把敏感大输出默认带走。 |
| D2 | restore 必须维护锁、停止活跃实例并先自动备份当前数据 | rename 不能阻止旧进程继续写，Windows 还会拒绝删除打开文件。 |
| D3 | GC 默认 dry-run；事件 TTL 按 session 最后活动且保护 active session | 防止旧 event 时间误删仍在使用的会话，升级后不发生隐式破坏。 |
| D4 | FTS/大型正文重写是显式可中断 maintenance，不放启动迁移 | 用户库可达 1GB；启动卡死比慢命令更危险。 |
| D5 | spill 配额超限默认 refuse，新数据短结果可用；GC apply 才 oldest-first 淘汰 | 不让后台 job 的瞬时增长静默删除旧证据。 |
| D6 | `.txt`/`.txt.gz` 共用透明 reader，ref 不变 | export、recall、session_search 不需要改变用户可见 ref。 |
| D7 | A 先行，B 作为分阶段目标；新 session 先文件化，旧 session 按需迁移 | A 风险和回滚成本较低，B 长期能避免 DB/FTS 双重正文。 |
| D8 | memory Markdown 是真相；缺 meta 不删文件，meta orphan/tombstone 可按 TTL 清理 | 避免治理破坏用户手工记忆或 files 后端数据。 |
| D9 | q3jy 的索引/事务先于 GC apply；不对 FTS5 虚表逐行存在性检查 | 复用 q3jy 的查询和忙重试，规避 v0.31.0 O(N²) 卡死。 |

## 待确认事项

| ID | 问题 | 倾向 |
|---|---|---|
| Q0 | （0.2）是否同意按 P1（FTS 只索引 tool 输出开头 1KB）→ P2 → P3 的顺序推进，B 暂缓？ | 倾向同意；P1 改动最小、回收最大。 |
| Q1 | backup 是否默认纳入 spill？ | 倾向不纳入，完整恢复时显式 `--include-spill`；归档必须记录 excluded。 |
| Q2 | 是否启用事件 TTL 默认值？ | 倾向默认 off，审批后以 180d 为保守默认；先只清已删 session 的 30d orphan events。 |
| Q3 | spill 达到配额时是否自动删除 oldest？ | 倾向 refuse；只在 `gc --apply` 明确确认后淘汰，并逐 ref 报告。 |
| Q4 | memory orphan 是否允许直接删除？ | 倾向只报告并移动 trash，至少 30d grace；不删除 `MEMORY.md` 列出的文件。 |
| Q5 | FTS 是否改 contentless + unicode61/CJK 二元组？ | 倾向先 benchmark；目标是去掉约 113MB content copy，不能牺牲现有 CJK 命中契约。 |
| Q6 | tool 正文内联阈值是否从 8KiB 降到 2–4KiB？ | 倾向 4KiB，保留头尾可回放；先以历史数据迁移 benchmark 验证回收量。 |
| Q7 | 是否批准 B 路线进入后续版本？ | 倾向批准“新 session 先文件化、旧数据按需迁移”，不批准一次性启动迁移。 |
| Q9 | （0.2）非交互会话（gofer job / `suga run`）是否单独设更短保留期？ | 倾向是：非交互 30 天、交互 180 天，均只在显式 `gc --apply` 时生效。 |
| Q8 | restore 是否允许 `--no-auto-backup`？ | 倾向保留但仅显式 flag + 交互确认；普通命令不允许跳过。 |

## 测试与验证要求

- 单测全部使用 `t.TempDir()` 和隔离 `SUGA_HOME`；ZIP 路径穿越、`.env` 排除、hash mismatch、压缩透明读取、bad gzip、active lock、dry-run 不写入、memory orphan 保护和 restore 自动备份各有 `t.Run` 用例。
- 大库基准生成至少 20,000 entries、相同比例 tool/assistant/events，另准备接近 1GB 的 fixture；测 FTS rebuild、event batch delete、tool spill migration、VACUUM/backup 和 JSONL export，断言无逐行 FTS `NOT EXISTS`、可中断 checkpoint、内存不随全库线性增长到正文大小。
- 反向验证：临时禁用 lock 检查，restore/GC active-instance 用例必须失败；临时让 reader 只找 `.txt`，`.txt.gz` recall/export 用例必须失败；临时删除 hash 校验，损坏归档用例必须失败；临时把 event TTL 条件改成只看 event 时间，active-session 用例必须失败；临时恢复旧 FTS 逐行检查，20,000 行耗时门槛必须失败/超时。
- Windows：`go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")`、`go test -count=1 ./internal/... ./pkg/...`、`staticcheck ./...`；Linux 容器再跑 `go test -race ./...` 和大库 benchmark。restore/rename/锁的 Windows 专项必须在主机跑。
- 真实 provider/PTY 不属于纯数据命令验收；如果实施改动触及 chat 写路径，遵循项目规则补 `make smoke-chat`，本设计阶段不声称已运行。

## 回滚与人工 Gate

本设计和计划均为 Draft，用户确认 Q1–Q8 前不实施。批准后先做 backup/restore 只读与 dry-run，再启用 apply；A 的 FTS/tool migration 和 B 的 JSONL writer 各自独立 feature flag/提交。任何 1GB fixture 超时、数据 hash 不一致、Windows 无法取得锁或真实库出现 startup blocking，都暂停扩大范围，恢复 pre-restore backup，并回到设计 Gate。

### 人工 Gate

1. Gate A：用户批准命令名、备份是否含 spill、GC TTL 和 A/B 路线。
2. Gate B：q3jy 索引/事务提交可用；确认 migration 不改旧文件名、不在启动做全表重写。
3. Gate C：20,000+ entries 与接近 1GB benchmark、正反向验证、Windows 文件锁验证通过。
4. Gate D：监督者审阅真实备份/恢复报告和 `git status`；未授权前不改真实 `~/.suga`、不 push/tag。

## 结论与人工计划 Gate

**0.2 结论**：先实施 P1（FTS 只索引 tool 输出开头，显式分批重建），再 P2（tool 内联阈值），再 P3（备份/GC/spill）；B 暂缓。以下为 0.1 结论原文：推荐先实施“在线备份/安全恢复 + dry-run GC + spill 配额/透明 gzip”，再实施 A 的 FTS/tool 瘦身；B 仅先接新会话和读路径兼容层。本文为 **Draft 0.1 / 待用户审批**，审批前不修改 `internal/personal/store`、不新增 migration、不增加 CLI。
