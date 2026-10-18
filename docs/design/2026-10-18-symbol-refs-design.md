<!-- template_id: design; template_version: 1.1.1 -->
# 符号引用检索设计

> 状态：Draft 0.1 / 待用户审批

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-18 | suga（gofer job） | 依据 `docs/research/2026-10-18-explore-efficiency.md` §7 复测（T4 31→33.5 步未降、`file_symbols` 在追调用链任务 0/2 使用）与 issue `suga-gxkw`，设计一个独立只读工具 `file_refs`：按标识符列出引用位置（Go 用 go/parser + go/ast 分类，其他语言用词边界近似），不做类型检查、不做跨语言调用图、不引 cgo/tree-sitter/LSP。 |

### 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：把“谁调用了 X、X 在哪被用”这一类追调用链问题从“猜正则 + 反复读文件”变成一次工具调用拿到跨文件的位置清单，从而降低探索阶段的模型往返步数；手段只加在内置只读工具上，不新增依赖、索引、缓存或外部进程。
- allowed_scope：`pkg/agentcore/tools/native`（新增 `refs.go`/`refs_go.go`/`refs_lines.go`，`symbols_mask.go` 增一个行内遮蔽薄函数，`file_symbols.go` 描述加一句交叉链接）、`internal/wiring` 的工具注册与分类表、`internal/acpserver` 的策略表、对应单元测试与 Linux 伪终端冒烟场景、真实模型复测、文档与 CHANGELOG。
- non_goals：不做类型检查/类型推断/接口实现查找、不做跨语言调用图、不做全仓索引或持久缓存、不引入 cgo/tree-sitter/gopls/LSP、不改 `file_grep`/`file_symbols`/`file_read` 的既有语义、不改稳定系统提示词与审批语义、不新增分析指标。
- expansion_policy：DEFER_OR_REQUEST。若需要改变现有工具 schema 的既有字段语义、改稳定系统提示词、改审批语义或引入新的持久化状态，停止实现并回到设计 Gate。
- review budget：小；复用现有符号提取层、遍历与输出治理路径，不新增抽象层。
- 停止条件：用户批准本设计与对应计划前不实施。

## 背景与目标

issue `suga-gxkw` 记录：`docs/research/2026-10-18-explore-efficiency.md` §7 的 v0.32.0 复测里，T4（`--read-only/--ro` 到写工具被禁用的调用链）从基线 31 步变成复测均值 33.5 步（区间 32–35），**没有下降**；`file_symbols` 在这两个运行里 0/2 使用，§7.4 的结论是“原因不是描述问题，而是能力边界：T4 问的是引用/调用关系与实现体，`file_symbols` 只列声明位置”。同一份报告 §7.4 建议 4 提出：`file_symbols` 的描述应写明“用于列出声明，不列调用关系”，并在要做下一步优化时优先补“引用位置”这一类能力。

本设计要回答的问题是：给模型一件“按符号名列出使用点（谁调用了它、谁引用了它）”的只读工具，能不能把 T4 类追调用链任务的步数压下来。

目标（用户可见结果）：

1. 问“谁调用了 X / X 在哪被用”时，一次调用就拿到跨文件的位置清单（`文件:行` + 类别 + 命中行原文），不必再自己拼正则、逐个文件取窗口。
2. 结果能区分“定义处”“调用处”“选择器用法（`pkg.Name`、`x.Name`）”“其他引用”，并给出限定符（是包名还是值/接收者），减少模型在“这条命中到底算不算”上的往返。
3. 不支持的文件类型、跨包漏查、结果被截断这三种情况都给可执行的下一步提示，不静默给空结果把模型带偏。
4. 现有工具语义不受影响：`file_grep`/`file_symbols`/`file_read` 的参数、输出与截断契约不变；只读语义、审批语义与稳定系统提示词前缀不变。

## 名词

