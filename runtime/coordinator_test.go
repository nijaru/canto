package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemoryLaneCoordinator_FIFOPerSession(t *testing.T) {
	coord := NewInMemoryLaneCoordinator()
	coord.SetLeaseTTL(100 * time.Millisecond)

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

	secondReady := make(chan LaneLease, 1)
	go func() {
		lease, err := coord.Await(t.Context(), second)
		if err != nil {
			t.Errorf("await second: %v", err)
			return
		}
		secondReady <- lease
	}()

	select {
	case lease := <-secondReady:
		t.Fatalf("second lease granted too early: %#v", lease)
	case <-time.After(20 * time.Millisecond):
	}

	if err := coord.Ack(t.Context(), firstLease, LaneResult{Status: "completed"}); err != nil {
		t.Fatalf("ack first: %v", err)
	}

	select {
	case lease := <-secondReady:
		if lease.Ticket.RequestID != second.RequestID {
			t.Fatalf(
				"second lease request_id = %q, want %q",
				lease.Ticket.RequestID,
				second.RequestID,
			)
		}
		if lease.Attempt != 1 {
			t.Fatalf("second attempt = %d, want 1", lease.Attempt)
		}
	case <-time.After(time.Second):
		t.Fatal("second lease was never granted")
	}
}

func TestInMemoryLaneCoordinator_ReclaimsExpiredLease(t *testing.T) {
	coord := NewInMemoryLaneCoordinator()
	coord.SetLeaseTTL(25 * time.Millisecond)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	firstLease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await first lease: %v", err)
	}
	if firstLease.Attempt != 1 {
		t.Fatalf("first attempt = %d, want 1", firstLease.Attempt)
	}

	time.Sleep(35 * time.Millisecond)

	secondLease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await reclaimed lease: %v", err)
	}
	if secondLease.Attempt != 2 {
		t.Fatalf("second attempt = %d, want 2", secondLease.Attempt)
	}
	if secondLease.LeaseToken <= firstLease.LeaseToken {
		t.Fatalf(
			"lease token did not advance: first=%d second=%d",
			firstLease.LeaseToken,
			secondLease.LeaseToken,
		)
	}
}

func TestInMemoryLaneCoordinator_RejectsStaleAckAfterReclaim(t *testing.T) {
	coord := NewInMemoryLaneCoordinator()
	coord.SetLeaseTTL(25 * time.Millisecond)

	ticket, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	firstLease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await first lease: %v", err)
	}

	time.Sleep(35 * time.Millisecond)

	secondLease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await second lease: %v", err)
	}

	err = coord.Ack(t.Context(), firstLease, LaneResult{Status: "completed"})
	if !errors.Is(err, ErrLaneLeaseStale) {
		t.Fatalf("stale ack error = %v, want ErrLaneLeaseStale", err)
	}

	if err := coord.Ack(t.Context(), secondLease, LaneResult{Status: "completed"}); err != nil {
		t.Fatalf("ack second lease: %v", err)
	}
}

func TestInMemoryLaneCoordinator_RenewExtendsLease(t *testing.T) {
	coord := NewInMemoryLaneCoordinator()
	coord.SetLeaseTTL(40 * time.Millisecond)

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
		t.Fatalf("renewed expiry %v is not after original %v", renewed.ExpiresAt, lease.ExpiresAt)
	}

	if err := coord.Ack(t.Context(), renewed, LaneResult{Status: "completed"}); err != nil {
		t.Fatalf("ack renewed lease: %v", err)
	}
}

func TestInMemoryLaneCoordinator_AllowsParallelLeasesAcrossSessions(t *testing.T) {
	coord := NewInMemoryLaneCoordinator()

	first, err := coord.Enqueue(t.Context(), "s1")
	if err != nil {
		t.Fatalf("enqueue s1: %v", err)
	}
	second, err := coord.Enqueue(t.Context(), "s2")
	if err != nil {
		t.Fatalf("enqueue s2: %v", err)
	}

	lease1, err := coord.Await(t.Context(), first)
	if err != nil {
		t.Fatalf("await s1: %v", err)
	}
	lease2, err := coord.Await(t.Context(), second)
	if err != nil {
		t.Fatalf("await s2: %v", err)
	}

	if lease1.Ticket.SessionID == lease2.Ticket.SessionID {
		t.Fatalf("expected distinct session leases, got %q", lease1.Ticket.SessionID)
	}
}

func TestInMemoryLaneCoordinator_NackKeepsRequestQueued(t *testing.T) {
	coord := NewInMemoryLaneCoordinator()
	coord.SetLeaseTTL(100 * time.Millisecond)

	ticket, err := coord.Enqueue(context.Background(), "s1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	lease, err := coord.Await(t.Context(), ticket)
	if err != nil {
		t.Fatalf("await: %v", err)
	}

	if err := coord.Nack(t.Context(), lease, LaneResult{
		Status: "retry",
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
