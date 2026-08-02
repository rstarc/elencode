# OpenAI Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add OpenAI (Responses API) as a second `agent.Provider`, selectable by config, at parity with the Anthropic path including reasoning that round-trips.

**Architecture:** A new `internal/provider/openai` package implements the two-method `agent.Provider` interface, mirroring `internal/provider/anthropic` structurally (a `newWithOptions` test seam, a ctx-cancellable `Stream` goroutine, pure `to*` converters). The provider stays stateless (`store:false`, full input replay every turn); the `Agent` keeps owning the context window. "Effort" becomes a first-class provider-neutral concept in `internal/agent`, and both providers honor it. `cmd/elencode/main.go` gains a `switch cfg.Provider` factory. Both providers get an end-to-end streaming test against an SSE stub, and the OpenAI package additionally gets a full agent-loop integration test that proves the reasoning/tool round-trip in the request bytes.

**Tech Stack:** Go 1.26, `github.com/openai/openai-go v1.12.0` (already fetched), `github.com/anthropics/anthropic-sdk-go v1.57.0`, Bubble Tea TUI, standard-library tests + `httptest` SSE stubs.

## Global Constraints

- Build/test only via the Makefile: `make build`, `make test` (`go vet ./...` + `go test -race ./...`). `make test` must pass before any task is considered done.
- `internal/agent` must not import a provider SDK. Vendor-specific translation lives inside `internal/provider/...`.
- Tests use the standard library only (plus `teatest` for the TUI). Hand-written fakes in the test file that needs them; no mocking library. Stub the SDK by pointing it at an `httptest` server via `option.WithBaseURL`.
- No new third-party dependencies beyond `openai-go` (already approved and fetched).
- Return errors instead of panicking, including in conversion code. Silently mislabeling (e.g. sending an unknown role as `user`) is worse than erroring.
- Sum types are an interface with an unexported marker method (see `agent.Event`, `agent.Block`).
- Commit style: imperative, capitalized subject, no `type:` prefix (e.g. "Add OpenAI provider skeleton"). End every commit message with the trailer:
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`
- Do not `git push`. Commit locally on branch `openai-provider`.

## Verified SDK facts (use these exact names — all checked against the module sources)

**OpenAI Responses (`github.com/openai/openai-go` v1.12.0, pkg `responses`, plus `shared`, top-level `openai` for `openai.String/Int/Bool`):**
- Call: `client.Responses.NewStreaming(ctx, responses.ResponseNewParams{...})` → `*ssestream.Stream[responses.ResponseStreamEventUnion]`.
- Request fields: `Model shared.ResponsesModel` (`= string`), `MaxOutputTokens param.Opt[int64]` (`openai.Int(n)`), `Store param.Opt[bool]` (`openai.Bool(false)`), `Input responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam{...}}`, `Tools []responses.ToolUnionParam`, `Reasoning shared.ReasoningParam`, `Include []responses.ResponseIncludable`.
- Input item constructors (all return `responses.ResponseInputItemUnionParam`): `ResponseInputItemParamOfFunctionCall(arguments, callID, name string)`, `ResponseInputItemParamOfFunctionCallOutput(callID, output string)`. For plain messages use `responses.EasyInputMessageParam{Role: ..., Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)}}` wrapped as `responses.ResponseInputItemUnionParam{OfMessage: &easyMsg}`. Roles: `EasyInputMessageRoleUser`, `EasyInputMessageRoleAssistant`, `EasyInputMessageRoleSystem` (and `Developer`) all exist. For reasoning round-trip build the union directly: `responses.ResponseInputItemUnionParam{OfReasoning: &responses.ResponseReasoningItemParam{ID: id, EncryptedContent: openai.String(sig), Summary: summary}}` where `Summary []responses.ResponseReasoningItemSummaryParam{{Text: t}}`. **`Summary` is tagged `omitzero,required`: a nil slice is omitted from the JSON and the API rejects the item — always pass a non-nil slice.** `param.Opt[string]` exposes `.Value`.
- Reasoning request: `shared.ReasoningParam{Effort: shared.ReasoningEffortLow|Medium|High, Summary: shared.ReasoningSummaryAuto}`. **`ReasoningEffort` has only low/medium/high in v1.12.0 (no `minimal`).**
- Encrypted round-trip: add `Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}` (wire value `"reasoning.encrypted_content"`).
- Tools: **do not use `responses.ToolParamOfFunction`** — it sets only name/parameters/strict and silently drops the description the model picks tools by. Build `responses.FunctionToolParam{Name, Description param.Opt[string], Parameters map[string]any, Strict param.Opt[bool]}` directly and wrap it in `responses.ToolUnionParam{OfFunction: &fn}`.
- Stream events (`stream.Current().AsAny().(type)`): `responses.ResponseTextDeltaEvent{Delta string}`, `responses.ResponseReasoningSummaryTextDeltaEvent{Delta string}`, `responses.ResponseCompletedEvent{Response responses.Response}`. The union dispatches on the JSON `type` field. Check `stream.Err()` after the loop.
- SSE framing: the SDK decoder unmarshals each `data:` payload directly into the union keyed on its JSON `type` field; `event:` lines are optional. A stub that writes only `data: {...}\n\n` lines decodes fine.
- Final `responses.Response`: `.Output []responses.ResponseOutputItemUnion`, `.Status responses.ResponseStatus` (`ResponseStatusCompleted`/`ResponseStatusIncomplete`), `.IncompleteDetails.Reason` is a **plain `string`** (`"max_output_tokens"` or `"content_filter"`).
- Output item variants (`item.AsAny().(type)`): `responses.ResponseOutputMessage` (`.Content []ResponseOutputMessageContentUnion`, each `part.AsAny()` → `ResponseOutputText{Text}` or `ResponseOutputRefusal{Refusal}`), `responses.ResponseFunctionToolCall{CallID, Name, Arguments string, ID string}`, `responses.ResponseReasoningItem{ID string, Summary []ResponseReasoningItemSummary, EncryptedContent string}`.
- Models: `client.Models.ListAutoPaging(ctx)` (no params argument; pager of `openai.Model{ID string}`).
- The SDK retries 5xx by default; tests that stub an error response need `option.WithMaxRetries(0)`.

**Anthropic (`anthropic-sdk-go` v1.57.0, alias `sdk`):**
- `sdk.MessageNewParams.OutputConfig sdk.OutputConfigParam` (`.Effort sdk.OutputConfigEffort`, constants `OutputConfigEffortLow/Medium/High/Xhigh/Max`).
- Model capability: `info.Capabilities.Effort.Supported bool` and per-level `Low/Medium/High/Xhigh/Max` (each a `CapabilitySupport`). These are plain struct fields, so a struct literal works in tests (unlike the union types, which need JSON decoding).

---

## File structure

- `internal/agent/blocks.go` — modify: add `ID` to `ThinkingBlock`.
- `internal/agent/provider.go` — modify: add `Effort` type + values, add `ThinkingEffort` to `ThinkingMode`.
- `internal/agent/effort_test.go` — create.
- `internal/config/config.go` — modify: `Provider`, `OpenAIAPIKey`, `ThinkingEffort`, per-key env provenance, provider-aware validation.
- `internal/config/config_test.go` — extend (reuse the existing `writeConfig` helper — **do not redefine it**).
- `cmd/elencode/configview.go`, `cmd/elencode/configview_test.go`, `cmd/elencode/tui_test.go` — modify: `APIKeyFromEnv` rename fallout.
- `internal/provider/anthropic/anthropic.go` — modify: effort (`New` arg, `toModel`, `messageParams`).
- `internal/provider/anthropic/anthropic_test.go` — modify: updated `newWithOptions` calls, effort assertions, new SSE streaming test.
- `internal/provider/openai/openai.go` — create: the provider.
- `internal/provider/openai/openai_test.go` — create: SSE-stub tests + agent-loop integration test.
- `cmd/elencode/main.go` — modify: `providerFromConfig` factory + effort wiring.
- `cmd/elencode/main_test.go` — create.

---

## Task 1: Shared effort + reasoning-id types (`internal/agent`)

**Files:**
- Modify: `internal/agent/provider.go`
- Modify: `internal/agent/blocks.go`
- Test: `internal/agent/effort_test.go` (create)

**Interfaces:**
- Produces: `agent.Effort` (string enum: `EffortNone, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax`); `agent.ThinkingEffort ThinkingMode`; `agent.ThinkingBlock.ID string`.

There is deliberately **no `EffortMinimal`**: neither pinned SDK can express it (openai-go v1.12.0 tops out at low/medium/high; Anthropic has low..max), so it would be a dead value clamped to `low` everywhere. Add it when an SDK can carry it.

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/effort_test.go
package agent

import "testing"

func TestEffortWireValues(t *testing.T) {
	cases := map[Effort]string{
		EffortNone: "", EffortLow: "low", EffortMedium: "medium",
		EffortHigh: "high", EffortXHigh: "xhigh", EffortMax: "max",
	}
	for e, want := range cases {
		if string(e) != want {
			t.Errorf("Effort %q: got %q, want %q", e, string(e), want)
		}
	}
}

func TestThinkingBlockCarriesID(t *testing.T) {
	b := ThinkingBlock{Thinking: "t", Signature: "s", ID: "rs_1"}
	if b.ID != "rs_1" {
		t.Fatalf("ID not carried: %q", b.ID)
	}
	if ThinkingEffort != ThinkingMode("effort") {
		t.Fatalf("ThinkingEffort = %q", ThinkingEffort)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — `EffortNone`, `ThinkingEffort`, and `ThinkingBlock.ID` undefined.

- [ ] **Step 3: Add the types**

In `internal/agent/provider.go`, add after the `ThinkingMode` consts:

```go
const (
	// ThinkingEffort: the model reasons at a discrete effort level the caller
	// picks (OpenAI reasoning_effort; Anthropic OutputConfig.Effort).
	ThinkingEffort ThinkingMode = "effort"
)

