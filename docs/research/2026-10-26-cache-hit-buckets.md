# 缓存命中率分桶口径（段内 vs 换段/折叠后）

跟踪 issue：`suga-jh7b`。本文件记录「命中率分桶」的口径、怎么读，以及一次实测数字；本步只加统计，不改请求行为。

## 为什么分桶

长会话实测回合内命中率约 88%、总体 86%，低于「段内同一 context 95%+」的目标（2026-10-26 用户结论）。单一命中率把两件事混在一起：

- 段内连续调用：请求只是在上一次之后追加，provider 的前缀缓存能命中绝大部分，这是 95% 目标针对的部分；
- 换段、压缩、折叠、换进程之后的第一批调用：请求前缀变了，本来就得重新写缓存，命中率低是结构性的。

只有分开看，才能判断「命中率低」是账要么算在结构上，要么是段内真的漏了。

## 分桶定义

口径实现在 `internal/personal/store/cachebucket.go`（`CacheCall`/`CacheBucket`），以 trace 事件为准：

| 桶 | 定义 | 判定依据 |
|---|---|---|
| `cold` | 会话的第一次模型调用（主循环） | 事件顺序：该会话第一个非 `purpose=compact` 的 model 事件 |
| `segment_start` | 换段后的第一次调用 | 上一条 model 事件的 `attrs.prefix == segment`（换段把历史替换成交接包） |
| `after_fold` | 旧工具结果折叠 / 截断 / 加载延迟工具后改变工具表 / 系统提示变化 / 其他改写之后的第一次调用 | `attrs.prefix` 为 `fold`、`system`、`tools`、`rewrite` |
| `steady` | 段内其余连续调用（请求只是上一次的追加） | `attrs.prefix == append` |
| `handoff` | 写交接包的那次调用本身（换段过程内的 side call） | `attrs.purpose == compact` |
| `resume` | 新进程续接同一段后的第一次调用（gofer 的 `suga run --resume`、评测逐轮起的进程） | `attrs.prefix == first` 但不是会话首次调用；若中间有成功的换段事件，归 `segment_start` |
| `unknown` | 判定不了的调用，计数不丢 | `attrs.prefix` 是本代码不认识的取值（不按 steady 猜，免得污染 95% 目标桶） |

补充规则：

- 旧会话没有 `attrs.prefix`（该 attr 是后加的）：退回到事件判定——上一次调用之后有成功的 `compaction(name=compact,status=ok)` 记 `segment_start`；有 `fold`、`compaction(name=truncate)`、`cache_invalidated=true` 的 `tool_search` 记 `after_fold`；其余记 `steady`。
- 被取消/失败的 compaction 不改前缀，不算换段。
- `handoff` 不占主循环的步号，也不清空「上次调用之后发生的事件」的累计。
- 判定以会话为单位：多会话（`--session X` 含子 agent，或 `stats --since` 跨会话）时每个会话各自有自己的 `cold`。

## 指标口径

沿用报告约定（与 `inspect stats` 的 MODEL 表一致）：

- `input` = 存储的 input − cache_read − cache_write（下限 0），即未命中部分；
- `prompt` = input + cache_read + cache_write；
- `hit%` = cache_read / prompt；
- provider 完全不报缓存（该桶的 cache_read 与 cache_write 都是 0）时显示 `-`，而不是 0%。

## 在哪看

- `suga inspect stats [--session ID] [--json]`：`cache_buckets` 数组，每桶 `calls`、`input_tokens`、`cache_read_tokens`、`cache_write_tokens`、`cache_hit_rate`、`cache_reported`；文本输出在 MODEL 表下面多一张 `CACHE BUCKET` 表，末行 `total` 就是总体命中率。
- `suga inspect trace <sid> --analyze [--json]`：同上，另有 `steady_low_hit`——steady 桶里单次命中率低于 90% 的前 5 次调用（`step` 序号、`uncached_input_tokens`、`prompt_tokens`、`hit_rate`、`events`：与上一次调用之间发生的 fold/segment/truncate/tools 事件，为空说明「两次调用之间什么都没发生却漏了」，那才是真正要查的 provider 侧或前缀细节问题）。
- `scripts/eval`：汇总表新增 `STEADY%` 列（与 `HIT%` 总体并列），表后一张 pooled 的 `cache by bucket` 表（含 `overall` 行）与低命中调用列表；`compare` 的 variant 总览新增 `steady 命中%` 列，并新增「按桶命中率」小节，每个 variant 一列。`results.jsonl` 里按运行记录 `cache_buckets` 与 `steady_low_hit`，`cache_buckets` 来自 `suga inspect trace <sid> --analyze --json`。

