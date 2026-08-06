# Review: `openai-provider` branch

Reviewed `git diff main...HEAD` (22 commits: the OpenAI Responses provider, effort
plumbing, config provider selection, agent-level retries, and the TUI retry notice).
Eight independent review passes (three correctness angles, reuse/simplification/
efficiency/altitude/conventions) followed by a verification pass on each candidate.
All findings below marked **confirmed** were verified against the code and the
vendored SDKs; none is speculative.

Overall: the branch is in good shape — the provider boundary is respected, the
comments are unusually good at explaining *why*, and the streaming/retry design is
sound. The real problems cluster in one theme: **context-window poisoning**. Three
independent confirmed paths leave the window in a state the API rejects on every
subsequent turn, with `/model` (which clears the conversation) as the only recovery.

---

## Correctness findings (ranked)

### 1. A max-tokens-truncated tool call permanently poisons the session — confirmed

`internal/agent/agent.go:103`, `internal/provider/openai/openai.go:451`

`stopReason` deliberately returns `StopReasonMaxTokens` before the function-call
scan so truncated argument JSON is never executed — good. But `Run` then appends
the assistant message (which still contains the `ToolUseBlock`; `toBlocks` converts
it unconditionally) and returns without running tools. The window now holds a
`function_call` with no `function_call_output` (OpenAI) / a `tool_use` with no
`tool_result` (Anthropic). Both APIs reject that shape, so **every later turn 400s**.
The `rollback` doc comment itself calls this shape "unsendable, so every later turn
would fail too" — it guards the panic and abandoned-consumer paths but misses this
third way to reach it. The repo's own `openai_test.go` "truncated tool call" test
pins the exact input that triggers it.

