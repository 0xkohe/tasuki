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

func TestProviderWarnThresholdDerivedWhenUnset(t *testing.T) {
	cfg := &Config{
		SwitchThreshold: 95,
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true},
		},
	}
	if got := cfg.ProviderWarnThreshold("claude"); got != 80 {
		t.Fatalf("ProviderWarnThreshold(claude) = %d, want 80 (95-15)", got)
	}
}

func TestProviderWarnThresholdProviderOverride(t *testing.T) {
	cfg := &Config{
		SwitchThreshold: 95,
		WarnThreshold:   70,
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true, WarnThreshold: 60},
			{Name: "codex", Enabled: true},
		},
	}
	if got := cfg.ProviderWarnThreshold("claude"); got != 60 {
		t.Fatalf("ProviderWarnThreshold(claude) = %d, want 60", got)
	}
	if got := cfg.ProviderWarnThreshold("codex"); got != 70 {
		t.Fatalf("ProviderWarnThreshold(codex) = %d, want 70 (top-level)", got)
	}
}

func TestProviderWarnThresholdClampedBelowSwitch(t *testing.T) {
	cfg := &Config{
		SwitchThreshold: 95,
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true, WarnThreshold: 95},
		},
	}
	// warn must not equal or exceed switch — clamped to switch-1.
	if got := cfg.ProviderWarnThreshold("claude"); got != 94 {
		t.Fatalf("ProviderWarnThreshold(claude) = %d, want 94", got)
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

func TestProviderPriorityDerivesFromResetCycle(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true, ResetCycle: "5h"},
			{Name: "codex", Enabled: true, ResetCycle: "weekly"},
			{Name: "copilot", Enabled: true, ResetCycle: "monthly"},
		},
	}

	if cfg.ProviderPriority("claude") >= cfg.ProviderPriority("codex") {
		t.Fatalf("claude priority should be higher (smaller) than codex: %d vs %d",
			cfg.ProviderPriority("claude"), cfg.ProviderPriority("codex"))
	}
	if cfg.ProviderPriority("codex") >= cfg.ProviderPriority("copilot") {
		t.Fatalf("codex priority should be higher than copilot: %d vs %d",
			cfg.ProviderPriority("codex"), cfg.ProviderPriority("copilot"))
	}
}

func TestProviderPriorityExplicitWins(t *testing.T) {
	seven := 7
	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true, ResetCycle: "5h"},
			{Name: "codex", Enabled: true, ResetCycle: "weekly", Priority: &seven},
		},
	}

	if got := cfg.ProviderPriority("codex"); got != 7 {
		t.Fatalf("ProviderPriority(codex) = %d, want 7 (explicit)", got)
	}
	// codex explicit 7 should now beat claude's derived 10.
	if cfg.ProviderPriority("codex") >= cfg.ProviderPriority("claude") {
		t.Fatalf("explicit codex priority should beat derived claude: %d vs %d",
			cfg.ProviderPriority("codex"), cfg.ProviderPriority("claude"))
	}
}

func TestProviderPriorityFallsBackToArrayPosition(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "a", Enabled: true},
			{Name: "b", Enabled: true},
			{Name: "c", Enabled: true},
		},
	}

	if cfg.ProviderPriority("a") >= cfg.ProviderPriority("b") {
		t.Fatalf("a should outrank b: %d vs %d", cfg.ProviderPriority("a"), cfg.ProviderPriority("b"))
	}
	if cfg.ProviderPriority("b") >= cfg.ProviderPriority("c") {
		t.Fatalf("b should outrank c: %d vs %d", cfg.ProviderPriority("b"), cfg.ProviderPriority("c"))
	}
}

func TestLoadPropagatesYoloFlag(t *testing.T) {
	root := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", globalDir)

	localPath := filepath.Join(root, ".tasuki", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte("yolo: true\nproviders:\n  - name: claude\n    enabled: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(root)
	if !cfg.Yolo {
		t.Fatal("Load should propagate yolo: true from local config")
	}
}

func TestLoadMergesGlobalAndLocalConfig(t *testing.T) {
	root := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", globalDir)

	globalPath := filepath.Join(globalDir, "tasuki", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("switch_threshold: 90\nproviders:\n  - name: codex\n    enabled: true\n  - name: claude\n    enabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	localPath := filepath.Join(root, ".tasuki", "config.yaml")
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
