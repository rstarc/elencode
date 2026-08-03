package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rstarc/elencode/internal/agent"
)

// These conversions used to log.Panicf on anything they did not recognise,
// which killed the program from inside the provider's goroutine. Any input the
// API can produce but we do not handle yet must come back as an error the TUI
// can render instead.

// decodeMessage builds an sdk.Message the way the SDK itself does. The union
// accessors read the raw JSON each block was decoded from, so a struct literal
// would produce empty variants.
func decodeMessage(t *testing.T, body string) *sdk.Message {
	t.Helper()

	msg := &sdk.Message{}
	if err := json.Unmarshal([]byte(body), msg); err != nil {
		t.Fatalf("building test message: %v", err)
	}
	return msg
}

func TestToBlocksRejectsUnhandledVariant(t *testing.T) {
	// A real block type the API can return today and toBlocks does not convert
	msg := decodeMessage(t, `{"content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}]}`)

	_, err := toBlocks(msg)

	if err == nil {
		t.Fatal("err = nil, want an error for an unhandled block variant")
	}
	if !strings.Contains(err.Error(), "server_tool_use") {
		t.Errorf("err = %q, want it to name the offending block type", err)
	}
}

func TestToBlocksConvertsKnownVariants(t *testing.T) {
	msg := decodeMessage(t, `{"content":[
		{"type":"text","text":"hi"},
		{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a.txt"}}
	]}`)

	blocks, err := toBlocks(msg)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v, want 2", blocks)
	}
	if got, want := blocks[0], (agent.TextBlock{Text: "hi"}); got != want {
		t.Errorf("blocks[0] = %#v, want %#v", got, want)
	}
	toolUse, ok := blocks[1].(agent.ToolUseBlock)
	if !ok {
		t.Fatalf("blocks[1] = %#v, want an agent.ToolUseBlock", blocks[1])
	}
	if toolUse.ID != "toolu_1" || toolUse.Name != "read" {
		t.Errorf("tool use = %#v, want ID toolu_1 and name read", toolUse)
	}
}

func TestToStopReasonRejectsUnknown(t *testing.T) {
	_, err := toStopReason("nonsense")

	if err == nil {
		t.Fatal("err = nil, want an error for an unknown stop reason")
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("err = %q, want it to name the offending stop reason", err)
	}
}

func TestToStopReasonConvertsKnown(t *testing.T) {
	got, err := toStopReason(sdk.StopReasonToolUse)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != agent.StopReasonToolUse {
		t.Errorf("stop reason = %q, want %q", got, agent.StopReasonToolUse)
	}
}

func TestToSdkRoleRejectsUnknown(t *testing.T) {
	_, err := toSdkRole("wizard")

	if err == nil {
		t.Fatal("err = nil, want an error for an unknown role")
	}
	// The old code interpolated the empty result rather than the input, so
	// assert the message actually names what was passed in.
	if !strings.Contains(err.Error(), "wizard") {
		t.Errorf("err = %q, want it to name the offending role", err)
	}
}

func TestToMessagesConvertsEveryBlockKind(t *testing.T) {
	msgs := []agent.Message{
		agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}}),
		{Role: agent.RoleAssistant, Content: []agent.Block{
			agent.ToolUseBlock{ID: "toolu_1", Name: "read", Input: []byte(`{}`)},
		}},
		agent.NewUserMessage([]agent.Block{agent.NewToolResultBlock("toolu_1", "out", false)}),
	}

	got, err := toMessages(msgs)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != len(msgs) {
		t.Errorf("converted %d messages, want %d", len(got), len(msgs))
	}
}

func TestDefaultModelIDIsAvailableToStartup(t *testing.T) {
	if got := DefaultModelID(); got != "claude-haiku-4-5" {
		t.Errorf("default model = %q, want claude-haiku-4-5", got)
	}
}

