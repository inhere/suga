package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// contextCmd is the :context command; its options follow it.
const contextCmd = ":context"

// maxContextFiles bounds the listing of :context --files.
const maxContextFiles = 100

// contextPartKeys names the resident context parts :context prints, in the
// order they appear in the request; the value is the catalog key of the name.
var contextPartKeys = map[string]string{
	"system_prompt":  "cmd.context.part.system_prompt",
	"environment":    "cmd.context.part.environment",
	"rules":          "cmd.context.part.rules",
	"memory_index":   "cmd.context.part.memory_index",
	"skills":         "cmd.context.part.skills",
	"append_prompt":  "cmd.context.part.append_prompt",
	"host_context":   "cmd.context.part.host_context",
	"deferred_tools": "cmd.context.part.deferred_tools",
}

// ContextPart is one measured part of the current context, filled from the
// runtime's resident report (the same strings the provider receives, measured
// by the same estimator the context trace event uses).
type ContextPart struct {
	Name   string
	Bytes  int
	Tokens int
	Note   string
	Files  []ContextFile
}

// ContextFile is one file of a part: the rule sources the session read.
type ContextFile struct {
	Path      string
	Bytes     int
	Truncated bool
	Included  bool
}

// ContextReport is what the runtime measures of the resident context plus the
// deferred native tools: how many are deferred and how many were loaded.
type ContextReport struct {
	Parts    []ContextPart
	Deferred int
	Loaded   int
}

// ToolsInfo is what :context says of the tool table: the schema size, how many
// tools it holds, how many are MCP ones and the deferred native ones.
type ToolsInfo struct {
	Bytes    int
	Tokens   int
	Count    int
	MCP      int
	Deferred int
	Loaded   int
}

// isContextCommand recognizes :context with its arguments.
func isContextCommand(line string) bool {
	return line == contextCmd || strings.HasPrefix(line, contextCmd+" ")
}

// BuildChatContext renders :context: one line per part with its size and
// estimated tokens, the tool table, the handoff package and the history, then
// the estimate of the whole request and the last turn's traced estimate.
func BuildChatContext(tr i18n.Translator, rep ContextReport, tools ToolsInfo, usage personal.ContextUsage) string {
	var b strings.Builder
	b.WriteString(tr.T("cmd.context.header"))
	total := tools.Tokens
	b.WriteString(tr.T("cmd.context.tools", map[string]any{
		"bytes":  formatBytes(int64(tools.Bytes)),
		"tokens": formatTokens(tools.Tokens),
		"count":  tools.Count,
	}))
	if tools.MCP > 0 {
		b.WriteString(tr.T("cmd.context.tools.mcp", map[string]any{"count": tools.MCP}))
	}
	if tools.Deferred > 0 || tools.Loaded > 0 {
		b.WriteString(tr.T("cmd.context.tools.deferred", map[string]any{"deferred": tools.Deferred, "loaded": tools.Loaded}))
	}
	b.WriteString(tr.T("cmd.context.resident"))
	empty := true
	for _, part := range rep.Parts {
		if part.Bytes <= 0 {
			continue
		}
		empty = false
		b.WriteString(tr.T("cmd.context.part", map[string]any{
			"title":  contextPartTitle(tr, part.Name),
			"bytes":  formatBytes(int64(part.Bytes)),
			"tokens": formatTokens(part.Tokens),
		}))
		total += part.Tokens
		if len(part.Files) > 0 {
			b.WriteString(" · " + tr.T("cmd.context.part.files", map[string]any{"count": len(part.Files)}))
		}
		if part.Note != "" {
			b.WriteString(tr.T("cmd.context.part.note", map[string]any{"note": part.Note}))
		}
	}
	if empty {
		b.WriteString(tr.T("cmd.context.empty"))
	}
	b.WriteString(tr.T("cmd.context.handoff", map[string]any{
		"bytes": formatBytes(int64(usage.Handoff)), "tokens": formatTokens(usage.HandoffTokens),
	}))
	b.WriteString(tr.T("cmd.context.history", map[string]any{
		"bytes": formatBytes(int64(usage.History)), "tokens": formatTokens(usage.HistoryTokens), "count": usage.HistoryMessages,
	}))
	total += usage.HandoffTokens + usage.HistoryTokens
	if usage.Input > 0 {
		b.WriteString(tr.T("cmd.context.input", map[string]any{"bytes": formatBytes(int64(usage.Input))}))
	}
	b.WriteString(tr.T("cmd.context.total", map[string]any{"tokens": formatTokens(total)}))
	if usage.EstTokens > 0 {
		b.WriteString(tr.T("cmd.context.estimated", map[string]any{"tokens": formatTokens(usage.EstTokens)}))
		if usage.Window > 0 {
			b.WriteString(" " + tr.T("cmd.context.window", map[string]any{"tokens": formatTokens(usage.Window)}))
		}
	}
	return b.String()
}

