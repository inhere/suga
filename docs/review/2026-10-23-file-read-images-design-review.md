# file_read 并入 view_image（读图合并）设计 评审

## 评审目标与范围

- 目标: `docs/design/2026-10-23-file-read-images-design.md`（suga 仓库，分支 `docs/file-read-images-design`，候选提交 `4b7027f0`）
- 修订版本: Draft 0.1
- 评审者: Claude（监督会话；设计由 suga 子 agent 撰写，评审者未参与撰写）
- 日期: 2026-10-23
- 排除项: 实现代码（尚无）；多图读取、OCR 等设计已列为“不做”的内容。

## 输入与方法

- 第 1 轮全量通读候选设计。
- 抽查关键事实：`pkg/agentcore/tools/prefetch.go:38-50`（`Registry.Prefetch` 只按静态 `MetaOf(t).Prefetchable` 筛选，确需按参数的豁免机制）、`internal/personal/images.go:27,124`（省略提示写的是 `view_image <ref>`，已写入历史）、`pkg/agentcore/tools/output.go`（输出层只处理 `Content`，不涉及 `Images`）。
- 依据：评审报告合同、SR1301、SR1409（未投产功能、无生产数据、可回滚、无出机器动作 → 单轮合并轴评审）。

## 验收标准

- 回答任务的 6 个问题（识别与返回、`max_side`、预取、兼容、工具集稳定性、风险与验证）；
- 不改 provider 映射、图片落盘与既有上限；`pkg/agentcore` 不依赖 `internal/*`；
- 收益与代价表述如实。

## 发现

### F1 [MEDIUM] 隐藏别名删除后，历史里的“需要时 view_image <ref>”提示会失效

- 证据: `internal/personal/images.go:124` 生成的省略提示 `[...；需要时 view_image <ref>]` 已写进既有会话历史；设计 §6 只把新提示改为 `file_read <ref>`、“已写进历史的消息不改”，§隐藏别名 计划“下一版删别名”。
- 影响: 别名删除后，续接旧会话的模型照历史提示调用 `view_image` 会得到“工具不存在”，正是 Q4 想避免的情况；只是推迟了一个版本。
- 建议: 删别名的条件不写成“下一版”，改为可观测条件（例如 trace 中一段时间没有 `view_image` 调用），或在删除时把未知工具名 `view_image` 的调用改为返回一句“已并入 file_read，请用 file_read <同一路径>”的说明（不是错误）。二选一写进设计。
- disposition: `CORE_CORRECTIVE`

### F2 [LOW] 收益结论需在“结论”节写明量级，避免误读为降本主因

- 证据: §8 如实算出净省约 140 token/次（约占固定前缀的 1.7%），但“结论”第 7 条把它与“工具集不随模型变化”并列，读者容易把本设计理解为主要降本手段。
- 影响: 期望偏差；不影响方案正确性。
- 建议: 结论第 7 条补一句“主要收益是工具集稳定与模型少走一次弯路，token 节省是次要的（约 1.7% 固定前缀）”。
- disposition: `CORE_CORRECTIVE`

### F3 [LOW] 修订记录作者应为实际撰写者

- 证据: 修订记录 0.1 作者写“监督（Claude）”，实际由 suga 子 agent（opencode-go/deepseek-v4.1-flash，job `job-20261023-085406-7b46a4b6`）撰写。
- 影响: 来源记录不准（元数据纠正，不递增版本）。
- 建议: 改为实际撰写者。
- disposition: `CORE_CORRECTIVE`

## 安全核对（SR1301）

- 鉴权 / 资源归属: 已核对——会话图片 ref 沿用既有越界判断（`isSessionImageRef`，拒绝 `../`、绝对路径与更深目录），路径解析仍走 `resolveRead`。
- 注入防护: 不适用（不执行外部命令；图片解码沿用既有像素与字节上限）。
- 秘密不进日志: 不适用（不新增日志内容；trace 只加 `images`/`image_bytes` 计数）。
- 入参校验: 已核对——20 MB / 40 MP / 3.5 MB 上限沿用；`max_side` 参数接受并忽略。

## 覆盖缺口与限制

- 未实测 §7 提到的“预取产生孤儿文件”注释是否成立（设计已要求实施时核实）。
- 多图读取明确不在范围内。

## 结论

PASS（无 `CORE_BLOCKING`）。F1–F3 为 `CORE_CORRECTIVE`，建议修订为 0.2 后提交用户批准；SR1409 封顶一轮，不再复审。

## 剩余风险与后续

- 计划阶段固化：别名删除条件或兜底说明（F1）、`vision_test.go`/`capabilities_test.go` 的改写、伪终端冒烟在容器验证、真实模型三项验证。
- `hooks.pre_tool_use.tools` 只配了 `view_image` 的用户会失去该 hook 触发，CHANGELOG 必须写 NOTE（设计兼容表已列）。
