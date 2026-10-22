package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runClaude runs one task with Claude Code (`claude -p --output-format
// stream-json`) in the prepared workspace and fills r from its event stream.
//
// Isolation: CLAUDE_CONFIG_DIR points at <configDir>, whose settings.json
// holds only the user's settings `env` (model mapping, base URL and token of
// the gateway) plus disableAllHooks, so the user's hooks, skills, agents,
// plugins, memory and sessions stay out; --strict-mcp-config with no
// --mcp-config loads no MCP servers. settings.json carries the gateway token:
// it is written 0600 and removed when the run ends, like the copied suga .env.
func runClaude(ctx context.Context, o *options, t *Task, timeout time.Duration, ws, configDir string, r *Result) {
	if found := ancestorInstructions(ws); found != "" {
		// Claude Code reads CLAUDE.md (and, through its built-in plugin,
		// AGENTS.md) from the working directory up to the root, past the
		// workspace's git root; omp and suga stop there. An eval inside
		// another project would hand claude that project's instructions.
		r.Error = fmt.Sprintf("claude would read %s above the workspace; use --out outside that tree", found)
		return
	}
	if err := writeClaudeSettings(o.claudeConfigFrom, configDir); err != nil {
		r.Error = err.Error()
		return
	}
	defer os.Remove(filepath.Join(configDir, "settings.json"))

	before := snapshotWorkspace(ws)

	var allOut []byte
	start := time.Now()
	var err error
	for i, prompt := range t.Turns() {
		args := []string{"-p", "--output-format", "stream-json", "--verbose",
			"--dangerously-skip-permissions", "--strict-mcp-config"}
		if i > 0 {
			args = append(args, "--continue")
		}
		if o.model != "" {
			args = append(args, "--model", o.model)
		}
		if r.Web == webOff { // the same default as suga: no web tools while scoring
			args = append(args, "--disallowedTools", "WebFetch,WebSearch")
		}
		args = append(args, splitArgs(o.claudeArgs)...)
		args = append(args, prompt)
		var out []byte
		out, err = runClaudeOnce(ctx, o.claudeBin, args, ws, configDir)
		allOut = append(allOut, out...)
		if err != nil {
			r.Error = fmt.Sprintf("claude turn %d: %v", i+1, err)
			break
		}
	}
	r.DurationMs = time.Since(start).Milliseconds()
	// The raw event stream, for analysis (no settings or token in it).
	_ = os.WriteFile(filepath.Join(filepath.Dir(configDir), "claude-events.jsonl"), allOut, 0o644)
	parseClaudeEvents(allOut, r)
	t.finish(ws, before, answersOf(r.Answer), r)
	r.Passed = err == nil && allPassed(r.Checks)
}

// writeClaudeSettings writes <configDir>/settings.json from the env of
// <from>/settings.json (default ~/.claude), with hooks off.
func writeClaudeSettings(from, configDir string) error {
	if from == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		from = filepath.Join(home, ".claude")
	}
	raw, err := os.ReadFile(filepath.Join(from, "settings.json"))
	if err != nil {
		return fmt.Errorf("--claude-config-from: %w", err)
	}
	var user struct {
		Env map[string]any `json:"env"`
	}
	if err := json.Unmarshal(raw, &user); err != nil {
		return fmt.Errorf("--claude-config-from settings.json: %w", err)
	}
	if len(user.Env) == 0 {
		return errors.New("--claude-config-from settings.json has no env (gateway URL and token)")
	}
	settings := map[string]any{
		"env":                               user.Env,
		"disableAllHooks":                   true,
		"skipDangerousModePermissionPrompt": true,
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, "settings.json"), out, 0o600)
}

// runClaudeOnce runs one claude invocation with CLAUDE_CONFIG_DIR set and
// returns its stdout; a failure's error includes the tail of stderr.
func runClaudeOnce(ctx context.Context, bin string, args []string, ws, configDir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(),
		"CLAUDE_CONFIG_DIR="+configDir,
		"DISABLE_AUTOUPDATER=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return stdout.Bytes(), fmt.Errorf("%v: %s", err, tail)
	}
	return stdout.Bytes(), nil
}

