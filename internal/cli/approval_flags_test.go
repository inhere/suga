package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
)

// runApproval runs `suga run` through the real command dispatch.
func runApproval(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	argv := append([]string{"run", "--prompt", "hi", "--output-format", "stream-json"}, args...)
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs(argv)
	return stdout.String(), stderr.String(), err
}

func firstJSONLine(t *testing.T, out string) map[string]any {
	t.Helper()
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	var row map[string]any
	assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(line), &row)))
	return row
}

// turnContextAttr reads one attribute of the session's turn_context event.
func turnContextAttr(t *testing.T, sessionID, key string) (any, bool) {
	t.Helper()
	dbPath, err := appconfig.ResolveCoreStorePath()
	assert.Require(t, assert.NoErr(t, err))
	repo, err := store.Open(dbPath)
	assert.Require(t, assert.NoErr(t, err))
	defer func() { _ = repo.Close() }()
	events, err := repo.ListEvents(context.Background(), store.EventFilter{SessionID: sessionID, Kinds: []string{store.EventTurnContext}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NotEmpty(t, events))
	value, ok := events[0].Attrs[key]
	return value, ok
}

func TestRunApproveFlags(t *testing.T) {
	setupAddDirsRun(t)
	setTestProvider(t, addDirsServer(t).URL)

	t.Run("--yolo is trust: session line and turn_context record it", func(t *testing.T) {
		stdout, _, err := runApproval(t, "--yolo", "--session-id", "approve-yolo")
		assert.NoErr(t, err)
		assert.Eq(t, "trust", firstJSONLine(t, stdout)["approval_mode"])
		value, ok := turnContextAttr(t, "approve-yolo", "approval_mode")
		assert.True(t, ok)
		assert.Eq(t, "trust", value)
	})
	t.Run("--approve trust", func(t *testing.T) {
		stdout, _, err := runApproval(t, "--approve", "trust", "--session-id", "approve-trust")
		assert.NoErr(t, err)
		assert.Eq(t, "trust", firstJSONLine(t, stdout)["approval_mode"])
	})
	t.Run("--approve ask and auto are recorded as given", func(t *testing.T) {
		for _, tier := range []string{"ask", "auto"} {
			stdout, _, err := runApproval(t, "--approve", tier, "--session-id", "approve-"+tier)
			assert.NoErr(t, err)
			assert.Eq(t, tier, firstJSONLine(t, stdout)["approval_mode"])
		}
	})
	t.Run("no flag leaves the output as it was", func(t *testing.T) {
		stdout, _, err := runApproval(t, "--session-id", "approve-none")
		assert.NoErr(t, err)
		_, has := firstJSONLine(t, stdout)["approval_mode"]
		assert.False(t, has)
		_, ok := turnContextAttr(t, "approve-none", "approval_mode")
		assert.False(t, ok)
	})
	t.Run("a bad value and the --yolo conflict are usage errors", func(t *testing.T) {
		_, _, err := runApproval(t, "--approve", "x")
		assert.ErrMsgContains(t, err, "invalid approval mode")
		_, _, err = runApproval(t, "--yolo", "--approve", "trust")
		assert.ErrMsgContains(t, err, "cannot be combined")
	})
	t.Run("--read-only wins over trust and says so", func(t *testing.T) {
		_, stderr, err := runApproval(t, "--yolo", "--read-only", "--session-id", "approve-ro")
		assert.NoErr(t, err)
		assert.StrContains(t, stderr, "--read-only takes priority over --approve trust")
		_, _, err = runApproval(t, "--approve", "auto", "--read-only", "--session-id", "approve-ro2")
		assert.NoErr(t, err)
	})
}

func TestChatApproveFlagsAreValidated(t *testing.T) {
	setupAddDirsRun(t)
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"chat", "--yolo", "--approve", "ask"})
	assert.ErrMsgContains(t, err, "cannot be combined")
	err = newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"chat", "--approve", "x"})
	assert.ErrMsgContains(t, err, "invalid approval mode")
}

func TestACPApproveFlags(t *testing.T) {
	setupAddDirsRun(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"bad value", []string{"acp", "--approve", "x"}, "invalid approval mode"},
		{"conflict", []string{"acp", "--yolo", "--approve", "trust"}, "cannot be combined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs(tc.args)
			assert.ErrMsgContains(t, err, tc.want)
		})
	}
	t.Run("the flags are accepted (parsing only: serving ends on an empty stdin)", func(t *testing.T) {
		for _, args := range [][]string{{"acp", "--approve", "trust"}, {"acp", "--yolo"}} {
			var stdout, stderr bytes.Buffer
			err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs(args)
			if err != nil {
				assert.StrNotContains(t, err.Error(), "flag")
			}
		}
	})
}

func TestConfigRefusesTrustTier(t *testing.T) {
	setupConfigCLI(t, "model: openai/config\n")
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"config", "set", "tools.approval", "trust"})
	assert.Error(t, err)
}
