package wiring

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
)

const okBody = `{"choices":[{"message":{"content":"ok-from-%s"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
const noUsageBody = `{"choices":[{"message":{"content":"ok-from-%s"},"finish_reason":"stop"}]}`

// resetConfig isolates the global config and the provider-related environment.
func resetConfig(t *testing.T) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	for _, k := range []string{"SUGA_MODEL", "SUGA_PROVIDER", "SUGA_MAX_STEPS", "SUGA_MAX_OUTPUT_TOKENS", "SUGA_BUDGET_TOKENS", "SUGA_BUDGET_COST_USD", "SUGA_BUDGET_SCOPE", "SUGA_BUDGET_ESTIMATE", "SUGA_BUDGET_ESTIMATE_FACTOR", "SUGA_BUDGET_NOTICE_RATIO", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "IDEV_STD_RELEASES_ROOT", "INHERE_DEV_STANDARDS_ROOT"} {
		t.Setenv(k, "")
	}
}

func setProviders(t *testing.T, providers map[string]any) {
	t.Helper()
	assert.NoErr(t, gkconfig.Set("providers", providers))
}

// noUsageServer answers without a provider usage object.
func noUsageServer(t *testing.T, status int, name string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(strings.Replace(noUsageBody, "%s", name, 1)))
			return
		}
		_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeServer answers every request with the given status and a fixed reply.
func fakeServer(t *testing.T, status int, name string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(strings.Replace(okBody, "%s", name, 1)))
			return
		}
		_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func openTestRuntime(t *testing.T, model string) *Runtime {
	t.Helper()
	rt, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir(), Model: model})
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func modelEvents(t *testing.T, rt *Runtime, sessionID string) []store.Event {
	t.Helper()
	events, err := rt.repo.ListEvents(context.Background(), store.EventFilter{SessionID: sessionID, Kinds: []string{store.EventModel}})
	assert.NoErr(t, err)
	return events
}

func TestContextWindowFollowsEffectiveModel(t *testing.T) {
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))
	window := func(flag string) int {
		settings, err := appconfig.ReadModelSettings(flag)
		assert.NoErr(t, err)
		return appconfig.ContextWindowFor(settings.Main)
	}
	t.Run("file model", func(t *testing.T) { assert.Eq(t, 128000, window("")) })
	t.Run("flag overrides file model", func(t *testing.T) { assert.Eq(t, 1000000, window("gpt-4.1")) })
	t.Run("env overrides file model", func(t *testing.T) {
		t.Setenv("SUGA_MODEL", "gpt-5")
		assert.Eq(t, 400000, window(""))
	})
	t.Run("configured context_window wins", func(t *testing.T) {
		setProviders(t, map[string]any{"openai": map[string]any{"models": map[string]any{"gpt-4.1": map[string]any{"context_window": 64000}}}})
		assert.Eq(t, 64000, window("gpt-4.1"))
	})
}

func TestOpenUsesPresetAndReportsWindow(t *testing.T) {
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	t.Run("deepseek preset", func(t *testing.T) {
		settings, err := appconfig.ReadModelSettings("deepseek/deepseek-chat")
		assert.NoErr(t, err)
		assert.Eq(t, "https://api.deepseek.com/v1", settings.Main.Provider.BaseURL)
		provider, err := buildMainProvider(settings)
		assert.NoErr(t, err)
		assert.Eq(t, "deepseek", provider.Name())
	})

	t.Run("request reaches the preset provider with its own key and model", func(t *testing.T) {
		var gotAuth, gotModel string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			if strings.Contains(string(raw), `"model":"deepseek-chat"`) {
				gotModel = "deepseek-chat"
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(strings.Replace(okBody, "%s", "ds", 1)))
		}))
		t.Cleanup(srv.Close)
		// Overriding base_url keeps the preset type; the credential is the deepseek one,
		// not the main openai provider's.
		setProviders(t, map[string]any{
			"openai":   map[string]any{"api_key": "openai-key"},
			"deepseek": map[string]any{"api_key": "ds-key", "base_url": srv.URL},
		})
		rt := openTestRuntime(t, "deepseek/deepseek-chat")
		assert.Eq(t, 128000, rt.ContextWindow)
		res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
		assert.NoErr(t, err)
		assert.Eq(t, "ok-from-ds", res.Output)
		assert.Eq(t, "Bearer ds-key", gotAuth)
		assert.Eq(t, "deepseek-chat", gotModel)
		sess, err := rt.repo.GetSession(context.Background(), res.SessionID)
		assert.NoErr(t, err)
		assert.Eq(t, "deepseek", sess.Meta["provider"])
		assert.Eq(t, "deepseek-chat", sess.Meta["model"])
	})

	t.Run("context window is always positive", func(t *testing.T) {
		setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k"}})
		assert.Eq(t, 128000, openTestRuntime(t, "openai/unknown-model").ContextWindow)
	})
}

func TestOpenFallbackModels(t *testing.T) {
	resetConfig(t)
	var mainHits, backupHits atomic.Int32
	// A 401 is not retried by the openai provider, so the pool moves on at once.
	bad := fakeServer(t, http.StatusUnauthorized, "main", &mainHits)
	good := fakeServer(t, http.StatusOK, "backup", &backupHits)
	assert.NoErr(t, gkconfig.Set("model", "primary/m1"))
	assert.NoErr(t, gkconfig.Set("fallback_models", []string{"backup/m2"}))
	setProviders(t, map[string]any{
		"primary": map[string]any{"type": "openai", "api_key": "k", "base_url": bad.URL},
		"backup":  map[string]any{"type": "openai", "api_key": "k", "base_url": good.URL},
	})
	rt := openTestRuntime(t, "")
	res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
	assert.NoErr(t, err)
	assert.Eq(t, "ok-from-backup", res.Output)
	assert.Eq(t, int32(1), mainHits.Load())
	assert.Eq(t, int32(1), backupHits.Load())

	events := modelEvents(t, rt, res.SessionID)
	assert.Require(t, assert.Eq(t, 2, len(events)))
	assert.Eq(t, "primary", events[0].Name)
	assert.Eq(t, "error", events[0].Status)
	assert.Eq(t, "backup", events[1].Name)
	// Session metadata records only the main model.
	sess, err := rt.repo.GetSession(context.Background(), res.SessionID)
	assert.NoErr(t, err)
	assert.Eq(t, "primary", sess.Meta["provider"])
	assert.Eq(t, "m1", sess.Meta["model"])
}

func TestOpenTraceUsesConfiguredProviderID(t *testing.T) {
	resetConfig(t)
	srv := fakeServer(t, http.StatusOK, "gw", nil)
	assert.NoErr(t, gkconfig.Set("model", "my-gw/some-model"))
	setProviders(t, map[string]any{"my-gw": map[string]any{"type": "openai", "api_key": "k", "base_url": srv.URL}})
	rt := openTestRuntime(t, "")
	res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
	assert.NoErr(t, err)
	events := modelEvents(t, rt, res.SessionID)
	assert.Require(t, assert.Eq(t, 1, len(events)))
	assert.Eq(t, "my-gw", events[0].Name)
	assert.Eq(t, "some-model", events[0].Attrs["model"])
}

func TestOpenRejectsLegacyConfig(t *testing.T) {
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("llm", map[string]any{"api_type": "openai", "model": "gpt-4o"}))
	_, err := Open(context.Background(), Options{Workspace: t.TempDir(), RuleCWD: t.TempDir()})
	assert.Err(t, err)
	assert.StrContains(t, err.Error(), "no longer supported")
}

func TestRuntimeSwitchPreservesFallbackModels(t *testing.T) {
	resetConfig(t)
	var seen []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		model := "unknown"
		for _, candidate := range []string{"m2", "fallback"} {
			if strings.Contains(string(body), `"model":"`+candidate+`"`) {
				model = candidate
				break
			}
		}
		mu.Lock()
		seen = append(seen, model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if model == "m2" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"m2 unavailable"}}`)
			return
		}
		_, _ = io.WriteString(w, okBody)
	}))
	t.Cleanup(server.Close)
	assert.NoErr(t, gkconfig.Set("model", "fake/m1"))
	assert.NoErr(t, gkconfig.Set("fallback_models", []string{"fake/fallback"}))
	setProviders(t, map[string]any{"fake": map[string]any{"type": "openai", "api_key": "k", "base_url": server.URL,
		"models": map[string]any{"m1": map[string]any{}, "m2": map[string]any{}, "fallback": map[string]any{}}}})
	rt := openTestRuntime(t, "")
	session, err := rt.Runner.NewSession(context.Background(), "switch-fallback", "test")
	assert.Require(t, assert.NoErr(t, err))
	_, err = rt.SwitchModel(context.Background(), session.ID, "fake/m2")
	assert.Require(t, assert.NoErr(t, err))
	_, err = rt.Runner.Run(context.Background(), personal.RunRequest{SessionID: session.ID, Prompt: "hi"})
	assert.NoErr(t, err)
	mu.Lock()
	assert.Eq(t, []string{"m2", "fallback"}, seen)
	mu.Unlock()
}

