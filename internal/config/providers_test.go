package axconfig

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/gookit/goutil/x/assert"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

// loadYAML replaces the global config with the given YAML and isolates the
// provider-related environment from the caller's shell.
func loadYAML(t *testing.T, doc string) {
	t.Helper()
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	gkconfig.AddDriver(yaml.Driver)
	if doc != "" {
		assert.NoErr(t, gkconfig.LoadSources(gkconfig.Yaml, []byte(doc)))
	}
	for _, k := range []string{"SUGA_MODEL", "SUGA_PROVIDER", "SUGA_MAX_STEPS", "SUGA_MAX_OUTPUT_TOKENS", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
}

func TestReadProviders(t *testing.T) {
	t.Run("presets with env keys", func(t *testing.T) {
		loadYAML(t, "")
		t.Setenv("DEEPSEEK_API_KEY", "sk-ds")
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, "openai", ps["openai"].Type)
		assert.Eq(t, "https://api.openai.com/v1", ps["openai"].BaseURL)
		assert.Eq(t, "anthropic", ps["anthropic"].Type)
		assert.Eq(t, "https://api.anthropic.com", ps["anthropic"].BaseURL)
		assert.Eq(t, "openai", ps["deepseek"].Type)
		assert.Eq(t, "https://api.deepseek.com/v1", ps["deepseek"].BaseURL)
		assert.Eq(t, "sk-ds", ps["deepseek"].APIKey)
		assert.Eq(t, "", ps["openai"].APIKey)
	})

	t.Run("user overrides preset field by field", func(t *testing.T) {
		loadYAML(t, `
providers:
  deepseek:
    base_url: https://proxy.example/v1
    api_key: sk-file
    type: ""
`)
		t.Setenv("DEEPSEEK_API_KEY", "sk-env")
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, "https://proxy.example/v1", ps["deepseek"].BaseURL)
		assert.Eq(t, "sk-file", ps["deepseek"].APIKey)
		assert.Eq(t, "openai", ps["deepseek"].Type) // empty field does not override
	})

	t.Run("preset key from env when user omits it", func(t *testing.T) {
		loadYAML(t, "providers:\n  openai:\n    base_url: https://x/v1\n")
		t.Setenv("OPENAI_API_KEY", "sk-env")
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, "sk-env", ps["openai"].APIKey)
		assert.Eq(t, "https://x/v1", ps["openai"].BaseURL)
	})

	t.Run("custom provider needs type", func(t *testing.T) {
		loadYAML(t, "providers:\n  openrouter:\n    base_url: https://openrouter.ai/api/v1\n")
		_, err := ReadProviders()
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "providers.openrouter")
		assert.StrContains(t, err.Error(), "type is required")
	})

	t.Run("custom provider invalid type", func(t *testing.T) {
		loadYAML(t, "providers:\n  x:\n    type: gemini\n")
		_, err := ReadProviders()
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "unsupported type")
	})

	t.Run("headers placeholder and model context window", func(t *testing.T) {
		loadYAML(t, `
providers:
  proxy:
    type: openai
    base_url: https://p/v1
    headers:
      X-Session: "{{session_id}}"
    models:
      gpt-4.1:
        context_window: 64000
`)
		ps, err := ReadProviders()
		assert.NoErr(t, err)
		assert.Eq(t, "{{session_id}}", ps["proxy"].Headers["X-Session"])
		assert.Eq(t, 64000, ps["proxy"].Models["gpt-4.1"].ContextWindow)
	})
}

