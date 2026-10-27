# i18n 阶段 1b 真实模型评测（模型侧英文最小集）

> 日期：2026-10-27 · 模型：`opencode-go/deepseek-v4.1-flash`（主机 `suga run`，评测复制配置到临时 home）· 二进制：main `7becf2ae` · 结论：用户 2026-10-27 接受阶段 1b

## 做法

`scripts/eval` 的两个 variant 只差 `language: en` / `language: zh`（阶段 1b 只在 `en` 下把 8 组模型可见文字换成英文；`zh` 请求逐字节不变，有快照测试）。每个任务每组 `--repeat 3`。评测按「任务 → variant（`--variant` 顺序）→ 轮次」串行，所以先跑的组从冷的 provider 缓存起步；第二轮把顺序反过来检验这一点。

## 结果

| 任务 | 轮次（先跑） | 通过 en / zh | steady 命中 en / zh | 总体命中 en / zh | 平均调用 en / zh | 总 prompt en / zh |
|---|---|---|---|---|---|---|
| longsession-lowfreq-tools | 1（en） | 3/3 / 2/3 | 87.0 / 82.4 | 75.4 / 67.8 | 6.7 / 6.0 | 211k / 198k |
| longsession-hard-distractor-rules | 1（en） | 3/3 / 2/3 | 80.2 / 86.3 | 77.0 / 82.1 | 23.3 / 23.7 | 1.00M / 0.93M |
| longsession-hard-distractor-rules | 2（zh） | 3/3 / 3/3 | 81.8 / 85.4 | 78.3 / 80.5 | 22.3 / 22.3 | 0.80M / 0.84M |
| longsession-hard-rule-update | 1（en） | 3/3 / 3/3 | 87.8 / 91.0 | 79.3 / 83.4 | 29.0 / 35.3 | 1.06M / 1.30M |
| longsession-hard-rule-update | 2（zh） | 3/3 / 3/3 | 87.8 / 88.1 | 77.3 / 77.8 | 27.7 / 29.0 | 0.99M / 1.03M |

- 通过：en 15/15，zh 13/15。zh 的两次失败：lowfreq r1 答案同时提到已作废的旧值；distractor r3 第 7 轮要求不用中文作答，模型仍输出了汉字（`answer_not_matches:[\p{Han}]`）。
- rule-update：第一轮差 3.2 点，换顺序后 0.3 点——主要是运行顺序 / provider 缓存冷热。
- distractor：两轮 en 都低 3.6–6.1 点。逐次调用看，低命中出现在两组相同的步（读大文件、追加新内容的位置，如第 3、12–15 步），en 没有额外的低命中点，前缀没有被破坏；差距来自个别运行的新增内容更多（如第一轮 en 段内未缓存 29.0k/41.6k/15.8k，zh 18.1k/23.0k/15.3k），原因未证实。

## 结论

按计划的门槛，通过率达标（en 更好），steady 命中率在 distractor 上未达标。考虑到 zh 请求不受影响、en 通过率更高、差距不是前缀失效，用户接受阶段 1b；distractor 上的差距记入 `suga-jh7b` 后续观察。原始数据：WP `tmp/eval/i18n-*`、`tmp/eval/i18n2-*`（`compare.md`、`results.jsonl`）。
