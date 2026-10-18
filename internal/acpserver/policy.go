package acpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/inhere/suga/internal/diffview"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/mcp"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

var ErrPermissionDenied = errors.New("ACP tool permission denied")
var ErrReadOnly = errors.New("tool is unavailable in read_only mode")

// PolicyDecision is the wiring-neutral result of the shared approval policy.
// action is one of allow, ask, or deny; rule is the representative rule
// summary, and scope is the stable key used by ACP session rules.
type PolicyDecision func(call agentcore.ToolCall, tool agentcore.Tool) (action, rule, scope string)

type sessionRule struct {
	action  string
	summary string
}

// sessionPolicy holds ACP's in-process always decisions. It deliberately has
// no persistence representation: a new ACP process starts with an empty set.
type sessionPolicy struct {
	mu    sync.Mutex
	next  int
	rules map[string]sessionRule
}

func newSessionPolicy() *sessionPolicy {
	return &sessionPolicy{rules: make(map[string]sessionRule)}
}

func (p *sessionPolicy) decide(scope string) (sessionRule, bool) {
	if p == nil || scope == "" {
		return sessionRule{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rule, ok := p.rules[scope]
	return rule, ok
}

func (p *sessionPolicy) add(scope, action string) sessionRule {
	if p == nil || scope == "" {
		return sessionRule{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rules == nil {
		p.rules = make(map[string]sessionRule)
	}
	p.next++
	rule := sessionRule{action: action, summary: fmt.Sprintf("session#%d %s action=%s", p.next, scope, action)}
	p.rules[scope] = rule
	return rule
}

// strength is shared by base policy and session rules: a deny can never be
// weakened by an allow_always decision, and an allow cannot weaken a deny.
func strength(action string) int {
	switch action {
	case "allow":
		return 1
	case "ask":
		return 2
	case "deny":
		return 3
	default:
		return 0
	}
}

// deniedError refuses a tool call: it matches tools.ErrDenied (so the approval
// layer returns it to the model as a tool error and observe records a denial)
// and keeps the ACP cause (ErrPermissionDenied / ErrReadOnly) reachable.
type deniedError struct{ cause error }

func (e *deniedError) Error() string   { return e.cause.Error() }
func (e *deniedError) Unwrap() []error { return []error{tools.ErrDenied, e.cause} }

// callState tracks one tool call between BeforeTool, Decide and AfterTool.
type callState int

const (
	callPending    callState = iota // tool_call sent, not yet decided
	callInProgress                  // approved, in_progress sent
	callDenied                      // refused, failed sent
)

// pendingCall is what BeforeTool computed for the notification; Decide reuses
// it for the permission request so the diff is read once.
type pendingCall struct {
	state     callState
	kind      acp.ToolKind
	readTool  bool
	locations []acp.ToolCallLocation
	content   []acp.ToolCallContent
}

// permissionHook is the per-prompt ACP turn gate. As an agentcore.Hook it sends
// the tool_call notifications (pending, in_progress, completed/failed); as a
// tools.Approver (Decide) it is the approval layer's policy: read_only
// refusals and RequestPermission for MCP/write tools.
type permissionHook struct {
	agentcore.NopHook
	conn      *acp.AgentSideConnection
	sessionID acp.SessionId
	cwd       string
	readOnly  bool
	sequence  uint64
	// lookup resolves a tool name in this runtime (set after it opens); the
	// announced kind and the read-only fast path come from the tool's
	// tools.Meta, so an MCP tool called "web_search" cannot pass for the
	// built-in read tool.
	lookup func(name string) agentcore.Tool
	// policy is supplied by wiring for real ACP sessions. A nil policy keeps
	// package-level compatibility tests on the historical read/ask behavior.
	policy  PolicyDecision
	session *sessionPolicy

	mu    sync.Mutex
	calls map[acp.ToolCallId]*pendingCall
}

var _ tools.Approver = (*permissionHook)(nil)

// maxDiffBytes caps each side of a diff; larger files only report locations.
// The change itself is built by internal/diffview, which chat shows too.
const maxDiffBytes = diffview.MaxBytes

// BeforeTool announces the call (status pending, with locations and diff). The
// permission decision happens later in Decide, inside the approval layer.
func (h *permissionHook) BeforeTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Call == nil || h.conn == nil {
		return ErrPermissionDenied
	}
	call := event.Call
	if call.CallID == "" {
		h.sequence++
		call.CallID = fmt.Sprintf("%s-%d", event.RunID, h.sequence)
	}
	id := acp.ToolCallId(call.CallID)
	kind, readTool := toolKind(h.tool(call.Name))
	locations := toolLocations(h.cwd, call.Arguments)
	content := toolDiff(h.cwd, kind, locations, call.Arguments)
	h.mu.Lock()
	if h.calls == nil {
		h.calls = map[acp.ToolCallId]*pendingCall{}
	}
	h.calls[id] = &pendingCall{kind: kind, readTool: readTool, locations: locations, content: content}
	h.mu.Unlock()
	// The diff is sent in the initial tool_call and once more in the permission
	// request (so the approver sees it); later status updates carry status only.
	if err := h.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: h.sessionID,
		Update:    acp.StartToolCall(id, callTitle(*call), acp.WithStartKind(kind), acp.WithStartStatus(acp.ToolCallStatusPending), acp.WithStartLocations(locations), acp.WithStartContent(content)),
	}); err != nil {
		return fmt.Errorf("send tool update: %w", err)
	}
	return nil
}

// Decide is the ACP approval policy. A refusal (read_only, reject, failed
// request) returns a deniedError: the model gets a tool error and the turn
// continues. A cancelled request (or cancelled turn) returns the ctx error,
// which aborts the turn.
func (h *permissionHook) Decide(ctx context.Context, call agentcore.ToolCall, tool agentcore.Tool) error {
	if h.conn == nil {
		return &deniedError{cause: ErrPermissionDenied}
	}
	id := acp.ToolCallId(call.CallID)
	h.mu.Lock()
	pending := h.calls[id]
	h.mu.Unlock()
	if pending == nil {
		// Not announced by BeforeTool (should not happen): classify here.
		kind, readTool := toolKind(h.tool(call.Name))
		pending = &pendingCall{kind: kind, readTool: readTool, locations: toolLocations(h.cwd, call.Arguments)}
		pending.content = toolDiff(h.cwd, kind, pending.locations, call.Arguments)
		h.mu.Lock()
		if h.calls == nil {
			h.calls = map[acp.ToolCallId]*pendingCall{}
		}
		h.calls[id] = pending
		h.mu.Unlock()
	}
	kind, readTool := pending.kind, pending.readTool
	if tools.IsMCP(tool) {
		kind, readTool = acp.ToolKindOther, false
	} else if tools.MetaOf(tool).AgentState {
		// Agent-state tools pass even in read_only mode: the tool decides
		// itself whether the write is allowed. Checked before the read-only
		// refusal below; MCP tools never take this path.
		readTool = true
	}
	action, rule, scope, sessionHit := h.decide(call, tool, readTool)
	if rule != "" {
		if sessionHit {
			tools.ReportRule(ctx, rule, scope)
		} else {
			tools.ReportRule(ctx, rule, "")
		}
	}
	if action == "deny" {
		return h.deny(ctx, id, pending, ErrPermissionDenied)
	}
	if h.readOnly && !readTool {
		return h.deny(ctx, id, pending, ErrReadOnly)
	}
	if action == "allow" {
		if sessionHit {
			tools.ReportApproval(ctx, tools.ApprovedAlways, 0)
		}
		return h.approve(ctx, pending, id)
	}
	if action != "ask" {
		return h.deny(ctx, id, pending, ErrPermissionDenied)
	}

	asked := time.Now()
	decision, err := h.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: h.sessionID,
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: id,
			Title:      acp.Ptr(callTitle(call)),
			Kind:       acp.Ptr(kind),
			Locations:  pending.locations,
			Content:    pending.content,
			Status:     acp.Ptr(acp.ToolCallStatusPending),
		},
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once", OptionId: "allow"},
			{Kind: acp.PermissionOptionKindAllowAlways, Name: "Allow always", OptionId: "allow_always"},
			{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject"},
			{Kind: acp.PermissionOptionKindRejectAlways, Name: "Reject always", OptionId: "reject_always"},
		},
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr // the turn was cancelled while asking
		}
		return h.deny(ctx, id, pending, fmt.Errorf("%w: %v", ErrPermissionDenied, err))
	}
	if decision.Outcome.Cancelled != nil {
		return context.Canceled
	}
	selected := ""
	if decision.Outcome.Selected != nil {
		selected = string(decision.Outcome.Selected.OptionId)
	}
	switch selected {
	case "allow":
		tools.ReportApproval(ctx, tools.ApprovedByUser, time.Since(asked))
	case "allow_always":
		sessionRule := h.session.add(scope, "allow")
		if sessionRule.summary != "" {
			tools.ReportRule(ctx, sessionRule.summary, scope)
		}
		tools.ReportApproval(ctx, tools.ApprovedAlways, time.Since(asked))
	case "reject_always":
		sessionRule := h.session.add(scope, "deny")
		if sessionRule.summary != "" {
			tools.ReportRule(ctx, sessionRule.summary, scope)
		}
		tools.ReportApproval(ctx, tools.AskedAndDenied, time.Since(asked))
		return h.deny(ctx, id, pending, ErrPermissionDenied)
	default:
		tools.ReportApproval(ctx, tools.AskedAndDenied, time.Since(asked))
		return h.deny(ctx, id, pending, ErrPermissionDenied)
	}
	return h.approve(ctx, pending, id)
}

