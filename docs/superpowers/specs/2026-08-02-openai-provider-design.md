# OpenAI provider — design

- Date: 2026-08-02
- Status: approved design, revised 2026-08-02 after implementation review (all SDK
  names verified against `openai-go v1.12.0` and `anthropic-sdk-go v1.57.0` sources)
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
   its own key field, env override, and **its own env-provenance flag** (see Config).
3. **Effort is a first-class, provider-neutral concept** in `internal/agent`, consumed the
   way `thinking` already is. Both the Anthropic SDK (`OutputConfigParam.Effort`,
   `ModelCapabilities.Effort`) and OpenAI (`reasoning.effort`) expose it.
4. **Both providers honor effort.** The Anthropic provider is migrated to set
   `OutputConfig.Effort` and to prefer the effort capability in `toModel`.
5. **Errors over silent coercion.** An unknown role, an unhandled block, an unrecognized
   output item, an invalid config value — each is an error the user sees, never a silent
   relabeling (e.g. system → user) or a silent clamp of a typo to a default.

## Shared abstraction changes (`internal/agent`)

Kept minimal; the Anthropic-shaped block/message types stay the source of truth.

- **`ThinkingBlock.ID string`** (new field). Round-tripping an OpenAI reasoning item needs
  both its `id` and its `encrypted_content`; one `Signature` field is not enough. OpenAI
  maps `Signature = encrypted_content`, `ID = reasoning item id`. The Anthropic mapping
  ignores `ID`. Provider-neutral; no other block change.
- **`agent.Effort`** (new string enum): `EffortLow`, `EffortMedium`, `EffortHigh`,
  `EffortXHigh`, `EffortMax`; zero value `EffortNone`. Providers clamp to the levels their
  own API accepts and treat the zero value as their default (medium). There is
  deliberately **no `EffortMinimal`**: neither pinned SDK can express it (`openai-go`
  v1.12.0 has only low/medium/high; Anthropic has low..max), so it would be a dead value
  clamped to `low` everywhere. Add it when an SDK can carry it.
- **`agent.ThinkingMode`** gains `ThinkingEffort` alongside `ThinkingAdaptive` and
  `ThinkingBudgeted`. A provider's `toModel` sets it for models whose capabilities report
  effort support.
- No change to `StopReason`. OpenAI has no `pause_turn`/`stop_sequence` analog; those
  values simply go unused by the OpenAI mapping.

Effort level (the chosen `agent.Effort`) is passed into the provider constructor, like
`thinking`. It is not added to `agent.Request`.

## Config changes (`internal/config`)

- Add `Provider string` (`"anthropic"` default so existing configs keep working;
  `"openai"` selects the new path). An unknown provider is a `Load` error.
- Add `OpenAIAPIKey Secret` and an `OPENAI_API_KEY` env override, mirroring the existing
  Anthropic key handling (reuse the `Secret` type).
- **Per-key env provenance:** the single `APIKeyFromEnv bool` becomes
  `AnthropicKeyFromEnv` + `OpenAIKeyFromEnv`. One shared bool cannot say *which* key came
  from the environment: with `provider:"openai"` and `ANTHROPIC_API_KEY` exported, `Load`
  holds the env's Anthropic key in memory, and a `Save` guarded by a selected-provider
  bool would write that secret to disk. Both env overrides are applied unconditionally
  (the value is real either way); `Save` blanks each key whose provenance flag is set.
  Rename fallout: `configview.go`, `configview_test.go`, `tui_test.go`, two config tests.
- Add `thinking_effort string` (default `"medium"`), used when `thinking_enabled` is true
  and the resolved model is effort-based. `thinking_enabled` stays as the master on/off.
  **Validated at load** against low/medium/high/xhigh/max — a typo must fail loudly at
  startup, not silently clamp to medium mid-session.
- `Load`: the "key not set" error checks the **selected** provider's key, not always
  Anthropic's.

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
- **Tools carry their descriptions.** The SDK's `ToolParamOfFunction` helper sets only
  name/parameters/strict and silently drops the description the model picks tools by;
  `toTools` builds `FunctionToolParam{Name, Description, Parameters, Strict}` directly.

### `toInput` — regroup, not a 1:1 block map

A single agent message is reshaped into Responses input items:

| agent content | Responses input item |
| --- | --- |
| user `TextBlock` | `message` item, role `user` |
| assistant `TextBlock` | `message` item, role `assistant` |
| system `TextBlock` | `message` item, role `system` (mapped explicitly; an unknown role is an error, never silently `user`) |
| assistant `ToolUseBlock` | `function_call` item `{ call_id, name, arguments }` |
| `ToolResultBlock` (currently carried inside a user message) | `function_call_output` item `{ call_id, output }`, detached from the message. `function_call_output` has no error flag, so when `IsError` is set the output is prefixed `"ERROR: "` — otherwise the model reads a failure as a result |
| `ThinkingBlock` | `reasoning` item `{ id, encrypted_content, summary }`, placed before the `function_call` items it produced. `summary` is a required field: always a **non-nil** slice, or the SDK's `omitzero` drops it and the API rejects the resubmission |

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
- `message` / `refusal` → `TextBlock` carrying the refusal text, so the transcript says
  why the turn ended
