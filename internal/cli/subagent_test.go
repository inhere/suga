package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// subAgentServer is a fake OpenAI endpoint: the parent model calls the agent
// tool once and then answers; a sub-agent request (its system prompt names
// it) greps once and then reports. It keeps the parent's second request.
type subAgentServer struct {
	mu          sync.Mutex
	parentCalls int
	childCalls  int
	parentLast  string // the parent's last request, as JSON
}

func (s *subAgentServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &body)
	child := false
	for _, m := range body.Messages {
		if m.Role == "system" && strings.Contains(fmt.Sprint(m.Content), "suga sub-agent") {
			child = true
		}
	}
	reply := func(message string, in int) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":%s,"finish_reason":"%s"}],"usage":{"prompt_tokens":%d,"completion_tokens":1,"total_tokens":%d}}`,
			message, map[bool]string{true: "tool_calls", false: "stop"}[strings.Contains(message, "tool_calls")], in, in+1)
	}
	call := func(name, args string) string {
		quoted, _ := json.Marshal(args)
		return `{"tool_calls":[{"id":"c-` + name + `","type":"function","function":{"name":"` + name + `","arguments":` + string(quoted) + `}}]}`
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if child {
		s.childCalls++
		if s.childCalls == 1 {
			reply(call("file_grep", `{"pattern":"func Foo"}`), 100)
			return
		}
		reply(`{"content":"REPORT: Foo is defined in a.go:3"}`, 100)
		return
	}
	s.parentCalls++
	if s.parentCalls == 1 {
		reply(call("agent", `{"description":"find Foo","prompt":"Where is Foo defined? Report file:line."}`), 10)
		return
	}
	s.parentLast = string(raw)
	reply(`{"content":"done"}`, 10)
}

// TestRunWithSubAgent: run's model hands a task to a sub-agent; its report is
// the tool result, its progress a stream-json line, its usage counts in the
// run, its session is hidden from the session list and named in the trace.
func TestRunWithSubAgent(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", shortTestHome(t))
	workspace := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(workspace, "a.go"), []byte("package a\n\nfunc Foo() {}\n"), 0o644))
	t.Chdir(workspace)
	assert.NoErr(t, gkconfig.Set("workspace", workspace))
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	fake := &subAgentServer{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)

	var out, diag bytes.Buffer
	args := []string{"--prompt", "find Foo", "--output-format", "stream-json", "--read-only"}
	assert.Require(t, assert.NoErr(t, RunPersonalCLI(context.Background(), "run", args, strings.NewReader(""), &out, &diag)), diag.String())

	var progress, result map[string]any
	for _, raw := range bytes.Split(out.Bytes(), []byte("\n")) {
		var line map[string]any
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		switch line["type"] {
		case "agent_progress":
			progress = line
		case "result":
			result = line
		}
	}
	t.Run("the report is the tool result", func(t *testing.T) {
		assert.Eq(t, 2, fake.childCalls)
		assert.StrContains(t, fake.parentLast, "REPORT: Foo is defined in a.go:3")
		// Both id formats: sg_... now, s_.../sess_... from older versions.
		assert.Eq(t, true, strings.Contains(fake.parentLast, "[sub-agent sg_") ||
			strings.Contains(fake.parentLast, "[sub-agent s_") ||
			strings.Contains(fake.parentLast, "[sub-agent sess_"))
		assert.StrContains(t, fake.parentLast, "stop=final")
	})
	t.Run("progress and usage reach the run", func(t *testing.T) {
		assert.Require(t, assert.NotNil(t, progress))
		assert.Eq(t, "find Foo", progress["agent"])
		assert.Eq(t, "file_grep", progress["name"])
		assert.Require(t, assert.NotNil(t, result))
		usage := result["usage"].(map[string]any)
		assert.Eq(t, float64(220), usage["input_tokens"]) // 2 parent calls of 10, 2 sub-agent calls of 100
	})
	t.Run("the sub-agent session is hidden and linked", func(t *testing.T) {
		dbPath, err := appconfig.ResolveCoreStorePath()
		assert.Require(t, assert.NoErr(t, err))
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		defer func() { _ = repo.Close() }()
		all, err := repo.ListSessions(context.Background())
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, all, 2))
		var parent, child store.Session
		for _, s := range all {
			if store.IsAgentSession(s) {
				child = s
			} else {
				parent = s
			}
		}
		assert.Eq(t, parent.ID, store.ParentSession(child))
		assert.Eq(t, "find Foo", child.Title)
		events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: parent.ID, Kinds: []string{store.EventTool}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, events, 1))
		assert.Eq(t, "agent", events[0].Name)
		assert.Eq(t, child.ID, events[0].Attrs["child_session_id"])
		assert.Eq(t, "final", events[0].Attrs["stop"])
		assert.Eq(t, "description=find Foo", events[0].Attrs["arg"]) // its label, not the whole prompt
		listedOut, err := runCLI(t, context.Background(), "session", "list", "--json")
		assert.Require(t, assert.NoErr(t, err))
		assert.NotContains(t, listedOut, child.ID)
		// The parent's stats include what its sub-agent spent.
		statsOut, err := runCLI(t, context.Background(), "inspect", "stats", "--session", parent.ID, "--json")
		assert.Require(t, assert.NoErr(t, err))
		var rep struct {
			Models []struct {
				Calls       int `json:"calls"`
				InputTokens int `json:"input_tokens"`
			} `json:"models"`
		}
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(statsOut), &rep)), statsOut)
		assert.Require(t, assert.Len(t, rep.Models, 1), statsOut)
		assert.Eq(t, 4, rep.Models[0].Calls)
		assert.Eq(t, 220, rep.Models[0].InputTokens)
	})
}