- **引用（reference）**：一个标识符名字在源码里出现的非注释、非字符串字面量的位置。
- **定义处（def）**：名字被绑定/声明的位置：函数名、方法名、类型名、常量/变量名、字段名、参数与接收者名、`:=` 左值、标签、import 名。本设计把局部变量与字段也算 def，因为不解析类型时它们结构上就是绑定位置。
- **选择器（selector）**：Go 的 `X.Name` 写法。`X` 叫限定符（qualifier）：可能是 import 的包名，也可能是值、字段或方法接收者。
- **approx（近似）**：结果来自“词边界文本匹配 + 注释/字符串遮蔽”而不是解析器；名字与位置可信，但不是“同一个对象”的证明。
- **误报/漏报**：误报 = 命中的是另一个同名对象（局部变量、字段、其他包的同名符号）；漏报 = 使用点不在本次 `path` 范围内（例如定义在 A 包、使用在 B 包）。
- **T4 任务**：调研报告 §1.3 的第 4 个任务（`--read-only` 到写工具禁用的调用链），本设计的主要复测靶子。

## 范围与非目标

### 范围

- `pkg/agentcore/tools/native/refs.go`（新）：`file_refs` 工具本体（参数、范围解析、遍历、输出与上限）。
- `pkg/agentcore/tools/native/refs_go.go`（新）：Go 的引用提取与分类（`go/parser` + `go/ast`，不做类型检查）。
- `pkg/agentcore/tools/native/refs_lines.go`（新）：TS/JS、Python、Java 的近似引用匹配（词边界 + 遮蔽）。
- `pkg/agentcore/tools/native/symbols_mask.go`：新增 `maskInline`（遮蔽单行字符串内容与行注释尾部），供近似引用匹配使用。
- `pkg/agentcore/tools/native/file_symbols.go`：描述加一句“它只列声明，不列使用点；看哪里用了某名字用 `file_refs`”。
- `pkg/agentcore/tools/native/catalog.go`：注册 `file_refs`（只读运行按 `BuildTools(env, false)` 从 catalog 取只读工具，因此注册即对只读 run/chat/ACP 生效）。
- `internal/wiring`、`internal/acpserver`：工具清单、只读分类、审批表、ACP 策略表与 schema `properties` 断言的同步（含 `internal/wiring/subagent.go` 的 explore/research 工具名单）。
- 测试：native 单元测试（`t.Run` + `gookit/goutil/x/assert`）、接线分类表测试、Linux 伪终端冒烟场景（`scripts/ptysmoke` + `fakellm.py`，用 `suga trace <sid> --json` 断言工具事件）。
- 验证：用调研报告里的 T4 任务做真实模型复测（同一模型每次 ≥2 次），并新增一个直接靶向“谁调用了 X”的小任务。
- 文档：本设计、`docs/plans/2026-10-18-symbol-refs-plan.md`、CHANGELOG「未发布」。

### 非目标

- 不做类型检查、类型推断、接口实现关系、方法集解析（即不引入 `go/types`）。
- 不做跨语言调用图、全仓索引、持久缓存、仓库概览缓存。
- 不引入 cgo、tree-sitter、gopls 或任何 LSP 客户端（结论见 D6）。
- 不改 `file_grep`/`file_symbols`/`file_read` 的既有语义：不给 grep 加“跳过注释与字符串”，不改 `file_symbols` 的输出形状，不把引用做成 `file_read` 的参数。
- 不改稳定系统提示词（含只读运行的环境说明段）、不改审批与写工具语义。
- 不做“读而未用”等新指标；不新建评测框架。

## 已确认事实与规范

### 实测事实（来自调研报告 §7，本设计的依据）

| 事实 | 数据 |
|---|---|
| T4 未达成计划目标 | 基线 31 步 / 50 次工具调用 → 复测 35、32 步（均值 33.5，区间 32–35）/ 55、56 次调用；输入 token 1.49M → 1.70M、1.82M |
| `file_symbols` 的使用率 | 复测 10 次运行里 4 次被使用；T2（列 `Read*` 清单）2/2 都主动用了它，步数均值 -37.5%；**T4 两次 0 使用** |
| `outline` 可选参数的使用率 | 0/10：作为 `file_read` 的第 5 个可选参数从未被触发 |
| 追调用链任务的真实痛点 | 报告 §7.4：T4 要的是“引用/调用关系与实现体”，声明清单给不出“谁用了 `ReadOnly`”；模型仍反复取行段拼全貌（T4-1 10/30 次读是重复文件，`cflag.go`×4、`cflag/util.go`×4） |
| 报告给出的下一步 | §7.4 建议 4：`file_symbols` 描述写明“不列调用关系”；引用位置是“比再改一次 `file_read` 更值钱的方向” |

