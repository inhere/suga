package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// TestCompactCommandStartsNewSegment covers the CLI form of chat's :compact:
// a turn, then `suga compact`, then a turn whose context is the new segment
// (the summary instead of the dropped messages).
func TestCompactCommandStartsNewSegment(t *testing.T) {
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
	// The second request is the handoff summary: it must see the dropped request.
	var calls atomic.Int32
	var summaryRequested, nextRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		n := calls.Add(1)
		content := fmt.Sprintf("reply-%d", n)
		if n == 2 {
			content = "SUMMARY-of-segment"
			for _, message := range body.Messages {
				summaryRequested += message.Content
			}
		}
		if n == 3 {
			for _, message := range body.Messages {
				nextRequest += message.Content + "\n"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, content)
	}))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)
	ctx := context.Background()

	var stdout, stderr bytes.Buffer
	assert.NoErr(t, RunPersonalCLI(ctx, "run", []string{"--prompt", "remember the plan", "--session-id", "compact-cli", "--output-format", "stream-json"},
		strings.NewReader(""), &stdout, &stderr))
	assert.StrContains(t, stdout.String(), `"result":"reply-1"`)

	stdout.Reset()
	stderr.Reset()
	assert.NoErr(t, newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"compact", "compact-cli", "--json"}))
	var rep compactReport
	assert.NoErr(t, json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &rep))
	assert.Eq(t, "compact-cli", rep.SessionID)
	assert.Eq(t, true, rep.TokensBefore > 0)
	assert.StrContains(t, summaryRequested, "remember the plan")

	// The next turn starts from the new segment: its context holds the summary.
	var nextOut, nextErr bytes.Buffer
	assert.NoErr(t, RunPersonalCLI(ctx, "run", []string{"--prompt", "next", "--resume", "compact-cli", "--output-format", "stream-json"},
		strings.NewReader(""), &nextOut, &nextErr))
	assert.StrContains(t, nextRequest, "SUMMARY-of-segment")

	t.Run("text report", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		assert.NoErr(t, newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"compact", "compact-cli"}))
		assert.StrContains(t, stdout.String(), "Compacted context: ~")
	})
	t.Run("unknown session", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"compact", "no-such-session"})
		assert.Err(t, err)
	})
	t.Run("session id required", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		err := newCLI(ctx, strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"compact"})
		assert.Err(t, err)
	})
}