// forkDirectiveMarks are the first words of the fork directive (model.fork.directive)
// in zh and en: a request carrying one is a fork's own model call. A fork's system
// prompt is the parent's own (design F2), so the parent's prompt cannot separate
// the two.
var forkDirectiveMarks = []string{"你是主会话派出的分身", "You are a fork dispatched"}

// forkServer is the fake OpenAI endpoint of the fork scenario (plan T7 GR110): the
// parent model hands the conversation to a fork (agent type: fork), waits for its
// task and answers; a fork request (its tail carries the directive) greps once and
// then reports. Every request is kept so the scenario can check what each side
// sent.
type forkServer struct {
	mu               sync.Mutex
	parentCalls      int
	forkCalls        int
	parentLast       string // the parent's last request, as JSON
	forkFirst        string // the fork's first request, as JSON
	forkFirstToolRes string // the tool result its first request carried (the directive)
}

func (s *forkServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &body)
	fork := false
	for _, m := range body.Messages {
		text := fmt.Sprint(m.Content)
		for _, mark := range forkDirectiveMarks {
			if strings.Contains(text, mark) {
				fork = true
			}
		}
	}
	reply := func(message string, in int) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":%s,"finish_reason":"%s"}],"usage":{"prompt_tokens":%d,"completion_tokens":1,"total_tokens":%d}}`,
			message, map[bool]string{true: "tool_calls", false: "stop"}[strings.Contains(message, "tool_calls")], in, in+1)
	}
	call := func(name, args string) string {
		quoted, _ := json.Marshal(args)
		return `{"tool_calls":[{"id":"c-` + name + `","type":"function","function":{"name":"` + name + `","arguments":` + string(quoted) + `}}]}`
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fork {
		s.forkCalls++
		if s.forkCalls == 1 {
			s.forkFirst = string(raw)
			for i := len(body.Messages) - 1; i >= 0; i-- {
				if body.Messages[i].Role == "tool" {
					s.forkFirstToolRes = fmt.Sprint(body.Messages[i].Content)
					break
				}
			}
			reply(call("file_grep", `{"pattern":"func Foo"}`), 100)
			return
		}
		reply(`{"content":"REPORT: Foo is defined in a.go:3"}`, 100)
		return
	}
	s.parentCalls++
	s.parentLast = string(raw)
	switch s.parentCalls {
	case 1:
		reply(call("agent", `{"description":"find Foo","prompt":"Where is Foo defined? Report file:line.","type":"fork"}`), 10)
	case 2:
		// The fork runs as a task: waiting for it is what hands the report back.
		reply(call("task", `{"action":"wait","id":"t1"}`), 10)
	default:
		reply(`{"content":"done"}`, 10)
	}
}

