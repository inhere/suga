<!-- template_id: design; template_version: 1.1.1 -->
# 内置（native）工具按需加载（延迟下发 schema）设计

> 状态：**Draft 0.1 / 待用户批准**（设计批准只授权进入规划，不授权实施）
> 日期：2026-10-23
> 关联：`docs/research/2026-10-23-fixed-prefix-tools-report.md`（结论 4、5、7 与第五、六节）、`docs/research/2026-10-22-longsession-cost-analysis.md`；前序设计 `docs/design/2026-10-08-core-capabilities-design.md`（C 工具分档与 `tool_search`）、`docs/design/2026-10-17-preferred-tools-design.md`（MCP pin 不进延迟集）、`docs/design/2026-10-11-segments-projection-design.md`（段快照与缓存稳定）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-23 | suga 子 agent | 初稿：把 native 工具纳入现有 `Visibility`/`tool_search` 延迟机制，回答选取、发现、加载时机（含前缀缓存账）、与 MCP 的统一、风险与验证六问 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 结论（先看）

1. **延迟的是「低频 native 工具」**，名单显式写在代码里（默认 9 个），**不用 schema 大小阈值**（`auto` 阈值留给 MCP）。默认名单：`agent`、`file_symbols`、`memory`、`recall`、`session_search`、`session_message`、`skill`、`suga_manifest`、`web_fetch`；高频工具（`file_read`/`file_edit`/`file_write`/`file_remove`/`file_grep`/`file_list`/`file_find`/`file_refs`/`shell_exec`）永不进默认名单。
2. **复用同一机制，不开第二套**：同一个 `Visibility` 层、同一个 `tool_search`、同一个 `loaded_tools` 会话元数据、同一个 `tool_search` trace 事件；只扩「哪些工具可以进延迟集」这一个谓词，并新增 `tools.deferred_native` 配置键。
3. **发现靠段上下文清单**（`segmentContext.DeferredTools`，每段冻结）+ `tool_search` 自身描述 + **直呼自动加载兜底**（现有行为：调用未加载的延迟工具会加载它并把 schema 还回来）。清单 9 行 ≈ 900 字符，与省下的 13,412 字符相比净省 ≈ 12.5k 字符 ≈ 3.2k token/次调用。
4. **加载不立即改工具表，只在段边界入表**（`tools.deferred_apply: segment` 为默认；`immediate` 保留现有 MCP 行为作兼容）。理由见 §5 的账：工具块位于 prompt 最前，任何中途改动都让整条前缀（工具 + 全部消息）作废一次，代价 ≈ 1.15 ×(前缀+历史)，常常吃掉整场会话的收益；而消息追加发生在尾部，本来就按未命中计费。
5. **本段内加载的工具立即可调用**：`Visibility.ExecuteTool` 早已放行「已加载但未在表里」的工具（`visibility.go:272-292`），`tool_search` 的结果文本给出紧凑参数清单，模型不必靠猜参数。
6. **已加载集合按会话持久**（session meta `loaded_tools`，`internal/personal/visibility.go:10-33`）：resume 后第一次请求的工具表就包含它们，换段时入表，不同加载顺序产生同一字节序列（表内顺序：直连工具按名排序，已加载工具按名追加在末尾，`visibility.go:247-267`）。
7. **子 agent 不参与**：它们走独立 registry + 白名单（6–8 个高频工具，`internal/wiring/subagent.go:53-56,204-219`），没有 `tool_search` 也没有 visibility 层，本次不改。
8. 8 个待批决定见下节（Q1–Q8），其中 Q1（默认开）与 Q4（入表时机）决定方案骨架。

## 决策（待批，优先看）

