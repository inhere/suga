# 探索方式对比实验 A/B：内置 file_* vs 接入 codebase-memory-mcp 图工具（suga 第十六批）

> 状态：调研报告（非设计文档，不过文档校验器）
> 日期：2026-10-18
> 关联：`docs/research/2026-10-18-explore-efficiency.md`（基线与 v0.32.0 复测）、`docs/design/2026-10-18-symbol-refs-design.md`（file_refs 设计，未实施）
> 产物：本报告；运行脚本与原始结果在运行期放在 `tmp/exp/`，交付后已删除

## 1. 目的与方法

回答一个问题：**给模型一个外部的代码知识图谱（codebase-memory-mcp）能不能把 suga 的只读探索步数压下来**，从而决定下一步值得做“自建引用检索 `file_refs`”还是“接入图工具”。

### 1.1 环境与隔离

| 项 | 取值 |
|---|---|
| 主机 | Windows（本 job 所在主机），无 pty、无 cgo |
| suga | 本 worktree `cmd/suga`（HEAD `1501566e`，v0.32.0 之后、含 `file_symbols`/`file_read` 大纲与多段范围读） |
| SUGA_HOME | 每次运行一个隔离目录 `tmp/exp/run/<tag>/home/`，只写入非密钥配置；密钥用 `api_key: "${OPENCODE_API_KEY}"` 从进程环境变量展开（**不复制 `.env`、不打印值**） |
| 模型 | `opencode-go/deepseek-v4.1-flash`（thinking high） |
| 运行方式 | `suga run --read-only --session-id <tag> -p <prompt>`，工作目录 = 本 worktree，`max_steps: 60`，`tools.approval: auto` |
| 规模 | 6 任务 × 2 档 × 2 次 = **24 次运行**，4 并发；24/24 `exit 0` |
| 计时 | 进程墙钟（含模型与工具时间）；指标由 `suga trace <sid> --analyze --json` + `suga trace <sid> --json` 事件统计 |

### 1.2 两档配置

| 档 | 说明 |
|---|---|
| A（现状） | 只读、无 MCP、无 `tools.preferred`；`tools_count = 14` |
| B（+图工具） | A + `mcp.servers: [{id: cbm, command: C:\Users\KZL\.local\bin\codebase-memory-mcp.exe}]` + `tools.preferred: [{name: cbm, kind: mcp, when: 查调用链、谁调用了谁、符号与代码结构}]` + 只放行只读检索工具；`tools_count = 31`（`tools_mcp = 17`），环境说明段 1124 字符 |

`cbm` 是常驻（非延迟）工具，17 个工具 schema 每次请求都发送。索引为已有索引（项目名 `suga`，`index_status` 报 ready），**未重建**。

**与任务要求的偏差（代码事实冲突）**：任务要求把只读工具放 `tools.auto_allow`，但 `--read-only` 只认 `tools.read_only_allow`（`internal/wiring/runtime.go:38-40`、`docs/usage/gofer-personal-agent.md:94`：「条目必须带服务名，`auto_allow` 不会扩大它」），只配 `auto_allow` 会让所有 MCP 调用被拒。本次两个列表写入同一组只读工具（`search_graph`/`query_graph`/`trace_path`/`get_code_snippet`/`get_file_outline`/`search_code`/`get_architecture`/`index_status`/`list_projects`），**未**放行 `index_repository`/`delete_project`/`manage_adr`/`ingest_traces`。另：清单未含 `get_graph_schema`，`T6-B-2` 调了一次被拒（`tool_errors: {get_graph_schema: 1}`），并花掉一步与一段错误上下文。

### 1.3 任务与标准答案（执行者按代码核对，路径相对 worktree）

