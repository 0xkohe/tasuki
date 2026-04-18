package ui

import (
	"fmt"
	"strings"
	"time"
)

// Color codes
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
)

// ProviderColor returns a color code for a given provider name.
func ProviderColor(name string) string {
	switch name {
	case "claude":
		return Magenta
	case "codex":
		return Green
	case "copilot":
		return Blue
	default:
		return Cyan
	}
}

// Banner prints the startup banner.
func Banner() {
	fmt.Println(Bold + "unblocked" + Reset + Dim + " — AI CLI failover orchestrator" + Reset)
	fmt.Println()
}

// ProviderStatus prints the current provider status line.
func ProviderStatus(name string, index, total int) {
	color := ProviderColor(name)
	fmt.Printf(Dim+"["+Reset+color+Bold+"%s"+Reset+Dim+" %d/%d]"+Reset+"\n", name, index+1, total)
}

// SwitchNotice prints a provider switch notification.
func SwitchNotice(from, to, reason string) {
	fmt.Println()
	fmt.Printf(Cyan+Bold+"[unblocked]"+Reset+" failover: %s -> %s"+Dim+" (%s)"+Reset+"\n", from, to, reason)
	fmt.Println()
}

// FailoverBanner prints a focused failover panel before steps begin.
func FailoverBanner(from, to, trigger, detail string) {
	line := strings.Repeat("═", 72)
	fmt.Println()
	fmt.Println(Cyan + line + Reset)
	fmt.Println(Cyan + Bold + "  Switching Provider" + Reset)
	fmt.Printf(Dim+"  from:    "+Reset+"%s\n", from)
	fmt.Printf(Dim+"  to:      "+Reset+"%s\n", to)
	fmt.Printf(Dim+"  trigger: "+Reset+"%s\n", trigger)
	if detail != "" {
		fmt.Printf(Dim+"  matched: "+Reset+"%s\n", trimForDisplay(detail, 140))
	}
	fmt.Println(Cyan + line + Reset)
}

// FailoverStep prints a loading-style failover step.
func FailoverStep(step, total int, label string) {
	filled := strings.Repeat("■", step)
	empty := strings.Repeat("□", total-step)
	fmt.Printf(Dim+"[loading] "+Reset+"[%s] %s\n", filled+empty, label)
}

// ProviderReady prints that the next provider is ready to take over.
func ProviderReady(name string, handoff bool) {
	if handoff {
		fmt.Printf(Green+"[ready] "+Reset+"%s handoff loaded. interactive session starting\n", name)
		return
	}
	fmt.Printf(Green+"[ready] "+Reset+"%s interactive session starting\n", name)
}

// MessageContent prints agent output text.
func MessageContent(provider, text string) {
	if text == "" {
		return
	}
	fmt.Print(text)
}

// ErrorMessage prints an error message.
func ErrorMessage(msg string) {
	fmt.Printf(Red+"[error] "+Reset+"%s\n", msg)
}

// InfoMessage prints an informational message.
func InfoMessage(msg string) {
	fmt.Printf(Dim+"[info] "+Reset+"%s\n", msg)
}

// PreFailoverWarning prints a heads-up that the current provider is
// approaching its switch threshold and the next candidate has been verified.
// nextStatus is a short label like "ready", "auth missing", or "cooldown".
func PreFailoverWarning(current, next, trigger, nextStatus, detail string) {
	fmt.Println()
	fmt.Println(Yellow + Bold + "[unblocked] Approaching Limit" + Reset)
	fmt.Printf(Dim+"provider: "+Reset+"%s\n", current)
	fmt.Printf(Dim+"trigger:  "+Reset+"%s\n", trigger)
	if detail != "" {
		fmt.Printf(Dim+"matched:  "+Reset+"%s\n", trimForDisplay(detail, 120))
	}
	if next != "" {
		statusColor := Green
		lower := strings.ToLower(nextStatus)
		if strings.Contains(lower, "missing") || strings.Contains(lower, "cooldown") || strings.Contains(lower, "unavailable") {
			statusColor = Yellow
		}
		fmt.Printf(Dim+"next:     "+Reset+"%s "+Dim+"("+Reset+statusColor+"%s"+Reset+Dim+")"+Reset+"\n", next, nextStatus)
	} else {
		fmt.Printf(Dim + "next:     " + Reset + Yellow + "no candidate available" + Reset + "\n")
	}
	fmt.Printf(Dim + "action:   " + Reset + "continuing on current provider, prepared to switch on hard limit\n")
	fmt.Println()
}

