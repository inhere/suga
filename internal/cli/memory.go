package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// newMemoryCommand builds the `memory` group. There was no `suga memory` group
// before this command; the group is introduced here because the subcommand
// maintains the long-term memory files themselves (MEMORY.md and entries),
// which is not session data (`inspect`/`data`) and not agent runtime state.
func newMemoryCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	group := capp.NewCmd("memory", "Maintain long-term memory: summaries (the index description of each entry)")
	group.SetOutput(stderr)
	group.Add(newMemorySummariesCommand(ctx, stdout, stderr))
	return group
}

// summariesOptions is the parsed input of `suga memory summaries`.
type summariesOptions struct {
	missing bool   // only entries without a summary
	fill    bool   // derive a summary from the body's first sentence
	apply   bool   // write the derived summaries (default: preview only)
	scope   string // "", "user" or "project"
}

// newMemorySummariesCommand builds `suga memory summaries`: it lists the index
// summary of every entry (--missing: only the ones without one) and, with
// --fill, previews the first sentence of their body as the missing summary.
// Nothing is written without --apply.
func newMemorySummariesCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	opts := summariesOptions{}
	cmd := capp.NewCmd("summaries", "List the index summary of each entry; --missing only those without one; --fill derives them from the body", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga memory summaries [--missing] [--fill] [--apply] [--scope user|project]")
		}
		if opts.apply && !opts.fill {
			return errors.New("--apply needs --fill: only derived summaries can be written")
		}
		return runMemorySummariesCommand(ctx, stdout, opts)
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&opts.missing, "missing", false, "only entries whose index line has no summary")
	cmd.BoolVar(&opts.fill, "fill", false, "derive the missing summary from the first sentence of the body")
	cmd.BoolVar(&opts.apply, "apply", false, "write the derived summaries (default is a preview)")
	cmd.StringVar(&opts.scope, "scope", "", "user or project (default: both)")
	return cmd
}

// runMemorySummariesCommand builds the memory store for the working directory
// and runs the report. Writes go through the governed store of the runtime so
// sanitizing and the index budget still apply.
func runMemorySummariesCommand(ctx context.Context, out io.Writer, opts summariesOptions) error {
	if opts.scope != "" && opts.scope != memory.ScopeUser && opts.scope != memory.ScopeProject {
		return fmt.Errorf("--scope must be user or project, got %q", opts.scope)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	setup, err := wiring.OpenMemoryStore(cwd)
	if err != nil {
		return err
	}
	scopes := []memory.Scope{setup.User.Scope, setup.Project.Scope}
	if opts.scope == memory.ScopeUser {
		scopes = scopes[:1]
	} else if opts.scope == memory.ScopeProject {
		scopes = scopes[1:]
	}
	store := memory.NewGoverned(setup.Router, memory.Policy{})
	return runMemorySummaries(ctx, out, store, scopes, opts)
}

// runMemorySummaries lists the entries of scopes and fills missing summaries.
// It is the whole behaviour of `suga memory summaries` and takes the store as a
// parameter so tests can drive it over a real files layout.
func runMemorySummaries(ctx context.Context, out io.Writer, store memory.Store, scopes []memory.Scope, opts summariesOptions) error {
	var total, missing, written int
	var unsupported []string
	for _, sc := range scopes {
		ix, err := store.Index(ctx, sc)
		if errors.Is(err, memory.ErrNoBackend) {
			continue // no backend for this scope: nothing to report.
		}
		if err != nil {
			return err
		}
		lines := make([]memory.IndexLine, 0, len(ix.Lines))
		for _, l := range ix.Lines {
			if !opts.missing || strings.TrimSpace(l.Summary) == "" {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(out, "%s memory (%s): %d entr%s\n", sc.String(), summarizeState(opts.missing), len(lines), plural(len(lines)))
		for _, l := range lines {
			fmt.Fprintf(out, "- %s (%s): %s\n", l.ID, l.Kind, l.Title)
			if l.Summary != "" {
				fmt.Fprintf(out, "  summary: %s\n", l.Summary)
				continue
			}
			missing++
			if !opts.fill {
				continue
			}
			e, gerr := store.Get(ctx, sc, l.ID)
			if gerr != nil {
				return gerr
			}
			summary := memory.FirstSentence(e.Body)
			if summary == "" {
				continue
			}
			fmt.Fprintf(out, "  fill: %s\n", summary)
			if !opts.apply {
				continue
			}
			sum, ok := summarizerFor(store, sc)
			if !ok {
				unsupported = append(unsupported, sc.String())
				continue
			}
			if serr := sum.SetSummary(ctx, sc, l.ID, summary); serr != nil {
				return serr
			}
			written++
		}
		total += len(lines)
	}
	printSummariesFooter(out, opts, total, missing, written, unsupported)
	return nil
}

// summarizeState names what the listing shows.
func summarizeState(missing bool) string {
	if missing {
		return "no summary"
	}
	return "all entries"
}

// plural returns the "ies"/"y" tail of "entries"/"entry".
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// summarizerFor finds the backend that can rewrite an index line of sc.
func summarizerFor(store memory.Store, sc memory.Scope) (memory.Summarizer, bool) {
	backend := store
	if r, ok := store.(memory.Resolver); ok {
		routed, err := r.Route(sc)
		if err != nil {
			return nil, false
		}
		backend = routed
	}
	sum, ok := backend.(memory.Summarizer)
	return sum, ok
}

// printSummariesFooter explains what happened and what the next step is.
func printSummariesFooter(out io.Writer, opts summariesOptions, total, missing, written int, unsupported []string) {
	switch {
	case total == 0 && opts.missing:
		fmt.Fprintln(out, "no entry is missing a summary")
	case total == 0:
		fmt.Fprintln(out, "no entry in this scope")
	case missing == 0:
		fmt.Fprintln(out, "every listed entry has a summary")
	case !opts.fill:
		fmt.Fprintln(out, "rerun with --fill to preview a summary from the first sentence of each body")
	case !opts.apply:
		fmt.Fprintln(out, "dry run: nothing was written; rerun with --fill --apply to store the summaries")
	default:
		fmt.Fprintf(out, "wrote %d summar%s\n", written, map[bool]string{true: "y", false: "ies"}[written == 1])
	}
	if len(unsupported) > 0 {
		fmt.Fprintf(out, "these backends cannot store a summary separately: %s\n", strings.Join(unsupported, ", "))
	}
}
