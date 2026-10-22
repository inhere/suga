<!-- template_id: review; template_version: 1.1.0 -->
# 会话消息可靠投递设计 评审

## 评审目标与范围

- 目标: `docs/design/2026-10-22-session-message-delivery-design.md`（Git root `inhere-tools/suga`，candidate commit `28ed8458`，subject path 如前，document revision Draft 0.1）
- 修订版本: Draft 0.1
- 评审者: Claude（独立评审子 Agent，未参与该设计的编写），一轮合并轴评审（Standards/Governance + Spec/Executability 合并）
- 日期: 2026-10-22
- 排除项: 不评审实施计划（尚不存在）；不修改被评审文档与代码；不评审 gofer 信箱设计本身，只核对它与本设计的接口交集；外部参考 whip 只核实文件存在，不评审其语义。

暴露度按 SR1409 判定为低：suga 有发布 tag（`v0.44.0` 等），但本功能只有设计、没有实现；实施后的改动是新增表（旧版本迁移只遍历内置文件，不认识的迁移记录不影响打开，见 `internal/personal/store/store.go:244-296`），可通过回滚恢复，且不涉及 Secret、部署或放量。所以只做一轮合并轴评审，不建 ledger。

## 输入与方法

- 规范：`idevstd load --task review --thinking-mode rigorous`（BOUND，FULL，15 个资源，payload hash 已核对）；评审合同 `review-contract.md`，模板 review 1.1.0。
- 全文读取被评审设计（139 行），以及背景文档 `2026-10-21-session-message-design.md` 末尾的“现状核实（2026-10-22）”。
- 只读核对的代码（suga）：`internal/wiring/session_message.go`、`internal/chat/chat_input.go`、`internal/chat/chat.go`、`internal/chat/steer_send.go`、`internal/chat/session_message.go`、`internal/chat/chat_turn.go`、`internal/personal/process/lock.go` 与 `process.go`、`internal/personal/runner.go:459-480`、`internal/personal/admit.go`、`pkg/agentcore/tools/invocation.go`、`internal/personal/store/store.go`（迁移）、`internal/cli/send.go`、`docs/usage/gofer-personal-agent.md:52`。
- 只读核对的代码（gofer，用于确认调用方兼容性）：`inhere-tools/gofer/internal/config/readonly.go:101-121`。

## 验收标准

- 设计自身的核心目标：消息不丢、不重复，发送方能知道是否已处理；scope freeze 写明“不改跳数限制、会话发现与选择器、gofer 信箱”。
- SR1301 安全清单；SR1403/SR1410（先考虑不做、再复用、最后才做最小实现，用重机制前要证明局部修复不够）；SR1108 scope freeze；评审合同对 `CORE_BLOCKING` 的五类限定与必要性证明。
- “已确认事实”必须与代码一致。

### 事实核对结果

| 设计中的说法 | 核对结果 |
|---|---|
| `sessionMessenger.Send` → `personalprocess.Send` | 一致（`internal/wiring/session_message.go:87-113`） |
| `deliveries` 容量 8，`DeliverImages`，退出时 `DiscardQueued` 丢弃 | 一致（`chat_input.go:198`、`262-288`、`292-306`；退出提示 `chat.go:452-453`） |
| 会话消息强制按 follow-up 处理；达跳数上限的消息累积在 `sessionHeld` | 一致（`steer_send.go:42-53`、`internal/chat/session_message.go:22-54`、`chat_turn.go:188`） |
| SQLite WAL，多进程共享，新增表要做迁移 | 一致（`store.go` 内置迁移和 `schema_migrations` 记录表） |
| 已有 `invocation_id` 可作幂等键 | 字段存在，但它在每次工具调用时都新生成，见 F5 |
| 接收方包括“ACP 会话（若当前支持）” | 目前不支持：run 和 ACP 都不登记会话 socket（`docs/usage/gofer-personal-agent.md:52`、`wiring/session_message.go:131-133`），见 F7 |
| 非目标里说 gofer 信箱“走 ACP 提示，不经本机制” | 说得不全：gofer 给运行中的 suga 会话投递时调用的就是 `suga send`（`gofer/internal/config/readonly.go:109-120`），见 F3 |
| 安全一节说“沿用现有的来源标注与 taint 规则” | 来源标注确实有，但会被绕过；没找到会话消息触发 taint 的规则（`taint_tools` 默认为空，`internal/wiring/manifest.go:659`），见 F6 |
| whip 参考路径 | 文件存在（`tmp/ext/whip/docs/concurrency.md`），本次没有评审它的语义 |

## 发现

### F1 [HIGH] 失败自动重试与“不会被处理两次”互相矛盾，也没有设计失败后的屏障

