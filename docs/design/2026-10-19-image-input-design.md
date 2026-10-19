<!-- template_id: design; template_version: 1.1.1 -->
# 图片输入设计（按路径读图 + ACP 图片内容块）

> 状态：Draft 0.3 / 已批准，按独立审核修订后可实施（“0.3 修订”优先于后文）
> 日期：2026-10-19
> 关联 issue：`suga-wfcw`；gofer 设计 `inhere-tools/gofer/docs/design/2026-10-19-workbench-image-attachments-design.md`（已批准、实施中）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-19 | 监督（Claude） | 用户 2026-10-19：“suga 能不能读本地图片——gofer 那边正在加上传图片支持”“图片的也设计下”。gofer 交图有两条路：① ACP 声明 `promptCapabilities.image` 时发 image 内容块；② 其余情况把图片存到项目 `tmp/gofer-attachments/<日期>/<id>.<ext>`，消息末尾加“[附图 N] <绝对路径>”。本设计分两期：第一期按路径读图（对应 ②，chat 也能用），第二期 ACP 图片内容块（对应 ①）。 |
| 0.2 | 2026-10-19 | 监督（Claude） | 用户批准（“图片设计按推荐来”）：Q1–Q7 按当前倾向。 |
| 0.3 | 2026-10-19 | 监督（Claude） | 按独立审核（结论“修改后可实施”）修订：补 P2 去重结构的 Images、打通 ToolResult/RunInput/RunRequest 的图片通道、落盘改在 AppendMessages、取回改在上下文组装、OpenAI 追加消息位置、Responses 核实与纯图消息、裁剪对齐 fold 边界并加单请求上限、图片 token 估算、view_image 可读会话图片、vision 三态与回退模型、ACP 声明策略、安全上限；按用户要求加入 chat Alt+V 贴图（第一期）。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 0.3 修订：独立审核后的定稿要点（本节优先于后文）

### 阻断项的修法

1. **P2 去重会丢图片**：`store.EncodeMessage` 的去重分支用手工列字段的 `dedupedMessage`/`dedupedToolResult`（`internal/personal/store/message_json.go:33-54`），没有 `Images`。补上 `Images`（字段顺序与 api 声明一致）；`TestEncodeMessageKeepsMessageFields`（`message_json_test.go:107`）给 Images 赋值，使新增字段漏写时测试失败。tool-bodies 的 `tools.jsonl` 存的就是 `EncodeMessage` 输出，修好后图片引用随正文保留；桩不含 Images，所以凡是要读 Images 的地方（孤儿图片检查、备份清单）都必须经 `ListEntries` 取回，不用 SQL `json_extract`。
2. **图片的通道**：`api.ToolResult` 增加 `Images`，`persistToolTranscript`（`pkg/agentcore/runtime.go:621-626`）把它带进 tool 消息；`RunInput`（`runtime.go:125` 由字符串 Instruction 生成用户消息）与 `personal.RunRequest`（`internal/personal/runner.go:239`）增加 `Images`，用户附图由此进入。

### 落盘与取回的位置（替代 0.2 的“宿主注入保存函数”）

- 工具与入口只在内存里带字节（`Data`）。**落盘统一在 `AgentStorage.AppendMessages`**（`internal/personal/store/agentstore.go:85`，调用 `EncodeMessage` 之前）：写 `sessions/<sid>/images/<sha256>.<ext>`（临时文件 → Sync → rename，同 sha 已存在则跳过），填 `Ref`/`SHA256`，清空 `Data`。用户图片同样走这里。
- `view_image` 不标 `Prefetchable`（避免并发预取产生孤儿文件，`pkg/agentcore/tools/prefetch.go:41`）。
- **取回在上下文组装**：`contextBuilder.BuildMessages`/`compose`（`internal/personal/context.go:302`、`:342`）返回消息副本并按 Ref 填 `Data`，按 sha 缓存；各处 clone 函数复制 `Images`。
- `sha256` 定义为发送字节的 sha；图片无需缩放且格式被 provider 支持时原样发送。

### provider 映射修正

