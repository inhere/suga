package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/agentcore/redact"
)

// toolSummaryFunc renders the arguments of one tool call as one readable line,
// without the tool name. An empty result means "show the JSON instead". The
// translator is the chat's own: the few summaries that name a count in words
// say it in the person's language.
type toolSummaryFunc func(t i18n.Translator, args map[string]any, workspace string) string

// toolSummaries maps a tool to the one-line summary its call shows. A tool that
// is not listed here (an MCP tool, update_plan whose plan has a display of its
// own), a call whose arguments do not decode and a summary that comes out empty
// all keep the compact JSON of the arguments.
var toolSummaries = map[string]toolSummaryFunc{
	"file_read":       summaryFileRead,
	"file_write":      summaryFileWrite,
	"file_edit":       summaryFileEdit,
	"file_remove":     summaryFileRemove,
	"file_list":       summaryFileList,
	"file_find":       summaryFileFind,
	"file_grep":       summaryFileGrep,
	"file_symbols":    summaryFileSymbols,
	"file_refs":       summaryFileRefs,
	"view_image":      summaryPathOnly,
	"shell_exec":      summaryShell,
	"web_fetch":       summaryURL,
	"web_search":      summaryQuery,
	"agent":           summaryAgent,
	"session_message": summarySessionMessage,
	"session_search":  summaryLedger,
	"recall":          summaryRecall,
	"skill":           summarySkill,
	"tool_search":     summaryQuery,
	"memory":          summaryMemory,
}

// toolCallArgs returns what a tool line shows after "⏺ <name>": the tool's own
// summary when there is one, the compact JSON of the call arguments otherwise.
// The workspace makes the paths inside it relative, and secrets are redacted
// like everywhere else in chat.
func toolCallArgs(t i18n.Translator, name, text string, full map[string]any, workspace string) string {
	if summary, ok := toolSummaries[name]; ok {
		// The event's whole arguments when it carries them (personal.Event
		// ToolArgs); the cut JSON of Text is only decodable for short calls.
		args, ok := full, full != nil
		if !ok {
			args, ok = toolCallArgMap(text)
		}
		if ok {
			if line := truncateLine(redact.Secrets(summary(t, args, workspace)), callArgsLimit); line != "" {
				return line
			}
		}
	}
	return truncateLine(toolArgsText(text), callArgsLimit)
}

// toolCallArgMap decodes the compact JSON of a call's arguments. A long call is
// cut where the event carries it (personal toolArgSummaryLimit), so its text is
// no longer one JSON value and the JSON stays on the line.
func toolCallArgMap(text string) (map[string]any, bool) {
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber() // numbers keep their exact form, like toolArgsText
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false // more than one value: not the summary this is written for
	}
	return args, true
}

// displayToolPath shows a path of the call relative to the workspace when it is
// inside it, so the tool lines read like the paths of the prompt. A path
// outside it (a read-only dependency root, an added directory) stays absolute,
// and so does a relative one: the model already wrote it the way it wants it.
func displayToolPath(workspace, p string) string {
	p = strings.TrimSpace(p)
	if workspace == "" || p == "" || !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(workspace, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

// firstLine keeps the first line of an argument and marks that more followed:
// a shell command or a message is often written over several lines, and the
// tool line stays one line.
func firstLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return s
	}
	first, rest := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
	if rest != "" {
		return first + " …"
	}
	return first
}

