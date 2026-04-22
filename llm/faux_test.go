package llm

import "testing"

func TestFauxProviderGenerateConsumesSteps(t *testing.T) {
	provider := NewFauxProvider("test", FauxStep{Content: "one"}, FauxStep{Content: "two"})
	for _, want := range []string{"one", "two"} {
		resp, err := provider.Generate(t.Context(), &Request{})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if resp.Content != want {
			t.Fatalf("content = %q, want %q", resp.Content, want)
		}
	}
	if provider.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0", provider.Remaining())
	}
}

func TestFauxProviderStream(t *testing.T) {
	provider := NewFauxProvider("test", FauxStep{
		Chunks: []Chunk{{Content: "a"}, {Content: "b"}},
	})
	stream, err := provider.Stream(t.Context(), &Request{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got string
	for {
		chunk, ok := stream.Next()
		if !ok {
			break
		}
		got += chunk.Content
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if got != "ab" {
		t.Fatalf("got %q, want ab", got)
	}
}
