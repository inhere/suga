<!-- template_id: plan; template_version: 1.2.0 -->
# 数据治理实施计划：备份恢复、GC 与输出配额

> 状态：Draft 0.1 / 待用户审批

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.0 | 2026-10-19 | suga（gofer job） | 建立数据治理任务分解、能力发现和验证 Gate。 |
| 0.1 | 2026-10-19 | suga（gofer job） | 对应 `docs/design/2026-10-19-data-governance-design.md` Draft 0.1，按可回滚功能点拆分备份恢复、GC、spill 和两阶段存储瘦身。 |

## 目标与完成定义

### 目标

在不改代码的本批中产出可执行设计和实施顺序；获批后实现：

1. 在线一致 DB 备份、memory/config 打包、spill 显式纳入、hash manifest，以及停止实例后安全 restore。
2. 默认 dry-run 的 GC：active session 保护、events TTL、memory/meta orphan、WAL checkpoint、FTS 派生数据和可量化的回收大小。
3. spill 目录逻辑/物理配额、按年龄 gzip、`.txt`/`.txt.gz` 透明读，且不改变 `out:<sid>:<hex>` ref。
4. 首先走 A（DB 内瘦身），保留 B（文件为真相 + DB 索引）为后续分阶段路线；所有大库工作可中断、可续、可回滚。

### 完成定义

- 设计和本计划均明确标记 `Draft 0.1 / 待用户审批`，validator 的 design/plan 均 exit code=0、`errors=[]`。
- 计划中的每个新函数/模块都有真实调用路径、近端单测和至少一项反向验证；不以直接调用私有 helper 代替真实 CLI/store/reader 路径。
- 20,000+ entries 的基准覆盖 migration/FTS、event TTL、spill migration、backup/VACUUM 和 JSONL export；任何迁移不对 FTS5 虚表做逐行 `NOT EXISTS`。
- Windows build/vet/gofmt/test/staticcheck 与 Linux `-race` 证据齐全；涉及 interactive chat 才补 `make smoke-chat`，纯治理命令不虚报 PTY 结果。
- 按功能点原子本地 commit，不 push、不打 tag、不改 `.gofer/tracker`；交付时贴 exit code 与原始输出摘要。

## 范围、排除项与授权

- 范围：`internal/cli`（新命令和报告）、`internal/personal/store`（在线快照/GC API、派生表维护）、`internal/personal`（spill reader/writer）、`internal/config`（治理配置）、`internal/wiring`（装配配置）、memory files layout、测试和使用文档。
- 排除：云备份、服务端、增量块存储、provider/ACP 协议、真实用户库原地试验、启动阶段全库重写、未经批准的 JSONL 真相源迁移。
- 兼容边界：现有 `export` JSONL 外形、message refs、output refs、`OpenReadOnly` 不迁移不写入、memory Markdown 真相源、snapshot undo 语义不变。
- 授权：当前只授权读代码、写两份文档和提交文档；用户批准前不得新增命令/config/migration。push/tag/真实库操作均需额外授权。
- host_or_non_offline_action=REQUIRED

## 输入与批准证据

