# Suga 文档

Suga 当前是个人 Agent，通过 CLI 或 ACP 运行；Gofer 负责作业、会话入口与审批。功能和命令以仓库根目录 [README](../README.md) 与实际源码为准。

## 当前资料

- [开发基础工具与 skills 设计](design/2026-10-10-dev-tools-and-skills-design.md)与[实施计划](plans/2026-10-10-dev-tools-and-skills-plan.md)（[复盘](plans/2026-10-10-dev-tools-and-skills-retrospective.md)）：file_grep/find/edit、web_fetch、shell_exec 重写、输出上限、基础提示词、skills、运行状态瘦身。
- [chat 写工具确认设计](design/2026-10-09-chat-write-approval-design.md)与[实施计划](plans/2026-10-09-chat-write-approval-plan.md)（[复盘](plans/2026-10-09-chat-write-approval-retrospective.md)）：tools.allow_write、chat 终端确认、stream-json 工具事件。
- [配置分层与 chat 体验设计](design/2026-10-09-config-layering-chat-design.md)与[实施计划](plans/2026-10-09-config-layering-chat-plan.md)（[复盘](plans/2026-10-09-config-layering-chat-retrospective.md)）：providers/model 配置、包分层、hooks 通用化、chat 渲染。
- [核心能力演进设计](design/2026-10-08-core-capabilities-design.md)：单库存储、压缩、计划、`--from` 续接、`tool_search`、观测与工具层审批。
- [核心能力实施计划](plans/2026-10-08-core-capabilities-plan.md)与[复盘](plans/2026-10-08-core-capabilities-retrospective.md)：已完成。
- [个人 Agent 设计](design/2026-10-06-personal-agent-redesign-design.md)与[实施计划](plans/2026-10-06-personal-agent-redesign-plan.md)：产品范围与会话边界（历史基线）。
- [Gofer 接入](usage/gofer-personal-agent.md)：当前 `suga` CLI 与 `suga-acp` 的配置、续接和审批。
- [LLM 配置](usage/llm-config-v0.md)：`model` / `providers` 格式、内置预设、引用解析与旧配置迁移。
- [测试用假模型与假工具](usage/llm-mock-usage.md)：`pkg/agentcore/mock` 与端到端 fakellm。
- [演示录屏](demo/README.md)：`docs/demo/suga-chat.cast` 的约 30 秒 chat 演示（演示内容、背后的假模型、重录与转 GIF 方法）。
- [ACP 调研](research/acp-protocoal-research.md)：协议参考；实现合同以源码和依赖版本为准。
- [终端与 Web 同会话调研](research/2026-10-14-web-same-session-research.md)：suga web 与终端同会话的可行性、单写者三层锁、读取最新与审批归属的现状与缺口；调研记录，实施前另出设计文档。
- [功能现状分析与可加强点](research/2026-10-16-feature-strengthening-analysis.md)：分层功能盘点与 P0–P3 可加强点（输出/成本上限、中文检索、审批一致性、chat 交互、协议与工具集缺口），逐条附代码位置；分析记录，落地前另出设计文档。
- [agentcore 分层审查](research/2026-10-16-agentcore-layering-analysis.md)：实测依赖图与分层声明的对照、守卫测试的覆盖缺口、`tasks→llm` 层次错位、文档与实现不一致与 `tools/native` 体量问题，附最小整改建议；分析记录，落地前另出设计文档。
- [pkg/chatui 组件](usage/chatui.md)：可复用的终端 chat 组件（工具行、配色、等待行、字节泵）与无 steer 宿主的最小接入。
- [agentcore](../pkg/agentcore/README.md)：应用层之下的内核与能力层（llm、tools、native、mcp、skills），分层与组装方式；接口文档见其 [docs](../pkg/agentcore/docs/README.md)。

## 历史资料

[archive/](archive/README.md) 保存早期 Agent OS、Host/Web、协议网关方案、旧实施计划与草稿。这些资料不再定义当前命令或运行方式。
