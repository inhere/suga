package native

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

const (
	// defaultTaskLines / maxTaskLines bound one task output read.
	defaultTaskLines = 100
	maxTaskLines     = 1000
	// defaultTaskWait is the wait timeout when the call gives none.
	defaultTaskWait = 30 * time.Second
	// taskTailLines are shown with a wait or kill result.
	taskTailLines = 20
)

// TaskTool manages the session's background tasks (started by shell_exec or
// agent with background: true): list, read output, wait, kill.
type TaskTool struct {
	m       *tasks.Manager
	maxWait time.Duration
}

// NewTaskTool returns the task tool over m; maxWait caps a wait timeout
// (0 = DefaultShellTimeoutMax).
func NewTaskTool(m *tasks.Manager, maxWait time.Duration) *TaskTool {
	if maxWait <= 0 {
		maxWait = DefaultShellTimeoutMax
	}
	return &TaskTool{m: m, maxWait: maxWait}
}

func (t *TaskTool) Name() string { return "task" }

func (t *TaskTool) Description() string {
	return "Manage the background tasks of this session (started with background: true by shell_exec, a command or a watch, " +
		"or by agent, a sub-agent whose output is its report). " +
		"action list: every task and its state. output: lines of a task's output (default the last 100; " +
		"offset/limit page from a line, grep filters by RE2). wait: block until the task ends (a watch: until its " +
		"next run is done) or timeout seconds pass, then its state and last lines. kill: stop the task and its child processes."
}

// ToolMeta: it only acts on tasks this session started, so it is agent
// state (no approval), like update_plan.
func (*TaskTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryOther, AgentState: true}
}

func (t *TaskTool) Schema() api.ToolSchema {
	return api.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":  map[string]any{"type": "string", "enum": []any{"list", "output", "wait", "kill"}},
				"id":      map[string]any{"type": "string", "description": "Task id, e.g. t1 (output, wait, kill)"},
				"offset":  map[string]any{"type": "integer", "description": "output: first line, 1-based (default: the last lines)"},
				"limit":   map[string]any{"type": "integer", "description": fmt.Sprintf("output: lines to return (default %d, max %d)", defaultTaskLines, maxTaskLines)},
				"grep":    map[string]any{"type": "string", "description": "output: only lines matching this RE2 pattern"},
				"timeout": map[string]any{"type": "integer", "description": fmt.Sprintf("wait: seconds (default %d, max %d)", int(defaultTaskWait.Seconds()), int(t.maxWait.Seconds()))},
			},
			"required": []any{"action"},
		},
	}
}

func (t *TaskTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	sid := t.m.SessionOf(ctx)
	if sid == "" {
		return nil, fmt.Errorf("task: no session")
	}
	args := call.Arguments
	action, _ := args["action"].(string)
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	var text string
	var err error
	switch action {
	case "list":
		text = t.list(sid)
	case "output":
		text, err = t.output(sid, id, args)
	case "wait":
		text, err = t.wait(ctx, sid, id, args)
	case "kill":
		var info tasks.Info
		if info, err = t.m.Kill(sid, id); err == nil {
			text = tasks.Describe(info) + withTail(info)
		}
	default:
		err = fmt.Errorf("action must be list, output, wait or kill, got %q", action)
	}
	if err != nil {
		return nil, fmt.Errorf("task: %w", err)
	}
	return &api.ToolResult{CallID: call.CallID, Name: t.Name(), Content: text}, nil
}

func (t *TaskTool) list(sid string) string {
	all := t.m.List(sid)
	if len(all) == 0 {
		return "no background tasks in this session"
	}
	lines := make([]string, len(all))
	for i, info := range all {
		lines[i] = tasks.Describe(info)
	}
	return strings.Join(lines, "\n")
}

func (t *TaskTool) output(sid, id string, args map[string]any) (string, error) {
	info, err := t.m.Get(sid, id)
	if err != nil {
		return "", err
	}
	limit := defaultTaskLines
	if n, ok := numberArg(args["limit"]); ok && n > 0 {
		limit = min(int(n), maxTaskLines)
	}
	head := tasks.Describe(info)
	if pattern, _ := args["grep"].(string); pattern != "" {
		hits, matched, err := tasks.GrepLines(info.Path, pattern, limit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s\n[%d lines match %q; showing %d]\n%s", head, matched, pattern, len(hits), strings.Join(hits, "\n")), nil
	}
	offset := 0
	if n, ok := numberArg(args["offset"]); ok && n > 0 {
		offset = int(n)
	}
	if offset == 0 { // the last lines
		_, total, _, err := tasks.ReadLines(info.Path, 1, 0)
		if err != nil {
			return "", err
		}
		offset = max(1, total-limit+1)
	}
	lines, total, more, err := tasks.ReadLines(info.Path, offset, limit)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return fmt.Sprintf("%s\n[no lines from %d; %d in all]", head, offset, total), nil
	}
	out := fmt.Sprintf("%s\n[lines %d-%d of %d]\n%s", head, offset, offset+len(lines)-1, total, strings.Join(lines, "\n"))
	if more {
		out += fmt.Sprintf("\n[more: offset %d]", offset+len(lines))
	}
	return out, nil
}

func (t *TaskTool) wait(ctx context.Context, sid, id string, args map[string]any) (string, error) {
	timeout := defaultTaskWait
	if n, ok := numberArg(args["timeout"]); ok && n > 0 {
		if d, valid := api.DurationFromSeconds(n, t.maxWait); valid {
			timeout = d
		}
	}
	info, how, err := t.m.Wait(ctx, sid, id, timeout)
	if err != nil {
		return "", err
	}
	text := tasks.Describe(info) + withTail(info)
	switch how {
	case tasks.WaitEnded:
		if info.Notice != "" { // what it reported last (an agent task's report)
			text = info.Notice + "\n" + tasks.Describe(info)
		}
	case tasks.WaitTimedOut:
		text = fmt.Sprintf("[still running after waiting %s]\n", timeout) + text
	case tasks.WaitUpdated: // a watch finished a run; it keeps running
		head := "[reported while running]\n"
		if info.Notice != "" {
			head = "[reported while running: " + info.Notice + "]\n"
		}
		text = head + text
	}
	return text, nil
}

// withTail appends the task's last lines, if any.
func withTail(info tasks.Info) string {
	if len(info.Tail) == 0 {
		return ""
	}
	return "\n[last lines]\n" + tasks.TailText(info, taskTailLines)
}