- 设计：`docs/design/2026-10-19-data-governance-design.md`，Draft 0.1 / 待用户审批。
- issue：`suga-zy9f`（backup/restore、GC、spill 配额压缩）；`suga-q3jy`（entries/events 索引、SQL 报表、`BEGIN IMMEDIATE` 忙重试）。
- 代码事实：`internal/personal/store/store.go:21-229`（embed migration、WAL、Open/ReadOnly）；`gc.go:9-98`（当前 session/event 删除与 VACUUM）；`fts.go:14-65` 与迁移 `20261010-234359-add-ledger-fts-memory-meta.sql:12-66`（FTS/metadata）；`entries.go:13-61`、`events.go:12-150`、`sessions.go:43-192`；`internal/personal/spill.go:23-121`、`internal/personal/ledger.go:57-212`；`internal/cli/gc.go:20-169`、`export.go:20-207`；`internal/config/paths.go:11-49`、`snapshots.go:12-119`、`settings_registry.go:88-195`。
- 用户实测：2026-10-19，1.09GB、4096 × 267540 页、freelist 183；FTS data 387MB、FTS content 约113MB、tool entries 292MB、events 26MB、spill 约72MB，日增量 44–145MB。
- 参照事实：codex 采用按会话 JSONL + 可重建 SQLite 投影；Claude Code 采用按项目/会话 JSONL；本计划不照搬 1.38GB 的 codex thread_items 投影缓存。
- 实施前必须重读 design、两 issue 和 q3jy 的实际提交；代码与文档事实冲突时停止，不进入 T1。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | SQLite 在线一致 backup/restore 与校验归档 | `store.Open/OpenReadOnly`；modernc SQLite 能力；现有 snapshot/`export` 文件读写 | `OpenReadOnly` 校验入口、config paths、现有 session lock | `internal/personal/backup` 作为唯一归档 owner；CLI 只解析 flags | OWNER_EXTENSION | 当前没有 archive manifest、VACUUM INTO/backup、restore staging 和自动备份 | 归档必须不复制 WAL，不把 `.env`/secret 变成第二份；只保留一个 ZIP owner |
| CAP-02 | GC 的 active 保护、事件 TTL、memory/meta orphan 和 WAL | `store.CollectGarbage`；`internal/cli/gc.go`；snapshot cleanup；memory/files Enumerator | session lock/run 进程发现、cascade/FTS trigger、snapshot limits | 扩展 `GCReport`/CLI 报告，store 提供分批事务 | THIN_ADAPTER | 当前没有 event TTL、memory 文件扫描、WAL checkpoint 和统一 dry-run bytes | GC 不应复制 lock 规则或删除 files 真相；每种数据有明确 owner |
| CAP-03 | spill 配额、gzip 和透明读取 | `SessionSpiller`；`Ledger.readOutputLines`；`export.writeExportOutputs`；`tools.Spiller` | `OutputFileName`/ref、`os.OpenRoot`、redaction、line paging | `internal/personal/spill` 的 `openOutput`/maintenance owner；ledger/export 调用它 | OWNER_EXTENSION | 现有只写 `.txt`，无压缩/配额/oldest-first | 不能在 ledger、export、GC 各复制 gzip 和 path validation |
| CAP-04 | q3jy 索引/事务与治理 SQL | q3jy issue 的 `entries(type)`、`events(kind/turn_id/session+time)`、BEGIN IMMEDIATE/retry | q3jy 的索引和 retry helper | GC/report 只消费 store interface，不另造一套 busy policy | DIRECT_REUSE | 治理在 q3jy 未完成时不能承诺 SQL 聚合性能 | migration 排序和应用时间要兼容；不修改已应用 migration |
| CAP-05 | DB 正文瘦身与 FTS 派生 rebuild | `entries_fts`、`ftsSourceSelect`、`AppendEntries` transaction、spill output layer | session/seq 定位、FTS trigger、output ref | 独立 maintenance command + checkpoint；不在 `Open` 做全表动作 | MINIMAL_NEW_MODULE | contentless/外部内容和 tool preview 尚不存在 | 迁移与 GC 分离；禁止 FTS 逐行 NOT EXISTS，避免 O(N²) |
| CAP-06 | 会话 JSONL 真相源的 staged migration | codex/Claude 布局；当前 `entries`/`AgentStorage`/ledger/export | 现有 entry type/seq/ref/export 语义 | 新 session writer + transcript reader，旧 DB fallback | MINIMAL_NEW_MODULE | 所有 resume/recall/trace/export/GC/backup 读路径都要迁移 | 不先做读兼容就不能删 DB json；gzip 会破坏随机 offset |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| MOD-04 | CAP-05 | `internal/personal/maintenance` | FTS constants/migration and current GC | FTS migration cannot run at startup or be coupled to GC | explicit checkpointed job is a new lifecycle | 1GB rebuild must be cancellable | one maintenance owner and one manifest | interrupted shadow table discarded; old FTS remains queryable |
| MOD-05 | CAP-06 | `internal/personal/transcript` | AgentStorage, Ledger, export | current readers assume DB JSON and random SQL rows | reader/writer seam supports old/new backends | offset + Windows lock semantics are new | transcript package owns wire record/CRC and fallback | feature flag off reads old DB; incomplete batch deletes only new files |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 默认启动时自动 VACUUM/FTS rebuild | CAP-02 | 删除后仍可显式 `gc --vacuum`/maintenance；启动副作用会重现 v0.31.0 大库卡死。 |
| `.env` 打包或恢复时展开 secret | CAP-01 | 删除后 config 中的 `${VAR}` 引用已足够；纳入 secret 会扩大泄漏面且无法跨主机复现。 |
| spill 超额立即 LRU 静默删除 | CAP-03 | 删除后 refuse + 显式 GC 保留可审计性；后台 job 不应悄悄丢证据。 |
| 每个 FTS row 做 `NOT EXISTS` 对账 | CAP-05 | 删除后 set-based rebuild/批次 checkpoint 可避免 FTS5 UNINDEXED 全表扫描。 |
| 一次性将所有旧 entries 在升级时改写为 JSONL | CAP-06 | 删除后新 session file-first + 旧 session 按需迁移仍可实现长期方向；启动必须快。 |
| codex 风格大型 SQLite 投影缓存 | CAP-06 | 删除后 DB 只保留最小索引即可，避免再造 1.38GB thread_items。 |

