<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护 实施计划 评审（Standards/Governance 轴，第 3 轮 纠正确认轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `9efb9d536322116baa5dae50d648c455ecb13e5b`；subject `docs/plans/2026-11-12-protected-config-keys-plan.md`（上轮 candidate `f8977380` / Draft 0.2，两轴 PASS）
- 修订版本: Draft 0.3
- 评审者: 独立评审子 agent（Standards/Governance 轴；REPORT_ONLY，不改仓库、不提交）
- 日期: 2026-11-12
- 排除项: 全量重评；设计 0.8 的决策本身；未变章节沿用第 1、2 轮结论。只核对 PS17–PS23、PF13–PF20 的修订，以及 0.3 修订是否引入新的 `CORE_BLOCKING` 或与计划/设计其他部分矛盾。

## 输入与方法

- 规范：`tmp/idev-std/d/SD95t2uhRN3Z9UqnPguVJg/payload.md`（GR108、GR109、plan-contract、planning-core-rigorous 轮次规则）；review-contract 与 review 模板 1.2.0（`tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`，`:497-502`、`:639-654`）。
- 上轮报告：`git show 9efb9d53:docs/review/2026-11-12-protected-config-keys-plan/{standards-review.md,spec-review.md,review-ledger.json}`（PS21/22/23 = PS5/PS7/PS15 续，按 ledger 编号）。
- `git diff f8977380..9efb9d53 -- <plan>`；0.3 全文中 T3、T4、T5、T6、T7、范围与授权、可追溯表。
- 设计 `docs/design/2026-11-11-protected-config-keys-design.md` §3（:151-176）与“架构”（:178-186）。
- 只读核实：`internal/wiring/runtime.go:722`、`internal/wiring/subagent.go:189`（均为 `personal.New(personal.Config{…})`）、`internal/chat/render.go:43`（`rendererOptions`）、`internal/config/load.go`（已用 `log/slog`）、`scripts/acpprobe/acp_probe.py:238-272`；`idevstd script validate_document --kind plan` 对 0.3 输出 `ok: true`。

## 验收标准

- 上轮 `CORE_CORRECTIVE` 逐项关闭；修订仅限 changed/dependent scope，不扩主旨。
- 设计“架构/代码组织”与 GR108：`internal/wiring` 只装配，判断逻辑归 owner 包。
- 唯一 `host_or_non_offline_action` 覆盖全部联网/凭据/主机动作。
- 新发现按五类判断是否 `CORE_BLOCKING`，否则为 `CORE_CORRECTIVE` / `DEFERRED_ENHANCEMENT`。

### 上轮发现处理核对

| 上轮 | 0.3 处理（计划位置） | 结论 |
|---|---|---|
| PS17 / PF15 acpprobe 恒空过 | T7（:216）期望改为“恰为 0 条”+两个 prompt 正常完成；去重标 `UNVERIFIED_REAL_ENV`，替代证据 T4 两次 `Open`；补 `--bin/--cwd/--home/--config-from/--prompt×2` 与“容器内、worktree 根” | 关闭（构建产物与 trace 查询环境见 PS25） |
| PS18 host 声明漏 acpprobe | :68 补“T7 的 acpprobe 用真实凭据调用模型 provider（联网、少量费用）”；仍只一处声明 | 关闭 |
| PS19 判断逻辑在 wiring | 缓存、去重、可否推进移到 `internal/config/protected_state.go`（:180）；wiring 只调 `CheckProtectedOnce` 并接回调、挂 Runtime（:181） | 关闭；与设计“架构”、GR108 一致（见下）。T4 文件清单未同步见 PS24 |
| PS20 范围漏 `pkg/sugasdk` | :65 补“（只加测试）” | 关闭 |
| PS21（PS5 续）可追溯 | :239-247 验证列写测试名；补“文档与使用方脚本”行；§3 行加 T7 | 关闭 |
| PS22（PS7 续）/ PF13 文案 | T2 `cliText(tr, "cli.config.protected_refused", …)`、`[cli]` 节；T4 `[status]` 节三键 zh/en 全文、`{keys}` 连接符、`{path}`=`state.Path`；T2 断言 `err.Error()` | 关闭 |
| PS23（PS15 续）/ PF18 | 删 `TestConfigLockPath`，新增 `TestProtectedPathNormalize`（含“锁名与快照名同源”）；T3 追加 `./internal/cli -run 'TestConfigCommands\|TestConfigPath'` | 关闭（PF18 建议的相对 `SUGA_CONFIG` 下 source 断言未显式写，属 Spec 轴，已由“`config get` 来源不变”覆盖默认场景） |
| PF14 无变化回调 | `NoticeFor` 无变化/不可用/已返回 → `ok=false`；失败按“无变化、不可推进”缓存；T4 补“无变化零条”用例 | 关闭 |
| PF16 `{path}` 与 rendererOptions | `state.Path`；chat 经 `rendererOptions`（约 `:412`）传递 | 关闭（文件清单见 PS24） |
| PF17 `personal.Config` 与子 runner | `personal.Config.ProtectedNotice`；`subagent.go:189` 单独构造不设回调（已核实），删假设分支 | 关闭 |
| PF19 Reload / 回退分支 | “`Reload`（= Reset + Load）会重新读取并刷新”；补 `ResolveSugaHome` 出错分支（三个回退） | 关闭 |
| PF20 ptye2e / grep | `ToolsExtra` 制造改动、第二次 chat 跑一回合、第三次同 `ToolsExtra`；终端用例自写 `config.yaml` 与空 `.env`；grep 补 unset、去掉 `grep -v _test.go` | 关闭 |

