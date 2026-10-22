package sugasdk

// The process-wide guards of the SDK: one engine per process, one loaded
// SUGA_HOME, one turn per session, and the panic isolation of a turn.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// TestMemoryConfirmAdapter covers the SDK confirmation the runtime sees: an
// unapproved (or missing) confirmation refuses the write.
func TestMemoryConfirmAdapter(t *testing.T) {
	ctx := context.Background()
	scope := memory.ProjectScope("proj")
	op := memory.Op{Action: memory.ActionAdd, Title: "t", Body: "b", Kind: memory.KindProject}

	t.Run("nil confirms nothing", func(t *testing.T) {
		assert.True(t, memoryConfirmAdapter(nil) == nil)
	})
	t.Run("approval passes the request through", func(t *testing.T) {
		var got MemoryConfirmRequest
		confirm := memoryConfirmAdapter(func(_ context.Context, req MemoryConfirmRequest) bool {
			got = req
			return true
		})
		assert.NoErr(t, confirm(ctx, scope, op))
		assert.Eq(t, "project:proj", got.Scope)
		assert.Eq(t, "add", got.Action)
		assert.Eq(t, "t", got.Title)
		assert.Eq(t, "b", got.Body)
	})
	t.Run("a refusal is an error", func(t *testing.T) {
		confirm := memoryConfirmAdapter(func(context.Context, MemoryConfirmRequest) bool { return false })
		assert.Err(t, confirm(ctx, scope, op))
	})
}

func TestSugaHomeGuard(t *testing.T) {
	ctx := context.Background()
	server := chatServer(t, "sdk_echo", "ok", nil)
	home := sdkTestHome(t, server.URL)

	t.Run("the first Open loads the requested root", func(t *testing.T) {
		engine, err := Open(ctx, EngineOptions{SugaHome: home})
		assert.Require(t, assert.NoErr(t, err))
		loaded, ok := appconfig.LoadedHome()
		assert.True(t, ok)
		assert.True(t, personal.SameDir(loaded, home))
		assert.Eq(t, home, engine.Home())
		assert.NoErr(t, engine.Close())
	})
	t.Run("the same root is reused", func(t *testing.T) {
		engine, err := Open(ctx, EngineOptions{SugaHome: home})
		assert.Require(t, assert.NoErr(t, err))
		loaded, _ := appconfig.LoadedHome()
		assert.True(t, personal.SameDir(loaded, home), "the loaded root stays the same")
		assert.NoErr(t, engine.Close())
	})
	t.Run("another root is refused", func(t *testing.T) {
		other := t.TempDir()
		_, err := Open(ctx, EngineOptions{SugaHome: other})
		assert.Require(t, assert.Err(t, err))
		assert.True(t, errors.Is(err, ErrSugaHomeMismatch), "want ErrSugaHomeMismatch, got %v", err)
		assert.StrContains(t, err.Error(), "SUGA_HOME")
		// The refused Open leaves no engine behind.
		engine, err := Open(ctx, EngineOptions{SugaHome: home})
		assert.Require(t, assert.NoErr(t, err))
		assert.NoErr(t, engine.Close())
	})
}

func TestOneEnginePerProcess(t *testing.T) {
	ctx := context.Background()
	server := chatServer(t, "sdk_echo", "ok", nil)
	home := sdkTestHome(t, server.URL)

	first, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	_, err = Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.Err(t, err))
	assert.True(t, errors.Is(err, ErrEngineOpen), "want ErrEngineOpen, got %v", err)

	assert.NoErr(t, first.Close())
	second, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err), "a closed engine can be reopened")
	assert.NoErr(t, second.Close())

	// A closed engine and its sessions refuse further use.
	_, err = second.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir()})
	assert.True(t, errors.Is(err, ErrEngineClosed), "want ErrEngineClosed, got %v", err)
	_, err = second.Sessions(ctx, SessionFilter{})
	assert.True(t, errors.Is(err, ErrEngineClosed), "want ErrEngineClosed, got %v", err)
	_, err = second.Search(ctx, "anything", 5)
	assert.True(t, errors.Is(err, ErrEngineClosed), "want ErrEngineClosed, got %v", err)
}

