# 第 3 轮评审：Spec/Executability（纠正确认轮）

## 候选四元组
- Git root：`/d/work/inhere/my-tools-dev/inhere-tools/suga`
- commit：`900536a6fa9b1cde86a4138a3a002eb3625e07eb`
- subject：`docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md`
- revision：0.4（上轮候选 `d50f631f` / 0.3）

## 评审者身份
- 独立评审子 agent（Claude Opus 5.5），轴：Spec/Executability；只读、REPORT_ONLY，未改文件、未提交。

## 实际读取范围
- `git diff d50f631f..900536a6 -- <subject>` 全部；上轮 `docs/review/2026-11-10-trust-mode-origin-judge-read-deny/spec-review.md`（F17–F21 与核对节）。
- 代码（均取 900536a6）：`pkg/agentcore/tools/rules/rules.go:140-170,255-310`；`internal/wiring/approval_policy.go:180-250`；`internal/wiring/chat_approver.go:125-175`；`internal/wiring/runtime.go:815-860`（`ACPPolicy`）；`internal/acpserver/policy.go:100-340`（`Decide`/`decide`/session 规则）；`pkg/agentcore/tools/approval.go:140-200`；`pkg/agentcore/tools/native/edit.go:55-140`、`edit_report.go:1-160`（含 `closestMatchHint`）、`file.go:72-240`、`file_guard.go:130-140`；`internal/personal/runner.go:335-345,490-500,560-575`；D8 引用的前序设计行 68/93 与记忆设计行 264。
- 未运行测试；未读 xhen。

## 上轮处置核对表

| 上轮项 | 0.4 处理 | 核对 | 结论 |
|---|---|---|---|
| F17 HIGH | 事实改为两个调用处（`rules.go:162-164` 规则命中、`approval_policy.go:198-202` 默认动作），两处降级 Ask 均与普通 ask 不可区分；架构列 `rules.go`（`ClampAllow`/`Policy.Decide` 返回标记）+ `approval_policy.go`；A 表第 1 项改为“未被 `ClampAllow` 降级的 ask”；关键流程与验证补默认动作/宽泛 allow 规则/chat always 会话规则三种来源 | 代码一致：chat always 规则存于同一 Policy 切片（`chat_approver.go:125-127`），经 `rules.Policy.Decide` → `ClampAllow`；chat/run 路径可唯一实施 | **关闭**；ACP 侧残留见 F22、F24 |
| F18 HIGH | 事实更正：`file_edit` 走 `resolveWrite`+`readFileInRoot`（`edit.go:79`），失败提示回显原文（`closestMatchHint`）；`file_write` 快照经 `readFileInRoot`（`file.go:167`）；C 节规定在 `FileEditTool.Execute` 中 `readFileInRoot` 前显式调用同一禁读判定；架构补 `edit.go`；关键流程与验证补 `file_edit` | 行号与行为核实无误；落点唯一（`writeTargetPath`→guard.check→判定→`readFileInRoot`） | **关闭**；`file_write`“快照差异”措辞见 F23 |
| F19 MEDIUM | 可信部分只取用户发起的请求；注入回合（`RunRequest.Injected`，`runner.go:342`）沿用最近一次用户请求、无则“未知”；注入正文归不可信块；删“未经 Runner”；验证补用例 | `runner.go:342/495/569` 核实；`Injected` 已在 Run 入口可用；回退“未知”是保守值，跨进程恢复时取不到也安全 | **关闭** |
| F20 MEDIUM | 隐含写只作用于 chat/run；SDK 以 `AllowWrite` 为准；ACP `read_only` 照旧生效；D2 同步；列入待确认（授权扩大） | 与 `acp.go:73`、SDK `AllowWrite` 一致；三处表述（A 节、D2、待确认）无矛盾 | **关闭**（取舍待用户批准，不评） |
| F21 LOW | 子 agent 无 MCP、trust 无实际影响；回合取消→返回取消错误中止、不记拒绝；架构补 `approval.go`；compact 回退延迟取值；SDK `ReadDeny` 在 wiring 合并时展开 | 取消：`approval.go:156-160` 非 `ErrDenied` 错误走 `abortedError`，可实施；其余可实施。`approval.go` 的用途写成“审批结果带标记”，与实际需要不符，见 F24；ACP `auto` 的 `defaultApprovalDefaults`/`compileACPApprovalPolicy` 点名仍未写（上轮已定计划中落实） | **基本关闭** |

