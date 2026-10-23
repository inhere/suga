<!-- template_id: design; template_version: 1.1.1 -->
# file_read 并入 view_image（读图合并）设计

> 状态：Draft 0.1 / 待用户批准
> 日期：2026-10-23
> 关联 issue：`suga-yri4`；前序设计 `docs/design/2026-10-19-image-input-design.md`（图片输入 0.3，`view_image` 的来源）；成本依据 `docs/research/2026-10-22-longsession-cost-analysis.md`（固定前缀占 prompt 29%）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-23 | 监督（Claude） | 用户 2026-10-22 提出“减少一个工具”：把 `view_image` 并入 `file_read`，并指出 `:model` 切换会改变工具集、使前缀缓存失效；模型拿到图片路径时本来就常先试 `file_read`。本稿只出设计，回答 6 个问题，实施等设计与计划批准。 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

## 结论（先看）

1. **一个工具读文本也读图片**：`file_read` 先按文件头魔数（PNG/JPEG/GIF/WebP，与 `view_image` 同一个 `sniffImage`）判断，**在现有的 NUL 二进制嗅探之前**；命中就走图片分支，否则完全保持今天的文本/二进制行为。
2. **能看图**：返回一个 image part（复用 `prepareImage` 的识别、缩放、EXIF、编码与全部常量：长边 `images.max_side`（默认 1568）、输入 ≤20MB、≤40MP、编码后 ≤3.5MB、JPEG q85），文本说明沿用现有格式。
3. **不能看图**：返回**一段文字说明（不是错误）**：格式、原始尺寸、字节数、为什么不附图、以及“用 `:model` 换成能看图的模型再读一次”。尺寸只解文件头（`DecodeConfig`），不做像素解码。
4. **参数**：去掉 `max_side`（缩放只由配置 `images.max_side` 决定）；`offset`/`limit`/`outline` 对图片不适用，答案里写明；`paths` 多条目、带行范围的条目、`outline=true` 的调用**不附图片**，只给一行说明——一次调用最多读一张图。
5. **会话图片 ref 继续支持**：`images/<sha256>.<ext>`（省略提示里的形式）仍可读，规则与今天 `view_image` 相同（`isSessionImageRef` + 会话目录解析器）。
6. **Prefetch**：`file_read` 仍是 `Prefetchable`（并行读文本是既有收益），但**图片调用不预取**：给 `tools` 层加一个可选接口，`Registry.Prefetch` 在按 `Meta` 筛选后再问一次“这次调用要不要跳过预取”，`FileReadTool` 在参数里出现图片扩展名或 `images/` ref 时返回“跳过”。
7. **工具列表不再随模型变化**：删掉 `observedTools.ListTools` 的 vision 过滤；`VisionGate` 保留给 chat `:image`、ACP 提示、llm 回退与 `file_read` 执行时判断。收益量级：单次请求少 762 字符 schema（实测 `json.Marshal(schema)`）≈195 token，占固定前缀工具块（27,383 字符 ≈ 7.0k token）的 2.8%；`file_read` 描述加一句读图纸质上净节省约 550 字符。
8. **兼容**：旧会话历史里的 `view_image` 调用、按工具名写的 hook / 审批 / `tools.disabled`、chat 摘要、ACP、`ExtraTools`、文档与 skills 逐项处理；建议**保留 `view_image` 作为隐藏别名一个版本**（可调用，但 `ListTools`/manifest/`tool_search` 不列），下一版删除。
9. 8 个待批决定紧接本节（Q1–Q8）。

## 待批决定（优先看）

