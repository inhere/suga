<!-- template_id: design; template_version: 1.1.1 -->
# 仓库探索效率优化设计

> 状态：Draft 0.3 / 已批准实施

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-18 | suga（gofer job） | 依据 `docs/research/2026-10-18-explore-efficiency.md` 的 9 次真实模型实测，只保留两个收益最大的手段：`file_read` 的声明大纲与多段范围读、Go 符号声明清单；辅助小模型（suag-5qb4）在本版暂不做。 |
| 0.2 | 2026-10-18 | 监督（Claude） | 按用户意见改为多语言符号提取：Go 用 go/parser，TS/JS、Python、Java 用轻量声明识别器，主机有 universal-ctags 时优先；Q1 300 行、Q2 file_symbols、Q3 覆盖模块缓存；Q4 另立 suga-6gx3（见“0.2 审核修订”）。 |
| 0.3 | 2026-10-18 | 监督（Claude） | 用户批准实施（2026-10-18“批准第十五批两份设计”），按 0.2 审核修订执行。 |

### 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：减少探索阶段的模型往返步数、重复读取与失败调用；手段只加在内置只读工具上，不新增模型、索引、缓存或外部依赖。
- allowed_scope：`pkg/agentcore/tools/native` 的 `file_read` 与新增的 Go 声明清单工具、`pkg/agentcore/tools/native` 的 Go 语言声明（`lang_go.go`）扩展、`internal/wiring` 的工具注册接线、对应单元测试与 Linux 伪终端冒烟场景、文档与 CHANGELOG。
- non_goals：不引入 gopls/LSP、不建代码索引、不做语义检索、不做仓库概览缓存、不替换 `file_grep`/`file_find`/`file_list`、不为 rg/xerj 写外部命令适配、不做辅助小模型的输出整理与命令检查、不改审批与写工具语义。
- expansion_policy：DEFER_OR_REQUEST。若需要改变稳定系统提示词、工具 schema 的既有字段语义、审批语义或引入新的持久化状态，停止实现并回到设计 Gate。
- review budget：小；两个手段都复用现有 `file_read`/`lang.go`/native 工具注册路径，不新增抽象层。
- 停止条件：用户批准本设计和对应计划前不实施。

## 0.2 审核修订（与下文冲突时以本节为准）

用户问：只做 Go 符号清单，换到 TS/Java/Python 项目会退化。修订：

- **符号提取是一层按语言的提取器**，`file_symbols` 与 `file_read` 的 `outline` 共用：
  - Go：`go/parser` 精确解析（原方案）。
  - TS/JS、Python、Java：第一版即支持，用轻量声明行识别器（TS/JS：function/class/interface/type/enum/export const|let 与类方法；Python：def/class，带缩进层级；Java：class/interface/enum/record 与方法签名），只输出 `文件:行 kind name 声明首行`，结果标注 `approx`（轻量识别），模型需要时再用 `file_grep` 核对。
  - 主机有 `universal-ctags`（PATH 中，`--output-format=json` 可用）时优先用它覆盖更多语言；无则用内置提取器，不强制依赖。
  - 不选 tree-sitter（cgo 或大体积纯 Go 移植，影响单二进制与 Windows）与 LSP（每语言一个服务，留给 suga-cm5e）。
  - 不支持的语言返回明确提示（列出支持的语言），不静默空结果。
- 工具描述与参数不再写“仅 Go”；`kind` 取值按语言映射（func/method/class/type/...）。
- Q1 大纲阈值 300 行；Q2 工具名 `file_symbols`；Q3 覆盖依赖模块缓存（只读根内）；Q4 只读运行列出 rg 的错配另立 issue suga-6gx3 修。
- 验证：每种语言一个夹具文件的单测；实测复跑原 4 个任务外，再加一个 TS 或 Python 仓库上的列清单任务。

## 背景与目标

用户在 `suag-7ih4` 记录：同模型同档位下 suga 探索阶段 9–19 步、omp 4–5 步。本批用真实模型在 suga 仓库上做了 4 个有标准答案的只读探索任务（11 次运行，A/B/C/D 四档配置），答案全部正确，但步数与 token 明显偏高，且浪费位置可复现：

