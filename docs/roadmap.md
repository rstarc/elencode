# Roadmap

- Date: 2026-10-01
- Status: draft, for discussion
- Scope: functionality, code quality, CI/CD and development workflow. TUI/UX work
  (rendering, statusline, markdown, syntax highlighting, themes, logo) is deliberately
  left out and listed separately at the end, so nothing from the original list is lost.

The goal is not to clone any one harness. elencode stays minimalistic and
opinionated (see `AGENTS.md`), so the reference harnesses are used to answer two
questions: *what does every serious coding agent have*, and *what is optional
taste*. The first group is the roadmap; the second is marked as optional.

---

## 1. Reference harnesses

| Harness | Character | What is worth learning from it |
|---|---|---|
| **Pi** (badlogic/earendil) | Minimal TUI agent, 4 default tools, everything else via extensions | Session tree in JSONL with branching/fork, compaction, print/JSON/RPC modes + SDK sharing one core, `AGENTS.md`/`SYSTEM.md`, skills, prompt templates, project trust, many providers incl. OpenAI-compatible endpoints, "no permission prompts, isolate instead" stance |
| **Claude Code** | Full-featured CLI agent | Permission modes and allow/deny rules, hooks, subagents, skills, MCP, `CLAUDE.md` hierarchy, compaction, checkpoints/rewind, headless `-p` with `stream-json`, GitHub Action, sandboxing |
| **OpenCode** | Client/server TUI agent, provider-agnostic | models.dev catalog (75+ providers), LSP diagnostics fed back to the model, agents (build/plan) + subagents, per-tool permission config, `opencode run`, HTTP server + SDK, session sharing |
| **Codex CLI** | OpenAI's CLI agent | OS sandboxing (Seatbelt/Landlock) combined with approval modes, `codex exec` headless mode, `AGENTS.md`, `apply_patch` tool, resume |
| **Antigravity** | Google's agent-first IDE | Mostly UI. Functional takeaways: plan/task "artifacts" the agent maintains, parallel agents in separate workspaces, a browser-driving subagent |

### Feature matrix

Legend: ✅ has it · ◐ partial · ✗ missing · — deliberately not a goal there.
Reference columns reflect the harnesses' public docs at the time of writing and are
approximate; the elencode column is checked against the code.