| 任务 | 提问（原文） | 标准答案 |
|---|---|---|
| T1 | `suga_manifest` 工具在哪里注册？它的 `section` 参数怎么处理？给出文件:行。 | 活运行时 `internal/wiring/runtime.go:503-521`（`Register(NewManifestTool(...))`，闭包实时 `BuildManifest`）；静态 `suga manifest` 路径 `internal/wiring/manifest.go:436`（`NewManifestTool(nil)`）；工具本体 `manifest_tool.go:28`、`section` 处理 `manifest_tool.go:71-107`（空=全量；source 为 string 时非空报错 `:77`；结构化值先 Marshal→Unmarshal→`all[section]`，未知 key 在 `:99` 报错并列出可用 key） |
| T2 | 列出 `internal/config` 包里所有导出的 `Read*` 函数及所在文件:行。 | 19 个（含 v0.32.0 的 `network_proxy.go:26` 与 `settings_registry.go:570`/`:603`） |
| T3 | `session_search` 对中文/CJK 短词（1–2 个字）是怎么处理的？给出文件:行。 | 按 rune 数分流：`minMatchRunes = 3`（`internal/personal/store/fts.go:73`），`>=3` 走 FTS5 trigram MATCH、1–2 rune 走 `text LIKE ? ESCAPE`（`fts.go:164-191`，`likeSnippet` `:341`）；内存索引同型 `memfts.go:148-166` |
| T4 | `suga run --read-only`（或 `--ro`）从命令行参数到写工具被禁用，经过了哪些函数？按调用顺序列出文件:行。 | `run.go:207`（`cmd.BoolVar(&opts.ReadOnly, "read-only" …)`）→ `validatePersonalOptions`（`run.go:241`）→ `runPersonalWithOptions`（`run.go:84`）→ `opts.wiringOptions`（`run.go:141` 调用 / `:224` 定义，`:229` `ReadOnly`、`:231` `MemoryReadOnly`）→ `wiring.OpenCLI`（`run.go:144` → `runtime.go:263`）→ `runtime.go:331` `opts.AllowWrite = (…) && !opts.ReadOnly` → `runtime.go:361` 局部 `allowWrite` → `buildPersonalCLICapabilitiesWithSnapshot`（`runtime.go:431` → `:808-809` `if allowWrite`）→ `native.BuildTools(env, allowWrite)`（`pkg/agentcore/tools/native/catalog.go:145`，非只读工具在 `:149` 被跳过）；另 `runtime.go:456`（`shell_exec` 仅 `allowWrite`）、`:279` `newCLIMCPGate(opts.ReadOnly)`、`:384` `adddir.Effective(…, opts.ReadOnly)` |
| T5 | 哪些地方调用了 `appconfig.ReadModelSettings`？列出调用方函数及文件:行。 | 生产代码 5 处：`internal/cli/models.go:27`（`models` 命令）、`internal/wiring/manifest.go:480`（`loadRuntimeSettings`，manifest/审批表）、`internal/wiring/runtime.go:760`（运行时主模型解析）、`internal/config/settings_registry.go:517`（`config get` 校验）、`:643`（配置读取/`config set` 相关解析）；定义在 `internal/config/providers.go:556` |
| T6 | `personal.Runner.Run` 被哪些地方调用（直接调用方），各自在什么场景下调用？ | 5 处：`internal/acpserver/server.go:286`（ACP `session/prompt`）、`internal/chat/chat_turn.go:192`（交互 chat 每轮）、`internal/cli/run.go:326`（`suga run` 一次性 prompt）、`internal/cli/run.go:447`（`finishBackgroundTasks` 后台任务未结束时再跑一轮）、`internal/personal/subagent.go:94`（`RunChild` 里的 `child.Run`，子代理，唯一生产入口 `internal/wiring/subagent.go:66`） |

### 1.4 与 §7 复测的口径差异

- 新增 T5/T6 两个“追调用链”任务；T1–T4 提示词按 §1.3 提问栏复原。
- 每档每任务 2 次（§7 只有档 A）；本次同时有 A/B 两档，A 档即 §7 口径的再跑。
- 指标沿用：步数=`model_calls`、工具调用=`tool_calls`、token=`input/cache_read/output`、墙钟=进程墙钟；重复读给两个口径：(a) `analyze` 的 `rereads`、(b) 本报告按事件统计“同一文件被 `file_read` 多次读”。

## 2. 结果

### 2.1 每任务均值与区间（24/24 `exit 0`）

