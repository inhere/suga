<!-- template_id: plan; template_version: 1.3.0 -->
# suga 可信模式、外部来源裁决与敏感路径禁读 实施计划

> 状态：Draft 0.1

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-10 | Claude | 初稿：实施 `docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md` 0.5（A 审批档位与可信模式、B 外部来源裁决、C 敏感路径禁读），并先修既有缺陷 `suga-flei` |

## 计划工作声明

- 思考模式：`RIGOROUS`（SR1408）。
- 核心目标：实现设计 0.5 的 A、B、C；设计中“ACP 下保护路径 ask 照常询问”以 `suga-flei` 修复为前提，故一并修复。
- Scope freeze：只改 suga 仓库：
  - 代码：`pkg/agentcore/tools`（`rules/rules.go`、`approval.go`、`origin.go` 不改匹配、新增档位类型）、`pkg/agentcore/api`（回合请求 ctx）、`pkg/agentcore/tools/native`（禁读：`readroots.go`、`file_read*.go`、`file_list.go`、`file_find.go`、`grep.go`、`refs.go`、`file_symbols.go`、`view_image.go`、`edit.go`、`file.go`）、`internal/config`（档位、裁决与禁读配置）、`internal/wiring`（`approval_policy.go`、`origin_guard.go`、`danger_guard.go`、`chat_approver.go`、`memory_runtime.go`、`runtime.go`、`runtime_build.go`、`capabilities.go`、`subagent.go`）、新包 `internal/originjudge`、`internal/personal`（`runner.go` 写回合请求、`observe.go` attrs）、`internal/cli`（`run.go`、`acp.go`）、`internal/acpserver`（`policy.go`）、`internal/output/streamjson.go`、`internal/chat`（状态行）、`pkg/sugasdk`（`types.go`、`session.go`）及各自测试；
  - 文档与记录：`README.md`、`docs/usage/gofer-personal-agent.md`、`CHANGELOG.md`、`.gofer/tracker/`、本计划与 `docs/review/2026-11-10-trust-mode-origin-judge-read-deny-plan/`。
  - 不改 gofer、xhen；不新增表与迁移；不新增第三方依赖。
- 暴露度：高（同设计：已发布、gofer/xhen 使用、改变安全边界）。计划评审两轴独立，配 ledger。
- 停止条件：两轴 PASS 后交你批准；实施中出现 Semantic Amendment 或 Ownership Conflict 即停。

## 目标与完成定义

1. **前提（suga-flei）**：ACP 与 chat 的 always 会话规则不再放行 `ClampAllow` 保护的路径（`.git`、`.suga`、SUGA_HOME、规则文件）。
2. **A**：
   - `chat`、`run`、`acp` 支持 `--approve ask|auto|trust` 与 `--yolo`（同时出现报错）；SDK 支持 `SessionOptions.ApprovalMode`；配置 `tools.approval` 仍只取 `ask|auto`，写 `trust` 报错；
   - `trust` 放开设计 A 表 5 项，硬限制全部保持；chat/run 下隐含 `--allow-write`；
   - 档位可见：chat 状态行、stream-json `session` 行 `approval_mode`、事件 attrs `approval_mode` 与 `approval=trust`。
3. **B**：外部来源初筛命中后经 `models.judge` 裁决；allow/deny/ask 与出错、取消的处理按设计；attrs 记 `origin_judge`、`origin_judge_reason`、`origin_judge_tokens`；`tools.origin_judge.enabled`（缺省 true）、`tools.origin_judge.timeout`（缺省 20s）。
4. **C**：`tools.read_deny` 与内置列表（`tools.read_deny_defaults` 缺省 true）对设计所列工具生效，匹配规则按设计；SDK `ReadDeny` 追加。
5. 合入后 main 上 `go build ./... && go vet ./... && go test -race ./...` 通过；`make smoke-go` 通过；主机 Windows `go test -p 1 ./...` 通过。
6. 真实验证：`acp_probe` 跑 `--approve trust`；裁决真实 provider 评测完成并记录结果。
7. 完成 Gate 前不推送、不发版。

## 范围、排除项与授权

- 范围：设计 0.5 的 A、B、C；`suga-flei`。
- 排除：
  - 沙箱、shell 内容解析；
  - ACP 会话内切换档位；
  - 裁决扩展到全部调用、只检查高风险动作；
  - gofer 的 job 退出码；xhen 侧接入。
