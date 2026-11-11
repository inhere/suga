<!-- template_id: design; template_version: 1.2.0 -->
# suga 可信模式、外部来源裁决与敏感路径禁读 设计

> 状态：Draft 0.4 / 待人工计划批准；issue `suga-kkh4`、`suga-f5la`、`suga-4t8i`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-10 | Claude | 初稿：三项合并设计——审批档位与可信模式（kkh4）、外部来源防护“规则初筛 + 小模型裁决”（f5la）、敏感路径禁读（4t8i） |
| 0.2 | 2026-11-10 | Claude | 记录用户对 Q1–Q4 的选择（2026-11-10：“Q1–Q4 按推荐”），转为决策 |
| 0.3 | 2026-11-10 | Claude | 处理第 1 轮两轴评审（`docs/review/2026-11-10-trust-mode-origin-judge-read-deny/`），以收窄为主，见下表 |
| 0.4 | 2026-11-10 | Claude | 处理第 2 轮（两轴 PASS）的纠正项，见第二张表 |

0.3 的处理（按类自查范围：A 的放开项与保护路径、档位来源；B 的输入、装配与信任边界；C 的工具覆盖与匹配规则；前序设计替代；验证）：

| 评审项 | 处理 |
|---|---|
| S1、F2（阻断） | 保护路径（`ClampAllow`）产生的 ask 单独标记为“保护”，`trust` 不放行：chat/ACP 仍询问，无人可问时拒绝 |
| S2、F7 | `trust` 不进配置：只能由启动参数（`--approve`/`--yolo`）或 SDK 选项设定；`tools.approval` 仍只取 `ask`/`auto` 且只作用于 chat（保持前序设计）。模型无法用 `suga config set` 提升档位 |
| S3 | 裁决输入中除用户本回合请求外一律作为不可信材料；无人值守入口的放宽在“兼容”中明示 |
| S4、F11 | 禁读放进读路径统一入口 `resolveRead`（及遍历类工具的逐文件判定），覆盖 `file_read`、`file_list`、`file_grep`、`file_find`、`file_refs`、`file_symbols`、`view_image`、`file_edit`/`file_write` 的读前比对；工具名更正 |
| F1（阻断） | 删去裁决缓存 |
| F3、F4 | 裁决输入改为：本回合用户请求（由 Runner 写入 ctx）、调用的工具名与完整参数（截断）、命中片段与来源；删去“会话目标”与“片段前后扩展” |
| F5 | 写明装配：wiring 构建 judge 场景 provider 并注入；用量写入审批事件 attrs，不计入会话预算 |
| F6 | `trust` 不再放开“目录外询问”（从放开项删除） |
| F8 | `trust` 隐含允许写（`--read-only` 仍优先） |
| F9 | 增“入口 × 档位”适用矩阵；SDK 下记忆确认仍由宿主 `MemoryConfirm` 决定 |
| F10、F12、F16 | 写明匹配规则（绝对路径、符号链接两端、Windows 不区分大小写、`!` 例外）、SDK 传递与 `$SUGA_HOME` 展开；内置列表补几项凭据文件 |
| F13、F14、S7、S10 | 统一表述；写明子 agent、ACP 下的裁决，三个开关的组合语义 |
| S5、S6 | 代码组织写明偏离理由；列出对前序设计的替代 |
| S8、S9 | 验证要求与兼容说明补齐 |
| 原 D6 | 删去（ACP 会话内切换档位不做） |
| S11、F15 | 决策去重、review budget 写具体；引用更正 |

0.4 的处理（第 2 轮）：

