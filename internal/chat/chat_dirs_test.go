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

	"github.com/inhere/suga/internal/adddir"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// fakeSessionDirs is the chat side's view of the session's added directories: it
// records what the commands asked for and answers like the CLI's handle does
// (the workspace first, then the added ones).
type fakeSessionDirs struct {
	workspace string
	dirs      []adddir.Dir
	added     []string
	removed   []string
}

func (f *fakeSessionDirs) Workspace() string { return f.workspace }

func (f *fakeSessionDirs) List(context.Context) ([]adddir.Dir, error) {
	out := make([]adddir.Dir, 0, len(f.dirs)+1)
	if f.workspace != "" {
		out = append(out, adddir.Dir{Path: f.workspace, Writable: true})
	}
	return append(out, f.dirs...), nil
}

func (f *fakeSessionDirs) Add(_ context.Context, value, source string) (adddir.Dir, error) {
	dir, err := adddir.Entry(value)
	if err != nil {
		return adddir.Dir{}, err
	}
	dir.Source = source
	f.added = append(f.added, value+" from "+source)
	f.dirs = adddir.Merge(f.dirs, []adddir.Dir{dir})
	return dir, nil
}

func (f *fakeSessionDirs) Remove(_ context.Context, value string) (bool, error) {
	f.removed = append(f.removed, value)
	path := adddir.Expand(adddir.StripMode(value))
	had := adddir.Has(f.dirs, path)
	f.dirs = adddir.Without(f.dirs, path)
	return had, nil
}

// TestChatDirCommands covers the local keyboard commands that show and change
// the session's added directories: they answer on the chat output, they never
// start a turn, and a malformed one only prints the usage.
func TestChatDirCommands(t *testing.T) {
	dir := t.TempDir()
	handle := &fakeSessionDirs{workspace: filepath.FromSlash("/ws")}
	runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	var stdout, stderr bytes.Buffer
	input := strings.Join([]string{
		":dirs",                   // empty set
		":add-dir " + dir + ":rw", // authorize read-write
		":add-dir " + dir,         // same path again, read-only now
		":dirs",                   // the listing
		":rm-dir " + dir + ":ro",  // revoke with the suffix :dirs printed
		":rm-dir " + dir,          // already gone
		":add-dir docs",           // not absolute
		":add-dir",                // no argument
		":dirs extra",             // no argument expected
		"hello",                   // a real turn
		":quit",
	}, "\n") + "\n"
	assert.NoErr(t, Run(context.Background(), runner, Options{Dirs: handle},
		strings.NewReader(input), &stdout, &stderr, nil))

	out := stdout.String()
	assert.StrContains(t, out, "工作区："+filepath.FromSlash("/ws")+"（读写）")
	assert.StrContains(t, out, "附加目录：无")
	assert.StrContains(t, out, "已授权："+dir+"（读写）")
	assert.StrContains(t, out, "已授权："+dir+"（只读）")
	assert.StrContains(t, out, "  "+dir+"（只读 · 会话中追加）")
	assert.StrContains(t, out, "已撤销："+dir)
	assert.StrContains(t, out, "MODEL_ANSWER") // the ordinary line still runs a turn
	assert.Eq(t, []string{dir + ":rw from " + adddir.SourceSession, dir + " from " + adddir.SourceSession}, handle.added)
	assert.Eq(t, []string{dir + ":ro", dir}, handle.removed)

	errOut := stderr.String()
	assert.StrContains(t, errOut, ":add-dir: \"docs\" is not an absolute path")
	assert.StrContains(t, errOut, dirCommandHelp)
	assert.StrContains(t, errOut, ":rm-dir："+dir+" 不在附加目录中")
}

// TestChatDirCommandsWithoutHandle: without the session's directory handle (run,
// ACP, a runtime that does not publish one) a ":add-dir" line is an ordinary
// message, exactly like before the commands existed.
func TestChatDirCommandsWithoutHandle(t *testing.T) {
	runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, Run(context.Background(), runner, Options{},
		strings.NewReader(":add-dir /tmp/x\n:quit\n"), &stdout, &stderr, nil))
	assert.StrContains(t, stdout.String(), "MODEL_ANSWER")
	assert.StrNotContains(t, stdout.String(), "已授权")
	assert.StrNotContains(t, stderr.String(), "已授权")
}

// TestChatHookReplyIsNeverCommand: an injected line that carries no delivery
// label, a Stop hook's block reason, is an ordinary message as well. Such a
// reply reading ":add-dir <目录>" reaches the model as that prompt and
// authorizes nothing; without the keyboard-only guard (the `case injected:` of
// the command switch in chat.go) chat would run it as a command instead, print
// 已授权 and start no turn, which is what this test fails on.
func TestChatHookReplyIsNeverCommand(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	dir := t.TempDir()
	handle := &fakeSessionDirs{workspace: filepath.FromSlash("/ws")}
	provider := &hookTestProvider{}
	runner, _ := newCLITestRunner(t, provider)
	hooks := newFakeHookClient()
	hooks.stopReason = ":add-dir " + dir

	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{Dirs: handle}, reader, &stdout, &stderr, hooks)
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

	assert.Empty(t, handle.added)
	assert.Empty(t, handle.removed)
	assert.StrNotContains(t, stdout.String(), "已授权")
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Require(t, assert.Eq(t, 2, len(provider.calls)))
	var found bool
	for _, message := range provider.calls[1] {
		if strings.Contains(message.Content, ":add-dir "+dir) {
			found = true
		}
	}
	assert.True(t, found, "the hook's reply should reach the model as a user message")
}

// TestChatDeliveredCommandIsNeverCommand: a message delivered by `suga send`
// (here through the session socket) is always an ordinary message, so a
// delivered ":add-dir /etc" authorizes nothing and a delivered ":quit" does not
// end the chat.
func TestChatDeliveredCommandIsNeverCommand(t *testing.T) {
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

	handle := &fakeSessionDirs{workspace: filepath.FromSlash("/ws")}
	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{Dirs: handle}, reader, &stdout, &stderr, nil)
	}()

	record := waitForProcessRecord(t, processRoot)
	for _, text := range []string{":add-dir /etc", ":quit"} {
		if _, err := personalprocess.Send(record, personalprocess.Request{Text: text, From: "cli"}); err != nil {
			t.Fatalf("deliver %q: %v", text, err)
		}
	}
	// Both delivered lines reach the model: as two turns, or as one when the second
	// arrives before the chat took the first (queued deliveries merge into one turn,
	// design session-message-delivery D3).
	waitProviderText(t, provider, ":quit")
	if _, err := io.WriteString(writer, ":quit\n"); err != nil { // typed here: this one exits
		t.Fatal(err)
	}
	_ = writer.Close()
	assert.NoErr(t, <-done)

	assert.Empty(t, handle.added)
	assert.Empty(t, handle.removed)
	assert.StrNotContains(t, stdout.String(), "已授权")
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Require(t, assert.True(t, len(provider.calls) >= 1 && len(provider.calls) <= 2))
	for _, want := range []string{":add-dir /etc", ":quit"} {
		var found bool
		for _, call := range provider.calls {
			for _, message := range call {
				if strings.Contains(message.Content, want) {
					found = true
				}
			}
		}
		assert.True(t, found, "a model call should carry the delivered text %q", want)
	}
}

// waitProviderText waits until a model call carries text.
func waitProviderText(t *testing.T, provider *hookTestProvider, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		for _, call := range provider.calls {
			for _, message := range call {
				if strings.Contains(message.Content, text) {
					provider.mu.Unlock()
					return
				}
			}
		}
		provider.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no model call carried %q", text)
}
