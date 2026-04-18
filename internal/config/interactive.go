package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// DetectedTool represents the result of probing one AI CLI.
type DetectedTool struct {
	Name       string // provider key: claude / codex / copilot
	Display    string // human-friendly label shown in prompts
	Available  bool
	Path       string // resolved binary path when available
	ResetCycle string // default subscription cycle for this tool
	Priority   int    // default failover priority (lower wins)
}

// DetectTools probes every supported AI CLI and returns their availability.
// The slice order doubles as the default failover priority (claude first).
func DetectTools() []DetectedTool {
	return []DetectedTool{
		detectLookPath("claude", "Claude Code", "claude", "5h", 1),
		detectLookPath("codex", "OpenAI Codex", "codex", "5h", 2),
		detectCopilot(3),
	}
}

func detectLookPath(name, display, binary, cycle string, priority int) DetectedTool {
	tool := DetectedTool{Name: name, Display: display, ResetCycle: cycle, Priority: priority}
	if path, err := exec.LookPath(binary); err == nil {
		tool.Available = true
		tool.Path = path
	}
	return tool
}

func detectCopilot(priority int) DetectedTool {
	tool := DetectedTool{Name: "copilot", Display: "GitHub Copilot (gh copilot)", ResetCycle: "monthly", Priority: priority}
	// Preferred check matches Copilot adapter: `gh copilot --version`.
	if ghPath, err := exec.LookPath("gh"); err == nil {
		cmd := exec.Command("gh", "copilot", "--version")
		if err := cmd.Run(); err == nil {
			tool.Available = true
			tool.Path = ghPath
		}
	}
	return tool
}

// ConfigExists reports whether a local or global config file is already present.
func ConfigExists(root string) bool {
	if _, err := os.Stat(LocalPath(root)); err == nil {
		return true
	}
	if _, err := os.Stat(GlobalPath()); err == nil {
		return true
	}
	return false
}

// InitOptions controls how InteractiveInit behaves.
type InitOptions struct {
	In      io.Reader
	Out     io.Writer
	Root    string
	Global  bool // when true, skip the save-location question and write to the global path
	NonTTY  bool // when true, skip prompts and enable every detected tool
	Threshold int // switch threshold percentage; 0 falls back to 80
}

// InteractiveInit walks the user through detecting installed CLIs and
// selecting which providers to enable, then writes the resulting config.
// Returns the path that was written.
func InteractiveInit(opts InitOptions) (string, error) {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Threshold <= 0 || opts.Threshold > 100 {
		opts.Threshold = 80
	}

	tools := DetectTools()

	fmt.Fprintln(opts.Out)
	fmt.Fprintln(opts.Out, "Welcome to tasuki. Running first-time setup.")
	fmt.Fprintln(opts.Out, "Detecting installed AI CLIs...")
	fmt.Fprintln(opts.Out)
	for _, t := range tools {
		if t.Available {
			fmt.Fprintf(opts.Out, "  [x] %-30s found: %s\n", t.Display, t.Path)
		} else {
			fmt.Fprintf(opts.Out, "  [ ] %-30s not found\n", t.Display)
		}
	}
	fmt.Fprintln(opts.Out)

	reader := bufio.NewReader(opts.In)

	enabled := make(map[string]bool)
	for _, t := range tools {
		defaultYes := t.Available
		if opts.NonTTY {
			enabled[t.Name] = defaultYes
			continue
		}
		question := fmt.Sprintf("Enable %s?", t.Display)
		if !t.Available {
			question = fmt.Sprintf("Enable %s (not detected)?", t.Display)
		}
		enabled[t.Name] = askYesNo(reader, opts.Out, question, defaultYes)
	}

	anyEnabled := false
	for _, v := range enabled {
		if v {
			anyEnabled = true
			break
		}
	}
	if !anyEnabled {
		return "", fmt.Errorf("no providers were selected")
	}

	saveGlobal := opts.Global
	if !opts.Global && !opts.NonTTY {
		fmt.Fprintln(opts.Out)
		fmt.Fprintln(opts.Out, "Where should the configuration be saved?")
		fmt.Fprintf(opts.Out, "  1) global  %s\n", GlobalPath())
		fmt.Fprintf(opts.Out, "  2) local   %s\n", LocalPath(opts.Root))
		choice := askChoice(reader, opts.Out, "Select", "1", []string{"1", "2"})
		saveGlobal = choice == "1"
	}

	cfg := &Config{
		SwitchThreshold: opts.Threshold,
	}
	for _, t := range tools {
		priority := t.Priority
		cfg.Providers = append(cfg.Providers, ProviderConfig{
			Name:       t.Name,
			Enabled:    enabled[t.Name],
			ResetCycle: t.ResetCycle,
			Priority:   &priority,
		})
	}

	var target string
	if saveGlobal {
		target = GlobalPath()
		if err := cfg.SaveGlobal(); err != nil {
			return "", err
		}
	} else {
		target = LocalPath(opts.Root)
		if err := cfg.SaveLocal(opts.Root); err != nil {
			return "", err
		}
	}

	fmt.Fprintln(opts.Out)
	fmt.Fprintf(opts.Out, "Wrote configuration: %s\n", target)
	fmt.Fprintf(opts.Out, "switch_threshold: %d%%\n", opts.Threshold)
	fmt.Fprintln(opts.Out)

	return target, nil
}

func askYesNo(reader *bufio.Reader, out io.Writer, question string, defaultYes bool) bool {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	for {
		fmt.Fprintf(out, "  %s %s: ", question, suffix)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return defaultYes
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "":
			return defaultYes
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Fprintln(out, "  Please answer y or n.")
	}
}

func askChoice(reader *bufio.Reader, out io.Writer, question, def string, allowed []string) string {
	for {
		fmt.Fprintf(out, "  %s [%s]: ", question, def)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return def
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			return def
		}
		for _, a := range allowed {
			if a == answer {
				return answer
			}
		}
		fmt.Fprintf(out, "  Please enter one of: %s\n", strings.Join(allowed, ", "))
	}
}
