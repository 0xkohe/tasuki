package adapter

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
)

// hardRateLimitRegexes match hard-stop rate-limit messages with enough context
// to avoid tripping on normal discussion of "rate limits" in handoff prompts.
var hardRateLimitRegexes = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\brate_limit_exceeded\b`),
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\brate[_ ]limit(?:\s+has\s+been)?\s+(?:reached|exceeded)\b`),
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\brate limited\b`),
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\byou'?ve hit your[^\r\n]{0,40}\brate[_ ]limit\b`),
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\btoo many requests\b`),
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\bquota exceeded\b`),
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\busage limit exceeded\b`),
	regexp.MustCompile(`(?:^|[\r\n])\s*(?:error[: ]+)?\bcapacity limit(?:\s+(?:reached|exceeded))\b`),
}

// usagePercentRegex matches Claude Code's built-in usage message:
// "You've used NN% of your session limit"
var usagePercentRegex = regexp.MustCompile(`you've used (\d+)% of your(?: [a-z]+)? session limit`)

// structuredRateLimitRegex matches tasuki's temporary Claude status line.
// Example: "Claude limits 5h:96% 7d:12%"
var structuredRateLimitRegex = regexp.MustCompile(`claude limits 5h:(\d+|na)% 7d:(\d+|na)%`)

// codexRemainingRegex matches older Codex status text like "47% left".
// Callers must guard against matches preceded by "context" — the modern
// Codex UI uses "Context N% left" for conversation-context usage, which is
// unrelated to the rate-limit budget this detector tracks.
var codexRemainingRegex = regexp.MustCompile(`(?:^|[\s|·])(\d+)% left(?:\b|$)`)

// codexUsedRegex matches Codex warnings like "used 75% of the weekly usage already".
var codexUsedRegex = regexp.MustCompile(`used (\d+)% of (?:the )?(?:weekly |5h |five-hour )?usage`)

// codexStatusRegex matches the full Codex status line. The canonical form
// sits at the bottom of the UI as "… · 5h N% · weekly N%", so we require a
// leading separator ("·" or "|") — or start of buffer — immediately before
// "5h". Without that anchor, source-code, diff output, or chat transcript
// that contains the same textual pattern (e.g. a test fixture literal like
// `"5h 15% · weekly 83%"` surfaced when the provider edits this file) would
// otherwise match and fire a spurious failover.
//
// Requiring both windows in a single canonical arrangement also keeps a
// partially corrupted redraw from false-firing when ANSI / chunk-boundary
// hiccups drop characters ("5h 9% · wekly 83%").
var codexStatusRegex = regexp.MustCompile(`(?i)(?:^|[·|])\s*5h\s+(\d+)%\s*[·|]\s*weekly\s+(\d+)%`)

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

const (
	defaultScreenRows     = 24
	defaultScreenCols     = 80
	statusStabilityCount  = 2
	statusStabilityWindow = 200 * time.Millisecond
	screenRowsGeneral     = 8
	screenRowsCodexStatus = 3
)

type pendingDetection struct {
	key       string
	firstSeen time.Time
	seenCount int
}

type terminalScreen struct {
	rows int
	cols int

	cells [][]rune
	row   int
	col   int

	savedRow int
	savedCol int

	escState screenEscapeState
	csiBuf   []byte
	oscEsc   bool
}

type screenEscapeState int

const (
	screenStateText screenEscapeState = iota
	screenStateEsc
	screenStateCSI
	screenStateOSC
)

// outputMonitor reads from src, writes to dst (pass-through), and scans for rate limit patterns.
type outputMonitor struct {
	src             io.Reader
	dst             io.Writer
	events          chan Event
	capture         *passthroughCapture
	provider        string
	threshold       int // 5h / generic switch threshold
	warnThreshold   int // 5h / generic warn threshold
	weeklyThreshold int // weekly switch threshold; 0 disables weekly monitoring
	weeklyWarn      int // weekly warn threshold; 0 disables weekly warning
	mu              sync.Mutex
	buf             strings.Builder // rolling window for pattern matching
	history         strings.Builder // recent plain-text output for handoff
	screen          *terminalScreen
	triggered       bool // switch threshold — fire once
	warned          bool // warn threshold — fire once
	pendingSwitch   *pendingDetection
	pendingWarn     *pendingDetection
}

