<!-- template_id: design; template_version: 1.2.0 -->
# suga 可信模式、外部来源裁决与敏感路径禁读 设计

> 状态：Draft 0.2 / 待人工计划批准；issue `suga-kkh4`、`suga-f5la`、`suga-4t8i`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-10 | Claude | 初稿：三项合并设计——审批档位与可信模式（kkh4）、外部来源防护“规则初筛 + 小模型裁决”（f5la）、敏感路径禁读（4t8i） |
| 0.2 | 2026-11-10 | Claude | 记录用户对 Q1–Q4 的选择（2026-11-10：“Q1–Q4 按推荐”），转为决策 D7–D10 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：
  1. 可信环境（如 gofer 在 worktree 里跑的编码任务）可以一键放开“需要人确认”类检查，不再因无人可问而整轮卡住；
  2. 外部来源防护的误报不再直接结束回合：逐字命中后由小模型判断这次调用是在执行用户任务还是在听外部内容的话；
  3. 读类工具不能把 `.env`、`~/.ssh` 这类敏感文件读进上下文与 trace。
- scope freeze：只做下文 A、B、C；不改审批规则文件格式、不改 hooks 语义、不改危险命令的判定规则本身。
- 新机制按 `DEFER_OR_REQUEST` 处理；review budget 按 SR1408。
- 暴露度（SR1409）：高。依据：suga 已公开发布（tag `v0.54.8`），gofer 以 `run`/`acp` 派活、xhen 以 SDK 嵌入；本设计改变安全边界（放开检查、引入模型裁决、拒读文件）。走两轴独立评审与 ledger，评审目录 `docs/review/2026-11-10-trust-mode-origin-judge-read-deny/`。
- 停止条件：无开放的 `CORE_BLOCKING`。

## 背景与目标

现状（证据见“已确认事实”）：
- 审批只有 `tools.approval: ask|auto` 两档，且只影响 chat 下原生工具的默认动作；`run` 无人可问，凡“需要问”的一律拒绝；同一回合被拒 3 次即结束（`DenialLimit`）。gofer 派的编码任务因此常在“本可放行”的调用上整轮停下，而 job 仍 exit 0。
- 外部来源防护（`originGuard`）按逐字窗口判定“照抄外部内容”。v0.54.7/0.54.8 已把窗口提到 40 字节、写文件不比对 URL，但规则本身两头不准：常见片段仍会误报，模型稍作改写即可绕过。
- 读类工具只有“路径须在工作区或 read roots 内”的约束；xhen 把 read roots 放开到全盘后，`.env` 中的 token 可被读入上下文与 trace（xhen gofer 对接设计 0.3 评审 S11）。

用户可见结果：
- `suga run --yolo …`（或 `--approve trust`）在可信环境下不再因“需要确认”而拒绝；任何模式下仍不会删除家目录、根目录，不会读 `.env`、`~/.ssh`。
- 下载过网页后，模型写自己的代码、提交信息不再被拦；照抄网页里的指令去执行时，裁决模型给出拒绝或询问，并说明理由。
- `file_read .env` 返回“该路径被禁止读取（tools.read_deny）”，列目录与搜索结果中不出现被禁止文件的内容。

## 名词

- **审批档位（approval mode）**：一次会话的审批严格程度，`ask` / `auto` / `trust` 三档。
- **硬限制**：任何档位都生效、不能被规则或档位放开的检查。
- **初筛**：现有的逐字窗口匹配（`ExternalOrigin.Find`/`FindIgnoringURLs`）。
- **裁决**：初筛命中后，由小模型对“这次调用是否在执行外部内容中的指令”给出 `allow`/`deny`/`ask`。
- **禁读路径**：`tools.read_deny` 列出的 glob，读类工具对其拒绝。

## 范围与非目标

范围：
- A 审批档位与可信模式：`--approve ask|auto|trust`、`--yolo`、配置 `tools.approval` 增加 `trust`；`run`、`chat`、`acp`、SDK 四个入口一致。
- B 外部来源裁决：`originGuard` 命中后调用裁决模型。
- C 敏感路径禁读：`tools.read_deny` 与内置缺省列表，作用于 `file_read`、`file_list`、`grep`、`find`。

