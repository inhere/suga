package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
)

// sessionIDLine matches the "session id: <id>" line run prints on stderr in
// text mode.
var sessionIDLine = regexp.MustCompile(`session id: (\S+)`)

// A provider that keeps failing ends the run once retry.max_elapsed is spent,
// and the session is free for the next run (issue suga-255e): the process does
// not hold the session lock for minutes of retries.
func TestRunFailsFastWhenTheProviderKeepsFailing(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", "") // fall back to HOME/.suga despite the TestMain default
	workspace := t.TempDir()
	t.Chdir(workspace)
	_ = gkconfig.Set("workspace", workspace)
	_ = gkconfig.Set("model", "openai/gpt-4o")
	assert.NoErr(t, gkconfig.Set("retry", map[string]any{
		"max_attempts": 50, "base_delay": "10ms", "max_delay": "10ms", "max_elapsed": "200ms",
	}))

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
	}))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)

	start := time.Now()
	var stdout, stderr bytes.Buffer
	err := RunPersonalCLI(context.Background(), "run", []string{"--prompt", "hi", "--read-only"},
		strings.NewReader(""), &stdout, &stderr)
	elapsed := time.Since(start)
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), "request failed after")
	assert.True(t, elapsed < 10*time.Second, "the run took %s", elapsed)
	assert.True(t, int(hits.Load()) < 50, "%d requests made", hits.Load())

	// The session is released: a resumed run starts another turn (which fails
	// the same way) instead of reporting the session as locked.
	m := sessionIDLine.FindStringSubmatch(stderr.String())
	assert.Require(t, assert.True(t, len(m) == 2), "no session id in %q", stderr.String())
	var resumeOut, resumeErr bytes.Buffer
	err = RunPersonalCLI(context.Background(), "run", []string{"--prompt", "again", "--read-only", "--resume", m[1]},
		strings.NewReader(""), &resumeOut, &resumeErr)
	assert.Require(t, assert.Err(t, err))
	assert.NotContains(t, err.Error(), "session is locked")
}
