<!-- template_id: plan; template_version: 1.3.1 -->
# suga 记忆可信开关 实施计划

> 状态：Draft 0.1 / 待人工计划批准；issue `suga-7s4v`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-26 | Claude | 初稿：按已批准设计 0.4 拆为 T1–T4 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

**思考与范围声明**
- 思考模式：`RIGOROUS`。
- 核心目标：落实设计 0.4——`memory.user.trusted`（bool）与 `memory.project.trusted`（仓库根目录列表）为真时，该作用域 get/search 结果不标外部来源、不污染会话；两键受保护；manifest 显示实际判定。
- scope freeze：设计 0.4 的总体方案 1–8、D1–D7 与“验证要求”。不含设计非目标与 deferred（worktree 按 `git-common-dir` 沿用主仓库可信）。
- 扩围策略：`DEFER_OR_REQUEST`；review budget：Full 档两轴计划评审，轮次按 `planning-core-rigorous`；停止条件：无开放 `CORE_BLOCKING`。
- 暴露度：Full（改变安全边界，依据见设计“安全、数据、运维与回滚”）。

## 目标与完成定义

完成定义（全部满足）：
1. 当前仓库根在 `memory.project.trusted` 中时，project 作用域 get/search 结果无 `external`，照抄 40 字节以上的 `shell_exec` 不被 originGuard 标记，会话无 `tainted_at`；未登记仓库、父目录登记、worktree 照旧被标记。`memory.user.trusted: true` 对 user 作用域同理。
2. 非法值（project 写成 `true`、标量、空项、相对路径；user 非布尔）启动失败并带键名。
3. 两键 Protected、归 `relax` 组：会话内与管道 stdin 的 `config set` 退出码 2、文件不变；改动启动提示报出键名。
4. `suga manifest`（JSON 与文本）的记忆项显示 `trusted`。
5. 设计“验证要求”全部落到测试并通过（单测、GR110、链路、ptye2e Linux 与 Windows ConPTY、反向验证）；文档写明登记方式与风险。

## 范围、排除项与授权

- 范围：suga 仓库 `internal/config/settings_registry.go`、`internal/config/settings_protected_test.go`、`internal/config/protected_snapshot_test.go`；`internal/wiring/memory.go`、`memory_runtime.go`、`manifest.go`、`main_test.go`（helper 分支）及测试；`internal/cli/manifest.go`、`internal/cli/config_protected_test.go`、`internal/cli/manifest_test.go`；`pkg/agentcore/tools/native/memory.go` 及测试；`test/ptye2e` 新场景、`fakellm_test.go` 一个模式、新 helper `test/ptye2e/testdata/fakememexec/main.go`；`config/config.yaml` 注释；README、`docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/config.md`、CHANGELOG「未发布」。
- 排除：worktree 沿用主仓库可信；按条目/写入者区分可信；改 ExternalOrigin 窗口与裁决；改 `taint.go`；gofer 侧改动；发版、推送、安装。
- 授权：本计划批准只授权进入实施请求；实施需你另行指示。当前执行请求将覆盖：建 worktree 分支 `feat/trusted-memory`、在其上本地提交、rebase 到本地 main、合并到本地 main；不含 push、tag、发版、安装。
- `host_or_non_offline_action=REQUIRED`：主机建 worktree `.worktrees/trusted-mem`（`tmp/xhen-p1/wt-repo.ps1`）、rebase 与合并到本地 main（`tmp/xhen-p1/host-merge.ps1`）；执行者 `gofer job -a suga` 在主机运行并调用模型 provider（联网、产生费用）；主机 Windows 测试；`staticcheck@2026.2.1` 联网下载模块。均不推送、不发布。

## 输入与批准证据

