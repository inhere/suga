package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Sub-agent types (the agent tool's type argument).
const (
	AgentExplore  = "explore"  // workspace read-only tools
	AgentResearch = "research" // explore plus web_fetch and web_search
)

// AgentRequest is one sub-agent run.
type AgentRequest struct {
	Type, Description, Prompt string
	// ParentSession, ParentTurn and CallID name the turn's tool call that
	// started it (SubAgents.Scope fills the first two at the call).
	ParentSession, ParentTurn, CallID string
	// Background: it runs as a task; the host marks the parent session
	// tainted itself when the sub-agent read external content (the report
	// reaches the model through a notice or the task tool, not this result).
	Background bool
}

// AgentReport is the outcome of a sub-agent run.
type AgentReport struct {
	SessionID string
	Report    string
	// Stop is why it ended: final, max_steps, max_tokens, timeout, cancelled
	// or failed.
	Stop      string
	Steps     int
	ToolCalls int
	Usage     api.Usage
	Duration  time.Duration
	// Tainted: the sub-agent read external content.
	Tainted bool
}

// SubAgents runs sub-agents for the agent tool; the host implements it.
type SubAgents interface {
	// Scope returns the session and turn of the tool call ctx belongs to.
	Scope(ctx context.Context) (session, turn string)
	// RunAgent runs req and returns its report; an error comes with the
	// report so far (its SessionID set when the session was created).
	RunAgent(ctx context.Context, req AgentRequest) (AgentReport, error)
}

// agentNoticeLines are the report lines a background sub-agent's notice shows.
const agentNoticeLines = 12

// AgentTool hands a task to a sub-agent: a model run in a session of its own
// with read-only tools; only its final report comes back, so exploring many
// files does not fill this conversation.
type AgentTool struct {
	agents SubAgents
	// tasks runs background: true sub-agents; nil hides the option.
	tasks *tasks.Manager
}

// NewAgentTool returns the agent tool over agents.
func NewAgentTool(agents SubAgents) *AgentTool { return &AgentTool{agents: agents} }

// SetTasks enables background: true (the host owns m).
func (t *AgentTool) SetTasks(m *tasks.Manager) { t.tasks = m }

func (t *AgentTool) Name() string { return "agent" }

// Description skips the type restatement: the type parameter's own description
// names both values and what each gets (docs/research/
// 2026-10-23-fixed-prefix-tools-report.md 三 #21).
func (t *AgentTool) Description() string {
	desc := "Hand a self-contained exploration or research task to a sub-agent: it works in a context of its own with " +
		"read-only tools and returns only its report, so reading many files does not fill this conversation. " +
		"Use it when the task means reading or searching widely (tracing callers across a codebase, comparing " +
		"implementations, surveying a dependency) and you only need the conclusion; for one or two files, read them " +
		"yourself. Give it a full prompt: what to find, where, and what the report should hold (findings with " +
		"file:line). The sub-agent cannot write files, run commands or ask the user; it sees nothing of this conversation. " +
		"Its report is the result: do not repeat its searches to check it; read only what you then need to change."
	if t.tasks != nil {
		desc += " With background: true it runs as a background task and returns a task id at once; you are told " +
			"when it ends (task wait / task output read its report): start several to explore in parallel."
	}
	return desc
}

// ToolMeta: it only reads (through its sub-agent), so it needs no approval
// and is available in read-only sessions.
func (*AgentTool) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryOther, ReadOnly: true}
}

func (t *AgentTool) Schema() api.ToolSchema {
	props := map[string]any{
		"description": map[string]any{"type": "string", "description": "A short label for the task (3-8 words), shown to the user"},
		"prompt":      map[string]any{"type": "string", "description": "The full task for the sub-agent: what to find, where to look, what to report"},
		"type":        map[string]any{"type": "string", "enum": []any{AgentExplore, AgentResearch}, "description": "explore (default): workspace read tools; research: also the web"},
	}
	if t.tasks != nil {
		props["background"] = map[string]any{"type": "boolean", "description": "Run as a background task: return a task id at once, report when it ends"}
	}
	return api.ToolSchema{Name: t.Name(), Description: t.Description(), InputSchema: map[string]any{
		"type": "object", "properties": props, "required": []any{"description", "prompt"},
	}}
}

func (t *AgentTool) Execute(ctx context.Context, call api.ToolCall) (*api.ToolResult, error) {
	args := call.Arguments
	req := AgentRequest{CallID: call.CallID}
	req.Description, _ = args["description"].(string)
	req.Prompt, _ = args["prompt"].(string)
	req.Type, _ = args["type"].(string)
	req.Description, req.Prompt, req.Type = strings.TrimSpace(req.Description), strings.TrimSpace(req.Prompt), strings.TrimSpace(req.Type)
	if req.Type == "" {
		req.Type = AgentExplore
	}
	if !slices.Contains([]string{AgentExplore, AgentResearch}, req.Type) {
		return nil, fmt.Errorf("agent: type must be explore or research, got %q", req.Type)
	}
	if req.Prompt == "" {
		return nil, errors.New("agent: prompt is required")
	}
	if req.Description == "" {
		req.Description = firstWords(req.Prompt, 6)
	}
	req.ParentSession, req.ParentTurn = t.agents.Scope(ctx)
	if bg, _ := args["background"].(bool); bg && t.tasks != nil {
		return t.startBackground(ctx, call, req)
	}
	rep, err := t.agents.RunAgent(ctx, req)
	if err != nil && ctx.Err() != nil {
		return nil, err // the turn was cancelled: it ends with it
	}
	return agentResult(call, req, rep, err), nil
}