| Capability | Pi | Claude Code | OpenCode | Codex | **elencode** |
|---|---|---|---|---|---|
| **Agent core** | | | | | |
| Streaming agent loop with tool calls | ✅ | ✅ | ✅ | ✅ | ✅ |
| Retry with backoff on transient errors | ✅ | ✅ | ✅ | ✅ | ✅ |
| Cancel a turn without quitting | ✅ | ✅ | ✅ | ✅ | ✅ (ctrl+c while busy) |
| System prompt | ✅ | ✅ | ✅ | ✅ | ✗ |
| Context files (`AGENTS.md`/`CLAUDE.md`) | ✅ | ✅ | ✅ | ✅ | ✗ |
| Thinking / reasoning effort | ✅ | ✅ | ✅ | ✅ | ✅ |
| Max output tokens per model | ✅ | ✅ | ✅ | ✅ | ✗ (hard-coded 8092) |
| Token usage per turn | ✅ | ✅ | ✅ | ✅ | ✗ |
| Cost tracking | ✅ | ✅ | ✅ | ◐ | ✗ |
| Prompt caching | ✅ | ✅ | ✅ | ✅ | ✗ |
| Steering / queued follow-up messages | ✅ | ✅ | ◐ | ◐ | ✗ |
| Parallel tool execution | ✅ | ✅ | ✅ | ✅ | ✗ (sequential) |
| Images as input | ✅ | ✅ | ✅ | ✅ | ✗ |
| **Sessions & context** | | | | | |
| Persist / resume sessions | ✅ | ✅ | ✅ | ✅ | ✗ (designed, `docs/sessions.md`) |
| Continue last session from CLI | ✅ | ✅ | ✅ | ✅ | ✗ |
| Compaction (manual + automatic) | ✅ | ✅ | ✅ | ✅ | ✗ |
| Branching / fork / rewind | ✅ | ✅ | ◐ | ◐ | ✗ |
| Export / share | ✅ | ◐ | ✅ | ◐ | ✗ |
| **Tools** | | | | | |
| read / write / edit / bash | ✅ | ✅ | ✅ | ✅ | ✅ (see correctness issues below) |
| grep / find / ls | ✅ | ✅ | ✅ | ◐ | ✗ |
| Output truncation with spill-to-file | ✅ | ✅ | ✅ | ✅ | ✗ |
| Read with offset/limit | ✅ | ✅ | ✅ | ✅ | ✗ |
| Multi-edit / patch | ◐ | ✅ | ✅ | ✅ | ✗ |
| Background shell processes | ◐ | ✅ | ◐ | ◐ | ✗ |
| Web fetch / search | — | ✅ | ✅ | ✅ | ✗ |
| Todo / plan tool | — | ✅ | ✅ | ✅ | ✗ |
| **Safety** | | | | | |
| Tool approval prompts | — | ✅ | ✅ | ✅ | ✗ (flag exists, never enforced) |
| Allow/deny rules per tool/command | — | ✅ | ✅ | ◐ | ✗ |
| OS sandbox | ◐ (docs) | ✅ | ✗ | ✅ | ✗ |
| Workspace path confinement | ✅ | ✅ | ✅ | ✅ | ◐ (`fs.ValidPath`, symlinks escape) |
| Project trust for repo-supplied config | ✅ | ✅ | ◐ | ✅ | n/a yet |
| **Providers** | | | | | |
| Anthropic | ✅ | ✅ | ✅ | ✗ | ✅ |
| OpenAI (Responses API) | ✅ | ✗ | ✅ | ✅ | ✅ |
| Generic OpenAI-compatible endpoint (Cerebras, Groq, OpenRouter, …) | ✅ | ✗ | ✅ | ✅ | ✗ |
| Local models (Ollama, llama.cpp, LM Studio) | ✅ | ✗ | ✅ | ✅ | ✗ |
| Google Gemini | ✅ | ✗ | ✅ | ✗ | ✗ |
| Cloud (Bedrock, Vertex, Azure) | ✅ | ✅ | ✅ | ✅ | ✗ |
| Multiple providers in one session | ✅ | — | ✅ | ✅ | ✅ |
| OAuth / subscription login | ✅ | ✅ | ✅ | ✅ | ✗ |
| **Interfaces** | | | | | |
| Interactive TUI | ✅ | ✅ | ✅ | ✅ | ✅ |
| One-shot print mode (`-p`) | ✅ | ✅ | ✅ | ✅ | ✗ |
| JSON event stream | ✅ | ✅ | ✅ | ✅ | ✗ |
| RPC / server mode | ✅ | ◐ (SDK) | ✅ | ✅ | ✗ |
| Library / SDK | ✅ | ✅ | ✅ | ◐ | ◐ (`agent` package, internal) |
| **Extensibility** | | | | | |
| Custom slash commands / prompt templates | ✅ | ✅ | ✅ | ✅ | ✗ |
| Skills | ✅ | ✅ | ✅ | ✅ | ✗ |
| MCP client | ✅ | ✅ | ✅ | ✅ | ✗ |
| Hooks / lifecycle events | ✅ | ✅ | ✅ | ◐ | ✗ |
| Subagents | ◐ (ext.) | ✅ | ✅ | ◐ | ✗ |
| LSP feedback | ◐ (ext.) | ◐ | ✅ | ✗ | ✗ |
| **Integrations** | | | | | |
| GitHub Action / CI usage | ◐ | ✅ | ✅ | ✅ | ✗ |
| Telemetry / logging / debug output | ✅ | ✅ | ✅ | ✅ | ✗ |

