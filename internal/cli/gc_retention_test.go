package cli

import (
	"context"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// sessionTimeLayout is the timestamp format the store writes (store.timeLayout):
// a test that ages a session after writing to it has to use the same one.
const sessionTimeLayout = "2006-01-02T15:04:05.000000000Z"

// TestGCRetentionCommand: `suga data gc --retention` reports one line per origin
// class (sessions, entries, estimated bytes), removes nothing without --apply,
// and refuses to mix the two selection rules of the command.
func TestGCRetentionCommand(t *testing.T) {
	home := shortCLIHome(t)
	t.Setenv("SUGA_HOME", home)
	t.Setenv("SUGA_HOOKS", "off")
	ctx := context.Background()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))

	day := 24 * time.Hour
	now := time.Now()
	mk := func(id string, age time.Duration, meta map[string]any) {
		at := now.Add(-age)
		assert.Require(t, assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: id, Title: id, CreatedAt: at, UpdatedAt: at, Meta: meta})))
		_, err := repo.AppendEntries(ctx, id, store.NewEntry{Type: store.EntryUser, JSON: []byte(`{"Role":"user","Content":"hello ` + id + `"}`)})
		assert.Require(t, assert.NoErr(t, err))
		// AppendEntries bumps updated_at to now; age the session again.
		_, err = repo.DB().ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatSessionTime(at), id)
		assert.Require(t, assert.NoErr(t, err))
	}
	mk("gc-chat-old", 200*day, map[string]any{store.MetaOrigin: store.OriginChat})
	mk("gc-historical", 200*day, nil)
	mk("gc-run-old", 40*day, map[string]any{store.MetaOrigin: store.OriginRun})
	mk("gc-chat-new", 10*day, map[string]any{store.MetaOrigin: store.OriginChat})
	mk("gc-run-new", 10*day, map[string]any{store.MetaOrigin: store.OriginRun})
	mk("gc-sdk-old", 400*day, map[string]any{store.MetaOrigin: store.OriginSDK})
	assert.Require(t, assert.NoErr(t, repo.Close()))

	exists := func(id string) bool {
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		defer repo.Close()
		_, err = repo.GetSession(ctx, id)
		return err == nil
	}

	t.Run("dry run reports every class and writes nothing", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "gc", "--retention")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "交互会话（保留 180d）：2 个会话")
		assert.StrContains(t, out, "非交互会话（保留 30d）：2 个会话")
		assert.StrContains(t, out, "将删除 4 个会话")
		assert.StrContains(t, out, "加 --apply 执行")
		assert.True(t, exists("gc-chat-old"))
		assert.True(t, exists("gc-run-old"))
		assert.True(t, exists("gc-sdk-old"))
	})

	t.Run("both selection rules at once are refused", func(t *testing.T) {
		_, err := runCLI(t, ctx, "data", "gc", "--retention", "--older-than", "30d")
		assert.ErrMsgContains(t, err, "--retention and --older-than")
	})

	t.Run("apply removes the expired classes only", func(t *testing.T) {
		out, err := runCLI(t, ctx, "data", "gc", "--retention", "--apply")
		assert.NoErr(t, err)
		assert.StrContains(t, out, "已删除 4 个会话")
		// Apply reports the deletion, never a VACUUM: the two are separate.
		assert.StrContains(t, out, "VACUUM 未执行")
		assert.False(t, exists("gc-chat-old"))
		assert.False(t, exists("gc-historical"))
		assert.False(t, exists("gc-run-old"))
		assert.True(t, exists("gc-chat-new"))
		assert.True(t, exists("gc-run-new"))
		assert.False(t, exists("gc-sdk-old"))
	})
}

// formatSessionTime renders a time the way the store does.
func formatSessionTime(t time.Time) string { return t.UTC().Format(sessionTimeLayout) }
