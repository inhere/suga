package main

import (
	"bufio"
	"bytes"
	"encoding/json"
)

// traceCall is the token usage of one model event in `suga inspect trace --json`.
// In the trace, input_tokens INCLUDES cache read/write (the store's raw
// convention; only stats/reports subtract them), so the uncached part is
// input - cache_read - cache_write.
type traceCall struct {
	Input, CacheRead, CacheWrite int64
}

func (c traceCall) uncached() int64 { return max(c.Input-c.CacheRead-c.CacheWrite, 0) }

// firstMainCall returns the first successful (status ok) model event that is
// not a compaction call (attrs.purpose == "compact") from the JSONL trace.
func firstMainCall(trace []byte) (traceCall, bool) {
	sc := bufio.NewScanner(bytes.NewReader(trace))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Kind       string `json:"kind"`
			Status     string `json:"status"`
			Input      int64  `json:"input_tokens"`
			CacheRead  int64  `json:"cache_read_tokens"`
			CacheWrite int64  `json:"cache_write_tokens"`
			Attrs      struct {
				Purpose string `json:"purpose"`
			} `json:"attrs"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Kind != "model" || ev.Status != "ok" || ev.Attrs.Purpose == "compact" {
			continue
		}
		return traceCall{Input: ev.Input, CacheRead: ev.CacheRead, CacheWrite: ev.CacheWrite}, true
	}
	return traceCall{}, false
}
