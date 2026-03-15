package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync/atomic"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

// Client represents a connection to an MCP server via stdio.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	nextID atomic.Uint64
	tools  []tool.Tool
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
		stdout: stdout,
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

// initialize sends the initialize JSON-RPC request to the MCP server.
func (c *Client) initialize() error {
	// A complete implementation would send the initialize request, wait for the response,
	// and then send the initialized notification. For now, we mock the flow.
	return nil
}

// DiscoverTools fetches available tools from the MCP server.
func (c *Client) DiscoverTools() ([]tool.Tool, error) {
	// A complete implementation would send tools/list request and parse the result.
	return nil, fmt.Errorf("mcp: DiscoverTools not implemented")
}

// CallTool executes a tool on the MCP server.
func (c *Client) CallTool(
	ctx context.Context,
	name string,
	arguments map[string]any,
) (string, error) {
	// A complete implementation would send tools/call request and wait for the result.
	return "", fmt.Errorf("not implemented")
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
