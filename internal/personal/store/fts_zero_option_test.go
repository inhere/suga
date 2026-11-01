package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// zeroOptionFixture is a fixed ledger for the check that EntrySearch with a zero
// ExcludeSession (what sugasdk engine.go and the CLI pass) behaves as it did
// before the option existed.
func zeroOptionFixture(t *testing.T) (*Store, string) {
	s := openTemp(t)
	root := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b"} {
		now := base
		assert.Require(t, assert.NoErr(t, s.CreateSession(context.Background(), Session{ID: id, Cwd: filepath.Join(root, id), CreatedAt: now, UpdatedAt: now})))
	}
	appendAll(t, s, "a", user(t, "alpha needle one"), assistant(t, "reply needle two"), user(t, "unrelated"),
		user(t, "alpha needle three"), assistant(t, "ok"))
	appendAll(t, s, "b", user(t, "beta needle four"), assistant(t, "needle five answer"))
	return s, root
}

// zeroOptionHits renders every hit of the fixed queries, snippets included.
func zeroOptionHits(t *testing.T, s *Store, root string) string {
	var sb strings.Builder
	for _, q := range []EntrySearch{
		{Query: "needle"},
		{Query: "needle", CurrentSessionID: "a"},
		{Query: "needle", WorkspaceDir: root, CurrentSessionID: "b"},
		{Query: "ne", Kinds: []string{EntryUser}},
	} {
		res, err := s.SearchEntriesPage(context.Background(), q)
		assert.Require(t, assert.NoErr(t, err))
		for _, h := range res.Hits {
			fmt.Fprintf(&sb, "%s/%d/%s/%s;", h.SessionID, h.Seq, h.Role, h.Snippet)
		}
		sb.WriteString("|")
	}
	return sb.String()
}

// Golden values were captured from the code before ExcludeSession existed.
func TestSearchEntriesPageZeroExcludeSessionUnchanged(t *testing.T) {
	s, root := zeroOptionFixture(t)
	t.Run("hits", func(t *testing.T) {
		want := "a/1/user/alpha needle one;a/2/assistant/reply needle two;b/1/user/beta needle four;a/4/user/alpha needle three;b/2/assistant/needle five answer;|" +
			"a/1/user/alpha needle one;a/2/assistant/reply needle two;a/4/user/alpha needle three;|" +
			"a/1/user/alpha needle one;a/2/assistant/reply needle two;b/1/user/beta needle four;a/4/user/alpha needle three;b/2/assistant/needle five answer;|" +
			"b/1/user/beta needle four;a/4/user/alpha needle three;a/1/user/alpha needle one;|"
		assert.Eq(t, want, zeroOptionHits(t, s, root))
	})
	t.Run("fingerprint", func(t *testing.T) {
		fp, err := fingerprintEntrySearch(EntrySearch{Query: "needle  one", CurrentSessionID: "a", Kinds: []string{EntryUser}, Limit: 5})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, searchFingerprint("219acc69600d726603f06d2522b66062ba1588bd9ba044035992e87187dd5038"), fp)
	})
	t.Run("an empty exclusion is the zero option", func(t *testing.T) {
		q := EntrySearch{Query: "needle", ExcludeSession: ExcludeSession{SessionID: "a"}}
		got, err := s.SearchEntriesPage(context.Background(), q)
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, got.Hits, 5)
	})
}
