# OpenAI provider — design

- Date: 2026-08-02
- Status: approved design, pending spec review
- Topic: add OpenAI as a second `agent.Provider`, alongside `internal/provider/anthropic`

## Goal

Let a session run against OpenAI as an alternative to Anthropic, selected by config.
First cut targets **full parity** with the Anthropic path, including reasoning that
round-trips (the model's reasoning is sent back verbatim on the next turn, the way
Anthropic thinking blocks already are).

### Non-goals

- Multiple providers active in one session, or switching provider at runtime. Provider
  is fixed for the process, chosen at startup like the API key is.
- Server-side conversation state. The agent owns the context window and replays it every
  turn; the provider stays stateless (see below).
- Images, PDFs, structured outputs, web search, or any server-tool blocks.

## Current architecture (recap)

- `agent.Provider` is the boundary (`internal/agent/provider.go`): `Stream(ctx, Request)
  <-chan Event` and `Models(ctx) ([]Model, error)`. A turn is terminated by exactly one
  `ResponseEvent` or `ErrorEvent`.
- `Resolve(ctx, modelID)` and `DefaultModelID()` are **not** on the interface — they are
  concrete methods on `*anthropic.Client`, called from `cmd/elencode/main.go`. We keep
  that pattern: the two-method interface stays, provider selection lives in `main.go`.
- The provider is stateless. The `Agent` holds the context window and sends the full
  message history on every `Request` (`agent.go` `infer`). We deliberately do **not** use
  the OpenAI Responses API's `previous_response_id` server state — it would duplicate and
  fight the agent's own context ownership.
- `thinking` is a construction-time setting on the provider (`anthropic.New(apiKey,
  thinking)`), sourced from config, fixed for the client's life. The new `effort` setting
  follows the same shape.

## Decisions

1. **OpenAI Responses API** (`/v1/responses`), not Chat Completions — it is the only
   OpenAI surface that returns re-submittable reasoning (`reasoning.encrypted_content`),
   which is what reasoning round-trip requires.
2. **Explicit `provider` config field** selects Anthropic vs OpenAI. Each provider keeps
   its own key field and env override.
3. **Effort is a first-class, provider-neutral concept** in `internal/agent`, consumed the
   way `thinking` already is. Both the Anthropic SDK (`OutputConfigParam.Effort`,
   `ModelCapabilities.Effort`) and OpenAI (`reasoning.effort`) expose it.
4. **Both providers honor effort.** The Anthropic provider is migrated to set
   `OutputConfig.Effort` and to prefer the effort capability in `toModel`.

## Shared abstraction changes (`internal/agent`)

Kept minimal; the Anthropic-shaped block/message types stay the source of truth.

- **`ThinkingBlock.ID string`** (new field). Round-tripping an OpenAI reasoning item needs
  both its `id` and its `encrypted_content`; one `Signature` field is not enough. OpenAI
  maps `Signature = encrypted_content`, `ID = reasoning item id`. The Anthropic mapping
  ignores `ID`. Provider-neutral; no other block change.
- **`agent.Effort`** (new string enum): `EffortMinimal`, `EffortLow`, `EffortMedium`,
  `EffortHigh`, `EffortXHigh`, `EffortMax`; zero value `EffortNone` = unset. This is the
  union of both providers' levels (`minimal` is OpenAI-only; `xhigh`/`max` Anthropic-only).
- **`agent.ThinkingMode`** gains `ThinkingEffort` alongside `ThinkingAdaptive` and
  `ThinkingBudgeted`. A provider's `toModel` sets it for models whose capabilities report
  effort support.
- No change to `StopReason`. OpenAI has no `pause_turn`/`stop_sequence` analog; those
  values simply go unused by the OpenAI mapping.

Effort level (the chosen `agent.Effort`) is passed into the provider constructor, like
`thinking`. It is not added to `agent.Request`.

## Config changes (`internal/config`)

- Add `Provider string` (`"anthropic"` default so existing configs keep working;
  `"openai"` selects the new path).
- Add `OpenAIAPIKey Secret` and an `OPENAI_API_KEY` env override, mirroring the existing
  Anthropic key handling (reuse the `Secret` type and provenance fields).
- Add `thinking_effort string` (default `"medium"`), used when `thinking_enabled` is true
  and the resolved model is effort-based. `thinking_enabled` stays as the master on/off.
- Refactor `Load`: the "key not set" error must check the **selected** provider's key,
  not always Anthropic's. Only the selected provider's env var is applied.

## Wiring (`cmd/elencode/main.go`)

A `switch cfg.Provider` factory builds the `agent.Provider`, its `DefaultModelID()`, and
runs its `Resolve`. `Resolve`/`DefaultModelID` remain concrete per-provider methods; the
switch calls the right one. Both provider constructors gain the effort argument.

## New package: `internal/provider/openai`

