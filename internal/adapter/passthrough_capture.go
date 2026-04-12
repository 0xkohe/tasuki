package adapter

import (
	"io"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type transcriptEntry struct {
	role string
	text string
}

type passthroughCapture struct {
	mu             sync.Mutex
	entries        []transcriptEntry
	currentInput   []rune
	inputPending   []byte
	outputPartial  string
	skipEscape     bool
	lastOutputLine string
}

func newPassthroughCapture() *passthroughCapture {
	return &passthroughCapture{}
}

func (c *passthroughCapture) RecordInput(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.inputPending = append(c.inputPending, data...)
	for len(c.inputPending) > 0 {
		b := c.inputPending[0]

		if c.skipEscape {
			c.inputPending = c.inputPending[1:]
			if b >= '@' && b <= '~' {
				c.skipEscape = false
			}
			continue
		}

		switch {
		case b == 0x1b:
			c.skipEscape = true
			c.inputPending = c.inputPending[1:]
		case b == '\r' || b == '\n':
			c.commitInput()
			c.inputPending = c.inputPending[1:]
		case b == 0x7f || b == 0x08:
			if len(c.currentInput) > 0 {
				c.currentInput = c.currentInput[:len(c.currentInput)-1]
			}
			c.inputPending = c.inputPending[1:]
		case b < 0x20 || b == 0:
			c.inputPending = c.inputPending[1:]
		case b < utf8.RuneSelf:
			c.currentInput = append(c.currentInput, rune(b))
			c.inputPending = c.inputPending[1:]
		default:
			r, size := utf8.DecodeRune(c.inputPending)
			if r == utf8.RuneError && size == 1 {
				return
			}
			c.currentInput = append(c.currentInput, r)
			c.inputPending = c.inputPending[size:]
		}
	}
}

func (c *passthroughCapture) RecordOutput(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	combined := c.outputPartial + text
	lines := strings.Split(combined, "\n")
	for _, line := range lines[:len(lines)-1] {
		c.commitOutputLine(line)
	}
	c.outputPartial = lines[len(lines)-1]
}

func (c *passthroughCapture) Snapshot(recentOutput string) PassthroughSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	entries := append([]transcriptEntry(nil), c.entries...)
	if pending := strings.TrimSpace(c.outputPartial); pending != "" && shouldKeepOutputLine(pending) {
		entries = append(entries, transcriptEntry{role: "assistant", text: normalizeWhitespace(pending)})
	}

	transcript := formatTranscript(entries, 24)
	return PassthroughSnapshot{
		RecentOutput:     recentOutput,
		RecentTranscript: transcript,
		Summary:          summarizeTranscript(entries, recentOutput),
	}
}

type inputProxy struct {
	src  *os.File
	dst  io.Writer
	stop *os.File
	wake *os.File
	done chan struct{}
}

func startInputProxy(src *os.File, dst io.Writer, capture *passthroughCapture) (*inputProxy, error) {
	stop, wake, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	p := &inputProxy{
		src:  src,
		dst:  dst,
		stop: stop,
		wake: wake,
		done: make(chan struct{}),
	}

	go p.run(capture)
	return p, nil
}