- 授权（计划批准加当前执行请求覆盖以下动作）：
  - 在 suga 的 `.worktrees/` 分支上实施，本地合入 suga main；
  - 仓库类主机 job：建 worktree（`tmp/xhen-p1/wt-repo.ps1`）、合入（`tmp/xhen-p1/host-merge.ps1`）、清理（确认 `git status --porcelain` 为空且已合入后 `git worktree remove --force`、`git branch -d`；suga 仓库不做 `git worktree prune`）；
  - 测试类主机 job：Windows `go test -p 1`（走代理下载模块）；`acp_probe`（隔离 SUGA_HOME，读真实 `config.yaml` 与 `.env` 但不复制）；裁决真实 provider 评测（同样隔离）；
  - 网络：GOPROXY；评测与 acp_probe 调用已配置的模型 provider；
  - 实施分工：T0–T6 由 sonnet 子 agent 实施；T7、T8 由我完成。
  - 不覆盖：推送、打 tag、发版、安装（另行授权）。
- `host_or_non_offline_action=REQUIRED`
  - 主机 gofer job：建、合、清 worktree，Windows 测试，acp_probe，裁决评测。
  - 网络：GOPROXY 与模型 provider 调用。

## 输入与批准证据

- 设计：`docs/design/2026-11-10-trust-mode-origin-judge-read-deny-design.md` 0.5，已批准（用户 2026-11-10：“批准 suga 安全设计 0.5（接受未复核修订与上述调整），开始写计划”）。
- 设计评审：`docs/review/2026-11-10-trust-mode-origin-judge-read-deny/`（3 轮，第 2、3 轮两轴 PASS；0.5 为未复核修订，已经你点名接受）。
- Q1–Q4：用户 2026-11-10：“Q1–Q4 按推荐”。
- 既有缺陷：`suga-flei`（设计第 3 轮评审 F22）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 区分保护路径 ask | `rules.ClampAllow`、`Policy.Decide`、`approval_policy.go` | 部分 | 两处返回降级标记 | OWNER_EXTENSION | 降级后的 Ask 与普通 ask 不可区分 | 标记随决策走，不新增状态 |
| CAP-02 | 审批档位 | `tools.approval`（ask/auto）、`newPolicyApprover`、`newChatApprover`、`permissionHook` | 部分 | 档位类型与各入口分支 | OWNER_EXTENSION | 无 trust 档、无统一档位 | 沿用现有 owner（设计偏离说明） |
| CAP-03 | 启动参数 | `internal/cli/run.go` 共用 flag、`acp.go` | 部分 | 增 `--approve`、`--yolo` | OWNER_EXTENSION | 无审批参数 | gcli 写法（SR2003） |
| CAP-04 | 回合请求进入工具 ctx | `api.WithSessionID/WithTurnID`、`Runner.Run` | 部分 | `api.WithTurnPrompt`，Runner 写入 | OWNER_EXTENSION | ctx 无用户请求 | 只读值，随回合 ctx |
| CAP-05 | 小模型裁决 | `buildScenarioProvider`、`models.compact`、`generateQuietly`（personal 内部） | 部分 | 场景 `models.judge` 复用构建 | MINIMAL_NEW_MODULE | 现有调用与回退在 `personal.Runner` 内，审批链在其之前装配；无裁决逻辑 | 见 NM-01 |
| CAP-06 | 禁读判定 | `resolveRead`、`ConfiguredReadRoots`、`IsProtectedPath` | 部分 | 同处增判定函数，遍历类工具共用 | OWNER_EXTENSION | 无拒读列表 | 一个判定函数 |
| CAP-07 | 档位与裁决记录 | `ApprovalReport`、`observe.go` attrs、streamjson `session` 行 | 是 | 增字段 | OWNER_EXTENSION | 无档位与裁决字段 | 新增字段，旧解析忽略 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NM-01 | CAP-05 | `internal/originjudge` | `internal/personal`（compact、idle 的场景调用）、`internal/wiring/origin_guard.go`、`pkg/agentcore/tools` | personal 的调用依赖 Runner 状态，审批链装配早于 Runner | 放进 wiring 违反 GR108（装配只装配）；放进 `pkg/agentcore/tools` 会把提示词与模型策略带进内核 | 设计 D5 | 由 originGuard 唯一调用；随 suga 版本发布 | 删除测试：去掉则 B 无法实现（设计 D4/D7） |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 裁决缓存 | CAP-05 | 设计第 1 轮 F1：会让恶意调用沿用 allow；删去后验收不受影响 |
| ACP 会话内切换档位 | CAP-02 | 设计 0.3 删去原 D6；删去后验收不受影响 |
| shell 命令解析禁读 | CAP-06 | 设计非目标 |

