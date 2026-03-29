//go:build ignore

// long-horizon demonstrates the "context reset loop" (or "Ralph Wiggum") pattern.
// It shows how to run an agent repeatedly on a long task across multiple sessions,
// where each cycle gets a fresh context window.
//
// Progress is tracked externally via a file (plan.md), so the agent can resume
// where it left off without exceeding the LLM context limits over time.
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
	"github.com/nijaru/canto/llm/providers"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"
)

const planFile = "./data/long-horizon/plan.md"

// CycleRunner implements a hard context reset loop.
type CycleRunner struct {
	Agent     agent.Agent
	Store     session.Store
	PlanFile  string
	MaxCycles int
	CheckFn   func(planPath string) (bool, error)
	SessionFn func(cycle int) *session.Session
}

func (c *CycleRunner) Run(ctx context.Context) error {
	for cycle := range c.MaxCycles {
		if err := ctx.Err(); err != nil {
			return err
		}

		sess := c.SessionFn(cycle)

		// Run the agent for this cycle.
		if _, err := c.Agent.Turn(ctx, sess); err != nil {
			return fmt.Errorf("cycle %d failed: %w", cycle, err)
		}

		// Persist the new events to the store.
		if c.Store != nil {
			for _, e := range sess.Events() {
				if err := c.Store.Save(ctx, e); err != nil {
					return fmt.Errorf("cycle %d save failed: %w", cycle, err)
				}
			}
		}

		// Check whether the goal has been reached.
		done, err := c.CheckFn(c.PlanFile)
		if err != nil {
			return fmt.Errorf("cycle %d check failed: %w", cycle, err)
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("reached MaxCycles (%d) without completing", c.MaxCycles)
}

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

	provider := providers.OpenAI()

	instructions := fmt.Sprintf(`You are working on a long task tracked in %s.
Each session: read the plan, complete the next unchecked task, mark it done, write back.
When all tasks are done, write "## Status: COMPLETE" to the plan file.`, planFile)

	a := agent.New("worker", instructions, "gpt-4o", provider, reg)

	store, err := session.NewJSONLStore("./data/long-horizon")
	if err != nil {
		log.Fatal(err)
	}

	cr := &CycleRunner{
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
				session.MessageAdded,
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
