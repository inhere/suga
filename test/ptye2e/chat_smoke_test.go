//go:build ptye2e

// Package ptye2e runs the same interactive chat smoke on unix ptys and Windows
// ConPTY. It deliberately starts the real suga binary instead of calling chat
// internals, so keyboard bytes and terminal redraws take the production path.
package ptye2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gookit/gopty"
	"github.com/gookit/gopty/ptytest"
)

type fakeChat struct {
	mode             string
	delay            time.Duration
	blockUntilCancel bool
	// command is what the tool modes pass to shell_exec, and background starts it
	// as a background task instead of waiting for it.
	command    string
	background bool

	mu       sync.Mutex
	lastUser string
	calls    int
}

// ServeHTTP implements the smallest chat/completions stream needed by these
// scenarios. It recognizes a completed tool call from the request history so the
// real agent performs the tool step before receiving the final text.
func (f *fakeChat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Messages []struct {
			Role      string          `json:"role"`
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	lastUser := ""
	lastUserIndex := -1
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == "user" {
			lastUser, lastUserIndex = request.Messages[i].Content, i
			break
		}
	}
	toolDone := false
	for _, message := range request.Messages[lastUserIndex+1:] {
		if message.Role == "tool" {
			toolDone = true
			break
		}
	}

	f.mu.Lock()
	f.lastUser = lastUser
	f.calls++
	callID := f.calls
	f.mu.Unlock()

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-r.Context().Done():
			return
		}
	}

	if f.blockUntilCancel {
		<-r.Context().Done()
		return
	}

	if f.mode == "approval" && !toolDone {
		writeToolCall(w, callID, "file_write", map[string]any{
			"path": approvalPath(lastUser), "content": "APPROVED",
		})
		return
	}

	if f.mode == "shell" && !toolDone {
		args := map[string]any{"command": f.command}
		if f.background {
			args["background"] = true
		}
		writeToolCall(w, callID, "shell_exec", args)
		return
	}

	reply := "收到：" + lastUser
	switch f.mode {
	case "approval":
		reply = "工具处理完成"
	case "shell":
		reply = "命令完成"
	}
	writeStream(w, map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"content": reply},
	}}})
}

// writeToolCall streams one call the model made: its name and the JSON arguments
// the provider sends for it.
func writeToolCall(w http.ResponseWriter, callID int, name string, args map[string]any) {
	raw, _ := json.Marshal(args)
	writeStream(w, map[string]any{
		"choices": []any{
			map[string]any{
				"delta": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"index": 0, "id": fmt.Sprintf("call-%d", callID),
							"function": map[string]any{"name": name, "arguments": string(raw)},
						},
					},
				},
			},
		},
	})
}

// approvalPath gives the rejection turn a separate path, allowing the smoke to
// check both the accepted write and the rejected write's absence.
func approvalPath(user string) string {
	if strings.Contains(user, "reject") {
		return "rejected.txt"
	}
	return "approved.txt"
}

