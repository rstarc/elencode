package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	c := newWithOptions("key", false, option.WithBaseURL(server.URL))
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
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	got, err := newWithOptions("key", true, option.WithBaseURL(server.URL)).Models(context.Background())
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
			c := newWithOptions("key", true)

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
	c := newWithOptions("key", false)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "model-from-request"}, MaxTokens: 8092}, nil)

	if got := string(params.Model); got != "model-from-request" {
		t.Errorf("request model = %q, want model-from-request", got)
	}
}

// TestAdaptiveThinkingAsksForTheSummary covers the reason reasoning is rendered
// at all: the API returns thinking blocks with empty text unless the summary is
// asked for, which would put a heading over nothing on screen.
func TestAdaptiveThinkingAsksForTheSummary(t *testing.T) {
	c := newWithOptions("key", true)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "m", Thinking: agent.ThinkingAdaptive}, MaxTokens: 8092}, nil)

	if got := params.Thinking.OfAdaptive.Display; got != sdk.ThinkingConfigAdaptiveDisplaySummarized {
		t.Errorf("display = %q, want %q", got, sdk.ThinkingConfigAdaptiveDisplaySummarized)
	}
}

func TestThinkingIsNotRequestedWhenDisabled(t *testing.T) {
	c := newWithOptions("key", false)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "m", Thinking: agent.ThinkingAdaptive}, MaxTokens: 8092}, nil)

	if params.Thinking.OfAdaptive != nil || params.Thinking.OfEnabled != nil {
		t.Errorf("thinking = %#v, want it left out of the request", params.Thinking)
	}
}

// TestBudgetLeavesRoomForAnAnswer guards the older kind of thinking: the API
// rejects a budget that does not leave the request room to answer.
func TestBudgetLeavesRoomForAnAnswer(t *testing.T) {
	c := newWithOptions("key", true)

	params := c.messageParams(agent.Request{Model: agent.Model{ID: "m", Thinking: agent.ThinkingBudgeted}, MaxTokens: 8092}, nil)

	if budget := params.Thinking.OfEnabled.BudgetTokens; budget < 1024 || budget >= params.MaxTokens {
		t.Errorf("budget = %d, want between 1024 and the %d token limit", budget, params.MaxTokens)
	}
}