| 评审项 | 处理 |
|---|---|
| F17 | 保护标记从 `ClampAllow` 的两个调用处（`rules.Policy.Decide` 与 `approval_policy.go`）都带出；`trust` 只放行“未被降级的 ask”；补宽泛 allow 规则与 always 会话规则命中保护路径的反向用例 |
| F18、S12 | 更正事实：`file_edit` 走 `resolveWrite` + `readFileInRoot`，不经 `resolveRead`；禁读在 `FileEditTool.Execute` 读原文前显式判定；`file_write` 写禁读文件时结果不带旧内容 |
| F19 | 只有用户发起的回合请求进入可信部分；注入回合（`RunRequest.Injected`）沿用本会话最近一次用户请求，注入正文归入不可信部分；删去“未经 Runner”的情形 |
| F20、S14 | “`trust` 隐含允许写”只作用于 chat/run；SDK 下仍以宿主 `AllowWrite` 为准；ACP 客户端选择的 `read_only` 会话模式在 `trust` 下仍生效；作为授权扩大列入待确认 |
| S13 | D8 更正出处并补列记忆设计 D7 |
| F21 | 子 agent 无 MCP，`trust` 对其无实际影响；回合取消时中止回合；架构补 `pkg/agentcore/tools/approval.go`；judge 回退到 compact 时延迟取 provider；SDK `ReadDeny` 的 `~` 在 wiring 合并时展开 |

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：
  1. 可信环境（如 gofer 在 worktree 里跑的编码任务）可以用启动参数放开“需要人确认”类检查，不再因无人可问而整轮卡住；
  2. 外部来源防护的误报不再直接结束回合：逐字命中后由小模型判断这次调用是在执行用户任务还是在听外部内容的话；
  3. 读类工具不能把 `.env`、`~/.ssh` 这类敏感文件读进上下文与 trace。
- scope freeze：只做下文 A、B、C；不改审批规则文件格式、不改 hooks 语义、不改危险命令判定规则、不做 ACP 会话内切换档位。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget：最多 8 轮（第 1 轮 discovery、第 2 轮 confirmation，其后仅在有开放 `CORE_BLOCKING` 时继续）。
- 暴露度（SR1409）：高。依据：suga 已公开发布（tag `v0.54.8`），gofer 以 `run`/`acp` 派活、xhen 以 SDK 嵌入；本设计改变安全边界。两轴独立评审与 ledger，评审目录 `docs/review/2026-11-10-trust-mode-origin-judge-read-deny/`。
- 停止条件：无开放的 `CORE_BLOCKING`。

## 背景与目标

现状（证据见“已确认事实”）：
- 审批只有 `tools.approval: ask|auto` 两档，且只作用于 chat；`run` 无人可问，凡“需要问”的一律拒绝；同一回合被拒 3 次即结束（`DenialLimit`）。gofer 派的编码任务因此常在“本可放行”的调用上整轮停下，而 job 仍 exit 0。
- 外部来源防护按逐字窗口判定“照抄外部内容”。v0.54.7/0.54.8 已把窗口提到 40 字节、写文件不比对 URL，但规则本身两头不准：常见片段仍会误报，模型稍作改写即可绕过。
- 读类工具只约束“路径须在工作区或 read roots 内”；xhen 把 read roots 放开到全盘后，`.env` 中的 token 可被读入上下文与 trace。

用户可见结果：
- `suga run --yolo …`（即 `--approve trust`）在可信环境下不再因“需要确认”而拒绝；任何档位下读类工具都不读 `.env`、`~/.ssh` 等，也不会删除家目录、根目录。
- 下载过网页后，模型写自己的代码、提交信息不再被拦；照抄网页里的指令去执行时，裁决给出拒绝或询问，并说明理由。
- `file_read .env` 返回“该路径被禁止读取（tools.read_deny）”；列目录与查找只显示文件名并标注“禁读”，搜索跳过这些文件。

## 名词

- **审批档位**：一次会话的审批严格程度：`ask` / `auto` / `trust`。
- **确认类检查**：结果为“需要问人”的检查；无人可问时变为拒绝。
- **硬限制**：任何档位都生效、不能被规则或档位放开的检查。
- **保护路径 ask**：`ClampAllow` 把指向 SUGA_HOME、工作区 `.git`、`.suga`、规则文件的 allow 规则降级产生的 ask。
- **初筛**：现有逐字窗口匹配（`ExternalOrigin.Find`/`FindIgnoringURLs`）。
- **裁决**：初筛命中后，由小模型对这次调用给出 `allow`/`deny`/`ask`。
- **禁读路径**：`tools.read_deny` 与内置缺省列表中的 glob。

