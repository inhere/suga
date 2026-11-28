<!-- template_id: review; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 实施计划 Standards/Governance 评审（第 1 轮）

## 评审目标与范围

- 目标: suga 计划 `docs/plans/2026-11-28-origin-confirm-remember-plan.md`；Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `6bf852a6`（工作树与 candidate 一致）；subject path 同上
- 修订版本: Draft 0.1
- 评审者: Standards/Governance 轴独立评审者（Claude 子 agent，未参与计划编写），REPORT_ONLY
- 日期: 2026-11-28
- 排除项: Spec/Executability 轴（窗口/截断算法正确性、用例能否按写法实现）；不改仓库、不提交

## 输入与方法

- 全量读：payload `C0TJVugR8Kpr8ZAERo543A`、`L1pSw4v7EitW46DpcPDbLQ`；`AGENTS.md`；计划全文（`git show 6bf852a6:`）；设计 0.4 全文。
- 代码事实核对：`internal/wiring/origin_guard.go`（`Decide` 的 `g.chat == nil` 在 156 行、`confirm` 在 160 行；`Find` 与 `Record` 都用 `api.SessionIDFrom(ctx)`）；`pkg/agentcore/tools/origin.go`（`visitStrings`、`s.names[s.index[h]]`、哈希 0 表示不足窗口）。计划引用的夹具与用例名都在：`newJudgeHarness`、`testChatApprover`、`memoryScriptServer`、`hostPageTool`、`JudgeReply`、`traceEvents`、`TestObserveWritesOriginJudgeAttrs`、`TestChatOriginJudge`、`TestLayering` 等。`tmp/xhen-p1/wt-repo.ps1` 存在。
- 仪式成本核对：三轮报告行数 119+93、104+60、69+81（`586f2166`、`c1ee9dff`、`d8a8ab5e`），ledger `wc -l` 为 620，设计 161 行，计划 181 行，与计划 Gate 1 的数字一致。
- `idevstd script validate_document --kind plan` 的结果是 `ok: true`，输出在 `tmp/review/suga-ilko-plan/validate-plan.txt`。`host_or_non_offline_action=` 在全文只出现 1 次。
- 对照 `tmp/xhen-p1/host-merge.ps1`，以及 2026-11-26 的两份 suga 计划（trusted-memory、background-fork）里的合并写法。

## 验收标准

plan-contract 必需项逐项核对：

| 项 | 结论 |
|---|---|
| 思考模式 / 核心目标 / scope freeze / review budget / 停止条件 | 已核对，齐全（RIGOROUS，Full 两轴） |
| Capability Discovery 三表 | 已核对：三个三级标题、列名与模板一致；CAP-01～04 唯一；New module / Rejected 用 `NONE` sentinel |
| 唯一 host 声明 | 已核对：恰 1 处，在“范围、排除项与授权”里，值为 `REQUIRED`，逐项列出 worktree、合并、模型调用、主机测试、staticcheck 下载 |
| baseline | 已核对：Git root、branch、HEAD `112e8fcd`、status 为空、worktree 列表、他人 tracker 提交归属、expected symbols 都有，并写明实施开始时重新记录 |
| 前置与 fail-closed | 已核对：build、focused 测试、反向验证不符合预期即停，按四类记录；Ownership Conflict 停 |
| 波次 | 已核对：W1–W4 串行，依赖与任务一致 |
| 每个任务的五个字段 | 已核对：T1–T4 都有文件、动作、验证、完成标准、依赖；运行目录写为 worktree 根 |
| 回滚 | 已核对：各任务提交是恢复点；合并后 revert；无数据、无配置键 |
| 人工 Gate | 已核对：Gate 1 由用户指定执行者（计划只给建议），列出了仪式成本数字（已复核）；另有执行请求 Gate 和外部动作 Gate |
| 可追溯性 | 已核对：方案 1–5、D1–D10、验证要求都映射到任务和用例（反向验证的构成见 IS1） |
| 完成 Gate | 已核对：≤8 行总结、retrospective 一次性提示、剩余项 |

规则核对：

- SR1301 逐项：
  - 鉴权：已核对。本次放宽的是审批门。计划把跳过分支放在裁决 allow/deny 之后、`g.chat == nil` 拒绝之后，与代码 156–160 行吻合。跳过时不带 `originConfirmedKey` 调内层，所以规则 deny 和普通 ask 照常。guard ④⑤⑥ 加 R6、R7 能防住分支放错位置。
  - 注入防护：已核对。没有新增命令拼接；“已允许前缀 + 模型自写尾部”属于设计残余 5，T3 的文档会披露。
  - 秘密：已核对。trace 只多一个 `added`/`hit` 值；问句与 `ReportDanger` 的片段沿用 `redact.Secrets`；窗口只在内存里存哈希；“打码原文随窗口记入”是设计残余 4，Gate 已披露。
  - 入参校验：已核对，回复沿用 `ParseApprovalReply`，d、空串和其他输入一律拒绝（guard ⑧）。
  - 资源归属：已核对。`Find` 与 `Allow` 都用 `api.SessionIDFrom(ctx)` 作会话键；另一会话用例③会断言；子 agent 有独立实例。
