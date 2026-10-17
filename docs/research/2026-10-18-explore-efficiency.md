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

9 次运行全部完成，答案**全部正确**（与上面标准答案一致，含文件:行；T1/T4 还给出了额外细节）。

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
- 所有 9 次运行的 `mode` 都是 `read_only`，`tools_count=13`（`file_read/file_grep/file_find/file_list` + 只读类）。**只读运行没有 `shell_exec`**（`internal/wiring/runtime.go:456`：`Registry.Get("shell_exec")` 且 `allowWrite` 才加它），所以 A/B/C 三档根本无法走 `rg`/`go doc` 这条路。

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

实测样本（9 次成功运行）里**没有** 5qb4 想解决的问题：

- **输出整理**：0 次 shell 调用、0 次构建/测试输出、0 次安装/抓取；工具结果全是 `file_read` 窗口和 `file_grep` 命中（唯一一次大输出由现有 spill 机制兜住：T3-A `spills 1`，已转存为可取回 ref）。也就是说本轮没有“噪声型大输出”样本，小模型摘要的收益**无法证伪也无法证实**；能确定的格式（溢出/折叠/截断）现有确定性机制已在做。
- **命令检查**：只读运行没有 shell（0 条命令），档 D 有 shell 但模型仍 0 条命令，因此**没有一条真实命令样本**可以评估误报/漏报；危险删除这类确定性风险已有 approval 规则与 `suag-dtom` 方向处理，用模型兜底会引入延迟与不稳定判断。

结论：**两半都暂不做**，理由分别是“无触发样本”和“无样本 + 更便宜的确定性规则已覆盖”；触发条件写成“当 shell 使用量出现（探索类任务里出现构建/测试/日志输出）后，再用同一套 trace 指标 A/B 复测”。详见设计文档“辅助小模型评估”。

## 6. 局限

- 每档每任务只跑 1 次，方差约 ±3 步；结论只对“方向一致的浪费模式”成立。
- 任务都是“只回答代码问题”的只读探索，未覆盖“改代码前必须先跑测试”的噪声场景，因此 5qb4 的结论只对本次样本成立（已在设计中写成触发条件）。
- 只读运行没有 shell，`tools.preferred` 的另一半（外部命令）在这批任务里未被真实触发；档 D 证明“给了能力也不会自动用”，但没证明“在需要大范围搜索时也不会用”。
- `xerj` 无索引、`command-code` 周额度用尽，两条外部对照（索引型检索、第三个模型）没有数据。
- “优先工具”这一半只测到“提示不改变行为”（档 D 有 shell 也不用 rg），没有测“需要大范围搜索时仍不用”，因此不能断言 rg 集成无价值。
