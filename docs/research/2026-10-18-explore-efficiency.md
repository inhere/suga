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

