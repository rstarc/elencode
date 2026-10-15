package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rstarc/elencode/internal/agent"
)

var argon = agent.Model{Provider: agent.ProviderGemini, ID: "gemini-4-argon", Thinking: agent.ThinkingEffort}

// received is what the stub server was sent.
type received struct {
	path string
	key  string
	body map[string]any
}

// stubServer answers every request with status and body, and hands over each
// request it was sent.
func stubServer(t *testing.T, status int, body string) (*httptest.Server, <-chan received) {
	t.Helper()
	requests := make(chan received, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		requests <- received{path: r.URL.Path, key: r.Header.Get("x-goog-api-key"), body: decoded}

		if status == http.StatusOK {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

// sse is chunks as the event stream streamGenerateContent answers with.
func sse(chunks ...string) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString("data: " + chunk + "\n\n")
	}
	return b.String()
}

func userSays(text string) agent.Message {
	return agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: text}})
}

// streamTurn runs one turn of req through c and returns every event it produced.
func streamTurn(t *testing.T, c *Client, req agent.Request) []agent.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var events []agent.Event
	for event := range c.Stream(ctx, req) {
		events = append(events, event)
	}
	if ctx.Err() != nil {
		t.Fatal("the turn did not finish")
	}
	return events
}

// turnRequest streams one turn of req from a server that answers with chunks,
// and returns what the server was sent.
func turnRequest(t *testing.T, thinking bool, req agent.Request) received {
	t.Helper()
	server, requests := stubServer(t, http.StatusOK, sse(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	streamTurn(t, newWithBaseURL("configured-key", thinking, server.URL), req)
	return <-requests
}

// finalResponse is the turn's response, failing the test if it ended any other way.
func finalResponse(t *testing.T, events []agent.Event) agent.Response {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last, ok := events[len(events)-1].(agent.ResponseEvent)
	if !ok {
		t.Fatalf("last event = %#v, want a ResponseEvent", events[len(events)-1])
	}
	return last.Response
}

// finalError is the turn's error, failing the test if it ended any other way.
func finalError(t *testing.T, events []agent.Event) error {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last, ok := events[len(events)-1].(agent.ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", events[len(events)-1])
	}
	return last.Err
}

// asJSON decodes s the way a request body is decoded, for comparing with one.
func asJSON(t *testing.T, s string) any {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(s), &decoded); err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return decoded
}

func TestNewTalksToTheGeminiAPI(t *testing.T) {
	c := New("configured-key", false)

	if got := c.config.HTTPOptions.BaseURL; got != "https://generativelanguage.googleapis.com/" {
		t.Errorf("base URL = %q, want the Gemini API's", got)
	}
}

// The SDK reads these itself. Each would change where the request goes or
// which key it carries, which is elencode's to say.
func TestTheEnvironmentDoesNotRedirectARequest(t *testing.T) {
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")
	t.Setenv("GOOGLE_GENAI_USE_ENTERPRISE", "true")
	t.Setenv("GOOGLE_API_KEY", "environment-key")
	t.Setenv("GEMINI_API_KEY", "environment-key")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "some-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "http://127.0.0.1:1/")

	got := turnRequest(t, false, agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

	if got.path != "/v1beta/models/gemini-4-argon:streamGenerateContent" {
		t.Errorf("request went to %s, want the Gemini API's streamGenerateContent", got.path)
	}
	if got.key != "configured-key" {
		t.Errorf("x-goog-api-key = %q, want the configured key", got.key)
	}
}

func TestStreamPaintsThoughtsAndTextAsTheyArrive(t *testing.T) {
	server, _ := stubServer(t, http.StatusOK, sse(
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Pondering","thought":true}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}]}`,
	))

	events := streamTurn(t, newWithBaseURL("key", true, server.URL), agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

	want := []agent.Event{
		agent.ThinkingDeltaEvent{Text: "Pondering"},
		agent.TextDeltaEvent{Text: "Hel"},
		agent.TextDeltaEvent{Text: "lo"},
		agent.ResponseEvent{Response: agent.Response{
			Message: agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
				agent.ThinkingBlock{Thinking: "Pondering"},
				agent.TextBlock{Text: "Hello"},
			}},
			StopReason: agent.StopReasonEndTurn,
		}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("events = %#v\nwant %#v", events, want)
	}
}

// The signature has to go back with the call it came with: Gemini rejects a
// history whose function calls have lost theirs.
func TestStreamKeepsAFunctionCallsSignature(t *testing.T) {
	server, _ := stubServer(t, http.StatusOK, sse(
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"read","args":{"path":"a.go"}},"thoughtSignature":"c2lnbmF0dXJl"}]},"finishReason":"STOP"}]}`,
	))

	events := streamTurn(t, newWithBaseURL("key", false, server.URL), agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

	got := finalResponse(t, events)
	want := agent.Response{
		Message: agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
			agent.ToolUseBlock{ID: "call-1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`), Signature: "c2lnbmF0dXJl"},
		}},
		StopReason: agent.StopReasonToolUse,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("response = %#v\nwant %#v", got, want)
	}
}

