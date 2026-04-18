package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/kooooohe/tasuki/internal/adapter"
	"github.com/kooooohe/tasuki/internal/config"
	"github.com/kooooohe/tasuki/internal/state"
)

// stubProvider implements adapter.Provider for select-logic tests.
type stubProvider struct{ name string }

func (s *stubProvider) Name() string                           { return s.name }
func (s *stubProvider) IsAvailable() bool                      { return true }
func (s *stubProvider) Stop() error                            { return nil }
func (s *stubProvider) SendInput(*adapter.InteractiveSession, string) error {
	return nil
}
func (s *stubProvider) Execute(context.Context, *adapter.Request) (<-chan adapter.Event, error) {
	return nil, nil
}
func (s *stubProvider) RunInteractive(context.Context, string, string) (*adapter.InteractiveSession, error) {
	return nil, nil
}

func newTestProviders() []adapter.Provider {
	return []adapter.Provider{
		&stubProvider{name: "claude"},
		&stubProvider{name: "codex"},
		&stubProvider{name: "copilot"},
	}
}

func newTestConfig() *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "claude", Enabled: true, ResetCycle: "5h"},
			{Name: "codex", Enabled: true, ResetCycle: "weekly"},
			{Name: "copilot", Enabled: true, ResetCycle: "monthly"},
		},
	}
}

func TestCycleFromRateLimitType(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"five_hour_96%", "5h"},
		{"5h", "5h"},
		{"seven_day_97%", "weekly"},
		{"weekly_98%", "weekly"},
		{"monthly_90%", "monthly"},
		{"", ""},
		{"unknown", ""},
	}
	for _, c := range cases {
		if got := cycleFromRateLimitType(c.in); got != c.out {
			t.Errorf("cycleFromRateLimitType(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}

func TestSelectStartingProviderFresh(t *testing.T) {
	cfg := newTestConfig()
	providers := newTestProviders()
	ps := state.NewProviderState()

	res := selectStartingProvider(cfg, providers, ps, time.Now(), "", "")
	if res.Index != 0 {
		t.Fatalf("expected index 0 (claude), got %d (%s)", res.Index, providers[res.Index].Name())
	}
	if res.Reason != "fresh" {
		t.Fatalf("Reason = %q, want fresh", res.Reason)
	}
}

func TestSelectStartingProviderSkipsCooldownProvider(t *testing.T) {
	cfg := newTestConfig()
	providers := newTestProviders()
	ps := state.NewProviderState()
	ps.SetCooldown("claude", "5h", time.Now().Add(5*time.Hour).Unix(), "five_hour_96%", "rate_limited")

	res := selectStartingProvider(cfg, providers, ps, time.Now(), "", "")
	if providers[res.Index].Name() != "codex" {
		t.Fatalf("expected codex, got %s", providers[res.Index].Name())
	}
}

func TestSelectStartingProviderPrunesExpiredCooldown(t *testing.T) {
	cfg := newTestConfig()
	providers := newTestProviders()
	ps := state.NewProviderState()
	ps.SetCooldown("claude", "5h", time.Now().Add(-1*time.Minute).Unix(), "five_hour_96%", "rate_limited")

	res := selectStartingProvider(cfg, providers, ps, time.Now(), "", "codex")
	if providers[res.Index].Name() != "claude" {
		t.Fatalf("expected claude (recovered), got %s", providers[res.Index].Name())
	}
	if res.Reason != "recovered" {
		t.Fatalf("Reason = %q, want recovered", res.Reason)
	}
	if _, stillCooling := ps.Cooldowns["claude"]; stillCooling {
		t.Fatal("expired cooldown should have been pruned")
	}
}

func TestSelectStartingProviderAllCooldownPicksSoonest(t *testing.T) {
	cfg := newTestConfig()
	providers := newTestProviders()
	ps := state.NewProviderState()
	now := time.Now()
	ps.Cooldowns["claude"] = state.ProviderCooldown{Provider: "claude", ResetsAt: now.Add(3 * time.Hour).Unix()}
	ps.Cooldowns["codex"] = state.ProviderCooldown{Provider: "codex", ResetsAt: now.Add(1 * time.Hour).Unix()}
	ps.Cooldowns["copilot"] = state.ProviderCooldown{Provider: "copilot", ResetsAt: now.Add(10 * 24 * time.Hour).Unix()}

	res := selectStartingProvider(cfg, providers, ps, now, "", "")
	if res.Reason != "all_cooldown" {
		t.Fatalf("Reason = %q, want all_cooldown", res.Reason)
	}
	if providers[res.Index].Name() != "codex" {
		t.Fatalf("expected codex (soonest reset), got %s", providers[res.Index].Name())
	}
}

func TestSelectStartingProviderPreferredOverridesCooldown(t *testing.T) {
	cfg := newTestConfig()
	providers := newTestProviders()
	ps := state.NewProviderState()
	ps.SetCooldown("codex", "weekly", time.Now().Add(2*24*time.Hour).Unix(), "weekly_97%", "rate_limited")

	res := selectStartingProvider(cfg, providers, ps, time.Now(), "codex", "")
	if providers[res.Index].Name() != "codex" {
		t.Fatalf("expected codex (preferred), got %s", providers[res.Index].Name())
	}
	if res.Reason != "preferred" {
		t.Fatalf("Reason = %q, want preferred", res.Reason)
	}
	if res.CooldownUntil.IsZero() {
		t.Fatal("expected CooldownUntil to be set when preferred is in cooldown")
	}
}

func TestSelectStartingProviderResumeRecovered(t *testing.T) {
	cfg := newTestConfig()
	providers := newTestProviders()
	ps := state.NewProviderState()
	// Session's CurrentProvider was codex; claude has no cooldown.
	res := selectStartingProvider(cfg, providers, ps, time.Now(), "", "codex")
	if providers[res.Index].Name() != "claude" {
		t.Fatalf("expected claude, got %s", providers[res.Index].Name())
	}
	if res.Reason != "recovered" {
		t.Fatalf("Reason = %q, want recovered", res.Reason)
	}
	if res.ReplacedActive != "codex" {
		t.Fatalf("ReplacedActive = %q, want codex", res.ReplacedActive)
	}
}

func TestSelectStartingProviderResumeStaysOnCurrentWhenTop(t *testing.T) {
	cfg := newTestConfig()
	providers := newTestProviders()
	ps := state.NewProviderState()
	res := selectStartingProvider(cfg, providers, ps, time.Now(), "", "claude")
	if providers[res.Index].Name() != "claude" {
		t.Fatalf("expected claude to remain, got %s", providers[res.Index].Name())
	}
	if res.Reason != "fresh" {
		t.Fatalf("Reason = %q, want fresh", res.Reason)
	}
}