Mirrors the anthropic package structure: `New(apiKey, thinking, effort)`, a
`newWithOptions(...option.RequestOption)` test seam (`openai-go` supports
`option.WithBaseURL`, so tests stub an `httptest` server exactly like `anthropic_test.go`),
a `Stream` goroutine, and pure `to*` converters.

### Request building

- Responses API, **stateless**: `store: false`, full `input` array sent every turn, never
  `previous_response_id`.
- When `thinking` is on and the model is effort-based: set `reasoning: { effort: <mapped>,
  summary: "auto" }` and `include: ["reasoning.encrypted_content"]` so reasoning items come
  back with re-submittable content.
- **Effort clamping**: map the configured `agent.Effort` to OpenAI's set; levels OpenAI
  does not accept (`xhigh`, `max`) clamp to `high` rather than letting the API reject.

### `toInput` — regroup, not a 1:1 block map

A single agent message is reshaped into Responses input items:

| agent content | Responses input item |
| --- | --- |
| user `TextBlock` | `message` item, role `user` |
| assistant `TextBlock` | `message` item, role `assistant` (`output_text`) |
| assistant `ToolUseBlock` | `function_call` item `{ call_id, name, arguments }` |
| `ToolResultBlock` (currently carried inside a user message) | `function_call_output` item `{ call_id, output }`, detached from the message |
| `ThinkingBlock` | `reasoning` item `{ id, encrypted_content, summary }`, placed before the `function_call` items it produced |

`RedactedThinkingBlock` has no OpenAI analog and is not produced by the OpenAI path; a
reasoning item with encrypted content but no summary maps to a `ThinkingBlock` with empty
`Thinking`.

### Streaming → Events

- `response.output_text.delta` → `TextDeltaEvent`
- `response.reasoning_summary_text.delta` → `ThinkingDeltaEvent`
- accumulate to the final `response.completed` payload; everything else (tool args, status)
  is recovered from it, mirroring the anthropic `Accumulate` approach.
- every send goes through the ctx-cancellable `emit`, as anthropic does.

### Response → blocks

- `message` / `output_text` → `TextBlock`
- `function_call` → `ToolUseBlock{ ID: call_id, Name, Input: arguments }`
- `reasoning` → `ThinkingBlock{ Thinking: summary, Signature: encrypted_content, ID: id }`

### Derived `StopReason` (no single field)

- output contains a `function_call` → `StopReasonToolUse`
- `status: incomplete` with reason `max_output_tokens` → `StopReasonMaxTokens`
- a refusal content part → `StopReasonRefusal`
- otherwise → `StopReasonEndTurn`

### `Models` / `Resolve`

`/v1/models` returns ids only — no display names, no capabilities. So `Models` returns a
curated list, and reasoning capability (`ThinkingEffort` + which effort levels) comes from
a static id-prefix table (`o*`, `gpt-5`, ...). `Resolve` is a table lookup, not an API call.

## Anthropic provider changes (effort migration)

- `toModel`: when `Capabilities.Effort.Supported`, set `Thinking = ThinkingEffort` (prefer
  it over adaptive/budgeted). Otherwise keep today's adaptive-then-budgeted preference.
- `messageParams`: set `OutputConfig.Effort` from the configured effort (clamped to what
  the model's `EffortCapability` reports) when the model is effort-based and thinking is on.
- `New` gains the effort argument.
- **To verify during implementation:** whether an effort-capable model still needs the
  `Thinking` param set (e.g. adaptive `Display: summarized`) alongside `OutputConfig.Effort`
  for reasoning summaries to stream, or whether effort alone returns them. Set both if in
  doubt; confirm against a live call.

## Dependency

New third-party dependency: **`github.com/openai/openai-go`** (official SDK). Mirrors how
`anthropic-sdk-go` is already used, including the `option.WithBaseURL` test seam. Per
CLAUDE.md this needs an explicit OK before it is added.

## Testing (TDD, per CLAUDE.md)

`httptest` server returning canned SSE + `option.WithBaseURL`, exactly like
`anthropic_test.go`. Red/green order:

1. text-only turn → `TextDeltaEvent`s + `ResponseEvent{end_turn}`
2. tool-use turn → `function_call` accumulated into a `ToolUseBlock`, `StopReasonToolUse`
3. history round-trip → `toInput` regroups tool results and **re-submits a reasoning item**
   (`id` + `encrypted_content` preserved)
4. `StopReason` derivation (each branch)
5. effort clamping (`xhigh`/`max` → `high`)
6. `Models` / `Resolve` static table
7. config: provider selection + selected-provider key validation
8. anthropic: `toModel` prefers effort; `messageParams` sets `OutputConfig.Effort`

`make test` (`go vet` + `go test -race`) must pass before the change is done.

## Open items to confirm during implementation

- Exact `openai-go` type/accumulator names for Responses streaming (the event/union types
  and how the final response is assembled).
- Per-model OpenAI effort support (`minimal` is gpt-5-only; o-series is `low/medium/high`).
- The Anthropic effort/thinking-param interplay noted above.