func newTerminalScreen(rows, cols int) *terminalScreen {
	if rows <= 0 {
		rows = defaultScreenRows
	}
	if cols <= 0 {
		cols = defaultScreenCols
	}
	s := &terminalScreen{}
	s.resize(rows, cols)
	return s
}

func (s *terminalScreen) resize(rows, cols int) {
	if rows <= 0 {
		rows = defaultScreenRows
	}
	if cols <= 0 {
		cols = defaultScreenCols
	}

	oldRows := s.rows
	oldCols := s.cols
	oldCells := s.cells

	s.rows = rows
	s.cols = cols
	s.cells = make([][]rune, rows)
	for i := range s.cells {
		s.cells[i] = make([]rune, cols)
		for j := range s.cells[i] {
			s.cells[i][j] = ' '
		}
	}

	if oldRows == 0 || oldCols == 0 {
		s.row = 0
		s.col = 0
		return
	}

	copyRows := minInt(oldRows, rows)
	rowOffsetOld := maxInt(0, oldRows-copyRows)
	rowOffsetNew := maxInt(0, rows-copyRows)
	for r := 0; r < copyRows; r++ {
		copyCols := minInt(oldCols, cols)
		copy(s.cells[rowOffsetNew+r][:copyCols], oldCells[rowOffsetOld+r][:copyCols])
	}

	if s.row >= rows {
		s.row = rows - 1
	}
	if s.col >= cols {
		s.col = cols - 1
	}
	if s.savedRow >= rows {
		s.savedRow = rows - 1
	}
	if s.savedCol >= cols {
		s.savedCol = cols - 1
	}
}

func (s *terminalScreen) apply(data []byte) {
	for i := 0; i < len(data); {
		b := data[i]
		switch s.escState {
		case screenStateText:
			switch b {
			case 0x1b:
				s.escState = screenStateEsc
				i++
				continue
			case '\r':
				s.col = 0
				i++
				continue
			case '\n':
				s.lineFeed()
				i++
				continue
			case '\b':
				if s.col > 0 {
					s.col--
				}
				i++
				continue
			case '\t':
				next := ((s.col / 8) + 1) * 8
				if next >= s.cols {
					s.col = s.cols - 1
				} else {
					s.col = next
				}
				i++
				continue
			}
			if b < 0x20 || b == 0x7f {
				i++
				continue
			}
			r, size := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && size == 1 {
				r = rune(b)
			}
			s.writeRune(r)
			i += size
		case screenStateEsc:
			switch b {
			case '[':
				s.escState = screenStateCSI
				s.csiBuf = s.csiBuf[:0]
			case ']':
				s.escState = screenStateOSC
				s.oscEsc = false
			case '7':
				s.savedRow = s.row
				s.savedCol = s.col
				s.escState = screenStateText
			case '8':
				s.row = clampInt(s.savedRow, 0, s.rows-1)
				s.col = clampInt(s.savedCol, 0, s.cols-1)
				s.escState = screenStateText
			default:
				s.escState = screenStateText
			}
			i++
		case screenStateCSI:
			s.csiBuf = append(s.csiBuf, b)
			i++
			if b >= 0x40 && b <= 0x7e {
				s.applyCSI(s.csiBuf)
				s.csiBuf = s.csiBuf[:0]
				s.escState = screenStateText
			}
		case screenStateOSC:
			i++
			if b == 0x07 {
				s.escState = screenStateText
				s.oscEsc = false
				continue
			}
			if s.oscEsc && b == '\\' {
				s.escState = screenStateText
				s.oscEsc = false
				continue
			}
			s.oscEsc = b == 0x1b
		}
	}
}

