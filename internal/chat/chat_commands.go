package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/inhere/suga/internal/output/termtext"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

const (
	chatHelpText = `:session 显示会话
:compact 压缩上下文
:add-dir/:dirs/:rm-dir 管理附加目录
:diff [N] 显示文件改动
:image <路径|clear> 管理待发图片（附到下一条消息）
:checkpoints 列出写操作快照
:undo [N] [force] 回滚文件改动
:help 显示此帮助
:usage 显示本会话用量与成本
:tasks 显示后台任务
:quit/:exit 退出
!<命令> 本地执行；!!<命令> 执行并附给下一条消息
按键：Enter/Alt+Enter/Ctrl+J 提交/换行；Alt+V 贴剪贴板图片；Esc 中止本轮；Ctrl+C 清空/中止/退出；回合中 Up 撤回插话；Up/Down 浏览历史`
	chatUsageHint  = ":usage 用量：本次 %d 轮 · 输入 %d / 输出 %d · 缓存读 %d / 写 %d · 成本 $%.6f；会话累计输入 %d / 输出 %d · 成本 $%.6f"
	chatTasksEmpty = "没有后台任务"
)

// BuildChatHelp returns the complete local-command and keyboard help text.
func BuildChatHelp() string { return chatHelpText }

// BuildChatUsage renders process and session usage using the same counters as the exit summary.
func BuildChatUsage(stats chatStats) string {
	return fmt.Sprintf(chatUsageHint, stats.turns, stats.usage.InputTokens, stats.usage.OutputTokens,
		stats.usage.CacheReadTokens, stats.usage.CacheWriteTokens, stats.usage.CostUSD,
		stats.session.InputTokens, stats.session.OutputTokens, stats.session.CostUSD)
}

// BuildChatTasks renders the task manager's stable start-order snapshot.
func BuildChatTasks(items []tasks.Info, now time.Time) string {
	if len(items) == 0 {
		return chatTasksEmpty
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		last := item.Ended
		if last.IsZero() {
			last = item.Started
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s · 已运行 %s · 最近输出 %s · %s",
			termtext.Clean(item.ID), termtext.Clean(string(item.Status)), termtext.Clean(item.Summary), formatElapsedAt(item.Duration(), now), last.Local().Format("15:04:05"), termtext.Clean(item.Kind)))
	}
	return strings.Join(lines, "\n")
}

func formatElapsedAt(d time.Duration, _ time.Time) string { return formatElapsed(d) }

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
	case line == ":tasks":
		if s.tasks == nil {
			fmt.Fprintln(s.source, chatTasksEmpty)
		} else {
			fmt.Fprintln(s.source, BuildChatTasks(s.tasks.List(s.sessionID), time.Now()))
		}
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