| # | 问题 | 建议 | 理由 |
|---|---|---|---|
| Q1 | 合并方向：`file_read` 读图，工具名 `view_image` 从工具表消失 | 是 | 少一个工具、工具集稳定、模型本来先试 `file_read` |
| Q2 | 不能看图时给**非错误说明**（尺寸来自文件头）而不是 error | 是 | 模型能照说明换模型；不污染失败统计 |
| Q3 | 去掉 `max_side` 参数，只用 `images.max_side` | 是 | schema 更小、少一类误用；旧参数接受并忽略 |
| Q4 | 保留 `view_image` **隐藏别名**一个版本（不进工具表/manifest/`tool_search`） | 是 | 旧会话续接与按名配置不断；下一版删除 |
| Q5 | 一次调用最多读一张图；`paths`/行范围/`outline` 对图片只给说明 | 是 | 三种 provider 的 tool-result 图片语义就是一张；多图另立设计 |
| Q6 | 预取豁免用“可选接口 + 按参数判断（扩展名/ref）” | 是 | 保住并行读文本；图片不预取与今天一致 |
| Q7 | `tools.disabled: [view_image]` 不再关掉读图（文档说明，不加隐式联动） | 接受；是否新增 `images.enabled` 总开关？本版**不加** | 隐式联动会让人猜；真有需求再加开关 |
| Q8 | trace 事件加 `images` / `image_bytes` 属性 | 加 | 现在只统计文本字节，图片读取在 trace 里看不出成本 |

Q1–Q6 是方案的骨架，Q7/Q8 只影响兼容与观测；1–8 节按“按推荐实施”的口径写，改任何一项都要连带改对应小节。

## 背景与动机

- 现状是两个只读工具：`view_image`（`pkg/agentcore/tools/native/view_image.go`，2026-10-19 图片输入 0.3 第一期）与 `file_read`（`pkg/agentcore/tools/native/file_read.go`）。`file_read` 把图片当二进制，只回一行 `[binary file: …]`（`file_read.go:632-637`）。
- 工具集随模型变化：当前模型不能看图时，`observedTools.ListTools` 把 `view_image` 从工具表里去掉（`internal/personal/observe.go:261-276`），执行层再拒一次缓存下来的旧调用（`:294-297`）。`:model` 切到不支持看图的模型、再切回来，工具块来回变。
- 成本形状（`docs/research/2026-10-22-longsession-cost-analysis.md`）：每次调用重复发送约 8.5k token 的固定前缀（工具 schema 27,383 字符 ≈ 7.0k token + system 3,391 字符 ≈ 0.9k token），84 次调用 ≈ 711k prompt token，占全部 prompt 的 29%。工具 schema 每省一个字符都是每次调用都省。
- 用户的观察（2026-10-22）：模型拿到图片路径时本来就常先试 `file_read`，而 `view_image` 得再试一次；一个工具同时读文本与图片，模型的选择面也小一圈。

## 已确认事实与约束

| 事实 | 位置 | 对设计的约束 |
|---|---|---|
| 图片识别只认文件头（`sniffImage`），PNG/JPEG/GIF/WebP；解码前用 `DecodeConfig` 查尺寸 | `view_image.go:282-294`、`:223-278` | 合并后沿用同一函数，识别不依赖扩展名；扩展名只作提示 |
| 图片文件几乎都含 NUL 字节 → 今天 `file_read` 先命中 `[binary file: …]` | `file_read.go:632-637` | 魔数判断必须排在 NUL 嗅探之前 |
| 工具结果里的图片只带内存字节，落盘在 `AppendMessages`（按 sha256 去重，写 `images/<sha>.<ext>`，只留 Ref） | `internal/personal/store/image_files.go:15-29`、`:82-147` | 合并后工具本身仍不写盘，不需要新的保存钩子 |
| `api.ToolResult` 已有 `Images []ImagePart`；三种 provider 映射已实现（Claude 原生、OpenAI Chat/Responses 追加 user 消息） | `pkg/agentcore/api/tools.go`、`design 0.3`“provider 映射” | 合并只改工具侧，provider 侧不动；一次调用只附一张图最省事 |
| `file_read` 是 `ReadOnly + Rereadable + Prefetchable`；`view_image` 是 `ReadOnly` 且刻意不 `Prefetchable` | `file_read.go:66-68`、`view_image.go:29-30`、`:113-115` | 合并后 `Meta` 要“文本可预取、图片不预取”，静态 `Meta` 表达不了 |
| `Prefetchable` 是工具级静态标记，`Registry.Prefetch` 按它筛选后并行执行 | `pkg/agentcore/tools/meta.go:42-45`、`prefetch.go:38-50` | 需要按调用参数判断的新机制 |
| 单请求图片上限已存在：张数 `images.max_request_count`（8）、字节 `images.max_request_bytes`（20MB）、历史裁剪按 fold 批次 | `internal/personal/images.go:17-37`、`:131-159` | 合并不会突破既有成本边界；多图一次读也没必要 |
| 会话图片 ref 由 `imagesDirFn` 在调用时解析当前会话目录（chat `:resume`/`:new` 后仍对） | `view_image.go:166-218`、`internal/wiring/runtime.go:746-754`、`internal/wiring/sessionaddirs.go:81-94` | `file_read` 需要同一个解析器与同一套 ref 规则 |
| `images.max_side` 由 `imageMaxSide()` 读成 `Env.ImageMaxSide` 传给工具 | `internal/config/images.go:23-25`、`internal/wiring/capabilities.go:475-484`、`catalog.go:109-112` | 缩放配置已有通路，`max_side` 参数可以退休 |
| `tools.disabled` 的校验白名单来自 native catalog 的名字 | `internal/wiring/tools_disabled.go:26-44` | 保留别名则 `tools.disabled: [view_image]` 仍然“合法”，但语义会变（见兼容） |
| `file_read` 的二进制/目录答案都是“非错误的说明”，路径类问题才是 error | `file_read.go:629-637`、`:160-176` | 图片的格式/尺寸问题沿用“非错误说明”，路径问题仍 error |
| `pkg/agentcore` 不得依赖 `internal/*` | `pkg/agentcore/layering_test.go` | 预取豁免接口只能放在 `pkg/agentcore/tools`，会话目录解析器仍是宿主注入的函数 |