// TestModelsListsWhatTheAPIReturns runs against a stub of the models endpoint,
// so it covers the request path and the conversion without a network call.
func TestModelsListsWhatTheAPIReturns(t *testing.T) {
	const body = `{"data":[
		{"type":"model","id":"claude-opus-4-5","display_name":"Claude Opus 4.5","created_at":"2025-11-01T00:00:00Z"},
		{"type":"model","id":"claude-haiku-4-5","display_name":"Claude Haiku 4.5","created_at":"2025-10-01T00:00:00Z"}
	],"has_more":false}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/models") {
			t.Errorf("requested %q, want the models endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	got, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}

	want := []agent.Model{
		{ID: "claude-opus-4-5", DisplayName: "Claude Opus 4.5"},
		{ID: "claude-haiku-4-5", DisplayName: "Claude Haiku 4.5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Models() = %v, want %v", got, want)
	}
}

func TestToBlocksConvertsThinking(t *testing.T) {
	msg := decodeMessage(t, `{"content":[{"type":"thinking","thinking":"let me check the file","signature":"sig-abc"}]}`)

	blocks, err := toBlocks(msg)
	if err != nil {
		t.Fatalf("toBlocks: %v", err)
	}

	want := agent.ThinkingBlock{Thinking: "let me check the file", Signature: "sig-abc"}
	if len(blocks) != 1 || blocks[0] != want {
		t.Errorf("blocks = %#v, want %#v", blocks, want)
	}
}

// TestToMessagesSendsThinkingBack matters for the next turn: reasoning left in
// the context window has to go back with its signature, or the API rejects the
// request rather than ignoring the block.
func TestToMessagesSendsThinkingBack(t *testing.T) {
	msgs := []agent.Message{{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.ThinkingBlock{Thinking: "let me check the file", Signature: "sig-abc"},
	}}}

	got, err := toMessages(msgs)
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}

	if len(got) != 1 || len(got[0].Content) != 1 {
		t.Fatalf("converted to %#v, want one block", got)
	}
	thinking := got[0].Content[0].OfThinking
	if thinking == nil {
		t.Fatalf("block = %#v, want a thinking block", got[0].Content[0])
	}
	if thinking.Thinking != "let me check the file" || thinking.Signature != "sig-abc" {
		t.Errorf("thinking block = %#v, want the text and signature carried through", thinking)
	}
}

func TestToBlocksConvertsRedactedThinking(t *testing.T) {
	msg := decodeMessage(t, `{"content":[{"type":"redacted_thinking","data":"encrypted-payload"}]}`)

	blocks, err := toBlocks(msg)
	if err != nil {
		t.Fatalf("toBlocks: %v", err)
	}

	want := agent.RedactedThinkingBlock{Data: "encrypted-payload"}
	if len(blocks) != 1 || blocks[0] != want {
		t.Errorf("blocks = %#v, want %#v", blocks, want)
	}
}

func TestToMessagesSendsRedactedThinkingBack(t *testing.T) {
	msgs := []agent.Message{{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.RedactedThinkingBlock{Data: "encrypted-payload"},
	}}}

	got, err := toMessages(msgs)
	if err != nil {
		t.Fatalf("toMessages: %v", err)
	}

	redacted := got[0].Content[0].OfRedactedThinking
	if redacted == nil {
		t.Fatalf("block = %#v, want a redacted thinking block", got[0].Content[0])
	}
	if redacted.Data != "encrypted-payload" {
		t.Errorf("data = %q, want it carried through unaltered", redacted.Data)
	}
}

// accumulate replays a streamed response the way Stream does, so a test can
// assert on what the SDK assembles rather than on a hand-built Message. The
// union accessors read each block's raw JSON, which Accumulate only rewrites
// when the block stops, so a test that skips those events proves nothing.
func accumulate(t *testing.T, events ...string) *sdk.Message {
	t.Helper()

	msg := &sdk.Message{}
	for _, body := range events {
		event := sdk.MessageStreamEventUnion{}
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			t.Fatalf("building stream event %s: %v", body, err)
		}
		if err := msg.Accumulate(event); err != nil {
			t.Fatalf("accumulating %s: %v", body, err)
		}
	}
	return msg
}

// TestToBlocksKeepsStreamedThinking covers thinking arriving the way it really
// does — one delta at a time — rather than whole in a single event.
func TestToBlocksKeepsStreamedThinking(t *testing.T) {
	msg := accumulate(t,
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"check the file."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"It is in internal/agent."}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`,
		`{"type":"message_stop"}`,
	)

	blocks, err := toBlocks(msg)
	if err != nil {
		t.Fatalf("toBlocks: %v", err)
	}

	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v, want the thinking and the answer", blocks)
	}
	want := agent.ThinkingBlock{Thinking: "Let me check the file.", Signature: "sig-abc"}
	if blocks[0] != want {
		t.Errorf("blocks[0] = %#v, want %#v", blocks[0], want)
	}
	if got, want := blocks[1], (agent.TextBlock{Text: "It is in internal/agent."}); got != want {
		t.Errorf("blocks[1] = %#v, want %#v", got, want)
	}
}

