package llm

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// A streamed body is the one part of a request no other timeout bounds (see
// timeouts.go): a long answer may stream for minutes, so a deadline would cut a
// healthy stream. A provider that stops sending data in the middle of an answer
// - a hung gateway, an upstream connection the reader never sees die - would
// then hold the turn until the caller cancels it. The idle timeout covers
// exactly that case: the time between two reads of a streamed body, any data
// counted (text deltas, tool arguments, keep-alive comments alike).
//
// The default depends on the request: an ordinary answer is expected to stream
// continuously, while a reasoning model may think for minutes before its first
// visible delta, with nothing but its own keep-alives in between.
const (
	DefaultStreamIdleTimeout          = 120 * time.Second
	DefaultReasoningStreamIdleTimeout = 300 * time.Second
)

// streamIdleError reports a streamed body that stayed silent longer than the
// idle timeout. The request was cancelled at that point; the error is a
// retryableStreamError, so retryStream requests the whole answer again and the
// text of the abandoned attempt is void (restart).
type streamIdleError struct{ timeout time.Duration }

func (e *streamIdleError) Error() string {
	return fmt.Sprintf("stream idle for %s: no data arrived between two chunks", e.timeout)
}

// retryReason implements retryableStreamError: chat and stream-json show it on
// the retry event, so the user learns why nothing happened for a while.
func (e *streamIdleError) retryReason() string { return fmt.Sprintf("stream idle for %s", e.timeout) }

// giveUp implements retryableStreamError: the failure of a stream that stayed
// idle on every attempt of the request.
func (e *streamIdleError) giveUp(provider string, attempts int) error {
	return fmt.Errorf("%s stream idle for %s on all %d attempts: no data arrived within the idle timeout",
		provider, e.timeout, attempts)
}

// streamIdleTimeout resolves the idle bound of one streamed request: a positive
// configured value wins, a negative one (no limit, `off`) disables the bound,
// and 0 takes the default of the request kind (see reasoningRequest).
func streamIdleTimeout(configured time.Duration, body map[string]any) time.Duration {
	switch {
	case configured > 0:
		return configured
	case configured < 0:
		return 0
	case reasoningRequest(body):
		return DefaultReasoningStreamIdleTimeout
	default:
		return DefaultStreamIdleTimeout
	}
}

// reasoningRequest reports whether the request asks the model to think before
// answering (an extended-thinking budget, a reasoning effort or a Responses
// reasoning block): such a request may stream nothing for a long while, so it
// gets the longer default idle timeout.
func reasoningRequest(body map[string]any) bool {
	if thinking, ok := body["thinking"].(map[string]any); ok && len(thinking) > 0 {
		return true
	}
	if _, ok := body["reasoning"]; ok {
		return true
	}
	if effort, ok := body["reasoning_effort"].(string); ok && effort != "" && effort != "none" {
		return true
	}
	return false
}

// idleBody wraps a streamed response body with the idle timeout: every read
// restarts the timer, and a timer that runs out cancels the request (cancel),
// which makes the pending read return. That read then reports streamIdleError
// instead of the cancellation it caused, so the reader reports a retryable
// stream failure rather than a cancelled turn.
type idleBody struct {
	r       io.ReadCloser
	timeout time.Duration
	cancel  context.CancelFunc
	timer   *time.Timer
	fired   atomic.Bool // the timer cancelled the request
}

// withIdleTimeout wraps body with the idle timeout; timeout 0 returns body
// unchanged, i.e. the bound is off. Close stops the timer.
func withIdleTimeout(body io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) io.ReadCloser {
	if timeout <= 0 {
		return body
	}
	b := &idleBody{r: body, timeout: timeout, cancel: cancel}
	b.timer = time.AfterFunc(timeout, func() {
		b.fired.Store(true)
		cancel()
	})
	return b
}

// Read restarts the idle timer around the underlying read. A read that returned
// data is never reported as idle: the timer may fire just as the data arrives,
// and the answer did make progress.
func (b *idleBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.timeout)
	n, err := b.r.Read(p)
	b.timer.Stop()
	if n > 0 || err == nil {
		return n, err
	}
	if b.fired.Load() {
		return 0, &streamIdleError{timeout: b.timeout}
	}
	return n, err
}

// Close stops the idle timer and closes the wrapped body.
func (b *idleBody) Close() error {
	b.timer.Stop()
	return b.r.Close()
}
