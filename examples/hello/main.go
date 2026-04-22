package main

import (
	"context"
	"fmt"
	"log"

	"github.com/nijaru/canto"
	"github.com/nijaru/canto/llm"
)

func main() {
	app, err := canto.NewAgent("hello").
		Instructions("You are a concise assistant.").
		Provider(llm.NewFauxProvider("faux", llm.FauxStep{Content: "Hello from Canto."})).
		Build()
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	res, err := app.Send(context.Background(), "hello-session", "Say hello.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content)
}
