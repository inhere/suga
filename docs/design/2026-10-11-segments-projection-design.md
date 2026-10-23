<!-- template_id: design; template_version: 1.1.1 -->
# 会话分段与上下文投影（第二期） 设计

> 状态：Draft 0.3 / 已批准进入计划（2026-10-11 用户：“批准设计 0.2”，Q1 补充“再加个到 ctx 50% 以上才触发（这里应该要加定时吧 - 定时/延迟功能对后续的自我触发等很重要，要封装好）”，其他按推荐；0.3 按此补充定时组件）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-11 | Claude | 初稿 |
| 0.2 | 2026-10-11 | Claude | 按合并评审修订：段上下文与交接包写入段起始记录、沿用回合采纳规则，meta 只作缓存，旧 handoff 视为第 0 段（CB1）；段内更新改为 user 角色并持久化回放，生效规则以段记录 + 已采纳更新为准，hook 系统上下文改为 user 角色，删除 promptStale/memoryPromptHook（CB2）；用量改为每次模型调用后输出累计，ACP 区分本 run 与会话累计（CB3）；Runner 按会话单飞，段记录为唯一提交点，覆盖 `:compact` 与 Stop hook（CB4）；稳定前缀只含身份与工作约定，skills 列表移入段上下文；W0 先做前缀缓存探针、W1 以其结果为门槛；Claude 断点改放在段上下文末尾与最后一条消息；交接包带最近 N 条用户请求 |
| 0.3 | 2026-10-11 | Claude | 按用户批准时的补充：新增通用进程内定时组件 `pkg/agentcore/schedule`（延迟、周期、空闲触发，可取消、可注入时钟、有事件），空闲整理基于它实现，为后续自我触发（people 第二循环、提醒）复用；Q1–Q5 记为已决 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 背景与目标

思考模式 RIGOROUS；核心目标：落实第一期总纲的“投影只在断点重算，断点之间只追加”——让缓存前缀尽量跨会话稳定、把有损压缩改为面向当前任务的“分段交接”、在 chat 空闲时主动整理，并补齐量化这些改动所需的观测。scope freeze：W0–W4（见“范围”）；review budget：一轮合并轴评审（低暴露度，SR1409，已完成）；停止条件：用户批准。

来源：

- 第一期总纲（[状态、记忆与可扩展核心设计](2026-10-10-state-memory-extensibility-design.md)）。
- suag-sg9i：同模型对比中 suag 发送的 prompt 总量只有 omp 的 1/3，但小任务未命中缓存的输入是 omp 的 3–4 倍；63b3787 调整系统提示词内部顺序只降约 5%。
- suag-kxlq：用户提出“空闲一段时间后自动整理上下文”，并决定“chat 进程内计时，不依赖外部”。
- gofer 会话（2026-10-11）：gofer 已读取 suag job 用量（取最后一次出现的 usage），请 suag 让用量在运行中途可见、确认 ACP 用量结构，并约定工具调用次数字段 `tool_calls`（gofer-z2c6 等待本期 W0）。

用户可见结果：

1. 新会话的第一次请求能复用其他会话缓存过的前缀（同模式、同工具集时）；评测中“首次请求未命中缓存的输入”明显下降（目标：小任务降到当前的一半以下）。前提由 W0 的探针先证实。
2. 上下文接近上限时，suag 开启新的一段：新段带着围绕当前任务整理的交接包（最近的用户请求原文、计划、涉及文件、可回读的 ref），旧段完整保留、可检索；trace 可见。
3. chat 空闲超过设定时间且上下文较大时，suag 在后台整理出新的一段；用户回来时直接继续。
4. gofer 在运行中途就能看到累计用量（`--max-tokens` 中途生效），suag-acp 的用量也能显示；评测能对比首次请求的缓存命中与多轮会话。

## 名词

