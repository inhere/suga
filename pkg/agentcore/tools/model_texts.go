package tools

// ModelTexts is the model-facing wording the tool stack appends to results and
// hints, for the host to inject by language (i18n phase 1b). The zero value is
// the historical Chinese text, so a run that injects nothing — and a run with
// language: zh — keeps its model requests byte-identical; a host resolving
// language: en fills the fields from its catalog
// (internal/i18n/catalog/*/model.ini, see internal/wiring).
//
// The values carry the format verbs of their line, which a translation must
// keep: the layer formats the line with fmt.Sprintf. Names of tools,
// arguments and ref markers are part of the model's protocol and stay as is.
type ModelTexts struct {
	// ApprovedNote ends the result of a call a person approved right now
	// (ApprovedResultNote when empty).
	ApprovedNote string
	// OutputSavedRef heads an oversized result stored under a ref:
	// %d bytes, %d lines, %s ref.
	OutputSavedRef string
	// OutputCondensed opens the note replacing a result by its tidied text:
	// %d bytes, %d lines.
	OutputCondensed string
	// OutputCondensedRef continues that note with the ref of the original: %s ref.
	OutputCondensedRef string
}

// The historical Chinese wording, the default of every ModelTexts field.
const (
	defaultOutputSavedRef     = "[输出 %d 字节 / %d 行已保存为 ref %s；先用 session_search(query, ref) 定位相关行，再用 recall(ref, offset, limit) 读附近窗口，不要逐页读完]"
	defaultOutputCondensed    = "[已整理输出：原文 %d 字节 / %d 行"
	defaultOutputCondensedRef = "已保存为 ref %s，需要细节时用 session_search(query, ref) 或 recall(ref, offset, limit)"
)

// DefaultModelTexts returns the historical Chinese wording.
func DefaultModelTexts() ModelTexts {
	return ModelTexts{
		ApprovedNote:       ApprovedResultNote,
		OutputSavedRef:     defaultOutputSavedRef,
		OutputCondensed:    defaultOutputCondensed,
		OutputCondensedRef: defaultOutputCondensedRef,
	}
}

// resolve fills the empty fields with the Chinese defaults, so a partially
// injected set never sends an empty note or an empty header line.
func (m ModelTexts) resolve() ModelTexts {
	defaults := DefaultModelTexts()
	if m.ApprovedNote == "" {
		m.ApprovedNote = defaults.ApprovedNote
	}
	if m.OutputSavedRef == "" {
		m.OutputSavedRef = defaults.OutputSavedRef
	}
	if m.OutputCondensed == "" {
		m.OutputCondensed = defaults.OutputCondensed
	}
	if m.OutputCondensedRef == "" {
		m.OutputCondensedRef = defaults.OutputCondensedRef
	}
	return m
}
