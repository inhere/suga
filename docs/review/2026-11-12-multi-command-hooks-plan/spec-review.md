<!-- template_id: review; template_version: 1.2.0 -->
# suga chat hooks 多命令 实施计划 评审（Spec/Executability 轴，第 3 轮：纠正确认轮）

## 评审目标与范围

- 目标: Git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；subject `docs/plans/2026-11-12-multi-command-hooks-plan.md`；candidate commit `147ad4a3b024f523c7e960761b597bfabaabce1c`（Draft 0.3）；上轮 candidate `3ef5f64239958d36404022be9381688565c46105`（Draft 0.2，两轴 PASS）。
- 修订版本: Draft 0.3
- 评审者: 独立评审子 agent（Spec/Executability 轴），未参与 0.3 修订
- 日期: 2026-11-12
- 排除项: 只核对第 2 轮纠正项 HPF13–HPF16 与 HPS1 续、HPS6 续、HPS12–HPS15 的修订，以及这些修订有没有引入新的 CORE_BLOCKING 或矛盾；不重做全量评审；未改动的章节沿用上轮结论；Standards/Governance 轴由另一评审者负责。REPORT_ONLY。

## 输入与方法

- review-contract 与 planning-core-rigorous（payload `tmp/idev-std/d/8Yz1eltnU9GuyZy8zAtfgA/payload.md`）、review 模板 1.2.0。
- 上轮报告 `docs/review/2026-11-12-multi-command-hooks-plan/spec-review.md`、`standards-review.md`。
- `git diff 3ef5f642..147ad4a3 -- <subject>`；`git show 147ad4a3:<subject>` 中 T2、T5、T6、T9、前置检查、回滚各节全文（下文行号均指该版本）。
- 代码核对（当前 main，已含 hyj7 合并 `7905a0f4`）：`internal/cli/config.go`（`writeConfigValue :274`、`formatConfigValue :292`、`runConfigList :131-140`）、`internal/config/settings_registry_readers.go`（`ReadConfigValue`、`effectiveConfigValue` 的 default 分支走 `lookupRawConfig`）、`internal/chat/hooks_record.go`（`StartStop` 转发协程、`recordingWait.Interrupt`）、`internal/chat/stop_background.go`（`start`/`run`/`takeBlocked`）、`internal/chat/chat.go`（Interrupt 调用点）、`pkg/agentcore/tasks/tasks.go`（`Kill`、`TakeNotices` 返回带 ID 的 `Info`）、`internal/hooks/hooks.go`（`stopWait.Interrupt`）、`internal/config/settings_protected_test.go`、`internal/config/providers.go`、`.gofer/tracker/issues.jsonl`、`.gitignore`。
- 实测：`git merge-base --is-ancestor 7905a0f4 main` 退出 0；在 `/tmp` 用 suga 的 goccy/go-yaml v1.19.2（离线模块缓存）运行 `MarshalWithOptions(..., Flow(true))`：`[]any{map{command:[x], timeout_ms:uint64(10000)}}` 得 `"[{command: [x], timeout_ms: 10000}]\n"`，`nil` 得 `"null\n"`、err 为 nil。

## 验收标准

- 第 2 轮各纠正项的建议要落成唯一、可执行的计划文字，并且和代码事实相符。
- 修订不能引入新的执行歧义、矛盾，也不能引入五类 CORE_BLOCKING（BUG/SECURITY/DATA/MISSING_FUNCTION/AUTHORITY）。
- 纠正确认轮：出现 CORE_BLOCKING 就回到 blocking closure；只有 CORE_CORRECTIVE 时结论仍为 PASS，剩下的修订在 Gate 按“未复核修订”披露。

### 上轮纠正项核对

