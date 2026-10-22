package personal

import (
	"fmt"
	"github.com/inhere/suga/pkg/agentcore"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// call builds one tool call as the turn's events report it.
func call(name string, ok bool, targets ...string) NudgeToolCall {
	return NudgeToolCall{Name: name, OK: ok, Targets: targets}
}

// shellCall builds one command-carrying check call as the turn's events report
// it (the command lives in the event's attrs).
func shellCall(ok bool, command string) NudgeToolCall {
	return NudgeToolCall{Name: defaultCheckTool, OK: ok, Command: command}
}

// builtinTestRules are the built-in rules for the tests below.
func builtinTestRules() []NudgeRule { return compileBuiltinRules() }

// ruleNames returns the rule names of the loaded rules, in order.
func ruleNames(rules []NudgeRule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.Name)
	}
	return out
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
		want  string // evidence part that must appear; "" = no unverified-write
	}{
		{
			name:  "a write followed by a command is verified",
			calls: []NudgeToolCall{call("file_edit", true, "a.go"), shellCall(true, "go test ./...")},
			want:  "",
		},
		{
			name:  "a command outside check_command is not a check",
			calls: []NudgeToolCall{call("file_edit", true, "a.go"), shellCall(true, "ls")},
			want:  "最后修改 a.go 之后没有运行 shell_exec",
		},
		{
			name:  "a command inside check_command is a check",
			calls: []NudgeToolCall{call("file_edit", true, "a.go"), shellCall(true, "make check")},
			want:  "",
		},
		{
			name:  "a write without a following command is unverified",
			calls: []NudgeToolCall{call("file_write", true, "a.go"), call("file_read", true)},
			want:  "最后修改 a.go 之后没有运行 shell_exec",
		},
		{
			name:  "the command has to follow the last write",
			calls: []NudgeToolCall{call("file_write", true, "a.go"), shellCall(true, "go vet ./..."), call("file_remove", true, "b.go")},
			want:  "b.go",
		},
		{
			name:  "a failed command still counts as run",
			calls: []NudgeToolCall{call("file_write", true, "a.go"), shellCall(false, "go test ./...")},
			want:  "",
		},
		{
			name:  "a failed write is not a change",
			calls: []NudgeToolCall{call("file_write", false, "a.go")},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := CheckNudge(builtinTestRules(), tt.calls, nil, "summary", true)
			if tt.want == "" {
				assert.NotContains(t, rules(findings), NudgeRuleUnverifiedWrite)
				return
			}
			assert.Require(t, assert.Contains(t, rules(findings), NudgeRuleUnverifiedWrite))
			assert.StrContains(t, findings[0].Evidence, tt.want)
		})
	}
	t.Run("an empty check_command accepts every command", func(t *testing.T) {
		rule := NudgeRule{Name: "w", Kind: NudgeKindUnverifiedWrite} // no check_command
		findings := CheckNudge([]NudgeRule{rule}, []NudgeToolCall{call("file_edit", true, "a.go"), shellCall(true, "echo hi")}, nil, "", true)
		assert.Len(t, findings, 0)
	})
	t.Run("a custom check_command matches what it names", func(t *testing.T) {
		rule := NudgeRule{Name: "w", Kind: NudgeKindUnverifiedWrite, CheckCommand: "^test "}
		rule.compile()
		findings := CheckNudge([]NudgeRule{rule}, []NudgeToolCall{call("file_edit", true, "a.go"), shellCall(true, "test ./...")}, nil, "", true)
		assert.Len(t, findings, 0)
	})
	t.Run("custom write/check tools are honored", func(t *testing.T) {
		rule := NudgeRule{Name: "w", Kind: NudgeKindUnverifiedWrite,
			WriteTools: []string{"file_write"}, CheckTools: []string{"agent"}}
		rule.compile()
		findings := CheckNudge([]NudgeRule{rule}, []NudgeToolCall{call("file_edit", true, "a.go")}, nil, "", true)
		assert.Len(t, findings, 0)
		findings = CheckNudge([]NudgeRule{rule}, []NudgeToolCall{call("file_write", true, "b.txt")}, nil, "", true)
		assert.Require(t, assert.Len(t, findings, 1))
		assert.StrContains(t, findings[0].Evidence, "最后修改 b.txt")
	})
	t.Run("open plan steps are their own rule", func(t *testing.T) {
		plan := &native.Plan{Steps: []native.PlanStep{{Step: "write the code", Status: native.PlanPending}, {Step: "run tests", Status: native.PlanInProgress}, {Step: "done", Status: native.PlanCompleted}}}
		findings := CheckNudge(builtinTestRules(), nil, plan, "", true)
		assert.Require(t, assert.Len(t, findings, 1))
		assert.Eq(t, NudgeRulePlanOpen, findings[0].Rule)
		assert.Eq(t, NudgeKindPlanOpen, findings[0].Kind)
		assert.StrContains(t, findings[0].Evidence, "计划还有 2 项未完成：write the code；run tests")
	})
	t.Run("a completed plan is done", func(t *testing.T) {
		plan := &native.Plan{Steps: []native.PlanStep{{Step: "write the code", Status: native.PlanCompleted}}}
		assert.Len(t, CheckNudge(builtinTestRules(), nil, plan, "", true), 0)
	})
	t.Run("both kinds make two findings in rule order", func(t *testing.T) {
		plan := &native.Plan{Steps: []native.PlanStep{{Step: "run tests", Status: native.PlanPending}}}
		findings := CheckNudge(builtinTestRules(), []NudgeToolCall{call("file_edit", true, "a.go")}, plan, "", true)
		assert.Require(t, assert.Len(t, findings, 2))
		assert.Eq(t, NudgeRuleUnverifiedWrite, findings[0].Rule)
		assert.Eq(t, NudgeKindUnverifiedWrite, findings[0].Kind)
		assert.StrContains(t, findings[0].Evidence, "a.go")
		assert.Eq(t, NudgeRulePlanOpen, findings[1].Rule)
		assert.StrContains(t, findings[1].Evidence, "run tests")
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
		{name: "chinese first person will", text: "改动都做完了，我会去跑集成测试。", plain: true, want: true},
		{name: "chinese first person 先", text: "我先运行一遍单元测试，再看结果。", plain: true, want: true},
		{name: "chinese first person 来", text: "我来检查一下构建。", plain: true, want: true},
		{name: "chinese 马上", text: "马上跑一遍完整测试。", plain: true, want: true},
		{name: "chinese 这就", text: "我这就去修改配置。", plain: true, want: true},
		{name: "chinese 接下来就", text: "接下来就开始补测试。", plain: true, want: true},
		{name: "english i will", text: "The file is written. I'll now run the tests.", plain: true, want: true},
		{name: "english next i will", text: "Done with the edit. Next, I'll check the build.", plain: true, want: true},
		{name: "english let me", text: "Let me run the test suite first", plain: true, want: true},
		// The report of 2026-10-22 (第三十一批 S3): a sub-agent described its test
		// procedure and the old (我)?(先|再...)-verb pattern read it as a promise.
		{name: "a described procedure is not a promise", text: "验证手法：先读取只读接口的 schema，再跑一遍只读接口，最后对比输出。", plain: true, want: false},
		{name: "a bare 再跑 is a description", text: "再跑一次测试就能通过。", plain: true, want: false},
		{name: "a described two-step test", text: "测试分两步：先运行单元测试，再跑集成测试。", plain: true, want: false},
		{name: "no first person marker", text: "这段代码先检查长度再解析。", plain: true, want: false},
		{name: "first person, past tense", text: "我跑了一遍测试，结果都通过。", plain: true, want: false},
		{name: "english past tense", text: "I ran the tests and they passed.", plain: true, want: false},
		{name: "a question is the person's decision", text: "要我接着跑测试吗？", plain: true, want: false},
		{name: "a trailing question mark", text: "接下来我去跑测试？", plain: true, want: false},
		{name: "a promise followed by a summary", text: "接下来我会跑测试。\n\n总结：本次只改了文档，测试未运行。", plain: true, want: false},
		{name: "a plain summary", text: "所有改动已完成，测试通过。", plain: true, want: false},
		{name: "a promise in an answer with tool calls", text: "接下来我去跑测试。", plain: false, want: false},
		{name: "an empty answer", text: "", plain: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := CheckNudge(builtinTestRules(), nil, nil, tt.text, tt.plain)
			assert.Eq(t, tt.want, len(findings) > 0)
			if tt.want {
				assert.Eq(t, NudgeKindPromise, findings[0].Kind)
				assert.Require(t, strings.HasPrefix(findings[0].Rule, "promise-"))
				assert.StrContains(t, findings[0].Evidence, "结尾说")
			}
		})
	}
	t.Run("only the end of a long answer is searched", func(t *testing.T) {
		// The promise sits far above the last ~300 characters: it is history.
		text := "接下来我去跑测试。\n" + longText(400)
		assert.Len(t, CheckNudge(builtinTestRules(), nil, nil, text, true), 0)
	})
	t.Run("the evidence stays on one line", func(t *testing.T) {
		plan := &native.Plan{Steps: []native.PlanStep{{Step: "run\nthe   tests", Status: native.PlanPending}}}
		findings := CheckNudge(builtinTestRules(), []NudgeToolCall{call("file_edit", true, "a.go")}, plan, "接下来我会跑测试\n并修复", true)
		assert.Require(t, assert.Len(t, findings, 3))
		for _, finding := range findings {
			assert.False(t, strings.Contains(finding.Evidence, "\n"))
			assert.False(t, strings.Contains(finding.Evidence, "  "))
		}
	})
}

