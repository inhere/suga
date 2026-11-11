# 第 3 轮评审（纠正确认轮）· Standards/Governance 轴

## 候选四元组
- Git root：/d/work/inhere/my-tools-dev/inhere-tools/suga
- commit：900536a6fa9b1cde86a4138a3a002eb3625e07eb
- subject：docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md
- revision：0.4（上轮候选 d50f631f / 0.3）

## 评审者身份
独立评审子 agent（Claude Opus 5.5），Standards/Governance 轴，REPORT_ONLY，只读，未改文件、未提交。

## 实际读取范围
- `git diff d50f631f..900536a6 -- <subject>` 全部改动；900536a6 下 standards-review.md 的 S4/S6/S12–S14 及结论。
- 代码核对（900536a6）：native/edit.go:70-79、file.go:160-170,291-297、readroots.go:102、rules/rules.go:155-168、personal/runner.go:340-344,495、sugasdk/session.go:94 与 types.go:51-52、acpserver 的 `read_only` 会话模式。
- 前序设计：approval-rules 行 68、93、190-191、357；state-memory 行 264（D7）；chat-write 行 59 及全文检索（无危险删除、无保护路径内容）。
- 规范：payload.md 的 finding disposition / CORE_BLOCKING 五类条款（行 152、497-518），delta 文件。
- 未读：spec-review.md、review-ledger.json 的 0.4 改动（不在本轴范围）；Q1–Q4 选择本身不评。

## 上轮处置核对表

| 上轮 | 处理（0.4） | 核对 | 结论 |
|---|---|---|---|
| S12 file_edit 落点 | 事实更正：file_edit 走 `resolveWrite`+`readFileInRoot`（edit.go:79），`file_guard.go:136` 归 file_read；C 写明 `FileEditTool.Execute` 在 `readFileInRoot` 前显式判定、不回显原文；file_write 仍可写且结果不带旧内容；架构补 edit.go/file.go；流程图与验证补用例 | `writeTargetPath`（file.go:291）即调用 `resolveWrite`（readroots.go:102），表述成立；edit.go:79、file.go:167 与代码一致 | 关闭 |
| S13 D8 出处 | 危险删除改指 approval-rules 行 68、93 与其 D8（行 357，核实）；MCP 默认归同一设计；补列 state-memory D7（行 264，核实），并写明 SDK 仍由宿主 `MemoryConfirm` 决定 | 出处与补列均正确；剩余括注的归属有小瑕疵，见 S15 | 关闭 |
| S14 授权扩大 | 待确认事项拆为“收窄 / 扩大（授权）”两类，扩大项写明 chat/run 下 `trust` 等同 `--allow-write`；A 写权限条目与 D2 写明 SDK 以宿主 `AllowWrite` 为准（session.go:94 核实）、ACP `read_only` 会话模式仍生效 | Gate 仍为“批准不授权实施”，未越权；SDK/ACP 对应已写清 | 关闭 |

## 新发现

### S15 [LOW] DEFERRED_ENHANCEMENT D8 末项括注把“保护路径降级”归到 chat-write 设计
- 证据：D8 第三项“其余（`2026-10-09-chat-write-approval-design.md` 中 `tools.approval` 只作用于 chat、保护路径降级）保持不变”。chat-write 设计只有 `tools.approval` 只作用于 chat（行 59），没有保护路径内容；保护路径降级出自 `2026-10-16-approval-rules-design.md` 行 190-191。
- 影响：只影响出处定位，不改变任何决策或行为；不阻断。
- 建议：改为“chat-write 设计的 `tools.approval` 只作用于 chat、approval-rules 设计的保护路径降级保持不变”。可在下次修订时顺手更正。

### 修订引入项检查（无发现）
- 越权：Gate 仍为“经两轴评审并由你批准后才写实施计划；批准不授权实施”；扩大项已交用户确认，没有自行认定。
- 扩围：新增改动点（rules.go/approval.go 带降级标记、edit.go 显式判定、file.go 结果不带旧内容、SDK `ReadDeny` 展开 `~`、注入回合处理）都用于落实既定目标 A/B/C 与上轮发现，没有新增目标。`pkg/agentcore/tools` 与 `rules` 带标记不引入对 `internal/*` 的依赖，符合 layering 约束。
- 结构：0.4 修订记录与处理表齐全，决策、待确认事项、架构、验证四处对上轮项的改动一致。

## 判定
PASS（本轴）。S12–S14 都已关闭；无 CORE_BLOCKING，也无开放的 CORE_CORRECTIVE；新增 S15 [LOW] DEFERRED_ENHANCEMENT（出处括注），不阻断批准。
