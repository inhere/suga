package gofermem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/memory"
)

// The test binary doubles as a fake gofer: when fakeStateEnv is set, TestMain
// runs fakeGofer instead of the tests. The real gofer is never executed.
const (
	fakeStateEnv     = "FAKE_GOFER_STATE"      // JSON file holding []record
	fakeLogEnv       = "FAKE_GOFER_LOG"        // JSON lines: one call per line
	fakeIgnoreTagEnv = "FAKE_GOFER_IGNORE_TAG" // "1": ls ignores --tag (proves the local filter)
	fakeFailEnv      = "FAKE_GOFER_FAIL"       // "1": every call fails with a non-not-found error
	fakeSleepEnv     = "FAKE_GOFER_SLEEP"      // "1": hang (timeout test)
	fakeShowUntagEnv = "FAKE_GOFER_SHOW_UNTAG" // "1": show drops the tags (re-tagged since the listing)
	fakeNoTitleEnv   = "FAKE_GOFER_NO_TITLE"   // "1": gofer predates `memory set --title` (legacy fallback)
	leakProbeEnv     = "GOFERMEM_TEST_API_KEY" // credential that must be stripped
	keepProbeEnv     = "GOFER_CONFIG_DIR"      // gofer setting that must be inherited
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeStateEnv) != "" {
		os.Exit(fakeGofer(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// call is one logged fake gofer invocation.
type call struct {
	Args   []string `json:"args"`
	Dir    string   `json:"dir"`
	Leaked bool     `json:"leaked"`
	Kept   string   `json:"kept"`
}

// fakeGofer mimics `gofer memory ls|show|set|rm --json` on the local tracker
// with a JSON file as storage, including gofer's not-found errors (exit 2).
func fakeGofer(args []string) int {
	statePath := os.Getenv(fakeStateEnv)
	dir, _ := os.Getwd()
	_, leaked := os.LookupEnv(leakProbeEnv)
	logLine, _ := json.Marshal(call{Args: args, Dir: dir, Leaked: leaked, Kept: os.Getenv(keepProbeEnv)})
	if f, err := os.OpenFile(os.Getenv(fakeLogEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.Write(append(logLine, '\n'))
		_ = f.Close()
	}
	if os.Getenv(fakeSleepEnv) == "1" {
		time.Sleep(30 * time.Second)
	}
	if os.Getenv(fakeFailEnv) == "1" {
		fmt.Fprintln(os.Stderr, "\x1b[97;41mERROR: \x1b[0mtracker is locked")
		return 2
	}
	if len(args) < 2 || args[0] != "memory" {
		fmt.Fprintln(os.Stderr, "fake gofer: unexpected args", args)
		return 64
	}
	sub := args[1]
	flags := map[string]string{}
	var tags []string
	var pos []string
	for i := 2; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			i = len(args)
		case a == "--json":
			flags["json"] = "1"
		case strings.HasPrefix(a, "--"):
			v := args[i+1]
			i++
			if a == "--tag" {
				tags = append(tags, v)
			}
			flags[strings.TrimPrefix(a, "--")] = v
		default:
			pos = append(pos, a)
		}
	}
	var recs []record
	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &recs)
	}
	save := func() {
		data, _ := json.Marshal(recs)
		_ = os.WriteFile(statePath, data, 0o600)
	}
	find := func(key string) int {
		for i, r := range recs {
			if r.Key == key {
				return i
			}
		}
		return -1
	}
	emit := func(v any) {
		data, _ := json.Marshal(v)
		fmt.Println(string(data))
	}

	switch sub {
	case "ls":
		kw := ""
		if len(pos) > 0 {
			kw = strings.ToLower(pos[0])
		}
		out := []record{}
		for _, r := range recs {
			if kw != "" && !strings.Contains(strings.ToLower(r.Key+"\n"+r.Summary+"\n"+r.Content), kw) {
				continue
			}
			if os.Getenv(fakeIgnoreTagEnv) != "1" && len(tags) > 0 && !hasTag(r.Tags, tags[0]) {
				continue
			}
			out = append(out, r)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
		emit(out)
	case "show":
		i := find(pos[0])
		if i < 0 {
			fmt.Println("[]")
			fmt.Fprintln(os.Stderr, "\x1b[97;41mERROR: \x1b[0mmemory not found: "+pos[0])
			return 2
		}
		if os.Getenv(fakeShowUntagEnv) == "1" {
			recs[i].Tags = nil
		}
		emit(recs[i])
	case "set":
		if os.Getenv(fakeNoTitleEnv) == "1" {
			if _, ok := flags["title"]; ok {
				fmt.Fprintln(os.Stderr, "ERROR: flag provided but not defined: -title")
				return 3
			}
		}
		key, content := pos[0], pos[1]
		i := find(key)
		if i < 0 {
			recs = append(recs, record{Key: key})
			i = len(recs) - 1
		}
		r := &recs[i]
		r.Content = content
		r.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if v, ok := flags["tag"]; ok {
			r.Tags = strings.Split(v, ",")
		}
		if v, ok := flags["summary"]; ok {
			if v == "-" { // gofer clears the field when the value is "-"
				r.Summary = ""
			} else {
				r.Summary = v
			}
		}
		if v, ok := flags["title"]; ok {
			if v == "-" {
				r.Title = ""
			} else {
				r.Title = v
			}
		}
		if v, ok := flags["kind"]; ok {
			r.Kind = v
		}
		if v, ok := flags["source"]; ok {
			r.Source = v
		}
		save()
		emit(*r)
	case "rm":
		i := find(pos[0])
		if i < 0 {
			fmt.Fprintln(os.Stderr, "\x1b[97;41mERROR: \x1b[0mmemory "+pos[0]+" not found")
			return 2
		}
		recs = append(recs[:i], recs[i+1:]...)
		save()
		emit(map[string]string{"removed": pos[0]})
	default:
		fmt.Fprintln(os.Stderr, "fake gofer: unknown subcommand", sub)
		return 64
	}
	return 0
}

// fake is a test fixture around one fake gofer state.
type fake struct {
	t     *testing.T
	dir   string // workspace dir passed as Config.Dir
	state string
	log   string
}

func newFake(t *testing.T, seed ...record) (*fake, *Store) {
	t.Helper()
	tmp := t.TempDir()
	f := &fake{t: t, dir: filepath.Join(tmp, "ws"), state: filepath.Join(tmp, "state.json"), log: filepath.Join(tmp, "calls.jsonl")}
	assert.Require(t, assert.NoErr(t, os.MkdirAll(f.dir, 0o700)))
	if seed == nil {
		seed = []record{}
	}
	f.writeState(seed)
	st, err := New(Config{Bin: os.Args[0], Dir: f.dir, Timeout: 10 * time.Second,
		// GORACE: a -race child otherwise sleeps 1s at exit (atexit_sleep_ms).
		Env: []string{fakeStateEnv + "=" + f.state, fakeLogEnv + "=" + f.log, "GORACE=atexit_sleep_ms=0"}})
	assert.Require(t, assert.NoErr(t, err))
	return f, st
}

func (f *fake) writeState(recs []record) {
	data, err := json.Marshal(recs)
	assert.Require(f.t, assert.NoErr(f.t, err))
	assert.Require(f.t, assert.NoErr(f.t, os.WriteFile(f.state, data, 0o600)))
}

func (f *fake) records() map[string]record {
	data, err := os.ReadFile(f.state)
	assert.Require(f.t, assert.NoErr(f.t, err))
	var recs []record
	assert.Require(f.t, assert.NoErr(f.t, json.Unmarshal(data, &recs)))
	m := map[string]record{}
	for _, r := range recs {
		m[r.Key] = r
	}
	return m
}

func (f *fake) calls() []call {
	data, err := os.ReadFile(f.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	assert.Require(f.t, assert.NoErr(f.t, err))
	var out []call
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var c call
		assert.Require(f.t, assert.NoErr(f.t, json.Unmarshal([]byte(line), &c)))
		out = append(out, c)
	}
	return out
}

// writeCalls returns the set/rm invocations (the only ones that write).
func (f *fake) writeCalls() []call {
	var out []call
	for _, c := range f.calls() {
		if c.Args[1] == "set" || c.Args[1] == "rm" {
			out = append(out, c)
		}
	}
	return out
}

var (
	ctx  = context.Background()
	proj = memory.ProjectScope("demo")
)

// foreign entries every isolation test seeds: a note of another agent, a
// rule with a suga-looking key but no suga tag, and a suga-tagged entry whose
// key does not follow the suga pattern.
func foreignSeed() []record {
	return []record{
		{Key: "team-rule", Content: "always run make lint before pushing", Tags: []string{"conventions"}, Kind: "rule",
			Summary: "lint before push", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "suga-project-foreign", Content: "impostor body mentions lint", Tags: []string{"other"}, Kind: "note",
			Summary: "Foreign", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "handwritten", Content: "tagged by hand, lint", Tags: []string{"suga"}, Kind: "note",
			Summary: "hand", UpdatedAt: "2026-10-01T00:00:00Z"},
	}
}

func TestNewAndScope(t *testing.T) {
	t.Run("dir is required", func(t *testing.T) {
		_, err := New(Config{})
		assert.True(t, errors.Is(err, memory.ErrInvalid))
	})
	t.Run("only the project scope is served", func(t *testing.T) {
		f, st := newFake(t)
		_, err := st.Index(ctx, memory.UserScope())
		assert.True(t, errors.Is(err, memory.ErrInvalid))
		_, err = st.Write(ctx, memory.UserScope(), memory.Op{Action: memory.ActionAdd, Title: "x", Body: "y", Kind: memory.KindUser})
		assert.True(t, errors.Is(err, memory.ErrInvalid))
		assert.Empty(t, f.calls())
		assert.Eq(t, "gofer", st.BackendName())
	})
}

func TestKeyMapping(t *testing.T) {
	assert.Eq(t, "suga-feedback-run-tests", Key(memory.KindFeedback, "run-tests"))
	for _, tc := range []struct {
		key  string
		kind memory.Kind
		id   string
		ok   bool
	}{
		{"suga-project-release-flow", memory.KindProject, "release-flow", true},
		{"suga-reference-docs-2", memory.KindReference, "docs-2", true},
		{"suga-unknown-x", "", "", false},
		{"suga-project-", "", "", false},
		{"suga-project-Bad_ID", "", "", false},
		{"team-rule", "", "", false},
	} {
		t.Run(tc.key, func(t *testing.T) {
			k, id, ok := ParseKey(tc.key)
			assert.Eq(t, tc.ok, ok)
			assert.Eq(t, tc.kind, k)
			assert.Eq(t, tc.id, id)
		})
	}
}

// TestReadTitleAndSummaryMapping pins the read mapping: an entry with a
// stored --title shows title and summary separately; legacy entries written
// before gofer had --title keep the summary slot as the title (summary empty);
// a tagged entry without either falls back to the first body line.
func TestReadTitleAndSummaryMapping(t *testing.T) {
	seed := []record{
		{Key: "suga-project-new", Title: "New title", Content: "body one", Tags: []string{"suga", "project"},
			Kind: "note", Summary: "New summary", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "suga-project-legacy", Content: "More text after", Tags: []string{"suga", "project"},
			Kind: "note", Summary: "Legacy title", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "suga-user-hand", Content: "First line\nSecond line", Tags: []string{"suga", "user"},
			Kind: "note", UpdatedAt: "2026-10-01T00:00:00Z"},
	}
	_, st := newFake(t, seed...)

	ix, err := st.Index(ctx, proj)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.Len(t, ix.Lines, 3))
	assert.Eq(t, "First line", ix.Lines[0].Title)
	assert.Eq(t, "", ix.Lines[0].Summary)
	assert.Eq(t, "Legacy title", ix.Lines[1].Title)
	assert.Eq(t, "", ix.Lines[1].Summary)
	assert.Eq(t, "New title", ix.Lines[2].Title)
	assert.Eq(t, "New summary", ix.Lines[2].Summary)

	t.Run("get mirrors the index", func(t *testing.T) {
		e, err := st.Get(ctx, proj, "legacy")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Legacy title", e.Title)
		assert.Eq(t, "", e.Summary)
		e, err = st.Get(ctx, proj, "new")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "New summary", e.Summary)
	})
}

