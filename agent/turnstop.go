package agent

import "github.com/nijaru/canto/session"

// TurnStopReason explains why a turn stopped.
type TurnStopReason string

const (
	TurnStopCompleted       TurnStopReason = "completed"
	TurnStopHandoff         TurnStopReason = "handoff"
	TurnStopWaiting         TurnStopReason = "waiting"
	TurnStopMaxTurnsHit     TurnStopReason = "max_turns_hit"
	TurnStopBudgetExhausted TurnStopReason = "budget_exhausted"
)

// StopsProgress reports whether this stop reason should stop orchestration
// instead of allowing another agent turn or graph edge to continue.
func (r TurnStopReason) StopsProgress() bool {
	switch r {
	case TurnStopWaiting, TurnStopMaxTurnsHit, TurnStopBudgetExhausted:
		return true
	default:
		return false
	}
}

func turnStopReasonForTurn(res StepResult, s *session.Session, steps, maxSteps int) TurnStopReason {
	switch {
	case res.Handoff != nil:
		return TurnStopHandoff
	case s != nil && s.IsWaiting():
		return TurnStopWaiting
	case maxSteps > 0 && steps >= maxSteps:
		return TurnStopMaxTurnsHit
	case len(res.ToolResults) > 0:
		return ""
	}

	return TurnStopCompleted
}
