package axconfig

// Chat settings, config value readers and effective value resolution for the settings registry.

import (
	"fmt"
	"strings"
	"time"

	gkconfig "github.com/gookit/config/v2"
)

// ReadChatSettings groups the existing chat readers without introducing another
// validation implementation for their defaults or accepted values.
type ChatSettings struct {
	History, Markdown, Spinner, HighlightInput bool
	Steering, TerminalTitle, Diff              bool
	AskOutsideDirs, UndoConfirm                bool
	AgentProgress                              string
	HistorySize, DiffLines                     int
	CodeHighlight                              bool
	CodeMaxLines                               int
	Notify                                     string
	NotifyAfter                                time.Duration
	Win32InputMode                             string
	Theme                                      string
}

func ReadChatSettings() (ChatSettings, error) {
	historySize, err := ChatHistorySize()
	if err != nil {
		return ChatSettings{}, err
	}
	diffLines, err := ChatDiffLines()
	if err != nil {
		return ChatSettings{}, err
	}
	codeMaxLines, err := ChatCodeMaxLines()
	if err != nil {
		return ChatSettings{}, err
	}
	notify, err := ChatNotifyMode()
	if err != nil {
		return ChatSettings{}, err
	}
	agentProgress, err := ChatAgentProgress()
	if err != nil {
		return ChatSettings{}, err
	}
	notifyAfter, err := ChatNotifyAfter()
	if err != nil {
		return ChatSettings{}, err
	}
	win32InputMode, err := ChatWin32InputMode()
	if err != nil {
		return ChatSettings{}, err
	}
	theme, err := ChatTheme()
	if err != nil {
		return ChatSettings{}, err
	}
	return ChatSettings{
		History: ChatHistoryEnabled(), Markdown: ChatMarkdownEnabled(), Spinner: ChatSpinnerEnabled(),
		HighlightInput: ChatHighlightInputEnabled(), Steering: ChatSteeringEnabled(),
		TerminalTitle: ChatTerminalTitleEnabled(), Diff: ChatDiffEnabled(),
		AskOutsideDirs: ChatAskOutsideDirs(), UndoConfirm: ChatUndoConfirmEnabled(),
		AgentProgress: agentProgress,
		HistorySize:   historySize, DiffLines: diffLines, Notify: notify, NotifyAfter: notifyAfter,
		CodeHighlight: ChatCodeHighlightEnabled(), CodeMaxLines: codeMaxLines,
		Win32InputMode: win32InputMode, Theme: theme,
	}, nil
}

// ReadConfigValue resolves a key through its existing reader and applies the
// documented model/environment/default precedence. Sensitive values are masked.
func ReadConfigValue(key string) (ResolvedConfigValue, error) {
	key = strings.TrimSpace(key)
	descriptor, err := MatchConfigKey(key)
	if err != nil {
		return ResolvedConfigValue{}, err
	}
	if descriptor.Reader != "" {
		if err := dispatchConfigReader(descriptor.Reader); err != nil {
			return ResolvedConfigValue{}, fmt.Errorf("validate %s: %w", key, err)
		}
	}
	value, _, err := effectiveConfigValue(key)
	if err != nil {
		return ResolvedConfigValue{}, err
	}
	source, sourceName := configValueSource(key)
	if descriptor.Sensitive || IsSecretKey(key) {
		descriptor.Sensitive = true
		value = maskSecret(value)
	}
	return ResolvedConfigValue{
		Key: key, Type: descriptor.Type, Value: value, Source: source,
		SourceName: sourceName, Sensitive: descriptor.Sensitive,
	}, nil
}

// ResolveConfigValue is an alias suited to callers that treat registry reads as
// resolution rather than raw configuration lookup.
func ResolveConfigValue(key string) (ResolvedConfigValue, error) {
	return ReadConfigValue(key)
}

