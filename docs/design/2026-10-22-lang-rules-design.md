# 语言规则文件 `lang-rules.yaml`（file_symbols / file_refs / 测试命令识别） 设计

> 状态：0.2 已批准（2026-10-22，用户：待定点按建议；同时把实现抽到子包）。第三十批实施。
> 来源：用户 2026-10-22——“refs/symbols 语言探测和优化也是内嵌的，想支持新语言也要改代码，是否也可以用规则文件来实现”。首批新语言按建议：C#、Kotlin、Ruby、C/C++。

## 1. 现状

一门语言的特殊处理散在三处 Go 代码里：

| 位置 | 内容 | 用途 |
|---|---|---|
| `lang.go` + `lang_*.go`（`Language`） | 名字、项目标记文件、测试命令正则、聚焦参数、依赖 API 提示子句、依赖源码根探测函数 | 只读根、基础提示、trace 分析识别测试运行 |
| `symbols*.go`（`symbolLanguage`） | 扩展名、`approx`、来源、ctags 语言名映射、提取函数（Go 用 go/parser；JS/TS、Java、PHP 用“声明行正则表 + 花括号深度”；Python 用缩进；YAML/TOML 专用） | `file_symbols` |
| `symbols_mask.go`（`maskStyle`）、`refs_lines.go`（`refMaskStyle`） | 注释与字符串遮蔽风格：`maskBraces`/`maskPython`/`maskPhp`/`maskHash`/`maskToml`，按扩展名选 | 声明识别与 `file_refs` 的近似匹配 |

不在表里的语言：`file_symbols` 拒绝（装了 universal-ctags 也只对登记了 ctags 映射的扩展名用），`file_refs` 退回不遮蔽的单词边界文本匹配。

支持一门新语言要改 3–4 个 Go 文件，并跟着改描述文本与测试。

## 2. 目标与非目标

目标：

- 一门“声明行能看出来”的语言，只写一段 YAML 就能获得：`file_symbols`（近似）、`file_refs` 的注释/字符串遮蔽、ctags 加速、trace 分析的测试命令识别。
- 内置的 JS/TS、Java、Python 改用同一机制（内嵌 YAML），**输出逐字节不变**。
- 首批用规则新增 C#、Kotlin、Ruby、C/C++（内置）。
- 分层与 output rules / nudge rules 一致：内置 → `$SUGA_HOME/lang-rules.yaml` → `<workspace>/.suga/lang-rules.yaml`；同名替换、新名追加、`enabled: false` 关闭。

非目标（保留在代码里）：

- Go（go/parser 的真解析与引用分类）、PHP（heredoc/nowdoc 遮蔽、`#[` 属性、PSR-12 换行花括号）、YAML/TOML（键路径提取）。规则文件可以**关闭**它们，但不能用规则替换其实现。
- 依赖源码根探测（要探测文件系统与环境变量）和基础提示里的依赖 API 子句（属于提示缓存前缀，规则不能改基础提示）。
- 任意代码：规则里只有正则与固定枚举，不能执行命令或脚本。

## 3. 规则格式

```yaml
languages:
  - name: Ruby                     # 唯一键；同名替换
    enabled: true                  # 可省略，默认 true
    exts: [.rb, .rake]             # 小写，含点
    markers: [Gemfile, Rakefile]   # 项目标记文件（目前与 Language.Markers 一样只声明）
    test_commands: '\b(rspec|rake test|bundle exec (rspec|rake test))\b'
    focused_flags: ['-e', '--example']
    ctags: Ruby                    # universal-ctags 的 --language-force；或按扩展名的映射 {.h: C++}
    syntax:                        # 遮蔽：声明识别与 file_refs 共用
      line_comment: ['#']
      block_comment: [['=begin', '=end']]   # 成对；=begin/=end 要求行首（见 §4.2 line_start）
      strings: ['"', "'"]                    # 单行字符串引号（反斜杠转义）
      multiline_strings: []                 # 跨行字符串的成对定界符，如 Python 的 '''/"""、C# 的 """
    blocks: end_keyword            # brace | indent | end_keyword
    end_keyword:                   # 仅 blocks=end_keyword：哪些行开块、哪个词关块
      open: '^\s*(class|module|def|if|unless|while|until|case|begin|do)\b|\bdo\s*(\|[^|]*\|)?\s*$'
      close: '^\s*end\b'
    decls:
      - {pattern: '^\s*class\s+([A-Z][\w:]*)', kind: class, name: 1, body: true}
      - {pattern: '^\s*module\s+([A-Z][\w:]*)', kind: module, name: 1, body: true}
      - {pattern: '^\s*def\s+(self\.)?([\w]+[?!=]?)', kind: method, name: 2, member: true}
```

