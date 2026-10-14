# Gemini provider — design

- Date: 2026-10-14
- Status: **draft, awaiting approval** — nothing implemented
- Topic: add Google's Gemini API as the `gemini` provider, for `gemini-4-argon`
- User documentation: the `docs/providers.md` changes are listed under "Files"

## Goal

`elencode connect gemini`, paste a key from AI Studio, `/model gemini-4-argon`, work.
Same shape as the Moonshot change (`5ae9646`): one new provider id, keyed, with a
hand-written catalog, reachable everywhere a provider is listed.

### Non-goals (first cut)

- **A subscription sign-in.** See "Subscriptions" below: there is no sanctioned one.
- Vertex AI (GCP billing, service-account / ADC auth). A different host, URL shape and
  auth; it can join later as its own provider if wanted.
- Older Gemini models in the catalog. Reachable as `gemini/<id>` (assumed not to
  reason, as for any uncatalogued model).
- Gemini's Interactions API, background mode (needed only for >64K output), context
  caching, images, grounding, code execution.
- Using `RetryInfo.retryDelay` from error details as the retry hint. The agent's own
  backoff applies.

## Subscriptions

Google AI Pro / Ultra raise the limits of Google's *own* tools (Gemini app, Gemini CLI,
Code Assist, Antigravity). The only way a third-party agent reaches that allowance is
by reusing Gemini CLI's OAuth client against the Code Assist backend
(`cloudcode-pa.googleapis.com`), which Gemini CLI's FAQ calls a violation of Google's
terms; it says the supported route for third-party agents is an AI Studio or Vertex AI
API key. Reports put enforcement (account restrictions) in place during 2026, and
consumer accounts reportedly no longer get through at all. Coverage of the launch also
says `gemini-4-argon` is paid-tier only and not part of AI Pro.

So unlike `chatgpt`, which uses OpenAI's own sanctioned "Sign in with ChatGPT" flow,
there is nothing to support here. **Decision: API key only.** If Google later publishes
a sanctioned sign-in for third-party agents, it would be a `gemini-plan`-style provider of
its own, the way `chatgpt` sits beside `openai`.

## Decisions

