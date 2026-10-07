# Provider setup — design

- Date: 2026-10-07
- Status: **implemented** (see "Implementation notes" for where it differs)
- Topic: how a user connects elencode to a provider, sees which providers are
  connected, and disconnects one; where credentials live; what happens on the very
  first start
- Followed by: [custom providers](2026-10-07-custom-providers-design.md), which builds
  on the commands and files introduced here
- User documentation: [`docs/providers.md`](../../providers.md)

## Goal

1. **One command to connect a provider**, inside a session and outside one: `/connect
   anthropic`, `elencode connect anthropic`. It asks for what that provider needs (an
   API key, or a ChatGPT sign-in), checks that it works, saves it, and offers that
   provider's models.
2. **One place to see the providers**: `/connect` with no provider lists every provider,
   whether it is connected, and where its credential comes from. `/config` shows the
   same.
3. **One command to disconnect**: `/disconnect anthropic`.
4. **A first start that sets itself up.** With no provider connected, `elencode` on a
   terminal opens the connect picker instead of exiting with an error.
5. **Secrets out of `config.json`**, into `credentials.json` (mode `0600`, warned about
   when others can read it).
6. **elencode decides which environment variables it reads.** The SDKs read none.

### Non-goals

- Custom providers. They are the next change, and slot into the same command (`/connect`
  gains a "server you run" choice).
- Per-provider settings in `config.json`.
- Token rotation, helper commands, OS keychains.

## Current state (recap)

- Keys: `anthropic_api_key` and `openai_api_key` in `config.json`, or `ANTHROPIC_API_KEY`
  and `OPENAI_API_KEY`. Nothing helps you set them: you edit the file or the shell.
- ChatGPT: `elencode login chatgpt [--device]` / `/login chatgpt`, saved in `chatgpt.json`.
  `logout` removes it.
- Seeing what is set up: `/config` shows the two keys masked and whether ChatGPT is
  signed in. Nothing lists providers as such.
- No credential at all: `config.Load` fails and elencode exits with an error naming the
  variables and fields.
- The SDKs read `OPENAI_BASE_URL`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, profile
  files and more by themselves (see "Explicit environment").

## Commands

`connect` and `disconnect` replace `login` and `logout`, which stay as aliases. Each is a
CLI command and a slash command of the same name, as the parity rule requires.

| Command | Alias | Does |
|---|---|---|
| `/connect [id] [--device]` · `elencode connect [id] [--device]` | `login` | without an id: lists the providers to choose from; with one: connects it, or replaces its credential if it is connected already |
| `/disconnect <id>` · `elencode disconnect <id>` | `logout` | forgets the provider's stored credential |

`--device` only means something for `chatgpt`, as today.

### Aliases

`commands.Command` and `cliCommand` gain `Aliases []string`. An alias runs the same
`Execute`/`Run`. The command menu lists only the name, with the alias in the
description (`connect a provider (also /login)`), and filtering matches either. Aliases
are subject to the parity rule like names: `cli_test.go` holds the alias lists to each
other.

### Listing

`/connect` with no id shows the providers in the frame, as a picker:

```
Connect a provider:

  anthropic   connected · API key from $ANTHROPIC_API_KEY
> openai      connected · API key in credentials.json
  chatgpt     not connected · sign in with a ChatGPT plan

↑/↓ choose · enter connect · esc close
```

Enter connects the highlighted provider, which for a provider already connected means
replacing its credential. That is also how a key is changed: there is no separate
command.

`elencode connect` with no id shows the same picker on a terminal. Without one, it
prints the rows and exits non-zero:

```
$ elencode connect | cat
anthropic   connected       API key from $ANTHROPIC_API_KEY
openai      connected       API key in credentials.json
chatgpt     not connected   sign in with a ChatGPT plan
elencode: name the provider to connect: elencode connect <id>
```

