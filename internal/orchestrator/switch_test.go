package orchestrator

import (
	"testing"
	"time"

	"github.com/kooooohe/tasuki/internal/config"
	"github.com/kooooohe/tasuki/internal/state"
)

func TestPreviewSelectionAfterCooldownSkipsCurrentRateLimitedProvider(t *testing.T) {
	orch := &Orchestrator{
		cfg:       newTestConfig(),
		providers: newTestProviders(),
		current:   0,
	}

	res := orch.previewSelectionAfterCooldown("claude", "5h", time.Now().Add(5*time.Hour).Unix(), "five_hour_91%", "rate_limited")
	if res.Index != 1 {
		t.Fatalf("previewSelectionAfterCooldown() picked index %d, want 1", res.Index)
	}
	if got := orch.providers[res.Index].Name(); got != "codex" {
		t.Fatalf("previewSelectionAfterCooldown() picked %q, want codex", got)
	}
}

func TestSwitchProviderWithContextInfoFailsOverAfterRateLimit(t *testing.T) {
	workDir := t.TempDir()
	store := state.NewStore(workDir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	orch := &Orchestrator{
		cfg:           newTestConfig(),
		providers:     newTestProviders(),
		current:       0,
		store:         store,
		workDir:       workDir,
		providerState: state.NewProviderState(),
		session:       state.NewSession("sess-1", "resume this task", "claude"),
	}

	err := orch.switchProviderWithContextInfo(
		"rate_limited",
		"five_hour_91%",
		"claude limits 5h:91% 7d:14%",
		nil,
	)
	if err != nil {
		t.Fatalf("switchProviderWithContextInfo() error = %v", err)
	}
	if got := orch.currentProvider().Name(); got != "codex" {
		t.Fatalf("current provider = %q, want codex", got)
	}
	if orch.session.CurrentProvider != "codex" {
		t.Fatalf("session current provider = %q, want codex", orch.session.CurrentProvider)
	}
	cd, ok := orch.providerState.Cooldowns["claude"]
	if !ok {
		t.Fatal("expected claude cooldown to be recorded")
	}
	if cd.Cycle != "5h" {
		t.Fatalf("claude cooldown cycle = %q, want 5h", cd.Cycle)
	}
}

func TestCloneProviderStateCopiesCooldownMap(t *testing.T) {
	src := state.NewProviderState()
	src.SetCooldown("claude", "5h", time.Now().Add(time.Hour).Unix(), "five_hour_91%", "rate_limited")

	cloned := cloneProviderState(src)
	cloned.SetCooldown("codex", "weekly", time.Now().Add(24*time.Hour).Unix(), "weekly_80%", "rate_limited")

	if _, ok := src.Cooldowns["codex"]; ok {
		t.Fatal("cloneProviderState() mutated the original cooldown map")
	}
}

func TestPreviewSelectionAfterCooldownRespectsExistingCooldowns(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "claude", Enabled: true, ResetCycle: "5h"},
			{Name: "codex", Enabled: true, ResetCycle: "weekly"},
			{Name: "copilot", Enabled: true, ResetCycle: "monthly"},
		},
	}
	ps := state.NewProviderState()
	ps.SetCooldown("codex", "weekly", time.Now().Add(24*time.Hour).Unix(), "weekly_80%", "rate_limited")

	orch := &Orchestrator{
		cfg:           cfg,
		providers:     newTestProviders(),
		current:       0,
		providerState: ps,
	}

	res := orch.previewSelectionAfterCooldown("claude", "5h", time.Now().Add(5*time.Hour).Unix(), "five_hour_91%", "rate_limited")
	if got := orch.providers[res.Index].Name(); got != "copilot" {
		t.Fatalf("previewSelectionAfterCooldown() picked %q, want copilot", got)
	}
}
