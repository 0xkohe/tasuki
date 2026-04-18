package adapter

import (
	"reflect"
	"testing"
)

func TestClaudeInteractiveArgsYolo(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		yolo   bool
		want   []string
	}{
		{"no prompt, no yolo", "", false, nil},
		{"prompt only", "hi", false, []string{"hi"}},
		{"yolo only", "", true, []string{"--dangerously-skip-permissions"}},
		{"yolo with prompt", "hi", true, []string{"--dangerously-skip-permissions", "hi"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeInteractiveArgs(tc.prompt, tc.yolo)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaudeExecuteArgsYolo(t *testing.T) {
	got := claudeExecuteArgs("hi", true)
	want := []string{"-p", "hi", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	gotNo := claudeExecuteArgs("hi", false)
	for _, a := range gotNo {
		if a == "--dangerously-skip-permissions" {
			t.Fatalf("non-yolo execute args must not contain bypass flag: %q", gotNo)
		}
	}
}

func TestCodexInteractiveArgsYolo(t *testing.T) {
	tests := []struct {
		name      string
		prompt    string
		preserve  bool
		yolo      bool
		want      []string
	}{
		{"bare", "", false, false, nil},
		{"yolo bare", "", false, true, []string{"--yolo"}},
		{"preserve + yolo + prompt", "hi", true, true, []string{"--no-alt-screen", "--yolo", "hi"}},
		{"prompt only", "hi", false, false, []string{"hi"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := codexInteractiveArgs(tc.prompt, tc.preserve, tc.yolo)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCodexExecuteArgsYoloSwapsFullAuto(t *testing.T) {
	noYolo := codexExecuteArgs("hi", false)
	wantNo := []string{"exec", "hi", "--json", "--full-auto"}
	if !reflect.DeepEqual(noYolo, wantNo) {
		t.Fatalf("no-yolo args = %q, want %q", noYolo, wantNo)
	}
	yolo := codexExecuteArgs("hi", true)
	wantYolo := []string{"exec", "hi", "--json", "--dangerously-bypass-approvals-and-sandbox"}
	if !reflect.DeepEqual(yolo, wantYolo) {
		t.Fatalf("yolo args = %q, want %q", yolo, wantYolo)
	}
}

func TestCopilotInteractiveArgsYolo(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		yolo   bool
		want   []string
	}{
		{"bare", "", false, []string{"copilot"}},
		{"prompt only", "hi", false, []string{"copilot", "-i", "hi"}},
		{"yolo only", "", true, []string{"copilot", "--allow-all-tools"}},
		{"yolo with prompt", "hi", true, []string{"copilot", "-i", "hi", "--allow-all-tools"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := copilotInteractiveArgs(tc.prompt, tc.yolo)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
