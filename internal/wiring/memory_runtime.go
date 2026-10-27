package wiring

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	gkconfig "github.com/gookit/config/v2"

	axconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/memory"
	"github.com/inhere/suga/pkg/agentcore/redact"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// MemoryConfirmFunc asks the person to approve a memory write from a tainted
// session (memory.Policy.Confirm). It receives the op before sanitizing.
type MemoryConfirmFunc func(ctx context.Context, s memory.Scope, op memory.Op) error

// memory.write and memory.write_tainted values (design D7) are shared with
// the config registry so command validation and runtime parsing cannot drift.
const (
	memoryWriteAuto    = axconfig.MemoryWriteAuto
	memoryWriteOff     = axconfig.MemoryWriteOff
	memoryTaintedAsk   = axconfig.MemoryTaintedAsk
	memoryTaintedDeny  = axconfig.MemoryTaintedDeny
	memoryTaintedAllow = axconfig.MemoryTaintedAllow
)

// memoryWritePolicy is memory.write / memory.write_tainted.
type memoryWritePolicy struct {
	Write   string
	Tainted string
}

// loadMemoryWritePolicy reads the write policy; unknown values are startup
// errors (fail closed), never guessed.
func loadMemoryWritePolicy() (memoryWritePolicy, error) {
	read := func(key, def string, allowed ...string) (string, error) {
		raw := gkconfig.Get(key)
		if raw == nil {
			return def, nil
		}
		if value, ok := raw.(string); ok {
			value = strings.ToLower(strings.TrimSpace(value))
			for _, a := range allowed {
				if value == a {
					return value, nil
				}
			}
		}
		return "", fmt.Errorf("%s must be one of %s, got %v", key, strings.Join(allowed, ", "), raw)
	}
	write, err := read("memory.write", memoryWriteAuto, memoryWriteAuto, memoryWriteOff)
	if err != nil {
		return memoryWritePolicy{}, err
	}
	tainted, err := read("memory.write_tainted", memoryTaintedAsk, memoryTaintedAsk, memoryTaintedDeny, memoryTaintedAllow)
	if err != nil {
		return memoryWritePolicy{}, err
	}
	return memoryWritePolicy{Write: write, Tainted: tainted}, nil
}

// memoryHost is what the governed store needs from the runner and the session
// store. Both exist only after the tools are built, so they are bound later
// (bind); tools run only inside turns, after binding.
type memoryHost struct {
	runner *personal.Runner
	prov   memory.ProvenanceStore
}

var _ memory.ProvenanceStore = (*memoryHost)(nil)

func (h *memoryHost) bind(runner *personal.Runner, prov memory.ProvenanceStore) {
	h.runner, h.prov = runner, prov
}

// tainted fails closed while unbound.
func (h *memoryHost) tainted(ctx context.Context) bool {
	if h.runner == nil {
		return true
	}
	return h.runner.SessionTainted(ctx)
}

func (h *memoryHost) observe(ctx context.Context, ev memory.Event) {
	if h.runner != nil {
		h.runner.RecordMemoryEvent(ctx, ev)
	}
}

var errMemoryHostUnbound = errors.New("memory: provenance store is not ready")

func (h *memoryHost) Record(ctx context.Context, backend string, s memory.Scope, e memory.Entry, fp string) error {
	if h.prov == nil {
		return errMemoryHostUnbound
	}
	return h.prov.Record(ctx, backend, s, e, fp)
}

func (h *memoryHost) Tombstone(ctx context.Context, backend string, s memory.Scope, id, fp string) error {
	if h.prov == nil {
		return errMemoryHostUnbound
	}
	return h.prov.Tombstone(ctx, backend, s, id, fp)
}

func (h *memoryHost) IsTombstoned(ctx context.Context, backend string, s memory.Scope, fp string) (bool, error) {
	if h.prov == nil {
		return false, errMemoryHostUnbound
	}
	return h.prov.IsTombstoned(ctx, backend, s, fp)
}

// memoryRuntime is the memory part of one runtime: the governed store over
// the scope router and the memory tool.
type memoryRuntime struct {
	setup *MemorySetup
	store *memory.Governed
	tool  *native.MemoryTool
}

// memoryTaintedConfirm turns memory.write_tainted into Policy.Confirm. ask
// uses the front end's confirmation; a nil one (run) makes Governed refuse
// with ErrNeedsConfirmation.
func memoryTaintedConfirm(mode string, ask MemoryConfirmFunc) MemoryConfirmFunc {
	switch mode {
	case memoryTaintedAllow:
		return func(context.Context, memory.Scope, memory.Op) error { return nil }
	case memoryTaintedDeny:
		return func(context.Context, memory.Scope, memory.Op) error {
			return errors.New("memory.write_tainted 为 deny：会话读过外部内容后不写入记忆")
		}
	}
	return ask
}