// Effort is how hard an effort-based model is asked to reason. Providers clamp
// it to the levels their own API accepts and treat the zero value as their
// default, medium.
type Effort string

const (
	EffortNone   Effort = ""
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)
```

In `internal/agent/blocks.go`, add `ID` to `ThinkingBlock`:

```go
type ThinkingBlock struct {
	Thinking  string
	Signature string
	// ID is an opaque provider handle for the reasoning item, carried only so
	// the block round-trips. Anthropic leaves it empty; OpenAI stores the
	// reasoning item id, which must accompany Signature when re-sent.
	ID string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/provider.go internal/agent/blocks.go internal/agent/effort_test.go
git commit  # "Add Effort type and reasoning-item id to agent"
```

---

## Task 2: Config — provider selection, OpenAI key, effort (`internal/config`)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go` (extend — the file exists and already has `writeConfig`/`readConfig` helpers; **reuse them, do not redefine**)
- Modify: `cmd/elencode/configview.go`, `cmd/elencode/configview_test.go`, `cmd/elencode/tui_test.go` (provenance-field rename fallout)

**Interfaces:**
- Produces: `config.Config.Provider string`, `config.Config.OpenAIAPIKey Secret`, `config.Config.ThinkingEffort string`; per-key provenance `AnthropicKeyFromEnv`/`OpenAIKeyFromEnv` (replacing `APIKeyFromEnv`); constants `config.ProviderAnthropic = "anthropic"`, `config.ProviderOpenAI = "openai"`, `config.OPENAI_API_KEY_ENV_VAR_NAME`. `Load` validates the selected provider's key, the provider name, and the effort level.

**Why per-key provenance:** a single `APIKeyFromEnv` bool cannot say *which* key came from the environment. With `provider:"openai"` and `ANTHROPIC_API_KEY` exported, `Load` puts the env's Anthropic key into the config value; a `Save` guarded by one selected-provider bool would then write that secret into the file — exactly what `TestSaveDoesNotWriteTheEnvironmentsAPIKey` exists to prevent. Each key tracks its own provenance and `Save` blanks each env-supplied key.

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go` (note: these use the **existing** `writeConfig` helper, which redirects `XDG_CONFIG_HOME`/`HOME`, and the existing `readConfig`):

```go
func TestLoadReadsTheOpenAIProviderSettings(t *testing.T) {
	writeConfig(t, `{"provider":"openai","openai_api_key":"sk-oai","thinking_effort":"high"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider != ProviderOpenAI || cfg.OpenAIAPIKey.Reveal() != "sk-oai" || cfg.ThinkingEffort != "high" {
		t.Fatalf("bad cfg: %+v", cfg)
	}
}

// TestLoadValidatesTheSelectedProvidersKey: an anthropic key on file must not
// satisfy the check when openai is the selected provider.
func TestLoadValidatesTheSelectedProvidersKey(t *testing.T) {
	writeConfig(t, `{"provider":"openai","anthropic_api_key":"sk-ant"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded without the selected provider's key")
	}
	if !strings.Contains(err.Error(), OPENAI_API_KEY_ENV_VAR_NAME) {
		t.Errorf("err = %q, want it to name the openai key", err)
	}
}

func TestLoadDefaultsProviderToAnthropic(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider != ProviderAnthropic {
		t.Fatalf("provider = %q, want anthropic", cfg.Provider)
	}
	if cfg.ThinkingEffort != "medium" {
		t.Fatalf("thinking_effort = %q, want the medium default", cfg.ThinkingEffort)
	}
}

func TestLoadAppliesTheOpenAIKeyFromTheEnvironment(t *testing.T) {
	writeConfig(t, `{"provider":"openai","openai_api_key":"from-file"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "sk-oai-env")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAIAPIKey.Reveal() != "sk-oai-env" {
		t.Error("OpenAIAPIKey is not the value from the environment")
	}
	if !cfg.OpenAIKeyFromEnv {
		t.Error("OpenAIKeyFromEnv is false, want true")
	}
}

// TestSaveWritesNeitherEnvironmentKey: with openai selected and BOTH env vars
// set, Save must not persist either environment secret — including the key of
// the provider that is not selected. One shared provenance bool cannot express
// this, which is why each key tracks its own.
func TestSaveWritesNeitherEnvironmentKey(t *testing.T) {
	file := writeConfig(t, `{"provider":"openai","anthropic_api_key":"ant-file","openai_api_key":"oai-file"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "ant-env")
	t.Setenv(OPENAI_API_KEY_ENV_VAR_NAME, "oai-env")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	saved := readConfig(t, file)
	if saved["anthropic_api_key"] != "ant-file" {
		t.Errorf("anthropic_api_key = %v, want the file's own value kept", saved["anthropic_api_key"])
	}
	if saved["openai_api_key"] != "oai-file" {
		t.Errorf("openai_api_key = %v, want the file's own value kept", saved["openai_api_key"])
	}
}

func TestLoadRejectsUnknownProvider(t *testing.T) {
	writeConfig(t, `{"provider":"nope","anthropic_api_key":"`+realKey+`"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an unknown provider")
	}
}

// TestLoadRejectsUnknownThinkingEffort: a typo like "hihg" must fail loudly
// rather than silently clamping to medium.
func TestLoadRejectsUnknownThinkingEffort(t *testing.T) {
	writeConfig(t, `{"anthropic_api_key":"`+realKey+`","thinking_effort":"turbo"}`)
	t.Setenv(ANTHROPIC_API_KEY_ENV_VAR_NAME, "")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an unknown thinking_effort")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — `ProviderOpenAI`, `OPENAI_API_KEY_ENV_VAR_NAME`, `OpenAIAPIKey`, `OpenAIKeyFromEnv`, `ThinkingEffort` undefined.

- [ ] **Step 3: Implement**

In `internal/config/config.go`:
- Add fields to `Config` (key/effort fields `omitempty` to preserve merge-save semantics; `Provider` too):

```go
Provider       string `json:"provider,omitempty"`
OpenAIAPIKey   Secret `json:"openai_api_key,omitempty"`
ThinkingEffort string `json:"thinking_effort,omitempty"`
```

- Replace the single provenance bool with one per key (still `json:"-"`):

```go
AnthropicKeyFromEnv bool `json:"-"` // the environment overrode the file value
OpenAIKeyFromEnv    bool `json:"-"`
```

- Add constants:

```go
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)
const OPENAI_API_KEY_ENV_VAR_NAME = "OPENAI_API_KEY"
```

- `Load`: defaults become `Config{ThinkingEnabled: true, ThinkingEffort: "medium", Provider: ProviderAnthropic}`. After unmarshal, normalize an explicitly empty `provider`/`thinking_effort` back to the defaults. Apply **both** env overrides, each to its own field with its own provenance bool (unconditionally, not just for the selected provider — the value is real either way; what matters is that `Save` never writes it):

```go
if val, ok := os.LookupEnv(ANTHROPIC_API_KEY_ENV_VAR_NAME); ok && val != "" {
	cfg.AnthropicAPIKey = Secret(val)
	cfg.AnthropicKeyFromEnv = true
}
if val, ok := os.LookupEnv(OPENAI_API_KEY_ENV_VAR_NAME); ok && val != "" {
	cfg.OpenAIAPIKey = Secret(val)
	cfg.OpenAIKeyFromEnv = true
}
```

- Then validate, selected provider first, then effort:

```go
switch cfg.Provider {
case ProviderAnthropic:
	if cfg.AnthropicAPIKey == "" {
		return cfg, fmt.Errorf("API key not set: provide %q in the environment or %q in %q", ANTHROPIC_API_KEY_ENV_VAR_NAME, "anthropic_api_key", configFilePath)
	}
case ProviderOpenAI:
	if cfg.OpenAIAPIKey == "" {
		return cfg, fmt.Errorf("API key not set: provide %q in the environment or %q in %q", OPENAI_API_KEY_ENV_VAR_NAME, "openai_api_key", configFilePath)
	}
default:
	return cfg, fmt.Errorf("unknown provider %q in %q (valid: %q, %q)", cfg.Provider, configFilePath, ProviderAnthropic, ProviderOpenAI)
}

switch cfg.ThinkingEffort {
case "low", "medium", "high", "xhigh", "max":
default:
	return cfg, fmt.Errorf("unknown thinking_effort %q in %q (valid: low, medium, high, xhigh, max)", cfg.ThinkingEffort, configFilePath)
}
```

- `Save`: blank each env-supplied key:

```go
if c.AnthropicKeyFromEnv {
	c.AnthropicAPIKey = ""
}
if c.OpenAIKeyFromEnv {
	c.OpenAIAPIKey = ""
}
```

- Rename fallout for `APIKeyFromEnv` → `AnthropicKeyFromEnv` (found by grep, all five files):
  - `internal/config/config_test.go` — `TestLoadRecordsFileAsTheSource`, `TestLoadRecordsEnvironmentAsTheSource`.
  - `cmd/elencode/configview.go:16` — the source label for the anthropic key row.
  - `cmd/elencode/configview_test.go:46`, `cmd/elencode/tui_test.go:421` — struct literals.

> **Adjacent work, do not do here:** the config view only shows the anthropic key row; showing `provider`, `openai_api_key`, and `thinking_effort` there is a separate change. Mention it in the commit body instead of widening this task.

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS, including the pre-existing config and configview tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config cmd/elencode/configview.go cmd/elencode/configview_test.go cmd/elencode/tui_test.go
git commit  # "Add provider selection and OpenAI key to config"
```

---

## Task 3: Anthropic effort migration (`internal/provider/anthropic`)

**Files:**
- Modify: `internal/provider/anthropic/anthropic.go`
- Modify: `internal/provider/anthropic/anthropic_test.go`
- Modify: `cmd/elencode/main.go` (single `anthropic.New` call site)

**Interfaces:**
- Consumes: `agent.Effort`, `agent.ThinkingEffort` (Task 1).
- Produces: `anthropic.New(apiKey string, thinking bool, effort agent.Effort) *Client`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/provider/anthropic/anthropic_test.go`:

```go
func TestToModelPrefersEffortCapability(t *testing.T) {
	info := sdk.ModelInfo{ID: "claude-x", DisplayName: "Claude X"}
	info.Capabilities.Effort.Supported = true
	if got := toModel(info); got.Thinking != agent.ThinkingEffort {
		t.Fatalf("Thinking = %q, want effort", got.Thinking)
	}
}

func TestMessageParamsSetsEffort(t *testing.T) {
	c := newWithOptions("key", true, agent.EffortHigh)
	m := agent.Model{ID: "claude-x", Thinking: agent.ThinkingEffort}
	params := c.messageParams(agent.Request{Model: m, MaxTokens: 10}, nil)
	if params.OutputConfig.Effort != sdk.OutputConfigEffortHigh {
		t.Fatalf("effort = %q, want high", params.OutputConfig.Effort)
	}
}

func TestEffortIsNotRequestedWhenThinkingDisabled(t *testing.T) {
	c := newWithOptions("key", false, agent.EffortHigh)
	m := agent.Model{ID: "claude-x", Thinking: agent.ThinkingEffort}
	params := c.messageParams(agent.Request{Model: m, MaxTokens: 10}, nil)
	if params.OutputConfig.Effort != "" {
		t.Fatalf("effort = %q, want it left out of the request", params.OutputConfig.Effort)
	}
}

func TestToAnthropicEffortClampsToKnownLevels(t *testing.T) {
	tests := map[agent.Effort]sdk.OutputConfigEffort{
		agent.EffortNone:   sdk.OutputConfigEffortMedium,
		agent.EffortLow:    sdk.OutputConfigEffortLow,
		agent.EffortMedium: sdk.OutputConfigEffortMedium,
		agent.EffortHigh:   sdk.OutputConfigEffortHigh,
		agent.EffortXHigh:  sdk.OutputConfigEffortXhigh,
		agent.EffortMax:    sdk.OutputConfigEffortMax,
	}
	for in, want := range tests {
		if got := toAnthropicEffort(in); got != want {
			t.Errorf("toAnthropicEffort(%q) = %q, want %q", in, got, want)
		}
	}
}
```

Update the signature of **every** existing `newWithOptions("key", <bool>...)` and `New(...)` call in the test file to pass an effort argument (use `agent.EffortMedium` where effort is irrelevant to the assertion).

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — `newWithOptions`/`New` arity, `toAnthropicEffort` undefined.

- [ ] **Step 3: Implement**

In `anthropic.go`:
- Add `effort agent.Effort` to the `Client` struct; thread it through `New` and `newWithOptions`.
- `toModel`: prefer effort when supported:

```go
switch {
case info.Capabilities.Effort.Supported:
	model.Thinking = agent.ThinkingEffort
case thinking.Types.Adaptive.Supported:
	model.Thinking = agent.ThinkingAdaptive
case thinking.Types.Enabled.Supported:
	model.Thinking = agent.ThinkingBudgeted
}
```

- `messageParams`: when `c.thinking` and `req.Model.Thinking == agent.ThinkingEffort`, set `OutputConfig`:

```go
if c.thinking && req.Model.Thinking == agent.ThinkingEffort {
	params.OutputConfig = sdk.OutputConfigParam{Effort: toAnthropicEffort(c.effort)}
}
```

- Add the clamp (zero value falls to the provider default, medium):

```go
func toAnthropicEffort(e agent.Effort) sdk.OutputConfigEffort {
	switch e {
	case agent.EffortLow:
		return sdk.OutputConfigEffortLow
	case agent.EffortHigh:
		return sdk.OutputConfigEffortHigh
	case agent.EffortXHigh:
		return sdk.OutputConfigEffortXhigh
	case agent.EffortMax:
		return sdk.OutputConfigEffortMax
	default:
		return sdk.OutputConfigEffortMedium
	}
}
```

- In `cmd/elencode/main.go`, update the existing call to `anthropic.New(cfg.AnthropicAPIKey.Reveal(), cfg.ThinkingEnabled, agent.Effort(cfg.ThinkingEffort))`.

> **Verify during this task:** whether an effort model also needs the `Thinking` param set for reasoning summaries to stream. If summaries stop appearing, keep `params.Thinking = c.thinkingParam(...)` alongside `OutputConfig` for `ThinkingEffort` models. Confirm against a live call before finalizing; note the result in the commit body.

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/anthropic cmd/elencode/main.go
git commit  # "Honor effort in the Anthropic provider"
```

---

## Task 4: Anthropic streaming path under test (`internal/provider/anthropic`)

The anthropic suite covers converters and `Models`, but nothing drives `Stream` itself — the goroutine, the accumulate loop, and the terminal `ResponseEvent` are untested. Before mirroring that structure in a second provider, put the original under the same test the new one will get.

**Files:**
- Modify: `internal/provider/anthropic/anthropic_test.go`

**Interfaces:** none new; covers the existing `Stream`.

- [ ] **Step 1: Write the failing test**

```go
// collectEvents drains a Stream until it closes, failing if it hangs.
func collectEvents(t *testing.T, ch <-chan agent.Event) []agent.Event {
	t.Helper()
	var out []agent.Event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timeout:
			t.Fatalf("stream did not end, got %d events so far", len(out))
		}
	}
}

// TestStreamAssemblesAResponseFromSSE drives Stream end-to-end through the SDK
// against a scripted server — the only test that exercises the streaming path
// itself rather than the converters it is built from.
func TestStreamAssemblesAResponseFromSSE(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			var typed struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(e), &typed); err != nil {
				t.Errorf("bad scripted event %s: %v", e, err)
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typed.Type, e)
		}
	}))
	defer server.Close()

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	got := collectEvents(t, c.Stream(context.Background(), agent.Request{
		Model:     agent.Model{ID: "claude-x"},
		MaxTokens: 100,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	var text string
	var resp *agent.ResponseEvent
	for _, e := range got {
		switch e := e.(type) {
		case agent.TextDeltaEvent:
			text += e.Text
		case agent.ResponseEvent:
			r := e
			resp = &r
		case agent.ErrorEvent:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}
	if text != "Hello" {
		t.Errorf("streamed text = %q, want Hello", text)
	}
	if resp == nil || resp.Response.StopReason != agent.StopReasonEndTurn {
		t.Fatalf("resp = %+v, want an end_turn ResponseEvent", resp)
	}
	want := agent.TextBlock{Text: "Hello"}
	if len(resp.Response.Message.Content) != 1 || resp.Response.Message.Content[0] != want {
		t.Errorf("content = %#v, want [%#v]", resp.Response.Message.Content, want)
	}
}
```

> The anthropic decoder is framed here with both `event:` and `data:` lines, matching what the real API sends. Confirm the stub decodes while making this pass; if the SDK insists on anything more (e.g. a `ping` event), add it to the script.

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL (or hang caught by the 2s timeout) until the framing is right — the assertions on text/stop reason are the red step. If it passes immediately, the streaming path was already correct; keep the test as regression cover and move on.

- [ ] **Step 3: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/provider/anthropic/anthropic_test.go
git commit  # "Cover the Anthropic streaming path with an SSE stub"
```

---

## Task 5: OpenAI provider — text-only turn (`internal/provider/openai`)

**Files:**
- Create: `internal/provider/openai/openai.go`
- Create: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: `agent.Request/Event/Message/Block`, `agent.Effort` (Task 1).
- Produces: `openai.New(apiKey string, thinking bool, effort agent.Effort) *Client`; `newWithOptions(apiKey string, thinking bool, effort agent.Effort, opts ...option.RequestOption) *Client`; `(*Client).Stream(ctx, agent.Request) <-chan agent.Event`; `DefaultModelID() string`.

- [ ] **Step 1: Write the failing test**

The test helpers are the backbone of every later task: a stub that **records request bodies** (so tests assert on what was sent, not just what came back) and a timeout-bounded `collect` (a bare `for range ch` hangs the whole test binary if the stub misframes).

```go
// internal/provider/openai/openai_test.go
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/rstarc/elencode/internal/agent"
)

// stub serves one canned SSE body per request and records the JSON body of
// each request, so tests can assert on what was sent as well as what came back.
type stub struct {
	t     *testing.T
	mu    sync.Mutex
	turns []string
	calls int
	sent  []map[string]any
}

func newStub(t *testing.T, turns ...string) (*stub, string) {
	s := &stub{t: t, turns: turns}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return s, server.URL
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.t.Errorf("request %d body did not decode: %v", s.calls, err)
	}
	s.sent = append(s.sent, body)

	if s.calls >= len(s.turns) {
		http.Error(w, "unscripted request", http.StatusInternalServerError)
		return
	}
	events := s.turns[s.calls]
	s.calls++

	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, events)
}

// body returns the recorded JSON of request i.
func (s *stub) body(i int) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent[i]
}

