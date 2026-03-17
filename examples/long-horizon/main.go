//go:build ignore

// long-horizon demonstrates the "context reset loop" pattern: an agent that
// works on a long task across multiple sessions. Each cycle gets a fresh
// context window. Progress is tracked in a file so the agent can resume.
//
// CycleRunner handles the loop; the agent uses bash to read/write progress.
//
// Run: OPENAI_API_KEY=... go run examples/long-horizon/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"

	longhorizon "github.com/nijaru/canto/examples/long-horizon"
)

const planFile = "./data/long-horizon/plan.md"

func main() {
	ctx := context.Background()

	os.MkdirAll("./data/long-horizon", 0o755)

	// Seed the plan file if it doesn't exist.
	if _, err := os.Stat(planFile); os.IsNotExist(err) {
		os.WriteFile(
			planFile,
			[]byte(
				"# Plan\n\n## Status: IN_PROGRESS\n\n## Tasks\n- [ ] Step 1\n- [ ] Step 2\n- [ ] Step 3\n",
			),
			0o644,
		)
	}

	reg := tool.NewRegistry()
	reg.Register(&tools.BashTool{})

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	instructions := fmt.Sprintf(`You are working on a long task tracked in %s.
Each session: read the plan, complete the next unchecked task, mark it done, write back.
When all tasks are done, write "## Status: COMPLETE" to the plan file.`, planFile)

	a := agent.New("worker", instructions, "gpt-4o", provider, reg)

	store, err := session.NewJSONLStore("./data/long-horizon")
	if err != nil {
		log.Fatal(err)
	}

	cr := &longhorizon.CycleRunner{
		Agent:     a,
		Store:     store,
		PlanFile:  planFile,
		MaxCycles: 10,
		CheckFn:   isDone,
		SessionFn: func(cycle int) *session.Session {
			id := fmt.Sprintf("lh-cycle-%d", cycle)
			sess := session.New(id)
			sess.Append(context.Background(), session.NewEvent(
				id,
				session.EventTypeMessageAdded,
				llm.Message{
					Role: llm.RoleUser,
					Content: fmt.Sprintf(
						"Continue work. Cycle %d. Read %s and do the next task.",
						cycle+1,
						planFile,
					),
				},
			))
			return sess
		},
	}

	if err := cr.Run(ctx); err != nil {
		log.Fatalf("cycle runner failed: %v", err)
	}

	fmt.Println("Task complete. Final plan:")
	data, _ := os.ReadFile(planFile)
	fmt.Println(string(data))
}

func isDone(planPath string) (bool, error) {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return false, nil // plan not written yet
	}
	return strings.Contains(string(data), "Status: COMPLETE"), nil
}
