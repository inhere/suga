package tools

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// DefaultNativeDeferredTools is the built-in deferred set of
// tools.deferred_native: auto (design 2026-10-23 section 2.1). The list is
// chosen by call frequency, not schema size: these tools cost ~13.4k characters
// of every request and are used a handful of times per session at most.
var DefaultNativeDeferredTools = []string{
	"agent",
	"file_symbols",
	"memory",
	"recall",
	"session_search",
	"session_message",
	"skill",
	"suga_manifest",
	"web_fetch",
}

// nativeToolPurposes is the one-line usage note of a default deferred tool. It
// is what the segment context listing shows the model (design Q6): the risk of
// deferring memory/recall/session_search is that the model does not know they
// exist, so every line says what the tool is for.
var nativeToolPurposes = map[string]string{
	"agent":           "交给子 agent 做只读探索/研究，省上下文",
	"file_symbols":    "按文件列出声明（函数/类型/方法）轮廓，替代整文件读完",
	"memory":          "写入或查询长期记忆（跨会话，长会话里要记住结论时用）",
	"recall":          "展开工具输出里被省略的大段内容（out: 引用），找回早前的大输出",
	"session_search":  "检索历史会话的消息，长会话里找回前文说过的话",
	"session_message": "给同机上的其它 chat 会话发消息并取回复",
	"skill":           "按名字加载 skills 指令",
	"suga_manifest":   "查看当前配置、工具、记忆与审批快照",
	"web_fetch":       "抓取网页正文",
}

// knownNativeToolNames are the native tool names a deferred_native list may
// name. Only the names matter here: whether a tool exists at runtime depends on
// the mode (read-only, skills/agents enabled), which must not turn a
// configuration into a startup error (design D7).
var knownNativeToolNames = map[string]bool{
	"agent": true, "file_find": true, "file_grep": true, "file_edit": true,
	"file_list": true, "file_read": true, "file_refs": true, "file_remove": true,
	"file_symbols": true, "file_write": true, "memory": true, "recall": true,
	"session_message": true, "session_search": true, "shell_exec": true,
	"skill": true, "suga_manifest": true, "task": true, "tool_search": true,
	"update_plan": true, "view_image": true, "web_fetch": true, "web_search": true,
}

// ToolCallName is the always-registered proxy tool through which the model
// calls a deferred native tool that is loaded but not in the tool table yet
// (tools.deferred_apply: segment). The provider probe of 2026-10-23 showed that
// models do not call tools missing from the request's tools field, so the
// segment mode needs this proxy (design Q9).
const ToolCallName = "tool_call"

// DeferredApplyMode is the tools.deferred_apply setting: when a loaded native
// tool joins the tool table. MCP tools keep the immediate behaviour.
type DeferredApplyMode string

const (
	// DeferredApplySegment folds loaded tools into the table at a segment
	// boundary only, so the tool prefix stays byte-identical within a segment.
	DeferredApplySegment DeferredApplyMode = "segment"
	// DeferredApplyImmediate lists a tool as soon as it is loaded.
	DeferredApplyImmediate DeferredApplyMode = "immediate"
)

// ParseDeferredApplyMode validates a tools.deferred_apply value; empty means
// segment (the default of the setting).
func ParseDeferredApplyMode(s string) (DeferredApplyMode, error) {
	switch m := DeferredApplyMode(strings.ToLower(strings.TrimSpace(s))); m {
	case "":
		return DeferredApplySegment, nil
	case DeferredApplySegment, DeferredApplyImmediate:
		return m, nil
	}
	return "", fmt.Errorf("tools.deferred_apply must be segment or immediate, got %q", s)
}

// ParseNativeDeferred parses a tools.deferred_native value: unset or "never"
// defers no native tool, "auto" selects DefaultNativeDeferredTools, and a YAML
// list (or a single name) is taken as the explicit set.
func ParseNativeDeferred(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		return parseNativeDeferredKeyword(v)
	case []string:
		return normalizeNativeNames(v)
	case []any:
		names := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("tools.deferred_native: list item %v is not a tool name", item)
			}
			names = append(names, s)
		}
		return normalizeNativeNames(names)
	default:
		return parseNativeDeferredKeyword(fmt.Sprint(v))
	}
}

func parseNativeDeferredKeyword(raw string) ([]string, error) {
	switch s := strings.ToLower(strings.TrimSpace(raw)); s {
	case "", "never":
		return nil, nil
	case "auto":
		return append([]string(nil), DefaultNativeDeferredTools...), nil
	default:
		return normalizeNativeNames([]string{raw})
	}
}

// normalizeNativeNames trims a configured list, drops duplicates and validates
// that every entry names a native tool (an MCP tool or a typo would silently
// never be deferred, which is worse than a startup error).
func normalizeNativeNames(names []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !knownNativeToolNames[n] {
			return nil, fmt.Errorf("tools.deferred_native: %q is not a native tool name", n)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// NativePurpose returns the listing line text of a deferred native tool: the
// built-in purpose for the default list, else a shortened schema description.
func NativePurpose(name, description string) string {
	if p, ok := nativeToolPurposes[name]; ok {
		return p
	}
	if strings.TrimSpace(description) == "" {
		return name
	}
	return truncateDescription(description, maxPromptDescription)
}

// warnIgnoredNativeNames logs the names of a configured list that cannot be
// deferred at runtime: not registered now (read-only mode, skill/agents off) is
// debug, tool_search/a hidden tool/a host extra tool is a warning.
func warnIgnoredNativeNames(schemas map[string]bool, hidden map[string]bool, deferred map[string]bool, names []string) {
	for _, n := range names {
		if deferred[n] {
			continue
		}
		switch {
		case !schemas[n]:
			slog.Debug("deferred native tool is not registered now", "tool", n)
		case hidden[n]:
			slog.Warn("not deferring a hidden native tool", "tool", n)
		default:
			slog.Warn("not deferring the reserved native tool", "tool", n)
		}
	}
}
