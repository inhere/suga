<!-- template_id: plan; template_version: 1.2.0 -->
# 仓库探索效率优化实施计划

> 状态：Draft 0.1 / 待用户审批

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-18 | suga（gofer job） | 依据 `docs/design/2026-10-18-explore-efficiency-design.md` Draft 0.1 与实测报告，拆分 `file_read` 大纲/多段范围、`file_symbols` 新工具、接线与验证四段任务；辅助小模型按 NOT_NEEDED 记录。 |

## 目标与完成定义

目标：把实测确认的两类探索浪费（大文件重复读、缺少符号级检索）用两个内置只读工具改动消掉，不新增依赖、不改稳定系统提示词与审批语义。

完成定义：

1. `file_read` 新增可选 `outline`；为 true 时对已声明语言的源码文件返回声明清单（行号 + 声明行）+ 省略标记，并在首行声明“这是大纲”；小文件与未声明语言回退窗口并说明原因。
2. `file_read` 的 `paths` 项支持 `path:start-end[,start-end]` 行段后缀；一次调用可混用带/不带后缀的项；**不带新参数时输出与当前完全一致**（`file_window_test.go` 契约不破）。
3. 新增只读工具 `file_symbols`：`go/parser` 列出 `file:line kind name signature`，支持目录递归、`name` 子串过滤、`kind` 过滤、`include_tests`（默认 false）、`limit`；非 Go 明确报错，不静默空结果；解析失败的文件只记一行错误。
4. `file_symbols` 在 run/chat/ACP 各模式可调用（只读分类、`properties` 非空、审批表与 ACP 策略表测试更新），`go test -count=1 ./internal/... ./pkg/...`、`go vet ./...`、`gofmt -l` 全绿，`staticcheck ./...` 无新告警。
5. Linux 伪终端冒烟新增一个场景：真实二进制 + `fakellm.py` 驱动模型发起 `file_read outline`、多段范围读与 `file_symbols` 三种调用，并用 `suga trace <sid> --json` 断言工具事件；做完反向验证（临时去掉被测逻辑，确认检查失败）。
6. 用 `docs/research/2026-10-18-explore-efficiency.md` 里的 4 个任务、同一模型做一次真实复测，报告 T1/T4 类任务的步数与重复读变化，并同时给出方差说明（单次运行噪声 ±3–5 步）。
7. 每个功能点原子本地提交（`feat:`/`test:`/`docs:`），提交前 `git status --short` 与 `git diff --cached --name-only` 核对；不 push、不打 tag、不改 `.gofer/tracker`。

## 范围、排除项与授权

- 范围：`pkg/agentcore/tools/native/file_read.go`、新增 `pkg/agentcore/tools/native/file_symbols.go`、`pkg/agentcore/tools/native/lang.go`/`lang_go.go` 的声明入口、`internal/wiring` 只读工具注册与分类测试、`internal/acpserver` 策略表、native 单元测试、Linux `scripts/ptysmoke` 场景、CHANGELOG 与文档。
- 不做：gopls/LSP、代码索引、语义检索、仓库概览缓存、grep 上下文行/相关度排序、多 pattern、外部命令（rg/xerj）适配、辅助小模型的输出整理与命令检查、稳定系统提示词与审批语义改动。
- 兼容边界：`file_read` 的窗口/截断/字节上限语义、错误消息风格、只读 roots 限制均不变；新工具不新增配置项、不新增持久化；若实现需要改变这些，停止并回到设计 Gate。
- `host_or_non_offline_action=REQUIRED`：Windows 主机的 `go build`/`go vet`/`gofmt`/`go test -count=1 ./internal/... ./pkg/...`/`staticcheck`、Linux 容器的伪终端冒烟与反向验证、以及真实模型复测，都是实施完成 Gate 的外部环境动作，本计划阶段不执行。
- 授权：用户批准设计与本计划后才实施；批准只授权本 worktree 的本地修改与提交，不授权 push/tag/release/tracker 修改。

## 输入与批准证据

