//go:build ptye2e

package ptye2e

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const codeLines = 45

// TestChatRender ports scripts/ptysmoke/chat_render.py: GFM table alignment,
// code highlighting while streaming, the fold of a long code block and :code.
// The fake model answers with a Chinese table and a 45 line go block; the checks
// fail when the table buffering, the fold limit or the highlighting is gone.
func TestChatRender(t *testing.T) {
	binary := buildSuga(t)
	var body strings.Builder
	for i := 0; i < codeLines; i++ {
		fmt.Fprintf(&body, "    fmt.Println(\"step %d\")\n", i+1)
	}
	reply := "回答如下：\n\n| 名称 | 状态 |\n| --- | --- |\n| 构建 | 通过 |\n| 测试 | 通过 |\n\n```go\nfunc demo() {\n" +
		body.String() + "}\n```\n\n完成。\n"
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 200 * time.Millisecond, Reply: reply})
	defer server.Close()
	// A colored terminal: NO_COLOR is not set, so chat renders markdown, colors
	// the code and aligns the table.
	s, _ := startChatOpts(t, binary, chatOpts{Home: t.TempDir(), Work: t.TempDir(), URL: server.URL, Color: true})

	mark := s.Mark()
	expect(t, s, `Suga chat · fake/m1`, mark)
	rawMark := len(s.RawText())
	s.SendLine("渲染一下")
	// The table is aligned by display width (CJK counts two columns).
	expect(t, s, `│ 名称 │ 状态 │`, mark)
	expect(t, s, `│ 构建 │ 通过 │`, mark)
	expect(t, s, `─{10,}`, mark)
	// A go block is colored while it streams: keywords cyan, strings green (16
	// color ANSI); Text strips escapes, so this reads the raw output.
	expectRaw(t, s, `\x1b\[36m(?:\x1b\[K)?(?:\r\n)?func`, rawMark)
	expectRaw(t, s, `\x1b\[32m"step 1"`, rawMark)
	// Past chat.code_max_lines the block folds and names :code.
	expect(t, s, fmt.Sprintf(`… 还有 %d 行（:code 查看全部）`, codeLines+2-40), mark)
	check(t, "the folded line is not printed", !s.Contains(fmt.Sprintf(`step %d`, codeLines), mark))
	expect(t, s, `tokens · `, mark)

	// :code shows the block it folded whole, again highlighted.
	mark = s.Mark()
	s.SendLine(":code")
	expect(t, s, fmt.Sprintf(`第 1 个代码块（go，共 %d 行）`, codeLines+2), mark)
	expect(t, s, fmt.Sprintf(`step %d`, codeLines), mark)
	// A block that does not exist only hints.
	mark = s.Mark()
	s.SendLine(":code 9")
	expect(t, s, `上一条回答只有 1 个代码块`, mark)
	ctrlCTwice(t, s)
}

// The SGR a heading, a keyword and an inline code span get. ConPTY re-encodes the
// stream: bold cyan arrives as "ESC[36m ESC[1m" instead of "ESC[1;36m", and a color
// set before a line break may come before the CR LF, so the patterns accept both.
const (
	boldCyanHeading = `\x1b\[(?:1;36m|36m\x1b\[1m)主题测试`
	boldBlueHeading = `\x1b\[(?:1;34m|34m\x1b\[1m)主题测试`
	cyanKeyword     = `\x1b\[36m(?:\x1b\[K)?(?:\r\n)?func`
	blueKeyword     = `\x1b\[34m(?:\x1b\[K)?(?:\r\n)?func`
	// The other theme's heading or inline code must not appear.
	darkPalette  = boldCyanHeading + `|\x1b\[93mmake smoke`
	lightPalette = boldBlueHeading + `|\x1b\[35mmake smoke`
)

// TestChatTheme ports scripts/ptysmoke/chat_theme.py (suga-exbd): run A (theme
// auto, no COLORFGBG: dark) and run B (SUGA_THEME=light) paint a heading, an
// inline code span and a go keyword with their own palette. The raw SGR is read
// from RawText because Text strips escape codes.
func TestChatTheme(t *testing.T) {
	binary := buildSuga(t)
	reply := "# 主题测试\n\n运行 `make smoke`。\n\n```go\nfunc demo() {\n}\n```\n"
	server := httptest.NewServer(&fakeLLM{Mode: "text", Delay: 200 * time.Millisecond, Reply: reply})
	defer server.Close()
	home := t.TempDir()

	run := func(t *testing.T, setEnv []string, prompt string, want [3]string, name [3]string, absent string) {
		s, _ := startChatOpts(t, binary, chatOpts{
			Home: home, Work: t.TempDir(), URL: server.URL, Color: true,
			DropEnv: []string{"COLORFGBG", "SUGA_THEME"}, SetEnv: setEnv,
		})
		mark := s.Mark()
		expect(t, s, `Suga chat · fake/m1`, mark)
		rawMark := len(s.RawText())
		s.SendLine(prompt)
		for i, pattern := range want {
			if !rawContains(s, pattern, rawMark, 15*time.Second) {
				t.Errorf("FAIL %s; raw tail: %q", name[i], tail(s.RawText()))
			}
		}
		check(t, "other palette is absent", !regexpMatch(absent, s.RawText()[rawMark:]))
		ctrlCTwice(t, s)
	}
	t.Run("dark", func(t *testing.T) {
		run(t, nil, "渲染一下",
			[3]string{boldCyanHeading, `\x1b\[93mmake smoke`, cyanKeyword},
			[3]string{"dark heading keeps bold cyan", "dark inline code keeps bright yellow", "dark keyword keeps cyan"},
			lightPalette)
	})
	t.Run("light", func(t *testing.T) {
		run(t, []string{"SUGA_THEME=light"}, "再渲染一下",
			[3]string{boldBlueHeading, `\x1b\[35mmake smoke`, blueKeyword},
			[3]string{"light heading is bold blue", "light inline code is magenta", "light keyword is blue"},
			darkPalette)
	})
}
