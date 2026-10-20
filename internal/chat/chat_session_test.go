package chat

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/testutil"
)

// shortTestHome returns a short SUGA_HOME (unix socket paths have a length limit).
func shortTestHome(t *testing.T) string {
	t.Helper()
	return testutil.Dir(t)
}

// newCLITestRunner builds a runner on a throwaway store (same helper cli tests use).
func newCLITestRunner(t *testing.T, provider agentcore.Provider, hooks ...agentcore.Hook) (*personal.Runner, *store.Store) {
	t.Helper()
	repo, err := store.Open(newTestStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{
		Repository: repo,
		Provider:   provider,
		Hooks:      hooks,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner, repo
}

// TestPersonalChatContextNoticeOnce: the resident context warnings chat is given
// are printed once at session start, on stderr, and never enter the
// conversation output.
func TestPersonalChatContextNoticeOnce(t *testing.T) {
	runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Final("hi")))
	var stdout, stderr bytes.Buffer
	notice := "提示：项目规则已截断（原 10 字节，保留 5）；运行 suga inspect manifest 查看各来源文件。"
	err := Run(context.Background(), runner, Options{ContextNotices: []string{notice}},
		strings.NewReader(":quit\n"), &stdout, &stderr, nil)
	assert.NoErr(t, err)
	assert.Eq(t, 1, strings.Count(stderr.String(), notice))
	assert.False(t, strings.Contains(stdout.String(), "inspect manifest"))
}

func TestPersonalChatReusesOneSession(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("first"), mock.Final("second")))
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), runner, Options{},
		strings.NewReader("Hello\nFollow up\n:quit\n"), &stdout, &stderr, nil)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := repo.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, 1, len(sessions))
	assert.Eq(t, true, strings.Contains(stdout.String(), "first"))
	assert.Eq(t, true, strings.Contains(stdout.String(), "second"))
	// Two prompts in one chat session are two completed turns of that session.
	turns, err := repo.ListTurns(context.Background(), sessions[0].ID, store.TurnCompleted)
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, 2, len(turns))
}

// TestPersonalChatExitCommand: :exit leaves chat exactly like :quit, at that
// very line. The :session line behind it is never read as a prompt (that would
// start a second turn).
func TestPersonalChatExitCommand(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("first"), mock.Final("second")))
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, Run(context.Background(), runner, Options{},
		strings.NewReader("Hello\n:exit\n:session\n"), &stdout, &stderr, nil))

	sessions, err := repo.ListSessions(context.Background())
	assert.NoErr(t, err)
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	turns, err := repo.ListTurns(context.Background(), sessions[0].ID, store.TurnCompleted)
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(turns))
	assert.StrContains(t, stdout.String(), "first")
	assert.StrNotContains(t, stdout.String(), "session id:")
}

// TestPersonalChatResumeShowsLastReply: continuing an existing session prints
// the model's last reply above the first prompt, so the person can go on from
// it; a new session prints nothing of the kind.
func TestPersonalChatResumeShowsLastReply(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("第一次的回答")))
	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, Run(ctx, runner, Options{}, strings.NewReader("你好\n:quit\n"), &stdout, &stderr, nil))
	assert.StrNotContains(t, stdout.String(), "上一次回复")

	sessions, err := repo.ListSessions(ctx)
	assert.NoErr(t, err)
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	stdout.Reset()
	assert.NoErr(t, Run(ctx, runner, Options{ResumeID: sessions[0].ID}, strings.NewReader(":quit\n"), &stdout, &stderr, nil))

	got := stdout.String()
	assert.StrContains(t, got, "上一次回复：\n▣ 第一次的回答")
	// Above the first prompt: it must read as history, not as an answer to input.
	assert.True(t, strings.Index(got, "第一次的回答") < strings.Index(got, "❯ "))
	// That turn ended normally, so the note stays away.
	assert.StrNotContains(t, got, "上一回合未正常结束")
}

// newTestStorePath returns a fresh core store path under the test's temp dir.
func newTestStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "db", "sessions.db")
}