非目标：
- 不做沙箱、网络隔离、按命令内容解析 shell（`shell_exec cat .env` 不在 C 的覆盖内，见“安全”）。
- 不把裁决扩展到所有工具调用（只在初筛命中时调用）；“只对高风险动作做检查”的收窄列为后续。
- 不改 gofer：job 在回合因拒绝结束时的退出码属 gofer，另提。
- 不改 xhen 的审批协调器；xhen 通过 SDK 新字段自行选择。

## 已确认事实与规范

- **调用链**：注册表自外向内 observe → output（`redact.Secrets`）→ visibility → originObserver → Approval → base（`internal/personal/runner.go:463-468`，`internal/wiring/runtime.go:580-594`）。审批器包装固定为 `guardExternalOrigin(guardDangerous(入口 Approver), chatApproverOf(...), origin)`（`runtime.go:591`）。
- **入口 Approver**：run 用 `newPolicyApprover`，ask 转 Denied（`approval_policy.go:152-171`）；chat 用 `newChatApprover`（`chat_approver.go:33`）；ACP 用 `permissionHook`（`internal/acpserver/policy.go:117,183`），失败或无连接按拒绝；SDK 透传宿主 Approver（`pkg/sugasdk/session.go:93-99`）。子 agent 用父策略快照、强制 run（`internal/wiring/subagent.go:122,176`）。
- **默认动作**：run 原生 Allow、MCP Deny；chat 原生 Ask（auto 时 Allow）、MCP Deny；ACP 原生 Ask、MCP Ask；只读与 AgentState 工具一律 Allow（`approval_policy.go:60-81`）。
- **既有硬限制**：
  - `ClampAllow`：对 SUGA_HOME、工作区 `.git`、`.suga`、规则文件的 allow 规则降级（`pkg/agentcore/tools/rules/rules.go:261-305`）；
  - 危险删除：hard 目标（家目录、根目录、工作区父目录、工作区外）任何入口都拒绝（`internal/wiring/danger_guard.go:46-71`，`native/danger.go`）；
  - `file_remove` 拒删工作区根、add-dir 根、`.git`、家目录（`native/remove.go:143-151`）；
  - `--read-only` 优先于一切（写工具不注册，`approval.go:111-115`）；
  - hooks 的 deny 与失败一律拒绝，allow 不跳过审批（`internal/hooks/hooks.go`）。
- **非硬的确认类检查**：审批规则中的 ask、`chat.ask_outside_dirs`（`chat_approver.go:483`）、危险删除的非 hard 目标（chat 要求输入 yes，其他入口拒绝）、外部来源防护（chat 询问一次，其他入口拒绝，`origin_guard.go:93-99`）、记忆写入 `memory.write_tainted: ask`（`memory_runtime.go:132-143`）。
- **CLI**：chat 与 run 共用 `--read-only`、`--allow-write`、`--add-dir` 等（`internal/cli/run.go:249-270`）；没有审批档位参数；`suga acp` 无参数，会话 mode 为 default/read_only（`acpserver/config_options.go:31`）。
- **辅助模型**：已有场景模型 `models.compact`/`models.idle`/`models.handoff`，经 `buildScenarioProvider`（`internal/wiring/runtime_build.go:76`）构建，`scenarioProvider` 依次回退 场景 → compact → 主模型（`internal/personal/compact.go:494`），`generateQuietly` 发非流式消费调用并记用量（`compact.go:464`，`runner.go:470-477`）。
- **读路径**：读类工具只经 `resolveRead`/`resolveRooted` 限定在工作区或 read roots（`pkg/agentcore/tools/native/readroots.go:95-127`）；无拒读列表；内联返回给模型的 `file_read` 内容不经 `redact`（`output.go:150-156`）。
- **记录**：拒绝记 `denial` 事件；stream-json `input_required` 带 `denied_tools`（`internal/output/streamjson.go:181-192`）；审批来源常量 rule/user/always/user_denied（`approval.go:194-200`）。
- **前序设计**：`docs/design/2026-10-09-chat-write-approval-design.md`、`docs/design/2026-10-16-approval-rules-design.md`。
- **规范**：SR1301、SR1403、SR1405、GR107、GR108、GR109、GR110、SR2003、SR2004；分层守卫 `pkg/agentcore/layering_test.go`（`pkg/agentcore` 不得依赖 `internal/*`）。