func TestResolveModel(t *testing.T) {
	loadYAML(t, "providers:\n  proxy:\n    type: openai\n")
	ps, err := ReadProviders()
	assert.NoErr(t, err)

	cases := []struct {
		name, ref, def, wantProv, wantModel, wantErr string
	}{
		{"prefixed preset", "deepseek/deepseek-chat", "", "deepseek", "deepseek-chat", ""},
		{"prefixed custom", "proxy/gpt-4o", "openai", "proxy", "gpt-4o", ""},
		{"bare uses default", "gpt-4o", "openai", "openai", "gpt-4o", ""},
		{"undefined prefix is part of model", "openrouter/meta/llama-3", "proxy", "proxy", "openrouter/meta/llama-3", ""},
		{"only first slash splits", "proxy/meta/llama-3", "openai", "proxy", "meta/llama-3", ""},
		{"empty", "  ", "openai", "", "", "empty"},
		{"bare without default", "gpt-4o", "", "", "", "no provider"},
		{"default undefined", "gpt-4o", "nope", "", "", "not defined"},
		{"missing model id", "openai/", "", "", "", "no model id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := ResolveModel(tc.ref, ps, tc.def)
			if tc.wantErr != "" {
				assert.Err(t, err)
				assert.StrContains(t, err.Error(), tc.wantErr)
				return
			}
			assert.NoErr(t, err)
			assert.Eq(t, tc.wantProv, spec.Provider.ID)
			assert.Eq(t, tc.wantModel, spec.Model)
			assert.Eq(t, tc.wantProv+"/"+tc.wantModel, spec.Ref())
		})
	}
}

func TestReadModelSettings(t *testing.T) {
	const doc = `
model: deepseek/deepseek-chat
compact_model: deepseek-lite
fallback_models:
  - openai/gpt-4o
  - deepseek-reasoner
`
	t.Run("config values", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Eq(t, "deepseek/deepseek-chat", s.Main.Ref())
		assert.NotNil(t, s.Compact)
		assert.Eq(t, "deepseek/deepseek-lite", s.Compact.Ref()) // bare uses main provider
		assert.Eq(t, 2, len(s.Fallbacks))
		assert.Eq(t, "openai/gpt-4o", s.Fallbacks[0].Ref())
		assert.Eq(t, "deepseek/deepseek-reasoner", s.Fallbacks[1].Ref())
	})

	t.Run("priority flag over env over config", func(t *testing.T) {
		loadYAML(t, doc)
		t.Setenv("SUGA_MODEL", "anthropic/claude-sonnet-4-6")
		s, err := ReadModelSettings("openai/gpt-4o")
		assert.NoErr(t, err)
		assert.Eq(t, "openai/gpt-4o", s.Main.Ref())

		s, err = ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Eq(t, "anthropic/claude-sonnet-4-6", s.Main.Ref())
		// bare compact model follows the new main provider
		assert.Eq(t, "anthropic/deepseek-lite", s.Compact.Ref())
	})

	t.Run("compact unset is nil", func(t *testing.T) {
		loadYAML(t, "model: openai/gpt-4o\n")
		s, err := ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Nil(t, s.Compact)
		assert.Eq(t, 0, len(s.Fallbacks))
	})

	t.Run("model id containing slash", func(t *testing.T) {
		loadYAML(t, "providers:\n  openrouter:\n    type: openai\nmodel: openrouter/meta/llama-3\n")
		s, err := ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Eq(t, "openrouter", s.Main.Provider.ID)
		assert.Eq(t, "meta/llama-3", s.Main.Model)
	})

	t.Run("model not configured", func(t *testing.T) {
		loadYAML(t, "")
		_, err := ReadModelSettings("")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "model: <provider>/<model>")
	})

	t.Run("bare main model rejected", func(t *testing.T) {
		loadYAML(t, "model: gpt-4o\n")
		_, err := ReadModelSettings("")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "no provider")
	})

	t.Run("bare flag or env model uses the configured provider", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("deepseek-reasoner")
		assert.NoErr(t, err)
		assert.Eq(t, "deepseek/deepseek-reasoner", s.Main.Ref())

		t.Setenv("SUGA_MODEL", "deepseek-coder")
		s, err = ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Eq(t, "deepseek/deepseek-coder", s.Main.Ref())
	})

	t.Run("bare flag model without configured model rejected", func(t *testing.T) {
		loadYAML(t, "")
		_, err := ReadModelSettings("gpt-4o")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "no provider")
	})

	t.Run("bad fallback names its position", func(t *testing.T) {
		loadYAML(t, "model: openai/gpt-4o\nfallback_models: [\"\"]\n")
		_, err := ReadModelSettings("")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "fallback_models[0]")
	})
}

