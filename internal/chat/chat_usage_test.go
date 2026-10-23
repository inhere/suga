package chat

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// TestChatCommandTable: the table of chat_usage.go describes every local command
// once - a summary, a usage that names the command, and the names Tab completes -
// and nothing else builds that text.
func TestChatCommandTable(t *testing.T) {
	t.Run("every entry is complete", func(t *testing.T) {
		for _, spec := range chatCommandSpecs {
			assert.Require(t, assert.NotEmpty(t, spec.Names))
			assert.NotEmpty(t, spec.Summary, spec.Names[0])
			assert.StrContains(t, spec.Usage, "用法："+spec.Names[0], spec.Names[0])
			// One or two examples, as the task asks.
			assert.StrContains(t, spec.Usage, "示例：", spec.Names[0])
		}
	})

	t.Run("the name list is the table in order, without duplicates", func(t *testing.T) {
		seen := make(map[string]bool, len(chatCommands))
		want := 0
		for _, spec := range chatCommandSpecs {
			want += len(spec.Names)
		}
		assert.Eq(t, want, len(chatCommands))
		for _, name := range chatCommands {
			assert.False(t, seen[name], name+" is listed twice")
			seen[name] = true
		}
		// The order :help prints and Tab completes stays the familiar one.
		assert.Eq(t, ":session", chatCommands[0])
		assert.True(t, seen[":quit"] && seen[":exit"])
	})

	t.Run("lookup accepts both spellings", func(t *testing.T) {
		withColon, ok := lookupCommand(":queue")
		assert.Require(t, assert.True(t, ok))
		withoutColon, ok := lookupCommand("queue")
		assert.Require(t, assert.True(t, ok))
		assert.Eq(t, withColon.Usage, withoutColon.Usage)
		_, ok = lookupCommand(":nope")
		assert.False(t, ok)
		assert.StrContains(t, commandUsageText(":nope"), "未知命令：:nope")
	})

	t.Run("the options Tab offers come from the table", func(t *testing.T) {
		assert.Eq(t, []string{"-d", "clear", "-h"}, commandOptions(":queue"))
		assert.Eq(t, []string{"-a", "-k", "-h"}, commandOptions(tasksCmd))
		assert.Eq(t, chatCommands, commandOptions(":help"))
		assert.Empty(t, commandOptions(":nope"))
	})
}

// TestChatHelpListing: :help lists every command with its summary and says where
// the detail of one command is.
func TestChatHelpListing(t *testing.T) {
	help := BuildChatHelp()
	assert.Eq(t, chatHelpText, help)
	for _, spec := range chatCommandSpecs {
		assert.StrContains(t, help, strings.Join(spec.Names, "/")+" "+spec.Summary)
	}
	lines := strings.Split(help, "\n")
	assert.Eq(t, ":session 显示会话", lines[0])
	assert.Eq(t, chatHelpHint, lines[len(lines)-1])
	assert.StrContains(t, help, "按键：Enter/Alt+Enter/Ctrl+J 提交/换行")
}

// TestChatCommandHelpEveryCommand drives "<命令> -h" through Run, the path chat
// takes: every command of the table answers with its usage and no line becomes a
// message to the model.
func TestChatCommandHelpEveryCommand(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	defer repo.Close()
	lines := make([]string, 0, len(chatCommands)+1)
	for _, name := range chatCommands {
		lines = append(lines, name+" -h")
	}
	lines = append(lines, ":quit")
	var stdout, stderr strings.Builder
	assert.NoErr(t, Run(context.Background(), runner, Options{Dirs: &fakeSessionDirs{workspace: filepath.FromSlash("/ws")}},
		strings.NewReader(strings.Join(lines, "\n")+"\n"), &stdout, &stderr, nil))

	out := stdout.String()
	for _, spec := range chatCommandSpecs {
		assert.StrContains(t, out, spec.Usage, spec.Names[0]+" prints its usage")
	}
	assert.StrNotContains(t, out, "MODEL_ANSWER")
	// --help is the same ask.
	stdout.Reset()
	assert.NoErr(t, Run(context.Background(), runner, Options{Dirs: &fakeSessionDirs{workspace: filepath.FromSlash("/ws")}},
		strings.NewReader(":queue --help\n:quit\n"), &stdout, &stderr, nil))
	assert.StrContains(t, stdout.String(), commandUsageText(queueCmd))
}

