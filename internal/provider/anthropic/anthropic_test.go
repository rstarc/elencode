package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
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