1. **大文件反复读**：T4 档 A 的 24 次 `file_read` 里 12 次是重复读（模块缓存 `cflag/util.go` ×4、`cflag.go` ×3、`internal/wiring/runtime.go` ×3、`internal/cli/run.go` ×2），档 B 36 步/1.85M 输入 token 同样如此；T1 档 A 读 `runtime.go` 3 次。
2. **“列清单/找定义”要靠猜正则**：T2（列出 `internal/config` 全部 `Read*` 函数）用 `file_grep` 试 2–5 次才凑出正则；T4 追调用链同样反复 grep。
3. **grep 无意图过滤**：`suga_manifest` 这类 pattern 命中大量 `*_test.go`，模型要额外步骤排除（T1 档 D 花了 15 次 grep）。
4. **失败与幻觉调用**：T1 档 C 有 4 次 `file_read` 失败、T4 各档有 1 次 grep 失败、T1 档 D 有 1 次调用不存在的工具 `read`，每次失败都占一个模型往返。
5. **提示优先不等于能用**：只读运行没有 `shell_exec`，档 D 即使放开 shell，模型 26 次调用里 0 次用 rg —— 说明优化要落在内置只读工具本身。

目标（用户可见结果）：

1. 读一个大文件时，模型能一次拿到“这个文件有哪些声明、各在第几行”，而不是反复取窗口拼全貌。
2. 一次调用可以对多个文件/多个行段取值，不必为每个窗口发一次请求。
3. 有一条只读工具能一次列出某个包/目录的 Go 声明（func/type/const/var + 行号），默认排除测试文件。
4. 以上都不改变现有只读语义、审批语义和稳定系统提示词前缀；失败时给出可执行的下一步提示而不是空结果。

## 范围与非目标

### 范围

- `pkg/agentcore/tools/native/file_read.go`：新增可选的大纲模式与 `paths` 项的行段后缀（详见总体方案）。
- `pkg/agentcore/tools/native/file_symbols.go`（新文件）：Go 声明清单只读工具（`go/parser`，只做声明级解析，不做类型检查）。
- `pkg/agentcore/tools/native/lang_go.go`：把“本语言的声明清单入口”作为 Go 语言声明的一部分（复用现有 `Language` 声明风格），供新工具与描述使用。
- `internal/wiring`：只读工具在所有模式的注册接线与只读分类；`properties` 非空（`TestToolSchemasDeclareProperties` 守住）。
- 测试：native 单元测试（`t.Run` + `gookit/goutil/x/assert`）、`internal/wiring` 注册/schema 测试、Linux 伪终端冒烟场景（真实二进制 + `fakellm.py`，检查模型按新描述发起调用）、以及用本报告相同的 4 个任务做一次真实模型复测。
- 文档：本设计、`docs/plans/2026-10-18-explore-efficiency-plan.md`、CHANGELOG「未发布」。

### 非目标

- 不引入 gopls/LSP、不建索引、不做语义检索、不做仓库概览缓存（评估见报告第 4 节，本轮暂不做）。
- 不替换或改写 `file_grep`/`file_find`/`file_list` 的既有语义；不加 grep 默认上下文行、不做相关度排序。
- 不为 rg/xerj 或任何外部命令写客户端/输出解析；本期不解决“只读运行没有 shell”的能力错配（只记录，见“待确认事项”）。
- 不做辅助小模型（`suag-5qb4`）的输出整理与命令检查，理由见“决策”D6。
- 不改审批、写工具、provider 协议与稳定系统提示词。

## 已确认事实与规范

### 实测事实（本批，见 `docs/research/2026-10-18-explore-efficiency.md`）

| 事实 | 数据 |
|---|---|
| 任务 | T1 `suga_manifest` 注册与 `section` 语义、T2 `internal/config` 的 `Read*` 函数清单、T3 `session_search` CJK 短词、T4 `--read-only` 到写工具过滤调用链 |
| 运行 | 11 次（T1×4 档、T2×2、T3×3、T4×2），只读、隔离 `SUGA_HOME`、真实模型 `opencode-go/deepseek-v4.1-flash`/`openai-codex/gpt-6-luna` |
| 答案正确率 | 11/11 正确（含文件:行） |
| 步数范围 | 最小 4 步（T2），最大 36 步（T4 档 B）；T4 输入 token 1.49M–1.85M |
| 重复读 | T4 档 A 12/24 次 `file_read` 是重复读；T1 档 A `runtime.go` ×3 |
| 失败调用 | `file_read` 失败 4 次（T1 档 C）、`file_grep` 失败 3 次（T4 档 A/B、T1 档 D 各 1）、幻觉工具 `read` 1 次 |
| 方差 | 同配置单次运行噪声约 ±3–5 步，因此只采信方向一致的信号 |

