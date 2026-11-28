package chat

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// forkBgState is a chat state over a buffer, so a :bg answer is asserted without
// a terminal or a runner.
func forkBgState(t *testing.T, start func(ctx context.Context, task string) (tasks.Info, error)) (*chatState, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	source, err := newChatInputSource(context.Background(), strings.NewReader(""), buf)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = source.Close() })
	return &chatState{ctx: context.Background(), source: source, stderr: buf, sessionID: "s1", startFork: start}, buf
}

// TestForkBgTaskArg: the task text of a :bg line is everything after the command.
func TestForkBgTaskArg(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{name: "task", line: ":bg 查一下 file_read", want: "查一下 file_read"},
		{name: "extra spaces", line: ":bg   两个 空格  ", want: "两个 空格"},
		{name: "no task", line: ":bg", want: ""},
		{name: "command match", line: ":bgx 不是命令", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.name != "command match", isBgCommand(tc.line))
			assert.Eq(t, tc.want, bgTaskArg(tc.line))
		})
	}
}

// TestForkBgDescription: the label of a :bg fork is its task's first six words,
// the same rule native gives an agent call without a description.
func TestForkBgDescription(t *testing.T) {
	for _, tc := range []struct{ name, task, want string }{
		{name: "short", task: "查一下 file_read", want: "查一下 file_read"},
		{name: "six words", task: "a b c d e f", want: "a b c d e f"},
		{name: "cut", task: "a b c d e f g h", want: "a b c d e f"},
		{name: "spaces", task: "  a   b ", want: "a b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, tc.want, forkTaskDescription(tc.task))
		})
	}
}

// TestForkBg: what :bg answers in each case - no task, no wiring, a started fork
// and the two refusals that carry numbers or a translated reason.
func TestForkBg(t *testing.T) {
	t.Run("no task prints the usage", func(t *testing.T) {
		s, out := forkBgState(t, func(context.Context, string) (tasks.Info, error) {
			t.Fatal("a :bg without a task must not start a fork")
			return tasks.Info{}, nil
		})
		s.runForkBg("   ")
		assert.StrContains(t, out.String(), testTranslator.T("cmd.bg.usage"))
	})

	t.Run("without the wiring it says the fork is unavailable", func(t *testing.T) {
		s, out := forkBgState(t, nil)
		s.runForkBg("查一下 X")
		assert.StrContains(t, out.String(), testTranslator.T("cmd.bg.disabled"))
	})

	t.Run("a started fork names the task and its label", func(t *testing.T) {
		var gotCtx context.Context
		var gotTask string
		s, out := forkBgState(t, func(ctx context.Context, task string) (tasks.Info, error) {
			gotCtx, gotTask = ctx, task
			return tasks.Info{ID: "t3", Kind: "agent", Summary: "agent: 查一下 file_read 在哪"}, nil
		})
		s.runForkBg("查一下 file_read 在哪被调用")
		// the session is on the context: the task manager scopes the task by it
		assert.Eq(t, "s1", api.SessionIDFrom(gotCtx))
		assert.Eq(t, "查一下 file_read 在哪被调用", gotTask)
		assert.StrContains(t, out.String(), "分身 t3 已在后台启动：查一下 file_read 在哪被调用")
	})

	t.Run("the limit names the numbers", func(t *testing.T) {
		s, out := forkBgState(t, func(context.Context, string) (tasks.Info, error) {
			return tasks.Info{}, &native.ForkLimitError{Running: 2, Limit: 2}
		})
		s.runForkBg("查一下 X")
		assert.StrContains(t, out.String(), "后台任务已满（2/2）")
		assert.StrContains(t, out.String(), ":tasks -k <ID>")
	})

	t.Run("a refusal shows its reason", func(t *testing.T) {
		s, out := forkBgState(t, func(context.Context, string) (tasks.Info, error) {
			return tasks.Info{}, &personal.ForkUnavailableError{Reason: "host_hook"}
		})
		s.runForkBg("查一下 X")
		assert.StrContains(t, out.String(), "无法启动分身：host_hook")
		// A refusal is answered, not reported as a command failure.
		assert.NotContains(t, out.String(), ":bg:")

		// The wrapped error still matches by errors.As, and a refusal without a hint
		// stays one line.
		s, out = forkBgState(t, func(context.Context, string) (tasks.Info, error) {
			return tasks.Info{}, errors.Join(errors.New("seed"), &personal.ForkUnavailableError{Reason: "no_request"})
		})
		s.runForkBg("查一下 X")
		assert.Eq(t, "无法启动分身：no_request", strings.TrimSpace(out.String()))
	})

	t.Run("any other error is shown as it is", func(t *testing.T) {
		s, out := forkBgState(t, func(context.Context, string) (tasks.Info, error) {
			return tasks.Info{}, errors.New("agent: fork needs background tasks")
		})
		s.runForkBg("查一下 X")
		assert.StrContains(t, out.String(), ":bg: agent: fork needs background tasks")
	})

	t.Run("the answers are translated", func(t *testing.T) {
		en := enTranslator(t)
		assert.StrContains(t, forkBgErrorText(en, &native.ForkLimitError{Running: 1, Limit: 4}), "Every background task slot is taken (1/4)")
		assert.StrContains(t, bgUnavailableText(en, "prompt_state", ""), "Cannot start a fork: prompt_state")
		assert.Eq(t, "Cannot start a fork: context\nask = no", bgUnavailableText(en, "context", "ask = no"))
	})
}

