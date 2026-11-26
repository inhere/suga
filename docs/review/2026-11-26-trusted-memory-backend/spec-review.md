<!-- template_id: review; template_version: 1.2.0 -->
# 可信记忆后端开关 设计评审（Spec/Executability 轴，第 1 轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `d5dc67ce`；subject `docs/design/2026-11-26-trusted-memory-backend-design.md`；document revision Draft 0.2（工作树与 d5dc67ce 无差异）
- 修订版本: Draft 0.2
- 评审者: 独立 reviewer（Spec/Executability 轴），未参与该设计的编写与修订
- 日期: 2026-11-26
- 排除项: 用户已选定的方案 A 本身（每作用域一个 bool）不评；Standards/Governance 轴（代码组织、GR107–GR110 结构条目合规）由另一轴负责；REPORT_ONLY，不改仓库、不提交

## 输入与方法

- 规范：`tmp/idev-std/d/C0TJVugR8Kpr8ZAERo543A/payload.md` 全文（含 review-contract、review-candidate-loop、planning-core-rigorous、SR1301）。
- 项目：`AGENTS.md`；设计全文（102 行）。
- 代码（实际读取）：
  - `pkg/agentcore/tools/native/memory.go`（全文至 360 行）、`pkg/agentcore/tools/native/agent.go:150-160`
  - `internal/wiring/memory_runtime.go` 全文、`internal/wiring/memory.go:1-80,180-360`
  - `internal/wiring/origin_guard.go:1-120`、`internal/personal/taint.go` 全文、`internal/personal/observe.go:351-358`
  - `internal/wiring/subagent.go:25-125`、`internal/wiring/manifest.go:613,695-720`、`internal/wiring/runtime.go:570-571`
  - `internal/config/settings_registry.go:1-80,255-300`、`internal/config/settings_protected_test.go:118-360`、`internal/config/load.go:79-90`、`internal/config/approval_rules.go:55-128`、`internal/cli/config.go:184-197`
  - `internal/adapters/gofermem/gofermem.go` 包注释、`pkg/agentcore/memory/store.go:106-113`、`pkg/agentcore/tools/origin.go:63-90`
  - `pkg/sugasdk/memory.go`、`pkg/sugasdk/*.go` 中 external/taint 引用
  - `test/ptye2e/` 目录、`chat_origin_judge_test.go:1-60`、`chat_protected_config_test.go:1-30`、`chat_memory_test.go`（grep）、`pkg/agentcore/memory/execstore/execstore_test.go`（helper 结构）
- 方法：逐条对照设计“已确认事实”“总体方案”“关键流程”“验证要求”与代码路径；枚举所有设置 `Metadata["external"]`、`tools.Meta.External`、taint、ExternalOrigin.Record 的位置，检查去掉记忆结果标记后是否还有路径触发。

## 验收标准

