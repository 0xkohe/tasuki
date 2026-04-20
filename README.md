# tasuki

AI CLI failover orchestrator — keep working when one provider rate-limits you.

`tasuki` wraps several AI coding CLIs behind a single entrypoint, runs whichever
one you pick in a fully interactive PTY, and automatically hands the session off
to the next available provider when the current one hits a rate limit or usage
cap. You use each CLI's native UI exactly as you would on its own; `tasuki`
only steps in when a switch is needed, carrying context forward so the next
provider can pick up where the previous one left off.

## Supported AI services

At least one of the following CLIs must be installed and authenticated on the
host. The name in parentheses is the internal provider name used in config and
CLI flags.

- **Claude Code** (`claude`) — https://github.com/anthropics/claude-code
- **Codex CLI** (`codex`) — https://github.com/openai/codex
- **GitHub Copilot CLI** (`copilot`) — https://github.com/github/gh-copilot

`tasuki` does not ship credentials or talk to any provider directly. It shells
out to whichever CLI is selected and relays stdin/stdout through a PTY.

## Requirements

- Go 1.26 or newer (only for building from source).
- One or more of the supported CLIs above, already installed and logged in.

## Install

From source:

```sh
go install github.com/0xkohe/tasuki/cmd/tasuki@latest
```

Or build locally from a clone:

```sh
git clone https://github.com/0xkohe/tasuki.git
cd tasuki
go build ./cmd/tasuki
./tasuki --help
```

## Quick start

Run `tasuki` from any project directory. On first run it walks you through
an interactive init to pick which providers to enable and writes
`.tasuki/config.yaml` into the current project:

```sh
cd path/to/your/project
tasuki
```


If the active provider hits its switch threshold, `tasuki` transparently
migrates the session to the next-priority provider that is not in cooldown.

## Usage

```
tasuki [flags] [prompt]
tasuki init [--global] [--non-interactive]
tasuki status
```

Root-command flags:

| Flag | Description |
| --- | --- |
| `-p`, `--provider <name>` | Force a specific provider (`claude`, `codex`, or `copilot`). |
| `--pipe` | Non-interactive mode — formats output for pipes / scripts. |
| `--resume` | Resume the previous `tasuki` session in this project. |
| `--ignore-cooldown` | Ignore persisted cooldown state and re-evaluate providers from top priority. |
| `--yolo` | Launch each AI CLI with its permission/sandbox bypass flag (see warning below). |

Subcommands:

- `tasuki init` — (re)initialize configuration. `--global` writes to the user
  config path instead of the project. `--non-interactive` skips prompts and
  enables every detected CLI with default thresholds.
- `tasuki status` — show the current session and provider status.

### Yolo mode

Setting `--yolo`, `yolo: true` in config, or the environment variable
`TASUKI_YOLO=1` (also accepts `true`, `yes`, `on`) forwards each adapter's
permission-bypass flag:

- `claude` — `--dangerously-skip-permissions`
- `codex` — `--yolo` / `--dangerously-bypass-approvals-and-sandbox`
- `copilot` — `--allow-all-tools`

These disable the CLI's built-in safety prompts. Only enable yolo mode when
you understand the sandbox implications for your environment.

## Configuration

Configuration is YAML. It is resolved in the following order, with later
sources overriding earlier ones:

1. Built-in defaults.
2. Global config: `$XDG_CONFIG_HOME/tasuki/config.yaml` (falls back to
   `~/.config/tasuki/config.yaml`).
3. Project-local config: `.tasuki/config.yaml` in the current working
   directory.

Example config with every supported key:

```yaml
# Global defaults applied when a provider doesn't override them.
switch_threshold: 95         # percent of 5h rate-limit budget that triggers failover
warn_threshold: 80           # percent that emits a pre-switch warning (5h window)
weekly_switch_threshold: 0   # weekly (7d) failover threshold; 0 disables weekly monitoring (default)
weekly_warn_threshold: 0     # weekly warn threshold; 0 disables weekly warnings
preserve_scrollback: false   # use the terminal's main screen instead of alt-screen
yolo: false                  # forward each CLI's permission-bypass flag

providers:
  - name: claude
    enabled: true
    reset_cycle: 5h               # "5h", "weekly", or "monthly"
    priority: 1                   # optional; lower value = higher priority
    switch_threshold: 90          # optional per-provider override (5h window)
    warn_threshold: 75            # optional per-provider override (5h window)
    weekly_switch_threshold: 90   # optional per-provider override for the weekly window; 0 disables
    weekly_warn_threshold: 75     # optional per-provider weekly warn override
    preserve_scrollback: true
  - name: codex
    enabled: true
    reset_cycle: weekly
  - name: copilot
    enabled: true
    reset_cycle: monthly
```

Provider selection order is resolved as:

1. Explicit `priority` field (lower = earlier).
2. Otherwise derived from `reset_cycle`: `5h` (10) < `weekly` (50) < `monthly` (90).
3. Otherwise the position in the `providers` array.

## How failover works

Each provider runs behind an adapter that watches the CLI's output stream
for rate-limit and usage signals. `switch_threshold` / `warn_threshold`
apply to the 5-hour window (the one most users hit during a session); the
weekly/7-day window is only monitored when `weekly_switch_threshold` is set
above 0. When a provider crosses either threshold, the orchestrator puts it
in cooldown and hands the session to the next-priority provider that is
still available. Session state, provider history, and a human-readable
handoff summary are persisted under `.tasuki/` so the next CLI can continue
the task without losing context.

## Project layout

```
cmd/tasuki/        # Cobra CLI entrypoint
internal/adapter/  # provider CLI wrappers + PTY handling
internal/orchestrator/  # failover flow and provider selection
internal/config/   # YAML config loading, merging, and init
internal/state/    # session + cooldown persistence
internal/ui/       # terminal output and messaging
.tasuki/           # per-project runtime state (generated; do not hand-edit)
```

## Development

From the repository root:

```sh
go build ./cmd/tasuki          # build the CLI
go run  ./cmd/tasuki --help    # run without producing a binary
go test ./...                  # run the unit test suite
go test ./... -cover           # run tests with coverage
gofmt -w cmd internal          # format sources
```

See `AGENTS.md` for contributor conventions.

## License

Released under the [MIT License](LICENSE).