修订未引入与 Q1–Q4、D1–D9 的新矛盾；D8 引用行（approval-rules 行 68/93、记忆设计 D7 行 264）核实无误。

## 新发现

### F22 — MEDIUM — RECORD（既有缺陷，范围外；建议另开 issue）：ACP 的“always”会话规则不经 `ClampAllow`，可覆盖保护路径 ask
- 代码事实：ACP 的 allow_always 存在 `acpserver.sessionPolicy`，不在 wiring 的 Policy 切片里；`permissionHook.decide`（`acpserver/policy.go:301-311`）在 wiring 返回 ask（含保护路径降级的 ask）后，只要 session 规则强度 ≥ 即返回 allow。`approvalScopeKey` 对根目录文件给出 `file_write:**`（`approval_policy.go:230-240`），对 README 回答一次 always 即覆盖其后 `.git/hooks/*`、`.suga/*` 的写入。
- 与设计关系：0.4 写“chat 的 always 会话规则命中保护路径时同样带标记”，对 chat 成立；但硬限制表“保护路径 ask（chat/ACP 询问）任何档位”在 ACP 下现状已不成立，与 trust 无关，trust 也不使其变坏。
- 处置：不阻塞本设计。建议在“已确认事实·保护路径”加一句 ACP always 规则的现状并另开 issue（修法：`decide` 遇保护标记时不让 session allow 覆盖）；若纳入本设计则需用户批准扩范围。

### F23 — LOW — CORE_CORRECTIVE（措辞）：“`file_write` 写禁读文件时结果与快照差异不带旧内容”中“快照差异”无定义
- 事实：`file_write` 结果本就只有字节数与快照提示（`file.go:124-133`），不含旧内容；快照为撤销保存完整旧内容（`snapshotRequest`，`file.go:158-185`），只给人看（`:undo`/`:rewind` 预览），不进模型上下文。
- 风险：字面可被理解为“禁读文件不做快照”（使写入不可撤销）或改人侧预览，二者都不是本意。
- 修法：改为“模型可见结果不带旧内容（现状即满足，加测试锁定）；快照照常保存以供撤销”，或删去“快照差异”。

### F24 — LOW — CORE_CORRECTIVE（落点）：ACP 的保护标记传递与 `approval.go` 用途表述
- `ACPPolicy`（`runtime.go:829-836`）经 `acpserver.PolicyDecision func(...) (action, rule, scope string)` 返回字符串，标记带不出；设计又写“`acpserver/policy.go` 按档位处理 ask”。实现需二选一：扩 `PolicyDecision` 返回标记，或在 wiring 的 `ACPPolicy` 闭包内按档位把非保护 ask 转 allow。建议写明其一（若选后者，需注意 F22 的 session 覆盖顺序不变）。
- 架构把 `pkg/agentcore/tools/approval.go` 写成“审批结果带标记”；标记只在 rules/wiring 间流动，`approval.go` 实际要改的是 `approval=trust` 来源常量与 `ApprovalReport` 的 `origin_judge*` 字段（上轮 F21 所指）。建议改括号内说明。

## 结论
**PASS**（无 CORE_BLOCKING）。F17–F21 处理正确、可唯一实施（F21 有一处表述偏差并入 F24）。新发现 F22（MEDIUM，既有缺陷，记录/另开 issue）、F23、F24（LOW，计划前或计划中修措辞/落点）。

## 剩余风险
- F22 未修前，ACP 下保护路径的保证依赖用户不对根目录写入回答 always；与 trust 无关。
- 裁决模型误判与真实 provider 评测风险同上轮。