## 总体方案

### 1. 识别顺序（file_read 的图片探测）

在 `readOne`/`readItem` 拿到已 `Stat` 的普通文件之后、进入 `readLineWindow` 之前插一层探测：

1. `Peek` 前 512 字节（魔数最长 12 字节，留余量）；
2. `sniffImage(head)` 命中 → 走图片分支（§2/§3）；
3. 未命中，且**扩展名属于** `.png/.jpg/.jpeg/.gif/.webp`、文件 ≤20MB → 仍按今天的规则读出内容（文本窗口或 `[binary file: …]`），但在答案第一行加一句 `[name: the file header is not a supported image format (PNG, JPEG, GIF, WebP)]`，告诉模型“它看着像图但不是图”（截断的 PNG 是常见情形）；
4. 其余情况**逐字节保持现状**：NUL 嗅探 → `[binary file: name, N bytes, type image/png; not shown]`；文本 → 带行号窗口。

顺序理由：图片一定含 NUL，先嗅 NUL 就永远读不到图；而魔数判断只对“命中了文件头”的文件生效，不会把文本当图片。

`ToolMeta` 不变（`CategoryRead`、`ReadOnly`、`Rereadable`、`Prefetchable`，`file_read.go:66-68`）：审批层、ACP tool kind、只读模式、`tools.read_only_allow` 的行为都不变；唯一的差别是图片调用在 `Prefetch` 里被跳过（§7）。

#### 工具描述草案（`file_read` 的 `Description`）

现有段落保留，末尾追加一段（长度约 200 字符，计入下面的收益口径）：

```
A picture (PNG, JPEG, GIF first frame or WebP; recognized by its file header, whatever the extension) is
returned as an image you can look at, scaled down to <images.max_side> px on its long side; a session image
ref such as images/<sha256>.png works too. A model that cannot see images gets a one-line note instead
(switch the model, e.g. chat :model, and read it again). offset, limit and outline apply to text only.
```

### 2. 能看图时：返回 image part

- 复用 `prepareImage`（`view_image.go:223-278`）：`sniffImage` → `DecodeConfig` 像素上限 → `decodeImage`（GIF 首帧、静态 WebP）→ EXIF 方向 → `scaleImage` → `encodeImage`（有透明 PNG，否则 JPEG q85）→ 超 3.5MB 再缩一半重编。常量全部不变（`:31-46`）。
- 结果：`&api.ToolResult{Content: text, Images: []api.ImagePart{part}}`，`text` 沿用今天的格式：`<path>: PNG 1600×900 (sent as image/jpeg 1568×882, 214 kB)`，末尾加一句 `the picture is attached above/below as an image part`（措辞在实现时按现有风格定）。
- 缩放长边只来自 `images.max_side`（`Env.ImageMaxSide`）；`max_side` 参数退休（§5）。
- 落盘仍由 `AppendMessages` 完成，工具不写文件（`store/image_files.go:15-29`）。
- `Env` 不新增配置项：`ImagesDir`、`Vision`、`ImageMaxSide` 三个字段（`catalog.go:69-77`）已经存在，只是 `file_read` 的 spec 也要把它们传进去。
- 已导出的管线函数 `native.ReadImageFile` / `native.PrepareImage`（`view_image.go:395-407`）继续供 chat `:image`、`run --image`、ACP prompt 使用，签名与语义不变；合并后它们与 `file_read` 的图片分支共用同一个内部实现，不复制第二份。