func TestDeltaEventCarriesEachStreamedKind(t *testing.T) {
	tests := []struct {
		name string
		body string
		want agent.Event
	}{
		{"text", `{"type":"text_delta","text":"hello"}`, agent.TextDeltaEvent{Text: "hello"}},
		{"thinking", `{"type":"thinking_delta","thinking":"hmm"}`, agent.ThinkingDeltaEvent{Text: "hmm"}},
		// Nothing to paint live: the signature is not shown, and a tool's input
		// is only worth rendering once it parses.
		{"signature", `{"type":"signature_delta","signature":"sig"}`, nil},
		{"tool input", `{"type":"input_json_delta","partial_json":"{\"a\":"}`, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delta := sdk.ContentBlockDeltaEvent{}
			if err := json.Unmarshal([]byte(`{"type":"content_block_delta","index":0,"delta":`+test.body+`}`), &delta); err != nil {
				t.Fatalf("building delta: %v", err)
			}

			got, ok := deltaEvent(delta)

			if test.want == nil {
				if ok {
					t.Errorf("delta produced %#v, want nothing to paint", got)
				}
				return
			}
			if !ok {
				t.Fatalf("delta produced nothing, want %#v", test.want)
			}
			if got != test.want {
				t.Errorf("delta produced %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestModelsReportWhatThinkingTheyAccept(t *testing.T) {
	const body = `{"data":[
		{"type":"model","id":"new","display_name":"New","created_at":"2026-01-01T00:00:00Z",
		 "capabilities":{"thinking":{"supported":true,"types":{"adaptive":{"supported":true},"enabled":{"supported":false}}}}},
		{"type":"model","id":"old","display_name":"Old","created_at":"2025-01-01T00:00:00Z",
		 "capabilities":{"thinking":{"supported":true,"types":{"adaptive":{"supported":false},"enabled":{"supported":true}}}}},
		{"type":"model","id":"none","display_name":"None","created_at":"2024-01-01T00:00:00Z",
		 "capabilities":{"thinking":{"supported":false,"types":{"adaptive":{"supported":false},"enabled":{"supported":false}}}}}
	],"has_more":false}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	got, err := newWithOptions("key", true, agent.EffortMedium, option.WithBaseURL(server.URL)).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}

	want := []agent.Model{
		{ID: "new", DisplayName: "New", Thinking: agent.ThinkingAdaptive},
		{ID: "old", DisplayName: "Old", Thinking: agent.ThinkingBudgeted},
		{ID: "none", DisplayName: "None", Thinking: agent.ThinkingNone},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Models() = %#v\nwant %#v", got, want)
	}
}

// TestThinkingMatchesWhatTheModelAccepts is the point of carrying the mode
// around: the API rejects the wrong kind rather than ignoring it.
func TestThinkingMatchesWhatTheModelAccepts(t *testing.T) {
	tests := []struct {
		name     string
		mode     agent.ThinkingMode
		adaptive bool
		budgeted bool
	}{
		{"adaptive", agent.ThinkingAdaptive, true, false},
		{"budgeted", agent.ThinkingBudgeted, false, true},
		{"none", agent.ThinkingNone, false, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := newWithOptions("key", true, agent.EffortMedium)

			params := c.messageParams(agent.Request{Model: agent.Model{ID: "m", Thinking: test.mode}, MaxTokens: 8092}, nil)

			if got := params.Thinking.OfAdaptive != nil; got != test.adaptive {
				t.Errorf("adaptive thinking requested = %v, want %v", got, test.adaptive)
			}
			if got := params.Thinking.OfEnabled != nil; got != test.budgeted {
				t.Errorf("budgeted thinking requested = %v, want %v", got, test.budgeted)
			}
		})
	}
}

func TestMessageParamsUsesTheRequestModel(t *testing.T) {
	c := newWithOptions("key", false, agent.EffortMedium)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "model-from-request"}, MaxTokens: 8092}, nil)

	if got := string(params.Model); got != "model-from-request" {
		t.Errorf("request model = %q, want model-from-request", got)
	}
}

// TestAdaptiveThinkingAsksForTheSummary covers the reason reasoning is rendered
// at all: the API returns thinking blocks with empty text unless the summary is
// asked for, which would put a heading over nothing on screen.
func TestAdaptiveThinkingAsksForTheSummary(t *testing.T) {
	c := newWithOptions("key", true, agent.EffortMedium)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "m", Thinking: agent.ThinkingAdaptive}, MaxTokens: 8092}, nil)

	if got := params.Thinking.OfAdaptive.Display; got != sdk.ThinkingConfigAdaptiveDisplaySummarized {
		t.Errorf("display = %q, want %q", got, sdk.ThinkingConfigAdaptiveDisplaySummarized)
	}
}

func TestThinkingIsNotRequestedWhenDisabled(t *testing.T) {
	c := newWithOptions("key", false, agent.EffortMedium)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "m", Thinking: agent.ThinkingAdaptive}, MaxTokens: 8092}, nil)

	if params.Thinking.OfAdaptive != nil || params.Thinking.OfEnabled != nil {
		t.Errorf("thinking = %#v, want it left out of the request", params.Thinking)
	}
}

// TestBudgetLeavesRoomForAnAnswer guards the older kind of thinking: the API
// rejects a budget that does not leave the request room to answer.
func TestBudgetLeavesRoomForAnAnswer(t *testing.T) {
	c := newWithOptions("key", true, agent.EffortMedium)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "m", Thinking: agent.ThinkingBudgeted}, MaxTokens: 8092}, nil)

	if budget := params.Thinking.OfEnabled.BudgetTokens; budget < 1024 || budget >= params.MaxTokens {
		t.Errorf("budget = %d, want between 1024 and the %d token limit", budget, params.MaxTokens)
	}
}

// TestToModelPrefersEffortCapability: a model that reasons at an effort level
// takes that over the adaptive or budgeted kinds, so the caller's configured
// level is the one that reaches the API.
func TestToModelPrefersEffortCapability(t *testing.T) {
	info := sdk.ModelInfo{ID: "claude-x", DisplayName: "Claude X"}
	info.Capabilities.Effort.Supported = true
	info.Capabilities.Thinking.Types.Adaptive.Supported = true

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

// TestEffortModelsStillAskForThinking: OutputConfig.Effort says how hard to
// reason, not whether the reasoning comes back. Without the thinking param an
// effort model returns nothing to render, so both go in the request.
func TestEffortModelsStillAskForThinking(t *testing.T) {
	c := newWithOptions("key", true, agent.EffortHigh)
	m := agent.Model{ID: "claude-x", Thinking: agent.ThinkingEffort}

	params := c.messageParams(agent.Request{Model: m, MaxTokens: 10}, nil)

	if params.Thinking.OfAdaptive == nil {
		t.Fatalf("thinking = %+v, want the adaptive summarized param alongside effort", params.Thinking)
	}
}

// collectEvents drains a Stream until it closes, failing if it hangs. A bare
// range over the channel would hang the whole test binary on a misframed stub
// or a leaked goroutine instead of failing this one test.
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

// streamAgainst runs one request against handler and drains the events.
func streamAgainst(t *testing.T, handler http.HandlerFunc) []agent.Event {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	return collectEvents(t, c.Stream(context.Background(), agent.Request{
		Model:     agent.Model{ID: "claude-x"},
		MaxTokens: 100,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))
}

// errorStatus answers with an API error envelope, which is what the SDK reads
// the error type out of.
func errorStatus(status int, errType string, hdr map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"boom"}}`, errType)
	}
}

// retryableError returns the RetryableError the stream ended with, failing if
// the last event was not one.
func retryableError(t *testing.T, events []agent.Event) *agent.RetryableError {
	t.Helper()

	if len(events) == 0 {
		t.Fatal("no events, want a terminal ErrorEvent")
	}
	last, ok := events[len(events)-1].(agent.ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
	var retryable *agent.RetryableError
	if !errors.As(last.Err, &retryable) {
		t.Fatalf("err = %v (%T), want it marked retryable", last.Err, last.Err)
	}
	return retryable
}

func TestStreamMarksTransientFailuresRetryable(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		errType string
	}{
		{name: "rate limit", status: http.StatusTooManyRequests, errType: "rate_limit_error"},
		// 529: the API's own "busy, come back" signal, and the one most worth
		// riding out rather than reporting.
		{name: "overloaded", status: 529, errType: "overloaded_error"},
		{name: "api error", status: http.StatusInternalServerError, errType: "api_error"},
		// A 5xx with no type the SDK recognises still has its status to go on
		{name: "untyped 5xx", status: http.StatusBadGateway, errType: "something_new"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			retryableError(t, streamAgainst(t, errorStatus(test.status, test.errType, nil)))
		})
	}
}

// The set matches what the SDK retries when left to itself. Diverging would
// mean this build gives up on failures the vendor considers transient.
func TestStreamMarksEveryStatusTheSDKWouldRetry(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			// A type the table does not know, so only the status can mark it
			retryableError(t, streamAgainst(t, errorStatus(status, "something_new", nil)))
		})
	}
}

