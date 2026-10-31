# pkg/chatui：可复用的终端 chat 组件

`pkg/chatui`（含 `lineedit`、`lineedit/pump`、`termtext`）是 suga chat 的终端显示与输入组件，对外部宿主（如 xhen）公开，不依赖 `internal/*`（`TestNoInternalImports` 守着）。API 以 `go doc -all ./pkg/chatui` 与各包的包注释为准，本文给出怎么接。

## 组件一览

| 组件 | 作用 |
|---|---|
| `ToolEvent`、`ToolCallLine`、`ToolResultLine`、`ToolCallArgs` | 一次工具调用 / 结果的单行显示：家族符号、工具名、参数摘要（路径相对工作区、秘密脱敏、截断到 `CallArgsLimit` / `ResultSummaryLimit`） |
| `Palette`、`NewPalette(theme, settings)` | 解析 `chat.colors.*`（`mode`、`style`、各元素与家族配色）；一次构建、终端色深只问一次；未知键 / 颜色报错；`WithColor(false)` 得到无色副本 |
| `Spinner`、`SpinnerConfig`、`NewSpinner` | 等待行；`StatusSetter`（`*lineedit.Terminal` 即实现者）存在时画在输入行上方的状态行，否则独占 `Out` 的一行；`Start`/`Stop`/`SetLabel`/`Suspend`/`Resume`；`Enabled=false` 全部空操作 |
| `ToolLabel(t, name)` | 工具运行期间等待行显示的文案 |
| `lineedit/pump` | 行编辑器读取用的字节泵与终端辅助（见下） |

所有文案走 `Translator`（`T(key, args...)`）；传 `nil` 使用内置中文，没有 catalog 的宿主不用配置。

## pump 速查

- `Hooks`：泵向调用方问的合同。三个 `*Wanted`（Alt+Enter、粘贴换行、单独 Esc）与 `Win32Records` 在**每次读取前**求值，因为同一个终端会以不同模式被读取（提示符读取、回合内插话读取）。其余方法是一次读取的生命周期（epoch / `HandOver`、`OnFirstByte`、`StickyEOF`、`OnEOF`、`OnEscPressed`）。
- `DefaultHooks`：给没有 steer 的宿主的现成实现，用指针；填 `Wait`（通常是 `pump.WaitTerminalInput(fd, d)`）、可选 `EscActive` / `EscPressed` / `Win32` / `FirstByte`。没有 `Wait` 时泵不做字节级判断（Esc 留给编辑器、换行都算提交）。
- `New(in, term, hooks)` / `NewPump(in, hooks)` + `Bind(term)`：`lineedit.NewTerminal` 需要先有 reader，所以先 `NewPump`，建好终端后 `Bind`（必须在第一次 `Read` 之前）。
- `ResetFirstByte()`：每次开始新的一行读取（`ReadLine` 之前）调用，让这次读取重新做“首字节活动握手”；不调用则 `OnFirstByte` 整个泵生命周期只跑一次。
- 冲刷：`FlushTerminalInput(fd)` 之后调用 `DefaultHooks.AdvanceEpoch()`，泵已读到但未交给编辑器的旧字节会被丢弃。
- `WatchSize(term, fd, onChange)`：立即并在每次窗口变化时把大小设给编辑器（Unix SIGWINCH，Windows 轮询）；返回 `stop`。无终端或 `fd<0` 时什么也不做。
- 其他：`WaitTerminalInput`、`FlushTerminalInput`、`RestoreOutputProcessing` / `ClearOutputProcessing`（原始输入会清掉输出标志，导致换行不回车）、常量 `EscWaitTimeout`、`PasteNewlineTimeout`、`WaitInputSupported`。

## 最小接入（无 steer 的宿主）

可编译的完整版本是 `pkg/chatui/example_test.go` 的 `Example_minimalHost`（`go test ./pkg/chatui` 会编译并运行）。骨架：

```go
hooks := &pump.DefaultHooks{Wait: func(d time.Duration) bool { return pump.WaitTerminalInput(fd, d) }}
p := pump.NewPump(os.Stdin, hooks)
term := lineedit.NewTerminal(rw{p, os.Stdout}, "> ") // rw 把 reader 与 writer 合成 io.ReadWriter
p.Bind(term)
stop := pump.WatchSize(term, fd, func(w, h int) {})
defer stop()

pal, _ := chatui.NewPalette("", nil) // theme 空 = 暗色；settings 即 chat.colors
spin := chatui.NewSpinner(chatui.SpinnerConfig{Enabled: true, Out: os.Stdout, Status: term, Paint: pal.PaintDim})

p.ResetFirstByte()
line, err := term.ReadLine()
// ... 发给模型；等待期间：
spin.Start()
spin.SetLabel(chatui.ToolLabel(nil, "file_read"))
fmt.Println(chatui.ToolCallLine(nil, pal, chatui.ToolEvent{Name: "file_read", Args: map[string]any{"path": "main.go"}}, workdir))
spin.Stop()
```

需要回合内插话（steer）的宿主自行实现 `pump.Hooks`，按当前读取模式回答 `*Wanted`；suga chat 的实现见 `internal/chat/input_pump.go`。
