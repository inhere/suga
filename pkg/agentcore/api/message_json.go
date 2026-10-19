package api

import "encoding/json"

// UnmarshalJSON decodes a message and restores a tool result the store kept
// only once. The store writes a tool message whose ToolResult.Content equals
// Content once, with a top-level ResultSameAsContent marker (see
// internal/personal/store.EncodeMessage); the decoded message is then exactly
// the one that was stored, marker cleared. Messages without the marker decode
// as before, and Message has no MarshalJSON: the encoding that the prefix hash
// and the exported JSON use is unchanged.
func (m *Message) UnmarshalJSON(data []byte) error {
	// plain drops the methods of Message so this decode cannot recurse.
	type plain Message
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.ResultSameAsContent && v.ToolResult != nil && v.ToolResult.Content == "" {
		v.ToolResult.Content = v.Content
	}
	// The marker is a storage detail: a decoded message compares equal to the
	// one the store captured.
	v.ResultSameAsContent = false
	*m = Message(v)
	return nil
}