// A tool result finds its call by id, so every call needs one, and a
// different one, even when Gemini sends none.
func TestStreamGivesEveryFunctionCallAnID(t *testing.T) {
	server, _ := stubServer(t, http.StatusOK, sse(
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","args":{"path":"a.go"}}},{"functionCall":{"name":"read","args":{"path":"b.go"}}}]},"finishReason":"STOP"}]}`,
	))

	events := streamTurn(t, newWithBaseURL("key", false, server.URL), agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

	blocks := finalResponse(t, events).Message.Content
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v, want two tool uses", blocks)
	}
	first, second := blocks[0].(agent.ToolUseBlock), blocks[1].(agent.ToolUseBlock)
	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Errorf("ids = %q and %q, want two different ones", first.ID, second.ID)
	}
}

func TestReplaySendsTheSignatureAndTheFunctionsName(t *testing.T) {
	got := turnRequest(t, false, agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{
		userSays("read them"),
		{Role: agent.RoleAssistant, Content: []agent.Block{
			agent.ThinkingBlock{Thinking: "Two files."},
			agent.ToolUseBlock{ID: "call-1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`), Signature: "c2lnbmF0dXJl"},
			agent.ToolUseBlock{ID: "call-2", Name: "bash", Input: json.RawMessage(`{"command":"cat b.go"}`)},
		}},
		agent.NewUserMessage([]agent.Block{
			agent.NewToolResultBlock("call-1", "package a", false),
			agent.NewToolResultBlock("call-2", "no such file", true),
		}),
	}})

	want := asJSON(t, `[
		{"role": "user", "parts": [{"text": "read them"}]},
		{"role": "model", "parts": [
			{"functionCall": {"id": "call-1", "name": "read", "args": {"path": "a.go"}}, "thoughtSignature": "c2lnbmF0dXJl"},
			{"functionCall": {"id": "call-2", "name": "bash", "args": {"command": "cat b.go"}}}
		]},
		{"role": "user", "parts": [
			{"functionResponse": {"id": "call-1", "name": "read", "response": {"output": "package a"}}},
			{"functionResponse": {"id": "call-2", "name": "bash", "response": {"error": "no such file"}}}
		]}
	]`)
	if !reflect.DeepEqual(got.body["contents"], want) {
		t.Errorf("contents = %v\nwant %v", got.body["contents"], want)
	}
}

func TestRequestCarriesTheToolsAndTheTokenLimit(t *testing.T) {
	got := turnRequest(t, false, agent.Request{
		Model:     argon,
		MaxTokens: 100,
		Messages:  []agent.Message{userSays("hi")},
		Tools: []agent.Tool{{
			Name:        "read",
			Description: "Reads a file",
			InputSchema: agent.InputSchema{
				Type:       "object",
				Properties: map[string]agent.Property{"path": {Type: "string", Description: "The file"}},
				Required:   []string{"path"},
			},
		}},
	})

	wantTools := asJSON(t, `[{"functionDeclarations": [{
		"name": "read",
		"description": "Reads a file",
		"parametersJsonSchema": {
			"type": "object",
			"properties": {"path": {"type": "string", "description": "The file"}},
			"required": ["path"]
		}
	}]}]`)
	if !reflect.DeepEqual(got.body["tools"], wantTools) {
		t.Errorf("tools = %v\nwant %v", got.body["tools"], wantTools)
	}
	config, _ := got.body["generationConfig"].(map[string]any)
	if config["maxOutputTokens"] != 100.0 {
		t.Errorf("maxOutputTokens = %v, want 100", config["maxOutputTokens"])
	}
}

