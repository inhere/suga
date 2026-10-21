package native

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
)

// The dependency root detection of the language registry: which local caches
// and repositories of this machine the read-only file tools may read. It stays
// in this package because it binds the read root type and probes the file
// system; codeintel only declares the languages (design
// docs/design/2026-10-22-lang-rules-design.md, section 7: the probes are
// attached by language name).

// langEnv is what the dependency root probes read: the user home directory and
// the Go environment (the process environment over the go env file).
type langEnv struct {
	home  string
	goVar func(key string) string
}

// newLangEnv gathers the environment once for all the probes.
func newLangEnv() langEnv {
	home, _ := os.UserHomeDir()
	goEnv := readGoEnvFile()
	return langEnv{home: home, goVar: func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return goEnv[key]
	}}
}

// rootCollector gathers the dependency roots of the languages of one detection
// run. A directory that is empty, relative or missing is left out, exactly as
// the per-language detection did before the split.
type rootCollector struct{ out []ReadRoot }

// add appends label: dir when dir is an absolute path of an existing directory.
func (c *rootCollector) add(label, dir string) {
	if dir == "" || !filepath.IsAbs(dir) {
		return
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return
	}
	c.out = append(c.out, ReadRoot{Label: label, Path: filepath.Clean(dir)})
}

// dependencyRootProbes attaches a probe to the declared languages that have
// local dependency caches: Go, Rust and Java. A language without an entry (JS,
// Python, PHP) keeps its packages in the project or the interpreter, so it
// needs no read-only root.
var dependencyRootProbes = map[string]func(env langEnv, add *rootCollector){
	"Go":   goDependencyRoots,
	"Rust": rustDependencyRoots,
	"Java": javaDependencyRoots,
}

// DependencyRoots detects the local dependency caches and repositories of every
// declared language, in Languages() order: the Go module cache, the Go standard
// library sources, the Rust crate sources, the Maven local repository and the
// Gradle module cache. Only directories holding dependency files are returned,
// never a whole tool home (~/.cargo, ~/.gradle and ~/.m2 hold credentials or
// build state too). Directories that do not exist are left out. It runs no
// external command: the go env file, environment variables and the go binary
// location in PATH are enough.
//
// A pathless file_grep / file_find only searches the workspace, so these large
// roots are walked only when the model passes one of them as the search path
// (such a walk stops at searchTimeout and at the tool's result limit).
func DependencyRoots() []ReadRoot {
	env, collector := newLangEnv(), &rootCollector{}
	for _, lang := range codeintel.Languages() {
		if probe := dependencyRootProbes[lang.Name]; probe != nil {
			probe(env, collector)
		}
	}
	return collector.out
}

// goDependencyRoots adds Go's dependency roots: the module cache and the
// standard library sources.
func goDependencyRoots(env langEnv, add *rootCollector) {
	// GOMODCACHE, else GOPATH/pkg/mod, else ~/go/pkg/mod, and GOROOT/src
	// (GOROOT from the environment or from the go binary in PATH).
	modCache := env.goVar("GOMODCACHE")
	if modCache == "" {
		if paths := filepath.SplitList(env.goVar("GOPATH")); len(paths) > 0 && paths[0] != "" {
			modCache = filepath.Join(paths[0], "pkg", "mod")
		} else if env.home != "" {
			modCache = filepath.Join(env.home, "go", "pkg", "mod")
		}
	}
	add.add("Go modules", modCache)
	if goRoot := goRootDir(env.goVar("GOROOT")); goRoot != "" {
		add.add("Go standard library", filepath.Join(goRoot, "src"))
	}
}

// readGoEnvFile parses the file `go env -w` writes (GOENV, default
// <UserConfigDir>/go/env): KEY=VALUE lines. Missing or unreadable is empty.
// GOENV=off means no file: the process environment is then the only source.
func readGoEnvFile() map[string]string {
	path := os.Getenv("GOENV")
	if path == "off" {
		return nil
	}
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil
		}
		path = filepath.Join(dir, "go", "env")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	vars := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && key != "" && !strings.HasPrefix(key, "#") {
			vars[key] = value
		}
	}
	return vars
}

// goRootDir returns GOROOT: the explicit value, or the directory two levels
// above the go binary found in PATH (<goroot>/bin/go).
func goRootDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	bin, err := exec.LookPath("go")
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		bin = real
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(bin))
}

// javaDependencyRoots adds Java's dependency roots: the Maven local repository
// and the Gradle module cache.
func javaDependencyRoots(env langEnv, add *rootCollector) {
	// The local Maven repository (a settings.xml may move it elsewhere) and
	// the Gradle module cache hold the jars and poms, addressed by groupId
	// path: javap and the file tools work straight off these paths.
	if env.home != "" {
		repo := readMavenLocalRepository(filepath.Join(env.home, ".m2", "settings.xml"), env.home)
		if repo == "" {
			repo = filepath.Join(env.home, ".m2", "repository")
		}
		add.add("Maven local repository", repo)
	}
	gradleHome := os.Getenv("GRADLE_USER_HOME")
	if gradleHome == "" && env.home != "" {
		gradleHome = filepath.Join(env.home, ".gradle")
	}
	if gradleHome != "" {
		add.add("Gradle module cache", filepath.Join(gradleHome, "caches", "modules-2", "files-2.1"))
	}
}

// readMavenLocalRepository returns the <localRepository> of a Maven
// settings.xml (the one place the local repository is configured), with
// ${user.home} resolved; a commented-out element does not count. "" when the
// file is missing or holds no usable value.
func readMavenLocalRepository(path, home string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := stripXMLComments(string(data))
	start := strings.Index(text, "<localRepository>")
	if start < 0 {
		return ""
	}
	rest := text[start+len("<localRepository>"):]
	end := strings.Index(rest, "</localRepository>")
	if end < 0 {
		return ""
	}
	value := strings.TrimSpace(rest[:end])
	if home != "" {
		value = strings.ReplaceAll(value, "${user.home}", home)
	}
	return value
}

// stripXMLComments removes every <!-- ... --> section; an unclosed comment
// swallows the rest of the text.
func stripXMLComments(s string) string {
	for {
		start := strings.Index(s, "<!--")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start+len("<!--"):], "-->")
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + s[start+len("<!--")+end+len("-->"):]
	}
}

// rustDependencyRoots adds the unpacked crate sources of CARGO_HOME.
func rustDependencyRoots(env langEnv, add *rootCollector) {
	cargoHome := os.Getenv("CARGO_HOME")
	if cargoHome == "" && env.home != "" {
		cargoHome = filepath.Join(env.home, ".cargo")
	}
	if cargoHome != "" {
		add.add("Rust crates", filepath.Join(cargoHome, "registry", "src"))
	}
}
