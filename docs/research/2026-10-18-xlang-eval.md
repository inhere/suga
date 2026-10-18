# 跨语言探索评测：suga vs omp（第十八批 L2，18 题 × 2 次）

> 状态：调研报告（非设计文档，不过文档校验器）
> 日期：2026-10-18
> 题库与判分依据：`docs/research/2026-10-18-xlang-tasks.md`（18 题、标准答案、必中/陷阱集合）
> 效率基线与口径来源：`docs/research/2026-10-18-explore-efficiency.md` §10（Go 任务 T5/T6 的只读档 A 基线）
> 运行产物（构建的 suga 二进制、36 个隔离 SUGA_HOME、会话库、答案与 trace 导出、`tmp/omp-jobs.tsv`）在所有分析完成后已删除

## 1. 方法与口径

| 项 | suga | omp |
|---|---|---|
| 被测 | 本 worktree 构建的 `cmd/suga`（HEAD `a36676a1`，`go build -o tmp/xl/suga.exe ./cmd/suga`，`suga dev (dev)`） | （外部 agent，同模型）36 个 gofer job，job id 见原 `tmp/omp-jobs.tsv` |
| 模型 | `opencode-go/deepseek-v4.1-flash`（thinking high） | 同（`opencode-go/deepseek-v4.1-flash`） |
| 档位 | 只读档 A：`suga run --read-only`，不接 cbm、无 `tools.preferred`；隔离 `SUGA_HOME=tmp/xl/run/<tag>/home`，只写非密钥配置（`model`、`max_steps: 60`、`tools.allow_write: false`、`tools.approval: auto`、`memory.project.backend: files`、provider 的 `type/context_window/base_url/headers/models`）；密钥经进程环境变量 `OPENCODE_API_KEY` 展开（`api_key: "${OPENCODE_API_KEY}"`），不复制 `.env`、不打印值 | 同模型只读口径（由监督方派出），每题 2 次 |
| 工作目录 | 该题的仓库目录（`requests`/`ky`/`gson`/`guzzle`/`walkdir`/`inhere.github.io` 快照） | 同 |
| 提问 | `只读任务，不要修改任何文件，也不要运行构建或测试。` + 题库原文 + `请直接给出答案（文件:行）。` | 同一句式 |
| 次数/并发 | 每题 2 次共 36 次，3 并发，单次超时 600s，**36/36 `exit 0`** | 36 个 job 全部 `done`/`exit 0` |
| 指标 | 步数 = trace `model` 事件数；工具调用 = trace `tool` 事件数；输入 token = `input_tokens` 之和（**含 cache read**，另给 cache）；墙钟 = 进程墙钟；工具分布/失败取自 trace 的 `name` 与 `status`；工具参数取自 `suga export` | 步数 = stderr 事件流 `turn_end`；工具调用 = `tool_execution_start`；失败 = `tool_execution_end` 且 `ok=false`；token = `gofer job show` 的 usage（`in` **不含** cache，另列 cache） |

**token 口径不可直接相减**：suga 的 `input_tokens` 含 cache read，omp 的 `in` 不含（omp 另有 `cache`）。表中两列只作量级参考，结论只采信步数与工具调用数。

## 2. 正确率（严格按题库「必中 + 陷阱」判分）

| 侧 | 全对 | 部分 | 错 | 陷阱命中 |
|---|---|---|---|---|
| suga | **36/36** | 0 | 0 | 0 |
| omp | **36/36** | 0 | 0 | 0 |

- 陷阱设计（`rebuild_auth`、`#calculateRetryDelay`、`fromJson(JsonReader, TypeToken)`、`isErrorType`、`TypeAdapter.write`、`requestAsync`/`sendAsync`、`fs::Metadata::file_type`、`rel_attributes` 调用点、`merge.ts:446`/`Ky.ts:218`、`Client.php:436`、`header.html:50/:60` 等）在 72 次作答里**没有一次被当成答案**；两边都主动把这些位置标成「不是/排除」。
- 少数判分边界（不改变判定）：RQ1 两次都把手写类的答案写成 `Session`（可选要求 `SessionRedirectMixin`）；GS3 四次把必中 `Gson.java:685` 放在括注/旁路里（必中位置被点名）；WD3-1 suga 把 `lib.rs:923/:952` 放在括注而非独立链路步骤；GS2-1 suga 首句笔误「4 处」但随后列全 5 处。

