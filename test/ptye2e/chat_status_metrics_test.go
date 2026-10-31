//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestChatStatusMetrics: after a turn that reported usage, a colored terminal
// shows the metrics line under the status line (cache hit rate, context share,
// folding headroom); chat.status_metrics: false and NO_COLOR keep it out.
func TestChatStatusMetrics(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 200 * time.Millisecond, PromptTokens: 4000, CachedTokens: 3000})
	defer server.Close()
	// Window 16000 -> fold threshold 12800: ctx 25%, headroom 68%, cache 75%.
	const window = "    models:\n      m1:\n        context_window: 16000\n"

	turn := func(t *testing.T, o chatOpts) (metrics bool) {
		t.Helper()
		o.Home, o.Work, o.URL, o.ProviderExtra = t.TempDir(), t.TempDir(), server.URL, window
		s, _ := startChatOpts(t, binary, o)
		mark := s.Mark()
		expect(t, s, `Suga chat · fake/m1`, mark)
		// Two turns: the second one's status line proves the first one's whole
		// output (metrics line included) had been printed before we look.
		for _, line := range []string{"你好", "再来"} {
			mark = s.Mark()
			s.SendLine(line)
			expect(t, s, `完成`, mark)
			expect(t, s, `tokens · `, mark)
		}
		time.Sleep(300 * time.Millisecond)
		metrics = s.Contains(`cache 75% · ctx 25% · 折叠余量 68%`, 0)
		ctrlCTwice(t, s)
		return metrics
	}

	t.Run("colored terminal shows the line", func(t *testing.T) {
		check(t, "metrics line", turn(t, chatOpts{Color: true}))
	})
	t.Run("switch off keeps it out", func(t *testing.T) {
		check(t, "no metrics line", !turn(t, chatOpts{Color: true, Extra: "chat:\n  status_metrics: false\n"}))
	})
	t.Run("NO_COLOR keeps it out", func(t *testing.T) {
		check(t, "no metrics line", !turn(t, chatOpts{}))
	})
}
