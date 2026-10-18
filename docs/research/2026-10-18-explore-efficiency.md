# 仓库探索效率实测（suga 第十五批调研）

> 状态：调研报告（非设计文档，不过文档校验器）
> 日期：2026-10-18
> 关联 issue：`suag-7ih4`（仓库探索的速度与准确性：更多优化手段）、`suag-5qb4`（辅助小模型：工具调用后整理输出、检查命令）
> 产物：`docs/design/2026-10-18-explore-efficiency-design.md`、`docs/plans/2026-10-18-explore-efficiency-plan.md`

## 1. 目的与方法

issue `suag-7ih4` 的证据是“同模型同档位对比中 suga 探索阶段 9–19 步、omp 4–5 步”，并列出 7 条候选手段。本报告先把“步数花在哪”用真实模型的实测钉住，再逐条评估候选手段的收益/成本，供设计取舍；同时为 `suag-5qb4`（辅助小模型）提供“是否值得做、做哪一半”的实测依据。

### 1.1 环境与隔离

| 项 | 取值 |
|---|---|
| 主机 | Windows（本 job 所在主机），无 pty、无 cgo |
| suga | 本 worktree 构建的 `cmd/suga`（基线 `ae085a9b`，v0.31.0） |
| SUGA_HOME | **每次运行一个隔离目录** `tmp/exp/run/<tag>/`，只复制真实 `config.yaml` 的非密钥配置；密钥经进程环境变量 `OPENCODE_API_KEY` / `CODEX_API_KEY` 传入，**不复制 `.env`、不打印值**；全部目录运行结束后删除 |
| 模型 | `opencode-go/deepseek-v4.1-flash`（thinking high，默认档）、`openai-codex/gpt-6-luna`（thinking medium，换模型档） |
| 其他 | 只读运行（`tools.allow_write: false`，不带 `--allow-write`）；`max_steps: 60`；工作目录 = 本 worktree |
| 计时 | 进程墙钟（含模型与工具时间），`suga trace <sid> --analyze` 取步数/工具分布/重读/token |

`command-code/deepseek-v4.1-flash` 原本是首选对照模型，但本机账号周额度用尽（HTTP 429，`rate_limit_error`，重置时间 2026-10-19），因此未纳入对比。

### 1.2 配置档

| 档 | 说明 |
|---|---|
| A | 默认档：只读、无 `tools.preferred` |
| B | A + `tools.preferred: [rg(command), xerj(command)]`（两项 `present=true, reason=found on PATH`；环境说明段从 904 涨到 1300 字符） |
| C | A + 换模型 `openai-codex/gpt-6-luna` |
| D | B + `tools.allow_write: true`（这样才有 `shell_exec`，才能真正执行 rg；提示词仍要求只读，运行后 `git status --short` 干净） |

`xerj` 只有名字被声明：本机 xerj server 未运行（`xerj autoindex map` → `endpoint unreachable: http://localhost:9200`），本仓库也没有已建索引，按任务要求**没有自行建大索引**，所以 xerj 在本轮只作为“被声明的优先工具”存在，没有真实检索能力。

### 1.3 任务与标准答案

| 任务 | 提问 | 标准答案（本报告作者用 rg 核对） |
|---|---|---|
| T1 | `suga_manifest` 在哪里注册、`section` 参数怎么处理 | `internal/wiring/runtime.go:503-516`（活运行时）+ `internal/wiring/manifest.go:409`（静态 `suga manifest`，source=nil）；`section` 取值 = manifest 顶层 JSON key（11 个），未知值报错并列出可用 key（`manifest_tool.go:93-100`） |
| T2 | 列出 `internal/config` 所有 `Read*` 导出函数及文件:行 | 18 个：agents.go:31、approval_rules.go:43、budget.go:49、fold.go:32、idle.go:32、maxsteps.go:22、mcp.go:11、mcp_cli.go:14、preferred_tools.go:22、providers.go:139、providers.go:519、retry.go:21、rules.go:18、settings_registry.go:557、settings_registry.go:590、snapshots.go:54、tasks.go:25、timeouts.go:43 |
| T3 | `session_search` 对中文/CJK 短词的处理 | 按词分流：`utf8.RuneCountInString(term) >= minMatchRunes(=3)` 走 FTS5 trigram MATCH，1–2 rune 词走 `text LIKE '%词%'`（`internal/personal/store/fts.go:72-73,144-192`；内存索引同型 `memfts.go:148-167`） |
| T4 | `--read-only/--ro` 到写工具被禁用的调用链 | `internal/cli/run.go:207`（`--ro` 是 cflag usage 元数据别名）→ `run.go:224-238` → `wiring.OpenCLI` → `runtime.go:331` `AllowWrite = (...) && !ReadOnly` → `native.BuildTools` 不注册 file_write/file_edit/file_remove/shell_exec |

## 2. 实测结果

11 次运行全部完成，答案**全部正确**（与上面标准答案一致，含文件:行；T1/T4 还给出了额外细节）。

| 任务 | 档 | 模型 | 步数（模型调用） | 工具调用 | 工具分布 | 墙钟 | 输入/输出 token | 重读 |
|---|---|---|---|---|---|---|---|---|
| T1 | A | deepseek-v4.1-flash | 10 | 17 | file_grep×9, file_read×8 | 75.4s | 171.9k / 4.9k | runtime.go ×3 |
| T1 | B | 同上 | **7** | 12 | file_grep×6, file_read×6 | **34.0s** | 107.9k / 4.2k | runtime.go ×2 |
| T1 | C | gpt-6-luna | 10 | 18 | file_read×10（**4 次失败**）, file_grep×6, file_find×1, file_list×1 | 49.2s | 140.1k / 2.5k | manifest.go ×2 |
| T1 | D | deepseek-v4.1-flash | **17** | 26 | file_grep×15, file_read×10, **幻觉工具 `read`×1（失败）** | 68.7s | **392k / 7.3k** | runtime.go ×3 |
| T2 | A | deepseek-v4.1-flash | 4 | 3 | file_grep×2, file_list×1 | 10.0s | 21.3k / 0.8k | – |
| T2 | B | 同上 | 4 | 6 | file_grep×5, file_list×1 | 34.5s | 31.2k / 1.6k | – |
| T3 | A | deepseek-v4.1-flash | 8 | 12 | file_grep×7, file_read×5 | 34.8s | 172.5k / 3.8k | – |
| T3 | B | 同上 | 5 | 7 | file_grep×5, file_read×2 | 27.1s | 114.4k / 2.8k | – |
| T3 | D | 同上 | 5 | 9 | file_read×5, file_grep×4 | 37.1s | 94k / 3.2k | – |
| T4 | A | deepseek-v4.1-flash | **31** | **50** | file_read×24, file_grep×23（1 失败）, file_find×2, file_list×1 | **153.8s** | **1.49M**（cache read 1.39M）/ 14.3k | **模块缓存 cflag/util.go×4、cflag.go×3**；runtime.go×3；cli/run.go×2 |
| T4 | B | 同上 | **36** | **57** | file_grep×29（1 失败）, file_read×26, file_find×1, file_list×1 | 175.6s | **1.85M**（cache read 1.71M）/ 14.3k | runtime.go×3、cflag.go×2、cflag/util.go×2 |

补充事实：

- **方差很大**：同一档（A/B 都是默认配置 + 一个纯提示差异）在 T1 上是 10 步 vs 7 步（B 更快），在 T4 上是 31 步 vs 36 步（B 更慢）；换成 D（多给 shell 能力）反而涨到 17 步。单次测量的噪声量级约 ±3–5 步，本报告只采信**方向一致**的信号（重复读、失败调用、依赖模块内读取），不采信 1–2 步的差异。
- **T1 档 B 的加速不是 rg 带来的**：B 与 A 的工具集完全相同（`tools_count=13`，无 `shell_exec`），B 只是环境说明多了 rg/xerj 两行；模型两次都没能执行任何 shell 命令（`shell 0 runs`）。
- **档 D 给了 shell，模型仍然一次没用 rg**（`shell 0 runs`，26 次工具调用全是内置 file_*）。也就是说“把 rg 写进环境说明”这个手段在当前模型上没有改变行为。
- 所有 11 次运行的 `mode` 都是 `read_only`，`tools_count=13`（`file_read/file_grep/file_find/file_list` + 只读类）。**只读运行没有 `shell_exec`**（`internal/wiring/runtime.go:456`：`Registry.Get("shell_exec")` 且 `allowWrite` 才加它），所以 A/B/C 三档根本无法走 `rg`/`go doc` 这条路。

## 3. 步数花在哪（浪费模式）

按 trace 事件逐条对照，浪费集中在四类，且都能对应到具体工具能力的缺失：

