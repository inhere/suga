package tools

import "github.com/inhere/suga/pkg/agentcore/api"

// Category is a protocol-neutral class of what a tool does. Hosts map it onto
// their own vocabulary (e.g. ACP tool kinds) and approval prompts.
type Category string

const (
	CategoryRead Category = "read"
	CategoryEdit Category = "edit"
	// CategoryDelete removes files (file_remove).
	CategoryDelete  Category = "delete"
	CategoryExecute Category = "execute"
	CategorySearch  Category = "search"
	CategoryFetch   Category = "fetch"
	CategoryThink   Category = "think"
	// CategoryOther is the default for tools that declare nothing.
	CategoryOther Category = "other"
)

// Meta describes a tool for permission decisions.
type Meta struct {
	Category Category
	// ReadOnly marks tools without side effects outside the session (reading
	// files, searching, fetching, updating the plan): approval layers may run
	// them without asking, and read-only tool sets keep only them.
	ReadOnly bool
	// External marks tools whose results come from outside the workspace
	// (web pages, search results): the session is flagged as tainted once such
	// a result enters the context.
	External bool
	// Rereadable marks tools that paginate on their own (the model can re-read
	// the rest by calling again): output wrapping layers neither truncate nor
	// externalise their results.
	Rereadable bool
	// AgentState marks tools that only change the agent's own state (e.g.
	// memory). Approval layers let them through without asking; the tool
	// itself decides whether a write is allowed in the current run mode
	// (e.g. read-only). Never honoured for MCP tools.
	AgentState bool
	// Prefetchable marks tools that only read local files and keep no state,
	// so a reply's calls to them may run ahead concurrently (Registry.Prefetch).
	// Never honoured for MCP tools.
	Prefetchable bool
	// Hidden marks a tool that stays registered and callable by name but is not
	// listed in the tool table (ListTools), the manifest or tool_search: an alias
	// of another tool kept for one version, so old sessions and name-based
	// configuration keep working (view_image, design 2026-10-23 Q4).
	Hidden bool
}

// Describer is the optional interface a tool implements to declare its Meta.
type Describer interface {
	ToolMeta() Meta
}

// MetaOf returns t's declared Meta. A tool without Describer, a nil tool and
// every MCP tool get {CategoryOther, ReadOnly: false, AgentState: false,
// Rereadable: false}; MCP results are always External. MCP tools are never
// trusted as read-only, agent-state or rereadable by name or declaration, so
// an MCP tool cannot pose as a built-in to bypass approval.
func MetaOf(t api.Tool) Meta {
	if t == nil || IsMCP(t) {
		return Meta{Category: CategoryOther, External: t != nil}
	}
	if d, ok := t.(Describer); ok {
		meta := d.ToolMeta()
		if meta.Category == "" {
			meta.Category = CategoryOther
		}
		return meta
	}
	return Meta{Category: CategoryOther}
}

// IsMCP reports whether t is backed by an MCP server.
func IsMCP(t api.Tool) bool { return t != nil && KindOf(t) == KindMCP }

// MCPServerOf returns the configured server name of an MCP tool, or "".
func MCPServerOf(t api.Tool) string {
	if named, ok := t.(MCPServerNamer); ok {
		return named.MCPServer()
	}
	return ""
}
