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
	// The cache buckets need the calls in order per session, so they come from
	// their own pass (BuildReport aggregates in SQL and is bucket-blind).
	buckets, err := repo.BuildCacheBuckets(ctx, f, 0)
	if err != nil {
		return err
	}
	rep.CacheBuckets = buckets.Buckets
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
			fmt.Fprintf(tw, "%s\t%d\t%.1f\t%dms\t%dms\t%s\t%d\t%d\t%d\t%d\t%.1f\t$%.4f\t%s\n",
				model, m.Calls, m.ErrorRate*100, m.P50Ms, m.P95Ms, ttft,
				m.InputTokens, m.OutputTokens, m.CacheRead, m.CacheWrite, m.CacheHitRate*100, m.CostUSD, cliText(cliTranslator(), "cli.stats.estimated", map[string]any{"count": m.EstimatedCalls}))
		} else {
			fmt.Fprintf(tw, "%s\t%d\t%.1f\t%dms\t%dms\t%s\t%d\t%d\t%d\t%d\t%.1f\t$%.4f\n",
				model, m.Calls, m.ErrorRate*100, m.P50Ms, m.P95Ms, ttft,
				m.InputTokens, m.OutputTokens, m.CacheRead, m.CacheWrite, m.CacheHitRate*100, m.CostUSD)
		}
	}
	if len(rep.CacheBuckets) > 0 {
		fmt.Fprintln(tw, "\t\t\t\t\t\t\t\t\t\t\t")
		fmt.Fprintln(tw, "CACHE BUCKET\tCALLS\tPROMPT\tCACHE-R\tHIT%\tREUSE%\t\t\t\t\t")
		var total store.CacheBucketStat
		for _, b := range rep.CacheBuckets {
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\t\t\t\t\t\n", b.Bucket, b.Calls,
				b.InputTokens+b.CacheRead+b.CacheWrite, b.CacheRead, hitCell(b), reuseCell(b))
			total.Calls += b.Calls
			total.InputTokens += b.InputTokens
			total.CacheRead += b.CacheRead
			total.CacheWrite += b.CacheWrite
			total.PrefixTokens += b.PrefixTokens
			total.CacheReported = total.CacheReported || b.CacheReported
		}
		// The overall row beside the buckets: what the per-bucket numbers add up to.
		if prompt := total.InputTokens + total.CacheRead + total.CacheWrite; prompt > 0 {
			total.CacheHitRate = float64(total.CacheRead) / float64(prompt)
		}
		// REUSE% is cache read over the estimated cacheable prefix of the previous
		// requests (see store.CacheBucketStat.PrefixTokens), capped at 100%.
		total.PrefixReuse = float64(min(total.CacheRead, total.PrefixTokens)) / float64(max(total.PrefixTokens, 1))
		if total.PrefixTokens == 0 {
			total.PrefixReuse = 0
		}
		fmt.Fprintf(tw, "total\t%d\t%d\t%d\t%s\t%s\t\t\t\t\t\n", total.Calls,
			total.InputTokens+total.CacheRead+total.CacheWrite, total.CacheRead, hitCell(total), reuseCell(total))
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

// hitCell renders a cache bucket's hit rate; a bucket without cache data of
// its provider shows "-" instead of 0%.
func hitCell(b store.CacheBucketStat) string {
	if !b.CacheReported {
		return "-"
	}
	return fmt.Sprintf("%.1f", b.CacheHitRate*100)
}

// reuseCell renders a cache bucket's prefix reuse rate (cache read over the
// estimated cacheable prefix), "-" when no call of the bucket carried a prefix
// estimate (an older session).
func reuseCell(b store.CacheBucketStat) string {
	if b.PrefixTokens == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", b.PrefixReuse*100)
}