## 3. 语言 × 题型对比（每题 2 次均值）

| 语言 | 题型 | n | suga 步数 | omp 步数 | suga 调用 | omp 调用 | suga 输入 token | omp 输入 token | suga 墙钟 | suga file_refs/syms | suga 被拒调用 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Python | C | 2 | 4.5 | 4.5 | 5.5 | 7.0 | 36k | 9k | 21s | 1.0 | 0.0 |
| Python | D | 2 | 4.0 | 3.0 | 4.5 | 3.5 | 29k | 0k | 19s | 0.0 | 0.0 |
| Python | F | 2 | 6.5 | 7.5 | 7.5 | 10.5 | 74k | 12k | 38s | 0.5 | 0.0 |
| TS | C | 2 | 3.5 | 4.0 | 4.5 | 5.0 | 29k | 4k | 11s | 1.0 | 0.0 |
| TS | D | 2 | 4.0 | 4.0 | 4.5 | 4.0 | 33k | 6k | 22s | 1.0 | 0.0 |
| TS | F | 2 | 6.0 | 9.0 | 8.0 | 12.5 | 106k | 18k | 36s | 0.0 | 0.0 |
| Java | C | 2 | 5.5 | 4.5 | 6.5 | 6.0 | 45k | 4k | 24s | 1.5 | 0.0 |
| Java | D | 2 | 3.0 | 3.5 | 3.0 | 3.5 | 21k | 0k | 10s | 0.0 | 0.0 |
| Java | F | 2 | 3.5 | 6.0 | 3.5 | 7.0 | 30k | 12k | 14s | 0.0 | 0.0 |
| PHP | C | 2 | 5.0 | 6.0 | 7.0 | 9.0 | 59k | 8k | 31s | 1.5 | 0.0 |
| PHP | D | 2 | 3.0 | 3.5 | 3.5 | 4.0 | 25k | 0k | 15s | 2.0 | 0.0 |
| PHP | F | 2 | 5.5 | 10.0 | 8.0 | 14.5 | 90k | 14k | 35s | 2.0 | 0.0 |
| Rust | C | 2 | 4.0 | 5.5 | 5.0 | 7.0 | 34k | 9k | 31s | 1.0 | 1.0 |
| Rust | D | 2 | 3.5 | 3.0 | 3.5 | 3.0 | 24k | 0k | 14s | 0.0 | 0.0 |
| Rust | F | 2 | 6.0 | 8.5 | 9.0 | 13.0 | 72k | 14k | 36s | 1.5 | 1.5 |
| Zola | C | 2 | 3.5 | 4.5 | 3.5 | 5.5 | 31k | 9k | 12s | 0.5 | 0.0 |
| Zola | D | 2 | 3.5 | 4.0 | 3.5 | 5.0 | 32k | 11k | 11s | 0.5 | 0.5 |
| Zola | F | 2 | 7.0 | 7.0 | 13.5 | 7.5 | 84k | 9k | 56s | 0.0 | 0.0 |

36 次合计：suga 步数 163 / 工具调用 208 / 输入 1.71M / 输出 51k / 墙钟 871s；
omp 步数 196 / 工具调用 255 / 输入 0.28M（不含 cache）。

## 4. 逐次明细

