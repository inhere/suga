<!-- template_id: plan; template_version: 1.2.0 -->
# 符号引用检索实施计划

> 状态：Draft 0.2 / 已批准实施

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-18 | suga（gofer job） | 依据 `docs/design/2026-10-18-symbol-refs-design.md` Draft 0.1 与调研报告 §7 的复测证据，拆分 Go 引用提取、多语言近似匹配、`file_refs` 工具与接线、伪终端冒烟、真实模型复测五段任务；不做类型检查/调用图/cgo/LSP。 |
| 0.2 | 2026-10-18 | 监督（Claude） | 用户批准实施（2026-10-18“批准”）：Q1–Q6 按当前倾向；A/B 实验（docs/research/2026-10-18-explore-compare.md）支持内置 file_refs 而非常驻图工具。 |

### 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：用一件新的内置只读工具 `file_refs` 把“谁调用了 X、X 在哪被用”变成一次调用，从而降低 T4 类追调用链任务的模型往返步数；不新增依赖、索引、缓存与外部进程，不改稳定系统提示词与审批语义。
- allowed_scope：见“范围、排除项与授权”。
- non_goals：类型检查/类型推断/接口实现查找、跨语言调用图、全仓索引与持久缓存、cgo/tree-sitter/gopls/LSP、`file_grep`/`file_symbols`/`file_read` 的既有语义、稳定系统提示词、审批与写工具语义、新分析指标。
- expansion_policy：DEFER_OR_REQUEST。需要改稳定系统提示词、既有工具 schema 字段语义、审批语义或引入持久化状态时，停止并回设计/计划 Gate。
- review budget：小；复用现有符号提取层与输出治理，不新增抽象层与框架。
- 停止条件：设计与本计划获用户批准前不实施；基线失败或代码事实与设计不一致时不进入实现。

## 目标与完成定义

目标：补上“按符号名列出使用点”的能力（设计 D1–D11），把 T4 类追调用链任务的步数压下来，同时不改现有工具语义与提示词。

完成定义：

1. 新增只读工具 `file_refs`：`name`（必填）+ `path`（默认工作区根，支持单文件/目录/只读 root 绝对路径）+ `include_tests`（默认 false）+ `limit`（默认 200）；输出 `path:line class[(qualifier)] 命中行`，按 path、line 排序；`ToolMeta{CategorySearch, ReadOnly: true, Prefetchable: true}`。
2. Go 用 `go/parser` + `go/ast`（`parser.SkipObjectResolution`）按标识符分类 `def`/`call`/`sel`/`ref`，`call`/`sel` 带限定符，且限定符是该文件 import 的包名时输出 `(pkg:X)`；**不做类型检查**，误报通过类别、限定符、命中行原文与固定提示暴露。
3. TS/JS、Python、Java 用 `maskSpans` + 新增 `maskInline`（单行字符串与行注释尾部遮蔽）+ 词边界匹配的近似实现，结果标 `approx`；不支持的类型/目录明确报错并列出支持语言，不静默空结果。
4. 成本可控：字节级预过滤（命中的 Go 文件才解析）+ `searchTimeout`/`maxSearchFileBytes`/`limit`/字节上限四道闸；单测里对目录级扫描有性能断言。
5. 接线完整：`catalog.go` 注册、只读运行计数（`internal/wiring/readonly_test.go` 6→7）、工具清单与审批表、ACP 分类表、explore/research 子 agent 工具名单、schema `properties` 非空断言全部同步；`file_symbols` 描述加一句指向 `file_refs`。
6. 验证：native 单测覆盖误报/边界/跨平台；Linux 伪终端冒烟新增场景并用 `suga trace <sid> --json` 断言，含反向验证；真实模型复测 T4（≥2 次）与新增“谁调用了 X”靶向任务，报告步数/工具分布/失败调用/token/墙钟与方差。
7. 每个功能点原子本地提交（`feat:`/`test:`/`docs:`），提交前核对 `git status --short` 与 `git diff --cached --name-only`；不 push、不打 tag、不改 `.gofer/tracker`。

