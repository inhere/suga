package llm

import (
	"errors"
	"fmt"
	"strings"
)

// This file holds the API error object a provider reports inside a stream event
// or a response body, and its classification: a temporary server side failure
// (overloaded, rate limited, an internal error) is worth requesting again,
// anything else (invalid_request_error, a bad model, a content filter) would
// only come back the same way. The temporary case travels as an
// apiTransientError, which retryStream retries and ErrorKind reports as
// ErrorKindTransient once the attempts run out.

// apiError is the error object of a provider: OpenAI carries code and type next
// to the message, Anthropic only the type (overloaded_error, api_error,
// rate_limit_error, invalid_request_error, ...).
type apiError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// empty reports whether the error object carries nothing at all.
func (e *apiError) empty() bool {
	return e == nil || (e.Message == "" && e.Code == "" && e.Type == "")
}

// transientAPIErrorCodes are the API error codes and types that name a
// temporary server side failure: OpenAI's server_is_overloaded, server_error,
// rate_limit_exceeded and slow_down, plus Anthropic's overloaded_error, its
// generic api_error and rate_limit_error. Both fields are looked up, because
// OpenAI sets either one and Anthropic only type.
var transientAPIErrorCodes = map[string]bool{
	"server_is_overloaded": true,
	"server_error":         true,
	"rate_limit_exceeded":  true,
	"slow_down":            true,
	"overloaded_error":     true,
	"api_error":            true,
	"rate_limit_error":     true,
}

// transientAPIErrorPhrases appear in the message of a temporary failure: some
// gateways report neither code nor type, only the text ("Our servers are
// currently overloaded. Please try again later."), or report a broken upstream
// stream as an API error ("Upstream HTTP/2 stream failed", suga-ryy0). They are
// matched case-insensitively.
var transientAPIErrorPhrases = []string{"overloaded", "try again later", "stream failed"}

func isContextLengthError(code, typ, msg string) bool {
	for _, value := range []string{code, typ} {
		if strings.EqualFold(strings.TrimSpace(value), "context_length_exceeded") {
			return true
		}
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "prompt is too long")
}

const contextLengthHint = "上下文超长，可 :compact 或开新会话"

// isTransientAPIError reports whether an API error is worth requesting again:
// its code or type names a temporary failure, or - without a usable code or
// type - its message asks the caller to come back later. Everything else is
// permanent: requesting it again would only get the same error.
func isTransientAPIError(code, typ, msg string) bool {
	if isContextLengthError(code, typ, msg) {
		return false
	}
	if hasQuotaMarker(code) || hasQuotaMarker(typ) || hasQuotaMarker(msg) {
		// An exhausted allowance is not temporary in any useful sense - it
		// resets in hours or days - so it is not retried (ErrorKindQuota).
		return false
	}
	if transientAPIErrorCodes[strings.ToLower(code)] || transientAPIErrorCodes[strings.ToLower(typ)] {
		return true
	}
	lower := strings.ToLower(msg)
	for _, phrase := range transientAPIErrorPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// apiTransientError is an API error the server itself reported as temporary
// (see isTransientAPIError). It is a retryableStreamError, so retryStream
// requests the whole answer again instead of failing the turn, and ErrorKind
// reports ErrorKindTransient when the attempts run out. Its text keeps the
// API's own wording, so a log still shows what the provider said.
type apiTransientError struct{ err error }

func (e *apiTransientError) Error() string { return e.err.Error() }
func (e *apiTransientError) Unwrap() error { return e.err }

// apiTransientReason is the retry cause of an overload or rate limit, shown by
// a frontend before the wait (the terminal prints it in its retry notice, stream-json
// in its retry line). It differs from the "stream broken" of a cut stream: there,
// part of an answer was already streamed and is void; here the provider refused
// the request before answering anything.
const apiTransientReason = "服务端过载/限流"

// retryReason implements retryableStreamError.
func (e *apiTransientError) retryReason() string { return apiTransientReason }

// giveUp implements retryableStreamError: the failure of an overload or rate
// limit that outlived every attempt of the request, naming how many were made.
func (e *apiTransientError) giveUp(provider string, attempts int) error {
	return fmt.Errorf("%s 服务端过载，已重试 %d 次: %w", provider, attempts, e)
}

// isAPITransientError reports whether err is (or wraps) an apiTransientError;
// ErrorKind uses it to answer ErrorKindTransient.
func isAPITransientError(err error) bool {
	var transient *apiTransientError
	return errors.As(err, &transient)
}

// newAPIError builds the error of an API error message; transient says whether
// isTransientAPIError already classified it as temporary. The text is the same
// either way ("<prefix>: <message>"); only the error's type decides whether
// retryStream retries it and which error_kind the hosts report.
func newAPIError(prefix, msg string, transient bool) error {
	if msg == "" {
		msg = "unknown error"
	}
	if transient {
		return &apiTransientError{fmt.Errorf("%s: %s", prefix, msg)}
	}
	return badResponsef("%s: %s", prefix, msg)
}

// apiErrorf builds the error of an API error object carried by a stream event
// or a response body. prefix is the provider's wording for its API errors, e.g.
// "openai api error" or "responses api error".
func apiErrorf(prefix string, e *apiError) error {
	if isContextLengthError(e.Code, e.Type, e.Message) {
		return &contextLengthError{fmt.Errorf("%s: %s；%s", prefix, e.Message, contextLengthHint)}
	}
	err := newAPIError(prefix, e.Message, isTransientAPIError(e.Code, e.Type, e.Message))
	if hasQuotaMarker(e.Code) || hasQuotaMarker(e.Type) {
		// insufficient_quota and the like name the exhausted allowance only in the
		// code: keep it on the error so ErrorKind reports ErrorKindQuota.
		return &quotaError{err: err}
	}
	return err
}

// apiMessageErrorf is apiErrorf for an API error the provider reported as plain
// text or as a bare message (a stream event with only a top-level "message",
// an error body that is not JSON): the message is all there is to classify.
func apiMessageErrorf(prefix, msg string) error {
	if isContextLengthError("", "", msg) {
		return &contextLengthError{fmt.Errorf("%s: %s；%s", prefix, msg, contextLengthHint)}
	}
	return newAPIError(prefix, msg, isTransientAPIError("", "", msg))
}
