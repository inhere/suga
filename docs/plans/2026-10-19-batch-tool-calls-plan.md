<!-- template_id: plan; template_version: 1.2.0 -->
# 批量工具调用 DSL 实施计划

> 状态：Draft 0.1 / 待用户审批
> 日期：2026-10-19
> 对应设计：`docs/design/2026-10-19-batch-tool-calls-design.md`
> 关联 issue：`suag-2p3u`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-19 | suga（gofer job） | 将方案①拆为前置 Gate、受限只读 DSL/投影、DAG 调度、完整工具管线接入、接线与冒烟、真实模型评测六个波次；明确不做写工具、shell、MCP、网络工具、脚本沙箱与递归。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：实现一个只读、可审计、有限 DAG 的 `tool_batch`，把“refs → select 过滤 → 多段 read”压缩为一次模型可见调用，同时不绕过任一既有工具管线。
- allowed_scope：设计文档所列 `pkg/agentcore` runtime/tool API、batch 调度与投影、`internal/wiring` 注册/分类、native 只读工具适配、单元测试、Linux 伪终端冒烟、真实 provider 评测和相关文档。
- non_goals：写文件、编辑、删除、shell、MCP、web 工具、agent/tool_search/update_plan、任意脚本、循环/条件/用户函数、动态工具发现、跨批次递归、数据库迁移和稳定提示词改动。
- expansion_policy：DEFER_OR_REQUEST。若需要改变审批策略、ToolResult 结构、trace 持久化格式、稳定系统提示词，或让 batch 直接持有 registry 绕过 runtime，停止并回到设计 Gate。
- review budget：中等；先实现最小只读路径，所有权限、投影、取消和 trace 语义需有测试证据。
- 停止条件：设计和计划获用户批准前不改产品代码；任一前置 Gate 失败不进入后续波次。

## 目标与完成定义

### 目标

1. 新增 `tool_batch`，输入 `steps[]`，每项有唯一 `id`、工具名、原始 args、可选 `depends_on` 与 `select`；无依赖步骤并行，有依赖步骤按 DAG 执行。
2. 第一版只允许显式本地只读工具：`file_read`、`file_list`、`file_grep`、`file_find`、`file_symbols`、`file_refs`、`recall`、`session_search`；不允许 MCP、网络、写工具、shell、agent 或自身递归。
3. 对允许工具提供显式结构化 `BatchValue.data` 投影；select 用受限 gojq（或经 Gate 批准的等价 JSON 查询子集）填充后续 args；没有结构化 data 时明确失败，不从普通文本猜 JSON。
4. 每个 nested step 都复用参数校验、可见性、approval、`BeforeTool`/`AfterTool`、错误 policy、超时/取消、输出治理和 trace；中间全文不进入模型上下文，但写入 trace/spill。
5. 结果预算统一由现有 `tools.max_output_bytes` 管理，默认 30,000 bytes 作为整个 batch envelope 的总预算；返回默认最终叶子，也支持显式 `return` 选择步骤。
6. T6 正确率不回退，并在真实评测中验证模型可见调用数、步数/token、nested 事件和 parent id 的收益。

### 完成定义

- `tool_batch` 的 schema、DAG 校验、并行/依赖/取消/错误/return 语义有单元测试；循环、未知依赖、重复 id、越界 step 数、递归 batch 和未结构化 select 都 fail closed。
- allowlist 和 `ToolMeta.ReadOnly` 双重限制生效；写工具、shell、MCP、web、agent、memory 写 action 均被拒绝且不触发底层执行。
- 每个 step 的正常、参数错误、approval 拒绝、hook 修改/失败、超时、父取消和 spill/trace 关联均有测试；独立分支在无关失败时继续，失败后代 skipped。
- `go test -count=1 ./internal/... ./pkg/...`、`go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")` 无输出，`staticcheck ./...` 无新告警；代码类实现阶段另按仓库规则运行 race 测试。
- Linux 容器 `make smoke-chat` 新场景通过，并完成一次反向验证：临时去掉 batch 注册或投影逻辑时，trace 断言必须失败；Windows 主机不运行 ptysmoke，交付说明标为待 Linux 验证。
- T6 及跨语言 C/F 评测按设计阈值报告正确率、模型 steps、model-visible/nested calls、token（含 cache）、墙钟、失败/拒绝/取消、spill 和 trace parent；不隐藏负面结果。
- 每个功能点单独本地 conventional commit；提交前 `git status --short` 为空或仅有本功能未提交文件，并核对 `git diff --cached --name-only`；不 push、不打 tag、不改 `.gofer/tracker`。