- 设计：`docs/design/2026-11-26-trusted-memory-backend-design.md` Draft 0.4，批准记录提交 `3e641baa`。
- 批准原话（2026-11-26）：“批准 suga 记忆可信开关设计 0.4（接受未复核修订），开始写计划”；Q1、Q2、Q4、Q5 的选择见设计修订记录。
- 设计评审：`docs/review/2026-11-26-trusted-memory-backend/`（两轮两轴 PASS，ledger `85426724`）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 受保护键、会话内拒绝、启动比较 | `ConfigKey.Protected`、`protectedWriteAllowed`、`protectedKeyDigests` | 是 | 注册两行、分类表两行 | DIRECT_REUSE | none | 无 |
| CAP-02 | 读取、校验与仓库根匹配 | `loadMemoryConfig`、`selectProjectBackend` 的 `gitRoot`、`parseProjectTrust` | 是 | 既有 owner 扩展（`MemoryConfig` 两字段、`MemoryBackendChoice.Trusted`） | OWNER_EXTENSION | 无现成“路径列表 + 整根匹配” | `project_trust` 是 map→hash，语义不同，不合并 |
| CAP-03 | 外部来源判定 | `MemoryTool.external`、`MemoryToolOptions.Backend` 回调 | 是 | 既有 owner 扩展（`Trusted` 回调） | OWNER_EXTENSION | none | 无 |
| CAP-04 | 假 exec 后端与伪终端 | `execstore_test.go` helper、`wiring/main_test.go` TestMain、`test/ptye2e` `startChatOpts`、`fakeLLM` `origin` 模式、`JudgeReply` | 是 | wiring 用 TestMain helper 分支；ptye2e 用 `go build` 的小 helper | OWNER_EXTENSION | ptye2e 无 TestMain 与 exec 夹具 | helper 只在测试中使用 |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| NONE | none | none |

characterization：本设计改变行为，不声称等价，不做基线回放。默认 false 的回归由现有用例不改即过来保证：`TestMemoryToolExternalReads`、`TestExternalOriginGuardThroughAssembly`、`TestTaintConfigLists`、`TestChatOriginJudge`、`TestChatMemory`。

## 前置检查与 fail-closed 条件

- workspace baseline（2026-11-26，0.1 写作时）：Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；branch `main`；HEAD `3e641baa`；`git status --short` 为空；另有 worktree `.worktrees/fork`（分支 `feat/background-fork`，他人实施中，不碰）。expected symbols：`loadMemoryConfig`、`MemoryConfig`、`MemoryBackendChoice`、`selectProjectBackend`、`gitRoot`、`newMemoryRuntime`、`MemoryToolOptions`、`MemoryTool.external`、`ManifestMemory`、`protectedClassification`、`protectedWriteAllowed`、`configStdinIsTerminal`。实施开始时重新记录 HEAD 与 status。
- 进度跟踪：开始时 `gofer plan create --title "suga 记忆可信开关实施" --project my-tools-dev --desc "仓库 inhere-tools/suga；计划 docs/plans/2026-11-26-trusted-memory-backend-plan.md"`，再 `gofer plan add-todo <plan-id> "T1 …" --cwd inhere-tools/suga --after prev` 建 T1–T4；每个执行者 job 挂到对应 todo。
- worktree：主机 `gofer job run -p my-tools-dev -a exec --runner local --sync -- pwsh -File tmp/xhen-p1/wt-repo.ps1 -Repo inhere-tools/suga -Name trusted-mem -Branch feat/trusted-memory`（基于当时的 main）。容器内不 `git worktree add/prune`；对 worktree 的 git 命令只用单条前缀 `GIT_DIR`/`GIT_WORK_TREE`，不 export，不与测试同一命令。
- 执行者：T1–T3 由 `gofer job -a suga` 在 `.worktrees/trusted-mem` 顺序实施，每任务一个 job、≤45 分钟（`--timeout 5400`）；模型由你在计划 Gate 指定，默认建议 `opencode-go/deepseek-v4.1-flash`。我审查（先看 `git diff --stat` 删除行）并复核反向验证；T4 由我执行。
- 任务书约束（每个 job 都写）：只改本任务列出的文件；不删注释、不重排无关代码；不用 web_fetch；可改 go.mod/go.sum（预期不改）；遵守 GR104、GR107–GR110 与 `pkg/agentcore` 不依赖 `internal/*`；只暂存本任务文件，提交前核对 `git diff --cached --name-only`；禁用 `git stash`、`git reset`、`git checkout -- <他人路径>`；首个提交不晚于开始后 20 分钟；`--sync`，不起空等后台任务；不在 export 了 `GIT_DIR`/`GIT_WORK_TREE` 的命令里跑测试。
- 测试环境：执行者在 Windows 主机，任务内 focused 测试不带 `-race`；每个任务验证先跑 worktree 根 `go build ./...`；`-race`、Linux ptye2e、staticcheck 在 T4 由我在容器里跑。测试用临时 `SUGA_HOME`；断言用 `github.com/gookit/goutil/x/assert`，多用例 `t.Run`（SR2002）。
- 反向验证写法：临时改动必须可编译，确认输出含对应断言失败且不含 `[build failed]`，恢复后写进提交说明。
- fail-closed：`go build ./...`、focused 测试或反向验证不符合预期即停止，按实施发现四类记录；遇到他人改动（Ownership Conflict）停止。
- 依赖：不新增第三方依赖。

