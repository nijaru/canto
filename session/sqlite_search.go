package session

import "context"

// Search searches the event log using FTS5.
func (s *SQLiteStore) Search(ctx context.Context, sessionID string, query string) ([]Event, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT e.id, e.session_id, COALESCE(e.turn_id, ''), e.seq, e.type, e.timestamp, e.data, e.metadata, e.cost
		 FROM events e
		 JOIN events_fts f ON f.rowid = e.rowid
		 WHERE e.session_id = ? AND f.content MATCH ?
		 ORDER BY e.rowid ASC`,
		sessionID,
		query,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []Event
	for rows.Next() {
		var idStr, turnID, typeStr, timeStr string
		var sessionID string
		var seq int64
		var data, metadata []byte
		var cost float64
		if err := rows.Scan(
			&idStr,
			&sessionID,
			&turnID,
			&seq,
			&typeStr,
			&timeStr,
			&data,
			&metadata,
			&cost,
		); err != nil {
			return nil, err
		}

		e, err := decodeEventRow(
			idStr,
			sessionID,
			turnID,
			typeStr,
			timeStr,
			seq,
			data,
			metadata,
			cost,
		)
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
