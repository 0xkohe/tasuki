package orchestrator

import (
	"fmt"

	"github.com/kooooohe/unblocked/internal/adapter"
	"github.com/kooooohe/unblocked/internal/state"
	"github.com/kooooohe/unblocked/internal/ui"
)

// switchProvider handles the transition from the current provider to the next one.
// Returns the new provider index, or an error if all providers are exhausted.
func (o *Orchestrator) switchProvider(reason string) error {
	currentName := o.providers[o.current].Name()

	// Record the switch in session state
	nextIdx := o.current + 1
	if nextIdx >= len(o.providers) {
		ui.AllProvidersExhausted()
		return fmt.Errorf("all providers exhausted")
	}

	nextProvider := o.providers[nextIdx]
	nextName := nextProvider.Name()

	// Update session
	o.session.RecordSwitch(currentName, nextName, reason)

	// Generate and save handoff
	handoffMD, err := GenerateHandoffMD(o.session)
	if err != nil {
		return fmt.Errorf("generate handoff: %w", err)
	}

	if err := o.store.SaveHandoff(handoffMD, o.session.HandoffCount); err != nil {
		return fmt.Errorf("save handoff: %w", err)
	}

	if err := o.store.SaveSession(o.session); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	// Display switch notification
	ui.SwitchNotice(currentName, nextName, reason)
	ui.HandoffSaved(o.store.Dir() + "/handoff.md")

	o.current = nextIdx
	return nil
}

// buildRequest creates an adapter.Request, injecting handoff context if this is a continuation.
func (o *Orchestrator) buildRequest(prompt string) (*adapter.Request, error) {
	req := &adapter.Request{
		Prompt:      prompt,
		WorkDir:     o.workDir,
		Constraints: o.session.Constraints,
	}

	// If this is a handoff (not the first provider), inject resume context
	if o.session.HandoffCount > 0 {
		resumePrompt, err := GenerateResumePrompt(o.session, o.workDir)
		if err != nil {
			return nil, fmt.Errorf("generate resume prompt: %w", err)
		}
		req.Context = resumePrompt
	}

	return req, nil
}

// currentProvider returns the active provider.
func (o *Orchestrator) currentProvider() adapter.Provider {
	return o.providers[o.current]
}

// initProviders creates provider instances from the configured names.
func initProviders(names []string) []adapter.Provider {
	registry := map[string]func() adapter.Provider{
		"claude":  func() adapter.Provider { return adapter.NewClaude() },
		"codex":   func() adapter.Provider { return adapter.NewCodex() },
		"copilot": func() adapter.Provider { return adapter.NewCopilot() },
	}

	var providers []adapter.Provider
	for _, name := range names {
		if factory, ok := registry[name]; ok {
			p := factory()
			if p.IsAvailable() {
				providers = append(providers, p)
			} else {
				ui.InfoMessage(fmt.Sprintf("provider %q not available (CLI not found), skipping", name))
			}
		}
	}
	return providers
}

// providerStatus returns a function to check the status of a provider session.
func (o *Orchestrator) isRateLimited(evt adapter.Event) bool {
	return evt.Type == adapter.EventRateLimit
}

// findProviderEntry returns the most recent provider history entry for the given provider.
func findProviderEntry(sess *state.Session, providerName string) *state.ProviderEntry {
	for i := len(sess.ProviderHistory) - 1; i >= 0; i-- {
		if sess.ProviderHistory[i].Provider == providerName {
			return &sess.ProviderHistory[i]
		}
	}
	return nil
}
