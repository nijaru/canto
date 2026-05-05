package runtime

import (
	"sync"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/session"
)

const (
	defaultWaitTimeout      = 30 * time.Second
	defaultExecutionTimeout = 2 * time.Minute
)

// Runner orchestrates the execution of an agent within a session.
// By default it uses built-in local coordination to serialize execution within
// a session while allowing concurrent execution across different sessions.
//
// Runner maintains an in-memory session registry so that Watch, Send,
// Run, and execute all share the same *session.Session object for a given
// session ID. This is required for Watch to receive events emitted by
// execute — without a shared object the channel is permanently silent.
type Runner struct {
	store            session.Store
	agent            agent.Agent
	waitTimeout      time.Duration
	executionTimeout time.Duration
	coordinator      Coordinator
	hooks            *hook.Runner
	scheduler        Scheduler
	beforeRun        []SessionFunc
	overflowRecovery overflowRecoveryOptions

	queue       *serialQueue
	childRunner *ChildRunner
	mu          sync.Mutex
	sessions    map[string]*session.Session
}

// NewRunner creates a Runner with per-session coordination enabled.
func NewRunner(s session.Store, a agent.Agent, opts ...Option) *Runner {
	cfg := applyOptions(opts)
	scheduler := cfg.scheduler
	if scheduler == nil {
		scheduler = NewLocalScheduler()
	}
	return &Runner{
		store:            s,
		agent:            a,
		waitTimeout:      cfg.waitTimeout,
		executionTimeout: cfg.executionTimeout,
		coordinator:      cfg.coordinator,
		queue:            newSerialQueue(),
		childRunner:      NewChildRunner(s, runnerChildOptions(cfg)...),
		hooks:            cfg.hooks,
		scheduler:        scheduler,
		beforeRun:        append([]SessionFunc(nil), cfg.beforeRun...),
		overflowRecovery: cfg.overflowRecovery,
		sessions:         make(map[string]*session.Session),
	}
}

// Close gracefully stops the internal local coordinator and any active goroutines.
func (r *Runner) Close() {
	if r.scheduler != nil {
		r.scheduler.Close()
	}
	if r.queue != nil {
		r.queue.stop()
	}
	if r.childRunner != nil {
		r.childRunner.Close()
	}
}
