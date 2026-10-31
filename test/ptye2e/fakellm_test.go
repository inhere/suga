//go:build ptye2e

package ptye2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// fakeLLM is the Go port of scripts/ptysmoke/fakellm.py: an OpenAI-compatible
// chat/completions server (SSE for streamed calls, one JSON body otherwise) that
// scripts the model side of a chat scenario. It is shared by every migrated
// scenario; the older fakeChat (approval/shell modes) stays for the first
// scenarios that predate it.
//
// Modes (fakeLLM.Mode):
//
//	text    every request gets the text Reply (Lines/Wide make a long answer).
//	tool    every user message first gets a shell_exec of Command (in the
//	        background when Background is set), then the text Reply.
//	edit    every user message gets file_grep, then file_edit of EditPath; the
//	        words 删除/新建/附加/shell select file_remove/file_write/shell_exec.
//	display a file_read with a line window, a two-line shell_exec and an
//	        update_plan (a tool without a summary of its own), then Reply.
//	memory  shell_exec of Command, then memory add ("Smoke rule"), then Reply;
//	        a user message containing "直接" skips the shell_exec.
//
// Not ported yet (used by scripts that are still Python): refs, adddir, image,
// deferred, demo, loop*, nudge, session, preferred, empty-final and
// the sub-agent paths.
type fakeLLM struct {
	Mode       string
	Delay      time.Duration // before every response (fakellm.py --delay, default 1s)
	Command    string        // tool/memory modes: the shell_exec command
	Background bool          // tool mode: shell_exec with background: true
	Reply      string        // the closing text (default "完成")
	Lines      int           // text mode: answer with this many lines
	Wide       int           // text mode with Lines: pad every line to this many columns

	// PromptTokens is the prompt size reported in usage (default 10); NoUsage
	// omits usage entirely.
	PromptTokens int
	NoUsage      bool

	// FailFirst answers the first N requests with HTTP 503, then OverloadFirst
	// requests answer 200 with only a temporary API error, then BreakFirst
	// streams hang up after half the reply.
	FailFirst     int
	OverloadFirst int
	BreakFirst    int

	EditPath, EditOld, EditNew string // edit mode (target.txt, "SMOKE_TARGET old" -> "... new")
	RemovePath                 string // edit mode: file_remove target (delete.txt)
	ShellCommand               string // edit mode: shell_exec command (echo SMOKE_SHELL)
	WritePath, WriteBody       string // edit mode "附加": file_write target

	mu       sync.Mutex
	served   int
	requests []fakeRequest
}

// fakeRequest is what a scenario can check the model received.
type fakeRequest struct {
	Stream   bool
	LastRole string
	User     string   // the last user message
	Tools    []string // names of the tools offered
	ToolText string   // the last tool result (first 300 bytes)
}

type fakeMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// text is the message content when it is a plain string (a list of parts, e.g. a
// tool result's picture, reads as empty).
func (m fakeMessage) text() string {
	var s string
	_ = json.Unmarshal(m.Content, &s)
	return s
}

// Requests returns a copy of the request log.
func (f *fakeLLM) Requests() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

func (f *fakeLLM) delay() time.Duration {
	if f.Delay != 0 {
		return f.Delay
	}
	return time.Second
}