| # | 问题 | 建议 | 理由 |
|---|---|---|---|
| Q1 | 默认是否开启（默认名单生效） | **默认开**，`tools.deferred: never` 作总开关一键关闭 | 默认关等于没有收益；风险靠「清单 + 直呼自动加载 + 段边界入表」兜住，并用评测守通过率 |
| Q2 | 默认名单 | 9 个（见 §2.1），`task`/`update_plan` 放第二批（显式配置可加） | `task` 紧跟 `shell_exec` 的高频路径、`update_plan` 只 671 字符且是行为塑形工具，收益/风险比不如其余 9 个 |
| Q3 | 名单写死还是可配 | 代码内置默认名单 + `tools.deferred_native: auto \| never \| [名字…]` 覆盖；**只全局配置**（`SUGA_HOME/config.yaml`） | 现状所有 `tools.*` 都是全局单层配置；项目级配置层要另立设计，本期不做 |
| Q4 | 加载后何时进工具表 | **段边界**（`tools.deferred_apply: segment`），`immediate` 保留给 MCP 现状与回退 | 中途改工具块 = 整条前缀重写一次（§5 的账），常吃掉收益；段边界本来就因换段/压缩而重建缓存 |
| Q5 | 是否把同一「段边界入表」用于 MCP | **是**（统一一套语义）；若不想动 MCP 现状，可先只对 native 生效（`apply` 按来源分档） | 两套时机最难解释也最容易出 bug；MCP 的 schema 走 `tool_search` 文本一样可用 |
| Q6 | `memory`/`recall`/`session_search` 这对「低频但关键」的工具是否默认延迟 | **默认延迟**，但清单行明写用途（长会话找回前文/长期记忆/大输出展开），并保留直呼自动加载 | 三者合计 6,024 字符（21% 的工具块）；关键在于「不知道有它」而不是「加载慢」，清单就是为此 |
| Q7 | 观测是否加字段 | `context` 事件加 `deferred_native` 计数；`tool_search` 事件加 `newly_loaded_kinds`；manifest 标出被延迟的工具 | 现在只能从 `deferred_tools` 字符数间接判断，诊断「为什么模型没搜到」缺入口（报告「发现但不碰」已验证这点） |
| Q8 | 是否新增「需要低频工具」的评测任务 | **新增 1 个**（`recall`/`session_search` 类）+ 复用 4 个 longsession-hard 做 `--repeat 3` 对照 | 现有评测没有任何 run 覆盖延迟加载（`deferred_tools` 恒 0），不新增就等于没有回归网 |

## 背景与动机

- 固定前缀实测 28,240 字符工具 schema + 3,391 字符 system ≈ 8.1k token，占每次请求的 79%（报告结论 1）。工具块里 **description 占 50%**，且成本与使用频率严重错配：`memory`（3,134 字符）在 128 个评测 run、3,348 次调用里只用了 2 次，`skill`/`session_message`/`web_fetch`/`suga_manifest` 为 0 次。
- `deferred_tools` 恒为 0 **不是配置问题**：可见性层只把 MCP 工具放进延迟集（`pkg/agentcore/tools/visibility.go:156` 的 `IsMCP` 过滤），20 个 native 工具**永远**进不去，`tools.deferred: always` 也一样（报告结论 4、第五节的档 3）。
- 报告给出 11 个零用/低频工具合计 15,197 字符、清单 826 字符、净省 14,371 字符 ≈ 3,685 token 的估算，并明确这是「中高风险、需要设计」的一档（结论 7）。本设计就是把这一档做实：回答选取、发现、加载时机、与 MCP 的统一、风险与验证。
- 用户 2026-10-23 的任务要求：**只出设计**，实施等设计与计划批准。

## 范围

**做**：native 工具的延迟选取（默认名单 + 配置覆盖）、段上下文清单文案、`tool_search` 的描述与结果格式、入表时机与缓存策略、与 MCP 延迟的统一、观测字段、测试与评测方案。

**不做**（非目标）：
- 不改任何工具的 schema、实现或执行语义；不改审批、hook、`tools.disabled` 的按名匹配。
- 不改 MCP 的 `auto` 阈值（窗口 10%）；不做「按历史频率自动学习名单」；不做向量检索式工具搜索。
- 不做项目级配置层（`tools.*` 仍是全局单层）；不改子 agent 工具集；不改 `Hidden`（`view_image` 别名）机制。
- 不新增 `tool_search` 之外的新工具（例如 `tool_manifest`）。

## 已确认事实与约束

