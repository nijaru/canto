package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/redis/go-redis/v9"

	"github.com/nijaru/canto/runtime"
)

// RedisCoordinator implements [runtime.Coordinator] using Redis for distributed
// per-session lane coordination. It maintains per-session sorted-set queues
// and lease hashes with TTL-based expiry. All critical path operations use
// Lua scripts for atomicity.
//
// Attempt counting uses a persistent per-session key so that lease reclaim
// after expiry increments the attempt rather than resetting it.
//
// Zero values are not safe for use; construct via [NewRedisCoordinator].
type RedisCoordinator struct {
	client       redis.Cmdable
	keyPrefix    string
	leaseTTL     time.Duration
	pollInterval time.Duration
	stopCh       chan struct{}
}

// Option configures a [RedisCoordinator].
type Option func(*RedisCoordinator)

// WithKeyPrefix sets the Redis key prefix. Default: "canto:coord:".
func WithKeyPrefix(p string) Option {
	return func(c *RedisCoordinator) { c.keyPrefix = p }
}

// WithLeaseTTL sets the default lease duration. Default: 30s.
func WithLeaseTTL(ttl time.Duration) Option {
	return func(c *RedisCoordinator) { c.leaseTTL = ttl }
}

// WithPollInterval sets how often Await retries for a lease. Default: 50ms.
func WithPollInterval(d time.Duration) Option {
	return func(c *RedisCoordinator) { c.pollInterval = d }
}

