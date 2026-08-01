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

func TestRenderUserMessageUsesPromptMarker(t *testing.T) {
	msg := NewUserMessage([]Block{TextBlock{Text: "hello"}})

	got := renderMessage(msg, 80)

	if !strings.HasPrefix(stripANSI(got), UserPromptMarker+" ") {
		t.Errorf("render = %q, want it to start with the input prompt marker %q", got, UserPromptMarker)
	}
}

func TestRenderUserMessageContinuesWithBorderRune(t *testing.T) {
	long := strings.Repeat("word ", 30)
	msg := NewUserMessage([]Block{TextBlock{Text: long}})

	got := renderMessage(msg, 30)

	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("render did not wrap to multiple lines: %q", got)
	}
	for i, line := range lines[1:] {
		if !strings.HasPrefix(stripANSI(line), markerRest+" ") {
			t.Errorf("line %d = %q, want it continued with the border rune", i+1, line)
		}
	}
}

func TestRenderUserMessageIsStyledDifferentlyFromAssistantText(t *testing.T) {
	userMsg := NewUserMessage([]Block{TextBlock{Text: "same text"}})
	assistantMsg := Message{Role: RoleAssistant, Content: []Block{TextBlock{Text: "same text"}}}

	userRendered := renderMessage(userMsg, 80)
	assistantRendered := renderMessage(assistantMsg, 80)

	if userRendered == assistantRendered {
		t.Errorf("user and assistant text rendered identically, want the user message set apart (e.g. background)")
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
