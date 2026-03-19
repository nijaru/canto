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
	ErrLaneTicketNotFound = errors.New("lane ticket not found")
	ErrLaneLeaseExpired   = errors.New("lane lease expired")
	ErrLaneLeaseStale     = errors.New("lane lease is stale")
)

const defaultLeaseTTL = 30 * time.Second

// LaneTicket identifies a queued session-lane request.
type LaneTicket struct {
	RequestID  string
	SessionID  string
	Sequence   uint64
	EnqueuedAt time.Time
}

// LaneLease grants temporary authority to execute a queued request.
type LaneLease struct {
	Ticket     LaneTicket
	Attempt    uint32
	LeaseToken uint64
	GrantedAt  time.Time
	ExpiresAt  time.Time
}

// LaneResult records the final disposition of a request.
type LaneStatus string

const (
	LaneStatusCompleted LaneStatus = "completed"
	LaneStatusCanceled  LaneStatus = "canceled"
	LaneStatusFailed    LaneStatus = "failed"
	LaneStatusRetry     LaneStatus = "retry"
)

// LaneResult records the final disposition of a request.
type LaneResult struct {
	Status      LaneStatus
	Error       string
	CompletedAt time.Time
	Metadata    map[string]any
}

// LaneCoordinator provides adapter-neutral lease + queue semantics for
// serialized per-session execution.
type LaneCoordinator interface {
	Enqueue(ctx context.Context, sessionID string) (LaneTicket, error)
	Await(ctx context.Context, ticket LaneTicket) (LaneLease, error)
	Renew(ctx context.Context, lease LaneLease) (LaneLease, error)
	Ack(ctx context.Context, lease LaneLease, result LaneResult) error
	Nack(ctx context.Context, lease LaneLease, result LaneResult) error
}

// InMemoryLaneCoordinator provides the same lease + queue semantics as the
// future distributed coordinator, but within one process for testing and local
// integration.
type InMemoryLaneCoordinator struct {
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
	ticket  LaneTicket
	attempt uint32
}

type laneActive struct {
	lease  LaneLease
	result LaneResult
}

// NewInMemoryLaneCoordinator creates a local coordinator with lease expiry and
// FIFO queue semantics.
func NewInMemoryLaneCoordinator() *InMemoryLaneCoordinator {
	return &InMemoryLaneCoordinator{
		leaseTTL: defaultLeaseTTL,
		lanes:    make(map[string]*coordinatorLane),
	}
}

// SetLeaseTTL updates the lease duration used for newly granted or renewed leases.
func (c *InMemoryLaneCoordinator) SetLeaseTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leaseTTL = ttl
}

func (c *InMemoryLaneCoordinator) Enqueue(
	ctx context.Context,
	sessionID string,
) (LaneTicket, error) {
	if err := ctx.Err(); err != nil {
		return LaneTicket{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	lane := c.getOrCreateLaneLocked(sessionID)
	lane.nextSeq++
	ticket := LaneTicket{
		RequestID:  ulid.Make().String(),
		SessionID:  sessionID,
		Sequence:   lane.nextSeq,
		EnqueuedAt: time.Now().UTC(),
	}
	lane.queue = append(lane.queue, &laneEntry{ticket: ticket})
	lane.notifyLocked()
	return ticket, nil
}

func (c *InMemoryLaneCoordinator) Await(ctx context.Context, ticket LaneTicket) (LaneLease, error) {
	for {
		if err := ctx.Err(); err != nil {
			return LaneLease{}, err
		}

		c.mu.Lock()
		lane, ok := c.lanes[ticket.SessionID]
		if !ok {
			c.mu.Unlock()
			return LaneLease{}, ErrLaneTicketNotFound
		}

		lease, waitCh, waitFor, err := c.tryGrantLocked(lane, ticket)
		c.mu.Unlock()

		if err != nil {
			return LaneLease{}, err
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
			return LaneLease{}, ctx.Err()
		case <-waitCh:
		case <-timer:
		}
	}
}

func (c *InMemoryLaneCoordinator) Renew(ctx context.Context, lease LaneLease) (LaneLease, error) {
	if err := ctx.Err(); err != nil {
		return LaneLease{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	lane, ok := c.lanes[lease.Ticket.SessionID]
	if !ok {
		return LaneLease{}, ErrLaneTicketNotFound
	}
	if lane.active == nil {
		return LaneLease{}, ErrLaneLeaseStale
	}
	if lane.active.lease.Ticket.RequestID != lease.Ticket.RequestID ||
		lane.active.lease.LeaseToken != lease.LeaseToken {
		return LaneLease{}, ErrLaneLeaseStale
	}
	if time.Now().After(lane.active.lease.ExpiresAt) {
		lane.active = nil
		lane.notifyLocked()
		return LaneLease{}, ErrLaneLeaseExpired
	}

	lane.active.lease.ExpiresAt = time.Now().UTC().Add(c.leaseTTL)
	return lane.active.lease, nil
}

func (c *InMemoryLaneCoordinator) Ack(
	ctx context.Context,
	lease LaneLease,
	result LaneResult,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finishLocked(lease, result, true)
}

func (c *InMemoryLaneCoordinator) Nack(
	ctx context.Context,
	lease LaneLease,
	result LaneResult,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finishLocked(lease, result, false)
}

func (c *InMemoryLaneCoordinator) tryGrantLocked(
	lane *coordinatorLane,
	ticket LaneTicket,
) (*LaneLease, chan struct{}, time.Duration, error) {
	now := time.Now().UTC()
	entryIdx := -1
	for i, entry := range lane.queue {
		if entry.ticket.RequestID == ticket.RequestID {
			entryIdx = i
			break
		}
	}
	if entryIdx == -1 {
		return nil, nil, 0, ErrLaneTicketNotFound
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
	lease := LaneLease{
		Ticket:     entry.ticket,
		Attempt:    entry.attempt,
		LeaseToken: lane.nextLease,
		GrantedAt:  now,
		ExpiresAt:  now.Add(c.leaseTTL),
	}
	lane.active = &laneActive{lease: lease}
	return &lease, nil, 0, nil
}

func (c *InMemoryLaneCoordinator) finishLocked(
	lease LaneLease,
	result LaneResult,
	remove bool,
) error {
	lane, ok := c.lanes[lease.Ticket.SessionID]
	if !ok {
		return ErrLaneTicketNotFound
	}
	if lane.active == nil {
		return ErrLaneLeaseStale
	}

	active := lane.active.lease
	if active.Ticket.RequestID != lease.Ticket.RequestID ||
		active.LeaseToken != lease.LeaseToken {
		return ErrLaneLeaseStale
	}
	if time.Now().After(active.ExpiresAt) {
		lane.active = nil
		lane.notifyLocked()
		return ErrLaneLeaseExpired
	}

	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}
	lane.active.result = result
	lane.active = nil

	if remove {
		if len(lane.queue) == 0 || lane.queue[0].ticket.RequestID != lease.Ticket.RequestID {
			return fmt.Errorf("lane ack %s: %w", lease.Ticket.RequestID, ErrLaneTicketNotFound)
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

func (c *InMemoryLaneCoordinator) getOrCreateLaneLocked(sessionID string) *coordinatorLane {
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