## 范围、排除项与授权

- 范围（所有文件）：
  - `pkg/agentcore/tools/native/refs.go`（新，工具本体）
  - `pkg/agentcore/tools/native/refs_go.go`（新，Go 引用分类）
  - `pkg/agentcore/tools/native/refs_lines.go`（新，词边界近似）
  - `pkg/agentcore/tools/native/symbols_mask.go`（新增 `maskInline`）
  - `pkg/agentcore/tools/native/file_symbols.go`（描述一行交叉链接）
  - `pkg/agentcore/tools/native/catalog.go`（注册一行）
  - 单测：`refs_test.go`、`refs_go_test.go`、`refs_lines_test.go`（新，均在 `pkg/agentcore/tools/native/`）
  - `internal/wiring/capabilities_test.go`、`internal/wiring/readonly_test.go`、`internal/wiring/approval_table_test.go`、`internal/wiring/subagent.go`、`internal/acpserver/policy_table_test.go`
  - `scripts/ptysmoke/`（新场景与 `Makefile` 的 `smoke-chat` 接入）
  - `docs/research/2026-10-18-explore-efficiency.md`（新增复测小节）、`CHANGELOG.md`（「未发布」）
- 排除项（不做）：`go/types` 类型检查、接口实现查找、跨语言调用图、全仓索引/持久缓存、cgo/tree-sitter/gopls/LSP、`file_grep` 的语义改动（不加“跳过注释与字符串”）、`file_symbols`/`file_read` 的输出形状改动、稳定系统提示词（含只读环境说明段）、审批与写工具语义、新的分析指标、评测框架。
- `host_or_non_offline_action=REQUIRED`：Windows 主机的 `go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")`、`go test -count=1 ./internal/... ./pkg/...`、`staticcheck ./...`，Linux 容器的伪终端冒烟与反向验证，以及真实模型的 T4/R1 复测，都是完成 Gate 的外部环境动作；本计划阶段不执行，实施时按“任务”的验证列执行。
- 兼容边界：`file_grep`/`file_symbols`/`file_read` 的参数与输出契约不变；新增工具不新增配置项、不新增持久化；若实现需要改变这些，停止并回到设计 Gate。
- 授权：用户批准设计与本计划后才实施；批准只授权本 worktree 的本地修改与提交，不授权 push/tag/release、部署或 `.gofer/tracker` 修改。

## 输入与批准证据