func (s *terminalScreen) applyCSI(seq []byte) {
	if len(seq) == 0 {
		return
	}
	final := seq[len(seq)-1]
	params := string(seq[:len(seq)-1])
	params = strings.TrimPrefix(params, "?")
	values := parseCSIParams(params)

	switch final {
	case 'A':
		s.row = clampInt(s.row-defaultCSIValue(values, 0, 1), 0, s.rows-1)
	case 'B':
		s.row = clampInt(s.row+defaultCSIValue(values, 0, 1), 0, s.rows-1)
	case 'C':
		s.col = clampInt(s.col+defaultCSIValue(values, 0, 1), 0, s.cols-1)
	case 'D':
		s.col = clampInt(s.col-defaultCSIValue(values, 0, 1), 0, s.cols-1)
	case 'E':
		s.row = clampInt(s.row+defaultCSIValue(values, 0, 1), 0, s.rows-1)
		s.col = 0
	case 'F':
		s.row = clampInt(s.row-defaultCSIValue(values, 0, 1), 0, s.rows-1)
		s.col = 0
	case 'G':
		s.col = clampInt(defaultCSIValue(values, 0, 1)-1, 0, s.cols-1)
	case 'H', 'f':
		s.row = clampInt(defaultCSIValue(values, 0, 1)-1, 0, s.rows-1)
		s.col = clampInt(defaultCSIValue(values, 1, 1)-1, 0, s.cols-1)
	case 'd':
		s.row = clampInt(defaultCSIValue(values, 0, 1)-1, 0, s.rows-1)
	case 'J':
		s.eraseDisplay(defaultCSIValue(values, 0, 0))
	case 'K':
		s.eraseLine(defaultCSIValue(values, 0, 0))
	case 'X':
		count := defaultCSIValue(values, 0, 1)
		for i := 0; i < count && s.col+i < s.cols; i++ {
			s.cells[s.row][s.col+i] = ' '
		}
	case 'm':
		// SGR styling does not affect text content.
	case 's':
		s.savedRow = s.row
		s.savedCol = s.col
	case 'u':
		s.row = clampInt(s.savedRow, 0, s.rows-1)
		s.col = clampInt(s.savedCol, 0, s.cols-1)
	}
}

func (s *terminalScreen) eraseDisplay(mode int) {
	switch mode {
	case 1:
		for r := 0; r <= s.row; r++ {
			end := s.cols
			if r == s.row {
				end = s.col + 1
			}
			for c := 0; c < end && c < s.cols; c++ {
				s.cells[r][c] = ' '
			}
		}
	case 2:
		for r := range s.cells {
			for c := range s.cells[r] {
				s.cells[r][c] = ' '
			}
		}
	default:
		for r := s.row; r < s.rows; r++ {
			start := 0
			if r == s.row {
				start = s.col
			}
			for c := start; c < s.cols; c++ {
				s.cells[r][c] = ' '
			}
		}
	}
}

func (s *terminalScreen) eraseLine(mode int) {
	switch mode {
	case 1:
		for c := 0; c <= s.col && c < s.cols; c++ {
			s.cells[s.row][c] = ' '
		}
	case 2:
		for c := 0; c < s.cols; c++ {
			s.cells[s.row][c] = ' '
		}
	default:
		for c := s.col; c < s.cols; c++ {
			s.cells[s.row][c] = ' '
		}
	}
}

func (s *terminalScreen) writeRune(r rune) {
	if s.rows == 0 || s.cols == 0 {
		return
	}
	if s.col >= s.cols {
		s.col = 0
		s.lineFeed()
	}
	s.cells[s.row][s.col] = r
	s.col++
	if s.col >= s.cols {
		s.col = 0
		s.lineFeed()
	}
}

func (s *terminalScreen) lineFeed() {
	if s.row == s.rows-1 {
		copy(s.cells[0:], s.cells[1:])
		s.cells[s.rows-1] = make([]rune, s.cols)
		for i := range s.cells[s.rows-1] {
			s.cells[s.rows-1][i] = ' '
		}
		return
	}
	s.row++
}

