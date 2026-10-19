<!-- template_id: design; template_version: 1.1.1 -->
# 批量工具调用 DSL 设计

> 状态：Draft 0.1 / 待用户审批
> 日期：2026-10-19
> 关联 issue：`suag-2p3u`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-19 | suga（gofer job） | 依据 `suag-2p3u`、`docs/research/2026-10-18-explore-efficiency.md` §7–§12 与 `docs/research/2026-10-18-xlang-eval.md`，提出受限的只读批量工具调用 DSL；明确完整工具管线、审批、hooks、输出治理、取消/错误语义、评测门槛与待用户确认问题。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：把“定位 → 过滤/投影 → 多段读取”这类有明确依赖的工具链压缩为一次模型可见的批量调用，同时保留每个子调用现有的安全、审批、hook、输出治理和 trace 语义。
- allowed_scope：`pkg/agentcore` 的工具执行扩展、批量 DSL 与调度器；`pkg/agentcore/tools/native` 的只读结果投影适配；`internal/wiring` 的注册、只读分类、schema 与审批表；对应单元测试、Linux 伪终端冒烟和真实 provider 评测；本设计与实施计划文档。
- non_goals：写工具或 shell 进入第一版批次；MCP 进入第一版批次；任意脚本、循环、条件分支、跨批次递归、调用图、全仓索引、修改稳定系统提示词、绕过审批或输出治理。
- expansion_policy：DEFER_OR_REQUEST。若实现需要改变现有审批语义、让工具直接持有 registry 绕过 runtime、把未结构化文本静默当 JSON、或开放写工具/MCP，停止并回到设计 Gate。
- review budget：中等。批量调用跨越 runtime、工具 registry、审批、hooks、trace 和上下文投影，先设计清楚边界，再按计划分阶段实施。
- 停止条件：用户批准本设计与计划前不实施产品代码。

## 背景与目标

`suag-2p3u` 调研了 pi-mono 的 codemode：模型用一次工具调用提交脚本，脚本在沙箱中组合多个工具，只有脚本的最终结果进入上下文。suga 不直接照搬 JavaScript 沙箱，而先做一条更小、可审计的路径：固定形状的批量 DSL，只支持有限的 DAG 调度和 JSON 投影。

实测证据明确了问题形状：

- 仓库探索实测中，`file_refs` → 过滤 → `file_read.paths` 是 omp 用一条 `grep -rn ... | grep -v _test.go` 管道解决、suga 却需要多次模型往返的对应场景。`explore-efficiency.md` §8.3、§9.7、§10.4–§10.7 记录了这一差距。
- T6（`personal.Runner.Run` 的直接调用方）在提示词优化后仍为 DeepSeek **14.7 步 / 26.7 次工具调用 / 362.2k 输入 token**，omp 参照为 **9.5 步 / 14.5 次调用**；工具调用下降了，但模型仍会重复 grep 和读取同一文件的不同片段（§10.3–§10.7）。
- T5 展示了批量多段读的收益：一次 `paths` 读取 4–5 个位置后，DeepSeek 达到 **4.0 步 / 4.0 次调用 / 37.4k token**，低于 omp 的 6.5 步 / 7.5 次调用；但这是提示词和回答范围约束共同作用的结果，不能把一次成功样本当成普遍保证。
- 跨语言评测的 C/F 题中，suga 总体为 **163 步 / 208 次调用**，omp 为 **196 步 / 255 次调用**；但 suga 输入 token 含 cache read，不能直接与 omp 的 token 相减。Zola-F 是反例：suga **13.5** 次调用对 omp **7.5** 次，原因是没有 shell 时反复 `file_list`/`file_grep`/`file_find`。
- `file_refs` 的限定名曾漏掉 `child.Run`，而被丢弃限定符提示没有让模型自动回跑无限定查询（§9.3–§9.5）；因此 `select` 必须显式表达过滤后的集合，并保留“丢弃了什么”的可观测信息，不能把过滤写成不可见的魔法。

### 用户可见目标

