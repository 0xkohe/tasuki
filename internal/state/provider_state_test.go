package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCooldownIsAvailableUsesResetsAt(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cd := ProviderCooldown{Provider: "claude", ResetsAt: now.Add(-1 * time.Second).Unix()}
	if !cd.IsAvailable(now) {
		t.Fatal("cooldown past ResetsAt should be available")
	}
	cd2 := ProviderCooldown{Provider: "claude", ResetsAt: now.Add(time.Hour).Unix()}
	if cd2.IsAvailable(now) {
		t.Fatal("cooldown before ResetsAt should not be available")
	}
}

func TestCooldownFallsBackToCycleDuration(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	cd := ProviderCooldown{Provider: "claude", Cycle: "5h", EnteredAt: now.Add(-6 * time.Hour).Unix()}
	if !cd.IsAvailable(now) {
		t.Fatal("expected 5h cooldown entered 6h ago to be available")
	}
	cd2 := ProviderCooldown{Provider: "codex", Cycle: "weekly", EnteredAt: now.Add(-2 * time.Hour).Unix()}
	if cd2.IsAvailable(now) {
		t.Fatal("expected weekly cooldown entered 2h ago to still be active")
	}
}

func TestSetAndClearCooldown(t *testing.T) {
	ps := NewProviderState()
	ps.SetCooldown("claude", "5h", 0, "five_hour_96%", "rate_limited")
	if _, ok := ps.Cooldowns["claude"]; !ok {
		t.Fatal("expected claude cooldown to be set")
	}
	ps.ClearCooldown("claude")
	if _, ok := ps.Cooldowns["claude"]; ok {
		t.Fatal("expected claude cooldown to be cleared")
	}
}

func TestProviderStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Simulate .tasuki directory structure Store expects.
	store := NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ps := NewProviderState()
	ps.SetCooldown("claude", "5h", time.Now().Add(5*time.Hour).Unix(), "five_hour_96%", "rate_limited")
	if err := store.SaveProviderState(ps); err != nil {
		t.Fatalf("SaveProviderState: %v", err)
	}
	loaded, err := store.LoadProviderState()
	if err != nil {
		t.Fatalf("LoadProviderState: %v", err)
	}
	cd, ok := loaded.Cooldowns["claude"]
	if !ok {
		t.Fatal("claude cooldown not persisted")
	}
	if cd.Cycle != "5h" || cd.TriggerType != "five_hour_96%" {
		t.Fatalf("unexpected cooldown: %#v", cd)
	}

	// Missing file returns empty state, not error.
	otherStore := NewStore(filepath.Join(dir, "does-not-exist"))
	_ = otherStore.Init()
	empty, err := otherStore.LoadProviderState()
	if err != nil {
		t.Fatalf("LoadProviderState on fresh dir: %v", err)
	}
	if len(empty.Cooldowns) != 0 {
		t.Fatalf("expected empty cooldowns, got %#v", empty.Cooldowns)
	}
}

func TestPruneRemovesExpiredCooldowns(t *testing.T) {
	ps := NewProviderState()
	past := time.Now().Add(-1 * time.Hour).Unix()
	future := time.Now().Add(1 * time.Hour).Unix()
	ps.Cooldowns = map[string]ProviderCooldown{
		"claude": {Provider: "claude", ResetsAt: past},
		"codex":  {Provider: "codex", ResetsAt: future},
	}
	if !ps.Prune(time.Now()) {
		t.Fatal("expected Prune to modify state")
	}
	if _, ok := ps.Cooldowns["claude"]; ok {
		t.Fatal("expired claude cooldown should have been pruned")
	}
	if _, ok := ps.Cooldowns["codex"]; !ok {
		t.Fatal("active codex cooldown should remain")
	}
}
