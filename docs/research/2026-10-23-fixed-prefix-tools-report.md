# 固定前缀（system + 工具 schema）缩减报告：实测 20 个工具 28,240 字符

日期：2026-10-23 · 数据：隔离 `SUGA_HOME` + 默认配置 + 本地假 provider 抓到的**真实请求体**（`tmp/prefix/dump/request-1.json`），
评测侧工具调用频率来自 `tmp/eval/` 下 128 个 run 的隔离会话库（只读 `mode=ro`）。
范围：只分析、不改 suga 的 Go 代码。

## 结论（先看这八条）

1. **实测固定前缀 31,631 字符 ≈ 8.1k token**：20 个工具 schema 28,240 字符（数组口径）/ 27,951 字符（suga 自己的 `[]ToolSchema` 口径，见 `context` 事件）+ system 3,391 字符，按 10-22 报告实测的 3.9 字符/token 折算。工具约占整个请求体（35,939 字节）的 79%，占首轮 `est_tokens` 8,694 的 93%（余下是 2,435 字符的用户消息）。与 10-22 报告的 27,383 + 3,391 一致：这次多 568 字符，因为 `view_image` 并入 `file_read` 成为隐藏别名后，工具表换成 `web_fetch`。
2. **结构上 description 是大头**：description 14,223 字符（50%）、parameters 12,326（44%）、工具名 185 + JSON 骨架 1,485（6%）。最贵的 5 个工具（memory / file_refs / file_symbols / shell_exec / file_read）合计 12,106 字符（43%），它们的 description 自身又占 55–61%。
3. **可机械删掉的冗余共 3,951 字符（14%）**（按改完重新 marshal 的 schema 重算为 3,955，差 4 个字符是 file_grep 里被删掉的 4 个引号在 JSON 里各多一个转义），分三类：①解析器/语言支持细节（file_refs 782、file_symbols 971）；②`memory` 工具描述里与「记忆索引」note 语义重叠的策略段（541，那两段在索引 note 里已用中文写清）；③跨工具重复的约定（`no_ignore` 在 5 个工具里各写一遍、`include_tests` ×2、`outline`/background/watch 的复述、system 已覆盖的 untrusted-data / rm / 精确编辑约定）。逐条字符数见第三节表格，全部在同一份真实 schema 上量过。
4. **`deferred_tools` 恒为 0 是三层过滤的结果，不是配置问题**：可见性层只把 **MCP 工具**放进延迟集（`pkg/agentcore/tools/visibility.go:156` 的 `IsMCP` 过滤），20 个 native 工具**永远**进不去；`auto` 模式还要求 MCP schema 总量 > 上下文窗口 × 4 字节/token × 10%（128k 窗口 = 51,200 字符），且被 `Preferred` 的 MCP 工具会被 pin 住；隔离 home 与 29 个有 `context` 事件的评测 run 里 `tools_mcp` 全为 0。
5. **使用频率极不均衡**：128 个评测 run、3,348 次工具调用里，`shell_exec` 1,158、`file_read` 841、`file_edit` 473、`file_grep` 328、`file_list` 204、`file_write` 167、`file_find` 55、`file_refs` 48、`file_remove` 22，其余 11 个工具合计 44 次（task 14、update_plan 12、file_symbols 6、agent 4、recall 4、memory 2、session_search 2；`skill`、`session_message`、`web_fetch`、`suga_manifest` 为 0），另有 8 次落在旧版工具名 `read`/`write`/`suag_manifest` 上。
6. **第一批（只改描述措辞，低风险）净省 3,611 字符 ≈ 926 token/次调用**（3,951 − 340 字符新增的系统提示约定）。对 naming-rerun 那种 84 次调用的会话 ≈ 303k 字符 ≈ 77.8k prompt token（多数是缓存命中，按 90% 命中、缓存读 1/10 价折算的 billed 侧 ≈ 7.8k）。
7. **第二批把 native 工具也改成延迟加载，能再省 14,371 字符 ≈ 3,685 token**（11 个零用/低频工具的 15,197 字符减去 826 字符的延迟清单），但必须改 `Visibility` 的选取范围 + 段上下文文案 + 配置键，属中高风险（模型可能不知道有这个工具），且会给前缀缓存新增一个变化点。
8. **工具表在会话中途已经基本稳定**：模型切换不再改工具表（`view_image` 成为 `Hidden` 别名，`pkg/agentcore/tools/registry.go:145` 过滤、manifest 也不列），现在中途唯一会变的来源是 MCP 延迟加载；跨 run 的差异全部来自二进制版本与配置（29 个评测 run 出现 4 种工具集合、12,346–27,383 字符）。

## 一、口径与复现