- 设计：`docs/design/2026-10-18-symbol-refs-design.md`，Draft 0.1 / 待用户审批（本计划的直接输入；其 D1–D11 与 Q1–Q6 是实施时的取舍依据）。
- 调研证据：`docs/research/2026-10-18-explore-efficiency.md` §7（v0.32.0 复测：T4 31→33.5 步未降、`file_symbols` 4/10 使用但 T4 0/2、`outline` 0/10、每次 1–3 次失败调用）与 §7.4（原因分析与 4 条建议）。
- issue：`suga-gxkw`（本批任务单，含“与 `file_symbols` 共用提取层”“其他语言先用 grep 回退”“需设计”的要求）。
- 代码事实（HEAD `1501566e`）：`pkg/agentcore/tools/native/symbols.go:66-96,120-141,146-159`；`symbols_go.go:25-71`；`symbols_mask.go:14-20,20-107`；`symbols_lines.go:104-160`；`file_symbols.go:18-25,56-87,103-133,149-190,238-305,386-399`；`grep.go:17-22,34-44,94-111,119-121`；`catalog.go:76-92,142-155`；`internal/wiring/runtime.go:795-806`；`internal/wiring/subagent.go:50-51`；`internal/acpserver/policy.go:440`。
- 实施前必须重新确认 HEAD/branch/dirty 状态与 `file_symbols`/`maskSpans` 的现状；若与设计记录不一致，停止进入 T1。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | Go：按标识符列出引用位置并分类（def/call/sel/ref + 限定符） | `pkg/agentcore/tools/native/symbols.go:66-96`（语言注册表）、`symbols_go.go:25-71`（`SkipObjectResolution` 最小解析）、`symbols.go:146-159`（`sourceLines`）；标准库 `go/ast`、`go/token` | 现有 Go 提取器与解析模式、`sourceLines` 取行、`file_symbols` 的解析错误处理 | 在符号提取层新增 `refs_go.go`：同一解析模式 + `ast.Inspect` 分类 + 包名限定符判定；不引入 `go/types` | OWNER_EXTENSION | `file_symbols` 只给声明位置；T4 复测 33.5 步未降且 `file_symbols` 在追调用链时 0/2 使用，模型要的是“谁用了 `ReadOnly`” | owner 是 native 符号提取层；不得另起解析框架、不得引入 `go/types` 或 LSP |
| CAP-02 | 多语言：按名字找出使用位置（TS/JS、Python、Java） | `symbols_mask.go:14-20,20-107`（`maskSpans` 只遮蔽跨行 span）、`symbols_lines.go:104-160`（按行识别，自己跳过单行字符串与注释）、语言注册表 `symbols.go:85-96` | 现有 `maskSpans`、语言注册表的扩展名与 mask 风格、`approx` 输出约定 | `symbols_mask.go` 增 `maskInline`（单行字符串与行注释尾部），新 `refs_lines.go` 做词边界逐行匹配 | OWNER_EXTENSION | 没有单行遮蔽，`"ReadOnly"` 字面量与 `// ReadOnly` 注释都会假命中；`file_symbols` 只表达声明不表达使用点 | owner 是同一遮蔽/语言层；不引 tree-sitter/cgo，保持单二进制 |
| CAP-03 | 新只读工具 `file_refs` 在只读 run/chat/ACP 可用、schema `properties` 非空 | `catalog.go:76-92,142-155`（catalog 与 `BuildTools` 只读筛选）、`file_symbols.go:27-133`（同型工具的骨架：参数校验/范围解析/输出）、`internal/wiring/runtime.go:795-806`（只读运行取 catalog 只读项） | 现有 native 只读工具注册路径、`resolveRead`/`walkWorkspace`、输出治理（`limit`、`TruncateHeadTail`、notes） | 以 `file_symbols` 为模板新增 `refs.go`，并在 catalog 增加一行 | MINIMAL_NEW_MODULE | “谁调用了 X”当前只能靠猜正则 grep：T4 33.5 步未降，每次 1–3 次失败调用 | owner 是 native 只读工具集（与 `file_symbols` 同层）；只读、无缓存、无持久化；与 `file_symbols` 职责分离（声明 vs 使用点） |
| CAP-04 | 结果条数/字节上界、排序与截断提示 | `file_symbols.go:18-25,238-305`、`grep.go:17-22,119-121` | `defaultSymbolLimit=200`、`maxGrepLineBytes=300`、`TruncateHeadTail(rawLimit(maxOut))`、notes 风格 | 直接复用，不新增策略 | DIRECT_REUSE | 没有证据要求不同的上限或输出治理 | 不得另建输出治理，也不得改现有工具的截断文案 |
| CAP-05 | 验证：Linux 伪终端冒烟 + trace 断言 + 真实模型复测与方差 | `scripts/ptysmoke/`（`chat_observe.py` 为参考）、`fakellm.py`；`internal/personal/analyze.go` 的 `--analyze` 与 `suga trace <sid> --json` | 现有冒烟框架与 trace 断言方式、`--analyze` 指标 | 新增一个冒烟场景 + 一份复测报告；不新增指标 | DIRECT_REUSE | 目前没有引用检索的回归用例，T4 已有可比的基线数据 | 不新建评测框架；改 schema 后必须真实模型跑一轮 |
| CAP-06 | 工作区级扫描的成本上界（先筛选后解析） | `grep.go:94-111`（逐文件处理的遍历式实现）、`walkWorkspace` 的 ignore/超时/大小限制 | 现有遍历、`searchTimeout`、`maxSearchFileBytes` | 在 `refs_go.go`/`refs_lines.go` 加字节级预过滤：命中的文件才解析/逐行匹配 | OWNER_EXTENSION | 对数千文件逐个 `go/parser` 会让工作区级查询明显变慢 | owner 是引用提取层；不引入索引或缓存 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| MOD-SR-01 | CAP-03 | `pkg/agentcore/tools/native/refs.go`（配套 `refs_go.go`、`refs_lines.go`） | `file_symbols.go`（同型骨架）、`grep.go`/`find.go`（遍历与 ignore）、`catalog.go` 注册点、`refs`/`file_refs` 现有实现（无） | 现有工具都不产出“按名字找使用点”：`file_grep` 是文本匹配（注释/字符串假命中、无分类、不排除测试文件）、`file_symbols` 只列声明、`file_read` 是窗口读取 | 职责无法塞进任一现有 owner：做成 `file_symbols` 的模式会让 `path` 的含义在“被列清单的目标”与“被查名字的宿主”之间漂移 | T4 实测 33.5 步未降、`file_symbols` 0/2 使用，缺的正是“谁用了它” | owner 是 native 只读工具集；只读、无缓存、无持久化；生命周期与其它 native 只读工具一致 | 删除后回到“用 `file_grep` 猜词边界正则”（实测每次 1–3 次失败调用），不留配置或 schema 残留 |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 引用检索做成 `file_symbols` 的 `refs=Name` 模式 | CAP-03 | 删除独立工具后本能力仍可由模式参数实现，但复测证据反对：可选参数 `outline` 0/10 从未被触发，而独立工具 `file_symbols` 4/10 且在 T2 上 2/2 被主动使用；同一工具里 `path` 的语义会在两种模式间漂移，返回构成也变得不确定。 |
| gopls/LSP 客户端适配器 | CAP-01 | 删除后 `go/ast` 的分类（def/call/sel/ref + 限定符）已覆盖本 issue 的问法；LSP 需要常驻进程、每语言一个服务与安装，只读运行没有 shell，类型信息对“谁用了它”不是必需。方向由 `suga-cm5e` 持有。 |
| tree-sitter 或 cgo 解析器 | CAP-02 | 删除后 `maskSpans` + `maskInline` + 词边界已覆盖 TS/JS、Python、Java 的按名查找；tree-sitter 需要 cgo 或大体积移植，影响单二进制与 Windows 构建。 |
| 全仓索引 / 持久引用缓存 | CAP-06 | 删除后字节预过滤 + 四道闸仍能完成工作区级查询；索引要处理失效、段一致性与生命周期，而实测浪费不在“缺少索引”。 |
| 跨语言调用图 / 接口实现查找工具 | CAP-01 | 删除后按名字的引用清单已覆盖本 issue 的问法；调用图需要类型检查与跨语言模型，属非目标且无实测需求。 |