1. **大文件反复读、只能靠“再读一段”拼全貌。** T4-A 的 24 次 `file_read` 里有 12 次是重复读（`gookit/goutil@v0.8.3/cflag/util.go` ×4、`cflag.go` ×3、`internal/wiring/runtime.go` ×3、`internal/cli/run.go` ×2），T4-B 同样在 runtime.go ×3、cflag 两个文件 ×2；T1-A 的 runtime.go ×3、T1-C 的 manifest.go ×2 同理。`file_read` 一次只回一个窗口，模型为了拿到“文件里还有哪些声明/哪个函数在第几行”必须反复取窗口，步数与 token 同时放大（T4-A 输入 1.49M token、T4-B 1.85M，其中 1.39M/1.71M 是 cache read）。
2. **没有符号级检索，“列清单”类问题要靠猜正则。** T2 这种“列出包内所有 X 函数”的问题，模型先用 `file_grep` 试正则（A 用 3 次工具调用、B 用 6 次）再拼表格；T4 找 flag 解析链同样反复 grep。若有一条“包级声明清单（func/type/const/var + 行号）”的只读工具，这两类任务都是一次调用。
3. **grep 无法按“定义”或“非测试文件”收窄。** T1 用 `suga_manifest` 当 pattern，命中大量 `*_test.go`，模型要额外步骤排除（档 D 花了 15 次 grep）；T4-A 有一次 grep 因正则/范围不当失败。现在的 `file_grep` 只有 pattern/path/glob 语义，没有“声明级”“定义处”这种意图过滤。
4. **工具调用失败与幻觉调用吃掉步数。** T1-C 有 4 次 `file_read` 失败（参数/范围不对），T4-A 有 1 次 grep 失败，T1-D 有 1 次调用**不存在的工具 `read`**（返回 `tool read error`）。失败调用各占一步模型往返，而且错误信息本身又进入上下文。

另外两点与“优先工具”相关，属于**能力错配**而非浪费：

- B/D 的环境说明列出了 rg/xerj（904 → 1300 字符），但只读运行时没有 shell；档 D 有 shell 也不用。提示与能力不匹配，说明探索优化应优先落在**内置只读工具本身**（模型对 `file_*` 有强习惯），而不是指望外部命令。
- 依赖 API 查询（issue 里 E5 的观察）在 T4-A 复现：为了确认 `--ro` 别名，模型读了 7 次模块缓存文件。这一步本可以用“符号清单 + 一次 `go doc` 风格”的方式完成，而当前只读运行连 `go doc` 都不可用。

## 4. 候选手段逐条评估（对照 issue 7ih4）

| # | 候选手段 | 实测针对的浪费 | 收益 | 成本/风险 | 结论 |
|---|---|---|---|---|---|
| 1 | 文件结构摘要：不带范围读大文件时返回声明大纲 + 被省略的精确行段；一个 path 支持多段范围 | 浪费 1（T4-A/T4-B 各 12+ 次重复读、T1 runtime.go ×3） | **高**：直接砍掉重复读与“拼全貌”的往返，也顺带缓解 4（失败读少） | 中：改 `file_read` 的 schema 与返回格式（`pkg/agentcore/tools/native`），需保证省略标记不被模型误当作完整内容；不能破坏现有 offset/limit 调用 | **做**（手段 A） |
| 2 | 符号级检索（Go：声明/签名清单，行号） | 浪费 2（T2 猜正则、T4 找解析链）与 4（部分失败 grep） | **高**：T2/T4 类“列清单/找定义”一次调用拿到 | 中：本仓库纯 Go，用 `go/parser` 生成 package 级声明即可，**不引入 gopls、不建索引、不新增缓存**；跨语言（md/yaml/sh）不覆盖，需在描述里写清适用 Go | **做**（手段 B，限定 Go） |
| 3 | 仓库概览缓存（目录树 + 包职责摘要，按 git HEAD 缓存，注入段上下文） | 首次定位（本轮最大浪费不在这一步，T2/T3 的定位只花 1–2 步） | 中：省最初 1–3 步 | 中高：缓存失效规则、提示词膨胀、段一致性都要处理；与手段 1/2 有重叠 | **暂不做**：先做 1/2，落地后复测是否仍缺“首次概览” |
| 4 | 多 pattern grep、grep 默认带上下文行、按相关度排序 | 浪费 3（测试文件噪声、无意图过滤） | 中低：本轮 grep 都在 2–5 次内收敛；真正的痛点是“没有声明级过滤” | 低（多 pattern/上下文行）～中（相关度排序需要打分，容易被误当语义排序） | **只做最小形态**：随手段 B 一起提供“只回声明行”的模式；**不单独做**上下文行默认值与相关度排序（会改变既有 `file_grep` 语义与输出体积） |
| 5 | 只读工具并发执行 | 步内串行 | 低：当前已有临时并行（T1-A 8 个并行步/17 调用；T4-A 20 个并行步/50 调用，最大 2 并发），不是瓶颈 | 中：并发本身会带来审批/顺序语义问题 | **不做**：已有能力，重复建设 |
| 6 | 语义检索（jfind + judge 小模型） | “找相关代码”类问题 | 低（本轮 4 个任务全部是结构性问题，语义检索帮不上） | 高：要索引 + 额外模型调用 | **不做** |
| 7 | 度量：`trace --analyze` 统计探索阶段步数、读了未用的文件数 | 让优化可回归 | 中：步数/重读/并行/失败已有；`--analyze` 已能满足本报告的对比 | 低（步数类）～高（“读了没用”需要把工具结果与后续引用关联，当前事件流里没有这个信息） | **部分做**：沿用现有 `--analyze` 作为回归指标；“读而未用”不做 |

## 5. 辅助小模型（suag-5qb4）的实测判断

实测样本（11 次成功运行）里**没有** 5qb4 想解决的问题：

- **输出整理**：0 次 shell 调用、0 次构建/测试输出、0 次安装/抓取；工具结果全是 `file_read` 窗口和 `file_grep` 命中（唯一一次大输出由现有 spill 机制兜住：T3-A `spills 1`，已转存为可取回 ref）。也就是说本轮没有“噪声型大输出”样本，小模型摘要的收益**无法证伪也无法证实**；能确定的格式（溢出/折叠/截断）现有确定性机制已在做。
- **命令检查**：只读运行没有 shell（0 条命令），档 D 有 shell 但模型仍 0 条命令，因此**没有一条真实命令样本**可以评估误报/漏报；危险删除这类确定性风险已有 approval 规则与 `suag-dtom` 方向处理，用模型兜底会引入延迟与不稳定判断。

结论：**两半都暂不做**，理由分别是“无触发样本”和“无样本 + 更便宜的确定性规则已覆盖”；触发条件写成“当 shell 使用量出现（探索类任务里出现构建/测试/日志输出）后，再用同一套 trace 指标 A/B 复测”。详见设计文档“辅助小模型评估”。

## 6. 局限

- 每档每任务只跑 1 次，方差约 ±3 步；结论只对“方向一致的浪费模式”成立。
- 任务都是“只回答代码问题”的只读探索，未覆盖“改代码前必须先跑测试”的噪声场景，因此 5qb4 的结论只对本次样本成立（已在设计中写成触发条件）。
- 只读运行没有 shell，`tools.preferred` 的另一半（外部命令）在这批任务里未被真实触发；档 D 证明“给了能力也不会自动用”，但没证明“在需要大范围搜索时也不会用”。
- `xerj` 无索引、`command-code` 周额度用尽，两条外部对照（索引型检索、第三个模型）没有数据。
- “优先工具”这一半只测到“提示不改变行为”（档 D 有 shell 也不用 rg），没有测“需要大范围搜索时仍不用”，因此不能断言 rg 集成无价值。

## 7. 复测（v0.32.0）

本节是计划 T5 的复测：1–6 节的 4 个任务（T1–T4）在**同一模型、同一只读档位**下重跑，另外加了 1 个多语言（TypeScript）任务，用来看 `file_read` 大纲/多段范围读与 `file_symbols` 三个新能力是否真的被模型用上、是否带来收益。**只测档 A**（`openai-codex` 网关当时不可用，档 C 未跑）。

### 7.1 方法与基线的差异

| 项 | 复测取值 | 与基线的差异 |
|---|---|---|
| suga | 本 worktree 构建的 `cmd/suga`（HEAD `535d77a2`，v0.32.0，含 T1–T3 三个新能力） | 基线是 `ae085a9b`（v0.31.0） |
| SUGA_HOME | 每次运行一个隔离目录 `tmp/exp/run/<任务>-<次数>/`，只写入非密钥配置（`model`、`max_steps: 60`、`tools.allow_write: false`、`tools.approval: auto`、`providers.opencode-go` 的 `type/context_window/models/base_url/headers`）；密钥用 `api_key: "${OPENCODE_API_KEY}"` 从进程环境变量展开，**不复制 `.env`、不打印密钥值** | 同一做法；基线档 A 配置同型 |
| 模型 | `opencode-go/deepseek-v4.1-flash`（thinking high） | 同 |
| 任务 | T1–T4 提示词同基线口径（T1 为基线原文；T2 为基线 `tasks.json` 可见原文；T3/T4 按报告 1.3 节提问栏与同一句式复原，措辞可能有细微差异） | 复原而非字节级相同，见 7.5 局限 |
| 次数 | 每个任务 **2 次**（基线每档 1 次），10 次运行全部 `exit 0`、全部答案正确 | 复测样本加倍 |
| 新增任务 | M1：`D:/work/inhere/my-tools-dev/inhere-tools/gofer/web/src/api`（19 个 `.ts`，含 2 个测试文件）列出“所有导出的函数/类型及文件:行”，只读方式为 `--add-dir …:ro` | 基线没有跨语言任务 |
| 环境 | 3 个运行并发（每个独立 SUGA_HOME/会话） | 基线串行 |