### 现有代码事实（行号对应 HEAD `1501566e`）

| 事实 | 位置 |
|---|---|
| 符号提取层：一个语言一条注册项（扩展名、approx、提取函数、ctags 语言名），入口 `ExtractSymbolsAt` 按“ctags → 内置提取器 → 明确拒绝”三级 | `pkg/agentcore/tools/native/symbols.go:66-96`（注册表 `:85`）、`symbols.go:120-141` |
| Go 声明提取只用 `parser.SkipObjectResolution` 读声明，不做类型检查、不解析被 import 的包 | `pkg/agentcore/tools/native/symbols_go.go:25-71`（`SkipObjectResolution` 在 `:27`） |
| `maskSpans` 只遮蔽**跨行** span（块注释、多行字符串/文档字符串），单行字符串与行注释**有意留给**各语言的声明识别器自己跳过 | `pkg/agentcore/tools/native/symbols_mask.go:14-20`、`:20-107` |
| `file_symbols` 的默认上限 200、最多列 5 个解析错误；参数 `path`（必填）/`name`/`kind`/`include_tests`/`limit` | `pkg/agentcore/tools/native/file_symbols.go:18-25`、`:56-87` |
| `file_symbols` 的范围解析（工作区或只读 root，文件/目录两路）、遍历（`walkWorkspace` + `searchTimeout` + `maxSearchFileBytes`，默认排除测试文件）、渲染与 notes（approx、截断、跳过测试、不可解析、超时）风格 | `:103-133`、`:149-190`、`:238-305`；测试文件名规则 `:386-399` |
| `file_grep` 的语义与上限：RE2 正则、`output=files`、`max_results` 默认 100/上限 1000、单行 trim 300 字节、`CategorySearch` | `pkg/agentcore/tools/native/grep.go:17-22`、`:34-44` |
| 只读运行的工具集来自 catalog 的只读项 | `pkg/agentcore/tools/native/catalog.go:76-92`、`:142-155`；`internal/wiring/runtime.go:795-806` |
| 新增工具必须同步的清单/分类表 | `internal/wiring/capabilities_test.go:42,59,100`、`internal/wiring/readonly_test.go:24`、`internal/wiring/approval_table_test.go:82`、`internal/acpserver/policy_table_test.go:72`、`internal/wiring/subagent.go:50-51` |
| 分类到 ACP 工具种类的映射 | `internal/acpserver/policy.go:440`（`CategorySearch → ToolKindSearch`） |

### 适用规则

- 工具 schema 的 `properties` 不能是空对象（`internal/wiring` 的 `TestToolSchemasDeclareProperties` 守住）；零参数工具也要有可选参数。
- 单个 Go 文件不超过 1000 行；关键逻辑加注释；测试用 `gookit/goutil/x/assert`，同一方法多用例用 `t.Run`。
- 终端交互行为用 Linux 伪终端冒烟（`make smoke-chat`）验证并做反向验证；Windows 主机跑不了 pty。
- `pkg/agentcore` 不得依赖 `internal/*`（`pkg/agentcore/layering_test.go`）。

## 总体方案

只做一件新工具，加上让它可以被复用的两个薄函数与一行交叉链接。

### 1. 工具形态：独立工具 `file_refs`（推荐），不做 `file_symbols` 的模式

结论：新增独立只读工具 `file_refs`，参数为“要查的名字 + 在哪查”，描述里直接使用“who calls X / where is X used”这类动词。

理由：

1. **证据**：上一批的复测直接对照了两种形态——作为 `file_read` 第 5 个可选参数的 `outline` 被用 0/10，而“只有它能做这件事”的独立工具 `file_symbols` 被用 4/10，且在正好需要它的 T2 上 2/2 主动使用。可选参数形式的反面教材就在同一次复测里。
2. **参数形状不同**：`file_symbols` 的 `path` 是“被列清单的目标”；引用检索需要 `name`（被查的标识符）与 `path`（在哪里查）两个正交维度。塞进一个工具会得到“`path` 有时是被查文件、有时是被查名字的宿主”的含糊 schema，模型更难判断何时该用。
3. **输出形状不同**：一个是“声明清单”，一个是“使用点清单”。用模式参数在同一次返回里切换两种语义，会让模型对结果构成产生不确定（复测里已有“读了工具结果又反复确认”的行为）。
4. **描述分工**：`file_refs` 用“谁调用/哪里被用”的动词，与 T4 的问法对齐；同时在 `file_symbols` 描述里加一句“只列声明，不列使用点，看引用用 `file_refs`”，修掉报告 §7.4 说的“模型在追调用链时既没用上、也可能误用”的问题（成本一行文本）。

