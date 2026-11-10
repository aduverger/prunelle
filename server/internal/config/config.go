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
	Listen        string   `toml:"listen"`
	DataDir       string   `toml:"data_dir"`
	Snapshot      Snapshot `toml:"snapshot"`
	AllowedLogins []string `toml:"allowed_logins"`
}

type Snapshot struct {
	DefaultBaseRef string `toml:"default_base_ref"`
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
	if metadata.IsDefined("session_backends") {
		return Config{}, errors.New("runtime backends were retired: remove [[session_backends]]; install the Prunelle Pi extension to connect the existing conversation")
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