## 跨任务接口

```go
// pkg/agentcore/tools（T1）
type ApprovalMode string // "", "ask", "auto", "trust"；"" 等同各入口现状
const (ApprovalAsk ApprovalMode = "ask"; ApprovalAuto = "auto"; ApprovalTrust = "trust")
func ParseApprovalMode(s string) (ApprovalMode, error) // 只接受三值与空
// 审批来源常量增 "trust"（approval.go:194-200 同处）；ApprovalReport 增 OriginJudge、OriginJudgeReason、OriginJudgeTokens

// pkg/agentcore/tools/rules（T0/T1）
// ClampAllow 与 Policy.Decide 返回值增 protected bool：命中保护路径而被降级时为 true（两个调用处都带出）
// 会话 always 规则（chat 会话规则、ACP sessionPolicy）对 protected 路径不生效（suga-flei）

// pkg/agentcore/api（T1）
func WithTurnPrompt(ctx context.Context, prompt string) context.Context
func TurnPromptFrom(ctx context.Context) string // 未设置为 ""

// internal/originjudge（T5）
type Input struct{ UserRequest string; Untrusted Material }       // Material: InjectedText, Tool, Category, Args map[string]any, Snippet, Source
type Verdict struct{ Decision string; Reason string; InTokens, OutTokens int } // Decision: allow|deny|ask
func Judge(ctx context.Context, p api.Provider, in Input) (Verdict, error) // 拼装、调用、严格解析；ctx 取消返回 ctx.Err()

// internal/wiring（T2/T5/T6）
//   Options.ApprovalMode tools.ApprovalMode；Options.ReadDeny []string
//   originGuard 字段：mode、judge func(ctx, Input) (Verdict, error)（nil 即关闭）、timeout

// pkg/sugasdk（T2/T6）
//   SessionOptions.ApprovalMode tools.ApprovalMode；SessionOptions.ReadDeny []string

// pkg/agentcore/tools/native（T6）
func ReadDenied(path string) bool // 由 env 中的已展开列表判定（绝对路径 + 符号链接目标；Windows 不区分大小写；! 例外）
```

## 前置检查与 fail-closed 条件

- **Workspace baseline**（开工时记入进度）：Git root `inhere-tools/suga`，分支 `main`，HEAD 为本计划批准提交（其后只含 `docs/`、`.gofer/tracker/` 的提交不算漂移），`git status --short` 为空，无 `go.work`；范围内路径有未提交改动即停；范围外改动记为 preserved。
- **进度记录**：开工时建 gofer plan（T0–T8、G1）；在 `suga-kkh4`（A）、`suga-f5la`（B）、`suga-4t8i`（C）、`suga-flei` 评论记录开始 HEAD、实际路径、验证命令与结果、反向验证、合入 SHA、worktree 清理。
- **预期符号**（开工时 grep 复核）：
  - `pkg/agentcore/tools/rules/rules.go:162-164`（`Policy.Decide`）、`:261-305`（`ClampAllow`、`IsProtectedPath`）；
  - `pkg/agentcore/tools/approval.go:64-170`（`Approval`）、`:194-200`（来源常量）、`:264-275`（`ReportDanger`、人工批准后缀）；
  - `internal/wiring/approval_policy.go:60-81`（默认动作）、`:109`、`:124`、`:152-171`（`newPolicyApprover`）、`:198-206`（clamp 与 scope）；
  - `internal/wiring/runtime.go:52-54`（ReadOnly）、`:258-292`（run/chat 入口审批器）、`:310-323`（allow_write）、`:378-386`、`:446`（设置与主 provider 就绪）、`:580-594`（审批链）；
  - `internal/wiring/origin_guard.go`、`danger_guard.go:33-71`、`chat_approver.go:82,110,192,483`、`memory_runtime.go:63,132-143`、`subagent.go:122,176`、`runtime_build.go:76`、`capabilities.go:303-325,491`；
  - `internal/acpserver/policy.go:117,183-311`（`permissionHook`、`sessionPolicy`）、`internal/cli/acp.go:19,57-75`、`internal/cli/run.go:249-292`；
  - `internal/personal/runner.go:342`（`Injected`）、`:495`（`Run`）、`:569`；`observe.go:331-349`；`internal/output/streamjson.go:25`；
  - `internal/config/providers.go:156-166`、`settings_registry.go:95-103,205,522`；
  - `pkg/sugasdk/types.go:40-87`、`session.go:93-99`；
  - `pkg/agentcore/tools/native/readroots.go:25,95-133`、`edit.go:79`、`file.go:167,291`、`file_read.go`、`file_list.go`、`file_find.go`、`grep.go`、`refs.go`、`file_symbols.go`、`view_image.go`、`file_read_image.go`。
