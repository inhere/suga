# suga 多语言文案清单（Draft）

> 日期：2026-10-26  
> 范围：Go 生产代码、内置文档、测试/冒烟中的文本契约；排除 `_test.go` 的统计行，但在“文本匹配”单独列出测试。  
> 结论：当前中文主要集中在 chat、CLI、personal runtime 和工具结果；模型可见文本与人类界面文本交错，不能通过全局替换安全完成 i18n。

## 1. 统计口径与可复现命令

这里的“条数”是**含中文的非注释代码行数**，不是词条数；同一行可能有多个字符串，故用于规模估算而不是实施工单。初步扫描结果为 Go 生产代码 1,245 行 / 176 个文件；测试也算入时为 3,094 行 / 414 个文件。文档扫描为：`docs` 21,406 行、`skills` 1,031 行、README 9 行、CHANGELOG 735 行（文档数字按含中文行计，包含设计/历史文档）。

初版数字由起草时的临时脚本得出（脚本放在 `tmp/`，未随仓库保留）。审阅时用下面可复现的命令复核（只排除整行 `//` 注释与 `_test.go`，行尾注释与多行注释仍计入，口径与初版脚本不完全相同）：

```bash
# 每个包：含中文的非整行注释代码行数、文件数
for d in internal/chat internal/cli internal/personal internal/wiring internal/acpserver internal/output internal/clipboardimage pkg; do
  l=$(grep -rhP "[\x{4e00}-\x{9fff}]" --include=*.go --exclude=*_test.go $d | grep -vP '^\s*//' | wc -l)
  n=$(grep -rlP "[\x{4e00}-\x{9fff}]" --include=*.go $d | grep -v _test.go | wc -l)
  echo "$d $l $n"
done
```

复核结果（2026-10-26，行 / 文件）：internal/chat 304 / 30、internal/cli 206 / 22、internal/personal 203 / 48、internal/wiring 91 / 15、internal/acpserver 16 / 4、internal/output 14 / 4、internal/clipboardimage 23 / 5、pkg 102 / 31；`internal pkg cmd` 合计 965 行。下文各表沿用初版数字，两者都只用于估算规模，实施计划以逐文件清点为准。扫描还发现 `scripts` 中 119 行生产/驱动文本，主要是评测和 ptysmoke 场景，不属于 suga 运行时包，但会约束回归。

## 2. 按包清点

### A. 直接显示给人

| 包/位置 | 规模（含中文行，约） | 代表内容 |
|---|---:|---|
| `internal/chat` | 344 / 30 文件 | `chat_usage.go` 的 `:help`、`:usage`、`:context` 与未知命令；`approval_reply.go:58-72` 的 `允许执行 ...？[y]是 [N]否`；`chat_undo.go` 的回滚确认与冲突提示；状态行、任务列表、图片/剪贴板提示 |
| `internal/cli` | 209 / 22 文件 | `config.go:82,110` 的“已写入/已删除”；`data_toolbody.go` 的迁移进度和 Ctrl-C 提示；`data_usage.go` 的库统计；命令错误、帮助补充和导出提示 |
| `internal/personal` | 274 / 48 文件 | 回合状态和预算告警；任务/会话列表、压缩、空闲整理、交接状态；向 chat/CLI 返回的错误和进度 |
| `internal/wiring` | 101 / 15 文件 | 启动时配置错误、工具/skills/MCP 装配提示、系统运行状态；部分内容会同时进入模型上下文，见 D |
| `internal/acpserver` | 20 / 4 文件 | ACP 侧用户可读的拒绝、会话/模型切换错误和内存确认；不是所有错误都能翻译（契约部分见设计稿） |
| `internal/output` | 14 / 4 文件 | 终端 markdown/live output 的展示前缀、错误展示辅助文字；stream-json 字段本身不应翻译 |
| `internal/clipboardimage` | 24 / 5 文件 | “剪贴板”、缺少 `wl-paste`/`xclip`、图片大小限制等平台提示 |
| `scripts/ptysmoke` | 约 119 行（驱动文本） | pty 场景输入、期望看到的提示和 trace 说明；英文/中文场景应由语言环境固定，不宜把脚本里的协议标记翻译掉 |

