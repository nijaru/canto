package memory

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/go-json-experiment/json"
)

func (s *CoreStore) UpsertMemory(ctx context.Context, memory Memory) error {
	metadata, err := json.Marshal(memory.Metadata)
	if err != nil {
		return err
	}
	updatedAt := memory.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memories (
			id, scope, scope_id, role, memory_key, content, metadata,
			observed_at, valid_from, valid_to, supersedes, superseded_by, forgotten_at,
			updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			role=excluded.role,
			memory_key=excluded.memory_key,
			content=excluded.content,
			metadata=excluded.metadata,
			observed_at=excluded.observed_at,
			valid_from=excluded.valid_from,
			valid_to=excluded.valid_to,
			supersedes=excluded.supersedes,
			superseded_by=excluded.superseded_by,
			forgotten_at=excluded.forgotten_at,
			updated_at=excluded.updated_at
	`,
		memory.ID,
		string(memory.Namespace.Scope),
		memory.Namespace.ID,
		string(memory.Role),
		memory.Key,
		memory.Content,
		string(metadata),
		formatOptionalTime(memory.ObservedAt),
		formatOptionalTime(memory.ValidFrom),
		formatOptionalTime(memory.ValidTo),
		emptyToNil(memory.Supersedes),
		emptyToNil(memory.SupersededBy),
		formatOptionalTime(memory.ForgottenAt),
		updatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *CoreStore) GetMemory(ctx context.Context, id string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+memorySelectColumns+`
		FROM memories m
		WHERE m.id = ?
	`, id)
	memory, err := scanMemory(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &memory, nil
}

func (s *CoreStore) ListMemories(
	ctx context.Context,
	input MemoryListInput,
) ([]Memory, error) {
	if input.Limit <= 0 {
		input.Limit = 32
	}
	args := []any{}
	query := "SELECT " + memorySelectColumns + " FROM memories m"

	var where []string
	if len(input.Namespaces) > 0 {
		var parts []string
		for _, ns := range input.Namespaces {
			parts = append(parts, "(m.scope = ? AND m.scope_id = ?)")
			args = append(args, string(ns.Scope), ns.ID)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if len(input.Roles) > 0 {
		placeholders := make([]string, len(input.Roles))
		for i, role := range input.Roles {
			placeholders[i] = "?"
			args = append(args, string(role))
		}
		where = append(where, "m.role IN ("+strings.Join(placeholders, ",")+")")
	}
	if !input.IncludeForgotten {
		where = append(where, "m.forgotten_at IS NULL")
	}
	if !input.IncludeSuperseded {
		where = append(where, "(m.superseded_by IS NULL OR m.superseded_by = '')")
	}
	if input.ValidAt != nil {
		when := input.ValidAt.UTC().Format(time.RFC3339Nano)
		where = append(where, "(m.valid_from IS NULL OR m.valid_from <= ?)")
		args = append(args, when)
		where = append(where, "(m.valid_to IS NULL OR m.valid_to >= ?)")
		args = append(args, when)
	}
	if input.ObservedAfter != nil {
		where = append(where, "m.observed_at IS NOT NULL AND m.observed_at >= ?")
		args = append(args, input.ObservedAfter.UTC().Format(time.RFC3339Nano))
	}
	if input.ObservedBefore != nil {
		where = append(where, "m.observed_at IS NOT NULL AND m.observed_at <= ?")
		args = append(args, input.ObservedBefore.UTC().Format(time.RFC3339Nano))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY m.updated_at DESC, m.id ASC LIMIT ?"
	args = append(args, input.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var memories []Memory
	for rows.Next() {
		memory, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if !matchesFilters(memory.Metadata, input.Filters) {
			continue
		}
		memories = append(memories, memory)
	}
	return memories, rows.Err()
}

func (s *CoreStore) SearchMemories(ctx context.Context, input SearchInput) ([]Memory, error) {
	if input.Limit <= 0 {
		input.Limit = 5
	}
	args := []any{}
	base := "SELECT " + memorySelectColumns + " FROM memories m"
	if input.Text != "" {
		base += ` JOIN memories_fts f ON f.rowid = m.rowid `
	}

	var where []string
	if input.Text != "" {
		where = append(where, "f.content MATCH ?")
		args = append(args, escapeFTS(input.Text))
	}
	if len(input.Namespaces) > 0 {
		var parts []string
		for _, ns := range input.Namespaces {
			parts = append(parts, "(m.scope = ? AND m.scope_id = ?)")
			args = append(args, string(ns.Scope), ns.ID)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if len(input.Roles) > 0 {
		placeholders := make([]string, len(input.Roles))
		for i, role := range input.Roles {
			placeholders[i] = "?"
			args = append(args, string(role))
		}
		where = append(where, "m.role IN ("+strings.Join(placeholders, ",")+")")
	}
	if !input.IncludeForgotten {
		where = append(where, "m.forgotten_at IS NULL")
	}
	if !input.IncludeSuperseded {
		where = append(where, "(m.superseded_by IS NULL OR m.superseded_by = '')")
	}
	if input.ValidAt != nil {
		when := input.ValidAt.UTC().Format(time.RFC3339Nano)
		where = append(where, "(m.valid_from IS NULL OR m.valid_from <= ?)")
		args = append(args, when)
		where = append(where, "(m.valid_to IS NULL OR m.valid_to >= ?)")
		args = append(args, when)
	}
	if input.ObservedAfter != nil {
		where = append(where, "m.observed_at IS NOT NULL AND m.observed_at >= ?")
		args = append(args, input.ObservedAfter.UTC().Format(time.RFC3339Nano))
	}
	if input.ObservedBefore != nil {
		where = append(where, "m.observed_at IS NOT NULL AND m.observed_at <= ?")
		args = append(args, input.ObservedBefore.UTC().Format(time.RFC3339Nano))
	}
	if len(where) > 0 {
		base += " WHERE " + strings.Join(where, " AND ")
	}
	base += " ORDER BY m.updated_at DESC LIMIT ?"
	args = append(args, input.Limit)

	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var memories []Memory
	for rows.Next() {
		memory, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if !matchesFilters(memory.Metadata, input.Filters) {
			continue
		}
		if input.Text == "" && input.IncludeRecent {
			memory.Score = 0.6
		} else if input.Text != "" {
			memory.Score = 0.8
		}
		memories = append(memories, memory)
	}
	return memories, rows.Err()
}