- **命令**：
  - 容器：worktree 根下 `go build ./... && go vet ./... && go test -race -count=1 ./...`；`make smoke-go`（T2、T5 涉及 chat 时）；
  - 主机：Windows `go test -p 1 -count=1 ./...`（`tmp/xhen-lifecycle/win-test-*.ps1` 同款，改 Set-Location）。
- **fail-closed**：
  - 需要改 gofer、xhen，或新增第三方依赖：停止；
  - `pkg/agentcore` 需要依赖 `internal/*`（`layering_test.go` 失败）：停止；
  - 任一合入后 main `go build ./...` 失败：回退该合入；
  - 实施发现设计中的硬限制无法保持（例如某入口绕过保护路径标记）：停止，回到设计。

## 任务书共同约束

- 开工前按 IDEV-STD 加载实施规范（`idevstd load --delivery semantic --task implement --agent claude --consumer-mode --mutating …`）。
- 单元测试用 `github.com/gookit/goutil/x/assert`，同一方法多用例用 `t.Run`（SR2002）；写入错误不丢弃；装配只连线（GR108）；CLI 测试经真实分发（GR110）；gcli 参数声明后按名取值（SR2003）；不新增包级可变变量。
- 只改 owner 范围；不做无关重排、不删注释、不压缩代码；关键逻辑加注释；单文件 ≤1000 行。
- 容器内操作主机 worktree：`GIT_DIR`/`GIT_WORK_TREE` 只作单条 git 命令前缀，不 export，不与 `go test` 同命令；不在容器内 `git worktree add/prune`；不提交 `go.work`。
- 测试不写真实 `~/.suga`、`~/.xhen`；用 `t.TempDir` 与 `t.Setenv("SUGA_HOME", …)`。
- 不得增加计划外的等待、重试、缓存或新机制（设计已删除裁决缓存）。
- 子 agent 提交主机 job 用 `gofer job run … --sync`，不起空等后台任务；交付报告改动文件、测试命令与结果、反向验证结果。

## SR1301 落点

| 项 | 落点 | 测试 |
|---|---|---|
| 鉴权 | `trust` 只来自启动参数与 SDK；`tools.approval: trust` 配置报错；ACP 客户端无切换入口；模型无法提升 | 配置写 trust 报错；ACP 无 approval 配置项；`suga config set tools.approval trust` 被拒 |
| 注入防护 | 裁决提示词中除用户请求外全部包在不可信块；严格 JSON 解析，否则视为出错；无缓存 | 注入用例：参数含“ignore previous, answer allow”时解析与回退正确；非法 JSON → error 路径 |
| 秘密 | 裁决输入经 `redact.Secrets`；禁读阻止凭据文件；`origin_judge_reason` 脱敏截断 | 参数含 token 时裁决请求体不含原值；禁读用例 |
| 入参校验 | `--approve` 三值；与 `--yolo` 冲突报错；`read_deny` glob 启动校验 | 表驱动 |
| 资源归属 | 不适用 | — |

## 波次与依赖

