package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

// Client represents a connection to an MCP server via stdio.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex // serializes requests; MCP stdio is sequential
	nextID atomic.Uint64
}

// NewStdioClient starts an MCP server process and connects via stdio.
func NewStdioClient(ctx context.Context, command string, args ...string) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	go func() {
		_ = cmd.Wait()
	}()

	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}

	if err := c.initialize(); err != nil {
		c.Close()
		return nil, err
	}

	return c, nil
}

// Close shuts down the client and the underlying process.
func (c *Client) Close() error {
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

// call sends a JSON-RPC request and returns the parsed response.
// Caller must hold c.mu.
func (c *Client) call(method string, params any) (*jsonrpcResponse, error) {
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      idRaw,
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp: marshal params: %w", err)
		}
		req.Params = raw
	}

	enc, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal request: %w", err)
	}
	enc = append(enc, '\n')
	if _, err := c.stdin.Write(enc); err != nil {
		return nil, fmt.Errorf("mcp: write: %w", err)
	}

	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("mcp: read response: %w", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("mcp: parse response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp: server error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return &resp, nil
}

// notify sends a JSON-RPC notification (no response expected).
// Caller must hold c.mu.
func (c *Client) notify(method string) error {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
	}
	enc, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("mcp: marshal notification: %w", err)
	}
	enc = append(enc, '\n')
	_, err = c.stdin.Write(enc)
	return err
}

// initialize sends the initialize request and initialized notification.
func (c *Client) initialize() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "canto", "version": "0.0.1"},
	}
	if _, err := c.call("initialize", params); err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}
	return c.notify("notifications/initialized")
}

// DiscoverTools fetches available tools from the MCP server and returns them
// as tool.Tool values that can be registered in a tool.Registry.
func (c *Client) DiscoverTools() ([]tool.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.call("tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", err)
	}

	var result struct {
		Tools []mcpToolSpec `json:"tools"`
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal result: %w", err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/list result: %w", err)
	}

	tools := make([]tool.Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, &wrapper{
			client: c,
			spec: llm.ToolSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return tools, nil
}

// CallTool executes a tool on the MCP server and returns its text output.
func (c *Client) CallTool(
	ctx context.Context,
	name string,
	arguments map[string]any,
) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}
	resp, err := c.call("tools/call", params)
	if err != nil {
		return "", fmt.Errorf("mcp tools/call %q: %w", name, err)
	}

	// Extract text content from the result.
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		return "", fmt.Errorf("mcp: marshal result: %w", err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("mcp: parse tools/call result: %w", err)
	}

	var text string
	for _, c := range result.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	if result.IsError {
		return "", fmt.Errorf("mcp tool %q error: %s", name, text)
	}
	return text, nil
}

// wrapper implements the tool.Tool interface for an MCP tool.
type wrapper struct {
	client *Client
	spec   llm.ToolSpec
}

func (w *wrapper) Spec() llm.ToolSpec {
	return w.spec
}

func (w *wrapper) Execute(ctx context.Context, args string) (string, error) {
	var parsedArgs map[string]any
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}
	return w.client.CallTool(ctx, w.spec.Name, parsedArgs)
}