## 总体方案

### A 审批档位与可信模式（suga-kkh4）

三档，一个概念贯穿四个入口：

| 档位 | 含义 | 与现状关系 |
|---|---|---|
| `ask` | 需要确认的调用询问人；无人可问时拒绝 | chat 现有默认；run 的“ask 转拒绝”即此档 |
| `auto` | 原生工具默认放行；规则中的 ask、外部来源、危险删除非 hard 目标、目录外写入等确认类检查照旧 | 现有 `tools.approval: auto` |
| `trust` | 在 `auto` 之上，把所有**确认类**检查改为放行；**硬限制**照旧 | 新增 |

- 入口参数：`chat`、`run`、`acp` 增 `--approve <ask|auto|trust>`；`--yolo` 等价于 `--approve trust`。配置 `tools.approval` 取值扩为 `ask|auto|trust`。命令行优先于配置。
- run 的缺省档位保持现状语义（原生放行、无人可问即拒绝），记为 `auto`；chat 缺省 `ask`；ACP 缺省 `ask`，并新增会话配置项 `approval_mode`（ACP `set_config_option`），客户端可在会话内切换，但只能在 `--approve` 设定的上限之内（`suga acp --approve auto` 时客户端不能切到 `trust`）。
- SDK：`SessionOptions.ApprovalMode`（空为 `ask`，即现状）。`trust` 时 suga 外层的 originGuard/dangerGuard 的确认类分支放行；宿主 Approver 仍被调用（宿主自己的策略不被绕过）。
- `trust` 放开的确认类检查（逐项）：
  1. 审批规则中命中 `ask` 的调用 → 放行（规则中显式 `deny` 仍拒绝）；
  2. MCP 工具的默认 Deny/Ask → 放行；
  3. 外部来源防护 → 不检查（B 的裁决也不调用）；
  4. 危险删除的非 hard 目标 → 放行；
  5. `chat.ask_outside_dirs` → 放行（读写范围本身仍按 read roots / add-dir / `--allow-write`）；
  6. `memory.write_tainted: ask` → 放行。
- `trust` 不放开的硬限制：`--read-only`、`ClampAllow`（SUGA_HOME、`.git`、`.suga`、规则文件）、危险删除 hard 目标、`file_remove` 拒删根、hooks 的 deny、规则中的显式 deny、C 的禁读路径、secret 脱敏、`DenialLimit`。
- 可见性：chat 状态行显示 `trust`；trace 的回合事件写 `approval_mode`；stream-json 开头的 `session` 行带 `approval_mode`；ACP 会话配置项可读。被 `trust` 放行的、原本会询问的调用在事件 attrs 写 `approval=trust`。
- 子 agent 继承父会话档位（目前子 agent 只读，影响限于外部来源检查与 MCP）。

### B 外部来源裁决（suga-f5la）

- 初筛不变（40 字节窗口，写/改文件忽略 URL）。命中后：
  1. 档位为 `trust`：不检查（见 A）；
  2. 否则调用裁决模型，得到 `allow`/`deny`/`ask` 与一句理由；
  3. `allow` → 交给内层审批器继续（不再因外部来源询问）；`deny` → 拒绝并把理由返回模型；`ask` → chat 询问人（问题里带理由），其他入口拒绝。
- 裁决输入（由 suga 拼装，不含会话全文）：
  - 用户本回合的原始请求（截断到 2 KB）与会话目标（若有）；
  - 待执行调用：工具名、类别、参数摘要（每个字符串值截断到 1 KB，经 `redact.Secrets`）；
  - 命中的外部片段（前后各扩展到 512 字节）与来源工具名；
  - 外部片段放在明确标注的“不可信材料”块中，提示词要求只按固定 JSON 输出：`{"decision":"allow|deny|ask","reason":"…"}`。