| tag | suga 步数/调用/墙钟/输入token | omp 步数/调用/输入token | suga 工具分布 | suga 工具错误 |
|---|---|---|---|---|
| RQ1-1 | 5/6/27s/37k | 3/3/0k | file_find×1, file_grep×2, file_read×3 | 0 |
| RQ1-2 | 3/3/11s/21k | 3/4/0k | file_find×1, file_grep×1, file_read×1 | 0 |
| RQ2-1 | 5/6/22s/41k | 6/9/15k | file_grep×3, file_list×1, file_read×1, file_refs×1 | 0 |
| RQ2-2 | 4/5/19s/32k | 3/5/3k | file_grep×2, file_read×2, file_refs×1 | 0 |
| RQ3-1 | 8/10/50s/98k | 8/11/15k | file_find×1, file_grep×3, file_read×5, file_refs×1 | 0 |
| RQ3-2 | 5/5/27s/51k | 7/10/9k | file_grep×1, file_list×1, file_read×3 | 0 |
| KY1-1 | 4/4/18s/34k | 4/4/11k | file_grep×2, file_read×1, file_symbols×1 | 0 |
| KY1-2 | 4/5/26s/32k | 4/4/0k | file_grep×2, file_list×1, file_read×1, file_symbols×1 | 0 |
| KY2-1 | 3/3/8s/23k | 4/5/4k | file_find×1, file_read×1, file_refs×1 | 0 |
| KY2-2 | 4/6/14s/34k | 4/5/4k | file_grep×3, file_list×1, file_read×1, file_refs×1 | 0 |
| KY3-1 | 5/6/43s/90k | 9/13/19k | file_grep×2, file_list×2, file_read×2 | 0 |
| KY3-2 | 7/10/28s/122k | 9/12/17k | file_grep×2, file_list×2, file_read×6 | 0 |
| GS1-1 | 3/3/10s/21k | 3/3/0k | file_find×1, file_grep×1, file_read×1 | 0 |
| GS1-2 | 3/3/9s/20k | 4/4/0k | file_find×1, file_grep×1, file_read×1 | 0 |
| GS2-1 | 8/10/35s/69k | 4/5/4k | file_find×1, file_grep×2, file_list×1, file_read×4, file_refs×2 | 0 |
| GS2-2 | 3/3/14s/22k | 5/7/4k | file_grep×1, file_read×1, file_refs×1 | 0 |
| GS3-1 | 3/3/15s/24k | 8/9/18k | file_grep×1, file_list×1, file_read×1 | 0 |
| GS3-2 | 4/4/14s/37k | 4/5/5k | file_find×1, file_grep×2, file_read×1 | 0 |
| GZ1-1 | 3/3/15s/26k | 3/3/0k | file_read×1, file_refs×1, file_symbols×1 | 0 |
| GZ1-2 | 3/4/16s/24k | 4/5/0k | file_grep×1, file_read×1, file_refs×2 | 0 |
| GZ2-1 | 5/6/31s/45k | 5/8/6k | file_find×1, file_grep×3, file_refs×1, file_symbols×1 | 0 |
| GZ2-2 | 5/8/30s/73k | 7/10/11k | file_find×1, file_grep×5, file_read×1, file_refs×1 | 0 |
| GZ3-1 | 7/10/41s/134k | 12/19/18k | file_find×1, file_grep×4, file_read×3, file_refs×1, file_symbols×1 | 0 |
| GZ3-2 | 4/6/30s/46k | 8/10/11k | file_grep×1, file_list×1, file_read×2, file_refs×1, file_symbols×1 | 0 |
| WD1-1 | 3/3/14s/21k | 3/3/0k | file_grep×1, file_list×1, file_read×1 | 0 |
| WD1-2 | 4/4/14s/28k | 3/3/0k | file_grep×2, file_list×1, file_read×1 | 0 |
| WD2-1 | 4/4/46s/31k | 5/6/7k | file_grep×1, file_list×1, file_read×1, **file_refs×1** | **file_refs 1** |
| WD2-2 | 4/6/17s/38k | 6/8/11k | file_grep×2, file_list×1, file_read×2, **file_refs×1** | **file_refs 1** |
| WD3-1 | 8/12/52s/97k | 9/13/15k | file_find×1, file_grep×3, file_list×1, file_read×4, **file_refs×2, file_symbols×1** | **file_refs 2, file_symbols 1** |
| WD3-2 | 4/6/20s/47k | 8/13/14k | file_find×1, file_grep×2, file_list×1, file_read×2 | 0 |
| ZL1-1 | 3/2/10s/29k | 3/4/5k | file_grep×1, file_read×1 | 0 |
| ZL1-2 | 4/5/12s/36k | 5/6/16k | file_find×1, file_grep×2, file_read×1, **file_symbols×1** | **file_symbols 1** |
| ZL2-1 | 3/2/11s/26k | 5/6/16k | file_grep×2 | 0 |
| ZL2-2 | 4/5/12s/37k | 4/5/3k | file_grep×4, file_refs×1 | 0 |
| ZL3-1 | 6/11/39s/66k | 7/7/9k | file_find×1, file_grep×3, file_list×3, file_read×4 | 0 |
| ZL3-2 | 8/16/72s/101k | 7/8/10k | file_find×3, file_grep×5, file_list×5, file_read×3 | 0 |

