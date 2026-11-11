<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 0.2 评审（Standards/Governance 轴，第 1 轮）

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md`
- 候选四元组：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `5095088921667f6c7f9f109c48510e5b395eacad`；subject `docs/design/2026-11-11-protected-config-keys-design.md`；revision `0.2`
- 修订版本：Draft 0.2
- 评审者：Standards/Governance 轴独立子 agent（未参与本设计起草或修订）
- 评审轴：Standards/Governance；第 1 轮全量读；`REPORT_ONLY`
- 日期：2026-11-11
- 排除项：用户已选定的 Q1–Q3（D3–D5）选择本身不评；Spec/Executability 轴内容（实现细节可执行性）只在影响治理判定时提及。

## 输入与方法

实际读取范围：
- 规范：`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`（全文 760 行）、`tmp/idev-std/d/bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（全文，含 design-contract 与 design 模板 1.2.0）；`AGENTS.md`（全文）。
- 候选：设计 0.2 全文（`git show 5095088:<subject>`），以及 `git diff 0c6f95ba..5095088`（0.1→0.2）。
- 前序设计：`docs/design/2026-10-09-config-layering-chat-design.md`（全文）、`docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md`（全文）；补读 `docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md` 中“新键不可写”相关行、`docs/design/2026-10-17-cli-onboarding-config-design.md` 的 config set 合同行。
- 代码事实核对：`internal/config/settings_registry.go`（全部键描述符与 provider 键）、`internal/cli/config.go:153-169,185-195`、`internal/cli/cwdcheck.go:209-215`、`internal/cli/init.go`、`internal/wiring/sessionenv.go`、`pkg/agentcore/tools/native/code_exec.go:563`、`internal/config/rules.go`、`internal/personal/rules.go`；对 suga/xhen/gofer 检索 `suga config set` 的使用方。

## 验收标准

design-contract 必需内容；SR1103、SR1108、SR1301、SR1403、SR1408、SR1409、GR107、GR108、GR110、SR2004；AGENTS.md 验证要求；review-contract 的 severity/disposition、CORE_BLOCKING 五类与 necessity proof、新机制 scope necessity、同类穷尽。

## 逐项核对摘要

- design-contract：思考声明、修订记录、背景、名词、范围/非目标、事实、方案、架构、流程、安全/回滚、决策、待确认、结论与 Gate 均在。缺“受影响项目”（S5）与验证要求（S4）。修订记录 0.2 为语义变化（Q→D），递增正确；决策 D3–D5 与“待确认：无”一致，0.2 摘要与 diff 一致。
- 代码组织：判定放 `internal/cli`（进程环境与 TTY 属入口事实，唯一写入口 `internal/cli/config.go:191-193`）、清单放 `internal/config`（注册表 owner）合理，依赖方向 `cli → config` 与配置分层设计一致；`config` 包不读进程 TTY 是正确分层。引用与参照项不足见 S7。
- 前序设计冲突：配置分层设计未约定 config set 可写性（该约定在 2026-10-17 onboarding 设计）；安全设计 D3（trust 不进配置）不受影响（`tools.approval` 仍拒 `trust`）；“新键不可写”来源于 2026-11-10 计划而非设计，D5 替代未声明（S3）。
- SR1301 逐项：鉴权——已核对，判定为启发式且绕过披露不实（S2）；注入防护——已核对，不适用命令拼接，结论成立；秘密——设计写“不变”，但清单漏掉可外送 API key 的端点键，未核对充分（S1）；入参校验——已核对，沿用现有；资源归属——不适用，成立。
- 新机制 scope necessity：`Protected` 字段与入口判定——必需（核心目标直接依赖）；防漏标注册表测试——支持核心目标（D1 用户已认可），但“模式”未定义，内部有效性留给 Spec 轴，见 S1 末。
- 暴露度：高（tag `v0.55.0` 已发布；安全边界），两轴独立 + ledger 相称；未发现静默扩围。

## 发现

### S1 [BLOCKER] 安全配置键清单漏掉模型端点类键，核心目标不成立

