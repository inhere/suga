//go:build windows

// Command wincons reproduces the Windows Ctrl+C behaviour of `suga chat` while a
// tool call is running, without a person at the keyboard.
//
// The terminal behaviour differs per platform and cannot be driven from a Linux
// PTY (scripts/ptysmoke is Linux/macOS only), so this harness drives the two
// consoles Windows has:
//
//   - --console classic: the process detaches from the console it was started
//     with (a gofer job or a runner has none worth keeping), creates a private
//     one and starts `suga chat` on that console's CONIN$. Ctrl+C is delivered
//     with GenerateConsoleCtrlEvent, which is what the console does for a real
//     Ctrl+C while it is cooked.
//   - --console pty (default): `suga chat` runs in a Windows pseudoconsole
//     (ConPTY) - the console Windows Terminal, VS Code and other modern
//     terminals give a program. Ctrl+C is delivered as the byte 0x03 written
//     into the input pipe, exactly what the terminal sends for the key, so this
//     is the faithful copy of the user's environment.
//
// Both runs use a fake OpenAI-compatible model (in process, no Python): the
// first request is answered with a shell_exec call to a slow command and later
// requests with text, so a tool runs for as long as wanted. The harness types
// the message, waits for the tool's marker file, presses Ctrl+C and reports how
// long suga took to exit, whether the tool was ended with it and the output.
//
//	go build -o tmp/wincons/suga.exe ./cmd/suga
//	go run ./scripts/wincons --bin tmp/wincons/suga.exe --work tmp/wincons
//
// Flags --pty-probe and --self-child check the console primitives themselves:
// the probe runs a child that waits for a signal in a pseudoconsole and reports
// whether the Ctrl+C byte reaches it as a signal, as input, or not at all.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole              = kernel32.NewProc("FreeConsole")
	procAllocConsole             = kernel32.NewProc("AllocConsole")
	procGetConsoleWindow         = kernel32.NewProc("GetConsoleWindow")
	procGenerateConsoleCtrlEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
	procSetConsoleCtrlHandler    = kernel32.NewProc("SetConsoleCtrlHandler")
	procWriteConsoleInputW       = kernel32.NewProc("WriteConsoleInputW")
)

const (
	ctrlCEvent = 0 // GenerateConsoleCtrlEvent event id for Ctrl+C
	// Console input modes (docs.microsoft.com/windows/console/setconsolemode).
	enableProcessedInput = 0x0001 // on: Ctrl+C becomes a CTRL_C_EVENT
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
	enableVTInput        = 0x0100

	startedMarker  = "tool-started.txt"
	finishedMarker = "tool-finished.txt"

	keyEventType = 0x0001
	vkReturn     = 0x0D
	// vkC and leftCtrlPressed are the key a real Ctrl+C press reports; its
	// character is 0x03 (ctrlCChar), the byte a raw reader gets for the key.
	vkC             = 0x43
	leftCtrlPressed = 0x0008
	ctrlCChar       = 0x03
	promptWait      = 30 * time.Second
	ctrlCByte       = "\x03"

	// What the scenarios wait for in the chat's output: the prompt, the notice
	// that only this turn ended, the line of an inserted message, and the resume
	// command chat prints on its way out (its absence means chat is still there).
	promptMarker = "❯ "
	abortMarker  = "已中断本轮"
	steerMarker  = "[插话]"
	// The id part is only "s" in the marker: it matches ids of both formats
	// (s_... now, sess_... from older versions).
	resumeMarker  = "续接：suga chat --resume s"
	confirmWindow = 3 * time.Second
)

// keyEventRecord is the KEY_EVENT_RECORD of a console input record.
type keyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

// inputRecord is one INPUT_RECORD written with WriteConsoleInputW.
type inputRecord struct {
	EventType uint16
	_         uint16
	Event     [16]byte
}

