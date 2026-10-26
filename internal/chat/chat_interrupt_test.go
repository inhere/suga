package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
)

// blockingProv holds the first model call until the test cancels the context:
// what Ctrl-C during a turn looks like to the chat.
type blockingProv struct {
	once    sync.Once
	started chan struct{}
}

func (p *blockingProv) Name() string { return "block" }

func (p *blockingProv) Generate(ctx context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestPersonalChatInterruptedTurn: Ctrl-C during a turn cancels the chat's
// context. chat ends as before (cmd/suga reports the process-wide 已中断 with
// exit 130) and the turn reads 已中断本轮, never the runner's "context canceled".
func TestPersonalChatInterruptedTurn(t *testing.T) {
	provider := &blockingProv{started: make(chan struct{})}
	runner, repo := newCLITestRunner(t, provider)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, runner, Options{}, strings.NewReader("干点活\n"), &stdout, &stderr, nil)
	}()
	<-provider.started
	cancel()

	assert.ErrIs(t, <-done, context.Canceled) // the exit path and its code are unchanged
	assert.StrContains(t, stdout.String(), "已中断本轮")
	assert.StrNotContains(t, stdout.String()+stderr.String(), "context canceled")

	// The turn itself is still recorded as cancelled.
	sessions, err := repo.ListSessions(context.Background())
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Eq(t, 1, len(sessions)))
	turns, err := repo.ListTurns(context.Background(), sessions[0].ID, store.TurnCancelled)
	assert.NoErr(t, err)
	assert.Eq(t, 1, len(turns))
}

// TestPersonalChatCancelledRunKeepsGoing: a turn cancelled while the chat itself
// lives on (a cancelled model call) reports 已中断本轮 and chat takes the next
// prompt instead of printing an "Error: context canceled" line.
func TestPersonalChatCancelledRunKeepsGoing(t *testing.T) {
	runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Error(context.Canceled), mock.Final("下一轮的答复")))
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, Run(context.Background(), runner, Options{},
		strings.NewReader("第一轮\n第二轮\n:quit\n"), &stdout, &stderr, nil))

	assert.StrContains(t, stdout.String(), "已中断本轮")
	assert.StrNotContains(t, stdout.String()+stderr.String(), "context canceled")
	assert.StrContains(t, stdout.String(), "下一轮的答复") // the chat went on
}

// TestFormatCommandError: an interrupted :command reads 已中断 instead of the
// runner's raw "context canceled"; every other failure keeps its text.
func TestFormatCommandError(t *testing.T) {
	t.Run("an interrupted command reads 已中断", func(t *testing.T) {
		assert.Eq(t, "Compact: 已中断", formatCommandError(nil, "Compact", context.Canceled))
	})
	t.Run("a wrapped cancellation is recognized too", func(t *testing.T) {
		assert.Eq(t, "Compact: 已中断", formatCommandError(nil, "Compact", fmt.Errorf("segment break: %w", context.Canceled)))
	})
	t.Run("another failure keeps its text", func(t *testing.T) {
		assert.Eq(t, "Compact: boom", formatCommandError(nil, "Compact", errors.New("boom")))
	})
}