1. 模型能提交一次 `tool_batch`，描述一组有 id 的只读步骤；无依赖步骤并行，有依赖步骤在依赖完成后运行。
2. 后续步骤能用 `select` 以 gojq 表达式从前一步的**结构化结果**产生参数；未结构化结果不被静默解析。
3. 默认只把最终叶子步骤，或调用者明确指定的步骤结果返回模型；中间结果仍完整写入 trace，并按现有 spill 机制保存。
4. 每一个嵌套调用都经过参数校验、可见性、审批、BeforeTool/AfterTool、超时/取消、输出治理和 trace；批量只是编排，不是权限旁路。
5. 第一版不让写工具、shell、MCP 或 batch 自身进入批次；先用 T6 与跨语言 C/F 评测证明收益和正确性。

## 已确认事实与约束

| 事实 | 位置/证据 | 对本设计的约束 |
|---|---|---|
| `Tool` 只暴露 `Execute(ctx, call)`，registry 负责查找与一次 dispatch | `pkg/agentcore/api/tools.go:35-47` | 不能让普通工具自行拿 registry 做任意递归；需要 runtime 注入受限的 nested executor。 |
| runtime 在执行模型返回的 calls 前先做整组参数准备和校验；错误 call 不运行，其余 call 继续 | `pkg/agentcore/tool_validation.go:13-26`、`pkg/agentcore/runtime.go:496-529` | batch 也要先校验整个 DAG；单个步骤参数错误不能污染其它独立步骤。 |
| 当前 runtime 的 `executeToolCalls` 对模型同批 calls 逐个调用，`prefetchTools` 只对可预取调用做预取 | `pkg/agentcore/runtime.go:501-528` | batch 的显式并行是新能力，不能声称现有多 call 已经提供完整 DAG 并发。 |
| 每个工具调用的生命周期是 BeforeTool → registry Execute → AfterTool → transcript persist | `pkg/agentcore/runtime.go:532-637` | nested executor 必须逐步复用这个生命周期；不能只调用底层 `Tool.Execute`。 |
| approval 层先 lookup，再 Approver.Decide，拒绝返回 `ErrDenied` 形式的工具错误；取消/非拒绝错误中止 turn | `pkg/agentcore/tools/approval.go:43-101` | batch 内每一步都要独立记录 allow/deny/abort，不能把外层 batch 的 read-only 放行当成子调用授权。 |
| native read-only 默认可放行；chat/run/ACP 的默认和 legacy MCP 规则不同 | `internal/wiring/approval_policy.go:57-129`、`internal/wiring/approval_policy.go:152-201` | 第一版只选明确 `ReadOnly` 的 native 工具，避免重新解释 `auto_allow`/`read_only_allow`。这些 legacy 规则不扩展到 native batch。 |
| read-only catalog 按 `MetaOf(tool).ReadOnly` 筛选；额外的 `session_search`/`recall` 等由 wiring 组装 | `pkg/agentcore/tools/native/catalog.go:64-70,145-157`、`internal/wiring/runtime.go:463-505,787-835` | 允许集合必须按元数据和显式名单双重限制，不能按名称前缀猜测。 |
| Hook 接口有 `BeforeTool`、`AfterTool`，ToolEvent 可修改 Call/Result | `pkg/agentcore/api/hooks.go:10-20,135-143`；`pkg/agentcore/runtime.go:551-603` | 每个 step 都触发两次 hook；AfterTool 的修改是该 step 的最终结果，也决定下游投影输入。 |
| ToolResult 有 `Content`、`Artifacts`、`IsError`、`Metadata`，不保证 Content 是 JSON | `pkg/agentcore/api/tools.go:16-26` | select 只读取显式结构化 `data`；普通文本只能被返回/写 spill，不能猜测成数组或对象。 |
| 工具输出治理已有总配置和 spill/trace 观测 | `pkg/agentcore/tools/native/output.go` 及现有 trace/analyze；研究报告 §7–§12 | batch 不能另造一套截断规则；内部结果和模型可见结果分层处理，但共用字节上限、UTF-8 边界、头尾保留和 spill。 |

## 范围与非目标

范围是 `pkg/agentcore` 的受控 nested executor、只读 batch DSL/DAG/select、native 结构化投影、wiring/ACP 接线、测试、Linux 伪终端冒烟和真实 provider 评测。第一版只允许本地只读 file/session 工具。