- **OpenAI Chat**：追加的 user 消息插在 `groupToolBatches`（`pkg/agentcore/llm/tool_batch.go:13`）分组之后、整组 tool 消息之后，不逐条插；单调用按对插入。
- **Responses**：实施时先按当前官方文档核实 `function_call_output` 是否支持 `input_image` 数组；加 compat 开关——官方端用原生方式，网关端回退为追加 user 消息。`buildInput` 对空文本 user 消息直接跳过（`responses.go:201`），纯图消息会丢，必须改。（2026-10-19 核实：官方 OpenAPI 的 `FunctionCallOutputItemParam.output` 是字符串或 `input_text`/`input_image`/`input_file` 数组，原生支持；实现为官方端把图片放进 `function_call_output` 的 `output` 数组，网关端仍追加 user 消息，由 compat 适配器 `openai`（`api.openai.com`）打开 `Compat.ToolOutputImages`。）
- **Claude**：图片块放在文字之前；总像素按当前文档建议控制（约 1.15MP 内效果最佳）。
- 所有追加消息在组装时确定性生成，回放稳定。

### 历史裁剪与成本（替代 0.2 的“最近 3 回合”）

- 按回合推进裁剪会在每张图过期那一轮改写请求中段，前缀缓存随之失效。改为**复用 fold 的分批边界**（`internal/personal/fold.go:64` 的 every/keep），或只在分段切换时裁剪（那时缓存本来就失效）。配置 `images.history_turns` 保留，含义改为“按 fold 批次保留”。
- 另加**单请求上限**：图片张数（默认 8）与 base64 总字节（默认 20MB，低于 Claude 单请求约 32MB 的上限）；超出时最早的图换成文字说明。
- **token 估算**计入图片：`tokensOfMessage`（`internal/personal/tokens.go:70`）按宽高估算（宽×高/750 的量级，按 provider 文档校准），压缩阈值与预算预估（`estimate.go:156`）随之准确。
- 被省略的图片提示“需要时 `view_image <Ref>`”：`view_image` 接受本会话图片的 `Ref`（`images/<sha>.<ext>`），否则会话目录不在工作区（`readroots.go:110`）读不到。

### vision 能力与回退

- 配置 `vision` 用三态（参照 `StreamRequiredSet`，`internal/config/providers.go:51`）：未设 = 按内置名单（按 `APIModel` 匹配），可显式 true/false 覆盖（网关不支持图片时设 false）；在 `settings_registry.go` 注册。
- 回退由 `llm.NewPool` 组合（`internal/wiring/runtime.go:702-718`），上下文组装时不知道最终用哪个 provider：**图片换成文字说明放在各 provider 的 vision 选项里做**。
- 工具表：`SwitchModel`（`runner.go:383`）只换 provider，但 runtime 每步都 `ListTools`（`runtime.go:359`），在 `observedTools`（`observe.go:253`）或新包装层按当前模型过滤 `view_image`。提示文案用 chat 实际命令 `:model`（`chat_commands.go:105`）。

### ACP（第二期）修正

- acp-go-sdk v0.13.5 有 `PromptCapabilities.Image`（`types_gen.go:3785`）与 `ContentBlock.Image{Data,MimeType,Uri}`（`:1195`、`:1271`）。
- `initialize` 在会话之前、没有 runtime，无法“随主模型声明”：改为**只要配置里有任一支持看图的模型就声明**，prompt 时按当前模型降级为文字说明。
- `promptText` 改为返回（文本, 图片）；ACP 图片走与 `view_image` 相同的校验与缩放。

### 安全上限（替代 0.2 的 10000×10000）

- 解码前读文件头尺寸，**总像素 ≤ 40MP**；并发解码数受限；先 `Stat`、只接受普通文件、`LimitReader` 读取（输入上限 20MB）；处理 JPEG EXIF 方向；动画 WebP 拒绝；GIF 只取首帧。

### 备份与清理

- 备份目前把整个 `sessions/` 当 spill 默认排除（`internal/personal/backup/backup.go:148-162`）：`images/` 要单独收集并在 restore 安装，`OmittedBytes` 扣除。
- `handoff.go:92`、`runner.go:483` 删会话不删目录：与 tool-bodies 一起处理（删除会话时清理会话目录或由检查命令报告孤儿）。

### chat 贴图 Alt+V（用户 2026-10-19 追加，第一期）

- 终端粘贴只传文字，suga 直接读系统剪贴板图片：Windows 用剪贴板 API 读 PNG / CF_DIBV5 / CF_DIB（`golang.org/x/sys/windows`，已是依赖）；macOS 用 `osascript` 取 «class PNGf»；Linux 先 `wl-paste --type image/png`，再 `xclip -selection clipboard -t image/png -o`，都没有时提示安装哪个。
- 绑定 **Alt+V**（Ctrl+V 被终端自己拦截用于粘贴文字，suga 收不到）；有图 → 与 `:image` 相同流程附到下一条消息，输入行上方显示“[已附图 N：宽×高 格式]”；无图 → 提示“剪贴板里没有图片”；模型不支持看图 → 拒绝并说明。
- 读剪贴板做成可替换的接口；伪终端冒烟用假剪贴板测按键流程（含反向验证）；真实剪贴板由用户在本机手测（Windows 截图后 Alt+V）。

