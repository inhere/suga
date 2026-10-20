package acpserver

import (
	"context"
	"errors"

	"github.com/coder/acp-go-sdk"
)

// configOptionID selects which of the two config options a
// session/set_config_option request addresses (design 2026-10-20).
const (
	configOptionModel = "model"
	configOptionMode  = "mode"
)

// ModelOption is one selectable main model of the configuration: its canonical
// provider/model ref (the value ACP clients pick) and the pieces the response
// shows beside it.
type ModelOption struct {
	Ref  string // provider/model, as chat's :model accepts it
	Name string // human label: the configured model id
	// Thinking is the model's effective thinking level ("" = provider default).
	Thinking string
}

// modeValues are the two session modes the mode select offers; the same ids
// session/set_mode accepts.
var modeValues = []ModelOption{
	{Ref: "default", Name: "Default"},
	{Ref: "read_only", Name: "Read only"},
}

// modelOption renders one acp select out of the session's option values.
func modelOption(id, name, current string, values []ModelOption, category acp.SessionConfigOptionCategory) acp.SessionConfigOption {
	opts := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(values))
	for _, m := range values {
		opts = append(opts, acp.SessionConfigSelectOption{Name: m.Name, Value: acp.SessionConfigValueId(m.Ref)})
	}
	return acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
		Id:           acp.SessionConfigId(id),
		Name:         name,
		Type:         "select",
		Category:     &category,
		CurrentValue: acp.SessionConfigValueId(current),
		Options:      acp.SessionConfigSelectOptions{Ungrouped: &opts},
	}}
}

// sessionConfigOptions renders the two selects every new/load/resume response
// carries and session/set_config_option answers with (design 2026-10-20): the
// models of the configuration (category model, the current value is the
// session's model) and the session's mode (category mode). A runtime with no
// configured models (test runtimes, empty catalog) offers the mode only.
func sessionConfigOptions(rt *Runtime, currentModel string, mode acp.SessionModeId) []acp.SessionConfigOption {
	options := make([]acp.SessionConfigOption, 0, 2)
	if rt != nil && len(rt.Models) > 0 {
		options = append(options, modelOption(configOptionModel, "Model", currentModel, rt.Models, acp.SessionConfigOptionCategoryModel))
	}
	options = append(options, modelOption(configOptionMode, "Mode", string(mode), modeValues, acp.SessionConfigOptionCategoryMode))
	return options
}

// SetSessionConfigOption applies one select's value to the session: the model
// select switches the main model for the next turn (refused while a turn
// runs), the mode select is session/set_mode under another id. Both return the
// full, up-to-date option list.
func (a *Agent) SetSessionConfigOption(ctx context.Context, req acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	// Only the ValueId variant carries the configId and session a select needs;
	// the (unstable) boolean variant has no option of ours.
	if req.ValueId == nil {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"configId": "unsupported; this session offers the model and mode selects"})
	}
	sessionID := req.ValueId.SessionId
	a.mu.Lock()
	state := a.sessions[sessionID]
	a.mu.Unlock()
	if state == nil {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": sessionID})
	}
	if state.cancel != nil {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"sessionId": sessionID, "reason": "prompt active"})
	}
	// One opening of the runtime answers and applies: it carries the configured
	// model catalog and the wiring switch. Its gate is nil, so it must fail
	// closed — a config change starts no turn and runs no tool.
	runtime, err := a.open(ctx, state.cwd, true, nil, nil)
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, err
	}
	var applyErr error
	switch req.ValueId.ConfigId {
	case configOptionModel:
		if runtime.SwitchModel == nil {
			applyErr = errors.New("model switching is not wired in this runtime")
			break
		}
		if _, err := runtime.SwitchModel(ctx, string(sessionID), string(req.ValueId.Value)); err != nil {
			applyErr = err
		}
	case configOptionMode:
		if req.ValueId.Value != "default" && req.ValueId.Value != "read_only" {
			applyErr = acp.NewInvalidParams(map[string]any{"value": req.ValueId.Value, "reason": "unknown session mode"})
			break
		}
		a.mu.Lock()
		if state.mode == "" {
			// Nothing should reach an empty mode; normalize to the default.
			state.mode = "default"
		}
		state.mode = acp.SessionModeId(req.ValueId.Value)
		a.mu.Unlock()
	default:
		applyErr = acp.NewInvalidParams(map[string]any{"configId": req.ValueId.ConfigId, "reason": "unknown config option"})
	}
	if applyErr != nil {
		return acp.SetSessionConfigOptionResponse{}, refuseWith(applyErr, runtime.close())
	}
	options := sessionConfigOptions(&runtime, currentModelOf(&runtime), a.sessionMode(sessionID))
	closeErr := runtime.close()
	return acp.SetSessionConfigOptionResponse{ConfigOptions: options}, closeErr
}

// sessionMode reads back a session's current mode (default when unknown).
func (a *Agent) sessionMode(id acp.SessionId) acp.SessionModeId {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[id]
	if state == nil || state.mode == "" {
		return "default"
	}
	return state.mode
}

// currentModelOf reads the ref of the model a turn of the runtime uses; a
// runtime built without the wiring hook (tests) only offers the mode select.
func currentModelOf(rt *Runtime) string {
	if rt == nil || rt.CurrentModel == nil {
		return ""
	}
	return rt.CurrentModel()
}
