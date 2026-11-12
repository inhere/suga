<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护实施计划 第 3 轮 Spec/Executability 评审（纠正确认轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；candidate commit `9efb9d536322116baa5dae50d648c455ecb13e5b`；subject `docs/plans/2026-11-12-protected-config-keys-plan.md`
- 修订版本: Draft 0.3（上轮 `f8977380` / Draft 0.2，两轴 PASS）
- 评审者: 独立评审子 agent（Spec/Executability 轴，第 3 轮，唯一一轮纠正确认轮，REPORT_ONLY）
- 日期: 2026-11-12
- 排除项: 全量重审；Standards/Governance 轴；设计 0.8 的选择本身；不改仓库、不提交

## 输入与方法

- 规范：payload `SD95t2uhRN3Z9UqnPguVJg`；review-contract、plan-closure 与 review 模板取自 payload `3Z3cOanwUM2ILAXHbxiTlQ`（五类 `BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY` 与 necessity proof 要求）。
- 读取：上轮两轴报告；计划 0.3 全文与 `git diff f8977380..9efb9d53`。当前 HEAD `1dae6a7a` 只比 candidate 多一个 hooks 设计文档提交，代码与 candidate 相同。
- 对照代码：`internal/cli/{config,i18n,chat,acp}.go`、`internal/cli/config_test.go`；`internal/config/{load,config_candidate,yaml_edit,settings_registry,settings_registry_source}.go`；`internal/wiring/{runtime,subagent}.go`；`internal/personal/runner.go`；`internal/chat/{chat,render}.go`；`internal/i18n/{i18n.go,catalog/zh/*.ini}`；`test/ptye2e/smoke_helpers_test.go`；`scripts/acpprobe/acp_probe.py`；依赖 `coder/acp-go-sdk v0.13.5 connection.go`。用 `go list` 核对各包的 import 关系；核对 `-run` 正则与现有测试名；查看了容器的 `$HOME`。
- 本轮未运行 `go test`。

## 验收标准

- plan-closure：修订后的文件、符号、调用方式和验证命令能被唯一、正确地执行；验证命令能证明完成标准。
- review-contract：`CORE_BLOCKING` 只限五类，并附 necessity proof。

## 上轮发现关闭情况（PF13–PF20、PS17–PS23 中与本轴相关的部分）

| ID | 状态 | 依据 |
|---|---|---|
| PF13 / PS22 | 关闭（有残留，见 PF24） | `protectedWriteAllowed` 内用 `cliText(tr, "cli.config.protected_refused", …)`，`tr` 取自 `cliTranslator()`（`config.go:62`、`:94` 就是这样取的），写法对。签名带 `path`，所以调用点必须在 `activeConfigPath()` 之后（`config.go:76`）。这仍满足“`writableConfigKey` 之后、写入之前”。`status.ini` 的节是 `[status]`，chat 里的调用方式是 `tr().T("status.xxx")`（`chat.go:481`、`chat_idle.go:144`），计划写的“`status.protected.*`、按该节现有调用方式取”能正确执行。单元测试改为断言 `err.Error()`。 |
| PF14 | 关闭 | 无变化、比较失败和 `ok=false` 时，`NoticeFor` 返回 `ok=false`；失败结果按“无变化、不可推进”缓存；补了“无变化零条”的用例。 |
| PF15 / PS17 | 方向关闭，命令不可执行，见 PF22 | 改为“首次运行恰为 0 条”，并标注 `UNVERIFIED_REAL_ENV`，语义正确：`acp_probe.py:262` 会清空 home，`:272` 把 `SUGA_CONFIG` 指向 `--config-from` 下的 `config.yaml`，所以快照首建在 `home/run/protected-config/<sha>.json`。参数 `--bin/--cwd/--home/--config-from/--prompt` 与 argparse（`:239-243`）一致。 |
| PF16 | 关闭 | `ProtectedState.Path` 提供 `{path}`；chat 经 `chat.Options` 传到 `rendererOptions`（`chat.go:412`），`render.go:740` 的 `Banner()` 在 `chat.go:543` 被调用，之后接 `OnBannerShown`。顺序可行。 |
| PF17 | 关闭（有残留，见 PF24） | `personal.Config`（`runner.go:43`）加 `ProtectedNotice func(string)(map[string]any,bool)`，与 `(*ProtectedState).NoticeFor` 的方法值签名一致。`runtime.go:722` 的 `personal.New` 在 `Open`（:380 起）里面；`subagent.go:189` 是另一处 `personal.New`，不设这个字段，子会话就不会记录。 |
| PF18 / PS23 | 部分关闭，见 PF23 | 删掉了 `TestConfigLockPath`，新增 `TestProtectedPathNormalize`。新写的 `TestConfigPath` 匹配不到任何测试；锁名回归用例没有加进 T3。 |
| PF19 | 关闭 | `Reload` 就是 Reset 加 Load（`config_candidate.go:50-53`），计划改为“会刷新 `LoadedConfigBytes`”，与代码一致。`Reload` 只在 `cli/config.go:197` 调用，这个进程不做比较，没有副作用。`ResolveSugaHome` 出错的分支在 `Load` 中已经先失败（`load.go:34-36`），所以快照放在文件旁边的规则实际不会走到，写上也无害。 |
| PF20 | 关闭 | `chatOpts.ToolsExtra` 写在 `tools:` 下面、`allow_write` 之前（`smoke_helpers_test.go:52-55`），按两空格缩进写 `read_roots` 即可；`tools.read_roots` 在注册表里是可写的 StringList（`settings_registry.go:248`），不会被校验拒绝。每次 `startChatOpts` 都会重写 config，所以第二次带 `ToolsExtra` 就是一次“其他方式的改动”；第三次内容相同，字节也相同，因此没有提示。第二次跑一个回合后，`exitAfterTurn` 拿到 session id，再交给 `traceEvents`（:138、:214），可行。终端用例自己写 `config.yaml` 和 `.env`；用 `sugaRun` 时 Stdin 为 nil，`isTerminalInput` 判为假。T6 的 grep 已补上 unset。 |
| import 环 | 无环 | `go list` 显示 `internal/config` 只依赖 `internal/i18n`、`suga/config` 和 `pkg/agentcore/*`，不依赖 personal、wiring、chat。`protected_state.go` 只用 config 包内的符号和 `log/slog`（`load.go:5` 已在用）。chat 和 personal 已经 import config；`personal.Config.ProtectedNotice` 是函数类型，personal 不需要引用 `ProtectedState`。wiring 已同时 import config 和 personal。 |

