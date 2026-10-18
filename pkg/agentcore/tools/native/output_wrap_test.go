package native

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/skills"
	"github.com/inhere/suga/pkg/agentcore/testutil"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// shortTempDir returns a fresh directory with a short path that is removed when
// the test ends.
func shortTempDir(t *testing.T) string {
	t.Helper()
	return testutil.Dir(t)
}

type countingSpiller struct{ calls map[string]int }

func (s *countingSpiller) Spill(_ context.Context, call api.ToolCall, _ string) (string, error) {
	s.calls[call.Name]++
	return "out:s:" + strings.Repeat("a", 16), nil
}

// TestOutputLayerWithNativeTools checks the split of responsibilities: tools
// return raw output up to RawBytes, file_read pages itself, and the output
// layer externalises every other oversized result.
func TestOutputLayerWithNativeTools(t *testing.T) {
	ws := shortTempDir(t)
	var sb strings.Builder
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&sb, "line %d of a large log file\n", i)
	}
	assert.NoErr(t, os.WriteFile(filepath.Join(ws, "big.log"), []byte(sb.String()), 0o644))
	many := filepath.Join(ws, "many")
	assert.NoErr(t, os.MkdirAll(many, 0o755))
	for i := 0; i < 400; i++ {
		assert.NoErr(t, os.WriteFile(filepath.Join(many, fmt.Sprintf("file-with-a-long-name-%04d.txt", i)), nil, 0o644))
	}
	skillRoot := filepath.Join(ws, "skills")
	assert.NoErr(t, os.MkdirAll(filepath.Join(skillRoot, "huge"), 0o755))
	body := "---\nname: huge\ndescription: d\n---\n" + strings.Repeat("step of a long skill body\n", 1000)
	assert.NoErr(t, os.WriteFile(filepath.Join(skillRoot, "huge", "SKILL.md"), []byte(body), 0o644))

	reg := tools.NewRegistry()
	for _, tool := range BuildTools(Env{Workspace: ws}, false) {
		reg.MustRegister(tool)
	}
	reg.MustRegister(NewSkillTool(skills.LoadRoots([]skills.Root{{Dir: skillRoot}}), 0))
	sp := &countingSpiller{calls: map[string]int{}}
	out := tools.NewOutput(reg, tools.OutputConfig{InlineBytes: DefaultMaxOutputBytes, Spiller: sp})
	run := func(name string, args map[string]any) *api.ToolResult {
		res, err := out.ExecuteTool(context.Background(), api.ToolCall{CallID: "c-" + name, Name: name, Arguments: args})
		assert.Require(t, assert.NoErr(t, err))
		return res
	}

	t.Run("file_read pages itself and is neither cut nor spilled", func(t *testing.T) {
		res := run("file_read", map[string]any{"path": "big.log"})
		assert.Eq(t, 0, sp.calls["file_read"])
		// One page of lines plus the "continue with offset" hint (whose guidance on
		// the file is bounded by truncationNoteBytes).
		assert.True(t, len(res.Content) <= DefaultReadPageBytes+truncationNoteBytes, "page %d", len(res.Content))
		assert.True(t, len(res.Content) > DefaultMaxOutputBytes, "a page is larger than the inline threshold and still not spilled")
		assert.StrNotContains(t, res.Content, "省略")
		assert.StrContains(t, res.Content, "continue with offset=")
		assert.StrNotContains(t, res.Content, "ref out:")
	})
	t.Run("file_list over the threshold is spilled", func(t *testing.T) {
		res := run("file_list", map[string]any{"dir": "many", "max_entries": 1000})
		assert.Eq(t, 1, sp.calls["file_list"])
		assert.True(t, len(res.Content) <= DefaultMaxOutputBytes)
		assert.StrContains(t, res.Content, "ref out:s:")
	})
	t.Run("skill over the threshold is spilled", func(t *testing.T) {
		res := run("skill", map[string]any{"name": "huge"})
		assert.Eq(t, 1, sp.calls["skill"])
		assert.True(t, len(res.Content) <= DefaultMaxOutputBytes)
		info, ok := tools.OutputInfoOf(res.Metadata)
		assert.True(t, ok)
		assert.True(t, info.Bytes > len(body)-100)
	})
}