// sse frames events as a text/event-stream body. The SDK decoder unmarshals
// each data: payload keyed on its JSON "type" field; event: lines are optional.
// Every event must be a single line of JSON.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "data: %s\n\n", e)
	}
	return b.String()
}

// collect drains events until the channel closes, failing if the stream hangs.
func collect(t *testing.T, ch <-chan agent.Event) []agent.Event {
	t.Helper()
	var out []agent.Event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timeout:
			t.Fatalf("stream did not end, got %d events so far", len(out))
		}
	}
}

func TestStreamTextOnly(t *testing.T) {
	s, url := newStub(t, sse(
		`{"type":"response.output_text.delta","delta":"Hello"}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}]}}`,
	))

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(url))
	req := agent.Request{
		Model:     agent.Model{ID: "gpt-5"},
		MaxTokens: 100,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}
	events := collect(t, c.Stream(context.Background(), req))

	var text string
	var resp *agent.ResponseEvent
	for _, e := range events {
		switch e := e.(type) {
		case agent.TextDeltaEvent:
			text += e.Text
		case agent.ResponseEvent:
			r := e
			resp = &r
		case agent.ErrorEvent:
			t.Fatalf("unexpected error: %v", e.Err)
		}
	}
	if text != "Hello" {
		t.Fatalf("text = %q", text)
	}
	if resp == nil || resp.Response.StopReason != agent.StopReasonEndTurn {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Response.Message.Content) != 1 {
		t.Fatalf("blocks = %+v", resp.Response.Message.Content)
	}

	// The provider must stay stateless and bounded; assert it in the bytes.
	body := s.body(0)
	if body["store"] != false {
		t.Errorf("store = %v, want false", body["store"])
	}
	if body["max_output_tokens"] != float64(100) {
		t.Errorf("max_output_tokens = %v, want 100", body["max_output_tokens"])
	}
	if body["model"] != "gpt-5" {
		t.Errorf("model = %v, want gpt-5", body["model"])
	}
}

func TestStreamSurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	// WithMaxRetries(0): the SDK retries 5xx by default, which would make this
	// test slow and assert nothing extra.
	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	events := collect(t, c.Stream(context.Background(), agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	if len(events) == 0 {
		t.Fatal("no events, want a terminal ErrorEvent")
	}
	if _, ok := events[len(events)-1].(agent.ErrorEvent); !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
}

func TestToInputMapsEveryRole(t *testing.T) {
	msgs := []agent.Message{
		{Role: agent.RoleSystem, Content: []agent.Block{agent.TextBlock{Text: "be brief"}}},
		agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}}),
		{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "hello"}}},
	}
	input, err := toInput(msgs)
	if err != nil {
		t.Fatal(err)
	}
	want := []responses.EasyInputMessageRole{
		responses.EasyInputMessageRoleSystem,
		responses.EasyInputMessageRoleUser,
		responses.EasyInputMessageRoleAssistant,
	}
	for i, role := range want {
		if input[i].OfMessage == nil || input[i].OfMessage.Role != role {
			t.Errorf("input[%d] = %+v, want role %q", i, input[i].OfMessage, role)
		}
	}
}

// A role we do not recognise must error, not silently become "user".
func TestToInputRejectsUnknownRole(t *testing.T) {
	_, err := toInput([]agent.Message{{Role: "wizard", Content: []agent.Block{agent.TextBlock{Text: "x"}}}})
	if err == nil || !strings.Contains(err.Error(), "wizard") {
		t.Fatalf("err = %v, want it to name the offending role", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — package `openai` has no `newWithOptions`/`Stream`/`toInput`.

- [ ] **Step 3: Implement the minimal package**

```go
// internal/provider/openai/openai.go
package openai

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/rstarc/elencode/internal/agent"
)

// eventBuffer is the capacity of the Event channel returned by Stream,
// mirroring the anthropic provider.
const eventBuffer = 64

const defaultModel = "gpt-5"

// DefaultModelID is the model used when configuration names none.
func DefaultModelID() string { return defaultModel }

type Client struct {
	client openai.Client
	// thinking and effort come from config and are fixed for the client's
	// life, like the anthropic provider's thinking flag.
	thinking bool
	effort   agent.Effort
}

func New(apiKey string, thinking bool, effort agent.Effort) *Client {
	return newWithOptions(apiKey, thinking, effort)
}

// newWithOptions is New with extra SDK options, which tests use to point the
// client at a stub server.
func newWithOptions(apiKey string, thinking bool, effort agent.Effort, opts ...option.RequestOption) *Client {
	opts = append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	return &Client{client: openai.NewClient(opts...), thinking: thinking, effort: effort}
}

// params builds the request for one round of inference. Stateless by design:
// store is off and the full input is replayed every turn, so the agent keeps
// owning the context window.
func (c *Client) params(req agent.Request, input responses.ResponseInputParam) responses.ResponseNewParams {
	p := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(req.Model.ID),
		MaxOutputTokens: openai.Int(req.MaxTokens),
		Store:           openai.Bool(false),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: input},
	}
	return p
}

// Stream sends every event through a select on ctx.Done, mirroring the
// anthropic provider: a send that is not cancellable leaks this goroutine
// whenever the consumer abandons the turn.
func (c *Client) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, eventBuffer)
	go func() {
		defer close(events)

		// Built before the request so a conversion failure surfaces as an
		// ErrorEvent instead of being discovered halfway through a stream.
		input, err := toInput(req.Messages)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		stream := c.client.Responses.NewStreaming(ctx, c.params(req, input))
		var final responses.Response
		for stream.Next() {
			switch e := stream.Current().AsAny().(type) {
			case responses.ResponseTextDeltaEvent:
				if !emit(ctx, events, agent.TextDeltaEvent{Text: e.Delta}) {
					return
				}
			case responses.ResponseCompletedEvent:
				final = e.Response
			}
		}
		if err := stream.Err(); err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}

		blocks, err := toBlocks(final)
		if err != nil {
			emit(ctx, events, agent.ErrorEvent{Err: err})
			return
		}
		emit(ctx, events, agent.ResponseEvent{Response: agent.Response{
			Message:    agent.Message{Role: agent.RoleAssistant, Content: blocks},
			StopReason: stopReason(final),
		}})
	}()
	return events
}

// emit sends ev unless the consumer has abandoned the turn. It reports whether
// the send happened, so a caller with more to produce can stop early.
func emit(ctx context.Context, events chan<- agent.Event, ev agent.Event) bool {
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func toRole(role agent.Role) (responses.EasyInputMessageRole, error) {
	switch role {
	case agent.RoleUser:
		return responses.EasyInputMessageRoleUser, nil
	case agent.RoleAssistant:
		return responses.EasyInputMessageRoleAssistant, nil
	case agent.RoleSystem:
		return responses.EasyInputMessageRoleSystem, nil
	default:
		return "", fmt.Errorf("unknown message role %q", role)
	}
}

// toInput reshapes agent messages into Responses input items. Text-only for now.
func toInput(msgs []agent.Message) (responses.ResponseInputParam, error) {
	var items responses.ResponseInputParam
	for _, msg := range msgs {
		role, err := toRole(msg.Role)
		if err != nil {
			return nil, err
		}
		for _, block := range msg.Content {
			switch b := block.(type) {
			case agent.TextBlock:
				items = append(items, responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{
					Role:    role,
					Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(b.Text)},
				}})
			default:
				return nil, fmt.Errorf("cannot send block of type %T to the API", block)
			}
		}
	}
	return items, nil
}

// toBlocks converts the final Response's output to agent blocks. Variants we
// do not handle yet are an error rather than a panic: the API may start
// returning one at any time, and the caller can render an error.
func toBlocks(resp responses.Response) ([]agent.Block, error) {
	var blocks []agent.Block
	for _, item := range resp.Output {
		switch it := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, part := range it.Content {
				switch p := part.AsAny().(type) {
				case responses.ResponseOutputText:
					blocks = append(blocks, agent.TextBlock{Text: p.Text})
				default:
					// Type, not %T: an unrecognised type string makes AsAny
					// return nil, which would report a useless "<nil>".
					return nil, fmt.Errorf("unsupported output content part %q", part.Type)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported output item type %q", item.Type)
		}
	}
	return blocks, nil
}

func stopReason(resp responses.Response) agent.StopReason {
	return agent.StopReasonEndTurn
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "Add OpenAI provider with text-only streaming"
```

---

## Task 6: OpenAI tool use + StopReason derivation

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: Task 5 (`toInput`, `toBlocks`, `stopReason`, `params`).
- Produces: `toInput` handles `ToolUseBlock`/`ToolResultBlock` (marking failed results); `toBlocks` emits `ToolUseBlock` and turns refusals into text; `params` sets `Tools` with descriptions; `stopReason` derives `MaxTokens`/`ToolUse`/`Refusal`/`EndTurn` — in that order.

**Ordering matters:** the incomplete/max-tokens check comes **before** the function-call scan. A response cut off by the token limit mid-tool-call would otherwise report `ToolUse`, and the agent loop would execute the tool with truncated argument JSON.

- [ ] **Step 1: Write the failing tests**

```go
func TestStreamToolUse(t *testing.T) {
	s, url := newStub(t, sse(
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"x\"}","id":"fc_1"}]}}`,
	))

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(url))
	req := agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Tools:    []agent.Tool{{Name: "read", Description: "read a file", InputSchema: agent.InputSchema{Type: "object"}}},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "read x"}})},
	}
	var resp agent.Response
	for _, e := range collect(t, c.Stream(context.Background(), req)) {
		if re, ok := e.(agent.ResponseEvent); ok {
			resp = re.Response
		}
	}
	if resp.StopReason != agent.StopReasonToolUse {
		t.Fatalf("stop = %q", resp.StopReason)
	}
	tu, ok := resp.Message.Content[0].(agent.ToolUseBlock)
	if !ok || tu.ID != "call_1" || tu.Name != "read" {
		t.Fatalf("tool use = %+v", resp.Message.Content[0])
	}

	// The model picks tools by description; sending it is not optional.
	// (ToolParamOfFunction drops it, which is why toTools builds the param
	// struct directly.)
	tools, ok := s.body(0)["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one", s.body(0)["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "read" || tool["description"] != "read a file" {
		t.Errorf("tool = %v, want name and description carried through", tool)
	}
}

