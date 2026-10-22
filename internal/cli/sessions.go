package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// sessionsOptions are the filters of `suga sessions`.
type sessionsOptions struct {
	limit        int
	since, until string
	query, cwd   string
	asJSON       bool
	// all lists sessions of every directory; the default scope is the current
	// working directory (design 2026-10-20, 会话列表默认当前目录).
	all bool
	// agents lists sub-agent sessions too (kept out by default: they belong
	// to the session that started them).
	agents bool
	// origin keeps only the sessions of one entry point (chat, run, acp, agent,
	// xhen); "all" lists every origin. The default leaves out the host-managed
	// sessions (origin xhen), which belong to the host application (design
	// 2026-10-20, 会话来源).
	origin string
}

func newSessionsCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	opts := sessionsOptions{limit: 10}
	cmd := capp.NewCmd("sessions", "List sessions, newest first (default the last 10)", func(c *capp.Cmd) error {
		// Keyword arguments add to --query.
		if words := strings.Join(c.Arg("keywords").Strings(), " "); words != "" {
			opts.query = strings.TrimSpace(opts.query + " " + words)
		}
		if opts.all && opts.cwd != "" {
			// Prefix walking (--cwd) reaches into subdirectories; --all has no
			// directory at all, and the two answer different questions.
			return fmt.Errorf("--cwd and --all are mutually exclusive")
		}
		return runSessions(ctx, stdout, stderr, opts, time.Now())
	})
	cmd.Aliases = []string{"ls"}
	cmd.SetOutput(stderr)
	cmd.IntVar(&opts.limit, "limit", 10, "how many sessions to show, 0 shows all;;n")
	cmd.StringVar(&opts.since, "since", "", "only sessions active since: 12h, 7d, or a date like 2026-10-01")
	cmd.StringVar(&opts.until, "until", "", "only sessions last active before: 12h, 7d, or a date")
	cmd.StringVar(&opts.query, "query", "", "keywords (all must match) in the title, id, directory, model or the conversation;;q")
	cmd.StringVar(&opts.cwd, "cwd", "", "only sessions whose directory starts with this path (. = current directory)")
	cmd.BoolVar(&opts.all, "all", false, "list sessions of every directory (default: the current directory only)")
	cmd.BoolVar(&opts.asJSON, "json", false, "write a JSON array")
	cmd.BoolVar(&opts.agents, "agents", false, "list sub-agent sessions too (each names its parent session)")
	cmd.StringVar(&opts.origin, "origin", "", "only sessions this entry point created: chat, run, acp, agent or xhen; 'all' lists every origin (default: all but xhen)")
	cmd.AddArg("keywords", "keywords, like --query", false, nil, true)
	return cmd
}