type options struct {
	bin       string
	work      string
	console   string
	message   string
	timeout   time.Duration
	command   string
	keep      bool
	noEvent   bool
	deliver   bool
	ptyProbe  bool
	selfChild bool
	// scenario selects what the harness expects of Ctrl+C during a tool call:
	// "steer" (chat.steering on, the default) presses the key so the console -
	// raw while the turn runs - hands the reader the byte 0x03, which must end
	// this turn only; "exit" configures chat.steering: false, the console stays
	// cooked, the key becomes SIGINT and chat exits at once (the behaviour before
	// steering).
	scenario string
	// steerMessage is what scenario "steer" types into the running turn before
	// pressing Ctrl+C.
	steerMessage string
	// consoleOutput gives the chat the console itself as stdout/stderr instead
	// of a pipe: colour, the spinner and the input highlight are then on, as in a
	// person's terminal (the pipe and NO_COLOR turn them off), but the output can
	// no longer be read by the harness.
	consoleOutput bool
}

func main() {
	opts := options{}
	flag.StringVar(&opts.bin, "bin", filepath.Join("tmp", "wincons", "suga.exe"), "suga binary to drive")
	flag.StringVar(&opts.work, "work", filepath.Join("tmp", "wincons"), "scratch directory (SUGA_HOME, workspace)")
	flag.StringVar(&opts.console, "console", "pty", "which Windows console to drive: pty (ConPTY, Windows Terminal) or classic")
	flag.StringVar(&opts.message, "message", "run", "message typed at the chat prompt")
	flag.DurationVar(&opts.timeout, "timeout", 15*time.Second, "how long to wait for suga to exit after Ctrl+C")
	flag.StringVar(&opts.command, "command", "", "tool command (default: write "+startedMarker+", sleep 60, write "+finishedMarker+")")
	flag.BoolVar(&opts.keep, "keep", false, "keep the scratch home and workspace")
	flag.BoolVar(&opts.noEvent, "no-ctrl-c", false, "do not send Ctrl+C (control run: the turn keeps running)")
	flag.BoolVar(&opts.consoleOutput, "console-output", false, "let the chat write to the console (colour, spinner, highlight on), as a person's terminal does")
	flag.BoolVar(&opts.deliver, "deliver", false, "start the turn with `suga send` instead of typing (a turn while the prompt read is pending)")
	flag.StringVar(&opts.scenario, "scenario", "steer", "what Ctrl+C must do: steer (chat.steering on: end this turn only) or exit (chat.steering off: end chat)")
	flag.StringVar(&opts.steerMessage, "steer-message", "改一下", "message typed into the running turn (scenario steer)")
	flag.BoolVar(&opts.ptyProbe, "pty-probe", false, "check what the Ctrl+C byte does to a child in a pseudoconsole")
	flag.BoolVar(&opts.selfChild, "self-child", false, "run as the child of --pty-probe")
	flag.Parse()
	if opts.selfChild {
		selfChild()
		return
	}
	if opts.ptyProbe {
		if err := ptyProbe(); err != nil {
			fmt.Fprintln(os.Stderr, "FAIL:", err)
			os.Exit(1)
		}
		return
	}
	if _, err := os.Stat(opts.bin); err != nil {
		fmt.Fprintf(os.Stderr, "suga binary %s: %v\n", opts.bin, err)
		os.Exit(2)
	}
	if opts.scenario != "steer" && opts.scenario != "exit" {
		fmt.Fprintf(os.Stderr, "unknown -scenario %q (steer or exit)\n", opts.scenario)
		os.Exit(2)
	}
	if opts.scenario == "steer" && opts.consoleOutput {
		// The steer scenario asserts on the output (the notice, the put-back
		// line), and --console-output sends it to the console instead.
		fmt.Fprintln(os.Stderr, "-scenario steer needs the output: do not combine it with -console-output")
		os.Exit(2)
	}
	if opts.command == "" {
		opts.command = fmt.Sprintf("Set-Content %s started; Start-Sleep -Seconds 60; Set-Content %s finished",
			startedMarker, finishedMarker)
	}
	// The child runs in the scratch workspace, so the binary path must be absolute.
	abs, err := filepath.Abs(opts.bin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "binary path:", err)
		os.Exit(2)
	}
	opts.bin = abs
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

