<!-- template_id: review; template_version: 1.2.0 -->
# suga 记忆可信开关实施计划 Standards/Governance 轴第 1 轮 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `452845ff`；subject `docs/plans/2026-11-26-trusted-memory-backend-plan.md`；document revision Draft 0.1
- 修订版本: 0.1（实施已批准设计 `docs/design/2026-11-26-trusted-memory-backend-design.md` 0.4，批准记录在 `3e641baa`）
- 评审者: Standards/Governance 轴独立评审子 agent（不是计划作者，第 1 轮全量读）
- 日期: 2026-11-26
- 排除项: Spec/Executability 轴（测试用例能否照写跑通、协议细节），由另一轴负责；不改仓库、不提交

## 输入与方法

- 规范 payload：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（规则、review 合同、candidate loop、review 模板），`tmp/idev-std/d/L1pSw4v7EitW46DpcPDbLQ/payload.md`（plan-contract、plan-closure、plan 模板 1.3.1）。
- `AGENTS.md`；`git show 452845ff:` 读计划全文（179 行）和设计全文（141 行）；并行计划 `docs/plans/2026-11-26-background-fork-plan.md` 的范围、各任务文件、执行者与合并段。
- 抽查代码事实：`internal/wiring/main_test.go`（已有 TestMain）、`gitRoot`（`.git` 文件也算根，所以 worktree 根就是它自己）、`selectProjectBackend`、`memory_runtime.go` 的 `Backend` 回调、`memory.Scope`（可比较的 struct）、`Makefile` 的 `smoke-go`（跑整个 ptye2e，新场景自动包含）、各 `-run` 用例名都存在、目标文件行数（最大 788 行，加改动后仍低于 1000）、`tmp/` 已被 WP `.gitignore` 忽略。

## 验收标准

plan-contract 必需项、plan-closure，以及 SR1301、SR1403、SR1407、SR1409、GR104、GR107–GR110、SR2002、SR2004；是否超出设计范围；与 `feat/background-fork` 的重叠文件和合并顺序。

核对结果（没有问题的写“已核对”）：

| 项 | 结论 |
|---|---|
| 思考模式/核心目标/scope freeze/budget/停止条件 | 已核对（第 14–19 行） |
| 修订记录、状态 `Draft 0.1` | 已核对 |
| Capability Discovery 三表 | 已核对：标题、列名固定；CAP-01..04 唯一；`NONE` sentinel；层级选的是 DIRECT_REUSE 或 OWNER_EXTENSION |
| 唯一的 `host_or_non_offline_action` | 已核对：恰有 1 处，值为 `REQUIRED`，列出了动作 |
| workspace baseline | 已核对：root、branch、HEAD、status、他人 worktree、expected symbols，并写明开工时重新记录 |
| 前置检查与 fail-closed | 已核对 |
| 波次 | 已核对：W1–W4 是串行依赖 |
| 每个任务的五个字段 | 已核对：T1–T4 都有文件、动作、验证、完成标准、依赖；运行目录写的是 worktree 根 |
| 回滚 | 已核对：有任务提交、合并前 HEAD、revert、用户侧删键 |
| 人工 Gate | 计划批准、执行请求、外部动作 Gate 三项齐全；执行者模型由用户在 Gate 指定。仪式成本数字不全，见 MS3 |
| 可追溯性 | 表格完整；“主机复现原场景”这一项与设计不一致，见 MS1 |
| 完成 Gate | 已核对：有完成条件、≤8 行完成总结和 retrospective |
| SR1301 鉴权 | 已核对：Protected、会话内与管道 stdin 拒绝由 GR110 子用例和 R2 覆盖 |
| SR1301 注入 | 不适用（不拼 SQL 或命令；fakememexec 只读夹具） |
| SR1301 秘密 | 有问题，见 MS2 |
| SR1301 入参校验 | 已核对：`loadMemoryTrust` 的四类错误由 R4 覆盖 |
| SR1301 资源归属 | 已核对：要求整根相等；父目录、子目录、第三方仓库、worktree 都有反例；host-key 恒为 false；其他 scope 返回 false |
| SR1403 | 已核对：复用 Protected 机制、扩展既有 owner，没有新 Module |
| SR1407 | 已核对：任务内只跑 focused 命令，全量放在 T4 跑一次 |
| SR1409 | 已核对：按 Full 处理（改变安全边界），两轴评审 |
| GR104 | 已核对：受影响文件最大 788 行 |
| GR107 | 不适用（不改 `cmd/*/main.go`） |
| GR108 | 已核对：读取、校验和匹配留在 wiring，与现有 `selectProjectBackend` 及 `project_trust` 的先例一致；agentcore 只收回调，T2 跑 `TestLayering` |
| GR109 | 不适用（新增路径不写用户可见状态） |
| GR110 | 已核对：T1 经 `app.Run([]string{"config","set",...})` 测试 |
| SR2002 | 已核对（第 75 行） |
| SR2004 | 不适用（不是新 CLI） |
| 超出设计范围 | 没有超出设计范围的任务。`skills/suga-guide/references/config.md`、`config/config.yaml` 注释和 CHANGELOG 是设计“运维文档”的配套说明，不扩展行为 |