- `function_call` → `ToolUseBlock{ ID: call_id, Name, Input: arguments }`
- `reasoning` → `ThinkingBlock{ Thinking: summary, Signature: encrypted_content, ID: id }`
- anything else → error (the API may start returning new item types at any time)

### Derived `StopReason` (no single field — **order matters**)

1. `status: incomplete` with reason `max_output_tokens` → `StopReasonMaxTokens`.
   Checked **first**: a response cut off mid tool call must not report `ToolUse`, or the
   agent loop executes the tool with truncated argument JSON.
2. output contains a `function_call` → `StopReasonToolUse`
3. a refusal content part → `StopReasonRefusal`
4. otherwise → `StopReasonEndTurn`

### `Models` / `Resolve`

`/v1/models` returns ids only — no display names, no capabilities — and includes audio,
image, embedding and moderation models that cannot hold a conversation. So `Models`
**filters to chat families** by id prefix (`gpt-5`, `gpt-4.1`, `gpt-4o`, `o3`, `o4`);
reasoning capability (`ThinkingEffort`) comes from a static id-prefix table (`o*`,
`gpt-5`). `Resolve` is a table lookup, not an API call, and accepts **any** id — a model
the table does not know simply runs without reasoning, so the config can still name one.

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
CLAUDE.md this needs an explicit OK before it is added. (Approved; v1.12.0 fetched.)

## Testing (TDD, per CLAUDE.md)

`httptest` server returning canned SSE + `option.WithBaseURL`, exactly like
`anthropic_test.go`. Two principles beyond the anthropic suite's:

- **Assert on the request bytes, not just the parsed structs.** The stub records each
  request body so tests prove `store:false`, tool descriptions, reasoning/include gating,
  and the marshalled shape of a resubmitted reasoning item (where `omitzero` surprises
  live) actually went over the wire.
- **A hang is a failure, not a timeout.** Event-channel draining is bounded (2s), so a
  misframed stub or a leaked goroutine fails the test instead of the test binary.

Red/green order:

1. text-only turn → `TextDeltaEvent`s + `ResponseEvent{end_turn}`; body has `store:false`
2. tool-use turn → `ToolUseBlock`, `StopReasonToolUse`; body carries tool descriptions
3. history round-trip → `toInput` regroups tool results (marking failures) and re-submits
   the reasoning item (`id` + `encrypted_content` + non-nil `summary`, before its
   `function_call`)
4. `StopReason` derivation — table over **every** branch, including the truncated tool
   call that must read as `max_tokens`, not `tool_use`
5. effort clamping — tables for both `toOpenAIEffort` and `toAnthropicEffort`
6. error paths: HTTP 5xx → `ErrorEvent` (retries disabled in the test); unhandled output
   item / input block / role → error naming the offender
7. cancellation: ctx cancel mid-stream closes the channel (goroutine leak caught by
   `-race` + the bounded drain)
8. `Models` filter / `Resolve` static table
9. config: provider selection, selected-provider key validation, per-key env provenance
   (Save persists **neither** env key), effort validation
10. anthropic: `toModel` prefers effort; `messageParams` sets `OutputConfig.Effort`; the
    streaming path itself gets an SSE-stub test (it had none)
11. **agent-loop integration** (openai package, importing `agent` — the allowed
    direction): a real `Agent.Run` with a fake tool against a two-turn scripted server;
    the second request's bytes must contain `message, reasoning, function_call,
    function_call_output` in order with ids preserved. The automated stand-in for the
    manual E2E.

`make test` (`go vet` + `go test -race`) must pass before the change is done.

## Resolved during review (were open items)

- `openai-go` streaming names: `ResponseTextDeltaEvent{Delta}`,
  `ResponseReasoningSummaryTextDeltaEvent{Delta}`, `ResponseCompletedEvent{Response}`;
  the union dispatches on the JSON `type` field, so an SSE stub needs only `data:` lines.
- No accumulator is needed: the `response.completed` payload carries the full output.
- `IncompleteDetails.Reason` is a plain string (`"max_output_tokens"`/`"content_filter"`).
- `Models.ListAutoPaging(ctx)` exists (no params argument).
- `param.Opt[string]` exposes `.Value`; `ResponseReasoningItemParam.Summary` is
  `omitzero,required` (hence the non-nil rule above).
- OpenAI effort levels: low/medium/high only in v1.12.0; no `minimal` anywhere (hence no
  `EffortMinimal`).

## Open items to confirm against the live API

- Whether resubmitting a reasoning item also requires the function call's own item id
  (`fc_…`, distinct from `call_id`), which `toBlocks` currently drops. Watch for a 400
  naming a reasoning item "without its required following item" during manual E2E; if it
  appears, carry the id as a second opaque field on `ToolUseBlock` and extend the
  integration test's body assertion.
- The Anthropic effort/thinking-param interplay noted above.
