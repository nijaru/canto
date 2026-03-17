//go:build ignore

// autoresearch demonstrates running an agent in a loop to produce
// a research report. Each iteration reads the current report draft
// from disk and continues writing until the agent signals completion.
//
// Run: OPENAI_API_KEY=... go run examples/autoresearch/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"
)

func main() {
	ctx := context.Background()

	// Tools: bash gives the agent file read/write and search.
	reg := tool.NewRegistry()
	reg.Register(&tools.BashTool{})

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	instructions := `You are a research assistant. Your task is to produce a thorough
research report on the given topic. On each run:
1. Read the current draft from report.md (if it exists).
2. Add new sections, fix gaps, and improve quality.
3. Write the updated report back to report.md.
4. When satisfied, output: RESEARCH_COMPLETE`

	a := agent.New("researcher", instructions, "gpt-4o", provider, reg)

	store, err := session.NewJSONLStore("./data/autoresearch")
	if err != nil {
		log.Fatal(err)
	}

	runner := runtime.NewRunner(store, a)

	topic := "the history and architecture of transformer models"
	sessionID := "autoresearch-transformers"

	// Seed the first message.
	sess, _ := store.Load(ctx, sessionID)
	if len(sess.Messages()) == 0 {
		store.Save(ctx, session.NewEvent(sessionID, session.MessageAdded,
			map[string]string{"role": "user", "content": fmt.Sprintf("Research topic: %s", topic)},
		))
	}

	// Run up to 5 iterations; each builds on the previous session.
	for i := range 5 {
		fmt.Printf("--- iteration %d ---\n", i+1)
		if _, err := runner.Run(ctx, sessionID); err != nil {
			log.Fatalf("run failed: %v", err)
		}

		// Check for completion signal in the session messages.
		sess, _ := store.Load(ctx, sessionID)
		msgs := sess.Messages()
		if len(msgs) > 0 {
			last := msgs[len(msgs)-1]
			if last.Role == "assistant" && containsAny(last.Content, "RESEARCH_COMPLETE") {
				fmt.Println("Research complete.")
				break
			}
		}
	}

	fmt.Println("Report written to report.md")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
