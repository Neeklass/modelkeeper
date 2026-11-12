package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"modelkeeper/internal/db"
	"modelkeeper/internal/msg"
)

func openCatalog(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(conn) })
	return conn
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// msgKey returns the message key of an error ("" for nil).
func msgKey(err error) string {
	if err == nil {
		return ""
	}
	return msg.From(err).Key
}

// A root must neither contain the data folder nor lie inside it (J1, M).
func TestAddRootRejectsDataFolder(t *testing.T) {
	conn := openCatalog(t)
	ctx := context.Background()
	base := t.TempDir()
	data := mkdir(t, filepath.Join(base, "modelkeeper", "data"))
	mkdir(t, filepath.Join(data, "thumbnails"))

	for _, p := range []string{base, filepath.Join(base, "modelkeeper"), data, filepath.Join(data, "thumbnails")} {
		if _, err := AddRoot(ctx, conn, p, data); msgKey(err) != "error.rootContainsData" {
			t.Errorf("AddRoot(%s): %v, want error.rootContainsData", p, err)
		}
	}
	other := mkdir(t, filepath.Join(base, "models"))
	if _, err := AddRoot(ctx, conn, other, data); err != nil {
		t.Errorf("AddRoot(%s): %v", other, err)
	}
}