func TestCheckLegacyConfig(t *testing.T) {
	t.Run("llm section", func(t *testing.T) {
		loadYAML(t, "llm:\n  model: gpt-4o\nmodel: openai/gpt-4o\n")
		err := CheckLegacyConfig()
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "llm: section is no longer supported")
		assert.StrContains(t, err.Error(), "model: <provider>/<model>")
		assert.StrContains(t, err.Error(), "providers:")
		_, err = ReadModelSettings("")
		assert.Err(t, err)
	})

	t.Run("SUGA_PROVIDER", func(t *testing.T) {
		loadYAML(t, "model: openai/gpt-4o\n")
		t.Setenv("SUGA_PROVIDER", "deepseek")
		err := CheckLegacyConfig()
		assert.Err(t, err)
		assert.True(t, strings.Contains(err.Error(), "SUGA_MODEL=<provider>/<model>"))
		_, err = ReadModelSettings("")
		assert.Err(t, err)
	})

	t.Run("clean", func(t *testing.T) {
		loadYAML(t, "model: openai/gpt-4o\n")
		assert.NoErr(t, CheckLegacyConfig())
	})
}

func TestContextWindowFor(t *testing.T) {
	loadYAML(t, `
providers:
  deepseek:
    models:
      deepseek-chat:
        context_window: 64000
  cc:
    type: openai
    context_window: 1000000
    models:
      small:
        context_window: 32000
llm:
  context_window: 99999
`)
	ps, err := ReadProviders()
	assert.NoErr(t, err)

	cases := []struct {
		name, ref string
		want      int
	}{
		{"configured wins", "deepseek/deepseek-chat", 64000},
		{"inferred from name", "deepseek/deepseek-reasoner", 128000},
		{"inferred claude", "anthropic/claude-sonnet-4-6", 200000},
		{"unknown default, ignores llm.context_window", "openai/mystery", DefaultContextWindow},
		{"provider default beats name inference", "cc/claude-sonnet-4-6", 1000000},
		{"model value beats provider default", "cc/small", 32000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := ResolveModel(tc.ref, ps, "")
			assert.NoErr(t, err)
			assert.Eq(t, tc.want, ContextWindowFor(spec))
		})
	}
}

// Provider entries are read as a raw map, so ${VAR} references must be expanded
// by ReadProviders itself (gkconfig only does it for typed getters).
func TestReadProvidersExpandsEnvReferences(t *testing.T) {
	loadYAML(t, `
providers:
  gw:
    type: openai
    api_key: "${SUGA_TEST_GW_KEY}"
    base_url: "${SUGA_TEST_GW_URL}"
    headers:
      x-session: "{{session_id}}"
      x-tag: "${SUGA_TEST_GW_TAG}"
`)
	t.Setenv("SUGA_TEST_GW_KEY", "sk-env")
	t.Setenv("SUGA_TEST_GW_URL", "https://gw.example/v1")
	t.Setenv("SUGA_TEST_GW_TAG", "team-a")
	ps, err := ReadProviders()
	assert.NoErr(t, err)
	gw := ps["gw"]
	assert.Eq(t, "sk-env", gw.APIKey)
	assert.Eq(t, "https://gw.example/v1", gw.BaseURL)
	assert.Eq(t, "team-a", gw.Headers["x-tag"])
	assert.Eq(t, "{{session_id}}", gw.Headers["x-session"])
}

func TestModelPrice(t *testing.T) {
	loadYAML(t, `
providers:
  deepseek:
    models:
      deepseek-chat:
        price: {input: 0.27, output: 1.1, cache_read: 0.07, cache_write: 2}
      deepseek-reasoner:
        context_window: 64000
`)
	ps, err := ReadProviders()
	assert.NoErr(t, err)
	t.Run("parsed", func(t *testing.T) {
		spec, err := ResolveModel("deepseek/deepseek-chat", ps, "")
		assert.NoErr(t, err)
		price, ok := PriceFor(spec)
		assert.True(t, ok)
		assert.Eq(t, Price{Input: 0.27, Output: 1.1, CacheRead: 0.07, CacheWrite: 2}, price)
	})
	t.Run("absent price is unknown", func(t *testing.T) {
		spec, err := ResolveModel("deepseek/deepseek-reasoner", ps, "")
		assert.NoErr(t, err)
		_, ok := PriceFor(spec)
		assert.False(t, ok)
	})
	t.Run("lookup by provider and model", func(t *testing.T) {
		spec, err := ResolveModel("deepseek/deepseek-chat", ps, "")
		assert.NoErr(t, err)
		lookup := ModelSettings{Main: spec}.PriceLookup()
		price, ok := lookup("deepseek", "deepseek-chat")
		assert.True(t, ok)
		assert.Eq(t, 0.27, price.Input)
		_, ok = lookup("other", "deepseek-chat")
		assert.False(t, ok)
		_, ok = lookup("deepseek", "nope")
		assert.False(t, ok)
	})
}