工具总分布与「新工具」使用率（36 次）：

| 工具 | 次数 | 备注 |
|---|---|---|
| file_read | 64 | 全部是单窗口/单段读；`paths` 多段读 **0 次**，`outline` **0 次**（与 §7/§9/§10 的 Go 任务一致） |
| file_grep | 73 | 平均 2.0 次/次运行，最高 ZL3-2 的 5 次 |
| file_list | 25 | 主要在 ZL 站点（目录多） |
| file_find | 18 | 多为第 1 步定位文件名 |
| **file_refs** | 20（16 ok / **4 error**） | 12/36 次运行用过（33%），低于 §10 Go 任务的 100% |
| **file_symbols** | 8（6 ok / **2 error**） | 6/36 次运行用过（17%） |

## 5. 重点问题：suga 在 Go 以外的语言和场景上是否明显退化？

**结论：没有明显退化。** 以 `docs/research/2026-10-18-explore-efficiency.md` §10 的 Go 只读档 A 基线（T5 4.0 步/4.0 调用/37.4k；T6 14.7 步/26.7 调用/362.2k）为参照：

1. **非 Go 的平均成本低于 Go 的 T6，与 T5 同档**：本批 36 次合计 163 步 / 208 调用 → 平均 **4.5 步 / 5.8 调用 / 47.5k 输入 token**；其中 14 个「D/C」格子平均 ≤5.5 步。与 T5（4.0/4.0/37.4k）同量级，显著优于 T6（14.7/26.7/362.2k）。
2. **对 omp 的横向比较：18 个语言×题型格子里，suga 在 13 个上调用数 ≤ omp**（合计 208 vs 255），步数 163 vs 196；只有 4 个格子 suga 步数更多（Python-D 4.0/3.0、Java-C 5.5/4.5、Rust-D 3.5/3.0、Zola-C 3.5/4.5 属平/噪声内），其中 2 个（Python-D、Rust-D）差 1 步以内。
3. **suga 唯一明显弱于 omp 的格子是 Zola-F（ZL3）**：步数持平（7.0/7.0），但 suga 调用 13.5 vs omp 7.5、输入 84k vs 9k。trace 证据：ZL3-2 用了 16 次调用（file_list×5、file_grep×5、file_find×3）在站点目录里反复找同名/近名模板，而 omp 用一条 `rg -n --no-ignore --hidden` 类命令一次收敛。这是「没有 shell、只能靠 file_* 组合」的结构性劣势在**多目录、多同名文件的站点仓库**上的体现，不是语言理解问题。
4. **suga 在 Go 上的老毛病（猜正则、重复读、`file_refs` 拖到第 6 步）在本批非 Go 题上基本没有出现**：36 次里 `file_grep` 平均 2.0 次（§10 Go 的 T6 平均 12 次），失败调用只有 6 次且全是「不支持的语言」这一种（见下）。也就是说，非 Go 之下 **file_grep + file_read 的通用路径反而更稳**。

### 5.1 被拒的工具调用（唯一一类真实失败）