- 设计的核心验收：trusted 作用域的 get/search 结果不进 ExternalOrigin、不污染会话；默认 false 行为不变；会话内 `config set` 拒绝；文件改动启动提示。
- 合同：review-contract 五类 CORE_BLOCKING + necessity proof；同类穷尽；SR1301 安全核对清单。
- 已核对的问题面：
  1. 去掉 external 后的残留路径 —— search 结果：与 get 同一 `result(..., t.external(scope))`（memory.go:164,170），去掉即两者都不记；spill：`taint_on_spill` 默认仅 `shell_exec`（manifest.go:709），memory 不在其中；`memory.taint_tools` 默认空（manifest.go:706）；子 agent：不注册 memory 工具（subagent.go:30-34，runtime.go:571 只在主 runtime 注册），`agent` 报告的 external 只随子会话自身 taint（agent.go:156-158）；xhen SDK：`pkg/sugasdk/memory.go` 是宿主 API，结果不进会话上下文、不带 external，SDK 会话走同一 runtime.go:570 路径，规则自然相同。结论：默认配置下无残留路径；显式配置见 TF3。
  2. `Trusted func(memory.Scope) bool` 签名与注入点：可实现；`memory.Scope.Kind` 可区分 user/project（memory.go:339 已在用）；注入点 `newMemoryRuntime`（memory_runtime.go:158）有两处调用方（runtime.go:570、manifest.go:613），后者不执行工具，无影响。见 TF5。
  3. user 作用域只能 files/exec：files 本来不标 external，`memory.user.trusted` 实际只对 exec 生效，含义为“信任 `memory.user.command` 的输出”；命令来自受保护键，语义清楚，无发现。
  4. 注册与分类表：`relax` 组存在且已含 `tools.ask_on_external_origin`、`memory.taint_tools`（settings_protected_test.go:210,219）；`TestProtectedClassification` 四个子测试会分别捕获“未分类”“Protected 与组不符”，反向验证可行。
  5. 启动比较：受保护键 digest 只收配置里出现的键（protected_snapshot.go:142-150），新增键不会在升级时误报；配置只从 `SUGA_HOME/config.yaml` 读（load.go:79-90），仓库内无法自设该键。
  6. 验证要求：见 TF4。
- SR1301 安全核对：
  - 鉴权（认证与授权）：已核对 —— 受保护键阻止会话内开启；但信任授予的作用对象不清，见 TF1。
  - 注入防护：已核对 —— 本设计就是有意放宽外部来源防护；放宽范围超出设计所述，见 TF1。
  - 秘密不进日志/输出/仓库：不适用（不新增秘密处理）。
  - 入参校验：已核对 —— `ConfigTypeBool` 注册；`gkconfig.Bool` 非法值读作 false，失败关闭。
  - 资源归属校验：已核对 —— project 作用域的内容随仓库变化，见 TF1。

## 发现

### TF1 [HIGH] `memory.project.trusted` 是全局开关，却作用于每个仓库自动选中的 gofer 后端，含第三方仓库自带的 `.gofer/tracker`

- disposition: CORE_BLOCKING
- 类别: [SECURITY]
- 证据:
  - `internal/wiring/memory.go`（selectProjectBackend，约 240-300 行）：未配置 `memory.project.backend` 时，仓库根有 `.gofer/tracker` 即自动选 gofer（reason `gofer_tracker`）；显式 `backend: gofer` 时同样用当前仓库根 `gofermem.Config{Dir: root}`。
  - `internal/adapters/gofermem/gofermem.go` 包注释：“gofer finds the tracker from its working directory … the tracker is the project”；tracker jsonl 随仓库提交（本工作区即如此），clone 下来的仓库可自带带 `suga` 标签、键为 `suga-<kind>-<id>` 的条目。
  - `internal/config/load.go:79-90`：配置只来自 `SUGA_HOME`，所以该键对本机所有仓库同时生效。
  - 设计“取舍”只写“被别的 agent（或经 gofer 的任何写入方）写入”，“非目标”写“不按条目或按写入者区分”；均未说明 project 作用域对应的是“当前所在仓库的 tracker”，信任会随 cwd 落到任何仓库。对照现有先例：项目级 `allow` 审批规则要按工作区 `project_trust`（路径→hash）单独信任（`internal/config/approval_rules.go:81-128`，README.md:134），即代码库已把“仓库自带内容”视为默认不可信。
- 影响 / necessity proof:
  - core_acceptance：设计“用户可见结果”与“安全”一节承诺的风险边界——只有用户自己写入的后端才被信任（“只有自己写入的后端才设 true”）。
  - necessary_causal_chain：用户为本工作区设 `memory.project.trusted: true` → 之后在 clone 的第三方仓库（自带 `.gofer/tracker`，含作者写入的 suga 记忆条目）里跑 chat 或 `gofer job -a suga` → `memory get` 返回仓库作者写的“运维步骤” → 结果不带 external，不进 ExternalOrigin、不污染 → 模型照抄到 `shell_exec`，originGuard 无命中，`approval: auto` 下直接执行。照文档实施后，用户以为只信任了自己的记忆，实际信任了任意仓库作者的内容；核心安全验收失败。
  - minimal_owner：设计“安全、数据、运维与回滚”与“决策”两节（不需要新模块）。
  - deletion_alternative：FAIL（开关本身就是功能，删不掉；问题在作用对象的定义）。
  - scope_effect：方案(a) 无新增；方案(b) 会给 Trusted 的判定加一个条件，不新增 Schema/接口，但改变用户结果，按 SR1401 需用户确认。