func TestToInputExplodesToolResults(t *testing.T) {
	msgs := []agent.Message{
		agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "read x"}}),
		{Role: agent.RoleAssistant, Content: []agent.Block{agent.ToolUseBlock{ID: "call_1", Name: "read", Input: []byte(`{}`)}}},
		agent.NewUserMessage([]agent.Block{agent.NewToolResultBlock("call_1", "file body", false)}),
	}
	input, err := toInput(msgs)
	if err != nil {
		t.Fatal(err)
	}
	// Expect: user message, function_call, function_call_output (3 items).
	if len(input) != 3 {
		t.Fatalf("items = %d, want 3", len(input))
	}
	if input[1].OfFunctionCall == nil || input[1].OfFunctionCall.CallID != "call_1" {
		t.Fatalf("item[1] not a function_call: %+v", input[1])
	}
	if input[2].OfFunctionCallOutput == nil || input[2].OfFunctionCallOutput.CallID != "call_1" {
		t.Fatalf("item[2] not a function_call_output: %+v", input[2])
	}
}

// function_call_output has no error flag, so a failed tool must say so in the
// output itself — otherwise the model reads the failure text as a result.
func TestToInputMarksFailedToolResults(t *testing.T) {
	input, err := toInput([]agent.Message{
		agent.NewUserMessage([]agent.Block{agent.NewToolResultBlock("call_1", "no such file", true)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := input[0].OfFunctionCallOutput
	if out == nil || out.Output != "ERROR: no such file" {
		t.Fatalf("output = %+v, want the failure marked", input[0])
	}
}

// decodeResponse builds a responses.Response the way the SDK does. The union
// accessors read the raw JSON each item was decoded from, so a struct literal
// would produce empty variants.
func decodeResponse(t *testing.T, body string) responses.Response {
	t.Helper()
	var resp responses.Response
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("building test response: %v", err)
	}
	return resp
}

func TestStopReasonDerivation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want agent.StopReason
	}{
		{"end turn",
			`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}]}`,
			agent.StopReasonEndTurn},
		{"tool use",
			`{"status":"completed","output":[{"type":"function_call","call_id":"c","name":"read","arguments":"{}"}]}`,
			agent.StopReasonToolUse},
		{"refusal",
			`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"no"}]}]}`,
			agent.StopReasonRefusal},
		{"max tokens",
			`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
			agent.StopReasonMaxTokens},
		// A response cut off mid tool call must not report ToolUse: the agent
		// would execute the tool with truncated arguments.
		{"truncated tool call",
			`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","call_id":"c","name":"read","arguments":"{\"pa"}]}`,
			agent.StopReasonMaxTokens},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := stopReason(decodeResponse(t, test.body)); got != test.want {
				t.Errorf("stopReason = %q, want %q", got, test.want)
			}
		})
	}
}

func TestToBlocksRejectsUnhandledOutputItem(t *testing.T) {
	resp := decodeResponse(t, `{"output":[{"type":"web_search_call","id":"ws_1","status":"completed"}]}`)
	_, err := toBlocks(resp)
	if err == nil || !strings.Contains(err.Error(), "web_search_call") {
		t.Fatalf("err = %v, want it to name the offending item type", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — tool result becomes an error / `stopReason` returns EndTurn / no `ToolUseBlock` / tools missing from the request body.

- [ ] **Step 3: Implement**

- `params`: add `Tools: toTools(req.Tools)` (only when `len(req.Tools) > 0`):

```go
// toTools builds the params directly rather than with ToolParamOfFunction,
// which has no way to carry the description the model picks tools by.
func toTools(tools []agent.Tool) []responses.ToolUnionParam {
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		fn := responses.FunctionToolParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Strict:      openai.Bool(false),
			Parameters: map[string]any{
				"type":       "object",
				"properties": t.InputSchema.Properties,
				"required":   t.InputSchema.Required,
			},
		}
		out = append(out, responses.ToolUnionParam{OfFunction: &fn})
	}
	return out
}
```

- `toInput`: add cases inside the block loop, before `default`:

```go
case agent.ToolUseBlock:
	items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(b.Input), b.ID, b.Name))
