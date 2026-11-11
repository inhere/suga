<!-- template_id: review; template_version: 1.2.0 -->
# suga 可信模式、外部来源裁决与敏感路径禁读 实施计划 评审（Standards/Governance 轴，第 1 轮）

## 评审目标与范围

- 目标: 实施计划 0.1 的规范与治理一致性（Plan Closure、授权与 Gate、结构规范、SR1301、验证要求、任务书约束、范围）
- 候选四元组:
  - Git root: `/d/work/inhere/my-tools-dev/inhere-tools/suga`
  - candidate commit: `962b3974f134574aaab20203718509187826b07a`
  - subject path: `docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md`
  - document revision: `0.1`
- 修订版本: 0.1（第 1 轮，全量读）
- 评审者: Standards/Governance 轴独立子 agent（未参与计划编写，不评审自己的修订）
- 日期: 2026-11-10
- 模式: `REPORT_ONLY`；未修改仓库文件、未提交
- 排除项: Spec/Executability 轴（另一轴独立评审）；设计 0.5 本身的决策（已批准）

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md` 全文（760 行）；`tmp/idev-std/d/qz7PAY12agfC2q2VK2MNXw/delta-bXLJzNcvvw_ZrjUevizE3A.md` 全文（229 行）。
- 项目规则：suga `AGENTS.md` 全文；工作区 `CLAUDE.md`、`workspace.md`（系统注入）。
- 候选：`git show 962b3974f:<plan>` 全文 293 行；设计 0.5 `git show 962b3974f:<design>` 全文 292 行；`docs/review/2026-11-10-trust-mode-origin-judge-read-deny/` 仅列目录。
- 代码核对（只读）：`rules.go:150-170`、`approval_policy.go:180-205`、`acpserver/policy.go:295-315`、`chat_approver.go:120-165,380-410`、`pkg/sugasdk/types.go:40-60`、`internal/cli/run.go:260`；`Decide/ClampAllow/IsProtectedPath` 全部调用点 grep；相关文件行数 `wc -l`；`Makefile:130-173`；`scripts/eval`、`scripts/ptysmoke`、`test/ptye2e` 目录；tag `v0.54.8`；`gofer issue show suga-flei`；`tmp/xhen-p1/{wt-repo,host-merge}.ps1` 存在性。
- 结构校验：`idevstd script validate_document --kind plan <plan>` → `ok: true, errors: []`（只读运行）。

## 验收标准

SR1301、SR1403–SR1410、GR104/GR107–GR110、SR2001–SR2004、plan-closure、review-contract、review-candidate-loop；AGENTS.md「验证」节；设计 0.5「架构/代码组织」「验证要求」。

## 已核对通过项

- `host_or_non_offline_action=REQUIRED` 恰出现一次（计划 :51），值正确（主机 job + GOPROXY + 模型 provider）。
- Capability Discovery 三表齐全；CAP-05 `MINIMAL_NEW_MODULE` 对应 NM-01，gap 论证成立（personal 调用依赖 Runner 状态；放 wiring 违反 GR108；放 `pkg/agentcore/tools` 把提示词与模型策略带入内核）；Rejected 三行有删除测试。
- 授权：推送、tag、发版、安装明确不覆盖（:50）；acp_probe 写明隔离 SUGA_HOME、读真实 config/.env 不复制（:47）；计划批准语句（:277）与 G1（:278）明确；设计批准原话与 Q1–Q4 原话引用（:57-59）。
- 暴露度高，有可核实事实（tag `v0.54.8` 存在）；两轴 + ledger。
- 结构：`pkg/agentcore` 不依赖 internal（ApprovalMode 在 `pkg/agentcore/tools`、TurnPrompt 在 `pkg/agentcore/api`、`ReadDenied` 由 env 列表判定，fail-closed :143）；GR107 不涉及 main.go；GR108 裁决逻辑独立 `internal/originjudge`，守卫留在 wiring 的偏离已在设计 :202 写明理由（SR2004）；SR2002/SR2003/GR109/GR110 写入共同约束（:150）；`run.go:260` 选项绑定 `opts` 结构体，符合 SR2003。
- 任务书约束覆盖：GIT_DIR 不 export 且不与 `go test` 同命令、容器不 `worktree add/prune`、测试用 `t.TempDir`+`t.Setenv("SUGA_HOME")`、禁止计划外等待/重试/缓存、子 agent `--sync` 不空等、禁止无关重排与删注释（:151-155）。
- 未静默扩大 A/B/C 范围；任务含具体动作与测试，非复制设计正文。

## 发现

均无 `CORE_BLOCKING`（无一满足五类 + 必要因果链）。按严重级别排序。

### S1 [MEDIUM] 终端交互改动只跑 `make smoke-go`，未按 AGENTS 跑 `make smoke-chat`；T0 无任何伪终端冒烟 — CORE_CORRECTIVE

- 证据: AGENTS.md :32 “终端交互改动（…确认交互…）必须再跑伪终端端到端冒烟：`make smoke-chat`”；`Makefile:135-160` `smoke-chat` = `smoke-go` + 仍未迁移的 Python 场景（含 `chat_approval.py`、`chat_memory.py`、`chat_pretool.py`）。同类全部出现点：计划 :139（命令只列 `make smoke-go`，且限“T2、T5 涉及 chat 时”）、T0 :186（完成标准只有 race+Windows，但 T0 改 `chat_approver.go` 的 always 确认路径）、T2 :223、T5 :252。
- 影响: T0/T2/T5 改动 chat 审批确认（always、危险删除 yes、记忆污染询问、裁决询问带理由），未迁移的 Python 审批/记忆冒烟不会运行，回归无人发现。
- 建议: T0、T2、T5 完成标准改为 `make smoke-chat`（Linux 容器），Windows ConPTY 仍以 `smoke-go` 为准；T0 加入冒烟要求。

### S2 [MEDIUM] ptye2e 新场景缺 trace 断言与反向验证；chat 交互式审批行为只用单元表驱动 — CORE_CORRECTIVE

- 证据: 设计 :264 “ptye2e：chat `--approve trust` 状态行、外部来源裁决询问场景…用 `suga inspect trace --json` 断言 attrs，并做反向验证”；AGENTS.md :33（人工审批等只在交互 chat 出现的行为用伪终端 + `inspect trace --json`，新场景做反向验证）。同类出现点：T2 :221 只断言状态行，未断言 `approval_mode`/`approval=trust` attrs；T2 反向验证 :222 与 T5 反向验证 :251 均为单元级，未覆盖 ptye2e 场景；T2 :217 “保护路径在 trust 下仍询问”与 T0 :184 “chat 同场景同样询问”均属 chat 人工审批，计划未要求伪终端场景。
- 影响: 设计验证要求未完整落实；交互审批行为仍依赖单元替身。
- 建议: T2 的 ptye2e 场景加 trace attrs 断言，并加“chat trust 下写 `.git/...` 仍询问”场景；T2、T5 各写明一次 ptye2e 反向验证（临时去掉被测逻辑，场景失败）。

### S3 [MEDIUM] T0 与 T1/T2 形成两套“保护路径”判定；chat 侧修复可能冗余且反向验证无法成立 — CORE_CORRECTIVE（结构：重复实现）

- 证据: T0 :183 在 ACP `sessionPolicy` 与 chat 会话规则“判定前先检查 `IsProtectedPath`”；T1 :192 又给 `ClampAllow`/`Policy.Decide` 加 `protected` 标记，T2 :201 扩 ACP `PolicyDecision` 返回 protected。chat 的会话规则与用户/项目规则同在一个 Policy slice（`chat_approver.go:125-127`、`:142`），赢家规则在 `rules.go:163` 已经过 `ClampAllow`，即 chat always 命中保护路径现已降为 Ask；真正绕过的只有 ACP `policy.go:307`（会话规则在 `h.policy` 之后覆盖）。`suga-flei` 描述也只说“chat 一并核对”。
- 影响: 违反 SR1403（最小充分）与“审查查重复实现”；T0 :185 “去掉新增的保护检查后两条询问用例失败”对 chat 不成立（去掉后仍由 `rules.go:163` 保护）。
- 建议: T0 的 chat 部分改为“核对 + 补反向用例（去掉 `rules.go:163` 的 ClampAllow 后失败）”，不加新检查；ACP 修复改为消费 T1 的 `protected` 标记（波次调为 T1→T0，或 T0 并入 T2 的 PolicyDecision 扩展），只保留一处判定。

### S4 [MEDIUM] owner 与 scope freeze 文件列表漏项 — CORE_CORRECTIVE

- 证据: T1 动作 :192 改 `approval_policy.go` 两个调用处，但 T1 Owner :190 未列该文件；`compiledPolicy.Decide` 包装的其他调用点 `chat_approver.go:142,277`、`runtime.go:830`、`internal/wiring/cli_mcp.go:82`（grep 结果），其中 `cli_mcp.go` 不在 scope freeze :17 列表与预期符号 :127-137；T7 新增 `scripts/eval/originjudge/`（:258）与 T2/T5 新增 `test/ptye2e/` 场景（:221、:250）不在 scope freeze（:17 只写“及各自测试”，:18 文档列表）。
- 影响: scope freeze 是显式文件清单，实施会遇到计划外路径；按 implementation-discovery 可分类，但上述均可在计划阶段预知。
- 建议: T1 Owner 补 `approval_policy.go`，并写明 wrapper 签名是否变化及受影响调用点；scope freeze 补 `internal/wiring/cli_mcp.go`、`test/ptye2e/`、`scripts/eval/originjudge/`。

### S5 [LOW] Capability Discovery 未覆盖评测驱动；表内两处不自洽 — CORE_CORRECTIVE

- 证据: 已有评测程序 `scripts/eval/`（package main：`probe.go`、`task.go`、`trace.go`、`tests.go` 等），T7 :258 新建独立 Go 驱动 `scripts/eval/originjudge/`，CAP 表无对应行；NM-01 `unique_owner_and_lifecycle` 写“由 originGuard 唯一调用”（:80），与 T7 驱动直接调用 `originjudge.Judge` 矛盾；CAP-07 `direct_reuse=是` 却判 `OWNER_EXTENSION` 且有 proven_gap（:74）。
- 影响: 评测工具不是产品 Module，不构成 AUTHORITY 阻断（删除替代：复用 `scripts/eval` 子命令同样可关闭）；但复用论证缺一行。
- 建议: 增 CAP-08（裁决评测）说明为何不复用 `scripts/eval` 现有入口或改为其子命令；NM-01 owner 改为“originGuard 与评测驱动调用”；CAP-07 `direct_reuse` 改“部分”。

### S6 [LOW] T0 纳入范围相对已批准设计是新增，计划 Gate 未单列 — CORE_CORRECTIVE

- 证据: 设计 :101 明写“既有缺陷（范围外）… 另立 issue `suga-flei` 修复；本设计的‘保护路径 ask 在 ACP 下询问’以该修复为前提”；计划 :15、:38 纳入；批准语句 :277 未提及。
- 判断: 纳入依据充分——设计硬限制（ACP 下保护路径仍询问）与验证（设计 :262 always 会话规则来源）以修复为前提，不修则 T2/T7 的 acp_probe“保护路径仍请求”验收不可靠；改动限于已在 scope 的文件。
- 建议: 计划 Gate 处按 SR1411 给一句差异：“相对设计 0.5 新增 T0（suga-flei，设计中列为范围外前提）”，由用户批准语句覆盖。

### S7 [LOW] 授权与记录边界的细节 — CORE_CORRECTIVE

- 证据与建议（同类全部出现点）：
  - T8 :265 “给 gofer 会话发接口变化说明”不在授权清单 :44-50，设计 :256 写“发版时”。建议移到 G1 或在授权清单列出。
  - 授权 :47 裁决评测“同样隔离”未写明是否读真实 `config.yaml`/`.env`、是否不复制；建议照 acp_probe 原文写全。
  - T7 :260 评测记录（每例理由）写入仓库文档与 issue，未要求确认不含真实配置或密钥（SR1301 秘密）；建议加一句“只记录用例 ID、decision、截断理由”。

### S8 [LOW] 任务书约束未覆盖的已知风险 — CORE_CORRECTIVE

- 证据: :147-155 未写：主机 acp_probe/评测 job 只按自己的 pid 停进程、收尾查 supervisor 日志；worktree 合入清理后返工用新 worktree 新派、不 resume；改发给模型的请求（裁决提示词）先 1 轮预跑查 trace 再跑全量 12 例。
- 建议: 在共同约束或 T7 中各加一句。

### S9 [LOW] T7 把“实施验证记录”放进评审目录 — CORE_CORRECTIVE

- 证据: T7 :260 写入 `docs/review/2026-11-10-trust-mode-origin-judge-read-deny-plan/`；review-candidate-loop 规定该目录的跟踪文件固定为 `review-ledger.json` 与每轴报告，“不新建按轮报告或 round-log 等汇总文件”。
- 建议: 评测结果记入 progress record（`suga-f5la` 评论已有）或项目已有的评测结果位置，不放评审目录。

### S10 [LOW] GR104：两个目标文件接近 1000 行，计划未给拆分落点 — CORE_CORRECTIVE（结构）

- 证据: `internal/wiring/runtime.go` 917 行（T2 入口档位、AllowWrite 隐含；T6 环境设置）、`internal/personal/runner.go` 933 行（T1 回合请求与最近用户请求）；计划 :151 只有通用“单文件 ≤1000 行”。
- 建议: T1、T2 写明新增逻辑放新文件（如 `internal/wiring/approval_mode.go`、`internal/personal/turn_prompt.go`），只在原文件连线。

### S11 [LOW] SR1301 落点的测试未全部映射到任务；基线描述自相矛盾 — CORE_CORRECTIVE

- 证据:
  - SR1301 :161 的“`suga config set tools.approval trust` 被拒”不在 T2 测试 :215-221（该项应经真实分发，GR110）；入参校验 :164 未含 `tools.origin_judge.timeout`、`models.judge` 校验；设计 :161 用户请求截断 2 KB 未出现在 T5 动作 :241。
  - 基线 :125 要求 `git status --short` 为空，同句又写“范围外改动记为 preserved”，两者不能同时成立。
- 建议: T2 测试补 `suga config set` 分发用例；T5 写 2 KB 截断与 timeout 校验；基线改为“范围内须干净，范围外 dirty 记为 preserved”。

## SR1301 逐项

| 项 | 结论 |
|---|---|
| 鉴权 | 已核对：trust 只来自参数与 SDK，配置拒绝，ACP 客户端与模型无切换入口；测试映射缺一处见 S11 |
| 注入防护 | 已核对：不可信块隔离、严格 JSON、出错不放宽、无缓存，注入用例与非法 JSON 用例齐 |
| 秘密 | 已核对：裁决输入 `redact.Secrets`、reason 脱敏截断、禁读、acp_probe 不复制 .env；评测记录与评测配置见 S7 |
| 入参校验 | 已核对：`--approve` 三值、与 `--yolo` 冲突、glob 校验；timeout 等缺项见 S11 |
| 资源归属 | 不适用（无多租户资源；同设计 :245） |

## 覆盖缺口与限制

- 未读设计评审报告正文与 ledger，只依赖设计中的处理表；未运行任何测试。
- 代码行号只抽查了与发现相关的位置，其余预期符号行号未逐一核对（属 Spec 轴）。

## 结论

PASS：无 `CORE_BLOCKING`。11 项 `CORE_CORRECTIVE`，其中 S3（重复实现）、S10（GR104）属于结构项，按评审合同须在计划批准前修正；其余建议同版修正。

## 剩余风险与后续

- S1/S2 不修会让交互审批回归漏检，属于本次安全边界改动的主要验证风险。
- 修订后按纠正确认轮只读 `git diff 962b3974f..<新 candidate> -- <plan>` 与依赖章节。
