package personal

import (
	"encoding/json"
	"strings"

	"github.com/inhere/suga/pkg/agentcore"
	"golang.org/x/text/width"
)

// Token estimation for the context window.
//
// The model sees bytes, and a tokenizer treats its scripts very differently: four
// bytes of ASCII text are about one token, while one CJK character (three UTF-8
// bytes) is a token of its own in the common tokenizers. Counting bytes/4 therefore
// reads a Chinese-heavy session 15-20% low - measured on a real session: a segment
// record said 440734 tokens where the provider reported 522760 input tokens for the
// same request (and after the break, 5177 against 11376 reported). Everything that
// estimates tokens from messages uses tokensOfMessages; charsOfMessages stays for
// the figures that really mean characters (the context event attrs, history folding).
const (
	// narrowQuarters is what one byte of narrow text costs: a quarter token, i.e.
	// four characters per token, which is still the right average for ASCII.
	narrowQuarters = 1
	// wideQuarters is what a wide rune (CJK, fullwidth forms) costs: a token of its
	// own, counted as four quarters so the sum stays in quarter tokens.
	wideQuarters = 4
)

// tokensOfText estimates the tokens of s: wide runes count one token each and
// narrow bytes a quarter token each. The sum is kept in quarter tokens and rounded
// up, so a short text never estimates as zero.
func tokensOfText(s string) int {
	if s == "" {
		return 0
	}
	quarters := 0
	for _, r := range s {
		if wideRune(r) {
			quarters += wideQuarters
			continue
		}
		quarters += narrowQuarters
	}
	return (quarters + 3) / 4
}

// wideRune reports whether r takes a token of its own in the common tokenizers:
// East Asian wide and fullwidth runes (CJK text and punctuation, most emoji).
// East Asian ambiguous runes count as narrow, like they do on screen
// (lineedit.runeWidth).
func wideRune(r rune) bool {
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return true
	}
	return false
}

// tokensOfMessages estimates what the model sees of ms.
func tokensOfMessages(ms []agentcore.Message) int {
	n := 0
	for _, m := range ms {
		n += tokensOfMessage(m)
	}
	return n
}

// tokensOfMessage mirrors charsOfMessage: content and reasoning (both occupy the
// window), the tool result counted once, and a tool call's name and arguments.
func tokensOfMessage(m agentcore.Message) int {
	n := tokensOfText(m.Content) + tokensOfText(m.Reasoning)
	if m.ToolResult != nil {
		if t := tokensOfText(m.ToolResult.Content); t > n {
			n = t
		}
	}
	if m.ToolCall != nil && m.Role != "tool" {
		n += tokensOfText(m.ToolCall.Name)
		if raw, err := json.Marshal(m.ToolCall.Arguments); err == nil {
			n += tokensOfText(string(raw))
		}
	}
	n += tokensOfImages(m.Images)
	if m.ToolResult != nil {
		n += tokensOfImages(m.ToolResult.Images)
	}
	return n
}

// tokensOfImages estimates the tokens of a message's images the way the
// providers do: roughly width×height/750, i.e. a 1568×1568 picture (the long
// side the image tools scale to) costs about a thousand tokens.
func tokensOfImages(images []agentcore.ImagePart) int {
	if len(images) == 0 {
		return 0
	}
	n := 0
	for _, part := range images {
		if part.Width > 0 && part.Height > 0 {
			n += (part.Width*part.Height + 749) / 750
			continue
		}
		// Unknown pixel size: charge the bytes as text instead of nothing.
		n += tokensOfText(strings.Repeat("x", len(part.Data)))
	}
	return n
}

// tokensOfTools estimates the JSON schema sent with a request. Encoding the
// complete provider-neutral schema keeps descriptions and nested properties in
// the same approximation as the request body without introducing a tokenizer.
func tokensOfTools(tools []agentcore.ToolSchema) int {
	if len(tools) == 0 {
		return 0
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return 0
	}
	return tokensOfText(string(raw))
}

// tokensOfToolCalls estimates the visible output of tool calls, excluding the
// call id because the model output contains the function name and arguments.
func tokensOfToolCalls(calls []agentcore.ToolCall) int {
	n := 0
	for _, call := range calls {
		n += tokensOfText(call.Name)
		if raw, err := json.Marshal(call.Arguments); err == nil {
			n += tokensOfText(string(raw))
		}
	}
	return n
}