// A request that never reached the API is retried too, matching the SDK's own
// treatment of a connection error. Cancellation is the exception: the user
// asked for the turn to stop, so there is nothing to recover.
func TestStreamMarksAConnectionFailureRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverURL := server.URL
	server.Close() // nothing is listening on that port now

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(serverURL))
	retryableError(t, collectEvents(t, c.Stream(context.Background(), agent.Request{
		Model:     agent.Model{ID: "claude-x"},
		MaxTokens: 100,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	})))
}

func TestClassifyLeavesCancellationAlone(t *testing.T) {
	// Shaped like what the SDK returns: the transport error wraps ctx's own.
	err := &url.Error{Op: "Post", URL: "https://api.anthropic.com", Err: context.Canceled}

	var retryable *agent.RetryableError
	if errors.As(classify(err), &retryable) {
		t.Errorf("classify(%v) marked it retryable, want a cancelled turn left alone", err)
	}
}

// x-should-retry is the API telling us directly, and it wins over anything the
// status or the error type implies — in both directions.
func TestStreamObeysTheShouldRetryHeader(t *testing.T) {
	t.Run("false on a rate limit", func(t *testing.T) {
		events := streamAgainst(t, errorStatus(
			http.StatusTooManyRequests, "rate_limit_error", map[string]string{"x-should-retry": "false"},
		))

		last := events[len(events)-1].(agent.ErrorEvent)
		var retryable *agent.RetryableError
		if errors.As(last.Err, &retryable) {
			t.Errorf("err = %v, want the header to veto the retry", last.Err)
		}
	})

	t.Run("true on a permanent status", func(t *testing.T) {
		retryableError(t, streamAgainst(t, errorStatus(
			http.StatusBadRequest, "invalid_request_error", map[string]string{"x-should-retry": "true"},
		)))
	})
}

