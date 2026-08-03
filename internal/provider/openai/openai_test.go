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