| 项 | 结论 | 依据 |
|---|---|---|
| HPF13 初始态、“有下一条”、共用收尾 | 关闭 | `:192-194`：`last` 初值 -1；`StartStop` 起协程前状态为 `between`；每条都走“`pointBeforeStartNext` → 取锁查 `frozen` → `running(i)`”；“有下一条”含阶段二条件；拦截路径与完成路径共用收尾（置 `finished` → `pointBeforeDone` → 写 `done` 并 close → close `finished`）。逐窗口推演：第一条前、条目间、running 子态、finished 四个窗口下 `Interrupt` 都能在 `finished` 关闭后返回，没有死锁；拦截时 ctx 是否已取消不影响 `Err=context.Canceled` |
| HPF13 第一条启动前用例 | 关闭 | `:203` 断言无 `start`/`Interrupt`、返回空、`Err=Canceled`、`Runs` 为空；与状态机一致 |
| HPF14 / HPS1 续（T5 between 断言） | 关闭 | `:202`：A 为 `block`，`Interrupt` 返回 `A-reason`；`done` 的 `Err` 为 Canceled，对照 `recordingWait.Interrupt`（`hooks_record.go:112-124`，只在 `r.Err == nil && reply == ""` 时补取），确实不会重复；`Runs` 只含 A |
| HPF14 T6 包装下 between | 未关闭，见 HPF18 | `:219` |
| HPF16(1) T6 finished 轮询 | 关闭 | `out` 是缓冲 1 的通道（`hooks_record.go:46`）；转发协程写入后 `close` 不会清掉已缓冲的值，`len(w.(*recordingWait).out)==1` 在包内测试可用、无竞态；之后 `inner.Interrupt` 返回 R1 reason，`reply != ""`，不补取，“恰好一次”成立 |
| HPF16(2)/HPS15 HF13 超时与过滤 | 关闭 | `stop_background.go:38-57` 的 `start` 在持锁时 `Kill` 旧任务；`Kill` 等 `t.done`（≤5s，`tasks.go:658-670`），所以 `b.start` 返回时旧任务已结束。第二个流水线中 B `timeout_ms: 30000`，断言窗口内不会出 R1 通知；`TakeNotices` 返回的 `Info` 带 ID，可以按第一个任务过滤。去掉 R0 做反向验证时，旧任务得 R1（`Err=nil`），`run` 在 Notify 前不查 ctx，用例会如预期失败 |
| HPF16(3)/HPS15 假进程措辞、删 `slow` | 关闭 | `:122` 措辞改为“`mode` 决定 Stop 行为；`timeout`/`ifail` 另作用于……”，`slow` 已删 |
| HPS14 `formatHookEntries` 落点与回显 | 落点与格式关闭；有一个回归，见 HPF17 | `:158` 与代码一致（`PreferredTool` 分支在 `formatConfigValue :305`，`writeConfigValue :274` 只拼两行）；goccy flow 实测输出与 T2 预期回显 `hooks.commands = [{command: [x], timeout_ms: 10000}]` 逐字相同 |
| HPS12 T9 合并前核对 | 方向正确；与计划自身的 tracker 写入冲突，见 HPF19 | `:251` |
| HPS13 凭据与残留 | 四条已写；检查式覆盖不全，见 HPF20 | `:250` |
| HPS6 续 / HPF15 前置判定 | 关闭 | `:113` `git merge-base --is-ancestor 7905a0f4 main` 实测退出 0；`7905a0f4` 是合并提交（父 `3ef5f642`、`5d0685ff`）；`TestProtectedClassification`（`settings_protected_test.go:297`）与 `ConfigKey.Protected` 都在；issue `suga-hyj7` 已关闭；baseline 已同步；回滚节 `:262` 已同步 |

## 发现

按严重级别排序，全部为 `CORE_CORRECTIVE`，没有 `CORE_BLOCKING`。

### HPF18 [MEDIUM] T6 包装下的 between 用例在 `internal/chat` 无法实现（CORE_CORRECTIVE）

