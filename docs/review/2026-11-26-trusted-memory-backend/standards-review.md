<!-- template_id: review; template_version: 1.2.0 -->
# 可信记忆后端开关设计 Standards/Governance 评审（第 2 轮）

## 评审目标与范围

- 目标: suga 设计 `docs/design/2026-11-26-trusted-memory-backend-design.md`
- 候选身份: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `8639724b`；上轮 candidate `d5dc67ce`；subject path 同上
- 修订版本: Draft 0.3
- 评审轴: Standards/Governance（独立评审者，未参与本候选的撰写或修订）
- 评审者: Claude（Standards/Governance 轴独立子 agent）
- 日期: 2026-11-26
- 排除项: 用户已作的选择本身（“登记 A”“7s4v Q1 选 A，去掉 D5”“7s4v Q2 选 A”）不评；Spec/Executability 轴；实现代码质量；未变章节沿用第 1 轮结论

## 输入与方法

- 完整读取 review payload `tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md` 的 review-contract、planning-core-rigorous、review-candidate-loop、SR1411、review 模板。
- 读取上轮本轴报告 `docs/review/2026-11-26-trusted-memory-backend/standards-review.md`（TS1–TS3），上轮 Spec 轴报告的 finding 列表（TF1–TF5，用于核对处理表）。
- `git diff d5dc67ce..8639724b -- <subject>`，并通读 0.3 全文以核对依赖章节（安全/回滚、决策、Gate）。
- 代码事实抽查（工作树 HEAD `4cc3b606`，subject 与 candidate 相同）：`internal/config/approval_rules.go:114-133`（`parseProjectTrust`）、`internal/wiring/rules.go:356-375`（`ProjectAllowTrusted`）、`internal/config/protected_snapshot.go:140-215`（`protectedKeyDigests`/`canonicalValue`）、`internal/config/settings_registry.go`（`ConfigTypeStringList` 及已有 Protected 列表键 `tools.interpreters`/`tools.read_roots`/`tools.read_deny`）、`internal/wiring/memory.go:258-300,376-390`（`selectProjectBackend`、`gitRoot`）、`internal/wiring/subagent.go:30-34`、`internal/personal/taint.go:52-70`；`git ls-tree 8639724b docs/review/2026-11-26-trusted-memory-backend/`。

## 验收标准

1. TS1–TS3 是否关闭。
2. D6 能否追溯到用户原话“7s4v Q2 选 A”，有无多余机制；与 protected-config-keys 设计、`project_trust` 做法是否一致。
3. 修订记录与处理表是否完整；用户可见差异是否在 Gate 上列出。
4. 只报影响核心验收或安全的问题。

### 核对结论