- 输出解析失败、超时（缺省 20 秒）、模型不可用：按现状处理（chat 询问人，其他入口拒绝），并在事件 attrs 记 `origin_judge=error`。
- 模型：新增场景 `models.judge`，回退顺序与其他场景一致（judge → compact → 主模型）。
- 配置：`tools.origin_judge.enabled`（缺省 `true`）、`tools.origin_judge.timeout`（缺省 `20s`）。关闭时行为同 v0.54.8。
- 记录：事件 attrs 写 `origin_judge=allow|deny|ask|error`、`origin_judge_reason`（脱敏、截断）；用量按 `purpose=origin_judge` 记入。
- 同一回合内对“同一工具 + 同一命中片段”的裁决结果缓存，避免重试或模型重复调用时反复裁决。

### C 敏感路径禁读（suga-4t8i）

- 配置 `tools.read_deny`：glob 列表，`~` 展开为家目录；与内置缺省列表合并（`tools.read_deny_defaults: false` 可关闭内置列表）。
- 内置缺省列表：`**/.env`、`**/.env.*`（`.env.example`、`.env.sample`、`.env.template` 除外）、`~/.ssh/**`、`~/.aws/credentials`、`~/.gnupg/**`、`~/.netrc`、`~/.docker/config.json`、`$SUGA_HOME/.env`。
- 作用：
  - `file_read`：命中即拒绝，错误写明“被 tools.read_deny 禁止”；
  - `file_list`/`find`：照常列出文件名（名字不是机密），在条目上标注“禁读”；
  - `grep`：跳过命中文件，结果末尾注明“已跳过 N 个禁读文件”。
- SDK：`SessionOptions.ReadDeny []string`，与配置合并（只能加，不能删内置项；宿主要关闭内置项需走配置）。
- 硬限制：任何档位（含 `trust`）都生效。
- 已知不覆盖：`shell_exec` 中 `cat .env` 之类命令（不解析 shell）；在“安全”中说明，由档位与审批规则另行约束。

## 架构

代码组织（参照 suga 现有分层；`pkg/agentcore` 不得依赖 `internal/*`）：

- **A**：
  - 档位类型与解析：`internal/config`（`ApprovalMode`，`tools.approval` 取值扩展，设置注册表 `settings_registry.go`）；
  - 策略：`internal/wiring/approval_policy.go`（按档位编译默认动作与 ask 的处理）、`origin_guard.go`/`danger_guard.go`（`trust` 时确认类分支放行）、`chat_approver.go`（`ask_outside_dirs`、记忆确认）；
  - 参数：`internal/cli/run.go`（chat/run 共用 flag 绑定）、`internal/cli/acp.go`（`--approve`）、`internal/acpserver/config_options.go`（会话配置项与上限）；
  - SDK：`pkg/sugasdk/types.go`（`SessionOptions.ApprovalMode`）→ `wiring.Options`；
  - 输出：`internal/output/streamjson.go`（`session` 行）、`internal/personal/observe.go`（attrs）。
- **B**：
  - 裁决逻辑放新领域包 `internal/originjudge`（拼装输入、调用 provider、解析与缓存、超时），对外只暴露 `Judge(ctx, Input) (Verdict, error)`；
  - `internal/wiring` 只装配：构建 `models.judge` 场景 provider，注入 `originGuard`（GR108）；
  - 场景模型配置：`internal/config/providers.go`（`ModelSettings.Judge`）。
- **C**：
  - 匹配：`pkg/agentcore/tools/native/readroots.go` 增禁读判定（与 `resolveRead` 同处），`file_read`/`list`/`grep`/`find` 调用；
  - 配置与缺省列表：`internal/config`；装配经 native 工具环境传入（`internal/wiring/capabilities.go`）；
  - SDK：`SessionOptions.ReadDeny`。
- **测试**：每个入口至少一个经真实分发的 CLI 测试（GR110：`suga run --yolo`、`suga acp --approve`）；ptye2e 冒烟增 chat 中 `--approve trust` 状态行与外部来源裁决询问场景（假模型）。

## 关键流程