// agentResult is the tool result of a sub-agent run. A failure is a tool
// error (not a Go error) so the host still sees the metadata, the taint mark
// included.
func agentResult(call api.ToolCall, req AgentRequest, rep AgentReport, err error) *api.ToolResult {
	meta := map[string]any{"child_session_id": rep.SessionID, "agent_type": req.Type, "description": req.Description,
		"steps": rep.Steps, "tool_calls": rep.ToolCalls, "duration_ms": rep.Duration.Milliseconds(), "background": req.Background,
		"input_tokens": rep.Usage.InputTokens, "cache_read_tokens": rep.Usage.CacheReadTokens,
		"output_tokens": rep.Usage.OutputTokens, "cost_usd": rep.Usage.CostUSD, "stop": rep.Stop}
	if rep.Tainted {
		meta["external"] = true // the report carries what the sub-agent read
	}
	res := &api.ToolResult{CallID: call.CallID, Name: "agent", Metadata: meta}
	if err != nil {
		res.IsError = true
		res.Content = fmt.Sprintf("sub-agent failed: %v\n%s", err, agentFooter(rep))
		return res
	}
	res.Content = strings.TrimSpace(rep.Report) + "\n\n" + agentFooter(rep)
	return res
}

// agentFooter is the closing line of a report: where it ran and what it cost.
func agentFooter(rep AgentReport) string {
	return fmt.Sprintf("[sub-agent %s · %d steps · in %s (cache %s) / out %s · %s · stop=%s]",
		orNone(rep.SessionID), rep.Steps, kTokens(rep.Usage.InputTokens), kTokens(rep.Usage.CacheReadTokens),
		kTokens(rep.Usage.OutputTokens), rep.Duration.Round(time.Second), orNone(rep.Stop))
}

// startBackground runs req as an agent task: the report goes to the task's
// output and, on its end, a notice with its first lines.
func (t *AgentTool) startBackground(ctx context.Context, call api.ToolCall, req AgentRequest) (*api.ToolResult, error) {
	req.Background = true
	info, err := t.tasks.Start(ctx, tasks.Spec{Kind: "agent", Summary: "agent: " + req.Description, NoEndNotice: true,
		Run: func(ctx context.Context, out io.Writer, r tasks.Reporter) (int, error) {
			rep, err := t.agents.RunAgent(ctx, req)
			// What the sub-agent spent and where it ran go to the task's end
			// event (trace, --analyze), as on a synchronous call's tool event.
			r.Count("steps", rep.Steps)
			r.Count("tool_calls", rep.ToolCalls)
			r.Count("input_tokens", rep.Usage.InputTokens)
			r.Count("cache_read_tokens", rep.Usage.CacheReadTokens)
			r.Count("output_tokens", rep.Usage.OutputTokens)
			r.Label("child_session_id", rep.SessionID)
			r.Label("stop", rep.Stop)
			r.Label("cost_usd", strconv.FormatFloat(rep.Usage.CostUSD, 'f', -1, 64))
			footer := agentFooter(rep)
			if err != nil {
				fmt.Fprintf(out, "sub-agent failed: %v\n%s\n", err, footer)
				r.Notify(fmt.Sprintf("agent %q failed: %v (%s)", req.Description, err, footer))
				return 1, nil
			}
			fmt.Fprintf(out, "%s\n\n%s\n", strings.TrimSpace(rep.Report), footer)
			head, cut := firstLines(rep.Report, agentNoticeLines)
			text := fmt.Sprintf("agent %q finished (stop=%s):\n%s", req.Description, rep.Stop, head)
			if cut {
				text += "\n[report cut; task output reads it all]"
			}
			// No Progress: it would return a task wait before the end.
			r.Notify(text + "\n" + footer)
			return 0, nil
		}})
	if err != nil {
		return nil, fmt.Errorf("agent: background: %w", err)
	}
	return &api.ToolResult{
		CallID: call.CallID,
		Name:   "agent",
		Content: fmt.Sprintf("[background agent task %s started: %s]\nYou are told when it ends with the start of its report; "+
			"task wait blocks on it, task output reads the whole report, task kill stops it.", info.ID, req.Description),
		Metadata: map[string]any{"task_id": info.ID, "background": true, "description": req.Description, "agent_type": req.Type},
	}, nil
}

// firstWords is up to n words of s, as a default description.
func firstWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}

// firstLines is the first n lines of s and whether more followed.
func firstLines(s string, n int) (string, bool) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n"), false
	}
	return strings.Join(lines[:n], "\n"), true
}

// kTokens renders a token count as 85k / 1.2M / 640.
func kTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprint(n)
	}
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