func TestWriteReadRoundTrip(t *testing.T) {
	f, st := newFake(t)
	t.Setenv(leakProbeEnv, "sk-secret")
	t.Setenv(keepProbeEnv, "/cfg/gofer")

	e, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Release flow", Body: "- tag first\n- then push",
		Kind: memory.KindProject, Prov: memory.Provenance{SessionID: "sess-1"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "release-flow", e.ID)
	assert.False(t, e.Updated.IsZero())

	t.Run("set arguments follow the mapping", func(t *testing.T) {
		w := f.writeCalls()
		assert.Require(t, assert.Len(t, w, 1))
		assert.Eq(t, []string{"memory", "set", "--json", "--title", "Release flow", "--kind", "note",
			"--tag", "suga,project", "--summary", "- tag first - then push", "--source", "suga:sess-1",
			"--", "suga-project-release-flow", "- tag first\n- then push"}, w[0].Args)
		r := f.records()["suga-project-release-flow"]
		assert.Eq(t, []string{"suga", "project"}, r.Tags)
		assert.Eq(t, "Release flow", r.Title)
		assert.Eq(t, "note", r.Kind)
	})
	t.Run("explicit summary over the body fallback", func(t *testing.T) {
		_, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Docs", Body: "see docs/ for all of it",
			Summary: "where the docs live", Kind: memory.KindReference})
		assert.Require(t, assert.NoErr(t, err))
		w := f.writeCalls()
		assert.StrContains(t, strings.Join(w[len(w)-1].Args, " "), "--summary where the docs live ")
		e, err := st.Get(ctx, proj, "docs")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "where the docs live", e.Summary)
	})
	t.Run("runs in the workspace with credentials stripped", func(t *testing.T) {
		for _, c := range f.calls() {
			assert.Eq(t, f.dir, c.Dir)
			assert.False(t, c.Leaked)
			assert.Eq(t, "/cfg/gofer", c.Kept)
		}
	})
	t.Run("get index search", func(t *testing.T) {
		got, err := st.Get(ctx, proj, "release-flow")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Release flow", got.Title)
		assert.Eq(t, "- tag first\n- then push", got.Body)
		assert.Eq(t, memory.KindProject, got.Kind)

		ix, err := st.Index(ctx, proj)
		assert.NoErr(t, err)
		assert.Eq(t, []memory.IndexLine{
			{ID: "docs", Title: "Docs", Summary: "where the docs live", Kind: memory.KindReference},
			{ID: "release-flow", Title: "Release flow", Summary: "- tag first - then push", Kind: memory.KindProject},
		}, ix.Lines)

		hits, err := st.Search(ctx, proj, "PUSH tag", 0)
		assert.NoErr(t, err)
		assert.Require(t, assert.Len(t, hits, 1))
		assert.Eq(t, "release-flow", hits[0].ID)
		assert.StrContains(t, hits[0].Snippet, "push")

		hits, err = st.Search(ctx, proj, "push missing-term", 0)
		assert.NoErr(t, err)
		assert.Empty(t, hits)
	})
	t.Run("source without a session", func(t *testing.T) {
		_, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Docs", Body: "see docs/", Kind: memory.KindReference})
		assert.NoErr(t, err)
		w := f.writeCalls()
		assert.Contains(t, strings.Join(w[len(w)-1].Args, " "), "--source suga --")
	})
	t.Run("missing id", func(t *testing.T) {
		_, err := st.Get(ctx, proj, "nope")
		assert.True(t, errors.Is(err, memory.ErrNotFound))
	})
}

