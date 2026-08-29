// Package settings persists bodek's own front-end preferences so they
// survive relaunches: theme, mouse, bell, notify, plain. odek's server-side
// configuration is unaffected — this file belongs to the terminal UI alone.
//
// Resolution order everywhere: explicit flag > BODEK_THEME env (theme) >
// this file > built-in default. Save rewrites the whole file; fields are
// pointers so "unset" survives a round trip and only what the user chose
// is ever written.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings is the persisted front-end preference set. Booleans are pointers:
// nil means "never chosen" and is skipped on save / treated as default on
// load; a value is an explicit user choice that flags may still override.
type Settings struct {
	Theme  string `json:"theme,omitempty"`
	Mouse  *bool  `json:"mouse,omitempty"`
	Bell   *bool  `json:"bel,omitempty"`
	Notify *bool  `json:"notify,omitempty"`
	Plain  *bool  `json:"plain,omitempty"`
}

// Path returns the settings file location: $BODEK_CONFIG if set, else
// ~/.bodek/config.json (mirroring odek's ~/.odek/config.json convention).
func Path() string {
	if p := os.Getenv("BODEK_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "" // no home dir: persistence is unavailable, Load stays empty
	}
	return filepath.Join(home, ".bodek", "config.json")
}

// Load reads the settings file. A missing file is not an error — it simply
// yields the zero Settings (everything defaulted). A malformed file IS an
// error so the caller can surface it instead of silently dropping choices.
func Load() (Settings, error) {
	var s Settings
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	return s, nil
}

// Save writes the settings file, creating ~/.bodek when needed. The write
// is a full replace (no merge) — callers load-modify-save to keep unset
// fields intact.
func Save(s Settings) error {
	path := Path()
	if path == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Bool resolves an optional persisted boolean against its built-in default.
func (s Settings) Bool(v *bool, def bool) bool {
	if v != nil {
		return *v
	}
	return def
}
