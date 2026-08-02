package transcript

import (
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rstarc/elencode/internal/agent"
)

// widest reports the width of the longest line, which is what decides whether
// the viewport has to clip.
func widest(render string) int {
	widest := 0
	for _, line := range strings.Split(render, "\n") {
		if w := lipgloss.Width(line); w > widest {
			widest = w
		}
	}
	return widest
}

func TestErrorIsLabelledAndBoxed(t *testing.T) {
	got := Error(errors.New("429 rate_limit_error"), 80)

	if !strings.Contains(got, "429 rate_limit_error") {
		t.Errorf("render = %q, want it to contain the error text", got)
	}
	// Labelled, so a failure is not mistaken for assistant output
	if !strings.Contains(got, "Error:") {
		t.Errorf("render = %q, want it to be labelled %q", got, "Error:")
	}
	// Marked like every other block, so it reads as part of the transcript
	if !strings.Contains(got, "*") {
		t.Errorf("render = %q, want it marked with the block-start asterisk", got)
	}
}

func TestMarksOnlyFirstLineWithAsterisk(t *testing.T) {
	// Force a wrap onto multiple lines so the marker can differ per line.
	long := errors.New(strings.Repeat("wraps to more than one line ", 10))
	got := Error(long, 30)

	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("render did not wrap to multiple lines: %q", got)
	}
	if !strings.HasPrefix(stripANSI(lines[0]), "*") {
		t.Errorf("first line = %q, want it to start with the block-start asterisk", lines[0])
	}
	for i, line := range lines[1:] {
		if strings.HasPrefix(stripANSI(line), "*") {
			t.Errorf("line %d = %q, want the asterisk only on the first line", i+1, line)
		}
		if !strings.HasPrefix(stripANSI(line), "│") {
			t.Errorf("line %d = %q, want it continued with the border rune", i+1, line)
		}
	}
}

// stripANSI removes escape sequences so prefix checks see the plain rune.
func stripANSI(s string) string {
	return ansi.Strip(s)
}

func TestFitsWithinAvailableWidth(t *testing.T) {
	long := errors.New(strings.Repeat("something went wrong ", 20))

	for _, available := range []int{20, 40, 80, 200} {
		if got := widest(Error(long, available)); got > available {
			t.Errorf("RenderError at width %d rendered %d columns wide, want <= %d", available, got, available)
		}
		if got := widest(Block(agent.TextBlock{Text: long.Error()}, agent.RoleAssistant, available)); got > available {
			t.Errorf("a text block at width %d rendered %d columns wide, want <= %d", available, got, available)
		}
	}
}

// TestRenderUsesTheWholeTerminal covers a wide terminal: a block fills the
// width it is given rather than stopping at a fixed column, so the transcript
// is laid out to the terminal the user chose.
func TestUsesTheWholeTerminal(t *testing.T) {
	const available = 200

	got := widest(Error(errors.New(strings.Repeat("wide ", 100)), available))

	if got != available {
		t.Errorf("render is %d columns wide, want the full %d", got, available)
	}
}

func TestStaysReadableWhenWidthIsUnusable(t *testing.T) {
	// Below a handful of columns lipgloss ignores the width entirely, so a
	// terminal narrower than minBlockWidth (or a model that has not seen a
	// WindowSizeMsg yet, width 0) must not be passed straight through.
	for _, available := range []int{0, 1, 4, 7} {
		got := widest(Error(errors.New("boom"), available))
		if got > minBlockWidth {
			t.Errorf("render at width %d is %d columns wide, want <= %d", available, got, minBlockWidth)
		}
	}
}

func TestToolUseFormatsArguments(t *testing.T) {
	got := Block(agent.ToolUseBlock{
		Name:  "read",
		Input: []byte(`{"path":"README.md","offset":10}`),
	}, agent.RoleAssistant, 80)

	plain := stripANSI(got)
	if !strings.Contains(plain, "read(README.md,offset=10)") {
		t.Errorf("tool use = %q, want formatted arguments", got)
	}
}

func TestToolUseBoldsName(t *testing.T) {
	got := Block(agent.ToolUseBlock{
		Name:  "read",
		Input: []byte(`{"path":"README.md"}`),
	}, agent.RoleAssistant, 80)

	bold := lipgloss.NewStyle().Foreground(textBlockColor).Bold(true).Render("read")
	if !strings.Contains(got, bold) {
		t.Errorf("tool use = %q, want the tool name bold", got)
	}
}

