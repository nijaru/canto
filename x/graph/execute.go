package graph

import (
	"context"
	"fmt"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func (g *Graph) execute(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if _, ok := g.nodes[g.entry]; !ok {
		return agent.StepResult{}, fmt.Errorf("graph: entry node %q not registered", g.entry)
	}

	current := g.entry
	var lastResult agent.StepResult
	var totalUsage llm.Usage
	var steps int

	if g.checkpoints != nil {
		checkpoint, err := g.checkpoints.Load(ctx, g.id, sess.ID())
		if err != nil {
			return agent.StepResult{}, fmt.Errorf("graph: load checkpoint: %w", err)
		}
		if checkpoint != nil {
			if checkpoint.Result.TurnStopReason == agent.TurnStopWaiting && sess.IsWaiting() {
				lastResult = cloneStepResult(checkpoint.Result)
				lastResult.Usage = checkpoint.Usage
				return lastResult, nil
			}
			if checkpoint.Result.TurnStopReason != agent.TurnStopWaiting &&
				checkpoint.LastEventID != "" {
				if lastEvent, ok := sess.LastEvent(); ok &&
					lastEvent.ID.String() != checkpoint.LastEventID {
					checkpoint = nil
				}
			}
			if checkpoint != nil {
				current = checkpoint.NextNode
				totalUsage = checkpoint.Usage
				lastResult = cloneStepResult(checkpoint.Result)
				steps = checkpoint.Steps
				if checkpoint.Completed {
					lastResult.Usage = totalUsage
					return lastResult, nil
				}
			}
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			lastResult.Usage = totalUsage
			return lastResult, err
		}

		a, ok := g.nodes[current]
		if !ok {
			lastResult.Usage = totalUsage
			return lastResult, fmt.Errorf("graph: node %q not registered", current)
		}

		result, err := runGraphNode(ctx, a, sess, chunkFn)
		if err != nil {
			lastResult.Usage = totalUsage
			return lastResult, fmt.Errorf("graph: node %q: %w", current, err)
		}

		totalUsage = aggregateUsage(totalUsage, result.Usage)
		lastResult = result
		steps++

		if result.Handoff != nil {
			if err := agent.RecordHandoff(ctx, sess, result.Handoff); err != nil {
				return lastResult, err
			}
		}

		if result.TurnStopReason.StopsProgress() {
			completed := result.TurnStopReason != agent.TurnStopWaiting
			if err := g.saveCheckpoint(ctx, sess, Checkpoint{
				GraphID:     g.id,
				SessionID:   sess.ID(),
				NextNode:    current,
				Steps:       steps,
				LastEventID: lastEventID(sess),
				Usage:       totalUsage,
				Result:      cloneStepResult(lastResult),
				Completed:   completed,
			}); err != nil {
				return lastResult, err
			}
			break
		}

		next := g.nextNode(current, result)
		if next == "" {
			if err := g.saveCheckpoint(ctx, sess, Checkpoint{
				GraphID:     g.id,
				SessionID:   sess.ID(),
				NextNode:    current,
				Steps:       steps,
				LastEventID: lastEventID(sess),
				Usage:       totalUsage,
				Result:      cloneStepResult(lastResult),
				Completed:   true,
			}); err != nil {
				return lastResult, err
			}
			break
		}
		if err := g.saveCheckpoint(ctx, sess, Checkpoint{
			GraphID:     g.id,
			SessionID:   sess.ID(),
			NextNode:    next,
			Steps:       steps,
			LastEventID: lastEventID(sess),
			Usage:       totalUsage,
			Result:      cloneStepResult(lastResult),
		}); err != nil {
			return lastResult, err
		}
		current = next
	}

	lastResult.Usage = totalUsage
	return lastResult, nil
}

func runGraphNode(
	ctx context.Context,
	a agent.Agent,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if chunkFn != nil {
		if streamer, ok := a.(agent.Streamer); ok {
			return streamer.StreamTurn(ctx, sess, chunkFn)
		}
	}
	return a.Turn(ctx, sess)
}

func (g *Graph) saveCheckpoint(
	ctx context.Context,
	sess *session.Session,
	checkpoint Checkpoint,
) error {
	if g.checkpoints == nil {
		return nil
	}
	if err := g.checkpoints.Save(ctx, checkpoint); err != nil {
		return fmt.Errorf("graph: save checkpoint: %w", err)
	}
	return nil
}

func lastEventID(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	if event, ok := sess.LastEvent(); ok {
		return event.ID.String()
	}
	return ""
}

func (g *Graph) nextNode(from string, result agent.StepResult) string {
	for _, edge := range g.edges {
		if edge.From == from && edge.Condition(result) {
			return edge.To
		}
	}
	return ""
}
