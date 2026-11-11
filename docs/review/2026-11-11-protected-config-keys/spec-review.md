<!-- template_id: review; template_version: 1.2.0 -->
# suga 安全配置键保护设计 R2 评审：Spec/Executability 轴

## 评审目标与范围

- 目标：`docs/design/2026-11-11-protected-config-keys-design.md` 的可执行性、是否有歧义、事实是否准确
- 候选四元组：git root `/d/work/inhere/my-tools-dev/inhere-tools/suga`；commit `a08457674817632068a03ef69fcbb8e502ac5bbb`；subject 为上述设计；revision Draft 0.3（上轮 `50950889` / 0.2）
- 修订版本：Draft 0.3
- 评审者：spec 轴独立子 agent，第 2 轮（confirmation），REPORT_ONLY
- 日期：2026-11-11
- 排除项：用户已选定的 Q1–Q6（D3–D8）本身不评；Standards/Governance 轴另行评审；不改仓库、不提交

## 输入与方法

- 规范：payload `tmp/idev-std/d/3Z3cOanwUM2ILAXHbxiTlQ/payload.md`，delta `.../bXLJzNcvvw_ZrjUevizE3A/delta-OfGu6IpGnHz92hejmPjb9g.md`（review-contract、review-candidate-loop、planning-core-rigorous）；仓库 `AGENTS.md`。
- 上轮本轴报告 `docs/review/2026-11-11-protected-config-keys/spec-review.md`（F1–F7）；设计 0.3 全文（`git show a0845767:<subject>`）与 `git diff 50950889..a0845767`。代码从 `a0845767` 到 HEAD `520fd9a7` 在 `internal/`、`pkg/` 下没有变化，以下行号都按工作树。
- 读过的代码：
  - `internal/config/settings_registry.go`：全部静态描述符与 4 张动态描述符表，第 88–345 行；
  - `settings_registry_source.go:95-120`（directEnvOverride）、`load.go:33-67`、`paths.go`、`yaml_edit.go:78-175`（锁与原子写）、`sessionmessage.go`、`providers.go:171-200,601-660`、`snapshots.go:95-118`、`approval_rules.go:80-110`；
  - `internal/cli/config.go`（全文）、`root.go:55-80`、`cwdcheck.go:195-215`、`chat.go:60-110`、`acp.go:60-100`、`run.go`（output-format）；`cmd/suga/main.go:25-70`；
  - `internal/wiring/runtime.go`（Open/OpenCLI 的调用方）、`tools_disabled.go`、`sessionenv.go`；`internal/personal/observe.go:95-120,536-556`、`internal/personal/store/cachebucket.go:205-330`、`internal/personal/backup/backup.go`（备份与恢复路径）；
  - `internal/output/streamjson.go:25-40`、`internal/chat/layout.go:76-100`、`internal/acpserver/server.go:189-210`、`pkg/sugasdk/engine.go:140-160`、`session.go:80-100`、`types.go:76-83`；
  - `pkg/agentcore/tools/rules/rules.go:277-310`、`native/local_command.go:118-128`、`internal/proctree/proctree_unix.go:22-27`；
  - 现有测试：`internal/cli/config_test.go`、`approval_flags_test.go`，以及在 `_test.go` 里 grep `"config","set|unset"` 的全部结果；xhen 与 gofer 中对 `SUGA_CONFIG`、`NoSessionEnv` 的 grep。
- 方法：
  - 逐项核对 F1–F7 是否关闭；
  - 把清单与注册表逐键对照；
  - 把 D3/D7/D8 的每一步落到具体代码位置上；
  - 对每一类问题，列出候选中的全部出现点。

## 验收标准

- 核心目标见设计第 34 行：模型不能用 `config set/unset` 放宽检查、扩大读写范围、引入外部可执行程序或改变请求去向；用其他方式改了这些配置，下次启动能被你看到。
- review-contract：只有五类可以标 CORE_BLOCKING，并且要给 necessity proof；同类问题要穷尽。
- SR1301 安全核对清单；GR107–GR110；AGENTS.md 验证规范（GR110、ptye2e、反向验证、Windows 主机测试）。

## F1–F7 关闭核对

