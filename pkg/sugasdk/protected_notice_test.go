package sugasdk

// The startup comparison of the protected settings reaches an SDK session: its
// first turn records one kind=config event, and another session of the same
// engine process records its own (the deduplication is per session).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// protectedTestHome writes a config file whose protected settings changed since
// the user last confirmed them: the snapshot is the digest of the same file with
// another tools.disabled list, so exactly that key is reported.
func protectedTestHome(t *testing.T, baseURL string) string {
	t.Helper()
	gkconfig.Reset()
	appconfig.ResetProtectedStateForTest()
	appconfig.ResetLoadedStateForTest()
	t.Cleanup(func() {
		gkconfig.Reset()
		appconfig.ResetProtectedStateForTest()
		appconfig.ResetLoadedStateForTest()
	})
	home := t.TempDir()
	config := func(disabled string) string {
		return "model: openai/gpt-4o\ntools:\n  disabled: [" + disabled + "]\nproviders:\n  openai:\n    type: openai\n" +
			"    api_key: local-test-key\n    base_url: " + baseURL + "\n"
	}
	digest, err := appconfig.DigestProtected([]byte(config("file_list")))
	assert.Require(t, assert.NoErr(t, err))
	payload, err := json.Marshal(struct {
		Version int               `json:"version"`
		Mode    string            `json:"mode"`
		Keys    map[string]string `json:"keys"`
		File    string            `json:"file"`
	}{1, digest.Mode, digest.Keys, digest.File})
	assert.Require(t, assert.NoErr(t, err))
	snapshotDir := filepath.Join(home, "run", "protected-config")
	assert.NoErr(t, os.MkdirAll(snapshotDir, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(snapshotDir, "default.json"), payload, 0o600))
	// The file the engine loads: one more tool disabled than the confirmed one.
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config("file_list, file_grep")), 0o600))
	t.Setenv("SUGA_HOME", home)
	return home
}

// TestSessionProtectedNoticeEvent: every SDK session that starts on a changed
// protected configuration gets one config event, once.
func TestSessionProtectedNoticeEvent(t *testing.T) {
	ctx := context.Background()
	var requests int32
	server := plainChatServer(t, "answer", &requests, nil)
	home := protectedTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })
	repo := openTestStore(t)

	for _, name := range []string{"first", "second"} {
		t.Run(name+" session records one event", func(t *testing.T) {
			session, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir()})
			assert.Require(t, assert.NoErr(t, err))
			t.Cleanup(func() { _ = session.Close() })
			for i := 0; i < 2; i++ {
				_, err = session.Send(ctx, Prompt{Text: "hello"}, nil)
				assert.Require(t, assert.NoErr(t, err))
			}
			events, err := repo.ListEvents(ctx, store.EventFilter{SessionID: session.ID(), Kinds: []string{store.EventConfig}})
			assert.Require(t, assert.NoErr(t, err))
			assert.Require(t, assert.Eq(t, 1, len(events)))
			assert.Eq(t, "protected_config_changed", events[0].Name)
			assert.Eq(t, []any{"tools.disabled"}, events[0].Attrs["keys"])
		})
	}
}
