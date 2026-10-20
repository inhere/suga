package acpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/testutil"
)

// seedListSessions creates the sessions a list test needs, through the real
// SUGA_HOME store: `here` sessions in the current directory, one in another
// directory, one archived, one sub-agent (under a parent here) and one without
// a recorded cwd. Returns the ids of the sessions the default view shows.
func seedListSessions(t *testing.T) (here []string) {
	t.Helper()
	// A fresh store per test/subtest: sessions belongs to one SUGA_HOME.
	t.Setenv("SUGA_HOME", testutil.Dir(t))
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer repo.Close()
	ctx := context.Background()
	wd, err := os.Getwd()
	assert.Require(t, assert.NoErr(t, err))
	wd = filepath.ToSlash(wd)
	at := time.Now()
	create := func(id, cwd string, status string, meta map[string]any) {
		s := store.Session{ID: id, Title: "task " + id, Cwd: cwd, Status: status, Meta: meta, CreatedAt: at, UpdatedAt: at}
		assert.NoErr(t, repo.CreateSession(ctx, s))
	}
	create("h1", wd, store.SessionOpen, nil)
	create("h2", wd, store.SessionOpen, nil)
	create("other", "D:/somewhere/else", store.SessionOpen, nil)
	create("arch", wd, store.SessionArchived, nil)
	create("agent", wd, store.SessionOpen, map[string]any{store.MetaKind: store.KindAgent, store.MetaParentSession: "h1"})
	// Sessions without a recorded cwd stay out of the default view too.
	assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: "nocwd", Title: "legacy"}))
	return []string{"h1", "h2"}
}

func TestACPListSessions(t *testing.T) {
	t.Run("default view: the current directory only, with otherDirs and the hint", func(t *testing.T) {
		t.Chdir(t.TempDir()) // nothing recorded for this directory yet
		here := seedListSessions(t)
		ctx := context.Background()
		conn := connectPersonalACP(t, testACPRuntime(t, mock.NewProvider(mock.Final("x")), nil), &testACPClient{})
		resp, err := conn.ListSessions(ctx, acp.ListSessionsRequest{})
		assert.Require(t, assert.NoErr(t, err))
		ids := make([]string, 0, len(resp.Sessions))
		for _, s := range resp.Sessions {
			ids = append(ids, string(s.SessionId))
		}
		// The cursor sorts by (updated_at, id); both sessions share one stamp,
		// so either order is a correct newest-first reply.
		assert.Eq(t, true, (ids[0] == "h1" && ids[1] == "h2") || (ids[0] == "h2" && ids[1] == "h1"))
		assert.Eq(t, true, resp.Meta["suga/otherDirs"].(float64) > 0)
		_, hasHint := resp.Meta["suga/hint"]
		assert.Eq(t, false, hasHint) // not empty: the hint stays out
		// From a directory nothing is recorded for the result is empty and the
		// hint names the way out; suga/all lists everything non-agent non-archived.
		resp, err = conn.ListSessions(ctx, acp.ListSessionsRequest{Cwd: strPtr("D:/elsewhere/nowhere")})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 0, len(resp.Sessions))
		assert.StrContains(t, resp.Meta["suga/hint"].(string), "suga sessions --all")
		resp, err = conn.ListSessions(ctx, acp.ListSessionsRequest{Meta: map[string]any{"suga/all": true}})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 4, len(resp.Sessions)) // h1, h2, other, nocwd — agent and archived stay hidden
		// here keeps the ids of the current-directory sessions on this store.
		_ = here
	})
	t.Run("request cwd filters, archived and agent sessions stay out", func(t *testing.T) {
		wd, err := os.Getwd()
		assert.Require(t, assert.NoErr(t, err))
		seedListSessions(t)
		conn := connectPersonalACP(t, testACPRuntime(t, mock.NewProvider(mock.Final("x")), nil), &testACPClient{})
		resp, err := conn.ListSessions(context.Background(), acp.ListSessionsRequest{Cwd: strPtr(wd)})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 2, len(resp.Sessions))
	})
	t.Run("pagination: cursor splits a long list", func(t *testing.T) {
		t.Chdir(t.TempDir())
		dbPath, err := appconfig.ResolveCoreStorePath()
		assert.Require(t, assert.NoErr(t, err))
		repo, err := store.Open(dbPath)
		assert.Require(t, assert.NoErr(t, err))
		ctx := context.Background()
		wd, err := os.Getwd()
		assert.Require(t, assert.NoErr(t, err))
		now := time.Now()
		for i := 0; i < 55; i++ {
			at := now.Add(-time.Duration(i) * time.Minute)
			assert.NoErr(t, repo.CreateSession(ctx, store.Session{ID: fmt.Sprintf("p%02d", i), Title: "t", Cwd: wd, CreatedAt: at, UpdatedAt: at}))
		}
		assert.NoErr(t, repo.Close())
		conn := connectPersonalACP(t, testACPRuntime(t, mock.NewProvider(mock.Final("x")), nil), &testACPClient{})
		resp, err := conn.ListSessions(ctx, acp.ListSessionsRequest{})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 50, len(resp.Sessions))
		assert.Require(t, assert.True(t, resp.NextCursor != nil))
		// Newest first: p00 opens the first page, the second holds the rest.
		assert.Eq(t, "p00", string(resp.Sessions[0].SessionId))
		resp2, err := conn.ListSessions(ctx, acp.ListSessionsRequest{Cursor: resp.NextCursor})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 5, len(resp2.Sessions))
		assert.Eq(t, "p50", string(resp2.Sessions[0].SessionId))
		assert.Eq(t, "p54", string(resp2.Sessions[4].SessionId))
		assert.Require(t, assert.True(t, resp2.NextCursor == nil))
	})
}

// fmt keeps the seeds' ids zero-padded (p00..p54).

// replayClient collects the notifications load replays.
type replayClient struct {
	testACPClient
	mu     sync.Mutex
	kinds  []string
	texts  []string
	titles []string
	// armed turns the collector on for one load: everything that arrives
	// while it is set goes into loadReplay (the prompt's own updates excluded).
	armed      bool
	loadReplay []string
}

func (c *replayClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := n.Update
	kind := "other"
	switch {
	case u.UserMessageChunk != nil:
		kind, c.texts = "user", append(c.texts, textOf(u.UserMessageChunk.Content))
	case u.AgentMessageChunk != nil:
		kind, c.texts = "assistant", append(c.texts, textOf(u.AgentMessageChunk.Content))
	case u.AgentThoughtChunk != nil:
		kind, c.texts = "thought", append(c.texts, textOf(u.AgentThoughtChunk.Content))
	case u.ToolCall != nil:
		kind, c.titles = "tool_call", append(c.titles, u.ToolCall.Title)
	case u.ToolCallUpdate != nil:
		kind, c.titles = "tool_call_update", append(c.titles, "")
	}
	c.kinds = append(c.kinds, kind)
	// While armed, everything also lands in loadReplay: the prompt's own turn
	// has finished, so these events are the load's replay (and nothing else).
	if c.armed {
		c.loadReplay = append(c.loadReplay, kind)
	}
	return c.testACPClient.SessionUpdate(nil, n)
}

func textOf(block acp.ContentBlock) string {
	if block.Text != nil {
		return block.Text.Text
	}
	return ""
}
