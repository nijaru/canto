package agent

import (
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// TerminalReason explains why a turn stopped.
type TerminalReason string

const (
	TerminalCompleted       TerminalReason = "completed"
	TerminalHandoff         TerminalReason = "handoff"
	TerminalWaiting         TerminalReason = "waiting"
	TerminalMaxTurnsHit     TerminalReason = "max_turns_hit"
	TerminalBudgetExhausted TerminalReason = "budget_exhausted"
)

// StopsProgress reports whether this terminal reason should stop orchestration
// instead of allowing another agent turn or graph edge to continue.
func (r TerminalReason) StopsProgress() bool {
	switch r {
	case TerminalWaiting, TerminalMaxTurnsHit, TerminalBudgetExhausted:
		return true
	default:
		return false
	}
}

func terminalReasonForTurn(res StepResult, s *session.Session, steps, maxSteps int) TerminalReason {
	switch {
	case res.Handoff != nil:
		return TerminalHandoff
	case s != nil && s.IsWaiting():
		return TerminalWaiting
	case maxSteps > 0 && steps >= maxSteps:
		return TerminalMaxTurnsHit
	}

	if s == nil {
		return TerminalCompleted
	}

	last, ok := s.LastMessage()
	if !ok || last.Role != llm.RoleTool {
		return TerminalCompleted
	}
	return ""
}
