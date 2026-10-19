package process

import (
	"encoding/json"
	"errors"
	"github.com/gookit/goutil/x/assert"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inhere/suga/pkg/agentcore/testutil"
)

func shortProcessRoot(t *testing.T) string {
	t.Helper()
	return testutil.Dir(t)
}

func TestServerPingDeliverListAndCleanup(t *testing.T) {
	root := shortProcessRoot(t)
	delivered := make(chan string, 2)
	server, err := Start(root, "session-a1", "chat title", t.TempDir(), func(mode, text, from string) (Delivery, error) {
		delivered <- from + ":" + text
		return Delivery{Queued: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	record := server.Record()
	// A short home still keeps the socket in the run root.
	if record.Socket != filepath.Join(root, itoa(record.PID)+".sock") {
		t.Fatalf("short home did not use the run-root socket: %q", record.Socket)
	}
	if got, err := List(root); err != nil || len(got) != 1 || got[0].SessionID != record.SessionID {
		t.Fatalf("list did not return live registration: %#v err=%v", got, err)
	}
	response, err := Send(record, Request{Type: "ping"})
	if err != nil || !response.OK {
		t.Fatalf("ping failed: %#v err=%v", response, err)
	}
	response, err = Send(record, Request{Text: "hello", From: "gofer"})
	if err != nil || !response.OK || !response.Queued {
		t.Fatalf("delivery failed: %#v err=%v", response, err)
	}
	if got := <-delivered; got != "gofer:hello" {
		t.Fatalf("unexpected delivered payload %q", got)
	}
	if err := server.SetBusy(true); err != nil {
		t.Fatalf("set busy state: %v", err)
	}
	if got, err := List(root); err != nil || len(got) != 1 || got[0].State != "busy" {
		t.Fatalf("busy state was not published: %#v err=%v", got, err)
	}
	if err := server.SetBusy(false); err != nil {
		t.Fatalf("set idle state: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, itoa(record.PID)+".json"), record.Socket} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("server cleanup left %s: %v", path, err)
		}
	}
}

func TestListRemovesOnlyDeadRegistrationsAndResolveDetectsAmbiguity(t *testing.T) {
	root := shortProcessRoot(t)
	deadPID := int(^uint(0) >> 1)
	stale := Record{PID: deadPID, SessionID: "stale", Socket: filepath.Join(root, itoa(deadPID)+".sock"), Host: "test"}
	data, _ := json.Marshal(stale)
	stalePath := filepath.Join(root, itoa(deadPID)+".json")
	if err := os.WriteFile(stalePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale.Socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	active := Record{PID: os.Getpid(), SessionID: "active", Socket: filepath.Join(root, itoa(os.Getpid())+".sock"), Host: "test"}
	activeData, _ := json.Marshal(active)
	if err := os.WriteFile(filepath.Join(root, itoa(active.PID)+".json"), activeData, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := List(root)
	if err != nil || len(got) != 1 || got[0].SessionID != "active" {
		t.Fatalf("expected only live process, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(stale.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket was not removed: %v", err)
	}
	_, err = Resolve([]Record{{SessionID: "abc-1", Name: "same"}, {SessionID: "abc-2", Name: "same"}}, "abc")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("expected prefix collision, got %v", err)
	}
}

func TestListDoesNotRemoveSocketPathOutsideRunRoot(t *testing.T) {
	root := shortProcessRoot(t)
	outside := filepath.Join(root, "keep.me")
	if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := Record{PID: int(^uint(0) >> 1), SessionID: "unsafe", Socket: outside, Host: "old-host"}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(root, itoa(record.PID)+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := List(root); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
		t.Fatalf("untrusted socket path was modified: data=%q err=%v", data, err)
	}
}

// fakeFallbackRecordFile writes a record file whose socket claims the fallback
// location of a different SUGA_HOME: it has the fallback's shape (shared
// per-user directory, "<hash>-<pid>.sock") but a hash no record of this root
// may own, the way a tampered record points the send machinery at a foreign
// socket. It returns the recorded socket path.
func fakeFallbackRecordFile(t *testing.T, root string, pid int) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other-home")
	socket := fallbackSocketPath(other, pid)
	data, _ := json.Marshal(Record{PID: pid, SessionID: "foreign", Socket: socket, Host: "test"})
	if err := os.WriteFile(filepath.Join(root, itoa(pid)+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return socket
}

// TestListRejectsForeignFallbackSocket uses a live PID so the only thing that
// decides the record's fate is the socket-path check: a record whose socket
// names a fallback path of another home is skipped by List, neither honored
// nor cleaned up like a dead registration's socket.
func TestListRejectsForeignFallbackSocket(t *testing.T) {
	root := shortProcessRoot(t)
	pid := os.Getpid()
	socket := fakeFallbackRecordFile(t, root, pid)
	got, err := List(root)
	if err != nil || len(got) != 0 {
		t.Fatalf("foreign fallback socket must be rejected, got %#v err=%v", got, err)
	}
	if _, serr := os.Stat(socket); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("a rejected record's socket must be left alone: %v", serr)
	}
	if _, rerr := os.Stat(filepath.Join(root, itoa(pid)+".json")); rerr != nil {
		t.Fatalf("a rejected record file must be kept for diagnosis: %v", rerr)
	}
}

func TestSessionLockRejectsLiveOwnerAndReclaimsDeadOwner(t *testing.T) {
	root := shortProcessRoot(t)
	lockRoot := filepath.Join(root, "locks")
	first, err := AcquireSessionLock(lockRoot, "session-lock", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireSessionLock(lockRoot, "session-lock", 0); !errors.Is(err, ErrSessionLocked) {
		t.Fatalf("live owner should block second open, got %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	dead := lockRecord{PID: int(^uint(0) >> 1), Host: first.rec.Host, SessionID: "session-lock", CreatedAt: time.Now().Add(-time.Hour)}
	data, _ := json.Marshal(dead)
	if err := os.WriteFile(filepath.Join(lockRoot, "session-lock.lock"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireSessionLock(lockRoot, "session-lock", 0)
	if err != nil {
		t.Fatalf("dead owner lock should be reclaimed: %v", err)
	}
	_ = second.Close()
}

func TestSeparateProcessesAreAddressableBySession(t *testing.T) {
	root := shortProcessRoot(t)
	spawn := func(sessionID string) (*exec.Cmd, string) {
		t.Helper()
		ready := filepath.Join(root, sessionID+".ready")
		cmd := exec.Command(os.Args[0], "-test.run=TestProcessChildFixture")
		cmd.Env = append(os.Environ(), "SUGA_PROCESS_FIXTURE=1", "SUGA_PROCESS_ROOT="+root, "SUGA_PROCESS_SESSION="+sessionID, "SUGA_PROCESS_READY="+ready)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				return cmd, ready
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child %s did not register", sessionID)
		return nil, ""
	}
	first, _ := spawn("dual-session-a")
	defer func() { _ = first.Process.Kill(); _ = first.Wait() }()
	second, _ := spawn("dual-session-b")
	defer func() { _ = second.Process.Kill(); _ = second.Wait() }()
	records, err := List(root)
	if err != nil || len(records) != 2 {
		t.Fatalf("expected two independent registrations, records=%#v err=%v", records, err)
	}
	selected, err := Resolve(records, "dual-session-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(selected, Request{Text: "targeted", From: "cli"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "dual-session-a.delivered"))
	if err != nil || string(got) != "cli:targeted" {
		t.Fatalf("target session did not receive text: got=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "dual-session-b.delivered")); !os.IsNotExist(err) {
		t.Fatalf("other session received targeted delivery: %v", err)
	}
}

func TestProcessChildFixture(t *testing.T) {
	if os.Getenv("SUGA_PROCESS_FIXTURE") != "1" {
		return
	}
	root, sessionID := os.Getenv("SUGA_PROCESS_ROOT"), os.Getenv("SUGA_PROCESS_SESSION")
	server, err := Start(root, sessionID, sessionID, root, func(mode, text, from string) (Delivery, error) {
		return Delivery{}, os.WriteFile(filepath.Join(root, sessionID+".delivered"), []byte(from+":"+text), 0o600)
	})
	if err != nil {
		os.Exit(10)
	}
	_ = os.WriteFile(os.Getenv("SUGA_PROCESS_READY"), nil, 0o600)
	for {
		time.Sleep(time.Second)
		_ = server
	}
}

// TestWindowsFallbackAlsoTooLongFailsClearly: when the fallback path is past
// the limit too, Start keeps the original AF_UNIX limit error (the message
// chat prints) and annotates it with the fallback's own failure.
func TestWindowsFallbackAlsoTooLongFailsClearly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows AF_UNIX path limit")
	}
	// A LOCALAPPDATA deep enough to push the fallback path past the limit too,
	// but short enough for a plain (non-\\?\) MkdirAll; the shallow end of the
	// created chain stays close to a real %LOCALAPPDATA% in length.
	base := t.TempDir()
	for i := 0; i < 5; i++ {
		base = filepath.Join(base, strings.Repeat("deep-segment-", 2))
	}
	t.Setenv("LOCALAPPDATA", base)
	root := filepath.Join(shortProcessRoot(t), strings.Repeat("long-segment-", 9))
	_, err := Start(root, "sid", "name", root, nil)
	if err == nil || !strings.Contains(err.Error(), "Windows AF_UNIX limit") || !strings.Contains(err.Error(), "too long as well") {
		t.Fatalf("fallback-too-long must keep the original error and name the fallback failure: %v", err)
	}
}

func TestSocketHashDiffersForRoots(t *testing.T) {
	first, second := socketHash(t.TempDir()), socketHash(filepath.Join(t.TempDir(), "suga"))
	if first == second {
		t.Fatalf("two homes hashed to the same fallback name %q", first)
	}
	if len(first) != 12 || first != strings.ToLower(first) {
		t.Fatalf("hash should be 12 lowercase hex chars, got %q", first)
	}
}

// TestLongHomeUsesFallbackSocket round-trips Start -> List -> Send through the
// per-user fallback location: the preferred run-root path is far past the
// AF_UNIX limit, so the record must name the hashed fallback socket and the
// chat behind it must still be reachable.
func TestLongHomeUsesFallbackSocket(t *testing.T) {
	relocate(t)
	root := filepath.Join(shortProcessRoot(t), strings.Repeat("long-segment-", 9))
	server, err := Start(root, "session-fallback", "fallback", root, func(mode, text, from string) (Delivery, error) {
		return Delivery{}, nil
	})
	if err != nil {
		t.Fatalf("long home should fall back, got %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	record := server.Record()
	if record.Socket != fallbackSocketPath(root, record.PID) {
		t.Fatalf("record must name the fallback socket: got %q want %q", record.Socket, fallbackSocketPath(root, record.PID))
	}
	listed, err := List(root)
	if err != nil || len(listed) != 1 || listed[0].Socket != record.Socket {
		t.Fatalf("list did not honor the fallback record: %#v err=%v", listed, err)
	}
	if response, err := Send(record, Request{Type: "ping"}); err != nil || !response.OK {
		t.Fatalf("send over the fallback socket failed: %#v err=%v", response, err)
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

// relocate points the per-OS fallback base at this test's private directory:
// %LOCALAPPDATA% on Windows, $TMPDIR elsewhere. The fallback directory tests
// then exercise is isolated from the real user locations and leaves nothing
// behind there (both variables are restored when the test ends).
func relocate(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The Windows fallback path counts UTF-16 units against a low limit, and
		// a t.TempDir test name would push it over; keep the base as short as a
		// real %LOCALAPPDATA% would be.
		dir, err := os.MkdirTemp("", "sg")
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOCALAPPDATA", dir)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	} else {
		t.Setenv("TMPDIR", t.TempDir())
	}
}

func TestResolvePrefersExactSessionID(t *testing.T) {
	records := []Record{{SessionID: "abc", PID: 1}, {SessionID: "abcdef", PID: 2}}
	t.Run("exact id beats prefix", func(t *testing.T) {
		got, err := Resolve(records, "abc")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, 1, got.PID)
	})
	t.Run("ambiguous prefix still errors", func(t *testing.T) {
		_, err := Resolve(records, "ab")
		assert.True(t, errors.Is(err, ErrAmbiguous))
	})
}
