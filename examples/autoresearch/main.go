//go:build ignore

// autoresearch demonstrates an autonomous experiment loop.
// It uses an agent to repeatedly optimize a target function, keeping
// changes that improve a performance metric and reverting changes that don't.
//
// Run: OPENAI_API_KEY=... cd examples/autoresearch && go run main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm/providers/openai"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/tools"
)

const (
	targetFile = "target.go"
	evalScript = "./evaluate.sh"
)

var scoreRegex = regexp.MustCompile(`SCORE:\s*([0-9.]+)`)

func main() {
	ctx := context.Background()

	// 1. Setup the Canto agent
	reg := tool.NewRegistry()
	reg.Register(&tools.BashTool{})

	provider := openai.New(os.Getenv("OPENAI_API_KEY"))

	instructionsBytes, err := os.ReadFile("program.md")
	if err != nil {
		log.Fatalf("failed to read program.md: %v", err)
	}

	a := agent.New("researcher", string(instructionsBytes), "gpt-4o", provider, reg)

	store, err := session.NewJSONLStore("./data/autoresearch")
	if err != nil {
		log.Fatal(err)
	}

	runner := runtime.NewRunner(store, a)
	sessionID := "autoresearch-loop"

	// 2. Establish the baseline
	fmt.Println("Running baseline evaluation...")
	bestScore, err := evaluate(ctx)
	if err != nil {
		log.Fatalf("Baseline evaluation failed. Ensure target.go compiles and evaluate.sh works. Error: %v", err)
	}
	fmt.Printf("Baseline Score: %.2f\n\n", bestScore)

	// Seed initial message if the session is new
	sess, _ := store.Load(ctx, sessionID)
	if len(sess.Messages()) == 0 {
		store.Save(ctx, session.NewEvent(sessionID, session.MessageAdded,
			map[string]string{"role": "user", "content": fmt.Sprintf("The current baseline score is %.2f ns/op. Please modify the target.go file to improve performance.", bestScore)},
		))
	}

	// 3. The Autonomous Loop
	for i := 1; i <= 10; i++ {
		fmt.Printf("--- Iteration %d ---\n", i)

		// Backup the current best version of target.go
		backup, err := os.ReadFile(targetFile)
		if err != nil {
			log.Fatalf("failed to read target: %v", err)
		}

		// Let the agent act (it will modify target.go)
		fmt.Println("Agent is thinking and modifying code...")
		if _, err := runner.Run(ctx, sessionID); err != nil {
			log.Printf("Agent run failed: %v", err)
		}

		// Evaluate the new code
		fmt.Println("Evaluating changes...")
		newScore, evalErr := evaluate(ctx)

		var outcomeMessage string

		if evalErr != nil {
			// Failed to compile or test failed
			fmt.Printf("Evaluation failed: %v. Reverting to backup.\n", evalErr)
			os.WriteFile(targetFile, backup, 0644)
			outcomeMessage = fmt.Sprintf("Your last change caused an error: %v. I have reverted target.go back to the previous state. Please try a different approach.", evalErr)
		} else if newScore < bestScore {
			// Improvement! (Lower ns/op is better)
			fmt.Printf("SUCCESS! Score improved from %.2f to %.2f. Keeping changes.\n", bestScore, newScore)
			bestScore = newScore
			outcomeMessage = fmt.Sprintf("Success! The benchmark score improved to %.2f ns/op. The changes have been kept. Please make another optimization to improve it further.", bestScore)
		} else {
			// Worse or equal performance
			fmt.Printf("Score worsened or did not improve (%.2f vs best %.2f). Reverting.\n", newScore, bestScore)
			os.WriteFile(targetFile, backup, 0644)
			outcomeMessage = fmt.Sprintf("The optimization did not improve the score (got %.2f, best is %.2f). I have reverted the file. Please try a different strategy.", newScore, bestScore)
		}

		fmt.Println()

		// Feed the outcome back to the agent so it learns
		store.Save(ctx, session.NewEvent(sessionID, session.MessageAdded,
			map[string]string{"role": "user", "content": outcomeMessage},
		))
	}

	fmt.Println("Autoresearch complete. Check target.go for the final optimized code.")
}

// evaluate runs the evaluation script and returns the parsed score.
func evaluate(ctx context.Context) (float64, error) {
	// Give the script a strict timeout so the loop doesn't hang on infinite loops.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, evalScript)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return 0, fmt.Errorf("evaluation timed out after 30 seconds (possible infinite loop)")
		}
		return 0, fmt.Errorf("script failed: %s\nOutput: %s", err, string(output))
	}

	// Extract the metric
	matches := scoreRegex.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return 0, fmt.Errorf("could not find 'SCORE: <value>' in output: %s", string(output))
	}

	score, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse score '%s': %v", matches[1], err)
	}

	return score, nil
}
