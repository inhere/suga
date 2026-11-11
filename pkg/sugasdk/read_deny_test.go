package sugasdk

// SessionOptions.ReadDeny: a host's patterns join the built-in credential list
// (tools.read_deny), "~" is expanded, and the model's file_read of a listed
// file is refused whatever the host's Approver answers.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// readProbe runs one session in which the model calls file_read on path and
// returns the second model request, the one that carries the tool result.
func readProbe(t *testing.T, opts SessionOptions, path string) (string, error) {
	t.Helper()
	ctx := context.Background()
	script := scriptedProvider(t, []struct {
		name string
		args map[string]any
	}{{"file_read", map[string]any{"path": path}}})
	// scriptedProvider does not keep the bodies, so a recording proxy wraps it.
	var mu sync.Mutex
	var bodies []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		req, _ := http.NewRequest(r.Method, script.URL+r.URL.Path, strings.NewReader(string(raw)))
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)
	home := sdkTestHome(t, proxy.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })
	opts.Approver = allowAll{}
	session, err := engine.OpenSession(ctx, "", opts)
	if err != nil {
		return "", err
	}
	t.Cleanup(func() { _ = session.Close() })
	_, err = session.Send(ctx, Prompt{Text: "read it"}, nil)
	assert.Require(t, assert.NoErr(t, err))
	mu.Lock()
	defer mu.Unlock()
	assert.Require(t, assert.True(t, len(bodies) >= 2))
	return bodies[1], nil
}

func TestSessionReadDeny(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ws := filepath.Join(home, "proj")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(ws, 0o755)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, ".env"), []byte("TOKEN=sekret-sdk"), 0o600)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, "vault.txt"), []byte("private-sdk"), 0o600)))
	assert.Require(t, assert.NoErr(t, os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("fine-sdk"), 0o600)))

	t.Run("built-in list holds without any option", func(t *testing.T) {
		body, err := readProbe(t, SessionOptions{Workspace: ws}, ".env")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, body, "tools.read_deny")
		assert.NotContains(t, body, "sekret-sdk")
	})
	t.Run("ReadDeny appends and expands ~", func(t *testing.T) {
		body, err := readProbe(t, SessionOptions{Workspace: ws, ReadDeny: []string{"~/proj/vault.txt"}}, "vault.txt")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, body, "tools.read_deny")
		assert.NotContains(t, body, "private-sdk")
	})
	t.Run("ReadDeny leaves the built-in entries in place", func(t *testing.T) {
		body, err := readProbe(t, SessionOptions{Workspace: ws, ReadDeny: []string{"~/proj/vault.txt"}}, ".env")
		assert.Require(t, assert.NoErr(t, err))
		assert.NotContains(t, body, "sekret-sdk")
	})
	t.Run("without the addition the file reads", func(t *testing.T) {
		body, err := readProbe(t, SessionOptions{Workspace: ws}, "ok.txt")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, body, "fine-sdk")
	})
	t.Run("an exception fails OpenSession", func(t *testing.T) {
		_, err := readProbe(t, SessionOptions{Workspace: ws, ReadDeny: []string{"!**/.env"}}, ".env")
		assert.Err(t, err)
	})
	t.Run("a bad glob fails OpenSession", func(t *testing.T) {
		_, err := readProbe(t, SessionOptions{Workspace: ws, ReadDeny: []string{"**/[x"}}, ".env")
		assert.Err(t, err)
	})
}