- 证据: T6 `:219` 写“between——A `block`、钩子拦在 B 启动前时打断，trace 中没有 B 的 hook 事件”。但能拦在 B 启动前的只有 `pipelineTestHook`，`:120` 与 `:196` 把它限定为 `internal/hooks` 包内**未导出**变量，只在该包的 `_test.go` 中赋值，“不导出”。`internal/chat` 的测试无法给它赋值。用假进程也得不到这个窗口：打断时 B 只要已经启动，状态就是 `running(B)`，B 会收到 Interrupt，`Runs` 里也会有 B，这和 between 语义不同。同类穷尽：T6 引用钩子的只有这一处；T6 的 running、finished、HF13、HF21 用例都只靠日志 `start` 行或通道长度同步，不依赖钩子；T7 不用钩子。
- 影响: 执行者只有三条路：导出钩子（违反 HS10 与 `:207` 的 grep 完成标准）、改用 sleep（违反“均不用 sleep”的同类约定），或者在测试失败后按 fail-closed 停下。不判 `CORE_BLOCKING`：core_acceptance 是设计里“打断时未启动的条目不记”这一项，它已经由 T5 between 用例的 `Runs` 只含 A（`:202`）覆盖，T6 的“两条新条目时 Stop 记两条”（`:218`）也证明了 chat 按 `Runs` 逐条记录。去掉这一例后验收仍然成立，deletion_alternative=PASS，因果链不成立。
- 建议: 删掉 T6 的 between 子项，并在 T6 写明“between 下 trace 不含未启动条目，由 T5 的 `Runs` 断言加上本任务的按 `Runs` 逐条记录用例共同证明”。如果坚持在 chat 层验证，可以在 T6 用一个只返回固定 `Runs`（只含 A）且 `Err=Canceled` 的假 `sugahooks.Client`，断言 trace，这样不需要钩子。

### HPF17 [LOW] `formatHookEntries` 未设值时输出 `null`，`config list`/`config get` 和其他键的 `<unset>` 不一致（CORE_CORRECTIVE）

- 证据: T2 `:158` 规定 `writeConfigValue` 在 `value.Key == "hooks.commands"` 时一律用 `formatHookEntries`，只有 Marshal **失败**才回落 `formatConfigValue`。键未设时，`ReadConfigValue` 经 `effectiveConfigValue` 的 default 分支（`lookupRawConfig`）得到 `Value=nil`；现在 `formatConfigValue(nil)` 返回 `<unset>`（`config.go:293-295`）。实测 goccy 对 nil 返回 `"null\n"`、err 为 nil，不会触发回落，所以输出会变成 `hooks.commands = null`。`config list` 会列出全部注册键（`configListKeys`，`config.go:242-253`），也经过 `writeConfigValue`（`:138`），因此**每个没配新键的用户**执行 `suga config list` 都会看到这一行。同类穷尽：`writeConfigValue` 有三个调用点（get `:60`、set `:98`、list `:138`），计划只写了 get/set。
- 影响: 输出口径不一致（其他未设键都是 `<unset>`）；T2 的用例（只测已设值和 `mcp.servers` 不变）发现不了。不影响 AC1–AC4 的行为语义。
- 建议: `formatHookEntries` 在 `value == nil` 时直接返回 `formatConfigValue(value)`；T2 加一条“未设时 `config get hooks.commands` 输出 `hooks.commands = <unset>`”的用例；“`config get` 与 `config set` 成功回显都经 `writeConfigValue`”补上 `config list`。

### HPF19 [LOW] T9 要求合并前“`status --short` 为空”，但计划自己的 issue 评论会写脏主目录的 tracker（CORE_CORRECTIVE）