### 3. 不能看图时：文字说明（不是错误）

草案文案（英文，与现有工具文案一致）：

```
foo/shots/a.png: PNG 1600×900, 2.1 MB. Not attached: the current model cannot see images.
Switch to a model with vision (chat: :model) and read the picture again.
```

- `err == nil`，`Images` 为空。尺寸来自 `DecodeConfig`（只解头），所以文本模型至少知道格式与像素。
- 为什么不再用 error：今天 `view_image` 报 `current model cannot see images: switch to a model with vision (chat: :model)`（`view_image.go:144-146`）；合并后这是“这次读不了图”的正常事实，写成说明模型能照着换模型，也不会把一次工具调用记成失败（trace 的 error 计数、拒绝/审批统计都不受影响）。
- 格式不支持 / 损坏 / 超 20MB / 超 40MP：同样用**非错误的说明**（像 `file_read` 今天的 `[binary file: …]`、`[empty file]`）；路径越界、不存在、不是普通文件仍是 error（与 `file_read` 现状一致）。

### 4. offset / limit / outline / paths 对图片

| 调用形态 | 处理 |
|---|---|
| `path` 单条、无行范围、`outline` 未开 | 真正读图（§2 / §3） |
| `path` + `offset`/`limit` | 读图，说明里加一句 `offset/limit do not apply to a picture`（忽略参数，不报错） |
| `path` + `outline=true` | 读图，说明里加一句 `outline does not apply to a picture` |
| `paths` 多条目 | 图片条目只给一行说明 `[image: b.png, PNG 800×600 — not attached: read a picture with a single path call]`；其余条目照常读文本 |
| `path:12-40`（行范围后缀） | 同上，只给说明（行范围对图片没有意义） |

一次调用最多附一张图：三种 provider 的 tool-result 图片实现都是“一个结果旁边一张图”（OpenAI/Responses 是追加一条 user 消息），多图要另设计；这也顺带避免一次调用就把 `images.max_request_count` 顶满。多图需求留作后续（§不做）。

### 5. `max_side` 参数：去掉

- 理由：模型几乎不会算出合理的 `max_side`；长边只由 `images.max_side` 决定本来就已经是默认路径（`view_image.go:97-102`）；少一个参数 = schema 少 100 余字符、少一类出错面。
- 旧历史/旧客户端可能仍带 `max_side`：合并后的工具**接受并忽略**（不报错），只在 `:model` 别名路径上也无差别处理。
- `images.max_side` 的校验不变（正数，`internal/config/images.go:39-58`）。

### 6. 会话图片 ref

- `file_read` 接受 `images/<sha256>.<ext>`：`isSessionImageRef`（`view_image.go:210-218`）命中时从会话图片目录读（`imagesDirFn()`，无会话时给说明），不经过 `resolveRead`（会话目录在工作区之外）。
- `internal/personal/images.go:124` 的省略提示要从 `需要时 view_image <ref>` 改成 `需要时 file_read <ref>`；**已写进历史的消息不改**（历史文本就是历史）。
- 越界/绝对路径/更深目录的 ref 一律拒绝（沿用现有判断与错误）。

### 7. Prefetch：文本预取，图片不预取

- 现状：`file_read` 可预取（`file_read.go:66-68`、`tools.parallel_reads` 默认开，`internal/wiring/capabilities.go:348-354`），`view_image` 不可预取。
- 合并后仍是 `Prefetchable`（并行读文本是真实收益），但要按调用参数豁免图片：

```go
// pkg/agentcore/tools（草案）
// PrefetchExempt is the optional interface a Prefetchable tool implements to
// skip the run-ahead for a single call (it stays Prefetchable for its other
// calls).
type PrefetchExempt interface { SkipPrefetch(args map[string]any) bool }
```

