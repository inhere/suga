package personal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// The transcript is the model-readable form of a session (issue suga-49g1,
// taking the trajectory syntax of google-research/harness2): the entries the
// model saw, in their stored order, each as one labelled block, so a reviewer
// (a person or another agent) reads the run without decoding jsonl.
//
// Only one thing is shortened: a single tool result. Everything else - user
// messages, assistant text, summaries - renders whole, and the cut carries a
// marker that names the renderer as its author, so a reader cannot mistake the
// missing tail for a defect of the tool. The lower bound of the cut follows
// harness2's obs_trunc rule: a cut below 10k is "in the range that fabricates
// defects".

const (
	// DefaultTranscriptObservation is the default --max-observation: the byte
	// limit of one rendered tool result.
	DefaultTranscriptObservation = 10000
	// MinTranscriptObservation is the smallest accepted --max-observation. A
	// shorter cut makes a model diagnose the cut as a defect of the run.
	MinTranscriptObservation = 10000
)

// TranscriptOptions selects what WriteTranscript renders.
type TranscriptOptions struct {
	// SessionID names the session in the header and in every truncation marker.
	SessionID string
	// TurnID narrows the output to one turn ("" = the whole session).
	TurnID string
	// MaxObservation is the byte limit of one tool result; 0 (or a negative
	// value) means DefaultTranscriptObservation. Below MinTranscriptObservation,
	// WriteTranscript returns an error instead of cutting further.
	MaxObservation int
	// NoReasoning leaves the [reasoning] blocks out.
	NoReasoning bool
}

// WriteTranscript renders entries, in the order given (by seq), as the plain
// text transcript of a session.
func WriteTranscript(w io.Writer, entries []store.Entry, opts TranscriptOptions) error {
	if opts.MaxObservation == 0 {
		opts.MaxObservation = DefaultTranscriptObservation
	}
	if opts.MaxObservation < MinTranscriptObservation {
		return fmt.Errorf("--max-observation %d is below the %d byte minimum: a shorter cut makes a reader "+
			"diagnose the cut itself as a defect of the run", opts.MaxObservation, MinTranscriptObservation)
	}
	bw := bufio.NewWriter(w)
	t := &transcriptWriter{w: bw, opts: opts, callNames: map[string]string{}}
	t.header(entries)
	for _, e := range entries {
		t.entry(e)
	}
	if t.err != nil {
		return t.err
	}
	return bw.Flush()
}

// transcriptWriter accumulates the render state: the first write error (so the
// rendering loop stays flat) and the tool call names seen so far, which name a
// later result whose own name is missing.
type transcriptWriter struct {
	w         *bufio.Writer
	opts      TranscriptOptions
	callNames map[string]string
	err       error
	blocks    int
}

// printf writes one line, keeping the first error.
func (t *transcriptWriter) printf(format string, args ...any) {
	if t.err != nil {
		return
	}
	_, t.err = fmt.Fprintf(t.w, format, args...)
}

// header writes what the blocks are and how far the renderer may cut one of
// them: the marker below is the renderer's, not the tool's output.
func (t *transcriptWriter) header(entries []store.Entry) {
	scope := ""
	if t.opts.TurnID != "" {
		scope = " · turn " + t.opts.TurnID
	}
	t.printf("# transcript of session %s%s · %d entries\n", t.opts.SessionID, scope, len(entries))
	t.printf("# 说明：单条工具结果超过 %d 字节时由本渲染器截断，截断标记是渲染器加的，不是工具输出的一部分。\n",
		t.opts.MaxObservation)
}

// block starts a labelled block; blocks are separated by an empty line.
func (t *transcriptWriter) block(label string) {
	if t.blocks > 0 {
		t.printf("\n")
	}
	t.blocks++
	t.printf("%s\n", label)
}