| 项 | 结论 |
|---|---|
| TS1 | 关闭（设计侧）。第 8 行补评审目录、两轴报告与 `review-ledger.json` 路径；§范围补“受影响项目：仅 suga”。注：`8639724b` 中该目录只有两份报告，`review-ledger.json` 尚未提交（见“剩余风险与后续”，属主 Agent 职责，不记 finding）。 |
| TS2 | 关闭。§方案 6、§安全第 2 条改为“子 agent 不注册 memory 工具，不受影响”，与 `subagent.go:30-34` 一致，已无同类残留；D5 补 `taint_tools`/`taint_on_spill` 例外，与 `taint.go:58-70` 一致，且写明 ExternalOrigin 仍不记。 |
| TS3 | 关闭。§方案 4、6：在 `loadMemoryConfig` 读取校验、经 `MemoryConfig`/`MemoryBackendChoice.Trusted`/`MemorySetup` 传入，`newMemoryRuntime` 不读全局 gkconfig；非法值带键名启动失败，与 `memory.project.backend` 未知值处理同口径；同步更新旧注释。 |
| D6 追溯 | 成立。修订记录 0.3 与 D6 引用原话“7s4v Q2 选 A”；选项记录写明 Q2 的 A=按工作区登记、B=全局，D6 即 A 的直接落地。 |
| D6 必要性 | 无多余机制。新增内容（列表类型、绝对路径校验、整根相等、Windows 大小写、不解析符号链接、非法值报错）都是“按仓库登记”能唯一实现所必需的规则，且全部复用现有设施：`ConfigTypeStringList` 已有（含 Protected 列表键先例 `tools.interpreters` 等），仓库根复用 `selectProjectBackend` 已算出的 `gitRoot`，无新模块、新标记、新状态。整根匹配而非前缀的理由（嵌套 clone）与安全目标直接相关。 |
| 与 protected-config-keys 一致 | 一致。仍归 `relax` 组；静态键自动进入 `protectedKeyDigests`；`canonicalValue` 只对 map 排序，列表按原序 JSON 编码，设计“调整顺序也会报键名”属实。 |
| 与 `project_trust` 一致 | 基本一致：都按绝对路径登记、trim 后校验 `IsAbs`、Clean 后整值匹配；`gitRoot` 注释写明与项目规则用的工作区根同口径。两处有意差异均合理：(1) `project_trust` 是路径→文件 hash 的 map，D6 是纯列表——记忆内容持续变化，绑定 hash 不适用；(2) `project_trust` 用 map 精确查找（Windows 也区分大小写），D6 在 Windows 用 `EqualFold`——只会匹配同一目录的大小写变体，不扩大信任到别的目录，更贴合 `/d` 不区分大小写的实际。设计称“参照 `project_trust`”而未点明 (2) 是偏离，不影响验收与安全，不单列 finding。 |
| 修订记录与处理表 | 完整。0.3 行写明来源（第 1 轮评审 + 用户 Q2 原话）；处理表覆盖 TF1–TF5 与 TS1–TS3 全部 8 项，无遗漏；0.2→0.3 含语义变化，递增合规。 |
| Gate 差异披露 | 未列出（TS4）。 |

### SR1301 安全核对（仅变化部分）

- 鉴权：已核对。`memory.project.trusted` 改为列表后仍 `Protected: true`，会话内 `config set/unset` 拒绝不变；GR110 用例改为列表值并覆盖终端/管道/`SUGA_SESSION_ID` 三种情形。
- 注入防护：已核对，且较 0.2 收紧——未登记仓库（含 clone 的第三方仓库自带 `.gofer/tracker`）仍按外部处理；子目录、父目录不匹配；反向验证含“改成前缀匹配或恒真须失败”。
- 入参校验：已核对。空项、相对路径、非列表（含 `true`）启动报错，失败关闭。
- 秘密、资源归属：不适用（同第 1 轮）。

## 发现

### TS4 [MEDIUM] Gate 未列出 0.3 的用户可见差异

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: §结论与人工计划 Gate（末行）仍只有“本设计待两轴评审（封顶两轮）；批准后才进入实施计划，批准不授权实施。”，与 0.2 相同。0.3 相对用户上次看到的版本有用户可见的接口变化：`memory.project.trusted` 由 bool 改为仓库根目录列表；默认值表述由“false”改为“false / 空列表”；未登记仓库照旧按外部处理；非法值（含写成 `true`）启动失败。SR1411 要求“候选变化时先给简短差异”。
- 影响: 用户在计划 Gate 批准时看不到配置写法已与最初“登记 A”的 bool 形式不同；不改变设计内容与安全结论。差异来自用户本人的 Q2 选择，所以不构成越权。
- 建议: 在 Gate 一节加 2–3 行“相对 0.2 的用户可见差异”：project 键改为根目录列表（D6）；写 `true`/相对路径/空项会启动失败；未登记仓库照旧弹询问。另附一句评审状态（两轴第 2 轮结论、有无开放 `CORE_BLOCKING`）。只改披露文字，不递增版本。