## 范围、排除项与授权

### 范围

- `pkg/agentcore`：batch 输入/结果类型（若需要）、受控 nested executor、DAG 调度、select/投影、取消/超时、trace/parent 元数据和运行时接入。
- `pkg/agentcore/tools/native`：只读工具的结构化 batch projector；不得改变普通调用的 Content、参数或输出语义。
- `internal/wiring`：`tool_batch` 的 catalog/registry 接线、只读工具清单、schema properties 非空测试、审批矩阵与 ACP 分类。
- 测试：`pkg/agentcore` 与 native 单测、wiring/ACP 分类测试、Linux `scripts/ptysmoke` 新场景与 `Makefile` 接入。
- 文档：本设计、本计划；评测完成后另行更新研究报告或新增复测报告。

### 排除项

- `file_write`、`file_edit`、`file_remove`、`shell_exec`：不可回滚、部分成功恢复和审批展示尚未设计。
- MCP 与 `auto_allow`/`read_only_allow` 扩展：避免通过 native batch 绕过 server/tool 级权限；MCP/deferred/单次超时另有管线。
- `web_fetch`、`web_search`：网络与 SSRF/成本/延迟不适合第一版确定性仓库探索链。
- `agent`、`tool_search`、`update_plan`、memory 写 action、batch 自身：避免控制面副作用和递归。
- Starlark、goja/QuickJS/wazero、任意脚本、循环/条件/动态工具名、持久索引、数据库 schema、稳定系统提示词改动。

### 授权与外部动作

- 当前请求仅授权两份设计/计划文档和一个 `docs:` 本地提交；本批不实现产品代码，不修改 `.gofer/tracker`。
- 后续实现需用户批准本设计和计划后，另按各阶段 Gate 授权 Windows 构建/测试、Linux 伪终端、真实 provider 以及可能的依赖下载。
- `host_or_non_offline_action=REQUIRED`：真实 provider、Linux ptysmoke 和跨平台主机验证不能由本批文档校验替代。

## 输入与批准证据