被拒的 6 次调用全部是「工具只支持 Go/JS-TS/Python/Java/PHP，遇到 Rust 与 Tera 模板时直接报错」，原文取自 trace：

- WD2-1：`file_refs` → `file_refs: no supported source files under ...\walkdir\src; supported: Go (.go), JavaScript/TypeScript (.ts, …), Python (.py, …), Java (.java), PHP (.php)`
- WD2-2、WD3-1：同上（WD3-1 里 `file_refs` 连续 2 次被拒）
- WD3-1：`file_symbols` → 同一句「no supported source files」
- ZL1-2：`file_symbols` → `file_symbols: unsupported file type ".html"; supported: Go (.go), …`

代价与行为影响：

- **代价只有 1 步 + 一次错误文本进上下文**：WD2-1 被拒后立刻 `file_grep` 补救，最终 4 步 4 调用（仍优于 omp 的 5 步 6 调用）；ZL1-2 被拒后 `file_grep` 一次就拿到定义，4 步 5 调用（omp 5 步 6 调用）。
- **没有改变正确率**：WD2/WD3/ZL1 的全部 6 次 suga 作答都判「全对」，且都避开了陷阱（WD2 只列 `lib.rs:844/847/857` 三处，把 `fs::Metadata::file_type`、`dent.rs` 标准库调用、文档注释示例全部显式排除）。
- **但 `file_refs` 在 Rust 题上等于「不可用」**，本批 WD1/WD2/WD3 的 6 次运行里只有 3 次尝试调用（且全部失败），其余靠 `file_grep`；而 §10 的 Go 任务上它是第 1 步就被叫出来的主力工具。Rust 题恰好是「同名近似匹配最容易跑偏」的题型（WD2 的 `file_type` 有 4 个不同接收者），**这正是最需要符号/引用级检索的场景，也是当前唯一完全不支持它的场景**。
- Zola 模板（`.html` + Tera 组件）同理：`file_symbols` 被拒，模型只能靠 `file_grep target_attribute` 数调用点；ZL2 两次仍答对 5 处，但 ZL1-2 多花了一步。

### 5.2 「先用 file_refs」提示词在不支持的语言上是否浪费

- 提示词那句（`internal/wiring/prompt.go`，§10 落地）在本批被触发的次数：`file_refs` 用于 12/36 次运行，其中 3 次落在 Rust 题并全部失败。**没有出现「先 refs 失败 → 再 refs → 再失败」的循环**，每次被拒后模型立刻换成 `file_grep`，浪费上限是 1–2 步。
- 反例（提示词有效且划算）：`file_refs` 在 Python/TS/Java/PHP 题上的 16 次成功调用里，多次一击命中答案集（RQ2 的两处调用、GZ2 的 6 处 `$this->request()` 调用点、KY2 的 5 处 `isError` 调用），这些正是「猜正则」的老毛病最重的地方。
- 所以问题不是「提示词在不支持语言上白跑」，而是**「工具的可支持语言面比用户仓库的语言面窄」**：被拒的调用只占 6/208（2.9%），但它把「唯一定位近似匹配的利器」在 Rust/模板上整体关掉了。

## 6. 改进建议（按收益排序，本任务不改代码）

1. **`file_refs` / `file_symbols` 对不支持的语言退化为词边界文本检索，而不是直接报错。**
   - 证据：WD2/WD3 的 3 次 `file_refs` 被拒、ZL1-2 的 `file_symbols` 被拒（§5.1 原文）；Rust 题（WD2）恰好是 §3.5 里「近似匹配最容易跑偏」的题。
   - 收益：Rust/模板类仓库从「不可用」变成「近似可用」，与提示词里「问调用方第一步用 file_refs」的约定自洽；预计 Rust 的 C 题从 4.0 步/5.0 调用继续向 3 步收敛（该题 omp 是 5.5/7.0，suga 已经领先，主要收益是**稳健性**而不是步数）。
   - 成本/风险：近似结果必须像现在一样打上「approx / 词边界匹配」标记，并保留当前 Go/其他语言精确分类的差别，避免把文本命中当调用点（题库里 `rel_attributes`、`isErrorType`、`file_type` 这一层干扰正是靠精确分类挡住的）。
