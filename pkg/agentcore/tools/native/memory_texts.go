package native

// MemoryTexts is the model-facing wording of the memory tool's refusals and
// errors, for the host to inject by language (i18n phase 1b). The zero value
// is the historical Chinese text, so a run that injects nothing — and a run
// with language: zh — keeps its model requests byte-identical; a host
// resolving language: en fills the fields from its catalog
// (internal/i18n/catalog/*/model.ini, see internal/wiring).
//
// The values keep the format verbs of their line, which are what the tool
// formats with fmt.Errorf. Actions (add/replace/remove/get/search) and scopes
// are part of the call shape and stay untranslated.
type MemoryTexts struct {
	// ReadOnly refuses a write in a read-only run: %s action.
	ReadOnly string
	// NeedsConfirmation refuses a write of a tainted session that cannot ask.
	NeedsConfirmation string
	// Denied is a write whose confirmation was not given: %v cause.
	Denied string
	// Tombstoned is a write equal to a removed entry: %v cause.
	Tombstoned string
	// OverBudget is a full index: %s scope, %v cause.
	OverBudget string
	// NotFound is a missing id: %s scope, %q id.
	NotFound string
	// NoBackend is a scope without a backend: %s scope.
	NoBackend string
	// Invalid is a bad request: %v cause.
	Invalid string
	// Failed wraps any other store error: %s action, %s scope, %w cause.
	Failed string
}

// The historical Chinese wording, the default of every MemoryTexts field.
const (
	defaultMemoryReadOnly          = "memory: 只读模式下不能写入记忆（%s 被拒绝）；get/search 仍可使用"
	defaultMemoryNeedsConfirmation = "memory: 会话读过外部内容，非交互模式不能写记忆（需要用户确认的写入被拒绝）"
	defaultMemoryDenied            = "memory: 会话读过外部内容，写入需要确认，未获同意：%v"
	defaultMemoryTombstoned        = "memory: 该内容与之前删除的条目相同，重新写入前先征得用户确认（%v）"
	defaultMemoryOverBudget        = "memory: %s 记忆索引超出上限，先用 replace 合并或用 remove 删除旧条目再写入（%v）"
	defaultMemoryNotFound          = "memory: %s 中没有条目 %q；用 action=search 查找 id"
	defaultMemoryNoBackend         = "memory: 没有为 %s 配置记忆后端"
	defaultMemoryInvalid           = "memory: 参数无效：%v"
	defaultMemoryFailed            = "memory: %s %s 失败：%w"
)

// DefaultMemoryTexts returns the historical Chinese wording.
func DefaultMemoryTexts() MemoryTexts {
	return MemoryTexts{
		ReadOnly:          defaultMemoryReadOnly,
		NeedsConfirmation: defaultMemoryNeedsConfirmation,
		Denied:            defaultMemoryDenied,
		Tombstoned:        defaultMemoryTombstoned,
		OverBudget:        defaultMemoryOverBudget,
		NotFound:          defaultMemoryNotFound,
		NoBackend:         defaultMemoryNoBackend,
		Invalid:           defaultMemoryInvalid,
		Failed:            defaultMemoryFailed,
	}
}

// resolve fills the empty fields with the Chinese defaults, so a partially
// injected set never sends a message with no wording at all.
func (m MemoryTexts) resolve() MemoryTexts {
	defaults := DefaultMemoryTexts()
	if m.ReadOnly == "" {
		m.ReadOnly = defaults.ReadOnly
	}
	if m.NeedsConfirmation == "" {
		m.NeedsConfirmation = defaults.NeedsConfirmation
	}
	if m.Denied == "" {
		m.Denied = defaults.Denied
	}
	if m.Tombstoned == "" {
		m.Tombstoned = defaults.Tombstoned
	}
	if m.OverBudget == "" {
		m.OverBudget = defaults.OverBudget
	}
	if m.NotFound == "" {
		m.NotFound = defaults.NotFound
	}
	if m.NoBackend == "" {
		m.NoBackend = defaults.NoBackend
	}
	if m.Invalid == "" {
		m.Invalid = defaults.Invalid
	}
	if m.Failed == "" {
		m.Failed = defaults.Failed
	}
	return m
}
