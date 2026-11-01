//go:build ptye2e

package ptye2e

import (
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// The parts of fakeLLM that smoke migration batch 3 (suga-hb8v) added, kept out
// of fakellm_test.go so that file only carries the field declarations and the
// few hook lines that call into here. Ports of fakellm.py's refs and loop modes,
// --fail-from/--fail-count and the per-message "seen" digest.

// token is fakellm.py message_token: one short token for a request message, so a
// scenario can see what the model received and in what order.
func (m fakeMessage) token() string {
	if m.Role == "assistant" {
		var names []string
		for _, c := range m.ToolCalls {
			names = append(names, c.Function.Name)
		}
		if len(names) == 0 {
			return "a"
		}
		return "a:" + strings.Join(names, ",")
	}
	head := []rune(strings.Join(strings.Fields(m.text()), " "))
	if len(head) > 40 {
		head = head[:40]
	}
	return m.Role + ":" + string(head)
}

// countArrival counts a request as it arrives, before the response delay, so a
// scenario can act (e.g. kill chat) while the model is still "thinking".
func (f *fakeLLM) countArrival() {
	f.mu.Lock()
	f.arrived++
	f.mu.Unlock()
}

// Arrived is how many requests reached the server so far, including ones a
// killed client never got an answer for (Requests only logs answered ones).
func (f *fakeLLM) Arrived() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.arrived
}

// WaitArrived waits until at least n requests have arrived.
func (f *fakeLLM) WaitArrived(n int, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if f.Arrived() >= n {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// failsFrom is --fail-from N [--fail-count M]: request n (1-based) fails when it
// is N or later and, unless M <= 0, fewer than M requests after N.
func (f *fakeLLM) failsFrom(n int) bool {
	return f.FailFrom > 0 && n >= f.FailFrom && (f.FailCount <= 0 || n < f.FailFrom+f.FailCount)
}

// refsCall is the refs mode: file_refs on one file, then on a directory.
func (f *fakeLLM) refsCall(step int) *toolCall {
	name := orDefault(f.RefName, "Load")
	calls := []toolCall{
		{"file_refs", map[string]any{"name": name, "path": orDefault(f.RefFile, "svc/svc.go")}},
		{"file_refs", map[string]any{"name": name, "path": orDefault(f.RefDir, ".")}},
	}
	if step < len(calls) {
		return &calls[step]
	}
	return nil
}

// loopCall is the plan of the loop modes, one call per model turn (fakellm.py
// loop_plan): loop reads an unchanged file four times (R1 on the third, R2 on the
// fourth); loop-edit runs the same successful command four times with a successful
// file_edit between runs (the workspace changes, so no reminder); loop-fail runs
// the same failing command three times (R3 on the third failure).
func (f *fakeLLM) loopCall(step int) *toolCall {
	var plan []toolCall
	switch f.Mode {
	case "loop-edit":
		oldS, newS := orDefault(f.EditOld, "SMOKE_TARGET old"), orDefault(f.EditNew, "SMOKE_TARGET new")
		for i := 0; i < 4; i++ {
			plan = append(plan, toolCall{"shell_exec", map[string]any{"command": orDefault(f.LoopCommand, "echo SMOKE_LOOP_SHELL")}})
			if i < 3 {
				// Alternating old/new keeps every edit a successful one.
				from, to := oldS, newS
				if i%2 == 1 {
					from, to = newS, oldS
				}
				plan = append(plan, toolCall{"file_edit", map[string]any{"path": orDefault(f.EditPath, "target.txt"), "old_string": from, "new_string": to}})
			}
		}
	case "loop-fail":
		for i := 0; i < 3; i++ {
			plan = append(plan, toolCall{"shell_exec", map[string]any{"command": orDefault(f.LoopFailCommand, "false")}})
		}
	default:
		for i := 0; i < 4; i++ {
			plan = append(plan, toolCall{"file_read", map[string]any{"path": "loop_target.txt"}})
		}
	}
	if step < len(plan) {
		return &plan[step]
	}
	return nil
}

// hintKind is which loop-guard reminder a request carried, from the text of all
// its messages (fakellm.py): "r3" for a command that keeps failing (已失败),
// "repeat" for R1/R2 (换一种方法), "" for none.
func (r fakeRequest) hintKind() string {
	switch {
	case strings.Contains(r.Messages, "已失败"):
		return "r3"
	case strings.Contains(r.Messages, "换一种方法"):
		return "repeat"
	}
	return ""
}

// switchHandler lets a scenario swap the fake model behind one server, the way
// the Python scripts restarted fakellm.py on the same port (the chat's config
// keeps one base URL).
type switchHandler struct{ cur atomic.Pointer[fakeLLM] }

func newSwitchHandler(f *fakeLLM) *switchHandler {
	h := &switchHandler{}
	h.cur.Store(f)
	return h
}

// Swap makes f answer the following requests.
func (h *switchHandler) Swap(f *fakeLLM) { h.cur.Store(f) }

func (h *switchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.cur.Load().ServeHTTP(w, r)
}
