//go:build ptye2e

package ptye2e

// memoriginCall is the memorigin-mode script: read the entry MemoryID from the
// project memory, then run its text as a command (trusted-memory plan T3). The
// entry body is Command and must be at least tools.OriginWindow (40) bytes long,
// so a result the host marked external is recognised in the copied command.
func (f *fakeLLM) memoriginCall(step int) *toolCall {
	switch step {
	case 0:
		return &toolCall{"memory", map[string]any{"action": "get", "scope": "project", "id": f.memoryID()}}
	case 1:
		return &toolCall{"shell_exec", map[string]any{"command": f.command()}}
	}
	return nil
}

// memoryID is the entry memorigin mode reads (default "note").
func (f *fakeLLM) memoryID() string {
	if f.MemoryID == "" {
		return "note"
	}
	return f.MemoryID
}