## 前置检查

- P0：记录 `git status --short`、HEAD、branch；只允许本批两个 Markdown 文件变更；提交前检查 `git diff --stat` 和 `git diff --cached --name-only`。
- P1：重读设计、`suga-zy9f`、`suga-q3jy`；确认当前 HEAD 没有 backup/restore 命令、spill gzip、治理 config；确认 q3jy 的实际索引名和事务 helper。
- P2：核对用户实测与代码 FTS 角色：用隔离副本 `SELECT role, COUNT(*) FROM entries_fts GROUP BY role`；不读取或修改真实 `~/.suga`，副本放 worktree `tmp/` 并删除。
- P3：验证 modernc SQLite 对 `VACUUM INTO`、`PRAGMA wal_checkpoint`、busy/backup 的实际支持；若 backup API 不可用，锁定 VACUUM INTO fallback，不自行引入 cgo。
- P4：设计 20,000+ entries fixture，含 tool 长正文、assistant、events、handoff/compaction、FTS；定义 batch 1000/5000 和 1GB 级别 benchmark 的 timeout。
- P5：确认 Windows 维护锁、rename、OpenRoot、FlushFileBuffers 行为；确认 Unix flock 与 Windows lock 的实现必须分文件或抽象到平台 adapter。
- P6：文档 validator 先验证本批设计/plan；代码验证只在用户批准并实施后执行。

## 波次与依赖

| 波次 | 任务 | 依赖 | 执行方式 |
|---|---|---|---|
| W0 | T0 前置检查和审批 Gate | 无 | 只读；事实冲突即停止 |
| W1 | T1 backup/restore/archive manifest | T0 | 先实现只读 backup，再 staged restore；独立提交 |
| W2 | T2 GC report、events TTL、memory/meta、WAL | T0、q3jy 索引/事务 | store → CLI；先 dry-run，apply 后置 |
| W3 | T3 spill quota/gzip/transparent reader | T0 | reader 先于压缩 writer；ledger/export/GC 统一入口 |
| W4 | T4 A 路线 FTS/content/tool shrink maintenance | T1–T3、benchmark | shadow table/批次 checkpoint；不进 Open |
| W5 | T5 B 路线新 session JSONL adapter | T1、T3、T4 结论 | file-first + DB fallback；先新写入，旧数据按需 |
| W6 | T6 cross-platform/large benchmark/reverse gates | T1–T5 | Windows 主机 + Linux race；失败不宣布完成 |
| W7 | T7 docs/README/runbook | T1–T6 | 与实现版本同步；纯文档独立提交 |

## 任务

### T0 前置检查与实现 Gate

- 文件：无产品改动；仅记录报告。
- 动作：执行 P0–P6；核实当前代码事实、q3jy 依赖、FTS 角色与 1GB 基准。
- 验证：文档 validator；`git status --short`；实施前 baseline build/vet/test/staticcheck。
- 完成标准：用户批准 Q1–Q8；不存在“启动迁移仍需全表操作”的方案；不改 `.gofer/tracker`。
- 反向验证：不适用，事实冲突时 fail-closed。
- 提交：不提交。

### T1 在线 backup/restore