字段与现有结构的对应：

| 规则字段 | 代码中对应 |
|---|---|
| `name`、`markers`、`test_commands`、`focused_flags` | `Language` 同名字段 |
| `exts`、`ctags` | `symbolLanguage.exts`、`ctagsLanguage` |
| `syntax` | 取代 `maskStyle` 枚举的一组参数 |
| `blocks` + `decls` | `braceSymbols`（brace）/ 缩进帧（indent）/ 新增 end_keyword 跟踪 + `braceDecl`（pattern/kind/kind_group/name/member/body） |
| — | 规则语言恒为 `approx: true`、`source: decl-lines`、`referenceParser: false` |

`decls` 每条：`pattern`（必填）、`kind` 或 `kind_group`（二选一）、`name`（名字所在捕获组）、`member`（只在类型体内生效）、`body`（打开一个类型体）。与 `braceDecl` 一一对应。

`brace` 风格另有 `body_brace_next_line: true|false`（PHP 用的 PSR-12 选项；规则语言可用，如 C# 的 Allman 风格）。

## 4. 实现要点

### 4.1 代码改动

1. **通用遮蔽**：新增 `maskSpec`（line_comment、block_comment、strings、multiline_strings、各自的行首约束），`maskSpans` 增加按 spec 遮蔽的路径。内置 `maskBraces`、`maskPython` 改成等价的 spec；`maskPhp`/`maskHash`/`maskToml` 保留原实现。
2. **块风格**：`brace`、`indent` 复用现有逻辑；新增 `end_keyword`（开块/关块正则 + 帧栈，类型体规则与 brace 的 member/body 语义相同）。
3. **注册表**：`symbolLanguages` 与 `languages` 在启动时由“代码语言（Go、PHP、YAML、TOML）+ 规则语言（内置 YAML + 用户层 + 项目层）”合成；**顺序**：现有语言保持原位置（描述文本与拒绝信息的语言列表不变），新语言追加在后。
4. **加载**：`LoadLangRules(files...)`，错误（YAML 语法、未知字段、坏正则、`kind` 与 `kind_group` 同时出现、`blocks` 未知、扩展名冲突）报文件路径 + 语言名，与 `LoadOutputRules` 一致；用户层或项目层文件出错时**跳过该文件**（与 output rules 一致：`internal/wiring/capabilities.go` 的 `minimizeOutput` 对出错文件 `slog.Warn` 后跳过），并且**在 `suga inspect manifest` 里列出跳过的文件与原因**——只写日志用户看不到，写错的语言规则会悄悄不生效。
5. **扩展名冲突**：后加载的同扩展名规则覆盖先前的（同名替换天然覆盖；不同名但扩展名相同 → 后者生效并警告），代码语言的扩展名（.go/.php/.yaml/.yml/.toml）不能被不同名规则抢走（报错）。
6. **展示**：`file_symbols`/`file_refs` 的工具描述里的语言列表由注册表生成；`suga inspect manifest` 列出生效语言（名字、扩展名、来源层、是否 ctags）。

### 4.2 内置迁移（逐字节不变）

- 先写 **characterization 测试**：对 `testdata/` 下每种内置语言的样例文件（现有测试样例 + 补充若干真实片段）记录当前 `file_symbols` 与 `file_refs` 输出为黄金文件。
- JS/TS、Java、Python 改成 `pkg/agentcore/tools/native/langrules/*.yaml`（embed），删除对应 Go 正则表；黄金文件必须完全一致。
- 工具描述文本、基础提示、只读根列表的字节不变（现有测试守护）。
- 反向验证：故意改一条内置规则，characterization 测试失败。