// effectiveConfigValue dispatches a known key to the existing typed readers and
// returns the effective value without creating a second configuration validator.
func effectiveConfigValue(key string) (any, bool, error) {
	if strings.HasPrefix(key, "providers.") {
		return effectiveProviderValue(key)
	}
	switch key {
	case "store.body_inline_bytes":
		settings, err := ReadToolBodySettings()
		return settings.InlineBytes, gkconfig.Get(key) != nil, err
	case "model", "compact_model", "fallback_models":
		settings, err := ReadModelSettings("")
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "model":
			return settings.Main.Ref(), true, nil
		case "compact_model":
			if settings.Compact == nil {
				return "", false, nil
			}
			return settings.Compact.Ref(), true, nil
		default:
			refs := make([]string, 0, len(settings.Fallbacks))
			for _, model := range settings.Fallbacks {
				refs = append(refs, model.Ref())
			}
			return refs, len(refs) > 0, nil
		}
	case "max_steps":
		value, err := ReadMaxSteps(0)
		return value, true, err
	case "gc.retention.interactive", "gc.retention.noninteractive":
		settings, err := ReadGCRetentionSettings()
		if err != nil {
			return nil, false, err
		}
		if key == "gc.retention.interactive" {
			return settings.Interactive, true, nil
		}
		return settings.NonInteractive, true, nil
	case "budget.tokens", "budget.cost_usd", "budget.scope", "budget.estimate", "budget.estimate_factor", "budget.notice_ratio":
		settings, err := ReadBudgetSettings(BudgetFlags{})
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "budget.tokens":
			return settings.Tokens, true, nil
		case "budget.cost_usd":
			return settings.CostUSD, true, nil
		case "budget.scope":
			return settings.Scope, true, nil
		case "budget.estimate":
			return settings.Estimate, true, nil
		case "budget.estimate_factor":
			return settings.EstimateFactor, true, nil
		default:
			return settings.NoticeRatio, true, nil
		}

	case "retry.max_attempts", "retry.base_delay", "retry.max_delay", "retry.reset_after":
		policy, err := ReadRetryPolicy()
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "retry.max_attempts":
			return policy.MaxAttempts, true, nil
		case "retry.base_delay":
			return policy.BaseDelay, true, nil
		case "retry.max_delay":
			return policy.MaxDelay, true, nil
		default:
			return policy.ResetAfter, true, nil
		}
	case "timeouts.request", "timeouts.response_header":
		settings, err := ReadRequestTimeouts()
		if err != nil {
			return nil, false, err
		}
		if key == "timeouts.request" {
			return settings.Request, true, nil
		}
		return settings.ResponseHeader, true, nil
	case "context.idle_after", "context.idle_min_ratio":
		settings, err := ReadIdleSettings()
		if err != nil {
			return nil, false, err
		}
		if key == "context.idle_after" {
			return settings.After, true, nil
		}
		return settings.MinRatio, true, nil
	case "context.fold_every", "context.fold_keep", "context.fold_at":
		settings, err := ReadFoldSettings()
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "context.fold_every":
			return settings.Every, true, nil
		case "context.fold_at":
			return settings.At, true, nil
		}
		return settings.Keep, true, nil
	case "chat.history", "chat.markdown", "chat.spinner", "chat.highlight_input", "chat.steering", "chat.terminal_title", "chat.diff", "chat.ask_outside_dirs", "chat.undo_confirm", "chat.history_size", "chat.diff_lines", "chat.notify", "chat.notify_after", "chat.agent_progress", "chat.code_highlight", "chat.code_max_lines", "chat.win32_input_mode", "chat.theme":
		settings, err := ReadChatSettings()
		if err != nil {
			return nil, false, err
		}
		return chatSettingValue(key, settings), true, nil
	case "rules.file":
		value, err := ReadRuleFileName()
		return value, true, err
	case "transcripts":
		return TranscriptsEnabled(), gkconfig.Get(key) != nil, nil
	case "tasks.max_running", "tasks.max_duration", "tasks.max_output_bytes", "tasks.wake_on_exit":
		settings, err := ReadTaskSettings()
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "tasks.max_running":
			return settings.Limits.MaxRunning, true, nil
		case "tasks.max_duration":
			return settings.Limits.MaxDuration, true, nil
		case "tasks.max_output_bytes":
			return settings.Limits.MaxOutputBytes, true, nil
		default:
			return settings.WakeOnExit, true, nil
		}
	case "agents.enabled", "agents.model", "agents.max_steps", "agents.max_tokens", "agents.timeout", "agents.nudge":
		settings, err := ReadAgentSettings()
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "agents.nudge":
			on, err := ReadNudgeSetting()
			if err != nil {
				return nil, false, err
			}
			return on, true, nil
		case "agents.enabled":
			return settings.Enabled, true, nil
		case "agents.model":
			return settings.Model, gkconfig.Get(key) != nil, nil
		case "agents.max_steps":
			return settings.MaxSteps, true, nil
		case "agents.max_tokens":
			return settings.MaxTokens, true, nil
		default:
			return settings.Timeout, true, nil
		}
	case "network.proxy.url", "network.proxy.sites":
		settings, err := ReadNetworkProxy()
		if err != nil {
			return nil, false, err
		}
		if key == "network.proxy.url" {
			return settings.URL, settings.URL != "", nil
		}
		return settings.Sites, len(settings.Sites) > 0, nil
	case "run.deadline_env":
		// A plain config string on purpose: the deadline itself is read from the
		// process environment per request, not resolved here.
		return ReadRunDeadlineEnv(), gkconfig.Get(key) != nil, nil
	case "mcp.call_timeout_sec":
		return MCPCallTimeout(), gkconfig.Get(key) != nil, nil
	case "mcp.startup_timeout_sec":
		return MCPStartupTimeout(), gkconfig.Get(key) != nil, nil
	case "tools.preferred":
		value, err := ReadPreferredTools()
		return value, gkconfig.Get(key) != nil, err
	case "tools.allow_write":
		if value, present := lookupRawConfig(key); present {
			return value, true, nil
		}
		return false, false, nil
	case "tools.deferred":
		if value, present := lookupRawConfig(key); present {
			return value, true, nil
		}
		return "auto", false, nil
	case "tools.approval":
		if value, present := lookupRawConfig(key); present {
			return value, true, nil
		}
		return "ask", false, nil
	case "tools.parallel_reads", "tools.minimize_output", "tools.loop_guard", "tools.dependency_roots", "tools.standards_roots", "tools.skill_roots":
		if value, present := lookupRawConfig(key); present {
			return value, true, nil
		}
		return true, false, nil
	case "tools.web_fetch.allow_private":
		if value, present := lookupRawConfig(key); present {
			return value, true, nil
		}
		return false, false, nil
	case "tools.snapshots.enabled", "tools.snapshots.max_file_bytes", "tools.snapshots.max_session_bytes", "tools.snapshots.max_global_bytes", "tools.snapshots.keep_turns", "tools.snapshots.keep_days", "tools.snapshots.on_error":
		settings, err := ReadSnapshotSettings()
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "tools.snapshots.enabled":
			return settings.Enabled, true, nil
		case "tools.snapshots.max_file_bytes":
			return settings.MaxFileBytes, true, nil
		case "tools.snapshots.max_session_bytes":
			return settings.MaxSessionBytes, true, nil
		case "tools.snapshots.max_global_bytes":
			return settings.MaxGlobalBytes, true, nil
		case "tools.snapshots.keep_turns":
			return settings.KeepTurns, true, nil
		case "tools.snapshots.keep_days":
			return settings.KeepDays, true, nil
		default:
			return settings.OnError, true, nil
		}
	case "tools.spill.max_bytes", "tools.spill.max_age", "tools.spill.compress_after", "tools.spill.compression", "tools.spill.on_quota", "tools.spill.keep_days":
		settings, err := ReadSpillSettings()
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "tools.spill.max_bytes":
			return settings.MaxBytes, true, nil
		case "tools.spill.max_age":
			return settings.MaxAge, true, nil
		case "tools.spill.compress_after":
			return settings.CompressAfter, true, nil
		case "tools.spill.compression":
			return settings.Compression, true, nil
		case "tools.spill.on_quota":
			return settings.OnQuota, true, nil
		default:
			return settings.KeepDays, true, nil
		}
	case "tools.approval_rules.enabled", "tools.approval_rules.always_prefix_tokens", "tools.approval_rules.persist_ask":
		settings, err := ReadApprovalRulesSettings()
		if err != nil {
			return nil, false, err
		}
		switch key {
		case "tools.approval_rules.enabled":
			return settings.Enabled, true, nil
		case "tools.approval_rules.always_prefix_tokens":
			return settings.AlwaysPrefixTokens, true, nil
		default:
			return settings.PersistAsk, true, nil
		}
	default:
		value, present := lookupRawConfig(key)
		return value, present, nil
	}
}