// entry renders one stored entry by its type. Unknown types render their raw
// body, so a newer binary's entries are not silently dropped.
func (t *transcriptWriter) entry(e store.Entry) {
	switch e.Type {
	case store.EntryUser, store.EntryAssistant, store.EntryTool:
		t.message(e)
	case store.EntryCompaction:
		t.compaction(e)
	case store.EntryContextUpdate:
		t.contextUpdate(e)
	case store.EntryHandoff:
		t.handoff(e)
	case store.EntryPlan:
		t.plan(e)
	default:
		t.block("[" + e.Type + "]")
		t.printf("%s\n", string(e.JSON))
	}
}

// message renders a conversation message: the reasoning first (as the model
// produced it), then the text, then the tool call, all with their own labels so
// the roles never blur together.
func (t *transcriptWriter) message(e store.Entry) {
	var m api.Message
	if err := json.Unmarshal(e.JSON, &m); err != nil {
		t.block("[" + e.Type + "]")
		t.printf("%s\n", string(e.JSON))
		return
	}
	if m.ToolCall != nil && m.ToolCall.CallID != "" && m.ToolCall.Name != "" {
		t.callNames[m.ToolCall.CallID] = m.ToolCall.Name
	}
	if m.ToolResult != nil {
		t.toolResult(e.Seq, m)
		return
	}
	if m.Reasoning != "" && !t.opts.NoReasoning {
		t.block("[reasoning]")
		t.printf("%s\n", m.Reasoning)
	}
	role := m.Role
	if role == "" {
		role = e.Type
	}
	images := m.Images
	printed := m.Reasoning != "" && !t.opts.NoReasoning
	if strings.TrimSpace(m.Content) != "" || len(images) > 0 {
		t.block("[" + role + "]")
		if m.Content != "" {
			t.printf("%s\n", m.Content)
		}
		t.images(images)
		printed = true
	}
	if m.ToolCall != nil {
		t.block("[assistant → tool_call " + t.toolName(m.ToolCall.Name) + "]")
		t.printf("%s\n", argumentsJSON(m.ToolCall.Arguments, m.ToolCall.ArgumentsError))
		printed = true
	}
	if !printed {
		// A message that holds nothing but a role still tells the reader the step
		// happened (an empty assistant reply).
		t.block("[" + role + "]")
		t.printf("(empty)\n")
	}
}

// toolResult renders one observation. It is the only block the renderer may
// shorten (see truncateObservation).
func (t *transcriptWriter) toolResult(seq int64, m api.Message) {
	result := m.ToolResult
	name := result.Name
	if name == "" {
		name = t.callNames[result.CallID]
	}
	status := "ok"
	if result.IsError {
		status = "error"
	}
	t.block("[tool_result " + t.toolName(name) + " " + status + "]")
	content := result.Content
	if content == "" {
		content = m.Content
	}
	if content != "" {
		t.printf("%s\n", t.observation(seq, content))
	}
	t.images(result.Images)
}

// observation returns one observation as it is rendered: unchanged up to the
// byte limit, otherwise cut on a UTF-8 boundary and followed by a marker naming
// the renderer, the bytes left out and where the whole text is.
func (t *transcriptWriter) observation(seq int64, content string) string {
	limit := t.opts.MaxObservation
	if len(content) <= limit {
		return content
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	removed := len(content) - cut
	return content[:cut] + fmt.Sprintf("\n…[transcript renderer: 截去 %d 字节，完整内容见 suga inspect trace %s --json 或 ref %s]",
		removed, t.opts.SessionID, transcriptEntryRef(t.opts.SessionID, seq))
}

// transcriptEntryRef names one stored entry: the row of `suga inspect export
// <session>` (or of trace --json) that holds the text a marker cut off.
func transcriptEntryRef(sessionID string, seq int64) string {
	return fmt.Sprintf("entry:%s:%d", sessionID, seq)
}

// images writes the picture placeholders of a message; the bytes are never part
// of the transcript, the reference is what a reader can load.
func (t *transcriptWriter) images(images []api.ImagePart) {
	for _, img := range images {
		ref := img.Ref
		if ref == "" {
			ref = "(unknown)"
		}
		size := ""
		if img.Width > 0 && img.Height > 0 {
			size = fmt.Sprintf(" %d×%d", img.Width, img.Height)
		}
		t.printf("[image: %s%s]\n", ref, size)
	}
}

// toolName is the label of a tool in a block header ("tool" when the entry
// carries no name).
func (t *transcriptWriter) toolName(name string) string {
	if name == "" {
		return "tool"
	}
	return name
}

// argumentsJSON renders tool call arguments as one JSON object, the form the
// model sent them in (an encoding error, or the provider's decode error, is
// shown instead of the arguments).
func argumentsJSON(args map[string]any, argErr string) string {
	if argErr != "" {
		return `{"arguments_error":` + quoteJSON(argErr) + `}`
	}
	if len(args) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return `{"arguments_error":` + quoteJSON(err.Error()) + `}`
	}
	return string(raw)
}