## 发现

### MS1 [MEDIUM] T4 删掉了设计要求的“主机 chat 复现原场景”，计划没有把它列为偏离

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据: 设计第 130 行要求“另在主机 chat 配 gofer 后端复现一次原场景”。计划 T4 第 135 行改成只看 `suga manifest` 是否显示 `trusted=true`，并写明“不做真实模型调用”。计划第 17 行的 scope freeze 和第 28 行的完成定义 5 却声称“设计‘验证要求’全部落到测试”。可追溯性第 168 行只把主机步骤对应到 D7。同类问题只有这一处：其余设计验证项（单元、manifest、分类、GR110、链路①–⑤、ptye2e、反向验证、make smoke-go）都有对应任务。
- 影响: 核心行为已由 ptye2e（假 exec 后端加 fakeLLM）验证，所以不构成阻断。但“真实 gofer 后端加真实 chat 不再询问”在真实环境下没有验证。计划还在没有记录偏离的情况下放宽了已批准设计的验证合同，完成定义和 T4 自相矛盾。
- 建议: 二选一。①恢复一次主机复现：隔离 `SUGA_HOME`、最小配置、一次真实模型调用，并按 MS2 处理凭据。②在计划里写一节“与设计的差异”，说明这一项改为 manifest 核对，同时写明 `UNVERIFIED_REAL_ENV`；完成定义 5 和可追溯性跟着改，并在计划 Gate 上请用户确认。

### MS2 [MEDIUM] T4 主机步骤“只读真实配置”的凭据边界没写清

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据: 计划第 135 行写“只读真实配置、隔离 `SUGA_HOME=<WP>/tmp/suga-7s4v/home`”，但没说读真实配置中的哪些段、怎么进入隔离 home。并行计划 T9 处理同类步骤时，明确写了只复制 provider 段、丢弃 `headers`、`api_key` 只接受 `${VAR}` 形式。这一步不调用模型，只需要 `memory.project.trusted` 和工作区。全计划同类只有这一处，T1–T3 都用临时 `SUGA_HOME`。
- 影响: 照字面执行时，执行者可能把整份真实 `config.yaml`（可能含明文 key 或 headers）拷到 WP `tmp/`，或者把它打进 job 日志。这属于 SR1301“秘密不进日志/输出”。`tmp/` 虽被 gitignore 忽略，但 job 日志和 manifest 输出不受 gitignore 保护。
- 建议: 改为“在隔离 home 写一份最小 `config.yaml`，只含 `memory.project.trusted`，不读也不复制真实配置”。如果按 MS1 选①，就沿用 fork 计划 T9 的凭据过滤规则。