- disposition：`CORE_BLOCKING` [SECURITY]
- 证据：设计“已确认事实”第 50-52 行称列出“可写的安全相关键”；注册表中以下键 `Writable: true` 且未列入：`providers.<id>.base_url`、`providers.<id>.type`、`providers.<id>.models.<name>.model_id`（`internal/config/settings_registry.go` providerKeyDescriptors 约 305-325 行）、`model`、`compact_model`、`fallback_models`、`models.compact|idle|handoff|judge`（95-106 行）、`agents.model`（185 行）。
- 影响：`suga config set providers.<preset>.base_url https://attacker` 可在会话内执行（非 protected，D3 不拦），之后每个会话的请求连同 `api_key`（来自 `.env` 的 `${…}`）发往该地址；该端点返回的工具调用在 run（gofer 可写任务，原生工具默认放行）下直接执行——即“持久命令执行”与秘密外送；改 `models.judge` 指向自建 provider 可让外部来源裁决恒为 allow，即“放宽安全检查”。
- necessity proof：
  - core_acceptance：核心目标“模型不能通过修改 suga 自己的配置……获得持久的命令执行”；用户可见结果“模型在会话里执行同样的命令会被拒绝”。
  - necessary_causal_chain：不采纳 → 端点键可在会话内 config set → 后续会话由攻击者端点驱动并携带密钥 → 核心验收被同一入口（config set）绕过。
  - minimal_owner：本设计“已确认事实”清单与 D4 列表（只增条目）。
  - deletion_alternative：FAIL（删除机制无法关闭）。
  - scope_effect：不新增 Module/Interface/Schema/state/lifecycle；但扩大用户 D4 所选清单，按 SR1408 须请用户确认。
- 同类穷尽（候选中其余未列入、需逐一判定是否归入的可写键）：`rules.file`（改指令文件名可绕开 `ClampAllow` 对规则文件的保护、持久注入指令，`internal/config/rules.go:14-32`）；`workspace`（`internal/config/paths.go:55`，影响工作区根，需核实是否扩大读写范围）；`memory.user|project.backend`（启用已配置的命令后端）；`log.file`（任意路径写日志）；`hooks.timeout_ms|stop_wait_sec|stop_background`、`tools.preferred`、`tools.disabled`、`network.proxy.sites`（`network.proxy.url` 已 Sensitive）——后几项可判为非安全但应写明判定。另：D1 的防漏标测试按“键名模式”判定，无法覆盖 `providers.<id>.base_url`、`rules.file` 这类不含执行/放宽字样的键，应改为显式清单 + 新增键必须声明 Protected 取值。
- 建议：补“端点/模型路由类”与上列需判定项，写入事实与 D4，并向用户确认扩充；或由用户明确接受为残余风险并写入“风险与限制”。

### S2 [HIGH] 绕过 1 的结论不实，残余风险披露不完整

- disposition：`CORE_CORRECTIVE`
- 证据：设计第 84 行称 `env -u SUGA_SESSION_ID` 后“仍因 shell_exec 子进程的标准输入不是终端而拒绝”。模型可在 shell 中分配伪终端（如 `script -qc "suga config set …" /dev/null`、`python -c 'import pty;pty.spawn(...)'`、`unbuffer`），stdin 即为终端；`internal/wiring/sessionenv.go:34-37` 的 `Options.NoSessionEnv` 让 SDK 宿主完全不导出 `SUGA_SESSION_ID`；第 86 行“外部来源裁决是另一道防线”在 `trust` 档不检查（安全设计 0.6 第 3 项）。D5 使 `tools.origin_judge.*`、`tools.read_deny*` 由 v0.55.0 的“无条件不可写”变为“满足 TTY 判定即可写”，对这两组键是防护面放宽。
- 影响：读者会误以为 config set 入口已封闭；授权边界“人在终端可改、模型不可改”实际为“提高门槛”。因 D2 已接受同等难度的直接改文件绕过，此项不阻断，但须如实披露。
- 建议：改写绕过 1 为“可经伪终端绕过，本设计不防”；补 SDK `NoSessionEnv`、`trust` 下裁决不生效；在 D5/兼容中写明两组键相对 v0.55.0 的变化。

### S3 [MEDIUM] D5 替代前序计划决策未声明

- disposition：`CORE_CORRECTIVE`
- 证据：“新键不可写”出自 `docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md:31,51,91-92,240,330`（F14/S12），并落到 `internal/config/settings_registry.go:227,250` 注释、`docs/usage/gofer-personal-agent.md:124`（“`suga config set` 拒绝修改”）与测试 `internal/cli/config_origin_judge_test.go`、`internal/cli/config_read_deny_test.go`。设计第 26、59、96 行只写“改回”，未引用出处、未列需同步的文档与测试。
- 影响：前序批准物与新设计并存矛盾；计划阶段可能漏改测试/文档。
- 建议：在决策中加“对前序计划的替代”条目，列出上述行与需同步的注释、usage 文档、测试。