| 事实 | 位置 | 对设计的约束 |
|---|---|---|
| 工具表每个模型步重新取一次（`r.listTools` 在 run 循环内） | `pkg/agentcore/runtime.go:177,359-368` | 延迟集必须在 `Visibility` 层裁剪，不能在 runner 里缓存；本层是唯一的裁剪点 |
| 延迟分类只认 MCP，延迟集在构造时固定 | `pkg/agentcore/tools/visibility.go:142-178`（`:156` 的 `IsMCP`） | 扩分类谓词即可；保持「构造时固定」以免清单与工具表在段内漂移 |
| `ListTools` 顺序：直连工具（`Registry.Schemas` 按名排序、跳过 `Hidden`）+ 已加载延迟工具按名追加在末尾 | `pkg/agentcore/tools/registry.go:126-158`、`visibility.go:247-267` | 同一 loaded 集 ⇒ 同一字节序列；不同加载顺序不影响结果（缓存友好） |
| 未加载的延迟工具被直接调用时：加载它、返回带 schema 的工具错误 | `visibility.go:270-292`；测试 `internal/personal/visibility_test.go:151` | 「模型不知道有它」的兜底已存在，native 复用即可，无需新代码 |
| 已加载集合存 session meta（`loaded_tools`），resume/交接后仍在 | `internal/personal/visibility.go:10-33`、`internal/personal/store` 的 `PatchSessionMeta` | native 名字同样持久；不用新建存储 |
| 段上下文的 `DeferredTools` 在段快照里冻结，参与 `segmentContext.Hash` | `internal/personal/segment.go:42-59,96-130`、`internal/personal/visibility.go:62-68` | 清单必须逐段稳定（不能按已加载集合实时渲染）；清单变更只发生在换段 |
| system 提示是进程级静态文本，且**不重复工具清单**（「tool schemas already carry it」） | `internal/wiring/prompt.go:27-43` | 延迟后唯一的发现渠道是段上下文清单；顺带不能把清单塞进 basePrompt（会让所有会话前缀变化） |
| 只有 Claude 配了 `prompt_cache` 时在**消息**上加 `cache_control` 断点，工具块没有任何断点 | `pkg/agentcore/llm/claude.go:388-397,413`；`responses.go:138-140` 的 `prompt_cache_key` | 工具块是 prompt 的第一段：改它 = 后面所有消息字节位移 = **整条前缀重写一次**；追加到工具表末尾**不能**保住缓存 |
| 缓存命中/未命中已在 usage 与 trace 里可见 | `pkg/agentcore/api/types.go:101`、`internal/personal/usage.go:81,138` | 「加载一次值不值」可以事后用 `cache_read_tokens` 实测，不需要新埋点 |
| `tool_search` 只在延迟集非空时注册；名字是保留名 | `internal/wiring/capabilities.go:510-528`、`:157-159` | native 参与后「只有 native 被延迟」也会注册 `tool_search`（今天需要 MCP），装配测试要补这个用例 |
| `tool_search` 描述目前写的是「deferred MCP tools」；打分是名字/描述分词（3/2/1 权），`select:` 走精确名 | `pkg/agentcore/tools/native/tool_search.go:29-53`、`visibility.go:302-347` | 描述与「Not deferred tools」提示要改；`select:` 天然支持 native 名 |
| `tools.deferred` 是带 enum 校验的配置键；`tools.disabled` 是 string_list 且未知名字启动即报错 | `internal/config/settings_registry.go:168,210,467`、`internal/wiring/tools_disabled.go:18-61` | 新键要同时加描述符 + 校验 + `suga config` 读写；但延迟名单里的名字**不能**启动报错（只读模式/未启 skill/未启 agents 时工具本就不存在） |
| 子 agent 用独立 registry + 白名单，无 visibility、无 MCP、无 ExtraTools | `internal/wiring/subagent.go:53-56,204-219` | native 延迟天然不影响子 agent；本期不做，并在文档写明 |
| `sugasdk` 的 `ExtraTools` 是 native kind，恒为直连 | `pkg/sugasdk/types.go:45-51`、`internal/wiring/capabilities.go:127` | 宿主工具默认**不**进延迟集（只有名单里显式点名才延迟），避免宿主以为工具总在 |
| hooks 按工具名 glob、审批规则按工具名 `path.Match`、`tools.disabled` 按名注销 | `internal/hooks/hooks.go:156-171`、`pkg/agentcore/tools/rules/rules.go:175-188`、`internal/wiring/tools_disabled.go:18-47` | 延迟只影响「是否列出」，不影响「能否执行/审批/被 hook」；已有配置与规则不用迁移 |
| 观测：`tool_search` 事件（`query/hits/newly_loaded/cache_invalidated`）、`context` 事件（`tools`/`tools_count`/`tool_names`/`tools_mcp`/`deferred_tools`） | `internal/personal/visibility.go:38-60`、`internal/personal/context_event.go:37-64` | native 延迟直接可测；只需补 native 计数，不必新事件类型 |
| manifest 的 `deferred_tools` 只报字节数与 note | `internal/cli/manifest.go:212,242-243`、`internal/wiring/context.go:55-61,300-306` | 9 个 native 被延迟后 manifest 若不列出名字，诊断能力反而下降（见 Q7） |
| 评测任务没有「需要哪些工具」的声明，只有事后 `ToolCounts`；`--repeat` 已存在 | `scripts/eval/task.go:20-57`、`scripts/eval/main.go:84,135` | 只能用「通过率 + 调用数 + tool_counts」对比；要验证「模型找得到低频工具」必须新写一个任务 |

## 总体方案

### 1. 选取：哪些 native 工具默认延迟

判据是**调用频率**（报告结论 5 的实测：128 run / 3,348 次），不是 schema 大小；同时给「低频但关键」的工具保留一句用途说明而不是取消延迟。

