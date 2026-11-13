//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// The screen, status and status-off scenarios of scripts/ptysmoke/chat_steer.py:
// what a long answer and the turn's own status row do to the input line, read off
// the screen model (vtscreen_test.go) instead of the byte stream.

const (
	// steerLong is the screen scenario's answer: three lines, each padded to 175
	// columns - wider than the pty window, so the terminal wraps every one of them.
	steerLong      = "STEER_LONG"
	steerLongLines = 3
	steerLongWide  = 175
	// steerScreenCols is the window the screen scenario runs in: a line of an
	// answer is wrapped by the terminal at this width, and chat has to know it
	// (SetSize) to lay its own lines out.
	steerScreenCols = 60
	// steerStatusReply is the status scenario's answer: several lines, streamed in
	// small chunks, so the status row can be looked at while it arrives.
	steerStatusReply = "STEER_OK\n第二行\n第三行"
	// steerTurnStatus is one frame of the status line (spinner.go): the animation
	// character, what the turn waits for and the elapsed seconds. The character of
	// a given frame is not predictable (the animation advances per frame), so any
	// of them is accepted.
	steerTurnStatus = `[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] (?:思考中|执行 [a-z_]+)… \d+s`
)

// steerLongReply is the screen scenario's answer as the fake model builds it, so
// the expectation cannot drift from what the model sends.
func steerLongReply() string {
	f := &fakeLLM{Mode: "text", Reply: steerLong, Lines: steerLongLines, Wide: steerLongWide}
	return f.reply()
}

// steerWrappedRows is the screen rows a text of whole lines takes on a cols wide
// screen: the prefix marks the start of the model message (its first line only,
// like LiveText prints it) and the terminal wraps every line at the window width.
func steerWrappedRows(text string, cols int, prefix string) []string {
	var rows []string
	for _, line := range strings.Split(prefix+text, "\n") {
		runes := []rune(line)
		for i := 0; i < len(runes); i += cols {
			rows = append(rows, string(runes[i:min(i+cols, len(runes))]))
		}
	}
	return rows
}

// steerRowIndex is where wanted shows up as a run of whole rows of rows (-1 when
// it does not).
func steerRowIndex(rows, wanted []string) int {
	for i := 0; i+len(wanted) <= len(rows); i++ {
		if slices.Equal(rows[i:i+len(wanted)], wanted) {
			return i
		}
	}
	return -1
}

// steerRowIndexes lists the rows of rows that hold text.
func steerRowIndexes(rows []string, text string) []int {
	var found []int
	for i, row := range rows {
		if strings.Contains(row, text) {
			found = append(found, i)
		}
	}
	return found
}

// steerStatusRows lists the rows of rows that are a status row of the turn in
// progress (the whole row, as the screen shows it).
func steerStatusRows(rows []string) []int {
	re := regexp.MustCompile(`^` + steerTurnStatus + `$`)
	var found []int
	for i, row := range rows {
		if re.MatchString(row) {
			found = append(found, i)
		}
	}
	return found
}

// steerStatusFrame is the pattern of one status-line frame saying label (思考中…,
// 执行 shell_exec…).
func steerStatusFrame(label string) string {
	return `[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] ` + regexp.QuoteMeta(label) + ` \d+s`
}

