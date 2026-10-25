package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// runCompare is the `compare` subcommand: it aligns two finished eval runs by
// task and classifies the difference (helped / hurt / no observed effect /
// confounded / insufficient samples), prints a markdown report and appends one
// line to the ledger. Usage:
//
//	go run ./scripts/eval compare <基线目录> <候选目录> [--ledger tmp/eval/ledger.jsonl] [--report out.md]
//
// Each side is a directory (holding results.jsonl), a results.jsonl file or a
// glob matching several of them (a group that was run task by task lives in
// several output dirs, e.g. tmp/eval/deferred-off-*).
func runCompare(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	ledger := fs.String("ledger", filepath.Join("tmp", "eval", "ledger.jsonl"), "台账 jsonl 路径（追加写一行）")
	report := fs.String("report", "", "把 markdown 报告另存到该路径（默认只打印）")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "用法：eval compare <基线目录|glob> <候选目录|glob> [flags]\n")
		fs.PrintDefaults()
	}
	// Flags and the two positional dirs may be interleaved (the flag package
	// stops at the first one): parse, take one positional, parse the rest.
	var pos []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 2 {
		fs.Usage()
		return errors.New("compare 需要两个参数：基线目录与候选目录")
	}
	base, err := loadCompareGroup(pos[0])
	if err != nil {
		return err
	}
	cand, err := loadCompareGroup(pos[1])
	if err != nil {
		return err
	}
	md, entry := compareReport(base, cand, time.Now())
	fmt.Fprint(stdout, md)
	if *report != "" {
		if err := os.MkdirAll(filepath.Dir(*report), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*report, []byte(md), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "\nreport: %s\n", *report)
	}
	if err := appendLedger(*ledger, entry); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ledger: %s（%s）\n", *ledger, entry.Verdict)
	return nil
}

// compareGroup is one side of a comparison: the results.jsonl rows of one or
// more output dirs (one dir per task set or per variant of the same run).
type compareGroup struct {
	// Label is what the user passed (a dir or a glob).
	Label string
	// Dirs are the output dirs that contributed rows, in load order.
	Dirs []string
	// Skipped are the matched dirs without rows (an aborted run) — listed in the
	// report so an empty group member is not silently dropped.
	Skipped []string
	Rows    []Result
}

// loadCompareGroup loads one side: a directory with results.jsonl, a
// results.jsonl file, or a glob matching several of them.
func loadCompareGroup(arg string) (*compareGroup, error) {
	paths, err := expandComparePaths(arg)
	if err != nil {
		return nil, err
	}
	g := &compareGroup{Label: arg}
	for _, p := range paths {
		rows, err := readResultsFile(p)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			g.Skipped = append(g.Skipped, p)
			continue
		}
		g.Dirs = append(g.Dirs, p)
		g.Rows = append(g.Rows, rows...)
	}
	if len(g.Rows) == 0 {
		return nil, fmt.Errorf("%s：没有 results.jsonl 结果行", arg)
	}
	return g, nil
}