func TestBudgetWrapsEveryPoolMemberBeforeObservation(t *testing.T) {
	resetConfig(t)
	var mainHits, backupHits atomic.Int32
	bad := noUsageServer(t, http.StatusUnauthorized, "main", &mainHits)
	good := noUsageServer(t, http.StatusOK, "backup", &backupHits)
	assert.NoErr(t, gkconfig.Set("model", "primary/m1"))
	assert.NoErr(t, gkconfig.Set("fallback_models", []string{"backup/m2"}))
	setProviders(t, map[string]any{
		"primary": map[string]any{"type": "openai", "api_key": "k", "base_url": bad.URL},
		"backup":  map[string]any{"type": "openai", "api_key": "k", "base_url": good.URL},
	})
	t.Setenv("SUGA_BUDGET_TOKENS", "100")
	rt := openTestRuntime(t, "")
	res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
	assert.NoErr(t, err)
	assert.Eq(t, int32(1), mainHits.Load())
	assert.Eq(t, int32(1), backupHits.Load())

	events := modelEvents(t, rt, res.SessionID)
	assert.Require(t, assert.Eq(t, 2, len(events)))
	assert.Eq(t, "backup", events[1].Name)
	assert.Eq(t, "m2", events[1].Attrs["model"])
	assert.Eq(t, true, events[1].Attrs["estimated"])
	assert.True(t, events[1].InputTokens > 0)
	assert.True(t, events[1].OutputTokens > 0)

	settings, err := appconfig.ReadModelSettings("")
	assert.NoErr(t, err)
	provider, err := buildMainProviderWithEstimate(settings, estimateOptionsForBudget(100, 0, "auto", 1))
	assert.NoErr(t, err)
	_, streaming := provider.(agentcore.StreamingProvider)
	assert.True(t, streaming)
	chooser, hasChooser := provider.(agentcore.ToolChooser)
	assert.Require(t, assert.True(t, hasChooser))
	assert.True(t, chooser.SupportsToolChoice())
}

