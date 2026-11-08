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
)

const DefaultListen = "127.0.0.1:7390"

type Config struct {
	Listen        string   `toml:"listen"`
	DataDir       string   `toml:"data_dir"`
	AllowedRoots  []string `toml:"allowed_roots"`
	AllowedLogins []string `toml:"allowed_logins"`
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
	_, err := toml.DecodeFile(path, &cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	home, _ := os.UserHomeDir()
	cfg.DataDir = expandHome(cfg.DataDir, home)
	for i, root := range cfg.AllowedRoots {
		cfg.AllowedRoots[i] = expandHome(root, home)
	}
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
