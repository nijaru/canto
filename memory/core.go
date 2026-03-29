package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/session"
	_ "modernc.org/sqlite"
)

// CoreStore represents a durable store for mutable agent/session structs.
type CoreStore struct {
	db *sql.DB
}

// NewCoreStore creates a new core store backed by SQLite.
func NewCoreStore(dsn string) (*CoreStore, error) {
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
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(16)
		db.SetMaxIdleConns(4)
	}
	s := &CoreStore{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *CoreStore) init() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS episodes (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT UNIQUE NOT NULL,
			session_id TEXT NOT NULL,
			data TEXT NOT NULL,
			content TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_episodes_session_id ON episodes(session_id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS episodes_fts USING fts5(
			content,
			content='episodes',
			content_rowid='rowid',
			tokenize='trigram'
		)`,
		`CREATE TRIGGER IF NOT EXISTS episodes_ai AFTER INSERT ON episodes BEGIN
			INSERT INTO episodes_fts(rowid, content) VALUES (new.rowid, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS episodes_ad AFTER DELETE ON episodes BEGIN
			INSERT INTO episodes_fts(episodes_fts, rowid, content) VALUES('delete', old.rowid, old.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS episodes_au AFTER UPDATE ON episodes BEGIN
			INSERT INTO episodes_fts(episodes_fts, rowid, content) VALUES('delete', old.rowid, old.content);
			INSERT INTO episodes_fts(rowid, content) VALUES (new.rowid, new.content);
		END`,
		`CREATE TABLE IF NOT EXISTS memory_blocks (
			scope TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			name TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (scope, scope_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS memories (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT UNIQUE NOT NULL,
			scope TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			role TEXT NOT NULL,
			memory_key TEXT,
			content TEXT NOT NULL,
			metadata TEXT,
			observed_at TEXT,
			valid_from TEXT,
			valid_to TEXT,
			supersedes TEXT,
			superseded_by TEXT,
			forgotten_at TEXT,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_scope_role ON memories(scope, scope_id, role)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			content,
			content='memories',
			content_rowid='rowid',
			tokenize='trigram'
		)`,
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, content) VALUES (new.rowid, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.rowid, old.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, content) VALUES('delete', old.rowid, old.content);
			INSERT INTO memories_fts(rowid, content) VALUES (new.rowid, new.content);
		END`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return s.ensureMemoryColumns()
}

func (s *CoreStore) ensureMemoryColumns() error {
	required := map[string]string{
		"observed_at":   "TEXT",
		"valid_from":    "TEXT",
		"valid_to":      "TEXT",
		"supersedes":    "TEXT",
		"superseded_by": "TEXT",
		"forgotten_at":  "TEXT",
	}
	rows, err := s.db.Query("PRAGMA table_info(memories)")
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for name, dataType := range required {
		if existing[name] {
			continue
		}
		if _, err := s.db.Exec(
			"ALTER TABLE memories ADD COLUMN " + name + " " + dataType,
		); err != nil {
			return err
		}
	}
	return nil
}

// SaveEpisode persists an Episode to the store, replacing any existing episode with the same ID.
func (s *CoreStore) SaveEpisode(ctx context.Context, ep *session.Episode) error {
	data, err := json.Marshal(ep)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO episodes (id, session_id, data, content) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data, content=excluded.content
	`, ep.ID, ep.SessionID, string(data), ep.Text())
	return err
}

// LoadEpisodes returns all episodes for a session ordered by insertion time.
func (s *CoreStore) LoadEpisodes(
	ctx context.Context,
	sessionID string,
) ([]*session.Episode, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT data FROM episodes WHERE session_id = ? ORDER BY rowid ASC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEpisodes(rows)
}

// SearchEpisodes finds episodes whose content matches the FTS5 query, most recent first.
func (s *CoreStore) SearchEpisodes(
	ctx context.Context,
	query string,
	limit int,
) ([]*session.Episode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.data FROM episodes e
		JOIN episodes_fts f ON f.rowid = e.rowid
		WHERE f.content MATCH ?
		ORDER BY e.rowid DESC
		LIMIT ?
	`, escapeFTS(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEpisodes(rows)
}

func scanEpisodes(rows *sql.Rows) ([]*session.Episode, error) {
	var result []*session.Episode
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var ep session.Episode
		if err := json.Unmarshal([]byte(data), &ep); err != nil {
			return nil, fmt.Errorf("episode decode: %w", err)
		}
		result = append(result, &ep)
	}
	return result, rows.Err()
}

// Close finalizes connection pooling.
func (s *CoreStore) Close() error {
	return s.db.Close()
}

// escapeFTS wraps the query in quotes and escapes internal quotes to prevent FTS5 syntax errors
// and properly perform substring searches with the trigram tokenizer.
func escapeFTS(query string) string {
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}