- **稳定前缀**：每个请求开头、与工作区和会话无关的部分：系统提示词（身份、工作约定）与直接工具的定义。工具定义随读写模式与 MCP 配置变化，因此跨会话复用只在“同模式、同工具集”时成立。
- **段（segment）**：一个会话里两个断点之间的投影，以一条段起始记录开头。
- **段起始记录**：存放该段冻结内容的 entry（扩展现有 compaction 记录）：段上下文快照（环境、项目规则、记忆索引、skills 列表、附加提示、延迟工具列表）、交接包、原因。沿用 compaction 的采纳规则；session meta 只作缓存，可由它重建。
- **段上下文消息**：由段起始记录渲染出的一条 user 角色消息，在每次组装上下文时生成，不是历史条目，不参与切点与压缩。
- **交接包**：新段开头带入的、围绕当前任务整理的内容：摘要、最近 N 条用户请求原文、当前计划、涉及文件、相关 ref。
- **上下文更新**：段内规则或附加提示变化时追加的 user 角色消息，作为 `context_update` 条目持久化并回放。
- **断点**：允许重建投影的时刻：会话开始、达到阈值、手动 `:compact`、空闲整理、跨会话 `--from`。
- **会话单飞**：Runner 对同一会话的 Run、Compact、空闲整理同一时刻只允许一个在执行。
- **定时组件**：进程内的通用定时与延迟触发能力（一次性延迟、周期、空闲触发），任务带名字、可取消与重置、可注入时钟、产生事件；空闲整理是它的第一个使用者，后续的自我触发复用它。

## 范围与非目标

范围（仓库 `inhere-tools/suag`）：

- **W0 观测、探针与评测**：前缀缓存探针（证实网关缓存“system + tools”前缀）；每次模型调用后输出累计用量（stream-json 新增 `type:"usage"` 行，`turn_end`/`result` 同名字段，含 `cache_read_tokens`、`cache_write_tokens`、`tool_calls`）；ACP 中途发 `usage_update`，`_meta` 区分本 run 与会话累计；评测支持多轮任务与“首次请求未命中缓存的输入”指标（含 omp 一侧）。
- **W1 稳定前缀**（以 W0 探针结果为门槛）：系统提示词只保留身份与工作约定；环境、规则、记忆索引、skills 列表、附加提示、延迟工具列表移入段上下文；段内更新改为持久化的 user 角色上下文更新；hook 系统上下文改为 user 角色；Claude provider 的 `system` 改为 block 数组并在段上下文末尾与最后一条消息加 `cache_control`（先验证网关接受）。
- **W2 分段交接**：阈值与手动 `:compact` 改为开启新段：段起始记录、面向当前任务的交接包、旧 handoff 视为第 0 段；trace 显示分段。
- **W3 定时组件与空闲整理**：新建 `pkg/agentcore/schedule`（见“定时组件”）；chat 用它做空闲计时；会话单飞；段起始记录为唯一提交点；状态行提示。
- **W4 文档**：README、配置样例、CHANGELOG。

非目标：只读子 agent；权限与信任档位；编辑工具稳健性与文件检查点；空闲时提炼记忆候选；people（suag-frgt）；tool_search 加载 MCP 工具导致的工具数组变化（低频，接受其缓存失效）；跨进程或由 gofer 触发的空闲整理。

## 已确认事实与规范

- 系统提示词由 `rulesPromptBuilder` 组装：base、`personal_rules_content`、`memory_index_content`、`personal_append_prompt`、延迟工具列表（`internal/personal/runner.go:766-792`）。规则或附加提示变化时 meta hash 在回合开始即前移（`runner.go:464-496`），并发一条一次性的 role=system 通知，与 hook 的 `SystemContext` 合成同一条消息（`runner.go:416`、`context.go:209-211`），不落库。
- `context_update` 条目目前“只留审计、从不回放”（`store/types.go:31-32`），生产代码没有写入方。
- skills 列表优先收录工作区的 `.agents/skills` 与 `skills`（`internal/wiring/skills.go:37-43`）；base 提示词含读写模式分支。
- OpenAI 路径 system 为 messages 首条、工具在 `body["tools"]`；Claude 路径把 role=system 消息合并进顶层 `system`，前缀顺序为 tools → system → messages；两者都没有 `cache_control`（`pkg/agentcore/llm/openai.go`、`claude.go`）。
- 每个模型步骤都重新组装提示词、上下文并 `ListTools`（`pkg/agentcore/runtime.go:128-144`）。
- 压缩：每步构建上下文时超阈值即在回合中途压缩（`context.go:162-193`）；切点只在 user 消息前或完整调用/结果对之后，`items()` 依赖“第 i 条消息对应第 i 个 entry”（`compact.go:86-175`）；回合中途写的 compaction 只有所属回合 completed 才被采纳，回合之间写的立即采纳（`context.go:54-106`）；摘要返回后用 `context.WithoutCancel` 写 entry，失败路径会写 compaction error 事件（`compact.go:191-222`）；压缩后刷新记忆快照（`compact.go:232-236`）。
- `--from` 交接：handoff 是普通 ctxItem，靠下次压缩折进摘要（`handoff.go:35-45`、`context.go:86-95`）。
- 用量：`run_finished`/`result` 只在 run 结束时输出（`runner.go:736`、`internal/output/streamjson.go:23-24`、`internal/output/usage.go:9-12`）；ACP `usage_update` 只在回合后发、且为会话累计（`internal/acpserver/usage.go:17-27`）。
- chat：回合同步执行（`chat.go:259-289`）；`:compact` 同步调用 `runner.Compact`（`chat.go:221-228`）；等待输入在 `nextChatInput` 中 select 键盘、投递、Stop hook 与 ctx（`chat.go:360-428`）；chat 终身持会话锁（`runner.go:204-236`）。
- `suag trace --json` 的 model 事件含每次调用的 input、cache_read、cache_write 与 attempt；input 已含缓存部分。
- 同模型对比数据（2026-10-11，`tmp/eval/cmp-suag2`、`tmp/eval/cmp-omp`）：suag 8 任务 prompt 148k / 未命中 37k，omp 501k / 61k；小任务未命中 suag 3.5–4.8k、omp 0.8–2k。
- 适用规范：SR1107、SR1403、SR1405、SR1409、SR1402.1、GR104、GR106。

