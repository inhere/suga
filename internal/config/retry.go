package axconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"

	"github.com/inhere/suga/pkg/agentcore/llm"
)

// ReadRetryPolicy reads the top-level retry: section (how every provider
// retries a model request that failed for a transient reason, see
// llm.RetryPolicy): max_attempts (>= 1, the first attempt included; 1 turns
// retries off), max_elapsed (the wall-clock budget of one request's retries;
// "off" removes it) and the durations base_delay, max_delay and reset_after
// ("2s", "1m", or seconds). Unset keys take llm.DefaultRetryPolicy; an invalid
// value is an error. A provider may override every field with
// providers.<id>.retry (same fields), see applyRetryPolicy.
func ReadRetryPolicy() (llm.RetryPolicy, error) {
	return applyRetryPolicy(llm.DefaultRetryPolicy, mapValue(gkconfig.Get("retry")), "retry")
}

// applyRetryPolicy overlays a retry map onto base; prefix names the section in
// errors ("retry", "providers.<id>.retry"). Keys left out keep base's value, so
// a provider section only names what it changes. Unknown keys are ignored.
func applyRetryPolicy(base llm.RetryPolicy, section map[string]any, prefix string) (llm.RetryPolicy, error) {
	out := base
	if raw, ok := section["max_attempts"]; ok {
		// A bare number arrives as whatever the YAML decoder chose (int, int64,
		// uint64, float64...), so read every value as text.
		text := strings.TrimSpace(fmt.Sprint(raw))
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 {
			return llm.RetryPolicy{}, fmt.Errorf("%s.max_attempts must be a whole number >= 1, got %q", prefix, text)
		}
		out.MaxAttempts = n
	}
	for key, field := range map[string]*time.Duration{
		"base_delay": &out.BaseDelay, "max_delay": &out.MaxDelay, "reset_after": &out.ResetAfter,
	} {
		raw, ok := section[key]
		if !ok {
			continue
		}
		d, err := parseIdleDuration(strings.TrimSpace(fmt.Sprint(raw)))
		if err != nil || d == 0 {
			if err == nil {
				err = fmt.Errorf("must be above 0")
			}
			return llm.RetryPolicy{}, fmt.Errorf("%s.%s: %w", prefix, key, err)
		}
		*field = d
	}
	if raw, ok := section["max_elapsed"]; ok {
		// The whole request's retry budget: unlike the delays above an explicit
		// off turns it off, while an unset key keeps the default.
		d, err := parseTimeoutValue(raw)
		if err != nil {
			return llm.RetryPolicy{}, fmt.Errorf("%s.max_elapsed: %w", prefix, err)
		}
		if d == 0 {
			d = llm.NoTimeout
		}
		out.MaxElapsed = d
	}
	if out.MaxDelay < out.BaseDelay {
		return llm.RetryPolicy{}, fmt.Errorf("%s.max_delay (%s) is below %s.base_delay (%s)",
			prefix, out.MaxDelay, prefix, out.BaseDelay)
	}
	return out, nil
}
