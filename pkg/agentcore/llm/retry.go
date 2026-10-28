package llm

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inhere/suga/pkg/agentcore"
)

type RetryPolicy struct {
	MaxAttempts                     int
	BaseDelay, MaxDelay, ResetAfter time.Duration
	// MaxElapsed is the wall-clock budget of one request's retries: once the
	// attempts and waits since the request started pass it, no further attempt is
	// made and the request fails (the caller's fallback_models then take over).
	// A provider that keeps failing would otherwise retry away minutes while the
	// process holds its session. Zero takes the default, NoTimeout disables it.
	MaxElapsed time.Duration
}

var DefaultRetryPolicy = RetryPolicy{MaxAttempts: 6, BaseDelay: time.Second, MaxDelay: 60 * time.Second, ResetAfter: 10 * time.Minute, MaxElapsed: 3 * time.Minute}

func (p RetryPolicy) withDefaults() RetryPolicy {
	d := DefaultRetryPolicy
	if p.MaxAttempts > 0 {
		d.MaxAttempts = p.MaxAttempts
	}
	if p.BaseDelay > 0 {
		d.BaseDelay = p.BaseDelay
	}
	if p.MaxDelay > 0 {
		d.MaxDelay = p.MaxDelay
	}
	if p.ResetAfter > 0 {
		d.ResetAfter = p.ResetAfter
	}
	switch {
	case p.MaxElapsed > 0:
		d.MaxElapsed = p.MaxElapsed
	case p.MaxElapsed < 0:
		d.MaxElapsed = 0 // off
	}
	d.MaxDelay = max(d.MaxDelay, d.BaseDelay)
	return d
}

type backoff struct {
	policy RetryPolicy
	mu     sync.Mutex
	level  int
	last   time.Time
	now    func() time.Time
	jitter func() float64
}

func newBackoff(p RetryPolicy) *backoff {
	return &backoff{policy: p.withDefaults(), now: time.Now, jitter: func() float64 { return rand.Float64()*2 - 1 }}
}
func (b *backoff) attempts() int { return b.policy.MaxAttempts }

// maxElapsed is the retry budget of one request (RetryPolicy.MaxElapsed); 0
// means no limit.
func (b *backoff) maxElapsed() time.Duration { return b.policy.MaxElapsed }

// retryBudget is the wall-clock budget of one request's retries, carried by the
// request's context: retryStream and the postWithRetry calls inside its attempts
// share it, so the whole request - not each attempt - is bounded by
// retry.max_elapsed.
type retryBudget struct {
	start time.Time
	limit time.Duration
}

type retryBudgetKey struct{}

// ensureRetryBudget installs the request's retry budget unless it is already
// installed; the first of retryStream/postWithRetry to run marks the start.
func ensureRetryBudget(ctx context.Context, b *backoff) context.Context {
	if _, ok := ctx.Value(retryBudgetKey{}).(*retryBudget); ok {
		return ctx
	}
	return context.WithValue(ctx, retryBudgetKey{}, &retryBudget{start: time.Now(), limit: b.maxElapsed()})
}

// overBudget reports whether waiting wait would pass the request's retry
// budget, i.e. the next attempt is not worth making.
func overBudget(ctx context.Context, wait time.Duration) bool {
	budget, _ := ctx.Value(retryBudgetKey{}).(*retryBudget)
	if budget == nil || budget.limit <= 0 {
		return false
	}
	return time.Since(budget.start)+wait > budget.limit
}
func (b *backoff) next(retryAfter time.Duration) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if !b.last.IsZero() && now.Sub(b.last) > b.policy.ResetAfter {
		b.level = 0
	}
	b.last = now
	d := b.policy.BaseDelay << min(b.level, 20)
	if d <= 0 || d > b.policy.MaxDelay {
		d = b.policy.MaxDelay
	}
	b.level++
	d += time.Duration(float64(d) * .2 * b.jitter())
	if retryAfter > d {
		d = retryAfter
	}
	return min(d, b.policy.MaxDelay)
}

type RetryReport struct {
	Retries   int
	Wait      time.Duration
	Last      string
	RequestID string
}
type retryReportKey struct{}

