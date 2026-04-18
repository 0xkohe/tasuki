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

// hardRateLimitRegexes match hard-stop rate-limit messages with enough context
// to avoid tripping on normal discussion of "rate limits" in handoff prompts.
var hardRateLimitRegexes = []*regexp.Regexp{
	regexp.MustCompile(`\brate_limit_exceeded\b`),
	regexp.MustCompile(`\brate[_ ]limit(?:\s+has\s+been)?\s+(?:reached|exceeded)\b`),
	regexp.MustCompile(`\b(?:reached|exceeded|hit|hitting)\b[^\r\n]{0,40}\brate[_ ]limit\b`),
	regexp.MustCompile(`\brate limited\b`),
	regexp.MustCompile(`\byou'?ve hit your[^\r\n]{0,40}\brate[_ ]limit\b`),
	regexp.MustCompile(`\btoo many requests\b`),
	regexp.MustCompile(`\bquota exceeded\b`),
	regexp.MustCompile(`\busage limit exceeded\b`),
	regexp.MustCompile(`\bcapacity limit(?:\s+(?:reached|exceeded))\b`),
}

// usagePercentRegex matches Claude Code's built-in usage message:
// "You've used NN% of your session limit"
var usagePercentRegex = regexp.MustCompile(`you've used (\d+)% of your(?: [a-z]+)? session limit`)

// structuredRateLimitRegex matches tasuki's temporary Claude status line.
// Example: "Claude limits 5h:96% 7d:12%"
var structuredRateLimitRegex = regexp.MustCompile(`claude limits 5h:(\d+|na)% 7d:(\d+|na)%`)

// codexRemainingRegex matches older Codex status text like "47% left".
var codexRemainingRegex = regexp.MustCompile(`(?:^|[\s|·])(\d+)% left(?:\b|$)`)

// codexUsedRegex matches Codex warnings like "used 75% of the weekly usage already".
var codexUsedRegex = regexp.MustCompile(`used (\d+)% of (?:the )?(?:weekly |5h |five-hour )?usage`)

// codexStatusRegex matches Codex status text like "5h 48% · weekly 74%".
var codexStatusRegex = regexp.MustCompile(`(5h|weekly)\s+(\d+)%`)

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
	src           io.Reader
	dst           io.Writer
	events        chan Event
	capture       *passthroughCapture
	provider      string
	threshold     int
	warnThreshold int
	mu            sync.Mutex
	buf           strings.Builder // rolling window for pattern matching
	history       strings.Builder // recent plain-text output for handoff
	triggered     bool            // switch threshold — fire once
	warned        bool            // warn threshold — fire once
}

func newOutputMonitor(src io.Reader, dst io.Writer, events chan Event, capture *passthroughCapture, provider string, threshold int) *outputMonitor {
	return newOutputMonitorWithWarn(src, dst, events, capture, provider, threshold, 0)
}

func newOutputMonitorWithWarn(src io.Reader, dst io.Writer, events chan Event, capture *passthroughCapture, provider string, threshold, warn int) *outputMonitor {
	if threshold <= 0 || threshold > 100 {
		threshold = defaultSwitchThreshold
	}
	if warn < 0 || warn >= threshold {
		warn = 0
	}
	return &outputMonitor{
		src:           src,
		dst:           dst,
		events:        events,
		capture:       capture,
		provider:      provider,
		threshold:     threshold,
		warnThreshold: warn,
	}
}

// Run starts the pass-through monitoring loop. Blocks until src is closed.
func (m *outputMonitor) Run() {
	processCh := make(chan string, 128)
	var processWG sync.WaitGroup
	processWG.Add(1)
	go func() {
		defer processWG.Done()
		for chunk := range processCh {
			m.checkForRateLimit([]byte(chunk))
		}
	}()

	buf := make([]byte, 4096)
	for {
		n, err := m.src.Read(buf)
		if n > 0 {
			// Pass through to user's terminal
			_, _ = m.dst.Write(buf[:n])

			// Scan for rate limit patterns off the hot path so redraw/input stays responsive.
			processCh <- string(buf[:n])
		}
		if err != nil {
			break
		}
	}
	close(processCh)
	processWG.Wait()
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
		return
	}

	if !m.warned && m.warnThreshold > 0 {
		if evt := detectRateLimitWarning(lower, m.provider, m.warnThreshold, m.threshold); evt != nil {
			m.warned = true
			// Reset the scan buffer so the first-match regex in detectRateLimit
			// isn't anchored to the stale warn-level percentage when the switch
			// threshold is eventually crossed.
			m.buf.Reset()
			m.events <- *evt
		}
	}
}

