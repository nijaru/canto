package graph

import (
	"context"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// LoopNode wraps an agent and allows bounded iteration inside a single graph
// node so the outer graph can remain acyclic.
type LoopNode struct {
	agent         agent.Agent
	maxIterations int
	exitCondition func(agent.StepResult) bool
}

// NewLoopNode creates a node that can run the wrapped agent multiple times
// before yielding one final StepResult back to the macro-graph.
func NewLoopNode(
	a agent.Agent,
	maxIterations int,
	exitCondition func(agent.StepResult) bool,
) *LoopNode {
	if maxIterations <= 0 {
		maxIterations = 1
	}
	return &LoopNode{
		agent:         a,
		maxIterations: maxIterations,
		exitCondition: exitCondition,
	}
}

func (n *LoopNode) ID() string { return n.agent.ID() }

func (n *LoopNode) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return n.Turn(ctx, sess)
}

func (n *LoopNode) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return n.run(ctx, sess, nil)
}

func (n *LoopNode) StreamTurn(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	return n.run(ctx, sess, chunkFn)
}

func (n *LoopNode) run(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	var last agent.StepResult
	for range n.maxIterations {
		if err := ctx.Err(); err != nil {
			last.Usage = aggregateUsage(last.Usage, llm.Usage{})
			return last, err
		}

		var res agent.StepResult
		var err error
		if chunkFn != nil {
			if streamer, ok := n.agent.(agent.Streamer); ok {
				res, err = streamer.StreamTurn(ctx, sess, chunkFn)
			} else {
				res, err = n.agent.Turn(ctx, sess)
			}
		} else {
			res, err = n.agent.Turn(ctx, sess)
		}
		if err != nil {
			res.Usage = aggregateUsage(last.Usage, res.Usage)
			return res, err
		}

		res.Usage = aggregateUsage(last.Usage, res.Usage)
		last = res

		if res.TurnStopReason.StopsProgress() || res.Handoff != nil {
			return last, nil
		}
		if n.exitCondition == nil || n.exitCondition(res) {
			return last, nil
		}
	}
	return last, nil
}

func aggregateUsage(total, next llm.Usage) llm.Usage {
	total.InputTokens += next.InputTokens
	total.OutputTokens += next.OutputTokens
	total.TotalTokens += next.TotalTokens
	total.Cost += next.Cost
	return total
}