---

## 2. Where elencode stands

### Strengths to preserve

- A clean `agent.Provider` boundary with no SDK leakage into `internal/agent`, and a
  real sum-type `Event` stream. `Run`'s doc comment already anticipates a headless
  CLI: "every caller — TUI, headless CLI, tests — only has to render Events".
- Careful turn semantics: a turn keeps its provider, rollback removes unanswerable
  `tool_use`, panics on the turn goroutine become `ErrorEvent`s, a shared `retry`
  package keeps both providers' judgement aligned.
- Two providers already, with reasoning round-tripped in both.
- Good test density in `agent`, providers, config and TUI (~330 test functions), CI running
  vet, race tests, golangci-lint and govulncheck, Dependabot for Go modules.
- Design docs written before the code (`docs/superpowers/specs/`).

### Correctness and safety issues found during the review

These are bugs or gaps in what exists, not new features, which is why they come
first.

1. **Tool approval is never enforced.** `agent.Tool.RequiresApproval`
   (`internal/agent/agent.go:22`) is set on write, edit and bash, but nothing reads
   it: every tool call runs immediately.
2. **`edit` reports success when nothing matched.** `bytes.Replace` with no match
   returns the file unchanged, which is then written back and reported as
   "Successfully replaced…" (`internal/tools/edit.go`). It also does not reject an
   ambiguous `oldText` that matches more than once, and it rewrites the file as
   `0644` regardless of its mode.
3. **`bash` schema and struct disagree.** The schema declares `timeout` as
   `"string"`, the Go field is `int` (`internal/tools/bash.go`). A model that follows
   the schema and sends `"30"` gets an unmarshal error. There is also no default
   timeout, so a hanging command hangs the turn until the user cancels.
4. **An unknown tool name panics.** `useTool` looks up `a.toolsMap[name]` and calls
   `Execute` on the zero `Tool` (`internal/agent/agent.go:60`). The panic is
   recovered, but the whole turn is lost; it should be an `is_error` tool result the
   model can recover from.
5. **No output limits.** `read` returns whole files and `bash` returns all output
   (TODOs in both). One large file or log can overflow the context window and fail
   every subsequent request.
6. **Workspace confinement is partial.** `fs.ValidPath` rejects `..`, but symlinks
   escape `os.DirFS`, and `write`/`edit` write through `os.WriteFile` relative to the
   process directory rather than through the root (`main.go` has
   `// TODO: Use os.OpenRoot instead`).
7. **`pause_turn` ends the turn.** The Anthropic provider maps it, but `Run` stops on
   anything other than `tool_use`; the API expects the request to be re-sent to
   continue.
8. **Fixed `max_tokens` of 8092** for every model. Too small for long reasoning
   (budgeted thinking needs its budget below `max_tokens`), and wasted on nothing.
