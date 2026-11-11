# 评审报告：suga 可信模式/外部来源裁决/敏感路径禁读 实施计划 — Spec/Executability 轴 第 4 轮（blocking closure）

## 候选四元组

| 项 | 值 |
|---|---|
| Git root | /d/work/inhere/my-tools-dev/inhere-tools/suga |
| commit | d51491a15f7420fd99d2da97e699a1b0e45ce4dc（HEAD 已核对一致） |
| subject | docs/plans/2026-11-10-trust-mode-origin-judge-read-deny-plan.md |
| revision | Draft 0.4（计划正文由 5c7bc582 修订，d51491a1 只加评审台账） |

- 评审者：独立评审子 agent（Claude Opus 5.5），Spec/Executability 轴；REPORT_ONLY，未改仓库文件、未提交。
- 上轮：候选 466e0dc8（0.3），报告 docs/review/2026-11-10-trust-mode-origin-judge-read-deny-plan/spec-review.md（F23 阻断，F24–F26 LOW）。

## 实际读取范围

- `git diff 466e0dc8..d51491a1 -- <subject>`（修订记录 0.4 表、scope、波次表、T1 动作 3/测试/反向验证、T2 Owner/动作 8/10、可追溯性表）；计划内 T5、T6 Owner 行（核对 F26 文件相交）。
- 上轮 spec-review.md 的 F21–F26 段落。
- 代码：internal/personal/segment_handoff.go:40-60、:116-175（recentRequests，含 :128 EntryUser 过滤、:134-138 dropMasked、:154 判定、:159 metaInjected）、:375-400（injectedStorage）；context.go:30-55（injectedItemKeys/isInjectedItem）；segment.go:350-362、:425-436、:367-373、:490-499；turncontext.go:50-65；runner.go:38-50、Injected 相关行；wiring/runtime.go:712-726；acpserver/i18n.go:5-20、server.go:140-150、:300-315；personal 包内所有 `meta*` 常量与 `Role: "user"` 构造点；EntryUser 写入点；replay.go:150-175；admit.go:95-105。
- 未读：设计文档、standards 轴报告、ledger JSON（超出本轴范围）。

## 结论

**PASS**：无 CORE_BLOCKING。F23 关闭；F24、F25、F26 关闭；修订未引入阻断性错误。新发现 1 项 LOW（F27，非阻断）。

## 上轮处置核对表

| 项 | 判定 | 依据 |
|---|---|---|
| F23 | 关闭 | T1 动作 3 现写明按 segment_handoff.go:154 排除 `isContextUpdate`、`isSegmentContext`、`isInjectedItem` 与 Metadata `metaInjected`，与 :154/:159 现成判定一致。覆盖核对：(1) 注入回合消息——`injectedStorage` 在持久化时对注入 run 的首条 user 消息打 `suga.injected`（:384-397），被排除；当前注入回合若尚未持久化则不在列表，已持久化也被排除。(2) steer/task notice/undo/中断说明——`injectedItemKeys` 覆盖。(3) context update——实际存为 `EntryContextUpdate` 的 record（无 role），回放消息带 `metaContextUpdate`，两种形态都不会被当作请求。(4) 分段上下文——`metaSegmentContext`，被排除。其余 `Role:"user"` 构造（摘要 context.go:249、hookContext/ephemeral :437/:443、deadline hint、handoff.go:48）均为请求期构造、不以 EntryUser 落库。测试“连续两个注入回合”“只有注入回合为空串”可直接击中 F23 因果链；反向验证“去掉 metaInjected 过滤后连续两个注入回合用例失败”成立（无过滤时返回注入回合 1 正文）。`isContextUpdate`/`isSegmentContext` 无专门反向用例，但二者在 EntryUser 中本不出现，属防御性判定，不影响证明 |
| F24 | 关闭 | `EventTurnContext` 位于 turncontext.go:57，attrs 已有取自 `r.cfg.Mode` 的 `mode`，增 `approval_mode` 同法可行；`personal.Config` 在 runner.go:43，runtime.go:720 处为 Config 字面量赋值（`Mode: runMode(allowWrite)` 同处）；turncontext.go、runner.go 已入 scope 与 T2 Owner |
| F25 | 关闭 | `ServeOption func(*Agent)`（i18n.go:8）与 `WithTranslator` 写法存在，`Serve(..., opts ...ServeOption)`（server.go:144）；`WithApprovalMode` 写入 Agent 字段，server.go:310 构造 `permissionHook` 时位于 Agent 方法内可读该字段，可实施 |
| F26 | 关闭 | 波次 4 改为 T5 依赖 T2、T6 都合入；T5 与 T6 共改 runtime_build.go、settings_registry.go、runtime.go，与 T2 共改 origin_guard.go、observe.go、runtime.go，均被顺序依赖覆盖（行内文件列举不全但依赖关系已足够，不另记） |

## 新发现

### F27 [LOW][NON_BLOCKING / ADVISORY] turn_prompt 只引用 :154 的判定，未带上 recentRequests 的被撤销条目剔除

- 事实：recentRequests 在 :154 判定之前还有 :128 `EntryFilter{Types: EntryUser}` 与 :134-138 `dropMasked`（剔除 rewind/undo 遮蔽区间）。T1 动作 3 只写“按 :154 的过滤”。
- 影响：类型过滤缺失无害（其他条目类型 JSON 无 role，解析后被 `m.Role != "user"` 跳过）。缺 `dropMasked` 时，用户撤销（rewind）掉的最后一条请求仍可能被取作“本次用户请求”。该文本仍出自用户本人，不构成 F23 那样的不可信文本进入可信块，故非安全阻断，只是裁决依据可能是用户已撤回的意图。
- 建议（可选）：T1 动作 3 改为“同 recentRequests（:128 类型过滤、:134 dropMasked、:154 判定）”，或在实现时复用其逻辑。

## 其他已核实

- 修订未扩大范围：新增 Owner 文件（turncontext.go、runner.go Config）均为 F24 所需最小落点；可追溯性表改为 T8、G1 与 S15 一致。
- T1 已改 runner.go（Run 调用点），T2 再改 runner.go Config，T1→T0→T2 顺序依赖，无并行冲突。