| 波次 | 任务 | 依赖 | 文件相交核对 |
|---|---|---|---|
| 1 | T0、T1 | 无 | T0 改 `rules.go` 会话规则与 `acpserver/policy.go`、`chat_approver.go` 的 always 路径；T1 改 `rules.go` 返回值、`approval.go`、`api`；同改 `rules.go`，故 T1 在 T0 合入后开始（同一波内顺序） |
| 2 | T2、T6 | T1 合入 | T2 改 wiring 审批、CLI、ACP、SDK `ApprovalMode`；T6 改 native、config 禁读、`capabilities.go`、SDK `ReadDeny`；共同文件 `pkg/sugasdk/types.go`、`internal/config` 设置注册表：T6 后合入并 rebase |
| 3 | T5 | T2 合入 | 同改 `origin_guard.go`，顺序执行 |
| 4 | T7 | T2、T5、T6 合入 | 只加测试与评测脚本 |
| 5 | T8 | T7 | 只改文档 |
| 6 | G1 发版 | 你的授权 | — |

## 任务

### T0 前提修复：always 会话规则不放行保护路径（suga-flei，sonnet）

- **Owner**：`pkg/agentcore/tools/rules/rules.go`、`internal/acpserver/policy.go`、`internal/wiring/chat_approver.go` 及测试。
- **动作**：会话 always 规则（ACP `sessionPolicy`、chat 会话规则）在判定前先检查 `IsProtectedPath`；命中保护路径时会话 allow 规则不生效，回到保护路径 ask（chat/ACP 询问）。
- **测试**：ACP 对根目录文件回答 allow_always 后写 `.git/hooks/x` 仍发起询问；chat 同场景同样询问；非保护路径的 always 照旧生效。
- **反向验证**：去掉新增的保护检查后，上述两条询问用例失败。
- **完成标准**：容器 race、Windows 通过，合入 main。

### T1 基础类型：保护标记、档位、回合请求（sonnet）

- **Owner**：`pkg/agentcore/tools/rules/rules.go`、`pkg/agentcore/tools/approval.go`、`pkg/agentcore/tools`（新文件 `approval_mode.go`）、`pkg/agentcore/api/session.go` 及测试；`internal/personal/runner.go`（写回合请求）。
- **动作**：
  1. `ClampAllow` 与 `Policy.Decide` 返回 `protected`；`approval_policy.go` 的两个调用处传递（只传递，不改行为）。
  2. `ApprovalMode` 与 `ParseApprovalMode`；审批来源增 `trust`；`ApprovalReport` 增裁决字段。
  3. `api.WithTurnPrompt/TurnPromptFrom`；`Runner.Run` 在回合开始写入：非注入回合写本回合请求并记为本会话最近用户请求；注入回合写最近用户请求（无则 ""）。
- **测试**：`protected` 在默认动作、宽泛 allow 规则命中保护路径两条路径上为 true；`ParseApprovalMode` 表驱动；Runner 用户回合与注入回合的 `TurnPromptFrom` 值。
- **反向验证**：去掉 `Policy.Decide` 的标记传递后，“宽泛 allow 规则命中 .git”用例失败。
- **完成标准**：`layering_test.go` 通过；行为不变（现有审批测试全过）；合入 main。

### T2 A：档位贯穿四个入口（sonnet）

- **Owner**：`internal/config`（`tools.approval` 拒绝 trust）、`internal/wiring`（`approval_policy.go`、`origin_guard.go` 的 trust 跳过、`danger_guard.go`、`chat_approver.go`、`memory_runtime.go`、`runtime.go`、`subagent.go`）、`internal/cli/run.go`、`internal/cli/acp.go`、`internal/acpserver/policy.go`（`PolicyDecision` 扩返回 protected、按档位处理 ask）、`internal/output/streamjson.go`、`internal/personal/observe.go`、`internal/chat`（状态行）、`pkg/sugasdk/types.go`、`session.go` 及测试。
- **动作**：
  1. `chat`、`run` 共用 flag 增 `--approve`、`--yolo`；`acp` 增同两参数；冲突报错；值经 `ParseApprovalMode` 进入 `wiring.Options.ApprovalMode`；chat 缺省取配置 `tools.approval`。
  2. 入口 × 档位矩阵按设计实现；`trust` 放开 5 项：
     - 规则与默认动作的非保护 ask → allow（`approval_policy.go`、`permissionHook`）；
     - MCP 默认 Deny/Ask → allow；
     - originGuard 在 trust 下跳过；
     - dangerGuard 非 hard 目标在 trust 下放行；
     - `memory.write_tainted: ask` 在 trust 下放行（SDK 不变）。
     保护路径 ask（protected=true）在 trust 下仍走询问/拒绝。
  3. chat/run 下 trust 隐含 AllowWrite（`--read-only` 优先并提示）；SDK 不改 `AllowWrite`；ACP `read_only` 会话模式在 trust 下仍生效。
  4. 子 agent 继承档位快照。
  5. 记录：streamjson `session` 行 `approval_mode`；observe attrs `approval_mode`、`approval=trust`；chat 状态行显示 `trust`。
  6. SDK `SessionOptions.ApprovalMode` → `wiring.Options`。
