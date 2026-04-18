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
	WarnThreshold      int    `yaml:"warn_threshold,omitempty"`
	PreserveScrollback *bool  `yaml:"preserve_scrollback,omitempty"`
	// ResetCycle is the subscription reset window for this provider.
	// Accepted values: "5h", "weekly", "monthly". Empty means unknown.
	ResetCycle string `yaml:"reset_cycle,omitempty"`
	// Priority controls provider selection order. Lower value = higher priority.
	// When nil, priority is derived from ResetCycle (5h < weekly < monthly),
	// falling back to the position in the providers array.
	Priority *int `yaml:"priority,omitempty"`
}

// Config is the top-level relay configuration.
type Config struct {
	SwitchThreshold    int  `yaml:"switch_threshold,omitempty"`
	WarnThreshold      int  `yaml:"warn_threshold,omitempty"`
	PreserveScrollback bool `yaml:"preserve_scrollback,omitempty"`
	// Yolo enables each provider's permission/sandbox bypass flag when true.
	Yolo      bool             `yaml:"yolo,omitempty"`
	Providers []ProviderConfig `yaml:"providers"`
	WorkDir   string           `yaml:"-"` // set at runtime, not serialized
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		SwitchThreshold: 95,
		WarnThreshold:   80,
		Providers: []ProviderConfig{
			{Name: "claude", Enabled: true, ResetCycle: "5h"},
			{Name: "codex", Enabled: true, ResetCycle: "weekly"},
			{Name: "copilot", Enabled: true, ResetCycle: "monthly"},
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

// SaveLocal writes the config to .tasuki/config.yaml.
func (c *Config) SaveLocal(root string) error {
	return c.saveToPath(LocalPath(root))
}

// SaveGlobal writes the config to the user's global config path.
func (c *Config) SaveGlobal() error {
	return c.saveToPath(GlobalPath())
}

// LocalPath returns the project-local config file path.
func LocalPath(root string) string {
	return filepath.Join(root, ".tasuki", "config.yaml")
}

// GlobalPath returns the global config file path.
func GlobalPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "tasuki", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".tasuki", "config.yaml")
	}
	return filepath.Join(home, ".config", "tasuki", "config.yaml")
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

// ProviderWarnThreshold returns the effective warning (pre-switch) threshold
// for the provider. It always sits strictly below the switch threshold; when
// callers haven't configured one, it's derived as switch - 15 (floor 50).
func (c *Config) ProviderWarnThreshold(name string) int {
	switchT := c.ProviderThreshold(name)

	configured := 0
	for _, p := range c.Providers {
		if p.Name == name {
			configured = p.WarnThreshold
			break
		}
	}
	if configured <= 0 {
		configured = c.WarnThreshold
	}

	if configured > 0 {
		if configured >= switchT {
			// Ignore misconfigurations that would short-circuit the switch stage.
			configured = switchT - 1
		}
		if configured < 1 {
			return 0
		}
		return configured
	}

	derived := switchT - 15
	if derived < 50 {
		derived = 50
	}
	if derived >= switchT {
		return 0
	}
	return derived
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

// ProviderResetCycle returns the configured reset cycle for the provider.
// Returns "" when not configured.
func (c *Config) ProviderResetCycle(name string) string {
	for _, p := range c.Providers {
		if p.Name == name {
			return p.ResetCycle
		}
	}
	return ""
}

// ProviderPriority returns the effective priority for a provider.
// Lower values sort first. Resolution order: explicit Priority > derived from
// ResetCycle (5h=10, weekly=50, monthly=90) > array position * 100.
func (c *Config) ProviderPriority(name string) int {
	for i, p := range c.Providers {
		if p.Name != name {
			continue
		}
		if p.Priority != nil {
			return *p.Priority
		}
		switch p.ResetCycle {
		case "5h":
			return 10
		case "weekly":
			return 50
		case "monthly":
			return 90
		}
		return (i + 1) * 100
	}
	return 1 << 30
}

func (c *Config) merge(overlay *Config) {
	if overlay == nil {
		return
	}
	if overlay.SwitchThreshold > 0 {
		c.SwitchThreshold = overlay.SwitchThreshold
	}
	if overlay.WarnThreshold > 0 {
		c.WarnThreshold = overlay.WarnThreshold
	}
	if overlay.Yolo {
		c.Yolo = true
	}
	if len(overlay.Providers) > 0 {
		c.Providers = append([]ProviderConfig(nil), overlay.Providers...)
	}
}

func (c *Config) normalize() {
	if c.SwitchThreshold <= 0 || c.SwitchThreshold > 100 {
		c.SwitchThreshold = 95
	}
	if c.WarnThreshold < 0 || c.WarnThreshold > 100 {
		c.WarnThreshold = 0
	}
	for i := range c.Providers {
		if c.Providers[i].SwitchThreshold < 0 || c.Providers[i].SwitchThreshold > 100 {
			c.Providers[i].SwitchThreshold = 0
		}
		if c.Providers[i].WarnThreshold < 0 || c.Providers[i].WarnThreshold > 100 {
			c.Providers[i].WarnThreshold = 0
		}
		switch c.Providers[i].ResetCycle {
		case "", "5h", "weekly", "monthly":
		default:
			c.Providers[i].ResetCycle = ""
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