## 总体方案

请求的形状从“系统提示词里什么都有”改为三层：

```text
[工具定义 + 系统提示词：稳定前缀]  身份、工作约定；直接工具（同模式同配置时跨会话相同）
[段上下文消息]                     由段起始记录渲染：环境、规则、记忆索引、skills 列表、附加提示、
                                   延迟工具列表、交接包；段内不变
[段内历史]                         只追加；规则/附加提示变化 → 追加一条上下文更新（持久化、回放）
```

断点时重算段上下文并写一条段起始记录；断点之间只追加。所有“这一段看到了什么”都在 entry 里，回合失败或取消不会造成 meta 与历史错位；meta 只缓存当前段的渲染结果。

W0 先用探针确认 OpenAI 兼容网关（opencode-go → DeepSeek）对“system + tools”前缀做了缓存：同模式、两个不同工作区各跑一次，比较第二次首个调用的 `cache_read`。若不成立，W1 只做 Claude 断点与段内更新的持久化，不移动内容，并在计划中如实记录。

## 架构

| 组件 | 改动 |
|---|---|
| `internal/wiring/prompt.go` | `basePrompt` 只保留身份与工作约定；环境与 skills 列表经新注入点（如 `Config.EnvironmentSource`、`Config.SkillsSource`）交给 personal |
| `internal/personal`（新 `segment.go`） | 段上下文快照的生成；段起始记录（扩展 compaction 记录：`reason`、`context`、`handoff`、`recent_requests`、`refs`）；分段入口供阈值、`:compact`、空闲整理共用；会话单飞 |
| `internal/personal/context.go` | `loadHistory` 以最新已采纳的段起始记录（或旧 handoff，视为第 0 段）确定段上下文与交接包；回放已采纳的 `context_update`；`compose` 生成段上下文消息（不是 ctxItem） |
| `internal/personal/runner.go` | `rulesPromptBuilder` 只输出稳定前缀；规则/附加提示变化写 `context_update`（随回合采纳）；生效 hash = 段记录 hash，再被已采纳的最新更新覆盖；hook `SystemContext` 与 lostReply 改为 user 角色；删除 `promptStale`/`memoryPromptHook` 及与 `contextBuilder.meta` 的共享 |
| `internal/personal/compact.go` | 摘要输入跳过 `context_update`，`previous` 取当前段交接包；模板面向当前任务 |
| `internal/personal/store/types.go` | `context_update` 语义改为“回放”，更新注释 |
| `pkg/agentcore/llm/claude.go` | `system` 改为 block 数组；在段上下文消息末尾与最后一条消息加 `cache_control`（W0 验证网关接受后启用） |
| `internal/output`、`internal/acpserver` | 每次模型调用后的累计用量输出；ACP 中途 `usage_update`，`_meta` 区分 run / session |
| `pkg/agentcore/schedule`（新） | 通用进程内定时组件，只依赖标准库 |
| `internal/chat` | 用定时组件注册空闲任务、后台分段、取消、状态行；`:compact` 与回合开始先取消再等待 |
| `scripts/eval` | 多轮任务（`prompts` 列表）；首次请求未命中指标（suag 取 trace 首个成功的主模型调用，omp 取首个 message_end） |

agentcore 内核不变：段上下文消息、上下文更新都是宿主 `ContextBuilder` 产生的普通消息。