## 思考模式与范围冻结

- thinking_mode：RIGOROUS。
- core_objective：模型能看到用户给的图片（本地路径或 ACP 内容块）；图片不进数据库；不支持看图的模型得到明确提示；现有纯文本会话的请求字节与存储格式不变。
- allowed_scope：`pkg/agentcore/api`（消息里的图片部件）、`pkg/agentcore/llm` 三个 provider 的映射、`pkg/agentcore/tools/native`（新工具 `view_image`）、`internal/personal`（图片落盘、请求前取回、历史图片裁剪）、`internal/config`（模型的看图能力）、`internal/acpserver`（第二期）、`internal/chat`（`:image` 命令，第一期）、备份、测试与文档。
- non_goals：图片生成、视频、音频、OCR、图片编辑；把图片写进数据库；改变纯文本消息的 JSON 编码。
- expansion_policy：DEFER_OR_REQUEST。需要新增 provider 或改审批语义时回到 Gate。
- review budget：中到大；涉及 api 层、三个 provider 与存储，实施前做一次独立审核。
- 停止条件：用户批准前不改代码。

## 背景与目标

现状（2026-10-19 核对）：`api.Message.Content` 只有字符串（`pkg/agentcore/api/types.go:42-52`）；三个 provider（`llm/claude.go:227`、`openai.go:158`、`responses.go:177`）都没有图片映射；ACP 的 `promptText`（`internal/acpserver/server.go:429`）遇到 text、resource_link 以外的内容块直接报 `unsupported content block`，`initialize` 也没声明 `promptCapabilities.image`（`:130`）；`file_read` 把图片当二进制，只报告不读取；模型配置里没有“能否看图”的声明。

目标：

1. **第一期**：新增只读工具 `view_image(path)`，按路径读本地图片交给模型；chat 新增 `:image <路径>` 把图片附到下一条消息。gofer 的“[附图 N] 路径”由模型调用 `view_image` 读取。
2. **第二期**：ACP `initialize` 声明 `promptCapabilities.image`，`session/prompt` 的 image 内容块按顺序变成用户消息里的图片。
3. 图片存到会话目录的文件里，数据库只存引用；请求前按引用读回。
4. 不支持看图的模型：工具与 ACP 都给出明确说明，不发图片字节。

## 已确认事实与约束

| 事实 | 位置/证据 | 约束 |
|---|---|---|
| `api.Message` 只有文本 `Content`；P2 已给它加了 `UnmarshalJSON`（还原去重） | `pkg/agentcore/api/types.go:42`、`message_json.go` | 新字段必须 `omitempty`，纯文本消息编码逐字节不变（前缀缓存哈希 `internal/personal/prefix.go` 依赖它）。 |
| `pkg/agentcore` 不得依赖 `internal/*` | `pkg/agentcore/layering_test.go` | 图片“按引用读回字节”由宿主（internal/personal）在请求前完成，或通过注入的读取函数；llm 包只认已带字节的图片部件。 |
| Claude 的 tool_result 内容可以包含 image 块；OpenAI Chat 的 tool 消息只能是文本；Responses 的 function_call_output 只能是文本 | provider 文档（实施时以当前 API 再核对） | OpenAI Chat 与 Responses 需要“在工具结果之后追加一条带图片的 user 消息”的变通。 |
| gofer 的附件放在执行机项目目录 `tmp/gofer-attachments/…`，即 suga 的工作区内 | gofer 设计 | `view_image` 的路径规则沿用文件工具（工作区、只读依赖根、`--add-dir`），不需要额外放行。 |
| 当前常用模型 deepseek-v4.1-flash、glm-5.3-flash 是否看图未知；gpt-6-sol、Claude 系列支持 | 配置与 provider 文档 | 需要按模型声明能力，默认按已知名单推断，可在配置里覆盖。 |

## 范围与非目标

见“思考模式与范围冻结”。

## 总体方案

### 消息里的图片部件（api）

