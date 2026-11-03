package bedrock

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/rstarc/elencode/internal/agent"
)

// streamEvent is one message of the binary event stream ConverseStream
// answers with: an event, or an exception the API reports once the stream is
// under way.
type streamEvent struct {
	kind      string // the event's type, such as contentBlockDelta
	exception string // set instead of kind for an exception, such as throttlingException
	payload   string // its JSON
}

// encodeEvent frames e the way AWS's event stream does: a prelude with the
// lengths, string headers, the payload, and a CRC32 over each part.
func encodeEvent(e streamEvent) []byte {
	headers := [][2]string{{":message-type", "event"}, {":event-type", e.kind}, {":content-type", "application/json"}}
	if e.exception != "" {
		headers = [][2]string{{":message-type", "exception"}, {":exception-type", e.exception}, {":content-type", "application/json"}}
	}
	var h bytes.Buffer
	for _, header := range headers {
		h.WriteByte(byte(len(header[0])))
		h.WriteString(header[0])
		h.WriteByte(7) // a string value
		_ = binary.Write(&h, binary.BigEndian, uint16(len(header[1])))
		h.WriteString(header[1])
	}

	var m bytes.Buffer
	total := 12 + h.Len() + len(e.payload) + 4
	_ = binary.Write(&m, binary.BigEndian, uint32(total))
	_ = binary.Write(&m, binary.BigEndian, uint32(h.Len()))
	_ = binary.Write(&m, binary.BigEndian, crc32.ChecksumIEEE(m.Bytes()))
	m.Write(h.Bytes())
	m.WriteString(e.payload)
	_ = binary.Write(&m, binary.BigEndian, crc32.ChecksumIEEE(m.Bytes()))
	return m.Bytes()
}

// capturedRequest is what the stub server was sent.
type capturedRequest struct {
	path          string
	authorization string
	body          map[string]any
}

// streamServer answers every request with events, and records the last
// request in captured.
func streamServer(t *testing.T, captured *capturedRequest, events ...streamEvent) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if captured != nil {
			captured.path = r.URL.Path
			captured.authorization = r.Header.Get("Authorization")
			captured.body = map[string]any{}
			if err := json.Unmarshal(body, &captured.body); err != nil {
				t.Errorf("request body is not JSON: %v: %s", err, body)
			}
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		for _, e := range events {
			_, _ = w.Write(encodeEvent(e))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// errorServer answers every request with an HTTP error, as Bedrock does
// before a stream starts.
func errorServer(t *testing.T, status int, errorType, message string, header http.Header) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, values := range header {
			w.Header()[name] = values
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-ErrorType", errorType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"message":"`+message+`"}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// clientFor is a client reaching server, in region.
func clientFor(server *httptest.Server, region string, thinking bool) *Client {
	return newWithOptions("ABSKkey", region, thinking, func(o *bedrockruntime.Options) {
		o.BaseEndpoint = &server.URL
		// A bearer token is only ever sent over TLS
		o.HTTPClient = server.Client()
	})
}

func collectEvents(t *testing.T, events <-chan agent.Event) []agent.Event {
	t.Helper()
	var got []agent.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, e)
		case <-timeout:
			t.Fatal("stream did not finish")
		}
	}
}

// lastEvent is the event that ended the turn.
func lastEvent(t *testing.T, events []agent.Event) agent.Event {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no events")
	}
	return events[len(events)-1]
}

func hello() []agent.Message {
	return []agent.Message{agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}})}
}

