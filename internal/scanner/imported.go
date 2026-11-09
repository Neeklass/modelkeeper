package scanner

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log"

	"modelkeeper/internal/hashshare"
)

// Hashes imported from another catalog of the same folder (hashshare). An
// entry replaces reading a file only when relative path, size and mtime are
// exactly those the walk just found; everything else is hashed as usual.
// A full check (F9) never uses them: it is meant to read every file.

type importedArchive struct {
	job hashJob
	sum [32]byte
	res archiveResult
}

// takeImported removes the jobs that an imported entry covers from files and
// archives and keeps them for storing.
func (s *scan) takeImported(ctx context.Context, files, archives []hashJob) ([]hashJob, []hashJob, error) {
	if s.full {
		return files, archives, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT path, size, mtime, sha256, archive FROM hash_import WHERE root_id = ?`, s.root.ID)
	if err != nil {
		return nil, nil, err
	}
	type entry struct {
		size, mtime int64
		sum         []byte
		archive     *string
	}
	byPath := map[string]entry{}
	for rows.Next() {
		var p string
		var e entry
		if err := rows.Scan(&p, &e.size, &e.mtime, &e.sum, &e.archive); err != nil {
			rows.Close()
			return nil, nil, err
		}
		byPath[p] = e
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(byPath) == 0 {
		return files, archives, err
	}
	match := func(j hashJob) (entry, bool) {
		e, ok := byPath[j.path]
		return e, ok && e.size == j.size && e.mtime == j.mtime && len(e.sum) == 32
	}

	restFiles := files[:0:0]
	for _, j := range files {
		e, ok := match(j)
		if !ok || e.archive != nil {
			restFiles = append(restFiles, j)
			continue
		}
		r := hashResult{job: j}
		copy(r.sum[:], e.sum)
		s.importedFiles = append(s.importedFiles, r)
	}
	restArchives := archives[:0:0]
	for _, j := range archives {
		e, ok := match(j)
		var a hashshare.Archive
		if !ok || e.archive == nil || json.Unmarshal([]byte(*e.archive), &a) != nil {
			restArchives = append(restArchives, j)
			continue
		}
		ia := importedArchive{job: j, res: archiveResult{Status: a.Status, ErrKey: a.ErrKey}}
		copy(ia.sum[:], e.sum)
		if a.Status != archiveOK {
			ia.res.Detail = "imported"
		}
		valid := true
		for _, m := range a.Members {
			am := archiveMember{Path: m.Path, Size: m.Size, Mtime: m.Mtime}
			if n, err := decodeHex(am.SHA256[:], m.SHA256); err != nil || n != 32 {
				valid = false
				break
			}
			ia.res.Members = append(ia.res.Members, am)
		}
		if !valid {
			restArchives = append(restArchives, j)
			continue
		}
		s.importedArchives = append(s.importedArchives, ia)
	}
	n := len(s.importedFiles) + len(s.importedArchives)
	s.t.update(func(p *Progress) { p.Imported = n })
	return restFiles, restArchives, nil
}

// storeImportedFiles stores the imported file hashes (in batches: nothing to lose here).
func (s *scan) storeImportedFiles() error {
	for len(s.importedFiles) > 0 {
		n := min(500, len(s.importedFiles))
		if err := s.storeHashes(context.Background(), s.importedFiles[:n]); err != nil {
			return err
		}
		s.importedFiles = s.importedFiles[n:]
	}
	return nil
}

// storeImportedArchives stores imported archive results like read ones.
func (s *scan) storeImportedArchives() error {
	for _, ia := range s.importedArchives {
		if err := s.storeArchive(context.Background(), ia.job, ia.sum, ia.res); err != nil {
			return err
		}
		switch ia.res.Status {
		case archiveBroken:
			s.issue(ia.job.id, "archive_broken", ia.res.ErrKey, "path", ia.job.path, "detail", ia.res.Detail)
			s.t.update(func(p *Progress) { p.ArchivesBroken++ })
		case archiveEncrypted:
			s.issue(ia.job.id, "archive_encrypted", ia.res.ErrKey, "path", ia.job.path)
			s.t.update(func(p *Progress) { p.ArchivesEncrypted++ })
		}
	}
	s.importedArchives = nil
	return nil
}

func decodeHex(dst []byte, h string) (int, error) {
	n, err := hex.Decode(dst, []byte(h))
	if err != nil {
		log.Printf("scan: invalid imported hash")
	}
	return n, err
}
