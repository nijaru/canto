package runtime

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/session"
)

// getOrLoad returns the cached session for sessionID, loading it from the
// store on first access. All Runner methods use this so that Watch and
// execute always operate on the same in-memory object.
func (r *Runner) getOrLoad(ctx context.Context, sessionID string) (*session.Session, error) {
	r.mu.Lock()
	if sess, ok := r.sessions[sessionID]; ok {
		r.mu.Unlock()
		return sess, nil
	}
	r.mu.Unlock()

	// Load outside the lock; Store.Load may involve I/O.
	sess, err := r.store.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check: another goroutine may have loaded the same session.
	if existing, ok := r.sessions[sessionID]; ok {
		return existing, nil
	}
	r.sessions[sessionID] = sess
	return sess, nil
}

// Evict removes sessionID from the in-memory registry. The session remains
// in the persistent store; the next access reloads it from there. Use this
// to release memory for idle sessions.
//
// Eviction is a no-op if the session has an active execution lane or
// live subscribers.
func (r *Runner) Evict(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sess, ok := r.sessions[sessionID]
	if !ok {
		return
	}

	if sess.HasWatchers() {
		return
	}

	if r.queue != nil && r.queue.IsActive(sessionID) {
		return
	}

	delete(r.sessions, sessionID)
}

// Watch returns a live, lossy stream of events for the given session.
//
// Events emitted by Run/Send on the same Runner are delivered to this
// subscription because both share the same in-memory session object.
func (r *Runner) Watch(ctx context.Context, sessionID string) (*session.Subscription, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return sess.Watch(ctx), nil
}

// Search searches the session history for the given query.
func (r *Runner) Search(ctx context.Context, sessionID, query string) ([]session.Event, error) {
	searchStore, ok := r.store.(session.SearchStore)
	if !ok {
		return nil, fmt.Errorf("session store does not support search")
	}
	return searchStore.Search(ctx, sessionID, query)
}

// Bootstrap records an environment snapshot as model-visible context so the
// first turn has workspace and tool context up front.
func (r *Runner) Bootstrap(ctx context.Context, sessionID string, snap Bootstrap) error {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return err
	}
	return snap.Append(ctx, sess)
}