- 文件/模块：新 `internal/personal/backup`（或经 T0 确认的等价 owner）、`internal/cli/backup.go`、`restore.go`、配置/路径 helper、测试、usage 文档。
- 动作：ZIP manifest/version/relative path/hash；DB 在线快照（backup API 或 VACUUM INTO）；config 脱敏；memory layout enumeration；spill 默认排除/flag include；maintenance lock；restore staging、hash/integrity check、自动 pre-restore backup、Windows/Unix replace。
- 实际路径：`suga backup` 不打开可迁移 store 作为写入源；restore 校验后用 `OpenReadOnly`/SQLite integrity check，成功替换才允许下一次 `store.Open` 迁移。
- 测试：在线 writer 期间 backup 内容可打开；`.env`/secret 不进 ZIP；路径穿越/duplicate/hash mismatch/坏 gzip/超大 archive 拒绝；active run/lock restore 失败；恢复失败保留旧数据；重复 restore 幂等；`--include-spill` 的 ref 可 recall，排除时返回明确 unavailable。
- 反向验证：去掉 hash check，损坏归档必须失败；去掉 maintenance lock，active restore 用例必须失败；让 backup 直接复制 live DB，WAL 一致性 fixture 必须失败。
- 大库：1.09GB 需约 1.1GB 临时空间；VACUUM INTO 粗估 NVMe 1–5m、SSD 3–10m、慢盘 10–20m；只在命令执行，临时文件完成后才归档。
- 提交：`feat(data): add verified session backup and restore`。

### T2 GC 扩展和 dry-run bytes

- 文件/模块：扩展 `internal/personal/store/gc.go` 或新增 `maintenance.go`、`internal/cli/gc.go`、`internal/config` 治理 reader/registry、memory layout scanner、测试。
- 动作：保留 `--older-than` 必填和默认 dry-run；增加 `events_ttl`（默认 off）、orphan events 30d、memory tombstone 180d、orphan grace 30d；active session/lock/status/turn 二次检查；分批 1000–5000 行短事务；memory orphan 先 trash；WAL `PASSIVE`/显式 `TRUNCATE`；`VACUUM` 仅显式 flag。
- FTS：session 删除走 cascade/trigger；不逐 entry 反查 FTS；可选 `optimize`/统计不阻塞 GC。memory_meta 以文件/索引为准，缺 meta 不删 entry。
- 与 q3jy：事件扫描/报表使用其索引和 BEGIN IMMEDIATE/retry；若 q3jy 未完成，apply 停在 dry-run 并明确提示，不复制一套事务策略。
- 测试：dry-run 零写入；active session/child session 保留；events 按 session last activity；orphan event TTL；memory listed/no-meta 保留、old tombstone 可清理、unknown file grace；WAL busy 报告；每批取消后可重跑；回收 bytes 分确定/估算。
- 反向验证：只看 event.started_at 应被 active-session fixture 失败；去掉 second lock check 应被 race fixture 失败；把 FTS 逐行 NOT EXISTS 注入 20k benchmark 应超过门槛。
- 大库：events 26MB 每 1000–5000 批次，预计秒至分钟级，不能把所有 events 拉进 Go；VACUUM 1GB 是维护窗口动作，预计分钟级并需等量空间。
- 提交：`feat(gc): govern events memory metadata and wal cleanup`。

### T3 spill 配额、gzip 与透明 reader

- 文件/模块：`internal/personal/spill.go` 扩展或 `spill_reader.go`、`internal/personal/ledger.go`、`internal/cli/export.go`、config/settings registry、测试与 usage docs。
- 动作：`tools.spill.max_bytes/max_age/compress_after/compression/on_quota/keep_days`；writer `.tmp` + close/fsync + rename；配额统计逻辑/物理两种 bytes；`refuse` 默认；gzip old idle `.txt` 到 `.txt.gz`；共用 `openOutput` 流式解压、同 ref、Root/path validation；task output 和 active lock 不压缩。
- 测试：`.txt`/`.txt.gz` recall、session_search(ref)、export 一致；bad gzip 错误；symlink/path traversal 拒绝；配额 refuse 不删除 old；GC apply oldest-first 报告每 ref；Windows open/rename 与 Unix lock；gzip 不一次读全文件。
- 反向验证：reader 只找 `.txt` 时 compressed fixture 必须失败；去掉 active check 时 task output fixture 必须失败；把 quota policy 改成 silent delete 时保护旧 ref 的用例必须失败。
- 大库：现有 spill 约 72MB，压缩是顺序 I/O，按文件逐个处理；以 72MB 和合成 1GB 输出测 p50/p95，不能一次读入内存。
- 提交：`feat(output): add spill quotas compression and transparent reads`。

### T4 A 路线 DB/FTS/tool 瘦身

