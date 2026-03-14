package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
)

// HTTPAdapter implements runtime.ChannelAdapter to expose Canto over HTTP.
// It supports basic REST requests and SSE streaming (to be expanded).
type HTTPAdapter struct {
	addr    string
	server  *http.Server
	handler runtime.ChannelHandler
}

// NewHTTPAdapter creates a new HTTP channel adapter.
func NewHTTPAdapter(addr string) *HTTPAdapter {
	return &HTTPAdapter{
		addr: addr,
	}
}

// Name returns the identifier for this channel.
func (a *HTTPAdapter) Name() string {
	return "http"
}

// Listen starts the HTTP server.
func (a *HTTPAdapter) Listen(ctx context.Context, handler runtime.ChannelHandler) error {
	a.handler = handler

	mux := http.NewServeMux()
	// Go 1.22+ routing syntax
	mux.HandleFunc("POST /v1/chat", a.handleChat)
	mux.HandleFunc("GET /v1/chat/stream", a.handleStream)

	a.server = &http.Server{
		Addr:    a.addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			fmt.Printf("http channel: server shutdown error: %v\n", err)
		}
	}()

	fmt.Printf("HTTP Channel Adapter listening on %s\n", a.addr)
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

type chatRequest struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	Message   string `json:"message"`
}

type chatResponse struct {
	SessionID string   `json:"session_id"`
	Messages  []string `json:"messages"`
}

func (a *HTTPAdapter) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	channelReq := runtime.ChannelRequest{
		SessionID: req.SessionID,
		AgentID:   req.AgentID,
		Message: llm.Message{
			Role:    llm.RoleUser,
			Content: req.Message,
		},
	}

	channelResp, err := a.handler.Handle(r.Context(), channelReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := chatResponse{
		SessionID: channelResp.SessionID,
		Messages:  make([]string, len(channelResp.Messages)),
	}

	for i, m := range channelResp.Messages {
		resp.Messages[i] = m.Content
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *HTTPAdapter) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}
	message := r.URL.Query().Get("message")

	channelReq := runtime.ChannelRequest{
		SessionID: sessionID,
		Message: llm.Message{
			Role:    llm.RoleUser,
			Content: message,
		},
	}

	// This assumes the handler blocks and returns a single response for now.
	// For true streaming, the ChannelHandler interface would need to support
	// returning a channel or callback for incremental updates. 
	// As a basic implementation, we just send the final result as a single SSE event.
	channelResp, err := a.handler.Handle(r.Context(), channelReq)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	for _, m := range channelResp.Messages {
		b, _ := json.Marshal(m.Content)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()
	}

	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