func TestConfiguredModelOptionsFollowOfflineCatalogOrder(t *testing.T) {
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("model", "proxy/primary"))
	assert.NoErr(t, gkconfig.Set("compact_model", "proxy/compact"))
	assert.NoErr(t, gkconfig.Set("fallback_models", []string{"deepseek/reasoner"}))
	setProviders(t, map[string]any{"proxy": map[string]any{
		"type": "openai", "base_url": "http://127.0.0.1:1/v1",
		"models": map[string]any{
			"compact": map[string]any{"context_window": 64000},
			"primary": map[string]any{"context_window": 128000},
		},
	}})

	providers, err := appconfig.ReadProviders()
	assert.Require(t, assert.NoErr(t, err))
	settings, err := appconfig.ReadModelSettings("")
	assert.Require(t, assert.NoErr(t, err))
	catalog := appconfig.BuildModelCatalog(providers, settings)
	options, specs, err := configuredModelOptions("")
	assert.Require(t, assert.NoErr(t, err))

	refs := make([]string, 0, len(options))
	for _, option := range options {
		refs = append(refs, option.Ref())
	}
	assert.Eq(t, catalog.Switchable, refs)
	assert.Eq(t, catalog.Switchable, sortedSpecRefs(specs))

	t.Run("explicit model is included", func(t *testing.T) {
		explicitSettings, err := appconfig.ReadModelSettings("deepseek/reasoner")
		assert.Require(t, assert.NoErr(t, err))
		explicitCatalog := appconfig.BuildModelCatalog(providers, explicitSettings)
		explicitOptions, _, err := configuredModelOptions("deepseek/reasoner")
		assert.Require(t, assert.NoErr(t, err))
		explicitRefs := make([]string, 0, len(explicitOptions))
		for _, option := range explicitOptions {
			explicitRefs = append(explicitRefs, option.Ref())
		}
		assert.Eq(t, explicitCatalog.Switchable, explicitRefs)
	})
}

