<!-- template_id: design; template_version: 1.2.0 -->
# 外部来源确认：本会话允许该片段 设计

> 状态：Draft 0.1 / 待人工计划批准；issue `suga-ilko`

- 思考模式：RIGOROUS；核心目标：chat 里对外部来源询问答一次“本会话允许该片段”，之后带同一片段的其他调用不再问这一问。
- scope freeze：`originGuard` 询问选项、会话内已允许片段集合、审批事件 attr、问句文案；其余 deferred。
- 暴露度：Full（放宽外部来源防护，属安全边界，SR1409）；review budget：两轴评审封顶两轮；停止条件：无开放 `CORE_BLOCKING` 或到达两轮交用户决定。

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-11-28 | Claude | 初稿 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 背景与目标

模型把外部结果里的一段命令前缀（实例：`sshc run <host> -- 'pct exec <id> -- bash -lc`）拼到多条不同命令前，每条都触发外部来源询问，只能逐条答 y。`suga-7s4v` 已让登记为可信的记忆后端不再触发；本项覆盖其余来源：网页、MCP、未登记可信的记忆后端等。

用户可见结果：外部来源询问多一个选项 `[a]本会话允许该片段`；答 a 后，本会话后续调用若只带这段已允许的片段，不再弹外部来源询问，但审批规则照常：deny 规则仍拒绝，普通 ask 仍询问，裁决 deny 仍拒绝；run 与 ACP 行为不变；trace 能看出是哪次放行。

## 名词

- **外部来源询问**：`originGuard.Decide` 命中后由 `confirm` 发出的 chat 问题。
- **复制段**：参数中与某外部结果逐字相同的连续文本，以其 40 字节窗口（`OriginWindow`）的哈希表示。
- **已允许片段**：用户答 a 后记住的复制段，键为 (会话, 来源工具, 窗口哈希集合)。

## 范围与非目标

范围（受影响项目：仅 suga）：`pkg/agentcore/tools/origin.go`（已允许集合与查找）、`internal/wiring/origin_guard.go`（选项与跳过）、`pkg/agentcore/tools/approval.go` 与 `internal/personal/observe.go`（attr）、`internal/i18n/catalog/{zh,en}/host.ini`（问句）。

非目标：不持久化；不改裁决、初筛窗口与审批规则；不加“本会话总是拒绝该片段”；不提供查看或撤销命令（Q3）；不改 run、ACP、子 agent。

## 已确认事实与规范

- `internal/wiring/origin_guard.go:115-120`：`Find` 命中后取 `hit.Snippet` 组成询问理由；`:156-158`：无 chat 时直接拒绝；`:160-165`：chat 答 y 后以 `originConfirmedKey` 调内层审批器，内层普通 ask 不再重复问。
- `internal/wiring/origin_guard.go:209-215`：只有 `ReplyAllow` 放行，其余一律拒绝；不记忆。
- `internal/wiring/origin_guard.go:123-154`：裁决在询问之前；allow 交内层，deny 拒绝，ask 才问人；`:71-74` 写明裁决不缓存，防止恶意调用搭上次的 allow。
- `pkg/agentcore/tools/origin.go:130-158`：`find` 只返回第一处命中；`Snippet` 为 `originSnippet(norm[i:])`，即参数中命中位置起 48 字节（`:165`），含复制段之后的非复制字节。上例前缀为 44 字节，不同命令的 `Snippet` 末尾不同。
- `pkg/agentcore/tools/origin.go:52-54`：`index` 把窗口哈希映射到最新持有它的来源，`names` 给出来源工具名。
- `internal/wiring/runtime.go:632`：`ExternalOrigin` 每个运行时在内存新建，不随会话持久化；`internal/wiring/subagent.go:199-200`：子 agent 有独立实例且 chat 为 nil。
- `pkg/chatui/approval.go:21-25`：`a`/`always` 解析为 `ReplyAllowAlways`；`:116-118`：普通审批问句已用“[a]本会话总是允许…”。`internal/wiring/chat_approver.go:230-245`：普通审批答 a 时追加内存中的会话规则（`SourceSession`），按工具与目标匹配，不能表达片段，且位于 `originGuard` 内层，不可直接复用为片段记忆。
- `internal/i18n/catalog/zh/host.ini:18`、`en/host.ini:19`：现问句“输入 y 允许，其它任意输入拒绝”。
- `internal/personal/observe.go:379,387`：审批事件已有 `approval_answered`、`origin_judge` 等 attr。
- 规范：GR108（装配只装配）、SR1409。

## 总体方案

1. 问句改为 `执行 {tool}？[y]是 [N]否 [a]本会话允许该片段：`（en 同义）；解析沿用 `chatreply.ParseApprovalReply`，`ReplyAllowAlways` 即“允许该片段”，`ReplyDenyAlways` 与其他输入仍为拒绝。
2. `ExternalOrigin` 增会话内已允许集合：`find` 收集参数中所有命中窗口（不再只取第一处），每个窗口带来源工具名；返回首个未被允许的命中，全部已允许时返回带 `Remembered` 标记的命中。
3. 答 a：本次按 y 处理（放行、`originConfirmedKey` 照旧），并把本次全部命中窗口按 (会话, 来源工具) 记入已允许集合。
4. 后续调用在裁决 allow/ask/error 之后、问人之前检查：命中全部已允许则不问，以不带 `originConfirmedKey` 的 ctx 调内层审批器，规则与普通 ask 照常。
5. trace：`ApprovalReport` 增 `OriginRemembered string`，事件 attr `origin_remembered`：答 a 的那次为 `added`，被跳过询问的为 `hit`；`danger` attr 照旧记录。

