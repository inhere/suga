# 长会话开销分析：`longsession-hard-naming-rules` 为什么跑了 1460 秒、249 万输入 token

日期：2026-10-22 · 数据：`tmp/eval/longsession-hard-naming-rerun`（隔离 SUGA_HOME，真实模型 `command-code/deepseek-v4.1-flash`）与同批 `tmp/eval/longsession-hard-baseline`
范围：只分析、不改 suga 的 Go 代码。

## 结论（先看这九条）

1. **249 万输入 token 里 2,142,848（86%）是缓存命中**，真正全价的新输入只有 348,134。开销的形状是「调用次数 × 上下文体积」，不是单次工具输出过大：88 次工具调用一共只产出 180 KB（176 KiB），最大的一次 64.9 KB 还被压到 8.0 KB 内联。
2. **每次调用都重复发送约 8.5k token 的固定前缀**（工具 schema 27,383 字符 ≈ 7.0k token + system 3,391 字符 ≈ 0.9k token，按实测 3.9 字符/token 折算）。84 次 turn 内调用 × 8.5k ≈ **711k prompt token，占全部 prompt 的 29%**。
3. **最贵的一轮是 t12（评测第 9 轮，`sort.Slice` vs `sort.SliceStable`）**：11 次调用、382,560 prompt、57,184 新输入、20,737 输出、135 s，占全价等价开销的 18%。原因是模型自选策略——它读了 Go 标准库 `sort/sort.go`（11.5 KB）、`sort/slice.go`、`slices/sort.go`（grep 13.7 KB），写了 2 个脚本、5 次 write/edit，又 4 次改跑 demo。
4. **t10 那 456 s（占总墙钟 31%）不是任务成本**：command-code 的 443 端口两次 dial timeout，重试 4 次 / 2 次（178 s + 178 s）后降级到 `opencode-go`；降级后缓存是冷的（37,714 新输入）。日志见 `home/logs/suga-20261022.log`。
5. **5 次 nudge（`unverified-write`）里有 4 次是误报**：规则的内置 `check_command` 只认 `go test|vet|build`、`pytest`、`npm test` 这类命令，不认这次实际用的 `python tmp/xxx.py`、`go run xxx.go`。按 trace 重放（见第五节）：4 次误报的代价是 8 次额外调用、298k prompt（12%）、48 s；第 5 次（t18 写 `notes_index.md` 后只列了目录）算真缺口。
6. **3 次换段（compaction）花掉 68,752 新输入 + 37,663 输出（占总输出 25%）+ 187 s（13% 墙钟）**。交接包 4,747 → 7,793 → 16,018 字符单调增长；第三份 ≈16 KB ≈ 5.8k token，此后 19 次调用每次都带着它（≈110k prompt token）。
7. **换段没有让模型丢掉需要的信息**：三份交接包都原文保留了第 1 轮的两条约束（「小写下划线」「vendor/ 只读」）和 `src/` 的符号/行号表；换段后首轮重读文件只有 2 次、每次 1.5–3.2 KB（≈400–900 token），不是开销主因。
8. **与另三项的差异主要来自任务设计 + 模型自选策略**：naming 用 14 轮里有 12 轮要求开放式长答、还要"给出行号依据"，把模型推成「读标准库源码 + 写 demo 反复验证」；rule-update/distractor 同样 13–14 轮却只有 5/13 次工具调用。suga 侧的放大项是固定前缀、nudge 误报、换段成本。
9. **另三项的耗时也不全是任务工作**：rule-update 的 273 s 里 100 s（37%）是 3 次换段，14 轮本身只 171 s；而且它 61% 的输入 token 是固定前缀（18 次 turn 调用 × 8.4k）。

## 一、口径与数据来源

| 数据 | 来源 | 用途 |
| --- | --- | --- |
| 每次模型调用的 prompt / 新输入 / 缓存 / 输出 / 状态 / 耗时 | 会话库 `home/db/sessions.db` 的 `events`（`kind='model'`，`attrs_json` 里有 `prefix=first/append`、`tool_calls`、`finish`） | 每轮、每次调用 |
| 每轮上下文构成（system / tools / history / handoff / est_tokens） | 同库 `events` 的 `kind='context'`（每轮开始一条） | 固定前缀、交接包体积 |
| 换段明细（tokens_before/after、摘要长度、丢弃消息数） | `kind='compaction'` 事件 + `entries` 里 `type='compaction'` 的 JSON | 换段成本与内容 |
| 总账（calls / input / cache / output / duration） | `results.jsonl`（评测器 `suga inspect` 汇总） | 交叉核对 |

