package chat

import "github.com/inhere/suga/pkg/chatui"

// The waiting line lives in pkg/chatui (spinner.go). These names keep chat's own
// code short.

type thinkingSpinner = chatui.Spinner

const (
	spinnerSpeed = chatui.SpinnerSpeed
	spinnerClear = chatui.SpinnerClear
)

var (
	spinnerChars = chatui.SpinnerChars
	toolLabel    = chatui.ToolLabel
)