## 发现

### PF21 [MEDIUM] `ProtectedState` 的会话去重和推进没有写并发保护，ACP 的请求是并发处理的

- disposition: `CORE_CORRECTIVE`
- 证据: T4 写的是“进程级缓存 `map[规范化配置路径]*ProtectedState`（`sync.Mutex`）”，`ProtectedState` 的字段是 `ok bool; recorded map[string]bool`，没有写 `NoticeFor` 和 `Advance` 是否持锁。`acp-go-sdk v0.13.5` 对每个入站请求单开一个 goroutine（`connection.go:412`）。ACP 每个 prompt 都会调用 `wiring.Open`（`cli/acp.go:84`），拿到的是同一个缓存的 `*ProtectedState`。两个会话的 `session/prompt` 同时进 `Runner.Run` 时，会并发写 `recorded`，Go 运行时会报 `concurrent map writes` 并终止进程；同一会话里“检查后再标记”也不是原子的。T4 的 ACP 用例是顺序调用两次 prompt，T4 的命令也没有加 `-race`，所以测不出这个问题。
- 为什么不定 `CORE_BLOCKING`: 计划没有禁止正确实现，在 `NoticeFor` 和 `Advance` 里持 `ProtectedState` 自己的锁就能解决，所以缺的是一句规格。T7 还会跑 `go test -race ./...`，但目前没有会触发并发的用例。
- 影响: 如果按字面实现，ACP 并发会话时可能崩溃，或者记重。
- 建议: T4 写明 `ProtectedState` 带自己的 `sync.Mutex`，`NoticeFor` 的检查和标记、`Advance` 都在锁内完成。补一个用例：多个 goroutine 用不同的 session id 并发调用 `NoticeFor`，加上同一 id 的并发调用，在 `-race` 下运行；或者给 T4 命令加 `-race`。

### PF22 [MEDIUM] T7 的 acpprobe 命令在容器里跑不起来：没有 `~/.suga`，没写构建步骤，`tmp/` 指向不明

- disposition: `CORE_CORRECTIVE`
- 证据:
  1. 计划写“acpprobe（容器内，worktree 根）… `--config-from ~/.suga`”。容器的 `$HOME=/workspace`，下面没有 `.suga`，只有 `.suag/transcripts` 和 `.xhen/suga`。脚本会把 `SUGA_CONFIG` 设成 `~/.suga/config.yaml`（`acp_probe.py:272`），这个文件不存在时，`Load` 返回 `load SUGA_CONFIG` 错误（`load.go:51-53`），ACP 起不来。真实凭据在主机上。
  2. `--bin tmp/suga-hyj7/suga`：计划没有写构建这个二进制的步骤（例如 `go build -o … ./cmd/suga`）。从 worktree 根看，`tmp/suga-hyj7/` 是 worktree 下的 `tmp`；而 T7 文件栏写的“主机脚本放 `tmp/suga-hyj7/`”指的是 WP 根的 `tmp`。两处说法不一致。
  3. “在该 home 上 `suga inspect trace --json`”需要 session id（脚本打印的是 `session {sid}`，在 :280），还要带 `SUGA_HOME=<home>` 的环境。脚本最后只打印审批、工具和任务事件，不打印 `kind=config`（:319-333）。
