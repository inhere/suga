package personal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/inhere/suga/pkg/agentcore"
)

// Handoff-package prefix reuse (design 2026-10-27-handoff-prefix-reuse-design):
// the handoff package of a segment break may be written by a call that
// continues the closing segment's last main-loop request - same System, same
// Tools, same messages, one new user instruction appended - so a provider
// prompt cache serves almost all of it. The request stays what it was whenever a
// reuse condition does not hold, and an answer that is not a handoff package is
// thrown away and asked for again the old way (H2).
//
// Scope (H1): only the segment break (segment_handoff.go -> summarize, scenario
// compact). The cross-session handoff (handoff.go) and the idle consolidation
// keep the old path.

// attrHandoffReuse is the model event attr recording how a handoff-package call
// was made: prefix (this call continued the segment's main-loop prefix) or
// fallback:<reason> (the old path, and why). A session without segment breaks
// carries no such attr.
const attrHandoffReuse = "handoff_reuse"

const (
	// handoffReusePrefix marks a call that continued the main-loop prefix.
	handoffReusePrefix = "prefix"
	// handoffReuseFallbackPrefix heads the value of a call made the old way.
	handoffReuseFallbackPrefix = "fallback:"
)

// Why a handoff package was not written on top of the segment's prefix: a reuse
// condition failed, or the reuse answer was discarded.
const (
	handoffFallbackDisabled        = "disabled"         // compact.handoff_reuse_prefix is off
	handoffFallbackModelDiffers    = "model_differs"    // the compact model is another model: no shared cache
	handoffFallbackNoRequest       = "no_request"       // the segment sent no request this process can continue
	handoffFallbackWindow          = "window"           // the request would not fit the model window
	handoffFallbackToolCall        = "tool_call"        // the answer asked for tools instead of writing the package
	handoffFallbackEmpty           = "empty"            // the answer had no text
	handoffFallbackMissingSections = "missing_sections" // the answer is not a handoff package
	handoffFallbackError           = "error"            // the reuse call itself failed
)

// handoffReuse is what a segment-break handoff call needs to continue the
// closing segment's last main-loop request.
type handoffReuse struct {
	req      *agentcore.GenerateRequest // System / Tools / Messages of that request
	provider agentcore.Provider         // the handoff call's provider (the turn's own model)
	tail     int                        // items of the closing view the new segment keeps verbatim
}

// reuseScopeText is the clause that replaces the transcript section of the
// reuse instruction: it names how far the summary reaches, and that the call
// must not go on working (the main system prompt invites exactly that).
func (r *Runner) reuseScopeText(tail int) string {
	text := modelTr(r.cfg.Translator).T(modelHandoffReuseScope)
	if strings.Count(text, "%d") < 2 {
		return text // a catalog entry without the two counts: use it as it is
	}
	return fmt.Sprintf(text, tail, tail)
}

// prepareHandoffReuse decides whether the handoff call of a segment break may
// continue the closing segment's last main-loop request. A nil reuse means the
// old path; reason names why it was not taken ("" when this is not a segment
// break at all: the cross-session handoff and the idle consolidation are out of
// scope and record no attr).
func (r *Runner) prepareHandoffReuse(sessionID, scenario string, tail int) (*handoffReuse, string) {
	if scenario != scenarioCompact {
		return nil, ""
	}
	if !r.cfg.HandoffReusePrefix {
		return nil, handoffFallbackDisabled
	}
	// Another model has its own cache: continuing this prefix would save nothing.
	if r.scenarioModelName(scenario) != r.cfg.Model {
		return nil, handoffFallbackModelDiffers
	}
	req := r.rec.prefix.lastRequest(sessionID)
	if req == nil || len(req.Messages) == 0 {
		return nil, handoffFallbackNoRequest
	}
	return &handoffReuse{req: req, provider: r.scenarioProvider(scenario), tail: tail}, ""
}

// request builds the handoff request of the reuse path: the last main-loop
// request (System, Tools and messages unchanged, so the provider cache still
// matches) plus the instruction as one appended user message. Tools stay
// declared - dropping them would change the prefix of every later request - and
// tool use is forbidden through ToolChoice where the provider honours it.
func (ru *handoffReuse) request(instruction string, maxTokens int) *agentcore.GenerateRequest {
	msgs := make([]agentcore.Message, 0, len(ru.req.Messages)+1)
	msgs = append(msgs, ru.req.Messages...)
	msgs = append(msgs, agentcore.Message{Role: "user", Content: instruction})
	req := &agentcore.GenerateRequest{
		Model:     ru.req.Model,
		System:    ru.req.System,
		Tools:     ru.req.Tools,
		Messages:  msgs,
		MaxTokens: maxTokens,
	}
	if chooser, ok := ru.provider.(agentcore.ToolChooser); ok && chooser.SupportsToolChoice() {
		req.ToolChoice = agentcore.ToolChoiceNone
	}
	return req
}

