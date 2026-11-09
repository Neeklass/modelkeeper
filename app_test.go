package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"modelkeeper/internal/appdir"
)

func TestThumbHandler(t *testing.T) {
	layout := appdir.NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	const name = "0190a5b2-7c4e-7d3a-9f00-123456789abc.jpg"
	os.WriteFile(filepath.Join(layout.Thumbnails, name), []byte("jpegdata"), 0o644)
	os.WriteFile(filepath.Join(layout.Root, "catalog.db"), []byte("secret"), 0o644)
	h := assetHandler{&App{layout: layout}}

	for _, c := range []struct {
		path string
		code int
	}{
		{"/thumbs/" + name + "?v=5", http.StatusOK},
		{"/thumbs/0190a5b2-7c4e-7d3a-9f00-000000000000.jpg", http.StatusNotFound},
		{"/thumbs/../catalog.db", http.StatusNotFound},
		{"/thumbs/..%5Ccatalog.db", http.StatusNotFound},
		{"/other", http.StatusNotFound},
		{"/files/abc", http.StatusNotFound},
		{"/files/1", http.StatusNotFound}, // no catalog open
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.code {
			t.Errorf("%s: code %d, want %d", c.path, rec.Code, c.code)
		}
		if c.code == http.StatusOK && rec.Body.String() != "jpegdata" {
			t.Errorf("%s: body %q", c.path, rec.Body.String())
		}
	}
}