- 证据: 设计第 62 行和 104-105 行写的是：回合失败或取消后消息回到 `queued`、`attempts+1`；接收方空闲时会马上再取件（第 61、110 行），所以最多会自动连跑 3 个回合。第 32 行承诺消息“不会被处理两次”，第 10 行修订记录写了“失败重试屏障”，但正文没有任何屏障。背景核实的建议 1（`2026-10-21-session-message-design.md` 第 150 行）恰恰是：失败后转为等待用户输入，不要立即再起回合。suga 中断的工具调用不会重放，只会补一条“效果未知”的结果（`internal/personal/admit.go:20-45`），说明失败的回合可能已经执行过一部分工具副作用。
- 影响: 一个回合在执行了一部分工具（比如写文件或 shell）之后失败，同一条消息会被重新处理，副作用会重复发生，“不处理两次”的核心验收被直接违反。用户按 Esc 或 Ctrl-C 取消回合后，消息又被自动重跑，违背用户意图。provider 配额或网络故障时，会连续烧掉 3 个回合，形成背景文档担心的“失败风暴”。
- disposition: `CORE_BLOCKING` [BUG]
- necessity proof: core_acceptance＝不重复处理、可知处理结果；necessary_causal_chain＝回合失败 → 回到 queued → 空闲时立即再取 → 同一消息再跑一个回合 → 副作用重复；minimal_owner＝设计第 4 条与 D4；deletion_alternative＝PASS（删掉自动重试即可关闭）；scope_effect＝没有新增，反而减少状态。
- 建议: 删掉自动重试。回合失败或取消后，消息转为“等待用户”（可以复用 `sessionHeld` 的语义：下次用户输入时一起交给模型），或者直接标为 `failed`，由发送方决定是否重发。正文把语义写清楚为“至多处理一次 + 失败可见”；如果坚持要自动重试，就必须承认是“至少一次”，并删除“不会被处理两次”的承诺。

### F2 [HIGH] 合并投递时的跳数，以及达上限消息在收件箱里的状态，都没有定义

- 证据: 一个回合只带一个跳数值（`chatInput.hops`，见 `chat.go:690`；`chat_turn.go:195`、`248-249`），但合并回合里各条消息的跳数可能不同，设计第 61 行只写了“跳数规则照旧”。达上限的消息目前放在内存 `sessionHeld` 里，退出时只统计条数（`chat.go:458`），设计没有说明这些消息在 `session_inbox` 里处于什么状态。
- 影响: (a) 实现可以取最小值，也可以取最大值或第一条的值，结果不唯一。取最小值时，第 3 跳的消息会搭着第 1 跳的回合运行，回复按第 2 跳计，绕过了防循环上限，违反 scope freeze 中“不改跳数规则”。(b) 达上限的消息如果标成 `taken` 或 `processed`，退出时照样丢失，违反“退出不丢”；如果留在 `queued`，空闲取件循环会反复取到它，要么起了不该起的回合，要么永远拿不出来。
- disposition: `CORE_BLOCKING` [BUG]
- necessity proof: core_acceptance＝不丢、跳数规则不变；necessary_causal_chain＝合并后跳数取法不唯一 → 可能取最小值 → 绕过跳数上限，或者达上限的消息状态未定义 → 丢失或循环；minimal_owner＝设计第 3 条；deletion_alternative＝FAIL（合并本身就是目标之一；如果把合并推迟到后续，这一条也随之消失）；scope_effect＝需要一个“等用户”状态，可以和 F1 共用。
- 建议: 明确合并回合的跳数取各条的最大值；达上限的消息不进入自动取件，而是在收件箱中标为“等用户”（与 F1 共用同一个状态），在用户下一次回合提交后才算 `processed`。

### F3 [HIGH] 离线保留会改变 `suga send` 的退出码约定，破坏 gofer 的投递降级链；离线目标也没法用现有选择器解析