指标口径：`步数 = model_calls`、`工具调用 = tool_calls`、token = `input_tokens/cache_read/output_tokens`、墙钟 = 进程墙钟；**重复读**给了两个口径：(a) `trace --analyze` 的 `re-reads`（同一 `target` 读多次，行段读的 target 含行段所以天然不重复）；(b) 本报告自己按 trace 事件统计的“同一文件被 `file_read` 读到多次”（与 3 节的基线口径一致，基线的“T4-A 24 次读里 12 次是重复读”就是这个口径）。

### 7.2 前后对比（档 A，deepseek-v4.1-flash，只读）

| 任务 | 基线 步数/调用/墙钟/token(in) | 复测 1 | 复测 2 | 复测均值 | 答案 |
|---|---|---|---|---|---|
| T1 `suga_manifest` 注册与 `section` | 10 / 17 / 75.4s / 171.9k | 10 / 17 / 55.6s / 161.4k | 13 / 27 / 71.7s / 287.0k | 11.5 / 22 / 63.7s / 224.2k | 2/2 正确 |
| T2 列出 `internal/config` 的 `Read*` | 4 / 3 / 10.0s / 21.3k | **2 / 2** / 8.4s / 15.8k | **3 / 4** / 10.8s / 25.5k | **2.5 / 3** / 9.6s / 20.7k | 2/2 正确（19 个，含 v0.32.0 新增的 `network_proxy.go:26`） |
| T3 `session_search` 的 CJK 短词 | 8 / 12 / 34.8s / 172.5k | 8 / 14 / 42.5s / 226.7k | 8 / 13 / 37.2s / 196.0k | 8 / 13.5 / 39.9s / 211.4k | 2/2 正确 |
| T4 `--read-only/--ro` 到写工具禁用 | **31 / 50 / 153.8s / 1.49M** | 35 / 56 / **293.8s** / 1.70M | 32 / 55 / 162.1s / 1.82M | **33.5 / 55.5 / 228.0s / 1.76M** | 2/2 正确 |
| M1 TS 目录导出清单（新增） | – | 9 / 35 / 115.6s / 228.3k | 10 / 31 / 118.8s / 279.2k | 9.5 / 33 / 117.2s / 253.8k | 2/2 正确（均报 623 条，与人工核对 622–623 条一致） |

工具分布与重复读（复测）：

| 运行 | 工具分布 | 新工具用法 | 重复读 (a) analyze | 重复读 (b) 同文件多次读 | 失败调用 |
|---|---|---|---|---|---|
| T1-1 | file_grep×10, file_read×7 | – | manifest.go×2, runtime.go×2 | manifest.go×2, runtime.go×2 | 0 |
| T1-2 | file_read×15, file_grep×10, file_list×1, **file_symbols×1** | symbols 1 次（`path=internal/wiring/manifest.go`） | manifest.go×2, runtime.go×2, 本报告×2 | 同上 | 0 |
| T2-1 | **file_symbols×1**, file_grep×1 | symbols 1 次（`path=internal/config`） | – | – | 0 |
| T2-2 | file_grep×2, **file_symbols×1**, file_list×1 | symbols 1 次 | – | – | 0 |
| T3-1 | file_grep×7, file_read×6, file_find×1 | 行段读 2 次 | – | – | 0 |
| T3-2 | file_read×8, file_grep×4, file_find×1 | – | ledger.go×2 | ledger.go×2 | 0 |
| T4-1 | file_read×30, file_grep×22, file_list×3, file_find×1 | **行段读 15 次**（含一次 4 段 `paths`），outline 0 | **无** | cflag.go×4, cflag/util.go×4, runtime.go×2（10/30 次读属于重复文件） | file_grep 1, file_read 2 |
| T4-2 | file_grep×32, file_read×18, file_list×4, file_find×1 | **行段读 12 次**（多次 2 段 `paths` 混读），outline 0 | **无** | runtime.go×4, cflag/util.go×2, adddir.go×2（8/27） | file_read 1 |
| M1-1 | file_grep×21, recall×12, **file_symbols×1**, file_list×1 | symbols 1 次（TS 目录） | – | – | 0 |
| M1-2 | file_grep×22, recall×5, file_read×2, file_list×1, file_find×1 | 未用 symbols / outline | types.ts×2 | types.ts×2 | 0 |

新能力总体使用率：`file_symbols` **4/10 次运行**（T2 2/2，M1 1/2，T1 1/2，T3/T4 0）；`file_read outline` **0/10**；行段范围读 **3/10**（T4 2/2 都是主力用户，T3 1 次）。

### 7.3 结论：计划目标达成情况

计划 T5 的两个量化目标（对照设计 4 节的手段 1/2）：

1. **“T4 类 30+ 步降到 20 步以内” —— 未达成。** 基线 T4-A 31 步，复测 35/32 步（均值 33.5，区间 32–35），仍在 30+ 档；工具调用 50 → 55/56；输入 token 1.49M → 1.70M/1.82M；墙钟 154s → 294s/162s（复测 1 次明显更慢）。多段范围读确实被模型大量使用（15/12 次调用，含一次 4 段混读），失败读从“基线 1 次 grep 失败”变成 2–3 次（新的行段语法写错时会整条调用失败），但**没有换来步数下降**。
2. **“重复读降到 1 次以内” —— 按 (b) 口径未达成，按 (a) 口径达成但含义变弱。** (a) `analyze` 的 re-reads 在 T4 两次都是 0（行段读的 target 带行号区间，同一文件的不同片段不再算重复），这是口径副作用，不能当成“重复读消失”；(b) 同一文件被 `file_read` 多次读的次数：T4-1 10/30、T4-2 8/27（基线 12/24），只是从“重复读同一个窗口”变成“重复读同一文件的不同片段”（例如 T4-1 把 `cflag.go` 取了 4 段、`cflag/util.go` 取了 4 段）。**模型拼全貌的行为没变，变的只是每次取多少行。**
3. **`file_symbols` 是这次唯一明确的收益。** T2 从 4 步/3–6 次调用降到 2–3 步/2–4 次调用（步数均值 -37.5%，token 21.3k → 20.7k 基本持平），而且 2/2 次运行都主动用了它，答案比基线更全（19 个 `Read*`，把 v0.32.0 新增的 `ReadNetworkProxy` 也算进来了）。M1 里 TS 目录的 `file_symbols` 输出正确（623 条导出，与人工核对的 622–623 条一致），说明跨语言可用。
4. **T1/T3 在噪声范围内（±3–5 步）没有变化**：T1 10/13（基线 10）、T3 8/8（基线 8）。T1 在 1/2 次运行用了 `file_symbols`，但没有真正变快。

所以：**手段 2（符号清单）达到预期，手段 1（大纲 + 多段范围读）的“省步数”预期没有达到**；多段范围读只减少了“同一窗口读两遍”，没有减少“为拼全貌而反复取片段”的往返。

### 7.4 模型为什么没按预期使用新工具（原因分析 + 建议，本任务不改代码）

**没有一次用 `outline`（0/10）。** 观察到的行为是：模型总是先 `file_grep` 定位、再 `file_read` 取窗口或行段，从没有先问“这个文件里有哪些声明”。

- 触发条件不可预判：`outline` 只对 >300 行的已声明语言文件生效，模型在**读之前不知道文件有多长**，也就没有理由为一个可能被忽略的参数做额外判断（T4 里它读的 `cflag.go`/`runtime.go` 都远超 300 行，仍然选择直接取窗口）。
- 收益链条太长：大纲只给声明行，不加代码，模型接下来仍要再读窗口；对“追调用链”类任务，大纲省下的那一步不足以打断“grep → 窗口”的既有习惯（这个习惯在训练数据和系统提示词里都被强化）。
- 提示面太弱：`outline` 是 `file_read` 的第 5 个可选参数（排在 path/paths/offset/limit 之后），描述里还带着“小文件、未知类型、带行段时回退窗口”的例外说明；稳定系统提示词（本批明确不改）里一个字都没提。模型只看 schema 时，这类“条件式可选优化”几乎不会被触发（对比 `file_symbols`：它是一件“只有它能做这件事”的独立工具，所以在“列清单”问题上立刻被用）。
- 建议（择一或组合，需另开一批）：
  1. 把大纲做成**默认**而非可选：未给 `offset/limit/spans` 且文件超过阈值时，`file_read` 直接返回“首行说明 + 大纲 + 取段提示”，模型要看正文时再带范围（副作用是改变现有窗口契约，须过设计 Gate）。
  2. 保留可选参数，但在**截断/分页提示**里带上钩子：当一次读被 `read_page_bytes` 截断时，返回行里加“本文件共 N 行、M 个声明，可用 `outline=true` 取清单，或用 `path:start-end` 取指定段”——让模型在被截断的那一刻看到这个能力。
  3. 在稳定系统提示词里加一句（与本批“不改提示词”的边界冲突，需评估 prompt 缓存代价）：“读大文件先取声明大纲，再按行段精读”。
  4. 给 `file_symbols` 的目录模式补一句“先用它取某包/目录的全部声明，再读实现”，把 T4 类任务的第一步从 grep 改成 symbols（但 T4 真正的痛点是“谁调用了谁”，声明清单帮不上，见下）。

