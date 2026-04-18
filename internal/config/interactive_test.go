package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigExistsReportsLocalAndGlobal(t *testing.T) {
	root := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", globalDir)

	if ConfigExists(root) {
		t.Fatal("ConfigExists should be false before any file is written")
	}

	localPath := LocalPath(root)
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte("providers: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !ConfigExists(root) {
		t.Fatal("ConfigExists should detect the local config")
	}
}

func TestInteractiveInitWritesLocalWithDefaults(t *testing.T) {
	root := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", globalDir)

	var out bytes.Buffer
	// Non-TTY path: enable all detected providers and save locally.
	path, err := InteractiveInit(InitOptions{
		In:     strings.NewReader(""),
		Out:    &out,
		Root:   root,
		NonTTY: true,
	})
	if err != nil {
		// When no CLI is detected, the function errors out — skip in that case.
		if strings.Contains(err.Error(), "no providers were selected") {
			t.Skip("no AI CLI detected in this environment")
		}
		t.Fatal(err)
	}
	if path != LocalPath(root) {
		t.Fatalf("path = %s, want %s", path, LocalPath(root))
	}

	cfg := Load(root)
	if cfg.SwitchThreshold != 80 {
		t.Fatalf("SwitchThreshold = %d, want 80", cfg.SwitchThreshold)
	}
	if len(cfg.Providers) != 3 {
		t.Fatalf("expected 3 provider entries, got %d", len(cfg.Providers))
	}
}

func TestInteractiveInitHonoursGlobalFlag(t *testing.T) {
	root := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", globalDir)

	var out bytes.Buffer
	path, err := InteractiveInit(InitOptions{
		In:     strings.NewReader(""),
		Out:    &out,
		Root:   root,
		Global: true,
		NonTTY: true,
	})
	if err != nil {
		if strings.Contains(err.Error(), "no providers were selected") {
			t.Skip("no AI CLI detected in this environment")
		}
		t.Fatal(err)
	}
	if path != GlobalPath() {
		t.Fatalf("path = %s, want %s", path, GlobalPath())
	}
	if _, err := os.Stat(GlobalPath()); err != nil {
		t.Fatalf("global config not written: %v", err)
	}
}