| 项 | 做法 |
| --- | --- |
| 二进制 | `go build -o tmp/prefix/suga.exe ./cmd/suga`（Windows 主机，go1.26.0） |
| 隔离 home | `tmp/prefix/home/config.yaml` 只写 `model: fake/m` + `providers.fake`（`type: openai`、`base_url: http://127.0.0.1:18799/v1`、`context_window: 128000`）与 `retry.max_attempts: 1`；其余全部默认值，未读改真实 `~/.suga` |
| 请求捕获 | `tmp/prefix/dump_server.py`：OpenAI 兼容端点，把每个请求体原样写成 `dump/request-N.json`，回一段 SSE 文本 |
| 触发 | `SUGA_HOME=<隔离 home> tmp/prefix/suga.exe run -p "say hi" --allow-write`（在 `tmp/prefix/ws` 这个空 git 仓库里跑） |
| 字符口径 | `json.dumps(obj, ensure_ascii=False, separators=(",",":"))` 的长度，与 Go 的 `json.Marshal` 等价 |
| token 口径 | 字符 ÷ 3.9（10-22 报告在 `command-code/deepseek-v4.1-flash` 上的实测比例），只用于排序与量级 |

交叉核对（suga 自己在 `context` 事件里记录的口径，`tmp/prefix/context.py` 从隔离 home 的 `sessions.db` 读出）：

```
context turn {'tools': 27951, 'tools_count': 20, 'tools_mcp': 0, 'deferred_tools': 0,
              'system': 3391, 'est_tokens': 8694, 'window': 128000, 'skills_total': 48}
tool_names: ['agent', 'file_edit', 'file_find', 'file_grep', 'file_list', 'file_read', 'file_refs',
             'file_remove', 'file_symbols', 'file_write', 'memory', 'recall', 'session_message',
             'session_search', 'shell_exec', 'skill', 'suga_manifest', 'task', 'update_plan', 'web_fetch']
```

27,951 与 28,240 的差 = 每个工具的 `{"function":…}` 外壳（约 14 字符/工具）与数组括号/逗号：`context` 事件 marshal 的是 `[]api.ToolSchema`（`internal/personal/context_event.go:37-45`），线上发的是 `{"type":"function","function":…}`。分析脚本：`tmp/prefix/analyze.py`（逐工具大小）、`measure.py`（逐条改动的字符数）、`parts.py`（分项与延迟场景）、`usage.py` / `stability.py`（评测侧频率与工具集），以及 `tmp/prefix/dump/` 下的原始请求与 `tools-text.json`；`tmp/` 已 gitignore，未入库，复现完可直接删该目录。

## 二、每个工具 schema 的字符数与占比

降序；`desc` 为工具描述，`params` 为整个 `parameters` 对象（含每个参数的 description 与 `type`/`enum`），`name+骨架` 为 `{"type":"function","function":{…}}` 的固定外壳（不含工具名）。

| # | 工具 | 字符 | 占比 | desc | params | 参数数 | name+骨架 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | memory | 3,134 | 11.1% | 1,906 | 1,144 | 10 | 78 |
| 2 | file_refs | 2,548 | 9.0% | 1,495 | 969 | 6 | 75 |
| 3 | file_symbols | 2,347 | 8.3% | 1,433 | 829 | 6 | 73 |
| 4 | shell_exec | 2,090 | 7.4% | 1,285 | 718 | 5 | 77 |
| 5 | file_read | 1,987 | 7.0% | 1,086 | 815 | 5 | 77 |
| 6 | file_grep | 1,911 | 6.8% | 612 | 1,213 | 10 | 77 |
| 7 | session_search | 1,859 | 6.6% | 929 | 843 | 8 | 73 |
| 8 | agent | 1,636 | 5.8% | 1,009 | 549 | 4 | 73 |
| 9 | file_edit | 1,366 | 4.8% | 485 | 799 | 5 | 73 |
| 10 | session_message | 1,243 | 4.4% | 613 | 538 | 4 | 77 |
| 11 | task | 1,114 | 3.9% | 484 | 553 | 6 | 73 |
| 12 | recall | 1,031 | 3.7% | 626 | 326 | 3 | 73 |
| 13 | web_fetch | 956 | 3.4% | 379 | 495 | 3 | 73 |
| 14 | file_find | 942 | 3.3% | 341 | 519 | 4 | 73 |
| 15 | file_list | 836 | 3.0% | 277 | 477 | 4 | 73 |
| 16 | file_remove | 794 | 2.8% | 390 | 320 | 2 | 73 |
| 17 | skill | 752 | 2.7% | 351 | 321 | 3 | 75 |
| 18 | update_plan | 671 | 2.4% | 152 | 435 | 2 | 73 |
| 19 | file_write | 548 | 1.9% | 177 | 288 | 2 | 73 |
| 20 | suga_manifest | 454 | 1.6% | 193 | 175 | 1 | 73 |
| — | **合计** | **28,219** | 100% | **14,223** | **12,326** | 93 | **1,485** |

（`name+骨架` 列只算 `{"type":"function","function":{…}}` 的固定外壳；20 个工具名本身再加 185 字符。参数数 93 = 各工具 `properties` 数之和。）

要点：前 5 个 43%，前 10 个 71%。**description 最贵的 7 个工具里有 6 个不是「用得多」的工具**（memory 2 次、file_refs 48 次、file_symbols 6 次、session_search 2 次、agent 4 次、session_message 0 次），说明前缀成本与使用频率严重错配——这正是延迟加载的动机。

