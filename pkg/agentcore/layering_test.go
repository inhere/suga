package agentcore_test

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	// sugaModule is the module of the application around agentcore.
	sugaModule = "github.com/inhere/suga/"
	// agentRoot is the subtree this guard covers.
	agentRoot = "github.com/inhere/suga/pkg/agentcore"
)

// layerAllowed is the layering contract of pkg/agentcore written as a table:
// each package of the subtree may import only the agentcore packages listed
// for it, and production, in-package test and external test imports all have
// to be covered by the same entry. Keys and values are paths relative to
// pkg/agentcore; "." is the root package itself, "internal/..." the shared
// base below the contract, "examples/..." the embedding demos.
//
// The table is the measured state of 2026-10-16 (see
// docs/research/2026-10-16-agentcore-layering-analysis.md), so it also states
// the direction of the dependencies, in production code: the kernel ("." and
// "api") imports only api and internal/*, no capability package imports the
// kernel root (only the test suites above it and storage/files' own test do),
// and the capability layer is a DAG whose top is tools/native. A new edge -
// e.g. tools -> tasks - has to be added here on purpose, which is the point of
// the table.
//
// Keeping it true to the code: a package without an entry, a dependency
// outside its entry and a stale entry all fail the test; the failure message
// names the entry to change.
var layerAllowed = map[string][]string{
	// kernel: the runtime loop and the contracts
	".":   {"api", "internal/runid", "internal/utils", "storage/files", "testing", "tools"},
	"api": {"internal/utils"},
	// shared base below the contracts (Go's internal rule keeps it inside the
	// subtree as well)
	"internal/proctree": {},
	"internal/runid":    {},
	"internal/utils":    {},
	// capability layer
	"extproc":          {},
	"llm":              {"api", "netproxy"},
	"memory":           {"redact"},
	"memory/execstore": {"extproc", "memory"},
	"memory/files":     {"memory"},
	"netproxy":         {},
	"redact":           {},
	"schedule":         {},
	"skills":           {},
	"storage/files":    {".", "api", "internal/runid", "internal/utils"},
	"tasks":            {"api", "internal/proctree"},
	"tools":            {"api", "testing"},
	"tools/rules":      {},
	"tools/mcp":        {"api", "extproc", "tools"},
	"tools/native":     {"api", "internal/proctree", "memory", "memory/files", "netproxy", "redact", "skills", "tasks", "testutil", "tools", "tools/native/codeintel"},
	// the pure analysis below the tool shell: no path policy, no file system
	"tools/native/codeintel": {},
	// host-side test suites above the kernel
	"mock":     {".", "api", "tools"},
	"testing":  {".", "mock", "storage/files", "tools"},
	"testutil": {},
	// embedding demos: they compose the whole library, but only downwards
	"examples/authority":   {".", "storage/files", "tools"},
	"examples/custom":      {".", "mock", "storage/files", "tools"},
	"examples/host":        {".", "mock", "storage/files", "tools"},
	"examples/minimal":     {".", "mock", "storage/files"},
	"examples/policies":    {".", "mock", "storage/files", "tools"},
	"examples/policyhost":  {".", "mock", "storage/files", "tools"},
	"examples/recommended": {".", "llm", "mock", "skills", "tools", "tools/mcp", "tools/native"},
	"examples/resume":      {".", "mock", "storage/files", "tools"},
	"examples/stream":      {".", "mock", "storage/files"},
}

