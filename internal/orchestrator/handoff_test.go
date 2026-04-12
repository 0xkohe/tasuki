package orchestrator

import (
	"strings"
	"testing"

	"github.com/kooooohe/unblocked/internal/state"
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
