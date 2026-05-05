package artifact

import (
	"io"
	"strings"
	"testing"
)

func TestFileStorePutStatOpen(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	desc, err := store.Put(t.Context(), Descriptor{
		Kind:              "note",
		Label:             "Review note",
		MIMEType:          "text/plain",
		ProducerSessionID: "sess-1",
		ProducerEventID:   "evt-1",
	}, strings.NewReader("hello artifact"))
	if err != nil {
		t.Fatalf("put artifact: %v", err)
	}
	if desc.ID == "" || desc.URI == "" {
		t.Fatalf("descriptor missing identity: %#v", desc)
	}
	if desc.Size == 0 || desc.Digest == "" {
		t.Fatalf("descriptor missing size/digest: %#v", desc)
	}

	stat, err := store.Stat(t.Context(), desc.ID)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if stat.URI != desc.URI || stat.Digest != desc.Digest {
		t.Fatalf("stat mismatch: got %#v want %#v", stat, desc)
	}

	r, opened, err := store.Open(t.Context(), desc.ID)
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read artifact body: %v", err)
	}
	if string(body) != "hello artifact" {
		t.Fatalf("artifact body = %q", body)
	}
	if opened.ID != desc.ID {
		t.Fatalf("opened descriptor = %#v want %#v", opened, desc)
	}
}

func TestFileStoreRejectsPathLikeArtifactIDs(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, id := range []string{"../escape", "nested/id", `nested\id`, ".", ".."} {
		t.Run(id, func(t *testing.T) {
			if _, err := store.Put(t.Context(), Descriptor{ID: id}, strings.NewReader("body")); err == nil {
				t.Fatal("expected invalid artifact id error")
			}
			if _, err := store.Stat(t.Context(), id); err == nil {
				t.Fatal("expected invalid artifact id error from Stat")
			}
			if rc, _, err := store.Open(t.Context(), id); err == nil {
				_ = rc.Close()
				t.Fatal("expected invalid artifact id error from Open")
			}
		})
	}
}
