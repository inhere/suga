package personal

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
)

// budgetTestConstraints is the protected part of the test packages: the hard
// constraints and rule lines that must survive every fold word for word (the
// long-session evaluation checks exactly these, research 2026-10-22 §3).
var budgetTestConstraints = strings.Join([]string{
	"- 硬约束：Go 标识符一律小写下划线，不允许驼峰。",
	"- 硬约束：vendor/ 只读，任何情况下不得修改或删除其中的文件。",
	"- 规则：增加注释可以，删除既有注释不行。",
	"- 原话：“先把 src/order.go 的 Parse 行号表补齐，再动其它文件。”",
}, "\n")

// packageSectionBody returns the body of one "## " section of a package text
// ("" when the section is absent), as a summarizer would copy it forward.
func packageSectionBody(text, name string) string {
	for _, s := range splitHandoffSections(text) {
		if got, ok := handoffSectionName(s.heading); ok && got == name {
			return s.body
		}
	}
	return ""
}

// echoGrowthProv is the summarizer the research measured: it carries every
// section of the previous package forward verbatim and appends the new
// segment's own line to each one, so the package grows without bound unless
// the budgets and the coordinate dedupe stop it (research 2026-10-22 §5 #2:
// 4.7 -> 7.8 -> 16.0 KB). The newest hot coordinate is written twice per
// package, as a faithful summarizer that re-derives it does.
type echoGrowthProv struct {
	mu        sync.Mutex
	sumReqs   []*agentcore.GenerateRequest
	answers   int
	truncated bool // report one summary answer as cut off by the output cap
}

func (p *echoGrowthProv) Name() string { return "echo-growth" }

func (p *echoGrowthProv) Generate(_ context.Context, req *agentcore.GenerateRequest) (*agentcore.GenerateResult, error) {
	if req.System != compactSystemPrompt {
		p.mu.Lock()
		p.answers++
		n := p.answers
		p.mu.Unlock()
		return finalText(fmt.Sprintf("reply-%d 读了 src/order.go 的 Parse，第 %d 行", n, n*10))
	}
	p.mu.Lock()
	p.sumReqs = append(p.sumReqs, req)
	n, truncated := len(p.sumReqs), p.truncated && len(p.sumReqs) == 1
	p.mu.Unlock()
	res := &agentcore.GenerateResult{Content: echoSummaryBody(previousPackage(req), n), FinishReason: agentcore.FinishFinal}
	res.Truncated = truncated
	return res, nil
}

// echoSummaryBody renders one generation of echoGrowthProv: every section of
// prev carried forward, the new segment's own (padded) content appended, the
// protected section echoed (or seeded on the first generation).
func echoSummaryBody(prev string, n int) string {
	i := strconv.Itoa(n)
	var coords, verified, progress, next strings.Builder
	coords.WriteString("- src/order.go: Parse 第 " + strconv.Itoa(n*100) + " 行\n" +
		"- src/order.go: Parse 第 " + strconv.Itoa(n*100+5) + " 行\n" +
		"- src/turn" + i + ".go: Step" + i + " 第 " + strconv.Itoa(n*10) + " 行\n")
	for k := 0; k < 8; k++ { // the coordinate table grows every segment
		coords.WriteString("- src/piece" + i + "_" + strconv.Itoa(k) + ".go: Part" + strconv.Itoa(k) + " 第 " + strconv.Itoa(k*3) + " 行\n")
	}
	for k := 0; k < 30; k++ { // enough per section to blow past every budget
		verified.WriteString("- go test ./internal/personal/ 通过（第 " + i + " 次第 " + strconv.Itoa(k) + " 项）\n")
		progress.WriteString("- 第 " + i + " 段：正在写报告，剩余 " + strconv.Itoa(k) + " 项\n")
		next.WriteString("- 第 " + i + " 段的下一步 " + strconv.Itoa(k) + "\n")
	}
	sections := []struct{ name, extra string }{
		{handoffSectionTask, "继续第 " + i + " 段"},
		{handoffSectionCoords, strings.TrimRight(coords.String(), "\n")},
		{handoffProtectedSection, ""},
		{handoffSectionVerified, strings.TrimRight(verified.String(), "\n")},
		{handoffSectionProgress, strings.TrimRight(progress.String(), "\n")},
		{handoffSectionNext, strings.TrimRight(next.String(), "\n")},
	}
	var sb strings.Builder
	for _, s := range sections {
		sb.WriteString("## " + s.name + "\n")
		body := strings.TrimSpace(packageSectionBody(prev, s.name))
		if body == "" && s.name == handoffProtectedSection {
			body = budgetTestConstraints
		}
		if body != "" {
			sb.WriteString(body + "\n")
		}
		if s.extra != "" {
			sb.WriteString(s.extra + "\n")
		}
	}
	sb.WriteString(refsHeading + "\n- 无")
	return sb.String()
}

