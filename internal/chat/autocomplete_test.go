package chat

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/chatui/lineedit"
)

func TestChatCompletion(t *testing.T) {
	t.Run("unique and multiple commands", func(t *testing.T) {
		source := &chatInputSource{output: &bytes.Buffer{}}
		line, pos, ok := completeCandidates(source, ":se", []string{":session"})
		assert.True(t, ok)
		assert.Eq(t, ":session", line)
		assert.Eq(t, len(line), pos)
		line, pos, ok = completeCandidates(source, ":", []string{":session", ":compact"})
		assert.True(t, ok)
		assert.Eq(t, ":", line)
		assert.Eq(t, 1, pos)
		line, pos, ok = completeCandidates(source, ":model f", []string{":model fake/m1", ":model fake/m2"})
		assert.True(t, ok)
		assert.Eq(t, ":model f", line)
		assert.Eq(t, len(line), pos)
	})

	t.Run("directory with spaces", func(t *testing.T) {
		dir := t.TempDir()
		child := filepath.Join(dir, "folder with space")
		assert.NoErr(t, os.Mkdir(child, 0o700))
		source := &chatInputSource{output: &bytes.Buffer{}}
		line, pos, ok := completePath(source, dir, ":add-dir "+dir+string(os.PathSeparator)+"fol", ":add-dir", dir+string(os.PathSeparator)+"fol")
		assert.True(t, ok)
		assert.Eq(t, ":add-dir "+child+string(os.PathSeparator), line)
		assert.Eq(t, len(line), pos)
	})

	t.Run("relative directory is resolved from workspace", func(t *testing.T) {
		workspace := t.TempDir()
		child := filepath.Join(workspace, "tm-dir")
		assert.NoErr(t, os.Mkdir(child, 0o700))
		source := &chatInputSource{output: &bytes.Buffer{}}
		line, _, ok := completePath(source, workspace, ":add-dir tm", ":add-dir", "tm")
		assert.True(t, ok)
		assert.Eq(t, ":add-dir "+child+string(os.PathSeparator), line)
	})
}

func TestChatCompletionIsInstalledOnTerminalSource(t *testing.T) {
	source := &chatInputSource{output: &bytes.Buffer{}}
	source.terminal = lineedit.NewTerminal(&bytes.Buffer{}, "❯ ")
	installCompletion(source, nil, []string{"fake/m1", "fake/m2"})
	line, pos, ok := source.terminal.AutoCompleteCallback(":se", len(":se"), '\t')
	assert.True(t, ok)
	assert.Eq(t, ":session", line)
	assert.Eq(t, len(line), pos)
	line, pos, ok = source.terminal.AutoCompleteCallback(":model f", len(":model f"), '\t')
	assert.True(t, ok)
	assert.Eq(t, ":model f", line)
	assert.Eq(t, len(line), pos)
	line, pos, ok = source.terminal.AutoCompleteCallback(":check", len(":check"), '\t')
	assert.True(t, ok)
	assert.Eq(t, ":checkpoints", line)
	assert.Eq(t, len(line), pos)
}
