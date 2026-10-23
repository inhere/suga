<!-- template_id: plan; template_version: 1.2.0 -->
# file_read 读图合并 实施计划

> 状态：Approved 0.1（2026-10-23 用户：“批准读图合并计划，开始实施”）/ 已实施（第三十七批 F）

## 修订记录

| 版本 | 日期 | 作者 | 摘要 |
|---|---|---|---|
| 0.1 | 2026-10-23 | Claude（监督会话） | 初稿：按已批准设计 0.2 拆为 6 个任务、3 个波次 |

> 仅语义变化递增版本；纯 identity/provenance/元数据纠正沿用原版本，并在 Git/进度记录中留痕。

思考模式：严谨型（rigorous），独立于设计阶段。核心目标：`file_read` 能读图片（能看图的模型得到图片，不能看图的得到非错误说明），工具表不再随模型变化，`view_image` 退为隐藏别名。scope freeze：设计 0.2 的“范围”与“实施拆分”；review budget：SR1409 低暴露度封顶一轮合并轴评审；停止条件：完成 Gate 全部满足，或出现 `Semantic Amendment` / `Ownership Conflict`。

## 目标与完成定义

目标：实现 `docs/design/2026-10-23-file-read-images-design.md`（Approved 0.2）。

完成定义（全部满足）：

1. `file_read` 读 PNG/JPEG/GIF/WebP（按文件头识别）：能看图的模型 → 结果含 1 个 image part 与文本说明；不能看图 → `err == nil`、无图片、说明含格式/尺寸与 `:model` 提示。文本与普通二进制文件的结果与改动前逐字节一致。
2. `paths` 多条目、行范围后缀、`outline=true` 遇到图片只给一行说明；`offset`/`limit` 对图片忽略并说明；`max_side` 参数接受并忽略。
3. 会话图片 ref `images/<sha256>.<ext>` 经 `file_read` 可读，越界 ref 被拒（规则同今天的 `view_image`）。
4. 工具表与当前模型无关（任何模型下列出的工具相同）；`view_image` 不出现在工具表、manifest、`tool_search`，但按名调用仍可执行（隐藏别名）。
5. 含图片参数的 `file_read` 调用不被预取，其余 `file_read` 调用的预取行为不变。
6. 省略提示改为“需要时 file_read <ref>”；trace 的读图事件带 `images`/`image_bytes`。
7. 真实模型验证完成（能看图模型读图答对、不能看图模型得到说明、切换模型前后工具列表字节一致），或如实写明未完成。

## 范围、排除项与授权

- 范围：设计 0.2“架构”与“实施拆分”所列改动，以及兼容清单中的测试与文档同步。
- 排除：多图读取、OCR、provider 映射/图片落盘/上限的任何改动、chat `:image`/`run --image`/`send --image`/Alt+V 入口、`images.enabled` 总开关、**删除 `view_image` 别名**（后续单独决定）。
- 授权：用户 2026-10-23 批准设计 0.2（只授权规划）。**实施需要用户批准本计划并给出执行请求。** 本地按任务提交是默认授权；push、tag、发版另行授权。
- `host_or_non_offline_action=NOT_APPLICABLE`

（真实模型验证经用户已配置的 provider 发起只读模型调用，与以往批次相同。）

## 输入与批准证据

- 设计：`docs/design/2026-10-23-file-read-images-design.md`，Approved 0.2（提交 `3ed6d7b7`，用户原话“批准读图合并设计 0.2”）。
- 评审：`docs/review/2026-10-23-file-read-images-design-review.md`（PASS，F1–F3 已并入 0.2）。
- 前序：`docs/design/2026-10-19-image-input-design.md`（图片输入 0.3）。

## Capability Discovery

### Capability decisions

| capability_id | required_capability | searched_candidates | direct_reuse | thin_adapter_or_owner_extension | decision | proven_gap | duplication_and_lifecycle_risk |
|---|---|---|---|---|---|---|---|
| CAP-01 | 图片识别、缩放、编码 | `view_image.go` 的 `sniffImage`/`prepareImage`、导出的 `ReadImageFile`/`PrepareImage` | 是 | 拆成同包共享函数供 `file_read` 调用 | `DIRECT_REUSE` | none | owner=tools/native;lifecycle=existing；只保留一份实现 |
| CAP-02 | `file_read` 图片分支 | `FileReadTool` 的 `readOne`/`readItem` | 部分 | 在现有工具内加分支（新文件 `file_read_image.go`） | `OWNER_EXTENSION` | none | owner=tools/native;lifecycle=existing；文件 ≤1000 行 |
| CAP-03 | 按调用参数跳过预取 | `Registry.Prefetch` 只按静态 `Prefetchable` 筛选 | 否 | 新增可选接口 `PrefetchExempt` 并在 `Prefetch` 中询问 | `OWNER_EXTENSION` | none | owner=agentcore/tools;lifecycle=existing；未实现接口的工具行为不变 |
| CAP-04 | 隐藏别名（可调用、不列出） | catalog spec、`ListTools`、manifest、`tool_search` | 部分 | `tools.Meta` 增加隐藏标记或别名表，列表处过滤 | `OWNER_EXTENSION` | none | owner=agentcore/tools;lifecycle=existing；删除别名另行决定 |
| CAP-05 | 工具表不随模型变化 | `observedTools.ListTools` 的 vision 过滤 | 是 | 删除过滤与预先拒绝 | `NOT_NEEDED` | none | owner=internal/personal;lifecycle=existing |