func TestReplaceAndRemove(t *testing.T) {
	f, st := newFake(t)
	e, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Use make lint", Body: "before commits", Kind: memory.KindProject})
	assert.Require(t, assert.NoErr(t, err))

	t.Run("replace keeps the key", func(t *testing.T) {
		got, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionReplace, ID: e.ID, Title: "Use make lint-all", Body: "before every commit"})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, memory.KindProject, got.Kind)
		r := f.records()["suga-project-use-make-lint"]
		assert.Eq(t, "before every commit", r.Content)
		assert.Eq(t, "Use make lint-all", r.Title)
		assert.Eq(t, "before every commit", r.Summary)
	})
	t.Run("kind change moves the key", func(t *testing.T) {
		got, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionReplace, ID: e.ID, Title: "Use make lint-all",
			Body: "before every commit", Kind: memory.KindFeedback})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, memory.KindFeedback, got.Kind)
		recs := f.records()
		_, oldThere := recs["suga-project-use-make-lint"]
		assert.False(t, oldThere)
		assert.Eq(t, []string{"suga", "feedback"}, recs["suga-feedback-use-make-lint"].Tags)
	})
	t.Run("remove returns the removed entry", func(t *testing.T) {
		old, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionRemove, ID: e.ID})
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Use make lint-all", old.Title)
		assert.Eq(t, "before every commit", old.Body)
		assert.Eq(t, memory.KindFeedback, old.Kind)
		assert.Empty(t, f.records())
		_, err = st.Get(ctx, proj, e.ID)
		assert.True(t, errors.Is(err, memory.ErrNotFound))
		_, err = st.Write(ctx, proj, memory.Op{Action: memory.ActionRemove, ID: e.ID})
		assert.True(t, errors.Is(err, memory.ErrNotFound))
	})
	t.Run("long titles are refused before gofer runs", func(t *testing.T) {
		before := len(f.calls())
		_, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: strings.Repeat("长", MaxTitleRunes+1), Body: "b", Kind: memory.KindUser})
		assert.True(t, errors.Is(err, memory.ErrInvalid))
		assert.Eq(t, before, len(f.calls()))
	})
}

