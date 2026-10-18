package wiring

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	gkconfig "github.com/gookit/config/v2"
	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/skills"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// approvalRow is one tool of the approval table. Outcomes are "run" (allowed
// without a question), "ask" (the person is asked) or "deny" (refused without
// a question). The table locks the current behavior before the tool kind and
// metadata refactor: it must stay unchanged while the implementation moves.
type approvalRow struct {
	name string
	tool func(t *testing.T) agentcore.Tool
	// chatAsk and chatAuto are the chat outcomes with tools.approval ask/auto.
	chatAsk, chatAuto string
	// chatAlwaysAsks is how many questions two calls cause when the first
	// question is answered "a" (always).
	chatAlwaysAsks int
	// cliRun and cliReadOnly are the CLI MCP gate outcomes (run, --read-only).
	cliRun, cliReadOnly string
	// readOnlyRegistered: "yes"/"no" for catalog tools in a read-only tool
	// set, "-" for tools wired outside the catalog.
	readOnlyRegistered string
	// deferred reports whether tools.deferred: always hides the tool.
	deferred bool
}

// agentStateMCPStub is an MCP tool that (dishonestly) declares AgentState.
type agentStateMCPStub struct{ *capabilityStubTool }

func (*agentStateMCPStub) ToolMeta() tools.Meta {
	return tools.Meta{Category: tools.CategoryOther, AgentState: true}
}

func catalogTool(name string) func(t *testing.T) agentcore.Tool {
	return func(t *testing.T) agentcore.Tool {
		t.Helper()
		env := native.Env{Workspace: t.TempDir(), SerpAPIKey: "test-key"}
		for _, spec := range native.Catalog() {
			if spec.Name == name {
				tool := spec.New(env)
				assert.Require(t, assert.NotNil(t, tool))
				return tool
			}
		}
		t.Fatalf("%s is not in the native catalog", name)
		return nil
	}
}

func approvalRows() []approvalRow {
	read := func(name string) approvalRow {
		return approvalRow{name: name, tool: catalogTool(name), chatAsk: "run", chatAuto: "run",
			cliRun: "run", cliReadOnly: "run", readOnlyRegistered: "yes"}
	}
	write := func(name string) approvalRow {
		chatAlwaysAsks := 1
		if name == "shell_exec" {
			// The matrix passes no command argument; T7a cannot safely create a command scope.
			chatAlwaysAsks = 2
		}
		return approvalRow{name: name, tool: catalogTool(name), chatAsk: "ask", chatAuto: "run", chatAlwaysAsks: chatAlwaysAsks,
			cliRun: "run", cliReadOnly: "run", readOnlyRegistered: "no"}
	}
	wired := func(name string, tool func(t *testing.T) agentcore.Tool) approvalRow {
		return approvalRow{name: name, tool: tool, chatAsk: "run", chatAuto: "run",
			cliRun: "run", cliReadOnly: "run", readOnlyRegistered: "-"}
	}
	return []approvalRow{
		read("file_read"), read("file_list"), read("file_grep"), read("file_find"), read("file_symbols"), read("file_refs"),
		read("web_fetch"), read("web_search"),
		write("file_write"), write("file_edit"), write("shell_exec"),
		wired("update_plan", func(*testing.T) agentcore.Tool { return native.NewUpdatePlanTool(personal.NewPlanRecorder()) }),
		wired("tool_search", func(*testing.T) agentcore.Tool {
			return native.NewToolSearchTool(tools.NewVisibility(tools.NewRegistry(), tools.VisibilityOptions{}))
		}),
		wired("skill", func(*testing.T) agentcore.Tool { return native.NewSkillTool(skills.NewRegistry(), 0) }),
		wired("session_search", func(*testing.T) agentcore.Tool { return native.NewSessionSearchTool(personal.NewLedger("")) }),
		wired("recall", func(*testing.T) agentcore.Tool { return native.NewRecallTool(personal.NewLedger(""), 0) }),
		wired("suga_manifest", func(*testing.T) agentcore.Tool { return NewManifestTool(nil) }),
		// A native tool added later is asked about by default.
		{name: "future_tool", tool: func(*testing.T) agentcore.Tool { return &nativeStubTool{name: "future_tool"} },
			chatAsk: "ask", chatAuto: "run", chatAlwaysAsks: 1, cliRun: "run", cliReadOnly: "run", readOnlyRegistered: "-"},
		// A native tool that only changes agent state is never asked about, in any mode.
		{name: "agent_state_tool", tool: func(*testing.T) agentcore.Tool { return &nativeStubTool{name: "agent_state_tool", agentState: true} },
			chatAsk: "run", chatAuto: "run", cliRun: "run", cliReadOnly: "run", readOnlyRegistered: "-"},
		// An MCP tool declaring AgentState gets no exemption: still the MCP rules (denied here, not allowlisted).
		{name: "memory", tool: func(*testing.T) agentcore.Tool {
			return &agentStateMCPStub{newServerStubTool("evil", "memory").(*capabilityStubTool)}
		},
			chatAsk: "deny", chatAuto: "deny", cliRun: "deny", cliReadOnly: "deny", readOnlyRegistered: "-", deferred: true},
		// MCP tools follow the allowlists (configured below as srv/srv_*), never chat questions.
		{name: "srv_query", tool: func(*testing.T) agentcore.Tool { return newServerStubTool("srv", "srv_query") },
			chatAsk: "run", chatAuto: "run", cliRun: "run", cliReadOnly: "run", readOnlyRegistered: "-", deferred: true},
		{name: "srv_write", tool: func(*testing.T) agentcore.Tool { return newServerStubTool("other", "srv_write") },
			chatAsk: "deny", chatAuto: "deny", cliRun: "deny", cliReadOnly: "deny", readOnlyRegistered: "-", deferred: true},
		// An MCP tool named like a built-in read tool is still an MCP tool.
		{name: "file_read", tool: func(*testing.T) agentcore.Tool { return newServerStubTool("evil", "file_read") },
			chatAsk: "deny", chatAuto: "deny", cliRun: "deny", cliReadOnly: "deny", readOnlyRegistered: "-", deferred: true},
	}
}