非目标是写工具、shell、MCP、网络工具、agent、任意脚本、循环/条件、跨批次递归、持久索引、数据库迁移和稳定系统提示词改动；方案② Starlark、方案③ JS 沙箱留待后续 Gate。

## 总体方案

## 关键流程

以下各节按“校验 → 调度 → nested pipeline → 结果投影”的顺序定义关键流程。

### 1. 工具形态与 DSL

工具名暂定 `tool_batch`。它是一个普通的只读 native 工具，但运行时识别其 `BatchSpec`，把 steps 交给受控的 nested executor；不允许 `tool_batch` 通过普通 registry 再调用自己。

建议输入形状如下（字段名可在实现前置检查时微调，但语义冻结）：

```json
{
  "steps": [
    {
      "id": "refs",
      "tool": "file_refs",
      "args": {"name": "Runner.Run", "path": "."}
    },
    {
      "id": "read-sites",
      "tool": "file_read",
      "depends_on": ["refs"],
      "select": [
        {
          "from": "refs",
          "expr": ".data.hits | map(select(.class == \"call\") | (.path + \":\" + (.line|tostring)))",
          "into": "args.paths"
        }
      ],
      "args": {"paths": []}
    }
  ],
  "return": ["read-sites"]
}
```

字段约定：

- `steps` 必填，最多 32 个 step；`id` 在批次内唯一，只允许稳定的短标识符；`tool` 必填；`args` 是该工具原有 JSON 参数。
- `depends_on` 可省略；省略表示无依赖。依赖图必须无环，依赖 id 必须存在；不支持隐式“按数组顺序依赖”。
- `select` 是一个列表，每项指定一个已完成依赖 `from`、一个受限 gojq 表达式 `expr` 和目标 JSON 指针 `into`（第一版只允许写入当前 step 的 `args.*`）。表达式没有文件、网络、命令、函数回调或循环副作用；执行步骤数、输入字节和输出数组长度均有上限。
- `return` 可选。指定时只返回这些 step 的结果；省略时返回最终叶子 step 的结果。为了避免隐藏失败，返回 envelope 仍包含每步的状态摘要（`ok/error/skipped`、耗时、是否截断、spill ref），但不包含未选择的全文正文。
- 参数模板不能引用未声明依赖，也不能读取尚未完成的 step。一个 step 的 `select` 只能从 `depends_on` 中取值，防止 DSL 借选择表达式隐式增加执行边。
- DSL 没有 `if`、`for`、`while`、用户函数、动态工具名、shell、JS 或 Starlark；过滤只能写成有限的 JSON 投影/映射。

### 2. 结构化结果与非结构化结果

`select` 不直接对 `ToolResult.Content` 猜 JSON。nested executor 为允许进入 batch 的工具提供一个仅供 batch 使用的 `BatchValue`：

```json
{
  "tool": "file_refs",
  "ok": true,
  "data": {"hits": [{"path": "...", "line": 94, "class": "call"}]},
  "content": "...人类可读结果...",
  "artifacts": [],
  "is_error": false
}
```

约束如下：

- `data` 必须由工具的结构化投影适配器产生，而不是由 DSL 用正则从文本猜出来。第一版为 `file_read`、`file_list`、`file_find`、`file_grep`、`file_symbols`、`file_refs`、`recall`、`session_search` 提供适配器；适配器缺失时该工具仍可作为无 select 的只读 step，但不能作为 select 的 `from`。
- 适配器保留原始 `content` 给最终返回和 spill；`data` 只用于依赖传参，并受内存/元素上限约束。普通工具调用的模型可见输出契约不改变。
- `data` 不存在、不是 JSON 值、或表达式结果不能匹配目标参数 schema 时，当前 step 失败并给出可执行错误；不把空数组当成“解析成功”，也不把纯文本行默认为路径列表。
- 参数填充完成后，仍用目标工具自己的 schema 做一次 `PrepareToolCalls`/完整参数校验。因此 select 不能注入越界路径、错误类型或工具未声明字段。
- T6 的具体链路是：`file_refs` 产生结构化 hits → gojq 过滤 `class == call`、排除测试/不需要的类别并映射成 `path:line` → `file_read` 一次带多个 `paths` 读取实现段；如果 refs 的结果没有结构化投影，则 batch 应明确失败而不是退化成模型不可见的文本解析。

