package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProviderConfig holds per-provider settings.
type ProviderConfig struct {
	Name               string `yaml:"name"`
	Enabled            bool   `yaml:"enabled"`
	SwitchThreshold    int    `yaml:"switch_threshold,omitempty"`
	PreserveScrollback *bool  `yaml:"preserve_scrollback,omitempty"`
}

// Config is the top-level relay configuration.
type Config struct {
	SwitchThreshold    int              `yaml:"switch_threshold,omitempty"`
	PreserveScrollback bool             `yaml:"preserve_scrollback,omitempty"`
	Providers          []ProviderConfig `yaml:"providers"`
	WorkDir            string           `yaml:"-"` // set at runtime, not serialized
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		SwitchThreshold: 95,
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true},
			{Name: "codex", Enabled: true},
			{Name: "copilot", Enabled: true},
		},
	}
}

// Load merges default, global, and local config in that order.
func Load(root string) *Config {
	cfg := Default()
	cfg.merge(loadFromPath(GlobalPath()))
	cfg.merge(loadFromPath(LocalPath(root)))
	cfg.WorkDir = root
	cfg.normalize()
	return cfg
}

// SaveLocal writes the config to .unblocked/config.yaml.
func (c *Config) SaveLocal(root string) error {
	return c.saveToPath(LocalPath(root))
}

// SaveGlobal writes the config to the user's global config path.
func (c *Config) SaveGlobal() error {
	return c.saveToPath(GlobalPath())
}

// LocalPath returns the project-local config file path.
func LocalPath(root string) string {
	return filepath.Join(root, ".unblocked", "config.yaml")
}

// GlobalPath returns the global config file path.
func GlobalPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "unblocked", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".unblocked", "config.yaml")
	}
	return filepath.Join(home, ".config", "unblocked", "config.yaml")
}

func (c *Config) saveToPath(path string) error {
	c.normalize()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
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

// ProviderThreshold returns the effective threshold for the provider.
func (c *Config) ProviderThreshold(name string) int {
	for _, p := range c.Providers {
		if p.Name == name {
			if p.SwitchThreshold > 0 {
				return p.SwitchThreshold
			}
			break
		}
	}
	return c.SwitchThreshold
}

// ProviderPreserveScrollback returns whether inline/no-alt-screen mode should be used.
func (c *Config) ProviderPreserveScrollback(name string) bool {
	for _, p := range c.Providers {
		if p.Name == name {
			if p.PreserveScrollback != nil {
				return *p.PreserveScrollback
			}
			break
		}
	}
	return c.PreserveScrollback
}

func (c *Config) merge(overlay *Config) {
	if overlay == nil {
		return
	}
	if overlay.SwitchThreshold > 0 {
		c.SwitchThreshold = overlay.SwitchThreshold
	}
	if len(overlay.Providers) > 0 {
		c.Providers = append([]ProviderConfig(nil), overlay.Providers...)
	}
}

func (c *Config) normalize() {
	if c.SwitchThreshold <= 0 || c.SwitchThreshold > 100 {
		c.SwitchThreshold = 95
	}
	for i := range c.Providers {
		if c.Providers[i].SwitchThreshold < 0 || c.Providers[i].SwitchThreshold > 100 {
			c.Providers[i].SwitchThreshold = 0
		}
	}
}

func loadFromPath(path string) *Config {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}
