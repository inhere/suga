# scripts/eval 评测运行器

在隔离环境里跑一组固定任务，度量 suga 的调用次数、token、缓存命中、成本与耗时。
不调用 shell 判定结果（Windows 可跑）。

## 用法

```bash
go build -o tmp/smoke/suga ./cmd/suga

# 假模型：只验证管线，任务成败不作数（需要 python3 与 scripts/ptysmoke/fakellm.py）
go run ./scripts/eval --fake --bin tmp/smoke/suga

# 真实模型（主机）：只读读取该目录的 config.yaml 与 .env
go run ./scripts/eval --model opencode-go/deepseek-v4.1-flash --config-from ~/.suga
```

参数：`--bin`（默认 `tmp/smoke/suga`，Windows 自动补 `.exe`）、`--task`（任务名 glob，默认全部）、
`--model`、`--fake`、`--config-from <dir>`、`--out`（默认 `tmp/eval/<run-id>/`）、`--timeout`（每任务，默认 10m）、
`--suga-args` / `--omp-args` / `--repeat`（见“附加参数、tool_counts 与重复运行”）、
`--allow-web`（见“禁网”，默认关闭 web 工具）、
`--variant <dir>`（可重复，见“variant 对比”，仅 suga）、
`--probe-prefix` / `--probe-repeat` / `--probe-gap`（见“前缀缓存探针”）。
终端输出汇总表；`<out>/results.jsonl` 每任务一行（成败、checks 明细、答案、调用/token/命中率/成本/工具调用数、
`tool_raw_bytes`/`tool_inline_bytes`，后两项在 `suga inspect stats` 提供之前为 null；
`web` 记录本次是否禁网、`removed_tests` 记录判分前移出模型自写的测试）。个别任务失败退出码仍为 0，运行器自身出错才非 0。

## 任务格式

`testdata/tasks/<name>/task.yaml` + `fixture/`：

```yaml
name: fix-failing-test
mode: allow-write        # read-only（默认，suga run 默认模式）| allow-write（加 --allow-write）
fake_mode: edit          # 可选，fakellm 的 tool|text|edit，默认 text
fake_reply: "..."        # 可选，--fake 时假模型固定的回答（fakellm --reply），让假模型跑法结果确定
prompt: ...              # 单轮；多轮改用 prompts（二选一）
prompts:                 # 多轮：同一会话按顺序发送；answer_contains 判定最后一轮的回答
  - 第一轮 ...
  - 第二轮 ...
compact_after: [2]       # 可选：第 2 轮之后强制换段（`suga compact <会话>`），见“长会话任务”
generate:                # 可选：运行时生成文件，避免提交大文件
  - path: logs/app.log
    count: 6000
    line: "... {i} ..."  # {i} 为行号
    inserts: [{at: 4217, text: "..."}]   # 替换指定行
checks:
  - {type: file_contains, path: a.go, text: "..."}
  - {type: file_not_contains, path: a.go, text: "..."}
  - {type: file_equals, path: a.txt, text: "..."}   # 忽略末尾换行
  - {type: answer_contains, text: "..."}            # 在最终回答中查找
  - {type: answer_not_contains, text: "..."}        # 最终回答中不得出现
  - {type: answer_matches, text: '^[-*] '}          # 最终回答匹配该正则
  - {type: answer_not_matches, text: '[\p{Han}]'}   # 最终回答不得匹配该正则
  - {type: answer_contains, text: "...", turn: 2}  # 判定第 2 轮的回答（turn 省略=最终回答）
  - {type: command, args: [go, test, -count=1, ./...], timeout: 5m}   # 在工作区内执行，退出码 0 为通过
timeout: 25m             # 可选：任务级超时，覆盖全局 --timeout（omp 的 --max-time 同步使用）
repo: {ref: 202bfa0}     # 可选：仓库模式（与 fixture/ 二选一），见“仓库模式”
acceptance: acceptance   # 可选：任务目录下的子目录名；agent 结束后、判定 checks 之前，其中文件按相对路径复制进工作区（覆盖同名）
allow_web: true          # 可选：本题允许 web_fetch/web_search（默认关闭，见“禁网”）
keep_tests: true         # 可选：保留模型自写的测试文件（默认判分前移出，见“判分只跑注入的验收测试”）
```

