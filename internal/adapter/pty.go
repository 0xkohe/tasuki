package adapter

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/creack/pty"
)

// exactRateLimitPatterns are literal strings that indicate a hard rate limit.
var exactRateLimitPatterns = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"quota exceeded",
	"usage limit exceeded",
	"capacity limit",
	"limit reached",
	"try again later",
	"you've hit your",
}

// usagePercentRegex matches Claude Code's built-in usage message:
// "You've used NN% of your session limit"
var usagePercentRegex = regexp.MustCompile(`you've used (\d+)% of your(?: [a-z]+)? session limit`)

// structuredRateLimitRegex matches unblocked's temporary Claude status line.
// Example: "Claude limits 5h:96% 7d:12%"
var structuredRateLimitRegex = regexp.MustCompile(`claude limits 5h:(\d+|na)% 7d:(\d+|na)%`)

// codexRemainingRegex matches Codex status text like "47% left".
var codexRemainingRegex = regexp.MustCompile(`(?:^|[\s|·])(\d+)% left(?:\b|$)`)

// codexUsedRegex matches Codex warnings like "used 75% of the weekly usage already".
var codexUsedRegex = regexp.MustCompile(`used (\d+)% of (?:the )?(?:weekly |5h |five-hour )?usage`)

// usageWarningRegex matches Claude Code warnings such as:
// "Approaching usage limit · resets at 10am"
var usageWarningRegex = regexp.MustCompile(`approaching usage limit(?:\s*[·•.-]\s*resets?(?: at)? [^\r\n]+)?`)

// sessionLimitReachedRegex matches hard-stop status messages such as:
// "Session limit reached · resets 6pm"
var sessionLimitReachedRegex = regexp.MustCompile(`session limit reached(?:\s*[·•.-]\s*resets?(?: at)? [^\r\n]+)?`)

// copilotUsedRegex matches Copilot context/token status text where the percentage is already "used".
var copilotUsedRegex = regexp.MustCompile(`(?:(?:context|token)(?: window| usage| limit)?[^0-9\r\n]{0,20}(\d+)%|(\d+)%[^a-z\r\n]{0,8}(?:used|usage|full))`)

// copilotCompactionRegex matches Copilot's documented auto-compaction threshold wording.
var copilotCompactionRegex = regexp.MustCompile(`approach(?:es|ing)? (\d+)% of the token limit`)

const defaultSwitchThreshold = 95

// outputMonitor reads from src, writes to dst (pass-through), and scans for rate limit patterns.
type outputMonitor struct {
	src       io.Reader
	dst       io.Writer
	events    chan Event
	capture   *passthroughCapture
	provider  string
	threshold int
	mu        sync.Mutex
	buf       strings.Builder // rolling window for pattern matching
	history   strings.Builder // recent plain-text output for handoff
	triggered bool            // only fire once
}

func newOutputMonitor(src io.Reader, dst io.Writer, events chan Event, capture *passthroughCapture, provider string, threshold int) *outputMonitor {
	if threshold <= 0 || threshold > 100 {
		threshold = defaultSwitchThreshold
	}
	return &outputMonitor{
		src:       src,
		dst:       dst,
		events:    events,
		capture:   capture,
		provider:  provider,
		threshold: threshold,
	}
}

// Run starts the pass-through monitoring loop. Blocks until src is closed.
func (m *outputMonitor) Run() {
	buf := make([]byte, 4096)
	for {
		n, err := m.src.Read(buf)
		if n > 0 {
			// Pass through to user's terminal
			_, _ = m.dst.Write(buf[:n])

			// Scan for rate limit patterns
			m.checkForRateLimit(buf[:n])
		}
		if err != nil {
			break
		}
	}
}

func (m *outputMonitor) checkForRateLimit(data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.triggered {
		return
	}

	// Strip ANSI escape codes for pattern matching
	clean := stripAnsi(string(data))
	m.buf.WriteString(clean)
	appendBounded(&m.history, clean, 16384)
	if m.capture != nil {
		m.capture.RecordOutput(clean)
	}

	// Only scan the recent portion (last 2KB)
	text := m.buf.String()
	if len(text) > 2048 {
		text = text[len(text)-2048:]
		m.buf.Reset()
		m.buf.WriteString(text)
	}

	lower := strings.ToLower(text)

	if evt := detectRateLimit(lower, m.provider, m.threshold); evt != nil {
		m.triggered = true
		m.events <- *evt
	}
}

