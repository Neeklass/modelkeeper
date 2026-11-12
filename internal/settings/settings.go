// Package settings loads and saves data/settings.json.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileTypes groups recognized file extensions (lowercase, without dot) by category (D2).
type FileTypes struct {
	Model         []string `json:"model"`
	Slicer        []string `json:"slicer"`
	Archive       []string `json:"archive"`
	Supplementary []string `json:"supplementary"`
}

// Settings is the content of settings.json.
type Settings struct {
	Version int `json:"version"`
	// Language is a language code ("de", "en") or "" to follow the system locale (N2).
	Language  string    `json:"language"`
	FileTypes FileTypes `json:"fileTypes"`
	// PartNames are folder names that are merged into the model above (F4c, F10).
	PartNames []string `json:"partNames"`
	// SizeTokens are prefixes/suffixes that mark size variants (F4f, F10).
	SizeTokens []string `json:"sizeTokens"`
	// NASRescanHours is the interval of scheduled incremental rescans of NAS
	// roots in hours; 0 disables them (E4).
	NASRescanHours int `json:"nasRescanHours"`
}

// Defaults returns the built-in settings.
func Defaults() Settings {
	return Settings{
		Version:  1,
		Language: "",
		FileTypes: FileTypes{
			Model:         []string{"stl", "obj", "mtl", "3mf", "ply", "amf", "step", "stp", "fbx", "blend", "ztl"},
			Slicer:        []string{"chitubox", "ctb", "cbddlp", "lys", "lyt", "pwmx", "pwma", "pws", "goo", "sl1", "photon"},
			Archive:       []string{"zip", "rar", "7z"},
			Supplementary: []string{"jpg", "jpeg", "png", "webp", "pdf", "txt", "md"},
		},
		PartNames:      []string{"supported", "unsupported", "presupported", "pre-supported", "stl", "stls", "parts", "base", "bases", "32mm", "75mm"},
		SizeTokens:     []string{"S", "M", "L", "XL", "XXL", "32mm", "75mm"},
		NASRescanHours: 24,
	}
}

// Store holds the current settings and persists them.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// Load reads settings from path. A missing file yields defaults (and is written).
func Load(path string) (*Store, error) {
	s := &Store{path: path, cur: Defaults()}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	// Unmarshal over defaults so newly added fields get default values.
	if err := json.Unmarshal(b, &s.cur); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns a copy of the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Update applies fn to a copy of the settings and saves the result.
func (s *Store) Update(fn func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur
	fn(&next)
	prev := s.cur
	s.cur = next
	if err := s.save(); err != nil {
		s.cur = prev
		return err
	}
	return nil
}

// save writes atomically (temp file + rename). Caller holds the lock (or owns s).
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".settings-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// ExtCategory returns the category of an extension ("model", "slicer",
// "archive", "supplementary") or "" if it is not recognized.
func (st Settings) ExtCategory(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	for cat, list := range map[string][]string{
		"model": st.FileTypes.Model, "slicer": st.FileTypes.Slicer,
		"archive": st.FileTypes.Archive, "supplementary": st.FileTypes.Supplementary,
	} {
		for _, e := range list {
			if e == ext {
				return cat
			}
		}
	}
	return ""
}

// ValidationError names a setting that cannot be saved. Key is a message key
// for the frontend (N4); Value is the offending entry.
type ValidationError struct {
	Key   string
	Value string
}

func (e *ValidationError) Error() string { return e.Key + ": " + e.Value }

// maxListLen bounds the editable lists.
const maxListLen = 200

// NormalizeFileTypes cleans user input for the file types (F10): lowercase,
// without leading dot, no duplicates. An extension may belong to one category
// only, and at least one 3D model type must remain.
func NormalizeFileTypes(ft FileTypes) (FileTypes, error) {
	seen := map[string]string{}
	clean := func(cat string, list []string) ([]string, error) {
		out := []string{}
		for _, e := range list {
			e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
			if e == "" {
				continue
			}
			if len(e) > 16 || strings.ContainsAny(e, `./\ *?"<>|:`) {
				return nil, &ValidationError{Key: "settings.error.invalidExt", Value: e}
			}
			if prev, ok := seen[e]; ok {
				if prev == cat {
					continue
				}
				return nil, &ValidationError{Key: "settings.error.extTwice", Value: e}
			}
			seen[e] = cat
			out = append(out, e)
		}
		if len(out) > maxListLen {
			return nil, &ValidationError{Key: "settings.error.tooMany", Value: cat}
		}
		return out, nil
	}
	var res FileTypes
	var err error
	if res.Model, err = clean("model", ft.Model); err != nil {
		return res, err
	}
	if res.Slicer, err = clean("slicer", ft.Slicer); err != nil {
		return res, err
	}
	if res.Archive, err = clean("archive", ft.Archive); err != nil {
		return res, err
	}
	if res.Supplementary, err = clean("supplementary", ft.Supplementary); err != nil {
		return res, err
	}
	if len(res.Model) == 0 {
		return res, &ValidationError{Key: "settings.error.noModelTypes"}
	}
	return res, nil
}

// NormalizeList cleans a list of names (part names, size tokens): trimmed,
// no empty entries, no case-insensitive duplicates.
func NormalizeList(list []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range list {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		if len([]rune(s)) > 40 {
			return nil, &ValidationError{Key: "settings.error.tooLong", Value: s}
		}
		seen[k] = true
		out = append(out, s)
	}
	if len(out) > maxListLen {
		return nil, &ValidationError{Key: "settings.error.tooMany"}
	}
	return out, nil
}

// categories maps each extension to its category.
func (ft FileTypes) categories() map[string]string {
	out := map[string]string{}
	for cat, l := range map[string][]string{"model": ft.Model, "slicer": ft.Slicer, "archive": ft.Archive, "supplementary": ft.Supplementary} {
		for _, e := range l {
			out[e] = cat
		}
	}
	return out
}

// Changed returns the extensions of prev that ft removes or puts into
// another category: changes that can drop catalog entries or detected
// models (and so call for a snapshot first, J2).
func (ft FileTypes) Changed(prev FileTypes) []string {
	next := ft.categories()
	var out []string
	for e, cat := range prev.categories() {
		if next[e] != cat {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// Added returns the extensions in next that are not in prev.
func (ft FileTypes) Added(prev FileTypes) []string {
	old := map[string]bool{}
	for _, l := range [][]string{prev.Model, prev.Slicer, prev.Archive, prev.Supplementary} {
		for _, e := range l {
			old[e] = true
		}
	}
	var out []string
	for _, l := range [][]string{ft.Model, ft.Slicer, ft.Archive, ft.Supplementary} {
		for _, e := range l {
			if !old[e] {
				out = append(out, e)
			}
		}
	}
	return out
}
