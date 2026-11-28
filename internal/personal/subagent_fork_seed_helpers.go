package personal

import "github.com/inhere/suga/pkg/agentcore"

func (r *Runner) observeForkTurnEnd(sessionID, turnID string, result *agentcore.RunResult, runErr error) {
	completed := runErr == nil && result != nil && result.Status == agentcore.RunCompleted
	r.rec.prefix.observeTurnEnd(sessionID, turnID, completed)
}