func TestToolUseUsesBrightBlackText(t *testing.T) {
	got := Block(agent.ToolUseBlock{
		Name:  "read",
		Input: []byte(`{"path":"README.md"}`),
	}, agent.RoleAssistant, 80)

	brightBlack, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(textBlockColor).Render("x"), "x")
	if !strings.Contains(got, brightBlack+"(README.md)") {
		t.Errorf("tool use = %q, want BrightBlack text", got)
	}
}

// TestRenderNoticeReadsAsNeitherSide covers what a notice is for: it reports
// something the program did, so it must not look like a message from the user
// or the assistant.
func TestNoticeReadsAsNeitherSide(t *testing.T) {
	got := stripANSI(Notice("switched to some-model", 80))

	if !strings.Contains(got, "switched to some-model") {
		t.Errorf("notice = %q, want it to carry the text", got)
	}
	for _, marker := range []string{markerFirst, markerRest, UserPromptMarker} {
		if strings.HasPrefix(got, marker+" ") {
			t.Errorf("notice = %q, want it not marked like a message block (%q)", got, marker)
		}
	}
}

func TestNoticeIsOneLine(t *testing.T) {
	if got := Notice("switched to some-model", 80); strings.Contains(got, "\n") {
		t.Errorf("notice = %q, want a single line", got)
	}
}

func TestNoticeFitsNarrowTerminal(t *testing.T) {
	for _, width := range []int{10, 20, 40, 80} {
		got := Notice("switched to a model with a rather long name", width)
		if lipgloss.Width(got) > max(width, minBlockWidth) {
			t.Errorf("notice at width %d is %d columns wide:\n%s", width, lipgloss.Width(got), got)
		}
	}
}

// TestRenderNoticeSpansTheTerminal covers the point of drawing a notice as a
// rule: it separates what is above it from what is below, which it can only do
// by reaching both edges.
func TestNoticeSpansTheTerminal(t *testing.T) {
	for _, width := range []int{40, 80, 120, 200} {
		if got := lipgloss.Width(Notice("switched to some-model", width)); got != width {
			t.Errorf("notice at width %d is %d columns wide, want the full %d", width, got, width)
		}
	}
}

func TestHeaderSpansTheTerminal(t *testing.T) {
	for _, width := range []int{40, 120, 200} {
		if got := lipgloss.Width(Header("elencode", width)); got != width {
			t.Errorf("header at width %d is %d columns wide, want the full %d", width, got, width)
		}
	}
}

func TestHeaderCarriesTheTitle(t *testing.T) {
	if got := stripANSI(Header("elencode", 80)); !strings.Contains(got, "elencode") {
		t.Errorf("header = %q, want it to carry the title", got)
	}
}

// TestRenderThinkingIsLaidOutLikeAssistantText pins the "same block, different
// styling" part: both wrap in the marker column, and only the styling differs.
func TestThinkingIsLaidOutLikeAssistantText(t *testing.T) {
	const same = "a sentence long enough to wrap onto a second row when the terminal is narrow"

	thinking := Block(agent.ThinkingBlock{Thinking: same}, agent.RoleAssistant, 40)
	text := Block(agent.TextBlock{Text: same}, agent.RoleAssistant, 40)

	if len(unmarked(thinking)) < 3 || len(unmarked(text)) < 2 {
		t.Errorf("blocks did not wrap at the terminal width:\nthinking:\n%s\ntext:\n%s", thinking, text)
	}
	if thinking == text {
		t.Error("thinking is styled the same as assistant text, want it set apart")
	}
}

// unmarked returns a block's lines with the marker column and styling removed,
// leaving what the text was wrapped to.
func unmarked(rendered string) []string {
	var lines []string
	for _, line := range strings.Split(stripANSI(rendered), "\n") {
		// The marker is one rune and a space, so the content starts after the
		// first space. Cutting avoids counting bytes in a multi-byte marker.
		_, content, _ := strings.Cut(line, " ")
		lines = append(lines, strings.TrimRight(content, " "))
	}
	return lines
}

func TestThinkingIsItalic(t *testing.T) {
	got := Block(agent.ThinkingBlock{Thinking: "wondering"}, agent.RoleAssistant, 80)

	want, _, _ := strings.Cut(lipgloss.NewStyle().Italic(true).Foreground(thinkingColor).Render("x"), "x")
	if !strings.Contains(got, want) {
		t.Errorf("thinking is not rendered italic and dim: %q, want the styling %q", got, want)
	}
}

