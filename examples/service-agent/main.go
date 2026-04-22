package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/nijaru/canto"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/service"
	"github.com/nijaru/canto/session"
	cantotool "github.com/nijaru/canto/tool"
)

type searchArgs struct {
	Query string `json:"query" jsonschema:"search query"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func main() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "canto-service-agent-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := session.NewSQLiteStore(filepath.Join(dir, "sessions.db"))
	if err != nil {
		log.Fatal(err)
	}

	searchTool, err := service.New(service.Config[searchArgs, searchResult]{
		Name:        "web_search",
		Description: "Search the web for a query.",
		Metadata: cantotool.Metadata{
			Category:    "service",
			ReadOnly:    true,
			Concurrency: cantotool.Parallel,
		},
		Execute: func(_ context.Context, args searchArgs) (searchResult, error) {
			return searchResult{
				Title:   "Canto service tools",
				URL:     "https://example.com/canto-service-tools",
				Snippet: "Typed service tools preserve schema, approval, and audit boundaries.",
			}, nil
		},
		Approval: service.ReadOnly("web.search", func(args searchArgs) string {
			return args.Query
		}),
	})
	if err != nil {
		log.Fatal(err)
	}

	provider := llm.NewFauxProvider(
		"faux",
		llm.FauxStep{
			Calls: []llm.Call{
				toolCall("call_1", "web_search", `{"query":"canto service tools"}`),
			},
		},
		llm.FauxStep{
			Content: "Canto can expose typed service/API tools with explicit schema, approval, and metadata boundaries.",
		},
	)

	app, err := canto.NewAgent("service-reference").
		Instructions("Use service tools when useful, then answer concisely.").
		Model("faux").
		Provider(provider).
		SessionStore(store).
		Tools(searchTool).
		Build()
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	res, err := app.Send(ctx, "service-session", "Find how Canto should expose service tools.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content)
}

func toolCall(id, name, args string) llm.Call {
	call := llm.Call{
		ID:   id,
		Type: "function",
	}
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}
