package chat

import (
	"strings"

	"github.com/inhere/suga/internal/personal"
)

// nudgeHint shows the "stopped too early" rule hits as one gray line under a
// finished turn. Chat never pushes the model back by itself: the person reads the
// hint and decides whether to send the next instruction (design
// docs/design/2026-10-22-nudge-design.md).
func (s *chatState) nudgeHint(result personal.RunResult) {
	findings := s.runner.NudgeFindings(s.ctx, s.sessionID, result)
	if len(findings) == 0 {
		return
	}
	for _, finding := range findings {
		personal.RecordNudge(s.ctx, s.runner, s.sessionID, personal.NudgeActionHinted, finding)
	}
	s.render.Hint("⚠ " + nudgeHintText(findings))
}

// nudgeHintText is the hint line's text without the marker, for tests and hosts
// that render it themselves.
func nudgeHintText(findings []personal.NudgeFinding) string {
	reasons := make([]string, 0, len(findings))
	for _, finding := range findings {
		reasons = append(reasons, finding.Evidence)
	}
	return strings.Join(reasons, "；")
}
