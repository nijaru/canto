package approval

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nijaru/canto/session"
	"github.com/oklog/ulid/v2"
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
}

type Policy interface {
	Decide(ctx context.Context, req Request) (Result, bool, error)
}

type Manager struct {
	policy Policy

	mu      sync.Mutex
	pending map[string]pendingRequest
}

type pendingRequest struct {
	ch       chan Result
	resolved bool
}

func NewManager(policy Policy) *Manager {
	return &Manager{
		policy:  policy,
		pending: make(map[string]pendingRequest),
	}
}

func (m *Manager) Request(
	ctx context.Context,
	sess *session.Session,
	toolName string,
	args string,
	requirement Requirement,
) (Result, error) {
	if sess == nil {
		return Result{}, errors.New("approval request: session is required")
	}
	req := Request{
		ID:        ulid.Make().String(),
		SessionID: sess.ID(),
		Tool:      toolName,
		Args:      args,
		Category:  requirement.Category,
		Operation: requirement.Operation,
		Resource:  requirement.Resource,
		Metadata:  cloneMetadata(requirement.Metadata),
	}

	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.ApprovalRequested, req)); err != nil {
		return Result{}, err
	}

	if m.policy != nil {
		res, handled, err := m.policy.Decide(ctx, req)
		if err != nil {
			return Result{}, err
		}
		if handled {
			res.RequestID = req.ID
			if err := m.appendResolved(ctx, sess, res); err != nil {
				return Result{}, err
			}
			return res, nil
		}
	}

	ch := make(chan Result, 1)
	m.mu.Lock()
	m.pending[req.ID] = pendingRequest{ch: ch}
	m.mu.Unlock()

	select {
	case res := <-ch:
		if err := m.appendResolved(ctx, sess, res); err != nil {
			return Result{}, err
		}
		return res, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.pending, req.ID)
		m.mu.Unlock()
		_ = sess.Append(
			context.Background(),
			session.NewEvent(sess.ID(), session.ApprovalCanceled, map[string]any{
				"id":   req.ID,
				"tool": toolName,
			}),
		)
		return Result{}, ctx.Err()
	}
}

func (m *Manager) Resolve(requestID string, decision Decision, reason string) error {
	if decision != DecisionAllow && decision != DecisionDeny {
		return ErrInvalidDecision
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	pending, ok := m.pending[requestID]
	if !ok {
		return ErrRequestNotFound
	}
	if pending.resolved {
		return ErrRequestResolved
	}
	pending.resolved = true
	m.pending[requestID] = pending
	delete(m.pending, requestID)
	pending.ch <- Result{
		RequestID: requestID,
		Decision:  decision,
		Reason:    reason,
	}
	return nil
}

func (m *Manager) Pending() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	return ids
}

func (m *Manager) appendResolved(ctx context.Context, sess *session.Session, result Result) error {
	return sess.Append(ctx, session.NewEvent(sess.ID(), session.ApprovalResolved, map[string]any{
		"id":       result.RequestID,
		"decision": result.Decision,
		"reason":   result.Reason,
	}))
}

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
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