### 4.3 首批新语言（内置）

| 语言 | exts | blocks | 要点 |
|---|---|---|---|
| C# | .cs | brace（`body_brace_next_line: true`） | namespace/class/struct/interface/enum/record、方法与属性成员；`//`、`/* */`、`"`、`@"…"`、`"""…"""` |
| Kotlin | .kt, .kts | brace | class/object/interface/enum class/data class/fun/val/var；`"""` 跨行字符串 |
| Ruby | .rb, .rake | end_keyword | class/module/def；`=begin/=end` 行首块注释；`#` |
| C/C++ | .c, .h, .cc, .cpp, .cxx, .hpp, .hh | brace | struct/class/enum/union/namespace/函数定义行（排除控制语句）；`.h` 的 ctags 映射为 C++ |

每种语言：样例文件 + 期望符号测试 + `file_refs` 遮蔽测试（注释/字符串里的名字不算）。函数定义行的识别保持“近似”（声明行能看出来的才报），写清已知漏报。

测试命令：C# `dotnet test`（原 `otherTestCommands` 中的条目迁入 C# 规则）、Kotlin `gradle(w)? test`、Ruby `rspec`/`rake test`（迁入）、C/C++ `ctest`/`make test`（迁入）。迁入后 trace 分析的识别结果不变（测试守护）。

## 5. 文档

- `skills/suga-guide/references/tools.md`：规则文件格式、字段表、示例（一门新语言的完整写法）、`suga inspect manifest` 查看生效语言。
- README 一句；CHANGELOG（新增）。

## 6. 测试与验证

- characterization（内置迁移逐字节不变）、四门新语言各自的符号与引用测试、加载错误与分层覆盖测试、扩展名冲突、manifest 展示。
- 伪终端冒烟不需要（不涉及 chat 交互）。
- 真实模型：用一个 C# 或 Ruby 小项目跑一轮带 `file_symbols`/`file_refs` 的 run，确认描述里的语言列表与结果可用（结果记入实施汇报）。

## 7. 包结构（0.2 新增，用户 2026-10-22）

先把语言相关实现从 `pkg/agentcore/tools/native` 抽到子包 **`pkg/agentcore/tools/native/codeintel`**，再在子包里做规则文件。试拆核实（临时副本把 `lang*`/`symbols*`/`refs*` 移入子包编译）：38 处未定义，集中在工具外壳（`refs.go` 的 `FileRefsTool` 27 处：路径解析、读根、遍历、上限）、依赖根探测（`lang.go` 3 处：`ReadRoot`/`AddDirs`）和少量小工具函数；`native` 包外只有 4 处引用（`internal/wiring` 的 `Languages`/`DependencyRoots`，`internal/personal` 的 `TestCommandPattern`）。

- **codeintel**（纯分析，输入是“路径 + 内容”，不碰文件系统策略）：语言注册表（声明部分）、遮蔽、各语言符号提取器、引用行匹配与分类、ctags 调用、规则加载。
- **native 保留**：`FileSymbolsTool`、`FileRefsTool` 的工具外壳（参数、路径、遍历、大小上限、输出），依赖根探测（按语言名挂探测函数，因为绑定 `ReadRoot`）。
- 小工具函数（`utf8Cut` 等）在 codeintel 里各自实现或从更底层的包取，不反向依赖 native。
- 加分层测试：`codeintel` 不得 import `native`。
- **两步提交**：①纯移动 + characterization 测试（行为不变，审核看 diff 是否只有移动）；②规则文件与新语言。

## 8. 已定（2026-10-22 用户批准）

1. 用户层/项目层规则文件出错：跳过该文件、写日志，并在 `suga inspect manifest` 显示跳过的文件与原因。
2. 项目层 `.suga/lang-rules.yaml` 不需要 `project_trust`（只含正则，与 output rules 一致）。
3. 本批迁移 JS/TS、Java、Python；PHP 保留代码实现。
