<!-- template_id: plan; template_version: 1.2.0 -->
# native 工具按需加载 实施计划

> 状态：Draft 0.1 / 待人工计划批准

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-23 | Claude（监督会话） | 初稿：按已批准设计 0.2 拆为探针先行的 6 个任务、4 个波次 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

思考模式：严谨型（rigorous）。核心目标：低频 native 工具可按需加载，段内加载后本段可用、工具表段内不变；默认关合入，过三项前置 Gate 后再切默认开。scope freeze：设计 0.2；review budget：SR1409 单轮；停止条件：完成 Gate 满足，或出现 `Semantic Amendment` / `Ownership Conflict`。

## 目标与完成定义

目标：实现 `docs/design/2026-10-23-native-deferred-tools-design.md`（Approved 0.2）。

完成定义：

1. T1 探针结论写入 `docs/research/2026-10-23-undeclared-tool-call-probe.md`，并据此确定段内调用方式（直接调用或代理工具 `tool_call`），在汇报中写明。
2. `tools.deferred_native: auto | never | [名字…]`（默认 `never`）：开启后默认名单 9 个工具（设计 §2.1）不进工具表，段上下文出现延迟清单；`tool_search` 命中或直呼会把工具记入 loaded 集。
3. 段内已加载工具可用（按 T1 结论的方式），执行、审批、hook、trace 按真实工具名；同一段内工具表字节不变。
4. 换段 / resume 后已加载工具原生入表，顺序稳定。
5. `tools.deferred_apply: segment | immediate`（默认 `segment`；MCP 维持现状 `immediate`，设计 Q5 的保守选项）。
6. 观测：`context` 事件含 `deferred_native` 计数，`tool_search` 事件含 `newly_loaded_kinds`，manifest 标出延迟工具（设计 Q7）。
7. 新增评测任务 `longsession-lowfreq-tools`（需要 `recall`/`session_search`/`memory` 之一才能完成），`--fake` 可跑。
8. 默认值仍为 `never`；“切默认开”不在本计划内，待三项前置 Gate 数据出来后单独请用户决定。

## 范围、排除项与授权

- 范围：设计 0.2 的选取、发现、加载时机、Q9 段内调用、配置键、观测、评测任务。
- 排除：子 agent registry；项目级配置层；MCP 改为 `segment`（Q5 先保守）；**切换默认值为开启**。
- 授权：用户 2026-10-23 批准设计 0.2（只授权规划）。实施需要用户批准本计划并给出执行请求。本地按任务提交为默认授权；push/tag/发版另行授权。
- `host_or_non_offline_action=NOT_APPLICABLE`

（T1 与 T6 的真实模型调用经用户已配置的 provider 发起只读请求，与以往批次相同。）

## 输入与批准证据

- 设计：`docs/design/2026-10-23-native-deferred-tools-design.md`，Approved 0.2（提交 `1a477ecb`，用户原话“批准按需加载设计 0.2”）。
- 评审：`docs/review/2026-10-23-native-deferred-tools-design-review.md`（F1 阻断已并入 0.2）。
- 依据报告：`docs/research/2026-10-23-fixed-prefix-tools-report.md`。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 延迟集选取与 loaded 集 | `pkg/agentcore/tools/visibility.go`（MCP 专用：`IsMCP` 过滤，`:156`） | 部分 | 扩大选取谓词，增加 native 名单 | `OWNER_EXTENSION` | none | owner=agentcore/tools;lifecycle=existing；与 MCP 共用一套 |
| CAP-02 | 发现（清单与搜索） | `segmentContext.DeferredTools`、`tool_search`（`pkg/agentcore/tools/native/tool_search.go`） | 是 | 清单文案补用途 | `DIRECT_REUSE` | none | owner=existing;lifecycle=existing |
| CAP-03 | 段内调用已加载工具 | `Visibility.ExecuteTool` 放行；未证实模型会调用未声明工具（评审 F1） | 否 | 视 T1 结论：直接调用，或新增常驻代理工具 `tool_call` | `OWNER_EXTENSION` | none | owner=agentcore/tools;lifecycle=existing；代理工具按真实工具名走审批 |
| CAP-04 | 配置键 | `internal/config/settings_registry.go`（已有 `tools.deferred`） | 部分 | 新增 `tools.deferred_native`、`tools.deferred_apply` | `OWNER_EXTENSION` | none | owner=internal/config;lifecycle=existing |
| CAP-05 | 评测任务 | `scripts/eval/testdata/tasks/` | 是 | 新增一个任务目录 | `DIRECT_REUSE` | none | owner=scripts/eval;lifecycle=existing |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 新的第二套 native 延迟机制 | CAP-01 | 删除测试 PASS：扩展现有 `Visibility` 即满足，第二套只会让 MCP 与 native 语义分叉 |

