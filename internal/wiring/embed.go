package wiring

// Accessors for host programs that embed suga in their own process
// (pkg/sugasdk): the same memory backends, governance and write policy the
// agent runtime uses, built without assembling a full runtime.

import (
	"context"

	"github.com/inhere/suga/internal/personal/store"
	"github.com/inhere/suga/pkg/agentcore/memory"
)

// OpenEmbeddedMemory builds the governed memory store of workspaceRoot: the
// user and project backends the agent runtime selects (configuration, MEMORY.md,
// .gofer/tracker), wrapped with the configured write policy
// (memory.write / memory.write_tainted) and the host's confirmation.
//
// repo backs the provenance records and the derived index and must stay open
// while the store is used. tainted reports whether the writing session has seen
// external content (nil: never); confirm approves such a write (nil: refuse,
// fail closed). The workspace must not be empty: the project scope is derived
// from it.
func OpenEmbeddedMemory(ctx context.Context, workspaceRoot string, repo *store.Store, tainted func(context.Context) bool,
	confirm MemoryConfirmFunc) (*memory.Governed, error) {
	cfg, err := loadMemoryConfig()
	if err != nil {
		return nil, err
	}
	setup, err := buildMemory(workspaceRoot, cfg)
	if err != nil {
		return nil, err
	}
	policy, err := loadMemoryWritePolicy()
	if err != nil {
		return nil, err
	}
	// The index wrappers of the files backend read and write derived rows in the
	// session database, so they are bound before the store is handed out.
	setup.Bind(repo)
	host := &memoryHost{}
	host.bind(nil, repo)
	p := memory.Policy{
		Tainted:    tainted,
		Provenance: host,
		// The host writes on behalf of itself, not of a turn: nothing to record.
		Observe: func(context.Context, memory.Event) {},
	}
	if c := memoryTaintedConfirm(policy.Tainted, confirm); c != nil {
		p.Confirm = c
	}
	return memory.NewGoverned(setup.Router, p), nil
}