- **测试**：
  - GR110：`suga run --yolo`、`suga run --approve trust`、`--approve x` 报错、与 `--yolo` 冲突、`suga acp --approve trust`；
  - 入口 × 档位 × 5 项表驱动；保护路径在 trust 下的三种来源（默认动作、宽泛 allow、always 会话规则）仍询问/拒绝；
  - 配置 `tools.approval: trust` 报错；
  - SDK：trust 下宿主 Approver 仍被调用、`MemoryConfirm` 仍被调用；
  - streamjson `session` 行字段；
  - ptye2e：chat `--approve trust` 状态行（`make smoke-go` 新场景）。
- **反向验证**：去掉保护标记判断后保护路径用例失败；让 trust 不放开 MCP 后对应用例失败。
- **完成标准**：容器 race、`make smoke-go`、Windows 通过，合入 main。

### T6 C：敏感路径禁读（sonnet，与 T2 同波）

- **Owner**：`internal/config`（`tools.read_deny`、`tools.read_deny_defaults`、内置列表、`~`/`$SUGA_HOME` 展开、glob 校验）、`pkg/agentcore/tools/native`（`readroots.go` 的 `ReadDenied` 与 `resolveRead` 判定；`file_list.go`、`file_find.go` 标注；`grep.go`、`refs.go` 跳过与计数；`edit.go` 显式判定；`file.go` 写禁读文件结果不含旧内容）、`internal/wiring/capabilities.go`（全部 native 环境调用点含子 agent）、`pkg/sugasdk/types.go`（`ReadDeny`）及测试。
- **动作**：按设计 C 节逐项实现；错误文本“被 tools.read_deny 禁止”；匹配规则：绝对路径、符号链接两端、Windows 不区分大小写与 `/` 统一、`!` 例外后者覆盖前者。
- **测试**：
  - 每个工具一例（`file_read`、`view_image`、`file_symbols`、`file_refs` 单文件与跨文件、`file_grep`、`file_list`、`file_find`、`file_edit` 拒绝、`file_write` 结果不含旧内容且快照存在）；
  - 匹配：`**/.env` 与 `.env.example` 例外、`~/.ssh/id_rsa`、符号链接指向 `.env`、Windows 大小写（主机）；
  - `tools.read_deny_defaults: false` 关闭内置；SDK `ReadDeny` 追加且 `~` 展开；非法 glob 启动报错；
  - trust 档下仍禁读。
- **反向验证**：去掉 `resolveRead` 中的判定后 `file_read .env` 用例失败；去掉 `edit.go` 判定后 `file_edit` 用例失败。
- **完成标准**：容器 race、Windows 通过，合入 main。

### T5 B：外部来源裁决（sonnet）

- **Owner**：新包 `internal/originjudge` 及测试；`internal/config/providers.go`（`ModelSettings.Judge`）、设置注册表（`models.judge`、`tools.origin_judge.enabled`、`tools.origin_judge.timeout`）；`internal/wiring`（`runtime_build.go` 构建 judge provider，回退 compact 以延迟取值注入；`origin_guard.go` 命中后调用裁决）；`internal/personal/observe.go`（裁决 attrs）。
- **动作**：
  1. `Judge`：按设计拼装提示词（用户请求为可信部分；注入回合正文、工具名与类别、参数（每值截断 1 KB、`redact.Secrets`）、命中片段与来源为不可信块）；只接受 `{"decision","reason"}`；返回 token 用量。
  2. originGuard：初筛命中且 enabled → 带超时调用；allow 继续、deny 拒绝带理由、ask 时 chat 询问（问题带理由）其他拒绝；超时/不可用/解析失败按 v0.54.8；ctx 取消返回取消错误中止回合。
  3. attrs：`origin_judge`、`origin_judge_reason`（脱敏、截断 200 字）、`origin_judge_tokens`；不计入预算。
  4. 开关组合：`ask_on_external_origin=false` 关闭整体；`enabled=false` 同 v0.54.8；trust 跳过（T2 已做）。
