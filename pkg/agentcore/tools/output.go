package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/inhere/suga/pkg/agentcore/api"
)

const (
	// DefaultInlineOutputBytes is the default inline threshold: a tool result
	// larger than this is not kept whole in the session (tools.max_output_bytes).
	DefaultInlineOutputBytes = 8000
	// DefaultRawOutputBytes is the default cap on the raw output one tool call
	// may produce before the output layer sees it (tools.max_spill_bytes).
	DefaultRawOutputBytes = 1 << 20
	// outputSnippetBytes is how much of the head and of the tail an
	// externalised result keeps inline.
	outputSnippetBytes = 2000
	// OutputMetaKey is the ToolResult.Metadata key the output layer writes its
	// OutputInfo under (a nested map, so tool-defined keys are never touched).
	OutputMetaKey = "output"
)

// Spiller stores the full text of an oversized tool result outside the
// session history and returns a ref the model can use to read it back.
// Implementations take the session/turn from ctx; an error makes the output
// layer fall back to plain head/tail truncation.
type Spiller interface {
	Spill(ctx context.Context, call api.ToolCall, content string) (ref string, err error)
}

// OutputConfig configures NewOutput.
type OutputConfig struct {
	// InlineBytes is the largest result kept whole (<=0 = DefaultInlineOutputBytes).
	InlineBytes int
	// Spiller stores oversized results; nil means they are only truncated.
	Spiller Spiller
	// Sanitize, when set, cleans an oversized result (e.g. redacts secrets)
	// before its OutputInfo is computed, it is spilled and the snippet is cut,
	// so the recorded hash matches the stored file.
	Sanitize func(string) string
	// Minimize, when set, may return a tidied version of a successful result
	// (test summaries, collapsed repeats, no colour codes). The original is
	// then spilled and the result carries the tidy text plus its ref, so
	// nothing is lost; ok=false leaves the result alone.
	Minimize func(call api.ToolCall, content string) (tidy string, ok bool)
}

// OutputInfo describes the raw output behind a shortened result. Bytes, Lines
// and SHA256 are computed over the raw text after OutputConfig.Sanitize, i.e.
// exactly what the Spiller stored.
type OutputInfo struct {
	Ref    string // empty when the output was only truncated
	Bytes  int
	Lines  int
	SHA256 string
	// Minimized marks a result replaced by OutputConfig.Minimize's tidy text.
	Minimized bool
}

// Output is the output-governance layer of the tool registry stack (order:
// observe -> output -> visibility -> approval -> base). Results up to
// InlineBytes pass unchanged; larger ones are replaced by a ref line plus a
// head/tail snippet, with the full text handed to the Spiller. Tools that
// paginate on their own (Meta.Rereadable) pass through untouched, and errors
// are never rewritten, so denial accounting in outer layers is unaffected.
type Output struct {
	inner api.ToolRegistry
	cfg   OutputConfig
}

var _ api.ToolRegistry = (*Output)(nil)
var _ Lookup = (*Output)(nil)

// NewOutput wraps inner with output governance.
func NewOutput(inner api.ToolRegistry, cfg OutputConfig) *Output {
	return &Output{inner: inner, cfg: cfg}
}

func (o *Output) ListTools(ctx context.Context) ([]api.ToolSchema, error) {
	return o.inner.ListTools(ctx)
}

// ExpandCall forwards the proxy-call expansion (tool_call) of the wrapped layer,
// so the runtime still sees the real tool call.
func (o *Output) ExpandCall(ctx context.Context, call api.ToolCall) (api.ToolCall, bool) {
	if e, ok := o.inner.(CallExpander); ok {
		return e.ExpandCall(ctx, call)
	}
	return api.ToolCall{}, false
}

// Get passes tool lookup through to the wrapped layers.
func (o *Output) Get(name string) (api.Tool, error) {
	if l, ok := o.inner.(Lookup); ok {
		return l.Get(name)
	}
	return nil, errors.New("tool lookup is not supported by the wrapped registry")
}

func (o *Output) inlineBytes() int {
	if o.cfg.InlineBytes > 0 {
		return o.cfg.InlineBytes
	}
	return DefaultInlineOutputBytes
}

// rereadable reports whether the named tool declares Meta.Rereadable. Unknown
// tools (lookup failure) are treated as not rereadable.
func (o *Output) rereadable(name string) bool {
	t, err := o.Get(name)
	return err == nil && MetaOf(t).Rereadable
}

// ExecuteTool runs the wrapped stack and then notes a call the person approved:
// the note is added here, above output governance, so a minimizer or a
// truncation cannot drop it from the result the model reads.
func (o *Output) ExecuteTool(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	res, err := o.execute(ctx, call)
	if res != nil {
		annotateApproved(ctx, res)
	}
	return res, err
}