// steerScreen: a long, overwide reply streams in slowly while a message is typed:
// the screen model says the reply keeps its rows, whole, and the input line stays
// one line of its own (r3 M1, the check for "only complete lines leave" and for
// the width the line is laid out with).
//
//	❯ prompt / ↳ while the model answers        until(❯ ) / until(↳ )
//	no status line without colors               no 思考中 on screen
//	the reply streams in                        until(STEER_LONG-01 ) while a message is typed
//	its second/last line arrived                until(STEER_LONG-02) / until(STEER_LONG-03 )
//	the half line is echoed                     until(插话abc)
//	the turn ends                               until(tokens ·)
//	the half line is back at the prompt         until(❯ 插话abc)
//	the reply's rows are whole and in place     the ▣ rows of the answer appear as one run
//	the half line is on one row only            exactly one row holds it
//	the input line is the last row              that row is the last of the screen
//	the input line carries its prompt           it is ↳+typed or ❯+typed
//	nothing of the reply is left after it       no STEER_LONG row and no "."-only row after it
func steerScreen(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{
		Mode: "text", Reply: steerLong, Lines: steerLongLines, Wide: steerLongWide,
		Chunk: 20, ChunkDelay: 120 * time.Millisecond,
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	s, _ := startSteer(t, binary, steerChatOpts(t.TempDir(), ws, server.URL, ""), steerScreenCols, 24)

	m := s.Mark()
	until(t, s, "screen: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("长回答")
	until(t, s, "screen: the prompt becomes ↳ while the model answers", steerPrompt, m)
	// The status line of the turn needs a colored terminal and these scenarios run
	// with NO_COLOR set: nothing of it is drawn (the status scenario checks it with
	// the colors on).
	check(t, "screen: no status line without colors", !s.Contains(`思考中`, m))

	// Type while the reply streams: the chunks arrive slowly (ChunkDelay), and every
	// part goes out once another piece of the answer has landed - no sleep decides
	// when.
	s.Send("插")
	until(t, s, "screen: the reply streams in", regexp.QuoteMeta(steerLong+"-01 "), m)
	s.Send("话")
	until(t, s, "screen: its second line arrived", regexp.QuoteMeta(fmt.Sprintf("%s-%02d", steerLong, 2)), m)
	s.Send("abc")
	typed := "插话abc"
	until(t, s, "screen: the half line is echoed", regexp.QuoteMeta(typed), m)
	until(t, s, "screen: its last line arrived", regexp.QuoteMeta(fmt.Sprintf("%s-%02d ", steerLong, steerLongLines)), m)
	until(t, s, "screen: the turn ends", `tokens · `, m)
	// The half line comes back on the next prompt (during the turn its prompt was
	// ↳): wait for that redraw before looking at the screen.
	until(t, s, "screen: the half line is back at the prompt", `❯ `+regexp.QuoteMeta(typed), m)

	// The screen, as a terminal would show it: the rows the reply occupies and the
	// one row the input line lives on.
	rows := screenRows(s, steerScreenCols)
	expected := steerWrappedRows(steerLongReply(), steerScreenCols, "▣ ")
	start := steerRowIndex(rows, expected)
	if start < 0 {
		t.Logf("--- screen ---\n%s", strings.Join(rows, "\n"))
		t.Logf("--- the rows the reply should occupy ---\n%s", strings.Join(expected, "\n"))
	}
	check(t, "screen: the reply's rows are whole and in place", start >= 0)
	typedRows := steerRowIndexes(rows, typed)
	check(t, "screen: the half line is on one row only", len(typedRows) == 1)
	check(t, "screen: the input line is the last row", len(typedRows) > 0 && typedRows[len(typedRows)-1] == len(rows)-1)
	if len(typedRows) > 0 {
		// Whichever end of the turn it is: the running one (↳) or the prompt the half
		// line was handed to (❯, design N5).
		last := rows[typedRows[len(typedRows)-1]]
		check(t, "screen: the input line carries its prompt", last == steerPrompt+typed || last == "❯ "+typed)
	}
	if start >= 0 {
		// Nothing of the streamed text is left between the reply and the input line:
		// no fragment of it, and no row that is only its fill character.
		residue := 0
		for _, row := range rows[start+len(expected):] {
			if strings.Contains(row, steerLong) || (row != "" && strings.Trim(row, ".") == "") {
				residue++
			}
		}
		if residue > 0 {
			t.Logf("--- screen rows after the reply ---\n%s", strings.Join(rows[start+len(expected):], "\n"))
		}
		check(t, "screen: nothing of the reply is left after it", residue == 0)
	}
	exitChat(t, s)
}

// steerStatus: the turn's own status line sits right above the input line, is
// refreshed in place while a message is typed, and is gone when the turn ends.
//
//	❯ prompt / status says the model is asked    until(❯ ) / a 思考中… frame
//	the input line stays ↳ / tool call shown      until(↳ ) / until($ shell_exec)
//	tool started (marker file)                    the marker file exists
//	the status line names the running tool        a 执行 shell_exec… frame
//	the status line is the row above the input    the second to last row is a frame
//	the input line is the last row                it is the ↳ prompt alone
//	the half line is echoed / keeps refreshing    until(插话) / a frame again
//	the input line keeps what was typed           last row is ↳ 插话
//	exactly one status row, right above it        the only frame row is len-2
//	tool result / back to the model               until(⎿ ok: TOOL_DONE) / a 思考中… frame
//	the answer starts streaming                   until(STEER_OK)
//	while streaming: one status row above it      only frame row is len-2, answer above it
//	while streaming: the ↳ input line is last     last row is ↳ 插话
//	the answer's last line arrived / turn ends    until(第三行) / until(tokens ·)
//	the half line comes back at the prompt        until(❯ 插话)
//	no status row is left on screen               no frame row at all
//	the input line is the last row, at ❯          last row is ❯ 插话
func steerStatus(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{
		Mode: "tool", ToolDoneRule: true, Delay: 3 * time.Second, Reply: steerStatusReply,
		Command: steerToolCommand(8), Chunk: 6, ChunkDelay: 300 * time.Millisecond,
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	// The status line of a running turn needs a colored terminal: NO_COLOR (what the
	// other scenarios set) keeps it off.
	o := steerChatOpts(t.TempDir(), ws, server.URL, "")
	o.Color = true
	s, _ := startSteer(t, binary, o, 100, 40)

	m := s.Mark()
	until(t, s, "status: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一个长命令")
	// The model is asked for its first message (Delay): the status line says so,
	// while nothing of the turn is on screen yet.
	until(t, s, "status: the status line says the model is asked", steerStatusFrame("思考中…"), m)
	until(t, s, "status: the input line stays the ↳ prompt", steerPrompt, m)
	until(t, s, "status: tool call shown", `\$ shell_exec`, m)
	if !check2(t, "status: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}
	until(t, s, "status: the status line names the running tool", steerStatusFrame("执行 shell_exec…"), m)

	// The rows of the screen: the status line is the row right above the input line.
	check(t, "status: the status line is the row above the input line",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) >= 2 && isSteerStatusRow(rows[len(rows)-2])
		}))
	check(t, "status: the input line is the last row",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) > 0 && rows[len(rows)-1] == strings.TrimSpace(steerPrompt)
		}))

	// Type while the tool runs: the frames keep rewriting that row (its seconds
	// change), and what is typed into the input line stays where it is.
	typed := "插话"
	s.Send(typed)
	until(t, s, "status: the half line is echoed", regexp.QuoteMeta(typed), m)
	m2 := s.Mark()
	until(t, s, "status: the status line keeps refreshing", steerStatusFrame("执行 shell_exec…"), m2)
	check(t, "status: the input line keeps what was typed, as the last row",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) > 0 && rows[len(rows)-1] == steerPrompt+typed
		}))
	check(t, "status: exactly one status row, right above it",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return slices.Equal(steerStatusRows(rows), []int{len(rows) - 2})
		}))

	// The result is in: the model is asked again (Delay) and the status line goes
	// back to the model's own wording.
	m2 = s.Mark()
	until(t, s, "status: tool result shown", `⎿ ok: TOOL_DONE`, m2)
	until(t, s, "status: the status line goes back to the model", steerStatusFrame("思考中…"), m2)

	// The answer streams in small chunks (Chunk): the status line keeps the row
	// right above the input line, and none of it is left in the output area above.
	until(t, s, "status: the answer starts streaming", regexp.QuoteMeta("STEER_OK"), m2)
	check(t, "status: while streaming: one status row, right above the input line",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) >= 3 && strings.Contains(strings.Join(rows[:len(rows)-2], " "), "STEER_OK") &&
				slices.Equal(steerStatusRows(rows), []int{len(rows) - 2})
		}))
	check(t, "status: while streaming: the ↳ input line is the last row",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) > 0 && rows[len(rows)-1] == steerPrompt+typed
		}))
	until(t, s, "status: the answer's last line arrived", regexp.QuoteMeta("第三行"), m2)
	until(t, s, "status: the turn ends", `tokens · `, m2)

	// The end of the turn: the status line is gone, and the half line comes back at
	// the ❯ prompt of the next read (design N5).
	until(t, s, "status: the half line comes back at the prompt", `❯ `+regexp.QuoteMeta(typed), m2)
	check(t, "status: no status row is left on screen",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) > 0 && rows[len(rows)-1] == "❯ "+typed && len(steerStatusRows(rows)) == 0
		}))
	check(t, "status: the input line is the last row, at the ❯ prompt",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) > 0 && rows[len(rows)-1] == "❯ "+typed
		}))
	exitChat(t, s)
}

