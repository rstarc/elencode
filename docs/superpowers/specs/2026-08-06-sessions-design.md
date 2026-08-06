# Sessions — design

- Date: 2026-08-06
- Status: approved design
- Topic: persist the context window to disk as a named, resumable session

## Goal

A conversation survives the process. Every context window is a session with an
identity, saved under the directory `elencode` was run in, listed and resumed by
`/resume`. See `docs/sessions.md` for the user-facing description.

### Non-goals

- **Token counts.** `agent.Response` carries only `Message` and `StopReason`, and
  neither provider reads usage off its stream. Storing a token count means
  plumbing `Usage` through both provider packages — its own change, after this
  one. `docs/sessions.md` lists the field; it is deferred, not dropped.
- Sessions shared between directories, or a global index of them. A session
  belongs to the directory it was made in, and the path says so.
- Editing, forking, or compacting a stored window.
- Rendering tool results in the transcript. `transcript.Block` returns `""` for
  `ToolResultBlock` today; a replayed transcript inherits that and shows tool
  calls without their output.

## Constraints from the current code

1. **A saved window is bound to the model that produced it.**
   `ThinkingBlock.Signature`/`ID` and `RedactedThinkingBlock.Data` are opaque
   handles the API verifies it issued (`internal/agent/blocks.go`). Replaying an
   Anthropic window against OpenAI is a rejected request, not a degraded one. The
   model is part of what makes a stored window valid.
2. **`Block` is a sum type, so `[]Message` does not round-trip through
   `encoding/json`.** Marshalling is fine; unmarshalling needs a discriminator per
   block. This is the bulk of the work.
3. **The transcript is not held in memory.** It is printed above the frame and
   belongs to the terminal's scrollback (`cmd/elencode/tui.go`). Resuming means
   re-rendering `[]Message` through `transcript.Message`.
4. **`SetModel` already clears the window** because it "was produced by the
   previous model and does not carry over" (`internal/agent/agent.go`). Sessions
   name that act rather than introducing a new one.

## Decisions

1. **The session replaces the Agent's loose state**, rather than sitting beside
   it. `contextWindow`, `model` and `contextGeneration` already *are* a session.
2. **`/model` ends the current session and starts a new one.** `Session.Model` is
   fixed at creation, so every stored file is internally consistent and a resumed
   window always goes back to the model that produced it. `/new` is the same
   operation on the same model.
3. **A message landing on the window is what writes it.** Nothing decides when to
   save: every change to the window writes it — the prompt, each assistant
   message, each batch of tool results, and the rollback that takes them away
   again. A `SessionStore` interface declared in `internal/agent`, implemented on
   disk elsewhere, the same shape as `Provider`.
4. **The stored model is a struct, not a qualified string.** Resolving a name
   through the catalog on load breaks two ways (see Storage format).
5. **`UpdatedAt` is stamped when a message lands**, not when the file is written.
   It answers "when did this conversation last say anything", which is not the
   same question as the file's mtime.
6. **Errors over silence**, as elsewhere: an unknown block `type`, an unknown
   format `version`, or a resume whose provider has no key each say so.

## `internal/agent`

### The Session type

```go
// internal/agent/session.go

type Session struct {
	ID        string    // UUIDv7, minted by the caller
	Name      string    // empty until /rename
	Model     Model     // the window is only valid against this
	CreatedAt time.Time
	UpdatedAt time.Time // when the last message landed
	Messages  []Message
}

// SessionStore is where a session outlives the process. Declared here and
// implemented in internal/session, the way Provider is: what a session is kept
// in is none of the agent's business, only that every change reaches it.
//
// Save receives the whole session each time, and is called for every change to
// the window. A session with no messages has no file at all — see
// internal/session, which removes one rather than writing it empty.
type SessionStore interface{ Save(s Session) error }
```

### The Agent