- 为什么不定 `CORE_BLOCKING`: 这一步只是真实环境的补充证据，去重由 T4 证明；执行失败会直接看到，不会悄悄通过；T7 由计划作者自己执行。
- 建议: 二选一。（a）改成主机 gofer exec job，用主机上的 `~/.suga` 和 Windows 二进制，在 job 里先 `go build`；（b）写明容器里可用的真实配置目录。另外补上构建命令；统一 `tmp/` 是相对 WP 根还是 worktree；写全检查命令，例如 `SUGA_HOME=<home> <bin> inspect trace <sid> --json`，然后数 `kind=config` 的条数。

### PF23 [LOW] T3 的 cli 回归命令里 `TestConfigPath` 匹配为空，锁名回归用例仍不在 T3 的范围内（PF18 残留，同类）

- disposition: `CORE_CORRECTIVE`
- 证据: 仓库里没有 `func TestConfigPath`。锁名 `run/locks/config.lock` 的断言在 `TestConfigSetAllowsSimpleRuntimeKeys`（`internal/cli/config_test.go:163`），不在 T3 的 `-run 'TestConfigCommands|TestConfigPath'` 里，但计划括号里写着这条命令能证明“锁名仍为 `config.lock`”。`config get` 在相对 `SUGA_CONFIG` 下的 source 断言也没有补。T7 的全量测试能兜住锁名回归，所以定为 LOW。
- 建议: 把 `TestConfigPath` 换成 `TestConfigSetAllowsSimpleRuntimeKeys`；或者在 `TestProtectedPathNormalize` 里直接断言默认配置的锁名是 `config.lock`。

### PF24 [LOW] 文案与接线的小缺口

- disposition: `CORE_CORRECTIVE`
- 证据（同类穷尽：本轮新写的文案调用与接线）:
  1. `{keys}` 规定“zh 用‘、’，en 用‘, ’”，但 `i18n.Translator` 只有 `T`（`i18n.go:100-102`），调用方拿不到当前语言。仓库里已有先例，用 catalog 键存分隔符：`approval.ini` 的 `render.agent.separator = 、`（zh/approval.ini:39）。
  2. run 的 stderr 要输出“同样文案”，但文本生成放在 `internal/chat/protected_banner.go`，计划没有写导出给 `internal/cli/run.go` 用。cli 已经 import chat，不会成环。
  3. `ProtectedState.Path` 的含义有两种读法：既用作缓存键（规范化路径，Windows 下转成小写），又用作展示用的 `{path}`。修订记录里写的是“`{path}` 来自 `LoadedConfigBytes`”，即原始绝对路径。需要写明 `Path` 存的是 `LoadedConfigBytes` 的原始绝对路径，缓存键另外求。
  4. T4 的“文件”栏没有列 `internal/config/protected_state.go`（及其测试），只出现在“动作”栏和 Capability 注释里。`runtime.go` 的改动清单也没有列 `:722` 处 `personal.Config{ProtectedNotice: …}` 这一行，以及 Skip 或 state 为 nil 时不设回调（runner 调用前要判 nil）。
- 建议: 加文案键 `status.protected.separator`（zh “、”，en “, ”），或者复用现有分隔符键；导出一个文本函数供 run 使用；写明 `Path` 的语义；补全 T4 的文件栏和 `runtime.go` 改动清单。

## 覆盖缺口与限制

- 没有运行任何测试。并发的结论来自 acp-go-sdk 的源码，以及 ACP 每个 prompt 都调用 `Open` 这一事实；没有实际复现。
- 没有核对 `pkg/sugasdk` 里多个会话是否会并发调用 `NoticeFor`（PF21 的建议对它同样适用）。
- 未改动的章节沿用第 2 轮结论。

## 结论

PASS。PF13–PF20 中，与本轴相关的 PS17、PS22、PS23 已经落到可执行的写法：`cliText` 和 translator 的取法、`status.ini` 的调用方式、`personal.Config.ProtectedNotice` 和 `subagent.go:189`、`rendererOptions` 的传递、`ToolsExtra` 的写法、第二次 chat 跑一个回合、`Reload` 的语义，都与代码一致。`internal/config/protected_state.go` 不会产生 import 环。修订没有引入新的 `CORE_BLOCKING`，与其他章节也没有矛盾。新增 `CORE_CORRECTIVE` 四项：PF21、PF22 为 MEDIUM，PF23、PF24 为 LOW。本轮是唯一一轮纠正确认轮，之后不再开评审轮：建议在实施前对计划做一次文字修订（不另开评审），否则在 Gate 上披露。

## 剩余风险与后续

- PF21 是实施时最值得先补的一项：并发去重不持锁，ACP 多会话并发时可能崩溃。在任务书里写一句话，再加一个 `-race` 用例即可。
- PF22 不改的话，T7 的 acpprobe 一步在容器里一定失败，需要临时改到主机执行；交付说明中的 `UNVERIFIED_REAL_ENV` 披露保持不变。
