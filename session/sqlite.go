package session

import (
	"context"
	"database/sql"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite"
)

// SQLiteStore is a persistent store that uses SQLite for durability and FTS5 for search.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite store.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
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
			cost REAL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_session_id ON events(session_id)`,
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
	_, err := s.db.ExecContext(
		ctx,
		"INSERT INTO events (id, session_id, type, timestamp, data, cost) VALUES (?, ?, ?, ?, ?, ?)",
		e.ID.String(),
		e.SessionID,
		string(e.Type),
		e.Timestamp.Format(time.RFC3339),
		[]byte(e.Data),
		e.Cost,
	)
	return err
}

// Load reconstructs a session from the database.
func (s *SQLiteStore) Load(ctx context.Context, sessionID string) (*Session, error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT id, session_id, type, timestamp, data, cost FROM events WHERE session_id = ? ORDER BY id ASC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sess := New(sessionID)
	for rows.Next() {
		var e Event
		var idStr, typeStr, timeStr string
		if err := rows.Scan(&idStr, &e.SessionID, &typeStr, &timeStr, &e.Data, &e.Cost); err != nil {
			return nil, err
		}

		id, err := ulid.Parse(idStr)
		if err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, timeStr)
		if err != nil {
			return nil, err
		}
		e.ID = id
		e.Type = EventType(typeStr)
		e.Timestamp = t
		sess.Append(e)
	}

	return sess, nil
}

// Search searches the event log using FTS5.
func (s *SQLiteStore) Search(ctx context.Context, sessionID string, query string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.session_id, e.type, e.timestamp, e.data, e.cost 
		 FROM events e
		 JOIN events_fts f ON f.rowid = e.rowid
		 WHERE e.session_id = ? AND f.content MATCH ?
		 ORDER BY e.id ASC`,
		sessionID, query,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []Event
	for rows.Next() {
		var e Event
		var idStr, typeStr, timeStr string
		if err := rows.Scan(&idStr, &e.SessionID, &typeStr, &timeStr, &e.Data, &e.Cost); err != nil {
			return nil, err
		}

		id, err := ulid.Parse(idStr)
		if err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, timeStr)
		if err != nil {
			return nil, err
		}
		e.ID = id
		e.Type = EventType(typeStr)
		e.Timestamp = t
		res = append(res, e)
	}
	return res, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
