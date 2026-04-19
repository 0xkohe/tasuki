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

// Copilot implements Provider for GitHub Copilot CLI.
type Copilot struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	process *os.Process
	opts    Options
}

// copilotEvent represents a single JSONL event from gh copilot -p --output-format json.
type copilotEvent struct {
	Type string `json:"type"`

	Data *struct {
		MessageID    string `json:"messageId,omitempty"`
		DeltaContent string `json:"deltaContent,omitempty"`
		Content      string `json:"content,omitempty"`
	} `json:"data,omitempty"`

	SessionID string `json:"sessionId,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	Usage     *struct {
		InputTokens     int `json:"inputTokens,omitempty"`
		OutputTokens    int `json:"outputTokens,omitempty"`
		ReasoningTokens int `json:"reasoningTokens,omitempty"`
	} `json:"usage,omitempty"`
}

func NewCopilot(opts Options) *Copilot {
	return &Copilot{opts: opts}
}

func (c *Copilot) Name() string {
	return "copilot"
}

func (c *Copilot) IsAvailable() bool {
	cmd := exec.Command("gh", "copilot", "--version")
	return cmd.Run() == nil
}

func (c *Copilot) Stop() error {
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

func (c *Copilot) SendInput(sess *InteractiveSession, text string) error {
	return WriteToSession(sess, text)
}

func (c *Copilot) RunInteractive(ctx context.Context, workDir string, initialPrompt string) (*InteractiveSession, error) {
	args := copilotInteractiveArgs(initialPrompt, c.opts.YoloMode)

	cmd := exec.CommandContext(ctx, "gh", args...)
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
		return nil, fmt.Errorf("copilot: start pty: %w", err)
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
		return nil, fmt.Errorf("copilot: start input proxy: %w", err)
	}

	monitor := newOutputMonitorWithOptions(ptmx, os.Stdout, events, capture, c.Name(), monitorThresholds{
		Switch:       c.opts.SwitchThreshold,
		Warn:         c.opts.WarnThreshold,
		WeeklySwitch: c.opts.WeeklySwitchThreshold,
		WeeklyWarn:   c.opts.WeeklyWarnThreshold,
	})
	monitor.SetSize(rows, cols)
	go monitor.Run()

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
			monitor.SetSize(rows, cols)
			_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
		},
		Snapshot: func() PassthroughSnapshot {
			return capture.Snapshot(monitor.RecentOutput())
		},
		Close: func() error {
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

// Execute runs Copilot in non-interactive mode.
func (c *Copilot) Execute(ctx context.Context, req *Request) (<-chan Event, error) {
	prompt := req.Prompt
	if req.Context != "" {
		prompt = req.Context + "\n\n" + prompt
	}

	args := copilotExecuteArgs(prompt, c.opts.YoloMode)

	cmd := exec.CommandContext(ctx, "gh", args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("copilot: stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("copilot: stderr pipe: %w", err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("copilot: start: %w", err)
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

			var evt copilotEvent
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
					Type: "copilot_rate_limit",
				},
			}
			return
		}

		if exitErr != nil {
			ch <- Event{
				Type:    EventError,
				Content: fmt.Sprintf("copilot exited with error: %v\nstderr: %s", exitErr, errText),
			}
		}
	}()

	return ch, nil
}

func (c *Copilot) parseEvent(evt *copilotEvent, raw []byte) *Event {
	switch evt.Type {
	case "assistant.message_delta":
		if evt.Data != nil && evt.Data.DeltaContent != "" {
			return &Event{
				Type:    EventMessageDelta,
				Content: evt.Data.DeltaContent,
				Raw:     raw,
			}
		}

	case "assistant.message":
		if evt.Data != nil && evt.Data.Content != "" {
			return &Event{
				Type:    EventMessageDelta,
				Content: evt.Data.Content,
				Raw:     raw,
			}
		}

	case "result":
		var usage *Usage
		if evt.Usage != nil {
			usage = &Usage{
				InputTokens:  evt.Usage.InputTokens,
				OutputTokens: evt.Usage.OutputTokens,
			}
		}
		if evt.ExitCode != 0 {
			return &Event{
				Type:    EventError,
				Content: fmt.Sprintf("copilot exited with code %d", evt.ExitCode),
				Raw:     raw,
				Usage:   usage,
			}
		}
		return &Event{
			Type:  EventDone,
			Raw:   raw,
			Usage: usage,
		}

	case "assistant.turn_end":
		return &Event{
			Type: EventTurnComplete,
			Raw:  raw,
		}
	}

	return nil
}

// copilotInteractiveArgs builds the argument list for `gh copilot` in interactive mode.
func copilotInteractiveArgs(prompt string, yolo bool) []string {
	args := []string{"copilot"}
	if prompt != "" {
		args = append(args, "-i", prompt)
	}
	if yolo {
		args = append(args, "--allow-all-tools")
	}
	return args
}

// copilotExecuteArgs builds the argument list for `gh copilot -p ...` in non-interactive mode.
// --allow-all-tools is required for Copilot's automated flow and is always added.
func copilotExecuteArgs(prompt string, yolo bool) []string {
	_ = yolo
	return []string{"copilot", "-p", prompt, "--output-format", "json", "--allow-all-tools"}
}
