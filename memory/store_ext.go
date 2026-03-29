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

func (s *CoreStore) ListBlocks(ctx context.Context, namespaces []Namespace) ([]Block, error) {
	query := "SELECT scope, scope_id, name, content, metadata, updated_at FROM memory_blocks"
	var where []string
	var args []any
	if len(namespaces) > 0 {
		var parts []string
		for _, ns := range namespaces {
			parts = append(parts, "(scope = ? AND scope_id = ?)")
			args = append(args, string(ns.Scope), ns.ID)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC, name ASC"
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
		INSERT INTO memories (id, scope, scope_id, role, memory_key, content, metadata, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			role=excluded.role,
			memory_key=excluded.memory_key,
			content=excluded.content,
			metadata=excluded.metadata,
			updated_at=excluded.updated_at
	`, memory.ID, string(memory.Namespace.Scope), memory.Namespace.ID, string(memory.Role), memory.Key, memory.Content, string(metadata), updatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *CoreStore) GetMemory(ctx context.Context, id string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT scope, scope_id, role, memory_key, content, metadata, updated_at
		FROM memories WHERE id = ?
	`, id)
	var scope, scopeID, role, key, content, metadata, updatedAt string
	if err := row.Scan(&scope, &scopeID, &role, &key, &content, &metadata, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	mem := &Memory{
		ID:        id,
		Namespace: Namespace{Scope: Scope(scope), ID: scopeID},
		Role:      Role(role),
		Key:       key,
		Content:   content,
	}
	if metadata != "" {
		if err := json.Unmarshal([]byte(metadata), &mem.Metadata); err != nil {
			return nil, err
		}
	}
	mem.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return mem, nil
}

func (s *CoreStore) SearchMemories(
	ctx context.Context,
	namespaces []Namespace,
	roles []Role,
	query string,
	limit int,
	filters map[string]any,
	includeRecent bool,
) ([]Memory, error) {
	if limit <= 0 {
		limit = 5
	}
	args := []any{}
	base := `
		SELECT m.id, m.scope, m.scope_id, m.role, m.memory_key, m.content, m.metadata, m.updated_at
		FROM memories m
	`
	if query != "" {
		base += `JOIN memories_fts f ON f.rowid = m.rowid `
	}

	var where []string
	if query != "" {
		where = append(where, "f.content MATCH ?")
		args = append(args, escapeFTS(query))
	}
	if len(namespaces) > 0 {
		var parts []string
		for _, ns := range namespaces {
			parts = append(parts, "(m.scope = ? AND m.scope_id = ?)")
			args = append(args, string(ns.Scope), ns.ID)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if len(roles) > 0 {
		placeholders := make([]string, len(roles))
		for i, role := range roles {
			placeholders[i] = "?"
			args = append(args, string(role))
		}
		where = append(where, "m.role IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(where) > 0 {
		base += " WHERE " + strings.Join(where, " AND ")
	}
	base += " ORDER BY m.updated_at DESC LIMIT ?"
	args = append(args, limit)

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
		if !matchesFilters(memory.Metadata, filters) {
			continue
		}
		if query == "" && includeRecent {
			memory.Score = 0.6
		} else if query != "" {
			memory.Score = 0.8
		}
		memories = append(memories, memory)
	}
	return memories, rows.Err()
}

func scanMemory(scanner interface{ Scan(dest ...any) error }) (Memory, error) {
	var memory Memory
	var scope, scopeID, role, key, metadata, updatedAt string
	if err := scanner.Scan(&memory.ID, &scope, &scopeID, &role, &key, &memory.Content, &metadata, &updatedAt); err != nil {
		return Memory{}, err
	}
	memory.Namespace = Namespace{Scope: Scope(scope), ID: scopeID}
	memory.Role = Role(role)
	memory.Key = key
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
