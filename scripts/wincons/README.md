# wincons

在 Windows 控制台上驱动真实 `suga chat`，复现「工具调用进行中按 Ctrl-C」的行为（suga-nrf2）。Linux/macOS 用 `scripts/ptysmoke`（PTY）；Windows 控制台不能用 Python 的 `pty` 驱动，所以这里是等价的 Go 工具。

`--scenario` 决定 Ctrl-C 该结束什么，两种都跑：

- `steer`（默认，`chat.steering` 开）：回合中控制台是 raw（`PROCESSED_INPUT` 关），所以 Ctrl-C 按键不会变成 CTRL_C_EVENT，而是把字符 `0x03` 交给读输入的进程。harness 用 `WriteConsoleInput` 写一条 `VirtualKeyCode=C` + `ControlKeyState=LEFT_CTRL_PRESSED` + `UnicodeChar=0x03` 的 KEY_EVENT 来模拟这个按键（README 末尾的「限制」说明为什么不能靠合成事件触发 CTRL_C_EVENT）。检查：单次 Ctrl-C 只结束本轮（出现“已中断本轮”、插话回到输入行、没有打印续接命令、进程仍在），随后 3 秒内再按一次才退出。
- `exit`（scratch 配置里写 `chat.steering: false`）：回合中控制台保持 cooked，Ctrl-C 变成 SIGINT，chat 立即退出（130）—— 与 steering 之前的实现一致。

一次运行做的事：

1. 脱离启动它的控制台（gofer job 没有可用的交互控制台），另建一个私有控制台，`suga chat` 以该控制台的 `CONIN$` 作为 stdin 启动（raw/cooked 处理是真实的）；
2. 起一个进程内的假模型（OpenAI 兼容 SSE，不依赖 Python）：第一个请求回一个 `shell_exec` 调用，命令先写 `tool-started.txt`、`sleep 60`、再写 `tool-finished.txt`；工具结果之后回文本，于是这一轮会长时间跑工具；
3. 在提示符处**键入**消息（`WriteConsoleInput` 写 KEY_EVENT，等价真人敲键；`--deliver` 改为用 `suga send` 投递，即「提示符读还挂着时开始回合」的那条路径）；
4. 等 `tool-started.txt` 出现（工具确已在跑），打印**控制台输入模式的采样时间线**（控制台输入模式是整台控制台共享的，所以这里看到的就是 suga 设置的结果），然后按 Ctrl-C（`steer` 场景在按之前先键入 `--steer-message` 的内容，检查它作为插话被回收到输入行）；
5. 报告 Ctrl-C 结束的是这一轮还是整个 chat、耗时多少、工具是否被一起结束、以及输出尾部。

退出码：`0` = 与 `--scenario` 的预期一致（`exit`：Ctrl-C 后 suga 正常退出；`steer`：第一次 Ctrl-C 只结束本轮、第二次退出）；`1` = 与预期不符（超时未退出、或一次 Ctrl-C 就结束了 chat）；`2` = 用法/环境错误。

## 用法

```powershell
go build -o tmp/wincons/suga.exe ./cmd/suga
go run ./scripts/wincons --bin tmp/wincons/suga.exe --work tmp/wincons --console classic            # 默认 --scenario steer
go run ./scripts/wincons --bin tmp/wincons/suga.exe --work tmp/wincons-exit --console classic --scenario exit
```

| 参数 | 说明 |
|---|---|
| `--scenario steer\|exit` | Ctrl-C 该结束什么：`steer`（默认，`chat.steering` 开）只结束本轮，`exit`（配置 `chat.steering: false`）结束 chat。`steer` 需要读得到输出，不能与 `--console-output` 同时用 |
| `--steer-message` | `steer` 场景在本轮中键入的内容（默认 `改一下`），检查它被回收到输入行 |
| `--console classic` | 普通 Windows 控制台（`AllocConsole`）：harness 自己附着在该控制台上，用 `GenerateConsoleCtrlEvent(CTRL_C_EVENT, 0)` 投递 Ctrl-C —— 这正是控制台在 cooked 模式下收到真人 Ctrl-C 时做的事 |
| `--console pty`（默认） | Windows 伪控制台（ConPTY），即 Windows Terminal / VS Code 给程序的「终端」：Ctrl-C 以终端的方式投递，即往输入管道写 `0x03` 字节，是否变成 CTRL_C_EVENT 由控制台模式决定。**注意当前 job 里跑不通，见下** |
| `--console-output` | 让 chat 自己写控制台（stdout/stderr = `CONOUT$`，不设 `NO_COLOR`）：彩色、spinner、输入行背景都打开，与真人在终端里一致；此时 harness 读不到输出（改用固定等待） |
| `--deliver` | 用 `suga send --session <id>` 开始回合（提示符那一次读仍挂着），即 suag-jsha 的场景 |
| `--message` | 提示符处键入的内容（默认 `run`） |
| `--command` | 假模型让 `shell_exec` 跑的命令（默认写 `tool-started.txt`、`sleep 60`、写 `tool-finished.txt`） |
| `--timeout` | 发 Ctrl-C 后等 suga 退出的上限（默认 15s） |
| `--no-ctrl-c` | 对照运行：不发 Ctrl-C，确认这一轮会一直跑（工具确实在跑，不是提前结束） |
| `--keep` | 保留 scratch 的 SUGA_HOME 与工作区 |

辅助模式：

- `--pty-probe`：在一个伪控制台里跑一个只等信号、只读 stdin 的子程序，然后写 `0x03`，报告它变成 **CTRL_C_EVENT**（cooked 路径正常）、变成了**普通输入字节**（processed input 被关掉），还是**两者都没有**（没人读控制台时按键丢失）。用来回答「ConPTY 下 Ctrl-C 到底走哪条路」。
- `--self-child`：`--pty-probe` 驱动的那个子程序。

