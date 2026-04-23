//go:build ignore

// autoresearch demonstrates an autonomous experiment loop.
// It uses an agent to repeatedly optimize a target function, keeping
// changes that improve a performance metric and reverting changes that don't.
//
// Run: OPENAI_API_KEY=... go run main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/coding"
	"github.com/nijaru/canto/llm/providers"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

const (
	targetFile = "target.go"
	evalScript = "./evaluate.sh"
	logFile    = "experiments.jsonl"
)

var scoreRegex = regexp.MustCompile(`SCORE:\s*([0-9.]+)`)

type ExperimentRecord struct {
	Iteration  int     `json:"iteration"`
	Timestamp  string  `json:"timestamp"`
	Score      float64 `json:"score"`
	BestScore  float64 `json:"best_score"`
	Kept       bool    `json:"kept"`
	Confidence float64 `json:"confidence,omitempty"`
	Error      string  `json:"error,omitempty"`
}

func main() {
	ctx := context.Background()

	// 1. Setup the Canto agent
	reg := tool.NewRegistry()
	reg.Register(&coding.BashTool{})

	provider := providers.OpenAI()

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
		log.Fatalf(
			"Baseline evaluation failed. Ensure target.go compiles and evaluate.sh works. Error: %v",
			err,
		)
	}
	fmt.Printf("Baseline Score: %.2f\n\n", bestScore)

	var history []float64
	history = append(history, bestScore)

	// Open or create the JSONL experiment log
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}
	defer f.Close()
	jsonEncoder := json.NewEncoder(f)

	// Log baseline
	jsonEncoder.Encode(ExperimentRecord{
		Iteration: 0,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Score:     bestScore,
		BestScore: bestScore,
		Kept:      true,
	})

	feedback := fmt.Sprintf(
		"The current baseline score is %.2f ns/op. Please modify the target.go file to improve performance.",
		bestScore,
	)

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
		if _, err := runner.Send(ctx, sessionID, feedback); err != nil {
			log.Printf("Agent run failed: %v", err)
		}

		// Evaluate the new code
		fmt.Println("Evaluating changes...")
		newScore, evalErr := evaluate(ctx)

		var outcomeMessage string
		var kept bool
		var confidence float64

		if evalErr != nil {
			// Failed to compile or test failed
			fmt.Printf("Evaluation failed: %v. Reverting to backup.\n", evalErr)
			os.WriteFile(targetFile, backup, 0o644)
			outcomeMessage = fmt.Sprintf(
				"Your last change caused an error: %v. I have reverted target.go back to the previous state. Please try a different approach.",
				evalErr,
			)
			kept = false
		} else {
			history = append(history, newScore)

			// Calculate statistical confidence
			improvement := bestScore - newScore
			mad := calculateMAD(history)

			if mad == 0 {
				mad = 1.0 // Prevent division by zero if all runs are exactly identical
			}
			confidence = improvement / mad

			// Rule: It must be an improvement AND statistically significant (e.g. > 1 MAD)
			if improvement > 0 && confidence >= 1.0 {
				fmt.Printf("SUCCESS! Score improved to %.2f (Confidence: %.2fx MAD). Keeping changes.\n", newScore, confidence)
				bestScore = newScore
				outcomeMessage = fmt.Sprintf("Success! The benchmark score improved to %.2f ns/op. The changes have been kept. Please make another optimization.", bestScore)
				kept = true
			} else if improvement > 0 {
				fmt.Printf("Score slightly improved to %.2f, but within noise margin (Confidence: %.2fx MAD < 1.0). Reverting to avoid complexity bloat.\n", newScore, confidence)
				os.WriteFile(targetFile, backup, 0o644)
				outcomeMessage = fmt.Sprintf("The score slightly improved to %.2f, but it was within the margin of error (noise). I have reverted the file to avoid adding unnecessary code complexity. Try a more significant optimization.", newScore)
				kept = false
			} else {
				fmt.Printf("Score worsened or did not improve (%.2f vs best %.2f). Reverting.\n", newScore, bestScore)
				os.WriteFile(targetFile, backup, 0o644)
				outcomeMessage = fmt.Sprintf("The optimization did not improve the score (got %.2f, best is %.2f). I have reverted the file. Please try a different strategy.", newScore, bestScore)
				kept = false
			}
		}

		fmt.Println()

		// Record the experiment
		record := ExperimentRecord{
			Iteration:  i,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			Score:      newScore,
			BestScore:  bestScore,
			Kept:       kept,
			Confidence: confidence,
		}
		if evalErr != nil {
			record.Error = evalErr.Error()
		}
		jsonEncoder.Encode(record)

		// Feed the outcome into the next iteration through the canonical host path.
		feedback = outcomeMessage
	}

	fmt.Println(
		"Autoresearch complete. Check target.go for the final optimized code and experiments.jsonl for the log.",
	)
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

// calculateMAD calculates the Median Absolute Deviation of a slice of floats.
// MAD is a robust measure of the variability of a univariate sample.
func calculateMAD(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}

	c := make([]float64, len(data))
	copy(c, data)
	median := calculateMedian(c)

	deviations := make([]float64, len(data))
	for i, val := range c {
		deviations[i] = math.Abs(val - median)
	}

	return calculateMedian(deviations)
}

func calculateMedian(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sort.Float64s(data)
	mid := len(data) / 2
	if len(data)%2 == 0 {
		return (data[mid-1] + data[mid]) / 2.0
	}
	return data[mid]
}
