package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// streamReadError marks a stream that broke while being read: the connection
// dropped mid-stream, or the body ended in the middle of a line (see
// streamTruncatedError). It is a retryableStreamError, so retryStream requests
// the whole answer again and the text of the broken attempt is void. A parse
// error or a permanent API error inside the stream is not retried - the next
// request would only get it again.
type streamReadError struct{ err error }

func (e *streamReadError) Error() string { return e.err.Error() }
func (e *streamReadError) Unwrap() error { return e.err }

// streamBrokenReason is the retry cause of a broken or cut stream; its text is
// part of what chat and stream-json show, and a smoke check asserts on it
// (scripts/ptysmoke/chat_retry.py).
const streamBrokenReason = "stream broken"

// retryReason implements retryableStreamError.
func (e *streamReadError) retryReason() string { return streamBrokenReason }

// giveUp implements retryableStreamError: the failure of a stream that broke on
// every attempt of the request.
func (e *streamReadError) giveUp(provider string, attempts int) error {
	return fmt.Errorf("%s stream broke %d times: %w", provider, attempts, e)
}

// streamTruncatedError names a stream the provider did not finish: the
// connection (a proxy, gateway or load balancer in between included) closed
// while the answer was still streaming. detail says what was seen of it. The
// error wraps io.ErrUnexpectedEOF, so callers can tell a cut stream from a
// transport error; it is a streamReadError, so retryStream requests the whole
// answer again.
func streamTruncatedError(provider, detail string) error {
	return &streamReadError{fmt.Errorf("read %s stream: stream truncated, %s: %w", provider, detail, io.ErrUnexpectedEOF)}
}

// streamBodyError wraps the error of reading an event-stream body: an idle
// stream keeps its own error (streamIdleError, which already says how long it
// was silent), a body that ended early (io.ErrUnexpectedEOF, which a dropped
// chunked connection reports too) is a truncated stream, any other read failure
// is reported as it is.
func streamBodyError(provider string, err error) error {
	var idle *streamIdleError
	if errors.As(err, &idle) {
		return idle
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return streamTruncatedError(provider, "the connection closed before the answer was complete")
	}
	return &streamReadError{fmt.Errorf("read %s stream: %w", provider, err)}
}

// retryableStreamError is a stream failure retryStream requests again from the
// start of the answer: a stream that broke while being read (streamReadError),
// or an API error the server itself reported as temporary (apiTransientError:
// overloaded, rate limited, a server error). retryReason is the cause a
// frontend shows before the wait; giveUp builds the error reported once the
// attempts run out.
type retryableStreamError interface {
	error
	retryReason() string
	giveUp(provider string, attempts int) error
}

// retryStream runs one streamed request (attempt) and repeats it from the
// start when it fails with a retryableStreamError (a broken or cut stream, or a
// temporary API error such as an overload), up to the policy's attempts. Each
// retry, including the HTTP retries inside attempt (postWithRetry), is
// announced to emit as a StreamEventRetry before the wait, so a frontend can
// tell the user why nothing happens. When the broken attempt already emitted
// text the event's metadata has restart=true: the next attempt streams the
// message again from its start. attempt receives the context carrying that
// notifier and emit wrapped to record text deltas.
func retryStream(ctx context.Context, b *backoff, provider string, emit agentcoreapi.StreamFunc,
	attempt func(ctx context.Context, emit agentcoreapi.StreamFunc) (*agentcoreapi.GenerateResult, error)) (*agentcoreapi.GenerateResult, error) {
	sent := false // the current attempt emitted text
	var tracked agentcoreapi.StreamFunc
	if emit != nil {
		tracked = func(ctx context.Context, ev agentcoreapi.StreamEvent) error {
			if ev.Kind == agentcoreapi.StreamEventTextDelta && ev.Delta != "" {
				sent = true
			}
			return emit(ctx, ev)
		}
		ctx = withRetryNotifier(ctx, func(ctx context.Context, attempt int, wait time.Duration, reason string) error {
			return emit(ctx, agentcoreapi.StreamEvent{Kind: agentcoreapi.StreamEventRetry, Delta: reason, Metadata: map[string]any{
				"attempt": attempt, "wait_ms": wait.Milliseconds(), "restart": sent,
			}})
		})
	}
	ctx = ensureRetryBudget(ctx, b)
	for n := 1; ; n++ {
		sent = false
		res, err := attempt(ctx, tracked)
		err = withRequestID(ctx, err)
		var retryErr retryableStreamError
		if err == nil || !errors.As(err, &retryErr) || ctx.Err() != nil {
			return res, err
		}
		// Both kinds of failure wait the same backoff: a Retry-After the server
		// sent was already honoured by postWithRetry (which retries 408/429/5xx
		// before the body is read). The budget (retry.max_elapsed) is checked with
		// that wait included, so a broken provider fails within the configured time
		// instead of retrying its way to minutes.
		wait := b.next(0)
		if n >= b.attempts() || overBudget(ctx, wait) {
			if overBudget(ctx, 0) {
				slog.Warn("llm stream retry budget reached", "provider", provider, "attempt", n, "retry_max_elapsed", b.maxElapsed(), "err", err)
			}
			return nil, withRequestID(ctx, retryErr.giveUp(provider, n))
		}
		reason := retryErr.retryReason()
		slog.Warn("llm stream failed, requesting again", "provider", provider, "attempt", n, "reason", reason, "text_shown", sent, "wait", wait.Round(time.Millisecond), "err", err)
		if err := noteRetry(ctx, n, wait, reason); err != nil {
			return nil, err
		}
		if err := sleepRetry(ctx, wait); err != nil {
			return nil, err
		}
	}
}
