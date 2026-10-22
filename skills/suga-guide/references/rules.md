# suga 的规则文件：审批 / 输出整理 / 收工检查 / 语言

suga 有四种模型可读写调整的规则文件。本文是它们的唯一完整参考：每节给出用途、文件位置与加载顺序、字段表（与代码一致）和一个完整示例。改完怎么核对、以及"受保护路径"的写权限说明见最前面的两节。

要写新规则时的标准流程：

1. `suga inspect rules <kind> --builtin` 打印内置规则的原始 YAML，照着最接近的内置条目改（nudge 的内置规则以可直接放进规则文件的形态打印，approval 没有内置规则）。
2. 起草规则，**先给用户看内容**（见下面的受保护路径说明），说明写到哪一层、影响什么。
3. 用户同意后再用 `file_write` 写入用户级或项目级文件（只增改需要的条目，不要整份重写用户手写的文件）。
4. 核对（见"怎么核对"）。

## 两件先知道的事

### 分层与覆盖语义（四种规则一致，approval 除外）

- 顺序：内置（输出/语言/收工检查有内置；审批没有）→ `$SUGA_HOME/<kind>-rules.yaml`（用户级）→ `<workspace>/.suga/<kind>-rules.yaml`（项目级）。后加载的赢。
- 同名规则**整条替换**（不会继承旧条目的字段），新名字追加到末尾，`enabled: false` 关闭同名的那条。要给内置规则"只加一个句式/字段"时必须整条抄写内置的原字段，或直接新起一个名字。
- 一个文件坏了（YAML 语法、未知字段、坏正则、字段组合非法）时**整份文件被跳过**并报告原因；suga 照常用其余层的规则继续跑。这也是为什么改完一定要核对。
- 审批规则不同：用户级与项目级是两个独立文件（不是"内置"），且项目 allow 有信任门槛（见审批一节）；文件损坏是启动/重载失败，不是跳过。

### 怎么核对

```bash
suga inspect rules <kind>            # 生效规则：条目、来源层，跳过的文件与原因
suga inspect rules <kind> --builtin  # 内置规则原文（照写模板）
suga inspect rules <kind> --json     # 机器可读，kind/rules/skipped 结构
suga inspect manifest                # Output rules / Nudge rules / Language rules 三节汇总
```

`kind` 取值：`approval`、`output`、`nudge`、`lang`。你新写的条目应出现在生效列表里且来源层是你写的那个文件；`skipped`（manifest 里是 `output_rule_skips` / `lang_rule_skips`）必须为空，否则按报错修文件后重查。

### 受保护路径（先给用户看，再写）

`$SUGA_HOME` 与 `<workspace>/.suga/` 是 suga 的控制面：写这两个目录下的文件（包括本页所有规则文件）在 chat / ACP 会先弹出审批询问；在 `run`（含子 agent）会被直接拒绝——这是有意行为，不要试图绕开它（例如让 shell 去写）。所以正确做法是：把规则内容作为草稿展示给用户，用户明确同意后再写；想要免去每次询问，可以建议用户把这类写入写进 approval-rules 的 allow（受保护路径的 allow 在无交互模式下同样会被拒，见审批一节的受保护路径说明）。

## 审批规则 `approval-rules.yaml`

**用途**：预置的审批策略——哪些工具调用直接放行（allow）、必须询问（ask）、直接拒绝（deny）。让按命令/路径放行的常用操作不再逐次询问，同时收紧危险目标。

**位置与设置**：用户级 `$SUGA_HOME/approval-rules.yaml`，项目级 `<workspace>/.suga/approval-rules.yaml`；两级的 `tools.approval_rules.enabled`（默认 `true`）关掉后两个文件都不读，回退 `tools.approval` 的旧语义。相关设置项（写在 `config.yaml`，不在规则文件里）：

| 设置 | 默认 | 说明 |
|---|---|---|
| `tools.approval_rules.enabled` | `true` | 关掉即不读规则文件 |
| `tools.approval_rules.always_prefix_tokens` | `2` | chat 里答 `a` 按命令前缀创建会话内 allow 的 token 数 |
| `tools.approval_rules.persist_ask` | `true` | 允许把后续的 ask 答案写回规则文件 |
| `tools.approval_rules.project_trust` | 无 | 绝对 workspace 路径 → 该项目规则文件的 sha256；信任后项目 allow 才生效 |

**语义**：