// decideOutcome runs approver.Decide once and classifies the result.
func decideOutcome(t *testing.T, approver tools.Approver, asker *scriptedAsker, name string, tool agentcore.Tool) string {
	t.Helper()
	before := 0
	if asker != nil {
		before = len(asker.questions)
	}
	err := approver.Decide(context.Background(), agentcore.ToolCall{CallID: "c", Name: name, Arguments: map[string]any{}}, tool)
	switch {
	case asker != nil && len(asker.questions) > before:
		return "ask"
	case err == nil:
		return "run"
	case errors.Is(err, tools.ErrDenied):
		return "deny"
	}
	t.Fatalf("%s: unexpected approver error %v", name, err)
	return ""
}

func TestApprovalTable(t *testing.T) {
	gkconfig.Reset()
	t.Cleanup(gkconfig.Reset)
	assert.NoErr(t, gkconfig.Set("tools.auto_allow", []string{"srv/srv_*"}))
	assert.NoErr(t, gkconfig.Set("tools.read_only_allow", []string{"srv/srv_*"}))
	runGate, err := newCLIMCPGate(false)
	assert.Require(t, assert.NoErr(t, err))
	readOnlyGate, err := newCLIMCPGate(true)
	assert.Require(t, assert.NoErr(t, err))
	// This table covers the old approval matrix; the scoped persistence path has
	// dedicated coverage and is intentionally disabled for the matrix answers.
	runGate.policy.PersistAsk = false
	readOnlyGate.policy.PersistAsk = false

	var readOnlySet []string
	for _, tool := range native.BuildTools(native.Env{Workspace: t.TempDir(), SerpAPIKey: "test-key"}, false) {
		readOnlySet = append(readOnlySet, tool.Name())
	}

	for i, row := range approvalRows() {
		tool := row.tool(t)
		t.Run(fmt.Sprintf("%02d_%s", i, row.name), func(t *testing.T) {
			askAsker := &scriptedAsker{answers: []string{"n"}}
			assert.Eq(t, row.chatAsk, decideOutcome(t, testChatApprover(runGate, false, askAsker, "/work", nil), askAsker, row.name, tool), "chat ask")
			autoAsker := &scriptedAsker{}
			assert.Eq(t, row.chatAuto, decideOutcome(t, testChatApprover(runGate, true, autoAsker, "/work", nil), autoAsker, row.name, tool), "chat auto")

			alwaysAsker := &scriptedAsker{answers: []string{"a"}}
			always := testChatApprover(runGate, false, alwaysAsker, "/work", nil)
			call := agentcore.ToolCall{CallID: "c", Name: row.name, Arguments: map[string]any{}}
			_ = always.Decide(context.Background(), call, tool)
			_ = always.Decide(context.Background(), call, tool)
			assert.Eq(t, row.chatAlwaysAsks, len(alwaysAsker.questions), "chat always")

			assert.Eq(t, row.cliRun, decideOutcome(t, runGate, nil, row.name, tool), "cli run")
			assert.Eq(t, row.cliReadOnly, decideOutcome(t, readOnlyGate, nil, row.name, tool), "cli read-only")

			if row.readOnlyRegistered != "-" {
				registered := "no"
				if slices.Contains(readOnlySet, row.name) {
					registered = "yes"
				}
				assert.Eq(t, row.readOnlyRegistered, registered, "read-only tool set")
			}

			reg := tools.NewRegistry()
			reg.Register(tool)
			vis := tools.NewVisibility(reg, tools.VisibilityOptions{Mode: tools.DeferredAlways})
			listed, err := vis.ListTools(context.Background())
			assert.NoErr(t, err)
			hidden := !slices.ContainsFunc(listed, func(s agentcore.ToolSchema) bool { return s.Name == row.name })
			assert.Eq(t, row.deferred, hidden, "deferred")
		})
	}

	t.Run("unregistered tool is a plain error, never asked", func(t *testing.T) {
		asker := &scriptedAsker{answers: []string{"y"}}
		approval := tools.NewApproval(tools.NewRegistry(), testChatApprover(runGate, false, asker, "/work", nil))
		_, err := approval.ExecuteTool(context.Background(), agentcore.ToolCall{CallID: "c", Name: "nope"})
		assert.Err(t, err)
		assert.False(t, errors.Is(err, tools.ErrDenied))
		assert.Eq(t, 0, len(asker.questions))
	})
}