// TestForkBgThroughRun: the command is dispatched from the prompt loop, so a
// typed :bg starts a fork and no turn is spent on the line.
func TestForkBgThroughRun(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	defer repo.Close()

	var tasksSeen []string
	var stdout, stderr strings.Builder
	start := func(ctx context.Context, task string) (tasks.Info, error) {
		tasksSeen = append(tasksSeen, task)
		// The command runs outside a turn, so it puts the chat's session on the
		// context itself (the task manager scopes the task by it).
		assert.NotEmpty(t, api.SessionIDFrom(ctx))
		return tasks.Info{ID: "t1", Kind: "agent"}, nil
	}
	input := ":bg\n:bg 查一下 file_read\n:help\n:quit\n"
	assert.NoErr(t, Run(context.Background(), runner, Options{StartFork: start}, strings.NewReader(input), &stdout, &stderr, nil))

	assert.Eq(t, []string{"查一下 file_read"}, tasksSeen)
	out := stdout.String()
	assert.StrContains(t, out, testTranslator.T("cmd.bg.usage"))
	assert.StrContains(t, out, "分身 t1 已在后台启动：查一下 file_read")
	assert.StrContains(t, out, ":bg 派出只读分身")
	assert.StrNotContains(t, out, "MODEL_ANSWER")
}

// TestForkBgHelp: :bg is a command of the :help table, so its usage is printed
// by ":bg -h" like every other command's.
func TestForkBgHelp(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	defer repo.Close()
	var stdout, stderr strings.Builder
	assert.NoErr(t, Run(context.Background(), runner, Options{},
		strings.NewReader(":bg -h\n:quit\n"), &stdout, &stderr, nil))
	assert.StrContains(t, stdout.String(), commandUsageText(testTranslator, bgCmd))
	assert.StrNotContains(t, stdout.String(), "MODEL_ANSWER")
}

// TestForkBgEnglish pins the English answers: a translated chat shows the same
// command with English text.
func TestForkBgEnglish(t *testing.T) {
	tr := enTranslator(t)
	assert.StrContains(t, tr.T("cmd.bg.usage"), "Usage: :bg <task>")
	assert.StrContains(t, i18n.Fallback().T("cmd.bg.started", map[string]any{"id": "t1", "desc": "x"}), "分身 t1")
	assert.StrContains(t, tr.T("cmd.bg.disabled"), "Fork unavailable")
}