## 关键流程

**段开始（会话开始 / 阈值 / `:compact` / 空闲整理 / `--from`）**

```text
获取会话单飞 → 生成段上下文快照（环境、规则重读、记忆索引重取、skills 列表、附加提示、延迟工具列表）
→ [非会话开始] 生成交接包：summarize(previous = 当前段交接包, 输入跳过 context_update,
   查询 = 最近 N 条用户请求)
→ 写段起始记录（唯一提交点；回合中途写的随回合采纳，回合之间写的立即采纳）
→ 更新 meta 缓存 → 之后每一步：稳定前缀 + 段上下文消息 + 段内历史 + 本回合
```

会话开始写的段起始记录不带交接包；旧会话没有段记录时，以 meta 中的规则/记忆快照渲染（兼容），下一断点起写段记录；旧 `handoff` 条目视为第 0 段的交接包。

**交接包模板**：

```text
## 当前任务（最近的用户请求原文附在交接包里，不必复述）
## 目标与约束
## 进展（已完成 / 进行中 / 阻塞）
## 当前计划
## 关键决策
## 涉及文件
## 与当前任务相关的引用（ref + 一句话说明，可用 recall 读取）
## 下一步
```

交接包 = 摘要 + 最近 N 条用户请求原文（默认 3，跳过 Stop hook 注入与“继续”类短句时向前补足）+ 当前计划；段内保留的原始尾部为最后一组完整的工具调用/结果。

**小节预算与折叠去重（2026-10-23）**：实际模板的固定小节为「当前任务 / 坐标 / 采纳与否决 / 验证到哪 / 进展与阻塞 / 下一步 / 与当前任务相关的引用」，除「采纳与否决」外每节在模板里写明字节预算（1 / 2 / 1.5 / 2 / 1 / 1 KB），超出的节只留最新的行并附一行省略说明；「采纳与否决」（硬约束、规则、用户原话）不设上限、不得改写，长会话评测逐字检查的正是这些内容。换段摘要调用的输出上限按预算总和 ×1.5 换算 token，被截断时重问一次。折叠上一份包时按坐标去重：同一文件同一符号只留最新，新素材（待压缩对话）里已含的旧坐标不再搬运。目的是止住“每次换段把上一份包整篇带进来”导致的无界增长（评测实测 4.7 → 7.8 → 16.0 KB，见 `docs/research/2026-10-22-longsession-cost-analysis.md` 第五节第 2 条）。

**段内规则/附加提示变化**：回合开始比较“生效 hash”（段记录 hash，被已采纳的最新 `context_update` 覆盖）与当前规则 hash；不同则本回合写一条 `context_update`（user 角色，全文），随回合采纳、之后回放，不进 `state.Messages`；不改系统提示词与段上下文。回合失败则下回合重新判定，不会丢也不会重复。

**空闲整理（chat）**

```text
回合结束 → 等待输入，启动计时器（context.idle_after，默认 15m，0 关闭）
到点 且 上下文估计 ≥ context.idle_min_ratio × 阈值（默认 0.65）
  → 获取会话单飞（取不到则放弃本次）→ 状态行“✦ 空闲整理中…” → 分段入口（compact_model）
  → 提交（写段起始记录）前被取消：记 compaction 事件 status=cancelled，不写记录与 meta
  → 提交后：视为成功，状态行“✦ 已整理上下文：X → Y token”
键盘输入、投递、Stop hook 产生回合、`:compact`：先取消再等待单飞释放，然后照常执行
```

**定时组件（`pkg/agentcore/schedule`）**

```go
type Scheduler interface {
	After(name string, d time.Duration, fn Func) Handle              // 一次性延迟
	Every(name string, d time.Duration, fn Func) Handle              // 周期
	Idle(name string, d time.Duration, fn Func) Handle               // 空闲触发：Touch 重置，到点触发一次后等待下一次空闲
	Touch()                                                           // 有活动：重置所有 Idle 任务的计时
	Close() error                                                     // 取消全部任务并等待运行中的结束
}
type Func func(ctx context.Context) error                           // ctx 在 Cancel/Close 时取消
type Handle interface{ Cancel(); Reset() }
```