func TestStreamAssemblesAResponseFromTheEventStream(t *testing.T) {
	server := streamServer(t, nil,
		streamEvent{kind: "messageStart", payload: `{"role":"assistant"}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"text":"Let me "}}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"text":"look."}}`},
		streamEvent{kind: "contentBlockStop", payload: `{"contentBlockIndex":0}`},
		streamEvent{kind: "contentBlockStart", payload: `{"contentBlockIndex":1,"start":{"toolUse":{"toolUseId":"tooluse_1","name":"read"}}}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"{\"path\":"}}}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":1,"delta":{"toolUse":{"input":"\"a.txt\"}"}}}`},
		streamEvent{kind: "contentBlockStop", payload: `{"contentBlockIndex":1}`},
		streamEvent{kind: "messageStop", payload: `{"stopReason":"tool_use"}`},
		streamEvent{kind: "metadata", payload: `{"usage":{"inputTokens":3,"outputTokens":5,"totalTokens":8},"metrics":{"latencyMs":10}}`},
	)

	got := collectEvents(t, clientFor(server, "us-east-1", false).Stream(context.Background(), agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	}))

	var text string
	for _, e := range got {
		if delta, ok := e.(agent.TextDeltaEvent); ok {
			text += delta.Text
		}
	}
	if text != "Let me look." {
		t.Errorf("streamed text = %q, want %q", text, "Let me look.")
	}

	resp, ok := lastEvent(t, got).(agent.ResponseEvent)
	if !ok {
		t.Fatalf("last event = %#v, want a ResponseEvent", lastEvent(t, got))
	}
	if resp.Response.StopReason != agent.StopReasonToolUse {
		t.Errorf("stop reason = %q, want %q", resp.Response.StopReason, agent.StopReasonToolUse)
	}
	blocks := resp.Response.Message.Content
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v, want text and a tool use", blocks)
	}
	if blocks[0] != (agent.TextBlock{Text: "Let me look."}) {
		t.Errorf("blocks[0] = %#v, want the text", blocks[0])
	}
	toolUse, ok := blocks[1].(agent.ToolUseBlock)
	if !ok || toolUse.ID != "tooluse_1" || toolUse.Name != "read" || string(toolUse.Input) != `{"path":"a.txt"}` {
		t.Errorf("blocks[1] = %#v, want the read tool use with its input", blocks[1])
	}
}

func TestStreamSendsTheKeyAsABearerToken(t *testing.T) {
	var captured capturedRequest
	server := streamServer(t, &captured, streamEvent{kind: "messageStop", payload: `{"stopReason":"end_turn"}`})

	collectEvents(t, clientFor(server, "us-east-1", false).Stream(context.Background(), agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	}))

	if captured.authorization != "Bearer ABSKkey" {
		t.Errorf("Authorization = %q, want the key as a bearer token", captured.authorization)
	}
}

// The SDK reads AWS_BEARER_TOKEN_BEDROCK for itself. Which key a request
// carries is elencode's to say, through config.Env.
func TestTheEnvironmentDoesNotReachTheRequest(t *testing.T) {
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "ABSKfromtheenvironment")
	var captured capturedRequest
	server := streamServer(t, &captured, streamEvent{kind: "messageStop", payload: `{"stopReason":"end_turn"}`})

	collectEvents(t, clientFor(server, "us-east-1", false).Stream(context.Background(), agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	}))

	if captured.authorization != "Bearer ABSKkey" {
		t.Errorf("Authorization = %q, want the client's own key", captured.authorization)
	}
}

func TestAShippedModelRunsThroughTheProfileForTheRegion(t *testing.T) {
	cases := []struct {
		region string
		want   string
	}{
		{"us-east-1", "/model/us.anthropic.claude-sonnet-5/converse-stream"},
		{"us-west-2", "/model/us.anthropic.claude-sonnet-5/converse-stream"},
		{"eu-central-1", "/model/eu.anthropic.claude-sonnet-5/converse-stream"},
		{"ap-northeast-1", "/model/global.anthropic.claude-sonnet-5/converse-stream"},
	}
	for _, c := range cases {
		t.Run(c.region, func(t *testing.T) {
			var captured capturedRequest
			server := streamServer(t, &captured, streamEvent{kind: "messageStop", payload: `{"stopReason":"end_turn"}`})
			model, _ := agent.FindModel(Catalog(), "anthropic.claude-sonnet-5")

			collectEvents(t, clientFor(server, c.region, false).Stream(context.Background(), agent.Request{
				Model: model, MaxTokens: 100, Messages: hello(),
			}))

			if captured.path != c.want {
				t.Errorf("path = %q, want %q", captured.path, c.want)
			}
		})
	}
}