| 上轮 | 结果 | 设计位置 | 代码证据 |
|---|---|---|---|
| F1 漏 `workspace`、端点类 | 已关闭 | 第 95–97、91、124、173 行 | `workspace` 在 settings_registry.go:114；provider 的 `type`/`base_url` 在 :311-312；模型级 `type`/`base_url` 在 :327-328；`models.judge` 在 :105 |
| F2 “绕过 1 仍拒绝”不成立 | 已关闭 | 第 109、151 行，D7 | proctree_unix.go:25 只设了 Setpgid（Windows 同类绕过见 F15） |
| F3 `!` 等场景误拒 | 已关闭 | 第 155、72–73 行 | local_command.go:124 的 shellEnv |
| F4 防漏标测试 | 已关闭 | 第 101–102 行 | 覆盖原则还有缺口，见 F12 |
| F5 拒绝细节 | 部分关闭 | 第 106–107、156 行 | 退出码、i18n、stdin、共用路径都已写明；会受影响的现有测试只列了两个文件，见 F13 |
| F6 待定键分类 | 部分关闭 | 第 95–99 行 | 上轮建议保护的键里，只有 `tools.disabled` 没列入，而且理由不成立，见 F11 |
| F7 其他写入口 | 已关闭 | 第 67、82、151 行 | 第 82 行的引用路径写错，见 F14 |

带 `*` 的项都能展开成具体键，展开后都存在：
- `tools.approval_rules.*`：4 个（:287-290）；
- `tools.shell_limits.*`：4 个（:238-241）；
- `tasks.max_*`：3 个，`max_running`、`max_duration`、`max_output_bytes`（:180-182）；
- `tools.session_message.*`：3 个（:203-207）；
- `tools.origin_judge.*`：2 个（:230-231，目前不可写，D5 改为可写）；
- `tools.read_deny*`：2 个（:252-253）；
- `memory.*.backend`：2 个（:263、:267）。

## 发现

### F8 [HIGH] 人工 `config set` 后“刷新快照”刷新哪些键没有写定；如果按整体重算，会把之前的篡改一起吸收

- 级别与处置：HIGH，`CORE_BLOCKING`，类别 `[BUG]`（实现方式不唯一，其中一种会让核心验收失败）。
- 证据：
  - 第 108 行写“人在终端里修改 protected 键成功后同时刷新配置快照”；
  - 第 138 行流程写“写入 → 刷新快照”；
  - 第 175 行 D8 写“人工 set 刷新快照”。
  - 三处都没说是只更新被改的那个键，还是用当前文件把全部键重算一遍。最省事的实现是复用启动时的“计算全部摘要 → 保存”。
- 必要性证明：
  - core_acceptance：第 34 行“用其他方式改动这些配置时，下次启动能被你看到”，以及第 54 行。
  - necessary_causal_chain：
    1. 模型用 shell_exec 直接改 `config.yaml` 的 `hooks.command`；
    2. 下次启动之前，你在终端里执行 `suga config set tools.read_roots …`；
    3. 实现按整体重算刷新快照，`hooks.command` 的新摘要被写进快照；
    4. 下次启动时比较结果相同，不提示，hook 照常执行。
    5. 验证要求（第 163 行“人工 set 后不提示”）两种实现都能通过，测试发现不了这个问题。
  - minimal_owner：设计 §2 第 108 行、§3、第 138 行流程，各加一句：“只更新被改键（unset 即删除该键条目）的摘要；其他键保留原摘要；没有快照时不新建”。计划里再加一条测试：先篡改另一个键，再人工 set，下次启动仍然报出被篡改的键。
  - deletion_alternative：PASS。整个删掉“人工 set 刷新快照”也能关闭这个问题，代价是你自己的修改会被提示一次。
  - scope_effect：不新增 Module、Interface、Schema 或 lifecycle，只把一个已有步骤的语义写确定。

### F9 [HIGH] 启动提示挂在哪个装配点、一次提示由谁消费、什么时候推进快照，都不确定，存在提示丢失的路径