func (h *permissionHook) decide(call agentcore.ToolCall, tool agentcore.Tool, readTool bool) (action, rule, scope string, sessionHit bool) {
	if h.policy == nil {
		if readTool {
			return "allow", "", call.Name, false
		}
		return "ask", "", call.Name, false
	}
	action, rule, scope = h.policy(call, tool)
	if scope == "" && h.policy == nil {
		scope = call.Name
	}
	if strength(action) == 0 {
		return "deny", rule, scope, false
	}
	if sessionRule, ok := h.session.decide(scope); ok && (rule == "" || strength(sessionRule.action) >= strength(action)) {
		return sessionRule.action, sessionRule.summary, scope, true
	}
	return action, rule, scope, false
}

func (h *permissionHook) approve(ctx context.Context, pending *pendingCall, id acp.ToolCallId) error {
	h.mu.Lock()
	pending.state = callInProgress
	h.mu.Unlock()
	return h.toolStatus(ctx, id, acp.ToolCallStatusInProgress)
}

// deny marks the call refused, reports status failed and returns the refusal.
func (h *permissionHook) deny(ctx context.Context, id acp.ToolCallId, pending *pendingCall, cause error) error {
	h.mu.Lock()
	pending.state = callDenied
	h.mu.Unlock()
	_ = h.toolStatus(ctx, id, acp.ToolCallStatusFailed)
	return &deniedError{cause: cause}
}