`Registry.Prefetch`（`prefetch.go:38-50`）在筛 `MetaOf(t).Prefetchable` 之后多问一次；`FileReadTool.SkipPrefetch` 在 `path`/`paths` 里出现图片扩展名或 `images/` ref 时返回 true。

- 理由：
  1. 每次预取都会真的解码、缩放、编码，单张上限 40MP / 3.5MB 输出，`maxPrefetch` 是 8 个并发（`prefetch.go:13`）；被切掉的那一步里这份工作全白做。
  2. 与今天 `view_image` 的行为保持一致（它从不预取），合并不该顺手改掉这条性质。
- **要核实的一点**：`view_image.go:29-30` 注释说预取会“产生孤儿文件”。按代码，图片只在 `AppendMessages` 里写盘（`store/image_files.go:15-29`），而持久化是运行时逐调用做的（`pkg/agentcore/runtime.go:623-657`），预取本身不写盘。所以实现时以“浪费解码/内存 + 保持既有性质”为依据；孤儿文件这一条要实测后要么证实要么改掉该注释。
- 豁免只按参数猜（扩展名/ref）：一张没有图片扩展名的图片会走预取（多一次解码，无可见副作用）；一个以 `.png` 结尾的文本会少一次并行（也不影响正确性）。

### 8. 工具集稳定性与缓存

- 删除 `internal/personal/observe.go:261-276` 的 `ListTools` 过滤（以及 `:294-297` 对 `view_image` 的预先拒绝）：工具列表此后与模型无关。
- `VisionGate` 保留（`pkg/agentcore/tools/vision.go`），继续供：`internal/config` 的 `vision` 推断与展示、chat `:image`/Alt+V 的拒绝（`internal/chat/chat.go:146-150`、`chat_commands.go:64-80`）、ACP prompt 里的“已转文字说明”提示（`internal/acpserver/server.go:311-313`）、llm 回退时把图片换成说明（`NoImages`）、以及 `file_read` 执行时判断能否附图。
- 缓存影响（诚实口径）：
  - 少一个工具：实测 `view_image` schema 762 字符 ≈ 195 token（3.9 字符/token，同研究口径），`file_read` 描述会多出约 200 字符 ≈ 50 token，净省约 140 token/次调用；84 次调用 ≈ 12k prompt token。
  - 换模型时工具块不再变化：只在“同一 provider / 同一 API 端点内切模型且前缀缓存跨模型共享”时才兑现成缓存命中；多数情况下换模型本来就会让缓存变冷（研究里 t18 的缓存失效原因未定，见其“没做到/未验证”）。这一点按“减少一个工具 + 少一处随模型变化的前缀成分”表述，不承诺具体命中率。

## 兼容清单

| 对象 | 现状 | 合并后 | 处理 |
|---|---|---|---|
| 旧会话历史里的 `view_image` 调用 | 历史里 tool_call 名是 `view_image` | 模型可能沿用这个名字 | 保留隐藏别名（Q4）：仍可执行，走同一图片管线 |
| `hooks.pre_tool_use.tools`（glob 按工具名，`skills/suga-guide/references/tools.md:236`） | 有人只配 `view_image` | 模型发的是 `file_read`，该 hook 不再触发 | **行为变化**，CHANGELOG 写 NOTE：把 `file_read` 加进 glob（别名不解决这个，因为模型不再发这个名字） |
| `tools.disabled`（`internal/wiring/tools_disabled.go`） | `[view_image]` 可真正关掉读图 | 只关掉别名，图片仍能经 `file_read` 读 | 文档写清“要关读图用 `tools.disabled: [file_read]` 或 hook/审批”；是否加 `images.enabled` 见 Q7 |
| `tools.auto_allow` / `read_only_allow` / `memory.taint_tools` | 两个工具名都在名单里可用 | `file_read` 不变；`view_image` 只剩别名 | 无需改配置；文档提一句 |
| trace / `suga inspect trace` | 事件名 `view_image` | 事件名 `file_read` | 新会话有效；`addOutputAttrs` 只统计文本字节（`observe.go:481-495`），建议同次加 `images`/`image_bytes`（Q8） |
| chat 工具调用行（`internal/chat/tool_summary.go:32`、`:315-318`） | `view_image` → `summaryPathOnly` | `file_read` 已有“显示路径”的摘要 | 别名映射保留；`tool_summary_test.go:78-79` 继续通过 |
| ACP | `initialize` 声明、`session/prompt` 图片块复用 `view_image` 管线（`internal/acpserver/server.go:504-552`） | 管线函数不变 | 无代码改动；冒烟/`acpprobe` 复核 |
| `pkg/sugasdk` / `wiring.Options.ExtraTools` | 只按名字注册 | 不依赖 `view_image` | 无改动 |
| 文档与 skills | `README.md`、`docs/usage/gofer-personal-agent.md:339,345`、`skills/suga-guide/references/{chat,tools,config}.md`、`CHANGELOG.md` | 读图入口写法变了 | 同批改文档；`scripts/` 无 `view_image` 引用（已 grep），冒烟脚本不受影响 |
| 测试 | `capabilities_test.go:42` 工具名列表（含 `view_image`）、`readonly_test.go:24`（9 个只读 schema）、`vision_test.go:40-75` 按模型增减 | 全部要改：列表去掉 `view_image`（9 → 8），`vision_test.go` 改为“任何模型下列出的工具都相同 + 执行时给说明” | 见“验证” |

