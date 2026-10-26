package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gkconfig "github.com/gookit/config/v2"
	"github.com/inhere/suga/internal/adddir"
	chatreply "github.com/inhere/suga/internal/chat"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
	approvalrules "github.com/inhere/suga/pkg/agentcore/tools/rules"
)

// chatApprover is the chat tools.Approver: the shared policy decides whether a
// call is allow/ask/deny, while the approver supplies the terminal interaction
// for ask. A file call that reaches outside the workspace and the session's
// directories is asked about separately (chat.ask_outside_dirs).
type chatApprover struct {
	gate      *cliMCPGate
	policy    approvalPolicy
	usePolicy bool
	auto      bool
	asker     tools.Asker
	workspace string
	// translator renders approval questions and their human-facing details.
	translator i18n.Translator
	// liveDirs is the session's additional-directory set: a file_remove prompt for
	// a path under a :rw directory is planned with the same rules as the workspace.
	liveDirs *native.AddDirs
	// readRoots are the configured read-only roots (native.RootPaths): a path under
	// one is already reachable and needs no authorization.
	readRoots []string
	// readOnly marks a --read-only session: it has no write tool at all, so only a
	// read-only directory can be authorized.
	readOnly bool
	// askOutside turns the outside-directory question on (chat.ask_outside_dirs,
	// on by default); addDirs is the session's added-directory handle it works
	// through (nil: the question stays off).
	askOutside bool
	addDirs    *SessionAddDirs

	// persistReporter records a best-effort writeback event with the turn context.
	persistReporter func(context.Context, ApprovalRulePersistEvent)

	// mu serializes questions and guards the in-memory session rules.
	mu     sync.Mutex
	always map[string]bool // compatibility for callers without the shared policy
}

// chatApproverOptions are the inputs of the chat approver, read when the runtime
// is assembled (the session does not exist yet).
type chatApproverOptions struct {
	gate       *cliMCPGate
	auto       bool
	asker      tools.Asker
	workspace  string
	translator i18n.Translator
	dirs       *native.AddDirs
	readRoots  []string
	readOnly   bool
	askOutside bool
	// persistReporter is called after an attempted file writeback, including a
	// failed attempt, so trace can explain why a session rule was not persisted.
	persistReporter func(context.Context, ApprovalRulePersistEvent)
}

func newChatApprover(opts chatApproverOptions) *chatApprover {
	reporter := opts.persistReporter
	if reporter == nil {
		reporter = func(ctx context.Context, event ApprovalRulePersistEvent) {
			personal.RecordApprovalRulePersist(ctx, event.File, string(event.Action), event.Result)
		}
	}
	a := &chatApprover{gate: opts.gate, auto: opts.auto, asker: opts.asker, workspace: opts.workspace,
		liveDirs: opts.dirs, readRoots: opts.readRoots, readOnly: opts.readOnly, askOutside: opts.askOutside,
		persistReporter: reporter, always: map[string]bool{}, translator: opts.translator}
	if opts.gate != nil {
		a.policy = opts.gate.policy
		// Tests and direct callers can provide a run-mode gate; chat owns the
		// interactive default even when the gate was built before chat wiring.
		a.policy.Mode = approvalrules.ModeChat
		a.policy.Defaults = defaultApprovalDefaults(approvalrules.ModeChat, opts.auto)
		a.usePolicy = !a.policy.compiled || a.policy.rulesEnabled
	}
	return a
}

// SetAddDirs wires the session's added-directory handle: the outside-directory
// question authorizes a directory through it, the same handle ApplyAddDirs
// publishes the session's directories with. nil keeps the question off.
func (a *chatApprover) SetAddDirs(dirs *SessionAddDirs) { a.addDirs = dirs }

