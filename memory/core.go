package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

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
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS core_personas (
			session_id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		);
	`)
	return err
}

// GetPersona retrieves the persona for a session. Returns nil if not found.
func (s *CoreStore) GetPersona(ctx context.Context, sessionID string) (*Persona, error) {
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT data FROM core_personas WHERE session_id = ?", sessionID).Scan(&data)
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

// Close finalizes connection pooling.
func (s *CoreStore) Close() error {
	return s.db.Close()
}