9. **No system prompt at all.** `Request` has no system field and nothing
   describes the environment (cwd, OS, date, tools' conventions) to the model.
   `agent.RoleSystem` exists and is mapped to the SDKs, but the Anthropic Messages
   API does not accept a `system` role inside `messages`, so it cannot serve that
   purpose.
10. **`internal/tools` has no tests**, although it is the code that touches the
    user's files.

### Development workflow gaps

- No tags, releases, changelog or release automation; commit messages are not in a
  conventional format, so a changelog cannot be generated from them.
- CI runs only on `main` pushes and PRs (fine), but there is no release workflow,
  no coverage report, and Dependabot does not watch `github-actions`.
- `.golangci.yml` uses only the `standard` linter set.
- No documentation for users beyond `README.md`, `docs/sessions.md` and
  `docs/development.md`; nothing on architecture or how to add a provider.

---

## 3. Roadmap

Milestones are ordered by dependency, not by size. Each item names the reason it is
needed; items marked *(optional)* are taste rather than table stakes. Items from
the original list are marked with ★.

### M0 — Make what exists correct and safe

Small, independent fixes. Each is a red/green TDD change on its own.

- [ ] Add a test suite for `internal/tools` (table tests against `t.TempDir()`).
      ★ *testing/feature: improve & properly test tool use*
- [ ] `edit`: fail when `oldText` is not found or matches more than once; preserve
      the file mode; require `oldText` to be non-empty.
- [ ] `bash`: make `timeout` an `integer` in the schema; add a default timeout (e.g.
      2 min) and report a timeout distinctly from a non-zero exit.
- [ ] Unknown tool name → `ToolResultBlock` with `IsError`, not a panic.
- [ ] Truncate tool output (head + tail, e.g. 2000 lines / 50 KB like Pi) and spill
      the full output to a temp file whose path is returned.
- [ ] `read`: `offset`/`limit` parameters, line numbers in output, refuse binary
      files, size cap.
- [ ] Root all file tools in an `os.Root` (Go 1.24+) instead of `os.DirFS` +
      relative `os.WriteFile`, so symlinks cannot escape the workspace.
- [ ] Enforce `RequiresApproval`: the agent emits an `ApprovalRequestEvent` and
      waits for an answer before executing (the TUI answers it; headless mode
      answers from a policy, see M4/M7). Until approvals land, document that every
      tool runs unattended.
- [ ] Handle `pause_turn` by re-sending the request instead of ending the turn.

### M1 — Development workflow, CI/CD and documentation

Independent of feature work; best done early because every later change benefits.

- [ ] ★ Conventional Commits from now on (`feat:`, `fix:`, `refactor:`, `docs:`,
      `test:`, `ci:`, `chore:`); document it in `docs/development.md` and
      `AGENTS.md` so the agent writes them too. Optionally lint PR titles in CI.
- [ ] ★ Semantic versioning: cut `v0.1.0` once M0 is done (the Makefile and
      `elencode version` already read the tag).
- [ ] ★ `CHANGELOG.md` in Keep a Changelog format with an `Unreleased` section that
      every PR updates.
- [ ] ★ GoReleaser: `.goreleaser.yaml` building linux/darwin (amd64/arm64) with the
      same `-X main.version` ldflags as the Makefile, checksums, GitHub release
      notes taken from the changelog; a `release.yml` workflow on `v*` tags.
      Later *(optional)*: Homebrew tap, `go install` instructions.
- [ ] CI hygiene: add `github-actions` to `dependabot.yml`; upload a coverage
      profile (`go test -coverprofile`) as an artifact; consider enabling a few more
      linters (`errorlint`, `gosec`, `bodyclose`, `contextcheck`, `nilerr`,
      `unparam`, `revive`) one at a time, fixing findings in the same PR.
- [ ] ★ Tests for the TUI: extend the existing `teatest` coverage with golden-file
      integration tests of whole interactions; evaluate catwalk for table-driven
      Update/View tests. (Needs asking before adding catwalk as a dependency.)
- [ ] ★ Refactor `tui.go` `Update()` into a routing table of `onX(msg)` handlers
      that can be unit-tested independently. (Code quality, not UX.)
- [ ] ★ Documentation, modeled after Pi's docs: user docs (install, config,
      commands, providers), ★ high-level architecture (`agent` loop, `Event` stream,
      provider boundary, TUI printing model), ★ "adding a provider" guide.
- [ ] ★ Use GitHub Issues + Milestones for this roadmap once M0/M1 are done — this
      document then becomes the seed for those issues.
- [ ] A `SessionStart` hook / devcontainer so remote agent sessions can run
      `make test` and `make lint` out of the box *(optional)*.

### M2 — Agent core: context, tokens and cost

The things every request depends on. Sessions and compaction need usage numbers, so
this precedes M3.

- [ ] **System prompt.** Add `System string` to `agent.Request`, map it to
      Anthropic's top-level `system` and OpenAI's `instructions`. A short base prompt
      describing the tools, the cwd, OS, date and git status.