### 现有代码事实

- `pkg/agentcore/tools/native/file_read.go:19-20`：`defaultReadLines = 2000`（不带 limit 时的默认窗口）。
- `pkg/agentcore/tools/native/file_read.go:43-83`：`Description()` 与 `Schema()` 现有参数为 `paths`（多路径）、`offset`、`limit`；`offset/limit` 在 `:89` 解析一次后**对所有 path 共用同一个窗口**。
- `pkg/agentcore/tools/native/file_read.go:137-158`：`readOne` 按 path 读取；`:206-268` `readLineWindow` 负责窗口与二进制/目录判断，截断提示为 `[truncated at %s; continue with offset=%d]`（`:268`），已有回归测试 `file_window_test.go`。
- `pkg/agentcore/tools/native/lang.go:11-26`：每种语言一个文件声明“依赖源码位置、读依赖 API 的方式、测试命令、项目识别文件”，消费者是 `dependencyRoots`、基础提示词的 dependency-API 子句、trace 分析（`internal/personal/analyze.go`）；`lang_go.go:11-13` 是 Go 的声明。
- `internal/wiring/runtime.go:456`：`shell_exec` 只在 `allowWrite` 时注册 —— 只读运行没有 shell，因此外部命令类手段在只读探索里不可用。
- 工具 schema 约束：`properties` 不能是空对象（`internal/wiring` 的 `TestToolSchemasDeclareProperties`），零参数工具也要有可选参数。
- 观测现状：`suga trace <sid> --analyze` 已经输出 `steps`（模型调用数）、`tools`（分布）、`re-reads`、`tokens`、`shell`、`spills`，够做本设计的回归指标，不需要改 `internal/personal/analyze.go`。

## 总体方案

只做两件事，都是“给已有的只读探索加结构化视图”。

### 手段 A：`file_read` 的声明大纲与多段范围读

1. **声明大纲**：新增可选参数 `outline`（bool，默认 `false`）。为 `true` 时，对每个 path 返回该文件的**声明清单**（行号 + 声明首行原文，例如 `12: func (t *FileReadTool) Name() string { return "file_read" }`），未展示的行段用与现有截断提示同风格的省略标记标出（如 `[omitted 41-118: 3 funcs, 1 type]`），并在结果首行写明“这是大纲，不是文件全文”。只对 `lang.go` 已声明语言的源码文件生效；其他文件（md/yaml/sh/未知扩展）回退为当前窗口行为，并在结果里说明回退原因。
2. **多段范围读**：`paths` 的每一项允许可选的行段后缀 `path:start-end`（一段或多段用逗号连接，如 `internal/wiring/runtime.go:490-530,540-560`，可混用不带后缀的整段 path）。带后缀的项使用自己的行段，不带后缀的项继续用 `offset/limit`；两种形式可以在一次调用里混用。这样 T4 那种“同一文件取多处”的往返可以合并成一次调用。
3. **不改变默认行为**：不带 `outline`、`paths` 不带后缀时，输出与现在完全一致（现有 `file_window_test.go` 的契约不变）。

### 手段 B：Go 符号声明清单工具 `file_symbols`

新增只读工具 `file_symbols`：

| 参数 | 说明 |
|---|---|
| `path`（必填） | 一个 Go 文件或目录；目录时在该目录内按 Go 文件递归（跳过 vendor/.git 与不可读路径） |
| `name`（可选） | 只回声明名匹配该子串的项（如 `Read`，对应 T2 的“所有 Read* 函数”） |
| `kind`（可选） | `func`/`method`/`type`/`const`/`var` 之一或逗号组合；空为全部 |
| `include_tests`（可选，默认 `false`） | 是否包含 `_test.go`；默认 false 直接对应实测里“测试文件噪声”的浪费 |
| `limit`（可选） | 结果条数上限，超出显示截断提示与 `offset` 续读方式 |