// TestHandoffPackageBudgetOverThreeSegments breaks the same session into new
// segments three times with a summarizer that folds the previous package
// whole: the third package must not grow over the second, the hard constraints
// must be there verbatim, and the coordinate table must keep one line per file
// and symbol.
func TestHandoffPackageBudgetOverThreeSegments(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &echoGrowthProv{}
	runner := compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 100000, 1000
	})
	var sid string
	for i := 1; i <= 4; i++ { // four segments: three folds of a growing package
		res, err := runner.Run(context.Background(), RunRequest{SessionID: sid,
			Prompt: fmt.Sprintf("question-%d 看 src/order.go 的 Parse 第 %d 行", i, i*10)})
		assert.NoErr(t, err)
		sid = res.SessionID
		_, err = runner.Compact(context.Background(), sid)
		assert.NoErr(t, err)
	}
	recs := compactionEntries(t, repo, sid)
	assert.Require(t, assert.Len(t, recs, 4))

	t.Run("every summary call carries the budget as its output cap", func(t *testing.T) {
		assert.Eq(t, 4, len(prov.sumReqs))
		for _, req := range prov.sumReqs {
			assert.Eq(t, handoffOutputTokens(), req.MaxTokens)
		}
		// Loose but bounded: the capped budgets plus half again for the
		// protected section, and far below the 21,676 tokens measured before.
		assert.Eq(t, true, handoffOutputTokens() >= handoffCappedTotal()/4)
		assert.Eq(t, true, handoffOutputTokens() < 8000)
	})

	t.Run("the third package does not grow over the second", func(t *testing.T) {
		for i, rec := range recs {
			t.Logf("package %d: %d bytes", i+1, len(rec.Summary))
		}
		// Every segment adds ~3 KB of its own content, so an unbounded fold
		// doubles the package each time (research 2026-10-22 §5 #2: 4.7 -> 7.8
		// -> 16.0 KB). With the budgets a capped section keeps its newest lines
		// and no more than its budget, so the package stops growing after the
		// first fold: 10% plus the protected section's own size is the room left.
		assert.Eq(t, true, len(recs[2].Summary) <= len(recs[3].Summary))
		assert.Eq(t, true, len(recs[3].Summary) <= len(recs[2].Summary)*11/10)
		assert.Eq(t, true, len(recs[2].Summary) <= handoffCappedTotal()+len(budgetTestConstraints)+1024)
		assert.Eq(t, true, len(recs[3].Summary) <= handoffCappedTotal()+len(budgetTestConstraints)+1024)
		// The four packages are 4x apart at most (unbounded they were ~8x by the
		// fourth segment).
		assert.Eq(t, true, len(recs[3].Summary) <= len(recs[0].Summary)*2)
	})

	t.Run("the refresh of a coordinate keeps only its latest line", func(t *testing.T) {
		for i, rec := range recs {
			assert.Eq(t, 1, strings.Count(rec.Summary, "src/order.go: Parse"))
			assert.StrContains(t, rec.Summary, "src/order.go: Parse 第 "+strconv.Itoa((i+1)*100+5)+" 行")
			assert.NotContains(t, rec.Summary, "src/order.go: Parse 第 "+strconv.Itoa((i+1)*100)+" 行")
		}
	})

	t.Run("the hard constraints stay verbatim in every package", func(t *testing.T) {
		for _, rec := range recs {
			assert.StrContains(t, rec.Summary, budgetTestConstraints)
			assert.StrContains(t, rec.Summary, "vendor/ 只读")
		}
	})

	t.Run("a coordinate the fresh material carries again is not copied forward", func(t *testing.T) {
		// The second break folds the first package; its transcript contains the
		// request naming src/order.go and Parse, so the old line is stale (the
		// summarizer writes the refreshed one). src/turn1.go is not in that
		// transcript and must be carried over.
		prev := packageSectionBody(previousPackage(prov.sumReqs[1]), handoffSectionCoords)
		assert.NotContains(t, prev, "src/order.go")
		assert.StrContains(t, prev, "src/turn1.go")
		assert.StrContains(t, packageSectionBody(previousPackage(prov.sumReqs[1]), handoffProtectedSection), budgetTestConstraints)
	})
}

