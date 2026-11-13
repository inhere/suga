//go:build ptye2e

package ptye2e

import (
	"regexp"
	"strings"
)

// The agent and session modes of the shared fake model (fakellm.py's --agent,
// --agent-when, --child-fail, --child-delay and --mode session), used by
// chat_agent_test.go and chat_session_message_test.go.

// subAgentPrompt is what a sub-agent's system prompt names itself by: a request
// carrying it is the sub-agent's own model call, not the parent chat's
// (fakellm.py's `child`).
const subAgentPrompt = "suga sub-agent"

// isSubAgent reports a request made by a sub-agent.
func (f *fakeLLM) isSubAgent(messages []fakeMessage) bool {
	for _, m := range messages {
		if m.Role == "system" && strings.Contains(m.text(), subAgentPrompt) {
			return true
		}
	}
	return false
}

// subAgentPlan is the sub-agent's script: one file_grep while the grep's result
// is not in, then the report it finishes with.
func (f *fakeLLM) subAgentPlan(messages []fakeMessage) (*toolCall, string) {
	if messages[len(messages)-1].Role != "tool" {
		return &toolCall{"file_grep", map[string]any{"pattern": "SMOKE"}}, ""
	}
	return nil, "SUBREPORT: SMOKE is in target.txt"
}

// agentCall is the agent mode's script for a parent chat request: the agent tool
// for a turn whose user message passes AgentWhen (every one without it) and
// nothing once its result is in, so the turn ends with the text reply. A user
// message that is a background task notice is answered with text, never with a
// new agent call.
func (f *fakeLLM) agentCall(user string, toolDone bool) *toolCall {
	if !f.Agent || !agentWanted(f.AgentWhen, user) || toolDone || strings.Contains(user, "[background task") {
		return nil
	}
	args := map[string]any{"description": "smoke survey", "prompt": "Where is SMOKE? Report file:line."}
	if f.Background {
		args["background"] = true
	}
	return &toolCall{"agent", args}
}

// agentWanted narrows the agent call to the user messages containing when
// ("" = every message): a smoke can run an agent turn and then a plain text turn
// in the same chat (the status line after a cancelled or killed agent).
func agentWanted(when, user string) bool { return when == "" || strings.Contains(user, when) }

// The two ways a session-message smoke names its target: the tag a received
// message carries ("[来自会话 <短名称>（<id>）的消息] ") and the person's own line
// ("发给 <短名称>：<text>"), so no fake server has to know the peers in advance.
var (
	sessionTag = regexp.MustCompile(`\[来自会话 (\S+?)（`)
	sessionTo  = regexp.MustCompile(`发给 (\S+?)[：:]`)
)

// sessionTarget is the short name a session mode answer goes to: the sender a
// received message names in its tag, or the peer the person's own line addresses.
// "" when the text names nobody.
func sessionTarget(text string) string {
	if m := sessionTag.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	if m := sessionTo.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// sessionCall is the session mode's script: one session_message send to the peer
// the user message names, then the text reply.
func (f *fakeLLM) sessionCall(user string, toolDone bool) *toolCall {
	target := sessionTarget(user)
	if toolDone || target == "" {
		return nil
	}
	return &toolCall{"session_message", map[string]any{
		"action": "send", "to": target, "text": orDefault(f.SessionText, "SMOKE_SESSION_TEXT"),
	}}
}
