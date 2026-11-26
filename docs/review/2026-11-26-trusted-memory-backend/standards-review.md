<!-- template_id: review; template_version: 1.2.0 -->
# 可信记忆后端开关设计 Standards/Governance 评审（第 1 轮）

## 评审目标与范围

- 目标: suga 设计 `docs/design/2026-11-26-trusted-memory-backend-design.md`
- 候选身份: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `d5dc67cefcd596f360d4ebfefef2948fe5f1e096`；subject path `docs/design/2026-11-26-trusted-memory-backend-design.md`；document revision Draft 0.2
- 修订版本: Draft 0.2
- 评审轴: Standards/Governance（独立评审者，未参与本候选的撰写或修订）
- 评审者: Claude（Standards/Governance 轴独立子 agent）
- 日期: 2026-11-26
- 排除项: 用户已作出的选择本身（“登记 A”、“7s4v Q1 选 A，去掉 D5”）不评；Spec/Executability 轴；实现代码质量

## 输入与方法

- 完整读取：review payload `tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md`（含 review-contract、planning-core-rigorous、review-candidate-loop、SR1301、SR1409、GR107–GR110、SR2004）；design payload `tmp/idev-std/d/LysoVXM1D86b1PtHtpG8jg/payload.md` 的 design-contract 与 design 模板（其余资源与前者同 hash）；`AGENTS.md`；设计全文（`git show d5dc67ce:<subject>`）；已批准设计 `docs/design/2026-11-11-protected-config-keys-design.md`（0.8）。
- 代码事实抽查（工作树，与 candidate 同 HEAD）：`pkg/agentcore/tools/native/memory.go`（`external()`、get/search 结果标记）、`internal/wiring/memory_runtime.go`（`newMemoryRuntime`、`loadMemoryWritePolicy`）、`internal/wiring/memory.go`（`loadMemoryConfig`、后端选择）、`internal/wiring/origin_guard.go`、`internal/personal/taint.go`、`internal/wiring/subagent.go`、`internal/wiring/manifest.go:613`、`internal/config/settings_registry.go`、`internal/config/protected_snapshot.go`、`internal/config/settings_protected_test.go`；全仓 grep `Metadata["external"]` 的消费方。

## 验收标准

- design-contract 必需内容齐全；
- 暴露度定档符合 SR1409；
- 与 protected-config-keys 设计的分类原则、分组、启动比较一致；
- SR1301 逐项核对；放宽类改动风险如实、回滚可行；
- 分层：agentcore 不读配置，经 wiring 回调注入（AGENTS.md 分层、GR108）；
- 无追溯不到核心目标的新机制（SR1403/SR1405/SR1410）。

### 核对结论

| 项 | 结论 |
|---|---|
| design-contract | 思考模式/核心目标/scope freeze/budget/停止条件、修订记录（0.2 语义变化递增合规）、背景与用户可见结果、名词、范围与非目标、事实与规则 ID、方案与架构、流程图、安全数据运维回滚、决策、待确认、结论与 Gate 均在。缺“受影响项目”一行与评审目录/ledger（见 TS1）。 |
| 暴露度 Full | 成立。候选改变安全边界（外部来源防护、受保护配置），SR1409 明确此类走 Full、不入本机工具档；suga 已有发布 tag（protected 设计记 v0.55.0/v0.56.0）。 |
| 分类原则 | 一致。两键关闭“约束执行能力的检查”（外部来源防护 + 污染），符合 protected 设计 §1 原则“关闭约束这些能力的检查与上限”。 |
| 分组 | 一致。`relax` 组已存在（`settings_protected_test.go:198-226`），同组含 `tools.ask_on_external_origin`、`memory.taint_tools`、`memory.write_tainted`；`memory.<scope>.backend` 在 `exec` 组，两键归 `relax` 更贴切。 |
| 启动比较 | 一致，“无需新代码”属实：`protectedKeyDigests` 遍历 `ProtectedConfigKeys()`（`protected_snapshot.go:140-152`），静态键自动进入比较；`config set` 拒绝走既有 Protected 判定。升级后文件未设该键两侧均“未设置”，不误报（protected 设计 §3）。 |
| 分层 | 符合。`Trusted func(memory.Scope) bool` 与现有 `Backend` 回调同构；nil 按不可信失败关闭；agentcore 不读配置。配置读取位置有偏离（TS3）。 |
| 新机制追溯 | 无多余机制。0.2 已删 manifest 显示；不加新标记（D5 直接复用去掉 `external` 的效果），全仓 `Metadata["external"]` 只有 `taint.go:62` 与 `origin_guard.go:47` 两个消费方（`agent.go:157` 为子 agent 报告，与记忆无关），设计“只去掉标记会同时关掉两者”属实。 |
| 回滚 | 可行。删键或置 false 即恢复；旧版本不在加载时拒绝未知键（未知键只在 `MatchConfigKey`/`config set` 报错），代码回滚后残留 `trusted: true` 被忽略、回到默认外部判定（失败安全）；无数据迁移。 |

### SR1301 安全核对

- 鉴权（认证与授权）：已核对。两键 `Protected: true`，会话内（`SUGA_SESSION_ID` 非空或 stdin 非终端）`config set/unset` 退出码 2；直接改文件由启动提示兜底，强度与 protected 设计 D7 相同，设计 §安全第 3 条如实写明。
- 注入防护：已核对。本改动是对提示注入防线的放宽：设计如实写出可信后端被他方写入恶意指令后只剩 `tools.approval`（auto/trust 档可能直接执行），以及 D5 后果（写记忆不再按 `memory.write_tainted` 确认、可经记忆自我扩散）；默认 false、按作用域、需人显式开启。无命令拼接。
- 秘密不进日志/输出/仓库：不适用（布尔开关，不触及秘密；启动提示只报键名）。
- 入参校验：已核对，有一处小偏离（TS3：读取方式未沿用现有带校验的读取）。
- 资源归属校验：不适用（单用户本机配置，无他人资源）。

