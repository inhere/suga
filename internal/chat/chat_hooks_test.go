package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	appconfig "github.com/inhere/suga/internal/config"
	sugahooks "github.com/inhere/suga/internal/hooks"
	"github.com/inhere/suga/internal/personal"
	personalprocess "github.com/inhere/suga/internal/personal/process"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

type hookTestProvider struct {
	mu    sync.Mutex
	calls [][]agentcore.Message
	count int
}

func (p *hookTestProvider) Name() string { return "hook-test" }
func (p *hookTestProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, append([]agentcore.Message(nil), req.Messages...))
	p.count++
	return &agentcore.GenerateResult{Content: fmt.Sprintf("answer-%d", p.count), FinishReason: agentcore.FinishFinal}, nil
}

type fakeHookClient struct {
	mu         sync.Mutex
	events     []sugahooks.Event
	stopCount  int
	startCount chan struct{}
	eventWake  chan struct{}
	blockFirst bool
	// stopReason is the block reason of the first Stop (default: a web reply
	// marker); a test may make it look like a chat command.
	stopReason string
	// lostOnInterrupt makes every Stop wait report this web reply when interrupted.
	lostOnInterrupt string
	// startNotice is returned as the SessionStart response Notice.
	startNotice string
	// postTool selects PostToolUse tools; empty sends none, like an unset hooks.post_tool_use.
	postTool sugahooks.PostToolFilter
}

func (f *fakeHookClient) PostToolName(tool string) (string, bool) { return f.postTool.Match(tool) }

func newFakeHookClient() *fakeHookClient {
	return &fakeHookClient{startCount: make(chan struct{}, 8), eventWake: make(chan struct{}, 64), blockFirst: true}
}

func (f *fakeHookClient) record(event sugahooks.Event) {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
	select {
	case f.eventWake <- struct{}{}:
	default:
	}
}

func (f *fakeHookClient) Call(_ context.Context, event sugahooks.Event) sugahooks.Response {
	f.record(event)
	if event.Name == "SessionStart" {
		return sugahooks.Response{AdditionalContext: "startup-context", Notice: f.startNotice}
	}
	if event.Name == "UserPromptSubmit" {
		return sugahooks.Response{AdditionalContext: "submit-context"}
	}
	return sugahooks.Response{}
}

func (f *fakeHookClient) StartStop(_ context.Context, event sugahooks.Event) sugahooks.Wait {
	f.record(event)
	f.mu.Lock()
	f.stopCount++
	stopCount := f.stopCount
	f.mu.Unlock()
	f.startCount <- struct{}{}
	done := make(chan sugahooks.Result, 1)
	if stopCount == 1 && f.blockFirst {
		reason := f.stopReason
		if reason == "" {
			reason = "[gofer web 回复] continue"
		}
		done <- sugahooks.Result{Response: sugahooks.Response{Decision: "block", Reason: reason}}
	}
	return &fakeStopWait{done: done, interrupt: func() {
		interrupt := event
		interrupt.Name = "Interrupt"
		interrupt.LastAssistant = ""
		f.Call(context.Background(), interrupt)
	}, lost: f.lostOnInterrupt}
}

func (f *fakeHookClient) snapshot() []sugahooks.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sugahooks.Event(nil), f.events...)
}

type fakeStopWait struct {
	done      chan sugahooks.Result
	interrupt func()
	// lost is the undelivered web reply Interrupt reports back (race simulation).
	lost string
}

func (w *fakeStopWait) Done() <-chan sugahooks.Result { return w.done }
func (w *fakeStopWait) Interrupt(context.Context) string {
	w.interrupt()
	return w.lost
}

