package session

import (
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestRebuilderRebuildEntriesWithoutCompactionFallsBackToRawHistory(t *testing.T) {
	sess := New("raw")
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "one"},
		{Role: llm.RoleAssistant, Content: "two"},
	} {
		if err := sess.Append(t.Context(), NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	entries, err := NewRebuilder().RebuildEntries(sess)
	if err != nil {
		t.Fatalf("RebuildEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Message.Content != "one" || entries[1].Message.Content != "two" {
		t.Fatalf("unexpected rebuilt entries: %#v", entries)
	}
}

func TestRebuilderRebuildEntriesInjectsWorkingSetAfterSummary(t *testing.T) {
	sess := New("compacted")
	oldUser := llm.Message{Role: llm.RoleUser, Content: "old"}
	cutoff := llm.Message{Role: llm.RoleAssistant, Content: "cutoff"}
	recent := llm.Message{Role: llm.RoleUser, Content: "recent"}

	for _, msg := range []llm.Message{oldUser, cutoff, recent} {
		if err := sess.Append(t.Context(), NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	events := sess.Events()
	snapshot := CompactionSnapshot{
		Strategy:      "summarize",
		CutoffEventID: events[1].ID.String(),
		Entries: []HistoryEntry{
			{
				EventID: "summary-event",
				Message: llm.Message{
					Role:    llm.RoleSystem,
					Content: "<conversation_summary>\nsummary\n</conversation_summary>",
				},
			},
		},
		ReadFiles:     []string{"a.txt", "c.txt", "a.txt"},
		ModifiedFiles: []string{"b.txt", "c.txt"},
	}
	if err := sess.Append(t.Context(), NewCompactionEvent(sess.ID(), snapshot)); err != nil {
		t.Fatalf("append compaction: %v", err)
	}

	entries, err := NewRebuilder().RebuildEntries(sess)
	if err != nil {
		t.Fatalf("RebuildEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Message.Role != llm.RoleUser ||
		!strings.Contains(entries[0].Message.Content, "<conversation_summary>") {
		t.Fatalf("expected summary first, got %#v", entries[0])
	}
	if entries[1].Message.Role != llm.RoleUser ||
		!strings.Contains(entries[1].Message.Content, "<working_set>") {
		t.Fatalf("expected working set second, got %#v", entries[1])
	}
	if !strings.Contains(entries[1].Message.Content, "Modified files:\n- b.txt\n- c.txt\n") {
		t.Fatalf("expected modified files block, got %q", entries[1].Message.Content)
	}
	if !strings.Contains(entries[1].Message.Content, "Read-only files:\n- a.txt\n") {
		t.Fatalf("expected read-only file block, got %q", entries[1].Message.Content)
	}
	if entries[2].Message.Content != "recent" {
		t.Fatalf("expected recent message last, got %q", entries[2].Message.Content)
	}
}

func TestEffectiveMessagesUsesRebuilderWorkingSetInjection(t *testing.T) {
	sess := New("effective")
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "old"},
		{Role: llm.RoleAssistant, Content: "cutoff"},
		{Role: llm.RoleUser, Content: "recent"},
	} {
		if err := sess.Append(t.Context(), NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	events := sess.Events()
	if err := sess.Append(t.Context(), NewCompactionEvent(sess.ID(), CompactionSnapshot{
		Strategy:      "summarize",
		CutoffEventID: events[1].ID.String(),
		Entries: []HistoryEntry{
			{
				Message: llm.Message{
					Role:    llm.RoleSystem,
					Content: "<conversation_summary>\nsummary\n</conversation_summary>",
				},
			},
		},
		ModifiedFiles: []string{"main.go"},
	})); err != nil {
		t.Fatalf("append compaction: %v", err)
	}

	messages, err := sess.EffectiveMessages()
	if err != nil {
		t.Fatalf("EffectiveMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if !strings.Contains(messages[1].Content, "<working_set>") {
		t.Fatalf("expected working set block in effective messages, got %q", messages[1].Content)
	}
}

func TestRebuilderRebuildEntriesUsesLatestProjectionSnapshot(t *testing.T) {
	sess := New("projected")
	oldUser := llm.Message{Role: llm.RoleUser, Content: "old"}
	cutoff := llm.Message{Role: llm.RoleAssistant, Content: "cutoff"}
	recent := llm.Message{Role: llm.RoleUser, Content: "recent"}

	for _, msg := range []llm.Message{oldUser, cutoff, recent} {
		if err := sess.Append(t.Context(), NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append message: %v", err)
		}
	}

	events := sess.Events()
	snapshot := ProjectionSnapshot{
		Strategy:      string(ProjectionTriggerTime),
		CutoffEventID: events[2].ID.String(),
		Entries: []HistoryEntry{
			{
				Message: llm.Message{
					Role:    llm.RoleSystem,
					Content: "<conversation_summary>\nsummary\n</conversation_summary>",
				},
			},
			{
				EventID: events[2].ID.String(),
				Message: llm.Message{Role: llm.RoleUser, Content: "recent"},
			},
		},
		ReadFiles:     []string{"a.txt"},
		ModifiedFiles: []string{"b.txt"},
	}
	if err := sess.Append(t.Context(), NewProjectionSnapshot(sess.ID(), snapshot)); err != nil {
		t.Fatalf("append projection snapshot: %v", err)
	}

	after := llm.Message{Role: llm.RoleAssistant, Content: "after"}
	if err := sess.Append(t.Context(), NewMessage(sess.ID(), after)); err != nil {
		t.Fatalf("append after: %v", err)
	}

	entries, err := NewRebuilder().RebuildEntries(sess)
	if err != nil {
		t.Fatalf("RebuildEntries: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
	if entries[0].Message.Role != llm.RoleUser ||
		!strings.Contains(entries[0].Message.Content, "<conversation_summary>") {
		t.Fatalf("expected summary first, got %#v", entries[0])
	}
	if entries[1].Message.Role != llm.RoleUser ||
		!strings.Contains(entries[1].Message.Content, "<working_set>") {
		t.Fatalf("expected working set second, got %#v", entries[1])
	}
	if !strings.Contains(entries[1].Message.Content, "Modified files:\n- b.txt\n") {
		t.Fatalf("expected modified file block, got %q", entries[1].Message.Content)
	}
	if entries[2].Message.Content != "recent" {
		t.Fatalf("expected recent entry third, got %q", entries[2].Message.Content)
	}
	if entries[3].Message.Content != "after" {
		t.Fatalf("expected post-snapshot entry last, got %q", entries[3].Message.Content)
	}
}

func TestRebuilderAcceptsSnapshotCutoffOnHiddenEvent(t *testing.T) {
	sess := New("hidden-cutoff")
	if err := sess.Append(t.Context(), NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "before",
	})); err != nil {
		t.Fatalf("append before: %v", err)
	}
	hidden := NewEvent(sess.ID(), TurnStarted, map[string]string{"turn": "1"})
	if err := sess.Append(t.Context(), hidden); err != nil {
		t.Fatalf("append hidden event: %v", err)
	}
	snapshot := ProjectionSnapshot{
		Strategy:      string(ProjectionTriggerManual),
		CutoffEventID: hidden.ID.String(),
		Entries: []HistoryEntry{{
			EventID: sess.Events()[0].ID.String(),
			Message: llm.Message{Role: llm.RoleUser, Content: "before"},
		}},
	}
	if err := sess.Append(t.Context(), NewProjectionSnapshot(sess.ID(), snapshot)); err != nil {
		t.Fatalf("append projection snapshot: %v", err)
	}
	if err := sess.Append(t.Context(), NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleAssistant,
		Content: "after",
	})); err != nil {
		t.Fatalf("append after: %v", err)
	}

	entries, err := NewRebuilder().RebuildEntries(sess)
	if err != nil {
		t.Fatalf("RebuildEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected snapshot entry plus post-cutoff message, got %#v", entries)
	}
	if entries[0].Message.Content != "before" || entries[1].Message.Content != "after" {
		t.Fatalf("unexpected rebuilt entries: %#v", entries)
	}
}