func TestStreamReadsRetryAfter(t *testing.T) {
	retryable := retryableError(t, streamAgainst(t, errorStatus(
		http.StatusTooManyRequests, "rate_limit_error", map[string]string{"Retry-After": "9"},
	)))

	if retryable.After != 9*time.Second {
		t.Errorf("After = %s, want 9s from the Retry-After header", retryable.After)
	}
}

func TestStreamPrefersRetryAfterMs(t *testing.T) {
	retryable := retryableError(t, streamAgainst(t, errorStatus(
		http.StatusTooManyRequests, "rate_limit_error",
		map[string]string{"Retry-After-Ms": "2500", "Retry-After": "9"},
	)))

	if retryable.After != 2500*time.Millisecond {
		t.Errorf("After = %s, want the finer-grained header to win", retryable.After)
	}
}

// A rejected request fails the same way however often it is sent, so retrying
// only delays telling the user what is wrong.
func TestStreamLeavesPermanentFailuresUnmarked(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		errType string
	}{
		{name: "invalid request", status: http.StatusBadRequest, errType: "invalid_request_error"},
		{name: "authentication", status: http.StatusUnauthorized, errType: "authentication_error"},
		{name: "not found", status: http.StatusNotFound, errType: "not_found_error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := streamAgainst(t, errorStatus(test.status, test.errType, nil))

			last, ok := events[len(events)-1].(agent.ErrorEvent)
			if !ok {
				t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
			}
			var retryable *agent.RetryableError
			if errors.As(last.Err, &retryable) {
				t.Errorf("err = %v, want it left unmarked", last.Err)
			}
		})
	}
}