被否的备选形态（记录以免反复讨论）：`file_symbols` 增加 `refs=Name` 模式、`file_grep` 增加 `refs` 参数、`file_read` 增加 `refs` 参数。前两者的 schema 与语义会随参数漂移，后者违反“`file_read` 是窗口读取”的既有契约（上一批已明确不改其窗口语义）。

### 2. Go：`go/parser` + `go/ast` 按标识符分类（不做类型检查）

实现：每个候选文件用 `parser.ParseFile(fset, file, src, parser.SkipObjectResolution)`（与 `symbols_go.go` 同一最小解析模式），然后 `ast.Inspect` 收集名字等于目标的 `*ast.Ident`，按父节点/上下文分类：

| 类别 | 判定 |
|---|---|
| `def` | `FuncDecl.Name`、`TypeSpec.Name`、`ValueSpec.Names`、`Field.Names`（结构体字段、接口方法、参数、返回值、接收者）、`:=` 赋值左值、`LabeledStmt`、`ImportSpec` 名 |
| `call` | `CallExpr.Fun` 直接是目标标识符（`f(...)`），或 `Fun` 是 `SelectorExpr` 的 `Sel`（`x.M(...)`、`pkg.F(...)`） |
| `sel` | 其他 `SelectorExpr.Sel`（`x.Field`、`pkg.Name` 非调用） |
| `ref` | 其余（类型引用、复合字面量、赋值右侧、嵌入名、表达式里的裸标识符） |

- 限定符（qualifier）：`call`/`sel` 类别额外记录 `X` 的源码文本。若 `X` 是本文件 `import` 的包名（含显式别名；`_`、`.` 除外），标记为包限定符（输出写 `(pkg:X)`），否则是值/字段/方法接收者限定符。这一步只用到同一文件的 import 列表，不需要类型信息，却能把 `runtime.ReadOnly`（包级符号）与 `cfg.ReadOnly`（字段）分开，明显降低“谁用了这个选项”类问题的噪声。
- 定位：用 `token.FileSet` 的 `Position` 取行号，用与 `symbols.go:146-159` 同一份 `sourceLines` 取该行的原文（`strings.TrimSpace`，超长行 trim 到与 grep 一致的 300 字节）。

**误报（本设计明确接受，并在输出里暴露）**：不做类型检查意味着以下几类都会命中目标名字，而它们未必是同一个对象：

- 同名局部变量、参数、字段、接口方法；
- 其他包里同名（不同对象）的导出符号；
- 不同类型上的同名方法（Go 里 `x.Name()` 的接收者类型不作区分）。

输出侧的提示方式（不试图消除误报）：

1. 每条命中带类别（`def`/`call`/`sel`/`ref`）与限定符；
2. 每条命中带该行原文，模型读一眼就能判断是不是同一个对象；
3. 结果尾部固定一行说明：`[no type resolution: a match may be another object with the same name (a local, a field, another package's symbol); the shown line decides]`；
4. 工具描述里写明“Go 按标识符文本定位、不做类型检查”。

漏报（本设计明确接受，并在零命中时提示）：引用检索只在给定 `path` 内查找，定义在 A 包、使用在 B 包时查 A 包会得到空结果，所以零命中的结果会附 `[no references in <path>; the name may be used from another package/directory - try path=. or the defining package's parent]`。

### 3. 其他语言：词边界 + 遮蔽的近似匹配（标 approx）

TS/JS、Python、Java 用文本近似，与 `file_symbols` 的“轻量识别 + `approx`”策略一致：

