package chat

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

// tasksCmd is the :tasks command; its options follow it.
const tasksCmd = ":tasks"

// chatTasksRecent caps the finished tasks the default :tasks listing shows.
const chatTasksRecent = 10

const (
	chatHelpText = `:session 显示会话
:compact 压缩上下文
:add-dir/:dirs/:rm-dir 管理附加目录
:diff [N] 显示文件改动
:code [N] 展开上一条回答的代码块
:image <路径|clear> 管理待发图片（附到下一条消息）
:queue [clear] 列出/清空排队的消息（回合中 Tab 排队，本轮结束后发送）
:checkpoints 列出写操作快照
:undo [N] [force] 回滚文件改动
:help 显示此帮助
:usage 显示本会话用量与成本
:tasks [-a|-k <ID>] 后台任务：运行中+最近结束（-a 全部，-k 终止）
:quit/:exit 退出
!<命令> 本地执行；!!<命令> 执行并附给下一条消息
按键：Enter/Alt+Enter/Ctrl+J 提交/换行；Tab 补全命令（回合中 Tab 把该行排到本轮结束后发送）；Alt+V 贴剪贴板图片；Esc 中止本轮；Ctrl+C 清空/中止/退出；回合中 Up 撤回插话；Up/Down 浏览历史`
	chatUsageHint  = ":usage 用量：本次 %d 轮 · 输入 %d / 输出 %d · 缓存读 %d / 写 %d · 成本 $%.6f；会话累计输入 %d / 输出 %d · 成本 $%.6f"
	chatTasksEmpty = "没有后台任务"
	chatTasksUsage = `用法：:tasks [选项]
  :tasks          列出运行中的任务和最近 10 条已结束任务
  :tasks -a       列出全部任务（含已结束的）
  :tasks -k <ID>  终止后台任务，可写多个：:tasks -k t3 t4
  :tasks -h       显示此帮助
示例：:tasks -k t2`
)

// BuildChatHelp returns the complete local-command and keyboard help text.
func BuildChatHelp() string { return chatHelpText }

// BuildChatUsage renders process and session usage using the same counters as the exit summary.
func BuildChatUsage(stats chatStats) string {
	return fmt.Sprintf(chatUsageHint, stats.turns, stats.usage.InputTokens, stats.usage.OutputTokens,
		stats.usage.CacheReadTokens, stats.usage.CacheWriteTokens, stats.usage.CostUSD,
		stats.session.InputTokens, stats.session.OutputTokens, stats.session.CostUSD)
}

// BuildChatTasks renders the task manager's snapshot for :tasks: by default the
// running tasks plus the 10 most recently active finished ones and how many are
// left; showAll lists every task in start order.
func BuildChatTasks(items []tasks.Info, now time.Time, showAll bool) string {
	if len(items) == 0 {
		return chatTasksEmpty
	}
	if showAll {
		lines := make([]string, 0, len(items))
		for _, item := range items {
			lines = append(lines, chatTaskLine(item, now))
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
		lines = append(lines, chatTaskLine(item, now))
	}
	if hidden > 0 {
		lines = append(lines, fmt.Sprintf("还有 %d 条已结束任务，:tasks -a 查看全部", hidden))
	}
	return strings.Join(lines, "\n")
}

// chatTaskLine is one task of the :tasks listing.
func chatTaskLine(item tasks.Info, now time.Time) string {
	return fmt.Sprintf("%s · %s · %s · 已运行 %s · 最近输出 %s · %s",
		termtext.Clean(item.ID), termtext.Clean(string(item.Status)), termtext.Clean(item.Summary),
		formatElapsedAt(item.Duration(), now), taskLastActivity(item).Local().Format("15:04:05"), termtext.Clean(item.Kind))
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
			fmt.Fprintln(s.source, chatTasksUsage)
			return
		case "-a":
			showAll = true
		case "-k":
			// Everything after -k is a task id, so several can be killed at once.
			kills = append(kills, args[i+1:]...)
			i = len(args)
		default:
			fmt.Fprintln(s.source, "未知参数："+arg)
			fmt.Fprintln(s.source, chatTasksUsage)
			return
		}
	}
	if len(kills) > 0 {
		s.killTasks(kills)
		return
	}
	if s.tasks == nil {
		fmt.Fprintln(s.source, chatTasksEmpty)
		return
	}
	fmt.Fprintln(s.source, BuildChatTasks(s.tasks.List(s.sessionID), time.Now(), showAll))
}

