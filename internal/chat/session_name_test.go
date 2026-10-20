package chat

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

func TestResolveChatShortName(t *testing.T) {
	constant := func(suffix string) personalprocess.SuffixSource {
		return func() string { return suffix }
	}
	// Another live chat process of the same machine, holding "taken-aa".
	otherLive := []personalprocess.Record{{PID: 4242, SessionID: "sess-other", Name: "taken-aa"}}

	t.Run("no stored name generates one and asks to save it", func(t *testing.T) {
		name, save, notice := resolveChatShortName("", nil, filepath.Join("tmp", "My Work"), constant("k7"))
		assert.Eq(t, "mywork-k7", name)
		assert.Eq(t, true, save)
		assert.Eq(t, "", notice)
	})
	t.Run("a stored name is reused as is", func(t *testing.T) {
		name, save, notice := resolveChatShortName("ws-k7", otherLive, "other", constant("zz"))
		assert.Eq(t, "ws-k7", name)
		assert.Eq(t, false, save)
		assert.Eq(t, "", notice)
	})
	t.Run("a stored name taken by another process gets a temporary one", func(t *testing.T) {
		name, save, notice := resolveChatShortName("taken-aa", otherLive, filepath.Join("tmp", "My Work"), constant("k7"))
		assert.Eq(t, "mywork-k7", name)
		assert.Eq(t, false, save) // the stored name is never overwritten
		assert.StrContains(t, notice, "taken-aa")
		assert.StrContains(t, notice, "mywork-k7")
	})
	t.Run("the own process record does not make a name taken", func(t *testing.T) {
		own := []personalprocess.Record{{PID: os.Getpid(), SessionID: "sess-me", Name: "ws-k7"}}
		name, save, _ := resolveChatShortName("ws-k7", own, "other", constant("zz"))
		assert.Eq(t, "ws-k7", name)
		assert.Eq(t, false, save)
	})
}

// TestChatShowsAndRegistersStoredShortName: a session that already has a short
// name registers under it again (a resume), shows it in the banner and in
// :session, and its process record keeps it instead of the first message.
func TestChatShowsAndRegistersStoredShortName(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := shortTestHome(t)
	processRoot := filepath.Join(home, "run")
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: mock.NewProvider(mock.Final("ok")), ProcessRoot: processRoot,
	})
	assert.Require(t, assert.NoErr(t, err))
	ctx := context.Background()
	model, err := runner.NewSession(ctx, "sess-named", "New chat")
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, runner.SaveSessionShortName(ctx, model.ID, "ws-k7"))

	// The pipe keeps chat waiting at the prompt while the record is read.
	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, runner, Options{SessionID: model.ID}, reader, &stdout, &stderr, nil)
	}()
	var record personalprocess.Record
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		records, err := personalprocess.List(processRoot)
		if err == nil && len(records) == 1 {
			record = records[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Eq(t, "ws-k7", record.Name)
	assert.Eq(t, model.ID, record.SessionID)

	_, err = io.WriteString(writer, ":session\n:quit\n")
	assert.NoErr(t, err)
	assert.NoErr(t, writer.Close())
	assert.NoErr(t, <-done)
	assert.StrContains(t, stdout.String(), "Suga chat · 会话 ws-k7\n")
	assert.StrContains(t, stdout.String(), "session id: "+model.ID+" · 会话 ws-k7")
	// The exit summary names the session next to the resume command.
	assert.StrContains(t, stderr.String(), "会话 ws-k7 · 续接：suga chat --resume "+model.ID+"\n")
}

// TestChatNamesTheSessionAfterItsWorkspace: a new chat generates a name from the
// workspace directory recorded on the session (config `workspace`), cleans it and
// stores it; the directory chat was started in only serves as a fallback.
func TestChatNamesTheSessionAfterItsWorkspace(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := shortTestHome(t)
	processRoot := filepath.Join(home, "run")
	// A workspace directory needing cleaning: upper case and an underscore.
	workspace := filepath.Join(t.TempDir(), "My_Work")
	assert.NoErr(t, os.MkdirAll(workspace, 0o700))
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: mock.NewProvider(mock.Final("ok")),
		Workspace: workspace, ProcessRoot: processRoot,
	})
	assert.Require(t, assert.NoErr(t, err))

	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{}, reader, &stdout, &stderr, nil)
	}()
	var record personalprocess.Record
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		records, err := personalprocess.List(processRoot)
		if err == nil && len(records) == 1 {
			record = records[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.StrContains(t, record.Name, "mywork-")
	// Stored on the first registration, so a resume keeps the address. The
	// process record is written before the session meta, so wait for the meta
	// too instead of reading it the moment the record appears.
	var saved string
	for time.Now().Before(deadline) {
		if saved, err = runner.SessionShortName(context.Background(), record.SessionID); err == nil && saved != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.NoErr(t, err)
	assert.Eq(t, record.Name, saved)

	_, err = io.WriteString(writer, ":quit\n")
	assert.NoErr(t, err)
	assert.NoErr(t, writer.Close())
	assert.NoErr(t, <-done)
}
