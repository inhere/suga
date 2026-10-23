package llm

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"time"
)

// DefaultConnectTimeout bounds the connection phase of a provider request -
// dialing the endpoint and the TLS handshake - on both provider clients (see
// newProviderTransport). It is deliberately independent of the overall request
// timeout (timeouts.go): http.DefaultTransport dials for up to 30s, which a
// turn with a fallback model should not pay in front of every step when the
// endpoint is unreachable. 10s is generous for a healthy endpoint and short
// enough that a dead one is noticed quickly.
const DefaultConnectTimeout = 10 * time.Second

// Failure phases: how a provider request failed. They name the reason in the
// log lines and in the fallback reason of the trace (Attempt.Fallback).
//
//   - phaseConnectFailed: the request never reached the provider - DNS
//     resolution, dialing, the TLS handshake. The endpoint is unreachable right
//     now, so a configured fallback model takes over instead of the same
//     endpoint being retried (postWithRetry).
//   - phaseRequestFailed: the connection is up but the request failed - a slow
//     response header, 408/429/5xx, a broken stream. These keep the retry
//     policy's handling unchanged.
const (
	phaseConnectFailed = "connect_failed"
	phaseRequestFailed = "request_failed"
)

// failurePhase names the phase err failed in.
func failurePhase(err error) string {
	if isConnectFailure(err) {
		return phaseConnectFailed
	}
	return phaseRequestFailed
}

// isConnectFailure reports whether err failed before the request reached the
// provider: DNS resolution, dialing the endpoint (refused, timed out) or the
// TLS handshake. A read or response-header timeout is a request-phase failure,
// so a connected but silent endpoint keeps the normal retry path.
func isConnectFailure(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}
	var verifyErr *tls.CertificateVerificationError
	if errors.As(err, &verifyErr) {
		return true
	}
	// net/http reports a handshake that outlived its deadline as an unexported
	// error; its text is the only handle on it.
	text := err.Error()
	return strings.Contains(text, "TLS handshake timeout") || strings.Contains(text, "tls: handshake failure")
}

// fallbackAvailableKey marks a request whose failure may move on to another
// candidate: a Pool calls withFallbackAvailable when a later provider is left.
// postWithRetry then ends a connection-phase failure after the first attempt
// instead of retrying an endpoint that is known to be unreachable.
type fallbackAvailableKey struct{}

// withFallbackAvailable marks ctx as having another candidate to try.
func withFallbackAvailable(ctx context.Context) context.Context {
	return context.WithValue(ctx, fallbackAvailableKey{}, true)
}

// fallbackAvailable reports whether the request has another candidate to try.
func fallbackAvailable(ctx context.Context) bool {
	ok, _ := ctx.Value(fallbackAvailableKey{}).(bool)
	return ok
}
