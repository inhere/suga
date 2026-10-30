# Decision models（TypeSafe System One / jev）调研

日期：2026-10-30　关联 issue：suag-zhh0（相关：suag-uesr、suag-5qb4、suga-a3ig）

## 核实范围
- 本地：`tmp/refs/commandcode/models.json`（88 个模型）、`tmp/refs/DeepSeek-Reasonix`（第三方项目，含 TypeSafe 客户端实现）、`local/analysis/2026-10-21-jev-nomi-borrowing.md`。
- 公开网页：`https://typesafe.ai` 与 `https://docs.typesafe.ai/introduction` 已读；其 Quick Start / Primitives / API 参考页**未读**（未核实）。
- 未调用任何真实 API；未经真实服务验证。

## 是什么
- Decision model（TypeSafe 称 “System One Model”）是给软件内部做判定用的模型，不生成给人看的文本，返回类型化的决策与校准过的置信度；jev 是 TypeSafe AI 的第一个公开型号，处于 early access（网页说法，厂商自述，未独立验证）。
- 网页文档列出三种问题类型：`choice`（从选项里选一个，返回选择、概率、置信度）、`score`（按评分档位打分）、`noul`（判断陈述真假，返回 0~1）。一次请求可混合多个问题，对同一份 state 并行评估。
- 厂商给出价格 $42 / 十亿输入 token、延迟约百毫秒级（厂商自述）。

## 与 command-code 的关系（结论：快照里查不到）
- `models.json` 全部 88 项的 `supported_endpoints` 只有三种组合：`/chat/completions`+`/responses`（68）、`/messages`（11）、`/chat/completions`（8）；没有 `typesafe`、`jev`、`decision` 相关条目，也没有第四种端点。
- 因此“command-code 已支持 Decision models”在这份快照里**找不到依据**；若 command-code 另有独立入口，需查其官方文档（未核实）。Decision 模型在 Reasonix 里是独立“provider kind”，不走 command-code 网关。

## 协议（来自 Reasonix 的实现 `internal/safety/typesafe/client.go`，第三方参考，非官方文档）
- `POST {base}/v1/systemone`，默认 base `https://api.typesafe.ai`，`Authorization: Bearer <key>`（环境变量约定 `TYPESAFE_API_KEY`）。
- 请求：`{"state": <任意 JSON>, "model": "jev-latest", "questions": {"<id>": {"type": "choice|score|noul", "instructions": ..., "criteria": ...}}}`。`choice` 的 criteria 是 2~255 项的对象（选项名到说明）；`score` 的 criteria 是 2~10 档有序数组；`noul` 无 criteria。
- 响应：`{"model", "answers": {"<id>": <类型化值>}, "usage": {"input_tokens","output_tokens"}, "routing"?}`；answers 的内部结构（概率、置信度字段名）因问题类型而异，Reasonix 以 RawMessage 保留，我们这里未核实具体字段。
- 另有 Laya：官方 Python SDK 本地运行，或自托管网关 `POST /v1/systemone`（同样来自 Reasonix 配置，细节未核实）。

结论：它**不是** OpenAI 兼容协议（不是 chat/completions、responses、messages），也没有流式、工具调用、消息列表；不能复用 suga 现有 `llm` provider（后者围绕对话消息与工具回合）。

## 在 suga 的可能用途
对应 2026-10-21 的借鉴整理：封闭选项的重复判断可交给决策模型，如
1. 模型路由 / 子 agent 选择（suga-a3ig，当前决定为显式配置不做自动路由）。
2. 审批小模型裁判（approval.judge_model，设计里已决定“维持暂停”）。
3. 卡死 / 目标检查、压缩时给旧工具结果打分（现为规则版 suga-yo3q）。
4. 工具调用前后的闸门判定（高置信度放行、低置信度转人工）。

## 接入成本
- 需要新增一个非对话的“decision client”（独立于 `llm.Provider`）：配置节（base_url、api_key_env、model）、问题构造器、答案解码、置信度阈值策略，以及上面某个具体调用点；还要 i18n、文档、httptest 测试。约 1~2 批的工作量，调用点本身（路由/裁判）才是大头。
- 外部依赖：厂商早期服务、需要单独的密钥；每次判定会把 state（可能含代码、命令）发往第三方，需接入 net-access / 隐私策略与脱敏（`redact`）。

## 建议
- **本次不做实现**：协议只来自第三方实现与营销页，官方 API 参考未核实；与现有 provider 不兼容；suga 内已有的候选用途都已被决定“规则版或维持暂停”，没有一个需要它的具体场景；command-code 侧也没有证据支持。硬接入价值不明。
- 若以后要做：先读 `docs.typesafe.ai` 的 Quick Start / Primitives 核实答案字段与错误码；挑一个具体场景（建议“run/ACP 无人值守时的审批判定”或“卡死检查”）做设计；用 `llm.Provider` 之外的独立小接口，并默认关闭、可配置阈值。
- 建议关闭 suag-zhh0 或降级为“调研完成，待场景”；与 suag-5qb4 的辅助小模型方案合并评估（后者走对话式小模型，更贴合现有 provider）。
