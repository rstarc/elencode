# Custom providers (Ollama, llama.cpp, LM Studio, vLLM) — design

- Date: 2026-10-07
- Status: **draft, on hold until [provider setup](2026-10-07-provider-setup-design.md) lands**
- Topic: let a session talk to a model served by a server the user runs, locally or
  on another machine, optionally behind an API key
- Depends on: [provider setup](2026-10-07-provider-setup-design.md), for `credentials.json`,
  the explicit environment and `/connect`
- User documentation: drafted in the appendix, to move into `docs/providers.md`

## Goal

Start a model with `ollama serve`, `llama-server`, LM Studio or vLLM, add the server to
`config.json`, and use its models the way a catalog model is used: pick one with
`/model`, switch to and away from it mid-session, have it saved as the next session's
model. The server may be on `localhost` or across the network, and may require a key.

### Non-goals (first cut)

- Starting, stopping or downloading models. The server is the user's to run.
- Reasoning on custom provider models. Setting a reasoning effort is not supported for
  them yet; they are assumed not to reason, as an uncatalogued model is today.
- Chat Completions (`/v1/chat/completions`). Every target server speaks one of the two
  APIs below.
- Adding a custom provider through `/connect`. That is the natural next step once this
  lands, but the first cut is edited into `config.json` by hand.
- Auth other than one bearer token. No rotation, no custom headers.
- Setting the server's context window. It is a server setting (see `docs/providers.md`).

## Decisions

1. **Two APIs, chosen per provider: `responses` and `messages`.** A custom provider is
   the existing `openai.Client` (Responses API) or `anthropic.Client` (Messages API)
   with a different base URL and key. No new protocol code. Ollama, llama.cpp and
   LM Studio serve both; vLLM serves `responses`.
2. **A `custom_providers` mapping in `config.json`.** Its key is the provider's id, which
   is what models are qualified with: `ollama/qwen3-coder:30b`. Entries hold `name`,
   `api`, `base_url` and optionally `api_key_env`. No `models` key.
3. **The key is never in `config.json`.** It comes from the environment variable
   `api_key_env` names, or from `credentials.json` under the provider's id. With neither
   it is not sent, and the key is always sent as `Authorization: Bearer`.
4. **The picker asks the server.** Opening `/model` (or running `elencode model`) lists
   each custom provider's models with `GET <base_url>/models`, every time. Upstream
   providers stay shipped, not fetched. Their catalogs keep saying which models reason,
   which a listing never says.
5. **"Server not running" fails at once.** A refused connection is not retried, for any
   provider, and a custom provider's error names the URL it tried.

## Config (`internal/config`)

```json
{
  "model": "ollama/qwen3-coder:30b",
  "custom_providers": {
    "ollama": {
      "name": "Ollama on this machine",
      "api": "responses",
      "base_url": "http://localhost:11434/v1"
    },
    "gpu-box": {
      "name": "llama.cpp on the GPU box",
      "api": "messages",
      "base_url": "https://llm.example.com",
      "api_key_env": "GPU_BOX_API_KEY"
    }
  }
}
```

```go
// CustomProvider is a server the user runs that speaks one of the APIs elencode
// has a client for. Its key in Config.CustomProviders is the provider id its
// models are qualified with.
type CustomProvider struct {
	// Name is what the picker and the config view call it. Defaults to the id.
	Name string `json:"name,omitempty"`
	// API is the protocol the server is spoken to in: APIResponses or APIMessages.
	API API `json:"api"`
	// BaseURL is what that API's own SDK takes as its base: up to /v1 for
	// responses, the server root for messages, as each server documents it.
	BaseURL string `json:"base_url"`
	// APIKeyEnv names the variable holding the key, which wins over a key in
	// credentials.json. Naming one that is unset leaves the provider unreachable
	// rather than reached without a key: the user said it needs one.
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

type API string

const (
	APIResponses API = "responses" // OpenAI Responses API
	APIMessages  API = "messages"  // Anthropic Messages API
)

type Config struct {
	// ...
	CustomProviders map[agent.ProviderName]CustomProvider `json:"custom_providers,omitempty"`
}
```

`Load` fails at startup, as for a bad `thinking_effort`, on:

- an id that is empty, contains `/`, or is a built-in provider's (`anthropic`, `openai`,
  `chatgpt`);
- an `api` that is neither `responses` nor `messages`. It is required, with no default:
  the two take different base URLs, and guessing wrong gives a bare 404;
