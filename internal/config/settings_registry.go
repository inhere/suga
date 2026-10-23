package axconfig

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/inhere/suga/pkg/agentcore/tools"

	goccyyaml "github.com/goccy/go-yaml"
	gkconfig "github.com/gookit/config/v2"
)

// ConfigValueType is the command-facing type of a configuration leaf.
type ConfigValueType string

const (
	ConfigTypeString     ConfigValueType = "string"
	ConfigTypeBool       ConfigValueType = "bool"
	ConfigTypeSwitch     ConfigValueType = "switch"
	ConfigTypeInt        ConfigValueType = "int"
	ConfigTypeInt64      ConfigValueType = "int64"
	ConfigTypeFloat      ConfigValueType = "float"
	ConfigTypeDuration   ConfigValueType = "duration"
	ConfigTypeStringList ConfigValueType = "string_list"
	ConfigTypeStringMap  ConfigValueType = "string_map"
	ConfigTypeYAML       ConfigValueType = "yaml"
	ConfigTypeEnum       ConfigValueType = "enum"
)

// ValueType is kept as a concise name for callers building config UIs.
type ValueType = ConfigValueType

// ConfigKey describes one supported configuration path. Reader is the existing
// typed reader used to validate the key; an empty Reader means that the key is
// known but has no typed reader and must not be reported as validated.
type ConfigKey struct {
	Pattern   string
	Type      ConfigValueType
	Sensitive bool
	Writable  bool
	Reader    string
}

// KeyDescriptor is the descriptive name used by config command adapters.
type KeyDescriptor = ConfigKey

// ConfigSource identifies where an effective value came from.
type ConfigSource string

const (
	SourceDefault ConfigSource = "default"
	SourceConfig  ConfigSource = "config"
	SourceEnv     ConfigSource = "env"
)

// ResolvedConfigValue is an effective value with safe source attribution. When
// Sensitive is true, Value is always a mask and never the secret itself.
type ResolvedConfigValue struct {
	Key        string
	Type       ConfigValueType
	Value      any
	Source     ConfigSource
	SourceName string
	Sensitive  bool
}

// Shared values are also consumed by the runtime readers. Keeping the enum
// vocabulary here prevents config set from accepting values that runtime wiring
// would reject.
const (
	ToolsApprovalAsk         = "ask"
	ToolsApprovalAuto        = "auto"
	ChatAgentProgressSummary = "summary"
	ChatAgentProgressFull    = "full"
	ChatAgentProgressOff     = "off"
	MemoryWriteAuto          = "auto"  // memory.write: writes take effect directly (default)
	MemoryWriteOff           = "off"   // memory.write: the memory tool is read-only
	MemoryTaintedAsk         = "ask"   // memory.write_tainted: tainted sessions ask (default; run refuses)
	MemoryTaintedDeny        = "deny"  // memory.write_tainted: tainted sessions never write
	MemoryTaintedAllow       = "allow" // memory.write_tainted: tainted sessions write without asking
)

