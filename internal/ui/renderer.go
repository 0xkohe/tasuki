package ui

import (
	"fmt"
	"strings"
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
	fmt.Printf(Yellow+Bold+"[unblocked]"+Reset+" switching: %s -> %s (%s)\n", from, to, reason)
	fmt.Println()
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

// RateLimitWarning prints a rate limit warning.
func RateLimitWarning(provider, limitType string) {
	fmt.Printf(Yellow+"[rate limit] "+Reset+"%s hit rate limit (%s)\n", provider, limitType)
}

// HandoffSaved prints notification of handoff save.
func HandoffSaved(path string) {
	fmt.Printf(Dim+"[unblocked] handoff saved: %s"+Reset+"\n", path)
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