**`file_symbols` 在追调用链任务里 0/2 使用。** 原因不是描述问题，而是能力边界：T4 问的是“选项如何一路生效”，需要的是引用/调用关系与实现体，`file_symbols` 只列声明位置，给不出“谁用了 `ReadOnly`”。要覆盖这类任务，缺的是“符号的引用位置”（设计里明确排除的 gopls/LSP 或索引方向），本次数据支持“先不做”；但可以在描述里更明确地写“用于列出某文件/目录的全部声明（func/type/const/var），不列调用关系”，避免模型在追调用链时误用。

另外两条与工具描述无关、但值得记进 issue 的观测：

- **模型仍会去读模块缓存拼依赖 API**：T4 两次分别在 `cflag.go`/`cflag/util.go` 上取了 4+4 段和 4+2 段。只读运行没有 shell、没有 `go doc`，`file_symbols` 对“这个依赖包有哪些声明”其实正好合适（它支持目录递归），但模型没想到对 `D:\env\gopath\pkg\mod\…` 用符号清单，而是一次次取行段。若要做下一步优化，这是比“再改一次 file_read”更值钱的方向（例如把 `file_symbols` 写进只读运行的环境说明，或让 `file_read` 在读到依赖根下的大文件时提示符号清单）。
- **隔离 SUGA_HOME 下 `recall` 空转**：M1 两次分别调了 12 次和 5 次 `recall`（记忆检索），在没有记忆的隔离 home 里全是空转（约 1 步、几万 token 的代价）。这是本批复测新暴露的行为，与本批两个新工具无关，记入发现但不在本任务处理。

### 7.5 局限

- T3/T4 的提示词是按报告提问栏复原的（T1/T2 有原文），措辞差异会给步数带来 ±1–2 步级别的噪声；结论只采信方向一致、幅度超过 ±3–5 步的信号（T2 的 37.5% 与 T4 的“仍 30+ 步”都远超噪声）。
- 只跑了档 A（每任务 2 次），未测档 B/D（外部命令）与档 C（网关不可用）；新工具在“有 shell”的档位下是否仍被使用没有数据。
- M1 的 TS 目录含 `types.ts`（288 条导出）这类超大文件，答案的正确性用“总条数 + 抽查逐文件条数”核对，没有逐条比对 623 行。
- 复测与基线之间还夹着 v0.32.0 的其他改动（例如新增 `internal/config/network_proxy.go` 改变了 T2 的标准答案、`file_read` 之外的工具描述也有微调），因此步数差不能全部归给本批三个新能力。

## 8. 引用检索后复测（file_refs，第十六批）

本节是 `docs/plans/2026-10-18-symbol-refs-plan.md` T5 的复测：内置只读工具 `file_refs`（“谁调用了 X / X 在哪被用”，Go 精确分类 `def/call/sel/ref`、其它语言词边界近似）落地后，用 `docs/research/2026-10-18-explore-compare.md` §1.3 的 **T4、T5、T6** 三个任务重跑。这三个任务正好覆盖“追调用链”（T4）与“找调用方”（T5/T6），也是 §7 与第十六批对比报告里都没有改善的一类。

### 8.1 方法与口径

| 项 | 取值 |
|---|---|
| suga | 本 worktree 构建的 `cmd/suga`（`cba48427` + 本批 T4 冒烟脚本，含 T1–T3 的 `file_refs`） |
| SUGA_HOME | 每次运行一个隔离目录 `tmp/exp-refs/run/<tag>/home/`，只写入非密钥配置（`model: opencode-go/deepseek-v4.1-flash`、`max_steps: 60`、`tools.allow_write: false`、`tools.approval: auto`、`memory.project.backend: files`、provider 的 `type/context_window/base_url/headers/models`）；密钥用 `api_key: "${OPENCODE_API_KEY}"` 从进程环境变量展开，**不复制 `.env`、不打印密钥值**；运行产物（二进制、隔离 home、答案与 trace）在本批结束删除 |
| 模型 | `opencode-go/deepseek-v4.1-flash`（thinking high） |
| 运行方式 | `suga run --read-only --session-id <tag> -p <prompt>`，工作目录 = 本 worktree，3 并发，6 次运行 6/6 `exit 0` |
| 任务 | explore-compare §1.3 的 T4（`--read-only` 到写工具被禁用的调用链）、T5（`ReadModelSettings` 的调用方）、T6（`personal.Runner.Run` 的直接调用方），提问原文照抄 |
| 口径 | 与 explore-compare A 档一致：只读、不接 codebase-memory-mcp、无 `tools.preferred`；步数 = `trace` 的 model 事件数，工具调用 = tool 事件数，token = `input_tokens`，墙钟 = 进程墙钟 |

### 8.2 前后对比（A 档口径，deepseek-v4.1-flash，只读）

| 任务 | 参照基线（explore-compare A 档，2 次） | 复测 1 | 复测 2 | 复测均值 | 均值差（步数） | 答案 |
|---|---|---|---|---|---|---|
| T4 `--read-only` 调用链 | 19.0 步 / 36.5 调用 / 162.7s / 909.9k | 26 / 48 / 161.1s / 1.31M | 31 / 57 / 204.0s / 1.73M | **28.5** / 52.5 / 182.6s / 1.52M | **+9.5 步**，调用 +16.0 | 2/2 正确 |
| T5 `ReadModelSettings` 调用方 | 8.0 / 17.5 / 74.5s / 131.7k | 7 / 21 / 76.9s / 115.9k | 11 / 18 / 52.4s / 162.0k | 9.0 / 19.5 / 64.7s / 138.9k | +1.0（噪声内） | 2/2 正确 |
| T6 `Runner.Run` 调用方 | 12.5 / 22.0 / 104.5s / 258.1k | 13 / 24 / 58.6s / 283.9k | 18 / 43 / 83.8s / 466.3k | 15.5 / 33.5 / 71.2s / **375.1k** | **+3.0 步**，调用 +11.5 | 2/2 正确 |
| §7 omp 参照（同模型，步数/调用） | – | – | – | T4 23.0 / 33.0；T5 6.5 / 7.5；T6 9.5 / 14.5 | – | 抽查一致 |

工具分布与新工具使用（`file_refs` 计首次出现的步 = 该调用之前已发生的 model 事件数）：

| 运行 | 工具分布 | `file_refs` 次数 | 首次出现 | 失败调用 |
|---|---|---|---|---|
| T4-1 | file_read×24, file_grep×21, file_list×3 | **0** | – | file_read 1 |
| T4-2 | file_read×29, file_grep×20, file_list×6, file_symbols×1, file_find×1 | **0** | – | file_read 2 |
| T5-1 | file_grep×9, file_symbols×7, file_read×3, **file_refs×2** | 2 | 第 1 步 | 0 |
| T5-2 | file_grep×6, file_symbols×6, file_read×3, **file_refs×3** | 3 | 第 1 步 | 0 |
| T6-1 | file_grep×9, **file_refs×7**, file_read×5, file_symbols×3 | 7 | 第 1 步 | file_read 1 |
| T6-2 | file_grep×18, **file_refs×12**, file_read×10, file_symbols×2, session_search×1 | 12 | 第 2 步 | 0 |

**`file_refs` 使用率：4/6 次运行（67%）**，且在 T5/T6 上是**第 1–2 步**就被叫出来；T4 两次都是 0。答案正确性：6/6 与标准答案的关键文件:行一致（T5 两次都列出生产代码 5 处并把 `providers.go:556` 的定义与大批测试调用分开说明；T6 两次都恰好列出 5 个直接调用方，没有把 `internal/chat/local_cmd.go:94` 的 `s.local.Run` 算进去）。

### 8.3 结论

1. **“独立工具更容易被用上”再次成立。** `file_refs` 在它覆盖的第 1 步就被叫出来（T5 2/2、T6 2/2），和 §7 的对比一致（独立工具 `file_symbols` 4/10，而 `file_read` 的可选参数 `outline` 0/10）。追调用链任务上 omp 的 `grep -rn … | grep -v _test.go` 管道，现在有一次调用的内置对应物。
2. **但步数没有下降，token 与调用数反而上升。** T5 +1 步（噪声内）、T6 +3 步 / 调用 +11.5 / 输入 token +45%、T4 +9.5 步（且 `file_refs` 0/2）。三个任务都没有出现超过噪声（±3–5 步）的改善。
3. **T4 是最差的一项，与 §7.4 的判断一致。** 模型仍以 file_grep + file_read 为主（两次运行 48/57 次调用里 41/49 是这两件），一次都没想到用 `file_refs` 查 `ReadOnly`/`allowWrite` 的引用位置。任务的问法是“这个选项如何一路生效”，模型的第一步选择没有因为新工具而改变。
4. **`file_symbols` 与 `file_refs` 在 T5/T6 是被一起用的**（先 symbols 找定义或清单、再 refs 找使用点），这是设计预期的分工；`file_refs` 的结果里 Go 的 `def/call/sel/ref` 分类与命中行原文，是模型能把同名假阳性排除掉的依据（本批无法把这份“排除”归因到工具，但两次答案都没有出现第十六批 B 档图工具那种误报）。

