package chat

import (
	"fmt"
	"strings"
)

// This file is the one place the local chat commands are described: the :help
// listing, the detailed usage that ":<命令> -h" prints (":help <命令>" is the
// same text) and the Tab completion of a command's options all read the table
// below, so a new option or a reworded usage is one edit.
//
// A command answers ":<命令> -h"/"--help" with its Usage, and a command without
// arguments answers any other argument the same way instead of dropping it
// (commandUsageReply).

// chatCommandSpec is one entry of the local command table.
type chatCommandSpec struct {
	// Names are the spellings the command answers to. :quit and :exit share one
	// entry, so :help lists them on one line and both print the same usage.
	Names []string
	// Summary is the one line :help prints.
	Summary string
	// Usage is the detailed text ":<命令> -h" prints: what the command does, its
	// arguments and one or two examples.
	Usage string
	// TakesArgs reports whether the command accepts arguments at all. One that
	// does not answers an argument with its usage rather than ignoring it.
	TakesArgs bool
	// Options are the arguments Tab offers after the command (":queue " + Tab).
	Options []string
}

// chatCommandSpecs lists every local command: its order is the order of :help
// and of the Tab completion candidates.
var chatCommandSpecs = []chatCommandSpec{
	{
		Names:   []string{":session"},
		Summary: "显示会话",
		Usage: `用法：:session
  显示本会话的 id，以及注册本地会话 socket 后得到的短名称（suga send --session <短名称> 用它）。

示例：
  :session`,
		Options: []string{"-h"},
	},
	{
		Names:   []string{":compact"},
		Summary: "压缩上下文",
		Usage: `用法：:compact
  立即压缩本会话的上下文，并打印压缩前后的 token 估算。

示例：
  :compact`,
		Options: []string{"-h"},
	},
	{
		Names:   []string{":add-dir"},
		Summary: "授权附加目录",
		Usage: `用法：:add-dir <绝对路径>[:ro|:rw]
  把一个目录加入本会话可访问的范围（默认只读，:rw 可写）。本会话立即生效，续接沿用。

示例：
  :add-dir /data/docs:rw
  :add-dir /data/cache`,
		TakesArgs: true,
		Options:   []string{"-h"},
	},
	{
		Names:   []string{":dirs"},
		Summary: "列出目录",
		Usage: `用法：:dirs
  列出工作区与附加目录，含只读/读写与来源（启动参数、环境变量、会话中追加、越界授权、会话沿用）。

示例：
  :dirs`,
		Options: []string{"-h"},
	},
	{
		Names:   []string{":rm-dir"},
		Summary: "撤销附加目录",
		Usage: `用法：:rm-dir <路径>
  撤销一个附加目录（:dirs 打印的 :ro/:rw 后缀可带上，会被忽略）。

示例：
  :rm-dir /data/cache`,
		TakesArgs: true,
		Options:   []string{"-h"},
	},
	{
		Names:   []string{":diff"},
		Summary: "显示文件改动",
		Usage: `用法：:diff [N]
  显示本会话的文件改动：不带 N 显示最近一次，N 显示第 N 次。

示例：
  :diff
  :diff 3`,
		TakesArgs: true,
		Options:   []string{"-h"},
	},
	{
		Names:   []string{":code"},
		Summary: "展开上一条回答的代码块",
		Usage: `用法：:code [N]
  展开上一条回答里被折叠的代码块：不带 N 展开最近一个，N 展开第 N 个（按出现顺序）。

示例：
  :code
  :code 2`,
		TakesArgs: true,
		Options:   []string{"-h"},
	},
	{
		Names:   []string{":image"},
		Summary: "管理待发图片（附到下一条消息）",
		Usage: `用法：:image [<路径>|clear]
  不带参数列出待发送的图片；<路径> 把一张图附到下一条消息；clear 清空待发图片。

示例：
  :image screenshot.png
  :image clear`,
		TakesArgs: true,
		Options:   []string{"clear", "-h"},
	},
	{
		Names:   []string{":queue"},
		Summary: "列出/清空排队的消息（回合中 Tab 排队，本轮结束后发送）",
		Usage: `用法：:queue [-d <N>|clear]
  不带参数列出排队的消息；-d <N> 删除第 N 条；clear 清空。
  回合中按 Tab 把输入行排队，本轮结束后按顺序各作为一条消息发送。

示例：
  :queue
  :queue -d 2
  :queue clear`,
		TakesArgs: true,
		Options:   []string{"-d", "clear", "-h"},
	},
	{
		Names:   []string{":checkpoints"},
		Summary: "列出写操作快照",
		Usage: `用法：:checkpoints [N]
  列出写操作快照（最近的回合在前）；N 只看最近 N 个回合。

示例：
  :checkpoints
  :checkpoints 5`,
		TakesArgs: true,
		Options:   []string{"-h"},
	},
	{
		Names:   []string{":undo"},
		Summary: "回滚文件改动",
		Usage: `用法：:undo [N] [force]
  N（默认 1）为最近 N 个有文件改动的回合；force 同时覆盖快照之后被改动过的文件。

示例：
  :undo
  :undo 2 force`,
		TakesArgs: true,
		Options:   []string{"force", "-h"},
	},
	{
		Names:   []string{":help"},
		Summary: "显示此帮助",
		Usage: `用法：:help [<命令>]
  不带参数列出全部命令与按键；带命令显示该命令的用法（等同 :<命令> -h）。

示例：
  :help
  :help :queue`,
		TakesArgs: true,
	},
	{
		Names:   []string{":model"},
		Summary: "查看/切换模型",
		Usage: `用法：:model [<provider>/<model>]
  不带参数显示当前模型与可选模型；带引用切换模型（下一回合生效，Tab 可补全模型名）。

示例：
  :model
  :model opencode-go/glm-5`,
		TakesArgs: true,
		Options:   []string{"-h"},
	},
	{
		Names:   []string{":usage"},
		Summary: "显示本会话用量与成本",
		Usage: `用法：:usage
  显示本次会话的轮数、输入/输出 token、缓存读/写与成本。

示例：
  :usage`,
		Options: []string{"-h"},
	},
	{
		Names:   []string{":tasks"},
		Summary: "后台任务：运行中+最近结束（-a 全部，-k 终止）",
		Usage: `用法：:tasks [选项]
  :tasks          列出运行中的任务和最近 10 条已结束任务
  :tasks -a       列出全部任务（含已结束的）
  :tasks -k <ID>  终止后台任务，可写多个：:tasks -k t3 t4
  :tasks -h       显示此帮助
示例：:tasks -k t2`,
		TakesArgs: true,
		Options:   []string{"-a", "-k", "-h"},
	},
	{
		Names:   []string{":quit", ":exit"},
		Summary: "退出",
		Usage: `用法：:quit（同 :exit）
  退出 chat；未发送的排队消息、没轮到运行的投递会在退出时提示丢弃。

示例：
  :quit
  :exit`,
		Options: []string{"-h"},
	},
}