### TS5 [LOW] 回滚与运维两句仍按 bool 写，与方案 4 冲突

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据（同类全部出现点）:
  1. §安全、数据、运维与回滚“数据”条：“回滚：删键或置 false 即恢复”；
  2. 同节“运维”条：“适用场景（只有自己写入的后端才设 true）”。
  - 对照 §方案 4：project 键“不是字符串列表（含写成 `true`）……启动失败”。按同一规则，把 `memory.project.trusted` 置 `false` 也会启动失败。
  - 其余出现点已更新：§背景“默认不可信（false / 空列表）”、D1“project 的形式见 D6”。
- 影响: README/usage 若照这两句写，用户按文档回滚 project 键（写 `false`）会让 suga 启动失败。它失败关闭，不会放宽防护，但回滚说明不成立，用户会遇到。产品行为已由方案 4 定死，修正只是对齐文字，所以标 false。
- 建议: 改为“回滚：删键（project 也可置空列表 `[]`；user 置 false）即恢复”；运维条改为“只把自己写入的后端设为可信（user 设 true，project 列出自己的仓库根）”。

### TS6 [LOW] “工作区根目录”与“仓库根目录”混用

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据（同类全部出现点）: 第 7 行 scope freeze“工作区根目录列表”；第 16 行修订记录“按工作区根目录登记”；第 107 行 Q2 选项记录“按工作区登记”。其余各处（§背景用户可见结果、方案 1、3，D6）都写“仓库根目录”，匹配对象是 `gitRoot` 的结果。
- 影响: 在嵌套仓库或子目录里启动时，工作区目录与 `gitRoot` 不同。用户若按“工作区”登记子目录，按方案 3 的整根匹配不会命中，误报依旧（核心验收对该用户不成立），而且不报错。Q2 选项记录保留用户选择时的原措辞可以理解，但 scope freeze 与修订摘要属于规范性文字。
- 建议: 第 7、16 行改为“仓库根目录（`gitRoot`）列表”；Q2 选项记录保留原措辞，加注“落地为仓库根，见 D6”；文档说明写“按 `suga manifest` 显示的仓库根登记”（方案 3 已有此意）。

## 覆盖缺口与限制

- 只评 Standards/Governance 轴的三项指定范围；Spec/Executability 轴另评，本报告不能单独构成两轴 PASS。
- 增量评审：读 diff 与依赖章节（安全/回滚、决策、Gate），未变章节沿用第 1 轮结论。
- 未运行测试；未核实 `config set` 对 `ConfigTypeStringList` 单个参数的写入语义（GR110 用例 `config set memory.project.trusted <root>` 成功分支的写法），这属于 Spec/Executability 轴。
- 未评 git worktree 场景：每个 worktree 的 `gitRoot` 不同，要分别登记。这是 D6 整根匹配的直接后果，属于可用性问题，不影响安全；是否需要在文档里说明由 Spec 轴或作者判断。

## 结论

PASS（Standards/Governance 轴）。TS1–TS3 均已关闭。D6 能追溯到用户原话“7s4v Q2 选 A”，复用现有列表类型与 `gitRoot`，没有多余机制；与 protected-config-keys 设计一致，与 `project_trust` 的差异有理由。修订记录和处理表覆盖了全部 8 项。无 `CORE_BLOCKING`。TS4–TS6 都是 `CORE_CORRECTIVE`，`user_visible_effect` 都是 false，不触发纠正确认轮；建议在计划批准前直接改进文档，并按“未复核修订”在 Gate 披露。

## 剩余风险与后续

- 设计已接受的残余风险不变：已登记的可信后端被他方写入时，只剩审批规则一道防线；直接改文件要到下次启动才提示。
- `docs/review/2026-11-26-trusted-memory-backend/review-ledger.json` 在 `8639724b` 中还不存在。第 8 行已声明它的位置，主 Agent 须在 Gate 前按 review-candidate-loop 提交 ledger（含两轮 receipt、两轴 reviewer、finding 与 verdict）；ledger 的 axis 或 reviewer 状态不明时不得形成 `REVIEW_PASS`。
- 作者按 TS4–TS6 修正文字（不递增版本），在 Gate 披露为未复核修订。
