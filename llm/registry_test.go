package llm

import (
	"testing"
)

func TestRegistry_ResolveMulti(t *testing.T) {
	r := NewRegistry()

	p1 := &mockProvider{
		id: "p1",
		models: []Model{
			{ID: "gpt-4o"},
		},
	}
	p2 := &mockProvider{
		id: "p2",
		models: []Model{
			{ID: "gpt-4o"},
		},
	}

	r.Register(p1)
	r.Register(p2)

	p, err := r.ResolveModel("gpt-4o")
	if err != nil {
		t.Fatalf("failed to resolve model: %v", err)
	}

	// Should be a SmartResolver because there are 2 providers
	smart, ok := p.(*SmartResolver)
	if !ok {
		t.Errorf("expected *SmartResolver, got %T", p)
	}

	if len(smart.providers) != 2 {
		t.Errorf("expected 2 providers in SmartResolver, got %d", len(smart.providers))
	}
}
