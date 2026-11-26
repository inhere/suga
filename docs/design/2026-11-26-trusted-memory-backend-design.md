<!-- template_id: design; template_version: 1.2.0 -->
# 可信记忆后端开关 设计

> 状态：Draft 0.3 / 待人工计划批准

- 思考模式：RIGOROUS；核心目标：用户声明可信的记忆后端，取回内容不再触发外部来源防护。
- scope freeze：`memory.user.trusted`（bool）与 `memory.project.trusted`（工作区根目录列表）、记忆工具的外部来源判定、注册与分类表、文档；其余 deferred。
- 暴露度：Full（改变安全边界：外部来源防护与受保护配置，SR1409）；review budget：两轴评审封顶两轮（issue `suga-7s4v`），报告与 ledger 在 `docs/review/2026-11-26-trusted-memory-backend/`（`standards-review.md`、`spec-review.md`、`review-ledger.json`）；停止条件：无开放 `CORE_BLOCKING` 或到达两轮交用户决定。

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-26 | Claude | 初稿：按用户 2026-11-26 选择 A（原话“登记 A”） |
| 0.2 | 2026-11-26 | Claude | 用户 2026-11-26“7s4v Q1 选 A，去掉 D5”：删除原 D5（manifest 显示）；Q1 定为 A，记为新 D5：可信同时免外部来源与污染，风险补其后果 |
| 0.3 | 2026-11-26 | Claude | 第 1 轮两轴评审处理（见下表）；用户 2026-11-26“7s4v Q2 选 A”：project 作用域改为按工作区根目录登记（D6） |

第 1 轮处理：

| 发现 | 处理 |
|---|---|
| TF1 | project 键改为根目录列表，写明匹配、报错、分类与启动比较（D6、方案 1–2）；第三方仓库用例进验证与反向验证 |
| TF2、TS2 | 子 agent 不注册 memory 工具，改为“不受影响” |
| TF3、TS2 | D5 写明 `taint_tools`/`taint_on_spill` 例外 |
| TF4 | 观测点改为审批事件 `origin_judge` 属性与询问文本；写明 ptye2e 假 exec 夹具；GR110 替换 `configStdinIsTerminal` |
| TF5、TS3 | 在 `loadMemoryConfig` 读取校验，经 `MemoryConfig`/`MemorySetup` 传入；更新旧注释 |
| TS1 | 补评审目录、ledger 与受影响项目 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 背景与目标

用户在 chat 里用 `memory get` 读到 gofer 托管记忆中的运维备忘（`sshc run … pct exec …`），模型照着执行时被外部来源防护拦下并询问“参数里有逐字复制自外部来源的文本，来源工具：memory”。这些备忘是用户自己写的，询问是误报。

用户可见结果：在 `config.yaml` 的 `memory.project.trusted` 列出自己的仓库根目录（或写 `memory.user.trusted: true`）后，这些仓库（或 user 作用域）的记忆照抄执行不再弹外部来源询问，也不再交裁决；默认不可信（false / 空列表），行为不变；模型在会话里不能经 `suga config set` 打开它；文件里改了它，下次启动会提示；读可信记忆后写记忆也不再要求确认；未登记的仓库（如 clone 的第三方仓库自带 `.gofer/tracker`）照旧按外部处理。

## 名词

- **外部来源标记**：工具结果的 `Metadata["external"]=true`。
- **ExternalOrigin**：会话内外部结果文本集合；写或执行调用的参数含 40 字节以上逐字片段时先裁决，再问人或拒绝。
- **污染（taint）**：会话读过外部内容后置 `tainted_at/tainted_by`，此后写记忆按 `memory.write_tainted` 处理（D7）。

## 范围与非目标

范围（受影响项目：仅 suga）：两个配置键；记忆工具 get/search 的外部来源判定；注册表与分类表；README 与 `docs/usage/gofer-personal-agent.md` 说明。

非目标：不按条目或按写入者区分可信；不改 ExternalOrigin 的窗口与裁决；不改记忆写入治理本身；不改 `files` 后端（本来就不标外部）；不改 MCP 等其他外部工具；manifest 不显示，用 `suga config get` 查看。

## 已确认事实与规范