// chatHelpKeys is the fixed tail of :help: the "!" prefixes and the keys.
const chatHelpKeys = `!<命令> 本地执行；!!<命令> 执行并附给下一条消息
按键：Enter/Alt+Enter/Ctrl+J 提交/换行；Tab 补全命令（回合中 Tab 把该行排到本轮结束后发送）；Alt+V 贴剪贴板图片；Esc 中止本轮；Ctrl+C 清空/中止/退出；回合中 Up 撤回插话；Up/Down 浏览历史`

// chatHelpHint tells where a single command's usage is.
const chatHelpHint = "查看命令详情：:<命令> -h（如 :queue -h），或 :help <命令>"

// chatCommands is every command name in table order (Tab completion).
var chatCommands = chatCommandNames()

// chatHelpText is the whole :help text, built once from the table.
var chatHelpText = buildChatHelp()

// dirCommandHelp is the one-line reminder the added-directory commands print
// inline (a malformed :add-dir/:rm-dir, the :dirs footer); the detailed usage of
// each of them is its table entry.
var dirCommandHelp = "用法：:add-dir <绝对路径>[:ro|:rw]（默认 :ro），:dirs 列出目录，:rm-dir <路径> 撤销"

// chatCommandNames collects every spelling of the table in order.
func chatCommandNames() []string {
	names := make([]string, 0, len(chatCommandSpecs)+2)
	for _, spec := range chatCommandSpecs {
		names = append(names, spec.Names...)
	}
	return names
}

// lookupCommand finds the table entry of a command name; ":queue" and "queue"
// name the same command.
func lookupCommand(name string) (chatCommandSpec, bool) {
	name = ":" + strings.TrimPrefix(strings.TrimSpace(name), ":")
	for _, spec := range chatCommandSpecs {
		for _, candidate := range spec.Names {
			if candidate == name {
				return spec, true
			}
		}
	}
	return chatCommandSpec{}, false
}

// commandUsageText is the detailed usage of a command. An unknown name says so
// instead of printing nothing, since silence would read as if it had run.
func commandUsageText(name string) string {
	if spec, ok := lookupCommand(name); ok {
		return spec.Usage
	}
	return "未知命令：" + name + "；:help 列出全部命令"
}

// commandAvailable reports whether chat runs a command in this process: the
// added-directory commands need the session's handle, which run, ACP and a chat
// without --add-dir do not have.
func commandAvailable(name string, dirsAvailable bool) bool {
	switch name {
	case addDirCmd, dirsCmd, rmDirCmd:
		return dirsAvailable
	}
	return true
}

// commandUsageReply is the text chat answers line with instead of running a
// command: a help ask (":queue -h", ":queue --help") shows that command's usage,
// and an argument a command cannot take shows the usage after naming the
// argument. An empty answer means line is a command to run (or no command at
// all), so chat goes on with it.
func commandUsageReply(line string, dirsAvailable bool) string {
	name, arg := splitCommand(line)
	spec, ok := lookupCommand(name)
	if !ok || !commandAvailable(name, dirsAvailable) {
		return ""
	}
	fields := strings.Fields(arg)
	if len(fields) > 0 && (fields[0] == "-h" || fields[0] == "--help") {
		return spec.Usage
	}
	if arg != "" && !spec.TakesArgs {
		return "未知参数：" + arg + "\n" + spec.Usage
	}
	return ""
}

// chatHelpFor answers :help: no argument lists every command, a command name
// shows that command's usage and anything else names the unknown command.
func chatHelpFor(arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return chatHelpText
	}
	if _, ok := lookupCommand(arg); ok {
		return commandUsageText(arg)
	}
	return "未知命令：" + arg + "；:help 列出全部命令"
}

// buildChatHelp renders :help: one line per command of the table, the fixed key
// lines and where the detail of a command is.
func buildChatHelp() string {
	var b strings.Builder
	for _, spec := range chatCommandSpecs {
		fmt.Fprintf(&b, "%s %s\n", strings.Join(spec.Names, "/"), spec.Summary)
	}
	b.WriteString(chatHelpKeys)
	b.WriteByte('\n')
	b.WriteString(chatHelpHint)
	return b.String()
}

// commandOptions lists the arguments Tab offers after a command: its own
// options and -h. :help instead completes the names of the commands.
func commandOptions(name string) []string {
	if name == ":help" {
		return chatCommands
	}
	spec, ok := lookupCommand(name)
	if !ok {
		return nil
	}
	return spec.Options
}
