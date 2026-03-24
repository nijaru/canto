package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
)

type workerTask struct {
	ID          string
	AgentID     string
	Task        string
	Context     string
	Summary     string
	ArtifactURI string
}

type workerAgent struct {
	id      string
	summary string
}

func (a *workerAgent) ID() string { return a.id }

func (a *workerAgent) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	msg := llm.Message{Role: llm.RoleAssistant, Content: a.summary}
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), msg)); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: a.summary}, nil
}

func (a *workerAgent) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return a.Step(ctx, sess)
}

type orchestrationResult struct {
	Summary string
	Run     *session.RunLog
}

func runExample(ctx context.Context) (orchestrationResult, error) {
	dbPath, err := tempDBPath()
	if err != nil {
		return orchestrationResult{}, err
	}
	defer os.Remove(dbPath)

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		return orchestrationResult{}, err
	}
	defer store.Close()

	parent := session.New("release-review").WithWriter(store)
	if err := parent.Append(ctx, session.NewMessage(parent.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "Review the release candidate and return merge-ready findings.",
	})); err != nil {
		return orchestrationResult{}, err
	}

	tasks := []workerTask{
		{
			ID:          "api-review",
			AgentID:     "api-worker",
			Task:        "Review API stability risks.",
			Context:     "Focus on public surface changes and migration friction.",
			Summary:     "API surface is stable; the main remaining work is documenting child-run lifecycle semantics.",
			ArtifactURI: "memory://release-review/api-review.md",
		},
		{
			ID:          "docs-review",
			AgentID:     "docs-worker",
			Task:        "Review migration and getting-started docs.",
			Context:     "Focus on clear framework-vs-agent boundaries.",
			Summary:     "Docs should describe Canto as a framework and keep planner policy out of framework examples.",
			ArtifactURI: "memory://release-review/docs-review.md",
		},
		{
			ID:          "runtime-review",
			AgentID:     "runtime-worker",
			Task:        "Review runtime child-session behavior.",
			Context:     "Focus on orchestration events, artifact refs, and nested run export.",
			Summary:     "Runtime child sessions now emit durable lifecycle facts and can be exported as nested trajectories.",
			ArtifactURI: "memory://release-review/runtime-review.md",
		},
	}

	childRunner := runtime.NewChildRunner(store)
	childRunner.MaxConcurrent = 2
	defer childRunner.Close()

	refs := make([]runtime.ChildRef, 0, len(tasks))
	for _, task := range tasks {
		ref, err := childRunner.Spawn(ctx, parent, runtime.ChildSpec{
			ID:      task.ID,
			Agent:   &workerAgent{id: task.AgentID, summary: task.Summary},
			Mode:    session.ChildModeHandoff,
			Task:    task.Task,
			Context: task.Context,
			InitialMessages: []llm.Message{
				{
					Role: llm.RoleUser,
					Content: strings.Join([]string{
						task.Task,
						"Context: " + task.Context,
						"Return one concise summary for the parent orchestrator.",
					}, "\n"),
				},
			},
		})
		if err != nil {
			return orchestrationResult{}, err
		}
		refs = append(refs, ref)
	}

	findings := make([]string, 0, len(refs))
	for i, ref := range refs {
		result, err := childRunner.Wait(ctx, ref.ID)
		if err != nil {
			return orchestrationResult{}, err
		}

		artifact := session.ArtifactRef{
			ID:    "artifact-" + ref.ID,
			Kind:  "review_note",
			URI:   tasks[i].ArtifactURI,
			Label: tasks[i].Task,
		}
		if err := parent.Append(ctx, session.NewArtifactRecordedEvent(parent.ID(), session.ArtifactRecordedData{
			ChildID:  ref.ID,
			Artifact: artifact,
		})); err != nil {
			return orchestrationResult{}, err
		}
		if err := parent.Append(ctx, session.NewChildMergedEvent(parent.ID(), session.ChildMergedData{
			ChildID:        ref.ID,
			ChildSessionID: ref.SessionID,
			ArtifactIDs:    []string{artifact.ID},
			Note:           "Merged worker summary into the parent release report.",
		})); err != nil {
			return orchestrationResult{}, err
		}

		findings = append(findings, fmt.Sprintf("- %s: %s", ref.ID, result.Result.Content))
	}

	summary := "Merged release review:\n" + strings.Join(findings, "\n")
	if err := parent.Append(ctx, session.NewMessage(parent.ID(), llm.Message{
		Role:    llm.RoleAssistant,
		Content: summary,
	})); err != nil {
		return orchestrationResult{}, err
	}

	run, err := session.ExportRunTree(parent, func(sessionID string) (*session.Session, error) {
		return store.Load(ctx, sessionID)
	})
	if err != nil {
		return orchestrationResult{}, err
	}
	return orchestrationResult{Summary: summary, Run: run}, nil
}

func tempDBPath() (string, error) {
	file, err := os.CreateTemp("", "canto-subagents-*.db")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}