- 文件/模块：新增显式 maintenance owner、shadow FTS migration/command、tool preview migration、基准/恢复测试；不修改已应用 migration。
- 动作：先 benchmark `contentless`、外部内容、unicode61/CJK 二元组和近 N 天索引；候选先把 FTS content copy 约 113MB作为可回收目标；tool >4KiB 先 spill fsync，再 DB 写 preview/ref；保留 old JSON 直到 hash/recall/export 校验完成；最后显式 VACUUM。
- 严格禁止：`Open` 迁移中全表重建；对 FTS5 逐行 `NOT EXISTS`；没有 checkpoint 的全库 `UPDATE`；直接删 tool 原文。
- 测试：近 4.5 万 searchable entries 相关度/CJK 回归；tool preview/recovery/export；shadow swap 崩溃；20k+ migration 几秒级门槛（按实际设备记录）；contentless 丢失/回退验证。
- 反向验证：临时恢复 non-contentless/trigram baseline，回收量目标和 benchmark 对照必须失败；临时先删 DB 原文再写 spill，spill failure fixture 必须失败。
- 估算：FTS content 113MB + FTS data/tokenizer 100–250MB + tool 100–180MB，现实目标回收约 300–550MB；必须以实测替代承诺。
- 提交：`feat(maintenance): compact derived ledger and tool bodies`。

### T5 B 路线按会话 JSONL

- 文件/模块：新增 transcript record/writer/reader、session lock adapter、DB offset index、旧 DB fallback、`AgentStorage`/`Ledger`/export/trace/backup/GC 测试。
- 动作：新 session file-first；记录含 schema/seq/type/turn/time/body/CRC/hash/offset/length；文件 fsync 后 DB `BEGIN IMMEDIATE` 写索引；启动 repair 只截断尾部 partial，DB 有索引文件缺失则 fail-closed；完整 file durable 但 DB index 缺失可幂等重放；旧 sessions 逐批迁移，checkpoint `(sid,next_seq,source_hash,target_offset)`。
- 读路径：resume、`--from`、compaction/handoff、segment replay、session_search/recall、export/trace、backup/restore/GC 全部经过 transcript reader；旧 DB `json` fallback，直到校验完成不删。
- 并发：同 session OS lock；Windows 独占句柄和重试，Unix flock/lockfile；不能以 busy timeout 替代。
- 反向验证：去掉 fsync-before-DB，crash fixture 必须发现 DB/file 不一致；去掉 old fallback，未迁移 session 读路径必须失败；把 gzip 当随机 offset 文件，recall offset fixture 必须失败。
- 大库：500–1000 entries checkpoint；1.09GB 顺序 I/O 粗估 NVMe 1–5m、SSD 3–10m、HDD/杀毒 10–30m；绝不在 Open 期间执行。
- 提交：`feat(transcript): add file-backed session ledger behind fallback`（仅用户另批批准 B 后）。

### T6 跨平台、基准和回归 Gate

- Windows：`go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")` 无输出、`go test -count=1 ./internal/... ./pkg/...`、`staticcheck ./...`；命令 exit code 与输出同时记录。
- Linux：`go test -race ./...`；基准/大库 fixture 在 worktree `tmp/`，完成后删除；不得复制真实 `~/.suga` 到系统临时目录。
- 真实 provider：数据治理不需要 provider；若 T5 改动 chat 交互，按规则补真实 provider/`make smoke-chat`，Windows 主机写明无法运行 PTY。
- 反向证据：T1–T5 每个至少一项“去掉逻辑失败、恢复后通过”；不能只口头描述。
- 提交：如只补测试，`test(data): cover governance failure and large-ledger gates`；无改动不空提交。

### T7 文档与使用说明

- 文件：README/README.zh-CN、`docs/usage/gofer-personal-agent.md`、suga-guide sessions/tools/config reference、`docs/README.md`。
- 动作：说明 backup/restore 的 `.env` 与 spill 规则、gc dry-run/apply/TTL、spill gzip/ref 不变、维护窗口和 1GB 风险；A/B 路线只写已实现部分，未批准内容标 Draft。
- 反向验证：暂时删掉命令/配置 grep 预期会发现文档验收失败；文档不声明未运行的 Windows/Linux/真实库验证。
- 提交：`docs: document data governance and recovery operations`。

## 提交与合并顺序

获批后只在本地按功能点提交，不 push：

1. T1 `feat(data): add verified session backup and restore`
2. T2 `feat(gc): govern events memory metadata and wal cleanup`
3. T3 `feat(output): add spill quotas compression and transparent reads`
4. T4 `feat(maintenance): compact derived ledger and tool bodies`
5. T5 `feat(transcript): add file-backed session ledger behind fallback`（另行批准）
6. T6 仅有补测试才提交
7. T7 `docs: document data governance and recovery operations`

