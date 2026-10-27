package chatui_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoInternalImports keeps the public chat UI package usable by other modules.
func TestNoInternalImports(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{join .Imports \" \"}}", "./...")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "/internal/") {
			t.Fatalf("pkg/chatui imports internal package %q", dep)
		}
	}
}