- 设计：`docs/design/2026-10-18-explore-efficiency-design.md`，Draft 0.1 / 待用户审批。
- 调研证据：`docs/research/2026-10-18-explore-efficiency.md`（10 次真实运行表格、浪费模式、候选手段逐条评估、5qb4 判断）。
- issue：`suag-7ih4`（候选手段与 E5 观察）、`suag-5qb4`（辅助小模型两个方向）。
- 代码事实：`pkg/agentcore/tools/native/file_read.go:19-20,43-83,137-158,206-268`；`pkg/agentcore/tools/native/lang.go:11-26`、`lang_go.go:11-13`；`internal/wiring/runtime.go:456,503-529`；`internal/wiring/native_tools_test.go:35-51`；`internal/wiring/approval_table_test.go`；`internal/acpserver/policy_table_test.go`；`internal/personal/analyze.go`。
- 实施前必须重新确认 HEAD/branch/dirty 状态；若 `file_read` 的参数或窗口语义与设计记录不一致，停止进入 T1。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | `file_read` 一次给出“文件有哪些声明、在第几行”，并支持一次取多个行段 | `pkg/agentcore/tools/native/file_read.go:19-20,43-83,137-158,206-268`；`file_window_test.go`；`lang.go:11-26` | 现有 `readOne`/`readLineWindow`、`paths` 多路径、`pageBytes` 与截断提示 | 在 `FileReadTool` 内增加大纲渲染与 `paths` 行段后缀解析两个薄函数 | OWNER_EXTENSION | 当前只有整窗读取，没有声明级视图与多段一次取；实测 T4 档 A 12/24 次 `file_read` 是重复读 | owner 是 `FileReadTool`；不得另建平行读工具，不得改窗口/截断契约 |
| CAP-02 | 一次列出文件/目录的 Go 声明（func/type/const/var + 行号），默认排除测试文件 | `lang_go.go:11-13`；`grep.go`/`find.go` 的遍历与 ignore；标准库 `go/parser`、`go/token` | 现有语言声明风格、native 只读工具 `ToolMeta`、目录遍历与 ignore 逻辑 | 新增 `file_symbols.go`，并在 `lang_go.go` 增加 Go 声明清单入口 | MINIMAL_NEW_MODULE | 当前无任何符号级检索：T2 只能用 `file_grep` 猜正则（2–5 次），T4 追调用链同样反复 grep | owner 是 native 只读工具集；不引入 gopls/索引/缓存；非 Go 明确拒绝 |
| CAP-03 | 新只读工具在 run/chat/ACP 各模式注册、只读分类、schema `properties` 非空 | `internal/wiring/runtime.go:456,503-529`；`internal/wiring/native_tools_test.go:35-51`；`internal/wiring/approval_table_test.go`；`internal/acpserver/policy_table_test.go` | 现有 native 工具注册表、只读 `ToolMeta`、审批与 ACP 分类表测试 | 在既有注册处增加一行，并在三个分类表测试中增加一行 | DIRECT_REUSE | 现有注册与分类表没有 `file_symbols` 条目 | 不得为它另建注册路径或放宽审批 |
| CAP-04 | 用真实模型与 trace 指标回归（steps/re-reads/tokens） | `internal/personal/analyze.go` 的 `--analyze` 输出；本批实测脚本思路；`scripts/ptysmoke` + `fakellm.py` | 现有 `suga trace --analyze`、伪终端冒烟框架 | 不新增指标，只把 4 个任务与配置档固化成验收清单 | DIRECT_REUSE | 目前没有“探索类任务”的固定回归用例 | 不新建评测框架；报告必须同时给方差，避免把噪声当收益 |
| CAP-05 | 辅助小模型：工具调用后整理输出、执行前检查命令 | `suag-5qb4` 里的 omp/pi-go/goose 参考；现有 spill/`out:` ref 与输出折叠；现有 approval 与危险删除规则 | 已有输出折叠与 spill、approval 规则 | 无（本版不实现） | NOT_NEEDED | 实测 11 次运行 0 条 shell 命令、0 次噪声型大输出，没有触发样本；确定性规则已覆盖可确定场景 | 强做会引入额外模型调用延迟与不稳定判断，且与现有输出治理重复 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| MOD-EE-01 | CAP-02 | `pkg/agentcore/tools/native/file_symbols.go` | `lang_go.go`；`grep.go`/`find.go`/`walk.go` 遍历与 ignore；`catalog.go` 工具清单 | 现有工具都不产出声明级视图：`file_read` 是窗口、`file_grep` 是文本命中、`file_list`/`file_find` 只给路径 | 需要在 `lang_go.go` 增加 Go 声明清单入口并复用遍历/ignore，但工具职责无法塞进任一现有 owner | 缺少符号检索是实测三类浪费之一（T2/T4） | owner 是 native 只读工具集；只读、无缓存、无持久化状态 | 删除后回到“用 `file_grep` 猜正则”（实测 2–5 次调用），不留配置/schema 残留 |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 仓库概览缓存/索引工具 | CAP-01 | 删除后 `file_list` + `file_read` 大纲仍能给出目录与声明全貌；缓存要处理失效、段一致性与提示词膨胀，而实测首次定位只花 1–2 步，收益未证。 |
| gopls/LSP adapter | CAP-02 | 删除后 `go/parser` 的声明清单已覆盖“列清单/找定义”；LSP 需要常驻进程、跨语言依赖与索引，超出非目标。 |
| 多 pattern / 上下文行 / 相关度排序版 grep | CAP-01 | 删除后 `file_grep` 语义不变；实测 grep 都在 2–5 次内收敛，真正痛点是“没有声明级过滤”，已由 CAP-02 覆盖。 |
| 语义检索（judge 小模型）工具 | CAP-01 | 删除后本批 4/4 结构性任务不受影响；需要索引加额外模型调用，没有实测收益。 |
| 辅助小模型摘要/命令检查旁路 | CAP-05 | 删除后现有 spill/折叠与 approval 规则仍生效；实测 0 shell 命令、0 噪声型大输出，没有触发样本。 |

