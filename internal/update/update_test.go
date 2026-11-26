package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		cur, rel string
		want     bool
	}{
		{"v0.11.0", "v0.12.0", true}, {"v0.12.0", "v0.12.0", false}, {"v0.12.1", "v0.12.0", false},
		{"v0.11.0-3-gabc1234", "v0.11.1", true}, {"v1.0", "v1.0.1", true}, {"dev-abc", "v9.0.0", false},
		{"v0.9.0", "v0.10.0", true}, {"v2.0.0", "garbage", false},
	} {
		if got := Newer(c.cur, c.rel); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.cur, c.rel, got)
		}
	}
}

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/none" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"tag_name":"v0.12.0","html_url":"https://github.com/Neeklass/modelkeeper/releases/tag/v0.12.0","published_at":"2026-11-20T10:00:00Z","body":"Notes"}`))
	}))
	defer srv.Close()
	old := URL
	defer func() { URL = old }()
	URL = srv.URL + "/latest"
	r, err := Latest(context.Background(), srv.Client())
	if err != nil || r.Version != "v0.12.0" || r.Notes != "Notes" {
		t.Errorf("%+v %v", r, err)
	}
	URL = srv.URL + "/none"
	if _, err := Latest(context.Background(), srv.Client()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("missing release: %v", err)
	}
}