## 三、冗余清单：哪些内容可以去掉（逐条实测 3,951 字符）

每条都在真实 schema 上按「删除的原文跨度」量过（`tmp/prefix/measure.py`，脚本断言每个跨度只出现一次）。

| # | 工具 | 冗余内容 | 为什么冗余 | 省字符 |
| --- | --- | --- | --- | --- |
| 1 | file_refs | 「Use it instead of guessing file_grep patterns for a call chain.」 | system 提示第 6 行已明确要求「who calls X … start with file_refs」 | 64 |
| 2 | file_refs | Go 解析细节（`def/call/sel/ref`、`pkg:X`、无类型检查） | 输出行自带 `class` 与 `pkg:` 前缀，模型看一行就懂；这是实现说明 | 245 |
| 3 | file_refs | JS/TS/Python/…语言列表、YAML/TOML 键路径、ctags 取舍 | 同上；`approx` 标记已在每个结果行上 | 377 |
| 4 | file_refs | 「Other text files use word-boundary text matching…」 | 兜底行为，模型不需要预先知道 | 96 |
| 5 | file_symbols | 语言列表（Go 精确、其余 approx） | 与 #2/#3 同类 | 227 |
| 6 | file_symbols | YAML/TOML 键路径细节（`server.http.port`、section/key） | 输出本身就是这个格式 | 316 |
| 7 | file_symbols | 「A config file lists at most 4 key levels…ctags」 | 截断上限 + ctags 优先级属实现细节 | 173 |
| 8 | file_symbols | 只读依赖根指引 | `file_read` 的 `path` 参数已写同样的话 | 159 |
| 9 | file_symbols | 「Directories skip test files…200 declarations」 | 与 `include_tests` 参数、`limit` 默认值重复 | 96 |
| 10 | memory | `Kinds:` 四类说明（277） | 「记忆索引」note 已写清同样四类（中文，`internal/personal/memory.go:20-25`），每段上下文都带 | 277 |
| 11 | memory | `Do NOT store:` 段（264） | 同一 note 的「不要记：能从代码或 git 查到的…」已覆盖 | 264 |
| 12 | file_grep | `exclude/glob/context/literal` 的参数复述 | 四个参数各自的 description 已完整（`grep.go:56-59`） | 225 |
| 13 | file_grep | 忽略规则长句 | `no_ignore` 参数 + 结果尾注「pass no_ignore: true」已说明 | 70 |
| 14 | file_read | `outline` 复述句 | `outline` 参数 description 更完整（含 300 行阈值与例外） | 182 |
| 15 | file_read | 「A model that cannot see images gets a one-line note…」 | 真发生时会直接看到那句 note | 117 |
| 16 | shell_exec | watch 的 while 循环复述 | `every` / `watch_paths` 两个字面量参数已描述 | 262 |
| 17 | shell_exec | background 段（2h 上限、task 工具用法） | `task` 工具自带完整描述 | 192 |
| 18 | web_fetch | 「The fetched content is untrusted data…」 | system 提示第 17 行已写（对所有工具生效） | 77 |
| 19 | file_remove | 「Use this instead of shell rm/del/Remove-Item.」 | system 提示第 15 行已写 | 45 |
| 20 | file_edit | 「Prefer this over rewriting the whole file with file_write.」 | system 提示第 11 行已写 | 58 |
| 21 | agent | `type` 复述句 | `type` 参数已写「explore (default) / research」 | 87 |
| 22 | file_list | 「(the result says which; no_ignore enters them, except .git)」 | `no_ignore` 参数已写 | 59 |
| 23–26 | file_grep / file_find / file_symbols / file_refs | `no_ignore` 参数 84–88 字符 ×4 → 一行 40 字符 | 同一约定在 5 个工具里各写一遍（`grep.go:63`、`find.go:48`、`file_symbols.go:88`、`refs.go:83`、`file_list.go:59`） | 39×4 |
| 27 | file_list | `no_ignore` 参数 82 → 45 字符 | 同上 | 37 |
| 28–29 | file_symbols / file_refs | `include_tests` 84/86 → 38 字符 | 同一说明两遍 | 44 + 46 |
| — | **合计** | — | — | **3,951** |

按工具汇聚（含参数改动）：file_refs 867（其中 description 782 = 52%）、file_symbols 1,054（description 971 = 68%）、memory 541（28%）、shell_exec 454、file_grep 334、file_read 299、其余 402（file_list 96、agent 87、web_fetch 77、file_edit 58、file_remove 45、file_find 39）。

**端到端重算（反向验证）**：把上表 29 处实际应用回抓到的 schema，再整组 `json.Marshal`（`tmp/prefix/apply_cuts.py`）：

