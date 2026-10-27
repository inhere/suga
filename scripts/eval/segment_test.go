package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
)

// segmentAnalyzeJSON is a trimmed `suga inspect trace --analyze --json` object
// with the segment section report the eval reads: three segments, the second
// changing memory_index and the third changing skills and dropping host_context.
const segmentAnalyzeJSON = `{"model_calls":4,"segment_sections":[
 {"segment":1,"reason":"threshold","bytes":4300,"sections":[
  {"name":"environment","bytes":500},{"name":"rules","bytes":2000},{"name":"memory_index","bytes":1000},{"name":"skills","bytes":800}]},
 {"segment":2,"reason":"threshold","bytes":4500,"changed":["memory_index"],"sections":[
  {"name":"environment","bytes":500},{"name":"rules","bytes":2000},{"name":"memory_index","bytes":1200},{"name":"skills","bytes":800}]},
 {"segment":3,"reason":"manual","bytes":4600,"changed":["skills","host_context"],"sections":[
  {"name":"environment","bytes":500},{"name":"rules","bytes":2000},{"name":"memory_index","bytes":1200},{"name":"skills","bytes":900}]}]}`

// TestParseSegmentChanges covers reading the segment part of the analyze
// object: the segment ordinal, the changed names and the bytes of every section,
// and an object without that part giving nothing.
func TestParseSegmentChanges(t *testing.T) {
	t.Run("changes and section bytes", func(t *testing.T) {
		segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
		assert.Require(t, assert.Eq(t, 3, len(segs)))
		assert.Eq(t, 1, segs[0].Segment)
		assert.Eq(t, 0, len(segs[0].Changed)) // the first segment has no previous one
		assert.Eq(t, 4, len(segs[0].Sections))
		assert.Eq(t, "manual", segs[2].Reason)
		assert.Eq(t, []string{"skills", "host_context"}, segs[2].Changed)
		n, ok := segs[1].bytesOf("memory_index")
		assert.True(t, ok)
		assert.Eq(t, 1200, n)
		_, ok = segs[2].bytesOf("host_context") // dropped at that break
		assert.False(t, ok)
	})
	t.Run("nothing to read", func(t *testing.T) {
		for _, raw := range []string{`[]`, `{}`, `{"model_calls":1}`, `not json`} {
			assert.Eq(t, 0, len(parseSegmentChanges([]byte(raw))))
		}
	})
}

// TestSectionBreakStats covers the per-section counts: the first segment of a
// run is not a break, a dropped section counts as changed with no bytes, and the
// order is by how often a section changed.
func TestSectionBreakStats(t *testing.T) {
	t.Run("counts, shares and mean bytes", func(t *testing.T) {
		segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
		stats := sectionBreakStats([]Result{{Task: "a", SegmentChanges: segs}})
		assert.Require(t, assert.Eq(t, 3, len(stats)))
		byName := map[string]sectionBreakStat{}
		for _, st := range stats {
			byName[st.Name] = st
		}
		for _, name := range []string{"host_context", "memory_index", "skills"} {
			assert.Eq(t, 1, byName[name].Changed, name)
			assert.Eq(t, 2, byName[name].Breaks, name) // the first segment is not a break
			assert.Eq(t, 0.5, byName[name].Share)
		}
		assert.Eq(t, int64(1200), byName["memory_index"].AvgBytes)
		assert.Eq(t, int64(900), byName["skills"].AvgBytes)
		assert.Eq(t, int64(0), byName["host_context"].AvgBytes)
		// Equal counts fall back to the name, so the report is reproducible.
		assert.Eq(t, "host_context", stats[0].Name)
		assert.Eq(t, "memory_index", stats[1].Name)
		assert.Eq(t, "skills", stats[2].Name)
		assert.Eq(t, 0, len(sectionBreakStats(nil)))
	})
	t.Run("a run without a break counts nothing", func(t *testing.T) {
		segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
		assert.Eq(t, 0, len(sectionBreakStats([]Result{{Task: "a", SegmentChanges: segs[:1]}})))
	})
}

// segmentStartBucket is the pooled bucket a run of these tests reports: cache
// read over the prompt and over the estimated prefix.
func segmentStartBucket(read, input, prefix int64) cacheBucket {
	return cacheBucket{Bucket: "segment_start", Calls: 1, Input: input, CacheRead: read, PrefixTokens: prefix, CacheReported: true}
}

