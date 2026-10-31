// Package chatui contains the reusable terminal chat controls used by suga and
// other clients. It deliberately depends only on public packages so hosts do not
// need access to suga's internal application layers.
//
// # Components
//
// Streaming text and markdown: LiveText (a model message's streamed text, fed with
// StreamEvent values), MDTerm (line-by-line markdown rendering), CodeBlock.
//
// Approval questions: ApprovalPrompt, PersistPrompt and the matching
// ParseApprovalReply / ParsePersistReply for the one-line answers.
//
// Tool lines: a host describes one tool call or result as a ToolEvent and prints
// it with ToolCallLine / ToolResultLine. ToolCallArgs is the argument summary on
// its own. Arguments and results pass through redaction, paths are made relative
// to the workdir argument (empty keeps them absolute) and the line is cut to
// CallArgsLimit / ResultSummaryLimit. ToolFamilyOf maps a tool name to its
// ToolFamily, which decides the symbol and color.
//
// Palette: NewPalette(theme, settings) resolves the chat.colors block (settings
// keys: ColorsKeyMode, ColorsKeyStyle and one key per element or family name) into
// SGR styles. It is built once per chat so the terminal's color depth is asked
// once; WithColor(false) returns a plain copy, and an unknown key or color is an
// error. Pass the palette to ToolCallLine / ToolResultLine and use Paint /
// PaintDim / OKInk / ErrInk / DimInk for the host's own lines.
//
// Waiting line: NewSpinner(SpinnerConfig) shows "thinking" while the host waits
// for the model. With a StatusSetter (the *lineedit.Terminal itself has
// SetStatus) the line lives on the status row above the input line; with nil
// Status it owns a line of Out. Start / Stop bracket the wait, SetLabel switches
// the text (ToolLabel(t, name) is the label for a running tool), Suspend /
// Resume hand the screen to something else for a while. Enabled=false makes every
// method a no-op. Now and Ticks are injection points for tests.
//
// Translator: every text the package writes goes through the host's Translator
// (T(key, args...)); nil selects the built-in Chinese text, so a host without a
// catalog needs nothing.
//
// # Input
//
// Package lineedit is the line editor and package lineedit/pump the byte pump it
// reads through; see the pump package documentation.
//
// # Minimal host
//
// Example_minimalHost (example_test.go) is the smallest wiring for a host with no
// steering (xhen style): pump.DefaultHooks + pump.NewPump + Bind + pump.WatchSize
// for input, ToolCallLine for a tool call and NewSpinner for the wait. The longer
// description lives in docs/usage/chatui.md.
package chatui