// killTasks kills the named background tasks and says what became of each: a
// typo and a task that had already ended read differently.
func (s *chatState) killTasks(ids []string) {
	if s.tasks == nil {
		fmt.Fprintln(s.source, chatTasksEmpty)
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
			fmt.Fprintf(s.source, "任务 %s 已结束（%s），无需终止\n", id, prev.Status)
			continue
		}
		info, err := s.tasks.Kill(s.sessionID, id)
		switch {
		case errors.Is(err, tasks.ErrUnknownTask):
			fmt.Fprintf(s.source, "没有任务 %s\n", id)
		case err != nil:
			fmt.Fprintf(s.source, "终止任务 %s 失败：%s\n", id, err.Error())
		case info.Status != tasks.Killed:
			fmt.Fprintf(s.source, "任务 %s 已结束（%s），无需终止\n", id, info.Status)
		default:
			killed = true
			fmt.Fprintf(s.source, "已终止任务 %s：%s\n", id, truncateLine(info.Summary, resultSummaryLimit))
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
		fmt.Fprintln(out, "图片附件在本进程不可用")
		return
	}
	if !s.canSeeImages() {
		fmt.Fprintf(out, "当前模型 %s 不支持看图，无法附图；请用 :model 切换到支持看图的模型\n", s.modelInfo.Ref())
		return
	}
	if len(s.pendingImages) >= maxPendingImages {
		fmt.Fprintf(out, "待发图片已达上限（%d 张），请先发送或 :image clear\n", maxPendingImages)
		return
	}
	part, _, err := s.readImage(arg)
	if err != nil {
		fmt.Fprintln(out, "读图失败："+err.Error())
		return
	}
	s.pendingImages = append(s.pendingImages, part)
	fmt.Fprintf(out, "[已附图 %d：%d×%d %s] 将随下一条消息发送\n",
		len(s.pendingImages), part.Width, part.Height, part.MediaType)
}

// attachFromClipboard is the Alt+V paste (design 0.3): one clipboard picture
// on the same queue as :image. The reader speaks for itself — no picture
// reports "剪贴板里没有图片", a missing reader says what to install.
func (s *chatState) attachFromClipboard() {
	out := s.errWriter()
	if s.readClip == nil {
		fmt.Fprintln(out, "Alt+V 贴图在本进程不可用")
		return
	}
	if !s.canSeeImages() {
		fmt.Fprintf(out, "当前模型 %s 不支持看图，无法附图；请用 :model 切换到支持看图的模型\n", s.modelInfo.Ref())
		return
	}
	if len(s.pendingImages) >= maxPendingImages {
		fmt.Fprintf(out, "待发图片已达上限（%d 张），请先发送或 :image clear\n", maxPendingImages)
		return
	}
	part, _, err := s.readClip()
	if err != nil {
		fmt.Fprintln(out, "Alt+V："+err.Error())
		return
	}
	s.pendingImages = append(s.pendingImages, part)
	fmt.Fprintf(out, "[已附图 %d：%d×%d %s] 将随下一条消息发送\n",
		len(s.pendingImages), part.Width, part.Height, part.MediaType)
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
func BuildChatModel(current ModelOption, options []ModelOption) string {
	var b strings.Builder
	fmt.Fprintf(&b, "当前模型：%s\n思考级别：%s\n上下文窗口：%d\n输出上限 max_output_tokens：%d\n可选模型：",
		current.Ref(), displayThinking(current.Thinking), current.ContextWindow, current.MaxOutputTokens)
	if len(options) == 0 {
		b.WriteString("无")
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

func displayThinking(level string) string {
	if level == "" {
		return "默认"
	}
	return level
}

// runChatCommand handles commands typed at this terminal. The caller checks injected first.
func (s *chatState) runChatCommand(line string) bool {
	switch {
	case line == ":help":
		fmt.Fprintln(s.source, BuildChatHelp())
	case isModelCommand(line) && !isCheckpointCommand(line) && !isUndoCommand(line):
		arg := strings.TrimSpace(strings.TrimPrefix(line, ":model"))
		if arg == "" {
			fmt.Fprintln(s.source, BuildChatModel(s.modelInfo, s.modelOptions))
			return true
		}
		if s.modelSwitch == nil {
			fmt.Fprintln(s.source, "模型切换不可用")
			return true
		}
		info, err := s.modelSwitch(s.ctx, s.sessionID, arg)
		if err != nil {
			fmt.Fprintln(s.source, "模型切换失败："+err.Error())
			return true
		}
		s.modelInfo = info
		s.render.SetModel(info.Provider, info.Model, info.Thinking, info.ContextWindow)
		fmt.Fprintf(s.source, "已切换模型：%s（下一回合生效）\n", info.Ref())
	case isImageCommand(line):
		out := s.errWriter()
		arg := strings.TrimSpace(strings.TrimPrefix(line, ":image"))
		switch {
		case arg == "" && len(s.pendingImages) == 0:
			fmt.Fprintln(out, "没有待发的图片")
		case arg == "":
			for i, part := range s.pendingImages {
				fmt.Fprintf(out, "[已附图 %d：%d×%d %s]\n", i+1, part.Width, part.Height, part.MediaType)
			}
		case arg == "clear":
			s.pendingImages = nil
			fmt.Fprintln(out, "已清空待发图片")
		default:
			s.attachImage(arg)
		}
	case line == ":usage":
		fmt.Fprintln(s.source, BuildChatUsage(*s.stats))
	case isQueueCommand(line):
		// The messages queued with Tab during a turn (followup.go).
		s.runQueueCommand(line)
	case isTasksCommand(line):
		s.runTasksCommand(line)
	case isCheckpointCommand(line):
		if strings.TrimSpace(line) != checkpointsCmd {
			fmt.Fprintln(s.source, "用法：:checkpoints")
		} else {
			s.runCheckpoints()
		}
	case isUndoCommand(line):
		command, ok := parseUndoCommand(line)
		if !ok {
			fmt.Fprintln(s.source, undoUsageHint)
		} else {
			s.runUndo(command)
		}
	default:
		return false
	}
	return true
}
