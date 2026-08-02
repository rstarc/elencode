# OpenAI Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add OpenAI (Responses API) as a second `agent.Provider`, selectable by config, at parity with the Anthropic path including reasoning that round-trips.

**Architecture:** A new `internal/provider/openai` package implements the two-method `agent.Provider` interface, mirroring `internal/provider/anthropic` structurally (a `newWithOptions` test seam, a ctx-cancellable `Stream` goroutine, pure `to*` converters). The provider stays stateless (`store:false`, full input replay every turn); the `Agent` keeps owning the context window. "Effort" becomes a first-class provider-neutral concept in `internal/agent`, and both providers honor it. `cmd/elencode/main.go` gains a `switch cfg.Provider` factory.

**Tech Stack:** Go 1.26, `github.com/openai/openai-go v1.12.0` (already fetched), `github.com/anthropics/anthropic-sdk-go v1.57.0`, Bubble Tea TUI, standard-library tests + `httptest` SSE stubs.

## Global Constraints

- Build/test only via the Makefile: `make build`, `make test` (`go vet ./...` + `go test -race ./...`). `make test` must pass before any task is considered done.
- `internal/agent` must not import a provider SDK. Vendor-specific translation lives inside `internal/provider/...`.
- Tests use the standard library only (plus `teatest` for the TUI). Hand-written fakes in the test file that needs them; no mocking library. Stub the SDK by pointing it at an `httptest` server via `option.WithBaseURL`.
- No new third-party dependencies beyond `openai-go` (already approved and fetched).
- Return errors instead of panicking, including in conversion code.
- Sum types are an interface with an unexported marker method (see `agent.Event`, `agent.Block`).
- Commit style: imperative, capitalized subject, no `type:` prefix (e.g. "Add OpenAI provider skeleton"). End every commit message with the trailer:
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`
- Do not `git push`. Commit locally on branch `openai-provider`.

## Verified SDK facts (use these exact names)

**OpenAI Responses (`github.com/openai/openai-go`, pkg `responses`, plus `shared`, top-level `openai` for `openai.String/Int/Bool`):**
- Call: `client.Responses.NewStreaming(ctx, responses.ResponseNewParams{...})` → `*ssestream.Stream[responses.ResponseStreamEventUnion]`.
- Request fields: `Model shared.ResponsesModel` (`= string`), `MaxOutputTokens param.Opt[int64]` (`openai.Int(n)`), `Store param.Opt[bool]` (`openai.Bool(false)`), `Input responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam{...}}`, `Tools []responses.ToolUnionParam`, `Reasoning shared.ReasoningParam`, `Include []responses.ResponseIncludable`.
- Input item constructors (all return `responses.ResponseInputItemUnionParam`): `ResponseInputItemParamOfFunctionCall(arguments, callID, name string)`, `ResponseInputItemParamOfFunctionCallOutput(callID, output string)`. For plain messages use `responses.EasyInputMessageParam{Role: responses.EasyInputMessageRoleUser|Assistant, Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(text)}}` wrapped as `responses.ResponseInputItemUnionParam{OfMessage: &easyMsg}`. For reasoning round-trip build the union directly: `responses.ResponseInputItemUnionParam{OfReasoning: &responses.ResponseReasoningItemParam{ID: id, EncryptedContent: openai.String(sig), Summary: summary}}` where `Summary []responses.ResponseReasoningItemSummaryParam{{Text: t}}` (may be empty slice).
- Reasoning request: `shared.ReasoningParam{Effort: shared.ReasoningEffortLow|Medium|High, Summary: shared.ReasoningSummaryAuto}`. **`ReasoningEffort` has only low/medium/high in v1.12.0 (no `minimal`).**
- Encrypted round-trip: add `Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}`.
- Tools: `responses.ToolParamOfFunction(name string, parameters map[string]any, strict bool)` → `responses.ToolUnionParam`.
- Stream events (`stream.Current().AsAny().(type)`): `responses.ResponseTextDeltaEvent{Delta string}`, `responses.ResponseReasoningSummaryTextDeltaEvent{Delta string}`, `responses.ResponseCompletedEvent{Response responses.Response}`. Check `stream.Err()` after the loop.
- Final `responses.Response`: `.Output []responses.ResponseOutputItemUnion`, `.Status responses.ResponseStatus` (`ResponseStatusCompleted`/`ResponseStatusIncomplete`), `.IncompleteDetails` (has a `.Reason` string; compare to `"max_output_tokens"` — confirm during Task 5).
- Output item variants (`item.AsAny().(type)`): `responses.ResponseOutputMessage` (`.Content []` of `ResponseOutputText{Text}` / `ResponseOutputRefusal`), `responses.ResponseFunctionToolCall{CallID, Name, Arguments string, ID string}`, `responses.ResponseReasoningItem{ID string, Summary []ResponseReasoningItemSummary, EncryptedContent string}`.
- Models: `client.Models.List(ctx)` (pager of `openai.Model{ID string}`).

**Anthropic (`anthropic-sdk-go`, alias `sdk`):**
- `sdk.MessageNewParams.OutputConfig sdk.OutputConfigParam` (`.Effort sdk.OutputConfigEffort`, values `Low/Medium/High/Xhigh/Max`).
- Model capability: `info.Capabilities.Effort.Supported bool` and per-level `Low/Medium/High/Xhigh/Max` (each a `CapabilitySupport`).

---

## File structure

- `internal/agent/blocks.go` — modify: add `ID` to `ThinkingBlock`.
- `internal/agent/provider.go` — modify: add `Effort` type + values, add `ThinkingEffort` to `ThinkingMode`.
- `internal/config/config.go` — modify: `Provider`, `OpenAIAPIKey`, `OPENAI_API_KEY` override, `thinking_effort`, provider-aware `Load`/`Save`.
- `internal/provider/openai/openai.go` — create: the provider.
- `internal/provider/openai/openai_test.go` — create: SSE-stub tests.
- `internal/provider/anthropic/anthropic.go` — modify: effort (`New` arg, `toModel`, `messageParams`).
- `internal/provider/anthropic/anthropic_test.go` — modify: updated `newWithOptions` calls + effort assertions.
- `cmd/elencode/main.go` — modify: `providerFromConfig` factory + effort wiring.

---

## Task 1: Shared effort + reasoning-id types (`internal/agent`)

**Files:**
- Modify: `internal/agent/provider.go`
- Modify: `internal/agent/blocks.go`
- Test: `internal/agent/effort_test.go` (create)

**Interfaces:**
- Produces: `agent.Effort` (string enum: `EffortNone, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax`); `agent.ThinkingEffort ThinkingMode`; `agent.ThinkingBlock.ID string`.

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/effort_test.go
package agent

import "testing"

func TestEffortWireValues(t *testing.T) {
	cases := map[Effort]string{
		EffortNone: "", EffortMinimal: "minimal", EffortLow: "low",
		EffortMedium: "medium", EffortHigh: "high", EffortXHigh: "xhigh", EffortMax: "max",
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

// Effort is how hard an effort-based model is asked to reason. The zero value
// asks for none. Providers clamp to the levels their own model accepts.
type Effort string

const (
	EffortNone    Effort = ""
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"
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
- Test: `internal/config/config_test.go` (create if absent; otherwise extend)

**Interfaces:**
- Produces: `config.Config.Provider string`, `config.Config.OpenAIAPIKey Secret`, `config.Config.ThinkingEffort string`; constants `config.ProviderAnthropic = "anthropic"`, `config.ProviderOpenAI = "openai"`, `config.OPENAI_API_KEY_ENV_VAR_NAME`. `Load` validates the selected provider's key.

- [ ] **Step 1: Write the failing test**

```go
// internal/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFromValidatesSelectedProviderKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	// openai selected, only openai key present -> ok, no anthropic error
	p := writeConfig(t, `{"provider":"openai","openai_api_key":"sk-oai","thinking_effort":"high"}`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Provider != "openai" || cfg.OpenAIAPIKey.Reveal() != "sk-oai" || cfg.ThinkingEffort != "high" {
		t.Fatalf("bad cfg: %+v", cfg)
	}

	// openai selected but no openai key -> error mentions openai
	p2 := writeConfig(t, `{"provider":"openai"}`)
	if _, err := loadFrom(p2); err == nil {
		t.Fatal("expected error for missing openai key")
	}
}

func TestLoadDefaultsProviderToAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	p := writeConfig(t, `{"anthropic_api_key":"sk-ant"}`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Provider != ProviderAnthropic {
		t.Fatalf("provider = %q, want anthropic", cfg.Provider)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — `loadFrom`, `ProviderAnthropic`, `OpenAIAPIKey`, `ThinkingEffort` undefined.

- [ ] **Step 3: Implement**

In `internal/config/config.go`:
- Add fields to `Config` (all key/effort fields `omitempty` to preserve merge-save semantics; `Provider` too):

```go
Provider        string `json:"provider,omitempty"`
OpenAIAPIKey    Secret `json:"openai_api_key,omitempty"`
ThinkingEffort  string `json:"thinking_effort,omitempty"`
```

- Add constants:

```go
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)
const OPENAI_API_KEY_ENV_VAR_NAME = "OPENAI_API_KEY"
```

- Extract the file logic from `Load` into `loadFrom(path string) (Config, error)` and have `Load` compute the path then call it. Defaults: `Config{ThinkingEnabled: true, ThinkingEffort: "medium", Provider: ProviderAnthropic}`. After unmarshal, if `cfg.Provider == ""` set `ProviderAnthropic`. Apply both env overrides to their own fields (`ANTHROPIC_API_KEY`→`AnthropicAPIKey`, `OPENAI_API_KEY`→`OpenAIAPIKey`, each setting `APIKeyFromEnv` only when it is the selected provider's key). Then validate:

```go
switch cfg.Provider {
case ProviderOpenAI:
	if cfg.OpenAIAPIKey == "" {
		return cfg, fmt.Errorf("API key not set: provide %q in the environment or %q in %q", OPENAI_API_KEY_ENV_VAR_NAME, "openai_api_key", cfg.Path)
	}
case ProviderAnthropic:
	if cfg.AnthropicAPIKey == "" {
		return cfg, fmt.Errorf("API key not set: provide %q in the environment or %q in %q", ANTHROPIC_API_KEY_ENV_VAR_NAME, "anthropic_api_key", cfg.Path)
	}
default:
	return cfg, fmt.Errorf("unknown provider %q", cfg.Provider)
}
```

- In `Save`, also blank `OpenAIAPIKey` when it was env-supplied (extend the existing `APIKeyFromEnv` guard to whichever key came from the environment).

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
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
```

Update the signature of **every** existing `newWithOptions("key", <bool>...)` and `New(...)` call in the test file to pass an effort argument (use `agent.EffortMedium` where effort is irrelevant to the assertion).

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — `newWithOptions`/`New` arity, `OutputConfig.Effort` not set.

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

- Add the clamp (Anthropic has no `minimal`; map it to low):

```go
func toAnthropicEffort(e agent.Effort) sdk.OutputConfigEffort {
	switch e {
	case agent.EffortLow, agent.EffortMinimal:
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

## Task 4: OpenAI provider — text-only turn (`internal/provider/openai`)

**Files:**
- Create: `internal/provider/openai/openai.go`
- Create: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: `agent.Request/Event/Message/Block`, `agent.Effort` (Task 1).
- Produces: `openai.New(apiKey string, thinking bool, effort agent.Effort) *Client`; `newWithOptions(apiKey string, thinking bool, effort agent.Effort, opts ...option.RequestOption) *Client`; `(*Client).Stream(ctx, agent.Request) <-chan agent.Event`; `DefaultModelID() string`.

- [ ] **Step 1: Write the failing test**

```go
// internal/provider/openai/openai_test.go
package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/option"
	"github.com/rstarc/elencode/internal/agent"
)

// sse writes Responses API stream events as text/event-stream.
func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		fmt.Fprintf(w, "%s\n\n", e)
	}
}

func collect(ch <-chan agent.Event) []agent.Event {
	var out []agent.Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestStreamTextOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`event: response.output_text.delta`+"\n"+`data: {"type":"response.output_text.delta","delta":"Hello"}`,
			`event: response.completed`+"\n"+`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}]}}`,
		)
	}))
	defer server.Close()

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	req := agent.Request{
		Model:     agent.Model{ID: "gpt-5"},
		MaxTokens: 100,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}
	events := collect(c.Stream(context.Background(), req))

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
}
```

> Confirm the exact SSE framing the SDK's `ssestream` decoder expects while making this pass (it reads `data:` lines; `event:` lines are optional). Adjust the `sse` helper if the stream doesn't decode.

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — package `openai` has no `newWithOptions`/`Stream`.

- [ ] **Step 3: Implement the minimal package**

```go
// internal/provider/openai/openai.go
package openai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/rstarc/elencode/internal/agent"
)

const eventBuffer = 64
const defaultModel = "gpt-5"

func DefaultModelID() string { return defaultModel }

type Client struct {
	client   openai.Client
	thinking bool
	effort   agent.Effort
}

func New(apiKey string, thinking bool, effort agent.Effort) *Client {
	return newWithOptions(apiKey, thinking, effort)
}

func newWithOptions(apiKey string, thinking bool, effort agent.Effort, opts ...option.RequestOption) *Client {
	opts = append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	return &Client{client: openai.NewClient(opts...), thinking: thinking, effort: effort}
}

func (c *Client) params(req agent.Request, input responses.ResponseInputParam) responses.ResponseNewParams {
	return responses.ResponseNewParams{
		Model:           shared.ResponsesModel(req.Model.ID),
		MaxOutputTokens: openai.Int(req.MaxTokens),
		Store:           openai.Bool(false),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: input},
	}
}

func (c *Client) Stream(ctx context.Context, req agent.Request) <-chan agent.Event {
	events := make(chan agent.Event, eventBuffer)
	go func() {
		defer close(events)

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

func emit(ctx context.Context, events chan<- agent.Event, ev agent.Event) bool {
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// toInput reshapes agent messages into Responses input items. Text-only for now.
func toInput(msgs []agent.Message) (responses.ResponseInputParam, error) {
	var items responses.ResponseInputParam
	for _, msg := range msgs {
		role := responses.EasyInputMessageRoleUser
		if msg.Role == agent.RoleAssistant {
			role = responses.EasyInputMessageRoleAssistant
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

func toBlocks(resp responses.Response) ([]agent.Block, error) {
	var blocks []agent.Block
	for _, item := range resp.Output {
		switch it := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			for _, part := range it.Content {
				if txt := part.AsAny(); true {
					if t, ok := txt.(responses.ResponseOutputText); ok {
						blocks = append(blocks, agent.TextBlock{Text: t.Text})
					}
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

var _ = json.RawMessage(nil) // used by later tasks
```

> Confirm the `part.AsAny()` accessor for `ResponseOutputMessage.Content` while making the test pass; the union accessor name may differ. Remove the `json` placeholder line once Task 5 imports it for real.

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "Add OpenAI provider with text-only streaming"
```

---

## Task 5: OpenAI tool use + StopReason derivation

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: Task 4 (`toInput`, `toBlocks`, `stopReason`, `params`).
- Produces: `toInput` handles `ToolUseBlock`/`ToolResultBlock`; `toBlocks` emits `ToolUseBlock`; `params` sets `Tools`; `stopReason` returns `ToolUse`/`MaxTokens`/`Refusal`/`EndTurn`.

- [ ] **Step 1: Write the failing tests**

```go
func TestStreamToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w, `event: response.completed`+"\n"+`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"x\"}","id":"fc_1"}]}}`)
	}))
	defer server.Close()

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	req := agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Tools:    []agent.Tool{{Name: "read", Description: "read a file", InputSchema: agent.InputSchema{Type: "object"}}},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "read x"}})},
	}
	var resp agent.Response
	for e := range c.Stream(context.Background(), req) {
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — tool result becomes an error / `stopReason` returns EndTurn / no `ToolUseBlock`.

- [ ] **Step 3: Implement**

- `params`: add tools — `Tools: toTools(req.Tools)`:

```go
func toTools(tools []agent.Tool) []responses.ToolUnionParam {
	out := make([]responses.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		params := map[string]any{
			"type":       "object",
			"properties": t.InputSchema.Properties,
			"required":   t.InputSchema.Required,
		}
		out = append(out, responses.ToolParamOfFunction(t.Name, params, false))
	}
	return out
}
```

- `toInput`: add cases inside the block loop, before `default`:

```go
case agent.ToolUseBlock:
	items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(b.Input), b.ID, b.Name))
case agent.ToolResultBlock:
	items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(b.ToolUseID, b.Content))
```

- `toBlocks`: add a case:

```go
case responses.ResponseFunctionToolCall:
	blocks = append(blocks, agent.ToolUseBlock{ID: it.CallID, Name: it.Name, Input: json.RawMessage(it.Arguments)})
```

- `stopReason`: derive it:

```go
func stopReason(resp responses.Response) agent.StopReason {
	for _, item := range resp.Output {
		if _, ok := item.AsAny().(responses.ResponseFunctionToolCall); ok {
			return agent.StopReasonToolUse
		}
	}
	if resp.Status == responses.ResponseStatusIncomplete && resp.IncompleteDetails.Reason == "max_output_tokens" {
		return agent.StopReasonMaxTokens
	}
	if hasRefusal(resp) {
		return agent.StopReasonRefusal
	}
	return agent.StopReasonEndTurn
}
```

- Add `hasRefusal` scanning message content for `responses.ResponseOutputRefusal`, and in `toBlocks` treat a refusal part as text (or skip). Remove the `json` placeholder line.

> Confirm `resp.IncompleteDetails.Reason`'s type/value while making the MaxTokens branch compile (it may be a typed string constant rather than a raw string).

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "Add tool use to the OpenAI provider"
```

---

## Task 6: OpenAI reasoning in responses

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: Task 5.
- Produces: `params` requests reasoning for effort models; `Stream` emits `ThinkingDeltaEvent`; `toBlocks` emits `ThinkingBlock`.

- [ ] **Step 1: Write the failing test**

```go
func TestStreamReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`event: response.reasoning_summary_text.delta`+"\n"+`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking..."}`,
			`event: response.completed`+"\n"+`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking..."}],"encrypted_content":"ENC"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`,
		)
	}))
	defer server.Close()

	c := newWithOptions("key", true, agent.EffortMedium, option.WithBaseURL(server.URL))
	req := agent.Request{Model: agent.Model{ID: "gpt-5", Thinking: agent.ThinkingEffort},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})}}

	var think string
	var resp agent.Response
	for e := range c.Stream(context.Background(), req) {
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
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — no `ThinkingDeltaEvent`; reasoning output item is an "unsupported output item type" error.

- [ ] **Step 3: Implement**

- `params`: when `c.thinking && req.Model.Thinking == agent.ThinkingEffort`, set reasoning + include:

```go
if c.thinking && req.Model.Thinking == agent.ThinkingEffort {
	p.Reasoning = shared.ReasoningParam{Effort: toOpenAIEffort(c.effort), Summary: shared.ReasoningSummaryAuto}
	p.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
}
```

(Refactor `params` to build a local `p` then return it so the fields can be set conditionally.)

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

- Add the effort clamp (no `minimal` in the SDK):

```go
func toOpenAIEffort(e agent.Effort) shared.ReasoningEffort {
	switch e {
	case agent.EffortMinimal, agent.EffortLow:
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

## Task 7: OpenAI reasoning round-trip

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: Task 6.
- Produces: `toInput` re-sends `ThinkingBlock` as a `reasoning` item carrying `id` + `encrypted_content`, before the function calls it produced.

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
```

> `EncryptedContent.Value` assumes `param.Opt[string]`; confirm the accessor (`.Value` / `.Or("")`) while making it pass.

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: FAIL — `ThinkingBlock` hits the `default` and errors, or item count wrong.

- [ ] **Step 3: Implement**

In `toInput`, add a case (block ordering already preserves "reasoning before tool call" because assistant blocks arrive in that order):

```go
case agent.ThinkingBlock:
	var summary []responses.ResponseReasoningItemSummaryParam
	if b.Thinking != "" {
		summary = []responses.ResponseReasoningItemSummaryParam{{Text: b.Thinking}}
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

## Task 8: OpenAI Models + Resolve

**Files:**
- Modify: `internal/provider/openai/openai.go`
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Produces: `(*Client).Models(ctx) ([]agent.Model, error)` (satisfies `agent.Provider`); `(*Client).Resolve(ctx, modelID string) (agent.Model, error)` (concrete, used by main.go).

- [ ] **Step 1: Write the failing test**

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make test`
Expected: compile failure — no `Resolve` method.

- [ ] **Step 3: Implement**

```go
// reasoningModel reports whether a model id is a reasoning model. OpenAI's
// /v1/models returns no capabilities, so this is a static prefix table.
func reasoningModel(id string) bool {
	for _, p := range []string{"o1", "o3", "o4", "gpt-5"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

func modelFor(id string) agent.Model {
	m := agent.Model{ID: id, DisplayName: id}
	if reasoningModel(id) {
		m.Thinking = agent.ThinkingEffort
	}
	return m
}

func (c *Client) Resolve(ctx context.Context, id string) (agent.Model, error) {
	return modelFor(id), nil
}

func (c *Client) Models(ctx context.Context) ([]agent.Model, error) {
	var models []agent.Model
	pager := c.client.Models.ListAutoPaging(ctx)
	for pager.Next() {
		models = append(models, modelFor(pager.Current().ID))
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return models, nil
}
```

> Confirm `client.Models.ListAutoPaging(ctx)` exists (else use `List` + manual paging). Add the `strings` import.

- [ ] **Step 4: Run test to verify it passes**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/openai
git commit  # "List and resolve OpenAI models"
```

---

## Task 9: Wire provider selection into main (`cmd/elencode`)

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

## Task 10: Manual end-to-end verification

**Files:** none (verification only).

- [ ] **Step 1:** Ensure `~/.config/elencode/config.json` (or `$XDG_CONFIG_HOME`) has `{"provider":"openai","thinking_effort":"medium"}` and `OPENAI_API_KEY` is exported.
- [ ] **Step 2:** `make run`. Send a prompt that needs a tool (e.g. "read go.mod and summarize it"). Confirm: text streams, the read tool runs, a second inference uses the result, and the turn ends.
- [ ] **Step 3:** With a reasoning model (`gpt-5`) and `thinking_enabled: true`, confirm reasoning summary renders and a follow-up turn (after a tool call) succeeds — proving the reasoning item round-tripped without an API rejection.
- [ ] **Step 4:** Switch `provider` back to `anthropic`, `make run`, confirm the Anthropic path still works (regression check on Task 3).
- [ ] **Step 5:** `make test` one final time. Note verification results; no commit needed.

---

## Self-review notes

- **Spec coverage:** provider field (Task 2), OpenAI key + env (Task 2), Responses API stateless (Task 4 `Store:false`), `toInput` regroup incl. reasoning ordering (Tasks 5, 7), derived StopReason (Task 5), reasoning round-trip w/ id+encrypted_content (Tasks 6, 7), effort enum + clamp both providers (Tasks 1, 3, 6), `ThinkingBlock.ID` (Task 1), Models/Resolve table (Task 8), Anthropic effort migration (Task 3), main wiring (Task 9), TDD + httptest seam (all), dependency (already fetched; committed in Task 9). Manual E2E (Task 10).
- **Type consistency:** `openai.New(apiKey, thinking, effort)` and `anthropic.New(apiKey, thinking, effort)` share the same 3-arg shape; `Resolve(ctx, id)` and `DefaultModelID()` concrete on both; `providerFromConfig` returns a `resolve` closure so `main` never calls a non-interface method through the interface.
- **Open leaf-name confirmations** are flagged inline as `>` notes at the exact step where they matter (SSE framing, `ResponseOutputMessage.Content` accessor, `IncompleteDetails.Reason` type, `param.Opt` accessor, `ListAutoPaging`), each resolvable by the failing test.