### MS3 [LOW] 计划批准 Gate 的仪式成本数字不全

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据: 计划第 153 行只写了“设计评审 2 轮两轴；计划评审待进行”。planning-core-rigorous 要求计划批准请求列出轮数、报告行数和被评文档行数。现有数据：设计评审报告 `standards-review.md` 91 行、`spec-review.md` 100 行，ledger 444 行，设计 141 行，计划 179 行。
- 影响: 用户在 Gate 上看不到治理成本，与 SR1409“仪式不得反超本体”的自检要求对不上。不影响实施结果。
- 建议: 发出计划 Gate 时，用 `validate_document --companion` 输出的 `ceremony` 补全设计评审和计划评审两组数字。这属于 Gate 文案，不必升 revision。

### MS4 [LOW] 合并顺序的条件与重叠清单不够严

- disposition: `CORE_CORRECTIVE`；`user_visible_effect: false`
- 证据（同类全列）:
  1. 计划第 132 行只在 “`feat/background-fork` 已合入本地 main 时” rebase，否则跳过。如果 main 因为其他提交已前进，`host-merge.ps1` 会生成一个合并结果，而这个结果没跑过 T4 的全量测试。
  2. 计划第 147 行说冲突解决后“重跑 focused 测试”，只指 T1–T3 的命令。`test/ptye2e/fakellm_test.go` 与 fork T7 的“按分身指令识别分身请求”在同一处分发逻辑。T4 在 Windows 上只跑 `TestChatTrustedMemory|TestChatOriginJudge`，fork 的 `TestChatFork` 在冲突解决后只有 Linux 的 `make smoke-go` 会覆盖。
  3. 第 147 行的重叠清单没有列 `skills/suga-guide/references/`。fork 计划第 63 行和 T8 是按目录声明的（它改 agent 与 chat 命令所在文件，大概率是 `tools.md` 和 `chat.md`，本计划改 `config.md`），冲突概率低，但清单不完整。
- 影响: 这三点都不影响本功能的核心验收。最坏情况是合并后的 main 在 Windows 上跑 fork 的 ptye2e 时失败，可以 revert 恢复。
- 建议: 把 rebase 条件改为“main 相对 worktree 基点有新提交即 rebase”，rebase 后再跑 T4 的全量测试。如果 `fakellm_test.go` 发生冲突，Windows 步骤的 `-run` 加上 `TestChatFork`。重叠清单补上 `skills/suga-guide/references/`（按目录）。

## 覆盖缺口与限制

- 没有核对测试能否照写跑通，例如 exec 子进程能否继承 `SUGA_TEST_MEMEXEC`，或 `config set` 如何解析 StringList 参数。这些归 Spec/Executability 轴。
- 并行分支 `feat/background-fork` 的 worktree 内容没有读，只按它的计划核对重叠文件。
- 没有运行 validator 和任何命令，只读了文档和代码。

## 结论

PASS。没有 `CORE_BLOCKING`。MS1–MS4 都是 `CORE_CORRECTIVE`，没有一项标 `user_visible_effect: true`，所以不会触发纠正确认轮。按 review 合同，这些项可以写入下一修订，或者在 Gate 上披露；其中 MS1、MS2 会改变 T4 的动作，属于计划语义变化，修改时要升 revision。没有发现结构违规（GR104/GR107–GR110/SR2004）。

## 剩余风险与后续

- 安全取舍（可信后端被他方写入恶意内容时，只剩审批规则兜底）已在设计中被接受，计划在“风险”一节如实写了。
- T1–T3 的默认执行者建议全部用 `opencode-go/deepseek-v4.1-flash`。T1 和 T2 是安全边界上的核心改动，fork 计划对同类核心任务建议用推理更强的模型。这一点留给用户在 Gate 上决定，不算 finding。
- 修订后请把 MS1 的选择写进修订记录，并把 MS3 的数字放进计划 Gate 请求。
