package adapter

import (
	"io"
	"strings"
	"testing"
)

func TestDetectRateLimitFromUsagePercent(t *testing.T) {
	evt := detectRateLimit("you've used 99% of your session limit · resets 3pm", "claude", 95)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_99%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromStructuredFiveHourStatusLine(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:96% 7d:12%", "claude", 95)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "five_hour_96%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromStructuredSevenDayStatusLine(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:12% 7d:97%", "claude", 95)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "seven_day_97%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromApproachingUsageWarning(t *testing.T) {
	evt := detectRateLimit("approaching usage limit · resets at 10am", "claude", 95)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_warning" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromSessionLimitReached(t *testing.T) {
	evt := detectRateLimit("session limit reached · resets 6pm", "claude", 95)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "session_limit_reached" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDoesNotDetectCustomProgressBar(t *testing.T) {
	evt := detectRateLimit("5h █████████▉ 99%", "claude", 95)
	if evt != nil {
		t.Fatalf("expected no event, got %#v", evt)
	}
}

func TestStripAnsiRemovesCSIAndOSC(t *testing.T) {
	input := "\x1b[31mApproaching usage limit\x1b[0m\x1b]8;;https://example.com\x07link\x1b]8;;\x07"
	got := stripAnsi(input)
	want := "Approaching usage limitlink"
	if got != want {
		t.Fatalf("stripAnsi() = %q, want %q", got, want)
	}
}

func TestSummarizeRecentOutputKeepsTailAndDropsEmptyLines(t *testing.T) {
	input := "\nline1\n\nline2\nline3\n"
	got := summarizeRecentOutput(input, 64, 2)
	want := "line2\nline3"
	if got != want {
		t.Fatalf("summarizeRecentOutput() = %q, want %q", got, want)
	}
}

func TestOutputMonitorRecentOutputUsesPlainTextHistory(t *testing.T) {
	monitor := newOutputMonitor(strings.NewReader(""), io.Discard, make(chan Event, 1), nil, "claude", 95)
	monitor.checkForRateLimit([]byte("\x1b[31mhello\x1b[0m\nworld\n"))
	got := monitor.RecentOutput()
	want := "hello\nworld"
	if got != want {
		t.Fatalf("RecentOutput() = %q, want %q", got, want)
	}
}

func TestDetectRateLimitRespectsThreshold(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:94% 7d:12%", "claude", 95)
	if evt != nil {
		t.Fatalf("expected no event, got %#v", evt)
	}
	evt = detectRateLimit("claude limits 5h:94% 7d:12%", "claude", 94)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
}

func TestDetectRateLimitForCodexRemainingPercent(t *testing.T) {
	evt := detectRateLimit("20% left", "codex", 20)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "remaining_20%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitForCodexUsedStatus(t *testing.T) {
	evt := detectRateLimit("gpt-5.4 high · context [▎ ] · 5h 48% · weekly 74%", "codex", 20)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "five_hour_48%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitForCopilotUsedPercent(t *testing.T) {
	evt := detectRateLimit("context usage 96%", "copilot", 95)
	if evt == nil {
		t.Fatal("expected copilot rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_96%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}