// isSteerStatusRow reports whether row is a status row of the running turn.
func isSteerStatusRow(row string) bool {
	return regexp.MustCompile(`^` + steerTurnStatus + `$`).MatchString(row)
}

// waitSteerRows reads the screen until ok holds for its rows (it repaints
// asynchronously, so a busy machine must not catch it half way) and reports
// whether it got there within 3s.
func waitSteerRows(t *testing.T, s *ptytest.Session, cols int, ok func(rows []string) bool) bool {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	var rows []string
	for {
		rows = screenRows(s, cols)
		if ok(rows) {
			return true
		}
		if time.Now().After(end) {
			t.Logf("screen rows %q", rows)
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// steerStatusOff: chat.spinner: false shows no status line, in a colored terminal
// too.
//
//	❯ prompt / tool call shown / tool started    until(❯ ) / until($ shell_exec) / marker file
//	the half line is echoed                       until(插话)
//	no status row above the input line            no frame row on the screen
//	the ↳ input line is the last row              last row is ↳ 插话
//	no status line anywhere                       no 思考中 in the output
//	tool result / the turn answers / turn ends    until(⎿ ok: TOOL_DONE) / until(answer) / until(tokens ·)
func steerStatusOff(t *testing.T, binary string) {
	ws := t.TempDir()
	fake := &fakeLLM{Mode: "tool", ToolDoneRule: true, Delay: 500 * time.Millisecond, Reply: steerReply, Command: steerToolCommand(4)}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	o := steerChatOpts(t.TempDir(), ws, server.URL, "chat:\n  spinner: false\n")
	o.Color = true
	s, _ := startSteer(t, binary, o, 100, 40)

	m := s.Mark()
	until(t, s, "status-off: ❯ prompt", `❯ `, m)
	m = s.Mark()
	s.SendLine("跑一个长命令")
	until(t, s, "status-off: tool call shown", `\$ shell_exec`, m)
	if !check2(t, "status-off: tool started (marker file)", waitMarker(filepath.Join(ws, steerStarted), 30*time.Second)) {
		return
	}
	typed := "插话"
	s.Send(typed)
	until(t, s, "status-off: the half line is echoed", regexp.QuoteMeta(typed), m)
	check(t, "status-off: no status row above the input line",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) > 0 && rows[len(rows)-1] == steerPrompt+typed && len(steerStatusRows(rows)) == 0
		}))
	check(t, "status-off: the ↳ input line is the last row",
		waitSteerRows(t, s, 100, func(rows []string) bool {
			return len(rows) > 0 && rows[len(rows)-1] == steerPrompt+typed
		}))
	check(t, "status-off: no status line anywhere", !s.Contains(`思考中`, m))

	m2 := s.Mark()
	until(t, s, "status-off: tool result shown", `⎿ ok: TOOL_DONE`, m2)
	until(t, s, "status-off: the turn answers", regexp.QuoteMeta(steerReply), m2)
	until(t, s, "status-off: the turn ends", `tokens · `, m2)
	exitChat(t, s)
}
