# Gemini provider — design

- Date: 2026-10-14, revised 2026-10-15 (use the SDK)
- Status: **approved and implemented**; the catalog is hand-written (option A), with
  every catalogued Gemini model treated alike
- Topic: add Google's Gemini API as the `gemini` provider, for `gemini-4-argon`
- SDK names verified against `google.golang.org/genai v1.74.0` sources
- User documentation: the `docs/providers.md` changes are listed under "Files"

## Goal

`elencode connect gemini`, paste a key from AI Studio, `/model gemini-4-argon`, work.
Same shape as the Moonshot change (`5ae9646`): one new provider id, keyed, reachable
everywhere a provider is listed.

### Non-goals (first cut)

- **A subscription sign-in.** See "Subscriptions" below: there is no sanctioned one.
- Vertex AI: Google Cloud's hosting of the same models, billed to a GCP project and
  authenticated with Google Cloud credentials rather than an AI Studio key. The SDK
  speaks it too, so it could join later as its own provider.
- Gemini's Interactions API, background mode (needed only for >64K output), context
  caching, images, grounding, code execution.

## Subscriptions

Google AI Pro / Ultra raise the limits of Google's *own* tools (Gemini app, Gemini CLI,
Code Assist, Antigravity). The only way a third-party agent reaches that allowance is
by reusing Gemini CLI's OAuth client against the Code Assist backend, which Gemini CLI's
FAQ calls a violation of Google's terms; it says the supported route for third-party
agents is an AI Studio or Vertex AI API key. Launch coverage also says `gemini-4-argon`
is paid-tier only and not part of AI Pro.

**Decision: API key only.** If Google later publishes a sanctioned sign-in for
third-party agents, it would be a provider of its own, the way `chatgpt` sits beside
`openai`.

## Decisions

1. **Provider id `gemini`**, keyed, env var `GEMINI_API_KEY` (Google's own name for it).
   `GOOGLE_API_KEY` is not read: one variable per provider, as elsewhere. Appended to the
   end of `agent.Providers`, so it is never preferred over an existing provider at startup.
2. **A new package `internal/provider/gemini` on `google.golang.org/genai`** (approved).
   It adds `cloud.google.com/go/auth`, gRPC and protobuf as indirect dependencies;
   `make vuln` is part of the change.
3. **The SDK's environment defaults are overridden, not trusted.** `genai.NewClient`
   reads `GOOGLE_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_GENAI_USE_VERTEXAI`,
   `GOOGLE_GEMINI_BASE_URL` and others itself, with no exported way to stop it. Every
   one of those is overridden by setting it explicitly, so the client is always built
   with all three of:
   - `Backend: genai.BackendGeminiAPI` (else `GOOGLE_GENAI_USE_VERTEXAI=1` switches to
     Vertex AI),
   - `APIKey` from `config.Env` / `credentials.json`,
   - `HTTPOptions.BaseURL: "https://generativelanguage.googleapis.com/"` (else
     `GOOGLE_GEMINI_BASE_URL` redirects the request, key included).

   A test sets each variable and checks the request still goes to the stub server with
   the configured key. One leak is left: with both `GOOGLE_API_KEY` and `GEMINI_API_KEY`
   set, the SDK `log.Printf`s a warning, which would land on the TUI. elencode does not
   use the standard logger, so `main` discards it (`log.SetOutput(io.Discard)`).
4. **The SDK does not retry** (it retries only when `HTTPOptions.RetryOptions` is set): the agent
   retries, as for the other providers. A `genai.APIError`'s `Code` goes through
   `retry.RetryableStatus`. The SDK does not expose response headers, so there is no
   `Retry-After` hint; the agent's backoff applies.
5. **Thought signatures ride on `agent.ToolUseBlock`.** Gemini 3+ attaches an opaque
   `ThoughtSignature []byte` to the function-call part; replaying the history without
   it is a 400. It belongs to the call, not to a reasoning block, so `ToolUseBlock` gets
   a `Signature string` (base64 of the bytes), documented like `ThinkingBlock.Signature`.
   Anthropic and OpenAI leave it empty. This is the one change to `internal/agent`, and
   it imports nothing. Signatures on plain text parts are dropped: the API validates
   only function-call signatures.
6. **Thinking.** For a model that thinks: with `thinking_enabled`, the request sends
   `ThinkingConfig{IncludeThoughts: true, ThinkingLevel}`; effort maps `low→LOW`,
   `medium→MEDIUM`, `high|xhigh|max→HIGH`, none → unset (API default). With thinking
   off, no `ThinkingConfig` is sent: the model still thinks at its default level, but
   none is shown — as with Moonshot. Thought-summary parts (`Thought: true`) stream as
   `ThinkingDeltaEvent` and land as `ThinkingBlock{Thinking}`; they are not sent back.
7. **Tool call ids.** `FunctionCall.ID` is used when present; otherwise the provider
   makes one up (`call_<n>`). A `FunctionResponse` needs the function's `Name`, which
   `agent.ToolResultBlock` does not carry, so the conversion finds it from the
   `ToolUseBlock` with the same id earlier in the history.
8. **Key check:** `Models.List` with a page size of 1. Gemini answers a bad key with
   `400 INVALID_ARGUMENT` and reason `API_KEY_INVALID` in `Details` (not 401), so that,
   401 and 403 are `agent.ErrKeyRejected`.

## Open: the catalog

Every provider's catalog is shipped today (`anthropic.Catalog()`, `openai.Catalog()`,
… are hand-written lists; see AGENTS.md "Which models exist is shipped, not fetched").
Two ways for Gemini:

