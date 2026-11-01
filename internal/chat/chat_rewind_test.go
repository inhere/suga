package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

func TestParseRewindCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		line  string
		want  rewindCommand
		valid bool
	}{
		{name: "list", line: ":rewind", want: rewindCommand{scope: personal.RewindScopeBoth}, valid: true},
		{name: "target defaults to both", line: ":rewind 3", want: rewindCommand{target: "3", scope: personal.RewindScopeBoth}, valid: true},
		{name: "files", line: ":rewind 2 files", want: rewindCommand{target: "2", scope: personal.RewindScopeFiles}, valid: true},
		{name: "conversation", line: ":rewind 2 conversation", want: rewindCommand{target: "2", scope: personal.RewindScopeConversation}, valid: true},
		{name: "turn id", line: ":rewind turn-abc both", want: rewindCommand{target: "turn-abc", scope: personal.RewindScopeBoth}, valid: true},
		{name: "bad scope", line: ":rewind 2 everything"},
		{name: "extra argument", line: ":rewind 2 files now"},
		{name: "other command", line: ":undo 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRewindCommand(tc.line)
			assert.Eq(t, tc.valid, ok)
			if ok {
				assert.Eq(t, tc.want, got)
			}
		})
	}
}

func TestParseForkCommand(t *testing.T) {
	target, ok := parseForkCommand(":fork 2")
	assert.True(t, ok)
	assert.Eq(t, "2", target)
	for _, line := range []string{":fork", ":fork 1 2", ":rewind 1"} {
		_, ok := parseForkCommand(line)
		assert.False(t, ok, line)
	}
}

func TestRewindErrorText(t *testing.T) {
	for sentinel, want := range map[error]string{
		personal.ErrRewindStale:             "请重新执行 :rewind",
		personal.ErrRewindBusy:              "等回合结束",
		personal.ErrRewindTargetNotFound:    "找不到这个用户轮",
		personal.ErrRewindTargetMasked:      "已被之前的回退排除",
		personal.ErrRewindTargetNotUserTurn: "不是用户发起的",
		personal.ErrRewindSessionArchived:   "已归档",
		personal.ErrRewindTasksRunning:      "后台任务在运行",
	} {
		// The runner wraps the sentinels with detail; the lookup is by errors.Is.
		got := rewindErrorText(testTranslator, fmt.Errorf("%w: detail", sentinel))
		assert.StrContains(t, got, want)
	}
	assert.Eq(t, "boom", rewindErrorText(testTranslator, errors.New("boom")))
	// the English catalog carries the same keys
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, rewindErrorText(en, personal.ErrRewindBusy), "turn ends")
	assert.StrContains(t, rewindErrorText(en, personal.ErrRewindStale), "Run :rewind again")
}

func TestBuildRewindPlanAndResult(t *testing.T) {
	plan := personal.RewindPlan{TargetTurn: "turn-2", Scope: personal.RewindScopeBoth, Files: []personal.RewindFileItem{
		{Path: "a.txt", Action: personal.RewindFileRestore},
		{Path: "b\x1b[2J.txt", Action: personal.RewindFileConflict, Reason: "content_changed"},
	}}
	got := BuildRewindPlan(testTranslator, plan, "2")
	assert.StrContains(t, got, "恢复 a.txt")
	assert.StrContains(t, got, "跳过 b.txt  快照之后被改动过")
	assert.NotContains(t, got, "\x1b")
	assert.StrContains(t, got, "未送达的后台任务通知将被丢弃")

	filesOnly := BuildRewindPlan(testTranslator, personal.RewindPlan{Scope: personal.RewindScopeFiles}, "2")
	assert.StrContains(t, filesOnly, "没有需要恢复的文件改动")
	assert.NotContains(t, filesOnly, "后台任务通知")

	res := personal.RewindResult{
		Files: []personal.RewindFileItem{{Path: "a.txt", Action: personal.RewindFileRestore}}, Conversation: true,
		DroppedTasks: []tasks.Info{{ID: "bg-1", Summary: "go test"}}, Warnings: []string{"fold reset failed"},
	}
	out := BuildRewindResult(testTranslator, plan, res)
	assert.StrContains(t, out, "恢复 a.txt")
	assert.StrContains(t, out, "对话已回退")
	assert.StrContains(t, out, "已丢弃任务通知：bg-1 go test")
	assert.StrContains(t, out, "警告：fold reset failed")
	none := BuildRewindResult(testTranslator, plan, personal.RewindResult{Conversation: true})
	assert.StrContains(t, none, "没有丢弃任何任务通知")
}

func TestHelpListsRewindAndFork(t *testing.T) {
	help := BuildChatHelp(testTranslator)
	assert.StrContains(t, help, ":rewind ")
	assert.StrContains(t, help, ":fork ")
	assert.StrContains(t, commandUsageText(testTranslator, rewindCmd), "files|conversation|both")
	assert.StrContains(t, commandUsageText(testTranslator, forkCmd), "suga chat --resume")
}