## 前置检查与 fail-closed 条件

### workspace baseline（实施开始时重新核对）

- Git root：`D:/work/inhere/my-tools-dev/inhere-tools/suga/.worktrees/b16d`（suga 的 worktree，主仓库 `inhere-tools/suga`；不得改 worktree 外的文件，也不得改 `.gofer/tracker`）。
- branch：`docs/refs-design`（基于 main = v0.32.0，HEAD `1501566e`，另含本批两份 docs 提交）。
- status：本工作树在写本计划前 `git status --short` 为空（无 dirty、无 untracked）；实施开始时必须重新确认，dirty 归属不清则停止。
- expected paths/symbols：见“范围、排除项与授权”的 7 组路径；新符号 `native.FileRefsTool`、`native.ScanGoRefs`、`native.ScanLineRefs`、`native.maskInline`（`maskInline` 为包内小写函数）。
- 依赖/能力：Go 工具链（本机 go1.25.10）、`staticcheck`、Linux 容器（pty + `make smoke-chat`）、可用的真实模型 provider（`opencode-go/deepseek-v4.1-flash`）与隔离 `SUGA_HOME` 运行方式。

### 前置检查项

- P0：记录 `git status --short`、HEAD、branch；确认只有本批文档改动；提交前只 stage 目标文件并核对 `git diff --cached --name-only`。
- P1：重读设计与报告 §7，逐条对照 `symbols.go`/`symbols_go.go`/`symbols_mask.go`/`file_symbols.go` 的现状（尤其是 `maskSpans` “单行字符串与注释有意不遮蔽”的注释与 `parser.SkipObjectResolution` 的用法）；不一致则停止。
- P2：跑近端基线 `go test -count=1 ./pkg/agentcore/tools/native/... ./internal/wiring/... ./internal/acpserver/...`（worktree 根目录），记录 exit code 与原始输出；基线失败标 `BASELINE_FAILED`，不进入实现。
- P3：确认跨平台测试可行：夹具一律 `t.TempDir()`，不写系统临时目录、不写只有一个系统成立的绝对路径字面量；只读 root 用例用 `t.TempDir()` 造目录。
- P4：确认只读运行确实能拿到新工具（`native.BuildTools(env, false)` 只筛 `MetaOf(t).ReadOnly`）与 ACP 分类映射（`CategorySearch → acp.ToolKindSearch`），据此确定 `internal/acpserver/policy_table_test.go` 的期望值。
- P5：确认 `scripts/ptysmoke` 现有场景结构、`fakellm.py` 的模式（tool/text/edit/memory）与 `make smoke-chat` 接入点，设计 trace 断言方式（`suga trace <sid> --json` 的工具事件与结果片段）。
- fail-closed：P2 失败、P1 发现设计与代码事实不一致、或实现需要改稳定系统提示词/审批语义时，停止并回到设计与人工 Gate，不自行扩大范围。