`command` check 直接 exec `args`（不经 shell，Windows 可用），工作目录为工作区，`timeout` 默认 5m。
结果里 `text` 为拼接后的命令行，另有 `exit_code`（未能启动或超时为 -1）与 `output`（合并输出的尾部，最多 4000 字节）；
汇总表与其余指标不变。`acceptance` 用于注入 agent 看不到的验收测试：它在 agent 运行结束后才复制，因此 agent 自己的文件与验收文件会一起被 checks 执行；
acceptance 目录不存在或复制失败时该任务判为失败。

## 判分只跑注入的验收测试

目的：模型留在工作区的自写测试（如 `zz_local_verify_test.go`）写错时会连带判失败，而那不是任务要求。
做法：工作区准备好（fixture / 仓库快照 / `generate` / variant `workspace/`）后、agent 运行前记下文件清单；
agent 结束后先注入 `acceptance/`，再删除清单之外、注入文件之外、名字看起来是测试的文件
（Go `*_test.go`，Python `test_*.py`/`*_test.py`，Java/Kotlin/C#/Swift `*Test(s).ext`，PHP/Rust/Ruby/JS/TS/Dart/Elixir/C++ 的对应命名），
`.git`、`node_modules`、`vendor`、`.venv` 不进入。被删的文件名写入 results.jsonl 的 `removed_tests`（升序，slash 分隔）。
只删测试文件，模型的实现文件保留。

例外：任务的 checks 就是检查模型自写的测试文件时（如 `config-two-step` 要求新增 `config_test.go` 并断言其内容），
task.yaml 写 `keep_tests: true`，本题不做清理。

## 禁网（默认关闭 web 工具）

2026-10-21 的 polyglot Go 基线（hidden 35/39）中模型调用 `web_fetch` 79 次，可能查到 Exercism 题解，分数偏高。
因此评测默认关网：suga 与 claude 默认禁用 `web_fetch`/`web_search`，`--allow-web` 或 task.yaml 的 `allow_web: true` 打开。

- suga：临时 `SUGA_HOME/config.yaml` 写 `tools.disabled: [web_fetch, web_search]`（suga 的通用配置：列出的工具不注册），
  所以模型根本看不到这两个工具（不在工具列表、`tool_search` 加载不到、`suga inspect manifest` 不列）；
  不用按次拒绝（那样模型仍会看到它们，且一回合被拒几次就结束回合，结果失真）。
  证明：拿该次运行的临时 SUGA_HOME 运行 `suga inspect manifest --json`，工具列表里没有这两个工具。
- claude：每个 prompt 加 `--disallowedTools WebFetch,WebSearch`。
- omp：评测没有可用的工具开关，**未禁网**；汇总表的 WEB 列记为 `unsupported`。
- 记录：汇总表新增 WEB 列（`off`/`on`/`unsupported`），表格下方输出一行 `web: off=N on=N unsupported=N`，
  results.jsonl 每行新增 `web` 字段；报告里引用这些值说明本次是否禁网。

每个 fixture 根目录自带 `go.mod`（`module evalfixture/<name>`），复制出去后是独立模块；
`testdata` 与 `tmp/` 都不会进入主模块的 `./...`。

多轮任务：suga 用同一个 `--session-id` 依次 `suga run`；omp 用临时 `--session-dir <out>/<task>/omp-sessions`，
第二轮起加 `--continue`（单轮任务也用临时 session-dir，不再用 `--no-session`）。用量按各轮累加，任一轮失败即停止。
现有多轮任务：`read-then-edit`（先读代码回答，再据此改文件）、`config-two-step`（先找隐患，再修复并补测试）。

## 长会话任务（多轮 + 强制换段）

衡量“长时间持续会话、切换主题也能跟上、不混乱”：任务开头给约束或事实，中间穿插无关工作，
在指定轮次之后**强制压缩**，再切回原题；检查只看确定性证据（回答文本的正则/包含、工作区文件状态、命令退出码），
不依赖模型评判。

