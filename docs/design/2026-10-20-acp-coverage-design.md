<!-- template_id: design; template_version: 1.1.1 -->
# ACP 覆盖补齐设计（session/list、resume、close、set_config_option，load 回放历史）

> 状态：Draft 0.2 / 已批准，可实施
> 日期：2026-10-20
> 关联：issue `suga-1ojw`；`internal/acpserver/server.go`；acp-go-sdk v0.13.5

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-20 | 监督（Claude） | 用户 2026-10-20 “继续下一批”，ACP 补齐先出设计。核对代码后发现 `session/load` 未按协议回放历史，一并纳入。 |
| 0.2 | 2026-10-20 | 监督（Claude） | 用户批准并调整：会话列表（CLI `suga sessions` 与 ACP `session/list`）默认只列当前目录的会话，结果为空时提示目录可能不对、用选项列出全部；新增“会话列表默认当前目录”一节，替代总体方案第 2 条与 Q2。chat `--resume` 目录不一致时的询问与 chdir 已于 v0.25.0 实现（`internal/cli/cwdcheck.go`），不在本设计范围。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：ACP 客户端（gofer 工作台、Zed 等）能列出 suga 会话、无回放地恢复、显式关闭、在会话里切换模型与模式；`session/load` 按协议回放历史。
- allowed_scope：`internal/acpserver`、`internal/cli/sessions.go`（默认目录过滤）、`internal/personal`（只读接口：会话列表、历史条目到消息的投影）、`scripts/acpprobe`、测试与文档。
- non_goals：session/fork、session/delete（删除走 `suga data gc`）、认证、跨进程共享“总是允许”规则。
- expansion_policy：DEFER_OR_REQUEST。
- review budget：低–中；单包改动，协议字段由 SDK 生成类型约束。
- 停止条件：用户批准前不改代码。

## 背景与目标

`internal/acpserver/server.go:560-570` 的 `CloseSession`、`ListSessions`、`ResumeSession`、`SetSessionConfigOption` 都返回 MethodNotFound；`Initialize` 只声明 `LoadSession`。`LoadSession`（`:176-215`）只登记会话状态，不发送任何 `session/update`，而协议要求 load 把历史以通知形式回放给客户端；所以客户端重新打开一个会话时看到的是空白。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| SDK 已有 `ListSessions/ResumeSession/CloseSession/SetSessionConfigOption` 接口与 `SessionCapabilities{List, Resume, Close}` | acp-go-sdk v0.13.5 `types_gen.go:4420`、`agent_gen.go:317-449` | 只需实现并在 initialize 声明。 |
| `ListSessionsRequest{Cursor, Cwd}` → `{Sessions []SessionInfo{SessionId, Cwd, Title, UpdatedAt}, NextCursor}` | `types_gen.go` | 分页与按 cwd 过滤。 |
| `ResumeSessionResponse` / `NewSessionResponse` 可带 `ConfigOptions`（select/boolean，类别 mode/model/thought_level） | `types_gen.go:4684-4686` | 模型切换用 select 类型、类别 model。 |
| 会话列表：`store.ListSessions` | `internal/personal/store/sessions.go:82` | 有 Title、Cwd、UpdatedAt。 |
| 模型切换：`Runner.SwitchModel` | `internal/personal/runner.go:408` | chat `:model` 已在用。 |
| 会话状态只在进程内（`sessionState`，含 always 规则） | `server.go:71-82` | close 释放它；不持久化 always。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

1. **initialize**：`SessionCapabilities{List, Resume, Close}` 声明为支持。
2. **session/list**：按 `updated_at` 倒序，目录过滤见“会话列表默认当前目录”；每页 50 条，`cursor` 为上一页最后一条的 `(updated_at, id)` 编码；只列 ACP、CLI、chat 来源的普通会话，排除 archived 与子 agent 会话。
3. **session/resume**：与 load 相同的检查（目录一致、没有进行中的回合），**不回放历史**；返回 modes 与 configOptions。
4. **session/load 回放**：登记后，把当前分段（压缩摘要 + `first_kept_seq` 之后）的条目按顺序转成 `session/update`：用户消息 → `user_message_chunk`；assistant 正文 → `agent_message_chunk`；工具调用与结果 → `tool_call`（status completed/failed，标题为工具名与参数摘要，结果只给前 2KB 文本）；压缩摘要 → 一条 `agent_thought_chunk`“（之前的对话已压缩：…）”。读取用 `NoHydrate` 列出、只取回当前分段（与上下文组装相同的边界，复用 `loadHistory` 的采用规则，不重复实现）。回放大小上限 2MB，超过时只回放最近部分并在开头说明。
5. **session/close**：若有进行中的回合先取消并等待结束（复用 `Cancel`），然后释放 `sessionState`；会话本身不删除。关闭后的 prompt 返回 invalid params。
6. **session/set_config_option**：在 new/load/resume 的响应里给出两个选项：`model`（select，类别 model，选项为配置里的全部模型，当前值为会话模型）与 `mode`（select，类别 mode，default/read_only，与现有 `SetSessionMode` 同源）。设置 model 时调用 `SwitchModel`（下一回合生效，回合进行中拒绝）；返回最新的全部选项。切到不支持看图的模型时，`promptCapabilities` 不变（initialize 已发出），图片按第一期规则降级。

