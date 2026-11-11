# 评审报告：suga 可信模式、外部来源裁决与敏感路径禁读 设计（R2 / Spec 轴，confirmation）

## 1. 评审目标与范围

- 候选四元组：
  - Git root：`/d/work/inhere/my-tools-dev/inhere-tools/suga`
  - candidate commit：`d50f631fc5fa6cf3b10c9a94d46aa2c7ec05879e`（工作区干净）
  - subject：`docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md`
  - revision：Draft 0.3（上轮候选 `113fa5f5` / 0.2）
- 评审者：Spec/Executability 轴独立子 agent，第 2 轮，REPORT_ONLY（未修改仓库、未提交）。
- 实际读取范围：
  - 设计 0.3 全文（268 行）；上轮 `spec-review.md` 全文；`git diff --stat 113fa5f5..d50f631f`。
  - 代码：
    - `pkg/agentcore/tools/rules/rules.go` 130-305（`Policy.Decide`、`ClampAllow`、`IsProtectedPath`）
    - `internal/wiring/approval_policy.go` 55-215；`chat_approver.go` 140-200、280-295；`origin_guard.go` 60-110；`subagent.go` 50-64、160-250；`runtime.go` 370-395、560-600、665-700（含 446 行主 provider）；`runtime_build.go` 60-110
    - `internal/personal/runner.go` 495-615；`observe.go` 30-70、295-350
    - `internal/acpserver/policy.go` 20-35、100-200、285-335；`server.go`（grep OpenRuntime）；`config_options.go`（grep 模式）；`internal/cli/acp.go` 55-90；`internal/cli/run.go` 620-640；注入回合调用点 grep（`nudge.go`、`verify.go`、`contract.go`）
    - `pkg/sugasdk/types.go` 41-90、`session.go` 85-105
    - `pkg/agentcore/tools/approval.go` 200-300；`pkg/agentcore/api/session.go`、`provider.go`（grep）；`pkg/agentcore/redact`
    - `pkg/agentcore/tools/native/`：`readroots.go` 90-135、`file_guard.go` 100-145、`edit.go` 55-120、`file.go` 70-130、285-300、`edit_report.go` 1-40、`grep.go` 275-290、`catalog.go`（工具名）、各文件 `resolveRead(` 调用点 grep
- 未读：xhen 仓库；`memory_runtime.go`（沿用上轮核对）；未运行测试，均为静态阅读。

## 2. 方法

- 对上轮 F1–F16 逐条核对 0.3 的修法，并沿代码验证修法所依赖的事实与落点是否成立。
- 对 0.3 新增/改写段落（保护路径标记、入口×档位矩阵、5 项放开表、裁决输入与装配、禁读匹配与工具覆盖）检查是否能写出唯一实施计划。
- Q1–Q4 选择及 0.3 对 Q2 的收窄取舍不评，只评落地可行性。

## 3. 验收标准

- 上轮两项 CORE_BLOCKING 关闭；修法依赖的代码事实属实；
- A/B/C 各落点可实施且语义唯一；
- 无开放 CORE_BLOCKING 为 PASS。

## 4. 上轮处置核对表

