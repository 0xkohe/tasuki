package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareAndRestoreClaudeStatusLineOverride(t *testing.T) {
	workDir := t.TempDir()
	settingsDir := filepath.Join(workDir, ".claude")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(settingsDir, "settings.local.json")
	original := []byte("{\n  \"theme\": \"light\",\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"/tmp/original-statusline.sh\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0644); err != nil {
		t.Fatal(err)
	}

	override, err := prepareClaudeStatusLineOverride(workDir)
	if err != nil {
		t.Fatalf("prepareClaudeStatusLineOverride() error = %v", err)
	}

	settings, err := readJSONMap(settingsPath)
	if err != nil {
		t.Fatalf("readJSONMap() error = %v", err)
	}
	statusLine, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine missing or invalid: %#v", settings["statusLine"])
	}
	if command, _ := statusLine["command"].(string); command != shellQuote(filepath.Join(workDir, ".tasuki", "claude-statusline.sh")) {
		t.Fatalf("unexpected command: %q", command)
	}

	if err := override.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	restored, err := readJSONMap(settingsPath)
	if err != nil {
		t.Fatalf("readJSONMap() after restore error = %v", err)
	}
	statusLine, ok = restored["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("restored statusLine missing or invalid: %#v", restored["statusLine"])
	}
	if command, _ := statusLine["command"].(string); command != "/tmp/original-statusline.sh" {
		t.Fatalf("unexpected restored command: %q", command)
	}
	if theme, _ := restored["theme"].(string); theme != "light" {
		t.Fatalf("unexpected restored theme: %q", theme)
	}
}

func TestRestoreRemovesInjectedStatusLineWhenNoOriginalValue(t *testing.T) {
	workDir := t.TempDir()

	override, err := prepareClaudeStatusLineOverride(workDir)
	if err != nil {
		t.Fatalf("prepareClaudeStatusLineOverride() error = %v", err)
	}
	if err := override.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	settingsPath := filepath.Join(workDir, ".claude", "settings.local.json")
	settings, err := readJSONMap(settingsPath)
	if err != nil {
		t.Fatalf("readJSONMap() error = %v", err)
	}
	if _, ok := settings["statusLine"]; ok {
		t.Fatalf("expected injected statusLine to be removed, got %#v", settings["statusLine"])
	}
}