1. 先用 `maskSpans`（`symbols_mask.go:20-107`）遮蔽跨行 span（块注释、多行字符串、Python 文档字符串）。
2. 新增 `maskInline`（同一文件内的薄函数）：遮蔽**单行**字符串的内容与行注释尾部（brace 语言 `//`，Python `#`），保留换行与行号。`maskSpans` 现在有意不处理这两种（由各语言声明识别器自己跳过），而引用检索没有“只在声明行匹配”这层保护——没有 `maskInline`，`"ReadOnly"` 字面量和 `// ReadOnly is ...` 注释都会变成假命中。
3. 在遮蔽后的文本上做逐行词边界匹配：`(^|[^0-9A-Za-z_$])` + 名字（`regexp.QuoteMeta`）+ `([^0-9A-Za-z_$]|$)`。不用 Go 的 `\b`，因为 `\b` 把 `$` 与 `-` 当边界，会漏掉 `$name`、`foo-name` 这类语言里的合法标识符片段。
4. 近似结果的类别统一为 `ref`（不做分类），结果尾部标 `[approx: ... matches come from word-boundary text matching, not a parser; confirm with file_grep]`。

**ctags 的用法与限制**：主机有 `universal-ctags` 时 `file_symbols` 会优先用它；但 ctags 输出的是**声明/tag**位置，不提供调用方（`--output-format=json` 的 tag 是定义；ctags 的 `--references` 需要额外配置且只标出“定义/引用”分类，不可靠）。因此引用检索**不依赖 ctags**，有它也不改变 `file_refs` 的行为；这一点写进工具描述，避免模型误以为能靠 ctags 拿引用。

**gopls/LSP：不做**。理由：需要常驻进程与每语言一个服务、需要安装与生命周期管理；只读运行没有 `shell_exec`，拿不到这类外部能力；T4 类问题不需要类型信息（“类别 + 限定符 + 命中行”已足够）；LSP 方向已由 `suga-cm5e` 持有。

### 4. 范围与参数

| 参数 | 说明 | 默认 |
|---|---|---|
| `name`（必填） | 要查的标识符。Go 按标识符精确匹配（区分大小写）；其他语言按词边界匹配（同样区分大小写） | – |
| `path`（可选） | 在哪查：单个文件、目录（递归）或工作区根；也接受只读 root 下的绝对路径（依赖模块缓存与 `file_symbols` 同理） | 工作区根 |
| `include_tests`（可选） | 是否包含 `_test.go`、`*.test.ts`、`test_*.py`、`*Test.java`（复用 `file_symbols.go:386-399` 的规则） | `false` |
| `limit`（可选） | 命中条数上限 | `200`（与 `file_symbols` 一致） |

- 三档范围都支持；推荐用法是“给了某个包/目录就查它”，工作区级默认值让模型不必先决定“要查哪个包”（T4 里模型并不知道用到这个选项的包在哪），代价由下面的四道闸兜住。截断时给出 `narrow path` 的提示。
- 遍历沿用 `walkWorkspace`（`.gitignore` 语义、`.git`/`node_modules`/`vendor`/`dist` 跳过、`searchTimeout` 超时、`maxSearchFileBytes` 大小上限），错误沿用现有风格：目录下没有任何支持的文件 → 报错并列出支持语言；单文件输入且类型不支持 → 直接报错（与 `file_symbols` 一致，不静默空结果）；解析失败的文件记一行（最多 5 行 + 计数）继续处理。
- **范围是否包含依赖模块缓存**：包含（只读 root 内可用绝对路径打开），与上一批 `file_symbols` 的 Q3 决定一致；不做缓存/索引，每次重新读。

### 5. 输出格式与上限

每条命中一行：

```
internal/wiring/runtime.go:331 def                  ReadOnly = allowWrite && !readOnly
internal/wiring/runtime.go:456 sel(pkg:registry)     registry.Get("shell_exec")
internal/cli/run.go:224 call(check)                  check(cfg)
internal/cli/run.go:207 ref                          ReadOnly bool
```

规范：`<path>:<line> <class>[(<qualifier>)] <命中行原文>`；`class` ∈ `def|call|sel|ref`；限定符只在 `call`/`sel` 出现，`pkg:` 前缀表示它是该文件 import 的包名；命中行原文 trim 两端空白、最多 300 字节（与 `file_grep` 的 `maxGrepLineBytes` 一致）。结果按 `path`、`line` 排序，保证同一查询可比。