### New module candidates

| candidate_id | capability_id | proposed_module | searched_candidates | direct_reuse_gap | thin_adapter_or_owner_extension_gap | proven_gap | unique_owner_and_lifecycle | deletion_or_merge_handling |
|---|---|---|---|---|---|---|---|---|
| NONE | none | none | none | none | none | none | none | none |

### Rejected new tools

| rejected_candidate | capability_id | deletion_test_and_reason |
|---|---|---|
| 新工具 `read_any` 取代两者 | CAP-02 | 删除测试 PASS：扩展现有 `file_read` 即满足验收，新工具只会多一个名字 |

## 前置检查与 fail-closed 条件

运行目录均为 Git root `inhere-tools/suga`。

- 基线：分支 `main`，HEAD `3ed6d7b7`，`git status --short` 为空（2026-10-23 规划时核对）；实施在 gofer worktree 分支进行。
- fail-closed：
  - 文本文件、普通二进制文件、目录的 `file_read` 结果必须逐字节不变（T1 回归用例对照改动前输出）。
  - `pkg/agentcore` 不得依赖 `internal/*`（`layering_test.go`）。
  - 需要改 provider 映射、图片落盘、单请求上限、chat 图片入口，或改变设计决定（Q1–Q8）→ `Semantic Amendment`，停止并请求人工 Gate。
  - 命中他人未提交改动 → `Ownership Conflict`，停止。

## 波次与依赖

| 波次 | 任务 | 依赖 |
|---|---|---|
| W1 | T1 图片分支；T2 预取豁免 | 无 |
| W2 | T3 接线与工具表；T4 隐藏别名 | T1、T2 |
| W3 | T5 周边同步与冒烟；T6 文档与真实模型 | T3、T4 |

每个任务完成后单独提交（提交信息带任务号）。波次末运行 `GOWORK=off go test -count=1 ./internal/... ./pkg/...` 一次。

## 任务

### T1 共享图片管线与 `file_read` 图片分支

- 文件: `pkg/agentcore/tools/native/view_image.go`（拆出共享函数）、新增 `pkg/agentcore/tools/native/file_read_image.go` 与 `file_read_image_test.go`、`file_read.go`（插入探测点与描述）
- 动作: 按设计 §1–§6：魔数探测排在 NUL 嗅探之前；能看图返回 image part + 文本说明；不能看图返回非错误说明；`offset/limit/outline/paths/行范围` 的处理表；`max_side` 接受并忽略；会话 ref 经 `imagesDirFn` 解析；扩展名像图片但文件头不是时加提示。`file_read` 描述追加设计草案的一段。
- 验证: `GOWORK=off go test -count=1 -run 'FileReadImage|ViewImage|FileRead' ./pkg/agentcore/tools/native/`，覆盖设计“验证/单测”中 `pkg/agentcore/tools/native` 的全部条目 + 文本/二进制/目录结果逐字节不变的回归。
- 完成标准: 用例通过；只有一份图片管线实现。
- 依赖: 无

### T2 预取豁免接口

- 文件: `pkg/agentcore/tools/prefetch.go`、`meta.go`（或新文件 `prefetch_exempt.go`）、`prefetch_test.go`；`file_read_image.go`（实现 `SkipPrefetch`）
- 动作: 新增 `type PrefetchExempt interface { SkipPrefetch(args map[string]any) bool }`；`Registry.Prefetch` 在 `Prefetchable` 筛选后询问；`FileReadTool.SkipPrefetch` 在 `path`/`paths` 含图片扩展名或 `images/` ref 时返回 true。核实 `view_image.go` 注释所说的“预取产生孤儿文件”，在汇报中给出结论并相应改注释。
- 验证: `GOWORK=off go test -count=1 -run 'Prefetch' ./pkg/agentcore/tools/`：两次 `file_read` 中一次为图片 → 只预取文本那次；未实现接口的工具不变。
- 完成标准: 用例通过。
- 依赖: 无（实现 `SkipPrefetch` 的部分依赖 T1 的文件，可在 T1 之后补）

### T3 接线与工具表稳定

- 文件: `pkg/agentcore/tools/native/catalog.go`、`internal/wiring/runtime.go`、`internal/wiring/capabilities.go`、`internal/personal/observe.go`；测试 `internal/wiring/{vision_test.go,capabilities_test.go,readonly_test.go,read_image_test.go}`
- 动作: 把 `Vision`、`imagesDirFn`、`ImageMaxSide` 接到 `file_read`；删除 `observedTools.ListTools` 的 vision 过滤与对 `view_image` 的预先拒绝（`errToolWithoutVision`）；trace 读图事件加 `images`/`image_bytes`（Q8）。
- 验证: `GOWORK=off go test -count=1 ./internal/wiring/ ./internal/personal/`：任何模型下工具列表相同；真实装配路径读图 `ToolResult.Images` 非空并经 `AppendMessages` 落盘为 `images/<sha>.png`；只读工具数 9 → 8。
- 完成标准: 用例通过；`layering_test.go` 通过。
- 依赖: T1、T2

