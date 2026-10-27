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

## 第二步：续接指纹、新桶与前缀复用率（2026-10-27）

第一步的实测暴露了两个口径问题：逐轮起进程时每次第一次调用都是 `prefix=first`（落进 `resume` 一桶），分不清「续接且前缀没变」和「续接但前缀被改写」；以及命中率把「本次新增了多少内容」也算进去，段内 83.3% 没法判断是缓存漏了还是新内容多。第二步只加统计，**发给 provider 的请求逐字节不变**（`observe_request_bytes_test.go` 锁住这一点）。

### 持久化上次请求指纹

回合结束时（`Runner.finish`）把本会话最后一次请求的指纹写进会话元数据 `cache_prefix_print`（`prefix.go` 的 `printRecord`）：系统提示与工具表的 sha256 与字节数、每条消息的累计 sha256（含角色与 call id，用于 fold/segment 判定）与 JSON 字节数。取舍：长会话只存最后 **64 条**消息与消息总数——累计哈希链住了整个请求，所以第一条被保留消息的累计哈希就代表了被丢掉的那一段，续接进程仍能区分「只追加」与「更早某条变了」；但若被保留窗口的第一条就对不上，就无法定位是哪条变的（只记 `rewrite` 原因、无位置）。

新进程续接的第一次调用（`prefixTracker.seed`，在 `Runner.Run` 用 `markRunning` 后的元数据注入）与这条指纹比较，模型事件上写 `prefix_resume`（`same`/`changed`）与 `prefix_resume_reason`（`system`/`tools`/`fold`/`rewrite`）；进程内已有的请求优先于指纹。

选「会话元数据」而不是「模型事件属性」的原因：指纹要在**下一次请求之前**读到（事件表只能事后读），而元数据随会话一起加载、已有 `PatchSessionMeta` 原子写入；代价是每次加载会话多带几 KB（64 条消息的哈希与字节数）。

### 新桶

| 桶 | 定义 |
|---|---|
| `resume_same` | 续接后的第一次调用，指纹是本次请求的前缀（只追加） |
| `resume_changed` | 续接后的第一次调用，前缀有变（`reasons` 记原因） |
| `provider_switch` | 与上一次主循环调用相比实际服务的 model/provider 变了、pool 降级（`attrs.fallback`）、或中间有 `model_switch` 会话事件 |
| `resume` | 旧数据（没有指纹）的续接首调，字段名不变 |

`attrs.model` 记的是**这次尝试实际服务的模型**：`attemptObserver` 取 `llm.Attempt.Model`（`pkg/agentcore/llm/observe.go:110` 的 `report`），Pool 每次降级都单独调用 `GenerateObserved/StreamObserved`（`pkg/agentcore/llm/pool.go:96`、`:160`），所以每个尝试事件的 `model` 是自己那次的服务模型，provider 不暴露模型时才回落到配置里的 model。

判定优先级：会话首次调用 `cold` → 换段（`prefix=segment` 或其间有成功的 `compact`）`segment_start` → `provider_switch` → `resume_same`/`resume_changed` → `prefix=first` 的 `resume` → `fold`/`system`/`tools`/`rewrite` 的 `after_fold` → `append` 的 `steady`。换段优先：它才是那次低命中的结构性原因。

### 失败尝试不进桶（2026-10-27）

没有可用用量的尝试（`status` 非 `ok`，或 provider 未报告 usage）不进任何桶的 CALLS 与命中率：它没有自己的 cache 读写，混进 `provider_switch` 之类的桶只会抬高调用数、拉低命中率。第四十五批 E2 实测就是这样：pool 降级前那次 `status=error`、usage 全 0、`attrs.fallback="next, provider_transient (connect_failed)"` 的尝试被记成 `provider_switch` 的一次调用（prompt 0、`cache_reported=false`），而降级成功的那次被算进 `steady`。