代表性界面词条（实施第一阶段候选）：

- `internal/chat/approval_reply.go:58-63`：允许执行、`[y]/[N]/[a]/[d]` 选项。
- `internal/chat/chat_usage.go:22,75,82,284-287`：用量、后台任务、未知 `:` 命令。
- `internal/cli/config.go:82,110`：配置写入/删除结果。
- `internal/personal/deadline.go:131-139`：截止时间提醒（交互式 job 可见，但默认也可能被模型读取，归 D）。
- `internal/clipboardimage/clipboard_linux.go:30`：平台依赖安装提示。

### B. 进入模型请求、会被模型读到

| 包/位置 | 规模（约） | 代表内容 |
|---|---:|---|
| `pkg/agentcore` | 123 / 30 文件 | 工具描述与参数说明、工具结果压缩/溢出提示、审批结果注记、API/图片能力错误；`tools/output.go:173` 的 `ref` 展开指引是模型操作协议的一部分 |
| `internal/personal` | 上表 274 行中约 120–160 行 | 预算/截止/循环调用提醒、交接包、压缩和收工检查、工具中断后的下一轮说明；例如 `budget.go:212-214` 明确要求模型如何收尾 |
| `internal/wiring` | 上表 101 行中约 50–70 行 | `prompt.go` 的系统提示片段、工具/规则/skills 清单、环境与宿主上下文、工具描述拼接 |
| `internal/chat` | 上表 344 行中约 30–60 行 | chat 运行时插入的模型提醒、交接/插话状态；纯 UI 渲染仍归 A |
| `pkg/agentcore/tools/native` | 包统计包含在 agentcore | `memory` 只读/确认、shell/文件工具错误；工具名和结构化参数是模型协议，不能仅按语言翻译 |

代表例子：

- `pkg/agentcore/tools/approval.go:275`：`[已经人工批准执行]`，是工具结果中给模型的事实标记。
- `pkg/agentcore/tools/output.go:173,189-194`：输出已保存为 `ref`、用 `session_search`/`recall` 展开的操作指引。
- `internal/personal/budget.go:212-214`：预算耗尽后要求“不要再调用任何工具、直接报告”，翻译会改变模型行为风险。
- `internal/personal/deadline.go:134-139`：截止提醒直接写给模型，且同一运行可能在终端显示。
- `pkg/agentcore/tools/native/memory.go:172,340-342`：只读、外部内容/确认语义，不能和普通 UI 错误混用。

### C. 文档

- `skills/suga-guide`：约 1,031 行含中文（目录总规模约 185KB），是给人读的安装、配置、命令和故障排查文档；不是运行时词条，也不是第一阶段翻译对象。
- `docs/`：约 21,406 行含中文，主要是设计、计划、研究和历史记录。它们应保留原语言作为项目记录；只有面向用户的 usage 文档在第二阶段考虑英文版。
- `README.md`：约 9 行含中文，通常是中文说明/链接，不应把设计文档或 CHANGELOG 当作可翻译 runtime 资源。
- `CHANGELOG.md`：约 735 行含中文，发布记录应保持版本历史原文；未来可按发布策略增加英文摘要，但不纳入本任务第 1 阶段。

### D. 两用/边界与文本契约