- 实现用 `go/parser`（`ParseFile` + `parser.SkipObjectResolution` 之外的最小模式），**只解析声明**，不做类型检查、不解析被 import 的包、不缓存；
- 输出为每条 `file:line kind name signature`；目录输入按文件路径排序，保证结果稳定可比；
- 非 Go 文件/目录返回明确的“仅支持 Go”错误消息（不静默空结果），并在工具描述里写明适用范围；
- 该工具只读（`ToolMeta{Category: CategoryRead, ReadOnly: true}`），所有模式（含只读 run）注册，进 `acpserver` 只读分类表。

### 为什么不选其余手段（对照报告第 4 节）

- 仓库概览缓存、语义检索、grep 相关度排序、只读工具并发：本轮无收益证据或已有能力，暂不做，落地本设计后按同一指标复测。
- 辅助小模型（输出整理/命令检查）：11 次运行里 0 次 shell 调用、0 次噪声型大输出，现有 spill/折叠机制已兜住唯一一次大输出（T3 档 A `spills 1`），没有触发样本；见 D6。

## 架构

| 组件 | 位置 | 变化 |
|---|---|---|
| 读工具 | `pkg/agentcore/tools/native/file_read.go` | 新增 `outline` 参数与 `paths` 行段后缀；窗口语义不变 |
| 语言声明 | `pkg/agentcore/tools/native/lang.go`、`lang_go.go` | Go 声明增加“声明清单入口”，供 `file_read` 大纲与 `file_symbols` 共用 |
| 新工具 | `pkg/agentcore/tools/native/file_symbols.go` | `go/parser` 声明清单；独立单测 |
| 注册接线 | `internal/wiring/runtime.go`（与现有 native 只读工具同一处） | 注册 `file_symbols`；只读分类与审批表随之更新 |
| 描述与提示 | 工具 `Description()`/`Schema()` | 只在工具描述里写明用法；**不动** `internal/wiring/prompt.go` 的稳定系统提示词 |
| 观测 | `internal/personal/analyze.go` | 不改；沿用 `steps`/`re-reads`/`tokens` 做回归指标 |

依赖方向：`native` 只用标准库（`go/parser`、`go/token`）；`internal/wiring` 依赖 `native`，不反向。`pkg/agentcore` 不依赖 `internal/*`（由 `layering_test.go` 守住），本设计不引入跨越。

## 关键流程

### 1. 大纲读（`file_read` + `outline=true`）

1. 解析参数（path、是否 outline）；`outline` 与 `offset/limit` 同给时以 path 的行段后缀为准，其次 `offset/limit`，其余部分给大纲。
2. 语言识别：按扩展名找 `lang.go` 声明的语言；未声明 → 走现有窗口逻辑并在结果首行说明“不认识的语言，按窗口返回”。
3. 声明提取：Go 用 `go/parser` 的声明位置；其他已声明语言用其声明入口（本期只实现 Go，其余语言回退窗口并说明）。
4. 渲染：按行号排序的声明行 + 省略标记（含省略区间与其中的声明计数）+ 文件总行数；超出 `pageBytes` 时按现有字节上限截断并提示续读方式。

### 2. 多段范围读

1. `paths` 每项先按后缀语法切分；无后缀 → `offset/limit` 窗口；有后缀 → 该项自己的行段列表。
2. 每段用现有 `readLineWindow` 读取，段间用省略标记分隔；全部读不到内容时报错并指出具体段。

### 3. 符号清单（`file_symbols`）

1. 解析 `path`：文件 → 直接解析；目录 → 递归收集 `*.go`（默认排除 `_test.go` 与 vendor/.git）。
2. `go/parser` 解析失败的文件不整体失败：记录 `file:line error` 一行，继续处理其他文件。
3. 过滤 `name`/`kind`，按 `file:line` 排序输出；命中数超过 `limit` 时给出截断提示与下一步（收窄 `name`/`kind`）。

### 4. 失败与边界

