package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// cachedUsageProvider reports provider-normalized usage where input_tokens
// includes the cache-read tokens (as the OpenAI and Claude providers do).
type cachedUsageProvider struct{}

func (cachedUsageProvider) Name() string { return "cached-usage" }

func (cachedUsageProvider) Generate(context.Context, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	return &agentcore.GenerateResult{
		Content: "done", FinishReason: agentcore.FinishFinal,
		Usage: agentcore.Usage{InputTokens: 14, OutputTokens: 6, TotalTokens: 20, CacheReadTokens: 4},
	}, nil
}

type contextUsageProvider struct{ input int }

func (p contextUsageProvider) Name() string { return "context-usage" }

func (p contextUsageProvider) Generate(context.Context, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	return &agentcore.GenerateResult{
		Content: "done", FinishReason: agentcore.FinishFinal,
		Usage: agentcore.Usage{InputTokens: p.input, OutputTokens: 1, TotalTokens: p.input + 1},
	}, nil
}

type yieldAfterModelHook struct{ agentcore.NopHook }

func (yieldAfterModelHook) AfterModel(_ context.Context, _ *agentcore.ModelEvent, ctrl *agentcore.Control) error {
	ctrl.Yield("input_required", nil)
	return nil
}

func lastJSONLine(t *testing.T, out string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var line map[string]any
	assert.NoErr(t, json.Unmarshal([]byte(lines[len(lines)-1]), &line))
	return line
}

func TestStreamJSONUsageUsesAnthropicAccounting(t *testing.T) {
	// usage is the run total (every field present, plus tool_calls);
	// session_usage keeps the compact shape without zero cache/cost fields.
	want := map[string]any{"input_tokens": float64(10), "output_tokens": float64(6), "cache_read_tokens": float64(4),
		"cache_write_tokens": float64(0), "total_tokens": float64(20), "cost_usd": float64(0), "tool_calls": float64(0)}
	wantSession := map[string]any{"input_tokens": float64(10), "output_tokens": float64(6), "cache_read_tokens": float64(4), "total_tokens": float64(20)}
	t.Run("result line", func(t *testing.T) {
		runner, _ := newCLITestRunner(t, cachedUsageProvider{})
		var stdout, stderr bytes.Buffer
		assert.NoErr(t, executePersonalRun(context.Background(), runner, personalOptions{Prompt: "x", OutputFormat: "stream-json"}, &stdout, &stderr))
		line := lastJSONLine(t, stdout.String())
		assert.Eq(t, "result", line["type"])
		assert.Eq(t, want, line["usage"])
		assert.Eq(t, wantSession, line["session_usage"])
	})
	t.Run("input_required line", func(t *testing.T) {
		runner, _ := newCLITestRunner(t, cachedUsageProvider{}, yieldAfterModelHook{})
		var stdout, stderr bytes.Buffer
		err := executePersonalRun(context.Background(), runner, personalOptions{Prompt: "x", OutputFormat: "stream-json"}, &stdout, &stderr)
		assert.Eq(t, true, errors.Is(err, ErrInputRequired))
		line := lastJSONLine(t, stdout.String())
		assert.Eq(t, "input_required", line["type"])
		assert.Eq(t, want, line["usage"])
		assert.Eq(t, wantSession, line["session_usage"])
	})
	t.Run("error line carries usage fields", func(t *testing.T) {
		runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Error(errors.New("boom"))))
		var stdout, stderr bytes.Buffer
		err := executePersonalRun(context.Background(), runner, personalOptions{Prompt: "x", OutputFormat: "stream-json"}, &stdout, &stderr)
		assert.Eq(t, true, err != nil)
		line := lastJSONLine(t, stdout.String())
		assert.Eq(t, "error", line["type"])
		assert.Eq(t, map[string]any{"input_tokens": float64(0), "output_tokens": float64(0), "cache_read_tokens": float64(0),
			"cache_write_tokens": float64(0), "total_tokens": float64(0), "cost_usd": float64(0), "tool_calls": float64(0)}, line["usage"])
		assert.Eq(t, true, line["session_usage"] != nil)
	})
}