- `pkg/agentcore/tools/native/memory.go:150-156,164,170,327-333`：get/search 结果在 `external(scope)` 为真时带 `external=true`；后端不是 `files`（含 `Backend` 为 nil 或未知名）一律为真。
- `internal/wiring/memory_runtime.go:158-169`：wiring 经 `Backend` 回调把作用域映射到后端名。
- `internal/wiring/origin_guard.go:41-50`：带 `external` 标记的结果记入 ExternalOrigin。
- `internal/personal/taint.go:52-70`：同一个 `external` 标记也让会话置为已污染。**结论：只去掉标记会同时关掉两者**，正合 D5。
- `internal/wiring/memory.go:225-237,258-300`：user 作用域只能是 files 或 exec；project 可为 files、gofer、exec，未配置时仓库根（`gitRoot`）有 `.gofer/tracker` 即自动选 gofer，含 clone 的第三方仓库。gofer 记忆可由任何能调用 `gofer memory set` 的 agent 写入。
- `internal/wiring/subagent.go:30-34`：子 agent 不注册 memory 工具。`internal/personal/taint.go:58-70`：列入 `memory.taint_tools`/`taint_on_spill` 的工具不看 metadata 也会污染。
- 先例：`tools.approval_rules.project_trust` 按绝对工作区路径登记信任（`internal/config/approval_rules.go:114-128`，`internal/wiring/rules.go:359-366`）。
- 受保护配置：`docs/design/2026-11-11-protected-config-keys-design.md`（v0.56.0 已实施）——`ConfigKey.Protected`、会话内 `config set/unset` 拒绝（退出码 2）、启动时变化提示、显式分类表 `internal/config/settings_protected_test.go`。
- 规范：GR108（装配只装配）、GR110（CLI 经真实分发测试）、SR1409。

## 总体方案

1. 注册（`memory.<scope>.backend` 旁，均 `Writable: true, Protected: true`）：`memory.user.trusted` 为 `ConfigTypeBool`；`memory.project.trusted` 为 `ConfigTypeStringList`，每项一个仓库根目录绝对路径。
2. 分类表两键归 `relax` 组（关闭约束能力的检查，与 `tools.ask_on_external_origin`、`memory.taint_tools` 同组）；Protected 使其自动进入启动比较与 `config set` 拒绝。启动比较按原始值的规范 JSON（列表保持顺序），增删改任一项、调整顺序都会报出键名。
3. 匹配（D6）：列表项 trim 后须为绝对路径，`filepath.Clean`；当前项目的仓库根取 `selectProjectBackend` 已算出的 `gitRoot` 结果（Abs + Clean）。只认整根相等，不含子目录（工作区根本身可能是含多个嵌套仓库的 git 仓库，前缀匹配会把嵌套的 clone 一并信任）；Windows 下不区分大小写（`strings.EqualFold`），其他平台区分；不解析符号链接，按 `suga manifest` 显示的记忆位置登记。host-key 路径（固定 files）不适用。
4. 非法值：user 键不是布尔、project 键不是字符串列表（含写成 `true`）、有空项或相对路径时，`loadMemoryConfig` 返回带键名的错误，启动失败（同 `memory.project.backend` 未知值）。
5. `native.MemoryToolOptions` 增 `Trusted func(memory.Scope) bool`；`external(s)` 在 `Trusted != nil && Trusted(s)` 时返回 false，否则沿用现有判定（nil 仍按外部，失败关闭）；同步更新 `Backend` 字段与 `external()` 的注释。agentcore 不读配置。
6. 两键在 `loadMemoryConfig` 读取校验，放进 `MemoryConfig`；`buildMemory` 把匹配结果写入 `MemoryBackendChoice.Trusted`，`newMemoryRuntime` 从 `MemorySetup` 取值构造回调，不读全局 gkconfig。子 agent 不注册 memory 工具，不受影响。
7. 不加新标记：去掉 `external` 后 ExternalOrigin 与 taint 同时不再触发（D5），`taint.go` 不改。

## 架构

改动点：`internal/config/settings_registry.go`（注册）、`internal/config/settings_protected_test.go`（分类）、`pkg/agentcore/tools/native/memory.go`（判定与注释）、`internal/wiring/memory.go`（读取、校验、匹配）、`internal/wiring/memory_runtime.go`（传值）。分层沿用现状：配置只在 wiring 读，agentcore 只收回调（GR108）。

## 关键流程

```text
memory get/search(scope)
  └─ external(scope) = !trusted(scope) && backend(scope) != files
       trusted(project) = 仓库根 ∈ memory.project.trusted；trusted(user) = memory.user.trusted
       ├─ true  → external=true → ExternalOrigin 记录 + 会话污染（现状）
       └─ false → 无标记 → 不记录、不污染（同 files 后端）
shell_exec(参数含该记忆片段) → originGuard 无命中 → 照常走 tools.approval 规则
```

## 安全、数据、运维与回滚

