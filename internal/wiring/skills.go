package wiring

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	gkconfig "github.com/gookit/config/v2"

	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	capskills "github.com/inhere/suga/pkg/agentcore/skills"
	builtinskills "github.com/inhere/suga/skills"
)

// buildDefaultSkillRegistry scans the skill roots once at startup, releasing
// the built-in skills into <SUGA_HOME>/skills first so they are found like any
// other skill.
func buildDefaultSkillRegistry(workspace string) *capskills.Registry {
	sugaHome := sugaHomeWithBuiltinSkills()
	homeDir, _ := os.UserHomeDir()
	return capskills.LoadRoots(defaultSkillRoots(workspace, homeDir, sugaHome))
}

// buildReadOnlySkillRegistry is the skill scan of the read-only diagnose
// commands (LoadManifest and LoadLocalEnv): the roots are read as they are,
// without releasing the built-in skills first, and a built-in skill not on
// disk is enumerated from the embedded resources instead of being written.
// A built-in skill is registered only when no scanned skill of the same name
// exists, so a released one and the user's own skill of that name keep the
// priority the roots list gives them. The embedded entries carry no Path and
// Dir: they are counted and listed, but a body is loaded only from disk (the
// session side, which releases first).
func buildReadOnlySkillRegistry(workspace string) *capskills.Registry {
	sugaHome, err := appconfig.ResolveSugaHome()
	if err != nil {
		slog.Warn("cannot resolve SUGA_HOME for the skill roots", "err", err)
		sugaHome = ""
	}
	homeDir, _ := os.UserHomeDir()
	reg := capskills.LoadRoots(defaultSkillRoots(workspace, homeDir, sugaHome))
	names, err := builtinSkillNames(builtinskills.FS)
	if err != nil {
		slog.Warn("cannot list the built-in skills", "err", err)
		return reg
	}
	for _, name := range names {
		if _, ok := reg.Get(name); ok {
			continue // already released, or the user's own skill of that name
		}
		data, err := fs.ReadFile(builtinskills.FS, path.Join(name, "SKILL.md"))
		if err != nil {
			slog.Warn("cannot read the built-in skill", "name", name, "err", err)
			continue
		}
		skill, _, err := capskills.Parse(data)
		if err != nil {
			slog.Warn("skip invalid built-in skill", "name", name, "err", err)
			continue
		}
		reg.Register(skill)
	}
	return reg
}

// defaultSkillRoots lists the skill directories in priority order: the
// workspace's own skills first, so a project can override a user skill of the
// same name, then the suga home (the built-in guide and user skills next to it)
// and finally the user's own ~/.agents/skills. sugaHome is passed in apart from
// homeDir because SUGA_HOME may point somewhere else than the home directory.
func defaultSkillRoots(workspace, homeDir, sugaHome string) []capskills.Root {
	roots := make([]capskills.Root, 0, 4)
	if strings.TrimSpace(workspace) != "" {
		roots = append(roots,
			capskills.Root{Dir: filepath.Join(workspace, ".agents", "skills"), Workspace: true},
			capskills.Root{Dir: filepath.Join(workspace, "skills"), Workspace: true},
		)
	}
	if strings.TrimSpace(sugaHome) != "" {
		roots = append(roots, capskills.Root{Dir: filepath.Join(sugaHome, builtinSkillsRoot)})
	}
	if strings.TrimSpace(homeDir) != "" {
		roots = append(roots, capskills.Root{Dir: filepath.Join(homeDir, ".agents", "skills")})
	}
	return roots
}

// skillRoots resolves the home directory and SUGA_HOME and returns this
// machine's skill roots in priority order (the manifest lists them).
func skillRoots(workspace string) []capskills.Root {
	homeDir, _ := os.UserHomeDir()
	sugaHome, err := appconfig.ResolveSugaHome()
	if err != nil {
		sugaHome = ""
	}
	return defaultSkillRoots(workspace, homeDir, sugaHome)
}