## 范围与非目标

范围：
- A 审批档位与可信模式：`--approve ask|auto|trust`、`--yolo`；`chat`、`run`、`acp` 与 SDK。
- B 外部来源裁决：`originGuard` 命中后调用裁决模型。
- C 敏感路径禁读：`tools.read_deny`、内置缺省列表、原生读类工具。

非目标：
- 不做沙箱、网络隔离、按命令内容解析 shell（`shell_exec cat .env` 不在 C 的覆盖内）。
- 不做 ACP 会话内切换档位。
- 不把裁决扩展到所有调用；“只对高风险动作检查”的收窄列为后续。
- 不改 gofer：回合因拒绝结束时 job 的退出码属 gofer，另提。
- 不改 xhen 的审批协调器；xhen 经 SDK 新字段自行选择。

## 已确认事实与规范

- **调用链**：注册表自外向内 observe → output（`redact.Secrets`）→ visibility → originObserver → Approval → base（`internal/personal/runner.go:463-468`，`internal/wiring/runtime.go:580-594`）。审批器包装固定为 `guardExternalOrigin(guardDangerous(入口 Approver), chatApproverOf(...), origin)`（`runtime.go:591`）。
- **入口 Approver**：run 用 `newPolicyApprover`，ask 转 Denied（`internal/wiring/approval_policy.go:152-171`）；chat 用 `newChatApprover`（`chat_approver.go:82`）；ACP 用 `permissionHook`（`internal/acpserver/policy.go:117,183`），无连接或失败按拒绝；SDK 透传宿主 Approver（`pkg/sugasdk/session.go:93-99`）。子 agent 只有只读工具，审批器为父策略快照的 `newPolicyApprover`，无人可问（`internal/wiring/subagent.go:122,176`）。
- **默认动作**：run 原生 Allow、MCP Deny；chat 原生 Ask（auto 时 Allow）、MCP Deny；ACP 原生 Ask、MCP Ask；只读与 AgentState 工具一律 Allow（`approval_policy.go:60-81`）。`tools.approval` 只在 chat 读取（`runtime.go:261-270`）。
- **保护路径**：`ClampAllow` 在 chat/ACP 把保护路径的 allow 降为 Ask，在 run/子 agent 降为 Deny；有两个调用处：规则命中时 `rules.Policy.Decide` 返回 `(Ask, 命中规则)`（`pkg/agentcore/tools/rules/rules.go:162-164`），默认动作时 `approval_policy.go:198-202`（`rules.go:261-305`）。两处降级后的 Ask 都与普通 ask 不可区分。
- **其他硬限制**：危险删除 hard 目标任何入口拒绝（`internal/wiring/danger_guard.go:46-71`）；`file_remove` 拒删根与 `.git`、家目录（`native/remove.go:143-151`）；`--read-only` 使写工具不注册（`approval.go:111-115`）；hooks 的 deny 与失败一律拒绝，allow 不跳过审批。
- **写权限**：`--allow-write` 由 `run`/`chat` 共用 flag 提供（`internal/cli/run.go:260`）；gofer 需要写时手工加 `--allow-write`（`docs/usage/gofer-personal-agent.md:165`）。
- **确认类检查**：规则 ask；危险删除非 hard 目标（chat 要求输入 yes，其他入口拒绝）；外部来源防护（chat 询问一次，其他入口拒绝，`origin_guard.go`）；`memory.write_tainted: ask`（`memory_runtime.go:132-143`；SDK 下由宿主 `MemoryConfirm` 决定，nil 即拒绝）；`chat.ask_outside_dirs`（`chat_approver.go:483`）。
- **ctx 内容**：工具调用时 ctx 带 session ID、turn ID（`pkg/agentcore/api/session.go:20-37`），不带用户请求；工具执行 ctx 派生自 `Runner.Run` 的回合 ctx，chat、run、ACP、SDK 都经 Runner（`internal/personal/runner.go:495`）；注入回合以 `RunRequest.Injected` 区分（`runner.go:342`）。
- **外部来源记录**：`ExternalOrigin` 只存窗口哈希与来源名，不存原文（`pkg/agentcore/tools/origin.go`）。
- **辅助模型**：场景模型 `models.compact`/`models.idle`/`models.handoff`（`internal/config/providers.go:156-166`），`buildScenarioProvider`（`internal/wiring/runtime_build.go:76`）构建；回退与用量记录在 `personal.Runner` 内（`internal/personal/compact.go:464,494`），审批链在 Runner 之前装配。
- **读路径**：`resolveRead` 是原生读类工具的路径解析入口，调用方为 `file_read`（含图片分支与 `file_guard.go:136` 的读路径）、`file_list`、`file_grep`、`file_refs`、`file_symbols`、`view_image`，`file_find` 经遍历（`pkg/agentcore/tools/native/readroots.go:95-127` 及各工具文件）。`file_edit` 走 `resolveWrite` + `readFileInRoot`（`edit.go:79`），失败提示会回显原文行（`edit_report.go`）；`file_write` 的快照也经 `readFileInRoot`（`file.go:167`），两者都不经 `resolveRead`。内联返回模型的 `file_read` 内容不经 `redact`（`output.go:150-156`）。
- **记录出口**：拒绝记 `denial` 事件；stream-json 首行 `session`、回合结束 `input_required` 带 `denied_tools`（`internal/output/streamjson.go:25,181-192`）；审批来源 rule/user/always/user_denied（`approval.go:194-200`）。
- **前序设计**：`docs/design/2026-10-09-chat-write-approval-design.md`、`docs/design/2026-10-16-approval-rules-design.md`（替代关系见“决策”D8）。
- **规范**：SR1301、SR1403、SR1405、SR1409、GR107、GR108、GR110、SR2003、SR2004；`pkg/agentcore` 不得依赖 `internal/*`（`pkg/agentcore/layering_test.go`）。

