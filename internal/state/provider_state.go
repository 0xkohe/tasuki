package state

import "time"

// ProviderCooldown records when a provider was rate-limited and when it is
// expected to recover.
type ProviderCooldown struct {
	Provider    string `json:"provider"`
	Cycle       string `json:"cycle,omitempty"` // "5h" | "weekly" | "monthly" | ""
	ResetsAt    int64  `json:"resets_at,omitempty"`
	EnteredAt   int64  `json:"entered_at"`
	Reason      string `json:"reason,omitempty"`
	TriggerType string `json:"trigger_type,omitempty"`
}

// ProviderState persists provider cooldown information across sessions.
// Stored in .tasuki/provider_state.json.
type ProviderState struct {
	Cooldowns map[string]ProviderCooldown `json:"cooldowns"`
	UpdatedAt string                      `json:"updated_at,omitempty"`
}

// NewProviderState returns an empty state with an initialized map.
func NewProviderState() *ProviderState {
	return &ProviderState{Cooldowns: map[string]ProviderCooldown{}}
}

// cycleDuration returns the reset window length for a given cycle label.
// Unknown cycles fall back to 24h, chosen to be conservative so a recovered
// provider isn't picked prematurely.
func cycleDuration(cycle string) time.Duration {
	switch cycle {
	case "5h":
		return 5 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// ExpiresAt returns the time the cooldown should clear. Prefers the explicit
// ResetsAt timestamp when present, otherwise estimates from EnteredAt + cycle.
func (c ProviderCooldown) ExpiresAt(now time.Time) time.Time {
	if c.ResetsAt > 0 {
		return time.Unix(c.ResetsAt, 0)
	}
	base := c.EnteredAt
	if base <= 0 {
		base = now.Unix()
	}
	return time.Unix(base, 0).Add(cycleDuration(c.Cycle))
}

// IsAvailable reports whether the provider is out of cooldown at the given time.
func (c ProviderCooldown) IsAvailable(now time.Time) bool {
	return !now.Before(c.ExpiresAt(now))
}

// SetCooldown records a cooldown entry, overwriting any existing one for the
// same provider.
func (s *ProviderState) SetCooldown(name, cycle string, resetsAt int64, trigger, reason string) {
	if s.Cooldowns == nil {
		s.Cooldowns = map[string]ProviderCooldown{}
	}
	s.Cooldowns[name] = ProviderCooldown{
		Provider:    name,
		Cycle:       cycle,
		ResetsAt:    resetsAt,
		EnteredAt:   time.Now().UTC().Unix(),
		Reason:      reason,
		TriggerType: trigger,
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// ClearCooldown removes a provider's cooldown entry if present.
func (s *ProviderState) ClearCooldown(name string) {
	if s.Cooldowns == nil {
		return
	}
	delete(s.Cooldowns, name)
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// Prune removes cooldown entries whose window has elapsed. Returns true when
// the state was modified.
func (s *ProviderState) Prune(now time.Time) bool {
	changed := false
	for name, cd := range s.Cooldowns {
		if cd.IsAvailable(now) {
			delete(s.Cooldowns, name)
			changed = true
		}
	}
	if changed {
		s.UpdatedAt = now.UTC().Format(time.RFC3339)
	}
	return changed
}
