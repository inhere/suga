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
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/schedule"
)

// The English side of the chat status output (language: en): the banner, the
// end-of-turn status line, the continued session's last reply, the idle
// consolidation, the steering/follow-up labels, the waiting line and the session
// short name notes. Every case drives the real caller (the renderer, chat.Run or
// the function chat itself calls) with an English catalog injected, so a text
// that never reaches the translator fails here.

// TestChatENBannerAndSessionLine: a chat started with the English catalog prints
// its banner and `:session` line in English, through chat.Run.
func TestChatENBannerAndSessionLine(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := shortTestHome(t)
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: mock.NewProvider(mock.Final("ok")),
		ProcessRoot: filepath.Join(home, "run"),
	})
	assert.Require(t, assert.NoErr(t, err))
	ctx := context.Background()
	model, err := runner.NewSession(ctx, "sess-en", "New chat")
	assert.Require(t, assert.NoErr(t, err))
	assert.NoErr(t, runner.SaveSessionShortName(ctx, model.ID, "ws-k7"))

	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, runner, Options{SessionID: model.ID, Translator: enTranslator(t)}, reader, &stdout, &stderr, nil)
	}()
	_, err = io.WriteString(writer, ":session\n:quit\n")
	assert.NoErr(t, err)
	assert.NoErr(t, writer.Close())
	assert.NoErr(t, <-done)

	assert.StrContains(t, stdout.String(), "Suga chat · session ws-k7\n")
	assert.StrContains(t, stdout.String(), "Type a task to start")
	assert.StrContains(t, stdout.String(), "Tools: read-only")
	assert.StrContains(t, stdout.String(), "session id: "+model.ID+" · session ws-k7")
	assert.NotContains(t, stdout.String(), "会话")
}

// TestChatENTurnStatus: the end-of-turn status line of an English chat names the
// inserted messages and the output limit in English, through Renderer.Status.
func TestChatENTurnStatus(t *testing.T) {
	plainColor(t)
	var out bytes.Buffer
	r := newRenderer(rendererOptions{Out: &out, Translator: enTranslator(t), Provider: "openai", Model: "gpt"})
	r.Status(personal.RunResult{
		Usage:       agentcore.Usage{InputTokens: 1200, OutputTokens: 34},
		Truncated:   true,
		OutputLimit: 1000,
	}, 2*time.Second, 2)

	got := out.String()
	assert.StrContains(t, got, "steers 2")
	assert.StrContains(t, got, "output limit reached (1000 tokens), answer truncated")
	assert.NotContains(t, got, "插话")
	assert.NotContains(t, got, "输出达到上限")
}

// TestChatENLastReply: the notes above a continued session's last reply are
// English, through Renderer.LastReply (the reply body itself is the model's text).
func TestChatENLastReply(t *testing.T) {
	plainColor(t)
	var out bytes.Buffer
	r := newRenderer(rendererOptions{Out: &out, Translator: enTranslator(t)})
	long := strings.Repeat("line\n", replyTailLines+5)
	r.LastReply(long, "sess-1", true)

	got := out.String()
	assert.StrContains(t, got, "Previous reply:")
	assert.StrContains(t, got, "Previous turn did not end normally")
	assert.StrContains(t, got, "First 5 lines truncated; full record: suga inspect trace sess-1")
	assert.NotContains(t, got, "上一次回复")
}

// TestChatENIdleConsolidation: the idle consolidation lines of an English chat
// are English, through chat.Run with the idle task on a fake clock.
func TestChatENIdleConsolidation(t *testing.T) {
	prov := newIdleTestProvider(100000) // ~0.89 of the threshold
	h := startIdleChat(t, prov, 0.5, func(opts *Options) { opts.Translator = enTranslator(t) })
	h.send("hello")
	h.waitEvents(schedule.Resumed, 1)
	h.clock.Advance(idleTestAfter)
	h.waitOutput("✦ context consolidated: ")
	assert.StrContains(t, h.out.String(), "✦ idle consolidation in progress…")
	assert.NotContains(t, h.out.String(), "空闲整理")
	h.waitEvents(schedule.Done, 1)
	h.quit()
}

