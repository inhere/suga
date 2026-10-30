package sugasdk

// Several sessions of one engine run turns at the same time (the xhen shape):
// every turn writes events and entries into the one session database, and a
// panicking host event callback must not fail a tool call (suga-g866).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// toolThenAnswerServer asks for sdk_echo while the request holds no tool result
// yet, and answers once it does: every session gets its own tool round.
func toolThenAnswerServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		const usage = `"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}`
		body := string(raw)
		// Only the part after the newest prompt counts: older turns hold tool results too.
		if !strings.Contains(body[strings.LastIndex(body, "use the tool"):], `"role":"tool"`) {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[{"id":"c1","function":{"name":"sdk_echo","arguments":"{\"text\":\"hello\"}"}}]},"finish_reason":"tool_calls"}],`+usage+`}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],`+usage+`}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestConcurrentSessionsShareOneDatabase(t *testing.T) {
	ctx := context.Background()
	server := toolThenAnswerServer(t)
	home := sdkTestHome(t, server.URL)
	engine, err := Open(ctx, EngineOptions{SugaHome: home})
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = engine.Close() })

	const sessions, turns = 6, 3
	var calls int32
	var wg sync.WaitGroup
	errs := make(chan error, sessions*turns+sessions)
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session, err := engine.OpenSession(ctx, "", SessionOptions{
				Workspace:  t.TempDir(),
				ExtraTools: []api.Tool{echoTool{calls: &calls}},
				Approver:   allowAll{},
			})
			if err != nil {
				errs <- fmt.Errorf("open session %d: %w", i, err)
				return
			}
			defer session.Close()
			for n := 0; n < turns; n++ {
				// A host callback that panics on tool events (its own store was
				// busy): the turn must still complete.
				res, err := session.Send(ctx, Prompt{Text: "use the tool"}, func(ev Event) {
					if ev.Kind == "tool_call" || ev.Kind == "tool_result" {
						panic("host callback: database is locked (5) (SQLITE_BUSY)")
					}
				})
				if err != nil || res.State != "COMPLETED" {
					errs <- fmt.Errorf("session %d turn %d: state %q err %v", i, n, res.State, err)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	assert.Eq(t, int32(sessions*turns), atomic.LoadInt32(&calls))
}
