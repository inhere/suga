# Codex harness / 上下文 / 记忆文章对照（suga 可借鉴点）

> 日期：2026-10-27 · 来源：WP `local/wechat_article_codex_harness_context_memory.md`（公众号二手解读，未读 Codex 源码）· 子 agent 只读分析，监督整理

## 结论

文章大部分机制 suga 已有对应实现（分层守卫、ACP/sugasdk、回合循环、只读工具并发预取、分段+结构化交接包、从最早消息折叠、session_search/recall）。值得借鉴的集中在两处：World State 的“带稳定标识的分节快照 + 差量”，以及 Token Budget 的“模型自管笔记 + 小便签”。

## 建议（按价值）

1. **持久化上次请求指纹，给续接桶算前缀复用率**（直接服务 suga-jh7b 第二步）：`internal/personal/prefix.go` 的 `prefixTracker` 只在进程内存里，新进程第一调一律 `prefixFirst`。回合结束把最后一次请求的指纹（system/tools 哈希、消息累计哈希与大小，长段设上限）存进 session meta 或 model 事件，续接时比较，拆出 `resume_same` / `resume_changed`。
2. **分桶识别 provider/模型变化**（直接服务 jh7b）：pool 降级与 `:model` 切换后请求字节只是追加，会被记成 steady，拉低段内命中；新增 `provider_switch` 桶。前提：核实 model 事件记录的是实际服务的 provider。
3. **段上下文按变化频率排序，节带稳定 ID**（可能改善换段后 50%）：`segment.go` `renderSegmentParts` 的顺序把记忆索引放在第三位，换段时一变就让后面的 skills 等失效；把最易变的节（记忆索引、延迟工具列表）挪到交接包前、靠末尾。先用第 1 条的复用数据实测，不能凭推断上线。
4. **写交接包的调用复用会话前缀**：`compact.go` `summaryCall` 用独立系统提示并重渲染对话，handoff 桶命中为 0；改为沿用本段前缀，末尾追加“请写交接包”。中等改动，需防模型调工具、长历史截断，用 4 个 hard 评测回归。（文章引申，非原文）
5. **模型自管工作笔记，交接包确定部分由代码拼**：大改动；建议先做“确定性部分由代码拼”。
6. **context_update 改为差量**：与设计 Q3 决议冲突，需重新决策。
7. **记忆使用计数与衰减**：中等改动；索引只允许换段时稳定重排。
8. **fork 式子 agent（继承最近 N 轮）**：低优先级，前缀不同拿不到缓存收益。

## 不建议

arg0 多入口；按 crate 细拆；服务端托管 Notes（与本地优先冲突，要做就落本地）；换模型强制压缩；另起 Op/Event 协议层；自动从会话提取长期记忆（与记忆设计 D7 taint 关口和非目标冲突）。

## 未核实

文章所述 Token Budget、约 4KB Thread Hint、Notes/History 工具、引用计数 Top-N、继承最近 3 轮、World State 差量格式、压缩是否复用前缀均未经源码核实；第 2 条依赖的 model 事件字段语义、第 3 条“记忆索引导致 segment_start 低”的推断、第 4 条在 DeepSeek 网关上的缓存收益都未验证。