// chatConsole drives one `suga chat` process on a Windows console.
type chatConsole interface {
	TypeLine(text string) error     // type a message and Enter, as a keyboard does
	SendCtrlC() error               // press Ctrl+C the way a cooked console reports it
	SendCtrlCByte() error           // write the byte the Ctrl+C key is in raw input mode
	ModeReport() string             // the console input mode flags seen while the turn ran
	Output() string                 // what the program printed so far
	WaitExit(d time.Duration) error // wait for the program to end
	Kill()
}

func run(opts options) error {
	work, _ := filepath.Abs(opts.work)
	home, ws := filepath.Join(work, "home"), filepath.Join(work, "ws")
	model, port, err := startFakeModel(opts.command)
	if err != nil {
		return err
	}
	defer model.Close()
	if err := prepare(opts, home, ws, port); err != nil {
		return err
	}
	fmt.Printf("fake model on 127.0.0.1:%d, command %q\n", port, opts.command)

	var cons chatConsole
	switch opts.console {
	case "classic":
		cons, err = newClassicConsole(opts.bin, home, ws, opts.consoleOutput)
	case "pty":
		cons, err = newPtyConsole(opts.bin, home, ws)
	default:
		return fmt.Errorf("unknown console %q (classic or pty)", opts.console)
	}
	if err != nil {
		return err
	}
	defer cons.Kill()

	if opts.consoleOutput {
		// The chat writes to the console: there is nothing to poll for the
		// prompt, so give it a moment to come up.
		time.Sleep(3 * time.Second)
	} else if err := waitForOutput(cons, "❯ ", promptWait); err != nil {
		fmt.Printf("output:\n%s\n", tail(cons.Output(), 40))
		return fmt.Errorf("the chat prompt never appeared: %w", err)
	}
	if opts.deliver {
		// The turn starts from a message delivered over the session socket: the
		// prompt read is still pending while it runs, the case where the console
		// must be put back into cooked mode by SetBusy(true).
		if err := deliverTurn(opts, home, ws, cons.Output()); err != nil {
			return err
		}
	} else if err := cons.TypeLine(opts.message); err != nil {
		return fmt.Errorf("type: %w", err)
	}
	if err := waitForFile(filepath.Join(ws, startedMarker), 60*time.Second); err != nil {
		fmt.Printf("output:\n%s\n", tail(cons.Output(), 40))
		return fmt.Errorf("the tool never started: %w", err)
	}
	time.Sleep(time.Second) // the tool is running by now
	fmt.Printf("tool started at %s\n", time.Now().Format("15:04:05.000"))
	fmt.Printf("console input mode while the tool runs: %s\n", cons.ModeReport())
	if opts.noEvent {
		fmt.Printf("control run: no Ctrl+C sent; waiting %s\n", opts.timeout)
		if err := cons.WaitExit(opts.timeout); err != nil {
			fmt.Printf("RESULT: suga still running, as expected without an interrupt\n")
			return nil
		}
		return fmt.Errorf("suga exited without an interrupt")
	}

	start := time.Now()
	return runInterruptScenario(opts, cons, ws, start)
}

