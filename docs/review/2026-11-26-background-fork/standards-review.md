<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）设计 Standards/Governance 轴第 2 轮评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `58dbf9df`；subject path `docs/design/2026-11-26-background-fork-design.md`
- 修订版本: Draft 0.3（上一轮为 `dd7b5fd8` / Draft 0.2，本轴结论 PASS，BS1、BS2 为 `CORE_CORRECTIVE`）
- 评审者: 独立 Standards/Governance 轴评审者（Claude 子 agent，未参与撰写与修订）；REPORT_ONLY
- 日期: 2026-11-26
- 排除项: 不做全量评审。只看三件事：BS1/BS2 是否关闭；0.3 新增或调整的机制（`stop=context`、`Runner.ForkSeed`、forkHeadHook、回退收紧）能否追溯、有无多余机制；修订记录与处理表是否完整。Spec/Executability 细节（BF1–BF10 的闭合）归另一轴；用户已选定的 D1–D5 本身不评。

## 输入与方法

- 规范: IDEV-STD 1.4.1 payload `tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（review-contract 五类与 necessity proof、SR1405 删除优先、SR1411 Gate 提示、SR1103、review 模板）。
- 上轮报告: `docs/review/2026-11-26-background-fork/standards-review.md`（BS1、BS2）、`spec-review.md`（BF3、BF4、BF5、BF6、BF9，用于追溯）。
- 差异: `git diff dd7b5fd8..58dbf9df -- docs/design/2026-11-26-background-fork-design.md`；0.3 全文 `git show 58dbf9df:<subject>`（208 行）。
- 事实核对（只读，`58dbf9df`）: `internal/personal/segment_handoff_reuse.go:41-50,82-113,206-227,269-295`（`requestBaseBetweenTurns`、`rebuildDiff`）；`internal/personal/prefix.go:42,375-391` 与 `runner_finish.go:176`（`cache_prefix_print` 只在回合结束写入）；`compact.go:413`、`rewind.go:248-254`（只清 `lastRequest`，不动 `cache_prefix_print`）；`pkg/agentcore/api/hooks.go:54`（`RequestWrapUp(reason, …)` 已存在）；`internal/output/wrapup.go:21-41`（未知 stop 原因走 `default` 通用文案）；`pkg/agentcore/tools/native/agent.go:40-42`；按 `func (r *Runner) …Seed|Base|Request` grep 确认无现成同类导出方法。

## 验收标准

- **BS1 已关闭**。总体方案第 6 条、F4、架构图统一为任务 `Kind:"agent"`、摘要 `agent: ` 前缀，只用 `agent_type=fork` 与 `fork_reuse` 区分；`fork_base` 只存 turn 与尾部来源（去掉了与 `parent_session_id` 重复的主会话 id）；“已确认事实”第 82 行列出 5 处按 `Kind=="agent"` 工作的消费方并声明自动覆盖；验收的 ptye2e 加了“状态行计数”“任务行显示步数”。
- **BS2 已关闭**。D4 与总体方案第 5 条写明“仅 `type: fork`……普通后台 agent 不变”，实现点定在 `startBackground` 的分支；单测验收有“普通后台 agent 通知不变”。（第 100 行“rune”与 Q4 原文“字符”用词不同，语义由 D4 定为 rune，不构成问题。）
- **`stop=context`**：可追溯到 BF3 的建议第 3 点（“把 `stop=context` 加进 `ChildResult.Stop` 与 `AgentReport.Stop`”），0.2 的 F6 已有该值，0.3 只是落到取值说明。复用现有 `RequestWrapUp(reason)`，`WrapUpNotice` 对未知原因走通用文案，不新增机制。通过。
- **`Runner.ForkSeed`**：可追溯到 BF6 建议（“让 personal 暴露取分身基准的方法，wiring 不直接碰 tracker”）与 GR108 分层；无现成同类导出方法可复用；放在 personal，chat 与 wiring 都只调它。通过。
- **forkHeadHook**：可追溯到 0.2 的 F2 与 BF4（位置、首条消息、瞬时消息）。运行时原样发送 `event.Request`，固定请求头只能在 hook 里做，删除替代不成立；0.3 只补规格，没有新增状态。通过。
- **回退收紧**：0.2 的 F7 是“重建不了按持久化历史组装，`fallback:<reason>`”；0.3 改为“重建不可信就不启动”，删掉了第三种请求头。这是删除优先方向（SR1405），追溯到 BF5（BF5 指出第三种请求头有歧义）。机制层面没有多余，但它改变了 `:bg` 的用户可见结果，且实际触发面比设计描述的大，见 BS3；Gate 告知问题见 BS4。
- **修订记录与处理表**：0.3 行已加，处理表覆盖 BF1–BF10 与 BS1、BS2，语义变化递增版本，符合 SR1103。未列入表的改动有两处：Review budget 删去“不展开相邻架构”，属措辞，不影响结论；删除“去掉执行白名单”这条反向验证，影响安全边界的验证，见 BS5。`ForkSeed` 没在表里单独出现，但 BF6 行和总体方案第 2 条可以对上，不单列 finding。

## 发现

### BS3 [MEDIUM] F7 把 `:compact`、`:rewind`、段断点、段清理列为“重建（rebuilt）”，但按现有 `requestBaseBetweenTurns` 这些情形基本都会因 `prefix_changed` 被拒，`:bg` 在这些操作之后不可用

- disposition: CORE_CORRECTIVE；user_visible_effect: true
- 证据:
  - 设计 F7（第 174 行）：“`--resume` 后未发请求、`:compact`、段断点、`:rewind`、段清理……一律按 `requestBaseBetweenTurns` 重建（`rebuilt`）……配置了 host hook、`prompt_state` 或 `prefix_changed` 时重建不可信，分身不启动”。依据栏写的是“上下文完整优先于缓存”。
  - `requestBaseBetweenTurns`（`segment_handoff_reuse.go:206-227`）用上一回合持久化的 `cache_prefix_print` 做 `rebuildDiff`：System、Tools 必须一致，且持久化的消息窗口必须是重建视图的前缀，否则返回 `length` 或 `msg:<n>`，也就是 `prefix_changed`（`:269-295`）。
  - `cache_prefix_print` 只在回合结束写入（`prefix.go:375-391`、`runner_finish.go:176`）；`compact.go:413` 与 `rewind.go:248-254` 只清 `lastRequest`，不更新这个指纹。所以 `:compact` 之后视图变成摘要，`:rewind` 之后视图变短，与上一回合的指纹对不上，重建会返回 `prefix_changed`。按 F7，`:bg` 拒绝启动。只有 `--resume`（视图未变）大概率能走到 `rebuilt`。
- 影响: 按文档实施时，用户在 `:compact` 或 `:rewind` 后、发出下一条消息前输入 `:bg`，会收到拒绝，不会得到 `rebuilt` 分身。设计正文说这些情形是重建，与依据栏“上下文完整优先于缓存”相反：这些情形下上下文其实完整，只是缓存对不上。验收第 200 行写的是“……`:compact` 后……的 `fork_reuse` 或拒绝原因”，两种结果都能通过，挡不住这一偏差。核心验收（模型触发、正常回合后的 `prefix`）不受影响，所以不判阻断。
- 建议: 在两种写法中选定一种，并写进 F7 与对应用例：
  1. 分身不需要和上一回合的指纹一致（缓存本来就对不上），这些情形调用重建时跳过 `prefix_changed` 判定，只保留 `host_hook`、`prompt_state`，结果记 `rebuilt`。这和依据栏一致，也不新增请求头来源。
  2. 保留拒绝，把 `:compact`、`:rewind`、段断点、段清理从“重建”移到“不启动”，写明提示“先发一条消息再 :bg”，并在 Gate 告知（见 BS4）。
  无论选哪种，验收中对每种情形写定唯一的期望值，不用“或”。

### BS4 [LOW] 0.3 新增的几处拒绝与验收放宽没有在 Gate 上告知用户

- disposition: CORE_CORRECTIVE；user_visible_effect: false（属于 Gate 披露，不改变产品行为）
- 证据: 用户只做过一次决定（“hao6 Q1–Q5 按推荐”）。0.3 在用户决定之外新增或改变了这些用户可见结果：
  - F7：`:bg` 在重建不可信时不启动，取代了 0.2 的“按持久化历史组装”；
  - F6：种子估算超过阈值时不启动，提示改用 `agent explore`；
  - 真实模型验收从“目标不低于段内复用水平”改为“只记录，不设门槛”（BF9 的原建议是给出基线）。
  第 208 行的 Gate 只写“第 2 轮两轴确认……交用户批准”，没有这几项差异。SR1411 要求“候选变化时先给简短差异”。
- 影响: 用户批准计划时，可能不知道 `:bg` 有拒绝场景，也不知道缓存命中已不是验收门槛。这些都在设计范围内，不越权，所以不判 AUTHORITY。
- 建议: 交 Gate 时附 2–3 行差异：`:bg` 的拒绝场景（按 BS3 的定稿）；长会话下种子超阈值时拒绝；缓存命中只记录、不设门槛。不需要用户另做决策，除非 BS3 选第 2 种写法。

### BS5 [LOW] 删去了只读白名单的反向验证，处理表没有记录，安全边界 D3 的验证变弱

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: 0.2 的反向验证有“临时去掉执行白名单，确认‘写工具被拒’断言失败”；0.3（第 204 行）换成了“把分身拒绝改回 `ErrDenied`”，白名单那一条没有了。处理表 BF9 行（第 26 行）没有提到这次删除。0.3 的单测“连续 3 次写调用后作答……`stop=final`”断言的是报告和停止原因。去掉白名单后，写调用可能按父策略快照执行，或者失败关闭，单测未必会失败。只有 `agent`、`tool_call`、MCP 名字有提示文案断言，写工具没有。
- 影响: D3（只读、不写、不跑 shell）是本设计唯一的权限边界（安全第 1、2 条），现在没有一条能证明它生效的反向验证。这条不判阻断：执行层白名单复用现有 explore 子 agent 的机制，那部分已有覆盖。
- 建议: 恢复白名单的反向验证，与 `ErrDenied` 那条并存。在“拒绝”单测中加一项断言：写调用返回 F9 文案，目标文件没有被创建或修改。处理表 BF9 行补上这次调整。

## 覆盖缺口与限制

- BS3 的结论来自读代码（指纹只在回合结束写，compact/rewind 不更新），没有实际运行。如果 `:compact` 流程里另有路径刷新 `cache_prefix_print`，`:compact` 那一项会不成立，但 `:rewind` 视图变短仍会返回 `length`。
- 没有复核 BF1–BF10 的闭合，也没有复核 forkHeadHook 与 `cfg.Hooks`（host hook）在分身 Runner 中的先后和交互、F10 尾部展开的可执行性，这些归 Spec/Executability 轴。
- ledger 与 review-candidate-loop 的流程产物不在本轮范围内。

## 结论

PASS。BS1、BS2 已关闭，没有新的 `CORE_BLOCKING`。`stop=context`、`Runner.ForkSeed`、forkHeadHook 都能追溯到第 1 轮发现，没有多余机制；回退收紧符合删除优先。BS3（`user_visible_effect: true`）、BS4、BS5 是 `CORE_CORRECTIVE`，应在交 Gate 前修正或披露。

## 剩余风险与后续

- BS3 定稿后，相关用例要逐一给出唯一期望值：`--resume`、`:compact`、`:rewind`、段断点、取消回合、`:model` 切换。
- 计划阶段仍需检查 GR109（`fork_base` 写 meta、task output 写入不吞错）和 GR104 拆分。
