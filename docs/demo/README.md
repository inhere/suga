# 演示录屏

`docs/demo/suga-chat.cast` 是一段约 30 秒的 `suga chat` 终端演示，用 asciicast v2 录制（100 列 × 28 行），可播放、可转 GIF，用于 README 与博客。

```bash
asciinema play docs/demo/suga-chat.cast   # 本地回放（需要 asciinema 3.x）
asciinema cat docs/demo/suga-chat.cast    # 只要文字输出
```

## 演示内容

| 时间轴 | 画面 |
|---|---|
| 启动 | 横幅：模型、会话短名称、工具是否可写 |
| 提问 | 「先看下这个项目是做什么的？」→ 两行工具调用 `⏺ file_read README.md`、`⏺ file_read todo.py`，然后回答 |
| 回合中排队 | 模型作答期间输入「顺便说下怎么运行」并按 `Tab`：`[排队] …（本轮结束后发送）`，本轮结束后自动发出去并显示 `（已发送）` |
| 写文件审批 | 「把 todo.py 里 added: 改成中文提示」→ `允许执行 file_edit`，显示原文/改为，输入 `y` 后 `⎿ ok: edited …` |
| 上下文 | `:context` 列出系统提示、工具 schema、常驻段上下文、历史消息与估算 token |
| 退出 | `:quit` 打印本次用量与 `续接：suga chat --resume <会话 ID>` |

## 确定性：不依赖真实模型与密钥

演示**不是**真实模型跑出来的：它用 `scripts/ptysmoke/fakellm.py` 的 `demo` 模式（本地 OpenAI 兼容假服务，按用户消息选回合：含「运行」只回文本，含「改成」发一次 `file_edit`），小项目与 SUGA_HOME 都在 `tmp/demo/` 下临时生成，录完即删。因此同一份脚本每次录出的画面一致，也不需要任何 API key。

录制时会把工作区与 SUGA_HOME 的绝对路径替换成 `~/demo/todo`、`~/.suga`，脚本最后还会检查输出里没有本机真实目录（有则退出非 0），所以录像里不会出现真实路径与用户名。

## 重录

需要 Linux/macOS（要用伪终端）：

```bash
make demo-cast                                             # 构建 tmp/smoke/suga 并重新录制
python3 -I scripts/demo/record.py --bin tmp/smoke/suga      # 已经有二进制时直接录
python3 -I scripts/demo/record.py --dry-run                 # 只打印时间轴自检（Windows 上也能跑）
```

其它选项：`--out <文件>`（默认 `docs/demo/suga-chat.cast`）、`--max-seconds N`（默认 60，超过即失败）、`--keep`（保留 `tmp/demo/` 便于排查）。任一时间轴的等待没有出现，脚本会打印缺失的那一步与最后 40 行输出并退出非 0，不会写出一段中间断掉的录像。

改演示内容要动两处：`scripts/demo/record.py` 里的 `STEPS`（按键时间轴）与 `REPLY_*`／`SAMPLE_FILES`（假模型的回答与示例项目）；假模型的回合规划在 `scripts/ptysmoke/fakellm.py` 的 `demo_plan`。

## 转成 GIF（可选）

```bash
# 安装：cargo install --locked agg，或从 https://github.com/asciinema/agg/releases 下载
agg --cols 100 --rows 28 --font-size 14 \
    --font-family "Noto Sans Mono CJK SC,DejaVu Sans Mono,monospace" \
    --idle-time-limit 2 \
    docs/demo/suga-chat.cast docs/images/demo.gif
```

- 画面是中文，字体必须带 CJK（`--font-family` 指向本机中文字体，如 `Noto Sans Mono CJK SC`、`Sarasa Mono SC`；否则中文显示成方块）。
- GIF 控制在 2MB 以内：先看 `ls -l docs/images/demo.gif`，超了用 `--speed 1.3` 加速、`--font-size 13` 缩小、或 `--fps 12` 降帧率重转。
- 只在本地产出，**不要**上传到 asciinema.org 等外部站点——要不要发布由维护者决定。