## 波次与依赖

| 波次 | 任务 | 依赖 | 执行方式 |
|---|---|---|---|
| W1 | T1 Go 引用提取与分类、T2 多语言近似匹配 | 无（两项互不依赖，可并行，分开提交） | 单测先行，绿后提交 |
| W2 | T3 `file_refs` 工具本体与接线 | T1、T2 | 单测 + 接线表同步，绿后提交 |
| W3 | T4 伪终端冒烟与反向验证 | T3 | Linux 容器执行 |
| W4 | T5 真实模型复测与文档 | T4 | 主机执行（隔离 SUGA_HOME） |

## 任务

### T1 Go 引用提取与分类

- 文件：`pkg/agentcore/tools/native/refs_go.go`（新）、`pkg/agentcore/tools/native/refs_go_test.go`（新）
- 动作：以 `symbols_go.go:25-71` 的 `parser.ParseFile(fset, file, src, parser.SkipObjectResolution)` 模式解析；`ast.Inspect` 收集名字与目标相等的 `*ast.Ident`，按父节点/上下文判定 `def`（`FuncDecl.Name`、`TypeSpec.Name`、`ValueSpec.Names`、`Field.Names`、`:=` 左值、`LabeledStmt`、`ImportSpec` 名）、`call`（`CallExpr.Fun` 是该标识符，或其 `Sel`）、`sel`（其他 `SelectorExpr.Sel`）、`ref`（其余）；`call`/`sel` 记录限定符，且限定符是本文件 import 的包名（含别名，跳过 `_`/`.`）时标记为包限定符；行号用 `token.FileSet`，行文用 `symbols.go:146-159` 的 `sourceLines` 并 trim 到 300 字节；导出 `ScanGoRefs`（或等价的包级入口）供 `refs.go` 调用，**不得只写不接**。
- 验证：worktree 根目录执行 `go test -count=1 ./pkg/agentcore/tools/native/ -run TestGoRefs`；`gofmt -l pkg/agentcore/tools/native/refs_go.go` 无输出；`staticcheck ./pkg/agentcore/tools/native/`。
- 单测（`t.Run` 分组，`gookit/goutil/x/assert`）：四类各一例；`x.M(ctx)` 与 `pkg.F()` 判为 `call` 且限定符/包限定符正确；`x.Field` 判 `sel`；注释与字符串里的同名不命中；同名局部变量与同名结构体字段各产出一条 `def`（误报场景），断言输出含该行原文；接收者名与 `:=` 左值为 `def`；名字不匹配不出结果；`parser` 解析失败的文件返回错误而不 panic。
- 完成标准：单测全绿；`refs_go.go` 少于 1000 行且关键分类逻辑有注释；提交前 grep 确认 `ScanGoRefs` 在 `refs.go`（T3）有调用点。
- 依赖：无。