## 总体方案

### A 审批档位与可信模式（suga-kkh4）

| 档位 | 含义 |
|---|---|
| `ask` | 确认类检查询问人；无人可问时拒绝 |
| `auto` | 原生工具默认放行；确认类检查照旧（即现有 `tools.approval: auto`） |
| `trust` | 在 `auto` 之上，把下表“放开”的确认类检查改为放行；硬限制照旧；隐含允许写 |

**设定来源**（`trust` 只能由启动方给出）：
- `chat`、`run`、`acp` 增 `--approve <ask|auto|trust>`；`--yolo` 等价于 `--approve trust`；同时出现时报错。
- 配置 `tools.approval` 仍只取 `ask|auto` 且只作用于 chat（不变）；`trust` 不能写进配置。
- SDK：`SessionOptions.ApprovalMode`（空即现状）。
- ACP：`suga acp --approve <…>` 定整个进程的档位，客户端不能修改；gofer 在 agent 配置的 args 中传入。

**入口 × 档位**：

| 入口 | 缺省 | `--approve ask` | `--approve auto` | `--approve trust` |
|---|---|---|---|---|
| chat | 按配置（ask） | ask | auto | trust |
| run | 现状（原生放行，ask 即拒绝） | 同缺省 | 同缺省 | trust |
| acp | 现状（原生与 MCP 询问客户端） | 同缺省 | 原生放行，MCP 照旧询问 | trust |
| SDK | 现状（宿主 Approver） | 同缺省 | 同缺省 | trust（只放开 suga 外层守卫） |

**`trust` 放开的确认类检查**：

| # | 检查 | chat/run/acp | SDK |
|---|---|---|---|
| 1 | 审批规则或默认动作的 ask（未被 `ClampAllow` 降级的） | 放行 | 不适用（宿主 Approver 决定） |
| 2 | MCP 工具默认 Deny/Ask | 放行 | 不适用 |
| 3 | 外部来源防护（含 B 的裁决） | 不检查 | 不检查 |
| 4 | 危险删除的非 hard 目标 | 放行 | 放行 |
| 5 | `memory.write_tainted: ask` | 放行 | 不适用（宿主 `MemoryConfirm` 决定） |

