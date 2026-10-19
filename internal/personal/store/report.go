package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ModelStat aggregates kind=model events of one provider/model pair.
// InputTokens follows the REPORTING convention (cache excluded, see
// personal.ReportUsage): stored input minus cache read/write, floored at 0 per
// event. CacheHitRate = cache_read / (input + cache_read + cache_write).
type ModelStat struct {
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	Calls          int     `json:"calls"`
	EstimatedCalls int     `json:"estimated_calls,omitempty"`
	Errors         int     `json:"errors"`
	ErrorRate      float64 `json:"error_rate"`
	P50Ms          int64   `json:"p50_ms"`
	P95Ms          int64   `json:"p95_ms"`
	P50TTFTMs      int64   `json:"p50_ttft_ms,omitempty"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	CacheRead      int64   `json:"cache_read_tokens"`
	CacheWrite     int64   `json:"cache_write_tokens"`
	CacheHitRate   float64 `json:"cache_hit_rate"`
	CostUSD        float64 `json:"cost_usd"`
}

// ToolStat aggregates tool events by tool name. Calls counts executed tool
// events; denial events (which replace the tool event when approval refuses a
// call) are counted in Denials only.
type ToolStat struct {
	Name      string  `json:"name"`
	Calls     int     `json:"calls"`
	Errors    int     `json:"errors"`
	ErrorRate float64 `json:"error_rate"`
	Denials   int     `json:"denials"`
	P50Ms     int64   `json:"p50_ms"`
	P95Ms     int64   `json:"p95_ms"`
	// RawBytes / InlineBytes sum the tool output produced and the part kept in
	// the session (event attrs raw_bytes / inline_bytes); Spilled counts calls
	// whose full output was saved to the session directory.
	RawBytes    int64 `json:"raw_bytes"`
	InlineBytes int64 `json:"inline_bytes"`
	Spilled     int   `json:"spilled"`
}

// Report is the result of `suga stats`.
type Report struct {
	Models      []ModelStat `json:"models"`
	Tools       []ToolStat  `json:"tools"`
	Compactions int         `json:"compactions"`
	ToolSearch  int         `json:"tool_searches"`
}

// reportKinds are the event kinds `suga stats` aggregates.
var reportKinds = []string{EventModel, EventTool, EventDenial, EventCompaction, EventToolSearch}

// BuildReport queries events matching f (session/since) and aggregates them in
// SQL: GROUP BY/COUNT/SUM for the totals and one windowed pass per group kind
// for the nearest-rank percentiles. The result equals AggregateEvents over the
// same events (TestBuildReportMatchesAggregateEvents), but a report over a large
// ledger no longer pulls every event through Go and JSON-decodes its attrs.
func (s *Store) BuildReport(ctx context.Context, f EventFilter) (Report, error) {
	where, args := reportFilter(f)
	var rep Report
	models, err := s.queryModelStats(ctx, where, args)
	if err != nil {
		return Report{}, err
	}
	if err := s.addModelPercentiles(ctx, where, args, models); err != nil {
		return Report{}, err
	}
	rep.Models = sortModelStats(models)
	tools, err := s.queryToolStats(ctx, where, args)
	if err != nil {
		return Report{}, err
	}
	if err := s.addToolPercentiles(ctx, where, args, tools); err != nil {
		return Report{}, err
	}
	rep.Tools = sortToolStats(tools)
	rep.Compactions, rep.ToolSearch, err = s.queryReportCounts(ctx, where, args)
	if err != nil {
		return Report{}, err
	}
	return rep, nil
}

// reportFilter builds the WHERE clause shared by the report queries: the filter
// of ListEvents with the report's fixed kind list (ListEvents would also honour
// f.Kinds; the report always asks for exactly reportKinds).
func reportFilter(f EventFilter) (string, []any) {
	q := `kind IN (` + placeholders(len(reportKinds)) + `)`
	args := make([]any, 0, len(reportKinds)+4)
	for _, k := range reportKinds {
		args = append(args, k)
	}
	if f.SessionID != "" && f.WithAgents {
		q += ` AND (session_id = ? OR session_id IN (SELECT id FROM sessions WHERE json_extract(meta_json, '$.` + MetaParentSession + `') = ?))`
		args = append(args, f.SessionID, f.SessionID)
	} else if f.SessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, f.SessionID)
	}
	if f.TurnID != "" {
		q += ` AND turn_id = ?`
		args = append(args, f.TurnID)
	}
	if !f.Since.IsZero() {
		q += ` AND started_at >= ?`
		args = append(args, formatTime(f.Since))
	}
	return q, args
}

// reportModelExpr is the report's model key of an event, matching
// AggregateEvents: attrs.model when it decodes to a JSON string, ” otherwise.
const reportModelExpr = `CASE WHEN NOT json_valid(attrs_json) THEN ''
	WHEN json_type(attrs_json, '$.model') = 'text' THEN json_extract(attrs_json, '$.model') ELSE '' END`

// reportBoolExpr is a boolean event attr as Go's type assertion reads it: only
// JSON true counts, an absent or crafted value does not.
func reportBoolExpr(attr string) string {
	path := `'$.` + attr + `'`
	return `(json_valid(attrs_json) AND json_type(attrs_json, ` + path + `) = 'true')`
}

// reportNumExpr is a numeric event attr as attrInt64 reads it: a JSON number
// only (an integer or a real, not a string that looks like one).
func reportNumExpr(attr string) string {
	path := `'$.` + attr + `'`
	return `CASE WHEN NOT json_valid(attrs_json) THEN 0
		WHEN json_type(attrs_json, ` + path + `) IN ('integer', 'real') THEN json_extract(attrs_json, ` + path + `) ELSE 0 END`
}

// withArgs returns args plus extra in a fresh slice: the report queries share
// one filter argument list, and appending into it would overwrite a previous
// query's last placeholder.
func withArgs(args []any, extra ...any) []any {
	out := make([]any, 0, len(args)+len(extra))
	out = append(out, args...)
	return append(out, extra...)
}

// queryModelStats groups the model events into ModelStat rows (all fields but
// the percentiles and the rates).
func (s *Store) queryModelStats(ctx context.Context, where string, args []any) (map[[2]string]*ModelStat, error) {
	q := `SELECT name, ` + reportModelExpr + ` AS model, COUNT(1),
		SUM(CASE WHEN ` + reportBoolExpr("estimated") + ` THEN 1 ELSE 0 END),
		SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END),
		SUM(MAX(input_tokens - cache_read_tokens - cache_write_tokens, 0)),
		SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
		SUM(` + reportNumExpr("cost_usd") + `)
		FROM events WHERE ` + where + ` AND kind = ? GROUP BY name, model`
	rows, err := s.db.QueryContext(ctx, q, withArgs(args, EventModel)...)
	if err != nil {
		return nil, fmt.Errorf("report models: %w", err)
	}
	defer rows.Close()
	out := map[[2]string]*ModelStat{}
	for rows.Next() {
		var st ModelStat
		if err := rows.Scan(&st.Provider, &st.Model, &st.Calls, &st.EstimatedCalls, &st.Errors,
			&st.InputTokens, &st.OutputTokens, &st.CacheRead, &st.CacheWrite, &st.CostUSD); err != nil {
			return nil, fmt.Errorf("report models: %w", err)
		}
		out[[2]string{st.Provider, st.Model}] = &st
	}
	return out, rows.Err()
}

// queryToolStats groups tool and denial events into ToolStat rows (all fields
// but the percentiles and the rates). A name seen only in denial events keeps a
// zero call count, like the in-Go aggregation.
func (s *Store) queryToolStats(ctx context.Context, where string, args []any) (map[string]*ToolStat, error) {
	q := `SELECT name,
		SUM(CASE WHEN kind = 'tool' THEN 1 ELSE 0 END),
		SUM(CASE WHEN kind = 'tool' AND status = 'error' THEN 1 ELSE 0 END),
		SUM(CASE WHEN kind = 'denial' THEN 1 ELSE 0 END),
		CAST(SUM(CASE WHEN kind = 'tool' THEN ` + reportNumExpr("raw_bytes") + ` ELSE 0 END) AS INTEGER),
		CAST(SUM(CASE WHEN kind = 'tool' THEN ` + reportNumExpr("inline_bytes") + ` ELSE 0 END) AS INTEGER),
		SUM(CASE WHEN kind = 'tool' AND ` + reportBoolExpr("spilled") + ` THEN 1 ELSE 0 END)
		FROM events WHERE ` + where + ` AND kind IN (?, ?) GROUP BY name`
	rows, err := s.db.QueryContext(ctx, q, withArgs(args, EventTool, EventDenial)...)
	if err != nil {
		return nil, fmt.Errorf("report tools: %w", err)
	}
	defer rows.Close()
	out := map[string]*ToolStat{}
	for rows.Next() {
		var st ToolStat
		if err := rows.Scan(&st.Name, &st.Calls, &st.Errors, &st.Denials,
			&st.RawBytes, &st.InlineBytes, &st.Spilled); err != nil {
			return nil, fmt.Errorf("report tools: %w", err)
		}
		out[st.Name] = &st
	}
	return out, rows.Err()
}

// queryReportCounts counts the compaction and tool-search events of the report.
func (s *Store) queryReportCounts(ctx context.Context, where string, args []any) (compactions, toolSearch int, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(1) FROM events WHERE `+where+` AND kind IN (?, ?) GROUP BY kind`,
		withArgs(args, EventCompaction, EventToolSearch)...)
	if err != nil {
		return 0, 0, fmt.Errorf("report counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return 0, 0, fmt.Errorf("report counts: %w", err)
		}
		if kind == EventCompaction {
			compactions = n
		} else {
			toolSearch = n
		}
	}
	return compactions, toolSearch, rows.Err()
}

