package agentcore

// ExpandToolCalls allocates the assistant transcript messages for one model
// reply. It deliberately keeps the model's original calls; runtime execution
// may expand, coerce, or mutate a call before persistToolTranscript stores it.
func ExpandToolCalls(res *GenerateResult) []Message {
	if res == nil || len(res.ToolCalls) == 0 {
		return nil
	}
	batch := ""
	if len(res.ToolCalls) > 1 {
		batch = res.ToolCalls[0].CallID
	}
	messages := make([]Message, len(res.ToolCalls))
	for i := range res.ToolCalls {
		message := Message{
			Role:     "assistant",
			ToolCall: cloneToolCallValue(&res.ToolCalls[i]),
		}
		if i == 0 {
			message.Content = res.Content
			message.Reasoning = res.Reasoning
			message.Metadata = cloneMetadata(res.Metadata)
		}
		if len(res.ToolCalls) > 1 {
			if message.Metadata == nil {
				message.Metadata = map[string]any{}
			}
			message.Metadata[ToolBatchMetaKey] = batch
		}
		messages[i] = message
	}
	return messages
}