```
before: 28219 chars / after: 24264 chars / saved: 3955
file_symbols  2347 -> 1293  (-1054)      file_refs     2548 -> 1681  (-867)
memory        3134 -> 2593  (-541)       shell_exec    2090 -> 1636  (-454)
file_grep     1911 -> 1573  (-338)       file_read     1987 -> 1688  (-299)
file_list      836 ->  740  (-96)        agent         1636 -> 1549  (-87)
web_fetch      956 ->  879  (-77)        file_edit     1366 -> 1308  (-58)
file_remove    794 ->  749  (-45)        file_find      942 ->  903  (-39)
其余 8 个工具不变
```

3,955 与逐条之和 3,951 的差 = file_grep 被删段落里的 `["*.go", "!*_test.go"]`（4 个引号在 JSON 里各占一个转义字符）。所以「线上字符节省」取 **3,955**，本表取「原文跨度」3,951，两者相差 0.1%。

**另一种冗余（不计入上表，因为需要改结构）**：`file_list` 与 `file_find` 的职责几乎重合（都是「按 glob 找路径」，一个列目录一个递归），`recall` 与 `session_search` 的 `out:` ref 语义写了两遍，`file_read` 的 `outline` 与 `file_symbols` 的「声明清单」也重叠——见第五节档 2。

## 四、按需加载（deferred tools）机制现状

### 4.1 机制

| 环节 | 位置 | 行为 |
| --- | --- | --- |
| 模式 | `pkg/agentcore/tools/visibility.go:20-39` | `tools.deferred` = `auto`（默认，空值）/ `always` / `never` |
| 选谁延迟 | `visibility.go:142-178` | 遍历 `ListTools`，**只收 `IsMCP(tool)` 且不在 pinned 名单里的**；`auto` 要求 MCP schema 总量 > `window × 4 × 10%`（128k 窗口 = 51,200 字符） |
| pinned | `internal/wiring/runtime.go:511-512` | `preferredMCPToolNames`：环境段里标为 preferred 的 MCP 工具**不延迟** |
| 已加载集合 | `internal/personal/visibility.go:13-33`、`handoff.go:14` | 存 session meta `loaded_tools`，续会话/交接后仍生效 |
| 列表 | `visibility.go:248-268` | 未加载的不出现在 `ListTools`（= 不进模型前缀） |
| 模型如何发现 | `visibility.go:422-450` → 段上下文「## 延迟加载的工具」 | 一行一个 `- name — description(≤160 字符)`，按 MCP server 分组 |
| 加载动作 | `pkg/agentcore/tools/native/tool_search.go`，仅在 `vis.Active()` 时注册（`internal/wiring/capabilities.go:525-527`） | `query` 关键字打分（名字权重大于描述）或 `select:a,b` 精确选择；每次加载写 `tool_search` 事件并标 `cache_invalidated` |
| 兜底 | `visibility.go:272-292` | 直接调用未加载的延迟工具：**不执行**，返回「未加载 + 完整 schema + 已加载，请再调一次」，因此不会触发审批，代价是一次额外调用 |

### 4.2 为什么当前是 0

三层过滤叠加，各层都有证据：

1. **只有 MCP 工具能被延迟**（`visibility.go:156`）——20 个 native 工具没有入口，即使 `tools.deferred: always` 也一样。
2. **`auto` 阈值很高**：128k 窗口下要 MCP schema 合计超过 51,200 字符才触发；几个工具的 MCP server 通常只有 2–5k，差一个量级。
3. **默认配置里没有 MCP server**（`internal/wiring/capabilities.go:105` 从 `ReadMCPServerSettings` 读，隔离 home 为空）→ 延迟集空 → `tool_search` 根本不注册。

实测：本报告抓的请求里 `tools_mcp=0`、`deferred_tools=0`、工具表里没有 `tool_search`；29 个有 `context` 事件的评测 run 里 **没有一个** `tools_mcp` 或 `deferred_tools` 非零（`tmp/prefix/stability.txt`）。所以 10-22 报告的「deferred_tools 恒为 0」是结构性的，不是这次跑得特殊。

### 4.3 哪些工具适合延迟（按 128 个评测 run 的使用频率）

| 分组 | 工具 | 字符 | 调用次数（3,348 次中） | 是否适合延迟 |
| --- | --- | --- | --- | --- |
| 零用 | skill | 752 | 0 | 适合：system 已列 skills 名，用到时 `skill name=` 一调即加载 |
| 零用 | session_message | 1,243 | 0 | 适合：只在与其它 chat 会话协作时需要 |
| 零用 | web_fetch | 956 | 0 | 适合（配合「模型不知道自己不能上网」的说明） |
| 零用 | suga_manifest | 454 | 0 | 适合：诊断用 |
| 极低频 | session_search / recall | 1,859 + 1,031 | 2 / 4 | 适合：历史检索，通常在一个会话里用几次 |
| 极低频 | memory | 3,134 | 2 | 适合，但要小心：写入策略（Do NOT store / Worth storing）是行为约束，延迟后模型只在搜索时才看到 |
| 极低频 | agent | 1,636 | 4 | 适合：子 agent，按需 |
| 低频 | task / update_plan | 1,114 + 671 | 14 / 12 | 可延迟，但与 `shell_exec background` 强耦合，建议保留（见风险） |
| 低频 | file_symbols | 2,347 | 6 | 可延迟，但它是「找声明」的主路径之一，延迟后每次多一轮 |
| 常用 | file_refs / file_grep / file_read / file_write / file_edit / file_list / file_find / shell_exec / file_remove | 15,022 | 3,320 | **不建议**延迟（占调用 99%） |

