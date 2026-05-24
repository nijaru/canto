package artifact

import (
	"io"
	"strings"
	"testing"

	"github.com/nijaru/canto/session"
)

func TestStoreSessionArtifactPersistsAndRecords(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	sess := session.New("sess-parent")
	desc, err := StoreSessionArtifact(t.Context(), sess, store, session.ArtifactRecordedData{
		ChildID: "child-1",
		Artifact: session.ArtifactRef{
			Kind:            "review_note",
			Label:           "Worker note",
			MIMEType:        "text/plain",
			ProducerEventID: "evt-1",
		},
	}, strings.NewReader("hello artifact"))
	if err != nil {
		t.Fatalf("store artifact: %v", err)
	}

	if desc.ProducerSessionID != "sess-parent" {
		t.Fatalf("producer session id = %q, want sess-parent", desc.ProducerSessionID)
	}

	opened, stat, err := store.Open(t.Context(), desc.ID)
	if err != nil {
		t.Fatalf("open stored artifact: %v", err)
	}
	defer opened.Close()

	body, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("read stored artifact: %v", err)
	}
	if string(body) != "hello artifact" {
		t.Fatalf("artifact body = %q, want hello artifact", body)
	}
	if stat.ProducerSessionID != "sess-parent" {
		t.Fatalf("stored producer session id = %q, want sess-parent", stat.ProducerSessionID)
	}
	if stat.ProducerEventID != "evt-1" {
		t.Fatalf("stored producer event id = %q, want evt-1", stat.ProducerEventID)
	}

	last := sess.Events()[len(sess.Events())-1]
	data, ok, err := last.ArtifactRecordedData()
	if err != nil {
		t.Fatalf("decode artifact recorded: %v", err)
	}
	if !ok {
		t.Fatal("expected artifact recorded payload")
	}
	if data.Artifact.ID != desc.ID {
		t.Fatalf("recorded artifact id = %q, want %q", data.Artifact.ID, desc.ID)
	}
}
