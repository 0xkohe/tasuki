package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/kooooohe/tasuki/internal/adapter"
	"github.com/kooooohe/tasuki/internal/config"
	"github.com/kooooohe/tasuki/internal/state"
	"github.com/kooooohe/tasuki/internal/ui"
	"golang.org/x/term"
)

// Orchestrator manages provider lifecycle and failover.
type Orchestrator struct {
	providers      []adapter.Provider
	current        int
	session        *state.Session
	store          *state.Store
	cfg            *config.Config
	workDir        string
	resume         bool
	preferred      string
	ignoreCooldown bool
	providerState  *state.ProviderState
	initialPick    selectionResult
}

// New creates and initializes an Orchestrator. `preferred` corresponds to the
// -p flag — when set, that provider is selected even if it is in cooldown.
func New(cfg *config.Config, workDir string, resume bool, preferred string, ignoreCooldown bool) (*Orchestrator, error) {
	store := state.NewStore(workDir)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	providerNames := cfg.ProviderNames()
	if len(providerNames) == 0 {
		return nil, fmt.Errorf("no providers configured")
	}

	providers := initProviders(cfg)
	if len(providers) == 0 {
		return nil, fmt.Errorf("no available providers found (checked: %v)", providerNames)
	}

	var ps *state.ProviderState
	var err error
	if ignoreCooldown {
		ps = state.NewProviderState()
		_ = store.DeleteProviderState()
	} else {
		ps, err = store.LoadProviderState()
		if err != nil {
			return nil, fmt.Errorf("load provider state: %w", err)
		}
	}

	pick := selectStartingProvider(cfg, providers, ps, time.Now(), preferred, "")
	if pick.Index < 0 {
		pick.Index = 0
	}

	if !ignoreCooldown && ps != nil && ps.Prune(time.Now()) {
		_ = store.SaveProviderState(ps)
	}

	return &Orchestrator{
		providers:      providers,
		current:        pick.Index,
		store:          store,
		cfg:            cfg,
		workDir:        workDir,
		resume:         resume,
		preferred:      preferred,
		ignoreCooldown: ignoreCooldown,
		providerState:  ps,
		initialPick:    pick,
	}, nil
}