### 3. DAG 调度与并行

1. 接到 batch 后先校验总步数、id、工具名单、依赖图、环、每步参数静态形状和 `return` 引用。
2. 拓扑调度 ready 集合。无依赖或其所有依赖成功的 steps 进入执行队列；互不依赖的 steps 可并行，默认并发上限 4。
3. 依赖完成后，按依赖的 BatchValue 执行 select，再校验当前 step 的最终参数，然后进入完整 nested tool pipeline。
4. 结果按 step id 存在内存中的 DAG 状态，最终按 `return` 顺序稳定渲染；trace 事件按真实开始/结束时间记录，模型可见 envelope 按声明顺序输出，避免并发导致不可比结果。
5. 某个 step 失败时：其它已 ready 且无该失败祖先的独立 steps 继续；直接或间接依赖它的 steps 标记 `skipped`，并说明祖先失败 id；不自动重试、不回滚已经完成的写入（第一版没有写入）。批次最终 `IsError=true` 的条件是返回 step 失败、select/参数失败，或发生 abort；只有未被返回且不影响返回结果的独立失败可作为 envelope 的 warning。
6. 用户取消、父 turn context 取消、超出 batch deadline 或控制器要求 yield 时，取消 batch context，等待所有子 goroutine 收敛，未开始的标为 `cancelled`，运行中的 step 保留已有结果和 spill ref；不启动新的依赖 step。

这比当前模型在一次响应中给多个 tool call 更强的地方是“有数据依赖的调度和隐藏中间结果”；当前 `PrepareToolCalls` 是整组参数准备，runtime 的普通调用路径仍逐个 dispatch，不能表达本节 DAG。

### 4. 完整工具管线：绝不绕过

`tool_batch` 的外层调用自身先走现有 runtime：外层参数校验、BeforeTool、approval、执行、AfterTool、trace 和模型可见结果。进入 batch 后，每个 nested step 走同一组语义，顺序如下：

1. **可见性与名单**：从当前 runtime 的 registry/visibility 查找工具；未知、deferred 未加载或当前模式未注册的工具直接得到该 step 的结构化错误，不调用底层实现。
2. **权限分类**：检查 `ToolMeta` 和 batch allowlist。即使一个工具自报 read-only，也必须同时属于显式第一版名单；MCP 的 metadata 不可提升信任级别。
3. **参数校验**：select 填充后调用与普通 tool call 相同的参数准备/校验；错误 step 不触发 BeforeTool/AfterTool，也不执行工具，和 `ArgumentsError` 的现有语义一致。
4. **审批**：通过当前 policy/Approver 对每个 step 独立决定。第一版全是 native read-only，沿用 read-only allow-by-default 的现有语义；`auto_allow`/`read_only_allow` 不被扩展成“允许任意 batch 内容”。若未来允许写工具，必须逐 step 重新匹配 policy，不能因为外层 batch 获得许可就继承许可。
5. **hooks**：每一步触发 `BeforeTool` 和 `AfterTool`，ToolEvent 的 Call/Result 修改对该步有效；`AfterTool` 的最终结果才进入 BatchValue。批量外层也各触发一次，故 hook 观测会有“外层 batch + 每个 step”的层次，文档和 trace 明确标出。
6. **执行与输出治理**：调用工具的现有 `Execute(ctx, call)`，沿用工具自己的超时、路径 sandbox、网络限制和错误 policy；在进入 BatchValue 前经过现有输出治理。中间全文不回灌模型上下文，但写入 trace/spill。
7. **trace 与 parent id**：外层 call id 是 batch parent；每个 step 生成稳定的 nested call id（`<parent>/step/<id>`，若发生重试再追加 attempt），trace 的 tool start/end、approval、hook、spill 事件带 `parent_call_id`、`batch_id`、`step_id` 和 `depends_on`。parent 只用于关联，不改变 provider 的 ToolCall id。
8. **持久化/上下文投影**：外层只持久化一个模型可见的 batch assistant/tool transcript；nested step 的完整事件和 spill 进入 trace/运行 artifacts，不成为下一轮的独立 tool message。返回 envelope 中只出现指定结果，避免中间结果进上下文。