// TestSegmentStartOf covers the split of the segment_start bucket: a run whose
// breaks changed the section on the changed side, the rest on the other.
func TestSegmentStartOf(t *testing.T) {
	segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
	rs := []Result{
		{Task: "a", SegmentChanges: segs, CacheBuckets: []cacheBucket{segmentStartBucket(900, 100, 2000)}},
		{Task: "b", SegmentChanges: segs[:1], CacheBuckets: []cacheBucket{segmentStartBucket(500, 500, 500)}},
	}
	s := segmentStartOf(rs, "memory_index")
	assert.Eq(t, 1, s.ChangedRuns)
	assert.Eq(t, 1, s.UnchangedRuns)
	assert.Eq(t, 90.0, s.Changed.Hit*100)
	assert.Eq(t, 45.0, s.Changed.Reuse*100)
	assert.Eq(t, 50.0, s.Unchanged.Hit*100)
	assert.Eq(t, 100.0, s.Unchanged.Reuse*100)

	// A section nobody changed has no changed side: its cells render "-".
	only := segmentStartOf(rs[1:], "memory_index")
	assert.Eq(t, 0, only.ChangedRuns)
	assert.Eq(t, "-", hitCellOf(only.Changed))
	assert.Eq(t, "-", reuseCellOf(only.Changed))
}

// TestPrintSegmentChanges covers the summary section: the section name, how
// often it changed and the segment_start rate of the runs that changed it
// against those that did not.
func TestPrintSegmentChanges(t *testing.T) {
	t.Run("sections with their rates", func(t *testing.T) {
		segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
		rs := []Result{
			{Task: "a", SegmentChanges: segs, CacheBuckets: []cacheBucket{segmentStartBucket(900, 100, 2000)}},
			{Task: "b", SegmentChanges: segs[:1], CacheBuckets: []cacheBucket{segmentStartBucket(500, 500, 500)}},
		}
		var buf bytes.Buffer
		printSegmentChanges(&buf, rs)
		out := buf.String()
		assert.StrContains(t, out, "segment sections changing at a break (2 breaks")
		assert.StrContains(t, out, "HIT% chg/unchg")
		assert.StrContains(t, out, "memory_index")
		assert.StrContains(t, out, "90.0/50.0")
		assert.StrContains(t, out, "45.0/100.0")
		assert.StrContains(t, out, "1/1")
	})
	t.Run("nothing printed without a break", func(t *testing.T) {
		var buf bytes.Buffer
		printSegmentChanges(&buf, []Result{{Task: "omp-run"}})
		assert.Eq(t, "", buf.String())
	})
}

// TestSegmentChangeSection covers the markdown section of the compare reports:
// one column per group, the break count of each group and both tables.
func TestSegmentChangeSection(t *testing.T) {
	segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
	groups := []segmentGroup{
		{Label: "v1", Rows: []Result{{Task: "a", SegmentChanges: segs, CacheBuckets: []cacheBucket{segmentStartBucket(900, 100, 2000)}}}},
		{Label: "v2", Rows: []Result{{Task: "a", SegmentChanges: segs[:1], CacheBuckets: []cacheBucket{segmentStartBucket(500, 500, 500)}}}},
	}
	out := segmentChangeSection(groups)
	assert.StrContains(t, out, "换段次数（首段没有可比的上一个段，不计入占比）：v1 2、v2 0")
	assert.StrContains(t, out, "| 节 | v1 变化/占比·平均字节 | v2 变化/占比·平均字节 |")
	assert.StrContains(t, out, "| memory_index | 1/50.0% · 1200B | - |")
	assert.StrContains(t, out, "v1 变化时 hit%/reuse%")
	assert.StrContains(t, out, "90.0/45.0 (1 轮)")
	assert.StrContains(t, out, "50.0/100.0 (1 轮)")

	t.Run("no break anywhere renders nothing", func(t *testing.T) {
		assert.Eq(t, "", segmentChangeSection([]segmentGroup{{Label: "v1", Rows: []Result{{Task: "a"}}}}))
		assert.Eq(t, "", segmentChangeSection(nil))
	})
}

// TestResultSegmentChangesJSON pins the results.jsonl field name and shape: the
// report of a later eval round reads segment_changes back.
func TestResultSegmentChangesJSON(t *testing.T) {
	segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
	raw, err := json.Marshal(Result{Task: "a", SegmentChanges: segs})
	assert.NoErr(t, err)
	assert.StrContains(t, string(raw), `"segment_changes":[{"segment":1,`)
	assert.StrContains(t, string(raw), `"changed":["memory_index"]`)
	assert.StrContains(t, string(raw), `"sections":[{"name":"environment","bytes":500}`)

	var back Result
	assert.NoErr(t, json.Unmarshal(raw, &back))
	assert.Require(t, assert.Eq(t, 3, len(back.SegmentChanges)))
	assert.Eq(t, "skills", back.SegmentChanges[2].Changed[0])

	// A row without segment breaks keeps the field out of the file.
	empty, err := json.Marshal(Result{Task: "a"})
	assert.NoErr(t, err)
	assert.StrNotContains(t, string(empty), "segment_changes")
}

