package graph

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/oklog/ulid/v2"
)

// Branch describes one branch in a fan-out node.
type Branch struct {
	Name  string
	Agent agent.Agent
}

// BranchResult is the gathered outcome for one fan-out branch.
type BranchResult struct {
	Name    string
	Session *session.Session
	Result  agent.StepResult
}

// JoinFunc reduces all branch results into the single StepResult returned from
// the macro-graph node.
type JoinFunc func([]BranchResult) agent.StepResult

// ParallelNode runs multiple branch agents concurrently against forked child
// sessions, then joins their results back in branch declaration order.
type ParallelNode struct {
	id       string
	branches []Branch
	join     JoinFunc
}

// NewParallelNode creates a graph node that scatters work across concurrent
// branches and joins the results into one StepResult.
func NewParallelNode(id string, branches []Branch, join JoinFunc) *ParallelNode {
	return &ParallelNode{
		id:       id,
		branches: append([]Branch(nil), branches...),
		join:     join,
	}
}

func (n *ParallelNode) ID() string { return n.id }

func (n *ParallelNode) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return n.Turn(ctx, sess)
}

func (n *ParallelNode) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	if len(n.branches) == 0 {
		return agent.StepResult{}, nil
	}

	branchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]BranchResult, len(n.branches))
	errs := make([]error, len(n.branches))
	var wg sync.WaitGroup

	for i, branch := range n.branches {
		i := i
		branch := branch
		child, err := sess.Branch(
			branchCtx,
			branchSessionID(sess.ID(), n.id, branch.Name, i),
			session.ForkOptions{
				BranchLabel: branchLabel(branch, i),
				ForkReason:  "graph fanout",
			},
		)
		if err != nil {
			return agent.StepResult{}, fmt.Errorf(
				"fanout: fork branch %q: %w",
				branchLabel(branch, i),
				err,
			)
		}
		results[i] = BranchResult{
			Name:    branch.Name,
			Session: child,
		}
		wg.Go(func() {
			res, err := branch.Agent.Turn(branchCtx, child)
			if err != nil {
				errs[i] = fmt.Errorf("branch %q: %w", branchLabel(branch, i), err)
				cancel()
				return
			}
			results[i].Result = res
		})
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return agent.StepResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return agent.StepResult{}, err
		}
	}

	if n.join != nil {
		result := n.join(results)
		result.Usage = aggregateBranchUsage(results, llm.Usage{})
		return result, nil
	}

	return defaultJoin(results), nil
}

func aggregateBranchUsage(results []BranchResult, base llm.Usage) llm.Usage {
	total := base
	for _, result := range results {
		total = aggregateUsage(total, result.Result.Usage)
	}
	return total
}

func defaultJoin(results []BranchResult) agent.StepResult {
	out := agent.StepResult{}
	parts := make([]string, 0, len(results))
	for _, branch := range results {
		if branch.Result.Content != "" {
			parts = append(parts, branch.Result.Content)
		}
		out.ToolResults = append(out.ToolResults, branch.Result.ToolResults...)
		if out.Handoff == nil && branch.Result.Handoff != nil {
			handoff := *branch.Result.Handoff
			out.Handoff = &handoff
		}
		if out.TurnStopReason == "" && branch.Result.TurnStopReason != "" {
			out.TurnStopReason = branch.Result.TurnStopReason
		}
	}
	out.Content = strings.Join(parts, "\n")
	out.Usage = aggregateBranchUsage(results, llm.Usage{})
	return out
}

func branchLabel(branch Branch, index int) string {
	if branch.Name != "" {
		return branch.Name
	}
	return fmt.Sprintf("branch-%d", index)
}

func branchSessionID(parentSessionID, nodeID, branchName string, index int) string {
	entropy := ulid.Monotonic(rand.Reader, 0)
	id := ulid.MustNew(ulid.Timestamp(time.Now().UTC()), entropy)
	name := branchName
	if name == "" {
		name = fmt.Sprintf("%d", index)
	}
	return fmt.Sprintf("%s:%s:%s:%s", parentSessionID, nodeID, name, id.String())
}