## 2026-10-15 实测结果（steering 开/关两组，本机 Windows 10 IoT LTSC 2024，Go 1.25.10）

`--console classic --scenario steer`（steering 开，回合中 raw）：

```
tool started at 20:02:33.926
console input mode while the tool runs: [-PROCESSED_INPUT -LINE_INPUT -ECHO_INPUT VT_INPUT]
timeline:
  0ms 0x1f7 [PROCESSED_INPUT LINE_INPUT ECHO_INPUT VT_INPUT]      # 提示符读行（raw，设计如此）
 100ms 0x3f0 [-PROCESSED_INPUT -LINE_INPUT -ECHO_INPUT VT_INPUT]  # 回合开始：SetBusy(true) 保持 raw（steering）
the inserted message is on screen at 20:02:33.984
wrote the Ctrl+C key (char 0x03) to the console input at 20:02:33.984
... [插话] 改一下（等待送达） / 已中断本轮，1 条插话已放回输入行 / ↳ 改一下 ...
tool finished on its own: false
RESULT: one Ctrl+C ended this turn only 50ms after the press; chat is still running
wrote the Ctrl+C key (char 0x03) to the console input at 20:02:34.034
suga exited: <nil>
RESULT: the second Ctrl+C 50ms after the first (window 3s) left chat
```

即：`chat.steering` 开时回合中控制台确实是 raw，合成的 Ctrl+C 按键（`UnicodeChar=0x03`）被读到并只结束了本轮（“已中断本轮”+ 插话回输入行、无续接命令、进程仍在），3 秒内再按一次才退出；工具子进程随本轮一起被结束（`cmd.Cancel`）。

`--console classic --scenario exit`（steering 关，回合中 cooked）：

```
timeline:
  0ms 0x1f7 [PROCESSED_INPUT LINE_INPUT ECHO_INPUT VT_INPUT]
 100ms 0x3f0 [-PROCESSED_INPUT -LINE_INPUT -ECHO_INPUT VT_INPUT]
 200ms 0x1f7 [PROCESSED_INPUT LINE_INPUT ECHO_INPUT VT_INPUT]      # 回合开始：回到 cooked
GenerateConsoleCtrlEvent(CTRL_C, console) r=1
suga exited: exit status 130
RESULT: chat.steering off: Ctrl+C ended chat 21ms after the press (SIGINT)
```

## 2026-10-14 实测结果（本机 Windows 10 IoT LTSC 2024，Go 1.25.10）

classic 控制台三路径都通过（`--console classic`；另两次加 `--deliver` 与 `--console-output`）。下面这一轮是 `--console-output`（彩色/高亮全开，最接近真人终端），所以能采到提示符期的 raw：

```
tool started at 13:37:34.076
console input mode while the tool runs: [PROCESSED_INPUT LINE_INPUT ECHO_INPUT VT_INPUT]
timeline:
  0ms 0x1f7 [PROCESSED_INPUT LINE_INPUT ECHO_INPUT VT_INPUT]
 100ms 0x3f0 [-PROCESSED_INPUT -LINE_INPUT -ECHO_INPUT VT_INPUT]   # 提示符读行（raw，设计如此）
3100ms 0x1f7 [PROCESSED_INPUT LINE_INPUT ECHO_INPUT VT_INPUT]      # 回合开始：SetBusy(true) 恢复 cooked
GenerateConsoleCtrlEvent(CTRL_C, console) r=1 at 13:37:34.084
suga exited: exit status 130
tool finished on its own: false
RESULT: suga exited 21ms after Ctrl+C
```

即：回合进行中控制台确实是 cooked（`PROCESSED_INPUT` 置位），Ctrl-C 立即变成 SIGINT，回合结束、chat 退出（130），工具子进程随回合被结束。键入路径、`suga send` 投递路径、彩色（spinner/highlight 打开）路径都一样。

顺便测到的两条 Windows 事实（见 `--help` 与探针注释）：

- 控制台**输入模式是整台控制台共享**的：`SetConsoleMode(CONIN$, ...)` 影响所有附着进程。
- `SetConsoleCtrlHandler(NULL, TRUE)`（「忽略 Ctrl-C」）会被**之后创建的子进程继承**；`CreateProcess` 里用 `CREATE_NEW_PROCESS_GROUP` 创建的子进程**收不到 CTRL_C_EVENT**（只能收 CTRL_BREAK）。两者都会让 Ctrl-C 静默失效。

## 限制

- **ConPTY 模式在 gofer job 里起不来**：`CreateProcess` 带 `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` 时子进程立刻以 `0xC0000142`（STATUS_DLL_INIT_FAILED）退出，连 `cmd.exe` 也一样；job 不允许 breakaway（`CREATE_BREAKAWAY_FROM_JOB` 返回 Access denied），脱离 job 也做不到。`--pty-probe` 在普通终端（Windows Terminal / conhost）里跑才有效。
- 打字用 `WriteConsoleInput` 写 KEY_EVENT；**合成 Ctrl-C 按键事件不能触发 CTRL_C_EVENT**（conhost 依赖真实键盘状态），所以 `exit` 场景（cooked）用 `GenerateConsoleCtrlEvent` 投递，`steer` 场景（raw）用合成按键，靠控制台把它的字符 `0x03` 交给读输入的一方。
- 只覆盖 shell_exec 前台工具；审批提问（`Ask`）、后台任务、MCP 工具的中断另有测试（`scripts/ptysmoke/chat_approval.py`、Go `TestChatTasks`（`test/ptye2e`））。
