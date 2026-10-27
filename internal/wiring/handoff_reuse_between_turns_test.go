package wiring

// Tests of the between-turn handoff reuse through the real assembly: two
// `Open`s on the same store stand in for two processes (`suga session compact`
// runs in its own), and an OpenAI-compatible fake records the wire requests.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
)

// handoffTemplateMarker is one section heading of the handoff template, so a
// request carrying it is a handoff-package call (the template is not
// translated, unlike the instruction around it).
const handoffTemplateMarker = "## 采纳与否决"

// fakeHandoffBody is a complete handoff package: every section the template
// asks for, so the reuse answer is accepted as it is.
const fakeHandoffBody = "## 当前任务\n修解析器\n## 坐标\n无\n## 采纳与否决\n无\n## 验证到哪\n无\n" +
	"## 进展与阻塞\n无\n## 下一步\n无\n## 与当前任务相关的引用\n无"

// betweenTurnsServer is an OpenAI-compatible fake: a main-loop call is answered
// with a final message, a handoff-package call with a complete package body.
// Every request body is kept, oldest first.
type betweenTurnsServer struct {
	mu       sync.Mutex
	requests [][]map[string]any
}

func (s *betweenTurnsServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.requests = append(s.requests, body.Messages)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		answer := "done"
		if wireIsHandoffCall(body.Messages) {
			answer = fakeHandoffBody
		}
		// A plain OpenAI chat completion whose content is answer as it is (the
		// handoff body's section headings have to stay at line start).
		resp, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": answer}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
		assert.Require(t, assert.NoErr(t, err))
		_, _ = w.Write(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wireIsHandoffCall reports whether an OpenAI-style request is a
// handoff-package call.
func wireIsHandoffCall(msgs []map[string]any) bool {
	for _, m := range msgs {
		if text, _ := m["content"].(string); strings.Contains(text, handoffTemplateMarker) {
			return true
		}
	}
	return false
}

// wireJSON renders a request's messages for a byte-for-byte comparison (map
// keys marshal sorted, so two equal requests render the same).
func wireJSON(t *testing.T, msgs []map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(msgs)
	assert.Require(t, assert.NoErr(t, err))
	return string(raw)
}

// handoffCall returns the messages of the handoff-package request (nil = none
// was sent).
func (s *betweenTurnsServer) handoffCall() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msgs := range s.requests {
		if wireIsHandoffCall(msgs) {
			return msgs
		}
	}
	return nil
}

// count returns how many requests the fake answered.
func (s *betweenTurnsServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// last returns the messages of the most recent request.
func (s *betweenTurnsServer) last() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

// TestHandoffReuseBetweenTurnsThroughAssembly: a segment break between turns in
// a fresh process (suga session compact) continues the session's last main-loop
// request through the assembly every entry point builds - the real system
// prompt, the real tool schemas and the persisted view - instead of the old
// handoff request with its own system prompt and one re-rendered transcript.
func TestHandoffReuseBetweenTurnsThroughAssembly(t *testing.T) {
	resetConfig(t)
	fake := &betweenTurnsServer{}
	server := fake.start(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": server.URL}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	// The reuse path is off by default since the 2026-10-27 evaluation.
	assert.NoErr(t, gkconfig.Set("context.handoff_reuse_prefix", true))

	open := func() *Runtime {
		rt, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir()})
		assert.Require(t, assert.NoErr(t, err))
		return rt
	}
	ctx := context.Background()
	// The first process runs one turn.
	first := open()
	res, err := first.Runner.Run(ctx, personal.RunRequest{Prompt: "修一下解析器"})
	assert.Require(t, assert.NoErr(t, err))
	mainMsgs := fake.last()
	assert.Require(t, assert.NotNil(t, mainMsgs))
	assert.Require(t, assert.NoErr(t, first.Close()))

	// A new process resumes the session and breaks it between turns: it has sent
	// no request, so the base is rebuilt from the persisted view.
	second := open()
	defer func() { _ = second.Close() }()
	_, err = second.Runner.Compact(ctx, res.SessionID)
	assert.NoErr(t, err)

	// One main-loop call and one handoff call: the old path would have been the
	// second request instead.
	assert.Eq(t, 2, fake.count())
	handoff := fake.handoffCall()
	assert.Require(t, assert.NotNil(t, handoff))
	// System prompt, tool list and messages are the turn's request byte for byte
	// (the assembly rendered them again); only the answer and the instruction
	// follow.
	assert.Require(t, assert.True(t, len(handoff) > len(mainMsgs)))
	assert.Eq(t, wireJSON(t, mainMsgs), wireJSON(t, handoff[:len(mainMsgs)]))
	rest := handoff[len(mainMsgs):]
	assert.Require(t, assert.Eq(t, 2, len(rest)))
	assert.Eq(t, "assistant", rest[0]["role"])
	assert.Eq(t, "done", rest[0]["content"])
	assert.Eq(t, "user", rest[1]["role"])
	instruction, _ := rest[1]["content"].(string)
	assert.StrContains(t, instruction, handoffTemplateMarker)
	assert.NotContains(t, instruction, "# 待压缩的对话")
}
