package chat

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// tasksCmd is the :tasks command; its options follow it.
const tasksCmd = ":tasks"

// chatTasksRecent caps the finished tasks the default :tasks listing shows.
const chatTasksRecent = 10

const (
	chatUsageHintKey  = "cmd.usage.stats"
	chatTasksEmptyKey = "cmd.tasks.empty"
)

// BuildChatHelp returns the complete local-command and keyboard help text.
func BuildChatHelp(tr i18n.Translator) string { return buildChatHelp(tr) }

// BuildChatUsage renders process and session usage using the same counters as the exit summary.
func BuildChatUsage(tr i18n.Translator, stats chatStats) string {
	return tr.T(chatUsageHintKey, map[string]any{
		"turns":          stats.turns,
		"input":          stats.usage.InputTokens,
		"output":         stats.usage.OutputTokens,
		"cache_read":     stats.usage.CacheReadTokens,
		"cache_write":    stats.usage.CacheWriteTokens,
		"cost":           fmt.Sprintf("$%.6f", stats.usage.CostUSD),
		"session_input":  stats.session.InputTokens,
		"session_output": stats.session.OutputTokens,
		"session_cost":   fmt.Sprintf("$%.6f", stats.session.CostUSD),
	})
}

// BuildChatTasks renders the task manager's snapshot for :tasks: by default the
// running tasks plus the 10 most recently active finished ones and how many are
// left; showAll lists every task in start order.
func BuildChatTasks(tr i18n.Translator, items []tasks.Info, now time.Time, showAll bool) string {
	if len(items) == 0 {
		return tr.T(chatTasksEmptyKey)
	}
	if showAll {
		lines := make([]string, 0, len(items))
		for _, item := range items {
			lines = append(lines, chatTaskLine(tr, item, now))
		}
		return strings.Join(lines, "\n")
	}
	var running, finished []tasks.Info
	for _, item := range items {
		if item.Status == tasks.Running {
			running = append(running, item)
		} else {
			finished = append(finished, item)
		}
	}
	// Most recently active first: the end time, or the start of a task without one.
	sort.SliceStable(finished, func(i, j int) bool {
		return taskLastActivity(finished[i]).After(taskLastActivity(finished[j]))
	})
	hidden := len(finished) - chatTasksRecent
	shown := make([]tasks.Info, 0, len(running)+len(finished))
	shown = append(shown, running...)
	if hidden > 0 {
		shown = append(shown, finished[:chatTasksRecent]...)
	} else {
		shown = append(shown, finished...)
	}
	lines := make([]string, 0, len(shown)+1)
	for _, item := range shown {
		lines = append(lines, chatTaskLine(tr, item, now))
	}
	if hidden > 0 {
		lines = append(lines, tr.T("cmd.tasks.more", map[string]any{"count": hidden}))
	}
	return strings.Join(lines, "\n")
}

// chatTaskLine is one task of the :tasks listing.
func chatTaskLine(tr i18n.Translator, item tasks.Info, now time.Time) string {
	return tr.T("cmd.tasks.line", map[string]any{
		"id":      termtext.Clean(item.ID),
		"status":  termtext.Clean(string(item.Status)),
		"summary": termtext.Clean(item.Summary),
		"elapsed": formatElapsedAt(item.Duration(), now),
		"last":    taskLastActivity(item).Local().Format("15:04:05"),
		"kind":    termtext.Clean(item.Kind),
	})
}

// taskLastActivity is when the task last did something: its end, or the start of
// a task that is still running.
func taskLastActivity(item tasks.Info) time.Time {
	if !item.Ended.IsZero() {
		return item.Ended
	}
	return item.Started
}

func formatElapsedAt(d time.Duration, _ time.Time) string { return formatElapsed(d) }

// isTasksCommand recognizes :tasks with its options.
func isTasksCommand(line string) bool {
	return line == tasksCmd || strings.HasPrefix(line, tasksCmd+" ")
}

// runTasksCommand runs the :tasks command: the default view (running tasks and
// the most recently active finished ones), -a for every task, -k <id>... to kill
// and -h for the usage.
func (s *chatState) runTasksCommand(line string) {
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, tasksCmd)))
	showAll := false
	var kills []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "-h", "--help":
			fmt.Fprintln(s.source, commandUsageText(s.tr(), tasksCmd))
			return
		case "-a":
			showAll = true
		case "-k":
			// Everything after -k is a task id, so several can be killed at once.
			kills = append(kills, args[i+1:]...)
			i = len(args)
		default:
			fmt.Fprintln(s.source, s.tr().T("cmd.unknown.arg", map[string]any{"arg": arg}))
			fmt.Fprintln(s.source, commandUsageText(s.tr(), tasksCmd))
			return
		}
	}
	if len(kills) > 0 {
		s.killTasks(kills)
		return
	}
	if s.tasks == nil {
		fmt.Fprintln(s.source, s.tr().T(chatTasksEmptyKey))
		return
	}
	fmt.Fprintln(s.source, BuildChatTasks(s.tr(), s.tasks.List(s.sessionID), time.Now(), showAll))
}