- 级别与处置：HIGH，`CORE_CORRECTIVE`。
- 证据：
  - **没有统一的进程启动点。**
    - `appconfig.Load()` 是所有命令都会调用的（cmd/suga/main.go:38、52），包括 `config get`、`inspect` 等，不能挂在这里。
    - 设计选的是 `internal/wiring` 装配（第 127 行），但它的调用方很多：
      - `wiring.OpenCLI` 由 run（run.go:187）、chat 和 `suga session compact`（compact.go:60）调用；
      - `wiring.Open` 由 ACP 调用，ACP 是在每个 prompt 或会话创建时打开 runtime（acp.go:73-86，注释写明 session create/load 时没有 turn）；
      - SDK 每个 session 调一次 `wiring.Open`（pkg/sugasdk/session.go:98）。
    - 设计没有写明哪些调用方参与比较，例如 compact 是否参与。
  - **装配时还没有会话，trace 写不进去。** trace 事件必须带 session ID，否则直接丢弃（observe.go:107-108 “not inside a turn; nothing to attribute the event to”）。ACP 先 `wiring.Open`，再 `NewSessionFor`（acpserver/server.go:189-198）；SDK 也是先 Open。如果在装配时“比较 → 写 trace → 更新快照”（第 141–144 行流程），ACP 和 SDK 的这条事件会被静默丢弃，快照却已经推进，这处改动就哪里都看不到了。
  - **run 默认的 text 输出没有提示面。** run 的默认输出是 `text`（run.go:267），设计只给 stream-json 写了提示（第 116 行）。
  - **自动入口会先把提示消费掉。** 提示只出一次（第 118 行）。gofer 并发 job、xhen 内嵌会话、ACP 会话，乃至模型自己再起一个 `suga run`，只要先启动就会消费这次提示，并把它写进没人看的 job 日志或 trace，你下次 chat 的横幅就什么都不显示。第 54 行写的用户可见结果实际上取决于哪个入口先启动。
- 建议：
  1. 写明参与比较的入口：chat、run（text 和 stream-json 都算）、ACP、SDK；compact 等维护命令不参与。
  2. 写明“送达之后才推进快照”：chat 是横幅已经渲染；run 是 session 行已经写出、text 模式写到 stderr；ACP 和 SDK 是第一个会话建立之后，带 session ID 的 trace 事件已经写入。没有送达就不更新快照。
  3. 在“风险”一节写明：自动入口会先消费提示；模型自己再起一个 suga 也能吞掉提示，这和改写快照属于同一类已接受的风险。
  4. 是否改成“chat 横幅一直显示，直到交互会话看过为止”，属于 Q6 范围内的取舍，请作者交给用户决定，本轴不替用户选。

### F10 [MEDIUM] 快照存放位置、按配置文件区分、并发读写都没有写定

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`。
- 证据：
  - **存放位置。** SUGA_HOME 下没有叫“状态目录”的目录（第 60、114、158 行），现有的是 `db/`、`sessions/`、`run/`（进程记录与 `run/locks`）、`transcripts/`。
    - `data restore` 会整体替换 `db/sessions.db`、`config.yaml`、`memory/user`、`projects`（backup/backup.go:432）。
    - 如果快照放在 `db/sessions.db` 或任何会被恢复的路径里，模型构造的归档就能同时带一份和配置相符的快照，恢复之后不提示。这与第 67 行“由启动提示兜底”冲突。
    - SUGA_HOME 整体是文件工具的保护路径（rules.go:291），所以放在 home 下的独立文件就能得到同等保护。
  - **按配置文件区分。** 只放在 SUGA_HOME 而不区分 `SUGA_CONFIG`，两份配置交替使用时会反复提示，或者互相吞掉提示。现有的配置锁已经按 `sha256(path)` 区分文件（yaml_edit.go:102-127），可以照用。
  - **并发读写。** “读、比较、写”三步没有锁，也没有原子替换，并发启动时可能重复提示，或者读到写了一半的文件。快照读失败或已损坏时，如果当作“首次运行”静默重建，变化就丢了（第 118–119 行只写了写入失败的情况）。
- 建议：
  1. 写明具体路径，例如 `run/protected-config[-<sha256(config path)>].json`，并写明它不在备份和恢复的范围内。
  2. 比较和更新在 `run/locks` 下持锁进行（沿用 `acquireBootstrapLock`），用临时文件加改名的方式原子写入。
  3. 读失败或损坏时记 Warn，并把所有键当作变化提示，不要静默重建。

### F11 [MEDIUM] `tools.disabled` 不列入的理由与代码不符（上轮 F6 未完全关闭）

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`。
- 证据：
  - 第 99 行的理由是“只能关闭工具”。
  - 实际上，unset 这个键或者把它改成更短的列表，就会重新启用你禁用的工具，其中也包括 MCP 的 `server/tool` 名（tools_disabled.go:13-20、:35-38）。
  - 这属于“放宽之后会话”的能力范围。