## 前置检查

- P0：记录 `git status --short`、HEAD、branch；确认只有本任务文档改动；提交前只 stage 对应文件并核对 `git diff --cached --name-only`。
- P1：重读设计与实测报告，确认 `file_read` 现有参数、窗口与截断语义、`file_window_test.go` 契约与设计记录一致；不一致则停止。
- P2：跑近端基线 `go test -count=1 ./pkg/agentcore/tools/native/... ./internal/wiring/... ./internal/acpserver/...`，记录 exit code 与原始输出；基线失败标 `BASELINE_FAILED`，不进入实现。
- P3：确认 Windows 路径语义：`paths` 项的行段后缀解析必须保留 `D:\...` 盘符与含冒号的既有行为，并准备非 Windows 也能通过的单测（不用系统临时目录，用 `t.TempDir()`）。
- P4：确认 `go/parser` 在只读 roots（含模块缓存）下的可用性与性能预算（单目录 ≤ 200 文件时单次调用 < 1s），并确认 `parser.SkipObjectResolution` 之类的最小解析模式。
- P5：确认 `scripts/ptysmoke` 现有场景结构、`fakellm.py` 模式（tool/text/edit/memory）与 `make smoke-chat` 接入点，设计新场景的断言方式（`suga trace --json` 事件）。

## 波次与依赖

| 波次 | 任务 | 依赖 | 执行方式 |
|---|---|---|---|
| W0 | T0 前置检查与实现 Gate | 无 | 只读；基线失败停止 |
| W1 | T1 `file_read` 大纲与多段范围读 | T0 | native 单测先行，绿后提交 |
| W2 | T2 `file_symbols` 工具与 Go 声明清单 | T0（与 T1 独立，可并行但分开提交） | 单测覆盖文件/目录/过滤/错误 |
| W3 | T3 注册接线、分类表与 schema 断言 | T2 | 只改注册与测试 |
| W4 | T4 伪终端冒烟与反向验证 | T1–T3 | Linux 容器执行 |
| W5 | T5 真实模型复测与报告更新 | T4 | 主机执行，含方差说明 |

## 任务

### T0 前置检查与实现 Gate

- 按 P0–P5 执行并记录原始输出；确认设计与代码事实一致（`file_read` 参数、`lang.go` 声明结构、注册路径）。
- 产出：基线测试输出、HEAD/branch 记录、`file_read` 参数对照结论。
- 完成判定：P0–P5 全绿或明确记录停因；不绿不进入 T1。

### T1 `file_read` 大纲与多段范围读

- 实现 `outline`（bool，默认 false）：已声明语言的源码文件返回声明清单（行号 + 声明行）+ 省略标记（区间 + 区间内声明计数）+ “这是大纲不是全文”首行；未声明语言与小文件回退窗口并说明原因。
- 实现 `paths` 项行段后缀 `path:start-end[,start-end]`：带后缀项用自己的行段；不带后缀项继续用 `offset/limit`；两者可在一次调用混用。
- 单测（`t.Run` 分组）：大纲输出形状、省略标记、小文件回退、未声明语言回退、行段后缀（单段/多段/混合/越界/非法格式）、Windows 盘符路径、**旧调用方式输出逐字节不变**。
- 完成判定：`go test -count=1 ./pkg/agentcore/tools/native/...` 全绿；`file_window_test.go` 未修改且通过。

### T2 `file_symbols` 工具与 Go 声明清单

- 新文件 `pkg/agentcore/tools/native/file_symbols.go`：`path`（文件/目录）、`name`、`kind`、`include_tests`（默认 false）、`limit`；`go/parser` 仅解析声明；输出 `file:line kind name signature` 并按路径+行号排序；目录递归跳过 `_test.go`（默认）、`vendor`、`.git`。
- `lang_go.go` 增加 Go 声明清单入口（谁提供“本语言如何提取声明”），供本工具与未来 `file_read outline` 复用；其他语言返回不支持错误。
- 单测：目录+文件名过滤（对应 T2 实测任务：`name=Read` 命中 18 个函数）、`kind` 过滤、`include_tests` 开关、非 Go 文件/不存在路径/解析失败文件/超 `limit` 截断。
- 完成判定：native 单测全绿；`staticcheck ./pkg/agentcore/...` 无新告警；单文件 < 1000 行。

