package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"encoding/json"
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
	msg := decodeMessage(t, `{"content":[{"type":"thinking","thinking":"hmm","signature":"sig"}]}`)

	_, err := toBlocks(msg)

	if err == nil {
		t.Fatal("err = nil, want an error for an unhandled block variant")
	}
	if !strings.Contains(err.Error(), "thinking") {
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

// TestNewUsesTheConfiguredModel and its sibling pin the two ways a model is
// chosen: the config file, or the built-in default when it says nothing.
func TestNewUsesTheConfiguredModel(t *testing.T) {
	c := New("key", "claude-opus-4-5")

	if got := string(c.model); got != "claude-opus-4-5" {
		t.Errorf("model = %q, want the configured one", got)
	}
}

func TestNewFallsBackToADefaultModel(t *testing.T) {
	c := New("key", "")

	if c.model == "" {
		t.Error("model is empty with none configured, want the default")
	}
}

func TestSetModelSwitchesTheModel(t *testing.T) {
	c := New("key", "claude-opus-4-5")

	c.SetModel("claude-haiku-4-5")

	if got := string(c.model); got != "claude-haiku-4-5" {
		t.Errorf("model = %q, want the one SetModel was given", got)
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

	c := newWithOptions("key", "", option.WithBaseURL(server.URL))
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
