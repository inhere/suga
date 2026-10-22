package axconfig

import (
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/llm"
)

// The `timeouts:` section: unset keys keep the defaults, `off` removes a bound,
// and a bad value names the key.
func TestReadRequestTimeouts(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		loadYAML(t, "")
		got, err := ReadRequestTimeouts()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, DefaultRequestTimeout, got.Request)
		assert.Eq(t, DefaultResponseHeaderTimeout, got.ResponseHeader)
	})

	t.Run("global section, durations and bare seconds", func(t *testing.T) {
		loadYAML(t, "timeouts:\n  request: 90s\n  response_header: 30\n")
		got, err := ReadRequestTimeouts()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 90*time.Second, got.Request)
		assert.Eq(t, 30*time.Second, got.ResponseHeader)
	})

	t.Run("off and 0 remove a bound", func(t *testing.T) {
		loadYAML(t, "timeouts:\n  request: off\n  response_header: 0\n")
		got, err := ReadRequestTimeouts()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, time.Duration(0), got.Request)
		assert.Eq(t, time.Duration(0), got.ResponseHeader)
	})

	t.Run("an invalid value names the key", func(t *testing.T) {
		loadYAML(t, "timeouts:\n  request: soon\n")
		_, err := ReadRequestTimeouts()
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "timeouts.request")
	})

	t.Run("an unknown key is rejected", func(t *testing.T) {
		loadYAML(t, "timeouts:\n  header: 5s\n")
		_, err := ReadRequestTimeouts()
		assert.Require(t, assert.Err(t, err))
		assert.StrContains(t, err.Error(), "timeouts.header: unknown key")
	})

	// stream_idle is on by default (llm has its own default), so an unset key
	// keeps 0 = "the provider's default" and only an explicit off removes it.
	t.Run("stream_idle: unset keeps the provider default", func(t *testing.T) {
		loadYAML(t, "timeouts:\n  request: 90s\n")
		got, err := ReadRequestTimeouts()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, time.Duration(0), got.StreamIdle)
	})

	t.Run("stream_idle: a duration and off", func(t *testing.T) {
		loadYAML(t, "timeouts:\n  stream_idle: 45s\n")
		got, err := ReadRequestTimeouts()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 45*time.Second, got.StreamIdle)

		loadYAML(t, "timeouts:\n  stream_idle: off\n")
		got, err = ReadRequestTimeouts()
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, llm.NoTimeout, got.StreamIdle)
	})
}

// providers.<id>.timeouts overrides the global section for that provider only.
func TestReadProvidersTimeouts(t *testing.T) {
	loadYAML(t, `timeouts:
  request: 90s
providers:
  gw:
    type: openai
    base_url: https://gw.example/v1
    timeouts:
      response_header: 15s
`)
	ps, err := ReadProviders()
	assert.Require(t, assert.NoErr(t, err))

	gw := ps["gw"]
	assert.Eq(t, 90*time.Second, gw.RequestTimeout)           // from the global section
	assert.Eq(t, 15*time.Second, gw.ResponseHeaderTimeout)    // overridden here
	assert.Eq(t, 90*time.Second, ps["openai"].RequestTimeout) // an untouched preset keeps the global value
	assert.Eq(t, DefaultResponseHeaderTimeout, ps["openai"].ResponseHeaderTimeout)
}

// timeouts.stream_idle reaches the providers, overridden per provider like the
// other two bounds.
func TestReadProvidersStreamIdleTimeout(t *testing.T) {
	loadYAML(t, `timeouts:
  stream_idle: 45s
providers:
  gw:
    type: openai
    base_url: https://gw.example/v1
    timeouts:
      stream_idle: off
  plain:
    type: openai
    base_url: https://plain.example/v1
`)
	ps, err := ReadProviders()
	assert.Require(t, assert.NoErr(t, err))

	assert.Eq(t, 45*time.Second, ps["plain"].StreamIdleTimeout) // from the global section
	assert.Eq(t, llm.NoTimeout, ps["gw"].StreamIdleTimeout)     // off here
	assert.Eq(t, 45*time.Second, ps["openai"].StreamIdleTimeout)
}

// A bad provider-level value names the provider's key.
func TestReadProvidersTimeoutsRejectsABadValue(t *testing.T) {
	loadYAML(t, `providers:
  gw:
    type: openai
    base_url: https://gw.example/v1
    timeouts:
      request: -5s
`)
	_, err := ReadProviders()
	assert.Require(t, assert.Err(t, err))
	assert.StrContains(t, err.Error(), "providers.gw.timeouts.request")
}

// llmRequestTimeout turns a resolved config value into the llm package's
// convention: "no limit" there is a negative number, 0 asks for the default.
func TestLLMRequestTimeoutMapping(t *testing.T) {
	assert.Eq(t, llm.NoTimeout, llmRequestTimeout(0))
	assert.Eq(t, llm.NoTimeout, llmRequestTimeout(-time.Second))
	assert.Eq(t, 90*time.Second, llmRequestTimeout(90*time.Second))
}