// NewRedisCoordinator creates a coordinator that uses the given Redis client.
func NewRedisCoordinator(client redis.Cmdable, opts ...Option) *RedisCoordinator {
	c := &RedisCoordinator{
		client:       client,
		keyPrefix:    "canto:coord:",
		leaseTTL:     30 * time.Second,
		pollInterval: 50 * time.Millisecond,
		stopCh:       make(chan struct{}),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Stop signals Await loops to exit. Does not close the Redis client.
func (c *RedisCoordinator) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// Enqueue adds a ticket to the session's FIFO queue.
// Uses a Lua script for atomic sequence assignment + ZADD NX.
func (c *RedisCoordinator) Enqueue(
	ctx context.Context,
	sessionID string,
) (runtime.Ticket, error) {
	ticket := runtime.Ticket{
		RequestID:  ulid.Make().String(),
		SessionID:  sessionID,
		EnqueuedAt: time.Now().UTC(),
	}

	seq, err := enqueueScript.Run(
		ctx, c.client,
		[]string{c.queueKey(sessionID), c.seqKey(sessionID)},
		ticket.RequestID,
	).Int64()
	if err != nil {
		return runtime.Ticket{}, fmt.Errorf("redis coordinator enqueue: %w", err)
	}
	if seq == 0 {
		return runtime.Ticket{}, fmt.Errorf(
			"redis coordinator enqueue: ticket %s already in queue",
			ticket.RequestID,
		)
	}
	ticket.Sequence = uint64(seq)

	return ticket, nil
}

// Await polls until the ticket's session lane grants a lease or ctx is cancelled.
func (c *RedisCoordinator) Await(
	ctx context.Context,
	ticket runtime.Ticket,
) (runtime.Lease, error) {
	for {
		select {
		case <-ctx.Done():
			return runtime.Lease{}, ctx.Err()
		case <-c.stopCh:
			return runtime.Lease{}, errors.New("redis coordinator stopped")
		default:
		}

		lease, err := c.tryGrant(ctx, ticket)
		if err != nil {
			return runtime.Lease{}, err
		}
		if lease != nil {
			return *lease, nil
		}

		timer := time.NewTimer(c.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return runtime.Lease{}, ctx.Err()
		case <-c.stopCh:
			timer.Stop()
			return runtime.Lease{}, errors.New("redis coordinator stopped")
		case <-timer.C:
		}
	}
}

// Renew extends the lease TTL if the lease is still valid.
func (c *RedisCoordinator) Renew(
	ctx context.Context,
	lease runtime.Lease,
) (runtime.Lease, error) {
	now := time.Now().UTC()
	args := []any{
		lease.Ticket.RequestID,
		strconv.FormatUint(uint64(lease.LeaseToken), 10),
		now.UnixMilli(),
		now.Add(c.leaseTTL).UnixMilli(),
	}

	result, err := renewScript.Run(
		ctx, c.client,
		[]string{c.leaseKey(lease.Ticket.SessionID)},
		args...,
	).Int64Slice()
	if err != nil {
		return runtime.Lease{}, fmt.Errorf("redis coordinator renew: %w", err)
	}
	if result[0] == 0 {
		if result[1] == 1 {
			return runtime.Lease{}, runtime.ErrLeaseExpired
		}
		return runtime.Lease{}, runtime.ErrLeaseStale
	}

	return c.parseLease(lease.Ticket, result[1:])
}

// Ack completes the lease and removes the ticket from the queue.
func (c *RedisCoordinator) Ack(
	ctx context.Context,
	lease runtime.Lease,
	result runtime.Result,
) error {
	return c.finish(ctx, lease, true)
}

// Nack releases the lease but keeps the ticket in the queue for retry.
func (c *RedisCoordinator) Nack(
	ctx context.Context,
	lease runtime.Lease,
	result runtime.Result,
) error {
	if result.Error != "" {
		c.client.HSet(ctx, c.leaseKey(lease.Ticket.SessionID),
			"last_error", result.Error)
	}
	return c.finish(ctx, lease, false)
}

// --- internals ---

func (c *RedisCoordinator) finish(
	ctx context.Context,
	lease runtime.Lease,
	ack bool,
) error {
	args := []any{
		lease.Ticket.RequestID,
		strconv.FormatUint(uint64(lease.LeaseToken), 10),
	}
	key := c.leaseKey(lease.Ticket.SessionID)

	var result []int64
	var err error
	if ack {
		result, err = ackScript.Run(
			ctx, c.client,
			[]string{
				c.queueKey(lease.Ticket.SessionID),
				key,
				c.attKey(lease.Ticket.SessionID),
				c.tokKey(lease.Ticket.SessionID),
			},
			args...,
		).Int64Slice()
	} else {
		result, err = nackScript.Run(
			ctx, c.client,
			[]string{key},
			args...,
		).Int64Slice()
	}
	if err != nil {
		return fmt.Errorf("redis coordinator finish: %w", err)
	}
	if result[0] == 0 {
		if result[1] == 1 {
			return runtime.ErrLeaseExpired
		}
		return runtime.ErrLeaseStale
	}
	return nil
}

func (c *RedisCoordinator) tryGrant(
	ctx context.Context,
	ticket runtime.Ticket,
) (*runtime.Lease, error) {
	args := []any{
		ticket.RequestID,
		time.Now().UTC().UnixMilli(),
		c.leaseTTL.Milliseconds(),
	}

	result, err := grantScript.Run(
		ctx, c.client,
		[]string{
			c.queueKey(ticket.SessionID),
			c.leaseKey(ticket.SessionID),
			c.attKey(ticket.SessionID),
			c.tokKey(ticket.SessionID),
		},
		args...,
	).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("redis coordinator try-grant: %w", err)
	}
	if result[0] == 0 {
		return nil, nil
	}

	lease, err := c.parseLease(ticket, result[1:])
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

// parseLease reconstructs a Lease from Lua script output. The original ticket
// is passed through so that RequestID and Sequence are preserved.
func (c *RedisCoordinator) parseLease(
	ticket runtime.Ticket,
	fields []int64,
) (runtime.Lease, error) {
	if len(fields) < 4 {
		return runtime.Lease{}, fmt.Errorf(
			"redis coordinator: expected 4 lease fields, got %d",
			len(fields),
		)
	}

	return runtime.Lease{
		Ticket:     ticket,
		Attempt:    uint32(fields[0]),
		LeaseToken: uint64(fields[1]),
		GrantedAt:  time.UnixMilli(fields[2]).UTC(),
		ExpiresAt:  time.UnixMilli(fields[3]).UTC(),
	}, nil
}
