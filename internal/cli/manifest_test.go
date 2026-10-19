package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/wiring"
)

// manifestCLIEnv isolates SUGA_HOME and the workspace (outside any git
// repository, so memory never selects gofer) and configures a fake provider
// that must never be contacted.
func manifestCLIEnv(t *testing.T) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	ws := t.TempDir()
	t.Chdir(ws)
	assert.NoErr(t, gkconfig.Set("workspace", ws))
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	assert.NoErr(t, gkconfig.Set("memory.project.backend", "files"))
	setTestProvider(t, "http://127.0.0.1:1/v1") // nothing listens: a request would fail the command
}

func TestManifestCommand(t *testing.T) {
	manifestCLIEnv(t)

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"inspect", "manifest", "--json"})
		assert.Require(t, assert.NoErr(t, err))
		var m wiring.Manifest
		assert.Require(t, assert.NoErr(t, json.Unmarshal(stdout.Bytes(), &m)))
		assert.Eq(t, "dev", m.Version)
		assert.Eq(t, "gpt-4o", m.Models.Main.Model)
		assert.True(t, len(m.Tools) > 0)
		assert.Len(t, m.Memory, 2)
		assert.Eq(t, "files", m.Memory[1].Backend)
		assert.True(t, m.Output.InlineBytes > 0)
	})
	t.Run("text has every section", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"inspect", "manifest"})
		assert.Require(t, assert.NoErr(t, err))
		for _, section := range []string{"Models", "Tools", "MCP servers", "Skills", "Memory", "Output", "Taint", "suga_manifest", "write_tainted="} {
			assert.StrContains(t, stdout.String(), section)
		}
	})
	t.Run("rejects positional arguments", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"inspect", "manifest", "extra"})
		assert.Err(t, err)
	})
}
