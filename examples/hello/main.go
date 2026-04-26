package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/nijaru/canto"
	"github.com/nijaru/canto/llm"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, w io.Writer) error {
	app, err := canto.NewAgent("hello").
		Instructions("You are a concise assistant.").
		Model("faux").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "Hello from Canto."})).
		Ephemeral().
		Build()
	if err != nil {
		return err
	}
	defer app.Close()

	res, err := app.Send(ctx, "hello-session", "Say hello.")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, res.Content)
	return err
}
