package tools

import "fmt"

// ApprovalMode is the approval tier an entry point starts with. It only ever
// comes from a start-up flag or an SDK option, never from a config file.
type ApprovalMode string

const (
	// ApprovalModeDefault ("") keeps each entry point's own behaviour.
	ApprovalModeDefault ApprovalMode = ""
	// ApprovalModeAsk asks for every call the policy does not allow.
	ApprovalModeAsk ApprovalMode = "ask"
	// ApprovalModeAuto allows native calls without asking.
	ApprovalModeAuto ApprovalMode = "auto"
	// ApprovalModeTrust is auto plus letting unprotected asks through.
	ApprovalModeTrust ApprovalMode = "trust"
)

// ParseApprovalMode validates s: "", "ask", "auto" or "trust" (exact, no
// trimming or case folding, so a typo cannot silently pick a tier).
func ParseApprovalMode(s string) (ApprovalMode, error) {
	switch mode := ApprovalMode(s); mode {
	case ApprovalModeDefault, ApprovalModeAsk, ApprovalModeAuto, ApprovalModeTrust:
		return mode, nil
	}
	return ApprovalModeDefault, fmt.Errorf("invalid approval mode %q: want ask, auto or trust", s)
}