### 8.4 按计划 Gate 的处理与建议

本批任务的验收口径是“T4 类任务步数下降、`file_refs` 至少被用上”：

- “至少被用上”：**达成**（T5/T6 四次运行全部使用，T4 两次为 0；总体 4/6）。
- “步数下降”：**未达成**（T5 在噪声内持平，T6 +3，T4 +9.5）。设计里的目标“T4 类 30+ 步降到 20 步内”按本批复测的 26/31 步仍不成立。

按计划“回滚与恢复”与 Gate C：

1. **不回滚 T3 的注册。** 新工具是被主动用上的（第 1 步），6/6 答案正确，且没有“误报把模型带偏”的证据；把它从只读运行里去掉，只会让 T5/T6 回到“多次 grep + 过滤测试文件”的老做法（omp 参照里这两项调用数并不高，正因为它的 shell 管道能做过滤）。
2. **要动的是提示面，而不是工具本体**（与 §7.4 同向，且本批明确不改稳定提示词）：让 `file_refs` 在“追选项生效链”类任务上被想起，需要在 `file_grep`/`file_read` 的截断或结果提示里挂钩（例如同一名字在一处命中多次或结果被截断时提示“按引用检索用 `file_refs`”）。这属于设计 Q5/Q6 的范围，需另开一批并过设计 Gate。
3. **T4 的方差大，不应作为验收指标。** 同一任务的 A 档口径在三批之间是 16–22（explore-compare）、32–35（§7 复测）、26–31（本批）。要衡量提示面批的效果，应改用在 T5/T6 上更稳定的指标（工具调用数、输入 token），而不是 T4 的步数。

### 8.5 局限

- 每任务只跑 2 次，单次噪声 ±3–5 步（口径同 §7 与 explore-compare）；只采信方向一致的信号。
- 与 explore-compare A 档不同批：两次之间仓库多了 `file_refs`（T1–T3）与冒烟脚本改动。T5/T6 的标准答案行号本 worktree 与 compare 报告一致（用 grep 复核过）；T4 的答案用“关键文件:行集合”（`run.go:207` → `OpenCLI` → `runtime.go:331` → `BuildTools`，另含 `catalog.go` 跳过非只读工具）核对，未逐字节比对。
- 只跑只读档：有 shell 的档位下 `file_refs` 是否仍被使用没有数据。
- 只在本 worktree 的仓库上测：`file_refs` 的误报率与大型仓库上的扫描成本没有测量（T3 只有单测里的性能断言）。
- 运行产物已删除，报告数字来自 `tmp/exp-refs/results.json`（临时文件，本次一并删除）与 `suga trace <sid> --json` 的事件统计；未做第三方复核。

## 9. 第十七批复测（file_refs 限定名与结果提示）

本节是 `feat/refs-qualified` 分支（v0.33.0 + 4 个提交：`file_refs` 限定名过滤、被丢弃限定符点名、Go 结论提示）的复测，任务是 explore-compare §1.3 的 **T5**（`appconfig.ReadModelSettings` 的调用方）与 **T6**（`personal.Runner.Run` 的直接调用方），各跑 **3 次**。选这两个任务的理由：§8 里它们是 `file_refs` 唯一被主动使用、且工具调用数与输入 token 明显高于 omp 参照的两项（T5 19.5 vs 7.5、T6 33.5 vs 14.5）。

### 9.1 方法与口径

| 项 | 取值 |
|---|---|
| suga | 本 worktree 构建的 `cmd/suga`（HEAD `1030139b`，v0.33.0 + 限定名过滤 + 被丢弃限定符点名 + Go 结论提示） |
| SUGA_HOME | 每次运行一个隔离目录 `tmp/b17q/run/<tag>/home/`，只写入非密钥配置（`model: opencode-go/deepseek-v4.1-flash`、`max_steps: 60`、`tools.allow_write: false`、`tools.approval: auto`、`memory.project.backend: files`、provider 的 `type/context_window/base_url/headers/models`）；密钥用 `api_key: "${OPENCODE_API_KEY}"` 从进程环境变量展开，**不复制 `.env`、不打印密钥值**；运行产物在本批结束删除 |
| 模型 | `opencode-go/deepseek-v4.1-flash`（thinking high），与 §8、explore-compare、omp 参照同一模型 |
| 运行方式 | `suga run --read-only --session-id <tag> -p <提问>`，工作目录 = 本 worktree，3 并发，6 次运行 6/6 `exit 0`；6/6 的 `turn_context` 都是 `mode=read_only`、`tools_count=15`、无 `preferred_tools`，`turn` 事件 `status=completed` |
| 任务 | explore-compare §1.3 的 T5、T6，提问原文照抄（每任务 3 次） |
| 口径 | 与 §8 一致：只读档 A（不接 codebase-memory-mcp、无 `tools.preferred`）；步数 = `model` 事件数，工具调用 = `tool` 事件数，token = `input_tokens` 之和（另给 cache read），墙钟 = 进程墙钟。`file_refs` 首次出现的“第 N 步”指该调用所在的回合序号（= 调用之前已发生的 `model` 事件数 + 1；§8 写的是不加 1 的旧口径，两者只差 1、结论相同） |

工具参数与结果取自 `suga export <sid>`（含 `ToolCall.Arguments` 与工具结果正文），事件计数取自 `suga trace <sid> --json` 交叉核对。

### 9.2 对比表（A 档口径，deepseek-v4.1-flash，只读）

| 任务 | 指标 | explore-compare A（2 次） | §8 v0.33.0（2 次） | **§9 本批（3 次）** | 本批区间 | omp（2 次） |
|---|---|---|---|---|---|---|
| T5 `ReadModelSettings` 调用方 | 步数 | 8.0 | 9.0 | **8.0** | 6–11 | 6.5 |
| | 工具调用 | 17.5 | 19.5 | **23.3** | 19–26 | 7.5 |
| | 输入 token | 131.7k | 138.9k | **129.9k** | 100.3–178.7k | – |
| | 墙钟 | 74.5s | 64.7s | **48.3s** | 41.3–62.0s | – |
| | 答案正确 | 2/2 | 2/2 | **3/3** | – | 抽查一致 |
| T6 `Runner.Run` 调用方 | 步数 | 12.5 | 15.5 | **16.3** | 12–22 | 9.5 |
| | 工具调用 | 22.0 | 33.5 | **42.7** | 26–74 | 14.5 |
| | 输入 token | 258.1k | 375.1k | **457.7k** | 253.9–755.5k | – |
| | 墙钟 | 104.5s | 71.2s | **101.4s** | 55.3–156.9s | – |
| | 答案正确 | 2/2 | 2/2 | **2/3** | – | 抽查一致 |

与 §8 的均值差：T5 步数 **-1.0**（噪声内）、调用 **+3.8**、token **-9.0k**、墙钟 -16.4s；T6 步数 **+0.8**、调用 **+9.2**、token **+82.6k（+22%）**、墙钟 **+30.2s**。**没有任何一项达到“超过噪声（±3–5 步）的改善”**；T6 的工具调用、token、墙钟都创了三个批次里的新高。

逐次明细（token 为输入/输出，calls 为工具调用数）：

| 运行 | 步数 | 工具调用 | 输入 / cache read / 输出 token | 墙钟 | 工具分布 | 工具错误 | 答案 |
|---|---|---|---|---|---|---|---|
| T5-1 | 7 | 19 | 110.8k / 89.5k / 5.6k | 41.7s | file_symbols×9, file_refs×6, file_read×3, file_grep×1 | 0 | ✔ 5 处生产调用 |
| T5-2 | 6 | 26 | 100.3k / 76.8k / 5.0k | 62.0s | file_symbols×12, file_read×10, file_grep×3, file_refs×1 | 0 | ✔ 5 处 |
| T5-3 | 11 | 25 | 178.7k / 161.7k / 4.6k | 41.3s | file_symbols×9, file_grep×6, file_read×6, file_refs×4 | 0 | ✔ 5 处 |
| T6-1 | 12 | 26 | 253.9k / 228.5k / 4.4k | 55.3s | file_refs×9, file_read×8, file_grep×6, file_symbols×3 | 0 | ✘ **只列 4 处**（漏 `personal/subagent.go:94`） |
| T6-2 | 15 | 28 | 363.7k / 331.8k / 7.1k | 92.1s | file_grep×13, file_read×7, file_refs×6, file_symbols×2 | 0 | ✔ 5 处（含 `subagent.go:94`） |
| T6-3 | 22 | 74 | 755.5k / 702.2k / 16.5k | 156.9s | file_grep×56, file_read×13, file_refs×2, file_symbols×1, file_list×1, session_search×1 | 0 | ✔ 5 处（含 `subagent.go:94`） |

### 9.3 `file_refs` 与多段读的使用统计