// killTasks kills the named background tasks and says what became of each: a
// typo and a task that had already ended read differently.
func (s *chatState) killTasks(ids []string) {
	if s.tasks == nil {
		fmt.Fprintln(s.source, s.tr().T(chatTasksEmptyKey))
		return
	}
	killed := false
	// The state before the kill decides the answer: Manager.Kill on a task that
	// already ended returns its final snapshot without an error, so a second
	// ":tasks -k" would otherwise report "已终止" again.
	before := make(map[string]tasks.Info)
	for _, info := range s.tasks.List(s.sessionID) {
		before[info.ID] = info
	}
	for _, id := range ids {
		if prev, ok := before[id]; ok && prev.Status != tasks.Running {
			fmt.Fprintln(s.source, s.tr().T("cmd.tasks.kill.ended", map[string]any{"id": id, "status": string(prev.Status)}))
			continue
		}
		info, err := s.tasks.Kill(s.sessionID, id)
		switch {
		case errors.Is(err, tasks.ErrUnknownTask):
			fmt.Fprintln(s.source, s.tr().T("cmd.tasks.kill.unknown", map[string]any{"id": id}))
		case err != nil:
			fmt.Fprintln(s.source, s.tr().T("cmd.tasks.kill.failed", map[string]any{"id": id, "error": err.Error()}))
		case info.Status != tasks.Killed:
			fmt.Fprintln(s.source, s.tr().T("cmd.tasks.kill.ended", map[string]any{"id": id, "status": string(info.Status)}))
		default:
			killed = true
			fmt.Fprintln(s.source, s.tr().T("cmd.tasks.kill.ok", map[string]any{"id": id, "summary": truncateLine(info.Summary, resultSummaryLimit)}))
		}
	}
	if killed && s.render != nil {
		// A killed background agent leaves the status line's running count now: Kill
		// suppresses the task's notice, so no event would tell the line it ended.
		s.render.refreshAgentStatus()
	}
}

// isImageCommand recognizes the :image command (chat :image, design 0.3): no
// argument lists the pending pictures, "clear" drops them, a path attaches one
// (built with the view_image pipeline; the same rules reject the file).
func isImageCommand(line string) bool {
	return line == ":image" || strings.HasPrefix(line, ":image ")
}

// maxPendingImages bounds the wait list: a pile of :image lines must not hold
// whole directories in memory, and one request carries at most
// images.max_request_count pictures anyway.
const maxPendingImages = 16

// attachImage loads one picture with the view_image rules and queues it for
// the next message; the note "[已附图 N：宽×高 格式]" shows above the input line.
func (s *chatState) attachImage(arg string) {
	out := s.errWriter()
	if s.readImage == nil {
		fmt.Fprintln(out, s.tr().T("cmd.image.unavailable"))
		return
	}
	if !s.canSeeImages() {
		fmt.Fprintln(out, s.tr().T("cmd.image.vision", map[string]any{"model": s.modelInfo.Ref()}))
		return
	}
	if len(s.pendingImages) >= maxPendingImages {
		fmt.Fprintln(out, s.tr().T("cmd.image.limit", map[string]any{"max": maxPendingImages}))
		return
	}
	part, _, err := s.readImage(arg)
	if err != nil {
		fmt.Fprintln(out, s.tr().T("cmd.image.read_failed", map[string]any{"error": err.Error()}))
		return
	}
	s.pendingImages = append(s.pendingImages, part)
	fmt.Fprintln(out, s.tr().T("cmd.image.attached", map[string]any{
		"count": len(s.pendingImages), "width": part.Width, "height": part.Height, "type": part.MediaType,
	}))
}

// attachFromClipboard is the Alt+V paste (design 0.3): one clipboard picture
// on the same queue as :image. The reader speaks for itself — no picture
// reports "剪贴板里没有图片", a missing reader says what to install.
func (s *chatState) attachFromClipboard() {
	out := s.errWriter()
	if s.readClip == nil {
		fmt.Fprintln(out, s.tr().T("cmd.image.clipboard_unavailable"))
		return
	}
	if !s.canSeeImages() {
		fmt.Fprintln(out, s.tr().T("cmd.image.vision", map[string]any{"model": s.modelInfo.Ref()}))
		return
	}
	if len(s.pendingImages) >= maxPendingImages {
		fmt.Fprintln(out, s.tr().T("cmd.image.limit", map[string]any{"max": maxPendingImages}))
		return
	}
	part, _, err := s.readClip()
	if err != nil {
		fmt.Fprintln(out, s.tr().T("cmd.image.clipboard_failed", map[string]any{"error": err.Error()}))
		return
	}
	s.pendingImages = append(s.pendingImages, part)
	fmt.Fprintln(out, s.tr().T("cmd.image.attached", map[string]any{
		"count": len(s.pendingImages), "width": part.Width, "height": part.Height, "type": part.MediaType,
	}))
}

// takePendingImages hands the queued images to the message being sent and
// clears the queue.
func (s *chatState) takePendingImages() []agentcore.ImagePart {
	images := s.pendingImages
	s.pendingImages = nil
	return images
}