// TestChatPromptRewindWithoutTerminalCancels: with confirmation on and a piped
// stdin, :rewind shows the plan and cancels without changing anything.
func TestChatPromptRewindWithoutTerminalCancels(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("answer"), mock.Final("answer2")))
	defer repo.Close()
	var stdout, stderr strings.Builder
	in := "第一问\n:rewind\n:rewind 1 conversation\n:rewind 1 bogus\n:rewind 9\n:quit\n"
	assert.NoErr(t, Run(context.Background(), runner, Options{}, strings.NewReader(in), &stdout, &stderr, nil))
	out := stdout.String()
	assert.StrContains(t, out, "可回退的用户轮")
	assert.StrContains(t, out, "第一问")
	assert.StrContains(t, out, "未送达的后台任务通知将被丢弃")
	assert.StrContains(t, out, testTranslator.T("cmd.rewind.cancelled.notty"))
	assert.StrContains(t, out, "用法：:rewind")
	assert.StrContains(t, stderr.String(), "找不到这个用户轮")
	assert.NotContains(t, out, "回退完成")
}

// TestChatPromptRewindAppliesAndForks: with confirmation off, :rewind executes and
// shows the result; :fork prints the new session id and the resume command, and
// the chat keeps its own session.
func TestChatPromptRewindAppliesAndForks(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("chat.undo_confirm", false))
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("answer"), mock.Final("answer2")))
	defer repo.Close()
	var stdout, stderr strings.Builder
	in := "第一问\n:fork 1\n:rewind 1 conversation\n:rewind 1\n:quit\n"
	assert.NoErr(t, Run(context.Background(), runner, Options{}, strings.NewReader(in), &stdout, &stderr, nil))
	out := stdout.String()
	assert.StrContains(t, out, "已分叉出新会话")
	assert.StrContains(t, out, "suga chat --resume ")
	assert.StrContains(t, out, "回退完成")
	assert.StrContains(t, out, "对话已回退")
	// the rewound turn is masked now, so a second :rewind 1 finds no user turn
	assert.StrContains(t, stderr.String(), "找不到这个用户轮")
}

func TestRunningTurnPutsRewindAndForkBack(t *testing.T) {
	for _, line := range []string{":rewind 1", ":fork 1"} {
		t.Run(line, func(t *testing.T) {
			h := newSteerHarness(t, newGateProv())
			done := h.startTurn()
			<-h.cast.started()

			h.kb.Type(line + "\r")
			waitUntil(t, "local command busy hint", func() bool { return strings.Contains(h.out.String(), zhUndoBusyHint) })
			assert.Eq(t, line, h.turnInputLine(t))

			h.cast.letGo()
			assert.NoErr(t, <-done)
			// the line never reached the model as a steering message
			assert.Eq(t, []string{"干活"}, h.cast.prompts())
		})
	}
}

// TestRewindReasonText: reason codes are shown as catalog text in zh and en; an
// unknown code stays as it is and restore_failed keeps the error text (suga-449r).
func TestRewindReasonText(t *testing.T) {
	en, err := i18n.New(i18n.LanguageEN)
	assert.Require(t, assert.NoErr(t, err))
	codes := []string{"content_changed", "cascade", "root_untrusted", "cleared", "no_snapshot", "snapshots_off",
		"not_restorable", "snapshot_incomplete", "file_missing", "not_regular_file", "snapshot_hash_missing",
		"unchanged", "unreadable"}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			zhText, enText := rewindReasonText(testTranslator, code), rewindReasonText(en, code)
			assert.NotEq(t, code, zhText)
			assert.NotEq(t, code, enText)
			assert.NotEq(t, zhText, enText)
		})
	}
	t.Run("restore_failed keeps the error text", func(t *testing.T) {
		assert.Eq(t, "恢复失败: disk full", rewindReasonText(testTranslator, "restore_failed: disk full"))
		assert.Eq(t, "restore failed: disk full", rewindReasonText(en, "restore_failed: disk full"))
	})
	t.Run("unknown code is unchanged", func(t *testing.T) {
		assert.Eq(t, "purged", rewindReasonText(testTranslator, "purged"))
		assert.Eq(t, "restore_failedX", rewindReasonText(testTranslator, "restore_failedX"))
		assert.Eq(t, "", rewindReasonText(testTranslator, ""))
	})
}

// TestBuildRewindResultOneRowPerPath: a path written by several turns is one
// row in the result, decided by the last step (suga-y027).
func TestBuildRewindResultOneRowPerPath(t *testing.T) {
	plan := personal.RewindPlan{Scope: personal.RewindScopeFiles}
	res := personal.RewindResult{Files: []personal.RewindFileItem{
		{Path: "a.txt", Action: personal.RewindFileRestore},
		{Path: "b.txt", Action: personal.RewindFileRestore},
		{Path: "a.txt", Action: personal.RewindFileRestore},
	}}
	out := BuildRewindResult(testTranslator, plan, res)
	assert.Eq(t, 1, strings.Count(out, "a.txt"))
	assert.Eq(t, 1, strings.Count(out, "b.txt"))
	assert.Eq(t, 3, len(res.Files)) // the per-step results are not modified
}
