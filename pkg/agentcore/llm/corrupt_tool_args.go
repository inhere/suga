package llm

import (
	"fmt"
	"strings"

	agentcoreapi "github.com/inhere/suga/pkg/agentcore/api"
)

// harmonyMarkers are control strings of the model's internal reasoning
// channel (the harmony format). A tool argument is data for a tool; when one of
// these shows up inside an argument string the upstream (a relay, or a server
// that does not constrain function arguments) leaked reasoning text into the
// call. Each marker is specific enough not to occur in ordinary paths, code or
// prose: a lone "#" (docs/#notes.md) is not "#+#+".
var harmonyMarkers = []string{
	"to=functions.",
	"(commentary)",
	"<|channel|>",
	"<|message|>",
	"assistant,analysis",
	"#+#+",
}

// corruptedOutputReason is the retry cause shown by a frontend before the wait
// and recorded as the StreamEventRetry delta, so a trace shows why the answer
// was requested again.
const corruptedOutputReason = "tool arguments carry reasoning-channel text"

// corruptedOutputError names a response whose tool arguments contain reasoning
// channel text: the upstream output is corrupt, the call must not be run. It is
// a retryableStreamError, so retryStream requests the answer again; the
// arguments are never "cleaned", since what the model meant is unknown.
type corruptedOutputError struct {
	tool   string
	marker string
}

func (e *corruptedOutputError) Error() string {
	return fmt.Sprintf("tool call %q arguments carry reasoning-channel text (%q); the upstream output is corrupt", e.tool, e.marker)
}

// retryReason implements retryableStreamError.
func (e *corruptedOutputError) retryReason() string { return corruptedOutputReason }

// giveUp implements retryableStreamError: the corruption outlived every attempt.
// It wraps a badResponseError, so ErrorKind reports provider_bad_response.
func (e *corruptedOutputError) giveUp(provider string, attempts int) error {
	return fmt.Errorf("%s returned tool arguments with reasoning-channel text in %d attempts, the channel may be faulty: %w",
		provider, attempts, badResponsef("%s", e.Error()))
}

// checkCorruptedToolCalls returns a corruptedOutputError for the first tool call
// that has a harmony control string in any of its string arguments (nested
// values included), nil when all calls are clean. Calls with an
// ArgumentsError carry no decoded arguments and are skipped.
func checkCorruptedToolCalls(calls []agentcoreapi.ToolCall) error {
	for _, c := range calls {
		if c.ArgumentsError != "" {
			continue
		}
		if m := findHarmonyMarker(c.Arguments); m != "" {
			return &corruptedOutputError{tool: c.Name, marker: m}
		}
	}
	return nil
}

func findHarmonyMarker(v any) string {
	switch x := v.(type) {
	case string:
		for _, m := range harmonyMarkers {
			if strings.Contains(x, m) {
				return m
			}
		}
	case map[string]any:
		for _, e := range x {
			if m := findHarmonyMarker(e); m != "" {
				return m
			}
		}
	case []any:
		for _, e := range x {
			if m := findHarmonyMarker(e); m != "" {
				return m
			}
		}
	}
	return ""
}
