package orchestrator

import (
	"strings"
	"testing"

	"github.com/0xkohe/tasuki/internal/state"
)

func TestGenerateResumePromptIncludesLastOutput(t *testing.T) {
	sess := state.NewSession("sess-1", "finish the task", "codex")
	sess.RecentSummary = "recent summary"
	sess.RecentTranscript = "user: hello\nassistant: hi"
	sess.LastOutput = "tail output"

	prompt, err := GenerateResumePrompt(sess, t.TempDir())
	if err != nil {
		t.Fatalf("GenerateResumePrompt() error = %v", err)
	}

	if !strings.Contains(prompt, "## Recent Summary\nrecent summary") {
		t.Fatalf("GenerateResumePrompt() missing recent summary:\n%s", prompt)
	}
	if !strings.Contains(prompt, "## Recent Transcript\nuser: hello\nassistant: hi") {
		t.Fatalf("GenerateResumePrompt() missing recent transcript:\n%s", prompt)
	}
	if !strings.Contains(prompt, "## Last Output Snapshot\ntail output") {
		t.Fatalf("GenerateResumePrompt() missing last output snapshot:\n%s", prompt)
	}
}

func TestGenerateResumePromptSanitizesHardLimitPhrases(t *testing.T) {
	sess := state.NewSession("sess-1", "investigate rate limit loop", "codex")
	sess.RecentSummary = "Warnings/errors: rate limit reached | too many requests | approaching usage limit · resets at 10am"
	sess.RecentTranscript = "assistant: the previous provider was rate limited; you've used 99% of your session limit"
	sess.LastOutput = "quota exceeded\nclaude limits 5h:96% 7d:12%\n5h 20% · weekly 74%\nused 82% of the weekly usage already"

	prompt, err := GenerateResumePrompt(sess, t.TempDir())
	if err != nil {
		t.Fatalf("GenerateResumePrompt() error = %v", err)
	}

	for _, raw := range []string{
		"rate limit reached",
		"too many requests",
		"rate limited",
		"quota exceeded",
		"approaching usage limit",
		"you've used 99% of your session limit",
		"claude limits 5h:96% 7d:12%",
		"5h 20% · weekly 74%",
		"used 82% of the weekly usage already",
	} {
		if strings.Contains(strings.ToLower(prompt), raw) {
			t.Fatalf("GenerateResumePrompt() leaked raw trigger phrase %q:\n%s", raw, prompt)
		}
	}
	if !strings.Contains(prompt, "rate-limit-reached") {
		t.Fatalf("GenerateResumePrompt() missing sanitized phrase:\n%s", prompt)
	}
	if !strings.Contains(prompt, "approaching-usage-threshold") {
		t.Fatalf("GenerateResumePrompt() missing sanitized usage-warning phrase:\n%s", prompt)
	}
	if !strings.Contains(prompt, "used-session-threshold-99%") {
		t.Fatalf("GenerateResumePrompt() missing sanitized usage-percent phrase:\n%s", prompt)
	}
	if !strings.Contains(prompt, "claude-usage-status 5h:96 7d:12") {
		t.Fatalf("GenerateResumePrompt() missing sanitized Claude status:\n%s", prompt)
	}
	if !strings.Contains(prompt, "budget-status 5h:20 weekly:74") {
		t.Fatalf("GenerateResumePrompt() missing sanitized Codex status:\n%s", prompt)
	}
	if !strings.Contains(prompt, "usage-at-82%") {
		t.Fatalf("GenerateResumePrompt() missing sanitized Codex usage phrase:\n%s", prompt)
	}
}

func TestGenerateHandoffMDSanitizesHardLimitPhrases(t *testing.T) {
	sess := state.NewSession("sess-1", "investigate rate limit loop", "codex")
	sess.RecentSummary = "Warnings/errors: rate limit reached | too many requests"
	sess.RecentTranscript = "assistant: you've hit your rate limit"
	sess.LastOutput = "quota exceeded"

	handoff, err := GenerateHandoffMD(sess)
	if err != nil {
		t.Fatalf("GenerateHandoffMD() error = %v", err)
	}

	for _, raw := range []string{
		"rate limit reached",
		"too many requests",
		"you've hit your rate limit",
		"quota exceeded",
	} {
		if strings.Contains(strings.ToLower(handoff), raw) {
			t.Fatalf("GenerateHandoffMD() leaked raw trigger phrase %q:\n%s", raw, handoff)
		}
	}
	if !strings.Contains(handoff, "rate-limit-reached") {
		t.Fatalf("GenerateHandoffMD() missing sanitized rate-limit phrase:\n%s", handoff)
	}
	if !strings.Contains(handoff, "too-many-requests") {
		t.Fatalf("GenerateHandoffMD() missing sanitized request phrase:\n%s", handoff)
	}
	if !strings.Contains(handoff, "rate-limit") {
		t.Fatalf("GenerateHandoffMD() missing sanitized transcript phrase:\n%s", handoff)
	}
	if !strings.Contains(handoff, "quota-exceeded") {
		t.Fatalf("GenerateHandoffMD() missing sanitized quota phrase:\n%s", handoff)
	}
}

func TestGenerateInteractiveResumePromptUsesHandoffFile(t *testing.T) {
	sess := state.NewSession("sess-1", "investigate rate limit loop", "codex")
	sess.RecentSummary = "Warnings/errors: rate limit reached | too many requests"
	sess.RecentTranscript = "assistant: you've hit your rate limit"
	sess.LastOutput = "quota exceeded"

	prompt, err := GenerateInteractiveResumePrompt(sess, t.TempDir())
	if err != nil {
		t.Fatalf("GenerateInteractiveResumePrompt() error = %v", err)
	}

	if !strings.Contains(prompt, ".tasuki/handoff.md") {
		t.Fatalf("GenerateInteractiveResumePrompt() missing handoff path:\n%s", prompt)
	}
	if strings.Contains(prompt, "## Recent Transcript") {
		t.Fatalf("GenerateInteractiveResumePrompt() should stay compact:\n%s", prompt)
	}
	for _, raw := range []string{
		"rate limit reached",
		"too many requests",
		"you've hit your rate limit",
		"quota exceeded",
	} {
		if strings.Contains(strings.ToLower(prompt), raw) {
			t.Fatalf("GenerateInteractiveResumePrompt() leaked raw trigger phrase %q:\n%s", raw, prompt)
		}
	}
}