func TestThinkingIsTitled(t *testing.T) {
	const width = 80

	lines := strings.Split(stripANSI(Block(agent.ThinkingBlock{Thinking: "wondering"}, agent.RoleAssistant, width)), "\n")

	if len(lines) != 2 {
		t.Fatalf("thinking rendered %d lines, want a title line and a line of content:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	// Right aligned: the title ends at the right edge, so the line fills the width
	if !strings.HasSuffix(lines[0], thinkingTitle) {
		t.Errorf("first line = %q, want it to end with the title %q", lines[0], thinkingTitle)
	}
	if got := lipgloss.Width(lines[0]); got != width {
		t.Errorf("title line is %d columns wide, want %d so the title sits at the right edge", got, width)
	}
	if !strings.Contains(lines[1], "wondering") {
		t.Errorf("second line = %q, want the reasoning below the title", lines[1])
	}
}

// TestRenderRedactedThinkingIsAPlaceholder covers a block with nothing to show:
// the reasoning is encrypted, so the title is the whole of it.
func TestRedactedThinkingIsAPlaceholder(t *testing.T) {
	const width = 80

	got := Block(agent.RedactedThinkingBlock{Data: "encrypted-payload"}, agent.RoleAssistant, width)

	lines := strings.Split(stripANSI(got), "\n")
	if len(lines) != 1 {
		t.Fatalf("redacted thinking rendered %d lines, want just the title:\n%s", len(lines), got)
	}
	if !strings.HasSuffix(lines[0], redactedThinkingTitle) {
		t.Errorf("line = %q, want it to end with %q", lines[0], redactedThinkingTitle)
	}
	if strings.Contains(got, "encrypted-payload") {
		t.Errorf("the encrypted payload is on screen, want only the placeholder:\n%s", got)
	}
}

func TestRedactedThinkingIsStyledLikeThinking(t *testing.T) {
	redacted := Block(agent.RedactedThinkingBlock{Data: "x"}, agent.RoleAssistant, 80)
	thinking := Block(agent.ThinkingBlock{Thinking: ""}, agent.RoleAssistant, 80)

	// Same styling, differing only in which title they carry
	if styleOf(t, redacted) != styleOf(t, thinking) {
		t.Errorf("redacted thinking is styled differently:\n%q\n%q", redacted, thinking)
	}
}

// styleOf returns the escape sequences a rendered block opens with, so two
// blocks can be compared on styling rather than on content.
func styleOf(t *testing.T, rendered string) string {
	t.Helper()

	var escapes []string
	for _, part := range strings.Split(rendered, "\x1b") {
		if i := strings.Index(part, "m"); i >= 0 && strings.HasPrefix(part, "[") {
			escapes = append(escapes, part[:i+1])
		}
	}
	return strings.Join(escapes, ",")
}

func TestMessageSkipsBlocksThatRenderEmpty(t *testing.T) {
	// ToolResultBlock deliberately renders to "" (raw tool output is not shown
	// in the transcript). It must not leave a blank line behind.
	msg := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{
		agent.TextBlock{Text: "hi"},
		agent.NewToolResultBlock("id", "some output", false),
	}}

	got := Message(msg, 80)

	if strings.Contains(got, "\n\n") {
		t.Errorf("render = %q, want no blank line left by the empty block", got)
	}
}

func TestUserMessageUsesPromptMarker(t *testing.T) {
	msg := agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "hello"}})

	got := Message(msg, 80)

	if !strings.HasPrefix(stripANSI(got), UserPromptMarker+" ") {
		t.Errorf("render = %q, want it to start with the input prompt marker %q", got, UserPromptMarker)
	}
}

func TestUserMessageContinuesWithBorderRune(t *testing.T) {
	long := strings.Repeat("word ", 30)
	msg := agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: long}})

	got := Message(msg, 30)

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

func TestUserMessageIsStyledDifferentlyFromAssistantText(t *testing.T) {
	userMsg := agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: "same text"}})
	assistantMsg := agent.Message{Role: agent.RoleAssistant, Content: []agent.Block{agent.TextBlock{Text: "same text"}}}

	userRendered := Message(userMsg, 80)
	assistantRendered := Message(assistantMsg, 80)

	if userRendered == assistantRendered {
		t.Errorf("user and assistant text rendered identically, want the user message set apart (e.g. background)")
	}
}
