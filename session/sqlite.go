package session

import (
	"context"
	"database/sql"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore is a persistent store that uses SQLite for durability and FTS5 for search.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite store.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	} else if !strings.Contains(dsn, "journal_mode") {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if strings.Contains(dsn, ":memory:") {
		// SQLite in-memory databases are scoped per connection. Pinning the pool
		// to a single connection keeps tests and ephemeral stores on one logical DB.
		db.SetMaxOpenConns(1)
	} else {
		// WAL supports many readers but only 1 writer. Cap total connections
		// to prevent thread/file-descriptor exhaustion and contention.
		db.SetMaxOpenConns(16)
		db.SetMaxIdleConns(4)
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *SQLiteStore) init() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS events (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT UNIQUE,
			session_id TEXT,
			type TEXT,
			timestamp TEXT,
			data BLOB,
			metadata BLOB,
			cost REAL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_session_id ON events(session_id)`,
		`CREATE TABLE IF NOT EXISTS session_ancestry (
			session_id TEXT PRIMARY KEY,
			parent_session_id TEXT,
			fork_point_event_id TEXT,
			branch_label TEXT,
			fork_reason TEXT,
			depth INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_ancestry_parent
			ON session_ancestry(parent_session_id)`,
		// FTS5 table for searching event content
		`CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(
			content,
			content='events',
			content_rowid='rowid',
			tokenize='trigram'
		)`,
		// Triggers to keep FTS in sync
		`CREATE TRIGGER IF NOT EXISTS events_ai AFTER INSERT ON events BEGIN
			INSERT INTO events_fts(rowid, content) VALUES (new.rowid, CAST(new.data AS TEXT));
		END;`,
		`CREATE TRIGGER IF NOT EXISTS events_ad AFTER DELETE ON events BEGIN
			INSERT INTO events_fts(events_fts, rowid, content) VALUES('delete', old.rowid, CAST(old.data AS TEXT));
		END;`,
		`CREATE TRIGGER IF NOT EXISTS events_au AFTER UPDATE ON events BEGIN
			INSERT INTO events_fts(events_fts, rowid, content) VALUES('delete', old.rowid, CAST(old.data AS TEXT));
			INSERT INTO events_fts(rowid, content) VALUES (new.rowid, CAST(new.data AS TEXT));
		END;`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Save persists an event to the database.
func (s *SQLiteStore) Save(ctx context.Context, e Event) error {
	if err := validateWritableEvent(&e); err != nil {
		return err
	}
	return s.saveTx(ctx, s.db, e)
}

func (s *SQLiteStore) saveTx(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, e Event,
) error {
	metadata, err := e.encodedMetadata()
	if err != nil {
		return err
	}

	_, err = exec.ExecContext(
		ctx,
		"INSERT INTO events (id, session_id, type, timestamp, data, metadata, cost) VALUES (?, ?, ?, ?, ?, ?, ?)",
		e.ID.String(),
		e.SessionID,
		string(e.Type),
		e.Timestamp.Format(time.RFC3339Nano),
		e.Data,
		metadata,
		e.Cost,
	)
	if err != nil {
		return err
	}
	return ensureRootAncestryTx(ctx, exec, e.SessionID, e.Timestamp)
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
