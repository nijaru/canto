package approval

import (
	"context"
	"errors"
	"fmt"

	"github.com/nijaru/canto/session"
)

type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
)

var (
	ErrRequestNotFound = errors.New("approval request not found")
	ErrRequestResolved = errors.New("approval request already resolved")
	ErrInvalidDecision = errors.New("invalid approval decision")
)

type Requirement struct {
	Category  string
	Operation string
	Resource  string
	Metadata  map[string]any
}

type Request struct {
	ID        string
	SessionID string
	Tool      string
	Args      string
	Category  string
	Operation string
	Resource  string
	Metadata  map[string]any
}

type Result struct {
	RequestID string
	Decision  Decision
	Reason    string
	Automated bool // true if decided by policy, false if resolved via Resolve (HITL)
}

type Policy interface {
	Decide(ctx context.Context, sess *session.Session, req Request) (Result, bool, error)
}

func (r Result) Allowed() bool {
	return r.Decision == DecisionAllow
}

func (r Result) Error() error {
	if r.Decision == DecisionDeny {
		if r.Reason == "" {
			return fmt.Errorf("approval denied")
		}
		return fmt.Errorf("approval denied: %s", r.Reason)
	}
	return nil
}