### T4 `view_image` 隐藏别名

- 文件: `pkg/agentcore/tools/meta.go`（隐藏标记）或别名表、`pkg/agentcore/tools/native/catalog.go`、`view_image.go`、工具列表/manifest/`tool_search` 的列出处（实施时 grep 定位）；`internal/wiring/tools_disabled.go`
- 动作: `view_image` 仍注册、可按名执行（复用 T1 图片分支），但不出现在 `ListTools`、manifest、`tool_search`；`tools.disabled: [view_image]` 仍是合法配置，只关掉别名（文档说明）。
- 验证: `GOWORK=off go test -count=1 -run 'Alias|Hidden|ViewImage|Disabled' ./pkg/agentcore/... ./internal/wiring/`：别名可执行且结果与 `file_read` 读同一图一致；列表、manifest、`tool_search` 中不出现。
- 完成标准: 用例通过。
- 依赖: T1

### T5 周边同步与伪终端冒烟

- 文件: `internal/personal/images.go`（省略提示改 `file_read <ref>`）与 `images_test.go`、`internal/chat/tool_summary.go` 与 `tool_summary_test.go`、新增 `scripts/ptysmoke/chat_read_image.py` 并加入 `Makefile` 的 `smoke-chat`
- 动作: 省略提示改写；chat 工具行显示 `file_read` 读图的路径摘要（`view_image` 映射保留）；冒烟：fake 模型调 `file_read` 读图 → 断言工具行与 trace 中 `file_read` 事件及 `images` 属性；不能看图配置下断言说明文本。
- 验证: `GOWORK=off go test -count=1 ./internal/personal/ ./internal/chat/`；冒烟脚本在 Windows 无法运行时写明“待监督在 Linux 容器验证”，并在脚本 docstring 写反向验证步骤。
- 完成标准: 单测通过；冒烟脚本已加入 `make smoke-chat`。
- 依赖: T3、T4

### T6 文档、CHANGELOG 与真实模型验证

- 文件: `README.md`、`docs/usage/gofer-personal-agent.md`、`skills/suga-guide/references/{chat,tools,config}.md`、`CHANGELOG.md`
- 动作: 读图入口改为 `file_read`；写清别名与 `tools.disabled` 语义变化；CHANGELOG 写 NOTE：`hooks.pre_tool_use.tools` 只配 `view_image` 的需把 `file_read` 加进去。真实模型三项（设计“验证/真实模型”）：隔离 SUGA_HOME，按 `scripts/eval --config-from` 的做法复制配置到临时目录，跑完删除。
- 验证: 三条真实模型命令与结果贴进汇报；`GOWORK=off go vet ./... && GOOS=windows go vet ./...`；`gofmt -l`。
- 完成标准: 文档与 CHANGELOG 更新；真实模型结果已记录或如实写明未完成。
- 依赖: T5

## 回滚与恢复

- 每个任务一个提交，按任务 `git revert`。不改存储结构、无迁移；trace 新属性对旧版本无影响。
- 恢复点：基线 `3ed6d7b7`；实施在独立 worktree 分支，未合入则丢弃分支。

## 人工 Gate

1. 计划批准（本文件 0.1 → Approved）。
2. 当前执行请求（批准时一并给出即可）。
3. 外部动作 Gate：push、tag、发版不在本计划内，另行授权。

## 可追溯性

| 需求（设计 0.2） | 任务 | 验证 |
|---|---|---|
| Q1、§1–§3 file_read 读图 / 非错误说明 | T1 | native 单测：四种格式、不能看图、逐字节回归 |
| Q3、§5 去掉 `max_side` | T1 | 传参不报错、不影响输出 |
| Q5、§4 一次一张图 | T1 | paths/行范围/outline 用例 |
| §6 会话图片 ref | T1、T5 | ref 用例；省略提示改写 |
| Q6、§7 预取豁免 | T2 | prefetch 用例 |
| §8 工具表稳定 | T3 | vision_test 改写：任何模型列表相同 |
| Q8 trace 属性 | T3、T5 | 事件属性断言、冒烟 trace 检查 |
| Q4、F1 隐藏别名 | T4 | 别名执行与不列出用例 |
| Q7、兼容清单 | T4、T6 | `tools.disabled` 用例；文档与 CHANGELOG NOTE |
| 验证/真实模型 | T6 | 三条真实模型命令输出 |

## 完成 Gate 与剩余工作

- T1–T6 完成并提交；各任务验证输出贴在汇报中。
- 反向验证（设计“验证/反向验证”四项）实施后各做一次并贴结果。
- 监督方：容器 race、`make smoke-chat`（含新冒烟）、suga-verify workflow 全部通过后合并。
- 剩余（不在本计划）：删除 `view_image` 别名（按设计 F1 的观察条件，另行决定）、多图读取。