// writeStream emits SSE boundaries separately, matching the provider's streaming
// contract while keeping this fake deterministic and dependency-free.
func writeStream(w http.ResponseWriter, chunk map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	data, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func TestChatPTYSmoke(t *testing.T) {
	binary := buildSuga(t)

	t.Run("enter submits and prints turn status", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "text"})
		defer server.Close()
		s := startChat(t, binary, server.URL, t.TempDir(), t.TempDir())
		mark := s.Mark()
		expect(t, s, `Suga chat`, mark)
		expect(t, s, `❯ `, mark)

		mark = s.Mark()
		s.SendLine("hello")
		expect(t, s, `收到：hello`, mark)
		expect(t, s, `tokens ·`, mark)
		s.SendLine(":quit")
		waitExit(t, s)
	})

	t.Run("multiline key submits one message", func(t *testing.T) {
		fake := &fakeChat{mode: "text"}
		server := httptest.NewServer(fake)
		defer server.Close()
		s := startChat(t, binary, server.URL, t.TempDir(), t.TempDir())
		mark := s.Mark()
		expect(t, s, `❯ `, mark)

		if runtime.GOOS == "windows" {
			// ConPTY accepts Alt+Enter for an embedded newline.
			s.Send("first\x1b\rsecond\r")
		} else {
			// Unix uses Alt+Enter (ESC, CR) because LF is ordinary Enter.
			s.Send("first\x1b\rsecond\r")
		}
		expect(t, s, `收到：first`, mark)
		expect(t, s, `❯`, mark)
		fake.mu.Lock()
		got := fake.lastUser
		fake.mu.Unlock()
		if got != "first\nsecond" {
			t.Fatalf("multiline user was %q, terminal tail:\n%s", got, tail(s.Text()))
		}
		s.SendLine(":quit")
		waitExit(t, s)
	})

	t.Run("esc and ctrl-c keep the prompt semantics", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "text", blockUntilCancel: true})
		defer server.Close()
		s := startChat(t, binary, server.URL, t.TempDir(), t.TempDir())
		mark := s.Mark()
		expect(t, s, `❯ `, mark)

		mark = s.Mark()
		s.SendLine("interrupt")
		expect(t, s, `↳ `, mark)
		s.Send("\x1b")
		expect(t, s, `❯`, mark)

		mark = s.Mark()
		s.Send("abc")
		s.SendCtrlC()
		expect(t, s, `❯`, mark)

		mark = s.Mark()
		s.SendCtrlC()
		expect(t, s, `再按一次 Ctrl\+C 退出`, mark)
		s.SendCtrlC()
		expect(t, s, `续接：suga chat --resume`, mark)
		waitExit(t, s)
	})

	t.Run("approval accepts and rejects file write", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "approval"})
		defer server.Close()
		work := t.TempDir()
		s := startChat(t, binary, server.URL, t.TempDir(), work)
		mark := s.Mark()
		expect(t, s, `❯ `, mark)

		s.SendLine("write it")
		expect(t, s, `允许执行 file_write`, mark)
		s.SendLine("y")
		expect(t, s, `工具处理完成`, mark)
		if body, err := os.ReadFile(filepath.Join(work, "approved.txt")); err != nil || string(body) != "APPROVED" {
			t.Fatalf("accepted write = %q, %v; terminal tail:\n%s", body, err, tail(s.Text()))
		}

		mark = s.Mark()
		s.SendLine("reject it")
		expect(t, s, `允许执行 file_write`, mark)
		s.SendLine("n")
		expect(t, s, `用户拒绝执行 file_write`, mark)
		if _, err := os.Stat(filepath.Join(work, "rejected.txt")); !os.IsNotExist(err) {
			t.Fatalf("rejected write exists or stat failed: %v; terminal tail:\n%s", err, tail(s.Text()))
		}
		s.SendLine(":quit")
		waitExit(t, s)
	})
	t.Run("local commands and unknown colon input", func(t *testing.T) {
		fake := &fakeChat{mode: "text"}
		server := httptest.NewServer(fake)
		defer server.Close()
		s := startChat(t, binary, server.URL, t.TempDir(), t.TempDir())
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		for _, command := range []string{":help", ":session -h", ":tasks", ":skills", ":context", ":sessio"} {
			mark = s.Mark()
			s.SendLine(command)
			expect(t, s, `❯`, mark)
		}
		mark = s.Mark()
		s.SendLine(":)")
		expect(t, s, `收到：\:\)`, mark)
		fake.mu.Lock()
		got := fake.lastUser
		fake.mu.Unlock()
		if got != ":)" {
			t.Fatalf("colon text was not sent as a message: %q", got)
		}
		s.SendLine(":quit")
		waitExit(t, s)
	})

	t.Run("bracketed paste is one message", func(t *testing.T) {
		fake := &fakeChat{mode: "text"}
		server := httptest.NewServer(fake)
		defer server.Close()
		s := startChat(t, binary, server.URL, t.TempDir(), t.TempDir())
		mark := s.Mark()
		expect(t, s, `❯ `, mark)
		s.Send("\x1b[200~first\nsecond\x1b[201~")
		time.Sleep(250 * time.Millisecond)
		fake.mu.Lock()
		calls := fake.calls
		fake.mu.Unlock()
		if calls != 0 {
			t.Fatalf("paste submitted before Enter, calls=%d", calls)
		}
		s.Send("\r")
		expect(t, s, `收到：first`, mark)
		expect(t, s, `❯`, mark)
		fake.mu.Lock()
		got := fake.lastUser
		fake.mu.Unlock()
		if got != "first\nsecond" {
			t.Fatalf("pasted message = %q", got)
		}
		s.SendCtrlC()
		s.SendCtrlC()
		waitExit(t, s)

	})

	t.Run("steering input is visible while a turn runs", func(t *testing.T) {
		server := httptest.NewServer(&fakeChat{mode: "text", delay: 700 * time.Millisecond})
		defer server.Close()
		s := startChat(t, binary, server.URL, t.TempDir(), t.TempDir())
		mark := s.Mark()
		expect(t, s, `❯`, mark)
		s.SendLine("first")
		expect(t, s, `↳`, mark)
		s.Send("queued\t")
		expect(t, s, `排队`, mark)
		expect(t, s, `收到：first`, mark)
		s.SendCtrlC()
		s.SendCtrlC()
		waitExit(t, s)
	})
}

