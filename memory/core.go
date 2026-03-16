package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nijaru/canto/session"
	_ "modernc.org/sqlite"
)

// Persona represents the mutable core memory for an agent.
type Persona struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Directives  string `json:"directives"`
}

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
	s := &CoreStore{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *CoreStore) init() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS core_personas (
			session_id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		)`,
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
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// GetPersona retrieves the persona for a session. Returns nil if not found.
func (s *CoreStore) GetPersona(ctx context.Context, sessionID string) (*Persona, error) {
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT data FROM core_personas WHERE session_id = ?", sessionID).
		Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Persona
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// SetPersona durably stores the persona for a session.
func (s *CoreStore) SetPersona(ctx context.Context, sessionID string, p *Persona) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO core_personas (session_id, data) VALUES (?, ?)
		ON CONFLICT(session_id) DO UPDATE SET data = excluded.data
	`, sessionID, string(data))
	return err
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
	`, query, limit)
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
