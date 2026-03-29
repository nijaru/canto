//go:build redis

package redis

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/nijaru/canto/runtime"
)

const testRedisURLVar = "CANTO_TEST_REDIS_URL"

// redisClient connects to an externally managed Redis instance for integration
// testing. CI provides Redis as a service; local runs can point at any
// disposable instance by setting CANTO_TEST_REDIS_URL.
func redisClient(t *testing.T) *goredis.Client {
	t.Helper()
	redisURL := os.Getenv(testRedisURLVar)
	if redisURL == "" {
		t.Skipf("%s is not set", testRedisURLVar)
	}

	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testRedisURLVar, err)
	}

	client := goredis.NewClient(opts)
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func newTestCoord(t *testing.T) *RedisCoordinator {
	t.Helper()
	client := redisClient(t)

	prefix := "test:coord:" + t.Name() + ":"
	ctx := t.Context()
	keys, _ := client.Keys(ctx, prefix+"*").Result()
	if len(keys) > 0 {
		_ = client.Del(ctx, keys...).Err()
	}
	t.Cleanup(func() {
		keys, _ := client.Keys(ctx, prefix+"*").Result()
		if len(keys) > 0 {
			_ = client.Del(ctx, keys...).Err()
		}
	})

	coord := NewRedisCoordinator(client,
		WithKeyPrefix(prefix),
		WithLeaseTTL(100*time.Millisecond),
		WithPollInterval(5*time.Millisecond),
	)
	t.Cleanup(coord.Stop)
	return coord
}

func TestRedisCoordinator_FIFOPerSession(t *testing.T) {
	coord := newTestCoord(t)

	first, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue first: %v", err)
	}
	second, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	firstLease, err := coord.Await(t.Context(), first)
	if err != nil {
		t.Fatalf("await first: %v", err)
	}

	secondReady := make(chan runtime.Lease, 1)
	go func() {
		lease, err := coord.Await(t.Context(), second)
		if err != nil {
			t.Errorf("await second: %v", err)
			return
		}
		secondReady <- lease
	}()

	// Second should NOT be granted while first holds the lease.
	select {
	case lease := <-secondReady:
		t.Fatalf("second lease granted too early: %+v", lease)
	case <-time.After(30 * time.Millisecond):
	}

	if err := coord.Ack(t.Context(), firstLease, runtime.Result{
		Status: runtime.ResultStatusCompleted,
	}); err != nil {
		t.Fatalf("ack first: %v", err)
	}

	// Now second should be granted.
	select {
	case lease := <-secondReady:
		if lease.Attempt != 1 {
			t.Fatalf("second attempt = %d, want 1", lease.Attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second lease was never granted")
	}
}

func TestRedisCoordinator_ReclaimsExpiredLease(t *testing.T) {
	coord := newTestCoord(t)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	first, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await first: %v", err)
	}
	if first.Attempt != 1 {
		t.Fatalf("first attempt = %d, want 1", first.Attempt)
	}

	time.Sleep(120 * time.Millisecond) // wait for expiry

	second, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await reclaimed: %v", err)
	}
	if second.Attempt != 2 {
		t.Fatalf("second attempt = %d, want 2", second.Attempt)
	}
	if second.LeaseToken <= first.LeaseToken {
		t.Fatalf(
			"lease token did not advance: first=%d second=%d",
			first.LeaseToken, second.LeaseToken,
		)
	}
}

func TestRedisCoordinator_RejectsStaleAckAfterReclaim(t *testing.T) {
	coord := newTestCoord(t)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	first, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await first: %v", err)
	}

	time.Sleep(120 * time.Millisecond)

	second, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await second: %v", err)
	}

	err = coord.Ack(t.Context(), first, runtime.Result{
		Status: runtime.ResultStatusCompleted,
	})
	if !errors.Is(err, runtime.ErrLeaseStale) {
		t.Fatalf("stale ack error = %v, want ErrLeaseStale", err)
	}

	if err := coord.Ack(t.Context(), second, runtime.Result{
		Status: runtime.ResultStatusCompleted,
	}); err != nil {
		t.Fatalf("ack second: %v", err)
	}
}

func TestRedisCoordinator_RenewExtendsLease(t *testing.T) {
	coord := newTestCoord(t)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	lease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	renewed, err := coord.Renew(t.Context(), lease)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !renewed.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatalf("renewed expiry %v not after original %v",
			renewed.ExpiresAt, lease.ExpiresAt)
	}

	if err := coord.Ack(t.Context(), renewed, runtime.Result{
		Status: runtime.ResultStatusCompleted,
	}); err != nil {
		t.Fatalf("ack renewed: %v", err)
	}
}