// toolsApprovalAuto reads tools.approval: "ask" (default) or "auto". Any other
// value is a startup error (fail closed), never guessed.
func toolsApprovalAuto() (bool, error) {
	switch value := gkconfig.Get("tools.approval").(type) {
	case nil:
		return false, nil
	case string:
		switch value {
		case appconfig.ToolsApprovalAsk:
			return false, nil
		case appconfig.ToolsApprovalAuto:
			return true, nil
		}
	}
	return false, fmt.Errorf("tools.approval must be \"ask\" or \"auto\", got %v", gkconfig.Get("tools.approval"))
}

// Decide applies explicit policy rules before the interactive default. Session
// rules are kept in the same Policy slice, so their deny/allow strength is
// identical to user and project rules and their scope remains observable.
func (a *chatApprover) Decide(ctx context.Context, call agentcore.ToolCall, tool agentcore.Tool) error {
	// A file call that reaches outside the workspace and the session's directories
	// is asked about first: what such a call needs is a new directory, not the
	// permission for one call.
	if err, stop := a.askOutsideDir(ctx, call); stop {
		return err
	}
	name := call.Name
	var action approvalrules.Action
	var hit *approvalrules.Rule
	var scope approvalScope
	if a.usePolicy {
		scope = a.scopeFor(call, tool)
		a.mu.Lock()
		action, hit = a.policy.Decide(a.policy.targetFor(call, tool), tools.MetaOf(tool))
		a.mu.Unlock()
		if hit != nil {
			reportScope := ""
			if hit.Source == approvalrules.SourceSession {
				reportScope = approvalScopeAttr(call.Name, scope.key)
			}
			tools.ReportRule(ctx, hit.Summary(), reportScope)
		}
		switch action {
		case approvalrules.Allow:
			if hit != nil && hit.Source == approvalrules.SourceSession {
				tools.ReportApproval(ctx, tools.ApprovedAlways, 0)
			}
			return nil
		case approvalrules.Deny:
			if hit != nil {
				return tools.Denied(fmt.Sprintf("规则 %s 拒绝执行 %s", hit.Summary(), name))
			}
			return tools.Denied("需要许可：" + name + "，本次运行未授权")
		}
		// Ask continues through the interactive path below. Session-level rules
		// already returned above; this fallback is only for an ordinary ask.
	} else {
		// Compatibility for small unit callers that construct the old approver
		// without a compiled policy. Production chat always uses the shared path.
		if tools.IsMCP(tool) {
			return a.gate.Decide(ctx, call, tool)
		}
		if meta := tools.MetaOf(tool); meta.ReadOnly || meta.AgentState || a.auto {
			return nil
		}
		a.mu.Lock()
		always := a.always[name]
		a.mu.Unlock()
		if always {
			tools.ReportApproval(ctx, tools.ApprovedAlways, 0)
			return nil
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	asked := time.Now()
	question := a.question(call)
	if a.usePolicy {
		question = a.questionFor(call, tool, scope)
	}
	answer, err := a.asker.Ask(ctx, question)
	if errors.Is(err, tools.ErrCannotAsk) {
		return tools.Denied(fmt.Sprintf("无法确认执行 %s（标准输入不是终端），已拒绝；如需在 chat 中自动放行，请设置 tools.approval: auto", name))
	}
	if err != nil {
		return err // not a refusal: the approval layer aborts the turn
	}
	wait := time.Since(asked)
	if !a.usePolicy {
		switch chatreply.ParseApprovalReply(answer) {
		case chatreply.ReplyAllow:
			tools.ReportApprovalAnswer(ctx, tools.ApprovedByUser, wait)
			return nil
		case chatreply.ReplyAllowAlways:
			a.always[name] = true
			tools.ReportApprovalAnswer(ctx, tools.ApprovedByUser, wait)
			return nil
		}
		tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
		return tools.Denied("用户拒绝执行 " + name)
	}

	reply := chatreply.ParseApprovalReply(answer)
	switch reply {
	case chatreply.ReplyAllow:
		tools.ReportApprovalAnswer(ctx, tools.ApprovedByUser, wait)
		return nil
	case chatreply.ReplyAllowAlways, chatreply.ReplyDenyAlways:
		if reply == chatreply.ReplyAllowAlways && !scope.allowAlways {
			tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
			return tools.Denied("用户拒绝执行 " + name)
		}
		decision := approvalrules.Allow
		if reply == chatreply.ReplyDenyAlways {
			decision = approvalrules.Deny
		}
		if scope.rule.Tool == "" {
			scope.rule.Tool = name
		}
		scope.rule.Source = approvalrules.SourceSession
		scope.rule.Index = a.nextSessionRuleIndex()
		scope.rule.Action = decision
		a.policy.Rules = append(a.policy.Rules, scope.rule)
		tools.ReportRule(ctx, scope.rule.Summary(), approvalScopeAttr(call.Name, scope.key))
		if decision == approvalrules.Allow {
			// 这一次调用是人在提示里批准的（选“总是允许”也算批准了这一次），
			// 模型可见结果会注明；之后由记住的会话规则放行的调用不再注明。
			tools.ReportApprovalAnswer(ctx, tools.ApprovedByUser, wait)
		} else {
			tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
		}
		if a.policy.PersistAsk {
			persistAnswer, persistErr := a.asker.Ask(ctx, tools.Question{Tool: "approval_rule", Prompt: chatreply.PersistPromptWithTranslator(a.translator, string(decision))})
			if persistErr != nil {
				// The session rule is already active. A failed follow-up must not
				// turn a successful approval into a failed tool call.
				slog.Warn("approval rule persistence prompt failed", "action", decision, "err", persistErr)
			} else {
				switch destination := chatreply.ParsePersistReply(persistAnswer); destination {
				case chatreply.PersistUser, chatreply.PersistProject:
					if persistErr := a.persistRule(ctx, destination, scope.rule); persistErr != nil {
						// Writeback is best effort: this call keeps using the session rule.
						slog.Warn("persist approval rule", "action", decision, "err", persistErr)
					}
				}
			}
		}
		if decision == approvalrules.Deny {
			return tools.Denied(fmt.Sprintf("规则 %s 拒绝执行 %s", scope.rule.Summary(), name))
		}
		return nil
	default:
		tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
		return tools.Denied("用户拒绝执行 " + name)
	}
}

// persistRule resolves the explicit user choice to one of the two rule files.
// The session rule remains in memory regardless of the write result.
func (a *chatApprover) persistRule(ctx context.Context, destination chatreply.PersistReply, rule approvalrules.Rule) error {
	var (
		path       string
		source     approvalrules.Source
		resolveErr error
	)
	switch destination {
	case chatreply.PersistUser:
		source = approvalrules.SourceUser
		path, resolveErr = appconfig.ResolveUserApprovalRulesPath()
	case chatreply.PersistProject:
		source = approvalrules.SourceProject
		path, resolveErr = appconfig.ResolveProjectApprovalRulesPath(a.workspace)
	default:
		return nil
	}
	if resolveErr != nil {
		event := ApprovalRulePersistEvent{File: path, Action: rule.Action, Result: "error"}
		a.persistReporter(ctx, event)
		return fmt.Errorf("resolve approval rule writeback path: %w", resolveErr)
	}
	return PersistApprovalRule(path, source, rule, func(event ApprovalRulePersistEvent) {
		a.persistReporter(ctx, event)
	})
}

func (a *chatApprover) nextSessionRuleIndex() int {
	index := 1
	for _, rule := range a.policy.Rules {
		if rule.Source == approvalrules.SourceSession && rule.Index >= index {
			index = rule.Index + 1
		}
	}
	return index
}

// approvalScope is the session-rule form and the human wording for one call.
type approvalScope struct {
	rule        approvalrules.Rule
	key         string
	description string
	allowAlways bool
}

func (a *chatApprover) scopeFor(call agentcore.ToolCall, tool agentcore.Tool) approvalScope {
	target := a.policy.targetFor(call, tool)
	scope := approvalScope{rule: approvalrules.Rule{Tool: call.Name, Modes: []approvalrules.Mode{approvalrules.ModeChat}}, description: approvalTr(a.translator).T("approval.scope.tool"), allowAlways: true}
	if target.MCP {
		scope.rule.Tool = target.MCPServer + "/" + call.Name
		if target.MCPServer == "" {
			scope.rule.Tool = call.Name
		}
		return scope
	}
	if call.Name == "shell_exec" {
		command, _ := call.Arguments["command"].(string)
		tokens, ok := scopeCommandTokens(command)
		if !ok || len(tokens) == 0 {
			scope.allowAlways = false
			return scope
		}
		count := a.policy.AlwaysPrefixTokens
		if count < 1 {
			count = 2
		}
		if count > len(tokens) {
			count = len(tokens)
		}
		prefix := scopeTokensText(tokens[:count])
		pattern := prefix
		if count < len(tokens) {
			pattern += " *"
		}
		scope.key, scope.description = prefix, approvalTr(a.translator).T("approval.scope.command", map[string]any{"prefix": displaySafe(redact.Secrets(prefix))})
		scope.rule.Command = []string{pattern}
		candidate := scope.rule
		candidate.Action = approvalrules.Allow
		scope.allowAlways = candidate.MatchesCommand(target)
		return scope
	}
	if call.Name == "file_write" || call.Name == "file_edit" || call.Name == "file_remove" {
		pathValue, _ := call.Arguments["path"].(string)
		relative, ok := a.relativeScopePath(pathValue)
		if !ok {
			return scope
		}
		scope.key = relative
		scope.rule.Root = a.policy.Workspace
		if call.Name == "file_remove" {
			scope.rule.Path = []string{relative}
			scope.description = approvalTr(a.translator).T("approval.scope.target")
		} else {
			// A write or edit names a file: "always" covers the directory it
			// is in (docs/a.txt -> docs/**), not the file taken as a directory.
			dir := path.Dir(relative)
			scope.key = dir
			if dir == "." {
				scope.key = ""
				scope.rule.Path = []string{"**"}
			} else {
				scope.rule.Path = []string{dir + "/**"}
			}
			scope.description = approvalTr(a.translator).T("approval.scope.directory", map[string]any{"path": displaySafe(redact.Secrets(scope.key))})
		}
	}
	return scope
}

func (a *chatApprover) relativeScopePath(value string) (string, bool) {
	if value == "" || a.policy.Workspace == "" {
		return "", false
	}
	absolute := value
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(a.policy.Workspace, absolute)
	}
	relative, err := filepath.Rel(a.policy.Workspace, filepath.Clean(absolute))
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func scopeCommandTokens(command string) ([]string, bool) {
	var tokens []string
	var token strings.Builder
	var quote byte
	inToken := false
	flush := func() {
		if inToken {
			tokens = append(tokens, token.String())
			token.Reset()
			inToken = false
		}
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '\'' || c == '"':
			if quote == 0 {
				quote = c
				inToken = true
			} else if quote == c {
				quote = 0
			} else {
				return nil, false
			}
		case c == ' ' && quote == 0:
			flush()
		default:
			token.WriteByte(c)
			inToken = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return tokens, true
}

func scopeTokensText(tokens []string) string {
	out := make([]string, len(tokens))
	for i, token := range tokens {
		if strings.Contains(token, " ") {
			out[i] = `"` + token + `"`
		} else {
			out[i] = token
		}
	}
	return strings.Join(out, " ")
}

// askOutsideDir asks the person to authorize the directory a file tool call
// wants to reach outside the workspace and the session's added directories
// (chat.ask_outside_dirs), before the call runs. The question's subject is the
// directory the tool would really resolve (native.OutsideOf), never a path the
// model described in words, so a call cannot talk its way into another directory.
//
// A granted directory is recorded and published at once (source ask), so this
// call and the ones after it reach the directory; the call then goes on through
// the ordinary rules, so a write still gets its own confirmation.
func (a *chatApprover) askOutsideDir(ctx context.Context, call agentcore.ToolCall) (err error, stop bool) {
	if !a.askOutside || a.addDirs == nil {
		return nil, false
	}
	pending, ok := native.OutsideOf(a.workspace, a.readRoots, a.liveDirs, call.Name, call.Arguments)
	if !ok {
		return nil, false
	}
	askedAt := time.Now()
	answer, askErr := a.asker.Ask(ctx, a.outsideQuestion(call, pending))
	if errors.Is(askErr, tools.ErrCannotAsk) {
		// No terminal: the tool refuses the path by itself, and a message here
		// would only hide that reason.
		return nil, false
	}
	if askErr != nil {
		return askErr, true
	}
	wait := time.Since(askedAt)
	writable, refuse := outsideAnswer(answer)
	if refuse {
		tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
		return tools.Denied("用户拒绝授权访问 " + pending.Path), true
	}
	if _, err := a.addDirs.add(ctx, adddir.Dir{Path: pending.Path, Writable: writable}, adddir.SourceAsk); err != nil {
		tools.ReportApproval(ctx, tools.AskedAndDenied, wait)
		return tools.Denied("授权访问 " + pending.Path + " 失败：" + err.Error()), true
	}
	tools.ReportApproval(ctx, tools.ApprovedByUser, wait)
	return nil, false
}

// outsideAnswer reads the answer to an outside-directory question: [r] a
// read-only directory (the default for anything that is not [w]), [w] a
// read-write one. refuse is true for "n", an empty answer and anything else.
func outsideAnswer(answer string) (writable, refuse bool) {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "r", "ro", "read", "只读":
		return false, false
	case "w", "rw", "write", "读写":
		return true, false
	}
	return false, true
}

// outsideQuestion is the question of one outside-directory authorization: the
// tool that asked, the path it gave and the canonical directory to authorize.
// A --read-only session offers the read-only answer only: it has no write tool
// to use a read-write directory with.
func (a *chatApprover) outsideQuestion(call agentcore.ToolCall, pending native.OutsideDir) tools.Question {
	tr := approvalTr(a.translator)
	details := []string{tr.T("approval.outside.detail.tool", map[string]any{"tool": call.Name}), tr.T("approval.outside.detail.target", map[string]any{"target": pending.Target})}
	if pending.Path != pending.Target {
		details = append(details, tr.T("approval.outside.detail.directory", map[string]any{"path": pending.Path}))
	}
	prompt := tr.T("approval.outside.prompt", map[string]any{"path": pending.Path})
	if a.readOnly {
		prompt = tr.T("approval.outside.prompt_readonly", map[string]any{"path": pending.Path})
	}
	for i, detail := range details {
		details[i] = displaySafe(redact.Secrets(detail))
	}
	return tools.Question{Tool: call.Name, Details: details, Prompt: displaySafe(redact.Secrets(prompt))}
}

// question shows the full key arguments of the call: redacted, never truncated.
func (a *chatApprover) question(call agentcore.ToolCall) tools.Question {
	return a.questionFor(call, nil, approvalScope{})
}

func (a *chatApprover) questionFor(call agentcore.ToolCall, _ agentcore.Tool, scope approvalScope) tools.Question {
	args := call.Arguments
	tr := approvalTr(a.translator)
	var details []string
	switch call.Name {
	case "shell_exec":
		details = []string{tr.T("approval.detail.command", map[string]any{"value": argText(args["command"])})}
	case "file_remove":
		details = []string{tr.T("approval.detail.path", map[string]any{"value": a.fullPath(argText(args["path"]))})}
		plan, err := native.PlanRemoveWithDirs(a.workspace, a.liveDirs.WritePaths(), args)
		switch {
		case err != nil:
			details = append(details, tr.T("approval.detail.check", map[string]any{"value": err.Error()}))
		case plan.Purge:
			details = append(details, tr.T("approval.detail.purge"))
		case plan.Dir:
			details = append(details, tr.T("approval.detail.directory", map[string]any{"files": plan.Files, "bytes": plan.Bytes}))
		default:
			details = append(details, tr.T("approval.detail.file", map[string]any{"bytes": plan.Bytes}))
		}
		if err == nil && len(plan.PurgeDirs) > 0 {
			details = append(details, tr.T("approval.detail.purge_dirs", map[string]any{"count": len(plan.PurgeDirs), "dirs": native.NotedDirs(plan.PurgeDirs)}))
		}
	case "file_write":
		details = []string{
			tr.T("approval.detail.path", map[string]any{"value": a.fullPath(argText(args["path"]))}),
			tr.T("approval.detail.content", map[string]any{"bytes": len(argText(args["content"]))}),
		}
	case "file_edit":
		details = []string{tr.T("approval.detail.path", map[string]any{"value": a.fullPath(argText(args["path"]))})}
		edits, err := native.EditList(args)
		if err != nil {
			raw, _ := json.Marshal(args["edits"])
			details = append(details, tr.T("approval.detail.edits", map[string]any{"value": string(raw)}))
		}
		for i, edit := range edits {
			label := ""
			if len(edits) > 1 {
				label = tr.T("approval.detail.edit_label", map[string]any{"index": i + 1})
			}
			details = append(details,
				tr.T("approval.detail.edit_at", map[string]any{"label": label, "value": previewLines(argText(edit["old_string"]))}),
				tr.T("approval.detail.edit_to", map[string]any{"label": label, "value": previewLines(argText(edit["new_string"]))}))
			if all, _ := edit["replace_all"].(bool); all {
				details = append(details, tr.T("approval.detail.replace_all", map[string]any{"label": label}))
			}
		}
	case "web_search":
		details = []string{tr.T("approval.detail.query", map[string]any{"value": argText(args["query"])})}
	default:
		raw, _ := json.Marshal(args)
		details = []string{tr.T("approval.detail.arguments", map[string]any{"value": string(raw)})}
	}
	for i, detail := range details {
		details[i] = displaySafe(redact.Secrets(detail))
	}
	prompt := chatreply.ApprovalPromptWithTranslator(a.translator, call.Name, "", true)
	if scope.rule.Tool != "" || scope.key != "" {
		prompt = chatreply.ApprovalPromptWithTranslator(a.translator, call.Name, scope.description, scope.allowAlways)
	}
	return tools.Question{Tool: call.Name, Details: details, Prompt: displaySafe(prompt)}
}

// editPreviewLines bounds each side of a file_edit preview.
const editPreviewLines = 20

// previewLines returns text cut to editPreviewLines lines, noting the total.
func previewLines(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= editPreviewLines {
		return text
	}
	return strings.Join(lines[:editPreviewLines], "\n") + fmt.Sprintf("\n…（共 %d 行）", len(lines))
}

// fullPath resolves a workspace-relative path for display.
func (a *chatApprover) fullPath(path string) string {
	if path == "" || filepath.IsAbs(path) || a.workspace == "" {
		return path
	}
	return filepath.Join(a.workspace, path)
}

func argText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

// displaySafe removes terminal control sequences (including OSC/CSI) before
// details or prompts are written. Newlines and tabs remain useful formatting.
func displaySafe(s string) string { return termtext.Clean(s) }

func approvalTr(t i18n.Translator) i18n.Translator {
	if t == nil {
		return i18n.Fallback()
	}
	return t
}

// approvalScopeAttr is the trace value of a session scope: the tool and its
// scope key ("file_write:docs", "shell_exec:go test"), empty without a key.
func approvalScopeAttr(tool, key string) string {
	if key == "" {
		return ""
	}
	return tool + ":" + key
}
