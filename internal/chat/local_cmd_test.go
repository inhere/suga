package chat

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// localRunner is the runner of chat's local commands on a scratch workspace
// (the CLI builds the same one with the configured shell and output limits).
func localRunner(t *testing.T) *native.LocalCommand {
	t.Helper()
	return native.NewLocalCommand(native.LocalCommandOptions{Workspace: t.TempDir()})
}

// outHasLine reports whether the chat printed line as a line of its own: the
// command's own output, as opposed to the echo of what was typed or a notice
// that mentions it. Each chat line starts with the prompt (the prompt read
// writes it without a newline when the output is not a terminal), so the
// comparison drops it.
func outHasLine(out, line string) bool {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(strings.TrimPrefix(l, promptText), "\r")
		if l == line {
			return true
		}
	}
	return false
}

// TestParseLocalCommand: only a line typed at the prompt that starts with "!"
// is a local command, and "!!" is the one that hands the result to the model. A
// leading space makes the line an ordinary message (that is what the chat trims
// off a prompt), and a "!" anywhere else is text too.
func TestParseLocalCommand(t *testing.T) {
	cases := []struct {
		name string
		line string
		ok   bool
		kind localCommandKind
		text string
	}{
		{"a run command", "!git status", true, localRun, "git status"},
		{"a send command", "!!go test ./...", true, localSend, "go test ./..."},
		{"a bare run command", "!", true, localRun, ""},
		{"a bare send command", "!!", true, localSend, ""},
		{"a command of spaces", "!   ", true, localRun, "   "},
		{"leading spaces make it a message", " !echo hi", false, 0, ""},
		{"a leading tab makes it a message", "\t!ls", false, 0, ""},
		{"plain text", "hello", false, 0, ""},
		{"a bang inside is text", "echo !hi", false, 0, ""},
		{"an exclamation as text", "wow!", false, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, ok := parseLocalCommand(c.line)
			assert.Eq(t, c.ok, ok)
			assert.Eq(t, c.kind, cmd.kind)
			assert.Eq(t, c.text, cmd.text)
		})
	}
}

// TestChatLocalCommandRunsWithoutTheModel: a "!cmd" line runs in the local
// shell, shows its output and starts no turn; a bare "!" only prints the usage,
// and a non-zero exit code is reported as a notice. The trace records every run
// as a local_command event.
func TestChatLocalCommandRunsWithoutTheModel(t *testing.T) {
	provider := &hookTestProvider{}
	runner, repo := newCLITestRunner(t, provider)
	var stdout, stderr bytes.Buffer
	input := strings.Join([]string{
		"!echo local-output-mark",
		"!exit 3",
		"!!", // no command
		"!",  // no command
		":quit",
	}, "\n") + "\n"
	assert.NoErr(t, Run(context.Background(), runner, Options{Local: localRunner(t)},
		strings.NewReader(input), &stdout, &stderr, nil))

	out := stdout.String()
	assert.True(t, outHasLine(out, "local-output-mark"), "the command's output is shown: %q", out)
	assert.StrContains(t, out, "本地命令退出码 3")
	assert.StrContains(t, out, testTranslator.T(localCommandHelpKey))
	provider.mu.Lock()
	assert.Eq(t, 0, len(provider.calls), "a local command never asks the model")
	provider.mu.Unlock()

	sessions, err := repo.ListSessions(context.Background())
	assert.NoErr(t, err)
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	events, err := repo.ListEvents(context.Background(),
		store.EventFilter{SessionID: sessions[0].ID, Kinds: []string{store.EventLocalCommand}})
	assert.NoErr(t, err)
	assert.Require(t, assert.Eq(t, 2, len(events)), "one event per command that ran")
	assert.Eq(t, "run", events[0].Name)
	assert.Eq(t, "echo local-output-mark", events[0].Attrs["command"])
	assert.Eq(t, float64(0), events[0].Attrs["exit_code"])
	assert.Eq(t, float64(3), events[1].Attrs["exit_code"])
	_, sent := events[0].Attrs["sent"]
	assert.False(t, sent, "a \"!\" command's result is not for the model")
}

// TestChatLocalCommandSendContext: a "!!cmd" command's result is handed to the
// model as additional context of the next message - every "!!" of this chat, in
// the order they ran, before the person's own text - and starts no turn of its
// own.
func TestChatLocalCommandSendContext(t *testing.T) {
	provider := &hookTestProvider{}
	runner, _ := newCLITestRunner(t, provider)
	var stdout, stderr bytes.Buffer
	input := strings.Join([]string{
		"!!echo first-mark",
		"!!echo second-mark",
		"你好",
		":quit",
	}, "\n") + "\n"
	assert.NoErr(t, Run(context.Background(), runner, Options{Local: localRunner(t)},
		strings.NewReader(input), &stdout, &stderr, nil))
	assert.StrContains(t, stdout.String(), testTranslator.T(localSendNoticeKey))

	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Require(t, assert.Eq(t, 1, len(provider.calls)), "both commands belong to the one turn that follows")
	var joined string
	for _, message := range provider.calls[0] {
		joined += message.Content + "\n"
	}
	assert.StrContains(t, joined, "[本地命令] echo first-mark")
	assert.StrContains(t, joined, "[本地命令] echo second-mark")
	assert.StrContains(t, joined, "退出码 0")
	first, second, message := strings.Index(joined, "first-mark"), strings.Index(joined, "second-mark"), strings.Index(joined, "你好")
	assert.True(t, 0 <= first && first < second && second < message,
		"the results come before the message, in order: %q", joined)
}

