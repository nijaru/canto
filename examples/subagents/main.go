// subagents demonstrates Canto's core orchestration primitives for managing
// multi-agent workflows.
//
// This example simulates a "release orchestrator" delegating three parallel
// review tasks to subagents. To allow this example to run instantly without
// burning LLM API credits, it uses a mock `workerAgent` that implements the
// `agent.Agent` interface.
//
// Key concepts demonstrated:
// 1. runtime.ChildRunner (spawning and waiting on parallel agents)
// 2. session.ChildMergedEvent (linking child results back into the parent)
// 3. session.ArtifactRecordedEvent (tracking files created by subagents)
// 4. session.ExportRunTree (exporting the full hierarchy for telemetry/evals)
//
// Run: go run ./examples/subagents
package main

import (
	"context"
	"fmt"
	"log"
)

func main() {
	result, err := runExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Summary)
	fmt.Printf("\nchild runs: %d\n", len(result.Run.ChildRuns))
}