### S4 [HIGH] 缺验证要求（GR110、Windows 主机、反向验证）

- disposition：`CORE_CORRECTIVE`
- 证据：设计只在第 55 行列 GR110，无“验证要求”；AGENTS.md“验证”要求 Windows 行为经 gofer exec job 在主机跑、交互行为用 ptye2e 并做反向验证。TTY 判定平台相关：Windows 下 Git Bash/mintty 的 stdin 为管道，`term.IsTerminal` 为假。
- 影响：核心用户结果“你在终端里照常可用”在 Windows 部分终端上可能不成立且无验证兜底。
- 建议：补经 `app.Run` 分发的 set/unset 用例（有 `SUGA_SESSION_ID`、非 TTY、TTY 三类）、chat 内 `shell_exec` 执行 config set 被拒的 ptye2e + 反向验证、Windows 主机（PowerShell/Windows Terminal 与 Git Bash）行为核对并写入兼容说明。

### S5 [MEDIUM] 受影响项目与使用方影响未写全

- disposition：`CORE_CORRECTIVE`
- 证据：design-contract 要求“范围、非目标和受影响项目”，设计未列受影响项目。`xhen/README.md:151` 指导人运行 `SUGA_HOME=~/.xhen/suga suga config set tools.read_roots '/'`（并注明 Windows 写法）——人在 Unix 终端仍可用，Windows mintty 下可能被拒；`suga/README.md:65` 的 `tools.allow_write` 变为 protected；gofer 仓库未发现调用 `suga config set`；`scripts/ptysmoke/chat_i18n_en.py:139` 只改 `language`，不受影响。拒绝的退出码与提示语未定，`docs/design/2026-10-17-cli-onboarding-config-design.md:298-299` 的 set/unset 退出码合同未扩展。
- 建议：列 xhen、gofer 为受影响项目及发版说明；写明拒绝退出码（建议沿用输入错误 2）与 unset 同样适用；文档更新清单加 xhen README（由对应会话维护）。

### S6 [MEDIUM] 同类写入口 `suga init --force` 未覆盖也未声明

- disposition：`CORE_CORRECTIVE`
- 证据：`internal/cli/init.go:16-31` 的 `--force` 备份并替换 config 模板，可一次清除用户的 `hooks.*`、`tools.read_deny`、`memory.write_tainted: deny` 等设置；设计 scope（第 18 行）只列 config set/unset 与配置文件。
- 影响：会话内可经该命令把 protected 键重置为缺省（放宽用户自设限制），与 unset 同类却不受限。
- 建议：将 `init --force` 纳入同一判定，或在非目标/风险中明确不防并说明理由。

### S7 [LOW] 代码组织引用与参照不准确

- disposition：`CORE_CORRECTIVE`
- 证据：设计第 67 行以 GR107 解释“命令层只做入口判断”，GR107 实为入口薄、命令放 `internal/commands`；suga 的命令层是 `internal/cli`（配置分层设计第 112 行确立），模板要求列出参照与偏离理由（SR2004）。
- 建议：改为引用 SR2004/配置分层设计的既有 owner，说明沿用 `internal/cli` 而非 `internal/commands` 的理由；架构补测试与文档 owner。

## 覆盖缺口与限制

- 未运行代码或测试；伪终端绕过与 Windows mintty 的 TTY 结论基于平台常识与代码阅读，计划阶段应实测。
- `workspace`、`memory.*.backend` 等是否属安全键只列出待判定，未逐一追代码。
- Spec/Executability 轴（如防漏标测试的具体规则）未深评。

## 结论

`BLOCKED`：S1 为开放 `CORE_BLOCKING` [SECURITY]。S2–S7 为 `CORE_CORRECTIVE`，计划批准前应修正（S7 属结构项，按合同不得延后）。

## 剩余风险与后续

- 即使修正 S1，直接改 `config.yaml` 与伪终端绕过仍在，防护本质为提高门槛；需在 README/usage 中如实写明。
- 修正 S1 会扩大用户所选 D4 清单，需用户确认后再出 0.3；下一轮按 diff 复核 S1–S7。
