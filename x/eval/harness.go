package eval

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nijaru/canto/session"
)

// HarnessTaskSpec captures the common inputs used by external eval harnesses.
type HarnessTaskSpec struct {
	TaskID          string
	InstructionText string
	EnvironmentID   string
	WorkspacePath   string
	Repository      string
	BaseCommit      string
	ContainerImage  string
	SetupCommands   []string
	TestCommands    []string
	Notes           []string
	Metadata        map[string]any
}

// HarnessEnvironment seeds the session with harness-specific context.
type HarnessEnvironment struct {
	EnvironmentID  string
	HarnessName    string
	TaskID         string
	WorkspacePath  string
	Repository     string
	BaseCommit     string
	ContainerImage string
	SetupCommands  []string
	TestCommands   []string
	Notes          []string
	Metadata       map[string]any
}

// ID returns the stable environment identifier.
func (e HarnessEnvironment) ID() string {
	if e.EnvironmentID != "" {
		return e.EnvironmentID
	}
	if e.HarnessName == "" {
		return e.TaskID
	}
	if e.TaskID == "" {
		return e.HarnessName
	}
	return e.HarnessName + ":" + e.TaskID
}

// Bootstrap appends a deterministic harness summary to the session.
func (e HarnessEnvironment) Bootstrap(ctx context.Context, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("eval: nil session")
	}

	content := e.render()
	if content == "" {
		return nil
	}

	return sess.AppendContext(ctx, session.ContextEntry{
		Kind:    session.ContextKindHarness,
		Content: content,
	})
}

func (e HarnessEnvironment) render() string {
	var sb strings.Builder
	writeLine := func(label, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&sb, "%s: %s\n", label, value)
	}

	writeLine("Harness", e.HarnessName)
	writeLine("Task", e.TaskID)
	writeLine("Workspace", e.WorkspacePath)
	writeLine("Repository", e.Repository)
	writeLine("Base commit", e.BaseCommit)
	writeLine("Container image", e.ContainerImage)

	writeList := func(title string, values []string) {
		if len(values) == 0 {
			return
		}
		sb.WriteString(title)
		sb.WriteString(":\n")
		for _, value := range values {
			if value == "" {
				continue
			}
			sb.WriteString("- ")
			sb.WriteString(value)
			sb.WriteByte('\n')
		}
	}

	writeList("Setup commands", e.SetupCommands)
	writeList("Test commands", e.TestCommands)
	writeList("Notes", e.Notes)

	if len(e.Metadata) > 0 {
		keys := make([]string, 0, len(e.Metadata))
		for key := range e.Metadata {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		sb.WriteString("Metadata:\n")
		for _, key := range keys {
			fmt.Fprintf(&sb, "- %s: %v\n", key, e.Metadata[key])
		}
	}

	return strings.TrimSpace(sb.String())
}

// HarborConnector adapts Harbor-style harness specs into Canto tasks.
type HarborConnector struct{}

// Connect builds a task for Harbor-oriented eval harnesses.
func (HarborConnector) Connect(spec HarnessTaskSpec) (TaskSpec, error) {
	if spec.TaskID == "" {
		return TaskSpec{}, fmt.Errorf("eval: harbor task id is required")
	}
	if spec.WorkspacePath == "" {
		return TaskSpec{}, fmt.Errorf("eval: harbor workspace path is required")
	}

	env := HarnessEnvironment{
		EnvironmentID:  spec.EnvironmentID,
		HarnessName:    "Harbor",
		TaskID:         spec.TaskID,
		WorkspacePath:  spec.WorkspacePath,
		ContainerImage: spec.ContainerImage,
		SetupCommands:  slices.Clone(spec.SetupCommands),
		TestCommands:   slices.Clone(spec.TestCommands),
		Notes:          slices.Clone(spec.Notes),
		Metadata:       cloneAnyMap(spec.Metadata),
	}
	return TaskSpec{
		TaskID:          spec.TaskID,
		InstructionText: spec.InstructionText,
		Env:             env,
	}, nil
}

// SWEBenchConnector adapts SWE-bench-style harness specs into Canto tasks.
type SWEBenchConnector struct{}

// Connect builds a task for SWE-bench-style eval harnesses.
func (SWEBenchConnector) Connect(spec HarnessTaskSpec) (TaskSpec, error) {
	if spec.TaskID == "" {
		return TaskSpec{}, fmt.Errorf("eval: swe-bench task id is required")
	}
	if spec.Repository == "" {
		return TaskSpec{}, fmt.Errorf("eval: swe-bench repository is required")
	}

	env := HarnessEnvironment{
		EnvironmentID: spec.EnvironmentID,
		HarnessName:   "SWE-bench",
		TaskID:        spec.TaskID,
		WorkspacePath: spec.WorkspacePath,
		Repository:    spec.Repository,
		BaseCommit:    spec.BaseCommit,
		SetupCommands: slices.Clone(spec.SetupCommands),
		TestCommands:  slices.Clone(spec.TestCommands),
		Notes:         slices.Clone(spec.Notes),
		Metadata:      cloneAnyMap(spec.Metadata),
	}
	return TaskSpec{
		TaskID:          spec.TaskID,
		InstructionText: spec.InstructionText,
		Env:             env,
	}, nil
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
