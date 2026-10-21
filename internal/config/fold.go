package axconfig

import (
	"fmt"
	"strconv"
	"strings"

	gkconfig "github.com/gookit/config/v2"
)

// History folding defaults (docs/design/2026-10-12-history-folding-design.md).
// Folding is off by default since 2026-10-12: rebuilding the prefix after
// each fold was about a third of the uncached input in the P5 retest (see
// docs/research/2026-10-11-suag-vs-omp-loop-analysis.md), and runs compare
// more directly with omp without it. context.fold_every: 10 turns it on.
const (
	DefaultFoldEvery = 0
	DefaultFoldKeep  = 5
	// DefaultFoldAt is the size trigger of folding: a request view estimated at
	// more than this share of the compaction threshold starts folding
	// (docs/design/2026-10-21-fold-by-size-design.md). It defaults to 0 (off):
	// two real comparisons (design 0.4) showed smaller requests but no clear
	// net gain once the cache rebuilds were counted; 0.5 is the tested value.
	DefaultFoldAt = 0
)

// FoldSettings configures history folding of the request view.
type FoldSettings struct {
	// Every (context.fold_every) is the batch size the fold boundary moves
	// by; 0 turns folding off.
	Every int
	// Keep (context.fold_keep) is how many recent steps stay whole (>= 1).
	Keep int
	// At (context.fold_at) is the share of the compaction threshold the
	// pre-fold request estimate has to pass before the segment starts folding
	// (0 <= at < 1; 0 disables the size trigger).
	At float64
}

// ReadFoldSettings reads context.fold_every (>= 0), context.fold_keep
// (>= 1) and context.fold_at (0 <= at < 1). Unset keys take the defaults; an
// invalid value is an error.
func ReadFoldSettings() (FoldSettings, error) {
	out := FoldSettings{Every: DefaultFoldEvery, Keep: DefaultFoldKeep, At: DefaultFoldAt}
	read := func(key string, min int, dst *int) error {
		if gkconfig.Get(key) == nil {
			return nil
		}
		raw := strings.TrimSpace(gkconfig.String(key))
		n, err := strconv.Atoi(raw)
		if err != nil || n < min {
			return fmt.Errorf("%s must be an integer >= %d, got %q", key, min, raw)
		}
		*dst = n
		return nil
	}
	if err := read("context.fold_every", 0, &out.Every); err != nil {
		return FoldSettings{}, err
	}
	if err := read("context.fold_keep", 1, &out.Keep); err != nil {
		return FoldSettings{}, err
	}
	if gkconfig.Get("context.fold_at") != nil {
		raw := strings.TrimSpace(gkconfig.String("context.fold_at"))
		at, err := strconv.ParseFloat(raw, 64)
		if err != nil || at < 0 || at >= 1 {
			return FoldSettings{}, fmt.Errorf("context.fold_at must be a number >= 0 and < 1, got %q", raw)
		}
		out.At = at
	}
	return out, nil
}