// BuildChatContextFiles renders :context --files: the files the context carries
// TOGETHER with where each came from. Tool-read files come first, most recent
// use first; the rule sources follow, since they are resident from the start.
func BuildChatContextFiles(tr i18n.Translator, rep ContextReport, files []personal.LoadedFile) string {
	var lines []string
	for _, file := range files {
		source := termtext.Clean(file.Tool)
		if file.Turn > 0 {
			source += " " + tr.T("cmd.context.file.turn", map[string]any{"turn": file.Turn})
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s", termtext.Clean(file.Path), source, formatBytes(int64(file.Bytes))))
	}
	for _, part := range rep.Parts {
		if part.Name != "rules" {
			continue
		}
		for _, file := range part.Files {
			note := tr.T("cmd.context.rule")
			if !file.Included {
				note += tr.T("cmd.context.rule.excluded")
			} else if file.Truncated {
				note += tr.T("cmd.context.rule.truncated")
			}
			lines = append(lines, fmt.Sprintf("%s · %s · %s", termtext.Clean(file.Path), note, formatBytes(int64(file.Bytes))))
		}
	}
	if len(lines) == 0 {
		return tr.T("cmd.context.files.empty")
	}
	header := tr.T("cmd.context.files.header", map[string]any{"count": len(lines)})
	return header + "\n" + strings.Join(lines, "\n")
}

// contextPartTitle is the printed name of a resident part (the part name
// itself for one this version does not know).
func contextPartTitle(tr i18n.Translator, name string) string {
	if key, ok := contextPartKeys[name]; ok {
		return tr.T(key)
	}
	return name
}

// runContextCommand runs :context: the composition of the current context, or
// --files for the files it already carries.
func (s *chatState) runContextCommand(line string) {
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, contextCmd)))
	filesOnly := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Fprintln(s.source, commandUsageText(s.tr(), contextCmd))
			return
		case "--files":
			filesOnly = true
		default:
			fmt.Fprintln(s.source, s.tr().T("cmd.unknown.arg", map[string]any{"arg": arg}))
			fmt.Fprintln(s.source, commandUsageText(s.tr(), contextCmd))
			return
		}
	}
	rep := s.contextReport()
	if filesOnly {
		fmt.Fprintln(s.source, BuildChatContextFiles(s.tr(), rep, s.loadedContextFiles()))
		return
	}
	fmt.Fprintln(s.source, BuildChatContext(s.tr(), rep, s.toolsInfo(rep), s.contextUsage()))
}

// contextReport is the runtime's resident composition, or an empty one when the
// process has no runtime (a chat started without wiring).
func (s *chatState) contextReport() ContextReport {
	if s.contextParts == nil {
		return ContextReport{}
	}
	return s.contextParts(s.ctx)
}

// toolsInfo measures the tool table the way the context trace event does (the
// JSON of ListTools) and takes the deferred native counts from rep.
func (s *chatState) toolsInfo(rep ContextReport) ToolsInfo {
	info := ToolsInfo{Deferred: rep.Deferred, Loaded: rep.Loaded}
	if s.runner == nil {
		return info
	}
	reg := s.runner.ToolRegistry()
	if reg == nil {
		return info
	}
	schemas, err := reg.ListTools(s.ctx)
	if err != nil {
		return info
	}
	raw, _ := json.Marshal(schemas)
	info.Bytes, info.Tokens, info.Count = len(raw), personal.EstimateTokens(string(raw)), len(schemas)
	if lookup, ok := reg.(tools.Lookup); ok {
		for _, schema := range schemas {
			if tool, err := lookup.Get(schema.Name); err == nil && tools.IsMCP(tool) {
				info.MCP++
			}
		}
	}
	return info
}

// contextUsage is the persisted half of the composition (handoff, history and
// the last turn's traced estimate); a read failure keeps it empty.
func (s *chatState) contextUsage() personal.ContextUsage {
	if s.runner == nil {
		return personal.ContextUsage{}
	}
	usage, err := s.runner.ContextUsage(s.ctx, s.sessionID)
	if err != nil {
		return personal.ContextUsage{}
	}
	return usage
}

// loadedContextFiles lists the files this session's file tools touched, most
// recent use first (the rule sources are added by BuildChatContextFiles).
func (s *chatState) loadedContextFiles() []personal.LoadedFile {
	if s.runner == nil {
		return nil
	}
	return s.runner.LoadedFiles(s.ctx, s.sessionID, maxContextFiles)
}
