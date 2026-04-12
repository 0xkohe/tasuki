package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProviderConfig holds per-provider settings.
type ProviderConfig struct {
	Name    string `yaml:"name"`
	Enabled bool   `yaml:"enabled"`
}

// Config is the top-level relay configuration.
type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
	WorkDir   string           `yaml:"-"` // set at runtime, not serialized
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true},
			{Name: "codex", Enabled: true},
			{Name: "copilot", Enabled: true},
		},
	}
}

// Load reads config from .unblocked/config.yaml, falling back to defaults.
func Load(root string) *Config {
	cfg := Default()
	path := filepath.Join(root, ".unblocked", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	_ = yaml.Unmarshal(data, cfg)
	cfg.WorkDir = root
	return cfg
}

// Save writes the config to .unblocked/config.yaml.
func (c *Config) Save(root string) error {
	dir := filepath.Join(root, ".unblocked")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0644)
}

// ProviderNames returns the ordered list of enabled provider names.
func (c *Config) ProviderNames() []string {
	var names []string
	for _, p := range c.Providers {
		if p.Enabled {
			names = append(names, p.Name)
		}
	}
	return names
}
