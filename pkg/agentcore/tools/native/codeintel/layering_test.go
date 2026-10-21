package codeintel_test

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	// codeintelImport is the package this guard keeps at the bottom of the
	// tools/native layer.
	codeintelImport = "github.com/inhere/suga/pkg/agentcore/tools/native/codeintel"
	// nativeImport is the tool layer above it: the analysis must never depend on
	// the tool shell (design docs/design/2026-10-22-lang-rules-design.md, section 7).
	nativeImport = "github.com/inhere/suga/pkg/agentcore/tools/native"
	// sugaModule is the module of the application around agentcore.
	sugaModule = "github.com/inhere/suga/"
)

// TestCodeintelDoesNotImportNative keeps the dependency one way: codeintel holds
// the pure analysis (a file's name and content in, symbols and references out),
// the tool shell in native resolves paths, reads files and renders results. The
// rule sits here as well as in pkg/agentcore/layering_test.go so a broken edge is
// reported by the package that broke it. Production, in-package test and
// external test imports are all checked.
func TestCodeintelDoesNotImportNative(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("the layering guard needs the go command: %v", err)
	}
	const format = "{{join .Imports \" \"}}|{{join .TestImports \" \"}}|{{join .XTestImports \" \"}}"
	cmd := exec.Command(goBin, "list", "-f", format, codeintelImport)
	cmd.Env = append(cmd.Environ(), "GOFLAGS=-mod=readonly")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}
	for _, group := range strings.Split(strings.TrimSpace(string(out)), "|") {
		for _, dep := range strings.Fields(group) {
			switch {
			case dep == nativeImport || strings.HasPrefix(dep, nativeImport+"/"):
				t.Errorf("codeintel imports %s: the analysis must take a path and its content as arguments instead", dep)
			case strings.HasPrefix(dep, sugaModule) && !strings.HasPrefix(dep, codeintelImport[:strings.LastIndex(codeintelImport, "/")]):
				t.Errorf("codeintel imports %s, which is outside pkg/agentcore/tools: keep the analysis self-contained", dep)
			}
		}
	}
}