- `limit` 默认 200；超过时停止（遍历在超出一条后即停，不扫完整个目录），尾部提示 `[showing 200 matches; more matched - narrow path, or use file_grep with a word-boundary pattern for the full list]`。
- 尾部 notes 与 `file_symbols` 同风格：不可解析文件、`approx` 提示、跳过的测试文件数、跳过的不支持类型数、超 `maxSearchFileBytes` 的文件数、`.gitignore` 忽略提示、超时提示、无类型解析提示；零命中时给“可能在其他包/目录”的下一步提示。
- 字节上限沿用现有输出治理（`TruncateHeadTail(rawLimit(t.maxOut))`），与 `file_symbols` 相同。

**为什么不直接 `file_grep` 一个词边界正则就够**：

1. 词边界正则要模型自己写对（Go 的 `$`/语言差异、`ignore_case` 取舍），写错是一次失败调用（复测里 T4 每次有 1–3 次失败调用，正是这类代价）；
2. grep 会命中注释与字符串里的同名文本，模型还得自己排除——`file_refs` 把注释/字符串先遮蔽掉（Go 因为按 AST 取标识符天然排除）；
3. grep 给不出“这条是定义、这条是调用、这条是 `pkg.` 选择器、限定符是包还是值”的分类；
4. grep 没有默认排除测试文件（实测第 3 类浪费就是测试文件噪声）；
5. 一次 `file_grep "\bReadOnly\b"` 在**已知文件**里找一处用法与 `file_refs` 等价——`file_refs` 的价值在“跨文件的谁用了它”与分类降噪，本设计不声称它在单点查找上更优。

### 6. 成本控制（工作区级扫描可行）

工作区级引用检索如果对每个 Go 文件都跑 `go/parser` 会明显慢（本仓库 + 依赖缓存是数千文件）。因此先做**字节级预过滤**：

1. 每个候选文件先用编译好的词边界正则（与近似语言同一套）在原始字节上做一次候选筛选；
2. 只有命中的 Go 文件才 `go/parser` 解析并走 AST 分类（AST 分类顺带把注释/字符串里的假命中排掉）；
3. 不命中的文件直接跳过，成本与 `file_grep` 同阶。

总成本由四道闸封顶：`searchTimeout`（遍历超时）、`maxSearchFileBytes`（跳过大文件）、`limit`（命中条数）、`rawLimit(maxOut)`（结果字节数）。

## 架构

| 组件 | 位置 | 变化 |
|---|---|---|
| 引用提取（Go） | `pkg/agentcore/tools/native/refs_go.go`（新） | `go/ast` 走查 + 四类分类 + 包名限定符判定 + 字节预过滤 |
| 引用提取（近似） | `pkg/agentcore/tools/native/refs_lines.go`（新） | `maskSpans` + `maskInline` + 词边界逐行匹配 |
| 行内遮蔽 | `pkg/agentcore/tools/native/symbols_mask.go` | 新增 `maskInline`（owner 内薄函数，供引用检索用） |
| 工具本体 | `pkg/agentcore/tools/native/refs.go`（新） | `FileRefsTool`：参数校验、范围解析、遍历、排序/截断、notes 渲染 |
| 语言归属 | `pkg/agentcore/tools/native/symbols.go:85` 的 `symbolLanguages` | 直接复用（扩展名与 mask 风格），`file_refs` 支持的语言集合与 `file_symbols` 完全一致 |
| 注册接线 | `pkg/agentcore/tools/native/catalog.go`、`internal/wiring/*`、`internal/acpserver/*`、`internal/wiring/subagent.go` | 增加一行注册 + 同步清单/分类表测试 + explore/research 工具名单 |
| 描述交叉链接 | `pkg/agentcore/tools/native/file_symbols.go` | 描述加一句指向 `file_refs` |
| 观测 | `internal/personal/analyze.go` | 不改；沿用 `steps`/`tools`/`re-reads`/`tokens`/`spills` 做回归指标 |

依赖方向：`native` 只用标准库（`go/ast`、`go/parser`、`go/token`、`regexp`、`strings`）；`internal/*` 依赖 `native`，不反向；不引入新的持久化状态，也不跨越 `pkg/agentcore` 的分层边界。

`file_refs` 的 `ToolMeta`：`{Category: tools.CategorySearch, ReadOnly: true, Prefetchable: true}`。选 `CategorySearch` 的理由：它的语义是“在文件内容里按名字检索”，与 `file_grep`/`file_find` 同类（ACP 映射为 `ToolKindSearch`），而不是像 `file_read`/`file_symbols` 那样“读某个目标的全部内容”；`ReadOnly` 让它在只读运行与免审批路径里与其它只读工具一致。