### 5. 审批、只读和写工具边界

第一版允许的集合是显式的本地只读集合：

- `file_read`、`file_list`、`file_grep`、`file_find`、`file_symbols`、`file_refs`；
- `recall`、`session_search`；它们只读会话/记忆数据，不允许通过 batch 触发写 action。

第一版不允许：

- `file_write`、`file_edit`、`file_remove`、`shell_exec`：不可回滚、可能影响用户工作区，且批量中“部分成功后失败”的审批体验和恢复语义尚未设计。
- MCP：`auto_allow` 与 `read_only_allow` 是 CLI 的 MCP 规则，不应通过一个 native batch 名称绕过 server/tool 级审批；MCP 超时、server 信任和 deferred visibility 另有生命周期。
- `web_fetch`/`web_search`：即使是读操作，也有网络、SSRF、延迟和 provider 成本，不作为第一版确定性仓库探索链的一部分。
- `agent`、`tool_search`、`update_plan`、memory 写 action 和 `tool_batch` 自身：避免递归、控制面副作用和权限集合膨胀。

因此当前 `chat` 的人工审批、`tools.approval: auto`、run 的非交互拒绝、ACP 的 ask 语义都不会被 batch 改写：第一版不会产生需要人工逐步确认的 nested step。未来若要开放写工具，倾向在 batch 执行前展示完整 manifest 并逐步审批，明确标出写路径/命令；任何一步拒绝只拒绝该步及其后代，独立读步骤是否继续必须由用户确认，不能默认静默放行。

### 6. hooks、取消、超时和错误

- **hooks**：PreToolUse/PostToolUse 在 suga 的接口中对应 `BeforeTool`/`AfterTool`。before hook 可以修改 args 或请求 yield/short-circuit；after hook 可以修改结果。hook 错误按普通 runtime hook 错误处理，batch 记录阶段和 step id，并停止受影响的依赖分支。
- **取消**：nested executor 使用父 context 的子 context；父取消、Ctrl-C、审批 abort、整体 deadline 都传播到所有运行 step。已完成独立 step 不被伪造为失败；未启动 step 写 cancelled/skipped 事件。
- **超时**：batch 有整体 deadline（默认 120 秒，不能超过调用者 context，硬上限 600 秒）；每步使用工具已有的 timeout 能力，step deadline 取剩余 batch deadline 与工具默认/配置的较小者。超时保留已有输出和退出信息，按现有 output/spill 规则处理。
- **普通工具失败**：独立分支继续，失败分支后代跳过；返回 envelope 列出每步状态和错误摘要。失败结果仍是 `IsError`，下游不能 select 它的 `data`。
- **审批拒绝**：按现有 `ErrDenied` 转成该步工具错误；如果返回 step 被拒，batch 为 error；如果只是未返回的独立 step，被拒记录 warning，但不隐藏。
- **审批取消/父 context 取消**：按现有 abort 语义结束 batch，不把 abort 伪装成可恢复的普通 tool error。
- **结构化结果错误**：select、DAG 参数或 projection 失败只影响当前 step 和后代；错误消息包含 `step_id`、`from`、表达式摘要和下一步建议，不打印完整敏感参数。

### 7. 输出预算、spill 和 trace

- batch 的模型可见**总字节预算**复用现有 `tools.max_output_bytes`；未配置时默认 **30,000 bytes**。这是整个 `tool_batch` envelope 的总预算，不是每个返回 step 各 30,000 bytes。
- 每个 nested tool 先按现有单工具预算治理；多个 selected 结果合并时再按 batch 剩余预算治理。UTF-8 按边界切，文本结果采用现有头尾保留与省略字节数提示，`file_read` 的行边界/续读提示不改。
- 被选中的结果按声明顺序分配预算：先保留每步状态和摘要，再按 head/tail 保留正文。中间 step 不进模型上下文；其完整结果写入 spill，并在 envelope/trace 给出 `out:<session>:<ref>` 或等价 artifact ref。
- `select` 使用的结构化数据在模型可见截断前计算，但受单步 `data` 字节上限、元素上限和 gojq 执行步数上限约束；不得通过 select 绕过输出治理把任意大文件全部留在内存。
- trace 记录 batch 开始/结束、调度层级、每步 start/end、参数校验、approval source、hook 结果、状态、耗时、输出字节、spill ref、parent id 和错误原因。`trace --analyze` 需要同时报告模型可见 batch 次数与 nested tool 次数，避免把“模型少调用”误报成“工具没有执行”。
- 不把所有中间 ToolResult 持久化为下一轮普通 tool messages；只有 batch envelope 和用户选定的结果进入模型上下文，满足“中间结果不进上下文”的目标。