### 0.3 修订一致性核对

| 项 | 结论 |
|---|---|
| `protected_state.go` 归属 vs 设计“架构” | 设计 :182 让 `internal/config` 承担“比较、整体推进”，:185 要求 wiring“自身不含判断逻辑（GR108）”，§3 :175 要求“进程内按配置路径缓存（测试提供重置函数）”。0.3 把缓存、`Advance` 的前置条件、会话去重放在 config，wiring 只调用与连线，符合 GR108“只构造依赖与连线”。设计未规定缓存所在包，放 config 不构成偏离。通过 |
| 日志 | config 包已用 `log/slog`（`load.go:5`），`CheckProtectedOnce` 记 Warn 无新依赖；GR109：快照写入/比较失败只 Warn 的例外理由沿用设计 §3。通过 |
| Capability Discovery | 说明段补 `protected_state.go`，仍属既有包 owner 扩展，NONE sentinel 不变（SR1403）。通过 |
| host 声明 | 唯一一处 `host_or_non_offline_action=REQUIRED`，覆盖主机 worktree/合并/Windows、staticcheck 下载、acpprobe 真实 provider。acpprobe 的 `SUGA_CONFIG` 指向真实文件但 `SUGA_HOME` 隔离，快照与锁落在隔离 home（非默认路径走 `<sha256>.json`），不写真实 `~/.suga`。通过 |
| 修订记录与版本 | 0.3 为语义变化，递增正确；按类自查范围写明；validator `ok: true`。通过 |
| 范围扩散 | 未新增主旨、验收或任务；新文件在已列范围 `internal/config` 内。通过 |

## 发现

### PS24 [LOW] T4“文件”清单未随 0.3 修订同步：漏 `internal/config/protected_state.go`，`runtime.go`/`chat.go`/`render.go` 的改动描述与动作矛盾

- disposition: CORE_CORRECTIVE
- 证据: T4 文件行（:178）仍是 0.2 原文：不含 `internal/config/protected_state.go`（及其测试）；`runtime.go` 写“只加 `Options` 字段与一处调用”。而 T4 动作（:180-183）把判断逻辑放进 `internal/config/protected_state.go`，并写“`runtime.go` 的改动：`Options` 字段、`Open` 中一处调用、`Runtime` 一个字段”；另外 `ProtectedNotice` 须在 `runtime.go:722` 的 `personal.Config{…}` 字面量中设置（`personal.New` 在此构造，wiring 的 `protected.go` 无法事后注入），即 `CheckProtectedOnce` 必须在 :722 之前调用。chat 动作（:185）要求 `chat.go` 把数据映射进 `rendererOptions`、`render.go:43` 的 `rendererOptions` 加字段，文件行只写“`Options` 字段与横幅后回调一处”“横幅组装处”。同类穷尽：仅 T4 文件行；T1–T3b、T5–T7 文件行与动作一致；Capability 说明段（:99）已列 `protected_state.go`。
- 影响: 由子 agent 按任务实施时，文件清单是改动边界与审查依据（`diff --stat` 对照）；清单与动作不一致会让实施者误判为扩围或漏改 config 文件。不判 `CORE_BLOCKING`：动作文本已唯一确定位置与签名，不影响可执行唯一性。
- 建议: T4 文件行补 `internal/config/protected_state.go`（与测试）；`runtime.go` 改为“`Options` 字段、`Open` 中一处调用（在 `:722` 构造 Runner 之前）、`Runtime` 一个字段、`personal.Config` 一行”；`chat.go` 补“`rendererOptions` 映射（约 `:412`）”，`render.go` 补“`rendererOptions` 字段”。

### PS25 [LOW] T7 acpprobe 命令缺构建 `--bin` 产物的步骤，trace 查询未写环境

