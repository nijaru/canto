package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	ErrSchedulerClosed      = errors.New("scheduler closed")
	ErrScheduledTaskStarted = errors.New("scheduled task already started")
	ErrScheduledTaskDone    = errors.New("scheduled task already completed")
)

// ScheduleRef identifies a queued scheduled task.
type ScheduleRef struct {
	ID     string
	DueAt  time.Time
	Queued time.Time
}

// ScheduledTask provides wait/cancel semantics for a scheduled callback.
type ScheduledTask interface {
	Ref() ScheduleRef
	Wait(ctx context.Context) error
	Cancel(ctx context.Context) error
}

// Scheduler schedules one-shot callbacks for later execution.
type Scheduler interface {
	Schedule(
		ctx context.Context,
		dueAt time.Time,
		fn func(context.Context) error,
	) (ScheduledTask, error)
	Close()
}

// LocalScheduler is an in-memory timer-backed scheduler substrate.
type LocalScheduler struct {
	mu     sync.Mutex
	tasks  map[string]*scheduledTask
	closed bool
}

// NewLocalScheduler constructs an in-memory scheduler.
func NewLocalScheduler() *LocalScheduler {
	return &LocalScheduler{
		tasks: make(map[string]*scheduledTask),
	}
}

// Close cancels every pending task.
func (s *LocalScheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	tasks := make([]*scheduledTask, 0, len(s.tasks))
	for _, task := range s.tasks {
		tasks = append(tasks, task)
	}
	s.tasks = make(map[string]*scheduledTask)
	s.mu.Unlock()

	for _, task := range tasks {
		_ = task.cancelTask(ErrSchedulerClosed)
	}
}

// Schedule registers fn for execution at dueAt.
func (s *LocalScheduler) Schedule(
	ctx context.Context,
	dueAt time.Time,
	fn func(context.Context) error,
) (ScheduledTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fn == nil {
		return nil, errors.New("scheduler: nil callback")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSchedulerClosed
	}

	now := time.Now().UTC()
	ref := ScheduleRef{
		ID:     ulid.Make().String(),
		DueAt:  dueAt.UTC(),
		Queued: now,
	}
	runCtx, cancel := context.WithCancel(context.Background())
	task := &scheduledTask{
		ref:       ref,
		fn:        fn,
		runCtx:    runCtx,
		runCancel: cancel,
		done:      make(chan struct{}),
		owner:     s,
	}
	delay := time.Until(ref.DueAt)
	if delay < 0 {
		delay = 0
	}
	task.mu.Lock()
	task.timer = time.AfterFunc(delay, task.start)
	s.tasks[ref.ID] = task
	task.mu.Unlock()
	return task, nil
}

func (s *LocalScheduler) removeTask(id string) {
	s.mu.Lock()
	delete(s.tasks, id)
	s.mu.Unlock()
}
