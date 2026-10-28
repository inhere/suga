package native

import (
	"context"
	"time"

	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/netproxy"
	"github.com/inhere/suga/pkg/agentcore/tools"
)

// Env carries the settings native tools are built with. Zero values fall back
// to each tool's defaults.
type Env struct {
	// Workspace confines file tools and is the shell working directory.
	Workspace string
	// TrashDir is where file_remove moves removed paths (kept TrashRetention);
	// empty leaves file_remove unavailable.
	TrashDir string
	// ReadRoots are directories outside the workspace the read-only file tools
	// may read too (dependency sources, tools.read_roots). Writes never go there.
	ReadRoots []string
	// AddDirs are the additional directories of the session being served
	// (--add-dir): the tools read the set at every call, because the runtime is
	// assembled before the session is opened. nil = no additional directory.
	AddDirs *AddDirs
	// Snapshots receives before/after notifications for file-writing tools.
	// nil disables snapshotting.
	Snapshots Snapshotter
	// InlineBytes is the inline threshold (tools.max_output_bytes). Tools
	// leave the inline cut to the output layer (tools.NewOutput); file_read
	// pages by itself with ReadPageBytes instead.
	InlineBytes int
	// ReadPageBytes caps one file_read page (tools.read_page_bytes);
	// 0 = DefaultReadPageBytes.
	ReadPageBytes int
	// RawBytes caps the raw result of every other tool (tools.max_spill_bytes),
	// i.e. how much the output layer may save to the session directory.
	RawBytes int
	// SerpAPIKey enables web_search; empty leaves it unavailable.
	SerpAPIKey string
	// Shell overrides the shell_exec shell (tools.shell); empty = OS default.
	Shell string
	// Interpreters are the script interpreters detected on PATH
	// (tools.interpreters, see DetectInterpreters); empty = none, no hint.
	Interpreters []Interpreter
	// ShellTimeoutMax caps a shell_exec timeout (tools.shell_timeout_max).
	ShellTimeoutMax time.Duration
	// TaskTimeoutMax caps a background shell task (tools.task_timeout_max).
	TaskTimeoutMax time.Duration
	// ShellBackgroundAfter is the foreground grace period before a shell is
	// transferred to the task manager; zero disables transfer.
	ShellBackgroundAfter time.Duration
	// ShellLimits caps the memory, process count and CPU time of a command and
	// its process tree (tools.shell_limits); the zero value is no limit.
	ShellLimits ShellLimits
	// FetchAllowPrivate lets web_fetch reach private and local addresses
	// (tools.web_fetch.allow_private).
	FetchAllowPrivate bool
	// FetchProxy routes web_fetch through a proxy (tools.web_fetch.proxy);
	// wiring validates it at startup with ParseFetchProxy. It only applies when
	// NetworkProxy is nil.
	FetchProxy string
	// NetworkProxy is the global network.proxy selector used by web_fetch and
	// web_search; nil keeps their previous behavior (web_fetch ignores the
	// environment proxies, web_search follows them).
	NetworkProxy *netproxy.Selector
	// FetchUserAgent is the User-Agent web_fetch sends
	// (tools.web_fetch.user_agent); empty keeps the tool's default.
	FetchUserAgent string
	// ImagesDir is the session's images directory (view_image accepts a ref to
	// one of its files); empty = only ordinary paths.
	ImagesDir string
	// Vision is the live capability of the current model (nil = can see):
	// view_image explains how to switch instead of reading a picture nobody can
	// see. The host updates the gate on a model switch.
	Vision *tools.VisionGate
	// ImageMaxSide is the long side view_image scales a picture to when the call
	// names none (images.max_side); 0 = DefaultImageMaxSide.
	ImageMaxSide int
	// ExtraEnv adds the variables suga exports to a subprocess (the session, its
	// short name and the turn); the tools and chat's local commands call it at
	// every run, so it may still be nil or return nothing when they are built.
	ExtraEnv func(context.Context) []string
	// SessionMessenger is the host side of the session_message tool (the running
	// chat sessions of this SUGA_HOME); nil keeps the tool out of the catalog
	// (tools.session_message.enabled, SUGA_SESSION_MESSAGE=off).
	SessionMessenger SessionMessenger
	// TurnID names the turn ctx belongs to ("" outside a turn): the per-turn send
	// limit of session_message resets with every turn.
	TurnID func(context.Context) string
}

// Spec is one entry of the native tool catalog. Whether a tool writes is its
// own tools.Meta (ToolMeta), not a catalog flag.
type Spec struct {
	Name string
	// New builds the tool, or returns nil when env lacks what it needs
	// (web_search without a key).
	New func(env Env) api.Tool
}

