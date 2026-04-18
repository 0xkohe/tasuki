package orchestrator

import (
	"bytes"
	"fmt"
	"os/exec"
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

	diff := getGitDiffSummary(workDir)

	data := handoffData{
		Session:     sess,
		DiffSummary: diff,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute resume template: %w", err)
	}
	return buf.String(), nil
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