func TestReadProvidersPromptCache(t *testing.T) {
	loadYAML(t, `
providers:
  anthropic:
    prompt_cache: true
  gw:
    type: anthropic
    base_url: https://gw.example
`)
	ps, err := ReadProviders()
	assert.NoErr(t, err)
	assert.True(t, ps["anthropic"].PromptCache)
	assert.False(t, ps["gw"].PromptCache) // default off
	assert.False(t, ps["openai"].PromptCache)
}

// TestReadProvidersPromptCacheRetention: providers.<id>.prompt_cache_retention
// reaches the provider config and refuses an unknown value (the API would answer
// with a 4xx that names neither the key nor the config file).
func TestReadProvidersPromptCacheRetention(t *testing.T) {
	loadYAML(t, `
providers:
  codex:
    type: openai-responses
    base_url: https://gw.example/v1
    prompt_cache_retention: 24h
  plain:
    type: openai-responses
    base_url: https://gw2.example/v1
`)
	ps, err := ReadProviders()
	assert.NoErr(t, err)
	assert.Eq(t, PromptCacheRetention24h, ps["codex"].PromptCacheRetention)
	assert.Eq(t, "", ps["plain"].PromptCacheRetention) // default: the API's own

	loadYAML(t, `
providers:
  codex:
    type: openai-responses
    base_url: https://gw.example/v1
    prompt_cache_retention: forever
`)
	_, err = ReadProviders()
	assert.ErrMsgContains(t, err, "prompt_cache_retention must be in_memory or 24h")
}

func TestModelID(t *testing.T) {
	doc := `
model: cc/deepseek-v4.1-flash
providers:
  cc:
    type: openai
    base_url: https://cc.example/v1
    models:
      deepseek-v4.1-flash:
        model_id: deepseek/deepseek-v4.1-flash
        context_window: 900000
      glm-5:
        context_window: 200000
`
	t.Run("the short key is used everywhere but the request", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "deepseek-v4.1-flash", s.Main.Model)
		assert.Eq(t, "cc/deepseek-v4.1-flash", s.Main.Ref())
		assert.Eq(t, "deepseek/deepseek-v4.1-flash", s.Main.APIModel())
		assert.Eq(t, 900000, ContextWindowFor(s.Main))
		var sent map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
		}))
		defer srv.Close()
		spec := s.Main
		spec.Provider.BaseURL = srv.URL
		p, err := BuildProvider(spec)
		assert.Require(t, assert.NoErr(t, err))
		_, err = p.Generate(context.Background(), &llm.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
		assert.NoErr(t, err)
		assert.Eq(t, "deepseek/deepseek-v4.1-flash", sent["model"]) // the request carries model_id
	})
	t.Run("the full upstream id maps back to its key", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("cc/deepseek/deepseek-v4.1-flash")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "deepseek-v4.1-flash", s.Main.Model)
		assert.Eq(t, 900000, ContextWindowFor(s.Main))
	})
	t.Run("a bare key uses the default provider", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("glm-5")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "cc/glm-5", s.Main.Ref())
		assert.Eq(t, "glm-5", s.Main.APIModel()) // no model_id: the key is sent
	})
	t.Run("an unlisted model is sent as is, with a warning", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("cc/deepseek-v4-1-flash")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "deepseek-v4-1-flash", s.Main.APIModel())
		assert.Eq(t, `model: "deepseek-v4-1-flash" is not under providers.cc.models `+
			`(configured: deepseek-v4.1-flash, glm-5); sending it to the provider as is`, unlistedModelWarning("model", s.Main))
		assert.Eq(t, "", unlistedModelWarning("model", ModelSpec{Provider: ProviderConfig{ID: "x"}, Model: "any"}))
	})
}

