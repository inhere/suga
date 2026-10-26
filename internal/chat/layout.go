package chat

// layout.go builds every line of text chat shows around a session, so the
// wording, the order and the styles of those three moments can be changed in
// this one file:
//
//   - startup: BannerInfo/BuildBanner is the banner Renderer.Banner prints once
//     chat.go has the session's short name and before the first prompt (after a
//     resume, the preview built by LastReplyInfo/BuildLastReplyNotices sits
//     between the two);
//   - end of turn: TurnStatus/BuildTurnStatus is what Renderer.Status prints
//     after each finished turn - the memory lines of the turn, then the status
//     line;
//   - exit: ExitSummary/BuildExitSummary is what chatStats.print writes to
//     stderr on the way out, below the prompt (its own blank line first).
//
// A builder returns []LayoutLine - the text and the color it is painted in -
// and therefore owns the styles too; whether colors are written at all stays
// with the caller (a pipe, a redirected output or NO_COLOR prints the plain
// text) and the caller also writes the lines out. Data that is not part of a
// line on screen stays with the caller: the resume preview's reply body and the
// terminal title, which go through the streaming/markdown renderer and their
// own writer.
//
// Making the layout configurable later means letting one of the builders take
// its text from a chat.layout.* setting or a text/template: the fields above
// are the values a custom format would have to offer.
//
// Not part of these three moments, so they stay where they are: the
// "session id: ..." line chat.go writes to stderr before the banner, the line
// `:session` shows (sessionLine in session_name.go) and the terminal title
// (terminal_title.go).

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/gookit/color"
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
)

// colorNone marks a LayoutLine without a style: it is printed plain.
const colorNone color.Color = color.OpReset

// LayoutLine is one line of chat's layout: its text and the color it is painted
// in (colorNone: plain text).
type LayoutLine struct {
	Text  string
	Color color.Color
}

// Paint returns the line as it is written: the text as it is, or wrapped in the
// line's color when the output takes colors (a pipe, a redirected output or
// NO_COLOR prints the plain text).
func (l LayoutLine) Paint(colorOn bool) string {
	if !colorOn || l.Color == colorNone {
		return l.Text
	}
	return l.Color.Text(l.Text)
}

// printLayout writes the lines of one layout block, one per line, on w.
func printLayout(w io.Writer, colorOn bool, lines []LayoutLine) {
	for _, line := range lines {
		fmt.Fprintln(w, line.Paint(colorOn))
	}
}

// BannerInfo is everything the startup banner shows.
type BannerInfo struct {
	// Provider and Model label the head line; either may be empty.
	Provider, Model string
	// Thinking is the effective thinking level ("" = the provider's default)
	// and ContextWindow the model's token window (0 = unknown).
	Thinking      string
	ContextWindow int
	// SessionName is the session's short name ("<workspace directory>-k7", the
	// address `suga send --session <name>` accepts); empty while it is unknown,
	// which keeps it off the head line.
	SessionName string
	// WriteTools selects the tool line: write and shell available, or read only.
	// Translator is the host UI catalog; nil preserves legacy Chinese output.
	Translator i18n.Translator
	WriteTools bool
}

// BuildBanner builds the chat header: the model in use, the commands, and
// whether the tools can write.
func BuildBanner(info BannerInfo) []LayoutLine {
	t := trOf(info.Translator)
	head := t.T("status.banner.title")
	if label := modelLabel(info.Provider, info.Model); label != "" {
		head += " · " + label
	}
	if info.Thinking != "" {
		head += " · thinking " + info.Thinking
	}
	if info.ContextWindow > 0 {
		head += " · ctx " + formatTokens(info.ContextWindow)
	}
	if info.SessionName != "" {
		head += " · " + t.T("status.banner.session", map[string]any{"name": info.SessionName})
	}
	tools := t.T("status.banner.read_only")
	if info.WriteTools {
		tools = t.T("status.banner.write_tools")
	}
	return []LayoutLine{
		{Text: head, Color: color.Cyan},
		{Text: t.T("status.banner.help"), Color: color.Gray},
		{Text: tools, Color: color.Gray},
	}
}

// LastReplyInfo is what the preview of a continued session's last reply shows
// besides the reply itself: the label above it and the two notes about it.
type LastReplyInfo struct {
	// Incomplete notes that the previous turn did not end normally.
	Incomplete bool
	// Omitted is how many leading lines of a long reply are not shown (0: the
	// whole reply is); a positive count adds the hint naming the trace command.
	Omitted int
	// SessionID only names the trace command of that hint.
	SessionID string
	// Translator is the host UI catalog; nil preserves legacy Chinese output.
	Translator i18n.Translator
}

