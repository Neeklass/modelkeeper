package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
)

// Storage statistics: how much is stored where, by whom, in which format,
// and how much is stored more than once. Counts files on disk (archives as
// one file, their contents are not counted again) that were present at the
// last scan, on active root folders.

// SizeCount is a group of files.
type SizeCount struct {
	Key   string `json:"key"` // root path, creator, category or extension
	ID    int64  `json:"id,omitempty"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

// DuplicateGroup is one file content stored in several places.
type DuplicateGroup struct {
	Hash   string `json:"hash"` // first 12 hex characters
	Path   string `json:"path"` // one of the places (relative to its root)
	Copies int64  `json:"copies"`
	Size   int64  `json:"size"`  // of one copy
	Extra  int64  `json:"extra"` // bytes beyond the first copy
}

// ModelSize is a model with the size of its files.
type ModelSize struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// Stats is the storage overview.
type Stats struct {
	Files      int64            `json:"files"`
	Bytes      int64            `json:"bytes"`
	Unhashed   int64            `json:"unhashed"` // files without a fingerprint yet (duplicates not known)
	Roots      []SizeCount      `json:"roots"`
	Creators   []SizeCount      `json:"creators"`   // the largest 25
	Categories []SizeCount      `json:"categories"` // model, slicer, archive, supplementary
	Exts       []SizeCount      `json:"exts"`       // the largest 15
	Models     []ModelSize      `json:"models"`     // the largest 20
	DupFiles   int64            `json:"dupFiles"`   // contents stored more than once
	DupExtra   int64            `json:"dupExtra"`   // bytes of the copies beyond the first
	Duplicates []DuplicateGroup `json:"duplicates"` // the 25 that take most extra space
}

// onDisk restricts locations "l" to present files on disk on active roots.
const onDisk = `l.archive_id IS NULL AND l.present = 1 AND l.root_id NOT IN ` + inactiveRoots

// GetStats computes the storage overview.
func GetStats(ctx context.Context, db *sql.DB) (Stats, error) {
	st := Stats{Roots: []SizeCount{}, Creators: []SizeCount{}, Categories: []SizeCount{}, Exts: []SizeCount{},
		Models: []ModelSize{}, Duplicates: []DuplicateGroup{}}
	if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(l.size), 0),
		coalesce(sum(l.file_id IS NULL), 0) FROM location l WHERE `+onDisk).Scan(&st.Files, &st.Bytes, &st.Unhashed); err != nil {
		return st, err
	}
	groups := []struct {
		q    string
		into *[]SizeCount
	}{
		{`SELECT r.path, r.id, count(*), coalesce(sum(l.size), 0) FROM location l JOIN root r ON r.id = l.root_id
			WHERE ` + onDisk + ` GROUP BY r.id ORDER BY 4 DESC`, &st.Roots},
		{`SELECT coalesce(c.name, ''), coalesce(c.id, 0), count(*), coalesce(sum(l.size), 0) FROM location l
			JOIN model m ON m.id = l.model_id LEFT JOIN creator c ON c.id = m.creator_id
			WHERE ` + onDisk + ` GROUP BY c.id ORDER BY 4 DESC LIMIT 25`, &st.Creators},
		{`SELECT l.category, 0, count(*), coalesce(sum(l.size), 0) FROM location l
			WHERE ` + onDisk + ` GROUP BY l.category ORDER BY 4 DESC`, &st.Categories},
		{`SELECT l.ext, 0, count(*), coalesce(sum(l.size), 0) FROM location l
			WHERE ` + onDisk + ` GROUP BY l.ext ORDER BY 4 DESC LIMIT 15`, &st.Exts},
	}
	for _, g := range groups {
		rows, err := db.QueryContext(ctx, g.q)
		if err != nil {
			return st, err
		}
		for rows.Next() {
			var sc SizeCount
			if err := rows.Scan(&sc.Key, &sc.ID, &sc.Files, &sc.Bytes); err != nil {
				rows.Close()
				return st, err
			}
			*g.into = append(*g.into, sc)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return st, err
		}
	}

	rows, err := db.QueryContext(ctx, `SELECT m.id, m.name, sum(l.size) FROM location l JOIN model m ON m.id = l.model_id
		WHERE `+onDisk+` GROUP BY m.id ORDER BY 3 DESC LIMIT 20`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var m ModelSize
		if err := rows.Scan(&m.ID, &m.Name, &m.Bytes); err != nil {
			rows.Close()
			return st, err
		}
		st.Models = append(st.Models, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	// Same content (fingerprint) in several places on disk.
	dup := `SELECT l.file_id, count(*) AS n, max(l.size) AS size, min(l.path) AS path FROM location l
		WHERE ` + onDisk + ` AND l.file_id IS NOT NULL GROUP BY l.file_id HAVING count(*) > 1`
	if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum((n - 1) * size), 0) FROM (`+dup+`)`).
		Scan(&st.DupFiles, &st.DupExtra); err != nil {
		return st, err
	}
	rows, err = db.QueryContext(ctx, `SELECT f.sha256, d.path, d.n, d.size, (d.n - 1) * d.size FROM (`+dup+`) d
		JOIN file f ON f.id = d.file_id ORDER BY 5 DESC LIMIT 25`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var g DuplicateGroup
		var sha []byte
		if err := rows.Scan(&sha, &g.Path, &g.Copies, &g.Size, &g.Extra); err != nil {
			return st, err
		}
		if h := hex.EncodeToString(sha); len(h) >= 12 {
			g.Hash = h[:12]
		}
		st.Duplicates = append(st.Duplicates, g)
	}
	return st, rows.Err()
}