func TestRedisCoordinator_AllowsParallelLeasesAcrossSessions(t *testing.T) {
	coord := newTestCoord(t)

	t1, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue s1: %v", err)
	}
	t2, err := coord.Enqueue(t.Context(), "s2")
	if err != nil {
		t.Fatalf("enqueue s2: %v", err)
	}

	l1, err := coord.Await(t.Context(), t1)
	if err != nil {
		t.Fatalf("await s1: %v", err)
	}
	l2, err := coord.Await(t.Context(), t2)
	if err != nil {
		t.Fatalf("await s2: %v", err)
	}

	if l1.Ticket.SessionID == l2.Ticket.SessionID {
		t.Fatalf("expected distinct sessions, got %q", l1.Ticket.SessionID)
	}
}

func TestRedisCoordinator_NackKeepsRequestQueued(t *testing.T) {
	coord := newTestCoord(t)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	lease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await: %v", err)
	}

	if err := coord.Nack(t.Context(), lease, runtime.Result{
		Status: runtime.ResultStatusRetry,
		Error:  "temporary failure",
	}); err != nil {
		t.Fatalf("nack: %v", err)
	}

	retried, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await retry: %v", err)
	}
	if retried.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", retried.Attempt)
	}
}

func TestRedisCoordinator_NackAfterLeaseExpiry(t *testing.T) {
	coord := newTestCoord(t)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	lease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await: %v", err)
	}

	// Let lease expire, then reclaim so the hash has a new token.
	time.Sleep(120 * time.Millisecond)
	_, _ = coord.Await(t.Context(), ticket)

	// Nack with the old (stale) lease — should be rejected.
	err = coord.Nack(t.Context(), lease, runtime.Result{
		Status: runtime.ResultStatusFailed,
		Error:  "too slow",
	})
	if !errors.Is(err, runtime.ErrLeaseStale) {
		t.Fatalf("nack stale = %v, want ErrLeaseStale", err)
	}
}

func TestRedisCoordinator_EnqueueIdempotent(t *testing.T) {
	coord := newTestCoord(t)

	_, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	_, err = coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}

	// Sorted set should have exactly 2 members (distinct request IDs).
	client := coord.client
	count, err := client.ZCard(t.Context(), coord.queueKey("s1")).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if count != 2 {
		t.Fatalf("queue size = %d, want 2", count)
	}
}

func TestRedisCoordinator_ConcurrentAccess(t *testing.T) {
	coord := newTestCoord(t)

	const n = 5
	tickets := make([]runtime.Ticket, n)
	for i := range tickets {
		ticket, err := coord.Enqueue(t.Context(), "s1")
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		tickets[i] = ticket
	}

	results := make(chan error, n)
	for _, ticket := range tickets {
		go func() {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			lease, err := coord.Await(ctx, ticket)
			if err != nil {
				results <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
			results <- coord.Ack(ctx, lease, runtime.Result{
				Status: runtime.ResultStatusCompleted,
			})
		}()
	}

	for range tickets {
		if err := <-results; err != nil {
			t.Errorf("concurrent execution: %v", err)
		}
	}
}

func TestRedisCoordinator_RenewRejectsStaleLease(t *testing.T) {
	coord := newTestCoord(t)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	lease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await: %v", err)
	}

	// Expire and reclaim.
	time.Sleep(120 * time.Millisecond)
	_, _ = coord.Await(t.Context(), ticket)

	_, err = coord.Renew(t.Context(), lease)
	if !errors.Is(err, runtime.ErrLeaseStale) {
		t.Fatalf("renew stale = %v, want ErrLeaseStale", err)
	}
}

func TestRedisCoordinator_EnqueueSequenceIsMonotonic(t *testing.T) {
	coord := newTestCoord(t)

	t1, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}
	t2, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	t3, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue 3: %v", err)
	}

	if t2.Sequence <= t1.Sequence {
		t.Fatalf("seq not monotonic: %d <= %d", t2.Sequence, t1.Sequence)
	}
	if t3.Sequence <= t2.Sequence {
		t.Fatalf("seq not monotonic: %d <= %d", t3.Sequence, t2.Sequence)
	}
}
