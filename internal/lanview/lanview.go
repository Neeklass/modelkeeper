// Package lanview is a read-only view of the catalog for other devices at
// home (a phone or tablet in the hobby room, TODO 6.3). It is off unless the
// user turns it on, answers only GET requests from private network
// addresses, and needs the access key the app shows. It shows names,
// pictures, tags, notes, collections and the print log; it never serves
// model files and cannot change anything.
package lanview

import (
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"modelkeeper/internal/catalog"
)

//go:embed index.html
var indexHTML []byte

// Server serves the view.
type Server struct {
	DB       *sql.DB
	ThumbDir string
	Key      string // access key (in the link the app shows)
	Language string // "de" or "en" for the page
}

const cookieName = "mk_key"

var thumbName = regexp.MustCompile(`^[0-9a-f-]{36}\.jpg$`)

// privateIP: loopback, RFC 1918, link-local, IPv6 unique local.
func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !privateIP(ip) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "read only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// The key comes in the link once (?k=...) and is kept in a cookie.
	if k := r.URL.Query().Get("k"); k != "" && s.keyOK(k) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: k, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
			MaxAge: 365 * 24 * 3600})
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/", http.StatusSeeOther) // the key leaves the address bar
			return
		}
	} else if c, err := r.Cookie(cookieName); err != nil || !s.keyOK(c.Value) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("modelkeeper: open the link shown in the app's settings (it contains the access key).\n" +
			"modelkeeper: öffne den Link aus den Einstellungen der App (er enthält den Zugangsschlüssel).\n"))
		return
	}
	switch p := r.URL.Path; {
	case p == "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(indexHTML)
	case p == "/api/info":
		s.json(w, map[string]any{"language": s.Language})
	case p == "/api/models":
		s.models(w, r)
	case strings.HasPrefix(p, "/api/model/"):
		s.model(w, r, strings.TrimPrefix(p, "/api/model/"))
	case p == "/api/tags":
		tags, err := catalog.ListTags(r.Context(), s.DB)
		s.result(w, tags, err)
	case p == "/api/collections":
		c, err := catalog.ListCollections(r.Context(), s.DB)
		s.result(w, c, err)
	case strings.HasPrefix(p, "/thumbs/"):
		name := strings.TrimPrefix(p, "/thumbs/")
		if !thumbName.MatchString(name) || s.ThumbDir == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, filepath.Join(s.ThumbDir, name))
	case strings.HasPrefix(p, "/photos/"):
		id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(p, "/photos/"), ".jpg"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		data, _, err := catalog.PrintPhoto(r.Context(), s.DB, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) keyOK(k string) bool {
	return s.Key != "" && subtle.ConstantTimeCompare([]byte(k), []byte(s.Key)) == 1
}

func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) result(w http.ResponseWriter, v any, err error) {
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	s.json(w, v)
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	num := func(k string) int64 {
		n, _ := strconv.ParseInt(q.Get(k), 10, 64)
		return max(n, 0)
	}
	query := catalog.ModelQuery{Text: q.Get("q"), TagID: num("tag"), CollectionID: num("collection"),
		Favorites: q.Get("fav") == "1", Sort: q.Get("sort")}
	if query.Sort != "newest" {
		query.Sort = ""
	}
	page, err := catalog.SearchModels(r.Context(), s.DB, query, int(num("offset")), 60)
	s.result(w, page, err)
}

// Detail is what the view shows of a model (no paths, no files).
type Detail struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Creator     string          `json:"creator"`
	Notes       string          `json:"notes"`
	PrintStatus string          `json:"printStatus"`
	Supports    string          `json:"supports"`
	Favorite    bool            `json:"favorite"`
	Origin      string          `json:"origin"`
	OriginURL   string          `json:"originUrl"`
	License     string          `json:"license"`
	Thumb       string          `json:"thumb"`
	Tags        []string        `json:"tags"`
	Variants    []string        `json:"variants"`
	Prints      []catalog.Print `json:"prints"`
	Collections []string        `json:"collections"`
	Files       int             `json:"files"`
}

func (s *Server) model(w http.ResponseWriter, r *http.Request, idText string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, err := catalog.GetModel(r.Context(), s.DB, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	out := Detail{ID: d.ID, Name: d.Name, Creator: d.Creator, Notes: d.Notes, PrintStatus: d.PrintStatus,
		Supports: d.Supports, Favorite: d.Favorite, Origin: d.Origin, OriginURL: d.OriginURL, License: d.License,
		Thumb: d.Thumb, Tags: d.Tags, Variants: d.Variants, Prints: d.Prints, Files: len(d.Files), Collections: []string{}}
	for _, c := range d.Collections {
		out.Collections = append(out.Collections, c.Name)
	}
	s.json(w, out)
}

// Listen starts the server on addr (e.g. ":8642"); it stops when ctx ends.
func Listen(ctx context.Context, addr string, s *Server) (net.Addr, <-chan error, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	return ln.Addr(), done, nil
}

// Addresses returns the private IPv4 addresses of this PC (for the link).
func Addresses() []string {
	var out []string
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := n.IP.To4(); ip4 != nil && ip4.IsPrivate() {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}