## 波次与依赖

| 波次 | 任务 | 说明 |
|---|---|---|
| W1 | T1 | 注册、分类、读取校验、匹配、GR110 |
| W2 | T2 | 判定回调、传值、manifest、wiring 链路 |
| W3 | T3 | ptye2e 场景与文档 |
| W4 | T4 | rebase、全量、主机 Windows、合并（我执行） |

## 任务

### T1 注册、读取校验与仓库根匹配（方案 1–4、D2、D6）

- 文件: `internal/config/settings_registry.go`；`internal/config/settings_protected_test.go`；`internal/config/protected_snapshot_test.go`；`internal/wiring/memory.go`；`internal/wiring/memory_test.go`；`internal/cli/config_protected_test.go`；`config/config.yaml`（`memory` 段注释两行）。
- 动作（签名为合同）:
  - 注册表在 `memory.user.backend`、`memory.project.backend` 旁各加一行：`{Pattern: "memory.user.trusted", Type: ConfigTypeBool, Writable: true, Protected: true}`、`{Pattern: "memory.project.trusted", Type: ConfigTypeStringList, Writable: true, Protected: true}`；分类表 `relax` 组加两键。
  - `MemoryConfig` 增 `UserTrusted bool`、`ProjectTrustedRoots []string`；`loadMemoryConfig` 内调用 `func loadMemoryTrust() (user bool, roots []string, err error)`：未设置 → false / nil；user 值不是布尔 → `memory.user.trusted must be true or false`；project 值不是字符串列表（含 `true`、标量字符串）→ `memory.project.trusted must be a list of absolute repository root paths`；某项 trim 后为空 → `memory.project.trusted item %d is empty`；非绝对路径 → `memory.project.trusted item %q must be an absolute path`；合法项存 `filepath.Clean` 后的值。错误为英文，与现有 `memory.*` 配置错误一致，不进 i18n。
  - `func rootTrusted(root string, roots []string, fold bool) bool`：`filepath.Clean(root)` 与每项整串相等；`fold` 为真用 `strings.EqualFold`；不做前缀匹配、不解析符号链接。
  - `MemoryBackendChoice` 增 `Trusted bool`。`selectProjectBackend` 在算出 `root` 后置 `choice.Trusted = rootTrusted(root, cfg.ProjectTrustedRoots, runtime.GOOS == "windows")`；`selectUserBackend` 置 `choice.Trusted = cfg.UserTrusted`；`selectExplicitProjectBackend`（host-key）恒为 false。
- 验证: worktree 根 `go build ./...`；`go test ./internal/wiring -run 'TestLoadMemoryConfig|TestLoadMemoryTrust|TestRootTrusted|TestSelectProjectBackend|TestMemoryExecBackend' -count=1`（`TestLoadMemoryTrust`：合法列表、`true`/标量/空项/相对路径/user 写成字符串各报对应错误；`TestRootTrusted`：整根相等、子目录与父目录不匹配、尾部分隔符经 Clean 相等、`fold` 真假两种大小写；`selectProjectBackend`：已登记、未登记、登记父目录、host-key 四种 `Trusted`）；`go test ./internal/config -run 'TestProtectedClassification|TestProtectedSnapshot' -count=1`（新增子用例：普通 YAML 写法改动 `memory.project.trusted` 列表一项，变化键名含该键）；`go test ./internal/cli -run TestConfigProtected -count=1`（新增子用例经 `app.Run([]string{"config","set","memory.project.trusted",<abs root>})`：替换 `configStdinIsTerminal` 为 true 且 `SUGA_SESSION_ID` 为空时成功；默认管道 stdin、带 `SUGA_SESSION_ID` 时退出码 2 且文件逐字节不变）。反向验证：R1 把 `rootTrusted` 改为 `strings.HasPrefix`，父目录与子目录用例失败；R2 去掉 `memory.project.trusted` 的 `Protected`，`TestProtectedClassification` 与 GR110 子用例失败；R3 删分类表中该键，`TestProtectedClassification` 失败；R4 让 `loadMemoryTrust` 对非列表值返回 nil 不报错，`TestLoadMemoryTrust` 失败。
- 完成标准: 用例通过；R1–R4 记录在提交说明。
- 依赖: 无。

### T2 判定回调、传值、manifest 与 wiring 链路（方案 5–8、D3–D5、D7）

