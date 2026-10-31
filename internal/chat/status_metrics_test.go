package chat

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

func metricsText(t *testing.T, m TurnMetrics) string {
	t.Helper()
	lines := BuildTurnMetrics(m)
	if len(lines) == 0 {
		return ""
	}
	assert.Eq(t, 1, len(lines))
	return lines[0].Text
}

// TestBuildTurnMetrics: every item shows only with its data, in a fixed order.
func TestBuildTurnMetrics(t *testing.T) {
	t.Run("all items", func(t *testing.T) {
		got := metricsText(t, TurnMetrics{
			Usage:           agentcore.Usage{InputTokens: 1000, CacheReadTokens: 820},
			LastInputTokens: 4100, ContextWindow: 10000, FoldThreshold: 6500, Background: 2,
		})
		assert.Eq(t, "cache 82% · ctx 41% · 折叠余量 36% · 后台 2", got)
	})
	t.Run("english catalog", func(t *testing.T) {
		got := metricsText(t, TurnMetrics{
			Usage:           agentcore.Usage{InputTokens: 100, CacheReadTokens: 50},
			LastInputTokens: 100, FoldThreshold: 200, Background: 1, Translator: enTranslator(t),
		})
		assert.Eq(t, "cache 50% · fold headroom 50% · bg 1", got)
	})
	t.Run("no usage leaves cache out", func(t *testing.T) {
		got := metricsText(t, TurnMetrics{LastInputTokens: 500, ContextWindow: 1000})
		assert.Eq(t, "ctx 50%", got)
	})
	t.Run("zero cache read is a real 0%", func(t *testing.T) {
		got := metricsText(t, TurnMetrics{Usage: agentcore.Usage{InputTokens: 10}})
		assert.Eq(t, "cache 0%", got)
	})
	t.Run("unknown window leaves ctx out", func(t *testing.T) {
		got := metricsText(t, TurnMetrics{LastInputTokens: 500, FoldThreshold: 1000})
		assert.Eq(t, "折叠余量 50%", got)
	})
	t.Run("past the threshold the headroom is 0", func(t *testing.T) {
		got := metricsText(t, TurnMetrics{LastInputTokens: 1500, FoldThreshold: 1000})
		assert.Eq(t, "折叠余量 0%", got)
	})
	t.Run("cache over input is capped", func(t *testing.T) {
		got := metricsText(t, TurnMetrics{Usage: agentcore.Usage{InputTokens: 10, CacheReadTokens: 30}})
		assert.Eq(t, "cache 100%", got)
	})
	t.Run("no data prints no line", func(t *testing.T) {
		assert.Nil(t, BuildTurnMetrics(TurnMetrics{}))
	})
}

func metricsRenderer(out *bytes.Buffer, o rendererOptions) *Renderer {
	o.Out, o.Model = out, "m"
	o.now = func() time.Time { return time.Unix(0, 0) }
	return newRenderer(o)
}

func metricsResult() personal.RunResult {
	return personal.RunResult{
		Usage:           agentcore.Usage{InputTokens: 1000, OutputTokens: 5, CacheReadTokens: 900},
		LastInputTokens: 2000,
	}
}

func runningTasks() []tasks.Info {
	return []tasks.Info{
		{ID: "t1", Status: tasks.Running}, {ID: "t2", Status: tasks.Running}, {ID: "t3", Status: tasks.Exited},
	}
}

// TestRendererStatusMetrics: the real turn-end path (Renderer.Handle for the
// turn's events, then Renderer.Status) prints the metrics line under the status
// line on a colored terminal, and only then.
func TestRendererStatusMetrics(t *testing.T) {
	run := func(o rendererOptions) string {
		var out bytes.Buffer
		r := metricsRenderer(&out, o)
		r.BeginTurn()
		r.Handle(personal.Event{Kind: "text", Text: "hi"})
		r.Finish()
		r.Status(metricsResult(), time.Second, 0)
		return out.String()
	}
	base := rendererOptions{ContextWindow: 8000, StatusMetrics: true, AgentTasks: runningTasks,
		FoldThreshold: func() int { return 5000 }}

	t.Run("colored terminal shows the line below the status line", func(t *testing.T) {
		o := base
		o.Color = true
		got := run(o)
		lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		assert.StrContains(t, lines[len(lines)-2], "in 1000 / out 5 tokens")
		assert.StrContains(t, lines[len(lines)-1], "cache 90% · ctx 25% · 折叠余量 60% · 后台 2")
	})
	t.Run("switch off prints the old output", func(t *testing.T) {
		on, off := base, base
		on.Color, off.Color, off.StatusMetrics = true, true, false
		got := run(off)
		assert.StrNotContains(t, got, "cache ")
		assert.StrNotContains(t, got, "折叠余量")
		assert.StrContains(t, got, "in 1000 / out 5 tokens")
	})
	t.Run("without color the output is byte for byte the old one", func(t *testing.T) {
		withMetrics, without := base, base
		without.StatusMetrics, without.AgentTasks, without.FoldThreshold = false, nil, nil
		assert.Eq(t, run(without), run(withMetrics))
		assert.StrNotContains(t, run(withMetrics), "cache ")
	})
	t.Run("a turn without data adds no line", func(t *testing.T) {
		var out, ref bytes.Buffer
		o := rendererOptions{Color: true, StatusMetrics: true}
		r := metricsRenderer(&out, o)
		r.Status(personal.RunResult{}, time.Second, 0)
		o.StatusMetrics = false
		metricsRenderer(&ref, o).Status(personal.RunResult{}, time.Second, 0)
		assert.Eq(t, ref.String(), out.String())
	})
}
