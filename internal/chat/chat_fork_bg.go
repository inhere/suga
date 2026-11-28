package chat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// :bg (background fork design 0.5, D1): a read-only fork of this conversation
// started from the prompt instead of by the model. Everything behind it - the
// seed between turns, the task limit, the read-only whitelist and the notice -
// is the model trigger's own path: chat calls Options.StartFork, which ends in
// native.AgentTool.StartFork, so the two entries cannot drift apart. The task
// text is the fork's prompt; an empty one only prints the usage.

// bgCmd is the :bg command; its task text follows it.
const bgCmd = ":bg"

// forkDescriptionWords is how many words of a task label the fork in the command
// answers: the firstWords rule of the agent tool, which labels a call that has
// no description the same way.
const forkDescriptionWords = 6

// isBgCommand recognizes :bg with its task text.
func isBgCommand(line string) bool {
	name, _ := splitCommand(line)
	return name == bgCmd
}

// bgTaskArg is the task of a ":bg <任务>" line ("" when none was given, or when
// the line names another command).
func bgTaskArg(line string) string {
	name, arg := splitCommand(line)
	if name != bgCmd {
		return ""
	}
	return strings.TrimSpace(arg)
}

// forkTaskDescription labels a :bg task from its own text: up to six words, as
// the model's agent call is labeled when it carries no description.
func forkTaskDescription(task string) string {
	words := strings.Fields(task)
	if len(words) > forkDescriptionWords {
		words = words[:forkDescriptionWords]
	}
	return strings.Join(words, " ")
}

// runForkBg runs ":bg <任务>": a read-only fork of this conversation runs in the
// background and the prompt stays usable. A refusal is shown where chat shows
// command errors (the input line's own writer while steering is on): the task
// limit names the numbers to act on, a session that cannot provide a request
// says why.
func (s *chatState) runForkBg(task string) {
	tr := s.tr()
	task = strings.TrimSpace(task)
	if task == "" {
		fmt.Fprintln(s.source, tr.T("cmd.bg.usage"))
		return
	}
	if s.startFork == nil {
		fmt.Fprintln(s.source, tr.T("cmd.bg.disabled"))
		return
	}
	// :bg runs outside a turn, so nothing put the session on the context yet: the
	// task manager scopes the task by it and the parent Runner finds the session's
	// request through it.
	info, err := s.startFork(api.WithSessionID(s.ctx, s.sessionID), task)
	if err != nil {
		fmt.Fprintln(s.errWriter(), forkBgErrorText(tr, err))
		return
	}
	fmt.Fprintln(s.source, tr.T("cmd.bg.started", map[string]any{"id": info.ID, "desc": forkTaskDescription(task)}))
}

// forkBgErrorText is why a :bg fork did not start: the limit is the one answer
// that names what to do about it, a refusal carries its stable reason and, when
// the host translated one (a conversation too large to carry), that explanation
// on a line of its own. Anything else is shown as it is.
func forkBgErrorText(tr i18n.Translator, err error) string {
	var limit *native.ForkLimitError
	if errors.As(err, &limit) {
		return tr.T("cmd.bg.full", map[string]any{"running": limit.Running, "limit": limit.Limit})
	}
	var unavailable *personal.ForkUnavailableError
	if errors.As(err, &unavailable) {
		return bgUnavailableText(tr, unavailable.Reason, forkHintOf(err))
	}
	return formatCommandError(tr, bgCmd, err)
}

// forkHintOf is the explanation a refusal carries when it has one (the same
// optional interface the agent tool reads): a reason alone is a stable code and
// says nothing to a person.
func forkHintOf(err error) string {
	var hinted interface{ Hint() string }
	if errors.As(err, &hinted) {
		return hinted.Hint()
	}
	return ""
}

// bgUnavailableText renders a refusal of a fork that cannot start: the reason,
// plus the translated hint when the host has one (the reason alone is a stable
// code and says nothing to a person).
func bgUnavailableText(tr i18n.Translator, reason, hint string) string {
	text := tr.T("cmd.bg.unavailable", map[string]any{"reason": reason})
	if hint != "" {
		text += "\n" + hint
	}
	return text
}