- **取舍**：可信后端被别的 agent（或经 gofer 的任何写入方）写入恶意指令时，suga 不再有外部来源防护；照抄执行只剩 `tools.approval` 与审批规则，`approval: auto`/trust 档下可能直接执行。这是用户以默认 false、按作用域与仓库显式登记换来的便利，文档如实写明。
- D5 的后果：读完可信记忆再写记忆时不再按 `memory.write_tainted` 要求确认，恶意内容可经记忆自我扩散。子 agent 不注册 memory 工具，不受影响。
- 防模型自开：Protected 键，会话内 `config set/unset` 被拒；直接改文件由启动提示兜底（强度同受保护配置设计 D7）。
- 数据：不迁移；记忆条目与来源记录不变。回滚：删键或置 false 即恢复；代码回滚无残留。
- 运维：README 与 usage 文档写明默认值、风险与适用场景（只有自己写入的后端才设 true）。

## 决策

- **D1** 采用方案 A：每个作用域一个可信开关，默认不可信（用户 2026-11-26“登记 A”；project 的形式见 D6）。
- **D2** 两键为 Protected，归分类表 `relax` 组。
- **D3** 只影响 get/search 结果的外部来源标记；写入治理、ExternalOrigin 机制本身不变。
- **D4** 判定在 agentcore 以回调注入，未注入按不可信（失败关闭）。
- **D5** 可信后端取回的内容既不记入外部来源，也不把会话标为已污染，与 `files` 后端同等对待（用户 2026-11-26“Q1 选 A”）。例外：用户把 `memory` 列入 `memory.taint_tools` 或 `memory.taint_on_spill` 时仍按该键污染（ExternalOrigin 仍不记）。
- **D6** project 作用域按仓库根目录登记：`memory.project.trusted` 为根目录列表，整根匹配，参照 `project_trust`；user 作用域仍为布尔（用户 2026-11-26“7s4v Q2 选 A”）。

## 待确认事项

无。

选项记录：Q1 可信只免 ExternalOrigin（B，保留写记忆确认，多一个标记）或同时免 taint（A，与 files 同等、改动最小）；已决 A（D5）。Q2 project 可信是全局（B，信任落到任意仓库自带 tracker）还是按工作区登记（A）；已决 A（D6）。

## 验证要求

- 单元：记忆工具 trusted 作用域 get/search 不带 `external`；未 trusted 的 gofer/exec 仍带；`Trusted` 为 nil 仍带；files 不变。`loadMemoryConfig`：合法列表、`true`/标量/空项/相对路径报错；匹配：整根相等、子目录与父目录不匹配、Windows 大小写（`runtime.GOOS` 分支）、尾部分隔符经 Clean 后相等。
- 分类：两键在 `relax` 组，`TestProtectedClassification` 通过；普通 YAML 写法改动 project 列表时启动提示报出键名。
- GR110：经 `app.Run([]string{"config","set","memory.project.trusted",<root>})` 真实分发：替换 `configStdinIsTerminal` 为 true 且无 `SUGA_SESSION_ID` 时成功；默认管道 stdin 与带 `SUGA_SESSION_ID` 时均退出码 2、文件不变。
- 链路：wiring 级测试用假 exec 后端（判定与后端无关），当前仓库根在列表里时 ExternalOrigin 为空、照抄 40 字节以上的 `shell_exec` 不被 originGuard 标记、会话无 `tainted_at`；对照两组：未登记（模拟第三方仓库自带 tracker，列表只含另一个根）与列表含其父目录，均仍被标记且有 `tainted_at`；`memory` 列入 `taint_tools` 时有 `tainted_at` 但 ExternalOrigin 为空。
- 端到端：`test/ptye2e` 加 chat 场景。假 exec 后端是一个小 Go 程序（如 `test/ptye2e/testdata/fakememexec`），与 suga 二进制一样在测试开始时 `go build`，按 exec 协议 v1 从夹具文件应答 get/search，不用 shell 脚本，Linux 与 Windows ConPTY 同跑。断言：已登记时该 `shell_exec` 的审批事件（`suga inspect trace --json`）无 `origin_judge` 属性，屏幕不出现“逐字复制自外部来源”询问；未登记仓库对照两者都有。加入 `make smoke-go`。另在主机 chat 配 gofer 后端复现一次原场景。
- 反向验证（确认能编译，失败不是 build failed）：去掉 `external()` 的 trusted 分支；把匹配改成前缀匹配或恒真（第三方仓库与父目录用例须失败）；去掉 Protected；去掉分类表条目；分别确认对应测试失败。

## 结论与人工计划 Gate

本设计待两轴评审（封顶两轮）；批准后才进入实施计划，批准不授权实施。
