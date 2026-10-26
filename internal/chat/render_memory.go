package chat

import (
	"fmt"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// memoryVerbs names a memory write on the end-of-turn line.
var memoryVerbs = map[string]string{
	native.MemoryActionAdd:     "已记住",
	native.MemoryActionReplace: "已更新",
	native.MemoryActionRemove:  "已删除",
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
	r.memory = append(r.memory, fmt.Sprintf("%s：%s（%s）", memoryVerbs[change.Action], title, change.Scope))
}