func TestAShippedModelWithoutAProfileRunsAsItIs(t *testing.T) {
	var captured capturedRequest
	server := streamServer(t, &captured, streamEvent{kind: "messageStop", payload: `{"stopReason":"end_turn"}`})
	model, _ := agent.FindModel(Catalog(), "openai.gpt-oss-120b-1:0")

	collectEvents(t, clientFor(server, "eu-central-1", false).Stream(context.Background(), agent.Request{
		Model: model, MaxTokens: 100, Messages: hello(),
	}))

	if want := "/model/openai.gpt-oss-120b-1:0/converse-stream"; captured.path != want {
		t.Errorf("path = %q, want %q", captured.path, want)
	}
}

func TestAModelNamedByHandIsSentAsWritten(t *testing.T) {
	for _, id := range []string{
		"global.anthropic.claude-sonnet-5",
		"arn:aws:bedrock:eu-central-1:123456789012:application-inference-profile/abc123",
	} {
		t.Run(id, func(t *testing.T) {
			var captured capturedRequest
			server := streamServer(t, &captured, streamEvent{kind: "messageStop", payload: `{"stopReason":"end_turn"}`})

			collectEvents(t, clientFor(server, "eu-central-1", false).Stream(context.Background(), agent.Request{
				Model: agent.Model{Provider: agent.ProviderBedrock, ID: id}, MaxTokens: 100, Messages: hello(),
			}))

			if want := "/model/" + id + "/converse-stream"; captured.path != want {
				t.Errorf("path = %q, want %q", captured.path, want)
			}
		})
	}
}

// requestBody streams req against a server that ends the turn at once, and
// returns the JSON the client sent.
func requestBody(t *testing.T, thinking bool, req agent.Request) map[string]any {
	t.Helper()
	var captured capturedRequest
	server := streamServer(t, &captured, streamEvent{kind: "messageStop", payload: `{"stopReason":"end_turn"}`})
	got := collectEvents(t, clientFor(server, "us-east-1", thinking).Stream(context.Background(), req))
	if errEvent, ok := lastEvent(t, got).(agent.ErrorEvent); ok {
		t.Fatalf("stream failed: %v", errEvent.Err)
	}
	return captured.body
}

// asJSON is v as compact JSON, to compare a part of a request body with.
func asJSON(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestStreamSendsTheConversation(t *testing.T) {
	redacted := base64.StdEncoding.EncodeToString([]byte("secret"))
	body := requestBody(t, false, agent.Request{
		Model:     agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"},
		MaxTokens: 100,
		Messages: []agent.Message{
			{Role: agent.RoleSystem, Content: []agent.Block{agent.TextBlock{Text: "be brief"}}},
			agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "read a.txt"}}),
			{Role: agent.RoleAssistant, Content: []agent.Block{
				agent.ThinkingBlock{Thinking: "I should read it", Signature: "sig"},
				agent.RedactedThinkingBlock{Data: redacted},
				agent.ToolUseBlock{ID: "tooluse_1", Name: "read", Input: json.RawMessage(`{"path":"a.txt"}`)},
			}},
			agent.NewUserMessage([]agent.Block{
				agent.NewToolResultBlock("tooluse_1", "contents", false),
			}),
			{Role: agent.RoleAssistant, Content: []agent.Block{
				agent.ToolUseBlock{ID: "tooluse_2", Name: "read", Input: json.RawMessage(`{"path":"b.txt"}`)},
			}},
			agent.NewUserMessage([]agent.Block{
				agent.NewToolResultBlock("tooluse_2", "no such file", true),
			}),
		},
	})

	if got, want := asJSON(t, body["system"]), `[{"text":"be brief"}]`; got != want {
		t.Errorf("system = %s, want %s", got, want)
	}
	if got, want := asJSON(t, body["inferenceConfig"]), `{"maxTokens":100}`; got != want {
		t.Errorf("inferenceConfig = %s, want %s", got, want)
	}
	want := `[` +
		`{"content":[{"text":"read a.txt"}],"role":"user"},` +
		`{"content":[` +
		`{"reasoningContent":{"reasoningText":{"signature":"sig","text":"I should read it"}}},` +
		`{"reasoningContent":{"redactedContent":"` + redacted + `"}},` +
		`{"toolUse":{"input":{"path":"a.txt"},"name":"read","toolUseId":"tooluse_1"}}` +
		`],"role":"assistant"},` +
		`{"content":[{"toolResult":{"content":[{"text":"contents"}],"status":"success","toolUseId":"tooluse_1"}}],"role":"user"},` +
		`{"content":[{"toolUse":{"input":{"path":"b.txt"},"name":"read","toolUseId":"tooluse_2"}}],"role":"assistant"},` +
		`{"content":[{"toolResult":{"content":[{"text":"no such file"}],"status":"error","toolUseId":"tooluse_2"}}],"role":"user"}` +
		`]`
	if got := asJSON(t, body["messages"]); got != want {
		t.Errorf("messages =\n%s\nwant\n%s", got, want)
	}
}

