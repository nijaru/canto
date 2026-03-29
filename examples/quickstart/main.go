//go:build ignore

// quickstart demonstrates the absolute minimum code required to
// initialize an agent and get a single response.
//
// Run: OPENAI_API_KEY=... go run examples/quickstart/main.go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers"
	"github.com/nijaru/canto/session"
)

func main() {
	ctx := context.Background()

	// 1. Initialize the LLM provider
	provider, err := providers.New("openai")
	if err != nil {
		log.Fatalf("failed to create provider: %v", err)
	}

	// 2. Create the agent with an empty tool registry (nil)
	a := agent.New("assistant", "You are a concise and helpful assistant.", "gpt-4o", provider, nil)

	// 3. Create a session to hold the context
	sess := session.New("quickstart-session")

	// 4. Append a message from the user
	msg := llm.Message{
		Role:    llm.RoleUser,
		Content: "Explain what an agent framework is in two sentences.",
	}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), msg)); err != nil {
		log.Fatalf("failed to append message: %v", err)
	}

	// 5. Execute a single turn
	fmt.Println("Thinking...")
	result, err := a.Turn(ctx, sess)
	if err != nil {
		log.Fatalf("agent turn failed: %v", err)
	}

	fmt.Println("\nResponse:")
	fmt.Println(result.Content)
}
