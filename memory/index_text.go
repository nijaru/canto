package memory

import (
	"strings"
	"unicode"
)

func summarizeIndexText(metadata map[string]any, content string, maxRunes int) string {
	if summary, ok := metadata["summary"].(string); ok && summary != "" {
		return clipIndexText(summary, maxRunes)
	}
	return clipIndexText(content, maxRunes)
}

func clipIndexText(content string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 120
	}
	collapsed := strings.Join(strings.Fields(content), " ")
	runes := []rune(collapsed)
	if len(runes) <= maxRunes {
		return collapsed
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func memoryLeafName(memory Memory) string {
	if memory.Key != "" {
		return memory.Key
	}
	if title, ok := memory.Metadata["title"].(string); ok && title != "" {
		return title
	}
	return "memory-" + shortIndexID(memory.ID)
}

func shortIndexID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func sanitizeIndexSegment(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == '.':
			if !lastDash {
				b.WriteRune(r)
				lastDash = r == '-'
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