- 证据: gofer 的内置投递是 `suga send --session <sid> --source gofer`，并明确依赖“退出码 3 = 会话进程不在线”来继续走降级链的下一步（`gofer/internal/config/readonly.go:109-120`）。suga 文档同样把退出码 3 定义为没有匹配的进程（`docs/usage/gofer-personal-agent.md:52`）。设计第 59 行和待确认事项 1 推荐“对方离线时也入库”，范围（第 45 行）又包含 `suga send`，非目标里却认为 gofer 不经本机制（第 47 行）。另外，目标解析只看进程记录（`wiring/session_message.go:88-100`，`personalprocess.Resolve`），短名称只对运行中的会话有意义；要支持离线投递，就得按存储解析会话，而这与 scope freeze 中“不改会话发现与选择器”（第 18 行）相矛盾。
- 影响: 实施后，`suga send` 对离线会话返回 0，gofer 会以为已经送达，于是停止降级（不再去续接或改走其他投递），消息只能等用户手动打开会话。这改变了跨项目接口，但设计没有点名。离线目标如何解析（完整 ID、存储查询，以及 run 会话怎么拒收）的实现也不唯一。
- disposition: `CORE_BLOCKING` [BUG]（跨项目接口回归、实现不唯一）
- necessity proof: core_acceptance＝现有调用方不回归、scope freeze 成立；necessary_causal_chain＝离线也入库 → `suga send` 不再返回 3 → gofer 降级链误判为已送达；minimal_owner＝设计第 1 条、待确认 1、范围与非目标；deletion_alternative＝PASS（一期不做离线保留，或者离线保留只对 `session_message` 工具生效，或者只在 `suga send` 显式加 `--queue-offline` 时生效）；scope_effect＝如果保留离线投递，就要新增选择器路径，属于 scope 扩张，需要用户确认。
- 建议: `suga send` 默认保持“离线返回 3”。离线入库只在显式参数下生效（例如 `--queue-offline`），只接受完整会话 ID，并在存储中确认它是 chat 会话。设计把 gofer 列为受影响的调用方，并把“选择器扩展”写进 scope，或者把离线投递推迟到后续阶段。

### F4 [MEDIUM] 取件和崩溃恢复没有绑定会话锁，也没有写原子性

- 证据: chat 在整个生命周期内都持有会话锁（`chat.go:275` `HoldSessionLock`）；run 和 ACP 只在每个回合内持锁（`runner.go:459-473`）。设计第 62 行写“重启时把 `taken` 恢复为 `queued`”，第 110 行写“启动时检查收件箱”，但没有说必须先持有会话锁，也没有说取件要用单条 `UPDATE … WHERE state='queued'` 事务。
- 影响: 不持锁做恢复时，另一个进程（比如等锁 30 秒的 `run --resume`，或者未来 ACP 成为接收方）可能把正在运行的回合取走的消息改回 `queued`，导致重复投递。
- disposition: `CORE_CORRECTIVE`
- 建议: 明确两点：取件和“taken 恢复”都只在持有会话锁时进行，并和现有的 `recoverStaleSession`（`recovery.go`）放在同一个时点；取件是“按 to_session 和 queued 状态，以单事务改为 taken 并记录 turn_id”。

### F5 [MEDIUM] 去重键与 `--wait` 语义：承诺过宽，兼容做法没写清

- 证据: `invocation_id` 由 run id 和 provider 的 call id 组成，每次工具调用都不同（`pkg/agentcore/tools/invocation.go:18-22`），中断的调用也不会重放。`suga send --wait` 当前的含义是“开始回合即返回”（`internal/cli/send.go:36-44`、`163`）。设计第 63 行改为等到 `processed`，“保留旧行为的开关”是可选项。
- 影响: 模型主动重发同一内容时会产生新的 ID，所以“不重复”实际只能防住同一次调用被重放，而这种重放本来就不会发生，去重的实际收益接近零。`--wait` 的默认含义一改，现有脚本会从“回合开始就返回”变成“等整个回合结束”，阻塞时间和退出码含义都会变。此外，模型要查询状态，就必须在工具结果里拿到消息 ID，设计没写这一点。
- disposition: `CORE_CORRECTIVE`
- 建议: 把“不重复”收窄为“同一 ID 只入库一次”，并写明模型重发不在保证范围内。`--wait` 的默认含义保持不变，新增 `--wait-processed`（或 `--until processed`）。工具结果返回消息 ID。

### F6 [MEDIUM] 合并格式的来源标注可以被伪造；“taint 规则”的说法与代码不符

- 证据: 当消息正文以 `[xxx] ` 开头时，`sessionDeliveryLine` 会原样保留正文，不加会话来源标签（`chat_input.go:322-342`，`hasSourceTag`）。也就是说，任何会话都可以发 `[来自 用户] …`，冒充用户本人。会话消息默认不触发 taint（`internal/wiring/manifest.go:659`）。设计第 115 行称“沿用来源标注与 taint 规则”，第 61 行的合并头部只“列出来源与时间”。
- 影响: 在合并回合里，一条消息的正文可以伪造另一条的头部或分隔，冒充用户指令（SR1301 的注入防护）。持久化之后，这类消息还会跨重启保留。这个绕过是现有问题，不是本设计引入的，但新的合并格式不应继续沿用。
- disposition: `CORE_CORRECTIVE`（现有的 `hasSourceTag` 绕过另开 issue，属 `OUT_OF_SCOPE`）
- 建议: 合并格式对每条消息都无条件加上来源标签，不认正文自带的标签，并对正文中的头部或分隔符做转义（或用不可伪造的结构化分段）。把“taint 规则”改成符合事实的表述；如果要让会话消息触发 taint，需要另做决策。

