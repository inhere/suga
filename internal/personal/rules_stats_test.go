package personal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// writeRuleFile writes content at dir/AGENTS.md.
func writeRuleFile(t *testing.T, dir, content string) string {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, DefaultRuleFile)
	assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// TestLoadRulesDetailed: the report describes each source file, keeps the rules
// text byte-identical to LoadRules, and reports the 64KB per-file and 128KB
// total truncation.
func TestLoadRulesDetailed(t *testing.T) {
	t.Run("per file sizes", func(t *testing.T) {
		root := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
		home := filepath.Join(root, "home")
		userFile := writeRuleFile(t, home, "USER RULES\n")
		projectFile := writeRuleFile(t, root, "PROJECT RULES\n")

		rules, hash, stats, err := LoadRulesDetailed(root, home, false, "")
		assert.Require(t, assert.NoErr(t, err))
		plain, plainHash, err := LoadRules(root, home, false, "")
		assert.Require(t, assert.NoErr(t, err))
		// Diagnostics must not change what the model receives.
		assert.Eq(t, plain, rules)
		assert.Eq(t, plainHash, hash)

		assert.Require(t, assert.Len(t, stats.Files, 2))
		assert.Eq(t, userFile, stats.Files[0].Path)
		assert.Eq(t, len("USER RULES\n"), stats.Files[0].Original)
		assert.Eq(t, len("USER RULES\n"), stats.Files[0].Kept)
		assert.Eq(t, projectFile, stats.Files[1].Path)
		assert.Eq(t, len("PROJECT RULES\n"), stats.Files[1].Original)
		assert.False(t, stats.Truncated)
		assert.Eq(t, len("USER RULES\n")+len("PROJECT RULES\n"), stats.OriginalBytes)
	})

	t.Run("include is a separate entry", func(t *testing.T) {
		root := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(root, "extra.md"), []byte("INCLUDED\n"), 0o644))
		writeRuleFile(t, root, "@extra.md\nPARENT\n")

		rules, _, stats, err := LoadRulesDetailed(root, "", false, "")
		assert.Require(t, assert.NoErr(t, err))
		assert.StrContains(t, rules, "INCLUDED")
		assert.Require(t, assert.Len(t, stats.Files, 2))
		assert.Eq(t, len("PARENT\n"), stats.Files[0].Kept)
		assert.False(t, stats.Files[0].Included)
		assert.True(t, stats.Files[1].Included)
		assert.Eq(t, len("INCLUDED\n"), stats.Files[1].Kept)
		// Per-file bytes never double count the included content.
		assert.Eq(t, stats.KeptBytes, stats.Files[0].Kept+stats.Files[1].Kept)
	})

	t.Run("single file over 64KB", func(t *testing.T) {
		root := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
		big := strings.Repeat("a", maxRuleFileBytes+100)
		writeRuleFile(t, root, big)

		rules, _, stats, err := LoadRulesDetailed(root, "", false, "")
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, stats.Truncated)
		assert.Require(t, assert.Len(t, stats.Files, 1))
		assert.True(t, stats.Files[0].Truncated)
		assert.Eq(t, len(big), stats.Files[0].Original)
		assert.True(t, stats.KeptBytes < stats.OriginalBytes)
		assert.StrContains(t, rules, "[规则已截断: 单文件超过 64KB]")
	})

	t.Run("total over 128KB", func(t *testing.T) {
		root := t.TempDir()
		assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
		sub := filepath.Join(root, "sub")
		home := filepath.Join(root, "home")
		part := strings.Repeat("b", 60*1024)
		writeRuleFile(t, home, part)
		writeRuleFile(t, root, part)
		writeRuleFile(t, sub, part)

		rules, _, stats, err := LoadRulesDetailed(sub, home, false, "")
		assert.Require(t, assert.NoErr(t, err))
		assert.True(t, stats.Truncated)
		assert.True(t, stats.KeptBytes < stats.OriginalBytes)
		assert.StrContains(t, rules, "[规则已截断: 总量超过 128KB]")
	})
}

// TestRenderSegmentPartsUnchanged pins the exact segment context bytes: the
// resident context report must not alter what an existing session sends.
func TestRenderSegmentPartsUnchanged(t *testing.T) {
	c := &segmentContext{
		Environment: "# Environment\n- OS: x", Rules: "RULES", MemoryIndex: "MEMORY",
		Skills: "SKILLS", AppendPrompt: "APPEND", DeferredTools: "DEFERRED",
	}
	want := "# Environment\n- OS: x\n\n" + rulesHeading + "\n\nRULES\n\nMEMORY\n\nSKILLS\n\n" +
		appendHeading + "\n\nAPPEND\n\nDEFERRED"
	assert.Eq(t, want, strings.Join(renderSegmentParts(c), "\n\n"))
}