`/config` and `elencode config` replace the two key rows and the ChatGPT row with one row
per provider, built by the same function as the picker's descriptions. So both views
always say the same thing.

States:

- `connected`: it has a credential.
- `not connected`: it has none. The description says what connecting it takes.
- `signed out`: ChatGPT only. Its login is saved, but can no longer be renewed. Detecting
  this costs a token refresh, so it is only shown after a request failed with
  `chatgpt.ErrSignedOut`. Until then a saved login reads as `connected`.

### Connecting

The steps, shown in the frame in a session, and in the same panel on a terminal outside
one, as the ChatGPT login is today:

1. **Choose** (only without an id), from the listing above.
2. **Credential.**
   - Key providers: a masked input, `Paste your Anthropic API key`, with the page that
     issues one beneath it (`console.anthropic.com/settings/keys`,
     `platform.openai.com/api-keys`). Enter submits, esc cancels.
   - `chatgpt`: the existing sign-in, browser or `--device`, unchanged.
3. **Check.** A key is tried with `GET /v1/models`. That request costs nothing; the
   models it lists serve only the check, and the catalog stays shipped. Then:
   - `401`/`403`: say so and ask again, so a pasted typo never reaches the file;
   - no answer (offline, timeout): save anyway, with a notice that the key is unchecked;
   - ChatGPT has its own check: the sign-in only succeeds with working tokens.
4. **Save.** The key goes into `credentials.json`, and a ChatGPT login into
   `chatgpt.json` as before. A provider already connected has its credential replaced
   without asking, since the check has just caught a bad paste. In a session, the client
   is built and added to the `providerSet` at once, as `/login chatgpt` already does.
5. **Model.** The model picker opens, narrowed to the new provider. Enter switches to the
   model and saves it as `model`, as `/model` does. Esc keeps the model in use. Outside a
   session the picker is skipped; the last line says how to pick one.

When the environment already supplies the key (`$ANTHROPIC_API_KEY`), `connect` says so
before asking. A key saved anyway is still overridden, so it then prints
`$ANTHROPIC_API_KEY is set and wins over the key just saved`.

Piped, without a terminal, `connect <id>` reads the key from standard input. That makes
it scriptable without the key landing in shell history or the process list:

```sh
pass show anthropic | elencode connect anthropic
```

### Disconnecting

`disconnect` deletes the provider's entry from `credentials.json`, or removes
`chatgpt.json` for `chatgpt`. It does not revoke anything with the provider. In a session
it also drops the client. If the model in use was that provider's, the session switches
to the default of the first provider still connected, with a notice. If none is left, the
connect picker opens.

A key that comes from the environment cannot be removed by elencode. `disconnect` says
`the key comes from $ANTHROPIC_API_KEY: unset it to disconnect anthropic`.

### First start

`config.Load` no longer fails for want of a credential. Whether there is one becomes a
question for `cmd/elencode`:

- On a terminal, with no provider connected, `elencode` opens the session on the connect
  picker, under a one-line welcome: `No provider is connected yet. Choose one to start
  with:`. Esc quits, since there is nothing to talk to.
- Without a terminal, it fails as today, with the message pointing at
  `elencode connect <id>` instead of the config fields.

## Files

`$XDG_CONFIG_HOME/elencode/`:

| File | Holds | Written by |
|---|---|---|
| `config.json` | settings; no secrets | `/model`, the user |
| `credentials.json` | API keys | `connect` / `disconnect`, the user |
| `chatgpt.json` | the ChatGPT login | `connect chatgpt`, token renewal |

### `credentials.json`

Keyed by provider id. Each provider gets an object, so a second value later needs no new
format:

```json
{
  "anthropic": { "api_key": "sk-ant-…" },
  "openai":    { "api_key": "sk-…" }
}
```

