package artifact

import (
	"bytes"
	"io"
	"testing"
)

func BenchmarkFileStorePut(b *testing.B) {
	store, err := NewFileStore(b.TempDir())
	if err != nil {
		b.Fatalf("new file store: %v", err)
	}
	defer store.Close()

	body := bytes.Repeat([]byte("artifact payload\n"), 256)

	for b.Loop() {
		if _, err := store.Put(b.Context(), Descriptor{
			Label:    "bench.txt",
			Kind:     "benchmark",
			MIMEType: "text/plain",
		}, bytes.NewReader(body)); err != nil {
			b.Fatalf("put artifact: %v", err)
		}
	}
}

func BenchmarkFileStoreStat(b *testing.B) {
	store, err := NewFileStore(b.TempDir())
	if err != nil {
		b.Fatalf("new file store: %v", err)
	}
	defer store.Close()

	desc, err := store.Put(b.Context(), Descriptor{
		Label:    "bench.txt",
		Kind:     "benchmark",
		MIMEType: "text/plain",
	}, bytes.NewReader([]byte("artifact payload")))
	if err != nil {
		b.Fatalf("put artifact: %v", err)
	}

	for b.Loop() {
		if _, err := store.Stat(b.Context(), desc.ID); err != nil {
			b.Fatalf("stat artifact: %v", err)
		}
	}
}

func BenchmarkFileStoreOpen(b *testing.B) {
	store, err := NewFileStore(b.TempDir())
	if err != nil {
		b.Fatalf("new file store: %v", err)
	}
	defer store.Close()

	desc, err := store.Put(b.Context(), Descriptor{
		Label:    "bench.txt",
		Kind:     "benchmark",
		MIMEType: "text/plain",
	}, bytes.NewReader(bytes.Repeat([]byte("artifact payload\n"), 64)))
	if err != nil {
		b.Fatalf("put artifact: %v", err)
	}

	for b.Loop() {
		rc, _, err := store.Open(b.Context(), desc.ID)
		if err != nil {
			b.Fatalf("open artifact: %v", err)
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			_ = rc.Close()
			b.Fatalf("read artifact: %v", err)
		}
		if err := rc.Close(); err != nil {
			b.Fatalf("close artifact: %v", err)
		}
	}
}