| 任务 | 档 | 步数 | 工具调用 | 墙钟 | 输入 token | 工具错误 |
|---|---|---|---|---|---|---|
| T1 `suga_manifest` | A | 7.0（7–7） | 11.0（7–15） | 24.9s | 91.1k | 1（grep） |
| | B | **5.0（4–6）** | 7.5（5–10） | 21.4s（15.2–27.5） | 83.4k | 0 |
| T2 `internal/config` `Read*` | A | 3.5（2–5） | 3.5（1–6） | 12.5s | 29.6k | 0 |
| | B | **2.5（2–3）** | 3.0（2–4） | 11.6s | 34.3k | 0 |
| T3 CJK 短词 | A | 6.0（6–6） | 9.5（9–10） | 30.9s | 104.7k | 0 |
| | B | **8.5（8–9）** | 13.0（12–14） | 44.2s | 206.4k | 0 |
| T4 `--read-only` 调用链 | A | 19.0（16–22） | 36.5（32–41） | 162.7s | 909.9k | 0 |
| | B | **27.0（22–32）** | **50.5（47–54）** | **394.3s（345–444）** | **1490.4k** | 3（read 1、grep 2） |
| T5 `ReadModelSettings` 调用方 | A | 8.0（7–9） | 17.5（15–20） | 74.5s | 131.7k | 0 |
| | B | **9.0（8–10）** | **26.0（22–30）** | 75.3s（57–94） | 205.5k | 0 |
| T6 `Runner.Run` 调用方 | A | 12.5（12–13） | 22.0（18–26） | 104.5s | 258.1k | 0 |
| | B | **18.5（18–19）** | **32.5（32–33）** | **138.1s** | **585.3k** | 1（被拒） |

### 2.2 工具分布与重复读

| 档 | 任务 | 工具分布（两次合计） | 重复读 (a) analyze | 重复读 (b) 同文件多次读 |
|---|---|---|---|---|
| A | T1 | file_grep×12, file_read×10 | manifest.go×2 | manifest.go×2, manifest_tool_test.go×2 |
| B | T1 | file_read×8, file_grep×7 | runtime.go×2, manifest.go×3 | runtime.go×2, manifest.go×3 |
| A | T2 | file_grep×3, file_symbols×2, file_find×1, file_list×1 | – | – |
| B | T2 | file_grep×3, file_symbols×2, file_list×1 | – | – |
| A | T3 | file_grep×10, file_read×9 | – | – |
| B | T3 | file_grep×14, file_read×11, file_symbols×1 | – | fts.go×2 |
| A | T4 | file_read×34, file_grep×32, file_list×4, file_find×2, session_search×1 | – | runtime.go×3、catalog.go×2、模块缓存 cflag/cflag.go×2、approval_policy.go×2 |
| B | T4 | file_grep×45, file_read×42, file_list×8, file_symbols×3, list_projects×2, file_find×1 | – | runtime.go×2、catalog.go×2、approval_policy.go×2、模块缓存 cflag.go×2 |
| A | T5 | file_grep×19, file_symbols×8, file_read×5, recall×2, file_find×1 | – | – |
| B | T5 | file_symbols×23, file_read×11, file_grep×10, recall×2, search_graph×2, trace_path×2, list_projects×1, file_list×1 | – | settings_registry.go×4 |
| A | T6 | file_grep×27, file_read×13, file_symbols×3, session_search×1 | – | runner.go×2, cli/run.go×2 |
| B | T6 | file_grep×26, file_read×15, list_projects×6, search_graph×5, trace_path×5, query_graph×3, get_file_outline×2, file_symbols×1, get_graph_schema×1（拒）, index_status×1 | – | subagent.go×2、acpserver/server.go×2、chat_turn.go×2、cli/run.go×2 |

观察：

1. **两档的主力工具完全一样**：`file_grep` + `file_read`（+ 在“列清单”任务上的 `file_symbols`）。B 档并没有把工具换成图工具，只是**在常用工具之外多背了 17 个 cbm 工具的 schema**（每次请求都发送）。
2. **B 档的额外开销**是“翻项目列表”（`list_projects` ×8，含 `offset=50/100/120/150` 翻页，结果为 50+ 个仓库路径清单，对本题无用）与“先探 qn 再查”（`search_graph` → `trace_path`/`query_graph` 的试错）。
3. **重复读没有因为图工具减少**：T5-B 反而出现 `settings_registry.go×4`（A 档为 0）；T4 两档都仍在 `runtime.go`/`catalog.go`/模块缓存 `cflag.go` 上重复取窗口。
4. **模块缓存依赖读取仍在**：T4 两档都去读 `D:/env/gopath/pkg/mod/.../cflag.go`（只读运行没有 shell/`go doc`，`file_symbols` 对目录可用但模型不往依赖目录用）。

### 2.3 答案正确率

**24/24 全部正确**（逐个对照 §1.3 的标准答案；含文件:行）。要点：