// percentileByGroup computes nearest-rank percentiles of col per
// (name, groupExpr) in one windowed pass: ROW_NUMBER orders the group's values,
// COUNT gives n, and the row at rank ceil(p/100*n) - here (p*n+99)/100 in
// integer arithmetic - carries that percentile. Same definition as Percentile.
func (s *Store) percentileByGroup(ctx context.Context, where string, args []any, kind, groupExpr, col, extra string, ps ...int) (map[[2]string]map[int]int64, error) {
	cols := make([]string, len(ps))
	for i, p := range ps {
		cols[i] = fmt.Sprintf(`MAX(CASE WHEN rn = (n * %d + 99) / 100 THEN v END)`, p)
	}
	q := `WITH ranked AS (SELECT name, ` + groupExpr + ` AS grp, ` + col + ` AS v,
		ROW_NUMBER() OVER (PARTITION BY name, ` + groupExpr + ` ORDER BY ` + col + `) AS rn,
		COUNT(1) OVER (PARTITION BY name, ` + groupExpr + `) AS n
		FROM events WHERE ` + where + ` AND kind = ?` + extra + `)
		SELECT name, grp, ` + strings.Join(cols, `, `) + ` FROM ranked GROUP BY name, grp`
	rows, err := s.db.QueryContext(ctx, q, withArgs(args, kind)...)
	if err != nil {
		return nil, fmt.Errorf("report percentiles: %w", err)
	}
	defer rows.Close()
	out := map[[2]string]map[int]int64{}
	for rows.Next() {
		var name, grp string
		vals := make([]int64, len(ps))
		dest := []any{&name, &grp}
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("report percentiles: %w", err)
		}
		byP := make(map[int]int64, len(ps))
		for i, p := range ps {
			byP[p] = vals[i]
		}
		out[[2]string{name, grp}] = byP
	}
	return out, rows.Err()
}