// claudeUsage is the usage object of an assistant message.
type claudeUsage struct {
	Input      int64 `json:"input_tokens"` // excludes cache reads/writes
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

// parseClaudeEvents reads the stream-json events: one API response may be
// split over several assistant events sharing its message id, so calls are
// counted per id and tool_use blocks by name. Token usage comes from the
// result event of each turn (summed; modelUsage when present, which includes
// subagent calls): through a gateway the per-message usage is all zero. The per-message usage (last seen per id) is used only
// when the result events report none; it also gives the first call's
// uncached input. The answer is the text of the last result event.
func parseClaudeEvents(raw []byte, r *Result) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	usage := map[string]claudeUsage{}
	var order []string
	tools := map[string]int{}
	var turns claudeUsage // summed over the result events
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				ID      string `json:"id"`
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content"`
				Usage *claudeUsage `json:"usage"`
			} `json:"message"`
			Result       string       `json:"result"`
			TotalCostUSD float64      `json:"total_cost_usd"`
			Usage        *claudeUsage `json:"usage"`
			// ModelUsage covers subagent calls too; usage only the main loop.
			ModelUsage map[string]struct {
				Input      int64 `json:"inputTokens"`
				Output     int64 `json:"outputTokens"`
				CacheRead  int64 `json:"cacheReadInputTokens"`
				CacheWrite int64 `json:"cacheCreationInputTokens"`
			} `json:"modelUsage"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			id := ev.Message.ID
			if _, seen := usage[id]; !seen {
				order = append(order, id)
				usage[id] = claudeUsage{}
			}
			if ev.Message.Usage != nil {
				usage[id] = *ev.Message.Usage
			}
			for _, c := range ev.Message.Content {
				if c.Type == "tool_use" {
					r.ToolCalls++
					tools[c.Name]++
				}
			}
		case "result":
			r.Answer = ev.Result
			r.CostUSD += ev.TotalCostUSD
			if len(ev.ModelUsage) > 0 {
				for _, u := range ev.ModelUsage {
					turns.Input, turns.Output = turns.Input+u.Input, turns.Output+u.Output
					turns.CacheRead, turns.CacheWrite = turns.CacheRead+u.CacheRead, turns.CacheWrite+u.CacheWrite
				}
			} else if u := ev.Usage; u != nil {
				turns.Input, turns.Output = turns.Input+u.Input, turns.Output+u.Output
				turns.CacheRead, turns.CacheWrite = turns.CacheRead+u.CacheRead, turns.CacheWrite+u.CacheWrite
			}
		}
	}
	var perMessage claudeUsage
	for i, id := range order {
		u := usage[id]
		if i == 0 && u != (claudeUsage{}) {
			v := u.Input
			r.FirstUncached = &v
		}
		r.Calls++
		perMessage.Input, perMessage.Output = perMessage.Input+u.Input, perMessage.Output+u.Output
		perMessage.CacheRead, perMessage.CacheWrite = perMessage.CacheRead+u.CacheRead, perMessage.CacheWrite+u.CacheWrite
	}
	total := turns
	if total == (claudeUsage{}) {
		total = perMessage
	}
	r.InputTokens, r.OutputToks, r.CacheRead, r.CacheWrite = total.Input, total.Output, total.CacheRead, total.CacheWrite
	if len(tools) > 0 {
		r.ToolCounts = tools
	}
	if total := r.InputTokens + r.CacheRead + r.CacheWrite; total > 0 {
		r.CacheHit = float64(r.CacheRead) / float64(total)
	}
}

// ancestorInstructions returns the first instruction file (CLAUDE.md,
// CLAUDE.local.md, AGENTS.md, .claude/CLAUDE.md) in a directory above ws, ""
// when there is none.
func ancestorInstructions(ws string) string {
	dir := filepath.Clean(ws)
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
		for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", filepath.Join(".claude", "CLAUDE.md")} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return filepath.Join(dir, name)
			}
		}
	}
}
