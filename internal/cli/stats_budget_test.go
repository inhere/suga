package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal/store"
)

func TestStatsTextMarksEstimatedCallsWithoutChangingUnmarkedShape(t *testing.T) {
	base := store.Report{Models: []store.ModelStat{{Provider: "provider", Model: "model", Calls: 2, CostUSD: 0.25}}}
	var plain bytes.Buffer
	assert.NoErr(t, writeStatsText(&plain, base))
	assert.NotContains(t, plain.String(), "EST")
	assert.NotContains(t, plain.String(), "估算")

	base.Models[0].EstimatedCalls = 1
	var marked bytes.Buffer
	assert.NoErr(t, writeStatsText(&marked, base))
	assert.StrContains(t, marked.String(), "EST")
	assert.StrContains(t, marked.String(), "1 次估算")
}

func TestStatsTextCleansProviderAndModelLabel(t *testing.T) {
	rep := store.Report{Models: []store.ModelStat{{Provider: "provider\x1b[31m", Model: "model\x1b[0m", Calls: 1}}}
	var out bytes.Buffer
	assert.NoErr(t, writeStatsText(&out, rep))
	assert.NotContains(t, out.String(), "\x1b")
	assert.StrContains(t, out.String(), "provider/model")
}

func TestStatsTextShowsFailedAttemptsAfterTotal(t *testing.T) {
	rep := store.Report{FailedAttempts: 1, CacheBuckets: []store.CacheBucketStat{{Bucket: store.BucketCold, Calls: 1, InputTokens: 100}}}
	var out bytes.Buffer
	assert.NoErr(t, writeStatsText(&out, rep))
	text := out.String()
	assert.StrContains(t, text, "total")
	assert.StrContains(t, text, "failed_attempts")
	assert.True(t, strings.Index(text, "total") < strings.Index(text, "failed_attempts"))
}
