package session

import (
	"slices"
	"strings"
	"time"

	"github.com/nijaru/canto/llm"
	"github.com/oklog/ulid/v2"
)

// RunLog represents a structured trace of an agent's execution.
// It is used for evaluation, reinforcement learning (RL) fine-tuning,
// and offline analysis.
type RunLog struct {
	SessionID string         `json:"session_id"`
	AgentID   string         `json:"agent_id"`
	StartTime time.Time      `json:"start_time"`
	EndTime   time.Time      `json:"end_time"`
	Turns     []RunTurn      `json:"turns"`
	ChildRuns []ChildRunLog  `json:"child_runs,omitzero"`
	TotalCost float64        `json:"total_cost"`
	Metadata  map[string]any `json:"metadata,omitzero"`
}

// ChildRunLog records a child run linked from a parent session.
type ChildRunLog struct {
	ChildID   string         `json:"child_id"`
	SessionID string         `json:"session_id"`
	AgentID   string         `json:"agent_id"`
	Mode      ChildMode      `json:"mode"`
	Status    ChildStatus    `json:"status"`
	Summary   string         `json:"summary,omitzero"`
	Artifacts []ArtifactRef  `json:"artifacts,omitzero"`
	Run       *RunLog        `json:"run,omitzero"`
	Metadata  map[string]any `json:"metadata,omitzero"`
}

// RunTurn represents a single perceive-decide-act-observe loop.
type RunTurn struct {
	TurnID      string         `json:"turn_id"`
	Timestamp   time.Time      `json:"timestamp"`
	Input       []llm.Message  `json:"input"`
	Output      llm.Message    `json:"output"`
	ToolCalls   []llm.Call     `json:"tool_calls,omitzero"`
	ToolResults []llm.Message  `json:"tool_results,omitzero"`
	Cost        float64        `json:"cost"`
	Metrics     map[string]any `json:"metrics,omitzero"`
}

