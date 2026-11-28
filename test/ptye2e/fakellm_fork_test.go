//go:build ptye2e

package ptye2e

import "strings"

// The fork mode of the shared fake model (background-fork design 0.5, plan T7):
// the parent model hands the conversation to a fork (agent type: fork) and the
// fork's own requests get the sub-agent script (one grep, then the report).
//
//	fork  a user message without a tool result yet gets the agent call with
//	      type: fork; after its result the plain reply follows, except for a
//	      message saying 看输出: that one gets the task tool's output action of
//	      the task named TaskID (the kill scenario reads a killed task's output).
//
// A fork's request is told apart by the fork directive in its tail: its system
// prompt is the parent's own (design F2), so a system-prompt marker cannot work.

// forkDirectiveMarks are the first words of the fork directive
// (model.fork.directive) in zh and en: a request carrying one was sent by a fork.
var forkDirectiveMarks = []string{"你是主会话派出的分身", "You are a fork dispatched"}

// isForkText reports whether text carries the fork directive.
func isForkText(text string) bool {
	for _, mark := range forkDirectiveMarks {
		if strings.Contains(text, mark) {
			return true
		}
	}
	return false
}

// isForkRequest reports whether one of the request's messages carries the fork
// directive (so the request is a fork's own model call).
func isForkRequest(messages []fakeMessage) bool {
	for _, m := range messages {
		if isForkText(m.fullText()) {
			return true
		}
	}
	return false
}

// isForkMsgs is isForkRequest over the request log's captured messages.
func isForkMsgs(messages []fakeMsg) bool {
	for _, m := range messages {
		if isForkText(m.Text) {
			return true
		}
	}
	return false
}

// forkCall is the fork mode's script for a parent request: the agent call with
// type: fork (a fork always runs as a task), then the task tool's output of
// TaskID for a message asking for it, then nothing (the text reply ends the turn).
// A message saying 你好 is a plain turn (the :bg scenario needs a completed turn
// before :bg has a request to fork from).
func (f *fakeLLM) forkCall(user string, toolDone bool) *toolCall {
	if toolDone || strings.Contains(user, "[background task") || strings.Contains(user, "你好") {
		return nil
	}
	if strings.Contains(user, "看输出") {
		return &toolCall{"task", map[string]any{"action": "output", "id": f.taskID()}}
	}
	return &toolCall{"agent", map[string]any{
		"description": "smoke fork", "prompt": "Where is SMOKE? Report file:line.", "type": "fork",
	}}
}

// taskID is the id the fork mode's task tool reads (default t1).
func (f *fakeLLM) taskID() string {
	if f.TaskID == "" {
		return "t1"
	}
	return f.TaskID
}