- `api.ImagePart{MediaType string; Ref string; SHA256 string; Width, Height int; Data []byte \`json:"-"\`}`。`Ref` 是相对会话目录的路径（`images/<sha256>.<ext>`）；`Data` 只在发请求前由宿主填充，**从不写进存储**。
- `api.Message` 增加 `Images []ImagePart \`json:",omitempty"\``。用户消息带图 = 用户附图；tool 消息带图 = 该工具结果里的图（`view_image` 的结果）。
- 纯文本消息不出现 `Images` 键，编码不变。

### 图片落盘与取回（internal/personal）

- 落盘：`sessions/<sid>/images/<sha256>.<ext>`（按内容去重，同一张图只存一份），写临时文件 → Sync → rename。
- 取回：每次组装请求时，宿主对投影进上下文的消息里的图片，按 `Ref` 读文件填充 `Data`；文件缺失或 sha256 不符时，把该图替换成一段文字“[图片缺失：<Ref>]”，不让请求失败。
- 历史图片裁剪：只保留最近 `images.history_turns` 个回合（默认 3）里的图片字节，更早的图片在请求里换成文字“[图片：<文件名> <宽>×<高>，已省略；需要时再 view_image]”，控制 token 成本；存储里的引用不变。
- 压缩/交接摘要：只保留图片的文字描述与路径，不保留图片。
- 备份：`sessions/*/images/` 与 `tools.jsonl` 一样**始终**包含；GC 删会话时随目录删除。

### 读图工具 `view_image`（第一期）

- 参数：`path`（必填，规则同 `file_read`：工作区、只读依赖根、附加目录）、`max_side`（可选，默认 1568 像素）。
- 识别：按文件头识别 PNG、JPEG、GIF（取第一帧）、WebP（`golang.org/x/image/webp`，只解码）；其它类型拒绝并说明支持的格式。
- 处理：长边超过 `max_side` 时等比缩小（`golang.org/x/image/draw`），重新编码为 PNG（有透明）或 JPEG（质量 85）；单张编码后上限 `images.max_bytes`（默认 3.5MB，按 provider 单图上限取较小者）。原文件不改。
- 结果：文本说明（文件名、原始与发送尺寸、格式、字节数）+ 一张图片部件；元数据 `ReadOnly`、`CategoryRead`。
- 当前模型不支持看图时：不注册该工具（工具表随模型切换重新计算时）；若该机制不支持按模型增减工具，则工具保留但返回明确错误“当前模型 <x> 不支持看图，可 /model 切换到支持看图的模型”。实施时先核对工具表能否随 `/model` 刷新。

### 模型的看图能力（internal/config）

- 模型配置新增 `vision: true|false`；未配置时按内置名单推断（Claude 3 及以后、gpt-4o/4.1/5/6 系列、gemini 等为 true；其它为 false），`suga models` 显示该能力。
- 运行时只看当前主模型；回退模型（fallbacks）不支持看图时，回退请求里的图片换成文字说明。

### provider 映射（pkg/agentcore/llm）

| provider | 用户消息里的图 | 工具结果里的图 |
|---|---|---|
| claude | `content` 数组加 `{"type":"image","source":{"type":"base64","media_type":…,"data":…}}` | `tool_result.content` 数组里加同样的 image 块 |
| openai（Chat Completions） | `content` 数组加 `{"type":"image_url","image_url":{"url":"data:<mt>;base64,…"}}` | tool 消息只放文本；在这一批工具结果之后追加一条 `role:user` 消息，内容是“以下是工具 <name>（<call id>）返回的图片”+ image_url 块 |
| responses | `input` 里 user 消息加 `{"type":"input_image","image_url":"data:…"}` | `function_call_output` 只放文本；之后追加一条带 `input_image` 的 user 消息 |

- 追加的那条 user 消息只在请求组装时生成，不写进存储，回放时同样生成，请求字节稳定。

### chat：`:image <路径>`（第一期）

- 把图片落盘并附到**下一条**用户消息；可多次，顺序保留；`:image` 无参数时列出待发送的图片；当前模型不支持看图时直接拒绝并说明。
- 交互行为需要伪终端冒烟（`make smoke-chat` 新场景 + 反向验证）。

### ACP 图片内容块（第二期）

- `initialize`：主模型支持看图时声明 `promptCapabilities.image = true`；不支持时不声明（gofer 会自动改走“附图路径”方案）。
- `session/prompt`：text 与 image 块按顺序处理，image 块（base64 + mimeType）落盘为图片部件，附到这一条用户消息；文本块之间保留顺序（图片在消息里的位置用占位文字“[图片 N]”标注）。
- 会话中途切到不支持看图的模型：图片仍落盘，请求里换成文字说明。