- T2 四次运行都给出 **19** 个 `Read*`（与本批次代码事实一致；报告 §1.3 的 19 个中 `network_proxy.go:26`、`settings_registry.go:570/603` 都被列到）。
- T1 的 A/B 都指出报告 §1.3 里两处**行号漂移**（`runtime.go:503-516` 实为 `503-521`、`manifest.go:409` 实为 `436`），并给出 `section` 的 string-source 报错分支与未知 key 报错；只有 B 档一次额外给出了实体注册的完整路径。
- T4 四次都给出 `run.go:207 → wiringOptions → OpenCLI → runtime.go:331 → BuildTools(allowWrite=false)` 这条链，并补出 MCP gate、memory、adddir 三处只读收紧。
- T6 四次都给出 5 个直接调用方（包含 `internal/personal/subagent.go:94` 的 `child.Run`）；A 档靠 `file_grep` 迭代找到，B 档两次里靠 `search_graph`+`trace_path` 找到。
- 没有一次出现“答案错但看起来自信”的失败样本；错误主要落在**工具调用本身**（共 5 次失败/拒绝）。

## 3. B 档 cbm 工具的使用率与效果

**使用率：3/12 次运行（25%）**，且全部集中在“追调用链/找调用方”任务；T1/T2/T3 六个 B 运行 **0 次**调用 cbm（这些任务它继续用 `file_grep`/`file_symbols`）。

| 运行 | cbm 调用 | 结果是否可用 |
|---|---|---|
| T4-B-1 | `list_projects` ×2（含 `offset=50` 翻页） | **无用**：返回 50+ 个仓库路径清单，与“`--read-only` 调用链”无关，占 2 步 |
| T4-B-2 | 0 | 自己退回 `file_grep`/`file_read`（32 步，本次最慢） |
| T5-B-2 | `list_projects` 1、`search_graph` 2（`name_pattern=ReadModelSettings`，先 `project=b16x` 再 `project=suga`）、`trace_path` 2 | 部分可用：`search_graph` 找到了定义位置；`trace_path` 用对 qn 后返回了入边 |
| T6-B-1 | `list_projects` 4（`offset=50/100/150`）、`search_graph` 2（`Runner\.Run`、`^Run$`）、`trace_path` 2 | 试错型：先用 `name_pattern=Runner\.Run` 摸 qn，再用 `suga.internal.personal.Run` 成功 |
| T6-B-2 | 15 次：`search_graph` 3、`query_graph` 3、`trace_path` 3、`get_file_outline` 2、`list_projects` 2、`index_status` 1、`get_graph_schema` 1（**被拒**） | **最有价值的一次**：Cypher `MATCH (c)-[:CALLS]->(m)` 直接给出调用方，模型再交叉核对 |

### 3.1 失败/误导案例（图工具给错或给空时模型怎么办）

1. **qn 命名与源码不一致（给空 + 报错）**：`trace_path(function_name="Runner.Run")` 与 `"suga.internal.personal.Runner.Run"` 都返回
   `{"error":"function not found","hint":"Use search_graph(name_pattern=...) to find the exact qualified name…"}`；索引里该方法的 qn 是 **`suga.internal.personal.Run`（receiver 被丢掉）**。T6-B-2 连试 3 次 `trace_path` 才走通；`search_graph(name_pattern=Runner\.Run)` 也不直接命中方法，而是回 `Runner` 结构体与字段。
2. **索引指向另一个检出**：索引的 file 列指向主检出 `D:/work/inhere/my-tools-dev/inhere-tools/suga`（main，模型读到 commit 是 `f3a9cd1`），而本次运行的工作区是 `.worktrees/b16x`（`1501566e`）。图里的**行号不能直接当答案**。
3. **漏报（静默少一条）**：严格过滤非测试文件的 `CALLS` 入边只回 5 行，**漏掉真实存在的** `internal/acpserver/server.go:286`；`trace_path` 能列出它，图与图之间也不完全一致。
4. **假阳性**：`internal/chat/local_cmd.go:94` 的 `s.local.Run(ctx, command, s.source)` 不是 `personal.Runner.Run`，是本地命令执行器。
5. **模型的处理方式（正面）**：T6-B-2 没有被误导——它在答案里单开一段“图查询的说明（避免误读）”，写明 qn 差异、索引指向主检出、`CALLS` 漏了 acpserver、以及假阳性的排除，并声明“上面的行号是我在本 worktree 里用 `file_grep` 逐个核对过的”。代价是：多次试错 + 1 次被拒的工具（`get_graph_schema`）+ 8 次输出转存（`spills=8`），最终 **18 步 / 33 次调用 / 551k 输入 token**，比 A 档的 12–13 步 / 18–26 次调用贵得多。

