package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/session"
	_ "modernc.org/sqlite"
)

// Persona represents the mutable core memory for an agent.
type Persona struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Directives  string `json:"directives"`
}

// KnowledgeItem represents an arbitrary piece of information stored in memory.
type KnowledgeItem struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitzero"`
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
		`CREATE TABLE IF NOT EXISTS knowledge (
			rowid INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT UNIQUE NOT NULL,
			session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_session_id ON knowledge(session_id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts USING fts5(
			content,
			content='knowledge',
			content_rowid='rowid',
			tokenize='trigram'
		)`,
		`CREATE TRIGGER IF NOT EXISTS knowledge_ai AFTER INSERT ON knowledge BEGIN
			INSERT INTO knowledge_fts(rowid, content) VALUES (new.rowid, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS knowledge_ad AFTER DELETE ON knowledge BEGIN
			INSERT INTO knowledge_fts(knowledge_fts, rowid, content) VALUES('delete', old.rowid, old.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS knowledge_au AFTER UPDATE ON knowledge BEGIN
			INSERT INTO knowledge_fts(knowledge_fts, rowid, content) VALUES('delete', old.rowid, old.content);
			INSERT INTO knowledge_fts(rowid, content) VALUES (new.rowid, new.content);
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

// SaveKnowledge persists a KnowledgeItem to the store.
func (s *CoreStore) SaveKnowledge(ctx context.Context, item *KnowledgeItem) error {
	metadata, err := json.Marshal(item.Metadata)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO knowledge (id, session_id, content, metadata) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET content=excluded.content, metadata=excluded.metadata
	`, item.ID, item.SessionID, item.Content, string(metadata))
	return err
}

// SearchKnowledge finds knowledge items whose content matches the FTS5 query.
func (s *CoreStore) SearchKnowledge(
	ctx context.Context,
	query string,
	limit int,
) ([]*KnowledgeItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT k.id, k.session_id, k.content, k.metadata FROM knowledge k
		JOIN knowledge_fts f ON f.rowid = k.rowid
		WHERE f.content MATCH ?
		ORDER BY k.rowid DESC
		LIMIT ?
	`, escapeFTS(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*KnowledgeItem
	for rows.Next() {
		var item KnowledgeItem
		var metadata string
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Content, &metadata); err != nil {
			return nil, err
		}
		if metadata != "" {
			if err := json.Unmarshal([]byte(metadata), &item.Metadata); err != nil {
				return nil, err
			}
		}
		result = append(result, &item)
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
