package mcp

import (
	"bufio"
	"context"
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"fmt"
	"io"
	"sync"

	"github.com/nijaru/canto/tool"
)

const protocolVersion = "2024-11-05"

// Server exposes a tool.Registry over the MCP stdio JSON-RPC protocol.
// It reads newline-delimited JSON-RPC requests from r and writes responses to w.
type Server struct {
	reg     *tool.Registry
	name    string
	version string
}

// NewServer creates a new MCP server backed by the given registry.
func NewServer(reg *tool.Registry, name, version string) *Server {
	return &Server{reg: reg, name: name, version: version}
}

// Serve reads JSON-RPC requests from r and writes responses to w until ctx is
// cancelled or r returns EOF. Each request is handled synchronously in order;
// the write side is mutex-protected so future parallel dispatch is safe to add.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	var mu sync.Mutex
	write := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	}

	scanner := bufio.NewScanner(r)
	// MCP messages can be large (tool results with file content, etc.).
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			return nil // EOF
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = write(errResponse(nil, codeParseError, "parse error: "+err.Error()))
			continue
		}
		if req.JSONRPC != "2.0" {
			_ = write(errResponse(req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\""))
			continue
		}

		// Notifications have no id — no response is sent.
		isNotification := req.ID == nil
		resp, err := s.dispatch(ctx, &req)
		if err != nil {
			if !isNotification {
				_ = write(errResponse(req.ID, codeInternalError, err.Error()))
			}
			continue
		}
		if resp != nil {
			_ = write(resp)
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req *jsonrpcRequest) (any, error) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil, nil // notification, no response
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		if req.ID == nil {
			return nil, nil // unknown notification, ignore
		}
		return errResponse(req.ID, codeMethodNotFound, "method not found: "+req.Method), nil
	}
}

func (s *Server) handleInitialize(req *jsonrpcRequest) (any, error) {
	result := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": s.name, "version": s.version},
	}
	return okResponse(req.ID, result), nil
}

func (s *Server) handleToolsList(req *jsonrpcRequest) (any, error) {
	specs := s.reg.Specs()
	tools := make([]mcpToolSpec, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, mcpToolSpec{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: normalizeSchema(spec.Parameters),
		})
	}
	return okResponse(req.ID, map[string]any{"tools": tools}), nil
}

func (s *Server) handleToolsCall(ctx context.Context, req *jsonrpcRequest) (any, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errResponse(req.ID, codeInvalidParams, "invalid params: "+err.Error()), nil
	}
	if params.Name == "" {
		return errResponse(req.ID, codeInvalidParams, "name is required"), nil
	}

	argsJSON, err := json.Marshal(params.Arguments)
	if err != nil {
		return errResponse(req.ID, codeInvalidParams, "cannot marshal arguments: "+err.Error()), nil
	}

	output, execErr := s.reg.Execute(ctx, params.Name, string(argsJSON))
	isError := execErr != nil
	text := output
	if isError {
		text = execErr.Error()
	}

	result := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
	return okResponse(req.ID, result), nil
}

// normalizeSchema converts a ToolSpec.Parameters (any JSON-serializable value)
// to a map[string]any suitable for the MCP inputSchema field.
func normalizeSchema(params any) map[string]any {
	base := map[string]any{"type": "object"}
	if params == nil {
		return base
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return base
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return base
	}
	if _, hasType := m["type"]; !hasType {
		m["type"] = "object"
	}
	return m
}

// JSON-RPC 2.0 types.

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      jsontext.Value `json:"id,omitzero"` // number, string, or null
	Method  string          `json:"method"`
	Params  jsontext.Value `json:"params,omitzero"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Result  any             `json:"result,omitzero"`
	Error   *jsonrpcError   `json:"error,omitzero"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

func okResponse(id jsontext.Value, result any) *jsonrpcResponse {
	return &jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResponse(id jsontext.Value, code int, msg string) *jsonrpcResponse {
	return &jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: msg},
	}
}
