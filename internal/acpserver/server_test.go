package acpserver

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

type testACPClient struct {
	acp.Client
	mu            sync.Mutex
	selected      acp.PermissionOptionId
	permissionErr error
	// cancelOutcome answers permission requests with the Cancelled outcome.
	cancelOutcome bool
	updates       []acp.SessionNotification
	requests      []acp.RequestPermissionRequest
}

func (c *testACPClient) RequestPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	if c.permissionErr != nil {
		return acp.RequestPermissionResponse{}, c.permissionErr
	}
	if c.cancelOutcome {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{OptionId: c.selected},
	}}, nil
}

func (c *testACPClient) SessionUpdate(_ context.Context, update acp.SessionNotification) error {
	c.mu.Lock()
	c.updates = append(c.updates, update)
	c.mu.Unlock()
	return nil
}

func connectPersonalACP(t *testing.T, open OpenRuntime, client acp.Client) *acp.ClientSideConnection {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	agent := New(open, false)
	agent.SetConnection(acp.NewAgentSideConnection(agent, agentToClientW, clientToAgentR))
	conn := acp.NewClientSideConnection(client, clientToAgentW, agentToClientR)
	t.Cleanup(func() {
		_ = clientToAgentW.Close()
		_ = clientToAgentR.Close()
		_ = agentToClientW.Close()
		_ = agentToClientR.Close()
	})
	return conn
}

func testACPRuntime(t *testing.T, provider agentcore.Provider, tool *countingTool) OpenRuntime {
	t.Helper()
	return testACPRuntimeWith(t, provider, tool, acpTestOptions{})
}

// acpTestOptions tweaks testACPRuntimeWith: deferred puts the visibility layer
// (MCP tools deferred) in front of approval; dbPath, when set, names the store.
type acpTestOptions struct {
	deferred bool
	dbPath   string
	// failRules, when set to true, makes the rule source fail so a turn dies
	// before the runner finishes it.
	failRules *atomic.Bool
	budget    personal.BudgetSettings
	// policy exercises the same runtime-to-ACP policy handoff as the CLI.
	policy PolicyDecision
	// noVision builds the runtime with a closed vision gate: a prompt that
	// brought image blocks must tell the client once (thought note).
	noVision bool
}

func testACPRuntimeWith(t *testing.T, provider agentcore.Provider, tool *countingTool, opts acpTestOptions) OpenRuntime {
	t.Helper()
	dbPath := opts.dbPath
	if dbPath == "" {
		dbPath = filepath.Join(t.TempDir(), "db", "sessions.db")
	}
	return func(_ context.Context, _ string, _ bool, _ []mcp.ServerSettings, gate TurnGate) (Runtime, error) {
		repo, err := store.Open(dbPath)
		if err != nil {
			return Runtime{}, err
		}
		registry := tools.NewRegistry()
		if tool != nil {
			registry.Register(tool)
		}
		// Same stack as the app: the gate is the hook and the approval policy.
		var hooks []agentcore.Hook
		var approver tools.Approver
		if gate != nil {
			hooks, approver = append(hooks, gate), gate
		}
		plans := personal.NewPlanRecorder()
		registry.Register(native.NewUpdatePlanTool(plans))
		cfg := personal.Config{Repository: repo, Provider: provider, Tools: tools.NewApproval(registry, approver), Hooks: hooks, Plans: plans, Budget: opts.budget,
			SessionsDir: filepath.Join(filepath.Dir(filepath.Dir(dbPath)), "sessions")}
		if opts.failRules != nil {
			cfg.RuleSource = func() (string, string, error) {
				if opts.failRules.Load() {
					return "", "", errors.New("rules unavailable")
				}
				return "", "h", nil
			}
		}
		if opts.deferred {
			// observe -> visibility -> approval -> base, as in the app.
			vis := tools.NewVisibility(tools.NewApproval(registry, approver), tools.VisibilityOptions{Mode: tools.DeferredAlways})
			cfg.Tools, cfg.Visibility = vis, vis
		}
		runner, err := personal.New(cfg)
		if err != nil {
			_ = repo.Close()
			return Runtime{}, err
		}
		// Same lookup as the app: classification reads the registered tool.
		lookup := func(name string) agentcore.Tool {
			found, err := registry.Get(name)
			if err != nil {
				return nil
			}
			return found
		}
		return Runtime{Runner: runner, Close: repo.Close, LookupTool: lookup, Policy: opts.policy,
			Vision: nilOrClosedVision(opts.noVision)}, nil
	}
}

