package axconfig

import (
	"sort"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// protectedClassification is the explicit classification of every registry key:
// the fixed descriptors plus the four dynamic provider/model tables. The value
// is the group the key belongs to; the first six groups are the protected ones
// (see protectedGroups). A key that is missing here, or whose group disagrees
// with its Protected field, fails TestProtectedClassification - the table is
// what keeps a newly added key from silently escaping the startup comparison.
var protectedClassification = map[string]string{
	// display: what the UI shows and how it behaves; it cannot widen access.
	"language":              "display",
	"chat.markdown":         "display",
	"chat.spinner":          "display",
	"chat.agent_progress":   "display",
	"chat.highlight_input":  "display",
	"chat.steering":         "display",
	"chat.terminal_title":   "display",
	"chat.status_metrics":   "display",
	"chat.diff":             "display",
	"chat.diff_lines":       "display",
	"chat.code_highlight":   "display",
	"chat.code_max_lines":   "display",
	"chat.undo_confirm":     "display",
	"chat.notify":           "display",
	"chat.notify_after":     "display",
	"chat.win32_input_mode": "display",
	"chat.theme":            "display",
	"chat.colors":           "display",

	// cost: spending and step budgets; they bound a run, they do not widen it.
	"budget.tokens":          "cost",
	"budget.cost_usd":        "cost",
	"budget.scope":           "cost",
	"budget.estimate":        "cost",
	"budget.estimate_factor": "cost",
	"budget.notice_ratio":    "cost",
	"max_steps":              "cost",
	"agents.max_steps":       "cost",
	"agents.max_tokens":      "cost",
	"agents.timeout":         "cost",
	"tools.loop_guard":       "cost",

	// retention: how long data survives.
	"gc.retention.interactive":          "retention",
	"gc.retention.noninteractive":       "retention",
	"transcripts":                       "retention",
	"chat.history":                      "retention",
	"chat.history_size":                 "retention",
	"chat.history_scope":                "retention",
	"tools.snapshots.enabled":           "retention",
	"tools.snapshots.max_file_bytes":    "retention",
	"tools.snapshots.max_session_bytes": "retention",
	"tools.snapshots.max_global_bytes":  "retention",
	"tools.snapshots.keep_turns":        "retention",
	"tools.snapshots.keep_days":         "retention",
	"tools.snapshots.on_error":          "retention",

	// model_choice: picks among providers that are already configured.
	"model":           "model_choice",
	"compact_model":   "model_choice",
	"fallback_models": "model_choice",
	"models.compact":  "model_choice",
	"models.idle":     "model_choice",
	"models.handoff":  "model_choice",
	"agents.model":    "model_choice",

	// runtime: timeouts, retries, output and context sizes of an existing run.
	"store.body_inline_bytes":        "runtime",
	"run.deadline_env":               "runtime",
	"network.proxy.sites":            "runtime",
	"retry.max_attempts":             "runtime",
	"retry.base_delay":               "runtime",
	"retry.max_delay":                "runtime",
	"retry.reset_after":              "runtime",
	"retry.max_elapsed":              "runtime",
	"timeouts.request":               "runtime",
	"timeouts.response_header":       "runtime",
	"timeouts.stream_idle":           "runtime",
	"context.idle_after":             "runtime",
	"context.idle_min_ratio":         "runtime",
	"context.fold_every":             "runtime",
	"context.fold_keep":              "runtime",
	"context.fold_at":                "runtime",
	"context.pin_first_user":         "runtime",
	"context.keep_user_words_budget": "runtime",
	"context.summary_max_tokens":     "runtime",
	"context.handoff_reuse_prefix":   "runtime",
	"images.history_turns":           "runtime",
	"images.max_request_count":       "runtime",
	"images.max_request_bytes":       "runtime",
	"images.max_side":                "runtime",
	"tasks.wake_on_exit":             "runtime",
	"mcp.call_timeout_sec":           "runtime",
	"mcp.startup_timeout_sec":        "runtime",
	"tools.max_output_bytes":         "runtime",
	"tools.max_spill_bytes":          "runtime",
	"tools.spill.max_bytes":          "runtime",
	"tools.spill.max_age":            "runtime",
	"tools.spill.compress_after":     "runtime",
	"tools.spill.compression":        "runtime",
	"tools.spill.on_quota":           "runtime",
	"tools.spill.keep_days":          "runtime",
	"tools.read_page_bytes":          "runtime",
	"tools.parallel_reads":           "runtime",
	"tools.minimize_output":          "runtime",
	"tools.shell_background_after":   "runtime",
	"memory.user.timeout_sec":        "runtime",
	"memory.project.timeout_sec":     "runtime",
	"hooks.timeout_ms":               "runtime",
	"hooks.stop_wait_sec":            "runtime",
	"hooks.stop_background":          "runtime",

	// tool_pref: tool and skill preferences inside the current tool set.
	"tools.preferred":            "tool_pref",
	"tools.deferred":             "tool_pref",
	"tools.deferred_native":      "tool_pref",
	"tools.deferred_apply":       "tool_pref",
	"tools.web_fetch.user_agent": "tool_pref",
	"skills.list_max":            "tool_pref",
	"skills.always_load":         "tool_pref",
	"agents.enabled":             "tool_pref",
	"agents.nudge":               "tool_pref",

	// log: everything about logging except log.file, which chooses the path.
	"log.level":    "log",
	"log.stderr":   "log",
	"log.max_size": "log",
	"log.keep":     "log",
	"log.compress": "log",

	// secret: credentials; they never leave the config except masked.
	"serpapi_key":            "secret",
	"providers.<id>.api_key": "secret",
	"providers.<id>.headers": "secret",

	// provider_tuning: per-provider and per-model knobs that do not change where
	// a request goes.
	"providers.<id>.context_window":                  "provider_tuning",
	"providers.<id>.max_output_tokens":               "provider_tuning",
	"providers.<id>.thinking":                        "provider_tuning",
	"providers.<id>.thinking_style":                  "provider_tuning",
	"providers.<id>.stream_required":                 "provider_tuning",
	"providers.<id>.vision":                          "provider_tuning",
	"providers.<id>.prompt_cache":                    "provider_tuning",
	"providers.<id>.prompt_cache_retention":          "provider_tuning",
	"providers.<id>.compat":                          "provider_tuning",
	"providers.<id>.retry.max_attempts":              "provider_tuning",
	"providers.<id>.retry.base_delay":                "provider_tuning",
	"providers.<id>.retry.max_delay":                 "provider_tuning",
	"providers.<id>.retry.reset_after":               "provider_tuning",
	"providers.<id>.retry.max_elapsed":               "provider_tuning",
	"providers.<id>.timeouts.request":                "provider_tuning",
	"providers.<id>.timeouts.response_header":        "provider_tuning",
	"providers.<id>.timeouts.stream_idle":            "provider_tuning",
	"providers.<id>.models.<name>.model_id":          "provider_tuning",
	"providers.<id>.models.<name>.context_window":    "provider_tuning",
	"providers.<id>.models.<name>.max_output_tokens": "provider_tuning",
	"providers.<id>.models.<name>.thinking":          "provider_tuning",
	"providers.<id>.models.<name>.thinking_style":    "provider_tuning",
	"providers.<id>.models.<name>.stream_required":   "provider_tuning",
	"providers.<id>.models.<name>.vision":            "provider_tuning",
	"providers.<id>.models.<name>.price":             "provider_tuning",
	"providers.<id>.models.<name>.price.input":       "provider_tuning",
	"providers.<id>.models.<name>.price.output":      "provider_tuning",
	"providers.<id>.models.<name>.price.cache_read":  "provider_tuning",
	"providers.<id>.models.<name>.price.cache_write": "provider_tuning",

	// exec: introduces an external program, instruction file or tool run.
	"hooks.command":             "exec",
	"hooks.stop_args":           "exec",
	"hooks.pre_tool_use.tools":  "exec",
	"hooks.pre_tool_use.name":   "exec",
	"hooks.post_tool_use.tools": "exec",
	"hooks.post_tool_use.name":  "exec",
	"memory.user.command":       "exec",
	"memory.user.args":          "exec",
	"memory.user.backend":       "exec",
	"memory.project.command":    "exec",
	"memory.project.args":       "exec",
	"memory.project.backend":    "exec",
	"mcp.servers":               "exec",
	"tools.shell":               "exec",
	"tools.interpreters":        "exec",
	"rules.file":                "exec",

	// relax: widens what the model may read, write or run, or turns the checks
	// that bound that off.
	"workspace":                                 "relax",
	"tools.allow_write":                         "relax",
	"tools.approval":                            "relax",
	"tools.auto_allow":                          "relax",
	"tools.read_only_allow":                     "relax",
	"tools.disabled":                            "relax",
	"tools.read_roots":                          "relax",
	"tools.dependency_roots":                    "relax",
	"tools.standards_roots":                     "relax",
	"tools.skill_roots":                         "relax",
	"tools.ask_on_external_origin":              "relax",
	"tools.protect_changed_files":               "relax",
	"tools.approval_rules.enabled":              "relax",
	"tools.approval_rules.always_prefix_tokens": "relax",
	"tools.approval_rules.persist_ask":          "relax",
	"tools.approval_rules.project_trust":        "relax",
	"chat.ask_outside_dirs":                     "relax",
	"memory.write":                              "relax",
	"memory.write_tainted":                      "relax",
	"memory.taint_tools":                        "relax",
	"memory.taint_on_spill":                     "relax",
	"tools.web_fetch.allow_private":             "relax",
	"tools.session_message.enabled":             "relax",
	"tools.session_message.in_gofer_jobs":       "relax",
	"tools.session_message.max_hops":            "relax",
	"tools.origin_judge.enabled":                "relax",
	"tools.origin_judge.timeout":                "relax",
	"tools.read_deny":                           "relax",
	"tools.read_deny_defaults":                  "relax",

	// exec_limit: the limits a shell or task may consume.
	"tools.shell_limits.memory":    "exec_limit",
	"tools.shell_limits.processes": "exec_limit",
	"tools.shell_limits.cpu_time":  "exec_limit",
	"tools.shell_limits.off":       "exec_limit",
	"tools.shell_timeout_max":      "exec_limit",
	"tools.task_timeout_max":       "exec_limit",
	"tasks.max_running":            "exec_limit",
	"tasks.max_duration":           "exec_limit",
	"tasks.max_output_bytes":       "exec_limit",

	// endpoint: where a request and its credentials are sent.
	"providers.<id>.base_url":               "endpoint",
	"providers.<id>.type":                   "endpoint",
	"providers.<id>.models.<name>.base_url": "endpoint",
	"providers.<id>.models.<name>.type":     "endpoint",
	"network.proxy.url":                     "endpoint",
	"tools.web_fetch.proxy":                 "endpoint",

	// judge: which model decides a flagged external-origin call.
	"models.judge": "judge",

	// other_protected: protected keys that fit none of the groups above.
	"log.file": "other_protected",
}

// protectedGroups are the classification groups whose keys must carry
// Protected: true. Everything else must leave it false.
var protectedGroups = map[string]bool{
	"exec":            true,
	"relax":           true,
	"exec_limit":      true,
	"endpoint":        true,
	"judge":           true,
	"other_protected": true,
}

// classificationGroups is the vocabulary the classification table may use.
var classificationGroups = map[string]bool{
	"exec":            true,
	"relax":           true,
	"exec_limit":      true,
	"endpoint":        true,
	"judge":           true,
	"other_protected": true,
	"cost":            true,
	"retention":       true,
	"model_choice":    true,
	"runtime":         true,
	"tool_pref":       true,
	"log":             true,
	"display":         true,
	"provider_tuning": true,
	"secret":          true,
}

// registryDescriptorsForClassification returns every descriptor the
// classification table has to cover: the fixed keys and the four dynamic
// provider/model tables.
func registryDescriptorsForClassification() []ConfigKey {
	out := append([]ConfigKey(nil), configKeyDescriptors...)
	for _, table := range []map[string]ConfigKey{
		providerKeyDescriptors, providerModelKeyDescriptors,
		providerModelNestedDescriptors, providerNestedDescriptors,
	} {
		for _, descriptor := range table {
			out = append(out, descriptor)
		}
	}
	return out
}

func TestProtectedClassification(t *testing.T) {
	t.Run("groups are known", func(t *testing.T) {
		patterns := make([]string, 0, len(protectedClassification))
		for pattern := range protectedClassification {
			patterns = append(patterns, pattern)
		}
		sort.Strings(patterns)
		for _, pattern := range patterns {
			group := protectedClassification[pattern]
			if !assert.True(t, classificationGroups[group]) {
				t.Logf("%s: unknown classification group %q", pattern, group)
			}
		}
	})

	t.Run("every registry key is classified", func(t *testing.T) {
		for _, descriptor := range registryDescriptorsForClassification() {
			_, ok := protectedClassification[descriptor.Pattern]
			if !assert.True(t, ok) {
				t.Logf("%s: missing from the explicit classification table", descriptor.Pattern)
			}
		}
	})

	t.Run("classification matches the Protected field", func(t *testing.T) {
		for _, descriptor := range registryDescriptorsForClassification() {
			group, ok := protectedClassification[descriptor.Pattern]
			if !ok {
				// Reported by the subtest above; nothing to compare here.
				continue
			}
			if !assert.Eq(t, protectedGroups[group], descriptor.Protected) {
				t.Logf("%s: classified as %q (protected group: %v) but Protected=%v",
					descriptor.Pattern, group, protectedGroups[group], descriptor.Protected)
			}
		}
	})

	t.Run("classified keys exist in the registry", func(t *testing.T) {
		registered := make(map[string]bool, len(protectedClassification))
		for _, descriptor := range registryDescriptorsForClassification() {
			registered[descriptor.Pattern] = true
		}
		patterns := make([]string, 0, len(protectedClassification))
		for pattern := range protectedClassification {
			patterns = append(patterns, pattern)
		}
		sort.Strings(patterns)
		for _, pattern := range patterns {
			if !assert.True(t, registered[pattern]) {
				t.Logf("%s: classified but not in the registry (stale table entry)", pattern)
			}
		}
	})

	t.Run("ProtectedConfigKeys lists the protected descriptors", func(t *testing.T) {
		want := map[string]ConfigKey{}
		for _, descriptor := range registryDescriptorsForClassification() {
			if descriptor.Protected {
				want[descriptor.Pattern] = descriptor
			}
		}
		got := ProtectedConfigKeys()
		assert.Eq(t, len(want), len(got))
		assert.True(t, sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Pattern < got[j].Pattern }))
		for _, descriptor := range got {
			if !assert.True(t, descriptor.Protected) {
				t.Logf("%s: returned by ProtectedConfigKeys but not Protected", descriptor.Pattern)
			}
			expected, ok := want[descriptor.Pattern]
			assert.True(t, ok)
			assert.Eq(t, expected, descriptor)
		}
		// The two pre-tool-use keys are comparison-only: protected, not writable.
		byPattern := map[string]ConfigKey{}
		for _, descriptor := range got {
			byPattern[descriptor.Pattern] = descriptor
		}
		for _, pattern := range []string{"hooks.pre_tool_use.tools", "hooks.pre_tool_use.name"} {
			descriptor, ok := byPattern[pattern]
			if !assert.True(t, ok) {
				t.Logf("%s: missing from ProtectedConfigKeys", pattern)
				continue
			}
			assert.False(t, descriptor.Writable)
		}
	})
}
