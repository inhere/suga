package personal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/llm"
)

// Segment start reasons (compactionRecord.Reason). A record without a reason
// was written by an older binary and is an ordinary compaction. A --from
// handoff is a handoff entry (segment 0), not a record.
const (
	segmentReasonStart     = "start"     // session start: snapshot only, no handoff package
	segmentReasonThreshold = "threshold" // automatic: the context crossed the threshold
	segmentReasonManual    = "manual"    // chat :compact
	segmentReasonIdle      = "idle"      // chat idle consolidation (Runner.ConsolidateIdle)
)

// metaSegmentContext marks the segment context message in Message.Metadata.
// The message is rendered by compose for every request: it is not a stored
// entry, not a ctxItem, never a cut candidate and never part of items().
const metaSegmentContext = llm.SegmentContextMetaKey

// metaContextUpdate marks a replayed context_update message (Message.Metadata,
// never sent by providers). Summaries skip it: the next segment re-reads the
// sources anyway.
const metaContextUpdate = "suga.context_update"

// segmentContext is the frozen context snapshot of one segment (design
// "段起始记录"): what the segment sees besides the stable system prompt and its
// history. It is stored in the segment start record and rendered as the
// segment context message; session meta only caches rules/memory/append.
type segmentContext struct {
	Environment  string `json:"environment"`
	Rules        string `json:"rules"`
	RulesHash    string `json:"rules_hash,omitempty"`
	MemoryIndex  string `json:"memory_index"`
	MemoryHash   string `json:"memory_hash,omitempty"`
	Skills       string `json:"skills"`
	AppendPrompt string `json:"append_prompt"`
	// HostContext is the embedding host's context text (persona, topic index,
	// verbatim constraints; HostContextSource) and its hash. Frozen when the
	// segment starts, compared by hash at every turn; nil source = no section,
	// which keeps the rendering of a runtime without a host byte-identical.
	HostContext     string `json:"host_context,omitempty"`
	HostContextHash string `json:"host_context_hash,omitempty"`
	DeferredTools   string `json:"deferred_tools"`
	// Hash is sha256 over all the fields above (see seal).
	Hash string `json:"hash"`
}

// seal computes Hash over the snapshot content (Hash itself excluded).
func (c *segmentContext) seal() {
	c.Hash = ""
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	c.Hash = hex.EncodeToString(sum[:])
}

// legacySnapshot is the compatibility view of a session that has no segment
// record with a context (sessions written before segment records, or whose
// latest record came from an older binary): rules, memory index and append
// prompt from the session meta, the rest from the current sources. It is never
// stored.
func (r *Runner) legacySnapshot(meta map[string]any) *segmentContext {
	c := &segmentContext{
		Environment:   r.segmentEnvironment(),
		Rules:         metaString(meta, "personal_rules_content"),
		RulesHash:     metaString(meta, "personal_rules_hash"),
		MemoryIndex:   metaString(meta, metaMemoryIndexContent),
		MemoryHash:    metaString(meta, metaMemoryIndexHash),
		Skills:        r.skills(),
		AppendPrompt:  metaString(meta, "personal_append_prompt"),
		DeferredTools: r.deferredToolsPrompt(),
	}
	c.seal()
	return c
}

// freshSnapshot builds a new segment's snapshot from the current sources:
// environment, rules (re-read), memory index (re-read), skills, append prompt
// and deferred tool listing. prev is the effective context of the segment
// being closed (nil for a new session): a memory index that cannot be read
// and an append prompt the caller did not supply are carried over from it.
// A RuleSource error is returned together with a snapshot that keeps prev's
// rules, so the caller decides whether it is fatal.
func (r *Runner) freshSnapshot(ctx context.Context, sessionID string, prev *segmentContext) (*segmentContext, error) {
	if prev == nil {
		prev = &segmentContext{}
	}
	// A new segment rebuilds the prompt prefix anyway: the deferred native tools
	// loaded so far join the tool table now (tools.deferred_apply: segment), so
	// the listing and the table of this segment agree.
	if r.cfg.Visibility != nil {
		r.cfg.Visibility.FoldLoaded(ctx)
	}
	c := &segmentContext{
		Environment:   r.segmentEnvironment(),
		Rules:         prev.Rules,
		RulesHash:     prev.RulesHash,
		MemoryIndex:   prev.MemoryIndex,
		MemoryHash:    prev.MemoryHash,
		Skills:        r.skills(),
		AppendPrompt:  r.currentAppendPrompt(prev.AppendPrompt),
		DeferredTools: r.deferredToolsPrompt(),
	}
	// The host context is re-read for every new segment (a handoff or compaction
	// ends a segment); without a source the previous one is carried over, like
	// the append prompt.
	c.HostContext, c.HostContextHash = prev.HostContext, prev.HostContextHash
	if content, hash, ok := r.hostContext(); ok {
		c.HostContext, c.HostContextHash = content, hash
	}
	if content, hash, ok := r.memorySnapshot(ctx, sessionID); ok {
		c.MemoryIndex, c.MemoryHash = content, hash
	}
	var err error
	if r.cfg.RuleSource != nil {
		rules, hash, rerr := r.cfg.RuleSource()
		if rerr != nil {
			err = rerr
		} else {
			c.Rules, c.RulesHash = rules, hash
		}
	}
	c.seal()
	return c, err
}

