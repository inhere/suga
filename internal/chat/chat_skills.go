package chat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/inhere/suga/internal/output/termtext"
)

// skillsCmd is the :skills command; its options follow it.
const skillsCmd = ":skills"

// maxSkillLineDescription bounds the description :skills prints per skill, so a
// long frontmatter description cannot push the source out of the line.
const maxSkillLineDescription = 80

// SkillInfo is one loaded skill as :skills lists it: the name, the one-line
// description, where it came from (项目/内置/用户) and the SKILL.md path.
// The CLI fills it from the runtime's skill registry.
type SkillInfo struct {
	Name        string
	Description string
	Source      string
	Path        string
	AlwaysLoad  bool
}

// isSkillsCommand recognizes :skills with its arguments.
func isSkillsCommand(line string) bool {
	return line == skillsCmd || strings.HasPrefix(line, skillsCmd+" ")
}

// BuildChatSkills renders :skills: one line per skill with its description and
// source, or only the names (--only-names). limit 0 lists every skill; a
// smaller limit keeps the first ones by name and says how many are left.
func BuildChatSkills(items []SkillInfo, namesOnly bool, limit int) string {
	if len(items) == 0 {
		return "没有加载任何 skill（工作区 .agents/skills、skills 目录以及 SUGA_HOME 与 ~/.agents/skills 下都没有 SKILL.md）；运行 suga inspect manifest 查看 skill 根目录"
	}
	shown := items
	hidden := 0
	if limit > 0 && limit < len(items) {
		shown, hidden = items[:limit], len(items)-limit
	}
	var b strings.Builder
	if namesOnly {
		names := make([]string, 0, len(shown))
		for _, item := range shown {
			names = append(names, termtext.Clean(item.Name))
		}
		fmt.Fprintf(&b, "%s", strings.Join(names, ", "))
		if hidden > 0 {
			fmt.Fprintf(&b, " …（还有 %d 个，省略 -n 显示全部）", hidden)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "已加载 %d 个 skill：", len(items))
	for _, item := range shown {
		b.WriteByte('\n')
		desc := termtext.Clean(strings.Join(strings.Fields(item.Description), " "))
		if desc == "" {
			desc = "（无说明）"
		}
		fmt.Fprintf(&b, "%s — %s", termtext.Clean(item.Name), truncateLine(desc, maxSkillLineDescription))
		if item.Source != "" {
			fmt.Fprintf(&b, " · %s", item.Source)
		}
		if item.AlwaysLoad {
			b.WriteString(" · 常驻")
		}
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "\n还有 %d 个未显示：省略 -n 显示全部", hidden)
	}
	return b.String()
}

// runSkillsCommand runs :skills: the default listing, --only-names for the
// names alone, -n N to keep N of them and -h for the usage.
func (s *chatState) runSkillsCommand(line string) {
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, skillsCmd)))
	namesOnly, limit := false, 0
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "-h", "--help":
			fmt.Fprintln(s.source, commandUsageText(skillsCmd))
			return
		case "--only-names":
			namesOnly = true
		case "-n":
			if i+1 >= len(args) {
				fmt.Fprintln(s.source, "未知参数：-n 需要一个数字")
				fmt.Fprintln(s.source, commandUsageText(skillsCmd))
				return
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				fmt.Fprintln(s.source, "未知参数：-n "+args[i+1]+" 需要非负整数")
				fmt.Fprintln(s.source, commandUsageText(skillsCmd))
				return
			}
			limit = n
			i++
		default:
			fmt.Fprintln(s.source, "未知参数："+arg)
			fmt.Fprintln(s.source, commandUsageText(skillsCmd))
			return
		}
	}
	if s.skills == nil {
		fmt.Fprintln(s.source, "skill 列表在本进程不可用")
		return
	}
	fmt.Fprintln(s.source, BuildChatSkills(s.skills(), namesOnly, limit))
}
