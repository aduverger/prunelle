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
	if err := os.WriteFile(path, []byte("workspace_root_paths = [\"~/code\", \"/srv/repos\"]\nallowed_logins = [\"friend@example.com\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen || cfg.DataDir == "" {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.WorkspaceRootPaths[0] != filepath.Join(home, "code") || cfg.WorkspaceRootPaths[1] != "/srv/repos" {
		t.Errorf("allowed roots = %v", cfg.WorkspaceRootPaths)
	}
}

func TestLoadRejectsObsoleteAllowedRoots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("allowed_roots = [\"~/code\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "workspace_root_paths") {
		t.Fatalf("obsolete configuration must fail with migration guidance: %v", err)
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
	if err := os.WriteFile(path, []byte("workspace_root_paths = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadSessionBackends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[[session_backends]]\nname = \"rex\"\nkind = \"REX\"\nserver = \"https://rex.example.ts.net\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SessionBackends) != 1 || cfg.SessionBackends[0].Server != "https://rex.example.ts.net" {
		t.Errorf("backends = %+v", cfg.SessionBackends)
	}

	for _, bad := range []string{
		"[[session_backends]]\nname = \"h\"\nkind = \"HERDR\"\n",
		"[[session_backends]]\nname = \"r\"\nkind = \"REX\"\n[[session_backends]]\nname = \"r\"\nkind = \"REX\"\n",
	} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