- 建议（二选一，交用户定）：
  - (a) 维持全局语义：在“取舍”里明确写出“project 作用域的可信对所有仓库生效，包括 clone 下来、自带 `.gofer/tracker` 的仓库”，README/usage 同步警示，并由用户在修订记录里点名接受。
  - (b) 收窄：project 的 trusted 只在可核实属于用户的仓库生效，例如复用已有的 `tools.approval_rules.project_trust` 工作区登记，或仅当后端为 exec（命令由用户配置）时生效；gofer 自动选择（reason `gofer_tracker`）不适用。
- 同类穷尽：user 作用域（files/exec，命令与位置都来自受保护配置）与 project exec（命令来自受保护配置）不受此问题影响；host-key 路径固定为 files（memory.go:327-337），不受影响。本类只此一处。

### TF2 [MEDIUM] 设计对子 agent 的描述与代码不符

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: 设计“总体方案”第 4 条“子 agent 同路径构建，规则相同”，“安全”第 2 条“子 agent 的 `Tainted` 也不会因此置位，父会话收不到污染信号”。代码：`internal/wiring/subagent.go:30-34` 注释“Sub-agents get no memory tools”；memory 工具只在 `internal/wiring/runtime.go:571` 注册到主 runtime。
- 影响: 子 agent 根本读不到记忆工具结果，两句话描述了不存在的路径；实施者可能为“规则相同”去给子 agent 接 Trusted，或为它补测试。
- 建议: 改为“子 agent 不注册 memory 工具，不受影响”；删去“安全”第 2 条里关于子 agent 的半句。同类穷尽：设计中仅这两处。

### TF3 [MEDIUM] D5“不污染”未写明显式 `memory.taint_tools` / `memory.taint_on_spill` 的优先级

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: `internal/personal/taint.go:58-70`：`taints()` 在检查 metadata 前后分别检查 `m.always[name]`（taint_tools）与 `m.onSpill[name]`（taint_on_spill + 输出已外置）；设计第 5 条“`taint.go` 不改”。所以用户若在这两个键里列了 `memory`，可信作用域的读取仍会污染会话（但 `origin_guard.go:47-48` 不看这两个键，ExternalOrigin 仍不记）。设计 D5 写成“既不记入外部来源，也不把会话标为已污染”，没有这个例外。
- 影响: 行为由代码唯一决定，但文档承诺与实际不一致；用户读 README 会以为可信时写记忆一定不再确认。
- 建议: D5 后补一句“显式把 `memory` 列入 `memory.taint_tools` 或 `memory.taint_on_spill` 时仍按该键污染（不改 taint.go）”，README/usage 同步。同类穷尽：其余污染来源（工具 `External` 元信息、`agent` 报告）不涉及 memory 工具。

