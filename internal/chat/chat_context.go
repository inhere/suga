package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// contextCmd is the :context command; its options follow it.
const contextCmd = ":context"

// maxContextFiles bounds the listing of :context --files.
const maxContextFiles = 100

// contextPartTitles names the resident context parts :context prints, in the
// order they appear in the request.
var contextPartTitles = map[string]string{
	"system_prompt":  "系统提示",
	"environment":    "环境信息",
	"rules":          "项目规则",
	"memory_index":   "记忆索引",
	"skills":         "skills 列表",
	"append_prompt":  "附加提示",
	"host_context":   "宿主上下文",
	"deferred_tools": "按需加载工具列表",
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
func BuildChatContext(rep ContextReport, tools ToolsInfo, usage personal.ContextUsage) string {
	var b strings.Builder
	b.WriteString("上下文构成（估算 token 与 context trace 事件同一套算法）：")
	total := tools.Tokens
	fmt.Fprintf(&b, "\n工具 schema：%s · 约 %s tok · %d 个工具", formatBytes(int64(tools.Bytes)), formatTokens(tools.Tokens), tools.Count)
	if tools.MCP > 0 {
		fmt.Fprintf(&b, "（MCP %d）", tools.MCP)
	}
	if tools.Deferred > 0 || tools.Loaded > 0 {
		fmt.Fprintf(&b, "；按需加载 %d 个，已加载 %d 个", tools.Deferred, tools.Loaded)
	}
	b.WriteString("\n常驻上下文：")
	empty := true
	for _, part := range rep.Parts {
		if part.Bytes <= 0 {
			continue
		}
		empty = false
		fmt.Fprintf(&b, "\n  %s：%s · 约 %s tok", contextPartTitle(part.Name), formatBytes(int64(part.Bytes)), formatTokens(part.Tokens))
		total += part.Tokens
		if len(part.Files) > 0 {
			fmt.Fprintf(&b, " · %d 个文件", len(part.Files))
		}
		if part.Note != "" {
			fmt.Fprintf(&b, "（%s）", part.Note)
		}
	}
	if empty {
		b.WriteString("（空）")
	}
	fmt.Fprintf(&b, "\n交接包：%s · 约 %s tok", formatBytes(int64(usage.Handoff)), formatTokens(usage.HandoffTokens))
	fmt.Fprintf(&b, "\n历史消息：%s · 约 %s tok · %d 条", formatBytes(int64(usage.History)), formatTokens(usage.HistoryTokens), usage.HistoryMessages)
	total += usage.HandoffTokens + usage.HistoryTokens
	if usage.Input > 0 {
		fmt.Fprintf(&b, "\n上一回合消息：%s", formatBytes(int64(usage.Input)))
	}
	fmt.Fprintf(&b, "\n估算合计：约 %s tok", formatTokens(total))
	if usage.EstTokens > 0 {
		fmt.Fprintf(&b, "；上一回合请求实测估算 %s tok", formatTokens(usage.EstTokens))
		if usage.Window > 0 {
			fmt.Fprintf(&b, " / 窗口 %s tok", formatTokens(usage.Window))
		}
	}
	return b.String()
}

// BuildChatContextFiles renders :context --files: the files the context carries
// TOGETHER with where each came from. Tool-read files come first, most recent
// use first; the rule sources follow, since they are resident from the start.
func BuildChatContextFiles(rep ContextReport, files []personal.LoadedFile) string {
	var lines []string
	for _, file := range files {
		source := termtext.Clean(file.Tool)
		if file.Turn > 0 {
			source += fmt.Sprintf(" 第 %d 轮", file.Turn)
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s", termtext.Clean(file.Path), source, formatBytes(int64(file.Bytes))))
	}
	for _, part := range rep.Parts {
		if part.Name != "rules" {
			continue
		}
		for _, file := range part.Files {
			note := "规则文件"
			if !file.Included {
				note += "（未纳入）"
			} else if file.Truncated {
				note += "（已截断）"
			}
			lines = append(lines, fmt.Sprintf("%s · %s · %s", termtext.Clean(file.Path), note, formatBytes(int64(file.Bytes))))
		}
	}
	if len(lines) == 0 {
		return "上下文里还没有已加载的文件：file_read 读过的文件、规则文件会出现在这里"
	}
	header := fmt.Sprintf("上下文已加载的文件（%d 个，最近使用在前）：", len(lines))
	return header + "\n" + strings.Join(lines, "\n")
}

// contextPartTitle is the Chinese name of a resident part (the part name
// itself for one this version does not know).
func contextPartTitle(name string) string {
	if title, ok := contextPartTitles[name]; ok {
		return title
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
			fmt.Fprintln(s.source, commandUsageText(contextCmd))
			return
		case "--files":
			filesOnly = true
		default:
			fmt.Fprintln(s.source, "未知参数："+arg)
			fmt.Fprintln(s.source, commandUsageText(contextCmd))
			return
		}
	}
	rep := s.contextReport()
	if filesOnly {
		fmt.Fprintln(s.source, BuildChatContextFiles(rep, s.loadedContextFiles()))
		return
	}
	fmt.Fprintln(s.source, BuildChatContext(rep, s.toolsInfo(rep), s.contextUsage()))
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
