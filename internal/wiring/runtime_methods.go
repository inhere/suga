package wiring

// Runtime methods for model switching, local commands and image reading, plus session model lookup.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/inhere/suga/internal/clipboardimage"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// ModelOptions returns the configured models in deterministic order.
func (rt *Runtime) ModelOptions() []ModelOption {
	if rt == nil {
		return nil
	}
	return append([]ModelOption(nil), rt.modelOptions...)
}

// SwitchModel builds the selected main provider, retaining the runtime's
// fallback pool, then asks the runner to persist and activate it for the next turn.
func (rt *Runtime) SwitchModel(ctx context.Context, sessionID, ref string) (ModelOption, error) {
	if rt == nil || rt.Runner == nil {
		return ModelOption{}, errors.New("personal runtime is nil")
	}
	ref = strings.TrimSpace(ref)
	spec, ok := rt.modelSpecs[ref]
	if !ok {
		refs := make([]string, 0, len(rt.modelOptions))
		for _, option := range rt.modelOptions {
			refs = append(refs, option.Ref())
		}
		return ModelOption{}, fmt.Errorf("model %q is not configured; choose one of: %s", ref, strings.Join(refs, ", "))
	}
	if rt.thinkingOverride != "" {
		spec.Thinking = rt.thinkingOverride
	}
	provider, err := buildMainProviderSpecsWithEstimate(spec, rt.fallbacks, estimateOptionsForBudget(rt.Budget.Tokens, rt.Budget.CostUSD, rt.Budget.Estimate, rt.Budget.EstimateFactor))
	if err != nil {
		return ModelOption{}, err
	}
	maxOutputTokens, err := appconfig.ResolveMaxOutputTokens(rt.maxOutputOverride, spec)
	if err != nil {
		return ModelOption{}, err
	}
	option := ModelOption{Provider: spec.Provider.ID, Model: spec.Model, Thinking: spec.ThinkingLevel(),
		ContextWindow: appconfig.ContextWindowFor(spec), MaxOutputTokens: maxOutputTokens}
	if err := rt.Runner.SwitchModel(ctx, sessionID, provider, option.Provider, option.Model, option.ContextWindow, maxOutputTokens); err != nil {
		return ModelOption{}, err
	}
	// The tool table follows the new model: view_image is listed only while the
	// current main model can see images.
	rt.vision.SetVision(appconfig.VisionFor(spec))
	rt.ContextWindow, rt.Thinking = option.ContextWindow, option.Thinking
	return option, nil
}

// LocalCommands is the runner of chat's local commands ("!cmd"/"!!cmd"): chat
// runs a line typed at the prompt through it. It is nil for a nil runtime.
func (rt *Runtime) LocalCommands() *native.LocalCommand {
	if rt == nil {
		return nil
	}
	return rt.local
}

// SessionAddDirs is the runtime's handle on the session's added directories
// (--add-dir): the chat front end lists and changes them through it after the
// session is open (ApplyAddDirs).
func (p *Runtime) SessionAddDirs() *SessionAddDirs {
	if p == nil {
		return nil
	}
	return p.sessionDirs
}

// Close releases the session store and tool providers.
func (p *Runtime) Close() error {
	if p == nil {
		return nil
	}
	return errors.Join(p.repo.Close(), p.capabilities.Close())
}

// ReadImage loads a picture for chat's :image / the Alt+V clipboard paste with
// the view_image rules (design 0.3): the same path resolution, input limits
// and scaling, so both sides hand the model identical pictures.
func (rt *Runtime) ReadImage(path string) (agentcore.ImagePart, string, error) {
	if rt == nil || rt.sessionDirs == nil {
		return agentcore.ImagePart{}, "", errors.New("读图在本进程不可用")
	}
	return ReadImageAt(rt.sessionDirs.Workspace(), path)
}

// ReadImageAt loads a picture with the rules of Runtime.ReadImage, resolving a
// relative path against workspace. `suga send --image` uses it with the sender's
// own working directory, so both ends check, scale and name a picture the same
// way (design send --image).
//
// The path is one the person typed (:image, run --image, send --image), not a
// model's tool call, so it is not confined to the workspace: its own directory
// is added as a read root. Every other view_image check (regular file only,
// input size, pixel and format limits) still applies.
func ReadImageAt(workspace, path string) (agentcore.ImagePart, string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workspace, abs)
	}
	data, name, err := native.ReadImageFile(workspace, []string{filepath.Dir(filepath.Clean(abs))}, nil, path)
	if err != nil {
		return agentcore.ImagePart{}, "", err
	}
	return native.PrepareImage(data, name, imageMaxSide())
}

// VisionGate exposes the live "the current model can see" gate: chat refuses
// :image / Alt+V while the current model cannot accept pictures.
func (rt *Runtime) VisionGate() *tools.VisionGate {
	return rt.vision
}

// PasteImageCallback wires chat's Alt+V (design 0.3): the platform clipboard
// reader plus the view_image pipeline (the same checks and scaling), so a
// paste and a :image path end up identical for the model.
func (rt *Runtime) PasteImageCallback() func() (agentcore.ImagePart, string, error) {
	if rt == nil || rt.sessionDirs == nil {
		return nil
	}
	read := clipboardimage.Reader()
	maxSide := imageMaxSide()
	return func() (agentcore.ImagePart, string, error) {
		data, name, err := read()
		if err != nil {
			return agentcore.ImagePart{}, "", err
		}
		return native.PrepareImage(data, name, maxSide)
	}
}

// SessionModelReference reads the last main model of a persisted session before
// runtime assembly. Chat uses it so --resume follows :model unless --model was
// explicitly supplied on the command line.
func SessionModelReference(ctx context.Context, sessionID string) (string, error) {
	dbPath, err := appconfig.ResolveCoreStorePath()
	if err != nil {
		return "", err
	}
	repo, err := store.Open(dbPath)
	if err != nil {
		return "", err
	}
	defer repo.Close()
	session, err := repo.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	provider, _ := session.Meta["provider"].(string)
	model, _ := session.Meta["model"].(string)
	if provider == "" || model == "" {
		return "", nil
	}
	return provider + "/" + model, nil
}