func TestModelTypeOverride(t *testing.T) {
	doc := `
model: cc/deepseek-v4.1-flash
providers:
  cc:
    type: openai
    base_url: https://cc.example/v1
    models:
      deepseek-v4.1-flash:
        model_id: deepseek/deepseek-v4.1-flash
      sonnet:
        type: anthropic
        base_url: ${CC_ANTHROPIC_URL}
        model_id: claude-sonnet-4-5
`
	t.Run("a model's type and base_url win over the provider's", func(t *testing.T) {
		loadYAML(t, doc)
		var path string
		var sent map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
		}))
		defer srv.Close()
		t.Setenv("CC_ANTHROPIC_URL", srv.URL)

		s, err := ReadModelSettings("cc/sonnet")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, ProviderTypeAnthropic, s.Main.Provider.Type)
		assert.Eq(t, srv.URL, s.Main.Provider.BaseURL)
		p, err := BuildProvider(s.Main)
		assert.Require(t, assert.NoErr(t, err))
		_, err = p.Generate(context.Background(), &llm.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
		assert.NoErr(t, err)
		assert.Eq(t, "/v1/messages", path) // the Anthropic protocol
		assert.Eq(t, "claude-sonnet-4-5", sent["model"])
	})
	t.Run("other models keep the provider's", func(t *testing.T) {
		loadYAML(t, doc)
		s, err := ReadModelSettings("")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, ProviderTypeOpenAI, s.Main.Provider.Type)
		assert.Eq(t, "https://cc.example/v1", s.Main.Provider.BaseURL)
	})
	t.Run("an unknown model type is rejected", func(t *testing.T) {
		loadYAML(t, strings.Replace(doc, "type: anthropic", "type: gemini", 1))
		_, err := ReadProviders()
		assert.ErrMsg(t, err, `providers.cc.models.sonnet: unsupported type "gemini" (want openai|openai-responses|anthropic)`)
	})
}

func TestResolveModelLooseMatch(t *testing.T) {
	doc := `
model: cc/deepseek-v4.1-flash
providers:
  cc:
    type: openai
    base_url: https://cc.example/v1
    models:
      deepseek-v4.1-flash:
        model_id: deepseek/deepseek-v4.1-flash
      deepseek-v4.1-flash-fast: {}
      claude-haiku-5-5:
        type: anthropic
      glm-5: {}
  oc:
    type: openai
    base_url: https://oc.example/v1
    models:
      kimi-k3: {}
      glm-5: {}
`
	resolve := func(t *testing.T, ref string) (ModelSettings, error) {
		loadYAML(t, doc)
		return ReadModelSettings(ref)
	}
	t.Run("a unique part of a name", func(t *testing.T) {
		s, err := resolve(t, "haiku")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "cc/claude-haiku-5-5", s.Main.Ref())
		assert.Eq(t, ProviderTypeAnthropic, s.Main.Provider.Type) // the model's own type still applies
		s, err = resolve(t, "KIMI")                               // another provider, any case
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "oc/kimi-k3", s.Main.Ref())
	})
	t.Run("an exact name wins over longer ones", func(t *testing.T) {
		s, err := resolve(t, "cc/DeepSeek-V4.1-Flash")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "cc/deepseek-v4.1-flash", s.Main.Ref())
	})
	t.Run("several matches are listed", func(t *testing.T) {
		_, err := resolve(t, "flash")
		assert.ErrMsg(t, err, `model: model "flash" matches several configured models: `+
			`cc/deepseek-v4.1-flash, cc/deepseek-v4.1-flash-fast; give one of them`)
		_, err = resolve(t, "glm") // in two providers
		assert.ErrMsg(t, err, `model: model "glm" matches several configured models: cc/glm-5, oc/glm-5; give one of them`)
		s, err := resolve(t, "glm-5") // a key of the default provider resolves as before
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "cc/glm-5", s.Main.Ref())
	})
	t.Run("a provider prefix limits the search", func(t *testing.T) {
		s, err := resolve(t, "oc/glm")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "oc/glm-5", s.Main.Ref())
	})
	t.Run("a bare name without a configured model", func(t *testing.T) {
		loadYAML(t, strings.Replace(doc, "model: cc/deepseek-v4.1-flash\n", "", 1))
		s, err := ReadModelSettings("kimi")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "oc/kimi-k3", s.Main.Ref())
		_, err = ReadModelSettings("qwen")
		assert.ErrMsg(t, err, `model: model "qwen" has no provider; use <provider>/<model>`)
	})
	t.Run("no match keeps the name", func(t *testing.T) {
		s, err := resolve(t, "cc/qwen-max")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "cc/qwen-max", s.Main.Ref())
	})
}