// runInterruptScenario presses Ctrl+C during the running tool call and checks
// what it ended. With chat.steering off ("exit") the console stays cooked, the
// key becomes SIGINT and chat exits at once - the behaviour before steering.
// With chat.steering on ("steer") the console is raw while the turn runs, so the
// key arrives as the byte 0x03, which must end this turn only: chat stays (no
// resume line), the message typed into the turn comes back to the input line,
// and a second press within confirmWindow leaves chat.
func runInterruptScenario(opts options, cons chatConsole, ws string, start time.Time) error {
	steering := opts.scenario == "steer"
	if steering {
		// A message typed while the tool runs: with the console raw the editor takes
		// it, and the turn's end must hand it back to the input line.
		if err := cons.TypeLine(opts.steerMessage); err != nil {
			return fmt.Errorf("type the inserted message: %w", err)
		}
		if err := waitForOutput(cons, steerMarker, promptWait); err != nil {
			fmt.Printf("output:\n%s\n", tail(cons.Output(), 40))
			return fmt.Errorf("the inserted message was never shown: %w", err)
		}
		fmt.Printf("the inserted message is on screen at %s\n", time.Now().Format("15:04:05.000"))
		start = time.Now()
	}
	press := cons.SendCtrlC
	if steering {
		press = cons.SendCtrlCByte
	}
	if err := press(); err != nil {
		return fmt.Errorf("Ctrl+C: %w", err)
	}
	if !steering {
		if err := cons.WaitExit(opts.timeout); err != nil {
			fmt.Printf("output:\n%s\n", tail(cons.Output(), 40))
			return fmt.Errorf("suga did not exit within %s after Ctrl+C", opts.timeout)
		}
		fmt.Printf("output:\n%s\n", tail(cons.Output(), 40))
		fmt.Printf("tool finished on its own: %v\n", fileExists(filepath.Join(ws, finishedMarker)))
		fmt.Printf("RESULT: chat.steering off: Ctrl+C ended chat %s after the press (SIGINT)\n",
			time.Since(start).Round(time.Millisecond))
		return nil
	}
	if err := waitForOutput(cons, abortMarker, opts.timeout); err != nil {
		fmt.Printf("output:\n%s\n", tail(cons.Output(), 40))
		return fmt.Errorf("one Ctrl+C did not end the turn: %w", err)
	}
	// The turn ended: chat must be back at the prompt (the input line, with the
	// inserted message back in it), and must not have printed its resume command.
	aborted := time.Since(start)
	_ = waitForOutput(cons, promptMarker, opts.timeout)
	out := cons.Output()
	if strings.Contains(out, resumeMarker) {
		fmt.Printf("output:\n%s\n", tail(out, 40))
		return fmt.Errorf("one Ctrl+C ended the whole chat; it must end this turn only")
	}
	if !strings.Contains(out, opts.steerMessage) {
		fmt.Printf("output:\n%s\n", tail(out, 40))
		return fmt.Errorf("the inserted message %q is not back in the input line", opts.steerMessage)
	}
	fmt.Printf("output:\n%s\n", tail(out, 40))
	fmt.Printf("tool finished on its own: %v\n", fileExists(filepath.Join(ws, finishedMarker)))
	fmt.Printf("RESULT: one Ctrl+C ended this turn only %s after the press; chat is still running\n",
		aborted.Round(time.Millisecond))
	second := time.Now()
	if err := cons.SendCtrlCByte(); err != nil {
		return fmt.Errorf("second Ctrl+C: %w", err)
	}
	if err := cons.WaitExit(opts.timeout); err != nil {
		return fmt.Errorf("suga did not exit within %s after the second Ctrl+C", opts.timeout)
	}
	fmt.Printf("RESULT: the second Ctrl+C %s after the first (window %s) left chat\n",
		second.Sub(start).Round(time.Millisecond), confirmWindow)
	return nil
}

// classicConsole is a plain Windows console (what AllocConsole gives): the
// harness is attached to it, so it can both read the console input mode and ask
// the console to deliver CTRL_C_EVENT.
type classicConsole struct {
	in    *os.File
	cmd   *exec.Cmd
	cmdIn *os.File
	out   *lockedBuffer
	modes *consoleModeLog
}

func newClassicConsole(bin, home, ws string, consoleOutput bool) (*classicConsole, error) {
	before, _, _ := procGetConsoleWindow.Call()
	_, _, _ = procFreeConsole.Call()
	ok, _, err := procAllocConsole.Call()
	after, _, _ := procGetConsoleWindow.Call()
	fmt.Printf("classic console: window %d -> %d (AllocConsole ok=%d err=%v)\n", before, after, ok, err)
	if ok == 0 {
		return nil, fmt.Errorf("AllocConsole: %v", err)
	}
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open CONIN$: %w", err)
	}
	cmdIn, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open CONIN$ for the child: %w", err)
	}
	buf := &lockedBuffer{}
	cmd := exec.Command(bin, "chat")
	cmd.Dir = ws
	cmd.Env = sugaEnv(home)
	cmd.Stdin = cmdIn
	if consoleOutput {
		// The chat's own output goes to the console, so colour detection, the
		// spinner and the input highlight are on exactly as in a real terminal.
		cmd.Env = sugaEnvColor(home)
		out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if err != nil {
			return nil, fmt.Errorf("open CONOUT$: %w", err)
		}
		cmd.Stdout, cmd.Stderr = out, out
	} else {
		cmd.Stdout, cmd.Stderr = buf, buf
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start suga: %w", err)
	}
	return &classicConsole{in: in, cmd: cmd, cmdIn: cmdIn, out: buf, modes: watchConsoleMode(in)}, nil
}