- **A. Shipped, like the rest** — chosen: `gemini-4-argon` (the default), `gemini-3.8-flash`,
  `gemini-3.5-flash-lite`, `gemini-3.1-pro-preview`, all `ThinkingEffort`.
  No change outside the new package; other Gemini models reachable as `gemini/<id>`.
- **B. Fetched from `Models.List`** — offer the models whose `SupportedActions` include
  `generateContent`, with `DisplayName`, and `Thinking: true` mapped to
  `ThinkingEffort`. Gemini's listing says whether a model thinks, which the other
  providers' do not. But it is the first fetched catalog: `catalog()` is used
  synchronously at startup and by `/model` and `elencode model`, so it needs a
  request (and a failure path) where none exists today, and AGENTS.md changes.
  The listing does not say *how* a model thinks: 2.5-era models take a token budget,
  not a level, and would be sent the wrong kind.

## Wire mapping

`Models.GenerateContentStream(ctx, model.ID, contents, config)`:

| agent | genai |
|---|---|
| `Message{Role: User}` | `Content{Role: "user"}` |
| `Message{Role: Assistant}` | `Content{Role: "model"}` |
| `TextBlock` | `Part{Text}` |
| `ThinkingBlock`, `RedactedThinkingBlock` | dropped |
| `ToolUseBlock` | `Part{FunctionCall{ID, Name, Args}, ThoughtSignature}` |
| `ToolResultBlock` | `Part{FunctionResponse{ID, Name, Response: {"output": …} or {"error": …}}}` |
| `Tools` | `Tools[0].FunctionDeclarations[]{Name, Description, ParametersJsonSchema}` |
| `MaxTokens` | `MaxOutputTokens` |

Stream: per part of `Candidates[0].Content.Parts`: `Thought` → `ThinkingDeltaEvent`,
`Text` → `TextDeltaEvent`, `FunctionCall` (arrives whole) → collected. At the end one
`ResponseEvent`. Stop reason: `MAX_TOKENS` → `max_tokens`; any function call →
`tool_use`; `SAFETY`/`RECITATION`/`PROHIBITED_CONTENT`/… → `refusal`; else `end_turn`.
A stream that ends with no candidate (prompt blocked: `PromptFeedback.BlockReason`) is
an `ErrorEvent` naming the reason.

## Files

New:
- `internal/provider/gemini/gemini.go` — `Client`, `New(key, thinking)`, a constructor
  taking a base URL for tests, `Stream`, `CheckKey`, the conversion
- `internal/provider/gemini/catalog.go` — `Catalog()`, `Default()` (shape depends on
  the open question)
- tests against an `httptest.Server` stub, standard library only

Changed (mirrors the Moonshot commit):
- `go.mod`, `go.sum` — `google.golang.org/genai`
- `internal/agent/model.go` — `ProviderGemini`, appended to `Providers`
- `internal/agent/blocks.go` — `ToolUseBlock.Signature`
- `internal/config/config.go` — `GEMINI_API_KEY` in `apiKeyEnvVars`
- `cmd/elencode/connect.go` — `keyProviders`, `apiKeyPages`
  (`https://aistudio.google.com/apikey`), `providerTitles` ("Gemini"),
  `newKeyedProvider`, `checkWithAPI`
- `cmd/elencode/main.go` — `catalog()`, `defaultModel`, discarding the standard logger
- tests beside each: `model_test.go`, `credentials_test.go`, `connect_test.go`,
  `main_test.go`
- `docs/providers.md` (table, examples, env vars, the variables that have no effect),
  `AGENTS.md` (layout line)

## Risks / to verify while implementing

- Google's docs are not reachable from this environment; the API details are from the
  SDK sources and prior knowledge. Worth one manual turn with a real key before
  merging: a tool call, its result, and a second tool call in the same turn (that is
  where a missing signature fails).
- `agent.maxTokens` is 8092 and Gemini counts thinking against `MaxOutputTokens`, so
  `HIGH` thinking may hit `MAX_TOKENS` on hard tasks. Same situation as Anthropic today;
  not changed here.
