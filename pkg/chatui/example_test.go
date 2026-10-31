package chatui_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/chatui"
	"github.com/inhere/suga/pkg/chatui/lineedit"
	"github.com/inhere/suga/pkg/chatui/lineedit/pump"
)

// rw joins the pump (what the editor reads) and an output writer into the
// io.ReadWriter lineedit.NewTerminal takes.
type rw struct {
	io.Reader
	io.Writer
}

// Example_minimalHost is the smallest wiring for a host without steering (xhen
// style): one input line read through the byte pump, a tool call printed as one
// line, and the waiting line while the model works. In a real host in is os.Stdin,
// out is os.Stdout and fd is the terminal descriptor; here the input is a string and
// fd is -1 (nothing to watch), so the example is deterministic.
func Example_minimalHost() {
	in := strings.NewReader("hello\r")
	var screen bytes.Buffer
	fd := -1 // int(os.Stdin.Fd()) on a terminal

	// 1. Hooks: DefaultHooks needs no steer logic. Wait is the byte-ready wait of
	// the platform, which the pump needs to tell Alt+Enter and pasted newlines
	// from keys; EscActive nil means a lone Esc is never a key of its own.
	hooks := &pump.DefaultHooks{
		Wait: func(d time.Duration) bool { return pump.WaitTerminalInput(fd, d) },
	}

	// 2. The pump is built before the terminal (NewTerminal takes the reader) and
	// bound to it afterwards.
	p := pump.NewPump(in, hooks)
	term := lineedit.NewTerminal(rw{p, &screen}, "> ")
	p.Bind(term)

	// 3. Follow the window size: the editor wraps at the size it was last given.
	// stop is a no-op without a terminal.
	stop := pump.WatchSize(term, fd, func(width, height int) {})
	defer stop()

	// 4. Read one line. ResetFirstByte arms the first-byte handshake of this read.
	p.ResetFirstByte()
	line, err := term.ReadLine()
	fmt.Println(line, err)

	// 5. Print a tool call as one line. A nil Translator uses the built-in text;
	// "off" colors keep the output plain.
	pal, err := chatui.NewPalette("", map[string]string{chatui.ColorsKeyMode: chatui.ColorModeOff})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(chatui.ToolCallLine(nil, pal, chatui.ToolEvent{
		Name: "file_read",
		Args: map[string]any{"path": "main.go"},
	}, ""))

	// 6. The waiting line. With a StatusSetter (the terminal itself) it lives on
	// the row above the input line; with nil Status it owns a line of Out.
	spin := chatui.NewSpinner(chatui.SpinnerConfig{Enabled: false, Out: os.Stdout, Status: term})
	spin.Start()
	spin.SetLabel(chatui.ToolLabel(nil, "file_read"))
	spin.Stop()

	// Output:
	// hello <nil>
	// ▸ file_read main.go
}
