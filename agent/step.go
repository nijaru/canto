package agent

import (
	"context"

	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tracing"
)

type preparedStep struct {
	Context  context.Context
	Request  *llm.Request
	Provider llm.Provider
}

func (a *BaseAgent) stepConfig() stepConfig {
	return stepConfig{
		ID:               a.agentID,
		Model:            a.model,
		Provider:         a.provider,
		Builder:          a.builder,
		Tools:            a.tools,
		Hooks:            a.hooks,
		Approvals:        a.approvals,
		MaxParallelTools: a.maxParallelTools,
	}
}

func prepareStep(ctx context.Context, s *session.Session, cfg stepConfig) (preparedStep, error) {
	req := &llm.Request{Model: cfg.Model}
	provider := tracing.WrapProvider(cfg.Provider)

	buildCtx, buildSpan := tracing.StartContext(ctx, cfg.ID, s.ID(), cfg.Model)
	if err := cfg.Builder.Build(buildCtx, provider, cfg.Model, s, req); err != nil {
		tracing.EndContext(buildSpan, err)
		return preparedStep{}, err
	}
	tracing.EndContext(buildSpan, nil)

	cacheFingerprint, err := prompt.FingerprintPromptCache(s, req)
	if err != nil {
		return preparedStep{}, err
	}
	stepStarted := session.NewStepStartedEvent(s.ID(), session.StepStartedData{
		AgentID: cfg.ID,
		Model:   cfg.Model,
		PromptCache: session.PromptCacheData{
			PrefixHash:     cacheFingerprint.PrefixHash,
			ToolSchemaHash: cacheFingerprint.ToolSchemaHash,
		},
	})
	if err := s.Append(buildCtx, stepStarted); err != nil {
		return preparedStep{}, err
	}

	return preparedStep{
		Context:  buildCtx,
		Request:  req,
		Provider: provider,
	}, nil
}

func appendStepCompleted(
	ctx context.Context,
	s *session.Session,
	agentID string,
	res StepResult,
	err error,
) {
	data := session.StepCompletedData{
		AgentID: agentID,
		Usage:   res.Usage,
	}
	if err != nil {
		data.Error = err.Error()
	}
	_ = s.Append(context.WithoutCancel(ctx), session.NewStepCompletedEvent(s.ID(), data))
}

func appendAssistantResponse(
	ctx context.Context,
	s *session.Session,
	providerID string,
	req *llm.Request,
	resp llm.Response,
) (string, bool, error) {
	msg := llm.Message{
		Role:           llm.RoleAssistant,
		Content:        resp.Content,
		Reasoning:      resp.Reasoning,
		ThinkingBlocks: resp.ThinkingBlocks,
		Calls:          resp.Calls,
	}
	llm.RecordUsage(ctx, providerID, req.Model, resp.Usage)
	if !hasAssistantPayload(msg) {
		return "", false, nil
	}

	e := session.NewEvent(s.ID(), session.MessageAdded, msg)
	e.Cost = resp.Usage.Cost
	if err := s.Append(ctx, e); err != nil {
		return "", false, err
	}
	return e.ID.String(), true, nil
}
