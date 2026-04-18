package orchestrator

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"text/template"

	"github.com/kooooohe/tasuki/internal/state"
)

const handoffTemplate = `# Handoff

## Goal
{{ .Goal }}

## Constraints
{{ range .Constraints }}- {{ . }}
{{ end }}
## Completed
{{ range .CompletedSteps }}- {{ . }}
{{ end }}
## Pending
{{ range .PendingSteps }}- {{ . }}
{{ end }}
## Files Touched
{{ range .FilesTouched }}- {{ . }}
{{ end }}
## Decisions
{{ range .Decisions }}- {{ .Title }}: {{ .Reason }}
{{ end }}
## Recent Summary
{{ .RecentSummary }}

## Recent Transcript
{{ .RecentTranscript }}

## Last Output Snapshot
{{ .LastOutput }}

## Provider History
{{ range .ProviderHistory }}- [{{ .At }}] {{ .Provider }}: {{ .Status }}
{{ end }}
`

const resumePromptTemplate = `You are continuing an in-progress coding task.
Read the following context carefully and continue exactly from where the previous agent stopped.

## Goal
{{ .Goal }}

## Constraints
{{ range .Constraints }}- {{ . }}
{{ end }}
## Completed
{{ range .CompletedSteps }}- {{ . }}
{{ end }}
## Pending
{{ range .PendingSteps }}- {{ . }}
{{ end }}
## Files Touched
{{ range .FilesTouched }}- {{ . }}
{{ end }}
## Important Decisions
{{ range .Decisions }}- {{ .Title }}: {{ .Reason }}
{{ end }}
## Recent Summary
{{ .RecentSummary }}

## Recent Transcript
{{ .RecentTranscript }}

## Last Output Snapshot
{{ .LastOutput }}
{{ if .DiffSummary }}## Current Git Diff Summary
{{ .DiffSummary }}
{{ end }}
IMPORTANT: Do not restart the task. Continue from exactly where it was left off.
Before finishing, output a structured summary of what you did and what remains.
`

type handoffData struct {
	*state.Session
	DiffSummary string
}

type textReplacement struct {
	re          *regexp.Regexp
	replacement string
}

var resumeTextReplacements = []textReplacement{
	{re: regexp.MustCompile(`(?i)\bapproaching usage limit(?:\s*[·•.-]\s*resets?(?: at)? [^\r\n]+)?`), replacement: "approaching-usage-threshold"},
	{re: regexp.MustCompile(`(?i)\byou'?ve used (\d+)% of your(?: [a-z]+)? session limit\b`), replacement: "used-session-threshold-$1%"},
	{re: regexp.MustCompile(`(?i)claude limits 5h:(\d+|na)% 7d:(\d+|na)%`), replacement: "claude-usage-status 5h:$1 7d:$2"},
	{re: regexp.MustCompile(`(?i)5h\s+(\d+)%\s*[·|]\s*weekly\s+(\d+)%`), replacement: "budget-status 5h:$1 weekly:$2"},
	{re: regexp.MustCompile(`(?i)\bused (\d+)% of (?:the )?(?:weekly |5h |five-hour )?usage\b`), replacement: "usage-at-$1%"},
	{re: regexp.MustCompile(`(?i)\b(\d+)% left\b`), replacement: "$1%-remaining-budget"},
	{re: regexp.MustCompile(`(?i)\bapproach(?:es|ing)? (\d+)% of the token limit\b`), replacement: "approaching-token-threshold-$1%"},
	{re: regexp.MustCompile(`(?i)\brate_limit_exceeded\b`), replacement: "rate-limit-exceeded"},
	{re: regexp.MustCompile(`(?i)\brate[_ ]limit\s+exceeded\b`), replacement: "rate-limit-exceeded"},
	{re: regexp.MustCompile(`(?i)\brate[_ ]limit\s+reached\b`), replacement: "rate-limit-reached"},
	{re: regexp.MustCompile(`(?i)\brate limited\b`), replacement: "rate-limited"},
	{re: regexp.MustCompile(`(?i)\brate[_ ]limit\b`), replacement: "rate-limit"},
	{re: regexp.MustCompile(`(?i)\btoo many requests\b`), replacement: "too-many-requests"},
	{re: regexp.MustCompile(`(?i)\bquota exceeded\b`), replacement: "quota-exceeded"},
	{re: regexp.MustCompile(`(?i)\busage limit exceeded\b`), replacement: "usage-limit-exceeded"},
	{re: regexp.MustCompile(`(?i)\bsession limit reached\b`), replacement: "session-limit-reached"},
	{re: regexp.MustCompile(`(?i)\bcapacity limit reached\b`), replacement: "capacity-limit-reached"},
	{re: regexp.MustCompile(`(?i)\bcapacity limit exceeded\b`), replacement: "capacity-limit-exceeded"},
}

