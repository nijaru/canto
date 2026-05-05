package coding

import (
	"errors"
	"io"
	"sync"
)

type outputCollector struct {
	max      int
	onOutput func(OutputChunk)

	mu        sync.Mutex
	remaining int
	stdout    []byte
	stderr    []byte
	combined  []byte
	truncated bool
}

func newOutputCollector(max int, onOutput func(OutputChunk)) *outputCollector {
	return &outputCollector{
		max:       max,
		onOutput:  onOutput,
		remaining: max,
	}
}

func (c *outputCollector) readStream(stream OutputStream, r io.Reader) error {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.capture(stream, buf[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (c *outputCollector) capture(stream OutputStream, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.max > 0 {
		if c.remaining <= 0 {
			c.truncated = true
			return
		}
		if len(data) > c.remaining {
			data = data[:c.remaining]
			c.truncated = true
		}
		c.remaining -= len(data)
	}

	switch stream {
	case StdoutStream:
		c.stdout = append(c.stdout, data...)
	case StderrStream:
		c.stderr = append(c.stderr, data...)
	}
	c.combined = append(c.combined, data...)
	if c.onOutput != nil {
		c.onOutput(OutputChunk{Stream: stream, Text: string(data)})
	}
}

func (c *outputCollector) result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := Result{
		Stdout:    string(c.stdout),
		Stderr:    string(c.stderr),
		Combined:  string(c.combined),
		Truncated: c.truncated,
	}
	if result.Truncated {
		result.Combined += "\n\n[Output truncated due to size limits]"
	}
	return result
}
