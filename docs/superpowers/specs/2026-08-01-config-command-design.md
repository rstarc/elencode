# The /config command, masked secrets, and two-press quit

Three changes, landing as separate commits on one branch. The first two are
prerequisites for the config view; the third is independent and can be reverted
on its own if it feels wrong in use.

## 1. A Secret type for config values

`internal/config` gains a type for values that must never be shown:

```go
type Secret string

func (s Secret) String() string   // "(unset)" when empty, otherwise a fixed mask
func (s Secret) GoString() string // so %#v masks too
func (s Secret) Reveal() string   // the one deliberate way to the real value
```

`Config.AnthropicAPIKey` becomes a `Secret`, and `main.go` calls `Reveal` when
constructing the provider. The underlying kind stays `string`, so
`json.Unmarshal` needs no help.

The mask is a fixed-length run of bullets: a mask that tracked the real length
would leak it.

`Secret` deliberately does **not** implement `MarshalJSON`. Masking on display is
safe; masking on serialize is a footgun, because a later config-writing feature
would silently replace the real key with bullets. Nothing marshals `Config`
today, so there is nothing to protect yet.

## 2. Recording where values came from

`Load` already knows whether the environment overrode the file, and what path it
read, but discards both. The config view needs them, so `Config` keeps them:

```go
type Config struct {
    AnthropicAPIKey Secret `json:"anthropic_api_key"`
    // Provenance, filled in by Load, never read from the file.
    Path          string `json:"-"`
    APIKeyFromEnv bool   `json:"-"`
}
```

`Path` is set before the file is read, so it is populated even when `Load` fails
and the caller wants to say which path was wrong.

## 3. The /config command

A second entry in the `commands` registry: `{"config", "show the current
configuration"}`.

`newModel` takes a `config.Config` alongside the agent. `main` already loads one;
it simply never reached the TUI before.

Model state is a single `configVisible bool`, set when the command runs and
cleared by Esc or Ctrl+C.

While it is visible, `View` renders the panel *instead of* the
viewport/menu/input stack, with no cursor — it is a modal view, not another row:

```
  configuration

  anthropic_api_key   ••••••••   (from ANTHROPIC_API_KEY)
  config file         /Users/rstarc/.config/elencode/config.json

  esc to close
```

When the key came from the file rather than the environment, the annotation
reads `(from config file)`.

`renderConfig(cfg config.Config, width int) string` lives in `commands.go` and is
pure, so it tests without `teatest`.

Keys while the view is open: Esc and Ctrl+C close it. Everything else is
swallowed — the input is not visible, so forwarding keystrokes to it would let
the user type blind.

## 4. Two-press quit

Ctrl+C stops quitting on the first press. Model state: `quitArmed bool` and
`quitGeneration int`.

Ctrl+C, in order:

1. Config view open: close it, disarm, done.
2. Turn in flight: cancel the turn only, do not arm. This is an interrupt, not a
   quit.
3. Otherwise: arm, and `tea.Tick` a `quitDisarmMsg{generation}` two seconds out.

While armed, `press ctrl+c again to exit` renders where the spinner row sits.
Ctrl+C while armed cancels any in-flight turn and returns `tea.Quit`. Any other
keystroke disarms.

`quitDisarmMsg` carries the generation it was issued for and is ignored when that
no longer matches. Without it this sequence misfires: press Ctrl+C, wait almost
two seconds, press another key to disarm, press Ctrl+C again — the first tick
lands after the second arming and would disarm it.

Tests deliver `quitDisarmMsg` directly rather than waiting, so no clock has to be
injected.

Consequence, accepted deliberately: Ctrl+C is no longer a single-keystroke escape
from a hung turn. One press cancels the turn, a second (once idle) quits.

## Testing

`internal/config`: `Secret` masks in `String`, `%v`, `%s` and `%#v`; `Reveal`
returns the real value; the empty secret reads `(unset)`; `Load` records `Path`
and sets `APIKeyFromEnv` for both sources.

`cmd/elencode`: `renderConfig` shows the mask and never the real key, and names
each source correctly. Through `Update`: `/config` opens the view, Esc and Ctrl+C
close it, other keys are swallowed while it is open. For quit: first Ctrl+C arms
rather than quitting, second quits, another keystroke disarms, a stale
`quitDisarmMsg` does not disarm a re-armed state, and Ctrl+C during a turn
cancels without arming.

## Out of scope

Editing configuration from the view. It is read-only; `/config` shows what is
loaded and nothing more.