- [ ] **Context files.** Load `AGENTS.md` (and `CLAUDE.md` as a fallback) from the
      user config dir, the git root and the cwd, appended to the system prompt.
      Flag to disable (`--no-context-files`).
- [ ] **Per-model limits in the catalog.** Add `ContextWindow` and
      `MaxOutputTokens` to `agent.Model`, filled in by each provider's hand-edited
      `Catalog()`; send `max_tokens` from it. This replaces ★ *set MaxTokens
      programmatically by querying the API* — the project has decided catalogs are
      shipped, not fetched, so the value belongs there. Unknown `provider/id`
      models keep a conservative default.
- [ ] **Usage.** Add `Usage{Input, Output, CacheRead, CacheWrite, Reasoning}` to
      `agent.Response`, read it from both streams, and emit it with
      `MessageEvent` (or a dedicated `UsageEvent`). ★ *usage, token & cost tracking*
- [ ] **Cost.** Add per-million-token prices to catalog entries and compute the cost
      of a turn and of a session. Also answers ★ *refactor: is the provider/model
      abstraction the right one?* — revisit once prices exist: if the same model is
      served by several providers at different prices (e.g. Claude via Anthropic and
      Bedrock), introduce an `llm.Model` with one or more `Offering{Provider, ID,
      Price}`; until then the current shape is sufficient.
- [ ] ★ **Prompt caching.** Anthropic: put `cache_control` breakpoints on the system
      prompt, tool definitions and the last message (the conversation is append-only,
      so this is close to free). OpenAI caches automatically; pass
      `prompt_cache_key` per session. Verify via `CacheRead` in usage. Keep the
      prefix stable: no timestamps in the system prompt, tools in fixed order.
- [ ] **Parallel tool execution** for tool calls in one response that do not
      require approval (read-only tools), keeping result order stable.
- [ ] **Steering / queued messages**: accept user input while a turn runs and
      inject it after the current tool batch rather than only after the turn.
- [ ] **Images as input** (`ImageBlock`), at least for `read` on image files and
      pasting a path *(optional until a need arises)*.

### M3 — Sessions and context management

The design in `docs/superpowers/specs/2026-08-06-sessions-design.md` is approved;
this milestone implements and then extends it.

- [ ] ★ Store sessions to disk (`internal/session`, `SessionStore`, JSON with a
      `version` and a block discriminator) as designed.
- [ ] ★ Load and continue a stored session (`/resume`, `/new`, `/rename`,
      `/session`), plus the CLI equivalents in M4 (`--continue`, `--session <id>`).
- [ ] Record token counts in the session once M2's usage exists (deferred in the
      design for that reason).
- [ ] **Compaction**: `/compact` plus automatic compaction when
      `context tokens > ContextWindow − reserve`; summarise older messages with a
      structured summary (goal, progress, decisions, files touched, next steps),
      keep the originals in the file. Also recover once from a context-overflow
      error by compacting and retrying.
- [ ] ★ **Branching**: fork a session from an earlier message, or rewind and retry
      from there. Pi's model — entries with parent IDs in an append-only JSONL file —
      is the reference; decide whether the v1 format should already be a tree so
      the format does not need migrating. ★ *prompt caching and branching* (the
      Earendil article) belongs here together with M2's caching.
- [ ] Export a session to Markdown *(optional; HTML is UI)*.

### M4 — Headless CLI

★ *elencode as a CLI*. Unlocks scripting, CI usage, the GitHub integration and an
evaluation harness, and is cheap because `agent.Run` is already UI-independent.

- [ ] Flag parsing with the standard `flag` package (subcommands: `version`,
      later `login`, `sessions`).
- [ ] `elencode -p "prompt"` / `--print`: run one prompt to completion, print the
      final assistant text to stdout, errors to stderr, non-zero exit on failure.