// quoteJSON encodes s as a JSON string, or as an empty string when that fails
// (it cannot for a Go string, the fallback keeps the signature total).
func quoteJSON(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(raw)
}

// compaction renders a segment break: the summary plus the numbers that say how
// much it replaced.
func (t *transcriptWriter) compaction(e store.Entry) {
	var rec compactionRecord
	if err := json.Unmarshal(e.JSON, &rec); err != nil {
		t.block("[compaction summary]")
		t.printf("%s\n", string(e.JSON))
		return
	}
	t.block("[compaction summary]")
	t.printf("(first_kept_seq=%d, tokens %d → %d)\n", rec.FirstKeptSeq, rec.TokensBefore, rec.TokensAfter)
	if rec.Summary != "" {
		t.printf("%s\n", rec.Summary)
	}
}

// contextUpdate renders what changed inside a segment. The parts themselves
// (rules, memory index) are repeated in full in the messages that follow, so
// only their names and sizes are shown here.
func (t *transcriptWriter) contextUpdate(e store.Entry) {
	var u contextUpdateRecord
	if err := json.Unmarshal(e.JSON, &u); err != nil {
		t.block("[context update]")
		t.printf("%s\n", string(e.JSON))
		return
	}
	t.block("[context update]")
	var parts []string
	if u.Environment != nil {
		parts = append(parts, fmt.Sprintf("environment %dB", len(*u.Environment)))
	}
	if u.Rules != nil {
		parts = append(parts, fmt.Sprintf("rules %dB", len(*u.Rules)))
	}
	if u.MemoryIndex != nil {
		parts = append(parts, fmt.Sprintf("memory_index %dB", len(*u.MemoryIndex)))
	}
	if u.AppendPrompt != nil {
		parts = append(parts, fmt.Sprintf("append_prompt %dB", len(*u.AppendPrompt)))
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing")
	}
	t.printf("changed: %s\n", strings.Join(parts, ", "))
}

// handoff renders the record a session created by --from starts with.
func (t *transcriptWriter) handoff(e store.Entry) {
	var rec handoffRecord
	if err := json.Unmarshal(e.JSON, &rec); err != nil {
		t.block("[handoff]")
		t.printf("%s\n", string(e.JSON))
		return
	}
	t.block("[handoff]")
	t.printf("from %s\n", rec.Source)
	if rec.Summary != "" {
		t.printf("%s\n", rec.Summary)
	}
}

// plan renders the plan steps a turn recorded.
func (t *transcriptWriter) plan(e store.Entry) {
	var rec struct {
		Explanation string `json:"explanation"`
		Plan        []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(e.JSON, &rec); err != nil {
		t.block("[plan]")
		t.printf("%s\n", string(e.JSON))
		return
	}
	t.block("[plan]")
	for _, step := range rec.Plan {
		status := step.Status
		if status == "" {
			status = "pending"
		}
		t.printf("- [%s] %s\n", status, step.Step)
	}
}
