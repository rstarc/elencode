# elencode

A minimalistic terminal coding agent written in Go. Personal toy project: prefer the
simple, concrete solution over the general one.

## Commands

- `make build` — builds `./bin/elencode`, stamping in the version
- `make test` — runs `go vet ./...` and `go test -race ./...`; must pass before a change is done
- `make run` — builds and runs the TUI (needs an API key)
- `make lint` — `golangci-lint run`, which reports gofumpt formatting too
- `make fmt` — applies the formatting `make lint` checks for
- `make vuln` — `govulncheck ./...`

Do not call `go build`/`go test` directly; use the Makefile targets. This applies to
local development only — CI calls the Go toolchain directly, see
`.github/workflows/ci.yml`. Versioning and the checks are documented in
`docs/development.md`.

## Working agreement

- Use red/green TDD: write the failing test first, run it and watch it fail for the
  expected reason, then write the minimum code to make it pass, then refactor.
- Keep changes minimalistic. Be hesitant to extend the scope of the request — if you
  spot adjacent work, mention it instead of doing it.
- Write idiomatic Go and keep comments minimalistic: explain why, not what.
- Don't add third-party dependencies without asking.

## Layout

- `cmd/elencode` — entrypoint and the Bubble Tea TUI model. Owns the provider
  clients: it builds one per API key found and hands the agent whichever serves the
  model in use.
- `internal/agent` — provider-agnostic agent loop, message/block types, `Event` stream
- `internal/provider/anthropic`, `internal/provider/openai` — implementations of
  `agent.Provider`, each with the hand-maintained catalog of its own models
- `internal/provider/retry` — the parts of "is this failure worth another attempt"
  that do not depend on which API answered
- `internal/tools` — read, write, edit and bash tools, rooted at the working directory
- `internal/config` — `$XDG_CONFIG_HOME/elencode/config.json`, overridden by
  `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`

## TUI

- The transcript is printed above the frame with `tea.Println`, never redrawn: the
  terminal owns it. The frame holds only what can still change — the row being
  streamed into, the spinner, the menus, the input. Printed output cannot be changed
  afterwards, so anything still in flight stays in the frame until it is final.
- Commands run concurrently, so prints issued from separate updates can arrive in
  either order. Chain anything ordered with `tea.Sequence`, not `tea.Batch`.
- `tea.Sequence` and `tea.Batch` return their only non-nil command directly rather
  than wrapping it (`compactCmds`). So a sequence whose print turned out empty *is*
  the command it was chained with — usually the one waiting for the next stream
  event, which blocks when run. A test that runs a command to see what it printed
  hangs on this rather than failing.

## Conventions

- `internal/agent` must not import a provider SDK. `agent.Provider` is the boundary;
  anything vendor-specific is translated inside `internal/provider/...`.
- The config file names no provider: every provider with an API key is loaded, and a
  model says which one serves it (`agent.Model.Provider`). Switching models is what
  switches providers, so `SetModel` takes both, and a turn keeps the provider it
  started on — retries included.
- Which models exist is shipped, not fetched: each provider's `Catalog()` is edited by
  hand. A model missing from it is still reachable as `provider/id`, which assumes it
  cannot reason — the assumption no request is ever rejected for.
- Tests use the standard library only (plus `teatest` for the TUI). Fakes such as
  `scriptedProvider` are hand-written in the test file that needs them — no mocking library.
- Sum types are emulated as an interface with an unexported marker method (see `agent.Event`).
- Return errors instead of panicking, including in conversion code. Panics on the turn
  goroutine are recovered and surfaced as an `ErrorEvent`.