// Reasoning without a signature is what models other than Claude return.
// Nothing can verify it, and DeepSeek asks for it not to be sent back.
func TestUnsignedReasoningIsNotSentBack(t *testing.T) {
	body := requestBody(t, false, agent.Request{
		Model:     agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"},
		MaxTokens: 100,
		Messages: []agent.Message{
			agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hi"}}),
			{Role: agent.RoleAssistant, Content: []agent.Block{
				agent.ThinkingBlock{Thinking: "they said hi"},
				agent.TextBlock{Text: "hello"},
			}},
			agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "bye"}}),
		},
	})

	want := `[{"content":[{"text":"hi"}],"role":"user"},{"content":[{"text":"hello"}],"role":"assistant"},{"content":[{"text":"bye"}],"role":"user"}]`
	if got := asJSON(t, body["messages"]); got != want {
		t.Errorf("messages = %s, want %s", got, want)
	}
}

func TestStreamSendsTools(t *testing.T) {
	body := requestBody(t, false, agent.Request{
		Model:     agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"},
		MaxTokens: 100,
		Messages:  hello(),
		Tools: []agent.Tool{{
			Name:        "read",
			Description: "Reads a file",
			InputSchema: agent.InputSchema{
				Type:       "object",
				Properties: map[string]agent.Property{"path": {Type: "string", Description: "the file"}},
				Required:   []string{"path"},
			},
		}},
	})

	want := `{"tools":[{"toolSpec":{"description":"Reads a file","inputSchema":{"json":{` +
		`"properties":{"path":{"description":"the file","type":"string"}},"required":["path"],"type":"object"` +
		`}},"name":"read"}}]}`
	if got := asJSON(t, body["toolConfig"]); got != want {
		t.Errorf("toolConfig =\n%s\nwant\n%s", got, want)
	}
}

// Converse refuses a toolConfig with no tools in it.
func TestNoToolsSendsNoToolConfig(t *testing.T) {
	body := requestBody(t, false, agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	})

	if _, ok := body["toolConfig"]; ok {
		t.Errorf("toolConfig = %s, want none", asJSON(t, body["toolConfig"]))
	}
}

