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
- Optimize for readability: simple, verbose code over shorter (in lines-of-code) but more complex implementations.
- Be hesitant to introduce small functions just to remove duplication: every call is
  a context switch for the reader. Extract a function only when the code really is
  shared (the CLI and the session both run it, say) or when it makes testing easier;
  otherwise keep it inline where it is used.
- Don't add third-party dependencies without asking.

## Layout

- `cmd/elencode` — entrypoint and the Bubble Tea TUI model. Owns the provider
  clients: it builds one per connected provider and hands the agent whichever serves
  the model in use. `connect.go` holds connecting and disconnecting, shared by
  `elencode connect` and `/connect`.
- `internal/agent` — provider-agnostic agent loop, message/block types, `Event` stream
- `internal/provider/anthropic`, `internal/provider/openai` — implementations of
  `agent.Provider`, each with the hand-maintained catalog of its own models. The
  openai package also serves the `chatgpt` provider: the same Responses API on
  `chatgpt.com/backend-api/codex`, billed to a ChatGPT plan instead of API credits.
  The anthropic package also serves the `moonshot` provider: Kimi's
  Anthropic-compatible Messages API on `api.moonshot.ai/anthropic`
- `internal/provider/retry` — the parts of "is this failure worth another attempt"
  that do not depend on which API answered
- `internal/chatgpt` — "Sign in with ChatGPT": OpenAI's OAuth login, in the
  browser or with a device code (`elencode connect chatgpt [--device]`, `/connect chatgpt`),
  and a `Source` that renews the saved tokens. The OAuth is `golang.org/x/oauth2`'s;
  the provider sees it through `openai.Credentials`
- `internal/tools` — read, write, edit and bash tools, rooted at the working directory
- `internal/config` — `$XDG_CONFIG_HOME/elencode/config.json` holds the settings and
  no secrets. Credentials are in `credentials.json` beside it (mode `0600`): API keys,
  which `ANTHROPIC_API_KEY`, `OPENAI_API_KEY` and `MOONSHOT_API_KEY` win over, and the ChatGPT login.
  Every write changes one provider's entry in the file as it is then
  (`SaveCredential`, `RemoveCredential`), since another session, or a token renewal,
  may have changed the rest

## TUI

- The transcript is printed above the frame with `tea.Println`, never redrawn: the
  terminal owns it. The frame holds only what can still change — the row being
  streamed into, the spinner, the menus, the input between a line ending in the
  model in use and the user's shell prompt (user@host, directory, git branch). While a login waits on the user,
  its panel (`c` to copy the link, `esc` to cancel) stands in for the input and has
  the keyboard, and so does the masked key entry while connecting asks for an API
  key, and the effort slider `/effort` opens. `elencode connect chatgpt` on a terminal shows the same login panel; an API
  key at the shell is a plain prompt that does not echo. Printed output cannot be
  changed afterwards, so anything still in flight stays in the frame until it is
  final.
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
- The config file names no provider: every connected provider is loaded, and a
  model says which one serves it (`agent.Model.Provider`). Switching models is what
  switches providers, so `SetModel` takes both, and a turn keeps the provider it
  started on — retries included.
- Which models exist is shipped, not fetched: each provider's `Catalog()` is edited by
  hand. A model missing from it is still reachable as `provider/id`, which assumes it
  cannot reason — the assumption no request is ever rejected for.
- Tests use the standard library only (plus `teatest` for the TUI). Fakes such as
  `scriptedProvider` are hand-written in the test file that needs them — no mocking library.
- Every CLI command (`elencode <name>`) has a slash command of the same name and
  vice versa, so neither is a second-class way in. The exceptions are listed in
  `slashOnly` with the reason (`/quit` and `/effort`), and `cli_test.go` holds the two lists to
  it. Aliases follow the same rule: `/login` and `elencode login` both run `connect`.
  Both sides share the logic underneath: a command is two thin entry points.
- Connecting a provider is `connect <id>`, whatever it takes: an API key for
  `anthropic` and `openai`, a sign-in for `chatgpt`. Another provider joins
  `agent.Providers` with its own step behind `connect` and `disconnect`, rather than
  adding commands of its own.
- elencode reads the environment through `config.Env`, never the SDKs: their clients
  are built without the environment defaults they would otherwise apply, so a
  variable such as `OPENAI_BASE_URL` cannot redirect a request or add a key to it.
- Sum types are emulated as an interface with an unexported marker method (see `agent.Event`).
- Return errors instead of panicking, including in conversion code. Panics on the turn
  goroutine are recovered and surfaced as an `ErrorEvent`.
