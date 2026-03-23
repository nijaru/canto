package target

import (
	"strings"
	"testing"
)

var (
	text = strings.Repeat("hello world and universe ", 1000)
	subs = []string{"notfound", "missing", "absent", "universe"}
)

func BenchmarkContainsAny(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = ContainsAny(text, subs...)
	}
}

// Ensure the implementation is correct before benchmarking it.
func TestContainsAny(t *testing.T) {
	if !ContainsAny("hello world", "world") {
		t.Fatal("expected true")
	}
	if ContainsAny("hello world", "universe") {
		t.Fatal("expected false")
	}
}