```go
type Agent struct {
	toolsMap   map[string]Tool
	tools      []Tool
	mu         sync.Mutex
	session    Session      // was: contextWindow + model
	provider   Provider     // stays out of Session: a live client, not data
	store      SessionStore // nil means the session is in memory only
	generation int          // was: contextGeneration
	maxTokens  int64
}
```

`turnMark` is unchanged: it already carries the model and provider a turn belongs
to, and the generation that fences it off.

Method changes:

- **`SetModel` is replaced by `NewSession(s Session, provider Provider)`** —
  installs a session as the current one and bumps `generation`, which is what
  `SetModel` did to the window. Starting fresh and resuming differ only in
  whether `s` arrives with messages in it, so they share the method; at the
  resume call site it reads as "this is the agent's new session", which is what
  it is. The agent mints no IDs and reads no clock here: the caller builds the
  `Session`. `NewSession` does not write — an empty session has no file, and a
  resumed one is already on disk unchanged.
- **`Session() Session`** returns a copy under the lock, for `/session`. The
  `Messages` slice is copied; blocks are value types and nothing mutates them in
  place. (Saving does not go through it — see `clone` below.)
- **`Rename(name string) error`** sets the name and saves, returning the failure
  for the caller to report. Renaming a session with no messages writes nothing,
  since it has no file yet; the name is carried in memory until the first
  prompt puts it there.
- **`New(tools []Tool, store SessionStore) *Agent`**. Existing tests pass nil.
- `beginTurn` and `appendMessage` stamp `UpdatedAt = time.Now()`.
- `appendMessage`, `rollback`, `infer` read `a.session.Messages` and
  `a.generation` in place of the old fields.

### Where the save happens

Every method that changes the window writes it, as part of the same locked
section that made the change:

```go
// save writes the session as it now stands. Called by everything that changes
// the window, while it still holds the lock: the file is a copy of the window,
// and there is no moment at which the two are allowed to disagree.
func (a *Agent) save() error {
	if a.store == nil {
		return nil
	}
	return a.store.Save(a.session.clone())
}
```

The callers are `beginTurn` (the prompt), `appendMessage` (each assistant message
and each batch of tool results), `rollback`, and `Rename`. `clone` is the
unexported copy `Session()` also uses — `Session()` takes the lock, so a caller
already holding it cannot go through the exported method.

**`rollback` saves too**, and this is the point of the rule rather than an
afterthought. Rollback exists to remove a `tool_use` that nothing answered,
because that shape "is unsendable, so every later turn would fail too". A file
that kept the pre-rollback window would hold exactly that shape permanently, and
`/resume` would hand the user a session that fails on its first turn. When the
rollback empties the window, the file is removed rather than written empty (see
`internal/session`), which is where "empty sessions are not stored to disk" is
enforced.

**Writing happens under the lock**, not after releasing it. There is one turn at a
time so there is nothing to contend with, and the alternative — copy, unlock,
write — lets a `Rename` from the TUI goroutine and an append from the turn
goroutine reach the file in the order their I/O finished rather than the order
they happened, which loses one of them.

**Failures are returned, not swallowed.** `beginTurn` returns `(turnMark, error)`
and `appendMessage` returns `(ok bool, err error)`; `Run` sends an `ErrorEvent`
for a save failure and carries on, since a window that did not reach the disk is
still a conversation the user can continue. A stale generation is already handled
by `appendMessage` and `rollback` returning early, so a `NewSession` landing
mid-turn stops the write for free — no separate check.

`beginTurn` is called before the goroutine starts, so its error has nowhere to go
yet: `Run` holds it and the goroutine emits it as its first `ErrorEvent`, before
the first round of inference. Emitting it from `Run` itself would mean sending on
`events` before anyone is reading, which blocks.

A rollback's save failure is reported the same way, as its own `ErrorEvent`
alongside whatever failure caused the rollback. Two events for one bad moment is
noisier than one, but they are two different problems and collapsing them would
hide the one the user can act on.