- 文件: `pkg/agentcore/tools/native/memory.go`、`pkg/agentcore/tools/native/memory_test.go`；`internal/wiring/memory_runtime.go`；`internal/wiring/manifest.go`、`internal/wiring/manifest_test.go`；`internal/cli/manifest.go`、`internal/cli/manifest_test.go`；`internal/wiring/main_test.go`（helper 分支）；新 `internal/wiring/memory_trusted_test.go`。
- 动作:
  - `MemoryToolOptions` 增 `Trusted func(memory.Scope) bool`（注释：未设置按不可信）；`external(s)`：`Trusted != nil && Trusted(s)` 返回 false，否则沿用现有判定；更新 `Backend` 字段与 `external()` 注释（不再写“非 files 一律外部”）。
  - `newMemoryRuntime` 传 `Trusted: func(s memory.Scope) bool`：`s.Kind == memory.ScopeUser` → `setup.User.Trusted`；`s == setup.Project.Scope` → `setup.Project.Trusted`；其余 false。不读 gkconfig。
  - `ManifestMemory` 增 `Trusted bool json:"trusted"`，取 `MemoryBackendChoice.Trusted`；`internal/cli/manifest.go` 记忆行末尾加 `, trusted=%t`。
  - `main_test.go` 的 TestMain 开头：环境变量 `SUGA_TEST_MEMEXEC` 非空时充当 exec 协议 v1 后端（读一行请求，`get`/`search`/`index` 从该变量指向的 JSON 夹具应答，见 `pkg/agentcore/memory/execstore/execstore.go` 包注释），然后 `os.Exit(0)`；参照 `execstore_test.go` 的 helper。
  - `memory_trusted_test.go` `TestTrustedMemoryChain`：经 `OpenCLI`（`ChatMode`、`tools.approval: auto`、`memory.project.backend: exec`、`memory.project.command: os.Args[0]`、临时 git 仓库为工作区），执行 `memory get` 后执行照抄正文 40 字节以上的 execute 调用（同 `TestExternalOriginGuardThroughAssembly` 的 stub 写法）。
- 验证: worktree 根 `go build ./...`；`go test ./pkg/agentcore/tools/native -run TestMemoryToolExternalReads -count=1`（新增：trusted 作用域 get/search 无 `external`；未 trusted 的 gofer/exec 仍有；`Trusted` 为 nil 仍有；files 不变）；`go test ./internal/wiring -run 'TestTrustedMemoryChain|TestLoadManifest|TestExternalOriginGuardThroughAssembly|TestTaintConfigLists' -count=1`（`TestTrustedMemoryChain` 子用例：①仓库根已登记 → asker 无提问、stub 运行、会话 meta 无 `tainted_at`；②列表只含另一个根（模拟第三方仓库）→ 提问一次、有 `tainted_at`；③列表只含父目录 → 同②；④工作区为该仓库的 worktree 目录（`.git` 文件）且主仓库已登记 → 同②；⑤已登记且 `memory.taint_tools: [memory]` → 有 `tainted_at`、无提问；`TestLoadManifest` 新增已登记 project `trusted=true`、user 未设 false）；`go test ./internal/cli -run TestManifestCommand -count=1`（文本含 `trusted=`）；`go test ./pkg/agentcore -run TestLayering -count=1`。反向验证：R5 让 `external()` 忽略 `Trusted`，native 新用例与链路①失败；R6 让 `rootTrusted` 恒真，链路②③④失败；R7 manifest 的 `Trusted` 恒写 false，`TestLoadManifest` 新断言失败。
- 完成标准: 用例通过；现有用例不改即过；R5–R7 记录在提交说明。
- 依赖: T1。

### T3 ptye2e 场景与文档（验证要求“端到端”、方案运维条）

