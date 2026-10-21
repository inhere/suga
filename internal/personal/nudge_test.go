package personal

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// call builds one tool call as the turn's events report it.
func call(name string, ok bool, targets ...string) NudgeToolCall {
	return NudgeToolCall{Name: name, OK: ok, Targets: targets}
}

// rules returns the rule names of the findings in order.
func rules(findings []NudgeFinding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, finding.Rule)
	}
	return out
}

func TestCheckNudgeUnverified(t *testing.T) {
	tests := []struct {
		name  string
		calls []NudgeToolCall
		plan  *native.Plan
		want  string // evidence part that must appear; "" = no N1
	}{
		{
			name:  "a write followed by a command is verified",
			calls: []NudgeToolCall{call("file_edit", true, "a.go"), call("shell_exec", true)},
			want:  "",
		},
		{
			name:  "a write without a following command is unverified",
			calls: []NudgeToolCall{call("file_write", true, "a.go"), call("file_read", true)},
			want:  "最后修改 a.go 之后没有运行 shell_exec",
		},
		{
			name:  "the command has to follow the last write",
			calls: []NudgeToolCall{call("file_write", true, "a.go"), call("shell_exec", true), call("file_remove", true, "b.go")},
			want:  "b.go",
		},
		{
			name:  "a failed command still counts as run",
			calls: []NudgeToolCall{call("file_write", true, "a.go"), call("shell_exec", false)},
			want:  "",
		},
		{
			name:  "a failed write is not a change",
			calls: []NudgeToolCall{call("file_write", false, "a.go")},
			want:  "",
		},
		{
			name: "open plan steps count on their own",
			plan: &native.Plan{Steps: []native.PlanStep{{Step: "write the code", Status: native.PlanPending}, {Step: "run tests", Status: native.PlanInProgress}, {Step: "done", Status: native.PlanCompleted}}},
			want: "计划还有 2 项未完成：write the code；run tests",
		},
		{
			name: "a completed plan is done",
			plan: &native.Plan{Steps: []native.PlanStep{{Step: "write the code", Status: native.PlanCompleted}}},
			want: "",
		},
		{
			name:  "both conditions are one finding",
			calls: []NudgeToolCall{call("file_edit", true, "a.go")},
			plan:  &native.Plan{Steps: []native.PlanStep{{Step: "run tests", Status: native.PlanPending}}},
			want:  "a.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := CheckNudge(tt.calls, tt.plan, "summary", true)
			if tt.want == "" {
				assert.NotContains(t, rules(findings), NudgeRuleUnverified)
				return
			}
			assert.Require(t, assert.Contains(t, rules(findings), NudgeRuleUnverified))
			assert.StrContains(t, findings[0].Evidence, tt.want)
		})
	}
	t.Run("both conditions share one evidence", func(t *testing.T) {
		findings := CheckNudge([]NudgeToolCall{call("file_edit", true, "a.go")},
			&native.Plan{Steps: []native.PlanStep{{Step: "run tests", Status: native.PlanPending}}}, "", true)
		assert.Require(t, assert.Len(t, findings, 1))
		assert.StrContains(t, findings[0].Evidence, "a.go")
		assert.StrContains(t, findings[0].Evidence, "run tests")
	})
}

func TestCheckNudgePromise(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		plain bool
		want  bool
	}{
		{name: "chinese promise", text: "文件已经改好。接下来我去跑测试。", plain: true, want: true},
		{name: "chinese next step", text: "下一步我会补上测试。", plain: true, want: true},
		{name: "chinese let me", text: "让我先运行一下单元测试", plain: true, want: true},
		{name: "english i will", text: "The file is written. I'll now run the tests.", plain: true, want: true},
		{name: "english next i will", text: "Done with the edit. Next, I'll check the build.", plain: true, want: true},
		{name: "english let me", text: "Let me run the test suite first", plain: true, want: true},
		{name: "a question is the person's decision", text: "要我接着跑测试吗？", plain: true, want: false},
		{name: "a trailing question mark", text: "接下来我去跑测试？", plain: true, want: false},
		{name: "a promise followed by a summary", text: "接下来我会跑测试。\n\n总结：本次只改了文档，测试未运行。", plain: true, want: false},
		{name: "a plain summary", text: "所有改动已完成，测试通过。", plain: true, want: false},
		{name: "a promise in an answer with tool calls", text: "接下来我去跑测试。", plain: false, want: false},
		{name: "an empty answer", text: "", plain: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := CheckNudge(nil, nil, tt.text, tt.plain)
			assert.Eq(t, tt.want, len(findings) > 0)
			if tt.want {
				assert.Eq(t, NudgeRulePromise, findings[0].Rule)
				assert.StrContains(t, findings[0].Evidence, "结尾说")
			}
		})
	}
	t.Run("only the end of a long answer is searched", func(t *testing.T) {
		// The promise sits far above the last ~300 characters: it is history.
		text := "接下来我去跑测试。\n" + longText(400)
		assert.Len(t, CheckNudge(nil, nil, text, true), 0)
	})
	t.Run("the evidence stays on one line", func(t *testing.T) {
		plan := &native.Plan{Steps: []native.PlanStep{{Step: "run\nthe   tests", Status: native.PlanPending}}}
		findings := CheckNudge([]NudgeToolCall{call("file_edit", true, "a.go")}, plan, "接下来我会跑测试\n并修复", true)
		assert.Require(t, assert.Len(t, findings, 2))
		for _, finding := range findings {
			assert.False(t, strings.Contains(finding.Evidence, "\n"))
			assert.False(t, strings.Contains(finding.Evidence, "  "))
		}
	})
}

// longText is n characters of filler that contains no promise.
func longText(n int) string {
	out := make([]rune, 0, n)
	for len(out) < n {
		out = append(out, []rune("填充文字 ")...)
	}
	return string(out)
}