func buildSuga(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "suga"+goexe())
	cmd := exec.Command("go", "build", "-o", out, "./cmd/suga")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build suga: %v\n%s", err, output)
	}
	return out
}

func startChat(t *testing.T, binary, baseURL, home, work string) *ptytest.Session {
	t.Helper()
	return startChatSpec(t, binary, baseURL, home, work, nil, "")
}

// startChatSpec is startChat with extra arguments for the child and extra lines
// under tools: (e.g. approvalAuto), so a scenario can resume a session or run a
// tool without a question.
func startChatSpec(t *testing.T, binary, baseURL, home, work string, args []string, toolsExtra string) *ptytest.Session {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`model: fake/m1
providers:
  fake:
    type: openai
    base_url: %s
    api_key: test
tools:
  allow_write: true
%smemory:
  project:
    backend: files
chat:
  markdown: false
  spinner: false
  highlight_input: false
  terminal_title: false
  history: false
`, baseURL, toolsExtra)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	spec := gopty.Spec{
		Command: binary,
		Args:    append([]string{"chat"}, args...),
		Dir:     work,
		Env:     testEnv(home),
		Cols:    100,
		Rows:    40,
	}
	return ptytest.StartSpec(t, spec)
}

// testEnv removes inherited suga settings so the temporary config is the only
// source of model, home, hook and feature settings for the child process.
func testEnv(home string) []string {
	env := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		key := entry
		if i := strings.IndexByte(key, '='); i >= 0 {
			key = key[:i]
		}
		if strings.HasPrefix(key, "SUGA_") || key == "NO_COLOR" {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"SUGA_HOME="+home,
		"SUGA_HOOKS=off",
		"NO_COLOR=1",
		"TERM=xterm",
	)
}

func expect(t *testing.T, s *ptytest.Session, pattern string, mark int) {
	t.Helper()
	if !s.Expect(pattern, mark, 15*time.Second) {
		t.Fatalf("expected %q; terminal tail:\n%s", pattern, tail(s.Text()))
	}
}

func waitExit(t *testing.T, s *ptytest.Session) {
	t.Helper()
	code, err := s.WaitExit(15 * time.Second)
	if err != nil || code != 0 {
		t.Fatalf("chat exit = (%d, %v); terminal tail:\n%s", code, err, tail(s.Text()))
	}
}

func tail(text string) string {
	const max = 4000
	if len(text) <= max {
		return text
	}
	return text[len(text)-max:]
}

func goexe() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
