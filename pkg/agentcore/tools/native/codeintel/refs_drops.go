package codeintel

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// maxDropNames caps how many dropped qualifiers a qualified file_refs
	// result names; the rest are only counted.
	maxDropNames = 20
	// RareDropUses is how often a variable qualifier may occur and still have
	// its lines shown: a name used once or twice (child.Run) is likely a
	// specific value, while t.Run or cmd.Run in bulk is another object.
	RareDropUses = 2
	// maxRareDropLines caps the lines shown for rare variable qualifiers.
	maxRareDropLines = 10
)

// RefDrops is what a qualified file_refs search left out: the count of
// occurrences by the qualifier text they had ("child" for child.Run,
// "pkg:exec" for exec.Run through an import, "" for a bare use or an
// approximate text match), and the occurrences through variables, whose type
// the textual filter cannot tell.
type RefDrops struct {
	counts map[string]int
	vars   []Ref
	// bare are the dropped Go uses without a qualifier. Inside the package
	// that defines the name a call needs none, so with Q a package alias
	// (appconfig.ReadModelSettings) the package's own ReadModelSettings("")
	// calls land here and must stay visible.
	bare []Ref
}

func NewRefDrops() *RefDrops { return &RefDrops{counts: map[string]int{}} }

// key is the counting key of one dropped Go occurrence.
func dropKey(ref Ref) string {
	if ref.Pkg && ref.Qualifier != "" {
		return "pkg:" + ref.Qualifier
	}
	return ref.Qualifier
}

// add counts one dropped Go occurrence and keeps it when a variable
// qualified it.
func (d *RefDrops) add(ref Ref) {
	key := dropKey(ref)
	d.counts[key]++
	switch {
	case key == "":
		// A definition of another object with the name is not a use.
		if ref.Class != RefDef {
			d.bare = append(d.bare, ref)
		}
	case !ref.Pkg:
		d.vars = append(d.vars, ref)
	}
}

// addText counts one dropped approximate (text) match.
func (d *RefDrops) addText() { d.counts[""]++ }

// Merge adds the drops of one file to the totals of a search.
func (d *RefDrops) Merge(other *RefDrops) {
	if other == nil {
		return
	}
	for k, n := range other.counts {
		d.counts[k] += n
	}
	d.vars = append(d.vars, other.vars...)
	d.bare = append(d.bare, other.bare...)
}

// Total is how many occurrences were dropped.
func (d *RefDrops) Total() int {
	if d == nil {
		return 0
	}
	n := 0
	for _, c := range d.counts {
		n += c
	}
	return n
}

// Rare returns the dropped occurrences worth a line of their own, in path and
// line order and capped, plus how many were left out by the cap: the Go uses
// without a qualifier (a call inside the defining package needs none) and the
// uses through a variable qualifier that occurs at most RareDropUses times in
// the search. The model reads result lines, not trailing notes: a *Runner held
// in a variable called child, or config's own ReadModelSettings("") under
// name appconfig.ReadModelSettings, has to appear as a line to be noticed.
func (d *RefDrops) Rare() ([]Ref, int) {
	if d == nil {
		return nil, 0
	}
	out := append([]Ref(nil), d.bare...)
	for _, r := range d.vars {
		if d.counts[dropKey(r)] <= RareDropUses {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	if len(out) > maxRareDropLines {
		return out[:maxRareDropLines], len(out) - maxRareDropLines
	}
	return out, 0
}

// describe names the dropped variable qualifiers (sorted, with counts) and
// sums up the imported packages and the unqualified uses:
// ` (variables: child 1, cmd 3, t 40; 5 through imported packages; 2
// unqualified or text matches)`. Empty when nothing was dropped.
func (d *RefDrops) Describe() string {
	if d.Total() == 0 {
		return ""
	}
	var vars []string
	pkgs, bare := 0, 0
	for k, n := range d.counts {
		switch {
		case k == "":
			bare += n
		case strings.HasPrefix(k, "pkg:"):
			pkgs += n
		default:
			vars = append(vars, k)
		}
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
			names = append(names, fmt.Sprintf("%s %d", k, d.counts[k]))
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