// TestForeignEntriesAreInvisible proves that entries without the suga tag (or
// without a suga key) can never be listed, read, changed or removed, even
// when gofer itself ignores the --tag filter.
func TestForeignEntriesAreInvisible(t *testing.T) {
	for _, ignoreTag := range []bool{false, true} {
		t.Run(fmt.Sprintf("gofer ignores --tag=%v", ignoreTag), func(t *testing.T) {
			if ignoreTag {
				t.Setenv(fakeIgnoreTagEnv, "1")
			}
			f, st := newFake(t, foreignSeed()...)
			want := foreignSeed()

			ix, err := st.Index(ctx, proj)
			assert.NoErr(t, err)
			assert.Empty(t, ix.Lines)

			hits, err := st.Search(ctx, proj, "lint", 0)
			assert.NoErr(t, err)
			assert.Empty(t, hits)

			_, err = st.Get(ctx, proj, "foreign")
			assert.True(t, errors.Is(err, memory.ErrNotFound))

			_, err = st.Write(ctx, proj, memory.Op{Action: memory.ActionReplace, ID: "foreign", Title: "hijack", Body: "x"})
			assert.True(t, errors.Is(err, memory.ErrNotFound))
			_, err = st.Write(ctx, proj, memory.Op{Action: memory.ActionRemove, ID: "foreign"})
			assert.True(t, errors.Is(err, memory.ErrNotFound))
			assert.Empty(t, f.writeCalls())

			// An add whose slug collides with the impostor key gets a new id.
			e, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Foreign", Body: "mine", Kind: memory.KindProject})
			assert.Require(t, assert.NoErr(t, err))
			assert.Eq(t, "foreign-2", e.ID)

			recs := f.records()
			for _, w := range want {
				assert.Eq(t, w, recs[w.Key])
			}
			ix, err = st.Index(ctx, proj)
			assert.NoErr(t, err)
			assert.Eq(t, []memory.IndexLine{{ID: "foreign-2", Title: "Foreign",
				Summary: "mine", Kind: memory.KindProject}}, ix.Lines)
		})
	}
}

