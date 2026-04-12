package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const relayDir = ".unblocked"

// Store manages session state persistence in the .unblocked/ directory.
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

// SaveSession writes the session to .unblocked/session.json.
func (s *Store) SaveSession(sess *Session) error {
	sess.Touch()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	path := filepath.Join(s.Dir(), "session.json")
	return os.WriteFile(path, data, 0644)
}

// LoadSession reads the session from .unblocked/session.json.
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

// SaveHandoff writes the handoff markdown to .unblocked/handoff.md
// and also archives it to .unblocked/history/.
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