// hostContext reads the injected host context (HostContextSource): the content
// and the hash the caller uses to tell versions apart. ok is false when there is
// none - no source, or empty content, which is the same as no section at all. A
// caller-supplied empty hash is derived from the content, so a host that only
// renders text still gets change detection.
func (r *Runner) hostContext() (string, string, bool) {
	if r.cfg.HostContextSource == nil {
		return "", "", false
	}
	content, hash := r.cfg.HostContextSource()
	if strings.TrimSpace(content) == "" {
		return "", "", false
	}
	if hash == "" {
		sum := sha256.Sum256([]byte(content))
		hash = hex.EncodeToString(sum[:])
	}
	return content, hash, true
}

// environment and skills read the injected sources ("" when not wired).
func (r *Runner) environment() string {
	if r.cfg.EnvironmentSource == nil {
		return ""
	}
	return r.cfg.EnvironmentSource()
}

func (r *Runner) segmentEnvironment() string {
	if r.cfg.SegmentEnvironmentSource != nil {
		return r.cfg.SegmentEnvironmentSource()
	}
	return r.environment()
}

func (r *Runner) skills() string {
	if r.cfg.SkillsSource == nil {
		return ""
	}
	return r.cfg.SkillsSource()
}

// currentAppendPrompt is the append prompt in effect for this process: the
// caller-supplied one (--append-system-prompt / SUGA_APPEND_SYSTEM_PROMPT; an
// explicitly supplied empty value clears it), otherwise the persisted one.
func (r *Runner) currentAppendPrompt(persisted string) string {
	if r.cfg.AppendSystemPrompt != "" || r.cfg.AppendSystemPromptSet {
		return r.cfg.AppendSystemPrompt
	}
	return persisted
}

// currentSegment is the snapshot of the segment in effect: the latest adopted
// segment record's context, or the legacy (meta) view for sessions without one.
func (r *Runner) currentSegment(view historyView, meta map[string]any) *segmentContext {
	if view.segment != nil {
		return view.segment
	}
	return r.legacySnapshot(meta)
}

// resolveSegment fills the view's rendered segment (record or legacy view)
// and its effective context: the segment snapshot overlaid with the adopted
// context updates written after it (design D3: "生效 hash"). The effective
// context - not the session meta - is what the per-turn change check compares
// against.
func (r *Runner) resolveSegment(view *historyView, meta map[string]any) {
	view.segment = r.currentSegment(*view, meta)
	eff := *view.segment
	for _, u := range view.updates {
		u.applyTo(&eff)
	}
	view.eff = &eff
}

// writeStartSegment appends the reason=start record of a new session. It
// belongs to no turn, so it is adopted immediately; first_kept_seq 0 keeps
// every later message, which an older binary reads as "empty summary, keep
// everything" - the same view as before.
func (r *Runner) writeStartSegment(ctx context.Context, sessionID string, seg *segmentContext) error {
	rec := compactionRecord{Reason: segmentReasonStart, Context: seg}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := r.cfg.Repository.AppendEntries(ctx, sessionID, store.NewEntry{Type: store.EntryCompaction, JSON: raw}); err != nil {
		return fmt.Errorf("write segment start record: %w", err)
	}
	return nil
}

// segmentMeta is the session meta cache of a segment snapshot. Nothing renders
// from it except the legacy view; the segment record stays authoritative.
func segmentMeta(seg *segmentContext) map[string]any {
	out := map[string]any{
		"personal_rules_content": seg.Rules,
		"personal_rules_hash":    seg.RulesHash,
		// An empty string (not nil) so PatchSessionMeta stores it.
		"personal_append_prompt": seg.AppendPrompt,
		// A new segment starts folding afresh (fold events count from 0, and the
		// size trigger has to fire again).
		metaFoldBoundary: 0,
		metaFoldActive:   false,
	}
	if seg.MemoryHash != "" {
		out[metaMemoryIndexContent], out[metaMemoryIndexHash] = seg.MemoryIndex, seg.MemoryHash
	}
	return out
}