// nilOrClosedVision mirrors the app wiring: nil means the current model can
// see images, a closed gate means it cannot.
func nilOrClosedVision(noVision bool) *tools.VisionGate {
	if noVision {
		return tools.NewVisionGate(false)
	}
	return nil
}

type countingTool struct {
	calls     atomic.Int32
	writePath string
	// name and mcp let a test pose as another tool; defaults are the native file_write.
	name string
	mcp  bool
	// agentState makes the stub declare Meta.AgentState.
	agentState bool
}

type emptyFinalACPProvider struct {
	calls atomic.Int32
}

func (p *emptyFinalACPProvider) Name() string { return "empty-final-acp" }

func (p *emptyFinalACPProvider) Generate(context.Context, *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	if p.calls.Add(1) == 1 {
		return &agentcore.GenerateResult{
			ToolCalls:    []agentcore.ToolCall{{CallID: "call-1", Name: "file_write"}},
			FinishReason: agentcore.FinishToolCalls,
		}, nil
	}
	return &agentcore.GenerateResult{FinishReason: agentcore.FinishFinal}, nil
}

type countedUsageProvider struct{ *mock.Provider }

type truncatedACPProvider struct {
	*mock.Provider
	calls      atomic.Int32
	truncateOn int32
	withUsage  bool
}

func (p *truncatedACPProvider) Stream(ctx context.Context, req *agentcore.GenerateRequest, emit agentcore.StreamFunc) (*agentcore.GenerateResult, error) {
	result, err := p.Provider.Stream(ctx, req, emit)
	call := p.calls.Add(1)
	if result != nil {
		if p.withUsage {
			result.Usage = agentcore.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10, CacheReadTokens: 2, CostUSD: 0.25}
		}
		if call == p.truncateOn {
			result.Truncated = true
			result.OutputLimit = 32
		}
	}
	return result, err
}

func (p countedUsageProvider) Stream(ctx context.Context, req *agentcore.GenerateRequest, emit agentcore.StreamFunc) (*agentcore.GenerateResult, error) {
	result, err := p.Provider.Stream(ctx, req, emit)
	if result != nil {
		result.Usage = agentcore.Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10, CacheReadTokens: 2, CostUSD: 0.25}
	}
	return result, err
}

func (t *countingTool) Name() string {
	if t.name != "" {
		return t.name
	}
	return "file_write"
}
func (*countingTool) Description() string { return "write test file" }

// ToolMeta gives the stub the metadata of the catalog tool it poses as
// (file_write by default); other names declare nothing.
func (t *countingTool) ToolMeta() tools.Meta {
	if t.agentState {
		return tools.Meta{Category: tools.CategoryOther, AgentState: true}
	}
	for _, spec := range native.Catalog() {
		if spec.Name == t.Name() {
			return tools.MetaOf(spec.New(native.Env{SerpAPIKey: "test-key"}))
		}
	}
	return tools.Meta{Category: tools.CategoryOther}
}

func (t *countingTool) Kind() tools.Kind {
	if t.mcp {
		return tools.KindMCP
	}
	return tools.KindNative
}
func (t *countingTool) Schema() agentcore.ToolSchema {
	return agentcore.ToolSchema{Name: t.Name(), InputSchema: map[string]any{"type": "object"}}
}
func (t *countingTool) Execute(_ context.Context, call agentcore.ToolCall) (*agentcore.ToolResult, error) {
	t.calls.Add(1)
	if t.writePath != "" {
		content, _ := call.Arguments["content"].(string)
		if err := os.WriteFile(t.writePath, []byte(content), 0644); err != nil {
			return nil, err
		}
	}
	return &agentcore.ToolResult{CallID: call.CallID, Name: call.Name, Content: "written"}, nil
}

