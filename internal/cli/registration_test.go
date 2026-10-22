package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
)

func TestCLIRegistersOnboardingCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"--help"})
	assert.NoErr(t, err)
	for _, name := range []string{"init", "config", "models"} {
		assert.StrContains(t, stdout.String(), name)
	}
	assert.StrContains(t, stdout.String(), "suga config get model")
}

func TestConfigGroupHelpListsLeavesWithoutWriting(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("SUGA_HOME", home)
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config"})
	assert.NoErr(t, err)
	for _, name := range []string{"get", "set", "unset", "list", "path"} {
		assert.StrContains(t, stderr.String(), name)
	}
	_, statErr := os.Stat(home)
	assert.True(t, errors.Is(statErr, os.ErrNotExist))
}

func TestOnboardingLeavesReachTheirCappHandlers(t *testing.T) {
	t.Run("config get", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "home")
		t.Setenv("SUGA_HOME", home)
		assert.NoErr(t, os.MkdirAll(home, 0o700))
		assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte("model: openai/test\n"), 0o600))
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		assert.NoErr(t, appconfig.Load())
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "get", "model"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "model = openai/test")
		assert.StrContains(t, stdout.String(), "source = config:")
	})

	t.Run("models json reaches its offline handler", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/test\n")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"models", "--json"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), `"offline": true`)
		assert.StrContains(t, stdout.String(), `"ref": "openai/test"`)
	})
}

// TestGroupHelpListsLeaves: a group without a subcommand only prints its own
// help, which must name every subcommand it holds (v0.36 command groups).
func TestGroupHelpListsLeaves(t *testing.T) {
	for _, tc := range []struct {
		group  string
		leaves []string
	}{
		{group: "inspect", leaves: []string{"trace", "stats", "export", "manifest", "rules"}},
		{group: "data", leaves: []string{"gc", "bodies", "vacuum", "compress-spill", "backup", "restore"}},
	} {
		t.Run(tc.group, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{tc.group})
			assert.NoErr(t, err)
			for _, name := range tc.leaves {
				assert.StrContains(t, stderr.String(), name)
			}
		})
	}
}

func TestConfigLeafHelpKeepsItsPositionalContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "--help"})
	assert.NoErr(t, err)
	assert.StrContains(t, stderr.String(), "key")
	assert.StrContains(t, stderr.String(), "value")
	assert.StrNotContains(t, stderr.String(), "SUBCOMMAND")
}
