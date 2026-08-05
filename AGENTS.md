# elencode

A minimalistic terminal coding agent written in Go. Personal toy project: prefer the
simple, concrete solution over the general one.

## Commands

- `make build` — builds `./bin/elencode`
- `make test` — runs `go vet ./...` and `go test -race ./...`; must pass before a change is done
- `make run` — builds and runs the TUI (needs an API key)

Do not call `go build`/`go test` directly; use the Makefile targets.

## Working agreement

- Use red/green TDD: write the failing test first, run it and watch it fail for the
  expected reason, then write the minimum code to make it pass, then refactor.
- Keep changes minimalistic. Be hesitant to extend the scope of the request — if you
  spot adjacent work, mention it instead of doing it.
- Write idiomatic Go and keep comments minimalistic: explain why, not what.
- Don't add third-party dependencies without asking.

## Layout

- `cmd/elencode` — entrypoint and the Bubble Tea TUI model
- `internal/agent` — provider-agnostic agent loop, message/block types, `Event` stream
- `internal/provider/anthropic` — Anthropic implementation of `agent.Provider`
- `internal/tools` — read, write, edit and bash tools, rooted at the working directory
- `internal/config` — `$XDG_CONFIG_HOME/elencode/config.json`, overridden by `ANTHROPIC_API_KEY`

## Terminal output

**The document.** Everything the session has settled, in order, as `transcript.Entry`
values rather than the strings they rendered to. `Render(width)` turns one into lines, so
the whole document can be laid out again at any width at any time. The program owns it;
the terminal is only where it currently happens to be drawn.

**The frame.** Only what is not settled yet: the row being streamed into, the spinner, the
menus, the input. bubbletea owns the frame and repaints it. Nothing that belongs to the
document is drawn in the frame.

**Three operations, and nothing else writes to the terminal:**

1. **Open** — `main` renders the document and writes it to stdout before the program
   starts. Nothing is cleared: the terminal keeps what it already held, and the session
   opens below it.
2. **Append** — `model.append` records an entry and prints it above the frame with
   `tea.Println`. While a block streams, its settled rows are printed as they settle but
   are *not* in the document; `model.settle` puts the whole block in when it ends, and
   only the part not already on screen is printed.
3. **Reprint** — on a width change only. `model.reprint` lays the whole document out again
   in one `tea.Println` whose payload begins with `clearAll` (`\x1b[2J\x1b[H\x1b[3J`).
   Screen and scrollback are cleared, so **the terminal's contents from before the session
   are lost on the first resize**. That is the price of owning the document, and it is
   what makes the conversation reflow instead of keeping the width it was printed at.

Rules:

- Nothing may be printed before the first frame is flushed. bubbletea sizes its screen
  buffer on that first flush, so an earlier `tea.Println` is measured against the whole
  terminal and lands at the top of the screen, over what the user already had there. That
  is why **Open** is `main`'s job and not a message handler's.
- The reprint goes out as one `tea.Println` rather than a raw write plus a print, because
  `insertAbove` ends by telling the renderer its frame starts at the cursor. The clear
  homes the cursor, the document is written from there, and the frame lands directly
  underneath — so the renderer's idea of where it is stays true without resynchronizing.
- Only a **width** change reprints. Height does not affect wrapping.
- Commands run concurrently, so prints issued from separate updates can arrive in
  either order. Chain anything ordered with `tea.Sequence`, not `tea.Batch`.
- `tea.Sequence` and `tea.Batch` return their only non-nil command directly rather
  than wrapping it (`compactCmds`). So a sequence whose print turned out empty *is*
  the command it was chained with — usually the one waiting for the next stream
  event, which blocks when run. A test that runs a command to see what it printed
  hangs on this rather than failing.
- Reprint is O(document): a long session re-renders every entry. That is accepted.

## Conventions

- `internal/agent` must not import a provider SDK. `agent.Provider` is the boundary;
  anything vendor-specific is translated inside `internal/provider/...`.
- Tests use the standard library only (plus `teatest` for the TUI). Fakes such as
  `scriptedProvider` are hand-written in the test file that needs them — no mocking library.
- Sum types are emulated as an interface with an unexported marker method (see `agent.Event`).
- Return errors instead of panicking, including in conversion code. Panics on the turn
  goroutine are recovered and surfaced as an `ErrorEvent`.