func withRetryReport(ctx context.Context) (context.Context, *RetryReport) {
	r := &RetryReport{}
	return context.WithValue(ctx, retryReportKey{}, r), r
}

func ensureRetryReport(ctx context.Context) context.Context {
	if _, ok := ctx.Value(retryReportKey{}).(*RetryReport); ok {
		return ctx
	}
	ctx, _ = withRetryReport(ctx)
	return ctx
}

type retryNotifier func(context.Context, int, time.Duration, string) error
type retryNotifierKey struct{}

func withRetryNotifier(ctx context.Context, fn retryNotifier) context.Context {
	return context.WithValue(ctx, retryNotifierKey{}, fn)
}
func noteRetry(ctx context.Context, attempt int, wait time.Duration, reason string) error {
	if r, _ := ctx.Value(retryReportKey{}).(*RetryReport); r != nil {
		r.Retries++
		r.Wait += wait
		r.Last = reason
	}
	if fn, _ := ctx.Value(retryNotifierKey{}).(retryNotifier); fn != nil {
		return fn(ctx, attempt, wait, reason)
	}
	return nil
}
func sleepRetry(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func retryableStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// retryAfterOf reads the preferred millisecond hint, then the standard Retry-After formats.
func retryAfterOf(h http.Header, now time.Time) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After-Ms")); v != "" {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

var retryAfterMessagePattern = regexp.MustCompile(`(?i)try again in\s+([0-9]+(?:\.[0-9]+)?)\s*(ms|milliseconds?|s|seconds?)\b`)

// retryAfterMessage parses the provider's human-readable retry hint. It is a
// hint only: backoff.next applies jitter and the configured maximum afterwards.
func retryAfterMessage(text string) time.Duration {
	m := retryAfterMessagePattern.FindStringSubmatch(text)
	if len(m) != 3 {
		return 0
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil || value <= 0 {
		return 0
	}
	unit := strings.ToLower(m[2])
	if strings.HasPrefix(unit, "ms") || strings.HasPrefix(unit, "millisecond") {
		if d, ok := agentcore.DurationFromSeconds(value/1000, 0); ok {
			return d
		}
		return 0
	}
	if d, ok := agentcore.DurationFromSeconds(value, 0); ok {
		return d
	}
	return 0
}

// requestIDOf returns the first supported provider correlation header.
func requestIDOf(h http.Header) string {
	for _, name := range []string{"x-request-id", "request-id", "anthropic-request-id", "openai-request-id"} {
		if id := strings.TrimSpace(h.Get(name)); id != "" {
			return id
		}
	}
	return ""
}

// noteRequestID records the id for the current HTTP attempt; an empty id clears a prior attempt.
func noteRequestID(ctx context.Context, id string) {
	if r, _ := ctx.Value(retryReportKey{}).(*RetryReport); r != nil {
		r.RequestID = id
	}
}

// requestIDFrom retrieves the correlation id recorded for the current attempt.
func requestIDFrom(ctx context.Context) string {
	if r, _ := ctx.Value(retryReportKey{}).(*RetryReport); r != nil {
		return r.RequestID
	}
	return ""
}

// withRequestID puts the provider's correlation id at the end of an error so
// the user can quote the same id when reporting the failure upstream.
func withRequestID(ctx context.Context, err error) error {
	id := requestIDFrom(ctx)
	if err == nil || id == "" || strings.Contains(err.Error(), "request-id:") {
		return err
	}
	return fmt.Errorf("%w (request-id: %s)", err, id)
}

var resetAtPattern = regexp.MustCompile(`(?i)resets at\s+([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.+\-]+Z)`)

func quotaResetAt(text string) string {
	m := resetAtPattern.FindStringSubmatch(text)
	if len(m) == 2 {
		if _, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
			return m[1]
		}
	}
	return ""
}
func quotaText(status int, text string) bool {
	if status == http.StatusPaymentRequired {
		return true
	}
	lower := strings.ToLower(text)
	for _, m := range []string{"insufficient_quota", "billing_hard_limit_reached", "usage limit", "quota", "limit resets", "billing"} {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
func quotaFailure(status int, provider string, body []byte) error {
	msg := fmt.Sprintf("%s request failed with HTTP %d: %s", provider, status, strings.TrimSpace(string(body)))
	if reset := quotaResetAt(string(body)); reset != "" {
		msg += fmt.Sprintf(" (resets at %s)", reset)
		return &quotaError{err: statusErr(status, fmt.Errorf("%s", msg)), resetAt: reset}
	}
	return &quotaError{err: statusErr(status, fmt.Errorf("%s", msg))}
}

// postWithRetry retries network and transient HTTP failures. Quota responses
// are recognized from the body before the normal 429 retry path and never wait.
func postWithRetry(ctx context.Context, client *http.Client, b *backoff, provider, path string, build func() (*http.Request, error)) (*http.Response, error) {
	if _, ok := ctx.Value(retryReportKey{}).(*RetryReport); !ok {
		// Direct provider calls still need request ids in their errors; observed
		// calls already installed the report used for attempt telemetry.
		ctx, _ = withRetryReport(ctx)
	}
	ctx = ensureRetryBudget(ctx, b)
	start := time.Now()
	var lastErr error
	for attempt := 1; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		noteRequestID(ctx, "")
		resp, err := client.Do(req)
		var retryAfter time.Duration
		var reason string
		phase := phaseRequestFailed
		switch {
		case err != nil:
			if ErrorKind(err) == ErrorKindRejected {
				// Redirect rejection is a policy failure, not a transient network error.
				return nil, err
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr, reason = err, failurePhase(err)
			phase = reason
			if phase == phaseConnectFailed && fallbackAvailable(ctx) {
				// The endpoint does not accept connections right now (DNS, dial, TLS
				// handshake): retrying it only pays the connect timeout again, so a
				// configured fallback model takes over after this first attempt.
				slog.Warn("llm provider connect failed, trying the next model instead of retrying", "provider", provider, "path", path,
					"attempt", attempt, "reason", phase, "elapsed", time.Since(start).Round(time.Millisecond), "err", lastErr)
				return nil, &transientRequestError{withRequestID(ctx, fmt.Errorf("%s request failed after %d attempts: %w", provider, attempt, lastErr))}
			}
		case retryableStatus(resp.StatusCode):
			noteRequestID(ctx, requestIDOf(resp.Header))
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			logProviderResponse(provider, path, resp.StatusCode, body)
			if quotaText(resp.StatusCode, string(body)) {
				return nil, withRequestID(ctx, quotaFailure(resp.StatusCode, provider, body))
			}
			if strings.EqualFold(strings.TrimSpace(resp.Header.Get("X-Should-Retry")), "false") {
				// The provider explicitly says this response is final. Classify the
				// actual HTTP failure without making another request.
				return nil, &transientRequestError{withRequestID(ctx, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))}
			}
			retryAfter = retryAfterOf(resp.Header, time.Now())
			if retryAfter == 0 {
				retryAfter = retryAfterMessage(string(body))
			}
			lastErr = fmt.Errorf("server error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
		default:
			noteRequestID(ctx, requestIDOf(resp.Header))
			return resp, nil
		}
		// The budget (retry.max_elapsed) is checked with the wait included: a request
		// that is out of time fails now instead of sleeping once more.
		wait := b.next(retryAfter)
		if attempt >= b.attempts() || overBudget(ctx, wait) {
			if overBudget(ctx, 0) {
				slog.Warn("llm request retry budget reached", "provider", provider, "attempt", attempt, "retry_max_elapsed", b.maxElapsed(), "err", lastErr)
			}
			return nil, &transientRequestError{withRequestID(ctx, fmt.Errorf("%s request failed after %d attempts: %w", provider, attempt, lastErr))}
		}
		slog.Warn("llm request failed, retrying", "provider", provider, "attempt", attempt, "phase", phase, "reason", reason, "wait", wait.Round(time.Millisecond), "err", lastErr)
		if err := noteRetry(ctx, attempt, wait, reason); err != nil {
			return nil, err
		}
		if err := sleepRetry(ctx, wait); err != nil {
			return nil, err
		}
	}
}