| 默认延迟（9 个，13,412 字符） | 字符 | 实测调用 | 一句话用途（清单里给模型看） |
|---|---|---|---|
| `agent` | 1,636 | 4 | 交给子 agent 做只读探索/研究，省上下文 |
| `file_symbols` | 2,347 | 6 | 按文件列声明（函数/类型/方法）轮廓 |
| `memory` | 3,134 | 2 | 写/查长期记忆（跨会话） |
| `recall` | 1,031 | 4 | 展开工具输出里被省略的大段内容（`out:` ref） |
| `session_search` | 1,859 | 2 | 检索历史会话消息 |
| `session_message` | 1,243 | 0 | 给同机其它 chat 会话发消息 |
| `skill` | 752 | 0 | 按名字加载 skills 指令 |
| `suga_manifest` | 454 | 0 | 查看当前配置/工具/记忆/审批快照 |
| `web_fetch` | 956 | 0 | 抓取网页正文 |

**永不默认延迟**：`file_read`（841）、`file_edit`（473）、`file_write`（167）、`file_grep`（328）、`file_list`（204）、`file_find`（55）、`file_refs`（48）、`file_remove`（22）、`shell_exec`（1,158）——占全部调用的 99%，延迟它们等于每次使用多一轮（报告第五节「为什么档 3 不该做全部延迟」）。

**第二批（不默认，可配置加入）**：`task`（1,114，紧跟 `shell_exec` 的后台轮询路径）、`update_plan`（671，行为塑形且极便宜）。11 个全延迟 = 15,197 字符，清单 ~900，净 ≈ 14.3k（与报告 14,371 一致）。

**不参与延迟**（即使写进配置也只告警不生效）：`tool_search` 自身（否则无处可搜）、`Hidden` 工具（`view_image` 别名，本来就不进工具表）、`sugasdk ExtraTools`（宿主所有，除非显式点名）。

**配置（Q3）**：

```yaml
tools:
  deferred: auto        # 现有键：MCP 判据（auto/always/never）；never = 连 native 一起关掉
  deferred_native: auto # 新增：auto（内置名单）| never | [名字…]（覆盖内置名单，[] 等于 never）
```

- `auto` = 内置 9 个名单；名单里出现当前模式未注册的工具（只读模式、未启 skill/agents）→ 忽略，`slog.Debug` 记一条，**不报错**；出现 `tool_search`/`Hidden`/`ExtraTools` → 忽略 + `slog.Warn`。
- 名单只影响「哪些工具可进延迟集」，最终是否延迟还要过 `tools.deferred`：`never` 一律不延迟。
- 配置只读进程级（`SUGA_HOME/config.yaml` + private 目录覆盖，`internal/config/load.go:52-59`），与其它 `tools.*` 一致；项目级覆盖要另立设计。

### 2. 发现：模型怎么知道有这些工具

三条路径，按可靠性排序：

1. **段上下文清单**（主渠道）：沿用 `Visibility.PromptSection()`，改成按来源分组（`native` 与各 MCP server），每工具一行 `- name — 用途（≤60 字符）`。清单随段快照冻结（`segmentContext.DeferredTools` 参与段 Hash），因此**段内逐字节稳定**，不影响前缀缓存。成本：9 行 ≈ 900 字符（报告按 11 行实测 826）；实现加一条单测守卫「清单 ≤ 1,200 字符」。
2. **`tool_search` 描述**：把「deferred MCP tools」改成「deferred tools（MCP servers 与内置工具）」，并说明 `select:` 可一次列多个、加载后本会话一直可用。
3. **直呼自动加载**（兜底）：模型若从别处（system 提示、记忆、用户要求）知道工具名，直接调用即可——`ExecuteTool` 会加载它并回一段带 schema 的提示（`visibility.go:283-291`），下一步照调。代价是一轮，但不会「找不到就放弃」。

清单文案（草案，实施时按实测字符数微调）：

```
## 延迟加载的工具
这些工具的 schema 默认不下发。需要时用 tool_search 加载（query 用关键字，或 select:name1,name2 一次选多个，
加载后本会话可一直直接调用；直接按名调用也会自动加载）。
- agent — 交给子 agent 做只读探索/研究，省上下文
- file_symbols — 按文件列声明（函数/类型/方法）轮廓
...
```

### 3. 加载：何时进工具表、对前缀缓存的影响

**缓存事实**（§已确认事实第 8 行）：prompt = 工具块 + 消息；工具块在最前。provider 的前缀缓存按字节前缀匹配，`claude.go` 的断点打在消息上，**工具块没有任何断点**。所以：

