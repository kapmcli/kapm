package monitor

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// LoadSessionsSQLite reads sessions from a v1 SQLite database.
// Returns empty slice (no error) if dbPath is empty or the file does not exist.
func LoadSessionsSQLite(ctx context.Context, dbPath string, since time.Time, cwdFilter string) ([]ParsedSession, error) {
	if dbPath == "" {
		return []ParsedSession{}, nil
	}
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return []ParsedSession{}, nil
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_busy_timeout=1000")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	const query = `
SELECT key, conversation_id, value, created_at, updated_at
FROM conversations_v2
WHERE updated_at >= ?
  AND (? = '' OR key = ? OR key LIKE ? || '/%')
ORDER BY updated_at DESC`

	sinceMS := since.UnixMilli()
	rows, err := db.QueryContext(ctx, query, sinceMS, cwdFilter, cwdFilter, cwdFilter)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []ParsedSession
	for rows.Next() {
		var row v1Row
		if err := rows.Scan(&row.Key, &row.ConversationID, &row.Value, &row.CreatedAt, &row.UpdatedAt); err != nil {
			slog.Warn("v1 sqlite scan error", "err", err)
			continue
		}
		ps, err := convertV1Session(row)
		if err != nil {
			slog.Warn("v1 sqlite convert error", "conversation_id", row.ConversationID, "err", err)
			continue
		}
		sessions = append(sessions, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if sessions == nil {
		sessions = []ParsedSession{}
	}
	return sessions, nil
}
