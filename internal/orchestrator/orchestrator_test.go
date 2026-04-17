package orchestrator

import (
	"testing"

	"github.com/kooooohe/unblocked/internal/config"
	"github.com/kooooohe/unblocked/internal/state"
)

func TestEnsureSessionDoesNotResumeByDefault(t *testing.T) {
	workDir := t.TempDir()
	cfg := config.Default()

	orch, err := New(cfg, workDir, false, "")
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

	orch, err := New(cfg, workDir, true, "")
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
