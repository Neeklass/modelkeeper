package catalog

import (
	"context"
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"

	"modelkeeper/internal/msg"
)

// Print log (L1): when a model was printed, how it went, with which printer
// and settings. The newest entry also sets the model's print status.

// Print is one entry of the print log.
type Print struct {
	ID        int64  `json:"id"`
	ModelID   int64  `json:"modelId"`
	PrintedAt int64  `json:"printedAt"` // unix ms
	Result    string `json:"result"`    // ok | failed
	Printer   string `json:"printer"`
	Settings  string `json:"settings"`
	Notes     string `json:"notes"`
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
		out = append(out, p)
	}
	return out, rows.Err()
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
