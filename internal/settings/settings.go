// Package settings loads and saves data/settings.json.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
		PartNames:  []string{"supported", "unsupported", "presupported", "pre-supported", "stl", "stls", "parts", "base", "bases", "32mm", "75mm"},
		SizeTokens: []string{"S", "M", "L", "XL", "XXL", "32mm", "75mm"},
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