There is no turn-level bookkeeping left: no defer ordering, no `persist`, no mark
threaded through it.

## `internal/session`

New package. Imports `agent`; never imported by it. It defines its own JSON wire
types and converts, the same boundary `internal/provider/…` draws for the same
reason: the discriminated-union encoding is a storage detail, not something
`Block` should grow tags for.

### Storage format

`.elencode/sessions/<id>.json`, relative to the working directory:

```json
{
  "version": 1,
  "id": "0199c4f2-8a11-7c3e-9f02-6b1d4e77a301",
  "name": "refactor the picker",
  "model": {"provider": "anthropic", "id": "claude-opus-5", "thinking": "effort"},
  "created_at": "2026-08-06T14:30:22Z",
  "updated_at": "2026-08-06T14:52:07Z",
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "hello"}]},
    {"role": "assistant", "content": [
      {"type": "thinking", "thinking": "…", "signature": "…", "id": ""},
      {"type": "tool_use", "id": "toolu_01", "name": "read", "input": {"path": "x.go"}}
    ]}
  ]
}
```

Block tags are the API's own type names — `text`, `thinking`,
`redacted_thinking`, `tool_use`, `tool_result` — matching what
`internal/tui/transcript` already puts on screen, so a file can be read against
the provider documentation without a glossary.

`version` exists so a later format change reports "unsupported session file"
instead of half-decoding one.

**The model is stored whole.** Storing `"anthropic/claude-opus-5"` and resolving
it through the catalog on load fails two ways: `agent.FindModel` synthesizes an
unknown qualified model with `ThinkingNone` (`internal/agent/model.go`), which
would send a window full of thinking blocks in a request that says it is not
thinking; and a session created on a model newer than the binary could never be
resumed. `Provider`/`ID`/`Thinking` makes the file self-describing, so resume
needs an API key for that provider and nothing else. `DisplayName` is cosmetic
and is re-derived from the catalog when it is there, falling back to the id.

### API

```go
type Store struct{ dir string } // ".elencode/sessions"

func NewStore(dir string) Store
func (s Store) Save(sess agent.Session) error        // satisfies agent.SessionStore
func (s Store) Load(id string) (agent.Session, error)
func (s Store) List() ([]agent.Session, error)       // newest UpdatedAt first
func (s Store) Path(id string) string                // for the /session view
func NewID() (string, error)                         // UUIDv7
```

- `Save` writes a temp file in the same directory and renames it, so an
  interrupted write cannot leave a half-decoded session behind. It creates
  `.elencode/sessions` with `0o700` on first use, and writes files `0o600`:
  a stored window holds whatever was said and read.
- **`Save` of a session with no messages removes its file** rather than writing an
  empty one, and is not an error when there is no file to remove. An empty
  session is not a session yet; and the rollback that emptied one must not leave
  behind a window whose first turn would fail. This is the only surprising thing
  about the interface, and it is the reason `SessionStore` needs no second
  method.
- `Save` is called for every message, so it rewrites the whole file each time.
  A long session with large tool results means repeatedly writing a growing file.
  Acceptable at this size, and the alternative (appending records, compacting
  later) buys nothing until sessions are much bigger.
- `List` decodes whole files rather than keeping an index — dozens of sessions in
  a project directory is not worth a second source of truth. A file it cannot
  decode is skipped rather than failing the listing, so one corrupt session does
  not lock the user out of `/resume`. A directory it cannot read is an error.
- A missing directory is an empty list, not an error: it is what a project that
  has never been used looks like.

### UUIDv7