// AfterTool closes the call: completed when it ran, nothing more when it was
// refused (failed was already sent), failed when it never reached approval
// (unknown tool, or an unloaded deferred tool answered by visibility).
func (h *permissionHook) AfterTool(ctx context.Context, event *agentcore.ToolEvent, _ *agentcore.Control) error {
	if event == nil || event.Call == nil || h.conn == nil {
		return nil
	}
	id := acp.ToolCallId(event.Call.CallID)
	h.mu.Lock()
	pending := h.calls[id]
	delete(h.calls, id)
	h.mu.Unlock()
	switch {
	case pending == nil || pending.state == callInProgress:
		return h.toolStatus(ctx, id, acp.ToolCallStatusCompleted)
	case pending.state == callDenied:
		return nil
	default:
		return h.toolStatus(ctx, id, acp.ToolCallStatusFailed)
	}
}

// toolStatus sends a status-only tool_call_update; all other fields are optional
// in ACP and were already delivered by the initial tool_call.
func (h *permissionHook) toolStatus(ctx context.Context, id acp.ToolCallId, status acp.ToolCallStatus) error {
	return h.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: h.sessionID,
		Update:    acp.UpdateToolCall(id, acp.WithUpdateStatus(status)),
	})
}

// toolLocations resolves the path argument exactly like the native file tools
// (native.ResolveSandboxedPath) and keeps a line hint only when one is given.
func toolLocations(cwd string, args map[string]any) []acp.ToolCallLocation {
	if args == nil {
		return nil
	}
	path, _ := args["path"].(string)
	if path == "" {
		path, _ = args["file_path"].(string)
	}
	if path == "" {
		path, _ = args["dir"].(string)
	}
	if path == "" {
		// file_read with paths: point at every file inside the workspace.
		list, err := native.ReadPathList(map[string]any{"paths": args["paths"]})
		if err != nil {
			return nil
		}
		var locations []acp.ToolCallLocation
		for _, p := range list {
			if resolved, err := native.ResolveSandboxedPath(cwd, p); err == nil {
				locations = append(locations, acp.ToolCallLocation{Path: resolved})
			}
		}
		return locations
	}
	resolved, err := native.ResolveSandboxedPath(cwd, path)
	if err != nil {
		return nil // the tool will reject this path; nothing to point at
	}
	location := acp.ToolCallLocation{Path: resolved}
	for _, key := range []string{"line", "line_number"} {
		if line, ok := args[key].(int); ok && line > 0 {
			location.Line = acp.Ptr(line)
			break
		}
		if line, ok := args[key].(float64); ok && line > 0 {
			location.Line = acp.Ptr(int(line))
			break
		}
	}
	return []acp.ToolCallLocation{location}
}

