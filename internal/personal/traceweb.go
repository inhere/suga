package personal

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

//go:embed traceweb.html
var traceWebHTML []byte

// maxWebText caps one text (message, tool argument value, tool result) sent
// to the trace page; the full text stays in the store (suga inspect export).
const maxWebText = 64 * 1024

// TraceView is what `suga inspect trace --web` serves at /api/session: the session,
// its events, its transcript entries joined to them (tool results to tool
// events by call id, assistant messages to the model call that produced
// them) and the loop analysis.
type TraceView struct {
	Session  traceSession  `json:"session"`
	Events   []webEvent    `json:"events"`
	Entries  []traceEntry  `json:"entries"`
	Analysis TraceAnalysis `json:"analysis"`
	// Delta marks a view that carries only the part the page did not have yet
	// (see TraceQuery): the page merges it into what it already shows. The
	// analysis stays the whole session's, so the header numbers keep up.
	Delta bool `json:"delta,omitempty"`
}

// TraceQuery narrows a trace view load to what a page does not have yet:
// entries after SinceSeq and events after SinceEvent. The zero value loads
// everything (the first load).
type TraceQuery struct {
	SinceSeq   int64 // entries with seq > SinceSeq
	SinceEvent int64 // events with id > SinceEvent
}

// incremental reports whether the query asks for the new part only.
func (q TraceQuery) incremental() bool { return q.SinceSeq > 0 || q.SinceEvent > 0 }

type traceSession struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Cwd       string         `json:"cwd"`
	Status    string         `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type webEvent struct {
	ID               int64          `json:"id"`
	TurnID           string         `json:"turn_id,omitempty"`
	Kind             string         `json:"kind"`
	Name             string         `json:"name"`
	Status           string         `json:"status"`
	Start            time.Time      `json:"start"`
	DurationMs       int64          `json:"duration_ms"`
	TTFTMs           int64          `json:"ttft_ms,omitempty"`
	InputTokens      int64          `json:"input_tokens,omitempty"`
	OutputTokens     int64          `json:"output_tokens,omitempty"`
	CacheReadTokens  int64          `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64          `json:"cache_write_tokens,omitempty"`
	Error            string         `json:"error,omitempty"`
	Attrs            map[string]any `json:"attrs,omitempty"`
	// Entries are the transcript entries (by seq) belonging to the event: a
	// tool call and its result for a tool event, the reply for a model call.
	Entries []int64 `json:"entries,omitempty"`
}