case agent.ToolResultBlock:
	// function_call_output has no error flag; mark failures in the text so
	// the model knows the tool did not succeed.
	output := b.Content
	if b.IsError {
		output = "ERROR: " + output
	}
	items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(b.ToolUseID, output))
```

- `toBlocks`: add an output item case and a refusal part case:

```go
case responses.ResponseFunctionToolCall:
	blocks = append(blocks, agent.ToolUseBlock{ID: it.CallID, Name: it.Name, Input: json.RawMessage(it.Arguments)})
```

```go
case responses.ResponseOutputRefusal:
	// Shown as text so the transcript says why the turn ended.
	blocks = append(blocks, agent.TextBlock{Text: p.Refusal})
```

- `stopReason`: derive it, max-tokens first:

```go
func stopReason(resp responses.Response) agent.StopReason {
	// Checked before the function-call scan: a response cut off mid tool call
	// must not hand the agent truncated arguments to execute.
	if resp.Status == responses.ResponseStatusIncomplete && resp.IncompleteDetails.Reason == "max_output_tokens" {
		return agent.StopReasonMaxTokens
	}
	for _, item := range resp.Output {
		if _, ok := item.AsAny().(responses.ResponseFunctionToolCall); ok {
			return agent.StopReasonToolUse
		}
	}
	if hasRefusal(resp) {
		return agent.StopReasonRefusal
	}
	return agent.StopReasonEndTurn
}

func hasRefusal(resp responses.Response) bool {
	for _, item := range resp.Output {
		msg, ok := item.AsAny().(responses.ResponseOutputMessage)
		if !ok {
			continue
		}
		for _, part := range msg.Content {
			if _, ok := part.AsAny().(responses.ResponseOutputRefusal); ok {
				return true
			}
		}
	}
	return false
}
```

- Add the `encoding/json` import.

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "Add tool use to the OpenAI provider"
```

---

## Task 7: OpenAI reasoning in responses

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: Task 6.
- Produces: `params` requests reasoning for effort models; `Stream` emits `ThinkingDeltaEvent`; `toBlocks` emits `ThinkingBlock`.

- [ ] **Step 1: Write the failing tests**

```go
func TestStreamReasoning(t *testing.T) {
	s, url := newStub(t, sse(
		`{"type":"response.reasoning_summary_text.delta","delta":"thinking..."}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking..."}],"encrypted_content":"ENC"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`,
	))

	c := newWithOptions("key", true, agent.EffortMedium, option.WithBaseURL(url))
	req := agent.Request{Model: agent.Model{ID: "gpt-5", Thinking: agent.ThinkingEffort},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})}}

	var think string
	var resp agent.Response
	for _, e := range collect(t, c.Stream(context.Background(), req)) {
		switch e := e.(type) {
		case agent.ThinkingDeltaEvent:
			think += e.Text
		case agent.ResponseEvent:
			resp = e.Response
		}
	}
	if think != "thinking..." {
		t.Fatalf("thinking = %q", think)
	}
	tb, ok := resp.Message.Content[0].(agent.ThinkingBlock)
	if !ok || tb.Signature != "ENC" || tb.ID != "rs_1" {
		t.Fatalf("thinking block = %+v", resp.Message.Content[0])
	}

	// Reasoning must be requested with a summary and the encrypted content
	// included, or nothing comes back to render or round-trip.
	body := s.body(0)
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" || reasoning["summary"] != "auto" {
		t.Errorf("reasoning = %v, want effort medium and summary auto", body["reasoning"])
	}
	include, ok := body["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v, want [reasoning.encrypted_content]", body["include"])
	}
}

// Reasoning params are gated twice: the config's thinking switch and the
// model's own mode. Either alone must not trigger them.
func TestParamsRequestsReasoningOnlyForEffortModelsWithThinkingOn(t *testing.T) {
	req := func(mode agent.ThinkingMode) agent.Request {
		return agent.Request{Model: agent.Model{ID: "gpt-5", Thinking: mode}, MaxTokens: 10}
	}

	off := newWithOptions("key", false, agent.EffortHigh)
	if p := off.params(req(agent.ThinkingEffort), nil); p.Reasoning.Effort != "" || len(p.Include) != 0 {
		t.Errorf("thinking off: reasoning = %+v, include = %v, want neither", p.Reasoning, p.Include)
	}

	on := newWithOptions("key", true, agent.EffortHigh)
	if p := on.params(req(agent.ThinkingNone), nil); p.Reasoning.Effort != "" || len(p.Include) != 0 {
		t.Errorf("non-effort model: reasoning = %+v, include = %v, want neither", p.Reasoning, p.Include)
	}
	if p := on.params(req(agent.ThinkingEffort), nil); p.Reasoning.Effort != shared.ReasoningEffortHigh {
		t.Errorf("effort = %q, want high", p.Reasoning.Effort)
	}
}

func TestToOpenAIEffortClampsToKnownLevels(t *testing.T) {
	tests := map[agent.Effort]shared.ReasoningEffort{
		agent.EffortNone:   shared.ReasoningEffortMedium,
		agent.EffortLow:    shared.ReasoningEffortLow,
		agent.EffortMedium: shared.ReasoningEffortMedium,
		agent.EffortHigh:   shared.ReasoningEffortHigh,
		agent.EffortXHigh:  shared.ReasoningEffortHigh,
		agent.EffortMax:    shared.ReasoningEffortHigh,
	}
	for in, want := range tests {
		if got := toOpenAIEffort(in); got != want {
			t.Errorf("toOpenAIEffort(%q) = %q, want %q", in, got, want)
		}
	}
}
```

(Add the `shared` import to the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — no `ThinkingDeltaEvent`; reasoning output item is an "unsupported output item type" error; `params` sets no reasoning.

- [ ] **Step 3: Implement**

- `params`: when `c.thinking && req.Model.Thinking == agent.ThinkingEffort`, set reasoning + include before returning `p`:

```go
if c.thinking && req.Model.Thinking == agent.ThinkingEffort {
	p.Reasoning = shared.ReasoningParam{Effort: toOpenAIEffort(c.effort), Summary: shared.ReasoningSummaryAuto}
	p.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
}
```

- `Stream` switch: add

```go
case responses.ResponseReasoningSummaryTextDeltaEvent:
	if !emit(ctx, events, agent.ThinkingDeltaEvent{Text: e.Delta}) {
		return
	}
```

- `toBlocks`: add

```go
case responses.ResponseReasoningItem:
	blocks = append(blocks, agent.ThinkingBlock{Thinking: joinSummary(it.Summary), Signature: it.EncryptedContent, ID: it.ID})
```

with `joinSummary` concatenating `it.Summary[i].Text`.

- Add the effort clamp (the SDK has only low/medium/high; zero value falls to the provider default, medium):

```go
func toOpenAIEffort(e agent.Effort) shared.ReasoningEffort {
	switch e {
	case agent.EffortLow:
		return shared.ReasoningEffortLow
	case agent.EffortHigh, agent.EffortXHigh, agent.EffortMax:
		return shared.ReasoningEffortHigh
	default:
		return shared.ReasoningEffortMedium
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "Parse OpenAI reasoning into thinking blocks"
```

---

## Task 8: OpenAI reasoning round-trip

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: Task 7.
- Produces: `toInput` re-sends `ThinkingBlock` as a `reasoning` item carrying `id` + `encrypted_content`, before the function calls it produced, with a summary that marshals even when empty.

- [ ] **Step 1: Write the failing test**

```go
func TestToInputResubmitsReasoning(t *testing.T) {
	msgs := []agent.Message{
		agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}}),
		{Role: agent.RoleAssistant, Content: []agent.Block{
			agent.ThinkingBlock{Thinking: "plan", Signature: "ENC", ID: "rs_1"},
			agent.ToolUseBlock{ID: "call_1", Name: "read", Input: []byte(`{}`)},
		}},
		agent.NewUserMessage([]agent.Block{agent.NewToolResultBlock("call_1", "body", false)}),
	}
	input, err := toInput(msgs)
	if err != nil {
		t.Fatal(err)
	}
	// user, reasoning, function_call, function_call_output
	if len(input) != 4 {
		t.Fatalf("items = %d, want 4", len(input))
	}
	r := input[1].OfReasoning
	if r == nil || r.ID != "rs_1" || r.EncryptedContent.Value != "ENC" {
		t.Fatalf("item[1] not a reasoning item with encrypted content: %+v", input[1])
	}
	if input[2].OfFunctionCall == nil {
		t.Fatalf("reasoning must precede its function_call; got %+v", input[2])
	}
}