// toolDiff records the proposed replacement for file_write (content) or
// file_edit (old file with the replacement applied) without reading outside
// the workspace. The change is built by internal/diffview, the same
// construction chat previews with, so both show the same old and new text.
// It never blocks: only regular files within maxDiffBytes are read, and binary
// or oversized content yields no diff (locations are still reported).
func toolDiff(cwd string, kind acp.ToolKind, locations []acp.ToolCallLocation, args map[string]any) []acp.ToolCallContent {
	if kind != acp.ToolKindEdit || len(locations) == 0 || args == nil {
		return nil
	}
	// A file call announced as an edit is file_write (its content argument) or
	// file_edit (old_string/new_string or edits): the same distinction the tool
	// makes by the arguments it carries.
	name := diffview.ToolWrite
	if _, isEdit := args["old_string"]; isEdit {
		name = diffview.ToolEdit
	} else if _, multi := args["edits"]; multi {
		name = diffview.ToolEdit
	}
	change, ok := diffview.At(cwd, name, locations[0].Path, args)
	if !ok || change.New == nil {
		return nil
	}
	return []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{
		Type: "diff", Path: change.Path, OldText: change.Old, NewText: *change.New,
	}}}
}

// tool resolves name in this runtime; nil when unknown or without lookup.
func (h *permissionHook) tool(name string) agentcore.Tool {
	if h.lookup == nil {
		return nil
	}
	return h.lookup(name)
}

// acpKinds maps the neutral tool categories onto ACP tool kinds.
var acpKinds = map[tools.Category]acp.ToolKind{
	tools.CategoryRead:    acp.ToolKindRead,
	tools.CategoryEdit:    acp.ToolKindEdit,
	tools.CategoryDelete:  acp.ToolKindDelete,
	tools.CategoryExecute: acp.ToolKindExecute,
	tools.CategorySearch:  acp.ToolKindSearch,
	tools.CategoryFetch:   acp.ToolKindFetch,
	tools.CategoryThink:   acp.ToolKindThink,
}