// RateLimitWarning prints a rate limit warning.
func RateLimitWarning(provider, limitType, detail string) {
	fmt.Println()
	fmt.Println(Yellow + Bold + "[unblocked] Rate Limit Detected" + Reset)
	fmt.Printf(Dim+"provider: "+Reset+"%s\n", provider)
	fmt.Printf(Dim+"trigger:  "+Reset+"%s\n", limitType)
	if detail != "" {
		fmt.Printf(Dim+"matched:  "+Reset+"%s\n", trimForDisplay(detail, 120))
	}
	fmt.Printf(Dim + "action:   " + Reset + "stop current provider, save handoff, continue with next provider\n")
	fmt.Println()
}

// HandoffSaved prints notification of handoff save.
func HandoffSaved(path string) {
	fmt.Printf(Dim+"[unblocked] handoff saved:"+Reset+" %s\n", path)
}

// HandoffSummary prints a short summary of what is being passed forward.
func HandoffSummary(summary string) {
	if strings.TrimSpace(summary) == "" {
		return
	}
	fmt.Printf(Dim+"[unblocked] handoff summary:"+Reset+" %s\n", trimForDisplay(summary, 160))
}

// SessionInfo prints session summary.
func SessionInfo(id, goal, provider string, handoffs int) {
	fmt.Printf(Dim+"session:  "+Reset+"%s\n", id[:8])
	fmt.Printf(Dim+"goal:     "+Reset+"%s\n", goal)
	fmt.Printf(Dim+"provider: "+Reset+"%s\n", provider)
	if handoffs > 0 {
		fmt.Printf(Dim+"handoffs: "+Reset+"%d\n", handoffs)
	}
	fmt.Println()
}

// Separator prints a visual separator.
func Separator() {
	fmt.Println(Dim + strings.Repeat("─", 60) + Reset)
}

// Prompt prints the input prompt.
func Prompt() {
	fmt.Print(Bold + "> " + Reset)
}

// Done prints a completion message.
func Done(provider string) {
	fmt.Println()
	fmt.Printf(Dim+"[%s] done"+Reset+"\n", provider)
}

// AllProvidersExhausted prints a message when no providers are left.
func AllProvidersExhausted() {
	fmt.Println()
	fmt.Println(Red + Bold + "[unblocked] all providers exhausted" + Reset)
	fmt.Println(Dim + "All configured providers have hit their limits." + Reset)
	fmt.Println(Dim + "Session state saved in .unblocked/ — resume later." + Reset)
}

// RecoveredMessage reports that a previously rate-limited higher-priority
// provider has recovered and we are switching back to it.
func RecoveredMessage(from, to string) {
	fmt.Printf(Green+"[recovered] "+Reset+"%s is available again — switching from %s\n", to, from)
}

// CooldownBanner reports that a provider is still within its cooldown window.
func CooldownBanner(provider, cycle string, until time.Time) {
	if until.IsZero() {
		fmt.Printf(Dim+"[cooldown] "+Reset+"%s is still cooling down (%s)\n", provider, cycle)
		return
	}
	fmt.Printf(Dim+"[cooldown] "+Reset+"%s cooling down (%s) until %s\n",
		provider, cycle, until.Local().Format("15:04 MST"))
}

// PreferredOverCooldown notes that the user explicitly requested a provider
// that is still in cooldown.
func PreferredOverCooldown(provider string, until time.Time) {
	if until.IsZero() {
		fmt.Printf(Yellow+"[warn] "+Reset+"%s is still in cooldown but was requested with -p\n", provider)
		return
	}
	fmt.Printf(Yellow+"[warn] "+Reset+"%s cooldown until %s, but using per -p flag\n",
		provider, until.Local().Format("15:04 MST"))
}

func trimForDisplay(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