零用 4 个 = 3,405 字符（12%），零用 + 极低频 11 个 = 15,197 字符（54%）。延迟清单本身要花字符：11 个工具各一行（名字 + 60 字符截断描述）≈ 826 字符，所以净省 14,371 字符 ≈ 3,685 token。

### 4.4 延迟后的发现路径与风险

- **发现路径**：段上下文一行/工具 + `tool_search`；另有 4.1 的兜底（直接调用未加载工具会返回 schema 并即时加载）。**兜底是这套机制的关键**：即使模型猜对了名字，最坏代价是「一次额外调用」，不是失败。
- 风险 1（中）：**模型不知道自己有某工具**。现在是「环境段 + 工具表」双保险；延迟后只剩段上下文一行，160 字符截断后 `session_search` / `memory` 这类描述很难一句话讲清。
- 风险 2（中）：**前缀缓存失效点增加**。加载动作必然改工具前缀（事件里的 `cache_invalidated`），若在段中途频繁加载，命中率下降；10-22 报告里 t5/t18 的缓存掉档就是这么吃的亏。设计上应**只允许在段边界（换段/交接）改变延迟集**。
- 风险 3（低）：`tool_search` 的检索是名字/描述分词的朴素打分（`visibility.go:305-400`），中文查询效果不如英文；`select:` 精确选择可以兜住。
- 风险 4（低）：延迟集固定于 `NewVisibility` 构造时（`visibility.go:139-141` 注释「fixed for the lifetime of the layer」），会话中途新增 MCP server 不会生效——这是现在的稳定设计，扩展 native 延迟时要保持同一约束。

## 五、压缩方案清单

三档：**档 0 只改配置**（不改代码）、**档 1 只改描述措辞**（低风险）、**档 2 合并/删参数**（中）、**档 3 改延迟加载**（中高）。字符数都是实测；token = 字符 ÷ 3.9。

### 档 0：`tools.disabled` 关掉零用工具（零代码，功能缺失换字符）

| 改哪里 | 省 | 风险 | 验证 |
| --- | --- | --- | --- |
| `config.yaml` 的 `tools.disabled: [skill, session_message, web_fetch, suga_manifest]`（机制：`internal/wiring/capabilities.go:131-136` 注销，schema 直接消失） | 3,405 字符 ≈ 873 token | 低技术风险、高功能风险：这些能力真的没了。适合「只做批处理/评测」的 home，不适合个人 chat 的默认配置 | `context` 事件的 `tools`/`tools_count` 降到 16 个 / 24,546 字符；相关评测项（若有）会失败 |

### 档 1：只改描述措辞（第一批建议，低风险）

| 改哪里（file:line） | 内容 | 省字符 | 风险 | 验证 |
| --- | --- | --- | --- | --- |
| `pkg/agentcore/tools/native/refs.go:41-56` | 删第三节 #1–#4（解析器/语言细节、与 system 重复的指引） | 782 | 低：输出的 `class`/`approx`/`pkg:` 标记不变，模型仍能从结果学到 | `scripts/eval` 里 refs 相关任务（`explore-callers-*`、naming-rules 的「谁调用」轮）检查项不变；`go test ./pkg/agentcore/tools/native -run FileRefs` |
| `native/file_symbols.go:41-…` | 删 #5–#9 | 971 | 低：同上，且 `outline` 与 `limit` 参数仍描述上限 | `-run FileSymbols`；长会话任务里「列签名」轮仍通过 |
| `native/memory.go:68-90` | 删 #10–#11（Kinds、Do NOT store），保留 Worth storing / Actions / summary 规则 | 541 | 低-中：记忆索引 note 每段都在（`internal/personal/memory.go:20-25`），信息不丢；但 note 是中文，工具描述是英文，模型对英文策略段的注意力可能更高 → 需要一次真实会话确认仍会写记忆 | `go test ./pkg/agentcore/tools/native -run Memory`；一次真实模型的 `memory add` 冒烟（`fakellm` 也能驱动） |
| `native/code_exec.go:166-…` | 删 #16–#17（watch/background 复述），background 留一句「返回 task id，见 task」 | 454 | 低：参数描述完整 | `-run ShellExec`；`scripts/ptysmoke` 的 background/watch 场景 |
| `native/grep.go:34-41` | 删 #12–#13 | 295 | 低：参数描述完整 | `-run FileGrep`；grep 相关评测 |
| `native/file_read.go:57-…` | 删 #14–#15 | 299 | 低 | `-run FileRead` |
| `native/fetch.go:264-…`、`remove.go:60-…`、`edit.go:23-…` | 删 #18–#20（system 已覆盖） | 180 | 低 | 现有单测 |
| `native/agent.go:80-…`、`file_list.go:30-…` | 删 #21–#22 | 146 | 低 | 现有单测 |
| 5 个工具的 `no_ignore` + 2 个 `include_tests` 参数（`grep.go:63`、`find.go:48`、`file_symbols.go:88`、`refs.go:83`、`file_list.go:59`） | 统一成一行，约定进 system 提示 | 283 | 低：system 新增 1 行（~120 字符）：「默认跳过 .git/node_modules/vendor/dist/.gitignore；用 no_ignore 包含它们；测试文件用 include_tests」 | `-run 'Ignore\|NoIgnore'`；`ignore_test.go` / `refs_ignore_test.go` 仍过 |
| **合计** | 3,951 − 340（新增 system 约定，估算）；重算 3,955 − 340 = 3,615 | **3,611 ≈ 926 token** | | `context` 事件 `tools` 从 27,951 → 24,000 |

