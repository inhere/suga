package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal/store"
)

// newDataUsageCommand builds `suga data usage`: a read-only report of where the
// space of the session store and the sessions root went.
func newDataUsageCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var asJSON bool
	var top int
	cmd := capp.NewCmd("usage", "Report where the session store and the sessions directory hold their bytes (read-only)", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) > 0 {
			return fmt.Errorf("usage: suga data usage [--json] [--top N]")
		}
		if top < 0 {
			return fmt.Errorf("--top must be 0 or more")
		}
		return runDataUsage(ctx, stdout, asJSON, top)
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&asJSON, "json", false, "write a single JSON object")
	cmd.IntVar(&top, "top", 10, "rows per breakdown: metadata keys, tools, event kinds, sessions, tables")
	return cmd
}

// runDataUsage opens the store read-only (no migration, no maintenance lock, no
// write) and prints the usage report.
func runDataUsage(ctx context.Context, out io.Writer, asJSON bool, top int) error {
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return err
	}
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return err
	}
	repo, err := store.OpenReadOnly(dbPath, store.WithSessionsDir(sessionsDir))
	if err != nil {
		return err
	}
	defer repo.Close()
	report, err := repo.Usage(ctx, store.UsageOptions{Top: top, Translator: cliTranslator()})
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(report)
	}
	return writeDataUsage(out, report)
}

