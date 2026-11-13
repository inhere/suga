//go:build ptye2e

package ptye2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	preferredFoundName   = "preferred-smoke-command"
	preferredMissingName = "suga-missing-preferred-command"
	preferredMCPName     = "preferred-mcp"
	preferredMCPTool     = "preferred_lookup"
	preferredMCPSecret   = "SMOKE_MCP_SECRET"
	// preferredReadonlyPrompt identifies the read-only run's request in the fake
	// model's log (the Python script did the same with READONLY_PROMPT).
	preferredReadonlyPrompt = "只读优先项"
	// mcpHelperEnv switches TestMCPHelperProcess from a skipped test into the stdio
	// MCP server the chat connects to; mcpLogEnv names the file it logs to.
	mcpHelperEnv = "PTYE2E_MCP_HELPER"
	mcpLogEnv    = "PTYE2E_MCP_LOG"
)

// TestMCPHelperProcess is not a test: the chat's mcp.servers entry re-runs this
// test binary with -test.run=^TestMCPHelperProcess$ (the Python smoke started a
// tiny python server instead). It appends every method it is asked for to the
// log file and answers initialize / tools/list (one preferred_lookup tool) /
// tools/call (PREFERRED_MCP_OK). It exits before the test framework prints its
// summary, so nothing but JSON-RPC reaches stdout.
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv(mcpHelperEnv) != "1" {
		return
	}
	logPath := os.Getenv(mcpLogEnv)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		logMCPMethod(logPath, request.Method)
		if len(request.ID) == 0 || string(request.ID) == "null" {
			continue // a notification (notifications/initialized) needs no answer
		}
		result := map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo":   map[string]any{"name": "preferred-smoke", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": preferredMCPTool, "description": "preferred smoke lookup",
				"inputSchema": map[string]any{"type": "object",
					"properties": map[string]any{"query": map[string]any{"type": "string"}}}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "PREFERRED_MCP_OK"}}}
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		fmt.Println(string(response))
	}
	os.Exit(0)
}

// logMCPMethod appends one method name to the helper's log file.
func logMCPMethod(path, method string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, method)
}

