<!-- template_id: review; template_version: 1.2.0 -->
# suga 可信模式、外部来源裁决与敏感路径禁读 实施计划 评审（Standards/Governance 轴，第 2 轮 confirmation）

## 评审目标与范围

- 目标: 计划 0.2 对第 1 轮 S1–S11 的处理，以及修订引入的授权、范围、结构变化
- 候选四元组:
  - Git root: `/d/work/inhere/my-tools-dev/inhere-tools/suga`
  - candidate commit: `57643ac42fd44468cce0acf0cd5e410edc11df8d`
  - subject path: `docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md`
  - document revision: `0.2`
- 上轮候选: `962b3974`（0.1）
- 评审者: Standards/Governance 轴独立子 agent（未参与计划编写与修订，与 Spec 轴隔离）
- 日期: 2026-11-10
- 模式: `REPORT_ONLY`；未修改仓库文件、未提交
- 排除项: Spec/Executability 轴；已批准设计 0.5 本身的决策

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md` 全文（760 行）；`tmp/idev-std/d/qz7PAY12agfC2q2VK2MNXw/delta-bXLJzNcvvw_ZrjUevizE3A.md` 全文（229 行）；suga `AGENTS.md` 全文。
- 上轮：`docs/review/2026-11-10-trust-mode-origin-judge-read-deny-plan/standards-review.md` 全文；`spec-review.md` F14 一节。
- 候选：`git diff 962b3974..57643ac4 -- <plan>`；因改动遍及多数章节，按需扩大为 `git show 57643ac4:<plan>` 全文（343 行）。
- 设计 0.5：按主题 grep（`config set`、gofer 说明、评测、代码组织 :194-205、回滚 :259、:21、:256）。
- 代码只读核对（57643ac4）：`internal/config/settings_registry.go`（`ConfigKey.Writable` :45、tools.* 键 :190-226，`tools.ask_on_external_origin` :222 与 `tools.approval` :205 为 Writable，非可写键现仅敏感键 :115/:118/:234）；`internal/cli/acp.go`、`run.go`（capp/cflag）；`pkg/agentcore/tools/native/catalog.go:14`（Env）；`scripts/eval/` 目录；`pkg/agentcore/layering_test.go` 依赖表；相关文件行数（runtime.go 917、runner.go 933、settings_registry.go 723、chat_approver.go 668、其余 <700）。
- 结构校验：对候选快照运行 `idevstd script validate_document --kind plan` → `ok: true, errors: []`。

## 验收标准

SR1301、SR1401、SR1403–SR1411、GR104/GR107/GR108/GR110、SR2002–SR2004；plan-closure、review-contract、review-candidate-loop；AGENTS.md「验证」节。

## 上轮处置核对

| ID | 0.2 落点 | 结论 |
|---|---|---|
| S1 smoke-chat | 命令 :184（T0/T2/T5 加 `make smoke-chat`）；T0 :246、T2 :271、T5 :301；目标 5 :66 | 已处理 |
| S2 ptye2e trace 与反向验证 | T2 :269、T5 :299 用 `suga inspect trace --json` 断言 attrs 并做场景级反向验证 | 已处理（残余：chat trust 下写 `.git` 仍询问为 chatApprover 单元自动用例而非伪终端场景；属自动化覆盖，不违反 AGENTS :33，不另开） |
| S3 T0/T1 重复判定 | T0 移到 T1 后（:220），只改 ACP；chat 只加用例（:244）；Rejected 增“chat 侧再加一次保护路径检查”（:122）。ACP 侧用 `rules.IsProtectedPath` 判目标路径（:143），与 T1 降级标记语义不同且复用同一函数，非重复实现 | 已处理 |
| S4 owner/scope | scope :43-52 补 `cli_mcp.go`、`test/ptye2e/`、`scripts/eval/originjudge/`、`scripts/acpprobe/`、`layout.go`、`i18n/catalog`；T1 owner :231 含 `approval_policy.go` 与 5 处调用点（:134-135） | 已处理 |
| S5 评测复用与列值 | CAP-08 :107（gap：现有评测跑整段会话，无法逐例控制裁决输入）；NM-01 调用方写全 :113；CAP-07 `direct_reuse=部分` :106 | 已处理 |
| S6 T0 范围差异 | 计划 Gate :327 差异说明 | 已处理 |
| S7 授权细节 | gofer 说明列入授权 :80 与 :85；评测隔离、读真实配置不复制、只存 decision 与脱敏理由 :78 | 已处理（时点见 S13） |
| S8 任务书约束 | pid 停进程 :200；返工新建 worktree :198；提示词先预跑 :202、T5 :301、T7 :308 | 已处理（supervisor 日志一句未写，LOW 残余，不另开） |
| S9 验证记录落点 | :172 写 issue 评论，原始输出 `tmp/suga-safety/`；T7 :305、:310 | 已处理 |
| S10 大文件落点 | `internal/wiring/approval_mode.go`、`internal/personal/turn_prompt.go`（:46、:48、:146、:235）；共同约束 :197 | 已处理 |
| S11 SR1301 映射与 baseline | SR1301 表 :209-212 映射 T2/T5/T6；T2 :263 config set 经分发；T5 :289 2 KB、:292 timeout；baseline :171 改为分类处理 | 已处理 |

## Plan Closure

- Capability 三表：decisions CAP-01–08、New module NM-01、Rejected 4 行；CAP-08 判 `OWNER_EXTENSION`（`scripts/eval` 下子目录，同模块）gap 成立；新增 Rejected 行有删除理由。闭合。
- `host_or_non_offline_action=REQUIRED` 恰一个（:83），子项覆盖主机 job、GOPROXY、模型 provider、gofer 说明。
- baseline :171、进度 :172、expected paths/symbols :173-182 齐全。validator 通过。

## 结构核对

- GR104：新逻辑放新文件；runtime.go/runner.go 只连线或一处调用，约束 :197 写明。通过。
- GR107：不改 `main.go`。不适用。
- GR108：裁决逻辑在 `internal/originjudge`；wiring 内档位编译放 `approval_mode.go`，沿用设计 :202 已写明的偏离（SR2004）。通过。
- GR110：`internal/cli` 经 `newCLI` 分发（:196、:263）。通过。
- SR2002：:196。SR2003：suga CLI 用 cflag/capp，非 gcli；选项绑定 `opts` 结构体、不新增包级可变变量（:196），精神一致。SR2004：同 GR108。
- `pkg/agentcore` 不依赖 internal：`ApprovalMode`（tools）、`TurnPrompt`（api）、`ReadDenyList`（native，接收已展开模式，展开在 `internal/config`/wiring）；fail-closed :188 由 `layering_test.go` 守住。通过。

## 授权核对

- gofer 说明：已列入授权与 `host_or_non_offline_action`，属设计 :256 的同一动作；但时点从“发版时”变为“T8 完成后、G1 前”，见 S13。
- 评测读真实配置不复制：与 AGENTS.md :35 acp_probe 的既有做法一致，评测记录只存 decision 与 `redact.Secrets` 理由（:78），未越过设计 :266 与用户原话。
- F14（新键不可写）：复用现有 `ConfigKey.Writable`（非新机制），改动在已在 scope 的 `settings_registry.go`，方向与设计 :21“模型无法用 `suga config set` 提升档位”一致；但设计未作此决定、Spec 轴原判 `DEFERRED_ENHANCEMENT`，且效果不完整，见 S12。不构成 AUTHORITY 阻断（无外部动作、可回退、同 owner）。

## 发现

均无 `CORE_BLOCKING`。新发现从 S12 起。

### S12 [LOW] F14 采纳为新键不可写：设计外的安全决定未在 Gate 披露，且“防止模型关闭防护”只对 C 成立 — CORE_CORRECTIVE

- scope necessity：否（A/B/C 核心验收不依赖它；删除替代 PASS，按 Spec 轴原判记 deferred 同样闭合）。因复用现有 Writable 字段、改动极小，可保留，但需按 SR1401/SR1411 交用户知情决定。
- 证据：处理表 :28 “防止模型关闭防护”；目标 3/4 :64-65；SR1301 :209；回滚 :321 改为“手工编辑配置文件”。设计 :259 回滚只写配置值，未定可写性；`settings_registry.go:222` `tools.ask_on_external_origin` 仍 Writable，模型仍可经 `suga config set` 关闭整个 B（设计 :156 该键为 false 即整体关闭）；`tools.approval` :205 可写（仅 ask/auto，不影响 trust）。同类全部出现点：:28、:64、:65、:209、:275、:287、:298、:280、:321。
- 影响：用户可见行为（`suga config set` 拒绝新键）属设计外新增；理由表述过强，读者会以为 B 已不可被模型关闭。
- 建议：二选一——(a) 保留：计划 Gate :327 差异说明加一句“新键 `tools.origin_judge.*`、`tools.read_deny*` 不可经 `suga config set` 修改（设计未定，计划新增）”，:28 理由改为“防止模型经 config set 关闭 C 与裁决；`tools.ask_on_external_origin` 仍可写，记入剩余工作”；(b) 删除该项，按 Spec 轴原判记为剩余工作。不得为此扩到改现有键的可写性（扩围）。

### S13 [LOW] gofer 接口说明时点早于发版 — CORE_CORRECTIVE

- 证据：授权 :80“T8 完成后给 gofer 监督会话发一封接口变化说明”；T8 :315-316；设计 :256 为“发版时”说明；G1 :328 在 T8 之后另行授权。
- 影响：G1 前新参数尚未安装到主机，gofer 会话按说明在 agent args 加 `--approve trust` 会对旧版本报错；与设计时点不一致。
- 建议：说明随 G1 发出（移入 G1 或写“G1 完成后发”），或说明正文注明“待发版、版本号届时补”。

## SR1301 逐项

| 项 | 结论 |
|---|---|
| 鉴权 | 已核对：trust 只来自参数与 SDK；config 写 trust 报错并经分发测试（T2 :263）；新键可写性见 S12 |
| 注入防护 | 已核对：不可信块、严格 JSON、无缓存、注入用例（T5 :289、:294-295；T7 :308） |
| 秘密 | 已核对：`redact.Secrets`、理由脱敏截断、禁读、评测不存原文、不复制 `.env`（:78、:211） |
| 入参校验 | 已核对：`--approve` 三值与 `--yolo` 冲突、timeout 正时长、glob 启动校验（:212、:292） |
| 资源归属 | 不适用 |

## 覆盖缺口与限制

- 设计只按主题 grep，未全文重读（第 1 轮已全量读，设计未变）。
- 代码行号未逐一复核（属 Spec 轴）；未运行测试。

## 结论

PASS：无 `CORE_BLOCKING`。S1–S11 全部处理（S2、S8 有可接受残余）；结构项（GR104/GR108/SR2004、分层）通过。新发现 S12、S13 均为 LOW `CORE_CORRECTIVE`，非结构项，不触发新轮；建议在计划批准前同版修正或在 Gate 披露。

## 剩余风险与后续

- `tools.ask_on_external_origin` 可被模型经 `suga config set` 关闭，B 的防护可被绕开；建议记入“剩余工作”，另立设计处理。