```go
package config

// Credentials are the secrets for each provider, kept apart from the settings
// so config.json holds nothing worth stealing.
type Credentials map[agent.ProviderName]Credential

type Credential struct {
	APIKey Secret `json:"api_key,omitempty"`
}

func CredentialsPath() (string, error)

// LoadCredentials reads path. A missing file is no credentials, not an error.
// warning is non-empty when the file can be read by anyone but its owner.
func LoadCredentials(path string) (creds Credentials, warning string, err error)

// Save writes creds to path with mode 0600: to a temporary file in the same
// directory first, renamed over the old one, so a crash cannot leave half a
// file holding keys.
func (creds Credentials) Save(path string) error
```

Unlike `config.json`, the file is rewritten whole, not merged: only `connect` and
`disconnect` write it.

**Permission warning.** When `mode & 0o077 != 0`:
`credentials.json can be read by others (mode 0644): run chmod 600 <path>`. It goes to the
`notices` writer that `loadSession` already prints startup notices to, so every command
that loads a session shows it. `chatgpt.json` gets the same check. Skipped on Windows,
where the mode bits mean nothing.

**No migration.** Keys in `config.json` are no longer read, and nothing says so: a
breaking change, accepted for a personal project. `AnthropicAPIKey`, `OpenAIAPIKey`,
`AnthropicKeyFromEnv` and `OpenAIKeyFromEnv` leave `Config`.

`config.json` gains nothing in this change. Custom providers will add
`custom_providers`.

## Explicit environment

```go
package config

// Env looks up an environment variable: os.LookupEnv in main, a map in tests.
type Env func(name string) (string, bool)

func Load(env Env) (Config, error)
```

What elencode reads:

| Variable | Effect |
|---|---|
| `ANTHROPIC_API_KEY` | anthropic's key; wins over `credentials.json` |
| `OPENAI_API_KEY` | openai's key; wins over `credentials.json` |
| `XDG_CONFIG_HOME`, `HOME` | where the files are, through `os.UserConfigDir` |

Go's standard library reads `HTTPS_PROXY`, `NO_PROXY` and `SSL_CERT_FILE`. They describe
the network, not a provider, and stay honoured.

The resolved key is not stored on `Config`. `loadProviders` resolves it from `Env` and
`Credentials`, and records where it came from for the listing.

**SDK isolation.**

- anthropic: `sdk.NewClient(option.WithoutEnvironmentDefaults(), option.WithAPIKey(key))`.
  That option is the SDK's own marker for "the caller resolves credentials". It skips
  `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_PROFILE`,
  profile files and env federation. It also skips the SDK's default HTTP client, which
  differs only by a 10-minute response-header timeout. That client is passed back
  explicitly, so a hung server still fails eventually.
- openai: `openai-go` has no such option: `NewClient` always prepends
  `DefaultClientOptions()`. The provider uses only `Responses`, plus `Models` for the
  key check, so it builds those two services directly, with
  `option.WithEnvironmentProduction()` and the key. `OPENAI_BASE_URL`, `OPENAI_ORG_ID`
  and `OPENAI_PROJECT_ID` are never read.

**Behaviour change:** those SDK variables stop having any effect.

## Code changes

- `internal/config`: `Credentials`, `Env`, the permission check.
  `Load` stops requiring a credential, and the key fields go.
- `internal/provider/anthropic`, `internal/provider/openai`: construction without
  environment defaults, and `CheckKey(ctx) error`, the `GET /v1/models` check. It returns
  an error marked as rejected for `401`/`403`.
- `internal/commands`: `Command.Aliases`. `NewConnectCommand` and `NewDisconnectCommand`
  replace `NewLoginCommand` and `NewLogoutCommand`, carrying `ConnectMsg{Arg string}` and
  `DisconnectMsg{Provider string}` to the TUI.