// canSeeImages reports whether the current model accepts images (nil: yes).
func (s *chatState) canSeeImages() bool {
	return s.vision == nil || s.vision.CanSee()
}

// isModelCommand recognizes the model command and the checkpoint commands that
// share the prompt dispatch branch in chat.go.
func isModelCommand(line string) bool {
	return line == ":model" || strings.HasPrefix(line, ":model ") || isCheckpointCommand(line) || isUndoCommand(line)
}

func modelRefs(options []ModelOption) []string {
	refs := make([]string, 0, len(options))
	for _, option := range options {
		refs = append(refs, option.Ref())
	}
	return refs
}

// BuildChatModel renders the current effective model and the configured choices.
func BuildChatModel(tr i18n.Translator, current ModelOption, options []ModelOption) string {
	var b strings.Builder
	b.WriteString(tr.T("cmd.model.current", map[string]any{
		"model":      current.Ref(),
		"thinking":   displayThinking(tr, current.Thinking),
		"window":     current.ContextWindow,
		"max_output": current.MaxOutputTokens,
	}))
	if len(options) == 0 {
		b.WriteString(tr.T("cmd.model.none"))
		return b.String()
	}
	for _, option := range options {
		b.WriteByte('\n')
		marker := "  "
		if option.Ref() == current.Ref() {
			marker = "* "
		}
		b.WriteString(marker + option.Ref())
	}
	return b.String()
}

func displayThinking(tr i18n.Translator, level string) string {
	if level == "" {
		return tr.T("cmd.model.thinking.default")
	}
	return level
}

// runChatCommand handles commands typed at this terminal. The caller checks injected first.
func (s *chatState) runChatCommand(line string) bool {
	// A help ask (":queue -h", ":queue --help") is answered from the table of
	// chat_usage.go before the command itself looks at its line.
	if reply := commandUsageReply(s.tr(), line, s.dirs != nil); reply != "" {
		fmt.Fprintln(s.source, reply)
		return true
	}
	switch {
	case line == ":help" || strings.HasPrefix(line, ":help "):
		// :help alone lists every command, :help <命令> shows one command's usage.
		fmt.Fprintln(s.source, chatHelpFor(s.tr(), strings.TrimPrefix(line, ":help")))
	case isModelCommand(line) && !isCheckpointCommand(line) && !isUndoCommand(line):
		arg := strings.TrimSpace(strings.TrimPrefix(line, ":model"))
		if arg == "" {
			fmt.Fprintln(s.source, BuildChatModel(s.tr(), s.modelInfo, s.modelOptions))
			return true
		}
		if s.modelSwitch == nil {
			fmt.Fprintln(s.source, s.tr().T("cmd.model.switch.unavailable"))
			return true
		}
		info, err := s.modelSwitch(s.ctx, s.sessionID, arg)
		if err != nil {
			fmt.Fprintln(s.source, s.tr().T("cmd.model.switch.failed", map[string]any{"error": err.Error()}))
			return true
		}
		s.modelInfo = info
		s.render.SetModel(info.Provider, info.Model, info.Thinking, info.ContextWindow)
		fmt.Fprintln(s.source, s.tr().T("cmd.model.switch.ok", map[string]any{"model": info.Ref()}))
	case isImageCommand(line):
		out := s.errWriter()
		arg := strings.TrimSpace(strings.TrimPrefix(line, ":image"))
		switch {
		case arg == "" && len(s.pendingImages) == 0:
			fmt.Fprintln(out, s.tr().T("cmd.image.none"))
		case arg == "":
			for i, part := range s.pendingImages {
				fmt.Fprintln(out, s.tr().T("cmd.image.line", map[string]any{
					"index": i + 1, "width": part.Width, "height": part.Height, "type": part.MediaType,
				}))
			}
		case arg == "clear":
			s.pendingImages = nil
			fmt.Fprintln(out, s.tr().T("cmd.image.cleared"))
		default:
			s.attachImage(arg)
		}
	case line == ":usage":
		fmt.Fprintln(s.source, BuildChatUsage(s.tr(), *s.stats))
	case isSkillsCommand(line):
		// :skills [--only-names] [-n N]: the skills this session loaded.
		s.runSkillsCommand(line)
	case isContextCommand(line):
		// :context [--files]: what the current context is made of.
		s.runContextCommand(line)
	case isQueueCommand(line):
		// The messages queued with Tab during a turn (followup.go).
		s.runQueueCommand(line)
	case isTasksCommand(line):
		s.runTasksCommand(line)
	case isCheckpointCommand(line):
		// :checkpoints [N]: N keeps the listing to the last N turns.
		s.runCheckpoints(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), checkpointsCmd)))
	case isUndoCommand(line):
		command, ok := parseUndoCommand(line)
		if !ok {
			fmt.Fprintln(s.source, commandUsageText(s.tr(), undoCmd))
		} else {
			s.runUndo(command)
		}
	default:
		return false
	}
	return true
}