### T2 多语言近似引用匹配

- 文件：`pkg/agentcore/tools/native/symbols_mask.go`、`pkg/agentcore/tools/native/refs_lines.go`（新）、`pkg/agentcore/tools/native/refs_lines_test.go`（新）
- 动作：在 `symbols_mask.go` 增加 `maskInline(src []byte, style maskStyle) []byte`——逐行遮蔽字符串字面量内容与行注释尾部（`maskBraces` 用 `//`，`maskPython` 用 `#`），保留换行与行号，供引用匹配使用（`maskSpans` 保持原语义不变）；新增 `refs_lines.go`：`maskSpans(src, style)` → `maskInline` → 逐行用 `(^|[^0-9A-Za-z_$])` + `regexp.QuoteMeta(name)` + `([^0-9A-Za-z_$]|$)` 匹配，返回行号与**未遮蔽**的命中行原文，`Approx=true`；语言归属复用 `symbols.go:85-96` 的注册表（扩展名 + mask 风格），只支持 `file_symbols` 覆盖的同一语言集合。
- 验证：`go test -count=1 ./pkg/agentcore/tools/native/ -run 'TestMaskInline|TestLineRefs'`；`go test -count=1 ./pkg/agentcore/tools/native/`（现有 `symbols_test.go` 必须仍通过，证明 `maskSpans` 语义未变）；`gofmt -l`、`staticcheck`。
- 单测：TS 夹具里 `foo` 的调用与引用命中；`"foo"` 字面量、`'foo'`、`// foo`、`/* foo */` 不命中；`foobar`、`$foobar`、`foo-bar` 中的 `foo` 不命中（词边界）；跨行模板字符串与 Python 三引号文档字符串里的 `foo` 不命中（`maskSpans`）；Python `# foo` 不命中；命中行原文与行号正确；`maskInline` 不改变行数、不改变未遮蔽行的内容。
- 完成标准：单测全绿；`symbols_mask.go` 只新增函数、不修改既有 `maskSpans` 行为；新函数在 `refs_lines.go` 有调用点。
- 依赖：无（与 T1 可并行）。

### T3 `file_refs` 工具本体与接线

- 文件：`pkg/agentcore/tools/native/refs.go`（新）、`pkg/agentcore/tools/native/refs_test.go`（新）、`pkg/agentcore/tools/native/catalog.go`、`pkg/agentcore/tools/native/file_symbols.go`、`internal/wiring/capabilities_test.go`、`internal/wiring/readonly_test.go`、`internal/wiring/approval_table_test.go`、`internal/wiring/subagent.go`、`internal/acpserver/policy_table_test.go`
- 动作：
  - `FileRefsTool`：参数 `name`（必填，空报错）/`path`（空 = 工作区根，支持文件、目录、只读 root 绝对路径）/`include_tests`（默认 false）/`limit`（默认 200，非法值回落）；`ToolMeta{Category: tools.CategorySearch, ReadOnly: true, Prefetchable: true}`；描述写明“who calls X / where is X used”“Go 按标识符分类、其他语言词边界近似”“不做类型检查”“ctags 不提供引用”。
  - 范围与遍历：`resolveRead` + `walkWorkspace`；单文件类型不支持直接报错并列出支持语言；目录下无支持文件报错；解析失败的文件记一行（最多 5 条 + 计数）；超大/不可读/不支持类型与跳过的测试文件分别计数。
  - 成本：字节级预过滤（先用词边界正则筛候选，命中的 Go 文件才 `go/parser`），`searchTimeout` 超时标记结果不完整。
  - 输出：`path:line class[(qualifier)] 命中行`，按 path、line 排序；`limit` 截断提示（`narrow path`）；notes 含 approx、跳过测试、不可解析、无类型解析固定提示、零命中时“可能在其他包/目录”的下一步提示；字节上限走 `TruncateHeadTail(rawLimit(t.maxOut))`。
  - 接线：`catalog.go` 注册一行；`file_symbols.go` 描述加一句“只列声明，不列使用点；看引用用 `file_refs`”；同步 `capabilities_test.go` 三处工具清单、`readonly_test.go` 只读工具计数 6→7、`approval_table_test.go` 的 read 列表、`subagent.go` 的 explore/research 名单、`policy_table_test.go` 的 `file_refs → acp.ToolKindSearch`。