// BuildLastReplyNotices builds the lines chat prints above the last reply of a
// session it continues: a dim label first, so the reply is not taken for a
// fresh answer, then the notes about that reply. The reply body is not part of
// them: the caller streams it like an answer (marker, markdown).
func BuildLastReplyNotices(info LastReplyInfo) []LayoutLine {
	t := trOf(info.Translator)
	lines := []LayoutLine{{Text: t.T("status.last_reply.label"), Color: color.Gray}}
	if info.Incomplete {
		lines = append(lines, LayoutLine{Text: t.T("status.last_reply.incomplete"), Color: color.Gray})
	}
	if info.Omitted > 0 {
		lines = append(lines, LayoutLine{
			Text:  t.T("status.last_reply.truncated", map[string]any{"lines": info.Omitted, "session": info.SessionID}),
			Color: color.Gray,
		})
	}
	return lines
}

// TurnStatus is everything the end-of-turn status shows.
type TurnStatus struct {
	// Memory lists this turn's successful memory writes, one line each, shown
	// above the status line.
	Memory []string
	// Provider and Model label the line; either may be empty.
	Provider, Model string
	// Usage is what this turn's model calls used.
	Usage agentcore.Usage
	// Budget is the latest budget observation of this turn. It is nil when no
	// budget is configured, preserving the historical status line byte-for-byte.
	Budget *personal.BudgetSnapshot
	// LastInputTokens is the input token count (cache included) of the turn's
	// last model request and ContextWindow is the model's window: together they
	// give the "ctx 55%" part (either unknown keeps it out).
	LastInputTokens, ContextWindow int
	// Elapsed is how long the turn took.
	Elapsed time.Duration
	// EndTime is the local time the turn ended at, shown as HH:MM.
	EndTime time.Time
	// Steers is how many inserted messages a model request of the turn carried
	// (the delivered ones; a message taken back with Up, or one the turn never
	// took, is not counted); 0 keeps that part out of the line.
	Steers int
	// Truncated marks an answer the model cut off at the output token limit
	// (personal.RunResult.Truncated); OutputLimit is that limit in tokens
	// (0 = neither the configuration nor the provider named one).
	Truncated bool
	// Translator is the host UI catalog; nil preserves legacy Chinese output.
	Translator  i18n.Translator
	OutputLimit int
}

// BuildTurnStatus builds the end of a turn: one green line per memory write of
// the turn, then the grey status line (model, token usage, how full the context
// is, elapsed time, the local time the turn ended at, inserted messages).
func BuildTurnStatus(s TurnStatus) []LayoutLine {
	t := trOf(s.Translator)
	lines := make([]LayoutLine, 0, len(s.Memory)+1)
	for _, memory := range s.Memory {
		lines = append(lines, LayoutLine{Text: memory, Color: color.Green})
	}
	var parts []string
	if label := modelLabel(s.Provider, s.Model); label != "" {
		parts = append(parts, label)
	}
	parts = append(parts,
		fmt.Sprintf("in %d / out %d tokens", s.Usage.InputTokens, s.Usage.OutputTokens),
	)
	// How full the context is: the same number the idle consolidation compares
	// with context.idle_min_ratio, so the person can tell when it will fire.
	if share := output.ContextShare(s.LastInputTokens, s.ContextWindow); share != "" {
		parts = append(parts, share)
	}
	parts = append(parts,
		formatElapsed(s.Elapsed),
		// When the turn ended, so the person can tell long sessions apart (:03).
		clockTime(s.EndTime))
	// Inserted messages (steering) only, so the line is unchanged for a turn
	// nobody typed into.
	if s.Steers > 0 {
		parts = append(parts, t.T("status.turn.steers", map[string]any{"count": s.Steers}))
	}
	// Keep budget at the end of the ordinary status fields, as a persistent
	// meter; a wrap-up reason or truncation note follows it when present.
	if budget := budgetStatus(s.Budget); budget != "" {
		parts = append(parts, budget)
	}
	if wrapUp := budgetWrapUpStatus(t, s.Budget); wrapUp != "" {
		parts = append(parts, wrapUp)
	}
	// An answer cut off at the output token limit: the person must not read it as
	// a finished one, and the limit names the setting that can raise it.
	if s.Truncated {
		if s.OutputLimit > 0 {
			parts = append(parts, t.T("status.turn.truncated_limit", map[string]any{"limit": s.OutputLimit}))
		} else {
			parts = append(parts, t.T("status.turn.truncated"))
		}
	}
	return append(lines, LayoutLine{Text: strings.Join(parts, " · "), Color: color.Gray})
}

