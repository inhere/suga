package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gookit/goutil/cflag/capp"
	"github.com/inhere/suga/internal/wiring"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// lintOptions is the parsed input of `suga memory lint`.
type lintOptions struct {
	scope  string // "", "user" or "project"
	asJSON bool   // write the stable JSON report instead of text
}

// lintReport is the stable --json structure of `suga memory lint`. It is
// read-only by construction: the command only reads entries and never writes.
type lintReport struct {
	ReadOnly bool              `json:"read_only"`
	Scopes   []lintScopeReport `json:"scopes"`
	Totals   lintTotals        `json:"totals"`
}

// lintScopeReport is one scope of the report.
type lintScopeReport struct {
	Scope      string          `json:"scope"`
	Entries    int             `json:"entries"`
	BodiesRead bool            `json:"bodies_read"` // false: only index lines were read
	Candidates []lintCandidate `json:"candidates"`
	Notes      []string        `json:"notes,omitempty"`
}

// lintTotals sums the scopes.
type lintTotals struct {
	Entries    int `json:"entries"`
	Candidates int `json:"candidates"`
}

// newMemoryLintCommand builds `suga memory lint`: a read-only listing of
// duplicate, possibly conflicting and possibly stale entries. It never writes
// memory and never calls a model; the exit code stays 0 even when it reports
// candidates, because this is a report and not a check.
func newMemoryLintCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	opts := lintOptions{}
	cmd := capp.NewCmd("lint", "Read-only listing of duplicate, possibly conflicting and possibly stale entries", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga memory lint [--scope user|project] [--json]")
		}
		return runMemoryLintCommand(ctx, stdout, opts)
	})
	cmd.SetOutput(stderr)
	cmd.StringVar(&opts.scope, "scope", "", "user or project (default: both)")
	cmd.BoolVar(&opts.asJSON, "json", false, "write one JSON object instead of the text report")
	return cmd
}

// runMemoryLintCommand resolves the memory backends of the working directory
// and prints the report. It builds the same stores the agent runtime uses, so
// the report sees the same entries.
func runMemoryLintCommand(ctx context.Context, out io.Writer, opts lintOptions) error {
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
	return runMemoryLint(ctx, out, setup.Router, scopes, opts)
}

// runMemoryLint builds the report over the registered backends and prints it.
// It takes the router as a parameter so tests can drive it without a
// configuration file or the working directory.
func runMemoryLint(ctx context.Context, out io.Writer, router *memory.Router, scopes []memory.Scope, opts lintOptions) error {
	now := time.Now()
	report := lintReport{ReadOnly: true, Scopes: []lintScopeReport{}}
	for _, sc := range scopes {
		backend, err := router.Route(sc)
		if errors.Is(err, memory.ErrNoBackend) {
			continue // no backend for this scope: nothing to report
		}
		if err != nil {
			return err
		}
		entries, err := lintLoadScope(ctx, backend, sc)
		if err != nil {
			return err
		}
		candidates, notes := lintScanScope(entries, now)
		report.Scopes = append(report.Scopes, lintScopeReport{
			Scope:      sc.String(),
			Entries:    len(entries),
			BodiesRead: len(entries) > 0 && entries[0].hasBody,
			Candidates: candidates,
			Notes:      notes,
		})
		report.Totals.Entries += len(entries)
		report.Totals.Candidates += len(candidates)
	}
	if opts.asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printLintReport(out, report)
	return nil
}

// lintLoadScope reads every entry of one scope. Enumerable backends (files)
// give title, body and the last update, which the stale hint needs; other
// backends only give their index lines, which still detects duplicates and
// conflicting titles but not age.
func lintLoadScope(ctx context.Context, backend memory.Store, sc memory.Scope) ([]lintEntry, error) {
	if enum, ok := backend.(memory.Enumerator); ok {
		all, err := enum.Entries(ctx, sc)
		if err != nil {
			return nil, err
		}
		entries := make([]lintEntry, 0, len(all))
		for _, e := range all {
			if memory.ValidateID(e.ID) != nil {
				continue // a backend is untrusted: ignore ids no store could have written
			}
			entries = append(entries, lintEntry{ID: e.ID, Title: e.Title, Body: e.Body, Summary: e.Summary,
				Kind: e.Kind, Updated: e.Updated, hasBody: true})
		}
		// A hand-written entry file may carry no title or kind in its front
		// matter; the index line fills those in, like the files backend's Get.
		if ix, ierr := backend.Index(ctx, sc); ierr == nil {
			fillLintFromIndex(entries, ix)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
		return entries, nil
	}

	ix, err := backend.Index(ctx, sc)
	if err != nil {
		return nil, err
	}
	entries := make([]lintEntry, 0, len(ix.Lines))
	for _, l := range ix.Lines {
		if memory.ValidateID(l.ID) != nil {
			continue
		}
		entries = append(entries, lintEntry{ID: l.ID, Title: l.Title, Summary: l.Summary, Kind: l.Kind})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

// fillLintFromIndex fills fields an entry file left out from its index line.
func fillLintFromIndex(entries []lintEntry, ix memory.Index) {
	byID := make(map[string]memory.IndexLine, len(ix.Lines))
	for _, l := range ix.Lines {
		byID[l.ID] = l
	}
	for i := range entries {
		l, ok := byID[entries[i].ID]
		if !ok {
			continue
		}
		if entries[i].Title == "" {
			entries[i].Title = l.Title
		}
		if !entries[i].Kind.Valid() {
			entries[i].Kind = l.Kind
		}
		if entries[i].Summary == "" {
			entries[i].Summary = l.Summary
		}
	}
}

// printLintReport renders the human-readable form: one block per scope, then a
// line saying that nothing was changed.
func printLintReport(out io.Writer, report lintReport) {
	if len(report.Scopes) == 0 {
		fmt.Fprintln(out, "no memory backend is configured for the selected scope")
		return
	}
	for _, sc := range report.Scopes {
		fmt.Fprintf(out, "%s memory (lint): %d entr%s\n", sc.Scope, sc.Entries, plural(sc.Entries))
		if !sc.BodiesRead && sc.Entries > 0 {
			fmt.Fprintln(out, "  note: this backend cannot enumerate entries; only titles and index summaries were compared, so age and bodies are not checked")
		}
		if len(sc.Candidates) == 0 {
			fmt.Fprintln(out, "  no candidate")
		}
		for _, c := range sc.Candidates {
			fmt.Fprintf(out, "- %s (%s): %s", c.Kind, c.Action, strings.Join(c.IDs, ", "))
			if c.More > 0 {
				fmt.Fprintf(out, " and %d more", c.More)
			}
			if c.Title != "" {
				fmt.Fprintf(out, " — %s", c.Title)
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  reason: %s\n", c.Reason)
		}
		for _, n := range sc.Notes {
			fmt.Fprintf(out, "note: %s\n", n)
		}
	}
	if report.Totals.Candidates == 0 {
		fmt.Fprintln(out, "no lint candidate; this report is read-only")
		return
	}
	fmt.Fprintf(out, "summary: %s across %s; this report is read-only, nothing was changed\n",
		lintCount(report.Totals.Candidates, "candidate", "candidates"),
		lintCount(report.Totals.Entries, "entry", "entries"))
}

// lintCount renders "n one" or "n many".
func lintCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
