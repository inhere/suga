package store

import (
	"bytes"
	"encoding/json"

	"github.com/inhere/suga/pkg/agentcore/api"
)

// Tool message storage (data governance P2, design 0.4).
//
// A tool message carries the same body twice: api.Message.Content and
// api.Message.ToolResult.Content (the providers read the latter, the context
// layers the former). 44,411 of them held 124MB of duplicate text on the
// 2026-10-19 ledger. EncodeMessage stores that body once, and api.Message's
// UnmarshalJSON restores it, so every read path (and the provider request
// bytes) stays what it was.
//
// Only the store dedupes: api.Message keeps its default encoding so that the
// prefix hash (prefix.go) and the exported JSON (hooks, export) do not change.

// ResultSameAsContentKey is the top-level key that marks a stored tool message
// whose ToolResult.Content was omitted because it equals Content. api.Message
// reads it on decode (see its UnmarshalJSON).
const ResultSameAsContentKey = "ResultSameAsContent"

// EncodeMessage encodes a message for an entries row. A tool message whose
// ToolResult repeats its Content is stored once, with the ResultSameAsContent
// marker; every other message is encoded exactly as json.Marshal encodes it, so
// the stored bytes of everything else (and of the provider replay) are
// untouched.
func EncodeMessage(m api.Message) ([]byte, error) {
	if m.ToolResult == nil || m.Content == "" || m.ToolResult.Content != m.Content {
		return json.Marshal(m)
	}
	// The fields are listed in the order api.Message declares them, so the
	// stored JSON differs from a plain encoding only by the omitted body and the
	// marker (TestEncodeMessageMatchesMessageFields guards the field list, and
	// `suga data dedupe-tool-results --undo` restores the plain bytes).
	return json.Marshal(dedupedMessage{
		Role:     m.Role,
		Content:  m.Content,
		ToolCall: m.ToolCall,
		ToolResult: &dedupedToolResult{
			CallID:    m.ToolResult.CallID,
			Name:      m.ToolResult.Name,
			Artifacts: m.ToolResult.Artifacts,
			IsError:   m.ToolResult.IsError,
			Metadata:  m.ToolResult.Metadata,
		},
		Metadata:            m.Metadata,
		Reasoning:           m.Reasoning,
		ResultSameAsContent: true,
	})
}

// dedupedMessage is the stored shape of a deduped tool message: the same keys
// in the same order as api.Message, with ToolResult's Content left out (it
// repeats Content) and the marker added at the end.
type dedupedMessage struct {
	Role       string
	Content    string
	ToolCall   *api.ToolCall
	ToolResult *dedupedToolResult
	Metadata   map[string]any
	Reasoning  string `json:",omitempty"`
	// ResultSameAsContent tells api.Message.UnmarshalJSON to restore the result.
	ResultSameAsContent bool
}

// dedupedToolResult is api.ToolResult minus Content: the fields and their
// order are the ones the default encoding of api.ToolResult produces, so a
// deduped row differs from a plain one only by the omitted body and the marker.
type dedupedToolResult struct {
	CallID    string
	Name      string
	Artifacts []api.Artifact
	IsError   bool
	Metadata  map[string]any
}

// RestoreMessageJSON returns a stored entry's JSON in the shape it had before
// the tool-result dedupe: an entry carrying the ResultSameAsContent marker is
// decoded (which restores ToolResult.Content) and encoded again the plain way;
// any other entry is returned as it is. Readers that hand stored JSON to the
// outside, such as `suga inspect export`, use it so the dedupe stays a storage
// detail.
func RestoreMessageJSON(raw []byte) []byte {
	if !bytes.Contains(raw, []byte(`"`+ResultSameAsContentKey+`"`)) {
		return raw
	}
	var m api.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
