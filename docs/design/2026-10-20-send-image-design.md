<!-- template_id: design; template_version: 1.1.1 -->
# send --image 设计（向运行中的会话投递图片）

> 状态：Draft 0.1 / 待用户审批
> 日期：2026-10-20
> 关联：issue `suga-asml`；`docs/design/2026-10-19-image-input-design.md`（图片第一、二期）；`internal/personal/process/process.go`；gofer-r0ke（gofer 命令行形态附图）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-20 | 监督（Claude） | 用户 2026-10-20 按建议排第二十四批：send --image 先出设计。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：`suga send --session <id|短名称> --image <路径>` 把图片随消息投递给正在运行的 chat/run 会话；新旧版本混跑时不静默丢图。
- allowed_scope：`internal/personal/process`（请求字段、进程记录的能力声明）、`internal/cli/send.go`、chat/run 的投递接收端、测试与文档。
- non_goals：跨机器投递；跨 `SUGA_HOME` 投递；改变 run --image / chat :image / ACP 的现有行为。
- expansion_policy：DEFER_OR_REQUEST。
- review budget：低–中。
- 停止条件：用户批准前不改代码。

## 背景与目标

`run --image`（v0.39.0）、chat `:image`/Alt+V（v0.37）、ACP 图片块（v0.38）都已支持；`send` 只能投递文本：`process.Request` 只有 `text/from/mode/wait`（`process.go:50-62`），服务端用 `LimitReader(conn, 1<<20)` 解码（`:301`）。

两个约束决定了方案：

1. **大小**：缩放后单张图最大 3.5MB，base64 后更大，超过 1MB 的请求上限；也不适合把图片字节塞进一条 socket 消息。
2. **版本错配**：旧版本的接收端用 `encoding/json` 解码，**静默忽略**未知字段——如果直接加一个 `images` 字段，旧接收端会收下文字、丢掉图片且不报错。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| 进程记录在 `$SUGA_HOME/run` 下，send 只能投递给同一 SUGA_HOME 的会话 | `process.Record`、`suga send` | 发送端与接收端共享会话目录，可以由发送端把图片写进目标会话的 `images/`。 |
| 图片按内容寻址落盘，只带 `Ref` 的图片部件原样通过存储，上下文组装时按 Ref 取回并校验 sha256 | `store/image_files.go:120-127`、图片第一期“落盘与取回” | 投递只需带引用（Ref、SHA256、MediaType、宽高），不带字节。 |
| `native.ReadImageFile` / `PrepareImage` 是 view_image、:image、run --image 共用的校验与缩放 | `pkg/agentcore/tools/native/view_image.go` | 发送端复用同一套。 |
| 旧接收端静默忽略未知字段 | `json.NewDecoder(...).Decode` | 必须由发送端事先确认接收端支持。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

1. **能力声明**：`process.Record` 增加 `Features []string`（JSON `features`，省略为空）。新版本写入 `["images"]`。旧记录没有这个字段。
2. **发送端**（`suga send --image <路径>`，可重复）：
   - 先解析目标会话的进程记录；记录里没有 `images` 能力 → 报错退出：“目标会话的 suga 版本不支持附图（<版本/PID>），请在新版本里重新打开该会话”，**不发送文字**（避免只到一半）。
   - 用 `native.ReadImageFile` + `PrepareImage` 读取、校验、缩放（相对路径按发送端的工作目录解析），然后按内容寻址写进**目标会话**的 `sessions/<sid>/images/<sha>.<ext>`（复用存储层的原子写入：临时文件 → Sync → rename，同 sha 跳过）。
   - 请求带 `images: [{ref, sha256, media_type, width, height}]`，不带字节；请求体仍在 1MB 上限内。
   - 张数上限与 chat 一致（16）。
3. **接收端**（chat、run 的投递服务端）：
   - 解码 `images`，逐个检查文件存在且 sha256 一致（防止伪造或文件被删），不一致的那张整条消息拒绝并回错误（发送端打印并以非 0 退出）。
   - 通过的图片作为只带 Ref 的 `ImagePart` 附到这条消息（follow-up 进入下一回合的用户消息；steer 进入当前回合）。
   - 当前模型不支持看图时：照第一期规则降级为文字说明，`Response` 增加 `warning`（发送端打印到 stderr，退出码仍为 0）。
4. **旧发送端 → 新接收端**：没有 `images` 字段，行为不变。
5. **gofer**：完成后通知 gofer 开发会话（gofer 的命令行形态可以对运行中的 suga 会话用 `send --image`）。

## 关键流程

见“总体方案”2、3。

## 架构

- `internal/personal/process`：`Record.Features`、`Request.Images`、`Response.Warning`；常量 `FeatureImages`。
- `internal/cli/send.go`：`--image`、能力检查、读取与写入目标会话目录（复用 `internal/personal/store` 的图片写入，不另写一份）。
- 接收端：chat 与 run 的投递处理里把 `Request.Images` 转成 `ImagePart`。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 发送端写入目标会话目录、请求只带引用 | 避开 1MB 请求上限；与已有“Ref 取回并校验”的存储路径一致。 |
| D2 | 进程记录声明能力，发送端先检查 | 旧接收端会静默丢弃未知字段；先检查才能报错而不是丢图。 |
| D3 | 能力不足时整条不发 | 只到文字、图片丢失的半投递比报错更难发现。 |
| D4 | 模型不支持看图时降级并警告，不拒绝 | 与 ACP、chat 的降级规则一致；用户可随后 `:model` 切换。 |

## 安全、数据、运维与回滚

- 发送端只能写同一 SUGA_HOME 下目标会话的 images 目录（路径由会话 ID 推出，校验会话存在）；接收端校验 sha256。
- 回滚：去掉 `Features` 里的 `images`，新发送端即按“不支持”报错。

## 测试与验证

- 单测：能力检查（旧记录无 features → 报错且不投递）；图片写入目标会话目录并按内容去重；接收端 sha 校验失败拒绝；follow-up 与 steer 两种模式下图片进入正确的用户消息；模型不支持看图时降级并带 warning；无 `--image` 时请求逐字节不变。
- 伪终端冒烟：chat 会话运行中，另一个进程 `suga send --session <短名称> --image x.png "看看这张"`，检查假模型收到的用户消息带 `image_url` 部件、`sessions/<sid>/images/` 有文件。
- 反向验证：去掉能力检查后“旧记录”用例失败；去掉 sha 校验后“伪造引用”用例失败。
- 真实验证：主机上一个 chat 会话（ds flash）+ 另一个终端 `send --image`，模型描述图片。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 发送端写入目标会话目录、只传引用（而不是传字节或传路径）？ | 是 |
| Q2 | 接收端版本过旧时整条不发并报错？ | 是 |
| Q3 | 目标会话模型不支持看图时降级并警告（不拒绝）？ | 是 |
| Q4 | 实施前独立审核？ | 否（实施后审核） |

## 结论与人工计划 Gate

用“进程记录声明能力 + 发送端写入目标会话目录 + 请求只带引用”实现 send --image；用户批准 Q1–Q4 后派出实施，批准前不改代码。
