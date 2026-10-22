package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
)

func TestFormatVersionIncludesAvailableBuildMetadata(t *testing.T) {
	assert.Eq(t, "suga dev (dev)", formatVersion("dev", ""))
	assert.Eq(t, "suga 1.2.3 (abc123)", formatVersion("1.2.3", "abc123"))
}

func TestExitReportInterrupted(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	msg, code := exitReport(canceled, fmt.Errorf("run turn: %w", context.Canceled))
	assert.Eq(t, "已中断", msg)
	assert.Eq(t, 130, code)

	// A canceled error without the signal context canceled is reported as is.
	msg, code = exitReport(context.Background(), context.Canceled)
	assert.Eq(t, "context canceled", msg)
	assert.Eq(t, 1, code)

	msg, code = exitReport(canceled, personal.SessionNotFoundError{ID: "x"})
	assert.Eq(t, "session not found: x", msg)
	assert.Eq(t, 4, code)
}

func TestBootstrapExemptRecognizesVersionAndHelp(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"version":            {args: []string{"version"}, want: true},
		"bare config group":  {args: []string{"config"}, want: true},
		"bare inspect group": {args: []string{"inspect"}, want: true},
		"config subcommand":  {args: []string{"config", "path"}, want: false},
		"version args":       {args: []string{"version", "extra"}, want: true},
		"short version":      {args: []string{"-V"}, want: true},
		"root help":          {args: []string{"--help"}, want: true},
		"help command":       {args: []string{"help"}, want: true},
		"command help":       {args: []string{"run", "--help"}, want: true},
		"after delimiter":    {args: []string{"send", "--", "--help"}, want: false},
		"chat":               {args: []string{"chat"}, want: false},
		"default chat":       {args: []string{}, want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Eq(t, tc.want, bootstrapExempt(tc.args))
		})
	}
}

func TestIsInitCommandOnlyMatchesTopLevelInit(t *testing.T) {
	assert.True(t, isInitCommand([]string{"init"}))
	assert.False(t, isInitCommand([]string{"run", "init"}))
	assert.False(t, isInitCommand(nil))
}

// TestReadOnlyInspectCommand pins the inspect subcommands that must not write
// anything under SUGA_HOME (see main's read-only branch).
func TestReadOnlyInspectCommand(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"manifest":           {args: []string{"inspect", "manifest"}, want: true},
		"manifest flags":     {args: []string{"inspect", "manifest", "--json"}, want: true},
		"manifest alias":     {args: []string{"inspect", "info"}, want: true},
		"rules kind":         {args: []string{"inspect", "rules", "output"}, want: true},
		"rules kind json":    {args: []string{"inspect", "rules", "nudge", "--json"}, want: true},
		"rules builtin":      {args: []string{"inspect", "rules", "lang", "--builtin"}, want: true},
		"rules no kind":      {args: []string{"inspect", "rules"}, want: false},
		"rules wrong kind":   {args: []string{"inspect", "rules", "wrong"}, want: false},
		"other subcommand":   {args: []string{"inspect", "trace", "sid"}, want: false},
		"other subcommands":  {args: []string{"inspect", "stats"}, want: false},
		"bare group":         {args: []string{"inspect"}, want: false},
		"top level manifest": {args: []string{"manifest"}, want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Eq(t, tc.want, readOnlyInspectCommand(tc.args))
		})
	}
}