type traceEntry struct {
	Seq       int64          `json:"seq"`
	TurnID    string         `json:"turn_id,omitempty"`
	Type      string         `json:"type"`
	Time      time.Time      `json:"time"`
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	Reasoning string         `json:"reasoning,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	Batch     string         `json:"batch,omitempty"`
	// Notice marks a user entry that is no input of the person: a background
	// task notice (message metadata suga.task_notice, see metaTaskNotice), so
	// the page can label it as such. Steer marks the other kind of user entry a
	// running turn injected: a message a person typed into it (metadata
	// suga.steer, see metaSteer), which the page labels "插话".
	Notice bool            `json:"notice,omitempty"`
	Steer  bool            `json:"steer,omitempty"`
	Raw    json.RawMessage `json:"raw,omitempty"` // non-message entries (segment, plan, handoff, context update)
}

// BuildTraceView loads one session for the trace page.
func BuildTraceView(ctx context.Context, repo *store.Store, sessionID string) (*TraceView, error) {
	return BuildTraceViewSince(ctx, repo, sessionID, TraceQuery{})
}

// BuildTraceViewSince loads one session for the trace page, narrowed to q: a
// page keeps what it loaded first and asks for the new part on every refresh,
// so a running session is not built and sent again in full each time.
func BuildTraceViewSince(ctx context.Context, repo *store.Store, sessionID string, q TraceQuery) (*TraceView, error) {
	sess, err := repo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("trace session %q: %w", sessionID, err)
	}
	// Events are read whole even for an incremental load: the analysis shown
	// in the header is the whole session's, and an event row is small (the
	// long texts live in the entries, which are read narrowed to q).
	events, err := repo.ListEvents(ctx, store.EventFilter{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	entries, err := repo.ListEntries(ctx, sessionID, store.EntryFilter{SinceSeq: q.SinceSeq})
	if err != nil {
		return nil, err
	}
	view := &TraceView{
		// Older sessions stored the whole first prompt as the title.
		Session: traceSession{ID: sess.ID, Title: SessionTitle(sess.Title), Cwd: sess.Cwd, Status: sess.Status,
			CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt, Meta: sess.Meta},
		Analysis: AnalyzeTrace(events),
		Delta:    q.incremental(),
	}
	for _, e := range entries {
		view.Entries = append(view.Entries, webEntry(e))
	}
	// Every event of the session goes through the join, also for an
	// incremental load: the main model calls of a turn pair with its assistant
	// messages in order, so a turn cut in half must still see all of them. Only
	// then is the view narrowed to the new part.
	for _, e := range events {
		view.Events = append(view.Events, webEvent{ID: e.ID, TurnID: e.TurnID, Kind: e.Kind, Name: e.Name,
			Status: e.Status, Start: e.StartedAt, DurationMs: e.DurationMs, TTFTMs: e.TTFTMs,
			InputTokens: e.InputTokens, OutputTokens: e.OutputTokens, CacheReadTokens: e.CacheReadTokens,
			CacheWriteTokens: e.CacheWriteTokens, Error: e.Error, Attrs: e.Attrs})
	}
	if q.incremental() {
		// The join runs over every entry of the session too, but through the
		// store, so the bodies the page already has stay there.
		stored, err := repo.ListEntryRefs(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		refs := make([]entryRef, 0, len(stored))
		for _, r := range stored {
			refs = append(refs, entryRef{seq: r.Seq, turnID: r.TurnID, typ: r.Type, callID: r.CallID, at: r.CreatedAt})
		}
		joinEntriesRefs(view, refs)
		newer := make([]webEvent, 0, len(view.Events))
		for _, e := range view.Events {
			if e.ID > q.SinceEvent {
				newer = append(newer, e)
			}
		}
		view.Events = newer
		return view, nil
	}
	joinEntries(view)
	return view, nil
}

// webEntry turns a stored entry into its page form: messages parsed and
// their long texts capped, other entries passed as raw JSON.
func webEntry(e store.Entry) traceEntry {
	out := traceEntry{Seq: e.Seq, TurnID: e.TurnID, Type: e.Type, Time: e.CreatedAt}
	switch e.Type {
	case store.EntryUser, store.EntryAssistant, store.EntryTool:
		var m agentcore.Message
		if json.Unmarshal(e.JSON, &m) != nil {
			out.Raw = e.JSON
			return out
		}
		out.Role, out.Content, out.Reasoning = m.Role, capWebText(m.Content, maxWebText), capWebText(m.Reasoning, maxWebText)
		if m.ToolCall != nil {
			out.CallID, out.Tool = m.ToolCall.CallID, m.ToolCall.Name
			if m.ToolResult == nil {
				out.Args = capArgs(m.ToolCall.Arguments)
			}
		}
		if m.ToolResult != nil {
			out.CallID, out.Tool, out.IsError = m.ToolResult.CallID, m.ToolResult.Name, m.ToolResult.IsError
			out.Content = capWebText(m.ToolResult.Content, maxWebText)
		}
		out.Batch, _ = m.Metadata[agentcore.ToolBatchMetaKey].(string)
		// A background task notice is stored as a user message; mark it, so the
		// page does not present it as the person's input. A steer is marked too,
		// so the page shows it as the input it was (typed into a running turn).
		out.Notice, _ = m.Metadata[metaTaskNotice].(bool)
		out.Steer = isSteerItem(m)
	default:
		out.Raw = e.JSON
	}
	return out
}

func capArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			v = capWebText(s, maxWebText)
		}
		out[k] = v
	}
	return out
}

// capWebText cuts s to at most n bytes on a rune boundary, noting the cut.
func capWebText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n[... %d more bytes; full text: suga inspect export]", len(s)-cut)
}

// entryRef is the minimal entry data the event↔entry join needs. The join runs
// over every entry of the session, also when only the new ones are sent.
type entryRef struct {
	seq    int64
	turnID string
	typ    string
	callID string
	at     time.Time
}

// joinEntries links entries to events: a tool event gets its call and
// result (by call id), a model event the assistant message it produced.
func joinEntries(view *TraceView) {
	refs := make([]entryRef, 0, len(view.Entries))
	for _, en := range view.Entries {
		refs = append(refs, entryRef{seq: en.Seq, turnID: en.TurnID, typ: en.Type, callID: en.CallID, at: en.Time})
	}
	joinEntriesRefs(view, refs)
}

// joinEntriesRefs is joinEntries over an explicit entry index.
func joinEntriesRefs(view *TraceView, refs []entryRef) {
	byCall := map[string][]int64{}
	for _, r := range refs {
		if r.callID != "" {
			byCall[r.callID] = append(byCall[r.callID], r.seq)
		}
	}
	var models []int // indexes of main model events, in time order
	for i := range view.Events {
		e := &view.Events[i]
		switch e.Kind {
		case store.EventTool, store.EventDenial:
			if id, _ := e.Attrs["call_id"].(string); id != "" {
				e.Entries = byCall[id]
			}
		case store.EventModel:
			if p, _ := e.Attrs["purpose"].(string); p == "" {
				models = append(models, i)
			}
		}
	}
	sort.SliceStable(models, func(a, b int) bool { return view.Events[models[a]].Start.Before(view.Events[models[b]].Start) })
	byTurn := map[string][]int{}
	var turns []string
	for _, i := range models {
		id := view.Events[i].TurnID
		if _, ok := byTurn[id]; !ok {
			turns = append(turns, id)
		}
		byTurn[id] = append(byTurn[id], i)
	}
	for _, turn := range turns {
		joinTurnModels(view, refs, byTurn[turn])
	}
}

// joinTurnModels links the main model calls of one turn (in time order) to
// the turn's assistant messages. Each call that succeeded wrote one message,
// so they pair in order; times alone cannot tell them apart when a fast
// provider puts two calls and their messages in the same millisecond (as on
// Windows CI). When the counts differ (a turn cut short, older data), a call
// gets the messages written from its start until the next call's start.
func joinTurnModels(view *TraceView, refs []entryRef, models []int) {
	turn := view.Events[models[0]].TurnID
	var answers []int64
	for _, r := range refs {
		if r.typ == store.EntryAssistant && r.turnID == turn {
			answers = append(answers, r.seq)
		}
	}
	var ok []int
	for _, i := range models {
		if view.Events[i].Status == eventOK {
			ok = append(ok, i)
		}
	}
	if len(ok) == len(answers) {
		for k, i := range ok {
			view.Events[i].Entries = append(view.Events[i].Entries, answers[k])
		}
		return
	}
	for k, i := range models {
		e := &view.Events[i]
		var until time.Time
		if k+1 < len(models) {
			until = view.Events[models[k+1]].Start
		}
		for _, r := range refs {
			if r.typ != store.EntryAssistant || r.turnID != turn || r.at.Before(e.Start) {
				continue
			}
			if !until.IsZero() && !r.at.Before(until) {
				continue
			}
			e.Entries = append(e.Entries, r.seq)
		}
	}
}

// traceQueryFrom reads the incremental markers of a /api/session request:
// since_seq and since_event are the page's highest seq and event id. A missing
// or unparsable value means "send everything" (the first load).
func traceQueryFrom(r *http.Request) TraceQuery {
	q := r.URL.Query()
	var out TraceQuery
	if v, err := strconv.ParseInt(q.Get("since_seq"), 10, 64); err == nil {
		out.SinceSeq = v
	}
	if v, err := strconv.ParseInt(q.Get("since_event"), 10, 64); err == nil {
		out.SinceEvent = v
	}
	return out
}

// ServeTraceView serves the trace page for view on addr (a loopback
// address; ":0" picks a port) until ctx ends. load is called per request, so
// a reload (or the page's own polling) shows a session still running; the
// request's since_seq / since_event reach it as a TraceQuery, so a page asks
// for the new part only. env, when set, serves /api/env: this machine's
// current environment for the session (the 本机环境 tab). ready receives the
// URL once the listener is up.
func ServeTraceView(ctx context.Context, addr string, load func(context.Context, TraceQuery) (*TraceView, error),
	env func(context.Context) (any, error), ready func(url string)) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("trace --web address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("trace --web listens on a loopback address only, got %q", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(traceWebHTML)
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		// Loaded per request, so a reload shows a session still running.
		view, err := load(r.Context(), traceQueryFrom(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)
	})
	mux.HandleFunc("/api/env", func(w http.ResponseWriter, r *http.Request) {
		if env == nil {
			http.NotFound(w, r)
			return
		}
		data, err := env(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if ready != nil {
		ready("http://" + ln.Addr().String() + "/")
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