const (
	// maxSkillDescription bounds one skill line in the skills listing.
	maxSkillDescription = 160
	// defaultSkillListMax is how many skills are listed before the listing
	// falls back to workspace skills plus search (skills.list_max).
	defaultSkillListMax = 15
	// maxPreloadBytes bounds the body of one always_load skill in the prompt.
	maxPreloadBytes = 8000
	// maxSkillNamesBytes bounds the names-only line of the skills past
	// list_max; the rest are counted and found through search.
	maxSkillNamesBytes = 3000
)

// skillPolicy is how the skills listing (segment context) presents skills.
type skillPolicy struct {
	// listMax: with more skills than this, only workspace skills are listed
	// and the rest are found through skill(query=...).
	listMax int
	// alwaysLoad names skills (lower case) whose body goes into the prompt,
	// in addition to those marked always_load in their frontmatter.
	alwaysLoad map[string]bool
}

// readSkillPolicy reads skills.list_max and skills.always_load.
func readSkillPolicy() skillPolicy {
	p := skillPolicy{listMax: gkconfig.Int("skills.list_max", defaultSkillListMax), alwaysLoad: map[string]bool{}}
	for _, name := range gkconfig.Strings("skills.always_load") {
		p.alwaysLoad[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return p
}

// skillsPrompt is the segment context section for skills ("" when there are
// none) and how many skills it presents in which form. Preloaded
// (always_load) skills appear with their body. The others are listed as
// "name — description" while they number at most listMax; beyond that only
// workspace skills are listed (up to listMax) and the rest by name only (up
// to maxSkillNamesBytes), so the model knows every skill without a large
// personal collection costing each turn its whole listing.
func skillsPrompt(reg *capskills.Registry, pol skillPolicy) (string, personal.SkillsStats) {
	plan := planSkills(reg, pol)
	stats := personal.SkillsStats{Total: plan.total, Listed: len(plan.listed), Named: len(plan.named), Preloaded: len(plan.preload)}
	if plan.total == 0 {
		return "", stats
	}
	listed, preload := plan.listed, plan.preload

	var b strings.Builder
	b.WriteString("# Skills\n")
	b.WriteString("Skills are task instructions. When a task matches one, load it with the skill tool (name) before acting.\n")
	for _, s := range listed {
		desc := s.Description
		if len(desc) > maxSkillDescription {
			desc = strings.TrimSpace(desc[:utf8Cut(desc, maxSkillDescription)]) + "…"
		}
		fmt.Fprintf(&b, "- %s — %s\n", s.Name, desc)
	}
	if names, unnamed := plan.named, plan.unnamed; len(names) > 0 || unnamed > 0 {
		b.WriteString("Also installed (no description here; load by name with the skill tool, or search with its query argument when unsure")
		if len(names) > 0 {
			b.WriteString("): " + strings.Join(names, ", "))
		} else {
			b.WriteString(")")
		}
		if unnamed > 0 {
			fmt.Fprintf(&b, " … and %d more (query \"*\" lists all names)", unnamed)
		}
		b.WriteString(".\n")
	}
	for _, s := range preload {
		_, body, err := capskills.ParseFile(s.Path)
		if err != nil {
			slog.Warn("skip always_load skill", "name", s.Name, "err", err)
			continue
		}
		if len(body) > maxPreloadBytes {
			body = body[:utf8Cut(body, maxPreloadBytes)] + "\n…[truncated; load the rest with the skill tool]"
		}
		fmt.Fprintf(&b, "\n## Skill %s (preloaded; directory %s)\n%s\n", s.Name, s.Dir, strings.TrimSpace(body))
	}
	return b.String(), stats
}

// SkillInfo is one loaded skill as chat's :skills lists it: its name, the
// one-line description, where it came from and the SKILL.md it was read from.
type SkillInfo struct {
	Name        string
	Description string
	// Source is the kind of the root the skill was found in: 项目 (a workspace
	// skills directory), 内置 (the skills released into SUGA_HOME) or 用户 (the
	// user's own ~/.agents/skills).
	Source     string
	Path       string
	AlwaysLoad bool
}

// source names of SkillInfo.Source.
const (
	SkillSourceProject = "项目"
	SkillSourceBuiltin = "内置"
	SkillSourceUser    = "用户"
)

// SkillInfos lists the registered skills sorted by name (Registry.List order)
// with the source of each. The registry keeps no source itself: it is derived
// from the root a skill was found under, and a workspace skill wins the name,
// so Workspace decides 项目 and SUGA_HOME/skills holds the released built-ins.
func SkillInfos(reg *capskills.Registry) []SkillInfo {
	if reg == nil {
		return nil
	}
	sugaHome, _ := appconfig.ResolveSugaHome()
	builtinRoot := ""
	if strings.TrimSpace(sugaHome) != "" {
		builtinRoot = filepath.Join(sugaHome, builtinSkillsRoot)
	}
	out := make([]SkillInfo, 0, reg.Len())
	for _, s := range reg.List() {
		out = append(out, SkillInfo{
			Name: s.Name, Description: s.Description, Source: skillSource(s, builtinRoot),
			Path: s.Path, AlwaysLoad: s.AlwaysLoad,
		})
	}
	return out
}

// skillSource classifies one skill by the root it lives in.
func skillSource(s capskills.Skill, builtinRoot string) string {
	if s.Workspace {
		return SkillSourceProject
	}
	if builtinRoot != "" && pathWithin(s.Path, builtinRoot) {
		return SkillSourceBuiltin
	}
	return SkillSourceUser
}

// pathWithin reports whether p is under dir (both cleaned, separator aware).
func pathWithin(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// skillPlan is how the skills listing presents the installed skills.
type skillPlan struct {
	preload, listed []capskills.Skill
	named           []string // by name only
	unnamed         int      // neither listed nor named (over maxSkillNamesBytes)
	total           int
}

// planSkills splits the installed skills by presentation: preloaded
// (always_load), listed with their description (all while they number at
// most listMax, else the workspace ones up to listMax), and the rest by name.
func planSkills(reg *capskills.Registry, pol skillPolicy) skillPlan {
	all := reg.List()
	plan := skillPlan{total: len(all)}
	var rest []capskills.Skill
	for _, s := range all {
		if s.AlwaysLoad || pol.alwaysLoad[strings.ToLower(s.Name)] {
			plan.preload = append(plan.preload, s)
		} else {
			rest = append(rest, s)
		}
	}
	for name := range pol.alwaysLoad {
		if _, ok := reg.Get(name); !ok {
			slog.Warn("skills.always_load names an unknown skill", "name", name)
		}
	}
	plan.listed = rest
	if len(rest) > pol.listMax {
		plan.listed = nil
		for _, s := range rest {
			if s.Workspace && len(plan.listed) < pol.listMax {
				plan.listed = append(plan.listed, s)
			}
		}
	}
	plan.named, plan.unnamed = unlistedSkillNames(rest, plan.listed)
	return plan
}

// unlistedSkillNames returns the names of the skills in rest that are not
// listed, within maxSkillNamesBytes, and how many did not fit.
func unlistedSkillNames(rest, listed []capskills.Skill) (names []string, unnamed int) {
	shown := make(map[string]bool, len(listed))
	for _, s := range listed {
		shown[s.Name] = true
	}
	size := 0
	for _, s := range rest {
		if shown[s.Name] {
			continue
		}
		if size+len(s.Name)+2 > maxSkillNamesBytes {
			unnamed++
			continue
		}
		names = append(names, s.Name)
		size += len(s.Name) + 2
	}
	return names, unnamed
}

// utf8Cut moves cut back to a rune start.
func utf8Cut(s string, cut int) int {
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}
