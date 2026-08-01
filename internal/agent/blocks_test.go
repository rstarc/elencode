package agent

import (
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

func TestRenderErrorIsLabelledAndBoxed(t *testing.T) {
	got := RenderError(errors.New("429 rate_limit_error"), 80)

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

func TestRenderMarksOnlyFirstLineWithAsterisk(t *testing.T) {
	// Force a wrap onto multiple lines so the marker can differ per line.
	long := errors.New(strings.Repeat("wraps to more than one line ", 10))
	got := RenderError(long, 30)

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

func TestRenderFitsWithinAvailableWidth(t *testing.T) {
	long := errors.New(strings.Repeat("something went wrong ", 20))

	for _, available := range []int{20, 40, 80, maxBlockWidth} {
		if got := widest(RenderError(long, available)); got > available {
			t.Errorf("RenderError at width %d rendered %d columns wide, want <= %d", available, got, available)
		}
		if got := widest(RenderStreamingText(long.Error(), available)); got > available {
			t.Errorf("RenderStreamingText at width %d rendered %d columns wide, want <= %d", available, got, available)
		}
	}
}

func TestRenderCapsAtMaxWidth(t *testing.T) {
	// Long lines are hard to read, so a wide terminal does not stretch a block
	// across the whole of it.
	got := widest(RenderError(errors.New(strings.Repeat("wide ", 100)), 500))

	if got > maxBlockWidth {
		t.Errorf("render is %d columns wide, want it capped at %d", got, maxBlockWidth)
	}
}

func TestRenderStaysReadableWhenWidthIsUnusable(t *testing.T) {
	// Below a handful of columns lipgloss ignores the width entirely, so a
	// terminal narrower than minBlockWidth (or a model that has not seen a
	// WindowSizeMsg yet, width 0) must not be passed straight through.
	for _, available := range []int{0, 1, 4, 7} {
		got := widest(RenderError(errors.New("boom"), available))
		if got > minBlockWidth {
			t.Errorf("render at width %d is %d columns wide, want <= %d", available, got, minBlockWidth)
		}
	}
}
