package memory
import (
	"context"
	"testing"
)

func TestFTS5EdgeCases(t *testing.T) {
	store, err := NewCoreStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.SearchEpisodes(context.Background(), "invalid\"query", 10)
	if err != nil {
		t.Fatalf("SearchEpisodes failed with FTS error: %v", err)
	}
}
