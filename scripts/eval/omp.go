package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runOmp runs one task with oh-my-pi (`omp -p --mode json`) in the prepared
// workspace and fills r from its JSON event stream. omp keeps its own auth and
// settings (read only here); sessions go to the temporary <sessionDir> so a
// multi-turn task can continue (--continue from the second turn on) without
// touching the user's real sessions.
//
// Unlike `suga run`, omp has no read-only mode switch, so read-only tasks run
// with omp's full tool set; the comparison notes this.
func runOmp(ctx context.Context, o *options, t *Task, timeout time.Duration, ws, sessionDir string, r *Result) {
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		r.Error = err.Error()
		return
	}
	before := snapshotWorkspace(ws)
	var allOut []byte
	start := time.Now()
	var err error
	turns := t.Turns()
	for i, prompt := range turns {
		args := []string{"-p", "--mode", "json", "--auto-approve", "--session-dir", sessionDir, "--no-title",
			"--cwd", ws, "--max-time", fmt.Sprintf("%ds", int(timeout.Seconds()))}
		if i > 0 {
			args = append(args, "--continue")
		}
		if o.model != "" {
			args = append(args, "--model", o.model)
		}
		args = append(args, splitArgs(o.ompArgs)...)
		args = append(args, prompt) // the message is the last positional argument
		var out []byte
		out, err = runOmpOnce(ctx, o.ompBin, args, ws)
		allOut = append(allOut, out...)
		if err != nil {
			r.Error = fmt.Sprintf("omp turn %d: %v", i+1, err)
			break
		}
	}
	r.DurationMs = time.Since(start).Milliseconds()
	// Usage is summed over all turns; the answer is the last assistant text
	// (parseOmpEvents keeps overwriting it), i.e. the last turn's.
	parseOmpEvents(allOut, r)
	t.finish(ws, before, r.Answer, r)
	r.Passed = err == nil && allPassed(r.Checks)
}

// runOmpOnce runs one omp invocation and returns its stdout; a failure's error
// includes the tail of stderr.
func runOmpOnce(ctx context.Context, bin string, args []string, ws string) ([]byte, error) {

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = ws
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

// ompMessage is the subset of an omp assistant message the eval reads.
type ompMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"` // toolCall blocks
	} `json:"content"`
	Usage *struct {
		Input      int64 `json:"input"` // excludes cache reads/writes
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
		Cost       struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

// parseOmpEvents sums usage over every assistant message_end (one per model
// call), counts tool calls in their content and keeps the text of the last
// assistant message as the answer.
func parseOmpEvents(raw []byte, r *Result) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20) // message_update lines can be large
	started, fromMsgs := map[string]int{}, map[string]int{}
	defer func() { // tool_execution_start is authoritative; toolCall blocks are the fallback
		if len(started) > 0 {
			r.ToolCounts = started
		} else if len(fromMsgs) > 0 {
			r.ToolCounts = fromMsgs
		}
	}()
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev struct {
			Type     string     `json:"type"`
			ToolName string     `json:"toolName"`
			Message  ompMessage `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type == "tool_execution_start" && ev.ToolName != "" {
			started[ev.ToolName]++
			continue
		}
		if ev.Type != "message_end" || ev.Message.Role != "assistant" {
			continue
		}
		m := ev.Message
		if m.Usage != nil {
			if r.FirstUncached == nil { // first assistant message_end; omp's input excludes cache
				v := m.Usage.Input
				r.FirstUncached = &v
			}
			r.Calls++
			r.InputTokens += m.Usage.Input
			r.OutputToks += m.Usage.Output
			r.CacheRead += m.Usage.CacheRead
			r.CacheWrite += m.Usage.CacheWrite
			r.CostUSD += m.Usage.Cost.Total
		}
		var text []string
		for _, c := range m.Content {
			switch {
			case strings.Contains(strings.ToLower(c.Type), "tool"):
				r.ToolCalls++
				if c.Name != "" {
					fromMsgs[c.Name]++
				}
			case c.Type == "text" && strings.TrimSpace(c.Text) != "":
				text = append(text, c.Text)
			}
		}
		if len(text) > 0 {
			r.Answer = strings.Join(text, "\n")
		}
	}
	if total := r.InputTokens + r.CacheRead + r.CacheWrite; total > 0 {
		r.CacheHit = float64(r.CacheRead) / float64(total)
	}
}