func chatSettingValue(key string, settings ChatSettings) any {
	switch key {
	case "chat.history":
		return settings.History
	case "chat.markdown":
		return settings.Markdown
	case "chat.spinner":
		return settings.Spinner
	case "chat.agent_progress":
		return settings.AgentProgress
	case "chat.highlight_input":
		return settings.HighlightInput
	case "chat.steering":
		return settings.Steering
	case "chat.terminal_title":
		return settings.TerminalTitle
	case "chat.diff":
		return settings.Diff
	case "chat.ask_outside_dirs":
		return settings.AskOutsideDirs
	case "chat.undo_confirm":
		return settings.UndoConfirm
	case "chat.history_size":
		return settings.HistorySize
	case "chat.diff_lines":
		return settings.DiffLines
	case "chat.code_highlight":
		return settings.CodeHighlight
	case "chat.code_max_lines":
		return settings.CodeMaxLines
	case "chat.notify":
		return settings.Notify
	case "chat.win32_input_mode":
		return settings.Win32InputMode
	case "chat.theme":
		return settings.Theme
	default:
		return settings.NotifyAfter
	}
}

func effectiveProviderValue(key string) (any, bool, error) {
	parts, modelName, field, nested, ok := parseProviderKey(key)
	if !ok {
		return nil, false, fmt.Errorf("unknown provider configuration key %q", key)
	}
	providers, err := ReadProviders()
	if err != nil {
		return nil, false, err
	}
	provider, exists := providers[parts[1]]
	if !exists {
		return nil, false, fmt.Errorf("provider %q is not defined", parts[1])
	}
	if modelName != "" {
		model, exists := provider.Models[modelName]
		if !exists {
			return nil, false, fmt.Errorf("model %q is not configured under provider %q", modelName, parts[1])
		}
		spec := bindModel(provider, modelName)
		switch field {
		case "model_id":
			return spec.APIModel(), true, nil
		case "type":
			return spec.Provider.Type, true, nil
		case "base_url":
			return spec.Provider.BaseURL, true, nil
		case "context_window":
			return ContextWindowFor(spec), true, nil
		case "max_output_tokens":
			return MaxOutputTokensFor(spec), true, nil
		case "thinking":
			return spec.ThinkingLevel(), true, nil
		case "price":
			return model.Price, true, nil
		case "price.input":
			return model.Price.Input, true, nil
		case "price.output":
			return model.Price.Output, true, nil
		case "price.cache_read":
			return model.Price.CacheRead, true, nil
		case "price.cache_write":
			return model.Price.CacheWrite, true, nil
		}
	}
	if nested {
		if strings.HasPrefix(field, "retry.") {
			if field == "retry.max_attempts" {
				return provider.Retry.MaxAttempts, true, nil
			}
			if field == "retry.base_delay" {
				return provider.Retry.BaseDelay, true, nil
			}
			if field == "retry.max_delay" {
				return provider.Retry.MaxDelay, true, nil
			}
			return provider.Retry.ResetAfter, true, nil
		}
		if field == "timeouts.request" {
			return provider.RequestTimeout, true, nil
		}
		return provider.ResponseHeaderTimeout, true, nil
	}
	switch field {
	case "type":
		return provider.Type, true, nil
	case "base_url":
		return provider.BaseURL, true, nil
	case "api_key":
		return provider.APIKey, true, nil
	case "headers":
		return provider.Headers, true, nil
	case "context_window":
		return provider.ContextWindow, true, nil
	case "max_output_tokens":
		return provider.MaxOutputTokens, true, nil
	case "thinking":
		return provider.Thinking, true, nil
	case "prompt_cache":
		return provider.PromptCache, true, nil
	case "compat":
		return provider.Compat, true, nil
	default:
		return nil, false, fmt.Errorf("unknown provider configuration key %q", key)
	}
}