**`trust` 不放开的硬限制**：`--read-only`；保护路径 ask（chat/ACP 询问，无人可问时拒绝）；危险删除 hard 目标；`file_remove` 拒删根；hooks 的 deny；规则中的显式 deny；C 的禁读；目录范围（`read roots`、`--add-dir`，`chat.ask_outside_dirs` 照旧询问）；secret 脱敏；`DenialLimit`。
- 保护标记：`ClampAllow` 的两个调用处都返回“已降级”标记，审批器据此区分保护路径 ask 与普通 ask；宽泛 allow 规则、chat 的 always 会话规则命中保护路径时同样带标记。
- 写权限（授权扩大，见待确认事项）：chat/run 下 `trust` 隐含 `--allow-write`；与 `--read-only` 同时给出时以 `--read-only` 为准并提示。SDK 下仍以宿主 `AllowWrite` 为准；ACP 客户端选择的 `read_only` 会话模式在 `trust` 下仍生效。
- 子 agent：继承父会话档位快照；子 agent 只有只读工具、没有 MCP 工具，`trust` 对其没有实际影响。
- 可见性：chat 状态行显示 `trust`；stream-json `session` 行带 `approval_mode`；回合事件 attrs 写 `approval_mode`；被 `trust` 放行、原本会询问的调用写 `approval=trust`。

### B 外部来源裁决（suga-f5la）

**开关组合**：`tools.ask_on_external_origin: false` → 整个防护关闭（同现状）；为 `true` 时初筛照旧，命中后若 `tools.origin_judge.enabled: true`（缺省）则裁决，否则同 v0.54.8；档位 `trust` → 不检查。

**流程**：初筛命中 → 裁决 → `allow` 交给内层审批器继续；`deny` 拒绝并把理由返回模型；`ask` 时 chat 询问人（问题带理由），其他入口拒绝。超时（缺省 20 秒）、模型不可用、输出解析失败 → 按 v0.54.8 处理（chat 询问，其他入口拒绝），事件 attrs 记 `origin_judge=error`；回合已取消 → 中止回合（返回取消错误，不记为拒绝）。不缓存裁决结果，每次命中单独裁决。

**输入**（不含会话全文）：
- 可信部分：用户发起的请求原文（截断到 2 KB）。Runner 在回合开始时写入 ctx（`api.WithTurnPrompt`，`pkg/agentcore/api`）：用户回合写本回合请求；注入回合（`RunRequest.Injected`：verify 输出、后台任务提醒、nudge、跨会话消息）沿用本会话最近一次用户请求，没有时写“未知”。
- 不可信部分（整体放进标注为“不可信材料”的块）：注入回合的正文、工具名与类别、完整参数（每个字符串值截断到 1 KB，经 `redact.Secrets`）、命中片段与来源工具名。
- 提示词规则：只有当这次调用不依赖外部材料中的指令、而是用户请求本身所需时才 `allow`；拿不准就 `ask`；只输出 `{"decision":"allow|deny|ask","reason":"…"}`，其他输出视为解析失败。

**模型与装配**：新增场景 `models.judge`；未配置时依次用 `models.compact`、主模型。`internal/wiring` 在装配审批链时用 `buildScenarioProvider` 构建 judge provider（主 provider 与设置在审批链之前已就绪，`runtime.go:446`）；回退到 compact 时以延迟取值的函数注入，避免依赖 compact provider 的构建先后；连同超时注入 `originGuard`；裁决逻辑（拼装、调用、解析）在领域包 `internal/originjudge`。用量（输入/输出 token）写入该次审批事件 attrs（`origin_judge_tokens`），不计入会话预算与 `turn_usage`。

**范围**：chat、run、acp、SDK 一致；子 agent 只有只读工具，防护不检查，裁决不发生。

**记录**：事件 attrs 写 `origin_judge=allow|deny|ask|error`、`origin_judge_reason`（脱敏、截断 200 字）。

### C 敏感路径禁读（suga-4t8i）