// budgetShare returns the largest configured dimension's percentage. Showing
// the largest ratio keeps the line useful when both token and cost limits exist.
func budgetShare(b *personal.BudgetSnapshot) (int, bool, bool) {
	if b == nil {
		return 0, false, false
	}
	ratio := -1.0
	for _, metric := range []personal.BudgetMetric{b.Tokens, b.CostUSD} {
		if metric.Limit <= 0 {
			continue
		}
		r := metric.Used / metric.Limit
		if r > ratio {
			ratio = r
		}
	}
	if ratio < 0 {
		return 0, false, false
	}
	ratio = math.Min(math.Max(ratio, 0), 1)
	return int(ratio * 100), b.Estimated, true
}

func budgetStatus(b *personal.BudgetSnapshot) string {
	share, estimated, ok := budgetShare(b)
	if !ok {
		return ""
	}
	prefix := ""
	if estimated {
		prefix = "~"
	}
	name := "budget"
	if b.Scope == "session" {
		name = "session budget"
	}
	return fmt.Sprintf("%s %s%d%%", name, prefix, share)
}

// budgetWrapUpStatus names why the turn wrapped up: the numbers themselves are
// formatted here (they are values, not text) and the sentence comes from t.
func budgetWrapUpStatus(t i18n.Translator, b *personal.BudgetSnapshot) string {
	if b == nil || !b.Exceeded {
		return ""
	}
	prefix := ""
	if b.Estimated {
		prefix = "~"
	}
	switch b.Reason {
	case "max_cost_usd":
		usage := fmt.Sprintf("%s$%.4f/$%.4f", prefix, b.CostUSD.Limit, b.CostUSD.Used)
		return trOf(t).T("status.budget.wrapup_cost", map[string]any{"usage": usage})
	case "max_tokens":
		usage := fmt.Sprintf("%s%.0f/%s%.0f", prefix, b.Tokens.Limit, prefix, b.Tokens.Used)
		return trOf(t).T("status.budget.wrapup_tokens", map[string]any{"usage": usage})
	default:
		return trOf(t).T("status.budget.wrapup")
	}
}

// budgetNoticeText is deliberately short: it may be printed while the model
// is streaming, while the detailed limit and usage remain on the final status.
func budgetNoticeText(t i18n.Translator, b *personal.BudgetSnapshot) string {
	share, estimated, ok := budgetShare(b)
	if !ok {
		return ""
	}
	prefix := ""
	if estimated {
		prefix = "~"
	}
	name := trOf(t).T("status.budget.name")
	if b.Scope == "session" {
		name = trOf(t).T("status.budget.name_session")
	}
	return trOf(t).T("status.budget.notice", map[string]any{
		"name": name, "share": fmt.Sprintf("%s%d%%", prefix, share),
	})
}

// ExitSummary is everything the exit summary shows.
type ExitSummary struct {
	// SessionID is the session `suga chat --resume <id>` continues; an empty one
	// means the summary is not printed at all.
	SessionID string
	// ShortName is the session's short name (the address `suga send --session
	// <name>` accepts), shown before the resume command; empty keeps it out.
	ShortName string
	// Turns and Usage are what this chat process ran; SessionUsage is the
	// session's cumulative usage after the last turn (its token counts 0:
	// unknown, keeps that part out).
	Turns        int
	Usage        agentcore.Usage
	SessionUsage agentcore.Usage
	// Elapsed is how long this chat process ran.
	Elapsed time.Duration
	// Translator is the host UI catalog; nil preserves legacy Chinese output.
	Translator i18n.Translator
}

// BuildExitSummary builds the lines chat leaves behind when it ends: the resume
// command (behind the session's short name), this process's turns and tokens
// (plus the session total when known) and its running time. A chat without a
// session builds no lines.
func BuildExitSummary(s ExitSummary) []LayoutLine {
	if s.SessionID == "" {
		return nil
	}
	t := trOf(s.Translator)
	line := t.T("status.exit.turns", map[string]any{
		"turns": s.Turns, "input": s.Usage.InputTokens, "output": s.Usage.OutputTokens,
	})
	if s.SessionUsage.InputTokens > 0 || s.SessionUsage.OutputTokens > 0 {
		line += t.T("status.exit.session_total", map[string]any{
			"input": s.SessionUsage.InputTokens, "output": s.SessionUsage.OutputTokens,
		})
	}
	// The short name goes before the resume command, which ends the line so it
	// can be copied as it is (a name glued after the id broke the command). The
	// command itself is never translated.
	name := ""
	if s.ShortName != "" {
		name = t.T("status.exit.session_name", map[string]any{"name": s.ShortName}) + " · "
	}
	// Leading empty line: Ctrl-C/Ctrl-D at the prompt leaves the cursor after "❯ ".
	return []LayoutLine{
		{},
		{Text: line + " · " + t.T("status.exit.elapsed", map[string]any{"elapsed": formatElapsed(s.Elapsed)})},
		{Text: name + t.T("status.exit.resume", map[string]any{"cmd": "suga chat --resume " + s.SessionID})},
	}
}
