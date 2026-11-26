package lanview_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"modelkeeper/internal/db"
	"modelkeeper/internal/lanview"
)

func TestAccess(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(conn)
	conn.Exec(`INSERT INTO model (id, uid, name, created_at, updated_at) VALUES (1, 'u', 'Orc', 0, 0)`)
	conn.Exec(`INSERT INTO model_fts (rowid, name, creator, tags, notes, paths) VALUES (1, 'Orc', '', '', '', '')`)
	s := &lanview.Server{DB: conn, Key: "secretkey", Language: "de"}
	do := func(method, target, remote, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, nil)
		r.RemoteAddr = remote
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "mk_key", Value: cookie})
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "/?k=secretkey", "8.8.8.8:1234", ""); w.Code != http.StatusForbidden {
		t.Errorf("internet address: %d", w.Code)
	}
	if w := do("GET", "/", "192.168.1.5:1234", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no key: %d", w.Code)
	}
	if w := do("GET", "/", "192.168.1.5:1234", "wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: %d", w.Code)
	}
	w := do("GET", "/?k=secretkey", "192.168.1.5:1234", "")
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Set-Cookie"), "mk_key=secretkey") {
		t.Errorf("key link: %d %v", w.Code, w.Header())
	}
	if w := do("POST", "/api/models", "192.168.1.5:1234", "secretkey"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("post: %d", w.Code)
	}
	if w := do("GET", "/", "[fd00::5]:1234", "secretkey"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "modelkeeper") {
		t.Errorf("page: %d", w.Code)
	}
	w = do("GET", "/api/models?q=orc", "127.0.0.1:1234", "secretkey")
	var page struct {
		Total int `json:"total"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 1 {
		t.Errorf("models: %d %s", w.Code, w.Body.String())
	}
	if w := do("GET", "/api/model/1", "127.0.0.1:1234", "secretkey"); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "anchors") {
		t.Errorf("model: %d %s", w.Code, w.Body.String())
	}
	if w := do("GET", "/files/1", "127.0.0.1:1234", "secretkey"); w.Code != http.StatusNotFound {
		t.Errorf("model files must not be served: %d", w.Code)
	}
	if w := do("GET", "/thumbs/../catalog.db", "127.0.0.1:1234", "secretkey"); w.Code != http.StatusNotFound {
		t.Errorf("thumbnail path: %d", w.Code)
	}
	empty := &lanview.Server{DB: conn}
	r := httptest.NewRequest("GET", "/?k=", nil)
	r.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	empty.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("server without key: %d", rec.Code)
	}
}
