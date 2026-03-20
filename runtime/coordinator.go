package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	ErrTicketNotFound = errors.New("coordination ticket not found")
	ErrLeaseExpired   = errors.New("coordination lease expired")
	ErrLeaseStale     = errors.New("coordination lease is stale")
)

const defaultLeaseTTL = 30 * time.Second

// Ticket identifies a queued session execution request.
type Ticket struct {
	RequestID  string
	SessionID  string
	Sequence   uint64
	EnqueuedAt time.Time
}

// Lease grants temporary authority to execute a queued request.
type Lease struct {
	Ticket     Ticket
	Attempt    uint32
	LeaseToken uint64
	GrantedAt  time.Time
	ExpiresAt  time.Time
}

// ResultStatus records the final disposition of a request attempt.
type ResultStatus string

const (
	ResultStatusCompleted ResultStatus = "completed"
	ResultStatusCanceled  ResultStatus = "canceled"
	ResultStatusFailed    ResultStatus = "failed"
	ResultStatusRetry     ResultStatus = "retry"
)

// Result records the final disposition of a request attempt.
type Result struct {
	Status      ResultStatus
	Error       string
	CompletedAt time.Time
	Metadata    map[string]any
}

// Coordinator provides adapter-neutral lease + queue semantics for
// serialized per-session execution.
type Coordinator interface {
	Enqueue(ctx context.Context, sessionID string) (Ticket, error)
	Await(ctx context.Context, ticket Ticket) (Lease, error)
	Renew(ctx context.Context, lease Lease) (Lease, error)
	Ack(ctx context.Context, lease Lease, result Result) error
	Nack(ctx context.Context, lease Lease, result Result) error
}

// LocalCoordinator provides the same lease + queue semantics as future
// distributed coordinators, but within one process for tests and local use.
type LocalCoordinator struct {
	mu       sync.Mutex
	leaseTTL time.Duration
	lanes    map[string]*coordinatorLane
}

type coordinatorLane struct {
	nextSeq   uint64
	nextLease uint64
	queue     []*laneEntry
	active    *laneActive
	waitCh    chan struct{}
}

type laneEntry struct {
	ticket  Ticket
	attempt uint32
}

type laneActive struct {
	lease  Lease
	result Result
}

// NewLocalCoordinator creates a local coordinator with lease expiry and
// FIFO queue semantics.
func NewLocalCoordinator() *LocalCoordinator {
	return &LocalCoordinator{
		leaseTTL: defaultLeaseTTL,
		lanes:    make(map[string]*coordinatorLane),
	}
}

// SetLeaseTTL updates the lease duration used for newly granted or renewed leases.
func (c *LocalCoordinator) SetLeaseTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leaseTTL = ttl
}

func (c *LocalCoordinator) Enqueue(
	ctx context.Context,
	sessionID string,
) (Ticket, error) {
	if err := ctx.Err(); err != nil {
		return Ticket{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	lane := c.getOrCreateLaneLocked(sessionID)
	lane.nextSeq++
	ticket := Ticket{
		RequestID:  ulid.Make().String(),
		SessionID:  sessionID,
		Sequence:   lane.nextSeq,
		EnqueuedAt: time.Now().UTC(),
	}
	lane.queue = append(lane.queue, &laneEntry{ticket: ticket})
	lane.notifyLocked()
	return ticket, nil
}

func (c *LocalCoordinator) Await(ctx context.Context, ticket Ticket) (Lease, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Lease{}, err
		}

		c.mu.Lock()
		lane, ok := c.lanes[ticket.SessionID]
		if !ok {
			c.mu.Unlock()
			return Lease{}, ErrTicketNotFound
		}

		lease, waitCh, waitFor, err := c.tryGrantLocked(lane, ticket)
		c.mu.Unlock()

		if err != nil {
			return Lease{}, err
		}
		if lease != nil {
			return *lease, nil
		}

		var timer <-chan time.Time
		if waitFor > 0 {
			timer = time.After(waitFor)
		}

		select {
		case <-ctx.Done():
			return Lease{}, ctx.Err()
		case <-waitCh:
		case <-timer:
		}
	}
}