- `compact_after: [2]`：第 2 轮结束后运行 `suga compact <会话> --json`（chat `:compact` 的命令行形式，
  由 `internal/cli/compact.go` 实现），把上下文强制换到新段；结果（`tokens_before`/`tokens_after`/`segment`）
  记入 results.jsonl 的 `turns[].compacted`。仅 `--agent suga` 支持（omp/claude 有该字段的任务直接报错）。
- 每轮回答都保留：results.jsonl 的 `turns` 数组逐轮记录 `turn`/`answer`/`duration_ms`/`compacted`；
  顶层 `answer` 仍是最后一轮。suga 的 checks 用 `turn: N`（1 起）判定第 N 轮回答，不写 `turn` 则判定最终回答（末轮）。
  没有逐轮记录的 agent（omp、claude）只有一条最终回答可用，`turn: 1` 之外的轮次判定会失败。
- 检查类型：`answer_contains` / `answer_not_contains` / `answer_matches` / `answer_not_matches`
  （后两个是正则，用来表达确定性的约束，如“不许出现中文”`[\p{Han}]`、“必须是 Markdown 列表”`(?m)^[-*] `）；
  文件与命令检查照旧，它们只跑一次，算作最后一轮的检查。
- 报告：主表之后按任务打印 `long sessions (per turn; ...)`：每轮一行（PASS/FAIL、该轮的检查、
  该轮的换段 `[compacted ~A -> ~B tokens, segment N]`、耗时），最后给出 `long sessions: passed N/M` 的长会话通过率。
- 运行：`go run ./scripts/eval --fake --bin tmp/smoke/suga --task 'longsession-*'`
  （任务的 `fake_reply` 让假模型给出确定回答，`--fake` 用来验证多轮、换段、逐轮检查的管线；
  真实模型的通过率另跑）。

任务（`testdata/tasks/`）：

| 任务 | 设计 | 关键检查 |
|---|---|---|
| `longsession-constraint-compact` | 开头给“全英文回答 / 不改 RULES.md”约束 → 两轮无关工作 → 第 2 轮后换段 → 最后重申 | 第 1、4 轮回答都不含中文；`RULES.md` 未被改 |
| `longsession-topic-switch-back` | A（读 `reports/sales.csv` 算 north 合计）做到一半 → 换 B（TCP 握手两轮）→ 第 2 轮后换段 → 回 A 继续算差额 | 第 1、4 轮都答出 42；第 4 轮提到 north；csv 未被改 |
| `longsession-quoted-rules` | 用户原话“必须 Markdown 列表 / 不要提到 deploy-prod-7” → 两轮无关工作 → 第 3 轮后换段 → 最后确认 | 第 4 轮回答是列表项；第 2、4 轮都不含禁词；`notes/rules.md` 未被改 |
| `longsession-fact-recall` | 早期三条事实（端口 7311 / 构建号 BUILD-1187 / 团队 heron）→ 两轮无关工作 → 第 3 轮后换段 → 再问 | 第 4 轮三个事实都对；不把 `archive/old-release.txt` 的旧值（BUILD-0002）当当前值 |

`--fake` 下这四个任务都 PASS（假模型按 `fake_reply` 回答且不动文件），用于验证框架本身；真实模型的分数另跑，
换段后失败才是这类任务要暴露的信号。

## 长编码任务

`long-coding-schedule`：fixture 只有 `go.mod`（`module evalfixture/longcoding`）与说明 README，agent 需从零写出
`schedule/` 包（进程内定时组件，接口与 `pkg/agentcore/schedule` 的公开 API 一致，说明改写自 T9）及其单元测试。
`timeout: 25m`；`acceptance/schedule/zz_acceptance_test.go` 是黑盒验收测试（真实时钟、短时长，含观察者重入不死锁），运行结束后注入；
checks 为 `go vet ./...` 与 `go test -count=1 ./schedule/...`（只跑验收测试：agent 自写的测试判分前已移出，见“判分只跑注入的验收测试”）。
`--fake` 下假模型不会写代码，checks 失败属预期，只用来确认注入与 command 执行的管线。
验收测试对 `pkg/agentcore/schedule` 参考实现应全部通过（把 `schedule.go` 复制到临时工作区的 `schedule/` 下验证）。

## Aider polyglot（Exercism Go 题）

