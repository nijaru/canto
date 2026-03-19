package main

import (
	"context"
	"fmt"
	"os"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/session"
)

func main() {
	ctx := context.Background()
	provider := openai.NewProvider(catwalk.Provider{
		ID:     "openai",
		APIKey: os.Getenv("OPENAI_API_KEY"),
	})

	a := agent.New("assistant", "You're a helpful assistant.", "gpt-4o", provider, nil)

	sess := session.New("user-123")
	msg := llm.Message{Role: llm.RoleUser, Content: "How does Canto handle state?"}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), msg)); err != nil {
		panic(err)
	}

	result, err := a.Turn(ctx, sess)
	if err != nil {
		panic(err)
	}

	fmt.Println(result.Content)
}