- 配置 `tools.read_deny`（glob 列表）与内置缺省列表合并；`tools.read_deny_defaults: false` 关闭内置列表。SDK：`SessionOptions.ReadDeny []string` 追加到配置结果（不能删除内置项），其中的 `~`、`$SUGA_HOME` 在 wiring 合并时展开。
- 内置缺省列表：
  - `**/.env`、`**/.env.*`，例外 `!**/.env.example`、`!**/.env.sample`、`!**/.env.template`；
  - `~/.ssh/**`、`~/.aws/credentials`、`~/.gnupg/**`、`~/.netrc`、`~/.docker/config.json`、`~/.git-credentials`、`~/.config/gh/hosts.yml`、`~/.kube/config`、`~/.npmrc`、`~/.pypirc`；
  - `$SUGA_HOME/.env`。
- 匹配规则：
  - 模式中的 `~` 与 `$SUGA_HOME` 在 `internal/config` 加载时展开为绝对路径；以 `**/` 开头的模式匹配任意目录下的文件名；
  - 被判定的路径是解析后的绝对路径（`filepath.Clean`），对符号链接同时判定链接路径与目标路径，任一命中即禁读；
  - Windows 不区分大小写，路径分隔符统一为 `/` 后匹配；
  - 列表按顺序，`!` 开头为例外，后出现的覆盖前面的（同 gitignore 规则）。
- 作用：
  - 直接读取（`file_read` 含图片、`view_image`、`file_symbols`、`file_refs` 指定文件）：经 `resolveRead` 拒绝，错误写明“被 tools.read_deny 禁止”；
  - `file_edit`：在 `FileEditTool.Execute` 中、`readFileInRoot` 之前显式调用同一禁读判定，命中即拒绝，因此不能编辑禁读文件，也不会回显原文；
  - `file_write`：整体覆写不需要读，仍可写；写禁读文件时结果与快照差异不带旧内容；
  - 遍历（`file_list`、`file_find`）：照常列出文件名并标注“禁读”；
  - 内容搜索（`file_grep`、`file_refs` 跨文件）：跳过禁读文件，结果末尾注明跳过数。
- 任何档位（含 `trust`）都生效；对 xhen（read roots 为全盘）同样生效。
- 已知不覆盖：`shell_exec`（如 `cat .env`）。`ask`/`auto` 档 shell 仍走审批规则；`trust` 档下可经 shell 读取，文档写明。

## 架构

代码组织（参照 suga 现有分层；`pkg/agentcore` 不得依赖 `internal/*`）：

- **A**：
  - 档位类型：`pkg/agentcore/tools`（`ApprovalMode`，供 SDK 与 wiring 共用）；
  - 策略与守卫：`pkg/agentcore/tools/rules/rules.go`（`ClampAllow` 与 `Policy.Decide` 返回降级标记）、`pkg/agentcore/tools/approval.go`（审批结果带标记）、`internal/wiring/approval_policy.go`（按档位编译默认动作、区分保护路径 ask）、`origin_guard.go`、`danger_guard.go`、`chat_approver.go`、`memory_runtime.go`；
  - 参数：`internal/cli/run.go`（chat/run 共用 flag 绑定）、`internal/cli/acp.go`；ACP 档位经 `wiring.Options` 传入每个 prompt 的 `Open`（`internal/acpserver/policy.go` 按档位处理 ask）；
  - SDK：`pkg/sugasdk/types.go`（`ApprovalMode`）→ `wiring.Options`；
  - 输出：`internal/output/streamjson.go`、`internal/personal/observe.go`。
  - 偏离说明（GR108、SR2004）：审批策略与两个守卫现已位于 `internal/wiring`（前序设计确立的 owner），本设计沿用现有 owner 只增加档位分支，不新建包；把它们整体迁出 wiring 是另一项重构，不在本设计范围。
