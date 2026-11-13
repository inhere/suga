//go:build ptye2e

package ptye2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixture the read looks at: two lines are shown, the third is outside the
// window of the call.
const toolLinesTarget = "SMOKE_TARGET first\nSMOKE_TARGET second\nSMOKE_TARGET third\n"

// TestChatToolLines ports scripts/ptysmoke/chat_tool_lines.py: chat shows a tool
// call by tool, not as JSON - a summary of the tool's own for file_read, the
// command for shell_exec (first line and an ellipsis) and the compact JSON of the
// arguments where the tool has no summary (update_plan). The trace keeps the
// calls as they are: the display is a display only. Also checks the trimmed
// startup hint: rare commands only in :help.
func TestChatToolLines(t *testing.T) {
	binary := buildSuga(t)
	fake := &fakeLLM{Mode: "display", Delay: 200 * time.Millisecond, Reply: "SMOKE_TOOL_LINES_REPLY"}
	server := httptest.NewServer(fake)
	defer server.Close()
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "target.txt"), []byte(toolLinesTarget), 0o600); err != nil {
		t.Fatal(err)
	}
	s, env := startChatOpts(t, binary, chatOpts{
		Home: t.TempDir(), Work: work, URL: server.URL, ToolsExtra: "  approval: auto\n",
	})

	mark := s.Mark()
	expect(t, s, `Suga chat · fake/m1`, mark)
	expect(t, s, `❯ `, mark)
	// The hint is trimmed, and only :help names the rare commands: these checks
	// look at the banner's own text, before :help runs.
	banner := s.Since(mark)
	check(t, "banner names the common commands",
		strings.Contains(banner, ":session") && strings.Contains(banner, ":compact") && strings.Contains(banner, ":diff"))
	check(t, "banner omits the rare commands",
		!strings.Contains(banner, ":add-dir") && !strings.Contains(banner, ":checkpoints") && !strings.Contains(banner, ":undo"))

	// One line per call of the turn.
	mark = s.Mark()
	s.SendLine("显示工具调用行")
	expect(t, s, `▸ file_read target\.txt:1-2`, mark)
	expect(t, s, `⎿ ok: 1 SMOKE_TARGET first`, mark)
	expect(t, s, `\$ shell_exec echo SMOKE_DISPLAY …`, mark)
	expect(t, s, `⎿ ok: SMOKE_DISPLAY`, mark)
	expect(t, s, `✦ update_plan \{"plan":\[\{"status":"in_progress","step":"read"\}\]\}`, mark)
	expect(t, s, `▣ SMOKE_TOOL_LINES_REPLY`, mark)
	expect(t, s, `tokens · `, mark)

	// One command per line: :help lists name and summary; the details are
	// :<command> -h.
	mark = s.Mark()
	s.SendLine(":help")
	expect(t, s, `:add-dir `, mark)
	expect(t, s, `:rm-dir `, mark)
	expect(t, s, `:checkpoints`, mark)
	expect(t, s, `:undo `, mark)

	session := ctrlCTwice(t, s)
	check(t, "session id", session != "")
	if session == "" {
		return
	}
	// The display changed only the screen: the trace keeps the three calls with
	// its own key=value argument summary (no ":1-2" window of the chat line).
	events := traceEvents(t, binary, work, env, session)
	var tools []map[string]any
	for _, e := range events {
		if e["kind"] == "tool" {
			tools = append(tools, e)
		}
	}
	names := make([]string, 0, len(tools))
	for _, e := range tools {
		names = append(names, str(e["name"]))
	}
	check(t, "trace: the three tool events", strings.Join(names, ",") == "file_read,shell_exec,update_plan")
	if len(tools) != 3 {
		return
	}
	target, _ := attrs(tools[0])["target"].([]any)
	check(t, "trace: the file_read kept its path", len(target) == 1 && str(target[0]) == "target.txt")
	check(t, "trace: the argument summaries keep the trace's own form",
		str(attrs(tools[0])["arg"]) == "path=target.txt" &&
			strings.HasPrefix(str(attrs(tools[1])["arg"]), "command=echo SMOKE_DISPLAY"))
}
