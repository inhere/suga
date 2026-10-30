package sugasdk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

func sessionMemoryCall(t *testing.T, session *Session, args map[string]any) string {
	t.Helper()
	tool, err := session.rt.Registry.Get("memory")
	assert.Require(t, assert.NoErr(t, err))
	result, err := tool.Execute(context.Background(), api.ToolCall{CallID: "memory-test", Name: "memory", Arguments: args})
	assert.Require(t, assert.NoErr(t, err))
	return result.Content
}

func TestProjectMemoryKeyIsolatedThroughSessionAssembly(t *testing.T) {
	ctx := context.Background()
	home := sdkTestHome(t, "http://127.0.0.1:1")
	workspace := filepath.Join(t.TempDir(), "repo", "topic")
	tracker := filepath.Join(filepath.Dir(workspace), ".gofer", "tracker")
	assert.NoErr(t, os.MkdirAll(tracker, 0o700))
	marker := filepath.Join(tracker, "state.jsonl")
	before := []byte("must not be touched\n")
	assert.NoErr(t, os.WriteFile(marker, before, 0o600))
	assert.NoErr(t, os.MkdirAll(filepath.Join(filepath.Dir(workspace), ".git"), 0o700))
	assert.NoErr(t, os.MkdirAll(workspace, 0o700))

	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })
	open := func(key string) *Session {
		s, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: workspace, ProjectMemoryKey: key,
			MemoryConfirm: func(context.Context, MemoryConfirmRequest) bool { return true }})
		assert.Require(t, assert.NoErr(t, err))
		t.Cleanup(func() { _ = s.Close() })
		return s
	}

	alpha := open("topic-alpha")
	beta := open("topic-beta")
	alphaAgain := open("topic-alpha")
	args := map[string]any{"action": "add", "scope": "project", "kind": "user", "title": "Topic preference", "body": "answer with alpha"}
	assert.StrContains(t, sessionMemoryCall(t, alpha, args), "Saved memory")
	assert.StrContains(t, sessionMemoryCall(t, beta, map[string]any{"action": "search", "scope": "project", "query": "alpha"}), "No project memory matched")
	assert.StrContains(t, sessionMemoryCall(t, alphaAgain, map[string]any{"action": "search", "scope": "project", "query": "alpha"}), "alpha")

	got, err := os.ReadFile(marker)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, string(before), string(got))
	_, err = os.Stat(filepath.Join(home, "projects", "topic-alpha", "memory"))
	assert.NoErr(t, err)
	_, err = os.Stat(filepath.Join(home, "projects", "topic-beta", "memory"))
	assert.True(t, os.IsNotExist(err))
	assert.True(t, !strings.Contains(string(got), "alpha"))
}

func TestProjectMemoryKeyValidation(t *testing.T) {
	ctx := context.Background()
	home := sdkTestHome(t, "http://127.0.0.1:1")
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })
	for _, key := range []string{".", "..", "bad/key", "空", strings.Repeat("a", 97)} {
		t.Run(key, func(t *testing.T) {
			_, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir(), ProjectMemoryKey: key})
			assert.Err(t, err)
			assert.StrContains(t, err.Error(), "invalid ProjectMemoryKey")
		})
	}
}