口径定义：`prompt` = 该次调用实际发送的上下文总 token（provider 的 prompt_tokens，含命中部分）；`新输入(uncached)` = prompt − cache_read；`cache` = 命中的前缀 token；`billed-eq` = 新输入 + 0.1 × cache + 输出（按 deepseek 缓存读约 1/10 价折算的**相对**成本，不是真实账单——网关不返回价格，`results.jsonl` 的 `cost_usd` 恒为 0）。

**核对**（这样可以确认表中的拆分覆盖了全部账单行）：

```
turn 内 84 次调用：prompt 2,422,230 = 新输入 279,382 + 缓存 2,142,848
换段 3 次调用：    prompt    68,752 = 新输入  68,752 + 缓存 0
合计：             2,490,982 = 348,134 + 2,142,848   ← results.jsonl: input_tokens 348134 + cache_read 2142848
输出：112,975 + 37,663 = 150,638                     ← results.jsonl: output_tokens 150638
墙钟：1,272 s（turn）+ 187 s（换段）= 1,459 s        ← results.jsonl: duration_ms 1460356
```

`calls: 87` = 84 次 turn 内调用 + 3 次换段调用；`tool_calls: 88` 与 `events` 里 88 条 `kind='tool'` 一致。

## 二、每轮明细（19 个 DB turn = 14 个 prompt turn + 5 个 nudge 注入 turn）

「对应 prompt」是被 nudge 注入的额外 turn 的归属：一次 `suga run` = 一个 prompt turn，nudge 推回的 turn 落在同一个 `Run` 里，所以评测器算作同一轮（例如评测第 2 轮 = DB t2 + t3）。