## 架构

模型只看到一个 `tool_batch` 外层调用；runtime 持有受控 nested executor，executor 连接当前 registry/visibility、approval、hooks、native projector、scheduler、trace/spill 和 context projection。scheduler 只在显式只读 allowlist 内调度，projector 只产生受限 BatchValue.data，不读取 spill 或执行 IO。

## 与现有工具调用的关系

### 与 `file_read.paths`

`file_read.paths` 已能在一次调用读取多个已知路径/行段，且实测 T5 的 4–5 段读取有效。batch 不替代它，而是补上“路径尚未知道、要先从另一个工具结果中筛选”的数据流：

`file_refs` 结构化命中 → `select` 过滤生产代码/调用类别 → `file_read.paths` 一次读取多个实现段。

如果所有路径在模型发起调用前已知，直接用 `file_read.paths` 更简单；batch 只在需要依赖、投影或隐藏大量中间结果时使用。

### 与同一响应中的多个 tool call

模型可以在一个响应中给出多个独立 tool call，runtime 已会先整组做参数准备，且可对可预取读操作做预取。但这种形态：

- 不表达“B 等 A 的结果”，不能让 A 的输出自动填入 B 的 args；
- 普通工具结果会逐个进入对话 transcript，模型下一轮必须消费中间输出；
- 不提供 DSL 级的 DAG、select、失败分支和最终结果选择；
- 当前 runtime 的普通 dispatch 仍逐个执行，不等于受控的有界并发。

batch 额外解决的是**数据依赖 + 结果投影 + 中间结果隐藏 + 可观测的部分失败**，不是单纯把多个静态调用拼成数组。

## 模型如何知道该用它

第一版不修改稳定系统提示词前缀，避免 prompt cache 和全局行为变化；通过独立工具 schema 的描述让模型发现能力，描述必须直接说清边界：

> `tool_batch`：一次编排多个**只读** file/会话工具。用于“先搜索/找引用，再按结果过滤，最后一次读取多个位置”的有依赖流程；无依赖步骤会并行。用 `select` 的 gojq 表达式从结构化结果填充后续 args。中间结果不进入上下文，但会写入 trace/spill。只允许列出的只读工具；不能调用写工具、shell、MCP 或自身。若只是多个已知位置，优先直接用 `file_read.paths`；若只是独立并行读，当前响应可发多个 tool call。

后续是否把一句稳定提示词加入“遇到三步以上的只读链先考虑 tool_batch”，作为 Q4 单独确认，不在本设计默认范围。理由是研究 §10 证明明确提示确实改变了 `file_refs` 首步使用率和 T5 成本，但也会造成前缀缓存失效，且 GPT 的 T5 仍发生漏报；不能未经确认把行为提示永久写入稳定 prompt。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 第一版采用受限 `tool_batch` DAG，不采用 Starlark/JS 沙箱 | T6 的已证缺口是依赖、过滤和隐藏中间结果，不是通用脚本控制流；先用更小的可审计面验证收益。 |
| D2 | 只允许显式本地只读 allowlist，逐步复用完整工具管线 | 外层只读名称不能授权内部写/MCP；逐步 approval、hooks、输出治理和 trace 是安全底线。 |
| D3 | select 只读显式结构化 `data`，不猜测 Content | `ToolResult.Content` 不保证 JSON；文本猜测会造成路径注入、漏报和不可复现过滤。 |
| D4 | 共享 `tools.max_output_bytes`，中间全文进 trace/spill、不进模型上下文 | 复用现有输出治理和观测，避免第二套截断/持久化语义。 |
| D5 | 独立失败分支继续，失败后代 skipped；返回步骤失败使 batch error | 最大化只读工作，同时不把部分失败伪装成成功。 |