*Suggestion:* when `StopReason == MaxTokens`, either drop trailing `ToolUseBlock`s
before appending, or append synthetic error `tool_result`s ("cut off by token
limit") so the shape stays paired. Add an agent-level test: MaxTokens response
containing a tool call → next turn's request must be well-formed.

### 2. With thinking off, OpenAI reasoning items are replayed with empty encrypted content — confirmed

`internal/provider/openai/openai.go:380` (`toInput`), `:430` (`toBlocks`)

When `thinking_enabled: false`, `params` sends no
`Include: reasoning.encrypted_content` — but gpt-5/o-series models still emit
reasoning items, which come back with `EncryptedContent == ""`. `toBlocks` stores
them as `ThinkingBlock{Signature: "", ID: "rs_..."}` and `toInput` resubmits them
with `EncryptedContent: openai.String("")` — verified to marshal as an explicit
`"encrypted_content":""` (the SDK's `Opt` defeats `omitzero`) under `store:false`,
where the server cannot resolve a bare `rs_` id. Every multi-round tool turn and
every second user turn fails with the default config's own provider+model combo.

*Suggestion:* skip `ThinkingBlock`s with an empty `Signature` in `toInput` (or skip
storing them in `toBlocks`). Both existing round-trip tests use a non-empty `"ENC"`
signature; add one with thinking off.

### 3. `content_filter` incompletes bypass the truncated-tool-JSON guard — confirmed

`internal/provider/openai/openai.go:451`

The SDK documents exactly two incomplete reasons: `"max_output_tokens"` and
`"content_filter"`. The guard string-matches only the former, so a content-filtered
incomplete response falls through to the function-call scan and can return
`StopReasonToolUse` — executing a tool with truncated argument JSON, the exact case
the guard's comment says it exists to prevent.

*Suggestion:* branch on `Status == incomplete` first, then map the reason
(`max_output_tokens` → MaxTokens, `content_filter` → Refusal or an error). Add a
content_filter case to the stopReason test table.

### 4. Removing infer-failure rollback wedges the session on "prompt too long" — confirmed

`internal/agent/agent.go:89`

On main, a failed inference rolled the turn back. The branch keeps everything, with
the comment "inference is only ever reached with the window in a sendable shape, so
a failure here leaves nothing to undo." That reasons about *shape*, not *size*: a
pasted prompt over the context limit (or a completed tool round whose huge output
pushed the window over it) fails with a non-retryable 400, stays in the window, and
is resent — and rejected — on every subsequent turn. The retry commit (1aea034) made
this trade deliberately for the transient case, but the persistent-oversize case is
a genuine regression vs main.

*Suggestion:* the transient-keep behavior is right; the fix is narrower than
restoring blanket rollback. Consider rolling back only the turn's opening user
message when the final error is non-retryable, or at minimum surfacing "your last
prompt is still in context and may be the cause" in the error.

### 5. Anthropic effort-model users are silently downgraded from high to medium — confirmed

`internal/provider/anthropic/anthropic.go:97`, `internal/config/config.go:114`

On main, effort-capable models got no `OutputConfig` and ran at the API's own
default, which Anthropic documents as **high**. The branch always sends an explicit
effort, and `config.Load` defaults `thinking_effort` to `"medium"` on the false
premise (stated in the comment) that "Medium is what both APIs use as their own
default." A user who never touched `thinking_effort` now silently reasons at medium.
Relatedly, `toAnthropicEffort`'s doc comment says "the zero value falls to the API's
own default" but its `default:` branch returns `OutputConfigEffortMedium` — the
zero value never falls through.

*Suggestion:* either default `thinking_effort` to `high` for Anthropic, or better:
send no `OutputConfig` when the user never set an effort (make "unset" mean "server
default", which is what the comments already claim happens).

### 6. SSE streams are never closed; error paths leak connections — confirmed

`internal/provider/openai/openai.go:182`, `internal/provider/anthropic/anthropic.go:178`

Neither provider calls `stream.Close()`, and in both SDKs `Close` is the only thing
that closes the response body — `Next()` never does, even at EOF. Every non-cancelled
early return (`response.failed`, `response.error`, `stream.Err()`, `Accumulate` /
`toBlocks` / `toStopReason` failures, missing terminal event) abandons an open body,
so the connection can't return to the pool and lives until the server times it out.
Repeated failed turns accumulate connections.

*Suggestion:* `defer stream.Close()` right after `NewStreaming` in both providers.
`Close` is nil-decoder-safe in both SDKs, so it's unconditional.

### 7. `/config` shows the wrong key and no provider — confirmed

`cmd/elencode/configview.go:24`

The view still renders only `anthropic_api_key` and only `AnthropicKeyFromEnv`.
With `provider: openai` and the key in `OPENAI_API_KEY`, it shows an empty Anthropic
key labeled "(from config file)" — wrong key, wrong provenance — and never shows
`provider`, the OpenAI key, or `thinking_effort`. The view's stated purpose ("say
where a value came from") no longer holds for the active provider.

*Suggestion:* render `provider`, both keys with their per-key provenance (the split
`AnthropicKeyFromEnv`/`OpenAIKeyFromEnv` flags exist precisely for this), and
`thinking_effort`.

### 8. A mid-stream retry leaves the failed attempt's output in the transcript — confirmed

`cmd/elencode/tui.go:439`

`Stream.flush` hands every settled row to `tea.Println` as deltas arrive, and printed
scrollback is immutable by design. `m.stream.Reset()` on `RetryEvent` only drops the
unprinted tail. A mid-stream `overloaded_error` after several printed paragraphs —
exactly what this branch newly retries — leaves the first attempt's partial output,
then the retry notice, then the full second attempt, with nothing marking the first
as discarded. The existing test streams one short delta that never leaves the tail,
so it passes without covering this.

*Suggestion:* full retraction is impossible by design (the terminal owns scrollback);
the honest fix is labeling — e.g. the retry notice could say "output above is from
the failed attempt". Add a test where flushed rows exist before the retry.

### 9. Sub-second retry hints render as "retrying in 0s" — confirmed

`cmd/elencode/tui.go:209`

`event.In.Round(time.Second)` turns any hint under 500ms into `0s`
(`Retry-After-Ms: 300` → "retrying in 0s (1/5)"). The existing TUI test even
exercises this (`After: time.Millisecond`) but only asserts the substring
"retrying". *Suggestion:* floor at `1s` for display, or render sub-second values as
milliseconds; tighten the test assertion.

### 10. A saved model id survives a provider switch and fails every turn — plausible

`cmd/elencode/main.go:32`

`model` is one shared config field; `/model` saves the picked id. Switch
`provider` to `openai` afterwards and the saved `claude-...` id is fed to the
OpenAI provider — whose `Resolve` is a table lookup that never errors — so startup
succeeds and every turn 404s. *Suggestion:* per-provider model fields, or clear
`model` when the provider changes, or have startup fall back to the provider default
with a notice when the id doesn't belong to it.

### Worth noting (lower severity)

- **`classify` retries permanent local failures** (both providers): any non-`*sdk.Error`
  — including x509/proxy misconfiguration — is marked retryable, so a user with a
  broken CA bundle watches ~30s of retries before seeing the real error. Consider
  exempting `*tls.CertificateVerificationError` and `*url.Error` wrapping it, or
  accept the delay as the price of mirroring the SDKs.
- **`Load` materializes defaults that `Save` then persists** (`internal/config/config.go:122`):
  `provider` and `thinking_effort` are filled into `cfg` even when the file omits
  them, so the first `/model` save writes `"provider":"anthropic","thinking_effort":"medium"`
  into the file as if chosen — pinning users to today's defaults, against the merge
  design's stated intent. Keep defaults out of the struct that `Save` marshals
  (apply them at the point of use), or blank default-valued fields before saving.

---

## Refactoring suggestions

### R1. Deduplicate the retry plumbing between providers

`retryAfter` and the `noRetry` var (with comments) are byte-identical copies;
`retryableResponse` shares the x-should-retry override and the 408/409/429/5xx
status core; `classify` shares its whole skeleton (cancellation pass-through,
non-API-error → retryable, wrap with `After`). Only the `errors.As` target and
Anthropic's error-type table are vendor-specific. The comments have already drifted
("overrules them" vs "overrules everything else") — the sync cost is real. Hoist the
SDK-agnostic core (retryAfter, status set, header override) into one shared spot —
`internal/agent` next to `RetryableError`, which the no-SDK-imports rule permits, or
a small `internal/provider/retry` package — leaving each provider only its vendor
unwrap. The near-verbatim duplicated test suites (the six-status table appears in
both `*_test.go` files) collapse along with it.

### R2. Put `Resolve` (and the default model) on the provider boundary

`providerFromConfig` returns a 4-tuple `(Provider, string, resolveClosure, error)`
because `agent.Provider` lacks what `main` needs, even though both clients implement
identical `Resolve` and `DefaultModelID` methods. Either widen `agent.Provider` or
declare a small interface in `main` (`interface { agent.Provider; Resolve(...) }`).
Kills the closure, the positional tuple, and the `_`-discards in `main_test.go`, and
makes a third provider's missing method a compile error at the boundary.

### R3. One home for the effort vocabulary

"Which effort levels exist" lives in four places: the `agent.Effort` constants, the
string switch in `config.Load` (`"low", "medium", "high", "xhigh", "max"`), and both
providers' clamp switches. Validate in config against the agent constants (config
importing agent is fine — it's SDK-free). Related: OpenAI silently clamps
`xhigh`/`max` to `high`, contradicting config's "a typo must fail at startup rather
than silently run at some other level" ethos — a startup notice ("openai has no max,
using high") would keep both providers honest.

### R4. Simplify `inferOnce`'s three-way return

`(Response, error, bool)` puts error in the middle and encodes "consumer went away"
as `ok=false, err=nil`, which the caller needs a comment to decode. A standard
`(Response, error)` with a package-private `errAbandoned` sentinel checked via
`errors.Is` reads on sight and removes the positional convention.

### R5. Config double-defaulting

`Load` seeds `Provider` and `ThinkingEffort` defaults into the struct *and*
re-applies them in the post-unmarshal `if == ""` normalization. The normalization
alone suffices for string fields (only `ThinkingEnabled`, a bool, needs the seed).
One site should own each default. (Also the fix point for the Save-persists-defaults
note above.)

### R6. Smaller cleanups

- `providerFromConfig`'s `"":` arm and `default:` arm are dead — `config.Load`
  already normalizes empty and rejects unknown providers. Two layers half-own one
  rule; let config own it.
- `backoff`'s `attempt > 8` overflow guard is unreachable (`maxAttempts = 5`, and
  the final attempt never calls `backoff`); the dedicated `{attempt: 9}` test row
  pins behavior the program can't produce. Drop both, or fold into
  `min(initialDelay<<min(attempt-1, 4), maxDelay)`.
- `stopReason`/`hasRefusal`/`toBlocks` scan `resp.Output` three times, re-running
  `AsAny()` union decoding each pass. One pass in `toBlocks` recording
  sawToolCall/sawRefusal flags is simpler *and* cheaper.
- `inferOnce` rebuilds the `Request` (and each provider re-converts the whole
  history) on every retry attempt, though nothing changes between attempts. Build
  the Request once per round in `infer`. (Per-attempt reconversion inside the
  provider is inherent to `Stream`'s signature — fine to leave.)
- `ThinkingBlock` now carries two parallel provider handles (`Signature`,
  `ID`). Fine at two providers for a toy project, but it's the field that will
  accrete; a single opaque provider-state value is the deeper shape if a third
  provider appears.

---

## Testing gaps

1. **Delete `internal/agent/effort_test.go`** — both tests are tautologies
   (constants equal their own literals; a field holds what was just assigned). The
   wire values that matter are already pinned where they serialize (openai_test
   asserts `"effort":"medium"` in request bytes; config_test validates the strings).
2. **No agent-level MaxTokens+ToolUse test** — the test that would have caught
   finding 1. Assert the window is still sendable after a truncated tool call.
3. **No thinking-off round-trip test for OpenAI reasoning** — would have caught
   finding 2. Both existing round-trip tests use a non-empty signature.
4. **Rollback-on-send-failure paths untested** — `TestRunRollsBackFailedToolRound`
   was deleted with the retry rework; its replacement asserts the keep-side only.
   The dangling-tool_use invariant at `agent.go:99/113` (consumer-gone rollback) now
   has no test; the panic test checks the ErrorEvent but not the window state.
5. **Retry TUI test only covers the unflushed-tail case** — add one where settled
   rows were already printed before the retry (finding 8), and assert the rendered
   delay text (it currently renders "0s" in the test without any assertion noticing).
6. **stopReason table lacks `content_filter`** — the SDK documents exactly two
   incomplete reasons; test both.

---

*Review by Claude (Fable 5) — 8 finder passes + per-finding verification, 2026-08-04.*
