// Package pump is the byte pump a line editor (lineedit.Terminal) reads through,
// plus the terminal helpers around it (platform input wait, input flush, output
// flags, window size watching).
//
// The pump hands over one byte per Read (the editor keeps partial key sequences
// itself) and decides what the editor cannot tell by itself: whether a 0x1b byte
// is the Esc key on its own or the start of an escape sequence, whether a newline
// is pasted text or a submit, and whether ESC followed by Enter is Alt+Enter. What
// the pump may decide is not a static setting: the caller (Hooks) answers it before
// every read, because the same terminal is read in different modes over time (a
// prompt between turns, a steer read during a turn).
//
// # Wiring
//
// A host that has no steering of its own (one kind of read) needs five calls:
//
//	hooks := &pump.DefaultHooks{Wait: func(d time.Duration) bool { return pump.WaitTerminalInput(fd, d) }}
//	p := pump.NewPump(os.Stdin, hooks)               // reader first: NewTerminal takes it
//	term := lineedit.NewTerminal(rw{p, os.Stdout}, "> ")
//	p.Bind(term)                                     // before the first Read
//	stop := pump.WatchSize(term, fd, nil)            // window size -> editor; defer stop()
//	p.ResetFirstByte()                               // before each term.ReadLine()
//
// New(in, term, hooks) is NewPump + Bind for callers that already have the
// terminal. Hooks is the contract; DefaultHooks is a ready implementation whose
// *Wanted answers are on wherever the platform wait can be trusted
// (WaitInputSupported) and whose lone-Esc decision follows EscActive. A host with
// separate read modes (suga chat: prompt vs steer) implements Hooks itself and
// answers per read.
//
// # Flushing
//
// After discarding pending input with FlushTerminalInput, call
// DefaultHooks.AdvanceEpoch so bytes the pump already read are dropped by HandOver
// instead of reaching the editor. ResetFirstByte re-arms the OnFirstByte handshake
// for a new line read.
//
// # Terminal helpers
//
// WaitTerminalInput(fd, timeout) asks the platform whether a byte is ready without
// reading it (the wait behind the lone-Esc and pasted-newline decisions; the
// timeouts are EscWaitTimeout and PasteNewlineTimeout). FlushTerminalInput drops
// unread input. RestoreOutputProcessing / ClearOutputProcessing switch the output
// flags (OPOST|ONLCR) that raw input clears; they matter for hosts that keep raw
// input while printing. WatchSize applies the terminal size now and on every
// change (SIGWINCH on Unix, polling on Windows).
//
// Example_minimalHost in the parent package compiles this wiring.
package pump