func (c *LocalCoordinator) Renew(ctx context.Context, lease Lease) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	lane, ok := c.lanes[lease.Ticket.SessionID]
	if !ok {
		return Lease{}, ErrTicketNotFound
	}
	if lane.active == nil {
		return Lease{}, ErrLeaseStale
	}
	if lane.active.lease.Ticket.RequestID != lease.Ticket.RequestID ||
		lane.active.lease.LeaseToken != lease.LeaseToken {
		return Lease{}, ErrLeaseStale
	}
	if time.Now().After(lane.active.lease.ExpiresAt) {
		lane.active = nil
		lane.notifyLocked()
		return Lease{}, ErrLeaseExpired
	}

	lane.active.lease.ExpiresAt = time.Now().UTC().Add(c.leaseTTL)
	return lane.active.lease, nil
}

func (c *LocalCoordinator) Ack(
	ctx context.Context,
	lease Lease,
	result Result,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finishLocked(lease, result, true)
}

func (c *LocalCoordinator) Nack(
	ctx context.Context,
	lease Lease,
	result Result,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finishLocked(lease, result, false)
}

func (c *LocalCoordinator) tryGrantLocked(
	lane *coordinatorLane,
	ticket Ticket,
) (*Lease, chan struct{}, time.Duration, error) {
	now := time.Now().UTC()
	entryIdx := -1
	for i, entry := range lane.queue {
		if entry.ticket.RequestID == ticket.RequestID {
			entryIdx = i
			break
		}
	}
	if entryIdx == -1 {
		return nil, nil, 0, ErrTicketNotFound
	}

	if lane.active != nil {
		if now.After(lane.active.lease.ExpiresAt) {
			lane.active = nil
			lane.notifyLocked()
		} else if lane.active.lease.Ticket.RequestID == ticket.RequestID {
			lease := lane.active.lease
			return &lease, nil, 0, nil
		} else {
			return nil, lane.waitCh, time.Until(lane.active.lease.ExpiresAt), nil
		}
	}

	if entryIdx != 0 {
		return nil, lane.waitCh, 0, nil
	}

	entry := lane.queue[0]
	entry.attempt++
	lane.nextLease++
	lease := Lease{
		Ticket:     entry.ticket,
		Attempt:    entry.attempt,
		LeaseToken: lane.nextLease,
		GrantedAt:  now,
		ExpiresAt:  now.Add(c.leaseTTL),
	}
	lane.active = &laneActive{lease: lease}
	return &lease, nil, 0, nil
}

func (c *LocalCoordinator) finishLocked(
	lease Lease,
	result Result,
	remove bool,
) error {
	lane, ok := c.lanes[lease.Ticket.SessionID]
	if !ok {
		return ErrTicketNotFound
	}
	if lane.active == nil {
		return ErrLeaseStale
	}

	active := lane.active.lease
	if active.Ticket.RequestID != lease.Ticket.RequestID ||
		active.LeaseToken != lease.LeaseToken {
		return ErrLeaseStale
	}
	if time.Now().After(active.ExpiresAt) {
		lane.active = nil
		lane.notifyLocked()
		return ErrLeaseExpired
	}

	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}
	lane.active.result = result
	lane.active = nil

	if remove {
		if len(lane.queue) == 0 || lane.queue[0].ticket.RequestID != lease.Ticket.RequestID {
			return fmt.Errorf("coordinator ack %s: %w", lease.Ticket.RequestID, ErrTicketNotFound)
		}
		lane.queue = lane.queue[1:]
	}

	if len(lane.queue) == 0 && lane.active == nil {
		delete(c.lanes, lease.Ticket.SessionID)
		return nil
	}
	lane.notifyLocked()
	return nil
}

func (c *LocalCoordinator) getOrCreateLaneLocked(sessionID string) *coordinatorLane {
	if lane, ok := c.lanes[sessionID]; ok {
		return lane
	}
	lane := &coordinatorLane{
		waitCh: make(chan struct{}),
	}
	c.lanes[sessionID] = lane
	return lane
}

func (l *coordinatorLane) notifyLocked() {
	close(l.waitCh)
	l.waitCh = make(chan struct{})
}