func TestPersonalChatBlockStartsInjectedTurnAndMarksStopActive(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	provider := &hookTestProvider{}
	runner, _ := newCLITestRunner(t, provider)
	hooks := newFakeHookClient()
	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{}, reader, &stdout, &stderr, hooks)
	}()
	if _, err := io.WriteString(writer, "first prompt\n"); err != nil {
		t.Fatal(err)
	}
	waitHookEvent(t, hooks, "UserPromptSubmit", 2)
	if _, err := io.WriteString(writer, ":quit\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	events := hooks.snapshot()
	var prompts []sugahooks.Event
	var stops []sugahooks.Event
	for _, event := range events {
		if event.Name == "UserPromptSubmit" {
			prompts = append(prompts, event)
		}
		if event.Name == "Stop" {
			stops = append(stops, event)
		}
	}
	if len(prompts) != 2 || prompts[1].Prompt != "[gofer web 回复] continue" {
		t.Fatalf("expected block reason as a new submitted prompt, got %#v", prompts)
	}
	if prompts[0].Injected || !prompts[1].Injected {
		t.Fatalf("terminal prompt must not be injected and Stop-block prompt must be: %#v", prompts)
	}
	if prompts[0].TranscriptPath == "" {
		t.Fatal("SessionStart and prompt hooks should receive the transcript path")
	}
	if _, err := os.Stat(prompts[0].TranscriptPath); err != nil {
		t.Fatalf("transcript should exist before SessionStart: %v", err)
	}
	if len(stops) != 2 || !stops[1].StopHookActive {
		t.Fatalf("expected injected turn Stop to set stop_hook_active, got %#v", stops)
	}
	if !strings.Contains(stdout.String(), "[gofer web 回复] continue") {
		t.Fatalf("injected reply was not shown: %q", stdout.String())
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.calls) != 2 || !hasUserText(provider.calls[0], "startup-context") || !hasUserText(provider.calls[0], "submit-context") {
		t.Fatalf("hook context did not reach the next model request: %#v", provider.calls)
	}
}

func TestPersonalChatInterruptsStopBeforeNextPromptAndContinuesWithoutHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUGA_HOME", home)
	runner, _ := newCLITestRunner(t, &hookTestProvider{})
	hooks := newFakeHookClient()
	var stdout, stderr strings.Builder
	err := Run(context.Background(), runner, Options{}, strings.NewReader("first\nsecond\n:quit\n"), &stdout, &stderr, hooks)
	if err != nil {
		t.Fatal(err)
	}
	events := hooks.snapshot()
	firstStop, interrupt, secondPrompt := -1, -1, -1
	for i, event := range events {
		if firstStop < 0 && event.Name == "Stop" {
			firstStop = i
		}
		if firstStop >= 0 && interrupt < 0 && event.Name == "Interrupt" {
			interrupt = i
		}
		if interrupt >= 0 && secondPrompt < 0 && event.Name == "UserPromptSubmit" && event.Prompt == "second" {
			secondPrompt = i
		}
	}
	if firstStop < 0 || interrupt <= firstStop || secondPrompt <= interrupt {
		t.Fatalf("expected Stop, Interrupt, then next UserPromptSubmit; got %#v", events)
	}
	if err := Run(context.Background(), runner, Options{}, strings.NewReader(":quit\n"), &stdout, &stderr, nil); err != nil {
		t.Fatalf("missing hooks interrupted the conversation: %v", err)
	}
}

func TestPersonalChatInterruptsOnFirstByteBeforeLineSubmission(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	runner, _ := newCLITestRunner(t, &hookTestProvider{})
	hooks := newFakeHookClient()
	hooks.blockFirst = false
	reader, writer := io.Pipe()
	defer writer.Close()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{}, reader, &stdout, &stderr, hooks)
	}()
	if _, err := io.WriteString(writer, "first prompt\n"); err != nil {
		t.Fatal(err)
	}
	waitHookEvent(t, hooks, "Stop", 1)
	if _, err := io.WriteString(writer, "second prompt without enter"); err != nil {
		t.Fatal(err)
	}
	waitHookEvent(t, hooks, "Interrupt", 1)
	if countHookEvents(hooks.snapshot(), "UserPromptSubmit") != 1 {
		t.Fatalf("partial input was submitted before newline: %#v", hooks.snapshot())
	}
	if _, err := io.WriteString(writer, "\n"); err != nil {
		t.Fatal(err)
	}
	waitHookEvent(t, hooks, "UserPromptSubmit", 2)
	if _, err := io.WriteString(writer, ":quit\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	prompts := make([]string, 0, 2)
	for _, event := range hooks.snapshot() {
		if event.Name == "UserPromptSubmit" {
			prompts = append(prompts, event.Prompt)
		}
	}
	if len(prompts) != 2 || prompts[1] != "second prompt without enter" {
		t.Fatalf("input should remain one intact prompt: %#v", prompts)
	}
}