| 上轮 | 0.3 处理 | 核对结论 |
|---|---|---|
| F1 裁决缓存旁路（BLOCKING） | 删去缓存（B 流程“不缓存裁决结果”、SR1301、D4） | **关闭** |
| F2 ClampAllow 的 Ask 在 trust 下（BLOCKING） | 行为写明：保护路径 ask 不放行，chat/ACP 询问、无人可问拒绝；`ClampAllow` 返回标记 | **关闭**（行为已定）；机制事实不完整，残留见 F17（上轮未穷尽） |
| F3 回合请求来源 | `api.WithTurnPrompt` 由 Runner 写入；删“会话目标” | 基本关闭：工具执行 ctx 派生自 `Runner.Run` 回合 ctx（`runner.go:565` 起），chat/run/ACP/SDK 都经 `personal.Runner.Run`，可达 originGuard。注入回合未处理，见 F19 |
| F4 片段前后扩展 | 删去 | **关闭** |
| F5 judge provider 装配与用量 | wiring 用 `buildScenarioProvider` 构建并注入；用量写审批事件 attrs，不计预算 | **关闭**（可行）：`settings` 与主 provider 在 `runtime.go:446` 已就绪，早于 `:591` 审批链；`buildScenarioProvider` 是纯函数；回退到 compact 需把 `:660` 附近的 compact 构建前移或延迟取值；attrs 经 `ApprovalReport` 加字段 + `observe.go:330-350` 写出可行（拒绝事件同样经此路径） |
| F6 ask_outside_dirs | 从放开项删除 | **关闭** |
| F7 trust 进配置 / ACP 档位 | trust 不进配置；ACP 进程级档位，经 `wiring.Options` 传入每个 prompt 的 Open | **关闭**：`internal/cli/acp.go:57-75` 的闭包可直接捕获启动参数写入 `wiring.Options`，无需改 `OpenRuntime` 签名 |
| F8 trust 与 allow-write | trust 隐含 `--allow-write`，`--read-only` 优先 | CLI 关闭；SDK 与 ACP `read_only` 会话模式未定，见 F20 |
| F9 SDK 逐项语义 | 矩阵与 SDK 列 | **关闭** |
| F10 匹配规则 | 绝对路径、符号链接两端、Windows 大小写、`!` 例外 | **关闭**（规则已定义，可实施） |
| F11 工具覆盖 | 列全工具，放进 `resolveRead` | 读类工具关闭；`file_edit` 的落点事实错误，见 F18（上轮未穷尽） |
| F12 SDK ReadDeny / `$SUGA_HOME` | 写明 `internal/config` 展开、`capabilities.go` 统一设置 | 基本关闭；SDK 传入项的 `~` 展开落点见 F21 |
| F13 表述矛盾 | 用户可见结果限定“读类工具” | **关闭** |
| F14 B 边界 | 子 agent、ACP、开关组合写明 | 基本关闭；“回合已取消”回退见 F21 |
| F15 引用与文件清单 | 更正 `newChatApprover:82`、子 agent 描述 | 部分：`pkg/agentcore/tools/approval.go` 仍未列入，见 F21 |
| F16 补凭据文件 | 已补 | 已采纳 |

## 5. 新发现

### F17 — HIGH — CORE_CORRECTIVE（SECURITY；上轮 F2 同类未穷尽）：`ClampAllow` 有两个调用处，规则命中路径产生的保护路径 Ask 带规则信息；设计事实“降级后的 Ask 不带规则命中信息”不成立

- 代码事实：
  - `rules.Policy.Decide` 在有规则命中时调用 `ClampAllow(winner.Action, …)` 并返回 `(Ask, winner)`，`winner.Action` 为 allow（`pkg/agentcore/tools/rules/rules.go:162-164`）；
  - `approvalPolicy.Decide` 遇 `hit != nil` 直接返回（`internal/wiring/approval_policy.go:194-196`），只有无命中时才在 `:201` 调 `ClampAllow`；
  - chat 的“always”答复存为 session 规则（`chat_approver.go:152-154`，`SourceSession`），同样走规则命中路径。
- 设计：“已确认事实·保护路径”写“降级后的 Ask 不带规则命中信息”；架构只写“需 `rules.ClampAllow` 返回标记”、调用处只点 `approval_policy.go`。
- 后果：若实现只在 `approval_policy.go:201` 带出标记，而把“规则命中 ask”识别为 `hit != nil && action == Ask`，则用户宽泛 allow 规则（如 `file_write` 不限路径）或 chat 的 always 会话规则命中 `.git/hooks`、`.suga`、规则文件时，被 clamp 成的 Ask 会按 A 第 1 项在 trust 下放行，违反 D2 与硬限制表。
- 为何不判 BLOCKING：设计对行为的规范性要求（D2、硬限制表、关键流程“保护路径 ask → 任何档位不放行”、验证中的反向用例）已唯一；缺的是机制事实与两个落点，计划阶段可修。但需在批准前改：
  - 更正事实：两个调用处，规则命中路径的 Ask 带 `winner`；
  - 写明标记须经 `rules.Policy.Decide` 与 `approvalPolicy.Decide` 两条路径带出（ACP 的 `PolicyDecision` 由 wiring 提供，wiring 侧按标记处理即可）；或等价地规定“trust 只放行 `hit.Action == ask` 的规则 ask”；
  - 验证用例加“显式 allow 规则 / session always 规则命中保护路径，trust 下仍询问/拒绝”。