## 发现

### TS1 [MEDIUM] Full 档评审仪式与受影响项目未写定

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: 设计第 8 行只写“两轴评审封顶两轮（issue `suga-7s4v`）”，未给评审目录与 ledger；对照 protected 设计第 58 行写明 `docs/review/2026-11-11-protected-config-keys/`。review-candidate-loop 要求 Full subject 的跟踪评审落在 `docs/review/<subject>/review-ledger.json` 与每轴固定报告，ledger 的 axis/reviewer/open blocker 任一 unknown 不得 PASS。另 design-contract 要求“受影响项目”，§范围与非目标未写（实际只有 suga 自身；gofer、xhen 无接口变化）。
- 影响: 不影响设计内容与安全；但按 Full 规则，缺 ledger 时本轮结论无法形成可复用的 `REVIEW_PASS`，计划 Gate 上缺证据落点。
- 建议: 在第 8 行补一句评审目录（如 `docs/review/2026-11-26-trusted-memory-backend/`，含 ledger 与两轴报告）；§范围补“受影响项目：仅 suga；gofer、xhen 无接口变化”。属元信息补充，不递增版本。

### TS2 [LOW] 子 agent 与 `memory.taint_tools` 的事实陈述不准

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据（同类全部出现点）:
  1. §总体方案 4（第 52 行）“子 agent 同路径构建，规则相同”；
  2. §安全第 2 条（第 72 行）“子 agent 的 `Tainted` 也不会因此置位，父会话收不到污染信号”；
  - 事实：`internal/wiring/subagent.go:30-32` 注释“Sub-agents get no memory tools”，子 agent 根本读不到记忆，这两句描述的路径不存在；记忆工具只在 `runtime.go:570`（主会话）与 `manifest.go:613`（manifest）构建。
  3. D5（第 83 行）与关键流程（第 65 行）称可信内容“不把会话标为已污染”；但 `taint.go:58-70` 中 `memory.taint_tools` 含 `memory`，或 `memory.taint_on_spill` 含 `memory` 且结果被外置时，仍会污染——这是用户显式配置，行为本身正确，但 README/usage 若照设计原文写会与实际不符。
- 影响: 计划可能为不存在的子 agent 路径安排测试；文档对 D5 的例外漏写。不改变核心行为与安全结论。
- 建议: 删去 1、2 中子 agent 的说法（如需提 fork，按 fork 设计核实后再写）；D5 补半句“`memory.taint_tools`/`taint_on_spill` 显式列入 `memory` 时仍按其规则污染”，文档同步。

### TS3 [LOW] 配置读取位置偏离现有模式，未写取值校验

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: 设计 §总体方案 4（第 52 行）“wiring 在 `newMemoryRuntime` 读 `gkconfig.Bool(...)`”。现状 `newMemoryRuntime` 不读配置，记忆相关配置在 `loadMemoryConfig`（`internal/wiring/memory.go:55-76`）与 `loadMemoryWritePolicy`（`memory_runtime.go:50-68`，非法值返回错误）中集中读取后以结构体传入；`newMemoryRuntime` 还被 `manifest.go:613` 调用。
- 影响: 在构造函数里读全局配置使 wiring 级测试依赖全局 gkconfig、两条构建路径读取口径分散；非法值（如 `trusted: maybe`）的处理未定义（`gkconfig.Bool` 是否静默当 false 未写明，虽失败关闭但用户无提示）。属结构一致性问题，按 review-contract 计划批准前修正。
- 建议: 改为在 `loadMemoryConfig`（或 `MemoryConfig`）中读取两键并随 setup/policy 传入 `newMemoryRuntime`；写明非法值按现有 `read()` 方式报错（或注册表 Reader 校验），与 `memory.write` 一致。

## 覆盖缺口与限制

- 只做 Standards/Governance 轴；Spec/Executability 轴另评，本报告不能单独构成两轴 PASS。
- 代码事实抽查基于工作树（HEAD 即 candidate，仅有一个无关未跟踪计划文件）；未运行测试。
- 未核实 `gkconfig.Bool` 对非布尔字符串的具体行为（TS3 已按“未定义”处理）。
- 未审 background fork 设计中 fork 会话是否持有记忆工具；若有，D5 对 fork 的污染继承影响需由作者按 fork 设计补核（不阻断本设计）。

## 结论

PASS（Standards/Governance 轴）。无 `CORE_BLOCKING`：暴露度 Full 成立，分类原则/分组/启动比较与 protected-config-keys 设计一致，SR1301 逐项通过，放宽风险（含 D5 后果）如实，回滚可行，分层符合，无追溯不到核心目标的机制。三项 `CORE_CORRECTIVE` 均 `user_visible_effect: false`，不触发纠正确认轮，建议计划批准前顺手修正。

## 剩余风险与后续

- 设计已接受的残余风险：可信后端被他方写入时只剩审批规则一道防线；直接改文件只在下次启动提示。
- 后续：作者按 TS1–TS3 修订（TS1 为元信息，不递增版本）；主 Agent 将本报告落入 Full 档评审目录与 ledger，并等待 Spec/Executability 轴结论后进入人工计划 Gate。