- 把新工具**追加在工具表末尾**（`ListTools` 已这么做）**并不能**保住前缀——追加点之后的所有字节（含全部消息）都位移了，缓存照样全失效。顺序的价值只在于「同一 loaded 集产生同一字节序列」，与缓存无关。
- 唯一有效的杠杆是**改动次数**，不是改动大小：一次改动的代价 ≈ 1.15 ×(前缀 + 历史) 的 token 单价（未命中写 1.25× vs 命中读 0.1×）。
- 账（按 3.9 字符/token、历史 20k token、前缀 ~8.1k → 延迟后 ~4.7k）：每次调用省 ≈ 3,210 token × 0.1 ≈ 320 单价；一次中途改动 ≈ 1.15 × 24.7k ≈ 28k 单价 ⇒ 要 ≈ 90 次调用才回本。**在一个 84 次调用的会话里中途加载一次，收益基本被吃掉**；而「零用工具从头到尾没被加载」是最常见的情形，那是不打折的净收益。这解释了为什么不能简单照抄 MCP 的「立即入表」。

**方案（Q4/Q5）**：

| 时机 | 行为 |
|---|---|
| `tool_search` 命中 / 直呼未加载工具 | 只把名字写入 loaded 集（session meta）；**工具表不变**；`tool_search` 结果文本给出紧凑参数清单（`name — 描述；参数：a(integer), b(string), …`，超过 10 个参数截断），模型可立刻按正确参数调用 |
| 已加载工具的执行 | `ExecuteTool` 已放行（`visibility.go:279-282`），执行、审批、hook、trace 与直连工具完全一致 |
| 换段（压缩/交接/空闲整理） | `freshSnapshot` 重算段快照时，把 loaded 集里属于延迟集的工具**并入工具表**（`ListTools` 在段边界之后多带它们）；此时前缀本来就要重建，不额外损失 |
| resume / 交接 | 构造 `Visibility` 时从 session meta 读 `loaded_tools`，第一次请求的工具表即包含它们（顺序按名追加，稳定） |
| `cache_invalidated` 语义 | 改为「本段内工具表是否变化」：段边界入表时为 false（缓存本来就重建），同一段内重复加载也为 false |

新增配置 `tools.deferred_apply: segment | immediate`（默认 `segment`；`immediate` = 现有 MCP 行为，用于回退与对照评测）。MCP 与 native 共用这一个值（Q5）；若评审要求 MCP 零变化，可把 `immediate` 设为 MCP 的固定行为，只对 native 应用 `segment`。

**为什么不用「加载即入表 + 只加载一次」**：模型在第一轮往往不知道要什么工具，`tool_search` 会被调用 2–3 次，每次都是一次整前缀重写；而且 `memory`/`session_search` 这类工具常常在会话中段才被想起。

### 4. 与 MCP 延迟加载的统一

| 维度 | 统一后 |
|---|---|
| 机制 | 一个 `Visibility` 层；一套 `Search`/`PromptSection`/`loaded_tools`/`tool_search` 工具 |
| 选取判据 | MCP 仍按 `tools.deferred` 的 `auto` 阈值（窗口 10%）与 preferred pin；native 按 `tools.deferred_native` 名单。**两者互不影响** |
| 入表时机 | 同一个 `tools.deferred_apply`（默认 `segment`） |
| 发现 | 同一个段上下文清单，按来源分组（`native` / 各 MCP server） |
| 观测 | 同一个 `tool_search` 事件（补 native/mcp 拆分）；`context` 事件补 `deferred_native` |
| 阈值沿用 | **native 不沿用 `auto` 大小阈值**：最贵的 native 工具（`memory` 3,134）恰是行为关键工具，而便宜的 `file_write`（548）高频；MCP 的阈值不动 |

### 5. 装配与影响面