// TestSummarizeReasksAfterTruncation: an answer cut off at the output cap is
// asked for once more with the budgets restated.
func TestSummarizeReasksAfterTruncation(t *testing.T) {
	repo := openObserveRepo(t)
	prov := &echoGrowthProv{truncated: true}
	runner := compactRunner(t, repo, prov, nil, func(c *Config) {
		c.ContextWindow, c.CompactReserveTokens = 100000, 1000
	})
	res, err := runner.Run(context.Background(), RunRequest{Prompt: "hello src/order.go"})
	assert.NoErr(t, err)
	_, err = runner.Compact(context.Background(), res.SessionID)
	assert.NoErr(t, err)
	assert.Eq(t, 2, len(prov.sumReqs))
	assert.NotContains(t, prov.sumReqs[0].Messages[0].Content, completeProtectedNote)
	assert.StrContains(t, prov.sumReqs[1].Messages[0].Content, completeProtectedNote)
	assert.Eq(t, handoffOutputTokens(), prov.sumReqs[1].MaxTokens)
	recs := compactionEntries(t, repo, res.SessionID)
	assert.Require(t, assert.Len(t, recs, 1))
	assert.StrContains(t, recs[0].Summary, budgetTestConstraints)
}

// TestFoldPreviousPackageDropsStaleCoordinates: the fold keeps the newest line
// per file and symbol, drops coordinates the fresh transcript carries, cuts the
// capped sections to their budgets and leaves the protected one untouched.
func TestFoldPreviousPackageDropsStaleCoordinates(t *testing.T) {
	prev := "## " + handoffSectionCoords + "\n" +
		"- src/a.go: Parse 第 10 行\n" +
		"- src/b.go: Order 第 5 行\n" +
		"- src/a.go: Parse 第 22 行\n" +
		"\n" +
		"## " + handoffProtectedSection + "\n" + budgetTestConstraints + "\n" +
		"## " + handoffSectionVerified + "\n" + strings.Repeat("- go vet ./... 通过\n", 200)

	t.Run("the latest line per file and symbol wins", func(t *testing.T) {
		out := foldPreviousPackage(prev, "")
		assert.NotContains(t, out, "Parse 第 10 行")
		assert.StrContains(t, out, "- src/a.go: Parse 第 22 行")
		assert.StrContains(t, out, "- src/b.go: Order 第 5 行")
		assert.StrContains(t, out, budgetTestConstraints)
		assert.Eq(t, true, len(packageSectionBody(out, handoffSectionVerified)) <= handoffSectionBudgets[handoffSectionVerified])
	})

	t.Run("coordinates the transcript carries again are dropped", func(t *testing.T) {
		fresh := "[tool_result file_read] src/a.go\nfunc Parse() {}\n"
		out := foldPreviousPackage(prev, fresh)
		assert.NotContains(t, out, "src/a.go")
		assert.StrContains(t, out, "- src/b.go: Order 第 5 行")
	})

	t.Run("a package without sections is left alone", func(t *testing.T) {
		assert.Eq(t, "SUMMARY-main-1", foldPreviousPackage("SUMMARY-main-1", "x"))
	})
}

// TestNormalizeHandoffBodyCutsOnlyBudgetedSections: the output side of the
// budgets never touches the protected section.
func TestNormalizeHandoffBodyCutsOnlyBudgetedSections(t *testing.T) {
	protected := strings.Repeat("- 硬约束：Go 标识符一律小写下划线，vendor/ 只读。\n", 120)
	body := "## " + handoffSectionCoords + "\n" +
		"- src/a.go: Parse 第 10 行\n- src/a.go: Parse 第 30 行\n" +
		"## " + handoffProtectedSection + "\n" + protected +
		"## " + handoffSectionProgress + "\n" + strings.Repeat("- 很长的进展\n", 600)
	out := normalizeHandoffBody(body)
	assert.Eq(t, 1, strings.Count(out, "src/a.go"))
	assert.StrContains(t, out, "- src/a.go: Parse 第 30 行")
	assert.Eq(t, 120, strings.Count(out, "硬约束：Go 标识符一律小写下划线"))
	assert.Eq(t, true, len(packageSectionBody(out, handoffSectionProgress)) <= handoffSectionBudgets[handoffSectionProgress])
	assert.StrContains(t, out, "字节预算")
	assert.Eq(t, "no sections at all", normalizeHandoffBody("no sections at all"))
}

// TestCoordKeys: a coordinate is keyed by its path and file+symbol pair, a
// free line by its text.
func TestCoordKeys(t *testing.T) {
	assert.Eq(t, []string{"p:src/a.go", "ps:src/a.go#Parse"}, coordKeys("- src/a.go: Parse 第 10 行"))
	assert.Eq(t, []string{"p:" + "D:/work/x.go", "ps:" + "D:/work/x.go#Order"}, coordKeys("* D:/work/x.go Order"))
	assert.Eq(t, []string{"s:main"}, coordKeys("- 分支 main"))
	assert.Eq(t, []string{"t:无"}, coordKeys("- 无"))
	assert.Eq(t, 0, len(coordKeys("   -   ")))
}
