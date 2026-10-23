package native

import (
	"path"
	"path/filepath"
	"strings"
)

// SkipPrefetch implements tools.PrefetchExempt: a call naming a picture or a
// session image ref is not run ahead. Decoding, scaling and encoding a picture
// for a step that is cut away is wasted work (design 2026-10-23 section 7),
// and view_image never prefetched either. The guess is by extension and ref
// form only: a picture without one of those extensions keeps being prefetched
// (one extra decode, no visible effect).
func (t *FileReadTool) SkipPrefetch(args map[string]any) bool {
	list, err := readPathArgs(args)
	if err != nil {
		return false // the call is answered with the same error either way
	}
	for _, arg := range list {
		item, err := splitReadItem(arg)
		if err != nil {
			continue
		}
		if isPicturePath(item.path) {
			return true
		}
	}
	return false
}

// isPicturePath reports whether an argument names a picture by extension or as a
// session image ref; the prefetch exemption uses it.
func isPicturePath(arg string) bool {
	return isSessionImageRef(arg) || imageExts[strings.ToLower(filepath.Ext(path.Clean(arg)))]
}