// sessionRow is one listed session (also the --json form).
type sessionRow struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Cwd         string    `json:"cwd"`
	Status      string    `json:"status"`
	Provider    string    `json:"provider,omitempty"`
	Model       string    `json:"model,omitempty"`
	SugaVersion string    `json:"suga_version,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Match is a snippet of the conversation when the keyword matched there.
	Match string `json:"match,omitempty"`
	// Parent is the session that started this sub-agent session (--agents).
	Parent string `json:"parent,omitempty"`
	// Origin is the entry point that created the session (meta.origin); empty
	// for a session from before the key existed.
	Origin string `json:"origin,omitempty"`
}

func runSessions(ctx context.Context, out, errOut io.Writer, opts sessionsOptions, now time.Time) error {
	var from, to time.Time
	var err error
	if opts.since != "" {
		if from, err = parseWhen(opts.since, now); err != nil {
			return fmt.Errorf("--since: %w", err)
		}
	}
	if opts.until != "" {
		if to, err = parseWhen(opts.until, now); err != nil {
			return fmt.Errorf("--until: %w", err)
		}
	}
	// Default scope (no --cwd, no --all): the current directory and only it.
	// Its absolute form is resolved once; SameDir folds the case on Windows.
	var cwd, here string
	sameDirScope := opts.cwd == "" && !opts.all
	cwd = opts.cwd
	if cwd == "." {
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	if sameDirScope {
		if here, err = os.Getwd(); err != nil {
			return err
		}
		here = personal.NormalizeDir(here)
	}
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()
	all, err := repo.ListSessions(ctx)
	if err != nil {
		return err
	}
	// Keywords match the session's own fields, or its conversation (the
	// ledger index; the first snippet is shown).
	terms := strings.Fields(strings.ToLower(opts.query))
	snippets := map[string]string{}
	if len(terms) > 0 {
		hits, err := repo.SearchEntries(ctx, store.EntrySearch{Query: opts.query, Limit: 100, IncludeAgents: opts.agents})
		if err != nil {
			return err
		}
		for _, h := range hits {
			if _, seen := snippets[h.SessionID]; !seen {
				snippets[h.SessionID] = h.Snippet
			}
		}
	}
	var rows []sessionRow
	// inScope reports whether a session falls into the directory scope of this
	// call (the current directory by default, a path prefix or everything).
	inScope := func(s store.Session) bool {
		switch {
		case sameDirScope:
			// No recorded directory (older sessions) stays out of the default list
			// as well: --all is the way to see them.
			return s.Cwd != "" && personal.SameDir(s.Cwd, here)
		case cwd != "":
			return pathHasPrefix(s.Cwd, cwd)
		}
		return true
	}
	// otherDirs counts the sessions the default scope hides: an empty result
	// names their number, so a person notices the directory may be wrong. The
	// host-managed ones are named separately instead of twice.
	otherDirs := 0
	if sameDirScope {
		for _, s := range all {
			if s.Cwd == "" || store.IsAgentSession(s) || store.HostManagedSession(s) || personal.SameDir(s.Cwd, here) {
				continue
			}
			otherDirs++
		}
	}
	// hiddenXhen counts the host-managed sessions (origin xhen) the default
	// origin filter leaves out of this call's scope: an empty result names their
	// number too, with the way to list them.
	hiddenXhen := 0
	if opts.origin == "" {
		for _, s := range all {
			if store.HostManagedSession(s) && !store.IsAgentSession(s) && inScope(s) {
				hiddenXhen++
			}
		}
	}
	for _, s := range all {
		if !from.IsZero() && s.UpdatedAt.Before(from) || !to.IsZero() && s.UpdatedAt.After(to) {
			continue
		}
		if store.IsAgentSession(s) && !opts.agents {
			continue
		}
		if !inScope(s) {
			continue
		}
		// Origin: the default leaves out the host-managed sessions (origin
		// xhen), which the host application lists and keeps on its own terms;
		// --origin names one entry point, --origin all every one of them.
		origin := store.SessionOrigin(s)
		if opts.origin == "" {
			if store.HostManagedSession(s) {
				continue
			}
		} else if opts.origin != "all" && origin != opts.origin {
			continue
		}
		// Older sessions stored the whole first prompt as the title.
		row := sessionRow{ID: s.ID, Title: personal.SessionTitle(s.Title), Cwd: s.Cwd, Status: s.Status, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Origin: origin}
		row.Provider, _ = s.Meta["provider"].(string)
		row.Model, _ = s.Meta["model"].(string)
		row.SugaVersion, _ = s.Meta["suga_version"].(string)
		row.Parent = store.ParentSession(s)
		if len(terms) > 0 {
			own := strings.ToLower(strings.Join([]string{s.ID, s.Title, s.Cwd, row.Provider, row.Model}, " "))
			matched := true
			for _, t := range terms {
				matched = matched && strings.Contains(own, t)
			}
			if snippet, ok := snippets[s.ID]; !matched && ok {
				row.Match, matched = snippet, true
			}
			if !matched {
				continue
			}
		}
		rows = append(rows, row)
	}
	total := len(rows)
	if opts.limit > 0 && len(rows) > opts.limit {
		rows = rows[:opts.limit]
	}
	if opts.asJSON {
		if rows == nil {
			rows = []sessionRow{}
		}
		writeEmptyHint(errOut, sameDirScope, here, otherDirs, hiddenXhen, rows)
		return json.NewEncoder(out).Encode(rows)
	}
	writeEmptyHint(errOut, sameDirScope, here, otherDirs, hiddenXhen, rows)
	if len(rows) == 0 {
		return nil
	}
	return writeSessionRows(out, rows, total, here, sameDirScope)
}

// emptyScopeHint is the stderr line of an empty result (design 2026-10-20):
// it is the shared personal.EmptyScopeHint text, so the CLI and the ACP
// session/list hint agree word for word.
func emptyScopeHint(dir string, otherDirs int) string {
	return personal.EmptyScopeHint(dir, otherDirs)
}

// writeEmptyHint prints the empty-scope hint on stderr — also for --json, so
// stdout stays machine data. Only the plain default view hints: an empty
// filtered (--cwd, --since, --query) result reports the filters, not the scope.
// An empty result also names the host-managed sessions the default origin
// filter hid, when there are any.
func writeEmptyHint(errOut io.Writer, sameDirScope bool, here string, otherDirs, hiddenXhen int, rows []sessionRow) {
	if len(rows) > 0 {
		return
	}
	if sameDirScope {
		fmt.Fprintln(errOut, emptyScopeHint(here, otherDirs))
	}
	if hiddenXhen > 0 {
		fmt.Fprintf(errOut, "另有 %d 个 origin=xhen 的会话未列出（--origin xhen 查看，--origin all 列出全部来源）\n", hiddenXhen)
	}
}

// writeSessionRows renders the table; the header names the scope, so a list
// read from another directory never looks like the whole catalog.
func writeSessionRows(out io.Writer, rows []sessionRow, total int, here string, sameDirScope bool) error {
	if sameDirScope {
		fmt.Fprintf(out, "当前目录 %s 的会话（--all 列出全部）\n", here)
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "UPDATED\tID\tMODEL\tSUGA\tTITLE\tDIRECTORY")
	for _, r := range rows {
		model := r.Model
		if r.Provider != "" {
			model = r.Provider + "/" + r.Model
		}
		title := clip(oneLine(r.Title), 40)
		if r.Parent != "" { // a sub-agent session (--agents) names its parent
			title = "[agent of " + r.Parent + "] " + title
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.UpdatedAt.Local().Format("01-02 15:04"), r.ID,
			orDash(model), orDash(r.SugaVersion), orDash(title), orDash(shortDir(r.Cwd)))
		if r.Match != "" {
			fmt.Fprintf(tw, "\t  ↳ %s\t\t\t\t\n", clip(oneLine(r.Match), 100))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if total > len(rows) {
		_, err := fmt.Fprintf(out, "%d of %d sessions; -n 0 shows all\n", len(rows), total)
		return err
	}
	return nil
}

// parseWhen reads a point in time: a duration back from now (12h, 7d) or a
// local date / date-time (2026-10-01, 2026-10-01 15:04, RFC 3339).
func parseWhen(s string, now time.Time) (time.Time, error) {
	if d, err := parseSince(s); err == nil {
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q: want 12h, 7d or a date like 2026-10-01", s)
}

// slashPath writes both separators as "/": sessions recorded on Windows are
// listed on any system.
func slashPath(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// pathHasPrefix compares directories by their slash form, ignoring case
// (Windows paths).
func pathHasPrefix(dir, prefix string) bool {
	norm := func(p string) string { return strings.ToLower(strings.TrimRight(slashPath(p), "/")) }
	d, p := norm(dir), norm(prefix)
	return d == p || strings.HasPrefix(d, p+"/")
}

// shortDir keeps the last two path elements.
func shortDir(p string) string {
	parts := strings.Split(strings.TrimRight(slashPath(p), "/"), "/")
	if len(parts) <= 2 {
		return p
	}
	return "…/" + strings.Join(parts[len(parts)-2:], "/")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
