package acpserver

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/mock"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// capturingProvider records the requests the runtime sends to the model, so a
// test asserts on the tool result the model actually sees (the note is added at
// the output layer, above the tool itself).
type capturingProvider struct {
	*mock.Provider
	mu       sync.Mutex
	requests []*api.GenerateRequest
}

func (p *capturingProvider) Generate(ctx context.Context, req *api.GenerateRequest) (*api.GenerateResult, error) {
	p.record(req)
	return p.Provider.Generate(ctx, req)
}

// Stream is the entry the runner actually calls; both entries record the request.
func (p *capturingProvider) Stream(ctx context.Context, req *api.GenerateRequest, emit api.StreamFunc) (*api.GenerateResult, error) {
	p.record(req)
	return p.Provider.Stream(ctx, req, emit)
}

func (p *capturingProvider) record(req *api.GenerateRequest) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
}

// modelToolResults collects the tool-result contents one request carried.
func modelToolResults(req *api.GenerateRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == "tool" && m.ToolResult != nil {
			out = append(out, m.ToolResult.Content)
		}
	}
	return out
}

func TestACPApprovedNote(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "t.txt")
	writeCall := func(id string) mock.Step {
		return mock.ToolCall(id, "file_write", map[string]any{"path": path, "content": "after\n"})
	}
	allResults := func(provider *capturingProvider) []string {
		var out []string
		for _, req := range provider.requests {
			out = append(out, modelToolResults(req)...)
		}
		return out
	}

	t.Run("allow once gets the note", func(t *testing.T) {
		provider := &capturingProvider{Provider: mock.NewProvider(writeCall("w1"), mock.Final("done"))}
		conn := connectPersonalACP(t, testACPRuntime(t, provider, &countingTool{writePath: path}), &testACPClient{selected: "allow"})
		ctx := testCtx(t)
		created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
		assert.NoErr(t, err)
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("edit")}})
		assert.NoErr(t, err)
		results := allResults(provider)
		assert.Require(t, assert.Len(t, results, 1))
		assert.True(t, strings.HasPrefix(results[0], tools.ApprovedResultNote))
	})

	t.Run("allow always notes the first call only", func(t *testing.T) {
		// The remembered always rule is only consulted when the runtime has a
		// policy (the nil-policy compat path of h.decide never checks it), so
		// this case runs with the same constant scope the existing tests use.
		policy := PolicyDecision(func(agentcore.ToolCall, agentcore.Tool) (string, string, string) {
			return "ask", "", "file_write:docs/**"
		})
		provider := &capturingProvider{Provider: mock.NewProvider(writeCall("w1"), mock.Final("done"), writeCall("w2"), mock.Final("done again"))}
		client := &testACPClient{selected: "allow_always"}
		conn := connectPersonalACP(t, testACPRuntimeWith(t, provider, &countingTool{writePath: path}, acpTestOptions{policy: policy}), client)
		ctx := testCtx(t)
		created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
		assert.NoErr(t, err)
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("first")}})
		assert.NoErr(t, err)
		// The remembered always rule belongs to the ACP session state, so the
		// second prompt runs the call without a permission request. The final
		// request carries the history (noted result) plus the fresh result: only
		// the fresh one must stay un-noted.
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("second")}})
		assert.NoErr(t, err)
		provider.mu.Lock()
		finalReq := provider.requests[len(provider.requests)-1]
		provider.mu.Unlock()
		// Only the first prompt asked: the second ran under the remembered rule.
		client.mu.Lock()
		assert.Eq(t, 1, len(client.requests))
		client.mu.Unlock()
		rresults := modelToolResults(finalReq)
		assert.Require(t, len(rresults) >= 2)
		assert.True(t, strings.HasPrefix(rresults[0], tools.ApprovedResultNote))
		assert.False(t, strings.Contains(rresults[len(rresults)-1], tools.ApprovedResultNote))
	})

	t.Run("rejected call keeps the plain denial", func(t *testing.T) {
		provider := &capturingProvider{Provider: mock.NewProvider(writeCall("w1"), mock.Final("done"))}
		conn := connectPersonalACP(t, testACPRuntime(t, provider, &countingTool{writePath: path}), &testACPClient{selected: "reject"})
		ctx := testCtx(t)
		created, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
		assert.NoErr(t, err)
		_, err = conn.Prompt(ctx, acp.PromptRequest{SessionId: created.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("edit")}})
		assert.NoErr(t, err)
		results := allResults(provider)
		assert.Require(t, assert.Len(t, results, 1))
		assert.True(t, strings.Contains(results[0], "permission denied"))
		assert.False(t, strings.Contains(results[0], tools.ApprovedResultNote))
	})
}
