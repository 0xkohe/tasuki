package orchestrator

import (
	"fmt"
	"time"

	"github.com/kooooohe/unblocked/internal/adapter"
	"github.com/kooooohe/unblocked/internal/config"
	"github.com/kooooohe/unblocked/internal/state"
	"github.com/kooooohe/unblocked/internal/ui"
)

// switchProvider handles the transition from the current provider to the next one.
// The destination is chosen by selectStartingProvider so chain failovers respect
// priority and cooldown state. Returns an error if every provider is exhausted.
func (o *Orchestrator) switchProvider(reason string) error {
	return o.switchProviderWithCooldown(reason, "", "", 0)
}

// switchProviderWithCooldown is switchProvider plus explicit cooldown metadata
// for the outgoing provider.
func (o *Orchestrator) switchProviderWithCooldown(reason, trigger, cycle string, resetsAt int64) error {
	currentName := o.providers[o.current].Name()

	if reason == "rate_limited" {
		recordTrigger := trigger
		if recordTrigger == "" {
			recordTrigger = reason
		}
		o.recordCooldown(currentName, cycle, resetsAt, recordTrigger, reason)
	}

	res := selectStartingProvider(o.cfg, o.providers, o.providerState, time.Now(), "", currentName)
	if res.Index < 0 || res.Index == o.current {
		ui.AllProvidersExhausted()
		return fmt.Errorf("all providers exhausted")
	}

	nextName := o.providers[res.Index].Name()

	ui.FailoverStep(1, 3, fmt.Sprintf("recording %s session state", currentName))
	o.session.RecordSwitchWithCooldown(currentName, nextName, reason, cycle, resetsAt)

	ui.FailoverStep(2, 3, "saving handoff context")
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

	// Clear any stale cooldown on the destination (e.g. after recovery).
	if o.providerState != nil {
		o.providerState.ClearCooldown(nextName)
		_ = o.store.SaveProviderState(o.providerState)
	}

	ui.FailoverStep(3, 3, fmt.Sprintf("queueing %s", nextName))
	ui.SwitchNotice(currentName, nextName, reason)
	ui.HandoffSaved(o.store.Dir() + "/handoff.md")
	ui.HandoffSummary(o.session.RecentSummary)

	o.current = res.Index
	return nil
}

func (o *Orchestrator) switchProviderWithContext(reason string, trigger string, detail string) error {
	return o.switchProviderWithContextInfo(reason, trigger, detail, nil)
}

// switchProviderWithContextInfo extends switchProviderWithContext with the
// adapter-emitted RateLimitInfo so adapter-level ResetsAt / Cycle values
// (when present) are preserved in the cooldown record.
func (o *Orchestrator) switchProviderWithContextInfo(reason, trigger, detail string, info *adapter.RateLimitInfo) error {
	currentName := o.providers[o.current].Name()

	cycle := ""
	resetsAt := int64(0)
	if info != nil {
		cycle = info.Cycle
		resetsAt = info.ResetsAt
	}
	if cycle == "" {
		cycle = cycleFromRateLimitType(trigger)
	}
	if resetsAt <= 0 && cycle != "" {
		resetsAt = estimateResetsAt(cycle, time.Now())
	}

	res := o.previewSelectionAfterCooldown(currentName, cycle, resetsAt, trigger, reason)
	if res.Index < 0 || res.Index == o.current {
		ui.AllProvidersExhausted()
		return fmt.Errorf("all providers exhausted")
	}

	ui.FailoverBanner(currentName, o.providers[res.Index].Name(), trigger, detail)
	return o.switchProviderWithCooldown(reason, trigger, cycle, resetsAt)
}