## 前置检查与 fail-closed 条件

运行目录均为 Git root `inhere-tools/suga`。

- 基线：分支 `main`，HEAD `1a477ecb`，工作区干净（规划时核对）。实施在 gofer worktree 分支进行。
- fail-closed：
  - `tools.deferred_native` 未设置（默认 `never`）时，工具表、段上下文、请求字节与现状**逐字节一致**（回归测试守护）。
  - MCP 延迟加载行为不变（现有测试全过）。
  - 延迟工具经代理调用时，审批、只读模式、hook、taint、trace 必须与直接调用等价（测试守护），不得出现绕过审批的路径。
  - 需要改动子 agent、ACP 协议、provider 请求格式，或改变设计决定 → `Semantic Amendment`，停止汇报。

## 波次与依赖

| 波次 | 任务 | 依赖 |
|---|---|---|
| W0 | T1 provider 探针 | 无 |
| W1 | T2 选取与配置；T3 发现清单 | T1（T2/T3 不依赖探针结论，可并行开始） |
| W2 | T4 段内调用（按 T1 结论）与段边界入表 | T1、T2 |
| W3 | T5 观测；T6 评测任务、文档与真实模型验证 | T4 |

每个任务单独提交；波次末 `GOWORK=off go test -count=1 ./internal/... ./pkg/...`。

## 任务

### T1 provider 探针：模型能否调用未声明工具

- 文件: 临时探针代码放 `tmp/`（不入库）；结论写 `docs/research/2026-10-23-undeclared-tool-call-probe.md`
- 动作: 用真实 provider（command-code 的 Claude 一个、deepseek 与 glm 各一个，按用户已有配置；隔离 SUGA_HOME，复制配置的做法同 `scripts/eval --config-from`，跑完删除）各做两项：①工具 schema 只在消息文本中给出、不在 `tools` 声明里，模型是否发出该工具的原生调用；②历史里含未声明工具的 `tool_use`/`tool_calls` 时，API 是否接受。每项至少 3 次。
- 验证: 报告里每个 provider × 每项的结果表与原始响应片段（去密钥）。
- 完成标准: 给出 Q9 选择（全部成立才“直接调用”，否则“代理工具”）。
- 依赖: 无

### T2 选取与配置

- 文件: `pkg/agentcore/tools/visibility.go`、`internal/config/settings_registry.go` 及读写处、`internal/wiring`（装配 Visibility 处，实施时 grep 定位）、对应测试
- 动作: 新增 `tools.deferred_native`（`auto`=默认 9 个名单 / `never` / 名字列表，校验名字必须是 native 工具）、`tools.deferred_apply`（`segment`/`immediate`，仅作用于 native；MCP 不变）；`Visibility` 的选取谓词扩到 native 名单。
- 验证: `GOWORK=off go test -count=1 -run 'Visibility|Deferred|Settings' ./pkg/agentcore/tools/ ./internal/config/ ./internal/wiring/`，含默认 `never` 逐字节回归与非法名字报错。
- 完成标准: 用例通过。
- 依赖: 无

### T3 发现清单

- 文件: `internal/personal/segment.go`（`DeferredTools` 文案）、`tool_search.go`（结果文本的紧凑参数清单）
- 动作: 清单一行一个“名字 — 用途”，`memory`/`recall`/`session_search` 的用途写明长会话场景（设计 Q6）；`tool_search` 结果给出紧凑参数清单（>10 参数截断）。
- 验证: `GOWORK=off go test -count=1 -run 'Segment|ToolSearch' ./internal/personal/ ./pkg/agentcore/tools/native/`。
- 完成标准: 用例通过；清单字符数在汇报中给出（设计估 ≈900）。
- 依赖: T2

### T4 段内调用与段边界入表