func TestPersonalACPEmptyFinalAfterToolEndsTurn(t *testing.T) {
	provider := &emptyFinalACPProvider{}
	tool := &countingTool{}
	client := &testACPClient{selected: "allow"}
	response := promptOnce(t, testACPRuntime(t, provider, tool), client, "")

	assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
	assert.Eq(t, int32(1), tool.calls.Load())
	assert.Eq(t, "", agentText(client))
}

func TestPersonalACPRequiresPermissionBeforeToolExecution(t *testing.T) {
	for _, selected := range []acp.PermissionOptionId{"reject", "allow"} {
		t.Run(string(selected), func(t *testing.T) {
			tool := &countingTool{}
			provider := mock.NewProvider(mock.ToolCall("edit-1", "file_write", map[string]any{"path": "test.txt"}), mock.Final("done"))
			client := &testACPClient{selected: selected}
			conn := connectPersonalACP(t, testACPRuntime(t, provider, tool), client)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
			if err != nil {
				t.Fatal(err)
			}
			response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("edit")}})
			if err != nil {
				t.Fatal(err)
			}
			client.mu.Lock()
			requests := len(client.requests)
			started, terminal := false, false
			for _, update := range client.updates {
				started = started || update.Update.ToolCall != nil
				if status := update.Update.ToolCallUpdate; status != nil && status.Status != nil {
					terminal = terminal || *status.Status == acp.ToolCallStatusCompleted || *status.Status == acp.ToolCallStatusFailed
				}
			}
			client.mu.Unlock()
			if requests != 1 || !started || !terminal {
				t.Fatalf("permission requests = %d, tool started = %v, terminal update = %v", requests, started, terminal)
			}
			if selected == "reject" {
				// A refusal is a tool error; the model finishes the turn (end_turn).
				if response.StopReason != acp.StopReasonEndTurn || tool.calls.Load() != 0 {
					t.Fatalf("rejected tool: response=%s executions=%d", response.StopReason, tool.calls.Load())
				}
			} else if response.StopReason != acp.StopReasonEndTurn || tool.calls.Load() != 1 {
				t.Fatalf("allowed tool: response=%s executions=%d", response.StopReason, tool.calls.Load())
			}
		})
	}
}

func TestPersonalACPEmitsUsagePlanAndEditDetails(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "test.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &countingTool{writePath: path}
	provider := countedUsageProvider{mock.NewProvider(
		mock.ToolCall("edit-1", "file_write", map[string]any{"path": "test.txt", "content": "after\n", "line": 2}),
		mock.Final("- [x] Inspect file\n- [ ] Verify edit"),
	)}
	client := &testACPClient{selected: "allow"}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, tool), client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("edit")}})
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("prompt = %+v, %v", response, err)
	}
	client.mu.Lock()
	updates := append([]acp.SessionNotification(nil), client.updates...)
	client.mu.Unlock()
	var usage *acp.SessionUsageUpdate
	var started *acp.SessionUpdateToolCall
	var completed *acp.SessionToolCallUpdate
	for _, notification := range updates {
		update := notification.Update
		if update.UsageUpdate != nil {
			usage = update.UsageUpdate
		}
		// A task list in the final text must not produce a plan update.
		assert.Eq(t, true, update.Plan == nil)
		if update.ToolCall != nil {
			started = update.ToolCall
		}
		if update.ToolCallUpdate != nil && update.ToolCallUpdate.Status != nil && *update.ToolCallUpdate.Status == acp.ToolCallStatusCompleted {
			completed = update.ToolCallUpdate
		}
	}
	if usage == nil || started == nil || completed == nil {
		t.Fatalf("missing S4 updates: usage=%v start=%v complete=%v", usage, started, completed)
	}
	assert.Eq(t, acp.ToolKindEdit, started.Kind)
	assert.Eq(t, path, started.Locations[0].Path)
	assert.Eq(t, 2, *started.Locations[0].Line)
	assert.Eq(t, "before\n", *started.Content[0].Diff.OldText)
	assert.Eq(t, "after\n", started.Content[0].Diff.NewText)
	// Later updates are status-only: the diff was already delivered by tool_call.
	assert.Empty(t, completed.Locations)
	assert.Empty(t, completed.Content)
	// Provider input (14) includes the 4 cache-read tokens; reported input excludes them.
	assert.Eq(t, float64(10), usage.Meta["input_tokens"])
	assert.Eq(t, float64(6), usage.Meta["output_tokens"])
	assert.Eq(t, float64(20), usage.Meta["total_tokens"])
	assert.Eq(t, float64(4), usage.Meta["cache_read_tokens"])
	assert.Eq(t, float64(0.5), usage.Meta["cost_usd"])
	assert.Eq(t, float64(0.5), usage.Cost.Amount)
	// used is the last request's context (one step), not the two-step sum.
	assert.Eq(t, 7, usage.Used)
	assert.Gt(t, usage.Size, 0)
}

