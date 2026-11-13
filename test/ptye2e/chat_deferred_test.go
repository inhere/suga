//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestChatDeferredNative ports scripts/ptysmoke/chat_deferred_native.py: with
// `tools.deferred_native: auto` the first request carries tool_search and
// tool_call but not memory, and the segment context lists the deferred native
// tools. After `:compact` (a segment boundary) the tool table holds memory
// again. The trace shows the deferred counts, the tool_search that loaded memory
// without invalidating the cache inside the segment, and the tool event named
// memory (the proxy call was expanded before approval and trace).
func TestChatDeferredNative(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "deferred", Delay: 400 * time.Millisecond, Reply: "SMOKE_ANSWER_ONE"}
	server := httptest.NewServer(fake)
	defer server.Close()
	work := t.TempDir()
	s, env := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: work, URL: server.URL,
		ToolsExtra: "  deferred_native: auto\n"})

	mark := s.Mark()
	expect(t, s, `❯ `, mark)
	mark = s.Mark()
	s.SendLine("找一下记忆里的 smoke 规则")
	expect(t, s, `SMOKE_ANSWER_ONE`, mark)
	promptReady(t, s, mark)
	mark = s.Mark()
	s.SendLine(":compact")
	promptReady(t, s, mark)
	mark = s.Mark()
	s.SendLine("再说一次")
	expect(t, s, `SMOKE_ANSWER_ONE`, mark)
	// The segment boundary must be behind us before the requests are read.
	promptReady(t, s, mark)

	id := exitAfterTurn(t, s, mark)
	check(t, "session id", id != "")
	if id == "" {
		return
	}

	// What the model was offered and told, from the fake model's request log.
	requests := fake.Requests()
	check(t, "request log is not empty", len(requests) > 0)
	if len(requests) == 0 {
		return
	}
	first := requests[0]
	check(t, "request 1: memory is not in the tool table", !containsString(first.Tools, "memory"))
	check(t, "request 1: tool_search is registered", containsString(first.Tools, "tool_search"))
	check(t, "request 1: tool_call is registered", containsString(first.Tools, "tool_call"))
	check(t, "request 1: segment context lists the deferred tools", strings.Contains(first.Messages, "延迟加载的工具"))
	check(t, "request 1: memory is listed with its purpose", strings.Contains(first.Messages, "- memory —"))
	check(t, "after the segment boundary the table contains memory",
		waitRequestTool(fake, 0, "memory", 5*time.Second))

	events := traceEvents(t, binary, work, env, id)
	ctx := attrs(findEvent(events, "context", ""))
	deferredNative, _ := ctx["deferred_native"].(float64)
	deferredTools, _ := ctx["deferred_tools"].(float64)
	check(t, "trace: context reports deferred_native > 0", deferredNative > 0)
	check(t, "trace: context reports a deferred listing", deferredTools > 0)

	var search map[string]any
	for _, e := range events {
		if e["kind"] != "tool_search" {
			continue
		}
		for _, name := range strings2strings(attrs(e)["newly_loaded"]) {
			if name == "memory" {
				search = e
			}
		}
	}
	check(t, "trace: tool_search loaded memory", search != nil)
	if search != nil {
		sattrs := attrs(search)
		check(t, "trace: loading inside a segment invalidates no cache", sattrs["cache_invalidated"] == false)
		kinds, _ := sattrs["newly_loaded_kinds"].(map[string]any)
		native, _ := kinds["native"].(float64)
		check(t, "trace: tool_search reports the native kind", int(native) == 1)
	}

	var names []string
	for _, e := range events {
		if e["kind"] == "tool" {
			names = append(names, str(e["name"]))
		}
	}
	check(t, "trace: the proxied call runs as memory", containsString(names, "memory"))
	check(t, "trace: the proxy never shows up as the executed tool", !containsString(names, "tool_call"))
}

// strings2strings lists the string items of an untyped JSON list (nil-safe).
func strings2strings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// waitRequestTool reports whether any request from index from on offers tool.
func waitRequestTool(f *fakeLLM, from int, tool string, timeout time.Duration) bool {
	_, ok := waitRequest(f, from, func(r fakeRequest) bool { return containsString(r.Tools, tool) }, timeout)
	return ok
}
