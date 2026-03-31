// long-horizon demonstrates Canto's automated context governance.
// It shows how to use the governor package to automatically offload and
// summarize context during a long-running session.
//
// Run: OPENAI_API_KEY=... go run examples/long-horizon/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/nijaru/canto/agent"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers"
	"github.com/nijaru/canto/session"
)

func main() {
	ctx := context.Background()
	provider := providers.OpenAI()

	// 1. Setup governance (Offload to disk when > 1000 tokens, then summarize)
	offloadDir := "./data/long-horizon-offload"
	os.MkdirAll(offloadDir, 0o755)

	gov := []ccontext.ContextMutator{
		governor.NewOffloader(1000, offloadDir),
		governor.NewSummarizer(1000, provider, "gpt-4o"),
	}

	// 2. Build a phased context pipeline
	builder := ccontext.NewBuilder(
		ccontext.Instructions("You are a helpful assistant."),
		ccontext.History(),
	)
	builder.AppendMutators(gov...)

	// 3. Initialize agent with the builder
	a := agent.New("assistant", "", "gpt-4o", provider, nil, agent.WithBuilder(builder))

	// 4. Run a long session
	sess := session.New("long-task")
	
	// Simulate many turns...
	for i := 1; i <= 5; i++ {
		msg := llm.Message{
			Role:    llm.RoleUser,
			Content: fmt.Sprintf("This is a very long message for turn %d. " + 
				"We want to trigger the governor's offloading logic.", i),
		}
		sess.Append(ctx, session.NewMessage(sess.ID(), msg))

		res, err := a.Turn(ctx, sess)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Turn %d completed. Content length: %d\n", i, len(res.Content))
	}

	fmt.Println("\nGovernance check:")
	for _, e := range sess.Events() {
		if e.Type == session.CompactionTriggered {
			fmt.Printf("- Compaction triggered at: %v\n", e.Timestamp)
		}
	}
}