### 会话列表默认当前目录（0.2，CLI 与 ACP 一致）

用户 2026-10-20：“session list 默认显示当前目录的（cli 也是），避免在不同目录进入一个会话；输出为空时提示是否目录不对或使用选项列出所有会话”。

- **规则**：默认只列 `sessions.cwd` 与当前目录相同（`personal.SameDir`，Windows 不区分大小写）的会话；没有记录目录的旧会话不出现在默认列表里（`--all` 可见）。
- **CLI `suga sessions`**（别名 `ls`）：
  - 默认当前目录；新增 `-a/--all` 列出全部目录；现有 `--cwd <路径>` 保留（按前缀匹配，`.` 为当前目录及其子目录），与 `--all` 互斥。
  - 关键词搜索、`--since/--until`、`--agents` 在目录过滤之后应用，规则不变。
  - **结果为空时**（stderr 一行，`--json` 时 stdout 仍输出 `[]`）：`当前目录 <dir> 没有会话（其它目录共有 N 个）；目录可能不对，可 cd 到会话所在目录，或用 suga sessions --all 列出全部`；N 为 0 时只说“还没有任何会话”。
  - 表格输出的标题行注明范围：`当前目录 <dir> 的会话（--all 列出全部）`。
- **ACP `session/list`**：请求带 `cwd` 时按它过滤（相同目录）；不带时用 suga 进程的工作目录（客户端启动 suga 的目录），与 CLI 默认一致；`_meta` 中 `suga/all: true` 列出全部。响应 `_meta` 带 `suga/otherDirs: N`（其它目录的会话数），客户端可据此提示；结果为空且 N>0 时，`_meta.suga/hint` 给出与 CLI 相同的提示文本。
- **不变**：`chat --resume <id>` 仍可续接任意目录的会话，目录不一致时按 v0.25.0 的三选一询问（切到会话目录并 chdir / 在当前目录继续 / 取消）；`run --resume` 与非终端 chat 仍报错并提示 `cd` 或 `--allow-cwd-change`。

## 关键流程

见“总体方案”1–6。

## 架构

- `internal/acpserver/session_list.go`（list/close/resume）、`internal/acpserver/replay.go`（load 回放）、`internal/acpserver/config_options.go`（set_config_option）；`server.go` 只保留分派。
- `internal/personal`：导出一个只读的“当前分段条目”接口供回放使用（复用 `loadHistory` 的边界计算）。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | load 回放只回放当前分段 | 与模型看到的内容一致；长会话不会发送几十 MB 通知。 |
| D2 | 模型切换走 configOptions 而不是 SDK 不稳定的 set_model | configOptions 是协议的稳定方式，客户端统一渲染。 |
| D3 | close 不删除会话 | 删除是数据治理范围（`suga data gc`），close 只释放进程内状态。 |

## 安全、数据、运维与回滚

- 只读接口；list 不暴露其他目录的会话内容（只有标题与目录）；回放文本限制 2MB。
- 回滚：去掉 initialize 声明即退回现状（方法仍可保留）。

## 测试与验证

- 单测：list 分页/默认当前目录/带 cwd/`suga/all`/排除 archived/`otherDirs` 与空结果提示；CLI `sessions` 默认当前目录、`--all`、`--cwd` 前缀、`--all` 与 `--cwd` 互斥、空结果 stderr 提示且 `--json` 输出 `[]`；resume 不发通知、目录不符拒绝；load 回放的顺序与类型（用户、assistant、工具、摘要）、超限截断说明、压缩点之前不回放；close 取消进行中的回合、关闭后 prompt 被拒；set_config_option 切模型下一回合生效、回合中拒绝、未知值报错。
- 反向验证：去掉回放后“load 后收到历史通知”用例失败；去掉默认目录过滤后 CLI 与 ACP 的 list 用例失败。
- 真实验证：`scripts/acpprobe/acp_probe.py` 扩展 list/load/resume/close/set_config_option；用 gofer 工作台重新打开一个已有 suga 会话，确认历史可见（gofer 侧由其开发会话确认）。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | load 回放只回放当前分段（压缩点之后），而不是全部历史？ | 已定：是 |
| Q2 | list 默认只列同目录？ | **已定：默认当前目录（CLI 与 ACP），空结果提示，选项列出全部**——见“会话列表默认当前目录” |
| Q3 | configOptions 暴露 model 与 mode 两项，暂不暴露 thinking 级别？ | 已定：是 |
| Q4 | 实施前独立审核？ | 已定：否（实施后审核） |

## 结论与人工计划 Gate

补齐四个方法并修正 load 回放；用户已于 2026-10-20 批准（Q2 调整为默认当前目录），派出实施。