- 顺序：用户级规则在前、项目级在后；判定时对所有命中的规则取**最强**动作（deny > ask > allow），同动作取文件顺序靠前的。
- `command` 规则要求 `tool: shell_exec`；token 精确匹配，只有末尾一个独立的 `*` / `**` 能扩展后续 token（`[ "go test *", "git status" ]`）。allow 的命令还必须通过所用 shell（sh/bash、PowerShell、cmd）的保守 ASCII 白名单，控制符、展开、包装器或未知 shell 不会产生 allow。
- `path` 规则要求 `tool` 是明确的 `file_write` / `file_edit` / `file_remove`（不能写文件工具的 glob），路径是相对 workspace 根的 glob（`docs/**`）；与 `path` 不能同时写 `command`。
- `tool` 是原生工具名 glob；MCP 工具必须写 `server/toolglob` 形式。
- `modes: [chat, run, acp]` 限定入口，省略适用于所有入口。
- 项目级不对称信任：项目 `deny` / `ask` 总是生效（可只收紧），项目 `allow` 必须同时把绝对 workspace 路径与当前文件 hash 写进 `project_trust` 才生效；hash 变了就失效。`suga inspect manifest` 的 Approval rules 一节显示两级文件路径、hash、信任原因与被忽略的条目。
- 一份文件上限 64 KiB、256 条规则；YAML anchor/alias、未知字段、非法组合直接启动失败（fail-closed，不会跳过）。
- 规则不能越过只读模式、沙箱、危险删除拦截和受保护路径：命中 `$SUGA_HOME`、workspace 下 `.suga/`、`.git/` 的 allow 会被钳制为 chat/ACP 的 ask、无交互 run/子 agent 的 deny。

**字段表**（每条 `rules:` 条目）：

| 字段 | 必填 | 说明 |
|---|---|---|
| `action` | 是 | `allow` / `ask` / `deny` |
| `tool` | 否 | 工具名 glob；MCP 写 `server/toolglob`；`command` 规则必须是 `shell_exec`，`path` 规则必须是三个文件工具之一 |
| `command` | 否 | shell_exec 的命令模式列表；一个文件里与另一个条目的 `path` 互斥，同一条目内 `command`/`path` 互斥 |
| `path` | 否 | 相对 workspace 根的路径 glob 列表（文件写/编辑/删除目标） |
| `modes` | 否 | `chat`/`run`/`acp` 子集；省略 = 所有入口 |

**完整示例**（项目级文件，先给用户看再写；allow 需要把 hash 放进 `project_trust`）：

```yaml
# <workspace>/.suga/approval-rules.yaml
rules:
  - tool: shell_exec
    command: ["git status", "git diff *", "git log *"]
    action: allow
  - tool: file_write
    path: ["docs/**"]
    action: ask
  - tool: file_remove
    path: [".git/**"]
    action: deny
  - tool: "myserver/*_delete"
    action: deny
```

**核对**：`suga inspect rules approval` 列出生效条目（来源层 `user` / `project`）与被信任门槛忽略的项目 allow；`suga inspect manifest` 显示两级文件路径、hash 与信任原因。没有内置规则可打印：`--builtin` 会说明默认动作来自入口默认与 `tools.approval`。

## 输出整理规则 `output-rules.yaml`

**用途**：`shell_exec` 结果在交给模型前的整理规则（`tools.minimize_output`，默认开）：丢行、折叠重复噪声、改写、分组、首尾保留，并给测试/构建输出生成一行汇总。原文永远保存为 ref，整理只是让上下文少占地方；命令自己带了管道过滤（`| grep` 等）时不套用。

**位置与内置文件**：内置规则按语言/工具族一份文件嵌进二进制，读取顺序即 `go, rust, js, python, java, git, grep, docker, make, gh, php`（`suga inspect rules output --builtin` 逐份打印原文，每份是独立文档）。之后依次读 `$SUGA_HOME/output-rules.yaml` → `<workspace>/.suga/output-rules.yaml`（同名替换、新名追加、`enabled: false` 关闭）。

**字段表**（`rules:` 列表，正则都是 Go regexp；`drop`/`failure`/`count`/`sum`/`fold` 按单行匹配，`match_output`/`replace` 看整段输出）：

