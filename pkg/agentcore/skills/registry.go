package skills

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// maxSkillDepth bounds how deep below a root SKILL.md files are looked for
// (root/<name>/SKILL.md is depth 2; grouped layouts go one level deeper).
const maxSkillDepth = 3

// Registry holds the skills found at startup. The first registration of a
// name wins, so roots are loaded in priority order.
type Registry struct {
	skills []Skill
	byName map[string]int
	// skipped and shadowed are the load diagnostics: a SKILL.md that could not
	// be parsed, and a skill a higher-priority root kept. They let the manifest
	// and the chat start notice explain a skill that is installed but absent.
	skipped  []SkippedSkill
	shadowed []ShadowedSkill
}

// SkippedSkill is a SKILL.md that was found but not registered, with the
// reason (a parse or validation error).
type SkippedSkill struct {
	Path string
	Err  string
}

// ShadowedSkill is a skill whose name was already registered by an earlier
// root: Winner is the SKILL.md that stays in effect, Loser the dropped one.
type ShadowedSkill struct {
	Name   string
	Winner string
	Loser  string
}

// Skipped reports the SKILL.md files that could not be registered.
func (r *Registry) Skipped() []SkippedSkill {
	if r == nil {
		return nil
	}
	return append([]SkippedSkill(nil), r.skipped...)
}

// Shadowed reports the same-name skills dropped in favour of an earlier root.
func (r *Registry) Shadowed() []ShadowedSkill {
	if r == nil {
		return nil
	}
	return append([]ShadowedSkill(nil), r.shadowed...)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{byName: map[string]int{}} }

// Register adds s unless a skill with the same name (case-insensitive) is
// already registered; it reports whether s was added.
func (r *Registry) Register(s Skill) bool {
	key := strings.ToLower(s.Name)
	if key == "" {
		return false
	}
	if _, dup := r.byName[key]; dup {
		return false
	}
	r.byName[key] = len(r.skills)
	r.skills = append(r.skills, s)
	return true
}

// Get returns the named skill.
func (r *Registry) Get(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	i, ok := r.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Skill{}, false
	}
	return r.skills[i], true
}

// List returns the skills sorted by name.
func (r *Registry) List() []Skill {
	if r == nil {
		return nil
	}
	out := append([]Skill(nil), r.skills...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Len is the number of registered skills.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.skills)
}

// Root is one directory scanned for skills.
type Root struct {
	Dir string
	// Workspace marks a root inside the workspace (project skills).
	Workspace bool
}

// LoadRoots scans each root (in priority order) for SKILL.md files and
// registers them; a later root never replaces an earlier skill of the same
// name. Missing roots are skipped; an invalid SKILL.md is logged and skipped.
func LoadRoots(roots []Root) *Registry {
	reg := NewRegistry()
	for _, r := range roots {
		root := r.Dir
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		var found []string
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			depth := len(strings.Split(rel, string(filepath.Separator)))
			if d.IsDir() {
				if path != root && (strings.HasPrefix(d.Name(), ".") || depth >= maxSkillDepth) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(d.Name(), "SKILL.md") {
				found = append(found, path)
			}
			return nil
		})
		sort.Strings(found)
		for _, path := range found {
			s, _, err := ParseFile(path)
			if err != nil {
				slog.Warn("skip invalid skill", "path", path, "err", err)
				reg.skipped = append(reg.skipped, SkippedSkill{Path: path, Err: err.Error()})
				continue
			}
			s.Path, s.Dir, s.Workspace = path, filepath.Dir(path), r.Workspace
			if !reg.Register(s) {
				// A later root never replaces an earlier skill of the same name;
				// record both paths so the manifest can say which one won.
				winner, _ := reg.Get(s.Name)
				reg.shadowed = append(reg.shadowed, ShadowedSkill{Name: s.Name, Winner: winner.Path, Loser: path})
			}
		}
	}
	return reg
}

// Search ranks skills against query: each query word found in the name
// scores 3, in the description 1 (substring match, case-insensitive). CJK runs
// are also split into overlapping two-character words so "前端页面设计" finds a
// description saying "前端设计". Skills scoring 0 are dropped; ties keep name
// order. At most limit skills are returned.
func (r *Registry) Search(query string, limit int) []Skill {
	terms := searchTerms(query)
	if len(terms) == 0 || r == nil {
		return nil
	}
	type hit struct {
		s     Skill
		score int
	}
	var hits []hit
	for _, s := range r.List() {
		name, desc := strings.ToLower(s.Name), strings.ToLower(s.Description)
		score := 0
		for _, t := range terms {
			if strings.Contains(name, t) {
				score += 3
			} else if strings.Contains(desc, t) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, hit{s, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]Skill, 0, min(limit, len(hits)))
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].s)
	}
	return out
}

// searchTerms lowercases query and splits it into words on anything but
// letters and digits; a word of 3+ Han characters also yields its
// two-character pieces.
func searchTerms(query string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		add(w)
		runes := []rune(w)
		if len(runes) >= 3 && unicode.Is(unicode.Han, runes[0]) {
			for i := 0; i+2 <= len(runes); i++ {
				add(string(runes[i : i+2]))
			}
		}
	}
	return out
}
