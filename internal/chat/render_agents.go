package chat

// Sub-agent progress on the renderer: the status label, the running list and
// the begin/finish/summary lines (moved out of render.go unchanged).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// agentProgressMode keeps direct renderer tests and callers without config on the
// documented default.
func (r *Renderer) agentProgressMode() string {
	switch r.opts.AgentProgress {
	case "full", "off", "summary":
		return r.opts.AgentProgress
	default:
		return "summary"
	}
}

func (r *Renderer) agentStatusLabel() string {
	if r.agentProgressMode() != "summary" {
		return r.tr().T("approval.render.spinner")
	}
	parts := r.runningAgents()
	if len(parts) == 0 {
		return r.tr().T("approval.render.spinner")
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		texts = append(texts, r.tr().T("approval.render.agent.calls", map[string]any{
			"description": truncateLine(part.description, 24), "calls": part.calls,
		}))
	}
	return truncateLine(r.tr().T("approval.render.agent.running", map[string]any{
		"count": len(parts), "details": strings.Join(texts, r.tr().T("approval.render.agent.separator")),
	}), 120)
}

// agentStatusPart is one running agent the status line counts.
type agentStatusPart struct {
	id          string
	description string
	calls       int
}

// runningAgents is what the status line counts as running, in display order: the
// foreground entries of this turn and the manager's running agent tasks. The
// tasks manager is the truth for a background agent - its task leaves the
// running set when it ends, fails or is killed, so the count never depends on a
// notice being delivered - and an entry whose task is gone is dropped here.
func (r *Renderer) runningAgents() []agentStatusPart {
	tasksByID := make(map[string]tasks.Info)
	for _, info := range r.agentTasks() {
		if info.Kind == "agent" && info.Status == tasks.Running {
			tasksByID[info.ID] = info
		}
	}
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	parts := make([]agentStatusPart, 0, len(r.agentProgress)+len(tasksByID))
	for id, state := range r.agentProgress {
		if state == nil {
			delete(r.agentProgress, id)
			continue
		}
		if state.taskID == "" {
			parts = append(parts, agentStatusPart{id: id, description: state.description, calls: state.calls})
			continue
		}
		if _, running := tasksByID[state.taskID]; !running {
			delete(r.agentProgress, id)
			continue
		}
		parts = append(parts, agentStatusPart{id: id, description: state.description, calls: state.calls})
		delete(tasksByID, state.taskID)
	}
	// A running agent task no entry knows (its call ended without a result) is
	// still a running sub-agent: name it by its task.
	for id, info := range tasksByID {
		parts = append(parts, agentStatusPart{id: id, description: strings.TrimPrefix(info.Summary, "agent: ")})
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].description != parts[j].description {
			return parts[i].description < parts[j].description
		}
		return parts[i].id < parts[j].id
	})
	return parts
}

// agentTasks is the renderer's view of the session's tasks (nil: none).
func (r *Renderer) agentTasks() []tasks.Info {
	if r.opts.AgentTasks == nil {
		return nil
	}
	return r.opts.AgentTasks()
}

// refreshAgentStatus puts what the line says back in place after a background
// agent changed outside the event stream (its task ended or was killed).
func (r *Renderer) refreshAgentStatus() { r.spin.SetLabel(r.agentStatusLabel()) }

// EndTurnAgents forgets the entries no background task backs: a foreground
// sub-agent ends with its turn (a cancelled turn leaves no tool result, a failed
// one no summary), so the status line of the next turn starts clean.
func (r *Renderer) EndTurnAgents() {
	r.agentMu.Lock()
	for id, state := range r.agentProgress {
		if state == nil || state.taskID == "" {
			delete(r.agentProgress, id)
		}
	}
	r.agentMu.Unlock()
	r.refreshAgentStatus()
}

// beginAgent records the agent tool call of event, so the status line counts
// the sub-agent's tool calls under that call. The call ID is the key and the
// description is only its label: two children with the same description stay
// two entries. A provider that sends no call id leaves only the description as a
// key (the turn's end still clears such an entry).
func (r *Renderer) beginAgent(event personal.Event) {
	if event.ToolName != "agent" {
		return
	}
	description, background := agentCallDetails(event.Text)
	if description == "" {
		return
	}
	r.agentMu.Lock()
	r.agentProgress[agentEntryKey(event.CallID, description)] = &agentProgressState{description: description, started: time.Now(), background: background}
	r.agentMu.Unlock()
}

// agentEntryKey is the entry of an agent call: its call ID, or a key built from
// the description when the provider sent no call id.
func agentEntryKey(callID, description string) string {
	if callID != "" {
		return callID
	}
	return "desc:" + description
}

func agentCallDetails(text string) (string, bool) {
	var args struct {
		Description string `json:"description"`
		Background  bool   `json:"background"`
	}
	if err := json.Unmarshal([]byte(text), &args); err != nil {
		return "", false
	}
	return strings.TrimSpace(args.Description), args.Background
}

// finishAgent ends the entry of an agent tool result. Any end counts - a report,
// an error or a refusal - so an entry never outlives its call. A background call
// keeps its entry: the task it started is what runs, and the manager's task list
// decides when it is gone; its summary line comes with that task's notice
// (AgentTaskSummaries).
func (r *Renderer) finishAgent(event personal.Event) {
	if event.ToolName != "agent" {
		return
	}
	summary := event.AgentSummary
	key := event.CallID
	if key == "" && summary != nil {
		key = agentEntryKey("", summary.Description)
	}
	r.agentMu.Lock()
	state := r.agentProgress[key]
	background := (state != nil && state.background) || (summary != nil && summary.Background)
	calls := 0
	if state != nil {
		calls = state.calls
	}
	switch {
	case background:
		// The task keeps the entry alive (runningAgents); its id is how the
		// manager's task list is matched against it.
		if state != nil && summary != nil {
			state.taskID = summary.TaskID
		}
	case key != "":
		delete(r.agentProgress, key)
	}
	r.agentMu.Unlock()
	if background || summary == nil || summary.Description == "" {
		return
	}
	if summary.ToolCalls > calls {
		calls = summary.ToolCalls
	}
	if r.agentProgressMode() == "summary" {
		fmt.Fprintln(r.opts.Out, r.tr().T("approval.render.agent.summary", map[string]any{
			"description": truncateLine(summary.Description, 40), "calls": calls, "elapsed": formatElapsed(summary.Duration),
		}))
	}
}

// dropAgentTask forgets the entry of a background agent task that ended.
func (r *Renderer) dropAgentTask(taskID string) {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	for id, state := range r.agentProgress {
		if state != nil && state.taskID == taskID {
			delete(r.agentProgress, id)
		}
	}
}

// AgentTaskSummaries prints completions of background agents before their task
// notice. The task manager owns their counters because they outlive the turn.
func (r *Renderer) AgentTaskSummaries(infos []tasks.Info) {
	if r.agentProgressMode() != "summary" {
		return
	}
	for _, info := range infos {
		if info.Kind != "agent" || !strings.HasPrefix(info.Summary, "agent: ") {
			continue
		}
		name := strings.TrimPrefix(info.Summary, "agent: ")
		calls := info.Counters["tool_calls"]
		fmt.Fprintln(r.opts.Out, r.tr().T("approval.render.agent.summary", map[string]any{
			"description": truncateLine(name, 40), "calls": calls, "elapsed": formatElapsed(info.Duration()),
		}))
		r.dropAgentTask(info.ID)
	}
	r.refreshAgentStatus()
}
