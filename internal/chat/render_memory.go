package chat

import (
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// memoryVerbs names a memory write on the end-of-turn line.
var memoryVerbs = map[string]string{
	native.MemoryActionAdd:     "approval.render.memory.add",
	native.MemoryActionReplace: "approval.render.memory.replace",
	native.MemoryActionRemove:  "approval.render.memory.remove",
}

// noteMemory remembers a successful memory write of this turn (parsed from
// the memory tool result) for the end-of-turn line.
func (r *Renderer) noteMemory(event personal.Event) {
	if event.ToolName != "memory" {
		return
	}
	change, ok := native.ParseMemoryResult(event.Text)
	if !ok {
		return
	}
	title := truncateLine(redact.Secrets(change.Title), resultSummaryLimit)
	verbKey := memoryVerbs[change.Action]
	r.memory = append(r.memory, r.tr().T(verbKey, map[string]any{"title": title, "scope": change.Scope}))
}