## 关键流程

见“图片落盘与取回”“读图工具”“provider 映射”“ACP 图片内容块”。

## 架构

- `pkg/agentcore/api`：`ImagePart`、`Message.Images`。
- `pkg/agentcore/llm/{claude,openai,responses}.go`：映射与“追加 user 消息”变通。
- `pkg/agentcore/tools/native/view_image.go`：读图、识别、缩放、编码；落盘交给宿主注入的保存函数（保持分层）。
- `internal/personal/images.go`：落盘、请求前取回、历史裁剪、缺失替换。
- `internal/config`：`vision` 声明与推断、`images.history_turns`、`images.max_bytes`。
- `internal/chat`：`:image`；`internal/acpserver`：第二期。

## 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 图片存会话目录文件，数据库只存引用 | 刚完成的数据治理不能被 base64 打回原形。 |
| D2 | 新字段 `omitempty` | 纯文本会话的编码与前缀缓存不变。 |
| D3 | 用 `view_image` 工具而非让 `file_read` 识别图片 | 工具名直观、模型更容易用对（实测命名工具的使用率远高于可选参数）；`file_read` 的文本语义不变。 |
| D4 | 工具结果里的图片在 OpenAI Chat / Responses 上用“追加 user 消息”变通 | 这两种接口的工具结果只能是文本。 |
| D5 | 历史图片只保留最近 3 个回合的字节 | 控制成本；需要时模型可再次 `view_image`。 |
| D6 | 分两期：先按路径读图，再 ACP 内容块 | 第一期就能覆盖 gofer 的附图路径方案与 chat；第二期改 ACP 协议面，单独验证。 |

## 安全、数据、运维与回滚

- `view_image` 只读，路径规则与 `file_read` 相同；缩放与编码在内存中完成，单张输入文件上限 20MB（超过拒绝），解码前先读文件头里的尺寸，超过 10000×10000 拒绝（防解压炸弹）。
- 回滚：去掉 `view_image` 注册与 ACP 声明即可；已存的图片文件无害；旧版本读到带 `Images` 的消息会忽略该字段（只看到文字）。

## 测试与验证

- api：纯文本消息编码逐字节不变；带图消息往返（`Data` 不进 JSON）。
- provider：三家的请求体快照（用户图、工具结果图、追加 user 消息的位置与内容），历史裁剪后的文字替换。
- `view_image`：四种格式、缩放边界、超大文件与尺寸拒绝、损坏文件、路径越界拒绝、模型不支持看图时的行为。
- 落盘与取回：去重、缺失与 sha256 不符时的文字替换、备份包含 images、GC 随会话删除。
- chat `:image`：伪终端冒烟（附图、多张、模型不支持时拒绝）+ 反向验证。
- ACP（第二期）：`initialize` 的声明随主模型变化、`session/prompt` 文本与图片块顺序、`scripts/acpprobe` 真实模型验证。
- 真实模型：用一个支持看图的模型（gpt-6-sol 或 Claude 系）跑一轮“看这张截图并描述”，三个 provider 映射中至少跑通实际可用的两个。

## 待确认事项

| # | 问题 | 当前倾向 |
|---|---|---|
| Q1 | 分两期（先按路径读图 + chat `:image`，再 ACP 图片块）？ | 是 |
| Q2 | 工具名 `view_image`？ | 是 |
| Q3 | 默认缩放长边 1568 像素、单张 3.5MB？ | 是（按 provider 单图上限取较小者） |
| Q4 | 历史图片保留最近几个回合？ | 3 个回合，可配置 |
| Q5 | 模型能否看图：配置 `vision` + 内置名单推断？ | 是；deepseek、glm 先按“不支持”处理，实测确认后再改名单 |
| Q6 | 引入 `golang.org/x/image`（webp 解码、缩放）？ | 是（Go 官方扩展库，BSD 许可） |
| Q7 | 实施前独立审核？ | 是 |

## 结论与人工计划 Gate

第一期：`view_image` + chat `:image` + 三个 provider 映射 + 图片落盘与取回 + 模型能力声明；第二期：ACP 声明与图片内容块。用户批准 Q1–Q7 后先做独立审核，再派出实施；批准前不改代码。完成后告知 gofer 会话两条路各自的支持情况。