**改法示例（file_refs，1,495 → 713 字符，信息不丢）**：

```
Find where a name is used: who calls X, who uses X, where is X referenced. Searches a file, a directory
(recursive) or the workspace and answers one `path:line class[(qualifier)] text` line each, sorted by path
and line. Pass context: 3 to also show the source lines around each match, so a call site can be read without
a file_read. A qualified name narrows the result to the uses of that type or package: name: "Runner.Run" keeps
the definitions of type Runner and the Runner.x / pkg.Run uses, and a longer path such as s.runner.Run keeps
its last segment. Markdown/text documentation (.md, .txt, .rst) is not searched. Directories skip test files
unless include_tests is set, and a result stops at 200 matches.
```

删掉的全是「实现怎么做」（Go 怎么解析、哪些语言是 approx、ctags 的取舍），保留的是「输入/输出长什么样、怎么缩小结果」。`memory` 删完是 1,365 字符，只剩 Worth storing / Facts vs skills / Actions 三段（删完记得合并空行）。

### 档 2：合并或删参数（中风险）

| 改哪里 | 内容 | 省字符 | 风险 | 验证 |
| --- | --- | --- | --- | --- |
| `native/file_list.go` + `native/find.go` | 合并成 `file_find`（`depth`/`max_entries` 变可选参数，或 `mode: list/find`）：836 + 942 = 1,778 → 约 1,060（file_find 的 942 + 一个 `mode` 参数 ~120） | ~720 | 中：两个工具都在用（204 + 55 次），改参数名会让模型用错一次；`file_list` 的目录语义（`dir` 默认 workspace root、`depth` 递归）要保留在描述里 | 评测里 `file_list`/`file_find` 的调用成功率与总调用数不变；单测合并后重写 |
| `native/file_symbols.go` | 删「It lists declarations only…use file_refs.」（93）与「Unsupported file types…unavailable for them.」（119）= 212（实测）；它跟 `file_read outline` 的目的重叠部分另议 | 212 | 中：这两句是「先用哪个工具」的指路牌，删后模型可能拿 file_symbols 找调用点（但 `file_refs` 的描述仍在） | 长会话任务 p7/p13（列签名）轮不出现「拿 file_symbols 找调用点」 |
| `native/plan.go:47-…` + `update_plan` 的 `plan` schema（289 字符） | 压缩 `plan` 的 item 描述（`step`/`status` 枚举）成一句 | ~100 | 低-中 | `-run UpdatePlan` |
| `native/ledger.go`（recall）与 `search.go` | `out:` ref 语义只写一次（recall 里），session_search 只写「tool outputs 用 recall 读」 | ~80 | 低 | `-run 'Recall\|SessionSearch'` |
| **合计** | | **~1,000 ≈ 256 token**（720 + 212 + 约 100 + 约 80，后两项为估算） | | 四项 eval 任务的 `calls` 不应上升 |

### 档 3：把 native 工具也纳入延迟加载（中高风险，收益最大）

| 改哪里 | 内容 | 省字符 | 风险 | 验证 |
| --- | --- | --- | --- | --- |
| `pkg/agentcore/tools/visibility.go:142-178` | 延迟集从 `IsMCP` 扩成「MCP + 一份 native 名单」；模式值沿用 `auto/always/never`，新增 `tools.deferred_native`（名单或 `never`） | — | 中高：改动在装配层，`tool_search` 打分只对 MCP 名做过验证 | 现有 `visibility_test.go`、`wiring/tool_visibility_test.go` 扩展用例 |
| `internal/wiring/capabilities.go:510-528` | `tool_search` 注册条件放宽到「有任何延迟工具」；`lateToolNames` 已含 `tool_search`，无需改 | — | 低 | `TestNewToolVisibility` |
| `internal/personal/segment.go:56,83,108` + `visibility.go:422-450` | 段上下文清单：native 与 MCP 分组、每工具一行（≤60 字符描述） | 826（清单成本） | 中：清单本身变长；且清单在段快照里冻结，换段才刷新 | `context_update_test.go` 的顺序断言 |
| `internal/personal/visibility.go:15-33` | `loaded_tools` 复用（native 名字同样存 session meta） | — | 低 | `visibility_test.go` 的 resume 用例 |
| 默认名单（建议） | 零用 4 个 + 极低频 `session_search`/`recall`/`memory`/`agent` = 8 个，11,065 字符；清单 ~600 | **~10,465 ≈ 2,683 token** | 中高：`memory` 是行为约束型工具，`agent` 是省 token 的关键工具，延迟后要确认模型仍会用 | 四项 longsession-hard 评测的通过率不变、`calls` 上升 ≤5%；`tool_search` 事件数与 `cache_invalidated` 次数 |
| **合计（11 个全延迟）** | | **14,371 ≈ 3,685 token** | | `context` 事件 `tools` 降到 ≈13,600 字符 |

