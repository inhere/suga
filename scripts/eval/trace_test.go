package main

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestFirstMainCall(t *testing.T) {
	t.Run("skips plan errors and compact", func(t *testing.T) {
		trace := `{"kind":"plan","plan":[]}
{"kind":"model","status":"error","input_tokens":999}
{"kind":"model","status":"ok","input_tokens":500,"attrs":{"purpose":"compact"}}
{"kind":"model","status":"ok","input_tokens":1000,"cache_read_tokens":700,"cache_write_tokens":100,"attrs":{"attempt":1}}
{"kind":"model","status":"ok","input_tokens":1200,"cache_read_tokens":1000}
`
		c, ok := firstMainCall([]byte(trace))
		assert.Require(t, assert.Eq(t, true, ok))
		assert.Eq(t, int64(200), c.uncached())
		assert.Eq(t, int64(700), c.CacheRead)
	})
	t.Run("none", func(t *testing.T) {
		_, ok := firstMainCall([]byte(`{"kind":"tool","status":"ok"}` + "\n"))
		assert.Eq(t, false, ok)
	})
	t.Run("uncached never negative", func(t *testing.T) {
		assert.Eq(t, int64(0), traceCall{Input: 10, CacheRead: 20}.uncached())
	})
}

// TestProviderFaultMs covers the failed-attempt time read from the trace: the
// duration of every model event that failed plus the backoff it waited.
func TestProviderFaultMs(t *testing.T) {
	trace := `{"kind":"model","status":"ok","duration_ms":900,"attrs":{"retries":2,"retry_wait_ms":300}}
{"kind":"model","status":"error","duration_ms":400,"attrs":{"retry_wait_ms":600,"fallback":"next"}}
{"kind":"model","status":"error","duration_ms":100}
{"kind":"model","status":"ok","duration_ms":500}
{"kind":"tool","status":"error","duration_ms":100}
`
	assert.Eq(t, int64(1100), providerFaultMs([]byte(trace)))

	t.Run("no model event", func(t *testing.T) {
		assert.Eq(t, int64(0), providerFaultMs([]byte("{\"kind\":\"plan\",\"plan\":[]}\n")))
	})
}

func TestAggregateProbe(t *testing.T) {
	rs := []ProbeRun{
		{Group: "same", Input: 1000, CacheRead: 800, Uncached: 200, WarmInput: 1000},
		{Group: "same", Input: 1000, CacheRead: 600, Uncached: 400, WarmInput: 1000},
		{Group: "same", Error: "boom"},
		{Group: "control", Input: 1000, Uncached: 1000, WarmInput: 1000},
	}
	m := aggregateProbe(rs)
	t.Run("same skips failed runs", func(t *testing.T) {
		s := m["same"]
		assert.Eq(t, 3, s.n)
		assert.Eq(t, 1, s.failed)
		assert.Eq(t, 1400.0, s.cacheRead)
		assert.Eq(t, 1.4, s.ratio)
	})
	t.Run("control has no hits", func(t *testing.T) {
		assert.Eq(t, 0.0, m["control"].cacheRead)
	})
}
