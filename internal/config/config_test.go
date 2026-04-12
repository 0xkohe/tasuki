package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderThresholdUsesProviderOverride(t *testing.T) {
	cfg := &Config{
		SwitchThreshold: 95,
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true, SwitchThreshold: 88},
			{Name: "codex", Enabled: true},
		},
	}

	if got := cfg.ProviderThreshold("claude"); got != 88 {
		t.Fatalf("ProviderThreshold(claude) = %d, want 88", got)
	}
	if got := cfg.ProviderThreshold("codex"); got != 95 {
		t.Fatalf("ProviderThreshold(codex) = %d, want 95", got)
	}
}

func TestProviderPreserveScrollbackUsesProviderOverride(t *testing.T) {
	trueValue := true
	falseValue := false
	cfg := &Config{
		PreserveScrollback: false,
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true, PreserveScrollback: &trueValue},
			{Name: "codex", Enabled: true, PreserveScrollback: &falseValue},
		},
	}

	if got := cfg.ProviderPreserveScrollback("claude"); !got {
		t.Fatal("ProviderPreserveScrollback(claude) = false, want true")
	}
	if got := cfg.ProviderPreserveScrollback("codex"); got {
		t.Fatal("ProviderPreserveScrollback(codex) = true, want false")
	}
	if got := cfg.ProviderPreserveScrollback("copilot"); got {
		t.Fatal("ProviderPreserveScrollback(copilot) = true, want false")
	}
}

func TestLoadMergesGlobalAndLocalConfig(t *testing.T) {
	root := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", globalDir)

	globalPath := filepath.Join(globalDir, "unblocked", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("switch_threshold: 90\nproviders:\n  - name: codex\n    enabled: true\n  - name: claude\n    enabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	localPath := filepath.Join(root, ".unblocked", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte("providers:\n  - name: claude\n    enabled: true\n    switch_threshold: 80\n  - name: copilot\n    enabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(root)
	if cfg.SwitchThreshold != 90 {
		t.Fatalf("SwitchThreshold = %d, want 90", cfg.SwitchThreshold)
	}
	if len(cfg.Providers) != 2 || cfg.Providers[0].Name != "claude" || cfg.Providers[1].Name != "copilot" {
		t.Fatalf("unexpected providers: %#v", cfg.Providers)
	}
	if got := cfg.ProviderThreshold("claude"); got != 80 {
		t.Fatalf("ProviderThreshold(claude) = %d, want 80", got)
	}
}
