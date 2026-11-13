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
//	nudge   a file_write of WritePath (WriteBody) once, then the text Reply: the
//	        change is never checked and the answer promises to continue, so both
//	        "stopped too early" rules hit (chat_nudge_test.go).
//
//	adddir  every user message gets file_read of ReadPath, then file_write of
//	        WritePath (WriteBody); "只读"/"只写" in the message narrows it to
//	        one of the two calls.
//	image  one file_read of ReadPath (a picture), then the text Reply.
//	deferred  tool_search select:memory, then tool_call of memory through the
//	        segment apply mode (only to a request that offers tools: the
//	        compaction summary call gets text).
//	preferred  the MCP tool named by PreferredTool, then the text Reply: the
//	        preferred-tool smoke, whose environment section the request log
//	        carries as Preferred.
//	writev  a user message containing "write vK" gets a file_write of WritePath
//	        with the content vK (fakellm_rewind_test.go; rewind plan T0).
//
// The agent and session modes (with the sub-agent paths) live in
// fakellm_agent_test.go.
//
// Not ported yet (used by scripts that are still Python): demo.
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
	// CachedTokens is the part of the prompt reported as served from the provider
	// cache (prompt_tokens_details.cached_tokens); 0 reports none.
	CachedTokens int

	// FailFirst answers the first N requests with HTTP 503, then OverloadFirst
	// requests answer 200 with only a temporary API error, then BreakFirst
	// streams hang up after half the reply.
	FailFirst     int
	OverloadFirst int
	BreakFirst    int

	EditPath, EditOld, EditNew string // edit mode (target.txt, "SMOKE_TARGET old" -> "... new")
	RemovePath                 string // edit mode: file_remove target (delete.txt)
	ShellCommand               string // edit mode: shell_exec command (echo SMOKE_SHELL)
	WritePath, WriteBody       string // edit mode "附加" / adddir / empty-final: file_write target
	ReadPath                   string // adddir mode: file_read target

	// Batch 3 additions (suga-hb8v); their behaviour lives in fakellm_batch3_test.go.
	//
	// FailFrom answers request N and later with HTTP 503 (0 = off), FailCount of
	// them (<= 0: every later one): the first steps of a turn run, then it fails.
	FailFrom, FailCount int
	// refs mode: every user message gets file_refs for RefName on RefFile, then on
	// RefDir (defaults Load, svc/svc.go, ".").
	RefName, RefFile, RefDir string
	// loop modes: the successful command of loop-edit (default echo SMOKE_LOOP_SHELL)
	// and the failing one of loop-fail (default false).
	LoopCommand, LoopFailCommand string
	// ToolOnce makes tool mode call the command only while the request carries no
	// tool result at all: a steer inserted after the result must not start the call
	// again (fakellm.py's tool_done rule, chat_approval_test.go).
	ToolOnce bool

	// origin mode (fakellm_origin_test.go; suga safety plan T5): web_fetch of
	// PageURL (served by this same server as PageText), then shell_exec of
	// Command; a judge request (the external-origin judge's prompt) is answered
	// with JudgeReply whatever the mode.
	PageURL, PageText string
	JudgeReply        string

	// PreferredTool is the MCP tool preferred mode calls (default preferred_lookup).
	PreferredTool string

	// agent mode (fakellm_agent_test.go; fakellm.py --agent/--agent-when): the
	// agent tool, and the sub-agent's own grep-then-report exchange. AgentWhen
	// narrows the agent call to the user messages containing it (a plain turn
	// otherwise), ChildFail makes the sub-agent's model call fail for good (a 400,
	// not retried) and ChildDelay slows its report.
	Agent      bool
	AgentWhen  string
	ChildFail  bool
	ChildDelay time.Duration
	// SessionText is the text a session mode session_message send carries
	// (fakellm.py --session-text).
	SessionText string

	mu       sync.Mutex
	served   int
	arrived  int // requests received, counted before the delay (served counts after it)
	requests []fakeRequest
}

