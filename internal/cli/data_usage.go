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
	report, err := repo.Usage(ctx, store.UsageOptions{Top: top})
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(report)
	}
	return writeDataUsage(out, report)
}

// writeDataUsage prints the report as the sectioned table the other data
// commands use: one block per question, with the command that acts on it.
func writeDataUsage(out io.Writer, rep store.UsageReport) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	// 库文件
	fmt.Fprintf(tw, "库文件\t%s\t%s\n", rep.File.Path, humanBytes(rep.File.Bytes))
	fmt.Fprintf(tw, "  页\t%s × %d 页\t\n", humanBytes(rep.File.PageSize), rep.File.Pages)
	fmt.Fprintf(tw, "  空闲页\t%d 页\t%s\n", rep.File.FreePages, humanBytes(rep.File.FreeBytes))
	fmt.Fprintf(tw, "  WAL\t\t%s\n", humanBytes(rep.File.WALBytes))
	// entries
	fmt.Fprintf(tw, "entries\t%d 条\t%s\n", rep.Entries.Rows, humanBytes(rep.Entries.Bytes))
	for _, stat := range rep.Entries.Types {
		fmt.Fprintf(tw, "  %s\t%d 条\t%s\n", stat.Name, stat.Rows, humanBytes(stat.Bytes))
	}
	if rep.Entries.Assistant.Rows > 0 {
		fmt.Fprintf(tw, "assistant 字段\t%d 条\t%s\n", rep.Entries.Assistant.Rows, humanBytes(rep.Entries.Assistant.Bytes))
		for _, stat := range rep.Entries.Assistant.Fields {
			fmt.Fprintf(tw, "  %s\t\t%s\n", stat.Name, humanBytes(stat.Bytes))
		}
	}
	if len(rep.Entries.Assistant.MetadataKeys) > 0 {
		fmt.Fprintln(tw, "Metadata 键\t\t")
		for _, stat := range rep.Entries.Assistant.MetadataKeys {
			fmt.Fprintf(tw, "  %s\t\t%s\n", stat.Name, humanBytes(stat.Bytes))
		}
	}
	if len(rep.Entries.Assistant.Tools) > 0 {
		fmt.Fprintln(tw, "工具调用参数\t调用次数\t")
		for _, stat := range rep.Entries.Assistant.Tools {
			fmt.Fprintf(tw, "  %s\t%d 次\t%s\n", stat.Name, stat.Rows, humanBytes(stat.Bytes))
		}
	}
	if rep.Entries.Tool.Stub.Rows+rep.Entries.Tool.Inline.Rows > 0 {
		fmt.Fprintf(tw, "tool 结果（桩）\t%d 条\t%s\n", rep.Entries.Tool.Stub.Rows, humanBytes(rep.Entries.Tool.Stub.Bytes))
		fmt.Fprintf(tw, "tool 结果（库内）\t%d 条\t%s\n", rep.Entries.Tool.Inline.Rows, humanBytes(rep.Entries.Tool.Inline.Bytes))
	}
	// events
	fmt.Fprintf(tw, "events\t%d 条\t%s\n", rep.Events.Rows, humanBytes(rep.Events.Bytes))
	for _, stat := range rep.Events.Kinds {
		fmt.Fprintf(tw, "  %s\t%d 条\t%s\n", stat.Name, stat.Rows, humanBytes(stat.Bytes))
	}
	// FTS
	fmt.Fprintf(tw, "全文索引\t%d 行\t%s\n", rep.FTS.Rows, humanBytes(rep.FTS.Bytes))
	fmt.Fprintf(tw, "  entries_fts_data\t\t%s\n", humanBytes(rep.FTS.DataBytes))
	fmt.Fprintf(tw, "  entries_fts_content\t\t%s\n", humanBytes(rep.FTS.ContentBytes))
	for _, stat := range rep.FTS.Roles {
		fmt.Fprintf(tw, "  role=%s\t%d 行\t\n", stat.Name, stat.Rows)
	}
	// sessions directory
	fmt.Fprintf(tw, "会话目录\t%d 个（库内 %d 个会话）\t\n", rep.Sessions.Dirs, rep.Sessions.InStore)
	fmt.Fprintf(tw, "  tools.jsonl\t%d 个文件\t%s\n", rep.Sessions.Tools.Files, humanBytes(rep.Sessions.Tools.Bytes))
	fmt.Fprintf(tw, "  outputs/\t%d 个文件（.gz %d）\t%s\n", rep.Sessions.Outputs.Files, rep.Sessions.OutputsGz.Files, humanBytes(rep.Sessions.Outputs.Bytes))
	fmt.Fprintf(tw, "  images/\t%d 个文件\t%s\n", rep.Sessions.Images.Files, humanBytes(rep.Sessions.Images.Bytes))
	fmt.Fprintf(tw, "  snapshots/\t%d 个文件\t%s\n", rep.Sessions.Snapshots.Files, humanBytes(rep.Sessions.Snapshots.Bytes))
	if rep.Sessions.Other.Files > 0 {
		fmt.Fprintf(tw, "  其他\t%d 个文件\t%s\n", rep.Sessions.Other.Files, humanBytes(rep.Sessions.Other.Bytes))
	}
	if len(rep.Sessions.Top) > 0 {
		fmt.Fprintln(tw, "最大的会话\t库内\t目录")
		for _, session := range rep.Sessions.Top {
			title := session.Title
			if title == "" {
				title = "-"
			}
			fmt.Fprintf(tw, "  %s  %s\t%s\t%s\n", session.ID, truncateTitle(title), humanBytes(session.DBBytes), humanBytes(session.DirBytes))
		}
	}
	// dbstat: the real page usage of each table and index.
	if rep.DBStat {
		fmt.Fprintln(tw, "表/索引（dbstat）\t\t")
		for _, table := range rep.Tables {
			fmt.Fprintf(tw, "  %s\t\t%s\n", table.Name, humanBytes(table.Bytes))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "合计 %s：库文件 %s + 会话目录 %s\n", humanBytes(rep.TotalBytes),
		humanBytes(rep.File.Bytes), humanBytes(rep.TotalBytes-rep.File.Bytes))
	if !rep.DBStat {
		fmt.Fprintln(out, "本次构建的 SQLite 没有 dbstat：上面的条目字节不含 B 树与页内开销")
	}
	writeUsageSuggestions(out, rep)
	return nil
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
func writeUsageSuggestions(out io.Writer, rep store.UsageReport) {
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
	fmt.Fprintln(out, "可处理：")
	for _, hint := range hints {
		fmt.Fprintf(out, "  - %s\n", hint)
	}
}