// TestShowRechecksTag covers an entry that lost its suga tag between the
// listing and the read: the listing (fake ignores --tag) still names it, the
// authoritative show must not.
func TestShowRechecksTag(t *testing.T) {
	f, st := newFake(t, record{Key: "suga-user-pref", Content: "c", Tags: []string{"suga", "user"}, Summary: "pref",
		UpdatedAt: "2026-10-01T00:00:00Z"})
	_, err := st.Get(ctx, proj, "pref")
	assert.Require(t, assert.NoErr(t, err))

	t.Setenv(fakeShowUntagEnv, "1")
	_, err = st.Get(ctx, proj, "pref")
	assert.True(t, errors.Is(err, memory.ErrNotFound))
	_, err = st.Write(ctx, proj, memory.Op{Action: memory.ActionRemove, ID: "pref"})
	assert.True(t, errors.Is(err, memory.ErrNotFound))
	assert.Empty(t, f.writeCalls())
}

func TestGoferFailures(t *testing.T) {
	t.Run("errors carry stderr without colors", func(t *testing.T) {
		_, st := newFake(t)
		t.Setenv(fakeFailEnv, "1")
		_, err := st.Index(ctx, proj)
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "tracker is locked")
		assert.NotContains(t, err.Error(), "\x1b[")
		assert.False(t, errors.Is(err, memory.ErrNotFound))
	})
	t.Run("timeout", func(t *testing.T) {
		_, st := newFake(t)
		st.cfg.Timeout = 300 * time.Millisecond
		t.Setenv(fakeSleepEnv, "1")
		start := time.Now()
		_, err := st.Index(ctx, proj)
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "timed out")
		assert.True(t, time.Since(start) < 10*time.Second)
	})
	t.Run("missing binary", func(t *testing.T) {
		st, err := New(Config{Bin: filepath.Join(t.TempDir(), "no-gofer"), Dir: t.TempDir()})
		assert.Require(t, assert.NoErr(t, err))
		_, err = st.Index(ctx, proj)
		assert.Err(t, err)
	})
}

