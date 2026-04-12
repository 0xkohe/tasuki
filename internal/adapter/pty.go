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

// usageWarningRegex matches Claude Code warnings such as:
// "Approaching usage limit · resets at 10am"
var usageWarningRegex = regexp.MustCompile(`approaching usage limit(?:\s*[·•.-]\s*resets?(?: at)? [^\r\n]+)?`)

// sessionLimitReachedRegex matches hard-stop status messages such as:
// "Session limit reached · resets 6pm"
var sessionLimitReachedRegex = regexp.MustCompile(`session limit reached(?:\s*[·•.-]\s*resets?(?: at)? [^\r\n]+)?`)

// switchThreshold is the usage percentage at which we trigger a switch.
const switchThreshold = 95

// outputMonitor reads from src, writes to dst (pass-through), and scans for rate limit patterns.
type outputMonitor struct {
	src       io.Reader
	dst       io.Writer
	events    chan Event
	mu        sync.Mutex
	buf       strings.Builder // rolling window for pattern matching
	triggered bool            // only fire once
}

func newOutputMonitor(src io.Reader, dst io.Writer, events chan Event) *outputMonitor {
	return &outputMonitor{
		src:    src,
		dst:    dst,
		events: events,
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

	// Only scan the recent portion (last 2KB)
	text := m.buf.String()
	if len(text) > 2048 {
		text = text[len(text)-2048:]
		m.buf.Reset()
		m.buf.WriteString(text)
	}

	lower := strings.ToLower(text)

	if evt := detectRateLimit(lower); evt != nil {
		m.triggered = true
		m.events <- *evt
	}
}

func detectRateLimit(lower string) *Event {
	// 1. Check unblocked's structured Claude status line derived from rate_limits.
	if matches := structuredRateLimitRegex.FindStringSubmatch(lower); len(matches) > 2 {
		if pct, ok := parseUsagePercent(matches[1]); ok && pct >= switchThreshold {
			return &Event{
				Type:    EventRateLimit,
				Content: matches[0],
				RateLimit: &RateLimitInfo{
					Type: "five_hour_" + matches[1] + "%",
				},
			}
		}
		if pct, ok := parseUsagePercent(matches[2]); ok && pct >= switchThreshold {
			return &Event{
				Type:    EventRateLimit,
				Content: matches[0],
				RateLimit: &RateLimitInfo{
					Type: "seven_day_" + matches[2] + "%",
				},
			}
		}
	}

	// 2. Check usage percentage from Claude Code's built-in usage message.
	if matches := usagePercentRegex.FindStringSubmatch(lower); len(matches) > 1 {
		pct, err := strconv.Atoi(matches[1])
		if err == nil && pct >= switchThreshold {
			return &Event{
				Type:    EventRateLimit,
				Content: matches[0],
				RateLimit: &RateLimitInfo{
					Type: "usage_" + matches[1] + "%",
				},
			}
		}
	}

	// 3. Check Claude Code's generic near-limit warning.
	if match := usageWarningRegex.FindString(lower); match != "" {
		return &Event{
			Type:    EventRateLimit,
			Content: match,
			RateLimit: &RateLimitInfo{
				Type: "usage_warning",
			},
		}
	}

	// 4. Check explicit session-limit reached messages.
	if match := sessionLimitReachedRegex.FindString(lower); match != "" {
		return &Event{
			Type:    EventRateLimit,
			Content: match,
			RateLimit: &RateLimitInfo{
				Type: "session_limit_reached",
			},
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
