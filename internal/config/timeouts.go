package axconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"

	"github.com/inhere/suga/pkg/agentcore/llm"
)

// Default model request timeouts: the llm package's defaults, repeated so the
// config layer can resolve an unset key on its own.
const (
	DefaultRequestTimeout        = llm.DefaultRequestTimeout
	DefaultResponseHeaderTimeout = llm.DefaultResponseHeaderTimeout
)

// RequestTimeouts bounds one model request:
//
//   - Request bounds a non-streamed request as a whole (dial, TLS, headers, body).
//   - ResponseHeader bounds the wait for a streamed request's response headers;
//     the streamed body itself is not bounded (a long answer may stream for
//     minutes).
//   - StreamIdle bounds the silence between two reads of a streamed body: a
//     provider that stops sending data mid-answer fails the request instead of
//     hanging the turn (see llm.stream_idle.go).
//
// A zero field means "no limit" ("off" in the config). StreamIdle is the one
// exception: left unset it takes the provider's default (llm.DefaultStreamIdleTimeout,
// or the longer reasoning one), and only an explicit "off" disables it.
// The values come from the top-level `timeouts:` section, overridden per
// provider by `providers.<id>.timeouts`.
type RequestTimeouts struct {
	Request        time.Duration
	ResponseHeader time.Duration
	StreamIdle     time.Duration
}

// DefaultRequestTimeouts is what an unconfigured `timeouts:` section resolves to.
func DefaultRequestTimeouts() RequestTimeouts {
	return RequestTimeouts{Request: DefaultRequestTimeout, ResponseHeader: DefaultResponseHeaderTimeout}
}

// ReadRequestTimeouts reads the top-level `timeouts:` section; unset keys keep
// the defaults and an invalid value is an error.
func ReadRequestTimeouts() (RequestTimeouts, error) {
	out := DefaultRequestTimeouts()
	if gkconfig.Get("timeouts") == nil {
		return out, nil
	}
	return applyTimeouts(out, mapValue(gkconfig.Get("timeouts")), "timeouts")
}

// applyTimeouts overlays a `timeouts:` map (request, response_header,
// stream_idle) onto base; prefix names the section in errors ("timeouts",
// "providers.<id>.timeouts").
func applyTimeouts(base RequestTimeouts, section map[string]any, prefix string) (RequestTimeouts, error) {
	for key, raw := range section {
		var field *time.Duration
		switch key {
		case "request":
			field = &base.Request
		case "response_header":
			field = &base.ResponseHeader
		case "stream_idle":
			// Unset means "the provider's default" here, so an explicit off has to
			// be told apart from it (see RequestTimeouts).
			d, err := parseTimeoutValue(raw)
			if err != nil {
				return RequestTimeouts{}, fmt.Errorf("%s.%s: %w", prefix, key, err)
			}
			if d == 0 {
				d = llm.NoTimeout
			}
			base.StreamIdle = d
			continue
		default:
			return RequestTimeouts{}, fmt.Errorf("%s.%s: unknown key (want request, response_header or stream_idle)", prefix, key)
		}
		d, err := parseTimeoutValue(raw)
		if err != nil {
			return RequestTimeouts{}, fmt.Errorf("%s.%s: %w", prefix, key, err)
		}
		*field = d
	}
	return base, nil
}

// parseTimeoutValue reads one timeout value: a duration ("90s", "2m"), a number
// of seconds (90), or off / none / 0 for no limit.
func parseTimeoutValue(raw any) (time.Duration, error) {
	var value string
	if s, ok := raw.(string); ok {
		value = strings.TrimSpace(s)
	} else {
		// A bare number arrives as whatever the YAML decoder chose (int, int64,
		// uint64, float64...): take anything that reads as a number of seconds.
		value = strings.TrimSpace(fmt.Sprint(raw))
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return 0, fmt.Errorf("want a duration like \"30s\", a number of seconds or \"off\", got %v", raw)
		}
	}
	switch strings.ToLower(value) {
	case "off", "none":
		return 0, nil
	}
	d, err := parseIdleDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%w (or \"off\" for no limit)", err)
	}
	return d, nil // a bare "0" lands here as no limit, like "off"
}

// mapValue returns raw as a key/value map, or nil when it is not one.
func mapValue(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	return m
}
