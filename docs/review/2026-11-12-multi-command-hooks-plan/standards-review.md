<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令 实施计划 第 3 轮（纠正确认轮）Standards/Governance 评审

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `147ad4a3`；subject path `docs/plans/2026-11-12-multi-command-hooks-plan.md`；上轮 candidate `3ef5f642`（Draft 0.2，两轴 PASS）
- 修订版本: Draft 0.3
- 评审者: 独立 Standards/Governance 轴评审者（Claude 子 agent，未参与计划撰写）；模式 `REPORT_ONLY`；第 3 轮，唯一一轮纠正确认轮
- 日期: 2026-11-12
- 排除项: 全量重评；Spec/Executability 轴的状态机与用例正确性（HPF13–HPF16 的语义闭合只在影响治理判断时引用）；设计 0.6；不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md`；review-contract 与 review 模板 1.2.0（同目录含 `review-contract.md` 的 payload）。
- 上轮报告：`147ad4a3` 中 `docs/review/2026-11-12-multi-command-hooks-plan/standards-review.md`（HPS1 残项、HPS6 残项、HPS12–HPS15）、`spec-review.md`（HPF13–HPF16）。
- 被评对象：`git diff 3ef5f642..147ad4a3 -- <subject>` 与 `git show 147ad4a3:<subject>` 相关段落（修订记录 `:8-14`、第 2 轮处理表 `:38-50`、CAP-01 `:90`、baseline/前置 `:112-113`、假进程合同 `:121`、T2 `:157`、T5 `:189-205`、T6 `:217-219`、T9 `:248-254`、回滚 `:258-262`）。
- 事实核对（只读）：当前 main `internal/cli/config.go:274-311`（`writeConfigValue`、`formatConfigValue`、`PreferredTool` 分支 `:305`）；`internal/config/settings_registry.go` 的 `Sensitive: true` 项（`providers.<id>.api_key` `:326`、`providers.<id>.headers` `:327`）；`internal/config/providers.go:214-224`（provider 段读取 `api_key`、`headers` 并 `expandEnv`）；suga 仓库 `.gofer/tracker/issues.jsonl` 存在、`AGENTS.md:13`（任务用 `gofer issue`，数据在 `.gofer/tracker`）；`git status --short` 干净、`git log 3ef5f642..147ad4a3`（区间含 hyj7 合并 `7905a0f4` 与第 2 轮评审提交）。

## 验收标准

- review-contract：五类 `CORE_BLOCKING` 需 necessity proof；同类穷尽；结构违规按 `CORE_CORRECTIVE`。
- plan-contract（dirty 工作树保护、Git 根边界、恢复点）、plan-closure、planning-core-rigorous（纠正确认轮只核对修订）；SR1301（秘密、资源归属）、SR1403、SR1412；修订记录版本规则（计划 `:14`）。

### 上轮项关闭核对

| ID | 结论 | 证据（0.3 行号） |
|---|---|---|
| HPS12 | 主体关闭：合并前核对分支为 `main`、`status --short` 为空、fail-closed 按 Ownership Conflict 停止，记录合并前 HEAD，回滚节同步。与计划自身 tracker 写入的冲突见 HPS16 | `:251`、`:261` |
| HPS13 | 基本关闭：四条全部落入 T9（只复制 `${VAR}`+复制后检查、不打印环境与 `.env`、结束删除 home/ws/二进制、记录临时 gofer 会话 id 并给处理方式）。检查式只覆盖 `api_key`，见 HPS17 | `:250` |
| HPS14 | 关闭：CAP-01 改为 `formatConfigValue` 的 `PreferredTool` 分支（按值类型），T2 写明 `writeConfigValue` 按 `value.Key` 选新 `formatHookEntries`、`formatConfigValue` 签名不改、两行回显格式。与当前代码一致（`config.go:274-276` 单个 `Fprintf` 两行、`:292`、`:305`） | `:90`、`:157` |
| HPS15 | 关闭：`slow` 删除；措辞改为“`mode` 决定 Stop 行为；`ifail` 另作用于 Interrupt；`timeout` 另作用于全部事件”，不再自相矛盾 | `:121` |
| HPS6 残项 | 关闭：判定式改为 `git merge-base --is-ancestor 7905a0f4 main`，并注明写作时已满足；HPF9 行补注“0.3 改为合并提交” | `:113`、`:28` |
| HPS1 残项 | 关闭：T5 between 用例断言 `Runs` 只含 A，新增“第一条启动前”用例断言 `Runs` 为空；T6 包装下 between 打断断言 trace 无 B 事件 | `:198-199`、`:217` |

### 治理面核对（无发现者）

| 项 | 结论 | 证据 |
|---|---|---|
| 版本递增 | 正确：0.3 含语义变化（状态机初始态、用例断言、T9 新步骤），递增为 0.3；状态行同步 | `:4`、`:12` |
| baseline 更新是否属元数据 | baseline（HEAD `5266449c`、hyj7 已合入、worktree/分支已删）是写作时事实更新，属元数据；与前置判定式（语义：fail-closed 判据改变）一同进入 0.3，无需另立版本，也未把元数据纠正冒充语义变化。`5266449c` 为 candidate 父提交，事实一致 | `:112-113` |
| 修订记录 | 0.3 行按类列出全部修订（含 hyj7 同步），与 diff 一致，未发现 diff 中有未登记的语义改动 | `:12` 对照 diff |
| 处理表 | 第 2 轮全部 10 项（HPF13–HPF16、HPS1 续、HPS6 续、HPS12–HPS15）均有行，合并行（HPF14+HPS1 续、HPF16+HPS15、HPS6 续+HPF15）处理内容覆盖双方建议 | `:38-50` |
| 无法追溯到设计的新机制 | 未发现。新增均追溯到评审项或已有来源：`formatHookEntries`（设计 §5 `config get` 输出 YAML，HPS14）；`last` 初值 -1、初始 `between`、共用收尾、第一条启动前用例（HPF13，落实设计打断语义）；HF13 的 `timeout_ms: 30000`、任务 ID 过滤、`t.Cleanup`（HPF16，测试编排）；合并前核对（plan-contract dirty 保护）；凭据与残留四条（SR1301） | `:195-199`、`:218`、`:250-251` |
| 授权与外部动作 | 临时 gofer 会话只“给出归档或删除方式”，不在计划内执行删除，未越过授权行 `:73` 与外部动作 Gate `:269`；`host_or_non_offline_action` 仍为一项 REQUIRED，覆盖该会话记录 | `:250`、`:73`、`:269` |
| Capability Discovery | CAP-01 更正后与代码一致；其余未改，`NONE` sentinel 保留 | `:90-100` |
| 恢复点 | 合并前 HEAD 作为恢复点，回滚节写 revert 合并提交并引用该恢复点；任务级恢复点不变 | `:251`、`:260-261` |

### SR1301 安全核对（仅修订部分）

| 面 | 结论 |
|---|---|
| 秘密 | 规则写全（只复制 `${VAR}`、不打印、结束删除）；自动检查覆盖不全，见 HPS17 |
| 资源归属 | 主目录合并前核对已加；与计划自身 issue 评论写入的冲突见 HPS16 |
| 鉴权 / 注入 / 入参 | 本轮未改，沿用上轮结论 |

无 SECURITY 类违反。

## 发现

全部 `CORE_CORRECTIVE`，无 `CORE_BLOCKING`。

### HPS16 [MEDIUM] 合并前“`status --short` 须为空”与计划自身在主目录写入的 issue 评论冲突，照字面执行会误判停止（CORE_CORRECTIVE）

- 证据: T9 合并前核对要求 `git -C inhere-tools/suga status --short` 为空，否则按 Ownership Conflict 停止（`:251`）。但计划在合并前多处向 suga 的 tracker 写入：T1 结论写进 issue 评论（`:144-147`）；T9 真实环境结果与 `UNVERIFIED_REAL_ENV` 行记录在 issue 评论（`:254`）。suga 的 issue 数据在仓库内 `.gofer/tracker/issues.jsonl`（`AGENTS.md:13`，文件存在），由主 Agent 在主目录执行 `gofer issue comment` 时写入主目录工作树。计划没有写这些 tracker 变化何时提交（全文 `提交` 相关条目只涉及 worktree 任务提交，`:117`、`:160`、`:182`、`:207`、`:260`）。同类穷尽：计划中在合并前写主目录的只有 issue 评论（T1、T9 两处）；gofer plan/todo 状态在 gofer server，不写工作树；真实环境产物在 WP `tmp/`，不在 suga 仓库。
- 影响: 主目录会因计划自己的 tracker 写入而 dirty，核对按字面判为 Ownership Conflict 停止（fail-closed 误报，同 HPS6 残项一类）；或执行者为通过核对临时把自己的改动当他人改动处理。不判 `CORE_BLOCKING`：结果是停止而非错误合并，无数据丢失，necessity proof 不成立。
- 建议: 合并前核对之前加一步“提交本计划产生的 tracker 变化（`.gofer/tracker/*.jsonl`，按功能点随提交）”，再要求 `status --short` 为空；或写明允许的例外仅限已登记为本计划所写的 `.gofer/tracker/issues.jsonl`，且须先提交再合并（与“worktree merge 前先提交 tracker”的既有经验一致）。T9 issue 评论的写入时机（合并前/后）一并写明。

### HPS17 [LOW] 真实环境凭据检查式只覆盖 `api_key`，未覆盖同为 Sensitive 的 provider `headers`（CORE_CORRECTIVE）

- 证据: T9 规则写“provider 段只能含 `${VAR}` 引用”，但自动检查只有 `Select-String -Pattern 'api_key:\s*[^$\s]'`（`:250`）。provider 段中另一个 Sensitive 字段是 `providers.<id>.headers`（`settings_registry.go:327`，`ConfigTypeStringMap`，`providers.go:223` 对其 `expandEnv`），常用于放 `Authorization` 等鉴权头，字面值不会被该正则命中。同类穷尽：provider 段的 Sensitive 字段只有 `api_key` 与 `headers` 两个（registry `Sensitive: true` 共 5 项，其余 `serpapi_key`、`network.proxy.url`、`tools.web_fetch.proxy` 不在 provider 段，计划只复制 provider 段，不涉及）。
- 影响: 真实配置的 provider 若用字面 `headers` 传凭据，会被复制进 `tmp/suga-5xmf/home/config.yaml` 并在检查后放行；结束删除缩短了暴露期，但规则与检查不一致。
- 建议: 检查改为覆盖 provider 段全部 Sensitive 字段：`api_key` 与 `headers` 下的每个值都须为 `${VAR}` 形式（例如对复制后的 provider 段逐值检查不含非 `${...}` 的字面值），或复制时直接丢弃 `headers` 并说明不影响本次验证。

## 覆盖缺口与限制

- 只读核对；未运行 validator 与测试（本轮修订不改结构段落，上轮 validator 已通过）。
- HPF13–HPF16 的状态机与用例语义闭合归 Spec/Executability 轴确认；本轴只核对其可追溯与登记完整。
- 未核对 gofer CLI 当前是否有会话归档/删除子命令；计划已写“没有时写明保留”。
- 未确认 `gofer issue comment` 在本机实际写入的是 suga 仓库 tracker 还是其他位置；结论依据 `AGENTS.md:13` 与仓库内 `.gofer/tracker/issues.jsonl` 的存在。若实际不写工作树，HPS16 降为措辞建议。

## 结论

PASS（Standards/Governance 轴，第 3 轮纠正确认轮）：无 `CORE_BLOCKING`。HPS12–HPS15 与 HPS1、HPS6 残项关闭；修订记录与第 2 轮处理表完整；baseline 更新属元数据且与语义修订同版合理；未引入无法追溯到设计的机制。新增 HPS16（MEDIUM）、HPS17（LOW）均为 `CORE_CORRECTIVE`。按 planning-core-rigorous，本轮为唯一纠正确认轮，不再开评审轮；两项可在批准前做小修订（Gate 披露为“未复核修订”），或在 Gate 作为已知风险披露。

## 剩余风险与后续

- HPS16 不修正时，T9 合并前需人工先提交 tracker 变化，否则核对会停止。
- HPS17 不修正时，真实验证前需人工确认 provider `headers` 无字面凭据。
- 续跑 block 真实路径仍为 `UNVERIFIED_REAL_ENV`；发版、push、IDEV-STD 消息仍在外部动作 Gate 之后。