## 实测（2026-10-26）

命令（真实 provider，评测把配置与 `.env` 复制到临时 home，结束删除；不读也不打印 `.env`）：

```
go build -o tmp/eval-bin/suga ./cmd/suga
go run ./scripts/eval --bin tmp/eval-bin/suga --model opencode-go/deepseek-v4.1-flash \
  --config-from ~/.suga --task longsession-hard-rule-update --repeat 1 --timeout 15m
```

结果：任务 PASS（14 轮、3 次换段，433.4s，26 次模型调用，prompt 318.8k，cache read 231.3k）。

| 桶 | 调用 | prompt | cache read | 命中率 |
|---|---|---|---|---|
| cold | 1 | 7.7k | 0 | - |
| segment_start | 3 | 29.4k | 14.7k | 50.0% |
| steady | 9 | 122.9k | 102.4k | 83.3% |
| handoff | 3 | 16.3k | 0 | - |
| resume | 10 | 142.6k | 114.2k | 80.1% |
| **overall** | 26 | 318.8k | 231.3k | 72.6% |

steady 桶低于 90% 的 4 次调用（`inspect trace <sid> --analyze`，`step` 为段内主循环步号）：

| step | 未命中 | prompt | 命中率 | 两次调用之间的事件 |
|---|---|---|---|---|
| 3 | 10.1k | 18.0k | 43.5% | 无 |
| 12 | 4.5k | 14.1k | 68.1% | 无 |
| 19 | 1.3k | 12.6k | 89.5% | 无 |
| 20 | 3.0k | 15.6k | 80.6% | 无 |

读法与注意：

- 本次运行的 14 轮是 14 个 `suga run` 进程（评测逐轮起进程），所以每轮第一次调用都是 `prefix=first`：除会话首次那一次记 `cold`、换段后那几次记 `segment_start` 之外，其余 10 次落在 `resume`（新进程续接同一段；初版实现把它们记为 `unknown`，审核时改成单独一桶）。这 10 次实际仍命中 80.1%，说明 provider 的前缀缓存跨进程有效；单进程的长 chat 会话里这批调用会是 `steady`，对比两种跑法时要把这一点算进去。
- `unknown` 是「判定不了的计数」，不是错误；它只在出现新的 `prefix` 取值时才会有数，出现时先补归类，而不是把它当 `steady`。
- steady 桶 83.3% 低于 95% 目标，但同一次运行的 `uncached input` 归因是 `71.2k ≈ fresh 71.2k + rebuilt 0 + provider-side 14`：未命中几乎全是「上次请求之后新增的内容」（每轮新 prompt、file_read 的大段结果），而不是前缀被重写或 provider 没写回缓存。所以 steady 桶的命中率还受「单次调用携带多少新内容」影响，不能把 83.3% 直接当成前缀缓存失效；下一步应把 steady 调用按新内容比例（`uncached_fresh` / prompt）再分一层。
- `handoff` 桶（3 次写交接包）的 prompt 16.3k 全未命中，符合预期：它发的是专用提示词，不是会话。

## 下一步

- steady 桶的低命中调用按 `events` 分组看：事件为空的那批才是前缀/缓存写回的问题（写回延迟、TTL、provider 侧 round 截断）；对空事件的那批，再用 `uncached_fresh` 与新增内容对比，把「新内容多」和「真漏」分开（见上面的实测）；
- 请求前缀相关的改动（工具表、系统提示、段上下文、提醒、旧结果折叠）都要看 steady 桶的变化，而不只是总体命中率。
