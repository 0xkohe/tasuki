package adapter

import (
	"io"
	"strings"
	"testing"
)

func TestDetectRateLimitFromUsagePercent(t *testing.T) {
	evt := detectRateLimit("you've used 99% of your session limit · resets 3pm", "claude", 95, 0)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_99%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromStructuredFiveHourStatusLine(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:96% 7d:12%", "claude", 95, 0)
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
	evt := detectRateLimit("claude limits 5h:12% 7d:97%", "claude", 95, 95)
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

// Reproduces the field report: switch_threshold=25, weekly monitoring
// disabled (weeklyThreshold=0). A 7d:49% reading must not trigger a switch
// anymore — only the 5h window matters.
func TestDetectRateLimitSkipsSevenDayWhenWeeklyDisabled(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:19% 7d:49%", "claude", 25, 0)
	if evt != nil {
		t.Fatalf("expected no event when weekly monitoring disabled, got %#v", evt)
	}
}

func TestDetectRateLimitFiresSevenDayWithWeeklyThreshold(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:19% 7d:49%", "claude", 25, 40)
	if evt == nil {
		t.Fatal("expected weekly rate-limit event when weeklyThreshold crossed")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "seven_day_49%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitDoesNotSwitchOnApproachingUsageWarning(t *testing.T) {
	evt := detectRateLimit("approaching usage limit · resets at 10am", "claude", 95, 0)
	if evt != nil {
		t.Fatalf("expected no hard-switch event, got %#v", evt)
	}
}

func TestDetectRateLimitFromSessionLimitReached(t *testing.T) {
	evt := detectRateLimit("session limit reached · resets 6pm", "claude", 95, 0)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "session_limit_reached" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromExplicitHardLimitPhrase(t *testing.T) {
	evt := detectRateLimit("Error: rate limit reached, please try again later", "claude", 95, 0)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "hard_limit" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitIgnoresPlainDiscussion(t *testing.T) {
	evt := detectRateLimit("This answer explains how rate limit handling works.", "claude", 95, 0)
	if evt != nil {
		t.Fatalf("expected no event, got %#v", evt)
	}
}

func TestDoesNotDetectCustomProgressBar(t *testing.T) {
	evt := detectRateLimit("5h █████████▉ 99%", "claude", 95, 0)
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

func TestLooksLikeHardRateLimitTextIgnoresQuotedExamples(t *testing.T) {
	text := `This should not trigger: patterns like "rate limit exceeded" / "you've hit your rate limit" can appear in explanations.`
	if LooksLikeHardRateLimitText(text) {
		t.Fatalf("expected quoted example to be ignored: %q", text)
	}
}

func TestLooksLikeHardRateLimitTextMatchesLineScopedError(t *testing.T) {
	text := "warning\nError: you've hit your rate limit, please try again later"
	if !LooksLikeHardRateLimitText(text) {
		t.Fatalf("expected explicit line-scoped error to match: %q", text)
	}
}

func TestDetectRateLimitRespectsThreshold(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:94% 7d:12%", "claude", 95, 0)
	if evt != nil {
		t.Fatalf("expected no event, got %#v", evt)
	}
	evt = detectRateLimit("claude limits 5h:94% 7d:12%", "claude", 94, 0)
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
}

func TestDetectRateLimitForCodexRemainingPercent(t *testing.T) {
	evt := detectRateLimit("20% left", "codex", 80, 0)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "remaining_20%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitForCodexRemainingPercentBelowUsedThreshold(t *testing.T) {
	evt := detectRateLimit("20% left", "codex", 81, 0)
	if evt != nil {
		t.Fatalf("expected no codex rate limit event, got %#v", evt)
	}
}

func TestDetectRateLimitForCodexStatusUsesRemainingSemantics(t *testing.T) {
	evt := detectRateLimit("gpt-5.4 high · context 100% left · 5h 20% · weekly 74%", "codex", 80, 0)
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
	evt := detectRateLimit("gpt-5.4 high · context 100% left · 5h 81% · weekly 89%", "codex", 70, 0)
	if evt != nil {
		t.Fatalf("expected no codex rate limit event, got %#v", evt)
	}
}

func TestDetectRateLimitForCodexWeeklyStatusUsesRemainingSemantics(t *testing.T) {
	evt := detectRateLimit("5h 90% · weekly 5%", "codex", 95, 95)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Cycle != "weekly" {
		t.Fatalf("Cycle = %#v, want weekly", evt.RateLimit)
	}
}

// Reproduces the codex side of the field report. With weekly monitoring
// disabled a "weekly 20%" (80% used) reading must not trigger a switch even
// though the 5h reading is 97% remaining (3% used, below threshold 25).
func TestDetectRateLimitForCodexStatusSkipsWeeklyWhenDisabled(t *testing.T) {
	evt := detectRateLimit("5h 97% · weekly 20%", "codex", 25, 0)
	if evt != nil {
		t.Fatalf("expected no event when weekly monitoring disabled, got %#v", evt)
	}
}

// Regression guard: a corrupted redraw like "5h 9% · wekly 83%" (drop of a
// single character inside "99" and "weekly" due to a chunk-boundary /
// rendering hiccup) must not match codexStatusRegex — the fresh, canonical
// "5h 99% · weekly 83%" frame that follows should take precedence.
func TestDetectRateLimitForCodexStatusIgnoresCorruptedFragment(t *testing.T) {
	buf := "5h 99% · weekly 83% ... 5h 9% · wekly 83% ... 5h 99% · weekly 83%"
	evt := detectRateLimit(buf, "codex", 25, 0)
	if evt != nil {
		t.Fatalf("expected no event — corrupted fragment must be ignored, got %#v", evt)
	}
}

// Uses lastSubmatch semantics: the latest frame's 5h reading is what gets
// compared. Buffer holds multiple past frames at safe values and one fresh
// frame at 90% used — only the latter should trigger.
func TestDetectRateLimitForCodexStatusUsesLatestFrame(t *testing.T) {
	buf := "5h 99% · weekly 83% 5h 99% · weekly 83% 5h 10% · weekly 83%"
	evt := detectRateLimit(buf, "codex", 80, 0)
	if evt == nil {
		t.Fatal("expected event from latest 5h frame")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "five_hour_10%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

// The modern Codex UI shows "Context N% left" for conversation-context
// usage, which is unrelated to the rate-limit budget. A high context usage
// (few % left) must not be treated as a rate-limit trigger.
func TestDetectRateLimitForCodexIgnoresContextLeftPrefix(t *testing.T) {
	evt := detectRateLimit("gpt-5.4 high · context 5% left", "codex", 80, 0)
	if evt != nil {
		t.Fatalf("expected no event from context-usage signal, got %#v", evt)
	}
}

func TestDetectRateLimitForCodexExplicitUsedStatus(t *testing.T) {
	evt := detectRateLimit("used 82% of the weekly usage already", "codex", 80, 80)
	if evt == nil {
		t.Fatal("expected codex rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Cycle != "weekly" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitForCodexExplicitWeeklyUsedSkippedWhenWeeklyDisabled(t *testing.T) {
	evt := detectRateLimit("used 82% of the weekly usage already", "codex", 80, 0)
	if evt != nil {
		t.Fatalf("expected no event when weekly monitoring disabled, got %#v", evt)
	}
}

func TestDetectRateLimitForCopilotUsedPercent(t *testing.T) {
	evt := detectRateLimit("context usage 96%", "copilot", 95, 0)
	if evt == nil {
		t.Fatal("expected copilot rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_96%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningBetweenThresholds(t *testing.T) {
	evt := detectRateLimitWarning("claude limits 5h:85% 7d:10%", "claude", 80, 95, 0, 0)
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
	evt := detectRateLimitWarning("approaching usage limit · resets at 10am", "claude", 80, 95, 0, 0)
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
	evt := detectRateLimitWarning("claude limits 5h:96% 7d:10%", "claude", 80, 95, 0, 0)
	if evt != nil {
		t.Fatalf("expected no warning at switch threshold, got %#v", evt)
	}
}

func TestDetectRateLimitWarningSkippedBelowWarn(t *testing.T) {
	evt := detectRateLimitWarning("claude limits 5h:50% 7d:10%", "claude", 80, 95, 0, 0)
	if evt != nil {
		t.Fatalf("expected no warning below warn threshold, got %#v", evt)
	}
}

func TestDetectRateLimitWarningSkipsWeeklyWhenDisabled(t *testing.T) {
	// 7d at 85% must not fire when weekly warn/switch are 0.
	evt := detectRateLimitWarning("claude limits 5h:10% 7d:85%", "claude", 80, 95, 0, 0)
	if evt != nil {
		t.Fatalf("expected no warning when weekly disabled, got %#v", evt)
	}
}

func TestDetectRateLimitWarningFiresWeeklyWhenEnabled(t *testing.T) {
	evt := detectRateLimitWarning("claude limits 5h:10% 7d:85%", "claude", 80, 95, 80, 95)
	if evt == nil {
		t.Fatal("expected weekly warning event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "seven_day_85%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningCodexStatusUsesRemainingSemantics(t *testing.T) {
	evt := detectRateLimitWarning("5h 20% · weekly 90%", "codex", 80, 95, 0, 0)
	if evt == nil {
		t.Fatal("expected codex warning event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Cycle != "5h" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningCodexStatusDoesNotFireWhenRemainingHigh(t *testing.T) {
	evt := detectRateLimitWarning("5h 81% · weekly 89%", "codex", 80, 95, 0, 0)
	if evt != nil {
		t.Fatalf("expected no codex warning event, got %#v", evt)
	}
}

func TestDetectRateLimitWarningCodexRemainingUsesUsedSemantics(t *testing.T) {
	evt := detectRateLimitWarning("20% left", "codex", 80, 95, 0, 0)
	if evt == nil {
		t.Fatal("expected codex warning event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "remaining_20%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitWarningCodexExplicitUsedStatus(t *testing.T) {
	evt := detectRateLimitWarning("used 82% of the weekly usage already", "codex", 80, 95, 80, 95)
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

// Regression guard for the "codex switched to copilot at 5h 97%" field
// report. Before the fix, the 2KB scan buffer could be cut mid-number so
// that "90% left" shrank to "0% left" and the regex matched via `^`. The
// buffer truncation now drops any partial leading token so this can't
// produce a false rate-limit trigger.
func TestOutputMonitorBufferTruncationIgnoresPartialLeadingToken(t *testing.T) {
	events := make(chan Event, 4)
	monitor := newOutputMonitorWithOptions(strings.NewReader(""), io.Discard, events, nil, "codex", monitorThresholds{Switch: 25})

	// Fill the buffer past the 2KB boundary with benign text that ends in a
	// healthy "90% left" status — after truncation the leading fragment
	// must not be interpreted as "0% left".
	filler := strings.Repeat("x", 2100)
	monitor.checkForRateLimit([]byte(filler + " context 90% left "))

	select {
	case evt := <-events:
		t.Fatalf("expected no rate-limit event from a mid-token buffer cut, got %#v", evt)
	default:
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
