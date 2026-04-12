package adapter

import "testing"

func TestDetectRateLimitFromUsagePercent(t *testing.T) {
	evt := detectRateLimit("you've used 99% of your session limit · resets 3pm")
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_99%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromStructuredFiveHourStatusLine(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:96% 7d:12%")
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "five_hour_96%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromStructuredSevenDayStatusLine(t *testing.T) {
	evt := detectRateLimit("claude limits 5h:12% 7d:97%")
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "seven_day_97%" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromApproachingUsageWarning(t *testing.T) {
	evt := detectRateLimit("approaching usage limit · resets at 10am")
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "usage_warning" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDetectRateLimitFromSessionLimitReached(t *testing.T) {
	evt := detectRateLimit("session limit reached · resets 6pm")
	if evt == nil {
		t.Fatal("expected rate limit event")
	}
	if evt.RateLimit == nil || evt.RateLimit.Type != "session_limit_reached" {
		t.Fatalf("unexpected rate limit info: %#v", evt.RateLimit)
	}
}

func TestDoesNotDetectCustomProgressBar(t *testing.T) {
	evt := detectRateLimit("5h █████████▉ 99%")
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
