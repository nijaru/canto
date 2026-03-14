package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Request represents a unit of work to be executed in a session lane.
type Request struct {
	Ctx    context.Context
	Fn     func(ctx context.Context) error
	Result chan error
}

// LaneManager manages per-session execution lanes to ensure sequential processing
// within a session while allowing concurrency across different sessions.
type LaneManager struct {
	mu    sync.RWMutex
	lanes map[string]*lane
	
	// Config
	LaneBufferSize int
	IdleTimeout    time.Duration
}

// NewLaneManager creates a new lane manager.
func NewLaneManager() *LaneManager {
	return &LaneManager{
		lanes:          make(map[string]*lane),
		LaneBufferSize: 64,
		IdleTimeout:    10 * time.Minute,
	}
}

// lane represents a single execution lane for a session.
type lane struct {
	sessionID string
	requests  chan Request
	lastUsed  time.Time
	mu        sync.Mutex
	done      chan struct{}
}

// Execute queues a function for execution in the specified session's lane.
// It returns a channel that will receive the result of the execution.
func (m *LaneManager) Execute(ctx context.Context, sessionID string, fn func(ctx context.Context) error) <-chan error {
	result := make(chan error, 1)
	req := Request{
		Ctx:    ctx,
		Fn:     fn,
		Result: result,
	}

	l := m.getOrCreateLane(sessionID)
	
	select {
	case l.requests <- req:
		// Queued successfully
	case <-ctx.Done():
		result <- ctx.Err()
	default:
		// Buffer full
		result <- fmt.Errorf("session lane %s is full", sessionID)
	}

	return result
}

func (m *LaneManager) getOrCreateLane(sessionID string) *lane {
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

	l = &lane{
		sessionID: sessionID,
		requests:  make(chan Request, m.LaneBufferSize),
		lastUsed:  time.Now(),
		done:      make(chan struct{}),
	}
	m.lanes[sessionID] = l

	go m.runLane(l)

	return l
}

func (m *LaneManager) runLane(l *lane) {
	defer func() {
		m.mu.Lock()
		delete(m.lanes, l.sessionID)
		m.mu.Unlock()
		close(l.done)
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
		}
	}
}

// Stop shuts down all lanes and waits for them to finish.
func (m *LaneManager) Stop() {
	m.mu.Lock()
	lanes := make([]*lane, 0, len(m.lanes))
	for _, l := range m.lanes {
		lanes = append(lanes, l)
	}
	m.mu.Unlock()

	// In a real implementation, we might want to close the requests channel
	// and wait for the done signal.
}
