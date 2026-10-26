package chat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// TestChatCommandsText pins the shape of :help: the table of chat_usage.go builds
// it, the first line is the first command and the last line says where the detail
// of a command is.
func TestChatCommandsText(t *testing.T) {
	help := BuildChatHelp(testTranslator)
	assert.Eq(t, buildChatHelp(testTranslator), help)
	lines := strings.Split(help, "\n")
	assert.Eq(t, ":session 显示会话", lines[0])
	assert.Eq(t, testTranslator.T(chatHelpHintKey), lines[len(lines)-1])
	assert.StrContains(t, help, "按键：Enter/Alt+Enter/Ctrl+J 提交/换行")
}

func TestChatUsageAndTasks(t *testing.T) {
	stats := chatStats{turns: 2, usage: agentcore.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, CacheWriteTokens: 1, CostUSD: .25}, session: agentcore.Usage{InputTokens: 30, OutputTokens: 8, CostUSD: .5}}
	assert.StrContains(t, BuildChatUsage(testTranslator, stats), "本次 2 轮 · 输入 10 / 输出 4 · 缓存读 3 / 写 1 · 成本 $0.250000")
	assert.Eq(t, testTranslator.T(chatTasksEmptyKey), BuildChatTasks(testTranslator, nil, time.Now(), false))
	m := tasks.NewManager(tasks.Config{Output: func(_, id string) (string, string, error) { return filepath.Join(t.TempDir(), id), "", nil }})
	info, err := m.Start(api.WithSessionID(context.Background(), "sid"), tasks.Spec{Kind: "shell", Summary: "go test", Run: func(_ context.Context, _ io.Writer, _ tasks.Reporter) (int, error) { return 0, nil }})
	assert.NoErr(t, err)
	assert.StrContains(t, BuildChatTasks(testTranslator, m.List("sid"), time.Now(), false), info.ID)
	m.Close()
}

// taskInfo is one listing row of the :tasks tests: ended tasks from 10:00 on,
// one minute apart, so the recent ones are the last ones.
func taskInfo(id string, status tasks.Status, ended int) tasks.Info {
	base := time.Date(2026, 10, 23, 10, 0, 0, 0, time.Local)
	info := tasks.Info{ID: id, Kind: "shell", Summary: "cmd " + id, Status: status, Started: base.Add(time.Duration(ended) * time.Minute)}
	if status != tasks.Running {
		info.Ended = info.Started.Add(time.Second)
	}
	return info
}

// TestBuildChatTasksRecent: the default listing is the running tasks plus the 10
// most recently active finished ones and how many are left; -a lists every task.
func TestBuildChatTasksRecent(t *testing.T) {
	items := []tasks.Info{taskInfo("t99", tasks.Running, 0)}
	for i := 1; i <= 12; i++ {
		items = append(items, taskInfo(fmt.Sprintf("t%d", i), tasks.Exited, i))
	}
	now := time.Date(2026, 10, 23, 11, 0, 0, 0, time.Local)

	t.Run("default keeps the running task and the 10 most recent", func(t *testing.T) {
		got := BuildChatTasks(testTranslator, items, now, false)
		assert.StrContains(t, got, "t99 · running")
		assert.StrContains(t, got, "还有 2 条已结束任务，:tasks -a 查看全部")
		assert.StrContains(t, got, "t3 · exited")
		assert.False(t, strings.Contains(got, "t1 · exited"), got)
		assert.False(t, strings.Contains(got, "t2 · exited"), got)
		// Most recently active first, after the running tasks.
		assert.Require(t, assert.True(t, strings.Index(got, "t12 · exited") < strings.Index(got, "t3 · exited")))
		assert.Eq(t, 11+1, len(strings.Split(got, "\n")))
	})

	t.Run("all lists every task in start order", func(t *testing.T) {
		got := BuildChatTasks(testTranslator, items, now, true)
		assert.Eq(t, len(items), len(strings.Split(got, "\n")))
		assert.StrContains(t, got, "t1 · exited")
		assert.NotContains(t, got, "还有")
	})

	t.Run("a finished task stays listed without a hint", func(t *testing.T) {
		got := BuildChatTasks(testTranslator, items[:2], now, false)
		assert.StrContains(t, got, "t1 · exited")
		assert.NotContains(t, got, "还有")
	})
}

// TestChatTasksCommand drives :tasks through runChatCommand, the path chat takes:
// -h (usage), -a, an unknown option, and -k for a running, an ended and an
// unknown task.
func TestChatTasksCommand(t *testing.T) {
	m := tasks.NewManager(tasks.Config{Output: func(_, id string) (string, string, error) { return filepath.Join(t.TempDir(), id), "", nil }})
	t.Cleanup(m.Close)
	run := func(ctx context.Context, _ io.Writer, _ tasks.Reporter) (int, error) { return 0, nil }
	ended, err := m.Start(api.WithSessionID(context.Background(), "sid"), tasks.Spec{Kind: "shell", Summary: "done cmd", Run: run})
	assert.Require(t, assert.NoErr(t, err))
	_, _, err = m.Wait(context.Background(), "sid", ended.ID, 5*time.Second)
	assert.Require(t, assert.NoErr(t, err))
	running, err := m.Start(api.WithSessionID(context.Background(), "sid"), tasks.Spec{Kind: "shell", Summary: "slow cmd",
		Run: func(ctx context.Context, _ io.Writer, _ tasks.Reporter) (int, error) { <-ctx.Done(); return 0, nil }})
	assert.Require(t, assert.NoErr(t, err))

	var buf bytes.Buffer
	source, err := newChatInputSource(context.Background(), strings.NewReader(""), &buf)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = source.Close() })
	s := &chatState{ctx: context.Background(), source: source, sessionID: "sid", tasks: m,
		render: newRenderer(rendererOptions{Out: &buf, Err: &buf})}

	t.Run("help", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":tasks -h")))
		assert.StrContains(t, buf.String(), "用法：:tasks [选项]")
		assert.StrContains(t, buf.String(), ":tasks -k <ID>")
	})

	t.Run("unknown option shows the usage", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":tasks -x")))
		assert.StrContains(t, buf.String(), "未知参数：-x")
		assert.StrContains(t, buf.String(), "用法：:tasks [选项]")
	})

	t.Run("default and all", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":tasks")))
		assert.StrContains(t, buf.String(), running.ID)
		assert.StrContains(t, buf.String(), ended.ID)
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":tasks -a")))
		assert.StrContains(t, buf.String(), running.ID)
	})

	t.Run("kill says what happened", func(t *testing.T) {
		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":tasks -k t404")))
		assert.StrContains(t, buf.String(), "没有任务 t404")

		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":tasks -k "+ended.ID)))
		assert.StrContains(t, buf.String(), "任务 "+ended.ID+" 已结束（exited），无需终止")

		buf.Reset()
		assert.Require(t, assert.True(t, s.runChatCommand(":tasks -k "+running.ID+" t404")))
		assert.StrContains(t, buf.String(), "已终止任务 "+running.ID+"：slow cmd")
		assert.StrContains(t, buf.String(), "没有任务 t404")
		info, err := m.Get("sid", running.ID)
		assert.NoErr(t, err)
		assert.Eq(t, tasks.Killed, info.Status)
	})
}