- **装配点唯一**：`internal/wiring/capabilities.go:510-528` 的 `newToolVisibility`（chat/run/ACP/sugasdk 都经 `Wiring.Open`）。新增：读 `tools.deferred_native`/`tools.deferred_apply`，把名单传给 `VisibilityOptions.NativeTools`，`Active()` 为真即注册 `tool_search`（今天只有 MCP 时也如此）。
- **配置**：`internal/config` 加两个描述符（`tools.deferred_native` string_list（或 enum `auto`/`never`）、`tools.deferred_apply` enum）+ 读取/校验 + `suga config` 读写 + 文档（`docs/usage/llm-config-v0.md`、`skills/suga-guide/references/config.md`）。
- **子 agent**：不参与（独立 registry + 白名单，无 `tool_search`）。
- **ACP**：走同一装配；权限按**执行时**的工具名判定（`internal/acpserver/policy.go:200,467`），延迟不改变审批行为；`internal/acpserver/policy_table_test.go` 与 `internal/wiring/approval_table_test.go` 的「工具全集」断言需按新默认更新（默认开 ⇒ 这两个测试会看到更少的直连工具）。
- **`sugasdk ExtraTools`**：默认不进延迟集；显式点名时按普通 native 处理（`capabilities.go:157-182` 的保留名检查不变）。
- **hooks/审批/`tools.disabled`**：按名匹配不变；延迟的工具仍可被 hook、仍按规则审批、仍可被 `tools.disabled` 注销（注销优先于延迟）。
- **manifest / chat 提示**：`suga_manifest` 的 `deferred_tools` 段列出被延迟的工具名（分 native/MCP）与已加载集合，避免「模型没搜到」时无处排查；chat 启动提示加一句「N 个工具按需加载」。
- **trace**：`context` 事件加 `deferred_native`（数量）与 `deferred_loaded`（本会话已加载数量）；`tool_search` 事件加 `newly_loaded_kinds`（`{"native":1}` 这类）。`suga inspect trace --json` 不用改结构。

## 架构

```
            ┌────────────────── 段快照（换段时冻结）──────────────┐
config.yaml │ segmentContext.DeferredTools = Visibility.PromptSection()
 tools.     │   → 段上下文消息里的一节「延迟加载的工具」
  deferred  │
  deferred_ └──────────────────────────────────────────────────────┘
  native           │                                          ▲
  deferred_apply   ▼                                          │
Wiring.Open → newToolVisibility ──► tools.Visibility ──► ListTools()（每模型步）
                     │                 │  direct: Registry.Schemas()（按名排序，跳过 Hidden）
                     │                 │  deferred: 仅「已入表」的 loaded（段边界入表）
                     │                 ├──► Search(query,limit) ──► tool_search 结果（含参数清单）
                     │                 └──► ExecuteTool：直连/已加载 → 内层（审批→base）；未加载 → 加载+回 schema
                     │                        │
                     │                        └── loaded_tools（session meta）+ tool_search 事件
                     └── tools.deferred 的 MCP 判定（不变）
```

新增/修改的代码落点（实施阶段）：

| 位置 | 改动 |
|---|---|
| `pkg/agentcore/tools/visibility.go` | `VisibilityOptions.NativeTools/Apply`；分类谓词从 `IsMCP` 扩成「MCP 规则 ∨ 名单内 native（非 Hidden、非 tool_search）」；`PromptSection` 按来源分组；`tableLoaded`（已入表集合）与 `loaded`（已加载集合）分离；`Search` 结果补参数摘要 |
| `pkg/agentcore/tools/native/tool_search.go` | 描述与结果文案（「deferred tools」、参数清单、「本会话可用」） |
| `internal/wiring/capabilities.go` | 读两个新键、传参与注册条件 |
| `internal/config/settings_registry.go` + readers | 两个新键的描述符与校验 |
| `internal/personal/visibility.go`、`segment.go` | 清单来源不变；`deferred_apply=segment` 时的「段边界入表」钩子（段开始时通知 visibility 折叠 loaded→table） |
| `internal/personal/context_event.go` | `deferred_native`/`deferred_loaded` 计数 |
| `internal/cli/manifest.go`、`internal/wiring/context.go` | manifest 列出延迟工具名 |

## 关键流程

1. **段开始**：`freshSnapshot` 调 `Visibility.PromptSection()` 写入 `segmentContext.DeferredTools`（含 native 分组），并让 visibility 把 loaded 集并入工具表（`segment` 模式）。
2. **第一次请求**：工具块 = 直连工具（9–15 个）+ 已入表的延迟工具；段上下文消息带清单。模型看到清单里的名字与用途。
3. **需要低频工具**：模型调 `tool_search{query:"记忆"}` 或 `select:memory`；命中后加载写入 session meta，结果文本返回「已加载 + 参数清单」，`tool_search` 事件记 `newly_loaded`、`cache_invalidated=false`（`segment` 模式）。
4. **下一次调用**：模型直接用正确参数调 `memory add`；`ExecuteTool` 走内层（审批 → 执行），trace 与直连工具一致。
5. **本段内再需要另一个低频工具**：同上，工具表仍不变（缓存友好）。
6. **换段/压缩/交接**：新段快照重算清单；loaded 集并入工具表；前缀本来重建，无额外损失。
7. **resume**：从 session meta 读 `loaded_tools`，第一请求即带这些工具（字节序列与上次相同）。
8. **回退**：`tools.deferred: never` 或 `tools.deferred_apply: immediate`，无需改代码。

