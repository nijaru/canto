package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	output := out.String()
	for _, want := range []string{
		"Updated README.md, checked service context, and verified the workspace smoke test.",
		"Session resumed with durable history; README.md remains verified.",
		"README.md: project: reference\nstatus: verified",
		"Events: message_added=13, tool_started=7, tool_completed=7, approval_requested=4, approval_resolved=4, turn_completed=2",
		"PreToolUse web_search",
		"PostToolUse execute_code",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