// addModelPercentiles fills P50Ms/P95Ms and P50TTFTMs of the model rows. TTFT is
// percentiled over the events that reported it (ttft_ms > 0 only).
func (s *Store) addModelPercentiles(ctx context.Context, where string, args []any, models map[[2]string]*ModelStat) error {
	durs, err := s.percentileByGroup(ctx, where, args, EventModel, reportModelExpr, "duration_ms", "", 50, 95)
	if err != nil {
		return err
	}
	ttfts, err := s.percentileByGroup(ctx, where, args, EventModel, reportModelExpr, "ttft_ms", " AND ttft_ms > 0", 50)
	if err != nil {
		return err
	}
	for key, st := range models {
		st.P50Ms, st.P95Ms = durs[key][50], durs[key][95]
		st.P50TTFTMs = ttfts[key][50]
	}
	return nil
}

// addToolPercentiles fills P50Ms/P95Ms of the tool rows. Denial events carry no
// duration of their own, so only executed tool events are measured.
func (s *Store) addToolPercentiles(ctx context.Context, where string, args []any, tools map[string]*ToolStat) error {
	durs, err := s.percentileByGroup(ctx, where, args, EventTool, `''`, "duration_ms", "", 50, 95)
	if err != nil {
		return err
	}
	for name, st := range tools {
		byP := durs[[2]string{name, ""}]
		st.P50Ms, st.P95Ms = byP[50], byP[95]
	}
	return nil
}

// sortModelStats turns the grouped models into the report order: most calls
// first, then provider/model (the order AggregateEvents produces).
func sortModelStats(models map[[2]string]*ModelStat) []ModelStat {
	out := make([]ModelStat, 0, len(models))
	for _, st := range models {
		st.ErrorRate = ratio(int64(st.Errors), int64(st.Calls))
		st.CacheHitRate = ratio(st.CacheRead, st.InputTokens+st.CacheRead+st.CacheWrite)
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Provider+"/"+out[i].Model < out[j].Provider+"/"+out[j].Model
	})
	return out
}