// Catalog lists every native tool. run, chat and ACP
// all register from this one list; update_plan and tool_search are wired
// separately because they depend on session state, not on Env.
func Catalog() []Spec {
	return []Spec{
		{Name: "file_read", New: func(env Env) api.Tool {
			t := &FileReadTool{workspace: env.Workspace, readRoots: env.ReadRoots, addDirs: env.AddDirs, maxOut: env.ReadPageBytes}
			// file_read reads pictures with the same pipeline as view_image (design
			// 2026-10-23-file-read-images); the host rebinds this instance to the
			// runtime's live vision gate and session images directory.
			return t.SetImagePipeline(newImageViewTool(env))
		}},
		{Name: "view_image", New: func(env Env) api.Tool {
			return newImageViewTool(env)
		}},
		{Name: "file_list", New: func(env Env) api.Tool {
			return &FileListTool{workspace: env.Workspace, readRoots: env.ReadRoots, addDirs: env.AddDirs, maxOut: env.RawBytes}
		}},
		{Name: "file_grep", New: func(env Env) api.Tool {
			return &FileGrepTool{workspace: env.Workspace, readRoots: env.ReadRoots, addDirs: env.AddDirs, maxOut: env.RawBytes}
		}},
		{Name: "file_find", New: func(env Env) api.Tool {
			return &FileFindTool{workspace: env.Workspace, readRoots: env.ReadRoots, addDirs: env.AddDirs, maxOut: env.RawBytes}
		}},
		{Name: "file_symbols", New: func(env Env) api.Tool {
			return &FileSymbolsTool{workspace: env.Workspace, readRoots: env.ReadRoots, addDirs: env.AddDirs, maxOut: env.RawBytes}
		}},
		{Name: "file_refs", New: func(env Env) api.Tool {
			return &FileRefsTool{workspace: env.Workspace, readRoots: env.ReadRoots, addDirs: env.AddDirs, maxOut: env.RawBytes}
		}},
		{Name: "web_fetch", New: func(env Env) api.Tool {
			t, err := NewWebFetchToolWithSelector(env.FetchAllowPrivate, env.FetchProxy, env.NetworkProxy)
			if err != nil {
				return nil // invalid proxy: rejected earlier by wiring
			}
			t.maxOut = env.RawBytes
			t.userAgent = env.FetchUserAgent
			return t
		}},
		{Name: "file_write", New: func(env Env) api.Tool {
			return &FileWriteTool{workspace: env.Workspace, addDirs: env.AddDirs, snapshots: env.Snapshots}
		}},
		{Name: "file_edit", New: func(env Env) api.Tool {
			return &FileEditTool{workspace: env.Workspace, addDirs: env.AddDirs, snapshots: env.Snapshots}
		}},
		{Name: "file_remove", New: func(env Env) api.Tool {
			if env.TrashDir == "" {
				return nil
			}
			t := NewFileRemoveTool(env.Workspace, env.TrashDir)
			t.addDirs = env.AddDirs
			t.snapshots = env.Snapshots
			return t
		}},
		{Name: "shell_exec", New: func(env Env) api.Tool {
			t := NewShellExecTool(env.Workspace, 0)
			t.shell = resolveShell(env.Shell)
			t.interpreters = env.Interpreters
			if env.ShellBackgroundAfter >= 0 {
				t.backgroundAfter = env.ShellBackgroundAfter
			}
			t.maxOut = env.RawBytes
			if env.ShellTimeoutMax > 0 {
				t.maxTimeout = env.ShellTimeoutMax
				t.timeout = min(t.timeout, t.maxTimeout)
			}
			if env.TaskTimeoutMax > 0 {
				t.taskTimeoutMax = env.TaskTimeoutMax
			}
			t.taskTimeoutMax = max(t.taskTimeoutMax, t.maxTimeout)
			t.limits = env.ShellLimits
			t.extraEnv = env.ExtraEnv
			return t
		}},
		{Name: "web_search", New: func(env Env) api.Tool {
			if env.SerpAPIKey == "" {
				return nil
			}
			t := NewWebSearchTool(env.SerpAPIKey)
			t.SetNetworkProxy(env.NetworkProxy)
			t.maxOut = env.RawBytes
			return t
		}},
		{Name: sessionMessageToolName, New: func(env Env) api.Tool {
			if env.SessionMessenger == nil {
				return nil
			}
			return &SessionMessageTool{messenger: env.SessionMessenger, turnID: env.TurnID}
		}},
	}
}

// newImageViewTool builds the picture reader from env: view_image wraps it as a
// tool, file_read reads pictures through it.
func newImageViewTool(env Env) *ViewImageTool {
	return &ViewImageTool{workspace: env.Workspace, readRoots: env.ReadRoots, addDirs: env.AddDirs,
		imagesDir: env.ImagesDir, vision: env.Vision, maxSide: env.ImageMaxSide}
}

// BuildTools returns the catalog tools available in env, in catalog order.
// Tools that are not read-only (tools.MetaOf) are skipped unless allowWrite
// is set.
func BuildTools(env Env, allowWrite bool) []api.Tool {
	var out []api.Tool
	for _, spec := range Catalog() {
		t := spec.New(env)
		if t == nil || (!allowWrite && !tools.MetaOf(t).ReadOnly) {
			continue
		}
		out = append(out, t)
	}
	return out
}
