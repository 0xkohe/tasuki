package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const claudeStatusLineScript = `#!/bin/sh
python3 -c '
import json
import sys

try:
    data = json.load(sys.stdin)
except Exception:
    raise SystemExit(0)

rate_limits = data.get("rate_limits") or {}
five_hour = rate_limits.get("five_hour") or {}
seven_day = rate_limits.get("seven_day") or {}

def to_pct(value):
    if value is None:
        return "na"
    try:
        return str(int(round(float(value))))
    except Exception:
        return "na"

five = to_pct(five_hour.get("used_percentage"))
seven = to_pct(seven_day.get("used_percentage"))

if five == "na" and seven == "na":
    raise SystemExit(0)

print(f"Claude limits 5h:{five}% 7d:{seven}%")
'
`

type claudeStatusLineOverride struct {
	settingsPath       string
	command            string
	hadOriginalStatus  bool
	originalStatusLine any
}

func prepareClaudeStatusLineOverride(workDir string) (*claudeStatusLineOverride, error) {
	if workDir == "" {
		return nil, nil
	}

	scriptPath := filepath.Join(workDir, ".tasuki", "claude-statusline.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		return nil, fmt.Errorf("mkdir status line dir: %w", err)
	}
	if err := os.WriteFile(scriptPath, []byte(claudeStatusLineScript), 0755); err != nil {
		return nil, fmt.Errorf("write status line script: %w", err)
	}

	settingsPath := filepath.Join(workDir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return nil, fmt.Errorf("mkdir claude config dir: %w", err)
	}

	settings, err := readJSONMap(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("read settings.local.json: %w", err)
	}

	override := &claudeStatusLineOverride{
		settingsPath: settingsPath,
		command:      shellQuote(scriptPath),
	}
	if existing, ok := settings["statusLine"]; ok {
		override.hadOriginalStatus = true
		override.originalStatusLine = existing
	}

	settings["statusLine"] = map[string]any{
		"type":    "command",
		"command": override.command,
		"padding": 0,
	}

	if err := writeJSONMap(settingsPath, settings); err != nil {
		return nil, fmt.Errorf("write settings.local.json: %w", err)
	}

	return override, nil
}

func (o *claudeStatusLineOverride) Restore() error {
	if o == nil {
		return nil
	}

	settings, err := readJSONMap(o.settingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	if current, ok := settings["statusLine"]; ok && !isManagedStatusLine(current, o.command) {
		return nil
	}

	if o.hadOriginalStatus {
		settings["statusLine"] = o.originalStatusLine
	} else {
		delete(settings, "statusLine")
	}

	if len(settings) == 0 {
		if err := os.Remove(o.settingsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	return writeJSONMap(o.settingsPath, settings)
}

func isManagedStatusLine(v any, command string) bool {
	statusLine, ok := v.(map[string]any)
	if !ok {
		return false
	}

	typeValue, _ := statusLine["type"].(string)
	commandValue, _ := statusLine["command"].(string)
	return typeValue == "command" && commandValue == command
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func writeJSONMap(path string, data map[string]any) error {
	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0644)
}

func shellQuote(s string) string {
	return "'" + fmt.Sprintf("%s", bytes.ReplaceAll([]byte(s), []byte("'"), []byte("'\"'\"'"))) + "'"
}