| 运行 | `file_refs` 次数 | 首次出现 | 用了限定名 | 出现“去掉 N 条”提示 | 之后仍做的 file_grep / file_read | 多段/多文件读 | outline | 同一文件被读多次 |
|---|---|---|---|---|---|---|---|---|
| T5-1 | 6 | 第 2 步 | 0（全部无限定） | 否 | 1 / 3 | **3 次**（5、3、3 段） | 0 | – |
| T5-2 | 1 | 第 2 步 | 0 | 否 | 3 / 10 | **1 次**（同 path 两段 `515-532,640-660`） | 0 | – |
| T5-3 | 4 | 第 2 步 | 0 | 否 | 6 / 6 | 0 | 0 | settings_registry.go×2 |
| T6-1 | 9 | 第 2 步 | **2**（`Runner.Run`、`acpserver.Serve`） | 是 | 6 / 8 | 0 | 0 | – |
| T6-2 | 6 | **第 9 步** | 0 | 否 | 3 / 4 | 0 | 0 | server.go×2、cli/run.go×2 |
| T6-3 | 2 | 第 3 步 | **1**（`Runner.Run`） | 是 | **55 / 13** | 0 | 1 | – |

- **`file_refs` 使用率 6/6（100%）**，高于 §8 的 4/6；T5 三次都在第 2 步、T6-1 第 2 步、T6-3 第 3 步，T6-2 拖到第 9 步（前 8 步 13 次 `file_grep` 自己在猜正则：`\.Run\(`、`[^t]\.Run\(`、`^[^t\n]*[^t]\.Run\(`、`Run\(ctx context.Context, req (personal\.)?RunRequest\)` 等）。
- **限定名只出现 2/6 次运行**，且**全部在 T6**；T5 三次都直接用无限定的 `ReadModelSettings`（T5-2/T5-3 还额外加了 `include_tests: true` 一次取全部）。也就是说这次新增的限定名能力在 T5 上**完全没有被触发**。
- **结果提示没有改变行为**：Go 结论提示写着“no file_grep needed to fill in”，T6-1 拿到限定结果后仍做 6 次 grep + 8 次读，T6-3 在第 3 步拿到 `Runner.Run` 限定结果后做了 **55 次 file_grep**。
- **多段读仍是少数用法**：只有 T5-1（3 次，最多一次 5 段跨 5 个文件）与 T5-2（1 次）用了 `paths`/同 path 多段，其余 4 次运行的 `file_read` 全是单段。
- **`outline` 只被用 1 次**（T6-3 读 `runner.go`），与 §7 的 0/10 同量级。
- 6/6 次运行的**工具错误为 0**（§8 有 1 次失败的 `file_read`），这一项比 §8 干净。

### 9.4 答案正确性与一次由限定名带来的漏报

- **T5：3/3 正确**，三次都给出 `internal/config/providers.go:556` 的定义与 5 处生产调用（`cli/models.go:27`、`wiring/manifest.go:480`、`wiring/runtime.go:760`、`config/settings_registry.go:523`、`:649`），与标准答案一致；三次都额外把测试里的调用点逐文件列了一遍（见 9.7 候选 4）。
- **T6：2/3 正确**。T6-2、T6-3 都恰好列出 5 个直接调用方，并主动排除了 `internal/chat/local_cmd.go:94` 的 `s.local.Run`（`local.Run` 的无限定结果里带着这一条）；**T6-1 只列了 4 处，漏掉 `internal/personal/subagent.go:94` 的 `child.Run`**，并明确写了“除此之外没有别的生产调用方……子 agent 等路径不经过 `Runner.Run`”。
  - 这一漏报正是限定名的已知副作用：T6-1 第一步用的就是 `file_refs {name: "Runner.Run"}`，结果里 `child.Run` 因为限定符是变量 `child` 而被丢弃，而结果尾部的提示已经点名了它（`dropped 58 use(s) … (variables: child 1, cmd 7, …; 1 through imported packages; 10 unqualified or text matches) - a variable among them may still refer to Runner, so check the names you do not recognise, or search without the qualifier for all of them`）。模型没有跟进这条提示，反而用 `file_grep "personal\.RunRequest{"` 自我确认，而 `subagent.go:94` 的 `child.Run(runCtx, RunRequest{…})` 正好不在这个 grep 的命中集合里（它是 `RunRequest{`，与 `personal.` 前缀无关）。对照组 T6-2、T6-3（一次无限定 `Run`、一次先无限定再限定）都答对了。
  - 结论：**“被丢弃限定符点名”这条提示目前不足以让模型回头补一次无限定检索**，而它恰好是限定名唯一的正确性保险。

### 9.5 结论（含负面结果）

1. **工具被用上：达成且比 §8 更稳**（6/6 次运行、其中 5 次在第 2–3 步），答案 5/6 正确，0 次工具错误。
2. **步数/token 未改善，T6 反而更差**：T5 只是调用数涨了（+3.8）、token 微降；T6 三项主指标全面上升（调用 +9.2、token +22%、墙钟 +30s），并且出现本批最差单次（T6-3：22 步 / 74 调用 / 755k 输入 token，其中 56 次是 `file_grep`）。按 §8.4 的建议改用“工具调用数、输入 token”作为指标后，结论更清楚：**本批没有缩小与 omp（T5 7.5、T6 14.5 调用）的差距，T6 反而拉开了**。
3. **限定名能力：用得少（2/6），没有收益，且带来一次漏报。** 它唯一被触发的场景是 T6 的“按类型找调用方”，而两次触发里一次（T6-1）直接漏掉了标准答案的 1/5；T5 这类“按包限定找调用方”的问题模型根本没用限定名（因为解析不到包名关系：T5-1 还专门 grep 求证 `appconfig` 是 `internal/config` 的别名）。
4. **结果提示（Go 结论 + 丢弃名单）没有改变行为**：拿到了“不需要再 grep 补齐”的结论提示后，模型仍在 T6-1/T6-3 靠 grep 复核（6 次、55 次）。
5. **多段读与 outline 仍是被动能力**：多段读只在 T5 的两次运行出现（方向正确：T5 的问题正好是“一次看 5 个调用点”），outline 只 1 次；都还不足以改变“逐文件、逐窗口读”的主体行为。
6. **T6 的方差被进一步放大**：12/15/22 步、26/28/74 调用，单次噪声远超 ±3–5 步；用它当验收指标不可靠（与 §8.4 第 3 条一致），T5 是更稳定的观察面。

### 9.6 局限

- 每任务 3 次，单次噪声 ±3–5 步（T6 实测 12–22 步）；只采信方向一致的信号。
- 只测了档 A（只读、无 `tools.preferred`），未测有 shell 的档位；只测了一个模型（`opencode-go/deepseek-v4.1-flash`）。
- 与 §8 相隔的 4 个提交里除了限定名过滤还有“被丢弃限定符点名”和“Go 结论提示”两条结果文案改动，步数/调用数的变化不能只归给限定名本身。
- T5/T6 的提问沿用 explore-compare §1.3 原文；标准答案用 `git grep` 在本 worktree 复核（`personal/subagent.go:94` 的 `child.Run(runCtx, RunRequest{…})` 等行号与 §8 一致）。
- 运行产物（二进制、隔离 home、会话库、答案与 trace 导出）已删除，数字来自本批的 `analyze-raw.json`/`seq.txt`（临时文件，一并删除）与 `suga trace --json`；未做第三方复核。

### 9.7 提示词调整候选（只写候选，不改代码与提示词）

按 trace 里“模型实际在哪多走了步”的证据列 4 条，均为**候选**，收益与风险都按本批数据估计；本任务不改任何代码或提示词。

1. **稳定系统提示词的只读/探索说明：给“找调用方/使用点”定一个默认第一步。**
   - 改哪段：稳定系统提示词里只读工具的使用说明（现在是通用描述，没有点名 `file_refs`）。
   - 证据：T6-2 前 8 步 13 次 `file_grep` 全是在猜 `.Run(` 的正则（`[^t]\.Run\(`、`^\s*[^t\n]*[^t]\.Run\(`），第 9 步才用 `file_refs`；T6-3 第 3 步拿到限定结果后又补了 55 次 grep。omp 在同一任务上一共 14.5 次调用，靠的是 `grep -rn … | grep -v _test.go` 一条管道。
   - 预期影响：T6 的工具调用从 42.7 往 omp 的 14.5 方向收敛，步数同步下降；T5（已经是第 2 步就用）估计不变。
   - 风险：稳定系统提示词改动会失效整段 prompt 缓存（每个会话一次重建），且“先 refs 后 grep”的硬性表述可能让模型在 `file_refs` 覆盖不到的语言/场景（md/yaml/sh）上白跑一次调用。
2. **`file_refs` 的限定名结果：把“掉落项”从说明升级为必须回跑的指令，并写清“问的是全部调用方就别用限定名”。**
   - 改哪段：`file_refs` 结果尾部的“dropped …”提示文字（`refs_drops.go` 的 `describe()` 与 Go 结论提示）。
   - 证据：T6-1 用 `name: "Runner.Run"` 时结果已列出 `variables: child 1`，模型仍回答“只有 4 处、子 agent 不经过 `Runner.Run`”；同批 T6-2/T6-3 用了无限定或先无限定的搜索都答对 5 处。这是本批唯一的正确性回归。
   - 预期影响：T6 正确率 2/3 → 3/3，代价是限定查询后多一次无限定 `file_refs`（约 1 次调用）。
   - 风险：提示如果写成“必须再搜一次”，会削弱限定名的价值（用户/模型明确想收窄时也强制回跑），并可能诱发“限定 + 无限定”两次大扫描（每次扫描是全仓库解析，T6-1 的 9 次 `file_refs` 已经说明多次扫描的 token 代价不小）。