- 验证：`go test -count=1 ./pkg/agentcore/tools/native/ -run TestFileRefs`；`go test -count=1 ./internal/... ./pkg/...`；`go build ./...`；`go vet ./...`；`gofmt -l $(git ls-files "*.go")` 无输出；`staticcheck ./...` 无新告警。
- 单测（`t.Run`）：目录级 Go 查询（夹具含 `def`/`call`/`sel`/`ref`，断言类别与排序）；默认跳过 `_test.go`、`include_tests=true` 时包含；`limit=1` 的截断提示；零命中的“可能在其他包”提示；非支持扩展名单文件报错含语言清单；只读 root（`t.TempDir()` 造的 root + 绝对路径）可查；`name` 缺失报错；目录级扫描的性能断言（造约 200 个文件，断言单次调用在 2 秒内，防止预过滤失效）；`ToolMeta` 断言为只读且 `CategorySearch`。
- 完成标准：全绿且无新 `staticcheck` 告警；`refs.go` 少于 1000 行；每个新函数都有真实调用点（提交前 grep 核对）。
- 依赖：T1、T2。

### T4 伪终端冒烟与反向验证（Linux 容器）

- 文件：`scripts/ptysmoke/`（新场景脚本，例如 `refs.py`）、`Makefile`（`smoke-chat` 目标）、必要时 `scripts/ptysmoke/README.md`
- 动作：用 `ptydrive.PtySession` 驱动真实二进制 + `fakellm.py` 假模型，依次发起 `file_refs` 的文件级与目录级调用，脚本用 `suga trace <sid> --json` 断言两次工具事件、参数与结果关键片段（类别标记、`(pkg:` 限定符、截断或零命中提示至少命中一项）；加入 `make smoke-chat`；完成后做反向验证：临时让 `file_refs` 返回空结果（或去掉 catalog 注册），确认该场景断言失败，恢复后重跑通过并记录两次输出。
- 验证：Linux 容器内 `make smoke-chat`（含新场景）；反向验证记录（临时改动已还原的 `git diff` 证据）。
- 完成标准：新场景通过且反向验证有记录；Windows 主机跑不了 pty，本任务由监督在容器执行（本计划不在主机预跑）。
- 依赖：T3。

### T5 真实模型复测与文档

- 文件：`docs/research/2026-10-18-explore-efficiency.md`（新增“§8 引用检索后复测”）、`CHANGELOG.md`（「未发布」→ 新增）
- 动作：用报告 §1.3 的 T4 任务加一个新的靶向任务 R1（“哪些地方使用了 `native.SupportsSymbols`”或同型的“谁调用了 X”问题），在隔离 `SUGA_HOME`、只读档、`max_steps: 60`、`opencode-go/deepseek-v4.1-flash` 下各跑 ≥2 次；用 `suga trace <sid> --analyze`/`--json` 记 steps/tool_calls/工具分布/失败调用/重复读/token/墙钟，与基线 31 步、复测均值 33.5 步并列；判定设计验收（T4 ≤22 步且至少一次运行使用 `file_refs`）；给出方差说明与未达成项；运行产物与隔离 home 放 worktree 的 `tmp/` 并在结束删除，密钥只用进程环境变量、不落盘不打印。
- 验证：报告里贴 `--analyze`/`--json` 的原始输出摘录与前后对比表；结束时 `git status --short` 干净（`tmp/` 已删）。
- 完成标准：复测数据与结论写入报告且与数据一致；未达预期时按“回滚与恢复”与 Gate C 处理，不隐藏负面结果。
- 依赖：T4。