func TestPersonalChatEOFExitsNormally(t *testing.T) {
	for _, input := range []string{"", "prompt\n"} {
		t.Run(fmt.Sprintf("input_%q", input), func(t *testing.T) {
			runner, _ := newCLITestRunner(t, mock.NewProvider(mock.Final("done")))
			ctx := testCtx(t)
			if err := Run(ctx, runner, Options{}, strings.NewReader(input), io.Discard, io.Discard, nil); err != nil {
				t.Fatalf("normal EOF returned an error: %v", err)
			}
		})
	}
}

func TestChatDeliveryQueueIsBoundedAndInjectsOnlyOnce(t *testing.T) {
	source, err := newChatInputSource(context.Background(), strings.NewReader(""), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.SetBusy(true)
	for i := 0; i < personalprocess.MaxQueue; i++ {
		queued, err := source.Deliver(fmt.Sprintf("message-%d", i), "gofer")
		if err != nil || !queued {
			t.Fatalf("busy delivery %d should queue: queued=%v err=%v", i, queued, err)
		}
	}
	if _, err := source.Deliver("overflow", "gofer"); !errors.Is(err, personalprocess.ErrQueueFull) {
		t.Fatalf("queue overflow should be rejected, got %v", err)
	}
	for i := 0; i < personalprocess.MaxQueue; i++ {
		event := <-source.deliveries
		if !event.injected || event.line != fmt.Sprintf("[来自 gofer] message-%d", i) {
			t.Fatalf("unexpected queued delivery %#v", event)
		}
	}
	source.SetBusy(false)
	queued, err := source.Deliver("hello", "local-agent")
	if err != nil || queued {
		t.Fatalf("idle delivery should be immediate: queued=%v err=%v", queued, err)
	}
	if event := <-source.deliveries; event.line != "[来自 local-agent] hello" || !event.injected {
		t.Fatalf("unexpected idle delivery event %#v", event)
	}
}

func TestEmptyChatRegistersShortNameAndUpdatesTitleOnFirstPrompt(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	processRoot := shortTestHome(t)
	lockRoot := filepath.Join(processRoot, "locks")
	repo, err := store.Open(newTestStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	provider := &blockingChatProvider{entered: make(chan struct{}), release: make(chan struct{})}
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: provider,
		LockRoot: lockRoot, ProcessRoot: filepath.Join(processRoot, "run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{}, reader, &stdout, &stderr, nil)
	}()
	processRoot = filepath.Join(processRoot, "run")
	var record personalprocess.Record
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		records, err := personalprocess.List(processRoot)
		if err == nil && len(records) == 1 {
			record = records[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record.SessionID == "" {
		t.Fatalf("chat process did not register before its first prompt")
	}
	created, err := runner.LoadSession(context.Background(), record.SessionID)
	if err != nil || created.Title != "New chat" {
		t.Fatalf("startup should create a real default-titled session: %#v err=%v", created, err)
	}
	if _, err := io.WriteString(writer, "first meaningful prompt\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not receive first prompt")
	}
	updated, err := personalprocess.List(processRoot)
	if err != nil || len(updated) != 1 || updated[0].Name != record.Name || !strings.HasPrefix(record.Name, "chat-") {
		t.Fatalf("the process should keep its generated short name: %#v err=%v", updated, err)
	}
	stored, err := runner.LoadSession(context.Background(), record.SessionID)
	if err != nil || stored.Title != "first meaningful prompt" {
		t.Fatalf("session title was not updated: %#v err=%v", stored, err)
	}
	// The short name is stored on the session when it first registers, so a
	// resumed chat goes on under the same address.
	saved, err := runner.SessionShortName(context.Background(), record.SessionID)
	if err != nil || saved != record.Name {
		t.Fatalf("short name was not stored on the session: %q err=%v", saved, err)
	}
	close(provider.release)
	if _, err := io.WriteString(writer, ":quit\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestChatProcessDeliveryTranscriptIsInjectedAndTerminalPromptIsNot(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	sugaHome := shortTestHome(t)
	t.Setenv("SUGA_HOME", sugaHome)
	repo, err := store.Open(newTestStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	processRoot := filepath.Join(sugaHome, "run")
	provider := &hookTestProvider{}
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: provider,
		TranscriptDir: filepath.Join(sugaHome, "transcripts"),
		LockRoot:      filepath.Join(processRoot, "locks"), ProcessRoot: processRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{}, reader, &stdout, &stderr, nil)
	}()
	record := waitForProcessRecord(t, processRoot)
	if response, err := personalprocess.Send(record, personalprocess.Request{Text: "S6_QUEUE_1", From: "cli"}); err != nil || response.Queued {
		t.Fatalf("idle process delivery should be immediate: response=%#v err=%v", response, err)
	}
	waitProviderCalls(t, provider, 1)
	if _, err := io.WriteString(writer, "ordinary terminal prompt\n"); err != nil {
		t.Fatal(err)
	}
	waitProviderCalls(t, provider, 2)
	if _, err := io.WriteString(writer, ":quit\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(sugaHome, "transcripts", record.SessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["type"] == "user" {
			entries = append(entries, entry)
		}
	}
	if len(entries) != 2 || entries[0]["text"] != "[来自 cli] S6_QUEUE_1" || entries[0]["injected"] != true || entries[1]["text"] != "ordinary terminal prompt" || entries[1]["injected"] != false {
		t.Fatalf("transcript did not preserve delivery provenance: %#v", entries)
	}
}

func TestChatProcessDeliveryUserPromptSubmitCarriesInjected(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	sugaHome := shortTestHome(t)
	t.Setenv("SUGA_HOME", sugaHome)
	repo, err := store.Open(newTestStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	processRoot := filepath.Join(sugaHome, "run")
	provider := &hookTestProvider{}
	runner, err := personal.New(personal.Config{
		Repository: repo, Provider: provider,
		TranscriptDir: filepath.Join(sugaHome, "transcripts"),
		LockRoot:      filepath.Join(processRoot, "locks"), ProcessRoot: processRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	hooks := newFakeHookClient()
	hooks.blockFirst = false
	reader, writer := io.Pipe()
	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), runner, Options{}, reader, &stdout, &stderr, hooks)
	}()
	record := waitForProcessRecord(t, processRoot)
	if _, err := personalprocess.Send(record, personalprocess.Request{Text: "S6_HOOK", From: "cli"}); err != nil {
		t.Fatal(err)
	}
	waitProviderCalls(t, provider, 1)
	waitHookEvent(t, hooks, "Stop", 1)
	if _, err := io.WriteString(writer, "typed prompt\n"); err != nil {
		t.Fatal(err)
	}
	waitProviderCalls(t, provider, 2)
	if _, err := io.WriteString(writer, ":quit\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var prompts []sugahooks.Event
	for _, event := range hooks.snapshot() {
		if event.Name == "UserPromptSubmit" {
			prompts = append(prompts, event)
		}
	}
	t.Run("delivery is injected", func(t *testing.T) {
		assert.Eq(t, 2, len(prompts))
		assert.Eq(t, "[来自 cli] S6_HOOK", prompts[0].Prompt)
		assert.True(t, prompts[0].Injected)
	})
	t.Run("terminal input is not injected", func(t *testing.T) {
		assert.Eq(t, "typed prompt", prompts[1].Prompt)
		assert.False(t, prompts[1].Injected)
	})
	t.Run("json omits false and only prompt event carries true", func(t *testing.T) {
		on, _ := json.Marshal(prompts[0])
		off, _ := json.Marshal(prompts[1])
		assert.StrContains(t, string(on), `"injected":true`)
		assert.StrNotContains(t, string(off), "injected")
		for _, event := range hooks.snapshot() {
			if event.Name != "UserPromptSubmit" {
				assert.False(t, event.Injected)
			}
		}
	})
}

func waitForProcessRecord(t *testing.T, root string) personalprocess.Record {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		records, err := personalprocess.List(root)
		if err == nil && len(records) == 1 {
			return records[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("chat process did not register")
	return personalprocess.Record{}
}

func waitProviderCalls(t *testing.T, provider *hookTestProvider, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		calls := len(provider.calls)
		provider.mu.Unlock()
		if calls >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("provider did not receive call %d", count)
}

type blockingChatProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (*blockingChatProvider) Name() string { return "blocking-chat-test" }
func (p *blockingChatProvider) Generate(ctx context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	select {
	case <-p.entered:
	default:
		close(p.entered)
	}
	select {
	case <-p.release:
		return &agentcore.GenerateResult{Content: "finished", FinishReason: agentcore.FinishFinal}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// A configured but failing hook process must not prevent the chat turn.
func TestPersonalChatSurvivesFailingHookCommand(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOOKS", "") // exercise the config value, not the TestMain override
	_ = gkconfig.Set("hooks.command", []string{filepath.Join(t.TempDir(), "missing-hook-command-7cb7")})
	hooks, err := sugahooks.New()
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NotNil(t, hooks))
	runner, _ := newCLITestRunner(t, &hookTestProvider{})
	var stdout, stderr strings.Builder
	if err := Run(context.Background(), runner, Options{}, strings.NewReader("hello\n:quit\n"), &stdout, &stderr, hooks); err != nil {
		t.Fatalf("hook failure interrupted chat: %v", err)
	}
	if !strings.Contains(stdout.String(), "answer-1") {
		t.Fatalf("chat did not complete after hook errors: %q", stdout.String())
	}
}

func TestTranscriptsCanBeDisabledByConfig(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	if !appconfig.TranscriptsEnabled() {
		t.Fatal("transcripts should default to enabled")
	}
	_ = gkconfig.Set("transcripts", "off")
	if appconfig.TranscriptsEnabled() {
		t.Fatal("transcripts: off must disable transcript files")
	}
}

func TestPersonalChatSendsOnlyBoundedRedactedShellOutputToPostToolUse(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	repo, err := store.Open(newTestStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	registry := tools.NewRegistry()
	registry.Register(appHookOutputTool{})
	runner, err := personal.New(personal.Config{
		Repository: repo,
		Provider: mock.NewProvider(
			mock.ToolCall("call-1", "shell_exec", map[string]any{"command": "print environment"}),
			mock.Final("finished"),
		),
		Tools: registry,
	})
	if err != nil {
		t.Fatal(err)
	}
	hooks := newFakeHookClient()
	hooks.postTool = sugahooks.PostToolFilter{Tools: []string{"shell_*"}, Name: "shell"}
	var stdout, stderr strings.Builder
	err = Run(context.Background(), runner, Options{}, strings.NewReader("run command\n:quit\n"), &stdout, &stderr, hooks)
	if err != nil {
		t.Fatal(err)
	}
	var postTool *sugahooks.Event
	postToolIndex, stopIndex := -1, -1
	for i, event := range hooks.snapshot() {
		if event.Name == "PostToolUse" {
			copyEvent := event
			postTool = &copyEvent
			postToolIndex = i
		}
		if event.Name == "Stop" {
			stopIndex = i
		}
	}
	if postTool == nil || postTool.ToolName != "shell" || stopIndex <= postToolIndex {
		t.Fatalf("expected shell PostToolUse event, got %#v", hooks.snapshot())
	}
	if len(postTool.ToolOutput) > toolHookLimit || strings.Contains(postTool.ToolOutput, "canary-credential-4a2b") || !strings.Contains(postTool.ToolOutput, "[REDACTED]") {
		shown := postTool.ToolOutput[:min(len(postTool.ToolOutput), 80)]
		t.Fatalf("tool output was not bounded and redacted: bytes=%d output=%q", len(postTool.ToolOutput), shown)
	}
}

type appHookOutputTool struct{}

func (appHookOutputTool) Name() string        { return "shell_exec" }
func (appHookOutputTool) Description() string { return "test shell tool" }
func (appHookOutputTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: "shell_exec"}
}
func (appHookOutputTool) Kind() tools.Kind { return tools.KindNative }
func (appHookOutputTool) Execute(context.Context, agentcore.ToolCall) (*agentcore.ToolResult, error) {
	return &agentcore.ToolResult{Name: "shell_exec", Content: "Authorization: Bearer canary-credential-4a2b {\"api_key\":\"canary-credential-4a2b\"} " + strings.Repeat("x", toolHookLimit+100)}, nil
}

func waitHookEvent(t *testing.T, hooks *fakeHookClient, name string, count int) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		seen := 0
		for _, event := range hooks.snapshot() {
			if event.Name == name {
				seen++
			}
		}
		if seen >= count {
			return
		}
		select {
		case <-hooks.eventWake:
		case <-timer.C:
			t.Fatalf("timed out waiting for %s event %d", name, count)
		}
	}
}

func countHookEvents(events []sugahooks.Event, name string) int {
	count := 0
	for _, event := range events {
		if event.Name == name {
			count++
		}
	}
	return count
}

// hasUserText reports whether a user message contains text: hook context
// reaches the model as a user message of its turn (never role=system).
func hasUserText(messages []agentcore.Message, text string) bool {
	for _, message := range messages {
		if message.Role == "user" && strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}

type failingChatProvider struct{}

func (failingChatProvider) Name() string { return "failing-chat-test" }
func (failingChatProvider) Generate(context.Context, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	return nil, errors.New("upstream exploded")
}

// A failed turn must still send Stop so gofer leaves the "running" state and web replies work.
func TestPersonalChatSendsStopWhenTurnFails(t *testing.T) {
	runner, _ := newCLITestRunner(t, failingChatProvider{})
	hooks := newFakeHookClient()
	hooks.blockFirst = false
	var stderr strings.Builder
	err := Run(context.Background(), runner, Options{}, strings.NewReader("do it\n:quit\n"), io.Discard, &stderr, hooks)
	if err != nil {
		t.Fatalf("failed turn should not end chat with an error: %v", err)
	}
	var stops []sugahooks.Event
	for _, event := range hooks.snapshot() {
		if event.Name == "Stop" {
			stops = append(stops, event)
		}
	}
	if len(stops) != 1 || !strings.Contains(stops[0].LastAssistant, "upstream exploded") {
		t.Fatalf("expected one Stop carrying the error summary, got %#v", stops)
	}
	if !strings.Contains(stderr.String(), "Error:") {
		t.Fatalf("error should still be reported on stderr: %q", stderr.String())
	}
}

func TestPersonalChatKeepsWebReplyReceivedDuringInterrupt(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	t.Run("delivered as next-turn context", func(t *testing.T) {
		provider := &hookTestProvider{}
		runner, _ := newCLITestRunner(t, provider)
		hooks := newFakeHookClient()
		hooks.blockFirst = false
		hooks.lostOnInterrupt = "[gofer web 回复] late-reply-canary"
		var stdout, stderr strings.Builder
		err := Run(context.Background(), runner, Options{}, strings.NewReader("first\nsecond\n:quit\n"), &stdout, &stderr, hooks)
		assert.Require(t, assert.NoErr(t, err))
		provider.mu.Lock()
		defer provider.mu.Unlock()
		assert.Require(t, assert.Gt(t, len(provider.calls), 1))
		found := false
		for _, m := range provider.calls[len(provider.calls)-1] {
			found = found || strings.Contains(m.Content, "late-reply-canary")
		}
		assert.True(t, found)
	})
	t.Run("warns when chat ends before another turn", func(t *testing.T) {
		runner, _ := newCLITestRunner(t, &hookTestProvider{})
		hooks := newFakeHookClient()
		hooks.blockFirst = false
		hooks.lostOnInterrupt = "[gofer web 回复] late-reply-canary"
		var stdout, stderr strings.Builder
		err := Run(context.Background(), runner, Options{}, strings.NewReader("first\n:quit\n"), &stdout, &stderr, hooks)
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, stderr.String(), "not delivered")
		assert.StrContains(t, stderr.String(), "late-reply-canary")
	})
}

func TestHookEventTranscriptPathAndSource(t *testing.T) {
	run := func(t *testing.T, opts Options) []sugahooks.Event {
		runner, _ := newCLITestRunner(t, &hookTestProvider{})
		hooks := newFakeHookClient()
		hooks.blockFirst = false
		var stdout, stderr strings.Builder
		assert.Require(t, assert.NoErr(t, Run(context.Background(), runner, opts, strings.NewReader(":quit\n"), &stdout, &stderr, hooks)))
		return hooks.snapshot()
	}
	startOf := func(events []sugahooks.Event) sugahooks.Event {
		for _, e := range events {
			if e.Name == "SessionStart" {
				return e
			}
		}
		t.Fatal("no SessionStart")
		return sugahooks.Event{}
	}
	t.Run("transcripts off omits transcript_path", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		t.Setenv("SUGA_HOME", t.TempDir())
		_ = gkconfig.Set("transcripts", "off")
		start := startOf(run(t, Options{}))
		assert.Eq(t, "", start.TranscriptPath)
		data, _ := json.Marshal(start)
		assert.StrNotContains(t, string(data), "transcript_path")
	})
	t.Run("transcripts on reports path", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		t.Setenv("SUGA_HOME", t.TempDir())
		assert.NotEq(t, "", startOf(run(t, Options{})).TranscriptPath)
	})
	t.Run("existing --session-id reports resume", func(t *testing.T) {
		gkconfig.Reset()
		t.Cleanup(gkconfig.Reset)
		t.Setenv("SUGA_HOME", t.TempDir())
		runner, _ := newCLITestRunner(t, &hookTestProvider{})
		var sources []string
		for range 2 {
			hooks := newFakeHookClient()
			var stdout, stderr strings.Builder
			assert.Require(t, assert.NoErr(t, Run(context.Background(), runner, Options{SessionID: "sess-fixed"}, strings.NewReader(":quit\n"), &stdout, &stderr, hooks)))
			sources = append(sources, startOf(hooks.snapshot()).Source)
		}
		assert.Eq(t, []string{"startup", "resume"}, sources)
	})
}

func TestHookNoticeIsShownOnStderr(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	t.Setenv("SUGA_HOME", t.TempDir())
	runner, _ := newCLITestRunner(t, &hookTestProvider{})
	hooks := newFakeHookClient()
	hooks.blockFirst = false
	hooks.startNotice = "session taken over on web"
	var stdout, stderr strings.Builder
	assert.Require(t, assert.NoErr(t, Run(context.Background(), runner, Options{}, strings.NewReader(":quit\n"), &stdout, &stderr, hooks)))
	assert.StrContains(t, stderr.String(), "session taken over on web")
	assert.StrNotContains(t, stdout.String(), "session taken over on web")
}

func TestPersonalChatCompactCommandCompressesSession(t *testing.T) {
	t.Setenv("SUGA_HOME", t.TempDir())
	provider := &hookTestProvider{}
	runner, _ := newCLITestRunner(t, provider)
	var stdout, stderr strings.Builder
	input := "first " + strings.Repeat("x", 400) + "\n:compact\nsecond\n:quit\n"
	err := Run(context.Background(), runner, Options{}, strings.NewReader(input), &stdout, &stderr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Compacted context: ~") {
		t.Fatalf("missing :compact report: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	// calls: first turn, the summary request, then "second" with summary + new prompt only.
	if len(provider.calls) != 3 {
		t.Fatalf("expected 3 model calls, got %d", len(provider.calls))
	}
	last := provider.calls[2]
	if len(last) != 2 || !strings.Contains(last[0].Content, "压缩摘要") || last[1].Content != "second" {
		t.Fatalf("unexpected context after :compact: %#v", last)
	}
}
