//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// The parts of fakeLLM that the rewind/fork e2e scenarios (plan T0) need, kept
// out of fakellm_test.go like the batch 3 ones: full request capture and the
// "writev" mode.
//
//	writev  a user message containing "write vK" gets a file_write of WritePath
//	        with the content "vK"; after the tool result the plain Reply follows.
//	        The trigger is the content of the question, never the request count,
//	        because compaction and handoff add requests of their own (plan F13).

// fakeMsg is one message of a request as the model received it.
type fakeMsg struct {
	Role string
	Text string   // the whole text content; list parts are joined, so wrapped content (compaction summary, handoff package) is searchable
	Tool []string // names of the tool calls of an assistant message
}

// fullText is the message content as text: a plain string, or the text parts of
// a content list (fakeMessage.text reads the latter as empty).
func (m fakeMessage) fullText() string {
	if s := m.text(); s != "" {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &parts) != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// captureMessages converts the request messages for fakeRequest.Msgs.
func captureMessages(messages []fakeMessage) []fakeMsg {
	out := make([]fakeMsg, 0, len(messages))
	for _, m := range messages {
		item := fakeMsg{Role: m.Role, Text: m.fullText()}
		for _, c := range m.ToolCalls {
			item.Tool = append(item.Tool, c.Function.Name)
		}
		out = append(out, item)
	}
	return out
}

var writeVersion = regexp.MustCompile(`write (v\d+)`)

// writevCall is the writev mode: the file_write is the first step of a turn whose
// question names a version and only when the file_write tool is offered at all
// (the non-streaming compaction request carries no tools and must not write).
func (f *fakeLLM) writevCall(user string, step int, tools []string) *toolCall {
	m := writeVersion.FindStringSubmatch(user)
	if m == nil || step != 0 || !containsString(tools, "file_write") {
		return nil
	}
	return &toolCall{"file_write", map[string]any{"path": f.WritePath, "content": m[1]}}
}

// RequestHas reports whether request k (1-based, in the order answered) has text
// in any message. A request that does not exist has nothing.
func (f *fakeLLM) RequestHas(k int, text string) bool {
	reqs := f.Requests()
	if k < 1 || k > len(reqs) {
		return false
	}
	for _, m := range reqs[k-1].Msgs {
		if strings.Contains(m.Text, text) {
			return true
		}
	}
	return false
}

// requireRequestText asserts request k has (want) or lacks (!want) text.
func requireRequestText(t *testing.T, f *fakeLLM, k int, text string, want bool) {
	t.Helper()
	if got := f.RequestHas(k, text); got != want {
		t.Fatalf("request %d has %q = %v, want %v (requests: %d)", k, text, got, want, len(f.Requests()))
	}
}
