package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// modelRequest is what a fake provider keeps of one model request: the tool
// table as JSON and the message contents in order.
type modelRequest struct {
	tools    string
	messages []string
}

// TestCompactCommandContinuesTheTurnsPrefix covers the root cause of H4: a
// segment break between turns runs in a fresh process (`suga session compact`),
// which has to declare the same tool table as the turn whose request the handoff
// package continues. The table depends on wiring facts the turn process had and
// the compact process used to lack - the run mode (write tools) and the
// background-task tool every turn process registers - so the rebuild differed
// from the persisted fingerprint and every break fell back (prefix_changed).
func TestCompactCommandContinuesTheTurnsPrefix(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", "")
	workspace := t.TempDir()
	t.Chdir(workspace)
	_ = gkconfig.Set("workspace", workspace)
	_ = gkconfig.Set("model", "openai/gpt-4o")
	// The reuse path is off by default since the 2026-10-27 evaluation.
	_ = gkconfig.Set("context.handoff_reuse_prefix", true)

	var mu sync.Mutex
	var requests []modelRequest
	// completePackage is an answer the reuse path accepts, so the break makes one
	// model call and the old path never asks again.
	const completePackage = "## 当前任务\n无\n## 坐标\n无\n## 采纳与否决\n无\n## 验证到哪\n无\n" +
		"## 进展与阻塞\n无\n## 下一步\n无\n## 与当前任务相关的引用\n无"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools    json.RawMessage `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		req := modelRequest{tools: string(body.Tools)}
		for _, m := range body.Messages {
			req.messages = append(req.messages, string(m.Content))
		}
		mu.Lock()
		requests = append(requests, req)
		n := len(requests)
		mu.Unlock()
		content := completePackage
		if n == 1 {
			content = "reply-1"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + quoteJSON(content) + `},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)
	ctx := context.Background()

	// The turn runs in write mode, so its tool table holds the write tools and
	// the background-task tool.
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, RunPersonalCLI(ctx, "run", []string{"--prompt", "remember the plan", "--session-id", "reuse-cli",
		"--allow-write", "--output-format", "stream-json"}, strings.NewReader(""), &stdout, &stderr))
	stdout.Reset()
	stderr.Reset()
	assert.NoErr(t, newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"session", "compact", "reuse-cli", "--json"}))

	mu.Lock()
	got := append([]modelRequest(nil), requests...)
	mu.Unlock()
	assert.Require(t, assert.True(t, len(got) >= 2))
	turn, compact := got[0], got[1]
	// The tool table is part of the request head: the break continues the turn's
	// request only if it declares the same tools.
	assert.Eq(t, turn.tools, compact.tools)
	// ... and the messages of the turn are a prefix of the break's request.
	assert.Require(t, assert.True(t, len(compact.messages) > len(turn.messages)))
	for i, want := range turn.messages {
		assert.Eq(t, want, compact.messages[i])
	}
	// The old path would render the transcript instead of continuing the prefix.
	assert.False(t, strings.Contains(strings.Join(compact.messages, "\n"), "# 待压缩的对话"))
}

// quoteJSON renders s as a JSON string literal (a fake provider answer).
func quoteJSON(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
