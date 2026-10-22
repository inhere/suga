# 输出契约（OutputSchema）设计 评审

## 评审目标与范围

- 目标: `docs/design/2026-10-22-output-schema-design.md`（suga 仓库，分支 `docs/output-schema-design`，候选提交 `23735ca8`）
- 修订版本: Draft 0.1
- 评审者: Claude（监督会话，未参与该设计的撰写；设计由 suga 子 agent 撰写）
- 日期: 2026-10-22
- 排除项: 二期内容（ACP `_meta`、provider 原生结构化输出、chat 提示）只核对“不影响一期”，不评审其内部完整性；不评审实现代码（尚无）。

## 输入与方法

- 全文通读候选设计（第 1 轮，全量）。
- 对照代码核实关键事实：`internal/personal/runner.go:58-60`（`PromptBuilder` 为稳定系统提示词，runner 原样发送）、`docs/design/2026-10-11-segments-projection-design.md` 的 W0/W1（前缀缓存依赖稳定的 system + tools 前缀）、`go.mod:21`（`golang.org/x/text` 已在依赖中）、`internal/cli` 与 `cmd/suga` 中退出码 6 当前未被使用。
- 依据：评审报告合同、SR1301 安全核对清单、SR1409（低暴露度：未投产的新功能、无生产数据、可廉价回滚、无出机器动作）→ 单轮合并轴评审，不建 ledger。

## 验收标准

- 设计回答任务提出的 6 个问题（入口、校验、纠正流程、会话与记录、风险与不做、分期与验证），且不传契约时行为逐字节不变；
- 不破坏 suga 已有的设计约束（稳定系统提示词与前缀缓存、`suga run` 退出码语义、SDK 公开类型不暴露 `internal/`）；
- 安全：schema 作为调用方数据处理，不加载外部资源。

## 发现

### F1 [MEDIUM] 把 schema 追加到系统提示词末尾会破坏整段会话的前缀缓存

- 证据: 设计 4.2 / 7.1 计划新增 `personal.RunRequest.SystemSuffix`，“由 prompt builder 在本轮系统提示**末尾**追加”；6.2 认为“放系统提示末尾，同一次调用内稳定，不破坏该调用的前缀缓存”。但 suga 的分段设计 D1/W1 要求系统提示词只放身份与工作约定、由 runner 原样发送（`internal/personal/runner.go:58-60`），因为请求的缓存前缀是 system → tools → 历史消息：system 末尾一变，后面全部历史都要重新计费。对 resume 的长会话（xhen 委托、gofer 反复调用同一会话）这是每次带契约调用都整段未命中缓存，而且带契约与不带契约的调用会互相把缓存冲掉。
- 影响: 设计目标（低开销地保证输出形状）不受功能性影响，但成本与延迟显著上升，并与已批准的分段/缓存设计相冲突。
- 建议: schema 改为放在**本次调用末尾的临时上下文**中：在模型请求的投影里，把契约块附加到本轮用户消息之后（或作为本轮最后一条临时上下文消息），只进入本次请求、不写入会话记录；纠正回合同样使用这一块。为空时投影逐字节不变。这样 system、tools 与历史前缀不变，缓存只在最后一段失效。实现方式（新字段名、在投影的哪一层追加）留给计划，但设计需改写 4.2、6.2、7.1 的对应表述。
- disposition: `CORE_CORRECTIVE`

### F2 [MEDIUM] `json.Unmarshal` 到 `any` 再重新编码会丢失大整数精度并改写值

- 证据: 3.1 “校验对象：`json.Unmarshal` 成 `any` 后交给 `Schema.Validate(any)`”；3.3 “校验通过后把该 JSON 值重新 `marshal` 成紧凑文本……作为 `OutputValue`”。Go 的 `json.Unmarshal` 到 `any` 会把数字变成 `float64`：超过 2^53 的整数（ID、时间戳纳秒、金额分）会被改值，`multipleOf`/`maximum` 等判定也基于被改过的值；重新 marshal 还会改键序。调用方拿到的 `OutputValue` 与模型实际输出不一致，而设计 6.4 第 2 条又承诺“不替模型修 JSON”。
- 影响: `OutputValue` 可能静默错误（数据被改），校验结论也可能不准。
- 建议: 解码时保留数字原文（`json.Decoder` + `UseNumber()`，并在计划阶段用探针确认 jsonschema v6 对 `json.Number` 的校验行为；若库自带保留数字的解码函数则用它）；`OutputValue` 直接取**提取出来的原始 JSON 片段**经 `json.Compact` 后的文本，不经过 `any` 重新编码。补一条单测：`{"id": 9007199254740993}` 原样返回。
- disposition: `CORE_CORRECTIVE`

### F3 [LOW] 纠正消息把 schema 全文写进会话历史，与“契约不落库”的表述不一致

- 证据: 4.2 的纠正消息末尾附 `<schema 原文，≤32 KiB>`，4.3 说纠正回合是 injected user entry 并落库；而 5. 与 8.7 称契约“不持久化”。结果是每次纠正都把最多 32 KiB 的 schema 永久写进会话历史并进入检索索引，后续回合的上下文也一直带着它。
- 影响: 会话体积与后续上下文成本增加，文档表述自相矛盾。
- 建议: 按 F1 的做法，schema 只在本次请求的临时上下文里出现；纠正消息只写校验错误与“请按上面的 JSON Schema 重新给出”，不重复 schema 原文。同时把 5. 的“会话里留下的东西”改成准确描述。
- disposition: `CORE_CORRECTIVE`

## 安全核对（SR1301）

- 鉴权 / 资源归属: 不适用（不新增访问面；ACP 入口在二期，且明确不接受路径）。
- 注入防护: 已核对——外部 `$ref`、未知 `$schema` 元 schema 加载都由 deny loader 拦截（3.1 已实测），不注册自定义 format；schema 中的文字可能影响模型输出，设计已说明不经过工具审批链路。
- 秘密不进日志: 已核对——trace 只记 schema 的哈希与大小，stderr 只回显路径与消息。
- 入参校验: 已核对——大小上限、顶层类型、编译失败都在创建会话前以退出码 2 拒绝。

## 覆盖缺口与限制

- 未实际运行 jsonschema v6 的探针（设计 3.1 的实测由作者完成，评审只核对其结论的合理性）；F2 建议中 v6 对 `json.Number` 的支持需在计划阶段验证。
- 二期内容只核对了与一期的边界。

## 结论

PASS（无 `CORE_BLOCKING`）。F1–F3 为 `CORE_CORRECTIVE`，建议在用户批准前修订为 0.2；不需要第二轮评审（SR1409 低暴露度，封顶一轮）。

## 剩余风险与后续

- 计划阶段需固化：临时上下文的追加位置与“为空时投影逐字节不变”的测试、`json.Number` 探针、退出码 6 写入文档与 gofer 侧说明（gofer 若用 `suga run` 解析退出码，新增码需告知）。
- 真实模型验证（设计 7.2 第 5 条）在实施交付时必须完成。
