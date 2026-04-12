package adapter

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPassthroughCaptureBuildsTranscriptAndSummary(t *testing.T) {
	capture := newPassthroughCapture()

	capture.RecordInput([]byte("こんにちは\r"))
	capture.RecordOutput("● こんにちは！何かお手伝いできますか？\n")
	capture.RecordInput([]byte("切り替わりますか？\r"))
	capture.RecordOutput("Claude limits 5h:101% 7d:55%\n")

	snapshot := capture.Snapshot("Claude limits 5h:101% 7d:55%")
	if snapshot.RecentTranscript == "" || snapshot.RecentTranscript == "(no recent transcript)" {
		t.Fatalf("expected transcript, got %q", snapshot.RecentTranscript)
	}
	if got := snapshot.RecentTranscript; got == "" || got == "(no recent transcript)" {
		t.Fatalf("unexpected transcript: %q", got)
	}
	if snapshot.Summary == "" || snapshot.Summary == "(no recent context)" {
		t.Fatalf("expected summary, got %q", snapshot.Summary)
	}
	if want := "user: こんにちは"; !contains(snapshot.RecentTranscript, want) {
		t.Fatalf("transcript missing %q: %q", want, snapshot.RecentTranscript)
	}
	if want := "assistant: Claude limits 5h:101% 7d:55%"; !contains(snapshot.RecentTranscript, want) {
		t.Fatalf("transcript missing %q: %q", want, snapshot.RecentTranscript)
	}
	if want := "Warnings/errors: Claude limits 5h:101% 7d:55%"; !contains(snapshot.Summary, want) {
		t.Fatalf("summary missing %q: %q", want, snapshot.Summary)
	}
}

func TestPassthroughCaptureHandlesBackspace(t *testing.T) {
	capture := newPassthroughCapture()
	capture.RecordInput([]byte("helo"))
	capture.RecordInput([]byte{0x7f})
	capture.RecordInput([]byte("lo\r"))

	snapshot := capture.Snapshot("")
	if want := "user: hello"; !contains(snapshot.RecentTranscript, want) {
		t.Fatalf("transcript missing %q: %q", want, snapshot.RecentTranscript)
	}
}

func TestInputProxyStopsCleanly(t *testing.T) {
	srcR, srcW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer srcR.Close()
	defer srcW.Close()

	dstR, dstW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer dstR.Close()
	defer dstW.Close()

	capture := newPassthroughCapture()
	proxy, err := startInputProxy(srcR, dstW, capture)
	if err != nil {
		t.Fatalf("startInputProxy() error = %v", err)
	}

	if _, err := srcW.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, len("hello\r"))
	if _, err := io.ReadFull(dstR, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if got := string(buf); got != "hello\r" {
		t.Fatalf("forwarded input = %q, want %q", got, "hello\r")
	}

	if err := proxy.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if _, err := srcW.Write([]byte("later")); err != nil {
		t.Fatal(err)
	}
	if err := dstW.Close(); err != nil {
		t.Fatal(err)
	}

	rest, err := io.ReadAll(dstR)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("unexpected forwarded bytes after stop: %q", string(rest))
	}

	snapshot := capture.Snapshot("")
	if want := "user: hello"; !contains(snapshot.RecentTranscript, want) {
		t.Fatalf("transcript missing %q: %q", want, snapshot.RecentTranscript)
	}
}

func contains(s string, substr string) bool {
	return strings.Contains(s, substr)
}