## 4. 结论

1. **没有任何一类任务在 B 档显著变好（阈值 ±3–5 步）**。T1（−2 步）、T2（−1 步）在噪声内；T3（**+2.5 步**、token ×2.0）、T4（**+8 步**、+14 次调用、墙钟 ×2.4、输入 token ×1.6）、T5（+1 步、**+8.5 次调用**、token ×1.6）、T6（**+6 步**、+10.5 次调用、token ×2.3）都是变慢或持平。
2. **慢在哪：工具面变大 + 使用率低 + 使用后的试错。** 24 次运行里只有 3 次真的用了 cbm；一旦用上，模型要先 `list_projects` 找项目、再用 `search_graph` 猜 qn、再 `trace_path`/`query_graph`，遇到 qn 不一致或权限拒绝还要多一个往返。对“结构性”问题（T1/T2/T3）图工具完全没被用到，纯粹的 schema 开销。
3. **图工具唯一明确的正面样本是 T6-B-2**：`CALLS` 入边把“谁调用了谁”一步拿到，模型用它做了交叉核对并主动写出图的漏报/误报。但同一任务的 A 档用 `file_grep` 也答对了（只是调用次数更多、token 更少）。也就是说**收益是“更省心的交叉验证”，不是“更少步数”**。
4. **建议：先做 `file_refs`（自建引用检索），不接入图工具。** 理由：
   - 实测把图工具**常驻**接入的代价（+17 个工具 schema、使用率 25%、T4/T6 步数与 token 明显上涨）大于收益；
   - 图工具的两个硬伤本次实测都有：索引指向别的工作区/commit（行号不可直接引用）、qn 命名与源码不一致（receiver 丢失），模型要为“索引正确性”付出额外核对步骤；
   - `file_refs`（设计已出、未实施）是内置、无外部进程、无索引维护、与 `file_symbols` 同一套描述与输出风格（一条“谁调用了 X”的描述线），按 §7 的证据（独立工具 `file_symbols` 被用 4/10，而 `file_read` 的可选参数 `outline` 被用 0/10）更可能被用上。
   - 若将来仍要接图工具，建议改成**按需延迟加载**（不是常驻全量）、把 `qn`/`section`/receiver 命名差异写进描述与提示、并要求索引与工作区 HEAD 对齐（本次索引是主检出）；同时给 `list_projects` 加“先确认项目名”的引导，否则模型容易用它翻页。
5. **附带观察（本批不处理）**：A 档 T4 为 16–22 步，明显低于报告 §7 复测的 32–35 步（同模型同档位）。两个批次之间夹着仓库其它改动与提示词复原差异，不能把这次下降归给任何单一改动，但说明 T4 这个任务的方差与批次敏感性都很高，结论只应采信方向一致的信号。

## 5. 局限

- 每档每任务只跑 2 次，单次噪声约 ±3–5 步；只采信方向一致、幅度超过噪声的信号（T4/B、T6/B 的劣化与“B 无显著收益”都满足）。
- 只跑只读档（`--read-only`）：有 shell 的档位（`tools.allow_write: true`）下模型是否更愿意用图工具没有数据。
- 索引是既有的、**指向主检出而非本 worktree** 的索引，且未重建；这既是本次“图工具给空/给错”的原因，也意味着“索引与工作区对齐时图工具是否更快”未被证伪。
- cbm 的 17 个工具里 freed 的只有 9 个（任务清单），`get_graph_schema` 被拒；放行更多只读检索工具（`check_index_coverage`/`detect_changes`）后行为可能不同。
- 答案正确性由本报告作者对照代码核对（含 24 份 stdout 全文），未做第三方复核。

## 6. 冲突与偏差记录

- 任务要求“`tools.auto_allow` 只放行只读检索工具”，但 `--read-only` 下真正生效的是 `tools.read_only_allow`（`internal/wiring/runtime.go:38-40`、`docs/usage/gofer-personal-agent.md:94`）。本次两个列表都写入同一组工具；只配 `auto_allow` 会让 MCP 调用全部被拒。
- 任务给出的放行清单未含 `get_graph_schema`（模型在 T6-B-2 调用了一次被拒，错误进入上下文并多花一步），也未含 `check_index_coverage`/`detect_changes`/`compare_graphs`。