// RunPassthrough runs the CLI in full interactive PTY mode.
// The user sees and uses the CLI's native UI directly.
// relay monitors output for rate limits and switches providers when needed.
func (o *Orchestrator) RunPassthrough(ctx context.Context, initialPrompt string) error {
	// Initialize session
	o.ensureSession(initialPrompt)

	var names []string
	for _, p := range o.providers {
		names = append(names, p.Name())
	}

	ui.Banner()
	ui.InfoMessage("providers: " + strings.Join(names, " -> "))
	ui.InfoMessage("rate limit detected → automatic failover")
	o.announceInitialPick()
	ui.Separator()
	fmt.Println()

	for {
		provider := o.currentProvider()
		providerName := provider.Name()

		withHandoff := o.session.HandoffCount > 0
		if withHandoff {
			ui.InfoMessage(fmt.Sprintf("loading %s with handoff context (provider %d/%d)", providerName, o.current+1, len(o.providers)))
		} else {
			ui.InfoMessage(fmt.Sprintf("starting %s (provider %d/%d)", providerName, o.current+1, len(o.providers)))
		}
		fmt.Println()

		// Build the prompt for this provider
		prompt := initialPrompt
		if o.session.HandoffCount > 0 {
			// Interactive providers get a short handoff prompt that points them
			// at .tasuki/handoff.md instead of echoing the full resume context.
			resumePrompt, err := GenerateInteractiveResumePrompt(o.session, o.workDir)
			if err != nil {
				ui.ErrorMessage(fmt.Sprintf("generate resume prompt: %v", err))
			} else {
				prompt = resumePrompt
				if initialPrompt != "" {
					prompt = resumePrompt + "\n\nContinue with: " + initialPrompt
				}
			}
		}

		// Print the ready banner BEFORE enabling raw mode. term.MakeRaw disables
		// OPOST/ONLCR on the shared TTY, so a trailing "\n" would no longer
		// return the cursor to column 0 — the next provider's banner would then
		// render offset by the length of this line.
		ui.ProviderReady(providerName, withHandoff)

		// Put terminal into raw mode for PTY passthrough
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("make raw: %w", err)
		}

		sess, err := provider.RunInteractive(ctx, o.workDir, prompt)
		if err != nil {
			term.Restore(int(os.Stdin.Fd()), oldState)
			ui.ErrorMessage(fmt.Sprintf("%s failed to start: %v", providerName, err))
			if switchErr := o.switchProvider("start_failed"); switchErr != nil {
				return switchErr
			}
			continue
		}

		// Handle terminal resize (SIGWINCH)
		sigWinch := make(chan os.Signal, 1)
		signal.Notify(sigWinch, syscall.SIGWINCH)
		go func() {
			for range sigWinch {
				if ws, err := pty.GetsizeFull(os.Stdin); err == nil {
					sess.Resize(uint16(ws.Rows), uint16(ws.Cols))
				}
			}
		}()

		// Monitor for rate limit events while the CLI runs.
		// When detected, terminate the current provider immediately and fail over.
		rateLimited := false
		rateLimitType := "unknown"
		rateLimitDetail := ""
		var rateLimitInfo *adapter.RateLimitInfo

	waitLoop:
		for {
			select {
			case evt, ok := <-sess.Events:
				if !ok {
					break waitLoop
				}
				if evt.Type == adapter.EventRateLimitWarning {
					warnType := "unknown"
					cycle := ""
					if evt.RateLimit != nil {
						if evt.RateLimit.Type != "" {
							warnType = evt.RateLimit.Type
						}
						cycle = evt.RateLimit.Cycle
					}
					o.prepareFailover(warnType, cycle, evt.Content, evt.RateLimit)
					continue
				}
				if evt.Type == adapter.EventRateLimit {
					rateLimited = true
					if evt.RateLimit != nil && evt.RateLimit.Type != "" {
						rateLimitType = evt.RateLimit.Type
					}
					if evt.RateLimit != nil {
						rateLimitInfo = evt.RateLimit
					}
					rateLimitDetail = evt.Content
					_ = sess.Close()
					break waitLoop
				}
			case <-sess.Done:
				// CLI process exited
				break waitLoop
			case <-ctx.Done():
				_ = sess.Close()
				term.Restore(int(os.Stdin.Fd()), oldState)
				signal.Stop(sigWinch)
				return nil
			}
		}

		if sess.Snapshot != nil {
			snapshot := sess.Snapshot()
			o.session.LastOutput = snapshot.RecentOutput
			o.session.RecentTranscript = snapshot.RecentTranscript
			o.session.RecentSummary = snapshot.Summary
			_ = o.store.SaveSession(o.session)
		}

		// Cleanup
		_ = sess.Close()
		term.Restore(int(os.Stdin.Fd()), oldState)
		signal.Stop(sigWinch)

		if !rateLimited {
			// Normal exit — done
			fmt.Println()
			ui.Done(providerName)
			if o.session != nil {
				_ = o.store.SaveSession(o.session)
			}
			return nil
		}

		// Rate limit was detected during this session.
		ui.RateLimitWarning(providerName, rateLimitType, rateLimitDetail)

		if err := o.switchProviderWithContextInfo("rate_limited", rateLimitType, rateLimitDetail, rateLimitInfo); err != nil {
			return err
		}

		// Clear the initial prompt for subsequent providers since
		// the handoff context will carry the goal
		initialPrompt = ""

		// Loop back and start the next provider
	}
}

// RunOnce executes a single prompt in non-interactive mode, handling failover.
func (o *Orchestrator) RunOnce(ctx context.Context, prompt string) error {
	o.ensureSession(prompt)
	o.session.Goal = prompt

	ui.ProviderStatus(o.currentProvider().Name(), o.current, len(o.providers))

	return o.executeWithFailover(ctx, prompt)
}

