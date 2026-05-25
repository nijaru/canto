package canto

import (
	"sync"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// Environment groups host-provided durable context for a harness. Capability
// tools stay opt-in through their concrete packages instead of being retained
// by the root facade.
type Environment struct {
	Bootstrap []session.ContextEntry
}

// Harness is an assembled agent runtime, registry, and session store. It is
// the root facade for host applications; lower-level packages remain available
// for advanced composition.
type Harness struct {
	Agent       agent.Agent
	Runner      *runtime.Runner
	Provider    llm.Provider
	Model       string
	Tools       *tool.Registry
	Store       session.Store
	Environment Environment
	ownsStore   bool
	mu          sync.Mutex
	sessions    map[string]*harnessSessionState
}

// Session returns a handle for one durable conversation.
func (h *Harness) Session(id string) *Session {
	return &Session{harness: h, id: id, state: h.sessionState(id)}
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

func (h *Harness) setModel(model string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.Model = model
	agent := h.Agent
	h.mu.Unlock()

	if setter, ok := agent.(interface{ SetModel(string) }); ok {
		setter.SetModel(model)
	}
}

func (h *Harness) sessionState(id string) *harnessSessionState {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = make(map[string]*harnessSessionState)
	}
	state := h.sessions[id]
	if state == nil {
		state = newHarnessSessionState(id)
		h.sessions[id] = state
	}
	return state
}