// TestPersonalACPPlanComesFromUpdatePlanTool: the plan update is driven by the
// update_plan tool call (also in read_only mode), not parsed from answer text.
func TestPersonalACPPlanComesFromUpdatePlanTool(t *testing.T) {
	for _, mode := range []acp.SessionModeId{"default", "read_only"} {
		t.Run(string(mode), func(t *testing.T) {
			provider := mock.NewProvider(
				mock.ToolCall("plan-1", "update_plan", map[string]any{"explanation": "start", "plan": []any{
					map[string]any{"step": "Inspect", "status": "completed"},
					map[string]any{"step": "Edit", "status": "in_progress"},
					map[string]any{"step": "Verify", "status": "pending"},
				}}),
				mock.Final("done"),
			)
			client := &testACPClient{selected: "reject"}
			conn := connectPersonalACP(t, testACPRuntime(t, provider, nil), client)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
			assert.Require(t, assert.NoErr(t, err))
			_, err = conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: created.SessionId, ModeId: mode})
			assert.Require(t, assert.NoErr(t, err))
			response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("plan it")}})
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
			client.mu.Lock()
			defer client.mu.Unlock()
			assert.Eq(t, 0, len(client.requests)) // no approval needed
			var plans []*acp.SessionUpdatePlan
			for _, n := range client.updates {
				if n.Update.Plan != nil {
					plans = append(plans, n.Update.Plan)
				}
			}
			assert.Require(t, assert.Eq(t, 1, len(plans)))
			assert.Eq(t, 3, len(plans[0].Entries))
			assert.Eq(t, "Inspect", plans[0].Entries[0].Content)
			assert.Eq(t, acp.PlanEntryStatusCompleted, plans[0].Entries[0].Status)
			assert.Eq(t, acp.PlanEntryStatusInProgress, plans[0].Entries[1].Status)
			assert.Eq(t, acp.PlanEntryStatusPending, plans[0].Entries[2].Status)
		})
	}
}

func TestToolDiffRejectsWorkspaceEscapeSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside old contents\n"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "linked.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}

	locations := toolLocations(workspace, map[string]any{"path": "linked.txt"})
	diff := toolDiff(workspace, acp.ToolKindEdit, locations, map[string]any{"content": "replacement\n"})
	if len(diff) != 0 {
		t.Fatalf("workspace escape symlink produced diff: %+v", diff)
	}
}

func TestToolDiffKeepsNewFileDiff(t *testing.T) {
	workspace := t.TempDir()
	locations := toolLocations(workspace, map[string]any{"path": "new.txt"})
	diff := toolDiff(workspace, acp.ToolKindEdit, locations, map[string]any{"content": "new contents\n"})
	if len(diff) != 1 || diff[0].Diff == nil {
		t.Fatalf("new file diff = %+v", diff)
	}
	if diff[0].Diff.OldText != nil {
		t.Fatalf("new file old text = %q, want nil", *diff[0].Diff.OldText)
	}
	assert.Eq(t, "new contents\n", diff[0].Diff.NewText)
}