- issue：`suag-2p3u`，记录 pi codemode 与三条路径；方案①为固定 batch DSL，后续方案② Starlark、方案③ JS/QuickJS。
- 研究：`docs/research/2026-10-18-explore-efficiency.md` §7–§12；重点是 T6 的 refs/过滤/多段读、omp 管道基线、提示词批复测、GPT 有效样本与失败样本。
- 研究：`docs/research/2026-10-18-xlang-eval.md`；18 题 × 2 次、六种语言的 C/F 对比、token 口径限制和不支持语言的失败成本。
- 代码事实：`pkg/agentcore/runtime.go:496-637`（工具校验、hook、执行、持久化）、`pkg/agentcore/tool_validation.go:13-48`、`pkg/agentcore/tools/approval.go:43-101`、`pkg/agentcore/api/hooks.go:10-20,135-143`、`pkg/agentcore/api/tools.go:16-47`。
- 接线事实：`pkg/agentcore/tools/native/catalog.go` 的只读元数据/筛选、`internal/wiring/runtime.go:463-505,787-835`、`internal/wiring/approval_policy.go:57-201`、`internal/wiring/approval_table_test.go:63-112`、`internal/wiring/readonly_test.go`。
- 待批准：本计划与对应设计均为 Draft 0.1；未获得用户批准不进入 T0 之后的实现。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 受控 nested tool executor，逐步复用完整 runtime 管线 | `pkg/agentcore/runtime.go:532-637`、`tools/approval.go:43-101`、`api/hooks.go`、`api/tools.go` | 现有校验、registry、approval、hooks、错误 policy、transcript/spill/trace owner | 在 runtime owner 内抽出可注入的受控执行入口；不让普通 Tool 直接拿 registry | OWNER_EXTENSION | 当前 Tool.Execute 不接受 nested executor；普通模型多 call 不表达 DAG 或隐藏中间结果 | 若另建底层 dispatch，会产生绕过 approval/hooks/output 的第二套管线；必须删除或合并平行路径 |
| CAP-02 | 有界 DAG 调度、并行、依赖失败传播与取消 | 当前 `executeToolCalls` 的逐个调用与 prefetch；标准库 context/sync/排序 | 复用 runtime context、现有 max steps/timeout 和工具结果类型 | 新增 batch scheduler 与 DAG 状态；并发上限/步数/时间硬上限 | MINIMAL_NEW_MODULE | 现有同响应 calls 只有静态列表，不能从结果填 args 或控制后代 skipped | 调度器不得成为脚本引擎；固定 DAG、无循环/条件/动态 dispatch |
| CAP-03 | select 与显式结构化结果投影 | `ToolResult.Content/Artifacts/Metadata`、native 工具输出、issue 的 gojq 建议 | 保留 Content/Artifacts 给普通调用；复用 schema validator | 新增 BatchValue/projector 注册表与 gojq/JSON evaluator | MINIMAL_NEW_MODULE | ToolResult 不保证 Content 是 JSON，隐式文本解析会误导 | 每个 projector 只拥有自己的结构化视图；禁止读取 spill 文件或绕过字节/元素预算 |
| CAP-04 | 只读 allowlist 与 wiring/ACP 分类 | `native.Catalog`、`MetaOf`、`BuildTools(..., false)`、审批表/ACP 表 | 复用 ReadOnly 元数据、catalog、现有 policy | 新增 batch 允许集合和 schema/注册测试 | DIRECT_REUSE | 外层 batch 名称不能证明内部工具安全；MCP 自报元数据不可信 | allowlist 与 Meta.ReadOnly 双检；禁止 `tool_batch` 自身和未注册/deferred 工具 |
| CAP-05 | parent/step trace、spill 与模型上下文投影 | 现有 tool trace/analyze、ToolBatchMetaKey、Artifacts/spill | 沿用 trace schema/分析口径和输出治理 | 增加 batch_id/parent_call_id/step_id/dependency 字段或等价关联 | OWNER_EXTENSION | 当前 transcript 以每个模型 tool call 为单位，无法直接表达隐藏 nested 事件 | trace 兼容旧事件；若需持久化 schema 变化先停在设计 Gate |
| CAP-06 | 真实评测和交互冒烟 | `scripts/ptysmoke`、`fakellm.py`、`suga trace --json/--analyze`、T6/C/F 基线 | 复用现有场景、trace、评测口径 | 新增 batch 场景与结果分析字段/报告 | DIRECT_REUSE | 当前没有 batch 的“中间不进上下文”和 parent 事件回归 | 反向验证必须证明检查会失败；真实 provider 需隔离 SUGA_HOME，不落密钥 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| MOD-BATCH-01 | CAP-02 | `pkg/agentcore/batch/` 或 runtime 内拆分的 batch scheduler 文件 | runtime tool loop、标准库 context/sync/排序 | 当前没有 DAG 调度 owner | 调度需独立测试，但必须由 runtime 传入受控 executor | T6 仍有 26.7 次调用且 refs→filter→read 依赖链是明确缺口 | owner 归 `pkg/agentcore` 能力层；不依赖 `internal/*`，不引脚本 runtime | 注册回滚后工具不可见；scheduler 可整体删除，不留配置、迁移或索引 |
| MOD-BATCH-02 | CAP-03 | `pkg/agentcore/batch/` 或 native projector owner 内的结构化结果适配 | `ToolResult`、native 工具输出、schema validator | 当前没有 BatchValue/projector contract | 每个允许工具需要稳定 data 视图，不能把职责塞进 DSL parser | T6 的 refs→过滤→read 需要结构化 hits；Content 不保证 JSON | projector 与具体工具同生命周期；删除后普通 Content 语义不变 | 撤销注册并删除 projector contract，不留持久化或文本解析旁路 |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 直接照搬 pi 的 JS/QuickJS codemode | CAP-02 | 删除后固定 DAG + select 仍覆盖 T6；JS 沙箱需 Promise bridge、VM 资源和安全维护，超出第一版。 |
| Starlark 方案② | CAP-02 | 删除后第一版不支持循环/条件，但已有评测尚未证明需要脚本控制流；等 DAG+select 评测失败再立项。 |
| 通用任意工具 `tool_batch`（含写/MCP/shell） | CAP-04 | 删除后受限只读 batch 仍可验证收益；通用范围会混入审批、不可回滚副作用和 MCP 信任边界。 |
| 让 batch 直接调用 `Tool.Execute`/registry | CAP-01 | 删除该旁路后仍可通过受控 nested executor 实现；直接调用会绕过参数校验、approval、hooks、output/spill 和 trace。 |
| 把普通文本 Content 用 JSON 正则/`json.Unmarshal` 猜成 data | CAP-03 | 删除后显式 projector 已覆盖允许工具；隐式猜测会把截断/人类可读文本误当路径或数组，存在安全和正确性风险。 |