- a `base_url` that does not parse, is not `http`/`https`, or has no host;
- a `messages` provider whose `base_url` ends in `/v1`. The SDK appends `v1/messages`
  itself, so the request would 404 on `/v1/v1/messages`. The error says to drop it.

A custom provider counts as a credential for the "nothing to talk to" check: a config
with only `custom_providers` starts.

`Save` round-trips the mapping: it is a top-level key like any other.

### Keys

From `credentials.json` (see the provider setup design), under the provider's id:

```json
{ "gpu-box": { "api_key": "…" } }
```

Resolution, in `loadProviders`:

1. `api_key_env` set → its value. Unset or empty → the provider is not loaded, and
   choosing its model says `no API key for gpu-box: $GPU_BOX_API_KEY is not set`.
2. Otherwise `credentials.json[id].api_key`, if present.
3. Otherwise no key: no `Authorization` header at all. This is the usual local case.

## Agent (`internal/agent`)

- `ProviderName` is unchanged. `Providers` stays the list of **built-in** providers.
- `FindModel` resolves any qualified `prefix/id`. Today it rejects a prefix not in
  `Providers`; that check moves to `cmd/elencode`, which owns the `providerSet` and
  already rejects a model whose provider has no client. There the error tells
  "unknown provider" apart from "no API key".