## 架构

片段匹配与集合放在 `pkg/agentcore/tools`（与窗口哈希同处，wiring 不碰哈希）；`originGuard` 只决定问不问、答 a 后调用记入；不新增配置键。分层与现状一致（GR108）。

## 关键流程

```text
write/exec 调用 → find（全部命中，标已允许）
  ├─ 无命中 → 内层审批器
  └─ 有命中 → 裁决（若启用）
        ├─ allow → 内层；deny → 拒绝（已允许也拒绝）
        └─ ask / error / 无裁决
              ├─ 无 chat（run、ACP、子 agent）→ 拒绝（不变）
              ├─ 全部已允许 → 内层（不带 originConfirmedKey），attr origin_remembered=hit
              └─ 否则问人：y → 放行；a → 放行并记入，attr origin_remembered=added；其他 → 拒绝
```

## 安全、数据、运维与回滚

- 风险：攻击者控制的页面在后续结果中重复同一段文本，或诱导模型用已允许前缀拼接恶意尾部。缓解：只在会话内存且须用户主动答 a；键含来源工具，换来源（如另一网页工具结果持有同一窗口）重新询问；同一调用里出现任何未允许的复制段仍询问；裁决 deny、规则 deny、普通 ask 均不被跳过；trace 记录 `added`/`hit` 可追查。残余：已允许前缀后接模型自写的恶意尾部不会被外部来源防护拦（尾部不是复制段，现状同样不拦），只剩审批规则与裁决。
- 数据：无持久化、无迁移。回滚：revert 代码即可，无残留；`tools.ask_on_external_origin: false` 行为不变。

## 决策

- **D1 作用域**：只存进程内存、随会话结束消失，不写 session meta。理由：`ExternalOrigin` 本身就在内存（`runtime.go:632`），resume 后来源文本不在、防护不会对旧片段触发，持久化的允许无可匹配；重新读取同一来源后若沿用旧允许，等于静默放宽一次新的读取。
- **D2 “同一片段”**：键用复制段的窗口哈希，不用 `hit.Snippet`（含非复制尾部，不同命令不同，见事实 4），也不用来源全文（会放行该页任意片段）。
- **D3 全部命中**：跳过询问须参数里所有命中窗口都已允许；只看首个命中会让“已允许前缀 + 新复制段”蒙混。
- **D4 裁决**：已允许不跳过裁决，也不覆盖裁决 deny；只替代“问人”这一步（与 `origin_guard.go:71-74` 不缓存裁决一致）。
- **D5 无询问入口**：run、ACP、子 agent 仍拒绝，不变。
- **D6 选项与解析**：复用 chat 已有的 `a` = “本会话总是允许…”写法与 `ParseApprovalReply`，不新增字母与解析器；不复用 `chatApprover` 会话规则（按工具匹配，表达不了片段）。
- **D7 内层 ask**：答 a 只记住片段，不同时给工具装会话规则；后续调用内层普通 ask 照常询问。

## 待确认事项

- **Q1 选项字母**：A 复用 `a`（推荐，与普通审批一致，无解析改动）；B 新增 `s`（区分“允许工具”与“允许片段”，需扩展解析）。
- **Q2 来源工具是否入键**：A 入键（推荐，换来源重问）；B 不入键（同一文本任何来源都放行，更少询问）。
- **Q3 查看与撤销**：A 不做，重开 chat 即清空（推荐）；B 加 `/origin` 列出与清除已允许片段。

## 验证要求

- 单元（`ExternalOrigin`）：答 a 记入后同会话同来源同片段不同尾部判为已允许；另一会话、另一来源工具、另一复制段均不判；“已允许片段 + 新复制段”返回未允许命中；被淘汰来源的窗口不影响已允许判定的正确性。
- guard：chat 假 asker 答 a 后第二条不同命令不再 Ask，attr 为 `hit`；内层 deny 规则仍拒绝；内层普通 ask 仍 Ask；裁决 deny 仍拒绝；chat 为 nil 仍拒绝；答 d 或空为拒绝且不记入。
- 端到端：`test/ptye2e` 在 `fakellm` origin 模式上加场景：页面含 40 字节以上前缀，模型依次跑两条带该前缀的不同命令；第一条询问，答 a；第二条屏幕不出现外部来源询问，`suga inspect trace --json` 中两次审批事件 `origin_remembered` 分别为 `added`、`hit`。
- 反向验证（确认能编译，失败不是 build failed）：改为只看首个命中、键去掉来源工具、已允许时跳过裁决、改用 `hit.Snippet` 作键，分别确认对应测试失败。

## 结论与人工计划 Gate

本设计为 Draft，待两轴评审与用户对 Q1–Q3 的选择；批准后才进入实施计划，批准不授权实施。