func TestOpenAIResponsesType(t *testing.T) {
	var path string
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer srv.Close()
	loadYAML(t, `
model: cc/flash
providers:
  cc:
    type: openai
    base_url: `+srv.URL+`/v1
    compat: [deepseek]
    thinking: high
    models:
      flash:
        type: openai-responses
        model_id: deepseek/deepseek-v4.1-flash
`)
	s, err := ReadModelSettings("")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, ProviderTypeOpenAIResponses, s.Main.Provider.Type)
	assert.Eq(t, []string{"deepseek"}, s.Main.Provider.Compat)
	p, err := BuildProvider(s.Main)
	assert.Require(t, assert.NoErr(t, err))
	res, err := p.Generate(context.Background(), &llm.GenerateRequest{Messages: []agentcoreapi.Message{{Role: "user", Content: "hi"}}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "ok", res.Content)
	assert.Eq(t, "/v1/responses", path)
	assert.Eq(t, "deepseek/deepseek-v4.1-flash", sent["model"])
	assert.Eq(t, map[string]any{"effort": "high"}, sent["reasoning"])
	assert.NotNil(t, sent["input"])
	assert.Nil(t, sent["messages"])
}

func TestReadModelSettingsScenarioModels(t *testing.T) {
	t.Run("models.compact wins over compact_model", func(t *testing.T) {
		loadYAML(t, "model: deepseek/deepseek-chat\n"+
			"compact_model: deepseek-lite\n"+
			"models:\n  compact: openai/gpt-4o-mini\n")
		s, err := ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Require(t, assert.NotNil(t, s.Compact))
		assert.Eq(t, "openai/gpt-4o-mini", s.Compact.Ref())
	})

	t.Run("compact_model alone still works", func(t *testing.T) {
		loadYAML(t, "model: deepseek/deepseek-chat\ncompact_model: deepseek-lite\n")
		s, err := ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Require(t, assert.NotNil(t, s.Compact))
		assert.Eq(t, "deepseek/deepseek-lite", s.Compact.Ref()) // bare name uses the main provider
		assert.Nil(t, s.Idle)
		assert.Nil(t, s.Handoff)
	})

	t.Run("models.idle and models.handoff resolve", func(t *testing.T) {
		loadYAML(t, "model: deepseek/deepseek-chat\n"+
			"models:\n  idle: gpt-4o-mini\n  handoff: openai/gpt-4o-mini\n")
		s, err := ReadModelSettings("")
		assert.NoErr(t, err)
		assert.Require(t, assert.NotNil(t, s.Idle))
		assert.Eq(t, "deepseek/gpt-4o-mini", s.Idle.Ref()) // bare name uses the main provider
		assert.Require(t, assert.NotNil(t, s.Handoff))
		assert.Eq(t, "openai/gpt-4o-mini", s.Handoff.Ref())
		assert.Nil(t, s.Compact)
	})

	t.Run("effective values via the config registry", func(t *testing.T) {
		loadYAML(t, "model: deepseek/deepseek-chat\n"+
			"models:\n  idle: openai/gpt-4o-mini\n")
		v, err := ReadConfigValue("models.idle")
		assert.NoErr(t, err)
		assert.Eq(t, "openai/gpt-4o-mini", v.Value)
		// unset scenario keys report "not configured", like compact_model.
		v, err = ReadConfigValue("models.handoff")
		assert.NoErr(t, err)
		assert.Eq(t, "", v.Value)
	})
}