每次提交前：`git status --short`、`git diff --stat`、`git diff --cached --name-only`；只 `git add` 当前功能文件；不改 `.gofer/tracker`。当前批只提交两份 Draft 文档。

## 回滚与恢复

- T1：删除新命令代码不影响旧 DB；恢复失败使用自动 pre-restore ZIP；归档格式不写回用户库。
- T2：关闭 `events_ttl`/apply 回到原 session GC；WAL checkpoint 可重复；已提交删除只能用 backup restore，不能依赖 DOWN migration。
- T3：关闭 compression 保留 `.txt`；透明 reader 兼容两种后缀；quota refuse 不改变旧数据；错误压缩文件从原 `.txt` backup 恢复。
- T4：shadow FTS swap 失败保留旧表；tool migration 在 hash 校验前不删 `entries.json`；contentless 方案不批准则回到 A 的 preview-only 或不做。
- T5：feature flag off 让新旧 session 都走 DB fallback；未完成 checkpoint 只清理新 JSONL；旧 `entries.json` 保留到 full audit。
- 用户级恢复：停止实例、取得 maintenance lock、从 pre-restore archive 校验后恢复；Windows 若有句柄未释放，先终止旧进程而不是强删文件。

## 人工 Gate

1. Gate A：用户确认 Q1–Q8，以及 backup 是否含 spill、events TTL、quota policy、A/B 路线。
2. Gate B：监督确认 q3jy 已合并或给出等价索引/事务证据；确认新 migration/maintenance 不在 Open 中执行。
3. Gate C：T1–T3 近端测试、20k+ benchmark、Windows lock/rename 和反向验证通过后，才允许 apply。
4. Gate D：T4/T5 各自审阅回收量、数据 hash、恢复流程和旧版本回滚；失败则不推进下一波。
5. Gate E：最终发布前文档、命令输出、`git status` 和提交列表无夹带；不 push/tag，真实 provider/PTY 仅在确实触及交互时执行。

## 可追溯性

| 设计要求 | 计划任务 | 验收证据 |
|---|---|---|
| D1 ZIP/hash/online DB backup | T1 | manifest、坏归档、在线 writer、自动备份 fixture |
| D2 restore 不覆盖活跃实例 | T1/T2 | maintenance lock、run/lock、Windows rename tests |
| D3 conservative GC/event TTL | T2 | dry-run zero-write、active session、last-activity TTL、batch report |
| D4 memory/meta orphan 安全 | T2 | listed/no-meta 保留、tombstone TTL、trash restore |
| D5 WAL/FTS 派生维护 | T2/T4 | checkpoint busy report、session trigger、无逐行 FTS 对账 |
| D6 spill quota/gzip/ref compatibility | T3 | `.txt`/`.gz` reader、export/search/recall equal output、quota refuse |
| D7 A 方向回收 300–550MB 估算 | T4 | 1GB benchmark、FTS/tool size before/after、hash/recovery |
| D8 B 文件真相和崩溃顺序 | T5 | fsync/DB crash fixture、offset/checkpoint、old fallback |
| D9 q3jy relationship | T2/T4 | SQL plan/index evidence、BEGIN IMMEDIATE retry、migration ordering |
| D10 Windows/Unix behavior | T1–T3/T6 | host output、race result、platform lock/rename tests |
| D11 no startup blocking | T0/T4/T5 | Open timing regression、maintenance-only command paths |

## 完成 Gate

宣布实施完成必须同时满足：

- 文档 validator：
  `python -I D:\work\inhere\runtime\idev-std-releases\0.26.1\plugins\inhere-dev-standards\skills\inhere-doc-workflow\scripts\validate_document.py --kind design docs/design/2026-10-19-data-governance-design.md`
  与 `--kind plan docs/plans/2026-10-19-data-governance-plan.md` 均 exit code=0、`errors=[]`。
- Windows 主机：`go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")` 无输出、`go test -count=1 ./internal/... ./pkg/...`、`staticcheck ./...` exit code=0。
- Linux：`go test -race ./...`；大库 benchmark 的命令、设备、批量、耗时和内存证据齐全。
- T1–T5 反向验证均有原始失败/恢复后通过证据；任何失败不能只标“通过”。
- `git status --short` 无夹带；本批提交仅为两份文档的一个 `docs:` commit，不 push/tag；监督者另行执行 Windows/Linux 外部 Gate。
