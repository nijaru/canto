package session

import (
	"context"
	"database/sql"
	"strings"
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
		e.Timestamp.Format(time.RFC3339),
		e.Data,
		metadata,
		e.Cost,
	)
	return err
}

// Load reconstructs a session from the database.
func (s *SQLiteStore) Load(ctx context.Context, sessionID string) (*Session, error) {
	return s.LoadUntil(
		ctx,
		sessionID,
		ulid.Make(),
	) // Load all events (Make() is max ULID effectively for current time)
}

// LoadUntil loads a session up to (and including) the given event ID.
func (s *SQLiteStore) LoadUntil(
	ctx context.Context,
	sessionID string,
	eventID ulid.ULID,
) (*Session, error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT id, session_id, type, timestamp, data, metadata, cost FROM events WHERE session_id = ? AND id <= ? ORDER BY id ASC",
		sessionID,
		eventID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sess := New(sessionID).WithWriter(s)
	for rows.Next() {
		var idStr, typeStr, timeStr string
		var loadedSessionID string
		var data, metadata []byte
		var cost float64
		if err := rows.Scan(&idStr, &loadedSessionID, &typeStr, &timeStr, &data, &metadata, &cost); err != nil {
			return nil, err
		}

		e, err := decodeEventRow(idStr, loadedSessionID, typeStr, timeStr, data, metadata, cost)
		if err != nil {
			return nil, err
		}
		// Internal load doesn't need write-through back to itself.
		sess.mu.Lock()
		sess.events = append(sess.events, e)
		sess.mu.Unlock()
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sess, nil
}

// Fork creates a new session by copying all events from an existing session.
func (s *SQLiteStore) Fork(ctx context.Context, originalID, newID string) (*Session, error) {
	sess, err := s.Load(ctx, originalID)
	if err != nil {
		return nil, err
	}

	forked := sess.Fork(newID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	for _, e := range forked.Events() {
		if err := s.saveTx(ctx, tx, e); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return forked, nil
}

// Search searches the event log using FTS5.
func (s *SQLiteStore) Search(ctx context.Context, sessionID string, query string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.session_id, e.type, e.timestamp, e.data, e.metadata, e.cost 
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
		var idStr, typeStr, timeStr string
		var sessionID string
		var data, metadata []byte
		var cost float64
		if err := rows.Scan(&idStr, &sessionID, &typeStr, &timeStr, &data, &metadata, &cost); err != nil {
			return nil, err
		}

		e, err := decodeEventRow(idStr, sessionID, typeStr, timeStr, data, metadata, cost)
		if err != nil {
			return nil, err
		}
		res = append(res, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
