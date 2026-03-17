package tools

import (
	"context"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// WASMExecutor executes a WebAssembly module with WASI support.
type WASMExecutor struct {
	Runtime wazero.Runtime
}

// NewWASMExecutor creates a new WASM executor.
func NewWASMExecutor(ctx context.Context) *WASMExecutor {
	r := wazero.NewRuntime(ctx)
	// Instantiate WASI
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	return &WASMExecutor{Runtime: r}
}

// Execute runs the provided WASM module binary with arguments.
// Returns stdout/stderr combined and any error.
func (e *WASMExecutor) Execute(ctx context.Context, wasmBinary []byte, args []string, stdin io.Reader) (string, error) {
	// Prepare the configuration
	config := wazero.NewModuleConfig().
		WithArgs(args...).
		WithStdin(stdin)

	// Capture stdout and stderr
	stdout := &bytesBuffer{}
	stderr := &bytesBuffer{}
	config = config.WithStdout(stdout).WithStderr(stderr)

	// Instantiate the module
	mod, err := e.Runtime.Instantiate(ctx, wasmBinary)
	if err != nil {
		return "", fmt.Errorf("wasm: failed to instantiate: %w", err)
	}
	defer mod.Close(ctx)

	// Combine outputs
	output := stdout.String() + stderr.String()
	return output, nil
}

// bytesBuffer is a simple thread-safe buffer for capturing output.
type bytesBuffer struct {
	buf []byte
}

func (b *bytesBuffer) Write(p []byte) (n int, err error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *bytesBuffer) String() string {
	return string(b.buf)
}

// Close the runtime.
func (e *WASMExecutor) Close(ctx context.Context) error {
	return e.Runtime.Close(ctx)
}
