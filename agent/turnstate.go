package agent

import (
	"context"
	"errors"

	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type turnState struct {
	steps       int
	escalations int
	totalUsage  llm.Usage
	stopReason  TurnStopReason
}

type stepOutcome struct {
	result      StepResult
	err         error
	yieldResult StepResult
	yieldErr    error
	stop        bool
	retry       bool
}

func (ts *turnState) appendUsage(usage llm.Usage) {
	ts.totalUsage.InputTokens += usage.InputTokens
	ts.totalUsage.OutputTokens += usage.OutputTokens
	ts.totalUsage.TotalTokens += usage.TotalTokens
	ts.totalUsage.CacheReadTokens += usage.CacheReadTokens
	ts.totalUsage.CacheCreationTokens += usage.CacheCreationTokens
	ts.totalUsage.Cost += usage.Cost
}

func (ts *turnState) handleStepResult(
	s *session.Session,
	res StepResult,
	maxSteps int,
) stepOutcome {
	ts.escalations = 0
	ts.steps++
	ts.appendUsage(res.Usage)
	ts.stopReason = turnStopReasonForTurn(res, s, ts.steps, maxSteps)
	res.TurnStopReason = ts.stopReason

	return stepOutcome{
		result:      res,
		yieldResult: res,
		stop:        ts.stopReason != "",
	}
}

func (ts *turnState) handleStepError(
	ctx context.Context,
	s *session.Session,
	agentID string,
	provider llm.Provider,
	maxEscalations int,
	err error,
) stepOutcome {
	if err == nil {
		return stepOutcome{}
	}

	var budgetErr *governor.BudgetExceededError
	if errors.As(err, &budgetErr) {
		ts.stopReason = TurnStopBudgetExhausted
		return stepOutcome{
			result:      StepResult{TurnStopReason: ts.stopReason, Usage: ts.totalUsage},
			yieldResult: StepResult{TurnStopReason: ts.stopReason, Usage: ts.totalUsage},
			stop:        true,
		}
	}

	escalation := classifyStepError(err, provider)
	if escalation != nil && escalation.recoverable && ts.escalations < maxEscalations {
		ts.escalations++
		if appendErr := appendWithheldToolMessage(ctx, s, escalation); appendErr != nil {
			return stepOutcome{err: appendErr, yieldErr: appendErr, stop: true}
		}
		if recordErr := recordEscalationRetry(ctx, s, agentID, ts.escalations, escalation); recordErr != nil {
			return stepOutcome{err: recordErr, yieldErr: recordErr, stop: true}
		}
		return stepOutcome{retry: true}
	}
	if escalation != nil && escalation.recoverable {
		hardErr := hardEscalationError(escalation, ts.escalations)
		return stepOutcome{err: hardErr, yieldErr: hardErr, stop: true}
	}

	return stepOutcome{err: err, yieldErr: err, stop: true}
}

func finalizeTurnResult(s *session.Session, ts turnState, res StepResult) StepResult {
	res.Usage = ts.totalUsage
	res.TurnStopReason = ts.stopReason
	if ts.steps > 0 {
		if msg, ok := s.LastAssistantMessage(); ok {
			res.Content = msg.Content
		}
	}
	return res
}