// Episode is a compressed record of a completed agent run.
// It captures only the signal — successful tool call pairs and the final conclusion —
// discarding the raw conversation transcript. Orchestrators retrieve episodes from
// archival memory rather than full session logs, keeping swarm coordination practical at scale.
type Episode struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	AgentID    string         `json:"agent_id"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	Conclusion string         `json:"conclusion"` // last assistant message without tool calls
	Calls      []EpisodeCall  `json:"calls,omitzero"`
	TotalCost  float64        `json:"total_cost"`
	Metadata   map[string]any `json:"metadata,omitzero"`
}

// EpisodeCall is a single successful tool invocation captured in an Episode.
type EpisodeCall struct {
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Result string `json:"result"`
}

// Text returns the searchable text for this Episode: conclusion followed by tool names.
// Used as FTS5 content when storing in memory.
func (ep *Episode) Text() string {
	var sb strings.Builder
	sb.WriteString(ep.Conclusion)
	for _, c := range ep.Calls {
		sb.WriteByte(' ')
		sb.WriteString(c.Tool)
	}
	return sb.String()
}

// Distill compresses a RunLog into an Episode by extracting only the signal:
// successful tool call pairs (call + result) and the final textual conclusion.
// The raw conversation transcript is discarded. The returned Episode is ready for
// storage in an archival memory store so orchestrators can retrieve completed work
// without loading full session logs.
func Distill(traj *RunLog) *Episode {
	ep := &Episode{
		ID:        ulid.Make().String(),
		SessionID: traj.SessionID,
		AgentID:   traj.AgentID,
		StartTime: traj.StartTime,
		EndTime:   traj.EndTime,
		TotalCost: traj.TotalCost,
	}

	for _, turn := range traj.Turns {
		// Map tool results by call ID for O(1) pairing.
		resultsByID := make(map[string]string, len(turn.ToolResults))
		for _, r := range turn.ToolResults {
			resultsByID[r.ToolID] = r.Content
		}

		for _, call := range turn.ToolCalls {
			result, ok := resultsByID[call.ID]
			if !ok {
				continue // skip calls with no matching result
			}
			ep.Calls = append(ep.Calls, EpisodeCall{
				Tool:   call.Function.Name,
				Args:   call.Function.Arguments,
				Result: result,
			})
		}

		// Track the final conclusion: last assistant message with no tool calls.
		if len(turn.ToolCalls) == 0 && turn.Output.Content != "" {
			ep.Conclusion = turn.Output.Content
		}
	}

	return ep
}

// ExportRun converts a session's event log into a structured RunLog.
func ExportRun(sess *Session) (*RunLog, error) {
	return exportRun(sess)
}

// ExportRunTree converts a session's event log into a structured RunLog and,
// when load is provided, recursively attaches child runs referenced by durable
// child lifecycle events.
func ExportRunTree(sess *Session, load func(sessionID string) (*Session, error)) (*RunLog, error) {
	return exportRunTree(sess, load, make(map[string]struct{}))
}

func exportRun(sess *Session) (*RunLog, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	events := sess.events
	if len(events) == 0 {
		return &RunLog{
			SessionID: sess.ID(),
			Turns:     []RunTurn{},
		}, nil
	}

	traj := &RunLog{
		SessionID: sess.ID(),
		StartTime: events[0].Timestamp,
		EndTime:   events[len(events)-1].Timestamp,
		Metadata:  make(map[string]any),
		Turns:     make([]RunTurn, 0, len(events)/4+1),
	}

	var currentTurn *RunTurn
	var inputBuffer []llm.Message

	for i := range events {
		e := &events[i]
		traj.TotalCost += e.Cost

		switch e.Type {
		case ContextAdded:
			entry, err := e.ensureContextEntry()
			if err != nil {
				continue
			}
			inputBuffer = append(inputBuffer, contextEntryMessage(*entry))
		case MessageAdded:
			msg, err := e.ensureMessage()
			if err != nil {
				continue
			}

			if msg.Role == llm.RoleUser || msg.Role == llm.RoleSystem {
				inputBuffer = append(inputBuffer, *msg)
			} else if msg.Role == llm.RoleAssistant {
				if currentTurn != nil {
					traj.Turns = append(traj.Turns, *currentTurn)
				}
				currentTurn = &RunTurn{
					TurnID:    e.ID.String(),
					Timestamp: e.Timestamp,
					Input:     make([]llm.Message, len(inputBuffer)),
					Output:    *msg,
					ToolCalls: msg.Calls,
					Cost:      e.Cost,
				}
				copy(currentTurn.Input, inputBuffer)
				inputBuffer = inputBuffer[:0] // Reset input for next turn without re-allocating
			} else if msg.Role == llm.RoleTool && currentTurn != nil {
				currentTurn.ToolResults = append(currentTurn.ToolResults, *msg)
				inputBuffer = append(inputBuffer, *msg)
			}
		}
	}

	if currentTurn != nil {
		traj.Turns = append(traj.Turns, *currentTurn)
	}

	return traj, nil
}

func exportRunTree(
	sess *Session,
	load func(sessionID string) (*Session, error),
	seen map[string]struct{},
) (*RunLog, error) {
	traj, err := exportRun(sess)
	if err != nil {
		return nil, err
	}
	if load == nil {
		return traj, nil
	}
	if _, ok := seen[sess.ID()]; ok {
		return traj, nil
	}
	seen[sess.ID()] = struct{}{}
	defer delete(seen, sess.ID())

	childByID := make(map[string]*ChildRunLog)
	for e := range sess.All() {
		switch e.Type {
		case ChildRequested:
			data, ok, err := e.ChildRequestedData()
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			childByID[data.ChildID] = &ChildRunLog{
				ChildID:   data.ChildID,
				SessionID: data.ChildSessionID,
				AgentID:   data.AgentID,
				Mode:      data.Mode,
				Status:    ChildStatusRequested,
				Metadata:  data.Metadata,
			}
		case ChildStarted:
			data, ok, err := e.ChildStartedData()
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			child := ensureChildRun(childByID, data.ChildID, data.ChildSessionID)
			child.AgentID = data.AgentID
			child.Status = ChildStatusRunning
		case ChildBlocked:
			data, ok, err := e.ChildBlockedData()
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			child := ensureChildRun(childByID, data.ChildID, data.ChildSessionID)
			child.Status = ChildStatusBlocked
		case ChildCompleted:
			data, ok, err := e.ChildCompletedData()
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			child := ensureChildRun(childByID, data.ChildID, data.ChildSessionID)
			child.Status = ChildStatusCompleted
			child.Summary = data.Summary
		case ChildFailed:
			data, ok, err := e.ChildFailedData()
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			child := ensureChildRun(childByID, data.ChildID, data.ChildSessionID)
			child.Status = ChildStatusFailed
		case ChildCanceled:
			data, ok, err := e.ChildCanceledData()
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			child := ensureChildRun(childByID, data.ChildID, data.ChildSessionID)
			child.Status = ChildStatusCanceled
		case ChildMerged:
			data, ok, err := e.ChildMergedData()
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			child := ensureChildRun(childByID, data.ChildID, data.ChildSessionID)
			child.Status = ChildStatusMerged
		case ArtifactRecorded:
			data, ok, err := e.ArtifactRecordedData()
			if err != nil {
				return nil, err
			}
			if !ok || data.ChildID == "" || IsWorkspaceFileReferenceArtifact(data.Artifact) {
				continue
			}
			child := ensureChildRun(childByID, data.ChildID, data.SessionID)
			child.Artifacts = append(child.Artifacts, data.Artifact)
		}
	}

	childIDs := make([]string, 0, len(childByID))
	for childID := range childByID {
		childIDs = append(childIDs, childID)
	}
	slices.Sort(childIDs)

	traj.ChildRuns = make([]ChildRunLog, 0, len(childIDs))
	for _, childID := range childIDs {
		child := childByID[childID]
		if child.SessionID != "" {
			childSess, err := load(child.SessionID)
			if err != nil {
				return nil, err
			}
			if childSess != nil {
				child.Run, err = exportRunTree(childSess, load, seen)
				if err != nil {
					return nil, err
				}
			}
		}
		traj.ChildRuns = append(traj.ChildRuns, *child)
	}
	return traj, nil
}

func ensureChildRun(childByID map[string]*ChildRunLog, childID, sessionID string) *ChildRunLog {
	if child, ok := childByID[childID]; ok {
		if child.SessionID == "" {
			child.SessionID = sessionID
		}
		return child
	}
	child := &ChildRunLog{
		ChildID:   childID,
		SessionID: sessionID,
	}
	childByID[childID] = child
	return child
}