**为什么档 3 不该做「全部延迟」**：`file_refs`（48 次）、`file_grep`（328）、`file_read`（841）、`shell_exec`（1,158）占调用 99%，延迟它们等于每次使用多一轮，按 naming-rerun 的 2,000 字符级调用成本算，得不偿失。

## 六、工具集稳定性：哪些因素会让前缀在会话里变化

| 因素 | 何时变 | 影响 | 证据 |
| --- | --- | --- | --- |
| 二进制版本 | 升级后（`view_image` 合并成 `Hidden` 别名、`suag_manifest` 改名 `suga_manifest`） | 整套前缀重建；跨 run 差异 12,346–27,383 字符 | 29 个评测 run 出现 4 种：17 run/20 工具（含 `view_image`，旧版）、6 run/15 工具（更旧版本：无 `agent`/`skill`/`session_message`/`file_refs`/`file_symbols`，manifest 名仍是 `suag_manifest`）、4 run/16 工具、2 run 无记录（`tmp/prefix/stability.txt`） |
| `--allow-write` / `--read-only` / chat 模式 | 会话开始 | 只读时 `shell_exec`/`file_write`/`file_edit`/`file_remove` 被 `BuildTools` 跳过（`pkg/agentcore/tools/native/catalog.go:199-207`），14–16 个工具 | 评测里 `retry-probe`、`web-probe` 是 12,346 字符/14 工具（事件早于 `tool_names` 字段，未记名字） |
| `tools.disabled` | 配置变更（下一次启动） | 直接少若干工具 | `internal/wiring/capabilities.go:131-136` |
| `web_search` | 配了 `serpapi_key` 才注册（`catalog.go:171-179`） | +1 工具 | 隔离 home 没配 → 无 `web_search` |
| `agents.enabled` | 配置 | `agent` 工具是否注册（`internal/wiring/runtime.go:432-448`） | 29 run 里 21 个有 `agent` |
| skills 是否存在 | `<SUGA_HOME>/skills` | `skill` 工具是否注册（`runtime.go:468-471`） | 隔离 home `skills_total=48` → 有 |
| MCP servers | 配置 / `--mcp` | MCP 工具追加；`tools_mcp` 与延迟集随之变 | 本次 `tools_mcp=0` |
| `session_message` | 有 SUGA_HOME 会话互通能力（`nativeEnvWithSnapshot` 的 `SessionMessenger`） | +1 工具 | 隔离 home 有 → 20 工具里含它 |
| **MCP 延迟加载** | 会话中途 `tool_search` / 直接调用 | 加载后工具表变化，`cache_invalidated` 事件 | `visibility.go:270-292` |
| 模型切换 | 会话中途 `:model` | **已不再变化**：`view_image` 是 `Hidden` 别名，`Registry.Schemas()` 过滤（`registry.go:143-147`），manifest 也不列；读图走 `file_read` 的图片分支 | 新旧两种 20 工具集合的差异正是这一处 |

结论：会话内现在只剩「MCP 延迟加载」一个变化点，其余都在会话开始时定死；档 3 会把 native 延迟加载变成第二个变化点，所以必须把它约束在**段边界**（换段/交接时快照，和 `segmentContext` 一起冻结）。代价方面要记住：provider 的前缀缓存按前缀逐字节匹配，上表任何一处变化都会让**整条**固定前缀（约 8.1k token）重算，10-22 报告里 t5 中途缓存从 18,816 掉到 4,096、t18 首调只命中 22% 就是这类失效的实测例子；因此「让工具表少变」本身和「让前缀更短」是两条独立的收益。

## 七、结论与建议的第一批改动（按收益/风险排序）