修正后：这类尝试单独计数为 `failed_attempts`（`inspect stats` 文本表在 total 下一行、`--json` 字段 `failed_attempts`；`inspect trace --analyze` 的 `failed attempts` 行与同名字段；`scripts/eval` 汇总的 `failed attempts` 行），为 0 时不显示；它带的 `attrs.fallback` 仍然算数，紧随其后的那次可用调用归 `provider_switch`（即真正换了 provider 的那次）。步号仍与 `--analyze` 的主循环模型调用序号一致（失败尝试在那个视图里也是一步）。

### 前缀复用率

```
prefix_tokens = 本次 prompt token × reused 字节 / (reused+rebuilt+fresh 字节)
复用率        = min(cache_read, prefix_tokens) / prefix_tokens
```

- 分子分母都是 token：`reused` 是本次请求中与上次完全一致的前缀（含系统提示与工具表）的字节数，按本次请求「报告 token / 请求总字节」折算，保守地假设缓存只在这段前缀里命中；
- 分母只算「上次请求里可被缓存的前缀」，本次新增内容不进分母，所以复用率不受新内容多少影响；
- 上限截到 100%：字节→token 只是推算，provider 报得更多不当作超过全部。

稳态目标（95%+）改用复用率衡量：命中率低但复用率高，说明只是这次新增内容多；两者都低才是缓存没命中。

### 换段分节哈希

换段事件新增 `sections`：新段上下文消息每一节（`environment`、`rules`、`memory_index`、`skills`、`append_prompt`、`host_context`、`deferred_tools`，以及交接包 `handoff`）的名字、字节数与 16 位 sha256。`--analyze` 的 `segment sections` / `segment_sections` 给出连续两段之间**哪些节变了**（被移除的节也算），这是后面判断「段上下文是否要按变化频率重排」的数据；本轮不动顺序。

### 在哪看

- 事件：模型事件的 `prefix_resume`/`prefix_resume_reason`/`prefix_reused`/`prefix_rebuilt`/`prefix_fresh`，换段事件的 `sections`；
- `suga inspect stats`：CACHE BUCKET 表新增 `REUSE%` 列（`prefix_reused_tokens`、`prefix_reuse_rate` 同时在 `--json` 里）；
- `suga inspect trace --analyze`：`prefix reuse` 与 `segment sections` 行；JSON 新增 `prefix_reused_tokens`、`prefix_reuse_rate`、`prefix_reuse_read_tokens`、`segment_sections`；每个桶另有 `reasons`；
- `scripts/eval`：按桶汇总表新增 `REUSE%`，`compare.md` 的总览新增 `steady 复用%` 列并追加「按桶前缀复用率」小节。

本步**没有跑真实模型**（下一步做），也没有在任何真实会话上取数：新增的桶与复用率目前只有单元测试覆盖。

## 第四十六批：写交接包的调用沿用会话前缀（本设计）

上面「写交接包的调用几乎不命中」是本节最大的可省部分；换段交接包改成沿用本段最后一次主循环请求（只多末尾一条指令）的方案、回退条件与验收口径见 `docs/design/2026-10-27-handoff-prefix-reuse-design.md`，新的计数看 `inspect trace --analyze` 的 `handoff reuse` 行与 `scripts/eval` 汇总里的交接包调用统计。

## 第二步结论（2026-10-27，第四十五批）

- 段内调用前缀复用率三个难版任务都是 100%：命中率不到 95% 只因每轮有新内容，按复用率口径 95% 目标已达到。
- 续接（新进程）复用率 79%–100%：前缀未变，缺口在 provider 侧缓存变冷。
- 换段时段上下文只有交接包一节变化（13 次换段），段上下文重排暂不做。
- 写交接包调用沿用前缀：命中可到 67%–80%，但整体只省约 1%–2%，默认关闭；详见 `docs/design/2026-10-27-handoff-prefix-reuse-design.md` 的“评测结论”。
- 统计口径新增：`resume_same` / `resume_changed` / `provider_switch` 桶、前缀复用率、换段分节变化、`failed_attempts`。

