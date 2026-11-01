package personal

import (
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/i18n"
)

// Model-facing wording of a rewind that took the conversation back (catalog
// model.ini). The notice is queued like an undo notice and injected on the
// next turn.
const (
	modelRewindHeadConv     = "model.rewind.head.conversation"
	modelRewindHeadBoth     = "model.rewind.head.both"
	modelRewindRestored     = "model.rewind.restored"
	modelRewindDeleted      = "model.rewind.deleted"
	modelRewindSkipped      = "model.rewind.skipped"
	modelRewindUnrecovered  = "model.rewind.unrecoverable"
	modelRewindNoFiles      = "model.rewind.files_none"
	modelRewindMore         = "model.rewind.more"
	modelRewindDropped      = "model.rewind.dropped"
	modelRewindTailBoth     = "model.rewind.tail.both"
	modelRewindClose        = "model.rewind.close"
	modelRewindLabelPrior   = "model.rewind.label.prior"
	modelRewindLabelCurrent = "model.rewind.label.current"
)

// noticeKindRewind marks a queue item written by a conversation rewind.
const noticeKindRewind = "rewind"

// newRewindNotice builds the queue item of a conversation rewind (scope
// conversation or both). Counts and paths are the facts of this operation only.
func (r *Runner) newRewindNotice(plan RewindPlan, ut userTurn, st rewindFileStat, dropped int) pendingUndoNotice {
	undo := st.undoResult(plan.SessionID)
	return pendingUndoNotice{
		Kind:          noticeKindRewind,
		Text:          rewindNoticeText(r.cfg.Translator, plan.Scope, ut.Ordinal, st, dropped),
		Turns:         st.turns,
		Restored:      undo.Restored,
		Deleted:       undo.Deleted,
		Skipped:       undo.Skipped,
		Unrecoverable: undo.Unrecoverable,
		Files:         undo.Files,
	}
}

// rewindNoticeText states what the rewind did. For scope both it spells out
// the files that were restored or deleted (counts and paths) next to the ones
// that were skipped or could not be recovered, so the model neither re-does
// restored work nor trusts a file that is still in the changed state.
func rewindNoticeText(tr i18n.Translator, scope RewindScope, ordinal int, st rewindFileStat, dropped int) string {
	t := modelTr(tr)
	var parts []string
	if scope == RewindScopeBoth {
		parts = append(parts, fmt.Sprintf(t.T(modelRewindHeadBoth), ordinal))
		before := len(parts)
		parts = appendRewindPart(parts, t, modelRewindRestored, st.restored)
		parts = appendRewindPart(parts, t, modelRewindDeleted, st.deleted)
		parts = appendRewindPart(parts, t, modelRewindSkipped, st.skipped)
		parts = appendRewindPart(parts, t, modelRewindUnrecovered, st.unrecoverable)
		if len(parts) == before {
			parts = append(parts, t.T(modelRewindNoFiles))
		}
	} else {
		parts = append(parts, fmt.Sprintf(t.T(modelRewindHeadConv), ordinal))
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf(t.T(modelRewindDropped), dropped))
	}
	if scope == RewindScopeBoth {
		parts = append(parts, t.T(modelRewindTailBoth))
	}
	return "[" + strings.Join(parts, "\n") + t.T(modelRewindClose)
}

// appendRewindPart adds "<n> files ...: a, b, ... (and k more)" for one outcome
// class; an empty class adds nothing. The listing is capped like an undo notice.
func appendRewindPart(parts []string, t i18n.Translator, key string, paths []string) []string {
	if len(paths) == 0 {
		return parts
	}
	shown := paths
	if len(shown) > undoNoticeTextFiles {
		shown = shown[:undoNoticeTextFiles]
	}
	text := fmt.Sprintf(t.T(key), len(paths), strings.Join(shown, ", "))
	if len(paths) > len(shown) {
		text += " " + fmt.Sprintf(t.T(modelRewindMore), len(paths)-len(shown))
	}
	return append(parts, text)
}

// mergeRewindTexts puts the bodies of superseded, undelivered rewind notices in
// front of the new one so one injection carries both operations in order. Each
// earlier body is labelled as an earlier rewind and the new one as this one; a
// body that is already such a chain has its last "this" label turned into
// "earlier" first, so any number of rewinds read in order. No upper bound.
func mergeRewindTexts(tr i18n.Translator, superseded []string, current string) string {
	if len(superseded) == 0 {
		return current
	}
	t := modelTr(tr)
	prior, cur := t.T(modelRewindLabelPrior)+"\n", t.T(modelRewindLabelCurrent)+"\n"
	chunks := make([]string, 0, len(superseded)+1)
	for _, old := range superseded {
		if strings.HasPrefix(old, prior) {
			if i := strings.LastIndex(old, "\n\n"+cur); i >= 0 {
				old = old[:i] + "\n\n" + prior + old[i+len("\n\n"+cur):]
			}
			chunks = append(chunks, old)
			continue
		}
		chunks = append(chunks, prior+old)
	}
	return strings.Join(chunks, "\n\n") + "\n\n" + cur + current
}
