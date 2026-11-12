//go:build ptye2e

package ptye2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gookit/gopty/ptytest"
)

// The multi-command hooks scenario (plan 5xmf T7) runs chat with two hook
// commands: the compatible hooks.command (label "gofer", a Stop that waits for an
// outside reply) and one hooks.commands entry (label "cont", which asks for a
// continuation once). Both commands are this test binary itself, the way
// TestHookHelper is, so the scenario needs no script.
const (
	multiHookProcessEnv = "PTYE2E_HOOK_PROCESS"
	multiHookLogEnv     = "PTYE2E_HOOK_LOG"
	// continueReason is the Stop block reason of the cont entry: chat shows it and
	// runs the turn it asks for, so it is the scenario's "the block worked" mark.
	continueReason = "continue-now"
)

// TestMultiHookHelper is not a test: chat runs this test binary as both hook
// commands, `-test.run=^TestMultiHookHelper$ -- <label> <mode>`. It follows the
// fake hook process contract of the plan (HPF7): every invocation appends
// "<label> <event> <phase>" to $PTYE2E_HOOK_LOG, so the log tells which entry ran
// when; the mode decides the Stop behaviour (block-once, wait, silent); Interrupt
// is logged and answered at once.
func TestMultiHookHelper(t *testing.T) {
	if os.Getenv(multiHookProcessEnv) != "1" {
		return
	}
	label, mode := multiHookArgs()
	logPath := os.Getenv(multiHookLogEnv)
	event := multiHookEvent()
	appendHookLog(logPath, label, event, "start")
	switch event {
	case "Interrupt":
		// Sent when the person types during a Stop wait: never keep chat waiting.
	case "Stop":
		multiHookStop(logPath, label, mode)
	case "SessionStart", "UserPromptSubmit":
		// The ordinary event shape of the contract: additional context on stdout,
		// a notice line on stderr.
		out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"additionalContext": label + "-ctx"}})
		fmt.Println(string(out))
		fmt.Fprintln(os.Stderr, "notice-"+label)
	}
	appendHookLog(logPath, label, event, "end")
	os.Exit(0)
}

// multiHookArgs reads the "<label> <mode>" pair after the "--" of the hook command.
func multiHookArgs() (label, mode string) {
	for i, arg := range os.Args {
		if arg == "--" && i+2 < len(os.Args) {
			return os.Args[i+1], os.Args[i+2]
		}
	}
	return "cont", "silent"
}

// multiHookEvent reads the event name of the JSON line chat writes on stdin.
func multiHookEvent() string {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	var event map[string]any
	_ = json.Unmarshal([]byte(line), &event)
	for _, key := range []string{"hook_event_name", "name", "Name"} {
		if name, ok := event[key].(string); ok {
			return name
		}
	}
	return ""
}

// multiHookStop is the Stop behaviour the mode names:
//
//	block-once  block with continueReason on the first Stop (counted in a state
//	            file next to the log), silent afterwards
//	wait        sleep until chat kills it, like a hook waiting for an outside reply
//	silent      answer nothing
func multiHookStop(logPath, label, mode string) {
	switch mode {
	case "block-once":
		if bumpStopCount(logPath, label) == 1 {
			out, _ := json.Marshal(map[string]any{"decision": "block", "reason": continueReason})
			fmt.Println(string(out))
		}
	case "wait":
		time.Sleep(30 * time.Second)
	}
}

// appendHookLog appends one "<label> <event> <phase>" line with a single write, so
// the Stop process and a concurrent Interrupt process never mix a line. Failing to
// write is best-effort: this is a helper process whose only output channel is the
// log, so there is nobody to report the error to (the scenario fails on the missing
// line instead).
func appendHookLog(path, label, event, phase string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck // best-effort log append
	_, _ = f.WriteString(label + " " + event + " " + phase + "\n")
}