// An overload can also arrive mid-stream, after the request was accepted. The
// SDK turns that SSE frame into a real API error, but one whose StatusCode is
// the 200 the stream opened with — so only the error type identifies it, and
// judging by status alone would miss every one of these.
func TestStreamMarksAMidStreamOverloadRetryable(t *testing.T) {
	events := streamAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-x\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n")
	})

	if retryable := retryableError(t, events); !strings.Contains(retryable.Error(), "overloaded") {
		t.Errorf("err = %v, want it to carry the API's explanation", retryable)
	}
}

// The SDK retries itself, with an uninterruptible sleep and no way to tell the
// UI. Leaving it on would multiply the agent's own attempts and make ctrl+c do
// nothing for seconds at a time.
func TestClientLeavesRetryingToTheAgent(t *testing.T) {
	var calls int
	streamAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		errorStatus(http.StatusTooManyRequests, "rate_limit_error", nil)(w, r)
	})

	if calls != 1 {
		t.Errorf("requests = %d, want 1: the SDK must not retry behind the agent", calls)
	}
}

// The model picks tools by description, so sending it is not optional. Nothing
// otherwise exercises the tool params at all.
func TestToolParamsCarryNameDescriptionAndSchema(t *testing.T) {
	tools := toolParams([]agent.Tool{{
		Name:        "read",
		Description: "read a file",
		InputSchema: agent.InputSchema{
			Type:       "object",
			Properties: map[string]agent.Property{"path": {Type: "string", Description: "the path"}},
			Required:   []string{"path"},
		},
	}})

	if len(tools) != 1 || tools[0].OfTool == nil {
		t.Fatalf("tools = %#v, want one function tool", tools)
	}
	got := tools[0].OfTool
	if got.Name != "read" {
		t.Errorf("name = %q, want read", got.Name)
	}
	if got.Description.Value != "read a file" {
		t.Errorf("description = %q, want it carried through", got.Description.Value)
	}
	if _, ok := got.InputSchema.Properties.(map[string]agent.Property); !ok {
		t.Errorf("properties = %#v, want the tool's own schema", got.InputSchema.Properties)
	}
	if len(got.InputSchema.Required) != 1 || got.InputSchema.Required[0] != "path" {
		t.Errorf("required = %v, want [path]", got.InputSchema.Required)
	}
}