// Summary is tagged omitzero,required in the SDK: a nil slice is dropped from
// the JSON and the API rejects the resubmitted item. Assert on the marshalled
// bytes, not the struct — that is where omitzero bites.
func TestResubmittedReasoningMarshalsAnEmptySummary(t *testing.T) {
	input, err := toInput([]agent.Message{{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.ThinkingBlock{Signature: "ENC", ID: "rs_2"}, // no summary text
	}}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(input[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"summary":[]`) {
		t.Errorf("marshalled reasoning item = %s, want an explicit empty summary", body)
	}
	if !strings.Contains(string(body), `"encrypted_content":"ENC"`) {
		t.Errorf("marshalled reasoning item = %s, want the encrypted content", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — `ThinkingBlock` hits the `default` and errors, or item count wrong.

- [ ] **Step 3: Implement**

In `toInput`, add a case (block ordering already preserves "reasoning before tool call" because assistant blocks arrive in that order):

```go
case agent.ThinkingBlock:
	// Summary is a required field on a reasoning item: always non-nil, or
	// omitzero drops it and the API rejects the resubmission.
	summary := []responses.ResponseReasoningItemSummaryParam{}
	if b.Thinking != "" {
		summary = append(summary, responses.ResponseReasoningItemSummaryParam{Text: b.Thinking})
	}
	items = append(items, responses.ResponseInputItemUnionParam{OfReasoning: &responses.ResponseReasoningItemParam{
		ID:               b.ID,
		EncryptedContent: openai.String(b.Signature),
		Summary:          summary,
	}})
```

(`RedactedThinkingBlock` has no OpenAI analog; leave it in the `default` error case.)

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "Round-trip OpenAI reasoning items"
```

---

## Task 9: OpenAI Models + Resolve

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Produces: `(*Client).Models(ctx) ([]agent.Model, error)` (satisfies `agent.Provider`); `(*Client).Resolve(ctx, modelID string) (agent.Model, error)` (concrete, used by main.go).

**Curated, not raw:** `/v1/models` lists every model the account can reach — whisper, tts, dall-e, embeddings — which cannot hold a conversation and would fill the picker with entries that break the turn when selected. `Models` filters to chat families (this is what the design spec means by "curated"); `Resolve` accepts any id, so a user can still point the config at a model the table does not know.

- [ ] **Step 1: Write the failing tests**

```go
func TestResolveThinkingByModelFamily(t *testing.T) {
	c := newWithOptions("key", true, agent.EffortMedium)
	reasoning, _ := c.Resolve(context.Background(), "gpt-5")
	if reasoning.Thinking != agent.ThinkingEffort {
		t.Fatalf("gpt-5 thinking = %q, want effort", reasoning.Thinking)
	}
	plain, _ := c.Resolve(context.Background(), "gpt-4o")
	if plain.Thinking != agent.ThinkingNone {
		t.Fatalf("gpt-4o thinking = %q, want none", plain.Thinking)
	}
}

// /v1/models lists audio, image and embedding models too; the picker must
// only offer models that can hold a conversation.
func TestModelsFiltersToChatModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[
			{"id":"gpt-5","object":"model","created":1,"owned_by":"openai"},
			{"id":"whisper-1","object":"model","created":1,"owned_by":"openai"},
			{"id":"text-embedding-3-small","object":"model","created":1,"owned_by":"openai"},
			{"id":"gpt-4o","object":"model","created":1,"owned_by":"openai"}
		]}`)
	}))
	defer server.Close()

	c := newWithOptions("key", true, agent.EffortMedium, option.WithBaseURL(server.URL))
	got, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}

	want := []agent.Model{
		{ID: "gpt-5", DisplayName: "gpt-5", Thinking: agent.ThinkingEffort},
		{ID: "gpt-4o", DisplayName: "gpt-4o"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Models() = %#v, want %#v", got, want)
	}
}
```

(Add the `reflect` import to the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — no `Resolve`/`Models` method.

- [ ] **Step 3: Implement**

```go
// chatModelPrefixes are the families the picker offers. /v1/models also lists
// audio, image, embedding and moderation models, which cannot hold a
// conversation; showing them would make most of the picker unusable.
var chatModelPrefixes = []string{"gpt-5", "gpt-4.1", "gpt-4o", "o3", "o4"}

// reasoningModelPrefixes mark ids whose family reasons at an effort level.
// /v1/models returns no capabilities, so this is a static table.
var reasoningModelPrefixes = []string{"o1", "o3", "o4", "gpt-5"}

func hasAnyPrefix(id string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

func modelFor(id string) agent.Model {
	m := agent.Model{ID: id, DisplayName: id}
	if hasAnyPrefix(id, reasoningModelPrefixes) {
		m.Thinking = agent.ThinkingEffort
	}
	return m
}

// Resolve is a table lookup, not an API call: any id resolves, and one the
// table does not know simply runs without reasoning.
func (c *Client) Resolve(ctx context.Context, id string) (agent.Model, error) {
	return modelFor(id), nil
}

func (c *Client) Models(ctx context.Context) ([]agent.Model, error) {
	var models []agent.Model
	pager := c.client.Models.ListAutoPaging(ctx)
	for pager.Next() {
		if id := pager.Current().ID; hasAnyPrefix(id, chatModelPrefixes) {
			models = append(models, modelFor(id))
		}
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return models, nil
}
```

Add the `strings` import.

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "List and resolve OpenAI models"
```

---

## Task 10: Agent-loop integration test + cancellation (`internal/provider/openai`)

The unit tests prove each converter; this proves the composition. It drives a full agent turn — reasoning, a tool call, the tool result, a second inference — through the **real `Agent`** and this provider against a scripted server, then asserts on the second request's bytes: the automated stand-in for the manual E2E check. (The dependency direction is fine: the provider package may import `agent`; only the reverse is forbidden.)

**Files:**
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:** none new.

- [ ] **Step 1: Write the failing tests**

```go
func TestAgentLoopRoundTripsReasoningAndTools(t *testing.T) {
	s, url := newStub(t,
		sse(
			`{"type":"response.reasoning_summary_text.delta","delta":"planning"}`,
			`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"planning"}],"encrypted_content":"ENC"},{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"go.mod\"}","id":"fc_1"}]}}`,
		),
		sse(
			`{"type":"response.output_text.delta","delta":"done"}`,
			`{"type":"response.completed","response":{"id":"resp_2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`,
		),
	)

	c := newWithOptions("key", true, agent.EffortMedium, option.WithBaseURL(url))
	read := agent.Tool{
		Name:        "read",
		Description: "read a file",
		Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
			return "module elencode", nil
		},
	}
	a := agent.New(c, []agent.Tool{read})
	a.SetModel(agent.Model{ID: "gpt-5", Thinking: agent.ThinkingEffort})

	events := collect(t, a.Run(context.Background(), "read go.mod"))

	// The turn must complete: the last transcript change is the final answer.
	var last agent.Message
	for _, e := range events {
		if m, ok := e.(agent.MessageEvent); ok {
			last = m.Message
		}
	}
	if len(last.Content) != 1 || last.Content[0] != (agent.TextBlock{Text: "done"}) {
		t.Fatalf("last message = %#v, want the final answer", last)
	}

	// Two rounds of inference, and the second request's input must replay the
	// turn in the shape the API demands: the reasoning item (id + encrypted
	// content) before the function_call it produced, then the output.
	if s.calls != 2 {
		t.Fatalf("inference rounds = %d, want 2", s.calls)
	}
	input, ok := s.body(1)["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("second request input = %v, want 4 items", s.body(1)["input"])
	}
	types := make([]string, len(input))
	for i, item := range input {
		m := item.(map[string]any)
		tp, _ := m["type"].(string)
		if tp == "" {
			tp = "message"
		}
		types[i] = tp
	}
	want := []string{"message", "reasoning", "function_call", "function_call_output"}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("second request input = %v, want %v", types, want)
	}
	reasoning := input[1].(map[string]any)
	if reasoning["id"] != "rs_1" || reasoning["encrypted_content"] != "ENC" {
		t.Errorf("reasoning item = %v, want id and encrypted content preserved", reasoning)
	}
	output := input[3].(map[string]any)
	if output["call_id"] != "call_1" || output["output"] != "module elencode" {
		t.Errorf("function_call_output = %v, want the tool result under its call id", output)
	}
}