[Aider polyglot benchmark](https://github.com/Aider-AI/polyglot-benchmark) 的 Go 39 题取自 Exercism Go track 的练习题，
适合做纯测试判定的 Go 题：题目只有标准库、无网络与外部依赖，Windows 主机可直接跑。
原仓库与 Exercism 的习题内容按 MIT 许可使用（见原仓库的 `LICENSE`）。

**分数只适合内部对比**：这些题目已大量进入训练语料，绝对分数偏高，不要与外部榜单比较；
用于 suga 版本间、配置间、模型间的相对对比。

转换器把题源转成任务目录（题源与生成的任务都不进仓库，生成到 `tmp/` 下）：

```bash
# 题源：浅克隆 https://github.com/Aider-AI/polyglot-benchmark 后的 go/exercises/practice 目录
go run ./scripts/eval/polyglot -src ../../tmp/refer-src/polyglot-benchmark/go/exercises/practice \
  -out tmp/eval-tasks/polyglot-go              # hidden：测试只在 agent 结束后注入

go run ./scripts/eval/polyglot -src <同上> -out tmp/eval-tasks/polyglot-go-visible -visible-tests
go run ./scripts/eval/polyglot -src <同上> -out <目录> -only 'bowling'   # 只转一题（glob）
```

每题生成 `<out>/polyglot-go-<题名>/`：

- `fixture/`：该题的桩代码（顶层非测试 `.go`）、`go.mod`、`.docs/instructions.md`（以及 `instructions.append.md`）；
  `.meta/`（参考答案）、`.approaches/`、`.articles/` 与测试文件默认不放进 fixture（`-visible-tests` 时测试文件也放进 fixture）。
- `acceptance/`：该题全部 `*_test.go`（含 `cases_test.go`），运行结束后注入工作区并覆盖同名文件，
  所以 visible 模式下 agent 改测试也不影响判定。
- `task.yaml`：`mode: allow-write`、`timeout: 5m`、`acceptance: acceptance`，
  check 为 `[{type: command, args: [go, test, ./...]}]`；prompt 要求按 `.docs/instructions.md` 实现桩文件、只改桩文件、不改测试。
- 任务名即目录名 `polyglot-go-<题名>`（`--task 'polyglot-go-bowling'` 可单跑）。
- 输出目录里会写一个空 `go.mod`（`module polyglotout`），使生成的 fixture 与 acceptance 不进入 suga 主模块的 `./...`
  （桩代码本身不完整，单独 `go vet ./...` 会报未定义类型）。

```bash
go run ./scripts/eval --tasks-dir tmp/eval-tasks/polyglot-go --model <model> --config-from ~/.suga --timeout 5m
```

注意：`counter` 原题在 Exercism 侧标记为跳过测试（`.meta/.skip_tests`），它的测试文件没有用例，
该题只检验代码能编译，通过不代表实现正确。

### 跳过无用例题

转换器会跳过无法判定实现的题，并把清单与原因打印在转换日志里（格式：`skipped N exercises:` 后每题一行 `<题名>: <原因>`）：

| 原因 | 判定方式 |
|---|---|
| `.meta/.skip_tests：原题标记跳过测试` | 题目录存在 `.meta/.skip_tests`（如 `counter`） |
| `测试文件里没有任何测试函数，只能检验代码能否编译` | 全部 `*_test.go` 里没有 `func TestXxx/ExampleXxx/FuzzXxx/BenchmarkXxx` |

两条规则在 `scripts/eval/polyglot/main.go` 的 `skipReason`；被跳过的题不会生成任务目录，也不计入分母。
跳过后题数不再是 39，报告里要同时写跳过的题名与原因（清单以转换日志为准）。

### 已知问题（不处理）

- 13 题的原题桩代码不能单独编译（缺类型/方法或签名不完整），fixture 里的 `.go` 单独 `go vet` 会报错；
  agent 要按说明补全。转换器仍照原样保留（与 Aider 基线一致），acceptance 一样是原题测试。
- 3 题的桩代码用占位签名（如参数个数与测试调用不一致），与原题测试不匹配；agent 需根据测试报错推断真实签名。
  这会影响这两类题的绝对通过率，不适合拿它们单独比较模型。

## 仓库模式

task.yaml 写 `repo: {ref: <git ref>}`（不要同时有 `fixture/`）时，工作区不是 fixture 副本，而是 eval 启动目录所在 git 仓库的 `<ref>` 快照：
运行器执行 `git archive --format=tar <ref>`（exec，不经 shell），用 Go 的 `archive/tar` 解包到 `<out>/<task>/ws/`（不依赖 tar 命令，Windows 可用），
再在 ws 里 `git init -q`、`git add -A`、`git -c user.name=eval -c user.email=eval@local commit -q -m base`，agent 看到的是只有一个 `base` 提交的干净仓库。
`generate`、`acceptance` 与 checks 照常工作（generate 在 base 提交之前写入）。

**仓库模式只能在 suga 仓库内运行 eval**（从仓库根目录执行，`--tasks-dir` 与 `--bin` 的相对路径也以此为准；`ref` 必须在该仓库里存在）。
示例：`repo-t9-schedule`（ref `202bfa0`，该提交里还没有 `pkg/agentcore/schedule`，agent 需新建；prompt 即 T9 说明，
验收测试注入到 `pkg/agentcore/schedule/zz_acceptance_test.go`，checks 为 `go vet`、`go test -count=1` 与 `go test ./pkg/agentcore/ -run Layering`）：

```bash
go run ./scripts/eval --task repo-t9-schedule --model <model> --config-from ~/.suga --suga-args "--no-project-rules"
```

另外三个仓库任务取自 suga 自己的历史提交：起点是某次修复/功能提交的父提交，prompt 只描述症状或需求，隐藏验收测试就是那次提交新增的测试（改名为 `TestAccept…` 以免与 agent 自写的测试重名；已验证在起点失败、在原提交通过，且起点上为断言失败而非编译错误）：

| 任务 | 起点 | 类型 | 验收 |
|---|---|---|---|
| `repo-fix-glob` | `9776de0^` | 修 bug：只给症状（glob 相对搜索路径时搜不到），需在 grep/find/walk 间定位 | `TestAcceptGlobRelativeToSearchPath` |
| `repo-feat-model-type` | `b121dab^` | 跨层功能：模型级 `type`/`base_url` 覆盖 provider，含文档 | `TestAcceptModelTypeOverride` |
| `repo-pipe-minimize` | `e20d71e^` | 小改动多边界：管道判断（引号、`\|\|`、反斜杠不转义） | `TestAcceptMinimizeSkipsPipedCommands` |

## 附加参数、tool_counts 与重复运行

- `--suga-args "<args>"` / `--omp-args "<args>"`：按空白切分（无引号处理），suga 附加在 `suga run` 参数末尾（`--prompt` 之后；run 只有 flag，位置无关），
  omp 放在 `omp -p ...` 参数之后、prompt 之前（prompt 是最后的位置参数）。记录在 results.jsonl 的 `agent_args`，表格新增 ARGS 列（超过 24 字符截断，空为 `-`）。
- `--suga-set dotted.key=value`（可重复）：改写临时 SUGA_HOME 的 config.yaml（值按 YAML 标量解析），例如 `--suga-set "tools.shell=C:/Program Files/Git/usr/bin/bash.exe"`；同样记入 `agent_args`。
- `tool_counts`（results.jsonl，`map[工具名]次数`，无工具调用时省略；表格不加列）：suga 取 `suga inspect stats --session <id> --json` 的 `tools[].name/calls`；
  omp 取事件流 `tool_execution_start` 的 `toolName`，没有该事件时退回统计 assistant 消息里 toolCall 块的 `name`。
- `--repeat N`（默认 1）：每个任务依次跑 N 轮。N>1 时输出目录为 `<out>/<task>-r<i>/`（N=1 保持 `<out>/<task>/`），session id 同样带 `-r<i>`；
  results.jsonl 每轮一行，新增 `round`（从 1 起）。表格逐轮显示（TASK 列为 `<task>-r<i>`），每个任务末尾追加一行 `<task> (avg)`：
  RESULT 为通过轮数/总轮数，CALLS、TOOLS、PROMPT、TOKENS(in/out)（未缓存输入/输出）与 TIME 为各轮平均，其余列为 `-`。

```bash
go run ./scripts/eval --fake --task repo-t9-schedule --repeat 2 --suga-args "--no-project-rules"
```

## variant 对比

`--variant <dir>`（可重复，仅 suga）让你用同一批任务在多个配置/规则下对比：每个任务对每个 variant 各跑
`--repeat` 轮。variant 目录可以包含三样东西，都不必同时出现：

- `config.yaml`：**叠加**到临时 SUGA_HOME 的配置上（先从 `--config-from` 或 fake provider 写入基础配置、再应用 `--suga-set`
  最后按 YAML 键深度合并该文件——map 递归合并，其它值直接覆盖，variant 优先级最高）。
- `env`：额外环境变量，`KEY=VALUE` 每行一个（`#` 注释与空行忽略；`SUGA_HOME`/`SUGA_CONFIG`/`SUGA_MODEL`/`HOME`/`USERPROFILE`
  不允许覆盖，保住隔离），传给该任务的子进程。
- `workspace/`：在任务工作区准备好（fixture/仓库快照之后、agent 运行之前）复制进每题工作区的文件，覆盖同名，
  如 `AGENTS.md`、`.agents/skills/...`。

variant 的 `name` 默认取目录名；目录重复时（同名）启动即报错。有 `--variant` 时输出为 `<out>/<variant>/<task>[-r<i>]/`，
session id 带 `<variant>-` 前缀，`results.jsonl` 每行多一个 `variant` 字段；结束时打印并写 `<out>/compare.md`：
各 variant 的通过率、平均 CALLS、平均 prompt（未缓存+缓存读写）、平均未缓存输入、总 prompt、缓存命中率与平均耗时，
WEB 列记录该 variant 本次是否禁网（`off`/`on`/`unsupported`，多个用 `+` 连接）：
以及逐任务一行（PASS/FAIL/TIMEOUT、CALLS、prompt、耗时），variant 之间结果不同的任务在“结果不一致”列标为 **是**。
不给 `--variant` 时输出结构与以前完全一致（无 variant 字段、无 compare.md）。任务必须用 `--agent suga`（且与
`--probe-prefix` 互斥）。

例子：两个 variant，一个关历史折叠、一个开到 0.5：

```bash
mkdir -p variants/fold-off variants/fold-on
printf 'context:\n  fold_at: 0\n'   > variants/fold-off/config.yaml
printf 'context:\n  fold_at: 0.5\n' > variants/fold-on/config.yaml

go run ./scripts/eval --task fix-failing-test --model opencode-go/deepseek-v4.1-flash \
  --config-from ~/.suga --variant variants/fold-off --variant variants/fold-on --repeat 2
```

## 历史折叠与思考档位

- suga 默认不折叠历史（`context.fold_every: 0`）；折叠组加 `--suga-set context.fold_every=10`。每轮的 `suga inspect trace` 中 `fold` 行给出折叠点与视图字符数。
- 与 omp 对比时两边要用同一思考档位：omp 以 `--thinking`（或其配置）为准，suga 用 `--suga-args "--thinking high"` 或 provider 配置的 `thinking`；不设时 suga 不发送 `reasoning_effort`，用 provider 默认。

## Claude Code（`--agent claude`）

```bash
go run ./scripts/eval --agent claude --claude-args "--effort high" --task repo-t9-schedule --repeat 3 \
  --out D:/work/inhere/tmp/suga-eval/<run>
```

- 运行 `claude -p <prompt> --output-format stream-json --verbose --dangerously-skip-permissions --strict-mcp-config`，多轮任务从第二轮起加 `--continue`；`--model` 原样传给 claude，`--claude-args` 放在 prompt 之前。
- 隔离：`CLAUDE_CONFIG_DIR=<out>/<task>/claude-home`，其中的 settings.json 只取 `--claude-config-from`（默认 `~/.claude`）settings.json 的 `env`（网关地址、令牌、模型映射），另加 `disableAllHooks`，所以用户的 hooks、skills、agents、插件、记忆与会话都不进入评测；不加载 MCP；关闭自动更新、非必要流量与 auto memory。settings.json 含令牌，权限 0600，运行结束即删除。
- **工作区必须在任何含 CLAUDE.md / AGENTS.md 的目录之外**：Claude Code 从工作目录一直向上读这些文件（越过工作区自己的 git 根，AGENTS.md 由内置插件读取），在 WP 里运行会把 WP 与 suga 仓库的说明带进去（实测确认；omp 与 suga 在 git 根停止，不受影响）。上层有这类文件时该任务直接报错，`--out` 改到外面，如 `D:/work/inhere/tmp/suga-eval/...`。
- 统计：模型调用按 assistant 事件的 `message.id` 去重（一次响应可拆成多条事件），工具调用数 tool_use 块；token 取每轮 result 事件的 `modelUsage` 之和（含子 agent），经网关时逐条消息的 usage 为 0，因此 FIRST 列为空。原始事件流存为 `<out>/<task>/claude-events.jsonl`。`total_cost_usd` 是 claude 按 Claude 价目的估算，经网关换了模型时无意义。

## first_call_uncached

`results.jsonl` 的 `first_call_uncached`（表格 FIRST 列）= 第一个主循环模型调用的未缓存输入 token，度量“冷启动前缀”的代价；取不到为 null。

- suga：取 `suga inspect trace <sid> --json` 中第一个 `status=ok` 且 `attrs.purpose` 不是 `compact` 的 model 事件，
  值 = `input_tokens − cache_read_tokens − cache_write_tokens`（下限 0）。**trace 里的 `input_tokens` 含缓存读写**
  （事件原始口径；`suga inspect stats` 与 ACP/stream-json 报告口径才是已扣除缓存的）。
- omp：第一个 assistant `message_end` 的 `usage.input`（omp 的 input 本身不含缓存）。

## 前缀缓存探针

`--probe-prefix`（仅 suga；与 `--task`/`--tasks-dir` 互斥）测 system+tools 前缀能否跨会话、跨工作区命中提供方缓存。
三组各重复 `--probe-repeat` 次（默认 3），相邻两次请求间隔 `--probe-gap`（默认 5s）。每次重复先发一次不计入统计的“预热”请求建立缓存，
再发被测请求；两者都用全新的 `SUGA_HOME` 与新会话，提示固定为“只回复 ok”，只取首个主循环模型调用的 input / cache_read / cache_write。

- (a) `same`：预热与被测使用同一工作区路径 `<out>/probe/ws`（每次清空重建并 `git init`）。
- (b) `cross`：预热在 `<out>/probe/ws-warm`，被测在 `<out>/probe/ws-<i>`，量化换工作区路径的缓存损失。
- (c) `control`：同 (a)，但预热与被测各设一个不同的随机 `SUGA_DEBUG_PROMPT_NONCE`（隐藏变量，把 `nonce: <值>` 放在系统提示词最前），
  前缀必然不同，作为“零命中”基线。

输出表格列：AVG_CACHE_READ、AVG_UNCACHED（= input − cache_read − cache_write）、READ/FIRST%（cache_read 占首次请求 input 的逐次平均）、
PREFIX~（预热请求的平均 input，含缓存，作为 system+tools 前缀规模的粗估：它还含极短的用户消息，约等于前缀）。
明细写入 `<out>/probe.jsonl`（每次被测请求一行，含 `warm_input`、`error`）。

判定：(a) 的 cache_read 显著高于 (c)，且覆盖首个请求 input 的大部分 → 前缀可跨会话缓存；(a)≈(c) → 不成立；
(b) 相对 (a) 的差值即跨工作区的损失（提示词里工作区路径在末尾，理论上前缀仍可命中）。波动过大时无法判定。
`--fake` 下数值无意义，仅验证管线。

## 隔离原则

- 每个任务把 fixture 复制到 `<out>/<task>/ws/` 并在其中 `git init`，使其自成 git 根，项目记忆后端探测不到上层仓库。
- 使用临时 `SUGA_HOME=<out>/<task>/home/`（同时设 `HOME`，Windows 还有 `USERPROFILE`），不读写真实 `~/.suga`；
  `--config-from` 只读复制 `config.yaml` 与 `.env`。
- 临时 `config.yaml` 强制 `memory.user.backend: files`、`memory.project.backend: files`（旧版本忽略该键）。
- 评测不写任何真实 gofer memory；记忆只落在临时目录，用后删除 `<out>`。