## 决策记录

| # | 决策 | 备选与被否理由 |
|---|---|---|
| D1 | 名单显式（代码内置 + 配置覆盖），不用大小阈值 | 大小阈值对 native 是反的：`memory` 最贵却最关键，`file_write` 便宜却高频 |
| D2 | 复用 `Visibility` 与 `tool_search`，不新开机制 | 第二套机制要同步两份发现文案、两份加载状态、两份 trace，长期一定漂移 |
| D3 | 默认开、`tools.deferred: never` 一键关 | 默认关 = 0 收益；灰度靠评测与配置，不靠默认值 |
| D4 | 段边界入表（`segment`）为默认 | 立即入表（MCP 现状）每次加载让整条前缀重写，账上常吃掉收益（§3）；「只加载一次」与模型的真实行为不符 |
| D5 | 清单放段上下文，不放 basePrompt | basePrompt 是进程级静态文本，加清单会让所有会话（含不用延迟的）前缀变化，且它明确声明「不重复工具清单」 |
| D6 | 配置只全局一层 | 现状 `tools.*` 全为全局；项目级配置层是另一个设计（`2026-10-09-config-layering-chat-design.md` 的范围外） |
| D7 | 未知名字只告警不报错 | 只读模式、未启 skill/agents 时工具不存在属正常；`tools.disabled` 的「未知即报错」语义不能被照抄 |

## 风险与缓解

| 风险 | 级别 | 最可能受影响的场景 | 缓解 |
|---|---|---|---|
| 模型不知道有某工具，行为退化 | 中 | ①长会话中「找回前文」不用 `session_search`/`recall`；②该写记忆时没写 `memory`；③`skill` 被忽略导致没读 skill 指令；④`shell_exec` 后台返回后不用 `task` 轮询 | 清单每行写用途；`tool_search` 描述点名「内置工具」；直呼自动加载兜底；新增「需要低频工具」的评测任务；评测 `--repeat 3` 守通过率 |
| 中途加载导致前缀缓存掉档 | 中（已按账本规避） | 一次会话里多次 `tool_search` | 段边界入表 + 单次可批量加载（`select:` 多名字，上限 10）；用 `cache_read_tokens` 对比验证 |
| 清单本身变长、挤占窗口 | 低 | 清单 + 9 个工具都不可见时无额外成本；清单 ~900 字符 | 每行 ≤60 字符；单测守卫清单 ≤1,200 字符 |
| 检索质量（中文查询、`select:` 拼错） | 低 | 中文描述 → 分词按非字母数字切，中文整词进 token 集，关键字能命中；`select:` 拼错时给 `Not deferred tools: …` 提示 | 清单写明 `select:` 用法；`tool_search` 结果对未知名字给可用名字提示 |
| 子 agent / ACP / ExtraTools / hooks / 审批回归 | 低 | 装配层测试按「工具全集」断言，默认开会改变直连工具集 | 逐个更新 `internal/wiring`、`internal/acpserver`、`internal/personal` 的工具集断言；PTY 冒烟覆盖 chat 观测 |
| trace 诊断能力下降 | 低 | 模型反复 `tool_search` 找不到时无从排查 | Q7 的观测字段 + manifest 列出延迟工具名 |
| 模型直接调用未入表的工具（provider 可能校验工具名） | 低 | 现有 MCP 路径已验证可行（`TestDeferredToolDirectCallAutoLoads`）；OpenAI 兼容网关若校验，得到的是 provider 错误而非 suga 的提示 | 保持「`tool_search` 结果给参数清单」为主路径，直呼只是兜底；真机验证时专门试一次 |

## 验证

### 单元测试（`github.com/gookit/goutil/x/assert`，多用例用 `t.Run`）

1. `pkg/agentcore/tools/visibility_test.go`：混合注册表（native 直连 + native 名单 + MCP + `Hidden` + ExtraTool）→ 延迟集/直连集正确；`tools.deferred: never` 时两者都不延迟；名单含 `tool_search`/`Hidden`/未注册名 → 忽略且不 panic。
2. 清单：golden 文本（分组、顺序、字符上限 ≤1,200）；`segment` 与 `immediate` 两种 `Apply` 下清单一致（清单只依赖延迟集，不依赖已加载集）。
3. 入表时机：`Apply=segment` 时 `Search` 后 `ListTools` **不变**、`ExecuteTool` 可执行；模拟段边界后 `ListTools` 带上该工具且顺序稳定（两次不同加载顺序 ⇒ 同一字节序列）。
4. 持久化：`memStore` 绑定后 resume（新建 Visibility 绑同一 store）可见已加载工具；`Apply=immediate` 保留现有 `TestVisibilityBoundStore` 语义。
5. `tool_search`：`select:memory,recall`；结果文本含参数清单；`tool_search` 事件带 `cache_invalidated=false`（segment 模式）。
6. `internal/wiring`：`newToolVisibility` 对新配置键的取值矩阵（`auto`/`never`/名单/非法值/只读模式缺工具）；**只有 native 被延迟**时 `tool_search` 被注册。
7. **真实装配路径**（规则要求）：`internal/wiring` 里用 `Wiring.Open`（或 `chat.Run`）构建运行时，断言 `Registry.ListTools`（observe→visibility→approval→base 全栈）输出不含延迟工具、`tool_search` 存在且可执行；不能只测裸 `Visibility`。