var configKeyDescriptors = []ConfigKey{
	{Pattern: "store.body_inline_bytes", Type: ConfigTypeInt64, Writable: true, Reader: "tool_body"},
	{Pattern: "model", Type: ConfigTypeString, Writable: true, Reader: "model"},
	{Pattern: "compact_model", Type: ConfigTypeString, Writable: true, Reader: "model"},
	{Pattern: "fallback_models", Type: ConfigTypeStringList, Writable: true, Reader: "model"},
	// models.<scenario> routes background work by scenario (models.compact
	// supersedes the compact_model alias when both are set); unset scenarios
	// fall back to the compact model, then the turn's own model.
	{Pattern: "models.compact", Type: ConfigTypeString, Writable: true, Reader: "model"},
	{Pattern: "models.idle", Type: ConfigTypeString, Writable: true, Reader: "model"},
	{Pattern: "models.handoff", Type: ConfigTypeString, Writable: true, Reader: "model"},
	{Pattern: "max_steps", Type: ConfigTypeInt, Writable: true, Reader: "max_steps"},
	{Pattern: "budget.tokens", Type: ConfigTypeInt, Writable: true, Reader: "budget"},
	{Pattern: "budget.cost_usd", Type: ConfigTypeFloat, Writable: true, Reader: "budget"},
	{Pattern: "budget.scope", Type: ConfigTypeEnum, Writable: true, Reader: "budget"},
	{Pattern: "budget.estimate", Type: ConfigTypeEnum, Writable: true, Reader: "budget"},
	{Pattern: "budget.estimate_factor", Type: ConfigTypeFloat, Writable: true, Reader: "budget"},
	{Pattern: "budget.notice_ratio", Type: ConfigValueType("float"), Writable: true, Reader: "budget"},
	{Pattern: "workspace", Type: ConfigTypeString, Writable: true},
	// run.deadline_env: the env var name carrying this run's job deadline
	// (gofer sets GOFER_JOB_DEADLINE); empty = the deadline hint is off.
	{Pattern: "run.deadline_env", Type: ConfigTypeString, Writable: true},
	{Pattern: "serpapi_key", Type: ConfigTypeString, Sensitive: true},
	// The global outbound proxy: url is sensitive (it may embed credentials and
	// is only ever reported masked), sites is the allowlist that opts hosts in.
	{Pattern: "network.proxy.url", Type: ConfigTypeString, Sensitive: true, Reader: "network_proxy"},
	{Pattern: "network.proxy.sites", Type: ConfigTypeStringList, Writable: true, Reader: "network_proxy"},
	{Pattern: "retry.max_attempts", Type: ConfigTypeInt, Writable: true, Reader: "retry"},
	{Pattern: "retry.base_delay", Type: ConfigTypeDuration, Writable: true, Reader: "retry"},
	{Pattern: "retry.max_delay", Type: ConfigTypeDuration, Writable: true, Reader: "retry"},
	{Pattern: "retry.reset_after", Type: ConfigTypeDuration, Writable: true, Reader: "retry"},
	{Pattern: "retry.max_elapsed", Type: ConfigTypeDuration, Writable: true, Reader: "retry"},
	{Pattern: "timeouts.request", Type: ConfigTypeDuration, Writable: true, Reader: "timeouts"},
	{Pattern: "timeouts.response_header", Type: ConfigTypeDuration, Writable: true, Reader: "timeouts"},
	{Pattern: "timeouts.stream_idle", Type: ConfigTypeDuration, Writable: true, Reader: "timeouts"},
	{Pattern: "context.idle_after", Type: ConfigTypeDuration, Writable: true, Reader: "idle"},
	{Pattern: "context.idle_min_ratio", Type: ConfigTypeFloat, Writable: true, Reader: "idle"},
	{Pattern: "context.fold_every", Type: ConfigTypeInt, Writable: true, Reader: "fold"},
	{Pattern: "context.fold_keep", Type: ConfigTypeInt, Writable: true, Reader: "fold"},
	{Pattern: "context.fold_at", Type: ConfigTypeFloat, Writable: true, Reader: "fold"},
	{Pattern: "images.history_turns", Type: ConfigTypeInt, Writable: true, Reader: "images"},
	{Pattern: "images.max_request_count", Type: ConfigTypeInt, Writable: true, Reader: "images"},
	{Pattern: "images.max_request_bytes", Type: ConfigTypeInt64, Writable: true, Reader: "images"},
	{Pattern: "images.max_side", Type: ConfigTypeInt, Writable: true, Reader: "images"},
	{Pattern: "chat.history", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.history_size", Type: ConfigTypeInt, Writable: true, Reader: "chat"},
	{Pattern: "chat.markdown", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.spinner", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.agent_progress", Type: ConfigTypeEnum, Writable: true, Reader: "chat"},
	{Pattern: "chat.highlight_input", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.steering", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.terminal_title", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.diff", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.diff_lines", Type: ConfigTypeInt, Writable: true, Reader: "chat"},
	{Pattern: "chat.code_highlight", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.code_max_lines", Type: ConfigTypeInt, Writable: true, Reader: "chat"},
	{Pattern: "chat.ask_outside_dirs", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.undo_confirm", Type: ConfigTypeSwitch, Writable: true, Reader: "chat"},
	{Pattern: "chat.notify", Type: ConfigTypeEnum, Writable: true, Reader: "chat"},
	{Pattern: "chat.notify_after", Type: ConfigTypeDuration, Writable: true, Reader: "chat"},
	{Pattern: "chat.win32_input_mode", Type: ConfigTypeEnum, Writable: true, Reader: "chat"},
	// chat.theme: auto | dark | light, the palette of chat's markdown and code
	// rendering (auto reads the terminal background from $COLORFGBG; SUGA_THEME
	// overrides the config, see ChatTheme).
	{Pattern: "chat.theme", Type: ConfigTypeEnum, Writable: true, Reader: "chat"},
	{Pattern: "rules.file", Type: ConfigTypeString, Writable: true, Reader: "rules"},
	{Pattern: "transcripts", Type: ConfigTypeSwitch, Writable: true},
	{Pattern: "tasks.max_running", Type: ConfigTypeInt, Writable: true, Reader: "tasks"},
	{Pattern: "tasks.max_duration", Type: ConfigTypeDuration, Writable: true, Reader: "tasks"},
	{Pattern: "tasks.max_output_bytes", Type: ConfigTypeInt64, Writable: true, Reader: "tasks"},
	{Pattern: "tasks.wake_on_exit", Type: ConfigTypeBool, Writable: true, Reader: "tasks"},
	{Pattern: "agents.enabled", Type: ConfigTypeBool, Writable: true, Reader: "agents"},
	{Pattern: "agents.model", Type: ConfigTypeString, Writable: true, Reader: "agents"},
	{Pattern: "agents.max_steps", Type: ConfigTypeInt, Writable: true, Reader: "agents"},
	{Pattern: "agents.max_tokens", Type: ConfigTypeInt, Writable: true, Reader: "agents"},
	{Pattern: "agents.timeout", Type: ConfigTypeDuration, Writable: true, Reader: "agents"},
	{Pattern: "agents.nudge", Type: ConfigTypeString, Writable: true, Reader: "agents"},
	{Pattern: "mcp.call_timeout_sec", Type: ConfigTypeInt, Writable: true},
	{Pattern: "mcp.startup_timeout_sec", Type: ConfigTypeInt, Writable: true},
	{Pattern: "mcp.servers", Type: ConfigTypeYAML, Writable: true, Reader: "mcp"},
	{Pattern: "tools.preferred", Type: ConfigTypeYAML, Writable: true, Reader: "preferred_tools"},
	{Pattern: "tools.deferred", Type: ConfigTypeEnum, Writable: true},
	// tools.deferred_native: which native tools may be deferred (auto | never |
	// [names]); tools.deferred_apply: when a loaded native tool joins the tool
	// table (segment | immediate). MCP tools keep the immediate behaviour.
	{Pattern: "tools.deferred_native", Type: ConfigTypeYAML, Writable: true, Reader: "deferred_native"},
	{Pattern: "tools.deferred_apply", Type: ConfigTypeEnum, Writable: true},
	{Pattern: "tools.allow_write", Type: ConfigTypeBool, Writable: true},
	// session_message: the tool that lets a model message the other chat sessions
	// of this SUGA_HOME, and how deep their automatic replies may go.
	{Pattern: "tools.session_message.enabled", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.session_message.max_hops", Type: ConfigTypeInt, Writable: true},
	{Pattern: "tools.approval", Type: ConfigTypeEnum, Writable: true},
	{Pattern: "tools.max_output_bytes", Type: ConfigTypeInt64, Writable: true},
	{Pattern: "tools.max_spill_bytes", Type: ConfigTypeInt64, Writable: true},
	{Pattern: "tools.spill.max_bytes", Type: ConfigTypeInt64, Writable: true, Reader: "spill"},
	{Pattern: "tools.spill.max_age", Type: ConfigTypeDuration, Writable: true, Reader: "spill"},
	{Pattern: "tools.spill.compress_after", Type: ConfigTypeDuration, Writable: true, Reader: "spill"},
	{Pattern: "tools.spill.compression", Type: ConfigTypeEnum, Writable: true, Reader: "spill"},
	{Pattern: "tools.spill.on_quota", Type: ConfigTypeEnum, Writable: true, Reader: "spill"},
	{Pattern: "tools.spill.keep_days", Type: ConfigTypeInt, Writable: true, Reader: "spill"},
	{Pattern: "tools.read_page_bytes", Type: ConfigTypeInt, Writable: true},
	{Pattern: "tools.parallel_reads", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.minimize_output", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.loop_guard", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.dependency_roots", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.standards_roots", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.skill_roots", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.shell", Type: ConfigTypeString, Writable: true},
	{Pattern: "tools.shell_timeout_max", Type: ConfigTypeInt, Writable: true},
	{Pattern: "tools.shell_background_after", Type: ConfigTypeDuration, Writable: true},
	// tools.shell_limits: the memory, process count and CPU time a shell_exec run
	// and its process tree may use (0 = that limit off, off = all off).
	{Pattern: "tools.shell_limits.memory", Type: ConfigTypeString, Writable: true},
	{Pattern: "tools.shell_limits.processes", Type: ConfigTypeInt, Writable: true},
	{Pattern: "tools.shell_limits.cpu_time", Type: ConfigTypeDuration, Writable: true},
	{Pattern: "tools.shell_limits.off", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.web_fetch.allow_private", Type: ConfigTypeBool, Writable: true},
	{Pattern: "tools.web_fetch.proxy", Type: ConfigTypeString, Sensitive: true},
	// tools.web_fetch.user_agent is the User-Agent the tool sends; empty keeps the
	// built-in suga-web-fetch/1.0.
	{Pattern: "tools.web_fetch.user_agent", Type: ConfigTypeString, Writable: true},
	{Pattern: "tools.interpreters", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "tools.read_roots", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "tools.auto_allow", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "tools.read_only_allow", Type: ConfigTypeStringList, Writable: true},
	// tools.disabled: tool names the runtime must not offer to the model at all
	// (not registered, so absent from the tool list, tool_search and manifest).
	{Pattern: "tools.disabled", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "memory.write", Type: ConfigTypeEnum, Writable: true},
	{Pattern: "memory.write_tainted", Type: ConfigTypeEnum, Writable: true},
	{Pattern: "memory.taint_tools", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "memory.taint_on_spill", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "memory.user.backend", Type: ConfigTypeEnum, Writable: true},
	{Pattern: "memory.user.command", Type: ConfigTypeString, Writable: true},
	{Pattern: "memory.user.args", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "memory.user.timeout_sec", Type: ConfigTypeInt, Writable: true},
	{Pattern: "memory.project.backend", Type: ConfigTypeEnum, Writable: true},
	{Pattern: "memory.project.command", Type: ConfigTypeString, Writable: true},
	{Pattern: "memory.project.args", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "memory.project.timeout_sec", Type: ConfigTypeInt, Writable: true},
	{Pattern: "skills.list_max", Type: ConfigTypeInt, Writable: true},
	{Pattern: "skills.always_load", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "hooks.command", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "hooks.timeout_ms", Type: ConfigTypeInt, Writable: true},
	{Pattern: "hooks.stop_args", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "hooks.stop_wait_sec", Type: ConfigTypeInt, Writable: true},
	{Pattern: "hooks.stop_background", Type: ConfigTypeBool, Writable: true},
	{Pattern: "hooks.post_tool_use.tools", Type: ConfigTypeStringList, Writable: true},
	{Pattern: "hooks.post_tool_use.name", Type: ConfigTypeString, Writable: true},
	{Pattern: "tools.snapshots.enabled", Type: ConfigTypeBool, Writable: true, Reader: "snapshots"},
	{Pattern: "tools.snapshots.max_file_bytes", Type: ConfigTypeInt64, Writable: true, Reader: "snapshots"},
	{Pattern: "tools.snapshots.max_session_bytes", Type: ConfigTypeInt64, Writable: true, Reader: "snapshots"},
	{Pattern: "tools.snapshots.max_global_bytes", Type: ConfigTypeInt64, Writable: true, Reader: "snapshots"},
	{Pattern: "tools.snapshots.keep_turns", Type: ConfigTypeInt, Writable: true, Reader: "snapshots"},
	{Pattern: "tools.snapshots.keep_days", Type: ConfigTypeInt, Writable: true, Reader: "snapshots"},
	{Pattern: "tools.snapshots.on_error", Type: ConfigTypeEnum, Writable: true, Reader: "snapshots"},
	{Pattern: "tools.approval_rules.enabled", Type: ConfigTypeBool, Writable: true, Reader: "approval_rules"},
	{Pattern: "tools.approval_rules.always_prefix_tokens", Type: ConfigTypeInt, Writable: true, Reader: "approval_rules"},
	{Pattern: "tools.approval_rules.persist_ask", Type: ConfigTypeBool, Writable: true, Reader: "approval_rules"},
	{Pattern: "tools.approval_rules.project_trust", Type: ConfigTypeStringMap, Writable: true, Reader: "approval_rules"},
	{Pattern: "gc.retention.interactive", Type: ConfigTypeDuration, Writable: true, Reader: "gc_retention"},
	{Pattern: "gc.retention.noninteractive", Type: ConfigTypeDuration, Writable: true, Reader: "gc_retention"},
	// log.* configures the process logger (internal/wiring/logging.go): level,
	// the rotating file (or off), the stderr copy, the rotation size, how many
	// rotated files are kept and whether they are compressed.
	{Pattern: "log.level", Type: ConfigTypeEnum, Writable: true, Reader: "log"},
	{Pattern: "log.file", Type: ConfigTypeString, Writable: true, Reader: "log"},
	{Pattern: "log.stderr", Type: ConfigTypeEnum, Writable: true, Reader: "log"},
	{Pattern: "log.max_size", Type: ConfigTypeInt64, Writable: true, Reader: "log"},
	{Pattern: "log.keep", Type: ConfigTypeInt, Writable: true, Reader: "log"},
	{Pattern: "log.compress", Type: ConfigTypeBool, Writable: true, Reader: "log"},
}

var (
	configIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_@:/-]+$`)
	modelIdentifierPattern  = regexp.MustCompile(`^[A-Za-z0-9_@:/.-]+$`)
	envReferencePattern     = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

var providerKeyDescriptors = map[string]ConfigKey{
	"type":              {Pattern: "providers.<id>.type", Type: ConfigTypeEnum, Writable: true, Reader: "providers"},
	"base_url":          {Pattern: "providers.<id>.base_url", Type: ConfigTypeString, Writable: true, Reader: "providers"},
	"api_key":           {Pattern: "providers.<id>.api_key", Type: ConfigTypeString, Sensitive: true, Reader: "providers"},
	"headers":           {Pattern: "providers.<id>.headers", Type: ConfigTypeStringMap, Sensitive: true, Reader: "providers"},
	"context_window":    {Pattern: "providers.<id>.context_window", Type: ConfigTypeInt, Writable: true, Reader: "providers"},
	"max_output_tokens": {Pattern: "providers.<id>.max_output_tokens", Type: ConfigTypeInt, Writable: true, Reader: "providers"},
	"thinking":          {Pattern: "providers.<id>.thinking", Type: ConfigTypeEnum, Writable: true, Reader: "providers"},
	"thinking_style":    {Pattern: "providers.<id>.thinking_style", Type: ConfigTypeEnum, Writable: true, Reader: "providers"},
	"stream_required":   {Pattern: "providers.<id>.stream_required", Type: ConfigTypeBool, Writable: true, Reader: "providers"},
	"vision":            {Pattern: "providers.<id>.vision", Type: ConfigTypeBool, Writable: true, Reader: "providers"},
	"prompt_cache":      {Pattern: "providers.<id>.prompt_cache", Type: ConfigTypeBool, Writable: true, Reader: "providers"},
	"compat":            {Pattern: "providers.<id>.compat", Type: ConfigTypeStringList, Writable: true, Reader: "providers"},
}

var providerModelKeyDescriptors = map[string]ConfigKey{
	"model_id":          {Pattern: "providers.<id>.models.<name>.model_id", Type: ConfigTypeString, Writable: true, Reader: "providers"},
	"type":              {Pattern: "providers.<id>.models.<name>.type", Type: ConfigTypeEnum, Writable: true, Reader: "providers"},
	"base_url":          {Pattern: "providers.<id>.models.<name>.base_url", Type: ConfigTypeString, Writable: true, Reader: "providers"},
	"context_window":    {Pattern: "providers.<id>.models.<name>.context_window", Type: ConfigTypeInt, Writable: true, Reader: "providers"},
	"max_output_tokens": {Pattern: "providers.<id>.models.<name>.max_output_tokens", Type: ConfigTypeInt, Writable: true, Reader: "providers"},
	"thinking":          {Pattern: "providers.<id>.models.<name>.thinking", Type: ConfigTypeEnum, Writable: true, Reader: "providers"},
	"thinking_style":    {Pattern: "providers.<id>.models.<name>.thinking_style", Type: ConfigTypeEnum, Writable: true, Reader: "providers"},
	"stream_required":   {Pattern: "providers.<id>.models.<name>.stream_required", Type: ConfigTypeBool, Writable: true, Reader: "providers"},
	"vision":            {Pattern: "providers.<id>.models.<name>.vision", Type: ConfigTypeBool, Writable: true, Reader: "providers"},
	"price":             {Pattern: "providers.<id>.models.<name>.price", Type: ConfigTypeStringMap, Writable: true, Reader: "providers"},
}

var providerModelNestedDescriptors = map[string]ConfigKey{
	"price.input":       {Pattern: "providers.<id>.models.<name>.price.input", Type: ConfigTypeFloat, Writable: true, Reader: "providers"},
	"price.output":      {Pattern: "providers.<id>.models.<name>.price.output", Type: ConfigTypeFloat, Writable: true, Reader: "providers"},
	"price.cache_read":  {Pattern: "providers.<id>.models.<name>.price.cache_read", Type: ConfigTypeFloat, Writable: true, Reader: "providers"},
	"price.cache_write": {Pattern: "providers.<id>.models.<name>.price.cache_write", Type: ConfigTypeFloat, Writable: true, Reader: "providers"},
}

var providerNestedDescriptors = map[string]ConfigKey{
	"retry.max_attempts":       {Pattern: "providers.<id>.retry.max_attempts", Type: ConfigTypeInt, Writable: true, Reader: "providers"},
	"retry.base_delay":         {Pattern: "providers.<id>.retry.base_delay", Type: ConfigTypeDuration, Writable: true, Reader: "providers"},
	"retry.max_delay":          {Pattern: "providers.<id>.retry.max_delay", Type: ConfigTypeDuration, Writable: true, Reader: "providers"},
	"retry.reset_after":        {Pattern: "providers.<id>.retry.reset_after", Type: ConfigTypeDuration, Writable: true, Reader: "providers"},
	"retry.max_elapsed":        {Pattern: "providers.<id>.retry.max_elapsed", Type: ConfigTypeDuration, Writable: true, Reader: "providers"},
	"timeouts.request":         {Pattern: "providers.<id>.timeouts.request", Type: ConfigTypeDuration, Writable: true, Reader: "providers"},
	"timeouts.response_header": {Pattern: "providers.<id>.timeouts.response_header", Type: ConfigTypeDuration, Writable: true, Reader: "providers"},
	"timeouts.stream_idle":     {Pattern: "providers.<id>.timeouts.stream_idle", Type: ConfigTypeDuration, Writable: true, Reader: "providers"},
}

// KnownConfigKeys returns a copy of the fixed key descriptors. Provider and
// model paths are represented by the dynamic patterns returned by MatchConfigKey.
func KnownConfigKeys() []ConfigKey {
	out := append([]ConfigKey(nil), configKeyDescriptors...)
	sort.Slice(out, func(i, j int) bool { return out[i].Pattern < out[j].Pattern })
	return out
}

// MatchConfigKey validates a key and returns its descriptor. Dynamic provider
// and model names are constrained so arbitrary YAML structure cannot be injected.
func MatchConfigKey(key string) (ConfigKey, error) {
	key = strings.TrimSpace(key)
	for _, descriptor := range configKeyDescriptors {
		if key == descriptor.Pattern {
			return descriptor, nil
		}
	}
	if descriptor, ok := matchProviderConfigKey(key); ok {
		return descriptor, nil
	}
	return ConfigKey{}, fmt.Errorf("unknown configuration key %q", key)
}

// IsSecretKey conservatively classifies credentials before a caller displays or
// writes a key. A false positive is safer than putting a likely secret in YAML.
func IsSecretKey(key string) bool {
	for _, part := range strings.FieldsFunc(strings.ToLower(key), func(r rune) bool { return r == '.' || r == '-' || r == '_' }) {
		switch part {
		case "key", "token", "password", "secret", "credential":
			return true
		}
	}
	return strings.Contains(strings.ToLower(key), "api_key")
}

// IsSimpleConfigType reports whether the registry can validate a value from its
// scalar/list representation without a structured reader.
func IsSimpleConfigType(valueType ConfigValueType) bool {
	switch valueType {
	case ConfigTypeString, ConfigTypeBool, ConfigTypeSwitch, ConfigTypeInt, ConfigTypeInt64,
		ConfigTypeFloat, ConfigTypeDuration, ConfigTypeStringList, ConfigTypeEnum:
		return true
	default:
		return false
	}
}

// ParseConfigValue converts a command value according to the key descriptor.
// Errors name both the key and the expected type so the CLI can return input
// error code 2 without duplicating the registry's type rules.
func ParseConfigValue(key, raw string) (any, error) {
	descriptor, err := MatchConfigKey(key)
	if err != nil {
		return nil, err
	}
	value, err := parseConfigValue(descriptor, raw, key)
	if err != nil {
		return nil, fmt.Errorf("%s: expected %s: %w", key, descriptor.Type, err)
	}
	return value, nil
}

func parseConfigValue(descriptor ConfigKey, raw, key string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch descriptor.Type {
	case ConfigTypeString:
		return raw, nil
	case ConfigTypeBool, ConfigTypeSwitch:
		switch strings.ToLower(raw) {
		case "true", "on", "yes":
			return true, nil
		case "false", "off", "no":
			return false, nil
		default:
			return nil, fmt.Errorf("want true or false")
		}
	case ConfigTypeInt:
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("want a whole number")
		}
		return value, nil
	case ConfigTypeInt64:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("want a whole number")
		}
		return value, nil
	case ConfigTypeFloat:
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("want a number")
		}
		return value, nil
	case ConfigTypeDuration:
		value, err := parseIdleDuration(raw)
		if err != nil {
			return nil, err
		}
		return value, nil
	case ConfigTypeStringList:
		if key == "tools.interpreters" && (strings.EqualFold(raw, "off") || strings.EqualFold(raw, "false")) {
			return false, nil
		}
		// The value may also be written as a YAML flow list, the way it appears
		// in config.yaml. A value that starts with "[" is parsed as that list and
		// must be a valid list of strings; anything else stays comma separated.
		if strings.HasPrefix(raw, "[") {
			return parseYAMLStringList(raw)
		}
		if raw == "" {
			return []string{}, nil
		}
		items := strings.Split(raw, ",")
		out := make([]string, len(items))
		for i, item := range items {
			out[i] = strings.TrimSpace(item)
			if out[i] == "" {
				return nil, fmt.Errorf("list item %d is empty", i)
			}
		}
		return out, nil
	case ConfigTypeStringMap:
		var value map[string]string
		if err := goccyyaml.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("want a YAML mapping")
		}
		return value, nil
	case ConfigTypeYAML:
		var value any
		if err := goccyyaml.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("want a YAML value")
		}
		return value, nil
	case ConfigTypeEnum:
		return parseConfigEnum(key, raw)
	default:
		return nil, fmt.Errorf("unsupported registry type %q", descriptor.Type)
	}
}

func parseConfigEnum(key, raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	var allowed []string
	switch {
	case strings.HasSuffix(key, ".type"):
		allowed = []string{ProviderTypeOpenAI, ProviderTypeOpenAIResponses, ProviderTypeAnthropic}
	case strings.HasSuffix(key, ".thinking"):
		allowed = ThinkingLevels
	case strings.HasSuffix(key, ".thinking_style"):
		allowed = ThinkingStyles
	case key == "chat.agent_progress":
		allowed = []string{ChatAgentProgressSummary, ChatAgentProgressFull, ChatAgentProgressOff}
	case key == "chat.notify":
		allowed = []string{"auto", "bell", "osc9", "off"}
	case key == "chat.win32_input_mode":
		allowed = []string{"auto", "on", "off"}
	case key == "chat.theme":
		allowed = []string{ChatThemeAuto, ChatThemeDark, ChatThemeLight}
	case key == "tools.deferred":
		allowed = []string{string(tools.DeferredAuto), string(tools.DeferredAlways), string(tools.DeferredNever)}
	case key == "tools.deferred_apply":
		allowed = []string{string(tools.DeferredApplySegment), string(tools.DeferredApplyImmediate)}
	case key == "tools.approval":
		allowed = []string{ToolsApprovalAsk, ToolsApprovalAuto}
	case key == "memory.write":
		allowed = []string{MemoryWriteAuto, MemoryWriteOff}
	case key == "log.level":
		allowed = LogLevels
	case key == "log.stderr":
		allowed = []string{LogStderrAuto, LogStderrAlways, LogStderrNever}
	case key == "memory.write_tainted":
		allowed = []string{MemoryTaintedAsk, MemoryTaintedDeny, MemoryTaintedAllow}
	case key == "memory.user.backend":
		allowed = []string{"files", "exec"}
	case key == "memory.project.backend":
		allowed = []string{"files", "gofer", "exec"}
	case strings.HasPrefix(key, "budget."):
		switch key {
		case "budget.scope":
			allowed = []string{"turn", "session"}
		case "budget.estimate":
			allowed = []string{"auto", "on", "off"}
		default:
			return "", fmt.Errorf("no enum values registered")
		}
	case strings.HasSuffix(key, ".on_error"):
		allowed = []string{SnapshotOnErrorDeny, SnapshotOnErrorContinue}
	default:
		return "", fmt.Errorf("no enum values registered")
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", fmt.Errorf("want one of %s", strings.Join(allowed, ", "))
}

// ValidateConfigKey dispatches validation to an existing typed reader. Simple
// keys without a reader still get type/range/enum validation from the registry;
// structured values remain rejected rather than gaining a second validator.
func ValidateConfigKey(key string) error {
	descriptor, err := MatchConfigKey(key)
	if err != nil {
		return err
	}
	if descriptor.Reader == "" {
		if !IsSimpleConfigType(descriptor.Type) {
			return fmt.Errorf("%s has no typed reader; semantic validation is not available", key)
		}
		return validateSimpleConfigValue(key, descriptor)
	}
	return dispatchConfigReader(descriptor.Reader)
}

func validateSimpleConfigValue(key string, descriptor ConfigKey) error {
	raw := gkconfig.Get(key)
	if raw == nil {
		return nil
	}
	switch descriptor.Type {
	case ConfigTypeString:
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("%s must be a string", key)
		}
	case ConfigTypeBool, ConfigTypeSwitch:
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("%s must be true or false", key)
		}
	case ConfigTypeInt, ConfigTypeInt64:
		n, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(raw)), 10, 64)
		if err != nil {
			return fmt.Errorf("%s must be a whole number", key)
		}
		if strings.HasSuffix(key, "timeout_sec") || strings.HasSuffix(key, "timeout_ms") ||
			strings.HasSuffix(key, "max_bytes") || strings.HasSuffix(key, "max_output_bytes") ||
			key == "mcp.call_timeout_sec" || key == "skills.list_max" {
			if n < 1 {
				return fmt.Errorf("%s must be a whole number >= 1", key)
			}
		}
	case ConfigTypeFloat:
		value, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(raw)), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s must be a number", key)
		}
	case ConfigTypeDuration:
		if _, err := parseIdleDuration(strings.TrimSpace(fmt.Sprint(raw))); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	case ConfigTypeStringList:
		if key == "tools.interpreters" {
			if _, ok := raw.(bool); ok {
				return nil
			}
		}
		if !isStringList(raw) {
			return fmt.Errorf("%s must be a list of strings", key)
		}
	case ConfigTypeEnum:
		if _, err := parseConfigEnum(key, fmt.Sprint(raw)); err != nil {
			return err
		}
	}
	return nil
}

func isStringList(raw any) bool {
	switch value := raw.(type) {
	case []string:
		return true
	case []any:
		for _, item := range value {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func dispatchConfigReader(reader string) error {
	var err error
	switch reader {
	case "model":
		_, err = ReadModelSettings("")
	case "providers":
		_, err = ReadProviders()
	case "max_steps":
		_, err = ReadMaxSteps(0)
	case "budget":
		_, err = ReadBudgetSettings(BudgetFlags{})
	case "retry":
		_, err = ReadRetryPolicy()
	case "timeouts":
		_, err = ReadRequestTimeouts()
	case "idle":
		_, err = ReadIdleSettings()
	case "fold":
		_, err = ReadFoldSettings()
	case "images":
		_, err = ReadImageSettings()
	case "chat":
		_, err = ReadChatSettings()
	case "tasks":
		_, err = ReadTaskSettings()
	case "agents":
		_, err = ReadAgentSettings()
	case "snapshots":
		_, err = ReadSnapshotSettings()
	case "spill":
		_, err = ReadSpillSettings()
	case "approval_rules":
		_, err = ReadApprovalRulesSettings()
	case "gc_retention":
		_, err = ReadGCRetentionSettings()
	case "mcp":
		_, err = ReadMCPServerSettings()
	case "network_proxy":
		_, err = ReadNetworkProxy()
	case "preferred_tools":
		_, err = ReadPreferredTools()
	case "deferred_native":
		_, err = ReadDeferredNativeTools()
	case "rules":
		_, err = ReadRuleFileName()
	case "tool_body":
		_, err = ReadToolBodySettings()
	case "log":
		_, err = ReadLogSettings()
	case "":
		return nil
	default:
		return fmt.Errorf("unknown config reader %q", reader)
	}
	return err
}
