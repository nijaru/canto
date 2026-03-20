package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// queueRequest represents a unit of work in the local serial queue.
type queueRequest struct {
	Ctx    context.Context
	Fn     func(ctx context.Context) error
	Result chan error
}

// serialQueue is Runner's built-in local coordination path.
// It serializes execution within a session while allowing concurrency across
// different sessions.
type serialQueue struct {
	mu    sync.RWMutex
	lanes map[string]*lane

	// Config
	LaneBufferSize int
	IdleTimeout    time.Duration
	DrainTimeout   time.Duration

	closing bool
}

func newSerialQueue() *serialQueue {
	return &serialQueue{
		lanes:          make(map[string]*lane),
		LaneBufferSize: 64,
		IdleTimeout:    10 * time.Minute,
		DrainTimeout:   30 * time.Second,
	}
}

// lane represents a single execution lane for a session.
type lane struct {
	sessionID string
	requests  chan queueRequest
	lastUsed  time.Time
	mu        sync.Mutex
	done      chan struct{}
	drain     chan struct{}
	cancel    context.CancelFunc
}

// execute queues a function for execution in the specified session lane.
func (m *serialQueue) execute(
	ctx context.Context,
	sessionID string,
	fn func(ctx context.Context) error,
) <-chan error {
	result := make(chan error, 1)
	req := queueRequest{
		Ctx:    ctx,
		Fn:     fn,
		Result: result,
	}

	for {
		m.mu.RLock()
		closing := m.closing
		m.mu.RUnlock()

		if closing {
			result <- fmt.Errorf("lane manager is shutting down")
			return result
		}

		l := m.getOrCreateLane(sessionID)

		select {
		case <-l.done:
			// Lane is shutting down, get a new one
			continue
		case l.requests <- req:
			// Queued successfully
			return result
		case <-ctx.Done():
			result <- ctx.Err()
			return result
		default:
			// Buffer full
			result <- fmt.Errorf("session lane %s is full", sessionID)
			return result
		}
	}
}

func (m *serialQueue) getOrCreateLane(sessionID string) *lane {
	m.mu.RLock()
	l, ok := m.lanes[sessionID]
	m.mu.RUnlock()

	if ok {
		l.mu.Lock()
		l.lastUsed = time.Now()
		l.mu.Unlock()
		return l
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double check under write lock
	if l, ok := m.lanes[sessionID]; ok {
		return l
	}

	ctx, cancel := context.WithCancel(context.Background())
	l = &lane{
		sessionID: sessionID,
		requests:  make(chan queueRequest, m.LaneBufferSize),
		lastUsed:  time.Now(),
		done:      make(chan struct{}),
		drain:     make(chan struct{}),
		cancel:    cancel,
	}
	m.lanes[sessionID] = l

	go m.runLane(ctx, l)

	return l
}

func (m *serialQueue) runLane(ctx context.Context, l *lane) {
	defer func() {
		m.mu.Lock()
		delete(m.lanes, l.sessionID)
		m.mu.Unlock()
		close(l.done)

		// Drain any buffered requests. Do NOT close(l.requests): closing while
		// Execute callers may still hold a reference to this lane would cause a
		// send-on-closed-channel panic. After close(l.done) no new sends will
		// succeed (callers see done and continue to a new lane), so the buffer
		// is bounded and this loop terminates.
		for {
			select {
			case req := <-l.requests:
				req.Result <- fmt.Errorf("lane shutting down")
			default:
				return
			}
		}
	}()

	timer := time.NewTimer(m.IdleTimeout)
	defer timer.Stop()

	for {
		select {
		case req := <-l.requests:
			if !timer.Stop() {
				<-timer.C
			}

			// Process request
			err := req.Fn(req.Ctx)
			req.Result <- err

			l.mu.Lock()
			l.lastUsed = time.Now()
			l.mu.Unlock()

			timer.Reset(m.IdleTimeout)

		case <-timer.C:
			// Idle timeout reached, shut down lane if no pending requests
			l.mu.Lock()
			if len(l.requests) == 0 {
				l.mu.Unlock()
				return
			}
			l.mu.Unlock()
			timer.Reset(m.IdleTimeout)

		case <-l.drain:
			// Drain remaining requests in the buffer.
			for {
				select {
				case req := <-l.requests:
					err := req.Fn(req.Ctx)
					req.Result <- err
				default:
					return
				}
			}

		case <-ctx.Done():
			// Shutdown signal
			return
		}
	}
}

// stop prevents new requests and drains active local work.
func (m *serialQueue) stop() {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	m.closing = true
	lanes := make([]*lane, 0, len(m.lanes))
	for _, l := range m.lanes {
		lanes = append(lanes, l)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, l := range lanes {
		wg.Go(func() {
			close(l.drain)
			select {
			case <-l.done:
			case <-time.After(m.DrainTimeout):
				l.cancel()
				<-l.done
			}
		})
	}
	wg.Wait()
}
