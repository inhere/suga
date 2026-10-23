package tools

// PrefetchExempt is the optional interface a Prefetchable tool implements to
// skip the run-ahead for a single call: it stays Prefetchable for its other
// calls. Registry.Prefetch asks it after the static Meta filter, once per call
// with that call's arguments (file_read reads pictures, and decoding one for a
// step that is cut away is wasted work - design 2026-10-23-file-read-images).
type PrefetchExempt interface {
	// SkipPrefetch reports whether this call must not run ahead.
	SkipPrefetch(args map[string]any) bool
}

// skipsPrefetch reports whether one call of t is exempt from the run-ahead.
func skipsPrefetch(t any, args map[string]any) bool {
	exempt, ok := t.(PrefetchExempt)
	return ok && exempt.SkipPrefetch(args)
}