func (s *terminalScreen) regionText(lastRows int) string {
	if lastRows <= 0 || s.rows == 0 {
		return ""
	}
	if lastRows > s.rows {
		lastRows = s.rows
	}
	start := s.rows - lastRows
	lines := make([]string, 0, lastRows)
	for _, row := range s.cells[start:] {
		line := strings.TrimRight(string(row), " ")
		line = strings.TrimRight(line, "\x00")
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func newOutputMonitor(src io.Reader, dst io.Writer, events chan Event, capture *passthroughCapture, provider string, threshold int) *outputMonitor {
	return newOutputMonitorWithOptions(src, dst, events, capture, provider, monitorThresholds{Switch: threshold})
}

func newOutputMonitorWithWarn(src io.Reader, dst io.Writer, events chan Event, capture *passthroughCapture, provider string, threshold, warn int) *outputMonitor {
	return newOutputMonitorWithOptions(src, dst, events, capture, provider, monitorThresholds{Switch: threshold, Warn: warn})
}

// monitorThresholds bundles the per-cycle thresholds the monitor cares about.
// Weekly entries at 0 disable weekly monitoring for the claude/codex status
// lines that distinguish 5h and weekly windows.
type monitorThresholds struct {
	Switch       int
	Warn         int
	WeeklySwitch int
	WeeklyWarn   int
}

func newOutputMonitorWithOptions(src io.Reader, dst io.Writer, events chan Event, capture *passthroughCapture, provider string, t monitorThresholds) *outputMonitor {
	if t.Switch <= 0 || t.Switch > 100 {
		t.Switch = defaultSwitchThreshold
	}
	if t.Warn < 0 || t.Warn >= t.Switch {
		t.Warn = 0
	}
	if t.WeeklySwitch < 0 || t.WeeklySwitch > 100 {
		t.WeeklySwitch = 0
	}
	if t.WeeklyWarn < 0 || t.WeeklySwitch <= 0 || t.WeeklyWarn >= t.WeeklySwitch {
		t.WeeklyWarn = 0
	}
	return &outputMonitor{
		src:             src,
		dst:             dst,
		events:          events,
		capture:         capture,
		provider:        provider,
		threshold:       t.Switch,
		warnThreshold:   t.Warn,
		weeklyThreshold: t.WeeklySwitch,
		weeklyWarn:      t.WeeklyWarn,
		screen:          newTerminalScreen(defaultScreenRows, defaultScreenCols),
	}
}

func (m *outputMonitor) SetSize(rows, cols uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.screen == nil {
		m.screen = newTerminalScreen(int(rows), int(cols))
		return
	}
	m.screen.resize(int(rows), int(cols))
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

	if m.screen != nil {
		m.screen.apply(data)
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
		// The cut point can land mid-token (e.g. between "9" and "0" of
		// "90% left"), leaving a leading fragment that would otherwise be
		// matched as a standalone value ("0% left" → remaining 0 → false
		// positive rate-limit trigger). Drop everything up to the first
		// separator so partial tokens can't anchor a match.
		if i := strings.IndexAny(text, " \t\r\n·|"); i > 0 {
			text = text[i:]
		}
		m.buf.Reset()
		m.buf.WriteString(text)
	}

	lower := strings.ToLower(text)
	screenText := ""
	if m.screen != nil {
		screenText = m.screen.regionText(screenRowsForProvider(m.provider))
	}
	screenLower := strings.ToLower(screenText)

	if evt := detectRateLimitFromSources(screenLower, lower, m.provider, m.threshold, m.weeklyThreshold); evt != nil {
		if m.confirmStable(&m.pendingSwitch, evt) {
			m.triggered = true
			m.pendingWarn = nil
			m.events <- *evt
		}
		return
	}
	m.pendingSwitch = nil

	if !m.warned && (m.warnThreshold > 0 || m.weeklyWarn > 0) {
		if evt := detectRateLimitWarningFromSources(screenLower, lower, m.provider, m.warnThreshold, m.threshold, m.weeklyWarn, m.weeklyThreshold); evt != nil {
			if m.confirmStable(&m.pendingWarn, evt) {
				m.warned = true
				// Reset the scan buffer so the first-match regex in detectRateLimit
				// isn't anchored to the stale warn-level percentage when the switch
				// threshold is eventually crossed.
				m.buf.Reset()
				m.events <- *evt
			}
			return
		}
	}
	m.pendingWarn = nil
}

func detectRateLimit(lower string, provider string, threshold, weeklyThreshold int) *Event {
	return detectRateLimitFromSources(lower, lower, provider, threshold, weeklyThreshold)
}

func detectRateLimitFromSources(screenLower, lower string, provider string, threshold, weeklyThreshold int) *Event {
	if threshold <= 0 || threshold > 100 {
		threshold = defaultSwitchThreshold
	}
	// weeklyThreshold of 0 (or negative) means weekly monitoring is disabled.
	if weeklyThreshold < 0 || weeklyThreshold > 100 {
		weeklyThreshold = 0
	}

	switch provider {
	case "claude":
		// Check tasuki's structured Claude status line derived from rate_limits.
		// Use the most-recent match — status lines are re-rendered in place and
		// accumulate in the rolling scan buffer, so the latest value wins.
		if matches := lastSubmatchFromSources(structuredRateLimitRegex, screenLower, lower); len(matches) > 2 {
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
			if weeklyThreshold > 0 {
				if pct, ok := parseUsagePercent(matches[2]); ok && pct >= weeklyThreshold {
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
		}

		// Check usage percentage from Claude Code's built-in usage message.
		if matches := lastSubmatchFromSources(usagePercentRegex, screenLower, lower); len(matches) > 1 {
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

		// Check explicit session-limit reached messages.
		if match := findStringFromSources(sessionLimitReachedRegex, screenLower, lower); match != "" {
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
		// "5h 81% · weekly 89%". We only trust the most-recent full match:
		// old redraws accumulate in the scan buffer and an occasional
		// character drop ("5h 9% · wekly 83%") would otherwise fire on a
		// stale fragment. lastSubmatch returns the final, freshest frame.
		if matches := lastSubmatch(codexStatusRegex, screenLower); len(matches) >= 3 {
			if fhRemaining, err := strconv.Atoi(matches[1]); err == nil {
				if used := 100 - fhRemaining; used >= threshold {
					return &Event{
						Type:    EventRateLimit,
						Content: matches[0],
						RateLimit: &RateLimitInfo{
							Type:  "five_hour_" + matches[1] + "%",
							Cycle: "5h",
						},
					}
				}
			}
			if weeklyThreshold > 0 {
				if wkRemaining, err := strconv.Atoi(matches[2]); err == nil {
					if used := 100 - wkRemaining; used >= weeklyThreshold {
						return &Event{
							Type:    EventRateLimit,
							Content: matches[0],
							RateLimit: &RateLimitInfo{
								Type:  "weekly_" + matches[2] + "%",
								Cycle: "weekly",
							},
						}
					}
				}
			}
		}

		// Older Codex builds show remaining percentage. Convert that to used%
		// so the configured threshold keeps the same meaning as other providers:
		// threshold=80 means "switch after 80% used", i.e. at 20% left.
		// No cycle metadata on this signal — always compared against the 5h
		// threshold (weekly monitoring opt-in doesn't apply here). Skip any
		// match preceded by "context" since modern Codex UI uses
		// "Context N% left" for conversation-context usage — that's not a
		// rate-limit signal.
		if matches := findCodexRemainingMatch(screenLower); matches != nil {
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
		if matches := firstSubmatchFromSources(codexUsedRegex, screenLower, lower); len(matches) > 1 {
			if used, err := strconv.Atoi(matches[1]); err == nil {
				cycle := ""
				if strings.Contains(matches[0], "weekly") {
					cycle = "weekly"
				} else if strings.Contains(matches[0], "5h") || strings.Contains(matches[0], "five-hour") {
					cycle = "5h"
				}
				effectiveT := threshold
				eligible := true
				if cycle == "weekly" {
					if weeklyThreshold <= 0 {
						eligible = false
					}
					effectiveT = weeklyThreshold
				}
				if eligible && used >= effectiveT {
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
		}

	case "copilot":
		// Copilot's context display is treated as used%.
		if matches := firstSubmatchFromSources(copilotUsedRegex, screenLower, lower); len(matches) > 2 {
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
		if matches := firstSubmatchFromSources(copilotCompactionRegex, screenLower, lower); len(matches) > 1 {
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
	if match := findHardRateLimitMatchFromSources(screenLower, lower); match != "" {
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
//
// warn/switchT cover the 5h / generic window; weeklyWarn/weeklySwitch cover
// the weekly window. A weekly threshold of 0 disables weekly warnings.
func detectRateLimitWarning(lower, provider string, warn, switchT, weeklyWarn, weeklySwitch int) *Event {
	return detectRateLimitWarningFromSources(lower, lower, provider, warn, switchT, weeklyWarn, weeklySwitch)
}

func detectRateLimitWarningFromSources(screenLower, lower, provider string, warn, switchT, weeklyWarn, weeklySwitch int) *Event {
	fiveHourEligible := warn > 0 && warn < switchT
	weeklyEligible := weeklyWarn > 0 && weeklySwitch > 0 && weeklyWarn < weeklySwitch
	if !fiveHourEligible && !weeklyEligible {
		return nil
	}

	inRange := func(pct, w, s int) bool { return pct >= w && pct < s }
	fiveHourInRange := func(pct int) bool {
		return fiveHourEligible && inRange(pct, warn, switchT)
	}
	weeklyInRange := func(pct int) bool {
		return weeklyEligible && inRange(pct, weeklyWarn, weeklySwitch)
	}

	switch provider {
	case "claude":
		if matches := lastSubmatchFromSources(structuredRateLimitRegex, screenLower, lower); len(matches) > 2 {
			if pct, ok := parseUsagePercent(matches[1]); ok && fiveHourInRange(pct) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type:  "five_hour_" + matches[1] + "%",
						Cycle: "5h",
					},
				}
			}
			if pct, ok := parseUsagePercent(matches[2]); ok && weeklyInRange(pct) {
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
		if matches := lastSubmatchFromSources(usagePercentRegex, screenLower, lower); len(matches) > 1 {
			if pct, err := strconv.Atoi(matches[1]); err == nil && fiveHourInRange(pct) {
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
		if fiveHourEligible {
			if match := findStringFromSources(usageWarningRegex, screenLower, lower); match != "" {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: match,
					RateLimit: &RateLimitInfo{
						Type:  "usage_warning",
						Cycle: "5h",
					},
				}
			}
		}

	case "codex":
		if matches := lastSubmatch(codexStatusRegex, screenLower); len(matches) >= 3 {
			if fhRemaining, err := strconv.Atoi(matches[1]); err == nil {
				if used := 100 - fhRemaining; fiveHourInRange(used) {
					return &Event{
						Type:    EventRateLimitWarning,
						Content: matches[0],
						RateLimit: &RateLimitInfo{
							Type:  "five_hour_" + matches[1] + "%",
							Cycle: "5h",
						},
					}
				}
			}
			if wkRemaining, err := strconv.Atoi(matches[2]); err == nil {
				if used := 100 - wkRemaining; weeklyInRange(used) {
					return &Event{
						Type:    EventRateLimitWarning,
						Content: matches[0],
						RateLimit: &RateLimitInfo{
							Type:  "weekly_" + matches[2] + "%",
							Cycle: "weekly",
						},
					}
				}
			}
		}
		if matches := firstSubmatchFromSources(codexUsedRegex, screenLower, lower); len(matches) > 1 {
			if used, err := strconv.Atoi(matches[1]); err == nil {
				cycle := ""
				if strings.Contains(matches[0], "weekly") {
					cycle = "weekly"
				} else if strings.Contains(matches[0], "5h") || strings.Contains(matches[0], "five-hour") {
					cycle = "5h"
				}
				eligible := false
				if cycle == "weekly" {
					eligible = weeklyInRange(used)
				} else {
					eligible = fiveHourInRange(used)
				}
				if eligible {
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
		}
		if matches := findCodexRemainingMatch(screenLower); matches != nil {
			if remaining, err := strconv.Atoi(matches[1]); err == nil && fiveHourInRange(100-remaining) {
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
		if matches := firstSubmatchFromSources(copilotUsedRegex, screenLower, lower); len(matches) > 2 {
			value := matches[1]
			if value == "" {
				value = matches[2]
			}
			if used, err := strconv.Atoi(value); err == nil && fiveHourInRange(used) {
				return &Event{
					Type:    EventRateLimitWarning,
					Content: matches[0],
					RateLimit: &RateLimitInfo{
						Type: "usage_" + value + "%",
					},
				}
			}
		}
		if matches := firstSubmatchFromSources(copilotCompactionRegex, screenLower, lower); len(matches) > 1 {
			if used, err := strconv.Atoi(matches[1]); err == nil && fiveHourInRange(used) {
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

func (m *outputMonitor) confirmStable(pending **pendingDetection, evt *Event) bool {
	if evt == nil {
		*pending = nil
		return false
	}
	if !requiresStableObservation(evt) {
		*pending = nil
		return true
	}

	key := pendingKey(evt)
	now := time.Now()
	if *pending == nil || (*pending).key != key {
		*pending = &pendingDetection{
			key:       key,
			firstSeen: now,
			seenCount: 1,
		}
		return false
	}

	(*pending).seenCount++
	if (*pending).seenCount >= statusStabilityCount || now.Sub((*pending).firstSeen) >= statusStabilityWindow {
		*pending = nil
		return true
	}
	return false
}

func requiresStableObservation(evt *Event) bool {
	if evt == nil || evt.RateLimit == nil {
		return false
	}
	switch evt.RateLimit.Type {
	case "usage_warning", "session_limit_reached", "hard_limit", "codex_rate_limit":
		return false
	default:
		return true
	}
}

func pendingKey(evt *Event) string {
	if evt == nil {
		return ""
	}
	key := strconv.Itoa(int(evt.Type))
	if evt.RateLimit != nil {
		key += "|" + stabilitySignalKey(evt.RateLimit.Type) + "|" + evt.RateLimit.Cycle
		return key
	}
	return key + "|" + evt.Content
}

func stabilitySignalKey(rateLimitType string) string {
	switch {
	case strings.HasPrefix(rateLimitType, "five_hour_"):
		return "five_hour"
	case strings.HasPrefix(rateLimitType, "seven_day_"):
		return "seven_day"
	case strings.HasPrefix(rateLimitType, "weekly_"):
		return "weekly"
	case strings.HasPrefix(rateLimitType, "usage_"):
		return "usage"
	case strings.HasPrefix(rateLimitType, "remaining_"):
		return "remaining"
	default:
		return rateLimitType
	}
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
			return strings.TrimSpace(match)
		}
	}
	return ""
}

func findHardRateLimitMatchFromSources(screenLower, lower string) string {
	for _, candidate := range []string{screenLower, lower} {
		if match := findHardRateLimitMatch(candidate); match != "" {
			return match
		}
	}
	return ""
}

func lastSubmatchFromSources(re *regexp.Regexp, primary, fallback string) []string {
	if matches := lastSubmatch(re, primary); len(matches) > 0 {
		return matches
	}
	if fallback == primary {
		return nil
	}
	return lastSubmatch(re, fallback)
}

func firstSubmatchFromSources(re *regexp.Regexp, primary, fallback string) []string {
	if matches := re.FindStringSubmatch(primary); len(matches) > 0 {
		return matches
	}
	if fallback == primary {
		return nil
	}
	return re.FindStringSubmatch(fallback)
}

func findStringFromSources(re *regexp.Regexp, primary, fallback string) string {
	if match := re.FindString(primary); match != "" {
		return match
	}
	if fallback == primary {
		return ""
	}
	return re.FindString(fallback)
}

func screenRowsForProvider(provider string) int {
	switch provider {
	case "codex":
		return screenRowsCodexStatus
	default:
		return screenRowsGeneral
	}
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

// findCodexRemainingMatch finds the most recent "N% left" reading in the
// rolling scan buffer, while excluding hits that belong to Codex's
// conversation-context UI ("Context N% left"). Context usage is tracked by
// Codex separately from the rate-limit budget and must not trigger failover.
func findCodexRemainingMatch(lower string) []string {
	indexes := codexRemainingRegex.FindAllStringSubmatchIndex(lower, -1)
	if len(indexes) == 0 {
		return nil
	}
	for i := len(indexes) - 1; i >= 0; i-- {
		idx := indexes[i]
		start := idx[0]
		windowStart := start - 16
		if windowStart < 0 {
			windowStart = 0
		}
		if strings.Contains(lower[windowStart:start], "context") {
			continue
		}
		return []string{
			lower[idx[0]:idx[1]],
			lower[idx[2]:idx[3]],
		}
	}
	return nil
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

func parseCSIParams(params string) []int {
	if params == "" {
		return nil
	}
	parts := strings.Split(params, ";")
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			values = append(values, 0)
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			values = append(values, 0)
			continue
		}
		values = append(values, value)
	}
	return values
}

func defaultCSIValue(values []int, index, fallback int) int {
	if index >= len(values) || values[index] == 0 {
		return fallback
	}
	return values[index]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