func detectRateLimit(lower string, provider string, threshold int) *Event {
	if threshold <= 0 || threshold > 100 {
		threshold = defaultSwitchThreshold
	}

	switch provider {
	case "claude":
		// Check unblocked's structured Claude status line derived from rate_limits.
		if matches := structuredRateLimitRegex.FindStringSubmatch(lower); len(matches) > 2 {
			if pct, ok := parseUsagePercent(matches[1]); ok && pct >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "five_hour_" + matches[1] + "%",
					},
				}
			}
			if pct, ok := parseUsagePercent(matches[2]); ok && pct >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "seven_day_" + matches[2] + "%",
					},
				}
			}
		}

		// Check usage percentage from Claude Code's built-in usage message.
		if matches := usagePercentRegex.FindStringSubmatch(lower); len(matches) > 1 {
			pct, err := strconv.Atoi(matches[1])
			if err == nil && pct >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "usage_" + matches[1] + "%",
					},
				}
			}
		}

		// Check Claude Code's generic near-limit warning.
		if match := usageWarningRegex.FindString(lower); match != "" {
			return &Event{
				Type:    EventRateLimit,
				Content: match,
				RateLimit: &RateLimitInfo{
					Type: "usage_warning",
				},
			}
		}

		// Check explicit session-limit reached messages.
		if match := sessionLimitReachedRegex.FindString(lower); match != "" {
			return &Event{
				Type:    EventRateLimit,
				Content: match,
				RateLimit: &RateLimitInfo{
					Type: "session_limit_reached",
				},
			}
		}

	case "codex":
		// Codex shows remaining percentage, so switch when the displayed value
		// falls at or below the configured threshold.
		if matches := codexRemainingRegex.FindStringSubmatch(lower); len(matches) > 1 {
			remaining, err := strconv.Atoi(matches[1])
			if err == nil && remaining <= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "remaining_" + matches[1] + "%",
					},
				}
			}
		}
		if matches := codexUsedRegex.FindStringSubmatch(lower); len(matches) > 1 {
			used, err := strconv.Atoi(matches[1])
			if err == nil && used >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "usage_" + matches[1] + "%",
					},
				}
			}
		}

	case "copilot":
		// Copilot's context display is treated as used%.
		if matches := copilotUsedRegex.FindStringSubmatch(lower); len(matches) > 2 {
			value := matches[1]
			if value == "" {
				value = matches[2]
			}
			used, err := strconv.Atoi(value)
			if err == nil && used >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "usage_" + value + "%",
					},
				}
			}
		}
		if matches := copilotCompactionRegex.FindStringSubmatch(lower); len(matches) > 1 {
			used, err := strconv.Atoi(matches[1])
			if err == nil && used >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "usage_" + matches[1] + "%",
					},
				}
			}
		}
	}

	// 5. Check exact rate limit phrases (hard limit fallback).
	for _, pattern := range exactRateLimitPatterns {
		if strings.Contains(lower, pattern) {
			return &Event{
				Type:    EventRateLimit,
				Content: pattern,
				RateLimit: &RateLimitInfo{
					Type: "hard_limit",
				},
			}
		}
	}

	return nil
}

func (m *outputMonitor) RecentOutput() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return summarizeRecentOutput(m.history.String(), 4000, 40)
}

func appendBounded(dst *strings.Builder, chunk string, max int) {
	dst.WriteString(chunk)
	if dst.Len() <= max {
		return
	}

	text := dst.String()
	text = text[len(text)-max:]
	dst.Reset()
	dst.WriteString(text)
}

func summarizeRecentOutput(text string, maxChars int, maxLines int) string {
	lines := strings.Split(text, "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return ""
	}
	if len(kept) > maxLines {
		kept = kept[len(kept)-maxLines:]
	}

	summary := strings.Join(kept, "\n")
	if len(summary) <= maxChars {
		return summary
	}
	return summary[len(summary)-maxChars:]
}

func parseUsagePercent(s string) (int, bool) {
	if s == "" || s == "na" {
		return 0, false
	}
	pct, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return pct, true
}

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\033' {
			i++
			if i < len(s) && s[i] == '[' {
				// CSI sequence: ESC [ ... final_byte
				i++
				for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
					i++
				}
				if i < len(s) {
					i++
				}
			} else if i < len(s) && s[i] == ']' {
				// OSC sequence: ESC ] ... ST (BEL or ESC \)
				i++
				for i < len(s) {
					if s[i] == '\007' { // BEL
						i++
						break
					}
					if s[i] == '\033' && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			} else if i < len(s) {
				// Other escape: skip one char
				i++
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

// startPTY spawns a command in a pseudo-terminal and returns the pty file and process.
func startPTY(name string, args []string, dir string, rows, cols uint16) (*os.File, *os.Process, error) {
	cmd := commandContext(name, args, dir)

	size := &pty.Winsize{
		Rows: rows,
		Cols: cols,
	}

	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, nil, err
	}

	return ptmx, cmd.Process, nil
}
