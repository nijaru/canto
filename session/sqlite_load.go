package session

import (
	"context"
	"database/sql"
	"errors"

	"github.com/oklog/ulid/v2"
)

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

func loadSessionRows(sessionID string, store *SQLiteStore, rows *sql.Rows) (*Session, error) {
	replayer := NewReplayer()
	sess := replayer.NewSession(sessionID).WithWriter(store)
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
		if err := replayer.Apply(sess, e); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sess, nil
}