- **B**：
  - 领域包 `internal/originjudge`：`Judge(ctx, provider, Input) (Verdict, error)`，只依赖 `pkg/agentcore/api` 与 `pkg/agentcore/redact`，不依赖 `internal/personal`；
  - `internal/wiring`：构建 judge provider（`runtime_build.go`）并注入 `originGuard`；
  - 配置：`internal/config/providers.go`（`ModelSettings.Judge`）、设置注册表（`tools.origin_judge.*`）；
  - 回合请求：`pkg/agentcore/api`（`WithTurnPrompt`/`TurnPromptFrom`），`internal/personal` 在回合开始时写入。
- **C**：
  - 匹配：`pkg/agentcore/tools/native/readroots.go`（禁读判定与 `resolveRead` 同处，遍历类工具共用判定函数）；`edit.go`（`file_edit` 显式判定）、`file.go`（`file_write` 结果不带旧内容）；
  - 配置与缺省列表、展开：`internal/config`；经 native 工具环境传入的全部调用点（含子 agent）由 `internal/wiring/capabilities.go` 统一设置；
  - SDK：`SessionOptions.ReadDeny`。

## 关键流程

```text
工具调用 → Approval
  ├─ disabled（--read-only 等）→ 拒绝
  ├─ originGuard（非只读、非 AgentState）
  │    ├─ 档位 trust 或 ask_on_external_origin=false → 跳过
  │    ├─ 初筛未命中 → 继续
  │    └─ 命中 → origin_judge.enabled ? 裁决 : 现状
  │         ├─ allow → 继续
  │         ├─ deny → 拒绝（理由给模型）
  │         └─ ask / 出错 → chat 询问人；其他入口拒绝
  ├─ dangerGuard（shell_exec 删除）
  │    ├─ hard 目标 → 拒绝（任何档位）
  │    └─ 非 hard → trust 放行；chat 要求 yes；其他拒绝
  └─ 入口 Approver
       ├─ 规则 deny → 拒绝
       ├─ 保护路径 ask（两处降级均带标记）→ chat/ACP 询问；其他拒绝（任何档位）
       ├─ 其他 ask → trust 放行；chat/ACP 询问；其他拒绝
       └─ 默认动作按入口与档位

读类工具 → resolveRead（工作区 / read roots / add-dir）→ read_deny 判定 → 读取 / 拒绝 / 标注 / 跳过
file_edit → resolveWrite → read_deny 判定 → 读原文并编辑 / 拒绝
```

## 安全、数据、运维与回滚

- **SR1301**：
  - 鉴权：`trust` 只能由启动方（命令行、SDK 宿主）设定，不进配置、ACP 客户端与模型都不能修改；
  - 注入防护：裁决输入中除用户请求外全部视为不可信材料，只接受固定 JSON；出错按现状处理，不放宽；不缓存裁决；
  - 秘密：裁决输入经 `redact.Secrets`；禁读阻止凭据文件进入上下文与 trace；
  - 入参校验：`--approve` 只接受三个值，与 `--yolo` 同时出现报错；`read_deny` glob 启动时校验，非法项报错；
  - 资源归属：不适用。
- **风险与限制**：
  - `trust` 下模型可执行任意命令（受硬限制约束），只用于可信环境；
  - 裁决模型可能误判：误判 `allow` 即放过一次照抄外部指令的调用；缓解：提示词“拿不准就 ask”、输入隔离、真实模型注入用例评测（见验证）；
  - 禁读不覆盖 `shell_exec`。
- **兼容与使用方变化**：
  - 不加参数时，chat/run/acp/SDK 的审批行为与 v0.54.8 一致；
  - 默认变化一：B 默认开启。run、ACP、SDK 这些无人值守入口，从“初筛命中即拒绝”变为“裁决 allow 时放行”，这是用户选定的取舍（D7）；
  - 默认变化二：C 默认开启。xhen（全盘 read roots）与 gofer 任务中，模型读 `.env` 等会被拒；
  - SDK 的裁决模型取自 SUGA_HOME 配置（与主模型同一配置来源）；SDK 宿主的 `MemoryConfirm` 不受 `trust` 影响；
  - stream-json `session` 行新增 `approval_mode` 字段（新增字段，旧解析器忽略）；
  - 发版时向 xhen（同会话维护）与 gofer 会话说明：新参数、SDK 新字段、默认变化与关闭方法。
