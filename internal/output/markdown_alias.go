package output

import "github.com/inhere/suga/pkg/chatui"

// Markdown types are kept as aliases for the non-chat output callers while the
// reusable implementation lives in pkg/chatui.
type MDTerm = chatui.MDTerm
type CodeBlock = chatui.CodeBlock

var NewMDTerm = chatui.NewMDTerm
