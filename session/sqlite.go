package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	if strings.Contains(dsn, ":memory:") {
		// SQLite in-memory databases are scoped per connection. Pinning the pool
		// to a single connection keeps tests and ephemeral stores on one logical DB.
		db.SetMaxOpenConns(1)
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

// Load reconstructs a session from the database.
func (s *SQLiteStore) Load(ctx context.Context, sessionID string) (*Session, error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT id, session_id, type, timestamp, data, metadata, cost FROM events WHERE session_id = ? ORDER BY rowid ASC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return loadSessionRows(sessionID, s, rows)
}

// LoadUntil loads a session up to (and including) the given event ID.
func (s *SQLiteStore) LoadUntil(
	ctx context.Context,
	sessionID string,
	eventID ulid.ULID,
) (*Session, error) {
	row := s.db.QueryRowContext(
		ctx,
		"SELECT rowid FROM events WHERE session_id = ? AND id = ?",
		sessionID,
		eventID.String(),
	)
	var targetRowID int64
	err := row.Scan(&targetRowID)
	var rows *sql.Rows
	switch {
	case err == nil:
		rows, err = s.db.QueryContext(
			ctx,
			"SELECT id, session_id, type, timestamp, data, metadata, cost FROM events WHERE session_id = ? AND rowid <= ? ORDER BY rowid ASC",
			sessionID,
			targetRowID,
		)
	case errors.Is(err, sql.ErrNoRows):
		rows, err = s.db.QueryContext(
			ctx,
			"SELECT id, session_id, type, timestamp, data, metadata, cost FROM events WHERE session_id = ? AND id <= ? ORDER BY rowid ASC",
			sessionID,
			eventID.String(),
		)
	default:
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return loadSessionRows(sessionID, s, rows)
}

// Fork creates a new session by copying all events from an existing session.
func (s *SQLiteStore) Fork(ctx context.Context, originalID, newID string) (*Session, error) {
	return s.ForkWithOptions(ctx, originalID, newID, ForkOptions{})
}

// ForkWithOptions creates a new session by copying all events from an existing
// session and persists session-level ancestry metadata for the child.
func (s *SQLiteStore) ForkWithOptions(
	ctx context.Context,
	originalID, newID string,
	opts ForkOptions,
) (*Session, error) {
	sess, err := s.Load(ctx, originalID)
	if err != nil {
		return nil, err
	}

	forked := sess.Fork(newID)
	parentEvents := sess.Events()
	forkPointEventID := ""
	if n := len(parentEvents); n > 0 {
		forkPointEventID = parentEvents[n-1].ID.String()
	}
	parentCreatedAt := sessionCreatedAt(sess)
	childCreatedAt := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	parentDepth, err := ensureSQLiteRootAncestryTx(ctx, tx, originalID, parentCreatedAt)
	if err != nil {
		return nil, err
	}
	if err := saveSQLiteAncestryTx(ctx, tx, SessionAncestry{
		SessionID:        newID,
		ParentSessionID:  originalID,
		ForkPointEventID: forkPointEventID,
		BranchLabel:      opts.BranchLabel,
		ForkReason:       opts.ForkReason,
		Depth:            parentDepth + 1,
		CreatedAt:        childCreatedAt,
	}); err != nil {
		return nil, err
	}
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
		 ORDER BY e.rowid ASC`,
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

func loadSessionRows(sessionID string, store *SQLiteStore, rows *sql.Rows) (*Session, error) {
	sess := New(sessionID).WithWriter(store)
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
		sess.mu.Lock()
		sess.events = append(sess.events, e)
		sess.mu.Unlock()
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sess, nil
}

// Parent returns the persisted ancestry record for the parent of sessionID.
func (s *SQLiteStore) Parent(ctx context.Context, sessionID string) (*SessionAncestry, error) {
	record, err := s.loadSQLiteAncestry(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if record.ParentSessionID == "" {
		return nil, nil
	}
	return s.loadSQLiteAncestry(ctx, record.ParentSessionID)
}

// Children lists the persisted ancestry records for direct children of sessionID.
func (s *SQLiteStore) Children(ctx context.Context, sessionID string) ([]SessionAncestry, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT session_id, parent_session_id, fork_point_event_id, branch_label, fork_reason, depth, created_at
		 FROM session_ancestry
		 WHERE parent_session_id = ?
		 ORDER BY created_at ASC, session_id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var children []SessionAncestry
	for rows.Next() {
		record, err := scanSQLiteAncestry(rows)
		if err != nil {
			return nil, err
		}
		children = append(children, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return children, nil
}

// Lineage returns the root-to-current ancestry chain for sessionID.
func (s *SQLiteStore) Lineage(ctx context.Context, sessionID string) ([]SessionAncestry, error) {
	lineage := make([]SessionAncestry, 0, 8)
	seen := make(map[string]struct{}, 8)
	current := sessionID

	for current != "" {
		record, err := s.loadSQLiteAncestry(ctx, current)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[current]; exists {
			return nil, errors.New("session ancestry cycle detected")
		}
		seen[current] = struct{}{}
		lineage = append(lineage, *record)
		current = record.ParentSessionID
	}

	reverseAncestry(lineage)
	return lineage, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func ensureRootAncestryTx(ctx context.Context, exec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, sessionID string, createdAt time.Time,
) error {
	_, err := exec.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO session_ancestry
		 (session_id, parent_session_id, fork_point_event_id, branch_label, fork_reason, depth, created_at)
		 VALUES (?, '', '', '', '', 0, ?)`,
		sessionID,
		createdAt.Format(time.RFC3339Nano),
	)
	return err
}

func ensureSQLiteRootAncestryTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionID string,
	createdAt time.Time,
) (int, error) {
	if err := ensureRootAncestryTx(ctx, tx, sessionID, createdAt); err != nil {
		return 0, err
	}
	row := tx.QueryRowContext(
		ctx,
		"SELECT depth FROM session_ancestry WHERE session_id = ?",
		sessionID,
	)
	var depth int
	if err := row.Scan(&depth); err != nil {
		return 0, err
	}
	return depth, nil
}

