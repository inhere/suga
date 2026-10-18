package wiring

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

func TestBasePromptDebugNonce(t *testing.T) {
	t.Run("unset has no nonce", func(t *testing.T) {
		t.Setenv(debugPromptNonceEnv, "")
		p := basePrompt()
		assert.Eq(t, false, strings.Contains(p, "nonce:"))
		assert.Eq(t, true, strings.HasPrefix(p, "You are suga"))
	})
	t.Run("set goes first", func(t *testing.T) {
		t.Setenv(debugPromptNonceEnv, "abc123")
		p := basePrompt()
		assert.Eq(t, true, strings.HasPrefix(p, "nonce: abc123\nYou are suga"))
	})
}

func TestBasePrompt(t *testing.T) {
	t.Run("identity and conventions only", func(t *testing.T) {
		p := basePrompt()
		assert.StrContains(t, p, `"Found, not touched"`)
		assert.StrContains(t, p, "contradict the actual code")
		// Workspace, mode and skills live in the segment context (design D1).
		assert.NotContains(t, p, "# Environment")
		assert.NotContains(t, p, "Mode:")
		assert.NotContains(t, p, "Workspace")
		assert.NotContains(t, p, "# Skills")
	})
	t.Run("build and test output is left to the minimizer", func(t *testing.T) {
		p := basePrompt()
		assert.StrContains(t, p, "is minimized into a summary plus the failure details")
		assert.StrContains(t, p, "`| Select-Object -Last`")
		assert.StrContains(t, p, "do not pass `-q` to mvn or gradle")
	})
	t.Run("dependency APIs are named per language", func(t *testing.T) {
		p := basePrompt()
		assert.StrContains(t, p, "`go doc <package>`")
		assert.StrContains(t, p, "`javap -public -cp <jar> <fully.qualified.Class>`")
		assert.StrContains(t, p, "~/.m2/repository")
		// The line is assembled from the language registry (suga-xt0o), so its
		// wording is locked here: it is part of the cached prompt prefix, and a
		// new clause must not change the existing one.
		want := "- To learn a dependency's API, use one command instead of reading its files one by one: " +
			"`go doc <package>` (or a grep for the exported names) for Go; " +
			"for Java, `javap -public -cp <jar> <fully.qualified.Class>` with the jar under " +
			"~/.m2/repository/<group path>/<artifact>/<version>/ (`mvn -o dependency:sources` fetches the sources)."
		assert.Eq(t, want, promptLine(p, "- To learn a dependency's API"))
	})
	t.Run("refs first for callers", func(t *testing.T) {
		p := basePrompt()
		// A line of the stable prefix (research 2026-10-18-explore-efficiency
		// section 9.7 candidate 1); its wording is locked here.
		assert.Eq(t, "- To answer who calls X, where X is used or which callers X has, start with file_refs "+
			"instead of guessing a file_grep pattern; for Go its result already lists every occurrence, so "+
			"read the few places you need and do not re-check with file_grep. grep is still for text that is "+
			"not an identifier (a string, a config key, a comment).", promptLine(p, "- To answer who calls X"))
	})
	t.Run("no date or time", func(t *testing.T) {
		re := regexp.MustCompile(`\d{4}-\d{2}-\d{2}|\d{2}:\d{2}`)
		assert.False(t, re.MatchString(basePrompt()))
		assert.False(t, re.MatchString(environmentPrompt(Options{Workspace: "/w"})))
	})
}