- 证据: issue `suga-5xmf` 存在 suga 仓库自己的 `.gofer/tracker/issues.jsonl`（`grep -c suga-5xmf` = 1；该文件历史提交如 `a8a325e0 chore(tracker): close suga-hyj7`），`gofer issue comment` 写的是主目录的这个文件。计划会在主目录写 issue 评论的地方有：T1（`:143-147`）、T9 真实环境结果“记录在 issue 评论”（`:254`，发生在合并**之前**）、IDEV-STD 草稿（`:253`），此外 claim 和状态更新也写这个文件。计划没有写这些 tracker 变化什么时候提交。`:251` 合并前核对要求 `status --short` 为空，否则按 Ownership Conflict 停止。（`.worktrees/` 已在 `.gitignore:1`，不会造成误报。）同类穷尽：计划中会写 suga 主目录的只有 tracker 和 T9 合并两类。
- 影响: 照字面执行时，T9 很可能因为自己没提交的 tracker 改动被误判为 Ownership Conflict 而停下。结果是停止，不会产生错误结果或丢数据，所以不判阻断。
- 建议: T9 合并前核对之前加一步“提交本任务的 tracker 变化（`chore(tracker): …`，只暂存 `.gofer/tracker/*.jsonl` 中本 issue 的改动）”；或者把核对放宽为“只允许 `.gofer/tracker/` 下由本计划产生的改动，先提交再合并”。在前置检查里写明 tracker 提交节奏（按工作区约定随功能点提交）。

### HPF20 [LOW] T9 凭据检查式只覆盖 `api_key:` 字面，不覆盖 `headers`，带引号的引用会误报（CORE_CORRECTIVE）

- 证据: `:250` 规定 provider 段“只能含 `${VAR}` 引用”，校验写成 `Select-String -Pattern 'api_key:\s*[^$\s]'`。provider 还有一个敏感字段 `headers`（`providers.go:223` 用 `expandEnvMap` 展开；注册表 `settings_registry.go:327` 标为 `Sensitive`），`Authorization: Bearer <字面密钥>` 这类值不会被检出。反过来，`api_key: "${VAR}"` 这种带引号的引用会因为 `"` 命中 `[^$\s]` 而被误判为字面值（这一侧只会让流程停下，是安全方向）。同类穷尽：计划里复制真实配置的只有 T9 这一处。
- 影响: 要求（只含引用）和检查式不一致；`headers` 里的字面密钥可能被复制进 `tmp/`。SR1301 秘密面已有“只含引用”的明文规定，执行者照规定复制就不会出问题，检查只是兜底，所以判 LOW。
- 建议: 改为检查 provider 段里 `api_key` 和 `headers` 下每个值，去掉引号后都要匹配 `^\$\{[A-Za-z0-9_]+\}$`；或者只复制 `type`/`base_url`/`models`/`api_key` 几个字段，并且不复制 `headers`（如果需要 headers，同样只允许引用）。

## 覆盖缺口与限制

- 没有执行任何计划任务或测试；goccy 输出是在 `/tmp` 用相同版本离线复现的。
- T9 主机脚本（`realenv.ps1`）尚不存在，只核对了计划文字。
- 未改动章节（T1、T3、T4、T7、T8、追溯表、Gate）沿用第 2 轮结论，没有重读核对。
- 只读到上轮两轴报告；本轮 Standards 轴结论不在本报告范围内。

## 结论

PASS（Spec/Executability 轴，第 3 轮纠正确认轮）：没有开放的 `CORE_BLOCKING`。HPF13、HPF14（T5 部分）、HPF15、HPF16、HPS6 续、HPS14（落点）、HPS15 已关闭；HPS12、HPS13 方向正确。本轮新增 HPF17–HPF20，均为 `CORE_CORRECTIVE`：HPF18 来自 0.3 新增的 T6 between 子项，HPF19 来自 0.3 新增的合并前核对，HPF20 来自 0.3 新增的检查式，HPF17 是 0.3 写定的 `formatHookEntries` 规则暴露出的未设值回归。按 planning-core-rigorous，纠正确认轮仍有 `CORE_CORRECTIVE` 时，之后的修订按“未复核修订”在 Gate 披露。

## 剩余风险与后续

- 修 HPF17–HPF20 时，这些修订在计划 Gate 按“未复核修订”披露（不再开新一轮评审）。
- HPF18 如果不改，T6 执行者大概率会停下或违反 HS10。建议批准前删掉该子项。
- HPF19 如果不改，T9 合并前大概率误停，需要人工判断 tracker 改动的归属。