## 回滚与恢复

- 每个功能点一个提交，可独立 `git revert`：T1 回滚后只剩近似匹配、T2 回滚后近似匹配消失（Go 精确分类仍在）、T3 回滚后 `file_refs` 不再注册（工具消失，`file_symbols` 描述交叉链接一并消失）、T4 回滚后冒烟场景移除、T5 只改文档。
- 无配置项、无数据库 schema、无持久化状态，回滚不需要迁移；`file_symbols`/`file_grep`/`file_read` 不受影响。
- 恢复点：T3 之前的所有提交都保持工作树可编译、测试可跑（T1/T2 只新增文件与函数，未接线时单测仍可跑）。
- 若复测显示工具仍不被使用（例如 T4 依旧 0 使用）或误报把模型带偏，先回滚 T3 的注册（工具从只读运行消失），再按设计 Gate 讨论是否做提示词批（设计 Q5/Q6）。

## 人工 Gate

- Gate A（现在）：设计与本计划获用户批准后才实施；未获批不改任何产品代码。
- Gate B（P0–P5 后）：基线失败、代码事实与设计不一致、或需要改稳定提示词/审批语义时，停下回到 Gate A 讨论，不自行扩大范围。
- Gate C（T5 后）：复测数据交用户看，决定是否做设计里记录为“暂不做/另立一批”的方向（只读环境说明列工具、按类别过滤参数、LSP）。
- 外部动作 Gate：push、打 tag、发版、部署与任何跨 Git root 的修改都不在本计划授权内；真实模型调用与容器内冒烟属出机器动作，实施时按当前执行请求逐项确认。

## 可追溯性

| 完成定义 / 决策 | 任务 | 验证证据 |
|---|---|---|
| 2、D2、D3、D4（Go 分类与误报暴露） | T1 | `TestGoRefs` 单测输出（含误报夹具）；`gofmt`/`staticcheck` 原始输出 |
| 3、D5、D6（多语言近似、ctags/LSP 结论） | T2 | `TestMaskInline`/`TestLineRefs` 输出；现有 `symbols_test.go` 仍绿 |
| 1、4、5、D1、D7、D8、D10、D11（工具形态、范围、成本、注册） | T3 | `TestFileRefs` + `internal/...` 全绿；`go build`/`go vet`/`gofmt`/`staticcheck` 原始输出；`git grep` 显示新函数调用点 |
| 6（冒烟） | T4 | Linux `make smoke-chat` 输出 + `suga trace --json` 断言 + 反向验证记录 |
| 6（真实模型复测） | T5 | 报告里的前后对比表、`--analyze` 输出与方差说明 |
| 7（提交纪律） | 全部 | `git status --short`、`git diff --cached --name-only`、提交列表 |
| Q1–Q6、D9（未决项与提示词边界） | 设计“待确认事项” | 批准记录与（若升级）新设计；本计划不含提示词改动 |

## 完成 Gate 与剩余工作

- 代码 Gate：完成定义 1–5 全部有原始输出证据；`go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")`（无输出）、`go test -count=1 ./internal/... ./pkg/...`、`staticcheck ./...`（无新告警）在 Windows 主机全绿。
- 平台 Gate：Windows 主机（无 pty、无 cgo）完成编译与单测；Linux 容器的伪终端冒烟与反向验证由监督执行并回报；两项都有记录才能宣布完成。`go test -race` 与 ptysmoke 在主机上跑不了，交付说明里必须标注“待容器验证”。
- 验证 Gate：T5 的复测含方差与未达成项；未达验收（T4 > 22 步或 0 次使用 `file_refs`）时写清原因并按 Gate C 决定下一步，不算完成。
- 提交 Gate：按任务逐点原子提交（`feat(native):`/`test(ptysmoke):`/`docs:`），不混入无关文件、不改 `.gofer/tracker`、不 push。
- 剩余工作处理：未完成的用例必须列进交付说明（哪些没跑、为什么、谁来跑）；发现的范围外问题写进汇报的“发现但不碰”，不在本批顺手修。
