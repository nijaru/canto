package safety

import (
	"context"

	"github.com/nijaru/canto/approval"
)

// Mode defines the execution mode of the agent.
type Mode string

const (
	// ModeRead allows only read-only operations.
	ModeRead Mode = "read"
	// ModeEdit requires approval for write and execute operations.
	ModeEdit Mode = "edit"
	// ModeAuto allows all operations without approval.
	ModeAuto Mode = "auto"
)

// Category defines the type of operation a tool performs.
type Category string

const (
	CategoryRead    Category = "read"
	CategoryWrite   Category = "write"
	CategoryExecute Category = "execute"
)

// Policy is an implementation of approval.Policy that enforces safety modes.
type Policy struct {
	mode Mode
}

// NewPolicy creates a new safety policy with the given mode.
func NewPolicy(mode Mode) *Policy {
	return &Policy{mode: mode}
}

// Decide implements approval.Policy.
func (p *Policy) Decide(ctx context.Context, req approval.Request) (approval.Result, bool, error) {
	switch p.mode {
	case ModeAuto:
		return approval.Result{
			Decision: approval.DecisionAllow,
			Reason:   "Auto mode enabled",
		}, true, nil
	case ModeRead:
		if Category(req.Category) == CategoryRead {
			return approval.Result{
				Decision: approval.DecisionAllow,
				Reason:   "Read operation allowed in read mode",
			}, true, nil
		}
		return approval.Result{
			Decision: approval.DecisionDeny,
			Reason:   "Only read operations allowed in read mode",
		}, true, nil
	case ModeEdit:
		if Category(req.Category) == CategoryRead {
			return approval.Result{
				Decision: approval.DecisionAllow,
				Reason:   "Read operation allowed in edit mode",
			}, true, nil
		}
		// Write and execute operations require manual approval (not handled by this policy automatically).
		return approval.Result{}, false, nil
	default:
		return approval.Result{
			Decision: approval.DecisionDeny,
			Reason:   "Unknown safety mode",
		}, true, nil
	}
}