// expandComparePaths turns a compare argument into result paths: a glob is
// expanded (sorted, so the report is reproducible), a plain path must exist. A
// directory stands for its results.jsonl.
func expandComparePaths(arg string) ([]string, error) {
	paths := []string{arg}
	if strings.ContainsAny(arg, "*?[") {
		matches, err := filepath.Glob(arg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("%s：没有匹配的目录", arg)
		}
		sort.Strings(matches)
		paths = matches
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// readResultsFile reads the results.jsonl rows of one compare path: a
// results.jsonl file is read directly, a directory through its results.jsonl.
// A path without that file is an empty run (a group member may have crashed
// before writing its first row), not an error.
func readResultsFile(path string) ([]Result, error) {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		path = filepath.Join(path, "results.jsonl")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []Result
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r Result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// ledgerEntry is one line of the ledger (default tmp/eval/ledger.jsonl): what
// was compared, under which variant and commit, and what compare concluded.
type ledgerEntry struct {
	Time    string   `json:"time"`
	Base    []string `json:"base"`
	Cand    []string `json:"cand"`
	Variant string   `json:"variant"`
	GitSHA  string   `json:"git_sha"`
	// CompareHead is the repository commit compare itself ran at (the runs'
	// own commit field is empty for results.jsonl written before it existed).
	CompareHead string   `json:"compare_head,omitempty"`
	Verdict     string   `json:"verdict"`
	Reasons     []string `json:"reasons,omitempty"`
	// Metrics are the overall relative changes (calls, prompt_total,
	// prompt_per_call, duration, ...) and pass_rate_pp, the pass-rate change in
	// percentage points; keys match the report's table.
	Metrics map[string]float64 `json:"metrics,omitempty"`
	Tasks   []ledgerTask       `json:"tasks"`
}

// ledgerTask is one task's verdict inside a ledger entry.
type ledgerTask struct {
	Task       string   `json:"task"`
	Verdict    string   `json:"verdict"`
	BasePass   int      `json:"base_passed"`
	BaseRounds int      `json:"base_rounds"`
	CandPass   int      `json:"cand_passed"`
	CandRounds int      `json:"cand_rounds"`
	Reasons    []string `json:"reasons,omitempty"`
}

// appendLedger appends one entry as a jsonl line, creating the directory.
func appendLedger(path string, e ledgerEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// headCommit is the git commit of the working directory ("" when git is not
// available or the directory is no repository).
func headCommit() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// taskDiff is one task of the comparison: both sides' rows, the gated metrics,
// the verdict and the tool call differences.
type taskDiff struct {
	Task    string
	Base    []Result
	Cand    []Result
	Metrics []metricDiff
	Verdict Verdict
}

// compareReport builds the markdown report and the ledger entry of one
// comparison. It is the whole behaviour of `compare` minus the printing, so the
// tests can look at the report without a terminal.
func compareReport(base, cand *compareGroup, now time.Time) (string, ledgerEntry) {
	baseOrder, baseTasks := rowsByTask(base.Rows)
	candOrder, candTasks := rowsByTask(cand.Rows)
	tasks := unionOrder(baseOrder, candOrder)

	costConf := globalConfounds(base, cand)
	onlyOneSide := []string{}
	diffs := make([]taskDiff, 0, len(tasks))
	for _, t := range tasks {
		bd, cd := baseTasks[t], candTasks[t]
		td := taskDiff{Task: t, Base: bd, Cand: cd}
		for _, m := range costMetrics {
			td.Metrics = append(td.Metrics, compareMetric(bd, cd, m))
		}
		conf := append([]string{}, costConf...)
		if len(bd) == 0 || len(cd) == 0 {
			onlyOneSide = append(onlyOneSide, t)
			conf = append(conf, fmt.Sprintf("只有一侧有该任务的结果（基线 %d 轮 / 候选 %d 轮）", len(bd), len(cd)))
		} else if len(bd) != len(cd) {
			conf = append(conf, fmt.Sprintf("样本数不同（基线 %d 轮 / 候选 %d 轮）", len(bd), len(cd)))
		}
		td.Verdict = classifyVerdict(classInput{
			BaseRounds: len(bd), BasePassed: passedCount(bd),
			CandRounds: len(cd), CandPassed: passedCount(cd),
			Metrics:   td.Metrics,
			Confounds: conf,
		})
		diffs = append(diffs, td)
	}

	overallMetrics := make([]metricDiff, 0, len(costMetrics))
	for _, m := range costMetrics {
		overallMetrics = append(overallMetrics, compareMetric(base.Rows, cand.Rows, m))
	}
	overallVerdict := classifyVerdict(classInput{
		BaseRounds: len(base.Rows), BasePassed: passedCount(base.Rows),
		CandRounds: len(cand.Rows), CandPassed: passedCount(cand.Rows),
		Metrics:   overallMetrics,
		Confounds: append(append([]string{}, costConf...), taskSetConfound(onlyOneSide)...),
		PassMixed: passMixed(diffs),
	})

	md, entry := renderCompare(base, cand, diffs, overallMetrics, overallVerdict, costConf, now)
	return md, entry
}

// renderCompare writes the markdown report and the ledger entry.
func renderCompare(base, cand *compareGroup, diffs []taskDiff, overall []metricDiff, verdict Verdict, confounds []string, now time.Time) (string, ledgerEntry) {
	var b strings.Builder
	fmt.Fprintf(&b, "# 评测台账对比：%s → %s\n\n", base.Label, cand.Label)
	fmt.Fprintf(&b, "- 生成：%s\n", now.Format("2006-01-02 15:04:05"))
	for _, g := range []*compareGroup{base, cand} {
		fmt.Fprintf(&b, "- %s：%d 个目录、%d 行（%s）\n", sideName(g, base), len(g.Dirs), len(g.Rows), describeTasks(g.Rows))
	}
	fmt.Fprintf(&b, "- 变体/参数：%s\n", variantLabel(base, cand))
	fmt.Fprintf(&b, "- git sha：%s；compare 运行时的仓库 HEAD：%s\n", gitSHA(base, cand), orUnknown(headCommit()))
	fmt.Fprintf(&b, "- 判定门槛：调用数 ±%s、prompt ±%s、耗时 ±%s，每侧至少 %d 轮；规则见 README「台账」\n",
		formatGate(callsGate), formatGate(tokenGate), formatGate(durationGate), minRounds)
	if sk := append(append([]string{}, base.Skipped...), cand.Skipped...); len(sk) > 0 {
		fmt.Fprintf(&b, "- 跳过的空目录（没有结果行）：%s\n", joinDirs(sk))
	}
	fmt.Fprintf(&b, "\n## 结论：%s\n\n", verdict.Label)
	for _, reason := range verdict.Reasons {
		fmt.Fprintf(&b, "- %s\n", reason)
	}

	fmt.Fprintf(&b, "\n## 总体指标\n\n")
	fmt.Fprintln(&b, "| 指标 | 基线 | 候选 | 差异 | 区间(95%) | 门槛 | 判定 |")
	fmt.Fprintln(&b, "|---|---|---|---|---|---|---|")
	fmt.Fprintf(&b, "| 通过率 | %s | %s | %s | - | 上升/下降 | %s |\n",
		passText(passedCount(base.Rows), len(base.Rows)), passText(passedCount(cand.Rows), len(cand.Rows)),
		passDeltaText(passedCount(base.Rows), len(base.Rows), passedCount(cand.Rows), len(cand.Rows)), passWord(base.Rows, cand.Rows, passMixed(diffs)))
	for _, m := range overall {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | ±%s | %s |\n",
			m.Label, statText(m.Base, m.Key == "duration"), statText(m.Cand, m.Key == "duration"),
			formatRel(m.Delta), intervalText(m), formatGate(m.Gate), metricWord(m.Dir))
	}
	fmt.Fprintf(&b, "\n成本拆分：%s\n", costSplit(verdict, overall))

	fmt.Fprintf(&b, "\n## 耗时拆分（每轮均值，秒）\n\n")
	fmt.Fprintln(&b, "| 项 | 基线 | 候选 | 差异 |")
	fmt.Fprintln(&b, "|---|---|---|---|")
	for _, row := range timingRows(base.Rows, cand.Rows) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", row.label, row.base, row.cand, row.delta)
	}
	fmt.Fprintf(&b, "\nprovider 故障耗时是 turn 的子集（重试发生在回合内部）；某组占回合时间超过 %s 且另一组低于 %s 时判为 confounded。\n",
		formatGate(faultShareGate), formatGate(lowFaultShare))

	fmt.Fprintf(&b, "\n## 逐任务\n\n")
	fmt.Fprintln(&b, "| 任务 | 通过 | 调用数 | 每次调用 prompt | 耗时(秒) | 结论 |")
	fmt.Fprintln(&b, "|---|---|---|---|---|---|")
	for _, d := range diffs {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | **%s** |\n", d.Task,
			passText(passedCount(d.Base), len(d.Base))+" → "+passText(passedCount(d.Cand), len(d.Cand)),
			metricArrow(d.Metrics, "calls"), metricArrow(d.Metrics, "prompt_per_call"),
			metricArrow(d.Metrics, "duration"), d.Verdict.Label)
	}
	fmt.Fprintf(&b, "\n逐任务理由：\n\n")
	for _, d := range diffs {
		fmt.Fprintf(&b, "- **%s** — %s：%s\n", d.Task, d.Verdict.Label, strings.Join(d.Verdict.Reasons, "；"))
	}

	fmt.Fprintf(&b, "\n## 工具调用差异（每轮均值；**%s** 为 |Δ|≥%.1f 且 ≥%s 的变化）\n\n",
		"显著", toolCountMin, formatGate(toolCountGate))
	fmt.Fprintln(&b, "| 工具 | 基线 | 候选 | 差异 |")
	fmt.Fprintln(&b, "|---|---|---|---|")
	for _, d := range toolDiffs(base.Rows, cand.Rows) {
		b.WriteString(toolLine(d))
	}
	for _, d := range diffs {
		if len(d.Base) == 0 || len(d.Cand) == 0 {
			continue
		}
		var marked []string
		for _, td := range toolDiffs(d.Base, d.Cand) {
			if td.Significant {
				marked = append(marked, fmt.Sprintf("%s %.1f→%.1f", td.Tool, td.Base, td.Cand))
			}
		}
		if len(marked) > 0 {
			fmt.Fprintf(&b, "\n- %s：%s\n", d.Task, strings.Join(marked, "、"))
		}
	}

	fmt.Fprintf(&b, "\n## 两组一致性（confounded 检查）\n\n")
	if len(confounds) > 0 {
		for _, c := range confounds {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	} else {
		fmt.Fprintln(&b, "- 模型、agent、commit、web 策略一致；每任务样本数相同；两组都没有占回合 20% 以上的 provider 故障")
	}

	// The ledger keeps every task, not only the shared ones: a task that only
	// one side ran is a fact worth reading back later.
	entry := ledgerEntry{
		Time:        now.Format(time.RFC3339),
		Base:        base.Dirs,
		Cand:        cand.Dirs,
		Variant:     variantLabel(base, cand),
		GitSHA:      gitSHA(base, cand),
		CompareHead: headCommit(),
		Verdict:     verdict.Label,
		Reasons:     verdict.Reasons,
		Metrics:     ledgerMetrics(overall, base, cand),
	}
	for _, d := range diffs {
		entry.Tasks = append(entry.Tasks, ledgerTask{
			Task: d.Task, Verdict: d.Verdict.Label,
			BasePass: passedCount(d.Base), BaseRounds: len(d.Base),
			CandPass: passedCount(d.Cand), CandRounds: len(d.Cand),
			Reasons: d.Verdict.Reasons,
		})
	}
	fmt.Fprintf(&b, "\n## 台账\n\n本结论会追加写入台账（时间、两组目录、变体、git sha 与每任务结论）。\n")
	return b.String(), entry
}

// rowsByTask groups rows by task name, returning the tasks in first-seen order.
func rowsByTask(rows []Result) ([]string, map[string][]Result) {
	order := make([]string, 0, len(rows))
	byTask := map[string][]Result{}
	for _, r := range rows {
		if _, ok := byTask[r.Task]; !ok {
			order = append(order, r.Task)
		}
		byTask[r.Task] = append(byTask[r.Task], r)
	}
	return order, byTask
}

// unionOrder merges two task orders without duplicates (a then b).
func unionOrder(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, t := range list {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// passedCount counts the passing rows.
func passedCount(rows []Result) int {
	n := 0
	for _, r := range rows {
		if r.Passed {
			n++
		}
	}
	return n
}

// describeTasks summarises a group as "N 个任务 × M 轮"（M 为每任务最多轮数）.
func describeTasks(rows []Result) string {
	order, byTask := rowsByTask(rows)
	maxRounds := 0
	for _, t := range order {
		maxRounds = max(maxRounds, len(byTask[t]))
	}
	return fmt.Sprintf("%d 个任务，最多 %d 轮/任务", len(order), maxRounds)
}

// passMixed reports whether a net pass rise hides a task that got worse: the
// overall quality change is then not a clean win.
func passMixed(diffs []taskDiff) bool {
	for _, d := range diffs {
		if len(d.Base) > 0 && len(d.Cand) > 0 && passedCount(d.Cand) < passedCount(d.Base) {
			return true
		}
	}
	return false
}

// distinctValues lists the distinct values of one field, sorted.
func distinctValues(rows []Result, get func(Result) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if v := get(r); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// globalConfounds lists the facts that make the whole comparison unsound: a
// field that differs inside one group or between the two groups, and a provider
// fault share that is much higher on one side. The --suga-set / --variant
// difference is NOT a confound: it is the treatment under test, and it is
// recorded in the ledger's variant field instead.
func globalConfounds(base, cand *compareGroup) []string {
	var out []string
	fields := []struct {
		name string
		get  func(Result) string
	}{
		{"模型", func(r Result) string { return r.Model }},
		{"agent", func(r Result) string { return r.Agent }},
		{"二进制提交", func(r Result) string { return r.Commit }},
		{"web 策略", func(r Result) string { return r.Web }},
		{"fake 模型", func(r Result) string { return fmt.Sprint(r.Fake) }},
		{"运行参数（组内）", func(r Result) string { return r.AgentArgs }},
	}
	for _, f := range fields {
		bs, cs := distinctValues(base.Rows, f.get), distinctValues(cand.Rows, f.get)
		if len(bs) > 1 || len(cs) > 1 {
			out = append(out, fmt.Sprintf("%s 在组内不一致：基线 %s / 候选 %s", f.name, listOrNone(bs), listOrNone(cs)))
			continue
		}
		// An empty field is "unknown", not "different": old results.jsonl files
		// have no commit field at all.
		if len(bs) == 1 && len(cs) == 1 && bs[0] != cs[0] && !(f.name == "运行参数（组内）") {
			out = append(out, fmt.Sprintf("%s 不一致：基线 %s / 候选 %s", f.name, bs[0], cs[0]))
		}
	}
	bf, cf := faultShare(base.Rows), faultShare(cand.Rows)
	if (bf >= faultShareGate && cf < lowFaultShare) || (cf >= faultShareGate && bf < lowFaultShare) {
		out = append(out, fmt.Sprintf("provider 故障耗时占比相差过大：基线 %.1f%% / 候选 %.1f%%（门槛 %.0f%% 对 %.0f%%）",
			bf*100, cf*100, faultShareGate*100, lowFaultShare*100))
	}
	return out
}

// taskSetConfound reports a task set that only one side ran: the overall
// averages then cover different work. Shared tasks stay comparable, so this is
// not added to their verdicts.
func taskSetConfound(onlyOneSide []string) []string {
	if len(onlyOneSide) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("任务集不同（只有一侧有：%s）", strings.Join(onlyOneSide, ", "))}
}

// variantLabel describes the treatment of the two groups: the --variant dir
// names and the --suga-set / --suga-args recorded per row.
func variantLabel(base, cand *compareGroup) string {
	bl, cl := groupTreatment(base), groupTreatment(cand)
	if bl == cl {
		return bl
	}
	return "基线 " + bl + " / 候选 " + cl
}

// groupTreatment is one group's variant and extra-args description ("-" when
// neither was set).
func groupTreatment(g *compareGroup) string {
	var parts []string
	if vs := distinctValues(g.Rows, func(r Result) string { return r.Variant }); len(vs) > 0 {
		parts = append(parts, "variant="+strings.Join(vs, ","))
	}
	if as := distinctValues(g.Rows, func(r Result) string { return r.AgentArgs }); len(as) > 0 {
		parts = append(parts, "--suga-args/--suga-set="+strings.Join(as, ","))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// gitSHA is the commit the compared runs were started from, "unknown" when the
// results.jsonl files predate the commit field.
func gitSHA(base, cand *compareGroup) string {
	get := func(r Result) string { return r.Commit }
	bs := distinctValues(base.Rows, get)
	cs := distinctValues(cand.Rows, get)
	switch {
	case len(bs) == 0 && len(cs) == 0:
		return "unknown（结果里没有 commit 字段）"
	case len(bs) == 1 && len(cs) == 1 && bs[0] == cs[0]:
		return bs[0]
	case len(bs) == 0:
		return "unknown / " + strings.Join(cs, ",")
	case len(cs) == 0:
		return strings.Join(bs, ",") + " / unknown"
	default:
		return strings.Join(bs, ",") + " / " + strings.Join(cs, ",")
	}
}

// ledgerMetrics keeps the overall numbers a later trend reads back: the
// relative change of every gated metric plus the pass-rate change in
// percentage points.
func ledgerMetrics(overall []metricDiff, base, cand *compareGroup) map[string]float64 {
	out := make(map[string]float64, len(overall)+1)
	for _, m := range overall {
		out[m.Key] = math.Round(m.Delta*1e4) / 1e4
	}
	if n := len(base.Rows); n > 0 {
		out["pass_rate_pp"] = math.Round((float64(passedCount(cand.Rows))/float64(len(cand.Rows))-float64(passedCount(base.Rows))/float64(n))*1e4) / 1e4
	}
	return out
}
