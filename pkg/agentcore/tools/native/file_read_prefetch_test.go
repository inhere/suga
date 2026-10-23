package native

import (
	"testing"

	"github.com/gookit/goutil/x/assert"

	"github.com/inhere/suga/pkg/agentcore/tools"
)

// TestFileReadSkipPrefetch pins the prefetch exemption (design 2026-10-23
// section 7): a call that names a picture or a session image ref is not run
// ahead, every other call of the same tool still is.
func TestFileReadSkipPrefetch(t *testing.T) {
	tool := (&FileReadTool{workspace: "."}).SetImagePipeline(NewViewImageTool(".", nil, nil, ""))
	cases := []struct {
		name string
		args map[string]any
		want bool
	}{
		{"a picture path", map[string]any{"path": "shot.png"}, true},
		{"a picture in paths", map[string]any{"paths": []any{"a.go", "b.webp"}}, true},
		{"a session image ref", map[string]any{"path": "images/abc123.png"}, true},
		{"a line span of a picture", map[string]any{"path": "shot.png:10-20"}, true},
		{"an upper case extension", map[string]any{"path": "SHOT.PNG"}, true},
		{"a text file", map[string]any{"path": "a.go"}, false},
		{"a picture without its extension", map[string]any{"path": "shot"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Eq(t, c.want, tool.SkipPrefetch(c.args))
		})
	}
	// The tool itself stays Prefetchable: only single calls are exempt.
	assert.True(t, tools.MetaOf(tool).Prefetchable)
}