// TestGovernedTombstoneShape checks the removed entry carries what Governed
// needs to fingerprint it.
func TestGovernedTombstoneShape(t *testing.T) {
	_, st := newFake(t)
	prov := &memProv{}
	g := memory.NewGoverned(st, memory.Policy{Provenance: prov})
	e, err := g.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Ports", Body: "api on 8080", Kind: memory.KindProject})
	assert.Require(t, assert.NoErr(t, err))
	_, err = g.Write(ctx, proj, memory.Op{Action: memory.ActionRemove, ID: e.ID})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, memory.Fingerprint("Ports", "api on 8080"), prov.tomb)
	assert.Eq(t, "gofer", prov.backend)
	_, err = g.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Ports", Body: "api on 8080", Kind: memory.KindProject})
	assert.True(t, errors.Is(err, memory.ErrTombstoned))
}

type memProv struct{ backend, tomb string }

func (p *memProv) Record(context.Context, string, memory.Scope, memory.Entry, string) error {
	return nil
}
func (p *memProv) Tombstone(_ context.Context, backend string, _ memory.Scope, _ string, fp string) error {
	p.backend, p.tomb = backend, fp
	return nil
}
func (p *memProv) IsTombstoned(_ context.Context, _ string, _ memory.Scope, fp string) (bool, error) {
	return fp != "" && fp == p.tomb, nil
}

// TestSetSummary covers the Summarizer: a title-carrying entry keeps its
// title, kind, tags and source (set keeps every field that is not passed);
// a legacy entry gets its title promoted in the same write.
func TestSetSummary(t *testing.T) {
	seed := []record{
		{Key: "suga-project-new", Title: "New title", Content: "body one", Tags: []string{"suga", "project"},
			Kind: "note", Summary: "old one", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "suga-project-legacy", Content: "More text after", Tags: []string{"suga", "project"},
			Kind: "note", Summary: "Legacy title", UpdatedAt: "2026-10-01T00:00:00Z"},
	}
	f, st := newFake(t, seed...)

	t.Run("title carrying entry keeps the title", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, st.SetSummary(ctx, proj, "new", "a fresh sentence.")))
		assert.Eq(t, []string{"memory", "set", "--json", "--summary", "a fresh sentence.", "--", "suga-project-new", "body one"}, f.writeCalls()[0].Args)
		r := f.records()["suga-project-new"]
		assert.Eq(t, "New title", r.Title)
		assert.Eq(t, "a fresh sentence.", r.Summary)
	})
	t.Run("legacy entry promotes the title", func(t *testing.T) {
		assert.Require(t, assert.NoErr(t, st.SetSummary(ctx, proj, "legacy", "a fresh sentence.")))
		w := f.writeCalls()
		assert.Eq(t, []string{"memory", "set", "--json", "--title", "Legacy title", "--summary", "a fresh sentence.",
			"--", "suga-project-legacy", "More text after"}, w[len(w)-1].Args)
		r := f.records()["suga-project-legacy"]
		assert.Eq(t, "Legacy title", r.Title)
		assert.Eq(t, "a fresh sentence.", r.Summary)
	})
	t.Run("missing id", func(t *testing.T) {
		assert.True(t, errors.Is(st.SetSummary(ctx, proj, "nope", "s"), memory.ErrNotFound))
	})
}

