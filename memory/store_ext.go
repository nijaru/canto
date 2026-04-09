package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-json-experiment/json"
)

func (s *CoreStore) UpsertBlock(ctx context.Context, block Block) error {
	metadata, err := json.Marshal(block.Metadata)
	if err != nil {
		return err
	}
	updatedAt := block.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_blocks (scope, scope_id, name, content, metadata, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, scope_id, name) DO UPDATE SET
			content=excluded.content,
			metadata=excluded.metadata,
			updated_at=excluded.updated_at
	`, string(block.Namespace.Scope), block.Namespace.ID, block.Name, block.Content, string(metadata), updatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *CoreStore) GetBlock(
	ctx context.Context,
	namespace Namespace,
	name string,
) (*Block, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT content, metadata, updated_at
		FROM memory_blocks
		WHERE scope = ? AND scope_id = ? AND name = ?
	`, string(namespace.Scope), namespace.ID, name)
	var block Block
	var metadata string
	var updatedAt string
	if err := row.Scan(&block.Content, &metadata, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	block.Namespace = namespace
	block.Name = name
	if metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &block.Metadata); err != nil {
			return nil, err
		}
	}
	block.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &block, nil
}

func (s *CoreStore) ListBlocks(ctx context.Context, input BlockListInput) ([]Block, error) {
	query := "SELECT scope, scope_id, name, content, metadata, updated_at FROM memory_blocks"
	var where []string
	var args []any
	if len(input.Namespaces) > 0 {
		var parts []string
		for _, ns := range input.Namespaces {
			parts = append(parts, "(scope = ? AND scope_id = ?)")
			args = append(args, string(ns.Scope), ns.ID)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC, name ASC"
	if input.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, input.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var blocks []Block
	for rows.Next() {
		var block Block
		var scope, scopeID, metadata, updatedAt string
		if err := rows.Scan(&scope, &scopeID, &block.Name, &block.Content, &metadata, &updatedAt); err != nil {
			return nil, err
		}
		block.Namespace = Namespace{Scope: Scope(scope), ID: scopeID}
		if metadata != "" {
			if err := json.Unmarshal([]byte(metadata), &block.Metadata); err != nil {
				return nil, err
			}
		}
		block.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

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
		SELECT scope, scope_id, role, memory_key, content, metadata,
		       observed_at, valid_from, valid_to, supersedes, superseded_by, forgotten_at,
		       updated_at
		FROM memories WHERE id = ?
	`, id)
	var scope, scopeID, role, key, content, metadata, updatedAt string
	var observedAt, validFrom, validTo, supersedes, supersededBy, forgottenAt sql.NullString
	if err := row.Scan(
		&scope,
		&scopeID,
		&role,
		&key,
		&content,
		&metadata,
		&observedAt,
		&validFrom,
		&validTo,
		&supersedes,
		&supersededBy,
		&forgottenAt,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	mem := &Memory{
		ID:           id,
		Namespace:    Namespace{Scope: Scope(scope), ID: scopeID},
		Role:         Role(role),
		Key:          key,
		Content:      content,
		ObservedAt:   parseNullTime(observedAt),
		ValidFrom:    parseNullTime(validFrom),
		ValidTo:      parseNullTime(validTo),
		Supersedes:   nullStringValue(supersedes),
		SupersededBy: nullStringValue(supersededBy),
		ForgottenAt:  parseNullTime(forgottenAt),
	}
	if metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &mem.Metadata); err != nil {
			return nil, err
		}
	}
	mem.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return mem, nil
}

func (s *CoreStore) ListMemories(
	ctx context.Context,
	input MemoryListInput,
) ([]Memory, error) {
	if input.Limit <= 0 {
		input.Limit = 32
	}
	args := []any{}
	query := `
		SELECT m.id, m.scope, m.scope_id, m.role, m.memory_key, m.content, m.metadata,
		       m.observed_at, m.valid_from, m.valid_to, m.supersedes, m.superseded_by, m.forgotten_at,
		       m.updated_at
		FROM memories m
	`

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
	base := `
		SELECT m.id, m.scope, m.scope_id, m.role, m.memory_key, m.content, m.metadata,
		       m.observed_at, m.valid_from, m.valid_to, m.supersedes, m.superseded_by, m.forgotten_at,
		       m.updated_at
		FROM memories m
	`
	if input.Text != "" {
		base += `JOIN memories_fts f ON f.rowid = m.rowid `
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
