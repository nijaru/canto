package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// SQLiteCheckpointStore persists graph checkpoints in SQLite.
type SQLiteCheckpointStore struct {
	db *sql.DB
}

// NewSQLiteCheckpointStore creates a new checkpoint store backed by SQLite.
func NewSQLiteCheckpointStore(dsn string) (*SQLiteCheckpointStore, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	} else if !strings.Contains(dsn, "journal_mode") {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}

	store := &SQLiteCheckpointStore{db: db}
	if err := store.init(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteCheckpointStore) init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS graph_checkpoints (
			graph_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			next_node TEXT NOT NULL,
			steps INTEGER NOT NULL,
			last_event_id TEXT NOT NULL,
			usage_json BLOB NOT NULL,
			result_json BLOB NOT NULL,
			completed INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (graph_id, session_id)
		)
	`)
	return err
}

func (s *SQLiteCheckpointStore) Load(
	ctx context.Context,
	graphID, sessionID string,
) (*Checkpoint, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT next_node, steps, last_event_id, usage_json, result_json, completed
		FROM graph_checkpoints
		WHERE graph_id = ? AND session_id = ?
	`, graphID, sessionID)

	var cp Checkpoint
	cp.GraphID = graphID
	cp.SessionID = sessionID
	var usageJSON, resultJSON []byte
	var completed int
	err := row.Scan(
		&cp.NextNode,
		&cp.Steps,
		&cp.LastEventID,
		&usageJSON,
		&resultJSON,
		&completed,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cp.Completed = completed != 0
	if err := json.Unmarshal(usageJSON, &cp.Usage); err != nil {
		return nil, fmt.Errorf("decode checkpoint usage: %w", err)
	}
	if err := json.Unmarshal(resultJSON, &cp.Result); err != nil {
		return nil, fmt.Errorf("decode checkpoint result: %w", err)
	}
	return &cp, nil
}

func (s *SQLiteCheckpointStore) Save(ctx context.Context, checkpoint Checkpoint) error {
	usageJSON, err := json.Marshal(checkpoint.Usage)
	if err != nil {
		return err
	}
	resultJSON, err := json.Marshal(checkpoint.Result)
	if err != nil {
		return err
	}
	completed := 0
	if checkpoint.Completed {
		completed = 1
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO graph_checkpoints (
			graph_id, session_id, next_node, steps, last_event_id, usage_json, result_json, completed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(graph_id, session_id) DO UPDATE SET
			next_node = excluded.next_node,
			steps = excluded.steps,
			last_event_id = excluded.last_event_id,
			usage_json = excluded.usage_json,
			result_json = excluded.result_json,
			completed = excluded.completed
	`,
		checkpoint.GraphID,
		checkpoint.SessionID,
		checkpoint.NextNode,
		checkpoint.Steps,
		checkpoint.LastEventID,
		usageJSON,
		resultJSON,
		completed,
	)
	return err
}

func (s *SQLiteCheckpointStore) Clear(ctx context.Context, graphID, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM graph_checkpoints WHERE graph_id = ? AND session_id = ?
	`, graphID, sessionID)
	return err
}
