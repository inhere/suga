package llm

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// FuzzSseParsers feeds arbitrary bytes through the three gateway stream
// parsers (the SSE entry points of pkg/agentcore/llm): a truncated line, a
// packet arriving out of order or a non-JSON data line must end in an error,
// never a panic or a hang. The recorded fixtures (testdata/sse) seed the
// corpus.
func FuzzSseParsers(f *testing.F) {
	seeds, err := filepath.Glob(filepath.Join("testdata", "sse", "*.sse"))
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range seeds {
		data, err := os.ReadFile(seed)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	// A few plain fragments on top of the recordings.
	f.Add([]byte("data: {}\n\ndata: [DONE]\n"))
	f.Add([]byte("data: {\"type\":\"message_start\"}"))
	f.Add([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}"))

	claude := NewClaudeProvider(ClaudeConfig{})
	openai := NewOpenAIProvider(OpenAIConfig{})
	responses := NewResponsesProvider(ResponsesConfig{OpenAIConfig: OpenAIConfig{}})
	f.Fuzz(func(t *testing.T, data []byte) {
		ctx := context.Background()
		r := bytes.NewReader(data)
		_, _ = claude.readStream(ctx, r, nil)
		r.Seek(0, 0)
		_, _ = openai.readStream(ctx, r, nil)
		r.Seek(0, 0)
		_, _ = responses.readResponsesStream(ctx, r, nil)
	})
}
