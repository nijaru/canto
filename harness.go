package canto

import (
	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/coding"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/workspace"
)

// Environment groups host-provided capabilities that tools and bootstrap
// logic can use while a harness runs. It describes where effects happen; it
// does not encode product policy.
type Environment struct {
	Workspace workspace.WorkspaceFS
	Executor  *coding.Executor
	Sandbox   safety.Sandbox
	Secrets   safety.SecretInjector
	Bootstrap []session.ContextEntry
}

// Harness is an assembled agent runtime, registry, and session store. It is
// the root facade for host applications; lower-level packages remain available
// for advanced composition.
type Harness struct {
	Agent       agent.Agent
	Runner      *runtime.Runner
	Tools       *tool.Registry
	Store       session.Store
	Environment Environment
	ownsStore   bool
}

// Session returns a handle for one durable conversation.
func (h *Harness) Session(id string) *Session {
	return &Session{harness: h, id: id}
}

// Close releases resources owned by the harness.
func (h *Harness) Close() error {
	if h == nil {
		return nil
	}
	if h.Runner != nil {
		h.Runner.Close()
	}
	if h.ownsStore {
		closer, ok := h.Store.(interface{ Close() error })
		if !ok {
			return nil
		}
		return closer.Close()
	}
	return nil
}