// slowServer answers the first request only after release is closed, and every
// later one with finalText; requests counts the model calls.
func slowServer(t *testing.T, release <-chan struct{}, requests *int32, bodies *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		*bodies = append(*bodies, string(raw))
		mu.Unlock()
		if atomic.AddInt32(requests, 1) == 1 {
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSendIsSingleFlight(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	var requests int32
	var bodies []string
	server := slowServer(t, release, &requests, &bodies)
	home := sdkTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	defer engine.Close()

	t.Run("Steer without a running turn is refused", func(t *testing.T) {
		session, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
		assert.Require(t, assert.NoErr(t, err))
		defer session.Close()
		err = session.Steer("hello")
		assert.Require(t, assert.Err(t, err))
		assert.True(t, errors.Is(err, ErrNoRunningTurn), "want ErrNoRunningTurn, got %v", err)
	})

	session, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
	assert.Require(t, assert.NoErr(t, err))
	defer session.Close()

	done := make(chan Result, 1)
	go func() {
		result, _ := session.Send(ctx, Prompt{Text: "first"}, nil)
		done <- result
	}()
	// Wait until the model request is in flight: the turn's inbox is open then.
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&requests) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the turn never reached the provider")
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Run("a second Send while one runs is busy instead of queueing", func(t *testing.T) {
		_, err := session.Send(ctx, Prompt{Text: "second"}, nil)
		assert.Require(t, assert.Err(t, err))
		assert.True(t, errors.Is(err, ErrBusy), "want ErrBusy, got %v", err)
	})
	t.Run("Steer reaches the running turn", func(t *testing.T) {
		assert.NoErr(t, session.Steer("also mention the tests"))
	})
	close(release)
	first := <-done

	// The turn's only model request was already out, so nothing delivered the
	// steer: the turn hands it back for the caller's next turn.
	assert.Require(t, assert.Len(t, first.ReturnedSteers, 1))
	assert.Eq(t, "also mention the tests", first.ReturnedSteers[0])
}

// panickingHook is a host hook that panics before the run: the SDK recovers it
// like any other panic of a turn.
type panickingHook struct{ agentcore.NopHook }

func (panickingHook) BeforeRun(context.Context, *agentcore.RunEvent, *agentcore.Control) error {
	panic("hook exploded")
}

// TestSendCancelsWithContext covers the turn following its caller's context:
// a cancelled Send ends the turn instead of waiting for the provider.
func TestSendCancelsWithContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var requests int32
	server := slowServer(t, release, &requests, &[]string{})
	home := sdkTestHome(t, server.URL)
	engine, err := Open(context.Background(), EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	defer engine.Close()

	session, err := engine.OpenSession(context.Background(), "", SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
	assert.Require(t, assert.NoErr(t, err))
	defer session.Close()

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := session.Send(ctx, Prompt{Text: "slow"}, nil)
		done <- outcome{result: result, err: err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt32(&requests) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the turn never reached the provider")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case got := <-done:
		assert.Require(t, assert.Err(t, got.err))
		assert.True(t, errors.Is(got.err, context.Canceled), "want context.Canceled, got %v", got.err)
		assert.Eq(t, "CANCELED", got.result.State)
	case <-time.After(15 * time.Second):
		t.Fatal("a cancelled turn did not end")
	}
}

func TestSendRecoversPanic(t *testing.T) {
	ctx := context.Background()
	server := chatServer(t, "sdk_echo", "fine", nil)
	home := sdkTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	defer engine.Close()

	session, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir(), Approver: allowAll{}})
	assert.Require(t, assert.NoErr(t, err))
	defer session.Close()

	t.Run("a panic in the event callback becomes an error", func(t *testing.T) {
		result, err := session.Send(ctx, Prompt{Text: "hello"}, func(Event) { panic("host callback exploded") })
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "panicked")
		assert.StrContains(t, err.Error(), "host callback exploded")
		assert.Eq(t, string(personal.RunFailed), result.State)
		assert.Eq(t, session.ID(), result.SessionID)
	})
	t.Run("the session still runs the next turn", func(t *testing.T) {
		result, err := session.Send(ctx, Prompt{Text: "again"}, nil)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "COMPLETED", result.State)
		assert.Eq(t, "fine", result.Output)
	})
	t.Run("a panic in a host hook becomes an error too", func(t *testing.T) {
		panicking, err := engine.OpenSession(ctx, "", SessionOptions{Workspace: t.TempDir(),
			Approver: allowAll{}, Hooks: []agentcore.Hook{panickingHook{}}})
		assert.Require(t, assert.NoErr(t, err))
		defer panicking.Close()
		result, err := panicking.Send(ctx, Prompt{Text: "hello"}, nil)
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "hook exploded")
		assert.Eq(t, string(personal.RunFailed), result.State)
	})
}