// TestChatLocalCommandContextDroppedOnExit: "!!" results collected for a message
// chat never sends are dropped when it ends (the commands themselves ran), with
// one warning on stderr.
func TestChatLocalCommandContextDroppedOnExit(t *testing.T) {
	provider := &hookTestProvider{}
	runner, _ := newCLITestRunner(t, provider)
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, Run(context.Background(), runner, Options{Local: localRunner(t)},
		strings.NewReader("!!echo dropped-mark\n:quit\n"), &stdout, &stderr, nil))

	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Eq(t, 0, len(provider.calls))
	assert.True(t, outHasLine(stdout.String(), "dropped-mark"), "the command itself ran: %q", stdout.String())
	assert.StrContains(t, stderr.String(), "local command results (!!) were not sent to the model")
}

// TestChatLocalCommandWithoutRunner: a chat without a local command runner (run,
// ACP, a runtime that does not publish one) passes a "!cmd" line to the model as
// an ordinary message, exactly like before the commands existed.
func TestChatLocalCommandWithoutRunner(t *testing.T) {
	provider := &hookTestProvider{}
	runner, _ := newCLITestRunner(t, provider)
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, Run(context.Background(), runner, Options{},
		strings.NewReader("!echo local-output-mark\n:quit\n"), &stdout, &stderr, nil))

	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Require(t, assert.Eq(t, 1, len(provider.calls)))
	var found bool
	for _, message := range provider.calls[0] {
		if strings.Contains(message.Content, "!echo local-output-mark") {
			found = true
		}
	}
	assert.True(t, found, "the line should reach the model as a message")
	assert.False(t, outHasLine(stdout.String(), "local-output-mark"))
}

// TestChatLocalCommandInjectedLine: a line that came from elsewhere (here the
// block reason of a Stop hook, the way a `suga send` delivery arrives) is never
// a local command, whatever it starts with: the text reaches the model as a
// message and nothing runs. Without the `case injected:` guard of Run's loop the
// line would be executed instead of being answered.
func TestChatLocalCommandInjectedLine(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	provider := &hookTestProvider{}
	runner, repo := newCLITestRunner(t, provider)
	hooks := newFakeHookClient()
	hooks.stopReason = "!echo injected-mark"

	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{Local: localRunner(t)}, reader, &stdout, &stderr, hooks)
	}()
	if _, err := io.WriteString(writer, "first prompt\n"); err != nil {
		t.Fatal(err)
	}
	// The hook's reply starts a turn of its own: the text was taken as a prompt.
	waitProviderCalls(t, provider, 2)
	if _, err := io.WriteString(writer, ":quit\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	assert.NoErr(t, <-done)

	assert.False(t, outHasLine(stdout.String(), "injected-mark"), "the command must not run")
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Require(t, assert.Eq(t, 2, len(provider.calls)))
	var found bool
	for _, message := range provider.calls[1] {
		if strings.Contains(message.Content, "!echo injected-mark") {
			found = true
		}
	}
	assert.True(t, found, "the reply should reach the model as a user message")

	sessions, err := repo.ListSessions(context.Background())
	assert.NoErr(t, err)
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	events, err := repo.ListEvents(context.Background(),
		store.EventFilter{SessionID: sessions[0].ID, Kinds: []string{store.EventLocalCommand}})
	assert.NoErr(t, err)
	assert.Eq(t, 0, len(events), "an injected line runs nothing")
}

// TestChatLocalCommandDeliveredIsNotACommand: a message delivered by `suga send`
// (here through the session socket) is an ordinary message even when it starts
// with "!": the model receives it as text and no command runs.
func TestChatLocalCommandDeliveredIsNotACommand(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	sugaHome := shortTestHome(t)
	t.Setenv("SUGA_HOME", sugaHome)
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	processRoot := filepath.Join(sugaHome, "run")
	provider := &hookTestProvider{}
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: provider,
		LockRoot: filepath.Join(processRoot, "locks"), ProcessRoot: processRoot,
	})
	assert.Require(t, assert.NoErr(t, err))

	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{Local: localRunner(t)}, reader, &stdout, &stderr, nil)
	}()

	record := waitForProcessRecord(t, processRoot)
	if _, err := personalprocess.Send(record, personalprocess.Request{Text: "!echo delivered-mark", From: "cli"}); err != nil {
		t.Fatalf("deliver the text: %v", err)
	}
	waitProviderCalls(t, provider, 1) // the delivered line ran as a turn
	if _, err := io.WriteString(writer, ":quit\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	assert.NoErr(t, <-done)

	assert.False(t, outHasLine(stdout.String(), "delivered-mark"), "the delivered text must not run")
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Require(t, assert.Eq(t, 1, len(provider.calls)))
	var found bool
	for _, message := range provider.calls[0] {
		if strings.Contains(message.Content, "!echo delivered-mark") {
			found = true
		}
	}
	assert.True(t, found, "the delivered text should reach the model as a message")

	sessions, err := repo.ListSessions(context.Background())
	assert.NoErr(t, err)
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	events, err := repo.ListEvents(context.Background(),
		store.EventFilter{SessionID: sessions[0].ID, Kinds: []string{store.EventLocalCommand}})
	assert.NoErr(t, err)
	assert.Eq(t, 0, len(events), "a delivered line runs nothing")
}

