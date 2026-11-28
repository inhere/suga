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
	"unicode/utf8"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Sub-agent types (the agent tool's type argument).
const (
	AgentExplore  = "explore"  // workspace read-only tools
	AgentResearch = "research" // explore plus web_fetch and web_search
	// AgentFork starts from this conversation's request prefix and always runs
	// as a background task (read-only; only its report comes back).
	AgentFork = "fork"
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
	// Seed is what ForkSeeder returned for a fork (a fork runs only with one);
	// the host owns its type, so it stays opaque here.
	Seed any
}

// AgentReport is the outcome of a sub-agent run.
type AgentReport struct {
	SessionID string
	Report    string
	// Stop is why it ended: final, max_steps, max_tokens, timeout, cancelled,
	// context (a fork reached the context threshold) or failed.
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

// ForkSeeder provides the request snapshot a fork starts from. It extends
// SubAgents optionally (a type assertion), so a host that does not implement
// it keeps working: the fork type then reports itself as unavailable.
type ForkSeeder interface {
	// ForkSeed returns the seed for the fork of req (the host's own type,
	// handed back in AgentRequest.Seed) or the reason the fork cannot start.
	ForkSeed(ctx context.Context, req AgentRequest) (any, error)
}

// ForkLimitError refuses a fork because the session already runs its limit of
// background tasks: a fork waits for no slot, the caller tells the user how to
// make room.
type ForkLimitError struct{ Running, Limit int }

func (e *ForkLimitError) Error() string {
	return fmt.Sprintf("agent: %d background tasks are running (limit %d); "+
		"task wait or task kill one before starting a fork", e.Running, e.Limit)
}

// agentNoticeLines are the report lines a background sub-agent's notice shows.
const agentNoticeLines = 12

// forkNoticeRunes is how long a fork's report may be and still reach the
// session whole (footer excluded): the report is the point of a fork, so it is
// shortened only past this size.
const forkNoticeRunes = 4000

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
	// What the sub-agent starts from differs by type, so the sentence says both;
	// fork is offered only together with tasks, so an explore/research-only tool
	// does not mention it.
	starts := "it sees nothing of this conversation"
	if t.tasks != nil {
		starts = "explore and research start empty, seeing nothing of this conversation, while fork carries all of it"
	}
	desc := "Hand a self-contained exploration or research task to a sub-agent: it works in a context of its own with " +
		"read-only tools and returns only its report, so reading many files does not fill this conversation. " +
		"Use it when the task means reading or searching widely (tracing callers across a codebase, comparing " +
		"implementations, surveying a dependency) and you only need the conclusion; for one or two files, read them " +
		"yourself. Give it a full prompt: what to find, where, and what the report should hold (findings with " +
		"file:line). The sub-agent cannot write files, run commands or ask the user; " + starts + ". " +
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
	types := []any{AgentExplore, AgentResearch}
	typeDesc := "explore (default): workspace read tools; research: also the web"
	props := map[string]any{
		"description": map[string]any{"type": "string", "description": "A short label for the task (3-8 words), shown to the user"},
		"prompt":      map[string]any{"type": "string", "description": "The full task for the sub-agent: what to find, where to look, what to report"},
		"type":        map[string]any{"type": "string", "enum": types, "description": typeDesc},
	}
	if t.tasks != nil {
		// A fork always runs as a task, so it is offered only with one.
		types = append(types, AgentFork)
		props["type"] = map[string]any{"type": "string", "enum": types, "description": typeDesc +
			"; fork: carries this whole conversation's context, runs in the background, read-only"}
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
	if !slices.Contains([]string{AgentExplore, AgentResearch, AgentFork}, req.Type) {
		return nil, fmt.Errorf("agent: type must be explore, research or fork, got %q", req.Type)
	}
	if req.Prompt == "" {
		return nil, errors.New("agent: prompt is required")
	}
	if req.Description == "" {
		req.Description = firstWords(req.Prompt, 6)
	}
	req.ParentSession, req.ParentTurn = t.agents.Scope(ctx)
	if req.Type == AgentFork {
		// background is ignored: a fork has no synchronous form (it would be a
		// plain sub-agent run).
		return t.executeFork(ctx, call, req), nil
	}
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
// output and, on its end, a notice with it (a fork's whole report up to its
// size limit: see agentNotice).
func (t *AgentTool) startBackground(ctx context.Context, call api.ToolCall, req AgentRequest) (*api.ToolResult, error) {
	info, err := t.startAgentTask(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("agent: background: %w", err)
	}
	return startedResult(call, req, info), nil
}

// executeFork starts a fork task for the model. A refusal (no seed, the task
// limit) is a tool error result: the model reports it and the turn goes on.
func (t *AgentTool) executeFork(ctx context.Context, call api.ToolCall, req AgentRequest) *api.ToolResult {
	info, err := t.StartFork(ctx, req)
	if err != nil {
		return forkRefusal(call, err)
	}
	return startedResult(call, req, info)
}

// StartFork starts req as a fork task and returns the task. The agent tool and
// the :bg command both start forks here, so both refuse in the same way.
func (t *AgentTool) StartFork(ctx context.Context, req AgentRequest) (tasks.Info, error) {
	req.Type, req.Background = AgentFork, true
	if t.tasks == nil {
		return tasks.Info{}, errors.New("agent: fork needs background tasks")
	}
	seeder, ok := t.agents.(ForkSeeder)
	if !ok {
		return tasks.Info{}, errors.New("agent: fork is not available")
	}
	// Refuse at the limit instead of queueing (D5). The manager refuses too, but
	// only here are the numbers known, for the message the caller shows.
	running, limit := len(t.tasks.Running(api.SessionIDFrom(ctx))), t.tasks.Limits().MaxRunning
	if limit > 0 && running >= limit {
		return tasks.Info{}, &ForkLimitError{Running: running, Limit: limit}
	}
	// The seed comes after the checks: a fork that cannot start must not pay for
	// (or be refused by) a request snapshot it never uses.
	seed, err := seeder.ForkSeed(ctx, req)
	if err != nil {
		return tasks.Info{}, err
	}
	req.Seed = seed
	return t.startAgentTask(ctx, req)
}

// startAgentTask starts the task behind both kinds of background sub-agent
// (plain and fork): the report goes to its output, its end notice to the
// session, its counters and labels to the trace. The notice differs for a fork
// (D4): its report is what the caller waited for, so it travels whole up to
// forkNoticeRunes; a plain agent keeps sending its first lines.
func (t *AgentTool) startAgentTask(ctx context.Context, req AgentRequest) (tasks.Info, error) {
	req.Background = true
	return t.tasks.Start(ctx, tasks.Spec{Kind: "agent", Summary: "agent: " + req.Description, NoEndNotice: true,
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
			if req.Type == AgentFork {
				// The kind stays "agent" (the consumers of Kind look for the
				// "agent: " summary); the label is what tells a fork apart.
				r.Label("agent_type", AgentFork)
			}
			footer := agentFooter(rep)
			if err != nil {
				if r.Killed() {
					// The task was ended on purpose (:tasks -k, chat exit): the report is
					// cancelled with it, and a failure notice would wake a turn nobody
					// asked for (its notice used to interrupt the line being typed).
					fmt.Fprintf(out, "已终止\n%s\n", footer)
					return 1, nil
				}
				fmt.Fprintf(out, "sub-agent failed: %v\n%s\n", err, footer)
				r.Notify(fmt.Sprintf("agent %q failed: %v (%s)", req.Description, err, footer))
				return 1, nil
			}
			fmt.Fprintf(out, "%s\n\n%s\n", strings.TrimSpace(rep.Report), footer)
			// No Progress: it would return a task wait before the end.
			r.Notify(agentNotice(req, rep) + "\n" + footer)
			return 0, nil
		}})
}

// agentNotice is the notice a finished background sub-agent leaves: the start
// of its report. Only a fork carries the report whole (D4), up to its size
// limit; a longer one, and every plain agent, is cut to its first lines.
func agentNotice(req AgentRequest, rep AgentReport) string {
	head, cut := firstLines(rep.Report, agentNoticeLines)
	if req.Type == AgentFork {
		if body := strings.TrimSpace(rep.Report); utf8.RuneCountInString(body) <= forkNoticeRunes {
			// Runes, not bytes: the directive allows 4000 characters of report.
			head, cut = body, false
		} else {
			cut = true // the head holds every line it can, not all the text
		}
	}
	text := fmt.Sprintf("agent %q finished (stop=%s):\n%s", req.Description, rep.Stop, head)
	if cut {
		text += "\n[report cut; task output reads it all]"
	}
	return text
}

// startedResult is the tool result telling the model the task started.
func startedResult(call api.ToolCall, req AgentRequest, info tasks.Info) *api.ToolResult {
	kind := "agent"
	if req.Type == AgentFork {
		kind = "fork"
	}
	return &api.ToolResult{
		CallID: call.CallID,
		Name:   "agent",
		Content: fmt.Sprintf("[background %s task %s started: %s]\nYou are told when it ends with the start of its report; "+
			"task wait blocks on it, task output reads the whole report, task kill stops it.", kind, info.ID, req.Description),
		Metadata: map[string]any{"task_id": info.ID, "background": true, "description": req.Description, "agent_type": req.Type},
	}
}

// forkRefusal is the tool result of a fork the host did not start: the model
// gets the reason and, when the host has an explanation for the user, its hint.
func forkRefusal(call api.ToolCall, err error) *api.ToolResult {
	text := err.Error()
	if !strings.HasPrefix(text, "agent: ") {
		text = "agent: " + text
	}
	var hint interface{ Hint() string }
	if errors.As(err, &hint) {
		if h := hint.Hint(); h != "" {
			text += "\n" + h
		}
	}
	return &api.ToolResult{CallID: call.CallID, Name: "agent", IsError: true, Content: text,
		Metadata: map[string]any{"agent_type": AgentFork}}
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
