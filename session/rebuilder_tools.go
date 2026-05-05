package session

import (
	"fmt"
	"strings"

	"github.com/nijaru/canto/llm"
)

type toolLifecycle struct {
	started      ToolStartedData
	completed    ToolCompletedData
	completedID  string
	hasStarted   bool
	hasCompleted bool
}

func recoverCompletedToolResults(entries []HistoryEntry, events []Event) ([]HistoryEntry, error) {
	lifecycle, err := toolLifecycleByID(events)
	if err != nil {
		return nil, err
	}

	out := make([]HistoryEntry, 0, len(entries))
	for i := 0; i < len(entries); {
		entry := entries[i]
		msg := normalizeTranscriptMessage(entry.Message)
		if msg.Role != llm.RoleAssistant || len(msg.Calls) == 0 {
			out = append(out, entry)
			i++
			continue
		}

		existing := make(map[string][]HistoryEntry)
		j := i + 1
		for j < len(entries) {
			next := entries[j]
			next.Message = normalizeTranscriptMessage(next.Message)
			if next.Message.Role != llm.RoleTool {
				break
			}
			if next.Message.ToolID != "" {
				existing[next.Message.ToolID] = append(existing[next.Message.ToolID], next)
			}
			j++
		}

		assistant := entry
		keptCalls := make([]llm.Call, 0, len(msg.Calls))
		toolEntries := make([]HistoryEntry, 0, len(msg.Calls))
		for _, call := range msg.Calls {
			if call.ID == "" {
				continue
			}
			queue := existing[call.ID]
			if len(queue) > 0 {
				toolEntries = append(toolEntries, queue[0])
				existing[call.ID] = queue[1:]
				keptCalls = append(keptCalls, call)
				continue
			}
			record, ok := lifecycle[call.ID]
			if !ok || !record.hasCompleted {
				continue
			}
			toolEntries = append(toolEntries, toolEntryFromLifecycle(call, record))
			keptCalls = append(keptCalls, call)
		}
		assistant.Message.Calls = keptCalls
		out = append(out, assistant)
		out = append(out, toolEntries...)
		i = j
	}
	return out, nil
}

func toolEntryFromLifecycle(call llm.Call, record toolLifecycle) HistoryEntry {
	name := record.completed.Tool
	if name == "" {
		name = record.started.Tool
	}
	if name == "" {
		name = call.Function.Name
	}
	content := record.completed.Output
	if record.completed.Error != "" && !strings.Contains(content, record.completed.Error) {
		content = strings.TrimSpace(
			strings.TrimSpace(content) + "\n" + fmt.Sprintf("Error: %s", record.completed.Error),
		)
	}
	tool := mergeToolHistory(nil, llm.Message{
		Role:   llm.RoleTool,
		ToolID: call.ID,
		Name:   name,
	}, record)
	return HistoryEntry{
		EventID:   record.completedID,
		EventType: ToolCompleted,
		Message: llm.Message{
			Role:    llm.RoleTool,
			ToolID:  call.ID,
			Name:    name,
			Content: content,
		},
		Tool: &tool,
	}
}

func toolLifecycleByID(events []Event) (map[string]toolLifecycle, error) {
	lifecycle := make(map[string]toolLifecycle)
	for i := range events {
		e := &events[i]
		switch e.Type {
		case ToolStarted:
			data, ok, err := e.ToolStartedData()
			if err != nil {
				return nil, err
			}
			if !ok || data.ID == "" {
				continue
			}
			record := lifecycle[data.ID]
			record.started = data
			record.hasStarted = true
			lifecycle[data.ID] = record
		case ToolCompleted:
			data, ok, err := e.ToolCompletedData()
			if err != nil {
				return nil, err
			}
			if !ok || data.ID == "" {
				continue
			}
			record := lifecycle[data.ID]
			record.completed = data
			record.completedID = e.ID.String()
			record.hasCompleted = true
			lifecycle[data.ID] = record
		}
	}
	return lifecycle, nil
}

func withToolHistory(entries []HistoryEntry, events []Event) ([]HistoryEntry, error) {
	lifecycle, err := toolLifecycleByID(events)
	if err != nil {
		return nil, err
	}
	if len(lifecycle) == 0 {
		return entries, nil
	}

	for i := range entries {
		if entries[i].Message.Role != llm.RoleTool || entries[i].Message.ToolID == "" {
			continue
		}
		record, ok := lifecycle[entries[i].Message.ToolID]
		if !ok && entries[i].Tool == nil {
			continue
		}
		tool := mergeToolHistory(entries[i].Tool, entries[i].Message, record)
		entries[i].Tool = &tool
	}
	return entries, nil
}

func mergeToolHistory(existing *ToolHistory, msg llm.Message, record toolLifecycle) ToolHistory {
	var tool ToolHistory
	if existing != nil {
		tool = *existing
	}
	if tool.ID == "" {
		tool.ID = msg.ToolID
	}
	if tool.Name == "" {
		tool.Name = msg.Name
	}
	if record.hasStarted {
		if tool.Name == "" {
			tool.Name = record.started.Tool
		}
		if tool.Arguments == "" {
			tool.Arguments = record.started.Arguments
		}
		if tool.IdempotencyKey == "" {
			tool.IdempotencyKey = record.started.IdempotencyKey
		}
	}
	if record.hasCompleted {
		if tool.Name == "" {
			tool.Name = record.completed.Tool
		}
		if tool.IdempotencyKey == "" {
			tool.IdempotencyKey = record.completed.IdempotencyKey
		}
		if record.completed.Error != "" {
			tool.IsError = true
			if tool.Error == "" {
				tool.Error = record.completed.Error
			}
		}
	}
	return tool
}

func pendingToolCalls(events []Event) (map[string]int, error) {
	pending := make(map[string]int)
	for i := range events {
		e := &events[i]
		if e.Type != MessageAdded {
			continue
		}
		msg, err := e.ensureMessage()
		if err != nil {
			return nil, err
		}
		switch msg.Role {
		case llm.RoleAssistant:
			clear(pending)
			addPendingToolCalls(pending, msg.Calls)
		case llm.RoleTool:
			if msg.ToolID == "" || pending[msg.ToolID] == 0 {
				continue
			}
			pending[msg.ToolID]--
			if pending[msg.ToolID] == 0 {
				delete(pending, msg.ToolID)
			}
		default:
			clear(pending)
		}
	}
	return pending, nil
}

func addPendingToolCalls(pending map[string]int, calls []llm.Call) {
	for _, call := range calls {
		if call.ID == "" {
			continue
		}
		pending[call.ID]++
	}
}