// writeDataUsage prints the report as the sectioned table the other data
// commands use: one block per question, with the command that acts on it. The
// file path stands on its own line so a long home does not stretch the table.
func writeDataUsage(out io.Writer, rep store.UsageReport) error {
	tr := cliTranslator()
	fmt.Fprintln(out, cliText(tr, "cli.data.usage.file", map[string]any{"path": rep.File.Path, "bytes": humanBytes(rep.File.Bytes)}))
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\t%d × %d %s\t\n", cliText(tr, "cli.data.usage.pages"), rep.File.PageSize, rep.File.Pages, cliText(tr, "cli.data.usage.pages"))
	fmt.Fprintf(tw, "  %s\t%d %s\t%s\n", cliText(tr, "cli.data.usage.free_pages"), rep.File.FreePages, cliText(tr, "cli.data.usage.pages"), humanBytes(rep.File.FreeBytes))
	fmt.Fprintf(tw, "  %s\t\t%s\n", cliText(tr, "cli.data.usage.wal"), humanBytes(rep.File.WALBytes))
	// entries
	fmt.Fprintf(tw, "%s\t%s\t%s\n", cliText(tr, "cli.data.usage.entries"), usageCount(tr, usageCellRows, rep.Entries.Rows), humanBytes(rep.Entries.Bytes))
	for _, stat := range rep.Entries.Types {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", stat.Name, usageCount(tr, usageCellRows, stat.Rows), humanBytes(stat.Bytes))
	}
	if rep.Entries.Assistant.Rows > 0 {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", cliText(tr, "cli.data.usage.assistant"), usageCount(tr, usageCellRows, rep.Entries.Assistant.Rows), humanBytes(rep.Entries.Assistant.Bytes))
		for _, stat := range rep.Entries.Assistant.Fields {
			fmt.Fprintf(tw, "  %s\t\t%s\n", stat.Name, humanBytes(stat.Bytes))
		}
	}
	if len(rep.Entries.Assistant.MetadataKeys) > 0 {
		fmt.Fprintln(tw, cliText(tr, "cli.data.usage.metadata")+"\t\t")
		for _, stat := range rep.Entries.Assistant.MetadataKeys {
			fmt.Fprintf(tw, "  %s\t\t%s\n", stat.Name, humanBytes(stat.Bytes))
		}
	}
	if len(rep.Entries.Assistant.Tools) > 0 {
		fmt.Fprintln(tw, cliText(tr, "cli.data.usage.tool_args")+"\t"+cliText(tr, "cli.data.usage.cell.call_count")+"\t")
		for _, stat := range rep.Entries.Assistant.Tools {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", stat.Name, usageCount(tr, usageCellCalls, stat.Rows), humanBytes(stat.Bytes))
		}
	}
	if rep.Entries.Tool.Stub.Rows+rep.Entries.Tool.Inline.Rows > 0 {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", cliText(tr, "cli.data.usage.tool_stub"), usageCount(tr, usageCellRows, rep.Entries.Tool.Stub.Rows), humanBytes(rep.Entries.Tool.Stub.Bytes))
		fmt.Fprintf(tw, "%s\t%s\t%s\n", cliText(tr, "cli.data.usage.tool_inline"), usageCount(tr, usageCellRows, rep.Entries.Tool.Inline.Rows), humanBytes(rep.Entries.Tool.Inline.Bytes))
	}
	// The bodies that left the ledger live in the session files: their count and
	// the file bytes say how much the ledger no longer holds.
	if rep.Entries.Tool.Stub.Rows+rep.Entries.MovedAssistant.Rows > 0 {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", cliText(tr, "cli.data.usage.moved"),
			cliText(tr, "cli.data.usage.cell.moved", map[string]any{"tools": rep.Entries.Tool.Stub.Rows, "assistant": rep.Entries.MovedAssistant.Rows}),
			humanBytes(rep.Sessions.Tools.Bytes))
	}
	// events
	fmt.Fprintf(tw, "%s\t%s\t%s\n", cliText(tr, "cli.data.usage.events"), usageCount(tr, usageCellRows, rep.Events.Rows), humanBytes(rep.Events.Bytes))
	for _, stat := range rep.Events.Kinds {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", stat.Name, usageCount(tr, usageCellRows, stat.Rows), humanBytes(stat.Bytes))
	}
	// FTS
	fmt.Fprintf(tw, "%s\t%s\t%s\n", cliText(tr, "cli.data.usage.fts"), usageCount(tr, usageCellLines, rep.FTS.Rows), humanBytes(rep.FTS.Bytes))
	fmt.Fprintf(tw, "  entries_fts_data\t\t%s\n", humanBytes(rep.FTS.DataBytes))
	fmt.Fprintf(tw, "  entries_fts_content\t\t%s\n", humanBytes(rep.FTS.ContentBytes))
	for _, stat := range rep.FTS.Roles {
		fmt.Fprintf(tw, "  role=%s\t%s\t\n", stat.Name, usageCount(tr, usageCellLines, stat.Rows))
	}
	// sessions directory
	fmt.Fprintf(tw, "%s\t%s\t\n", cliText(tr, "cli.data.usage.sessions"),
		cliText(tr, "cli.data.usage.cell.sessions", map[string]any{"count": rep.Sessions.Dirs, "instore": rep.Sessions.InStore}))
	// One line per body file name found: a store restored from an old backup can
	// hold both tools.jsonl and bodies.jsonl.
	for _, stat := range rep.Sessions.BodyFiles {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", stat.Name, usageCount(tr, usageCellFiles, stat.Rows), humanBytes(stat.Bytes))
	}
	fmt.Fprintf(tw, "  outputs/\t%s\t%s\n", cliText(tr, "cli.data.usage.cell.outputs", map[string]any{"files": rep.Sessions.Outputs.Files, "gz": rep.Sessions.OutputsGz.Files}), humanBytes(rep.Sessions.Outputs.Bytes))
	fmt.Fprintf(tw, "  images/\t%s\t%s\n", usageCount(tr, usageCellFiles, rep.Sessions.Images.Files), humanBytes(rep.Sessions.Images.Bytes))
	fmt.Fprintf(tw, "  snapshots/\t%s\t%s\n", usageCount(tr, usageCellFiles, rep.Sessions.Snapshots.Files), humanBytes(rep.Sessions.Snapshots.Bytes))
	if rep.Sessions.Other.Files > 0 {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", cliText(tr, "cli.data.usage.other"), usageCount(tr, usageCellFiles, rep.Sessions.Other.Files), humanBytes(rep.Sessions.Other.Bytes))
	}
	// dbstat: the real page usage of each table and index.
	if rep.DBStat {
		fmt.Fprintln(tw, cliText(tr, "cli.data.usage.tables")+"\t\t")
		for _, table := range rep.Tables {
			fmt.Fprintf(tw, "  %s\t\t%s\n", table.Name, humanBytes(table.Bytes))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// The largest sessions get a table of their own: a long id/title in the main
	// table would widen every other row.
	if err := writeUsageTopSessions(out, rep.Sessions.Top); err != nil {
		return err
	}
	fmt.Fprintln(out, cliText(tr, "cli.data.usage.total", map[string]any{"total": humanBytes(rep.TotalBytes), "file": humanBytes(rep.File.Bytes), "sessions": humanBytes(rep.TotalBytes - rep.File.Bytes)}))
	if !rep.DBStat {
		fmt.Fprintln(out, cliText(tr, "cli.data.usage.no_dbstat"))
	}
	writeUsageSuggestions(out, rep, tr)
	return nil
}

// Cells of the usage table that carry a count: the unit travels with the number
// in the catalog, so a language switch needs no format change in the code.
const (
	usageCellRows  = "cli.data.usage.cell.rows"
	usageCellLines = "cli.data.usage.cell.lines"
	usageCellCalls = "cli.data.usage.cell.calls"
	usageCellFiles = "cli.data.usage.cell.files"
)

// usageCount renders one "<n> <unit>" cell of the usage table.
func usageCount(tr i18n.Translator, key string, count int64) string {
	return cliText(tr, key, map[string]any{"count": count})
}

// writeUsageTopSessions prints the largest sessions: ledger bytes, directory
// bytes and the title, one line each.
func writeUsageTopSessions(out io.Writer, sessions []store.UsageSession) error {
	if len(sessions) == 0 {
		return nil
	}
	fmt.Fprintln(out, cliText(cliTranslator(), "cli.data.usage.largest"))
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, session := range sessions {
		title := session.Title
		if title == "" {
			title = "-"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", session.ID, humanBytes(session.DBBytes),
			humanBytes(session.DirBytes), truncateTitle(title))
	}
	return tw.Flush()
}

// truncateTitle keeps a session title on one line of the table.
func truncateTitle(title string) string {
	runes := []rune(strings.Join(strings.Fields(title), " "))
	if len(runes) > 24 {
		return string(runes[:24]) + "…"
	}
	return string(runes)
}

// writeUsageSuggestions prints the "what now" line of every section that found
// something to do.
func writeUsageSuggestions(out io.Writer, rep store.UsageReport, tr i18n.Translator) {
	var hints []string
	for _, hint := range []string{
		rep.File.Suggestion, rep.Entries.Suggestion, rep.Events.Suggestion,
		rep.FTS.Suggestion, rep.Sessions.Suggestion,
	} {
		if hint != "" {
			hints = append(hints, hint)
		}
	}
	if len(hints) == 0 {
		return
	}
	fmt.Fprintln(out, cliText(tr, "cli.data.usage.actionable"))
	for _, hint := range hints {
		fmt.Fprintf(out, "  - %s\n", hint)
	}
}