- 文件: 新 `test/ptye2e/testdata/fakememexec/main.go`；新 `test/ptye2e/chat_trusted_memory_test.go`（首行 `//go:build ptye2e`）；`test/ptye2e/fakellm_test.go`（模式 `memorigin` 一个分支与 Mode 注释一行）；新 `test/ptye2e/fakellm_memorigin_test.go`；README.md（Protected settings 的 *relax* 清单与 Memory 说明）；`docs/usage/gofer-personal-agent.md`（protected 清单与项目记忆一节）；`skills/suga-guide/references/config.md`（`memory.*` 表两行）；CHANGELOG.md「未发布」。
- 动作:
  - `fakememexec`：`package main`，只用标准库，按 exec 协议 v1 读一行请求、从 `FAKEMEMEXEC_FIXTURE` 指向的 JSON 应答 `index`/`get`/`search`；测试开始时与 suga 二进制同样 `go build -o <tmp> ./test/ptye2e/testdata/fakememexec`（不用 shell 脚本，Linux 与 Windows 同路径）。
  - `memorigin` 模式（`memoriginCall(step)`，参照 `fakellm_origin_test.go`）：第 0 步 `memory get {scope: project, id: <Fixture id>}`，第 1 步 `shell_exec` 的 `command` 为夹具正文（≥40 字节），之后回 Reply；`JudgeReply` 固定 `ask`。
  - `TestChatTrustedMemory`：临时 git 仓库为 `Work`，`MemoryExtra` 配 exec 后端与 `trusted` 列表。子用例：①已登记 → 屏幕不出现“逐字复制自 memory 结果的文本”，`suga inspect trace --json` 该 `shell_exec` 的审批事件无 `origin_judge` 属性；②列表只含另一个根 → 屏幕出现该询问且审批事件有 `origin_judge`。
  - 文档：两键含义、默认值、登记依据 `git rev-parse --show-toplevel`、嵌套仓库逐个登记（本工作区举例）、worktree 不生效、`config set` 整体替换列表、用 `suga manifest` 的 `trusted` 核对、风险（可信后端被写入恶意内容时不再有外部来源防护，写记忆不再确认）与例外（`taint_tools`/`taint_on_spill`）；`config/config.yaml` 已在 T1 加注释。
- 验证: worktree 根 `go build ./...`；`go vet -tags ptye2e ./test/ptye2e`；`go test -tags ptye2e ./test/ptye2e -run 'TestChatTrustedMemory|TestChatOriginJudge|TestChatMemory' -count=1 -v`，输出含 `--- PASS: TestChatTrustedMemory`；`grep -n "memory.project.trusted" README.md CHANGELOG.md docs/usage/gofer-personal-agent.md skills/suga-guide/references/config.md` 各有结果；`git diff -U0 main -- CHANGELOG.md` 改动只在「未发布」内。反向验证：R8 在 `newMemoryRuntime` 临时不传 `Trusted`（失败关闭），子用例①失败（出现询问）。
- 完成标准: 用例在 Windows ConPTY 下通过；R8 记录在提交说明（Linux 由 T4 跑）。
- 依赖: T2。

### T4 rebase、全量验证与合并（我执行）

- 文件: 无代码改动；主机脚本放 `tmp/suga-7s4v/`。
- 动作:
  - rebase：`feat/background-fork` 已合入本地 main 时，主机 job 在 worktree 执行 `git -C inhere-tools/suga/.worktrees/trusted-mem rebase main`；冲突按“风险”一节处理，解决后重跑 T1–T3 的 focused 命令。未合入时跳过，合并后通知 fork 会话 rebase。
  - 容器内 worktree 根：`go test -race ./...`、`go run honnef.co/go/tools/cmd/staticcheck@2026.2.1 ./...`、`make smoke-go`（含 Linux 上的 `TestChatTrustedMemory`）。
  - 主机 Windows（gofer exec job，只写 `tmp/`）：worktree 根 `go test ./...` 与 `go test -tags ptye2e ./test/ptye2e -run 'TestChatTrustedMemory|TestChatOriginJudge' -count=1 -v`。
  - 主机复现原场景（gofer exec job，只读真实配置、隔离 `SUGA_HOME=<WP>/tmp/suga-7s4v/home`）：在一个有 `.gofer/tracker` 的仓库登记 `memory.project.trusted` 后，`suga manifest` 显示 `trusted=true`；不做真实模型调用（核心行为已由假模型覆盖），结束删除 `home`。
  - 合并前核对（fail-closed）：主机 job `git -C inhere-tools/suga branch --show-current` 须为 `main`、`status --short` 为空（他人改动未清时按 Ownership Conflict 停止并询问）；记录合并前 `rev-parse HEAD` 作恢复点。
  - 合并：主机 `tmp/xhen-p1/host-merge.ps1 -Repo inhere-tools/suga -Branch feat/trusted-memory -MsgFile …`，再以主机 job `git -C inhere-tools/suga merge-base --is-ancestor feat/trusted-memory main` 确认退出码 0；CHANGELOG 已发布小节逐字未变。
- 验证: 以上命令全部通过；Windows 输出含 `--- PASS: TestChatTrustedMemory`；`manifest` 输出含 `trusted=true`。
- 完成标准: 全绿并合并到本地 main；issue `suga-7s4v` 评论附验证结果。
- 依赖: T3。