- 同名任务重复注册时替换旧任务；同一任务不会并发执行（上一次没结束则跳过本次并记事件）。
- 选项：注入时钟（测试用假时钟，不睡眠）、`Observe` 事件回调（scheduled / fired / skipped / cancelled / done / failed，带任务名与耗时），宿主把事件写进 events 表，trace 可见。
- 只在进程内，不持久化；需要跨进程或持久的计划（如 people 的长期提醒）时再加可选的持久层，不在本期。
- chat 的空闲整理：`Idle("idle-consolidate", context.idle_after, fn)`，每次输入、回合开始、`:compact` 调用 `Touch()`；fn 内先检查上下文比例（≥ `context.idle_min_ratio`，默认 0.65）再进入分段入口。

**用量（W0）**：每次模型调用结束（AfterModel）输出一行累计用量（本次 run 至此，含缓存与 `tool_calls`）；`turn_end` 与 `result` 带同名字段，`result` 最后覆盖。ACP 在同一时机发 `usage_update`，`_meta` 中给出 `run` 与 `session` 两组累计，并与 gofer 约定预算取 `run`。

## 安全、数据、运维与回滚

- 段上下文里的规则与记忆索引内容与现在相同，只是位置变化；污染标记与记忆写入关口不受影响。
- 段起始记录是唯一提交点，与回合采纳规则一致；meta 只作缓存，可由记录重建，避免“meta 已前移、记录未采纳”的错位。
- 段起始记录在 compaction 记录上增加字段；旧记录缺字段时按旧规则加载；`context_update` 开始回放只影响新写入的条目（现有库没有此类条目）。回滚即恢复旧加载逻辑。
- 空闲整理只在 chat 持锁进程内运行，经会话单飞与 Run、`:compact` 互斥。
- `cache_control` 只影响计费与延迟；网关拒绝时保持不发。

## 决策

| ID | 决策 | 理由 |
|---|---|---|
| D1 | 稳定前缀只含身份、工作约定与直接工具；其余进段上下文 | 跨会话复用前缀；Codex 把环境与 AGENTS.md 放进首条 user 消息是同一思路；skills 列表含工作区 skills，不稳定 |
| D2 | 段上下文与交接包写入段起始记录，沿用 compaction 采纳规则，meta 只作缓存 | 回合失败/取消不错位（评审 CB1） |
| D3 | 段内更新以 `context_update` 持久化并回放，生效 hash 由记录推导 | 不丢不重复，断点之间只追加（评审 CB2） |
| D4 | 交接包以最近 N 条用户请求原文 + 计划 + 相关 ref 为核心，旧 handoff 视为第 0 段 | 面向当前任务；`--from` 内容在后续分段中不丢 |
| D5 | Runner 会话单飞，段起始记录为唯一提交点 | 空闲整理、`:compact`、回合互斥；取消语义明确（评审 CB4） |
| D6 | 空闲整理只在 chat 进程内计时，默认 15 分钟、上下文达阈值 50% | 用户决定；空闲超过缓存有效期后前缀已冷，整理几乎不额外损失缓存 |
| D7 | W1 以 W0 前缀缓存探针为门槛；Claude 断点放在段上下文末尾与最后一条消息 | 先证实收益再搬内容；Claude 前缀顺序为 tools → system → messages |
| D8 | 每次模型调用后输出累计用量，ACP 区分 run 与 session | 预算中途生效（评审 CB3）；与 gofer 约定字段 |
| D9 | 定时能力封装为通用组件 `pkg/agentcore/schedule`，空闲整理是第一个使用者 | 用户要求“定时/延迟功能对后续的自我触发等很重要，要封装好”；people 的第二循环、提醒都需要 |

## 待确认事项

| ID | 问题 | 结论 |
|---|---|---|
| Q1 | 空闲阈值默认值 | 已决：15 分钟（`context.idle_after`，`0` 关闭），且上下文达阈值 65% 以上（`context.idle_min_ratio`；2026-10-15 由 0.5 调整为 0.65）才触发；定时能力封装为通用组件（D9） |
| Q2 | 新段保留多少原始内容 | 已决（按推荐）：只保留最后一组完整的工具调用/结果；其余靠交接包与 ref |
| Q3 | 段内规则变化时追加全文还是提示 | 已决（按推荐）：追加全文，下一段并入段上下文 |
| Q4 | `:compact` 的名字 | 已决（按推荐）：保留 `:compact`，行为改为开启新段 |
| Q5 | 交接包带几条最近的用户请求 | 已决（按推荐）：3 条（跳过 Stop hook 注入与“继续”类短句） |

## 结论与人工计划 Gate

本设计确立第二期范围（W0–W4），合并评审的 4 条阻塞问题已在 0.2 处理，0.3 按用户批准时的补充加入定时组件。已批准进入计划；计划需要单独批准后才实施。