func TestThinkingConfig(t *testing.T) {
	uncatalogued := agent.Model{Provider: agent.ProviderGemini, ID: "gemini-9"}
	tests := []struct {
		name     string
		thinking bool
		model    agent.Model
		effort   agent.Effort
		want     any // nil: no thinkingConfig at all
	}{
		{"low", true, argon, agent.EffortLow, map[string]any{"includeThoughts": true, "thinkingLevel": "LOW"}},
		{"medium", true, argon, agent.EffortMedium, map[string]any{"includeThoughts": true, "thinkingLevel": "MEDIUM"}},
		{"high", true, argon, agent.EffortHigh, map[string]any{"includeThoughts": true, "thinkingLevel": "HIGH"}},
		{"xhigh is high", true, argon, agent.EffortXHigh, map[string]any{"includeThoughts": true, "thinkingLevel": "HIGH"}},
		{"max is high", true, argon, agent.EffortMax, map[string]any{"includeThoughts": true, "thinkingLevel": "HIGH"}},
		{"no effort leaves the level to the API", true, argon, agent.EffortNone, map[string]any{"includeThoughts": true}},
		{"thinking off", false, argon, agent.EffortHigh, nil},
		{"a model not known to think", true, uncatalogued, agent.EffortHigh, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := turnRequest(t, test.thinking, agent.Request{Model: test.model, Effort: test.effort, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

			config, _ := got.body["generationConfig"].(map[string]any)
			thinkingConfig, sent := config["thinkingConfig"]
			if test.want == nil {
				if sent {
					t.Errorf("thinkingConfig = %v, want none", thinkingConfig)
				}
				return
			}
			if !reflect.DeepEqual(thinkingConfig, test.want) {
				t.Errorf("thinkingConfig = %v, want %v", thinkingConfig, test.want)
			}
		})
	}
}

func TestStopReasons(t *testing.T) {
	tests := []struct {
		finishReason string
		want         agent.StopReason
	}{
		{"STOP", agent.StopReasonEndTurn},
		{"MAX_TOKENS", agent.StopReasonMaxTokens},
		{"SAFETY", agent.StopReasonRefusal},
		{"PROHIBITED_CONTENT", agent.StopReasonRefusal},
	}
	for _, test := range tests {
		server, _ := stubServer(t, http.StatusOK, sse(
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"`+test.finishReason+`"}]}`,
		))

		events := streamTurn(t, newWithBaseURL("key", false, server.URL), agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

		if got := finalResponse(t, events).StopReason; got != test.want {
			t.Errorf("%s: stop reason = %q, want %q", test.finishReason, got, test.want)
		}
	}
}

// A blocked prompt gets no candidate at all, only the reason.
func TestABlockedPromptEndsTheTurnWithTheReason(t *testing.T) {
	server, _ := stubServer(t, http.StatusOK, sse(`{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`))

	events := streamTurn(t, newWithBaseURL("key", false, server.URL), agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

	if err := finalError(t, events); !strings.Contains(err.Error(), "PROHIBITED_CONTENT") {
		t.Errorf("err = %v, want it to name the reason", err)
	}
}

func TestTransientFailuresAreRetryable(t *testing.T) {
	tests := []struct {
		status    int
		body      string
		retryable bool
	}{
		{429, `{"error":{"code":429,"message":"slow down","status":"RESOURCE_EXHAUSTED"}}`, true},
		{503, `{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`, true},
		{400, `{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`, false},
	}
	for _, test := range tests {
		server, _ := stubServer(t, test.status, test.body)

		events := streamTurn(t, newWithBaseURL("key", false, server.URL), agent.Request{Model: argon, MaxTokens: 100, Messages: []agent.Message{userSays("hi")}})

		var retryable *agent.RetryableError
		if got := errors.As(finalError(t, events), &retryable); got != test.retryable {
			t.Errorf("%d: retryable = %v, want %v", test.status, got, test.retryable)
		}
	}
}

func TestCheckKey(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		rejected bool
		fails    bool
	}{
		{"accepted", 200, `{"models":[]}`, false, false},
		// Gemini says a key is bad with a 400 rather than a 401
		{"invalid key", 400, `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`, true, true},
		{"forbidden", 403, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, true, true},
		{"another bad request", 400, `{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`, false, true},
		{"server error", 500, `{"error":{"code":500,"message":"oops","status":"INTERNAL"}}`, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			err := newWithBaseURL("key", false, server.URL).CheckKey(context.Background())

			if got := errors.Is(err, agent.ErrKeyRejected); got != test.rejected {
				t.Errorf("err = %v, rejected = %v, want %v", err, got, test.rejected)
			}
			if (err != nil) != test.fails {
				t.Errorf("err = %v, want failure = %v", err, test.fails)
			}
		})
	}
}
