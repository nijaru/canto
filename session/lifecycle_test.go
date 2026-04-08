package session

import (
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestLifecycleEventsRoundTrip(t *testing.T) {
	step := NewStepStartedEvent("sess", StepStartedData{
		AgentID: "agent",
		Model:   "model",
		PromptCache: PromptCacheData{
			PrefixHash:     "prefix",
			ToolSchemaHash: "tools",
		},
	})
	data, ok, err := step.StepStartedData()
	if err != nil {
		t.Fatalf("decode step started: %v", err)
	}
	if !ok {
		t.Fatal("expected step started payload")
	}
	if data.AgentID != "agent" || data.PromptCache.PrefixHash != "prefix" {
		t.Fatalf("unexpected step started payload: %+v", data)
	}

	turn := NewTurnCompletedEvent("sess", TurnCompletedData{
		AgentID:        "agent",
		Steps:          3,
		Usage:          llm.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		TurnStopReason: "completed",
		Error:          "boom",
	})
	turnData, ok, err := turn.TurnCompletedData()
	if err != nil {
		t.Fatalf("decode turn completed: %v", err)
	}
	if !ok {
		t.Fatal("expected turn completed payload")
	}
	if turnData.Steps != 3 || turnData.TurnStopReason != "completed" || turnData.Error != "boom" {
		t.Fatalf("unexpected turn completed payload: %+v", turnData)
	}

	tool := NewToolStartedEvent("sess", ToolStartedData{
		Tool:      "read",
		Arguments: "{}",
		ID:        "call-1",
	})
	toolData, ok, err := tool.ToolStartedData()
	if err != nil {
		t.Fatalf("decode tool started: %v", err)
	}
	if !ok {
		t.Fatal("expected tool started payload")
	}
	if toolData.Tool != "read" || toolData.ID != "call-1" {
		t.Fatalf("unexpected tool started payload: %+v", toolData)
	}
}
