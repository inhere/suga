package mcp

import "github.com/inhere/suga/pkg/agentcore/api"

// Annotations is the optional "annotations" object of one tools/list entry
// (MCP spec: title, readOnlyHint, destructiveHint, idempotentHint,
// openWorldHint). Every hint is a pointer so "declared false" stays apart from
// "not declared": a server that says nothing is not read as "not destructive"
// and never as read-only.
type Annotations struct {
	Title string `json:"title,omitempty"`
	// ReadOnlyHint marks a tool the server claims has no side effects.
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`
	// DestructiveHint marks a tool the server claims may destroy data.
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	// IdempotentHint marks a repeated call with the same arguments as harmless.
	IdempotentHint *bool `json:"idempotentHint,omitempty"`
	// OpenWorldHint marks interaction with an open world (the internet).
	OpenWorldHint *bool `json:"openWorldHint,omitempty"`
}

// ReadOnly reports the server's readOnlyHint == true claim. It is intent
// declared by an external server: a host may report it (ACP announces
// kind=read), but suga itself never approves a call because of it.
func (a Annotations) ReadOnly() bool { return a.ReadOnlyHint != nil && *a.ReadOnlyHint }

// Destructive reports the server's destructiveHint == true claim.
func (a Annotations) Destructive() bool {
	return a.DestructiveHint != nil && *a.DestructiveHint
}

// Declared reports whether the server declared at least one annotation field.
func (a Annotations) Declared() bool {
	return a.Title != "" || a.ReadOnlyHint != nil || a.DestructiveHint != nil ||
		a.IdempotentHint != nil || a.OpenWorldHint != nil
}

// SafeReadOnly reports a tool the server declares read-only and not
// destructive; a destructive read-only claim is contradictory and ignored.
func (a Annotations) SafeReadOnly() bool { return a.ReadOnly() && !a.Destructive() }

// Annotator is the optional interface of a tool that carries the annotations
// its MCP server declared in tools/list. Only MCP tool adapters implement it.
type Annotator interface {
	MCPAnnotations() Annotations
}

// AnnotationsOf returns the annotations an MCP tool's server declared, and
// whether the tool carries any at all. Non-MCP tools, nil tools and MCP tools
// whose server declared nothing report false, so callers keep their default
// classification.
func AnnotationsOf(t api.Tool) (Annotations, bool) {
	annotator, ok := t.(Annotator)
	if !ok {
		return Annotations{}, false
	}
	annotations := annotator.MCPAnnotations()
	return annotations, annotations.Declared()
}
