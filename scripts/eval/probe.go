package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
	"time"
)

// probeNonceEnv must match internal/wiring's hidden debug variable: a
// non-empty value is put first in the system prompt, defeating prefix caches.
const probeNonceEnv = "SUGA_DEBUG_PROMPT_NONCE"

// probePrompt is the minimal request every probe run sends.
const probePrompt = "只回复 ok"

// ProbeRun is one probe.jsonl line: the first model call of a measured run and
// the input of the warm-up request that built the cache just before it.
type ProbeRun struct {
	Group      string `json:"group"` // same | cross | control
	Index      int    `json:"index"`
	Workspace  string `json:"workspace"`
	WarmInput  int64  `json:"warm_input"` // input of the warm-up's first call, cache included
	Input      int64  `json:"input_tokens"`
	CacheRead  int64  `json:"cache_read_tokens"`
	CacheWrite int64  `json:"cache_write_tokens"`
	Uncached   int64  `json:"uncached_tokens"`
	Error      string `json:"error,omitempty"`
}

// runProbe measures whether the request prefix (system prompt + tools) is
// served from the provider cache across sessions and workspaces. Every group
// repeats probeRepeat times; each repetition first sends a warm-up request
// (not counted) and then the measured one, both in fresh SUGA_HOMEs and fresh
// sessions, o.probeGap apart:
//
//	same    warm-up and run use the same workspace path (<out>/probe/ws)
//	cross   warm-up uses <out>/probe/ws-warm, the run <out>/probe/ws-<i>
//	control same as "same", but warm-up and run carry different random
//	        SUGA_DEBUG_PROMPT_NONCE values, so no cache hit is possible
func runProbe(o *options, bin, runID string) error {
	ctx := context.Background()
	root := filepath.Join(o.out, "probe")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	port := 0
	if o.fake {
		stop, p, err := startFake(ctx, "text", "")
		if err != nil {
			return err
		}
		defer stop()
		port = p
	}
	jf, err := os.Create(filepath.Join(o.out, "probe.jsonl"))
	if err != nil {
		return err
	}
	defer jf.Close()
	enc := json.NewEncoder(jf)

	p := &prober{o: o, bin: bin, runID: runID, root: root, port: port}
	var all []ProbeRun
	for _, group := range []string{"same", "cross", "control"} {
		for i := 1; i <= o.probeRepeat; i++ {
			wsWarm, ws := filepath.Join(root, "ws"), filepath.Join(root, "ws")
			if group == "cross" {
				wsWarm, ws = filepath.Join(root, "ws-warm"), filepath.Join(root, fmt.Sprintf("ws-%d", i))
			}
			warmNonce, nonce := "", ""
			if group == "control" {
				warmNonce, nonce = randHex(), randHex()
			}
			r := ProbeRun{Group: group, Index: i, Workspace: ws}
			if i > 1 || group != "same" {
				time.Sleep(o.probeGap)
			}
			warm, werr := p.request(ctx, fmt.Sprintf("%s-%d-warm", group, i), wsWarm, warmNonce)
			if werr != nil {
				r.Error = "warm-up: " + werr.Error()
			} else {
				r.WarmInput = warm.Input
				time.Sleep(o.probeGap)
				c, err := p.request(ctx, fmt.Sprintf("%s-%d", group, i), ws, nonce)
				if err != nil {
					r.Error = err.Error()
				} else {
					r.Input, r.CacheRead, r.CacheWrite, r.Uncached = c.Input, c.CacheRead, c.CacheWrite, c.uncached()
				}
			}
			all = append(all, r)
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	}
	printProbeTable(os.Stdout, all)
	fmt.Printf("\nprobe: %s\n", filepath.Join(o.out, "probe.jsonl"))
	return nil
}

type prober struct {
	o     *options
	bin   string
	runID string
	root  string
	port  int // fake server port; 0 with a real model
}

// request resets ws, runs one minimal prompt in a fresh SUGA_HOME and session
// and returns the first main-loop model call from its trace.
func (p *prober) request(ctx context.Context, name, ws, nonce string) (traceCall, error) {
	if err := os.RemoveAll(ws); err != nil {
		return traceCall{}, err
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return traceCall{}, err
	}
	if out, err := exec.Command("git", "-C", ws, "init", "-q").CombinedOutput(); err != nil {
		return traceCall{}, fmt.Errorf("git init: %v: %s", err, out)
	}
	home := filepath.Join(p.root, "home-"+name)
	if err := os.MkdirAll(home, 0o755); err != nil {
		return traceCall{}, err
	}
	model := p.o.model
	if p.o.fake {
		if err := writeConfig(home, "", p.port, p.o.sugaSet); err != nil {
			return traceCall{}, err
		}
		if model == "" {
			model = "fake/m1"
		}
	} else if err := writeConfig(home, p.o.configFrom, 0, p.o.sugaSet); err != nil {
		return traceCall{}, err
	}
	env := isolatedEnv(home)
	if nonce != "" {
		env = append(env, probeNonceEnv+"="+nonce)
	}
	rctx, cancel := context.WithTimeout(ctx, p.o.timeout)
	defer cancel()
	sid := "probe-" + p.runID + "-" + name
	if _, err := runCmd(rctx, p.bin, sugaRunArgs(sid, probePrompt, model, false, nil), ws, env); err != nil {
		return traceCall{}, err
	}
	tout, err := runCmd(ctx, p.bin, []string{"inspect", "trace", sid, "--json"}, ws, env)
	if err != nil {
		return traceCall{}, err
	}
	c, ok := firstMainCall(tout)
	if !ok {
		return traceCall{}, fmt.Errorf("no successful model event in trace of %s", sid)
	}
	return c, nil
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// probeStat is the per-group aggregate shown in the table.
type probeStat struct {
	n, failed                         int
	cacheRead, uncached, warm, ratioN float64
	ratio                             float64
}

// aggregateProbe averages the successful runs per group. RATIO is the mean of
// per-run cache_read / input (input includes cache), so every run weighs the same.
func aggregateProbe(rs []ProbeRun) map[string]*probeStat {
	m := map[string]*probeStat{}
	for _, r := range rs {
		s := m[r.Group]
		if s == nil {
			s = &probeStat{}
			m[r.Group] = s
		}
		s.n++
		if r.Error != "" {
			s.failed++
			continue
		}
		s.cacheRead += float64(r.CacheRead)
		s.uncached += float64(r.Uncached)
		s.warm += float64(r.WarmInput)
		if r.Input > 0 {
			s.ratio += float64(r.CacheRead) / float64(r.Input)
		}
		s.ratioN++
	}
	return m
}

// printProbeTable prints one row per group. PREFIX~ is the mean input of the
// warm-up request (system + tools + the tiny user turn, cache included): a
// rough size of the cacheable prefix, to read CACHE_READ against.
func printProbeTable(w io.Writer, rs []ProbeRun) {
	stats := aggregateProbe(rs)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tN\tFAILED\tAVG_CACHE_READ\tAVG_UNCACHED\tREAD/FIRST%\tPREFIX~(warm input)")
	for _, g := range []string{"same", "cross", "control"} {
		s := stats[g]
		if s == nil {
			continue
		}
		d := max(s.ratioN, 1)
		fmt.Fprintf(tw, "%s\t%d\t%d\t%.0f\t%.0f\t%.1f\t%.0f\n", g, s.n, s.failed,
			s.cacheRead/d, s.uncached/d, s.ratio/d*100, s.warm/d)
	}
	_ = tw.Flush()
}