// compactingUsageProvider drives a long tool loop: calls with tools answer
// with a "big" tool call (the first `steps` times) and then a final answer;
// a call without tools is the compaction summary request. Every call reports
// usage (input includes the cache read part, like the real providers).
type compactingUsageProvider struct {
	mu          sync.Mutex
	steps       int
	main, sums  int
	outPerMain  int
	outPerSum   int
	cacheReadIn int
}

func (*compactingUsageProvider) Name() string { return "compacting-usage" }

func (p *compactingUsageProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(req.Tools) == 0 {
		p.sums++
		return &agentcore.GenerateResult{Content: "SUMMARY", FinishReason: agentcore.FinishFinal,
			Usage: agentcore.Usage{InputTokens: 300, OutputTokens: p.outPerSum}}, nil
	}
	p.main++
	// A realistic input size (~4 chars per token): the runner estimates the
	// next request from the last reported input, so a tiny number would
	// never let the context reach the compaction threshold.
	chars := len(req.System)
	for _, m := range req.Messages {
		chars += len(m.Content)
	}
	usage := agentcore.Usage{InputTokens: chars/4 + p.cacheReadIn, OutputTokens: p.outPerMain, CacheReadTokens: p.cacheReadIn}
	if p.main <= p.steps {
		return &agentcore.GenerateResult{FinishReason: agentcore.FinishToolCalls, Usage: usage,
			ToolCalls: []agentcore.ToolCall{{CallID: fmt.Sprintf("c%d", p.main), Name: "big", Arguments: map[string]any{}}}}, nil
	}
	return &agentcore.GenerateResult{Content: "done", FinishReason: agentcore.FinishFinal, Usage: usage}, nil
}

// bigOutputTool returns a large result so a small context window compacts.
type bigOutputTool struct{}

func (bigOutputTool) Name() string        { return "big" }
func (bigOutputTool) Description() string { return "big output" }
func (bigOutputTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: "big", InputSchema: map[string]any{"type": "object"}}
}
func (bigOutputTool) Execute(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
	return &agentcore.ToolResult{Name: "big", Content: strings.Repeat("x", 2000)}, nil
}

// Each model call (compaction included) prints a run-cumulative "usage" line;
// they only grow, and turn_end/result carry the last value.
func TestStreamJSONUsageLinesPerModelCall(t *testing.T) {
	repo, err := store.Open(newTestStorePath(t))
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = repo.Close() })
	reg := tools.NewRegistry()
	reg.Register(bigOutputTool{})
	prov := &compactingUsageProvider{steps: 8, outPerMain: 5, outPerSum: 20, cacheReadIn: 10}
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: prov, Tools: reg, Model: "m", ProviderName: "p", MaxSteps: 40,
		// threshold = 3000-500 tokens (~10000 chars): a few 2000-char results compact.
		ContextWindow: 3000, CompactReserveTokens: 500,
	})
	assert.Require(t, assert.NoErr(t, err))
	var stdout, stderr bytes.Buffer
	assert.Require(t, assert.NoErr(t, executePersonalRun(context.Background(), runner, personalOptions{Prompt: "long job", OutputFormat: "stream-json"}, &stdout, &stderr)))

	var usages []map[string]any
	var turnEnd, result map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var line map[string]any
		assert.Require(t, assert.NoErr(t, json.Unmarshal([]byte(raw), &line)))
		switch line["type"] {
		case "usage":
			assert.Eq(t, true, line["session_id"] != "" && line["run_id"] != "")
			usages = append(usages, line["usage"].(map[string]any))
		case "turn_end":
			turnEnd = line["usage"].(map[string]any)
		case "result":
			result = line["usage"].(map[string]any)
		}
	}
	assert.Require(t, assert.Gt(t, prov.sums, 0)) // at least one compaction call
	assert.Require(t, assert.Eq(t, prov.main+prov.sums, len(usages)))
	for i := 1; i < len(usages); i++ {
		prev, cur := usages[i-1], usages[i]
		for _, key := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "tool_calls"} {
			assert.Eq(t, true, cur[key].(float64) >= prev[key].(float64), key)
		}
		assert.Eq(t, true, cur["total_tokens"].(float64) > prev["total_tokens"].(float64))
	}
	last := usages[len(usages)-1]
	assert.Eq(t, last, turnEnd)
	assert.Eq(t, last, result)
	// The run total includes the compaction calls and counts every tool call.
	assert.Eq(t, float64(prov.main*5+prov.sums*20), result["output_tokens"])
	assert.Eq(t, float64(prov.main*10), result["cache_read_tokens"])
	assert.Eq(t, float64(8), result["tool_calls"])
}

