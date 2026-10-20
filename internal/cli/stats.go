package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal/store"
)

// newStatsCommand builds `suga inspect stats`.
func newStatsCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var since, session string
	var asJSON bool
	cmd := capp.NewCmd("stats", "Summarize model/tool calls, latency, tokens and cache hits", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga inspect stats [--since 7d] [--session id] [--json]")
		}
		return runStats(ctx, stdout, since, session, asJSON)
	})
	cmd.SetOutput(stderr)
	cmd.StringVar(&since, "since", "", "only events newer than this, e.g. 12h or 7d")
	cmd.StringVar(&session, "session", "", "only events of this session ID;;sid")
	cmd.BoolVar(&asJSON, "json", false, "write a single JSON object")
	return cmd
}

func openCoreStore() (*store.Store, error) {
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return nil, err
	}
	// The threshold of store.body_inline_bytes decides what the maintenance
	// commands see as pending and what --move moves out.
	settings, err := appconfig.ReadToolBodySettings()
	if err != nil {
		return nil, err
	}
	return store.Open(dbPath, store.WithToolBodyInlineBytes(settings.InlineBytes))
}

// parseSince parses "<n>h" or "<n>d" into a duration.
func parseSince(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid --since %q: want <n>h or <n>d", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid --since %q: want <n>h or <n>d", s)
	}
	switch s[len(s)-1] {
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid --since %q: want <n>h or <n>d", s)
}

func runStats(ctx context.Context, out io.Writer, since, session string, asJSON bool) error {
	// A session's stats include its sub-agent sessions (what it spent in all).
	f := store.EventFilter{SessionID: session, WithAgents: true}
	if since != "" {
		d, err := parseSince(since)
		if err != nil {
			return err
		}
		f.Since = time.Now().Add(-d)
	}
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()
	if session != "" {
		if _, err := repo.GetSession(ctx, session); err != nil {
			return fmt.Errorf("stats session %q: %w", session, err)
		}
	}
	rep, err := repo.BuildReport(ctx, f)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(rep)
	}
	return writeStatsText(out, rep)
}

func writeStatsText(out io.Writer, rep store.Report) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	hasEstimated := false
	for _, m := range rep.Models {
		if m.EstimatedCalls > 0 {
			hasEstimated = true
			break
		}
	}
	if hasEstimated {
		fmt.Fprintln(tw, "MODEL\tCALLS\tERR%\tP50\tP95\tTTFT-P50\tIN\tOUT\tCACHE-R\tCACHE-W\tHIT%\tCOST\tEST")
	} else {
		fmt.Fprintln(tw, "MODEL\tCALLS\tERR%\tP50\tP95\tTTFT-P50\tIN\tOUT\tCACHE-R\tCACHE-W\tHIT%\tCOST")
	}
	for _, m := range rep.Models {
		ttft := "-"
		if m.P50TTFTMs > 0 {
			ttft = fmt.Sprintf("%dms", m.P50TTFTMs)
		}
		model := termtext.Clean(m.Provider + "/" + m.Model)
		if hasEstimated {
			fmt.Fprintf(tw, "%s\t%d\t%.1f\t%dms\t%dms\t%s\t%d\t%d\t%d\t%d\t%.1f\t$%.4f\t%d 次估算\n",
				model, m.Calls, m.ErrorRate*100, m.P50Ms, m.P95Ms, ttft,
				m.InputTokens, m.OutputTokens, m.CacheRead, m.CacheWrite, m.CacheHitRate*100, m.CostUSD, m.EstimatedCalls)
		} else {
			fmt.Fprintf(tw, "%s\t%d\t%.1f\t%dms\t%dms\t%s\t%d\t%d\t%d\t%d\t%.1f\t$%.4f\n",
				model, m.Calls, m.ErrorRate*100, m.P50Ms, m.P95Ms, ttft,
				m.InputTokens, m.OutputTokens, m.CacheRead, m.CacheWrite, m.CacheHitRate*100, m.CostUSD)
		}
	}
	fmt.Fprintln(tw, "\t\t\t\t\t\t\t\t\t\t\t")
	fmt.Fprintln(tw, "TOOL\tCALLS\tERR%\tDENIED\tP50\tP95\t\t\t\t\t\t")
	for _, t := range rep.Tools {
		fmt.Fprintf(tw, "%s\t%d\t%.1f\t%d\t%dms\t%dms\t\t\t\t\t\t\n", t.Name, t.Calls, t.ErrorRate*100, t.Denials, t.P50Ms, t.P95Ms)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "\ncompactions: %d  tool_searches: %d\n", rep.Compactions, rep.ToolSearch)
	return err
}