| 排序 | 批次 | 收益 | 风险 | 建议 |
| --- | --- | --- | --- | --- |
| 1 | 档 1（只改描述）29 处 | 净 3,611 字符 ≈ 926 token/次调用（naming-rerun 84 次调用 ≈ 77.8k prompt token） | 低：信息仍在输出/参数/system 提示里 | **做**：先改 file_refs + file_symbols（1,753，占一半），再 memory（541），再其余 |
| 2 | 档 0（`tools.disabled` 关零用工具） | 3,405 字符 ≈ 873 token | 低技术/高功能 | 只给专用 home（评测、批处理）用，不进默认配置 |
| 3 | 档 2（合并 file_list/file_find、压缩 plan schema 等） | ~1,000 ≈ 256 token | 中：改高频工具的参数语义 | 第二批，配合 eval 回归 |
| 4 | 档 3 8 个工具延迟 | ~10,465 字符 ≈ 2,683 token | 中高 | 第三批，先做「零用 4 个」灰度（3,405 − 清单 ~300 → 净 ≈3,105 字符）再扩 |
| 5 | 档 3 全部 11 个延迟 | 再 +3,906 ≈ 1,002 token | 中高 | 不建议一次做 |

**第一批（档 1）预计总节省：3,951 → 净 3,611 字符（−12.8% 的前缀字符），≈ 926 token/次调用**（重算口径 3,955 → 净 3,615）；工具表 27,951 → 24,000 字符（`context` 事件可核对）。加上档 2 合计 ≈ 4,955 字符（−17.6%），扣除新增 system 约定后净 ≈ 4,615。

**验证方式（按方案写清该看哪个数）**：

1. 单测：`go test ./pkg/agentcore/tools/native -run 'FileRefs|FileSymbols|Memory|ShellExec|FileGrep|FileRead'`，改参数语义的加 `wiring/native_tool_args_test.go` 一类断言（`properties` 非空的那条规则仍要过）。
2. 前缀数字：`suga inspect trace <sid> --json` 或 `context` 事件的 `tools`/`tools_count`/`deferred_tools`，逐批对照第三节的字符数（本报告的量法可复用 `tmp/prefix/measure.py`）。
3. 行为：`scripts/eval` 的四项 longsession-hard 通过率**不得下降**；`calls` 与 `uncached` 应当**不升**（档 1 不应改变模型策略），档 2/3 允许 `calls` 小幅上升但 `prompt` 总量必须下降。
4. 延迟加载：`tool_search` 事件数、`newly_loaded`、`cache_invalidated`（`internal/personal/visibility.go:38-60`），以及换段前后 `context` 事件的 `tools` 是否只在段边界变化。

## 八、没做到 / 未验证

- **没有改代码重跑**：3,951/3,955 字符是「在真实 schema 上删除这些跨度并重新 marshal」的实测值（后者已做端到端重算），但不是改完 Go 代码后再抓一次请求的对照；「节省的 token」是字符折算，不是 provider 的 `prompt_tokens` 实测。
- **只抓了一次请求**：Windows 主机、默认配置、无 MCP、`--allow-write`、OpenAI chat/completions 协议。Anthropic 协议用 `input_schema` 字段名（`schemaJSON` 里可见），字符数与这里略有出入，未测；`openai-responses` 同理。
- **使用频率来自评测，不是真实个人会话**：128 个 run 都是文件型任务（`tmp/eval/`），`skill`/`session_message`/`web_fetch` 为 0 很可能因为评测不用这些能力，不能推断真实用户也不用（按任务要求未读真实会话库）。
- **3.9 字符/token 是单一 provider 的实测比例**，对别的模型（尤其中文更省 token 的分词器）只是估算；本轮所有 token 数都是相对量。
- **档 3 的「模型仍会用被延迟的工具」未验证**：需要真实模型跑一轮带 `tool_search` 的会话，本次没有联网。
- 隔离 home 的 `context` 事件 `est_tokens=8694` 与「31,631 字符 ÷ 3.9 = 8,110」的差（584）来自用户消息 2,435 字符与 suga 自己的估算系数，未拆到小数。

## 发现但不碰

- `internal/personal/context_event.go:37-45`：`context` 事件的 `tools` 记的是 `[]ToolSchema` 的 marshal 长度，比线上数组少约 14 字符/工具，三份数字（27,383 / 27,951 / 28,240）混用会算错口径。
- `pkg/agentcore/tools/native/memory.go:68-90`：工具描述与 `internal/personal/memory.go:20-25` 的记忆索引 note 是两份独立维护的同一套策略，已经出现中英文不一致（note 中文、描述英文）与内容漂移。
- `pkg/agentcore/tools/native/grep.go:37-40`：description 里复述了 `glob`/`exclude`/`context`/`literal` 四个参数的语义，改参数时容易漏改描述。
- `pkg/agentcore/tools/visibility.go:170`：`auto` 阈值把「MCP schema 是否超过窗口 10%」当唯一判据，不考虑**调用频率**，因此大而少用的 MCP 工具（`gofer_*`）不会延迟，小而常用的反而可能被延迟。
- `internal/wiring/capabilities.go:507-528`：`tool_search` 只在延迟集非空时注册，诊断「为什么模型没搜到工具」时没有入口（`suga_manifest` 的 `deferred_tools.note` 是唯一线索）。
- 评测侧：`tmp/eval/` 里 29 个 run 的工具集合只有 4 种，但没有任何 run 记录 `tools_mcp`/`deferred_tools` 非零，延迟加载机制实际上**没有被任何评测覆盖**。
