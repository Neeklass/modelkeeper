package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// A configuration file (TODO 2.3, M): the settings and the list of root
// folders as readable JSON, to set up another PC or a fresh installation the
// same way, or to keep the configuration apart from the catalog. The cataloging
// work itself (tags, notes …) is in the catalog export (package userexport).

// ConfigFormat identifies a configuration file.
const (
	ConfigFormat        = "modelkeeper-config"
	ConfigFormatVersion = 1
)

// Config is the content of a configuration file.
type Config struct {
	Format        string       `json:"format"`
	FormatVersion int          `json:"formatVersion"`
	AppVersion    string       `json:"appVersion,omitempty"`
	ExportedAt    string       `json:"exportedAt,omitempty"`
	Settings      Settings     `json:"settings"`
	Roots         []ConfigRoot `json:"roots"`
}

// ConfigRoot is a root folder in a configuration file.
type ConfigRoot struct {
	Path         string `json:"path"`
	Kind         string `json:"kind,omitempty"`
	CreatorLevel bool   `json:"creatorLevel"`
}

// NewConfig builds the configuration of s and roots (the PC-specific
// DataDir is left out).
func NewConfig(s Settings, roots []ConfigRoot, appVersion string, now time.Time) Config {
	s.DataDir, s.SyncID = "", "" // specific to this installation
	s.LANKey = ""                // a key stays on its PC
	if roots == nil {
		roots = []ConfigRoot{}
	}
	return Config{Format: ConfigFormat, FormatVersion: ConfigFormatVersion, AppVersion: appVersion,
		ExportedAt: now.Format(time.RFC3339), Settings: s, Roots: roots}
}

// WriteConfigFile writes c atomically, indented.
func WriteConfigFile(file string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".config-*.json")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), file)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}

// ErrNotAConfig: the file is no modelkeeper configuration.
var ErrNotAConfig = errors.New("not a modelkeeper configuration")

// ReadConfigFile reads and checks a configuration file. Lists are
// normalized; invalid values are rejected before anything is applied. Missing
// settings get the built-in defaults.
func ReadConfigFile(file string) (*Config, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeConfig(io.LimitReader(f, 16<<20))
}

// DecodeConfig parses and checks a configuration.
func DecodeConfig(r io.Reader) (*Config, error) {
	c := Config{Settings: Defaults()}
	if err := json.NewDecoder(r).Decode(&c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAConfig, err)
	}
	if c.Format != ConfigFormat || c.FormatVersion < 1 {
		return nil, ErrNotAConfig
	}
	if c.FormatVersion > ConfigFormatVersion {
		return nil, fmt.Errorf("%w: from a newer modelkeeper", ErrNotAConfig)
	}
	s := &c.Settings
	var err error
	if s.FileTypes, err = NormalizeFileTypes(s.FileTypes); err != nil {
		return nil, fmt.Errorf("%w: file types: %v", ErrNotAConfig, err)
	}
	if s.PartNames, err = NormalizeList(s.PartNames); err != nil {
		return nil, fmt.Errorf("%w: part names: %v", ErrNotAConfig, err)
	}
	if s.SizeTokens, err = NormalizeList(s.SizeTokens); err != nil {
		return nil, fmt.Errorf("%w: size tokens: %v", ErrNotAConfig, err)
	}
	switch s.Language {
	case "", "de", "en":
	default:
		s.Language = ""
	}
	if s.NASRescanHours < 0 || s.NASRescanHours > 24*365 {
		s.NASRescanHours = Defaults().NASRescanHours
	}
	s.DataDir, s.SyncID = "", ""
	s.LANKey = ""
	var roots []ConfigRoot
	for _, r := range c.Roots {
		if r.Path != "" {
			roots = append(roots, r)
		}
	}
	if roots == nil {
		roots = []ConfigRoot{}
	}
	c.Roots = roots
	return &c, nil
}