- disposition: CORE_CORRECTIVE
- 证据: T7（:216）`--bin tmp/suga-hyj7/suga`，全计划无产出该二进制的 `go build` 步骤（`grep "go build"` 无结果）；:213 又写“主机脚本放 `tmp/suga-hyj7/`”，worktree 根的 `tmp/` 与 WP 根 `tmp/` 未区分。结束后“在该 home 上 `suga inspect trace --json`”未写需 `SUGA_HOME=tmp/suga-hyj7/acp-home`（`acp_probe.py:271-272` 只对子进程设置）。同类穷尽：T7 acpprobe 一处；ptye2e 由测试自建二进制，不受影响。
- 影响: 执行者需自行补构建与环境，若用旧二进制或默认 `SUGA_HOME` 查询，“恰为 0 条”会在错误对象上成立。不判 `CORE_BLOCKING`：T7 由计划作者本人执行、修正局部。
- 建议: acpprobe 前加 `go build -o tmp/suga-hyj7/suga ./cmd/suga`（worktree 根），查询写成 `SUGA_HOME=tmp/suga-hyj7/acp-home SUGA_CONFIG=~/.suga/config.yaml tmp/suga-hyj7/suga inspect trace --json`；或以 `acp_probe.py` 自带的 trace 输出为准并写明。

### PS26 [LOW] `ProtectedState.recorded` 的并发保护未写定

- disposition: CORE_CORRECTIVE
- 证据: 0.2 的去重集合在 wiring 进程级缓存的 `sync.Mutex` 之内；0.3（:180）把 `recorded map[string]bool` 放进导出的 `*ProtectedState`，`sync.Mutex` 只写在“进程级缓存 map”上；`NoticeFor` 由各会话的 Runner 在回合内调用（ACP 同进程多会话可并发 prompt）。计划未写 `NoticeFor` 读写 `recorded` 时持哪把锁。
- 影响: 按字面实现可能出现 map 并发读写；T7 `go test -race` 只有在 T4 用例覆盖并发会话时才会发现。不判 `CORE_BLOCKING`：属实现细节，修正为一句合同。
- 建议: 写明 `ProtectedState` 内含 `mu sync.Mutex`（或复用缓存锁），`NoticeFor` 持锁检查并记录；T4 加一个多 goroutine 不同 session 并发 `NoticeFor` 的 `-race` 用例（T4 验证命令可加 `-race`）。

### PS27 [INFO] trace attrs 组装放在 `internal/config`

- disposition: DEFERRED_ENHANCEMENT
- 证据: `NoticeFor` 返回 `map[string]any`（`keys`/`whole_file`/`snapshot_unreadable`），即 trace 事件 schema 由 config 包生成；设计“架构”把 trace 输出归 `internal/personal/observe.go`。
- 影响: 不违反 GR108 与设计（设计未规定 attrs 组装归属），仅内聚性：config 包知道了 trace 字段名。
- 建议: 可选：`NoticeFor` 返回 `(ProtectedChange, bool)`，attrs 由 `internal/personal/protected_notice.go` 组装。实施时可顺手处理，不要求本轮修订。

## 覆盖缺口与限制

- 未运行任何测试；结论基于计划文本、设计文本与只读源码核对。
- 未重评未变章节（T1、T2 其余部分、T3b、波次、回滚、Gate）。
- 报告写在 `tmp/review/suga-config-keys-plan/`，归档到 `docs/review/2026-11-12-protected-config-keys-plan/` 由调用方处理。

## 结论

PASS（本轴）。PS17–PS23 与 PF13–PF20 均已正确修订并关闭；`protected_state.go` 的归属与设计“架构”、GR108 一致；host 声明完整且唯一。本轮无 `CORE_BLOCKING`（PS24–PS26 均不属五类：动作文本已唯一确定实现，不阻断核心验收、安全/授权或可执行唯一性）。新开 `CORE_CORRECTIVE`：PS24、PS25、PS26（均 LOW、局部文字修正）；`DEFERRED_ENHANCEMENT`：PS27。

## 剩余风险与后续

- 按 planning-core-rigorous，本轮为唯一一轮纠正确认轮，之后不再进入评审轮。PS24–PS26 的修订若在批准前做，属“未复核修订”，需在批准 Gate 中列出其摘要供用户点名接受；或按 Draft 0.3（最后已评审 revision）批准，并把 PS24–PS26 作为实施任务书中的明确要求带入。
- 实施审查时对照 T4 实际 `diff --stat`：`runtime.go`/`runner.go`/`chat.go` 净增 ≤20 行，`internal/wiring/protected.go` 不含条件判断之外的业务逻辑。