func TestToolDiffSupportsSymlinkedWorkspaceRoot(t *testing.T) {
	realWorkspace := t.TempDir()
	path := filepath.Join(realWorkspace, "existing.txt")
	if err := os.WriteFile(path, []byte("existing contents\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workspaceLink := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(realWorkspace, workspaceLink); err != nil {
		t.Skipf("cannot create workspace symlink on this platform: %v", err)
	}

	locations := toolLocations(workspaceLink, map[string]any{"path": "existing.txt"})
	diff := toolDiff(workspaceLink, acp.ToolKindEdit, locations, map[string]any{"content": "replacement\n"})
	if len(diff) != 1 || diff[0].Diff == nil {
		t.Fatalf("symlinked workspace diff = %+v", diff)
	}
	if diff[0].Diff.OldText == nil {
		t.Fatal("symlinked workspace old text = nil, want existing contents")
	}
	assert.Eq(t, "existing contents\n", *diff[0].Diff.OldText)
}

func TestPersonalACPReadOnlyRejectsToolWithoutPermissionRequest(t *testing.T) {
	tool := &countingTool{}
	client := &testACPClient{selected: "allow"}
	conn := connectPersonalACP(t, testACPRuntime(t, mock.NewProvider(mock.ToolCall("edit-1", "file_write", nil), mock.Final("done")), tool), client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: created.SessionId, ModeId: "read_only"}); err != nil {
		t.Fatal(err)
	}
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("edit")}})
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	requests := len(client.requests)
	client.mu.Unlock()
	// read_only refusal counts as one denial; the turn continues to end_turn.
	if response.StopReason != acp.StopReasonEndTurn || tool.calls.Load() != 0 || requests != 0 {
		t.Fatalf("read_only: response=%s executions=%d permission requests=%d", response.StopReason, tool.calls.Load(), requests)
	}
}

func TestPersonalACPPermissionFailureDoesNotExecuteTool(t *testing.T) {
	tool := &countingTool{}
	client := &testACPClient{permissionErr: context.DeadlineExceeded}
	conn := connectPersonalACP(t, testACPRuntime(t, mock.NewProvider(mock.ToolCall("edit-1", "file_write", nil), mock.Final("done")), tool), client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("edit")}})
	// A failed permission request is a denial: tool not run, turn ends normally.
	if err != nil || response.StopReason != acp.StopReasonEndTurn || tool.calls.Load() != 0 {
		t.Fatalf("permission failure: response=%+v error=%v executions=%d", response, err, tool.calls.Load())
	}
}

type recallACPProvider struct {
	mu    sync.Mutex
	calls int
}

func (*recallACPProvider) Name() string { return "recall" }
func (p *recallACPProvider) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls > 1 {
		found := false
		for _, message := range req.Messages {
			found = found || message.Content == "amber"
		}
		if !found {
			return nil, errors.New("prior answer missing from resumed ACP context")
		}
	}
	return &agentcore.GenerateResult{Content: "amber", FinishReason: agentcore.FinishFinal}, nil
}

func TestPersonalACPTwoTurnsAndNewConnectionLoad(t *testing.T) {
	provider := &recallACPProvider{}
	open := testACPRuntime(t, provider, nil)
	client := &testACPClient{}
	conn := connectPersonalACP(t, open, client)
	// One budget for six steps, three of them full turns that each open a
	// runtime and write the store: a guard against a hang, roomy enough for a
	// loaded Windows host (suag-5o5g).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	initialized, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
	if err != nil || !initialized.AgentCapabilities.LoadSession {
		t.Fatalf("initialize: %+v, %v", initialized, err)
	}
	cwd := t.TempDir()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"remember amber", "what was the color?"} {
		response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(prompt)}})
		if err != nil || response.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("prompt %q: %+v, %v", prompt, response, err)
		}
	}
	newClient := &testACPClient{}
	newConn := connectPersonalACP(t, open, newClient)
	if _, err := newConn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: cwd, McpServers: []acp.McpServer{}}); err != nil {
		t.Fatal(err)
	}
	response, err := newConn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("again?")}})
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("loaded prompt: %+v, %v", response, err)
	}
	newClient.mu.Lock()
	gotText := false
	for _, update := range newClient.updates {
		if update.Update.AgentMessageChunk != nil && update.Update.AgentMessageChunk.Content.Text != nil {
			gotText = gotText || strings.Contains(update.Update.AgentMessageChunk.Content.Text.Text, "amber")
		}
	}
	newClient.mu.Unlock()
	if !gotText {
		t.Fatal("loaded prompt did not emit answer update")
	}
}

