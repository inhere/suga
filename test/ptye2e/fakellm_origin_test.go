//go:build ptye2e

package ptye2e

import "strings"

// originCall is the origin-mode script: fetch the page, then run the command
// copied from it (suga safety plan T5, external-origin judge).
func (f *fakeLLM) originCall(step int) *toolCall {
	switch step {
	case 0:
		return &toolCall{"web_fetch", map[string]any{"url": f.PageURL}}
	case 1:
		return &toolCall{"shell_exec", map[string]any{"command": f.command()}}
	}
	return nil
}

// isJudgeRequest reports a request made by the external-origin judge: its
// system prompt names the reviewer role.
func isJudgeRequest(messages []fakeMessage) bool {
	for _, m := range messages {
		if m.Role == "system" && strings.Contains(m.text(), "外部来源审查员") {
			return true
		}
	}
	return false
}

// judgeRequests returns the requests the judge made (prompt text in Messages).
func (f *fakeLLM) judgeRequests() []fakeRequest {
	var out []fakeRequest
	for _, r := range f.Requests() {
		if strings.Contains(r.Messages, "外部来源审查员") {
			out = append(out, r)
		}
	}
	return out
}