// bumpStopCount counts the Stop events of the entry in a state file next to the
// log: hook processes do not share memory, so a file keeps the count.
func bumpStopCount(logPath, label string) int {
	path := filepath.Join(filepath.Dir(logPath), label+"-stop.count")
	count := 0
	if data, err := os.ReadFile(path); err == nil {
		count, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	count++
	_ = os.WriteFile(path, []byte(strconv.Itoa(count)), 0o600)
	return count
}

// hookCommand is the argv of one fake hook entry as a YAML/JSON list.
func hookCommand(self, label, mode string) string {
	argv, _ := json.Marshal([]string{self, "-test.run=^TestMultiHookHelper$", "--", label, mode})
	return string(argv)
}

// TestChatMultiHooks runs the real chat binary against a compatible hook command
// (entry 0, Stop waits 30s) and one hooks.commands entry (entry 1, asks for a
// continuation once). The first Stop is answered by the entry alone and continues
// the turn; the next Stop runs the entry and then the waiting command, and typing
// cuts that wait short. The hook log shows which entry ran when, and
// `inspect trace --json` shows one hook event per entry (with entry/program) and
// none for Interrupt.
func TestChatMultiHooks(t *testing.T) {
	binary := buildSuga(t)
	server := httptest.NewServer(&fakeLLM{})
	defer server.Close()
	home, work := t.TempDir(), t.TempDir()
	logPath := filepath.Join(t.TempDir(), "hooks.log")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	extra := fmt.Sprintf(`hooks:
  command: %s
  stop_wait_sec: 30
  commands:
    - command: %s
      timeout_ms: 5000
`, hookCommand(self, "gofer", "wait"), hookCommand(self, "cont", "block-once"))
	s, env := startChatOpts(t, binary, chatOpts{
		Home: home, Work: work, URL: server.URL,
		Extra:  extra,
		SetEnv: []string{"SUGA_HOOKS=on", multiHookProcessEnv + "=1", multiHookLogEnv + "=" + logPath},
	})

	mark := s.Mark()
	expect(t, s, `❯ `, mark)
	mark = s.Mark()
	s.SendLine("跑一下")
	// The first Stop is the entry's alone and asks for a continuation: chat shows
	// the reason and runs the turn it asked for.
	expect(t, s, continueReason, mark)
	// The continuation's Stop runs the entry first, then the waiting hooks.command.
	// Typing cuts that wait short, so the next turn starts.
	waitHookLog(t, logPath, "gofer Stop start", 1)
	s.SendLine("先这样")
	// Leave only once the waiting command of that turn's Stop is running: a Ctrl-C
	// pressed earlier could reach whichever entry is running then.
	waitHookLog(t, logPath, "gofer Stop start", 2)
	id := exitChatAsSession(t, s)

	lines := readHookLog(t, logPath)
	stops := hookLogLabels(lines, "Stop")
	check(t, "first Stop runs only the hooks.commands entry", len(stops) >= 2 && stops[0] == "cont" && stops[1] == "cont")
	check(t, "second Stop runs the entry before the waiting hooks.command", len(stops) >= 3 && stops[2] == "gofer")
	interrupts := hookLogLabels(lines, "Interrupt")
	check(t, "Interrupt reaches only the waiting hooks.command", len(interrupts) > 0 && onlyLabel(interrupts, "gofer"))

	events := traceEvents(t, binary, work, env, id)
	byName := map[string]map[int]bool{}
	named := true
	for _, e := range events {
		if e["kind"] != "hook" {
			continue
		}
		name, _ := e["name"].(string)
		entry, hasEntry := attrs(e)["entry"].(float64)
		program, _ := attrs(e)["program"].(string)
		if !hasEntry || program == "" {
			// Every hook run of the pipeline names its entry and program (HF14).
			check(t, "trace: hook event "+name+" names entry and program", false)
			named = false
			continue
		}
		if byName[name] == nil {
			byName[name] = map[int]bool{}
		}
		byName[name][int(entry)] = true
	}
	check(t, "trace: hook events are named", named)
	for _, name := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		check(t, "trace: "+name+" hook of the entry and of the command", byName[name][0] && byName[name][1])
	}
	check(t, "trace: no Interrupt hook event", findEvent(events, "hook", "Interrupt") == nil)
	first := firstHookEvent(events, "Stop", 1)
	check(t, "trace: the entry's first Stop blocked", first != nil && attrs(first)["decision"] == "block")
}

// waitHookLog waits until the fake hook log holds n lines containing needle: the
// scenario's deterministic "this step ran" signal instead of a fixed sleep.
func waitHookLog(t *testing.T, path, needle string, n int) {
	t.Helper()
	end := time.Now().Add(45 * time.Second)
	for time.Now().Before(end) {
		if strings.Count(readHookLogText(path), needle) >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%q did not appear %d time(s); hook log:\n%s", needle, n, readHookLogText(path))
}

func readHookLogText(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func readHookLog(t *testing.T, path string) []string {
	t.Helper()
	text := readHookLogText(path)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("hook log %s is empty", path)
	}
	return strings.Split(strings.TrimSpace(text), "\n")
}

// hookLogLabels returns the label of every started invocation of the event, in
// log order (the "end" line of the same invocation is ignored).
func hookLogLabels(lines []string, event string) []string {
	var labels []string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[1] == event && fields[2] == "start" {
			labels = append(labels, fields[0])
		}
	}
	return labels
}

func onlyLabel(labels []string, label string) bool {
	for _, item := range labels {
		if item != label {
			return false
		}
	}
	return true
}

// firstHookEvent returns the first hook event of the name from the given entry.
func firstHookEvent(events []map[string]any, name string, entry int) map[string]any {
	for _, e := range events {
		if e["kind"] != "hook" || e["name"] != name {
			continue
		}
		if at, _ := attrs(e)["entry"].(float64); int(at) == entry {
			return e
		}
	}
	return nil
}

// exitChatAsSession leaves chat the way exitChat does (a press that only cuts a
// running Stop hook wait short is repeated until chat asks to confirm) and
// returns the session id of the resume hint it prints.
func exitChatAsSession(t *testing.T, s *ptytest.Session) string {
	t.Helper()
	mark := s.Mark()
	exitChat(t, s)
	id := resumeID.FindStringSubmatch(s.Since(mark))
	if id == nil {
		t.Fatal("the resume hint names no session")
	}
	return id[1]
}
