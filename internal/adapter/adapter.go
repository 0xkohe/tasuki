package adapter

import (
	"context"
	"encoding/json"
	"io"
	"os"
)

// EventType represents the type of event from a provider.
type EventType int

const (
	EventMessageDelta EventType = iota
	EventTurnComplete
	EventRateLimit
	EventError
	EventDone
)

// Usage tracks token and cost information for a single execution.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// RateLimitInfo holds rate limit details when detected.
type RateLimitInfo struct {
	ResetsAt int64  `json:"resets_at"`       // Unix timestamp
	Type     string `json:"type"`            // e.g. "five_hour_96%"
	Cycle    string `json:"cycle,omitempty"` // "5h" | "weekly" | "monthly" | ""
}

// Event represents a single event from a provider's execution stream.
type Event struct {
	Type      EventType
	Content   string
	Raw       json.RawMessage
	Usage     *Usage
	RateLimit *RateLimitInfo
}

// PassthroughSnapshot captures recent interactive session context for handoff.
type PassthroughSnapshot struct {
	RecentOutput     string
	RecentTranscript string
	Summary          string
}

// Options controls provider-specific runtime behavior.
type Options struct {
	SwitchThreshold    int
	PreserveScrollback bool
}

// Request represents a prompt to send to a provider.
type Request struct {
	Prompt      string   // The main prompt
	Context     string   // Handoff context from a previous provider
	WorkDir     string   // Working directory
	Constraints []string // Task constraints
}

// InteractiveSession represents a running interactive CLI session.
type InteractiveSession struct {
	// PTY is the pseudo-terminal file descriptor.
	// Read from it to get CLI output, write to it to send input.
	PTY *os.File

	// Events receives rate limit and error events detected from output.
	Events <-chan Event

	// Done is closed when the CLI process exits.
	Done <-chan struct{}

	// Resize sends terminal size changes to the PTY.
	Resize func(rows, cols uint16)

	// Snapshot returns recent interactive context for handoff.
	Snapshot func() PassthroughSnapshot

	// Close terminates the session.
	Close func() error
}

// Provider is the interface each CLI adapter must implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "claude", "codex", "copilot").
	Name() string

	// Execute starts the CLI in non-interactive mode with the given request
	// and returns a channel of events. The channel is closed when done.
	Execute(ctx context.Context, req *Request) (<-chan Event, error)

	// RunInteractive starts the CLI in full interactive mode with a PTY.
	// The user's terminal is connected directly to the CLI.
	// Returns an InteractiveSession for monitoring and control.
	RunInteractive(ctx context.Context, workDir string, initialPrompt string) (*InteractiveSession, error)

	// SendInput writes text to the interactive session as if the user typed it.
	SendInput(sess *InteractiveSession, text string) error

	// Stop gracefully terminates the running CLI process.
	Stop() error

	// IsAvailable checks if the CLI binary is installed and reachable.
	IsAvailable() bool
}

// WriteToSession sends input to an interactive session's PTY.
func WriteToSession(sess *InteractiveSession, text string) error {
	_, err := io.WriteString(sess.PTY, text)
	return err
}