// End to end through the real flag parser: an explicit empty value (both
// "--flag ”" and "--flag=") clears the saved append prompt; an absent flag keeps it.
// The append prompt flags of one session: the first run freezes "saved" in the
// segment context; later runs append a context update only when the effective
// value changes (what the model sees; session meta only caches the segment).
func TestAppendSystemPromptCanBeClearedFromCLI(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_APPEND_SYSTEM_PROMPT", "")
	t.Setenv("SUGA_MODEL", "")
	home := shortTestHome(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUGA_HOME", "") // fall back to HOME/.suga despite the TestMain default
	workspace := t.TempDir()
	t.Chdir(workspace)
	_ = gkconfig.Set("workspace", workspace)
	_ = gkconfig.Set("model", "openai/m")
	var mu sync.Mutex
	var last []string // user message contents of the latest request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		last = last[:0]
		for _, m := range body.Messages {
			if m.Role == "user" {
				last = append(last, m.Content)
			}
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	}))
	t.Cleanup(server.Close)
	setTestProvider(t, server.URL)

	const cleared = "附加提示已清除"
	// run returns the segment context and the context updates the model saw.
	run := func(extra ...string) (segment string, updates []string) {
		t.Helper()
		args := append([]string{"--prompt", "hi", "--read-only", "--session-id", "clear-cli"}, extra...)
		assert.NoErr(t, RunPersonalCLI(context.Background(), "run", args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}))
		mu.Lock()
		defer mu.Unlock()
		for _, c := range last {
			if strings.HasPrefix(c, "# 上下文更新") {
				updates = append(updates, c)
			}
		}
		return last[0], updates
	}
	segment, updates := run("--append-system-prompt", "saved")
	assert.StrContains(t, segment, "saved")
	assert.Eq(t, 0, len(updates))
	_, updates = run() // absent flag keeps the saved prompt
	assert.Eq(t, 0, len(updates))
	_, updates = run("--append-system-prompt", "") // empty keeps it: Gofer renders an unset system prompt as ""
	assert.Eq(t, 0, len(updates))
	_, updates = run("--clear-append-system-prompt")
	assert.Require(t, assert.Eq(t, 1, len(updates)))
	assert.StrContains(t, updates[0], cleared)
	_, updates = run("--append-system-prompt", "again")
	assert.Require(t, assert.Eq(t, 2, len(updates)))
	assert.StrContains(t, updates[1], "again")
	_, updates = run("--clear-append-system-prompt", "--append-system-prompt", "") // the Gofer argv shape still clears
	assert.Require(t, assert.Eq(t, 3, len(updates)))
	assert.StrContains(t, updates[2], cleared)
	_, updates = run() // nothing changes any more
	assert.Eq(t, 3, len(updates))
}

// Gofer contract: a missing --resume target or --from source exits 4 with a
// "session not found: <id>" message, distinct from other run failures.
func TestMissingSessionIsStableError(t *testing.T) {
	for _, opts := range []personalOptions{
		{Prompt: "x", ResumeID: "gone-session"},
		{Prompt: "x", FromID: "gone-session", SessionID: "fresh-child"},
	} {
		runner, _ := newCLITestRunner(t, cachedUsageProvider{})
		err := executePersonalRun(context.Background(), runner, opts, &bytes.Buffer{}, &bytes.Buffer{})
		var notFound personal.SessionNotFoundError
		assert.Eq(t, true, errors.As(err, &notFound))
		assert.Eq(t, 4, notFound.ExitCode())
		assert.Eq(t, "session not found: gone-session", err.Error())
		assert.Eq(t, true, errors.Is(err, store.ErrSessionNotFound))
	}
}