// sortToolStats turns the grouped tools into the report order: most calls
// first, then name.
func sortToolStats(tools map[string]*ToolStat) []ToolStat {
	out := make([]ToolStat, 0, len(tools))
	for _, st := range tools {
		st.ErrorRate = ratio(int64(st.Errors), int64(st.Calls))
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// AggregateEvents is the pure aggregation behind BuildReport; the SQL report must
// keep producing the same result (TestBuildReportMatchesAggregateEvents).
func AggregateEvents(events []Event) Report {
	type modelAcc struct {
		stat       ModelStat
		durs, ttft []int64
	}
	type toolAcc struct {
		stat ToolStat
		durs []int64
	}
	models := map[[2]string]*modelAcc{}
	tools := map[string]*toolAcc{}
	tool := func(name string) *toolAcc {
		if tools[name] == nil {
			tools[name] = &toolAcc{stat: ToolStat{Name: name}}
		}
		return tools[name]
	}
	var rep Report
	for _, e := range events {
		switch e.Kind {
		case EventModel:
			model, _ := e.Attrs["model"].(string)
			key := [2]string{e.Name, model}
			a := models[key]
			if a == nil {
				a = &modelAcc{stat: ModelStat{Provider: e.Name, Model: model}}
				models[key] = a
			}
			a.stat.Calls++
			if estimated, _ := e.Attrs["estimated"].(bool); estimated {
				a.stat.EstimatedCalls++
			}
			if e.Status == "error" {
				a.stat.Errors++
			}
			a.durs = append(a.durs, e.DurationMs)
			if e.TTFTMs > 0 {
				a.ttft = append(a.ttft, e.TTFTMs)
			}
			// Stored input includes cache; report it without (floored at 0).
			a.stat.InputTokens += max(e.InputTokens-e.CacheReadTokens-e.CacheWriteTokens, 0)
			a.stat.OutputTokens += e.OutputTokens
			a.stat.CacheRead += e.CacheReadTokens
			a.stat.CacheWrite += e.CacheWriteTokens
			if c, ok := e.Attrs["cost_usd"].(float64); ok {
				a.stat.CostUSD += c
			}
		case EventTool:
			a := tool(e.Name)
			a.stat.Calls++
			if e.Status == "error" {
				a.stat.Errors++
			}
			a.durs = append(a.durs, e.DurationMs)
			a.stat.RawBytes += attrInt64(e.Attrs["raw_bytes"])
			a.stat.InlineBytes += attrInt64(e.Attrs["inline_bytes"])
			if spilled, _ := e.Attrs["spilled"].(bool); spilled {
				a.stat.Spilled++
			}
		case EventDenial:
			tool(e.Name).stat.Denials++
		case EventCompaction:
			rep.Compactions++
		case EventToolSearch:
			rep.ToolSearch++
		}
	}
	rep.Models = make([]ModelStat, 0, len(models))
	for _, a := range models {
		st := a.stat
		st.ErrorRate = ratio(int64(st.Errors), int64(st.Calls))
		st.P50Ms, st.P95Ms = Percentile(a.durs, 50), Percentile(a.durs, 95)
		st.P50TTFTMs = Percentile(a.ttft, 50)
		st.CacheHitRate = ratio(st.CacheRead, st.InputTokens+st.CacheRead+st.CacheWrite)
		rep.Models = append(rep.Models, st)
	}
	sort.Slice(rep.Models, func(i, j int) bool {
		if rep.Models[i].Calls != rep.Models[j].Calls {
			return rep.Models[i].Calls > rep.Models[j].Calls
		}
		return rep.Models[i].Provider+"/"+rep.Models[i].Model < rep.Models[j].Provider+"/"+rep.Models[j].Model
	})
	rep.Tools = make([]ToolStat, 0, len(tools))
	for _, a := range tools {
		st := a.stat
		st.ErrorRate = ratio(int64(st.Errors), int64(st.Calls))
		st.P50Ms, st.P95Ms = Percentile(a.durs, 50), Percentile(a.durs, 95)
		rep.Tools = append(rep.Tools, st)
	}
	sort.Slice(rep.Tools, func(i, j int) bool {
		if rep.Tools[i].Calls != rep.Tools[j].Calls {
			return rep.Tools[i].Calls > rep.Tools[j].Calls
		}
		return rep.Tools[i].Name < rep.Tools[j].Name
	})
	return rep
}

func ratio(n, d int64) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// Percentile returns the p-th percentile (0<p<=100) of vals by the
// nearest-rank method: the value at 1-based rank ceil(p/100*n) of the sorted
// data. Empty input yields 0. vals is not modified.
func Percentile(vals []int64, p float64) int64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]int64(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = min(max(rank, 1), len(sorted))
	return sorted[rank-1]
}

// attrInt64 reads a numeric event attr (float64 after a JSON round trip).
func attrInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}