- 不存在的路径、二进制文件、目录（对 `file_read`）沿用现有错误消息风格。
- 大纲模式下文件小于阈值（建议 300 行，见 Q1）时直接返回窗口内容并说明“文件不大，已返回全文窗口”，避免多一次往返。
- 新工具的 schema `properties` 必须非空，零参数情况也保留可选参数（既有规则）。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 只在内置只读工具上加结构化视图，不引入索引/模型/外部进程 | 实测浪费在“读文件与找符号”，不在“缺少外部工具”；档 D 证明给了 shell 也不用 |
| D2 | 大纲是 `file_read` 的**可选参数**而非默认行为 | 避免改变既有窗口/截断契约与 `file_window_test.go`，也让模型按需选择 |
| D3 | 多段范围用 `paths` 项的行段后缀，不新增参数组 | 现有 `paths` 已经多路径，加后缀是兼容扩展；不破坏纯文件名（含冒号的文件名在 Windows 上不可用，但 `D:\...` 已在现有路径解析里处理，需要保留驱动器盘符判断） |
| D4 | 符号检索单独做 `file_symbols`，不塞进 `file_grep` | grep 语义是文本匹配，混入 AST 会让同一 pattern 的含义随文件类型变化，行为不可预测 |
| D5 | `include_tests` 默认 false | 直接对应实测第 3 类浪费（测试文件噪声）；需要时显式打开 |
| D6 | 辅助小模型（`suag-5qb4`）本版**暂不做** | 实测 0 条 shell 命令、0 次构建/测试/日志型大输出（唯一大输出由现有 spill 兜住），没有触发样本；确定性规则（现有输出治理 + approval/危险删除拦截）已覆盖能确定的场景。触发条件：探索类任务里出现构建/测试/日志输出或 shell 使用量上来后，用同一套 trace 指标 A/B 复测再决定 |
| D7 | 不改稳定系统提示词与审批语义 | 扩大范围且违反本批“不改产品代码之外语义”的边界 |
| D8 | 度量用现有 `trace --analyze`（steps/re-reads/tokens） | 已有字段足够回归；“读而未用”需要新增事件关联，成本高，不做 |

## 安全、数据、运维与回滚

- 安全：两个手段都是只读本地文件访问，走现有 `file_read` 的 workspace/read-roots 限制与路径解析（`pathresolve.go`、`readroots.go`），不新增写路径、不新增网络、不新增持久化状态。
- 数据：不写数据库、不写缓存、不落盘中间产物；`file_symbols` 只在内存里解析。
- 运维：无新配置项、无迁移、无后台任务。
- 回滚：两个手段都可用一次提交整体回滚（删除 `file_symbols.go` 与 `file_read` 的新参数即可），回滚后工具 schema 与行为回到当前状态；因为不改配置文件与数据库 schema，没有兼容迁移问题。
- 风险控制：`outline` 与行段后缀都要有“旧调用方式输出不变”的测试；新工具必须覆盖“非 Go 文件/不存在路径/目录/解析失败文件”的错误用例，不允许静默空结果。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 大纲的生效阈值（多少行以上才返回大纲）：200/300/500？ | 300 行；小于阈值直接返回窗口，避免小文件多一次往返 |
| Q2 | 工具名 `file_symbols` 与参数名（`name`/`kind`/`include_tests`/`limit`）是否符合使用习惯 | 倾向 `file_symbols`；若用户希望更少工具，可退化为 `file_read` 的 `outline=decls` 模式 |
| Q3 | 符号清单是否应覆盖依赖模块缓存（`D:/env/gopath/pkg/mod`，已在只读 roots 内） | 倾向覆盖：T4 实测 7 次读模块缓存文件，一次符号清单即可列出依赖 API |
| Q4 | 是否同时把「只读运行没有 shell、但环境说明列出 rg」的错配一起修（提示只在可用时列出，或只读模式明确说明命令不可用） | 本设计不改；建议单独立 issue，因为涉及稳定提示词与既有 preferred 设计 |

## 结论与人工计划 Gate

- 结论：本期只做手段 A（`file_read` 大纲 + 多段范围）与手段 B（`file_symbols`），两者互相独立、可分别提交与回滚；两者都不引入新依赖、不改稳定提示词、不改审批语义。
- 收益预期：按实测的重复读与“猜正则”浪费，T1/T4 类任务的步数应下降（目标：T4 类 30+ 步降到 20 步以内，重复读次数降到 1 次以内）；验收用同一套 4 个任务 + 同一模型复测，并且必须同时报告方差（单次运行噪声 ±3–5 步）。
- 人工 Gate：本设计（Draft 0.1）与 `docs/plans/2026-10-18-explore-efficiency-plan.md`（Draft 0.1）需用户审批后才进入实现；审批前不改任何产品代码。
