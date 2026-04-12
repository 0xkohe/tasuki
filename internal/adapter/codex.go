package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// Codex implements Provider for OpenAI Codex CLI.
type Codex struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	process *os.Process
}

// codexEvent represents a single JSONL event from codex exec --json.
type codexEvent struct {
	Type string `json:"type"`

	Item *struct {
		ID   string `json:"id,omitempty"`
		Type string `json:"type,omitempty"`
		Text string `json:"text,omitempty"`
	} `json:"item,omitempty"`

	Usage *struct {
		InputTokens       int `json:"input_tokens,omitempty"`
		CachedInputTokens int `json:"cached_input_tokens,omitempty"`
		OutputTokens      int `json:"output_tokens,omitempty"`
	} `json:"usage,omitempty"`

	Error *struct {
		Message string `json:"message,omitempty"`
		Code    string `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

func NewCodex() *Codex {
	return &Codex{}
}

func (c *Codex) Name() string {
	return "codex"
}

func (c *Codex) IsAvailable() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

func (c *Codex) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process != nil {
		return c.process.Kill()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return nil
}

func (c *Codex) SendInput(sess *InteractiveSession, text string) error {
	return WriteToSession(sess, text)
}

func (c *Codex) RunInteractive(ctx context.Context, workDir string, initialPrompt string) (*InteractiveSession, error) {
	args := []string{}
	if initialPrompt != "" {
		args = append(args, initialPrompt)
	}

	cmd := exec.CommandContext(ctx, "codex", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	rows, cols := uint16(24), uint16(80)
	if ws, err := pty.GetsizeFull(os.Stdin); err == nil {
		rows = uint16(ws.Rows)
		cols = uint16(ws.Cols)
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, fmt.Errorf("codex: start pty: %w", err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.process = cmd.Process
	c.mu.Unlock()

	events := make(chan Event, 16)
	done := make(chan struct{})

	monitor := newOutputMonitor(ptmx, os.Stdout, events)
	go monitor.Run()

	go func() {
		_, _ = io.Copy(ptmx, os.Stdin)
	}()

	go func() {
		_ = cmd.Wait()
		time.Sleep(100 * time.Millisecond)
		close(done)
	}()

	sess := &InteractiveSession{
		PTY:    ptmx,
		Events: events,
		Done:   done,
		Resize: func(rows, cols uint16) {
			_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
		},
		Close: func() error {
			ptmx.Close()
			if cmd.Process != nil {
				return cmd.Process.Kill()
			}
			return nil
		},
	}

	return sess, nil
}

// Execute runs Codex in non-interactive mode.
func (c *Codex) Execute(ctx context.Context, req *Request) (<-chan Event, error) {
	prompt := req.Prompt
	if req.Context != "" {
		prompt = req.Context + "\n\n" + prompt
	}

	args := []string{
		"exec",
		prompt,
		"--json",
		"--full-auto",
	}

	cmd := exec.CommandContext(ctx, "codex", args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stderr pipe: %w", err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start: %w", err)
	}

	ch := make(chan Event, 64)

	go func() {
		defer close(ch)

		var stderrBuf strings.Builder
		go func() {
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				stderrBuf.WriteString(scanner.Text() + "\n")
			}
		}()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var evt codexEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}

			parsed := c.parseEvent(&evt, line)
			if parsed != nil {
				ch <- *parsed
			}
		}

		exitErr := cmd.Wait()

		errText := stderrBuf.String()
		if isRateLimitError(errText) {
			ch <- Event{
				Type:    EventRateLimit,
				Content: errText,
				RateLimit: &RateLimitInfo{
					Type: "codex_rate_limit",
				},
			}
			return
		}

		if exitErr != nil {
			ch <- Event{
				Type:    EventError,
				Content: fmt.Sprintf("codex exited with error: %v\nstderr: %s", exitErr, errText),
			}
		}
	}()

	return ch, nil
}

func (c *Codex) parseEvent(evt *codexEvent, raw []byte) *Event {
	switch evt.Type {
	case "item.completed":
		if evt.Item != nil && evt.Item.Text != "" {
			return &Event{
				Type:    EventMessageDelta,
				Content: evt.Item.Text,
				Raw:     raw,
			}
		}

	case "turn.completed":
		var usage *Usage
		if evt.Usage != nil {
			usage = &Usage{
				InputTokens:  evt.Usage.InputTokens,
				OutputTokens: evt.Usage.OutputTokens,
			}
		}
		return &Event{
			Type:  EventTurnComplete,
			Raw:   raw,
			Usage: usage,
		}

	case "error":
		content := ""
		if evt.Error != nil {
			content = evt.Error.Message
			if evt.Error.Code == "rate_limit_exceeded" || strings.Contains(content, "rate limit") {
				return &Event{
					Type:    EventRateLimit,
					Content: content,
					Raw:     raw,
					RateLimit: &RateLimitInfo{
						Type: "codex_rate_limit",
					},
				}
			}
		}
		return &Event{
			Type:    EventError,
			Content: content,
			Raw:     raw,
		}
	}

	return nil
}

func isRateLimitError(text string) bool {
	lower := strings.ToLower(text)
	patterns := []string{
		"rate limit",
		"rate_limit",
		"too many requests",
		"429",
		"quota exceeded",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
