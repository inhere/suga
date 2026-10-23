package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Error kinds a host reports for a turn that failed on a provider error.
const (
	// ErrorKindTransient: the stream broke or was cut off, or the API reported a
	// temporary failure (overloaded, rate limited, a server error), and the
	// retries ran out (streamReadError, apiTransientError). The request itself
	// was fine, so a later attempt or a continued session is likely to work; the
	// text of a cut answer was already shown and is void (retryStream restarted
	// the message, see the stream-json "retry" lines with restart=true).
	ErrorKindTransient = "provider_transient"
	// ErrorKindBadResponse: the provider answered, but with something unusable -
	// a body or stream event that is not valid JSON, an answer that is missing
	// its content, or an error the API reported itself. Requesting it again
	// would get the same answer, so it is not retried.
	ErrorKindBadResponse = "provider_bad_response"
	// ErrorKindRejected: the provider refused the request with an HTTP status that
	// is about the request or this provider (400/401/403/404/422 ...); whether
	// another provider may help is ShouldFallback's call.
	ErrorKindRejected = "provider_rejected"
	// ErrorKindQuota means the provider account or billing allowance is exhausted.
	// It is checked before transient because weekly quota responses are often
	// labelled rate_limit_error/RATE_LIMITED by APIs.
	ErrorKindQuota = "provider_quota"
)

type badResponseError struct{ err error }

func (e *badResponseError) Error() string { return e.err.Error() }
func (e *badResponseError) Unwrap() error { return e.err }
func badResponsef(format string, args ...any) error {
	return &badResponseError{fmt.Errorf(format, args...)}
}

type transientRequestError struct{ err error }

func (e *transientRequestError) Error() string { return e.err.Error() }
func (e *transientRequestError) Unwrap() error { return e.err }

type contextLengthError struct{ err error }

func (e *contextLengthError) Error() string { return e.err.Error() }
func (e *contextLengthError) Unwrap() error { return e.err }

type statusError struct {
	status int
	err    error
}

func (e *statusError) Error() string { return e.err.Error() }
func (e *statusError) Unwrap() error { return e.err }
func (e *statusError) Status() int   { return e.status }

// quotaError preserves an optional RFC3339 reset time for trace and hosts.
type quotaError struct {
	err     error
	resetAt string
}

func (e *quotaError) Error() string { return e.err.Error() }
func (e *quotaError) Unwrap() error { return e.err }

// NewQuotaError marks err as an exhausted quota (ErrorKindQuota) with an optional
// RFC3339 reset time, for a host whose own provider reports one and for tests.
func NewQuotaError(err error, resetAt string) error {
	return &quotaError{err: err, resetAt: resetAt}
}

// ResetAt returns the provider-supplied quota reset time, if one was present.
func ResetAt(err error) string {
	var q *quotaError
	if errors.As(err, &q) {
		return q.resetAt
	}
	return ""
}

func statusErr(status int, err error) error {
	if err == nil || status >= 200 && status < 300 {
		return err
	}
	return &statusError{status: status, err: err}
}

// ErrorKind classifies a failed model request for the hosts: ErrorKindQuota
// (the account or billing allowance is exhausted; checked first, since quota
// answers often come labelled as rate limits), ErrorKindTransient (a broken or
// cut stream, a temporary API error or a retryable HTTP status whose retries ran
// out), ErrorKindRejected (an HTTP status that refuses the request itself or a
// context that cannot fit), ErrorKindBadResponse (a provider answer that cannot
// be used, or a permanent error the API reported) or "" for everything else -
// a cancelled turn, a storage, tool or hook failure, or an error that did not
// come from a provider. The value goes on the turn event's attrs and on the
// stream-json error line.
func ErrorKind(err error) string {
	if isQuotaError(err) {
		return ErrorKindQuota
	}
	var readErr *streamReadError
	if errors.As(err, &readErr) {
		return ErrorKindTransient
	}
	if isAPITransientError(err) {
		return ErrorKindTransient
	}
	var transient *transientRequestError
	if errors.As(err, &transient) {
		return ErrorKindTransient
	}
	var contextLength *contextLengthError
	if errors.As(err, &contextLength) {
		return ErrorKindRejected
	}
	var rejected *statusError
	if errors.As(err, &rejected) {
		return ErrorKindRejected
	}
	var bad *badResponseError
	if errors.As(err, &bad) {
		return ErrorKindBadResponse
	}
	return ""
}

// ShouldFallback reports whether another provider may succeed. Quota is allowed
// here, while Pool separately prevents it from selecting another model of the
// same provider (the account allowance would be shared). A context-length
// rejection may succeed on a different provider with a larger window, but Pool
// skips sibling models of the same provider.
func ShouldFallback(err error) bool {
	if _, contextLength := err.(*contextLengthError); contextLength {
		return true
	}
	var contextLength *contextLengthError
	if errors.As(err, &contextLength) {
		return true
	}
	switch ErrorKind(err) {
	case ErrorKindTransient, ErrorKindQuota:
		return true
	case ErrorKindRejected:
		var rejected *statusError
		if errors.As(err, &rejected) {
			return rejected.Status() == http.StatusUnauthorized || rejected.Status() == http.StatusForbidden || rejected.Status() == http.StatusNotFound
		}
	}
	return false
}

func isQuotaError(err error) bool {
	if err == nil {
		return false
	}
	var quota *quotaError
	if errors.As(err, &quota) {
		return true
	}
	var status *statusError
	if errors.As(err, &status) && status.Status() == http.StatusPaymentRequired {
		return true
	}
	return hasQuotaMarker(err.Error())
}

// quotaMarkers are the words an exhausted account or billing allowance shows up
// with, whatever code or type the API gives it (a weekly plan limit often comes
// as rate_limit_error / RATE_LIMITED).
var quotaMarkers = []string{"insufficient_quota", "billing_hard_limit_reached", "usage limit", "quota", "limit resets", "billing"}

// hasQuotaMarker reports whether text (an error message, code or type) names an
// exhausted allowance; matched case-insensitively.
func hasQuotaMarker(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range quotaMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func fallbackReason(err error) string {
	reason := ErrorKind(err)
	if reason == "" {
		reason = "unclassified"
	}
	var rejected *statusError
	if errors.As(err, &rejected) {
		reason = fmt.Sprintf("%s (HTTP %d)", reason, rejected.Status())
	}
	if isConnectFailure(err) {
		// The trace shows whether the model was unreachable or its request failed.
		reason = fmt.Sprintf("%s (%s)", reason, phaseConnectFailed)
	}
	return reason
}
