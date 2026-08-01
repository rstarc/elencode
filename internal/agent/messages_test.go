package agent

import (
	"strings"
	"testing"
)

func TestRenderMessageSkipsBlocksThatRenderEmpty(t *testing.T) {
	// ToolResultBlock deliberately renders to "" (raw tool output is not shown
	// in the transcript). It must not leave a blank line behind.
	msg := Message{Role: RoleAssistant, Content: []Block{
		TextBlock{Text: "hi"},
		NewToolResultBlock("id", "some output", false),
	}}

	got := renderMessage(msg, 80)

	if strings.Contains(got, "\n\n") {
		t.Errorf("render = %q, want no blank line left by the empty block", got)
	}
}

func TestRenderTranscriptSkipsMessagesThatRenderEmpty(t *testing.T) {
	a := New(nil, nil)
	a.contextWindow = []Message{
		{Role: RoleAssistant, Content: []Block{TextBlock{Text: "hi"}}},
		// A message made only of tool results renders empty entirely.
		{Role: RoleUser, Content: []Block{NewToolResultBlock("id", "some output", false)}},
		{Role: RoleAssistant, Content: []Block{TextBlock{Text: "bye"}}},
	}

	got := RenderTranscript(a, 80)

	if strings.Contains(got, "\n\n") {
		t.Errorf("render = %q, want no blank line left by the empty message", got)
	}
}