```text
工具调用 → Approval
  ├─ disabled（--read-only 等）→ 拒绝
  ├─ originGuard（非只读、非 AgentState）
  │    ├─ 档位 trust → 跳过
  │    ├─ 初筛未命中 → 继续
  │    └─ 命中 → 裁决（enabled 时）
  │         ├─ allow → 继续
  │         ├─ deny → 拒绝（理由给模型）
  │         └─ ask / 出错 → chat 询问人；其他入口拒绝
  ├─ dangerGuard（shell_exec 删除）
  │    ├─ hard 目标 → 拒绝（任何档位）
  │    └─ 非 hard → trust 放行；chat 要求 yes；其他拒绝
  └─ 入口 Approver（规则：deny 优先；ask → trust 放行 / chat 询问 / 其他拒绝；默认动作按档位）

读类工具 → resolveRead（工作区 / read roots）→ read_deny 判定 → 读取 / 拒绝 / 标注
```

## 安全、数据、运维与回滚

- **SR1301**：
  - 鉴权：`trust` 只能由启动方（命令行、配置、SDK 宿主、ACP 启动参数）设定；ACP 客户端只能在启动上限之内切换，模型无法改变档位；
  - 注入防护：裁决提示把外部片段作为不可信材料隔离，只接受固定 JSON；解析失败按现状处理（不放宽）；裁决模型看不到会话全文，降低被注入面；
  - 秘密：裁决输入经 `redact.Secrets`；禁读阻止 `.env` 等进入上下文与 trace；
  - 入参校验：`--approve` 只接受三个值；`read_deny` glob 启动时校验，非法项报错；
  - 资源归属：不涉及。
- **风险与限制**：
  - `trust` 下模型可执行任意命令（受硬限制约束）；仅用于可信环境，文档与状态行明示；
  - 裁决模型可能误判：`allow` 误判即放过一次注入；以 `ask` 为不确定时的推荐输出，提示词要求“拿不准就 ask”；
  - 禁读不覆盖 `shell_exec`：`ask`/`auto` 档下 shell 仍走审批规则；`trust` 档下可通过 shell 读取，这是可信模式的含义之一，文档写明。
- **数据**：无表结构变化；裁决结果写 trace 事件 attrs。
- **运维**：裁决每次命中增加一次小模型调用（通常 < 2 秒），只在初筛命中时发生；配置可关闭。
- **兼容**：不加参数时 chat/run/acp/SDK 行为与 v0.54.8 一致，默认变化只有两处：C 的内置禁读列表开启（D10）与 B 的裁决开启（D9）。
- **回滚**：`tools.origin_judge.enabled: false`、`tools.read_deny_defaults: false` 可单独关闭 B、C 的默认行为；A 不加参数即无变化；整体回滚为 revert 对应版本。

## 决策

- **D1** 审批档位为单一概念 `ask|auto|trust`，四入口一致；`--yolo` 为 `--approve trust` 的别名。
- **D2** `trust` 只放开确认类检查，硬限制列表固定（见 A）；规则中的显式 deny 仍有效。
- **D3** 外部来源防护保留逐字初筛，命中后由场景模型 `models.judge` 裁决；出错按现状处理，不放宽。
- **D4** 裁决逻辑独立成领域包 `internal/originjudge`，wiring 只装配。
- **D5** 禁读为硬限制，作用于四个读类工具，列表名可见、内容不可读；不解析 shell。
- **D6** ACP 客户端只能在启动参数给定的上限内切换档位。
- **D7**（Q1）参数为 `--approve ask|auto|trust`，`--yolo` 为 `--approve trust` 的别名；不采用 `never|auto|on|off`。
- **D8**（Q2）`trust` 只放开 A 中列出的 6 项确认类检查；规则中的显式 deny、`ClampAllow` 对 `.git`/`.suga`/SUGA_HOME/规则文件的降级等硬限制不动。
- **D9**（Q3）外部来源裁决默认开启（`tools.origin_judge.enabled: true`）。
- **D10**（Q4）禁读内置列表默认开启（`tools.read_deny_defaults: true`）。

## 待确认事项

无。Q1–Q4 已由用户选定（2026-11-10：“Q1–Q4 按推荐”），见 D7–D10。

## 结论与人工计划 Gate

本设计经两轴评审并由你批准后才写实施计划；批准不授权实施。
