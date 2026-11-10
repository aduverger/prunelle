package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAppliesDefaultsAndExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("data_dir = \"~/prunelle-data\"\nallowed_logins = [\"friend@example.com\"]\n[snapshot]\ndefault_base_ref = \"origin/main\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen || cfg.DataDir == "" {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.DataDir != filepath.Join(home, "prunelle-data") || cfg.Snapshot.DefaultBaseRef != "origin/main" {
		t.Fatalf("configuration = %+v", cfg)
	}
}

func TestLoadRejectsRetiredConfigAndInvalidCaptureDefaults(t *testing.T) {
	for name, input := range map[string]struct{ contents, errorHint string }{
		"retired allowed roots":    {"allowed_roots = ['/source']\n", "[snapshot]"},
		"retired roots":            {"workspace_root_paths = ['/source']\n", "[snapshot]"},
		"retired workspace table":  {"[[workspace]]\nroot_paths = ['/source']\n", "[snapshot]"},
		"retired runtime backends": {"[[session_backends]]\nname = 'rex'\nkind = 'REX'\n", "Pi extension"},
		"empty base ref":           {"[snapshot]\ndefault_base_ref = ''\n", "snapshot.default_base_ref"},
		"invalid base ref":         {"[snapshot]\ndefault_base_ref = '--upload-pack=evil'\n", "snapshot.default_base_ref"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(input.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), input.errorHint) {
				t.Fatalf("expected actionable configuration error containing %q: %v", input.errorHint, err)
			}
		})
	}
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil || cfg.Listen != DefaultListen || cfg.Snapshot.DefaultBaseRef != "main" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}

func TestLoadRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("workspace_root_paths = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}
