package tools

import "sync/atomic"

// VisionGate is the live "the current model can see images" switch of one
// runtime. The tool set is built once, but chat can switch the main model
// between turns (:model) and the runtime lists the tools at every step, so the
// capability cannot be baked into the tool instances: the host updates the gate
// on a model switch and the observers/tools consult it per call.
//
// A nil gate means "can see": a host that does not track the capability (tests,
// the examples) keeps the previous behavior.
type VisionGate struct {
	noVision atomic.Bool
}

// NewVisionGate returns a gate for a model with the given vision capability.
func NewVisionGate(vision bool) *VisionGate {
	g := &VisionGate{}
	g.SetVision(vision)
	return g
}

// SetVision records whether the current model accepts images.
func (g *VisionGate) SetVision(vision bool) {
	if g == nil {
		return
	}
	g.noVision.Store(!vision)
}

// CanSee reports whether the current model accepts images (a nil gate: yes).
func (g *VisionGate) CanSee() bool {
	return g == nil || !g.noVision.Load()
}
