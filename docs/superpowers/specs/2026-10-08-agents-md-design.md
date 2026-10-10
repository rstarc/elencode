# AGENTS.md — design

- Date: 2026-10-08
- Status: **implemented**
- Topic: loading `AGENTS.md` files (falling back to `CLAUDE.md`), the user's and the
  project's, into every conversation, so the instructions reach the model without being
  pasted in

## The specification

[AGENTS.md](https://agents.md) is plain Markdown with no required fields: "a README for
agents". It says little about loading. The one normative rule is in its FAQ: *"The
closest AGENTS.md to the edited file wins; explicit user chat prompts override
everything."* Monorepos are told to put one in each package, since agents "read the
nearest file in the directory tree".

## How others load it

| | Codex | Claude Code |
|---|---|---|
| Where | project root (git root) down to the working directory, plus a global `~/.codex/AGENTS.md` | the working directory and every directory above it; subdirectories lazily, when a file there is read |
| Per directory | at most one file: `AGENTS.override.md`, then `AGENTS.md`, then configured fallback names | `CLAUDE.md` files; `AGENTS.md` only when no `CLAUDE.md` exists anywhere up the tree |
| Combining | concatenated root first, so deeper files are read later and win | concatenated root first |
| Limits | empty files skipped; 32 KiB total (`project_doc_max_bytes`) | — |

Neither picks a single winner as the spec's wording suggests: both concatenate, and
rely on the order to make the nearest file win.

## What elencode does

- **One file per directory**, `AGENTS.md`, or `CLAUDE.md` where a directory has no
  `AGENTS.md`. Codex's shape, with `CLAUDE.md` as the one fallback name. A project
  keeping both usually keeps the same text in each (this one links `CLAUDE.md` to
  `AGENTS.md`), so reading both would say everything twice.
- **From the git root down to the working directory**, root first. The root is the
  nearest directory holding `.git` — a directory, or a file in a worktree or submodule.
  Outside a checkout only the working directory is read: nothing says where the project
  starts, and walking to `/` would pick up someone else's files.
- **The user's own file** is `$XDG_CONFIG_HOME/elencode/AGENTS.md` (or `CLAUDE.md`),
  beside the config. It is read first, so every project file comes later and wins: the
  user's file says how they like to work anywhere, the project's how this project works.
- **Empty files count as none**, so an empty `AGENTS.md` falls through to `CLAUDE.md`.
- **Each file is cut at 32 KiB** (Codex's number), at the start of a character, and the
  model is told it was cut. Per file rather than in total as Codex does: files are added
  as the session goes, and a total cap would cut the deepest ones, which are the ones
  that win. The intro marks a cut file `(truncated)`.
- **Subdirectories are read as the agent reaches them.** The first time a tool
  touches a file below the working directory, the files of the directories between
  the two are attached to the end of the tool's output (Claude Code's way), behind a
  line saying they apply to that directory and win over those given before. The system
  prompt stays what it was at startup.
- **The conversation is the record of what was attached.** Before attaching, the
  wrapper looks through the tool results so far for files already there. One that left
  with the conversation, on a `/model` switch or a turn rolled back after a failure,
  is attached again the next time it is reached; no list of sent files has to be kept
  in step with the conversation.
- **Read, write and edit name their file; bash is guessed at.** Each word of its
  command, unquoted and without a leading `./`, that is the path of a file or directory
  below the working directory counts as touched: `cat api/handler.go`, `cd api && go
  test`. Globs and paths inside flags (`--file=api/x`) are missed. Only a tool call that
  succeeded counts; a command exiting non-zero still succeeded as a call.
- **The startup files are sent as the system prompt** (`agent.Request.SystemPrompt`):
  Anthropic's `system`, OpenAI's `instructions`. The ChatGPT backend already requires
  instructions of its own; the project's follow them.
- **Each file fenced with its path** (`<instructions path="…">`): in the system prompt
  behind a sentence restating the spec's precedence (a project file applies to its
  directory and below, later wins, the user's requests win over all), in a tool's
  output named relative to the working directory, as the tools name files.
- **The user sees every file.** The intro names those read at startup: the project's
  relative to the working directory, the user's in full. Tool results are not shown,
  so when one lands carrying attached files, the transcript says `found api/AGENTS.md,
  added its instructions`: after the tool call, before the reply the file shaped. Read
  from the result itself (`instructions.Attached`), so what the user is told is what
  the model was sent.

### How subdirectories are noticed: wrapping the tools

`cmd/elencode` wraps each tool's `Execute`: after the tool succeeds, the paths in its
input are looked up with `instructions.Below`, and new files are attached to its output.
The wrapper is the only place that sees, at the moment it matters, which file was
touched, and it needs nothing from the agent or the tools: they stay unaware of
instruction files. The alternatives:

- **Inside each tool.** The read tool would read the instructions itself. Every file
  tool, and bash, would grow the same code, and `internal/tools` would depend on
  `internal/instructions` for something that is not reading or writing files.
- **Inside the agent loop**, after `runTools`. The agent would have to know which tools
  name files and how, which is the vendor-neutral loop learning elencode's tool schemas.
- **In the TUI, from the tool calls it sees.** The assistant message carrying the calls
  lands in the TUI while the tools already run on the turn's goroutine, so anything it
  adds could arrive after the next round has been sent. Racy.

Where the files go was tried both ways. Added to the system prompt, they outlive the
conversation, but that needed a list of files sent, a lock around it, a setter called
mid-turn, and a separate channel to tell the TUI; and the system prompt changed under
the model mid-conversation, which prompt caching, were elencode to use it, would pay
for. Attached to the tool's output, they are part of the conversation, which is then
the only record: of what to attach, and of what to tell the user.

The cost is matching: `instructions.Attached` finds the files by the header `Attach`
writes and the `<instructions path="…">` lines after it. A file the agent reads that
holds that header and such lines would pass for attached instructions: the user would
see a notice for it, and the files it names would not be attached.

### Not done

- `AGENTS.override.md`, configurable fallback names, `@path` imports.