- `cmd/elencode`:
  - `connect.go`: the shared logic. It holds the provider rows (state and credential
    source), connect (credential → check → save), disconnect, and the list of providers
    that can be connected, which replaces `signInProviders`;
  - `cli.go`: `connect` and `disconnect` replace `login` and `logout`, which become their
    aliases, and `cliCommand.Aliases` is added;
  - `tui_connect.go`: the picker, the key input (bubbles `textinput` with
    `EchoPassword`), then the existing login panel for ChatGPT and the existing model
    picker;
  - `loadProviders(cfg, creds, env)`; `missingCredential` points at `/connect`;
  - `configSettings`: one row per provider, from the shared rows, plus a `credentials`
    row with the file's path.
- `CLAUDE.md`:
  - the config section names `credentials.json` and the environment variables read;
  - the sign-in convention becomes "connecting a provider is `connect <id>`, whatever it
    needs";
  - the parity rule mentions aliases.

## Testing (red/green TDD)

- `config`:
  - credentials load, and the environment wins over the file;
  - a missing file is empty;
  - `0644` warns and `0600` does not;
  - `Save` writes `0600` and replaces the file;
  - a key left in `config.json` is ignored;
  - `Load` reads only the `Env` it is given: a conflicting real variable is set with
    `t.Setenv`.
- `anthropic`, `openai`: against an `httptest` server, with every SDK variable above set
  to a sentinel, no request carries any of them, and none goes to the `*_BASE_URL`
  given. `CheckKey` returns nil for `200`, a rejected-key error for `401`, and a
  different error for a closed listener.
- `commands`: an alias runs its command; the menu shows the name once and filters on the
  alias.
- `cmd/elencode`:
  - the rows for a key from the environment, a key from the file, a ChatGPT login, and
    nothing, in both the picker and `/config`;
  - `connect` from stdin saves the key; a rejected key is not saved; an environment key
    is warned about;
  - `disconnect` deletes the entry, refuses for an environment key, and switches away
    from a model in use;
  - in the TUI: `/connect anthropic`, paste, the model picker shows only anthropic's
    models, enter switches. `/login chatgpt` still signs in;
  - with no provider connected, the session opens on the picker; without a terminal it
    fails with the new message;
  - CLI/slash parity holds for names and aliases.

## Decided during review

- A key that cannot be checked (offline, timeout) is saved, with a warning that it is
  unchecked.

## Implementation notes

Where the implementation differs from the design above, and why:

- **No `signed out` state.** The listing shows a saved ChatGPT login as `connected`.
  Telling an expired one apart would need a refresh or remembering a failed request,
  and `chatgpt.ErrSignedOut` already says what to do when it happens.
- **`connect` and `disconnect` never read `config.json`.** They load through
  `config.LoadConnections`, the credentials half of `Load`: connecting needs none of
  the settings, so a mistake in them must not stand in its way.
- **`chatGPTAuth` no longer deletes the organisation and project headers.** Nothing sets
  them once the services are built without environment defaults, and the test that
  sets the variables still guards it.
- **Providers are listed in `agent.Providers` order**: anthropic, chatgpt, openai.
- **The session's provider list has no title or hint row**, like the other lists.
- **`elencode connect` at the shell has no screens of its own.** A key is read with
  `charmbracelet/x/term`'s `ReadPassword`, which shows nothing as it is typed, and
  asked for again after a rejection. With no provider named it lists them, on a
  terminal as on a pipe. The session keeps its masked entry and its list.
- **The config view drops the ChatGPT login's path.** Each provider gets a row, plus one
  for `credentials.json`.
- **The first provider connected on a first start gives the session that provider's
  default model** straight away, so closing the model list without a choice still
  leaves a usable session.
- **The ChatGPT login is kept in `credentials.json` too**, under `"chatgpt": {"login":
  …}`, decided after the first implementation. `chatgpt.json` was a file of its own
  so token renewals could not race the merging save of `config.json`, which no longer
  holds secrets. Every write to `credentials.json`, a renewal included, re-reads it
  and changes only its own provider's entry. An old `chatgpt.json` is not read: signing
  in again is the migration.