func saveSQLiteAncestryTx(ctx context.Context, tx *sql.Tx, record SessionAncestry) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO session_ancestry
		 (session_id, parent_session_id, fork_point_event_id, branch_label, fork_reason, depth, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO NOTHING`,
		record.SessionID,
		record.ParentSessionID,
		record.ForkPointEventID,
		record.BranchLabel,
		record.ForkReason,
		record.Depth,
		record.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) loadSQLiteAncestry(
	ctx context.Context,
	sessionID string,
) (*SessionAncestry, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT session_id, parent_session_id, fork_point_event_id, branch_label, fork_reason, depth, created_at
		 FROM session_ancestry
		 WHERE session_id = ?`,
		sessionID,
	)
	record, err := scanSQLiteAncestry(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session ancestry %q not found", sessionID)
		}
		return nil, err
	}
	return &record, nil
}

type ancestryScanner interface {
	Scan(dest ...any) error
}

func scanSQLiteAncestry(scanner ancestryScanner) (SessionAncestry, error) {
	var record SessionAncestry
	var createdAt string
	if err := scanner.Scan(
		&record.SessionID,
		&record.ParentSessionID,
		&record.ForkPointEventID,
		&record.BranchLabel,
		&record.ForkReason,
		&record.Depth,
		&createdAt,
	); err != nil {
		return SessionAncestry{}, err
	}
	ts, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return SessionAncestry{}, err
	}
	record.CreatedAt = ts
	return record, nil
}

func sessionCreatedAt(sess *Session) time.Time {
	events := sess.Events()
	if len(events) == 0 {
		return time.Now().UTC()
	}
	return events[0].Timestamp
}
