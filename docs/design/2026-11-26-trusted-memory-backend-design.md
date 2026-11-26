<!-- template_id: design; template_version: 1.2.0 -->
# 可信记忆后端开关 设计

> 状态：Draft 0.2 / 待人工计划批准

- 思考模式：RIGOROUS；核心目标：用户声明可信的记忆后端，取回内容不再触发外部来源防护。
- scope freeze：两个 bool 配置键、记忆工具的外部来源判定、注册与分类表、文档；其余 deferred。
- 暴露度：Full（改变安全边界：外部来源防护与受保护配置，SR1409）；review budget：两轴评审封顶两轮（issue `suga-7s4v`）；停止条件：无开放 `CORE_BLOCKING` 或到达两轮交用户决定。

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-26 | Claude | 初稿：按用户 2026-11-26 选择 A（原话“登记 A”） |
| 0.2 | 2026-11-26 | Claude | 用户 2026-11-26“7s4v Q1 选 A，去掉 D5”：删除原 D5（manifest 显示）；Q1 定为 A，记为新 D5：可信同时免外部来源与污染，风险补其后果 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 背景与目标

用户在 chat 里用 `memory get` 读到 gofer 托管记忆中的运维备忘（`sshc run … pct exec …`），模型照着执行时被外部来源防护拦下并询问“参数里有逐字复制自外部来源的文本，来源工具：memory”。这些备忘是用户自己写的，询问是误报。

用户可见结果：在 `config.yaml` 写 `memory.project.trusted: true`（或 `memory.user.trusted: true`）后，该作用域的记忆照抄执行不再弹外部来源询问，也不再交裁决；默认 false，行为不变；模型在会话里不能经 `suga config set` 打开它；文件里改了它，下次启动会提示；读可信记忆后写记忆也不再要求确认。

## 名词

- **外部来源标记**：工具结果的 `Metadata["external"]=true`。
- **ExternalOrigin**：会话内外部结果文本集合；写或执行调用的参数含 40 字节以上逐字片段时先裁决，再问人或拒绝。
- **污染（taint）**：会话读过外部内容后置 `tainted_at/tainted_by`，此后写记忆按 `memory.write_tainted` 处理（D7）。

## 范围与非目标

范围：`memory.user.trusted`、`memory.project.trusted`；记忆工具 get/search 的外部来源判定；注册表与分类表；README 与 `docs/usage/gofer-personal-agent.md` 说明。

非目标：不按条目或按写入者区分可信；不改 ExternalOrigin 的窗口与裁决；不改记忆写入治理本身；不改 `files` 后端（本来就不标外部）；不改 MCP 等其他外部工具；manifest 不显示，用 `suga config get` 查看。

## 已确认事实与规范

- `pkg/agentcore/tools/native/memory.go:150-156,164,170,327-333`：get/search 结果在 `external(scope)` 为真时带 `external=true`；后端不是 `files`（含 `Backend` 为 nil 或未知名）一律为真。
- `internal/wiring/memory_runtime.go:158-169`：wiring 经 `Backend` 回调把作用域映射到后端名。
- `internal/wiring/origin_guard.go:41-50`：带 `external` 标记的结果记入 ExternalOrigin。
- `internal/personal/taint.go:52-70`：同一个 `external` 标记也让会话置为已污染。**结论：只去掉标记会同时关掉两者**，正合 D5。
- `internal/wiring/memory.go:225-237`：user 作用域只能是 files 或 exec；project 可为 files、gofer、exec。gofer 记忆可由任何能调用 `gofer memory set` 的 agent 写入。
- 受保护配置：`docs/design/2026-11-11-protected-config-keys-design.md`（v0.56.0 已实施）——`ConfigKey.Protected`、会话内 `config set/unset` 拒绝（退出码 2）、启动时变化提示、显式分类表 `internal/config/settings_protected_test.go`。
- 规范：GR108（装配只装配）、GR110（CLI 经真实分发测试）、SR1409。

## 总体方案

1. 注册两个键：`{Pattern: "memory.user.trusted", Type: ConfigTypeBool, Writable: true, Protected: true}`，project 同理，放在 `memory.<scope>.backend` 旁。
2. 分类表把两个键归入 `relax` 组（“关闭约束能力的检查”，与 `tools.ask_on_external_origin`、`memory.taint_tools` 同组）；Protected 后自动进入启动比较与 `config set` 拒绝，无需新代码。
3. `native.MemoryToolOptions` 增 `Trusted func(memory.Scope) bool`；`external(s)` 在 `Trusted != nil && Trusted(s)` 时返回 false，否则沿用现有判定（nil 仍按外部，失败关闭）。agentcore 不读配置。
4. wiring 在 `newMemoryRuntime` 读 `gkconfig.Bool("memory.<kind>.trusted")` 传入回调；子 agent 同路径构建，规则相同。
5. 不加新标记：去掉 `external` 后 ExternalOrigin 与 taint 同时不再触发（D5），`taint.go` 不改。