// execute is the output layer proper (minimize, spill, truncate); ExecuteTool
// wraps it with the human-approved note.
func (o *Output) execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	res, err := o.inner.ExecuteTool(ctx, call)
	if err != nil || res == nil {
		return res, err // errors (ErrDenied, ErrAborted ...) pass through as is
	}
	if o.cfg.Minimize != nil && !res.IsError && !o.rereadable(call.Name) {
		if tidy, ok := o.cfg.Minimize(call, res.Content); ok && len(tidy) < len(res.Content) {
			res = o.minimized(ctx, call, res, tidy)
		}
	}
	limit := o.inlineBytes()
	if len(res.Content) <= limit || o.rereadable(call.Name) {
		return res, nil
	}
	raw := res.Content
	if o.cfg.Sanitize != nil {
		raw = o.cfg.Sanitize(raw)
	}
	info := OutputInfo{Bytes: len(raw), Lines: countLines(raw), SHA256: sha256Hex(raw)}
	out := *res
	// An error result is never externalised: it is only cut to the threshold.
	if res.IsError || o.cfg.Spiller == nil {
		out.Content = TruncateHeadTail(raw, limit)
		out.Metadata = withOutputInfo(res.Metadata, info)
		return &out, nil
	}
	snippet := min(outputSnippetBytes, limit/4)
	ref, spillErr := o.cfg.Spiller.Spill(ctx, call, raw)
	var header string
	if spillErr != nil {
		slog.Warn("spill tool output", "tool", call.Name, "call_id", call.CallID, "err", spillErr)
		header = fmt.Sprintf("[输出 %d 字节 / %d 行，未能保存原文，只保留头尾片段]", info.Bytes, info.Lines)
	} else {
		info.Ref = ref
		// The ref line comes first so that any later head/tail trimming of
		// the context (which keeps the head) still shows it.
		// Search first: paging a long output with recall costs one model
		// call per window (seen in eval: 38 recalls over 4000 lines).
		header = fmt.Sprintf("[输出 %d 字节 / %d 行已保存为 ref %s；先用 session_search(query, ref) 定位相关行，再用 recall(ref, offset, limit) 读附近窗口，不要逐页读完]",
			info.Bytes, info.Lines, ref)
	}
	out.Content = header + "\n" + headTailSnippet(raw, snippet)
	out.Metadata = withOutputInfo(res.Metadata, info)
	return &out, nil
}

// minimized replaces res with the tidy text, spilling the original (when a
// Spiller is set) so it can still be read with recall.
func (o *Output) minimized(ctx context.Context, call api.ToolCall, res *api.ToolResult, tidy string) *api.ToolResult {
	raw := res.Content
	if o.cfg.Sanitize != nil {
		raw = o.cfg.Sanitize(raw)
	}
	info := OutputInfo{Bytes: len(raw), Lines: countLines(raw), SHA256: sha256Hex(raw), Minimized: true}
	note := fmt.Sprintf("[已整理输出：原文 %d 字节 / %d 行", info.Bytes, info.Lines)
	if o.cfg.Spiller != nil {
		if ref, err := o.cfg.Spiller.Spill(ctx, call, raw); err == nil {
			info.Ref = ref
			note += "已保存为 ref " + ref + "，需要细节时用 session_search(query, ref) 或 recall(ref, offset, limit)"
		} else {
			slog.Warn("spill tool output", "tool", call.Name, "call_id", call.CallID, "err", err)
		}
	}
	note += "]"
	// The original is kept under the ref: a tidy text that still does not
	// fit is cut here rather than spilled a second time.
	// truncateMarkerBytes leaves room for TruncateHeadTail's own marker line.
	const truncateMarkerBytes = 96
	if budget := o.inlineBytes() - len(note) - 1; len(tidy) > budget {
		tidy = TruncateHeadTail(tidy, max(budget-truncateMarkerBytes, 0))
	}
	out := *res
	out.Content = tidy + "\n" + note
	out.Metadata = withOutputInfo(res.Metadata, info)
	return &out
}

// OutputInfoOf reads the OutputInfo the output layer recorded in md. It
// accepts both in-memory values (int) and JSON round-tripped ones (float64).
func OutputInfoOf(md map[string]any) (OutputInfo, bool) {
	m, ok := md[OutputMetaKey].(map[string]any)
	if !ok {
		return OutputInfo{}, false
	}
	info := OutputInfo{}
	info.Ref, _ = m["ref"].(string)
	info.SHA256, _ = m["sha256"].(string)
	info.Bytes = anyInt(m["bytes"])
	info.Lines = anyInt(m["lines"])
	info.Minimized, _ = m["minimized"].(bool)
	return info, true
}

func anyInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// withOutputInfo returns a copy of md with info added under OutputMetaKey;
// every other key the tool set is kept as is.
func withOutputInfo(md map[string]any, info OutputInfo) map[string]any {
	out := make(map[string]any, len(md)+1)
	for k, v := range md {
		out[k] = v
	}
	m := map[string]any{"bytes": info.Bytes, "lines": info.Lines, "sha256": info.SHA256}
	if info.Ref != "" {
		m["ref"] = info.Ref
	}
	if info.Minimized {
		m["minimized"] = true
	}
	out[OutputMetaKey] = m
	return out
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// countLines counts lines the way a line-window reader sees them: a trailing
// newline does not start another line.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// headTailSnippet keeps n bytes of the head and of the tail of s (UTF-8 safe)
// with an omission marker in between.
func headTailSnippet(s string, n int) string {
	if len(s) <= 2*n {
		return s
	}
	headLen := utf8Cut(s, n)
	tailStart := utf8CutFrom(s, len(s)-n)
	return fmt.Sprintf("%s\n…[省略 %d 字节]…\n%s", s[:headLen], tailStart-headLen, s[tailStart:])
}

// TruncateHeadTail keeps the head and tail of s within limit bytes (UTF-8
// safe) and notes how many bytes were omitted in between. The tail usually
// holds errors and exit status, so both ends are kept. limit <= 0 returns s.
func TruncateHeadTail(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	headLen := utf8Cut(s, limit/2)
	tailStart := utf8CutFrom(s, len(s)-(limit-headLen))
	omitted := tailStart - headLen
	return fmt.Sprintf("%s\n…[省略 %d 字节，输出上限 %d 字节]…\n%s", s[:headLen], omitted, limit, s[tailStart:])
}

// utf8Cut moves cut back so that s[:cut] does not split a multi-byte rune.
func utf8Cut(s string, cut int) int {
	if cut >= len(s) {
		return len(s)
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

// utf8CutFrom moves start forward so that s[start:] begins on a rune start.
func utf8CutFrom(s string, start int) int {
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return start
}