// Section headings of the segment context message (and of context updates).
const (
	segmentContextIntro = "以下是本段对话的上下文（环境、规则与参考信息）；本段内的变化会以“上下文更新”消息追加，以最新的为准。"
	rulesHeading        = "# Project rules"
	appendHeading       = "# Additional instructions"
	// hostContextHeading heads the embedding host's section (persona, topic
	// index, verbatim constraints): after the additional instructions, before
	// the deferred tool listing.
	hostContextHeading = "## Host context"
)

// renderSegmentParts renders the snapshot parts in the fixed order 环境 → 项目规则
// → 记忆索引 → skills → 附加提示 → 宿主上下文 → 延迟工具列表; empty parts are skipped.
func renderSegmentParts(c *segmentContext) []string {
	if c == nil {
		return nil
	}
	var parts []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	add(c.Environment)
	if strings.TrimSpace(c.Rules) != "" {
		add(rulesHeading + "\n\n" + c.Rules)
	}
	add(c.MemoryIndex)
	add(c.Skills)
	if strings.TrimSpace(c.AppendPrompt) != "" {
		add(appendHeading + "\n\n" + c.AppendPrompt)
	}
	if strings.TrimSpace(c.HostContext) != "" {
		add(hostContextHeading + "\n\n" + c.HostContext)
	}
	add(c.DeferredTools)
	return parts
}

// segmentContextMessage renders the segment context message: the snapshot
// parts, then the handoff package pkg ("" = none). fromHandoff marks a --from
// handoff (segment 0), which keeps its own wording. ok is false when there is
// nothing to render. Without snapshot parts the content is exactly the
// package's rendering (what an older binary shows for the same record).
func segmentContextMessage(seg *segmentContext, pkg string, fromHandoff bool) (agentcore.Message, bool) {
	parts := renderSegmentParts(seg)
	if len(parts) > 0 {
		parts = append([]string{segmentContextIntro}, parts...)
	}
	if pkg != "" {
		if !fromHandoff {
			pkg = summaryMessage(pkg).Content
		}
		parts = append(parts, pkg)
	}
	if len(parts) == 0 {
		return agentcore.Message{}, false
	}
	return agentcore.Message{Role: "user", Content: strings.Join(parts, "\n\n"), Metadata: map[string]any{metaSegmentContext: true}}, true
}

// isSegmentContext reports whether m is a rendered segment context message.
func isSegmentContext(m agentcore.Message) bool {
	v, _ := m.Metadata[metaSegmentContext].(bool)
	return v
}

// contextUpdateRecord is the JSON body of a context_update entry (design D3):
// the parts of the effective context that changed inside a segment. A nil
// field is unchanged; a non-nil one is the new value ("" = cleared). It is
// written at turn start with the turn's id, so it is adopted (and replayed as
// a user message at its seq) only when the turn completes.
type contextUpdateRecord struct {
	Environment  *string `json:"environment,omitempty"`
	Rules        *string `json:"rules,omitempty"`
	RulesHash    string  `json:"rules_hash,omitempty"`
	MemoryIndex  *string `json:"memory_index,omitempty"`
	MemoryHash   string  `json:"memory_hash,omitempty"`
	AppendPrompt *string `json:"append_prompt,omitempty"`
	// HostContext is the host's new text (nil = unchanged; "" = cleared).
	HostContext     *string `json:"host_context,omitempty"`
	HostContextHash string  `json:"host_context_hash,omitempty"`
}

// applyTo overlays the update on the effective context c.
func (u contextUpdateRecord) applyTo(c *segmentContext) {
	if u.Environment != nil {
		c.Environment = *u.Environment
	}
	if u.Rules != nil {
		c.Rules, c.RulesHash = *u.Rules, u.RulesHash
	}
	if u.MemoryIndex != nil {
		c.MemoryIndex, c.MemoryHash = *u.MemoryIndex, u.MemoryHash
	}
	if u.AppendPrompt != nil {
		c.AppendPrompt = *u.AppendPrompt
	}
	if u.HostContext != nil {
		c.HostContext, c.HostContextHash = *u.HostContext, u.HostContextHash
	}
}

// contextUpdateIntro heads a context update message.
const contextUpdateIntro = "# 上下文更新\n本段上下文中的下列部分已变化，以这里的版本为准（替代此前的版本）："