- SR1403：已核对，全部是既有 owner 扩展，无新 Module、新依赖、新配置键。
- SR1407：已核对，任务层只跑 focused `-run`，全量（`-race`、staticcheck、smoke-go、Windows）只在 T4 跑一次。
- SR1409：已核对，Full 档成立（改变安全边界）。
- GR104：已核对，`origin.go` 271 行、`fakellm_test.go` 720 行、`observe.go` 596 行，增量远低于 1000 行。
- GR107：不适用（不动入口）。
- GR108：已核对。哈希与集合留在 `pkg/agentcore/tools`；wiring 只在既有 guard 里加一个判断分支，沿用现有结构；有 `TestLayering`。
- GR109：已核对。`Allow` 与 `ReportOriginRemembered` 只改内存状态，没有可吞的写入错误。
- GR110：不适用（不改 CLI 参数与子命令；ptye2e 本身也驱动真实二进制）。
- SR2002：已核对。任务书约束写了 `x/assert` 加 `t.Run`；现有相关测试都用 `x/assert`。
- SR2004：不适用（不是新 CLI）。

超出设计范围的检查：

- 单元用例②（P 的 ≥40 字节子串为 `Remembered`）是合理补充。它直接由设计“名词：判定逐窗口”和 D2 推出，不扩大行为。它也是设计要求的反向验证“改用 `hit.Snippet` 作键”能失败的必要用例：0.4 起 `Snippet` 不再带非复制尾部，①“同段不同尾部”在以 Snippet 作键时仍会通过，抓不到这个变异，只有②能抓到。
- `TestOriginRememberTurn`（真实回合）、fakellm 新模式 `originrepeat`、README/usage/CHANGELOG 三处文档：设计没有逐字列出，但都服务于设计的验收与“残余（Gate 须知）”，代价小，归为合理补充，不计 finding。

偏离登记检查：

- 不做真实复现：已登记（T4“设计未要求……无真实复现”，host 声明里也写了）。本次不涉及 provider 协议，AGENTS.md 不强制，判为合规。
- 反向验证构成与合并方式：未登记，见 IS1。

## 发现

### IS1 [MEDIUM] 两处与设计或既有流程的偏离未登记（CORE_CORRECTIVE，`user_visible_effect: false`）

- 证据:
  1. **反向验证构成**：设计“验证要求·反向验证”（设计第 149 行）共 9 项，计划的 R1–R9（第 100、112、124 行）与它的对应关系如下：
     - R1 对应“改回整段记入”；
     - R2 对应“只看首个命中”；
     - R3 对应“键去掉来源工具”；
     - R4 对应“改用 `hit.Snippet` 作键”；
     - R5 对应“多算 1 个窗口”；
     - R6 对应“跳过裁决”；
     - R7 对应“挪到无 chat 判断之前”；
     - R8 与 R9 都对应“去掉跳过分支”（R9 是它在 ptye2e 上的再跑一次）。

     设计第 1 项“改为记入本次全部段（P+X 用例失败）”没有对应。完成定义第 4 条写“9 项反向验证”，数目与设计相同，但构成不同，计划里没有任何地方说明这一处删减与合并。对应的正向用例是存在的：guard ⑦“P+X（两个来源）答 a 后只带 X 仍 Ask”，所以核心验收并非没有验证手段，只是缺了证明这个用例确实有效的反向验证。
  2. **合并方式**：T4（第 138 行）改为主机 job 直接执行 `git -C inhere-tools/suga merge --no-ff feat/origin-remember -m …`，没有写为何不用 `tmp/xhen-p1/host-merge.ps1`，而 2026-11-26 的两份 suga 计划（trusted-memory 第 167 行、background-fork 第 246 行）都用这个脚本。另外，脚本里带有 `-c user.name=inhere -c user.email=…` 和 `-F <消息文件>`，计划的命令没有。如果主机 git 没有全局 identity，`--no-ff` 合并会失败（fail-closed，可恢复）；即使配置了，合并提交的作者也可能与以往不同。
- 影响: 照计划实施时，“记入本次全部段”这一变异没有反向验证兜底；如果 guard ⑦ 写错（比如 X 与 P 来自同一来源、或没有真正断言 Ask），这个放宽超出同意范围的错误会漏过。合并一步可能因缺 identity 白跑一次主机 job。两者都不改变用户可见结果。
- 建议（任一即可，不需要新开一轮评审）:
  1. 在 T2 的反向验证里补 R10：`confirm` 在 `ReplyAllowAlways` 时重新调用 `findAll` 并把全部段都记入，确认 guard ⑦ 失败；可追溯性中“方案 3、D7”一行加上 R10，完成定义改为“10 项”。或者在 T2 写一句删减理由。
  2. 在 T4 写一句不用脚本的理由（脚本只打印最后 2 行，会吞掉 git 报错，见 memory `host-merge-script-silent-fail`），并在命令里补 `-c user.name=inhere -c user.email=…` 和 `-F <消息文件>`（消息文件放 `tmp/suga-ilko/`）。

## 覆盖缺口与限制

- 没有评审 Spec/Executability 轴的算法细节（所示片段起止、`cut` 边界、ptye2e 页面文本是否构成 ≥40 字节的复制段），这些由另一轴负责。
- 没有预跑计划中的任何测试或主机命令；只读核对了符号与文件是否存在。

## 结论

PASS：没有 `CORE_BLOCKING`。IS1 是 `CORE_CORRECTIVE`，`user_visible_effect: false`，可以在计划批准前顺手改正，不触发纠正确认轮。

## 剩余风险与后续

- 设计 0.4 是未复核修订（用户已点名接受）。计划的风险一节把 T1 的边界算错列为最大风险，并由 ⑤⑥、R1、R5 和 T4 复核兜底，处理得当。
- 计划 Gate 1 需要用户指定 T1–T3 的执行者；T2、T3 由主机 suga job 调用模型 provider，费用已在 host 声明里写明。
