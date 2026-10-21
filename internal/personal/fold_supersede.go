package personal

import (
	"fmt"
	"reflect"

	"github.com/inhere/suga/pkg/agentcore"
)

// readDefaultLines is how many lines file_read returns without a limit
// (native.defaultReadLines); the fold layer uses it as the end of the window of
// a read call that passes neither offset nor limit.
const readDefaultLines = 2000

// foldReadName is the tool whose result a later modification of the same file
// makes stale: only a plain text read is replaced by a note here.
const foldReadName = "file_read"

// foldModifyTools are the tools whose call on a path changes that file. A write
// done by another tool (shell_exec, a hook) cannot be attributed to a path from
// the call arguments, so it does not mark a read result stale.
var foldModifyTools = map[string]bool{"file_edit": true, "file_write": true, "file_remove": true}

// supersedeKind tells why an old tool result is no longer worth sending.
type supersedeKind int

const (
	// supersedeReread: a later call of the same tool with the same arguments
	// read the same content again.
	supersedeReread supersedeKind = iota + 1
	// supersedeModified: a later call modified the file that was read.
	supersedeModified
)

// supersede is why one tool result of the request view was superseded: the
// kind, the 1-based step of the later call (assistant messages counted like
// foldBoundary does) and, for a modification, the path.
type supersede struct {
	kind supersedeKind
	step int
	path string
}

// note is the one-line stand-in for a superseded tool result. It points at the
// later step that made the content redundant, so the model looks there (or
// calls the tool again) instead of missing text it still needed.
func (s supersede) note() string {
	if s.kind == supersedeModified {
		return fmt.Sprintf("[已折叠：%s 在后面第 %d 步被修改，此处是修改前的内容]", s.path, s.step)
	}
	return fmt.Sprintf("[已折叠：后面第 %d 步重新读取了同一内容]", s.step)
}

// foldCall is one tool call of the request view: the step number foldBoundary
// counts for its assistant message, the index of that message and the call.
type foldCall struct {
	step int
	msg  int
	call *agentcore.ToolCall
}

// foldCalls returns the tool calls of msgs in order, each numbered by the step
// of its assistant message (an assistant message without a call counts too, so
// the numbers are the ones the fold boundary and the notes use).
func foldCalls(msgs []agentcore.Message) []foldCall {
	var out []foldCall
	step := 0
	for i, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		step++
		if m.ToolCall != nil {
			out = append(out, foldCall{step: step, msg: i, call: m.ToolCall})
		}
	}
	return out
}

// foldSuperseded maps the index of every tool message before cut that a later
// call superseded to the reason (design 2026-10-21, 0.3). It looks only at the
// request view (tool names, arguments and their order), never at files or
// storage. A result is superseded when a later call of the same tool passed the
// same arguments (for file_read: the same path and a line window covering the
// earlier one), or when a later step modified the file that was read
// (file_edit / file_write / file_remove on the same path). The first such step
// wins: after a modification a re-read returns other content, so it no longer
// stands for the folded text.
func foldSuperseded(msgs []agentcore.Message, cut int) map[int]supersede {
	calls := foldCalls(msgs)
	if len(calls) == 0 {
		return nil
	}
	out := make(map[int]supersede)
	next := 0 // the next call of calls, by message index
	for i, m := range msgs {
		if i >= cut {
			break
		}
		for next < len(calls) && calls[next].msg < i {
			next++
		}
		early := next - 1 // the call that produced this result
		if early < 0 || m.Role != "tool" || m.ToolResult == nil {
			continue
		}
		for _, later := range calls[early+1:] {
			if s, ok := supersededBy(calls[early].call, later.call, later.step); ok {
				out[i] = s
				break
			}
		}
	}
	return out
}

// supersededBy reports whether a call at the 1-based step `step` superseded the
// result of the early call, and why.
func supersededBy(early, later *agentcore.ToolCall, step int) (supersede, bool) {
	if later == nil || early == nil {
		return supersede{}, false
	}
	if foldModifyTools[later.Name] && early.Name == foldReadName {
		if path := argString(early.Arguments, "path"); path != "" && path == argString(later.Arguments, "path") {
			return supersede{kind: supersedeModified, step: step, path: path}, true
		}
	}
	if rereadOf(early, later) {
		return supersede{kind: supersedeReread, step: step}, true
	}
	return supersede{}, false
}

// rereadOf reports whether later read the same content again: the same tool
// with the same arguments, or for file_read the same path with a line window
// covering the earlier one.
func rereadOf(early, later *agentcore.ToolCall) bool {
	if early.Name != later.Name {
		return false
	}
	if early.Name == foldReadName {
		if covers, ok := readRangeCovers(early.Arguments, later.Arguments); ok {
			return covers
		}
	}
	// Any other read tool (file_grep, file_find, file_list, file_symbols,
	// file_refs) returns the same answer for the same arguments.
	return reflect.DeepEqual(early.Arguments, later.Arguments)
}

// readRangeCovers reports whether the later file_read window covers the earlier
// one: same path and outline flag, and a line range containing the earlier
// range. ok is false when a call uses `paths` (it reads several files, so the
// whole argument map is compared instead).
func readRangeCovers(early, later map[string]any) (covers, ok bool) {
	if _, multi := early["paths"]; multi {
		return false, false
	}
	if _, multi := later["paths"]; multi {
		return false, false
	}
	path := argString(early, "path")
	if path == "" || path != argString(later, "path") {
		return false, true
	}
	if boolArg(early, "outline") != boolArg(later, "outline") {
		return false, true
	}
	eStart, eEnd := readRange(early)
	lStart, lEnd := readRange(later)
	return lStart <= eStart && lEnd >= eEnd, true
}

// readRange returns the inclusive line window of a file_read call, with the
// tool's defaults (1-based start, 2000 lines).
func readRange(args map[string]any) (start, end int) {
	start = 1
	if n, ok := intArg(args["offset"]); ok && n >= 1 {
		start = n
	}
	limit := readDefaultLines
	if n, ok := intArg(args["limit"]); ok && n >= 1 {
		limit = n
	}
	return start, start + limit - 1
}

// intArg reads an integer tool argument; arguments decoded from JSON carry
// numbers as float64.
func intArg(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

// argString reads a string tool argument.
func argString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

// boolArg reads a boolean tool argument.
func boolArg(args map[string]any, key string) bool {
	b, _ := args[key].(bool)
	return b
}