2. **报错文本升级为「已退化的结果」**：即使先不做退化，至少把「no supported source files」改成「该语言不支持符号级检索，请用 `file_grep` 的词边界模式：`pattern=\bname\(`」这类**单行、可执行的下一步**，而不是让模型自己决定。
   - 证据：6 次被拒调用各消耗 1 步并把错误文本带进上下文（WD3-1 连续 2 次 `file_refs` + 1 次 `file_symbols`）。
3. **把「支持的语言」前移到工具描述 + 只读环境说明**，让模型在**决定调用前**就知道本题仓库的语言是否被覆盖（本批 ZL 站点是 Tera/HTML、walkdir 是 Rust，都是 100% 不可用）。
   - 证据：ZL1-2 第 1 步直接对 `settings.html` 调 `file_symbols`；WD2-1 第 1 步对 `walkdir/src` 调 `file_refs`。两次都是「先试一次才发现不支持」。
   - 收益：非 Go 题每批省 1 步、约 20–40k 输入 token（本批 6 次失败调用各自的输入 token 在 20k–100k 量级）。
   - 风险：环境说明变长（§7 已测过「说明变长不改变行为」的先例），但不改这条的话每批都会有固定损耗。
4. **`file_symbols` 支持模板/配置类文件的「声明」概念**（Tera 的 `{% component %}`/`{% macro %}`、`config.toml` 的段名），作为 Go 之外的第二优先语言面。
   - 证据：ZL1（找组件定义 + 参数列表）与 ZL3（追 `extra.stylesheets` 链路）都发生在同一个只有 Tera/TOML 的仓库里，是本批唯一「suga 弱于 omp」的方向（ZL3 调用 13.5 vs 7.5）。
5. **`file_read` 的多段读/`outline` 在非 Go 题上依然是 0 次使用**（本批 64 次 `file_read` 全是单窗口，`paths` 0、`outline` 0；与 §7 的 0/10、§9 的 1/6、§10 的 0/6 同型）。
   - 证据：ZL3-2 / KY3-2 这类「同一问题涉及 5–6 个位置」的题仍然靠 3–6 次单段读拼出来（ZL3-2：`file_read×3` + `file_list×5`）。
   - 建议：若 §10 已定的「在截断/分页提示里带上 `outline`/多段读钩子」要做，**跨语言仓库（站点、模板）也应该算目标场景**，那里目录层数多、同名文件多，多段读的收益比 Go 仓库更大。

## 7. 局限与偏差

- 每题 2 次，单次噪声按 §9 的量级（±3–5 步、调用数可达 ±2 倍）计；本批结论只采信「语言×题型格子级、方向一致且幅度 >1 倍调用数」的信号（如 Zola-F 的 13.5 vs 7.5、PHP-F 的 8.0 vs 14.5）；单题 1 步以内的差异（RQ1-1 的 5 vs 3）不作为结论。
- omp 是同一模型的外部 agent，本任务只读它的 job 日志（`stdout.log` 答案 + `stderr.log` 事件流）；**其提示词由监督方派出，本任务无法逐字核对两侧提示是否完全一致**（我能核对的只有我自己侧的逐字提示与「同句式」约定），这是本报告最大的口径偏差。
- token 口径不同（§1）：suga 含 cache read、omp 的 `in` 不含，故 token 列只看量级。
- 判分由 5 个子 agent 按题库 §2/§3 严格判分汇总，未做第三方复核；行号未对仓库原文二次复核（答案与题库行号逐字一致）。
- 本批只测只读档 A（无 shell）；omp 的 `bash`/`grep` 说明了「有 shell」的对比面没有测。
- 运行产物：`tmp/xl/`（二进制、36 个隔离 home、trace/export/答案导出）与 `tmp/omp-jobs.tsv` 已删除；数字来自分析时导出的表格（临时文件，一并删除）。