### T3 注册接线、分类表与 schema 断言

- 在 `internal/wiring` 现有只读 native 工具注册处注册 `file_symbols`；确认只读分类（`ToolMeta{CategoryRead, ReadOnly: true}`）与审批/ACP 策略一致。
- 更新 `internal/wiring/native_tools_test.go`、`internal/wiring/approval_table_test.go`、`internal/acpserver/policy_table_test.go` 的期望清单；新增/沿用 `TestToolSchemasDeclareProperties` 保证 `properties` 非空。
- 完成判定：`go test -count=1 ./internal/... ./pkg/...` 全绿。

### T4 伪终端冒烟与反向验证

- 在 `scripts/ptysmoke/` 新增场景：假模型依次调用 `file_read`（`outline=true`）、`file_read`（多段范围 `paths`）、`file_symbols`，脚本用 `suga trace <sid> --json` 断言三次工具事件与结果关键片段；加入 `make smoke-chat`。
- 反向验证：临时去掉 `outline` 分支，确认该场景断言失败；恢复后重跑通过。
- 完成判定：Linux 容器内 `make smoke-chat` 通过；反向验证有记录（Windows 主机跑不了 PTY，此 Gate 由监督在容器执行）。

### T5 真实模型复测与报告更新

- 用实测报告的 4 个任务与同一模型（`opencode-go/deepseek-v4.1-flash`）复测，至少覆盖 T1 与 T4（重复读最重的两个任务），记录 steps/工具分布/re-reads/tokens/墙钟。
- 更新 `docs/research/2026-10-18-explore-efficiency.md`（新增“优化后复测”小节）或新增一份复测报告，写明方差与结论（含未达预期项）。
- 完成判定：复测数据与原始基线并列呈现，结论与数据一致；若收益未达设计预期，写清原因并回到设计 Gate 讨论是否继续。

## 提交与合并顺序

1. `docs`: 设计与计划（本批两个文档，一个提交）。
2. `feat(native)`: T1 `file_read` 大纲与多段范围读（含单测）。
3. `feat(native)`: T2 `file_symbols` 与 Go 声明入口（含单测）。
4. `feat(wiring)`: T3 注册与分类表（含测试）。
5. `test(ptysmoke)`: T4 场景与反向验证记录。
6. `docs`: T5 复测报告与 CHANGELOG「未发布」条目。

每个提交单独 stage 对应文件，不混入无关改动；不 push、不打 tag。

## 回滚与恢复

- 每个功能点一个提交，可独立 `git revert` 单点：T3 回滚后 `file_symbols` 不再注册（工具消失），T2 回滚后删除 `file_symbols.go`，T1 回滚后 `file_read` 回到窗口语义。
- 无配置项、无数据库 schema、无持久化状态，回滚不需要迁移。
- 若复测显示收益未达预期或引入新问题（例如模型把省略标记当全文），先回滚对应提交，再按设计 Gate 重新评估（设计 D2 允许把大纲降级为实验开关）。

## 人工 Gate

- Gate A（现在）：设计与本计划获批后才实施；未获批不改任何产品代码。
- Gate B（T0 后）：前置检查失败或代码事实与设计不一致时，停下并回到 Gate A 讨论，不自行扩大范围。
- Gate C（T5 后）：复测数据交用户看，决定是否继续做报告中“暂不做”的手段（仓库概览缓存、辅助小模型等）。

## 可追溯性

| 完成定义 | 任务 | 证据 |
|---|---|---|
| 1、2 | T1 | `file_read` 单测输出、`file_window_test.go` 通过 |
| 3 | T2 | `file_symbols` 单测输出、`name=Read` 命中 18 个函数 |
| 4 | T3 | `go test -count=1 ./internal/... ./pkg/...`、`go vet`、`gofmt -l`、`staticcheck` 原始输出 |
| 5 | T4 | Linux `make smoke-chat` 输出 + `suga trace --json` 断言 + 反向验证记录 |
| 6 | T5 | 复测表格（优化前后并列）与方差说明 |
| 7 | 全部 | `git status --short`、`git diff --cached --name-only` 与提交列表 |

## 完成 Gate

- 代码：全部完成定义 1–4 有原始输出证据；`gofmt -l $(git ls-files "*.go")` 无输出、`go vet ./...` 与 `staticcheck ./...` 无新告警、`go test -count=1 ./internal/... ./pkg/...` 全绿。
- 平台：Windows 主机（无 pty）完成编译与单测；Linux 容器的伪终端冒烟与反向验证由监督执行并回报；两项都记录后才能宣布完成。
- 验证：T5 的真实模型复测含方差说明；未达预期时按回滚与 Gate C 处理。
- 提交：按“提交与合并顺序”逐点提交，每个提交附跑过的命令与结果；未完成项如实列出。