// TypeLine writes text plus Enter as console key events: the console hands them
// to whoever reads the input buffer, which is what a keyboard does.
func (c *classicConsole) TypeLine(text string) error {
	records := make([]inputRecord, 0, 2*len(text)+4)
	for _, r := range text {
		records = append(records, keyRecord(uint16(r), true), keyRecord(uint16(r), false))
	}
	records = append(records, keyRecord(vkReturn, true), keyRecord(vkReturn, false))
	var written uint32
	r, _, e := procWriteConsoleInputW.Call(c.in.Fd(), uintptr(unsafe.Pointer(&records[0])),
		uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
	if r == 0 {
		return fmt.Errorf("WriteConsoleInput: %v", e)
	}
	return nil
}

// SendCtrlCByte writes the Ctrl+C key into the console input buffer as a
// KEY_EVENT: with ENABLE_PROCESSED_INPUT off (raw, which suga sets while a turn
// runs with chat.steering) the console does not raise a CTRL_C_EVENT for the key
// but hands its character 0x03 to whoever reads - exactly what a person pressing
// Ctrl+C on a raw console gets.
func (c *classicConsole) SendCtrlCByte() error {
	records := []inputRecord{ctrlCKeyRecord(true), ctrlCKeyRecord(false)}
	var written uint32
	r, _, e := procWriteConsoleInputW.Call(c.in.Fd(), uintptr(unsafe.Pointer(&records[0])),
		uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
	if r == 0 {
		return fmt.Errorf("WriteConsoleInput: %v", e)
	}
	fmt.Printf("wrote the Ctrl+C key (char 0x03) to the console input at %s\n", time.Now().Format("15:04:05.000"))
	return nil
}

// SendCtrlC asks the console to deliver CTRL_C_EVENT to its processes: what it
// does itself for a real Ctrl+C while ENABLE_PROCESSED_INPUT is set.
func (c *classicConsole) SendCtrlC() error {
	// suga was started before this: the "ignore Ctrl+C" state is inherited by
	// children created afterwards, so the order matters.
	_, _, _ = procSetConsoleCtrlHandler.Call(0, 1)
	r, _, e := procGenerateConsoleCtrlEvent.Call(ctrlCEvent, 0)
	fmt.Printf("GenerateConsoleCtrlEvent(CTRL_C, console) r=%d err=%v at %s\n", r, e, time.Now().Format("15:04:05.000"))
	if r == 0 {
		return e
	}
	return nil
}

func (c *classicConsole) ModeReport() string {
	return describeMode(modeOf(c.in)) + "\ntimeline:\n" + c.modes.timeline()
}

func (c *classicConsole) Output() string { return c.out.String() }

func (c *classicConsole) WaitExit(d time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		fmt.Printf("suga exited: %v\n", err)
		return nil
	case <-time.After(d):
		return fmt.Errorf("still running after %s", d)
	}
}

func (c *classicConsole) Kill() {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.cmdIn != nil {
		_ = c.cmdIn.Close()
	}
	_ = c.in.Close()
}

// ptyConsole runs the program in a Windows pseudoconsole: a terminal, so input
// (and therefore Ctrl+C) is written as bytes into the input pipe.
type ptyConsole struct {
	pty *pty
}

func newPtyConsole(bin, home, ws string) (*ptyConsole, error) {
	p, err := startPty([]string{bin, "chat"}, sugaEnv(home), ws, 120, 30)
	if err != nil {
		return nil, err
	}
	return &ptyConsole{pty: p}, nil
}

// TypeLine sends the text and Enter (raw mode reads a carriage return).
func (c *ptyConsole) TypeLine(text string) error { return c.pty.Write(text + "\r") }

// SendCtrlC writes the byte the terminal sends when Ctrl+C is pressed. Whether
// that ends up as a CTRL_C_EVENT or as input to the program is exactly what the
// console mode decides.
func (c *ptyConsole) SendCtrlC() error {
	fmt.Printf("writing the Ctrl+C byte 0x03 to the pseudoconsole input at %s\n", time.Now().Format("15:04:05.000"))
	return c.pty.Write(ctrlCByte)
}

// SendCtrlCByte writes the byte the terminal sends for the Ctrl+C key: on a raw
// console (chat.steering on) that byte reaches the program as input, on a cooked
// one it becomes a CTRL_C_EVENT.
func (c *ptyConsole) SendCtrlCByte() error {
	fmt.Printf("writing the Ctrl+C byte 0x03 to the pseudoconsole input at %s\n", time.Now().Format("15:04:05.000"))
	return c.pty.Write(ctrlCByte)
}

// ModeReport cannot be read from outside: the input mode of a pseudoconsole
// lives in the console host, and the harness is not attached to it.
func (c *ptyConsole) ModeReport() string { return "n/a (pseudoconsole: only the program can read it)" }

func (c *ptyConsole) Output() string { return c.pty.Output() }

func (c *ptyConsole) WaitExit(d time.Duration) error { return c.pty.WaitExit(d) }

func (c *ptyConsole) Kill() { c.pty.Close() }

// ptyProbe checks the console primitives: a child that waits for a signal and
// reads its input is started in a pseudoconsole, then the Ctrl+C byte is
// written. A CTRL_C_EVENT means the child reports the signal; the byte means it
// arrives as input; neither means the key is lost while nothing reads the
// console (which is what `suga` looks like to a person pressing Ctrl+C during a
// turn).
func ptyProbe() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	log := &lockedBuffer{}
	p, err := startPtyCapture([]string{exe, "--self-child"}, os.Environ(), "", 100, 30, log)
	if err != nil {
		return err
	}
	defer p.Close()
	if err := waitForOutput(&probeOutput{log}, "child ready", promptWait); err != nil {
		return fmt.Errorf("child never started: %w (output %q)", err, log.String())
	}
	fmt.Printf("child output:\n%s\n", strings.TrimSpace(log.String()))
	fmt.Println("writing 0x03 into the pseudoconsole input (the Ctrl+C key)")
	if err := p.Write(ctrlCByte); err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	out := log.String()
	fmt.Printf("child output after Ctrl+C:\n%s\n", strings.TrimSpace(out))
	switch {
	case strings.Contains(out, "child got signal"):
		fmt.Println("RESULT: the Ctrl+C byte became a CTRL_C_EVENT (the cooked-console path works)")
	case strings.Contains(out, "child read byte 0x3"):
		fmt.Println("RESULT: the Ctrl+C byte arrived as plain input, no CTRL_C_EVENT was raised")
	default:
		fmt.Println("RESULT: the Ctrl+C byte was neither a signal nor readable input: it was lost")
		return fmt.Errorf("Ctrl+C had no effect in the pseudoconsole")
	}
	return nil
}