// TestLayering keeps pkg/agentcore a self-contained tree: no package of the
// subtree may depend on another github.com/inhere/suga package outside it, and
// each package may depend only on what layerAllowed registers for it.
func TestLayering(t *testing.T) {
	pkgs := listAgentcorePackages(t)
	known := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		known[relName(p.path)] = true
	}

	t.Run("no dependency outside agentcore", func(t *testing.T) {
		var leaks []string
		for _, p := range pkgs {
			for _, dep := range p.deps {
				if strings.HasPrefix(dep, sugaModule) && !isAgentcore(dep) {
					leaks = append(leaks, fmt.Sprintf("%s imports %s", relName(p.path), dep))
				}
			}
		}
		if len(leaks) > 0 {
			t.Fatalf("pkg/agentcore must not depend on the suga application; found:\n  %s\nMove the shared code into pkg/agentcore or keep the dependency in the host (internal/*).",
				strings.Join(leaks, "\n  "))
		}
	})

	t.Run("only registered agentcore dependencies", func(t *testing.T) {
		var violations []string
		for _, p := range pkgs {
			name := relName(p.path)
			allowed, ok := layerAllowed[name]
			if !ok {
				violations = append(violations, fmt.Sprintf("%s is a new package: add %q: {%s} to layerAllowed in pkg/agentcore/layering_test.go",
					name, name, quoteList(agentcoreDeps(p.deps))))
				continue
			}
			for _, dep := range agentcoreDeps(p.deps) {
				if !slices.Contains(allowed, dep) {
					violations = append(violations, fmt.Sprintf("%s imports %s, which layerAllowed[%q] does not list: if the edge is intended, add %q there; otherwise drop the import",
						name, dep, name, dep))
				}
			}
		}
		if len(violations) > 0 {
			t.Fatalf("pkg/agentcore dependencies must stay inside their layer; found:\n  %s", strings.Join(violations, "\n  "))
		}
	})

	t.Run("the table names current packages", func(t *testing.T) {
		for name, allowed := range layerAllowed {
			if !known[name] {
				t.Errorf("layerAllowed[%q] has no package under pkg/agentcore any more: remove the entry", name)
			}
			for _, dep := range allowed {
				if !known[dep] {
					t.Errorf("layerAllowed[%q] lists %q, which is not a package under pkg/agentcore: fix the entry", name, dep)
				}
			}
		}
	})
}

// listedPackage is one package of the subtree with its imports.
type listedPackage struct {
	path string   // full import path
	deps []string // sorted unique imports, production and test merged, without itself
}

// listAgentcorePackages returns the packages of pkg/agentcore with their
// direct imports. The test runs in the pkg/agentcore directory, so ./... is
// the subtree.
func listAgentcorePackages(t *testing.T) []listedPackage {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("the layering guard needs the go command: %v", err)
	}
	const format = "{{.ImportPath}}|{{join .Imports \" \"}}|{{join .TestImports \" \"}}|{{join .XTestImports \" \"}}"
	cmd := exec.Command(goBin, "list", "-f", format, "./...")
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=readonly")
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v\n%s", err, stderr)
	}
	var pkgs []listedPackage
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			t.Fatalf("unexpected go list output %q: want 4 |-separated fields", line)
		}
		p := listedPackage{path: fields[0]}
		seen := map[string]bool{}
		// A test variant imports the package under test: that self reference is
		// not a dependency and stays out of the table.
		seen[p.path] = true
		for _, group := range fields[1:] {
			for _, dep := range strings.Fields(group) {
				if seen[dep] {
					continue
				}
				seen[dep] = true
				p.deps = append(p.deps, dep)
			}
		}
		slices.Sort(p.deps)
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no package under pkg/agentcore")
	}
	return pkgs
}

// isAgentcore reports whether path is pkg/agentcore or a package inside it.
// The trailing slash keeps a sibling such as pkg/agentcoreX out of the subtree.
func isAgentcore(path string) bool {
	return path == agentRoot || strings.HasPrefix(path, agentRoot+"/")
}

// relName is path relative to pkg/agentcore, "." for the root package.
func relName(path string) string {
	if path == agentRoot {
		return "."
	}
	return strings.TrimPrefix(path, agentRoot+"/")
}

// agentcoreDeps keeps the imports into the subtree, as relative names.
func agentcoreDeps(deps []string) []string {
	var out []string
	for _, dep := range deps {
		if isAgentcore(dep) {
			out = append(out, relName(dep))
		}
	}
	return out
}

// quoteList renders names for a table entry in a failure message.
func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return strings.Join(quoted, ", ")
}
