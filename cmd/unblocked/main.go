package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kooooohe/unblocked/internal/config"
	"github.com/kooooohe/unblocked/internal/orchestrator"
	"github.com/kooooohe/unblocked/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func main() {
	var providerFlag string
	var pipeMode bool
	var initGlobal bool
	var resumeFlag bool
	var ignoreCooldown bool

	rootCmd := &cobra.Command{
		Use:   "unblocked [prompt]",
		Short: "AI CLI failover orchestrator",
		Long: `unblocked seamlessly switches between Claude Code, Codex CLI, and Copilot CLI
when rate limits are hit. You use each CLI's native interactive UI directly —
unblocked monitors for rate limits in the background and switches to the next
provider automatically.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			workDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getwd: %w", err)
			}

			if !config.ConfigExists(workDir) {
				if _, err := config.InteractiveInit(config.InitOptions{
					Root:   workDir,
					NonTTY: !term.IsTerminal(int(os.Stdin.Fd())),
				}); err != nil {
					return fmt.Errorf("initial setup: %w", err)
				}
			}

			cfg := config.Load(workDir)
			cfg.WorkDir = workDir

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				cancel()
			}()

			orch, err := orchestrator.New(cfg, workDir, resumeFlag, providerFlag, ignoreCooldown)
			if err != nil {
				return err
			}

			prompt := strings.Join(args, " ")

			if pipeMode {
				if prompt == "" {
					return orch.RunInteractive(ctx)
				}
				return orch.RunOnce(ctx, prompt)
			}

			// Default: full interactive PTY passthrough mode
			return orch.RunPassthrough(ctx, prompt)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.Flags().StringVarP(&providerFlag, "provider", "p", "", "preferred provider (claude, codex, copilot)")
	rootCmd.Flags().BoolVar(&pipeMode, "pipe", false, "non-interactive mode (formats output)")
	rootCmd.Flags().BoolVar(&resumeFlag, "resume", false, "resume the previous unblocked session in this project")
	rootCmd.Flags().BoolVar(&ignoreCooldown, "ignore-cooldown", false, "ignore persisted cooldown state on startup and re-evaluate providers from top priority")

	var initNonInteractive bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize unblocked configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			workDir, err := os.Getwd()
			if err != nil {
				return err
			}

			nonTTY := initNonInteractive || !term.IsTerminal(int(os.Stdin.Fd()))
			path, err := config.InteractiveInit(config.InitOptions{
				Root:   workDir,
				Global: initGlobal,
				NonTTY: nonTTY,
			})
			if err != nil {
				return err
			}
			ui.InfoMessage("initialized " + path)
			return nil
		},
	}
	initCmd.Flags().BoolVar(&initGlobal, "global", false, "write config to the global path instead of the current project")
	initCmd.Flags().BoolVar(&initNonInteractive, "non-interactive", false, "skip prompts and enable every detected CLI with default thresholds")

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show current session status",
		RunE: func(cmd *cobra.Command, args []string) error {
			workDir, err := os.Getwd()
			if err != nil {
				return err
			}
			cfg := config.Load(workDir)
			cfg.WorkDir = workDir

			orch, err := orchestrator.New(cfg, workDir, true, "", false)
			if err != nil {
				return err
			}
			ctx := context.Background()
			_ = orch.RunOnce(ctx, "/status")
			return nil
		},
	}

	rootCmd.AddCommand(initCmd, statusCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s%s%s\n", ui.Red, err, ui.Reset)
		os.Exit(1)
	}
}