// selfChild is the program ptyProbe drives: it reports its console, waits for a
// signal and reads stdin, so any of the three outcomes is visible.
func selfChild() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	fmt.Printf("child ready pid=%d stdin-tty=%v stdout-tty=%v\n",
		os.Getpid(), isConsole(os.Stdin), isConsole(os.Stdout))
	type readResult struct {
		n   int
		b   byte
		err error
	}
	readCh := make(chan readResult, 1)
	go func() {
		var b [8]byte
		n, err := os.Stdin.Read(b[:])
		readCh <- readResult{n, b[0], err}
	}()
	select {
	case s := <-ch:
		fmt.Printf("child got signal %v\n", s)
		time.Sleep(200 * time.Millisecond)
		os.Exit(130)
	case r := <-readCh:
		fmt.Printf("child read byte 0x%x (n=%d err=%v)\n", r.b, r.n, r.err)
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	case <-time.After(30 * time.Second):
		fmt.Println("child timeout")
		os.Exit(1)
	}
}

func isConsole(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

// startPtyCapture is startPty with the output going into an extra buffer, so a
// caller can poll it.
func startPtyCapture(argv, env []string, dir string, w, h int16, buf *lockedBuffer) (*pty, error) {
	p, err := startPty(argv, env, dir, w, h)
	if err != nil {
		return nil, err
	}
	p.extra = buf
	return p, nil
}

// probeOutput adapts a lockedBuffer to waitForOutput.
type probeOutput struct{ buf *lockedBuffer }

func (o *probeOutput) Output() string { return o.buf.String() }

// deliverTurn starts the turn with `suga send --session <id>`: the chat is
// waiting at the prompt with a blocked read, exactly the suag-jsha setup.
func deliverTurn(opts options, home, ws, output string) error {
	session := sessionID(output)
	if session == "" {
		return fmt.Errorf("no session id in the chat output")
	}
	cmd := exec.Command(opts.bin, "send", "--session", session, "--text", opts.message)
	cmd.Dir = ws
	cmd.Env = sugaEnv(home)
	out, err := cmd.CombinedOutput()
	fmt.Printf("suga send --session %s: %v (%s)\n", session, err, strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("suga send: %w", err)
	}
	return nil
}

// sessionID reads the id chat prints at startup ("session id: sess_x").
func sessionID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(line, "session id: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// sugaEnvColor is sugaEnv without the switches that turn colour, the spinner and
// the input highlight off: the chat then behaves like it does for a person.
func sugaEnvColor(home string) []string {
	env := cleanEnv(os.Environ())
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "NO_COLOR", "TERM":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "SUGA_HOME="+home, "SUGA_HOOKS=off")
}

// sugaEnv is the environment for the chat under test: its own SUGA_HOME, no
// hooks, no colours (the output is asserted as text).
func sugaEnv(home string) []string {
	return append(cleanEnv(os.Environ()), "SUGA_HOME="+home, "SUGA_HOOKS=off", "TERM=xterm", "NO_COLOR=1")
}

// cleanEnv drops suga's own settings so the child reads the scratch config.
func cleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "SUGA_HOME", "SUGA_CONFIG", "SUGA_MODEL":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// prepare writes the isolated SUGA_HOME and workspace (its own git root, so suga
// does not pick up the enclosing repository).
func prepare(opts options, home, ws string, port int) error {
	if !opts.keep {
		_ = os.RemoveAll(home)
		_ = os.RemoveAll(ws)
	}
	for _, dir := range []string{home, ws} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	config := fmt.Sprintf(`model: fake/m1
providers:
  fake:
    type: openai
    base_url: http://127.0.0.1:%d/v1
    api_key: k
tools:
  allow_write: true
  approval: auto
memory:
  project:
    backend: files
`, port)
	if opts.scenario == "exit" {
		// The behaviour before steering: a turn leaves the console cooked, so Ctrl+C
		// raises SIGINT and ends chat.
		config += "chat:\n  steering: false\n"
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o644); err != nil {
		return err
	}
	init := exec.Command("git", "init", "-q", ws)
	if out, err := init.CombinedOutput(); err != nil {
		return fmt.Errorf("git init %s: %v (%s)", ws, err, out)
	}
	return nil
}

// consoleModeLog samples the console input mode and remembers the changes.
type consoleModeLog struct {
	mu      sync.Mutex
	start   time.Time
	entries []string
	mode    uint32
}

func watchConsoleMode(in *os.File) *consoleModeLog {
	log := &consoleModeLog{start: time.Now(), mode: modeOf(in)}
	log.entries = append(log.entries, fmt.Sprintf("  0ms 0x%x %s", log.mode, describeMode(log.mode)))
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for range tick.C {
			m := modeOf(in)
			log.mu.Lock()
			if m != log.mode {
				log.mode = m
				log.entries = append(log.entries, fmt.Sprintf("%4dms 0x%x %s",
					time.Since(log.start).Milliseconds(), m, describeMode(m)))
			}
			log.mu.Unlock()
		}
	}()
	return log
}