// reuseFitsWindow reports whether the reuse request plus the compaction reserve
// (the room a reply and the next calls need) fits the model's window. The reuse
// path sends the whole segment context instead of a size-capped transcript, so a
// closing segment near the window takes the old path.
func (r *Runner) reuseFitsWindow(req *agentcore.GenerateRequest) bool {
	window, reserve := r.compactLimits()
	need := tokensOfText(req.System) + tokensOfTools(req.Tools) + tokensOfMessages(req.Messages) + reserve
	return need <= window
}

// handoffBodyComplete reports whether body carries every section compactTemplate
// asks for. It is the reuse path's shape check: a reply that only answers in
// prose, or that stops after a few sections, is not a handoff package and the
// old path asks again. A section present with "无" counts.
func handoffBodyComplete(body string) bool {
	seen := make(map[string]bool, 8)
	for _, s := range splitHandoffSections(body) {
		if name, ok := handoffSectionName(s.heading); ok {
			seen[name] = true
		}
	}
	for _, name := range []string{handoffSectionTask, handoffSectionCoords, handoffSectionVerified,
		handoffSectionProgress, handoffSectionNext, handoffSectionRefs} {
		if !seen[name] {
			return false
		}
	}
	return true
}

// handoffAnswerReason names why an answer of the reuse path cannot become the
// handoff package ("" = it can): a reply that asks for tools went on working
// instead of writing the package, an empty reply says nothing, and a body
// without the template's sections is no package.
func handoffAnswerReason(body string, toolCalls int) string {
	switch {
	case toolCalls > 0:
		return handoffFallbackToolCall
	case strings.TrimSpace(body) == "":
		return handoffFallbackEmpty
	case !handoffBodyComplete(body):
		return handoffFallbackMissingSections
	}
	return ""
}

// reuseCall sends one handoff request on top of the segment's main-loop prefix
// and reports why the answer is unusable ("" = usable).
func (r *Runner) reuseCall(ctx context.Context, ru *handoffReuse, instruction string, maxTokens int) (body string, truncated bool, reason string) {
	req := ru.request(instruction, maxTokens)
	if !r.reuseFitsWindow(req) {
		return "", false, handoffFallbackWindow
	}
	// purposeCompact keeps these calls out of the main-loop prefix statistics
	// (and in the handoff bucket); the attr names the reuse path.
	text, calls, truncated, err := summaryRequest(withHandoffReuse(ctx, handoffReusePrefix), ru.provider, req)
	if err != nil {
		slog.Warn("handoff package on the segment prefix failed; asking the old way", "err", err)
		return "", false, handoffFallbackError
	}
	if why := handoffAnswerReason(text, calls); why != "" {
		slog.Warn("handoff package on the segment prefix unusable; asking the old way", "reason", why)
		return "", false, why
	}
	return text, truncated, ""
}

// handoffRequestHook keeps the request each main-loop call sends, for the handoff
// reuse of a segment break. It is the last hook of a turn: every hook that may
// still change the request (the deadline hint, the host hooks) ran before it, so
// the reuse call continues exactly what was sent.
type handoffRequestHook struct {
	agentcore.NopHook
	tracker *prefixTracker
}

func (h handoffRequestHook) BeforeModel(ctx context.Context, event *agentcore.ModelEvent, _ *agentcore.Control) error {
	if event != nil {
		h.tracker.observeRequest(observeScopeFrom(ctx).sessionID, event.Request)
	}
	return nil
}

// withHandoffReuse marks the handoff-package calls of this ctx with how they are
// made (the model event attr attrHandoffReuse).
func withHandoffReuse(ctx context.Context, value string) context.Context {
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, handoffReuseKey{}, value)
}

type handoffReuseKey struct{}

// handoffReuseFrom returns how the handoff call of this ctx is made ("" = the
// ctx belongs to no handoff-package call).
func handoffReuseFrom(ctx context.Context) string {
	v, _ := ctx.Value(handoffReuseKey{}).(string)
	return v
}
