package personal

import (
	"context"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// TestRecordProtectedNotice: a session whose turn starts on changed protected
// settings writes one config event with the changed key names, and only the
// sessions the source names.
func TestRecordProtectedNotice(t *testing.T) {
	t.Run("the notice is recorded once per session", func(t *testing.T) {
		repo := openObserveRepo(t)
		asked := map[string]bool{}
		runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(mock.Final("ok"), mock.Final("ok"), mock.Final("ok")),
			ProtectedNotice: func(sessionID string) ([]string, bool, bool, bool) {
				if asked[sessionID] {
					return nil, false, false, false
				}
				asked[sessionID] = true
				return []string{"tools.read_roots"}, false, false, true
			}})
		assert.Require(t, assert.NoErr(t, err))
		ctx := context.Background()

		first, err := runner.Run(ctx, RunRequest{Prompt: "hi"})
		assert.Require(t, assert.NoErr(t, err))
		events := eventsOf(t, repo, first.SessionID, store.EventConfig)
		assert.Require(t, assert.Eq(t, 1, len(events)))
		assert.Eq(t, "protected_config_changed", events[0].Name)
		assert.Eq(t, []any{"tools.read_roots"}, events[0].Attrs["keys"])

		// The second turn of the same session is not told again.
		_, err = runner.Run(ctx, RunRequest{SessionID: first.SessionID, Prompt: "again"})
		assert.NoErr(t, err)
		assert.Eq(t, 1, len(eventsOf(t, repo, first.SessionID, store.EventConfig)))

		// Another session is.
		other, err := runner.Run(ctx, RunRequest{Prompt: "other"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, len(eventsOf(t, repo, other.SessionID, store.EventConfig)))
	})

	t.Run("whole-file and unreadable changes carry their own attrs", func(t *testing.T) {
		cases := map[string]struct {
			keys              []string
			wholeFile, unread bool
			attrs             map[string]any
		}{
			"whole file": {wholeFile: true, attrs: map[string]any{"whole_file": true}},
			"unreadable": {keys: []string{"tools.read_deny"}, unread: true,
				attrs: map[string]any{"keys": []any{"tools.read_deny"}, "snapshot_unreadable": true}},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				repo := openObserveRepo(t)
				runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(mock.Final("ok")),
					ProtectedNotice: func(string) ([]string, bool, bool, bool) {
						return tc.keys, tc.wholeFile, tc.unread, true
					}})
				assert.Require(t, assert.NoErr(t, err))
				res, err := runner.Run(context.Background(), RunRequest{Prompt: "hi"})
				assert.Require(t, assert.NoErr(t, err))
				events := eventsOf(t, repo, res.SessionID, store.EventConfig)
				assert.Require(t, assert.Eq(t, 1, len(events)))
				assert.Eq(t, tc.attrs, events[0].Attrs)
			})
		}
	})

	t.Run("no source records nothing", func(t *testing.T) {
		repo := openObserveRepo(t)
		// A sub-agent's runner is built without the callback.
		runner, err := New(Config{Repository: repo, Provider: mock.NewProvider(mock.Final("ok"))})
		assert.Require(t, assert.NoErr(t, err))
		res, err := runner.Run(context.Background(), RunRequest{Prompt: "hi"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, len(eventsOf(t, repo, res.SessionID, store.EventConfig)))
	})
}