## 关键流程

### 1. 一次 `file_refs` 调用

1. 校验参数：`name` 必填（空 → 报错）；`limit` 非法值回落到默认；解析 `path`（空 = 工作区根）经 `resolveRead` 得到工作区/只读 root 内的目标。
2. 单文件输入：类型不支持 → 直接报错并列出支持语言；支持 → 按该语言提取，`approx` 标记随语言而定。
3. 目录/工作区输入：`walkWorkspace` 遍历；`include_tests=false` 时跳过测试文件并计数；不支持的扩展名计数跳过；超大文件与不可读文件分别计数/记错。
4. 字节预过滤 → Go 走 AST 分类，其他语言走词边界近似。
5. 排序（path、line）→ 按 `limit` 截断 → 拼接 notes → 字节上限兜底。

### 2. Go 分类（一次文件内的顺序）

1. `parser.ParseFile(..., parser.SkipObjectResolution)`；失败 → 记一行错误，继续下一个文件。
2. 收集该文件的 import 名（显式别名优先，否则取包路径最后一段；跳过 `_`、`.`）。
3. `ast.Inspect`：命中名字相等的 `Ident` → 按父节点判定类别（见总体方案 2）；`SelectorExpr` 的 `Sel` 再判定限定符是不是包名。
4. 产出 `{File, Line, Class, Qualifier, Text}`。

### 3. 近似语言匹配

1. `src` → `maskSpans(src, 该语言的 maskStyle)` → 逐行 `maskInline`。
2. 逐行跑词边界正则，记录行号与该行原文（原文取自未遮蔽的行，保证展示可读）。
3. 类别统一 `ref`，`Approx=true`。

### 4. 失败与边界

- 零命中：给“可能在其他包/目录”的下一步提示（不是 `no matches` 了事）。
- 结果被截断：说明还有更多、建议收窄 `path`（或退回 `file_grep`）。
- 遍历超时：结果标不完整（复用现有超时提示文案）。
- 二进制文件、目录当文件读等错误沿用现有 `file_read`/`file_symbols` 的错误风格。

## 安全、数据、运维与回滚

- 安全：只读本地文件访问，复用 `pathresolve.go`/`readroots.go` 的路径解析与 `os.Root` 读取（工作区与只读 root 边界不变，`os.Root` 阻止逃逸）；不新增写路径、不新增网络、不改审批与危险操作规则。
- 数据：不写数据库、不写缓存、不落盘中间产物；解析只在内存里进行。
- 运维：无新配置项、无迁移、无后台任务；成本上界由 `searchTimeout`/`maxSearchFileBytes`/`limit`/字节上限四道闸给出。
- 回滚：一次提交即可整体回滚（删除 `refs*.go`、撤掉 `catalog.go` 一行注册与 `file_symbols` 描述一行、还原分类表测试）；不改配置与数据库 schema，无需兼容迁移。
- 风险控制：
  - **误导致错**（误报让模型得出错误结论）：类别 + 限定符 + 命中行 + 固定提示，让判断权回到模型；描述里写明“不做类型检查”。
  - **工具不被使用**（最大风险，上一批 `file_symbols` 在 T4 就 0/2）：独立工具 + 描述动词对齐问法 + `file_symbols` 交叉链接 + 复测门（若 T4 复测仍 0 使用，按待确认 Q5 单独讨论提示词批，不在本设计内扩权）。
  - **工作区级扫描变慢**：字节预过滤 + 四道闸；单测里对“目录级扫描”设性能预算（见计划 T3）。
  - **漏报**（跨包）：零命中提示 + 描述里说明“只在 path 内查找”。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 独立工具 `file_refs`，不做 `file_symbols`/`file_grep`/`file_read` 的模式或参数 | 复测证据：可选参数 `outline` 0/10、独立工具 `file_symbols` 4/10；参数与输出形状正交，混在一起会让 schema 与返回语义含糊 |