func matchProviderConfigKey(key string) (ConfigKey, bool) {
	parts, modelName, field, nested, ok := parseProviderKey(key)
	if !ok || !configIdentifierPattern.MatchString(parts[1]) {
		return ConfigKey{}, false
	}
	if modelName != "" {
		if descriptor, exists := providerModelNestedDescriptors[field]; exists {
			return descriptor, true
		}
		descriptor, exists := providerModelKeyDescriptors[field]
		return descriptor, exists
	}
	if nested {
		descriptor, exists := providerNestedDescriptors[field]
		return descriptor, exists
	}
	descriptor, exists := providerKeyDescriptors[field]
	return descriptor, exists
}

func parseProviderKey(key string) (parts []string, modelName, field string, nested, ok bool) {
	parts = strings.Split(key, ".")
	if len(parts) < 3 || parts[0] != "providers" || parts[1] == "" || !configIdentifierPattern.MatchString(parts[1]) {
		return nil, "", "", false, false
	}
	if parts[2] == "models" {
		if len(parts) < 5 {
			return nil, "", "", false, false
		}
		field = parts[len(parts)-1]
		modelEnd := len(parts) - 1
		if len(parts) >= 6 && parts[len(parts)-2] == "price" {
			field = "price." + field
			modelEnd = len(parts) - 2
		}
		modelName = strings.Join(parts[3:modelEnd], ".")
		if modelName == "" || !modelIdentifierPattern.MatchString(modelName) {
			return nil, "", "", false, false
		}
		return parts, modelName, field, false, true
	}
	if len(parts) == 4 && (parts[2] == "retry" || parts[2] == "timeouts") {
		return parts, "", parts[2] + "." + parts[3], true, true
	}
	if len(parts) != 3 {
		return nil, "", "", false, false
	}
	return parts, "", parts[2], false, true
}