type blockingACPProvider struct{ started chan struct{} }

func (*blockingACPProvider) Name() string { return "blocking" }
func (p *blockingACPProvider) Generate(ctx context.Context, _ *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPersonalACPCancelEndsActivePrompt(t *testing.T) {
	provider := &blockingACPProvider{started: make(chan struct{})}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, nil), &testACPClient{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan acp.PromptResponse, 1)
	errs := make(chan error, 1)
	go func() {
		response, runErr := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("wait")}})
		result <- response
		errs <- runErr
	}()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := conn.Cancel(ctx, acp.CancelNotification{SessionId: created.SessionId}); err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if response := <-result; response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop reason = %s", response.StopReason)
	}
}

func TestACPMCPSettingsAcceptStdioAndRejectUnsupportedTransport(t *testing.T) {
	settings, err := mcpSettings([]acp.McpServer{{Stdio: &acp.McpServerStdio{
		Name: "local", Command: "mcp-server", Args: []string{"--stdio"},
		Env: []acp.EnvVariable{{Name: "MODE", Value: "test"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 1 || settings[0].ID != "local" || settings[0].Command != "mcp-server" || settings[0].Env["MODE"] != "test" {
		t.Fatalf("stdio mapping = %+v", settings)
	}
	if _, err := mcpSettings([]acp.McpServer{{Http: &acp.McpServerHttpInline{}}}); err == nil {
		t.Fatal("unsupported HTTP MCP transport was accepted")
	}
}

// TestPersonalACPMCPToolNamedLikeReadToolStillAsks verifies an MCP tool that
// reuses a read-only built-in name does not skip the permission request.
func TestPersonalACPMCPToolNamedLikeReadToolStillAsks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mcp      bool
		requests int
	}{
		{name: "web_search", mcp: true, requests: 1},
		{name: "web_search", mcp: false, requests: 0},
	} {
		t.Run(tc.name+map[bool]string{true: "/mcp", false: "/native"}[tc.mcp], func(t *testing.T) {
			tool := &countingTool{name: tc.name, mcp: tc.mcp}
			provider := mock.NewProvider(mock.ToolCall("call-1", tc.name, map[string]any{}), mock.Final("done"))
			client := &testACPClient{selected: "allow"}
			conn := connectPersonalACP(t, testACPRuntime(t, provider, tool), client)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
			assert.NoErr(t, err)
			_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("go")}})
			assert.NoErr(t, err)
			client.mu.Lock()
			defer client.mu.Unlock()
			assert.Eq(t, tc.requests, len(client.requests))
			assert.Eq(t, int32(1), tool.calls.Load())
		})
	}
}

func TestPersonalACPBudgetWrapUpUsesEndTurnAndMetadata(t *testing.T) {
	provider := countedUsageProvider{mock.NewProvider(mock.Final("first"), mock.Final("wrap-up"))}
	client := &testACPClient{}
	open := testACPRuntimeWith(t, provider, nil, acpTestOptions{budget: personal.BudgetSettings{Tokens: 5, Scope: "turn"}})
	conn := connectPersonalACP(t, open, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("budget")}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
	updates := usageUpdates(client)
	assert.Gt(t, len(updates), 1)
	meta := updates[len(updates)-1].Meta
	assert.Eq(t, "turn", meta["budget_scope"])
	assert.Eq(t, true, meta["budget_exceeded"])
	assert.Eq(t, "max_tokens", meta["budget_reason"])
	assert.False(t, meta["estimated"] == true)
	limits := meta["budget_limit"].(map[string]any)
	used := meta["budget_used"].(map[string]any)
	assert.Eq(t, float64(5), limits["tokens"])
	assert.Gt(t, used["tokens"].(float64), float64(4))
}

func TestPersonalACPProviderTruncationKeepsEndTurn(t *testing.T) {
	provider := &truncatedACPProvider{Provider: mock.NewProvider(mock.Final("cut")), truncateOn: 1}
	client := &testACPClient{}
	conn := connectPersonalACP(t, testACPRuntime(t, provider, nil), client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("truncate")}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
}

func TestPersonalACPBudgetWrapUpAndTruncationKeepEndTurn(t *testing.T) {
	provider := &truncatedACPProvider{Provider: mock.NewProvider(mock.Final("first"), mock.Final("cut wrap-up")), truncateOn: 2, withUsage: true}
	client := &testACPClient{}
	open := testACPRuntimeWith(t, provider, nil, acpTestOptions{budget: personal.BudgetSettings{Tokens: 5, Scope: "turn"}})
	conn := connectPersonalACP(t, open, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	response, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("truncate wrap-up")}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
}

func TestPersonalACPAlwaysDecisionsAreSessionScoped(t *testing.T) {
	policy := PolicyDecision(func(agentcore.ToolCall, agentcore.Tool) (string, string, string) {
		return "ask", "", "file_write:docs/**"
	})
	for _, tc := range []struct {
		name       string
		selected   acp.PermissionOptionId
		wantCalls  int32
		wantPrompt int
	}{
		{name: "allow_always", selected: "allow_always", wantCalls: 2, wantPrompt: 1},
		{name: "reject_always", selected: "reject_always", wantCalls: 0, wantPrompt: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &countingTool{}
			provider := mock.NewProvider(
				mock.ToolCall("call-1", "file_write", map[string]any{"path": "docs/a.txt"}),
				mock.ToolCall("call-2", "file_write", map[string]any{"path": "docs/b.txt"}),
				mock.Final("done"),
			)
			client := &testACPClient{selected: tc.selected}
			response := promptOnce(t, testACPRuntimeWith(t, provider, tool, acpTestOptions{policy: policy}), client, "")
			assert.Eq(t, acp.StopReasonEndTurn, response.StopReason)
			assert.Eq(t, tc.wantCalls, tool.calls.Load())
			client.mu.Lock()
			assert.Eq(t, tc.wantPrompt, len(client.requests))
			client.mu.Unlock()
		})
	}
}