// RunInteractive runs a simple REPL loop in non-interactive mode.
func (o *Orchestrator) RunInteractive(ctx context.Context) error {
	ui.Banner()

	var names []string
	for _, p := range o.providers {
		names = append(names, p.Name())
	}
	ui.InfoMessage("providers: " + strings.Join(names, " -> "))
	o.announceInitialPick()
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		o.maybeRestoreHigherPriority()
		ui.Prompt()
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "/quit" || input == "/exit":
			if o.session != nil {
				_ = o.store.SaveSession(o.session)
			}
			ui.InfoMessage("session saved. goodbye.")
			return nil

		case input == "/status":
			o.printStatus()
			continue

		case input == "/switch":
			if err := o.switchProvider("manual"); err != nil {
				ui.ErrorMessage(err.Error())
			}
			continue

		case input == "/handoff":
			if o.session != nil {
				md, err := GenerateHandoffMD(o.session)
				if err != nil {
					ui.ErrorMessage(err.Error())
				} else {
					fmt.Println(md)
				}
			}
			continue
		}

		if err := o.RunOnce(ctx, input); err != nil {
			ui.ErrorMessage(err.Error())
		}

		fmt.Println()
	}

	return nil
}

// maybeRestoreHigherPriority re-evaluates cooldown state and, when a previously
// rate-limited higher-priority provider has recovered, switches back to it via
// the standard handoff flow.
func (o *Orchestrator) maybeRestoreHigherPriority() {
	if o.preferred != "" {
		// User explicitly pinned a provider — don't second-guess during REPL.
		return
	}
	if o.providerState == nil {
		return
	}
	currentName := o.providers[o.current].Name()
	res := selectStartingProvider(o.cfg, o.providers, o.providerState, time.Now(), "", currentName)
	if res.Reason != "recovered" || res.Index == o.current {
		return
	}
	if err := o.switchProvider("recovered"); err != nil {
		ui.ErrorMessage(err.Error())
	}
}

// announceInitialPick surfaces why the starting provider was chosen so the
// user understands any deviation from config order.
func (o *Orchestrator) announceInitialPick() {
	switch o.initialPick.Reason {
	case "recovered":
		ui.RecoveredMessage(o.initialPick.ReplacedActive, o.providers[o.initialPick.Index].Name())
	case "preferred":
		if !o.initialPick.CooldownUntil.IsZero() {
			ui.PreferredOverCooldown(o.providers[o.initialPick.Index].Name(), o.initialPick.CooldownUntil)
		}
	case "all_cooldown":
		ui.CooldownBanner(
			o.providers[o.initialPick.Index].Name(),
			o.cfg.ProviderResetCycle(o.providers[o.initialPick.Index].Name()),
			o.initialPick.CooldownUntil,
		)
	}
	if o.providerState != nil {
		for name, cd := range o.providerState.Cooldowns {
			if name == o.providers[o.initialPick.Index].Name() {
				continue
			}
			ui.CooldownBanner(name, cd.Cycle, cd.ExpiresAt(time.Now()))
		}
	}
}

func (o *Orchestrator) ensureSession(goal string) {
	if o.session != nil {
		return
	}
	if o.resume && o.store.HasSession() {
		sess, err := o.store.LoadSession()
		if err == nil {
			o.session = sess
			ui.InfoMessage("resuming session " + sess.SessionID[:8])
			newName := o.currentProvider().Name()
			if sess.CurrentProvider != "" && sess.CurrentProvider != newName {
				// Prior session was on a different provider; record a handoff
				// so the resume prompt is regenerated for the recovered one.
				cycle := ""
				resetsAt := int64(0)
				if o.providerState != nil {
					if cd, ok := o.providerState.Cooldowns[sess.CurrentProvider]; ok {
						cycle = cd.Cycle
						resetsAt = cd.ResetsAt
					}
				}
				sess.RecordSwitchWithCooldown(sess.CurrentProvider, newName, "recovered", cycle, resetsAt)
				if handoffMD, herr := GenerateHandoffMD(sess); herr == nil {
					_ = o.store.SaveHandoff(handoffMD, sess.HandoffCount)
				}
				_ = o.store.SaveSession(sess)
			}
			return
		}
	}
	o.session = state.NewSession(
		uuid.New().String(),
		goal,
		o.currentProvider().Name(),
	)
}