## 回滚与恢复

- 每个任务至少一个提交且 `go build ./...` 通过，恢复点为各任务提交；合并前改动只在 worktree 分支上。
- 合并后回滚：revert 合并提交（恢复点为 T4 记录的合并前 main HEAD）；旧版本把 `memory.*.trusted` 当未知键忽略，回到默认外部判定（失败安全）。用户侧回滚：删键，或 project 置 `[]`、user 置 false。
- 风险：
  - 与 `feat/background-fork` 的重叠（核对两份计划的文件清单）：`README.md`、`CHANGELOG.md`「未发布」、`test/ptye2e/fakellm_test.go`（fork T7 加分身识别，本计划加 `memorigin` 一个分支）。`internal/wiring/memory*.go`、`manifest.go`、i18n catalog 不重叠（fork 改 `subagent*.go`、`runtime.go` 与 `model.ini`/`cmd.ini`，本计划不改 i18n）。处理：fork 先开工、改动大，默认 fork 先合；本计划在 T4 rebase 到新 main，冲突全部是两边各加段落或分支，保留双方后重跑 focused 测试。本计划若先完成，照常合并并通知 fork 会话由其 rebase。
  - 安全取舍（设计已接受）：可信后端被他方写入恶意内容时只剩审批规则；文档如实写明。
  - Windows 路径大小写与符号链接：`rootTrusted` 只做 Clean 与大小写折叠，登记路径与 `git rev-parse --show-toplevel` 不一致时静默不生效（失败关闭），用 manifest 核对。

## 人工 Gate

1. 计划批准（评审后），同时指定 T1–T3 执行者模型（默认建议 `opencode-go/deepseek-v4.1-flash`）。仪式成本：设计评审 2 轮两轴；计划评审待进行。
2. 当前执行请求：你指示开始实施；覆盖建 worktree 分支 `feat/trusted-memory`、rebase 与合并到本地 main，不含 push。
3. 外部动作 Gate：push、tag、发版、安装均需你另行逐项批准。

## 可追溯性

| 设计条目 | 任务 | 验证 |
|---|---|---|
| 方案 1、2、D2（注册、分类、启动比较） | T1 | `TestProtectedClassification`、`TestProtectedSnapshot` + R2、R3 |
| 方案 3、D6（整根匹配、大小写、host-key） | T1、T2 | `TestRootTrusted`、`TestSelectProjectBackend`、`TestTrustedMemoryChain` ②③④ + R1、R6 |
| 方案 4（非法值启动失败） | T1 | `TestLoadMemoryTrust` + R4 |
| GR110（会话内与管道拒绝） | T1 | `TestConfigProtected` 子用例 + R2 |
| 方案 5、D4（回调、失败关闭、注释） | T2 | `TestMemoryToolExternalReads` + R5 |
| 方案 6（经 `MemorySetup` 传值、子 agent 不受影响） | T2 | `TestTrustedMemoryChain` ① |
| 方案 7、D3、D5（不记录、不污染、`taint_tools` 例外） | T2 | `TestTrustedMemoryChain` ①⑤ |
| 方案 8、D7（manifest `trusted`） | T2、T4 | `TestLoadManifest`、`TestManifestCommand` + R7；主机 manifest |
| Q4（worktree 不生效） | T2 | `TestTrustedMemoryChain` ④ |
| 端到端（审批事件 `origin_judge`、询问文本、judge 固定 ask） | T3、T4 | `TestChatTrustedMemory` ①② + R8，Windows 与 Linux |
| 运维文档 | T3 | grep |
| 验证要求（`-race`、Linux ptye2e、Windows、staticcheck） | T4 | 全量命令与主机 job |
| 合并到本地 main | T4 | `merge-base --is-ancestor` 退出码 0 |

## 完成 Gate 与剩余工作

- 完成条件：T1–T4 完成标准全部满足、合并到本地 main，issue `suga-7s4v` 评论附验证结果后关闭；gofer plan 的 todo 全部 done，plan 状态置 done。
- 进入 `COMPLETE` 时写 ≤8 行完成总结（主旨、状态、自主决策、提交数与区间、耗时、剩余项），并按 plan-closure 做一次 retrospective 判定与提示（仅首次 `COMPLETE` 写入时提示一次）。
- 剩余：发版与推送待你授权；deferred：worktree 按 `git-common-dir` 沿用主仓库可信（设计 Q4）。