### F18 — HIGH — CORE_CORRECTIVE（SECURITY；上轮 F11 同类未穷尽）：`file_edit` 不经 `resolveRead`，按设计落点实现时禁读不覆盖 `file_edit`，且其结果回显文件内容

- 代码事实：
  - `file_guard.go:136` 的 `resolveRead` 在 `FileReadTool.readGuarded` 内，属于 `file_read` 读路径，不是“写/改前的读比对”；写前比对 `FileGuard.check`（`file_guard.go:105-122`）只比对时间戳，不调 `resolveRead`；
  - `FileEditTool.Execute` 用 `writeTargetPath → resolveWrite` 解析路径，再 `readFileInRoot` 读全文（`edit.go:70-80`，`file.go:291-297`）；结果带编辑处前后各 2 行、最多 60 行的片段（`edit_report.go:14-17`）；`old_string` 是否命中也可当作探测。
- 设计：“已确认事实·读路径”把 `file_guard` 记为“写/改前的读比对”；C 节称“`file_edit`/`file_write` 的读前比对 经 `resolveRead` 拒绝；`file_edit` 因此不能编辑禁读文件”。
- 后果：禁读只放进 `resolveRead` 时，`file_edit .env` 仍可执行并把 `.env` 片段带进上下文与 trace，核心目标 3 对该工具不成立。
- 为何不判 BLOCKING：期望结果（`file_edit` 不能编辑禁读文件）写得明确，且验证要求“各工具”；修正只需改事实与 owner：禁读判定须在 `FileEditTool.Execute`（或 `writeTargetPath` 的 edit 调用方）显式调用共用判定函数；`file_write` 不读、不回显，保持可写与设计一致。

### F19 — MEDIUM — CORE_CORRECTIVE（SECURITY；上轮 F3 未关闭部分）：注入回合的 prompt 被当作“可信的用户请求”

- 代码事实：`Injected: true` 的回合有 `nudge.go:67`、`verify.go:201`（含验证命令输出）、`run.go:633`（后台任务提醒）、`contract.go:101`、跨会话 `session_message`（`Hops`，另一会话的模型撰写）；Runner 在 `runner.go:569` 已能区分 `req.Injected`。
- 设计 B“输入”只写“本回合用户请求原文”，未说注入回合。按字面实现，验证命令输出或另一会话模型写的文字会进入“可信部分”，而真实用户请求在更早的回合，裁决依据错位。
- 修法（不扩范围）：注入回合写入 `WithTurnPrompt` 时标“非用户输入”放进不可信块，可信部分取该会话最近一次非注入回合的请求或“未知”。另：SDK 也经 `personal.Runner.Run`（`pkg/sugasdk/session.go`），“SDK 宿主未经 Runner”的情形不存在，可删以免误导。

### F20 — MEDIUM — CORE_CORRECTIVE（AUTHORITY）：“trust 隐含允许写”在 SDK 与 ACP `read_only` 会话模式下的语义未定

- SDK：`SessionOptions.AllowWrite` 由宿主显式给（`types.go:51`，`session.go:94`）。设计只写“trust 隐含 `--allow-write`”。若在 SDK 下也隐含，宿主设 `ApprovalMode=trust, AllowWrite=false`（如只读委托）时会被暴露写与 shell 工具，越过宿主授权；与 SDK 列“只放开 suga 外层守卫”的意图冲突。
- ACP：客户端可选会话模式 `read_only`（`config_options.go:30-31,102`；`acp.go:73` `AllowWrite: !readOnly`）。设计“`--read-only` 优先”只覆盖 CLI 参数，ACP `read_only` 模式在 trust 下是否仍生效未写（自然解读是生效，但“客户端不能修改档位”易被误读为忽略）。
- 修法：写明“隐含写只作用于 chat/run；SDK 仍以 `AllowWrite` 为准；ACP 的 `read_only` 会话模式在 trust 下照旧生效”。

