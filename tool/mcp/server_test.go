package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

// echoTool is a minimal tool.Tool for testing.
type echoTool struct{ name string }

func (e *echoTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        e.name,
		Description: "Echoes the input.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"msg": map[string]any{"type": "string"}},
			"required":   []string{"msg"},
		},
	}
}

func (e *echoTool) Execute(_ context.Context, args string) (string, error) {
	var p struct {
		Msg string `json:"msg"`
	}
	_ = json.Unmarshal([]byte(args), &p)
	return "echo: " + p.Msg, nil
}

func newTestServer() (*Server, *tool.Registry) {
	reg := tool.NewRegistry()
	reg.Register(&echoTool{name: "echo"})
	return NewServer(reg, "test", "0.1"), reg
}

// roundtrip sends req to the server and returns the response object.
func roundtrip(t *testing.T, srv *Server, req string) map[string]any {
	t.Helper()
	r := strings.NewReader(req + "\n")
	var w strings.Builder
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, r, &w) }()
	<-done

	out := strings.TrimSpace(w.String())
	if out == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("response is not valid JSON: %q — %v", out, err)
	}
	return m
}

func TestServer_Initialize(t *testing.T) {
	srv, _ := newTestServer()
	resp := roundtrip(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %v", resp["result"])
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion: got %v, want %v", result["protocolVersion"], protocolVersion)
	}
	if result["serverInfo"] == nil {
		t.Error("serverInfo must be present")
	}
}

func TestServer_ToolsList(t *testing.T) {
	srv, _ := newTestServer()
	resp := roundtrip(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result := resp["result"].(map[string]any)
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", result["tools"])
	}
	spec := tools[0].(map[string]any)
	if spec["name"] != "echo" {
		t.Errorf("tool name: got %v, want echo", spec["name"])
	}
	schema := spec["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("inputSchema.type: got %v, want object", schema["type"])
	}
}

func TestServer_ToolsCall(t *testing.T) {
	srv, _ := newTestServer()
	req := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hello"}}}`
	resp := roundtrip(t, srv, req)

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Error("isError should be false for successful call")
	}
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}
	block := content[0].(map[string]any)
	if block["text"] != "echo: hello" {
		t.Errorf("text: got %v, want %q", block["text"], "echo: hello")
	}
}

func TestServer_ToolsCall_UnknownTool(t *testing.T) {
	srv, _ := newTestServer()
	req := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"no-such-tool","arguments":{}}}`
	resp := roundtrip(t, srv, req)

	// Unknown tool returns isError:true, not a JSON-RPC error.
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Error("unknown tool call must set isError:true")
	}
}

func TestServer_UnknownMethod(t *testing.T) {
	srv, _ := newTestServer()
	resp := roundtrip(t, srv, `{"jsonrpc":"2.0","id":5,"method":"no/such","params":{}}`)

	if resp["error"] == nil {
		t.Fatal("expected JSON-RPC error for unknown method")
	}
	errObj := resp["error"].(map[string]any)
	if int(errObj["code"].(float64)) != codeMethodNotFound {
		t.Errorf("code: got %v, want %d", errObj["code"], codeMethodNotFound)
	}
}

func TestServer_Notification_NoResponse(t *testing.T) {
	srv, _ := newTestServer()
	// Notifications have no id — server must not write a response.
	resp := roundtrip(t, srv, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp != nil {
		t.Errorf("notification must produce no response, got: %v", resp)
	}
}

func TestServer_ParseError(t *testing.T) {
	srv, _ := newTestServer()
	resp := roundtrip(t, srv, `not json`)
	if resp["error"] == nil {
		t.Fatal("expected parse error")
	}
	errObj := resp["error"].(map[string]any)
	if int(errObj["code"].(float64)) != codeParseError {
		t.Errorf("code: got %v, want %d", errObj["code"], codeParseError)
	}
}
