package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// OmenDBStore implements VectorStore by communicating with a running OmenDB
// process over a Unix domain socket using JSON-RPC 2.0.
//
// OmenDB handles its own memory management and HNSW index; this adapter is
// a thin transport layer. No CGo required — zero framework coupling.
//
// Start OmenDB independently before calling NewOmenDBStore. This adapter
// does not manage the OmenDB process lifecycle.
//
// Example:
//
//	store, err := memory.NewOmenDBStore("/run/omendb/omendb.sock")
//	if err != nil { ... } // fails fast if socket is unreachable
//	defer store.Close()
type OmenDBStore struct {
	socketPath string
	idGen      atomic.Uint64
	dialTimeout time.Duration
}

// NewOmenDBStore creates an OmenDBStore and verifies the socket is reachable.
// Returns an error if OmenDB is not listening at socketPath.
func NewOmenDBStore(socketPath string) (*OmenDBStore, error) {
	s := &OmenDBStore{
		socketPath:  socketPath,
		dialTimeout: 3 * time.Second,
	}
	if err := s.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("omendb: connect %q: %w", socketPath, err)
	}
	return s, nil
}

// Ping sends a health-check RPC to OmenDB. Returns an error if unreachable.
func (s *OmenDBStore) Ping(ctx context.Context) error {
	_, err := s.call(ctx, "ping", nil)
	return err
}

// Upsert inserts or updates a vector in OmenDB.
func (s *OmenDBStore) Upsert(ctx context.Context, id string, vector []float32, metadata map[string]any) error {
	_, err := s.call(ctx, "upsert", map[string]any{
		"id":       id,
		"vector":   vector,
		"metadata": metadata,
	})
	return err
}

// Search returns the k nearest neighbours to vector with optional metadata filtering.
// filter keys and values are passed directly to OmenDB's ACORN-1 filtered search.
// Pass nil for unfiltered search.
func (s *OmenDBStore) Search(ctx context.Context, vector []float32, k int, filter map[string]any) ([]SearchResult, error) {
	params := map[string]any{
		"vector": vector,
		"k":      k,
	}
	if filter != nil {
		params["filter"] = filter
	}

	raw, err := s.call(ctx, "search", params)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("omendb: decode search results: %w", err)
	}
	return results, nil
}

// Delete removes a vector from OmenDB by ID.
func (s *OmenDBStore) Delete(ctx context.Context, id string) error {
	_, err := s.call(ctx, "delete", map[string]any{"id": id})
	return err
}

// --- internal JSON-RPC transport ---

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("omendb rpc error %d: %s", e.Code, e.Message)
}

// call opens a new connection per request, sends one JSON-RPC call, and returns
// the raw Result field. A new connection per request is simple and correct for
// a Unix socket; connection pooling can be added when profiling shows it matters.
func (s *OmenDBStore) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	dialer := net.Dialer{Timeout: s.dialTimeout}
	conn, err := dialer.DialContext(ctx, "unix", s.socketPath)
	if err != nil {
		return nil, fmt.Errorf("omendb: dial: %w", err)
	}
	defer conn.Close()

	// Respect context deadline on the socket I/O.
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl) //nolint:errcheck
	}

	id := s.idGen.Add(1)
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("omendb: encode request: %w", err)
	}

	var resp rpcResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("omendb: decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}
