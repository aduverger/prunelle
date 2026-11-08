package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("allowed_roots = [\"~/code\", \"/srv/repos\"]\nallowed_logins = [\"friend@example.com\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen || cfg.DataDir == "" {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.AllowedRoots[0] != filepath.Join(home, "code") || cfg.AllowedRoots[1] != "/srv/repos" {
		t.Errorf("allowed roots = %v", cfg.AllowedRoots)
	}
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil || cfg.Listen != DefaultListen {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("allowed_roots = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}