func sortedSpecRefs(specs map[string]appconfig.ModelSpec) []string {
	refs := make([]string, 0, len(specs))
	for ref := range specs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func TestBudgetEstimatorRemainsOffWithoutLimits(t *testing.T) {
	resetConfig(t)
	server := noUsageServer(t, http.StatusOK, "plain", nil)
	assert.NoErr(t, gkconfig.Set("model", "fake/m1"))
	setProviders(t, map[string]any{"fake": map[string]any{"type": "openai", "api_key": "k", "base_url": server.URL}})
	rt := openTestRuntime(t, "")
	res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
	assert.NoErr(t, err)
	events := modelEvents(t, rt, res.SessionID)
	assert.Require(t, assert.Eq(t, 1, len(events)))
	_, estimated := events[0].Attrs["estimated"]
	assert.False(t, estimated)
	assert.Eq(t, int64(0), events[0].InputTokens)
	assert.Eq(t, int64(0), events[0].OutputTokens)
}

// TestOpenFallbackOnConnectFailure drives the real assembly: the main model's
// endpoint refuses connections, so the pool hands the turn to the fallback model
// after one attempt instead of retrying the dead endpoint.
//
// Reverse verification: removing the fallbackAvailable branch in postWithRetry
// makes the run spend the default policy's 6 attempts with 1s..16s waits (31s)
// on the dead primary, so the elapsed bound fails.
func TestOpenFallbackOnConnectFailure(t *testing.T) {
	resetConfig(t)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := dead.URL
	dead.Close()
	good := fakeServer(t, http.StatusOK, "backup", nil)
	assert.NoErr(t, gkconfig.Set("model", "primary/m1"))
	assert.NoErr(t, gkconfig.Set("fallback_models", []string{"backup/m2"}))
	setProviders(t, map[string]any{
		"primary": map[string]any{"type": "openai", "api_key": "k", "base_url": closedURL},
		"backup":  map[string]any{"type": "openai", "api_key": "k", "base_url": good.URL},
	})
	rt := openTestRuntime(t, "")
	start := time.Now()
	res, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
	elapsed := time.Since(start)
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "ok-from-backup", res.Output)
	assert.True(t, elapsed < 5*time.Second, "the fallback was reached after %s", elapsed)

	events := modelEvents(t, rt, res.SessionID)
	assert.Require(t, assert.Eq(t, 2, len(events)))
	assert.Eq(t, "primary", events[0].Name)
	fallback, _ := events[0].Attrs["fallback"].(string)
	assert.StrContains(t, fallback, "connect_failed")
}