`github.com/google/uuid`, `uuid.NewV7()`. The standard library's `uuid` package
is accepted and milestoned for Go 1.27 (golang/go#62026); this module is on
go1.26.5. `NewID` is the only caller, with a TODO to drop the dependency once the
toolchain moves.

## `cmd/elencode` and the TUI

- **Startup** (`main.go`): build the store, mint an ID, `agent.NewSession` a fresh
  session on the startup model. `agent.New(tools, store)`. Nothing is written
  until the first prompt.
- **`/new`** starts a fresh session on the same model; **`/model`** starts one on
  a different model. The same operation, a different notice. Both interrupt an
  in-flight turn the way `selectModel` already does.
- **`/resume`** lists sessions, opens the picker, and on selection: refuse if that
  provider has no key, reusing the wording `chooseModel` already uses; otherwise
  `Load`, `NewSession`, and replay.
- **Replay** renders every stored message through `transcript.Message`, joins them,
  and prints the result as *one* `tea.Println` followed by a resumed notice — not
  one sequenced command per message.
- **`/rename <name>`** calls `Agent.Rename` and reports a failure into the
  transcript. With no argument it reports what the session is currently called.
- **`/session`** is a second modal beside `configview.go`, showing id, name,
  model, created, last message, message count, and file path.

Two small refactors, both forced by this change rather than adjacent to it:

- `model.configVisible bool` becomes `model.modal modalKind`
  (`modalNone`/`modalConfig`/`modalSession`). Two booleans that must never both be
  true is worse than one field.
- `defaultCommands()` in `main.go` gains the four commands.

New package `internal/tui/sessionpicker`, modeled on `internal/tui/modelpicker`
(104 lines, same `Show`/`Update`/`View`/`SelectedMsg`/`ClosedMsg` shape). Each row
shows the name or the id's first segment, the model, the relative age, and the
message count.

`.elencode/` is added to `.gitignore`.

`docs/sessions.md` is updated to match what ships: the working directory is not a
stored field (the path says it), and the token count is named as deferred rather
than listed as stored.

## Testing

Red first, per the working agreement. Standard library only, hand-written fakes.

**`internal/session`**
- Every block type round-trips through `Save` then `Load`, including an empty
  `ThinkingBlock.ID`, a `RedactedThinkingBlock`, and a `ToolUseBlock` whose raw
  input is preserved byte-for-byte.
- An unknown block `type` fails, naming the type.
- An unknown `version` fails.
- `List` orders by `UpdatedAt`, newest first.
- `List` skips a file that does not decode and returns the rest.
- `List` on a missing directory returns an empty list and no error.
- `Save` leaves no temp file behind, and replaces an existing file atomically.
- `Save` of a session with no messages removes an existing file.
- `Save` of a session with no messages and no file is not an error.

**`internal/agent`**

A fake store recording every `Save` it is handed, hand-written in the test file
alongside `scriptedProvider`.

- A turn with one tool call writes four times, in order: the prompt, the
  assistant message, the tool results, the final assistant message. Each write
  holds the window as it stood at that moment, not just the last one.
- `NewSession` writes nothing, whether the session it installs is empty or
  loaded.
- A turn on an agent with a nil store completes and does not panic.
- A `NewSession` during an in-flight turn stops that turn's writes: the store
  records nothing after the switch.
- A rollback writes the truncated window, and a rollback to empty hands `Save` a
  session with no messages (the removal itself is `internal/session`'s test).
- A store failure reaches the caller as an `ErrorEvent`, and the turn continues.
- `NewSession` bumps the generation, so an in-flight turn's appends are rejected
  — the existing `SetModel` test, renamed.
- `Session()` returns a copy whose `Messages` cannot mutate the agent's window.
- `beginTurn` and `appendMessage` move `UpdatedAt` forward.
- `Rename` writes, and returns the store's failure.

**`cmd/elencode`** (teatest)
- `/resume` on a stored session replays its transcript above the frame.
- `/resume` on a session whose provider has no key reports that and changes
  nothing.
- `/rename` renames and reports a store failure into the transcript.

## Open, deliberately

The number of tokens in a session, per the non-goals. Once `Usage` reaches
`agent.Response`, `Session` gains the field, `version` goes to 2, and the picker
shows it.
