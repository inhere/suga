package store

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore/api"
)

// Data governance P2: a tool message stores the same body in Content and
// ToolResult.Content; the store keeps it once and the decoder restores it.

// toolMessage is a tool message whose result repeats its Content, i.e. the one
// the store dedupes.
func toolMessage() api.Message {
	return api.Message{
		Role:     "tool",
		Content:  "total 2\n-rw-r--r-- 1 a.txt",
		ToolCall: &api.ToolCall{CallID: "c1", Name: "shell", Arguments: map[string]any{"cmd": "ls"}},
		ToolResult: &api.ToolResult{
			CallID:  "c1",
			Name:    "shell",
			Content: "total 2\n-rw-r--r-- 1 a.txt",
		},
	}
}

// objectKeys returns the keys of a JSON object in the order the encoding wrote
// them, so a test can tell that a field list did not change.
func objectKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, json.Delim('{'), tok)
	keys := []string{}
	for dec.More() {
		key, err := dec.Token()
		assert.Require(t, assert.NoErr(t, err))
		keys = append(keys, key.(string))
		var skip json.RawMessage
		assert.Require(t, assert.NoErr(t, dec.Decode(&skip)))
	}
	return keys
}

// objectField returns one field of a JSON object as raw bytes ("" when absent).
func objectField(t *testing.T, raw []byte, name string) json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	assert.Require(t, assert.NoErr(t, json.Unmarshal(raw, &obj)))
	return obj[name]
}

func TestEncodeMessage(t *testing.T) {
	call := &api.ToolCall{CallID: "c1", Name: "shell"}
	cases := []struct {
		name    string
		msg     api.Message
		deduped bool
	}{
		{name: "user message", msg: api.Message{Role: "user", Content: "hello"}},
		{name: "assistant with tool call", msg: api.Message{Role: "assistant", Content: "calling",
			ToolCall: call}},
		{name: "tool result differs", msg: api.Message{Role: "tool", Content: "short",
			ToolCall: call, ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: "long"}}},
		{name: "tool without result", msg: api.Message{Role: "tool", Content: "body", ToolCall: call}},
		{name: "tool with empty content", msg: api.Message{Role: "tool", Content: "",
			ToolCall: call, ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: ""}}},
		{name: "tool double stored", msg: toolMessage(), deduped: true},
		{name: "tool error result", msg: api.Message{Role: "tool", Content: "boom", ToolCall: call,
			ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: "boom", IsError: true}},
			deduped: true},
		{name: "tool with artifacts and metadata", msg: api.Message{Role: "tool", Content: "art",
			ToolCall:   call,
			ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: "art", Artifacts: []api.Artifact{{Kind: "file", URI: "out:a", Content: "x"}}, Metadata: map[string]any{"path": "/x"}},
			Metadata:   map[string]any{"suga.tool_batch": "b1"},
			Reasoning:  "why"}, deduped: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EncodeMessage(c.msg)
			assert.Require(t, assert.NoErr(t, err))
			plain, err := json.Marshal(c.msg)
			assert.Require(t, assert.NoErr(t, err))
			if !c.deduped {
				// Everything else is byte for byte what json.Marshal writes.
				assert.Eq(t, string(plain), string(got))
				return
			}
			assert.True(t, !bytes.Equal(got, plain))
			// The body is stored once: ToolResult has no Content and the marker
			// is set at the top level.
			assert.Eq(t, "true", string(objectField(t, got, ResultSameAsContentKey)))
			result := objectField(t, got, "ToolResult")
			assert.Eq(t, 0, len(objectField(t, result, "Content")))
		})
	}
}

// TestEncodeMessageKeepsMessageFields guards the deduped shape against a new
// field in api.Message or api.ToolResult: the deduped encoding must carry every
// key of the plain one, in the same order, minus ToolResult.Content.
func TestEncodeMessageKeepsMessageFields(t *testing.T) {
	msg := toolMessage()
	msg.ToolCall.ArgumentsError = "bad"
	msg.ToolResult.Artifacts = []api.Artifact{{Kind: "file", URI: "out:a", Content: "x", Metadata: map[string]any{"m": "1"}}}
	msg.ToolResult.IsError = true
	msg.ToolResult.Metadata = map[string]any{"path": "/x"}
	msg.Metadata = map[string]any{"suga.tool_batch": "b1"}
	msg.Reasoning = "why"

	plain, err := json.Marshal(msg)
	assert.Require(t, assert.NoErr(t, err))
	deduped, err := EncodeMessage(msg)
	assert.Require(t, assert.NoErr(t, err))

	wantKeys := append(objectKeys(t, plain), ResultSameAsContentKey)
	assert.Eq(t, wantKeys, objectKeys(t, deduped))

	wantResult := []string{}
	for _, key := range objectKeys(t, objectField(t, plain, "ToolResult")) {
		if key != "Content" {
			wantResult = append(wantResult, key)
		}
	}
	assert.Eq(t, wantResult, objectKeys(t, objectField(t, deduped, "ToolResult")))
}

func TestDecodeMessageRestoresToolResult(t *testing.T) {
	cases := []struct {
		name string
		msg  api.Message
	}{
		{name: "double stored result", msg: toolMessage()},
		{name: "error result", msg: api.Message{Role: "tool", Content: "boom",
			ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: "boom", IsError: true}}},
		{name: "result with artifacts", msg: api.Message{Role: "tool", Content: "art",
			ToolResult: &api.ToolResult{CallID: "c1", Name: "shell", Content: "art",
				Artifacts: []api.Artifact{{Kind: "file", URI: "out:a", Content: "x"}},
				Metadata:  map[string]any{"path": "/x"}},
			Metadata: map[string]any{"suga.tool_batch": "b1"}}},
		{name: "plain user message", msg: api.Message{Role: "user", Content: "hello", Metadata: map[string]any{"k": float64(1)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stored, err := EncodeMessage(c.msg)
			assert.Require(t, assert.NoErr(t, err))
			var back api.Message
			assert.Require(t, assert.NoErr(t, json.Unmarshal(stored, &back)))
			// The decoded message is the one that was stored, marker cleared.
			assert.True(t, reflect.DeepEqual(c.msg, back))
			// And it encodes exactly as before the dedupe, so the provider
			// request bytes and the prefix hash do not change.
			plain, err := json.Marshal(c.msg)
			assert.Require(t, assert.NoErr(t, err))
			again, err := json.Marshal(back)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, string(plain), string(again))
			// The round trip through the store is idempotent.
			restored, err := EncodeMessage(back)
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, string(stored), string(restored))
		})
	}
	t.Run("marker without a result", func(t *testing.T) {
		var m api.Message
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(`{"Role":"user","Content":"hi","ResultSameAsContent":true}`), &m)))
		assert.Eq(t, "hi", m.Content)
		assert.Eq(t, (*api.ToolResult)(nil), m.ToolResult)
		assert.False(t, m.ResultSameAsContent)
	})
	t.Run("marker with a kept result", func(t *testing.T) {
		var m api.Message
		raw := `{"Role":"tool","Content":"a","ToolResult":{"CallID":"c1","Name":"s","Content":"b"},"ResultSameAsContent":true}`
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(raw), &m)))
		// Only an empty result is restored: a different body stays as stored.
		assert.Eq(t, "b", m.ToolResult.Content)
		assert.False(t, m.ResultSameAsContent)
	})
}
