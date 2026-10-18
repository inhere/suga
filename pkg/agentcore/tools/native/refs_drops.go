package native

import (
	"fmt"
	"sort"
	"strings"
)

// maxDropNames caps how many dropped qualifiers a qualified file_refs result
// names; the rest are only counted.
const maxDropNames = 20

// refDrops counts the occurrences a qualified file_refs search left out, by the
// qualifier text they had: "child" for child.Run, "pkg:exec" for exec.Run
// through an import, "" for a bare use or an approximate text match.
type refDrops map[string]int

// add counts one dropped Go occurrence under its qualifier.
func (d refDrops) add(ref Ref) {
	key := ref.Qualifier
	if ref.Pkg && key != "" {
		key = "pkg:" + key
	}
	d[key]++
}

// merge adds the drops of one file to the totals of a search.
func (d refDrops) merge(other refDrops) {
	for k, n := range other {
		d[k] += n
	}
}

// total is how many occurrences were dropped.
func (d refDrops) total() int {
	n := 0
	for _, c := range d {
		n += c
	}
	return n
}

// describe names the dropped variable qualifiers (sorted, with counts) and
// sums up the imported packages and the unqualified uses, so a *Runner held in
// a variable called child stays visible: ` (variables: child 1, cmd 3, t 40;
// 5 through imported packages; 2 unqualified)`. Empty when nothing was dropped.
func (d refDrops) describe() string {
	var vars []string
	pkgs, bare := 0, 0
	for k, n := range d {
		switch {
		case k == "":
			bare += n
		case strings.HasPrefix(k, "pkg:"):
			pkgs += n
		default:
			vars = append(vars, k)
		}
	}
	if len(vars) == 0 && pkgs == 0 && bare == 0 {
		return ""
	}
	sort.Strings(vars)
	var parts []string
	if len(vars) > 0 {
		shown := vars
		if len(shown) > maxDropNames {
			shown = shown[:maxDropNames]
		}
		names := make([]string, 0, len(shown))
		for _, k := range shown {
			names = append(names, fmt.Sprintf("%s %d", k, d[k]))
		}
		list := strings.Join(names, ", ")
		if more := len(vars) - len(shown); more > 0 {
			list += fmt.Sprintf(" and %d more", more)
		}
		parts = append(parts, "variables: "+list)
	}
	if pkgs > 0 {
		parts = append(parts, fmt.Sprintf("%d through imported packages", pkgs))
	}
	if bare > 0 {
		parts = append(parts, fmt.Sprintf("%d unqualified or text matches", bare))
	}
	return " (" + strings.Join(parts, "; ") + ")"
}