// TestCompareReportSegmentSection covers the compare report: the base and the
// candidate columns of the segment section, and the note when neither side kept
// segment breaks.
func TestCompareReportSegmentSection(t *testing.T) {
	segs := parseSegmentChanges([]byte(segmentAnalyzeJSON))
	base := &compareGroup{Label: "base", Rows: []Result{{Task: "a", Passed: true, SegmentChanges: segs,
		CacheBuckets: []cacheBucket{segmentStartBucket(900, 100, 2000)}}}}
	cand := &compareGroup{Label: "cand", Rows: []Result{{Task: "a", Passed: true, SegmentChanges: segs[:1]}}}
	md, _ := compareReport(base, cand, time.Now())
	assert.StrContains(t, md, "## 换段时变化的节")
	assert.StrContains(t, md, "| 节 | 基线 变化/占比·平均字节 | 候选 变化/占比·平均字节 |")
	assert.StrContains(t, md, "| memory_index | 1/50.0% · 1200B | - |")
	assert.StrContains(t, md, "90.0/45.0 (1 轮)")

	t.Run("no break at all is spelled out", func(t *testing.T) {
		quiet, _ := compareReport(&compareGroup{Label: "b", Rows: []Result{{Task: "a"}}},
			&compareGroup{Label: "c", Rows: []Result{{Task: "a"}}}, time.Now())
		assert.StrContains(t, quiet, "结果里没有换段记录")
	})
}

// segmentStub builds a stand-in suga binary whose analyze view carries the
// segment report above, so the end-to-end test runs the real command path.
func segmentStub(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "stub")
	assert.NoErr(t, os.MkdirAll(src, 0o755))
	code := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
)

const analyze = %q

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		fmt.Println("{\"type\":\"result\",\"result\":\"run ok\"}")
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "inspect" {
		if os.Args[2] == "stats" {
			fmt.Println("{\"models\":[{\"calls\":1,\"input_tokens\":10,\"output_tokens\":5}],\"tools\":[]}")
			return
		}
		for _, a := range os.Args {
			if a == "--analyze" {
				fmt.Println(analyze)
				return
			}
		}
		fmt.Println("[]")
	}
}
`, segmentAnalyzeJSON)
	assert.NoErr(t, os.WriteFile(filepath.Join(src, "main.go"), []byte(code), 0o644))
	out := filepath.Join(t.TempDir(), "stub-suga.exe") // .exe (also valid on Linux)
	bo, err := exec.Command("go", "build", "-o", out, filepath.Join(src, "main.go")).CombinedOutput()
	if err != nil {
		t.Fatal("build stub:", err, string(bo))
	}
	return out
}

// TestSegmentChangesEndToEnd runs the real eval loop against the stub: the
// segment report of the analyze view lands in the results.jsonl row.
func TestSegmentChangesEndToEnd(t *testing.T) {
	stub := segmentStub(t)
	base := t.TempDir()
	taskDir := filepath.Join(base, "tasks", "seg")
	assert.NoErr(t, os.MkdirAll(filepath.Join(taskDir, "fixture"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "fixture", "go.mod"), []byte("module evalfixture/seg\n"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(taskDir, "task.yaml"),
		[]byte("name: seg\nprompt: hi\nchecks:\n  - {type: answer_contains, text: \"run ok\"}\n"), 0o644))
	tasks, err := loadTasks(filepath.Join(base, "tasks"), "*")
	assert.NoErr(t, err)

	out := filepath.Join(base, "out")
	assert.NoErr(t, os.MkdirAll(out, 0o755))
	jf, err := os.Create(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	defer jf.Close()
	o := &options{bin: stub, agent: "suga", out: out, timeout: time.Minute, tasksDir: filepath.Join(base, "tasks"), repeat: 1}
	assert.NoErr(t, runTaskSet(o, stub, "20260101-000001", tasks, nil, jf))

	raw, err := os.ReadFile(filepath.Join(out, "results.jsonl"))
	assert.NoErr(t, err)
	var r Result
	assert.Require(t, assert.NoErr(t, json.Unmarshal(bytes.TrimSpace(raw), &r)))
	assert.True(t, r.Passed, r.Checks)
	assert.Require(t, assert.Eq(t, 3, len(r.SegmentChanges)))
	assert.Eq(t, "memory_index", r.SegmentChanges[1].Changed[0])
	assert.Eq(t, 1200, r.SegmentChanges[1].Sections[2].Bytes)
}
