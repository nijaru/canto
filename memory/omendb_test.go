package memory

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// startMockOmenDB launches a minimal JSON-RPC server over a Unix socket.
// It handles upsert, search, delete, and ping with canned responses.
// Returns the socket path and a cleanup function.
func startMockOmenDB(t *testing.T) (socketPath string, cleanup func()) {
	t.Helper()

	dir := t.TempDir()
	socketPath = filepath.Join(dir, "omendb.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("mock omendb: listen: %v", err)
	}

	// Serve in background; handle one connection per request.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go handleMockConn(conn)
		}
	}()

	return socketPath, func() {
		ln.Close()
		os.Remove(socketPath)
	}
}

func handleMockConn(conn net.Conn) {
	defer conn.Close()

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}

	type response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Result  any    `json:"result"`
	}

	var result any
	switch req.Method {
	case "ping":
		result = "pong"
	case "upsert":
		result = "ok"
	case "delete":
		result = "ok"
	case "search":
		// Return two canned SearchResult values.
		result = []map[string]any{
			{"ID": "vec-a", "Score": float32(0.99), "Metadata": map[string]any{"label": "a"}},
			{"ID": "vec-b", "Score": float32(0.75), "Metadata": map[string]any{"label": "b"}},
		}
	default:
		result = nil
	}

	resp := response{JSONRPC: "2.0", ID: req.ID, Result: result}
	json.NewEncoder(conn).Encode(resp) //nolint:errcheck
}

func TestOmenDBStore(t *testing.T) {
	socketPath, cleanup := startMockOmenDB(t)
	defer cleanup()

	store, err := NewOmenDBStore(socketPath)
	if err != nil {
		t.Fatalf("NewOmenDBStore: %v", err)
	}

	ctx := context.Background()
	vec := []float32{0.1, 0.2, 0.3}

	// Upsert
	if err := store.Upsert(ctx, "vec-a", vec, map[string]any{"label": "a"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search (with and without filter)
	results, err := store.Search(ctx, vec, 2, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 search results, got %d", len(results))
	}
	if results[0].ID != "vec-a" {
		t.Errorf("expected first result ID vec-a, got %q", results[0].ID)
	}

	// Search with filter passthrough (mock ignores it, just verifying no error).
	_, err = store.Search(ctx, vec, 1, map[string]any{"label": "a"})
	if err != nil {
		t.Fatalf("Search with filter: %v", err)
	}

	// Delete
	if err := store.Delete(ctx, "vec-a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
