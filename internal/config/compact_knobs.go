package axconfig

import (
	"fmt"
	"strconv"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// CompactSettings are the three compaction knobs (suga-m34w). Every default
// keeps the behavior from before the knobs existed.
type CompactSettings struct {
	// PinFirstUser (context.pin_first_user, default false) keeps the session's
	// first user request in every handoff package, next to the recent requests.
	PinFirstUser bool
	// KeepUserWordsBudget (context.keep_user_words_budget, bytes, default 0) is
	// the own budget of the user's original words in a package. 0 keeps the
	// fixed rule (the 3 latest requests, each capped); above 0 the latest
	// requests are kept newest first for as long as they fit in this many bytes.
	KeepUserWordsBudget int
	// SummaryMaxTokens (context.summary_max_tokens, default 0) caps the output
	// of one summary call; 0 sets no cap. An answer cut at the cap is asked for
	// once more with twice the cap.
	SummaryMaxTokens int
}

// ReadCompactSettings reads context.pin_first_user (bool),
// context.keep_user_words_budget (integer >= 0) and context.summary_max_tokens
// (integer >= 0). Unset keys take the defaults; an invalid value is an error.
func ReadCompactSettings() (CompactSettings, error) {
	var out CompactSettings
	if gkconfig.Get("context.pin_first_user") != nil {
		raw := strings.TrimSpace(gkconfig.String("context.pin_first_user"))
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return CompactSettings{}, fmt.Errorf("context.pin_first_user must be true or false, got %q", raw)
		}
		out.PinFirstUser = v
	}
	read := func(key string, dst *int) error {
		if gkconfig.Get(key) == nil {
			return nil
		}
		raw := strings.TrimSpace(gkconfig.String(key))
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return fmt.Errorf("%s must be an integer >= 0, got %q", key, raw)
		}
		*dst = n
		return nil
	}
	if err := read("context.keep_user_words_budget", &out.KeepUserWordsBudget); err != nil {
		return CompactSettings{}, err
	}
	if err := read("context.summary_max_tokens", &out.SummaryMaxTokens); err != nil {
		return CompactSettings{}, err
	}
	return out, nil
}