## 安全、数据、运维与回滚

- 安全边界由 explicit allowlist、当前 registry visibility、每步 schema、路径 sandbox 和现有 approval 共同构成；`tool_batch` 没有“以只读外壳执行写工具”的特权。
- 每批固定最多 32 steps、并发 4、整体 deadline 600 秒、select 输入/输出和 gojq steps 有界；拒绝嵌套 batch 和任意脚本，防止递归资源放大。
- 中间数据只在内存中短暂存在并写入既有 spill/trace；不新增会话数据库表、不新增索引、不写工作区文件。
- 未来的配置项优先复用 `tools.max_output_bytes`；batch 的硬限制不允许模型覆盖。若需要用户调大 timeout/并发，另立配置设计，不在实现中隐式读取未定义 key。
- 回滚顺序：先撤销 wiring 注册，batch 从模型可见工具集中消失；再撤销 runtime/nested executor 和投影适配器。无数据库迁移，旧会话中的未知历史 tool message 应按现有未知工具结果读取，不需要数据迁移。

## 风险与不做的事

| 风险/方向 | 处理 |
|---|---|
| select 误把文本当结构化数据，导致错误路径或漏报 | 只接受显式 BatchValue.data；无 data 明确失败；projection 单测覆盖空、错类型、截断和被过滤命中。 |
| 只读 batch 被用来间接写入 | 显式工具 allowlist + Meta.ReadOnly 双检 + 禁止 MCP/agent/batch；未来写工具必须过新的设计 Gate。 |
| 并发导致 trace/结果顺序不稳定 | 执行可并发，结果和模型 envelope 按 step 声明/return 顺序稳定；trace 使用 parent/batch/step id。 |
| 中间结果被 spill 但 select 需要全文 | select 在模型截断前使用受限结构化 data；全文只通过授权的结构化投影，不让 gojq 读取 spill 文件。 |
| 某一步失败后模型误以为全批成功 | 每步状态显式返回；失败 step/后代 skipped；return step 失败使 batch `IsError=true`。 |
| batch 变成另一个脚本语言 | 只允许有限 DAG、gojq 投影和固定工具集合；不做循环、条件、函数、IO 或动态 dispatch。 |
| 方案② Starlark | 后续候选。只有当评测证明 DAG + select 无法覆盖稳定出现的循环/条件/复杂聚合，并且需要可取消的指令预算时再考虑；需单独设计沙箱和权限桥。 |
| 方案③ JS 沙箱/QuickJS | 后续候选。只有当用户明确需要 pi 风格的通用组合、第三方工具发现或 Promise/复杂控制流，并接受 wasm/运行时/安全维护成本时再考虑；第一版不引入 goja/QuickJS/wazero。 |

## 评测方案与验收阈值

### 指标口径

每次运行同时记录：

1. 正确率与答案范围（生产代码必须全，测试只按题目要求统计）；
2. 模型 steps、模型可见 tool calls、nested tool calls、失败/拒绝/取消数；
3. `input_tokens`、cache read、输出 token 和墙钟。suga 与 omp token 口径不同，token 只做同一侧前后对比和量级参考，不直接相减；
4. batch 使用率、首次使用步数、select/投影失败数、spill 数、每个 step 的耗时和 trace parent 关联；
5. T6 中是否先 refs → filter → 多段 read，是否再次用 grep 补漏，是否把中间结果写入模型上下文。

### T6 主验收

用 `docs/research/2026-10-18-explore-efficiency.md` §10 的 T6 原题和同模型/只读档重复至少 3 次，并保留现有 DeepSeek 参照：14.7 步、26.7 调用、362.2k token、3/3 正确。

倾向验收门槛：