// TestRunWithFork (plan T7, design GR110): through the CLI's own dispatch, run's
// model hands the conversation to a fork and waits for its task. The fork's
// request carries the parent's prefix, the report comes back as the task tool's
// result, and its session is the fork child of the parent (agent_type=fork,
// fork_base) with the fork's task event in the parent's trace.
func TestRunWithFork(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", shortTestHome(t))
	workspace := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(workspace, "a.go"), []byte("package a\n\nfunc Foo() {}\n"), 0o644))
	t.Chdir(workspace)
	assert.NoErr(t, gkconfig.Set("workspace", workspace))
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	fake := &forkServer{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)

	var out, diag bytes.Buffer
	args := []string{"--prompt", "find Foo", "--output-format", "stream-json", "--read-only"}
	assert.Require(t, assert.NoErr(t, RunPersonalCLI(context.Background(), "run", args, strings.NewReader(""), &out, &diag)), diag.String())

	var result map[string]any
	for _, raw := range bytes.Split(out.Bytes(), []byte("\n")) {
		var line map[string]any
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		if line["type"] == "result" {
			result = line
		}
	}

	t.Run("the fork request starts from the parent's request", func(t *testing.T) {
		assert.Eq(t, 2, fake.forkCalls) // grep, then the report
		assert.StrContains(t, fake.forkFirst, "find Foo")
		// The tail's tool result is the directive, not the parent's started line:
		// the fork was handed the conversation, not the task result.
		for _, mark := range forkDirectiveMarks {
			if strings.Contains(fake.forkFirstToolRes, mark) {
				return
			}
		}
		t.Fatalf("the fork's first request carries no directive: %q", fake.forkFirstToolRes)
	})
	t.Run("the report is the task result", func(t *testing.T) {
		assert.StrContains(t, fake.parentLast, "REPORT: Foo is defined in a.go:3")
		assert.Require(t, assert.NotNil(t, result), out.String())
		assert.Eq(t, "done", result["result"])
	})
	t.Run("the fork session is recorded as a fork", func(t *testing.T) {
		dbPath, err := appconfig.ResolveCoreStorePath()
		assert.Require(t, assert.NoErr(t, err))
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		defer func() { _ = repo.Close() }()
		all, err := repo.ListSessions(context.Background())
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, all, 2))
		var parent, child store.Session
		for _, s := range all {
			if store.IsAgentSession(s) {
				child = s
			} else {
				parent = s
			}
		}
		assert.Eq(t, parent.ID, store.ParentSession(child))
		assert.Eq(t, "fork", child.Meta["agent_type"])
		assert.StrContains(t, fmt.Sprint(child.Meta["fork_base"]), "|call")
		events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: parent.ID, Kinds: []string{store.EventTask}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, events, 1))
		assert.Eq(t, "agent", events[0].Attrs["kind"])
		assert.Eq(t, "fork", events[0].Attrs["agent_type"])
		assert.Eq(t, child.ID, events[0].Attrs["child_session_id"])
		assert.Eq(t, "final", events[0].Attrs["stop"])
		assert.Eq(t, true, attrNumber(events[0].Attrs["steps"]) > 0, fmt.Sprint(events[0].Attrs))
	})
}

// attrNumber is an event attr as an int (0 when it is not a number): a value read
// back from the store loses the Go type it was written with.
func attrNumber(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