| 字段 | 说明 |
|---|---|
| `name` | 唯一键，同名替换 |
| `enabled` | 省略即 true，`false` 关闭同名规则 |
| `match` | 必填，命令行的正则；命中才套用 |
| `match_output` | 可选第二条件：输出里也要命中它（匹配整段输出，`^`/`$` 要写 `(?m)`） |
| `drop` | 要丢掉的行（失败块内不丢） |
| `block` | 按测试分组：`start`/`body`/`pass`/`fail`——`body` 行跟最近的 `start`；`pass` 行连同它后面的 `body` 一起丢，`fail` 行保留 |
| `failure` | 代表失败的行：保留且不再输出汇总 |
| `count` | 名字 → 行正则；命中行计数进汇总 |
| `sum` | 名字 → 有捕获组的行正则；捕获的数字累加进汇总 |
| `fold` | 重复噪声折叠：`start` 匹配首行、`body` 匹配后续行、`text` 是替身（`{lines}` = 被折叠行数，`{1}`/`{名}` = 首行捕获组；`text` 为空则整段删除） |
| `replace` | 对保留文本的改写：`pattern`（多行模式，`^`/`$` 锚在行上）/`with`（`$1`、`${名}` 引用捕获组；字面 `$` 写 `$$`），多条按顺序执行 |
| `group_by` | 有捕获组的正则：相邻行捕获值相同的合并成一条组头 + 缩进行 |
| `head` / `tail` | 保留结果的前/后 N 行，中间变 `[... N lines omitted ...]` |
| `min_bytes` / `min_saving` | 规则自己的门槛；0/省略用全局默认（结果 <600 字节不动、节省 <20% 放弃），命中同一命令的多条规则取最宽松值 |
| `summary` | 汇总模板：`{名}` 填 count/sum 的值，`[, 包含全部为零的部分]` 整段省略；全零则不产出汇总 |

执行顺序：fold → block/drop/failure 与 count/sum 计数 → replace → group_by → head/tail → summary（最后追加，不会被 head/tail 切掉）；整理结果必须比原文短且达到节省门槛才采用。

**完整示例**（给 `terraform plan` 写一条整理规则）：

```yaml
# <workspace>/.suga/output-rules.yaml
rules:
  - name: terraform-plan
    match: '\bterraform\s+plan\b'
    match_output: 'Plan: [0-9]+'          # 输出里确实有 plan 摘要才套用
    drop: ['^\s*$']                       # 空行
    count:
      added: '^\s*\+ '                    # 统计各色变更行
      changed: '^\s*[-~] '
    failure: ['Error:', 'failed']
    head: 40                              # 详细资源列表太长：留头留尾
    tail: 10
    summary: 'terraform plan: {added} to add, {changed} to change'
```

**核对**：`suga inspect rules output` 里应出现 `terraform-plan`（来源层是你写的文件）；`suga inspect manifest` 的 `Output rules` 一节按名字、来源层列出全部生效规则，`output_rule_skips` 列被跳过的文件。

## 收工检查规则 `nudge-rules.yaml`

**用途**：chat 回答结束前的"停得太早"检查：回答末尾承诺了"接下来我会…"却没有后续动作、写了文件却没有验证、会话计划还有未完成项。种类（kind）固定三种，规则文件只能调参数、加句式、开关，不能教它新逻辑。

**位置**：内置 4 条（`promise-zh`、`promise-en`：中英文承诺句式；`unverified-write`：常见测试/构建命令的验证白名单；`plan-open`：计划未完），随后 `$SUGA_HOME/nudge-rules.yaml` → `<workspace>/.suga/nudge-rules.yaml`。

**语义**：

- 同名条目**整条替换**；配置省略 `kind` 时沿用同名内置规则的种类；新规则必须写 `kind`。
- 新规则（或替换条目）里空字段回落到该 kind 的默认：`unverified_write` 的 `write_tools` 默认 `file_edit/file_write/file_remove`（省略即三者都算改了工作区）、`check_tools` 默认 `shell_exec`（省略即任何 shell 调用都算验证，除非写了 `check_command`）。
- 一个文件出错（坏正则、未知 kind、新规则缺 kind、解析失败）整份跳过并按文件路径报错。
- `kind: promise` 的 `patterns` 匹配回答的**最后一个段落**的 Go 正则，多式其一即可。

**字段表**（`rules:` 列表）：

| 字段 | 说明 |
|---|---|
| `name` | 唯一键，同名替换整条 |
| `kind` | `promise` / `unverified_write` / `plan_open`；替换内置同名条目时可省（沿用其种类） |
| `enabled` | 省略即 true，`false` 关闭同名规则 |
| `patterns` | `promise` 用：承诺句式的正则列表（替换时是重写，不是追加） |
| `write_tools` | `unverified_write` 用：成功调用即视为改动了工作区的工具；空 = 默认三个文件工具 |
| `check_tools` | `unverified_write` 用：能验证改动的工具；空 = 默认 `shell_exec` |
| `check_command` | 可选正则：check 工具的命令命中它才算验证；省略 = 任何 check 工具调用都算 |

**完整示例**（加一条句式、关掉一条内置、另加一条只认测试命令的写规则）：

```yaml
# $SUGA_HOME/nudge-rules.yaml
rules:
  - name: promise-later
    kind: promise
    patterns: ['(我|让我)稍后(再)?(会|来|去)?', '待会(儿)?再(说|看|跑)']
  - name: plan-open
    enabled: false
  - name: unverified-write
    check_command: '(go +(test|vet|build)|make +(check|test)|pytest)'
```