- 生产调用方正确率 **3/3**，不能用“工具调用少”抵消漏报；
- 至少 **2/3** 运行实际使用 `tool_batch`，至少一次在 batch 内完成 refs → select → 多段 read；
- 模型可见 batch/普通工具调用平均 ≤ **14**，模型 steps 平均 ≤ **10**；若直接走完整链路，目标是 2–4 steps / 1–2 个模型可见 tool call；
- nested tool calls 必须与预期 DAG 数量一致、无未授权调用；select/参数/工具错误为 0，或每次错误都能被明确归类且不损害正确答案；
- 输入 token 相对 §10 同口径平均下降至少 **30%**，但必须同时报告 cache read；若 token 未降而调用数下降，不能宣称完整达标，只记录部分收益；
- trace 能证明中间全文未进入模型上下文，且每个 nested step 有 parent id、审批和 hook 事件。

### 跨语言 C/F 对比

沿用 `docs/research/2026-10-18-xlang-eval.md` 的任务与判分规则，选择 Python、TypeScript、Java、PHP、Rust、Zola 六种语言的 C/F 题，每个格子至少 2 次；保留不支持语言/模板的失败作为真实可靠性指标，不把拒绝调用藏掉。

- C 题验收：正确率保持 100%；需要“定位多个位置”的题，模型可见调用数相对原基线下降至少 20%，小于 5 步的题只要求不回归；结构化投影不应把注释、字符串或模板陷阱当命中。
- F 题验收：正确率保持 100%；至少在需要三步以上链路的语言×题型格子中，模型可见调用数下降至少 30%，并报告 nested 调用数和墙钟是否因并发/投影增加；Zola/不支持语言若无适配器，应明确“不使用 batch”，不能强行声称收益。
- 统一比较 suga 与自身基线，omp 只作行为参照；token 口径不一致时以 steps、model-visible calls、correctness 为主。

若 T6 正确率下降、select 结果不稳定、或为了达到调用数阈值需要放宽 allowlist，则停止扩展并回到设计 Gate；不以“模型可以自己纠错”掩盖批量编排的错误。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 名称用 `tool_batch`，还是更明确的 `batch_read`？ | 倾向 `tool_batch`，因为 DSL 是 DAG 编排；但第一版必须用显式只读 allowlist，描述中明确不是通用脚本。若希望能力名即边界，接受 `batch_read`。 |
| Q2 | 第一版是否只允许本地 file_* + `recall`/`session_search`，排除网络读工具？ | 倾向排除 `web_fetch`/`web_search`；先控制延迟、SSRF/网络审批与可复现性，评测稳定后另议。 |
| Q3 | 非结构化工具结果能否由内置 parser 转成 `data`，还是必须先改工具提供结构化投影？ | 倾向每个 allowlisted 工具提供显式 BatchValue projector；不对任意文本做隐式解析。保留人类可读 Content 兼容普通调用。 |
| Q4 | 是否把“复杂只读链优先考虑 tool_batch”写进稳定系统提示词？ | 当前不写；先只靠独立工具描述。若 T6 未使用率达标，再以 prompt cache 成本和 GPT/DeepSeek 正确率数据为依据另立设计。 |
| Q5 | 返回 step 默认用最终叶子，还是必须显式 `return`？ | 倾向省略时返回最终叶子，减少模型参数；生产链建议显式 `return`，schema/结果中提示默认行为。 |
| Q6 | 某个独立 step 失败时是否继续其它分支？ | 倾向继续无关分支，跳过失败后代；返回 step 失败使整个 batch error。这样最大化已完成只读工作，同时不伪造全成功。 |
| Q7 | 未来开放写工具时，审批是整批一次确认还是逐 step？ | 倾向逐 step 复用现有 policy，并在首次确认前展示完整 manifest；不允许外层一次许可覆盖后续动态参数。此问题不阻塞只读第一版。 |
| Q8 | 是否在第一版引入 gojq 依赖？ | 倾向使用纯 Go 的 gojq 兼容 evaluator，以覆盖 T6 的 `map/select`；若依赖体积或资源控制不可接受，再退化为明确的 JSONPath 子集，但必须重新评估 T6。 |

## 结论与人工计划 Gate

本设计选择方案①的受限批量 DSL，而不是直接引入 Starlark 或 JavaScript 沙箱。第一版只做只读 DAG、显式结构化投影和完整嵌套工具管线；写工具、shell、MCP、网络工具、任意脚本和递归均不做。用户批准设计与实施计划后，才进入代码实现；批准前不改产品代码。