// GenerateHandoffMD renders the handoff markdown from session state.
func GenerateHandoffMD(sess *state.Session) (string, error) {
	tmpl, err := template.New("handoff").Parse(handoffTemplate)
	if err != nil {
		return "", fmt.Errorf("parse handoff template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, sess); err != nil {
		return "", fmt.Errorf("execute handoff template: %w", err)
	}
	return buf.String(), nil
}

// GenerateResumePrompt creates the prompt to inject into the next provider.
func GenerateResumePrompt(sess *state.Session, workDir string) (string, error) {
	tmpl, err := template.New("resume").Parse(resumePromptTemplate)
	if err != nil {
		return "", fmt.Errorf("parse resume template: %w", err)
	}

	diff := sanitizeResumeText(getGitDiffSummary(workDir))

	data := handoffData{
		Session:     sanitizeResumeSession(sess),
		DiffSummary: diff,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute resume template: %w", err)
	}
	return buf.String(), nil
}

func sanitizeResumeSession(sess *state.Session) *state.Session {
	if sess == nil {
		return nil
	}

	clone := *sess
	clone.Goal = sanitizeResumeText(sess.Goal)
	clone.Constraints = sanitizeResumeStrings(sess.Constraints)
	clone.CompletedSteps = sanitizeResumeStrings(sess.CompletedSteps)
	clone.PendingSteps = sanitizeResumeStrings(sess.PendingSteps)
	clone.FilesTouched = sanitizeResumeStrings(sess.FilesTouched)
	clone.Decisions = make([]state.Decision, len(sess.Decisions))
	for i, decision := range sess.Decisions {
		clone.Decisions[i] = state.Decision{
			Title:  sanitizeResumeText(decision.Title),
			Reason: sanitizeResumeText(decision.Reason),
		}
	}
	clone.ProviderHistory = make([]state.ProviderEntry, len(sess.ProviderHistory))
	for i, entry := range sess.ProviderHistory {
		clone.ProviderHistory[i] = state.ProviderEntry{
			Provider: sanitizeResumeText(entry.Provider),
			Status:   sanitizeResumeText(entry.Status),
			At:       entry.At,
			Cycle:    entry.Cycle,
			ResetsAt: entry.ResetsAt,
		}
	}
	clone.CurrentProvider = sanitizeResumeText(sess.CurrentProvider)
	clone.RecentSummary = sanitizeResumeText(sess.RecentSummary)
	clone.RecentTranscript = sanitizeResumeText(sess.RecentTranscript)
	clone.LastOutput = sanitizeResumeText(sess.LastOutput)
	return &clone
}

func sanitizeResumeStrings(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = sanitizeResumeText(value)
	}
	return out
}

func sanitizeResumeText(text string) string {
	out := text
	for _, replacement := range resumeTextReplacements {
		out = replacement.re.ReplaceAllString(out, replacement.replacement)
	}
	return out
}

func getGitDiffSummary(dir string) string {
	cmd := exec.Command("git", "diff", "--stat")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "(no git diff available)"
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "(no changes)"
	}
	return s
}
