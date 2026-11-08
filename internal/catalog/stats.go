package catalog

import (
	"context"
	"database/sql"
	"encoding/json"

	"modelkeeper/internal/msg"
)

// RootStats summarizes what the catalog knows about one root.
type RootStats struct {
	Files             int   `json:"files"`   // present files on disk
	Bytes             int64 `json:"bytes"`   // their total size
	Missing           int   `json:"missing"` // gone since the last complete scan
	Unhashed          int   `json:"unhashed"`
	Archives          int   `json:"archives"`
	ArchivesBroken    int   `json:"archivesBroken"`
	ArchivesEncrypted int   `json:"archivesEncrypted"`
	ArchivesPending   int   `json:"archivesPending"`
	ArchiveMembers    int   `json:"archiveMembers"`
	DuplicateFiles    int   `json:"duplicateFiles"` // contents present at more than one location
}

// GetRootStats computes statistics for a root.
func GetRootStats(ctx context.Context, db *sql.DB, rootID int64) (RootStats, error) {
	var s RootStats
	err := db.QueryRowContext(ctx, `SELECT
		count(*) FILTER (WHERE archive_id IS NULL AND present = 1),
		coalesce(sum(size) FILTER (WHERE archive_id IS NULL AND present = 1), 0),
		count(*) FILTER (WHERE archive_id IS NULL AND present = 0),
		count(*) FILTER (WHERE archive_id IS NULL AND present = 1 AND file_id IS NULL),
		count(*) FILTER (WHERE archive_id IS NULL AND present = 1 AND category = 'archive'),
		count(*) FILTER (WHERE archive_id IS NULL AND present = 1 AND archive_status = 'broken'),
		count(*) FILTER (WHERE archive_id IS NULL AND present = 1 AND archive_status = 'encrypted'),
		count(*) FILTER (WHERE archive_id IS NULL AND present = 1 AND archive_status = 'pending'),
		count(*) FILTER (WHERE archive_id IS NOT NULL AND present = 1)
		FROM location WHERE root_id = ?`, rootID).Scan(
		&s.Files, &s.Bytes, &s.Missing, &s.Unhashed, &s.Archives,
		&s.ArchivesBroken, &s.ArchivesEncrypted, &s.ArchivesPending, &s.ArchiveMembers)
	if err != nil {
		return s, err
	}
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM (
		SELECT file_id FROM location WHERE present = 1 AND file_id IS NOT NULL
		  AND file_id IN (SELECT file_id FROM location WHERE root_id = ? AND present = 1)
		GROUP BY file_id HAVING count(*) > 1)`, rootID).Scan(&s.DuplicateFiles)
	return s, err
}

// Issue is a problem recorded by a scan.
type Issue struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"`
	Msg       msg.Msg `json:"msg"`
	CreatedAt int64   `json:"createdAt"`
}

// ListLastScanIssues returns the issues of the most recent scan of a root.
func ListLastScanIssues(ctx context.Context, db *sql.DB, rootID int64, limit int) ([]Issue, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, kind, msg_key, params, created_at FROM scan_issue
		WHERE scan_run_id = (SELECT max(id) FROM scan_run WHERE root_id = ?)
		ORDER BY id LIMIT ?`, rootID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Issue{}
	for rows.Next() {
		var is Issue
		var params sql.NullString
		if err := rows.Scan(&is.ID, &is.Kind, &is.Msg.Key, &params, &is.CreatedAt); err != nil {
			return nil, err
		}
		if params.Valid {
			json.Unmarshal([]byte(params.String), &is.Msg.Params)
		}
		out = append(out, is)
	}
	return out, rows.Err()
}