// TestPreferredTools ports scripts/ptysmoke/preferred_tools.py: the environment
// lists the found command and the connected preferred MCP server but not the
// missing command, a preferred MCP tool is directly visible with
// `tools.deferred: always` (no tool_search) while its call still needs
// permission, the turn_context trace names exactly the listed tools, the
// standalone manifest reports presence without starting the server, and the
// read-only run of the same configuration keeps the MCP server but drops the
// command.
//
// Python check -> Go assertion:
//
//	banner / prompt                                   Expect(Suga chat · fake/m1), Expect(❯ )
//	missing command is not shown                      !Contains(preferredMissingName)
//	preferred MCP is not auto-allowed                 Expect(需要许可：preferred_lookup)
//	turn ends / exit: resume command                  Expect(tokens ·), ctrlCTwice
//	trace command succeeds / trace: preferred names match prompt
//	                                                  inspects trace exit 0 and turn_context
//	                                                  attrs.preferred_tools == [found, mcp]
//	trace: preferred observation is names only        trace text has no 搜索文本/SMOKE_SECRET,
//	                                                  the attrs carry no count/hash keys
//	manifest command succeeds / manifest is JSON      `inspect manifest --json` exit 0 + parses
//	manifest found command / missing command reason / MCP without starting it / no secret leak
//	                                                  preferred_tools entries' present+reason
//	MCP server was not started by manifest            the log holds one initialize
//	denied MCP call never reached the server          the log holds no tools/call
//	read-only run exits 0                             `run --read-only --prompt 只读优先项` exit 0
//	found command is listed / preferred MCP is listed / missing command is not listed
//	                                                  first request's "Preferred tool:" lines
//	fixed help and memory hints are listed            --help and memory in those lines
//	fake model saw preferred MCP schema directly      first request offers preferred_lookup
//	preferred MCP did not require tool_search          no tool_search in the first request
//	read-only run still lists the preferred MCP server / leaves the preferred command out
//	                                                  the read-only request's preferred lines
func TestPreferredTools(t *testing.T) {
	binary := buildSuga(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	home, ws, binDir := filepath.Join(work, "home"), filepath.Join(work, "ws"), filepath.Join(work, "bin")
	for _, dir := range []string{home, ws, binDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writePreferredCommand(t, binDir)
	mcpLog := filepath.Join(home, "mcp.log")

	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	preferred := fmt.Sprintf(`  preferred:
    - name: %s
      kind: command
      when: 搜索文本
    - name: %s
      kind: command
      when: 不应出现
    - name: %s
      kind: mcp
      when: 代码图谱
`, preferredFoundName, preferredMissingName, preferredMCPName)
	mcp := fmt.Sprintf(`mcp:
  servers:
    - id: %s
      command: %s
      args:
        - %s
      env:
        %s: "1"
        %s: %s
        SMOKE_SECRET: %s
`, preferredMCPName, quote(self), quote("-test.run=^TestMCPHelperProcess$"),
		mcpHelperEnv, mcpLogEnv, quote(mcpLog), preferredMCPSecret)

	fake := &fakeLLM{Mode: "preferred", Delay: 300 * time.Millisecond, PreferredTool: preferredMCPTool, Reply: "优先工具完成"}
	server := httptest.NewServer(fake)
	defer server.Close()

	// deferred: always would put every MCP tool behind tool_search; the preferred
	// server's tools must stay direct anyway (fakellm.py's comment).
	o := chatOpts{Home: home, Work: ws, URL: server.URL,
		ToolsExtra: "  deferred: always\n" + preferred, Extra: mcp}
	writeChatHome(t, o)
	env := withPath(chatEnv(t, o), binDir)

	// The standalone manifest runs before any MCP server may start: it reports
	// command presence and configured MCP status without probing the server.
	stdout, _, code := sugaRun(t, binary, ws, env, "inspect", "manifest", "--json")
	check(t, "manifest command succeeds", code == 0)
	var manifest struct {
		Preferred []struct {
			Name    string `json:"name"`
			Present bool   `json:"present"`
			Reason  string `json:"reason"`
		} `json:"preferred_tools"`
	}
	isJSON := json.Unmarshal([]byte(stdout), &manifest) == nil
	check(t, "manifest is JSON", isJSON)
	found := map[string]int{}
	for i, item := range manifest.Preferred {
		found[item.Name] = i
	}
	entry := func(name string) (bool, string) {
		if i, ok := found[name]; ok {
			return manifest.Preferred[i].Present, manifest.Preferred[i].Reason
		}
		return false, ""
	}
	present, reason := entry(preferredFoundName)
	check(t, "manifest found command", present && reason == "found on PATH; read-only sessions do not list it")
	present, reason = entry(preferredMissingName)
	check(t, "manifest keeps missing command reason", !present && reason == "not found on PATH")
	present, reason = entry(preferredMCPName)
	check(t, "manifest reports configured MCP without starting it",
		!present && reason == "configured; connection confirmed at session start")
	check(t, "manifest does not leak MCP secret", !strings.Contains(stdout, preferredMCPSecret))

	s, env := startChatIn(t, binary, ws, chatCols, 40, []string{"chat"}, env)
	mark := s.Mark()
	check(t, "banner", s.Expect(`Suga chat · fake/m1`, mark, 15*time.Second))
	check(t, "prompt", s.Expect(`❯ `, mark, 15*time.Second))

	mark = s.Mark()
	s.SendLine("使用优先工具")
	// Preferred is not permission: chat and run deny MCP tools that are not in
	// tools.auto_allow (the existing rule), so the call is refused, not run.
	check(t, "preferred MCP is not auto-allowed", s.Expect(`需要许可：preferred_lookup`, mark, 15*time.Second))
	check(t, "turn ends", s.Expect(`tokens · `, mark, 15*time.Second))
	// The environment section is sent to the model, not shown here.
	check(t, "missing command is not shown", !s.Contains(preferredMissingName, mark))
	id := exitAfterTurn(t, s, mark)
	check(t, "session id", id != "")
	if id == "" {
		return
	}

	traceOut, _, code := sugaRun(t, binary, ws, env, "inspect", "trace", id, "--json")
	check(t, "trace command succeeds", code == 0)
	var turn map[string]any
	for _, line := range strings.Split(traceOut, "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event["kind"] == "turn_context" || event["name"] == "turn_context" {
			turn = event
			break
		}
	}
	var names []string
	if a := attrs(turn); a != nil {
		list, _ := a["preferred_tools"].([]any)
		for _, v := range list {
			text, _ := v.(string)
			names = append(names, text)
		}
	}
	check(t, "trace: preferred names match prompt", strings.Join(names, "|") == preferredFoundName+"|"+preferredMCPName)
	check(t, "trace: preferred observation is names only",
		!strings.Contains(traceOut, "搜索文本") && !strings.Contains(traceOut, "SMOKE_SECRET") &&
			!strings.Contains(strings.Join(names, "|"), preferredMissingName) &&
			attrs(turn)["preferred_count"] == nil && attrs(turn)["preferred_hash"] == nil)

	// Checked before the read-only run, which connects to the same server.
	methods := mcpMethods(t, mcpLog)
	check(t, "MCP server was not started by manifest", countString(methods, "initialize") == 1)
	check(t, "denied MCP call never reached the server", !containsString(methods, "tools/call"))

	// Read-only group: the same configuration without shell_exec. The fake model's
	// requests are inspected below, once the runs are over.
	_, _, code = sugaRun(t, binary, ws, env, "run", "--read-only", "--prompt", preferredReadonlyPrompt)
	check(t, "read-only run exits 0", code == 0)

	requests := fake.Requests()
	var first fakeRequest
	for _, r := range requests {
		if len(r.Tools) > 0 {
			first = r
			break
		}
	}
	listed := strings.Join(first.Preferred, " ")
	check(t, "found command is listed",
		strings.Contains(listed, "Preferred tool: preferred-smoke-command (command) — 搜索文本"))
	check(t, "preferred MCP is listed",
		strings.Contains(listed, "Preferred tool: preferred-mcp (mcp) — 代码图谱"))
	check(t, "missing command is not listed", !strings.Contains(listed, preferredMissingName))
	check(t, "fixed help and memory hints are listed", strings.Contains(listed, "--help") && strings.Contains(listed, "memory"))
	check(t, "fake model saw preferred MCP schema directly", containsString(first.Tools, preferredMCPTool))
	check(t, "preferred MCP did not require tool_search", !containsString(first.Tools, "tool_search"))

	// The read-only run's own request: no shell_exec there, so the environment
	// keeps the connected MCP server and drops the command.
	var readonly fakeRequest
	for _, r := range requests {
		if r.User == preferredReadonlyPrompt {
			readonly = r
		}
	}
	readonlyListed := strings.Join(readonly.Preferred, " ")
	check(t, "read-only run still lists the preferred MCP server",
		strings.Contains(readonlyListed, "Preferred tool: preferred-mcp (mcp) — 代码图谱"))
	check(t, "read-only run leaves the preferred command out",
		!strings.Contains(readonlyListed, preferredFoundName) && !strings.Contains(readonlyListed, preferredMissingName))
}

// writePreferredCommand puts the command the environment must list on PATH: a
// no-op script, a .cmd on Windows where PATH lookup takes PATHEXT.
func writePreferredCommand(t *testing.T, dir string) {
	t.Helper()
	name, body := preferredFoundName, "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name, body = preferredFoundName+".cmd", "@exit 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// withPath prepends dir to the env's PATH entry (its key is "Path" on Windows),
// so the session finds the fixture command and nothing else is lost.
func withPath(env []string, dir string) []string {
	out := make([]string, 0, len(env))
	sep := string(os.PathListSeparator)
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PATH") {
			out = append(out, key+"="+dir+sep+value)
			continue
		}
		out = append(out, entry)
	}
	return out
}

// mcpMethods reads the methods the MCP helper logged (one per line).
func mcpMethods(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			methods = append(methods, line)
		}
	}
	return methods
}

// countString counts the entries equal to value.
func countString(list []string, value string) int {
	n := 0
	for _, v := range list {
		if v == value {
			n++
		}
	}
	return n
}