// argText reads a string argument ("path", "command", ...).
func argText(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

// argList reads a list argument: an array, or the JSON text of one, which some
// callers and models send instead (file_read paths, file_grep glob).
func argList(args map[string]any, key string) []string {
	switch v := args[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return v
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		if !strings.HasPrefix(s, "[") {
			return []string{s}
		}
		var list []string
		if err := json.Unmarshal([]byte(s), &list); err != nil {
			return nil
		}
		return list
	}
	return nil
}

// argInt reads an integer argument. Decoded JSON numbers are json.Number here
// (decoder.UseNumber), so the digits survive as written; a float64 or int is
// accepted for callers that built the map themselves.
func argInt(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// argFlag reads a boolean argument.
func argFlag(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// summaryFileRead: the paths, and the line window asked for a single one
// (offset/limit), the way the tool's own schema names them.
func summaryFileRead(_ i18n.Translator, args map[string]any, workspace string) string {
	if paths := argList(args, "paths"); len(paths) > 0 {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			out = append(out, displayToolPath(workspace, p))
		}
		return strings.Join(out, ", ")
	}
	p := argText(args, "path")
	if p == "" {
		return ""
	}
	line := displayToolPath(workspace, p)
	offset, hasOffset := argInt(args, "offset")
	limit, hasLimit := argInt(args, "limit")
	if !hasOffset && !hasLimit {
		return line
	}
	start := 1
	if hasOffset && offset > 0 {
		start = offset
	}
	if hasLimit && limit > 0 {
		return fmt.Sprintf("%s:%d-%d", line, start, start+limit-1)
	}
	return fmt.Sprintf("%s:%d-", line, start)
}

// summaryFileWrite: the path and how much content is written, so a long write
// says something about its size without printing it.
func summaryFileWrite(t i18n.Translator, args map[string]any, workspace string) string {
	p := displayToolPath(workspace, argText(args, "path"))
	if p == "" {
		return ""
	}
	content, _ := args["content"].(string)
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n") + 1
	}
	return trOf(t).T("status.tool.write_size", map[string]any{"path": p, "bytes": len(content), "lines": lines})
}

// summaryFileEdit: the path and how many replacements the call applies.
func summaryFileEdit(t i18n.Translator, args map[string]any, workspace string) string {
	p := displayToolPath(workspace, argText(args, "path"))
	if p == "" {
		return ""
	}
	n := 0
	if edits, ok := args["edits"].([]any); ok {
		n = len(edits)
	}
	if n == 0 && argText(args, "old_string") != "" {
		n = 1
	}
	if n == 0 {
		return ""
	}
	return trOf(t).T("status.tool.edit_count", map[string]any{"path": p, "count": n})
}

// summaryFileRemove: the path going to the trash.
func summaryFileRemove(_ i18n.Translator, args map[string]any, workspace string) string {
	return displayToolPath(workspace, argText(args, "path"))
}

// summaryFileList: the directory and the depth when more than one level is
// listed.
func summaryFileList(_ i18n.Translator, args map[string]any, workspace string) string {
	dir := displayToolPath(workspace, argText(args, "dir"))
	if dir == "" {
		dir = "."
	}
	if depth, ok := argInt(args, "depth"); ok && depth > 1 {
		dir += fmt.Sprintf(" (depth %d)", depth)
	}
	return dir
}

// summaryFileFind: the glob and where it searches.
func summaryFileFind(_ i18n.Translator, args map[string]any, workspace string) string {
	pattern := argText(args, "pattern")
	if pattern == "" {
		return ""
	}
	if p := displayToolPath(workspace, argText(args, "path")); p != "" && p != "." {
		return pattern + " in " + p
	}
	return pattern
}

// summaryFileGrep: the pattern in quotes with the path and globs it is limited
// to, the parts a person checks before the result arrives.
func summaryFileGrep(_ i18n.Translator, args map[string]any, workspace string) string {
	pattern := argText(args, "pattern")
	if pattern == "" {
		return ""
	}
	parts := []string{strconv.Quote(pattern)}
	if p := displayToolPath(workspace, argText(args, "path")); p != "" && p != "." {
		parts = append(parts, p)
	}
	if globs := argList(args, "glob"); len(globs) > 0 {
		parts = append(parts, strings.Join(globs, " "))
	}
	return strings.Join(parts, " ")
}

// summaryFileSymbols: the name filter, else the path it lists.
func summaryFileSymbols(_ i18n.Translator, args map[string]any, workspace string) string {
	name := argText(args, "name")
	p := displayToolPath(workspace, argText(args, "path"))
	if name == "" {
		return p
	}
	if p != "" && p != "." {
		return name + " in " + p
	}
	return name
}

// summaryFileRefs: the identifier and where it is searched.
func summaryFileRefs(_ i18n.Translator, args map[string]any, workspace string) string {
	name := argText(args, "name")
	if name == "" {
		return displayToolPath(workspace, argText(args, "path"))
	}
	if p := displayToolPath(workspace, argText(args, "path")); p != "" && p != "." {
		return name + " " + p
	}
	return name
}

// summaryPathOnly: the path of a single-path read (view_image).
func summaryPathOnly(_ i18n.Translator, args map[string]any, workspace string) string {
	return displayToolPath(workspace, argText(args, "path"))
}

// summaryURL: the URL of a fetch, or the URL count of a urls fetch plus the
// first one (the whole list would not fit on the line).
func summaryURL(t i18n.Translator, args map[string]any, _ string) string {
	if urls := argList(args, "urls"); len(urls) == 1 {
		return trOf(t).T("status.tool.url_one", map[string]any{"url": urls[0]})
	} else if len(urls) > 1 {
		return trOf(t).T("status.tool.url_many", map[string]any{"count": len(urls), "url": urls[0]})
	}
	return argText(args, "url")
}

// summaryQuery: the query words of a search (web_search, tool_search).
func summaryQuery(_ i18n.Translator, args map[string]any, _ string) string {
	return argText(args, "query")
}

// summaryShell: the command itself, first line only, with the timeout or the
// background/watch mode it was given as a short note.
func summaryShell(t i18n.Translator, args map[string]any, _ string) string {
	command := firstLine(argText(args, "command"))
	if command == "" {
		return ""
	}
	notes := make([]string, 0, 3)
	if timeout, ok := argInt(args, "timeout"); ok && timeout > 0 {
		notes = append(notes, fmt.Sprintf("timeout %ds", timeout))
	}
	if argFlag(args, "background") {
		notes = append(notes, "background")
	}
	if every, ok := argInt(args, "every"); ok && every > 0 {
		notes = append(notes, fmt.Sprintf("every %ds", every))
	}
	if watches := argList(args, "watch_paths"); len(watches) > 0 {
		notes = append(notes, trOf(t).T("status.tool.watch_paths", map[string]any{"count": len(watches)}))
	}
	if len(notes) == 0 {
		return command
	}
	return command + " (" + strings.Join(notes, ", ") + ")"
}

// summaryAgent: the sub-agent type and the task label it was given (its prompt
// first line when the call carries no label).
func summaryAgent(_ i18n.Translator, args map[string]any, _ string) string {
	kind := argText(args, "type")
	if kind == "" {
		kind = "explore"
	}
	task := argText(args, "description")
	if task == "" {
		task = firstLine(argText(args, "prompt"))
	}
	line := kind
	if task != "" {
		line += " " + task
	}
	if argFlag(args, "background") {
		line += " (background)"
	}
	return line
}

// summarySessionMessage: "list", or the target and the first line of the text
// sent to it.
func summarySessionMessage(_ i18n.Translator, args map[string]any, _ string) string {
	action := argText(args, "action")
	if action == "" || action == "list" {
		return "list"
	}
	to, text := argText(args, "to"), firstLine(argText(args, "text"))
	switch {
	case to != "" && text != "":
		return fmt.Sprintf("→ %s %q", to, text)
	case to != "":
		return "→ " + to
	case text != "":
		return action + " " + strconv.Quote(text)
	}
	return action
}

// summaryLedger: the ledger query, or the ref it searches inside (session_search).
func summaryLedger(_ i18n.Translator, args map[string]any, _ string) string {
	if q := argText(args, "query"); q != "" {
		return q
	}
	return argText(args, "ref")
}

// summaryRecall: the ref whose messages or output lines it reads.
func summaryRecall(_ i18n.Translator, args map[string]any, _ string) string {
	return argText(args, "ref")
}

// summarySkill: the skill name, or the query that searches skills.
func summarySkill(_ i18n.Translator, args map[string]any, _ string) string {
	if name := argText(args, "name"); name != "" {
		if file := argText(args, "file"); file != "" {
			return name + " " + file
		}
		return name
	}
	if query := argText(args, "query"); query != "" {
		return "query " + strconv.Quote(query)
	}
	return ""
}

// summaryMemory: the action and the title, query or id it works on.
func summaryMemory(_ i18n.Translator, args map[string]any, _ string) string {
	action := argText(args, "action")
	label := argText(args, "title")
	if label == "" {
		label = argText(args, "query")
	}
	if label == "" {
		label = argText(args, "id")
	}
	switch {
	case action != "" && label != "":
		return action + " " + label
	case action != "":
		return action
	}
	return label
}