// The Stream goroutine must exit and close its channel when the consumer
// abandons the turn; -race plus the collect timeout make a leak loud.
func TestStreamStopsWhenContextCancelled(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"He\"}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	events := c.Stream(ctx, agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	})

	<-started
	cancel()

	// collect returning at all is the assertion: a Stream that ignored
	// cancellation would leave the channel open and time out here.
	collect(t, events)
}
```

- [ ] **Step 2: Run test to verify it fails (or passes)**

Run: `make test`
Expected: if Tasks 5–9 are correct, these may pass immediately — they are composition tests, and the red step already happened per-converter. If the round-trip test fails, the request bytes disagree with what the converters were believed to produce; fix the converter, not the test.

- [ ] **Step 3: Run `make test` once more, clean**

- [ ] **Step 4: Commit**

```bash
git add internal/provider/openai
git commit  # "Cover the OpenAI provider with an agent-loop test"
```

---

## Task 11: Wire provider selection into main (`cmd/elencode`)

**Files:**
- Modify: `cmd/elencode/main.go`
- Test: `cmd/elencode/main_test.go` (create)

**Interfaces:**
- Consumes: `openai.New/DefaultModelID/Resolve`, `anthropic.New/DefaultModelID/Resolve`, `config.Config`.
- Produces: `providerFromConfig(cfg config.Config) (prov agent.Provider, defaultModelID string, resolve func(context.Context, string) (agent.Model, error), err error)`.

- [ ] **Step 1: Write the failing test**

```go
// cmd/elencode/main_test.go
package main

import (
	"testing"

	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/provider/openai"
)

func TestProviderFromConfigSelectsOpenAI(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderOpenAI, OpenAIAPIKey: "sk", ThinkingEffort: "high"}
	prov, defModel, _, err := providerFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prov.(*openai.Client); !ok {
		t.Fatalf("provider = %T, want *openai.Client", prov)
	}
	if defModel != openai.DefaultModelID() {
		t.Fatalf("default model = %q", defModel)
	}
}

func TestProviderFromConfigSelectsAnthropic(t *testing.T) {
	cfg := config.Config{Provider: config.ProviderAnthropic, AnthropicAPIKey: "sk"}
	prov, defModel, _, err := providerFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prov.(*anthropic.Client); !ok {
		t.Fatalf("provider = %T, want *anthropic.Client", prov)
	}
	if defModel != anthropic.DefaultModelID() {
		t.Fatalf("default model = %q", defModel)
	}
}

func TestProviderFromConfigRejectsUnknown(t *testing.T) {
	if _, _, _, err := providerFromConfig(config.Config{Provider: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — no `providerFromConfig`.

- [ ] **Step 3: Implement**

Add to `main.go` and refactor `main()` to call it (replacing the hard-coded `anthropic.New`/`Resolve`/`DefaultModelID` block):

```go
func providerFromConfig(cfg config.Config) (agent.Provider, string, func(context.Context, string) (agent.Model, error), error) {
	effort := agent.Effort(cfg.ThinkingEffort)
	switch cfg.Provider {
	case config.ProviderOpenAI:
		c := openai.New(cfg.OpenAIAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
		return c, openai.DefaultModelID(), c.Resolve, nil
	case config.ProviderAnthropic, "":
		c := anthropic.New(cfg.AnthropicAPIKey.Reveal(), cfg.ThinkingEnabled, effort)
		return c, anthropic.DefaultModelID(), c.Resolve, nil
	default:
		return nil, "", nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
}
```

In `main()`: get `provider, modelID, resolve, err := providerFromConfig(cfg)`; keep `if cfg.Model != "" { modelID = cfg.Model }`; call `resolve(context.Background(), modelID)` where it currently calls `provider.Resolve`. Add the `openai` import.

- [ ] **Step 4: Run test to verify it passes + build**

Run: `make test && make build`
Expected: PASS; binary builds.

- [ ] **Step 5: Commit**

```bash
git add cmd/elencode/main.go cmd/elencode/main_test.go go.mod go.sum
git commit  # "Select provider from config in main"
```

---

## Task 12: Manual end-to-end verification

**Files:** none (verification only).

- [ ] **Step 1:** Ensure `~/.config/elencode/config.json` (or `$XDG_CONFIG_HOME`) has `{"provider":"openai","thinking_effort":"medium"}` and `OPENAI_API_KEY` is exported.
- [ ] **Step 2:** `make run`. Send a prompt that needs a tool (e.g. "read go.mod and summarize it"). Confirm: text streams, the read tool runs, a second inference uses the result, and the turn ends.
- [ ] **Step 3:** With a reasoning model (`gpt-5`) and `thinking_enabled: true`, confirm reasoning summary renders and a follow-up turn (after a tool call) succeeds — proving the reasoning item round-tripped without an API rejection.
  - **Known risk to watch for:** a 400 like *"Item 'rs_…' of type 'reasoning' was provided without its required following item"*. The converters keep the reasoning item adjacent to its `function_call` and preserve the `call_id`, but they drop the function call's own item id (`fc_…`). If the API demands it, carry that id too (a second opaque id field on `ToolUseBlock`, mirrored from `ResponseFunctionToolCall.ID`, resubmitted via `ResponseFunctionToolCallParam`) — and add the request-body assertion for it to the Task 10 integration test.
- [ ] **Step 4:** Deliberately misconfigure once (`thinking_effort: "turbo"`) and confirm startup fails with the config error rather than a mid-turn surprise. Restore.
- [ ] **Step 5:** Switch `provider` back to `anthropic`, `make run`, confirm the Anthropic path still works (regression check on Task 3), including that reasoning summaries still stream on an effort-capable Claude model (the Task 3 open question).
- [ ] **Step 6:** `make test` one final time. Note verification results; no commit needed.

---

## Self-review notes

- **Spec coverage:** provider field + key validation (Task 2, per-key env provenance), Responses API stateless asserted in request bytes (Task 5 `store:false`), `toInput` regroup incl. reasoning ordering and failed-tool marking (Tasks 6, 8), derived StopReason with max-tokens-first ordering and full branch table (Task 6), reasoning round-trip w/ id+encrypted_content+non-nil summary (Tasks 7, 8), effort enum + clamp tables both providers (Tasks 1, 3, 7), `ThinkingBlock.ID` (Task 1), curated Models/Resolve table (Task 9), Anthropic effort migration (Task 3), Anthropic streaming under SSE test (Task 4), agent-loop integration + cancellation (Task 10), main wiring (Task 11), manual E2E incl. known-risk watch items (Task 12).
- **Review findings folded in:** tool descriptions carried via a hand-built `FunctionToolParam` (Task 6); `writeConfig` reused, not redefined (Task 2); per-key env provenance replacing the single `APIKeyFromEnv` with all five reference sites listed (Task 2); stop-reason ordering fixed with a truncated-tool-call regression case (Task 6); `RoleSystem` mapped explicitly and unknown roles rejected (Task 5); `IsError` marked in `function_call_output` text (Task 6); `EffortMinimal` dropped as inexpressible in both pinned SDKs (Task 1); `thinking_effort` and provider validated at load (Task 2); request-body assertions throughout; `collect` timeout-bounded; SDK 5xx retry disabled where stubbing errors.
- **Type consistency:** `openai.New(apiKey, thinking, effort)` and `anthropic.New(apiKey, thinking, effort)` share the same 3-arg shape; `Resolve(ctx, id)` and `DefaultModelID()` concrete on both; `providerFromConfig` returns a `resolve` closure so `main` never calls a non-interface method through the interface.
- **Remaining live-API risks** (unit-untestable, parked at Task 12): the `fc_…` id pairing on reasoning resubmission, and whether Anthropic effort models still need the `Thinking` param for summaries to stream.