- **数据**：无表结构变化；裁决结果写审批事件 attrs。
- **运维**：裁决每次命中增加一次小模型调用（只在初筛命中时）。
- **回滚**：`tools.origin_judge.enabled: false`、`tools.read_deny_defaults: false` 分别关闭 B、C 的默认行为；A 不加参数即无变化；整体回滚为 revert 对应版本。
- **验证要求**（计划中落实）：
  - 单元与经真实分发的 CLI 测试（GR110）：`suga run --yolo`、`suga acp --approve`、参数冲突；
  - 入口 × 档位 × 5 项放开与硬限制的表驱动用例，含保护路径在 `trust` 下仍询问/拒绝的反向验证（默认动作、宽泛 allow 规则、chat always 会话规则三种来源）；
  - 裁决输入：注入回合不把注入正文放进可信部分；
  - ptye2e：chat `--approve trust` 状态行、外部来源裁决询问场景（假模型），用 `suga inspect trace --json` 断言 attrs，并做反向验证；
  - ACP：`scripts/acpprobe/acp_probe.py` 验证 `--approve trust` 与 `approval_mode`；
  - 裁决的真实 provider 评测：一组用例（正常引用、照抄指令、改写指令、提示词注入裁决模型），记录 allow/deny/ask 分布；
  - 禁读：各工具（含 `file_edit` 拒绝、`file_write` 结果不带旧内容）、符号链接、Windows 大小写（主机 Windows 测试）。

## 决策

- **D1**（Q1）审批档位为 `ask|auto|trust`；参数 `--approve`，`--yolo` 为 `--approve trust` 的别名；不采用 `never|auto|on|off`。
- **D2**（Q2）`trust` 只放开 A 表中 5 项确认类检查；硬限制（含保护路径 ask、规则显式 deny、目录范围）不动；chat/run 下 `trust` 隐含允许写（SDK 以宿主 `AllowWrite` 为准，ACP `read_only` 会话模式仍生效）。
- **D3** `trust` 只能由启动方设定，不进配置，ACP 客户端与模型都不能修改。
- **D4** 外部来源防护保留逐字初筛，命中后由场景模型 `models.judge` 裁决；出错按现状处理；不缓存。
- **D5** 裁决逻辑独立成领域包 `internal/originjudge`，wiring 只装配；用户请求经 ctx 传入。
- **D6** 禁读为硬限制，经 `resolveRead` 与遍历判定覆盖原生读类工具；文件名可见、内容不可读；不解析 shell。
- **D7**（Q3）外部来源裁决默认开启。
- **D8** 对前序设计的替代，均只在 `trust` 档下：
  - `2026-10-16-approval-rules-design.md`（行 68、93 与其 D8）“危险删除在 auto/always 下不绕过”→ 放行非 hard 目标；同一设计中 MCP 默认 Deny/Ask → 放行；
  - `2026-10-10-state-memory-extensibility-design.md` D7（行 264）“会话被污染后写入需确认”→ chat/run/acp 下放行（SDK 仍由宿主 `MemoryConfirm` 决定）；
  - 其余（`2026-10-09-chat-write-approval-design.md` 中 `tools.approval` 只作用于 chat、保护路径降级）保持不变。
- **D9**（Q4）禁读内置列表默认开启。

## 待确认事项

Q1–Q4 已由用户选定（2026-11-10：“Q1–Q4 按推荐”），见 D1、D2、D7、D9。评审后对 Q2 的落地有以下调整，请在批准时一并确认：
- 收窄（安全修正）：`trust` 不进配置；保护路径 ask 不放行；目录范围（`chat.ask_outside_dirs`）不放开；ACP 不做会话内切换。
- 扩大（授权）：chat/run 下 `trust` 隐含允许写（等同同时给出 `--allow-write`）；SDK 与 ACP 的写权限不受影响。

## 结论与人工计划 Gate

本设计经两轴评审并由你批准后才写实施计划；批准不授权实施。