func TestReasoningIsAskedForTheWayTheModelTakesIt(t *testing.T) {
	cases := []struct {
		name     string
		model    string
		thinking bool
		effort   agent.Effort
		want     string // additionalModelRequestFields, or "null" for none
	}{
		{
			"claude at an effort", "anthropic.claude-opus-5", true, agent.EffortXHigh,
			`{"output_config":{"effort":"xhigh"},"thinking":{"display":"summarized","type":"adaptive"}}`,
		},
		{
			"claude at the API's own effort", "anthropic.claude-opus-5", true, agent.EffortNone,
			`{"thinking":{"display":"summarized","type":"adaptive"}}`,
		},
		{"claude with thinking off", "anthropic.claude-opus-5", false, agent.EffortHigh, `null`},
		{"gpt-oss at an effort", "openai.gpt-oss-120b-1:0", true, agent.EffortLow, `{"reasoning_effort":"low"}`},
		{"gpt-oss above its highest effort", "openai.gpt-oss-120b-1:0", true, agent.EffortMax, `{"reasoning_effort":"high"}`},
		{"gpt-oss at the API's own effort", "openai.gpt-oss-120b-1:0", true, agent.EffortNone, `null`},
		{"gpt-oss with thinking off", "openai.gpt-oss-120b-1:0", false, agent.EffortLow, `null`},
		{"a model whose reasoning cannot be set", "deepseek.r1-v1:0", true, agent.EffortHigh, `null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			model, ok := agent.FindModel(Catalog(), c.model)
			if !ok {
				t.Fatalf("%s is not in the catalog", c.model)
			}
			body := requestBody(t, c.thinking, agent.Request{Model: model, Effort: c.effort, MaxTokens: 100, Messages: hello()})

			if got := asJSON(t, body["additionalModelRequestFields"]); got != c.want {
				t.Errorf("additionalModelRequestFields = %s, want %s", got, c.want)
			}
		})
	}
}

func TestStreamKeepsTheModelsReasoning(t *testing.T) {
	server := streamServer(t, nil,
		streamEvent{kind: "messageStart", payload: `{"role":"assistant"}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"They said "}}}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"reasoningContent":{"text":"hi."}}}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"reasoningContent":{"signature":"sig"}}}`},
		streamEvent{kind: "contentBlockStop", payload: `{"contentBlockIndex":0}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":1,"delta":{"reasoningContent":{"redactedContent":"` + base64.StdEncoding.EncodeToString([]byte("secret")) + `"}}}`},
		streamEvent{kind: "contentBlockStop", payload: `{"contentBlockIndex":1}`},
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":2,"delta":{"text":"Hello"}}`},
		streamEvent{kind: "contentBlockStop", payload: `{"contentBlockIndex":2}`},
		streamEvent{kind: "messageStop", payload: `{"stopReason":"end_turn"}`},
	)

	got := collectEvents(t, clientFor(server, "us-east-1", true).Stream(context.Background(), agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	}))

	var thinking string
	for _, e := range got {
		if delta, ok := e.(agent.ThinkingDeltaEvent); ok {
			thinking += delta.Text
		}
	}
	if thinking != "They said hi." {
		t.Errorf("streamed thinking = %q, want %q", thinking, "They said hi.")
	}
	resp, ok := lastEvent(t, got).(agent.ResponseEvent)
	if !ok {
		t.Fatalf("last event = %#v, want a ResponseEvent", lastEvent(t, got))
	}
	want := []agent.Block{
		agent.ThinkingBlock{Thinking: "They said hi.", Signature: "sig"},
		agent.RedactedThinkingBlock{Data: base64.StdEncoding.EncodeToString([]byte("secret"))},
		agent.TextBlock{Text: "Hello"},
	}
	if got := resp.Response.Message.Content; asJSON(t, got) != asJSON(t, want) {
		t.Errorf("blocks = %#v, want %#v", got, want)
	}
}

func TestToStopReasonMapsEveryKnownReason(t *testing.T) {
	cases := map[string]agent.StopReason{
		"end_turn":                      agent.StopReasonEndTurn,
		"tool_use":                      agent.StopReasonToolUse,
		"max_tokens":                    agent.StopReasonMaxTokens,
		"stop_sequence":                 agent.StopReasonStopSequence,
		"guardrail_intervened":          agent.StopReasonRefusal,
		"content_filtered":              agent.StopReasonRefusal,
		"model_context_window_exceeded": agent.StopReasonMaxTokens,
	}
	for reason, want := range cases {
		got, err := toStopReason(types.StopReason(reason))
		if err != nil || got != want {
			t.Errorf("toStopReason(%q) = %q, %v; want %q", reason, got, err, want)
		}
	}
}

func TestToStopReasonRejectsUnknown(t *testing.T) {
	_, err := toStopReason("malformed_tool_use")

	if err == nil || !strings.Contains(err.Error(), "malformed_tool_use") {
		t.Errorf("err = %v, want one naming the stop reason", err)
	}
}