func (l *consoleModeLog) timeline() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.entries, "\n")
}

func modeOf(f *os.File) uint32 {
	var mode uint32
	_ = windows.GetConsoleMode(windows.Handle(f.Fd()), &mode)
	return mode
}

// describeMode names the flags that decide Ctrl+C handling and line editing.
func describeMode(mode uint32) string {
	flag := func(m uint32, name string) string {
		if mode&m != 0 {
			return name
		}
		return "-" + name
	}
	return fmt.Sprintf("[%s %s %s %s]",
		flag(enableProcessedInput, "PROCESSED_INPUT"), flag(enableLineInput, "LINE_INPUT"),
		flag(enableEchoInput, "ECHO_INPUT"), flag(enableVTInput, "VT_INPUT"))
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// tail keeps the last n lines: the output of one turn can be long.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{fmt.Sprintf("... %d earlier lines", len(lines)-n)}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n")
}

func waitForFile(path string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fileExists(path) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear within %s", path, d)
}

// waitForOutput waits until the program wrote something matching needle.
func waitForOutput(out interface{ Output() string }, needle string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(out.Output(), needle) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%q did not appear within %s", needle, d)
}

// keyRecord builds one KEY_EVENT: an upper-case virtual key with the character,
// which is what the console host reports for a typed character or Enter.
func keyRecord(char uint16, down bool) inputRecord {
	rec := inputRecord{EventType: keyEventType}
	k := keyEventRecord{RepeatCount: 1, VirtualKeyCode: char, UnicodeChar: char}
	if char >= 'a' && char <= 'z' {
		k.VirtualKeyCode = char - 'a' + 'A'
	}
	if down {
		k.KeyDown = 1
	}
	copy(rec.Event[:], (*[16]byte)(unsafe.Pointer(&k))[:])
	return rec
}