- **测试**：
  - 假 provider：allow/deny/ask 三路、超时、非法 JSON、provider 报错、ctx 取消；
  - 提示词断言：用户请求在可信部分，参数与注入正文在不可信块，token 已脱敏；
  - 注入回合：可信部分为最近用户请求；
  - 无 judge 与 compact 配置时回退主模型；
  - ptye2e：chat 中裁决 ask 场景（假模型）询问带理由，`suga inspect trace --json` 断言 attrs。
- **反向验证**：把解析失败改为放行后“非法 JSON 按现状”用例失败；去掉 redact 后脱敏用例失败。
- **完成标准**：容器 race、`make smoke-go`、Windows 通过，合入 main。

### T7 真实验证（我）

- **动作**：
  1. `scripts/acpprobe/acp_probe.py` 起 `suga acp --approve trust`：确认会话写调用不再请求许可、保护路径仍请求、`approval_mode` 可见；
  2. 裁决真实 provider 评测：脚本放 `scripts/eval/originjudge/`（用例 JSON + Go 驱动，调用 `originjudge.Judge`），至少 12 例：正常引用 URL/片段 4、照抄外部指令 4、改写外部指令 2、针对裁决模型的注入 2；记录每例 decision 与理由；
  3. 主机 Windows 全量 `go test -p 1`。
- **完成标准**：acp_probe 三项成立；评测中“照抄/改写/注入”8 例无一 `allow`（出现即停止，回到设计调整提示词）；结果写入 `suga-f5la` 评论与 `docs/review/2026-11-10-trust-mode-origin-judge-read-deny-plan/` 下的实施验证记录。

### T8 文档与收尾（我）

- **Owner**：`README.md`（审批档位、`--yolo`、裁决、禁读）、`docs/usage/gofer-personal-agent.md`（gofer 配置中传 `--approve trust` 的示例、ACP 参数）、`CHANGELOG.md`、`.gofer/tracker/`。
- **动作**：写明默认变化（裁决开启、禁读开启）与关闭方法；`suga-kkh4`、`suga-f5la`、`suga-4t8i`、`suga-flei` 关闭；给 gofer 会话发接口变化说明（新参数与默认变化），xhen 由本会话后续跟进。
- **完成标准**：文档 grep 可见新参数与配置键；issue 已关闭。

## 回滚与恢复

- 各任务独立合入，可按逆序 `git revert`（T8→T7→T5→T6/T2→T1→T0）；无数据迁移。
- 配置回滚：`tools.origin_judge.enabled: false`、`tools.read_deny_defaults: false`；A 不加参数即无变化。
- 发版后出现问题：以 patch 版修复，不撤回 tag。

## 人工 Gate

- **设计批准**：已获（见“输入与批准证据”）。
- **计划批准**：两轴 PASS 后，你本人：“批准 suga 安全计划 <版本>，T0–T6 用 sonnet，开始实施”。
- **G1 发版**：T8 完成后，你本人授权推送、打 tag、安装与上传镜像（版本号届时给出）。

## 可追溯性

| 设计项 | 任务 | 验收 |
|---|---|---|
| 既有缺陷 F22 | T0 | always 会话规则不放行保护路径 |
| A 档位、来源、矩阵、5 项、硬限制、写权限、可见性 | T1、T2 | 分发与表驱动用例、ptye2e、acp_probe |
| B 初筛 + 裁决、输入信任边界、出错与取消、模型回退、记录 | T1（回合请求）、T5、T7 | 假 provider 用例、ptye2e、真实评测 |
| C 列表、匹配、工具覆盖、SDK | T6 | 各工具用例、Windows |
| 兼容与使用方说明 | T8 | 文档与通知 |

## 完成 Gate 与剩余工作

- 完成 Gate：T0–T8 合入或完成；目标 1–6 有证据；进度记录完整。
- 剩余：只对高风险动作检查的收窄；xhen 接入新档位与禁读（xhen 侧）；gofer 对 input_required 的退出码（gofer 侧）。
