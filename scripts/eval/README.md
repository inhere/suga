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
`--probe-prefix` / `--probe-repeat` / `--probe-gap`（见“前缀缓存探针”）。
终端输出汇总表；`<out>/results.jsonl` 每任务一行（成败、checks 明细、答案、调用/token/命中率/成本/工具调用数、
`tool_raw_bytes`/`tool_inline_bytes`，后两项在 `suga inspect stats` 提供之前为 null）。个别任务失败退出码仍为 0，运行器自身出错才非 0。

## 任务格式

`testdata/tasks/<name>/task.yaml` + `fixture/`：

```yaml
name: fix-failing-test
mode: allow-write        # read-only（默认，suga run 默认模式）| allow-write（加 --allow-write）
fake_mode: edit          # 可选，fakellm 的 tool|text|edit，默认 text
prompt: ...              # 单轮；多轮改用 prompts（二选一）
prompts:                 # 多轮：同一会话按顺序发送；answer_contains 判定最后一轮的回答
  - 第一轮 ...
  - 第二轮 ...
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
  - {type: command, args: [go, test, -count=1, ./...], timeout: 5m}   # 在工作区内执行，退出码 0 为通过
timeout: 25m             # 可选：任务级超时，覆盖全局 --timeout（omp 的 --max-time 同步使用）
repo: {ref: 202bfa0}     # 可选：仓库模式（与 fixture/ 二选一），见“仓库模式”
acceptance: acceptance   # 可选：任务目录下的子目录名；agent 结束后、判定 checks 之前，其中文件按相对路径复制进工作区（覆盖同名）
```

`command` check 直接 exec `args`（不经 shell，Windows 可用），工作目录为工作区，`timeout` 默认 5m。
结果里 `text` 为拼接后的命令行，另有 `exit_code`（未能启动或超时为 -1）与 `output`（合并输出的尾部，最多 4000 字节）；
汇总表与其余指标不变。`acceptance` 用于注入 agent 看不到的验收测试：它在 agent 运行结束后才复制，因此 agent 自己的文件与验收文件会一起被 checks 执行；
acceptance 目录不存在或复制失败时该任务判为失败。

每个 fixture 根目录自带 `go.mod`（`module evalfixture/<name>`），复制出去后是独立模块；
`testdata` 与 `tmp/` 都不会进入主模块的 `./...`。

多轮任务：suga 用同一个 `--session-id` 依次 `suga run`；omp 用临时 `--session-dir <out>/<task>/omp-sessions`，
第二轮起加 `--continue`（单轮任务也用临时 session-dir，不再用 `--no-session`）。用量按各轮累加，任一轮失败即停止。
现有多轮任务：`read-then-edit`（先读代码回答，再据此改文件）、`config-two-step`（先找隐患，再修复并补测试）。

## 长编码任务

`long-coding-schedule`：fixture 只有 `go.mod`（`module evalfixture/longcoding`）与说明 README，agent 需从零写出
`schedule/` 包（进程内定时组件，接口与 `pkg/agentcore/schedule` 的公开 API 一致，说明改写自 T9）及其单元测试。
`timeout: 25m`；`acceptance/schedule/zz_acceptance_test.go` 是黑盒验收测试（真实时钟、短时长，含观察者重入不死锁），运行结束后注入；
checks 为 `go vet ./...` 与 `go test -count=1 ./schedule/...`（agent 自己的测试与验收测试一起跑）。
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