// message renders the update as the replayed user message (deterministic, so
// every later request sends the same bytes).
func (u contextUpdateRecord) message() agentcore.Message {
	parts := []string{contextUpdateIntro}
	if u.Environment != nil {
		parts = append(parts, orCleared(*u.Environment, "# Environment\n（无）"))
	}
	if u.Rules != nil {
		parts = append(parts, rulesHeading+"\n\n"+orCleared(*u.Rules, "（已清空：当前没有规则文件）"))
	}
	if u.MemoryIndex != nil {
		parts = append(parts, orCleared(*u.MemoryIndex, renderMemorySection("")))
	}
	if u.AppendPrompt != nil {
		parts = append(parts, appendHeading+"\n\n"+orCleared(*u.AppendPrompt, "（附加提示已清除，不再适用）"))
	}
	if u.HostContext != nil {
		parts = append(parts, hostContextHeading+"\n\n"+orCleared(*u.HostContext, "（宿主上下文已清除，不再适用）"))
	}
	return agentcore.Message{Role: "user", Content: strings.Join(parts, "\n\n"), Metadata: map[string]any{metaContextUpdate: true}}
}

func orCleared(s, cleared string) string {
	if strings.TrimSpace(s) == "" {
		return cleared
	}
	return strings.TrimSpace(s)
}

// isContextUpdate reports whether m is a replayed context update.
func isContextUpdate(m agentcore.Message) bool {
	v, _ := m.Metadata[metaContextUpdate].(bool)
	return v
}

// contextUpdate compares the current sources with the effective context eff
// and returns the update this turn must append (nil when nothing changed):
//   - rules: RuleSource hash differs from the effective rules hash (both empty
//     counts as unchanged); a RuleSource error fails the turn as before;
//   - append prompt: a caller-supplied value differs from the effective one;
//   - environment: the injected environment differs (e.g. resumed in another
//     mode or workspace);
//   - memory index: the segment has none yet (its snapshot failed when the
//     segment started, or the session predates memory). Memory writes inside a
//     segment never trigger an update (design D6: frozen until the next break).
//
// Since eff comes from adopted entries only, a failed turn's update is simply
// recomputed by the next turn: never lost, never duplicated.
func (r *Runner) contextUpdate(ctx context.Context, sessionID string, eff *segmentContext) (*contextUpdateRecord, error) {
	var u contextUpdateRecord
	changed := false
	if r.cfg.EnvironmentSource != nil {
		if env := r.cfg.EnvironmentSource(); env != eff.Environment {
			u.Environment, changed = &env, true
		}
	}
	if r.cfg.RuleSource != nil {
		rules, hash, err := r.cfg.RuleSource()
		if err != nil {
			return nil, err
		}
		if hash != eff.RulesHash && (rules != "" || eff.Rules != "") {
			u.Rules, u.RulesHash, changed = &rules, hash, true
		}
	}
	if eff.MemoryHash == "" {
		if content, hash, ok := r.memorySnapshot(ctx, sessionID); ok {
			u.MemoryIndex, u.MemoryHash, changed = &content, hash, true
		}
	}
	if appended := r.currentAppendPrompt(eff.AppendPrompt); appended != eff.AppendPrompt {
		u.AppendPrompt, changed = &appended, true
	}
	// The host context is compared by hash, not by text: a host that publishes a
	// new version of an unchanged-looking text still gets its update. No source
	// or empty content means the section is gone (cleared when the segment had one).
	if content, hash, ok := r.hostContext(); ok {
		if hash != eff.HostContextHash {
			u.HostContext, u.HostContextHash, changed = &content, hash, true
		}
	} else if eff.HostContext != "" {
		empty := ""
		u.HostContext, changed = &empty, true
	}
	if !changed {
		return nil, nil
	}
	return &u, nil
}

// writeContextUpdate appends u as this turn's context_update entry and returns
// it as a context item (its seq precedes the turn's user message, so the
// position is stable when replayed).
func (r *Runner) writeContextUpdate(ctx context.Context, sessionID, turnID string, u *contextUpdateRecord) (ctxItem, error) {
	raw, err := json.Marshal(u)
	if err != nil {
		return ctxItem{}, err
	}
	es, err := r.cfg.Repository.AppendEntries(ctx, sessionID, store.NewEntry{TurnID: turnID, Type: store.EntryContextUpdate, JSON: raw})
	if err != nil {
		return ctxItem{}, fmt.Errorf("write context update: %w", err)
	}
	if len(es) == 0 {
		return ctxItem{}, fmt.Errorf("write context update: no entry returned")
	}
	return ctxItem{msg: u.message(), seq: es[0].Seq}, nil
}

// cacheSegmentMeta stores the snapshot in the session meta cache
// (best-effort: the record is authoritative).
func (r *Runner) cacheSegmentMeta(ctx context.Context, sessionID string, seg *segmentContext) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.cfg.Repository.PatchSessionMeta(wctx, sessionID, segmentMeta(seg)); err != nil {
		slog.Warn("cache segment snapshot in session meta", "session_id", sessionID, "err", err)
	}
}