func (f *fakeLLM) reply() string {
	reply := f.Reply
	if reply == "" {
		reply = "完成"
	}
	if f.Mode != "text" || f.Lines <= 1 {
		return reply
	}
	lines := make([]string, f.Lines)
	for i := range lines {
		line := fmt.Sprintf("%s-%02d ", reply, i+1)
		if pad := f.Wide - len([]rune(line)); pad > 0 {
			line += strings.Repeat(".", pad)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// toolCall is one scripted call: the tool name and its JSON arguments.
type toolCall struct {
	name string
	args map[string]any
}

// editCalls selects the edit-mode script by the words of the user message: the
// ordinary flow is grep -> edit, the named ones pick one write tool for the undo
// style scenarios.
func (f *fakeLLM) editCalls(user string) []toolCall {
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	switch {
	case strings.Contains(user, "回滚通知"):
		return nil
	case strings.Contains(user, "删除"):
		return []toolCall{{"file_remove", map[string]any{"path": or(f.RemovePath, "delete.txt")}}}
	case strings.Contains(user, "新建"):
		return []toolCall{{"file_write", map[string]any{"path": "new.txt", "content": "SMOKE_NEW"}}}
	case strings.Contains(user, "附加"):
		return []toolCall{{"file_write", map[string]any{"path": f.WritePath, "content": or(f.WriteBody, "SMOKE_ADD_DIR_WRITE")}}}
	case strings.Contains(user, "shell"):
		return []toolCall{{"shell_exec", map[string]any{"command": or(f.ShellCommand, "echo SMOKE_SHELL")}}}
	}
	return []toolCall{
		{"file_grep", map[string]any{"pattern": "SMOKE_TARGET"}},
		{"file_edit", map[string]any{
			"path": or(f.EditPath, "target.txt"), "old_string": or(f.EditOld, "SMOKE_TARGET old"),
			"new_string": or(f.EditNew, "SMOKE_TARGET new"),
		}},
	}
}

func (f *fakeLLM) command() string {
	if f.Command == "" {
		return "echo SMOKE_OK"
	}
	return f.Command
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stream   bool          `json:"stream"`
		Messages []fakeMessage `json:"messages"`
		Tools    []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
		http.Error(w, fmt.Sprint("bad request: ", err), http.StatusBadRequest)
		return
	}

	// step counts the tool results since the last user message: the step of this
	// turn. lastTool/lastAssistant tell whether the step's result is already in.
	user, step := "", 0
	lastTool, lastAssistant := -1, -1
	for i := len(body.Messages) - 1; i >= 0; i-- {
		switch body.Messages[i].Role {
		case "user":
			if user == "" {
				user = body.Messages[i].text()
			}
		case "tool":
			if lastTool < 0 {
				lastTool = i
			}
		case "assistant":
			if lastAssistant < 0 {
				lastAssistant = i
			}
		}
	}
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role == "user" && !isImagePart(body.Messages[i]) {
			break
		}
		if body.Messages[i].Role == "tool" {
			step++
		}
	}
	toolDone := lastTool > lastAssistant

	req := fakeRequest{Stream: body.Stream, LastRole: body.Messages[len(body.Messages)-1].Role, User: user}
	for _, tl := range body.Tools {
		name := tl.Function.Name
		if name == "" {
			name = tl.Name
		}
		req.Tools = append(req.Tools, name)
	}
	if lastTool >= 0 {
		req.ToolText = body.Messages[lastTool].text()
		if len(req.ToolText) > 300 {
			req.ToolText = req.ToolText[:300]
		}
	}

	select {
	case <-time.After(f.delay()):
	case <-r.Context().Done():
		return
	}
	f.mu.Lock()
	f.served++
	n := f.served
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	if n <= f.FailFirst {
		http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
		return
	}
	afterFailures := n - f.FailFirst
	if afterFailures <= f.OverloadFirst {
		f.overload(w, body.Stream)
		return
	}
	broken := afterFailures <= f.OverloadFirst+f.BreakFirst

	var call *toolCall
	switch f.Mode {
	case "memory":
		calls := []toolCall{
			{"shell_exec", map[string]any{"command": f.command()}},
			{"memory", map[string]any{"action": "add", "kind": "feedback", "title": "Smoke rule", "body": "smoke memory body"}},
		}
		if strings.Contains(user, "直接") {
			calls = calls[1:]
		}
		if step < len(calls) {
			call = &calls[step]
		}
	case "display":
		calls := []toolCall{
			{"file_read", map[string]any{"path": "target.txt", "offset": 1, "limit": 2}},
			{"shell_exec", map[string]any{"command": "echo SMOKE_DISPLAY\n  echo SMOKE_SECOND"}},
			{"update_plan", map[string]any{"plan": []any{map[string]any{"step": "read", "status": "in_progress"}}}},
		}
		if step < len(calls) {
			call = &calls[step]
		}
	case "edit":
		if calls := f.editCalls(user); step < len(calls) {
			call = &calls[step]
		}
	case "tool":
		if !toolDone && !strings.Contains(user, "[background task") {
			args := map[string]any{"command": f.command()}
			if f.Background {
				args["background"] = true
			}
			call = &toolCall{"shell_exec", args}
		}
	}

	var delta map[string]any
	if call != nil {
		raw, _ := json.Marshal(call.args)
		delta = map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprintf("call_%d", time.Now().UnixNano()),
			"function": map[string]any{"name": call.name, "arguments": string(raw)},
		}}}
	} else {
		delta = map[string]any{"content": f.reply()}
	}
	var usage map[string]any
	if !f.NoUsage {
		prompt := f.PromptTokens
		if prompt == 0 {
			prompt = 10
		}
		usage = map[string]any{"prompt_tokens": prompt, "completion_tokens": 2, "total_tokens": prompt + 2}
	}

	if !body.Stream {
		// Non-streaming calls (e.g. the compaction summary) get one JSON body.
		message := map[string]any{"role": "assistant", "content": delta["content"]}
		finish := "stop"
		if calls, ok := delta["tool_calls"].([]any); ok {
			for _, c := range calls {
				c.(map[string]any)["type"] = "function"
			}
			message["tool_calls"], finish = calls, "tool_calls"
		}
		response := map[string]any{"id": "fake", "object": "chat.completion", "choices": []any{
			map[string]any{"index": 0, "message": message, "finish_reason": finish},
		}}
		if usage != nil {
			response["usage"] = usage
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	send := func(chunk any) {
		data, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	if broken {
		// Half of the text, then hang up without ending the body.
		half := []rune(f.Reply)
		send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"content": string(half[:max(1, len(half)/2)]),
		}}}})
		panic(http.ErrAbortHandler)
	}
	send(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
	if usage != nil {
		send(map[string]any{"choices": []any{}, "usage": usage})
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

// isImagePart reports a user message that only carries a tool result's picture.
func isImagePart(m fakeMessage) bool {
	var parts []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(m.Content, &parts) != nil {
		return false
	}
	for _, p := range parts {
		if p.Type == "image_url" {
			return true
		}
	}
	return false
}

// overload answers 200 with only a temporary API error: as an SSE error event
// when streamed, as a JSON error body otherwise (the shapes providers report an
// overload in; both must be retried instead of failing the turn).
func (f *fakeLLM) overload(w http.ResponseWriter, stream bool) {
	payload, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": "server_is_overloaded", "message": "Our servers are currently overloaded. Please try again later.",
	}})
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}