3. **`file_read` 的描述：把 `paths` 多文件/多段读放成首选用法（并给一条跨文件例子），outline 的触发条件写具体。**
   - 改哪段：`file_read` 工具描述（可选参数 `paths` / `outline` 的说明与排列）。
   - 证据：本批只有 T5-1（3 次，一次 5 段跨 5 文件）与 T5-2 用了多段读，其余 4 次运行全是单段；T5-3 把 `internal/config/settings_registry.go` 读了两次，T6-2 把 `acpserver/server.go`、`cli/run.go` 各读 2 次。`outline` 只 1 次（§7 是 0/10），说明“可选优化参数”依旧不被触发。
   - 预期影响：把“同一问题涉及 3–5 个文件”的场景（T5 就是）压到 1–2 次调用，重复读进一步下降。
   - 风险：描述变长占 schema token；模型可能滥用多段读（一次要 8–10 段）导致单次结果过大触发 spill，反而多一次取回；`outline` 若默认化会改变现有窗口契约（§7.4 已列为需过设计 Gate 的方案）。
4. **`file_symbols`（目录模式）与探索收尾说明：不要为“测试里的调用点”逐文件建清单。**
   - 改哪段：`file_symbols` 目录模式的描述/结果提示，或稳定提示词里“回答层级”的一句（只答被问的范围，测试调用点用一次 `file_refs include_tests: true` 说明数量即可，不必逐文件列）。
   - 证据：T5 三次运行都在第 1–2 步就拿到了 5 处生产调用方，随后把 `internal/config/*_test.go`、`internal/wiring/providers_test.go` 等逐个 `file_symbols`（T5-1 共 9 次、其中一轮 7 个；T5-2 共 12 次、全在一轮）再配对 `file_read` 展开（T5-2 的 10 次 `file_read` 里 5 次读的是测试文件），最终答案里的测试清单对提问（“列出调用方函数及文件:行”）不是必需；这部分占了 T5-1 的 3/7 步、T5-2 的 4/6 步、T5-3 的 7/11（第 3–9 步）。
   - 预期影响：T5 步数 6–11 降到 3–5，token 从 130k 降到 60k 量级；T5 是本批唯一稳定的任务，收益可直接回归验证。
   - 风险：会抑制“顺带列出测试调用”的有用补充，可能被用户视为答案不完整；与“全面回答”的倾向冲突，需要先确认这是期望行为（这属于答案口径的产品决定，不只是提示词措辞）。

## 10. 提示词批复测（第十七批提示词调整）

本节是 §9.7 三条候选落地后的复测：任务、模型、档位、口径与 §9 完全相同（T5、T6 各 3 次），用来回答“这三句话是否改变了模型行为、是否带来收益”。

### 10.1 改了什么（三处，各一个提交）

| 提交 | 位置 | 改动 |
|---|---|---|
| `7a1fce2` | `internal/wiring/prompt.go`「Working conventions」，紧跟 “Put independent reads…” | 加一句：问“谁调用了 X / X 在哪被用 / X 的调用方”时第一步用 `file_refs`，不要先猜 `file_grep` 正则；Go 的结果已列出全部出现位置，接着读需要的那几处即可，不要再用 `file_grep` 补漏或复核；`grep` 仍用于非标识符文本（字符串、配置键、注释） |
| `f9ba14bd` | `pkg/agentcore/tools/native/file_read.go` 的 `Description()` | 把 `paths` 写成“一次看多个位置时的首选用法”并给跨文件例子 `paths: ["a.go:10-30", "b.go:40-60,90-110"]`；`outline` 的触发条件写成“只需要知道一个大文件里有哪些声明时”。描述 +14.6%（604 → 692 字符），schema 字段与工具行为不变 |
| `aefc5968` | `internal/wiring/prompt.go` 同上 | 加一句：只回答被问的范围；问调用方/使用点时默认只列生产代码，测试里的使用只给数量（如 “另有 N 处在测试中”），除非用户问到测试 |

三句都在稳定系统提示词（`basePrompt`，只读与普通模式共用）里，没有做只读专属分支。

### 10.2 方法与口径

| 项 | 取值 |
|---|---|
| suga | 本 worktree 构建的 `cmd/suga`（HEAD `aefc5968`，v0.33.0 + 限定名 + 被丢弃限定符点名 + Go 结论提示 + 本节三处提示词/描述改动） |
| SUGA_HOME | 每次运行一个隔离目录 `tmp/b17q/run/<tag>/home`，只写入非密钥配置（`model`、`max_steps: 60`、`tools.allow_write: false`、`tools.approval: auto`、`memory.project.backend: files`、provider 的 `type/context_window/base_url/headers/models`）；密钥用 `api_key: "${OPENCODE_API_KEY}"` 从进程环境变量展开，**不复制 `.env`、不打印密钥值**；运行产物在本节结束时删除 |
| 模型 | `opencode-go/deepseek-v4.1-flash`（thinking high），与 §8、§9、explore-compare、omp 参照同一模型 |
| 运行方式 | `suga run --read-only --session-id <tag> -p <提问>`，工作目录 = 本 worktree，3 并发，6 次运行 6/6 `exit 0`；6/6 的 `turn_context` 都是 `mode=read_only`、`tools_count=15`、无 `preferred_tools` |
| 任务 | explore-compare §1.3 的 T5、T6，提问原文照抄（每任务 3 次），与 §9 同一批提问 |
| 口径 | 与 §8/§9 一致：只读档 A；步数 = `model` 事件数，工具调用 = `tool` 事件数，token = `input_tokens` 之和（另给 cache read），墙钟 = 进程墙钟；`file_refs` 首次出现的“第 N 步”= 该调用所在的回合序号（含 1，与 §9 口径相同） |

工具参数取自 `suga export <sid>`（`ToolCall.Arguments`），事件计数与 token 取自 `suga trace <sid> --json`。

### 10.3 对比表（A 档口径，deepseek-v4.1-flash，只读）

| 任务 | 指标 | explore-compare A（2 次） | §8 v0.33.0（2 次） | §9 本分支基线（3 次） | **§10 本批（3 次）** | 本批区间 | omp（2 次） |
|---|---|---|---|---|---|---|---|
| T5 `ReadModelSettings` 调用方 | 步数 | 8.0 | 9.0 | 8.0 | **4.0** | 3–5 | 6.5 |
| | 工具调用 | 17.5 | 19.5 | 23.3 | **4.0** | 4–4 | 7.5 |
| | 输入 token | 131.7k | 138.9k | 129.9k | **37.4k** | 29.0–44.8k | – |
| | 墙钟 | 74.5s | 64.7s | 48.3s | **15.8s** | 13.6–19.0s | – |
| | 答案正确 | 2/2 | 2/2 | 3/3 | **3/3** | – | 抽查一致 |
| T6 `Runner.Run` 调用方 | 步数 | 12.5 | 15.5 | 16.3 | **14.7** | 12–17 | 9.5 |
| | 工具调用 | 22.0 | 33.5 | 42.7 | **26.7** | 22–35 | 14.5 |
| | 输入 token | 258.1k | 375.1k | 457.7k | **362.2k** | 270.7–437.2k | – |
| | 墙钟 | 104.5s | 71.2s | 101.4s | **95.5s** | 64.3–120.5s | – |
| | 答案正确 | 2/2 | 2/2 | 2/3 | **3/3** | – | 抽查一致 |

与 §9 的均值差：

- **T5 全面下降且幅度远超噪声**：步数 **-4.0**（-50%）、工具调用 **-19.3（-83%）**、输入 token **-92.5k（-71%）**、墙钟 **-32.5s（-67%）**；而且首次**超过 omp 参照**（4.0 步/4.0 调用 vs 6.5/7.5）。
- **T6 只有调用数与 token 下降、步数仍不达标**：步数 **-1.6**（噪声内）、工具调用 **-16.0（-37%）**、输入 token **-95.5k（-21%）**、墙钟 -5.9s（与 §8 的 71.2s 相比反而更慢）；仍高于 omp（14.7/26.7 vs 9.5/14.5）。
- **正确性：T6 从 2/3 回到 3/3**（§9 的 `subagent.go:94` 漏报没有重现）；T5 仍 3/3。

### 10.4 逐次明细

