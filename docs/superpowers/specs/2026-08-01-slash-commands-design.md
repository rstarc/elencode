# Slash commands

## Goal

Typing `/` in the TUI input opens a menu of available commands. The input fuzzy-matches
against command names as the user keeps typing. Up/Down move a highlight, Tab completes
the highlighted command, Enter runs the command named by the input text.

The initial registry holds one command, `/quit`, which exits the program.

## Command registry

`cmd/elencode/commands.go` holds everything command-specific:

```go
type command struct {
    name        string // without the leading slash
    description string
}

var commands = []command{
    {"quit", "exit elencode"},
}
```

`matchCommands(input string) []command` returns the commands whose names fuzzy-match
`input`, which must start with `/`. Matching is a case-insensitive subsequence test on
the name, so `/qt` matches `quit`. Registry order is preserved; there is no scoring.
`/` alone matches everything. Input not starting with `/` matches nothing.

Dispatch is a `switch cmd.name` in `tui.go`. A `run func` field on `command` is
indirection the current registry does not need.

`commands.go` is pure logic plus a render function, so it tests without `teatest`.

## Model state

Three additions to `model`:

- `height int` — the last `WindowSizeMsg` height. The menu appears and disappears
  between window-size messages, so the viewport must be re-measured then too.
- `menuDismissed bool` — set by Esc.
- `menuIndex int` — index of the highlighted match.

Menu visibility is derived from the input rather than stored:

```go
func (m model) menuVisible() bool {
    return !m.menuDismissed && strings.HasPrefix(m.input.Value(), "/")
}
```

`menuDismissed` resets to `false` as soon as the input stops starting with `/`. Esc
therefore hides the menu for the rest of that slash-line; typing more characters does
not bring it back.

`menuIndex` resets to `0` on any input change, since the match set may have changed
under it. Up/Down clamp at the ends rather than wrapping.

`chromeHeight()` gains the menu's rendered height. A new `resize()` helper re-applies
`viewport.SetHeight(max(m.height-m.chromeHeight(), 1))`; `WindowSizeMsg` and every key
that changes the input or the menu both call it, so the frame cannot drift when the
menu pops in or out.

## Key handling

In the `tea.KeyPressMsg` switch, before the existing cases:

- **Esc** — if the menu is visible, set `menuDismissed = true` and consume the key.
  Otherwise fall through to the input.
- **Up / Down** — if the menu is visible, move `menuIndex` within the match set,
  clamped, and consume the key. Otherwise fall through.
- **Tab** — if the menu is visible and has at least one match, set the input to
  `/` + the highlighted match's name. The menu stays open showing that single exact
  match. No match: consume, do nothing.
- **Enter** — if the input starts with `/`, it never reaches the agent, in either UI
  state:
  - exact name match: run the command,
  - otherwise: set `m.err` to `unknown command: <input>` and reset the input. This
    renders through the existing `agent.RenderError`, so it looks like every other
    block.

Enter on non-slash input keeps today's behaviour: it requires `uiStateIdle` and
non-empty input.

Slash commands work while a turn is in flight. `/quit` cancels any in-flight turn, the
way Ctrl+C already does, then returns `tea.Quit`.

## Rendering

`renderMenu(matches []command, index, width int) string` in `commands.go` renders one
row per match:

```
/quit   exit elencode
```

It uses the left-marker idiom from `internal/agent/blocks.go` so the menu matches the
transcript's look: command name in a bright colour, description in `BrightBlack`, and
the highlighted row marked. An empty match set renders a single `no matching command`
row, so the menu still shows it is live and the text is wrong.

`View()` places the menu between the spinner row and the input row. The existing
cursor-offset loop already accounts for the extra row.

## Testing

`commands_test.go` covers `matchCommands` (exact, subsequence, case-insensitivity,
no match, non-slash input, `/` alone) and `renderMenu` (rows, highlight, empty set).

`tui_test.go` gains `teatest` cases: `/` opens the menu; Esc dismisses it and further
typing does not revive it; Up/Down move the highlight; Tab completes the highlighted
command; Enter on `/quit` quits; Enter on `/qut` shows the error; `/quit` works
mid-turn.

## Out of scope

Command arguments. Every command in the registry is a bare name, so nothing consumes
text after it. A space does not close the menu.
