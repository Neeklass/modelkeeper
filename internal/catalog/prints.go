package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"modelkeeper/internal/msg"
)

// Print log (L1): when a model was printed, how it went, with which printer
// and settings. The newest entry also sets the model's print status.

// Print is one entry of the print log.
type Print struct {
	ID        int64   `json:"id"`
	ModelID   int64   `json:"modelId"`
	PrintedAt int64   `json:"printedAt"` // unix ms
	Result    string  `json:"result"`    // ok | failed
	Printer   string  `json:"printer"`
	Settings  string  `json:"settings"`
	Notes     string  `json:"notes"`
	Photos    []int64 `json:"photos"` // ids of its photos, oldest first (URL /photos/<id>.jpg)
}

func cleanPrint(p *Print) error {
	switch p.Result {
	case "ok", "failed":
	default:
		return msg.Wrap(nil, "error.invalidValue")
	}
	p.Printer, p.Settings, p.Notes = strings.TrimSpace(p.Printer), strings.TrimSpace(p.Settings), strings.TrimSpace(p.Notes)
	if utf8.RuneCountInString(p.Printer) > maxFieldLength || utf8.RuneCountInString(p.Settings) > 4*maxFieldLength ||
		utf8.RuneCountInString(p.Notes) > 20*maxFieldLength {
		return msg.Wrap(nil, "error.invalidValue")
	}
	if p.PrintedAt <= 0 {
		p.PrintedAt = time.Now().UnixMilli()
	}
	return nil
}

// ListPrints returns a model's print log, newest first.
func ListPrints(ctx context.Context, db queryer, modelID int64) ([]Print, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, model_id, printed_at, result, printer, settings, notes FROM print_log
		WHERE model_id = ? ORDER BY printed_at DESC, id DESC`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Print{}
	for rows.Next() {
		var p Print
		if err := rows.Scan(&p.ID, &p.ModelID, &p.PrintedAt, &p.Result, &p.Printer, &p.Settings, &p.Notes); err != nil {
			return nil, err
		}
		p.Photos = []int64{}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		ph, err := db.QueryContext(ctx, `SELECT id FROM print_photo WHERE print_id = ? ORDER BY id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for ph.Next() {
			var id int64
			if err := ph.Scan(&id); err != nil {
				ph.Close()
				return nil, err
			}
			out[i].Photos = append(out[i].Photos, id)
		}
		ph.Close()
		if err := ph.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AddPrint logs a print. If it is the newest print of the model, the model's
// print status follows it (printed / failed). An entry is removed with
// DeletePrint (not through undo).
func AddPrint(ctx context.Context, db *sql.DB, p Print) (Print, error) {
	if err := cleanPrint(&p); err != nil {
		return p, err
	}
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM model WHERE id = ?`, p.ModelID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return msg.Wrap(nil, "error.modelNotFound")
		}
		now := time.Now().UnixMilli()
		if err := tx.QueryRowContext(ctx, `INSERT INTO print_log (model_id, printed_at, result, printer, settings, notes, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`, p.ModelID, p.PrintedAt, p.Result, p.Printer, p.Settings, p.Notes,
			now).Scan(&p.ID); err != nil {
			return err
		}
		status := "printed"
		if p.Result == "failed" {
			status = "failed"
		}
		_, err := tx.ExecContext(ctx, `UPDATE model SET print_status = ?1, updated_at = ?2 WHERE id = ?3
			AND NOT EXISTS (SELECT 1 FROM print_log WHERE model_id = ?3 AND printed_at > ?4)`,
			status, now, p.ModelID, p.PrintedAt)
		return err
	})
	return p, err
}

// DeletePrint removes an entry of the print log (the model's print status
// stays as it is).
func DeletePrint(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM print_log WHERE id = ?`, id)
	return err
}

// MaxPhotosPerPrint bounds the photos of one print log entry.
const MaxPhotosPerPrint = 12

// AddPrintPhoto stores a photo (an already resized JPEG) with a print log
// entry and returns its id. The same photo twice is stored once.
func AddPrintPhoto(ctx context.Context, db *sql.DB, printID int64, jpg []byte, w, h int) (int64, error) {
	sum := sha256.Sum256(jpg)
	var id int64
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM print_photo WHERE print_id = ?`, printID).Scan(&n); err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM print_log WHERE id = ?`, printID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return msg.Wrap(nil, "prints.notFound")
		}
		err := tx.QueryRowContext(ctx, `SELECT id FROM print_photo WHERE print_id = ? AND sha256 = ?`, printID, sum[:]).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if n >= MaxPhotosPerPrint {
			return msg.Wrap(nil, "prints.tooManyPhotos", "max", MaxPhotosPerPrint)
		}
		return tx.QueryRowContext(ctx, `INSERT INTO print_photo (print_id, data, sha256, width, height, created_at)
			VALUES (?, ?, ?, ?, ?, ?) RETURNING id`, printID, jpg, sum[:], w, h, time.Now().UnixMilli()).Scan(&id)
	})
	return id, err
}

// PrintPhoto returns the JPEG of a photo and the model it belongs to.
func PrintPhoto(ctx context.Context, db *sql.DB, id int64) (data []byte, modelID int64, err error) {
	err = db.QueryRowContext(ctx, `SELECT p.data, l.model_id FROM print_photo p JOIN print_log l ON l.id = p.print_id
		WHERE p.id = ?`, id).Scan(&data, &modelID)
	return data, modelID, err
}

// DeletePrintPhoto removes a photo.
func DeletePrintPhoto(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM print_photo WHERE id = ?`, id)
	return err
}
