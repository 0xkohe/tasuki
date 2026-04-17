package state

import "time"

// Decision records a design decision made during the session.
type Decision struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// ProviderEntry logs a provider switch event.
type ProviderEntry struct {
	Provider string `json:"provider"`
	Status   string `json:"status"` // "active", "rate_limited", "error", "done"
	At       string `json:"at"`
	Cycle    string `json:"cycle,omitempty"`
	ResetsAt int64  `json:"resets_at,omitempty"`
}

// Session represents the canonical state of an ongoing task.
type Session struct {
	SessionID        string          `json:"session_id"`
	Goal             string          `json:"goal"`
	Constraints      []string        `json:"constraints"`
	CompletedSteps   []string        `json:"completed_steps"`
	PendingSteps     []string        `json:"pending_steps"`
	FilesTouched     []string        `json:"files_touched"`
	Decisions        []Decision      `json:"decisions"`
	CurrentProvider  string          `json:"current_provider"`
	ProviderHistory  []ProviderEntry `json:"provider_history"`
	HandoffCount     int             `json:"handoff_count"`
	RecentSummary    string          `json:"recent_summary"`
	RecentTranscript string          `json:"recent_transcript"`
	LastOutput       string          `json:"last_output"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
}

// NewSession creates a new session with defaults.
func NewSession(id, goal, provider string) *Session {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Session{
		SessionID:       id,
		Goal:            goal,
		Constraints:     []string{},
		CompletedSteps:  []string{},
		PendingSteps:    []string{},
		FilesTouched:    []string{},
		Decisions:       []Decision{},
		CurrentProvider: provider,
		ProviderHistory: []ProviderEntry{
			{Provider: provider, Status: "active", At: now},
		},
		HandoffCount: 0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// RecordSwitch appends a provider switch entry.
func (s *Session) RecordSwitch(from, toProvider, reason string) {
	s.RecordSwitchWithCooldown(from, toProvider, reason, "", 0)
}

// RecordSwitchWithCooldown appends a provider switch entry and annotates the
// outgoing provider's history entry with cooldown metadata.
func (s *Session) RecordSwitchWithCooldown(from, toProvider, reason, cycle string, resetsAt int64) {
	now := time.Now().UTC().Format(time.RFC3339)
	s.ProviderHistory = append(s.ProviderHistory, ProviderEntry{
		Provider: from, Status: reason, At: now, Cycle: cycle, ResetsAt: resetsAt,
	})
	s.ProviderHistory = append(s.ProviderHistory, ProviderEntry{
		Provider: toProvider, Status: "active", At: now,
	})
	s.CurrentProvider = toProvider
	s.HandoffCount++
	s.UpdatedAt = now
}

// Touch marks the session as updated.
func (s *Session) Touch() {
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}
