package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// Claude implements Provider for Claude Code CLI.
type Claude struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	process *os.Process
	opts    Options
}

// claudeStreamEvent represents a single JSONL event from claude --output-format stream-json.
type claudeStreamEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`

	// For type=assistant message_delta
	Delta struct {
		Type string `json:"type,omitempty"`
		Text string `json:"text,omitempty"`
	} `json:"delta,omitempty"`

	// For content_block_delta
	ContentBlock struct {
		Type string `json:"type,omitempty"`
		Text string `json:"text,omitempty"`
	} `json:"content_block,omitempty"`

	// For type=result
	Result         string  `json:"result,omitempty"`
	IsError        bool    `json:"is_error,omitempty"`
	TotalCostUSD   float64 `json:"total_cost_usd,omitempty"`
	DurationMS     int     `json:"duration_ms,omitempty"`
	NumTurns       int     `json:"num_turns,omitempty"`
	SessionID      string  `json:"session_id,omitempty"`
	StopReason     string  `json:"stop_reason,omitempty"`
	InputTokens    int     `json:"input_tokens,omitempty"`
	OutputTokens   int     `json:"output_tokens,omitempty"`
	RateLimitEvent *struct {
		Status        string `json:"status"`
		ResetsAt      int64  `json:"resetsAt"`
		RateLimitType string `json:"rateLimitType"`
		OverageStatus string `json:"overageStatus"`
	} `json:"rate_limit_event,omitempty"`
}

func NewClaude(opts Options) *Claude {
	return &Claude{opts: opts}
}

func (c *Claude) Name() string {
	return "claude"
}

func (c *Claude) IsAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

func (c *Claude) Stop() error {
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

func (c *Claude) SendInput(sess *InteractiveSession, text string) error {
	return WriteToSession(sess, text)
}

func (c *Claude) RunInteractive(ctx context.Context, workDir string, initialPrompt string) (*InteractiveSession, error) {
	override, err := prepareClaudeStatusLineOverride(workDir)
	if err != nil {
		return nil, fmt.Errorf("claude: prepare status line: %w", err)
	}
	restoreOverride := func() {}
	if override != nil {
		restoreOverride = func() {
			_ = override.Restore()
		}
	}

	args := claudeInteractiveArgs(initialPrompt, c.opts.YoloMode)

	cmd := exec.CommandContext(ctx, "claude", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	// Get terminal size
	rows, cols := uint16(24), uint16(80)
	if ws, err := pty.GetsizeFull(os.Stdin); err == nil {
		rows = uint16(ws.Rows)
		cols = uint16(ws.Cols)
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		restoreOverride()
		return nil, fmt.Errorf("claude: start pty: %w", err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.process = cmd.Process
	c.mu.Unlock()

	events := make(chan Event, 16)
	done := make(chan struct{})
	capture := newPassthroughCapture()
	inputProxy, err := startInputProxy(os.Stdin, ptmx, capture)
	if err != nil {
		ptmx.Close()
		restoreOverride()
		return nil, fmt.Errorf("claude: start input proxy: %w", err)
	}

	// Monitor output for rate limit patterns
	monitor := newOutputMonitorWithOptions(ptmx, os.Stdout, events, capture, c.Name(), monitorThresholds{
		Switch:       c.opts.SwitchThreshold,
		Warn:         c.opts.WarnThreshold,
		WeeklySwitch: c.opts.WeeklySwitchThreshold,
		WeeklyWarn:   c.opts.WeeklyWarnThreshold,
	})
	monitor.SetSize(rows, cols)
	go monitor.Run()

	// Wait for process exit
	go func() {
		_ = cmd.Wait()
		// Small delay to let monitor flush
		time.Sleep(100 * time.Millisecond)
		close(done)
	}()

	sess := &InteractiveSession{
		PTY:    ptmx,
		Events: events,
		Done:   done,
		Resize: func(rows, cols uint16) {
			monitor.SetSize(rows, cols)
			_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
		},
		Snapshot: func() PassthroughSnapshot {
			return capture.Snapshot(monitor.RecentOutput())
		},
		Close: func() error {
			restoreOverride()
			_ = inputProxy.Stop()
			ptmx.Close()
			if cmd.Process != nil {
				return cmd.Process.Kill()
			}
			return nil
		},
	}

	return sess, nil
}

// Execute runs Claude in non-interactive mode (unchanged from before).
func (c *Claude) Execute(ctx context.Context, req *Request) (<-chan Event, error) {
	prompt := req.Prompt
	if req.Context != "" {
		prompt = req.Context + "\n\n" + prompt
	}

	args := claudeExecuteArgs(prompt, c.opts.YoloMode)

	cmd := exec.CommandContext(ctx, "claude", args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: stderr pipe: %w", err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude: start: %w", err)
	}

	ch := make(chan Event, 64)

	go func() {
		defer close(ch)

		go func() {
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
			}
		}()

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var evt claudeStreamEvent
			if err := json.Unmarshal(line, &evt); err != nil {
				continue
			}

			parsed := c.parseEvent(&evt, line)
			if parsed != nil {
				ch <- *parsed
			}
		}

		_ = cmd.Wait()
	}()

	return ch, nil
}

func (c *Claude) parseEvent(evt *claudeStreamEvent, raw []byte) *Event {
	switch evt.Type {
	case "assistant":
		if evt.Subtype == "text" || evt.Delta.Text != "" {
			return &Event{
				Type:    EventMessageDelta,
				Content: evt.Delta.Text,
				Raw:     raw,
			}
		}
		return nil

	case "content_block_delta":
		text := ""
		if evt.Delta.Text != "" {
			text = evt.Delta.Text
		}
		if text == "" {
			return nil
		}
		return &Event{
			Type:    EventMessageDelta,
			Content: text,
			Raw:     raw,
		}

	case "result":
		if evt.RateLimitEvent != nil && evt.RateLimitEvent.Status != "allowed" {
			return &Event{
				Type: EventRateLimit,
				Raw:  raw,
				RateLimit: &RateLimitInfo{
					ResetsAt: evt.RateLimitEvent.ResetsAt,
					Type:     evt.RateLimitEvent.RateLimitType,
					Cycle:    claudeRateLimitCycle(evt.RateLimitEvent.RateLimitType),
				},
				Usage: &Usage{
					InputTokens:  evt.InputTokens,
					OutputTokens: evt.OutputTokens,
					CostUSD:      evt.TotalCostUSD,
				},
			}
		}

		if evt.IsError {
			return &Event{
				Type:    EventError,
				Content: evt.Result,
				Raw:     raw,
			}
		}

		return &Event{
			Type:    EventDone,
			Content: evt.Result,
			Raw:     raw,
			Usage: &Usage{
				InputTokens:  evt.InputTokens,
				OutputTokens: evt.OutputTokens,
				CostUSD:      evt.TotalCostUSD,
			},
		}

	case "error":
		return &Event{
			Type:    EventError,
			Content: string(raw),
			Raw:     raw,
		}
	}

	return nil
}

// claudeInteractiveArgs builds the argument list for `claude` in interactive mode.
func claudeInteractiveArgs(prompt string, yolo bool) []string {
	var args []string
	if yolo {
		args = append(args, "--dangerously-skip-permissions")
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	return args
}

// claudeExecuteArgs builds the argument list for `claude -p ...` in non-interactive mode.
func claudeExecuteArgs(prompt string, yolo bool) []string {
	args := []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
	if yolo {
		args = append(args, "--dangerously-skip-permissions")
	}
	return args
}

// claudeRateLimitCycle maps Claude's rateLimitType strings to the canonical cycle.
func claudeRateLimitCycle(rlType string) string {
	s := strings.ToLower(rlType)
	switch {
	case strings.Contains(s, "5_hour"), strings.Contains(s, "five_hour"), strings.Contains(s, "5h"):
		return "5h"
	case strings.Contains(s, "7_day"), strings.Contains(s, "seven_day"), strings.Contains(s, "weekly"):
		return "weekly"
	case strings.Contains(s, "monthly"):
		return "monthly"
	}
	return ""
}
