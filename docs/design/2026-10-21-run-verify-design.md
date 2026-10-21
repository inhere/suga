# `suga run --verify`：交付前验证循环 设计

> 状态：0.2 已批准（2026-10-21，用户：按推荐，退出码 5 可以）。issue：suga-p4gn。
> 来源：Nomi Harness 的“Ralph 验证循环”（交付前独立核对，不通过就回灌）；整理见工作区 `local/analysis/2026-10-21-jev-nomi-borrowing.md`。

## 1. 问题

`suga run` 的一个回合在模型“说完成”时就结束。模型常在没跑测试、测试没过、或只跑了一部分时宣布完成。现在要靠调用方（gofer 派活后的人工审核、eval 的 checks）事后发现，再派一轮修复，往返一次就是一整个 job。

chat 已有 Stop hook 的 block 能在回合结束后开新回合，但 `run` 没有 Stop hook，hook 也需要另写脚本，不适合“跑一条命令看过没过”这种最常见的需求。

## 2. 目标与非目标

目标：

- 调用方给一条验证命令；回合正常完成后 suga 自己跑它，失败就把输出作为新回合交给模型修复，直到通过或用完轮数。
- 结果对调用方可见：退出码、stream-json 事件、trace。

非目标：

- 不做“模型自评/小模型裁判”的验证（只认命令退出码）。
- chat 不加（Stop hook 已覆盖）。
- v1 不从项目配置读取验证命令（见 §6 待定）。
- eval 默认不使用（验收测试不能交给被测模型看，见 §5）。

## 3. 用法

```
suga run --verify "go test ./..." "修复 xxx"
suga run --verify "make check" --verify-rounds 3 --verify-timeout 15m "..."
```

| 参数 | 默认 | 说明 |
|---|---|---|
| `--verify <命令>` | 空（关闭） | 用与 `shell_exec` 相同的 shell（Windows 按 `tools.shell` 配置）在会话工作区执行 |
| `--verify-rounds N` | 2 | 失败后最多回灌几轮（1–5）；第一次验证不计入 |
| `--verify-timeout D` | 10m | 单次验证命令的超时；超时按失败处理 |

## 4. 流程

```
回合完成（RunCompleted，且后台任务已按 finishBackgroundTasks 收尾）
  → 跑验证命令（第 0 次）
     ├ 退出码 0 → 通过，run 正常结束
     └ 非 0 / 超时 → 还有轮数？
          ├ 有 → 注入一条回合（Injected=true）：
          │      [verify failed] 命令、退出码、耗时、输出尾部
          │      “修复后再给出最终答复；不要修改验证命令或删除测试来让它通过。”
          │      → 回合完成 → 再跑验证（第 1 次）…
          └ 无 → run 以退出码 5 结束（ErrVerifyFailed），最后一次输出留在 stderr 摘要
```

规则：

- **只在回合正常完成时验证**。回合出错、yield（input_required）、被取消时不验证，原有退出语义不变；有被拒绝工具（denied_tools）的回合照常验证，但 denied 行照旧输出。
- 输出尾部：合并 stdout/stderr，取最后 120 行且不超过 8000 字符，先过 `redact.Secrets` 与 `termtext.Clean`；更长的完整输出不进上下文（只记 trace 里的长度）。
- 验证命令由**调用者**给出，不经模型、不走审批规则；但仍受 Ctrl-C/取消影响（取消即杀掉进程组）。
- 每轮回灌都是独立回合：各自的 `--max-steps`、预算按现有规则累计（turn budget 到顶即停，不再回灌，退出码按预算规则）。
- 验证通过后**不再追加回合**；模型最后一次答复就是 run 的输出。

## 5. 可见性

- 文本模式：stderr 每次验证一行 `verify #1: go test ./... → exit 1 (12.3s)`；最终失败时打印尾部摘要。stdout 仍只有模型答复。
- stream-json：新增 `{"type":"verify","round":0,"command":"…","exit_code":1,"passed":false,"duration_ms":…}`；结果行增加 `"verify":{"passed":true,"rounds":1}`（未开 `--verify` 时结果行逐字节不变）。
- trace：事件 `verify`（attrs：round、exit、duration、output_len、passed）。`suga inspect trace` 能看到。
- 退出码：通过 0；用完轮数仍失败 5（新，需写进 README 的退出码表）。

eval：`scripts/eval` 不默认使用。验收测试一旦作为 `--verify` 交给 suga，模型就能从失败输出里看到隐藏用例，等于泄题。可以作为单独的 `--variant`（如“自带的可见测试作验证”）比较效果，另开 issue。

gofer：派活时 gofer 可以把 `--verify` 加到 suga 的参数里（gofer 侧改动，由 gofer 会话决定）。

## 6. 已定（2026-10-21 用户批准）

1. **项目配置**：v1 不支持 `.suga/config.yaml` 的 `run.verify`（仓库内容不能决定执行什么命令）；以后要做时需 `project_trust`。
2. **退出码 5**：用完轮数仍失败。
3. **默认轮数 2**，可用 `--verify-rounds` 调整（1–5）。

## 7. 测试

- 单测（假 runner + 假命令）：通过不回灌；失败回灌 N 轮后通过；用完轮数退出码 5；超时按失败；回合出错/yield 不验证；输出尾部截断与脱敏；stream-json 行与结果行（关闭时结果行不变）。
- 端到端：`fakellm` 第一轮不修、第二轮修好，`--verify` 命令检查文件内容，断言两轮、退出码 0、trace 有两条 `verify` 事件；再跑一次永远失败，断言退出码 5。反向验证。
