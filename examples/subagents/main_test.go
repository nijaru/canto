package main

import (
	"strings"
	"testing"

	"github.com/nijaru/canto/session"
)

func TestRunExample(t *testing.T) {
	result, err := runExample(t.Context())
	if err != nil {
		t.Fatalf("runExample: %v", err)
	}
	if !strings.Contains(result.Summary, "Merged release review:") {
		t.Fatalf("summary = %q, want merged release report", result.Summary)
	}
	if len(result.Run.ChildRuns) != 3 {
		t.Fatalf("child runs = %d, want 3", len(result.Run.ChildRuns))
	}

	for _, child := range result.Run.ChildRuns {
		if child.Mode != session.ChildModeHandoff {
			t.Fatalf("child %s mode = %s, want handoff", child.ChildID, child.Mode)
		}
		if child.Status != session.ChildStatusMerged {
			t.Fatalf("child %s status = %s, want merged", child.ChildID, child.Status)
		}
		if len(child.Artifacts) != 1 {
			t.Fatalf("child %s artifacts = %d, want 1", child.ChildID, len(child.Artifacts))
		}
		if child.Run == nil {
			t.Fatalf("child %s missing nested run", child.ChildID)
		}
		if len(child.Run.Turns) != 1 {
			t.Fatalf("child %s turns = %d, want 1", child.ChildID, len(child.Run.Turns))
		}
		if child.Run.Turns[0].Output.Content == "" {
			t.Fatalf("child %s missing assistant output", child.ChildID)
		}
	}
}