### 隐藏别名的建议

**建议保留 `view_image` 一个版本，但不进工具列表**。

- 做法：`view_image` 仍在 catalog 与 registry 里（`catalog.go` 的 spec 保留），执行直接复用 `file_read` 的图片分支（同一个函数，避免两条管线漂移），但 `ListTools`、manifest、`tool_search` 都不列（`tools.Meta` 加 `Hidden` 一类标记，或维护一张别名表）。
- 理由：① 续接旧会话时模型常沿用历史里的工具名，报“工具不存在”会让它反复试；② 用户 hooks/审批/`tools.disabled` 里按名写的配置不会因为名字消失而报“未知工具”；③ 成本很小（一个已存在的对象，只是不列）。
- 代价：一个版本内要维护“别名与主工具同义”的约束与测试；`tools.disabled` 的语义分裂（见上表），文档要写清楚。
- 下一版：删别名，同步清理 `chat/tool_summary.go` 的映射、`tools_disabled` 白名单、`view_image_test.go` 与文档。

## 风险

1. **`vision` 推断错**：把能看图的模型判成不能看 → 模型会读到说明、多走一次调用（可见、可恢复）；判成能看而实际不能 → 请求带 image part 被 provider 拒（现状同样如此，没变差）。
2. **输出层与图片**：`file_read` 是 `Rereadable`（`file_read.go:66-68`），输出层不截断/不外置它的结果；今天 `view_image` 不是。本次已 grep `pkg/agentcore/tools/*.go` 没有 `Images` 引用（它们只处理 `Content`），所以图片不会被外置；仍要加一条单测锁住“图片结果的文本不被外置、图片部件不丢”。
3. **预取豁免靠猜测**：漏判（无扩展名图片）多一次解码，误判（`.png` 文本）少一次并行，都无正确性影响。
4. **一次只读一张图**：想看两张（前后对比截图）要两次调用，多一次模型往返（≈8.5k 前缀 + 输出）。若真实使用里常见，另立“paths 多图”设计（要同时改 provider 追加消息与上限）。
5. **`file_read.go` 已 760 行**：图片分支按规范拆到 `file_read_image.go`（单文件 ≤1000 行），不动无关代码。
6. **`tools.disabled: [view_image]` 语义变化**（见兼容表）：若有人靠它省钱，合并后会失效；本稿建议文档说明，不加隐式联动。
7. **模型把图片当文本重读**：`file_read` 读图失败（格式不支持）时给说明而不是错误，模型可能再试一次；说明里写明支持的格式与“用单 path 读”。

## 不做

- 多图/批量读图（`paths` 里的图片只给说明）。
- OCR、图片生成、缩略图缓存文件、按 provider 选不同编码格式。
- 改 provider 映射、图片落盘、历史裁剪、单请求上限（沿用 design 0.3）。
- 改 chat `:image` / `run --image` / `send --image` / Alt+V 的入口（它们继续直接附图，不经模型调用）。
- 为“看图能力”保留任何工具表过滤的替代品（提示词里也不再写“当前模型不能看图时不要调用”之类）。

