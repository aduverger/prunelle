// Package config loads the server configuration file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/aduverger/prunelle/server/internal/git"
)

const DefaultListen = "127.0.0.1:7390"

type Config struct {
	Listen          string           `toml:"listen"`
	DataDir         string           `toml:"data_dir"`
	Snapshot        Snapshot         `toml:"snapshot"`
	AllowedLogins   []string         `toml:"allowed_logins"`
	SessionBackends []SessionBackend `toml:"session_backends"`
}

type Snapshot struct {
	DefaultBaseRef string `toml:"default_base_ref"`
}

// SessionBackend configures an external session provider. Only REX is implemented.
type SessionBackend struct {
	Name string `toml:"name"`
	Kind string `toml:"kind"`
	// RexPath is the rex CLI (default "rex" on PATH).
	RexPath string `toml:"rex_path"`
	// Server is passed to rex -S; empty means the default local server.
	Server string `toml:"server"`
}

// DefaultPath returns <user config dir>/prunelle/config.toml.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "prunelle", "config.toml"), nil
}

// Load reads path. A missing file yields defaults.
func Load(path string) (Config, error) {
	var cfg Config
	metadata, err := toml.DecodeFile(path, &cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if metadata.IsDefined("allowed_roots") || metadata.IsDefined("workspace_root_paths") || metadata.IsDefined("workspace") {
		return Config{}, errors.New("workspace configuration was retired: remove root lists and use [snapshot].default_base_ref; authenticated callers can review any server-readable Git source")
	}
	if !metadata.IsDefined("snapshot", "default_base_ref") {
		cfg.Snapshot.DefaultBaseRef = "main"
	}
	if err := git.ValidateRef(cfg.Snapshot.DefaultBaseRef); err != nil {
		return Config{}, fmt.Errorf("snapshot.default_base_ref: %w", err)
	}
	home, _ := os.UserHomeDir()
	cfg.DataDir = expandHome(cfg.DataDir, home)
	if cfg.Listen == "" {
		cfg.Listen = DefaultListen
	}
	if err := validateSessionBackends(cfg.SessionBackends); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if cfg.DataDir == "" {
		if cfg.DataDir, err = defaultDataDir(); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func defaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Prunelle"), nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "prunelle"), nil
	}
	return filepath.Join(home, ".local", "share", "prunelle"), nil
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

func validateSessionBackends(backends []SessionBackend) error {
	names := map[string]bool{}
	for _, b := range backends {
		if b.Name == "" {
			return errors.New("session backend without a name")
		}
		if names[b.Name] {
			return fmt.Errorf("session backend %q is defined twice", b.Name)
		}
		names[b.Name] = true
		if b.Kind != "REX" {
			return fmt.Errorf("session backend %q: kind %q is not supported (REX)", b.Name, b.Kind)
		}
	}
	return nil
}
