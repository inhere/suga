package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
)

func TestModelsCommandUsesOfflineCatalogThroughCapp(t *testing.T) {
	t.Run("text is deterministic and marks roles", func(t *testing.T) {
		setupConfigCLI(t, `
model: gateway/main
compact_model: gateway/compact
fallback_models: [openai/backup]
providers:
  gateway:
    type: openai
    base_url: http://127.0.0.1:1/v1
    models:
      compact: {context_window: 64000}
      main: {model_id: upstream-main}
`)
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"models"})
		assert.NoErr(t, err)
		assert.StrContains(t, stdout.String(), "Providers (offline)")
		assert.True(t, regexp.MustCompile(`(?m)^  gateway +custom; 2 configured models`).MatchString(stdout.String()))
		assert.StrContains(t, stdout.String(), "* gateway/main")
		assert.StrContains(t, stdout.String(), "fallback#1")
		assert.StrContains(t, stdout.String(), ":model switch range: gateway/compact, gateway/main, openai/backup")
		assert.StrContains(t, stdout.String(), "No network request was made.")
		assert.Eq(t, "", stderr.String())
	})

	t.Run("json has stable offline shape", func(t *testing.T) {
		setupConfigCLI(t, "model: openai/gpt-4o\n")
		var stdout, stderr bytes.Buffer
		err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"models", "--json"})
		assert.NoErr(t, err)
		var catalog appconfig.ModelCatalog
		assert.NoErr(t, json.Unmarshal(stdout.Bytes(), &catalog))
		assert.True(t, catalog.Offline)
		assert.Eq(t, "config", catalog.Source)
		assert.Eq(t, []string{"openai/gpt-4o"}, catalog.Switchable)
		assert.Eq(t, 1, len(catalog.Models))
		assert.Eq(t, "openai/gpt-4o", catalog.Models[0].Ref)
		assert.Eq(t, "current", catalog.Models[0].Roles[0])
		assert.Eq(t, "", stderr.String())
	})
}

func TestModelsCommandShowsDeclaredThinkingStyleAndStreamRequirement(t *testing.T) {
	setupConfigCLI(t, `
model: gateway/main
providers:
  gateway:
    type: openai
    thinking_style: adaptive
    base_url: http://127.0.0.1:1/v1
    models:
      main: {stream_required: true}
`)
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"models"})
	assert.NoErr(t, err)
	assert.StrContains(t, stdout.String(), "thinking_style=adaptive")
	assert.StrContains(t, stdout.String(), "stream_required=yes")
	assert.Eq(t, "", stderr.String())
}

func TestModelsCommandDoesNotContactConfiguredEndpoint(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	setupConfigCLI(t, "model: offline/m\nproviders:\n  offline:\n    type: openai\n    base_url: "+server.URL+"\n    models:\n      m: {}\n")

	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"models", "--json"})
	assert.NoErr(t, err)
	assert.Eq(t, int32(0), requests.Load())
}

func TestModelsCommandReportsInvalidConfiguration(t *testing.T) {
	setupConfigCLI(t, "model: broken/m\nproviders:\n  broken:\n    models:\n      m: {}\n")
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"models"})
	assert.Require(t, assert.Err(t, err))
	assert.Eq(t, 1, ExitCode(err))
	assert.Eq(t, "", stdout.String())
}