- Only the first `/` splits, so ids with a slash in them (vLLM's `Qwen/Qwen3-…`) work as
  `gpu-box/Qwen/Qwen3-…` and from the picker.

## Providers (`internal/provider/...`)

```go
package openai

// NewCustom is a client for a server the user runs that speaks the Responses
// API. apiKey may be empty, and then no Authorization header is sent.
func NewCustom(baseURL, apiKey string) *Client

// ListModels asks the server which models it serves, as GET <base>/models.
func (c *Client) ListModels(ctx context.Context) ([]string, error)
```

```go
package anthropic

// NewCustom is a client for a server the user runs that speaks the Messages
// API. The key, if any, goes out as a bearer token rather than x-api-key:
// what llama.cpp, Ollama and the gateways in front of them all accept.
func NewCustom(baseURL, apiKey string) *Client

func (c *Client) ListModels(ctx context.Context) ([]string, error)
```

- Neither takes `thinking`/`effort`: a custom client never asks for reasoning. The
  existing gate (`Model.Thinking`) would already stop it, because a listed model has
  `ThinkingNone`. The constructor makes it explicit too.
- The clients return ids, not `agent.Model`s, because a client does not know which
  provider id it was configured under. `cmd/elencode` stamps the provider, using the id
  as the display name.
- Both build on the SDK isolation from the credentials change, so no environment
  variable reaches a custom provider's request.
- **Refused connections:** `retry.Refused(err)` (`errors.Is(err, syscall.ECONNREFUSED)`)
  makes `classify` in both providers return the error unwrapped, so it is not retried.
  It applies to every provider: a refused connection never cures itself between
  attempts. A custom client wraps it as `cannot reach http://localhost:11434/v1: is the
  server running? (…connection refused)`.

What happens with a server's replies, checked against the current conversion code:

- **responses:** `max_output_tokens` is sent. A reasoning model (gpt-oss, qwen3) still
  returns reasoning items. They come back without encrypted content, so `toInput`
  already skips them on replay. They show as a bare "thinking" title, as an OpenAI model
  with thinking off does today, because `toBlocks` reads only `summary`. Acceptable while
  reasoning is out of scope.
- **messages:** a thinking block that a local server returns is shown, and is replayed
  on the next request with whatever signature it came with, usually an empty one.
  `toMessages` replays every thinking block. Anthropic would reject an empty signature;
  llama.cpp and Ollama are expected to ignore it, since they convert the request to
  their own format. That is the first thing to verify against a real server (see
  Testing). If they reject it, `NewCustom` drops unsigned thinking blocks before
  sending, as the openai side already does.
- A server whose model is not loaded yet can take a minute before the first token. No
  timeout is in the way (see the provider setup design).

## Wiring (`cmd/elencode`)

- `loadProviders`: one `NewCustom` per entry in `CustomProviders`, keyed by its id.
- **Listing:** a new interface beside `providerSet`, held only for custom providers:

  ```go
  // modelLister is a provider that is asked which models it has, rather than
  // having them shipped. Only custom providers are.
  type modelLister interface {
  	ListModels(ctx context.Context) ([]string, error)
  }
  ```

- **`/model` with no argument:** if there are custom providers, `chooseModel` returns a
  command that lists all of them concurrently, with a 5-second timeout each. The spinner
  line says `listing models…` meanwhile. The result message opens the picker on the
  upstream catalog plus whatever came back. A provider that failed is a notice
  (`ollama: cannot list models: …`), not a failure of `/model`. Nothing is cached:
  `/model` is rare, and a local server's model set changes as models are pulled.
- **`/model ollama/<id>`** and a saved `model` resolve without listing, through the
  qualified-name rule. Startup stays offline.
- **`elencode model`** lists the same way, synchronously, and prints listing failures
  to the same writer as notices.
- **`defaultModel`:** custom providers are never a default, since that would need a
  listing at startup. A config whose only credentials are custom providers and which
  names no `model` fails with
  `no model chosen: pick one with /model, or name one in "model" (elencode model lists them)`.
  Proposed tweak: start anyway and open the picker. See Open questions.
- **`selectModel`:** no `(no thinking: unknown model)` notice for a custom provider's
  model. Not thinking is expected there, so the notice would only be noise.
- **`configSettings`:** one row per custom provider:
  `ollama   responses  http://localhost:11434/v1  (no key)` and
  `gpu-box  messages   https://llm.example.com    (key from $GPU_BOX_API_KEY)`.
- **CLAUDE.md:** "which models exist is shipped, not fetched" gains "for upstream
  providers; a custom provider's server is asked". The layout section gains the
  custom provider clients.

## Testing (red/green TDD)

- `config`: the mapping loads and round-trips through `Save`; each invalid case above
  fails `Load` with its message; a config with only custom providers loads.
- `openai` / `anthropic`, against `httptest` servers:
  - requests go to `<base>/responses` and `<base>/v1/messages`;
  - no `Authorization` without a key, `Bearer <key>` with one, and never `x-api-key`;
  - no reasoning or thinking parameter is sent;
  - `ListModels` parses an OpenAI-shaped and an Anthropic-shaped `/models` reply;
  - a refused connection (a closed listener's address) comes back unwrapped, not
    `RetryableError`, and names the URL.
- `agent`: `FindModel` resolves `anyname/id`.
- `cmd/elencode`: `/model` lists a fake lister's models next to the catalog; a failing
  lister becomes a notice and the picker still opens; `/model ollama/x` selects without
  listing; an unknown prefix says so rather than "no API key"; an unset `api_key_env`
  makes the provider unreachable with that reason; the config view rows.
- Manually, once: Ollama with `api: responses` and with `api: messages`, a turn that
  calls a tool, then switch to an upstream model and back.

## Open questions

1. **Only custom providers and no `model`:** with first-start onboarding in place, open
   the session on the model picker rather than failing?
2. **Bearer for `messages` too** (proposed), or `x-api-key` as Anthropic's own API uses?
   llama.cpp takes either; a gateway may want one or the other.

## Appendix: user documentation draft

To become the custom providers section of `docs/providers.md`.

### Custom providers

A custom provider is a server you run, on this machine or another, that speaks either
the **OpenAI Responses API** or the **Anthropic Messages API**. Add it to `config.json`
under `custom_providers`. The key you give it is the provider id its models are written
with:

```json
{
  "custom_providers": {
    "ollama": {
      "name": "Ollama on this machine",
      "api": "responses",
      "base_url": "http://localhost:11434/v1"
    }
  }
}
```

Then pick one of its models with `/model`, or name one directly:
`/model ollama/qwen3-coder:30b`.

| Field | Required | Meaning |
|---|---|---|
| `api` | yes | `responses` (OpenAI Responses API) or `messages` (Anthropic Messages API) |
| `base_url` | yes | for `responses`, the URL up to and including `/v1`; for `messages`, the server root **without** `/v1` |
| `name` | no | what the picker and `/config` call it; defaults to the id |
| `api_key_env` | no | the environment variable holding the key |

The id may not contain `/`, and may not be `anthropic`, `openai` or `chatgpt`.

#### Which API

Pick whichever your server supports. Both work for tool calling:

| Server | `api: responses` | `api: messages` |
|---|---|---|
| Ollama (≥ 0.14) | `http://localhost:11434/v1` | `http://localhost:11434` |
| llama.cpp `llama-server` | `http://localhost:8080/v1` | `http://localhost:8080` |
| LM Studio (≥ 0.4.1) | `http://localhost:1234/v1` | `http://localhost:1234` |
| vLLM | `http://localhost:8000/v1` | — |

#### Models

You do not list a custom provider's models. Opening `/model`, or running
`elencode model`, asks each custom provider's server which models it has
(`GET <base_url>/models`). A server that does not answer is reported, and the picker
opens with the rest.

Naming a model directly (`/model ollama/qwen3-coder:30b`), or having it saved as
`model`, does not ask the server, so a session starts even while the server is down.
The first message then fails at once with `cannot reach … is the server running?`. A
refused connection is not retried.

Custom provider models are assumed not to reason: elencode sends no reasoning or
thinking settings to them, whatever `thinking_enabled` says. Reasoning may still be
shown when a server returns it unasked.

If you have no upstream credential and no `model` set, elencode cannot pick a model for
you without asking a server. Run `elencode model` to see what your servers have, then
`elencode model ollama/<id>` to choose one.

#### Authentication

A server on your own machine usually needs no key: leave it out, and no
`Authorization` header is sent.

For a server that requires one, the key is sent as `Authorization: Bearer <key>`. Give
it either through an environment variable named by `api_key_env`:

```json
"gpu-box": {
  "api": "messages",
  "base_url": "https://llm.example.com",
  "api_key_env": "GPU_BOX_API_KEY"
}
```

or in `credentials.json`, under the provider's id:

```json
{ "gpu-box": { "api_key": "…" } }
```

When `api_key_env` is set, it wins. If that variable is unset, the provider is
unavailable rather than tried without a key, and `/model` says why.

Keys are never read from `config.json`.

#### Examples

**Ollama**, local:

```sh
OLLAMA_CONTEXT_LENGTH=32768 ollama serve
ollama pull qwen3-coder:30b
```

```json
"ollama": { "api": "responses", "base_url": "http://localhost:11434/v1" }
```

**llama.cpp**, on another machine, with a key:

```sh
llama-server -m qwen3-coder-30b.gguf -c 65536 --host 0.0.0.0 --api-key "$GPU_BOX_API_KEY"
```

```json
"gpu-box": {
  "name": "llama.cpp on the GPU box",
  "api": "messages",
  "base_url": "http://gpu-box.lan:8080",
  "api_key_env": "GPU_BOX_API_KEY"
}
```

**LM Studio**: start the server from the Developer tab, or with `lms server start`.

```json
"lmstudio": { "api": "responses", "base_url": "http://localhost:1234/v1" }
```

**vLLM**, which needs tool calling switched on, with the parser for your model:

```sh
vllm serve Qwen/Qwen3-Coder-30B-A3B-Instruct --api-key "$VLLM_API_KEY" \
  --enable-auto-tool-choice --tool-call-parser qwen3_coder
```

```json
"vllm": {
  "api": "responses",
  "base_url": "http://localhost:8000/v1",
  "api_key_env": "VLLM_API_KEY"
}
```

Model ids with a slash in them are written in full after the provider:
`/model vllm/Qwen/Qwen3-Coder-30B-A3B-Instruct`.

#### Troubleshooting

- **Tool calls break, or the model forgets what it read.** The server's context window is
  too small and it is silently dropping the start of the conversation. Ollama defaults
  to 4k tokens on machines with less than 24 GiB of VRAM. Raise it on the server:
  `OLLAMA_CONTEXT_LENGTH` for Ollama, `-c` for llama.cpp, the context length setting
  when loading a model in LM Studio, `--max-model-len` for vLLM. elencode cannot set it
  per request.
- **A 404 on every request.** Check `base_url` against the table above. `responses`
  wants the `/v1`; `messages` must not have it.
- **`cannot reach … is the server running?`** Nothing is listening at `base_url`. For a
  server on another machine, check it listens on more than `127.0.0.1` (`--host 0.0.0.0`
  for llama.cpp, `OLLAMA_HOST=0.0.0.0` for Ollama).
- **The first reply takes a long time.** The server is loading the model into memory.
  Later replies are faster.
- **A remote server over plain `http://`** sends your key unencrypted. Prefer `https://`,
  or an SSH tunnel: `ssh -L 8080:localhost:8080 gpu-box`, then use
  `http://localhost:8080`.
- **A self-signed certificate.** Point `SSL_CERT_FILE` at it, or at a bundle containing
  it.
