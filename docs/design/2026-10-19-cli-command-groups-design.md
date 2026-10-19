<!-- template_id: design; template_version: 1.1.1 -->
# CLI 命令分组设计

> 状态：Draft 0.1 / 待用户审批
> 日期：2026-10-19
> 关联 issue：`suga-azgs`

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-19 | 监督（Claude） | 依据用户 2026-10-19 要求（常用命令留在顶层，同组或不常用的移到二级命令）与现有 18 个顶层命令、gofer 调用点、仓库内引用统计，提出 `inspect`/`data` 两个分组、旧写法的参数改写兼容与 `gc` 多开关拆分。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：`suga --help` 的顶层只剩日常用的命令；会话诊断与数据维护各自成组；所有旧写法在过渡期内照常可用，并提示新写法；外部集成（gofer、Zed/ACP）零改动。
- allowed_scope：`internal/cli`（命令树、参数改写、帮助）、`cmd/suga` 入口、chat 里引用命令名的提示文字、README / docs/usage / skills 引用、`scripts/ptysmoke` 与测试里的命令行、CHANGELOG。
- non_goals：不改任何命令的行为、参数名与输出格式；不升级 goutil（v0.8.3 的 capp 已支持 `Cmd.Add` 子命令与 `Aliases`）；不改 gofer。
- expansion_policy：DEFER_OR_REQUEST。若需要修改 capp 本身（例如隐藏命令），停止并回到 Gate。
- review budget：小到中等；主要是搬家与兼容，风险在“漏掉某个调用点”。
- 停止条件：用户批准前不改代码。

## 背景与目标

现有 18 个顶层命令：`run chat acp init config models version ps send trace sessions stats manifest gc backup restore undo export`。第二十批又给 `gc` 加了 `--retention --shrink-fts --status --abort --batch --vacuum --compress-spill` 七个开关，一个命令承担了五种不同的维护动作，`suga gc --help` 很难读。

目标：

1. 顶层只放日常命令；
2. 会话诊断（看 trace、统计、导出、manifest）成一组；
3. 数据维护（清理、瘦身、VACUUM、压缩、备份、恢复）成一组，并把 `gc` 的多个开关拆成各自的子命令；
4. 旧写法在过渡期内继续可用，执行时在 stderr 提示新写法；
5. gofer 与 ACP 客户端不用改。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| capp v0.8.3 支持 `(*Cmd).Add` 子命令与 `Aliases`，分组命令不能声明位置参数 | `gookit/goutil` `cflag/capp/cmd.go:144-181`、`:300` | 分组命令只做容器；不需要升级依赖。 |
| capp 不支持隐藏命令 | 同上，无 Hidden 字段 | 旧写法不能以“隐藏子命令”保留，改为入口处改写参数。 |
| gofer 调用 suga 的只有 `acp`、`run …`、`chat`（交互）、`send --session … --source gofer`、`--version` | gofer `internal/agent/templates.go:150-175`、`internal/config/readonly.go:74` | 这几个必须留在顶层且参数不变。 |
| 仓库内引用：`send` 121 处、`trace` 76、`gc` 63、`manifest` 38、`config` 33、`stats` 20、`ps` 19、`sessions`/`export`/`init` 各 16、`undo` 14、`models` 11 | 2026-10-19 统计（不含 CHANGELOG 与历史设计/研究文档） | 迁移时要逐一更新 README、docs/usage、skills、ptysmoke、chat 提示文字。 |
| chat 的提示文字里直接写了 `suga trace <sid>`、`suga gc` | `internal/chat/render.go:49`、`layout.go:149`、`chat_undo.go:269`、`chat.go:144` | 提示改成新写法。 |

## 范围与非目标

范围：命令树重组、参数改写兼容层、帮助与文档、引用更新、测试。非目标：命令行为、参数名、输出格式的任何变化；gofer 改动；capp 改动。

## 总体方案

### 分组后的命令树

| 位置 | 命令 | 说明 |
|---|---|---|
| 顶层（日常） | `chat`、`run`、`send`、`ps`、`sessions`、`undo`、`config`、`models`、`init`、`version` | 不变 |
| 顶层（外部集成） | `acp` | gofer、Zed 按 `suga acp` 调用，不变 |
| `suga inspect …`（会话诊断） | `trace`、`stats`、`export`、`manifest` | 原顶层同名命令搬入，参数不变 |
| `suga data …`（数据维护） | `gc`（`--older-than` / `--retention` + `--apply`）、`shrink-fts`（`--status` / `--abort` / `--batch`）、`vacuum`、`compress-spill`（`--apply`）、`backup`、`restore` | 原 `gc` 的四个维护开关拆成独立子命令；`backup`/`restore` 搬入 |