### 伪终端冒烟（Linux 容器，`scripts/ptysmoke/`）

新增 `chat_deferred_native.py`：隔离 `SUGA_HOME`（脚本开头 `shutil.rmtree` 再建）+ `fakellm.py --mode tool` 依次返回 `tool_search{select:memory}` → `memory{action:add,...}` → 文本；断言 `suga inspect trace <sid> --json`（参考 `chat_observe.py`）：段上下文含 9 个名字、`context` 事件 `deferred_tools>0` 与 `deferred_native=9`、`tool_search` 事件 `newly_loaded=["memory"]`、`cache_invalidated=false`；第二个 prompt 里工具表（`context` 事件 `tool_names`）已含 `memory`（段边界入表或 resume 路径）。加入 `make smoke-chat`，并做一次反向验证（临时去掉 native 分类谓词，确认检查失败）。Windows 主机跑不了，交付说明要写明「待监督在 Linux 容器验证」。

### 长会话评测

- 4 个 longsession-hard 任务 `--repeat 3` 前后对比：**通过率不得下降**；`calls` 上升 ≤5%；`prompt`/`uncached` 必须下降（工具块 28.2k → ~14.8k 字符）。
- **新增 1 个「需要低频工具」的任务**（Q8）：任务要求在上一会话/上一段的大输出里找回内容（迫使 `recall`/`session_search`）或按要求写记忆（`memory`），断言检查项通过且 `tool_counts` 里出现该工具——这是「清单够不够让模型想到它」的唯一硬证据。
- 反向对照：同一任务在 `tools.deferred: never` 下跑一遍，确认 `deferred_tools=0` 且行为回到现状（用于区分「延迟导致退化」与「模型本来如此」）。
- 真机一轮（AGENTS 要求）：用一个真实 provider 跑一次需要 `memory` 与 `session_search` 的会话，确认模型能按清单加载而不是连续猜错；做不到就在交付说明里写明。

### 度量（实施后立刻可查）

`context` 事件：`tools` 14.8k 字符左右、`tools_count` 11（写模式默认配置）、`deferred_tools` ≈900、`deferred_native` = 9；`tool_search` 事件数与 `cache_invalidated` 次数（segment 模式应≈0 中途失效）；usage 的 `cache_read_tokens` 命中率不低于改造前。

## 待确认事项

1. Q1 默认开，还是先灰度一版默认关（`tools.deferred_native: auto` 需显式写入）？建议默认开。
2. Q2 名单是否同意 9 个；`task`/`update_plan` 是否进默认。
3. Q4/Q5 入表时机：`segment` 为默认、并把 MCP 也统一到 `segment`，还是 MCP 保 `immediate`。
4. Q6 `memory`/`recall`/`session_search` 默认延迟是否可接受（个人助理/长会话场景的关键工具）。
5. Q7 观测字段命名（`deferred_native` / `deferred_loaded` / `newly_loaded_kinds`）与 manifest 展示范围。
6. Q8 新增评测任务的具体场景（`recall` 找回大输出 / `session_search` 跨会话 / `memory` 写入）。
7. 清单文案语言（现为中文混英文工具名，与 `PromptSection` 现状一致）。
8. 是否需要 `suga config` 之外的一条诊断命令（如 `suga tools --deferred`）——建议不做，manifest 够用。

## 人工 Gate

- **Gate 1（本文）**：用户批准设计后才进入计划；本设计不含实施。
- **Gate 2（计划评审）**：实施计划必须逐项列出上表验证手段（含伪终端冒烟与新增评测任务），并注明「默认开」的配置迁移说明。
- **Gate 3（实施后）**：提供 `context`/`tool_search` 事件的改造前后数字、4 个 longsession-hard `--repeat 3` 的通过率/调用数对照、新增评测任务结果、一次真实模型的观察记录；通过率下降或 `calls` 上升 >5% 时回到本设计重议 Q1/Q2。