// TestRunTurnRefusesLocalCommand: a "!cmd" line typed at the ↳ input line while a
// turn runs runs nothing (the prompt belongs to the turn): chat says so and the
// line goes back to the input, for the next prompt - it is not inserted into the
// running turn either.
func TestRunTurnRefusesLocalCommand(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	h.state.local = localRunner(t)
	done := h.startTurn()
	<-h.cast.started()

	h.kb.Type("!echo steer-mark\r")
	waitUntil(t, "the refusal hint", func() bool {
		return strings.Contains(h.out.String(), localCommandBusyHint)
	})
	h.cast.letGo()

	assert.NoErr(t, <-done)
	assert.Eq(t, "!echo steer-mark", h.turnInputLine(t), "the line waits in the input")
	assert.False(t, outHasLine(h.out.String(), "steer-mark"), "the command must not run")
	assert.Eq(t, []string{"干活"}, h.cast.prompts(), "it is not a message of the running turn")
}

// TestRunLocalCommandStopsOnCtrlC: Ctrl-C during a local command kills the
// command (the chat keeps its keyboard raw for it, so the press arrives as a
// byte) and chat goes on at its prompt.
func TestRunLocalCommandStopsOnCtrlC(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	h.state.local = localRunner(t)
	done := make(chan struct{})
	go func() {
		h.state.runLocalCommand(localCommand{kind: localRun, text: "sleep 30"})
		close(done)
	}()
	// The command's own read is up at once. The press comes a moment later, so it
	// ends a command that is already running (a shell that has not started yet is
	// the other path, also a cancellation).
	time.Sleep(300 * time.Millisecond)
	h.kb.Type("\x03")
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Ctrl-C did not end the local command")
	}
	assert.StrContains(t, h.out.String(), testTranslator.T(localStoppedNoticeKey))
	// Chat is still there: the next prompt read is served.
	assert.True(t, h.source.Request("❯ "))
}

// TestRunLocalCommandCtrlCWithTextOnTheLine: the line typed while a local command
// runs belongs to that command (it goes back to the input when the command is
// over), so Ctrl-C ends the command even with text on it - unlike the prompt and
// the reads of a turn, where a non-empty line is what Ctrl-C drops (wll1).
func TestRunLocalCommandCtrlCWithTextOnTheLine(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	h.state.local = localRunner(t)
	done := make(chan struct{})
	go func() {
		h.state.runLocalCommand(localCommand{kind: localRun, text: "sleep 30"})
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	h.kb.Type("半行\x03") // typed, then the press that stops the command
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Ctrl-C with text on the line did not end the local command")
	}
	assert.StrContains(t, h.out.String(), testTranslator.T(localStoppedNoticeKey))
	// What was typed is not lost: the next prompt read carries it.
	assert.Eq(t, "半行", h.turnInputLine(t))
}

// TestRunLocalCommandCtrlCAbortCarriesTheLine: the Ctrl-C that ends a local
// command's read reports the line it was typed on instead of leaving the text
// to the editor buffer. The abort event carries the line (the read reports it
// and the editor is cleared on the way), so the half-typed line still reaches
// the next prompt whichever goroutine gets there first - the contract behind
// TestRunLocalCommandCtrlCWithTextOnTheLine, driven here without the command
// and its watcher, on the read and the event alone.
func TestRunLocalCommandCtrlCAbortCarriesTheLine(t *testing.T) {
	h := newSteerHarness(t, newGateProv())
	_, ok := h.source.RequestCommandKeys()
	assert.True(t, ok)
	waitUntil(t, "the read waits for input", func() bool { return h.kb.Reading() > 0 })

	h.kb.Type("半行\x03") // typed, then the press that ends the command
	var event chatInputEvent
	select {
	case event = <-h.source.TurnEvents():
	case <-time.After(10 * time.Second):
		t.Fatal("the abort event did not arrive")
	}
	assert.Eq(t, keyAbort, event.key)
	assert.Eq(t, "半行", event.line, "the abort event carries the line")
	// The editor keeps nothing of it: the line rides on the event.
	assert.Eq(t, "", h.turnInputLine(t))

	// What was typed comes back on the input line, the way the command's watcher
	// puts it back: whatever the next prompt read is, the text is there.
	h.source.putBackLine(event.line)
	assert.Eq(t, "半行", h.turnInputLine(t))
}
