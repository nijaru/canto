package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-json-experiment/json"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// Indexer monitors sessions and automatically indexes new text messages
// into a vector store. It ensures that semantic search always has the latest
// conversation context available.
//
// The caller must defer Stop to release background goroutines.
type Indexer struct {
	store    VectorStore
	embedder llm.Embedder
	onError  func(error)

	mu       sync.Mutex
	watching map[string]context.CancelFunc
	wg       sync.WaitGroup
}

// NewIndexer creates a new indexer that writes to store using embedder.
// onError is called for non-fatal background errors (embedding failures, upsert
// failures). Pass nil to ignore errors.
func NewIndexer(
	store VectorStore,
	embedder llm.Embedder,
	onError func(error),
) *Indexer {
	return &Indexer{
		store:    store,
		embedder: embedder,
		onError:  onError,
		watching: make(map[string]context.CancelFunc),
	}
}

// Watch starts monitoring a session for new messages. It returns immediately;
// indexing happens in a background goroutine. If the session is already being
// watched, this is a no-op.
func (i *Indexer) Watch(ctx context.Context, sess *session.Session) {
	i.mu.Lock()
	if _, ok := i.watching[sess.ID()]; ok {
		i.mu.Unlock()
		return
	}
	watchCtx, cancel := context.WithCancel(ctx)
	i.watching[sess.ID()] = cancel
	i.mu.Unlock()

	i.wg.Go(func() {
		defer func() {
			i.mu.Lock()
			delete(i.watching, sess.ID())
			i.mu.Unlock()
		}()

		sub := sess.Watch(watchCtx)
		defer sub.Close()
		events := sub.Events()
		for {
			select {
			case <-watchCtx.Done():
				return
			case e, ok := <-events:
				if !ok {
					return
				}
				if e.Type == session.MessageAdded || e.Type == session.ContextAdded {
					i.indexMessage(watchCtx, sess.ID(), e)
				}
			}
		}
	})
}

func (i *Indexer) indexMessage(ctx context.Context, sessionID string, e session.Event) {
	if e.Type == session.ContextAdded {
		i.indexContext(ctx, sessionID, e)
		return
	}

	var msg llm.Message
	if err := json.Unmarshal(e.Data, &msg); err != nil {
		i.handleError(fmt.Errorf("indexer: unmarshal message: %w", err))
		return
	}

	// Only index messages with text content; tool calls/results have no
	// meaningful embedding surface for conversation recall.
	if msg.Content == "" {
		return
	}

	id := fmt.Sprintf("%s:%s", sessionID, e.ID.String())

	vector, err := i.embedder.EmbedContent(ctx, msg.Content)
	if err != nil {
		i.handleError(fmt.Errorf("indexer: embed %s: %w", id, err))
		return
	}

	metadata := map[string]any{
		"session_id": sessionID,
		"event_id":   e.ID.String(),
		"role":       string(msg.Role),
		"content":    msg.Content,
	}

	if err := i.store.Upsert(ctx, id, vector, metadata); err != nil {
		i.handleError(fmt.Errorf("indexer: upsert %s: %w", id, err))
	}
}

func (i *Indexer) indexContext(ctx context.Context, sessionID string, e session.Event) {
	var entry session.ContextEntry
	if err := json.Unmarshal(e.Data, &entry); err != nil {
		i.handleError(fmt.Errorf("indexer: unmarshal context: %w", err))
		return
	}
	if entry.Content == "" {
		return
	}

	id := fmt.Sprintf("%s:%s", sessionID, e.ID.String())

	vector, err := i.embedder.EmbedContent(ctx, entry.Content)
	if err != nil {
		i.handleError(fmt.Errorf("indexer: embed %s: %w", id, err))
		return
	}

	metadata := map[string]any{
		"session_id": sessionID,
		"event_id":   e.ID.String(),
		"role":       "context",
		"kind":       string(entry.Kind),
		"content":    entry.Content,
	}

	if err := i.store.Upsert(ctx, id, vector, metadata); err != nil {
		i.handleError(fmt.Errorf("indexer: upsert %s: %w", id, err))
	}
}

func (i *Indexer) handleError(err error) {
	if i.onError != nil {
		i.onError(err)
	}
}

// Stop cancels all active watchers and waits for background goroutines to exit.
func (i *Indexer) Stop() {
	i.mu.Lock()
	for _, cancel := range i.watching {
		cancel()
	}
	i.mu.Unlock()
	i.wg.Wait()
}