// previewSelectionAfterCooldown computes the next provider as if the current
// one had already been placed into cooldown. This keeps the failover banner and
// exhaustion checks aligned with the actual switch that follows.
func (o *Orchestrator) previewSelectionAfterCooldown(
	currentName, cycle string,
	resetsAt int64,
	trigger, reason string,
) selectionResult {
	previewState := cloneProviderState(o.providerState)
	if previewState == nil {
		previewState = state.NewProviderState()
	}
	previewState.SetCooldown(currentName, cycle, resetsAt, trigger, reason)
	return selectStartingProvider(o.cfg, o.providers, previewState, time.Now(), "", currentName)
}

func cloneProviderState(src *state.ProviderState) *state.ProviderState {
	if src == nil {
		return nil
	}
	dst := state.NewProviderState()
	dst.UpdatedAt = src.UpdatedAt
	for name, cooldown := range src.Cooldowns {
		dst.Cooldowns[name] = cooldown
	}
	return dst
}

// prepareFailover is invoked when the current provider crosses the warning
// threshold but not the switch threshold yet. It eagerly picks the next
// candidate (as if the current were already cooling down), re-validates its
// availability (catches auth drift), and surfaces a heads-up so the eventual
// switch is near-instant. No session mutation happens here.
func (o *Orchestrator) prepareFailover(trigger, cycle, detail string, info *adapter.RateLimitInfo) {
	currentName := o.providers[o.current].Name()

	resolvedCycle := cycle
	if resolvedCycle == "" && info != nil {
		resolvedCycle = info.Cycle
	}
	if resolvedCycle == "" {
		resolvedCycle = cycleFromRateLimitType(trigger)
	}
	resetsAt := int64(0)
	if info != nil {
		resetsAt = info.ResetsAt
	}
	if resetsAt <= 0 && resolvedCycle != "" {
		resetsAt = estimateResetsAt(resolvedCycle, time.Now())
	}

	res := o.previewSelectionAfterCooldown(currentName, resolvedCycle, resetsAt, trigger, "warn")
	if res.Index < 0 || res.Index == o.current {
		ui.PreFailoverWarning(currentName, "", trigger, "", detail)
		return
	}

	next := o.providers[res.Index]
	nextName := next.Name()
	status := "ready"
	if !next.IsAvailable() {
		status = "auth missing"
	} else if res.Reason == "all_cooldown" {
		status = "cooldown"
	} else if o.providerState != nil {
		if cd, ok := o.providerState.Cooldowns[nextName]; ok && !cd.IsAvailable(time.Now()) {
			status = "cooldown"
		}
	}
	ui.PreFailoverWarning(currentName, nextName, trigger, status, detail)
}

// recordCooldown stores a cooldown entry for the outgoing provider and
// persists the updated state.
func (o *Orchestrator) recordCooldown(name, cycle string, resetsAt int64, trigger, reason string) {
	if o.providerState == nil {
		return
	}
	if cycle == "" {
		cycle = o.cfg.ProviderResetCycle(name)
	}
	if resetsAt <= 0 && cycle != "" {
		resetsAt = estimateResetsAt(cycle, time.Now())
	}
	o.providerState.SetCooldown(name, cycle, resetsAt, trigger, reason)
	_ = o.store.SaveProviderState(o.providerState)
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
func initProviders(cfg *config.Config) []adapter.Provider {
	registry := map[string]func(adapter.Options) adapter.Provider{
		"claude":  func(opts adapter.Options) adapter.Provider { return adapter.NewClaude(opts) },
		"codex":   func(opts adapter.Options) adapter.Provider { return adapter.NewCodex(opts) },
		"copilot": func(opts adapter.Options) adapter.Provider { return adapter.NewCopilot(opts) },
	}

	var providers []adapter.Provider
	for _, name := range cfg.ProviderNames() {
		if factory, ok := registry[name]; ok {
			p := factory(adapter.Options{
				SwitchThreshold:    cfg.ProviderThreshold(name),
				WarnThreshold:      cfg.ProviderWarnThreshold(name),
				PreserveScrollback: cfg.ProviderPreserveScrollback(name),
			})
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