// fakeRequest is what a scenario can check the model received.
type fakeRequest struct {
	Model    string // the "model" field of the request
	Stream   bool
	LastRole string
	User     string   // the last user message
	Messages string   // the text of every message the model received, one per line
	Tools    []string // names of the tools offered
	ToolText string   // the last tool result (first 300 bytes)
	// Batch 3 additions: Seen is one short token per message of the request
	// (fakellm.py message_token: "user:<head>", "a:file_edit", "tool:edited ..."),
	// Step the tool results since the last user message.
	Seen []string
	Step int
	// Msgs is every message of the request in full (rewind plan T0).
	Msgs []fakeMsg
	// ImageParts counts the image_url parts of the last user message and UserRaw
	// is that message's raw content (the pictures a request carries), for the
	// image scenarios.
	ImageParts int
	UserRaw    string
	// ToolTextFull is the last tool result without the 300-byte cap of ToolText.
	ToolTextFull string
	// Preferred holds the "Preferred tool:" lines of the environment the model was
	// sent (fakellm.py's preferred field): what the prompt listed, in order.
	Preferred []string
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Command == "" {
		return "echo SMOKE_OK"
	}
	return f.Command
}

// preferredTool is the MCP tool preferred mode calls (fakellm.py --preferred-tool).
func (f *fakeLLM) preferredTool() string {
	if f.PreferredTool == "" {
		return "preferred_lookup"
	}
	return f.PreferredTool
}