- 文件: `pkg/agentcore/tools/visibility.go`；若 T1 结论为代理工具，新增代理工具实现（放 `pkg/agentcore/tools/` 或 `native/`，按现有结构）及测试；`internal/personal` 段快照重建处（`freshSnapshot`）
- 动作: `segment` 模式下加载只写 loaded 集、段内工具表不变；段内调用按 T1 结论（直接调用或经 `tool_call {name, arguments}`，后者在执行层展开为真实工具调用后走同一审批/hook/trace）；换段与 resume 时 loaded 集原生入表、按名排序追加。
- 验证: `GOWORK=off go test -count=1 -run 'Visibility|Deferred|ToolCall|Segment' ./pkg/agentcore/... ./internal/personal/`：段内请求的 tools 字节不变；代理调用的审批/只读/hook/trace 与直接调用等价（含被拒绝路径）；换段后入表；resume 后首个请求含已加载工具。
- 完成标准: 用例通过；至少一个测试走真实装配路径（`internal/wiring`）。
- 依赖: T1、T2

### T5 观测

- 文件: `internal/personal`（`context` 事件、`tool_search` 事件）、manifest 生成处（实施时 grep `suga_manifest`）
- 动作: 按设计 Q7 增加字段；`suga inspect trace` 能显示。
- 验证: `GOWORK=off go test -count=1 -run 'Context|ToolSearch|Manifest' ./internal/...`。
- 完成标准: 用例通过。
- 依赖: T4

### T6 评测任务、文档与真实模型验证

- 文件: 新增 `scripts/eval/testdata/tasks/longsession-lowfreq-tools/`；`README.md`、`docs/usage/llm-config-v0.md`、`skills/suga-guide/references/{config,tools}.md`、`CHANGELOG.md`
- 动作: 新任务需要用到 `recall`/`session_search`/`memory` 之一才能完成（如早期大输出被折叠后要找回其中一个数字）；文档写清两个新配置键与默认关；真实模型：`tools.deferred_native: auto` 下跑新任务与一个难版任务各一次，记录是否正确加载与调用（隔离 home，同 T1 做法）。
- 验证: `go test ./scripts/eval/...`；`go run ./scripts/eval --fake --task longsession-lowfreq-tools`；真实模型命令与结果贴汇报；`GOWORK=off go vet ./... && GOOS=windows go vet ./...`。
- 完成标准: 以上通过或如实写明未完成。
- 依赖: T4

## 回滚与恢复

- 每个任务一个提交，按任务 `git revert`；默认 `never`，合入即无行为变化，回滚风险低。
- 恢复点：基线 `1a477ecb`；实施在独立 worktree 分支。

## 人工 Gate

1. 计划批准（本文件 0.1 → Approved）。
2. 当前执行请求。
3. 切默认开：本计划完成后，由监督用 `--repeat 3` 对照（4 个难版 + 新任务）给出数据，再单独请用户决定（设计“默认开启的前置 Gate”）。
4. 外部动作 Gate：push/tag/发版另行授权。

## 可追溯性

| 需求（设计 0.2） | 任务 | 验证 |
|---|---|---|
| Q9 / 评审 F1 段内可用 | T1、T4 | 探针报告；代理/直接调用用例 |
| Q1–Q3 选取、名单、配置 | T2 | 配置与 Visibility 用例、默认逐字节回归 |
| Q6 发现清单用途 | T3 | 清单文案用例 |
| Q4 段边界入表、resume | T4 | 段内 tools 字节不变、换段/resume 入表用例 |
| Q5 MCP 保守不变 | T2 | MCP 现有测试 |
| Q7 观测 | T5 | 事件与 manifest 用例 |
| Q8 / 评审 F2 评测任务 | T6 | 新任务 `--fake` 与真实模型 |

## 完成 Gate 与剩余工作

- T1–T6 完成并提交，验证输出贴汇报；反向验证：去掉段内调用支持 → 对应用例失败；去掉“默认 never 逐字节”保护 → 回归用例失败。
- 监督：容器 race、`make smoke-chat`、suga-verify workflow 通过后合并；随后跑 `--repeat 3` 对照，为“切默认开”的决定准备数据。
- 剩余：切默认开（人工 Gate 3）、MCP 统一到 `segment`（设计 Q5 后续）。