// promptLine returns the line of p that starts with prefix, or the empty string
// when p holds none.
func promptLine(p, prefix string) string {
	for _, line := range strings.Split(p, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func TestEnvironmentPrompt(t *testing.T) {
	t.Run("read-only names the enable hint for the entry", func(t *testing.T) {
		p := environmentPrompt(Options{Workspace: "/w", CLIMode: true})
		assert.True(t, strings.HasPrefix(p, "# Environment\n"))
		assert.StrContains(t, p, "Mode: read-only")
		assert.StrContains(t, p, "加 --allow-write")
		assert.StrContains(t, p, "Workspace (file tools are confined to it): /w")
		assert.NotContains(t, p, "Shell for shell_exec")
	})
	t.Run("read roots are listed", func(t *testing.T) {
		resetConfig(t)
		dir := t.TempDir()
		assert.NoErr(t, gkconfig.Set("tools.dependency_roots", false))
		assert.NoErr(t, gkconfig.Set("tools.read_roots", []string{dir}))
		p := environmentPrompt(Options{Workspace: "/w"})
		assert.StrContains(t, p, "Read-only roots outside the workspace")
		assert.StrContains(t, p, "  - configured: "+dir)
		assert.Eq(t, []string{dir}, nativeEnv("/w", nil).ReadRoots)
	})
	t.Run("no read roots, no section", func(t *testing.T) {
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.dependency_roots", false))
		assert.NotContains(t, environmentPrompt(Options{Workspace: "/w"}), "Read-only roots")
		assert.Empty(t, nativeEnv("/w", nil).ReadRoots)
	})
	t.Run("writable names the shell", func(t *testing.T) {
		p := environmentPrompt(Options{Workspace: "/w", AllowWrite: true})
		assert.StrContains(t, p, "Mode: writable")
		assert.StrContains(t, p, "Shell for shell_exec: ")
	})
	t.Run("preferred tools list only present entries and fixed usage hint", func(t *testing.T) {
		p := environmentPrompt(Options{Workspace: "/w", AllowWrite: true, PreferredTools: []PreferredToolStatus{
			{Name: "rg", Kind: "command", When: "search text", Present: true},
			{Name: "offline", Kind: "mcp", When: "graph", Present: false},
		}})
		assert.StrContains(t, p, "Preferred tool: rg (command) — search text")
		assert.StrContains(t, p, "`--help`")
		assert.StrContains(t, p, "record learned usage in memory")
		assert.NotContains(t, p, "offline")
	})
	// A read-only session registers no shell_exec, so a command declaration is
	// not listed there; a connected MCP server is listed in both modes.
	t.Run("preferred commands need shell_exec, MCP does not", func(t *testing.T) {
		statuses := []PreferredToolStatus{
			{Name: "rg", Kind: "command", When: "search text", Present: true},
			{Name: "graph", Kind: "mcp", When: "code graph", Present: true},
		}
		readOnly := environmentPrompt(Options{Workspace: "/w", PreferredTools: statuses})
		assert.StrContains(t, readOnly, "Mode: read-only")
		assert.StrContains(t, readOnly, "Preferred tool: graph (mcp) — code graph")
		assert.NotContains(t, readOnly, "Preferred tool: rg")
		writable := environmentPrompt(Options{Workspace: "/w", AllowWrite: true, PreferredTools: statuses})
		assert.StrContains(t, writable, "Preferred tool: rg (command) — search text")
		assert.StrContains(t, writable, "Preferred tool: graph (mcp) — code graph")
	})
}

// stubInterpreters makes the environment section and the native tool catalog
// see the given interpreters instead of the machine's PATH.
func stubInterpreters(t *testing.T, list []native.Interpreter) {
	t.Helper()
	prev := detectInterpreters
	detectInterpreters = func() []native.Interpreter { return list }
	t.Cleanup(func() { detectInterpreters = prev })
}

// TestEnvironmentPromptInterpreters: the interpreters found on PATH are named
// in the environment section with the write-a-script advice, and nothing at all
// is written when none was found (suga-bkt8).
func TestEnvironmentPromptInterpreters(t *testing.T) {
	t.Run("listed when an interpreter exists", func(t *testing.T) {
		stubInterpreters(t, []native.Interpreter{
			{Name: "python", Path: `C:\Python312\python.exe`, Version: "3.12.4"},
			{Name: "node", Path: `/usr/bin/node`, Version: "22.6.0"},
		})
		p := environmentPrompt(Options{Workspace: "/w", AllowWrite: true})
		assert.StrContains(t, p, "- 可用解释器：python 3.12.4（python）、node 22.6.0（node）。")
		assert.StrContains(t, p, "先用 file_write 把脚本写到工作区 tmp/ 下再用解释器执行")
	})
	t.Run("nothing when no interpreter exists", func(t *testing.T) {
		stubInterpreters(t, nil)
		p := environmentPrompt(Options{Workspace: "/w", AllowWrite: true})
		assert.StrContains(t, p, "Shell for shell_exec: ")
		assert.NotContains(t, p, "可用解释器")
		assert.NotContains(t, p, "先用 file_write")
	})
	t.Run("read-only has no shell_exec", func(t *testing.T) {
		stubInterpreters(t, []native.Interpreter{{Name: "node", Path: "/usr/bin/node", Version: "22.6.0"}})
		assert.NotContains(t, environmentPrompt(Options{Workspace: "/w"}), "可用解释器")
	})
	t.Run("the configuration drives the detection", func(t *testing.T) {
		// No stub here: the real probe runs, bounded by the configured list. A
		// name that exists nowhere yields nothing — were the configuration
		// ignored, auto-detection would find the machine's own python/node and
		// write the line anyway.
		resetConfig(t)
		assert.NoErr(t, gkconfig.Set("tools.interpreters", []any{"definitely-not-an-interpreter-e1"}))
		assert.NotContains(t, environmentPrompt(Options{Workspace: "/w", AllowWrite: true}), "可用解释器")
	})
}

// TestNativeEnvInterpretersReachShellTool: the interpreters the environment
// section names also reach shell_exec's description, through nativeEnv and the
// catalog.
func TestNativeEnvInterpretersReachShellTool(t *testing.T) {
	resetConfig(t)
	assert.NoErr(t, gkconfig.Set("tools.shell", "pwsh"))
	stubInterpreters(t, []native.Interpreter{{Name: "python", Path: `C:\Python312\python.exe`, Version: "3.12.4"}})

	env := nativeEnv(t.TempDir(), nil)
	assert.Eq(t, 1, len(env.Interpreters))
	for _, tool := range native.BuildTools(env, true) {
		if tool.Name() != "shell_exec" {
			continue
		}
		assert.StrContains(t, tool.Description(), "先用 file_write")
		assert.StrContains(t, tool.Description(), "整体加引号")
		return
	}
	assert.Fail(t, "shell_exec is not in the catalog")
}

// requestRecorder is an OpenAI-compatible fake that keeps every request's
// system message and the first non-system message.
type requestRecorder struct {
	mu       sync.Mutex
	systems  []string
	firsts   []string
	rawFirst []map[string]any
}

func (rr *requestRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		rr.mu.Lock()
		for i, m := range body.Messages {
			text, _ := m["content"].(string)
			if m["role"] == "system" {
				rr.systems = append(rr.systems, text)
				continue
			}
			if i <= 1 {
				rr.firsts = append(rr.firsts, text)
				rr.rawFirst = append(rr.rawFirst, m)
			}
		}
		rr.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Replace(okBody, "%s", "x", 1)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSystemPromptIsSentAndStable runs two turns of one session and checks the
// system message carries the base prompt with identical bytes both times,
// while the environment travels in the segment context message.
func TestSystemPromptIsSentAndStable(t *testing.T) {
	resetConfig(t)
	rr := &requestRecorder{}
	srv := rr.server(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": srv.URL}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	rt := openTestRuntime(t, "")
	first, err := rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
	assert.Require(t, assert.NoErr(t, err))
	_, err = rt.Runner.Run(context.Background(), personal.RunRequest{SessionID: first.SessionID, Prompt: "again"})
	assert.Require(t, assert.NoErr(t, err))

	assert.Require(t, assert.Len(t, rr.systems, 2))
	assert.Eq(t, basePrompt(), rr.systems[0])
	assert.Eq(t, rr.systems[0], rr.systems[1])
	assert.Require(t, assert.Len(t, rr.firsts, 2))
	// The read-only hint is in the segment context, not the system prompt.
	assert.StrContains(t, rr.firsts[0], "# Environment")
	assert.StrContains(t, rr.firsts[0], "read_only 模式")
	assert.Eq(t, rr.firsts[0], rr.firsts[1])
	// The segment context marker never reaches the wire.
	_, leaked := rr.rawFirst[0]["metadata"]
	assert.False(t, leaked)
}

// TestSystemPromptSharedAcrossWorkspacesAndModes: sessions in two different
// workspaces (same mode), and in another mode, send byte-identical system
// prompts; only the segment context differs (design D1).
func TestSystemPromptSharedAcrossWorkspacesAndModes(t *testing.T) {
	resetConfig(t)
	rr := &requestRecorder{}
	srv := rr.server(t)
	setProviders(t, map[string]any{"openai": map[string]any{"api_key": "k", "base_url": srv.URL}})
	assert.NoErr(t, gkconfig.Set("model", "openai/gpt-4o"))

	run := func(opts Options) {
		rt, err := Open(context.Background(), opts)
		assert.Require(t, assert.NoErr(t, err))
		defer func() { _ = rt.Close() }()
		_, err = rt.Runner.Run(context.Background(), personal.RunRequest{Prompt: "hi"})
		assert.Require(t, assert.NoErr(t, err))
	}
	one, two := t.TempDir(), t.TempDir()
	run(Options{Workspace: one, RuleCWD: one})
	run(Options{Workspace: two, RuleCWD: two})
	run(Options{Workspace: two, RuleCWD: two, AllowWrite: true})

	assert.Require(t, assert.Len(t, rr.systems, 3))
	assert.Eq(t, rr.systems[0], rr.systems[1])
	assert.Eq(t, rr.systems[0], rr.systems[2])
	assert.NotContains(t, rr.systems[0], one)
	assert.Require(t, assert.Len(t, rr.firsts, 3))
	assert.StrContains(t, rr.firsts[0], one)
	assert.StrContains(t, rr.firsts[1], two)
	assert.StrContains(t, rr.firsts[2], "Mode: writable")
}
