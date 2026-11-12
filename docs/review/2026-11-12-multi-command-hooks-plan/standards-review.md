<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令 实施计划 第 2 轮 Standards/Governance 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `3ef5f642`（`3ef5f64239958d36404022be9381688565c46105`）；subject path `docs/plans/2026-11-12-multi-command-hooks-plan.md`；上轮 candidate `1b747836`（Draft 0.1，本轴 PASS）
- 修订版本: Draft 0.2
- 评审者: 独立 Standards/Governance 轴评审者（Claude 子 agent，未参与计划撰写，未读本轮 Spec/Executability 轴报告）；模式 `REPORT_ONLY`，第 2 轮 confirmation
- 日期: 2026-11-12
- 排除项: Spec/Executability 轴的状态机正确性、竞态闭合、测试能否跑通（只在影响治理判断时引用）；设计 0.6 本身（已批准）；不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md` 全文（router、文档、AI 协作及细则、Go 规则、inhere 规则包 SR2001–SR2004、doc-workflow、document-routing、gates、plan-contract、planning-core-rigorous、plan-closure、plan 模板 1.3.1）；`tmp/idev-std/d/8Yz1eltnU9GuyZy8zAtfgA/payload.md` 中 review-contract 与 review 模板 1.2.0。
- 被评对象：`git show 3ef5f642:<subject>` 全文（290 行）与 `git diff 1b747836 3ef5f642 -- <subject>`。
- 依据：设计 0.6（`git show 3ef5f642:docs/design/2026-11-12-multi-command-hooks-design.md`，重点 `:235`、`:248-259`、`:286-304`、`:339`）；`AGENTS.md`；上轮本轴报告 `docs/review/2026-11-12-multi-command-hooks-plan/standards-review.md`（HPS1–HPS11）。
- 事实核对（只读）：`idevstd script validate_document --kind plan`（对 candidate 导出副本，结果 `ok: true, errors: []`）；`host_or_non_offline_action=` 计数 1；`internal/cli/config.go`（candidate 与当前 main）；`tmp/xhen-p1/host-merge.ps1` 全文；`internal/hooks/hooks.go:126-128,254`（`backgroundStopTimeout`）；`hooks_env_test.go:19`（`TestFakeHookEnvProcess`）；gofer `internal/commands/hook.go:158,282-286`；当前 main 上 hyj7 产物（`TestProtectedClassification` `settings_protected_test.go:297`、README `:101` *exec* 清单、usage `:140` 执行类清单）；`git log 3ef5f642..main`、`git merge-base --is-ancestor feat/protected-config-keys main`；`gofer issue show suga-hyj7`。
- 协调者补充（已采纳）：hyj7 已合入本地 main（`7905a0f4`），`.worktrees/hyj7` 与分支已删除；“前置：hyj7 已合入”已成立，不作为发现。

## 验收标准

- plan-contract / plan-closure / plan 模板 1.3.1；review-contract（五类 `CORE_BLOCKING` + necessity proof、同类穷尽、结构违规按 `CORE_CORRECTIVE`）。
- SR1107、SR1137、SR1301、SR1402.1、SR1403/SR1403.1、SR1405、SR1407、SR1409、SR1411、SR1412、SR1432；GR104、GR107–GR110；SR2002、SR2004；AGENTS.md“验证”“项目编码规范”。

### HPS1–HPS11 关闭核对

| ID | 结论 | 证据（0.2 行号） |
|---|---|---|
| HPS1 | 基本关闭；第 5 子项后半（“打断时未启动的条目不记”）仍无断言，见下文 HPS1 残项 | T3 兼容用例 `:159`；T6 后台正例 `:206`；T5 Notice `:185`；T7 entry/program 与无 Interrupt `:217`；R0 替代说明 `:207`、`:218` |
| HPS2 | 关闭 | 可追溯表补 D8 `:264`、HF12 `:272`、HF19 `:273`、HF20 `:274`、真实边界 `:282`、IDEV-STD 消息 `:283`、合并 `:284`；完成定义 6 提及消息 `:54` |
| HPS3 | 关闭 | T9 真实环境 `:236`，`UNVERIFIED_REAL_ENV` 行含复现命令；完成定义 5 `:53`。凭据处理细节见 HPS13 |
| HPS4 | 关闭 | 执行者由 Gate 1 指定、不继承 hyj7 单次授权、T5 按 SR1432 建议更强执行者 `:103`；任务书补暂存核对、禁 stash/reset/checkout、提交节奏 `:104` |
| HPS5 | 关闭 | 授权 `:60`、Gate 2 `:252` 写明建分支与合并到本地 main，不含 push |
| HPS6 | 部分关闭：Gate 2 与 T1 口径已统一（`:135`、`:252`）；判定式改用分支名，见下文 HPS6 残项 | `:100` |
| HPS7 | 关闭 | T3 文件含 `pipeline.go` 骨架 `:152`；T1 分支②记入 issue `:131`；T4 `checkpoint` `:169` |
| HPS8 | 关闭 | CAP-05 `:81`，validator 通过 |
| HPS9 | 关闭 | Windows `go test ./...` `:235`；容器只跑 `make smoke-chat` `:234` |
| HPS10 | 关闭 | gofer plan/todo `:101`；retrospective `:289` |
| HPS11 | 关闭 | 消息要点（形状、整体替换、protected 需终端、版本）与发送时机理由 `:238`；Gate 4 `:254` |

### 0.2 新增内容的治理核对（无发现者）

| 项 | 结论 | 证据 |
|---|---|---|
| 思考与范围声明 | 符合；scope freeze 新增 `internal/cli/config.go` 一处，追溯到设计 §5 `:235`“`config get hooks.commands` 输出 YAML”，不超出设计 | `:39-44` |
| 修订记录 / 状态 / 模板首行 | 符合：0.2 有语义变化，递增正确；第 1 轮处置表逐项登记 | `:1`、`:4`、`:8-37` |
| Capability Discovery | 结构符合（validator 通过）；CAP-05 `OWNER_EXTENSION` 与设计 D1/架构一致；`NONE` sentinel 保留正确（无新 Module）。CAP-01 证据位置不准见 HPS14 | `:73-95` |
| `host_or_non_offline_action` | 恰一个 `REQUIRED`，补列 T9 真实模型调用、gofer server 临时会话记录 | `:61` |
| 授权与 Gate | 执行者在 Gate 1 指定（SR1412：不跨计划继承）；Gate 2 可只覆盖 T1，T2 起另需前置条件；T1 分支②独立停点；外部动作 Gate 一项覆盖 push/tag/发版/安装/IDEV-STD 消息，逐项列目标（plan-contract“一个外部动作 Gate”） | `:251-254` |
| 外部动作：IDEV-STD 消息 | 授权行明确“不含向其他会话发消息”`:60`；计划内只写草稿 `:238`；可追溯到设计 `:339`；发送推迟到发版后有理由 | `:238`、`:254`、`:290` |
| T1 先行 | T1 只读 + issue 评论，无前置限制，Gate 2 允许单独覆盖 | `:116`、`:135`、`:252` |
| 超出设计的任务 | 未发现。新增项均有来源：`config get` YAML（设计 `:235`）、真实环境验证（SR1402.1）、gofer plan/todo（AGENTS.md）、retrospective（plan-closure） | — |
| 设计“代码组织”一致性 | `internal/cli/config.go` 未列在设计架构 `:250-259`，但设计 §5 `:235` 要求该输出，计划在 scope freeze 显式说明来源；属 CLI 输出层，不违反分层 | `:42`、`:58` |
| SR1407 | 任务内 focused；全量、`-race`、staticcheck、smoke、Windows 全量各在 T9 一次；T9 Windows 重跑 `TestChatMultiHooks` 属完成 Gate 的最终树复核，可接受 | `:234-235` |
| SR1409 | 高暴露（已发布 v0.55.0、命令执行）两轴 + ledger | `:44` |
| SR1432 | 执行者按任务性质建议，最终由用户指定 | `:103` |
| GR104 | `hooks.go` 523 行，编排放 `pipeline.go`；chat 两文件净增 ≤25 行 | `:95`、`:208` |
| GR107 | 不适用（不改 `cmd/*`） | — |
| GR108 | 装配层 `internal/cli/chat.go`、`internal/wiring/localenv.go` 不改；合并逻辑全在 `internal/hooks` | `:58`；设计 `:257` |
| GR109 | Stop/普通事件/Interrupt 失败记 Warn，取消/超时记 Debug，并有 `slog` handler 断言；`writeConfigValue` 回落属输出格式化，非状态写入 | `:167`、`:178`、`:185`、`:191` |
| GR110 | `TestConfigHooksCommands` 经 root 命令真实分发 | `:146` |
| SR2002 / SR2004 | 符合 | `:106`；设计 `:250` |
| SR1403 | 无新依赖、无新 Module；假进程合同有一处冗余见 HPS15 | `:110` |

### SR1301 安全核对（计划层）

| 面 | 结论 |
|---|---|
| 鉴权 | 已核对：`Protected:true`，T2 验证 `SUGA_SESSION_ID=x` 下被拒；hyj7 已在 main（`7905a0f4`），`TestProtectedClassification` 存在 |
| 注入防护 | 已核对：argv 直接执行，复用 `commandClient` |
| 秘密 | 测试与 trace 已核对（只记 `entry`、`program`）；T9 真实环境的凭据处理未写全，见 HPS13 |
| 入参校验 | 已核对：`ReadHookEntries` 归一化顺序写全，错误精确到 `hooks.commands[i].<字段>` |
| 资源归属 | T9 合并目标检出的归属核对缺失，见 HPS12 |

无 SECURITY 类违反。

## 发现

按严重级别排序；全部 `CORE_CORRECTIVE`，无 `CORE_BLOCKING`。

### HPS12 [MEDIUM] T9 合并到本地 main 缺目标检出的分支与 dirty 核对（CORE_CORRECTIVE）

- 证据: T9 合并动作只写“主机 `host-merge.ps1 -Repo inhere-tools/suga -Branch feat/multi-command-hooks -MsgFile …`，以 `is_ancestor=0` 为收尾前提”（`:237`）。脚本全文（`tmp/xhen-p1/host-merge.ps1`）直接 `git -C $Repo merge --no-ff $Branch`，合并到主目录**当前 HEAD**，不核对当前分支是否为 `main`、工作树是否干净。plan-contract 要求“dirty 工作树保护、Git 根边界”；计划的 baseline 与 fail-closed（`:99`、`:109`）只覆盖 worktree `.worktrees/hooks5x`，没有覆盖合并目标即 suga 主目录。suga 主目录由其他会话并行使用（本轮评审期间 main 已由 `3ef5f642` 前进到 `7905a0f4`，hyj7 由另一会话合入）。同类穷尽：计划中写主目录的动作只有这一处（T1、T2–T8 均在 worktree 或只读）。
- 影响: 主目录不在 `main` 时会合到错误分支；主目录有他人未提交改动时合并可能失败或把冲突留在他人工作区，构成 Ownership Conflict。不判 `CORE_BLOCKING`（DATA）：`git merge` 遇到会被覆盖的本地改动时会拒绝合并，合到错误分支也可以 reset 恢复，没有不可回滚的数据丢失，necessity proof 不成立。
- 建议: T9 合并前加核对并写成 fail-closed：主机 job 先跑 `git -C inhere-tools/suga branch --show-current`（须为 `main`）与 `git -C inhere-tools/suga status --short`（须为空，或只含与本分支改动路径不相交且已登记归属的路径）；不满足即按 Ownership Conflict 停止。合并前重新记录 main HEAD 作为恢复点（回滚节 `:246` 相应补一句）。

### HPS13 [LOW] T9 真实环境的凭据与残留处理未写全（SR1301 秘密）（CORE_CORRECTIVE）

- 证据: T9 写“`config.yaml` 只含从真实配置复制的 provider 引用……`.env` 只读引用真实凭据，不复制文件”（`:236`）。未写明：①复制进 `tmp/suga-5xmf/home/config.yaml` 的 provider 段只能含 `${VAR}` 引用、不能含字面密钥（真实配置中如有字面 `api_key` 会被复制进 `tmp/`）；②`realenv.ps1` 与 gofer job 日志不打印环境变量或 `.env` 内容；③完成后删除 `tmp/suga-5xmf/home`（含 trace、session 数据）与构建产物，或说明保留理由；④gofer server 上产生的临时 hook 会话（`:61`）的清理方式。AGENTS.md 对同类工具 acpprobe 的约束是“读真实 config.yaml 与 .env 但不复制文件”。同类穷尽：计划中接触真实凭据的只有 T9 这一处（执行者 job 由 gofer 自身凭据处理，不在计划内复制）。
- 影响: 密钥可能落进 `tmp/` 或 job 日志；残留会话混入用户真实 gofer 会话列表。
- 建议: T9 加四条：provider 段只复制 `${VAR}` 引用，复制后 `grep -E 'api_key:\s*[^$\s]'` 为空才继续；脚本不 echo 环境；结束删除 `tmp/suga-5xmf/home`；记录临时 gofer 会话 id 并在完成说明中给出归档/删除命令（或写明保留）。

### HPS14 [LOW] CAP-01 与 T2 的复用证据位置不准（CORE_CORRECTIVE）

- 证据: CAP-01 写“`internal/cli/config.go` `writeConfigValue` 的 `PreferredTool` YAML 分支”（`:77`），T2 写“仿 `PreferredTool` 分支”在 `writeConfigValue` 内按键判断（`:145`）。实际 `PreferredTool` 分支在 `formatConfigValue`（candidate `internal/cli/config.go:260`、`:273`；当前 main `:305`），`writeConfigValue`（candidate `:242-247`）只拼 `key = value\nsource = …` 两行，且 `formatConfigValue` 只收值、不收键。同类穷尽：计划中引用 `internal/cli/config.go` 符号的只有 `:77`、`:145`（另 `:22` 处置表同述），三处一致地指向错误函数。
- 影响: Capability Discovery 的复用证据与代码不符；执行者按“仿 `writeConfigValue` 内的分支”找不到先例，可能自行选择插入点（是否需要改 `formatConfigValue` 签名属 Spec 轴判断）。
- 建议: CAP-01 改为“`formatConfigValue` 的 `PreferredTool` 分支（按值类型）”；T2 写明分支落点（在 `writeConfigValue` 中按 `value.Key` 选择格式化函数，或给 `formatConfigValue` 传键）与回显的两行格式（`hooks.commands = …` / `source = …`），两处保持一致。

### HPS15 [LOW] 假进程合同含未使用模式与自相矛盾表述（SR1403）（CORE_CORRECTIVE）

- 证据: 假进程合同（`:108`）定义 `slow`（“睡 1s 后无输出”），全文没有任何任务使用（`grep -n slow` 只命中 `:108`）；同句写“`mode` 只作用于 Stop”，随后又写“`timeout` 模式在全部事件上睡 1s”（T4 `:170` 在 SessionStart 上使用）。同类穷尽：其余模式 `block`、`fail`、`wait`、`silent`、`ifail`、`timeout` 均有使用点（`:170`、`:185-191`、`:206`、`:215`）。
- 影响: 三个包各实现一遍未用的模式（SR1403 不做优先）；“只作用于 Stop”与 `timeout` 冲突，执行者可能实现成 Stop 才超时，T4 超时用例失效。
- 建议: 删除 `slow`；把表述改为“除 `timeout` 作用于全部事件外，其余 `mode` 只作用于 Stop”。

### HPS6 残项（上轮未穷尽）前置判定式依赖已删除的分支名

- 证据: 0.2 判定改为 `git merge-base --is-ancestor feat/protected-config-keys main`（`:100`；处置表 `:27`）。hyj7 合并后分支已删除，当前运行该命令得 `fatal: Not a valid object name feat/protected-config-keys`、退出码 128。上轮建议是以合并提交判定。
- 影响: 前置条件事实上已成立（协调者确认，不作为发现），但按计划字面执行，fail-closed 判定会误报不满足而阻止 T2。
- 建议: 改为 `git merge-base --is-ancestor 7905a0f4 main`（hyj7 合并提交），其余两项（issue 关闭、符号存在）保留。处置沿用 HPS6 的 `CORE_CORRECTIVE`。

### HPS1 残项（上轮未穷尽）“打断时未启动的条目不记”无断言

- 证据: 设计 `:289`“Interrupt 不进 trace；打断时未启动的条目不记”。0.2 只覆盖前半（T6 `:204`、T7 `:217`）；T5 between 用例（`:189`）断言 B 与等待条目没有 `start` 日志，但没有断言 `done` 结果的 `Runs` 不含 B，T6 也没有 between 打断下的 trace 用例。
- 影响: `Runs` 若给未启动条目填空记录，chat 会多写 hook 事件，现有用例发现不了。
- 建议: T5 between 用例加 `len(Runs)==1 && Runs[0].Entry==<A>`（或 T6 包装下的 between 打断断言 trace 中没有 B 的 Stop 事件）。处置沿用 HPS1 的 `CORE_CORRECTIVE`。

## 覆盖缺口与限制

- 只读核对，未运行测试；validator 对 candidate 导出副本运行，只证结构。
- 未复核 gofer `internal/hookrelay/run.go`、`internal/httpapi/session_handler.go` 的具体行号（T1 自身会读）；`hook.go:158,282-286` 已核对与计划描述一致。
- 状态机语义、钩子点能否复现子态、`config get` 回显的精确格式归 Spec/Executability 轴；HPS14 只从“复用证据准确性”角度记录。
- 计划写作时的 baseline（HEAD `1b747836`、hyj7 在 worktree）在评审时已过时（main 现为 `7905a0f4`）；计划已写“实施开始时重新记录 HEAD 与 status”（`:99`），不构成发现。
- 本报告写在 `tmp/review/suga-hooks-plan/`；正式评审目录与 ledger 由主 Agent 落盘。

## 结论

PASS（Standards/Governance 轴，第 2 轮）：无 `CORE_BLOCKING`。HPS2–HPS5、HPS7–HPS11 关闭；HPS1、HPS6 各剩一个残项（上轮未穷尽，沿用 `CORE_CORRECTIVE`）；新增 HPS12–HPS15 均为 `CORE_CORRECTIVE`。未发现 GR107–GR110 或规则包结构违规，也没有超出设计的任务。按 planning-core-rigorous，第 2 轮全 PASS 但含 `CORE_CORRECTIVE`，修订后可做唯一一轮纠正确认轮（candidate 须变化）；不做则这些修订按“未复核修订”在 Gate 披露。

## 剩余风险与后续

- HPS12 不修正时，T9 合并依赖执行时人工确认主目录状态。
- T9 真实环境不覆盖续跑 block 路径（已按 `UNVERIFIED_REAL_ENV` 披露）。
- 发版须与 hyj7 同版或其后（D6）；IDEV-STD 消息发送、push、发版仍在外部动作 Gate 之后。