| 位置/文本 | 两用原因 | 风险 |
|---|---|---|
| `pkg/agentcore/tools/approval.go:275` ` [已经人工批准执行]` | 既是模型能理解的事实，又可能出现在 transcript/trace 和人工查看的工具结果中 | 改写会影响模型判定和现有断言；应保留稳定 key/协议标记，UI 另显示本地化文案 |
| `internal/personal/deadline.go:131-139` `[时间]`、截止提醒 | 同一字符串可能作为上下文消息进入请求，也会在 job 输出可见 | 第 1 阶段不翻译 B；至少把模型提醒和 UI 告警拆成不同词条 |
| `internal/personal/budget.go:212-214` 预算收尾指示 | 不是普通错误，而是控制模型下一步行为的提示 | 必须由 B 类评测覆盖，暂不纳入 UI 翻译 |
| `pkg/agentcore/tools/output.go:173,189-194` `ref`/`session_search`/`recall` | 模型调用工具的操作协议，同时用户会看到压缩结果 | 工具名、`out:` ref、参数格式不可翻译；自然语言说明可在第三阶段单独评测 |
| `internal/wiring/prompt.go` 与工具 description | prompt 前缀的一部分，影响模型行为和缓存命中率 | 第 1 阶段排除；任何改动都要做长会话/缓存基线 |
| `:help`/`:usage`/审批输入选项 | 终端文案 + ptye2e/ptysmoke 按完整文本或片段匹配 | `zh` 固定场景继续使用中文；英文场景新增断言，不修改协议 token |
| ACP / stream-json 字段及错误 code | 人可读 message 与机器协议混在一起 | 字段名、method、tool name、error code、stop reason 保持英文/稳定；只有展示层 message 可按客户端能力翻译 |

## 3. 按文本匹配的现有依赖

以下不是完整调用图，而是清点时确认的高风险代表；实施前应由 grep + 反向验证补全：

- 单元测试：`internal/chat/approval_reply_test.go:41-44` 精确断言审批提示；`internal/chat/chat_approval_test.go:97-98` 匹配“用户拒绝执行”和“本轮被拒绝的工具”；`internal/chat/budget_chat_test.go:76`、`budget_render_test.go:22,37` 匹配预算文字；`internal/acpserver/denial_test.go:136` 匹配“3 次工具调用被拒绝”。
- 工具/模型契约测试：`pkg/agentcore/tools/approval` 相关测试检查批准注记；输出测试检查 `ref`/省略标记；工具描述测试可能按中文描述或工具名匹配。
- ptye2e：`test/ptye2e/chat_smoke_test.go`、`chat_turn_test.go`、`chat_history_test.go` 对提示符、审批、工具摘要、命令输出做文本片段断言。
- ptysmoke：`scripts/ptysmoke/chat_approval.py`、`chat_observe.py`、`chat_steer.py` 等通过正则匹配 `❯`、`⏺`、`[插话]`、审批答复、状态行，并检查 trace 事件；这些脚本启动真实二进制，不是可忽略的单测。
- 代码逻辑：`:` 命令的命令名、`[时间]`/`[收工检查]` 等标记、`out:` ref 和 `tool_search`/工具名由程序或模型按文本识别。此类应使用稳定 key/机器字段，不能依赖翻译后的自然语言。

## 4. 初步分期建议

1. **第一阶段只做 A**：chat/CLI/审批/状态/用户可读错误；默认保留 `zh` 兼容现有断言，并提供 `en`。从模型请求、工具 description、预算/截止/交接提示中排除双用项。
2. **第二阶段做文档**：按 `lang` 发布匹配版本的 suga-guide 英文版；设计/研究/历史文档不自动翻译。缺少对应版本时回退到中文并明确文档版本。
3. **第三阶段做 B**：另出设计和真实 provider 评测，覆盖工具调用成功率、预算/截止/审批语义、缓存命中率和长会话基线；不能把第三阶段混入普通 UI PR。

## 5. 清单结论

已知现状“gcli 帮助基本英文”只对命令框架描述成立；实际 `config` 成功/错误输出和大量 chat 文案仍是中文。`lang-rules.yaml` 是编程语言规则，不应纳入语言选择配置。第一阶段应使用稳定的 key、固定机器 token 与 UI facade，避免对模型提示前缀和 ACP/stream-json 契约造成隐性变化。