| D2 | Go 用 `go/parser` + `go/ast` 按标识符分类，不做类型检查 | 标准库、无新依赖；只读运行没有 shell，跑不了 gopls；T4 类问题不需要类型信息 |
| D3 | 输出四类 `def`/`call`/`sel`/`ref` 并带限定符，不给类别加过滤参数 | 分类让“定义 vs 使用”一眼可辨；再加过滤参数会让 schema 与描述复杂化，收益未被证据支持 |
| D4 | 误报用“类别 + 限定符 + 命中行 + 固定提示”暴露，不试图消除 | 消除需要类型系统（非目标）；暴露成本低，模型可自判 |
| D5 | 其他语言用词边界 + `maskSpans`/`maskInline` 近似并标 `approx` | 与 `file_symbols` 的轻量识别策略一致；不引 tree-sitter/cgo |
| D6 | 不依赖 ctags 的引用能力、不做 gopls/LSP | ctags 只给声明/tag；LSP 需常驻进程与安装，只读运行无法获得，方向由 `suga-cm5e` 持有 |
| D7 | 范围支持单文件/目录/工作区（`path` 默认工作区根），默认排除测试文件 | 与 `file_grep` 的 `path` 默认一致，降低“先决定查哪个包”的门槛；测试文件噪声是实测第 3 类浪费；工作区级由预过滤与四道闸兜住 |
| D8 | 加字节级预过滤，命中的 Go 文件才解析 | 让工作区级引用检索的成本与 `file_grep` 同阶；避免数千文件的 AST 解析 |
| D9 | 只改工具描述（含 `file_symbols` 一行交叉链接），不改稳定系统提示词 | 与上一批边界一致；把工具写进只读环境说明涉及稳定提示词与 prompt 缓存代价，需另立一批（待确认 Q5） |
| D10 | 复用符号提取层与其输出风格（200 默认上限、notes 形状、`sourceLines`） | 避免两套遍历/遮蔽/渲染；owner 是 native 的符号提取层，不新建平行框架 |
| D11 | `CategorySearch` + `ReadOnly` | 语义上是“按名字在内容里检索”（与 `file_grep`/`file_find` 同类，ACP 映射 `ToolKindSearch`），不是“读某个目标的全部内容” |

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 工具名 `file_refs` 还是 `file_usages`/`file_references` | 倾向 `file_refs`（短、动词明确）；描述里写 “who calls X / where is X used”，与问法对齐 |
| Q2 | `limit` 默认值与 `file_grep`（100）还是 `file_symbols`（200）对齐 | 倾向 200（与 `file_symbols` 一致，引用命中通常比声明多）；超出给 `narrow path` 提示 |
| Q3 | 目录/工作区默认是否排除测试文件 | 倾向与 `file_symbols` 一致默认排除（`include_tests=false`）；查“测试里怎么用”时显式打开 |
| Q4 | Go 结果里是否保留 `def`（“定义在哪”与“谁用了它”一次给出） | 倾向保留：一行成本，且能省掉一次 `file_symbols`/`file_read` 定位 |
| Q5 | 是否把 `file_refs` 写进只读运行的环境说明段以提高使用率 | 本设计不改稳定提示词；若 T4 复测仍 0 使用，作为单独的提示词批（评估 prompt 缓存代价）讨论 |
| Q6 | 是否需要“按类别过滤”参数（只要 `call`） | 倾向不做（D3）；若复测显示模型在结果里自行过滤耗时明显，再单独评估 |

## 结论与人工计划 Gate

- 结论：本期只做 `file_refs` 一个只读工具（Go 精确分类 + 其他语言近似）、`maskInline` 一个遮蔽薄函数、`file_symbols` 描述一行交叉链接与接线/分类表同步；不做类型检查、不做跨语言调用图、不引 cgo/tree-sitter/LSP、不改稳定系统提示词与审批语义。
- 收益预期与验收：T4 任务（`--read-only` 调用链）复测步数从均值 33.5 降到 **≤22**（同模型 `opencode-go/deepseek-v4.1-flash`、只读档、每次 ≥2 次，且至少一次运行真的用了 `file_refs`）；新增一个直接靶向“谁调用了 X”的小任务，要求一次调用给出跨文件使用点清单；复测必须同时报方差（单次噪声 ±3–5 步）与未达成项。
- 人工 Gate：本设计（Draft 0.1）与 `docs/plans/2026-10-18-symbol-refs-plan.md`（Draft 0.1）需用户审批后才进入实现；审批前不改任何产品代码。