- [ ] Read the prompt from stdin when piped (`git diff | elencode -p "review"`).
- [ ] `--format text|json`: `json` writes the `Event` stream as JSONL (needs a
      stable JSON encoding of `Event` and `Block`, shared with M3's storage).
- [ ] `--model`, `--effort`, `--continue`, `--session <id>`, `--no-session`,
      `--tools <allowlist>`, `--system-prompt`, `--append-system-prompt`.
- [ ] Non-interactive approval policy: `--allow <tools>` / `--yolo`, refusing
      approval-requiring tools by default.
- [ ] RPC mode (JSONL commands on stdin) *(optional, later)* — prerequisite for
      editor integrations and ★ *remote-controlled sessions*.

### M5 — Providers and authentication

- [ ] **Generic OpenAI-compatible provider** (Chat Completions API with
      configurable `base_url`, key and a user-declared model list in the config).
      One implementation covers ★ **Cerebras**, Groq, OpenRouter, Together,
      DeepSeek, and ★ **local models** via Ollama, llama.cpp `llama-server`,
      LM Studio and vLLM. Requires extending the config with named custom
      providers, and the `provider/id` resolution in `agent.FindModel` to accept
      them.
- [ ] Google Gemini (native API, for thinking signatures and caching).
- [ ] Cloud-hosted Anthropic/OpenAI (Bedrock, Vertex, Azure) *(optional)*.
- [ ] Cross-provider handoff: switching model mid-conversation currently clears
      the window (thinking signatures are provider-bound). Convert foreign thinking
      blocks to plain text instead, so a session can continue on another model
      *(optional; changes the "model fixed per session" decision in the sessions
      design)*.
- [ ] ★ **Auth after startup**: `elencode login` / `/login` to store a key without
      editing JSON; `api_key_command` in config to read keys from a password
      manager; OAuth/subscription login *(optional, provider ToS permitting)*.
- [ ] ★ Re-evaluate the configuration update path (`config.Save` merging raw JSON)
      once there are custom providers and project-level settings. Prefer staying
      on the standard library over viper unless layering (user + project + env +
      flags) becomes hard to express.
- [ ] Project-level settings (`.elencode/settings.json`) gated by a trust prompt,
      since a repository must not be able to change the model or add MCP servers
      silently.

### M6 — Tool set

- [ ] `grep` (ripgrep if present, Go fallback), `find`/`glob`, `ls` as dedicated
      read-only tools: cheaper and safer than bash, and they can run without
      approval.
- [ ] `edit` with multiple edits per call (the existing TODO), or an
      `apply_patch`-style tool for models trained on it (GPT family).
- [ ] `bash`: persistent shell between calls *(optional)*, background processes
      with a way to read their output and kill them, streaming output as events.
- [ ] `web_fetch` (URL → text/markdown) *(optional)*; web search via the
      providers' server-side tools *(optional)*.
- [ ] `todo` tool the model uses to track multi-step work *(optional; Pi does
      without, Claude Code/OpenCode/Codex all have one)*.
- [ ] Tool allowlist/denylist from config and CLI (`--tools`).

### M7 — Safety and permissions

Builds on M0's approval mechanism.

- [ ] Permission policy in config: per tool `allow | ask | deny`, with patterns for
      bash commands (`git status*` allow, `rm -rf*` deny) and paths.
- [ ] "Remember this answer" for the session and persisted to project settings.
- [ ] OS-level sandbox for `bash` *(optional)*: Landlock on Linux / `sandbox-exec`
      on macOS, or document running in a container (Pi's approach). Decide the
      stance explicitly — Pi's "no prompts, isolate instead" is a valid answer for a
      personal tool, as long as it is a decision and not an accident (see M0).

### M8 — Extensibility

- [ ] ★ **Prompt templates / custom commands**: Markdown files in
      `~/.config/elencode/prompts/` and `.elencode/prompts/` registered as slash
      commands with `$ARGUMENTS` substitution. Small, fits the existing
      `commands.Registry`.
- [ ] ★ **Skills**: directories with a `SKILL.md` (name + description frontmatter);
      only descriptions go into the system prompt, the body is read on demand with
      the `read` tool. Compatible with the Agent Skills format used by Claude Code
      and Pi so existing skills work.
- [ ] **MCP client** (stdio + streamable HTTP), tools namespaced `server__tool`.
      The official Go SDK (`modelcontextprotocol/go-sdk`) would be a new
      dependency — ask first.
- [ ] **Hooks**: run user commands on events (before tool call, after edit, turn
      end), e.g. to format after edits or block a command *(optional)*.
- [ ] **Subagents**: a `task` tool that runs a nested `agent.Agent` with its own
      window and a restricted tool set, returning only its final text *(optional)*.
- [ ] ★ **LSP**: research first whether diagnostics after edits measurably help;
      OpenCode is the reference. Likely most useful for small/local models
      *(research)*.

### M9 — Integrations

- [ ] ★ **GitHub integration**: a GitHub Action running `elencode -p` (M4) on issue
      or PR comments, with the approval policy from M7; first as a documented
      workflow example, not a marketplace action.
- [ ] ★ **Remote-controlled sessions**: run elencode on a server and drive it from
      elsewhere. Needs M4's RPC mode and M3's sessions first; the transport (XMPP,
      SSH + RPC, HTTP) can then be a thin adapter *(research)*.
- [ ] Structured logging to a file (`log/slog`, `--debug`) for diagnosing provider
      failures without the TUI.

### Suggested order

```
M0 ──┬─> M2 ──> M3 ──> M4 ──┬─> M9
M1 ──┘          │           └─> M7
                └─> M5, M6, M8 (independent, any order once M2 is done)
```

M0 and M1 can run in parallel. M2 (system prompt, usage, limits) is the real
foundation: sessions want token counts, compaction wants context sizes, cost wants
usage, and caching wants a stable system prompt. M4 is small but high-leverage and
can be pulled forward if scripting or evals are wanted sooner.

---

## 4. Original list, mapped

| Item | Where |
|---|---|
| catwalk + teatest tests for the TUI | M1 |
| Conventional commits, semver, changelog | M1 |
| goreleaser | M1 |
| GitHub issues + milestones | M1 |
| Properly forward and display errors | ✅ done |
| UI improvements (left border, user message style) | ✅ done |
| Cancel a turn without quitting | ✅ done (ctrl+c while processing cancels the turn; a second ctrl+c when idle quits) |
| OpenAI provider (listed twice) | ✅ done |
| Set MaxTokens programmatically | M2 (from the shipped catalog, not an API query) |
| Store sessions to disk / load and continue | M3 |
| Prompt caching and branching | M2 (caching), M3 (branching) |
| Local LLM provider | M5 (OpenAI-compatible provider) |
| Model picker shows models for all providers | ✅ done (provider shown in the description column) |
| Provider/model vs. LLM abstraction | M2, decided once prices exist |
| `Update()` routing table | M1 |
| Re-design configuration updates / viper | M5 |
| Documentation (Pi-style, providers, architecture) | M1 |
| Sessions documentation and proposal | ✅ done (`docs/sessions.md`, sessions design spec) |
| GitHub integration | M9 |
| `login` command | M5 |
| Remote-controlled sessions (XMPP) | M9 |
| Usage, token & cost tracking | M2 (tracking); displaying it is UI |
| LSP support | M8 (research) |
| Prompt templates, skills | M8 |
| Improve & properly test tool use | M0 |
| elencode as a CLI | M4 |
| Cerebras provider | M5 (OpenAI-compatible provider) |

### Excluded for now (UI/UX)

Kept here so they are not lost: render tool output below the tool use; syntax
highlighting for bash and code; diff view for `edit`; markdown rendering of
assistant text; statusline with model, timer, token count, context percentage and
cost (its data comes from M2); fuzzy matching in the model picker; logo.