## 架构

改动点：`internal/config/settings_registry.go`（注册）、`internal/config/settings_protected_test.go`（分类）、`pkg/agentcore/tools/native/memory.go`（判定）、`internal/wiring/memory_runtime.go`（传值）。分层沿用现状：配置只在 wiring 读，agentcore 只收回调（GR108）。

## 关键流程

```text
memory get/search(scope)
  └─ external(scope) = !trusted(scope) && backend(scope) != files
       ├─ true  → external=true → ExternalOrigin 记录 + 会话污染（现状）
       └─ false → 无标记 → 不记录、不污染（同 files 后端）
shell_exec(参数含该记忆片段) → originGuard 无命中 → 照常走 tools.approval 规则
```

## 安全、数据、运维与回滚

- **取舍**：可信后端被别的 agent（或经 gofer 的任何写入方）写入恶意指令时，suga 不再有外部来源防护；照抄执行只剩 `tools.approval` 与审批规则，`approval: auto`/trust 档下可能直接执行。这是用户以默认 false、按作用域、显式打开换来的便利，文档如实写明。
- D5 的后果：读完可信记忆再写记忆时不再按 `memory.write_tainted` 要求确认，恶意内容可经记忆自我扩散；子 agent 的 `Tainted` 也不会因此置位，父会话收不到污染信号。
- 防模型自开：Protected 键，会话内 `config set/unset` 被拒；直接改文件由启动提示兜底（强度同受保护配置设计 D7）。
- 数据：不迁移；记忆条目与来源记录不变。回滚：删键或置 false 即恢复；代码回滚无残留。
- 运维：README 与 usage 文档写明默认值、风险与适用场景（只有自己写入的后端才设 true）。

## 决策

- **D1** 采用方案 A：每个作用域一个 bool 可信开关，默认 false（用户 2026-11-26“登记 A”）。
- **D2** 两键为 Protected，归分类表 `relax` 组。
- **D3** 只影响 get/search 结果的外部来源标记；写入治理、ExternalOrigin 机制本身不变。
- **D4** 判定在 agentcore 以回调注入，未注入按不可信（失败关闭）。
- **D5** 可信后端取回的内容既不记入外部来源，也不把会话标为已污染，与 `files` 后端同等对待（用户 2026-11-26“Q1 选 A”）。

## 待确认事项

无。

选项记录：Q1 可信只免 ExternalOrigin（B，保留写记忆确认，多一个标记）或同时免 taint（A，与 files 同等、改动最小）；已决 A（D5）。

## 验证要求

- 单元：记忆工具 trusted 作用域 get/search 不带 `external`；未 trusted 的 gofer/exec 仍带；`Trusted` 为 nil 仍带；files 不变。
- 分类：两键在分类表 `relax` 组，`TestProtectedClassification` 通过；`config.yaml` 改动该键时启动提示报出键名。
- GR110：经 `app.Run([]string{"config","set","memory.project.trusted","true"})` 真实分发，带 `SUGA_SESSION_ID` 时退出码 2 且文件不变；终端无会话时成功。
- 链路：wiring 级测试用假 exec/gofer 后端，trusted=true 时读记忆后 ExternalOrigin 为空、照抄 40 字节以上的 `shell_exec` 不被 originGuard 标记、trace 无外部来源记录；trusted=false 对照仍被标记。断言会话无 `tainted_at`，对照组有。
- 反向验证：去掉 `external()` 的 trusted 分支、去掉 Protected、去掉分类表条目，分别确认对应测试失败（确认能编译，失败原因不是 build failed）。
- 端到端：`test/ptye2e` 加一个 chat 场景（假模型 + 假 exec 后端）：trusted=true 时照抄记忆的 `shell_exec` 不弹外部来源询问，`suga inspect trace --json` 无外部来源事件；trusted=false 对照弹询问；加入 `make smoke-go` 并做反向验证。另在主机 chat 配 gofer 后端复现一次原场景。

## 结论与人工计划 Gate

本设计待两轴评审（封顶两轮）；批准后才进入实施计划，批准不授权实施。