func TestPersonalACPLoadSessionKeepsAlwaysOnlyInProcess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db", "sessions.db")
	policy := PolicyDecision(func(agentcore.ToolCall, agentcore.Tool) (string, string, string) {
		return "ask", "", "file_write:docs/**"
	})
	provider := mock.NewProvider(
		mock.ToolCall("call-1", "file_write", map[string]any{"path": "docs/a.txt"}), mock.Final("first"),
		mock.ToolCall("call-2", "file_write", map[string]any{"path": "docs/b.txt"}), mock.Final("second"),
		mock.ToolCall("call-3", "file_write", map[string]any{"path": "docs/c.txt"}), mock.Final("third"),
	)
	open := testACPRuntimeWith(t, provider, &countingTool{}, acpTestOptions{dbPath: dbPath, policy: policy})
	client := &testACPClient{selected: "allow_always"}
	conn := connectPersonalACP(t, open, client)
	cwd := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.Require(t, assert.NoErr(t, err))
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("first")}})
	assert.NoErr(t, err)
	_, err = conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("second")}})
	assert.NoErr(t, err)
	client.mu.Lock()
	assert.Eq(t, 1, len(client.requests))
	client.mu.Unlock()

	// A fresh Agent models a new ACP process: the database session survives,
	// but its in-memory always rule must not.
	newClient := &testACPClient{selected: "allow"}
	newConn := connectPersonalACP(t, open, newClient)
	_, err = newConn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: created.SessionId, Cwd: cwd, McpServers: []acp.McpServer{}})
	assert.NoErr(t, err)
	_, err = newConn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("third")}})
	assert.NoErr(t, err)
	newClient.mu.Lock()
	assert.Eq(t, 1, len(newClient.requests))
	newClient.mu.Unlock()
}
