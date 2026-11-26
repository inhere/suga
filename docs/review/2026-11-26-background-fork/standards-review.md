<!-- template_id: review; template_version: 1.2.0 -->
# suga 后台分身（fork）设计 Standards/Governance 轴第 1 轮评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `dd7b5fd84fdba50322368088c176ec92ed9d563d`；subject path `docs/design/2026-11-26-background-fork-design.md`
- 修订版本: Draft 0.2
- 评审者: 独立 Standards/Governance 轴评审者（Claude 子 agent，未参与该设计的撰写和修订）；REPORT_ONLY
- 日期: 2026-11-26
- 排除项: 用户已选定的 hao6 Q1–Q5（D1–D5）本身不评；Spec/Executability 轴（实现细节能否执行、流程边角）归另一轴；不评审实现代码

## 输入与方法

- 规范: IDEV-STD 1.4.1 payload `tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（全文：SR1103、SR1403/1403.1、SR1405/1405.1、SR1409 本机工具档、SR1410、SR1301、GR101–GR110、SR2001–SR2004、review-contract、planning-core-rigorous、review-candidate-loop、review 模板）；design-contract 与 design 模板（`tmp/idev-std/d/LysoVXM1D86b1PtHtpG8jg/payload.md`）。
- 项目: `AGENTS.md`（分层、验证、三项目分工）。
- 设计全文: `git show dd7b5fd8:docs/design/2026-11-26-background-fork-design.md`（182 行，第 1 轮全量读）。
- 事实核对（只读）: `git merge-base --is-ancestor bc0e9ca5 dd7b5fd8`（设计引用的事实 HEAD 是 candidate 的祖先）；`internal/personal/prefix.go:341-372`、`segment_handoff_reuse.go:90-131`、`pkg/agentcore/runtime.go:376-402`、`pkg/agentcore/tools/native/agent.go`、`internal/wiring/subagent.go:72`（explore 白名单）、`git remote -v`、`.github/workflows/release.yml`、`git tag`、`README.md:38-40`；按 `Kind == "agent"` 与 `taskKindAgent` grep 了 `internal/`、`pkg/`。

## 验收标准

- design-contract 必需内容逐项核对: 设计工作声明（模式、核心目标、scope freeze、budget、停止条件）、修订记录、背景与用户可见目标、名词、范围/非目标/受影响项目、事实与规则 ID、总体方案与架构、关键流程、安全/数据/运维/回滚、决策与未决、结论与 Gate、模板要求的“代码组织”。结论：全部齐全。
- SR1103: 修订记录含 0.1/0.2，版本/日期/作者/摘要齐全；0.2 因 Q→D 决策这一语义变化递增，符合规定。
- SR1409 暴露度：设计判为 Full。依据可核实：`origin` 为 `github.com/inhere/suga`；`release.yml` 由 `v*` tag 触发；tag 已到 v0.56.x（事实 HEAD `bc0e9ca5` 时最新为 v0.56.2，v0.56.3 在其后、candidate 之前）；README 第 38–40 行列出三种公开安装方式。公开 release 进本机工具档须附“没有已知外部用户”的事实记录，设计说明了缺这项依据，按不明处理，所以判 Full 成立。第二条理由“新增无人值守执行路径属于权限面变化”有些夸大：分身的执行白名单与现有后台 explore 子 agent 相同。这条不影响结论，不单列 finding。
- SR1403/SR1405/SR1410: 对每个新增机制逐个检查 scope necessity。基准请求加尾部、固定请求头 hook、不发 ToolChoice、回退原因、`fork_reuse` attr 都能追溯到核心目标（带全部上下文、命中缓存，并可在 trace 中观测）或用户决策 D1–D5。F1 不改投影、F4 复用 RunChild、F8 不与 `:fork N` 共享实现，都符合最小充分原则。唯一没有证明必要性的是新任务 Kind 及与已有标识重叠的 meta（BS1）。
- GR107–GR110 / SR2004: 不改入口，也不新增 CLI 子命令，GR107 与 SR2004 不适用（SR2004 只针对新 CLI）。GR108：wiring 只负责装配分支，逻辑放在 personal，依赖方向 chat → personal、`pkg/agentcore` 不依赖 internal，与 AGENTS.md 分层一致。GR104：已写明接近上限时拆分。GR109：列在适用规范中，设计层没有具体写入点；属于计划/实施的必查项，不构成设计缺陷。GR110：验收中写有 `app.Run([]string{"run", ...})` 分发测试。
- 分层与复用: 与现有子 agent（RunChild、子 Runner 工厂、父策略快照、tasks/Notify）复用合理。与会话 fork（`Runner.Fork`）明确不共享，理由成立。不改上下文投影与 `loadHistory`，前缀沿用复用了交接包的做法（H5 教训也已吸收）。
- SR1301 安全核对：鉴权——已核对（执行层只注册 explore 白名单，模型可见的全部工具定义不可执行；父策略快照、询问失败关闭、外部来源守卫均沿用）；注入——不适用（无 SQL 或命令拼接，分身禁用 shell）；秘密——已核对（新 meta 只含 id 与来源，provider 凭据沿用主会话已有配置，不新增出机器目的地）；入参校验——已核对（沿用 `agent` 工具参数解析，新增的只是一个 type 值）；资源归属——已核对（基准请求按本进程 sessionID 取，污染标记双向传递）。

## 发现

### BS1 [MEDIUM] 新任务 Kind `fork` 未证明必要，且绕开了现有按 `Kind == "agent"` 工作的消费方

- disposition: CORE_CORRECTIVE；user_visible_effect: true
- 证据: 设计第 85 行“任务 `Kind=fork`”与 F4（第 153 行）“复用 RunChild 与后台 agent 路径；会话 `kind=agent`、`agent_type=fork`”自相矛盾。现有代码中以 `"agent"` 识别后台子 agent 任务的位置（同类全部出现点）：
  - `internal/chat/render_agents.go:62`（状态行的运行中 agent 计数）
  - `internal/chat/render_agents.go:226`（agent 任务摘要行，还要求 Summary 以 `agent: ` 开头）
  - `internal/personal/trace.go:435`（`agentEvent`：trace 把任务事件归为子 agent 调用）
  - `internal/personal/trace.go:467`（`taskTraceParts`：渲染 steps/calls/exit）
  - `internal/personal/analyze.go:244`（`agentSpent`、AgentMs、BackgroundMs 汇总）
  同一类重叠标识还有：`fork_base` 中的“主会话 id”与已有 meta `parent_session_id`（`internal/personal/subagent.go:82` 写入）重复，`agent_type=fork` 已足以区分分身。
- 影响: 照文档实施时，分身不出现在 chat 状态行的运行中计数和摘要里；`inspect trace` 把它按普通任务渲染，不显示 steps/calls；analyze 的子 agent 用量与后台时长漏算分身。这些都是用户可见的输出差异。核心验收（`:tasks` 列出、通知、缓存命中、`fork_reuse`）不受直接影响，所以不判阻断。新 Kind 不能追溯到核心目标（SR1405 无证据不抽象）。
- 建议: 删除优先。任务沿用 `Kind:"agent"`，Summary 沿用 `agent: <description>` 前缀，分身只用已有的 `agent_type=fork` 与新增的 `fork_reuse` 区分；`fork_base` 只保留不能从现有 meta 得到的字段（基准请求所在 turn、尾部来源）。如果坚持新 Kind，设计须列出上述 5 处消费方及各自的处理方式。

### BS2 [MEDIUM] D4 的回交截断规则没有限定只作用于分身，可能改变现有后台 agent 的通知

- disposition: CORE_CORRECTIVE；user_visible_effect: true
- 证据: 设计第 84 行“沿用后台 agent 的 `Notify` 路径；报告不超过 4000 字符时通知带全文”，D4（第 148 行）同样没有限定对象。现有后台 agent 的通知固定取前 12 行（`pkg/agentcore/tools/native/agent.go:61` 的 `agentNoticeLines = 12` 与 `:213`）。scope freeze（第 19 行）只覆盖分身。
- 影响: 两种实现都符合字面：改共享路径会让所有后台子 agent 的通知变长（多占主会话上下文，ptye2e 与已有通知断言也会变），只改分身则不影响。用户可见结果不同，而前者超出了 scope freeze。
- 建议: 在 D4 与总体方案第 5 条写明“仅 `type: fork`；普通后台 agent 保持前 12 行”，并在单测验收中加一条“普通后台 agent 的通知不变”。

## 覆盖缺口与限制

- 仓库是否公开、有无外部用户：容器内 `gh` 未登录，未独立核实，只核对了 remote、release 流水线、tag 与 README；按 SR1409，不明即按 Full，所以不影响结论。
- 没有评审尾部 assistant 消息如何从 native 工具传到 personal 层、host hook（`cfg.Hooks`）下分身后续请求是否经过 hook 链、回退路径的具体组装方式等可执行性细节，这些归 Spec/Executability 轴。
- Full 档按 review-candidate-loop 需要 `docs/review/<subject>/review-ledger.json` 与固定轴报告；这是评审流程产物，不属于设计内容，本轮未检查。

## 结论

PASS。没有 `CORE_BLOCKING`；design-contract 必需内容齐全，暴露度 Full 判定成立且依据可核实，分层与复用合理。BS1、BS2 为 `CORE_CORRECTIVE`（均为 `user_visible_effect: true`），应在计划批准前修正；修正后可按 ledger 2.2.0 规则做一次纠正确认轮。

## 剩余风险与后续

- 修订 BS1 时，按“任务 Kind / 会话 meta 标识”这一类，自查计划中所有按 Kind 或 `agent_type` 分支的读者。
- GR109（fork_base 写 meta、task output 写入不吞错）与 GR104 拆分留给计划和实施阶段的必查项。
