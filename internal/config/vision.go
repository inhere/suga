package axconfig

import "strings"

// Vision capability of a model (design 0.3, "vision 三态"): the model accepts
// images in a request. It is declared per model and per provider in the same
// three-state way as stream_required:
//
//   - providers.<id>.models.<model>.vision written -> that value;
//   - providers.<id>.vision written -> that value;
//   - neither written -> inferred from the model id sent to the provider
//     (InferVision), so a new model of a known family works without config and a
//     gateway serving it over another protocol can still be pinned to false.
//
// The capability decides two things: whether view_image is in the tool table of
// the current model, and whether a request carries image parts at all (a
// fallback model without vision gets a text note instead, see llm.NoImages).
func VisionFor(spec ModelSpec) bool {
	if cfg, ok := spec.Provider.Models[spec.Model]; ok && cfg.VisionSet {
		return cfg.Vision
	}
	if spec.Provider.VisionSet {
		return spec.Provider.Vision
	}
	return InferVision(spec.APIModel())
}

// visionExcluded lists the model families known not to accept images, checked
// before the enabling list so an id such as "deepseek-gpt-4o-lookalike" stays
// text-only.
var visionExcluded = []string{"deepseek", "glm", "qwen", "ernie", "hunyuan", "yi-", "kimi", "moonshot"}

// visionEnabled lists the model ids that accept images: Claude 3 and later,
// gpt-4o/4.1/4-turbo/5/6, Gemini.
var visionEnabled = []string{
	"claude-3", "claude-4", "claude-5", "claude-6",
	"gpt-4o", "gpt-4.1", "gpt-4-turbo", "gpt-5", "gpt-6", "chatgpt-4o",
	"gemini",
}

// InferVision guesses whether a model id sent to the provider accepts images.
// The vendor prefix of a gateway id ("openai/gpt-4o") is dropped first, so the
// same family is recognized through every gateway. An unknown id counts as
// text-only: sending image parts to a model that cannot read them fails the
// whole request, while a text note only costs one line.
func InferVision(apiModel string) bool {
	name := strings.ToLower(strings.TrimSpace(apiModel))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return false
	}
	for _, excluded := range visionExcluded {
		if strings.Contains(name, excluded) {
			return false
		}
	}
	if strings.HasPrefix(name, "claude") {
		// Claude 3 and later see images; the long retired 1/2 and instant
		// models do not.
		return !strings.HasPrefix(name, "claude-1") && !strings.HasPrefix(name, "claude-2") &&
			!strings.Contains(name, "instant")
	}
	for _, enabled := range visionEnabled {
		if strings.HasPrefix(name, enabled) {
			return true
		}
	}
	return false
}