### TF4 [MEDIUM] 验证要求有三处观测点写得不具体，照写无法唯一落地

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据与建议（同类一次列全）:
  1. “trace 无外部来源记录 / `suga inspect trace --json` 无外部来源事件”：`ExternalOrigin.Record` 不写 trace（`pkg/agentcore/tools/origin.go:63-90`）；trace 里能观测的是 originGuard 命中后审批事件上的 `origin_judge`/`origin_judge_reason` 等属性（`internal/personal/observe.go:351-358`，`test/ptye2e/chat_origin_judge_test.go` 即按此断言）。不指明字段，“无事件”断言恒真，trusted=true 一侧做不了反向验证。建议写成：trusted=true 时该 `shell_exec` 的审批事件无 `origin_judge` 属性且未出现“逐字复制自 memory”询问；trusted=false 时有。
  2. “假 exec 后端”：`test/ptye2e` 现有夹具里没有 exec 记忆后端；唯一的假 exec 后端是 `pkg/agentcore/memory/execstore/execstore_test.go` 的 TestMain helper（`os.Args[0]` + 环境变量），且 ptye2e 要在 Linux 与 Windows ConPTY 同跑（AGENTS.md 验证节）。建议写明夹具做法（例如 ptye2e 测试二进制自身按环境变量充当 exec 后端，或预编译一个 Go helper），不要依赖 shell 脚本。
  3. GR110 的“终端无会话时成功”：`internal/cli/config.go:184-197` 除 `SUGA_SESSION_ID` 外还要求 stdin 是终端，`app.Run` 测试的 stdin 不是终端，需替换 `configStdinIsTerminal`。建议写明“替换 `configStdinIsTerminal` 为 true 时成功；默认（管道 stdin）与带 `SUGA_SESSION_ID` 时退出码 2 且文件不变”。
- 影响: 不补齐时实施者可能写出恒真断言，反向验证会“通过”，但证明不了核心验收。各项均有其他测试覆盖（单元、链路测试可直接查 ExternalOrigin 与 `tainted_at`），所以不算核心验收完全无验证手段。

### TF5 [LOW] 配置读取位置与旧注释

- disposition: CORE_CORRECTIVE；user_visible_effect: false
- 证据: 设计第 4 条“wiring 在 `newMemoryRuntime` 读 `gkconfig.Bool(...)`”。`newMemoryRuntime` 的参数里没有配置，现有 `memory.<scope>.*` 都在 `loadMemoryConfig`（`internal/wiring/memory.go:56-77`）里读成 `MemoryConfig`；`MemoryToolOptions.Backend` 注释（`pkg/agentcore/tools/native/memory.go:40-43`）写的是“非 files 一律外部”。
- 影响: 在 `newMemoryRuntime` 里读全局 gkconfig 也能跑，但链路测试得改全局配置状态；旧注释会和新判定矛盾。
- 建议: 把两个 bool 放进 `MemoryConfig`（或 `MemoryBackendChoice`），经 `MemorySetup` 传给 `newMemoryRuntime`；同时更新 `Backend` 字段注释与 `external()` 注释。

## 覆盖缺口与限制

- 未运行任何测试或命令验证行为，结论来自静态阅读。
- `protected_snapshot.go` 只读了键收集部分；启动提示的文本与 file 模式（配置用了特殊写法时只报“文件变化”，不报键名）没有细看。设计验收“启动提示报出键名”需要测试用普通 YAML 写法，否则可能只报文件级变化。
- 未读 gofer 侧 tracker 的实现，TF1 中“clone 仓库自带的 tracker 条目会被 gofermem 读出”依据 gofermem 包注释（按 cwd 找 tracker、按标签和键过滤）推断，未实跑。
- 本报告只覆盖 Spec/Executability 轴。

## 结论

BLOCKED —— TF1（CORE_BLOCKING / SECURITY）未关闭：project 作用域的可信会落到任意仓库自带的 gofer tracker，设计没有写明也没有限制。按 TF1 的 (a) 或 (b) 修订并由用户确认后，可做闭合确认。TF2–TF5 为 CORE_CORRECTIVE，均不改变用户可见结果，在计划前随修订一起改即可。

## 剩余风险与后续

- 方案 A 本身的取舍（可信后端被写入恶意指令后无外部来源防护、可经记忆自我扩散）设计已写明，属于用户接受的风险，本轮不再列。
- TF1 选 (b) 时会改变“写一个键即生效”的用户步骤，按 SR1401 需用户确认后再改设计。
- 实施时注意 `memory.go` 已有 438 行，改动很小，不涉及 GR104。