// ctrlCKeyRecord is the KEY_EVENT the console reports for a Ctrl+C press: the C
// key with the Ctrl state held, whose character is 0x03.
func ctrlCKeyRecord(down bool) inputRecord {
	rec := inputRecord{EventType: keyEventType}
	k := keyEventRecord{
		RepeatCount: 1, VirtualKeyCode: vkC, UnicodeChar: ctrlCChar,
		ControlKeyState: leftCtrlPressed,
	}
	if down {
		k.KeyDown = 1
	}
	copy(rec.Event[:], (*[16]byte)(unsafe.Pointer(&k))[:])
	return rec
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// startFakeModel serves an OpenAI-compatible chat endpoint on a random port: the
// first request (no tool result yet) asks for one shell_exec call, everything
// after it gets text, so the turn runs the tool and then finishes. The caller
// closes it; the port is what prepare writes into the config.
func startFakeModel(command string) (*http.Server, int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	var mu sync.Mutex
	requests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		lastRole := ""
		if len(req.Messages) > 0 {
			lastRole = req.Messages[len(req.Messages)-1].Role
		}
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		fmt.Printf("model request %d: last role %q stream=%v\n", n, lastRole, req.Stream)

		var message map[string]any
		finish := "stop"
		if lastRole != "tool" {
			args, _ := json.Marshal(map[string]any{"command": command})
			message = map[string]any{
				"role": "assistant", "content": nil,
				"tool_calls": []map[string]any{{
					"index": 0, "id": fmt.Sprintf("call_%d", n), "type": "function",
					"function": map[string]any{"name": "shell_exec", "arguments": string(args)},
				}},
			}
			finish = "tool_calls"
		} else {
			message = map[string]any{"role": "assistant", "content": "完成"}
		}
		usage := map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
		if !req.Stream {
			writeJSON(w, map[string]any{"id": "fake", "object": "chat.completion", "usage": usage,
				"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		delta := map[string]any{"role": "assistant"}
		if calls, ok := message["tool_calls"]; ok {
			delta["tool_calls"] = calls
		} else {
			delta["content"] = message["content"]
		}
		for _, chunk := range []any{
			map[string]any{"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}},
			map[string]any{"choices": []map[string]any{}, "usage": usage},
			"[DONE]",
		} {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(chunk))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return srv, port, nil
}

func mustJSON(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