**核对**：`suga inspect rules nudge` 按名字、种类、来源层列出全部生效规则；坏文件出现在 `skipped`。

## 语言规则 `lang-rules.yaml`

**用途**：`file_symbols` / `file_refs` 的语言识别与 trace 分析的测试命令识别由语言注册表决定：内置 Go/PHP/YAML/TOML 的实现在代码里（规则抢不走它们的 `.go`/`.php`/`.yaml`/`.toml` 扩展名，写了整份文件报错跳过）；其余语言——内置 JavaScript/TypeScript、Python、Java、C#、Kotlin、Ruby、C/C++、Dart（含 Flutter）与你新增的——都由规则文件描述。

**位置**：内置每语言一份（`javascript, python, java, csharp, kotlin, ruby, cpp, dart`），随后 `$SUGA_HOME/lang-rules.yaml` → `<workspace>/.suga/lang-rules.yaml`（同名替换整条、新名追加、`enabled: false` 关掉，包括内置语言）。

**语义**：

- 后加载的规则用同名扩展名抢走别的语言的扩展名时后者生效并在日志警告；所以替换一门语言（而非新增）要**同名**。
- 未知字段直接报错（不是悄悄忽略）；文件只含正则与固定枚举，不能执行命令。
- 规则语言的符号结果是 approx（按声明行近似，不解析），注释与字符串里的同名词不算引用；声明 `ctags` 时装了 universal-ctags 的环境优先用 ctags。
- Dart 的已知漏报：`/* */` 块注释在 Dart 里可嵌套，规则只能当成不嵌套（内层 `/*` 被算进外层注释）；raw 字符串 `r'...'` 的反斜杠不转义这点表达不了；没有修饰词的隐式类型字段（`int retries = 0;`）不报，只报 `final`/`const`/`var`/`late`/`static` 字段；字符串插值 `${...}` 中的真实调用在遮蔽后不算 `file_refs` 的引用。
- 依赖源码根探测与基础提示里的依赖 API 子句在代码里（提示缓存前缀的一部分），规则改不了。

**字段表**（`languages:` 列表）：

| 字段 | 说明 |
|---|---|
| `name` | 唯一键，同名替换；也是工具描述里显示的名字 |
| `enabled` | 省略即 true，`false` 关掉该名字的语言 |
| `exts` | 小写、含点的扩展名列表 |
| `markers` | 项目标记文件（声明用） |
| `test_commands` | 识别"跑测试"的完整正则（trace 分析用） |
| `focused_flags` | 只跑部分测试的参数（正则片段） |
| `ctags` | universal-ctags 的语言名；或按扩展名映射，如 `{.h: C++}` |
| `syntax` | 遮蔽：`line_comment`、`block_comment`（成对）、`strings`（单行引号）、`multiline_strings`（跨行定界符）、`line_start`（只在行首生效的块注释开头，如 Ruby 的 `=begin`） |
| `blocks` | `brace`（花括号，默认）/ `indent`（缩进，Python）/ `end_keyword`（关键字收尾，Ruby） |
| `body_brace_next_line` | 花括号风格：类型体的 `{` 写在下一行（C# 的 Allman） |
| `end_keyword` | `blocks: end_keyword` 时用：`open` 开块行正则，`close` 收尾关键字正则 |
| `decls` | 声明行规则列表，见下 |

`decls` 每条：`pattern`（Go 正则，匹配去掉首尾空白后的整行）、`kind` 或 `kind_group`（二选一，后者是捕获组号，用于 `class|interface|enum` 这种一行多关键字）、`name`（名字所在捕获组号）、`member`（只在类型体内生效）、`body`（该行打开一个类型体）。

**完整示例**（新增一门 Lua）：

```yaml
# <workspace>/.suga/lang-rules.yaml
languages:
  - name: Lua
    exts: [.lua]
    test_commands: '\bbusted\b'
    ctags: Lua
    syntax:
      line_comment: ['--']
      block_comment: [['--[[', ']]']]
      strings: ['"', "'"]
    blocks: end_keyword
    end_keyword:
      open: '^\s*(function|if|for|while|do)\b'
      close: '^\s*end\b'
    decls:
      - {pattern: '^\s*function\s+([A-Za-z_][\w.:]*)', kind: func, name: 1}
      - {pattern: '^\s*local\s+function\s+([A-Za-z_]\w*)', kind: func, name: 1}
```

**核对**：`suga inspect rules lang` 列出全部生效语言（名字、扩展名、块风格、来源层、是否用 ctags），`suga inspect manifest` 的 `Language rules` 一节同样列出并显示被跳过的规则文件。
