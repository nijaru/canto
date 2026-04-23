package coding

import (
	"fmt"
	"strings"
)

// OutputCompressor rewrites command output into a shorter, model-friendly form.
type OutputCompressor interface {
	Compress(Result) Result
}

// LineOutputCompressor is the default tool-output compressor.
//
// It keeps the transformation conservative: exact repeated lines collapse to a
// counted summary and runs of blank lines collapse to a single blank line.
// That captures the high-value RTK-style win without trying to infer semantics
// from arbitrary command output.
type LineOutputCompressor struct{}

var _ OutputCompressor = (*LineOutputCompressor)(nil)

// NewLineOutputCompressor returns the default line-oriented compressor.
func NewLineOutputCompressor() *LineOutputCompressor {
	return &LineOutputCompressor{}
}

// Compress shortens the combined tool output while leaving stdout/stderr
// intact for callers that need the raw streams.
func (c *LineOutputCompressor) Compress(result Result) Result {
	result.Combined = compressText(result.Combined)
	return result
}

func compressText(text string) string {
	if text == "" {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	out := make([]string, 0, len(lines))
	flush := func(line string, count int) {
		switch {
		case count == 0:
			return
		case line == "":
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
		case count == 1:
			out = append(out, line)
		default:
			out = append(out, fmt.Sprintf("%dx %s", count, line))
		}
	}

	current := ""
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush(current, count)
			current = ""
			count = 0
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		if count > 0 && line == current {
			count++
			continue
		}
		flush(current, count)
		current = line
		count = 1
	}
	flush(current, count)

	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
