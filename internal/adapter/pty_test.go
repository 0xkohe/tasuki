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
	if evt.RateLimit.Cycle != "5h" {
		t.Fatalf("Cycle = %q, want 5h", evt.RateLimit.Cycle)
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
	if evt.RateLimit.Cycle != "weekly" {
		t.Fatalf("Cycle = %q, want weekly", evt.RateLimit.Cycle)
	}
}

func TestDetectRateLimitDoesNotSwitchOnApproachingUsageWarning(t *testing.T) {
	evt := detectRateLimit("approaching usage limit · resets at 10am", "claude", 95)
	if evt != nil {
		t.Fatalf("expected no hard-switch event, got %#v", evt)
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

func TestDetectRateLimitFromExplicitHardLimitPhrase(t *testing.T) {
	evt := detectRateLimit("Error: rate limit reached, please try again later", "claude", 95)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "hard_limit" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitIgnoresPlainDiscussion(t *testing.T) {
	evt := detectRateLimit("This answer explains how rate limit handling works.", "claude", 95)
	if evt != nil {
		t.Fatalf("expected no event, got %#v", evt)
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

func TestLooksLikeHardRateLimitText(t *testing.T) {
	if !LooksLikeHardRateLimitText("too many requests") {
		t.Fatal("expected hard rate limit match")
	}
	if LooksLikeHardRateLimitText("Let's talk about rate limit policy changes.") {
		t.Fatal("expected plain discussion to be ignored")
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
	evt := detectRateLimit("20% left", "codex", 80)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "remaining_20%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitForCodexRemainingPercentBelowUsedThreshold(t *testing.T) {
	evt := detectRateLimit("20% left", "codex", 81)
	if evt != nil {
		t.Fatalf("expected no codex rate limit event, got %#v", evt)
	}
}

func TestDetectRateLimitForCodexStatusUsesRemainingSemantics(t *testing.T) {
	evt := detectRateLimit("gpt-5.4 high · context 100% left · 5h 20% · weekly 74%", "codex", 80)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "five_hour_20%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
	if evt.RateLimit.Cycle != "5h" {
		t.Fatalf("Cycle = %q, want 5h", evt.RateLimit.Cycle)
	}
}

func TestDetectRateLimitForCodexStatusDoesNotFireWhenRemainingAboveThreshold(t *testing.T) {
	evt := detectRateLimit("gpt-5.4 high · context 100% left · 5h 81% · weekly 89%", "codex", 70)
	if evt != nil {
		t.Fatalf("expected no codex rate limit event, got %#v", evt)
	}
}

func TestDetectRateLimitForCodexWeeklyStatusUsesRemainingSemantics(t *testing.T) {
	evt := detectRateLimit("5h 90% · weekly 5%", "codex", 95)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Cycle != "weekly" {
		t.Fatalf("Cycle = %#v, want weekly", evt.RateLimit)
	}
}

func TestDetectRateLimitForCodexExplicitUsedStatus(t *testing.T) {
	evt := detectRateLimit("used 82% of the weekly usage already", "codex", 80)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Cycle != "weekly" {
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

func TestDetectRateLimitWarningBetweenThresholds(t *testing.T) {
	evt := detectRateLimitWarning("claude limits 5h:85% 7d:10%", "claude", 80, 95)
	if evt == nil {
		t.Fatal("expected warning event")
	}
	if evt.Type != EventRateLimitWarning {
		t.Fatalf("unexpected event type: %v", evt.Type)
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "five_hour_85%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningFromApproachingUsageWarning(t *testing.T) {
	evt := detectRateLimitWarning("approaching usage limit · resets at 10am", "claude", 80, 95)
	if evt == nil {
		t.Fatal("expected warning event")
	}
	if evt.Type != EventRateLimitWarning {
		t.Fatalf("unexpected event type: %v", evt.Type)
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_warning" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningSkippedWhenAtSwitch(t *testing.T) {
	// 96% is at/above the switch threshold — warning must not fire (the
	// hard-limit detector handles it).
	evt := detectRateLimitWarning("claude limits 5h:96% 7d:10%", "claude", 80, 95)
	if evt != nil {
		t.Fatalf("expected no warning at switch threshold, got %#v", evt)
	}
}

func TestDetectRateLimitWarningSkippedBelowWarn(t *testing.T) {
	evt := detectRateLimitWarning("claude limits 5h:50% 7d:10%", "claude", 80, 95)
	if evt != nil {
		t.Fatalf("expected no warning below warn threshold, got %#v", evt)
	}
}

func TestDetectRateLimitWarningCodexStatusUsesRemainingSemantics(t *testing.T) {
	evt := detectRateLimitWarning("5h 20% · weekly 90%", "codex", 80, 95)
	if evt == nil {
		t.Fatal("expected codex warning event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Cycle != "5h" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningCodexStatusDoesNotFireWhenRemainingHigh(t *testing.T) {
	evt := detectRateLimitWarning("5h 81% · weekly 89%", "codex", 80, 95)
	if evt != nil {
		t.Fatalf("expected no codex warning event, got %#v", evt)
	}
}

func TestDetectRateLimitWarningCodexRemainingUsesUsedSemantics(t *testing.T) {
	evt := detectRateLimitWarning("20% left", "codex", 80, 95)
	if evt == nil {
		t.Fatal("expected codex warning event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "remaining_20%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningCodexExplicitUsedStatus(t *testing.T) {
	evt := detectRateLimitWarning("used 82% of the weekly usage already", "codex", 80, 95)
	if evt == nil {
		t.Fatal("expected codex warning event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Cycle != "weekly" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestOutputMonitorFiresWarningBeforeSwitch(t *testing.T) {
	events := make(chan Event, 4)
	monitor := newOutputMonitorWithWarn(strings.NewReader(""), io.Discard, events, nil, "claude", 95, 80)
	monitor.checkForRateLimit([]byte("claude limits 5h:85% 7d:10%"))
	select {
	case evt := <-events:
		if evt.Type != EventRateLimitWarning {
			t.Fatalf("expected warning, got %v", evt.Type)
		}
	default:
		t.Fatal("expected warning event to be emitted")
	}

	// Warning should fire only once.
	monitor.checkForRateLimit([]byte("claude limits 5h:86% 7d:10%"))
	select {
	case evt := <-events:
		t.Fatalf("expected no further warning event, got %v", evt.Type)
	default:
	}

	// Crossing the switch threshold still fires the RateLimit event.
	monitor.checkForRateLimit([]byte("claude limits 5h:97% 7d:10%"))
	select {
	case evt := <-events:
		if evt.Type != EventRateLimit {
			t.Fatalf("expected rate limit, got %v", evt.Type)
		}
	default:
		t.Fatal("expected rate limit event after crossing switch threshold")
	}
}

func TestOutputMonitorSwitchFiresWithoutWarnStage(t *testing.T) {
	events := make(chan Event, 2)
	monitor := newOutputMonitorWithWarn(strings.NewReader(""), io.Discard, events, nil, "claude", 95, 80)
	// Going straight over the switch threshold must not also emit a warning.
	monitor.checkForRateLimit([]byte("claude limits 5h:97% 7d:10%"))
	var types []EventType
	for {
		select {
		case evt := <-events:
			types = append(types, evt.Type)
			continue
		default:
		}
		break
	}
	if len(types) != 1 || types[0] != EventRateLimit {
		t.Fatalf("expected single rate-limit event, got %v", types)
	}
}