// TestChatENSteerAndFollowUpLabels: the lines of a message inserted into the
// running turn and of one queued with Tab are English, through the renderer the
// steering code calls.
func TestChatENSteerAndFollowUpLabels(t *testing.T) {
	plainColor(t)
	var out bytes.Buffer
	r := newRenderer(rendererOptions{Out: &out, Translator: enTranslator(t)})
	r.SteerWaiting("fix the test")
	r.SteerWithdrawn("fix the test")
	r.FollowUpQueued("then run the suite")
	r.FollowUpSent("then run the suite")

	got := out.String()
	// The English suffix is a separate word group, so the catalog separates it from
	// the text with a space (approval.render.interjection = "[interjection] {text}
	// {suffix}"); the Chinese one keeps its full-width bracket glued to the text.
	assert.StrContains(t, got, "[interjection] fix the test (waiting for delivery)")
	assert.StrContains(t, got, "[interjection] fix the test (withdrawn)")
	assert.StrContains(t, got, "[queued] then run the suite (sent after this turn)")
	assert.StrContains(t, got, "[queued] then run the suite (sent)")
	assert.NotContains(t, got, "fix the test(")
	assert.NotContains(t, got, "插话")
	assert.NotContains(t, got, "排队")
}

// TestChatENSpinner: the waiting line of an English chat says what the turn waits
// for in English, both its own label and the one a running tool gives it.
func TestChatENSpinner(t *testing.T) {
	t.Run("the waiting line of its own", func(t *testing.T) {
		out, clock := &syncOutput{}, newSpinnerClock()
		spin := newThinkingSpinner(spinnerConfig{
			enabled: true, out: out, translator: enTranslator(t), now: clock.Now, ticks: clock.ticks,
		})
		spin.Start()
		clock.tick(time.Second)
		assert.Require(t, waitOutput(t, out, "thinking… 1s"))
		spin.Stop()
		assert.StrNotContains(t, out.String(), spinnerLabel)
	})

	t.Run("the status line names the running tool", func(t *testing.T) {
		plainColor(t)
		r, log, clock := newStatusRenderer(t, rendererOptions{Spinner: true, Color: true, Translator: enTranslator(t)})
		r.BeginTurn()
		clock.tick(time.Second)
		assert.Require(t, waitStatusContains(t, log, "Thinking… 1s"))

		r.Handle(personal.Event{Kind: "tool_call", ToolName: "shell_exec", Text: `{"command":"echo hi"}`})
		assert.StrContains(t, log.last(), "running shell_exec…")
		assert.NotContains(t, log.last(), "执行")
		r.Status(personal.RunResult{}, time.Second, 0)
	})
}

// TestChatENToolSummary: the tool lines whose summary names a count say it in
// English, through the renderer's tool_call path.
func TestChatENToolSummary(t *testing.T) {
	plainColor(t)
	for _, tc := range []struct {
		name, tool, args, want string
	}{
		{name: "file_write", tool: "file_write", args: `{"path":"a.go","content":"one\ntwo"}`,
			want: "⏺ file_write a.go (7 bytes, 2 lines)"},
		{name: "file_edit", tool: "file_edit", args: `{"path":"a.go","old_string":"a","new_string":"b"}`,
			want: "⏺ file_edit a.go (1 replacements)"},
		{name: "web_fetch urls", tool: "web_fetch", args: `{"urls":["https://example.com/a","https://example.com/b"]}`,
			want: "⏺ web_fetch 2 URLs: https://example.com/a, …"},
		{name: "shell_exec watch", tool: "shell_exec",
			args: `{"command":"go test ./x","watch_paths":["**/*.go","./docs"]}`,
			want: "⏺ shell_exec go test ./x (watch 2 paths)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			newRenderer(rendererOptions{Out: &out, Translator: enTranslator(t)}).Handle(personal.Event{
				Kind: "tool_call", ToolName: tc.tool, Text: tc.args})
			assert.Eq(t, tc.want+"\n", out.String())
		})
	}
}

// TestChatENCommandError: the line of a failed :command that was interrupted is
// English too (the display only; run, ACP and stream-json keep the raw error).
func TestChatENCommandError(t *testing.T) {
	assert.Eq(t, "Compact: Interrupted", formatCommandError(enTranslator(t), "Compact", context.Canceled))
}

// TestChatENSessionNameNotice: the note about a stored short name another process
// holds is English, through resolveChatShortName (registerChatProcess calls it).
func TestChatENSessionNameNotice(t *testing.T) {
	constant := func(suffix string) personalprocess.SuffixSource {
		return func() string { return suffix }
	}
	otherLive := []personalprocess.Record{{PID: 4242, SessionID: "sess-other", Name: "taken-aa"}}

	name, save, notice := resolveChatShortName("taken-aa", otherLive, filepath.Join("tmp", "My Work"), constant("k7"), enTranslator(t))
	assert.Eq(t, "mywork-k7", name)
	assert.Eq(t, false, save)
	assert.Eq(t, "Note: session short name taken-aa is used by another process; temporarily using mywork-k7 (stored session name remains taken-aa).\n", notice)
}
