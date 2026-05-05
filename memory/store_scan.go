package memory

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/go-json-experiment/json"
)

const memorySelectColumns = `
	m.id, m.scope, m.scope_id, m.role, m.memory_key, m.content, m.metadata,
	m.observed_at, m.valid_from, m.valid_to, m.supersedes, m.superseded_by, m.forgotten_at,
	m.updated_at
`

func scanMemory(scanner interface{ Scan(dest ...any) error }) (Memory, error) {
	var memory Memory
	var scope, scopeID, role, key, metadata, updatedAt string
	var observedAt, validFrom, validTo, supersedes, supersededBy, forgottenAt sql.NullString
	if err := scanner.Scan(
		&memory.ID,
		&scope,
		&scopeID,
		&role,
		&key,
		&memory.Content,
		&metadata,
		&observedAt,
		&validFrom,
		&validTo,
		&supersedes,
		&supersededBy,
		&forgottenAt,
		&updatedAt,
	); err != nil {
		return Memory{}, err
	}
	memory.Namespace = Namespace{Scope: Scope(scope), ID: scopeID}
	memory.Role = Role(role)
	memory.Key = key
	memory.ObservedAt = parseNullTime(observedAt)
	memory.ValidFrom = parseNullTime(validFrom)
	memory.ValidTo = parseNullTime(validTo)
	memory.Supersedes = nullStringValue(supersedes)
	memory.SupersededBy = nullStringValue(supersededBy)
	memory.ForgottenAt = parseNullTime(forgottenAt)
	if metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &memory.Metadata); err != nil {
			return Memory{}, err
		}
	}
	memory.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return memory, nil
}

func matchesFilters(metadata map[string]any, filters map[string]any) bool {
	if len(filters) == 0 {
		return true
	}
	for key, want := range filters {
		got, ok := metadata[key]
		if !ok || fmt.Sprint(got) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