## 前置检查与 fail-closed 条件

### T0 必须确认

- 记录 `git status --short`、HEAD、branch，确认本批只有两份 docs 目标文件；提交前不 stage 其它文件。
- 对照 `runtime.go:496-637`、`tools/approval.go`、`api/hooks.go` 和当前 trace/spill 实现；如果 trace 没有可扩展的 parent 关联字段，先停在设计 Gate，不隐式改协议。
- 确认 `ToolResult` 的结构化来源：若允许工具当前没有可稳定提供的 projection，先实现 projection contract/测试，不从 Content 猜测。
- 确认 gojq 或等价 evaluator 的依赖、执行步数、输入/输出字节上限和取消能力；若只能引入无界 evaluator，停止并重新设计。
- 确认 native catalog/wiring/ACP 的 read-only 分类及 schema `properties` 非空约束，batch 自身应为只读且不能递归。
- 确认现有 `tools.max_output_bytes`、spill、trace JSON 和模型 context 的实际边界；不能只在文档中假定“中间不进上下文”。

### Fail-closed 条件

- 基线近端测试失败：停止，不开始代码波次。
- nested step 无法逐个进入现有 approval/hooks/output/trace：停止，不用“内部调用”名义绕过。
- select 能读取未经限制的 spill、文件、网络或命令：停止并拒绝实现。
- 任何写/MCP/network 工具能通过别名、Meta 错误或动态名进入 batch：停止并补 allowlist/双重校验。
- 取消后 scheduler 仍启动新 step、或并发 goroutine 无法在 deadline 内收敛：停止。
- 真实评测出现正确率下降、结构化投影漏报或中间正文进入下一轮 context：回滚该波次并回到设计 Gate。

## 波次与依赖

| 波次 | 任务 | 依赖 | 执行方式 |
|---|---|---|---|
| W0 | T0 基线、代码事实与 evaluator/trace Gate | 无 | 只读检查；失败停止 |
| W1 | T1 DSL schema、静态 DAG 校验、allowlist 与结构化 projector contract | T0 | 单测先行，提交 |
| W2 | T2 nested executor 接入完整 pipeline 与 trace parent | T1 | runtime 单测，提交 |
| W3 | T3 scheduler：并行、select、失败传播、取消/超时、预算 | T1–T2 | 单测，提交 |
| W4 | T4 wiring/ACP 分类、native projector 实现与 schema/审批矩阵 | T2–T3 | 接线测试，提交 |
| W5 | T5 Linux ptysmoke 与反向验证 | T4 | Linux 容器执行，提交 |
| W6 | T6 真实 provider T6/C/F 评测与研究报告 | T5 | 隔离环境，提交 |

## 任务

### T0 基线与 Gate