// TestMigrateLegacyTitles covers the one-time migration: the preview lists
// exactly the entries written with the old title-in-summary mapping and
// writes nothing; --apply moves each summary to --title and fills the
// summary from the body's first sentence.
func TestMigrateLegacyTitles(t *testing.T) {
	seed := []record{
		{Key: "suga-project-new", Title: "New title", Content: "body", Tags: []string{"suga", "project"},
			Kind: "note", Summary: "kept", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "suga-project-legacy", Content: "First sentence. Second sentence.", Tags: []string{"suga", "project"},
			Kind: "note", Summary: "Legacy title", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "suga-user-handless", Content: "no summary slot", Tags: []string{"suga", "user"},
			Kind: "note", UpdatedAt: "2026-10-01T00:00:00Z"},
		{Key: "not-suga", Content: "foreign", Tags: []string{"other"}, Summary: "x", UpdatedAt: "2026-10-01T00:00:00Z"},
	}
	f, st := newFake(t, seed...)

	t.Run("preview lists and writes nothing", func(t *testing.T) {
		moves, err := st.MigrateLegacyTitles(ctx, proj, false)
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, []TitleMove{{ID: "legacy", Kind: memory.KindProject, Title: "Legacy title",
			NewSummary: "First sentence."}}, moves)
		assert.Empty(t, f.writeCalls())
	})
	t.Run("apply rewrites the legacy entries", func(t *testing.T) {
		moves, err := st.MigrateLegacyTitles(ctx, proj, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.Require(t, assert.Len(t, moves, 1))
		assert.Eq(t, []string{"memory", "set", "--json", "--title", "Legacy title", "--summary", "First sentence.",
			"--", "suga-project-legacy", "First sentence. Second sentence."}, f.writeCalls()[0].Args)
		r := f.records()["suga-project-legacy"]
		assert.Eq(t, "Legacy title", r.Title)
		assert.Eq(t, "First sentence.", r.Summary)
		// Migrated twice over: nothing is left to move.
		moves, err = st.MigrateLegacyTitles(ctx, proj, true)
		assert.Require(t, assert.NoErr(t, err))
		assert.Empty(t, moves)
		assert.Eq(t, "New title", f.records()["suga-project-new"].Title)
	})
	t.Run("non-project scope is refused", func(t *testing.T) {
		_, err := st.MigrateLegacyTitles(ctx, memory.UserScope(), false)
		assert.True(t, errors.Is(err, memory.ErrInvalid))
	})
}

// TestLegacyGoferFallback covers gofer without `memory set --title`: the
// first write falls back to the old title-in-summary mapping, warns once and
// keeps working; the summary maintenance fails with a clear error.
func TestLegacyGoferFallback(t *testing.T) {
	f, st := newFake(t)
	var warned []string
	st.cfg.Warn = func(msg string) { warned = append(warned, msg) }
	t.Setenv(fakeNoTitleEnv, "1")

	e, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Release flow",
		Body: "- tag first", Kind: memory.KindProject, Prov: memory.Provenance{SessionID: "s1"}})
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "release-flow", e.ID)

	t.Run("set keeps the title in --summary", func(t *testing.T) {
		w := f.writeCalls() // the refused --title attempt is logged too; the retry lands
		assert.Require(t, assert.Len(t, w, 2))
		assert.Eq(t, []string{"memory", "set", "--json", "--kind", "note", "--tag", "suga,project",
			"--source", "suga:s1", "--summary", "Release flow", "--", "suga-project-release-flow", "- tag first"}, w[1].Args)
		r := f.records()["suga-project-release-flow"]
		assert.Eq(t, "", r.Title)
		assert.Eq(t, "Release flow", r.Summary)
	})
	t.Run("the warning is reported once", func(t *testing.T) {
		assert.Len(t, warned, 1)
		assert.StrContains(t, warned[0], "memory set --title")
		_, err := st.Write(ctx, proj, memory.Op{Action: memory.ActionAdd, Title: "Second", Body: "b", Kind: memory.KindUser})
		assert.Require(t, assert.NoErr(t, err))
		assert.Len(t, warned, 1) // still one
	})
	t.Run("reads stay on the legacy mapping", func(t *testing.T) {
		e, err := st.Get(ctx, proj, "release-flow")
		assert.Require(t, assert.NoErr(t, err))
		assert.Eq(t, "Release flow", e.Title)
		assert.Eq(t, "", e.Summary)
	})
	t.Run("summary maintenance is refused", func(t *testing.T) {
		err := st.SetSummary(ctx, proj, "release-flow", "s")
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "no separate summary slot")
	})
	t.Run("migration is refused", func(t *testing.T) {
		_, err := st.MigrateLegacyTitles(ctx, proj, true)
		assert.Err(t, err)
		assert.StrContains(t, err.Error(), "update gofer")
	})
}