| 运行 | 步数 | 工具调用 | 输入 / cache read / 输出 token | 墙钟 | 工具分布 | `file_refs` 次数（首次步） | 限定名 | 多段/多文件读 | outline | 同一文件重复读 |
|---|---|---|---|---|---|---|---|---|---|---|
| T5-1 | 5 | 4 | 44.8k / 33.5k / 0.8k | 19.0s | file_refs×2, file_read×2 | 2（第 1 步） | 0 | **1 次（5 段跨 5 文件）** | 0 | models.go×2、settings_registry.go×2 |
| T5-2 | 4 | 4 | 38.4k / 26.0k / 1.1k | 13.6s | file_refs×2, file_symbols×1, file_read×1 | 2（第 1 步） | 0 | **1 次（4 段，其中一段两区间）** | 0 | – |
| T5-3 | 3 | 4 | 29.0k / 16.8k / 1.5k | 14.7s | file_refs×2, file_read×1, file_symbols×1 | 2（第 1 步） | 1（`appconfig.ReadModelSettings`） | **1 次（5 段）** | 0 | settings_registry.go×2 |
| T6-1 | 17 | 35 | 437.2k / 404.2k / 7.4k | 101.6s | file_grep×16, file_read×9, file_refs×9, file_symbols×1 | 9（**第 6 步**） | 2 | 5 次 | 0 | server.go×2、chat_turn.go×2、run.go×2 |
| T6-2 | 12 | 22 | 270.7k / 243.5k / 6.2k | 64.3s | file_grep×8, file_refs×7, file_read×6, file_symbols×1 | 7（第 1 步） | 2 | 6 次 | 0 | server.go×3、run.go×4、chat_turn.go×2、subagent.go×2 |
| T6-3 | 15 | 23 | 378.6k / 322.3k / 6.7k | 120.5s | file_grep×12, file_read×7, file_refs×4 | 4（第 1 步） | 2 | 6 次 | 0 | run.go×4、chat_turn.go×2、server.go×2、runtime.go×2 |

6/6 次运行的**工具错误为 0**（与 §9 相同），没有幻觉工具调用。

### 10.5 三句话是否改变了行为

1. **“第一步用 `file_refs`”：T5 完全生效（3/3 都在第 1 步），T6 部分生效（2/3 第 1 步）。** §9 里 T5 是“第 2 步”、T6 是“第 2/2/3/9 步”；本批 T5 三次第 1 步、T6-2/T6-3 第 1 步，但 **T6-1 拖到第 6 步**（前 5 步 5 次 `file_grep` 在猜 `\.Run\(ctx\|turnCtx\|...`、`RunRequest\{` 这类正则，与 §9 T6-2 前 8 步 13 次 grep 同型）。
2. **“Go 结果已列全部位置、不要再用 `file_grep` 补漏”：T5 明显生效，T6 只减半。** T5 三次**全程 0 次 `file_grep`**（§9 是 1/3/6 次），拿到 `file_refs` 结果后直接用 `file_read`/`file_symbols` 核实；T6 的 grep 总数从 §9 的 75 次降到 **36 次**（16/8/12），但 T6-1 拿到结果后仍用 7 次 `file_grep` 自查。
3. **“只答被问的范围，测试使用只给数量”：6/6 全部照做，是本批最一致的行为变化。** §9 的 T5 三次都在拿到生产调用点后逐个为测试文件建清单（名**§9.7 候选 4**），本批 6/6 次运行的答案**只列生产代码，测试调用只给一句数量**（T5-1 “测试代码另有 40 处”、T5-2 “约 50 处”、T5-3 按文件给出 50 处的小计、T6-1 “100+ 处”、T6-2 “40 多个”、T6-3 “176 行”）。T5 三次都**额外用一次 `file_refs include_tests: true` 去数**（这是三次运行里唯一“多余”的一步）。
4. **`file_read` 的多段/多文件读从“少数用法”变成 T5 的主力。** T5 三次各 1 次 `paths` 读（一次 5 段跨 5 个文件、一次 4 条目其中一条两区间），把“看 5 个调用点”压成 1 次调用；T6 三次各 5/6/6 次多段读（§9 全批只有 4 次）。**`outline` 仍然 0/6 次**，与 §7（0/10）、§9（1/6）同量级——“只需要知道一个大文件里有哪些声明”这个触发条件在本批三个任务里没有出现（这三个任务都需要读实现体）。

### 10.6 答案正确性

- **T5：3/3 正确**，三次都给出 `internal/config/providers.go:556` 的定义与 5 处生产调用（`cli/models.go:27`、`wiring/manifest.go:480`、`wiring/runtime.go:760`、`config/settings_registry.go:523`、`:649`），与标准答案一致；T5-3 用了限定名 `appconfig.ReadModelSettings`，拿到 3 处（同包 2 处未加包名限定）后拆成“生产代码 3 处 + 包内自调用 2 处”说明，没有漏报。
- **T6：3/3 正确**，三次都恰好列出 5 个直接调用方（含 `internal/personal/subagent.go:94` 的 `child.Run`），§9 里由限定名导致的漏报（T6-1 只列 4 处）没有重现；三次也都没有把 `internal/chat/local_cmd.go:94` 的 `s.local.Run` 算进去。
- 三次 T6 答案都把测试调用写成数量而不是清单，正确性没有因此受影响（提问没有要求列测试）。

### 10.7 结论与建议

1. **建议保留三句，不回滚任何一句。** T5 是 §8/§9 里唯一稳定的观察面，本批在该任务上的改善幅度（-50% 步数、-83% 调用、-71% token）远超噪声，且首次超过 omp 参照；T6 的工具调用 -37%、token -21%、正确率 2/3 → 3/3，也只有步数一项落在噪声内没有改善。三句话里第 3 句（答案范围）单独就能解释 T5 的大部分收益，第 1 句解释了“第 1 步就调 `file_refs`”，第 4 条改动（`file_read` 描述）解释了 T5 的“一次 5 段读”。
2. **负面/未改善的部分要记下来：**
   - T6 步数没有改善（12–17 步，仍高于 omp 的 9.5），T6-1 仍在“猜正则 → 才想起 `file_refs`”，说明**提示词对“按类型找调用方”这类问题的第一步修辞仍不够硬**；若要继续，候选是 §9.7 候选 2（把 `file_refs` 的“dropped”名单从说明升级为“回跑一次无限定检索”的指令），而不是再改系统提示词。
   - `outline` 6/6 未用，`file_read` 描述里那句在本批没有触发场景，属于“写了但没被验证”的一行。
   - T6 仍大量重复读同一文件的不同片段（T6-2 run.go×4、T6-3 run.go×4），多段读没有解决“拼全貌”。
   - 本批的固定成本没有下降：6/6 次的 cache read 占总输入 68%–92%，T6 的 270k–437k 输入 token 里大部分是重复发送的窗口内容。
3. **下一批的两个可验证候选（本次未改代码）：**
   - `file_refs` 结果里直接给出“测试里另有 N 处”（现在模型要额外一次 `include_tests: true` 调用才能数，T5 三次都做了这一步），可把 T5 压到 2–3 步 / 3 次调用。
   - T6-1 型的“先猜正则”收尾，按第 2 条的“回跑提示”做，并用 T6 的“调用数/正确率”而不是步数做验收。

### 10.8 局限

- 每任务 3 次，T6 方差大（12–17 步、22–35 调用）；本节只采信方向一致、幅度超过 ±3–5 步（或调用/token 上同向且 >20%）的信号，T6 的“步数不变”与 T5 的全面下降都超出了这个门槛。
- 只测了一个模型（`opencode-go/deepseek-v4.1-flash`）的只读档 A；有 shell 的档位、`tools.preferred`、其它模型都没有数据。omp 参照是 explore-compare 的同模型两次数值，不是本批同刻复跑。
- 与 §9 相隔的提交是 `1760bec2`（限定查询把少见变量限定符的行以 “[maybe also Q: …]” 组显示）；§8 里那 4 个提交（限定名过滤、被丢弃限定符点名、Go 结论提示）也在两条基线之间，因此步数/调用差不能全部归给本节三处提示词改动。
- 提示词改动会让 provider 的前缀缓存整段失效（每会话重建一次）；本批没有单独测量这项代价。
- 标准答案用 `git grep` 在本 worktree 复核（行号与 §8/§9 一致）；运行产物（二进制、隔离 home、会话库、答案与 trace 导出）已删除，数字来自 `tmp/b17q/metrics.json`（临时文件，一并删除）与 `suga trace --json`；未做第三方复核。


### 10.6 补充参照：codex（gpt-6-sol，监督补测）

gpt 网关恢复后用 codex（gpt-6-sol，reasoning high，有 shell）跑同样的 T5/T6 各 2 次，只读提示。工具调用取 codex 事件流里的 `exec` 次数（每次一条 pwsh 命令，多为 `rg -n --glob '*.go' …` 与 `Get-Content | Select-Object -Skip … -First …` 读片段）；token 为 gofer 记录的总量（含缓存，口径与 suga 的输入 token 不同，只作量级参考）。

| 任务 | codex 命令数（2 次） | codex 总 token | 答案 |
|---|---|---|---|
| T5 | 11 / 10 | 90k / 70k | 2/2 列出生产调用点 |
| T6 | 20 / 43 | 157k / 98k | 2/2 列出 5 处（含 `subagent.go:94`） |

模型不同（gpt vs deepseek-v4.1-flash），不与 §10.3 直接比较步数；可以看出的是：codex 同样以“rg 搜 + 分段读”为主，T6 的方差同样大（20–43 次），T5 的 4 次调用（§10）已少于 codex 的 10–11 次。下一步用 suga 默认的 gpt 模型跑同一组任务，才能和 codex 同模型对比。