| DB turn | 对应 prompt | 模型调用 | 工具调用(写/命令) | prompt | 新输入 | 缓存 | 输出 | billed-eq | 墙钟 s |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| t1 | p1 复述约束 | 4 | 3 (0/2) | 36,302 | 5,838 | 30,464 | 1,612 | 10,496 | 31 |
| t2 | p2 读 3000 行日志 | 7 | 6 (1/5) | 99,145 | 9,033 | 90,112 | 4,429 | 22,473 | 40 |
| t3 | ↑ nudge 注入 | 3 | 2 (1/1) | 62,360 | 4,120 | 58,240 | 1,735 | 11,679 | 17 |
| t4 | p3 读 src/*.go | 4 | 3 (0/1) | 95,269 | 4,261 | 91,008 | 6,166 | 19,527 | 47 |
| **t5** | p4 时间单调时钟 | 8 | 10 (2/3) | 141,379 | 33,603 | 107,776 | 12,676 | **57,056** | 89 |
| t6 | ↑ nudge 注入 | 2 | 1 (0/1) | 57,275 | 9,787 | 47,488 | 1,120 | 15,655 | 14 |
| t7 | p5 列 order.go 签名 | 2 | 7 (0/0) | 61,920 | 3,808 | 58,112 | 4,045 | 13,664 | 29 |
| t8 | p6 日志 1000–1100 行 | 4 | 3 (1/1) | 152,039 | 7,911 | 144,128 | 2,881 | 25,204 | 29 |
| t9 | ↑ nudge 注入 | 2 | 1 (0/1) | 82,215 | 2,855 | 79,360 | 587 | 11,378 | 10 |
| **t10** | p7 日志按天/按小时切分 | 4 | 1 (0/1) | 86,944 | 41,120 | 45,824 | 9,815 | 55,517 | **456** |
| t11 | p8 vendor 依赖 | 6 | 9 (0/2) | 85,060 | 9,156 | 75,904 | 5,994 | 22,740 | 49 |
| **t12** | p9 sort.Slice vs SliceStable | 11 | 15 (5/5) | 382,560 | 57,184 | 325,376 | 20,737 | **110,458** | 135 |
| **t13** | p10 store/util 拆分 | 5 | 5 (2/2) | 277,510 | 11,142 | 266,368 | 11,648 | 49,426 | 86 |
| t14 | p11 幂等键设计 | 3 | 6 (0/1) | 196,987 | 10,875 | 186,112 | 8,290 | 37,776 | 58 |
| t15 | p12 UTC 统一 | 7 | 7 (3/3) | 161,460 | 19,636 | 141,824 | 11,028 | 44,846 | 87 |
| t16 | p13 回到第 1 轮约束 | 5 | 4 (2/2) | 165,085 | 10,205 | 154,880 | 5,709 | 31,402 | 47 |
| t17 | ↑ nudge 注入 | 2 | 1 (0/1) | 74,999 | 3,063 | 71,936 | 1,004 | 11,260 | 13 |
| t18 | p14 动手写文件 | 3 | 3 (2/1) | 119,982 | 34,478 | 85,504 | 2,655 | 45,683 | 23 |
| t19 | ↑ nudge 注入 | 2 | 1 (0/1) | 83,739 | 1,307 | 82,432 | 844 | 10,394 | 12 |
| 合计 | | 84 | 88 (19/34) | 2,422,230 | 279,382 | 2,142,848 | 112,975 | 606,634 | 1,272 |
| 换段 ×3（不计入上表） | | 3 | 0 | 68,752 | 68,752 | 0 | 37,663 | 106,415 | 187 |

换段发生在 t4、t10、t14 之后（`compact_after: [3, 7, 11]` 对应评测第 3、7、11 轮）。

### 成本构成（近似拆分）

| 组成 | token | 占 prompt | 怎么算出来的 |
| --- | --- | --- | --- |
| 固定前缀（system + 20 个工具 schema） | ≈711k | 29% | 首轮 `est_tokens` 8,464（history=0）× 84 次调用 |
| 交接包常驻 | ≈227k | 9% | 三段 handoff 7,493 / 11,695 / 22,756 字符 ÷ 3.9 × 各段内调用数（22 / 25 / 19） |
| 历史与工具结果 | ≈1.48M | 61% | 余量 |
| 换段（另计） | 68,752 新输入 + 37,663 输出 | — | 3 次 compaction 的 model 事件 |

### 哪几轮最贵，为什么

- **t12（p9，110,458 billed-eq，18%）**：11 次调用里 15 次工具。模型为了"证明" `sort.Slice` 不稳定，读了 `sort/sort.go`（11,546 B）、`sort/slice.go`（2,637 B）、grep 了 `sort` 包（13,744 B，唯一一次 spill），写了 `tmp/analyze_timestamp_ties.py`、`tmp/sort_stable_demo.go`，然后 4 次改、跑 demo，还 grep 了 `slices/sort.go`（4,418 B）。每次改跑都是一次全量上下文重发（最后 4 次 prompt 已是 38k–48k），所以一轮就吃掉 382k prompt。
- **t10（p7，456 s）**：`command-code` 443 端口两次 dial timeout，按 4 次 / 2 次尝试重试（178 s + 178 s），随后降级 `opencode-go`（首调缓存冷，37,714 新输入）。这一轮几乎是纯环境成本；顺带把「日志按天/按小时切分」这道纯论述题变成了 456 s。日志原文：`dial tcp 104.21.49.202:443: i/o timeout`、`command-code request failed after 4 attempts`。
- **t5（p4，57,056 billed-eq）**：8 次调用 / 10 次工具。这是**换段后的第一轮**：首调 11,182 prompt 只命中 8,064 缓存；中途第 6 次调用缓存从 18,816 掉到 4,096（新输入 19,912），因为读了 Go 工具链 `time/time.go`（3,208 B）+ grep `time` 包（6,520 B）——工具结果本身不大，但前缀变了就整段重新计费。所以这轮的 33.6k 新输入里，相当一部分是缓存失效而非新信息。
- **t13 / t14 / t15（49k / 38k / 45k）**：后段每轮的历史已经到 50k–60k prompt，两三次调用就顶上前期一整轮；t13+t14 的 8 次调用里 4 次是写 demo 并跑（`idem_demo.go`、比较 store/util 行数），t15 又重写了 `utc_display_demo.go` 并改两次、跑两次。
- **t18（p14，45,683 billed-eq）**：只有 3 次调用，但首调 38,680 prompt 只命中 8,448 缓存（22%），30,232 个新输入。前后没有换段、t17 结束时 prompt 已经是 38k，所以这是 **provider 侧缓存失效**（DeepSeek 的自动前缀缓存是按节点/窗口的，suga 感知不到），不是 suga 主动丢弃了什么。
- **t2 / t8（读大文件）**：t2 里 `python tmp/analyze_orders.py` 一次吐出 64,952 B，被内联截到 7,960 B（占 12%），工具输出体积控制是有效的；t8 读 `data/orders.log` 只取了 7,194 B。

### 五个常见假设，逐个对账

| 假设 | 结论 | 证据 |
| --- | --- | --- |
| 工具输出过大 | **不是主因** | 88 次调用共 180,389 B（176 KiB），内联后 113,926 B；最大的一次 `python tmp/analyze_orders.py` 64,952 B 被截到 7,960 B，只有 1 次 spill（`file_grep` 13,744→4,273 B）。 |
| 反复读同一个文件 | **存在但很便宜** | `src/order.go` 被读 5 次（t4/t5/t7/t12/t15），单次 1,550 B；重复读取总量 <20 KB，不到全部工具输出的 11%。 |
| 换段后又把内容读回来 | **只有 2 次，≈1.3k token** | t5 重读 `order.go`（1,550 B）、t15 重读三个 src 文件（3,176 B）；交接包里已有符号/行号表（第三节）。 |
| 任务本身要求多 | **是主要成本来源之一** | 14 个 prompt 里 12 个要求读文件/给行号/写两段以上；仅 p2/p6/p9 三个"读+统计"轮（t2/t8/t12）就贡献 24 次工具调用。 |
| 模型来回试错 | **是主要成本来源之一** | 19 次写工具调用 = 9 个 tmp 脚本 + 2 个交付文件（`notes/*.md`），34 次 shell；t12 一轮就 4 次"改 demo→跑"，t15/t16/t18 各 2–3 次。每次试错都是一次全量上下文重发。 |

## 三、换段是否让模型丢了信息、从而重读

### 三次换段的实际开销

| 段 | 触发点 | tokens before → after | 丢弃消息 | 摘要长度 | 换段模型调用 | 换段耗时 |
| --- | --- | --- | --- | --- | --- | --- |
| 2 | t4 后 | 24,479 → 8,121 | 36 | 4,747 字符 | prompt 13,572 / 输出 7,166 | 39 s |
| 3 | t10 后 | 47,958 → 9,313 | 61 | 7,793 字符 | prompt 21,287 / 输出 8,821 | 44 s |
| 4 | t14 后 | 66,866 → 12,418 | 79 | 16,018 字符 | prompt 33,893 / 输出 21,676 | 102 s |

第三次换段最贵：要把前一份 7.8 KB 的包折叠进新包，输出 21,676 token（占全部输出 14%）、耗时 102 s，`tokens_after` 也因此从 8–9k 抬到 12.4k。`internal/personal/compact.go` 的 `summarize` 注释明确写着 "No MaxTokens on purpose: a summary is short by nature"——在英文短会话里成立，在这种中文长会话里不成立。

### 换段后第一轮的工具调用（证据）

| 换段 | 下一轮 | 首轮工具调用 | 是重读吗 | 代价 |
| --- | --- | --- | --- | --- |
| 段 2（t4 后） | t5 / p4 | `file_read src/order.go` | 是（t4 已整篇读过），但 p4 本身要求"读 src/order.go 列签名" | 1,550 B ≈ 400 token |
| 段 3（t10 后） | t11 / p8 | `file_read vendor/VENDORED.md + lib_util.go` | 否，第一次读 vendor | — |
| 段 4（t14 后） | t15 / p12 | `file_read src/{order,util,store}.go` | 是（t13/t14 读过 store/util，t4/t5/t7/t12 读过 order） | 3,176 B ≈ 810 token |

### 结论：没有证据表明换段丢了必要信息

- 三份交接包的「采纳与否决」章节都原文保留了两条硬约束（`小写下划线`、`vendor/ 只读`，第三次换段时 `recent_requests` 已经不含第 1 轮原文，但摘要里仍在）——而这两条正是这个任务的全部检查项（第 13、14 轮的 answer 检查 + `vendor/` 的 `file_equals`）。
- 交接包还带了 `src/` 的符号表与行号（`Order` 第 10 行、`Parse` 第 22 行……），所以第 13 轮"比较两个文件的行数与函数个数"时模型重读 1.6 KB 就能确认，而不是从零再探索。
- 两次重读合计不到 1.3k token，占全部 prompt 的 0.05%。**换段的真实成本是"写摘要"（106k billed-eq、187 s）和"摘要常驻"（≈227k prompt token），不是"丢信息导致重读"。**

## 四、与另三项对比：差异来自任务设计还是 suga

| 任务 | prompt 轮 | DB turn | 模型调用 | 工具调用 | prompt | 新输入 | 缓存命中 | 输出 | 输出里换段占比 | 换段(次/墙钟) | 总墙钟 | billed-eq |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| naming-rules | 14 | 19 | 87 | 88 | 2,490,982 | 348,134 | 86.0% | 150,638 | 25% | 3 / 187 s | 1,460 s | 713,056 |
| topic-cycles | 15 | 15 | 45 | 36 | 611,442 | 88,946 | 85.5% | 44,545 | 50% | 3 / 117 s | 754 s | 185,740 |
| distractor-rules | 13 | 13 | 25 | 13 | 371,628 | 52,652 | 85.8% | 36,937 | 22% | 2 / 48 s | 324 s | 121,486 |
| rule-update | 14 | 14 | 21 | 5 | 247,658 | 51,818 | 79.1% | 30,846 | 54% | 3 / 100 s | 273 s | 102,248 |

（上表含换段调用，格式为 prompt/输出：naming 3 次 68,752/37,663、topic-cycles 3 次 27,347/22,163、distractor 2 次 11,889/8,046、rule-update 3 次 14,961/16,720。）

**任务设计造成的差异（大头）**

1. **工具调用的数量级**：naming 88 次 vs rule-update 5 次。naming 的 12 个中间轮是"读 3000 行日志给行号依据""读三个 .go 文件每个至少两段""完整读取 log 1000–1100 行统计"这类**要求可核验证据**的任务，模型自然会去读文件、写脚本；rule-update 的中间轮几乎全是纯论述（"解释指数退避""说明 mutex 与 channel"），不需要碰工作区。
2. **模型自选策略放大**：naming 里同一批文件被读了 5 次（`src/order.go` 在 t4/t5/t7/t12/t15），还额外读了 Go 标准库（`sort`、`slices`、`time`）+ 写了 9 个 tmp 脚本并反复改、跑（t2/t5/t8/t12/t13/t15/t16 七个 turn）。这既不是任务要求的，也不是 suga 要求的——是"给依据"这种要求 + 模型偏好"实测证明"的结果。rule-update 一次都没写文件，所以也没触发 tool 输出。
3. **输出量**：naming 150,638 输出 token，是 distractor（36,937）的 4 倍。三个任务的平均回答篇幅其实相近，多出来的是 5 个 nudge turn 的自我辩解（"这是误报"）+ 3 次换段摘要（37,663）。
4. **强制换段的相对成本**：rule-update 的 3 次换段要 100 s，比它 14 轮本身（171 s）的一半还多，因为它 `tokens_before` 只有 10–14k，摘要却要"整篇重写并折叠上一份包"——**换段成本几乎与段长无关，短会话里换段就是纯开销**。更直观的一组数：rule-update 的 30,846 输出 token 里有 16,720（54%）是换段摘要，topic-cycles 是 22,163/44,545（50%）。

**suga 行为造成的放大**

1. 固定前缀：naming 29% 的 prompt 是固定部分，rule-update/distractor/topic-cycles 分别是 65% / 54% / 60%——便宜的任务里，输入 token 大头其实是工具 schema 和 system。
2. nudge 误报：只有 naming（和 topic-cycles 1 次，t8 后）产生了 nudge，因为只有它们写文件；判据是命令白名单，见下一节第 1 条。
3. 缓存失效不可见：t18 的 22% 命中率、t5 中途掉到 4,096 都只体现在 events 里，`results.jsonl` 只有总命中率 86%，看不出"哪里贵"。

## 五、可改进点（按收益/风险排序）

| # | 归属 | 改哪里 | 预期节省 | 风险 | 怎么验证 |
| --- | --- | --- | --- | --- | --- |
| 1 | suga | `internal/personal/nudge_rules.go:58` `builtinCheckCommand` + `internal/personal/nudge.go:110` `uncheckedWrite` | 4 次误报 → 8 次调用 / 298k prompt（12%）/ 48 s | 低：把 `python <脚本>`、`go run`、`node <脚本>`、`pwsh -File` 加进白名单（不要放宽成"任意 shell"，否则 t18 那个真缺口也不再提醒） | 同一任务 `nudge` 事件 5 → 1，`calls` 87 → ~79；`scripts/eval --task longsession-hard-naming-rules` 检查项仍全过 |
| 2 | suga | `internal/personal/compact.go:515 summarize` / `:546`（"No MaxTokens on purpose"）/ `:379` 折叠上一份包 | 换段输出 37.7k → ~20k；常驻交接包 5.8k → ~3k token/次，后 19 次调用省 ≈55k prompt | 中：摘要变短可能丢约束（本任务检查的就是约束），必须保留「采纳与否决/硬约束」章节原文，并对四个 hard 任务回归 | 换段事件的 `tokens_after`（8.1k/9.3k/12.4k → 全 <8k）、换段 model 事件输出 token、t15–t19 的 prompt 之和；四项通过率不得下降 |
| 3 | 评测 | `scripts/eval/testdata/tasks/longsession-hard-naming-rules/task.yaml` 的 p4/p9/p10/p11/p12 | calls 87 → ~45、新输入 348k → ~200k、墙钟 1460 s → ~600 s（去掉 t12 那类自选验证） | 中：降低"给依据"的要求会削弱"长会话上下文压力"，应保留 2 个读大文件的轮次（p2/p6）撑上下文 | 同任务 `calls`/`uncached`/`duration_ms` 下降，且 14 轮检查项（含第 13/14 轮的约束记忆）仍全过 |
| 4 | suga | 工具装配（`context` 事件的 `tools` 27,383 字符；`deferred_tools` 这次恒为 0） | 固定前缀 8.5k → 5k 量级，84 次调用省 ≈290k prompt（多为缓存命中，billed 侧省 ≈30k） | 中高：工具集变化会重建前缀缓存，必须只在换段边界改；模型可能"不知道有某工具" | `context` 事件的 `tools` 字符数、`uncached` 总量；四项评测 + `tool-schema-nonempty-properties` 类测试 |
| 5 | suga | 重试/降级策略（这次单次失败 ≈178 s 才降级） | 故障场景下 456 s → 100 s 量级 | 低：只调预算（尝试次数/单次超时），不动正常路径 | 注入网络故障重跑 p7 一类轮次，看 `duration_ms`；正常轮次不变 |
| 6 | 评测 | 每轮一个独立 `suga run`，使 nudge 的 per-run 预算（`internal/cli/nudge.go:26` `maxNudgeUnverifiedPushes = 1`）每轮重置 | 若改常驻 chat 或按 session 计预算，nudge 最多 1 次 | 低 | `suga trace --json` 里 nudge 事件数；同会话多次 run 的计数 |
| 7 | 评测 | `compact_after` 用固定轮次而非 token 预算（rule-update 在 10–14k token 处强制换段） | rule-update 省 ≈100 s（37%） | 低（但会改变"多次换段"这个被测行为，需要另留一项压缩测试） | 换段次数与墙钟；`tokens_before` 是否达到阈值 |
| 8 | suga | 交接包里的「坐标」章节改成结构化 refs（`compact.go` 的 `refCandidate`/`refs` 已有基础设施），正文只留指针 | 第三份摘要 16 KB → 更小；同时保住"可按需 file_read" | 中：模型可能不查而猜 | 摘要字符数、换段后首轮是否主动读 refs、四项通过率 |
| 9 | 评测 | `results.jsonl` 单列 compaction 与 provider 故障耗时 | 0（只改可解释性） | 无 | 报告里 `duration_ms` 能被拆成 turn / compaction / 故障三块 |

### 展开说明（前三条）

**1. nudge 的误报是这次最"便宜可修"的一处。** 规则的本意是"改完要验证"，但内置 `check_command` 是 `(go +(test|vet|build)|make +|npm +(test|run)|…|pytest|python +-m +pytest|…)`。这次的验证方式是 `python tmp/analyze_orders.py`、`go run .\tmp\mono_clock_demo.go`、`gofmt -l`——**都在白名单外**，于是：

| nudge | 最后一次写 | 之后实际跑了什么 | 判定 |
| --- | --- | --- | --- |
| t2 后 | `tmp/analyze_orders.py` | `python tmp/analyze_orders.py`（并有 4 次核对） | 误报 |
| t5 后 | `tmp/mono_clock_demo.go` | `go run .\tmp\mono_clock_demo.go`（不匹配白名单；nudge 后 t6 才补跑 `gofmt; go vet; go run`） | 误报 |
| t8 后 | `tmp/check_orders_1000_1100.py` | `python tmp/check_orders_1000_1100.py` | 误报 |
| t16 后 | `tmp/verify_vendor_baseline.py` | `python .\tmp\verify_vendor_baseline.py`（还跑了两次） | 误报 |
| t18 后 | `notes_index.md` | `Get-ChildItem` / `Get-Content`（真正的内容校验是 nudge 之后的 t19 才跑的 `Get-FileHash`） | 真缺口（不算误报） |

**这条表是把 trace 按内置规则重放出来的**（脚本用同一份 `builtinCheckCommand` 正则匹配"最后一次成功写入之后"的每条成功 `shell_exec`）：5 次全部命中 `FIRES`；若把判据放宽成"写之后有任何成功 shell"，5 次全部不再触发——包括 t18 那个真缺口。所以正确改法是**扩展白名单**（解释器跑工作区脚本算验证），而不是取消命令匹配。

**2. 交接包该有输出预算。** 现状：`tokens_after` 8,121 → 9,313 → 12,418（越换越大），摘要 4.7 → 7.8 → 16.0 KB，第三次输出 21,676 token。任何"长期会话"都会撞上这个单调增长：每次折叠都把上一份包整篇带进来。建议 `summarize` 给模板各章节一个预算（如「坐标」≤2 KB、「背景与结论」≤4 KB），并在折叠时按 `refs` 去重旧坐标；硬约束/规则章节保持原文不动（这正是评测的检查项）。

**3. 任务侧的"给依据"会诱发自我验证循环。** p9（`sort.Slice` vs `SliceStable`）本来是一道论述题，但任务模板里要求"给出一段你推荐的稳定排序写法"+"实测"，模型就去读标准库、写 demo、跑 4 次。如果这项评测的目标是"长会话里的信息保持"，论述轮的验收可以只看"是否引用了前面轮次的结论"，而不要求模型去实测——那 88 次工具调用能砍掉一半。

## 六、没做到 / 未验证

- **单次运行的归因**：只分析了这一批 4 个任务各一次运行，没有重跑命名任务做对照（需要联网真实模型），所以结论是"这一次为什么贵"，不是统计分布。
- **billed-eq 是相对量**：网关不返回价格（`cost_usd: 0`），缓存读按 1/10 价折算，只用于排序，不能当账单。
- **t18 的缓存失效原因未定**：38,680 prompt 只命中 8,448，前后没有换段；可能是 provider 节点/窗口差异，也可能是前缀里某处被重建（`prefix=first` 且 `prefix_reused=0`），没有在 provider 侧核实。
- **改进项的节省都是估算**：没有改代码/改任务后重跑，只给了"哪个评测数字应该变"。
- **Windows 主机环境特性**：t10 的 `dial tcp ... i/o timeout` 是本机网络/代理问题，Linux 容器里可能不复现。

## 发现但不碰

- `scripts/eval` 的 `results.jsonl` 只存汇总，没有 per-call usage；这次能拆到"每一次调用"全靠会话库 `events` 表，分析入口不直观。
- `compaction` 事件的 `requests` 字段是"交接包里包含的最近用户请求条数"，不是模型请求数（初读容易误解为换段做了 3 次模型调用）。
- 评测的 `turns[]` 把 nudge 注入 turn 并进同一个 prompt turn，导致 `checks.turn` 的编号与 DB turn 编号错位（p14 的检查落在 DB t18+t19 上）。
- `internal/personal/compact.go:546` 注释 "No MaxTokens on purpose" 在中文长会话里代价被低估（见第五节第 2 条）。
- `context` 事件的 `system/tools/history/handoff` 字段是字符数，`est_tokens` 才是 token 估计，两者混读会算错 4 倍。