### F21 — LOW — CORE_CORRECTIVE：若干小误差与漏列

- 子 agent：“实际只影响第 2 项（MCP）”不成立。子 agent 注册表只含白名单 native 工具与 `extraTools`（生产为空，`subagent.go:57-60,235-245`），没有 MCP 工具，trust 对子 agent 无实际效果。
- “回合已取消 → 按 v0.54.8 处理（拒绝）”：取消应返回 ctx 错误中止回合（与 `acpserver/policy.go:179-183`、chat `asker` 出错时中止的约定一致），否则记 denial 并计入 `DenialLimit`。
- 架构漏列 `pkg/agentcore/tools/approval.go`（`ApprovalReport` 增加 `origin_judge*` 字段、`approval=trust` 的来源常量）与 `internal/personal/observe.go` 在 B 下的改动；ACP `auto` 需改 `defaultApprovalDefaults` 的 ACP 分支与 `compileACPApprovalPolicy`（现固定 `chatAuto=false`，`approval_policy.go:72-74,124-130`），文件已列但未点出。
- judge 回退到 `models.compact` 时，compact provider 现构建于审批链之后（`runtime.go:660` 附近 vs `:591`），计划需写明前移或延迟取值。
- SDK `ReadDeny` 中 `~`/`$SUGA_HOME` 的展开：SDK 项不经配置加载，需在 wiring 调用 `internal/config` 的同一展开函数。

## 6. 落地可行性核对（问询项）

- `WithTurnPrompt` → originGuard：可达。工具执行 ctx 派生自 `Runner.Run` 的回合 ctx；ACP 每个 prompt、SDK 每次 Run、chat/run 均经此；子 agent 有自己的 Runner 与 prompt（父模型撰写），但其注册表无非只读工具，guard 不触发。
- `internal/originjudge` 依赖：`api.Provider` 定义在 `pkg/agentcore/api/provider.go`，`redact.Secrets` 在 `pkg/agentcore/redact`，足够；用量从 provider 响应读取即可。
- ACP 档位传递：闭包捕获即可，无需改 `acpserver.OpenRuntime`；`acp + auto` 落在 `defaultApprovalDefaults` ACP 分支（Native→Allow，MCP 保持 Ask），保护路径经 `ClampAllow` 在 ACP 下仍为 Ask，与矩阵一致。
- 5 项放开表与矩阵：除 F17、F20 外可唯一实现。
- 禁读：读类工具全部 `resolveRead` 调用点已核对（`file_read` 三处、`file_read_image`、`view_image`、`file_list`、`file_symbols`、`file_refs`、`grep.go searchStart`、`file_guard.go:136`）；遍历逐文件判定需在 walk 回调中调用共用函数，设计已写；`file_edit` 见 F18。
- 保护路径在 run 下仍为 Deny（`ClampAllow` 非 chat/ACP 返回 Deny）、`DenialLimit`、`input_required.denied_tools` 不受影响。

## 7. 结论

**PASS**（无开放 CORE_BLOCKING）。F1、F2 关闭。建议在批准前修 F17、F18（两项 HIGH，均为修法依赖的代码事实不准，期望行为已明确）；F19、F20 可在 0.4 或计划中写明；F21 计划中落实。

## 8. 剩余风险

- F17/F18 若未修即进入计划，存在“按机制忠实实现但违背规范性要求”的风险，计划的反向验证需覆盖显式 allow 规则命中保护路径与 `file_edit .env`。
- 裁决模型误判 allow 的风险属设计已接受的取舍，依赖真实 provider 评测。
- 未读 xhen；未运行测试。