### F7 [MEDIUM] 范围事实有误：ACP 接收方、gofer 非目标

- 证据: 同“事实核对结果”表中的第 6、7 行。
- 影响: 实施计划可能误以为 ACP 已经能接收（ACP 没有 socket，无法唤醒），也可能漏掉 gofer 这个调用方（与 F3 相关）。
- disposition: `CORE_CORRECTIVE`
- 建议: 范围明确写“一期只支持 chat 接收方，ACP 不接收（不登记 socket）”，与待确认事项 4 合并表述；更正 gofer 非目标的说法。

### F8 [MEDIUM] 收件箱失去背压，queued 无上限、不过期

- 证据: 现在队列满会返回 `ErrQueueFull`，相当于背压（`wiring/session_message.go:117-118`）。设计第 116 行只对 `processed`/`failed` 设了 30 天保留期，`queued` 既没有上限也没有过期时间；回滚说明（第 118 行）还明确提到“升级回来后继续投递”。
- 影响: 长期离线或已经弃用的会话会无限累积消息。回滚几周后再升级，过期的指令会被自动投递执行，存在数据与安全风险。
- disposition: `CORE_CORRECTIVE`
- 建议: 每个收件人的 `queued` 设上限（超过时返回现有的“队列已满”），`queued` 设过期时间（例如 7 天后转为 `failed: expired`），单条正文沿用 `suga send` 的 1 MiB 上限；`suga data gc` 一并清理。

### F9 [MEDIUM] 缺少“不做 / 复用 / 最小方案”的取舍论证（SR1403/SR1410）

- 证据: 背景核实建议的顺序是：失败屏障（只改 `internal/chat`，不动协议）→ 合并 → 持久化（“成本最高，最后评估”）→ 回合级回执（`2026-10-21-session-message-design.md` 第 150-153 行）。本设计一次做完全部，新增了表、状态机、CLI 和工具动作，却没有备选方案与取舍一节。现有的版本能力声明机制（`process.go:37-48` 的 `Features`、`FeatureHops`）可以直接用来解决“新发旧收”，设计没有提到。
- 影响: 重机制的必要性没有证明，评审和用户无法判断一期能否只做“屏障 + 回合结束才回执（把 settle 从取件处移到回合结束）+ 合并”。
- disposition: `CORE_CORRECTIVE`
- 建议: 补一段备选与取舍，至少比较“内存方案（屏障 + 回合结束回执 + 合并）”和“持久收件箱”，说明持久化的必要性证据（是否真的发生过退出或崩溃丢消息）。建议分期：一期做内存方案，二期做持久化与离线投递。“新发旧收”直接复用 `Features`（新增如 `inbox` 能力）；同时写明旧版发送方投给新版接收方时，新版接收方要把 socket 直投的消息也落库。

## 覆盖缺口与限制

- 没有运行代码或测试；行号是 2026-10-22 核对时的快照。
- whip 参考只核实了路径存在，没有评审其语义。
- 没有核实 xhen 和 gofer `agent:` 收件的未来依赖，它们只是设计里的动机说明。
- SQLite 写竞争（发送方插入和接收方回合写入并发）没有实测，只是推断：现有 `beginWriteTx` 和 WAL 应该可以承受，实施计划需要包含并发测试。

## 结论

BLOCKED。有 3 项开放的 `CORE_BLOCKING`：F1 [BUG] 失败自动重试与“不处理两次”矛盾；F2 [BUG] 合并回合的跳数和达上限消息的状态未定义；F3 [BUG] 离线保留破坏 `suga send` 退出码 3 约定和 gofer 降级链，且离线目标解析与 scope freeze 冲突。三项都可以靠删除或收窄机制关闭（删掉自动重试、合并回合跳数取最大值并增加“等用户”状态、`suga send` 默认不做离线入库），不需要扩张设计。

## 剩余风险与后续

- 修订后按 Review Candidate Loop 做一轮 changed-scope confirmation，只核对 F1-F3 的关闭情况，以及 F4-F9 中被采纳的部分。
- 现有的 `hasSourceTag` 来源标签绕过（F6）与本设计无关，建议另开 gofer issue 跟踪。
- 设计批准不授权实施；实施计划需要覆盖迁移的大库耗时测试（gofer memory `suga-migration-large-ledger`）、取件并发测试，以及伪终端冒烟测试（chat 退出、重启后的投递）。