func (p *inputProxy) run(capture *passthroughCapture) {
	defer close(p.done)
	defer p.stop.Close()

	buf := make([]byte, 1024)
	fds := []unix.PollFd{
		{Fd: int32(p.src.Fd()), Events: unix.POLLIN},
		{Fd: int32(p.stop.Fd()), Events: unix.POLLIN | unix.POLLHUP},
	}

	for {
		_, err := unix.Poll(fds, -1)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}

		if fds[1].Revents != 0 {
			return
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
			continue
		}

		n, err := p.src.Read(buf)
		if n > 0 {
			if capture != nil {
				capture.RecordInput(buf[:n])
			}
			if _, writeErr := p.dst.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *inputProxy) Stop() error {
	var err error
	if p.wake != nil {
		_, err = p.wake.Write([]byte{1})
		_ = p.wake.Close()
		p.wake = nil
	}
	<-p.done
	return err
}

func (c *passthroughCapture) commitInput() {
	text := strings.TrimSpace(string(c.currentInput))
	c.currentInput = c.currentInput[:0]
	if text == "" {
		return
	}
	c.addEntry("user", text)
}

func (c *passthroughCapture) commitOutputLine(line string) {
	line = strings.TrimSpace(line)
	if !shouldKeepOutputLine(line) {
		return
	}
	line = normalizeWhitespace(line)
	if line == "" || line == c.lastOutputLine {
		return
	}
	c.lastOutputLine = line
	c.addEntry("assistant", line)
}

func (c *passthroughCapture) addEntry(role string, text string) {
	c.entries = append(c.entries, transcriptEntry{role: role, text: text})
	if len(c.entries) > 200 {
		c.entries = c.entries[len(c.entries)-200:]
	}
}

func shouldKeepOutputLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "❯") || strings.HasPrefix(line, ">") {
		return false
	}
	if onlyUIChrome(line) {
		return false
	}
	return true
}

func onlyUIChrome(line string) bool {
	hasLetter := false
	for _, r := range line {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			hasLetter = true
		case unicode.IsSpace(r):
		case strings.ContainsRune("│╭╮╰╯─━═▐▛▜▌▝▘", r):
		default:
			return false
		}
	}
	return !hasLetter
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func formatTranscript(entries []transcriptEntry, maxEntries int) string {
	if len(entries) == 0 {
		return "(no recent transcript)"
	}
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}

	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, entry.role+": "+entry.text)
	}
	return strings.Join(lines, "\n")
}

func summarizeTranscript(entries []transcriptEntry, recentOutput string) string {
	if len(entries) == 0 && strings.TrimSpace(recentOutput) == "" {
		return "(no recent context)"
	}

	userLines := collectTail(entries, "user", 3)
	assistantLines := collectTail(entries, "assistant", 5)
	alerts := collectAlerts(entries, recentOutput)

	var sections []string
	if len(userLines) > 0 {
		sections = append(sections, "Recent user requests: "+strings.Join(userLines, " | "))
	}
	if len(assistantLines) > 0 {
		sections = append(sections, "Recent assistant output: "+strings.Join(assistantLines, " | "))
	}
	if len(alerts) > 0 {
		sections = append(sections, "Warnings/errors: "+strings.Join(alerts, " | "))
	}
	if len(sections) == 0 {
		return "(no significant recent context)"
	}
	return strings.Join(sections, "\n")
}

func collectTail(entries []transcriptEntry, role string, n int) []string {
	var out []string
	for i := len(entries) - 1; i >= 0 && len(out) < n; i-- {
		if entries[i].role != role {
			continue
		}
		out = append([]string{entries[i].text}, out...)
	}
	return out
}

func collectAlerts(entries []transcriptEntry, recentOutput string) []string {
	keywords := []string{"error", "failed", "rate limit", "limit reached", "usage limit", "warning", "quota"}
	seen := map[string]struct{}{}
	var alerts []string

	check := func(text string) {
		lower := strings.ToLower(text)
		if strings.HasPrefix(lower, "claude limits ") {
			if _, ok := seen[text]; ok {
				return
			}
			seen[text] = struct{}{}
			alerts = append(alerts, text)
			return
		}
		for _, keyword := range keywords {
			if strings.Contains(lower, keyword) {
				if _, ok := seen[text]; ok {
					return
				}
				seen[text] = struct{}{}
				alerts = append(alerts, text)
				return
			}
		}
	}

	for _, entry := range entries {
		check(entry.text)
	}
	for _, line := range strings.Split(recentOutput, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			check(line)
		}
	}

	if len(alerts) > 3 {
		alerts = alerts[len(alerts)-3:]
	}
	return alerts
}