顶层从 18 个降到 11 个。

## 关键流程

### 旧写法兼容（参数改写）

在 `cmd/suga` 把参数交给 capp 之前，按下表改写 `os.Args`，并在 stderr 打印一行：`提示：suga <旧写法> 已改为 suga <新写法>，旧写法将在 v0.38 移除`。只改写第一个非全局选项的参数，后续参数原样保留。

| 旧写法 | 新写法 |
|---|---|
| `suga trace …` / `stats …` / `export …` / `manifest …` | `suga inspect trace …` 等 |
| `suga gc --shrink-fts [--status\|--abort\|--batch N]` | `suga data shrink-fts [同样参数]` |
| `suga gc --vacuum` | `suga data vacuum` |
| `suga gc --compress-spill [--apply]` | `suga data compress-spill [--apply]` |
| `suga gc …`（其余） | `suga data gc …` |
| `suga backup …` / `suga restore …` | `suga data backup …` / `suga data restore …` |

- 改写放在独立、可测的函数里（输入参数切片，输出新切片与提示文字），表驱动测试覆盖每一行。
- 提示只打到 stderr，stdout 内容不变，脚本解析不受影响；`--output-format stream-json` 时同样只走 stderr。
- 过渡期：两个小版本（v0.36、v0.37）；v0.38 删除改写表，旧写法得到“未知命令”与新写法提示。

### 帮助

- `suga --help` 只列顶层 11 个与两个分组；分组的一行说明列出其子命令名（如 `inspect  会话诊断：trace / stats / export / manifest`）。
- `suga data --help`、`suga inspect --help` 用 capp 的分组帮助。

## 架构

- `internal/cli/root.go`：顶层 `Add` 11 个命令 + `newInspectCommand()`、`newDataCommand()` 两个分组。
- 新文件 `internal/cli/legacy_args.go`：改写表与 `rewriteLegacyArgs(args []string) (out []string, notice string)`。
- `internal/cli/gc.go` 拆为 `data_gc.go`（gc）、`data_maintenance.go`（shrink-fts、vacuum、compress-spill），复用现有 `runShrinkFTS`、`runGCVacuum`、`runGCCompressSpill`、`runGCRetention`，不改实现。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 分组名用 `inspect` 与 `data` | 动词式、短；`session` 易与顶层 `sessions` 混淆。 |
| D2 | `sessions`、`undo`、`ps` 留在顶层 | 日常使用频繁（`undo` 在 chat 外也常用）。 |
| D3 | 旧写法用入口参数改写兼容，而不是重复注册隐藏命令 | capp 无隐藏命令；改写集中在一处，到期一次删除。 |
| D4 | `gc` 的维护开关拆成子命令 | 一个命令五种动作、开关互斥规则复杂；拆开后每个子命令的帮助只讲一件事。 |
| D5 | 过渡期两个小版本 | 给脚本与文档留迁移时间；gofer 不受影响。 |

## 安全、数据、运维与回滚

- 纯命令层改动，不碰数据与配置；回滚即还原命令树与删除改写表。
- 风险是漏改调用点：用 `grep -rn "suga \(trace\|stats\|export\|manifest\|gc\|backup\|restore\)"` 清单逐一处理，测试断言 chat 提示文字为新写法。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 分组名 `inspect` / `data` 是否合适？（备选：`debug`/`dev`、`db`/`maint`） | `inspect`、`data` |
| Q2 | `trace` 使用频繁（76 处引用），是否保留在顶层而不进 `inspect`？ | 进 `inspect`，但 `suga trace` 的兼容改写过渡期内不提示（静默），到 v0.38 再决定是否永久保留为别名 |
| Q3 | `gc` 是否拆分维护开关为独立子命令？ | 拆（D4） |
| Q4 | 旧写法过渡期几个版本？ | 两个小版本，v0.38 删除 |
| Q5 | `backup`/`restore` 进 `data` 还是留顶层？ | 进 `data`（低频，且属于数据维护） |

## 结论与人工计划 Gate

本设计把顶层从 18 个命令减到 11 个，新增 `inspect`、`data` 两个分组，旧写法经入口参数改写兼容两个小版本，gofer 与 ACP 不受影响。用户批准 Q1–Q5 后实施（派出实现，监督审核）；批准前不改代码。
