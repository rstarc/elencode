package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
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

// requests reports how many requests the stub has served.
func (s *stub) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
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
// A bare range would hang the whole test binary on a misframed stub or a leaked
// goroutine instead of failing this one test.
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

	// The model picks tools by description, so sending it is not optional.
	// ToolParamOfFunction drops it, which is why toTools builds the param
	// struct directly.
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

func TestStreamReasoning(t *testing.T) {
	s, url := newStub(t, sse(
		`{"type":"response.reasoning_summary_text.delta","delta":"thinking..."}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking..."}],"encrypted_content":"ENC"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`,
	))

	c := newWithOptions("key", true, agent.EffortMedium, option.WithBaseURL(url))
	req := agent.Request{
		Model:    agent.Model{ID: "gpt-5", Thinking: agent.ThinkingEffort},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}

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
		// The SDK has no level above high, so the two Anthropic-only levels
		// clamp rather than letting the API reject the request.
		agent.EffortXHigh: shared.ReasoningEffortHigh,
		agent.EffortMax:   shared.ReasoningEffortHigh,
	}
	for in, want := range tests {
		if got := toOpenAIEffort(in); got != want {
			t.Errorf("toOpenAIEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

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

func TestResolveThinkingByModelFamily(t *testing.T) {
	c := newWithOptions("key", true, agent.EffortMedium)

	reasoning, err := c.Resolve(context.Background(), "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	if reasoning.Thinking != agent.ThinkingEffort {
		t.Fatalf("gpt-5 thinking = %q, want effort", reasoning.Thinking)
	}

	plain, err := c.Resolve(context.Background(), "gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Thinking != agent.ThinkingNone {
		t.Fatalf("gpt-4o thinking = %q, want none", plain.Thinking)
	}
}

// Resolve is a table lookup, not an API call: an id the table does not know
// still resolves, so the config can name a model this build has not heard of.
func TestResolveAcceptsAnUnknownModel(t *testing.T) {
	c := newWithOptions("key", true, agent.EffortMedium)

	got, err := c.Resolve(context.Background(), "gpt-9-turbo")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "gpt-9-turbo" {
		t.Fatalf("model = %+v, want the id passed through", got)
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

// TestAgentLoopRoundTripsReasoningAndTools proves the composition the unit
// tests cannot: a full turn — reasoning, a tool call, the tool result, a second
// inference — through the real Agent, asserting on the bytes of the second
// request. The automated stand-in for the manual end-to-end check.
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
	if s.requests() != 2 {
		t.Fatalf("inference rounds = %d, want 2", s.requests())
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

// A turn the API gives up on must fail loudly. Reporting it as an empty
// end_turn response would make the reply vanish with no explanation, and the
// agent loop would treat the turn as finished.
func TestStreamSurfacesAFailedResponse(t *testing.T) {
	_, url := newStub(t, sse(
		`{"type":"response.output_text.delta","delta":"partial"}`,
		`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"upstream exploded"},"output":[]}}`,
	))

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(url))
	events := collect(t, c.Stream(context.Background(), agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	last, ok := events[len(events)-1].(agent.ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
	if !strings.Contains(last.Err.Error(), "upstream exploded") {
		t.Errorf("err = %v, want it to carry the API's explanation", last.Err)
	}
}

// A stream that stops before any terminal event is a failure too: without one
// there is no output to report, and claiming end_turn would silently drop the
// turn.
func TestStreamSurfacesATruncatedStream(t *testing.T) {
	_, url := newStub(t, sse(`{"type":"response.output_text.delta","delta":"partial"}`))

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(url))
	events := collect(t, c.Stream(context.Background(), agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	if _, ok := events[len(events)-1].(agent.ErrorEvent); !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
}

// Hitting the token limit is a legitimate ending, not a failure: the API ends
// the stream with response.incomplete rather than response.completed, and the
// partial output still has to reach the transcript.
func TestStreamReportsIncompleteAsMaxTokens(t *testing.T) {
	_, url := newStub(t, sse(
		`{"type":"response.output_text.delta","delta":"cut off"}`,
		`{"type":"response.incomplete","response":{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"cut off"}]}]}}`,
	))

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(url))
	events := collect(t, c.Stream(context.Background(), agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	last, ok := events[len(events)-1].(agent.ResponseEvent)
	if !ok {
		t.Fatalf("last event = %#v, want a ResponseEvent", events[len(events)-1])
	}
	if last.Response.StopReason != agent.StopReasonMaxTokens {
		t.Errorf("stop = %q, want max_tokens", last.Response.StopReason)
	}
	if len(last.Response.Message.Content) != 1 || last.Response.Message.Content[0] != (agent.TextBlock{Text: "cut off"}) {
		t.Errorf("content = %#v, want the partial output kept", last.Response.Message.Content)
	}
}

// A refusal carries the reason the turn ended, so it has to reach the
// transcript as text rather than being counted and discarded.
func TestToBlocksKeepsRefusalText(t *testing.T) {
	resp := decodeResponse(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that"}]}]}`)

	blocks, err := toBlocks(resp)
	if err != nil {
		t.Fatal(err)
	}

	want := agent.TextBlock{Text: "I cannot help with that"}
	if len(blocks) != 1 || blocks[0] != want {
		t.Fatalf("blocks = %#v, want [%#v]", blocks, want)
	}
}

func TestToBlocksRejectsUnhandledContentPart(t *testing.T) {
	resp := decodeResponse(t, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_audio","transcript":"hi"}]}]}`)

	_, err := toBlocks(resp)

	if err == nil || !strings.Contains(err.Error(), "output_audio") {
		t.Fatalf("err = %v, want it to name the offending content part", err)
	}
}

// A block the API cannot take must be rejected rather than dropped: sending a
// turn with part of its history missing is worse than failing the turn.
func TestToInputRejectsUnsupportedBlock(t *testing.T) {
	_, err := toInput([]agent.Message{{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.RedactedThinkingBlock{Data: "opaque"},
	}}})

	if err == nil || !strings.Contains(err.Error(), "RedactedThinkingBlock") {
		t.Fatalf("err = %v, want it to name the offending block type", err)
	}
}

// Stream must turn a conversion failure into an ErrorEvent. The converters are
// tested directly, but nothing otherwise proves Stream surfaces their errors
// rather than hanging or reporting an empty turn.
func TestStreamSurfacesAConversionError(t *testing.T) {
	s, url := newStub(t, sse(
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
	))

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(url))
	events := collect(t, c.Stream(context.Background(), agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Messages: []agent.Message{{Role: "wizard", Content: []agent.Block{agent.TextBlock{Text: "x"}}}},
	}))

	if len(events) != 1 {
		t.Fatalf("events = %#v, want just the ErrorEvent", events)
	}
	if _, ok := events[0].(agent.ErrorEvent); !ok {
		t.Fatalf("event = %#v, want an ErrorEvent", events[0])
	}
	// The request must not have been sent at all: the conversion runs first.
	if s.requests() != 0 {
		t.Errorf("requests = %d, want the turn abandoned before calling the API", s.requests())
	}
}

// An output item we cannot convert must fail the turn, not be silently
// dropped from the assembled response.
func TestStreamSurfacesAnUnsupportedOutputItem(t *testing.T) {
	_, url := newStub(t, sse(
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"web_search_call","id":"ws_1","status":"completed"}]}}`,
	))

	c := newWithOptions("key", false, agent.EffortMedium, option.WithBaseURL(url))
	events := collect(t, c.Stream(context.Background(), agent.Request{
		Model:    agent.Model{ID: "gpt-5"},
		Messages: []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})},
	}))

	last, ok := events[len(events)-1].(agent.ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
	if !strings.Contains(last.Err.Error(), "web_search_call") {
		t.Errorf("err = %v, want it to name the offending item", last.Err)
	}
}