// Tools reach the API, asserted on the request body rather than the params
// struct: the SDK decides the wire shape, and that is what the model reads.
func TestStreamSendsToolsWithDescriptions(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body did not decode: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"c\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	collectEvents(t, c.Stream(context.Background(), agent.Request{
		Model:     agent.Model{ID: "claude-x"},
		MaxTokens: 10,
		Tools:     []agent.Tool{{Name: "read", Description: "read a file"}},
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one", body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "read" || tool["description"] != "read a file" {
		t.Errorf("tool = %v, want name and description carried through", tool)
	}
}

func TestToStopReasonMapsEveryKnownReason(t *testing.T) {
	tests := map[sdk.StopReason]agent.StopReason{
		sdk.StopReasonEndTurn:      agent.StopReasonEndTurn,
		sdk.StopReasonMaxTokens:    agent.StopReasonMaxTokens,
		sdk.StopReasonStopSequence: agent.StopReasonStopSequence,
		sdk.StopReasonToolUse:      agent.StopReasonToolUse,
		sdk.StopReasonPauseTurn:    agent.StopReasonPauseTurn,
		sdk.StopReasonRefusal:      agent.StopReasonRefusal,
	}
	for in, want := range tests {
		got, err := toStopReason(in)
		if err != nil {
			t.Errorf("toStopReason(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("toStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToSdkRoleMapsSystem(t *testing.T) {
	got, err := toSdkRole(agent.RoleSystem)
	if err != nil {
		t.Fatal(err)
	}
	if got != sdk.MessageParamRoleSystem {
		t.Fatalf("role = %q, want system", got)
	}
}

// toMessages has a default branch for an unhandled block, but no test can
// reach it: agent.Block's marker method is unexported, so only the agent
// package can add a variant and toMessages handles every one that exists. The
// branch stays as a guard for whenever a new block type is added.

// Stream must turn a conversion failure into an ErrorEvent rather than hanging
// or reporting a turn that never happened.
func TestStreamSurfacesAConversionError(t *testing.T) {
	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL("http://127.0.0.1:0"))

	events := collectEvents(t, c.Stream(context.Background(), agent.Request{
		Model:     agent.Model{ID: "claude-x"},
		MaxTokens: 10,
		Messages:  []agent.Message{{Role: "wizard", Content: []agent.Block{agent.TextBlock{Text: "x"}}}},
	}))

	if len(events) != 1 {
		t.Fatalf("events = %#v, want just the ErrorEvent", events)
	}
	if _, ok := events[0].(agent.ErrorEvent); !ok {
		t.Fatalf("event = %#v, want an ErrorEvent", events[0])
	}
}

// A stream that ends before the message is complete must fail the turn: the
// stop reason is still unset, and reporting it as a finished turn would drop
// whatever the model was in the middle of saying.
func TestStreamSurfacesATruncatedStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"c\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	defer server.Close()

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	events := collectEvents(t, c.Stream(context.Background(), agent.Request{
		Model:     agent.Model{ID: "claude-x"},
		MaxTokens: 10,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	if _, ok := events[len(events)-1].(agent.ErrorEvent); !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
}

// The Stream goroutine must exit and close its channel when the consumer
// abandons the turn; -race plus the collect timeout make a leak loud.
func TestStreamStopsWhenContextCancelled(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"c\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(server.URL))
	events := c.Stream(ctx, agent.Request{
		Model:     agent.Model{ID: "claude-x"},
		MaxTokens: 10,
		Messages:  []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	})

	<-started
	cancel()

	// collectEvents returning at all is the assertion: a Stream that ignored
	// cancellation would leave the channel open and time out here.
	collectEvents(t, events)
}