// TestRunTextModeShowsContextShare checks the real text-mode closing path uses
// the same context ratio as chat, while omitting it when the window is unknown.
func TestRunTextModeShowsContextShare(t *testing.T) {
	for _, tc := range []struct {
		name, want    string
		input, window int
	}{
		{name: "rounds down to zero", input: 9, window: 1000, want: "ctx 0%"},
		{name: "reports an oversized request", input: 1050, window: 1000, want: "ctx 105%"},
		{name: "unknown window is omitted", input: 550},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := store.Open(newTestStorePath(t))
			assert.Require(t, assert.NoErr(t, err))
			t.Cleanup(func() { _ = repo.Close() })
			runner, err := personal.New(personal.Config{
				Repository: repo, Provider: contextUsageProvider{input: tc.input},
				ContextWindow: tc.window,
			})
			assert.Require(t, assert.NoErr(t, err))
			var stdout, stderr bytes.Buffer
			assert.NoErr(t, executePersonalRun(context.Background(), runner,
				personalOptions{Prompt: "x", OutputFormat: "text"}, &stdout, &stderr))
			if tc.want == "" {
				assert.StrNotContains(t, stderr.String(), "ctx ")
				return
			}
			assert.StrContains(t, stderr.String(), tc.want)
		})
	}
}

// TestStreamJSONResultContext pins the result event's context object: the
// provider-reported used/share when the model window is known, and the turn's
// own estimate (est_tokens) with the window compaction measures against in
// every case the turn sent a request.
func TestStreamJSONResultContext(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		window                          int
		wantContext                     bool
		wantUsed, wantWindow, wantShare float64
	}{
		{name: "known window", window: 1000, wantContext: true, wantUsed: 550, wantWindow: 1000, wantShare: 55},
		// Without a configured window the default one of the trace's context
		// event applies, so used and the estimate are both reported.
		{name: "default window", wantContext: true, wantUsed: 550, wantWindow: 128000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, err := store.Open(newTestStorePath(t))
			assert.Require(t, assert.NoErr(t, err))
			t.Cleanup(func() { _ = repo.Close() })
			runner, err := personal.New(personal.Config{
				Repository: repo, Provider: contextUsageProvider{input: 550}, ContextWindow: tc.window,
			})
			assert.Require(t, assert.NoErr(t, err))
			var stdout, stderr bytes.Buffer
			assert.NoErr(t, executePersonalRun(context.Background(), runner,
				personalOptions{Prompt: "x", OutputFormat: "stream-json"}, &stdout, &stderr))
			line := lastJSONLine(t, stdout.String())
			contextValue, ok := line["context"].(map[string]any)
			assert.Eq(t, tc.wantContext, ok)
			if !tc.wantContext {
				return
			}
			assert.Eq(t, tc.wantWindow, contextValue["window"])
			assert.Eq(t, true, contextValue["est_tokens"].(float64) > 0)
			assert.Eq(t, tc.wantUsed, contextValue["used"])
			assert.Eq(t, tc.wantShare, contextValue["share"])
		})
	}
}

// Text mode prints the model at the start and a usage summary at the end on
// stderr; stdout keeps only the answer. stream-json output remains machine-readable.
func TestRunTextModeShowsModelAndSummary(t *testing.T) {
	runner, _ := newCLITestRunner(t, cachedUsageProvider{})
	var stdout, stderr bytes.Buffer
	assert.NoErr(t, executePersonalRun(context.Background(), runner, personalOptions{Prompt: "x", OutputFormat: "text"}, &stdout, &stderr))
	assert.Eq(t, "done\n", stdout.String())
	errOut := stderr.String()
	assert.StrContains(t, errOut, "model: ")
	assert.StrContains(t, errOut, "in 14 / out 6 tokens")

	var jsonOut, jsonErr bytes.Buffer
	assert.NoErr(t, executePersonalRun(context.Background(), runner, personalOptions{Prompt: "x", OutputFormat: "stream-json"}, &jsonOut, &jsonErr))
	assert.NotContains(t, jsonErr.String(), "model: ")
	assert.StrContains(t, strings.SplitN(jsonOut.String(), "\n", 2)[0], `"type":"session"`)
}