func TestLoadNudgeRules(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.yaml")
	assert.NoErr(t, os.WriteFile(user, []byte("rules:\n  - name: promise-later\n    kind: promise\n    patterns: ['我稍后(会|再)']\n"), 0o644))

	t.Run("the found names", func(t *testing.T) {
		rules, errs := LoadNudgeRules()
		assert.Empty(t, errs)
		assert.Eq(t, []string{NudgeRulePromiseZh, NudgeRulePromiseEn, NudgeRuleUnverifiedWrite, NudgeRulePlanOpen}, ruleNames(rules))
	})
	t.Run("a missing file is skipped", func(t *testing.T) {
		rules, errs := LoadNudgeRules(filepath.Join(t.TempDir(), "missing.yaml"))
		assert.Empty(t, errs)
		assert.Eq(t, []string{NudgeRulePromiseZh, NudgeRulePromiseEn, NudgeRuleUnverifiedWrite, NudgeRulePlanOpen}, ruleNames(rules))
	})
	t.Run("a new promise rule is appended", func(t *testing.T) {
		rules, errs := LoadNudgeRules(user)
		assert.Empty(t, errs)
		assert.Require(t, assert.Len(t, rules, 5))
		assert.Eq(t, "promise-later", rules[4].Name)
		assert.Eq(t, NudgeKindPromise, rules[4].Kind)
		assert.Eq(t, user, rules[4].Source)
	})
	t.Run("a file replaces a same-name rule whole and a disabled entry removes it", func(t *testing.T) {
		disabled := filepath.Join(dir, "disabled.yaml")
		assert.NoErr(t, os.WriteFile(disabled, []byte("rules:\n  - name: plan-open\n    enabled: false\n"), 0o644))
		rules, errs := LoadNudgeRules(disabled)
		assert.Empty(t, errs)
		assert.NotContains(t, ruleNames(rules), NudgeRulePlanOpen)
		// A whole replacement forgets the old fields: a check_command entry
		// rebuilds the rule from the kind's defaults.
		check := filepath.Join(dir, "check.yaml")
		assert.NoErr(t, os.WriteFile(check, []byte("rules:\n  - name: unverified-write\n    check_command: ^test \n"), 0o644))
		rules, errs = LoadNudgeRules(check)
		assert.Empty(t, errs)
		assert.Require(t, assert.Len(t, rules, 4))
		var hit bool
		for i := range rules {
			if rules[i].Name != NudgeRuleUnverifiedWrite {
				continue
			}
			hit = true
			assert.StrContains(t, fmt.Sprint(rules[i].CheckCommand), "^test")
		}
		assert.True(t, hit)
	})
	t.Run("workspace overrides suga home", func(t *testing.T) {
		homeFile := filepath.Join(dir, "home.yaml")
		assert.NoErr(t, os.WriteFile(homeFile, []byte("rules:\n  - name: promise-zh\n    enabled: false\n"), 0o644))
		wsFile := filepath.Join(dir, "ws.yaml")
		assert.NoErr(t, os.WriteFile(wsFile, []byte("rules:\n  - name: plan-open\n    kind: plan_open\n"), 0o644))
		rules, errs := LoadNudgeRules(homeFile, wsFile)
		assert.Empty(t, errs)
		assert.NotContains(t, ruleNames(rules), NudgeRulePromiseZh)
		assert.Require(t, assert.Contains(t, ruleNames(rules), NudgeRulePlanOpen))
		for _, rule := range rules {
			if rule.Name == NudgeRulePlanOpen {
				assert.Eq(t, wsFile, rule.Source)
			}
		}
	})
	t.Run("a broken pattern, unknown kind and missing kind are reported", func(t *testing.T) {
		bad := filepath.Join(dir, "bad.yaml")
		assert.NoErr(t, os.WriteFile(bad, []byte("rules:\n  - name: broken\n    kind: promise\n    patterns: ['(真']\n"), 0o644))
		_, errs := LoadNudgeRules(bad)
		assert.Require(t, assert.Len(t, errs, 1))
		assert.StrContains(t, errs[0].Error(), "broken")

		unknown := filepath.Join(dir, "unknown.yaml")
		assert.NoErr(t, os.WriteFile(unknown, []byte("rules:\n  - name: odd\n    kind: mystery\n"), 0o644))
		_, errs = LoadNudgeRules(unknown)
		assert.Require(t, assert.Len(t, errs, 1))
		assert.StrContains(t, errs[0].Error(), "rule odd")

		kindless := filepath.Join(dir, "kindless.yaml")
		assert.NoErr(t, os.WriteFile(kindless, []byte("rules:\n  - name: fresh\n"), 0o644))
		_, errs = LoadNudgeRules(kindless)
		assert.Require(t, assert.Len(t, errs, 1))
		assert.StrContains(t, errs[0].Error(), "kind must be one of")
	})
	t.Run("compileBuiltinRules keeps a broken pattern out", func(t *testing.T) {
		for _, rule := range compileBuiltinRules() {
			assert.Require(t, assert.NoErr(t, rule.compile()))
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

// TestShellCommandAttrIsRedacted: the trace keeps the shell command for the
// check_command rule, redacted like the other call attributes.
func TestShellCommandAttrIsRedacted(t *testing.T) {
	attrs := map[string]any{}
	call := agentcore.ToolCall{Name: "shell_exec", Arguments: map[string]any{
		"command": "curl -H 'Authorization: Bearer sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789' https://x"}}
	addCallAttrs(attrs, call, &agentcore.ToolResult{}, "")
	command, _ := attrs["command"].(string)
	assert.StrContains(t, command, "curl")
	assert.NotContains(t, command, "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789")
}