- 文件：无产品修改；记录相关代码、trace/spill 和 evaluator 依赖事实。
- 动作：执行近端 `go test -count=1 ./pkg/agentcore/... ./internal/wiring/... ./internal/acpserver/...`；确认当前 branch/status；核对 `Tool.Execute`、approval、hooks、output、trace、wiring 事实。
- 验证：输出同时记录 exit code 与原始输出；确认 `go build ./...`、`go vet ./...`、`gofmt -l $(git ls-files "*.go")`、`staticcheck ./...` 的基线状态。
- 完成标准：基线全绿，或明确写 `BASELINE_FAILED` 并停止；不在 T0 期间改代码。
- 依赖：无。

### T1 DSL、allowlist 与结构化 projector contract

- 文件：`pkg/agentcore` 的 batch 类型/校验文件（按现有 owner 拆分）、`pkg/agentcore/tools/native` 的 projector 文件与测试。
- 动作：实现 steps/id/tool/args/depends_on/select/return 的 JSON schema；限制 32 steps、select 只能引用直接依赖、DAG 无环；固定 allowlist、禁止 batch 自身/写/MCP/network/agent；定义 `BatchValue` 和显式 projector 接口。
- select：先采用受限 gojq evaluator；若 T0 证明依赖或资源边界不可接受，停下并让用户确认 JSONPath 子集，不静默替换语义。
- 单测：`t.Run` 覆盖重复 id、未知工具/依赖、环、过多 steps、非法 target、跨依赖引用、递归、无 data、data 类型不匹配、projector 保留 Content 但不把文本猜成 JSON、每个 allowlisted 工具的代表投影。
- 验证：`go test -count=1 ./pkg/agentcore/...`；schema `properties` 非空；新函数均有真实调用点。
- 完成标准：静态错误 fail closed；无写/MCP/network 工具可进入；提交 `feat(agentcore): define read-only batch DSL`。
- 依赖：T0。

### T2 nested executor 与完整工具管线

- 文件：`pkg/agentcore/runtime.go` 或拆出的 runtime-owned executor、trace 事件定义/适配、相关测试。
- 动作：让 batch step 经当前 registry/visibility lookup、目标 schema 参数校验、当前 `Approver`、`BeforeTool`、工具 Execute、`AfterTool`、错误 policy、输出治理和 spill；传递 parent/batch/step/dependency 元数据；禁止直接调用底层 Tool。
- 审批：read-only native 继续现有 allow-by-default；拒绝/abort 分别保持 `ErrDenied` 可恢复工具错误与取消终止语义；不得把外层 batch 的许可传给未来写工具。
- hooks：验证外层 batch 与每个 step 都有 Before/After；hook 对 Call/Result 的修改影响实际 step/下游 projector；hook error 只影响该分支并按 runtime policy 处理。
- 单测：正常 step、参数错误不触发 hooks/execute、unknown/deferred/disabled、approval deny/abort、before/after mutation、tool error、spill、parent id、普通 batch transcript 只保留 envelope。
- 验证：`go test -count=1 ./pkg/agentcore/...`；trace JSON 断言 step 与 parent 关联；检查 `pkg/agentcore` 不依赖 `internal/*`。
- 完成标准：没有第二套 bypass dispatch；提交 `feat(agentcore): execute batch steps through runtime pipeline`。
- 依赖：T1。

### T3 DAG scheduler、select、取消与预算

- 文件：`pkg/agentcore` scheduler/select/预算实现及测试。
- 动作：ready 集合并发上限 4；依赖完成后 select → 参数校验 → nested executor；独立分支继续，失败后代 skipped；batch deadline 默认 120 秒、硬上限 600 秒、不得超过父 context；取消后不启新 step并等待已运行任务收敛。
- 输出：单步先按现有治理；全 batch envelope 再用 `tools.max_output_bytes` 总预算（默认 30,000 bytes），UTF-8 头尾保留；selected 结果按 return 顺序；中间全文写 trace/spill 不进下一轮 context。
- 错误：返回 step 失败、select/参数失败、abort 使 batch error；不影响 return 的独立失败为 warning；每步状态为 ok/error/skipped/cancelled，明确祖先失败。
- 单测：独立并行（用 barrier 证明并发）、依赖串行、多个 ready、环/死锁保护、失败传播、部分结果、deadline、context cancel、超时保留输出、预算头尾/spill、稳定返回顺序、select gojq steps/输入输出上限。
- 验证：`go test -count=1 ./pkg/agentcore/...`；必要时 `go test -race` 在 Linux/CI 运行；不在 Windows 声称已完成 race。
- 完成标准：所有 goroutine 在 deadline 内收敛且不存在数据竞争；提交 `feat(agentcore): schedule read-only batch DAG`。
- 依赖：T1、T2。

