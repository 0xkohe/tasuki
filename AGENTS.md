# Repository Guidelines

## Project Structure & Module Organization
`cmd/unblocked/main.go` is the Cobra entrypoint for the CLI. Core logic lives under `internal/`: `adapter/` wraps provider CLIs and PTY handling, `orchestrator/` manages failover flow, `config/` loads and initializes YAML config, `state/` persists sessions/cooldowns, and `ui/` handles terminal output. Project-local runtime config is stored in `.unblocked/`; treat it as generated state, not source. The checked-in `unblocked` binary at the repo root is a build artifact and should not be edited directly.

## Build, Test, and Development Commands
Use standard Go tooling from the repository root:

- `go build ./cmd/unblocked` builds the CLI entrypoint.
- `go run ./cmd/unblocked --help` runs the app without producing a new binary.
- `go test ./...` runs the full unit test suite.
- `go test ./... -cover` checks package coverage when touching orchestrator or adapter behavior.
- `gofmt -w cmd internal` formats source before review.

## Coding Style & Naming Conventions
Follow idiomatic Go style and let `gofmt` define formatting; do not hand-align code. Keep packages small and focused by responsibility under `internal/`. Exported identifiers use `CamelCase`; unexported helpers use `camelCase`. Prefer descriptive file names tied to behavior, such as `priority.go` with `priority_test.go`. Keep CLI flags and provider names consistent with existing strings: `claude`, `codex`, and `copilot`.

## Testing Guidelines
Tests live alongside code as `*_test.go` files and primarily use the standard `testing` package. Prefer table-driven tests for config and provider-selection logic, and use `t.TempDir()` for filesystem-backed state tests. Add regression coverage for any change that affects failover thresholds, session resumption, PTY passthrough, or config merging.

## Commit & Pull Request Guidelines
Recent history follows short Conventional Commit subjects such as `feat: ...`, `fix: ...`, and `init`. Keep commits scoped to one behavior change. Pull requests should explain the user-facing effect, list manual verification steps, and mention any config or session-state impact. Include terminal screenshots or transcript snippets when changing interactive UI, PTY behavior, or failover messaging.

## Configuration Notes
Local config resolves from `.unblocked/config.yaml`; global config uses the user config directory. Do not hardcode machine-specific paths, credentials, or provider tokens in tests or sample configs.