func TestStreamSurfacesATruncatedStream(t *testing.T) {
	server := streamServer(t, nil,
		streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"text":"Hel"}}`},
	)

	got := collectEvents(t, clientFor(server, "us-east-1", false).Stream(context.Background(), agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	}))

	if _, ok := lastEvent(t, got).(agent.ErrorEvent); !ok {
		t.Errorf("last event = %#v, want an ErrorEvent", lastEvent(t, got))
	}
}

// streamError runs one turn against server and returns the error it ended on.
func streamError(t *testing.T, server *httptest.Server) error {
	t.Helper()
	got := collectEvents(t, clientFor(server, "us-east-1", false).Stream(context.Background(), agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	}))
	errEvent, ok := lastEvent(t, got).(agent.ErrorEvent)
	if !ok {
		t.Fatalf("last event = %#v, want an ErrorEvent", lastEvent(t, got))
	}
	return errEvent.Err
}

func TestStreamMarksTransientFailuresRetryable(t *testing.T) {
	cases := []struct {
		status    int
		errorType string
	}{
		{http.StatusTooManyRequests, "ThrottlingException"},
		{http.StatusServiceUnavailable, "ServiceUnavailableException"},
		{http.StatusInternalServerError, "InternalServerException"},
		{http.StatusTooManyRequests, "ModelNotReadyException"},
	}
	for _, c := range cases {
		t.Run(c.errorType, func(t *testing.T) {
			err := streamError(t, errorServer(t, c.status, c.errorType, "try later", nil))

			var retryable *agent.RetryableError
			if !errors.As(err, &retryable) {
				t.Errorf("err = %v, want it marked retryable", err)
			}
		})
	}
}

func TestStreamReadsRetryAfter(t *testing.T) {
	err := streamError(t, errorServer(t, http.StatusTooManyRequests, "ThrottlingException", "slow down", http.Header{"Retry-After": {"7"}}))

	var retryable *agent.RetryableError
	if !errors.As(err, &retryable) || retryable.After != 7*time.Second {
		t.Errorf("err = %#v, want it retryable after 7s", err)
	}
}

func TestStreamLeavesPermanentFailuresUnmarked(t *testing.T) {
	err := streamError(t, errorServer(t, http.StatusForbidden, "AccessDeniedException",
		"You don't have access to the model with the specified model ID.", nil))

	var retryable *agent.RetryableError
	if errors.As(err, &retryable) {
		t.Errorf("err = %v, want it left unmarked", err)
	}
	if want := "bedrock: You don't have access to the model with the specified model ID. (AccessDeniedException)"; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

func TestStreamMarksAMidStreamThrottlingRetryable(t *testing.T) {
	err := streamError(t, streamServer(t, nil,
		streamEvent{kind: "messageStart", payload: `{"role":"assistant"}`},
		streamEvent{exception: "throttlingException", payload: `{"message":"too many tokens"}`},
	))

	var retryable *agent.RetryableError
	if !errors.As(err, &retryable) {
		t.Errorf("err = %v, want it marked retryable", err)
	}
}

func TestStreamMarksAConnectionFailureRetryable(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	server.Close()

	err := streamError(t, server)

	var retryable *agent.RetryableError
	if !errors.As(err, &retryable) {
		t.Errorf("err = %v, want it marked retryable", err)
	}
}

// The agent retries, and tells the UI it is doing so. The SDK retrying as well
// would hide the wait and multiply the attempts.
func TestClientLeavesRetryingToTheAgent(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("X-Amzn-ErrorType", "ThrottlingException")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"message":"slow down"}`)
	}))
	t.Cleanup(server.Close)

	_ = streamError(t, server)

	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
}

func TestStreamStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encodeEvent(streamEvent{kind: "contentBlockDelta", payload: `{"contentBlockIndex":0,"delta":{"text":"Hel"}}`}))
		w.(http.Flusher).Flush()
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	got := collectEvents(t, clientFor(server, "us-east-1", false).Stream(ctx, agent.Request{
		Model: agent.Model{Provider: agent.ProviderBedrock, ID: "some.model-v1:0"}, MaxTokens: 100, Messages: hello(),
	}))

	for _, e := range got {
		if errEvent, ok := e.(agent.ErrorEvent); ok {
			var retryable *agent.RetryableError
			if errors.As(errEvent.Err, &retryable) {
				t.Errorf("err = %v, want a cancelled turn left unmarked", errEvent.Err)
			}
		}
	}
}
