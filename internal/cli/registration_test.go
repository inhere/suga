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
		{group: "session", leaves: []string{"list", "compact", "undo"}},
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

// TestRemovedTopLevelSessionCommands pins that `suga sessions`, `suga compact`
// and `suga undo` are gone from the command tree: they moved into the `session`
// group (user 2026-10-27). The old spellings still run because NormalizeArgs
// forwards them before the tree sees them (TestNormalizeArgsForwardsSession);
// the tree itself must not register them again.
func TestRemovedTopLevelSessionCommands(t *testing.T) {
	for _, old := range []string{"sessions", "compact", "undo"} {
		t.Run(old, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{old, "x"})
			assert.Require(t, assert.Err(t, err))
			assert.ErrMsgContains(t, err, old)
			assert.ErrMsgContains(t, err, "not exists")
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

// TestNormalizeArgsForwardsSession covers the forwarding of the session
// commands that left the top level: the rewritten arguments and the one-line
// notice in the configured language.
func TestNormalizeArgsForwardsSession(t *testing.T) {
	var notice bytes.Buffer
	prev := normalizeNotice
	normalizeNotice = &notice
	t.Cleanup(func() { normalizeNotice = prev })

	t.Run("rewrites to the session group", func(t *testing.T) {
		notice.Reset()
		assert.Eq(t, []string{"session", "list", "--all"}, NormalizeArgs([]string{"sessions", "--all"}))
		assert.Eq(t, []string{"session", "compact", "sid"}, NormalizeArgs([]string{"compact", "sid"}))
		assert.Eq(t, []string{"session", "undo", "sid", "2"}, NormalizeArgs([]string{"undo", "sid", "2"}))
		assert.StrContains(t, notice.String(), "suga sessions 已移到 suga session list")
	})
	t.Run("english notice", func(t *testing.T) {
		notice.Reset()
		t.Setenv("SUGA_LANGUAGE", "en")
		NormalizeArgs([]string{"undo"})
		assert.StrContains(t, notice.String(), "suga undo moved to suga session undo")
	})
	t.Run("other commands are untouched", func(t *testing.T) {
		notice.Reset()
		assert.Eq(t, []string{"send", "x"}, NormalizeArgs([]string{"send", "x"}))
		assert.Eq(t, "", notice.String())
	})
}