// SetCommand changes the shell_exec command between the phases of a scenario.
func (f *fakeLLM) SetCommand(command string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Command = command
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && f.PageText != "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(w, f.PageText)
		return
	}
	var body struct {
		Model    string        `json:"model"`
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

	f.countArrival()

	// A sub-agent's request names itself in its system prompt; its model call
	// fails for good with ChildFail (a 400 is not retried), so the agent tool
	// answers with an error result and no report.
	sub := f.isSubAgent(body.Messages)
	if sub && f.ChildFail {
		http.Error(w, `{"error":{"message":"child model is broken"}}`, http.StatusBadRequest)
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

	req := fakeRequest{Model: body.Model, Stream: body.Stream, LastRole: body.Messages[len(body.Messages)-1].Role, User: user, Step: step}
	for _, m := range body.Messages {
		req.Seen = append(req.Seen, m.token())
	}
	for _, tl := range body.Tools {
		name := tl.Function.Name
		if name == "" {
			name = tl.Name
		}
		req.Tools = append(req.Tools, name)
	}
	var texts []string
	for _, m := range body.Messages {
		texts = append(texts, m.text())
	}
	req.Messages = strings.Join(texts, "\n")
	// The "Preferred tool:" lines of the environment the model was sent (they never
	// reach the terminal), fakellm.py's preferred field.
	for _, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "Preferred tool:") {
				req.Preferred = append(req.Preferred, strings.TrimSpace(line))
			}
		}
	}
	req.Msgs = captureMessages(body.Messages)
	for _, m := range body.Messages {
		if m.Role == "user" {
			req.UserRaw = string(m.Content)
			req.ImageParts = len(imageParts(m))
		}
	}
	if lastTool >= 0 {
		req.ToolText = body.Messages[lastTool].text()
		req.ToolTextFull = req.ToolText
		if len(req.ToolText) > 300 {
			req.ToolText = req.ToolText[:300]
		}
	}

	delay := f.delay()
	if sub && f.ChildDelay > 0 && body.Messages[len(body.Messages)-1].Role == "tool" {
		// ChildDelay slows the sub-agent's report (after its grep).
		delay += f.ChildDelay
	}
	select {
	case <-time.After(delay):
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
	if f.failsFrom(n) {
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
	subText := "" // a sub-agent's scripted text reply
	switch f.Mode {
	case "agent":
		call = f.agentCall(user, toolDone)
	case "session":
		call = f.sessionCall(user, toolDone)
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
	case "adddir":
		calls := []toolCall{
			{"file_read", map[string]any{"path": f.ReadPath}},
			{"file_write", map[string]any{"path": f.WritePath, "content": orDefault(f.WriteBody, "SMOKE_ADD_DIR_WRITE")}},
		}
		switch {
		case strings.Contains(user, "只读"):
			calls = calls[:1]
		case strings.Contains(user, "只写"):
			calls = calls[1:]
		}
		if step < len(calls) {
			call = &calls[step]
		}
	case "image":
		if step < 1 {
			call = &toolCall{"file_read", map[string]any{"path": orDefault(f.ReadPath, "shot.png")}}
		}
	case "writev":
		call = f.writevCall(user, step, req.Tools)
	case "deferred":
		// A request without tools (the compaction summary) is answered with text:
		// a tool call there leaves the summary empty and the segment break fails.
		calls := []toolCall{
			{"tool_search", map[string]any{"query": "select:memory"}},
			{"tool_call", map[string]any{"name": "memory", "arguments": map[string]any{"action": "search", "query": "smoke"}}},
		}
		if step < len(calls) && len(req.Tools) > 0 {
			call = &calls[step]
		}
	case "refs":
		call = f.refsCall(step)
	case "loop", "loop-edit", "loop-fail":
		call = f.loopCall(step)
	case "empty-final":
		if !toolDone {
			call = &toolCall{"file_write", map[string]any{"path": f.WritePath, "content": f.WriteBody}}
		}
	case "nudge":
		// nudge mode: a change that is never checked, then an answer that promises
		// to continue, so both rules of personal.CheckNudge hit.
		if !toolDone {
			call = &toolCall{"file_write", map[string]any{"path": f.WritePath, "content": f.WriteBody}}
		}
	case "edit":
		if calls := f.editCalls(user); step < len(calls) {
			call = &calls[step]
		}
	case "preferred":
		// The preferred MCP tool, then the closing text: the tool is listed in the
		// environment but not auto-allowed, so chat and run deny the call.
		if !toolDone {
			call = &toolCall{f.preferredTool(), map[string]any{"query": "SMOKE_PREFERRED"}}
		}
	case "origin":
		call = f.originCall(step)
	case "tool":
		// The step of THIS turn decides, not whether any tool result is the last
		// message: an interrupted turn leaves its own tool result in the
		// transcript, so the next user message arrives after a tool result that
		// belongs to the turn before it (chat's transcript keeps the pair, see
		// chat_tool_ctrlc_test.go). Step 0 is "no tool result for this user message
		// yet", which is the same answer as !toolDone in a turn that was not cut.
		want := step == 0
		if f.ToolOnce {
			// A steer after the result is a user message, so the step count starts
			// over: only a request without any tool result may call again.
			want = !hasToolResult(body.Messages)
		}
		if want && !strings.Contains(user, "[background task") {
			args := map[string]any{"command": f.command()}
			if f.Background {
				args["background"] = true
			}
			call = &toolCall{"shell_exec", args}
		}
	}

	// A sub-agent's request is answered by the child script in every mode
	// (fakellm.py's child branch): one file_grep, then the report.
	if sub {
		call, subText = f.subAgentPlan(body.Messages)
	}

	judge := isJudgeRequest(body.Messages)
	if judge {
		call = nil // the judge only ever answers with its verdict text
	}
	var delta map[string]any
	if call != nil {
		raw, _ := json.Marshal(call.args)
		delta = map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprintf("call_%d", time.Now().UnixNano()),
			"function": map[string]any{"name": call.name, "arguments": string(raw)},
		}}}
	} else if sub {
		delta = map[string]any{"content": subText}
	} else if f.Mode == "empty-final" {
		delta = map[string]any{}
	} else if judge {
		delta = map[string]any{"content": f.JudgeReply}
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
		if f.CachedTokens > 0 {
			usage["prompt_tokens_details"] = map[string]any{"cached_tokens": f.CachedTokens}
		}
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

// hasToolResult reports whether the request carries any tool result at all (any
// turn, not only the last one), which is what ToolOnce keys on.
func hasToolResult(messages []fakeMessage) bool {
	for _, m := range messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// isImagePart reports a user message that only carries a tool result's picture.
func isImagePart(m fakeMessage) bool {
	return len(imageParts(m)) > 0
}

// imageParts lists the image_url parts of a message (nil when it holds none).
func imageParts(m fakeMessage) []string {
	var parts []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(m.Content, &parts) != nil {
		return nil
	}
	var found []string
	for _, p := range parts {
		if p.Type == "image_url" {
			found = append(found, p.Type)
		}
	}
	return found
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
