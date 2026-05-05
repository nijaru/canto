package memory

import (
	"context"
	"database/sql"
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