## 验证

**单测**（`github.com/gookit/goutil/x/assert`，同一函数多用例 `t.Run`）：

- `pkg/agentcore/tools/native`（`file_read_image_test.go` 新建，或并入 `view_image_test.go`）：
  - 四种格式经 `file_read` 读：返回 1 个 image part + 文本说明；缩放边界（等于/大于/小于 `max_side`）；透明 → PNG、无透明 → JPEG；EXIF 方向；超 40MP / 超 20MB / 损坏文件给非错误说明；`.png` 结尾的文本文件不发图且带“文件头不是图片”提示；普通二进制答案与今天一致。
  - 不能看图（`tools.NewVisionGate(false)`）：同样的调用 `err == nil`、`Images` 为空、说明含 `:model`。
  - `paths` 多条目含图片、`path:12-40`、`outline=true`：都只给说明，图片条目不附图。
  - 会话图片 ref：`images/<sha>.png` 经 `imagesDirFn` 命中；无会话目录时给说明而不是崩；`../images/x.png` 被拒。
  - `max_side` 参数：传了也不报错、不影响输出（向后兼容）。
- `pkg/agentcore/tools`（`prefetch_test.go` 扩展）：两个 `file_read` 调用、其中一个是图片 → 只有文本那个被预取（工具统计执行次数）；未实现接口的工具行为不变。
- `internal/wiring`（真实装配路径：`buildPersonalCapabilities` / `wiring.Open`）：任何模型下列出的工具一样；执行 `file_read` 读一张图，`ToolResult.Images` 非空，经真实 `AppendMessages` 落盘成 `images/<sha>.png`（`internal/wiring/read_image_test.go` 已有同类装配）。
- `internal/personal`：省略提示改成 `file_read <ref>` 后 `images_test.go` 同步。
- `internal/chat`：`tool_summary_test.go` 保留 `view_image` 用例；补 `file_read` 读图的显示用例。
- `internal/acpserver`：现有图片用例不改（管线未变）。

**真实模型**（必须，写进交付说明）：

1. 能看图的模型：`suga run --model <vision 模型> "读 <工作区里一张截图>，说出图上第一行文字"` → 只有一次 `file_read` 调用、答案内容正确，trace 里是 `file_read` 事件、无 `view_image`。
2. 不能看图的模型（例如显式 `vision: false` 的模型）：同一提示 → 工具结果是一段说明、回合不报失败，模型据此提示换模型。
3. `:model` 切换：同一 provider 内切模型前后，首轮请求的工具有序列表字节一致（配合 `suga inspect trace --json` 的请求快照/`prefix_reused` 观察）。

**伪终端冒烟**：新增 `scripts/ptysmoke/` 场景（fake 模型调 `file_read` 读一张图 → 断言工具行渲染 + trace 里有 `file_read` 与图片事件；不能看图时断言说明），加入 `make smoke-chat`。本机是 Windows、无 pty，**脚本未运行，待监督在 Linux 容器验证**。

**反向验证**（实施后各做一次）：去掉魔数探测 → “图片返回 image part”失败；去掉预取豁免 → “图片不被预取”失败；把“不能看图”改回 error → 对应断言失败；把 `ListTools` 过滤加回去 → “工具列表与模型无关”失败。

## 实施拆分（批准计划后，每步一个本地提交）

1. `pkg/agentcore/tools/native`：拆出共享图片管线 + `file_read` 图片分支与说明（`file_read_image.go`）+ 单测；
2. `internal/wiring` + `catalog.go`：把 `vision`/`imagesDirFn`/`ImageMaxSide` 接到 `file_read`；删 `ListTools` 过滤、`errToolWithoutVision` 与旧拒绝；改 `vision_test.go`/`capabilities_test.go`/`readonly_test.go`；
3. `pkg/agentcore/tools`：预取豁免接口 + `Registry.Prefetch` 接线 + 单测；
4. `internal/personal`：省略提示改 `file_read`；`internal/chat` 摘要用例；`internal/acpserver` 复核；
5. `view_image` 隐藏别名 + 兼容测试（`tools.disabled`、旧调用、manifest 不列）；
6. 文档（README、`docs/usage/gofer-personal-agent.md`、skills、CHANGELOG）、ptysmoke 场景、真实模型记录。