// toolKind returns the ACP kind of tool and whether it is read-only (runs
// without a permission request, also in read_only mode). Unknown tools, MCP
// tools and tools that declare nothing are Other and require permission.
// callTitle is the title of a tool call in the client: the tool name, or for
// the agent tool its task description ("agent: find callers of Foo").
func callTitle(call agentcore.ToolCall) string {
	if call.Name == "agent" {
		if desc, _ := call.Arguments["description"].(string); strings.TrimSpace(desc) != "" {
			return "agent: " + strings.TrimSpace(desc)
		}
	}
	return call.Name
}

func toolKind(tool agentcore.Tool) (acp.ToolKind, bool) {
	meta := tools.MetaOf(tool)
	kind, ok := acpKinds[meta.Category]
	if !ok {
		kind = acp.ToolKindOther
	}
	return kind, meta.ReadOnly
}

// mcpSettings normalizes the MCP servers of one session into the settings the
// runtime starts. Both the stdio and the HTTP shape of the protocol are
// accepted; a server must declare exactly one of them with a unique name.
// Headers given by the ACP client are passed through as they are: suga expands
// ${VAR} only for its own configuration file, so a remote client cannot read
// the host environment through this path.
func mcpSettings(servers []acp.McpServer) ([]mcp.ServerSettings, error) {
	settings := make([]mcp.ServerSettings, 0, len(servers))
	seen := make(map[string]bool)
	for _, server := range servers {
		switch {
		case server.Stdio != nil && server.Http != nil:
			return nil, acp.NewInvalidParams(map[string]any{"mcpServers": "server needs exactly one of stdio or http"})
		case server.Stdio != nil:
			setting, err := stdioMCPSetting(server.Stdio, seen)
			if err != nil {
				return nil, err
			}
			settings = append(settings, setting)
		case server.Http != nil:
			setting, err := httpMCPSetting(server.Http, seen)
			if err != nil {
				return nil, err
			}
			settings = append(settings, setting)
		default:
			return nil, acp.NewInvalidParams(map[string]any{"mcpServers": "server needs a stdio or http transport"})
		}
	}
	return settings, nil
}

// stdioMCPSetting maps the protocol's stdio server onto ServerSettings.
func stdioMCPSetting(stdio *acp.McpServerStdio, seen map[string]bool) (mcp.ServerSettings, error) {
	name, command := strings.TrimSpace(stdio.Name), strings.TrimSpace(stdio.Command)
	if name == "" || command == "" || seen[name] {
		return mcp.ServerSettings{}, acp.NewInvalidParams(map[string]any{"mcpServers": "server needs unique name and command"})
	}
	seen[name] = true
	env := make(map[string]string, len(stdio.Env))
	for _, variable := range stdio.Env {
		if strings.TrimSpace(variable.Name) == "" {
			return mcp.ServerSettings{}, acp.NewInvalidParams(map[string]any{"mcpServers": "environment variable name is required"})
		}
		env[variable.Name] = variable.Value
	}
	return mcp.ServerSettings{ID: name, Command: command, Args: append([]string(nil), stdio.Args...), Env: env, Transport: mcp.TransportStdio}, nil
}

// httpMCPSetting maps the protocol's HTTP server onto ServerSettings.
func httpMCPSetting(httpServer *acp.McpServerHttpInline, seen map[string]bool) (mcp.ServerSettings, error) {
	name, serverURL := strings.TrimSpace(httpServer.Name), strings.TrimSpace(httpServer.Url)
	if name == "" || serverURL == "" || seen[name] {
		return mcp.ServerSettings{}, acp.NewInvalidParams(map[string]any{"mcpServers": "server needs unique name and url"})
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return mcp.ServerSettings{}, acp.NewInvalidParams(map[string]any{"mcpServers": "http server url needs an http or https scheme"})
	}
	seen[name] = true
	headers := make(map[string]string, len(httpServer.Headers))
	for _, header := range httpServer.Headers {
		if strings.TrimSpace(header.Name) == "" {
			return mcp.ServerSettings{}, acp.NewInvalidParams(map[string]any{"mcpServers": "http header name is required"})
		}
		headers[header.Name] = header.Value
	}
	return mcp.ServerSettings{ID: name, URL: serverURL, Headers: headers, Transport: mcp.TransportStreamableHTTP}, nil
}