// newMemoryRuntime wraps the router with the write policy (design "写入记忆")
// and builds the memory tool. readOnly (--read-only, ACP read_only) and
// memory.write: off make the tool refuse writes itself.
func newMemoryRuntime(setup *MemorySetup, policy memoryWritePolicy, readOnly bool, confirm MemoryConfirmFunc, host *memoryHost, texts native.MemoryTexts) *memoryRuntime {
	p := memory.Policy{
		Tainted:    host.tainted,
		Sanitize:   redact.Clean,
		Observe:    host.observe,
		Provenance: host,
	}
	if c := memoryTaintedConfirm(policy.Tainted, confirm); c != nil {
		p.Confirm = c
	}
	gov := memory.NewGoverned(setup.Router, p)
	tool := native.NewMemoryTool(gov, native.MemoryToolOptions{
		Writable:   !readOnly && policy.Write != memoryWriteOff,
		ProjectKey: setup.Project.Scope.Key,
		Backend: func(s memory.Scope) string {
			b, err := setup.Router.Route(s)
			if err != nil {
				return ""
			}
			return memory.BackendName(b)
		},
		ProvenanceFrom: personal.MemoryProvenance,
		Texts:          texts,
	})
	return &memoryRuntime{setup: setup, store: gov, tool: tool}
}

// index renders the user and project indexes for the session snapshot
// (personal.Config.MemoryIndex). A failing scope is skipped and reported.
func (m *memoryRuntime) index(ctx context.Context) (string, error) {
	var b strings.Builder
	var errs []error
	for _, s := range []memory.Scope{m.setup.User.Scope, m.setup.Project.Scope} {
		ix, err := m.store.Index(ctx, s)
		if err != nil {
			errs = append(errs, fmt.Errorf("memory index %s: %w", s, err))
			continue
		}
		b.WriteString(ix.Render())
	}
	return b.String(), errors.Join(errs...)
}

// chatMemoryConfirmer asks at the chat terminal before a tainted session
// writes memory. "a" stops asking for the rest of that session in this
// process.
type chatMemoryConfirmer struct {
	asker tools.Asker
	mu    sync.Mutex
	// always holds the sessions answered "a".
	always map[string]bool
	// translator renders the question the person reads.
	translator i18n.Translator
}

func newChatMemoryConfirmer(asker tools.Asker, translator i18n.Translator) *chatMemoryConfirmer {
	return &chatMemoryConfirmer{asker: asker, always: map[string]bool{}, translator: translator}
}

func (c *chatMemoryConfirmer) confirm(ctx context.Context, s memory.Scope, op memory.Op) error {
	session := personal.SessionIDFromContext(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if session != "" && c.always[session] {
		return nil
	}
	answer, err := c.asker.Ask(ctx, memoryQuestion(c.translator, s, op))
	if errors.Is(err, tools.ErrCannotAsk) {
		return errors.New("无法确认写入记忆（标准输入不是终端）")
	}
	if err != nil {
		return err // cancelled: the memory tool ends the turn
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	case "a", "always":
		if session != "" {
			c.always[session] = true
		}
		return nil
	}
	return errors.New("用户拒绝写入记忆")
}

// memoryBodyPreviewRunes bounds the body shown in the question.
const memoryBodyPreviewRunes = 200

// memoryQuestion shows what would be written. Confirm gets the op before the
// governance layer sanitizes it, so each field is cleaned (redact.Clean) and
// made display-safe here; the body is cut to a preview. tr is the
// startup-resolved host catalog (the ACP permission request uses the same keys,
// see acpserver/memory_confirm.go).
func memoryQuestion(tr i18n.Translator, s memory.Scope, op memory.Op) tools.Question {
	t := approvalTr(tr)
	details := []string{
		t.T("host.memory.need_confirm"),
		t.T("host.memory.label.scope", map[string]any{"scope": s.String()}),
		t.T("host.memory.label.action", map[string]any{"action": string(op.Action)}),
	}
	if op.Kind != "" {
		details = append(details, t.T("host.memory.label.kind", map[string]any{"kind": string(op.Kind)}))
	}
	if op.ID != "" {
		details = append(details, t.T("host.memory.label.id", map[string]any{"id": op.ID}))
	}
	if op.Title != "" {
		details = append(details, t.T("host.memory.label.title", map[string]any{"title": redact.Clean(op.Title)}))
	}
	if op.Body != "" {
		body := []rune(redact.Clean(op.Body))
		if len(body) > memoryBodyPreviewRunes {
			body = append(body[:memoryBodyPreviewRunes], '…')
		}
		details = append(details, t.T("host.memory.label.body", map[string]any{"body": string(body)}))
	}
	for i, d := range details {
		details[i] = displaySafe(d)
	}
	return tools.Question{Tool: "memory", Details: details, Prompt: t.T("host.memory.prompt")}
}
