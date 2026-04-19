package orchestrator

import (
	"testing"
	"time"

	"github.com/0xkohe/tasuki/internal/config"
	"github.com/0xkohe/tasuki/internal/state"
)

func TestEnsureSessionDoesNotResumeByDefault(t *testing.T) {
	workDir := t.TempDir()
	cfg := config.Default()

	orch, err := New(cfg, workDir, false, "", false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	old := state.NewSession("old-session", "old goal", "claude")
	old.Goal = "old goal"
	if err := orch.store.SaveSession(old); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	orch.ensureSession("new goal")
	if orch.session == nil {
		t.Fatal("expected session to be created")
	}
	if orch.session.Goal != "new goal" {
		t.Fatalf("session goal = %q, want %q", orch.session.Goal, "new goal")
	}
}

func TestEnsureSessionResumesWhenEnabled(t *testing.T) {
	workDir := t.TempDir()
	cfg := config.Default()

	orch, err := New(cfg, workDir, true, "", false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	old := state.NewSession("old-session", "old goal", "claude")
	old.Goal = "old goal"
	if err := orch.store.SaveSession(old); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	orch.ensureSession("new goal")
	if orch.session == nil {
		t.Fatal("expected session to be loaded")
	}
	if orch.session.Goal != "old goal" {
		t.Fatalf("session goal = %q, want %q", orch.session.Goal, "old goal")
	}
}

func TestNewIgnoresPreviousCurrentProviderOnStartup(t *testing.T) {
	workDir := t.TempDir()
	store := state.NewStore(workDir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	old := state.NewSession("old-session", "old goal", "copilot")
	if err := store.SaveSession(old); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	orch, err := New(config.Default(), workDir, true, "", false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if got := orch.currentProvider().Name(); got != "claude" {
		t.Fatalf("current provider = %q, want claude", got)
	}
}

func TestNewUsesPersistedCooldownsByDefault(t *testing.T) {
	workDir := t.TempDir()
	store := state.NewStore(workDir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	old := state.NewSession("old-session", "old goal", "copilot")
	if err := store.SaveSession(old); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}

	ps := state.NewProviderState()
	ps.SetCooldown("claude", "5h", time.Now().Add(5*time.Hour).Unix(), "five_hour_95%", "rate_limited")
	if err := store.SaveProviderState(ps); err != nil {
		t.Fatalf("SaveProviderState() error = %v", err)
	}

	orch, err := New(config.Default(), workDir, true, "", false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if got := orch.currentProvider().Name(); got != "codex" {
		t.Fatalf("current provider = %q, want codex", got)
	}
}

func TestNewIgnoreCooldownOptionSkipsPersistedCooldowns(t *testing.T) {
	workDir := t.TempDir()
	store := state.NewStore(workDir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	ps := state.NewProviderState()
	ps.SetCooldown("claude", "5h", time.Now().Add(5*time.Hour).Unix(), "five_hour_95%", "rate_limited")
	ps.SetCooldown("codex", "5h", time.Now().Add(5*time.Hour).Unix(), "five_hour_91%", "rate_limited")
	if err := store.SaveProviderState(ps); err != nil {
		t.Fatalf("SaveProviderState() error = %v", err)
	}

	orch, err := New(config.Default(), workDir, false, "", true)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if got := orch.currentProvider().Name(); got != "claude" {
		t.Fatalf("current provider = %q, want claude", got)
	}
	if len(orch.providerState.Cooldowns) != 0 {
		t.Fatalf("expected empty cooldown state, got %#v", orch.providerState.Cooldowns)
	}
}