### T4 wiring、native projector 与审批/ACP 分类

- 文件：`pkg/agentcore/tools/native/catalog.go` 或现有 catalog owner、`internal/wiring` 工具清单/审批表/schema 测试、`internal/acpserver` policy table、native projector tests。
- 动作：注册 `tool_batch` 为只读工具；wiring 在 run/chat/ACP 中可见；同步只读清单、`TestToolSchemasDeclareProperties`、approval table、ACP Search/Read 分类（按实际 owner 语义确认）、disabled hints 与子 agent名单（第一版 batch 不进子 agent除非 Gate 明确批准）。
- allowlist：即使 registry 中有写工具/网络工具，也由 batch 内 allowlist+Meta.ReadOnly 拒绝；MCP 自报 read-only 不得提升权限；`recall`/`session_search` 仅允许只读 action。
- native projectors：为 `file_*`、`file_symbols`、`file_refs`、`recall`、`session_search` 提供显式结构化 data；普通调用 Content、错误和输出限制保持兼容。
- 验证：`go test -count=1 ./internal/... ./pkg/...`；`go build ./...`；`go vet ./...`；`gofmt -l $(git ls-files "*.go")` 无输出；`staticcheck ./...` 无新告警。
- 完成标准：read-only catalog 正确、schema properties 非空、审批表不放宽；提交 `feat(wiring): register read-only batch tool`。
- 依赖：T2、T3。

### T5 Linux 伪终端冒烟与反向验证

- 文件：`scripts/ptysmoke/` 新场景、`Makefile` 的 `smoke-chat` 接入、必要时场景 README。
- 动作：使用 `ptydrive.PtySession` 驱动真实二进制和 `fakellm.py`，让 fake model 发起：至少一个无依赖并行 batch、一个 `file_refs → select → file_read.paths` DAG；检查 prompt/模型请求只见 batch envelope，不见中间全文；用 `suga trace <sid> --json` 断言 parent/step/dependency/approval/hook/spill 事件。
- 反向验证：临时去掉 catalog 注册、投影或 parent 关联中的一项，场景断言必须失败；恢复修改后重跑。临时产物只放 worktree `tmp/`，结束删除。
- 验证：Linux 容器 `make smoke-chat`；记录原始输出与反向失败输出。Windows 主机不运行，交付说明写“未运行，待监督在 Linux 容器验证”。
- 完成标准：新场景与既有场景全绿；提交 `test(ptysmoke): cover read-only batch pipeline`。
- 依赖：T4。

### T6 真实 provider 与 T6/C/F 评测

- 文件：新增或更新 `docs/research/2026-10-19-batch-tool-calls-eval.md`（实现完成后）、必要时 CHANGELOG「未发布」。
- 动作：隔离 `SUGA_HOME=tmp/...`，不复制 `.env`/密钥；同模型、只读档、同题复测 T6 至少 3 次，并跑 xlang C/F 六种语言各至少 2 次；记录 raw trace/export，结束删除二进制、home、数据库和答案产物。
- 指标：正确率/答案范围、steps、model-visible calls、nested calls、工具错误/拒绝/取消、select 错误、input/cache/output token、墙钟、batch 首次步数、spill、parent 关联；token 不跨 suga/omp 直接相减。
- 验收：T6 正确率 3/3；至少 2/3 使用 batch 且至少一次完成 refs→select→多段 read；平均 model-visible calls ≤14、steps ≤10；输入 token 同口径下降 ≥30%或如未达成如实记录；中间全文不入 context，nested 事件完整。C/F 保持 100% 正确率，三步以上格子的 model-visible calls 下降 ≥20–30%（按设计分 C/F 门槛），不支持语言不强行使用 batch。
- 真实 provider：至少一个真实 provider 跑带 batch 工具调用的一轮；若 provider 不可用，报告 `UNVERIFIED_REAL_ENV` 和未验证原因，不伪造通过。
- 完成标准：报告含前后表、原始 trace 摘录、方差、失败样本和 Gate 决策；提交 `docs: record batch tool evaluation`。
- 依赖：T5。

