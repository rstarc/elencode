package transcript

import (
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
)

// TestDocumentRendersEveryEntry covers what the document is for: it holds
// values, not the strings they rendered to, so a session can be laid out again.
func TestDocumentRendersEveryEntry(t *testing.T) {
	var doc Document
	doc.Append(HeaderEntry{Title: "elencode"})
	doc.Append(MessageEntry{Message: agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hello"}})})
	doc.Append(NoticeEntry{Text: "switched"})
	doc.Append(ErrorEntry{Err: errors.New("boom")})

	got := doc.Render(60)

	for _, want := range []string{"elencode", "hello", "switched", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("document is missing %q:\n%s", want, got)
		}
	}
}

// TestDocumentFollowsTheWidth is the whole point of holding entries as values:
// the same document laid out at a new width comes out at that width.
func TestDocumentFollowsTheWidth(t *testing.T) {
	var doc Document
	doc.Append(HeaderEntry{Title: "elencode"})

	for _, width := range []int{40, 100} {
		got := doc.Render(width)
		if lipgloss.Width(got) != width {
			t.Errorf("document at width %d is %d columns wide:\n%s", width, lipgloss.Width(got), got)
		}
	}
}

// TestEmptyDocumentRendersNothing keeps a blank line out of the transcript
// before anything has been said.
func TestEmptyDocumentRendersNothing(t *testing.T) {
	var doc Document

	if got := doc.Render(80); got != "" {
		t.Errorf("empty document rendered %q, want nothing", got)
	}
}

// TestDocumentDropsEmptyEntries covers a message whose blocks all render empty,
// such as one carrying only a tool result: it must not leave a blank line.
func TestDocumentDropsEmptyEntries(t *testing.T) {
	var doc Document
	doc.Append(NoticeEntry{Text: "first"})
	doc.Append(MessageEntry{Message: agent.NewUserMessage([]agent.Block{agent.ToolResultBlock{ToolUseID: "1"}})})
	doc.Append(NoticeEntry{Text: "second"})

	if got := doc.Render(40); strings.Contains(got, "\n\n") {
		t.Errorf("document has a blank line between entries:\n%q", got)
	}
}
