package session

import (
	"io"
	"strings"
	"testing"

	"github.com/nijaru/canto/artifact"
	"github.com/nijaru/canto/llm"
)

func TestChildRequestedEventRoundTrip(t *testing.T) {
	event := NewChildRequestedEvent("parent", ChildRequestedData{
		ChildID:        "child-1",
		ChildSessionID: "sess-child-1",
		ParentEventID:  "evt-parent-1",
		AgentID:        "reviewer",
		Mode:           ChildModeHandoff,
		Task:           "Review changed files",
		Context:        "Focus on correctness and regressions",
	})

	data, ok, err := event.ChildRequestedData()
	if err != nil {
		t.Fatalf("decode child requested: %v", err)
	}
	if !ok {
		t.Fatal("expected child requested payload")
	}
	if data.Mode != ChildModeHandoff || data.AgentID != "reviewer" {
		t.Fatalf("unexpected payload: %#v", data)
	}
}

func TestChildCompletedEventRoundTrip(t *testing.T) {
	event := NewChildCompletedEvent("parent", ChildCompletedData{
		ChildID:        "child-1",
		ChildSessionID: "sess-child-1",
		Summary:        "Reviewed 3 files",
		ArtifactIDs:    []string{"artifact-1"},
		EpisodeID:      "episode-1",
		Usage: llm.Usage{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
			Cost:         0.12,
		},
	})

	data, ok, err := event.ChildCompletedData()
	if err != nil {
		t.Fatalf("decode child completed: %v", err)
	}
	if !ok {
		t.Fatal("expected child completed payload")
	}
	if data.EpisodeID != "episode-1" || len(data.ArtifactIDs) != 1 {
		t.Fatalf("unexpected payload: %#v", data)
	}
	if data.Usage.TotalTokens != 30 {
		t.Fatalf("unexpected usage: %#v", data.Usage)
	}
}

func TestArtifactRecordedEventDefaultsSessionID(t *testing.T) {
	event := NewArtifactRecordedEvent("sess-parent", ArtifactRecordedData{
		ChildID: "child-1",
		Artifact: ArtifactRef{
			ID:    "artifact-1",
			Kind:  "patch",
			URI:   "/tmp/patch.diff",
			Label: "Worker patch",
		},
	})

	data, ok, err := event.ArtifactRecordedData()
	if err != nil {
		t.Fatalf("decode artifact recorded: %v", err)
	}
	if !ok {
		t.Fatal("expected artifact recorded payload")
	}
	if data.SessionID != "sess-parent" {
		t.Fatalf("artifact session_id = %q, want sess-parent", data.SessionID)
	}
	if data.Artifact.Kind != "patch" {
		t.Fatalf("unexpected artifact payload: %#v", data.Artifact)
	}
}

func TestRecordArtifactDefaultsProducerSessionID(t *testing.T) {
	sess := New("sess-parent")

	if err := RecordArtifact(t.Context(), sess, ArtifactRecordedData{
		ChildID: "child-1",
		Artifact: ArtifactRef{
			ID:   "artifact-1",
			Kind: "patch",
			URI:  "memory://patch.diff",
		},
	}); err != nil {
		t.Fatalf("record artifact: %v", err)
	}

	last := sess.Events()[len(sess.Events())-1]
	data, ok, err := last.ArtifactRecordedData()
	if err != nil {
		t.Fatalf("decode artifact recorded: %v", err)
	}
	if !ok {
		t.Fatal("expected artifact recorded payload")
	}
	if data.SessionID != "sess-parent" {
		t.Fatalf("session id = %q, want sess-parent", data.SessionID)
	}
	if data.Artifact.ProducerSessionID != "sess-parent" {
		t.Fatalf("producer session id = %q, want sess-parent", data.Artifact.ProducerSessionID)
	}
}

func TestStoreArtifactPersistsAndRecords(t *testing.T) {
	store, err := artifact.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	sess := New("sess-parent")
	desc, err := StoreArtifact(t.Context(), sess, store, ArtifactRecordedData{
		ChildID: "child-1",
		Artifact: ArtifactRef{
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

func TestChildDataDecodeReturnsFalseForOtherEventTypes(t *testing.T) {
	event := NewMessage("sess", llm.Message{Role: llm.RoleUser, Content: "hi"})

	_, ok, err := event.ChildStartedData()
	if err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if ok {
		t.Fatal("expected non-child event to return ok=false")
	}
}