func (o *Orchestrator) executeWithFailover(ctx context.Context, prompt string) error {
	for {
		provider := o.currentProvider()

		req, err := o.buildRequest(prompt)
		if err != nil {
			return err
		}

		events, err := provider.Execute(ctx, req)
		if err != nil {
			ui.ErrorMessage(fmt.Sprintf("%s failed to start: %v", provider.Name(), err))
			if switchErr := o.switchProvider("start_failed"); switchErr != nil {
				return switchErr
			}
			continue
		}

		var output strings.Builder
		needSwitch := false
		var switchReason string
		rateLimitType := "unknown"
		rateLimitDetail := ""
		var rateLimitInfo *adapter.RateLimitInfo

		for evt := range events {
			switch evt.Type {
			case adapter.EventMessageDelta:
				ui.MessageContent(provider.Name(), evt.Content)
				output.WriteString(evt.Content)

			case adapter.EventTurnComplete:
				// noop

			case adapter.EventRateLimitWarning:
				warnType := "unknown"
				cycle := ""
				if evt.RateLimit != nil {
					if evt.RateLimit.Type != "" {
						warnType = evt.RateLimit.Type
					}
					cycle = evt.RateLimit.Cycle
				}
				o.prepareFailover(warnType, cycle, evt.Content, evt.RateLimit)

			case adapter.EventRateLimit:
				limitType := "unknown"
				if evt.RateLimit != nil {
					limitType = evt.RateLimit.Type
					rateLimitInfo = evt.RateLimit
				}
				ui.RateLimitWarning(provider.Name(), limitType, evt.Content)
				needSwitch = true
				switchReason = "rate_limited"
				rateLimitType = limitType
				rateLimitDetail = evt.Content

			case adapter.EventError:
				if isLikelyRateLimit(evt.Content) {
					ui.RateLimitWarning(provider.Name(), "detected_from_error", evt.Content)
					needSwitch = true
					switchReason = "rate_limited"
					rateLimitType = "detected_from_error"
					rateLimitDetail = evt.Content
				} else {
					ui.ErrorMessage(fmt.Sprintf("[%s] %s", provider.Name(), evt.Content))
					needSwitch = true
					switchReason = "error"
				}

			case adapter.EventDone:
				if evt.Content != "" {
					ui.MessageContent(provider.Name(), evt.Content)
					output.WriteString(evt.Content)
				}
			}
		}

		o.session.LastOutput = output.String()
		_ = o.store.SaveSession(o.session)

		if !needSwitch {
			ui.Done(provider.Name())
			return nil
		}

		if switchReason == "rate_limited" {
			if err := o.switchProviderWithContextInfo(switchReason, rateLimitType, rateLimitDetail, rateLimitInfo); err != nil {
				return err
			}
			continue
		}

		if err := o.switchProvider(switchReason); err != nil {
			return err
		}
	}
}

func (o *Orchestrator) printStatus() {
	if o.session == nil {
		ui.InfoMessage("no active session")
		return
	}

	ui.Separator()
	ui.SessionInfo(
		o.session.SessionID,
		o.session.Goal,
		o.session.CurrentProvider,
		o.session.HandoffCount,
	)

	fmt.Printf("providers:\n")
	for i, p := range o.providers {
		marker := "  "
		if i == o.current {
			marker = "-> "
		}
		fmt.Printf("  %s%s\n", marker, p.Name())
	}
	ui.Separator()
}

func isLikelyRateLimit(text string) bool {
	return adapter.LooksLikeHardRateLimitText(text) || strings.Contains(strings.ToLower(text), "429")
}
