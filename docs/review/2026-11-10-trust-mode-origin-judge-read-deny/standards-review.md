<!-- template_id: review; template_version: 1.2.0 -->
# suga 可信模式、外部来源裁决与敏感路径禁读 设计 评审（Standards/Governance 轴，第 2 轮 confirmation）

## 评审目标与范围

- 目标: `docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md`
- 候选四元组:
  - Git root: `/d/work/inhere/my-tools-dev/inhere-tools/suga`
  - candidate commit: `d50f631fc5fa6cf3b10c9a94d46aa2c7ec05879e`（`git rev-parse HEAD` 核对一致；设计正文最后变更于 `54b126e4`，`d50f631f` 只增评审 ledger/报告）
  - subject path: `docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md`
  - document revision: `0.3`
- 上轮候选: `113fa5f5`（0.2）；上轮本轴报告 `docs/review/2026-11-10-trust-mode-origin-judge-read-deny/standards-review.md`（S1–S11）
- 评审者: Standards/Governance 轴独立子 agent（未参与本候选起草或修订）
- 模式: `REPORT_ONLY`（未修改仓库文件、未提交；只写本文件）
- 日期: 2026-11-10
- 排除项: 用户已选定的 Q1–Q4（D1、D2、D7、D9）选择本身；Spec/Executability 轴

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md` 全文（760 行）；`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md` 全文（design-contract、design 模板、workspace 本地上下文）。
- 项目规则：`AGENTS.md` 全文。
- 候选：0.3 设计全文（268 行，`git show d50f631f:<subject>`）；0.2 的 A/B/C、架构、流程、安全、决策段（对照 diff）；上轮本轴报告全文；`spec-review.md` 的 F2、F7、F8、F11 与结论段。
- 前序设计（按关键字核对 D8）：`2026-10-09-chat-write-approval-design.md`（危险删除/always/yes）、`2026-10-16-approval-rules-design.md`（行 23、68、93、112、190、204-208、256、263、357）、`2026-10-10-state-memory-extensibility-design.md`（行 189、196、264）。
- 代码事实核对（只读）：`pkg/agentcore/tools/native/file_guard.go:120-145`、`edit.go:60-100`、`file.go:80-185`、`resolveRead`/`readFileInRoot` 全部调用点、`edit_report.go` 函数列表；`pkg/agentcore/api/provider.go:6`（`Provider` 接口位置）；`pkg/sugasdk/session.go`（经 `personal.Runner`）、`pkg/sugasdk/types.go`（`SugaHome`、`MemoryConfirm`）；`internal/wiring/runtime.go:255-275`。

方法：逐条核对上轮 S1–S11 处置；按 design-contract 必需项、GR107/GR108/GR110、SR2003/SR2004、`pkg/agentcore` 分层、SR1301、SR1409 复核 changed/dependent scope；对 0.3 新增内容先答 scope necessity；同类穷尽检查上轮发现是否再现。

## 验收标准

同第 1 轮：design-contract 齐全；代码组织合规（结构违规 `CORE_CORRECTIVE`）；SR1301 逐项可核对、`trust` 不可被模型/ACP 客户端提升；与前序设计冲突已声明；验证要求与 AGENTS.md 一致；无静默扩围；决策与待确认一致。

## 上轮处置核对表

| ID | 上轮 | 0.3 处理 | 本轮结论 |
|---|---|---|---|
| S1 [SECURITY] | CORE_BLOCKING | 名词“保护路径 ask”；A 硬限制列入；流程分出“保护路径 ask → chat/ACP 询问；其他拒绝（任何档位）”；架构要求 `rules.ClampAllow` 返回标记；验证含 trust 下保护路径反向用例 | 关闭 |
| S2 [AUTHORITY] | CORE_BLOCKING | `trust` 不进配置；`tools.approval` 仍只取 ask/auto 且只作用于 chat；ACP 由启动参数定档、客户端不能改；删去会话内切换；D3 | 关闭（`suga config set` 无法提升档位；shell 改配置只能达到 auto，属现状能力） |
| S3 [SECURITY] | CORE_BLOCKING | 可信段仅“本回合用户请求”（Runner 经 ctx 写入，SDK 也经 Runner：`pkg/sugasdk/session.go:301`）；工具名、完整参数、片段整体入不可信块；只收固定 JSON；无缓存；风险段写误判 allow；兼容段明示无人值守入口由拒绝变为可放行；评测含“提示词注入裁决模型”用例 | 关闭。残余：隔离不能消除不可信块对裁决的影响，风险段以“误判”概括，可接受（见剩余风险） |
| S4 [SECURITY] | CORE_BLOCKING | 工具名更正；覆盖 file_read（含图片/批量）、view_image、file_symbols、file_refs、file_list、file_find、file_grep、file_edit；`shell_exec` 不覆盖已披露 | 目标与各工具的结果已写明，阻断关闭；但 file_edit 的落点事实写错，见 S12（上轮未穷尽） |
| S5 结构 | CORE_CORRECTIVE | “偏离说明（GR108、SR2004）”：沿用 wiring 现有 owner、迁出不在本范围；originjudge 只依赖 `pkg/agentcore/api`（`Provider` 确在 `api/provider.go:6`）与 `redact`，不依赖 personal；用量写审批事件 attrs | 关闭 |
| S6 前序替代 | CORE_CORRECTIVE | 新增 D8 | 部分关闭：出处错配且遗漏一项，见 S13（上轮未穷尽） |
| S7 开关组合 | CORE_CORRECTIVE | B“开关组合”与流程第一分支 | 关闭 |
| S8 验证 | CORE_CORRECTIVE | GR110 CLI、表驱动含反向、ptye2e + `suga inspect trace --json` + 反向验证、acp_probe、真实 provider 评测、Windows 主机 | 关闭 |
| S9 兼容 | CORE_CORRECTIVE | 按入口列默认变化一/二；SDK 裁决模型来源；MemoryConfirm；stream-json 字段；发版通知 xhen/gofer | 关闭 |
| S10 子 agent | CORE_CORRECTIVE | 继承父档位快照，只影响 MCP；B 中子 agent 不裁决 | 关闭 |
| S11 去重/budget | CORE_CORRECTIVE | D1–D9 去重；budget “最多 8 轮…” | 关闭 |

## design-contract 必需项（changed scope 复核）

思考与范围声明、修订记录（0.3 为语义变化，递增合规，附按类自查范围）、背景与目标、名词、范围/非目标、事实与规则 ID、总体方案与代码组织、关键流程、安全/数据/运维/回滚、决策、待确认、结论与 Gate：均齐全。SR1301 五项逐项写明（资源归属“不适用”）。SR1409 暴露度判定沿用可核实事实（tag `v0.54.8`）。

## 新增内容 scope necessity

| 0.3 新增 | necessity | 说明 |
|---|---|---|
| `api.WithTurnPrompt` ctx 键 | 是 | S3/F3 关闭所需的唯一可信输入来源 |
| `rules.ClampAllow` 返回保护标记 | 是 | S1 关闭所需；收窄 |
| 入口 × 档位矩阵 | 是 | 消除实现不唯一 |
| `trust` 隐含允许写 | 是（对核心目标 1 / F8） | 但属授权扩大，Gate 披露不完整，见 S14 |
| 内置禁读列表补凭据文件 | 弱 | 同一机制的数据项，不新增 Module/Interface；可接受 |
| 匹配规则（符号链接、大小写、`!` 例外） | 是 | 禁读结果唯一性所需 |

未发现新增 Module/外部协议/Schema 之外的静默扩围；删去裁决缓存与 ACP 会话内切换为收窄。

## 发现

### S12 [MEDIUM] CORE_CORRECTIVE 上轮未穷尽（S4 同类）：file_edit/file_write 的读原文不经 `resolveRead`，设计事实与 C 的落点写错

- 证据: 设计“已确认事实·读路径”把 `file_guard` 记为“写/改前的读比对”，C 节写“`file_edit`/`file_write` 的读前比对：经 `resolveRead` 拒绝”。代码：`file_guard.go:136` 位于 `FileReadTool.readGuarded`，是 `file_read` 的读路径；`file_edit` 用 `writeTargetPath` + `readFileInRoot`（`edit.go:71,79`），`file_write` 的快照用 `readFileInRoot`（`file.go:167`），二者均不调用 `resolveRead`。`file_edit` 的错误提示（`edit_report.go` 的 `matchLines`、`closestMatchHint`）与 `editSnippet` 会把原文行带回模型。
- 影响: 按“放进 `resolveRead`”实施时 file_edit 不受禁读约束，`.env` 内容可经编辑失败提示进入上下文与 trace；C 已写明结果“file_edit 因此不能编辑禁读文件”且验证要求“各工具”，故结果唯一、不升为阻断，但 owner 落点事实错误会误导计划。
- 同类穷尽: 候选中“读原文但不经 `resolveRead`”的点为 `edit.go:79`、`file.go:167`；其余读工具均经 `resolveRead`（已列）。
- 建议: 事实改为“`file_guard.go:136` 属 file_read；file_edit/file_write 读原文经 `writeTargetPath`+`readFileInRoot`”；C 写明 file_edit 在 `writeTargetPath` 之后、`readFileInRoot` 之前调用同一禁读判定函数；file_write 不读给模型，可不禁（其快照把原文写入本地快照库，若需排除另提，不在本范围）。计划批准前修正。

### S13 [MEDIUM] CORE_CORRECTIVE 上轮未穷尽（S6 同类）：D8 出处错配，且遗漏对记忆污染确认决策的替代

- 证据: D8 称 `2026-10-09-chat-write-approval-design.md` 规定“危险删除在 auto/always 下不绕过”，但该文无危险删除相关内容（仅行 63 的 y/always 交互）；该约定实际在 `2026-10-16-approval-rules-design.md` 行 68（非目标“不改…chat 每次打 yes 的语义”）、行 93、D8（行 357）。另外 A 第 5 项 `trust` 放行 `memory.write_tainted: ask`，改写了 `2026-10-10-state-memory-extensibility-design.md` D7（行 264，“会话被污染后写入需确认”），D8 未列，却声明“其余保持不变”。（上轮 S6 的同类清单也未含后者，属上轮未穷尽。）
- 影响: 读者按 D8 无法定位被替代的现行约定，“其余保持不变”不准确。
- 建议: D8 改为：approval-rules 行 68/93/D8（危险删除非 hard 目标，trust 下放行）、approval-rules 行 204-205/256（MCP 默认，trust 下放行）、state-memory D7（污染后写入确认，trust 下在 chat/run/acp 放行，SDK 仍由宿主 MemoryConfirm）；chat-write 设计保留“`tools.approval` 只作用于 chat”不变。

### S14 [MEDIUM] CORE_CORRECTIVE [AUTHORITY 性质] `trust` 隐含允许写属于授权扩大，Gate 只把 0.3 对 Q2 的变化描述为“收窄”

- scope necessity: 是——F8 指出不带写权限时核心目标 1 的用户结果不成立。
- 证据: A“写权限：`trust` 隐含 `--allow-write`”、D2“`trust` 隐含允许写”；0.2 的 Q2 推荐项不含此条（0.2 D8 只列 6 项放开与硬限制）。待确认事项写“0.3 对 Q2 落地的收窄（目录范围不放开、保护路径 ask 不放行、`trust` 不进配置）…在批准时一并确认”，未列这项扩大；修订表也只写“F8 `trust` 隐含允许写”。SDK 下“隐含允许写”如何映射（SDK 写权限选项）未写。
- 影响: 用户按“只有收窄”确认 Q2 落地时，可能未意识到 `--yolo` 同时授予写权限（gofer run 原本需显式 `--allow-write`）。决定本身在 D2 可见且 Gate 未越过，故不判阻断。
- 建议: 待确认事项改为“0.3 对 Q2 的调整：收窄三项 + 扩大一项（trust 隐含允许写，`--read-only` 优先）”，请用户批准时一并确认；A 补一句 SDK 下的对应（或写明 SDK 不适用）。

## 覆盖缺口与限制

- 第 2 轮按合同读 diff 与依赖章节；未变章节（背景、非目标主体）沿用上轮结论。
- 前序设计按关键字核对，未全文复读；未运行测试或真实 provider，代码事实为静态阅读。
- Spec/Executability 轴（F1–F16 的关闭）不在本轴范围，只作参考。

## 结论

PASS（本轴）。上轮 CORE_BLOCKING S1–S4 均已关闭，无新的 CORE_BLOCKING。开放 CORE_CORRECTIVE 3 项：S12、S13（均为上轮未穷尽）、S14，计划批准前修正；按严谨规划核心，可在修订后做一次纠正确认轮（candidate 须变化）。

## 剩余风险与后续

- 裁决的注入抗性只能由真实 provider 评测给出证据；不可信块隔离降低但不消除被指挥的可能，run/ACP/SDK 默认由“命中即拒绝”变为“裁决 allow 可放行”（用户选定 D7），建议计划把评测结果列为 B 默认开启的发版前置条件。
- `trust` 下 `shell_exec` 可读禁读文件、可改规则文件与 `$SUGA_HOME/config.yaml`（但无法写入 `trust`）——已披露的可信模式含义。
- S14 的确认需用户在批准时给出。