## 提交与合并顺序

1. `docs:` 本批设计与计划（当前任务，一个提交）。
2. `feat(agentcore): define read-only batch DSL`：T1。
3. `feat(agentcore): execute batch steps through runtime pipeline`：T2。
4. `feat(agentcore): schedule read-only batch DAG`：T3。
5. `feat(wiring): register read-only batch tool`：T4。
6. `test(ptysmoke): cover read-only batch pipeline`：T5。
7. `docs:` 真实评测报告：T6。

每个提交只 stage 对应文件，提交前核对 `git status --short`、`git diff --cached --name-only` 和 `git diff --stat`；不 push、不打 tag、不改 tracker。

## 回滚与恢复

- T4 先撤销 wiring/catalog 注册，工具从模型可见集合消失；T3/T2/T1 可按依赖逆序 revert。
- 无数据库迁移、无持久索引、无工作区写入；若 trace 事件结构确实变化，必须先完成兼容性设计并另设恢复点。
- 若评测正确率下降、select 误报/漏报、nested 事件缺 parent、或中间正文泄露到 context，立即停止后续波次，保留失败 trace，回滚最近波次并回到设计 Gate。
- 若只是批量收益未达阈值，不开放写/MCP/脚本补救；记录负面结果，重新比较是否需要方案②或③。

## 人工 Gate

- Gate A（现在）：用户批准设计与本计划后才实施。
- Gate B（T0 后）：代码事实、trace/spill 可扩展性、evaluator 资源边界和基线测试均通过；否则停止。
- Gate C（T3/T4 后）：完整管线、审批、取消、输出治理和只读接线证据齐全后才跑 ptysmoke。
- Gate D（T6 后）：真实评测决定是否保留方案①；未达正确率或 context 隔离门槛时不得继续开放更宽工具范围。
- push、tag、release、部署、gofer 配置和 `.gofer/tracker` 修改均不在本计划授权内。

## 可追溯性

| 完成定义/决策 | 任务 | 证据 |
|---|---|---|
| DSL 字段、DAG fail-closed、结构化 data、allowlist | T1 | batch schema/校验/projector 单测；无写/MCP/递归路径 |
| 完整嵌套管线、逐步审批/hooks、parent trace | T2 | runtime 单测、trace JSON、hook/approval/spill 断言 |
| 并行、依赖、select、失败传播、取消、预算 | T3 | scheduler 单测、取消/超时/结果 envelope 原始输出 |
| run/chat/ACP 接线与只读分类 | T4 | wiring/ACP/ schema 测试、build/vet/gofmt/staticcheck 原始输出 |
| 真实 chat 行为、context 隔离、trace 观测 | T5 | Linux `make smoke-chat`、`suga trace --json`、反向验证 |
| T6 与跨语言 C/F 收益、正确性和方差 | T6 | 评测报告、trace/export 摘录、真实 provider 结果 |
| 方案②/③后续 Gate | T6 | 评测是否覆盖不了循环/条件/复杂控制流的结论 |
| 提交纪律 | 全部 | `git status --short`、`git diff --cached --name-only`、提交列表 |

## 完成 Gate 与剩余工作

- 本批完成 Gate：两份文档通过 `validate_document.py` 的 design/plan 校验；只提交一个 `docs:` commit；工作树无夹带。
- 产品实现不属于本批完成范围；T0–T6 是用户批准后的后续工作，不能以文档校验通过宣称 batch 已实现。
- Windows 本批只运行文档校验及必要的仓库基线检查；Linux ptysmoke、race、真实 provider 和跨语言评测待后续监督/实现批次执行。