func detectRateLimit(lower string, provider string, threshold int) *Event {
	if threshold <= 0 || threshold > 100 {
		threshold = defaultSwitchThreshold
	}

	switch provider {
	case "claude":
		// Check tasuki's structured Claude status line derived from rate_limits.
		// Use the most-recent match — status lines are re-rendered in place and
		// accumulate in the rolling scan buffer, so the latest value wins.
		if matches := lastSubmatch(structuredRateLimitRegex, lower); len(matches) > 2 {
			if pct, ok := parseUsagePercent(matches[1]); ok && pct >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "five_hour_" + matches[1] + "%",
						Cycle: "5h",
					},
				}
			}
			if pct, ok := parseUsagePercent(matches[2]); ok && pct >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "seven_day_" + matches[2] + "%",
						Cycle: "weekly",
					},
				}
			}
		}

		// Check usage percentage from Claude Code's built-in usage message.
		if matches := lastSubmatch(usagePercentRegex, lower); len(matches) > 1 {
			pct, err := strconv.Atoi(matches[1])
			if err == nil && pct >= threshold {
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "usage_" + matches[1] + "%",
						Cycle: "5h",
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
					Type:  "usage_warning",
					Cycle: "5h",
				},
			}
		}

		// Check explicit session-limit reached messages.
		if match := sessionLimitReachedRegex.FindString(lower); match != "" {
			return &Event{
				Type:    EventRateLimit,
				Content: match,
				RateLimit: &RateLimitInfo{
					Type:  "session_limit_reached",
					Cycle: "5h",
				},
			}
		}

	case "codex":
		// Current Codex UI status line shows remaining budget like
		// "5h 81% · weekly 89%". Convert that to used% so the configured
		// threshold keeps the same meaning as other providers.
		if matches := codexStatusRegex.FindAllStringSubmatch(lower, -1); len(matches) > 0 {
			for _, match := range matches {
				if len(match) < 3 {
					continue
				}
				remaining, err := strconv.Atoi(match[2])
				used := 100 - remaining
				if err != nil || used < threshold {
					continue
				}
				typeName := "usage_" + match[2] + "%"
				cycle := ""
				if match[1] == "5h" {
					typeName = "five_hour_" + match[2] + "%"
					cycle = "5h"
				}
				if match[1] == "weekly" {
					typeName = "weekly_" + match[2] + "%"
					cycle = "weekly"
				}
				return &Event{
					Type:    EventRateLimit,
					Content: match[0],
					RateLimit: &RateLimitInfo{
						Type:  typeName,
						Cycle: cycle,
					},
				}
			}
		}

		// Older Codex builds show remaining percentage. Convert that to used%
		// so the configured threshold keeps the same meaning as other providers:
		// threshold=80 means "switch after 80% used", i.e. at 20% left.
		if matches := codexRemainingRegex.FindStringSubmatch(lower); len(matches) > 1 {
			remaining, err := strconv.Atoi(matches[1])
			used := 100 - remaining
			if err == nil && used >= threshold {
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
				cycle := ""
				if strings.Contains(matches[0], "weekly") {
					cycle = "weekly"
				} else if strings.Contains(matches[0], "5h") || strings.Contains(matches[0], "five-hour") {
					cycle = "5h"
				}
				return &Event{
					Type:    EventRateLimit,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "usage_" + matches[1] + "%",
						Cycle: cycle,
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

	// 5. Check explicit hard-limit phrases as a fallback when no provider-
	// specific structured signal is available.
	if match := findHardRateLimitMatch(lower); match != "" {
		return &Event{
			Type:    EventRateLimit,
			Content: match,
			RateLimit: &RateLimitInfo{
				Type: "hard_limit",
			},
		}
	}

	return nil
}

// detectRateLimitWarning mirrors the percent-based branches of detectRateLimit
// but fires when usage sits between warn and switch thresholds. Hard-limit
// phrases (e.g. "rate limit reached") are ignored here — those belong to the
// switch stage since there is no "warning" when the limit is already hit.
func detectRateLimitWarning(lower, provider string, warn, switchT int) *Event {
	if warn <= 0 || warn >= switchT {
		return nil
	}

	inRange := func(pct int) bool { return pct >= warn && pct < switchT }

	switch provider {
	case "claude":
		if matches := lastSubmatch(structuredRateLimitRegex, lower); len(matches) > 2 {
			if pct, ok := parseUsagePercent(matches[1]); ok && inRange(pct) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "five_hour_" + matches[1] + "%",
						Cycle: "5h",
					},
				}
			}
			if pct, ok := parseUsagePercent(matches[2]); ok && inRange(pct) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "seven_day_" + matches[2] + "%",
						Cycle: "weekly",
					},
				}
			}
		}
		if matches := lastSubmatch(usagePercentRegex, lower); len(matches) > 1 {
			if pct, err := strconv.Atoi(matches[1]); err == nil && inRange(pct) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "usage_" + matches[1] + "%",
						Cycle: "5h",
					},
				}
			}
		}

	case "codex":
		if matches := codexStatusRegex.FindAllStringSubmatch(lower, -1); len(matches) > 0 {
			for _, match := range matches {
				if len(match) < 3 {
					continue
				}
				remaining, err := strconv.Atoi(match[2])
				used := 100 - remaining
				if err != nil || !inRange(used) {
					continue
				}
				typeName := "usage_" + match[2] + "%"
				cycle := ""
				if match[1] == "5h" {
					typeName = "five_hour_" + match[2] + "%"
					cycle = "5h"
				}
				if match[1] == "weekly" {
					typeName = "weekly_" + match[2] + "%"
					cycle = "weekly"
				}
				return &Event{
					Type:    EventRateLimitWarning,
					Content: match[0],
					RateLimit: &RateLimitInfo{
						Type:  typeName,
						Cycle: cycle,
					},
				}
			}
		}
		if matches := codexUsedRegex.FindStringSubmatch(lower); len(matches) > 1 {
			if used, err := strconv.Atoi(matches[1]); err == nil && inRange(used) {
				cycle := ""
				if strings.Contains(matches[0], "weekly") {
					cycle = "weekly"
				} else if strings.Contains(matches[0], "5h") || strings.Contains(matches[0], "five-hour") {
					cycle = "5h"
				}
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "usage_" + matches[1] + "%",
						Cycle: cycle,
					},
				}
			}
		}
		if matches := codexRemainingRegex.FindStringSubmatch(lower); len(matches) > 1 {
			if remaining, err := strconv.Atoi(matches[1]); err == nil && inRange(100-remaining) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "remaining_" + matches[1] + "%",
					},
				}
			}
		}

	case "copilot":
		if matches := copilotUsedRegex.FindStringSubmatch(lower); len(matches) > 2 {
			value := matches[1]
			if value == "" {
				value = matches[2]
			}
			if used, err := strconv.Atoi(value); err == nil && inRange(used) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "usage_" + value + "%",
					},
				}
			}
		}
		if matches := copilotCompactionRegex.FindStringSubmatch(lower); len(matches) > 1 {
			if used, err := strconv.Atoi(matches[1]); err == nil && inRange(used) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "usage_" + matches[1] + "%",
					},
				}
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

// LooksLikeHardRateLimitText reports whether text contains an explicit hard
// rate-limit message. This is shared by PTY monitoring and non-interactive
// error handling so they interpret provider output consistently.
func LooksLikeHardRateLimitText(text string) bool {
	return findHardRateLimitMatch(text) != ""
}

func findHardRateLimitMatch(text string) string {
	normalized := strings.ToLower(stripAnsi(text))
	for _, re := range hardRateLimitRegexes {
		if match := re.FindString(normalized); match != "" {
			return match
		}
	}
	return ""
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

// lastSubmatch returns the last submatch of re against s, or nil if no match.
// Status lines are redrawn in place and the rolling scan buffer accumulates
// successive snapshots, so "latest wins" is the correct semantic.
func lastSubmatch(re *regexp.Regexp, s string) []string {
	all := re.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
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
