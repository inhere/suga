//go:build ptye2e

package ptye2e

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/gookit/gopty/ptytest"
)

// Helpers added by the fourth smoke migration batch (input and rendering: paste,
// win32-input-mode, spinner, tool lines, interrupts, history and the English UI).

// win32Rec is one win32-input-mode record (DECSET 9001): virtual key, scan code,
// the UTF-16 code unit, key down (1) or up (0), control state and repeat count.
func win32Rec(vk, sc, uc, kd, cs, rc int) string {
	return fmt.Sprintf("\x1b[%d;%d;%d;%d;%d;%d_", vk, sc, uc, kd, cs, rc)
}

// win32Press is one key press as a terminal in that mode reports it: the down
// record and the up record.
func win32Press(vk, uc, cs, sc, rc int) string {
	return win32Rec(vk, sc, uc, 1, cs, rc) + win32Rec(vk, sc, uc, 0, cs, rc)
}

// win32Text is plain typed text: one down/up record pair per UTF-16 code unit (a
// character outside the BMP arrives as a surrogate pair, exactly as a terminal
// sends it).
func win32Text(text string) string {
	var out string
	for _, unit := range utf16.Encode([]rune(text)) {
		out += win32Press(0, int(unit), 0, 0, 1)
	}
	return out
}

// Keys the win32-input-mode scenario sends (virtual key, code unit, control state,
// scan code, repeat count).
var (
	win32Enter     = win32Press(13, 13, 0, 28, 1)
	win32CtrlEnter = win32Press(13, 10, 8, 28, 1)
	win32Backspace = win32Press(8, 8, 0, 0, 1)
	win32CtrlD     = win32Press(68, 4, 8, 0, 1)
)

// requestsOf returns the requests the fake model has answered so far (a copy).
func requestsOf(f *fakeLLM) []fakeRequest { return f.Requests() }

// userWants is the user message of every answered request.
func userWants(f *fakeLLM) []string {
	reqs := f.Requests()
	users := make([]string, 0, len(reqs))
	for _, req := range reqs {
		users = append(users, req.User)
	}
	return users
}

// waitUserRequest waits until some request's user message contains want, the way
// the Python scripts' FakeLog.wait_user did: it is the only place a smoke can see
// what the model actually received.
func waitUserRequest(f *fakeLLM, want string, timeout time.Duration) bool {
	return waitUserRequestName(f, want, timeout) != ""
}

// waitUserRequestName is waitUserRequest that returns the matching message ("" on
// a timeout), so a check can log what did arrive.
func waitUserRequestName(f *fakeLLM, want string, timeout time.Duration) string {
	end := time.Now().Add(timeout)
	for {
		for _, user := range userWants(f) {
			if strings.Contains(user, want) {
				return user
			}
		}
		if time.Now().After(end) {
			return ""
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// countUserWith counts the answered requests whose user message contains want.
func countUserWith(f *fakeLLM, want string) int {
	n := 0
	for _, user := range userWants(f) {
		if strings.Contains(user, want) {
			n++
		}
	}
	return n
}

// noTurnAfter waits settle and reports that no further model request arrived
// after from: the "no turn starts" checks of the paste scenarios. The wait for
// the input line itself is an Expect on the screen, this only covers the time a
// turn would need to show up.
func noTurnAfter(f *fakeLLM, from int, settle time.Duration) bool {
	time.Sleep(settle)
	return len(f.Requests()) == from
}

// settleOutput waits dur and returns the raw output received after rawMark, so a
// negative check ("the hint was erased", "no title sequence anywhere") reads the
// screen it covers rather than a snapshot taken too early.
func settleOutput(s *ptytest.Session, rawMark int, dur time.Duration) string {
	time.Sleep(dur)
	raw := s.RawText()
	if rawMark > len(raw) {
		return ""
	}
	return raw[rawMark:]
}
