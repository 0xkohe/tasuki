package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const relayDir = ".tasuki"

// Store manages session state persistence in the .tasuki/ directory.
type Store struct {
	root string // project root directory
}

// NewStore creates a store rooted at the given directory.
func NewStore(root string) *Store {
	return &Store{root: root}
}

// Dir returns the .relay directory path.
func (s *Store) Dir() string {
	return filepath.Join(s.root, relayDir)
}

// Init ensures the .relay directory structure exists.
func (s *Store) Init() error {
	dirs := []string{
		s.Dir(),
		filepath.Join(s.Dir(), "history"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}

// SaveSession writes the session to .tasuki/session.json.
func (s *Store) SaveSession(sess *Session) error {
	sess.Touch()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	path := filepath.Join(s.Dir(), "session.json")
	return os.WriteFile(path, data, 0644)
}

// LoadSession reads the session from .tasuki/session.json.
func (s *Store) LoadSession() (*Session, error) {
	path := filepath.Join(s.Dir(), "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &sess, nil
}

// HasSession checks if a session.json exists.
func (s *Store) HasSession() bool {
	path := filepath.Join(s.Dir(), "session.json")
	_, err := os.Stat(path)
	return err == nil
}

// SaveHandoff writes the handoff markdown to .tasuki/handoff.md
// and also archives it to .tasuki/history/.
func (s *Store) SaveHandoff(content string, count int) error {
	// Current handoff
	path := filepath.Join(s.Dir(), "handoff.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write handoff: %w", err)
	}

	// Archive
	archivePath := filepath.Join(s.Dir(), "history", fmt.Sprintf("handoff_%03d.md", count))
	return os.WriteFile(archivePath, []byte(content), 0644)
}

// LoadHandoff reads the current handoff.md.
func (s *Store) LoadHandoff() (string, error) {
	path := filepath.Join(s.Dir(), "handoff.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DeleteSession removes the current session file.
func (s *Store) DeleteSession() error {
	path := filepath.Join(s.Dir(), "session.json")
	return os.Remove(path)
}

// DeleteProviderState removes the persisted provider cooldown state.
func (s *Store) DeleteProviderState() error {
	path := filepath.Join(s.Dir(), "provider_state.json")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// LoadProviderState reads .tasuki/provider_state.json. When the file does
// not exist, an empty state is returned (not an error).
func (s *Store) LoadProviderState() (*ProviderState, error) {
	path := filepath.Join(s.Dir(), "provider_state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewProviderState(), nil
		}
		return nil, fmt.Errorf("read provider state: %w", err)
	}
	ps := NewProviderState()
	if err := json.Unmarshal(data, ps); err != nil {
		return nil, fmt.Errorf("unmarshal provider state: %w", err)
	}
	if ps.Cooldowns == nil {
		ps.Cooldowns = map[string]ProviderCooldown{}
	}
	return ps, nil
}

// SaveProviderState writes the provider state to .tasuki/provider_state.json.
func (s *Store) SaveProviderState(ps *ProviderState) error {
	if ps == nil {
		return nil
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provider state: %w", err)
	}
	path := filepath.Join(s.Dir(), "provider_state.json")
	return os.WriteFile(path, data, 0644)
}