1. **Provider id `gemini`**, keyed, env var `GEMINI_API_KEY` (Google's own name for it).
   `GOOGLE_API_KEY` is not read: one variable per provider, as elsewhere. Appended to the
   end of `agent.Providers`, so it is never preferred over an existing provider at startup.
2. **A new package `internal/provider/gemini`, written against the REST API with
   `net/http`, no SDK.** The alternative, `google.golang.org/genai`:
   - is a new dependency (needs your OK), and brings `cloud.google.com/go/auth`, gRPC,
     protobuf, a sentencepiece tokenizer and websockets with it;
   - reads `GOOGLE_API_KEY`, `GEMINI_API_KEY`, `GOOGLE_GENAI_USE_VERTEXAI`,
     `GOOGLE_GEMINI_BASE_URL` etc. from the process environment, with no exported way to
     stop it (`envVarProvider` is unexported). Explicit config wins over most of it, but
     with both key variables set it `log.Printf`s a warning — onto the TUI. That conflicts
     with "elencode reads the environment through `config.Env`, never the SDKs".

   What we need of the API is small and stable: one streaming endpoint, one listing, a
   handful of JSON types. Hand-written, it is ~300 lines with nothing hidden.
   (Gemini's OpenAI-compatible endpoint was considered and rejected: it is Chat
   Completions, which elencode does not speak — `openai` is Responses-only — and thought
   signatures ride in a non-standard `extra_content` field that `openai-go` does not model.)
3. **Thought signatures ride on `agent.ToolUseBlock`.** Gemini 3+ attaches an opaque
   `thoughtSignature` to the function-call part; replaying the history without it is a
   400. It belongs to the call, not to a reasoning block, so `ToolUseBlock` gets a
   `Signature string`, documented like `ThinkingBlock.Signature` ("opaque, carried only
   so the block round-trips"). Anthropic and OpenAI leave it empty. This is the one
   change to `internal/agent`, and it imports nothing.
   Signatures on plain text parts are dropped: the API validates only function-call
   signatures.
4. **Thinking.** `gemini-4-argon` is `agent.ThinkingEffort`. With `thinking_enabled`, the
   request sends `thinkingConfig: {includeThoughts: true, thinkingLevel}`; effort maps
   `low→LOW`, `medium→MEDIUM`, `high|xhigh|max→HIGH`, none → omitted (API default).
   With thinking off, no `thinkingConfig` is sent: the model still thinks at its default
   level (it cannot be switched off), but none is shown — as with Moonshot.
   Thought-summary parts (`thought: true`) stream as `ThinkingDeltaEvent` and land as
   `ThinkingBlock{Thinking}`; they are not sent back (Gemini ignores them).
5. **Tool call ids.** Gemini's `functionCall.id` is used when present; otherwise the
   provider makes one up (`call_<n>`). A `functionResponse` needs the function's `name`,
   which `agent.ToolResultBlock` does not carry, so `toContents` finds it from the
   `ToolUseBlock` with the same id earlier in the history.
6. **Key check:** `GET /v1beta/models`. Gemini answers a bad key with `400
   INVALID_ARGUMENT` / reason `API_KEY_INVALID` (not 401), so that, 401 and 403 are
   `agent.ErrKeyRejected`.
7. **Retries:** non-2xx bodies are `{"error":{code,status,message}}`; the status goes
   through `retry.RetryableStatus` (429, 5xx) and `retry.After` as the other providers do.

## Wire mapping (`internal/provider/gemini`)

Request — `POST https://generativelanguage.googleapis.com/v1beta/models/{id}:streamGenerateContent?alt=sse`,
header `x-goog-api-key`:

| agent | Gemini |
|---|---|
| `Message{Role: User}` | `content{role: "user"}` |
| `Message{Role: Assistant}` | `content{role: "model"}` |
| `TextBlock` | `part{text}` |
| `ThinkingBlock`, `RedactedThinkingBlock` | dropped |
| `ToolUseBlock` | `part{functionCall{id, name, args}, thoughtSignature}` |
| `ToolResultBlock` | `part{functionResponse{id, name, response: {"output": …} or {"error": …}}}` |
| `Tools` | `tools[0].functionDeclarations[]{name, description, parameters}` |
| `MaxTokens` | `generationConfig.maxOutputTokens` |

Stream — each SSE `data:` line is a `GenerateContentResponse`; per part of
`candidates[0].content.parts`: `thought: true` → `ThinkingDeltaEvent`, `text` →
`TextDeltaEvent`, `functionCall` (arrives whole) → collected. At the end one
`ResponseEvent`. Stop reason: `MAX_TOKENS` → `max_tokens`; any function call →
`tool_use`; `SAFETY`/`RECITATION`/`PROHIBITED_CONTENT`/… → `refusal`; else `end_turn`.
A stream that ends with no candidate (prompt blocked: `promptFeedback.blockReason`) is
an `ErrorEvent` naming the reason.

## Files

New:
- `internal/provider/gemini/gemini.go` — `Client`, `New(key, thinking)`,
  `newWithBaseURL` for tests, `Stream`, `CheckKey`, the wire types and mapping
- `internal/provider/gemini/catalog.go` — `Catalog()`, `Default()`: just
  `{ID: "gemini-4-argon", DisplayName: "Gemini 4 Argon", Thinking: ThinkingEffort}`
- `internal/provider/gemini/gemini_test.go`, `catalog_test.go` — against an
  `httptest.Server` stub, standard library only

Changed (mirrors the Moonshot commit):
- `internal/agent/model.go` — `ProviderGemini`, appended to `Providers`
- `internal/agent/blocks.go` — `ToolUseBlock.Signature`
- `internal/config/config.go` — `GEMINI_API_KEY` in `apiKeyEnvVars`
- `cmd/elencode/connect.go` — `keyProviders`, `apiKeyPages`
  (`https://aistudio.google.com/apikey`), `providerTitles` ("Google Gemini"),
  `newKeyedProvider`, `checkWithAPI`
- `cmd/elencode/main.go` — `catalog()`, `defaultModel`
- tests beside each: `model_test.go`, `credentials_test.go`, `connect_test.go`,
  `main_test.go`
- `docs/providers.md` (table, examples, env vars), `AGENTS.md` (layout line)

## Risks / to verify while implementing

- Google's docs are not reachable from this environment, so the endpoint details above
  are from prior knowledge plus the `google.golang.org/genai` v1.74.0 sources (which
  have `thinkingLevel` LOW/MEDIUM/HIGH and `thoughtSignature` on parts). Worth one
  manual turn with a real key before merging: a tool call, its result, and a second
  tool call in the same turn (that is where a missing signature fails).
- `agent.maxTokens` is 8092 and Gemini counts thinking against `maxOutputTokens`, so
  `HIGH` thinking may hit `MAX_TOKENS` on hard tasks. Same situation as Anthropic today;
  not changed here.
- `gemini-4-argon`'s introductory price ($2/$10 per M tokens) ends 2026-11-30. Nothing
  in elencode depends on price.