// TestChatCommandRejectsUnknownArgs: a command that takes no argument answers one
// with its usage instead of ignoring it or turning the line into a message; the
// chat goes on.
func TestChatCommandRejectsUnknownArgs(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	defer repo.Close()
	input := strings.Join([]string{
		":quit now", // refused, chat stays open
		":session extra",
		":compact extra",
		":usage extra",
		":dirs extra",
		":session", // the line behind it still runs
		":quit",
	}, "\n") + "\n"
	var stdout, stderr strings.Builder
	assert.NoErr(t, Run(context.Background(), runner, Options{Dirs: &fakeSessionDirs{workspace: filepath.FromSlash("/ws")}},
		strings.NewReader(input), &stdout, &stderr, nil))

	out := stdout.String()
	for _, arg := range []string{"now", "extra"} {
		assert.StrContains(t, out, "未知参数："+arg)
	}
	for _, name := range []string{":session", ":compact", ":usage", ":dirs"} {
		assert.StrContains(t, out, commandUsageText(name))
	}
	// :quit now did not leave chat: the :session behind it still printed the id.
	assert.StrContains(t, out, "session id: ")
	assert.StrNotContains(t, out, "MODEL_ANSWER")
}

// TestChatHelpCommandArg: :help <命令> shows that command's usage, the same text
// "<命令> -h" prints; an unknown name says so instead of running a turn.
func TestChatHelpCommandArg(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	defer repo.Close()
	var stdout, stderr strings.Builder
	assert.NoErr(t, Run(context.Background(), runner, Options{},
		strings.NewReader(":help :queue\n:help nope\n:quit\n"), &stdout, &stderr, nil))

	out := stdout.String()
	assert.StrContains(t, out, commandUsageText(queueCmd))
	assert.StrContains(t, out, "未知命令：nope")
	assert.StrNotContains(t, out, "MODEL_ANSWER")
}

// TestChatQueueCommandDrop: :queue -d drops the message with that number, as the
// :queue listing counts them.
func TestChatQueueCommandDrop(t *testing.T) {
	var buf bytes.Buffer
	source, err := newChatInputSource(context.Background(), strings.NewReader(""), &buf)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = source.Close() })
	s := &chatState{source: source}
	for _, text := range []string{"第一句", "第二句", "第三句"} {
		s.followUps.push(text)
	}

	t.Run("list numbers the messages", func(t *testing.T) {
		buf.Reset()
		s.runQueueCommand(":queue")
		assert.StrContains(t, buf.String(), "排队的消息（3 条")
		assert.StrContains(t, buf.String(), "  2. 第二句")
	})

	t.Run("drops the second", func(t *testing.T) {
		buf.Reset()
		s.runQueueCommand(":queue -d 2")
		assert.StrContains(t, buf.String(), "已删除第 2 条排队的消息：第二句")
		assert.Eq(t, []string{"第一句", "第三句"}, s.followUps.pending())
	})

	t.Run("a number that is not queued says so", func(t *testing.T) {
		buf.Reset()
		s.runQueueCommand(":queue -d 3")
		assert.StrContains(t, buf.String(), "没有第 3 条排队的消息")
		assert.Eq(t, 2, len(s.followUps.pending()))
	})

	t.Run("unusable arguments print the usage", func(t *testing.T) {
		for _, line := range []string{":queue -d", ":queue -d x", ":queue -d 0", ":queue clear now", ":queue bogus"} {
			buf.Reset()
			s.runQueueCommand(line)
			assert.StrContains(t, buf.String(), queueUsageHint, line)
			assert.Eq(t, 2, len(s.followUps.pending()), line)
		}
	})

	t.Run("clear drops the rest", func(t *testing.T) {
		buf.Reset()
		s.runQueueCommand(":queue clear")
		assert.StrContains(t, buf.String(), "已清空 2 条排队的消息")
		assert.Empty(t, s.followUps.pending())
	})
}

// TestCheckpointLimitArg: :checkpoints reads an optional count of turns.
func TestCheckpointLimitArg(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
		want int
		ok   bool
	}{
		{name: "all", arg: "", want: 0, ok: true},
		{name: "count", arg: "3", want: 3, ok: true},
		{name: "spaces", arg: " 2 ", want: 2, ok: true},
		{name: "zero", arg: "0", ok: false},
		{name: "negative", arg: "-1", ok: false},
		{name: "not a number", arg: "x", ok: false},
		{name: "two values", arg: "1 2", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := checkpointLimit(tc.arg)
			assert.Eq(t, tc.ok, ok)
			assert.Eq(t, tc.want, got)
		})
	}
}

// TestChatCheckpointsCountArg drives :checkpoints N and a bad count through Run:
// the count is accepted (a session without snapshots still says so) and a bad one
// prints the usage instead of starting a turn.
func TestChatCheckpointsCountArg(t *testing.T) {
	runner, repo := newCLITestRunner(t, mock.NewProvider(mock.Final("MODEL_ANSWER")))
	defer repo.Close()
	var stdout, stderr strings.Builder
	assert.NoErr(t, Run(context.Background(), runner, Options{},
		strings.NewReader(":checkpoints 2\n:checkpoints x\n:quit\n"), &stdout, &stderr, nil))

	assert.StrContains(t, stdout.String(), noUndoHint)
	assert.StrContains(t, stderr.String(), commandUsageText(checkpointsCmd))
	assert.StrNotContains(t, stdout.String(), "MODEL_ANSWER")
}