- 建议：二选一。
  - 把它列为 protected；
  - 或者把理由改成能成立的说法，例如“重新启用的工具仍要走审批；能执行 `suga config` 的会话已经有 shell”，同时写明 MCP 工具的情况。

### F12 [MEDIUM] 资源上限类的划分原则没写，“其余显示与性能类键”这个兜底分类盖不住剩下的键

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`（同类穷尽）。
- 证据：
  - 设计把 `tools.shell_timeout_max`、`tools.task_timeout_max`、`tasks.max_*`、`tools.shell_limits.*` 列为 protected。
  - 但下面这些同属上限类的键没有列入，也没有给理由：
    - `budget.tokens`、`budget.cost_usd`：无人值守 gofer job 的成本上限；
    - `max_steps`；
    - `agents.max_steps`、`agents.max_tokens`、`agents.timeout`；
    - `hooks.timeout_ms`；
    - `mcp.call_timeout_sec`。
  - 另有一批键既不属于显示类，也不属于性能类：
    - `tools.snapshots.enabled`、`tools.snapshots.on_error`：从 `deny` 改成 `continue` 就关掉了“写前快照失败即拒绝”的保护（snapshots.go:107-117）；
    - `transcripts`；
    - `gc.retention.*`：调短会提前删除会话和 trace；
    - `network.proxy.sites`；
    - `run.deadline_env`；
    - `tools.loop_guard`。
  - 计划阶段的显式分类表要给每个键写理由（第 102 行），现在的设计给不出可以照着判定的规则。
- 建议：
  - 在设计里补一句可判定的原则，例如“只保护限制子进程执行的上限；成本、步数、显示、保留期不保护，理由……”；
  - 并把上面这些键逐个点名，写明 protected 或者不保护的理由。

### F13 [MEDIUM] 判定细节和验证要求还有缺口，计划写不出确定的测试

- 级别与处置：MEDIUM，`CORE_CORRECTIVE`。
- 证据：
  - **“环境中有 `SUGA_SESSION_ID`”的含义没定。** 第 106、170 行没说是“变量存在”还是“值非空”。
    - Go 的 `t.Setenv` 不能删除变量。
    - 实施者本身就是 suga，在 shell_exec 里跑 `go test` 时环境带着 `SUGA_SESSION_ID`（sessionenv.go:17），“终端可写”这类正向用例在 agent 里跑必然失败。
    - 应定为“值非空”，并写明测试要显式置空。
  - **正向路径不可测。**
    - `isTerminalInput` 要求 stdin 是 `*os.File`（cwdcheck.go:212-215）。
    - GR110 测试注入的是 `strings.NewReader("")`，永远不是终端，只能测出拒绝。
    - 要么提供一个可注入的终端判定函数，要么用 pty 文件，设计里要写明。
  - **现有测试会被新规则拒绝，但设计没列出。** 第 156 行只列了 origin_judge 和 read_deny 两个测试文件，实际还有：
    - `internal/cli/config_test.go:132-160`：在非终端 stdin 下 set `tools.allow_write`、`tools.approval`、`workspace`、`memory.write`，并断言成功；
    - `config_test.go:175-204`：`tools.auto_allow` 的取值解析与报错；
    - `approval_flags_test.go:133`：`tools.approval trust`。
    - 拒绝发生在取值解析之前（第 106 行），所以 protected 键的取值校验测试必须借助上面说的终端判定 seam 才能保留。
  - **验证要求缺几项。**
    - 缺 ACP 和 SDK 的 trace 事件测试（可以沿用 `pkg/sugasdk/trace_events_test.go`）；
    - 缺 run text 模式；
    - 缺 SUGA_CONFIG 区分与并发用例（F10）；
    - 缺 F8 的“篡改后再人工 set 仍报出”用例；
    - Windows 的正向用例应写成 `test/ptye2e` 在 ConPTY 下跑（`make smoke-go`），用真实二进制执行 `config set` 成功、经管道执行被拒，而不是第 164 行这种笼统写法；
    - CLI 拒绝的测试也应做一次反向验证。

### F14 [LOW] 事实引用写错

- 级别与处置：LOW，`CORE_CORRECTIVE`。
- 证据与正确位置：
  - 第 78 行 `internal/chat/local_command.go:122-127`：这个文件不存在，实际是 `pkg/agentcore/tools/native/local_command.go:122-127`；
  - 第 82 行 `internal/cli/backup.go:152,432`：这个文件不存在，实际是 `internal/personal/backup/backup.go:152,432`；
  - 第 83、156 行 `docs/usage/gofer-personal-agent.md:124`：只对应 origin_judge，read_deny 在 `:125`。
- 建议：改成上面的正确位置。

### F15 [LOW] 绕过清单只写了 Unix 的做法

- 级别与处置：LOW，`CORE_CORRECTIVE`（同类穷尽）。
- 证据：
  - 第 109、151 行列出的 `</dev/tty`、`script`、pty 都是 Unix 写法。
  - Windows 上 shell_exec 的子进程会继承 chat 的控制台，`< CON` 或打开 `CONIN$` 就是同一类绕过。
  - 如果判定是“变量存在”，`SUGA_SESSION_ID=`（空值）也是同类。
- 建议：这些都属于 D7 已经接受的风险，在风险一节补一句即可，不需要新机制。

### SR1301 核对

- 鉴权：已核对。D3 的判定为什么只挡直接调用，已在 D7 写明。
- 注入防护：已核对。键名是精确匹配（settings_registry.go:371-375），不拼接命令。
- 秘密：已核对。快照只存摘要，提示只给键名，`api_key` 和 `headers` 仍是 Sensitive。注意：如果快照按生效值计算，`${VAR}` 展开后的摘要也不会泄露原值，可以接受。
- 入参校验：已核对，沿用现有校验。
- 资源归属：不适用。
- 补充：摘要应该按原始 YAML 值算，还是按 `${VAR}` 展开后的生效值算？
  - 当前 directEnvOverride（settings_registry_source.go:95-120）不覆盖任何 protected 键，env 只能经 `gkconfig.ParseEnv` 展开 `${VAR}` 来影响它们（load.go:48）。
  - 按生效值算：每个 gofer job 的环境不同，就会误报。
  - 按原始值算：改 SUGA_HOME/.env 就能间接改掉 base_url，而且不会被提示。
  - 两种都可以，但设计要选定一种并写进风险。
  - 同时要写明：
    - 新增或删除 provider/model 条目算作变化；
    - YAML、映射、列表类型的值要先规范化再算摘要，避免只是重排就误报；
    - 你按拒绝提示的建议手工编辑 `config.yaml` 后，下次也会收到“如果不是你改的”提示。
  - 这一条并入 F10 的修订范围，归为 MEDIUM `CORE_CORRECTIVE`，不另外编号。

## 覆盖缺口与限制

- 没有实际运行任何测试或 ptye2e；Windows 下 `term.IsTerminal` 的行为是按 x/term 的 GetConsoleMode 实现推断的：Windows Terminal/conhost 为 true，mintty 管道和 NUL 为 false。
- 没有逐个读 gofer 的 runner 环境，以确认是否设置了 `SUGA_CONFIG`；在 gofer 的 Go 代码里没有 grep 到。
- 未变章节沿用上轮结论。本轮是增量复核，但清单和 D8 新增内容是全量对照代码检查的。

## 结论

**BLOCKED。**
- 存在 1 个 CORE_BLOCKING：F8，`[BUG]`，“刷新快照”的范围不唯一，其中整体重算的实现会吸收此前的篡改，违反第 34 行的核心验收。补一句“只更新被改的键”加一条测试就能关闭。
- F9–F15 都是 CORE_CORRECTIVE，计划批准之前应当修正。

## 剩余风险与后续

- 关闭 F8 后进入第 3 轮，只复核 changed/dependent scope，也就是 §2、§3、关键流程和验证要求。
- F9 中“提示由谁消费”的取舍（自动入口抢先消费提示）需要作者交给用户确认，属于 Q6 范围内的细化。
- D7 已接受的风险不变：刻意绕过 CLI 判定、改写或删除快照、模型自起 suga 吞掉提示。
